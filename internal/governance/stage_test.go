package governance_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func stageFixture(t *testing.T) string {
	t.Helper()
	root := canonicalTemp(t)
	put(t, root, "yss-project.yaml", "schema_version: 1\nrepository_mode: project-instance\n")
	put(t, root, "CONTEXT.md", validContext)
	put(t, root, ".template-spec/agents/issue-tracker.md", "---\ntracker:\n  platform: local-markdown\n  root: docs/.scratch\n  lifecycle_tracking_version: 1\n---\n# Tracker\n")
	put(t, root, ".template-spec/process/lifecycle-registry.yaml", "schema_version: 1\nstages:\n  - id: stage.plan\nwork_units:\n  - id: work-unit.plan-requirements\n  - id: work-unit.stage-decision\n")
	b, err := os.ReadFile("testdata/stage-tracking.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, ".template-spec/process/schemas/stage-tracking.schema.json", string(b))
	put(t, root, "docs/.scratch/report/checkpoint.json", `{"schema_version":1,"repository_mode":"project-instance","feature_id":"report","stage":"stage.plan","next_work_unit":"work-unit.plan-requirements","gates":{},"blockers":[]}`)
	put(t, root, "seeds.json", `[{"id":"requirements","title":"梳理报告要求","stage":"stage.plan","work_unit":"work-unit.plan-requirements","owner":"需求经理","scope":"梳理填报范围","acceptance":["明确范围"],"source_refs":["CONTEXT.md"]}]`)
	return root
}

// The retained runtime is an optional development oracle, never a runtime dependency.
func TestStageDifferentialRetainedValidator(t *testing.T) {
	module := os.Getenv("YSS_RETAINED_STAGE_MODULE")
	if module == "" {
		t.Skip("set YSS_RETAINED_STAGE_MODULE for retained Node differential development verification")
	}
	root := stageFixture(t)
	applyStagePlan(t, root, "register", "seeds.json")
	cp := "docs/.scratch/report/checkpoint.json"
	compare := func(wantValid bool) {
		t.Helper()
		_, nativeErr := governance.Run("stage", "status", root, map[string]string{"checkpoint": cp})
		code := `import fs from 'node:fs'; import {pathToFileURL} from 'node:url'; const {assertStageTracking}=await import(pathToFileURL(process.env.YSS_STAGE_ORACLE_MODULE).href); const cp=JSON.parse(fs.readFileSync(process.env.YSS_STAGE_ORACLE_ROOT+'/'+process.env.YSS_STAGE_ORACLE_CP,'utf8')); try { const result=assertStageTracking(cp,{root:process.env.YSS_STAGE_ORACLE_ROOT,checkpointRef:process.env.YSS_STAGE_ORACLE_CP}); console.log(JSON.stringify({valid:true,result})); } catch(e) {console.log(JSON.stringify({valid:false,error:e.message}));}`
		cmd := exec.Command("node", "--input-type=module", "--eval", code)
		cmd.Env = append(os.Environ(), "YSS_STAGE_ORACLE_MODULE="+module, "YSS_STAGE_ORACLE_ROOT="+root, "YSS_STAGE_ORACLE_CP="+cp)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("retained oracle unavailable: %v: %s", err, out)
		}
		var result struct {
			Valid bool   `json:"valid"`
			Error string `json:"error"`
		}
		if err = json.Unmarshal(out, &result); err != nil {
			t.Fatalf("oracle output: %v: %s", err, out)
		}
		if result.Valid != wantValid || (nativeErr == nil) != result.Valid {
			t.Fatalf("differential mismatch: native=%v retained=%s", nativeErr, out)
		}
	}
	compare(true)
	evidence := []byte("当前证据\n")
	put(t, root, "evidence.txt", string(evidence))
	patch := map[string]any{"id": "requirements", "progress": "completed", "completion": []any{map[string]any{"criterion": "明确范围", "evidence_refs": []any{map[string]any{"ref": "evidence.txt", "digest": "sha256:" + safefs.Digest(evidence)}}}}}
	b, _ := json.Marshal(patch)
	put(t, root, "patch.json", string(b))
	applyStagePlan(t, root, "update", "patch.json")
	compare(true)
	put(t, root, "evidence.txt", "旧证据变化\n")
	compare(false)
}

