package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

// Exercise the public planner rather than a second implementation of selection.
func TestUpgradeAttachRoutesNativeInstance(t *testing.T) {
	root := lockNativeFixture(t)
	_, err := Build(root, "", "attach", nil, nil)
	if lockCode(err) != "SYNC_REQUIRED" {
		t.Fatalf("native attach must route to sync, got %v", err)
	}
}

func TestUpgradeAddsNewBaseAndReportsRetiredAssets(t *testing.T) {
	root := lockNativeFixture(t)
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	delete(id.Native.Managed, ".codex/config.toml")
	if err := os.Remove(filepath.Join(root, ".codex/config.toml")); err != nil {
		t.Fatal(err)
	}
	retiredRecord := id.Native.Managed["AGENTS.md"]
	// An unavailable historical baseline is valid; another file's Bundle
	// source path is not a valid source for this retired asset.
	retiredRecord.Source = &identitymeta.BaselineSource{Kind: "unavailable"}
	id.Native.Managed["retired-template.md"] = retiredRecord
	b, _ := json.Marshal(id.Native.Managed)
	id.Native.BaselineDigest = safefs.Digest(b)
	b, _ = jsonBytes(id.Native)
	if err := os.WriteFile(filepath.Join(root, MetadataFile), b, 0644); err != nil {
		t.Fatal(err)
	}
	p, err := Build(root, "", "sync", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	added := false
	for _, c := range p.Changes {
		added = added || c.Path == ".codex/config.toml"
	}
	if !added {
		t.Fatal("sync silently omitted a new base asset")
	}
	retired := false
	for _, ref := range p.Preserved {
		retired = retired || ref == "retired-template.md"
	}
	if !retired {
		t.Fatal("retired managed asset omitted from the plan")
	}
}

func TestManagedAssetDirectoriesRollbackAndConcurrentContent(t *testing.T) {
	root := freshRoot(t)
	user := filepath.Join(root, "user-data")
	if err := os.MkdirAll(user, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(user, "business.txt")
	if err := os.WriteFile(marker, []byte("preserve\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Build(root, "spec", "attach", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	dirs := []string{}
	for _, c := range p.Changes {
		if c.After.Type == "directory" {
			dirs = append(dirs, c.Path)
		}
	}
	if len(dirs) == 0 {
		t.Fatal("new asset parents were not planned")
	}
	if _, err = Apply(p); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(root, dirs[0], "user-owned.txt")
	if err = os.WriteFile(extra, []byte("later work"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = transaction.Rollback(root); err == nil {
		t.Fatal("rollback deleted a directory containing later user work")
	}
	if raw, err := os.ReadFile(extra); err != nil || string(raw) != "later work" {
		t.Fatal("failed rollback changed later work", err)
	}
	if _, err = os.Stat(filepath.Join(root, MetadataFile)); err != nil {
		t.Fatal("failed rollback partially restored metadata", err)
	}
	if err = os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	if _, err = transaction.Rollback(root); err != nil {
		t.Fatal(err)
	}
	for _, dir := range dirs {
		if _, err := os.Stat(filepath.Join(root, dir)); !os.IsNotExist(err) {
			t.Fatalf("created directory survived rollback: %s %v", dir, err)
		}
	}
	if raw, err := os.ReadFile(marker); err != nil || string(raw) != "preserve\n" {
		t.Fatal("business bytes changed", err)
	}
	if st, err := os.Stat(marker); err != nil || st.Mode().Perm() != 0600 {
		t.Fatal("business mode changed", err)
	}
	if st, err := os.Stat(user); err != nil || st.Mode().Perm() != 0700 {
		t.Fatal("existing directory mode changed", err)
	}
}
