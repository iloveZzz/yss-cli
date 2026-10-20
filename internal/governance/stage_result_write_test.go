package governance

import (
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stageWriteFile(t *testing.T, root, ref string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, ref)
	if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, raw, 0644); err != nil {
		t.Fatal(err)
	}
}
func stageWriteFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	cpRef := ".work/demo/checkpoint.json"
	write := func(ref string, v any) { stageWriteFile(t, root, ref, v) }
	write("yss-project.yaml", map[string]any{"schema_version": 2, "repository_mode": "project-instance"})
	write(".yss.json", map[string]any{"schemaVersion": 4, "bundleSchemaVersion": 4, "protocolVersion": 1, "profile": "spec", "profileId": "harness.spec-template", "templateCommit": strings.Repeat("a", 40), "snapshotHash": strings.Repeat("b", 64), "manifestHash": strings.Repeat("c", 64), "templateSourceState": "committed", "cliSourceState": "unknown", "bundleHash": strings.Repeat("d", 64), "cliVersion": "1.0.0", "templateVersion": "1.0.0", "legacyCliVersion": "1.0.0", "variables": map[string]any{}, "distribution": map[string]any{}, "managedFiles": map[string]any{}, "baselineDigest": safefs.Digest([]byte("{}"))})
	write(".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 3, "profile_id": "harness.spec-template", "status": "active", "required_capabilities": []string{kernel.RoleIndependentCapability, kernel.StageGatesCapability}, "lifecycle": map[string]any{}, "execution": map[string]any{}})
	gates := []any{}
	for _, id := range kernel.StageGateIDs {
		gates = append(gates, map[string]any{"id": id, "work_stages": []string{"stage.plan"}, "checks": []any{}})
	}
	write(".template-spec/process/lifecycle-registry.yaml", map[string]any{"schema_version": 1, "status": "active", "gates": gates})
	for _, name := range []string{"lifecycle-registry", "stage-result", "lifecycle-checkpoint-v2"} {
		write(".template-spec/process/schemas/"+name+".schema.json", map[string]any{"type": "object"})
	}
	versions := map[string]any{"required_capability": kernel.RoleIndependentCapability, "activation": "native-consumers-and-bundle-closure-required-before-instance-writes", "legacy_policy": "read-verify-explicit-migrate-only-no-new-task-or-stage-write"}
	for k, v := range map[string]int{"project_identity": 2, "harness_profile": 3, "task": 3, "approval_record": 3, "review_bundle": 3, "continuation": 2, "delivery_authorization": 2, "daily": 2, "standard": 2, "user_decision": 1, "lifecycle_registry": 1, "native_bundle": 4, "native_metadata": 4, "source_lock": 5, "slice_implementation_contract": 4, "workflow_execution_result": 1} {
		versions[k] = map[string]any{"current": v}
	}
	write(".template-spec/process/orchestration-contract.yaml", map[string]any{"schema_version": 2, "governance_versions": versions, "stage_gates": map[string]any{"schema_version": 1, "capability": kernel.StageGatesCapability, "gates": gates}})
	tracker := "---\ntracker:\n  root: .work\n  platform: local-markdown\n---\n"
	file := filepath.Join(root, kernel.TrackerRef)
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(tracker), 0644); err != nil {
		t.Fatal(err)
	}
	write(cpRef, map[string]any{"schema_version": 2, "repository_mode": "project-instance", "feature_id": "feature.demo", "gates": map[string]any{}})
	write("asset.json", map[string]any{"content": "formed"})
	raw, _ := os.ReadFile(filepath.Join(root, "asset.json"))
	write("execution.json", map[string]any{"phase": "execution", "argv": []string{"node", "check-asset"}, "exit_code": 0, "executed_at": "2026-10-20T00:00:00Z", "result": "passed", "blocking_findings": []any{}})
	write("input.json", map[string]any{"schema_version": 1, "kind": "stage-result", "gate_id": "gate.plan-approved", "feature_id": "feature.demo", "scope": []string{"feature.demo"}, "assets": []any{map[string]any{"ref": "asset.json", "digest": safefs.Digest(raw)}}, "checks": []any{}, "outcome": "ready-for-human", "blocking_findings": []any{}, "provenance": map[string]any{"tool": "node", "execution_ref": "execution.json"}})
	return root, cpRef
}
func TestStageResultPlanIsReadOnly(t *testing.T) {
	root, cp := stageWriteFixture(t)
	before, _ := os.ReadFile(filepath.Join(root, cp))
	plan, err := PlanStageResult(context.Background(), root, cp, "input.json")
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(root, cp))
	if string(before) != string(after) || len(plan.Operations) != 2 {
		t.Fatalf("plan mutated or missing operations: %d", len(plan.Operations))
	}
	for _, op := range plan.Operations {
		if op.Path != cp {
			if _, err = os.Stat(filepath.Join(root, op.Path)); !os.IsNotExist(err) {
				t.Fatal("record written during plan")
			}
		}
	}
}
func TestStageResultApplyRejectsInputDrift(t *testing.T) {
	root, cp := stageWriteFixture(t)
	plan, err := PlanStageResult(context.Background(), root, cp, "input.json")
	if err != nil {
		t.Fatal(err)
	}
	planRef := filepath.Join(t.TempDir(), "plan.json")
	raw, _ := json.Marshal(plan)
	if err = os.WriteFile(planRef, raw, 0644); err != nil {
		t.Fatal(err)
	}
	stageWriteFile(t, root, "asset.json", map[string]any{"changed": true})
	before, _ := os.ReadFile(filepath.Join(root, cp))
	if err = ApplyStageResult(context.Background(), root, planRef); err == nil {
		t.Fatal("stale plan applied")
	}
	after, _ := os.ReadFile(filepath.Join(root, cp))
	if string(before) != string(after) {
		t.Fatal("rejected apply mutated checkpoint")
	}
}
func TestStageResultApplyIsIdempotent(t *testing.T) {
	root, cp := stageWriteFixture(t)
	plan, err := PlanStageResult(context.Background(), root, cp, "input.json")
	if err != nil {
		t.Fatal(err)
	}
	planRef := filepath.Join(t.TempDir(), "plan.json")
	raw, _ := json.Marshal(plan)
	if err = os.WriteFile(planRef, raw, 0644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = ApplyStageResult(context.Background(), root, planRef); err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
}
func TestStageGateMigrationDoesNotInventApproval(t *testing.T) {
	root, cp := stageWriteFixture(t)
	stageWriteFile(t, root, cp, map[string]any{"schema_version": 1, "repository_mode": "project-instance", "feature_id": "feature.demo", "gates": map[string]any{}})
	stageWriteFile(t, root, "mapping.json", map[string]any{"schema_version": 1, "target_checkpoint_ref": ".work/demo/checkpoint.yaml", "source_policy_ref": "missing.json", "source_policy_digest": "bad", "stage_sources": []any{}})
	before, _ := os.ReadFile(filepath.Join(root, cp))
	_, err := PlanStageGateMigration(context.Background(), root, cp, "mapping.json")
	if err == nil {
		t.Fatal("missing original policy accepted")
	}
	after, _ := os.ReadFile(filepath.Join(root, cp))
	if string(before) != string(after) {
		t.Fatal("original checkpoint changed")
	}
}
func TestStageResultRejectsForgedPlan(t *testing.T) {
	root := t.TempDir()
	p := WritePlan{Kind: stageDecisionPlanKind, Root: root, Observed: map[string]domain.Descriptor{}}
	raw, _ := json.Marshal(p)
	ref := filepath.Join(root, "plan.json")
	os.WriteFile(ref, raw, 0644)
	if err := ApplyStageResult(context.Background(), root, ref); err == nil {
		t.Fatal("forged plan accepted")
	}
}

func TestStageResultRecoveryIsAtomic(t *testing.T) {
	root, cp := stageWriteFixture(t)
	before, _ := os.ReadFile(filepath.Join(root, cp))
	plan, err := PlanStageResult(context.Background(), root, cp, "input.json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = transaction.ApplyContextWithValidation(context.Background(), root, "stage-decision", plan.Operations, plan.Observed, nil, func() error { return domain.Fail("TEST_INTERRUPTED", "synthetic post-write failure") })
	if err == nil {
		t.Fatal("injected interruption was ignored")
	}
	after, _ := os.ReadFile(filepath.Join(root, cp))
	if string(before) != string(after) {
		t.Fatal("checkpoint was only partly restored")
	}
	for _, op := range plan.Operations {
		if op.Path != cp {
			if _, e := os.Stat(filepath.Join(root, op.Path)); !os.IsNotExist(e) {
				t.Fatal("partial stage record remained")
			}
		}
	}
}
