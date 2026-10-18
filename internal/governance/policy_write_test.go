package governance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageLegacyWriteRefusesBeforeReadingWriteInputs(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "yss-project.yaml"), []byte("schema_version: 1\nrepository_mode: project-instance\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"register", "update"} {
		_, err := buildStagePlan(root, action, "missing-checkpoint.yaml", "missing-input.yaml", false, context.Background())
		if errCode(err) != "GOVERNANCE_MIGRATION_REQUIRED" || !strings.Contains(err.Error(), "迁移") {
			t.Fatalf("%s: %v", action, err)
		}
	}
}

func TestStageApplyRechecksLegacyPolicyBeforeMutation(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "yss-project.yaml"), []byte("schema_version: 1\nrepository_mode: project-instance\n"), 0644); err != nil {
		t.Fatal(err)
	}
	plan := WritePlan{SchemaVersion: 1, ProtocolVersion: 1, Kind: "stage-tracking-go-plan", Root: root, Action: "register", CheckpointRef: "checkpoint.yaml", InputRef: "input.yaml"}
	plan.PlanDigest, err = nativePlanDigest(plan)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "saved-plan.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	_, err = stageRun(context.Background(), "apply", root, map[string]string{"plan-file": "saved-plan.json"})
	if errCode(err) != "GOVERNANCE_MIGRATION_REQUIRED" {
		t.Fatalf("apply: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("rejected plan wrote files: %v", entries)
	}
}
