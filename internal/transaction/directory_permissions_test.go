package transaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestDirectoryTransactionAndFailedInitRollback(t *testing.T) {
	root := t.TempDir()
	if _, err := Apply(root, "init", []Operation{{Path: "created", Directory: true, Mode: 0755}}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(filepath.Join(root, "created")); err != nil || !st.IsDir() {
		t.Fatalf("created directory missing: %v", err)
	}
	_, err := ApplyContextWithValidation(context.Background(), root, "init", []Operation{{Path: "rollback", Directory: true, Mode: 0755}}, nil, nil, func() error { return errors.New("stop") })
	if err == nil {
		t.Fatal("validation failure accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "rollback")); !os.IsNotExist(err) {
		t.Fatalf("owned empty directory not rolled back: %v", err)
	}
}

func TestDirectoryRecoveryRetainsUnknownContentsAndModeChange(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "guard"), 0755); err != nil {
		t.Fatal(err)
	}
	l := loaded{base: ".yss/transactions/0123456789abcdef", plan: plan{Guards: map[string]domain.Descriptor{"guard": {Type: "directory", Mode: 0755}}, Operations: []record{{Path: "created", Before: domain.Descriptor{Type: "missing"}, After: domain.Descriptor{Type: "directory", Mode: 0755}}}}}
	if err := preflightCreatedDirectories(root, l, 1); err != nil {
		t.Fatalf("unchanged guard rejected: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "created"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "created", "user.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := preflightCreatedDirectories(root, l, 1); err == nil {
		t.Fatal("unknown child accepted")
	}
	if b, err := os.ReadFile(filepath.Join(root, "created", "user.txt")); err != nil || string(b) != "keep" {
		t.Fatalf("user data changed: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "created", "user.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, "guard"), 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "guard"), 0755) })
	if err := preflightCreatedDirectories(root, l, 1); err == nil {
		t.Fatal("read-only directory change accepted")
	}
}

func TestTemporaryDirectoryRecoveryRecognizesInitialMode(t *testing.T) {
	root := t.TempDir()
	base := ".yss/transactions/0123456789abcdef"
	r := record{Path: "created", After: domain.Descriptor{Type: "directory", Mode: 0755}}
	ref := temporary(r.Path, base, 0, "apply")
	if err := os.Mkdir(filepath.Join(root, ref), 0700); err != nil {
		t.Fatal(err)
	}
	if err := tempCheck(root, base, 0, r, "apply", r.After); err != nil {
		t.Fatalf("initial empty directory rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, ref, "user.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := tempCheck(root, base, 0, r, "apply", r.After); err == nil {
		t.Fatal("unknown temporary content accepted")
	}
}
