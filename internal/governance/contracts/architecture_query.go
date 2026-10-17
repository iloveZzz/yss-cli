package contracts

import (
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"path/filepath"
)

func init() {
	kernel.RegisterRoleIndependentValidator("architecture-evidence", verifyArchitectureEvidenceQuery)
}

// The preparation compiler consumes the same original engineering-source validator as execution.
func verifyArchitectureEvidenceQuery(s *kernel.SemanticSession, ref string, opts map[string]string) error {
	input, err := s.Doc(ref)
	if err != nil {
		return err
	}
	identity := kernel.ApMap(input["architecture_identity"])
	if kernel.ApNumber(identity["schema_version"]) != 2 || identity["source_kind"] != "existing-registration" {
		return s.Reject("ARCH_SOURCE_UNSUPPORTED", "此查询仅接受已登记既有工程的原始证据")
	}
	evidence := kernel.ApMap(input["architecture_evidence"])
	basis := map[string]map[string]any{}
	for _, key := range []string{"engineering_baseline", "repository_registration", "manifest"} {
		basis[key] = kernel.ApMap(evidence[key])
	}
	registry, err := s.Doc(".template-spec/agents/yss-skill-registry.yaml")
	if err != nil {
		return err
	}
	c := &NativeSlice{Basis: basis, Normalized: map[string]any{"common": map[string]any{}}}
	if sliceRef := kernel.Text(input["slice_ref"]); sliceRef != "" {
		c, err = LoadNativeSlice(s, sliceRef)
		if err != nil {
			return err
		}
		for key, bound := range basis {
			if !kernel.ApEqual(bound, c.Basis[key]) {
				return s.Reject("ARCH_EVIDENCE_CONFLICT", "准备输入不属于当前Slice")
			}
		}
		if err = ContractSliceFresh(s, c, map[string]string{"approval-ref": kernel.Text(input["approval_ref"])}); err != nil {
			return err
		}
	}
	if err = contractArchitecture(s, identity, c, registry, opts); err != nil {
		return err
	}
	registration, err := ContractBoundDoc(s, basis["repository_registration"])
	if err != nil {
		return err
	}
	root := filepath.Join(kernel.Text(registration["local_worktree"]), kernel.Text(registration["project_root"]))
	kernel.SemanticCoverage(s, "architecture_evidence", map[string]any{"source_kind": "existing-registration", "project_root": root, "source_digest": identity["source_digest"], "build_units_digest": identity["build_units_digest"], "bindings": evidence, "read_only": true, "execution_authorization": "not-granted"})
	return nil
}
