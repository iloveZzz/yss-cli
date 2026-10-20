package governance

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/domain"
	_ "github.com/iloveZzz/yss-cli/internal/governance/approval"
	"github.com/iloveZzz/yss-cli/internal/governance/contracts"
	_ "github.com/iloveZzz/yss-cli/internal/governance/evidence"
	"github.com/iloveZzz/yss-cli/internal/governance/kernel"
	"github.com/iloveZzz/yss-cli/internal/governance/lifecycle"
	"github.com/iloveZzz/yss-cli/internal/governance/reading"
	"github.com/iloveZzz/yss-cli/internal/safefs"
	"os"
	"path/filepath"
	"strings"
)

func Run(group, action, root string, args map[string]string) (any, error) {
	return RunContext(context.Background(), group, action, root, args)
}

func RunContext(ctx context.Context, group, action, root string, args map[string]string) (any, error) {
	if ctx == nil {
		return nil, domain.Fail("ARGUMENT", "治理执行取消上下文不可为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.Wrap("CANCELLED", err)
	}
	if root == "" {
		return nil, domain.Fail("ROOT", "治理命令需要显式项目根目录")
	}
	var err error
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if _, err = safefs.Path(root, "yss-project.yaml"); err != nil {
		return nil, err
	}
	st, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, domain.Fail("ROOT", "项目根目录必须是目录")
	}
	if args == nil {
		args = map[string]string{}
	}

	semantic := action == "verify" && (group == "lifecycle" || group == "contract" || group == "evidence" || group == "handoff")
	if !semantic && (args["history"] != "" || args["require-approved"] != "") {
		return nil, domain.Fail("UNPORTED", "历史兼容校验与批准语义尚未迁移；不能由结构校验代替")
	}
	allowed := ArgumentKeys(group, action)
	if err := rejectUnportedArgs(args, allowed...); err != nil {
		if semantic || group == "project-ci" && (action == "check" || action == "verify") && args["scope"] != "native-go" {
			s := kernel.NewSemanticSession(ctx, root, args)
			err = s.Unavailable("ARGUMENT", err.Error())
			s.Diagnostic("arguments", err)
			return s.Report, &semanticFailure{cause: err, report: s.Report}
		}
		return nil, err
	}
	if semantic {
		return semanticRun(ctx, group, action, root, args)
	}
	if group == "reading" {
		return readingRun(ctx, action, root, args)
	}
	if group == "lifecycle" && (action == "route" || action == "verify-daily") {
		return lifecycle.Run(ctx, action, root, args)
	}
	if group == "contract" && action == "view" || args["view"] != "" {
		return reading.Run(ctx, group, action, root, args, reading.Sources{ScanContext: scanContext, ValidateRegistry: validateRegistry, LifecycleState: lifecycleState, Copy: contracts.ContractCopy, Same: contracts.ContractSame, Plan: contracts.ContractPlan})
	}
	if group == "lifecycle" && action == "target" {
		return progressionTargetRun(ctx, root, args)
	}
	if group == "contract" && action == "publish-review" {
		return publishPendingReview(ctx, root, args)
	}
	if group == "context" {
		return contextRun(action, root, args)
	}
	if group == "archive" {
		out, err := archiveRun(ctx, action, root, args)
		if err != nil && ctx.Err() != nil {
			return nil, domain.Wrap("CANCELLED", ctx.Err())
		}
		return out, err
	}
	if group == "stage" && (args["checkpoint"] != "" || args["file"] != "" || strings.Contains(args["arg0"], "/") || action == "register" || action == "update" || action == "plan" || action == "apply" || action == "check") {
		return stageRun(ctx, action, root, args)
	}
	if group == "project-ci" {
		return projectCIRun(ctx, action, root, args)
	}
	if group == "lifecycle" || group == "stage" {
		return lifecycleRun(ctx, group, action, root, args)
	}
	if group == "contract" || group == "evidence" || group == "handoff" {
		return assetCheck(group, action, root, args)
	}
	if group == "runtime" {
		out, err := runtimeRun(ctx, action, root, args)
		if err != nil && ctx.Err() != nil {
			return nil, domain.Wrap("CANCELLED", ctx.Err())
		}
		return out, err
	}
	if group == "xml" {
		return xmlRun(action, root, args)
	}
	return nil, domain.Fail("UNPORTED", "此治理动作尚未完成原生迁移")
}

