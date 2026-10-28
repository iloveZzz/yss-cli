package bundle

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestBundleInstallationViews(t *testing.T) {
	b := &Bundle{SchemaVersion: 5, Files: map[string]File{"extra": {}}, Installations: map[string]Installation{
		"skills":   {Initial: map[string]File{"AGENTS.md": encodedFile([]byte("local engineering"), 0644, "managed")}, Distribution: map[string]any{"installation": "skills"}},
		"governed": {Initial: map[string]File{"yss-project.yaml": encodedFile([]byte("identity"), 0644, "managed")}, Distribution: map[string]any{"installation": "governed"}},
	}}
	view, err := b.ForInstallation("skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Initial) != 1 || view.Distribution["installation"] != "skills" {
		t.Fatal("wrong selection")
	}
	delete(view.Initial, "AGENTS.md")
	view.Distribution["installation"] = "changed"
	if len(b.Installations["skills"].Initial) != 1 || b.Installations["skills"].Distribution["installation"] != "skills" {
		t.Fatal("view mutated bundle")
	}
	if _, err = b.ForInstallation("unknown"); err == nil {
		t.Fatal("accepted unknown installation")
	}
}
func TestInstallationRejectsBadReferences(t *testing.T) {
	for _, ref := range []string{"../escape", "/absolute"} {
		b := &Bundle{SchemaVersion: 5, Installations: map[string]Installation{"skills": {Initial: map[string]File{ref: encodedFile([]byte("x"), 0644, "managed")}}}}
		if _, err := b.ForInstallation("skills"); err == nil {
			t.Fatalf("accepted %s", ref)
		}
	}
}
func TestOldBundleOnlySupportsGoverned(t *testing.T) {
	b := &Bundle{SchemaVersion: 4, Initial: map[string]File{"x": {}}, Distribution: map[string]any{}}
	if _, err := b.ForInstallation("skills"); err == nil {
		t.Fatal("old bundle guessed skills")
	}
	if _, err := b.ForInstallation("governed"); err != nil {
		t.Fatal(err)
	}
}

func TestInstallationArchiveBindsSelection(t *testing.T) {
	b := &Bundle{Installations: map[string]Installation{"skills": {Distribution: map[string]any{"installation": "skills"}}}}
	before := Hash(b)
	b.Installations["skills"].Distribution["installation"] = "governed"
	if Hash(b) == before {
		t.Fatal("archive hash omits installation selection")
	}
}

func TestGovernedInstallationReusesRootInitial(t *testing.T) {
	b := &Bundle{SchemaVersion: 5, Initial: map[string]File{"AGENTS.md": encodedFile([]byte("formal"), 0644, "managed")}, Installations: map[string]Installation{"governed": {Distribution: map[string]any{"installation": "governed"}}}}
	view, err := b.ForInstallation("governed")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Initial) != 1 {
		t.Fatal("lost original governed collection")
	}
	delete(view.Initial, "AGENTS.md")
	if len(b.Initial) != 1 {
		t.Fatal("mutated governed collection")
	}
}

func TestGovernedViewDoesNotInstallSkillOnlySourceReferences(t *testing.T) {
	source := ".template-source/process/reference.md"
	file := encodedFile([]byte("reference"), 0644, "managed")
	b := &Bundle{SchemaVersion: 5, Files: map[string]File{source: file, ".template-spec/process/reference.md": file, ".agents/skills/review/SKILL.md": file}, Initial: map[string]File{source: file}, Installations: map[string]Installation{
		"skills":   {Initial: map[string]File{source: file}, Distribution: map[string]any{"installation": "skills"}, SkillReferences: map[string][]string{"review": {source}}},
		"governed": {Distribution: map[string]any{"installation": "governed"}},
	}}
	governed, err := b.ForInstallation("governed")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := governed.Files[source]; ok {
		t.Error("governed full selection exposes a skills-only source asset")
	}
	if _, ok := governed.Initial[source]; ok {
		t.Error("governed initial selection exposes a skills-only source asset")
	}
	if _, ok := b.Files[source]; !ok {
		t.Error("filter mutated source bundle")
	}
	skills, err := b.ForInstallation("skills")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := skills.Files[source]; !ok {
		t.Error("skills view lost its source reference")
	}
}

