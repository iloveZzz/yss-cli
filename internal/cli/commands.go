package cli

import (
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/width"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// Command metadata supplies help and preflight, while the existing executors
// retain required inputs, authority checks and transaction guards.
type argumentSpec struct {
	placeholder, description string
	boolean                  bool
	choices                  []string
}

var argumentSpecs = map[string]argumentSpec{}
var projectOptionNames = strings.Fields("root target-dir profile json help version project-name business-domain team-size plan out apply plan-file binding-file full issue-tracker")
var commonOptionNames = []string{"help", "json", "version", "human", "diagnostics"}

func initializeArguments() {
	// Every flag accepted by the existing dispatchers has a registered type.
	for _, key := range strings.Fields("root target-dir asset asset-ref backend-root tool-root out output source artifact plan-file binding-file checkpoint file schema snapshot package items item task requirements expected binding implementation-root template-checkout cli-source report-dir home run-dir") {
		argumentSpecs[key] = argumentSpec{placeholder: "<路径>", description: "文件或目录路径"}
	}
	for _, key := range strings.Fields("profile project-name business-domain team-size issue-tracker installation sha256 to id term-refs allowed-context-ids work-unit stage kind gate boundary consumer approval-ref task-ref unit slice phase scope current-work-unit next-work-unit provider branch additional-path base runtime-store input token reason type value status exit-code native view") {
		argumentSpecs[key] = argumentSpec{placeholder: "<值>", description: "按当前命令说明指定"}
	}
	for _, key := range strings.Fields("json human diagnostics plan apply help version check required all dry-run include-example-docs force history require-approved continuation from-record allow-template recover full no-reconciliation source-context-snapshot prepare-inputs changed refresh") {
		argumentSpecs[key] = argumentSpec{description: "布尔开关，可使用 =true 或 =false；默认 false", boolean: true}
	}
	defineArgument("installation", "<方式>", "新建或显式迁移的安装方式，已有项目同步保持原方式", "skills", "governed")
	defineArgument("profile", "<Profile>", "Spec 或 Backend；项目命令可从身份检测", "spec", "backend")
	defineArgument("root", "<目录>", "项目根，默认当前目录；仅项目命令")
	defineArgument("target-dir", "<目录>", "项目根的兼容参数；优先使用 --root")
	defineArgument("tool-root", "<目录>", "程序安装目录；update 必需，upgrade 默认识别运行目录")
	defineArgument("out", "<新路径>", "计划文件或导出目录；目标须不存在，按命令选择")
	defineArgument("plan-file", "<文件>", "消费已有计划；写入前核验输入摘要")
	defineArgument("review-out", "<新目录>", "在项目外导出冲突、基线、合并候选和决议模板")
	defineArgument("base-bundle", "<路径>", "显式提供完整历史 Bundle 材料；默认离线")
	defineArgument("resolution-file", "<文件>", "结合 --plan-file 原计划重新规划已决议资产")
	defineArgument("artifact", "<归档>", "本机平台 .tar.gz 或 .zip 发行包")
	defineArgument("sha256", "<SHA-256>", "发行包的 64 位十六进制 SHA-256")
	defineArgument("to", "<稳定版本>", "在线升级目标，如 1.3.0 或 v1.3.0；默认最新稳定版")
	defineArgument("issue-tracker", "<追踪器>", "Spec Tracker 配置", "local-markdown", "github", "gitlab")
	defineArgument("provider", "<提供方>", "CI 提供方，默认 github", "github")
	defineArgument("base", "<完整SHA>", "已确认实现仓的完整 Git 基线")
	defineArgument("exit-code", "<整数>", "实际退出码；成功状态必须为 0")
	defineArgument("value", "<JSON>", "单个 JSON 值，默认 null")
	defineArgument("asset", "<路径>", "发生变化的项目内资产路径")
	argumentSpecs["all"] = argumentSpec{description: "对策略登记的全部对象逐个执行；与指定单个对象互斥", boolean: true}
	argumentSpecs["fetch"] = argumentSpec{description: "tracker status 读取链接 Issue 的远端标签；只读", boolean: true}
	defineArgument("ticket", "<文件>", "tracker 根下的 parent-ticket.md 或 issues/*.md")
	defineArgument("issue", "<owner/repo#编号>", "要链接的 GitHub Issue")
	defineArgument("feature", "<功能包>", "tracker 根下的功能包名")
	defineArgument("html", "<新文件>", "trace 另写的静态矩阵页；须在项目外且不存在")
	argumentSpecs["strict"] = argumentSpec{description: "trace 存在缺口时以 TRACE_GAPS 失败", boolean: true}
	argumentSpecs["changed"] = argumentSpec{description: "yss check 选择工作树相对 HEAD 的改动与未跟踪文件", boolean: true}
	argumentSpecs["dry-run"] = argumentSpec{description: "只返回将写的文件与摘要，不写任何文件", boolean: true}
	argumentSpecs["required"] = argumentSpec{description: "即使 managed 策略未启用也按阅读视图要求核验", boolean: true}
	defineArgument("view", "<视图>", "显式阅读模式；未指定时保持原查询输出")
	defineArgument("mode", "<模式>", "业务票报告模式；默认 draft", "draft", "formal")
	defineArgument("review-ref", "<路径>", "业务票 formal 报告的实际审查来源")
	defineArgument("kind", "<类型>", "当前命令的领域类型；runtime begin 默认 command")
	defineArgument("status", "<终态>", "运行记录的终态；成功状态必须匹配退出码 0")
	defineArgument("project-name", "<名称>", "项目名称，作为模板变量")
	defineArgument("business-domain", "<领域>", "项目业务领域，作为模板变量")
	defineArgument("team-size", "<规模>", "团队规模，作为模板变量")
	argumentSpecs["details"] = argumentSpec{description: "返回注册信息和安装声明；不核验整个技能库", boolean: true}
	defineArgument("agent-runtime", "<运行时>", "显式核验所选宿主投影；省略时仅核验 canonical，文件就绪不代表宿主已发现", "claude", "codex", "cursor", "pi", "workbuddy")
	defineArgument("when", "<条件列表>", "逗号分隔的已登记条件；只展开命中的条件依赖")
	defineArgument("since", "<Git引用>", "git audit/changelog 的起始引用（不含）")
	defineArgument("until", "<Git引用>", "git audit/changelog 的结束引用，默认 HEAD")
	defineArgument("message", "<主题>", "git commit 的单行提交主题；Ticket trailer 由策略追加")
	defineArgument("paths", "<路径,路径…>", "git commit 要提交的仓库相对路径，逗号分隔；范围外改动不会暂存")
	defineArgument("slug", "<短名>", "git branch start 的分支短名，小写字母、数字、连字符")
	defineArgument("from", "<Git引用>", "git branch start 的起点；默认策略默认分支")
	defineArgument("remote", "<远端名>", "git push 的远端，如 origin")
	defineArgument("confirm-remote", "<远端名>", "git push --apply 的用户确认；必须与计划中的远端同名")
	defineArgument("tag", "<标签>", "git push 改为推送该本地带注释标签，如 v1.2.3")
	defineArgument("key", "<字段>", "git policy set 的策略字段")
	defineArgument("tag-version", "<版本>", "git release tag 的版本，如 2.2.0，标签为 v2.2.0")
	defineArgument("evidence-file", "<路径>", "git release tag 记录摘要的仓库内证据文件，须已纳入版本控制")

	for _, row := range []struct{ key, description string }{
		{"json", "执行结果输出 JSON；帮助始终输出文本"},
		{"human", "强制中文摘要；与 --json 互斥；管道默认保留原格式"},
		{"diagnostics", "与 --json 同用，在失败 envelope 中附加诊断"},
		{"help", "显示当前命令帮助；不读取项目、不联网、不写入"},
		{"version", "显示 CLI 版本及来源；不需要项目"},
		{"plan", "只生成计划，使用 --out 保存；不应用项目修改"},
		{"apply", "应用已保存计划或显式恢复；写入前检查冲突"},
		{"check", "仅查询在线版本；不下载发行包、不写入"},
		{"full", "init/attach 扩大所选安装方式的资源集合；不切换安装方式"},
	} {
		spec := argumentSpecs[row.key]
		spec.description = row.description
		argumentSpecs[row.key] = spec
	}
}
func defineArgument(key, placeholder, description string, choices ...string) {
	argumentSpecs[key] = argumentSpec{placeholder, description, false, choices}
}
func argumentError(message string) error {
	return &domain.Error{Code: "ARGUMENT", Message: message, Exit: 2}
}
func helpCommand(args []string) string {
	root := newRootCommand(nil, nil, nil)
	if len(args) > 0 && args[0] == "help" {
		args = args[1:]
	}
	cmd, rest := lookupCommand(root, args)
	if cmd == root {
		return "yss --help"
	}
	_ = rest
	return cmd.CommandPath() + " --help"
}
func suggestions(input string, candidates []string) string {
	best := 3
	matches := []string{}
	for _, candidate := range candidates {
		d := editDistance(input, candidate)
		if d < best {
			best = d
			matches = []string{candidate}
		} else if d == best && d < 3 {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	if len(matches) > 3 {
		matches = matches[:3]
	}
	return "；您可能想使用: " + strings.Join(matches, "、")
}
func editDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	prev := make([]int, len(y)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, r := range x {
		row := make([]int, len(y)+1)
		row[0] = i + 1
		for j, s := range y {
			cost := 0
			if r != s {
				cost = 1
			}
			row[j+1] = min(row[j]+1, prev[j+1]+1, prev[j]+cost)
		}
		prev = row
	}
	return prev[len(y)]
}
func unknownOption(key string, candidates []string) error {
	names := []string{}
	for _, k := range candidates {
		names = append(names, "--"+k)
	}
	return argumentError("未知或不支持的参数: --" + key + suggestions("--"+key, names))
}

func terminalWidth(s string) int {
	columns := 0
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			columns += 2
		default:
			columns++
		}
	}
	return columns
}

// Finite values belong to their command; project-defined IDs remain unrestricted.
func commandChoices(key string) map[string][]string {
	switch key {
	case "contract view":
		return map[string][]string{"kind": {"slice"}, "view": {"review", "task", "full"}}
	case "context query", "lifecycle query", "lifecycle status", "lifecycle route":
		return map[string][]string{"view": {"agent"}}
	case "contract verify":
		return map[string][]string{"kind": {"backend-preparation-inputs", "scaffold-inputs", "stage-result", "slice-draft", "slice-execution-result", "governance-policy", "architecture-evidence", "task-baseline", "task-candidate", "task-selection", "governance-write", "continuation-comparison", "work-unit-user-decision", "slice", "scaffold", "task", "openapi-validation", "backend-delivery-terminal", "backend-review", "scaffold-decisions", "slice-preflight", "business-tickets", "plan-review-task", "plan-review-control", "plan-entry", "implementation-scope", "checkpoint-user-decisions", "scaffold-user-decision", "checkpoint-approvals", "gate-checks"}, "phase": {"preflight", "design", "contract", "inputs", "implementation", "verification"}, "mode": {"draft", "formal"}}
	case "evidence verify":
		return map[string][]string{"kind": {"approval", "user-decision", "verification", "context-reconciliation"}}
	case "handoff verify":
		return map[string][]string{"kind": {"package", "consumption", "backend-delivery", "source", "delivery", "source-approval"}}
	case "runtime complete":
		return map[string][]string{"status": {"passed", "success", "completed", "ok", "failed", "failure", "cancelled", "canceled", "timed-out", "timeout", "error"}}
	}
	return nil
}

// Public verify flags are narrower than the structural dispatch superset.
// Keep kind-specific selectors out of unrelated interfaces before reading assets.
func verificationOptions(group, kind string) []string {
	options := strings.Fields("root target-dir profile json file checkpoint home run-dir tool-root template-checkout")
	kinds := []string{kind}
	if group == "lifecycle" {
		return append(options, "history")
	}
	options = append(options, "kind")
	if kind == "" {
		kinds = commandChoices(group + " verify")["kind"]
	}
	for _, k := range kinds {
		switch group + "." + k {
		case "contract.slice-execution-result":
			options = append(options, "task")
		case "contract.slice-preflight":
			options = append(options, "approval-ref", "unit")
		case "contract.slice":
			options = append(options, "approval-ref", "unit")
		case "contract.business-tickets":
			options = append(options, "mode", "review-ref")
		case "contract.task":
			options = append(options, "history", "task-ref")
		case "contract.scaffold-decisions":
			options = append(options, "allow-template")
		case "contract.openapi-validation":
			options = append(options, "allow-template")
		case "evidence.approval":
			options = append(options, "require-approved", "history", "gate", "boundary", "task", "expected", "binding")
		case "evidence.user-decision":
			options = append(options, "requirements", "continuation", "from-record", "task")
		case "evidence.verification":
			options = append(options, "approval-ref", "task")
		case "contract.stage-result":
			options = append(options, "gate", "asset-ref")
		case "contract.gate-checks":
			options = append(options, "gate")
		case "handoff.source-approval":
			options = append(options, "gate", "source-context-snapshot")
		case "handoff.source":
			options = append(options, "history")
		case "handoff.delivery", "handoff.package":
			options = append(options, "package")
		case "handoff.backend-delivery":
			options = append(options, "package")
		case "handoff.consumption":
			options = append(options, "consumer", "slice")
		}
	}
	return options
}
