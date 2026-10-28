package project

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func referenceMigrationFixture(t *testing.T) (string, *bundle.Bundle, string) {
	t.Helper()
	b := governedScaffoldFixture(t, "probe")
	ref := ".template-source/process/reference.md"
	f := b.Files["AGENTS.md"]
	b.Files[ref] = f
	governed := b.Installations["governed"]
	for path, raw := range map[string]string{
		"yss-project.yaml": "schema_version: 2\nrepository_mode: project-instance\n",
		".template-spec/process/harness-profile.yaml": "schema_version: 3\nprofile_id: harness.spec-template\n",
		"CONTEXT.md": "---\ncontext_schema_version: 1\n---\n## 流程术语\n|术语|含义|英文标识|避免 / 备注|\n|---|---|---|---|\n## 业务术语\n|术语|含义|英文标识|适用业务责任区|避免 / 备注|\n|---|---|---|---|---|\n",
	} {
		data := []byte(raw)
		asset := bundle.File{Data: base64.StdEncoding.EncodeToString(data), Digest: safefs.Digest(data), Mode: 0644, Ownership: "managed"}
		b.Files[path], governed.Initial[path] = asset, asset
	}
	b.Installations["governed"] = governed
	skills := b.Installations["skills"]
	skills.Initial[ref] = f
	skills.SkillReferences = map[string][]string{"implement": {ref}}
	b.Installations["skills"] = skills
	root, _ := writeInstallationBundleFixture(t, b)
	return root, b, ref
}

func TestSkillsMigrationRetainsVerifiedSourceReferences(t *testing.T) {
	root, b, ref := referenceMigrationFixture(t)
	p, err := buildCore(root, "spec", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %v", p.Conflicts)
	}
	for _, c := range p.Changes {
		if c.Path == ref {
			t.Fatalf("migration must preserve verified reference: %+v", c)
		}
		if c.Path == MetadataFile {
			data, e := base64.StdEncoding.DecodeString(c.Data)
			if e != nil {
				t.Fatal(e)
			}
			var meta Metadata
			if e = json.Unmarshal(data, &meta); e != nil {
				t.Fatal(e)
			}
			got := stringsOf(meta.Distribution["retainedTemplateSourceReferences"])
			if len(got) != 1 || got[0] != ref {
				t.Fatalf("missing retained source reference declaration: %v", got)
			}
			return
		}
	}
	t.Fatal("migration did not produce metadata")
}

func TestSkillsMigrationRejectsUntrustedSourceFiles(t *testing.T) {
	for _, scenario := range []string{"modified", "unmanaged", "symlink"} {
		t.Run(scenario, func(t *testing.T) {
			root, b, ref := referenceMigrationFixture(t)
			file := filepath.Join(root, filepath.FromSlash(ref))
			switch scenario {
			case "modified":
				if e := os.WriteFile(file, []byte("user changes"), 0644); e != nil {
					t.Fatal(e)
				}
			case "unmanaged":
				file = filepath.Join(root, ".template-source/process/user.txt")
				if e := os.WriteFile(file, []byte("user data"), 0644); e != nil {
					t.Fatal(e)
				}
			case "symlink":
				file = filepath.Join(root, ".template-source/process/user-link")
				if e := os.Symlink("reference.md", file); e != nil {
					t.Skipf("symlink unavailable: %v", e)
				}
			}
			before, e := os.Lstat(file)
			if e != nil {
				t.Fatal(e)
			}
			_, err := buildCore(root, "spec", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
			if err == nil || !strings.Contains(err.Error(), ".template-source") {
				t.Fatalf("untrusted reference was not rejected: %v", err)
			}
			after, e := os.Lstat(file)
			if e != nil || after.Mode() != before.Mode() {
				t.Fatalf("user file changed: %v", e)
			}
		})
	}
}

func TestSkillsMigrationRejectsNewSourceFilesAfterPlanning(t *testing.T) {
	root, b, _ := referenceMigrationFixture(t)
	p, err := buildCore(root, "spec", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, ".template-source/process/after-plan.txt")
	if err = os.WriteFile(file, []byte("new user file"), 0644); err != nil {
		t.Fatal(err)
	}
	if err = verifyInstallationWork(t.Context(), p); err == nil {
		t.Fatal("source tree drift accepted")
	}
}

func TestSkillsMigrationRejectsDifferentReferenceBundle(t *testing.T) {
	for _, field := range []string{"commit", "snapshot", "bundle", "file"} {
		t.Run(field, func(t *testing.T) {
			root, b, ref := referenceMigrationFixture(t)
			switch field {
			case "commit":
				b.TemplateCommit = strings.Repeat("b", 40)
			case "snapshot":
				b.SnapshotHash = strings.Repeat("a", 64)
			case "bundle":
				b.BundleHash = strings.Repeat("e", 64)
			case "file":
				f := b.Files[ref]
				f.Digest = strings.Repeat("0", 64)
				b.Files[ref] = f
			}
			_, err := buildCore(root, "spec", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
			if err == nil {
				t.Fatal("different reference provenance accepted")
			}
		})
	}
}

func TestSkillsMigrationRetainsGroupOnlySourceReferences(t *testing.T) {
	root, b, ref := referenceMigrationFixture(t)
	item := b.Installations["skills"]
	item.SkillReferences = nil
	item.ReferenceGroups = map[string][]string{"shared-source": {ref}}
	item.SkillReferenceGroups = map[string][]string{"implement": {"shared-source"}}
	b.Installations["skills"] = item
	plan, err := buildCore(root, "spec", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
	if err != nil {
		t.Fatalf("group-only source reference blocked migration: %v", err)
	}
	if err = applyStandaloneFixture(t, plan, b); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var next bundle.Bundle
	if err = json.Unmarshal(data, &next); err != nil {
		t.Fatal(err)
	}
	next.TemplateCommit, next.SnapshotHash, next.BundleHash = strings.Repeat("f", 40), strings.Repeat("1", 64), strings.Repeat("2", 64)
	f := next.Files[ref]
	updated := []byte("updated group reference")
	f.Data, f.Digest = base64.StdEncoding.EncodeToString(updated), safefs.Digest(updated)
	next.Files[ref] = f
	nextItem := next.Installations["skills"]
	nextItem.Initial[ref] = f
	next.Installations["skills"] = nextItem
	sync, err := buildCore(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: &next})
	if err != nil {
		t.Fatalf("group-only source reference blocked sync: %v", err)
	}
	if err = applyStandaloneFixture(t, sync, &next); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref)))
	if err != nil || string(actual) != string(updated) {
		t.Fatalf("group-only source not updated: %v", err)
	}
}

