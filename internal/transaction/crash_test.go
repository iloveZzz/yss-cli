package transaction_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

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
	// Enough fsynced operations for the parent to terminate a real process between
	// target mutations. No production hooks or synthetic journal fixtures are used.
	data := bytes.Repeat([]byte("payload"), 8192)
	for i := 0; i < 300; i++ {
		ops = append(ops, transaction.Operation{Path: fmt.Sprintf("generated/%03d", i), Data: data, Mode: 0600})
	}
	ops = append(ops, transaction.Operation{Path: ".yss.json", Data: []byte("{\"schemaVersion\":1,\"profile\":\"spec\"}\n"), Mode: 0644})
	return ops, nil
}

func TestTransactionCrashHelper(t *testing.T) {
	root := os.Getenv("YSS_TRANSACTION_CRASH_ROOT")
	if root == "" {
		return
	}
	ops, err := crashOperations(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Apply(root, "crash-test", ops); err != nil {
		t.Fatal(err)
	}
}

func startAndInterrupt(t *testing.T, root string) {
	t.Helper()
	child := exec.Command(os.Args[0], "-test.run=^TestTransactionCrashHelper$")
	child.Env = append(os.Environ(), "YSS_TRANSACTION_CRASH_ROOT="+root)
	var output bytes.Buffer
	child.Stdout = &output
	child.Stderr = &output
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(root, "first"))
		if string(b) == "after" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not reach the first target mutation")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := transaction.Apply(root, "competing", []transaction.Operation{{Path: "competitor", Data: []byte("must not appear")}}); errorCode(err) != "LOCKED" {
		t.Fatalf("concurrent process did not retain exclusive ownership: %v", err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err := child.Wait()
	waited = true
	if err == nil {
		t.Fatalf("process finished before crash injection: %s", output.String())
	}
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
