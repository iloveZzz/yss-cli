package governance

import (
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/governance/approval"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func formalFile(t *testing.T, root, ref string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, ref)
	if err = os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, raw, 0644); err != nil {
		t.Fatal(err)
	}
}
func TestPolicy2FormalBoundaryUsesCurrentPolicyWithoutRoles(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	formalFile(t, root, ".template-spec/process/harness-profile.yaml", map[string]any{"schema_version": 3, "profile_id": "harness.spec-template"})
	policy := map[string]any{"schema_version": 2, "phase_boundary": map[string]any{"choices": []string{"continue"}, "evidence": map[string]any{"required": []string{"reason"}}}}
	formalFile(t, root, ".template-spec/process/orchestration-contract.yaml", policy)
	formalFile(t, root, "checkpoint.json", map[string]any{"stage": "stage.entry-triage", "status": "routing", "phase_boundary": map[string]any{"decision": "continue", "reason": "continue authorized preparation"}})
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	s.GovernanceVersion = 2
	s.GovernancePolicy = policy
	s.Report.Profile = "spec"
	s.Registry = map[string]any{"stages": []any{map[string]any{"id": "stage.entry-triage"}}}
	if err = s.Verify("checkpoint-boundary", "checkpoint.json", nil); err != nil {
		t.Fatal(err)
	}
	formalFile(t, root, "checkpoint.json", map[string]any{"stage": "stage.entry-triage", "phase_boundary": map[string]any{"decision": "invented", "reason": "invalid"}})
	s.V = kernel.NewView(root)
	if err = s.Verify("checkpoint-boundary", "checkpoint.json", nil); kernel.SemanticCode(err) != "CHECKPOINT_BOUNDARY" {
		t.Fatalf("unregistered boundary accepted: %v", err)
	}
	if len(s.Roles) != 0 {
		t.Fatal("new boundary loaded role qualifications")
	}
}

func TestPolicy2DraftPreparationDoesNotRequireFuturePlanApproval(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	formalFile(t, root, "checkpoint.json", map[string]any{"repository_mode": "project-instance", "stage": "stage.spec-architecture", "next_work_unit": "work-unit.spec-synthesis", "status": "routing", "mode": "orchestrate"})
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	s.GovernanceVersion = 2
	s.GovernancePolicy = map[string]any{"preparation_policy": map[string]any{"work_units": []string{"work-unit.spec-synthesis"}}}
	if err = s.Verify("plan-checkpoint", "checkpoint.json", nil); err != nil {
		t.Fatal(err)
	}
	// A draft exception cannot validate an actual approved aggregate.
	if err = s.Verify("plan-aggregate", "checkpoint.json", nil); kernel.SemanticCode(err) != "PLAN_GATE_BLOCKED" {
		t.Fatalf("draft became approved: %v", err)
	}
}

