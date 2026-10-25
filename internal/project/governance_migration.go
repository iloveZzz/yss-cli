package project

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

type GovernanceMigration struct {
	SourceProfile string                         `json:"sourceProfile"`
	History       identitymeta.GovernanceHistory `json:"history"`
}

func migrationIdentity(root, profile, command string, allowAbsent bool) (*Identity, error) {
	if command != "migrate" {
		return Detect(root, profile, allowAbsent)
	}
	id, err := Detect(root, "", false)
	if err != nil {
		return nil, err
	}
	if id.Native == nil || id.Native.SchemaVersion != 3 {
		return nil, domain.Fail("MIGRATION_UNSUPPORTED", "仅支持统一 metadata3、identity1、policy1 的显式迁移；原件保留")
	}
	source := kernel.NewSemanticSession(context.Background(), id.Root, nil)
	if err = source.Authorities(); err != nil {
		return nil, err
	}
	if source.GovernanceVersion != 1 {
		return nil, domain.Fail("MIGRATION_UNSUPPORTED", "迁移只接受已核验的原 policy1")
	}
	target := id.Profile.Name

	if profile != "" && profile != target {
		return nil, domain.Fail("IDENTITY", "迁移目标与支持的 Spec/Backend 路径不匹配")
	}
	id.Profile = domain.Profiles[target]
	return id, nil
}

func planOriginalGovernance(p *Plan, id *Identity) (*identitymeta.GovernanceHistory, error) {
	if p.Command != "migrate" {
		if id.Native != nil {
			return id.Native.GovernanceHistory, nil
		}
		return nil, nil
	}
	identity, err := load(id.Root, "yss-project.yaml")
	if err != nil {
		return nil, err
	}
	doc, ok := object(identity)
	if !ok || len(doc) != 2 || number(doc["schema_version"]) != 1 || doc["repository_mode"] != "project-instance" {
		return nil, domain.Fail("MIGRATION_UNSUPPORTED", "原根身份不属于支持的精确 identity1 合同")
	}
	snapshot := identitymeta.GovernanceSnapshot{SchemaVersion: 1, Kind: "governance-original-source", SourceProfile: id.Native.Profile, Files: map[string]domain.Descriptor{}}
	data := map[string][]byte{}
	total := 0
	err = filepath.WalkDir(id.Root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		ref, err := filepath.Rel(id.Root, file)
		if err != nil {
			return err
		}
		ref = filepath.ToSlash(ref)
		if ref == "." {
			return nil
		}
		if entry.IsDir() && (entry.Name() == ".git" || entry.Name() == "node_modules" || ref == ".yss/transactions" || ref == ".yss/asset-transactions" || ref == ".template-spec/compatibility/governance-history") {
			return filepath.SkipDir
		}
		if entry.Name() == ".git" {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return domain.Fail("MIGRATION_UNSUPPORTED", "迁移范围含符号链接，原件保留: "+ref)
		}
		if entry.IsDir() {
			return nil
		}
		if err := safefs.ValidateRef(ref); err != nil {
			return err
		}
		before, err := safefs.Describe(id.Root, ref)
		if err != nil {
			return err
		}
		if before.Type != "file" {
			return domain.Fail("MIGRATION_UNSUPPORTED", "迁移范围含非普通文件: "+ref)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if safefs.Digest(raw) != before.Digest {
			return domain.Fail("INPUT_DRIFT", "封存原件时字节变化: "+ref)
		}
		total += len(raw)
		if total > 128*1024*1024 || len(data) >= 20000 {
			return domain.Fail("MIGRATION_UNSUPPORTED", "原治理快照超过受支持的 128 MiB / 20000 文件范围，未写入")
		}
		snapshot.Files[ref] = before
		data[ref] = raw
		p.Inputs[ref] = before
		return nil
	})
	if err != nil {
		return nil, err
	}
	raw, err := jsonBytes(snapshot)
	if err != nil {
		return nil, err
	}
	hash := safefs.Digest(raw)
	prefix := ".template-spec/compatibility/governance-history/" + hash
	history := &identitymeta.GovernanceHistory{Ref: prefix + "/manifest.json", Digest: hash}
	for ref, bytes := range data {
		if err = addHistoryFile(p, prefix+"/files/"+identitymeta.HistoryFile(ref), bytes, snapshot.Files[ref].Mode); err != nil {
			return nil, err
		}
	}
	if err = addHistoryFile(p, history.Ref, raw, 0644); err != nil {
		return nil, err
	}
	p.GovernanceMigration = &GovernanceMigration{SourceProfile: id.Native.Profile, History: *history}
	return history, nil
}
func addHistoryFile(p *Plan, ref string, data []byte, mode uint32) error {
	before, err := safefs.Describe(p.Root, ref)
	if err != nil {
		return err
	}
	if before.Type != "missing" {
		return domain.Fail("MIGRATION_COLLISION", "历史快照目标已存在，拒绝覆盖: "+ref)
	}
	after := domain.Descriptor{Type: "file", Digest: safefs.Digest(data), Mode: mode}
	p.Inputs[ref] = before
	p.Changes = append(p.Changes, Change{ref, before, after, base64.StdEncoding.EncodeToString(data), "preserved-original-source"})
	addSystemAsset(p, ref, before, after, "preserved-original-source")
	return nil
}

