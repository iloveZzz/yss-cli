package approval

import (
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/schema"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

var decisionDimensions = []string{"business_scope", "acceptance", "contract_commitments", "authorization", "risk_acceptance", "quality", "external_commitments"}

func comparisonModel(ref string, raw []byte) (any, string, error) {
	ext := strings.ToLower(filepath.Ext(ref))
	if ext == ".md" {
		front, body, err := kernel.Frontmatter(raw)
		var value any = map[string]any{}
		if err != nil {
			if strings.HasPrefix(string(raw), "---") {
				return nil, "", err
			}
			body = raw
		} else {
			value, err = schema.Parse(front)
			if err != nil {
				return nil, "", err
			}
		}
		return map[string]any{"frontmatter": value, "body": strings.ReplaceAll(string(body), "\r\n", "\n")}, "markdown", nil
	}
	if ext != ".json" && ext != ".yaml" && ext != ".yml" {
		return nil, "", fmt.Errorf("不支持的比较文档格式: %s", ref)
	}
	value, err := schema.Parse(raw)
	return value, "structured", err
}
func pointerParts(pointer string) ([]string, error) {
	if !strings.HasPrefix(pointer, "/") || strings.Contains(pointer, "*") {
		return nil, fmt.Errorf("须明确非根JSON Pointer: %s", pointer)
	}
	parts := strings.Split(pointer[1:], "/")
	for i, p := range parts {
		for j := 0; j < len(p); j++ {
			if p[j] == '~' {
				if j+1 >= len(p) || (p[j+1] != '0' && p[j+1] != '1') {
					return nil, fmt.Errorf("无效JSON Pointer")
				}
				j++
			}
		}
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(p, "~1", "/"), "~0", "~")
	}
	return parts, nil
}
func pointerValue(model any, pointer string) (any, bool) {
	parts, err := pointerParts(pointer)
	if err != nil {
		return nil, false
	}
	value := model
	for _, part := range parts {
		if array, ok := value.([]any); ok {
			index, err := strconv.Atoi(part)
			if err != nil || index < 0 || strconv.Itoa(index) != part || index >= len(array) {
				return nil, false
			}
			value = array[index]
		} else {
			m, ok := kernel.Object(value)
			if !ok {
				return nil, false
			}
			value, ok = m[part]
			if !ok {
				return nil, false
			}
		}
	}
	return value, true
}
func pointerChild(path, parent string) bool {
	return path == parent || strings.HasPrefix(path, parent+"/")
}
func pointerIntersects(a, b string) bool { return pointerChild(a, b) || pointerChild(b, a) }
func escapePointer(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1")
}
func presentValue(value any, present bool) map[string]any {
	out := map[string]any{"present": present}
	if present {
		out["value"] = value
	}
	return out
}
func actualDifferences(before, after any) map[string]map[string]any {
	out := map[string]map[string]any{}
	var walk func(string, any, bool, any, bool)
	walk = func(path string, a any, ap bool, b any, bp bool) {
		if ap == bp && kernel.ApEqual(a, b) {
			return
		}
		am, aok := kernel.Object(a)
		bm, bok := kernel.Object(b)
		if ap && bp && aok && bok {
			keys := map[string]bool{}
			for k := range am {
				keys[k] = true
			}
			for k := range bm {
				keys[k] = true
			}
			ordered := []string{}
			for k := range keys {
				ordered = append(ordered, k)
			}
			sort.Strings(ordered)
			for _, k := range ordered {
				av, ax := am[k]
				bv, bx := bm[k]
				walk(path+"/"+escapePointer(k), av, ax, bv, bx)
			}
			return
		}
		out[path] = map[string]any{"path": path, "before": presentValue(a, ap), "after": presentValue(b, bp)}
	}
	walk("", before, true, after, true)
	return out
}
func protectedSelectors(target map[string]any, model any) ([]string, error) {
	selectors := []string{}
	for _, dimension := range decisionDimensions {
		paths := kernel.ApMap(target["decision_basis"])[dimension]
		if !kernel.ApUnique(paths) {
			return nil, fmt.Errorf("缺少决定依据选择器: %s", dimension)
		}
		for _, p := range kernel.ApStrings(paths) {
			if _, ok := pointerValue(model, p); !ok {
				return nil, fmt.Errorf("原批准选择器不指向已形成字段: %s", p)
			}
			selectors = append(selectors, p)
		}
	}
	if target["boundary"] == "implementation-scope" {
		selectors = append(selectors, "/slices")
	}
	return selectors, nil
}
func validateContinuationPaths(target map[string]any, model any, protected []string) error {
	for class, values := range kernel.ApMap(target["continuation_paths"]) {
		if !kernel.ApContains([]string{"implementation-detail", "evidence-supplement"}, class) || !kernel.ApUnique(values) {
			return fmt.Errorf("未知延续分类或未明确路径")
		}
		for _, p := range kernel.ApStrings(values) {
			if _, ok := pointerValue(model, p); !ok {
				return fmt.Errorf("延续路径不是原批准的具体字段: %s", p)
			}
			for _, limit := range protected {
				if pointerIntersects(p, limit) {
					return fmt.Errorf("延续路径覆盖决定依据或实施范围: %s", p)
				}
			}
		}
	}
	return nil
}
func compareContinuation(s *kernel.SemanticSession, target, comparison map[string]any, beforeRaw, afterRaw []byte) error {
	bad := func(m string) error { return s.Reject("user-decision-continuation-invalid", m) }
	before, beforeKind, err := comparisonModel(kernel.Text(target["subject_ref"]), beforeRaw)
	if err != nil {
		return bad(err.Error())
	}
	after, afterKind, err := comparisonModel(kernel.Text(kernel.ApMap(comparison["after"])["ref"]), afterRaw)
	if err != nil {
		return bad(err.Error())
	}
	if beforeKind != afterKind {
		return bad("前后比较模型不同")
	}
	protected, err := protectedSelectors(target, before)
	if err != nil {
		return bad(err.Error())
	}
	if err = validateContinuationPaths(target, before, protected); err != nil {
		return bad(err.Error())
	}
	for _, p := range protected {
		a, ap := pointerValue(before, p)
		b, bp := pointerValue(after, p)
		if !ap || !bp || !kernel.ApEqual(a, b) {
			return bad("原批准决定依据或实施范围发生变化: " + p)
		}
	}
	if comparison["decision_basis_unchanged"] != true || !kernel.ApIsArray(comparison["material_changes"]) || len(kernel.ApArray(comparison["material_changes"])) != 0 {
		return bad("存在实质或未知变化")
	}
	actual := actualDifferences(before, after)
	seen := map[string]bool{}
	refs := map[string]bool{}
	if len(kernel.ApRows(comparison["differences"])) == 0 {
		return bad("缺少实际差异证据")
	}
	for _, binding := range kernel.ApRows(comparison["differences"]) {
		ref := kernel.Text(binding["ref"])
		if refs[ref] {
			return bad("差异证据重复")
		}
		refs[ref] = true
		if _, err = s.EvidenceAsset(binding); err != nil {
			return err
		}
		diff, err := s.Doc(ref)
		if err != nil {
			return err
		}
		if kernel.ApNumber(diff["schema_version"]) != 1 || len(diff) != 2 || !kernel.ApIsArray(diff["changes"]) || len(kernel.ApRows(diff["changes"])) != len(kernel.ApArray(diff["changes"])) {
			return bad("差异载体无效")
		}
		class := kernel.Text(binding["classification"])
		if strings.TrimSpace(kernel.Text(binding["reason"])) == "" {
			return bad("缺少差异理由")
		}
		if class == "presentation-only" {
			if len(actual) != 0 || len(kernel.ApArray(diff["changes"])) != 0 {
				return bad("展示变化实际改变了文档值或正文")
			}
		} else if !kernel.ApContains([]string{"implementation-detail", "evidence-supplement"}, class) || len(kernel.ApArray(diff["changes"])) == 0 {
			return bad("未知分类或空语义差异")
		}
		for _, change := range kernel.ApRows(diff["changes"]) {
			path := kernel.Text(change["path"])
			if seen[path] || actual[path] == nil || !kernel.ApEqual(change, actual[path]) {
				return bad("差异遗漏、伪造、重复或值不符: " + path)
			}
			covered := false
			for _, allow := range kernel.ApStrings(kernel.ApMap(target["continuation_paths"])[class]) {
				covered = covered || pointerChild(path, allow)
			}
			if !covered {
				return bad("变化没有原批准的明确分类范围: " + path)
			}
			for _, p := range protected {
				if pointerIntersects(path, p) {
					return bad("变化覆盖受保护决定依据")
				}
			}
			seen[path] = true
		}
	}
	if len(seen) != len(actual) {
		return bad("差异证据未逐项覆盖全部实际变化")
	}
	return nil
}
