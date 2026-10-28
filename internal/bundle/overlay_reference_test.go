package bundle

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOverlaySourceIncludesOnlyExactAllowedSourceReference(t *testing.T) {
	root := t.TempDir()
	command := func(args ...string) string {
		t.Helper()
		c := exec.Command("git", append([]string{"-C", root}, args...)...)
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.test", "GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.test")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	command("init")
	files := map[string]string{
		".template-source/process/reference.md":                  "kernel source",
		".template-source/process/shared.md":                     "shared source",
		".template-source/process/private.md":                    "do not include",
		"profiles/backend/.template-source/process/reference.md": "backend source",
		"profiles/backend/README.md":                             "backend",
	}
	for ref, content := range files {
		filename := filepath.Join(root, ref)
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	command("add", ".")
	command("commit", "-m", "fixture")
	commit := command("rev-parse", "HEAD")
	manifest := map[string]any{"allowRootEntries": []string{"README.md"}, "excludeRootEntries": []string{".template-source"}, "allowFiles": []string{".template-source/process/reference.md", ".template-source/process/shared.md"}}
	merged, err := readOverlaySource(context.Background(), root, commit, []string{"profiles/backend"}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(merged[".template-source/process/reference.md"].data); got != "backend source" {
		t.Fatalf("backend must override shared source reference: %q", got)
	}
	if got := string(merged[".template-source/process/shared.md"].data); got != "shared source" {
		t.Fatalf("missing shared source fallback: %q", got)
	}
	if _, ok := merged[".template-source/process/private.md"]; ok {
		t.Fatal("unallowed source file was included")
	}
}
