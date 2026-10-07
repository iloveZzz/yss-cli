package transaction

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

func TestExplicitDirectoryGuardRemainsReadOnly(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "guarded")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	guards := map[string]domain.Descriptor{"guarded": {Type: "directory", Mode: uint32(info.Mode().Perm())}}
	result, err := ApplyWithGuards(root, "sync", nil, guards)
	if err != nil || result.Status != "unchanged" {
		t.Fatalf("explicit directory guard: %v %v", result, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_, err = ApplyWithGuards(root, "sync", nil, guards)
	var failure *domain.Error
	if !errors.As(err, &failure) || failure.Code != "INPUT_DRIFT" {
		t.Fatalf("removed directory must invalidate its guard: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".yss")); !os.IsNotExist(err) {
		t.Fatalf("read-only guard created transaction state: %v", err)
	}
}
