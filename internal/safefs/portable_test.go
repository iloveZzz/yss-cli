package safefs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestRejectNonportableWindowsNamesOnEveryPlatform(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"NUL", "docs/con.md", "a/PRN.txt", "aux.yaml", "COM1", "lpt9.log", "COM¹.txt", "LPT²", "CONIN$", "conout$.json", "CON .md", "docs/file.", "docs/file ", "a/<file>", "a/question?", "a/star*", "a/quote\"", "a/bar|", "a/control\x1f.md", "a/delete\x7f", "invalid\xff"} {
		if _, err := Path(root, ref); err == nil {
			t.Errorf("accepted nonportable path %q", ref)
		}
	}
	for _, ref := range []string{"normal.md", "docs/中文.yaml", "COM10.txt", "compositor.md", "a/.config", "release-1.0.md"} {
		if _, err := Path(root, ref); err != nil {
			t.Errorf("safe path rejected %q: %v", ref, err)
		}
	}
}
func TestPathSetRejectsCaseAndParentSpellingCollisions(t *testing.T) {
	for _, paths := range [][]string{{"README.md", "readme.md"}, {"Docs/a.md", "docs/b.md"}, {"A/b", "a"}, {"K/a", "K/b"}, {"S/a", "ſ/b"}} {
		var set PathSet
		if err := set.Add(paths[0]); err != nil {
			t.Fatal(err)
		}
		if err := set.Add(paths[1]); err == nil {
			t.Errorf("accepted portable collision %v", paths)
		}
	}
	var set PathSet
	for _, ref := range []string{"docs/a", "docs/b", "docs/a"} {
		if err := set.Add(ref); err != nil {
			t.Fatal(err)
		}
	}
}
func TestDescribeUsesRuntimeFileModeAndRetainsMissing(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []uint32{0751, 0644, 0444} {
		name := filepath.Join(root, "mode")
		if err := os.WriteFile(name, []byte("payload"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(name, os.FileMode(mode)); err != nil {
			t.Fatal(err)
		}
		got, err := Describe(root, "mode")
		if err != nil {
			t.Fatal(err)
		}
		if got.Mode != domain.FileMode(mode) {
			t.Fatalf("mode %#o described %#o", mode, got.Mode)
		}
		if err := os.Remove(name); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Describe(root, "absent")
	if err != nil || got.Type != "missing" || got.Mode != 0 {
		t.Fatalf("missing descriptor changed: %+v %v", got, err)
	}
}
