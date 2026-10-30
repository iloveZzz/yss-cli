package project

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func claudeSkillsProject(t *testing.T) (*Identity, *bundle.Bundle) {
	t.Helper()
	id, b := skillsOnlyProject(t)
	id.Native.Distribution["runtimes"] = []string{"claude", "workbuddy"}
	for ref, f := range b.Files {
		if strings.HasPrefix(ref, ".workbuddy/skills/") {
			b.Files[strings.Replace(ref, ".workbuddy/", ".claude/", 1)] = f
		}
	}
	raw, _ := b.Files["skills-lock.json"].Render(nil)
	var lock map[string]any
	if err := json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	lock["projectionRoots"] = []string{".claude/skills", ".workbuddy/skills"}
	lock["skills"].(map[string]any)["shared"].(map[string]any)["probe"].(map[string]any)["targets"] = []string{".agents/skills", ".claude/skills", ".workbuddy/skills"}
	raw, _ = json.Marshal(lock)
	raw, err := bundle.RenderedSkillLock(raw, b.Files, nil)
	if err != nil {
		t.Fatal(err)
	}
	b.Files["skills-lock.json"] = bundle.File{Data: base64.StdEncoding.EncodeToString(raw), Digest: safefs.Digest(raw), Mode: 0644, Ownership: "managed"}
	for ref, f := range b.Files {
		if ref != "skills-lock.json" && !strings.HasPrefix(ref, ".claude/") {
			continue
		}
		p := filepath.Join(id.Root, filepath.FromSlash(ref))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		content, err := f.Render(nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, content, 0644); err != nil {
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

func TestResolveClaudeProjection(t *testing.T) {
	for _, kind := range []string{"ready", "missing", "extra", "content", "permission", "symlink", "metadata", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			id, b := claudeSkillsProject(t)
			p := filepath.Join(id.Root, ".claude/skills/probe/payload.txt")
			runtime := "claude"
			switch kind {
			case "missing":
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
			case "extra":
				if err := os.WriteFile(p+".extra", []byte("extra"), 0644); err != nil {
					t.Fatal(err)
				}
			case "content":
				if err := os.WriteFile(p, []byte("changed"), 0644); err != nil {
					t.Fatal(err)
				}
			case "permission":
				if err := os.Chmod(p, 0755); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Remove(p); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("SKILL.md", p); err != nil {
					t.Fatal(err)
				}
			case "metadata":
				id.Native.Distribution["runtimes"] = []string{"workbuddy"}
			case "unknown":
				runtime = "unknown"
			}
			var before map[string]domain.Descriptor
			if kind != "symlink" {
				before = recoveryTree(t, id.Root)
			}
			got, err := ResolveSkills(id, b, []string{"probe"}, runtime, "")
			if kind == "unknown" {
				if e, ok := err.(*domain.Error); !ok || e.Code != "ARGUMENT" {
					t.Fatalf("unknown runtime: %v", err)
				}
			} else {
				want := "blocked"
				if kind == "ready" {
					want = "ready"
				}
				if err != nil || got.Status != want {
					t.Fatalf("want %s: %+v %v", want, got, err)
				}
			}
			if kind == "symlink" {
				if target, err := os.Readlink(p); err != nil || target != "SKILL.md" {
					t.Fatal("query changed symlink")
				}
			} else if !reflect.DeepEqual(before, recoveryTree(t, id.Root)) {
				t.Fatal("resolve wrote project files")
			}
		})
	}
}

func TestRetiredSkillResolveV2WithoutJavaScript(t *testing.T) {
	id, b := skillsOnlyProject(t)
	raw, err := b.Files[skillRegistryRef].Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	registry := strings.Replace(string(raw), "maturity: supported}", "maturity: supported, aliases: [research]}", 1) + retirementV2
	b.Files[skillRegistryRef] = retirementFile(registry)
	delete(b.Files, retirementJavaScript)
	before := recoveryTree(t, id.Root)
	got, err := ResolveSkills(id, b, []string{"research"}, "workbuddy", "")
	if err != nil || got.Status != "ready" || !reflect.DeepEqual(got.CanonicalIDs, []string{"probe"}) {
		t.Fatalf("%+v %v", got, err)
	}
	if !reflect.DeepEqual(before, recoveryTree(t, id.Root)) {
		t.Fatal("resolve mutated project")
	}
}
