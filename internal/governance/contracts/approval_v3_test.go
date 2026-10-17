package contracts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance/approval"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func approval3Fixture(t *testing.T) (*kernel.SemanticSession, map[string]any, map[string]any) {
	s, task := completedReviewer(t, true)
	approval.Bind(approval.Inputs{TrustedTaskImplementation: TrustedTaskImplementation})

	taskFile(t, s.Root, kernel.ApprovalSchemaRef, map[string]any{"type": "object"})
	taskFile(t, s.Root, ".template-spec/process/schemas/lifecycle-checkpoint.schema.json", map[string]any{"type": "object"})
	review := kernel.ApMap(task["review_context"])
	c := kernel.ApMap(review["candidate"])
	raw, _ := os.ReadFile(filepath.Join(s.Root, "review-task.json"))
	record := map[string]any{"schema_version": 3, "gate_id": "gate.spec-baseline-approved", "decision": "approved", "actor_kind": "agent", "subject_ref": c["ref"], "subject_digest": c["digest"], "approval_scope": review["approval_scope"], "basis": review["basis"], "evidence_refs": []string{"review-log.json"}, "review_task_ref": "review-task.json", "review_task_digest": safefs.Digest(raw), "check_ids": review["check_ids"], "policy": task["policy"], "review": review["review"]}
	taskFile(t, s.Root, "approval.json", record)
	state := map[string]any{"subject_ref": c["ref"], "subject_digest": c["digest"], "approval_scope": review["approval_scope"], "basis": review["basis"], "approval_ref": "approval.json"}
	taskFile(t, s.Root, "checkpoint.json", map[string]any{"gates": map[string]any{"gate.spec-baseline-approved": state}})
	s.V = kernel.NewView(s.Root)
	return s, record, task
}

func TestApproval3UsesActualCompletedReviewAndOutput(t *testing.T) {
	s, _, _ := approval3Fixture(t)
	if err := s.Verify("approval", "approval.json", map[string]string{"checkpoint": "checkpoint.json", "gate": "gate.spec-baseline-approved", "require-approved": "true"}); err != nil {
		t.Fatal(err)
	}
}

func TestApproval3RejectsChangedOrUnprovenEvidence(t *testing.T) {
	for _, scenario := range []string{"stale-subject", "stale-task", "missing-basis", "wrong-scope", "fake-review", "wrong-checks", "not-approved", "human-without-reply", "required-user-reply", "legacy", "cross-profile", "open-result"} {
		t.Run(scenario, func(t *testing.T) {
			s, record, task := approval3Fixture(t)
			switch scenario {
			case "stale-subject":
				taskFile(t, s.Root, "drafts/spec.md", map[string]any{"changed": true})
			case "stale-task":
				record["review_task_digest"] = kernel.ApDigest("stale")
			case "missing-basis":
				record["basis"] = []any{}
			case "wrong-scope":
				record["approval_scope"] = []string{"elsewhere"}
			case "fake-review":
				record["review"] = map[string]any{"mode": "independent-review", "provenance": task["provenance"]}
			case "wrong-checks":
				record["check_ids"] = []string{"check.other"}
			case "not-approved":
				record["decision"] = "rejected"
			case "human-without-reply":
				record["actor_kind"] = "human"
			case "required-user-reply":
				s.GovernancePolicy["user_decision_policy"] = map[string]any{"profile_scope": map[string]any{"backend": map[string]any{"gates": []string{"gate.spec-baseline-approved"}}}}
			case "legacy":
				record["schema_version"] = 2
			case "cross-profile":
				s.Report.Profile = "spec"
			case "open-result":
				kernel.ApMap(task["result"])["blocking_signals"] = []string{"open"}
				taskFile(t, s.Root, "review-task.json", task)
				raw, _ := os.ReadFile(filepath.Join(s.Root, "review-task.json"))
				record["review_task_digest"] = safefs.Digest(raw)
			}
			taskFile(t, s.Root, "approval.json", record)
			s.V = kernel.NewView(s.Root)
			if err := s.Verify("approval", "approval.json", map[string]string{"checkpoint": "checkpoint.json", "gate": "gate.spec-baseline-approved", "require-approved": "true"}); err == nil {
				t.Fatal("invalid approval accepted")
			}
		})
	}
}

