//go:build !yss_nobundle

package project

import (
	"context"
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

func TestMigrationRollbackRequiresTrustedPriorSource(t *testing.T) {
	b, err := bundle.LoadBaseline("spec")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, version                   string
		empty, tampered, recover, mixed bool
		want                            bool
	}{{"same-generation", b.TemplateVersion, false, false, false, false, true}, {"old-product", "4.6.1", false, false, false, false, false}, {"forged-empty", b.TemplateVersion, true, false, false, false, false}, {"tampered-anchor", b.TemplateVersion, false, true, false, false, false}, {"same-generation-recover", b.TemplateVersion, false, false, true, false, true}, {"old-product-recover", "4.6.1", false, false, true, false, false}, {"mixed-governance-rollback", b.TemplateVersion, false, false, false, true, false}, {"mixed-governance-recover", b.TemplateVersion, false, false, true, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			history := []byte(`{"source_profile":"spec"}`)
			historyHash := safefs.Digest(history)
			meta := Metadata{SchemaVersion: 5, BundleSchemaVersion: 5, ProtocolVersion: domain.ProtocolVersion, Profile: "spec", ProfileID: domain.Profiles["spec"].ID, CLIVersion: "3.0.0", LegacyCLIVersion: "3.0.0", TemplateVersion: tc.version, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, CLISourceState: "unknown", Variables: map[string]string{}, Distribution: map[string]any{"installation": "skills"}, Managed: map[string]Managed{}, BaselineDigest: safefs.Digest([]byte("{}")), GovernanceHistory: &identitymeta.GovernanceHistory{Ref: ".template-spec/compatibility/governance-history/" + historyHash + "/manifest.json", Digest: historyHash}}
			if !tc.empty {
				ref := ".agents/skills/code-review/SKILL.md"
				f, ok := b.Files[ref]
				if !ok {
					t.Fatal("官方 Bundle 缺少技能锚")
				}
				data, e := f.Render(meta.Variables)
				if e != nil {
					t.Fatal(e)
				}
				d := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: domain.FileMode(f.Mode)}
				meta.Managed[ref] = Managed{Baseline: d, Applied: d, Ownership: f.Ownership, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, BundleHash: b.BundleHash, Path: ref, Variant: "initial", TemplateDigest: f.Digest}}
				managed, _ := json.Marshal(meta.Managed)
				meta.BaselineDigest = safefs.Digest(managed)
				file := filepath.Join(root, ref)
				if e = os.MkdirAll(filepath.Dir(file), 0755); e != nil {
					t.Fatal(e)
				}
				if tc.tampered {
					data = append(data, '\n')
				}
				if e = os.WriteFile(file, data, os.FileMode(f.Mode)); e != nil {
					t.Fatal(e)
				}
			}
			historyFile := filepath.Join(root, meta.GovernanceHistory.Ref)
			if err = os.MkdirAll(filepath.Dir(historyFile), 0755); err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(historyFile, history, 0644); err != nil {
				t.Fatal(err)
			}
			old, _ := json.Marshal(meta)
			file := filepath.Join(root, MetadataFile)
			if err = os.WriteFile(file, old, 0644); err != nil {
				t.Fatal(err)
			}
			before, err := safefs.Describe(root, MetadataFile)
			if err != nil {
				t.Fatal(err)
			}
			meta.CLIVersion = "3.0.1"
			after, _ := json.Marshal(meta)
			ops := []transaction.Operation{{Path: MetadataFile, Before: &before, Data: after, Mode: 0644}}
			policyFile := ""
			var policyAfter []byte
			if tc.mixed {
				ref := ".template-spec/process/orchestration-contract.yaml"
				policyFile = filepath.Join(root, ref)
				if err = os.MkdirAll(filepath.Dir(policyFile), 0755); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(policyFile, []byte("old untrusted policy"), 0644); err != nil {
					t.Fatal(err)
				}
				oldPolicy, e := safefs.Describe(root, ref)
				if e != nil {
					t.Fatal(e)
				}
				f := b.Files[ref]
				policyAfter, e = f.Render(nil)
				if e != nil {
					t.Fatal(e)
				}
				ops = append(ops, transaction.Operation{Path: ref, Before: &oldPolicy, Data: policyAfter, Mode: uint32(f.Mode)})
			}
			tx, err := transaction.Apply(root, "migrate", ops)
			if err != nil {
				t.Fatal(err)
			}
			user := filepath.Join(root, "user.txt")
			if err = os.WriteFile(user, []byte("business"), 0644); err != nil {
				t.Fatal(err)
			}
			if tc.recover {
				journal := filepath.Join(tx.BackupPath, "journal.json")
				body, e := os.ReadFile(journal)
				if e != nil {
					t.Fatal(e)
				}
				var j map[string]any
				if e = json.Unmarshal(body, &j); e != nil {
					t.Fatal(e)
				}
				j["phase"] = "applying"
				body, _ = json.Marshal(j)
				if e = os.WriteFile(journal, body, 0600); e != nil {
					t.Fatal(e)
				}
			}
			validator := func(summary transaction.Summary, _ []string) error {
				if summary.TransactionID != tx.TransactionID {
					t.Fatal("事务 ID 不符")
				}
				return ValidateMigrationRollbackForTransaction(root, summary.TransactionID)
			}
			if tc.recover {
				_, err = transaction.RecoverContextWithValidator(context.Background(), root, validator)
			} else {
				_, err = transaction.RollbackContextWithValidator(context.Background(), root, validator)
			}
			if (err == nil) != tc.want {
				t.Fatalf("回滚前态来源: %v", err)
			}
			if got, e := os.ReadFile(user); e != nil || string(got) != "business" {
				t.Fatal("验证改变用户文件")
			}
			want := after
			if tc.want {
				want = old
			}
			if got, e := os.ReadFile(file); e != nil || string(got) != string(want) {
				t.Fatal("回滚 metadata 不符合预期")
			}
			if got, e := os.ReadFile(historyFile); e != nil || string(got) != string(history) {
				t.Fatal("历史文件发生变化")
			}
			if tc.mixed {
				if got, e := os.ReadFile(policyFile); e != nil || string(got) != string(policyAfter) {
					t.Fatal("拒绝恢复时政策文件发生变化")
				}
			}
		})
	}
}

