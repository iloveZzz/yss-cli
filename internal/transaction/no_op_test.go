package transaction_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

type noOpFile struct {
	Mode   os.FileMode
	MTime  int64
	Digest string
}

func noOpTree(t *testing.T, root string) map[string]noOpFile {
	return noOpTreeWithHeldMutex(t, root, nil)
}

func noOpTreeWithHeldMutex(t *testing.T, root string, mutex *noOpFile) map[string]noOpFile {
	t.Helper()
	out := map[string]noOpFile{}
	if err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		ref, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		v := noOpFile{Mode: info.Mode(), MTime: info.ModTime().UnixNano()}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			v.Digest = safefs.Digest([]byte(link))
		} else if mutex != nil && filepath.ToSlash(ref) == ".yss/transactions/.lock" {
			// Windows LockFileEx denies reads of the locked byte range. The
			// real child readiness proves the lock is held; stat remains checked
			// here, and exact mutex bytes are compared before/after release.
			if !info.Mode().IsRegular() || info.Size() != 0 || v.Mode != mutex.Mode || v.MTime != mutex.MTime {
				return errors.New("held mutex type/size/mode/mtime changed")
			}
			v.Digest = mutex.Digest
		} else if !info.IsDir() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			v.Digest = safefs.Digest(data)
		}
		out[filepath.ToSlash(ref)] = v
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func assertNoOpTree(t *testing.T, root string, before map[string]noOpFile) {
	t.Helper()
	after := noOpTree(t, root)
	if !reflect.DeepEqual(before, after) {
		for ref, want := range before {
			if got, ok := after[ref]; !ok || got != want {
				t.Fatalf("no-op changed whole-tree bytes/mode/mtime at %s: before=%+v after=%+v", ref, want, got)
			}
		}
		t.Fatalf("no-op created an asset: before=%d after=%d", len(before), len(after))
	}
}

func noOpFixture(t *testing.T) string {
	t.Helper()
	root := testRoot(t)
	if _, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: "asset", Data: []byte("managed bytes"), Mode: 0644}}); err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct {
		ref, data string
		mode      os.FileMode
	}{{"CONTEXT.md", "user vocabulary", 0644}, {"src/business.sh", "business bytes", 0751}, {".git/index", "index sentinel bytes", 0600}, {".github/workflows/user.yml", "user workflow", 0644}} {
		p := filepath.Join(root, f.ref)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.data), f.mode); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func noOpGuard(t *testing.T, root, ref string) map[string]domain.Descriptor {
	t.Helper()
	d, err := safefs.Describe(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]domain.Descriptor{ref: d}
}

func TestNoOpReplayPreservesWholeTreeBytesModeAndMTime(t *testing.T) {
	root := noOpFixture(t)
	// Fixed prior timestamps make even a rapid lock.json create/unlink visible.
	old := time.Unix(1000000000, 123000000)
	if err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(p, old, old)
	}); err != nil {
		t.Fatal(err)
	}
	before := noOpTree(t, root)
	for i := 0; i < 2; i++ {
		result, err := transaction.ApplyWithGuards(root, "skills", nil, noOpGuard(t, root, "CONTEXT.md"))
		if err != nil || result.Status != "unchanged" {
			t.Fatalf("satisfied replay failed: %+v %v", result, err)
		}
		assertNoOpTree(t, root, before)
	}
}

func TestNoOpFreshRootValidatesWithoutCreatingState(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing-root", true: "absent-root"}[absent], func(t *testing.T) {
			parent := testRoot(t)
			root := parent
			guards := map[string]domain.Descriptor{"missing-fact": {Type: "missing"}}
			if absent {
				root = filepath.Join(parent, "not-created")
			} else {
				if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("current"), 0644); err != nil {
					t.Fatal(err)
				}
				guards = noOpGuard(t, root, "CONTEXT.md")
			}
			before := noOpTree(t, parent)
			result, err := transaction.ApplyWithGuards(root, "sync", nil, guards)
			if err != nil || result.Status != "unchanged" {
				t.Fatalf("fresh zero-op: %+v %v", result, err)
			}
			assertNoOpTree(t, parent, before)
			if _, err := os.Lstat(filepath.Join(root, ".yss")); !os.IsNotExist(err) {
				t.Fatalf("fresh no-op created state: %v", err)
			}
		})
	}
}