func TestBuildInstallationsAddsDeclaredSkillReferences(t *testing.T) {
	file := func(text string) File { return encodedFile([]byte(text), 0644, "managed") }
	b := &Bundle{Profile: "spec", Files: map[string]File{
		".agents/skills/root/SKILL.md":          file("---\nname: root\ndescription: Fixture\n---\n"),
		".template-spec/process/reference.md":   file("spec"),
		".template-source/process/reference.md": file("source"),
		"skills-lock.json":                      file(`{"version":3,"skills":{"shared":{"root":{"skillPath":".agents/skills/root","targets":[".agents/skills"]}}}}`),
	}, Initial: map[string]File{}, Distribution: map[string]any{"runtimes": []string{}}}
	policy := Policy{Manifest: map[string]any{"allowFiles": []string{".template-source/process/reference.md"}}, Installations: map[string]InstallationPolicy{"governed": {}, "skills": {Skills: []string{"root"}, SkillReferences: map[string][]string{"root": {".template-spec/process/reference.md", ".template-source/process/reference.md"}}}}}
	if err := buildInstallations(context.Background(), b, policy, Source{}, "", ""); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{".template-spec/process/reference.md", ".template-source/process/reference.md"} {
		if _, ok := b.Installations["skills"].Initial[ref]; !ok {
			t.Errorf("default installation missing reference %s", ref)
		}
	}
}

func TestBuildInstallationsRejectsUnsafeOrMissingSkillReference(t *testing.T) {
	for _, ref := range []string{"../escape", "scripts/arbitrary.sh", ".template-source/private.txt", ".template-spec/missing.md"} {
		t.Run(ref, func(t *testing.T) {
			f := encodedFile([]byte("data"), 0644, "managed")
			b := &Bundle{Files: map[string]File{".agents/skills/root/SKILL.md": f, ".template-source/private.txt": f}, Distribution: map[string]any{}}
			policy := Policy{Installations: map[string]InstallationPolicy{"governed": {}, "skills": {SkillReferences: map[string][]string{"root": {ref}}}}}
			if err := buildInstallations(context.Background(), b, policy, Source{}, "", ""); err == nil {
				t.Fatalf("accepted reference %q", ref)
			}
		})
	}
}

func TestInstallationViewRejectsTamperedReferences(t *testing.T) {
	ref := ".template-spec/process/reference.md"
	good := encodedFile([]byte("source"), 0644, "managed")
	b := &Bundle{SchemaVersion: 5, Files: map[string]File{ref: good, ".agents/skills/root/SKILL.md": good}, Installations: map[string]Installation{"skills": {Initial: map[string]File{}, Distribution: map[string]any{"installation": "skills"}, SkillReferences: map[string][]string{"root": {ref}}}}}
	if _, err := b.ForInstallation("skills"); err != nil {
		t.Fatalf("valid reference rejected: %v", err)
	}
	item := b.Installations["skills"]
	item.Initial[ref] = encodedFile([]byte("different"), 0644, "managed")
	b.Installations["skills"] = item
	if _, err := b.ForInstallation("skills"); err == nil {
		t.Fatal("accepted inconsistent initial reference bytes")
	}
	delete(item.Initial, ref)
	delete(b.Files, ref)
	if _, err := b.ForInstallation("skills"); err == nil {
		t.Fatal("accepted missing referenced asset")
	}
}

