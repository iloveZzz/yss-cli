package governance

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"strings"
)

func init() {
	kernel.RegisterSemanticValidator("context-reconciliation", verifySemanticReconciliation)
	kernel.RegisterRoleIndependentValidator("context-reconciliation", verifySemanticReconciliation)
}

func verifySemanticReconciliation(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	v, err := s.Doc(ref)
	if err != nil {
		return err
	}
	if err = s.ValidateSchema(".template-spec/process/schemas/context-reconciliation.schema.json", v); err != nil {
		return err
	}
	identity, err := s.Doc("yss-project.yaml")
	if err != nil {
		return err
	}
	if v["repository_mode"] != identity["repository_mode"] {
		return s.Reject("CONTEXT", "对账仓库身份不一致")
	}
	contract, err := s.ContextSnapshot(kernel.SemMap(v["context_snapshot"]))
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, t := range contract["business_terms"].([]kernel.Term) {
		known[t.TermRef] = true
	}
	changed := map[string]bool{}
	for _, k := range []string{"added", "updated", "deprecated"} {
		for _, id := range kernel.SemStrings(kernel.SemMap(v["changes"])[k]) {
			if !known[id] || changed[id] {
				return s.Reject("CONTEXT_REFERENCE", "变更术语缺失或跨分类重复")
			}
			changed[id] = true
		}
	}
	status := kernel.Text(v["status"])
	unresolved := kernel.SemList(v["unresolved_terms"])
	if kernel.Text(v["repository_mode"]) == "project-instance" && status != "reconciled" || status == "reconciled" && len(unresolved) > 0 || status == "blocked" && len(unresolved) == 0 || status == "not-applicable" && (kernel.Text(v["repository_mode"]) != "template-source" || strings.TrimSpace(kernel.Text(v["reason"])) == "") {
		return s.Reject("CONTEXT", "Context 对账状态不允许批准或流转")
	}
	for _, r := range kernel.SemStrings(v["evidence_refs"]) {
		if strings.HasPrefix(r, "https://") || strings.HasPrefix(r, "http://") {
			continue
		}
		if _, err = s.Bytes(r); err != nil {
			return err
		}
	}
	s.Report.Applicability = append(s.Report.Applicability, map[string]any{"id": "context-reconciliation", "status": status, "reason": v["reason"], "source_ref": ref})
	return nil
}
