package transaction

import (
	"context"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// ScopeValidator receives verified archive metadata and a detached list of the
// selected transaction's operation paths while its exclusive lock is held.
// Returning an error blocks restoration before any target mutation.
type ScopeValidator func(Summary, []string) error

func validateArchiveScope(l loaded, validate ScopeValidator) error {
	if validate == nil {
		return nil
	}
	paths := make([]string, len(l.plan.Operations))
	for i, r := range l.plan.Operations {
		paths[i] = r.Path
	}
	summary := Summary{TransactionID: l.plan.ID, Kind: l.plan.Kind, Phase: l.journal.Phase, PlanDigest: l.journal.PlanDigest, Operations: len(l.plan.Operations), CreatedAt: l.plan.CreatedAt, Sequence: l.plan.Sequence}
	return validate(summary, paths)
}

// RecoverKind restores a unique pending transaction only if its family matches.
func RecoverKind(root, wantKind string) (Result, error) {
	return RecoverKindContext(context.Background(), root, wantKind)
}

// RecoverKindContext checks cancellation before acquisition and again under the
// exclusive transaction lock. Once restoration begins it finishes the durable
// restore; abandoning it midway on cancellation would leave a partial rollback.
// The initial scan is only a no-write fast path. Selection and kind validation
// are repeated under the lock, never trusted from an earlier Status result.
func RecoverKindContext(ctx context.Context, root, wantKind string) (Result, error) {
	return RecoverKindContextWithValidator(ctx, root, wantKind, nil)
}

// RecoverKindContextWithValidator additionally validates application scope under
// the same lock as selection, family validation, drift checks and restoration.
func RecoverKindContextWithValidator(ctx context.Context, root, wantKind string, validate ScopeValidator) (Result, error) {
	if ctx == nil {
		return Result{}, fail("ARGUMENT", "恢复必须提供 context")
	}
	if err := ctx.Err(); err != nil {
		return Result{Status: "cancelled"}, domain.Wrap("CANCELLED", err)
	}
	if strings.TrimSpace(root) == "" {
		return Result{}, fail("ARGUMENT", "恢复必须显式指定 root")
	}
	if strings.TrimSpace(wantKind) == "" || strings.ContainsAny(wantKind, "\r\n\x00") {
		return Result{}, fail("KIND", "恢复必须指定非空事务 kind")
	}
	root, err := safeRoot(root, false)
	if err != nil {
		return Result{}, err
	}
	all, preparations, err := scan(root)
	if err != nil {
		return Result{}, err
	}
	hasPending := false
	for _, l := range all {
		if !terminal(l.journal.Phase) {
			hasPending = true
			break
		}
	}
	if !hasPending {
		return Result{Status: "unchanged", Preparations: preparations}, nil
	}
	if err = ctx.Err(); err != nil {
		return Result{Status: "cancelled"}, domain.Wrap("CANCELLED", err)
	}
	release, err := acquire(root)
	if err != nil {
		return Result{}, err
	}
	defer release()
	if err = ctx.Err(); err != nil {
		return Result{Status: "cancelled"}, domain.Wrap("CANCELLED", err)
	}
	all, preparations, err = scan(root)
	if err != nil {
		return Result{}, err
	}
	pending := []loaded{}
	for _, l := range all {
		if !terminal(l.journal.Phase) {
			pending = append(pending, l)
		}
	}
	if len(pending) == 0 {
		return Result{Status: "unchanged", Preparations: preparations}, nil
	}
	if len(pending) != 1 {
		return Result{Status: "blocked"}, fail("STATE", "多个未完成事务，不能猜测恢复顺序")
	}
	l := &pending[0]
	if l.plan.Kind != wantKind {
		return result(*l, "blocked"), fail("KIND", "未完成事务 kind="+l.plan.Kind+"，不能作为 "+wantKind+" 恢复")
	}
	if err = validateArchiveScope(*l, validate); err != nil {
		return result(*l, "blocked"), err
	}
	if err = ctx.Err(); err != nil {
		return Result{Status: "cancelled"}, domain.Wrap("CANCELLED", err)
	}
	return restore(root, l, l.journal.Phase == "rolling-back")
}

// RollbackKindContextWithValidator uses the generic rollback selection core; it
// never searches past a later committed or already rolled-back transaction.
func RollbackKindContextWithValidator(ctx context.Context, root, wantKind string, validate ScopeValidator) (Result, error) {
	if strings.TrimSpace(root) == "" {
		return Result{}, fail("ARGUMENT", "回退必须显式指定 root")
	}
	if strings.TrimSpace(wantKind) == "" || strings.ContainsAny(wantKind, "\r\n\x00") {
		return Result{}, fail("KIND", "回退必须指定非空事务 kind")
	}
	return rollbackWithValidator(ctx, root, wantKind, validate)
}
