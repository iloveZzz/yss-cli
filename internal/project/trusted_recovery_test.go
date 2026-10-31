//go:build !yss_nobundle

package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/nativeprofile"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func TestRecoveryIdentityAuthenticatesArchivedProfile(t *testing.T) {
	b, err := bundle.Load("backend")
	if err != nil {
		t.Fatal(err)
	}
	p, err := domain.GetProfile("backend")
	if err != nil {
		t.Fatal(err)
	}
	const ref = ".template-spec/process/harness-profile.yaml"
	f, ok := b.Files[ref]
	if !ok {
		t.Fatal("基线缺少 profile")
	}
	f, err = nativeprofile.Generate(f, p)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := f.Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		forged  bool
		pending bool
		version string
		want    bool
	}{
		{"trusted", false, false, b.TemplateVersion, true}, {"forged-policy", true, false, b.TemplateVersion, false}, {"old-version", false, false, "4.0.0", false}, {"pending-forged-policy", false, true, b.TemplateVersion, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			candidate := append([]byte(nil), profile...)
			if tc.forged {
				candidate = append(candidate, '\n')
			}
			d := domain.Descriptor{Type: "file", Digest: safefs.Digest(candidate), Mode: 0644}
			managed := map[string]identitymeta.Managed{ref: {Baseline: d, Applied: d, Ownership: f.Ownership, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, BundleHash: b.BundleHash, Path: ref, Variant: "generated", TemplateDigest: safefs.Digest(candidate)}}}
			mraw, _ := json.Marshal(managed)
			meta := identitymeta.Metadata{SchemaVersion: 4, BundleSchemaVersion: 4, ProtocolVersion: domain.ProtocolVersion, Profile: "backend", ProfileID: p.ID, CLIVersion: "3.0.0", LegacyCLIVersion: "3.0.0", TemplateVersion: tc.version, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, CLISourceState: "unknown", Variables: map[string]string{}, Distribution: map[string]any{}, Managed: managed, BaselineDigest: safefs.Digest(mraw)}
			metadata, _ := json.Marshal(meta)
			if tc.pending {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, ref)), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, ref), profile, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, MetadataFile), metadata, 0644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(root, "yss-project.yaml"), []byte("schema_version: 2\nrepository_mode: project-instance\n"), 0644); err != nil {
					t.Fatal(err)
				}
				candidate = append(candidate, '\n')
			}
			ops := []transaction.Operation{{Path: ref, Data: candidate, Mode: 0644}}
			if tc.pending {
				ops[0].Before = &d
			}
			if !tc.pending {
				ops = append(ops, transaction.Operation{Path: MetadataFile, Data: metadata, Mode: 0644})
			}
			result, err := transaction.Apply(root, "init", ops)
			if err != nil {
				t.Fatal(err)
			}
			if tc.pending {
				if err = os.WriteFile(filepath.Join(root, ref), profile, 0644); err != nil {
					t.Fatal(err)
				}
				journal := filepath.Join(result.BackupPath, "journal.json")
				raw, err := os.ReadFile(journal)
				if err != nil {
					t.Fatal(err)
				}
				var j map[string]any
				if err = json.Unmarshal(raw, &j); err != nil {
					t.Fatal(err)
				}
				j["phase"] = "applying"
				raw, _ = json.Marshal(j)
				if err = os.WriteFile(journal, raw, 0600); err != nil {
					t.Fatal(err)
				}
				if _, err = Detect(root, "backend", false); err != nil {
					t.Fatalf("已安装源应有效: %v", err)
				}
			} else {
				if err = os.Remove(filepath.Join(root, MetadataFile)); err != nil {
					t.Fatal(err)
				}
				if err = os.Remove(filepath.Join(root, ref)); err != nil {
					t.Fatal(err)
				}
			}
			id, err := RecoveryIdentity(root, "backend")
			if (err == nil) != tc.want {
				t.Fatalf("恢复来源校验: id=%v err=%v", id, err)
			}
			if _, err = os.Stat(filepath.Join(root, MetadataFile)); !tc.pending && !os.IsNotExist(err) {
				t.Fatal("身份检查不应写入")
			}
		})
	}
}

func TestRecoveryRejectsUnregisteredArchivedPolicy(t *testing.T) {
	b, err := bundle.Load("backend")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ref := ".template-spec/process/schemas/unknown.schema.json"
	if _, err = transaction.Apply(root, "sync", []transaction.Operation{{Path: ref, Data: []byte(`{}`), Mode: 0644}}); err != nil {
		t.Fatal(err)
	}
	id := &Identity{Root: root, Profile: domain.Profiles["backend"], Native: &identitymeta.Metadata{Profile: b.Profile, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, Managed: map[string]identitymeta.Managed{}}}
	if err = VerifyRecoveryCandidatePolicies(root, id, []string{ref}, ""); err == nil {
		t.Fatal("未登记的策略归档不应授权恢复")
	}
	if got, err := os.ReadFile(filepath.Join(root, ref)); err != nil || string(got) != "{}" {
		t.Fatal("拒绝时不得更改现场")
	}
}

