package project

import (
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"io/fs"
	"path/filepath"
)

// The only nonempty init exception is its own fully restored transaction
// history. No business file, installed identity, unfinished state or foreign
// profile can make a project eligible for re-initialization.
func verifyRestoredInitialization(id *Identity) error {
	if id.Native != nil || id.Legacy != nil {
		return domain.Fail("CONFLICT", "已安装实例不能重新初始化")
	}
	status, err := transaction.Status(id.Root)
	if err != nil {
		return err
	}
	if len(status.Pending) != 0 || len(status.Preparations) != 0 {
		return domain.Fail("CONFLICT", "初始化历史尚未完整恢复")
	}
	if len(status.Transactions) == 0 {
		ok, e := transaction.SealedInitialization(id.Root, id.Profile.Name)
		if e != nil {
			return e
		}
		if ok {
			return nil
		}
		return domain.Fail("CONFLICT", "缺少已验证初始化历史")
	}
	latest := status.Transactions[len(status.Transactions)-1]
	if latest.Kind != "init" || (latest.Phase != "rolled-back" && latest.Phase != "recovered") {
		return domain.Fail("CONFLICT", "非已回退初始化历史")
	}
	archived, err := RecoveryIdentity(id.Root, id.Profile.Name)
	if err != nil {
		return err
	}
	if archived.Profile.Name != id.Profile.Name {
		return domain.Fail("IDENTITY", "初始化历史Profile冲突")
	}
	allowed := map[string]bool{".": true}
	for ref := range archived.Native.Managed {
		for parent := filepath.Dir(filepath.FromSlash(ref)); parent != "."; parent = filepath.Dir(parent) {
			allowed[parent] = true
		}
	}
	// Rollback can leave empty parents of the archived managed files. Only
	// those registered paths may coexist with the verified transaction store.
	return filepath.WalkDir(id.Root, func(p string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		ref, err := filepath.Rel(id.Root, p)
		if err != nil {
			return err
		}
		if ref == ".yss" && entry.IsDir() {
			return filepath.SkipDir
		}
		if !entry.IsDir() || !allowed[ref] {
			return domain.Fail("CONFLICT", "init 需要空目录或完整回退后的受管空目录")
		}
		return nil
	})
}
