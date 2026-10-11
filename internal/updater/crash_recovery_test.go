package updater

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

// This helper exercises the real public installation API in a process that the
// parent terminates after an observed real preparation or target mutation.
func TestProgramCrashHelper(t *testing.T) {
	tool := os.Getenv("YSS_TEST_PROGRAM_ROOT")
	if tool == "" {
		return
	}
	p, err := Build(tool, os.Getenv("YSS_TEST_PROGRAM_ARCHIVE"), os.Getenv("YSS_TEST_PROGRAM_SHA"))
	if err != nil {
		t.Fatal(err)
	}
	var ctx context.Context = context.Background()
	if os.Getenv("YSS_TEST_PREPARATION_BARRIER") == "1" {
		ctx = &preparationObservationContext{Context: ctx, root: tool}
	}
	if _, err = Apply(ctx, p); err != nil {
		t.Fatal(err)
	}
}

// The public Context seam observes the producer's persisted plan at a real
// cancellation checkpoint. It never fabricates a journal or changes Apply.
// Holding stdin open lets the parent terminate this exact unpublished state.
type preparationObservationContext struct {
	context.Context
	root string
	once sync.Once
}

func (c *preparationObservationContext) Err() error {
	paths, err := filepath.Glob(filepath.Join(c.root, ".yss", "transactions", ".preparing-*"))
	if err != nil {
		return err
	}
	if len(paths) == 1 {
		c.once.Do(func() {
			if err := json.NewEncoder(os.Stdout).Encode(paths[0]); err != nil {
				panic(err)
			}
			if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
				panic(err)
			}
		})
	}
	return c.Context.Err()
}

func interruptProgram(t *testing.T, tool string) {
	t.Helper()
	files := programFiles("interrupted-release")
	// The large second target provides a real write interval after the small
	// README commit and before receipt mutation. It remains within archive limits.
	files["docs/compatibility.md"] = bytes.Repeat([]byte("release-data-"), (32<<20)/len("release-data-"))
	archive, sha := fixture(t, files, runtime.GOOS+"/"+runtime.GOARCH)
	child := exec.Command(os.Args[0], "-test.run=^TestProgramCrashHelper$")
	child.Env = append(os.Environ(), "YSS_TEST_PROGRAM_ROOT="+tool, "YSS_TEST_PROGRAM_ARCHIVE="+archive, "YSS_TEST_PROGRAM_SHA="+sha)
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
	deadline := time.Now().Add(30 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(tool, "README.md"))
		if string(b) == "interrupted-release" {
			break
		}
		if time.Now().After(deadline) {
			_ = child.Process.Kill()
			_ = child.Wait()
			waited = true
			t.Fatalf("child did not reach program target: %s", output.String())
		}
		time.Sleep(time.Millisecond)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err := child.Wait()
	waited = true
	if err == nil {
		t.Fatalf("child completed before interruption: %s", output.String())
	}
	state, err := transaction.Status(tool)
	if err != nil || len(state.Pending) != 1 {
		t.Fatalf("real program interruption missing: %+v %v; %s", state, err, output.String())
	}
}

func assertProgramPendingStatus(t *testing.T, tool string, installed bool) {
	t.Helper()
	out, err := Status(tool)
	if err != nil {
		t.Fatal(err)
	}
	state, ok := out.(map[string]any)
	if !ok || state["installed"] != installed || state["recoverable"] != true {
		t.Fatalf("pending installation hidden: %+v", out)
	}
	if state["recoveryRequired"] != true || state["recoveryKindMatches"] != true || state["recoveryPreflightPerformed"] != false {
		t.Fatalf("status claimed complete recovery preflight: %+v", out)
	}
	txn, ok := state["transaction"].(transaction.Result)
	if !ok || txn.Status != "pending" || len(txn.Pending) != 1 {
		t.Fatalf("native pending missing: %+v", out)
	}
	if len(txn.Transactions) == 0 || txn.Transactions[len(txn.Transactions)-1].Kind != "program-update" {
		t.Fatalf("program pending family missing: %+v", out)
	}
}

func TestProgramRecoverInterruptedFirstInstallationWithoutReceipt(t *testing.T) {
	tool := filepath.Join(root(t), "tools")
	interruptProgram(t, tool)
	if _, err := os.Stat(filepath.Join(tool, receiptRef)); !os.IsNotExist(err) {
		t.Fatalf("first-install crash did not precede receipt: %v", err)
	}
	assertProgramPendingStatus(t, tool, false)
	out, err := Recover(context.Background(), tool)
	if err != nil || out.Status != "recovered" || out.Kind != "program-update" {
		t.Fatalf("first recovery: %+v %v", out, err)
	}
	for _, ref := range []string{receiptRef, fileName(), "README.md", "docs/source-lock.json", "docs/compatibility.md", "release-manifest.json"} {
		if _, err = os.Stat(filepath.Join(tool, ref)); !os.IsNotExist(err) {
			t.Fatalf("first recovery retained %s: %v", ref, err)
		}
	}
	out, err = Recover(context.Background(), tool)
	if err != nil || out.Status != "unchanged" {
		t.Fatalf("repeat recovery: %+v %v", out, err)
	}
}

