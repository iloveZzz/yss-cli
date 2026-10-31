package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

type cancelPrepared struct {
	context.Context
	root   string
	cancel context.CancelFunc
}

func (c *cancelPrepared) Err() error {
	if refs, _ := filepath.Glob(filepath.Join(c.root, ".yss/transactions/.preparing-*/plan.json")); len(refs) > 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func interruptedProgram(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(updaterTemp(t), "tool")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := transaction.ApplyContext(&cancelPrepared{Context: ctx, root: root, cancel: cancel}, root, "program-update", []transaction.Operation{{Path: "README.md", Data: []byte("new"), Mode: 0644, Before: &domain.Descriptor{Type: "missing"}}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected preparation cancellation: %v", err)
	}
	s, err := transaction.Status(root)
	if err != nil || len(s.Preparations) != 1 || len(s.Pending) != 0 {
		t.Fatalf("expected unpublished preparation: %+v %v", s, err)
	}
	return root, filepath.Join(root, ".yss/transactions", s.Preparations[0])
}

func TestInterruptedProgramRequiresReinstallation(t *testing.T) {
	root, preparation := interruptedProgram(t)
	status, err := Status(root)
	if err != nil {
		t.Fatal(err)
	}
	facts := status.(map[string]any)
	if facts["reinstallRequired"] != true {
		t.Fatalf("interrupted install must require reinstall: %v", facts)
	}
	if _, ok := facts["recoverable"]; ok {
		t.Fatalf("status must not offer recovery: %v", facts)
	}
	if _, err := os.Stat(preparation); err != nil {
		t.Fatalf("preparation evidence must be preserved: %v", err)
	}
}

func updaterTemp(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
