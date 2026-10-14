package reading

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"strings"
)

func init() { kernel.RegisterSemanticValidator("reading-transition", verifyReadingTransitionSemantic) }

func verifyReadingTransitionSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	const policyRef = ".template-spec/process/reading-policy.yaml"
	exists, err := s.Exists(policyRef)
	if err != nil {
		return err
	}
	if !exists {
		s.Report.Applicability = append(s.Report.Applicability, map[string]any{"id": "reading-views", "status": "not-applicable", "reason": "项目未启用 managed 阅读视图"})
		return nil
	}
	policy, err := s.Doc(policyRef)
	if err != nil {
		return err
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/reading-policy.schema.json", policy); err != nil {
		return err
	}
	if policy["mode"] != "managed" || !kernel.SemHas(policy["checkpoints"], ref) {
		s.Report.Applicability = append(s.Report.Applicability, map[string]any{"id": "reading-views", "status": "not-applicable", "reason": "当前 checkpoint 不要求 managed 阅读视图"})
		return nil
	}
	cp, err := s.Doc(ref)
	if err != nil {
		return err
	}
	_, base, err := kernel.StageFeatureBinding(s, ref, cp)
	if err != nil {
		return err
	}
	for _, suffix := range []string{".transaction.json", ".lock"} {
		present, err := s.Exists(base + "/reading/" + suffix)
		if err != nil {
			return err
		}
		if present {
			return s.Reject("READING_INCOMPLETE", "阅读材料仍有未完成事务")
		}
	}
	expected, err := BuildReading(s, ref)
	if err != nil {
		return err
	}
	manifest, err := s.Doc(base + "/reading/.manifest.json")
	if err != nil {
		return err
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/reading-manifest.schema.json", manifest); err != nil {
		return err
	}
	if manifest["checkpoint_ref"] != ref || !kernel.ApEqual(manifest["renderer"], expected.Renderer) {
		return s.Reject("READING_MANIFEST_STALE", "阅读清单的 checkpoint 或固定工具闭包不匹配")
	}
	actualDependencies := map[string]any{}
	for _, value := range kernel.SemList(manifest["dependencies"]) {
		row := kernel.SemMap(value)
		key := kernel.Text(row["scope"]) + "\x00" + kernel.Text(row["ref"])
		if _, duplicate := actualDependencies[key]; duplicate {
			return s.Reject("READING_MANIFEST_STALE", "阅读依赖重复")
		}
		actualDependencies[key] = row["digest"]
	}
	dependencies := map[string]any{}
	for key, value := range expected.ProjectRefs {
		dependencies["project\x00"+key] = value
	}
	for key, value := range expected.ToolRefs {
		dependencies["tool\x00"+key] = value
	}
	if !kernel.ApEqual(actualDependencies, dependencies) {
		return s.Reject("READING_MANIFEST_STALE", "阅读来源依赖闭包与当前原生重算不一致")
	}
	outputs := map[string]any{}
	for key, value := range expected.Outputs {
		outputs[key] = "sha256:" + safefs.Digest([]byte(value))
		actual, err := s.Bytes(key)
		if err != nil {
			return err
		}
		if string(actual) != value {
			return s.Reject("READING_OUTPUT_STALE", "阅读输出过期: "+key)
		}
	}
	if !kernel.ApEqual(manifest["outputs"], outputs) || len(kernel.SemList(manifest["diagnostics"])) != 0 {
		return s.Reject("READING_MANIFEST_STALE", "阅读输出清单或来源诊断不匹配")
	}
	navigation := kernel.SemMap(manifest["navigation"])
	if navigation["ref"] != base+"/map.md" || navigation["block_digest"] != "sha256:"+safefs.Digest([]byte(expected.Block)) {
		return s.Reject("READING_NAVIGATION_STALE", "阅读导航清单过期")
	}
	mapBytes, err := s.Bytes(base + "/map.md")
	if err != nil {
		return err
	}
	body := string(mapBytes)
	begin, end := "<!-- YSS-READING:BEGIN -->", "<!-- YSS-READING:END -->"
	a, b := strings.Index(body, begin), strings.Index(body, end)
	if strings.Count(body, begin) != 1 || strings.Count(body, end) != 1 || b < a || body[a:b+len(end)] != expected.Block {
		return s.Reject("READING_NAVIGATION_STALE", "阅读导航受管区过期或标记冲突")
	}
	return nil
}