func TestNoOpGuardSchemaPathAndTypeRefusalsWriteNothing(t *testing.T) {
	for _, name := range []string{"bytes", "mode", "missing", "descriptor", "protected", "traversal", "case-alias", "directory", "symlink", "cancelled"} {
		t.Run(name, func(t *testing.T) {
			root := noOpFixture(t)
			guards := noOpGuard(t, root, "CONTEXT.md")
			want := "INPUT_DRIFT"
			ctx := context.Background()
			switch name {
			case "bytes":
				if err := os.WriteFile(filepath.Join(root, "CONTEXT.md"), []byte("later user bytes"), 0644); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(filepath.Join(root, "CONTEXT.md"), 0444); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "CONTEXT.md"), 0644) })
			case "missing":
				if err := os.Remove(filepath.Join(root, "CONTEXT.md")); err != nil {
					t.Fatal(err)
				}
			case "descriptor":
				guards["CONTEXT.md"] = domain.Descriptor{Type: "file", Digest: "invalid", Mode: 0644}
				want = "PLAN"
			case "protected":
				guards = map[string]domain.Descriptor{".git/index": {Type: "missing"}}
				want = "PATH"
			case "traversal":
				guards = map[string]domain.Descriptor{"../outside": {Type: "missing"}}
				want = "PATH"
			case "case-alias":
				guards = map[string]domain.Descriptor{"Docs/a": {Type: "missing"}, "docs/b": {Type: "missing"}}
				want = "PLAN"
			case "directory":
				if err := os.Remove(filepath.Join(root, "CONTEXT.md")); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(filepath.Join(root, "CONTEXT.md"), 0755); err != nil {
					t.Fatal(err)
				}
				want = "PATH"
			case "symlink":
				if err := os.Remove(filepath.Join(root, "CONTEXT.md")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("asset", filepath.Join(root, "CONTEXT.md")); err != nil {
					if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
						t.Skip("Windows symlink privilege unavailable")
					}
					t.Fatal(err)
				}
				want = "PATH"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = ""
			}
			before := noOpTree(t, root)
			_, err := transaction.ApplyContextWithGuards(ctx, root, "sync", nil, guards)
			if name == "cancelled" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancelled context accepted: %v", err)
				}
			} else if errorCode(err) != want {
				t.Fatalf("no-op accepted %s: %v want %s", name, err, want)
			}
			assertNoOpTree(t, root, before)
		})
	}
}

