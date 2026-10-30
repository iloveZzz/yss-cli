package contracts

import (
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestBackendDeliveryDraftSliceNeedsCurrentStageAdmission(t *testing.T) {
	for _, tc := range []struct {
		name    string
		stage   bool
		version int
		id      string
		want    string
	}{
		{"stage-draft-consumes-admission", true, 4, "contract.current", "STAGE_PENDING"},
		{"legacy-draft-still-rejected", false, 4, "contract.current", "BACKEND_DELIVERY"},
		{"old-slice-still-rejected", true, 3, "contract.current", "BACKEND_DELIVERY"},
		{"wrong-identity-still-rejected", true, 4, "contract.other", "BACKEND_DELIVERY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
			s.GovernanceVersion = 2
			s.Report.Profile = "spec"
			if tc.stage {
				s.GovernancePolicy = map[string]any{"stage_gates": map[string]any{"capability": kernel.StageGatesCapability}}
			}
			taskFile(t, s.Root, "checkpoint.json", map[string]any{"gates": map[string]any{}})
			c := &NativeSlice{Ref: "slice.json", Raw: map[string]any{"schema_version": json.Number(strconv.Itoa(tc.version))}, Normalized: map[string]any{"status": "draft", "contract_id": "contract.current", "contract_version": "v1"}}
			err := backendSliceApprovalBinding(s, c, map[string]any{"id": tc.id, "version": "v1", "approval_ref": "checkpoint.json"}, nil)
			if kernel.SemanticCode(err) != tc.want {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestBackendProjectAcceptsVerifiedNewIdentityAndRejectsMixedGeneration(t *testing.T) {
	s, _ := taskFixture(t)
	raw := []byte("---\ncontext_schema_version: 1\n---\n## 流程术语\n|术语|含义|英文标识|避免 / 备注|\n|---|---|---|---|\n## 业务术语\n|术语|含义|英文标识|适用业务责任区|避免 / 备注|\n|---|---|---|---|---|\n")
	if err := os.WriteFile(filepath.Join(s.Root, "CONTEXT.md"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := backendProject(s); err != nil {
		t.Fatal(err)
	}
	s.GovernanceVersion = 1
	if err := backendProject(s); err == nil {
		t.Fatal("accepted identity2 through legacy qualification")
	}
	s.GovernanceVersion = 2
	taskFile(t, s.Root, "yss-project.yaml", map[string]any{"schema_version": 1, "repository_mode": "project-instance"})
	s.V = kernel.NewView(s.Root)
	if err := backendProject(s); err == nil {
		t.Fatal("accepted identity1 as new governance")
	}
}

func TestBackendReview2UsesActualTaskSelfCheckProvenance(t *testing.T) {
	s, task := completedReviewer(t, false)
	review := kernel.ApMap(task["review_context"])
	input := map[string]any{"scope_kind": "baseline", "basis": review["basis"], "implementation_candidate": review["candidate"]}
	result := map[string]any{"assessment": review["review"]}
	if err := backendReviewProvenance(s, input, result, s.Root); err != nil {
		t.Fatal(err)
	}
	input["basis"] = []any{}
	if err := backendReviewProvenance(s, input, result, s.Root); err == nil {
		t.Fatal("accepted no actual implementation task")
	}
}

func TestBackendFreshTaskCannotUseCompletedDraftAsImplementation(t *testing.T) {
	s, record, _ := approval3Fixture(t)
	record["gate_id"] = "gate.fresh-verification-passed"
	taskFile(t, s.Root, "approval.json", record)
	s.V = kernel.NewView(s.Root)
	cp := map[string]any{"gates": map[string]any{"gate.fresh-verification-passed": map[string]any{"approval_ref": "approval.json"}}}
	if _, err := backendFreshTask(s, cp, "slice.json"); kernel.SemanticCode(err) != "BACKEND_DELIVERY" {
		t.Fatalf("draft must not qualify as completed Slice implementation: %v", err)
	}
}

func TestExternalStrategicBackendDeliveryIsUnsupported(t *testing.T) {
	s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
	_, err := BackendInspectDelivery(s, "legacy-delivery.json", nil)
	if kernel.SemanticCode(err) != "UNPORTED" {
		t.Fatalf("legacy external delivery must be explicitly unsupported, got %v", err)
	}
	if err := verifyBackendDeliveryPackagePublic(s, "legacy-package", nil); kernel.SemanticCode(err) != "UNPORTED" {
		t.Fatalf("legacy external package must be explicitly unsupported, got %v", err)
	}
	_, localErr := backendInspectDeliveryMode(s, "local-delivery.json", nil, true, "")
	if localErr == nil || kernel.SemanticCode(localErr) == "UNPORTED" {
		t.Fatalf("local evidence must retain its actual validation path, got %v", localErr)
	}
}
