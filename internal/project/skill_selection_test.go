package project

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func selectionFixture(t *testing.T) *bundle.Bundle {
	t.Helper()
	file := func(s string) bundle.File {
		return bundle.File{Data: base64.StdEncoding.EncodeToString([]byte(s)), Digest: safefs.Digest([]byte(s)), Mode: 0644, Ownership: "managed"}
	}
	b := &bundle.Bundle{SchemaVersion: 5, Files: map[string]bundle.File{}, SkillRequirements: map[string]bundle.Requirement{
		"root": {Paths: []string{".template-spec/Common.yaml", "scripts/step.sh"}, Skills: []string{"required"}},
	}}
	registry := "schema_version: 3\nstatus: active\ncanonical_content_root: .agents/skills\ninvocation_contract:\n  schema_version: 2\nskills:\n"
	for _, name := range []string{"root", "required", "conditional", "review", "coordination", "component", "deprecated"} {
		maturity, aliases := "supported", "[]"
		if name == "root" {
			aliases = "[r]"
		}
		if name == "deprecated" {
			maturity = "deprecated"
		}
		registry += "  - {id: " + name + ", layer: core, maturity: " + maturity + ", aliases: " + aliases + "}\n"
		for _, prefix := range []string{".agents/skills/", ".claude/skills/", ".workbuddy/skills/"} {
			b.Files[prefix+name+"/SKILL.md"] = file("---\nname: " + name + "\ndescription: Fixture\n---\n")
			b.Files[prefix+name+"/references/guide.md"] = file(name + " guide\n")
		}
	}
	registry += "skill_dependencies:\n  root:\n    - {skill: required, type: context-required}\n    - {skill: conditional, type: context-conditional, when: x}\n    - {skill: review, type: review-only}\n    - {skill: coordination, type: coordination-only}\n    - {skill: component, type: component-dependency}\n"
	b.Files[skillRegistryRef] = file(registry)
	b.Files["skills-lock.json"] = file(`{"version":3,"skills":{"shared":{}}}`)
	b.Files["scripts/lib/skill-supply-chain.mjs"] = file(`export const OBSOLETE = new Set(["old"]);`)
	b.Files[".template-spec/Common.yaml"] = file("governance\n")
	b.Files["scripts/step.sh"] = file("governance step\n")
	return b
}

