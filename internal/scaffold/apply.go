package scaffold

import (
	"context"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

// ApplyPlan rebuilds the same plan before creating staging output. Write steps
// are not safely interruptible: after publication, callers inspect actual files.
func ApplyPlan(ctx context.Context, p *Plan, expectedDigest string) (*Result, error) {
	if ctx == nil {
		return nil, domain.Fail("ARGUMENT", "取消上下文不可为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, domain.Wrap("CANCELLED", err)
	}
	if err := validatePlan(p); err != nil {
		return nil, err
	}
	if expectedDigest == "" || expectedDigest != p.Digest {
		return nil, domain.Fail("INPUT_DRIFT", "保存计划与用户确认摘要不一致；请重新预演并确认")
	}
	if !p.ReadyToApply || len(p.Blockers) > 0 {
		return nil, domain.Fail("ARGUMENT", "脚手架计划存在阻断，不可应用")
	}
	cli, err := currentCLISource()
	if err != nil {
		return nil, err
	}
	if cli != p.CLI {
		return nil, domain.Fail("INPUT_DRIFT", "计划绑定的 CLI 已改变；请重新规划")
	}
	src, binding, err := planSource()
	if err != nil {
		return nil, err
	}
	if binding != p.Bundle {
		return nil, domain.Fail("INPUT_DRIFT", "计划绑定的 Bundle 已改变；请重新规划")
	}
	o := p.Options
	o.renderContext = &p.RenderContext
	rendered, err := renderProject(ctx, src, &o)
	if err != nil {
		return nil, err
	}
	rebuilt := describePlan(o, p.RenderContext, cli, binding, rendered.tree)
	if rebuilt.Digest != p.Digest {
		return nil, domain.Fail("INPUT_DRIFT", "脚手架计划与固定来源重新生成结果不一致；请重新规划")
	}
	result, err := publishRendered(ctx, &o, rendered)
	if err != nil {
		return nil, err
	}
	inspection, err := VerifyPlanOutput(context.WithoutCancel(ctx), p)
	if err != nil {
		return result, err
	}
	if inspection.Outcome != "complete" {
		return result, domain.Fail("VERIFY", "脚手架已经发布，但输出核查未确认完整；请检查保存计划和实际文件")
	}
	return result, nil
}
