//go:build !yss_nobundle

package cli

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/project"
	"os"
	"path/filepath"
	"reflect"
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
	roots := []string{"yss-product-lifecycle", "grilling", "yss-research", "domain-modeling", "to-spec", "i-have-adhd", "code-review", "implement"}
	installed := meta["distribution"].(map[string]any)["installedSkills"].([]any)
	if len(installed) != len(roots) {
		t.Fatalf("default skills: %v", installed)
	}
	before := installationInventory(t, root)
	doctor := commandOK(t, root, "doctor")
	readiness := doctor["readiness"].(map[string]any)
	if readiness["skills"].(map[string]any)["status"] != "ready" || readiness["workflow"] != "not-evaluated" || readiness["files"] != "aligned" || doctor["readyToApply"] != true {
		t.Fatalf("wrong skills doctor contract: %v", doctor)
	}
	for _, runtime := range []string{"", "claude", "workbuddy"} {
		args := append([]string{"skills", "resolve"}, roots...)
		if runtime != "" {
			args = append(args, "--agent-runtime", runtime)
		}
		resolved := commandOK(t, root, args...)
		if resolved["status"] != "ready" || len(resolved["issues"].([]any)) != 0 || len(resolved["missing"].([]any)) != 0 {
			t.Fatalf("default skills not ready: %v", resolved)
		}
		for _, item := range resolved["skills"].([]any) {
			skill := item.(map[string]any)
			entry := filepath.Join(root, ".agents", "skills", skill["id"].(string), "SKILL.md")
			raw, err := os.ReadFile(entry)
			if err != nil {
				t.Fatal(err)
			}
			if skill["status"] != "ready" || skill["entryPath"] != entry || skill["contentDigest"] != fmt.Sprintf("%x", sha256.Sum256(raw)) {
				t.Fatalf("unverified skill entry: %v", skill)
			}
		}
	}
	if !reflect.DeepEqual(before, installationInventory(t, root)) {
		t.Fatal("readiness query wrote project files")
	}
	plan := filepath.Join(t.TempDir(), "sync.json")
	commandOK(t, root, "sync", "--plan", "--out", plan)
	result := commandOK(t, root, "sync", "--apply", "--plan-file", plan)
	if result["status"] != "unchanged" {
		t.Fatalf("fresh sync changed installation: %v", result)
	}
	plan = filepath.Join(t.TempDir(), "ensure.json")
	commandOK(t, root, "skills", "ensure", "setup-yss-harness", "--plan", "--out", plan)
	commandOK(t, root, "skills", "--apply", "--plan-file", plan)
	raw, err = os.ReadFile(filepath.Join(root, ".yss.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if len(meta["distribution"].(map[string]any)["installedSkills"].([]any)) != 9 {
		t.Fatal("ensure roots inconsistent")
	}
	for _, runtime := range []string{"", "claude", "workbuddy"} {
		args := append([]string{"skills", "resolve"}, append(roots, "setup-yss-harness")...)
		if runtime != "" {
			args = append(args, "--agent-runtime", runtime)
		}
		if got := commandOK(t, root, args...); got["status"] != "ready" {
			t.Fatalf("ensured skills: %v", got)
		}
	}
	for _, ref := range []string{"yss-project.yaml", "CONTEXT.md", ".template-spec", "scripts"} {
		if _, err = os.Stat(filepath.Join(root, ref)); !os.IsNotExist(err) {
			t.Fatalf("ensure introduced governance: %s", ref)
		}
	}
	for _, kind := range []string{"governed", "skills"} {
		plan = filepath.Join(t.TempDir(), "migration.json")
		commandOK(t, root, "migrate", "--installation", kind, "--plan", "--out", plan)
		commandOK(t, root, "migrate", "--apply", "--plan-file", plan)
		if kind == "governed" {
			roots := []string{"yss-product-lifecycle", "setup-yss-harness", "yss-research", "grilling", "domain-modeling", "yss-stage-decision", "i-have-adhd", "code-review"}
			raw, err := os.ReadFile(filepath.Join(root, ".yss.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &meta); err != nil {
				t.Fatal(err)
			}
			installed := meta["distribution"].(map[string]any)["installedSkills"].([]any)
			for _, skill := range roots {
				found := false
				for _, value := range installed {
					found = found || value == skill
				}
				if !found {
					t.Fatalf("governed core missing %s: %v", skill, installed)
				}
			}
			for _, runtime := range []string{"", "claude", "workbuddy"} {
				args := append([]string{"skills", "resolve"}, roots...)
				if runtime != "" {
					args = append(args, "--agent-runtime", runtime)
				}
				if got := commandOK(t, root, args...); got["status"] != "ready" {
					t.Fatalf("governed skills not ready: %v", got)
				}
			}
			doctor := commandOK(t, root, "doctor")
			if doctor["identity"] == nil || doctor["context"] == nil || doctor["plan"] == nil || doctor["stableReady"] != false || doctor["readiness"].(map[string]any)["workflow"] != "not-evaluated" {
				t.Fatalf("governed doctor structure lost: %v", doctor)
			}

		}
	}
}

func installationInventory(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		var raw []byte
		if !info.IsDir() {
			raw, err = os.ReadFile(path)
			if err != nil {
				return err
			}
		}
		files[path] = fmt.Sprintf("%o:%s", info.Mode(), raw)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return files
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
