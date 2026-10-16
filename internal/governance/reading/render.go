package reading

import (
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"path"
	"regexp"
	"strings"
)

// ReadingBuild 是阅读视图的纯构建产物：渲染输出、导航块以及构建时观察到的
// 项目文件、工具文件和渲染闭包摘要。
type ReadingBuild struct {
	Outputs     map[string]string
	OutputOrder []string
	Block       string
	ProjectRefs map[string]any
	// ProjectOrder 记录项目文件的首次发现顺序，用于清单里缺失文件行的稳定排序。
	ProjectOrder []string
	ToolRefs     map[string]any
	Renderer     map[string]any
}

// FindingClass 区分策略层把构建诊断转换成哪一类错误。
type FindingClass int

const (
	// FindingReject 对应退出码 1 的拒绝。
	FindingReject FindingClass = iota + 1
	// FindingUnavailable 对应退出码 2 的输入或能力问题。
	FindingUnavailable
)

// ReadingFinding 是构建遇到的第一个阻断诊断。构建在此处停止，Build 为空。
type ReadingFinding struct {
	Class   FindingClass
	Code    string
	Message string
}

func (f *ReadingFinding) Error() string { return f.Code + ": " + f.Message }

func readingReject(code, message string) error {
	return &ReadingFinding{Class: FindingReject, Code: code, Message: message}
}

func readingUnavailable(code, message string) error {
	return &ReadingFinding{Class: FindingUnavailable, Code: code, Message: message}
}

// ReadingResult 是 RenderReading 的结构化结果：要么有 Build，要么有 Finding。
type ReadingResult struct {
	Build   *ReadingBuild
	Finding *ReadingFinding
}

// RenderReading 只构建，不做拒绝决策。读取与会话错误原样返回；
// 构建诊断放进 Finding，由调用方策略决定如何处理。
func RenderReading(s *kernel.SemanticSession, cpRef string) (*ReadingResult, error) {
	b, err := buildReading(s, cpRef)
	if err != nil {
		if finding, ok := err.(*ReadingFinding); ok {
			return &ReadingResult{Finding: finding}, nil
		}
		return nil, err
	}
	return &ReadingResult{Build: b}, nil
}

