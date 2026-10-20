package handoff

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"testing"
)

func TestStageResultSourceApprovalUsesApprovedMembership(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "asset.md"), []byte("current"), 0644)
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	s.GovernanceVersion = 2
	s.Report.Profile = "spec"
	s.GovernancePolicy = map[string]any{"stage_gates": map[string]any{"capability": kernel.StageGatesCapability, "gates": []any{map[string]any{"id": "gate.plan-approved"}}}}
	prior := kernel.RoleIndependentValidators["stage-result"]
	defer func() { kernel.RoleIndependentValidators["stage-result"] = prior }()
	called := false
	kernel.RoleIndependentValidators["stage-result"] = func(s *kernel.SemanticSession, ref string, opts map[string]string) error {
		called = true
		if ref != "stage.json" || opts["gate"] != "gate.plan-approved" || opts["asset-ref"] != "asset.md" {
			t.Fatalf("incorrect source membership: %v", opts)
		}
		return nil
	}
	binding := map[string]any{"ref": "asset.md", "digest": "sha256:" + safefs.Digest([]byte("current")), "digest_kind": "sha256-bytes"}
	if err := contractSourceApproval(s, "gate.plan-approved", binding, "stage.json"); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("source stage consumer not used")
	}
	binding["digest"] = "sha256:" + safefs.Digest([]byte("stale"))
	if err := contractSourceApproval(s, "gate.plan-approved", binding, "stage.json"); err == nil {
		t.Fatal("stale source accepted")
	}
}
