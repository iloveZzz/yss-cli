package mcp

import (
	"fmt"
	"strings"
)

// param 描述一个工具参数如何落到 yss 参数：flag 非空时作为 "--flag 值"，否则作为位置参数。
type param struct {
	name, kind, flag, description string
	required                      bool
	enum                          []string
}

type tool struct {
	name, description string
	base              []string
	params            []param
}

// tools 是唯一允许的只读命令集合；新增工具须是只读命令，且不能接收任意参数。
var tools = []tool{
	{"lifecycle_route", "判定同一任务记录走 daily / standard / governed / needs-info（agent 视图）；只读，不授予实现资格。", []string{"lifecycle", "route", "--view", "agent"}, []param{
		{name: "task", kind: "string", flag: "task", description: "项目内任务 Markdown 记录", required: true},
		{name: "implementation_root", kind: "string", flag: "implementation-root", description: "已确认实现仓的 Git 根（绝对路径）", required: true},
		{name: "base", kind: "string", flag: "base", description: "完整 SHA 或实现仓内可解析引用，如 HEAD", required: true},
	}},
	{"verify_daily", "核验 daily / standard 任务的当前差异、测试、独立审查与 API 证据；不执行测试，不授予合并或发布。", []string{"lifecycle", "verify-daily"}, []param{
		{name: "task", kind: "string", flag: "task", description: "项目内任务 Markdown 记录", required: true},
		{name: "implementation_root", kind: "string", flag: "implementation-root", description: "已确认实现仓的 Git 根（绝对路径）", required: true},
		{name: "base", kind: "string", flag: "base", description: "完整 SHA 或实现仓内可解析引用", required: true},
	}},
	{"lifecycle_status", "读取当前 checkpoint 的阶段、目标、协作与下一步（agent 视图）；next_action 不是执行授权。", []string{"lifecycle", "status", "--view", "agent"}, []param{
		{name: "checkpoint", kind: "string", flag: "checkpoint", description: "当前 checkpoint 文件", required: true},
	}},
	{"lifecycle_query", "按稳定 ID 查询生命周期注册表（阶段、门禁、工作单元等）的 agent 视图。", []string{"lifecycle", "query", "--view", "agent"}, []param{
		{name: "id", kind: "string", flag: "id", description: "注册表稳定 ID，如 work-unit.entry-triage"},
		{name: "work_unit", kind: "string", flag: "work-unit", description: "工作单元 ID"},
		{name: "stage", kind: "string", flag: "stage", description: "阶段 ID"},
	}},
	{"context_query", "查询根 CONTEXT.md 的稳定业务词汇（agent 视图）。", []string{"context", "query", "--view", "agent"}, []param{
		{name: "id", kind: "string", flag: "id", description: "术语 ID；省略时返回全部"},
	}},
	{"contract_view", "读取 Slice 实现合同的审查、任务或完整阅读视图。", []string{"contract", "view", "--kind", "slice"}, []param{
		{name: "file", kind: "string", flag: "file", description: "Slice 合同文件", required: true},
		{name: "view", kind: "string", flag: "view", description: "阅读视图", required: true, enum: []string{"review", "task", "full"}},
	}},
	{"explain", "解释流程术语或生命周期稳定 ID（含去掉类别前缀的写法）。", []string{"explain"}, []param{
		{name: "term", kind: "string", description: "术语或稳定 ID，如 gate.plan-approved", required: true},
	}},
	{"check", "识别实例资产并调用对应 verify；每条发现带 code/file/pointer/fix/command。缺上下文为 not-evaluated，不算通过。", []string{"check"}, []param{
		{name: "files", kind: "array", description: "项目内 YAML/JSON 资产；与 changed 二选一"},
		{name: "changed", kind: "boolean", flag: "changed", description: "检查工作树相对 HEAD 的改动与未跟踪文件"},
		{name: "base", kind: "string", flag: "base", description: "与 changed 同用，并入 base..HEAD 的提交改动"},
		{name: "checkpoint", kind: "string", flag: "checkpoint", description: "需要当前上下文的资产（如用户决定）使用的 checkpoint"},
	}},
	{"verify_gate", "核验一个门禁所需的当前 checkpoint 与检查依赖；只核验不批准。", []string{"verify"}, []param{
		{name: "gate", kind: "string", flag: "gate", description: "门禁 ID，如 plan-approved", required: true},
		{name: "checkpoint", kind: "string", flag: "checkpoint", description: "当前 checkpoint 文件", required: true},
	}},
}

func toolByName(name string) (tool, bool) {
	for _, t := range tools {
		if t.name == name {
			return t, true
		}
	}
	return tool{}, false
}

func (t tool) schema() map[string]any {
	props := map[string]any{}
	required := []string{}
	for _, p := range t.params {
		prop := map[string]any{"type": p.kind, "description": p.description}
		if p.kind == "array" {
			prop["items"] = map[string]any{"type": "string"}
		}
		if len(p.enum) > 0 {
			prop["enum"] = p.enum
		}
		props[p.name] = prop
		if p.required {
			required = append(required, p.name)
		}
	}
	return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
}

// argv 严格转换参数：拒绝未知键、错误类型与以 - 开头的值，避免把值当成选项注入。
func (t tool) argv(arguments map[string]any) ([]string, error) {
	known := map[string]param{}
	for _, p := range t.params {
		known[p.name] = p
	}
	for name := range arguments {
		if _, ok := known[name]; !ok {
			return nil, fmt.Errorf("工具 %s 不接受参数 %s", t.name, name)
		}
	}
	args := append([]string{}, t.base...)
	positional := []string{}
	for _, p := range t.params {
		value, present := arguments[p.name]
		if !present || value == nil {
			if p.required {
				return nil, fmt.Errorf("工具 %s 缺少参数 %s", t.name, p.name)
			}
			continue
		}
		switch p.kind {
		case "boolean":
			b, ok := value.(bool)
			if !ok {
				return nil, fmt.Errorf("%s 必须是布尔值", p.name)
			}
			if b {
				args = append(args, "--"+p.flag)
			}
		case "array":
			list, ok := value.([]any)
			if !ok || len(list) > 200 {
				return nil, fmt.Errorf("%s 必须是不超过 200 项的字符串数组", p.name)
			}
			for _, item := range list {
				s, ok := item.(string)
				if !ok || !safeValue(s) {
					return nil, fmt.Errorf("%s 含非法值", p.name)
				}
				positional = append(positional, s)
			}
		default:
			s, ok := value.(string)
			if !ok || !safeValue(s) {
				return nil, fmt.Errorf("%s 必须是非空字符串且不能以 - 开头", p.name)
			}
			if len(p.enum) > 0 && !contains(p.enum, s) {
				return nil, fmt.Errorf("%s 只能是 %s", p.name, strings.Join(p.enum, "、"))
			}
			if p.flag == "" {
				positional = append(positional, s)
			} else {
				args = append(args, "--"+p.flag, s)
			}
		}
	}
	return append(args, positional...), nil
}

func safeValue(s string) bool {
	return strings.TrimSpace(s) != "" && !strings.HasPrefix(s, "-") && !strings.ContainsRune(s, 0) && len(s) <= 4096
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
