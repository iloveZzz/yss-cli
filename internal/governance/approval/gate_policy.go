package approval

import (
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"strings"
)

func semanticCountersign(roles map[string]any, id string) (bool, error) {
	policy := kernel.SemMap(roles["gate_policy"])
	for _, bucket := range []string{"check_reviews", "dual_digital_human", "digital_human_review"} {
		for _, row := range kernel.SemList(policy[bucket]) {
			if kernel.Text(kernel.SemMap(row)["gate"]) == id {
				return true, nil
			}
		}
	}
	for _, bucket := range []string{"product_digital_human_with_biological_veto", "biological_human"} {
		if kernel.SemHas(kernel.SemStrings(policy[bucket]), id) {
			return true, nil
		}
	}
	for _, bucket := range []string{"evidence_only", "orchestrator", "automatic_checks"} {
		if kernel.SemHas(kernel.SemStrings(policy[bucket]), id) {
			return false, nil
		}
	}
	return false, fmt.Errorf("GATE_POLICY_REQUIRED: 未分类门禁或检查 %s", id)
}

// Gate consumers may provide partial state. Bind approval to that independent
// state without treating its transport file as a complete lifecycle checkpoint.
func gateStateApproval(s *kernel.SemanticSession, item map[string]any, ref, id string) error {
	approvalRef := kernel.Text(item["approval_ref"])
	record, err := s.Doc(approvalRef)
	if err != nil {
		return err
	}
	schemaRef := kernel.ApprovalSchemaRef
	if kernel.Text(record["kind"]) == "review-bundle" {
		schemaRef = ".template-spec/process/schemas/review-bundle.schema.json"
	}
	if err = s.ValidateSchema(schemaRef, record); err != nil {
		return err
	}
	if err = kernel.RejectProgressionEvidence(s, approvalRef); err != nil {
		return err
	}
	if err = CurrentApprovalReferencesSemantic(s, record); err != nil {
		return err
	}
	selected, err := SelectApprovalRecordSemantic(s, record, id)
	if err != nil {
		return err
	}
	if id == "gate.plan-approved" {
		if err = s.Verify("plan-aggregate", ref, map[string]string{"approval": approvalRef, "gate": id}); err != nil {
			return err
		}
	} else {
		expected, err := ApprovalExpectationFromState(s, id, item)
		if err != nil {
			return err
		}
		if err = AssertCurrentApprovalSemantic(s, selected, expected); err != nil {
			return err
		}
		rule, err := ApprovalRuleSemantic(s, selected)
		if err != nil {
			return err
		}
		if s.Report.Coverage == nil {
			s.Report.Coverage = map[string]any{}
		}
		validated, _ := s.Report.Coverage["approval_validated"].([]any)
		s.Report.Coverage["approval_validated"] = append(validated, map[string]any{"gate_id": id, "rule": rule})
	}
	s.Report.Applicability = append(s.Report.Applicability, map[string]any{"kind": "approval", "mode": "current-binding", "execution_authorization": "not-evaluated"})
	return nil
}

