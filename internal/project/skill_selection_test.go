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

func TestGovernedSkillSelectionTypedClosure(t *testing.T) {
	b := selectionFixture(t)
	b.SkillRequirements["root"] = bundle.Requirement{Paths: []string{".template-spec/Common.yaml", "scripts/step.sh"}}
	b.SkillRequirements["required"] = bundle.Requirement{Skills: []string{"component"}}
	f := b.Files[skillRegistryRef]
	raw, _ := f.Render(nil)
	raw = append(raw, []byte("  required:\n    - {skill: conditional, type: context-required}\n  component:\n    - {skill: coordination, type: context-required}\n")...)
	f.Data, f.Digest = base64.StdEncoding.EncodeToString(raw), safefs.Digest(raw)
	b.Files[skillRegistryRef] = f
	refs, err := SelectSkillResources(b, InstallationGoverned, []string{"root"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	selected := strings.Join(refs, "\n")
	for _, skill := range []string{"root", "required", "conditional", "component", "coordination"} {
		if !strings.Contains(selected, ".agents/skills/"+skill+"/SKILL.md") {
			t.Fatalf("governed closure omitted required skill %s: %v", skill, refs)
		}
	}
	if strings.Contains(selected, "/review/") || !strings.Contains(selected, "scripts/step.sh") {
		t.Fatalf("optional or governance resource selection changed: %v", refs)
	}
	for _, invalid := range []string{"missing-registry", "unsupported-required"} {
		b = selectionFixture(t)
		if invalid == "missing-registry" {
			delete(b.Files, skillRegistryRef)
		} else {
			b.SkillRequirements["required"] = bundle.Requirement{UnsupportedReason: "required fixture unavailable"}
		}
		if _, err := SelectSkillResources(b, InstallationGoverned, []string{"root"}, nil); err == nil {
			t.Fatalf("governed selection accepted %s", invalid)
		}
	}
	for _, version := range []int{2, 3, 4} {
		b = selectionFixture(t)
		b.SchemaVersion = version
		delete(b.Files, skillRegistryRef)
		if _, err := SelectSkillResources(b, InstallationGoverned, []string{"root"}, nil); err != nil {
			t.Fatalf("legacy bundle %d: %v", version, err)
		}
	}
}

func governedScaffoldFixture(t *testing.T, skill string) *bundle.Bundle {
	t.Helper()
	b := standaloneFixture(t)
	files := map[string]bundle.File{}
	for ref, f := range b.Files {
		raw, _ := f.Render(nil)
		for _, change := range [][2]string{{"probe", skill}, {"extra", "alibaba-java-code-style"}} {
			ref = strings.ReplaceAll(ref, change[0], change[1])
			raw = []byte(strings.ReplaceAll(string(raw), change[0], change[1]))
		}
		if ref == skillRegistryRef {
			raw = []byte(strings.ReplaceAll(string(raw), "skill: implement, type: context-required", "skill: alibaba-java-code-style, type: context-required"))
		}
		f.Data, f.Digest = base64.StdEncoding.EncodeToString(raw), safefs.Digest(raw)
		files[ref] = f
	}
	put := func(ref, raw string) {
		files[ref] = bundle.File{Data: base64.StdEncoding.EncodeToString([]byte(raw)), Digest: safefs.Digest([]byte(raw)), Mode: 0644, Ownership: "managed"}
	}
	put("yss-project.yaml", "schema_version: 2\nrepository_mode: project-instance\n")
	put(".template-spec/process/harness-profile.yaml", "schema_version: 3\nprofile_id: harness.spec-template\n")
	put("CONTEXT.md", "---\ncontext_schema_version: 1\n---\n## 流程术语\n|术语|含义|英文标识|避免 / 备注|\n|---|---|---|---|\n## 业务术语\n|术语|含义|英文标识|适用业务责任区|避免 / 备注|\n|---|---|---|---|---|\n")
	b.Files = files
	b.SkillRequirements = map[string]bundle.Requirement{skill: {Paths: []string{skillRegistryRef}, Skills: []string{"implement"}}}
	initial := map[string]bundle.File{}
	for _, ref := range []string{"AGENTS.md", "yss-project.yaml", "CONTEXT.md", ".template-spec/process/harness-profile.yaml", skillRegistryRef, "skills-lock.json", "scripts/lib/skill-supply-chain.mjs"} {
		initial[ref] = files[ref]
	}
	for ref, f := range files {
		if strings.Contains(ref, "/implement/") {
			initial[ref] = f
		}
	}
	b.Installations["governed"] = bundle.Installation{Initial: initial, Distribution: map[string]any{"installation": "governed", "mode": "selected", "installedSkills": []string{"implement"}, "runtimes": []string{"claude", "workbuddy"}}}
	return b
}

func TestGovernedSkillEnsureScaffoldClosure(t *testing.T) {
	for _, skill := range []string{"yss-ddd-scaffold-generator", "yss-layered-mvc-scaffold-generator"} {
		t.Run(skill, func(t *testing.T) {
			b := governedScaffoldFixture(t, skill)
			root := t.TempDir()
			init, err := buildCore(root, "spec", "init", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
			if err != nil {
				t.Fatal(err)
			}
			if err := applyStandaloneFixture(t, init, b); err != nil {
				t.Fatal(err)
			}
			refs, err := SelectSkillResources(b, InstallationGoverned, []string{skill}, nil)
			if err != nil {
				t.Fatal(err)
			}
			p, err := buildCore(root, "spec", "skills", nil, refs, nil, PlanningOptions{target: b})
			if err != nil || !p.ReadyToApply {
				t.Fatalf("plan: %+v %v", p, err)
			}
			if err := applyStandaloneFixture(t, p, b); err != nil {
				t.Fatal(err)
			}
			id, err := Detect(root, "spec", false)
			if err != nil || skillsIdentity(id) {
				t.Fatalf("governed identity changed: %+v %v", id, err)
			}
			for _, runtime := range []string{"", "claude", "workbuddy"} {
				got, err := ResolveSkills(id, b, []string{skill}, runtime, "")
				if err != nil || got.Status != "ready" || len(got.Missing) != 0 || len(got.Issues) != 0 {
					t.Fatalf("%s: %+v %v", runtime, got, err)
				}
			}
		})
	}
}

func TestGovernedSkillEnsureOmittedDependencyRollsBack(t *testing.T) {
	const skill = "yss-ddd-scaffold-generator"
	b := governedScaffoldFixture(t, skill)
	root := t.TempDir()
	init, err := buildCore(root, "spec", "init", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
	if err != nil {
		t.Fatal(err)
	}
	if err := applyStandaloneFixture(t, init, b); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "user-note.txt"), []byte("preserve user work"), 0600); err != nil {
		t.Fatal(err)
	}
	refs, err := SelectSkillResources(b, InstallationGoverned, []string{skill}, nil)
	if err != nil {
		t.Fatal(err)
	}
	omitted := []string{}
	for _, ref := range refs {
		if !strings.Contains(ref, "/alibaba-java-code-style/") {
			omitted = append(omitted, ref)
		}
	}
	p, err := buildCore(root, "spec", "skills", nil, omitted, nil, PlanningOptions{target: b})
	if err != nil {
		t.Fatal(err)
	}
	observed := func() map[string]domain.Descriptor {
		files := recoveryTree(t, root)
		for ref := range files {
			if strings.HasPrefix(filepath.ToSlash(ref), ".yss/") {
				delete(files, ref)
			}
		}
		return files
	}
	before := observed()
	err = applyStandaloneFixture(t, p, b)
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "VERIFY" {
		t.Fatalf("omitted dependency did not fail VERIFY: %v", err)
	}
	if !reflect.DeepEqual(before, observed()) {
		t.Fatal("failed application did not restore files, metadata, user bytes and modes")
	}
	status, err := transaction.Status(root)
	if err != nil || len(status.Pending) != 0 || len(status.Preparations) != 0 {
		t.Fatalf("failed application left pending work: %+v %v", status, err)
	}
	recovered := false
	for _, item := range status.Transactions {
		recovered = recovered || item.Kind == "skills" && item.Phase == "recovered"
	}
	if !recovered {
		t.Fatalf("missing recovered transaction: %+v", status)
	}
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	missing, err := ResolveSkills(id, b, []string{skill}, "", "")
	if err != nil || missing.Status != "missing" || len(missing.Missing) == 0 {
		t.Fatalf("failed installation was falsely ready: %+v %v", missing, err)
	}
	if !reflect.DeepEqual(before, observed()) {
		t.Fatal("missing dependency query wrote files")
	}
}

func TestStandaloneSkillReferencesIncludeTransitiveSelectedSkills(t *testing.T) {
	b := selectionFixture(t)
	b.Files[".template-source/process/reference.md"] = b.Files[".template-spec/Common.yaml"]
	var installation bundle.Installation
	if err := json.Unmarshal([]byte(`{"skillReferences":{"root":[".template-spec/Common.yaml"],"required":[".template-source/process/reference.md", "scripts/step.sh"]}}`), &installation); err != nil {
		t.Fatal(err)
	}
	b.Installations = map[string]bundle.Installation{"skills": installation}
	got, err := SelectSkillResources(b, InstallationSkills, []string{"root"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{".template-spec/Common.yaml", ".template-source/process/reference.md", "scripts/step.sh"} {
		if !containsString(got, ref) {
			t.Errorf("selected standalone skill missing reference %s: %v", ref, got)
		}
	}
	if err := validateSkillsSelection(b, got, nil); err != nil {
		t.Fatalf("declared references must be allowed: %v", err)
	}
	delete(b.Files, ".template-source/process/reference.md")
	if _, err := SelectSkillResources(b, InstallationSkills, []string{"root"}, nil); err == nil {
		t.Fatal("missing referenced file should prevent a ready selection")
	}
}

func TestStandaloneSkillReferencesRejectUnregisteredPaths(t *testing.T) {
	b := selectionFixture(t)
	var installation bundle.Installation
	if err := json.Unmarshal([]byte(`{"skillReferences":{"root":[".template-spec/Common.yaml"]}}`), &installation); err != nil {
		t.Fatal(err)
	}
	b.Installations = map[string]bundle.Installation{"skills": installation}
	if err := validateSkillsSelection(b, []string{".template-spec/Common.yaml"}, nil); err != nil {
		t.Fatalf("declared reference rejected: %v", err)
	}
	if err := validateSkillsSelection(b, []string{"scripts/step.sh"}, nil); err == nil {
		t.Fatal("unregistered governance resource was selected")
	}
}

func TestStandaloneSkillResolutionDetectsMissingReference(t *testing.T) {
	b := standaloneFixture(t)
	b.Files[".template-spec/reference.md"] = b.Files[".agents/skills/probe/references/guide.md"]
	entry := b.Installations["skills"]
	raw, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["skillReferences"] = json.RawMessage(`{"probe":[".template-spec/reference.md"]}`)
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &entry); err != nil {
		t.Fatal(err)
	}
	b.Installations["skills"] = entry
	root, _ := writeInstallationBundleFixture(t, b)
	refs, err := SelectSkillResources(b, InstallationSkills, []string{"probe"}, []string{"claude", "workbuddy"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := buildCore(root, "spec", "skills", nil, refs, nil, PlanningOptions{target: b})
	if err != nil || !p.ReadyToApply {
		t.Fatalf("plan: %+v %v", p, err)
	}
	if err = applyStandaloneFixture(t, p, b); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(root, ".template-spec/reference.md")); err != nil {
		t.Fatal(err)
	}
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	result, err := ResolveSkills(id, b, []string{"probe"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status == "ready" {
		t.Fatalf("missing declared reference was reported ready: %+v", result)
	}
}

func TestStandaloneReferenceReadinessAcceptsRenderedTracker(t *testing.T) {
	for _, tracker := range []string{"", "gitlab"} {
		t.Run("tracker="+tracker, func(t *testing.T) {
			b := standaloneFixture(t)
			ref := ".template-spec/agents/issue-tracker.md"
			data := []byte("---\ntracker:\n  platform: local-markdown\n  root: .work\n---\n| `platform` | `local-markdown` |\n")
			b.Files[ref] = bundle.File{Data: base64.StdEncoding.EncodeToString(data), Digest: safefs.Digest(data), Mode: 0644, Ownership: "managed"}
			item := b.Installations["skills"]
			item.Initial[ref] = b.Files[ref]
			item.SkillReferences = map[string][]string{"implement": {ref}}
			b.Installations["skills"] = item
			b.BundleHash = strings.Repeat("a", 64)
			root := t.TempDir()
			p, err := buildCore(root, "spec", "init", map[string]string{"issueTracker": tracker}, nil, nil, PlanningOptions{target: b, Installation: InstallationSkills})
			if err != nil || !p.ReadyToApply {
				t.Fatalf("init plan: %+v %v", p, err)
			}
			if err = applyStandaloneFixture(t, p, b); err != nil {
				t.Fatal(err)
			}
			id, err := Detect(root, "spec", false)
			if err != nil {
				t.Fatal(err)
			}
			result, err := ResolveSkills(id, b, []string{"implement"}, "", "")
			if err != nil || result.Status != "ready" {
				t.Fatalf("valid rendered reference not ready: %+v %v", result, err)
			}
			syncPlan, err := buildCore(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
			if err != nil {
				t.Fatal(err)
			}
			for _, change := range syncPlan.Changes {
				if change.Path == ref {
					t.Fatalf("sync unexpectedly rewrites unchanged reference: %+v", change)
				}
			}
			previous := id.Native.Managed[ref]
			originalSource := *previous.Source
			wrongSource := originalSource
			wrongSource.BundleHash = strings.Repeat("b", 64)
			previous.Source = &wrongSource
			id.Native.Managed[ref] = previous
			result, err = ResolveSkills(id, b, []string{"implement"}, "", "")
			if err != nil || result.Status == "ready" {
				t.Fatalf("wrong bundle source falsely ready: %+v %v", result, err)
			}
			previous.Source = &originalSource
			id.Native.Managed[ref] = previous
			if err = os.WriteFile(filepath.Join(root, ref), []byte("locally changed"), 0644); err != nil {
				t.Fatal(err)
			}
			local, err := safefs.Describe(root, ref)
			if err != nil {
				t.Fatal(err)
			}
			previous = id.Native.Managed[ref]
			previous.Applied = local
			id.Native.Managed[ref] = previous
			result, err = ResolveSkills(id, b, []string{"implement"}, "", "")
			if err != nil || result.Status == "ready" {
				t.Fatalf("preserved local change falsely ready: %+v %v", result, err)
			}
		})
	}
}

func TestStandaloneSkillReferenceReadinessAcceptsNativeProfile(t *testing.T) {
	b := standaloneFixture(t)
	ref := ".template-spec/process/harness-profile.yaml"
	data := []byte("schema_version: 2\nprofile_id: harness.spec-template\ninstantiation:\n  cli_package: legacy\n")
	b.Files[ref] = bundle.File{Data: base64.StdEncoding.EncodeToString(data), Digest: safefs.Digest(data), Mode: 0644, Ownership: "managed"}
	item := b.Installations["skills"]
	item.SkillReferences = map[string][]string{"probe": {ref}}
	b.Installations["skills"] = item
	root, _ := writeInstallationBundleFixture(t, b)
	refs, err := SelectSkillResources(b, InstallationSkills, []string{"probe"}, []string{"claude", "workbuddy"})
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
	id, err := Detect(root, "spec", false)
	if err != nil || !skillsIdentity(id) {
		t.Fatalf("identity: %+v %v", id, err)
	}
	result, err := ResolveSkills(id, b, []string{"probe"}, "", "")
	if err != nil || result.Status != "ready" {
		t.Fatalf("native profile reference not ready: %+v %v", result, err)
	}
}

func TestStandaloneSkillReferenceGroupsFollowSelectedClosure(t *testing.T) {
	b := selectionFixture(t)
	b.Files[".template-spec/shared.yaml"] = b.Files[".template-spec/Common.yaml"]
	b.Files[".template-spec/unselected.yaml"] = b.Files[".template-spec/Common.yaml"]
	var installation bundle.Installation
	if err := json.Unmarshal([]byte(`{"referenceGroups":{"common":[".template-spec/shared.yaml"],"unrelated":[".template-spec/unselected.yaml"]},"skillReferenceGroups":{"required":["common"],"review":["unrelated"]}}`), &installation); err != nil {
		t.Fatal(err)
	}
	b.Installations = map[string]bundle.Installation{"skills": installation}
	got, err := SelectSkillResources(b, InstallationSkills, []string{"root"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(got, ".template-spec/shared.yaml") {
		t.Fatalf("missing selected group: %v", got)
	}
	if containsString(got, ".template-spec/unselected.yaml") {
		t.Fatalf("installed unselected group: %v", got)
	}
	if err = validateSkillsSelection(b, got, nil); err != nil {
		t.Fatalf("selected group rejected: %v", err)
	}
}
