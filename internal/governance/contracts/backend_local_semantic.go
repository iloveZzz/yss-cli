package contracts

import "github.com/iloveZzz/yss-cli/internal/governance/lifecycle"

import "github.com/iloveZzz/yss-cli/internal/governance/approval"

import (
	"github.com/iloveZzz/yss-cli/internal/governance/handoff"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"regexp"
	"strings"
)

// Local Spec milestones consume the same approved Slice, independent review
// and actual contract/deployment records without inventing a strategic package.
func backendVerifyLocalTerminal(s *kernel.SemanticSession, ref string, record map[string]any, a lifecycle.BackendAuthorization, opts map[string]string) error {
	fullFeature, err := backendLocalFullFeature(s, a)
	if err != nil {
		return err
	}
	return backendVerifyLocalTerminalMode(s, ref, record, a, opts, !fullFeature)
}

// A full Spec feature may contain approved frontend work while its backend
// evidence is consumed. An explicit external owner cannot be replaced locally;
// permanent execution scopes and dedicated profiles retain their responsibility.
func backendLocalFullFeature(s *kernel.SemanticSession, a lifecycle.BackendAuthorization) (bool, error) {
	if a.Mode != "feature-target" {
		return false, nil
	}
	local, err := dependencies.HasLocalImplementationInputs(s)
	if err != nil || !local {
		return false, err
	}
	configRef, cp, err := kernel.ProgressionLocation(s, a.CheckpointRef)
	if err != nil {
		return false, err
	}
	present, err := s.Exists(configRef)
	if err != nil {
		return false, err
	}
	if !present {
		return true, nil
	}
	raw, err := s.Bytes(configRef)
	if err != nil {
		return false, err
	}
	intent, err := dependencies.ParseProgressionTarget(raw)
	if err != nil {
		return false, err
	}
	if intent.FeatureID != kernel.Text(cp["feature_id"]) || intent.CheckpointRef != a.CheckpointRef {
		return false, s.Reject("PROGRESSION_BINDING", "本地后端证据目标未绑定当前功能")
	}
	for _, consumer := range intent.Consumers {
		if consumer.Profile == "backend" && consumer.Root != s.Root {
			return false, nil
		}
	}
	return true, nil
}

// Both full Spec milestones and business implementation consume this existing
// evidence closure. The narrower scope mode remains explicit at its callers.
func backendVerifyLocalTerminalMode(s *kernel.SemanticSession, ref string, record map[string]any, a lifecycle.BackendAuthorization, opts map[string]string, backendOnly bool) error {
	if kernel.ApNumber(record["schema_version"]) != 1 || record["kind"] != "backend-delivery-terminal" || record["business_completed"] != false || record["release_authorized"] != false || record["delivery_mode"] != "local-evidence" {
		return BackendReject(s, "本地交付记录不能自报业务完成或发布批准")
	}
	localProfile := false
	if a.Mode == "backend-profile" {
		profile, policy, err := kernel.ProgressionPolicy(s)
		if err != nil {
			return err
		}
		localProfile = profile == "backend" && policy["local_implementation_inputs"] == "native-profile-current-feature-approved-assets"
	}
	if (a.Mode != "feature-target" && !localProfile) || record["checkpoint_ref"] != a.CheckpointRef || record["bundle_ref"] != nil || record["bundle_digest"] != nil {
		return BackendReject(s, "本地交付证据必须绑定当前Spec功能且不得伪造交付包")
	}
	for _, key := range []string{"delivery", "review_state"} {
		if _, err := BackendBound(s, record[key]); err != nil {
			return err
		}
	}
	delivery, err := backendInspectDeliveryMode(s, kernel.Text(kernel.SemMap(record["delivery"])["ref"]), opts, true, a.CheckpointRef)
	if err != nil {
		return err
	}
	if backendOnly {
		if err = backendTerminalSliceScope(s, delivery); err != nil {
			return err
		}
	}
	if localProfile {
		if err = backendProfileFresh(s, a.CheckpointRef, delivery); err != nil {
			return err
		}
	}
	if err = backendCurrentCheckpointSlice(s, a.CheckpointRef, record, delivery); err != nil {
		return err
	}
	if !backendOnly {
		cp, err := s.Doc(a.CheckpointRef)
		if err != nil {
			return err
		}
		if err = approval.GateChecks(s, cp, a.CheckpointRef, "gate.slice-contract-approved"); err != nil {
			return err
		}
	}
	state, err := s.Doc(kernel.Text(kernel.SemMap(record["review_state"])["ref"]))
	if err != nil {
		return err
	}
	input := kernel.SemMap(state["review_input"])
	if input["scope_kind"] != "change" || input["slice_contract_ref"] != kernel.SemMap(delivery["slice_contract"])["ref"] {
		return BackendReject(s, "独立审查未绑定本地交付的批准Slice")
	}
	result, err := backendReview(s, state, opts)
	if err != nil {
		return err
	}
	if result["status"] != "passed" || input["review_mode"] != "committed" {
		return BackendReject(s, "本地交付需要当前提交的独立后端审查")
	}
	root, err := backendProjectRoot(s, input)
	if err != nil {
		return err
	}
	buildSource, err := CandidateBuildSource(s, root, input)
	if err != nil {
		return err
	}
	if kernel.SemMap(delivery["build"])["source_commit"] != buildSource {
		return BackendReject(s, "本地构建并非当前已审查源码提交")
	}
	for _, key := range []string{"owner", "ticket_ref", "verification_plan", "target_version"} {
		if strings.TrimSpace(kernel.Text(kernel.SemMap(record["downstream"])[key])) == "" {
			return BackendReject(s, "本地交付缺少下游接收责任："+key)
		}
	}
	cp, err := s.Doc(a.CheckpointRef)
	if err != nil {
		return err
	}
	s.Report.Coverage = map[string]any{"result": "backend-delivered", "delivery_mode": "local-evidence", "checkpoint_ref": a.CheckpointRef, "terminal_ref": ref, "delivery_id": delivery["delivery_id"], "version": delivery["version"], "next_work_unit": cp["next_work_unit"], "business_completed": false, "release_authorized": false, "live_service_checked": false, "downstream": record["downstream"]}
	return nil
}

