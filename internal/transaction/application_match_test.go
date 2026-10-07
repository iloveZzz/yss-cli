package transaction_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestCommittedApplicationMatchesExactInitializationOnly(t *testing.T) {
	root := testRoot(t)
	ops := []transaction.Operation{{Path: "native", Data: []byte("candidate"), Mode: 0644}}
	if matched, err := transaction.CommittedApplicationMatches(root, "init", ops); err != nil || matched {
		t.Fatalf("unapplied bytes authorized: %v %v", matched, err)
	}
	if _, err := transaction.Apply(root, "init", ops); err != nil {
		t.Fatal(err)
	}
	if matched, err := transaction.CommittedApplicationMatches(root, "init", ops); err != nil || !matched {
		t.Fatalf("exact init not recognized: %v %v", matched, err)
	}
	for _, different := range [][]transaction.Operation{nil, {{Path: "native", Data: []byte("other"), Mode: 0644}}, {{Path: "native", Data: []byte("candidate"), Mode: 0600}}} {
		if matched, err := transaction.CommittedApplicationMatches(root, "init", different); err != nil || matched {
			t.Fatalf("different plan matched init: %v %v", matched, err)
		}
	}
	if matched, err := transaction.CommittedApplicationMatches(root, "sync", ops); err != nil || matched {
		t.Fatalf("different family matched init: %v %v", matched, err)
	}
	if _, err := transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	if matched, err := transaction.CommittedApplicationMatches(root, "init", ops); err != nil || matched {
		t.Fatalf("rolled back init still authorized: %v %v", matched, err)
	}
	if _, err := os.Stat(filepath.Join(root, "native")); !os.IsNotExist(err) {
		t.Fatal("readonly matching restored removed file")
	}
}

func TestProfileLinksRegistrationRetainsLatestTransactionBarrier(t *testing.T) {
	root := testRoot(t)
	if _, err := transaction.Apply(root, "migrate", []transaction.Operation{{Path: "asset", Data: []byte("migration")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Apply(root, "profile-links", []transaction.Operation{{Path: ".yss-profile-links.json", Data: []byte("links")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.RollbackKind(root, "migrate"); errorCode(err) != "KIND" {
		t.Fatalf("migration rollback bypassed latest links: %v", err)
	}
	if _, err := transaction.RollbackKind(root, "profile-links"); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.RollbackKind(root, "migrate"); errorCode(err) != "KIND" {
		t.Fatalf("migration rollback crossed removed links: %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(root, "asset")); string(raw) != "migration" {
		t.Fatal("links rollback changed prior migration asset")
	}
}