// The child holds a genuine recovery mutex, whose normal diagnostic lock.json
// also proves that the writing protocol remains unchanged.
func TestNoOpHeldLockHelper(t *testing.T) {
	root := os.Getenv("YSS_NOOP_LOCK_ROOT")
	if root == "" {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := transaction.RollbackKindContextWithValidator(ctx, root, "sync", func(transaction.Summary, []string) error {
		if _, err := io.WriteString(os.Stdout, "NOOP_LOCK_HELD\n"); err != nil {
			return err
		}
		_, err := io.Copy(io.Discard, os.Stdin)
		cancel()
		return err
	})
	if errorCode(err) != "CANCELLED" {
		t.Fatalf("child did not cancel under its real lock: %v", err)
	}
}

func TestNoOpLiveSubprocessLockRefusesWithoutTouchingDiagnostic(t *testing.T) {
	root := noOpFixture(t)
	mutexBefore := noOpTree(t, root)[".yss/transactions/.lock"]
	child := exec.Command(os.Args[0], "-test.run=^TestNoOpHeldLockHelper$")
	child.Env = append(os.Environ(), "YSS_NOOP_LOCK_ROOT="+root)
	input, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
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
	ready := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		for {
			line, err := reader.ReadString('\n')
			if strings.Contains(line, "NOOP_LOCK_HELD") {
				ready <- line
				return
			}
			if err != nil {
				ready <- ""
				return
			}
		}
	}()
	select {
	case line := <-ready:
		if line == "" {
			t.Fatal("child exited before holding lock")
		}
	case <-time.After(20 * time.Second):
		t.Fatal("child did not retain live lock")
	}
	raw, err := os.ReadFile(filepath.Join(root, ".yss/transactions/lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var diagnostic struct {
		PID         int
		Host, Token string
	}
	if err := json.Unmarshal(raw, &diagnostic); err != nil || diagnostic.PID != child.Process.Pid || diagnostic.Host == "" || len(diagnostic.Token) != 32 {
		t.Fatalf("normal writer diagnostic missing: %s %v", raw, err)
	}
	var heldMutex *noOpFile
	if runtime.GOOS == "windows" {
		heldMutex = &mutexBefore
	}
	before := noOpTreeWithHeldMutex(t, root, heldMutex)
	if runtime.GOOS != "windows" && !reflect.DeepEqual(before, noOpTreeWithHeldMutex(t, root, &mutexBefore)) {
		t.Fatal("held-mutex observation differs from actual readable whole-tree evidence")
	}
	if _, err := transaction.ApplyWithGuards(root, "sync", nil, noOpGuard(t, root, "CONTEXT.md")); errorCode(err) != "LOCKED" {
		t.Fatalf("live mutex bypassed by no-op: %v", err)
	}
	if after := noOpTreeWithHeldMutex(t, root, heldMutex); !reflect.DeepEqual(before, after) {
		t.Fatal("no-op changed held-lock whole-tree bytes/mode/mtime")
	}
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("real held-lock child failed: %v %s", err, stderr.String())
	}
	waited = true
	if mutexAfter := noOpTree(t, root)[".yss/transactions/.lock"]; mutexAfter != mutexBefore {
		t.Fatalf("real held-lock mutex bytes/mode/mtime changed: before=%+v after=%+v", mutexBefore, mutexAfter)
	}
}

func TestNoOpGenuinePendingAndPreparingRefuseWithoutWrites(t *testing.T) {
	t.Run("published-pending", func(t *testing.T) {
		root := testRoot(t)
		if err := os.WriteFile(filepath.Join(root, "first"), []byte("original"), 0751); err != nil {
			t.Fatal(err)
		}
		startAndInterrupt(t, root)
		before := noOpTree(t, root)
		result, err := transaction.Apply(root, "sync", nil)
		if errorCode(err) != "INTERRUPTED" || result.Status != "pending" {
			t.Fatalf("pending zero-op accepted: %+v %v", result, err)
		}
		assertNoOpTree(t, root, before)
	})
	t.Run("unpublished-preparing", func(t *testing.T) {
		root, _ := killedPreparationObject(t)
		before := noOpTree(t, root)
		result, err := transaction.ApplyWithGuards(root, "sync", nil, noOpGuard(t, root, "identity.txt"))
		if errorCode(err) != "INTERRUPTED" || result.Status != "preparing" {
			t.Fatalf("preparing zero-op accepted: %+v %v", result, err)
		}
		assertNoOpTree(t, root, before)
	})
}

