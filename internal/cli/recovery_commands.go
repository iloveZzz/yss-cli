package cli

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

func recoveryCommand(ctx context.Context, o options) (any, string, error) {

	command := o.args[0]
	profile := o.values["profile"]
	root, err := projectRoot(o)
	if err != nil {
		return nil, profile, err
	}
	if err = ctx.Err(); err != nil {
		return nil, profile, domain.Wrap("CANCELLED", err)
	}
	if command == "rollback" {
		for k := range o.values {
			switch k {
			case "root", "target-dir", "profile", "json", "help", "apply":
			default:
				return nil, profile, domain.Fail("ARGUMENT", "rollback不支持参数: --"+k)
			}
		}
		id, e := project.RecoveryIdentity(root, profile)
		if e != nil {
			return nil, profile, e
		}
		profile = id.Profile.Name
		if o.values["apply"] != "true" {
			r, e := transaction.Status(root)
			return r, profile, e
		}
		r, e := transaction.RollbackContextWithValidator(ctx, root, func(summary transaction.Summary, paths []string) error {
			switch summary.Kind {
			case "profile-links":
				if e := project.ValidateProfileLinksTransaction(root, profile, paths); e != nil {
					return e
				}
			case "spec-baseline-import":
				if profile != "design" {
					return domain.Fail("IDENTITY", "Spec 基线接入仅支持 Design")
				}
				if e := governance.ValidateSpecBaselineImportTransaction(root, profile, paths); e != nil {
					return e
				}
			case "lifecycle-target":
				if e := governance.ValidateProgressionTransaction(root, profile, paths); e != nil {
					return e
				}
			case "reading-render", "reading-enable":
				if e := governance.ValidateReadingTransaction(root, profile, paths); e != nil {
					return e
				}
			case "init", "attach", "sync", "migrate", "skills", "assets":
			default:
				return domain.Fail("KIND", "rollback仅支持项目事务")
			}
			current, e := project.RecoveryIdentity(root, profile)
			if e != nil {
				return e
			}
			if current.Profile.Name != profile {
				return domain.Fail("IDENTITY", "回退时Profile已变化")
			}
			return nil
		})
		return r, profile, e
	}
	if command == "recover" {
		for k := range o.values {
			switch k {
			case "root", "target-dir", "profile", "json", "help", "version", "apply":
			default:
				return nil, profile, domain.Fail("ARGUMENT", "恢复/状态不支持参数: --"+k)
			}
		}
		applyRecovery := o.values["apply"] == "true"
		preparation, handled, e := project.RecoverPreparation(ctx, root, profile, applyRecovery)
		if e != nil {
			return nil, profile, e
		}
		if handled {
			return preparation, profile, nil
		}
		id, e := project.RecoveryIdentity(root, profile)
		if e != nil {
			return nil, profile, e
		}
		profile = id.Profile.Name
		var r transaction.Result
		if !applyRecovery {
			r, err = transaction.Status(root)
			return r, profile, err
		}
		r, err = transaction.RecoverContextWithValidator(ctx, root, func(summary transaction.Summary, paths []string) error {
			switch summary.Kind {
			case "profile-links":
				if e := project.ValidateProfileLinksTransaction(root, profile, paths); e != nil {
					return e
				}
			case "spec-baseline-import":
				if profile != "design" {
					return domain.Fail("IDENTITY", "Spec 基线接入仅支持 Design")
				}
				if e := governance.ValidateSpecBaselineImportTransaction(root, profile, paths); e != nil {
					return e
				}
			case "lifecycle-target":
				if e := governance.ValidateProgressionTransaction(root, profile, paths); e != nil {
					return e
				}
			case "reading-render", "reading-enable":
				if e := governance.ValidateReadingTransaction(root, profile, paths); e != nil {
					return e
				}
			case "init", "attach", "sync", "migrate", "skills", "assets":
			default:
				return domain.Fail("KIND", "recover仅支持项目事务")
			}
			current, e := project.RecoveryIdentity(root, profile)
			if e != nil {
				return e
			}
			if current.Profile.Name != profile {
				return domain.Fail("IDENTITY", "恢复时Profile已变化")
			}
			return nil
		})
		return r, profile, err
	}

	return nil, profile, argumentError("未知恢复命令")
}
