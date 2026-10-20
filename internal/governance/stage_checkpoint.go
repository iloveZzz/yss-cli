package governance

import (
	"github.com/iloveZzz/yss-cli/internal/governance/approval"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func verifyStageCheckpoint(s *kernel.SemanticSession, ref string, cp map[string]any, opts map[string]string) error {
	if err := s.ValidateSchema(".template-spec/process/schemas/lifecycle-checkpoint-v2.schema.json", cp); err != nil {
		return err
	}
	if err := s.CurrentAsset(ref); err != nil {
		return err
	}
	if cp["repository_mode"] != "project-instance" {
		return s.Reject("IDENTITY", "阶段 checkpoint 必须属于项目实例")
	}
	if kernel.SemanticDefinition(s.Registry, "stages", kernel.Text(cp["stage"])) == nil {
		return s.Reject("STAGE_CHECKPOINT", "阶段未登记")
	}
	if cp["next_work_unit"] != nil && kernel.SemanticDefinition(s.Registry, "work_units", kernel.Text(cp["next_work_unit"])) == nil {
		return s.Reject("STAGE_CHECKPOINT", "下一工作单元未登记")
	}
	for gate, value := range kernel.ApMap(cp["gates"]) {
		binding := kernel.ApMap(value)
		record, err := s.Doc(kernel.Text(binding["record_ref"]))
		if err != nil {
			return err
		}
		if record["outcome"] == "approved" {
			if err = approval.VerifyCheckpointStage(s, ref, gate, ""); err != nil {
				return err
			}
		} else {
			// Pending records remain visible without conferring stage authority.
			if err = s.ValidateSchema(approval.StageResultSchema, record); err != nil {
				return err
			}
			if record["gate_id"] != gate || record["feature_id"] != cp["feature_id"] {
				return s.Reject("STAGE_CHECKPOINT", "待定记录不属于当前功能或阶段")
			}
			if err = s.Basis([]any{map[string]any{"ref": binding["record_ref"], "digest": binding["record_digest"]}}); err != nil {
				return err
			}
		}
	}
	// Preparing designs and compiling a Slice precedes admission. Only entering
	// business implementation requires that current admission already exists.
	stage := cp["stage"]
	if next := kernel.SemanticDefinition(s.Registry, "work_units", kernel.Text(cp["next_work_unit"])); next != nil && next["stage"] != nil {
		stage = next["stage"]
	}
	required := ""
	switch stage {
	case "stage.spec-architecture", "stage.product-design":
		required = "gate.plan-approved"
	case "stage.system-data-engineering", "stage.ticket-formalization":
		required = "gate.spec-baseline-approved"
	case "stage.vertical-slice-implementation", "stage.verification-release-retrospective":
		required = "gate.implementation-admitted"
	}
	if cp["next_work_unit"] == "work-unit.slice-implementation" {
		required = "gate.implementation-admitted"
	}
	if required != "" {
		if err := approval.VerifyCheckpointStage(s, ref, required, ""); err != nil {
			return err
		}
	}
	return nil
}