func TestRecoveryDirectoryOperationIsNotPolicyFile(t *testing.T) {
	b, err := bundle.Load("backend")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ref := ".template-spec/process/schemas"
	r, err := transaction.Apply(root, "init", []transaction.Operation{{Path: ref, Directory: true, Mode: 0755}})
	if err != nil {
		t.Fatal(err)
	}
	id := &Identity{Root: root, Profile: domain.Profiles["backend"], Native: &identitymeta.Metadata{Profile: b.Profile, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, Managed: map[string]identitymeta.Managed{}}}
	if err = VerifyRecoveryCandidatePolicies(root, id, []string{ref}, r.TransactionID); err != nil {
		t.Fatalf("已登记目录不应按政策文件拒绝: %v", err)
	}
}

func TestRecoveryPolicyDeletionRemainsRejected(t *testing.T) {
	b, err := bundle.Load("backend")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	ref := ".template-spec/process/schemas/unknown.schema.json"
	file := filepath.Join(root, ref)
	if err = os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("user data"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := safefs.Describe(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	r, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: ref, Delete: true, Before: &before}})
	if err != nil {
		t.Fatal(err)
	}
	id := &Identity{Root: root, Profile: domain.Profiles["backend"], Native: &identitymeta.Metadata{Profile: b.Profile, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, Managed: map[string]identitymeta.Managed{}}}
	if err = VerifyRecoveryCandidatePolicies(root, id, []string{ref}, r.TransactionID); err == nil {
		t.Fatal("未知策略删除不能获得恢复授权")
	}
}

func TestRecoveryAcceptsTrustedArchivedPolicy(t *testing.T) {
	b, err := bundle.Load("backend")
	if err != nil {
		t.Fatal(err)
	}
	const ref = ".template-spec/process/orchestration-contract.yaml"
	f := b.Files[ref]
	raw, err := f.Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	d := domain.Descriptor{Type: "file", Digest: safefs.Digest(raw), Mode: domain.FileMode(f.Mode)}
	m := identitymeta.Managed{Baseline: d, Applied: d, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, BundleHash: b.BundleHash, Path: ref, Variant: "initial", TemplateDigest: f.Digest}}
	id := &Identity{Profile: domain.Profiles["backend"], Native: &identitymeta.Metadata{Profile: b.Profile, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, Managed: map[string]identitymeta.Managed{ref: m}}}
	root := t.TempDir()
	if _, err = transaction.Apply(root, "sync", []transaction.Operation{{Path: ref, Data: raw, Mode: uint32(f.Mode)}}); err != nil {
		t.Fatal(err)
	}
	if err = VerifyRecoveryCandidatePolicies(root, id, []string{ref}, ""); err != nil {
		t.Fatalf("受信归档政策应通过: %v", err)
	}
}

func TestRecoveryPolicyUsesSelectedTransaction(t *testing.T) {
	b, err := bundle.Load("backend")
	if err != nil {
		t.Fatal(err)
	}
	const ref = ".template-spec/process/orchestration-contract.yaml"
	f := b.Files[ref]
	raw, err := f.Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	d := domain.Descriptor{Type: "file", Digest: safefs.Digest(raw), Mode: domain.FileMode(f.Mode)}
	id := &Identity{Profile: domain.Profiles["backend"], Native: &identitymeta.Metadata{Profile: b.Profile, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, Managed: map[string]identitymeta.Managed{ref: {Baseline: d, Applied: d, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, BundleHash: b.BundleHash, Path: ref, Variant: "initial", TemplateDigest: f.Digest}}}}}
	for _, same := range []bool{false, true} {
		t.Run(map[bool]string{false: "different", true: "same"}[same], func(t *testing.T) {
			root := t.TempDir()
			a, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: ref, Data: raw, Mode: uint32(f.Mode)}})
			if err != nil {
				t.Fatal(err)
			}
			other := ".template-spec/process/schemas/unknown.schema.json"
			var before *domain.Descriptor
			if same {
				other = ref
				before = &d
			}
			btx, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: other, Data: []byte("forged"), Mode: 0644, Before: before}})
			if err != nil {
				t.Fatal(err)
			}
			if same {
				err = os.WriteFile(filepath.Join(root, ref), raw, os.FileMode(f.Mode))
			} else {
				err = os.Remove(filepath.Join(root, other))
			}
			if err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(btx.BackupPath, "journal.json")
			data, err := os.ReadFile(journal)
			if err != nil {
				t.Fatal(err)
			}
			var j map[string]any
			if err = json.Unmarshal(data, &j); err != nil {
				t.Fatal(err)
			}
			j["phase"] = "recovered"
			data, _ = json.Marshal(j)
			if err = os.WriteFile(journal, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err = VerifyRecoveryCandidatePolicies(root, id, []string{ref}, a.TransactionID); err != nil {
				t.Fatalf("应验证 A 的受信内容: %v", err)
			}
			if err = VerifyRecoveryCandidatePolicies(root, id, []string{ref}, ""); err == nil {
				t.Fatal("不得将后来的 B 当成 A")
			}
		})
	}
}
