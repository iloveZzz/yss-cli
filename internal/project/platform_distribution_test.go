package project

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestFullSpecRegistersLockedNestedPlatformGroup(t *testing.T) {
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	selection := []string{}
	for ref := range b.Files {
		if !strings.HasPrefix(ref, ".cursor/") && !strings.HasPrefix(ref, ".pi/") {
			selection = append(selection, ref)
		}
	}
	sort.Strings(selection)
	profile, _ := domain.GetProfile("spec")
	d, err := distributionFor(&Identity{Profile: profile}, b, selection)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stringsOf(d["runtimes"]), []string{"codex"}) {
		t.Fatalf("full Spec changed runtime boundary: %+v", d)
	}
	if !containsPlatformSkill(stringsOf(d["installedSkills"]), "product-design") {
		t.Fatalf("full Spec installed a source-locked platform group without registering it: %+v", d)
	}
	if _, exists := b.Files[".codex/skills/product-design/SKILL.md"]; exists {
		t.Fatal("fixture must exercise nested Skills without a root SKILL.md")
	}
	f, err := selectedLock(b, d)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(f.Data)
	var lock map[string]any
	if err := json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	platform := lock["skills"].(map[string]any)["platform"].(map[string]any)
	group, ok := platform[".codex/skills"].(map[string]any)
	if !ok || group["product-design"] == nil || platform[".cursor/skills"] != nil || platform[".pi/skills"] != nil {
		t.Fatalf("full selected lock does not match installed platform group: %+v", platform)
	}
}

func containsPlatformSkill(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

func TestSelectedPlatformSkillsRequireFixedSourceCompleteSelectedGroup(t *testing.T) {
	profile, _ := domain.GetProfile("spec")
	file := func(s string) bundle.File {
		return bundle.File{Data: base64.StdEncoding.EncodeToString([]byte(s)), Digest: safefs.Digest([]byte(s)), Mode: 0644, Ownership: "managed"}
	}
	for _, name := range []string{"complete", "partial", "previous-managed", "previous-unmanaged", "unregistered", "excluded-runtime", "unknown-runtime", "unknown-file"} {
		t.Run(name, func(t *testing.T) {
			lock := `{"version":3,"skills":{"shared":{},"platform":{".codex/skills":{"group":{"effectiveHash":"fixed"}},".cursor/skills":{"cursor-only":{"effectiveHash":"fixed"}}}}}`
			b := &bundle.Bundle{Distribution: map[string]any{"mode": "selected", "runtimes": []string{"codex"}, "installedSkills": []string{}}, Files: map[string]bundle.File{"skills-lock.json": file(lock), ".codex/skills/group/skills/one/SKILL.md": file("one"), ".codex/skills/group/skills/two/SKILL.md": file("two"), ".codex/skills/unregistered/SKILL.md": file("unregistered"), ".cursor/skills/cursor-only/SKILL.md": file("cursor")}}
			id := &Identity{Profile: profile}
			selection := []string{".codex/skills/group/skills/one/SKILL.md", ".codex/skills/group/skills/two/SKILL.md"}
			wantNames := []string{"group"}
			wantCode := ""
			switch name {
			case "partial":
				selection = selection[:1]
				wantCode = "ASSET"
			case "previous-managed":
				selection = selection[:1]
				id.Native = &Metadata{Distribution: b.Distribution, Managed: map[string]Managed{".codex/skills/group/skills/two/SKILL.md": {Ownership: "managed"}}}
			case "previous-unmanaged":
				selection = selection[:1]
				id.Native = &Metadata{Distribution: b.Distribution, Managed: map[string]Managed{}}
				wantCode = "ASSET"
			case "unregistered":
				selection = []string{".codex/skills/unregistered/SKILL.md"}
				wantNames = []string{}
			case "excluded-runtime":
				selection = []string{".cursor/skills/cursor-only/SKILL.md"}
				wantNames = []string{}
			case "unknown-runtime":
				b.Distribution["runtimes"] = []string{"unknown"}
				wantCode = "BUNDLE"
			case "unknown-file":
				selection = append(selection, ".codex/skills/group/unknown")
				wantCode = "ASSET"
			}
			d, err := distributionFor(id, b, selection)
			if wantCode != "" {
				var e *domain.Error
				if !errors.As(err, &e) || e.Code != wantCode {
					t.Fatalf("expected %s, got %v", wantCode, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if names := stringsOf(d["installedSkills"]); !reflect.DeepEqual(names, wantNames) {
				t.Fatalf("source registration mismatch: %+v", d)
			}
			if !reflect.DeepEqual(stringsOf(d["runtimes"]), []string{"codex"}) {
				t.Fatal("selected projection expanded runtime boundary")
			}
		})
	}
}
