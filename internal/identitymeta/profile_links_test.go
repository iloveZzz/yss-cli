package identitymeta

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProfileLinksRejectInvalidDocumentAndRootBoundaries(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "source")
	if err = os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"schema_version":1,"schema_version":1,"links":{}}`,
		`{"schema_version":1,"links":{},"status":"complete"}`,
		`{"schema_version":2,"links":{}}`,
		`{"schema_version":1,"links":{"spec":"/somewhere"}}`,
		`{"schema_version":1,"links":{"backend":"relative"}}`,
	} {
		if err = os.WriteFile(filepath.Join(root, ProfileLinksFile), []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
		if _, err = ReadProfileLinks(root); err == nil {
			t.Fatalf("unsafe links accepted: %s", raw)
		}
	}
	for _, links := range []map[string]string{
		{"backend": root},
		{"backend": filepath.Join(root, "child")},
		{"backend": filepath.Join(base, "target"), "frontend": filepath.Join(base, "target", "child")},
		{"backend": string(filepath.Separator)},
	} {
		if err = ValidateProfileLinks(root, &ProfileLinks{SchemaVersion: 1, Links: links}); err == nil {
			t.Fatalf("overlapping roots accepted: %+v", links)
		}
	}
}

func TestProfileLinksRejectSymlinkAndAcceptMissingTarget(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "source")
	if err = os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	links, err := ReadProfileLinks(root)
	if err != nil || links.SchemaVersion != 1 || len(links.Links) != 0 {
		t.Fatalf("missing document: %+v %v", links, err)
	}
	if err = ValidateProfileLinks(root, &ProfileLinks{SchemaVersion: 1, Links: map[string]string{"backend": filepath.Join(base, "new", "backend")}}); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(root, filepath.Join(base, "linked")); err != nil {
		t.Fatal(err)
	}
	if err = ValidateProfileLinks(root, &ProfileLinks{SchemaVersion: 1, Links: map[string]string{"backend": filepath.Join(base, "linked", "backend")}}); err == nil {
		t.Fatal("symlink target accepted")
	}
	if err = os.Symlink(filepath.Join(base, "outside.json"), filepath.Join(root, ProfileLinksFile)); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadProfileLinks(root); err == nil {
		t.Fatal("symlink relation file accepted")
	}
}
