package contracts

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"strings"
)

func init() {
	kernel.RegisterRoleIndependentValidator("slice-draft", verifySliceDraft)
	kernel.RegisterRoleIndependentValidator("slice", verifySlice4)
	kernel.RegisterRoleIndependentValidator("slice-preflight", verifySlice4Preflight)
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

func verifySlice4(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	if err := verifySliceDraft(s, ref, opts); err != nil {
		return err
	}
	return verifySliceSemantic(s, ref, opts)
}
func verifySlice4Preflight(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	if err := verifySliceDraft(s, ref, opts); err != nil {
		return err
	}
	return verifySlicePreflightSemantic(s, ref, opts)
}

// The contract gate consumes the existing checkpoint and actual user scope decision;
// technical review is delegated to the current approval consumer, never legacy role rules.
func slice4Approval(s *kernel.SemanticSession, c *NativeSlice, opts map[string]string) error {
	if s.StageGatesEnabled() {
		return VerifyStageAdmission(s, kernel.First(opts["checkpoint"], opts["approval-ref"], opts["approval_ref"]), c.Ref)
	}
	ref := kernel.First(opts["approval-ref"], opts["approval_ref"])
	if ref == "" || c.Raw["status"] != "approved" {
		return s.Reject("SLICE_APPROVAL_REQUIRED", "需要当前已批准Slice及checkpoint")
	}
	cp, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/lifecycle-checkpoint.schema.json", cp); err != nil {
		return err
	}
	gate := kernel.ApMap(kernel.ApMap(cp["gates"])["gate.slice-contract-approved"])
	binding, err := s.Bind(c.Ref)
	if err != nil {
		return err
	}
	if cp["repository_mode"] != "project-instance" || cp["status"] == "blocked" || !kernel.ApIsArray(cp["blockers"]) || len(kernel.ApArray(cp["blockers"])) > 0 || gate["status"] != "approved" || gate["subject_ref"] != c.Ref || strings.TrimPrefix(kernel.Text(gate["subject_digest"]), "sha256:") != strings.TrimPrefix(binding.Digest, "sha256:") {
		return s.Reject("SLICE_APPROVAL_REQUIRED", "checkpoint未绑定当前Slice字节或仍有阻断")
	}
	review := kernel.ApMap(kernel.ApMap(cp["checks"])["check.design-reviewed"])
	if review["status"] != "passed" && review["status"] != "approved" {
		return s.Reject("DESIGN_REVIEW_REQUIRED", "缺少当前设计技术复核")
	}
	refs := kernel.ApStrings(review["evidence_refs"])
	if len(refs) != 1 {
		return s.Reject("DESIGN_REVIEW_REQUIRED", "设计技术复核必须有明确当前记录")
	}
	if err = s.Verify("approval", refs[0], map[string]string{"checkpoint": ref, "gate": "check.design-reviewed", "require-approved": "true"}); err != nil {
		return err
	}
	if kernel.RoleIndependentValidators["user-decision"] == nil {
		return s.Unavailable("CAPABILITY", "尚缺原生语义校验能力: user-decision")
	}
	implementation := ContractCopy(kernel.ApMap(kernel.ApMap(cp["human_review"])["implementation"]))
	if implementation["slice_contract_ref"] != c.Ref {
		return s.Reject("SLICE_APPROVAL_REQUIRED", "实施决定未绑定当前Slice")
	}
	implementation["user_decisions"] = kernel.ApMap(cp["human_review"])["user_decisions"]
	return AssertImplementationDecisionSemantic(s, implementation)
}
