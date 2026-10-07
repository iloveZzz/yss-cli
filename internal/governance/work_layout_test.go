package governance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/governance"
)

func TestStageUsesConfiguredNewAndCustomWorkRoot(t *testing.T) {
	for _, base := range []string{".work", "docs/custom-work"} {
		t.Run(base, func(t *testing.T) {
			root := stageFixture(t)
			if e := os.MkdirAll(filepath.Dir(filepath.Join(root, base)), 0755); e != nil {
				t.Fatal(e)
			}
			if e := os.Rename(filepath.Join(root, "docs/.scratch"), filepath.Join(root, base)); e != nil {
				t.Fatal(e)
			}
			tracker := filepath.Join(root, ".template-spec/agents/issue-tracker.md")
			raw, _ := os.ReadFile(tracker)
			if e := os.WriteFile(tracker, []byte(strings.Replace(string(raw), "root: docs/.scratch", "root: "+base, 1)), 0644); e != nil {
				t.Fatal(e)
			}
			cp := base + "/report/checkpoint.json"
			plan, e := governance.Run("stage", "register", root, map[string]string{"checkpoint": cp, "items": "seeds.json"})
			if e != nil {
				t.Fatal(e)
			}
			b, e := json.Marshal(plan)
			if e != nil {
				t.Fatal(e)
			}
			put(t, root, "plan.json", string(b))
			if _, e := governance.Run("stage", "apply", root, map[string]string{"plan-file": "plan.json"}); e != nil {
				t.Fatal(e)
			}
			if _, e := governance.Run("stage", "status", root, map[string]string{"checkpoint": cp}); e != nil {
				t.Fatal(e)
			}
			if _, e := os.Stat(filepath.Join(root, base, "report/parent-ticket.md")); e != nil {
				t.Fatal(e)
			}
			if _, e := governance.Run("stage", "status", root, map[string]string{"checkpoint": "docs/.scratch/report/checkpoint.json"}); e == nil {
				t.Fatal("fixed legacy checkpoint accepted on new root")
			}
		})
	}
}

func TestRuntimeProtectsRepositoriesRegisteredUnderConfiguredRoot(t *testing.T) {
	for _, base := range []string{".work", "custom-work"} {
		t.Run(base, func(t *testing.T) {
			root, external := canonicalTemp(t), canonicalTemp(t)
			put(t, root, ".template-spec/agents/issue-tracker.md", "---\ntracker:\n  platform: local-markdown\n  root: "+base+"\n---\n")
			put(t, root, base+"/report/repository.yaml", "local_worktree: '"+external+"'\n")
			if _, e := governance.Run("runtime", "begin", root, map[string]string{"home": filepath.Join(external, "runtime")}); e == nil {
				t.Fatal("configured-root registration did not protect runtime")
			}
		})
	}
}
