package handoff

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"path"
	"strings"
)

// The frozen source session selects this consumer. The receiving Backend's
// own lifecycle remains unchanged.
func contractStageSourceApproval(s *kernel.SemanticSession, gate string, binding map[string]any, ref string) error {
	if s.StageGate(gate) == nil {
		return s.Reject("HANDOFF_STAGE", "来源未登记该四阶段出口")
	}
	assetRef := kernel.Text(binding["ref"])
	if err := s.Verify("stage-result", ref, map[string]string{"gate": gate, "asset-ref": assetRef}); err != nil {
		return err
	}
	raw, err := s.Bytes(assetRef)
	if err != nil {
		return err
	}
	actual := "sha256:" + safefs.Digest(raw)
	if binding["digest_kind"] == "canonical-json" {
		doc, e := s.Doc(assetRef)
		if e != nil {
			return e
		}
		actual = dependencies.ContractDigest(doc)
	} else if kind := kernel.Text(binding["digest_kind"]); kind != "" && kind != "sha256-bytes" {
		return s.Reject("HANDOFF_STAGE", "未知阶段成员摘要算法")
	}
	if binding["digest"] != nil && strings.TrimPrefix(kernel.Text(binding["digest"]), "sha256:") != strings.TrimPrefix(actual, "sha256:") {
		return s.Reject("HANDOFF_SOURCE_STALE", "阶段来源摘要漂移")
	}
	if binding["id"] != nil || binding["version"] != nil {
		record, e := s.Doc(ref)
		if e != nil {
			return e
		}
		found := false
		for _, member := range kernel.ApRows(record["assets"]) {
			av, ar, ae := s.ReferenceView(assetRef)
			mv, mr, me := s.ReferenceView(kernel.Text(member["ref"]))
			if ae != nil || me != nil || av != mv || ar != mr {
				continue
			}
			id, version := member["id"], member["version"]
			{
				if doc, e := s.Doc(assetRef); e == nil {
					actualID := kernel.First(kernel.Text(doc["domain_strategy_id"]), kernel.Text(doc["stage_decision_id"]), kernel.Text(doc["spec_id"]), kernel.Text(doc["id"]))
					if actualID != "" {
						id = actualID
					}
					actualVersion := kernel.First(kernel.Text(doc["domain_version"]), kernel.Text(doc["package_version"]), kernel.Text(doc["version"]))
					if actualVersion != "" {
						version = actualVersion
					}
				}
			}
			found = (binding["id"] == nil || binding["id"] == id) && (binding["version"] == nil || dependencies.ContractSame(binding["version"], version))
		}
		if !found {
			return s.Reject("HANDOFF_SOURCE_IDENTITY", "阶段成员身份或版本与当前来源不符")
		}
	}
	return nil
}

func contractStageHandoffApprovals(s *kernel.SemanticSession, ref string, b *NativeHandoff) error {
	if err := contractHandoffSourcePolicy(s, kernel.ContractN(b.Handoff["schema_version"])); err != nil {
		return err
	}
	sources := kernel.ApMap(b.Handoff["source"])
	approvals := kernel.ApMap(b.Config["approvals"])
	for key, value := range sources {
		source := kernel.ApMap(value)
		assetRef := kernel.Text(source["persisted_ref"])
		if key == "visual_baseline_ref" || key == "existing_ui_baseline_ref" {
			assetRef = path.Join(assetRef, kernel.Text(source["manifest_ref"]))
		}
		gate := "gate.spec-baseline-approved"
		if key == "domain_strategy_ref" || key == "stage_decision_package_ref" {
			gate = "gate.plan-approved"
		}
		descriptor := kernel.ApMap(approvals[key])
		if descriptor["gate_id"] != gate {
			return s.Reject("HANDOFF_STAGE", "来源应复用所属阶段记录: "+key)
		}
		bound := map[string]any{"ref": assetRef, "digest": source["digest"], "digest_kind": descriptor["digest_kind"]}
		if err := contractStageSourceApproval(s, gate, bound, kernel.Text(descriptor["record_ref"])); err != nil {
			return err
		}
	}
	// Handoff is a deterministic transport of those members. Its compatibility
	// descriptor aliases the Spec result, never a fifth confirmation.
	transport := kernel.ApMap(approvals["handoff"])
	spec := kernel.ApMap(approvals["spec_ref"])
	if transport["gate_id"] != "gate.spec-baseline-approved" || transport["record_ref"] != spec["record_ref"] {
		return s.Reject("HANDOFF_STAGE", "交接应复用 Spec 阶段记录")
	}
	set := kernel.ApMap(sources["business_ticket_set_ref"])
	business, report, err := dependencies.ContractCollectBusinessTickets(s, kernel.Text(set["persisted_ref"]), "formal", "")
	if err != nil {
		return err
	}
	specSource := kernel.ApMap(sources["spec_ref"])
	if business["id"] != set["id"] || business["version"] != set["version"] || kernel.ApMap(business["spec"])["ref"] != specSource["persisted_ref"] || kernel.ApMap(business["spec"])["version"] != specSource["version"] {
		return s.Reject("HANDOFF_BUSINESS", "业务集合与当前 Spec 冲突")
	}
	b.Business, b.BusinessReport = business, report
	return nil
}
