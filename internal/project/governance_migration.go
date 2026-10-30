package project

import (
	"encoding/json"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/identitymeta"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

type GovernanceMigration struct {
	SourceProfile string                         `json:"sourceProfile"`
	History       identitymeta.GovernanceHistory `json:"history"`
}

func migrationIdentity(root, profile, command string, allowAbsent bool) (*Identity, error) {
	id, err := Detect(root, profile, allowAbsent)
	if err != nil {
		return nil, err
	}
	if command == "migrate" {
		return nil, domain.Fail("MIGRATION_UNSUPPORTED", "旧治理迁移已停止；同代安装方式转换请指定 --installation，旧实例请使用原版本工具")
	}
	return id, nil
}

func migrationRecoveryIdentity(root, explicit, transactionID string) (*Identity, error) {
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
	raw, err := recoveryFile(root, transactionID, MetadataFile)
	if err != nil {
		return nil, err
	}
	m, err := identitymeta.ValidateNative(root, raw, "")
	if err != nil {
		return nil, err
	}
	if err = verifyRecoverySource(root, m, transactionID); err != nil {
		return nil, err
	}
	if m.SchemaVersion < 4 || m.GovernanceHistory == nil {
		return nil, domain.Fail("IDENTITY", "迁移归档缺少明确新身份及原快照")
	}
	if explicit != "" && explicit != m.Profile {
		return nil, domain.Fail("IDENTITY", "恢复 Profile 与固定迁移目标不符")
	}
	manifest, err := recoveryFile(root, transactionID, m.GovernanceHistory.Ref)
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
		after, err := recoveryFile(root, transactionID, ref)
		if err != nil {
			return nil, err
		}
		before, err := recoveryFile(root, transactionID, prefix+identitymeta.HistoryFile(ref))
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
