package governance

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestOrchestrationConsumesInstalledProfileAuthority(t *testing.T) {
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			root := apTestProfileRoot(t, profile)
			b, err := bundle.Load(profile)
			if err != nil {
				t.Fatal(err)
			}
			ref := guidanceContractRef(profile)
			file, present := b.Files[ref]
			if !present {
				t.Fatalf("installed Profile has no authoritative orchestration contract: %s", ref)
			}
			raw, err := file.Render(map[string]string{"projectName": "synthetic-governance", "businessDomain": "test-only", "teamSize": "2"})
			if err != nil {
				t.Fatal(err)
			}
			apTestPut(t, root, ref, raw)
			if profile != "spec" {
				apTestPut(t, root, guidanceContractRef("spec"), map[string]any{"schema_version": 1, "unrelated_profile": "full-spec"})
			}
			s := apTestSession(t, root)
			before := verificationTree(t, root)
			policy, selected, err := s.orchestration()
			if err != nil || selected != ref || policy["unrelated_profile"] != nil {
				t.Fatalf("actual %s Profile did not select its own policy: %s %v", profile, selected, err)
			}
			if profile == "design" {
				if _, err := s.planPolicy(); err != nil {
					t.Fatalf("Design cannot verify its genuine local Plan approval policy: %v", err)
				}
			}
			if !contractSame(before, verificationTree(t, root)) {
				t.Fatal("Profile policy selection wrote to the instance")
			}
		})
	}
	root := apTestProfileRoot(t, "design")
	apTestPut(t, root, ".template-spec/process/checkpoint-boundary.yaml", map[string]any{"schema_version": 1})
	if _, ref, err := apTestSession(t, root).orchestration(); err != nil || ref != ".template-spec/process/checkpoint-boundary.yaml" {
		t.Fatalf("legacy boundary fallback changed: %s %v", ref, err)
	}
}

// Initial checkpoints have no approved boundary and authorize no real action.
// Rules are the actual fixed Profile bytes, rather than a permissive test policy.
func TestCurrentCheckpointFourProfilesPublicAndUnknownStage(t *testing.T) {
	t.Setenv("PATH", "")
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		t.Run(profile, func(t *testing.T) {
			root := apTestProfileRoot(t, profile)
			b, err := bundle.Load(profile)
			if err != nil {
				t.Fatal(err)
			}
			for ref, f := range b.Files {
				if ref == trackerRef || ref == ".template-spec/process/checkpoint-boundary.yaml" || strings.HasPrefix(ref, ".agents/skills/yss-product-lifecycle/") {
					raw, e := f.Render(map[string]string{"projectName": "synthetic-governance", "businessDomain": "test-only", "teamSize": "2"})
					if e != nil {
						t.Fatal(e)
					}
					apTestPut(t, root, ref, raw)
				}
			}
			registry, err := load(root, approvalRegistryRef)
			if err != nil {
				t.Fatal(err)
			}
			stage := text(semMap(semList(registry["stages"])[0])["id"])
			layout, err := viewWorkLayout(apTestSession(t, root).v)
			if err != nil {
				t.Fatal(err)
			}
			ref := layout.Root + "/feature-demo/checkpoint.json"
			cp := map[string]any{"schema_version": 1, "repository_mode": "project-instance", "feature_id": "feature-demo", "mode": "audit", "status": "running", "stage": stage, "artifacts": map[string]any{}, "gates": map[string]any{}, "context_reconciliation": map[string]any{"status": "pending", "ref": nil, "evidence_refs": []any{}}, "next_work_unit": nil, "ticket_sync": map[string]any{}, "verification": map[string]any{}, "human_review": map[string]any{}, "git_checkpoint": map[string]any{}, "blockers": []any{}, "rollback": []any{}}
			apTestPut(t, root, ref, cp)
			result, err := RunContext(context.Background(), "lifecycle", "verify", root, map[string]string{"checkpoint": ref})
			if err != nil {
				t.Fatal(err)
			}
			if report := result.(*SemanticReport); report.Status != "passed" || report.ApprovalCreated || !report.ReadOnly {
				t.Fatalf("unsafe result: %+v", report)
			}
			apTestExportNativeFixture(t, root, profile, "lifecycle", "positive", []string{"lifecycle", "verify", "--checkpoint", ref}, 0)
			for _, action := range []string{"check", "verify"} {
				if _, err = RunContext(context.Background(), "project-ci", action, root, nil); err != nil {
					t.Fatalf("full CI %s: %v", action, err)
				}
				apTestExportNativeFixture(t, root, profile, "project-ci-"+action, "positive", []string{"project-ci", action}, 0)
			}
			// A selector is an additional required entry, never a way to narrow full CI.
			bad := map[string]any{}
			for key, value := range cp {
				bad[key] = value
			}
			bad["feature_id"] = "feature-bad"
			bad["stage"] = "stage.unknown"
			badRef := layout.Root + "/feature-bad/checkpoint.json"
			apTestPut(t, root, badRef, bad)
			for _, action := range []string{"check", "verify"} {
				for _, selected := range []string{"", ref} {
					result, err = RunContext(context.Background(), "project-ci", action, root, map[string]string{"checkpoint": selected})
					apTestCode(t, err, "PROJECT_CI_REJECTED")
					if !semHas(result.(*SemanticReport).Coverage["checkpoints"].([]string), badRef) {
						t.Fatal("full CI selector omitted the other checkpoint")
					}
				}
			}
			apTestExportNativeFixture(t, root, profile, "project-ci-selector", "refusal", []string{"project-ci", "verify", "--checkpoint", ref}, 1)
			if err = os.Remove(filepath.Join(root, badRef)); err != nil {
				t.Fatal(err)
			}
			cp["stage"] = "stage.unknown"
			apTestPut(t, root, ref, cp)
			_, err = RunContext(context.Background(), "lifecycle", "verify", root, map[string]string{"checkpoint": ref})
			apTestCode(t, err, "CHECKPOINT_BOUNDARY")
			apTestExportNativeFixture(t, root, profile, "lifecycle", "refusal", []string{"lifecycle", "verify", "--checkpoint", ref}, 1)
		})
	}
}

