package governance

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"testing"
)

// The Node producer uses the actual current template scripts; the native reader
// must reproduce that output and its dependency closure without writing.
func TestRegisteredReadingCurrentNodeNativeParity(t *testing.T) {
	tool := os.Getenv("YSS_LEGACY_ORACLE_ROOT")
	if tool == "" {
		t.Skip("set YSS_LEGACY_ORACLE_ROOT for fixed current Node/native reading parity")
	}
	for _, scenario := range []struct{ profile, checkpoint, feature string }{
		{"spec", ".work/supplier/checkpoint.json", "feature.supplier"},
		{"design", "intake-checkpoint.json", "feature.supplier"},
		{"spec", ".work/supplier/checkpoint.json", "supplier"},
	} {
		t.Run(scenario.profile+"/"+scenario.feature, func(t *testing.T) {
			root, base := stageRegisteredFeatureFixture(t, scenario.profile, ".work", scenario.checkpoint)
			cp := apTestCheckpoint(map[string]any{})
			cp["feature_id"] = scenario.feature
			apTestPut(t, root, scenario.checkpoint, cp)
			apTestPut(t, root, ".template-spec/process/reading-policy.yaml", map[string]any{"schema_version": 1, "mode": "managed", "checkpoints": []any{scenario.checkpoint}})
			if scenario.feature == "feature.supplier" {
				apTestPut(t, root, ".work/other/map.md", "---\ncheckpoint_ref: .work/other/checkpoint.json\n---\n")
				apTestPut(t, root, ".work/other/checkpoint.json", map[string]any{"feature_id": "feature.other"})
			}
			run := func(action string) {
				t.Helper()
				cmd := exec.Command("node", filepath.Join(tool, "scripts/contract"), action, "--root", root, "--checkpoint", scenario.checkpoint, "--json")
				out, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("actual Node %s: %v: %s", action, err, out)
				}
			}
			run("render")
			run("check-views")
			before := progressionInventory(t, root)
			s := newSemanticSession(context.Background(), root, map[string]string{"tool-root": tool})
			if err := s.authorities(); err != nil {
				t.Fatal(err)
			}
			expected, err := s.buildReading(scenario.checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			if expected.OutputOrder[0] != base+"/reading/status.review.md" {
				t.Fatalf("native output ignored registration root: %#v", expected.OutputOrder)
			}
			if _, cycle := expected.ProjectRefs[base+"/map.md"]; cycle {
				t.Fatal("navigation map became its own dependency")
			}
			if err = verifyReadingTransitionSemantic(s, scenario.checkpoint, nil); err != nil {
				t.Fatal(err)
			}
			if err = s.finish(); err != nil {
				t.Fatal(err)
			}
			if !contractSame(before, progressionInventory(t, root)) {
				t.Fatal("native reading check wrote files")
			}
			// Mutation of any recognised renderer must still fail the fixed
			// capability boundary, even when the stored manifest was genuine.
			toolCopy, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			for ref := range expected.ToolRefs {
				raw, err := os.ReadFile(filepath.Join(tool, filepath.FromSlash(ref)))
				if err != nil {
					t.Fatal(err)
				}
				apTestPut(t, toolCopy, ref, raw)
			}
			apTestPut(t, toolCopy, "yss-project.yaml", "schema_version: 1\nrepository_mode: template-source\n")
			ref := "scripts/lib/lifecycle-presentation.mjs"
			raw, err := os.ReadFile(filepath.Join(toolCopy, filepath.FromSlash(ref)))
			if err != nil {
				t.Fatal(err)
			}
			apTestPut(t, toolCopy, ref, append(raw, []byte("\n// unsupported renderer\n")...))
			_, err = newSemanticSession(context.Background(), root, map[string]string{"tool-root": toolCopy}).buildReading(scenario.checkpoint)
			apTestCode(t, err, "CAPABILITY")
			if path.Dir(expected.OutputOrder[0]) != base+"/reading" {
				t.Fatal("reading directory changed during validation")
			}
		})
	}
}