func TestPlanTask3BindsCurrentCycleAndRawSubject(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	s.GovernanceVersion = 2
	s.Report.Profile = "spec"
	p := map[string]any{"enabled": true, "protocol": approval.NativePlanProtocol, "professional_check_ids": []string{"check.domain-strategy-approved", "check.stage-decision-package-approved"}, "aggregate_gate": "gate.plan-approved", "phase_order": []string{"initial", "rereview", "exception"}, "limits": map[string]any{"regular": 2, "exception": 1, "total": 3}, "finding_kinds": []string{"violation", "missing-evidence", "suggestion"}, "later_finding_origins": []any{}}
	s.GovernancePolicy = map[string]any{"planning": map[string]any{"review_control": p}}
	formalFile(t, root, ".template-spec/process/orchestration-contract.yaml", s.GovernancePolicy)
	s.GovernancePolicy, err = s.Doc(".template-spec/process/orchestration-contract.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p = kernel.SemMap(kernel.SemMap(s.GovernancePolicy["planning"])["review_control"])
	pd, _ := kernel.SemanticDocumentDigest(p)
	formalFile(t, root, ".template-spec/process/schemas/plan-review-control.schema.json", map[string]any{"type": "object"})
	formalFile(t, root, "draft.json", map[string]any{"feature_id": "feature.orders"})
	raw, _ := os.ReadFile(filepath.Join(root, "draft.json"))
	formalFile(t, root, "base.json", map[string]any{"feature_id": "feature.orders"})
	sourceRaw, _ := os.ReadFile(filepath.Join(root, "base.json"))
	basis := []any{map[string]any{"ref": "base.json", "digest": safefs.Digest(sourceRaw)}}
	rows := []any{map[string]any{"boundary": "check.domain-strategy-approved", "subject_ref": "draft.json", "subject_digest": safefs.Digest(raw), "approval_scope": []string{"feature.orders"}, "basis": basis}}
	candidateDigest, _ := kernel.SemanticDocumentDigest(rows)
	a := map[string]any{"attempt_id": "cycle.attempt-1", "request_key": strings.Repeat("b", 64), "phase": "initial", "status": "reserved", "outcome": "passed", "check_ids": []string{"check.domain-strategy-approved"}, "target_finding_ids": []any{}, "checkpoint_ref": "cp.json", "task_ref": "task.json", "candidate_ref": "subject.json", "candidate_digest": candidateDigest, "basis": basis, "check_results": []any{map[string]any{"check_id": "check.domain-strategy-approved", "status": "passed"}}}
	c := map[string]any{"protocol_version": approval.NativePlanProtocol, "feature_id": "feature.orders", "cycle_id": "cycle", "scope_digest": strings.Repeat("c", 64), "policy_ref": ".template-spec/process/orchestration-contract.yaml", "policy_digest": pd, "history_count": 0, "attempts": []any{a}, "findings": []any{}}
	binding := approval.PlanBinding(c, a)
	formalFile(t, root, "subject.json", map[string]any{"source_checkpoint": map[string]any{"ref": "cp.json", "digest": candidateDigest}, "current_approvals": rows, "plan_review_binding": binding})
	raw, _ = os.ReadFile(filepath.Join(root, "subject.json"))
	task := map[string]any{"schema_version": 3, "task_id": "PLAN-TASK", "workflow_status": "not-started", "review_context": map[string]any{"check_ids": a["check_ids"], "candidate": map[string]any{"ref": "subject.json", "digest": safefs.Digest(raw)}, "basis": basis, "plan_review_binding": binding}}
	formalFile(t, root, "task.json", task)
	raw, _ = os.ReadFile(filepath.Join(root, "task.json"))
	a["task_digest"] = safefs.Digest(raw)
	formalFile(t, root, "cp.json", map[string]any{"feature_id": "feature.orders", "plan_review_control": c, "checks": map[string]any{"check.domain-strategy-approved": rows[0]}})
	s.V = kernel.NewView(root)
	if err = s.Verify("plan-review-task", "task.json", map[string]string{"checkpoint": "cp.json"}); err != nil {
		t.Fatal(err)
	}
	// A formed subject need not contain a future review cycle; task/control hold that binding.
	subject, _ := s.Doc("subject.json")
	delete(subject, "plan_review_binding")
	formalFile(t, root, "subject.json", subject)
	raw, _ = os.ReadFile(filepath.Join(root, "subject.json"))
	kernel.SemMap(kernel.SemMap(task["review_context"])["candidate"])["digest"] = safefs.Digest(raw)
	formalFile(t, root, "task.json", task)
	raw, _ = os.ReadFile(filepath.Join(root, "task.json"))
	a["task_digest"] = safefs.Digest(raw)
	formalFile(t, root, "cp.json", map[string]any{"feature_id": "feature.orders", "plan_review_control": c, "checks": map[string]any{"check.domain-strategy-approved": rows[0]}})
	s.V = kernel.NewView(root)
	if err = s.Verify("plan-review-task", "task.json", map[string]string{"checkpoint": "cp.json"}); err != nil {
		t.Fatal(err)
	}
	// Completion changes real task bytes; preserve issuance and bind the actual completed carrier in the existing result.
	issued, _ := os.ReadFile(filepath.Join(root, "task.json"))
	if err = os.WriteFile(filepath.Join(root, "issued.json"), issued, 0644); err != nil {
		t.Fatal(err)
	}
	task["workflow_status"] = "resolved"
	task["result"] = map[string]any{"result": "completed"}
	formalFile(t, root, "task.json", task)
	completed, _ := os.ReadFile(filepath.Join(root, "task.json"))
	report := map[string]any{"schema_version": 2, "kind": "plan-review-result", "plan_review_binding": binding, "outcome": "passed", "check_results": a["check_results"], "issued_task": map[string]any{"ref": "issued.json", "digest": safefs.Digest(issued)}, "completed_task": map[string]any{"ref": "task.json", "digest": safefs.Digest(completed)}}
	formalFile(t, root, "result.json", report)
	resultRaw, _ := os.ReadFile(filepath.Join(root, "result.json"))
	a["status"] = "completed"
	a["result_ref"] = "result.json"
	a["result_digest"] = safefs.Digest(resultRaw)
	formalFile(t, root, "cp.json", map[string]any{"feature_id": "feature.orders", "plan_review_control": c, "checks": map[string]any{"check.domain-strategy-approved": rows[0]}})
	s.V = kernel.NewView(root)
	if err = s.Verify("plan-review-task", "task.json", map[string]string{"checkpoint": "cp.json", "require-completed": "true"}); err != nil {
		t.Fatal(err)
	}
	formalFile(t, root, "draft.json", map[string]any{"changed": true})
	s.V = kernel.NewView(root)
	if err = s.Verify("plan-review-task", "task.json", nil); err == nil {
		t.Fatal("accepted stale actual source")
	}
}