func TestCheckpointExecutionScopeCannotBypassTerminalWithoutTrace(t *testing.T) {
	root := apTestRoot(t)
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	const policy = ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml"
	raw, err := b.Files[policy].Render(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, policy, raw)
	apTestPut(t, root, ".yss-execution-scope.yaml", map[string]any{"schema_version": 1, "scope_id": "plan-to-backend"})
	const ref = "docs/.scratch/feature-demo/checkpoint.json"
	for _, tc := range []struct {
		name, status, mode string
		next               any
		wantPass           bool
	}{
		{"running-entry", "running", "orchestrate", "work-unit.entry-triage", true},
		{"completed-missing-terminal", "completed", "audit", nil, false},
		{"completed-retains-next", "completed", "audit", "work-unit.entry-triage", false},
		{"audit-unknown-next", "running", "audit", "work-unit.unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apTestPut(t, root, ref, map[string]any{"repository_mode": "project-instance", "stage": "stage.entry-triage", "status": tc.status, "mode": tc.mode, "next_work_unit": tc.next})
			s := apTestSession(t, root)
			err := s.verify("execution-scope", ref, nil)
			if (err == nil) != tc.wantPass {
				t.Fatalf("pass=%v want=%v error=%v", err == nil, tc.wantPass, err)
			}
		})
	}
}

