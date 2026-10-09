package governance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func progressionFixture(t *testing.T) (string, string) {
	t.Helper()
	root := apTestRoot(t)
	ref := ".work/unrelated-directory/checkpoint.yaml"
	apTestPut(t, root, ref, "schema_version: 1\nrepository_mode: project-instance\nfeature_id: feature.demo\nstage: stage.entry-triage\nnext_work_unit: work-unit.plan-opportunity\nblockers: []\ngates: {}\n")
	apTestPut(t, root, trackerRef, "---\ntracker:\n  platform: local-markdown\n  root: .work\n---\n# 工作\n")
	apTestPut(t, root, ".work/unrelated-directory/map.md", "---\ncheckpoint_ref: "+ref+"\n---\n# 功能\n")
	apTestPut(t, root, guidanceContractRef("spec"), "schema_version: 1\nprogression_target:\n  schema_version: 1\n  required_capabilities: [lifecycle-target-v1]\n  writer_profiles: [spec]\n  config_file: progression-target.json\n  default_target: business-accepted\n  completion_policy:\n    spec-approved:\n      required_gates: [gate.spec-baseline-approved]\n      required_checks: [business-tickets-draft]\n    business-accepted:\n      required_gates: [gate.delivery-accepted]\n      required_checks: [business-acceptance]\n")
	return root, ref
}

func progressionTestInput(cp, target string) map[string]any {
	return map[string]any{"schema_version": 1, "kind": "lifecycle-progression-target", "feature_id": "feature.demo", "checkpoint_ref": cp, "target": target, "intent_source": "已确认用户请求", "consumers": []any{}}
}

func progressionTestPlan(t *testing.T, root, cp string) string {
	t.Helper()
	apTestPut(t, root, "target-input.json", progressionTestInput(cp, "spec-approved"))
	file := filepath.Join(t.TempDir(), "plan.json")
	if _, err := RunContext(context.Background(), "lifecycle", "target", root, map[string]string{"checkpoint": cp, "input": "target-input.json", "plan": "true", "out": file}); err != nil {
		t.Fatal(err)
	}
	return file
}

