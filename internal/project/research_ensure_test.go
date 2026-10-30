package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The real governed Spec catalog contains research references that may be
// selected on demand; ensure installs the reference and protects later edits.
func TestGovernedInitAndEnsureKeepInstalledSkillResources(t *testing.T) {
	b := trustedBundle(t, "spec")
	view, err := b.ForInstallation("governed")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := writeInstallationBundleFixtureWithKind(t, b, InstallationGoverned)
	const skill = "yss-research"
	const target = ".template-spec/plan/templates/competitive-matrix-template.md"
	refs, err := SelectSkillResources(view, InstallationGoverned, []string{skill}, nil)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := buildCore(root, "spec", "skills", nil, refs, nil, PlanningOptions{target: b})
	if err != nil || !plan.ReadyToApply {
		t.Fatalf("plan: %+v %v", plan, err)
	}
	if err = applyStandaloneFixture(t, plan, b); err != nil {
		t.Fatal(err)
	}
	want, err := view.Files[target].Render(plan.Variables)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, target))
	if err != nil || string(got) != string(want) {
		t.Fatalf("reference not installed: %v", err)
	}
	repeat, err := buildCore(root, "spec", "skills", nil, refs, nil, PlanningOptions{target: b})
	if err != nil || !repeat.ReadyToApply || len(repeat.Changes) != 0 {
		t.Fatalf("ensure not idempotent: %+v %v", repeat, err)
	}
	if err = os.Remove(filepath.Join(root, target)); err != nil {
		t.Fatal(err)
	}
	plan, err = buildCore(root, "spec", "skills", nil, refs, nil, PlanningOptions{target: b})
	if err != nil {
		t.Fatal(err)
	}
	if plan.ReadyToApply || !strings.Contains(strings.Join(plan.Conflicts, "\n"), target) {
		t.Fatalf("missing reference not diagnosed: %+v", plan.Conflicts)
	}
	if _, err = os.Stat(filepath.Join(root, target)); !os.IsNotExist(err) {
		t.Fatalf("planning wrote missing reference: %v", err)
	}
}

func TestGovernedEnsureAddsNeverInstalledResearchResource(t *testing.T) {
	b := trustedBundle(t, "spec")
	view, err := b.ForInstallation("governed")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := writeInstallationBundleFixtureWithKind(t, b, InstallationGoverned)
	const target = ".template-spec/plan/templates/competitive-matrix-template.md"
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := id.Native.Managed[target]; ok {
		t.Fatal("resource already managed")
	}
	if _, err = os.Stat(filepath.Join(root, target)); !os.IsNotExist(err) {
		t.Fatalf("resource already exists: %v", err)
	}
	refs, err := SelectSkillResources(view, InstallationGoverned, []string{"yss-research"}, nil)
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
		t.Fatal("ensure omitted never-installed resource")
	}
	if _, err = os.Stat(filepath.Join(root, target)); !os.IsNotExist(err) {
		t.Fatalf("planning wrote resource: %v", err)
	}
	writeFixtureChanges(t, root, plan)
	want, err := view.Files[target].Render(plan.Variables)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, target))
	if err != nil || string(got) != string(want) {
		t.Fatalf("ensure failed to install reference: %v", err)
	}
}
