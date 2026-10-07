package project

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func lockCode(err error) string {
	var failure *domain.Error
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}

func lockRead(t *testing.T, root string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "skills-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func lockTree(t *testing.T, root string, runtime bool) map[string]domain.Descriptor {
	t.Helper()
	out := map[string]domain.Descriptor{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		ref, _ := filepath.Rel(root, p)
		ref = filepath.ToSlash(ref)
		if !runtime && ref == ".yss" && info.IsDir() {
			return filepath.SkipDir
		}
		if info.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[ref] = domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: domain.FileMode(uint32(info.Mode().Perm()))}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func lockProtected(t *testing.T, root string) {
	t.Helper()
	for _, f := range []struct {
		ref, data string
		mode      os.FileMode
	}{{"src/business.sh", "#!/bin/sh\n# preserved business\n", 0751}, {".git/index", "preserved Git index sentinel bytes", 0600}, {".github/workflows/user.yml", "name: User workflow\n", 0644}} {
		p := filepath.Join(root, f.ref)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.data), f.mode); err != nil {
			t.Fatal(err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(root, "CONTEXT.md")); err == nil {
		if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), append(raw, []byte("\n用户备注：补装不得更改本段。\n")...), 0644); err != nil {
			t.Fatal(err)
		}
	}
}

func lockNativeFixture(t *testing.T) string {
	t.Helper()
	root := freshRoot(t)
	p, err := Build(root, "spec", "init", map[string]string{"projectName": "锁一致性项目"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(p); err != nil {
		t.Fatal(err)
	}
	lockProtected(t, root)
	return root
}

func lockSelection(t *testing.T, command, name string) []string {
	t.Helper()
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	var r bundle.Requirement
	var ok bool
	if command == "assets" {
		r, ok = b.StageRequirements[name]
	} else {
		r, ok = b.SkillRequirements[name]
	}
	if !ok || r.UnsupportedReason != "" {
		t.Fatalf("unsupported fixture requirement %s/%s", command, name)
	}
	refs := map[string]bool{}
	for _, ref := range r.Paths {
		refs[ref] = true
	}
	names := r.Skills
	if command == "skills" {
		names = append(append([]string{}, names...), name)
	}
	for ref := range b.Files {
		for _, skill := range names {
			if strings.HasPrefix(ref, ".agents/skills/"+skill+"/") || strings.HasPrefix(ref, ".codex/skills/"+skill+"/") {
				refs[ref] = true
			}
		}
	}
	out := []string{}
	for ref := range refs {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

func lockChanged(p *Plan) bool {
	for _, c := range p.Changes {
		if c.Path == "skills-lock.json" {
			return true
		}
	}
	return false
}

func assertInstalledLock(t *testing.T, root, skill string) {
	t.Helper()
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(b.Files["skills-lock.json"].Data)
	if err != nil {
		t.Fatal(err)
	}
	var source map[string]any
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	want := source["skills"].(map[string]any)["shared"].(map[string]any)[skill].(map[string]any)
	lock := lockRead(t, root)
	shared := lock["skills"].(map[string]any)["shared"].(map[string]any)
	entry, ok := shared[skill].(map[string]any)
	if !ok || entry["effectiveHash"] != want["effectiveHash"] {
		t.Fatalf("installed Skill is absent or has wrong source hash: %s %+v", skill, entry)
	}
	if !reflect.DeepEqual(stringsOf(entry["targets"]), []string{".agents/skills", ".codex/skills"}) || !reflect.DeepEqual(stringsOf(lock["projectionRoots"]), []string{".codex/skills"}) {
		t.Fatalf("lock has wrong projection targets: %+v", lock)
	}
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for name := range shared {
		names = append(names, name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, union(stringsOf(id.Native.Distribution["installedSkills"]))) {
		t.Fatalf("lock and installed metadata disagree: %v %+v", names, id.Native.Distribution)
	}
	d, err := safefs.Describe(root, "skills-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	if m := id.Native.Managed["skills-lock.json"]; m.Ownership != "generated" || m.Applied != d || m.Baseline != d {
		t.Fatalf("lock baseline did not advance in the same transaction: %+v %+v", m, d)
	}
	for _, ref := range []string{".agents/skills/" + skill + "/SKILL.md", ".codex/skills/" + skill + "/SKILL.md"} {
		want, err := b.Files[ref].Render(id.Native.Variables)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(root, ref))
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("missing or incorrect installed projection %s: %v", ref, err)
		}
	}
}

func TestDerivedLockEnsureStageAndSkillRepeatSyncRollback(t *testing.T) {
	for _, x := range []struct{ command, name, skill string }{{"skills", "tdd", "tdd"}, {"assets", "stage.product-design", "yss-prototype-stage"}} {
		t.Run(x.command, func(t *testing.T) {
			root := lockNativeFixture(t)
			before := lockTree(t, root, false)
			selection := lockSelection(t, x.command, x.name)
			p, err := Build(root, "spec", x.command, nil, selection)
			if err != nil {
				t.Fatal(err)
			}
			if len(p.Conflicts) != 0 || !lockChanged(p) {
				t.Fatalf("successful ensure must update the derived lock: conflicts=%v preserved=%v", p.Conflicts, p.Preserved)
			}
			if _, err := Apply(p); err != nil {
				t.Fatal(err)
			}
			assertInstalledLock(t, root, x.skill)
			for _, command := range []string{x.command, "sync", "doctor", "diff"} {
				var selectAgain []string
				if command == x.command {
					selectAgain = selection
				}
				snapshot := lockTree(t, root, true)
				p, err := Build(root, "spec", command, nil, selectAgain)
				if err != nil {
					t.Fatal(err)
				}
				if len(p.Conflicts) != 0 || len(p.Changes) != 0 {
					t.Fatalf("repeat %s drift: changes=%d conflicts=%v", command, len(p.Changes), p.Conflicts)
				}
				if !reflect.DeepEqual(snapshot, lockTree(t, root, true)) {
					t.Fatalf("read-only %s changed the project", command)
				}
			}
			if _, err := transaction.Rollback(root); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, lockTree(t, root, false)) {
				t.Fatal("rollback did not restore lock, metadata, asset closure and all protected bytes/modes together")
			}
		})
	}
}

func TestDerivedLockRefusesUserBytesModeMissingAndPostPlanDrift(t *testing.T) {
	for _, when := range []string{"before-plan", "after-plan"} {
		for _, what := range []string{"bytes", "mode", "missing"} {
			t.Run(when+"/"+what, func(t *testing.T) {
				root := lockNativeFixture(t)
				selection := lockSelection(t, "skills", "tdd")
				var p *Plan
				var err error
				if when == "after-plan" {
					p, err = Build(root, "spec", "skills", nil, selection)
					if err != nil {
						t.Fatal(err)
					}
				}
				file := filepath.Join(root, "skills-lock.json")
				switch what {
				case "bytes":
					raw, err := os.ReadFile(file)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(file, append(raw, []byte("\n ")...), 0644); err != nil {
						t.Fatal(err)
					}
				case "mode":
					if err := os.Chmod(file, 0444); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = os.Chmod(file, 0644) })
				case "missing":
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
				}
				before := lockTree(t, root, true)
				want := "INPUT_DRIFT"
				if when == "before-plan" {
					p, err = Build(root, "spec", "skills", nil, selection)
					if err != nil {
						t.Fatal(err)
					}
					want = "CONFLICT"
					found := false
					for _, ref := range p.Conflicts {
						if ref == "skills-lock.json" {
							found = true
						}
					}
					if !found {
						t.Fatal("user bytes/mode/deletion silently preserved or replaced instead of rejecting ensure")
					}
				}
				if _, err := Apply(p); lockCode(err) != want {
					t.Fatalf("wrong refusal: %v want %s", err, want)
				}
				if !reflect.DeepEqual(before, lockTree(t, root, true)) {
					t.Fatal("refusal wrote targets, metadata, index or recovery state")
				}
			})
		}
	}
}

