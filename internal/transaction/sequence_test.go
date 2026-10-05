package transaction_test

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"os"
	"path/filepath"
	"testing"
)

func rewriteArchivedPlan(t *testing.T, out transaction.Result, patch func(map[string]any)) {
	t.Helper()
	file := filepath.Join(out.BackupPath, "plan.json")
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err = json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	patch(p)
	b, err = json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if err = os.WriteFile(file, b, 0600); err != nil {
		t.Fatal(err)
	}
	file = filepath.Join(out.BackupPath, "journal.json")
	j, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err = json.Unmarshal(j, &v); err != nil {
		t.Fatal(err)
	}
	v["planDigest"] = safefs.Digest(b)
	j, err = json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, append(j, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestSequenceIgnoresClockRollbackForFamilyAndArchive(t *testing.T) {
	root := testRoot(t)
	first, err := transaction.Apply(root, "migrate", []transaction.Operation{{Path: "a", Data: []byte("old")}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: "b", Data: []byte("new")}})
	if err != nil {
		t.Fatal(err)
	}
	rewriteArchivedPlan(t, first, func(p map[string]any) { p["createdAt"] = "2099-12-31T23:59:59Z" })
	rewriteArchivedPlan(t, second, func(p map[string]any) { p["createdAt"] = "2000-01-01T00:00:00Z" })
	if _, err = transaction.RollbackKind(root, "migrate"); errorCode(err) != "KIND" {
		t.Fatalf("clock rewind selected old migrate: %v", err)
	}
	if b, err := transaction.ArchivedFile(root, "b"); err != nil || string(b) != "new" {
		t.Fatalf("archive followed clock: %q %v", b, err)
	}
	out, err := transaction.Status(root)
	if err != nil || len(out.Transactions) != 2 || out.Transactions[0].Sequence != 1 || out.Transactions[1].Sequence != 2 {
		t.Fatalf("order: %+v %v", out, err)
	}
	if _, err = transaction.RollbackKind(root, "sync"); err != nil {
		t.Fatal(err)
	}
}
func TestSequenceRejectsUnknownOrAmbiguousHistory(t *testing.T) {
	for _, variant := range []string{"legacy-v1", "zero", "duplicate", "overflow"} {
		t.Run(variant, func(t *testing.T) {
			root := testRoot(t)
			first, err := transaction.Apply(root, "first", []transaction.Operation{{Path: "a", Data: []byte("a")}})
			if err != nil {
				t.Fatal(err)
			}
			second, err := transaction.Apply(root, "second", []transaction.Operation{{Path: "b", Data: []byte("b")}})
			if err != nil {
				t.Fatal(err)
			}
			rewriteArchivedPlan(t, second, func(p map[string]any) {
				switch variant {
				case "legacy-v1":
					p["schemaVersion"] = 1
					delete(p, "sequence")
				case "zero":
					p["sequence"] = 0
				case "duplicate":
					p["sequence"] = 1
				case "overflow":
					p["sequence"] = json.Number("18446744073709551615")
				}
			})
			if variant == "overflow" {
				_, err = transaction.Apply(root, "next", []transaction.Operation{{Path: "c", Data: []byte("c")}})
			} else {
				_, err = transaction.Status(root)
			}
			if errorCode(err) != "STATE" {
				t.Fatalf("invalid history accepted: %v", err)
			}
			if b, _ := os.ReadFile(filepath.Join(root, "a")); string(b) != "a" {
				t.Fatal("first file changed")
			}
			_ = first
		})
	}
}
