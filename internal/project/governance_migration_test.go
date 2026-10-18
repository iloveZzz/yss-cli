package project

import (
	"encoding/json"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalGovernanceSnapshotPreservesBytes(t *testing.T) {
	root := t.TempDir()
	for ref, raw := range map[string]string{"yss-project.yaml": "schema_version: 1\nrepository_mode: project-instance\n", "CONTEXT.md": "original language\n", "old-approval.json": "{\"id\":\"original-id\",\"approved\":true}\n"} {
		if err := os.WriteFile(filepath.Join(root, ref), []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
	}
	p := &Plan{Root: root, Command: "migrate", Inputs: map[string]domain.Descriptor{}}
	id := &Identity{Root: root, Native: &Metadata{SchemaVersion: 3, Profile: "design"}}
	h, err := planOriginalGovernance(p, id)
	if err != nil {
		t.Fatal(err)
	}
	if p.GovernanceMigration.SourceProfile != "design" || h == nil {
		t.Fatal("missing explicit original binding")
	}
	if err = finalizePlan(p); err != nil {
		t.Fatal(err)
	}
	for _, change := range p.Changes {
		if change.Path == ".yss" {
			t.Fatal("project plan must not manage transaction-owned .yss root")
		}
	}
	var manifest identitymeta.GovernanceSnapshot
	for _, change := range p.Changes {
		if change.Path == h.Ref {
			if err = json.Unmarshal(mustDecode(change.Data), &manifest); err != nil {
				t.Fatal(err)
			}
		}
		if filepath.Base(change.Path) == "CONTEXT.md" {
			t.Fatal("snapshot must not create a second CONTEXT.md")
		}
	}
	if len(manifest.Files) != 3 || manifest.SourceProfile != "design" {
		t.Fatal("snapshot omitted original files")
	}
	for ref, want := range manifest.Files {
		if p.Inputs[ref] != want {
			t.Fatal("unbound source input")
		}
	}
	if err = os.Symlink("old-approval.json", filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err = planOriginalGovernance(&Plan{Root: root, Command: "migrate", Inputs: map[string]domain.Descriptor{}}, id); err == nil {
		t.Fatal("symbolic source was accepted")
	}
}
func TestMigrationRollbackDoesNotDiscardNewRecords(t *testing.T) {
	root := t.TempDir()
	raw, _ := json.Marshal(Metadata{SchemaVersion: 4, GovernanceHistory: &identitymeta.GovernanceHistory{Ref: ".template-spec/compatibility/governance-history/example/manifest.json"}})
	if err := os.WriteFile(filepath.Join(root, MetadataFile), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMigrationRollback(root); err == nil {
		t.Fatal("completed migration downgrade was accepted")
	}
}
