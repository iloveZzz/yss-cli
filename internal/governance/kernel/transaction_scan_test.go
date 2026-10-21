package kernel

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestSemanticScanValidatesUnderTransactionLock(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = transaction.ApplyContextWithValidation(ctx, root, "scan-test", []transaction.Operation{{Path: "result.json", Data: []byte("{}"), Mode: 0644}}, nil, nil, func() error {
		s := NewSemanticSession(ctx, root, nil)
		s.InsideTransaction = true
		if _, e := s.Scan(".yss/transactions"); e != nil {
			return e
		}
		return s.Finish()
	})
	if err != nil {
		t.Fatalf("validation under the writer lock: %v", err)
	}

	// Read-only verification must still bind the mutex contents after the
	// transaction releases it, and reject subsequent changes.
	s := NewSemanticSession(ctx, root, nil)
	if _, err = s.Scan(".yss/transactions"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ".yss/transactions/.lock"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(); err == nil {
		t.Fatal("read-only scan accepted changed mutex contents")
	}
}
