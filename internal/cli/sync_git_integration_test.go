//go:build !yss_nobundle

package cli

import (
	"context"
	"path/filepath"
	"testing"
)

func prepareCLISyncGit(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "--allow-empty", "-qm", "fixture"}} {
		if out, err := gitIn(root, args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

func TestSyncRequiresGitRepository(t *testing.T) {
	root := commandProject(t)
	plan := filepath.Join(t.TempDir(), "sync.json")
	code, out, _ := captureCommand(t, context.Background(), []string{"sync", "--root", root, "--plan", "--out", plan, "--json"}, false)
	if code == 0 || commandEnvelope(t, out)["code"] != "ROOT" {
		t.Fatalf("sync did not reject a project outside Git: %s", out)
	}
}
