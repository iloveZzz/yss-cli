package project

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestInstalledSkillReadiness(t *testing.T) {
	for _, kind := range []string{"ready", "projection", "empty", "backend", "legacy", "runtime", "skill", "bundle", "invalid-skills", "invalid-runtimes", "no-runtimes", "null-runtimes", "empty-runtimes", "blank-runtime", "projection-undeclared"} {
		t.Run(kind, func(t *testing.T) {
			id, b := claudeSkillsProject(t)
			switch kind {
			case "projection":
				if err := os.Remove(filepath.Join(id.Root, ".claude/skills/probe/payload.txt")); err != nil {
					t.Fatal(err)
				}
			case "no-runtimes", "projection-undeclared":
				delete(id.Native.Distribution, "runtimes")
				if kind == "projection-undeclared" {
					if err := os.Remove(filepath.Join(id.Root, ".claude/skills/probe/payload.txt")); err != nil {
						t.Fatal(err)
					}
				}
			case "null-runtimes":
				id.Native.Distribution["runtimes"] = nil
			case "empty-runtimes":
				id.Native.Distribution["runtimes"] = []string{}
			case "blank-runtime":
				id.Native.Distribution["runtimes"] = []string{""}
			case "empty":
				id.Native.Distribution["installedSkills"] = []string{}
			case "backend":
				id.Profile = domain.Profiles["backend"]
			case "legacy":
				id.Native.SchemaVersion = 4
			case "runtime":
				id.Native.Distribution["runtimes"] = []string{"unknown"}
			case "skill":
				id.Native.Distribution["installedSkills"] = []string{"unknown"}
			case "invalid-skills":
				id.Native.Distribution["installedSkills"] = []any{"probe", 7}
			case "invalid-runtimes":
				id.Native.Distribution["runtimes"] = []any{"claude", "workbuddy", 7}
			case "bundle":
				delete(b.Files, skillRegistryRef)
			}
			before := recoveryTree(t, id.Root)
			got, err := CheckInstalledSkills(id, b)
			if kind == "runtime" || kind == "skill" || kind == "bundle" || strings.HasPrefix(kind, "invalid-") {
				if err == nil && got.Status == "ready" {
					t.Fatalf("invalid declaration accepted: %+v", got)
				}
			} else {
				want := "ready"
				if kind == "projection" || kind == "no-runtimes" || kind == "null-runtimes" || kind == "empty-runtimes" || kind == "blank-runtime" || kind == "projection-undeclared" {
					want = "blocked"
				}
				if kind == "empty" || kind == "backend" || kind == "legacy" {
					want = "not-evaluated"
				}
				if err != nil || got.Status != want || got.Scope != "installed-skills" || got.Checked == nil || got.Missing == nil || got.Issues == nil || got.Runtimes == nil {
					t.Fatalf("want %s: %+v %v", want, got, err)
				}
				if want == "ready" && (!reflect.DeepEqual(got.Checked, []string{"probe"}) || !reflect.DeepEqual(got.Runtimes, []string{"claude", "workbuddy"})) {
					t.Fatalf("wrong declaration: %+v", got)
				}
			}
			if !reflect.DeepEqual(before, recoveryTree(t, id.Root)) {
				t.Fatal("readiness wrote files")
			}
		})
	}
}

func TestSpecSkillVerificationBlocksFalseSuccess(t *testing.T) {
	b := trustedBundle(t, "spec")
	root, _ := writeInstallationBundleFixture(t, b)
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = verifySkillTrees(root, id.Native); err != nil {
		t.Fatalf("valid lock rejected: %v", err)
	}
	file := filepath.Join(root, "skills-lock.json")
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var lock map[string]any
	if err = json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	shared := lock["skills"].(map[string]any)["shared"].(map[string]any)
	if len(shared) == 0 {
		t.Fatal("real lock contains no shared skills")
	}
	for _, value := range shared {
		value.(map[string]any)["effectiveHash"] = strings.Repeat("0", 64)
		break
	}
	raw, err = json.Marshal(lock)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, raw, 0644); err != nil {
		t.Fatal(err)
	}
	err = verifySkillTrees(root, id.Native)
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "VERIFY" || !strings.Contains(err.Error(), "Skill 来源锁") {
		t.Fatalf("inconsistent lock must fail VERIFY: %v", err)
	}
}