func TestReviewBundle3UsesActualReviewWithoutRoles(t *testing.T) {
	s, record, task := approval3Fixture(t)
	taskFile(t, s.Root, ".template-spec/process/schemas/review-bundle.schema.json", map[string]any{"type": "object"})
	bundle := map[string]any{"kind": "review-bundle", "schema_version": 3, "bundle_id": "bundle", "task_id": task["task_id"], "work_unit_id": task["work_unit_id"], "reviews": []any{record}}
	for _, key := range []string{"review_task_ref", "review_task_digest", "policy", "review", "check_ids", "basis"} {
		bundle[key] = record[key]
	}
	taskFile(t, s.Root, "bundle.json", bundle)
	if err := s.Verify("approval", "bundle.json", map[string]string{"checkpoint": "checkpoint.json", "gate": "gate.spec-baseline-approved", "require-approved": "true"}); err != nil {
		t.Fatal(err)
	}
	bundle["review"] = map[string]any{"mode": "independent-review"}
	taskFile(t, s.Root, "bundle.json", bundle)
	s.V = kernel.NewView(s.Root)
	if err := s.Verify("approval", "bundle.json", map[string]string{"checkpoint": "checkpoint.json", "gate": "gate.spec-baseline-approved", "require-approved": "true"}); err == nil {
		t.Fatal("bundle replaced actual review")
	}
}

