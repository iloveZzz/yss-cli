package approval

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlan3AggregateReusesActualTechnicalContextWithoutRoleIdentity(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	s.GovernanceVersion = 2
	s.Report.Profile = "spec"
	s.GovernanceDigest = strings.Repeat("a", 64)
	s.GovernancePolicy = map[string]any{"gate_policy": map[string]any{"profiles": map[string]any{"spec": map[string]any{"technical_reviews": []any{map[string]any{"gate": "gate.plan-approved"}}}}}}
	decisionFile(t, root, ".template-spec/process/schemas/approval-record.schema.json", map[string]any{"type": "object"})
	decisionFile(t, root, "review.json", map[string]any{"feature_id": "feature.orders"})
	raw, _ := os.ReadFile(filepath.Join(root, "review.json"))
	basis := []any{map[string]any{"ref": "review.json", "digest": safefs.Digest(raw)}}
	last := map[string]any{"attempt_id": "cycle.attempt-1", "phase": "initial", "checkpoint_ref": "cp.json", "request_key": strings.Repeat("b", 64), "check_ids": []string{"check.domain-strategy-approved"}, "target_finding_ids": []any{}, "status": "completed", "outcome": "passed", "task_ref": "task.json", "task_digest": strings.Repeat("c", 64)}
	control := map[string]any{"protocol_version": NativePlanProtocol, "cycle_id": "cycle", "scope_digest": strings.Repeat("d", 64), "attempts": []any{last}}
	record := map[string]any{"schema_version": 3, "gate_id": "gate.plan-approved", "decision": "approved", "actor_kind": "agent", "subject_ref": "review.json", "subject_digest": safefs.Digest(raw), "approval_scope": []string{"feature.orders"}, "basis": basis, "policy": map[string]any{"ref": ".template-spec/process/orchestration-contract.yaml", "version": 2, "digest": s.GovernanceDigest}, "review_task_ref": last["task_ref"], "review_task_digest": last["task_digest"], "review": map[string]any{"mode": "self-check"}, "check_ids": last["check_ids"], "review_bundle_ref": "bundle.json", "plan_review_binding": PlanBinding(control, last)}
	bundle := kernel.ApCopy(record)
	bundle["kind"] = "review-bundle"
	delete(bundle, "actor_kind")
	// The caller validates the bundle's actual task/results; this seam enforces reuse.
	decisionFile(t, root, "approval.json", record)
	cp := map[string]any{"feature_id": "feature.orders", "plan_review_ref": "review.json", "plan_approval_ref": "approval.json", "plan_review_control": control}
	if err = PlanAggregateRecord(s, cp, map[string]any{}, basis, map[string]map[string]any{"bundle.json": bundle}); err != nil {
		t.Fatal(err)
	}
	record["review_task_digest"] = strings.Repeat("e", 64)
	decisionFile(t, root, "approval.json", record)
	s.V = kernel.NewView(root)
	if err = PlanAggregateRecord(s, cp, map[string]any{}, basis, map[string]map[string]any{"bundle.json": bundle}); kernel.SemanticCode(err) != "PLAN_APPROVAL_REUSE" {
		t.Fatalf("accepted changed completed task: %v", err)
	}
}
