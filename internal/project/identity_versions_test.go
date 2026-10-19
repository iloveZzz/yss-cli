package project

import (
	"encoding/json"
	"fmt"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovedDesignOriginalSourceRejected(t *testing.T) {
	root := t.TempDir()
	raw := []byte("{\"schema_version\":1,\"kind\":\"governance-original-source\",\"source_profile\":\"design\",\"files\":{}}")
	digest := safefs.Digest(raw)
	ref := ".template-spec/compatibility/governance-history/" + digest + "/manifest.json"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, ref)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ref), raw, 0644); err != nil {
		t.Fatal(err)
	}
	meta := Metadata{SchemaVersion: 4, BundleSchemaVersion: 4, ProtocolVersion: 1, Profile: "spec", ProfileID: "harness.spec-template", CLIVersion: "2.5.0", TemplateVersion: "4.6.1", LegacyCLIVersion: "3.5.10", TemplateCommit: strings.Repeat("a", 40), TemplateSourceState: "committed", CLISourceState: "unknown", SnapshotHash: strings.Repeat("b", 64), ManifestHash: strings.Repeat("c", 64), BundleHash: strings.Repeat("d", 64), Managed: map[string]Managed{}, BaselineDigest: safefs.Digest([]byte("{}")), Variables: map[string]string{}, Distribution: map[string]any{}, GovernanceHistory: &identitymeta.GovernanceHistory{Ref: ref, Digest: digest}}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	for file, content := range map[string][]byte{MetadataFile: data, "yss-project.yaml": []byte("schema_version: 2\nrepository_mode: project-instance\n"), ".template-spec/process/harness-profile.yaml": []byte("schema_version: 3\nprofile_id: harness.spec-template\n")} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, file), content, 0644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Detect(root, "spec", false); err == nil {
		t.Fatal("Design original policy was accepted under a Spec identity")
	}
	after, err := os.ReadFile(filepath.Join(root, ref))
	if err != nil || string(after) != string(raw) {
		t.Fatal("historical source bytes changed")
	}
}

func TestRemovedDesignDoesNotBecomeFreshSpec(t *testing.T) {
	if _, err := domain.GetProfile("design"); err == nil {
		t.Fatal("removed Design is still a supported Profile")
	}
	for _, ref := range []string{".yss-harness-design.json", ".yss-product-design-plugin.json"} {
		t.Run(ref, func(t *testing.T) {
			root := t.TempDir()
			file := filepath.Join(root, ref)
			if err := os.WriteFile(file, []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Detect(root, "spec", true); err == nil {
				t.Fatal("old Design directory was accepted as a new Spec root")
			}
			if _, err := RecoveryIdentity(root, "spec"); err == nil {
				t.Fatal("old Design recovery was accepted")
			}
			raw, err := os.ReadFile(file)
			if err != nil || string(raw) != "{}\n" {
				t.Fatal("old Design bytes changed")
			}
			info, err := os.Stat(file)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("old Design permissions changed")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 1 {
				t.Fatal("refusal created files")
			}
		})
	}
}

func TestDetectRoleIndependentIdentityTuple(t *testing.T) {
	for _, tc := range []struct {
		root, profile string
		want          bool
	}{{"1", "2", true}, {"2", "3", true}, {"2", "2", false}, {"1", "3", false}, {"2.5", "3", false}, {"2", "99", false}} {
		t.Run(tc.root+"-"+tc.profile, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(filepath.Join(root, ".template-spec/process"), 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, "yss-project.yaml"), []byte(fmt.Sprintf("schema_version: %s\nrepository_mode: project-instance\n", tc.root)), 0644); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, ".template-spec/process/harness-profile.yaml"), []byte(fmt.Sprintf("schema_version: %s\nprofile_id: harness.spec-template\n", tc.profile)), 0644); err != nil {
				t.Fatal(err)
			}
			_, err = Detect(root, "spec", false)
			if (err == nil) != tc.want {
				t.Fatalf("identity tuple %s/%s: %v", tc.root, tc.profile, err)
			}
		})
	}
}
