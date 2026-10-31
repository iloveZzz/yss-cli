package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSourceDocumentUsesExternalSnapshot(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dir, "source-lock.json")
	if err := os.WriteFile(snapshot, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YSS_SOURCE_SNAPSHOT", snapshot)
	got, err := sourceDocumentPath("docs/source-lock.json")
	if err != nil || got != snapshot {
		t.Fatalf("snapshot %q, %v", got, err)
	}
	got, err = sourceDocumentPath("README.md")
	if err != nil || got != "README.md" {
		t.Fatalf("document %q, %v", got, err)
	}
	t.Setenv("YSS_SOURCE_SNAPSHOT", "relative.json")
	if _, err := sourceDocumentPath("docs/source-lock.json"); err == nil {
		t.Fatal("relative snapshot accepted")
	}
}
