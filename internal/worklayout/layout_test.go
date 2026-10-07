package worklayout_test

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/worklayout"
	"os"
	"path/filepath"
	"testing"
)

func TestSharedLayoutContract(t *testing.T) {
	b, e := os.ReadFile("testdata/cases.json")
	if e != nil {
		t.Fatal(e)
	}
	var suite struct {
		Cases []struct {
			Root                                    any  `json:"root"`
			Valid                                   bool `json:"valid"`
			Normalized, Feature, Checkpoint, Ticket string
		}
	}
	if e = json.Unmarshal(b, &suite); e != nil {
		t.Fatal(e)
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	for _, c := range suite.Cases {
		l, e := worklayout.New(root, map[string]any{"platform": "local-markdown", "root": c.Root})
		if (e == nil) != c.Valid {
			t.Fatalf("root=%v valid=%t err=%v", c.Root, c.Valid, e)
		}
		if !c.Valid {
			continue
		}
		if l.Root != c.Normalized {
			t.Fatalf("root=%s", l.Root)
		}
		if f, e := l.CheckpointFeature(c.Checkpoint); e != nil || f != c.Feature {
			t.Fatalf("%s %v", f, e)
		}
		if !l.IsTicket(c.Ticket) {
			t.Fatal(c.Ticket)
		}
	}
}

func TestConfiguredFeaturePaths(t *testing.T) {
	for _, base := range []string{".work", "docs/.scratch", "docs/custom-work"} {
		t.Run(base, func(t *testing.T) {
			root, _ := filepath.EvalSymlinks(t.TempDir())
			l, e := worklayout.New(root, map[string]any{"root": base, "platform": "local-markdown"})
			if e != nil {
				t.Fatal(e)
			}
			if got, e := l.FeatureRoot("report"); e != nil || got != base+"/report" {
				t.Fatalf("%s %v", got, e)
			}
			if f, e := l.CheckpointFeature(base + "/report/checkpoint.yaml"); e != nil || f != "report" {
				t.Fatalf("%s %v", f, e)
			}
			if !l.IsTicket(base + "/report/issues/01-export.md") {
				t.Fatal("ticket rejected")
			}
			if _, e := l.CheckpointFeature("outside/report/checkpoint.json"); e == nil {
				t.Fatal("wrong root accepted")
			}
		})
	}
}
func TestUnsafeAndLegacyRoots(t *testing.T) {
	for _, ref := range []string{"", "../outside", "/outside", ".git", ".yss/work", ".agents/work", ".scratch", "docs/requirements/tickets"} {
		if _, e := worklayout.New(t.TempDir(), map[string]any{"root": ref, "platform": "local-markdown"}); e == nil {
			t.Fatalf("accepted %q", ref)
		}
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if e := os.Symlink(t.TempDir(), filepath.Join(root, ".work")); e != nil {
		t.Skip(e)
	}
	if _, e := worklayout.New(root, map[string]any{"root": ".work", "platform": "local-markdown"}); e == nil {
		t.Fatal("symlink accepted")
	}
}