func TestProgramRecoverInterruptedUpgradeRestoresOriginalBytesAndModes(t *testing.T) {
	tool := filepath.Join(root(t), "tools")
	installProgram(t, tool, nil)
	refs := []string{receiptRef, fileName(), "README.md", "docs/source-lock.json", "docs/compatibility.md", "release-manifest.json"}
	before := map[string]domain.Descriptor{}
	for _, ref := range refs {
		d, err := safefs.Describe(tool, ref)
		if err != nil {
			t.Fatal(err)
		}
		before[ref] = d
	}
	interruptProgram(t, tool)
	assertProgramPendingStatus(t, tool, true)
	out, err := Recover(context.Background(), tool)
	if err != nil || out.Status != "recovered" {
		t.Fatalf("upgrade recovery: %+v %v", out, err)
	}
	for _, ref := range refs {
		d, err := safefs.Describe(tool, ref)
		if err != nil || d != before[ref] {
			t.Fatalf("upgrade recovery changed prior file %s: %+v want %+v %v", ref, d, before[ref], err)
		}
	}
}

func TestProgramRecoverInterruptedInstallationPreservesManualChanges(t *testing.T) {
	for _, change := range []string{"bytes", "mode"} {
		t.Run(change, func(t *testing.T) {
			tool := filepath.Join(root(t), "tools")
			interruptProgram(t, tool)
			path := filepath.Join(tool, "README.md")
			if change == "bytes" {
				if err := os.WriteFile(path, []byte("human after interruption"), 0644); err != nil {
					t.Fatal(err)
				}
			} else {
				mode := os.FileMode(0600)
				if runtime.GOOS == "windows" {
					mode = 0444
				}
				if err := os.Chmod(path, mode); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0644) })
			}
			before, err := safefs.Describe(tool, "README.md")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = Recover(context.Background(), tool); updateErrorCode(err) != "RECOVERY_FAILED" {
				t.Fatalf("manual change accepted: %v", err)
			}
			after, err := safefs.Describe(tool, "README.md")
			if err != nil || after != before {
				t.Fatalf("manual change overwritten: %+v %+v %v", before, after, err)
			}
			state, err := transaction.Status(tool)
			if err != nil || len(state.Pending) != 1 {
				t.Fatalf("failed restore removed recovery history: %+v %v", state, err)
			}
		})
	}
}

func TestProgramScopeCrashHelper(t *testing.T) {
	tool := os.Getenv("YSS_TEST_SCOPE_ROOT")
	if tool == "" {
		return
	}
	before, err := safefs.Describe(tool, "business.go")
	if err != nil {
		t.Fatal(err)
	}
	ops := []transaction.Operation{{Path: "business.go", Before: &before, Data: []byte("after"), Mode: 0644}}
	for i := 0; i < 300; i++ {
		ops = append(ops, transaction.Operation{Path: fmt.Sprintf("generated/%03d", i), Data: bytes.Repeat([]byte("payload"), 8192), Mode: 0644})
	}
	if _, err = transaction.Apply(tool, os.Getenv("YSS_TEST_SCOPE_KIND"), ops); err != nil {
		t.Fatal(err)
	}
}

func interruptScopedTransaction(t *testing.T, tool, kind string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(tool, "business.go"), []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestProgramScopeCrashHelper$")
	child.Env = append(os.Environ(), "YSS_TEST_SCOPE_ROOT="+tool, "YSS_TEST_SCOPE_KIND="+kind)
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
		b, _ := os.ReadFile(filepath.Join(tool, "business.go"))
		if string(b) == "after" {
			break
		}
		if time.Now().After(deadline) {
			_ = child.Process.Kill()
			_ = child.Wait()
			waited = true
			t.Fatalf("scope fixture not reached: %s", output.String())
		}
		time.Sleep(time.Millisecond)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err := child.Wait()
	waited = true
	if err == nil {
		t.Fatalf("scope child completed before interruption: %s", output.String())
	}
	state, err := transaction.Status(tool)
	if err != nil || len(state.Pending) != 1 {
		t.Fatalf("scope pending not present: %+v %v", state, err)
	}
}

