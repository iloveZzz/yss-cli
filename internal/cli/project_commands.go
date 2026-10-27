package cli

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/bundle"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/transaction"
	"github.com/spf13/cobra"
	"sort"
	"strings"
)

func addProjectCommands(root *cobra.Command, inv *invocation) {
	projectFlags := strings.Join(projectOptionNames, " ")
	for _, name := range []string{"init", "attach", "sync", "migrate", "diff", "doctor", "skills", "assets"} {
		handler := projectCommand
		cmd := addCommand(root, inv, name, handler)
		addFlags(cmd, inv, projectFlags)
		if name == "init" || name == "attach" || name == "migrate" {
			addFlags(cmd, inv, "installation")
		}
		if name == "attach" || name == "sync" || name == "migrate" {
			addFlags(cmd, inv, "review-out base-bundle resolution-file")
		}
		if name == "skills" || name == "assets" {
			list := addCommand(cmd, inv, "list", projectCommand)
			addFlags(list, inv, projectFlags)
			if name == "skills" {
				addFlags(list, inv, "details")
			}
			ensure := addCommand(cmd, inv, "ensure", projectCommand)
			addFlags(ensure, inv, projectFlags)
			ensure.Args = cobra.ArbitraryArgs
			if name == "skills" {
				resolve := addCommand(cmd, inv, "resolve", projectCommand)
				addFlags(resolve, inv, "root target-dir profile agent-runtime when")
				resolve.Args = cobra.ArbitraryArgs
			}
		}
	}
	for _, name := range []string{"recover", "rollback"} {
		addFlags(addCommand(root, inv, name, recoveryCommand), inv, "root target-dir profile apply")
	}
	profile := addCommand(root, inv, "profile", profileDispatch)
	for _, cmd := range []*cobra.Command{profile, addCommand(profile, inv, "prepare", profileDispatch)} {
		addFlags(cmd, inv, "root backend-root checkpoint project-name business-domain team-size plan out apply plan-file")
	}
	scaffold := addCommand(root, inv, "scaffold", scaffoldCommand)
	for _, cmd := range []*cobra.Command{scaffold, addCommand(scaffold, inv, "ddd", scaffoldCommand), addCommand(scaffold, inv, "mvc", scaffoldCommand)} {
		addFlags(cmd, inv, scaffoldValues+" "+scaffoldFlags)
	}
	bundle := addCommand(root, inv, "bundle", bundleCommand)
	addFlags(bundle, inv, "profile out")
	addFlags(addCommand(bundle, inv, "inspect", bundleCommand), inv, "profile")
	addFlags(addCommand(bundle, inv, "export", bundleCommand), inv, "profile out")
}

