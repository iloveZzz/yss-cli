package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestProgramRollbackReversesLatestInstallationAndRepeatIsUnchanged(t *testing.T) {
	file, sha := fixture(t, nil, runtime.GOOS+"/"+runtime.GOARCH)
	tool := filepath.Join(root(t), "tools")
	p, err := Build(tool, file, sha)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	out, err := Rollback(context.Background(), tool)
	if err != nil || out.Status != "rolled-back" || out.Kind != "program-update" {
		t.Fatalf("rollback: %+v %v", out, err)
	}
	for _, ref := range []string{receiptRef, fileName(), "README.md", "release-manifest.json"} {
		if _, err = os.Stat(filepath.Join(tool, ref)); !os.IsNotExist(err) {
			t.Fatalf("rollback retained %s: %v", ref, err)
		}
	}
	out, err = Rollback(context.Background(), tool)
	if err != nil || out.Status != "unchanged" {
		t.Fatalf("repeat rollback: %+v %v", out, err)
	}
}

func updateErrorCode(err error) string {
	var failure *domain.Error
	if errors.As(err, &failure) {
		return failure.Code
	}
	return ""
}

func installProgram(t *testing.T, tool string, files map[string][]byte) {
	t.Helper()
	file, sha := fixture(t, files, runtime.GOOS+"/"+runtime.GOARCH)
	p, err := Build(tool, file, sha)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func programFiles(readme string) map[string][]byte {
	return map[string][]byte{fileName(): []byte("native-binary"), "README.md": []byte(readme), "docs/source-lock.json": []byte(`{}`), "docs/compatibility.md": []byte("alpha")}
}

func TestProgramRollbackRestoresPreviousBytesModesAndStopsAtLatestHistory(t *testing.T) {
	tool := filepath.Join(root(t), "tools")
	installProgram(t, tool, nil)
	refs := []string{fileName(), "README.md", "docs/source-lock.json", "docs/compatibility.md", receiptRef, "release-manifest.json"}
	before := map[string]domain.Descriptor{}
	for _, ref := range refs {
		d, err := safefs.Describe(tool, ref)
		if err != nil {
			t.Fatal(err)
		}
		before[ref] = d
	}
	installProgram(t, tool, programFiles("new-release"))
	out, err := Rollback(context.Background(), tool)
	if err != nil || out.Status != "rolled-back" {
		t.Fatalf("upgrade rollback: %+v %v", out, err)
	}
	for _, ref := range refs {
		d, err := safefs.Describe(tool, ref)
		if err != nil || d != before[ref] {
			t.Fatalf("previous bytes/mode changed %s: %+v want %+v %v", ref, d, before[ref], err)
		}
	}
	out, err = Rollback(context.Background(), tool)
	if err != nil || out.Status != "unchanged" {
		t.Fatalf("repeat walked into old successful install: %+v %v", out, err)
	}
	if _, err = os.Stat(filepath.Join(tool, receiptRef)); err != nil {
		t.Fatal("repeat removed prior installation", err)
	}
	if _, err = transaction.Apply(tool, "sync", []transaction.Operation{{Path: "other-family", Data: []byte("other")}}); err != nil {
		t.Fatal(err)
	}
	if _, err = Rollback(context.Background(), tool); updateErrorCode(err) != "KIND" {
		t.Fatalf("later sync treated as program rollback: %v", err)
	}
	if _, err = transaction.Rollback(tool); err != nil {
		t.Fatal(err)
	}
	if _, err = Rollback(context.Background(), tool); updateErrorCode(err) != "KIND" {
		t.Fatalf("passed a later already rolled-back sync: %v", err)
	}
	if _, err = os.Stat(filepath.Join(tool, receiptRef)); err != nil {
		t.Fatal("foreign history removed previous install", err)
	}
}

func TestProgramRollbackPreservesManualByteAndModeChanges(t *testing.T) {
	for _, change := range []string{"bytes", "mode"} {
		t.Run(change, func(t *testing.T) {
			tool := filepath.Join(root(t), "tools")
			installProgram(t, tool, nil)
			path := filepath.Join(tool, "README.md")
			if change == "bytes" {
				if err := os.WriteFile(path, []byte("human edit"), 0644); err != nil {
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
			if _, err = Rollback(context.Background(), tool); updateErrorCode(err) != "CONCURRENT" {
				t.Fatalf("manual drift accepted: %v", err)
			}
			after, err := safefs.Describe(tool, "README.md")
			if err != nil || before != after {
				t.Fatalf("manual edit overwritten: %+v %+v %v", before, after, err)
			}
			if _, err = os.Stat(filepath.Join(tool, receiptRef)); err != nil {
				t.Fatal("failed rollback removed receipt", err)
			}
		})
	}
}

func TestProgramOperationsShareExplicitRootProtectionAndAllowNestedToolDirectory(t *testing.T) {
	file, sha := fixture(t, nil, runtime.GOOS+"/"+runtime.GOARCH)
	for _, marker := range []string{".git", "yss-project.yaml"} {
		t.Run(marker, func(t *testing.T) {
			tool := root(t)
			if err := os.WriteFile(filepath.Join(tool, marker), []byte("protected"), 0644); err != nil {
				t.Fatal(err)
			}
			calls := []func() error{
				func() error { _, e := Build(tool, file, sha); return e }, func() error { _, e := Status(tool); return e },
				func() error { _, e := Recover(context.Background(), tool); return e }, func() error { _, e := Rollback(context.Background(), tool); return e },
			}
			for _, call := range calls {
				if err := call(); updateErrorCode(err) != "PROTECTED" {
					t.Fatalf("root protection not shared: %v", err)
				}
			}
			if _, err := os.Stat(filepath.Join(tool, ".yss")); !os.IsNotExist(err) {
				t.Fatalf("blocked operation created state: %v", err)
			}
		})
	}
	parent := root(t)
	for _, marker := range []string{".git", "yss-project.yaml"} {
		if err := os.WriteFile(filepath.Join(parent, marker), []byte("parent protected"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tool := filepath.Join(parent, "tools", "yss")
	installProgram(t, tool, nil)
	if _, err := Status(tool); err != nil {
		t.Fatal(err)
	}
	if out, err := Recover(context.Background(), tool); err != nil || out.Status != "unchanged" {
		t.Fatalf("nested recover: %+v %v", out, err)
	}
	if _, err := Rollback(context.Background(), tool); err != nil {
		t.Fatal(err)
	}
}

func TestProgramRecoveryAndRollbackRequireRootAndRespectPreCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := filepath.Join(root(t), "absent")
	for _, call := range []func(context.Context, string) (transaction.Result, error){Recover, Rollback} {
		if _, err := call(context.Background(), ""); updateErrorCode(err) != "ARGUMENT" {
			t.Fatalf("implicit root accepted: %v", err)
		}
		if _, err := call(ctx, tool); updateErrorCode(err) != "CANCELLED" {
			t.Fatalf("cancelled operation: %v", err)
		}
		if _, err := call(nil, tool); updateErrorCode(err) != "ARGUMENT" {
			t.Fatalf("nil context: %v", err)
		}
	}
	if _, err := os.Stat(tool); !os.IsNotExist(err) {
		t.Fatalf("rejected operation created root: %v", err)
	}
}

func TestProgramRollbackRejectsBusinessPathEvenWhenKindMatches(t *testing.T) {
	tool := root(t)
	if _, err := transaction.Apply(tool, "program-update", []transaction.Operation{{Path: "business.go", Data: []byte("business-owned")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Rollback(context.Background(), tool); updateErrorCode(err) != "SCOPE" {
		t.Fatalf("same-kind business transaction accepted: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(tool, "business.go"))
	if err != nil || string(b) != "business-owned" {
		t.Fatalf("business file overwritten: %q %v", b, err)
	}
	// The generic transaction API continues to support ordinary user paths.
	if _, err = transaction.Rollback(tool); err != nil {
		t.Fatal(err)
	}
	if _, err = Rollback(context.Background(), tool); updateErrorCode(err) != "SCOPE" {
		t.Fatalf("already rolled-back foreign scope not rejected: %v", err)
	}
}

func TestProgramRecoverWithoutHistoryDoesNotCreateToolRoot(t *testing.T) {
	tool := filepath.Join(root(t), "absent")
	out, err := Recover(context.Background(), tool)
	if err != nil || out.Status != "unchanged" {
		t.Fatalf("empty recover: %+v %v", out, err)
	}
	if _, err = os.Stat(tool); !os.IsNotExist(err) {
		t.Fatalf("recover created empty root: %v", err)
	}
}

func TestProgramStatusWithoutInstallationShowsTransactionStateAndCreatesNothing(t *testing.T) {
	tool := filepath.Join(root(t), "absent")
	out, err := Status(tool)
	if err != nil {
		t.Fatal(err)
	}
	state, ok := out.(map[string]any)
	if !ok || state["installed"] != false {
		t.Fatalf("status: %+v", out)
	}
	txn, ok := state["transaction"].(transaction.Result)
	if !ok || len(txn.Pending) != 0 {
		t.Fatalf("native transaction summary missing: %+v", out)
	}
	if state["recoverable"] != false {
		t.Fatalf("empty history marked recoverable: %+v", out)
	}
	if _, err = os.Stat(tool); !os.IsNotExist(err) {
		t.Fatalf("status created empty root: %v", err)
	}
}
