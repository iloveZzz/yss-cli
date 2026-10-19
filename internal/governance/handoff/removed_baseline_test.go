package handoff

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"os"
	"path/filepath"
	"testing"
)

func TestRemovedSpecBaselineApprovalContextRejected(t *testing.T) {
	s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
	s.GovernanceVersion = 1
	if err := sourceApprovalContextVersion(s, map[string]any{"source_baseline": map[string]any{}}); err == nil {
		t.Fatal("inherited approval context accepted")
	}
}

func TestRemovedSpecBaselineBusinessSourceRejected(t *testing.T) {
	s := kernel.NewSemanticSession(context.Background(), t.TempDir(), nil)
	if _, _, _, err := ContractBusinessSource(s, "docs/spec-baselines/old/v1/working-files/business.json", ""); err == nil {
		t.Fatal("removed source accepted")
	}
}

func TestSaveBaselineJSONProtectsExistingFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "plan.json")
	if err := SaveBaselineJSON(file, map[string]any{"value": "first"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveBaselineJSON(file, map[string]any{"value": "second"}); err == nil {
		t.Fatal("overwrote existing plan")
	}
	after, err := os.ReadFile(file)
	if err != nil || string(after) != string(before) {
		t.Fatal("existing plan changed", err)
	}
}

func TestRemovedDesignHandoffSourceRejected(t *testing.T) {
	root := t.TempDir()
	ref := filepath.Join(root, ".template-spec/process/harness-profile.yaml")
	if err := os.MkdirAll(filepath.Dir(ref), 0755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"schema_version":2,"profile_id":"harness.business-ddd-strategy-handoff"}`)
	if err := os.WriteFile(ref, raw, 0600); err != nil {
		t.Fatal(err)
	}
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	s.GovernanceVersion = 1
	err := contractHandoffSourcePolicy(s, 5)
	if err == nil || kernel.SemanticCode(err) != "PROFILE_REMOVED" {
		t.Fatal("legacy Design source accepted or reinterpreted", err)
	}
	after, err := os.ReadFile(ref)
	if err != nil || string(after) != string(raw) {
		t.Fatal("source changed", err)
	}
}