func TestProgramRecoverRejectsSameKindBusinessArchive(t *testing.T) {
	tool := root(t)
	interruptScopedTransaction(t, tool, "program-update")
	if _, err := Recover(context.Background(), tool); updateErrorCode(err) != "SCOPE" {
		t.Fatalf("same-kind business recovery accepted: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(tool, "business.go"))
	if err != nil || string(b) != "after" {
		t.Fatalf("scope rejection restored business: %q %v", b, err)
	}
	state, err := transaction.Status(tool)
	if err != nil || len(state.Pending) != 1 {
		t.Fatalf("scope rejection lost pending history: %+v %v", state, err)
	}
	if _, err = transaction.Recover(tool); err != nil {
		t.Fatal(err)
	}
	b, err = os.ReadFile(filepath.Join(tool, "business.go"))
	if err != nil || string(b) != "original" {
		t.Fatalf("generic recovery failed ordinary path: %q %v", b, err)
	}
}

func TestProgramStatusReportsForeignPendingWithoutClaimingRecovery(t *testing.T) {
	tool := root(t)
	interruptScopedTransaction(t, tool, "sync")
	out, err := Status(tool)
	if err != nil {
		t.Fatal(err)
	}
	state := out.(map[string]any)
	if state["installed"] != false || state["recoveryRequired"] != true || state["recoveryKindMatches"] != false || state["recoverable"] != false || state["recoveryPreflightPerformed"] != false {
		t.Fatalf("foreign status: %+v", out)
	}
	kinds, ok := state["recoveryBlockedKinds"].([]string)
	if !ok || len(kinds) != 1 || kinds[0] != "sync" {
		t.Fatalf("foreign family omitted: %+v", out)
	}
	txn, ok := state["transaction"].(transaction.Result)
	if !ok || len(txn.Pending) != 1 {
		t.Fatalf("foreign pending hidden: %+v", out)
	}
	if _, err = Recover(context.Background(), tool); updateErrorCode(err) != "KIND" {
		t.Fatalf("foreign kind restored: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(tool, "business.go"))
	if err != nil || string(b) != "after" {
		t.Fatalf("foreign kind changed: %q %v", b, err)
	}
}

func TestProgramStatusRetainsUnpublishedPreparationSummary(t *testing.T) {
	tool := filepath.Join(root(t), "tools")
	archive, sha := fixture(t, programFiles("preparation-only"), runtime.GOOS+"/"+runtime.GOARCH)
	child := exec.Command(os.Args[0], "-test.run=^TestProgramCrashHelper$")
	child.Env = append(os.Environ(), "YSS_TEST_PROGRAM_ROOT="+tool, "YSS_TEST_PROGRAM_ARCHIVE="+archive, "YSS_TEST_PROGRAM_SHA="+sha, "YSS_TEST_PREPARATION_BARRIER=1")
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	child.Stderr = &stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		_ = input.Close()
		if !waited {
			_ = child.Process.Kill()
			_ = child.Wait()
		}
	})
	type observation struct {
		path string
		err  error
	}
	ready := make(chan observation, 1)
	go func() {
		var ref string
		err := json.NewDecoder(output).Decode(&ref)
		ready <- observation{ref, err}
	}()
	var preparing string
	select {
	case observed := <-ready:
		if observed.err != nil {
			t.Fatalf("producer did not report preparation: %v", observed.err)
		}
		preparing = observed.path
	case <-time.After(30 * time.Second):
		t.Fatal("producer did not reach preparation checkpoint")
	}
	if filepath.Dir(preparing) != filepath.Join(tool, ".yss", "transactions") || !strings.HasPrefix(filepath.Base(preparing), ".preparing-") {
		t.Fatalf("producer reported unrelated preparation: %q", preparing)
	}
	before, err := os.ReadFile(filepath.Join(preparing, "plan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	err = child.Wait()
	waited = true
	if err == nil {
		t.Fatalf("child finished before preparation interruption: %s", stderr.String())
	}
	afterPlan, err := os.ReadFile(filepath.Join(preparing, "plan.json"))
	if err != nil || !bytes.Equal(before, afterPlan) {
		t.Fatalf("killed producer's preparation changed: %v", err)
	}
	out, err := Status(tool)
	if err != nil {
		t.Fatal(err)
	}
	state := out.(map[string]any)
	txn, ok := state["transaction"].(transaction.Result)
	if state["installed"] != false || state["recoveryPreflightPerformed"] != false || !ok || len(txn.Preparations) != 1 || len(txn.Pending) != 0 {
		t.Fatalf("unpublished preparation hidden: %+v", out)
	}
	if _, err = os.Stat(filepath.Join(tool, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("prepublication interruption wrote target: %v", err)
	}
	outRecovery, err := Recover(context.Background(), tool)
	if err != nil || outRecovery.Status != "unchanged" || len(outRecovery.Preparations) != 1 {
		t.Fatalf("preparation-only recovery: %+v %v", outRecovery, err)
	}
	stateAfter, err := Status(tool)
	if err != nil {
		t.Fatal(err)
	}
	after := stateAfter.(map[string]any)["transaction"].(transaction.Result)
	if len(after.Preparations) != 1 {
		t.Fatalf("read-only status removed preparation: %+v", stateAfter)
	}
}
