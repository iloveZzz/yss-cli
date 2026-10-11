package transaction_test

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func crashOperations(root string) ([]transaction.Operation, error) {
	before, err := safefs.Describe(root, "first")
	if err != nil {
		return nil, err
	}
	ops := []transaction.Operation{{Path: "first", Before: &before, Data: []byte("after"), Mode: 0644}}
	// Observe a completed real producer write instead of racing hundreds of
	// writes: an arbitrary partial temporary is correctly refused by recovery.
	data := bytes.Repeat([]byte("payload"), 8192)
	for i := 0; i < 2; i++ {
		ops = append(ops, transaction.Operation{Path: fmt.Sprintf("generated/%03d", i), Data: data, Mode: 0600})
	}
	ops = append(ops, transaction.Operation{Path: ".yss.json", Data: []byte("{\"schemaVersion\":1,\"profile\":\"spec\"}\n"), Mode: 0644})
	return ops, nil
}

func TestTransactionCrashHelper(t *testing.T) {
	root := os.Getenv("YSS_TRANSACTION_CRASH_ROOT")
	partial := false
	if root == "" {
		root = os.Getenv("YSS_TRANSACTION_PARTIAL_CRASH_ROOT")
		partial = root != ""
	}
	if root == "" {
		return
	}
	defer transaction.SetTemporaryWriterForTest(func(f *os.File, b []byte) (int, error) {
		if partial && filepath.Dir(f.Name()) == filepath.Join(root, "generated") && strings.HasSuffix(f.Name(), "-apply.tmp") {
			n, err := f.Write(b[:len(b)/2])
			if err == nil {
				err = retainProducerWrite(f)
			}
			return n, err
		}
		n, err := f.Write(b)
		if err == nil && filepath.Dir(f.Name()) == filepath.Join(root, "generated") && strings.HasSuffix(f.Name(), "-apply.tmp") {
			if n != len(b) {
				return n, io.ErrShortWrite
			}
			err = retainProducerWrite(f)
		}
		return n, err
	})()
	ops, err := crashOperations(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Apply(root, "crash-test", ops); err != nil {
		t.Fatal(err)
	}
}

func TestKilledPartialProducerTemporaryRemainsRefused(t *testing.T) {
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "first"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	expected := bytes.Repeat([]byte("payload"), 4096)
	ref := killObservedProducer(t, root, "TestTransactionCrashHelper", "YSS_TRANSACTION_PARTIAL_CRASH_ROOT", func(ref string) {
		b, err := os.ReadFile(ref)
		if err != nil || !bytes.Equal(b, expected) {
			t.Fatalf("partial producer bytes not observed: %v", err)
		}
	})
	if _, err := transaction.Recover(root); errorCode(err) != "RECOVERY_FAILED" {
		t.Fatalf("ambiguous partial temporary was accepted: %v", err)
	}
	b, err := os.ReadFile(ref)
	if err != nil || !bytes.Equal(b, expected) {
		t.Fatal("refused temporary changed")
	}
	b, err = os.ReadFile(filepath.Join(root, "first"))
	if err != nil || string(b) != "after" {
		t.Fatal("refusal partially restored transaction")
	}
}

func startAndInterrupt(t *testing.T, root string) {
	t.Helper()
	observed := false
	killObservedProducer(t, root, "TestTransactionCrashHelper", "YSS_TRANSACTION_CRASH_ROOT", func(ref string) {
		b, err := os.ReadFile(filepath.Join(root, "first"))
		if err != nil || string(b) != "after" {
			t.Fatalf("first target not mutated: %q %v", b, err)
		}
		b, err = os.ReadFile(ref)
		if err != nil || !bytes.Equal(b, bytes.Repeat([]byte("payload"), 8192)) {
			t.Fatal("incomplete producer temporary", err)
		}
		if !observed {
			if _, err := transaction.Apply(root, "competing", []transaction.Operation{{Path: "competitor", Data: []byte("must not appear")}}); errorCode(err) != "LOCKED" {
				t.Fatalf("concurrent process did not retain exclusive ownership: %v", err)
			}
		}
		observed = true
	})
	state, err := transaction.Status(root)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "pending" || len(state.Pending) != 1 {
		t.Fatalf("crash did not leave an identifiable pending transaction: %+v", state)
	}
}

func TestKilledProcessRecoversWithOriginalBytesAndNoCreatedFiles(t *testing.T) {
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "first"), []byte("original"), 0751); err != nil {
		t.Fatal(err)
	}
	startAndInterrupt(t, root)
	out, err := transaction.Recover(root)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != "recovered" {
		t.Fatalf("unexpected recovery: %+v", out)
	}
	original, err := os.ReadFile(filepath.Join(root, "first"))
	if err != nil || string(original) != "original" {
		t.Fatalf("original not restored: %q %v", original, err)
	}
	info, err := os.Stat(filepath.Join(root, "first"))
	if err != nil || domain.FileMode(uint32(info.Mode().Perm())) != domain.FileMode(0751) {
		t.Fatalf("original mode not restored: %v %v", info, err)
	}
	files, _ := filepath.Glob(filepath.Join(root, "generated", "*"))
	if len(files) != 0 {
		t.Fatalf("new files survived recovery: %v", files)
	}
	state, err := transaction.Status(root)
	if err != nil || len(state.Pending) != 0 {
		t.Fatalf("recovery remained pending: %+v %v", state, err)
	}
}

func TestKilledProcessRecoveryPreservesLaterUserChange(t *testing.T) {
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "first"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	startAndInterrupt(t, root)
	if err := os.WriteFile(filepath.Join(root, "first"), []byte("user edit after crash"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Recover(root); errorCode(err) != "RECOVERY_FAILED" {
		t.Fatalf("recovery did not block user change: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "first"))
	if err != nil || string(b) != "user edit after crash" {
		t.Fatalf("recovery overwrote user change: %q %v", b, err)
	}
}
