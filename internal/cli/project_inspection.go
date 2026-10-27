package cli

import (
	"context"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/project"
)

func projectInspectionCommand(ctx context.Context, o options) (any, string, error) {
	if len(o.args) != 2 {
		return nil, "", argumentError("请选择 project inspect 或 project verify")
	}
	switch o.args[1] {
	case "inspect":
		if err := onlyOptions(o, "json root"); err != nil {
			return nil, "", err
		}
		root, err := projectRoot(o)
		if err != nil {
			return nil, "", err
		}
		r, err := project.Inspect(ctx, root)
		return r, "", err
	case "verify":
		if err := onlyOptions(o, "json plan-file"); err != nil {
			return nil, "", err
		}
		if o.values["plan-file"] == "" {
			return nil, "", domain.Fail("PLAN_REQUIRED", "核查须提供 --plan-file")
		}
		p, err := project.ReadPlan(o.values["plan-file"])
		if err != nil {
			return nil, "", err
		}
		r, err := project.VerifyPlanOutput(ctx, p)
		return r, p.Profile, err
	}
	return nil, "", argumentError("请选择 project inspect 或 project verify")
}
