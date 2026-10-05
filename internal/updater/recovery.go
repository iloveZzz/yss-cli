package updater

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

// toolRootPath applies the same explicit-root contract to installation and
// recovery. A tools/yss directory within a repository remains supported; the
// tool root itself must not be a repository or governance project root.
func toolRootPath(toolRoot string) (string, error) {
	if strings.TrimSpace(toolRoot) == "" {
		return "", fail("ARGUMENT", "程序操作须显式指定 --tool-root")
	}
	root, err := filepath.Abs(toolRoot)
	if err != nil {
		return "", err
	}
	if _, err = safefs.Path(root, receiptRef); err != nil {
		return "", err
	}
	for _, ref := range []string{".git", "yss-project.yaml"} {
		if _, err = os.Lstat(filepath.Join(root, ref)); err == nil {
			return "", fail("PROTECTED", "工具目录不能是 Git 仓库根或治理项目根: "+ref)
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	return root, nil
}

func cancellation(ctx context.Context) error {
	if ctx == nil {
		return fail("ARGUMENT", "程序操作须提供 context")
	}
	if err := ctx.Err(); err != nil {
		return domain.Wrap("CANCELLED", err)
	}
	return nil
}

// Recover restores the unique pending program-update transaction, including a
// first installation that was interrupted before installation.json was written.
// Transaction selection and the kind check occur under the same exclusive lock.
func Recover(ctx context.Context, toolRoot string) (transaction.Result, error) {
	if err := cancellation(ctx); err != nil {
		return transaction.Result{Status: "cancelled"}, err
	}
	root, err := toolRootPath(toolRoot)
	if err != nil {
		return transaction.Result{}, err
	}
	return transaction.RecoverKindContextWithValidator(ctx, root, "program-update", validateProgramScope)
}

func validateProgramScope(summary transaction.Summary, paths []string) error {
	if summary.Kind != "program-update" {
		return fail("KIND", "程序恢复不能使用其他事务类型")
	}
	if len(paths) == 0 {
		return fail("STATE", "程序事务缺少操作路径")
	}
	allowed := expectedFiles()
	allowed[receiptRef] = true
	for _, ref := range paths {
		if !allowed[ref] {
			return fail("SCOPE", "程序事务包含非程序路径: "+ref)
		}
	}
	return nil
}

// Rollback reverses only the most recent successful program-update transaction.
// Once restoration starts it completes its durable restore instead of leaving
// a partially reverted installation on context cancellation.
func Rollback(ctx context.Context, toolRoot string) (transaction.Result, error) {
	if err := cancellation(ctx); err != nil {
		return transaction.Result{Status: "cancelled"}, err
	}
	root, err := toolRootPath(toolRoot)
	if err != nil {
		return transaction.Result{}, err
	}
	if err = cancellation(ctx); err != nil {
		return transaction.Result{Status: "cancelled"}, err
	}
	return transaction.RollbackKindContextWithValidator(ctx, root, "program-update", validateProgramScope)
}