// ArgumentKeys is shared with native CLI preflight and help. arg0 is an internal
// positional transport key, never a public flag. Authority checks remain in RunContext.
func ArgumentKeys(group, action string) []string {
	allowed := []string{"json", "profile", "root", "target-dir"}
	switch group + "." + action {
	case "context.query":
		allowed = append(allowed, "id", "term-refs", "allowed-context-ids", "arg0", "view")
	case "context.verify", "context.check":
		allowed = append(allowed, "term-refs", "snapshot", "file", "allowed-context-ids", "arg0")
	case "lifecycle.query":
		allowed = append(allowed, "id", "work-unit", "stage", "arg0", "view")
	case "lifecycle.status":
		allowed = append(allowed, "checkpoint", "file", "arg0", "view")
	case "contract.publish-review":
		allowed = append(allowed, "file")
	case "contract.view":
		allowed = append(allowed, "kind", "file", "unit", "view")
	case "lifecycle.target":
		allowed = append(allowed, "checkpoint", "input", "plan", "out", "apply", "plan-file")
	case "lifecycle.route":
		allowed = append(allowed, "task", "implementation-root", "base", "view", "prepare-inputs")
	case "lifecycle.verify-daily":
		allowed = append(allowed, "task", "implementation-root", "base")
	case "lifecycle.verify":
		allowed = append(allowed, "checkpoint", "file", "arg0", "history", "home", "run-dir", "tool-root", "template-checkout")
	case "stage.query", "stage.status", "stage.check":
		allowed = append(allowed, "checkpoint", "file", "arg0", "id", "work-unit", "stage")
	case "stage.register", "stage.update", "stage.plan", "stage.apply":
		allowed = append(allowed, "checkpoint", "file", "arg0", "items", "item", "apply", "plan-file", "refresh")
	case "project-ci.verify", "project-ci.check", "project-ci.install", "project-ci.plan", "project-ci.apply", "project-ci.transition":
		allowed = append(allowed, "scope", "checkpoint", "task", "file", "arg0", "current-work-unit", "next-work-unit", "provider", "branch", "additional-path", "cli-source", "apply", "plan-file", "base", "runtime-store", "recover", "home", "run-dir", "tool-root", "template-checkout")
	case "contract.check", "evidence.check", "handoff.check":
		allowed = append(allowed, "schema", "file", "arg0")
	case "contract.verify", "evidence.verify", "handoff.verify":
		allowed = append(allowed, "schema", "kind", "file", "arg0", "asset-ref", "checkpoint", "task", "gate", "boundary", "consumer", "package", "history", "require-approved", "home", "run-dir", "requirements", "continuation", "from-record", "expected", "binding", "allow-template", "tool-root", "template-checkout", "approval-ref", "task-ref", "unit", "slice", "phase", "mode", "review-ref", "source-context-snapshot")
	case "reading.render":
		allowed = append(allowed, "checkpoint", "all", "dry-run")
	case "reading.plan-enable":
		allowed = append(allowed, "checkpoint", "output")
	case "reading.apply-enable":
		allowed = append(allowed, "arg0")
	case "reading.check-views":
		allowed = append(allowed, "checkpoint", "required")
	case "reading.affected":
		allowed = append(allowed, "asset", "kind")
	case "archive.pack", "archive.unpack":
		allowed = append(allowed, "source", "file", "output", "arg0")
	case "archive.verify":
		allowed = append(allowed, "source", "file", "arg0")
	case "runtime.inspect":
		allowed = append(allowed, "home")
	case "runtime.run", "runtime.events", "runtime.commands", "runtime.pins":
		allowed = append(allowed, "home", "id", "arg0")
	case "runtime.pin", "runtime.unpin":
		allowed = append(allowed, "home", "id", "arg0", "token", "reason")
	case "runtime.begin":
		allowed = append(allowed, "home", "kind", "input", "report-dir")
	case "runtime.event":
		allowed = append(allowed, "home", "id", "token", "type", "value", "arg0")
	case "runtime.complete":
		allowed = append(allowed, "home", "id", "token", "status", "exit-code", "arg0")
	case "xml.inspect", "xml.query":
		allowed = append(allowed, "file", "arg0")
	}
	return allowed
}