func TestBuildInstallationsRejectsUndeclaredSourceAllowlistEntry(t *testing.T) {
	for _, ref := range []string{".template-source", ".template-source/private.md"} {
		f := encodedFile([]byte("data"), 0644, "managed")
		b := &Bundle{Files: map[string]File{".template-source/private.md": f}, Distribution: map[string]any{}}
		p := Policy{Manifest: map[string]any{"allowFiles": []string{ref}}, Installations: map[string]InstallationPolicy{"skills": {}, "governed": {}}}
		if err := buildInstallations(context.Background(), b, p, Source{}, "", ""); err == nil || !strings.Contains(err.Error(), "no skill reference") {
			t.Fatalf("unmapped allowlist entry %q: %v", ref, err)
		}
	}
}

func TestBuildInstallationsRejectsProtectedSourceReference(t *testing.T) {
	file := func(text, own string) File { return encodedFile([]byte(text), 0644, own) }
	ref := ".template-source/process/reference.md"
	b := &Bundle{Files: map[string]File{".agents/skills/root/SKILL.md": file("skill", "managed"), ref: file("source", "protected")}, Distribution: map[string]any{}}
	p := Policy{Manifest: map[string]any{"allowFiles": []string{ref}}, Installations: map[string]InstallationPolicy{"skills": {SkillReferences: map[string][]string{"root": {ref}}}, "governed": {}}}
	if err := buildInstallations(context.Background(), b, p, Source{}, "", ""); err == nil || !strings.Contains(err.Error(), "managed") {
		t.Fatalf("protected reference accepted: %v", err)
	}
}

func TestInstallationRejectsInvalidReferenceGroups(t *testing.T) {
	for _, tc := range []struct{ name, data string }{
		{"unknown", `{"referenceGroups":{},"skillReferenceGroups":{"root":["absent"]}}`},
		{"unused", `{"referenceGroups":{"leftover":[".template-spec/a.md"]}}`},
		{"unsafe-name", `{"referenceGroups":{"bad/name":[".template-spec/a.md"]},"skillReferenceGroups":{"root":["bad/name"]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := encodedFile([]byte("x"), 0644, "managed")
			item := Installation{Initial: map[string]File{}, Distribution: map[string]any{"installation": "skills"}}
			if err := json.Unmarshal([]byte(tc.data), &item); err != nil {
				t.Fatal(err)
			}
			b := &Bundle{SchemaVersion: 5, Files: map[string]File{".agents/skills/root/SKILL.md": f, ".template-spec/a.md": f}, Installations: map[string]Installation{"skills": item}}
			if _, err := b.ForInstallation("skills"); err == nil {
				t.Fatal("accepted invalid group")
			}
		})
	}
}

func TestBuildInstallationsIncludesDefaultSkillGroupAndBindsHash(t *testing.T) {
	file := func(text string) File { return encodedFile([]byte(text), 0644, "managed") }
	ref := ".template-source/process/reference.md"
	b := &Bundle{Profile: "spec", Files: map[string]File{".agents/skills/root/SKILL.md": file("---\nname: root\ndescription: Fixture\n---\n"), ref: file("reference"), "skills-lock.json": file(`{"version":3,"skills":{"shared":{"root":{"skillPath":".agents/skills/root","targets":[".agents/skills"]}}}}`)}, Initial: map[string]File{}, Distribution: map[string]any{"runtimes": []string{}}}
	p := Policy{Manifest: map[string]any{"allowFiles": []string{ref}}, Installations: map[string]InstallationPolicy{"governed": {}, "skills": {Skills: []string{"root"}, ReferenceGroups: map[string][]string{"shared": {ref}}, SkillReferenceGroups: map[string][]string{"root": {"shared"}}}}}
	if err := buildInstallations(context.Background(), b, p, Source{}, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Installations["skills"].Initial[ref]; !ok {
		t.Fatal("initial reference group missing")
	}
	before := Hash(b)
	item := b.Installations["skills"]
	item.ReferenceGroups["shared"] = nil
	b.Installations["skills"] = item
	if Hash(b) == before {
		t.Fatal("bundle hash does not bind reference groups")
	}
	if _, err := b.ForInstallation("skills"); err == nil {
		t.Fatal("accepted tampered group")
	}
}
