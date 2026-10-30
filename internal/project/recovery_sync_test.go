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
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"github.com/iloveZzz/yss-cli/internal/trustguard"
)

func TestSyncRecoveryRejectsUntrustedOriginalPolicy(t *testing.T) {
	b, err := bundle.LoadBaseline("spec")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := writeInstallationBundleFixtureWithKind(t, b, InstallationGoverned)
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	ref := SpecWorkflowRef
	file := filepath.Join(root, ref)
	valid, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, []byte("untrusted"), 0644); err != nil {
		t.Fatal(err)
	}
	before, err := safefs.Describe(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: ref, Before: &before, Data: valid, Mode: 0644}})
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyRecoveryCandidatePolicies(root, id, []string{ref}, tx.TransactionID); err == nil {
		t.Fatal("未经认证的原策略不得恢复")
	}
	if got, e := os.ReadFile(file); e != nil || string(got) != string(valid) {
		t.Fatal("拒绝时不能更改现场")
	}
}

func TestSyncRecoveryRejectsUnapprovedPolicyDeletion(t *testing.T) {
	b, err := bundle.LoadBaseline("spec")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := writeInstallationBundleFixtureWithKind(t, b, InstallationGoverned)
	id, err := Detect(root, "spec", false)
	if err != nil {
		t.Fatal(err)
	}
	ref := SpecWorkflowRef
	before, err := safefs.Describe(root, ref)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: ref, Before: &before, Delete: true}})
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyRecoveryCandidatePolicies(root, id, []string{ref}, tx.TransactionID); err == nil {
		t.Fatal("目标来源没有退役规则时不得放行")
	}
	if _, e := os.Lstat(filepath.Join(root, ref)); !os.IsNotExist(e) {
		t.Fatal("拒绝时不能改写现场")
	}
}

