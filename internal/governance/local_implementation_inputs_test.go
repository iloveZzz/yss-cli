package governance

import (
	"context"
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalImplementationInputsSelectExplicitStagePolicy(t *testing.T) {
	for _, tc := range []struct {
		name      string
		version   int
		stage, ok bool
	}{
		{"legacy", 1, false, true}, {"stage", 2, true, true}, {"unselected-v2", 2, false, false}, {"unknown", 3, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			s := kernel.NewSemanticSession(context.Background(), root, nil)
			s.Report.Profile = "spec"
			if tc.stage {
				s.GovernanceVersion = 2
				s.GovernancePolicy = map[string]any{"stage_gates": map[string]any{"capability": kernel.StageGatesCapability}}
			}
			docs := map[string]any{"yss-project.yaml": map[string]any{"schema_version": 1, "repository_mode": "project-instance"}, ".yss.json": map[string]any{"profile": "spec", "profileId": "harness.spec-template"}, ".template-spec/process/harness-profile.yaml": map[string]any{"profile_id": "harness.spec-template"}, kernel.GuidanceContractRef("spec"): map[string]any{"schema_version": tc.version, "progression_target": map[string]any{"schema_version": 1, "config_file": kernel.ProgressionFile, "required_capabilities": []any{"lifecycle-target-v1"}, "writer_profiles": []any{"spec"}, "local_implementation_inputs": "native-spec-current-feature-approved-assets"}}}
			for ref, doc := range docs {
				p := filepath.Join(s.Root, ref)
				if e := os.MkdirAll(filepath.Dir(p), 0755); e != nil {
					t.Fatal(e)
				}
				b, e := json.Marshal(doc)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(p, b, 0644); e != nil {
					t.Fatal(e)
				}
			}
			enabled, e := hasLocalImplementationInputs(s)
			if (enabled && e == nil) != tc.ok {
				t.Fatalf("local source selection: enabled=%v err=%v", enabled, e)
			}
		})
	}
}