func TestGovernedMigrationRollbackRequiresWorkflowAndRootIdentity(t *testing.T) {
	b, err := bundle.LoadBaseline("spec")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name        string
		omit        bool
		rootVersion string
		want        bool
	}{
		{"valid", false, "2", true}, {"omitted-workflow", true, "2", false}, {"mixed-root-identity", false, "1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			managed := map[string]Managed{}
			for _, ref := range []string{".template-spec/process/harness-profile.yaml", SpecWorkflowRef} {
				f, ok := b.Files[ref]
				if !ok {
					t.Fatal("基线缺少政策", ref)
				}
				if ref == ".template-spec/process/harness-profile.yaml" {
					f, err = nativeprofile.Generate(f, domain.Profiles["spec"])
					if err != nil {
						t.Fatal(err)
					}
				}
				data, e := f.Render(nil)
				if e != nil {
					t.Fatal(e)
				}
				d := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: domain.FileMode(f.Mode)}
				managed[ref] = Managed{Baseline: d, Applied: d, Ownership: f.Ownership, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, BundleHash: b.BundleHash, Path: ref, Variant: "initial", TemplateDigest: f.Digest}}
				file := filepath.Join(root, ref)
				if e = os.MkdirAll(filepath.Dir(file), 0755); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(file, data, os.FileMode(f.Mode)); e != nil {
					t.Fatal(e)
				}
			}
			if err = os.WriteFile(filepath.Join(root, "yss-project.yaml"), []byte("schema_version: "+tc.rootVersion+"\nrepository_mode: project-instance\n"), 0644); err != nil {
				t.Fatal(err)
			}
			distribution := map[string]any{"installation": "governed"}
			if !tc.omit {
				distribution["specWorkflow"] = identitymeta.MattSpecWorkflow
			}
			mraw, _ := json.Marshal(managed)
			meta := Metadata{SchemaVersion: 5, BundleSchemaVersion: 5, ProtocolVersion: domain.ProtocolVersion, Profile: "spec", ProfileID: domain.Profiles["spec"].ID, CLIVersion: "3.0.0", LegacyCLIVersion: "3.0.0", TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, CLISourceState: "unknown", Variables: map[string]string{}, Distribution: distribution, Managed: managed, BaselineDigest: safefs.Digest(mraw)}
			old, _ := json.Marshal(meta)
			file := filepath.Join(root, MetadataFile)
			if err = os.WriteFile(file, old, 0644); err != nil {
				t.Fatal(err)
			}
			before, err := safefs.Describe(root, MetadataFile)
			if err != nil {
				t.Fatal(err)
			}
			meta.CLIVersion = "3.0.1"
			after, _ := json.Marshal(meta)
			tx, err := transaction.Apply(root, "migrate", []transaction.Operation{{Path: MetadataFile, Before: &before, Data: after, Mode: 0644}})
			if err != nil {
				t.Fatal(err)
			}
			err = ValidateMigrationRollbackForTransaction(root, tx.TransactionID)
			if (err == nil) != tc.want {
				t.Fatalf("政策与身份校验不符: %v", err)
			}
		})
	}
}

