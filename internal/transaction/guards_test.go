package transaction_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestInputDriftAndMissingBaselineNeverOverwriteExistingFiles(t *testing.T) {
	root := testRoot(t)
	for _, name := range []string{"first", "second"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("old"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := safefs.Describe(root, "first")
	second, _ := safefs.Describe(root, "second")
	if err := os.WriteFile(filepath.Join(root, "second"), []byte("user change"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: "first", Before: &first, Data: []byte("new")}, {Path: "second", Before: &second, Data: []byte("new")}}); errorCode(err) != "CONCURRENT" {
		t.Fatalf("input drift accepted: %v", err)
	}
	if _, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: "first", Data: []byte("unknown overwrite")}}); errorCode(err) != "CONCURRENT" {
		t.Fatalf("nil baseline accepted existing content: %v", err)
	}
	for name, wanted := range map[string]string{"first": "old", "second": "user change"} {
		b, e := os.ReadFile(filepath.Join(root, name))
		if e != nil || string(b) != wanted {
			t.Fatalf("preflight modified %s: %q %v", name, b, e)
		}
	}
}

func TestReadOnlyFactGuardsRejectDriftAndContradictoryWriteBaseline(t *testing.T) {
	root := testRoot(t)
	if e := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("approved vocabulary"), 0644); e != nil {
		t.Fatal(e)
	}
	d, e := safefs.Describe(root, "CONTEXT.md")
	if e != nil {
		t.Fatal(e)
	}
	guards := map[string]domain.Descriptor{"CONTEXT.md": d}
	if e = os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("new vocabulary"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = transaction.ApplyWithGuards(root, "stage-register", []transaction.Operation{{Path: "checkpoint.json", Data: []byte("planned")}}, guards); errorCode(e) != "INPUT_DRIFT" {
		t.Fatalf("guard drift accepted: %v", e)
	}
	if _, e = os.Stat(filepath.Join(root, "checkpoint.json")); !os.IsNotExist(e) {
		t.Fatal("drifted transaction wrote target")
	}
	actual, e := safefs.Describe(root, "CONTEXT.md")
	if e != nil {
		t.Fatal(e)
	}
	guards["CONTEXT.md"] = actual
	if _, e = transaction.ApplyWithGuards(root, "invalid", []transaction.Operation{{Path: "CONTEXT.md", Before: &d, Data: []byte("bad")}}, guards); errorCode(e) != "PLAN" {
		t.Fatalf("contradictory write guard accepted: %v", e)
	}
	if _, e = transaction.ApplyWithGuards(root, "stage-register", []transaction.Operation{{Path: "checkpoint.json", Data: []byte("current")}}, guards); e != nil {
		t.Fatal(e)
	}
	status, e := transaction.Status(root)
	if e != nil || len(status.Transactions) != 1 {
		t.Fatalf("bound guards not durable: %+v %v", status, e)
	}
}

func TestGuardDriftDuringApplyRestoresTargetsAndPreservesFactEdit(t *testing.T) {
	root := testRoot(t)
	if e := os.WriteFile(filepath.Join(root, "first"), []byte("original"), 0644); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("old"), 0644); e != nil {
		t.Fatal(e)
	}
	d, e := safefs.Describe(root, "CONTEXT.md")
	if e != nil {
		t.Fatal(e)
	}
	ops, e := crashOperations(root)
	if e != nil {
		t.Fatal(e)
	}
	type completion struct {
		out transaction.Result
		err error
	}
	done := make(chan completion, 1)
	go func() {
		out, e := transaction.ApplyWithGuards(root, "sync", ops, map[string]domain.Descriptor{"CONTEXT.md": d})
		done <- completion{out, e}
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(root, "first"))
		if string(b) == "after" {
			break
		}
		if time.Now().After(deadline) {
			<-done
			t.Fatal("apply did not reach target writes")
		}
		time.Sleep(time.Millisecond)
	}
	if e = os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("human edit"), 0644); e != nil {
		t.Fatal(e)
	}
	result := <-done
	if errorCode(result.err) != "INPUT_DRIFT" || result.out.Status != "recovered" {
		t.Fatalf("drift did not restore targets: %+v %v", result.out, result.err)
	}
	for ref, want := range map[string]string{"first": "original", "CONTEXT.md": "human edit"} {
		b, e := os.ReadFile(filepath.Join(root, ref))
		if e != nil || string(b) != want {
			t.Fatalf("unexpected %s after recovery: %q %v", ref, b, e)
		}
	}
}