func buildReading(s *kernel.SemanticSession, cpRef string) (*ReadingBuild, error) {
	checkpoint, err := s.Doc(cpRef)
	if err != nil {
		return nil, err
	}
	feature, base, err := kernel.StageFeatureBinding(s, cpRef, checkpoint)
	if err != nil {
		return nil, err
	}
	directory := base + "/reading"
	b := &ReadingBuild{Outputs: map[string]string{}, ProjectRefs: map[string]any{}, ToolRefs: map[string]any{}, Renderer: map[string]any{}}
	project := func(ref string) ([]byte, error) {
		if _, seen := b.ProjectRefs[ref]; !seen {
			b.ProjectOrder = append(b.ProjectOrder, ref)
		}
		exists, err := s.Exists(ref)
		if err != nil {
			return nil, err
		}
		if !exists {
			b.ProjectRefs[ref] = nil
			return nil, nil
		}
		raw, err := s.Bytes(ref)
		if err == nil {
			b.ProjectRefs[ref] = "sha256:" + safefs.Digest(raw)
		}
		return raw, err
	}
	for _, ref := range []string{"yss-project.yaml", "CONTEXT.md", ".template-spec/process/reading-policy.yaml", kernel.ApprovalRegistryRef, kernel.ApprovalRolesRef} {
		if _, err := project(ref); err != nil {
			return nil, err
		}
	}
	if strings.HasPrefix(feature, "feature.") {
		_, contractRefs, _, err := s.OrchestrationDocument(kernel.GuidanceContractRef(s.Report.Profile))
		if err != nil {
			return nil, err
		}
		for _, ref := range append([]string{kernel.TrackerRef, ".template-spec/process/harness-profile.yaml"}, contractRefs...) {
			if _, err := project(ref); err != nil {
				return nil, err
			}
		}
		config, err := kernel.Tracker(s.V, s)
		if err != nil {
			return nil, err
		}
		root := strings.TrimSuffix(kernel.Text(config["root"]), "/")
		files, err := s.Scan(root)
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
			meta, err := kernel.ContractTicketMetadata(s, ref)
			if err != nil {
				return nil, err
			}
			if checkpoint := kernel.Text(meta["checkpoint_ref"]); checkpoint != "" {
				if _, err := project(checkpoint); err != nil {
					return nil, err
				}
			}
		}
	}
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return nil, err
	}
	if identity["repository_mode"] != "project-instance" {
		return nil, readingReject("READING_IDENTITY", "managed 阅读视图仅用于 project-instance")
	}
	cpBytes, err := project(cpRef)
	if err != nil {
		return nil, err
	}
	cp, err := readingParse(cpBytes)
	if err != nil {
		return nil, readingUnavailable("INPUT", err.Error())
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
			ref := kernel.Text(p.Value.Value)
			if ref == "" {
				v := kernel.SemMap(p.Value.Value)
				ref = kernel.Text(v["ref"])
				if ref == "" && kernel.SemHas([]string{"missing", "not-applicable"}, kernel.Text(v["status"])) {
					continue
				}
			}
			if ref == "" || seen[kind] {
				return nil, readingReject("READING_ASSET", "阅读资产缺引用或种类重复")
			}
			seen[kind] = true
			assets = append(assets, struct{ ref, kind string }{ref, kind})
		}
	}
	var bindReferences func(*readingNode, string) error
	bindReferences = func(n *readingNode, key string) error {
		if n.Kind == 0 {
			ref := kernel.Text(n.Value)
			if (key == "ref" || strings.HasSuffix(key, "_ref") || key == "evidence_refs") && (strings.HasPrefix(ref, "docs/") || strings.HasPrefix(ref, ".template-spec/") || strings.HasPrefix(ref, "CONTEXT.md")) {
				ref = strings.SplitN(ref, "#", 2)[0]
				if strings.HasPrefix(ref, directory+"/") || ref == base+"/map.md" {
					return readingReject("READING_CYCLE", "阅读视图不能引用自身")
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
			return nil, readingUnavailable("INPUT", err.Error())
		}
		if err = readingRequireUnblocked(s, node, ""); err != nil {
			return nil, err
		}
		if err = bindReferences(node, ""); err != nil {
			return nil, err
		}
		schemaRef := map[string]string{"checkpoint": ".template-spec/process/schemas/lifecycle-checkpoint.schema.json", "domain-strategy": ".template-spec/process/schemas/domain-strategy-reading.schema.json", "stage-decision-package": ".template-spec/process/schemas/stage-decision-package-reading.schema.json"}[asset.kind]
		if err = s.ValidateSchema(schemaRef, node.Value); err != nil {
			return nil, err
		}
		if err = b.readingToolSchema(s, schemaRef, map[string]bool{}); err != nil {
			return nil, err
		}
		sourceCount := 0
		var refs func(*readingNode) error
		visited := map[string]bool{}
		refs = func(n *readingNode) error {
			m, ok := kernel.Object(n.Value)
			if ok {
				ref, d := kernel.Text(m["ref"]), kernel.Text(m["digest"])
				if ref != "" && d != "" && !visited[ref+"\x00"+d] {
					visited[ref+"\x00"+d] = true
					sourceCount++
					raw, err := project(ref)
					if err != nil {
						return err
					}
					if raw == nil || d != "sha256:"+safefs.Digest(raw) {
						return readingReject("READING_SOURCE_DRIFT", "阅读资产来源摘要过期: "+ref)
					}
				}
				for _, p := range n.Pairs {
					if strings.HasSuffix(p.Key, "_ref") {
						paired := strings.TrimSuffix(p.Key, "_ref") + "_digest"
						d := kernel.Text(m[paired])
						ref := kernel.Text(p.Value.Value)
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
			if a, ok := kernel.SemMap(node.Value)["blockers"].([]any); ok && len(a) == 0 {
				blockerNote = "未登记阻塞；不等于可执行。"
			}
			note := &readingNode{Kind: '{', Pairs: []readingPair{{"源声明", readingScalar("上面的阶段、状态和工作项进度均为源声明，分别保留。")}, {"阻塞登记", readingScalar(blockerNote)}, {"未核验", readingScalar("批准、完整门禁、Ticket 就绪及外部事实未核验；历史验证记录不能替代本次验证。")}}}
			content.Pairs = append(content.Pairs, readingPair{"状态阅读说明", note})
		}
		m := kernel.SemMap(node.Value)
		id := asset.ref
		for _, key := range []string{"contract_id", "feature_id", "domain_strategy_id", "stage_decision_id", "technical_design_id", "decision_id"} {
			if kernel.Text(m[key]) != "" {
				id = kernel.Text(m[key])
				break
			}
		}
		if id == asset.ref && kernel.Text(kernel.SemMap(m["request"])["request_id"]) != "" {
			id = kernel.Text(kernel.SemMap(m["request"])["request_id"])
		}
		if id == asset.ref {
			for _, key := range []string{"handoff_id", "baseline_id", "spec_id", "plan_id", "id"} {
				if kernel.Text(m[key]) != "" {
					id = kernel.Text(m[key])
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
		names := readingNames(content, s.Registry, s.Roles)
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

func (b *ReadingBuild) readingToolSchema(s *kernel.SemanticSession, ref string, seen map[string]bool) error {
	if seen[ref] {
		return nil
	}
	seen[ref] = true
	raw, err := s.ToolBytes(ref)
	if err != nil {
		return err
	}
	b.ToolRefs[ref] = "sha256:" + safefs.Digest(raw)
	node, err := readingParse(raw)
	if err != nil {
		return readingUnavailable("CAPABILITY", err.Error())
	}
	var visit func(*readingNode) error
	visit = func(n *readingNode) error {
		for _, p := range n.Pairs {
			if p.Key == "$ref" || p.Key == "$dynamicRef" {
				value := kernel.Text(p.Value.Value)
				target := strings.SplitN(value, "#", 2)[0]
				if target != "" {
					if strings.Contains(target, ":") {
						return readingUnavailable("CAPABILITY", "阅读 Schema 禁止远程引用")
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

func (b *ReadingBuild) readingToolClosure(s *kernel.SemanticSession) error {
	imports := regexp.MustCompile(`(?:from\s*|import\s*)['"](\.[^'"]+)['"]`)
	var visit func(string) error
	visit = func(ref string) error {
		if _, ok := b.Renderer[ref]; ok {
			return nil
		}
		if len(b.Renderer) >= 2048 {
			return readingUnavailable("CAPABILITY", "阅读工具闭包过大")
		}
		raw, err := s.ToolBytes(ref)
		if err != nil {
			return err
		}
		sha := safefs.Digest(raw)
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

// The new current-transition interface refuses source diagnostics rather than
// accepting a historically rendered diagnostic-only view as current evidence.
func readingRequireUnblocked(s *kernel.SemanticSession, n *readingNode, key string) error {
	if kernel.SemHas([]string{"blockers", "blocking_findings", "stale_inputs", "readiness_blockers"}, key) && len(n.Items) > 0 {
		return readingReject("READING_SOURCE_BLOCKED", "阅读来源仍有阻断: "+key)
	}
	idFields := map[string]string{"contexts": "context_id", "subdomains": "subdomain_id", "relationships": "relationship_id", "rule_catalog": "rule_id", "scenarios": "scenario_id", "concept_candidates": "concept_id", "invariants": "invariant_id", "downstream_mapping": "mapping_id", "items": "id"}
	if field := idFields[key]; field != "" && n.Kind == '[' {
		seen := map[string]bool{}
		for _, item := range n.Items {
			id := kernel.Text(kernel.SemMap(item.Value)[field])
			if id != "" {
				if seen[id] {
					return readingReject("READING_SOURCE_BLOCKED", "阅读来源稳定 ID 重复: "+key)
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
