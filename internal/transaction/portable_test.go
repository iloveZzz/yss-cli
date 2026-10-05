package transaction_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestRejectPortableOperationAndGuardCaseCollisions(t *testing.T) {
	for _, paths := range [][]string{{"README.md", "readme.md"}, {"Docs/a", "docs/b"}, {"a", "A/b"}, {"A/b", "a"}, {"same", "same"}} {
		root := testRoot(t)
		ops := []transaction.Operation{{Path: paths[0], Data: []byte("first")}, {Path: paths[1], Data: []byte("second")}}
		if _, err := transaction.Apply(root, "case-collision", ops); errorCode(err) != "PLAN" {
			t.Fatalf("collision %v not rejected: %v", paths, err)
		}
		for _, ref := range paths {
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(ref))); !os.IsNotExist(err) {
				t.Fatalf("rejected transaction wrote %s", ref)
			}
		}
	}
	root := testRoot(t)
	missing := domain.Descriptor{Type: "missing"}
	if _, err := transaction.ApplyWithGuards(root, "guard-case", []transaction.Operation{{Path: "docs/a", Data: []byte("new")}}, map[string]domain.Descriptor{"Docs/b": missing}); errorCode(err) != "PLAN" {
		t.Fatalf("guard alias ambiguity accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs/a")); !os.IsNotExist(err) {
		t.Fatal("guard collision wrote target")
	}
}
func TestRequestedExecutableModeMatchesActualAndRollback(t *testing.T) {
	root := testRoot(t)
	result, err := transaction.Apply(root, "executable", []transaction.Operation{{Path: "tool", Data: []byte("script"), Mode: 0755}})
	if err != nil {
		t.Fatal(err)
	}
	actual, err := safefs.Describe(root, "tool")
	if err != nil || actual.Mode != domain.FileMode(0755) {
		t.Fatalf("after descriptor disagrees with actual mode: %+v %v", actual, err)
	}
	if _, err = transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "tool")); !os.IsNotExist(err) {
		t.Fatal("rollback retained created file")
	}
	if result.Kind != "executable" {
		t.Fatal("kind changed")
	}
}

func TestReadOnlyFilePolicyPreservesPlatformContract(t *testing.T) {
	root := testRoot(t)
	file := filepath.Join(root, "readonly")
	if err := os.WriteFile(file, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0444); err != nil {
		t.Fatal(err)
	}
	before, err := safefs.Describe(root, "readonly")
	if err != nil {
		t.Fatal(err)
	}
	out, err := transaction.Apply(root, "readonly-policy", []transaction.Operation{{Path: "readonly", Before: &before, Data: []byte("new"), Mode: 0644}})
	if runtime.GOOS == "windows" {
		if errorCode(err) != "UNPORTED" {
			t.Fatalf("unverified Windows readonly replacement must fail closed: %+v %v", out, err)
		}
		if actual, _ := os.ReadFile(file); string(actual) != "original" {
			t.Fatal("readonly source changed")
		}
		if _, err = os.Stat(filepath.Join(root, ".yss")); !os.IsNotExist(err) {
			t.Fatal("readonly refusal created journal")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transaction.RollbackKind(root, "readonly-policy"); err != nil {
		t.Fatal(err)
	}
	actual, err := safefs.Describe(root, "readonly")
	if err != nil || actual.Mode != 0444 || actual.Digest != before.Digest {
		t.Fatalf("Unix readonly original not restored: %+v %v", actual, err)
	}
}

func TestReadOnlyDeletePolicy(t *testing.T) {
	root := testRoot(t)
	file := filepath.Join(root, "readonly")
	if err := os.WriteFile(file, []byte("original"), 0444); err != nil {
		t.Fatal(err)
	}
	before, err := safefs.Describe(root, "readonly")
	if err != nil {
		t.Fatal(err)
	}
	_, err = transaction.Apply(root, "readonly-delete", []transaction.Operation{{Path: "readonly", Before: &before, Delete: true}})
	if runtime.GOOS == "windows" {
		if errorCode(err) != "UNPORTED" {
			t.Fatalf("readonly delete accepted: %v", err)
		}
		if b, _ := os.ReadFile(file); string(b) != "original" {
			t.Fatal("readonly file changed")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err = transaction.RollbackKind(root, "readonly-delete"); err != nil {
		t.Fatal(err)
	}
	actual, err := safefs.Describe(root, "readonly")
	if err != nil || actual != before {
		t.Fatalf("readonly deletion rollback: %+v %v", actual, err)
	}
}