func TestSkillsSelectionRejectsGovernance(t *testing.T) {
	root, _ := writeInstallationFixture(t)
	before, err := os.ReadFile(filepath.Join(root, MetadataFile))
	if err != nil {
		t.Fatal(err)
	}
	_, err = buildCore(root, "spec", "skills", nil, []string{skillRegistryRef}, nil, PlanningOptions{target: installationFixture()})
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "GOVERNED_REQUIRED" {
		t.Fatalf("governance selection: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, MetadataFile))
	if string(after) != string(before) {
		t.Fatal("planning changed metadata")
	}
	if _, err := os.Stat(filepath.Join(root, skillRegistryRef)); !os.IsNotExist(err) {
		t.Fatal("planning wrote governance")
	}
}

func TestSkillsSavedSelectionCannotEnableGovernance(t *testing.T) {
	root, _ := writeInstallationFixture(t)
	b := installationFixture()
	old := &Plan{SchemaVersion: 4, ProtocolVersion: 1, Root: root, Profile: "spec", Command: "skills", Installation: InstallationSkills,
		TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, Selection: []string{skillRegistryRef}}
	old.Digest = planDigest(old)
	file := filepath.Join(t.TempDir(), "legacy-plan.json")
	raw, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, raw, 0644); err != nil {
		t.Fatal(err)
	}
	saved, err := ReadPlan(file)
	if err != nil {
		t.Fatal(err)
	}
	_, err = buildCore(saved.Root, saved.Profile, saved.Command, saved.Variables, saved.Selection, saved.Binding, PlanningOptions{target: b})
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "GOVERNED_REQUIRED" {
		t.Fatalf("saved selection replay: %v", err)
	}
}

func TestSkillSelectionTypedDependencies(t *testing.T) {
	for _, tc := range []struct {
		roots []string
		when  string
		want  []string
	}{
		{[]string{"r"}, "", []string{"required", "root"}},
		{[]string{"root"}, "x", []string{"required", "conditional", "root"}},
		{[]string{"r", "root"}, "x,x", []string{"required", "conditional", "root"}},
	} {
		got, err := resolveSkillSelection(selectionFixture(t), tc.roots, tc.when)
		if err != nil || !reflect.DeepEqual(got.CanonicalIDs, tc.want) {
			t.Fatalf("%+v: %+v %v", tc, got, err)
		}
		want := []SkillDependency{{From: "root", Skill: "required", Type: "context-required"}}
		if tc.when != "" {
			want = append(want, SkillDependency{From: "root", Skill: "conditional", Type: "context-conditional", When: "x"})
			if !reflect.DeepEqual(got.When, []string{"x"}) {
				t.Fatalf("conditions: %v", got.When)
			}
		}
		if !reflect.DeepEqual(got.Dependencies, want) {
			t.Fatalf("dependencies: %+v", got.Dependencies)
		}
	}
	for _, tc := range []struct{ root, when string }{{"root", "unknown"}, {"missing", ""}, {"deprecated", ""}, {"old", ""}} {
		if _, err := resolveSkillSelection(selectionFixture(t), []string{tc.root}, tc.when); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	b := selectionFixture(t)
	raw, _ := b.Files[skillRegistryRef].Render(nil)
	raw = append(raw, []byte("  required:\n    - {skill: root, type: context-required}\n")...)
	f := b.Files[skillRegistryRef]
	f.Data = base64.StdEncoding.EncodeToString(raw)
	f.Digest = safefs.Digest(raw)
	b.Files[skillRegistryRef] = f
	got, err := resolveSkillSelection(b, []string{"root"}, "")
	if err != nil || !reflect.DeepEqual(got.CanonicalIDs, []string{"required", "root"}) {
		t.Fatalf("cycle: %+v %v", got, err)
	}
}

func TestSelectSkillResourcesStandalone(t *testing.T) {
	b := selectionFixture(t)
	got, err := SelectSkillResources(b, InstallationSkills, []string{"r"}, []string{"claude", "workbuddy"})
	want := []string{
		".agents/skills/required/SKILL.md", ".agents/skills/required/references/guide.md", ".agents/skills/root/SKILL.md", ".agents/skills/root/references/guide.md",
		".claude/skills/required/SKILL.md", ".claude/skills/required/references/guide.md", ".claude/skills/root/SKILL.md", ".claude/skills/root/references/guide.md",
		".workbuddy/skills/required/SKILL.md", ".workbuddy/skills/required/references/guide.md", ".workbuddy/skills/root/SKILL.md", ".workbuddy/skills/root/references/guide.md",
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("paths: %v %v", got, err)
	}
	got, err = SelectSkillResources(b, InstallationSkills, []string{"root"}, nil)
	if err != nil || !reflect.DeepEqual(got, want[:4]) {
		t.Fatalf("canonical-only: %v %v", got, err)
	}
	for _, runtime := range []string{"unknown", "codex"} {
		if _, err = SelectSkillResources(b, InstallationSkills, []string{"root"}, []string{runtime}); err == nil {
			t.Fatalf("accepted unshipped runtime %s", runtime)
		}
	}
	delete(b.Files, ".workbuddy/skills/required/SKILL.md")
	if _, err = SelectSkillResources(b, InstallationSkills, []string{"root"}, []string{"workbuddy"}); err == nil {
		t.Fatal("accepted missing projection entry")
	}
	b = selectionFixture(t)
	got, err = SelectSkillResources(b, InstallationGoverned, []string{"root"}, nil)
	if err != nil || !strings.Contains(strings.Join(got, "\n"), ".template-spec/Common.yaml") || !strings.Contains(strings.Join(got, "\n"), "scripts/step.sh") {
		t.Fatalf("governed closure: %v %v", got, err)
	}
	b.SkillRequirements["root"] = bundle.Requirement{UnsupportedReason: "fixture unavailable"}
	for _, kind := range []InstallationKind{InstallationSkills, InstallationGoverned} {
		if _, err = SelectSkillResources(b, kind, []string{"root"}, nil); err == nil {
			t.Fatalf("accepted unsupported skill for %s", kind)
		}
	}
}

func standaloneFixture(t *testing.T) *bundle.Bundle {
	t.Helper()
	b := installationFixture()
	file := func(raw []byte) bundle.File {
		return bundle.File{Data: base64.StdEncoding.EncodeToString(raw), Digest: safefs.Digest(raw), Mode: 0644, Ownership: "managed"}
	}
	registry, _ := b.Files[skillRegistryRef].Render(nil)
	registry = append(registry, []byte("  - {id: probe, layer: core, maturity: supported}\n  - {id: extra, layer: core, maturity: supported}\nskill_dependencies:\n  probe:\n    - {skill: implement, type: context-required}\n")...)
	b.Files[skillRegistryRef] = file(registry)
	shared := map[string]any{}
	for _, name := range []string{"implement", "probe", "extra"} {
		shared[name] = map[string]any{"skillPath": ".agents/skills/" + name, "targets": []string{".agents/skills", ".claude/skills", ".workbuddy/skills"}}
		for _, root := range []string{".agents/skills/", ".claude/skills/", ".workbuddy/skills/"} {
			b.Files[root+name+"/SKILL.md"] = file([]byte("---\nname: " + name + "\ndescription: Fixture\n---\n"))
			b.Files[root+name+"/references/guide.md"] = file([]byte(name + " guidance\n"))
		}
	}
	raw, _ := json.Marshal(map[string]any{"version": 3, "canonicalRoot": ".agents/skills", "projectionRoots": []string{".claude/skills", ".workbuddy/skills"}, "skills": map[string]any{"shared": shared, "platform": map[string]any{}}})
	b.Files["skills-lock.json"] = file(raw)
	item := b.Installations["skills"]
	item.Distribution["runtimes"] = []string{"claude", "workbuddy"}
	for ref, f := range b.Files {
		if strings.Contains(ref, "/implement/") {
			item.Initial[ref] = f
		}
	}
	b.Installations["skills"] = item
	b.SkillRequirements = map[string]bundle.Requirement{"probe": {Paths: []string{skillRegistryRef}, Skills: []string{"implement"}}}
	return b
}

func applyStandaloneFixture(t *testing.T, p *Plan, b *bundle.Bundle) error {
	t.Helper()
	ops, err := nativePlanOperations(p)
	if err != nil {
		return err
	}
	_, err = transaction.ApplyContextWithValidation(context.Background(), p.Root, p.Command, ops, p.Inputs, nil, func() error { return verifyAppliedPlan(context.Background(), p, b) })
	return err
}

func TestSkillsEnsureStandaloneClosure(t *testing.T) {
	b := standaloneFixture(t)
	root, _ := writeInstallationBundleFixture(t, b)
	refs, err := SelectSkillResources(b, InstallationSkills, []string{"probe"}, []string{"claude", "workbuddy"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := buildCore(root, "spec", "skills", nil, refs, nil, PlanningOptions{target: b})
	if err != nil {
		t.Fatal(err)
	}
	if !p.ReadyToApply {
		t.Fatalf("blocked: %+v", p.Blockers)
	}
	if err = applyStandaloneFixture(t, p, b); err != nil {
		t.Fatal(err)
	}
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, runtime := range []string{"", "claude", "workbuddy"} {
		got, err := ResolveSkills(id, b, []string{"probe"}, runtime, "")
		if err != nil || got.Status != "ready" || len(got.Missing) > 0 || len(got.Issues) > 0 {
			t.Fatalf("%s: %+v %v", runtime, got, err)
		}
	}
	for _, ref := range []string{"yss-project.yaml", "CONTEXT.md", ".template-spec", "scripts"} {
		if _, err := os.Stat(filepath.Join(root, ref)); !os.IsNotExist(err) {
			t.Fatalf("introduced governance: %s", ref)
		}
	}
}
