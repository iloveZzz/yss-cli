package reading

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"math/big"
	"regexp"
	"strings"
)

type readingPair struct {
	Key   string
	Value *readingNode
}

type readingNode struct {
	Value any
	Pairs []readingPair
	Items []*readingNode
	Kind  byte
}

func readingParse(raw []byte) (*readingNode, error) {
	ordered, err := kernel.OrderedAssetJSON(raw)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(ordered))
	d.UseNumber()
	var read func() (*readingNode, error)
	read = func() (*readingNode, error) {
		v, err := d.Token()
		if err != nil {
			return nil, err
		}
		n := &readingNode{Value: v}
		if number, ok := v.(json.Number); ok {
			// 与 Node 的 parseSliceYaml 一致：整数必须落在 JS 可精确表示的范围内，超出须改用字符串。
			if value, isInteger := new(big.Int).SetString(number.String(), 10); isInteger && value.CmpAbs(big.NewInt(1<<53-1)) > 0 {
				return nil, fmt.Errorf("YAML 整数超出精确范围，请使用字符串")
			}
		}
		delim, ok := v.(json.Delim)
		if !ok {
			return n, nil
		}
		n.Kind = byte(delim)
		if delim == '{' {
			n.Value = map[string]any{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, err
				}
				child, err := read()
				if err != nil {
					return nil, err
				}
				n.Pairs = append(n.Pairs, readingPair{key.(string), child})
				n.Value.(map[string]any)[key.(string)] = child.Value
			}
		} else if delim == '[' {
			n.Value = []any{}
			for d.More() {
				child, err := read()
				if err != nil {
					return nil, err
				}
				n.Items = append(n.Items, child)
				n.Value = append(n.Value.([]any), child.Value)
			}
		} else {
			return nil, fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return n, err
	}
	return read()
}

func readingScalar(value any) *readingNode { return &readingNode{Value: value} }

func (n *readingNode) get(key string) *readingNode {
	for _, p := range n.Pairs {
		if p.Key == key {
			return p.Value
		}
	}
	return nil
}

func (n *readingNode) reorder(order []string) *readingNode {
	out := &readingNode{Value: n.Value, Kind: n.Kind}
	seen := map[string]bool{}
	for _, key := range order {
		if value := n.get(key); value != nil {
			seen[key] = true
			out.Pairs = append(out.Pairs, readingPair{key, value})
		}
	}
	for _, p := range n.Pairs {
		if !seen[p.Key] {
			out.Pairs = append(out.Pairs, p)
		}
	}
	return out
}

var readingLabels = map[string]string{
	"domain-strategy": "领域战略审阅", "stage-decision-package": "阶段决策审阅", "checkpoint": "阶段状态快照", "contexts": "业务责任区", "subdomains": "业务板块", "relationships": "协作与交接关系", "rule_catalog": "业务规则", "scenarios": "业务场景", "concept_candidates": "关键业务对象候选", "invariants": "不可违反的规则", "downstream_mapping": "下游传播", "context_snapshot": "术语来源", "evidence_refs": "证据来源", "approval": "批准记录",
	"responsibilities": "负责事项", "non_responsibilities": "不负责事项", "owner": "负责人", "status": "声明状态", "statement": "规则内容", "responsible_context": "责任区", "preconditions": "前提条件", "commands": "操作", "rules": "适用规则", "events": "业务事件", "success_results": "成功结果", "failure_results": "失败结果", "consumers": "消费方", "rule_refs": "规则引用", "critical": "关键场景",
	"problem_statement": "问题与目标", "target_users": "目标用户", "mvp": "本期范围", "non_goals": "非目标", "success_criteria": "成功标准", "test_seams": "测试边界", "confirmed_decisions": "已确认决定", "assumptions": "假设", "constraints": "约束", "unresolved_items": "未决事项", "domain_strategy_ref": "领域战略来源", "impact_assessment": "影响面",
	"stage": "当前阶段", "next_work_unit": "下一工作单元", "blockers": "已登记阻塞", "stage_tracking": "阶段工作", "items": "工作项", "progress": "工作项进度", "acceptance": "验收条件", "dependencies": "依赖", "completion": "完成证据", "recheck_required": "需要重新检查", "deferred": "延期安排", "cancellation_reason": "取消原因",
	"artifacts": "阶段资产", "gates": "门禁登记", "checks": "检查范围", "verification": "验证记录", "human_review": "人工审阅", "context_reconciliation": "术语对账", "ticket_sync": "Ticket 同步", "git_checkpoint": "Git 授权记录", "rollback": "回滚",
	"changes": "文件变化", "before": "修改前", "after": "修改后", "changed_refs": "变更文件", "source_operation": "源操作结果", "reading_update": "阅读材料更新", "name": "名称", "title": "标题", "description": "说明", "rationale": "依据", "scope": "范围", "type": "类型", "next_action": "下一动作", "target_version": "目标版本", "semantic_upstream": "规则提供方", "semantic_downstream": "规则使用方", "business_authority": "业务决策权", "transport_direction": "信息方向", "translation_responsibility": "口径转换负责人", "model_change_impact": "模型变更影响", "direction_explanation": "方向说明",
}