func projectCommand(ctx context.Context, o options) (any, string, error) {
	command := o.args[0]

	profile := o.values["profile"]
	root, err := projectRoot(o)
	if err != nil {
		return nil, profile, err
	}
	if err = ctx.Err(); err != nil {
		return nil, profile, domain.Wrap("CANCELLED", err)
	}

	if o.values["apply"] == "true" && o.values["plan-file"] != "" {
		if o.values["installation"] != "" || o.values["binding-file"] != "" || o.values["full"] != "" || o.values["review-out"] != "" || o.values["base-bundle"] != "" || o.values["resolution-file"] != "" {
			return nil, profile, domain.Fail("ARGUMENT", "apply consumes binding and resource selection from the saved plan")
		}
		p, e := project.ReadPlan(o.values["plan-file"])
		if e != nil {
			return nil, profile, e
		}
		if p.Root != root || (profile != "" && profile != p.Profile) || p.Command != command {
			return nil, profile, domain.Fail("PLAN", "计划与命令、项目根或 Profile 不匹配")
		}
		r, e := project.ApplyContext(ctx, p)
		return installationReceipt(r, p.Installation), p.Profile, e
	}
	detectProfile := profile
	if command == "migrate" {
		detectProfile = ""
	}
	id, e := project.Detect(root, detectProfile, command == "init" || command == "attach")
	if e != nil {
		return nil, profile, e
	}
	if command == "migrate" && o.values["installation"] != "" && profile != "" && profile != id.Profile.Name {
		return nil, profile, domain.Fail("IDENTITY", "迁移目标 Profile 与当前工程不一致")
	}
	if command != "migrate" || profile == "" {
		profile = id.Profile.Name
	}
	var selection []string
	if o.values["full"] == "true" {
		if command != "init" && command != "attach" {
			return nil, profile, domain.Fail("ARGUMENT", "--full only supports init or attach")
		}
		b, e := bundle.Load(profile)
		if e != nil {
			return nil, profile, e
		}
		kind, err := project.ResolveInstallation(id, command, project.InstallationKind(o.values["installation"]))
		if err != nil {
			return nil, profile, err
		}
		for ref := range b.Files {
			if b.SchemaVersion >= 5 && kind == project.InstallationSkills && !strings.HasPrefix(ref, ".agents/skills/") {
				continue
			}
			if profile == "spec" && (strings.HasPrefix(ref, ".cursor/skills/") || strings.HasPrefix(ref, ".pi/skills/") || strings.HasPrefix(ref, ".claude/skills/") || strings.HasPrefix(ref, ".workbuddy/skills/")) {
				continue
			}
			selection = append(selection, ref)
		}
		sort.Strings(selection)
	}
	if command == "skills" || command == "assets" {
		if len(o.args) < 2 {
			return nil, profile, domain.Fail("ARGUMENT", "需要 list 或 ensure 子命令")
		}
		b, e := bundle.Load(profile)
		if e != nil {
			return nil, profile, e
		}
		if o.args[1] == "list" {
			if command == "assets" {
				return b.StageRequirements, profile, nil
			}
			if o.values["details"] == "true" {
				result, e := project.SkillsDetails(id, b)
				return result, profile, e
			}
			names := map[string]bool{}
			for ref := range b.Files {
				if strings.HasPrefix(ref, ".agents/skills/") {
					name := strings.Split(strings.TrimPrefix(ref, ".agents/skills/"), "/")[0]
					names[name] = true
				}
			}
			list := []string{}
			for name := range names {
				list = append(list, name)
			}
			sort.Strings(list)
			return list, profile, nil
		}
		if command == "skills" && o.args[1] == "resolve" {
			result, e := project.ResolveSkills(id, b, o.args[2:], o.values["agent-runtime"], o.values["when"])
			return result, profile, e
		}
		if o.args[1] != "ensure" || len(o.args) < 3 {
			return nil, profile, domain.Fail("ARGUMENT", "需要 ensure <标识>")
		}
		if command == "assets" {
			for _, stage := range o.args[2:] {
				r, ok := b.StageRequirements[stage]
				if !ok {
					return nil, profile, domain.Fail("ASSET", "未知阶段或 Profile 不支持按阶段补装: "+stage)
				}
				selection = append(selection, r.Paths...)
				for _, s := range r.Skills {
					for ref := range b.Files {
						if strings.HasPrefix(ref, ".agents/skills/"+s+"/") || strings.HasPrefix(ref, ".claude/skills/"+s+"/") {
							selection = append(selection, ref)
						}
					}
				}
			}
		} else {
			for _, name := range o.args[2:] {
				if r, ok := b.SkillRequirements[name]; ok {
					if r.UnsupportedReason != "" {
						return nil, profile, domain.Fail("UNPORTED", r.UnsupportedReason)
					}
					selection = append(selection, r.Paths...)
					for _, dep := range r.Skills {
						for ref := range b.Files {
							if strings.HasPrefix(ref, ".agents/skills/"+dep+"/") || strings.HasPrefix(ref, ".claude/skills/"+dep+"/") {
								selection = append(selection, ref)
							}
						}
					}
				}
				prefix := ".agents/skills/" + name + "/"
				count := 0
				for ref := range b.Files {
					if strings.HasPrefix(ref, prefix) || strings.HasPrefix(ref, ".claude/skills/"+name+"/") {
						selection = append(selection, ref)
						count++
					}
				}
				if count == 0 {
					return nil, profile, domain.Fail("SKILL", "快照未登记 Skill: "+name)
				}
			}
		}
	}
	vars := map[string]string{"projectName": o.values["project-name"], "businessDomain": o.values["business-domain"], "teamSize": o.values["team-size"]}
	if tracker, ok := o.values["issue-tracker"]; ok {
		if command != "init" && command != "attach" {
			return nil, profile, domain.Fail("ARGUMENT", "tracker更改需要单独迁移，不支持此命令参数")
		}
		switch tracker {
		case "local-markdown", "github", "gitlab":
			vars["issueTracker"] = tracker
		default:
			return nil, profile, domain.Fail("ARGUMENT", "未知issue-tracker")
		}
	}
	var binding *project.Binding
	if file := o.values["binding-file"]; file != "" {
		binding, e = project.ReadBinding(file)
		if e != nil {
			return nil, profile, e
		}
	}
	planning := project.PlanningOptions{Installation: project.InstallationKind(o.values["installation"]), BaseBundlePath: o.values["base-bundle"], ResolutionFile: o.values["resolution-file"]}
	if o.values["plan-file"] != "" {
		if o.values["resolution-file"] == "" || o.values["plan"] != "true" {
			return nil, profile, domain.Fail("ARGUMENT", "重新规划须同时提供 --plan --plan-file 和 --resolution-file")
		}
		for _, key := range []string{"project-name", "business-domain", "team-size", "issue-tracker", "installation", "full", "binding-file"} {
			if _, supplied := o.values[key]; supplied {
				return nil, profile, domain.Fail("ARGUMENT", "决议重规划使用原保存计划，不能同时改写参数: "+key)
			}
		}
		original, e := project.ReadPlan(o.values["plan-file"])
		if e != nil {
			return nil, profile, e
		}
		planning.Original = original
		if original.InstallationMigration != nil {
			planning.Installation = original.Installation
		}
		vars = original.Variables
		selection = original.Selection
		binding = original.Binding
		if planning.BaseBundlePath == "" {
			planning.BaseBundlePath = original.BaseBundlePath
		}
	}
	p, e := project.BuildWithOptions(root, profile, command, vars, selection, binding, planning)
	if e != nil {
		return nil, profile, e
	}
	var readiness map[string]any
	if command == "doctor" {
		b, err := bundle.Load(profile)
		if err != nil {
			return nil, profile, err
		}
		skills, err := project.CheckInstalledSkills(id, b)
		if err != nil {
			return nil, profile, err
		}
		files := "aligned"
		if len(p.Conflicts) != 0 || len(p.Blockers) != 0 {
			files = "blocked"
		} else if len(p.Changes) != 0 {
			files = "changes-required"
		}
		readiness = map[string]any{"files": files, "skills": skills, "workflow": "not-evaluated"}
	}
	if command == "doctor" && p.Installation != project.InstallationSkills {
		checks, e := governance.Run("context", "verify", root, nil)
		if e != nil {
			return nil, profile, e
		}
		identity := map[string]any{"root": id.Root, "profile": id.Profile, "repositoryMode": id.Mode}
		if id.Native != nil {
			identity["native"] = map[string]any{"schemaVersion": id.Native.SchemaVersion, "protocolVersion": id.Native.ProtocolVersion, "cliVersion": id.Native.CLIVersion, "templateVersion": id.Native.TemplateVersion, "legacyCliVersion": id.Native.LegacyCLIVersion, "templateCommit": id.Native.TemplateCommit, "snapshotHash": id.Native.SnapshotHash, "manifestHash": id.Native.ManifestHash, "templateSourceState": id.Native.TemplateSourceState, "baselineDigest": id.Native.BaselineDigest, "managedFileCount": len(id.Native.Managed)}
		}
		return map[string]any{"identity": identity, "context": checks, "plan": p.Public(), "executionCore": "go-native", "stableReady": false, "readiness": readiness}, profile, nil
	}
	if path := o.values["out"]; path != "" {
		if e = project.SavePlan(p, path); e != nil {
			return nil, profile, e
		}
	}
	if out := o.values["review-out"]; out != "" {
		if e = project.ExportReview(p, out); e != nil {
			return nil, profile, e
		}
	}
	if command == "init" && o.values["plan"] != "true" {
		r, e := project.ApplyContext(ctx, p)
		return installationReceipt(r, p.Installation), profile, e
	}
	if o.values["apply"] == "true" {
		return nil, profile, domain.Fail("PLAN_REQUIRED", "非初始化写入需要 --apply --plan-file <已保存的计划>")
	}
	if readiness != nil {
		result := resultObject(p.Public())
		result["readiness"] = readiness
		return result, profile, nil
	}
	return p.Public(), profile, nil

}

func installationReceipt(result transaction.Result, installation project.InstallationKind) map[string]any {
	out := resultObject(result)
	out["installation"] = installation
	return out
}

func profileDispatch(ctx context.Context, o options) (any, string, error) {
	root, err := projectRoot(o)
	if err != nil {
		return nil, o.values["profile"], err
	}
	return profileCommand(ctx, root, o)
}
