package kernel

import (
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"strings"
)

func SemanticDefinition(registry map[string]any, bucket, id string) map[string]any {
	for _, row := range SemList(registry[bucket]) {
		m := SemMap(row)
		if Text(m["id"]) == id {
			return m
		}
	}
	return nil
}

func (s *SemanticSession) Orchestration() (map[string]any, string, error) {
	ref := ".agents/skills/yss-product-lifecycle/references/orchestration-contract.yaml"
	if present, err := s.Exists(".template-spec/process/harness-profile.yaml"); err != nil {
		return nil, "", err
	} else if present {
		profile, err := s.Doc(".template-spec/process/harness-profile.yaml")
		if err != nil {
			return nil, "", err
		}
		preferred := ""
		for name, known := range domain.Profiles {
			if profile["profile_id"] == known.ID {
				preferred = GuidanceContractRef(name)
			}
		}
		if preferred == "" {
			return nil, "", s.Unavailable("IDENTITY", "无法识别当前 Profile 的主控合同")
		}
		if present, err := s.Exists(preferred); err != nil {
			return nil, "", err
		} else if present {
			ref = preferred
		}
	}
	exists, err := s.Exists(ref)
	if err != nil {
		return nil, "", err
	}
	if !exists {
		ref = ".template-spec/process/checkpoint-boundary.yaml"
	}
	doc, err := s.Doc(ref)
	if err == nil {
		version, valid := Integer(doc["schema_version"])
		if !valid || version != 1 && !(version == 2 && ref == GuidanceContractRef("design")) {
			return nil, ref, s.Unavailable("CAPABILITY", "未知本地主控合同版本")
		}
	}
	return doc, ref, err
}

func semanticCountersign(roles map[string]any, id string) (bool, error) {
	policy := SemMap(roles["gate_policy"])
	for _, bucket := range []string{"check_reviews", "dual_digital_human", "digital_human_review"} {
		for _, row := range SemList(policy[bucket]) {
			if Text(SemMap(row)["gate"]) == id {
				return true, nil
			}
		}
	}
	for _, bucket := range []string{"product_digital_human_with_biological_veto", "biological_human"} {
		if SemHas(SemStrings(policy[bucket]), id) {
			return true, nil
		}
	}
	for _, bucket := range []string{"evidence_only", "orchestrator", "automatic_checks"} {
		if SemHas(SemStrings(policy[bucket]), id) {
			return false, nil
		}
	}
	return false, fmt.Errorf("GATE_POLICY_REQUIRED: 未分类门禁或检查 %s", id)
}

// Gate consumers may provide partial state. Bind approval to that independent
// state without treating its transport file as a complete lifecycle checkpoint.
func (s *SemanticSession) gateStateApproval(item map[string]any, ref, id string) error {
	approvalRef := Text(item["approval_ref"])
	record, err := s.Doc(approvalRef)
	if err != nil {
		return err
	}
	schemaRef := ApprovalSchemaRef
	if Text(record["kind"]) == "review-bundle" {
		schemaRef = ".template-spec/process/schemas/review-bundle.schema.json"
	}
	if err = s.ValidateSchema(schemaRef, record); err != nil {
		return err
	}
	if err = RejectProgressionEvidence(s, approvalRef); err != nil {
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

func (s *SemanticSession) GateChecks(cp map[string]any, ref, gateID string) error {
	gate := SemanticDefinition(s.Registry, "gates", gateID)
	if gate == nil {
		return s.Reject("GATE_UNKNOWN", "未知门禁: "+gateID)
	}
	checks := SemMap(cp["checks"])
	seen := map[string]bool{}
	active := map[string]bool{}
	var check func(string) error
	proofs := func(item map[string]any, definition map[string]any, id string) error {
		if err := s.Basis(item["basis"]); err != nil {
			return err
		}
		refs := map[string]bool{}
		for _, row := range SemList(item["basis"]) {
			refs[Text(SemMap(row)["ref"])] = true
		}
		for _, kind := range SemStrings(definition["evidence"]) {
			rows := SemStrings(SemMap(item["evidence"])[kind])
			if len(rows) == 0 {
				return s.Reject("GATE_EVIDENCE", "缺少绑定证据: "+id+"/"+kind)
			}
			for _, r := range rows {
				if !refs[r] {
					return s.Reject("GATE_EVIDENCE", "证据未绑定当前依据: "+r)
				}
			}
		}
		required, err := semanticCountersign(s.Roles, id)
		if err != nil {
			return s.Unavailable("CAPABILITY", err.Error())
		}
		if required {
			if !refs[Text(item["approval_ref"])] || !refs[Text(item["subject_ref"])] {
				return s.Reject("APPROVAL_CONTEXT_REQUIRED", "未绑定批准与主体: "+id)
			}
			if err = s.gateStateApproval(item, ref, id); err != nil {
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
		definition := SemanticDefinition(s.Registry, "checks", id)
		item := SemMap(checks[id])
		if definition == nil || item == nil {
			return s.Reject("GATE_CHECK_REQUIRED", "缺少当前检查: "+id)
		}
		active[id] = true
		defer delete(active, id)
		if err := s.Basis(item["basis"]); err != nil {
			return err
		}
		if item["status"] == "not-applicable" {
			if item["applicable"] != false || strings.TrimSpace(Text(item["reason"])) == "" {
				return s.Reject("GATE_APPLICABILITY", "不适用检查缺少评估原因: "+id)
			}
			seen[id] = true
			s.Report.Applicability = append(s.Report.Applicability, map[string]any{"id": id, "status": "not-applicable", "reason": item["reason"], "source_ref": ref})
			return nil
		}
		if item["applicable"] != true || !SemHas([]string{"passed", "approved"}, Text(item["status"])) {
			return s.Reject("GATE_CHECK_BLOCKED", "当前检查未通过: "+id)
		}
		for _, dep := range SemStrings(definition["requires_checks"]) {
			if err := check(dep); err != nil {
				return err
			}
			if SemMap(checks[dep])["status"] == "not-applicable" {
				return s.Reject("GATE_DEPENDENCY", "通过检查依赖了不适用检查: "+id)
			}
		}
		if err := proofs(item, definition, id); err != nil {
			return err
		}
		seen[id] = true
		return nil
	}
	for _, id := range SemStrings(gate["requires_checks"]) {
		if err := check(id); err != nil {
			return err
		}
	}
	item := SemMap(SemMap(cp["gates"])[gateID])
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
		for _, row := range SemList(SemMap(checks[id])["basis"]) {
			m := SemMap(row)
			covered := false
			for _, binding := range SemList(item["basis"]) {
				b := SemMap(binding)
				if m["ref"] == b["ref"] && m["digest"] == b["digest"] {
					covered = true
				}
			}
			if !covered {
				return s.Reject("GATE_COVERAGE", "聚合批准缺少检查依据: "+id+"/"+Text(m["ref"]))
			}
		}
	}
	return nil
}
