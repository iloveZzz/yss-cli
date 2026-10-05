package project

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLegacyInterruptionIsNeverMistakenForNativeIdle(t *testing.T) {
	root := freshRoot(t)
	if e := os.MkdirAll(filepath.Join(root, ".yss-harness-state/spec/transactions/test"), 0755); e != nil {
		t.Fatal(e)
	}
	journal := filepath.Join(root, ".yss-harness-state/spec/transactions/test/journal.json")
	for _, b := range [][]byte{[]byte(`{"schemaVersion":2,"profileId":"harness.spec-template","phase":"apply"}`), []byte(`{"schemaVersion":99,"profileId":"harness.spec-template","phase":"committed"}`)} {
		if e := os.WriteFile(journal, b, 0644); e != nil {
			t.Fatal(e)
		}
		if e := CheckLegacyState(root, "spec"); e == nil {
			t.Fatal("legacy pending/corrupted journal ignored")
		}
	}
	if e := os.WriteFile(journal, []byte(`{"schemaVersion":2,"profileId":"harness.spec-template","phase":"committed"}`), 0644); e != nil {
		t.Fatal(e)
	}
	if e := CheckLegacyState(root, "spec"); e != nil {
		t.Fatal(e)
	}
	if e := CheckLegacyState(root, "backend"); e == nil {
		t.Fatal("foreign old state accepted")
	}
	if e := os.WriteFile(filepath.Join(root, ".yss-harness-migrate.lock"), []byte(`{"pid":1}`), 0644); e != nil {
		t.Fatal(e)
	}
	if e := CheckLegacyState(root, "spec"); e == nil {
		t.Fatal("legacy lock ignored")
	}
}

func TestRecoveryIdentityUsesVerifiedMetadataArchive(t *testing.T) {
	root := freshRoot(t)
	p, e := Build(root, "spec", "init", nil, nil)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(p); e != nil {
		t.Fatal(e)
	}
	if e = os.Remove(filepath.Join(root, MetadataFile)); e != nil {
		t.Fatal(e)
	}
	id, e := RecoveryIdentity(root, "")
	if e != nil || id.Profile.Name != "spec" {
		t.Fatalf("archive identity not recovered: %+v %v", id, e)
	}
	if _, e = RecoveryIdentity(root, "backend"); e == nil {
		t.Fatal("archived family mismatch accepted")
	}
	if e = os.WriteFile(filepath.Join(root, "yss-project.yaml"), []byte("schema_version: 1\nrepository_mode: template-source\n"), 0644); e != nil {
		t.Fatal(e)
	}
	if _, e = RecoveryIdentity(root, "spec"); e == nil {
		t.Fatal("source root recovery bypassed identity")
	}
}