func applyStagePlan(t *testing.T, root, action, input string) {
	t.Helper()
	key := "items"
	if action == "update" {
		key = "item"
	}
	p, err := governance.Run("stage", action, root, map[string]string{"checkpoint": "docs/.scratch/report/checkpoint.json", key: input})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "plan.json", string(b))
	if _, err = governance.Run("stage", "apply", root, map[string]string{"plan-file": "plan.json"}); err != nil {
		t.Fatal(err)
	}
}

func TestStageRejectsDependencyCycleAndManualDefinition(t *testing.T) {
	for _, scenario := range []string{"cycle", "manual", "profile", "ready"} {
		t.Run(scenario, func(t *testing.T) {
			root := stageFixture(t)
			a := `{"id":"requirements","title":"要求","stage":"stage.plan","work_unit":"work-unit.plan-requirements","owner":"需求经理","scope":"要求","acceptance":["明确范围"],"source_refs":["CONTEXT.md"],"dependencies":["decision"]}`
			b := `{"id":"decision","title":"决定","stage":"stage.plan","work_unit":"work-unit.stage-decision","owner":"需求经理","scope":"决定","acceptance":["明确决定"],"source_refs":["CONTEXT.md"],"dependencies":["requirements"]}`
			if scenario == "manual" {
				b = strings.ReplaceAll(b, `"dependencies":["requirements"]`, `"dependencies":[]`)
				put(t, root, "docs/.scratch/report/work-items/decision.md", "人工文件")
			}
			if scenario == "profile" {
				put(t, root, ".template-spec/process/harness-profile.yaml", "profile_id: harness.backend-delivery\nallowed_work_units: []\n")
			}
			if scenario == "ready" {
				a = strings.ReplaceAll(a, `"dependencies":["decision"]`, `"dependencies":[],"progress":"ready-for-agent"`)
				b = ""
			}
			seeds := "[" + a
			if b != "" {
				seeds += "," + b
			}
			seeds += "]"
			put(t, root, "seeds.json", seeds)
			if _, err := governance.Run("stage", "register", root, map[string]string{"checkpoint": "docs/.scratch/report/checkpoint.json", "items": "seeds.json"}); err == nil {
				t.Fatal("invalid stage registration accepted")
			}
			cp, _ := os.ReadFile(filepath.Join(root, "docs/.scratch/report/checkpoint.json"))
			if strings.Contains(string(cp), "stage_tracking") {
				t.Fatal("rejected plan wrote checkpoint")
			}
		})
	}
}

func TestStagePlanDriftCompletionEvidenceAndStalePropagation(t *testing.T) {
	root := stageFixture(t)
	cp := "docs/.scratch/report/checkpoint.json"
	plan, err := governance.Run("stage", "register", root, map[string]string{"checkpoint": cp, "items": "seeds.json"})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(plan)
	put(t, root, "plan.json", string(b))
	put(t, root, "CONTEXT.md", validContext+"\n来源变化\n")
	if _, err = governance.Run("stage", "apply", root, map[string]string{"plan-file": "plan.json"}); err == nil {
		t.Fatal("stale source plan accepted")
	}
	applyStagePlan(t, root, "register", "seeds.json")
	put(t, root, "patch.json", `{"id":"requirements","progress":"completed"}`)
	if _, err = governance.Run("stage", "update", root, map[string]string{"checkpoint": cp, "item": "patch.json"}); err == nil {
		t.Fatal("completed without evidence accepted")
	}
	evidence := []byte("报告范围已经核验\n")
	put(t, root, "evidence.txt", string(evidence))
	patch := map[string]any{"id": "requirements", "progress": "completed", "completion": []any{map[string]any{"criterion": "明确范围", "evidence_refs": []any{map[string]any{"ref": "evidence.txt", "digest": "sha256:" + safefs.Digest(evidence)}}}}}
	b, _ = json.Marshal(patch)
	put(t, root, "patch.json", string(b))
	applyStagePlan(t, root, "update", "patch.json")
	if _, err = governance.Run("stage", "status", root, map[string]string{"checkpoint": cp}); err != nil {
		t.Fatal(err)
	}
	put(t, root, "evidence.txt", "证据变化\n")
	if _, err = governance.Run("stage", "status", root, map[string]string{"checkpoint": cp}); err == nil {
		t.Fatal("completed evidence drift accepted")
	}
}

