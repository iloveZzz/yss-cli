package governance

import (
	"regexp"
	"strings"
)

// Local Spec milestones consume the same approved Slice, independent review
// and actual contract/deployment records without inventing a strategic package.
func backendVerifyLocalTerminal(s *semanticSession, ref string, record map[string]any, a backendAuthorization, opts map[string]string) error {
	fullFeature, err := backendLocalFullFeature(s, a)
	if err != nil {
		return err
	}
	return backendVerifyLocalTerminalMode(s, ref, record, a, opts, !fullFeature)
}

// A full Spec feature may contain approved frontend work while its backend
// evidence is consumed. An explicit external owner cannot be replaced locally;
// permanent execution scopes and dedicated profiles retain their responsibility.
func backendLocalFullFeature(s *semanticSession, a backendAuthorization) (bool, error) {
	if a.Mode != "feature-target" {
		return false, nil
	}
	local, err := hasLocalImplementationInputs(s)
	if err != nil || !local {
		return false, err
	}
	configRef, cp, err := progressionLocation(s, a.CheckpointRef)
	if err != nil {
		return false, err
	}
	present, err := s.exists(configRef)
	if err != nil {
		return false, err
	}
	if !present {
		return true, nil
	}
	raw, err := s.bytes(configRef)
	if err != nil {
		return false, err
	}
	intent, err := parseProgressionTarget(raw)
	if err != nil {
		return false, err
	}
	if intent.FeatureID != text(cp["feature_id"]) || intent.CheckpointRef != a.CheckpointRef {
		return false, s.reject("PROGRESSION_BINDING", "本地后端证据目标未绑定当前功能")
	}
	for _, consumer := range intent.Consumers {
		if consumer.Profile == "backend" && consumer.Root != s.root {
			return false, nil
		}
	}
	return true, nil
}

// Both full Spec milestones and business implementation consume this existing
// evidence closure. The narrower scope mode remains explicit at its callers.
func backendVerifyLocalTerminalMode(s *semanticSession, ref string, record map[string]any, a backendAuthorization, opts map[string]string, backendOnly bool) error {
	if apNumber(record["schema_version"]) != 1 || record["kind"] != "backend-delivery-terminal" || record["business_completed"] != false || record["release_authorized"] != false || record["delivery_mode"] != "local-evidence" {
		return backendReject(s, "本地交付记录不能自报业务完成或发布批准")
	}
	if a.Mode != "feature-target" || record["checkpoint_ref"] != a.CheckpointRef || record["bundle_ref"] != nil || record["bundle_digest"] != nil {
		return backendReject(s, "本地交付证据必须绑定当前Spec功能且不得伪造交付包")
	}
	for _, key := range []string{"delivery", "review_state"} {
		if _, err := backendBound(s, record[key]); err != nil {
			return err
		}
	}
	delivery, err := backendInspectDeliveryMode(s, text(semMap(record["delivery"])["ref"]), opts, true, a.CheckpointRef)
	if err != nil {
		return err
	}
	if backendOnly {
		if err = backendTerminalSliceScope(s, delivery); err != nil {
			return err
		}
	}
	if err = backendCurrentCheckpointSlice(s, a.CheckpointRef, record, delivery); err != nil {
		return err
	}
	if !backendOnly {
		cp, err := s.doc(a.CheckpointRef)
		if err != nil {
			return err
		}
		if err = s.gateChecks(cp, a.CheckpointRef, "gate.slice-contract-approved"); err != nil {
			return err
		}
	}
	state, err := s.doc(text(semMap(record["review_state"])["ref"]))
	if err != nil {
		return err
	}
	input := semMap(state["review_input"])
	if input["scope_kind"] != "change" || input["slice_contract_ref"] != semMap(delivery["slice_contract"])["ref"] {
		return backendReject(s, "独立审查未绑定本地交付的批准Slice")
	}
	result, err := backendReview(s, state, opts)
	if err != nil {
		return err
	}
	if result["status"] != "passed" || input["review_mode"] != "committed" {
		return backendReject(s, "本地交付需要当前提交的独立后端审查")
	}
	root, err := backendProjectRoot(s, input)
	if err != nil {
		return err
	}
	buildSource, err := candidateBuildSource(s, root, input)
	if err != nil {
		return err
	}
	if semMap(delivery["build"])["source_commit"] != buildSource {
		return backendReject(s, "本地构建并非当前已审查源码提交")
	}
	for _, key := range []string{"owner", "ticket_ref", "verification_plan", "target_version"} {
		if strings.TrimSpace(text(semMap(record["downstream"])[key])) == "" {
			return backendReject(s, "本地交付缺少下游接收责任："+key)
		}
	}
	cp, err := s.doc(a.CheckpointRef)
	if err != nil {
		return err
	}
	s.report.Coverage = map[string]any{"result": "backend-delivered", "delivery_mode": "local-evidence", "checkpoint_ref": a.CheckpointRef, "terminal_ref": ref, "delivery_id": delivery["delivery_id"], "version": delivery["version"], "next_work_unit": cp["next_work_unit"], "business_completed": false, "release_authorized": false, "live_service_checked": false, "downstream": record["downstream"]}
	return nil
}

