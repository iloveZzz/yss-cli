package kernel

import (
	"context"
	"testing"
)

func TestLegacyMultipartContainerIsNotNewGovernance(t *testing.T) {
	root := t.TempDir()
	writePolicyFixture(t, root, ".template-spec/process/orchestration-contract.yaml", map[string]any{"schema_version": 2, "parts": []string{"orchestration/old.yaml"}})
	writePolicyFixture(t, root, ".template-spec/process/orchestration/old.yaml", map[string]any{"request_triage": map[string]any{"policy": "original"}})
	s := NewSemanticSession(context.Background(), root, nil)
	s.Report.Profile = "design"
	if err := s.loadGovernancePolicy(map[string]any{"schema_version": 1, "repository_mode": "project-instance"}, map[string]any{"schema_version": 2}); err == nil {
		t.Fatal("removed Design multipart policy was accepted")
	}
	writePolicyFixture(t, root, ".template-spec/process/orchestration-contract.yaml", map[string]any{"schema_version": 2})
	s = NewSemanticSession(context.Background(), root, nil)
	if err := s.loadGovernancePolicy(map[string]any{"schema_version": 1, "repository_mode": "project-instance"}, map[string]any{"schema_version": 2}); err == nil {
		t.Fatal("unknown flat policy2 accepted as legacy")
	}
}
