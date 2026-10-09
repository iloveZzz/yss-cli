package project

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
)

func TestResolveSkillsReadinessAndBoundedClosure(t *testing.T) {
	root := lockNativeFixture(t)
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	query := func(names []string, when string) *SkillResolution {
		t.Helper()
		before := lockTree(t, root, true)
		r, e := ResolveSkills(id, b, names, "codex", when)
		if e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(before, lockTree(t, root, true)) {
			t.Fatal("resolve wrote files")
		}
		return r
	}
	r := query([]string{"code-review", "codebase-design", "code-review"}, "")
	if r.Status != "missing" || !reflect.DeepEqual(r.CanonicalIDs, []string{"code-review", "codebase-design"}) || len(r.Dependencies) != 0 {
		t.Fatalf("minimal closure: %+v", r)
	}
	r = query([]string{"code-review"}, "lifecycle-document-output,lifecycle-document-output")
	if !reflect.DeepEqual(r.CanonicalIDs, []string{"i-have-adhd", "code-review"}) || !reflect.DeepEqual(r.Missing, []string{"code-review"}) || len(r.Dependencies) != 1 {
		t.Fatalf("conditional closure: %+v", r)
	}
	r = query([]string{"api-integration", "yss-api-integration"}, "")
	if !reflect.DeepEqual(r.CanonicalIDs, []string{"yss-api-integration"}) {
		t.Fatalf("alias: %+v", r)
	}
	r = query([]string{"frontend-commit"}, "")
	if !reflect.DeepEqual(r.CanonicalIDs, []string{"git-commit-core", "frontend-commit"}) || r.Skills[0].Invocation["invocation_mode"] != "user" {
		t.Fatalf("required closure/user boundary: %+v", r)
	}
	for _, x := range []struct {
		names         []string
		runtime, when string
	}{{[]string{"unknown"}, "codex", ""}, {[]string{"yss-harness-upgrade"}, "codex", ""}, {[]string{"research"}, "codex", ""}, {[]string{"product-design"}, "codex", ""}, {[]string{"code-review"}, "pi", ""}, {[]string{"code-review"}, "codex", "unknown"}, {[]string{"code-review"}, "codex", "lifecycle-document-output,"}} {
		if _, e := ResolveSkills(id, b, x.names, x.runtime, x.when); e == nil {
			t.Fatalf("invalid input accepted: %+v", x)
		}
	}
	selection := append(lockSelection(t, "skills", "code-review"), lockSelection(t, "skills", "codebase-design")...)
	p, err := Build(root, "spec", "skills", nil, selection)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(p); err != nil {
		t.Fatal(err)
	}
	id, err = Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	r = query([]string{"code-review", "codebase-design"}, "")
	if r.Status != "ready" || len(r.Missing) != 0 || len(r.Issues) != 0 {
		t.Fatalf("ready: %+v", r)
	}
	for _, s := range r.Skills {
		if !filepath.IsAbs(s.EntryPath) || s.ContentDigest == "" || s.EffectiveHash == "" {
			t.Fatalf("entry not verified: %+v", s)
		}
	}
	// An unrelated broken tree cannot turn a selected query into a library audit.
	other := filepath.Join(root, ".agents/skills/i-have-adhd/SKILL.md")
	raw, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(other, []byte("unrelated drift"), 0644); err != nil {
		t.Fatal(err)
	}
	if query([]string{"code-review"}, "").Status != "ready" {
		t.Fatal("query inspected an unrelated skill")
	}
	if query([]string{"code-review"}, "lifecycle-document-output").Status != "blocked" {
		t.Fatal("condition did not expose affected drift")
	}
	if err = os.WriteFile(other, raw, 0644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, ref, kind string }{
		{"entry-bytes", ".agents/skills/code-review/SKILL.md", "bytes"},
		{"resource-missing", ".agents/skills/code-review/references/candidate-capture.md", "missing"},
		{"permission", ".agents/skills/code-review/SKILL.md", "mode"},
		{"projection", ".codex/skills/code-review/SKILL.md", "bytes"},
		{"symlink", ".agents/skills/code-review/SKILL.md", "symlink"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(root, tc.ref)
			saved, e := os.ReadFile(p)
			if e != nil {
				t.Fatal(e)
			}
			info, e := os.Stat(p)
			if e != nil {
				t.Fatal(e)
			}
			switch tc.kind {
			case "bytes":
				e = os.WriteFile(p, []byte("drift"), info.Mode())
			case "missing":
				e = os.Remove(p)
			case "mode":
				e = os.Chmod(p, 0600)
			case "symlink":
				e = os.Remove(p)
				if e == nil {
					e = os.Symlink("SKILL.md", p)
				}
			}
			if e != nil {
				t.Fatal(e)
			}
			defer func() {
				if tc.kind == "symlink" {
					_ = os.Remove(p)
				}
				if e := os.WriteFile(p, saved, info.Mode()); e != nil {
					t.Fatal(e)
				}
				_ = os.Chmod(p, info.Mode())
			}()
			if tc.kind == "symlink" { // The snapshot helper intentionally refuses to follow a cycle.
				r, e := ResolveSkills(id, b, []string{"code-review"}, "codex", "")
				if e != nil || r.Status != "blocked" {
					t.Fatalf("symlink accepted: %+v %v", r, e)
				}
			} else if r := query([]string{"code-review"}, ""); r.Status != "blocked" || r.Skills[0].EntryPath != "" {
				t.Fatalf("drift accepted: %+v", r)
			}
		})
	}
	occupant := filepath.Join(root, ".agents/skills/codebase-design/extra.txt")
	if err = os.WriteFile(occupant, []byte("occupied"), 0644); err != nil {
		t.Fatal(err)
	}
	if query([]string{"codebase-design"}, "").Status != "blocked" {
		t.Fatal("unregistered resource accepted")
	}
	if err = os.Remove(occupant); err != nil {
		t.Fatal(err)
	}
	id.Native.TemplateCommit = strings.Repeat("a", 40)
	r = query([]string{"code-review"}, "")
	if r.Status != "blocked" || r.Issues[0].Code != "cli-snapshot-mismatch" || r.Skills[0].EntryPath != "" {
		t.Fatalf("source mismatch accepted: %+v", r)
	}
}

func TestResolveSkillsRefusesOccupiedUninstalledDirectory(t *testing.T) {
	root := lockNativeFixture(t)
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundle.Load("spec")
	if err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{".agents/skills/code-review", ".codex/skills/code-review"} {
		p := filepath.Join(root, prefix)
		if err = os.MkdirAll(p, 0755); err != nil {
			t.Fatal(err)
		}
		r, e := ResolveSkills(id, b, []string{"code-review"}, "codex", "")
		if e != nil || r.Status != "blocked" || len(r.Missing) != 0 {
			t.Fatalf("occupied path accepted: %+v %v", r, e)
		}
		if err = os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
}
