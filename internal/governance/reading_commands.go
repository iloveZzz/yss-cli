package governance

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/governance/reading"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"github.com/iloveZzz/yss-cli/internal/transaction"
)

// readingRun 提供 yss reading 的只读命令。会话与语义校验使用同一套权威加载，
// 因此输入、事务与 Context 的前置拒绝和 reading-transition 保持一致。
func readingRun(ctx context.Context, action, root string, args map[string]string) (any, error) {
	s := kernel.NewSemanticSession(ctx, root, args)
	if err := s.Guard(); err != nil {
		return nil, err
	}
	if err := s.Authorities(); err != nil {
		return nil, err
	}
	if _, err := s.ContextContract(); err != nil {
		return nil, err
	}
	switch action {
	case "render":
		return readingRender(ctx, root, args)
	case "plan-enable":
		return readingPlanEnable(ctx, root, args)
	case "apply-enable":
		return readingApplyEnable(ctx, root, args)
	case "check-views":
		ref := args["checkpoint"]
		if ref == "" {
			return nil, s.Unavailable("ARGUMENT", "check-views 需要 --checkpoint")
		}
		report, err := reading.CheckViews(s, ref, args["required"] == "true")
		if final := s.Finish(); final != nil {
			return nil, final
		}
		return report, err
	case "affected":
		ref, kind := args["asset"], args["kind"]
		if ref == "" || kind == "" {
			return nil, s.Unavailable("ARGUMENT", "affected 需要 --asset 与 --kind")
		}
		checkpoints, err := reading.AffectedCheckpoints(s, ref, kind)
		if err != nil {
			return nil, err
		}
		if final := s.Finish(); final != nil {
			return nil, final
		}
		return map[string]any{"asset_ref": ref, "kind": kind, "checkpoints": checkpoints, "read_only": true}, nil
	}
	return nil, s.Unavailable("ARGUMENT", "未知 reading 动作: "+action)
}

const readingRenderKind = "reading-render"

// readingRender 渲染一个或全部 managed checkpoint。每个 checkpoint 一个事务；
// 提交后用新会话重新核验，不一致则整个事务回滚。
func readingRender(ctx context.Context, root string, args map[string]string) (any, error) {
	dry := args["dry-run"] == "true"
	if args["all"] == "true" {
		if args["checkpoint"] != "" {
			return nil, domain.Fail("ARGUMENT", "--all 与 --checkpoint 互斥")
		}
		s := kernel.NewSemanticSession(ctx, root, args)
		if err := readingSessionReady(s); err != nil {
			return nil, err
		}
		refs, err := reading.ManagedCheckpoints(s)
		if err != nil {
			return nil, err
		}
		if len(refs) == 0 {
			return nil, domain.Fail("ARGUMENT", "项目未启用 managed 阅读视图，没有可渲染的 checkpoint")
		}
		sort.Strings(refs)
		results := []any{}
		for _, ref := range refs {
			result, err := readingRenderOne(ctx, root, args, ref, dry)
			if err != nil {
				return nil, allFailure(ref, len(results), err)
			}
			results = append(results, result)
		}
		return map[string]any{"status": "rendered", "dry_run": dry, "results": results}, nil
	}
	if args["checkpoint"] == "" {
		return nil, domain.Fail("ARGUMENT", "render 需要 --checkpoint 或 --all")
	}
	return readingRenderOne(ctx, root, args, args["checkpoint"], dry)
}

// allFailure 保留原错误码与退出码，只在消息里补充失败的 checkpoint 及已提交数量。
func allFailure(ref string, done int, err error) error {
	prefix := fmt.Sprintf("渲染 %s 失败（此前 %d 个已提交）: ", ref, done)
	var de *domain.Error
	if errors.As(err, &de) {
		return &domain.Error{Code: de.Code, Message: prefix + de.Message, Exit: de.Exit}
	}
	return domain.Fail("INTERNAL", prefix+err.Error())
}

func readingSessionReady(s *kernel.SemanticSession) error {
	if err := s.Guard(); err != nil {
		return err
	}
	if err := s.Authorities(); err != nil {
		return err
	}
	_, err := s.ContextContract()
	return err
}

func readingRenderOne(ctx context.Context, root string, args map[string]string, ref string, dry bool) (map[string]any, error) {
	status, err := transaction.Status(root)
	if err != nil {
		return nil, err
	}
	if len(status.Pending) > 0 {
		return nil, domain.Fail("INTERRUPTED", "存在未完成 Go 事务，先 recover")
	}
	s := kernel.NewSemanticSession(ctx, root, args)
	if err := readingSessionReady(s); err != nil {
		return nil, err
	}
	plan, err := reading.PlanReading(s, ref)
	if err != nil {
		return nil, err
	}
	if err := s.Finish(); err != nil {
		return nil, err
	}
	changed, changes := []string{}, []any{}
	for _, c := range plan.Changes {
		changed = append(changed, c.Ref)
		var before any
		if c.Exists {
			before = "sha256:" + safefs.Digest(c.Before)
		}
		changes = append(changes, map[string]any{"ref": c.Ref, "before_digest": before, "after_digest": "sha256:" + safefs.Digest(c.After)})
	}
	result := map[string]any{"checkpoint_ref": ref, "changed_refs": changed, "diagnostics": []any{}, "approval_created": false}
	if len(plan.Changes) == 0 {
		result["status"] = "unchanged"
		return result, nil
	}
	result["status"], result["changes"] = "rendered", changes
	if dry {
		result["dry_run"] = true
		return result, nil
	}
	tx, err := readingCommit(ctx, root, args, s, plan, ref, readingRenderKind)
	if err != nil {
		return nil, err
	}
	result["transaction"] = tx
	return result, nil
}