func TestStageDependenciesBlockRunningAndCancelledRequiresReason(t *testing.T) {
	root := stageFixture(t)
	cp := "docs/.scratch/report/checkpoint.json"
	seed := `[{"id":"requirements","title":"要求","stage":"stage.plan","work_unit":"work-unit.plan-requirements","owner":"需求经理","scope":"要求","acceptance":["明确范围"],"source_refs":["CONTEXT.md"]},{"id":"decision","title":"决定","stage":"stage.plan","work_unit":"work-unit.stage-decision","owner":"需求经理","scope":"决定","acceptance":["明确决定"],"source_refs":["CONTEXT.md"],"dependencies":["requirements"]}]`
	put(t, root, "seeds.json", seed)
	applyStagePlan(t, root, "register", "seeds.json")
	for _, patch := range []string{`{"id":"decision","progress":"running"}`, `{"id":"requirements","progress":"cancelled"}`} {
		put(t, root, "patch.json", patch)
		if _, err := governance.Run("stage", "update", root, map[string]string{"checkpoint": cp, "item": "patch.json"}); err == nil {
			t.Fatal("invalid progress accepted")
		}
	}
	put(t, root, "patch.json", `{"id":"requirements","progress":"cancelled","cancellation_reason":"用户取消当前范围"}`)
	applyStagePlan(t, root, "update", "patch.json")
	put(t, root, "CONTEXT.md", validContext+"\n来源变更\n")
	result, err := governance.Run("stage", "status", root, map[string]string{"checkpoint": cp})
	if err != nil {
		t.Fatal(err)
	}
	stale := result.(map[string]any)["stale_item_ids"].([]string)
	if len(stale) != 2 {
		t.Fatalf("stale source did not propagate: %#v", stale)
	}
}

func TestStageRegistrationUsesExplicitPlanAndKeepsGatesUnapproved(t *testing.T) {
	root := stageFixture(t)
	cp := "docs/.scratch/report/checkpoint.json"
	before, err := os.ReadFile(filepath.Join(root, cp))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := governance.Run("stage", "register", root, map[string]string{"checkpoint": cp, "items": "seeds.json"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(root, cp))
	if err != nil || string(before) != string(after) {
		t.Fatal("plan changed authoritative checkpoint")
	}
	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	put(t, root, "registration-plan.json", string(b))
	if _, err = governance.Run("stage", "register", root, map[string]string{"apply": "true", "plan-file": "registration-plan.json"}); err != nil {
		t.Fatal(err)
	}
	v, err := governance.Run("stage", "status", root, map[string]string{"checkpoint": cp})
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if m["status"] != "valid" || m["approval_created"] != false {
		t.Fatalf("unexpected tracking status: %#v", m)
	}
	b, err = os.ReadFile(filepath.Join(root, cp))
	if err != nil {
		t.Fatal(err)
	}
	var checkpoint map[string]any
	if err = json.Unmarshal(b, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if len(checkpoint["gates"].(map[string]any)) != 0 {
		t.Fatal("work registration created stage approval")
	}
	tracking := checkpoint["stage_tracking"].(map[string]any)
	items := tracking["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["progress"] != "pending" {
		t.Fatalf("registration inferred progress: %#v", tracking)
	}
}