func progressionInventory(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(file string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files[file] = info.Mode().String() + ":" + safefs.Digest(raw)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestLifecycleTargetRejectsUntrustedBindingsAndCapability(t *testing.T) {
	for _, variant := range []string{"unknown-version", "unknown-target", "wrong-feature", "wrong-source-profile", "future-wrong-profile", "duplicate-map", "old-policy", "scope-upper-bound"} {
		t.Run(variant, func(t *testing.T) {
			root, cp := progressionFixture(t)
			input := progressionTestInput(cp, "spec-approved")
			switch variant {
			case "unknown-version":
				input["schema_version"] = 2
			case "unknown-target":
				input["target"] = "publish"
			case "wrong-feature":
				input["feature_id"] = "feature.other"
			case "wrong-source-profile":
				raw, _ := os.ReadFile(filepath.Join(root, cp))
				apTestPut(t, root, cp, string(raw)+"profile_id: harness.frontend-delivery\n")
			case "future-wrong-profile":
				target := t.TempDir()
				apTestPut(t, target, ".template-spec/process/harness-profile.yaml", "schema_version: 2\nprofile_id: harness.frontend-delivery\n")
				input["consumers"] = []any{map[string]any{"profile": "backend", "root": target, "checkpoint_ref": ".work/future/checkpoint.yaml"}}
			case "duplicate-map":
				apTestPut(t, root, ".work/second/map.md", "---\ncheckpoint_ref: "+cp+"\n---\n# 重复登记\n")
			case "old-policy":
				apTestPut(t, root, guidanceContractRef("spec"), "schema_version: 1\n")
			case "scope-upper-bound":
				apTestPut(t, root, ".yss-execution-scope.yaml", "schema_version: 1\nscope_id: plan-to-backend\n")
				raw, _ := os.ReadFile(filepath.Join(root, guidanceContractRef("spec")))
				apTestPut(t, root, guidanceContractRef("spec"), string(raw)+"execution_scopes:\n  plan-to-backend:\n    terminal_work_unit: work-unit.backend-delivery\n    allowed_work_units: [work-unit.entry-triage]\n")
				input["target"] = "business-accepted"
			}
			apTestPut(t, root, "input.json", input)
			before := progressionInventory(t, root)
			if _, err := RunContext(context.Background(), "lifecycle", "target", root, map[string]string{"checkpoint": cp, "input": "input.json", "plan": "true", "out": filepath.Join(t.TempDir(), "plan.json")}); err == nil {
				t.Fatal("untrusted input accepted")
			}
			if !reflect.DeepEqual(before, progressionInventory(t, root)) {
				t.Fatal("rejection mutated project")
			}
		})
	}
}

func TestLifecycleTargetApplyRejectsTamperingAndInputDrift(t *testing.T) {
	for _, variant := range []string{"operation", "rehash-operation", "mode", "input-drift", "checkpoint-drift"} {
		t.Run(variant, func(t *testing.T) {
			root, cp := progressionFixture(t)
			file := progressionTestPlan(t, root, cp)
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var plan WritePlan
			if err = json.Unmarshal(raw, &plan); err != nil {
				t.Fatal(err)
			}
			switch variant {
			case "operation", "rehash-operation":
				plan.Operations[0].Path = cp
			case "mode":
				plan.Operations[0].Mode = 0777
			case "input-drift":
				input := progressionTestInput(cp, "spec-approved")
				input["intent_source"] = "后续意图改变"
				apTestPut(t, root, "target-input.json", input)
			case "checkpoint-drift":
				b, _ := os.ReadFile(filepath.Join(root, cp))
				apTestPut(t, root, cp, string(b)+"# 后续推进\n")
			}
			if variant == "rehash-operation" || variant == "mode" {
				plan.PlanDigest, err = nativePlanDigest(plan)
				if err != nil {
					t.Fatal(err)
				}
			}
			if variant == "operation" || variant == "rehash-operation" || variant == "mode" {
				b, _ := json.Marshal(plan)
				if err = os.WriteFile(file, b, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := progressionInventory(t, root)
			if _, err = RunContext(context.Background(), "lifecycle", "target", root, map[string]string{"apply": "true", "plan-file": file}); err == nil {
				t.Fatal("tampered or stale plan applied")
			}
			if !reflect.DeepEqual(before, progressionInventory(t, root)) {
				t.Fatal("rejected apply mutated project")
			}
		})
	}
}

func TestLifecycleTargetReadonlyAndOldStatusPreserveBytes(t *testing.T) {
	root, cp := progressionFixture(t)
	before := progressionInventory(t, root)
	if _, err := RunContext(context.Background(), "lifecycle", "target", root, map[string]string{"checkpoint": cp}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, progressionInventory(t, root)) {
		t.Fatal("readonly target wrote files")
	}
	apTestPut(t, root, guidanceContractRef("spec"), "schema_version: 1\n")
	before = progressionInventory(t, root)
	result, err := RunContext(context.Background(), "lifecycle", "status", root, map[string]string{"checkpoint": cp})
	if err != nil {
		t.Fatal(err)
	}
	m := semMap(result)
	if m["read_only"] != true || m["next_work_unit"] != "work-unit.plan-opportunity" || semMap(m["progression"])["enabled"] != false {
		t.Fatalf("old status changed: %#v", m)
	}
	if !reflect.DeepEqual(before, progressionInventory(t, root)) {
		t.Fatal("old status wrote migration/config")
	}
}

func TestLifecycleTargetIgnoresFrozenPayloadRegistrations(t *testing.T) {
	root, cp := progressionFixture(t)
	// Captured source maps belong to immutable delivery evidence, not the
	// active Tracker's parallel feature registrations.
	apTestPut(t, root, ".work/unrelated-directory/package/payload/files/map.md", "---\ncheckpoint_ref: absent-captured-checkpoint.yaml\n---\n")
	apTestPut(t, root, ".work/unrelated-directory/archive/map.md", "---\ncheckpoint_ref: "+cp+"\n---\n")
	if _, err := progressionRead(context.Background(), root, cp); err != nil {
		t.Fatalf("captured map changed active feature selection: %v", err)
	}
}

func TestLifecycleTargetPrecommitRejectsNewFeatureRegistration(t *testing.T) {
	root, cp := progressionFixture(t)
	apTestPut(t, root, "target-input.json", progressionTestInput(cp, "spec-approved"))
	plan, err := buildProgressionPlan(context.Background(), root, cp, "target-input.json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = transaction.ApplyContextWithValidation(context.Background(), root, progressionKind, plan.Operations, plan.Observed, nil, func() error {
		// This is the real precommit validation window, after plan compilation
		// and transaction preparation but before any intent file is installed.
		apTestPut(t, root, ".work/second/map.md", "---\ncheckpoint_ref: "+cp+"\n---\n")
		return validateProgressionInputs(context.Background(), root, plan)
	})
	if semanticCode(err) != "INPUT_DRIFT" {
		t.Fatalf("late duplicate registration was not refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, plan.Options["config_ref"])); !os.IsNotExist(err) {
		t.Fatal("drifting registration installed target intent")
	}
}

func TestLifecycleTargetWholeBusinessRequiresCurrentConsumers(t *testing.T) {
	consumer := []ProgressionConsumer{{Profile: "backend", Root: "/synthetic/backend", CheckpointRef: ".work/demo/checkpoint.yaml"}}
	for _, test := range []struct{ name, input, completion, root, want string }{
		{"current-delivery", "verified", "reached", "reached", "reached"},
		{"unverified-receipt", "pending", "reached", "reached", "pending"},
		{"delivery-not-finished", "verified", "pending", "reached", "pending"},
		{"backend-version-mismatch", "blocked", "reached", "reached", "blocked"},
		{"registered-approval-is-not-proof", "approved", "reached", "reached", "pending"},
		{"root-business-not-accepted", "verified", "reached", "pending", "pending"},
	} {
		t.Run(test.name, func(t *testing.T) {
			status, _ := progressionBusinessQualification(test.root, "root qualification", consumer, map[string]any{"backend": map[string]any{"input_status": test.input, "completion_status": test.completion, "reason": "synthetic current binding mismatch"}})
			if status != test.want {
				t.Fatalf("business completion disregards current consumer qualification: got %s want %s", status, test.want)
			}
		})
	}
}

func TestLifecycleTargetBlockersPreventWholeProfileCompletion(t *testing.T) {
	root, cpRef := progressionFixture(t)
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		for _, blockers := range []any{nil, []any{"unresolved current blocker"}} {
			s := newSemanticSession(context.Background(), root, nil)
			status, reason := progressionProfileTerminal(s, profile, cpRef, map[string]any{"blockers": blockers})
			if status != "blocked" || reason != "当前 Profile 缺少有效无阻塞事实" {
				t.Fatalf("%s completion bypassed blockers: %s %s", profile, status, reason)
			}
		}
	}
	apTestPut(t, root, ".work/unrelated-directory/progression-target.json", progressionTestInput(cpRef, "spec-approved"))
	cp := semMap(mustParseContract(mustReadSpecBaselineTestFile(t, filepath.Join(root, cpRef))))
	cp["blockers"] = []any{"unresolved current blocker"}
	apTestPut(t, root, cpRef, cp)
	result, err := progressionRead(context.Background(), root, cpRef)
	if err != nil {
		t.Fatal(err)
	}
	p := semMap(result["progression"])
	for _, layer := range []string{"profile", "business"} {
		if semMap(semMap(p["completion"])[layer])["status"] != "blocked" {
			t.Fatalf("short target reported %s completed despite blockers: %#v", layer, p)
		}
	}
}

func TestLifecycleTargetFutureConsumerCannotHideRootNext(t *testing.T) {
	for _, goal := range []string{"spec-approved", "business-accepted"} {
		t.Run(goal, func(t *testing.T) {
			root, cpRef := progressionFixture(t)
			input := progressionTestInput(cpRef, goal)
			input["consumers"] = []any{map[string]any{"profile": "backend", "root": t.TempDir(), "checkpoint_ref": ".work/demo/checkpoint.yaml"}}
			apTestPut(t, root, ".work/unrelated-directory/progression-target.json", input)
			result, err := progressionRead(context.Background(), root, cpRef)
			if err != nil {
				t.Fatal(err)
			}
			next := semMap(result["next_action"])
			if next["kind"] != "continue" || next["profile"] != "spec" || next["work_unit"] != "work-unit.plan-opportunity" || next["root"] != root {
				t.Fatalf("unready external consumer replaced source next action: %#v", result)
			}
		})
	}
}

func TestLifecycleTargetCannotBecomeApprovalOrHandoffEvidence(t *testing.T) {
	root, _ := progressionFixture(t)
	for _, ref := range []string{"progression-target.json", "nested/Progression-Target.JSON"} {
		t.Run(ref, func(t *testing.T) {
			apTestPut(t, root, ref, "intent")
			s := newSemanticSession(context.Background(), root, nil)
			if err := s.basis([]any{map[string]any{"ref": ref, "digest": "sha256:" + safefs.Digest([]byte("intent"))}}); semanticCode(err) != "PROGRESSION_EVIDENCE" {
				t.Fatalf("intent accepted as proof: %v", err)
			}
			if _, err := apBasis(s, []any{map[string]any{"ref": ref, "digest": "sha256:" + safefs.Digest([]byte("intent"))}}, "current approval", "APPROVAL_CURRENT_INVALID"); semanticCode(err) != "PROGRESSION_EVIDENCE" {
				t.Fatalf("intent accepted as approval proof: %v", err)
			}
			for _, state := range []map[string]any{{"subject_ref": ref, "approval_ref": "approval.json", "approval_scope": []any{"feature.demo"}}, {"subject_ref": "subject.json", "approval_ref": ref, "approval_scope": []any{"feature.demo"}}} {
				if _, err := approvalExpectationFromState(s, "check.design-reviewed", state); semanticCode(err) != "PROGRESSION_EVIDENCE" {
					t.Fatalf("intent accepted as current subject/approval reference: %v", err)
				}
			}
			_, err := contractHandoffClosure(s, ref, &nativeHandoff{Handoff: map[string]any{}, Config: map[string]any{}})
			if semanticCode(err) != "PROGRESSION_EVIDENCE" {
				t.Fatalf("intent accepted in package closure: %v", err)
			}
		})
	}
}

func TestLifecycleTargetTaskAdmissionUsesCurrentFeatureAndLegacyPolicy(t *testing.T) {
	for _, variant := range []string{"new-default-continue", "explicit-pending-continue", "asset-binding", "asset-nested-map-ignored", "asset-unregistered", "asset-duplicate-binding", "missing-feature", "wrong-feature", "blocked-current-input", "legacy", "reviewer", "resolved", "failed", "maintenance", "read-only"} {
		t.Run(variant, func(t *testing.T) {
			root, cp := progressionFixture(t)
			task := map[string]any{"execution_state": "Drafter", "workflow_status": "active", "work_unit_id": "work-unit.technical-analysis", "allowed_write_paths": []any{"design.md"}, "checkpoint_ref": cp, "contract": map[string]any{"kind": "lifecycle-work-unit"}}
			wantBlocked := false
			switch variant {
			case "explicit-pending-continue":
				apTestPut(t, root, ".work/unrelated-directory/progression-target.json", progressionTestInput(cp, "spec-approved"))
			case "asset-binding", "asset-nested-map-ignored", "asset-unregistered", "asset-duplicate-binding":
				delete(task, "checkpoint_ref")
				semMap(task["contract"])["lifecycle_ref"] = "design.md"
				if variant != "asset-unregistered" {
					raw := mustReadSpecBaselineTestFile(t, filepath.Join(root, cp))
					apTestPut(t, root, cp, string(raw)+"artifacts:\n  product-design:\n    ref: design.md\n")
				}
				if variant == "asset-nested-map-ignored" {
					apTestPut(t, root, ".work/unrelated-directory/delivery/payload/map.md", "---\ncheckpoint_ref: missing-checkpoint.yaml\n---\n# 历史冻结登记\n")
				}
				if variant == "asset-duplicate-binding" {
					apTestPut(t, root, ".work/second/map.md", "---\ncheckpoint_ref: "+cp+"\n---\n# 重复资产登记\n")
				}
				wantBlocked = variant == "asset-unregistered" || variant == "asset-duplicate-binding"
			case "missing-feature":
				delete(task, "checkpoint_ref")
				wantBlocked = true
			case "wrong-feature":
				task["checkpoint_ref"] = ".work/missing/checkpoint.yaml"
				wantBlocked = true
			case "blocked-current-input":
				raw := mustReadSpecBaselineTestFile(t, filepath.Join(root, cp))
				apTestPut(t, root, cp, string(raw)+"profile_id: harness.frontend-delivery\n")
				wantBlocked = true
			case "legacy":
				apTestPut(t, root, guidanceContractRef("spec"), "schema_version: 1\n")
				delete(task, "checkpoint_ref")
			case "reviewer":
				task["execution_state"] = "Reviewer"
				delete(task, "checkpoint_ref")
			case "resolved", "failed":
				task["workflow_status"] = variant
				delete(task, "checkpoint_ref")
			case "maintenance":
				semMap(task["contract"])["kind"] = "template-maintenance"
				delete(task, "checkpoint_ref")
			case "read-only":
				task["allowed_write_paths"] = []any{}
				delete(task, "checkpoint_ref")
			}
			before := progressionInventory(t, root)
			s := newSemanticSession(context.Background(), root, nil)
			err := progressionTaskEntry(s, task)
			if (err != nil) != wantBlocked {
				t.Fatalf("task admission %s: %v", variant, err)
			}
			if err == nil && s.finish() != nil {
				t.Fatal("admission input snapshot no longer current")
			}
			if !reflect.DeepEqual(before, progressionInventory(t, root)) {
				t.Fatal("task admission mutated project")
			}
		})
	}
}

func TestLifecycleTargetRecoveryAndRollbackOnlyIntent(t *testing.T) {
	root, cp := progressionFixture(t)
	file := progressionTestPlan(t, root, cp)
	cpBytes, _ := os.ReadFile(filepath.Join(root, cp))
	marker := filepath.Join(t.TempDir(), "pending")
	child := exec.Command(os.Args[0], "-test.run=^TestLifecycleTargetCrashHelper$")
	child.Env = append(os.Environ(), "YSS_TARGET_CRASH_ROOT="+root, "YSS_TARGET_CRASH_PLAN="+file, "YSS_TARGET_CRASH_MARKER="+marker)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill() })
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("target transaction failed to reach precommit interruption")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = child.Process.Kill()
	_ = child.Wait()
	validate := func(summary transaction.Summary, refs []string) error {
		return ValidateProgressionTransaction(root, "spec", refs)
	}
	if result, err := transaction.RecoverKindContextWithValidator(context.Background(), root, progressionKind, validate); err != nil || result.Status != "recovered" {
		t.Fatalf("recover: %#v %v", result, err)
	}
	config := filepath.Join(root, ".work/unrelated-directory/progression-target.json")
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatalf("recover retained config: %v", err)
	}
	if _, err := RunContext(context.Background(), "lifecycle", "target", root, map[string]string{"apply": "true", "plan-file": file}); err != nil {
		t.Fatal(err)
	}
	if result, err := transaction.RollbackKindContextWithValidator(context.Background(), root, progressionKind, validate); err != nil || result.Status != "rolled-back" {
		t.Fatalf("rollback: %#v %v", result, err)
	}
	if _, err := os.Stat(config); !os.IsNotExist(err) {
		t.Fatalf("rollback retained config: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, cp))
	if string(after) != string(cpBytes) {
		t.Fatal("recovery/rollback changed checkpoint")
	}
	if err := ValidateProgressionTransaction(root, "spec", []string{cp}); err == nil {
		t.Fatal("recovery guard accepted checkpoint write")
	}
}

func TestLifecycleTargetNoUIDeclarationAndForeignConsumerDoNotComplete(t *testing.T) {
	root, cp := progressionFixture(t)
	policyRaw, _ := os.ReadFile(filepath.Join(root, guidanceContractRef("spec")))
	apTestPut(t, root, guidanceContractRef("spec"), string(policyRaw)+"    product-design-completed:\n      required_gates: [gate.spec-baseline-approved, gate.product-design-approved]\n      required_checks: [business-tickets-formal]\n      conditional_gates: {gate.product-design-approved: product-design-impact}\n")
	input := progressionTestInput(cp, "product-design-completed")
	apTestPut(t, root, ".work/unrelated-directory/progression-target.json", input)
	apTestPut(t, root, "assessment.json", map[string]any{"impact_assessment": map[string]any{"product_design": false}})
	assessment, _ := os.ReadFile(filepath.Join(root, "assessment.json"))
	apTestPut(t, root, cp, map[string]any{"schema_version": 1, "repository_mode": "project-instance", "feature_id": "feature.demo", "stage": "stage.product-design", "next_work_unit": "work-unit.technical-analysis", "blockers": []any{}, "stage_decision_package_ref": "assessment.json", "gates": map[string]any{"gate.spec-baseline-approved": map[string]any{"status": "approved"}, "gate.product-design-approved": map[string]any{"status": "not-applicable", "applicable": false, "reason": "无UI", "basis": []any{map[string]any{"ref": "assessment.json", "digest": "sha256:" + safefs.Digest(assessment)}}}}})
	result, err := progressionRead(context.Background(), root, cp)
	if err != nil {
		t.Fatal(err)
	}
	if semMap(result["progression"])["reached"] == true {
		t.Fatal("self-declared noUI promoted target without current original approvals")
	}
	foreign, foreignCP := progressionFixture(t)
	bytes, _ := os.ReadFile(filepath.Join(foreign, foreignCP))
	apTestPut(t, foreign, foreignCP, string(bytes)+"profile_id: harness.backend-delivery\n")
	input = progressionTestInput(cp, "business-accepted")
	input["consumers"] = []any{map[string]any{"profile": "backend", "root": foreign, "checkpoint_ref": foreignCP}}
	apTestPut(t, root, ".work/unrelated-directory/progression-target.json", input)
	result, err = progressionRead(context.Background(), root, cp)
	if err != nil {
		t.Fatal(err)
	}
	if semMap(semMap(result["coordination"])["backend"])["input_status"] != "blocked" || semMap(result["progression"])["reached"] == true {
		t.Fatalf("wrong consumer qualified: %#v", result)
	}
}

func TestLifecycleTargetCrashHelper(t *testing.T) {
	root := os.Getenv("YSS_TARGET_CRASH_ROOT")
	if root == "" {
		t.Skip("subprocess crash helper")
	}
	raw, err := os.ReadFile(os.Getenv("YSS_TARGET_CRASH_PLAN"))
	if err != nil {
		t.Fatal(err)
	}
	var p WritePlan
	if err = json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	_, err = transaction.ApplyContextWithValidation(context.Background(), root, progressionKind, p.Operations, p.Observed, nil, func() error {
		if e := os.WriteFile(os.Getenv("YSS_TARGET_CRASH_MARKER"), []byte("pending"), 0600); e != nil {
			return e
		}
		select {}
	})
	t.Fatal("parent should interrupt pending target transaction", err)
}

func TestLifecycleTargetPlansAndAppliesOnlyFeatureIntent(t *testing.T) {
	root, cp := progressionFixture(t)
	before, err := os.ReadFile(filepath.Join(root, cp))
	if err != nil {
		t.Fatal(err)
	}
	input := map[string]any{"schema_version": 1, "kind": "lifecycle-progression-target", "feature_id": "feature.demo", "checkpoint_ref": cp, "target": "spec-approved", "intent_source": "已确认用户请求", "consumers": []any{}}
	apTestPut(t, root, "target-input.json", input)
	planFile := filepath.Join(t.TempDir(), "plan.json")
	if _, err = RunContext(context.Background(), "lifecycle", "target", root, map[string]string{"checkpoint": cp, "input": "target-input.json", "plan": "true", "out": planFile}); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(root, ".work/unrelated-directory/progression-target.json")
	if _, err = os.Stat(config); !os.IsNotExist(err) {
		t.Fatal("plan wrote target intent")
	}
	if _, err = RunContext(context.Background(), "lifecycle", "target", root, map[string]string{"apply": "true", "plan-file": planFile}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil || result["target"] != "spec-approved" {
		t.Fatalf("wrong intent: %s %v", raw, err)
	}
	after, _ := os.ReadFile(filepath.Join(root, cp))
	if string(before) != string(after) {
		t.Fatal("target update changed checkpoint bytes")
	}
	if _, err = RunContext(context.Background(), "lifecycle", "target", root, map[string]string{"apply": "true", "plan-file": planFile}); err != nil {
		t.Fatalf("retry not idempotent: %v", err)
	}
}

func TestLifecycleTargetReadonlyDefaultsAndDoesNotTrustApproved(t *testing.T) {
	root, cp := progressionFixture(t)
	result, err := RunContext(context.Background(), "lifecycle", "target", root, map[string]string{"checkpoint": cp})
	if err != nil {
		t.Fatal(err)
	}
	p := semMap(semMap(result)["progression"])
	if p["enabled"] != true || p["target"] != "business-accepted" || p["status"] != "pending" || p["checkpoint_digest"] == "" || p["config_digest"] != nil {
		t.Fatalf("default projection: %#v", p)
	}
	apTestPut(t, root, cp, "schema_version: 1\nrepository_mode: project-instance\nfeature_id: feature.demo\nstage: stage.entry-triage\nnext_work_unit: work-unit.plan-opportunity\nblockers: []\ngates:\n  gate.delivery-accepted:\n    status: approved\n")
	result, err = RunContext(context.Background(), "lifecycle", "target", root, map[string]string{"checkpoint": cp})
	if err != nil {
		t.Fatal(err)
	}
	p = semMap(semMap(result)["progression"])
	if p["status"] == "reached" || semMap(semMap(result)["next_action"])["work_unit"] != "work-unit.plan-opportunity" {
		t.Fatalf("registered approval granted completion: %#v", result)
	}
}
