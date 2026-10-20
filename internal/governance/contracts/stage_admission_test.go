package contracts

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"testing"
)

func TestStageAdmissionRequiresExplicitSpecPolicy(t *testing.T) {
	s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
	if err := VerifyStageAdmission(s, "checkpoint.json", "slice.json"); err == nil {
		t.Fatal("legacy policy silently admitted")
	}
}
func TestStageAdmissionRejectsMissingOrNonSliceInputs(t *testing.T) {
	s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
	s.GovernanceVersion = 2
	s.Report.Profile = "spec"
	s.GovernancePolicy = map[string]any{"stage_gates": map[string]any{"capability": kernel.StageGatesCapability}}
	taskFile(t, s.Root, "subject.json", map[string]any{"kind": "business-ticket"})
	taskFile(t, s.Root, "record.json", map[string]any{"gate_id": "gate.implementation-admitted", "assets": []any{map[string]any{"ref": "subject.json"}}})
	s.V = kernel.NewView(s.Root)
	if err := VerifyStageAdmissionInputs(s, "record.json", nil); err == nil {
		t.Fatal("non-Slice admitted")
	}
}

// Stub only the two delegated validators; installed Worker scenarios exercise
// their real implementations. This test isolates coverage of every Slice.
func TestStageDeliveryRequiresEveryAdmittedSlice(t *testing.T) {
	s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
	s.GovernanceVersion = 2
	s.Report.Profile = "spec"
	s.GovernancePolicy = map[string]any{"stage_gates": map[string]any{"capability": kernel.StageGatesCapability}}
	priorStage, priorVerification := kernel.RoleIndependentValidators["stage-result"], kernel.RoleIndependentValidators["verification"]
	defer func() {
		kernel.RoleIndependentValidators["stage-result"] = priorStage
		kernel.RoleIndependentValidators["verification"] = priorVerification
	}()
	kernel.RoleIndependentValidators["stage-result"] = func(*kernel.SemanticSession, string, map[string]string) error { return nil }
	kernel.RoleIndependentValidators["verification"] = func(*kernel.SemanticSession, string, map[string]string) error { return nil }
	taskFile(t, s.Root, "slice-a.json", map[string]any{"schema_version": 4, "slice_id": "slice.a"})
	taskFile(t, s.Root, "slice-b.json", map[string]any{"schema_version": 4, "slice_id": "slice.b"})
	taskFile(t, s.Root, "task.json", map[string]any{"contract": map[string]any{"slice_contract_ref": "slice-a.json"}})
	taskFile(t, s.Root, "report.json", map[string]any{"consumed_contract": map[string]any{}})
	admitted := map[string]any{"kind": "stage-result", "gate_id": "gate.implementation-admitted", "feature_id": "feature.demo", "scope": []string{"feature.demo"}, "assets": []any{map[string]any{"ref": "slice-a.json"}}}
	taskFile(t, s.Root, "admission.json", admitted)
	taskFile(t, s.Root, "delivery.json", map[string]any{"feature_id": "feature.demo", "scope": []string{"feature.demo"}, "provenance": map[string]any{"task_ref": "task.json"}, "assets": []any{map[string]any{"ref": "admission.json"}, map[string]any{"ref": "report.json"}}})
	if err := verifyStageDeliveryInputs(s, "delivery.json", nil); err != nil {
		t.Fatal(err)
	}
	admitted["assets"] = append(kernel.ApArray(admitted["assets"]), map[string]any{"ref": "slice-b.json"})
	taskFile(t, s.Root, "admission.json", admitted)
	s.V = kernel.NewView(s.Root)
	if err := verifyStageDeliveryInputs(s, "delivery.json", nil); err == nil {
		t.Fatal("partial delivery accepted")
	}
}

func TestStagePreparationCannotAuthorizeItselfOrUseFutureAdmission(t *testing.T) {
	s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
	s.GovernanceVersion = 2
	s.Report.Profile = "spec"
	s.GovernancePolicy = map[string]any{"stage_gates": map[string]any{"capability": kernel.StageGatesCapability}}
	if e := verifyStagePreparationAuthorization(s, "", "subject.json", ""); e == nil {
		t.Fatal("preparation lacked authority")
	}
	taskFile(t, s.Root, "future.json", map[string]any{"kind": "stage-result", "gate_id": "gate.implementation-admitted"})
	if e := verifyStagePreparationAuthorization(s, "future.json", "subject.json", "checkpoint.json"); e == nil {
		t.Fatal("preparation consumed future admission")
	}
}

func TestStagePreparationSnapshotDoesNotRewriteSource(t *testing.T) {
	original := map[string]any{"decisions": []any{map[string]any{"user_confirmation": map[string]any{"stage_record_ref": "immutable-stage.json"}}}}
	copy, e := stageSnapshotCopy(original)
	if e != nil {
		t.Fatal(e)
	}
	delete(kernel.ApRows(copy["decisions"])[0], "user_confirmation")
	if kernel.ApRows(original["decisions"])[0]["user_confirmation"] == nil {
		t.Fatal("normalization mutated current source")
	}
}
