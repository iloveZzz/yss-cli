package kernel

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
)

func TestAuthoritiesRejectRemovedDesignIdentity(t *testing.T) {
	for _, virtual := range []bool{false, true} {
		for _, scenario := range []string{"spec", "backend", ".yss-harness-design.json", ".yss-product-design-plugin.json", "design-history", "bad-history-digest"} {
			t.Run(scenario+map[bool]string{false: "/physical", true: "/frozen"}[virtual], func(t *testing.T) {
				root, _ := policyFixture(t)
				if strings.HasPrefix(scenario, ".yss-") {
					if err := os.WriteFile(filepath.Join(root, scenario), []byte("null\n"), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					source := scenario
					if strings.Contains(scenario, "history") {
						source = "design"
					}
					raw, _ := json.Marshal(identitymeta.GovernanceSnapshot{SchemaVersion: 1, Kind: "governance-original-source", SourceProfile: source})
					digest := safefs.Digest(raw)
					if scenario == "bad-history-digest" {
						digest = strings.Repeat("e", 64)
					}
					ref := ".template-spec/compatibility/governance-history/" + digest + "/manifest.json"
					writePolicyFixture(t, root, ref, json.RawMessage(raw))
					writePolicyFixture(t, root, domain.MetadataFile, identitymeta.Metadata{SchemaVersion: 4, BundleSchemaVersion: 4, ProtocolVersion: 1, Profile: "backend", ProfileID: "harness.backend-delivery", CLIVersion: "2.5.0", TemplateVersion: "4.6.1", LegacyCLIVersion: "1.0.0", TemplateCommit: strings.Repeat("a", 40), TemplateSourceState: "committed", CLISourceState: "unknown", SnapshotHash: strings.Repeat("b", 64), ManifestHash: strings.Repeat("c", 64), BundleHash: strings.Repeat("d", 64), Managed: map[string]identitymeta.Managed{}, BaselineDigest: safefs.Digest([]byte("{}")), Variables: map[string]string{}, Distribution: map[string]any{}, GovernanceHistory: &identitymeta.GovernanceHistory{Ref: ref, Digest: digest}})
				}
				s := NewSemanticSession(context.Background(), root, nil)
				if virtual {
					s.V.Virtual = map[string]semanticArchiveFile{}
					if err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
						if err != nil || entry.IsDir() {
							return err
						}
						ref, err := filepath.Rel(root, name)
						if err != nil {
							return err
						}
						info, err := entry.Info()
						if err != nil {
							return err
						}
						raw, err := os.ReadFile(name)
						s.V.Virtual[filepath.ToSlash(ref)] = semanticArchiveFile{Data: raw, Mode: uint32(info.Mode().Perm())}
						return err
					}); err != nil {
						t.Fatal(err)
					}
					// A frozen source cannot acquire identities from its physical host.
					if err := os.WriteFile(filepath.Join(root, ".yss-harness-design.json"), []byte("{}"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				err := s.Authorities()
				if strings.HasPrefix(scenario, ".yss-") {
					if err == nil || !strings.Contains(err.Error(), "Design Profile 已移除") {
						t.Fatal("semantic authority accepted removed Design identity", err)
					}
				} else if SemanticCode(err) != "BASELINE" {
					t.Fatal("unsupported legacy source must be rejected before historical policy consumption", err)
				}
			})
		}
	}
}
