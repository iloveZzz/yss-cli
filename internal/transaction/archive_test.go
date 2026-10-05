package transaction_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestRollbackKindProtectsLatestSuccessAndNeverReachesPastRolledBack(t *testing.T) {
	root := testRoot(t)
	if _, err := transaction.Apply(root, "migrate", []transaction.Operation{{Path: "asset", Data: []byte("migration")}}); err != nil {
		t.Fatal(err)
	}
	before, err := safefs.Describe(root, "asset")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: "asset", Before: &before, Data: []byte("sync")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.RollbackKind(root, "migrate"); errorCode(err) != "KIND" {
		t.Fatalf("sync treated as migration rollback: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "asset")); string(b) != "sync" {
		t.Fatal("mismatched rollback changed file")
	}
	if out, err := transaction.RollbackKind(root, "sync"); err != nil || out.Status != "rolled-back" {
		t.Fatalf("matching rollback: %+v %v", out, err)
	}
	if _, err := transaction.RollbackKind(root, "migrate"); errorCode(err) != "KIND" {
		t.Fatalf("reached past latest rolled-back transaction: %v", err)
	}
	if out, err := transaction.RollbackKind(root, "sync"); err != nil || out.Status != "unchanged" {
		t.Fatalf("repeat rollback: %+v %v", out, err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "asset")); string(b) != "migration" {
		t.Fatal("older success was reverted")
	}
}
func TestArchivedFileReadsVerifiedCandidateAndNeverCurrentBusinessFile(t *testing.T) {
	root := testRoot(t)
	candidate := []byte{0, 255, '\r', '\n'}
	out, err := transaction.Apply(root, "init", []transaction.Operation{{Path: ".yss.json", Data: candidate}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".yss.json"), []byte("user edit"), 0644); err != nil {
		t.Fatal(err)
	}
	actual, err := transaction.ArchivedFile(root, ".yss.json")
	if err != nil || !bytes.Equal(actual, candidate) {
		t.Fatalf("candidate archive: %q %v", actual, err)
	}
	if _, err := transaction.ArchivedFile(root, "unknown"); errorCode(err) != "ARCHIVE" {
		t.Fatalf("unknown archived path: %v", err)
	}
	if err := os.WriteFile(filepath.Join(out.BackupPath, "objects", safefs.Digest(candidate)), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.ArchivedFile(root, ".yss.json"); errorCode(err) != "STATE" {
		t.Fatalf("corrupt object accepted: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".yss.json")); string(b) != "user edit" {
		t.Fatal("archive lookup modified business file")
	}
	empty := testRoot(t)
	if _, err := transaction.ArchivedFile(empty, ".yss.json"); errorCode(err) != "ARCHIVE" {
		t.Fatalf("absent archive: %v", err)
	}
	entries, err := os.ReadDir(empty)
	if err != nil || len(entries) != 0 {
		t.Fatal("readonly archive lookup created state")
	}
}
func TestArchivedFileRoutesPendingInitBeforeMetadataWasWritten(t *testing.T) {
	root := testRoot(t)
	if _, err := transaction.Apply(root, "prior-committed", []transaction.Operation{{Path: "prior", Data: []byte("committed candidate")}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "first"), []byte("original"), 0751); err != nil {
		t.Fatal(err)
	}
	startAndInterrupt(t, root)
	if _, err := os.Stat(filepath.Join(root, ".yss.json")); !os.IsNotExist(err) {
		t.Fatal("metadata must remain unwritten for this crash sample")
	}
	candidate, err := transaction.ArchivedFile(root, ".yss.json")
	if err != nil || string(candidate) != "{\"schemaVersion\":1,\"profile\":\"spec\"}\n" {
		t.Fatalf("pending candidate unavailable: %q %v", candidate, err)
	}
	if _, err := transaction.ArchivedFile(root, "prior"); errorCode(err) != "ARCHIVE" {
		t.Fatalf("lookup skipped pending to older committed archive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".yss.json")); !os.IsNotExist(err) {
		t.Fatal("archive route wrote target metadata")
	}
	if _, err := transaction.Recover(root); err != nil {
		t.Fatal(err)
	}
}

func TestArchivedFileRejectsMissingCandidateAndAmbiguousPending(t *testing.T) {
	root := testRoot(t)
	first, err := transaction.Apply(root, "one", []transaction.Operation{{Path: "a", Data: []byte("a")}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := safefs.Describe(root, "a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := transaction.Apply(root, "two", []transaction.Operation{{Path: "a", Before: &before, Delete: true}, {Path: "b", Data: []byte("b")}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.ArchivedFile(root, "a"); errorCode(err) != "ARCHIVE" {
		t.Fatalf("deleted candidate accepted: %v", err)
	}
	// Deliberately make two real, digest-bound committed archives claim pending;
	// an untrusted/contradictory state must never pick a recovery identity.
	for _, result := range []transaction.Result{first, second} {
		file := filepath.Join(result.BackupPath, "journal.json")
		bytes, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var value map[string]any
		if err := json.Unmarshal(bytes, &value); err != nil {
			t.Fatal(err)
		}
		value["phase"] = "applying"
		bytes, err = json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, append(bytes, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := transaction.ArchivedFile(root, "b"); errorCode(err) != "AMBIGUOUS" {
		t.Fatalf("ambiguous pending archive selected: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "b")); string(b) != "b" {
		t.Fatal("archive query changed current bytes")
	}
}