func GateChecks(s *kernel.SemanticSession, cp map[string]any, ref, gateID string) error {
	gate := kernel.SemanticDefinition(s.Registry, "gates", gateID)
	if gate == nil {
		return s.Reject("GATE_UNKNOWN", "未知门禁: "+gateID)
	}
	checks := kernel.SemMap(cp["checks"])
	seen := map[string]bool{}
	active := map[string]bool{}
	var check func(string) error
	proofs := func(item map[string]any, definition map[string]any, id string) error {
		if err := s.Basis(item["basis"]); err != nil {
			return err
		}
		refs := map[string]bool{}
		for _, row := range kernel.SemList(item["basis"]) {
			refs[kernel.Text(kernel.SemMap(row)["ref"])] = true
		}
		for _, kind := range kernel.SemStrings(definition["evidence"]) {
			rows := kernel.SemStrings(kernel.SemMap(item["evidence"])[kind])
			if len(rows) == 0 {
				return s.Reject("GATE_EVIDENCE", "缺少绑定证据: "+id+"/"+kind)
			}
			for _, r := range rows {
				if !refs[r] {
					return s.Reject("GATE_EVIDENCE", "证据未绑定当前依据: "+r)
				}
			}
		}
		required, err := currentGateCountersign(s, id)
		if err != nil {
			return s.Unavailable("CAPABILITY", err.Error())
		}
		if s.GovernanceVersion == 2 && kernel.ApContains(s.GatePolicy()["contract_approval_gates"], id) {
			subject := map[string]any{"ref": item["subject_ref"], "digest": item["subject_digest"]}
			if _, err = s.EvidenceAsset(subject); err != nil {
				return err
			}
			implementation := kernel.ApCopy(kernel.ApMap(kernel.ApMap(cp["human_review"])["implementation"]))
			if implementation["slice_contract_ref"] != item["subject_ref"] {
				return s.Reject("SLICE_APPROVAL_REQUIRED", "合同批准未绑定当前实施范围")
			}
			implementation["user_decisions"] = kernel.ApMap(cp["human_review"])["user_decisions"]
			if dependencies.AssertImplementationDecisionSemantic == nil {
				return s.Unavailable("CAPABILITY", "缺少实际实施范围消费者")
			}
			if err = dependencies.AssertImplementationDecisionSemantic(s, implementation); err != nil {
				return err
			}
		}
		if required {
			if !refs[kernel.Text(item["approval_ref"])] || !refs[kernel.Text(item["subject_ref"])] {
				return s.Reject("APPROVAL_CONTEXT_REQUIRED", "未绑定批准与主体: "+id)
			}
			if err = gateStateApproval(s, item, ref, id); err != nil {
				return err
			}
		}
		return nil
	}
	check = func(id string) error {
		if active[id] {
			return s.Reject("REFERENCE_CYCLE", "门禁依赖循环: "+id)
		}
		if seen[id] {
			return nil
		}
		definition := kernel.SemanticDefinition(s.Registry, "checks", id)
		item := kernel.SemMap(checks[id])
		if definition == nil || item == nil {
			return s.Reject("GATE_CHECK_REQUIRED", "缺少当前检查: "+id)
		}
		active[id] = true
		defer delete(active, id)
		if err := s.Basis(item["basis"]); err != nil {
			return err
		}
		if item["status"] == "not-applicable" {
			if item["applicable"] != false || strings.TrimSpace(kernel.Text(item["reason"])) == "" {
				return s.Reject("GATE_APPLICABILITY", "不适用检查缺少评估原因: "+id)
			}
			seen[id] = true
			s.Report.Applicability = append(s.Report.Applicability, map[string]any{"id": id, "status": "not-applicable", "reason": item["reason"], "source_ref": ref})
			return nil
		}
		if item["applicable"] != true || !kernel.SemHas([]string{"passed", "approved"}, kernel.Text(item["status"])) {
			return s.Reject("GATE_CHECK_BLOCKED", "当前检查未通过: "+id)
		}
		for _, dep := range kernel.SemStrings(definition["requires_checks"]) {
			if err := check(dep); err != nil {
				return err
			}
			if kernel.SemMap(checks[dep])["status"] == "not-applicable" {
				return s.Reject("GATE_DEPENDENCY", "通过检查依赖了不适用检查: "+id)
			}
		}
		if err := proofs(item, definition, id); err != nil {
			return err
		}
		seen[id] = true
		return nil
	}
	for _, id := range kernel.SemStrings(gate["requires_checks"]) {
		if err := check(id); err != nil {
			return err
		}
	}
	item := kernel.SemMap(kernel.SemMap(cp["gates"])[gateID])
	if item["status"] != "approved" {
		return s.Reject("GATE_BLOCKED", "门禁未批准: "+gateID)
	}
	if gateID == "gate.plan-approved" {
		if err := s.Verify("plan-aggregate", ref, map[string]string{}); err != nil {
			return err
		}
	}
	if err := proofs(item, gate, gateID); err != nil {
		return err
	}
	for id := range seen {
		for _, row := range kernel.SemList(kernel.SemMap(checks[id])["basis"]) {
			m := kernel.SemMap(row)
			covered := false
			for _, binding := range kernel.SemList(item["basis"]) {
				b := kernel.SemMap(binding)
				if m["ref"] == b["ref"] && m["digest"] == b["digest"] {
					covered = true
				}
			}
			if !covered {
				return s.Reject("GATE_COVERAGE", "聚合批准缺少检查依据: "+id+"/"+kernel.Text(m["ref"]))
			}
		}
	}
	return nil
}

func currentGateCountersign(s *kernel.SemanticSession, id string) (bool, error) {
	if s.GovernanceVersion != 2 {
		return semanticCountersign(s.Roles, id)
	}
	policy := s.GatePolicy()
	for _, bucket := range []string{"check_reviews", "technical_reviews"} {
		if kernel.ApFind(policy[bucket], "gate", id) != nil {
			return true, nil
		}
	}
	if kernel.ApContains(policy["user_decision_gates"], id) {
		return true, nil
	}
	for _, bucket := range []string{"automatic_checks", "contract_approval_gates", "evidence_only"} {
		if kernel.ApContains(policy[bucket], id) {
			return false, nil
		}
	}
	return false, fmt.Errorf("GATE_POLICY_REQUIRED: 当前Profile未分类门禁或检查 %s", id)
}