func TestDerivedLockDoesNotAdoptUnmanagedAttachLockOrOtherGeneratedFiles(t *testing.T) {
	t.Run("unmanaged-attach-lock", func(t *testing.T) {
		root := freshRoot(t)
		if err := os.MkdirAll(root, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "skills-lock.json"), []byte("{\"version\":3,\"skills\":{\"shared\":{},\"platform\":{}},\"user\":true}\n"), 0644); err != nil {
			t.Fatal(err)
		}
		p, err := Build(root, "spec", "attach", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		before := lockTree(t, root, true)
		if _, err := Apply(p); lockCode(err) != "CONFLICT" {
			t.Fatalf("first attach silently adopted an unmanaged generated lock: %v", err)
		}
		if !reflect.DeepEqual(before, lockTree(t, root, true)) {
			t.Fatal("unmanaged lock refusal wrote assets")
		}
	})
	t.Run("other-generated-preserved", func(t *testing.T) {
		root := lockNativeFixture(t)
		ref := ".agents/skills/.yss-skills-manifest.json"
		raw, err := os.ReadFile(filepath.Join(root, ref))
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		value["userAnnotation"] = "must stay"
		raw, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, ref), raw, 0644); err != nil {
			t.Fatal(err)
		}
		before, err := safefs.Describe(root, ref)
		if err != nil {
			t.Fatal(err)
		}
		p, err := Build(root, "spec", "skills", nil, lockSelection(t, "skills", "tdd"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(p); err != nil {
			t.Fatal(err)
		}
		assertInstalledLock(t, root, "tdd")
		after, err := safefs.Describe(root, ref)
		if err != nil || after != before {
			t.Fatal("narrow lock fix overwrote another generated asset")
		}
	})
}

