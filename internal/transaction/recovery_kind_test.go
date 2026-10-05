package transaction_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestRecoverKindEmptyRootCreatesNoStateAndCancelledRequestIsReadOnly(t *testing.T) {
	root := filepath.Join(testRoot(t), "absent")
	out, err := transaction.RecoverKind(root, "program-update")
	if err != nil || out.Status != "unchanged" {
		t.Fatalf("empty recovery: %+v %v", out, err)
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("recovery created empty root: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = transaction.RecoverKindContext(ctx, root, "program-update"); errorCode(err) != "CANCELLED" {
		t.Fatalf("cancelled recovery: %v", err)
	}
	if _, err = os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("cancelled recovery created root: %v", err)
	}
}

func TestRecoverKindRefusesForeignPendingBeforeRestoringMatchingKind(t *testing.T) {
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "first"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	startAndInterrupt(t, root)
	out, err := transaction.RecoverKind(root, "program-update")
	if errorCode(err) != "KIND" || out.Kind != "crash-test" {
		t.Fatalf("foreign recovery: %+v %v", out, err)
	}
	b, err := os.ReadFile(filepath.Join(root, "first"))
	if err != nil || string(b) != "after" {
		t.Fatalf("foreign pending was modified: %q %v", b, err)
	}
	state, err := transaction.Status(root)
	if err != nil || len(state.Pending) != 1 {
		t.Fatalf("foreign pending disappeared: %+v %v", state, err)
	}
	out, err = transaction.RecoverKindContext(context.Background(), root, "crash-test")
	if err != nil || out.Status != "recovered" {
		t.Fatalf("matching recovery: %+v %v", out, err)
	}
	b, err = os.ReadFile(filepath.Join(root, "first"))
	if err != nil || string(b) != "original" {
		t.Fatalf("original not restored: %q %v", b, err)
	}
}

func TestRecoverKindScopeValidatorAndCancellationRunUnderLock(t *testing.T) {
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "first"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	startAndInterrupt(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	out, err := transaction.RecoverKindContextWithValidator(ctx, root, "crash-test", func(summary transaction.Summary, paths []string) error {
		called = true
		if summary.Kind != "crash-test" || summary.Operations != len(paths) || len(paths) < 2 || paths[0] != "first" {
			t.Fatalf("scope callback metadata: %+v %v", summary, paths)
		}
		if _, err := transaction.Apply(root, "competing", []transaction.Operation{{Path: "competitor", Data: []byte("bad")}}); errorCode(err) != "LOCKED" {
			t.Fatalf("validator did not hold exclusive lock: %v", err)
		}
		cancel()
		return nil
	})
	if !called || errorCode(err) != "CANCELLED" || out.Status != "cancelled" {
		t.Fatalf("locked cancellation: %+v %v callback=%v", out, err, called)
	}
	b, err := os.ReadFile(filepath.Join(root, "first"))
	if err != nil || string(b) != "after" {
		t.Fatalf("cancelled recovery wrote targets: %q %v", b, err)
	}
	if _, err = transaction.RecoverKind(root, "crash-test"); err != nil {
		t.Fatal(err)
	}
}

func TestRollbackKindValidatorOwnsLockCancelsAndReceivesDetachedPaths(t *testing.T) {
	root := testRoot(t)
	if _, err := transaction.Apply(root, "program-update", []transaction.Operation{{Path: "first", Data: []byte("after")}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	called := false
	out, err := transaction.RollbackKindContextWithValidator(ctx, root, "program-update", func(summary transaction.Summary, paths []string) error {
		called = true
		if summary.Kind != "program-update" || summary.Phase != "committed" || len(paths) != 1 || paths[0] != "first" {
			t.Fatalf("rollback callback: %+v %v", summary, paths)
		}
		if _, err := transaction.Apply(root, "competing", []transaction.Operation{{Path: "competitor", Data: []byte("bad")}}); errorCode(err) != "LOCKED" {
			t.Fatalf("rollback validator not locked: %v", err)
		}
		paths[0] = "cannot-change-archive"
		cancel()
		return nil
	})
	if !called || errorCode(err) != "CANCELLED" || out.Status != "cancelled" {
		t.Fatalf("rollback cancellation: %+v %v callback=%v", out, err, called)
	}
	b, err := os.ReadFile(filepath.Join(root, "first"))
	if err != nil || string(b) != "after" {
		t.Fatalf("cancelled rollback wrote targets: %q %v", b, err)
	}
	if _, err = transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(root, "first")); !os.IsNotExist(err) {
		t.Fatalf("callback altered operation archive: %v", err)
	}
	called = false
	_, err = transaction.RollbackKindContextWithValidator(context.Background(), root, "program-update", func(summary transaction.Summary, paths []string) error {
		called = true
		if summary.Phase != "rolled-back" {
			t.Fatalf("latest phase changed: %+v", summary)
		}
		return domain.Fail("SCOPE", "rejected archived scope")
	})
	if !called || errorCode(err) != "SCOPE" {
		t.Fatalf("latest already rolled-back scope was skipped: callback=%v %v", called, err)
	}
	if out, err = transaction.Rollback(root); err != nil || out.Status != "unchanged" {
		t.Fatalf("generic repeat changed: %+v %v", out, err)
	}
}
