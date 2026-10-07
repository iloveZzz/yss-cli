package cli

import (
	"context"
	"sort"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/project"
)

func profileCommand(ctx context.Context, root string, o options) (any, string, error) {
	if len(o.args) != 2 || o.args[1] != "prepare" {
		return nil, "", domain.Fail("ARGUMENT", "profile 需要 prepare 子命令")
	}
	if o.values["apply"] == "true" {
		if o.values["plan"] == "true" || o.values["plan-file"] == "" {
			return nil, "", domain.Fail("PLAN_REQUIRED", "联合准备应用需要 --apply --plan-file <保存计划>")
		}
		for _, key := range []string{"design-root", "backend-root", "frontend-root", "checkpoint", "project-name", "business-domain", "team-size", "out"} {
			if o.values[key] != "" {
				return nil, "", domain.Fail("ARGUMENT", "应用使用保存计划，不接受 --"+key)
			}
		}
		p, err := project.ReadProfilesPlan(o.values["plan-file"])
		if err != nil {
			return nil, "", err
		}
		if p.SourceRoot != root {
			return nil, p.SourceProfile, domain.Fail("PLAN", "联合准备计划与源工程根不匹配")
		}
		targets := []string{}
		for _, target := range p.Targets {
			targets = append(targets, target.Profile)
		}
		validator := func(ctx context.Context, sourceRoot string) error {
			_, err := governance.ProfilePreparationSource(ctx, sourceRoot, p.SourceCheckpoint, targets)
			return err
		}
		result, err := project.ApplyProfilesWithOptions(ctx, p, project.ProfileApplyOptions{ValidateSource: validator, PlanFile: o.values["plan-file"]})
		return result, p.SourceProfile, err
	}
	if o.values["plan"] != "true" || o.values["plan-file"] != "" {
		return nil, "", domain.Fail("PLAN_REQUIRED", "profile prepare 需要 --plan；应用使用 --apply --plan-file")
	}
	targets := project.ProfileTargets{}
	names := []string{}
	for _, name := range []string{"design", "backend", "frontend"} {
		if value := o.values[name+"-root"]; value != "" {
			targets[name] = value
			names = append(names, name)
		}
	}
	sort.Strings(names)
	refs, err := governance.ProfilePreparationSource(ctx, root, o.values["checkpoint"], names)
	if err != nil {
		return nil, "", err
	}
	validator := func(ctx context.Context, sourceRoot string) error {
		_, err := governance.ProfilePreparationSource(ctx, sourceRoot, o.values["checkpoint"], names)
		return err
	}
	vars := map[string]string{"projectName": o.values["project-name"], "businessDomain": o.values["business-domain"], "teamSize": o.values["team-size"]}
	p, err := project.PrepareProfiles(ctx, root, targets, vars, o.values["checkpoint"], refs, validator)
	if err != nil {
		return nil, "", err
	}
	if file := o.values["out"]; file != "" {
		if err = project.SaveProfilesPlan(p, file); err != nil {
			return nil, p.SourceProfile, err
		}
	}
	return p.Public(), p.SourceProfile, nil
}
