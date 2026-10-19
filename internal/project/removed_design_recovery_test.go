package project

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestRecoveryArchiveOriginalProfile(t *testing.T) {
	for _, source := range []string{"spec", "backend", "design", "bad-digest", "missing-archive"} {
		t.Run(source, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			profile := source
			if source == "bad-digest" || source == "missing-archive" {
				profile = "design"
			}
			raw, _ := json.Marshal(identitymeta.GovernanceSnapshot{SchemaVersion: 1, Kind: "governance-original-source", SourceProfile: profile})
			digest := safefs.Digest(raw)
			if source == "bad-digest" {
				digest = strings.Repeat("e", 64)
			}
			ref := ".template-spec/compatibility/governance-history/" + digest + "/manifest.json"
			meta := Metadata{SchemaVersion: 4, BundleSchemaVersion: 4, ProtocolVersion: 1, Profile: "spec", ProfileID: "harness.spec-template", CLIVersion: "2.5.0", TemplateVersion: "4.6.1", LegacyCLIVersion: "1.0.0", TemplateCommit: strings.Repeat("a", 40), TemplateSourceState: "committed", CLISourceState: "unknown", SnapshotHash: strings.Repeat("b", 64), ManifestHash: strings.Repeat("c", 64), BundleHash: strings.Repeat("d", 64), Managed: map[string]Managed{}, BaselineDigest: safefs.Digest([]byte("{}")), Variables: map[string]string{}, Distribution: map[string]any{}, GovernanceHistory: &identitymeta.GovernanceHistory{Ref: ref, Digest: digest}}
			metadata, _ := json.Marshal(meta)
			ops := []transaction.Operation{{Path: MetadataFile, Data: metadata, Mode: 0644}}
			if source != "missing-archive" {
				ops = append(ops, transaction.Operation{Path: ref, Data: raw, Mode: 0644})
			}
			if _, err = transaction.ApplyContextWithValidation(context.Background(), root, "init", ops, nil, nil, func() error { return domain.Fail("STATE", "test rollback before installed identity") }); err == nil {
				t.Fatal("fixture must leave only the verified archive")
			}
			if _, err = os.Lstat(filepath.Join(root, MetadataFile)); !os.IsNotExist(err) {
				t.Fatal("fixture retained local metadata", err)
			}
			// A physical manifest must not fill or replace the selected archive.
			if err = os.MkdirAll(filepath.Dir(filepath.Join(root, ref)), 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, ref), []byte(`{"source_profile":"spec"}`), 0600); err != nil {
				t.Fatal(err)
			}
			before := recoveryTree(t, root)
			id, err := RecoveryIdentity(root, "spec")
			switch source {
			case "spec", "backend":
				if err != nil || id.Profile.Name != "spec" {
					t.Fatal("supported archive rejected", err)
				}
			case "design":
				if err == nil || !strings.Contains(err.Error(), "Design Profile 已移除") {
					t.Fatal("Design original policy accepted from recovery archive", err)
				}
			case "bad-digest":
				var failure *domain.Error
				if !errors.As(err, &failure) || failure.Code != "HISTORY_SOURCE_DRIFT" {
					t.Fatal("archive digest must be checked first", err)
				}
			case "missing-archive":
				if err == nil {
					t.Fatal("physical manifest substituted for missing archive")
				}
			}
			if !reflect.DeepEqual(before, recoveryTree(t, root)) {
				t.Fatal("identity read changed bytes, modes or file inventory")
			}
		})
	}
}

func recoveryTree(t *testing.T, root string) map[string]domain.Descriptor {
	t.Helper()
	files := map[string]domain.Descriptor{}
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		ref, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		d, err := safefs.Describe(root, filepath.ToSlash(ref))
		files[ref] = d
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}