func continuation2Fixture(t *testing.T) (*kernel.SemanticSession, map[string]any) {
	s, _, task := approval3Fixture(t)
	s.GovernancePolicy["user_decision_policy"] = map[string]any{"source_kinds": []string{"session-export"}, "continuation": map[string]any{"capability": "role-independent-governance-v1", "boundaries_by_profile": map[string]any{"backend": []string{"gate.spec-baseline-approved"}}}}
	taskFile(t, s.Root, ".template-spec/process/schemas/user-decision.schema.json", map[string]any{"type": "object"})
	taskFile(t, s.Root, ".template-spec/process/schemas/approved-scope-continuation.schema.json", map[string]any{"type": "object"})
	taskFile(t, s.Root, ".template-spec/process/schemas/delivery-authorization.schema.json", map[string]any{"type": "object"})
	review := kernel.ApMap(task["review_context"])
	candidate := kernel.ApMap(review["candidate"])
	frozen := []byte(`{"current":"draft output"}`)
	if err := os.WriteFile(filepath.Join(s.Root, "before.json"), frozen, 0644); err != nil {
		t.Fatal(err)
	}
	before := map[string]any{"ref": "before.json", "digest": safefs.Digest(frozen)}
	protected := map[string]any{}
	for _, key := range []string{"business_scope", "acceptance", "contract_commitments", "authorization", "risk_acceptance", "quality", "external_commitments"} {
		protected[key] = []string{"/body"}
	}
	mandate := map[string]any{"schema_version": 2, "kind": "delivery-authorization", "basis": []any{map[string]any{"ref": candidate["ref"], "digest": before["digest"]}}, "external_rules": map[string]any{"state": "none-known"}, "targets": []any{map[string]any{"boundary": "gate.spec-baseline-approved", "subject_ref": candidate["ref"], "scope": review["approval_scope"], "decision_basis": protected}}}
	taskFile(t, s.Root, "mandate.json", mandate)
	raw, _ := os.ReadFile(filepath.Join(s.Root, "mandate.json"))
	item := map[string]any{"id": "D1", "boundary": "delivery-scope", "title": "批准已形成方案", "subject": map[string]any{"ref": "mandate.json", "version": "v1", "digest": kernel.ApDigest(raw)}, "basis": []any{}, "changes": "已形成的明确方案", "scope": review["approval_scope"], "risks": []string{}, "recommendation": "批准", "next_actions": []string{"继续批准范围"}, "responder_ref": "human"}
	requestSource := map[string]any{"source_kind": "session-export", "messages": []any{map[string]any{"id": "req", "actor_kind": "biological-human", "principal_ref": "human", "original_ref": "chat:req", "sent_at": "2026-01-01T00:00:00Z", "text": "准备方案"}}}
	taskFile(t, s.Root, "human-request.json", requestSource)
	raw, _ = os.ReadFile(filepath.Join(s.Root, "human-request.json"))
	request := map[string]any{"id": "REQ-1", "requester_ref": "human", "requester_source": map[string]any{"kind": "session-export", "ref": "human-request.json", "digest": kernel.ApDigest(raw), "message_id": "req"}, "items": []any{item}}
	requestDigest := kernel.ApDigest(request)
	capture := map[string]any{"source_kind": "session-export", "messages": []any{map[string]any{"id": "show", "principal_ref": "agent", "original_ref": "chat:show", "sent_at": "2026-01-01T00:01:00Z", "text": "批准已形成方案 mandate.json v1 已形成的明确方案 drafts 批准 继续批准范围"}, map[string]any{"id": "reply", "actor_kind": "biological-human", "principal_ref": "human", "original_ref": "chat:reply", "sent_at": "2026-01-01T00:02:00Z", "text": "批准", "reply_to": "chat:show"}}}
	taskFile(t, s.Root, "human-reply.json", capture)
	raw, _ = os.ReadFile(filepath.Join(s.Root, "human-reply.json"))
	source := func(id string) map[string]any {
		return map[string]any{"kind": "session-export", "ref": "human-reply.json", "digest": kernel.ApDigest(raw), "message_id": id}
	}
	request["presented_source"] = source("show")
	decision := map[string]any{"schema_version": 1, "request": request, "responses": []any{map[string]any{"source": source("reply"), "principal_ref": "human", "responded_at": "2026-01-01T00:02:00Z", "text": "批准", "request_digest": requestDigest, "decision": "approved", "selection": "single", "item_ids": []string{"D1"}, "approved_scope": review["approval_scope"]}}}
	taskFile(t, s.Root, "decision.json", decision)
	taskFile(t, s.Root, "delta.json", map[string]any{"schema_version": 1, "changes": []any{}})
	raw, _ = os.ReadFile(filepath.Join(s.Root, "delta.json"))
	proof := map[string]any{"schema_version": 2, "kind": "approved-scope-continuation-v2", "boundary": "gate.spec-baseline-approved", "scope": review["approval_scope"], "subject": candidate, "basis": review["basis"], "source": map[string]any{"user_decision_ref": "decision.json", "boundary": "delivery-scope", "subject_ref": "mandate.json", "scope": review["approval_scope"]}, "comparison": map[string]any{"before": before, "after": candidate, "differences": []any{map[string]any{"ref": "delta.json", "digest": safefs.Digest(raw), "classification": "presentation-only", "reason": "JSON表示格式变化"}}, "affected_check_ids": review["check_ids"], "material_changes": []any{}, "decision_basis_unchanged": true}, "verification_results": task["verification_results"], "review": review["review"], "external_rules": map[string]any{"state": "none-known"}}
	taskFile(t, s.Root, "continuation.json", proof)
	requirement := map[string]any{"boundary": "gate.spec-baseline-approved", "subject_ref": candidate["ref"], "scope": review["approval_scope"], "continuation_ref": "continuation.json"}
	taskFile(t, s.Root, "requirements.json", requirement)
	s.V = kernel.NewView(s.Root)
	return s, proof
}
func TestContinuation2UsesOriginalHumanApprovalAndActualDelta(t *testing.T) {
	s, _ := continuation2Fixture(t)
	if err := s.Verify("user-decision", "continuation.json", map[string]string{"requirements": "requirements.json", "continuation": "true"}); err != nil {
		t.Fatal(err)
	}
}
func TestContinuationImplementationManifestChecksActualContracts(t *testing.T) {
	for _, scenario := range []string{"current", "expanded-write", "wrong-repository", "stale-contract", "stale-baseline", "duplicate-ticket"} {
		t.Run(scenario, func(t *testing.T) {
			s, _, _ := approval3Fixture(t)
			taskFile(t, s.Root, "baseline.json", map[string]any{"version": "current"})
			baseline, _ := os.ReadFile(filepath.Join(s.Root, "baseline.json"))
			contract := map[string]any{"schema_version": 2, "lifecycle_refs": map[string]any{"ticket": "TICKET-1", "engineering_baseline": "baseline.json"}, "common": map[string]any{"project_roots": []string{s.Root}, "allowed_write_paths": []string{"src"}}}
			taskFile(t, s.Root, "slice.json", contract)
			raw, _ := os.ReadFile(filepath.Join(s.Root, "slice.json"))
			row := map[string]any{"ticket_ref": "TICKET-1", "contract": map[string]any{"ref": "slice.json", "digest": kernel.ApDigest(raw)}, "repositories": []string{s.Root}, "allowed_write_paths": []string{"src"}, "baselines": []any{map[string]any{"ref": "baseline.json", "digest": kernel.ApDigest(baseline)}}}
			rows := []any{row}
			switch scenario {
			case "expanded-write":
				row["allowed_write_paths"] = []string{"src", "private"}
			case "wrong-repository":
				row["repositories"] = []string{"/other"}
			case "stale-contract":
				taskFile(t, s.Root, "slice.json", map[string]any{"changed": true})
			case "stale-baseline":
				taskFile(t, s.Root, "baseline.json", map[string]any{"changed": true})
			case "duplicate-ticket":
				rows = append(rows, row)
			}
			taskFile(t, s.Root, "manifest.json", map[string]any{"kind": "implementation-scope", "slices": rows})
			s.V = kernel.NewView(s.Root)
			err := ValidateImplementationManifestSemantic(s, "manifest.json", []string{"TICKET-1"})
			if (err == nil) != (scenario == "current") {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}

func requiredReviewer(t *testing.T) (*kernel.SemanticSession, map[string]any) {
	s, task := completedReviewer(t, true)
	s.GovernancePolicy["user_decision_policy"] = map[string]any{"source_kinds": []string{"session-export"}}
	taskFile(t, s.Root, "request-source.json", map[string]any{"source_kind": "session-export", "messages": []any{map[string]any{"id": "R1", "actor_kind": "biological-human", "principal_ref": "user", "original_ref": "conversation:request", "sent_at": "2026-01-01T00:00:00Z", "text": "请独立复核 drafts/spec.md"}}})
	raw, _ := os.ReadFile(filepath.Join(s.Root, "request-source.json"))
	taskFile(t, s.Root, "review-requirement.json", map[string]any{"kind": "review-requirement", "mode": "independent-review", "scope": []string{"drafts/spec.md"}, "source": map[string]any{"kind": "session-export", "ref": "request-source.json", "digest": kernel.ApDigest(raw), "message_id": "R1"}})
	raw, _ = os.ReadFile(filepath.Join(s.Root, "review-requirement.json"))
	binding := map[string]any{"ref": "review-requirement.json", "digest": safefs.Digest(raw)}
	task["inputs"] = append(kernel.ApStrings(task["inputs"]), "review-requirement.json")
	task["input_bindings"] = append(kernel.ApArray(task["input_bindings"]), binding)
	context := kernel.ApMap(task["review_context"])
	context["basis"] = append(kernel.ApArray(context["basis"]), binding)
	log, _ := s.Doc("review-log.json")
	log["input_bindings"] = task["input_bindings"]
	taskFile(t, s.Root, "review-log.json", log)
	raw, _ = os.ReadFile(filepath.Join(s.Root, "review-log.json"))
	kernel.ApRows(task["verification_results"])[0]["evidence_digest"] = safefs.Digest(raw)
	taskFile(t, s.Root, "review-task.json", task)
	s.V = kernel.NewView(s.Root)
	return s, task
}
func TestCompletedSelfCheckCannotDowngradeBoundUserRequirement(t *testing.T) {
	s, _ := requiredReviewer(t)
	err := s.Verify("task", "review-task.json", nil)
	if err == nil || !strings.Contains(err.Error(), "不能替代当前必需复核") {
		t.Fatalf("bound child-path human requirement was not enforced: %v", err)
	}
}

func TestRequiredChildPathReviewUsesActualIndependentContexts(t *testing.T) {
	for _, scenario := range []string{"separate-contexts", "same-context"} {
		t.Run(scenario, func(t *testing.T) {
			s, task := requiredReviewer(t)
			implementation, _ := s.Doc("task.json")
			implementation = ContractCopy(implementation)
			ip := kernel.ApMap(implementation["provenance"])
			ip["identity"] = map[string]any{"status": "available", "principal_ref": "same-account", "session_ref": "implementation-session"}
			ilog, _ := s.Doc("origin.json")
			ilog = ContractCopy(ilog)
			ilog["identity"] = ip["identity"]
			taskFile(t, s.Root, "origin.json", ilog)
			raw, _ := os.ReadFile(filepath.Join(s.Root, "origin.json"))
			kernel.ApRows(implementation["verification_results"])[0]["evidence_digest"] = safefs.Digest(raw)
			taskFile(t, s.Root, "task.json", implementation)
			raw, _ = os.ReadFile(filepath.Join(s.Root, "task.json"))
			context := kernel.ApMap(task["review_context"])
			for _, bindings := range []any{task["input_bindings"], context["basis"]} {
				for _, binding := range kernel.ApRows(bindings) {
					if binding["ref"] == "task.json" {
						binding["digest"] = safefs.Digest(raw)
					}
				}
			}
			rp := kernel.ApMap(task["provenance"])
			session := "review-session"
			if scenario == "same-context" {
				session = "implementation-session"
			}
			rp["identity"] = map[string]any{"status": "available", "principal_ref": "same-account", "session_ref": session}
			rlog, _ := s.Doc("review-log.json")
			rlog = ContractCopy(rlog)
			rlog["identity"] = rp["identity"]
			rlog["input_bindings"] = task["input_bindings"]
			taskFile(t, s.Root, "review-log.json", rlog)
			raw, _ = os.ReadFile(filepath.Join(s.Root, "review-log.json"))
			kernel.ApRows(task["verification_results"])[0]["evidence_digest"] = safefs.Digest(raw)
			context["review"] = map[string]any{"mode": "independent-review", "provenance": rp, "independence": map[string]any{"implementation": ip, "reviewer": rp, "implementation_context_ref": "implementation-context.json", "reviewer_context_ref": "review-context.json", "evidence": map[string]any{"ref": "review-log.json", "digest": safefs.Digest(raw)}, "candidate": context["candidate"], "scope": context["approval_scope"], "readonly": true}}
			for _, row := range []struct {
				ref    string
				p, log map[string]any
			}{{"implementation-context.json", ip, ilog}, {"review-context.json", rp, rlog}} {
				identity := kernel.ApMap(row.p["identity"])
				taskFile(t, s.Root, row.ref, map[string]any{"source_kind": "session-export", "session_ref": identity["session_ref"], "messages": []any{map[string]any{"id": "trace", "original_ref": row.ref + "/trace", "principal_ref": "same-account", "sent_at": "2026-01-01T00:00:00Z", "text": "executed " + kernel.Text(row.p["execution_ref"]) + " for " + kernel.Text(row.log["candidate_digest"])}}})
			}
			taskFile(t, s.Root, "review-task.json", task)
			s.V = kernel.NewView(s.Root)
			err := s.Verify("task", "review-task.json", nil)
			if (err == nil) != (scenario == "separate-contexts") {
				t.Fatalf("%s: %v", scenario, err)
			}
		})
	}
}

func TestContinuation2RejectsDifferentImplementationSource(t *testing.T) {
	s, proof := continuation2Fixture(t)
	task, _ := s.Doc("task.json")
	task = ContractCopy(task)
	provenance := ContractCopy(kernel.ApMap(task["provenance"]))
	provenance["task_ref"] = "other-task.json"
	provenance["execution_ref"] = "other-log.json"
	task["provenance"] = provenance
	log, _ := s.Doc("origin.json")
	log = ContractCopy(log)
	log["task_ref"] = "other-task.json"
	taskFile(t, s.Root, "other-log.json", log)
	raw, _ := os.ReadFile(filepath.Join(s.Root, "other-log.json"))
	row := ContractCopy(kernel.ApRows(task["verification_results"])[0])
	row["evidence_ref"] = "other-log.json"
	row["evidence_digest"] = safefs.Digest(raw)
	task["verification_results"] = []any{row}
	taskFile(t, s.Root, "other-task.json", task)
	s.V = kernel.NewView(s.Root)
	if err := s.Verify("task", "other-task.json", nil); err != nil {
		t.Fatalf("test requires second actually valid completed source: %v", err)
	}
	raw, _ = os.ReadFile(filepath.Join(s.Root, "other-task.json"))
	proof["basis"] = []any{map[string]any{"ref": "other-task.json", "digest": safefs.Digest(raw)}}
	taskFile(t, s.Root, "continuation.json", proof)
	s.V = kernel.NewView(s.Root)
	err := s.Verify("user-decision", "continuation.json", map[string]string{"requirements": "requirements.json", "continuation": "true"})
	if err == nil || !strings.Contains(err.Error(), "实施来源不一致") {
		t.Fatalf("different real source was not rejected: %v", err)
	}
}

func TestPolicy2GateChecksConsumeCurrentApprovalWithoutRoleRegistry(t *testing.T) {
	s, record, _ := approval3Fixture(t)
	policy := kernel.ApMap(kernel.ApMap(kernel.ApMap(s.GovernancePolicy["gate_policy"])["profiles"])["backend"])
	policy["user_decision_gates"] = []any{}
	policy["technical_reviews"] = []any{map[string]any{"gate": "gate.spec-baseline-approved", "capability_ids": []string{"capability.spec-acceptance"}}}
	s.GovernancePolicy["technical_checks"] = []any{map[string]any{"id": "capability.spec-acceptance", "profiles": []string{"backend"}, "review_skills": []string{"yss-product-lifecycle"}}}
	s.Registry["gates"] = []any{map[string]any{"id": "gate.spec-baseline-approved"}}
	cp, err := s.Doc("checkpoint.json")
	if err != nil {
		t.Fatal(err)
	}
	item := kernel.ApMap(kernel.ApMap(cp["gates"])["gate.spec-baseline-approved"])
	item["status"] = "approved"
	raw, _ := os.ReadFile(filepath.Join(s.Root, "approval.json"))
	item["basis"] = append(kernel.ApArray(item["basis"]), map[string]any{"ref": "approval.json", "digest": safefs.Digest(raw)}, map[string]any{"ref": record["subject_ref"], "digest": record["subject_digest"]})
	if err = approval.GateChecks(s, cp, "checkpoint.json", "gate.spec-baseline-approved"); err != nil {
		t.Fatal(err)
	}
	delete(item, "approval_ref")
	if err = approval.GateChecks(s, cp, "checkpoint.json", "gate.spec-baseline-approved"); err == nil {
		t.Fatal("accepted missing actual approval")
	}
}

func TestPlan3WithoutApplicableProfessionalChecksUsesActualSelfCheckWithoutEmptyTask(t *testing.T) {
	s, task := completedDraft(t, func(s *kernel.SemanticSession, task map[string]any) {
		task["feature_id"] = "feature.orders"
		task["checkpoint_ref"] = "feature.json"
		taskFile(t, s.Root, "feature.json", map[string]any{"feature_id": "feature.orders"})
		input, _ := s.Doc("input.json")
		input["feature_id"] = "feature.orders"
		taskFile(t, s.Root, "input.json", input)
		raw, _ := os.ReadFile(filepath.Join(s.Root, "input.json"))
		task["input_bindings"] = []any{map[string]any{"ref": "input.json", "digest": safefs.Digest(raw)}}
	})
	approval.Bind(approval.Inputs{TrustedTaskImplementation: TrustedTaskImplementation})
	policy := kernel.ApMap(kernel.ApMap(kernel.ApMap(s.GovernancePolicy["gate_policy"])["profiles"])["backend"])
	policy["technical_reviews"] = []any{map[string]any{"gate": "gate.plan-approved"}}
	taskFile(t, s.Root, kernel.ApprovalSchemaRef, map[string]any{"type": "object"})
	raw, _ := os.ReadFile(filepath.Join(s.Root, "drafts/spec.md"))
	subject := map[string]any{"ref": "drafts/spec.md", "digest": safefs.Digest(raw)}
	log, _ := s.Doc("origin.json")
	log = kernel.ApCopy(log)
	log["candidate_digest"] = subject["digest"]
	log["scope"] = []string{"feature.orders"}
	taskFile(t, s.Root, "plan-selfcheck.json", log)
	provenance := kernel.ApCopy(kernel.ApMap(task["provenance"]))
	provenance["execution_ref"] = "plan-selfcheck.json"
	raw, _ = os.ReadFile(filepath.Join(s.Root, "task.json"))
	basis := []any{map[string]any{"ref": "task.json", "digest": safefs.Digest(raw)}}
	record := map[string]any{"schema_version": 3, "gate_id": "gate.plan-approved", "decision": "approved", "actor_kind": "agent", "subject_ref": subject["ref"], "subject_digest": subject["digest"], "approval_scope": []string{"feature.orders"}, "basis": basis, "policy": task["policy"], "check_ids": kernel.ApMap(task["skill_source"])["check_ids"], "review": map[string]any{"mode": "self-check", "provenance": provenance}, "evidence_refs": []string{"plan-selfcheck.json"}}
	taskFile(t, s.Root, "plan-approval.json", record)
	s.V = kernel.NewView(s.Root)
	cp := map[string]any{"feature_id": "feature.orders", "plan_review_ref": subject["ref"], "plan_approval_ref": "plan-approval.json", "plan_review_control": map[string]any{"attempts": []any{}}}
	review := map[string]any{"internal_checks": map[string]any{"check.domain-strategy-approved": map[string]any{"status": "not-applicable"}, "check.stage-decision-package-approved": map[string]any{"status": "not-applicable"}}}
	if err := approval.PlanAggregateRecord(s, cp, review, basis, map[string]map[string]any{}); err != nil {
		t.Fatal(err)
	}
	record["review"] = map[string]any{"mode": "self-check", "provenance": task["provenance"]}
	taskFile(t, s.Root, "plan-approval.json", record)
	s.V = kernel.NewView(s.Root)
	if err := approval.PlanAggregateRecord(s, cp, review, basis, map[string]map[string]any{}); err == nil {
		t.Fatal("accepted implementation log as raw-subject self-check")
	}
}
