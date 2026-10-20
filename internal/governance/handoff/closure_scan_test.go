package handoff

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
)

func TestHandoffClosureIncludesPreviouslyScannedRegularFile(t *testing.T) {
	root := t.TempDir()
	for ref, content := range map[string]string{"source.json": "{}", "docs/proof.json": "{}", "CONTEXT.md": "# Context", ".template-spec/agents/digital-human-roles.yaml": "{}"} {
		file := filepath.Join(root, ref)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	s := kernel.NewSemanticSession(ctx, root, nil)
	if _, err := s.Scan("source.json"); err != nil {
		t.Fatal(err)
	}
	refs, err := contractHandoffClosure(s, "source.json", &NativeHandoff{Handoff: map[string]any{}, Config: map[string]any{"additional_files": []any{"docs"}}})
	if err != nil || len(refs) != 4 {
		t.Fatalf("regular source must be included once: refs=%v err=%v", refs, err)
	}
}

func TestStageHandoffClosureFreezesTaskCheckpoint(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{"source.json": `{"schema_version":3,"task_id":"prepare","contract":{"kind":"lifecycle-work-unit"},"checkpoint_ref":"cp.json"}`, "cp.json": `{"schema_version":2,"gates":{}}`, "CONTEXT.md": "# Context", ".template-spec/process/orchestration-contract.yaml": "{}", "yss-project.yaml": "{}", ".template-spec/process/harness-profile.yaml": `{"profile_id":"harness.spec-template"}`, ".template-spec/process/lifecycle-registry.yaml": "{}", ".template-spec/agents/yss-skill-registry.yaml": "{}"}
	for ref, content := range files {
		file := filepath.Join(root, ref)
		if e := os.MkdirAll(filepath.Dir(file), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(file, []byte(content), 0644); e != nil {
			t.Fatal(e)
		}
	}
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	s.GovernanceVersion = 2
	s.Report.Profile = "spec"
	s.GovernancePolicy = map[string]any{"stage_gates": map[string]any{"capability": kernel.StageGatesCapability}}
	refs, e := contractHandoffClosure(s, "source.json", &NativeHandoff{Handoff: map[string]any{"schema_version": 5}, Config: map[string]any{}})
	if e != nil || !slices.Contains(refs, "cp.json") {
		t.Fatalf("frozen task lost checkpoint authority: %v %v", refs, e)
	}
}

func TestLegacyHandoffClosurePreservesSourceProfile(t *testing.T) {
	root := t.TempDir()
	profileRef := ".template-spec/process/harness-profile.yaml"
	for ref, content := range map[string]string{"source.json": "{}", "CONTEXT.md": "# Context", ".template-spec/agents/digital-human-roles.yaml": "{}", profileRef: `{"schema_version":2,"profile_id":"harness.spec-template"}`} {
		file := filepath.Join(root, ref)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s := kernel.NewSemanticSession(context.Background(), root, nil)
	refs, err := contractHandoffClosure(s, "source.json", &NativeHandoff{Handoff: map[string]any{"schema_version": 5}, Config: map[string]any{}})
	if err != nil || !slices.Contains(refs, profileRef) {
		t.Fatalf("offline source lost its original Profile: %v %v", refs, err)
	}
}