func backendBusinessLocalEvidence(s *semanticSession, cpRef string) error {
	a, err := progressionBackendAuthorization(s, cpRef, "", true)
	if err != nil {
		return err
	}
	record, err := s.doc(a.TerminalRef)
	if err != nil {
		return err
	}
	return backendVerifyLocalTerminalMode(s, a.TerminalRef, record, a, map[string]string{"checkpoint": cpRef}, false)
}

func backendTerminalSliceScope(s *semanticSession, delivery map[string]any) error {
	c, err := loadNativeSlice(s, text(semMap(delivery["slice_contract"])["ref"]))
	if err != nil {
		return err
	}
	if semMap(c.Normalized["backend"])["status"] != "required" || semMap(c.Normalized["frontend"])["status"] != "not-applicable" || semHas(semMap(c.Normalized["common"])["impacted_areas"], "ui") || semHas(semMap(c.Normalized["common"])["impacted_areas"], "frontend") {
		return backendReject(s, "后端终点不能扩大前端实现批准")
	}
	for _, unit := range semList(c.Normalized["work_units"]) {
		if semMap(unit)["role_id"] == "role.frontend-engineer" {
			return backendReject(s, "后端终点不能承接前端实施单元")
		}
	}
	return nil
}

func backendCurrentCheckpointSlice(s *semanticSession, cpRef string, record, delivery map[string]any) error {
	if cpRef == "" {
		return nil
	} // Historical explicit scope readers have no feature selector.
	cp, err := s.doc(cpRef)
	if err != nil {
		return err
	}
	if registered := text(record["checkpoint_ref"]); registered != "" && registered != cpRef {
		return backendReject(s, "终点记录绑定不同功能checkpoint")
	}
	current := first(text(semMap(semMap(cp["human_review"])["implementation"])["slice_contract_ref"]), text(semMap(cp["review_input"])["slice_contract_ref"]), checkpointAsset(cp, "slice_contract_ref", "artifact.slice-implementation-contract"), text(semMap(cp["slice_contract"])["ref"]), text(semMap(semMap(cp["gates"])["gate.slice-contract-approved"])["subject_ref"]))
	if current == "" || current != semMap(delivery["slice_contract"])["ref"] {
		return backendReject(s, "交付终点不是当前显式checkpoint的批准Slice")
	}
	return nil
}

func backendLocalSpecCoverage(s *semanticSession, cpRef string, c *nativeSlice, delivery, tests map[string]any) error {
	cp, err := s.doc(cpRef)
	if err != nil {
		return err
	}
	spec := c.Basis["spec"]
	if len(spec) == 0 || spec["ref"] != checkpointAsset(cp, "spec_ref", "artifact.spec") {
		return backendReject(s, "本地交付Slice没有绑定当前Spec")
	}
	if digest := semMap(semMap(cp["artifacts"])["artifact.spec"])["digest"]; digest != nil && digest != spec["digest"] {
		return backendReject(s, "本地交付当前Spec原字节绑定不一致")
	}
	specRaw, err := backendBound(s, spec)
	if err != nil {
		return err
	}
	var entries []contractPlanEntry
	supported := false
	if strings.HasPrefix(strings.TrimSpace(string(specRaw)), "---\n") {
		entries, supported, err = contractPlan(s, text(spec["ref"]))
		if err != nil {
			return err
		}
	}
	known := []string{}
	if supported {
		for _, entry := range entries {
			if entry.Kind == "FR" || entry.Kind == "AC" {
				known = append(known, entry.ID)
			}
		}
	} else {
		// Existing approved Spec text retains its published FR/AC identifiers.
		// This branch never approves it or fabricates an API/design contract.
		seen := map[string]bool{}
		for _, id := range regexp.MustCompile(`\b(?:FR|AC)-[A-Za-z0-9._-]+\b`).FindAllString(string(specRaw), -1) {
			if !seen[id] {
				seen[id] = true
				known = append(known, id)
			}
		}
		if len(known) == 0 {
			return backendReject(s, "既有批准Spec缺少稳定FR/AC ID")
		}
	}
	ids := semStrings(semMap(delivery["scope"])["source_ids"])
	for _, id := range ids {
		if !semHas(known, id) {
			return backendReject(s, "本地交付包含未知当前Spec来源ID")
		}
	}
	count := 0
	for id := range semMap(c.Raw["acceptance"]) {
		if !strings.HasPrefix(id, "AC-") || !semHas(known, id) || !semHas(ids, id) {
			return backendReject(s, "本地交付未覆盖当前Slice验收ID")
		}
		count++
		for _, outcome := range []string{"success", "failure"} {
			found := false
			for _, coverage := range semList(tests["coverage"]) {
				item := semMap(coverage)
				if item["source_id"] == id && item["outcome"] == outcome {
					found = true
				}
			}
			if !found {
				return backendReject(s, "本地契约验证缺少Slice验收成功/失败覆盖")
			}
		}
	}
	if count == 0 {
		return backendReject(s, "本地交付缺少当前Slice验收条件")
	}
	for _, id := range semStrings(semMap(delivery["scope"])["operation_ids"]) {
		if !semHas(tests["operation_ids"], id) {
			return backendReject(s, "本地契约验证未覆盖批准接口")
		}
	}
	return nil
}
