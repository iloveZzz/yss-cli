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

func workBuddyProject(t *testing.T) (*Identity, *bundle.Bundle) {
	t.Helper()
	profile, _ := domain.GetProfile("spec")
	id := &Identity{Root: t.TempDir(), Profile: profile, Native: &Metadata{Managed: map[string]Managed{}, Distribution: map[string]any{"mode": "selected", "runtimes": []string{"workbuddy"}, "installedSkills": []string{"probe"}}}}
	b := &bundle.Bundle{Profile: "spec", Files: map[string]bundle.File{}, Initial: map[string]bundle.File{}, Distribution: id.Native.Distribution}
	file := func(raw string) bundle.File {
		return bundle.File{Data: base64.StdEncoding.EncodeToString([]byte(raw)), Digest: safefs.Digest([]byte(raw)), Mode: 0644, Ownership: "managed"}
	}
	b.Files[skillRegistryRef] = file("schema_version: 3\nstatus: active\ncanonical_content_root: .agents/skills\ninvocation_contract:\n  schema_version: 2\nskills:\n  - {id: probe, layer: core, maturity: supported}\n")
	b.Files["scripts/lib/skill-supply-chain.mjs"] = file("export const OBSOLETE = new Set([]);\n")
	for _, root := range []string{".agents/skills", ".workbuddy/skills"} {
		b.Files[root+"/probe/SKILL.md"] = file("---\nname: probe\ndescription: Probe\n---\n")
		b.Files[root+"/probe/payload.txt"] = file("resource\n")
	}
	lock := []byte(`{"version":3,"canonicalRoot":".agents/skills","projectionRoots":[".workbuddy/skills"],"skills":{"shared":{"probe":{"skillPath":".agents/skills/probe/SKILL.md","targets":[".agents/skills",".workbuddy/skills"]}},"platform":{}}}`)
	raw, err := bundle.RenderedSkillLock(lock, b.Files, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.Files["skills-lock.json"] = file(string(raw))
	for ref, f := range b.Files {
		p := filepath.Join(id.Root, filepath.FromSlash(ref))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		raw, _ := f.Render(nil)
		if err := os.WriteFile(p, raw, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0644); err != nil {
			t.Fatal(err)
		}
		d, err := safefs.Describe(id.Root, ref)
		if err != nil {
			t.Fatal(err)
		}
		id.Native.Managed[ref] = Managed{Applied: d, Baseline: d, Ownership: f.Ownership}
	}
	return id, b
}

func TestWorkBuddyResolveStates(t *testing.T) {
	for _, kind := range []string{"ready", "missing", "lost-resource", "mode", "extra-file", "unregistered-runtime", "snapshot", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			id, b := workBuddyProject(t)
			target := filepath.Join(id.Root, ".workbuddy/skills/probe/payload.txt")
			want := "blocked"
			switch kind {
			case "ready":
				want = "ready"
			case "missing":
				want = "missing"
				for _, root := range []string{".agents/skills/probe", ".workbuddy/skills/probe"} {
					os.RemoveAll(filepath.Join(id.Root, root))
				}
				for ref := range id.Native.Managed {
					if strings.Contains(ref, "/probe/") {
						delete(id.Native.Managed, ref)
					}
				}
				id.Native.Distribution["installedSkills"] = []string{}
				var local map[string]any
				raw, _ := b.Files["skills-lock.json"].Render(nil)
				json.Unmarshal(raw, &local)
				local["skills"].(map[string]any)["shared"] = map[string]any{}
				raw, _ = json.Marshal(local)
				lockPath := filepath.Join(id.Root, "skills-lock.json")
				os.WriteFile(lockPath, raw, 0644)
				d, _ := safefs.Describe(id.Root, "skills-lock.json")
				m := id.Native.Managed["skills-lock.json"]
				m.Applied = d
				id.Native.Managed["skills-lock.json"] = m
			case "lost-resource":
				os.Remove(target)
			case "mode":
				os.Chmod(target, 0600)
			case "extra-file":
				os.WriteFile(filepath.Join(filepath.Dir(target), "personal.txt"), []byte("keep"), 0644)
			case "unregistered-runtime":
				id.Native.Distribution["runtimes"] = []string{}
			case "snapshot":
				id.Native.SnapshotHash = "different"
			case "dangling":
				os.Remove(target)
				os.Symlink("missing", target)
			}
			var before map[string]domain.Descriptor
			if kind != "dangling" {
				before = recoveryTree(t, id.Root)
			}
			got, err := ResolveSkills(id, b, []string{"probe"}, "workbuddy", "")
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != want {
				t.Fatalf("got %s want %s: %+v", got.Status, want, got.Issues)
			}
			if kind != "dangling" && !reflect.DeepEqual(before, recoveryTree(t, id.Root)) {
				t.Fatal("query wrote project files")
			}
			if kind == "dangling" {
				if destination, _ := os.Readlink(target); destination != "missing" {
					t.Fatal("changed dangling link")
				}
			}
		})
	}
}

