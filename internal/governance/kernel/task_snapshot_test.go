package kernel

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestTaskSnapshotObjectsAreContentAddressed(t *testing.T) {
	for _, tc := range []struct {
		name, kind, oid string
		ok              bool
	}{
		{"empty-tree", "tree", "4b825dc642cb6eb9a060e54bf8d69288fbee4904", true},
		{"forged-object", "tree", "0000000000000000000000000000000000000000", false},
		{"unsupported-object", "tag", "4b825dc642cb6eb9a060e54bf8d69288fbee4904", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSemanticSession(context.Background(), t.TempDir(), nil)
			_, err := decodeSnapshotObjects(s, []any{map[string]any{"type": tc.kind, "oid": tc.oid, "data_base64": base64.StdEncoding.EncodeToString(nil)}})
			if (err == nil) != tc.ok {
				t.Fatalf("content proof: %v", err)
			}
		})
	}
}

func TestTaskSnapshotDoesNotTreatArrayDataAsATask(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewSemanticSession(context.Background(), root, nil)
	s.GovernanceVersion = 2
	if err := os.WriteFile(filepath.Join(s.Root, "data.json"), []byte("[]"), 0644); err != nil {
		t.Fatal(err)
	}
	proof, refs, err := s.CaptureTaskSnapshot([]string{"data.json"})
	if err != nil || proof != nil || len(refs) != 1 {
		t.Fatalf("plain data changed task capture: %v", err)
	}
}

func TestTaskSnapshotReconstructsGitWithoutARepository(t *testing.T) {
	s := NewSemanticSession(context.Background(), t.TempDir(), nil)
	objects := map[string]snapshotGitObject{}
	put := func(kind string, raw []byte) string {
		sum := sha1.Sum(append([]byte(fmt.Sprintf("%s %d\x00", kind, len(raw))), raw...))
		oid := hex.EncodeToString(sum[:])
		objects[oid] = snapshotGitObject{kind, raw}
		return oid
	}
	blob := put("blob", []byte("hello\n"))
	rawOID, _ := hex.DecodeString(blob)
	tree := put("tree", append([]byte("100755 run.sh\x00"), rawOID...))
	commit := put("commit", []byte("tree "+tree+"\n\nTEST immutable source\n"))
	s.taskSnapshot = &taskSourceSnapshot{root: "/source-no-longer-present", objects: objects}
	got, err := s.Git(s.taskSnapshot.root, "ls-tree", "-r", "-z", commit)
	if err != nil || string(got) != "100755 blob "+blob+"\trun.sh\x00" {
		t.Fatalf("tree: %q %v", got, err)
	}
	got, err = s.Git(s.taskSnapshot.root, "cat-file", "blob", blob)
	if err != nil || !bytes.Equal(got, []byte("hello\n")) {
		t.Fatalf("blob: %q %v", got, err)
	}
	if _, err = s.Git(s.taskSnapshot.root, "status", "--porcelain"); err == nil {
		t.Fatal("snapshot granted a physical Git query")
	}
	if _, err = s.Git(s.Root, "rev-parse", "--show-toplevel"); err == nil {
		t.Fatal("snapshot borrowed receiver root")
	}
	delete(objects, tree)
	if _, err = s.Git(s.taskSnapshot.root, "ls-tree", "-r", "-z", commit); err == nil {
		t.Fatal("missing tree accepted")
	}
}

func TestTaskSnapshotOriginIsPortableButCannotEscape(t *testing.T) {
	for _, root := range []string{"/tmp/source", "C:\\work\\source", "D:/work/source"} {
		if !snapshotOriginRoot(root) {
			t.Fatalf("source root rejected: %q", root)
		}
	}
	for _, root := range []string{"relative/source", "/tmp/../private", "C:/work/../private", "/tmp/source\x00"} {
		if snapshotOriginRoot(root) {
			t.Fatalf("invalid source root accepted: %q", root)
		}
	}
}
