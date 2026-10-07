package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDailyFormalHistoryAcrossConfiguredAndHistoricalWorkRoots(t *testing.T) {
	for _, current := range []string{".work", "docs/custom-work"} {
		for _, formal := range []string{current, "docs/.scratch", ".scratch", "docs/requirements/tickets"} {
			t.Run(current+"-"+formal, func(t *testing.T) {
				f := newDailyFixture(t)
				dailyWrite(t, f.root, ".template-spec/agents/issue-tracker.md", []byte("---\ntracker:\n  platform: local-markdown\n  root: "+current+"\n---\n"))
				ref := formal + "/example/slice.yaml"
				dailyWrite(t, f.root, ref, []byte("schema_version: 3\nslice_id: slice.example\nscope:\n  ticket_ref: task.md\n"))
				if code, env := f.run(t, "route"); code != 0 || env["result"].(map[string]any)["delivery_path"] != "governed" {
					t.Fatalf("formal task lost after layout change: %d %#v", code, env)
				}
				dailyGit(t, f.root, "init", "-q")
				dailyGit(t, f.root, "add", ".")
				dailyGit(t, f.root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "formal basis")
				if e := os.Remove(filepath.Join(f.root, ref)); e != nil {
					t.Fatal(e)
				}
				if code, env := f.run(t, "route"); code != 0 || env["result"].(map[string]any)["delivery_path"] != "governed" {
					t.Fatalf("Git history downgraded: %d %#v", code, env)
				}
			})
		}
	}
}
