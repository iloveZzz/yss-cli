package project

import (
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

func trustedReferenceMigrationFixture(t *testing.T) (string, *bundle.Bundle, string) {
	t.Helper()
	b := trustedBundle(t, "backend")
	root, _ := writeInstallationBundleFixture(t, b)
	refs, err := SelectSkillResources(b, InstallationSkills, []string{"implement"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p, err := buildCore(root, "backend", "skills", nil, refs, nil, PlanningOptions{target: b})
	if err != nil || !p.ReadyToApply {
		t.Fatalf("ensure: %+v %v", p, err)
	}
	writeFixtureChanges(t, root, p)
	return root, b, ".template-source/process/MATT-POCOCK-ENGINEERING-SKILLS.md"
}

func TestSkillsMigrationRetainsVerifiedSourceReferences(t *testing.T) {
	root, b, ref := trustedReferenceMigrationFixture(t)
	p, err := buildCore(root, "backend", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
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
			root, b, ref := trustedReferenceMigrationFixture(t)
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
			var userData []byte
			var linkTarget string
			if scenario == "symlink" {
				linkTarget, e = os.Readlink(file)
			} else {
				userData, e = os.ReadFile(file)
			}
			if e != nil {
				t.Fatal(e)
			}
			meta, e := os.ReadFile(filepath.Join(root, MetadataFile))
			if e != nil {
				t.Fatal(e)
			}
			_, err := buildCore(root, "backend", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
			if err == nil || !strings.Contains(err.Error(), ".template-source") {
				t.Fatalf("untrusted reference was not rejected: %v", err)
			}
			after, e := os.Lstat(file)
			if e != nil || after.Mode() != before.Mode() {
				t.Fatalf("user file changed: %v", e)
			}
			if scenario == "symlink" {
				actual, e := os.Readlink(file)
				if e != nil || actual != linkTarget {
					t.Fatalf("symlink changed: %v", e)
				}
			} else {
				actual, e := os.ReadFile(file)
				if e != nil || string(actual) != string(userData) {
					t.Fatalf("user data changed: %v", e)
				}
			}
			now, e := os.ReadFile(filepath.Join(root, MetadataFile))
			if e != nil || string(now) != string(meta) {
				t.Fatalf("metadata changed: %v", e)
			}
		})
	}
}

func TestSkillsMigrationRejectsNewSourceFilesAfterPlanning(t *testing.T) {
	root, b, _ := trustedReferenceMigrationFixture(t)
	p, err := buildCore(root, "backend", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
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
			root, b, ref := trustedReferenceMigrationFixture(t)
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
			before, err := os.ReadFile(filepath.Join(root, ref))
			if err != nil {
				t.Fatal(err)
			}
			meta, err := os.ReadFile(filepath.Join(root, MetadataFile))
			if err != nil {
				t.Fatal(err)
			}
			_, err = buildCore(root, "backend", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
			var failure *domain.Error
			if !errors.As(err, &failure) || failure.Code != "MIGRATION_INPUT" || !strings.Contains(err.Error(), ".template-source") {
				t.Fatalf("different reference provenance accepted: %v", err)
			}
			after, err := os.ReadFile(filepath.Join(root, ref))
			if err != nil || string(after) != string(before) {
				t.Fatalf("source changed: %v", err)
			}
			after, err = os.ReadFile(filepath.Join(root, MetadataFile))
			if err != nil || string(after) != string(meta) {
				t.Fatalf("metadata changed: %v", err)
			}
		})
	}
}

func TestSkillsMigrationRetainsGroupOnlySourceReferences(t *testing.T) {
	root, b, ref := trustedReferenceMigrationFixture(t)
	item := b.Installations["skills"]
	if len(item.SkillReferences["implement"]) != 0 || len(item.SkillReferenceGroups["implement"]) == 0 {
		t.Fatal("official implement reference is not group-only")
	}
	plan, err := buildCore(root, "backend", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
	if err != nil || !plan.ReadyToApply {
		t.Fatalf("migration: %+v %v", plan, err)
	}
	writeFixtureChanges(t, root, plan)
	id, err := Detect(root, "backend", false)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stringsOf(id.Native.Distribution["retainedTemplateSourceReferences"]), []string{ref}) {
		t.Fatal("group reference not retained")
	}
	syncTestSnapshot(t, root)
	sync, err := buildCore(root, "backend", "sync", nil, nil, nil, PlanningOptions{target: b})
	if err != nil || !sync.ReadyToApply {
		t.Fatalf("same bundle sync: %+v %v", sync, err)
	}
	for _, change := range sync.Changes {
		if change.Path == ref {
			t.Fatalf("unchanged reference rewritten: %+v", change)
		}
	}
}

func TestGovernedSyncPlansRetainedReferenceUpdateFromTrustedSource(t *testing.T) {
	root, b, ref := trustedReferenceMigrationFixture(t)
	itemOld := b.Installations["skills"]
	if len(itemOld.SkillReferences["implement"]) != 0 || len(itemOld.SkillReferenceGroups["implement"]) == 0 {
		t.Fatal("expected official group-only reference")
	}
	migration, err := buildCore(root, "backend", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
	if err != nil || !migration.ReadyToApply {
		t.Fatalf("migration: %+v %v", migration, err)
	}
	writeFixtureChanges(t, root, migration)
	// Altered target exercises planner behavior only; it is not a trusted release.
	target := trustedBundle(t, "backend")
	target.TemplateCommit = strings.Repeat("f", 40)
	target.SnapshotHash = strings.Repeat("1", 64)
	target.BundleHash = strings.Repeat("2", 64)
	f := target.Files[ref]
	raw, err := f.Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	f.Data, f.Digest = base64.StdEncoding.EncodeToString(raw), safefs.Digest(raw)
	target.Files[ref] = f
	item := target.Installations["skills"]
	if _, ok := item.Initial[ref]; ok {
		item.Initial[ref] = f
	}
	target.Installations["skills"] = item
	syncTestSnapshot(t, root)
	p, err := buildCore(root, "backend", "sync", nil, nil, nil, PlanningOptions{target: target})
	if err != nil || !p.ReadyToApply {
		t.Fatalf("planner: %+v %v", p, err)
	}
	for _, c := range p.Changes {
		if c.Path == ref && c.After.Digest == safefs.Digest(raw) {
			return
		}
	}
	t.Fatalf("retained reference update not planned: %s", ref)
}

func TestGovernedSyncProtectsRetainedReferences(t *testing.T) {
	for _, scenario := range []string{"modified", "deleted", "unmanaged", "target-missing", "after-plan"} {
		t.Run(scenario, func(t *testing.T) {
			root, b, ref := trustedReferenceMigrationFixture(t)
			p, err := buildCore(root, "backend", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
			if err != nil {
				t.Fatal(err)
			}
			writeFixtureChanges(t, root, p)
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
				delete(b.Files, ref)
			}
			meta, err := os.ReadFile(filepath.Join(root, MetadataFile))
			if err != nil {
				t.Fatal(err)
			}
			guarded := file
			if scenario == "unmanaged" {
				guarded = filepath.Join(root, ".template-source/process/user.txt")
			}
			before, beforeErr := os.ReadFile(guarded)
			syncTestSnapshot(t, root)
			sync, err := buildCore(root, "backend", "sync", nil, nil, nil, PlanningOptions{target: b})
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
				created, readErr := os.ReadFile(filepath.Join(root, ".template-source/process/new.txt"))
				if readErr != nil || string(created) != "user" {
					t.Fatalf("drift file changed: %v", readErr)
				}
			} else if err == nil || !strings.Contains(err.Error(), ".template-source") {
				t.Fatalf("%s: wrong sync rejection: %v", scenario, err)
			}
			after, afterErr := os.ReadFile(guarded)
			if (beforeErr == nil) != (afterErr == nil) || string(before) != string(after) {
				t.Fatalf("%s: user reference changed: %v", scenario, afterErr)
			}
			now, readErr := os.ReadFile(filepath.Join(root, MetadataFile))
			if readErr != nil || string(now) != string(meta) {
				t.Fatalf("%s: metadata changed: %v", scenario, readErr)
			}
		})
	}
}
