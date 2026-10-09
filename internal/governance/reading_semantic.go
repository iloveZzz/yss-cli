package governance

import (
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"strings"
)

func init() { registerSemanticValidator("reading-transition", verifyReadingTransitionSemantic) }

func verifyReadingTransitionSemantic(s *semanticSession, ref string, opts map[string]string) error {
	const policyRef = ".template-spec/process/reading-policy.yaml"
	exists, err := s.exists(policyRef)
	if err != nil {
		return err
	}
	if !exists {
		s.report.Applicability = append(s.report.Applicability, map[string]any{"id": "reading-views", "status": "not-applicable", "reason": "项目未启用 managed 阅读视图"})
		return nil
	}
	policy, err := s.doc(policyRef)
	if err != nil {
		return err
	}
	if err = s.validateSchema(".template-spec/process/schemas/reading-policy.schema.json", policy); err != nil {
		return err
	}
	if policy["mode"] != "managed" || !semHas(policy["checkpoints"], ref) {
		s.report.Applicability = append(s.report.Applicability, map[string]any{"id": "reading-views", "status": "not-applicable", "reason": "当前 checkpoint 不要求 managed 阅读视图"})
		return nil
	}
	cp, err := s.doc(ref)
	if err != nil {
		return err
	}
	_, base, err := stageFeatureBinding(s, ref, cp)
	if err != nil {
		return err
	}
	for _, suffix := range []string{".transaction.json", ".lock"} {
		present, err := s.exists(base + "/reading/" + suffix)
		if err != nil {
			return err
		}
		if present {
			return s.reject("READING_INCOMPLETE", "阅读材料仍有未完成事务")
		}
	}
	expected, err := s.buildReading(ref)
	if err != nil {
		return err
	}
	manifest, err := s.doc(base + "/reading/.manifest.json")
	if err != nil {
		return err
	}
	if err = s.validateSchema(".template-spec/process/schemas/reading-manifest.schema.json", manifest); err != nil {
		return err
	}
	if manifest["checkpoint_ref"] != ref || !apEqual(manifest["renderer"], expected.Renderer) {
		return s.reject("READING_MANIFEST_STALE", "阅读清单的 checkpoint 或固定工具闭包不匹配")
	}
	actualDependencies := map[string]any{}
	for _, value := range semList(manifest["dependencies"]) {
		row := semMap(value)
		key := text(row["scope"]) + "\x00" + text(row["ref"])
		if _, duplicate := actualDependencies[key]; duplicate {
			return s.reject("READING_MANIFEST_STALE", "阅读依赖重复")
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
	if !apEqual(actualDependencies, dependencies) {
		return s.reject("READING_MANIFEST_STALE", "阅读来源依赖闭包与当前原生重算不一致")
	}
	outputs := map[string]any{}
	for key, value := range expected.Outputs {
		outputs[key] = "sha256:" + safefs.Digest([]byte(value))
		actual, err := s.bytes(key)
		if err != nil {
			return err
		}
		if string(actual) != value {
			return s.reject("READING_OUTPUT_STALE", "阅读输出过期: "+key)
		}
	}
	if !apEqual(manifest["outputs"], outputs) || len(semList(manifest["diagnostics"])) != 0 {
		return s.reject("READING_MANIFEST_STALE", "阅读输出清单或来源诊断不匹配")
	}
	navigation := semMap(manifest["navigation"])
	if navigation["ref"] != base+"/map.md" || navigation["block_digest"] != "sha256:"+safefs.Digest([]byte(expected.Block)) {
		return s.reject("READING_NAVIGATION_STALE", "阅读导航清单过期")
	}
	mapBytes, err := s.bytes(base + "/map.md")
	if err != nil {
		return err
	}
	body := string(mapBytes)
	begin, end := "<!-- YSS-READING:BEGIN -->", "<!-- YSS-READING:END -->"
	a, b := strings.Index(body, begin), strings.Index(body, end)
	if strings.Count(body, begin) != 1 || strings.Count(body, end) != 1 || b < a || body[a:b+len(end)] != expected.Block {
		return s.reject("READING_NAVIGATION_STALE", "阅读导航受管区过期或标记冲突")
	}
	return nil
}
