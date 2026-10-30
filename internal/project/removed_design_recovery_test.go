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

func TestRecoveryRejectsPreFiveArchiveAndPreservesFiles(t *testing.T) {
	// Without an embedded Bundle, this is not an instance provenance test.
	trustedBundle(t, "spec")
	for _, source := range []string{"spec", "backend"} {
		t.Run(source, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(identitymeta.GovernanceSnapshot{SchemaVersion: 1, Kind: "governance-original-source", SourceProfile: source})
			digest := safefs.Digest(raw)
			ref := ".template-spec/compatibility/governance-history/" + digest + "/manifest.json"
			meta := Metadata{SchemaVersion: 4, BundleSchemaVersion: 4, ProtocolVersion: 1, Profile: "spec", ProfileID: "harness.spec-template", CLIVersion: "2.5.0", TemplateVersion: "4.6.1", LegacyCLIVersion: "1.0.0", TemplateCommit: strings.Repeat("a", 40), TemplateSourceState: "committed", CLISourceState: "unknown", SnapshotHash: strings.Repeat("b", 64), ManifestHash: strings.Repeat("c", 64), BundleHash: strings.Repeat("d", 64), Managed: map[string]Managed{}, BaselineDigest: safefs.Digest([]byte("{}")), Variables: map[string]string{}, Distribution: map[string]any{}, GovernanceHistory: &identitymeta.GovernanceHistory{Ref: ref, Digest: digest}}
			metadata, _ := json.Marshal(meta)
			ops := []transaction.Operation{{Path: MetadataFile, Data: metadata, Mode: 0644}, {Path: ref, Data: raw, Mode: 0644}}
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
			_, err = RecoveryIdentity(root, "spec")
			var failure *domain.Error
			if !errors.As(err, &failure) || failure.Code != "BASELINE" {
				t.Fatalf("Template 4.6 归档必须先按不受信来源拒绝: %v", err)
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