func TestLatestSuccessfulTransactionCanOnlyBeRolledBackOnce(t *testing.T) {
	root := testRoot(t)
	if _, err := transaction.Apply(root, "init", []transaction.Operation{{Path: "asset", Data: []byte("first")}}); err != nil {
		t.Fatal(err)
	}
	first, _ := safefs.Describe(root, "asset")
	if _, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: "asset", Before: &first, Data: []byte("second")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	result, err := transaction.Rollback(root)
	if err != nil || result.Status != "unchanged" {
		t.Fatalf("older transaction became eligible: %+v %v", result, err)
	}
	b, err := os.ReadFile(filepath.Join(root, "asset"))
	if err != nil || string(b) != "first" {
		t.Fatalf("rollback went beyond latest successful transaction: %q %v", b, err)
	}
}

func TestDeletedFileRestoresFromArchivedOriginalAndCorruptionBlocksRollback(t *testing.T) {
	root := testRoot(t)
	original := []byte{0xff, 0, '\r', '\n'}
	if err := os.WriteFile(filepath.Join(root, "asset"), original, 0751); err != nil {
		t.Fatal(err)
	}
	before, _ := safefs.Describe(root, "asset")
	out, err := transaction.Apply(root, "prune", []transaction.Operation{{Path: "asset", Before: &before, Delete: true}})
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(out.BackupPath, "objects", safefs.Digest(original))
	if err := os.WriteFile(archive, []byte("corrupt archive"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Rollback(root); errorCode(err) != "STATE" {
		t.Fatalf("archive corruption accepted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "asset")); !os.IsNotExist(err) {
		t.Fatal("corrupted archive was restored")
	}
	if err := os.WriteFile(archive, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "asset"))
	if err != nil || string(b) != string(original) {
		t.Fatalf("original binary bytes not restored: %v %v", b, err)
	}
}

func TestCancelledActiveTransactionRestoresAllTouchedFiles(t *testing.T) {
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "first"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	ops, err := crashOperations(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type completion struct {
		result transaction.Result
		err    error
	}
	done := make(chan completion, 1)
	go func() {
		out, err := transaction.ApplyContext(ctx, root, "cancel-test", ops)
		done <- completion{out, err}
	}()
	deadline := time.Now().Add(20 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(root, "first"))
		if string(b) == "after" {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("apply did not reach cancellable target writes")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	out := <-done
	if !errors.Is(out.err, context.Canceled) || out.result.Status != "recovered" {
		t.Fatalf("cancellation did not restore transaction: %+v %v", out.result, out.err)
	}
	b, err := os.ReadFile(filepath.Join(root, "first"))
	if err != nil || string(b) != "original" {
		t.Fatalf("cancellation did not restore original: %q %v", b, err)
	}
	files, _ := filepath.Glob(filepath.Join(root, "generated", "*"))
	if len(files) != 0 {
		t.Fatalf("cancelled file creations survived: %v", files)
	}
}

func TestStatusIsReadOnlyAndProtectedPathsAreRejected(t *testing.T) {
	base := testRoot(t)
	missing := filepath.Join(base, "missing")
	if _, err := transaction.Status(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("status created a project root")
	}
	for _, ref := range []string{".git/index", "../escape", ".yss/transactions/plan.json", ".YSS/transactions/plan.json"} {
		if _, err := transaction.Apply(base, "sync", []transaction.Operation{{Path: ref, Data: []byte("bad")}}); err == nil {
			t.Fatalf("protected path accepted: %s", ref)
		}
	}
	if err := os.MkdirAll(filepath.Join(base, "nested/.git"), 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Apply(base, "sync", []transaction.Operation{{Path: "nested/asset", Data: []byte("bad")}}); errorCode(err) != "PROTECTED" {
		t.Fatalf("nested Git repository accepted: %v", err)
	}
}
