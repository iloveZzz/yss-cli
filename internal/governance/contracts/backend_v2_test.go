package contracts

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"os"
	"path/filepath"
	"testing"
)

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