func BackendBusinessLocalEvidence(s *kernel.SemanticSession, cpRef string) error {
	a, err := dependencies.ProgressionBackendAuthorization(s, cpRef, "", true)
	if err != nil {
		return err
	}
	record, err := s.Doc(a.TerminalRef)
	if err != nil {
		return err
	}
	return backendVerifyLocalTerminalMode(s, a.TerminalRef, record, a, map[string]string{"checkpoint": cpRef}, false)
}

func backendTerminalSliceScope(s *kernel.SemanticSession, delivery map[string]any) error {
	c, err := LoadNativeSlice(s, kernel.Text(kernel.SemMap(delivery["slice_contract"])["ref"]))
	if err != nil {
		return err
	}
	if kernel.SemMap(c.Normalized["backend"])["status"] != "required" || kernel.SemMap(c.Normalized["frontend"])["status"] != "not-applicable" || kernel.SemHas(kernel.SemMap(c.Normalized["common"])["impacted_areas"], "ui") || kernel.SemHas(kernel.SemMap(c.Normalized["common"])["impacted_areas"], "frontend") {
		return BackendReject(s, "后端终点不能扩大前端实现批准")
	}
	for _, unit := range kernel.SemList(c.Normalized["work_units"]) {
		if kernel.SemMap(unit)["role_id"] == "role.frontend-engineer" {
			return BackendReject(s, "后端终点不能承接前端实施单元")
		}
	}
	return nil
}

func backendCurrentCheckpointSlice(s *kernel.SemanticSession, cpRef string, record, delivery map[string]any) error {
	if cpRef == "" {
		return nil
	} // Historical explicit scope readers have no feature selector.
	cp, err := s.Doc(cpRef)
	if err != nil {
		return err
	}
	if registered := kernel.Text(record["checkpoint_ref"]); registered != "" && registered != cpRef {
		return BackendReject(s, "终点记录绑定不同功能checkpoint")
	}
	current := kernel.First(kernel.Text(kernel.SemMap(kernel.SemMap(cp["human_review"])["implementation"])["slice_contract_ref"]), kernel.Text(kernel.SemMap(cp["review_input"])["slice_contract_ref"]), handoff.CheckpointAsset(cp, "slice_contract_ref", "artifact.slice-implementation-contract"), kernel.Text(kernel.SemMap(cp["slice_contract"])["ref"]), kernel.Text(kernel.SemMap(kernel.SemMap(cp["gates"])["gate.slice-contract-approved"])["subject_ref"]))
	if current == "" || current != kernel.SemMap(delivery["slice_contract"])["ref"] {
		return BackendReject(s, "交付终点不是当前显式checkpoint的批准Slice")
	}
	return nil
}

func backendLocalSpecCoverage(s *kernel.SemanticSession, cpRef string, c *NativeSlice, delivery, tests map[string]any) error {
	cp, err := s.Doc(cpRef)
	if err != nil {
		return err
	}
	spec := c.Basis["spec"]
	if len(spec) == 0 || spec["ref"] != handoff.CheckpointAsset(cp, "spec_ref", "artifact.spec") {
		return BackendReject(s, "本地交付Slice没有绑定当前Spec")
	}
	if digest := kernel.SemMap(kernel.SemMap(cp["artifacts"])["artifact.spec"])["digest"]; digest != nil && digest != spec["digest"] {
		return BackendReject(s, "本地交付当前Spec原字节绑定不一致")
	}
	specRaw, err := BackendBound(s, spec)
	if err != nil {
		return err
	}
	var entries []ContractPlanEntry
	supported := false
	if strings.HasPrefix(strings.TrimSpace(string(specRaw)), "---\n") {
		entries, supported, err = ContractPlan(s, kernel.Text(spec["ref"]))
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
			return BackendReject(s, "既有批准Spec缺少稳定FR/AC ID")
		}
	}
	ids := kernel.SemStrings(kernel.SemMap(delivery["scope"])["source_ids"])
	for _, id := range ids {
		if !kernel.SemHas(known, id) {
			return BackendReject(s, "本地交付包含未知当前Spec来源ID")
		}
	}
	count := 0
	for id := range kernel.SemMap(c.Raw["acceptance"]) {
		if !strings.HasPrefix(id, "AC-") || !kernel.SemHas(known, id) || !kernel.SemHas(ids, id) {
			return BackendReject(s, "本地交付未覆盖当前Slice验收ID")
		}
		count++
		for _, outcome := range []string{"success", "failure"} {
			found := false
			for _, coverage := range kernel.SemList(tests["coverage"]) {
				item := kernel.SemMap(coverage)
				if item["source_id"] == id && item["outcome"] == outcome {
					found = true
				}
			}
			if !found {
				return BackendReject(s, "本地契约验证缺少Slice验收成功/失败覆盖")
			}
		}
	}
	if count == 0 {
		return BackendReject(s, "本地交付缺少当前Slice验收条件")
	}
	for _, id := range kernel.SemStrings(kernel.SemMap(delivery["scope"])["operation_ids"]) {
		if !kernel.SemHas(tests["operation_ids"], id) {
			return BackendReject(s, "本地契约验证未覆盖批准接口")
		}
	}
	return nil
}
