package project

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

// Governance initializes the closure for skills it declares already installed.
func TestGovernedInitAndEnsureKeepInstalledSkillResources(t *testing.T) {
	const skill = "yss-research"
	const target = ".template-spec/plan/templates/competitive-matrix-template.md"
	b := governedScaffoldFixture(t, skill)
	data := []byte("# matrix template\n")
	b.Files[target] = bundle.File{Data: base64.StdEncoding.EncodeToString(data), Digest: safefs.Digest(data), Mode: 0644, Ownership: "managed"}
	req := b.SkillRequirements[skill]
	req.Paths = append(req.Paths, target)
	b.SkillRequirements[skill] = req
	installation := b.Installations["governed"]
	installation.Distribution["installedSkills"] = []string{"implement", "alibaba-java-code-style", skill}
	for ref, f := range b.Files {
		if strings.Contains(ref, "/"+skill+"/") || strings.Contains(ref, "/alibaba-java-code-style/") {
			installation.Initial[ref] = f
		}
	}
	b.Installations["governed"] = installation
	root := t.TempDir()
	init, err := buildCore(root, "spec", "init", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
	if err != nil {
		t.Fatal(err)
	}
	if err = applyStandaloneFixture(t, init, b); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, target))
	if err != nil || string(got) != string(data) {
		t.Fatalf("initial resource missing: %q %v", got, err)
	}
	refs, err := SelectSkillResources(b, InstallationGoverned, []string{skill}, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildCore(root, "spec", "skills", nil, refs, nil, PlanningOptions{target: b})
	if err != nil || !plan.ReadyToApply {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	if len(plan.Changes) != 0 {
		t.Fatalf("complete skill unnecessarily changed files: %+v", plan.Changes)
	}
	if err = applyStandaloneFixture(t, plan, b); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(root, target))
	if err != nil || string(got) != string(data) {
		t.Fatalf("resource not installed: %q %v", got, err)
	}
	if err = os.Remove(filepath.Join(root, target)); err != nil {
		t.Fatal(err)
	}
	plan, err = buildCore(root, "spec", "skills", nil, refs, nil, PlanningOptions{target: b})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ReadyToApply || !strings.Contains(strings.Join(plan.Conflicts, "\n"), target) {
		t.Fatalf("missing managed resource not diagnosed: %+v", plan)
	}
	if _, err = os.Stat(filepath.Join(root, target)); !os.IsNotExist(err) {
		t.Fatalf("planning wrote missing resource: %v", err)
	}
}

// Model an existing governed instance created before a conditional requirement
// was selected. Its skill is already installed and the resource was never
// installed or registered as managed, so ensure can add it without overwriting.
func TestGovernedEnsureAddsNeverInstalledConditionalResource(t *testing.T) {
	const skill = "yss-research"
	const target = ".template-spec/plan/templates/competitive-matrix-template.md"
	b := governedScaffoldFixture(t, skill)
	data := []byte("# matrix template\n")
	b.Files[target] = bundle.File{Data: base64.StdEncoding.EncodeToString(data), Digest: safefs.Digest(data), Mode: 0644, Ownership: "managed"}
	installation := b.Installations["governed"]
	installation.Distribution["installedSkills"] = []string{"implement", "alibaba-java-code-style", skill}
	for ref, f := range b.Files {
		if strings.Contains(ref, "/"+skill+"/") || strings.Contains(ref, "/alibaba-java-code-style/") {
			installation.Initial[ref] = f
		}
	}
	b.Installations["governed"] = installation
	root := t.TempDir()
	init, err := buildCore(root, "spec", "init", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
	if err != nil {
		t.Fatal(err)
	}
	if err = applyStandaloneFixture(t, init, b); err != nil {
		t.Fatal(err)
	}
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := id.Native.Managed[target]; ok {
		t.Fatal("never-installed resource was already registered")
	}
	if _, err = os.Stat(filepath.Join(root, target)); !os.IsNotExist(err) {
		t.Fatalf("never-installed resource already exists: %v", err)
	}
	if _, err = os.Stat(filepath.Join(root, ".agents/skills/yss-research/SKILL.md")); err != nil {
		t.Fatalf("research skill missing: %v", err)
	}
	// Fixture-only selection update models an additional conditional requirement;
	// candidate source identity and actual bundle contents are tested separately.
	req := b.SkillRequirements[skill]
	req.Paths = append(req.Paths, target)
	b.SkillRequirements[skill] = req
	refs, err := SelectSkillResources(b, InstallationGoverned, []string{skill}, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildCore(root, "spec", "skills", nil, refs, nil, PlanningOptions{target: b})
	if err != nil || !plan.ReadyToApply {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	found := false
	for _, change := range plan.Changes {
		if change.Path == target && change.Before.Type == "missing" && change.After.Type == "file" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ensure plan omitted never-installed resource: %+v", plan.Changes)
	}
	if _, err = os.Stat(filepath.Join(root, target)); !os.IsNotExist(err) {
		t.Fatalf("ensure planning wrote resource: %v", err)
	}
	if err = applyStandaloneFixture(t, plan, b); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, target))
	if err != nil || string(got) != string(data) {
		t.Fatalf("ensure did not install resource: %q %v", got, err)
	}
}
