package project

import (
	"os"
	"path/filepath"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/schema"
)

// CheckLegacyState keeps retained journal/lock formats under their original
// executor. Native recovery must never report a legacy interruption as idle.
func CheckLegacyState(root, profile string) error {
	for _, ref := range []string{".yss-harness-migrate.lock", ".yss-harness-state/lock.json"} {
		p, e := safefs.Path(root, ref)
		if e != nil {
			return e
		}
		if _, e = os.Lstat(p); e == nil {
			return domain.Fail("LEGACY_INTERRUPTED", "存在旧执行器锁，请先使用对应固定版本旧 CLI 检查和恢复: "+ref)
		} else if !os.IsNotExist(e) {
			return e
		}
	}
	state, e := safefs.Path(root, ".yss-harness-state")
	if e != nil {
		return e
	}
	info, e := os.Lstat(state)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if !info.IsDir() {
		return domain.Fail("STATE", "旧事务状态不是目录")
	}
	if p := filepath.Join(state, "upgrade.json"); filePresent(p) {
		v, e := schema.LoadFile(p)
		if e != nil {
			return e
		}
		m, ok := object(v)
		runs, okRuns := m["runs"].([]any)
		if !ok || number(m["schemaVersion"]) != 1 || !okRuns {
			return domain.Fail("STATE", "未知旧迁移状态，不猜测恢复")
		}
		if profile != "" && text(m["family"]) != domain.Profiles[profile].LegacyCommand {
			return domain.Fail("IDENTITY", "旧迁移状态所属家族与当前 Profile 矛盾")
		}
		for _, v := range runs {
			r, ok := object(v)
			if !ok {
				return domain.Fail("STATE", "旧迁移回执无效")
			}
			switch text(r["phase"]) {
			case "applied", "rolled-back", "recovered":
			case "applying", "rolling-back":
				return domain.Fail("LEGACY_INTERRUPTED", "旧迁移尚未完成，须先使用旧 CLI migrate recover")
			default:
				return domain.Fail("STATE", "未知旧迁移回执阶段")
			}
		}
	}
	if p := filepath.Join(state, "owner.json"); filePresent(p) {
		v, e := schema.LoadFile(p)
		if e != nil {
			return e
		}
		m, ok := object(v)
		if !ok || number(m["schemaVersion"]) != 1 {
			return domain.Fail("STATE", "未知旧事务状态所有者")
		}
		if profile != "" && text(m["profileId"]) != domain.Profiles[profile].ID {
			return domain.Fail("IDENTITY", "旧事务所有者与当前 Profile 矛盾")
		}
	}
	for side := range domain.Profiles {
		ref := ".yss-harness-state/" + side + "/transactions"
		dir, e := safefs.Path(root, ref)
		if e != nil {
			return e
		}
		entries, e := os.ReadDir(dir)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return e
		}
		if len(entries) > 0 && profile != "" && profile != side {
			return domain.Fail("IDENTITY", "检测到异族旧事务目录")
		}
		for _, entry := range entries {
			p, e := safefs.Path(root, ref+"/"+entry.Name()+"/journal.json")
			if e != nil {
				return e
			}
			v, e := schema.LoadFile(p)
			if e != nil {
				return e
			}
			m, ok := object(v)
			if !ok || (number(m["schemaVersion"]) != 1 && number(m["schemaVersion"]) != 2) || text(m["profileId"]) != domain.Profiles[side].ID {
				return domain.Fail("STATE", "未知或损坏的旧事务日志")
			}
			switch text(m["phase"]) {
			case "committed", "rolled-back":
			default:
				return domain.Fail("LEGACY_INTERRUPTED", "旧事务尚未完成，须先使用对应固定版本旧 CLI recover")
			}
		}
	}
	return nil
}
func filePresent(path string) bool { _, e := os.Lstat(path); return e == nil }