func TestSyncRecoveryChecksPendingMetadataState(t *testing.T) {
	b, err := bundle.LoadBaseline("backend")
	if err != nil {
		t.Fatal(err)
	}
	root, _ := writeInstallationBundleFixtureWithKind(t, b, InstallationGoverned)
	file := filepath.Join(root, MetadataFile)
	original, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var meta Metadata
	if err = json.Unmarshal(original, &meta); err != nil {
		t.Fatal(err)
	}
	meta.CLIVersion = "3.0.1"
	after, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	before := domain.Descriptor{Type: "file", Digest: safefs.Digest(original), Mode: 0644}
	tx, err := transaction.Apply(root, "sync", []transaction.Operation{{Path: MetadataFile, Before: &before, Data: after, Mode: 0644}})
	if err != nil {
		t.Fatal(err)
	}
	journal := filepath.Join(tx.BackupPath, "journal.json")
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
	if err = os.WriteFile(file, original, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = syncRecoveryIdentity(root, "backend", tx.TransactionID); err != nil {
		t.Fatalf("受信原态应允许恢复: %v", err)
	}
	if err = os.WriteFile(file, append(original, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = syncRecoveryIdentity(root, "backend", tx.TransactionID); err == nil {
		t.Fatal("漂移的 metadata 不能取得恢复身份")
	}
}

func TestSyncRetirementRequiresMatchingBundleRule(t *testing.T) {
	b, err := bundle.LoadBaseline("spec")
	if err != nil {
		t.Fatal(err)
	}
	ref := ".template-spec/process/schemas/context-reconciliation.schema.json"
	originalFile := b.Files[ref]
	delete(b.Files, ref)
	previous := &Metadata{Profile: b.Profile, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, BundleHash: b.BundleHash}
	target := &Metadata{Managed: map[string]Managed{}}
	b.Upgrade.Rules = append(b.Upgrade.Rules, bundle.MigrationRule{ID: "test.retire-policy", From: bundle.SourceMatch{Profile: previous.Profile, TemplateCommit: previous.TemplateCommit, SnapshotHash: previous.SnapshotHash, BundleHash: previous.BundleHash}, Action: "delete", SourcePath: ref})
	if err = verifyRecoveryRetirementInBundle(target, previous, ref, nil, nil, b); err != nil {
		t.Fatalf("匹配的受信策略退役规则应允许恢复: %v", err)
	}
	wrong := *previous
	wrong.TemplateCommit = "1111111111111111111111111111111111111111"
	if err = verifyRecoveryRetirementInBundle(target, &wrong, ref, nil, nil, b); err == nil {
		t.Fatal("规则来源不匹配时不得恢复")
	}
	target.Managed[ref] = Managed{}
	if err = verifyRecoveryRetirementInBundle(target, previous, ref, nil, nil, b); err == nil {
		t.Fatal("目标仍登记该策略时不得恢复")
	}
	delete(target.Managed, ref)
	b.Files[ref] = originalFile
	if err = verifyRecoveryRetirementInBundle(target, previous, ref, nil, nil, b); err == nil {
		t.Fatal("目标来源仍包含该策略时不得恢复")
	}
}

func TestSyncRetirementRenameRequiresTrustedTarget(t *testing.T) {
	b, err := bundle.LoadBaseline("spec")
	if err != nil {
		t.Fatal(err)
	}
	retired := ".template-spec/process/schemas/context-reconciliation.schema.json"
	destination := SpecWorkflowRef
	f := b.Files[destination]
	data, err := f.Render(nil)
	if err != nil {
		t.Fatal(err)
	}
	desc := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: domain.FileMode(f.Mode)}
	previous := &Metadata{Profile: b.Profile, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, BundleHash: b.BundleHash}
	target := &Metadata{Profile: b.Profile, TemplateVersion: b.TemplateVersion, TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, ManifestHash: b.ManifestHash, BundleHash: b.BundleHash, TemplateSourceState: b.SourceState, Managed: map[string]Managed{
		destination: {Baseline: desc, Applied: desc, Source: &identitymeta.BaselineSource{Kind: "bundle", TemplateCommit: b.TemplateCommit, SnapshotHash: b.SnapshotHash, BundleHash: b.BundleHash, Path: destination, Variant: "initial", TemplateDigest: f.Digest}},
	}}
	guard, err := trustguard.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	delete(b.Files, retired)
	rule := bundle.MigrationRule{ID: "test.rename-policy", From: bundle.SourceMatch{Profile: previous.Profile, TemplateCommit: previous.TemplateCommit, SnapshotHash: previous.SnapshotHash, BundleHash: previous.BundleHash}, Action: "rename", SourcePath: retired, TargetPath: destination}
	b.Upgrade.Rules = append(b.Upgrade.Rules, rule)
	var matchingArchive *transaction.CandidateArchive
	matchingRoot := t.TempDir()
	for _, tc := range []struct {
		name                     string
		missing, content, mode   bool
		wrongSource, wrongTarget bool
		wrongPath, expected      bool
	}{
		{name: "匹配目标", expected: true},
		{name: "目标缺失", missing: true},
		{name: "目标字节被改", content: true},
		{name: "目标权限被改", mode: true},
		{name: "旧来源不匹配", wrongSource: true},
		{name: "目标路径错误", wrongTarget: true},
		{name: "原路径错误", wrongPath: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := matchingArchive
			if archive == nil || tc.missing || tc.content || tc.mode {
				root := matchingRoot
				if !tc.expected {
					root = t.TempDir()
				}
				path, content, mode := destination, data, f.Mode
				if tc.missing {
					path = ".template-spec/process/schemas/other.json"
				}
				if tc.content {
					content = append(append([]byte(nil), data...), '\n')
				}
				if tc.mode {
					mode = 0755
				}
				tx, e := transaction.Apply(root, "sync", []transaction.Operation{{Path: path, Data: content, Mode: mode}})
				if e != nil {
					t.Fatal(e)
				}
				archive, e = transaction.OpenCandidateArchive(root, tx.TransactionID)
				if e != nil {
					t.Fatal(e)
				}
				if tc.expected {
					matchingArchive = archive
				}
			}
			old := *previous
			if tc.wrongSource {
				old.TemplateCommit = "1111111111111111111111111111111111111111"
			}
			candidateRule := rule
			if tc.wrongTarget {
				candidateRule.TargetPath = ".template-spec/process/schemas/unregistered.json"
			}
			if tc.wrongPath {
				candidateRule.SourcePath = ".template-spec/process/schemas/other.json"
			}
			b.Upgrade.Rules[len(b.Upgrade.Rules)-1] = candidateRule
			e := verifyRecoveryRetirementInBundle(target, &old, retired, guard, archive, b)
			if (e == nil) != tc.expected {
				t.Fatalf("改名验证不符合预期: %v", e)
			}
		})
	}
}