func TestDerivedLockRefusesFutureBytesAndWrongNativeOwnership(t *testing.T) {
	for _, what := range []string{"future-desired-bytes", "different-managed-ownership"} {
		t.Run(what, func(t *testing.T) {
			root := lockNativeFixture(t)
			selection := lockSelection(t, "skills", "tdd")
			if what == "future-desired-bytes" {
				p, err := Build(root, "spec", "skills", nil, selection)
				if err != nil {
					t.Fatal(err)
				}
				for _, c := range p.Changes {
					if c.Path == "skills-lock.json" {
						data, err := base64.StdEncoding.DecodeString(c.Data)
						if err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(root, c.Path), data, 0644); err != nil {
							t.Fatal(err)
						}
					}
				}
			} else {
				id, err := Detect(root, "spec", false)
				if err != nil {
					t.Fatal(err)
				}
				m := id.Native.Managed["skills-lock.json"]
				m.Ownership = "managed"
				id.Native.Managed["skills-lock.json"] = m
				managed, err := json.Marshal(id.Native.Managed)
				if err != nil {
					t.Fatal(err)
				}
				id.Native.BaselineDigest = safefs.Digest(managed)
				raw, err := jsonBytes(id.Native)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, MetadataFile), raw, 0644); err != nil {
					t.Fatal(err)
				}
			}
			before := lockTree(t, root, true)
			p, err := Build(root, "spec", "skills", nil, selection)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Apply(p); lockCode(err) != "CONFLICT" {
				t.Fatalf("derived lock silently adopted %s: %v", what, err)
			}
			if !reflect.DeepEqual(before, lockTree(t, root, true)) {
				t.Fatal("rejected lock adoption wrote project files")
			}
		})
	}
}

func lockLegacyFixture(t *testing.T) (string, []byte) {
	t.Helper()
	root := freshRoot(t)
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(b.Files["skills-lock.json"].Data)
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]any
	if err := json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	lock["legacyFixture"] = "previous declared managed baseline"
	raw, err = json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{"metadataSchemaVersion": 3, "templateSource": domain.Profiles["spec"].TemplateSource, "templateCommit": strings.Repeat("a", 40), "managedFiles": map[string]any{"skills-lock.json": map[string]any{"contentHash": safefs.Digest(raw)}}, "distribution": b.Distribution}
	meta, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	for ref, data := range map[string][]byte{"skills-lock.json": raw, domain.Profiles["spec"].Metadata: meta, "yss-project.yaml": []byte("schema_version: 1\nrepository_mode: project-instance\n")} {
		if err := os.WriteFile(filepath.Join(root, ref), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	lockProtected(t, root)
	return root, raw
}

func TestDerivedLockLegacyMigrationRollbackAndUnprovenModeRefusal(t *testing.T) {
	t.Run("migration-and-rollback", func(t *testing.T) {
		root, old := lockLegacyFixture(t)
		before := lockTree(t, root, false)
		for _, command := range []string{"doctor", "diff"} {
			if _, err := Build(root, "spec", command, nil, nil); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, lockTree(t, root, false)) {
				t.Fatal("legacy read-only inspection changed old assets")
			}
		}
		p, err := Build(root, "spec", "migrate", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Conflicts) != 0 || !lockChanged(p) {
			t.Fatalf("explicit migration did not plan derived lock update: %v %v", p.Conflicts, p.Preserved)
		}
		if _, err := Apply(p); err != nil {
			t.Fatal(err)
		}
		newRaw, err := os.ReadFile(filepath.Join(root, "skills-lock.json"))
		if err != nil || reflect.DeepEqual(newRaw, old) {
			t.Fatal("migration left the old generated lock")
		}
		if _, err := transaction.Rollback(root); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, lockTree(t, root, false)) {
			t.Fatal("legacy lock/raw metadata/mode and business targets did not roll back together")
		}
	})
	t.Run("unproven-legacy-mode", func(t *testing.T) {
		root, _ := lockLegacyFixture(t)
		file := filepath.Join(root, "skills-lock.json")
		if err := os.Chmod(file, 0444); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(file, 0644) })
		before := lockTree(t, root, true)
		p, err := Build(root, "spec", "migrate", nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(p); lockCode(err) != "CONFLICT" {
			t.Fatalf("unrecorded legacy permission change accepted: %v", err)
		}
		if !reflect.DeepEqual(before, lockTree(t, root, true)) {
			t.Fatal("rejected legacy mode was overwritten")
		}
	})
}
