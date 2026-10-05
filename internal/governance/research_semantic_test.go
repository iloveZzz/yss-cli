package governance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func researchTestFixture(t *testing.T, competitive bool) (string, map[string]any) {
	t.Helper()
	root := apTestRoot(t)
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	for ref, f := range b.Files {
		if strings.HasPrefix(ref, ".agents/skills/yss-research/") || semHas([]string{"scripts/lib/json-schema.mjs", "scripts/lib/validation-phase.mjs", ".template-spec/plan/templates/competitive-matrix-template.md", ".template-spec/plan/templates/competitive-analysis-template.md"}, ref) {
			raw, e := f.Render(nil)
			if e != nil {
				t.Fatal(e)
			}
			apTestPut(t, root, ref, raw)
		}
	}
	var data map[string]any
	raw, err := os.ReadFile(filepath.Join(root, ".agents/skills/yss-research/assets/evidence-template.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	evidence := semMap(semList(data["evidence_items"])[0])
	evidence["source_level"] = "primary"
	evidence["source_class"] = "official-documentation"
	evidence["source_ref"] = "fixture-public-document.md"
	claim := semMap(semList(data["claims"])[0])
	claim["claim_kind"] = "technical-fact"
	claim["statement"] = "固定资料明确支持工作流审批。"
	if competitive {
		data["competitive_analysis"] = map[string]any{"schema_version": 1, "output_selection": "both", "as_of": "2026-10-02", "comparison_scope": "固定资料中的团队版审批能力", "competitors": []any{map[string]any{"id": "product-a", "name": "样例 A", "type": "direct", "product": "样例产品", "version": "1", "edition": "team", "region": "global"}}, "capabilities": []any{map[string]any{"id": "approval", "module": "流程", "name": "审批", "definition": "指定审批人确认后继续流程", "user_value": "责任可追溯"}}, "assessments": []any{map[string]any{"competitor_id": "product-a", "capability_id": "approval", "status": "supported", "claim_refs": []any{"claim-001"}, "limitations": []any{}, "gap": nil}}, "artifacts": map[string]any{"matrix": "demo-competitive-matrix.md", "report": "demo-competitive-analysis.md"}}
	}
	apTestPut(t, root, "demo-evidence.yaml", data)
	brief, err := os.ReadFile(filepath.Join(root, ".agents/skills/yss-research/assets/research-brief-template.md"))
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, "demo-research-brief.md", string(brief)+"\nclaim-001\n")
	if competitive {
		claims, evidence, searches := map[string]map[string]any{}, map[string]map[string]any{}, map[string]map[string]any{}
		for _, pair := range []struct {
			key  string
			dest map[string]map[string]any
		}{{"claims", claims}, {"evidence_items", evidence}, {"search_log", searches}} {
			for _, v := range semList(data[pair.key]) {
				r := semMap(v)
				pair.dest[text(r["id"])] = r
			}
		}
		for _, kind := range []string{"matrix", "report"} {
			filename := text(semMap(semMap(data["competitive_analysis"])["artifacts"])[kind])
			apTestPut(t, root, filename, "# 人工文字\n"+competitiveManagedBody(data, kind, claims, evidence, searches)+"\n")
		}
	}
	return root, data
}

func TestResearchNativePackageRecordedCompletion(t *testing.T) {
	for _, competitive := range []bool{false, true} {
		t.Run(map[bool]string{false: "basic", true: "competitive"}[competitive], func(t *testing.T) {
			root, _ := researchTestFixture(t, competitive)
			s := newSemanticSession(context.Background(), root, nil)
			bindings, err := validateResearchPackageSemantic(s, "demo-research-brief.md", "demo-evidence.yaml")
			if err != nil {
				t.Fatal(err)
			}
			bind := func(ref string) map[string]any {
				b, err := os.ReadFile(filepath.Join(root, ref))
				if err != nil {
					t.Fatal(err)
				}
				return map[string]any{"ref": ref, "digest": safefs.Digest(b)}
			}
			apTestPut(t, root, "context.json", map[string]any{"status": "not-applicable", "reason": "synthetic test"})
			apTestPut(t, root, "stdout.log", "actual synthetic log")
			apTestPut(t, root, "stderr.log", "")
			inputs := map[string]any{"brief": bind("demo-research-brief.md"), "evidence": bind("demo-evidence.yaml"), "validator": bind(researchValidator)}
			if competitive {
				inputs["competitive"] = bindings
			}
			apTestPut(t, root, "verification.json", map[string]any{"schema_version": 1, "kind": "maintenance-research-verification", "inputs": inputs, "command": []any{"node", researchValidator, "demo-research-brief.md", "demo-evidence.yaml"}, "started_at": "2026-10-05T00:00:00Z", "completed_at": "2026-10-05T00:00:01Z", "exit_code": 0, "stdout": bind("stdout.log"), "stderr": bind("stderr.log")})
			refs := []any{"context.json", "verification.json", "demo-research-brief.md", "demo-evidence.yaml"}
			for _, b := range bindings {
				refs = append(refs, semMap(b)["ref"])
			}
			state := map[string]any{"research_verification": bind("verification.json"), "context_reconciliation": map[string]any{"status": "not-applicable", "reason": "test-only", "ref": "context.json"}, "evidence_refs": refs}
			for _, k := range []string{"blocking_signals", "drift", "violation", "new_impacts", "stale_candidates"} {
				state[k] = []any{}
			}
			apTestPut(t, root, "checkpoint.yaml", map[string]any{"context_reconciliation": state["context_reconciliation"], "decision_state": state})
			if err := verifyResearchCompletionSemantic(newSemanticSession(context.Background(), root, nil), "checkpoint.yaml", nil); err != nil {
				t.Fatal(err)
			}
			for _, mutation := range []string{"unreferenced", "blocked", "stale", "unknown-validator"} {
				t.Run(mutation, func(t *testing.T) {
					raw, err := os.ReadFile(filepath.Join(root, "checkpoint.yaml"))
					if err != nil {
						t.Fatal(err)
					}
					var cp map[string]any
					if err = json.Unmarshal(raw, &cp); err != nil {
						t.Fatal(err)
					}
					st := semMap(cp["decision_state"])
					switch mutation {
					case "unreferenced":
						st["evidence_refs"] = []any{}
					case "blocked":
						st["violation"] = []any{"finding"}
					case "stale":
						semMap(st["research_verification"])["digest"] = strings.Repeat("0", 64)
					case "unknown-validator":
						apTestPut(t, root, researchValidator, "changed rule")
					}
					apTestPut(t, root, "bad.yaml", cp)
					if err := verifyResearchCompletionSemantic(newSemanticSession(context.Background(), root, nil), "bad.yaml", nil); err == nil {
						t.Fatal("invalid completion accepted")
					}
				})
			}
		})
	}
}

// Development oracle only. Production validation has no interpreter dispatch.
func TestResearchFixedSourceDifferential(t *testing.T) {
	node := os.Getenv("YSS_RESEARCH_ORACLE_NODE")
	if node == "" {
		t.Skip("fixed-source development oracle not configured")
	}
	for _, competitive := range []bool{false, true} {
		root, _ := researchTestFixture(t, competitive)
		cmd := exec.Command(node, filepath.Join(root, researchValidator), filepath.Join(root, "demo-research-brief.md"), filepath.Join(root, "demo-evidence.yaml"))
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("oracle positive: %v %s", err, out)
		}
		if competitive {
			cmd = exec.Command(node, filepath.Join(root, ".agents/skills/yss-research/scripts/render-competitive-outputs.mjs"), filepath.Join(root, "demo-evidence.yaml"))
			out, err = cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("renderer: %v %s", err, out)
			}
			if _, err = validateResearchPackageSemantic(newSemanticSession(context.Background(), root, nil), "demo-research-brief.md", "demo-evidence.yaml"); err != nil {
				t.Fatalf("native disagrees with fixed renderer: %v", err)
			}
		}
		raw, err := os.ReadFile(filepath.Join(root, "demo-evidence.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]any
		if err = json.Unmarshal(raw, &data); err != nil {
			t.Fatal(err)
		}
		semMap(semList(data["claims"])[0])["evidence_refs"] = []any{"unknown"}
		apTestPut(t, root, "demo-evidence.yaml", data)
		cmd = exec.Command(node, filepath.Join(root, researchValidator), filepath.Join(root, "demo-research-brief.md"), filepath.Join(root, "demo-evidence.yaml"))
		if out, err = cmd.CombinedOutput(); err == nil {
			t.Fatalf("oracle negative accepted: %s", out)
		}
		if _, err = validateResearchPackageSemantic(newSemanticSession(context.Background(), root, nil), "demo-research-brief.md", "demo-evidence.yaml"); err == nil {
			t.Fatal("native negative accepted")
		}
	}
}
