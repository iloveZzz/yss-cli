package contracts

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"strings"
)

func init() {
	kernel.RegisterRoleIndependentValidator("slice-draft", verifySliceDraft)
	kernel.RegisterRoleIndependentValidator("task-selection", verifyTaskSelection)
	kernel.RegisterSemanticValidator("governance-write", governanceWrite)
	kernel.RegisterRoleIndependentValidator("governance-write", governanceWrite)
}
func governanceWrite(s *kernel.SemanticSession, _ string, _ map[string]string) error {
	return s.RequireGovernanceWrite()
}

func validateSlice4Selection(s *kernel.SemanticSession, c *NativeSlice) error {
	if kernel.ContractN(c.Raw["schema_version"]) != 4 || s.GovernanceVersion != 2 {
		return s.Reject("CONTRACT_SCHEMA", "新政策只消费Slice4；历史版本须使用原政策只读读取")
	}
	impacts := kernel.ApStrings(kernel.ApMap(c.Raw["scope"])["impacted_areas"])
	selection, err := kernel.CompileTaskSelection(s, "work-unit.slice-implementation", impacts)
	if err != nil {
		return err
	}
	for _, unit := range kernel.ApRows(c.Normalized["work_units"]) {
		if !kernel.ApSetEqual(unit["check_ids"], selection["check_ids"]) {
			return s.Reject("SLICE_CHECKS", "Slice工作单元检查未按当前Profile和影响闭合")
		}
	}
	return nil
}
func verifySliceDraft(s *kernel.SemanticSession, ref string, _ map[string]string) error {
	c, err := LoadNativeSlice(s, ref)
	if err != nil {
		return err
	}
	if err = validateSlice4Selection(s, c); err != nil {
		return err
	}
	for _, binding := range []struct{ ref, key string }{{".template-spec/agents/yss-skill-registry.yaml", "registry_digest"}, {".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml", "compiler_contract_digest"}} {
		doc, err := s.Doc(binding.ref)
		if err != nil {
			return err
		}
		if kernel.ApMap(c.Raw["resolution"])[binding.key] != strings.TrimPrefix(ContractDigest(doc), "sha256:") {
			return s.Reject("CONTRACT_STALE", "编译依据已变化")
		}
	}
	if s.Report.Coverage == nil {
		s.Report.Coverage = map[string]any{}
	}
	s.Report.Coverage["slice_draft"] = map[string]any{"contract": c.Normalized, "read_only": true, "execution_allowed": false, "approval_created": false}
	return nil
}

// Selection describes requirements only; impacts here are a query, not verified task evidence.
func verifyTaskSelection(s *kernel.SemanticSession, ref string, _ map[string]string) error {
	doc, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if len(doc) != 2 || !kernel.ApUnique(doc["impacts"]) {
		return s.Reject("TASK_SELECTION", "选择输入必须为work_unit_id和明确impacts")
	}
	selection, err := kernel.CompileTaskSelection(s, kernel.Text(doc["work_unit_id"]), kernel.ApStrings(doc["impacts"]))
	if err != nil {
		return err
	}
	if s.Report.Coverage == nil {
		s.Report.Coverage = map[string]any{}
	}
	s.Report.Coverage["task_selection"] = selection
	s.Report.ExecutionAuthorization = "not-evaluated"
	return nil
}