func TestWorkBuddyEnsureAndSyncPlan(t *testing.T) {
	id, b := workBuddyProject(t)
	d, err := distributionFor(id, b, []string{".agents/skills/probe/SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	refs := map[string]bundle.File{}
	if err := completeTargets(id, b, d, refs, id.Native.Managed, "skills"); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{".agents/skills/probe/payload.txt", ".workbuddy/skills/probe/payload.txt", "skills-lock.json"} {
		if _, ok := refs[ref]; !ok {
			t.Fatalf("incomplete closure: %s", ref)
		}
	}
	if got := shippedRuntimes(&bundle.Bundle{Files: map[string]bundle.File{}}, []string{"workbuddy"}); len(got) != 0 {
		t.Fatal("declared unshipped runtime", got)
	}
}

func TestBackendSyncRefreshesFullRuntimeMetadata(t *testing.T) {
	profile, _ := domain.GetProfile("backend")
	id := &Identity{Profile: profile, Native: &Metadata{Distribution: map[string]any{"mode": "profile-full", "custom": "preserve"}}}
	b := &bundle.Bundle{Distribution: map[string]any{"mode": "profile-full", "runtimes": []string{"workbuddy"}, "installedSkills": []string{"a", "b"}}, Files: map[string]bundle.File{"unrelated.txt": {}, ".workbuddy/skills/a/SKILL.md": {}}}
	d, err := distributionFor(id, b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stringsOf(d["runtimes"]), []string{"workbuddy"}) || !reflect.DeepEqual(stringsOf(d["installedSkills"]), []string{"a", "b"}) || d["custom"] != "preserve" {
		t.Fatal(d)
	}
	refs := map[string]bundle.File{}
	if err := completeTargets(id, b, d, refs, nil, "sync"); err != nil {
		t.Fatal(err)
	}
	if len(refs) != len(b.Files) {
		t.Fatal("full Profile became selected")
	}
}

func TestWorkBuddyConflictAndRollback(t *testing.T) {
	id, b := workBuddyProject(t)
	extra := ".workbuddy/skills/probe/personal.txt"
	os.WriteFile(filepath.Join(id.Root, extra), []byte("keep"), 0644)
	p := &Plan{Inputs: map[string]domain.Descriptor{}}
	u := &upgradePlanner{id: id, bundle: b, plan: p, old: id.Native.Managed, maintenanceRules: map[string]bundle.MigrationRule{}}
	if err := u.classifyForeignSkillFiles(b.Files); err != nil {
		t.Fatal(err)
	}
	if !containsString(p.Conflicts, extra) {
		t.Fatal("foreign WorkBuddy file was not classified as conflict")
	}
	if !withinSelectedSkillTree(extra, b.Files) {
		t.Fatal("WorkBuddy scope unrecognized")
	}
	file := ".workbuddy/skills/probe/payload.txt"
	before, _ := os.ReadFile(filepath.Join(id.Root, file))
	_, err := transaction.ApplyContextWithValidation(context.Background(), id.Root, "sync", []transaction.Operation{{Path: file, Data: []byte("replacement"), Mode: 0644}}, nil, nil, func() error { return errors.New("validation failed") })
	if err == nil {
		t.Fatal("failed validation accepted")
	}
	after, _ := os.ReadFile(filepath.Join(id.Root, file))
	if string(after) != string(before) {
		t.Fatal("rollback lost original projection")
	}
	if raw, _ := os.ReadFile(filepath.Join(id.Root, extra)); string(raw) != "keep" {
		t.Fatal("rollback changed user file")
	}
}

func TestWorkBuddyPostApplyVerification(t *testing.T) {
	id, _ := workBuddyProject(t)
	if err := verifySkillTrees(id.Root, id.Native); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(id.Root, ".workbuddy/skills/probe/payload.txt"), []byte("changed projection"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := verifySkillTrees(id.Root, id.Native); err == nil {
		t.Fatal("post-apply verifier ignored WorkBuddy resource drift")
	}
}

func TestWorkBuddyRejectsDivergentFixedProjection(t *testing.T) {
	for _, kind := range []string{"mode", "crlf"} {
		t.Run(kind, func(t *testing.T) {
			id, b := workBuddyProject(t)
			ref := ".workbuddy/skills/probe/payload.txt"
			if kind == "mode" {
				ref = ".agents/skills/probe/payload.txt"
				file := b.Files[ref]
				file.Mode = 0755
				b.Files[ref] = file
				if err := os.Chmod(filepath.Join(id.Root, ref), 0755); err != nil {
					t.Fatal(err)
				}
			} else {
				raw := []byte("resource\r\n")
				file := b.Files[ref]
				file.Data = base64.StdEncoding.EncodeToString(raw)
				file.Digest = safefs.Digest(raw)
				b.Files[ref] = file
				if err := os.WriteFile(filepath.Join(id.Root, ref), raw, 0644); err != nil {
					t.Fatal(err)
				}
			}
			d, err := safefs.Describe(id.Root, ref)
			if err != nil {
				t.Fatal(err)
			}
			managed := id.Native.Managed[ref]
			managed.Applied = d
			managed.Baseline = d
			id.Native.Managed[ref] = managed
			result, err := ResolveSkills(id, b, []string{"probe"}, "workbuddy", "")
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != "blocked" {
				t.Fatalf("divergent fixed projection became %s", result.Status)
			}
		})
	}
}