// A committed migration cannot be downgraded: new untracked records may already exist.
func ValidateMigrationRollback(root string) error {
	raw, err := os.ReadFile(filepath.Join(root, MetadataFile))
	if err != nil {
		return domain.Wrap("IDENTITY", err)
	}
	var m Metadata
	if err = json.Unmarshal(raw, &m); err != nil {
		return err
	}
	if m.SchemaVersion >= 4 && m.GovernanceHistory != nil && strings.HasPrefix(m.GovernanceHistory.Ref, ".template-spec/compatibility/governance-history/") {
		return domain.Fail("DOWNGRADE_UNSUPPORTED", "已完成的治理迁移不支持降级；保留原快照和所有新记录。未完成事务使用 recover。")
	}
	return nil
}

func migrationRecoveryIdentity(root, explicit string) (*Identity, error) {
	status, err := transaction.Status(root)
	if err != nil {
		return nil, err
	}
	pendingMigration := false
	for _, tx := range status.Transactions {
		if len(status.Pending) == 1 && tx.TransactionID == status.Pending[0] && tx.Kind == "migrate" {
			pendingMigration = true
		}
	}
	if !pendingMigration {
		return nil, domain.Fail("IDENTITY", "没有唯一未完成治理迁移")
	}
	raw, err := transaction.ArchivedFile(root, MetadataFile)
	if err != nil {
		return nil, err
	}
	m, err := identitymeta.ValidateNative(root, raw, "")
	if err != nil {
		return nil, err
	}
	if m.SchemaVersion < 4 || m.GovernanceHistory == nil {
		return nil, domain.Fail("IDENTITY", "迁移归档缺少明确新身份及原快照")
	}
	if explicit != "" && explicit != m.Profile {
		return nil, domain.Fail("IDENTITY", "恢复 Profile 与固定迁移目标不符")
	}
	manifest, err := transaction.ArchivedFile(root, m.GovernanceHistory.Ref)
	if err != nil {
		return nil, err
	}
	if err = identitymeta.ValidateHistoryProfile(m.GovernanceHistory, manifest); err != nil {
		return nil, err
	}
	var snap identitymeta.GovernanceSnapshot
	if err = json.Unmarshal(manifest, &snap); err != nil {
		return nil, err
	}
	prefix := strings.TrimSuffix(m.GovernanceHistory.Ref, "manifest.json") + "files/"
	for _, ref := range []string{MetadataFile, "yss-project.yaml", ".template-spec/process/harness-profile.yaml"} {
		after, err := transaction.ArchivedFile(root, ref)
		if err != nil {
			return nil, err
		}
		before, err := transaction.ArchivedFile(root, prefix+identitymeta.HistoryFile(ref))
		if err != nil {
			return nil, err
		}
		want, ok := snap.Files[ref]
		if !ok || safefs.Digest(before) != want.Digest {
			return nil, domain.Fail("IDENTITY", "迁移归档原身份未绑定")
		}
		current, err := safefs.Describe(root, ref)
		if err != nil {
			return nil, err
		}
		if current != want && (current.Type != "file" || current.Digest != safefs.Digest(after) || current.Mode != 0644) {
			return nil, domain.Fail("IDENTITY", "迁移途中身份发生未登记修改: "+ref)
		}
	}
	p, _ := domain.GetProfile(m.Profile)
	return &Identity{Root: root, Profile: p, Mode: "project-instance", Native: m}, nil
}
