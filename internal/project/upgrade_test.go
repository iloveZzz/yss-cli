package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
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