func TestNoOpRealNonzeroApplyKeepsDiagnosticLockProtocol(t *testing.T) {
	root := testRoot(t)
	if err := os.WriteFile(filepath.Join(root, "first"), []byte("original"), 0751); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestTransactionCrashHelper$")
	child.Env = append(os.Environ(), "YSS_TRANSACTION_CRASH_ROOT="+root)
	var output bytes.Buffer
	child.Stdout, child.Stderr = &output, &output
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	waited := false
	t.Cleanup(func() {
		if !waited {
			_ = child.Process.Kill()
			<-done
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	found := false
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(filepath.Join(root, ".yss/transactions/lock.json"))
		var diagnostic struct {
			PID         int
			Host, Token string
		}
		if err == nil && json.Unmarshal(raw, &diagnostic) == nil && diagnostic.PID == child.Process.Pid && diagnostic.Host != "" && len(diagnostic.Token) == 32 {
			found = true
			break
		}
		select {
		case err := <-done:
			waited = true
			t.Fatalf("nonzero child completed before diagnostic observation: %v %s", err, output.String())
		default:
		}
		time.Sleep(100 * time.Microsecond)
	}
	if !found {
		t.Fatal("real nonzero apply did not publish its diagnostic lock")
	}
	if _, err := transaction.Apply(root, "sync", nil); errorCode(err) != "LOCKED" {
		t.Fatalf("real nonzero writer was not mutually exclusive with no-op: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("nonzero apply failed: %v %s", err, output.String())
	}
	waited = true
}

func TestNoOpAbnormalControlAndSchemaRefusalsWriteNothing(t *testing.T) {
	for _, name := range []string{"missing-lock", "lock-directory", "lock-symlink", "lock-bytes", "lock-mode", "lock-diagnostic", "state-link", "state-parent-file", "unknown-control", "journal-schema"} {
		t.Run(name, func(t *testing.T) {
			root := noOpFixture(t)
			state := filepath.Join(root, ".yss/transactions")
			lock := filepath.Join(state, ".lock")
			want := "STATE"
			var wantPathError *os.PathError
			switch name {
			case "missing-lock":
				if err := os.Remove(lock); err != nil {
					t.Fatal(err)
				}
			case "lock-directory":
				if err := os.Remove(lock); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(lock, 0755); err != nil {
					t.Fatal(err)
				}
			case "lock-symlink":
				if err := os.Remove(lock); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(root, "asset"), lock); err != nil {
					if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
						t.Skip("Windows symlink privilege unavailable")
					}
					t.Fatal(err)
				}
				want = "PATH"
			case "lock-bytes":
				if err := os.WriteFile(lock, []byte("unproven control"), 0600); err != nil {
					t.Fatal(err)
				}
			case "lock-mode":
				if err := os.Chmod(lock, 0444); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(lock, 0600) })
			case "lock-diagnostic":
				if err := os.WriteFile(filepath.Join(state, "lock.json"), []byte("{invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			case "state-link":
				moved := filepath.Join(root, "state-evidence")
				if err := os.Rename(state, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, state); err != nil {
					if runtime.GOOS == "windows" && errors.Is(err, os.ErrPermission) {
						t.Skip("Windows symlink privilege unavailable")
					}
					t.Fatal(err)
				}
				want = "PATH"
			case "state-parent-file":
				moved := filepath.Join(root, "state-evidence")
				if err := os.Rename(filepath.Join(root, ".yss"), moved); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, ".yss"), []byte("user evidence"), 0600); err != nil {
					t.Fatal(err)
				}
				// The existing safefs seam can return the OS Lstat error before
				// the higher-level directory check. Preserve that exact refusal.
				_, existingErr := safefs.Path(root, ".yss/transactions")
				if existingErr == nil {
					t.Fatal("existing unsafe-parent path contract accepted file")
				}
				want = errorCode(existingErr)
				if want == "" && !errors.As(existingErr, &wantPathError) {
					t.Fatalf("unexpected existing unsafe-parent error: %T %v", existingErr, existingErr)
				}
			case "unknown-control":
				if err := os.WriteFile(filepath.Join(state, "unknown"), []byte("user evidence"), 0600); err != nil {
					t.Fatal(err)
				}
			case "journal-schema":
				files, err := filepath.Glob(filepath.Join(state, "*/journal.json"))
				if err != nil || len(files) != 1 {
					t.Fatalf("genuine journal: %v %v", files, err)
				}
				raw, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				var journal map[string]any
				if err := json.Unmarshal(raw, &journal); err != nil {
					t.Fatal(err)
				}
				journal["schemaVersion"] = 99
				raw, err = json.Marshal(journal)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(files[0], raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before := noOpTree(t, root)
			_, err := transaction.ApplyWithGuards(root, "sync", nil, noOpGuard(t, root, "CONTEXT.md"))
			if err == nil || errorCode(err) != want {
				t.Fatalf("unsafe control accepted %s: %v want %s", name, err, want)
			}
			if wantPathError != nil {
				var actual *os.PathError
				if !errors.As(err, &actual) || actual.Op != wantPathError.Op || actual.Path != wantPathError.Path || !errors.Is(err, wantPathError.Err) {
					t.Fatalf("unsafe-parent refusal changed: %v want %v", err, wantPathError)
				}
			}
			assertNoOpTree(t, root, before)
		})
	}
}
