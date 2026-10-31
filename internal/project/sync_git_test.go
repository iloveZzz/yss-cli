package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func syncTestGit(t *testing.T, root string, args ...string) {
	t.Helper()
	args = append([]string{"-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false"}, args...)
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// syncTestSnapshot gives planner tests a committed Git starting point.
func syncTestSnapshot(t *testing.T, root string) {
	t.Helper()
	syncTestGit(t, root, "init", "-q")
	syncTestGit(t, root, "add", "-A")
	staged := exec.Command("git", "-C", root, "diff", "--cached", "--quiet", "--exit-code")
	head := exec.Command("git", "-C", root, "rev-parse", "--verify", "HEAD")
	if staged.Run() == nil && head.Run() == nil {
		return
	}
	syncTestGit(t, root, "commit", "--allow-empty", "-qm", "fixture")
}

func TestSyncApplyRejectsChangedGitHead(t *testing.T) {
	b := trustedBundle(t, "spec")
	root, _ := writeInstallationBundleFixture(t, b)
	syncTestGit(t, root, "init", "-q")
	syncTestGit(t, root, "add", "-A")
	syncTestGit(t, root, "commit", "-qm", "base")
	p, err := buildCore(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
	if err != nil {
		t.Fatal(err)
	}
	syncTestGit(t, root, "commit", "--allow-empty", "-qm", "changed HEAD")
	_, err = prepareApplication(context.Background(), p)
	var rejection *domain.Error
	if !errors.As(err, &rejection) || rejection.Code != "INPUT_DRIFT" {
		t.Fatalf("changed Git HEAD not rejected as input drift: %v", err)
	}
}

func TestSyncApplyRejectsNewUntrackedFile(t *testing.T) {
	b := trustedBundle(t, "spec")
	root, _ := writeInstallationBundleFixture(t, b)
	syncTestSnapshot(t, root)
	p, err := buildCore(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
	if err != nil {
		t.Fatal(err)
	}
	userFile := filepath.Join(root, "user-untracked.txt")
	if err := os.WriteFile(userFile, []byte("user data"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = prepareApplication(context.Background(), p)
	var rejection *domain.Error
	if !errors.As(err, &rejection) || rejection.Code != "CONFLICT" {
		t.Fatalf("untracked file not rejected: %v", err)
	}
	if got, err := os.ReadFile(userFile); err != nil || string(got) != "user data" {
		t.Fatalf("user data changed: %q %v", got, err)
	}
}

func TestSyncGitRejectsDirtySubmodule(t *testing.T) {
	module := t.TempDir()
	syncTestGit(t, module, "init", "-q")
	if err := os.WriteFile(filepath.Join(module, "file"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	syncTestGit(t, module, "add", "file")
	syncTestGit(t, module, "commit", "-qm", "base")
	root := t.TempDir()
	syncTestGit(t, root, "init", "-q")
	syncTestGit(t, root, "-c", "protocol.file.allow=always", "submodule", "add", "-q", module, "module")
	syncTestGit(t, root, "commit", "-qm", "base")
	if _, err := syncGitHead(context.Background(), root); err != nil {
		t.Fatalf("clean submodule rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "module", "file"), []byte("user edit"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := syncGitHead(context.Background(), root); err == nil {
		t.Fatal("dirty submodule accepted")
	}
}

func TestSyncGitPreservesCancellation(t *testing.T) {
	root := t.TempDir()
	syncTestGit(t, root, "init", "-q")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := syncGitHead(ctx, root)
	var rejection *domain.Error
	if !errors.As(err, &rejection) || rejection.Code != "CANCELLED" {
		t.Fatalf("cancellation misreported: %v", err)
	}
}

func TestSyncGitRequiresCommittedCleanWorktree(t *testing.T) {
	for _, state := range []string{"unborn", "untracked", "staged", "unstaged"} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			syncTestGit(t, root, "init", "-q")
			if state != "unborn" {
				file := filepath.Join(root, "tracked")
				if err := os.WriteFile(file, []byte("base"), 0600); err != nil {
					t.Fatal(err)
				}
				syncTestGit(t, root, "add", "tracked")
				syncTestGit(t, root, "commit", "-qm", "base")
				if _, err := syncGitHead(context.Background(), root); err != nil {
					t.Fatalf("clean worktree rejected: %v", err)
				}
				if state == "untracked" {
					file = filepath.Join(root, "untracked")
				}
				if err := os.WriteFile(file, []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
				if state == "staged" {
					syncTestGit(t, root, "add", "tracked")
				}
			}
			if _, err := syncGitHead(context.Background(), root); err == nil {
				t.Fatalf("accepted %s worktree", state)
			}
		})
	}
}

func TestSyncRejectsIgnoredTargets(t *testing.T) {
	root := t.TempDir()
	syncTestGit(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("hidden/\ntracked.txt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("tracked"), 0644); err != nil {
		t.Fatal(err)
	}
	syncTestGit(t, root, "add", ".gitignore")
	syncTestGit(t, root, "add", "-f", "tracked.txt")
	syncTestGit(t, root, "commit", "-qm", "base")
	for _, ref := range []string{"hidden/new.txt", "tracked.txt"} {
		if err := syncCheckIgnored(context.Background(), root, []string{ref}); err == nil {
			t.Fatalf("ignored target accepted: %s", ref)
		}
	}
	if err := syncCheckIgnored(context.Background(), root, []string{"normal.txt"}); err != nil {
		t.Fatal(err)
	}
}

func TestSyncPlanRejectsIgnoredManagedChange(t *testing.T) {
	b := trustedBundle(t, "spec")
	root, _ := writeInstallationBundleFixture(t, b)
	metadata := filepath.Join(root, MetadataFile)
	raw, err := os.ReadFile(metadata)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["cliVersion"] = "0.0.0"
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadata, raw, 0644); err != nil {
		t.Fatal(err)
	}
	syncTestSnapshot(t, root)
	if err := os.WriteFile(filepath.Join(root, ".git/info/exclude"), []byte("/.yss.json\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := buildCore(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b}); err == nil {
		t.Fatal("sync accepted ignored managed file")
	}
}

func TestSyncRechecksIgnoreRulesBetweenWrites(t *testing.T) {
	root := t.TempDir()
	syncTestGit(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("# baseline\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("protected"), 0644); err != nil {
		t.Fatal(err)
	}
	syncTestGit(t, root, "add", "-A")
	syncTestGit(t, root, "commit", "-qm", "base")
	ignoreBefore, err := safefs.Describe(root, ".gitignore")
	if err != nil {
		t.Fatal(err)
	}
	secretBefore, err := safefs.Describe(root, "secret.txt")
	if err != nil {
		t.Fatal(err)
	}
	result, err := transaction.ApplySyncWithValidation(context.Background(), root, []transaction.Operation{
		{Path: ".gitignore", Before: &ignoreBefore, Data: []byte("secret.txt\n"), Mode: 0644},
		{Path: "secret.txt", Before: &secretBefore, Data: []byte("overwrite"), Mode: 0644},
	}, nil, nil, func(ref string) error { return syncCheckIgnored(context.Background(), root, []string{ref}) }, nil)
	if err == nil || result.Status != "pending" {
		t.Fatalf("changed ignore rule must stop the write: %+v %v", result, err)
	}
	if got, e := os.ReadFile(filepath.Join(root, "secret.txt")); e != nil || string(got) != "protected" {
		t.Fatalf("ignored target changed: %q %v", got, e)
	}
}

func TestSyncDoesNotOverwriteIgnoredExistingControlFile(t *testing.T) {
	root := t.TempDir()
	syncTestGit(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".yss/transactions/lock.json\n"), 0644); err != nil {
		t.Fatal(err)
	}
	syncTestGit(t, root, "add", ".gitignore")
	syncTestGit(t, root, "commit", "-qm", "base")
	ref := ".yss/transactions/lock.json"
	file := filepath.Join(root, filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("user data"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := transaction.ApplySyncWithValidation(context.Background(), root, []transaction.Operation{{Path: "new.txt", Data: []byte("new"), Mode: 0644}}, nil, nil, func(ref string) error { return syncCheckIgnored(context.Background(), root, []string{ref}) }, nil)
	if err == nil {
		t.Fatal("sync accepted ignored control file")
	}
	if got, e := os.ReadFile(file); e != nil || string(got) != "user data" {
		t.Fatalf("ignored control file changed: %q %v", got, e)
	}
}

func TestSyncDoesNotRewriteIgnoredPreviousJournal(t *testing.T) {
	root := t.TempDir()
	syncTestGit(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".yss/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	syncTestGit(t, root, "add", ".gitignore")
	syncTestGit(t, root, "commit", "-qm", "base")
	check := func(ref string) error { return syncCheckIgnored(context.Background(), root, []string{ref}) }
	first, err := transaction.ApplySyncWithValidation(context.Background(), root, []transaction.Operation{{Path: "first", Data: []byte("new"), Mode: 0644}}, nil, nil, check, func() error { return errors.New("failed") })
	if err == nil {
		t.Fatal("expected failed sync")
	}
	if err := os.Remove(filepath.Join(root, "first")); err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(first.BackupPath, "journal.json")
	before, err := os.ReadFile(journal)
	if err != nil {
		t.Fatal(err)
	}
	_, err = transaction.ApplySyncWithValidation(context.Background(), root, []transaction.Operation{{Path: "second", Data: []byte("new"), Mode: 0644}}, nil, nil, check, nil)
	if err == nil {
		t.Fatal("sync accepted ignored previous journal")
	}
	after, err := os.ReadFile(journal)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("ignored journal changed: %v", err)
	}
}