func TestCompleteCIRegisteredScaffoldDispatch(t *testing.T) {
	root, reportRef, _, recorded, save := verificationFrontendFixture(t)
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	for ref, f := range b.Files {
		if ref == trackerRef || ref == ".template-spec/process/checkpoint-boundary.yaml" || strings.HasPrefix(ref, ".agents/skills/yss-product-lifecycle/") {
			raw, e := f.Render(map[string]string{"projectName": "synthetic", "businessDomain": "test-only", "teamSize": "2"})
			if e != nil {
				t.Fatal(e)
			}
			apTestPut(t, root, ref, raw)
		}
	}
	layout, err := viewWorkLayout(apTestSession(t, root).v)
	if err != nil {
		t.Fatal(err)
	}
	good := layout.Root + "/feature-demo/checkpoint.json"
	cp := map[string]any{"schema_version": 1, "repository_mode": "project-instance", "feature_id": "feature-demo", "mode": "audit", "status": "running", "stage": "stage.entry-triage", "artifacts": map[string]any{}, "gates": map[string]any{}, "context_reconciliation": map[string]any{"status": "pending", "ref": nil, "evidence_refs": []any{}}, "next_work_unit": nil, "ticket_sync": map[string]any{}, "verification": map[string]any{}, "human_review": map[string]any{}, "git_checkpoint": map[string]any{}, "blockers": []any{}, "rollback": []any{}}
	apTestPut(t, root, good, cp)

	if _, e := RunContext(context.Background(), "contract", "verify", root, map[string]string{"kind": "scaffold", "file": "docs/.scratch/frontend/scaffold-contract.json"}); e != nil {
		t.Fatalf("scaffold control: %v", e)
	}
	scaffold, err := load(root, "docs/.scratch/frontend/scaffold-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = scaffold
	binding, err := apTestSession(t, root).bind("docs/.scratch/frontend/scaffold-contract.json")
	if err != nil {
		t.Fatal(err)
	}
	cp["artifacts"] = map[string]any{"artifact.project-scaffold-contract": map[string]any{"status": "approved", "ref": "docs/.scratch/frontend/scaffold-contract.json", "digest": binding.Digest, "evidence_refs": []any{"docs/.scratch/frontend/scaffold-contract.json"}}}
	cp["verification"] = map[string]any{"commands": []any{}, "evidence_refs": []any{reportRef}}
	apTestPut(t, root, good, cp)
	if _, e := RunContext(context.Background(), "lifecycle", "verify", root, map[string]string{"checkpoint": good}); e != nil {
		t.Fatalf("current CP control: %v", e)
	}
	r, e := RunContext(context.Background(), "project-ci", "verify", root, nil)
	t.Logf("full CI report=%+v error=%v", r, e)
	if e != nil {
		t.Fatalf("full CI rejects known approved scaffold already independently checked: %v", e)
	}
	contractTestRetainFixture(t, root, "complete-ci-scaffold-verification", map[string]any{"group": "project-ci", "action": "verify", "isolate_root": true, "expected_exit": 0})
	semMap(semList(recorded["commands"])[0])["exit_code"] = 1
	save()
	r, e = RunContext(context.Background(), "project-ci", "verify", root, nil)
	apTestCode(t, e, "PROJECT_CI_REJECTED")
	found := false
	for _, check := range r.(*SemanticReport).Checks {
		found = found || check.SourceRef == reportRef && check.Status != "passed"
	}
	if !found {
		t.Fatal("full CI did not validate persisted verification results")
	}
	contractTestRetainFixture(t, root, "complete-ci-scaffold-verification-refusal", map[string]any{"group": "project-ci", "action": "verify", "isolate_root": true, "expected_exit": 1})
	// The approved implementation repository can be outside the governance
	// project. Report validation cannot depend on claim-map iteration order.
	external, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	contractRef := "docs/.scratch/frontend/scaffold-contract.json"
	contract, err := load(root, contractRef)
	if err != nil {
		t.Fatal(err)
	}
	contract["implementation_repository"], contract["target_output_dir"], contract["allowed_write_paths"] = external, external, []any{external}
	rawContract := apTestPut(t, root, contractRef, contract)
	manifest, err := load(root, "app/.yss/scaffold-generation.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest["target_output_dir"], manifest["contract_digest"] = external, "sha256:"+safefs.Digest(rawContract)
	apTestPut(t, external, ".yss/scaffold-generation.json", manifest)
	apTestPut(t, external, "pnpm-lock.yaml", "lockfileVersion: '9.0'\n")
	semMap(semList(recorded["commands"])[0])["exit_code"] = 0
	recorded["project_root"], recorded["scaffold_manifest_ref"] = external, filepath.Join(external, ".yss/scaffold-generation.json")
	apTestPut(t, root, reportRef, recorded)
	binding, err = apTestSession(t, root).bind(contractRef)
	if err != nil {
		t.Fatal(err)
	}
	semMap(semMap(cp["artifacts"])["artifact.project-scaffold-contract"])["digest"] = binding.Digest
	apTestPut(t, root, good, cp)
	for i := 0; i < 4; i++ {
		if report, err := RunContext(context.Background(), "project-ci", "verify", root, nil); err != nil {
			t.Fatalf("external approved repository CI: %v diagnostics=%+v", err, report.(*SemanticReport).Diagnostics)
		}
	}
}
