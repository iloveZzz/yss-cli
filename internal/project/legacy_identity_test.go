package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

const historicalDesignProfile = "docs/process/harness-profile.yaml"

func legacyDesignFixture(t *testing.T) (string, map[string]any) {
	t.Helper()
	root := freshRoot(t)
	p := domain.Profiles["design"]
	data := []byte("schema_version: 2\nprofile_id: " + p.ID + "\n")
	d := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: 0644}
	m := map[string]any{"metadataSchemaVersion": 2, "profileId": p.ID, "templateSource": p.TemplateSource, "templateCommit": strings.Repeat("a", 40), "templateSourceState": "committed", "snapshotHash": strings.Repeat("b", 64), "manifestHash": strings.Repeat("c", 64), "managedFiles": map[string]any{historicalDesignProfile: map[string]any{"baseline": d, "lastApplied": d, "ownership": "managed"}}}
	for ref, b := range map[string][]byte{"yss-project.yaml": []byte("schema_version: 1\nrepository_mode: project-instance\n"), historicalDesignProfile: data} {
		file := filepath.Join(root, filepath.FromSlash(ref))
		if e := os.MkdirAll(filepath.Dir(file), 0755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(file, b, 0644); e != nil {
			t.Fatal(e)
		}
	}
	writeLegacyDesignMetadata(t, root, m)
	return root, m
}

func writeLegacyDesignMetadata(t *testing.T, root string, m map[string]any) {
	t.Helper()
	managed, e := json.Marshal(m["managedFiles"])
	if e != nil {
		t.Fatal(e)
	}
	m["baselineDigest"] = safefs.Digest(managed)
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, domain.Profiles["design"].Metadata), b, 0644); e != nil {
		t.Fatal(e)
	}
}

func TestLegacyDesignProfileHistoricalLocation(t *testing.T) {
	root, _ := legacyDesignFixture(t)
	id, e := Detect(root, "", false)
	if e != nil {
		t.Fatal(e)
	}
	if id.Profile.Name != "design" || id.Native != nil {
		t.Fatalf("unexpected identity: %+v", id)
	}
	if id.ProfileRef != historicalDesignProfile {
		t.Fatalf("historical authority path lost: %s", id.ProfileRef)
	}
	data, e := os.ReadFile(filepath.Join(root, historicalDesignProfile))
	if e != nil {
		t.Fatal(e)
	}
	canonical := filepath.Join(root, ".template-spec/process/harness-profile.yaml")
	if e = os.MkdirAll(filepath.Dir(canonical), 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(canonical, data, 0644); e != nil {
		t.Fatal(e)
	}
	if id, e = Detect(root, "", false); e != nil || id.ProfileRef != ".template-spec/process/harness-profile.yaml" {
		t.Fatalf("identical dual contracts rejected: %+v %v", id, e)
	}
}

func TestLegacyDesignProfileRejectsUnprovenOrDriftingContract(t *testing.T) {
	for _, change := range []string{"bytes", "mode", "missing-applied", "uncommitted", "missing-snapshot", "conflicting-location", "wrong-profile", "symlink"} {
		t.Run(change, func(t *testing.T) {
			root, m := legacyDesignFixture(t)
			file := filepath.Join(root, historicalDesignProfile)
			switch change {
			case "bytes":
				os.WriteFile(file, []byte("schema_version: 2\nprofile_id: harness.business-ddd-strategy-handoff\n# modified\n"), 0644)
			case "mode":
				os.Chmod(file, 0444)
			case "missing-applied":
				delete(m["managedFiles"].(map[string]any)[historicalDesignProfile].(map[string]any), "lastApplied")
			case "uncommitted":
				m["templateSourceState"] = "working-tree"
			case "missing-snapshot":
				delete(m, "snapshotHash")
			case "conflicting-location":
				os.MkdirAll(filepath.Join(root, ".template-spec/process"), 0755)
				os.WriteFile(filepath.Join(root, ".template-spec/process/harness-profile.yaml"), []byte("schema_version: 2\nprofile_id: "+domain.Profiles["design"].ID+"\n# ambiguous\n"), 0644)
			case "wrong-profile":
				m["profileId"] = domain.Profiles["backend"].ID
			case "symlink":
				os.Rename(file, file+".real")
				if e := os.Symlink("harness-profile.yaml.real", file); e != nil {
					t.Fatal(e)
				}
			}
			writeLegacyDesignMetadata(t, root, m)
			if _, e := Detect(root, "design", false); e == nil {
				t.Fatal("unproven historical Profile accepted")
			}
		})
	}
}

func TestLegacyDesignProfileNativeRequiresCanonicalLocation(t *testing.T) {
	root, m := legacyDesignFixture(t)
	p := domain.Profiles["design"]
	n := Metadata{SchemaVersion: 1, Profile: "design", ProfileID: p.ID, ProtocolVersion: 1, CLIVersion: domain.Version, TemplateSourceState: "committed", TemplateVersion: p.LegacyVersion, LegacyCLIVersion: p.LegacyVersion, TemplateCommit: m["templateCommit"].(string), SnapshotHash: m["snapshotHash"].(string), ManifestHash: m["manifestHash"].(string), Managed: map[string]Managed{}, Variables: map[string]string{"projectName": "fixture", "businessDomain": "fixture", "teamSize": "1"}, Distribution: map[string]any{}}
	b, _ := json.Marshal(n.Managed)
	n.BaselineDigest = safefs.Digest(b)
	b, _ = json.Marshal(n)
	if e := os.WriteFile(filepath.Join(root, MetadataFile), b, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := Detect(root, "", false); e == nil {
		t.Fatal("native identity accepted historical-only Profile")
	}
	data, e := os.ReadFile(filepath.Join(root, historicalDesignProfile))
	if e != nil {
		t.Fatal(e)
	}
	canonical := filepath.Join(root, ".template-spec/process/harness-profile.yaml")
	if e = os.MkdirAll(filepath.Dir(canonical), 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(canonical, data, 0644); e != nil {
		t.Fatal(e)
	}
	if id, e := Detect(root, "", false); e != nil || id.Native == nil || id.ProfileRef != ".template-spec/process/harness-profile.yaml" {
		t.Fatalf("valid native canonical contract rejected: %+v %v", id, e)
	}
}
