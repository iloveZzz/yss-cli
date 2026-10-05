package governance

import (
	"archive/zip"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipTestBytes(t *testing.T, names []string) []byte {
	t.Helper()
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for _, name := range names {
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		h.SetMode(0644)
		w, e := z.CreateHeader(h)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write([]byte("archive-only")); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}

func TestSemanticZIPRejectsUnicodeAndPrefixCollisions(t *testing.T) {
	for _, names := range [][]string{{"Straße.txt", "STRASSE.txt"}, {"Σ.txt", "ς.txt"}, {"A", "a/x"}, {"A/x", "a/y"}, {"a", "a/x"}, {"../escape"}, {"same", "same"}} {
		t.Run(names[0], func(t *testing.T) {
			root := semanticTestRoot(t)
			apTestPut(t, root, "package.zip", zipTestBytes(t, names))
			s := newSemanticSession(context.Background(), root, nil)
			_, _, e := s.zipSourceSession("package.zip")
			apTestCode(t, e, "HANDOFF_PATH")
		})
	}
}

func TestSemanticZIPSourceIsImmutableAndNeverFallsBack(t *testing.T) {
	root := semanticTestRoot(t)
	apTestPut(t, root, "package.zip", zipTestBytes(t, []string{"handoff.yaml", "payload/input.json"}))
	s := newSemanticSession(context.Background(), root, nil)
	child, prefix, e := s.zipSourceSession("package.zip")
	if e != nil {
		t.Fatal(e)
	}
	rows, e := child.list(prefix)
	if e != nil || len(rows) != 2 {
		t.Fatalf("list %v: %v", rows, e)
	}
	raw, e := child.bytes(prefix + "/payload/input.json")
	if e != nil || string(raw) != "archive-only" {
		t.Fatalf("read %q: %v", raw, e)
	}
	// Even injected physical files cannot supply omitted archive members.
	apTestPut(t, child.root, prefix+"/injected.json", "physical")
	if exists, err := child.exists(prefix + "/injected.json"); err != nil || exists {
		t.Fatal("physical fallback")
	}
	if e = child.v.set(prefix+"/handoff.yaml", []byte("changed")); e == nil {
		t.Fatal("archive write allowed")
	}
	if e = child.registerExternalRoot(root, prefix+"/handoff.yaml"); e == nil {
		t.Fatal("archive grants external permission")
	}
	if e = s.finish(); e != nil {
		t.Fatal(e)
	}
	apTestPut(t, root, "package.zip", zipTestBytes(t, []string{"handoff.yaml", "payload/changed.json"}))
	apTestCode(t, s.finish(), "INPUT_DRIFT")
}

func TestSemanticZIPDoesNotExtract(t *testing.T) {
	root := semanticTestRoot(t)
	apTestPut(t, root, "package.zip", zipTestBytes(t, []string{"handoff.yaml"}))
	s := newSemanticSession(context.Background(), root, nil)
	if _, _, e := s.zipSourceSession("package.zip"); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(root, ".yss-archive-view")); !os.IsNotExist(e) {
		t.Fatalf("extracted: %v", e)
	}
}

func TestSemanticZIPFullSourceAuthoritiesUseObservedArchive(t *testing.T) {
	root := apTestRoot(t)
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	bindings := map[string]string{}
	if err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		original, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		original = filepath.ToSlash(original)
		stored := "files/" + original
		if original == "CONTEXT.md" {
			stored = "files/source-context.snapshot.md"
		}
		bindings[original] = stored
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		w, err := z.Create("payload/" + stored)
		if err != nil {
			return err
		}
		_, err = w.Write(raw)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	apTestPut(t, root, "source.zip", archive.Bytes())
	s := apTestSession(t, root)
	// The consumer is a different Profile. Its current checkpoint cannot
	// replace the source identity or approval expectations.
	s.args = map[string]string{"profile": "frontend", "checkpoint": "receiving-checkpoint.json"}
	zipView, prefix, err := s.zipSourceSession("source.zip")
	if err != nil {
		t.Fatal(err)
	}
	source, err := zipView.sourceSessionBindings(prefix+"/payload", bindings)
	if err != nil {
		t.Fatal(err)
	}
	if source.args["profile"] != "" || source.checkpointRef != "" {
		t.Fatal("receiving identity leaked into source")
	}
	if _, err = source.contextContract(); err != nil {
		t.Fatal(err)
	}
	for _, input := range source.inputs() {
		if strings.HasPrefix(input.Ref, ".yss/transactions") && input.Descriptor.Type != "missing" {
			t.Fatal("unexpected transaction")
		}
	}
	if err = s.finish(); err != nil {
		t.Fatal(err)
	}
}
