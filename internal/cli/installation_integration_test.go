//go:build !yss_nobundle

package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstallationFullPreservesChoice(t *testing.T) {
	for _, kind := range []string{"", "skills"} {
		t.Run(kind, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "not-created")
			file := filepath.Join(t.TempDir(), "full.json")
			args := []string{"init", "--profile", "spec", "--plan", "--out", file, "--full"}
			want := project.InstallationGoverned
			if kind != "" {
				args = append(args, "--installation", kind)
				want = project.InstallationSkills
			}
			commandOK(t, root, args...)
			p, err := project.ReadPlan(file)
			if err != nil {
				t.Fatal(err)
			}
			if p.Installation != want {
				t.Fatalf("full switched installation: %s", p.Installation)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("planning wrote target: %v", err)
			}
		})
	}
}

func TestExplicitSkillsCLI(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	if b.SchemaVersion < 5 {
		t.Skip("fixed source predates independent installations")
	}
	root := filepath.Join(t.TempDir(), "project")
	commandOK(t, root, "init", "--profile", "spec", "--installation", "skills")
	for _, ref := range []string{"yss-project.yaml", "CONTEXT.md"} {
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
	assertInstalledSkillReferences(t, root, b, roots)
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
	for _, ref := range []string{"yss-project.yaml", "CONTEXT.md"} {
		if _, err = os.Stat(filepath.Join(root, ref)); !os.IsNotExist(err) {
			t.Fatalf("ensure introduced governance: %s", ref)
		}
	}
	assertInstalledSkillReferences(t, root, b, append(append([]string{}, roots...), "setup-yss-harness"))
	// 新 Spec 不自动将独立 skills 或旧实例转换到新 governed 工作流。
	plan = filepath.Join(t.TempDir(), "migration.json")
	code, out, _ := captureCommand(t, context.Background(), []string{"migrate", "--root", root, "--installation", "governed", "--plan", "--out", plan, "--json"}, false)
	if code == 0 || commandEnvelope(t, out)["code"] != "SPEC_MANUAL_MIGRATION_REQUIRED" {
		t.Fatalf("unexpected automatic workflow migration: %d %s", code, out)
	}
	if _, err := os.Stat(plan); !os.IsNotExist(err) {
		t.Fatal("rejected migration wrote plan")
	}

}

// Historical v5 bundles have no reference map. New bundles must install and
// bind each selected skill's declared passive assets, without acquiring identity.
func assertInstalledSkillReferences(t *testing.T, root string, b *bundle.Bundle, skills []string) {
	t.Helper()
	id, err := project.Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	if id.Native.Distribution["installation"] != "skills" {
		t.Fatal("reference assets changed installation identity")
	}
	item := b.Installations["skills"]
	seenSource, seenSpec := false, false
	for _, skill := range skills {
		refs, err := item.ReferencesForSkill(skill)
		if err != nil {
			t.Fatal(err)
		}
		for _, ref := range refs {
			seenSource = seenSource || strings.HasPrefix(ref, ".template-source/")
			seenSpec = seenSpec || strings.HasPrefix(ref, ".template-spec/")
			expected, ok := b.Files[ref]
			if !ok {
				t.Fatalf("declared reference absent from Bundle: %s", ref)
			}
			got, err := safefs.Describe(root, ref)
			if err != nil {
				t.Fatal(err)
			}
			managed, ok := id.Native.Managed[ref]
			if !ok || got.Type != "file" || got != managed.Applied || managed.Source == nil || managed.Source.Path != ref || managed.Source.TemplateCommit != b.TemplateCommit || managed.Source.SnapshotHash != b.SnapshotHash {
				t.Fatalf("reference missing or unbound: %s", ref)
			}
			// harness-profile is natively transformed during materialization.
			if ref != ".template-spec/process/harness-profile.yaml" && managed.Source.TemplateDigest != expected.Digest {
				t.Fatalf("reference source mismatch: %s", ref)
			}
		}
	}
	if os.Getenv("YSS_TEST_REQUIRE_SKILL_REFERENCES") == "1" && (!seenSource || !seenSpec) {
		t.Fatal("required skill reference declarations missing from Bundle")
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
	// Reuse the initialized governed fixture while isolating its files and journals.
	root := commandProject(t)
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
	decisions := []project.Resolution{decision}
	for _, asset := range original.Assets {
		if asset.Path == "skills-lock.json" {
			for _, conflict := range original.Conflicts {
				if conflict == asset.Path {
					decisions = append(decisions, project.Resolution{Path: asset.Path, Choice: "use-template", Before: asset.Before, Target: asset.Target})
				}
			}
		}
	}
	raw, err := json.Marshal(project.ResolutionFile{SchemaVersion: 1, PlanDigest: original.Digest, Decisions: decisions})
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
		t.Fatalf("resolution lost installation intent: migration=%+v installation=%s conflicts=%v", resolved.InstallationMigration, resolved.Installation, resolved.Conflicts)
	}
	commandOK(t, root, "migrate", "--apply", "--plan-file", resolvedFile)
}