var readingOrders = map[string][]string{"checkpoint": {"stage", "status", "next_work_unit", "blockers", "stage_tracking", "artifacts", "gates", "verification", "human_review"}, "domain-strategy": {"contexts", "subdomains", "relationships", "rule_catalog", "scenarios", "concept_candidates", "invariants", "downstream_mapping", "status"}, "stage-decision-package": {"problem_statement", "target_users", "mvp", "non_goals", "confirmed_decisions", "success_criteria", "test_seams", "assumptions", "constraints", "unresolved_items", "downstream_mapping"}}

func readingEscape(v string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\\", "\\\\", "`", "\\`", "*", "\\*", "_", "\\_", "[", "\\[", "]", "\\]", "|", "\\|").Replace(v)
}

func readingString(v any) string {
	if v == nil {
		return "null"
	}
	if s, ok := v.(string); ok {
		return s
	}
	return string(kernel.ApCanonical(v))
}

func readingNamed(v any, names map[string]string) string {
	if str, ok := v.(string); ok {
		if name, ok := names[str]; ok {
			return name + "（" + str + "）"
		}
	}
	return readingString(v)
}

func readingText(v any, names map[string]string) string {
	return strings.Join(strings.Split(readingEscape(strings.ReplaceAll(readingNamed(v, names), "\r\n", "\n")), "\n"), "\n> ")
}

func readingRenderValue(n *readingNode, names map[string]string) string {
	if n.Kind == 0 {
		return readingText(n.Value, names)
	}
	if len(n.Pairs) == 0 && len(n.Items) == 0 {
		if n.Kind == '[' {
			return "[]（来源明确为空）"
		}
		return "{}（来源明确为空）"
	}
	pairs := n.Pairs
	if n.Kind == '[' {
		pairs = []readingPair{}
		for i, v := range n.Items {
			pairs = append(pairs, readingPair{fmt.Sprint(i + 1), v})
		}
	}
	out := []string{}
	for _, p := range pairs {
		if n.Kind == '[' && p.Value.Kind == 0 {
			out = append(out, "- "+strings.ReplaceAll(readingText(p.Value.Value, names), "\n", "\n  "))
			continue
		}
		label := readingLabels[p.Key]
		if n.Kind == '[' {
			label = "第 " + p.Key + " 项"
		} else if label == "" {
			label = readingNamed(p.Key, names)
		}
		label = readingEscape(label)
		rendered := readingRenderValue(p.Value, names)
		if len(p.Value.Pairs) > 0 || len(p.Value.Items) > 0 {
			out = append(out, "- **"+label+"**：\n  "+strings.ReplaceAll(rendered, "\n", "\n  "))
		} else {
			out = append(out, "- **"+label+"**："+strings.ReplaceAll(rendered, "\n", "\n  "))
		}
	}
	return strings.Join(out, "\n")
}

var readingToken = regexp.MustCompile(`(?:stage|gate|check|artifact|work-unit|evidence|role)\.[a-z0-9][a-z0-9-]*`)

func readingNames(n *readingNode, registry, roles map[string]any) map[string]string {
	records := map[string]map[string]any{}
	for _, key := range []string{"stages", "gates", "checks", "artifacts", "work_units", "evidence"} {
		for _, v := range kernel.SemList(registry[key]) {
			r := kernel.SemMap(v)
			records[kernel.Text(r["id"])] = r
		}
	}
	for _, v := range kernel.SemList(roles["roles"]) {
		r := kernel.SemMap(v)
		records[kernel.Text(r["id"])] = r
	}
	if r := kernel.SemMap(roles["orchestrator"]); len(r) > 0 {
		records[kernel.Text(r["id"])] = r
	}
	names := map[string]string{}
	visitString := func(value string) {
		for _, bounds := range readingToken.FindAllStringIndex(value, -1) {
			word := func(c byte) bool {
				return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_./-", rune(c))
			}
			if bounds[0] > 0 && word(value[bounds[0]-1]) || bounds[1] < len(value) && word(value[bounds[1]]) {
				continue
			}
			id := value[bounds[0]:bounds[1]]
			r := records[id]
			name, exists := r["public_name"]
			if !exists || name == nil {
				name = r["name"]
			}
			if strings.TrimSpace(kernel.Text(name)) == "" {
				name = "名称未识别"
			}
			names[id] = kernel.Text(name)
		}
	}
	var walk func(*readingNode)
	walk = func(n *readingNode) {
		if n.Kind == 0 {
			if v, ok := n.Value.(string); ok {
				visitString(v)
			}
		}
		for _, p := range n.Pairs {
			visitString(p.Key)
			walk(p.Value)
		}
		for _, child := range n.Items {
			walk(child)
		}
	}
	walk(n)
	return names
}

// BuildReading 是策略层：构建诊断按原有错误码转换为拒绝（退出码 1）或不可用（退出码 2）。
func BuildReading(s *kernel.SemanticSession, cpRef string) (*ReadingBuild, error) {
	result, err := RenderReading(s, cpRef)
	if err != nil {
		return nil, err
	}
	if f := result.Finding; f != nil {
		if f.Class == FindingUnavailable {
			return nil, s.Unavailable(f.Code, f.Message)
		}
		return nil, s.Reject(f.Code, f.Message)
	}
	return result.Build, nil
}
