package project

import (
	"context"
	"encoding/base64"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationWorkDigestDetectsNewTasks(t *testing.T) {
	root := t.TempDir()
	before, _, err := observeInstallationWork(context.Background(), root, []string{".work"})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(root, ".work/task"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ".work/task/parent-ticket.md"), []byte("active task"), 0644); err != nil {
		t.Fatal(err)
	}
	after, _, err := observeInstallationWork(context.Background(), root, []string{".work"})
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatal("new task not observed")
	}
}
func TestActiveBindingsBlockMigration(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".work/task"), 0755)
	_ = os.WriteFile(filepath.Join(root, ".work/task/checkpoint.json"), []byte(`{"schema_version":2,"status":"running"}`), 0644)
	if err := validateInstallationWork(context.Background(), root, []string{".work"}); err == nil {
		t.Fatal("active task permitted migration")
	}
}
func TestMigrationRejectsUnknownSource(t *testing.T) {
	root := t.TempDir()
	if _, err := BuildInstallationMigration(context.Background(), root, InstallationSkills); err == nil {
		t.Fatal("missing source accepted")
	}
}

func TestMigrationDoesNotRewriteHistory(t *testing.T) {
	root, _ := writeInstallationFixture(t)
	history := filepath.Join(root, "history.txt")
	if err := os.WriteFile(history, []byte("original decision"), 0644); err != nil {
		t.Fatal(err)
	}
	p, err := buildCore(root, "spec", "migrate", nil, nil, nil, PlanningOptions{target: installationFixture(), Installation: InstallationGoverned})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Changes {
		if c.Path == "history.txt" {
			t.Fatal("changed user history")
		}
	}
	data, err := os.ReadFile(history)
	if err != nil || string(data) != "original decision" {
		t.Fatal("history changed")
	}
	if p.InstallationMigration == nil || p.InstallationMigration.Source != InstallationSkills || p.InstallationMigration.Target != InstallationGoverned {
		t.Fatal("missing migration preview")
	}
	if err = os.MkdirAll(filepath.Join(root, ".work/new"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = verifyInstallationWork(context.Background(), p); err == nil {
		t.Fatal("plan allowed added task directory")
	}
}

func TestDeletingTrackerCannotHideActiveBindings(t *testing.T) {
	root := t.TempDir()
	tracker := filepath.Join(root, ".template-spec/agents/issue-tracker.md")
	_ = os.MkdirAll(filepath.Dir(tracker), 0755)
	_ = os.WriteFile(tracker, []byte("---\ntracker:\n  root: tasks\n---\n"), 0644)
	_ = os.MkdirAll(filepath.Join(root, "tasks/active"), 0755)
	_ = os.WriteFile(filepath.Join(root, "tasks/active/checkpoint.json"), []byte(`{"status":"running"}`), 0644)
	id := &Identity{Root: root, Native: &Metadata{SchemaVersion: 4, Distribution: map[string]any{}}}
	if _, err := planInstallationMigration(context.Background(), id, InstallationSkills); err == nil {
		t.Fatal("active binding accepted")
	}
	if err := os.Remove(tracker); err != nil {
		t.Fatal(err)
	}
	if _, err := planInstallationMigration(context.Background(), id, InstallationSkills); err == nil {
		t.Fatal("deleting tracker hid active tasks")
	}
}
func TestLegacyIdentityIsRegeneratedForBundle5Migration(t *testing.T) {
	root := t.TempDir()
	oldData := []byte("schema_version: 1\nrepository_mode: project-instance\n")
	if err := os.WriteFile(filepath.Join(root, "yss-project.yaml"), oldData, 0644); err != nil {
		t.Fatal(err)
	}
	before, err := safefs.Describe(root, "yss-project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	b := installationFixture()
	target := bundle.File{Data: base64.StdEncoding.EncodeToString([]byte("schema_version: 2\nrepository_mode: project-instance\n")), Digest: safefs.Digest([]byte("schema_version: 2\nrepository_mode: project-instance\n")), Mode: 0644, Ownership: "user-owned"}
	p := &Plan{Root: root, Command: "migrate", Profile: "spec", Inputs: map[string]domain.Descriptor{}, Variables: map[string]string{}}
	id := &Identity{Root: root, Native: &Metadata{SchemaVersion: 3, Distribution: map[string]any{}}}
	old := map[string]Managed{"yss-project.yaml": {Baseline: before, Applied: before, Ownership: "user-owned"}}
	u, err := newUpgradePlanner(id, b, p, old, PlanningOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = u.planAssets(map[string]bundle.File{"yss-project.yaml": target}, false); err != nil {
		t.Fatal(err)
	}
	for _, c := range p.Changes {
		if c.Path == "yss-project.yaml" && c.After.Digest == target.Digest {
			return
		}
	}
	t.Fatal("legacy identity1 was preserved alongside metadata5")
}

func TestSkillsMigrationDoesNotRequireCheckpointForOrdinaryTask(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, ".work/task"), 0755)
	_ = os.WriteFile(filepath.Join(root, ".work/task/task.md"), []byte("ordinary work in progress"), 0644)
	id := &Identity{Root: root, Native: &Metadata{SchemaVersion: 5, Distribution: map[string]any{"installation": "skills"}}}
	if _, err := planInstallationMigration(context.Background(), id, InstallationGoverned); err != nil {
		t.Fatal(err)
	}
}
