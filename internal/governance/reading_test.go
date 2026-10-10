package governance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func readingFixture(t *testing.T) (string, map[string]any) {
	t.Helper()
	root := apTestRoot(t)
	apTestPut(t, root, "CONTEXT.md", "---\ncontext_schema_version: 1\n---\n## 流程术语\n| 术语 | 含义 | 英文标识 | 避免 / 备注 |\n|---|---|---|---|\n| Spec | 规格 | — | |\n## 业务术语\n| 术语 | 含义 | 英文标识 | 适用业务责任区 | 避免 / 备注 |\n|---|---|---|---|---|\n| 报告 | 提交的报告 | Report | Reporting | 避免：表单 |\n| 删除 | 删除报告 | Delete | Reporting | |\n")
	apTestPut(t, root, "spec.md", "# 目标\n实现两个行为\n## 验收\nAC1: 保存报告\nAC2: 删除报告\n")
	b, _ := os.ReadFile(filepath.Join(root, "spec.md"))
	notApplicable := map[string]any{"status": "not-applicable", "reason": "只读试验"}
	c := map[string]any{
		"schema_version": 3, "contract_id": "slice.test", "contract_version": "v1", "slice_id": "test", "status": "draft",
		"basis":         map[string]any{"spec": map[string]any{"ref": "spec.md", "version": "v1", "digest": "sha256:" + safefs.Digest(b)}, "ticket": "spec"},
		"scope":         map[string]any{"impacted_areas": []any{}, "implementation_path_policy": "external-repository-native", "project_roots": []any{root}, "allowed_write_paths": []any{"src"}, "forbidden_patterns": []any{"禁止覆盖资产"}, "context_plan": map[string]any{"unknown_rule": "不得遗漏的约束"}},
		"applicability": map[string]any{"frontend": notApplicable, "backend": notApplicable, "api": notApplicable, "cross_repo": notApplicable},
		"resolution":    map[string]any{"required_capabilities": []any{"cap.test"}, "required_skills": []any{"tdd"}, "recipe_ids": []any{}, "conditions": []any{}, "registry_digest": strings.Repeat("a", 64), "compiler_contract_digest": strings.Repeat("b", 64)},
		"acceptance":    map[string]any{"AC1": map[string]any{"source": "spec", "locator": "AC1:"}, "AC2": map[string]any{"source": "spec", "locator": "AC2:"}},
		"verification":  map[string]any{}, "work_units": []any{},
	}
	for _, id := range []string{"AC1", "AC2"} {
		semMap(c["verification"])[id] = map[string]any{"command": "go test ./...", "cwd": root, "expected_evidence": []any{id + ".log"}, "test_seams": []any{"public"}, "acceptance_refs": []any{id}}
		c["work_units"] = append(semList(c["work_units"]), map[string]any{"id": id, "behavior": id + strings.Repeat(" 执行当前行为", 30), "role_id": "role.test-engineer", "primary_skill": "tdd", "tdd_mode": "behavior-tdd", "verification_refs": []any{id}, "acceptance_refs": []any{id}})
	}
	apTestPut(t, root, "slice.yaml", map[string]any{"slice_contract": c})
	return root, c
}

func readResult(t *testing.T, group, action, root string, args map[string]string) map[string]any {
	t.Helper()
	r, e := RunContext(context.Background(), group, action, root, args)
	if e != nil {
		t.Fatal(e)
	}
	return r.(map[string]any)
}

func TestReadingSliceFocusedAndFailures(t *testing.T) {
	root, c := readingFixture(t)
	args := map[string]string{"kind": "slice", "file": "slice.yaml", "view": "task", "unit": "AC1"}
	r := readResult(t, "contract", "view", root, args)
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), "不得遗漏的约束") || strings.Contains(string(b), "删除报告") || strings.Contains(string(b), "markdown") {
		t.Fatalf("bad projection: %s", b)
	}
	if semMap(r["content"])["full_acceptance_ref"] == nil {
		t.Fatal("missing full acceptance binding")
	}
	if !strings.Contains(text(semMap(r["content"])["reading_stop_conditions"]), "new_impacts") {
		t.Fatal("missing execution stop conditions")
	}
	full, _ := json.Marshal(readResult(t, "contract", "view", root, map[string]string{"kind": "slice", "file": "slice.yaml", "view": "full"}))
	if len(b) >= len(full) {
		t.Fatalf("focused view did not shrink: %d >= %d", len(b), len(full))
	}
	for _, id := range []string{"", "unknown"} {
		args["unit"] = id
		if _, e := Run("contract", "view", root, args); e == nil {
			t.Fatalf("accepted unit %q", id)
		}
	}
	args["unit"] = "AC1"
	missing := semMap(c["basis"])["spec"].(map[string]any)["ref"]
	semMap(c["basis"])["spec"].(map[string]any)["ref"] = "missing.md"
	apTestPut(t, root, "slice.yaml", map[string]any{"slice_contract": c})
	if _, e := Run("contract", "view", root, args); e == nil {
		t.Fatal("accepted missing reference")
	}
	semMap(c["basis"])["spec"].(map[string]any)["ref"] = missing
	c["work_units"] = append(semList(c["work_units"]), semList(c["work_units"])[0])
	apTestPut(t, root, "slice.yaml", map[string]any{"slice_contract": c})
	if _, e := Run("contract", "view", root, args); e == nil {
		t.Fatal("accepted duplicate unit")
	}
	c["work_units"] = semList(c["work_units"])[:2]
	apTestPut(t, root, "slice.yaml", map[string]any{"slice_contract": c})
	apTestPut(t, root, "spec.md", "stale")
	if _, e := Run("contract", "view", root, args); e == nil {
		t.Fatal("accepted stale source")
	}
	args["file"] = "../escape.yaml"
	if _, e := Run("contract", "view", root, args); e == nil {
		t.Fatal("accepted path escape")
	}
}

