package governance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/safefs"
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
	ordered, err := orderedAssetJSON(raw)
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
	return string(apCanonical(v))
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
		for _, v := range semList(registry[key]) {
			r := semMap(v)
			records[text(r["id"])] = r
		}
	}
	for _, v := range semList(roles["roles"]) {
		r := semMap(v)
		records[text(r["id"])] = r
	}
	if r := semMap(roles["orchestrator"]); len(r) > 0 {
		records[text(r["id"])] = r
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
			if strings.TrimSpace(text(name)) == "" {
				name = "名称未识别"
			}
			names[id] = text(name)
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

type readingBuild struct {
	Outputs     map[string]string
	OutputOrder []string
	Block       string
	ProjectRefs map[string]any
	ToolRefs    map[string]any
	Renderer    map[string]any
}

func (s *semanticSession) buildReading(cpRef string) (*readingBuild, error) {
	checkpoint, err := s.doc(cpRef)
	if err != nil {
		return nil, err
	}
	feature, base, err := stageFeatureBinding(s, cpRef, checkpoint)
	if err != nil {
		return nil, err
	}
	directory := base + "/reading"
	b := &readingBuild{Outputs: map[string]string{}, ProjectRefs: map[string]any{}, ToolRefs: map[string]any{}, Renderer: map[string]any{}}
	project := func(ref string) ([]byte, error) {
		exists, err := s.exists(ref)
		if err != nil {
			return nil, err
		}
		if !exists {
			b.ProjectRefs[ref] = nil
			return nil, nil
		}
		raw, err := s.bytes(ref)
		if err == nil {
			b.ProjectRefs[ref] = "sha256:" + safefs.Digest(raw)
		}
		return raw, err
	}
	for _, ref := range []string{"yss-project.yaml", "CONTEXT.md", ".template-spec/process/reading-policy.yaml", approvalRegistryRef, approvalRolesRef} {
		if _, err := project(ref); err != nil {
			return nil, err
		}
	}
	if strings.HasPrefix(feature, "feature.") {
		for _, ref := range []string{trackerRef, ".template-spec/process/harness-profile.yaml", guidanceContractRef(s.report.Profile)} {
			if _, err := project(ref); err != nil {
				return nil, err
			}
		}
		config, err := tracker(s.v, s)
		if err != nil {
			return nil, err
		}
		root := strings.TrimSuffix(text(config["root"]), "/")
		files, err := s.scan(root)
		if err != nil {
			return nil, err
		}
		for _, ref := range files {
			if path.Base(ref) != "map.md" || path.Dir(path.Dir(ref)) != root {
				continue
			}
			// The current navigation map is rewritten by rendering. Its
			// registration is recomputed, never frozen as its own dependency.
			if ref != base+"/map.md" {
				if _, err := project(ref); err != nil {
					return nil, err
				}
			}
			meta, err := contractTicketMetadata(s, ref)
			if err != nil {
				return nil, err
			}
			if checkpoint := text(meta["checkpoint_ref"]); checkpoint != "" {
				if _, err := project(checkpoint); err != nil {
					return nil, err
				}
			}
		}
	}
	identity, err := s.doc("yss-project.yaml")
	if err != nil {
		return nil, err
	}
	if identity["repository_mode"] != "project-instance" {
		return nil, s.reject("READING_IDENTITY", "managed 阅读视图仅用于 project-instance")
	}
	cpBytes, err := project(cpRef)
	if err != nil {
		return nil, err
	}
	cp, err := readingParse(cpBytes)
	if err != nil {
		return nil, s.unavailable("INPUT", err.Error())
	}
	assets := []struct{ ref, kind string }{{cpRef, "checkpoint"}}
	seen := map[string]bool{}
	kinds := map[string]string{"artifact.domain-strategy": "domain-strategy", "domain_strategy": "domain-strategy", "domain-strategy": "domain-strategy", "artifact.stage-decision-package": "stage-decision-package", "stage_decision": "stage-decision-package", "stage-decision-package": "stage-decision-package"}
	if a := cp.get("artifacts"); a != nil {
		for _, p := range a.Pairs {
			kind := kinds[p.Key]
			if kind == "" {
				continue
			}
			ref := text(p.Value.Value)
			if ref == "" {
				v := semMap(p.Value.Value)
				ref = text(v["ref"])
				if ref == "" && semHas([]string{"missing", "not-applicable"}, text(v["status"])) {
					continue
				}
			}
			if ref == "" || seen[kind] {
				return nil, s.reject("READING_ASSET", "阅读资产缺引用或种类重复")
			}
			seen[kind] = true
			assets = append(assets, struct{ ref, kind string }{ref, kind})
		}
	}
	var bindReferences func(*readingNode, string) error
	bindReferences = func(n *readingNode, key string) error {
		if n.Kind == 0 {
			ref := text(n.Value)
			if (key == "ref" || strings.HasSuffix(key, "_ref") || key == "evidence_refs") && (strings.HasPrefix(ref, "docs/") || strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, "CONTEXT.md")) {
				ref = strings.SplitN(ref, "#", 2)[0]
				if strings.HasPrefix(ref, directory+"/") || ref == base+"/map.md" {
					return s.reject("READING_CYCLE", "阅读视图不能引用自身")
				}
				_, err := project(ref)
				return err
			}
			return nil
		}
		for _, p := range n.Pairs {
			if err := bindReferences(p.Value, p.Key); err != nil {
				return err
			}
		}
		for _, v := range n.Items {
			if err := bindReferences(v, key); err != nil {
				return err
			}
		}
		return nil
	}
	if err = bindReferences(cp, ""); err != nil {
		return nil, err
	}
	for _, asset := range assets {
		raw, err := project(asset.ref)
		if err != nil {
			return nil, err
		}
		node, err := readingParse(raw)
		if err != nil {
			return nil, s.unavailable("INPUT", err.Error())
		}
		if err = readingRequireUnblocked(s, node, ""); err != nil {
			return nil, err
		}
		if err = bindReferences(node, ""); err != nil {
			return nil, err
		}
		schemaRef := map[string]string{"checkpoint": ".template-spec/process/schemas/lifecycle-checkpoint.schema.json", "domain-strategy": ".template-spec/process/schemas/domain-strategy-reading.schema.json", "stage-decision-package": ".template-spec/process/schemas/stage-decision-package-reading.schema.json"}[asset.kind]
		if err = s.validateSchema(schemaRef, node.Value); err != nil {
			return nil, err
		}
		if err = b.readingToolSchema(s, schemaRef, map[string]bool{}); err != nil {
			return nil, err
		}
		sourceCount := 0
		var refs func(*readingNode) error
		visited := map[string]bool{}
		refs = func(n *readingNode) error {
			m, ok := object(n.Value)
			if ok {
				ref, d := text(m["ref"]), text(m["digest"])
				if ref != "" && d != "" && !visited[ref+"\x00"+d] {
					visited[ref+"\x00"+d] = true
					sourceCount++
					raw, err := project(ref)
					if err != nil {
						return err
					}
					if raw == nil || d != "sha256:"+safefs.Digest(raw) {
						return s.reject("READING_SOURCE_DRIFT", "阅读资产来源摘要过期: "+ref)
					}
				}
				for _, p := range n.Pairs {
					if strings.HasSuffix(p.Key, "_ref") {
						paired := strings.TrimSuffix(p.Key, "_ref") + "_digest"
						d := text(m[paired])
						ref := text(p.Value.Value)
						if ref != "" && d != "" {
							synthetic := &readingNode{Value: map[string]any{"ref": ref, "digest": d}}
							if err := refs(synthetic); err != nil {
								return err
							}
						}
					}
				}
			}
			for _, p := range n.Pairs {
				if err := refs(p.Value); err != nil {
					return err
				}
			}
			for _, v := range n.Items {
				if err := refs(v); err != nil {
					return err
				}
			}
			return nil
		}
		if err = refs(node); err != nil {
			return nil, err
		}
		content := node.reorder(readingOrders[asset.kind])
		if asset.kind == "checkpoint" {
			blockerNote := "按源记录检查；缺失不等于无阻塞。"
			if a, ok := semMap(node.Value)["blockers"].([]any); ok && len(a) == 0 {
				blockerNote = "未登记阻塞；不等于可执行。"
			}
			note := &readingNode{Kind: '{', Pairs: []readingPair{{"源声明", readingScalar("上面的阶段、状态和工作项进度均为源声明，分别保留。")}, {"阻塞登记", readingScalar(blockerNote)}, {"未核验", readingScalar("批准、完整门禁、Ticket 就绪及外部事实未核验；历史验证记录不能替代本次验证。")}}}
			content.Pairs = append(content.Pairs, readingPair{"状态阅读说明", note})
		}
		m := semMap(node.Value)
		id := asset.ref
		for _, key := range []string{"contract_id", "feature_id", "domain_strategy_id", "stage_decision_id", "technical_design_id", "decision_id"} {
			if text(m[key]) != "" {
				id = text(m[key])
				break
			}
		}
		if id == asset.ref && text(semMap(m["request"])["request_id"]) != "" {
			id = text(semMap(m["request"])["request_id"])
		}
		if id == asset.ref {
			for _, key := range []string{"handoff_id", "baseline_id", "spec_id", "plan_id", "id"} {
				if text(m[key]) != "" {
					id = text(m[key])
					break
				}
			}
		}
		version := "源资产未声明"
		for _, key := range []string{"contract_version", "domain_version", "package_version", "decision_version", "handoff_version", "version"} {
			if m[key] != nil && m[key] != false && m[key] != "" {
				version = readingString(m[key])
				break
			}
		}
		checks := []string{"可读取", "结构校验"}
		if sourceCount > 0 {
			checks = append(checks, fmt.Sprintf("核对 %d 项可识别来源摘要；其余语义由资产所有者核验", sourceCount))
		}
		names := readingNames(content, s.registry, s.roles)
		lines := []string{"# " + readingLabels[asset.kind] + " · " + readingEscape(id), "版本：" + readingEscape(version) + "；权威文件：" + readingEscape(asset.ref), "来源 SHA-256：sha256:" + safefs.Digest(raw), "本页为来源快照；当前有效性需重新检查。批准有效性未核验；本视图不授予执行权限。", "检查范围：" + strings.Join(checks, "；")}
		for _, p := range content.Pairs {
			label := readingLabels[p.Key]
			if label == "" {
				label = readingEscape(p.Key)
			}
			lines = append(lines, "## "+label, readingRenderValue(p.Value, names))
		}
		name := asset.kind
		if name == "checkpoint" {
			name = "status"
		}
		out := directory + "/" + name + ".review.md"
		b.OutputOrder = append(b.OutputOrder, out)
		b.Outputs[out] = strings.Join(lines, "\n\n") + "\n"
	}
	lines := []string{"<!-- YSS-READING:BEGIN -->", "## 阅读导航", ""}
	for _, ref := range b.OutputOrder {
		lines = append(lines, "- ["+path.Base(ref)+"](./reading/"+path.Base(ref)+")")
	}
	lines = append(lines, "", "以上为来源快照；提交审阅或交接前运行 `scripts/contract check-views --checkpoint "+cpRef+"`。", "<!-- YSS-READING:END -->")
	b.Block = strings.Join(lines, "\n")
	if err = b.readingToolClosure(s); err != nil {
		return nil, err
	}
	if err = b.readingToolSchema(s, ".template-spec/process/schemas/reading-manifest.schema.json", map[string]bool{}); err != nil {
		return nil, err
	}
	if err = b.readingToolSchema(s, ".template-spec/process/schemas/reading-policy.schema.json", map[string]bool{}); err != nil {
		return nil, err
	}
	return b, nil
}
func (b *readingBuild) readingToolSchema(s *semanticSession, ref string, seen map[string]bool) error {
	if seen[ref] {
		return nil
	}
	seen[ref] = true
	raw, err := s.toolBytes(ref)
	if err != nil {
		return err
	}
	b.ToolRefs[ref] = "sha256:" + safefs.Digest(raw)
	node, err := readingParse(raw)
	if err != nil {
		return s.unavailable("CAPABILITY", err.Error())
	}
	var visit func(*readingNode) error
	visit = func(n *readingNode) error {
		for _, p := range n.Pairs {
			if p.Key == "$ref" || p.Key == "$dynamicRef" {
				value := text(p.Value.Value)
				target := strings.SplitN(value, "#", 2)[0]
				if target != "" {
					if strings.Contains(target, ":") {
						return s.unavailable("CAPABILITY", "阅读 Schema 禁止远程引用")
					}
					if err := b.readingToolSchema(s, path.Join(path.Dir(ref), target), seen); err != nil {
						return err
					}
				}
			}
			if err := visit(p.Value); err != nil {
				return err
			}
		}
		for _, v := range n.Items {
			if err := visit(v); err != nil {
				return err
			}
		}
		return nil
	}
	return visit(node)
}
func (b *readingBuild) readingToolClosure(s *semanticSession) error {
	fingerprint := map[string][]string{
		"scripts/lib/reading-view-bundle.mjs":    {"2aab86301d37ffb95fbbc1136bf8989411b0f594aa8246ea3df0552d952b4772", "8d35fe88e6d6c933d5e172dabb8604565de5d9b055a028c399ddb712547a6539"},
		"scripts/lib/contract-views.mjs":         {"f88b6f5da3fea534717f58b833da3e8f1fa4d6c04f2e4ff9a754618316ef555c"},
		"scripts/lib/reading-view-adapters.mjs":  {"a749e7a86e0b62c720aea1da5980e7e1a6ae70620fb87056f2b89e28f44e91e9"},
		"scripts/lib/reading-view-markdown.mjs":  {"5b55e76271831f4c957f4991ba12f99e2245767ae8ab8a20188dcc0cf6b68e11"},
		"scripts/lib/lifecycle-presentation.mjs": {"beb03307591ab98186d1fadcefb40f0301a7d1417a239943a75f296f13171d37", "8dbed3b7c403fb055f53979e7c2c7c779fe6253510fd8ad6b83488d1ac442581"},
	}
	imports := regexp.MustCompile(`(?:from\s*|import\s*)['"](\.[^'"]+)['"]`)
	var visit func(string) error
	visit = func(ref string) error {
		if _, ok := b.Renderer[ref]; ok {
			return nil
		}
		if len(b.Renderer) >= 2048 {
			return s.unavailable("CAPABILITY", "阅读工具闭包过大")
		}
		raw, err := s.toolBytes(ref)
		if err != nil {
			return err
		}
		sha := safefs.Digest(raw)
		if expected, ok := fingerprint[ref]; ok && !contains(expected, sha) {
			return s.unavailable("CAPABILITY", "阅读渲染规则版本未迁移: "+ref)
		}
		b.Renderer[ref] = "sha256:" + sha
		b.ToolRefs[ref] = "sha256:" + sha
		if strings.HasSuffix(ref, ".mjs") || strings.HasSuffix(ref, ".js") {
			for _, m := range imports.FindAllSubmatch(raw, -1) {
				if err := visit(path.Join(path.Dir(ref), string(m[1]))); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, ref := range []string{"scripts/lib/reading-view-bundle.mjs", "scripts/contract"} {
		if err := visit(ref); err != nil {
			return err
		}
	}
	return nil
}
func readingSortedKeys(v map[string]any) []string {
	keys := []string{}
	for key := range v {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// The new current-transition interface refuses source diagnostics rather than
// accepting a historically rendered diagnostic-only view as current evidence.
func readingRequireUnblocked(s *semanticSession, n *readingNode, key string) error {
	if semHas([]string{"blockers", "blocking_findings", "stale_inputs", "readiness_blockers"}, key) && len(n.Items) > 0 {
		return s.reject("READING_SOURCE_BLOCKED", "阅读来源仍有阻断: "+key)
	}
	idFields := map[string]string{"contexts": "context_id", "subdomains": "subdomain_id", "relationships": "relationship_id", "rule_catalog": "rule_id", "scenarios": "scenario_id", "concept_candidates": "concept_id", "invariants": "invariant_id", "downstream_mapping": "mapping_id", "items": "id"}
	if field := idFields[key]; field != "" && n.Kind == '[' {
		seen := map[string]bool{}
		for _, item := range n.Items {
			id := text(semMap(item.Value)[field])
			if id != "" {
				if seen[id] {
					return s.reject("READING_SOURCE_BLOCKED", "阅读来源稳定 ID 重复: "+key)
				}
				seen[id] = true
			}
		}
	}
	for _, p := range n.Pairs {
		if err := readingRequireUnblocked(s, p.Value, p.Key); err != nil {
			return err
		}
	}
	for _, v := range n.Items {
		if err := readingRequireUnblocked(s, v, key); err != nil {
			return err
		}
	}
	return nil
}
