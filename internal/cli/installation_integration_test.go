//go:build !yss_nobundle

package cli

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/project"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillsDefaultCLI(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	if b.SchemaVersion < 5 {
		t.Skip("fixed source predates independent installations")
	}
	root := filepath.Join(t.TempDir(), "project")
	commandOK(t, root, "init", "--profile", "spec")
	for _, ref := range []string{"yss-project.yaml", "CONTEXT.md", ".template-spec"} {
		if _, err = os.Stat(filepath.Join(root, ref)); !os.IsNotExist(err) {
			t.Fatalf("unexpected ordinary governance asset %s", ref)
		}
	}
	raw, err := os.ReadFile(filepath.Join(root, ".yss.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	if err = json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta["schemaVersion"] != float64(5) || meta["distribution"].(map[string]any)["installation"] != "skills" {
		t.Fatal("ordinary installation not recorded")
	}
	plan := filepath.Join(t.TempDir(), "sync.json")
	commandOK(t, root, "sync", "--plan", "--out", plan)
	result := commandOK(t, root, "sync", "--apply", "--plan-file", plan)
	if result["status"] != "unchanged" {
		t.Fatalf("fresh sync changed installation: %v", result)
	}
	for _, kind := range []string{"governed", "skills"} {
		plan = filepath.Join(t.TempDir(), "migration.json")
		commandOK(t, root, "migrate", "--installation", kind, "--plan", "--out", plan)
		commandOK(t, root, "migrate", "--apply", "--plan-file", plan)
	}
}

func TestInstallationMigrationResolutionCLI(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	if b.SchemaVersion < 5 {
		t.Skip("requires installation source")
	}
	root := filepath.Join(t.TempDir(), "project")
	commandOK(t, root, "init", "--installation", "governed", "--profile", "spec")
	entry := filepath.Join(root, "AGENTS.md")
	data, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(entry, append(data, []byte("\nLocal project instruction.\n")...), 0644); err != nil {
		t.Fatal(err)
	}
	// Preserve the original mode even under a restrictive umask.
	if err = os.Chmod(entry, 0644); err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(t.TempDir(), "migration.json")
	commandOK(t, root, "migrate", "--installation", "skills", "--plan", "--out", planFile)
	original, err := project.ReadPlan(planFile)
	if err != nil {
		t.Fatal(err)
	}
	var decision project.Resolution
	for _, a := range original.Assets {
		if a.Path == "AGENTS.md" {
			decision = project.Resolution{Path: a.Path, Choice: "use-template", Before: a.Before, Target: a.Target}
			break
		}
	}
	if len(original.Conflicts) == 0 || decision.Path == "" {
		t.Fatal("custom entry conflict not observed")
	}
	resolutionFile := filepath.Join(t.TempDir(), "resolution.json")
	raw, err := json.Marshal(project.ResolutionFile{SchemaVersion: 1, PlanDigest: original.Digest, Decisions: []project.Resolution{decision}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(resolutionFile, raw, 0644); err != nil {
		t.Fatal(err)
	}
	resolvedFile := filepath.Join(t.TempDir(), "resolved.json")
	commandOK(t, root, "migrate", "--plan", "--plan-file", planFile, "--resolution-file", resolutionFile, "--out", resolvedFile)
	resolved, err := project.ReadPlan(resolvedFile)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.InstallationMigration == nil || resolved.Installation != project.InstallationSkills || len(resolved.Conflicts) != 0 {
		t.Fatal("resolution lost installation intent")
	}
	commandOK(t, root, "migrate", "--apply", "--plan-file", resolvedFile)
}