func TestInstallationMigrationWithHistoryCanRollback(t *testing.T) {
	b, err := bundle.LoadBaseline("backend")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := writeInstallationBundleFixture(t, b)
	file := filepath.Join(root, MetadataFile)
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var meta Metadata
	if err = json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	history := []byte(`{"source_profile":"backend"}`)
	digest := safefs.Digest(history)
	meta.GovernanceHistory = &identitymeta.GovernanceHistory{Ref: ".template-spec/compatibility/governance-history/" + digest + "/manifest.json", Digest: digest}
	historyFile := filepath.Join(root, meta.GovernanceHistory.Ref)
	if err = os.MkdirAll(filepath.Dir(historyFile), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(historyFile, history, 0644); err != nil {
		t.Fatal(err)
	}
	old, _ := json.Marshal(meta)
	if err = os.WriteFile(file, old, 0644); err != nil {
		t.Fatal(err)
	}
	plan, err := buildCore(root, "backend", "migrate", nil, nil, nil, PlanningOptions{target: b, Installation: InstallationGoverned})
	if err != nil {
		t.Fatal(err)
	}
	if plan.InstallationMigration == nil {
		t.Fatal("没有同代安装转换计划")
	}
	all, err := nativePlanOperations(plan)
	if err != nil {
		t.Fatal(err)
	}
	var selected []transaction.Operation
	for _, op := range all {
		if op.Path == MetadataFile {
			selected = append(selected, op)
		}
	}
	if len(selected) != 1 {
		t.Fatal("安装转换计划缺少 metadata 变更")
	}
	tx, err := transaction.Apply(root, "migrate", selected)
	if err != nil {
		t.Fatal(err)
	}
	user := filepath.Join(root, "user.txt")
	if err = os.WriteFile(user, []byte("business"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err = transaction.RollbackContextWithValidator(context.Background(), root, func(summary transaction.Summary, _ []string) error {
		if summary.TransactionID != tx.TransactionID {
			t.Fatal("事务 ID 不符")
		}
		return ValidateMigrationRollbackForTransaction(root, summary.TransactionID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, e := os.ReadFile(file); e != nil || string(got) != string(old) {
		t.Fatal("原身份未恢复")
	}
	if got, e := os.ReadFile(user); e != nil || string(got) != "business" {
		t.Fatal("用户文件改变")
	}
	if got, e := os.ReadFile(historyFile); e != nil || string(got) != string(history) {
		t.Fatal("历史文件改变")
	}
}
