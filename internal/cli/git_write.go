package cli

import (
	"context"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/gitops"
)

// gitPlanParams names, per plan kind, the flags that become plan parameters.
var gitPlanParams = map[string][]string{
	gitops.KindBranchStart: {"task", "type", "slug", "from"},
	gitops.KindCommit:      {"task", "message", "paths"},
	gitops.KindTag:         {"tag-version", "evidence-file"},
	gitops.KindHooks:       {},
	gitops.KindPolicySet:   {"key", "value"},
	gitops.KindPush:        {"remote", "tag"},
}

var gitRequiredParams = map[string][]string{
	gitops.KindBranchStart: {"task", "slug"},
	gitops.KindCommit:      {"task", "message", "paths"},
	gitops.KindTag:         {"tag-version"},
	gitops.KindPolicySet:   {"key", "value"},
	gitops.KindPush:        {"remote"},
}

// gitWrite runs the plan/apply protocol shared by every write: --plan builds
// and optionally saves a plan, --apply re-derives it from the saved file and
// proceeds only if the repository still matches what was reviewed.
func gitWrite(ctx context.Context, r *gitops.Runner, kind string, o options) (any, error) {
	plan, apply := o.values["plan"] == "true", o.values["apply"] == "true"
	if o.values["confirm-remote"] != "" && !apply {
		return nil, domain.Fail("ARGUMENT", "--confirm-remote 只用于 --apply")
	}
	if plan == apply {
		return nil, domain.Fail("PLAN_REQUIRED", "写入命令需要 --plan [--out <新文件>]，或 --apply --plan-file <保存计划>")
	}
	if apply {
		if o.values["plan-file"] == "" {
			return nil, domain.Fail("PLAN_REQUIRED", "应用需要 --plan-file <保存计划>")
		}
		for _, key := range []string{"out", "task", "message", "paths", "type", "slug", "from", "tag-version", "evidence-file", "key", "value", "remote", "tag"} {
			if o.values[key] != "" {
				return nil, domain.Fail("ARGUMENT", "应用使用保存计划，不接受 --"+key)
			}
		}
		saved, err := gitops.ReadPlan(o.values["plan-file"])
		if err != nil {
			return nil, err
		}
		if saved.Kind != kind {
			return nil, domain.Fail("PLAN", "计划类型 "+saved.Kind+" 与本命令不符")
		}
		// A push leaves the machine: it also needs the user to name the remote.
		if kind == gitops.KindPush && o.values["confirm-remote"] != saved.Params["remote"] {
			return nil, domain.Fail("CAPABILITY", "推送需要用户确认：--confirm-remote "+saved.Params["remote"]+"；未触达远端")
		}
		return r.Apply(ctx, saved)
	}
	if o.values["plan-file"] != "" {
		return nil, domain.Fail("ARGUMENT", "--plan 不接受 --plan-file；应用使用 --apply --plan-file")
	}
	params := map[string]string{}
	for _, key := range gitPlanParams[kind] {
		if v := o.values[key]; v != "" {
			params[key] = v
		}
	}
	for _, key := range gitRequiredParams[kind] {
		if params[key] == "" {
			return nil, domain.Fail("ARGUMENT", "需要 --"+key)
		}
	}
	if kind == gitops.KindBranchStart && params["type"] == "" {
		params["type"] = "feat"
	}
	p, err := r.Build(ctx, kind, params)
	if err != nil {
		return nil, err
	}
	if out := o.values["out"]; out != "" {
		if err = gitops.SavePlan(r.Root, out, p); err != nil {
			return nil, err
		}
	}
	return p.Public(), nil
}
