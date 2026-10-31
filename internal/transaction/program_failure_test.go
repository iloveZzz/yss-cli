package transaction

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestProgramFailureDoesNotAutomaticallyRestore(t *testing.T) {
	for _, test := range []struct {
		kind       string
		wantExists bool
	}{{"program-update", true}, {"init", false}} {
		t.Run(test.kind, func(t *testing.T) {
			root := t.TempDir()
			result, err := ApplyContextWithValidation(context.Background(), root, test.kind, []Operation{{Path: "output", Data: []byte("new"), Mode: 0644}}, nil, nil, func() error { return errors.New("verification failed") })
			if err == nil {
				t.Fatal("verification failure was ignored")
			}
			data, readErr := os.ReadFile(filepath.Join(root, "output"))
			if test.wantExists {
				if readErr != nil || string(data) != "new" || result.Status != "pending" {
					t.Fatalf("program failure restored or hid partial state: %q %+v %v", data, result, readErr)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatalf("other operations must retain automatic protection: %q %v", data, readErr)
			}
		})
	}
}

type cancelAfterFirstProgramWrite struct {
	context.Context
	first string
}

func (c cancelAfterFirstProgramWrite) Err() error {
	if _, err := os.Stat(c.first); err == nil {
		return context.Canceled
	}
	return nil
}

func TestProgramWriteCancellationPreservesIncompleteState(t *testing.T) {
	root := t.TempDir()
	ctx := cancelAfterFirstProgramWrite{Context: context.Background(), first: filepath.Join(root, "first")}
	result, err := ApplyContext(ctx, root, "program-update", []Operation{
		{Path: "first", Data: []byte("first"), Mode: 0644},
		{Path: "second", Data: []byte("second"), Mode: 0644},
	})
	if !errors.Is(err, context.Canceled) || result.Status != "pending" {
		t.Fatalf("interrupted update must report pending failure: %+v %v", result, err)
	}
	if got, e := os.ReadFile(ctx.first); e != nil || string(got) != "first" {
		t.Fatalf("already-written file must remain: %q %v", got, e)
	}
	if _, e := os.Stat(filepath.Join(root, "second")); !os.IsNotExist(e) {
		t.Fatalf("unattempted file must remain absent: %v", e)
	}
}

func TestSyncFailureRequiresManualRestoration(t *testing.T) {
	root := t.TempDir()
	result, err := ApplyContextWithValidation(context.Background(), root, "sync", []Operation{{Path: "new.txt", Data: []byte("new"), Mode: 0644}}, nil, nil, func() error { return errors.New("verification failed") })
	if err == nil || result.Status != "pending" {
		t.Fatalf("failure not reported as pending: %+v %v", result, err)
	}
	if got, e := os.ReadFile(filepath.Join(root, "new.txt")); e != nil || string(got) != "new" {
		t.Fatalf("sync automatically restored: %q %v", got, e)
	}
	if len(result.CreatedPaths) != 1 || result.CreatedPaths[0] != "new.txt" {
		t.Fatalf("new file missing from result: %+v", result)
	}
	if len(result.NewControlPaths) == 0 {
		t.Fatalf("new transaction control paths missing from result: %+v", result)
	}
	status, err := Status(root)
	if err != nil || len(status.Pending) != 1 {
		t.Fatalf("partial sync must remain pending: %+v %v", status, err)
	}
	if err := os.Remove(filepath.Join(root, "new.txt")); err != nil {
		t.Fatal(err)
	}
	status, err = Status(root)
	if err != nil || len(status.Pending) != 0 {
		t.Fatalf("manual restoration must unblock sync: %+v %v", status, err)
	}
	if _, err := ApplyContext(context.Background(), root, "sync", []Operation{{Path: "later.txt", Data: []byte("later"), Mode: 0644}}); err != nil {
		t.Fatalf("manual restoration did not permit a new transaction: %v", err)
	}
}

func TestSyncFailureListsNewParentsAndForbidsRecovery(t *testing.T) {
	root := t.TempDir()
	result, err := ApplyContextWithValidation(context.Background(), root, "sync", []Operation{{Path: "new-dir/new.txt", Data: []byte("new"), Mode: 0644}}, nil, nil, func() error { return errors.New("verification failed") })
	if err == nil || len(result.CreatedPaths) != 2 || result.CreatedPaths[0] != "new-dir" || result.CreatedPaths[1] != "new-dir/new.txt" {
		t.Fatalf("incomplete new paths: %+v %v", result, err)
	}
	if _, err := Recover(root); err == nil {
		t.Fatal("sync recovery must not run")
	}
	if got, err := os.ReadFile(filepath.Join(root, "new-dir/new.txt")); err != nil || string(got) != "new" {
		t.Fatalf("recovery altered sync output: %q %v", got, err)
	}
}

func TestSuccessfulSyncCannotBeRolledBackByTransaction(t *testing.T) {
	root := t.TempDir()
	if _, err := ApplyContext(context.Background(), root, "sync", []Operation{{Path: "new.txt", Data: []byte("new"), Mode: 0644}}); err != nil {
		t.Fatal(err)
	}
	if _, err := Rollback(root); err == nil {
		t.Fatal("sync rollback must be rejected")
	}
	if got, err := os.ReadFile(filepath.Join(root, "new.txt")); err != nil || string(got) != "new" {
		t.Fatalf("rollback altered sync output: %q %v", got, err)
	}
}

type cancelDuringSyncPreparation struct {
	context.Context
	root string
}

func (c cancelDuringSyncPreparation) Err() error {
	refs, _ := filepath.Glob(filepath.Join(c.root, ".yss", "transactions", ".preparing-*", "plan.json"))
	if len(refs) != 0 {
		return context.Canceled
	}
	return nil
}

func TestSyncInterruptedDuringPreparationListsNewControlState(t *testing.T) {
	root := t.TempDir()
	ctx := cancelDuringSyncPreparation{Context: context.Background(), root: root}
	result, err := ApplyContext(ctx, root, "sync", []Operation{{Path: "new.txt", Data: []byte("new"), Mode: 0644}})
	if !errors.Is(err, context.Canceled) || len(result.NewControlPaths) == 0 {
		t.Fatalf("preparation interruption did not identify control state: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Join(root, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("preparation wrote project target: %v", err)
	}
	status, err := Status(root)
	if err != nil || len(status.Preparations) == 0 {
		t.Fatalf("interrupted preparation missing from status: %+v %v", status, err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".yss")); err != nil {
		t.Fatal(err)
	}
	status, err = Status(root)
	if err != nil || len(status.Preparations) != 0 {
		t.Fatalf("manual cleanup did not clear preparation: %+v %v", status, err)
	}
}
