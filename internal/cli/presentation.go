package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/helpview"
	"github.com/mattn/go-isatty"
)

func writerIsTerminal(w io.Writer) bool {
	fd, ok := w.(interface{ Fd() uintptr })
	return ok && (isatty.IsTerminal(fd.Fd()) || isatty.IsCygwinTerminal(fd.Fd()))
}

func resultObject(result any) map[string]any {
	b, _ := json.Marshal(result)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func renderHumanSuccess(command string, o options, profile string, result any) string {
	m := resultObject(result)
	var out strings.Builder
	if command == "version" {
		fmt.Fprintf(&out, "CLI 版本：%s\n协议版本：%d\n", domain.Version, domain.ProtocolVersion)
		if source, ok := m["source"].(map[string]any); ok {
			fmt.Fprintf(&out, "源码提交：%v\n来源状态：%v\n", source["commit"], source["sourceState"])
		}
		out.WriteString("详细来源：yss version --json")
		return out.String()
	}
	title := "查询完成"
	if command == "lifecycle" && len(o.args) > 1 && o.args[1] == "route" {
		title = "路由判定完成"
	}
	plan := o.values["plan"] == "true" || m["digest"] != nil && m["changes"] != nil || m["kind"] == "stage-tracking-go-plan" || len(o.args) > 1 && o.args[1] == "plan"
	if plan {
		title = "计划已生成"
	}
	if o.values["apply"] == "true" || len(o.args) > 1 && o.args[1] == "apply" || command == "init" && o.values["plan"] != "true" {
		title = "操作已完成"
	}
	if m["status"] == "passed" && (strings.Contains(strings.Join(o.args, " "), "verify") || command == "project-ci") {
		title = "验证通过"
	}
	if command == "recover" || command == "rollback" || len(o.args) > 1 && (o.args[1] == "recover" || o.args[1] == "rollback") {
		title = "事务状态查询完成"
		if o.values["apply"] == "true" || command == "update" || command == "migrate" {
			title = "事务操作已完成"
		}
	}
	fmt.Fprintf(&out, "%s：yss %s\n", title, strings.Join(o.args, " "))
	if profile != "" {
		fmt.Fprintf(&out, "Profile：%s\n", profile)
	}
	if file := o.values["out"]; file != "" {
		fmt.Fprintf(&out, "输出路径：%s\n", file)
	} else if plan {
		out.WriteString("计划保存：未保存；原始计划需使用 --out，或按该命令帮助重定向保存。\n")
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := m[k]
		if k == "profile_guidance" {
			renderProfileGuidance(&out, v)
			continue
		}
		if k == "progression" {
			renderProgression(&out, v)
			continue
		}
		if k == "coordination" {
			renderCoordination(&out, v)
			continue
		}
		if k == "next_action" {
			a, _ := v.(map[string]any)
			fmt.Fprintf(&out, "下一步：%v；承接 %v，工程 %v，工作单元 %v\n", a["kind"], a["profile"], a["root"], a["work_unit"])
			if reason := stringValue(a["reason"], ""); reason != "" {
				fmt.Fprintf(&out, "  %s\n", reason)
			}
			continue
		}
		if k == "checks" || k == "diagnostics" || k == "diagnostic" {
			continue
		}
		if k == "changes" || k == "conflicts" || k == "blockers" {
			if rows, ok := v.([]any); ok {
				fmt.Fprintf(&out, "%s：%d 项\n", humanLabel(k), len(rows))
				for i, row := range rows {
					if i >= 12 {
						out.WriteString("  更多条目见 --json\n")
						break
					}
					if fields, ok := row.(map[string]any); ok {
						if target, ok := fields["target_root"].(string); ok {
							fmt.Fprintf(&out, "  %v：%s → %s\n", fields["profile"], stringValue(fields["previous_root"], "未关联"), target)
							continue
						}
						fmt.Fprintf(&out, "  %s", firstText(fields, "path", "ref", "id", "code"))
						if state := fields["status"]; state != nil {
							fmt.Fprintf(&out, "：%v", state)
						}
						out.WriteByte('\n')
					} else {
						fmt.Fprintf(&out, "  %v\n", row)
					}
				}
				continue
			}
		}
		if k == "plan" || k == "context" {
			if nested, ok := v.(map[string]any); ok {
				fmt.Fprintf(&out, "%s：\n", humanLabel(k))
				renderReport(&out, nested)
				for _, key := range []string{"stats", "readyToApply", "status", "templateCommit", "blockers"} {
					if val := nested[key]; val != nil {
						fmt.Fprintf(&out, "  %s：%s\n", humanLabel(key), compactValue(val))
					}
				}
				continue
			}
		}
		fmt.Fprintf(&out, "%s：%s\n", humanLabel(k), compactValue(v))
	}
	renderReport(&out, m)
	if m == nil {
		b, _ := json.Marshal(result)
		fmt.Fprintf(&out, "查询结果：%s\n", b)
	}
	if plan && o.values["out"] != "" {
		argv := savedPlanApplyCommand(o, profile, m)
		fmt.Fprintf(&out, "下一步：检查计划与阻断项，满足应用条件后执行\n  %s", formatArgv(argv))
	} else if command == "update" || command == "upgrade" {
		toolRoot, _ := m["toolRoot"].(string)
		if toolRoot == "" {
			toolRoot = o.values["tool-root"]
		}
		if toolRoot != "" {
			fmt.Fprintf(&out, "下一步：核对程序安装与事务状态\n  %s", formatArgv([]string{"yss", "update", "status", "--tool-root", toolRoot, "--json"}))
		} else {
			fmt.Fprintf(&out, "下一步：%s\n  %s", nextReading(strings.Join(o.args, " ")), helpCommand(o.args))
		}
	} else if command == "init" && !plan {
		root := stringValue(m["root"], o.values["root"])
		if view, err := helpview.Load(profile); err == nil {
			fmt.Fprintf(&out, "下一步：进入本 Profile 的已登记入口\n  %s", formatArgv([]string{"yss", "lifecycle", "query", "--root", root, "--id", view.EntryWorkUnit, "--json"}))
		} else {
			fmt.Fprintf(&out, "下一步：%s", helpCommand([]string{"lifecycle", "query"}))
		}
	} else if o.values["apply"] == "true" {
		fmt.Fprintf(&out, "下一步：核对实际身份与变更\n  %s", formatArgv([]string{"yss", "doctor", "--root", stringValue(m["root"], o.values["root"]), "--json"}))
	} else {
		fmt.Fprintf(&out, "下一步：%s\n  %s", nextReading(strings.Join(o.args, " ")), helpCommand(o.args))
	}
	return out.String()
}

func renderProgression(out *strings.Builder, value any) {
	p, _ := value.(map[string]any)
	labels := map[string]string{"spec-approved": "Spec 已批准", "product-design-completed": "产品设计完成", "backend-deliverable": "后端可交付", "frontend-accepted": "前端接收完成", "business-accepted": "业务验收完成", "profile-terminal": "本 Profile 交付终点", "pending": "待完成", "reached": "已达到", "not-applicable": "不适用（已核验）", "not-evaluated": "待核验", "blocked": "受阻", "unsupported": "当前实例不支持"}
	label := func(v any) string {
		s := fmt.Sprint(v)
		if name := labels[s]; name != "" {
			return name
		}
		return s
	}
	fmt.Fprintf(out, "推进目标：%s；%s\n", label(p["target"]), label(p["status"]))
	if reason := stringValue(p["reason"], ""); reason != "" {
		fmt.Fprintf(out, "  %s\n", reason)
	}
	completion, _ := p["completion"].(map[string]any)
	for _, pair := range [][2]string{{"stage", "阶段"}, {"profile", "Profile"}, {"business", "业务"}} {
		row, _ := completion[pair[0]].(map[string]any)
		if row != nil {
			fmt.Fprintf(out, "  %s完成：%s\n", pair[1], label(row["status"]))
		}
	}
}

func renderCoordination(out *strings.Builder, value any) {
	rows, _ := value.(map[string]any)
	for _, profile := range []string{"design", "backend", "frontend"} {
		row, _ := rows[profile].(map[string]any)
		if row == nil {
			continue
		}
		fmt.Fprintf(out, "专职承接 %s：输入 %v；本端完成 %v；%v\n", profile, row["input_status"], row["completion_status"], row["root"])
		if reason := stringValue(row["reason"], ""); reason != "" {
			fmt.Fprintf(out, "  %s\n", reason)
		}
	}
}

func renderProfileGuidance(out *strings.Builder, value any) {
	g, ok := value.(map[string]any)
	if !ok {
		return
	}
	fmt.Fprintf(out, "Profile 引导：%s\n", compactValue(g["status"]))
	if reason, _ := g["reason"].(string); reason != "" {
		fmt.Fprintf(out, "  %s\n", reason)
	}
	if g["default"] == "continue-current-spec" {
		out.WriteString("  默认：在当前 Spec 继续已登记工作；独立 Design 可按适用范围选择。\n")
	}
	defaultCommands, _ := g["default_commands"].([]any)
	for _, command := range defaultCommands {
		parts, _ := command.([]any)
		argv := []string{}
		for _, part := range parts {
			argv = append(argv, fmt.Sprint(part))
		}
		fmt.Fprintf(out, "    %s\n", formatArgv(argv))
	}
	labels := map[string]string{"unlinked": "未关联", "not-initialized": "未初始化", "initialized": "已初始化", "needs-attention": "需处理", "pending-verification": "待核验", "waiting-input": "等待输入", "verified": "已核验", "blocked": "受阻", "required": "推荐", "optional": "可选", "not-applicable": "不适用"}
	label := func(v any) string {
		text, _ := v.(string)
		if translated, ok := labels[text]; ok {
			return translated
		}
		return text
	}
	recommendations, _ := g["recommendations"].([]any)
	for _, value := range recommendations {
		r, _ := value.(map[string]any)
		fmt.Fprintf(out, "  %v：%s；工程%s；输入%s\n", r["profile"], label(r["recommendation"]), label(r["engineering_status"]), label(r["input_status"]))
		if root, _ := r["target_root"].(string); root != "" {
			fmt.Fprintf(out, "    目标：%s\n", root)
		}
		if basis, _ := r["basis"].(string); basis != "" {
			fmt.Fprintf(out, "    依据：%s\n", basis)
		}
		missingItems, _ := r["missing"].([]any)
		for _, missing := range missingItems {
			fmt.Fprintf(out, "    待处理：%v\n", missing)
		}
		commands, _ := r["commands"].([]any)
		for _, command := range commands {
			argv := []string{}
			parts, _ := command.([]any)
			for _, part := range parts {
				argv = append(argv, fmt.Sprint(part))
			}
			fmt.Fprintf(out, "    %s\n", formatArgv(argv))
		}
	}
	missingItems, _ := g["missing"].([]any)
	for _, missing := range missingItems {
		fmt.Fprintf(out, "  待处理：%v\n", missing)
	}
	out.WriteString("  建议不授予实施或批准权限。\n")
}

func savedPlanApplyCommand(o options, profile string, result map[string]any) []string {
	command := o.args[0]
	root := o.values["root"]
	if root == "" {
		root = o.values["target-dir"]
	}
	root = stringValue(result["root"], root)
	// Explicit user scope is authoritative for the generated invocation.
	if value := o.values["root"]; value != "" {
		root = value
	}
	argv := append([]string{"yss"}, o.args...)
	if command == "update" || command == "upgrade" {
		return []string{"yss", "update", "apply", "--tool-root", stringValue(result["toolRoot"], o.values["tool-root"]), "--plan-file", o.values["out"], "--json"}
	}
	if command == "migrate" || command == "stage" {
		argv = []string{"yss", command, "apply", "--root", root, "--plan-file", o.values["out"], "--json"}
	} else {
		argv = append(argv, "--root", root, "--apply", "--plan-file", o.values["out"], "--json")
	}
	if command == "init" || o.values["profile"] != "" {
		argv = append(argv, "--profile", profile)
	}
	if command == "handoff" && o.values["kind"] != "" {
		argv = append(argv, "--kind", o.values["kind"])
	}
	return argv
}

func stringValue(value any, fallback string) string {
	if s, ok := value.(string); ok && s != "" {
		return s
	}
	if fallback != "" {
		return fallback
	}
	return "."
}

func compactValue(value any) string {
	if value == true {
		return "是"
	}
	if value == false {
		return "否"
	}
	b, _ := json.Marshal(value)
	s := strings.Trim(string(b), "\"")
	if len([]rune(s)) > 320 {
		s = string([]rune(s)[:320]) + "…（完整内容见 --json）"
	}
	return s
}

func humanLabel(key string) string {
	labels := map[string]string{"status": "当前状态", "delivery_path": "交付路径", "message": "说明", "reason": "原因", "root": "项目目录", "toolRoot": "程序目录", "tool-root": "程序目录", "file": "输入文件", "checkpoint": "Checkpoint", "plan-file": "计划文件", "task": "任务记录", "artifact": "发行文件", "implementation-root": "实现仓目录", "command": "命令", "profile": "Profile", "path": "受影响路径", "operation": "文件操作", "environment": "运行环境", "cliVersion": "CLI 版本", "checks": "检查", "diagnostics": "阻断与诊断", "coverage": "覆盖范围", "approval_created": "是否创建批准", "execution_authorization": "执行授权状态", "read_only": "是否只读", "kind": "类型", "files": "相关文件", "changes": "变更", "conflicts": "冲突", "blockers": "阻断项", "plan": "计划", "context": "Context 核验", "identity": "项目身份", "stats": "统计", "readyToApply": "是否满足应用条件", "digest": "输入摘要", "scope": "适用范围", "transaction": "事务", "transactionKind": "事务类型", "transactionId": "事务标识", "operations": "操作数量", "pending": "未完成事务", "templateCommit": "实例模板提交", "currentTemplateCommit": "当前模板提交", "introducedVersion": "能力引入版本", "metadataSchema": "实例 Schema", "instanceCLI": "实例 CLI 版本", "instanceProtocol": "实例协议版本", "metadataFile": "实例元数据", "templateVersion": "模板版本", "version": "版本", "installationConsistent": "安装是否一致", "recordedVersion": "记录版本", "runningVersion": "运行版本", "unchanged": "是否无变更", "sourceState": "来源状态", "source": "固定来源", "phase": "失败环节", "httpStatus": "HTTP 状态"}
	if label, ok := labels[key]; ok {
		return label
	}
	return key
}
