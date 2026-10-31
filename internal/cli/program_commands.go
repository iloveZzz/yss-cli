package cli

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/updater"
	"github.com/spf13/cobra"
	"path/filepath"
	"strings"
)

func addProgramCommands(root *cobra.Command, inv *invocation) {
	addFlags(addCommand(root, inv, "upgrade", programCommand), inv, "check to tool-root")
	update := addCommand(root, inv, "update", programCommand)
	addFlags(update, inv, "tool-root artifact sha256 plan apply out plan-file")
	for _, name := range []string{"plan", "apply", "status"} {
		cmd := addCommand(update, inv, name, programCommand)
		addFlags(cmd, inv, "tool-root")
		if name == "plan" || name == "apply" {
			addFlags(cmd, inv, "artifact sha256 plan apply out plan-file")
		}
	}
}

func programCommand(ctx context.Context, o options) (any, string, error) {
	command := o.args[0]
	if command == "upgrade" {
		if len(o.args) != 1 || len(o.duplicates) > 0 {
			return nil, "", &domain.Error{Code: "ARGUMENT", Message: "upgrade 不接受子命令、位置参数或重复参数", Exit: 2}
		}
		for key, value := range o.values {
			switch key {
			case "json", "help", "check":
			case "to", "tool-root":
				if strings.TrimSpace(value) == "" {
					return nil, "", &domain.Error{Code: "ARGUMENT", Message: "upgrade 参数不能为空: --" + key, Exit: 2}
				}
			default:
				return nil, "", &domain.Error{Code: "ARGUMENT", Message: "upgrade 不支持 --" + key + "；项目模板升级使用 yss sync", Exit: 2}
			}
		}
		result, err := (updater.UpgradeClient{}).Run(ctx, updater.UpgradeRequest{Check: o.values["check"] == "true", To: o.values["to"], ToolRoot: o.values["tool-root"]})
		return result, "", err
	}

	if err := ctx.Err(); err != nil {
		return nil, "", domain.Wrap("CANCELLED", err)
	}
	if command == "update" {
		if len(o.args) > 2 {
			return nil, "", domain.Fail("ARGUMENT", "程序升级子命令参数过多")
		}
		for k := range o.values {
			switch k {
			case "json", "help", "version", "tool-root", "artifact", "sha256", "plan", "apply", "out", "plan-file":
			default:
				return nil, "", domain.Fail("ARGUMENT", "程序升级不接受项目参数: --"+k)
			}
		}
		action := "plan"
		if len(o.args) > 1 {
			action = o.args[1]
		}
		if action == "status" {
			for k := range o.values {
				if k != "json" && k != "tool-root" {
					return nil, "", domain.Fail("ARGUMENT", "程序状态仅接受 --tool-root 与 --json")
				}
			}
			r, e := updater.Status(o.values["tool-root"])
			return r, "", e
		}
		if action != "plan" && action != "apply" {
			return nil, "", domain.Fail("ARGUMENT", "程序升级只支持 plan|apply|status")
		}
		if action == "apply" || o.values["apply"] == "true" {
			if o.values["plan-file"] == "" {
				return nil, "", domain.Fail("PLAN_REQUIRED", "程序安装须 --apply --plan-file <已保存计划>")
			}
			p, e := updater.ReadPlan(o.values["plan-file"])
			if e != nil {
				return nil, "", e
			}
			toolRoot, e := filepath.Abs(o.values["tool-root"])
			if e != nil || o.values["tool-root"] == "" || p.ToolRoot != toolRoot {
				return nil, "", domain.Fail("PLAN", "程序升级计划与显式工具根不匹配")
			}
			r, e := updater.Apply(ctx, p)
			return r, "", e
		}
		if o.values["artifact"] == "" || o.values["sha256"] == "" {
			return nil, "", domain.Fail("ARGUMENT", "离线升级须指定 --artifact 与 --sha256；程序升级不迁移项目")
		}
		p, e := updater.Build(o.values["tool-root"], o.values["artifact"], o.values["sha256"])
		if e != nil {
			return nil, "", e
		}
		if file := o.values["out"]; file != "" {
			if e = updater.SavePlan(p, file); e != nil {
				return nil, "", e
			}
		}
		return p, "", nil
	}

	return nil, "", argumentError("未知程序命令")
}
