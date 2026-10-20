package updater

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestRecoverProgramPreparationAndRetry(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "legacy-without-header"}[legacy], func(t *testing.T) {
			root, _ := interruptedProgram(t)
			status, err := Status(root)
			if err != nil {
				t.Fatal(err)
			}
			facts := status.(map[string]any)
			preview, ok := facts["preparationRecovery"].(map[string]any)
			if !ok || preview["status"] != "preparing" || facts["recoveryRequired"] != true {
				t.Fatalf("status must explain preparation recovery: %v", facts)
			}
			if legacy {
				refs, _ := filepath.Glob(filepath.Join(root, ".yss/transactions/.preparation-*.json"))
				for _, ref := range refs {
					if err := os.Remove(ref); err != nil {
						t.Fatal(err)
					}
				}
			}
			r, err := Recover(context.Background(), root)
			if err != nil || r.Status != "sealed" || len(r.SealedPreparations) != 1 {
				t.Fatalf("must seal unpublished program: %+v %v", r, err)
			}
			if _, err = os.Stat(filepath.Join(root, "README.md")); !os.IsNotExist(err) {
				t.Fatal("sealing must not write targets")
			}
			r, err = Recover(context.Background(), root)
			if err != nil || r.Status != "unchanged" {
				t.Fatalf("repeated recovery must be idempotent: %+v %v", r, err)
			}
			if _, err = transaction.ApplyContext(context.Background(), root, "program-update", []transaction.Operation{{Path: "README.md", Data: []byte("retry"), Mode: 0644, Before: &domain.Descriptor{Type: "missing"}}}); err != nil {
				t.Fatalf("sealed preparation must not block retry: %v", err)
			}
		})
	}
}

func TestRecoverProgramPreparationRefusesUnsafeState(t *testing.T) {
	for _, scenario := range []string{"missing-plan", "wal", "user-change", "multiple"} {
		t.Run(scenario, func(t *testing.T) {
			root, prep := interruptedProgram(t)
			switch scenario {
			case "missing-plan":
				if err := os.Remove(filepath.Join(prep, "plan.json")); err != nil {
					t.Fatal(err)
				}
				refs, _ := filepath.Glob(filepath.Join(root, ".yss/transactions/.preparation-*.json"))
				for _, ref := range refs {
					if err := os.Remove(ref); err != nil {
						t.Fatal(err)
					}
				}
			case "wal":
				if err := os.WriteFile(filepath.Join(prep, "intent.wal"), []byte("intent"), 0600); err != nil {
					t.Fatal(err)
				}
			case "user-change":
				if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("user"), 0644); err != nil {
					t.Fatal(err)
				}
			case "multiple":
				if err := os.Mkdir(filepath.Join(filepath.Dir(prep), ".preparing-"+strings.Repeat("a", 32)), 0755); err != nil {
					t.Fatal(err)
				}
			}
			_, err := Recover(context.Background(), root)
			if err == nil {
				t.Fatal("unsafe preparation must not be reported as recovered/unchanged")
			}
			if _, e := os.Stat(prep); e != nil {
				t.Fatalf("unsafe evidence must remain: %v", e)
			}
			if scenario == "user-change" {
				raw, _ := os.ReadFile(filepath.Join(root, "README.md"))
				if string(raw) != "user" {
					t.Fatal("user bytes changed")
				}
			}
		})
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
