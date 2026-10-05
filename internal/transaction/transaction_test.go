package transaction_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestApplyAndRollbackPreserveOriginalBytesModeAndGitIndex(t *testing.T) {
	root := testRoot(t)
	original := []byte{0, 0xff, '\r', '\n', 1}
	if err := os.WriteFile(filepath.Join(root, "existing"), original, 0751); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	index := []byte("untouched git index")
	if err := os.WriteFile(filepath.Join(root, ".git/index"), index, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := safefs.Describe(root, "existing")
	if err != nil {
		t.Fatal(err)
	}
	result, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: "existing", Data: []byte("updated"), Mode: 0644, Before: &before}, {Path: "new/asset", Data: []byte("created"), Mode: 0600}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "applied" || result.TransactionID == "" || result.PlanDigest == "" {
		t.Fatalf("missing transaction result: %+v", result)
	}
	if _, err := transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(root, "existing"))
	if err != nil || string(actual) != string(original) {
		t.Fatalf("original byte archive not restored: %v %v", actual, err)
	}
	info, err := os.Stat(filepath.Join(root, "existing"))
	if err != nil || domain.FileMode(uint32(info.Mode().Perm())) != domain.FileMode(0751) {
		t.Fatalf("original mode not restored: %v %v", info, err)
	}
	if _, err := os.Stat(filepath.Join(root, "new/asset")); !os.IsNotExist(err) {
		t.Fatalf("created file survived rollback: %v", err)
	}
	actual, err = os.ReadFile(filepath.Join(root, ".git/index"))
	if err != nil || string(actual) != string(index) {
		t.Fatal("Git index changed")
	}
}

func testRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func errorCode(err error) string {
	var e *domain.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestRollbackConflictStopsTheWholeTransactionAndKeepsUserEdits(t *testing.T) {
	root := testRoot(t)
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("original"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := safefs.Describe(root, "first")
	second, _ := safefs.Describe(root, "second")
	if _, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: "first", Before: &first, Data: []byte("template")}, {Path: "second", Before: &second, Data: []byte("template")}}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "second"), []byte("user edit"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Rollback(root); errorCode(err) != "CONCURRENT" {
		t.Fatalf("expected explicit concurrent-edit rejection: %v", err)
	}
	for name, wanted := range map[string]string{"first": "template", "second": "user edit"} {
		got, e := os.ReadFile(filepath.Join(root, name))
		if e != nil || string(got) != wanted {
			t.Fatalf("partial rollback or user overwrite for %s: %q %v", name, got, e)
		}
	}
}