func TestReadingContextAndRegistryRequireSelection(t *testing.T) {
	root, _ := readingFixture(t)
	for _, group := range []string{"context", "lifecycle"} {
		if _, e := Run(group, "query", root, map[string]string{"view": "agent"}); e == nil {
			t.Fatalf("%s accepted no selection", group)
		}
	}
	old := readResult(t, "context", "query", root, nil)
	terms := old["business_terms"].([]Term)
	if len(terms) == 0 {
		t.Fatal("fixture terms empty")
	}
	r := readResult(t, "context", "query", root, map[string]string{"view": "agent", "term-refs": terms[0].TermRef})
	content := semMap(r["content"])
	if content["business_terms"] != nil || len(content["terms"].([]Term)) != 1 {
		t.Fatalf("duplicate terms: %#v", content)
	}
	r = readResult(t, "lifecycle", "query", root, map[string]string{"view": "agent", "id": "stage.plan"})
	if text(semMap(semMap(r["content"])["entry"])["id"]) != "stage.plan" {
		t.Fatal("missing entry")
	}
}

func TestReadingStatusKeepsNestedBlockersAndEvidenceReferences(t *testing.T) {
	root, _ := readingFixture(t)
	apTestPut(t, root, "checkpoint.json", map[string]any{"schema_version": 1, "repository_mode": "project-instance", "feature_id": "test", "stage": "stage.plan", "next_work_unit": "work-unit.plan-requirements", "blockers": []any{}, "nested": map[string]any{"blockers": []any{map[string]any{"code": "nested-stop", "reason": "不得执行"}}}, "gates": map[string]any{"gate.plan-approved": map[string]any{"status": "blocked", "evidence": map[string]any{"ref": "missing-on-purpose.log", "digest": "sha256:" + strings.Repeat("f", 64), "body": strings.Repeat("large evidence body", 200)}}}})
	r := readResult(t, "lifecycle", "status", root, map[string]string{"view": "agent", "checkpoint": "checkpoint.json"})
	b, _ := json.Marshal(r)
	if !strings.Contains(string(b), "nested-stop") || !strings.Contains(string(b), "missing-on-purpose.log") || strings.Contains(string(b), "large evidence body") {
		t.Fatalf("bad status: %s", b)
	}
	if r["execution_allowed"] != false {
		t.Fatal("reading grants execution")
	}
}

func TestReadingStatusKeepsRegisteredConclusionsAndLegacyBlockers(t *testing.T) {
	root, c := readingFixture(t)
	cp := map[string]any{"schema_version": 1, "repository_mode": "project-instance", "feature_id": "test", "stage": "stage.plan", "next_work_unit": "work-unit.plan-requirements",
		"context_reconciliation": map[string]any{"status": "blocked", "reason": "术语待核验", "evidence_refs": []any{"context-check.json"}},
		"verification":           map[string]any{"status": "failed", "evidence_refs": []any{"test.log"}},
		"artifacts":              map[string]any{"spec": map[string]any{"status": "stale", "content": "必须保留的登记约束"}},
		"scope":                  map[string]any{"doubt_driven_review": map[string]any{"blocking_findings": []any{"禁止推进"}}, "readiness_blockers": []any{"证据待补齐"}},
		"evidence":               map[string]any{"body": map[string]any{"blocking_findings": []any{"证据中登记的阻断"}, "evidence_refs": []any{"nested-evidence.json"}, "stdout": "不要展开整个证据正文"}},
	}
	apTestPut(t, root, "checkpoint.json", cp)
	r := readResult(t, "lifecycle", "status", root, map[string]string{"view": "agent", "checkpoint": "checkpoint.json"})
	content := semMap(r["content"])
	b, _ := json.Marshal(content)
	for _, expected := range []string{"术语待核验", "failed", "必须保留的登记约束", "context-check.json", "test.log"} {
		if !strings.Contains(string(b), expected) {
			t.Fatalf("lost registered conclusion/reference: %s", expected)
		}
	}
	if len(semList(content["blockers"])) != 3 || len(semList(content["evidence_refs"])) != 3 || strings.Contains(string(b), "不要展开整个证据正文") {
		t.Fatalf("incomplete blocker/reference index: %#v", content)
	}
	semMap(c["scope"])["doubt_driven_review"] = cp["scope"]
	apTestPut(t, root, "slice.yaml", map[string]any{"slice_contract": c})
	r = readResult(t, "contract", "view", root, map[string]string{"view": "review", "kind": "slice", "file": "slice.yaml"})
	if len(semList(r["blockers"])) != 2 {
		t.Fatal("Slice omitted legacy nested blockers")
	}
}

func TestReadingObservationsRejectDriftAndReuseBytes(t *testing.T) {
	root, _ := readingFixture(t)
	r := &readingSession{s: newSemanticSession(context.Background(), root, nil), docs: map[string]any{}}
	b, e := r.bytes("spec.md")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.bytes("spec.md"); e != nil || len(r.s.v.observed) != 1 {
		t.Fatalf("read reuse: %v", e)
	}
	apTestPut(t, root, "spec.md", string(b)+"changed")
	apTestCode(t, r.s.finishInputs(), "INPUT_DRIFT")
	outside := filepath.Join(t.TempDir(), "outside.md")
	if e = os.WriteFile(outside, []byte("escape"), 0644); e != nil {
		t.Fatal(e)
	}
	if e = os.Symlink(outside, filepath.Join(root, "link.md")); e != nil {
		t.Fatal(e)
	}
	if _, e = r.bytes("link.md"); e == nil {
		t.Fatal("accepted symlink escape")
	}
}
