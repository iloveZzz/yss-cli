package reading

import (
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"path/filepath"
	"strings"
)

// readingDependencyScope 按文件所在位置归类：位于项目根之内的记为 project（引用相对项目根），
// 否则记为 tool（引用相对工具根）。实例自带 scripts/ 时工具根即项目根，全部记为 project。
func readingDependencyScope(s *kernel.SemanticSession, toolRef string) (string, string) {
	toolRoot := s.Args["tool-root"]
	if toolRoot == "" || toolRoot == s.Root {
		return "project", toolRef
	}
	rel, err := filepath.Rel(s.Root, filepath.Join(toolRoot, filepath.FromSlash(toolRef)))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "tool", toolRef
	}
	return "project", filepath.ToSlash(rel)
}

func init() {
	kernel.RegisterSemanticValidator("reading-transition", verifyReadingTransitionSemantic)
	kernel.RegisterRoleIndependentValidator("reading-transition", verifyReadingTransitionSemantic)
}

func verifyReadingTransitionSemantic(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	policy, present, err := loadReadingPolicy(s)
	if err != nil {
		return err
	}
	if !present {
		s.Report.Applicability = append(s.Report.Applicability, map[string]any{"id": "reading-views", "status": "not-applicable", "reason": "项目未启用 managed 阅读视图"})
		return nil
	}
	if policy["mode"] != "managed" || !kernel.SemHas(policy["checkpoints"], ref) {
		s.Report.Applicability = append(s.Report.Applicability, map[string]any{"id": "reading-views", "status": "not-applicable", "reason": "当前 checkpoint 不要求 managed 阅读视图"})
		return nil
	}
	return verifyReadingCurrent(s, ref)
}

// verifyReadingCurrent 要求 checkpoint 的 managed 阅读材料与当前原生重算逐项一致。
// 它不判断策略是否启用；策略门禁由调用方负责。
func verifyReadingCurrent(s *kernel.SemanticSession, ref string) error {
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
	v2 := peekManifestVersion(s, base+"/reading/.manifest.json") == 2
	expected, err := BuildReadingMode(s, ref, v2)
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
	if manifest["checkpoint_ref"] != ref {
		return s.Reject("READING_MANIFEST_STALE", "阅读清单的 checkpoint 或固定工具闭包不匹配")
	}
	if v2 {
		if err = verifyRendererV2(s, manifest["renderer"]); err != nil {
			return err
		}
	} else if !kernel.ApEqual(manifest["renderer"], expected.Renderer) {
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
		scope, ref := readingDependencyScope(s, key)
		dependencies[scope+"\x00"+ref] = value
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
	manifestBytes, err := s.Bytes(base + "/reading/.manifest.json")
	if err != nil {
		return err
	}
	if err = verifyReadingManifestOrder(s, manifestBytes, expected.OutputOrder, v2); err != nil {
		return err
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

// verifyReadingManifestOrder 要求清单的键序、依赖行序与渲染器写出的序列化一致：渲染器按序列化文本比较清单，
// 所以仅顺序不同的清单也算过期。依赖按 scope+ref 的本地化顺序排列，缺失文件的行（摘要为空）追加在其后；
// 缺失行彼此之间的相对顺序取决于渲染器的发现顺序，这里不比较，其集合已由前面的依赖闭包比较核对。
func verifyReadingManifestOrder(s *kernel.SemanticSession, raw []byte, outputOrder []string, v2 bool) error {
	stale := func() error {
		return s.Reject("READING_MANIFEST_STALE", "阅读清单的键序或依赖顺序与渲染器写出的不一致")
	}
	node, err := readingParse(raw)
	if err != nil {
		return s.Unavailable("INPUT", err.Error())
	}
	keysOf := func(n *readingNode) []string {
		keys := []string{}
		if n != nil {
			for _, p := range n.Pairs {
				keys = append(keys, p.Key)
			}
		}
		return keys
	}
	same := func(a, b []string) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}
	order := collate.New(language.English)
	if !same(keysOf(node), []string{"schema_version", "checkpoint_ref", "renderer", "dependencies", "outputs", "navigation", "diagnostics"}) ||
		!same(keysOf(node.get("navigation")), []string{"ref", "block_digest"}) ||
		!same(keysOf(node.get("outputs")), outputOrder) {
		return stale()
	}
	renderer := keysOf(node.get("renderer"))
	if v2 {
		if !same(renderer, []string{"id", "rules_version"}) {
			return stale()
		}
	} else {
		for i := 1; i < len(renderer); i++ {
			if order.CompareString(renderer[i-1], renderer[i]) > 0 {
				return stale()
			}
		}
	}
	previous, missing := "", false
	if rows := node.get("dependencies"); rows != nil {
		for _, row := range rows.Items {
			if !same(keysOf(row), []string{"scope", "ref", "digest"}) {
				return stale()
			}
			key := kernel.Text(row.get("scope").Value) + kernel.Text(row.get("ref").Value)
			if row.get("digest").Value == nil {
				missing = true
				continue
			}
			if missing || previous != "" && order.CompareString(previous, key) > 0 {
				return stale()
			}
			previous = key
		}
	}
	return nil
}

// verifyRendererV2 校验 v2 清单的渲染器标识：id 必须是 yss-reading，rules_version 必须等于当前规则版本。
// 更高的版本由更新的 yss 写出，旧程序无法判断其输出，按不可用拒绝；更低的版本需要重新渲染。
func verifyRendererV2(s *kernel.SemanticSession, value any) error {
	renderer := kernel.SemMap(value)
	version, isInteger := kernel.Integer(renderer["rules_version"])
	if len(renderer) != 2 || renderer["id"] != RendererID || !isInteger {
		return s.Reject("READING_MANIFEST_STALE", "阅读清单的渲染器标识不是 "+RendererID)
	}
	switch {
	case version > RulesVersion:
		return s.Unavailable("CAPABILITY", fmt.Sprintf("阅读清单由更新的渲染规则 rules_version=%d 写出，当前 yss 只支持 %d；请升级 yss", version, RulesVersion))
	case version < RulesVersion:
		return s.Reject("READING_MANIFEST_STALE", fmt.Sprintf("阅读清单的渲染规则 rules_version=%d 已过期（当前 %d）；运行 yss reading render 迁移", version, RulesVersion))
	}
	return nil
}