// readingCommit 把变更集作为一个事务提交；事务内用新会话对磁盘上的最终状态做完整核验，
// 不一致则整体回滚。输入被其他进程改动时由守卫拒绝。
func readingCommit(ctx context.Context, root string, args map[string]string, s *kernel.SemanticSession, plan *reading.RenderPlan, ref, kind string) (transaction.Result, error) {
	ops, err := readingOperations(s, plan)
	if err != nil {
		return transaction.Result{}, err
	}
	guards := map[string]domain.Descriptor{}
	written := map[string]bool{}
	for _, op := range ops {
		written[op.Path] = true
	}
	for path, d := range s.V.Observed {
		if path == ".yss/transactions" || strings.HasPrefix(path, ".yss/transactions/") || written[path] {
			continue
		}
		guards[path] = d
	}
	validate := func() error {
		fresh := kernel.NewSemanticSession(ctx, root, args)
		fresh.InsideTransaction = true
		if err := readingSessionReady(fresh); err != nil {
			return err
		}
		if err := reading.VerifyCurrent(fresh, ref); err != nil {
			return err
		}
		return fresh.Finish()
	}
	return transaction.ApplyContextWithValidation(ctx, root, kind, ops, guards, nil, validate)
}

// readingOperations 把变更集转成事务操作。新建的父目录须显式登记，由浅到深。
func readingOperations(s *kernel.SemanticSession, plan *reading.RenderPlan) ([]transaction.Operation, error) {
	dirs, seen := []string{}, map[string]bool{}
	for _, c := range plan.Changes {
		missing := []string{}
		for parent := path.Dir(c.Ref); parent != "." && parent != "/" && !seen[parent]; parent = path.Dir(parent) {
			p, err := safefs.Path(s.Root, parent)
			if err != nil {
				return nil, err
			}
			st, err := os.Lstat(p)
			if err == nil {
				if !st.IsDir() {
					return nil, domain.Fail("PATH", "父路径不是目录: "+parent)
				}
				break
			}
			if !os.IsNotExist(err) {
				return nil, err
			}
			missing = append(missing, parent)
		}
		for i := len(missing) - 1; i >= 0; i-- {
			seen[missing[i]] = true
			dirs = append(dirs, missing[i])
		}
	}
	ops := []transaction.Operation{}
	for _, dir := range dirs {
		ops = append(ops, transaction.Operation{Path: dir, Directory: true})
	}
	for _, c := range plan.Changes {
		d, err := s.V.Watch(c.Ref)
		if err != nil {
			return nil, err
		}
		op := transaction.Operation{Path: c.Ref, Data: c.After, Mode: 0644}
		if d.Type == "file" {
			before := d
			op.Before, op.Mode = &before, d.Mode
		}
		ops = append(ops, op)
	}
	return ops, nil
}

var readingTransactionPath = regexp.MustCompile(`^(?:(?:.+/)?(?:reading/(?:(?:domain-strategy|stage-decision-package|status)\.review\.md|\.manifest\.json)|map\.md)|\.template-spec/process/reading-policy\.yaml)$`)

// ValidateReadingTransaction 约束恢复/回退范围：reading-render 事务只允许触碰阅读目录、
// 其清单、同级 map.md 与阅读策略文件（及为它们新建的目录）。
func ValidateReadingTransaction(root, profile string, paths []string) error {
	for _, p := range paths {
		if readingTransactionPath.MatchString(p) || strings.HasSuffix(p, "/reading") || path.Base(p) == "reading" {
			continue
		}
		return domain.Fail("TRANSACTION_SCOPE", "阅读渲染事务越出受管范围: "+p)
	}
	return nil
}

// stageReadingUpdate 在阶段写事务提交之后更新 managed 阅读视图，语义同 Node finalizeReading：
// 策略未启用该 checkpoint → manual；渲染失败不回滚已提交的源，只在结果里报告失败和恢复命令。
func stageReadingUpdate(ctx context.Context, root, ref string) map[string]any {
	failed := func(err error) map[string]any {
		return map[string]any{"status": "failed", "error": err.Error(), "recovery": "yss reading render --checkpoint " + ref}
	}
	s := kernel.NewSemanticSession(ctx, root, map[string]string{})
	policy, present, err := reading.LoadPolicy(s)
	if err != nil {
		return failed(err)
	}
	if !present || policy["mode"] != "managed" || !kernel.SemHas(policy["checkpoints"], ref) {
		return map[string]any{"status": "manual"}
	}
	result, err := readingRenderOne(ctx, root, map[string]string{}, ref, false)
	if err != nil {
		return failed(err)
	}
	return result
}