func TestGovernedSyncUpdatesRetainedReferencesWithBundle(t *testing.T) {
	root, b, ref := referenceMigrationFixture(t)
	p, err := buildCore(root, "spec", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
	if err != nil {
		t.Fatal(err)
	}
	if err = applyStandaloneFixture(t, p, b); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var next bundle.Bundle
	if err = json.Unmarshal(data, &next); err != nil {
		t.Fatal(err)
	}
	next.TemplateCommit, next.SnapshotHash, next.BundleHash = strings.Repeat("f", 40), strings.Repeat("1", 64), strings.Repeat("2", 64)
	f := next.Files[ref]
	updated := []byte("updated reference")
	f.Data, f.Digest = base64.StdEncoding.EncodeToString(updated), safefs.Digest(updated)
	next.Files[ref] = f
	item := next.Installations["skills"]
	item.Initial[ref] = f
	next.Installations["skills"] = item
	sync, err := buildCore(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: &next})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range sync.Changes {
		if c.Path == ref && c.After.Digest == safefs.Digest(updated) {
			found = true
		}
	}
	if !found {
		t.Fatalf("governed sync failed to plan updated retained source reference %s", ref)
	}
	if err = applyStandaloneFixture(t, sync, &next); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(ref)))
	if err != nil || string(actual) != string(updated) {
		t.Fatalf("retained reference not updated: %v", err)
	}
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	if id.Native.TemplateCommit != next.TemplateCommit || id.Native.Managed[ref].Source.TemplateCommit != next.TemplateCommit {
		t.Fatal("updated metadata not bound to new source")
	}
	repeat, err := buildCore(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: &next})
	if err != nil || len(repeat.Changes) != 0 {
		t.Fatalf("repeat sync: %+v %v", repeat, err)
	}
}

func TestGovernedSyncProtectsRetainedReferences(t *testing.T) {
	for _, scenario := range []string{"modified", "deleted", "unmanaged", "target-missing", "after-plan"} {
		t.Run(scenario, func(t *testing.T) {
			root, b, ref := referenceMigrationFixture(t)
			p, err := buildCore(root, "spec", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
			if err != nil {
				t.Fatal(err)
			}
			if err = applyStandaloneFixture(t, p, b); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(root, filepath.FromSlash(ref))
			switch scenario {
			case "modified":
				if err = os.WriteFile(file, []byte("user edit"), 0644); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if err = os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "unmanaged":
				if err = os.WriteFile(filepath.Join(root, ".template-source/process/user.txt"), []byte("user"), 0644); err != nil {
					t.Fatal(err)
				}
			case "target-missing":
				item := b.Installations["skills"]
				delete(item.SkillReferences, "implement")
				b.Installations["skills"] = item
			}
			sync, err := buildCore(root, "spec", "sync", nil, nil, nil, PlanningOptions{target: b})
			if scenario == "after-plan" {
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(root, ".template-source/process/new.txt"), []byte("user"), 0644); err != nil {
					t.Fatal(err)
				}
				if err = verifyRetainedSourceBefore(t.Context(), sync); err == nil {
					t.Fatal("source tree drift accepted")
				}
			} else if err == nil {
				t.Fatalf("%s: unsafe sync accepted", scenario)
			}
		})
	}
}
