// Package cli exposes only installation and generation operations.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/project"
	"github.com/iloveZzz/yss-cli/internal/scaffold"
	"github.com/iloveZzz/yss-cli/internal/updater"
	"github.com/spf13/cobra"
)

type invocation struct {
	json             bool
	version          bool
	result           any
	command, profile string
	executed         bool
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	inv := &invocation{}
	// Capture JSON intent even if an earlier argument fails parsing. Stop at --.
	for _, arg := range args {
		if arg == "--" {
			break
		}
		if arg == "--json" || arg == "--json=true" {
			inv.json = true
		}
		if arg == "--json=false" {
			inv.json = false
		}
	}
	jsonIntent := inv.json
	root := newRoot(inv, stdout, stderr)
	inv.json = jsonIntent
	root.SetArgs(args)
	command, err := root.ExecuteContextC(ctx)
	if err != nil && !inv.executed {
		err = argument(err)
	}
	if err == nil && !inv.executed {
		return 0
	}
	status, code, exit := "ok", "OK", 0
	if err != nil {
		status, code, exit = "error", "ERROR", 1
		var d *domain.Error
		if errors.As(err, &d) {
			code = d.Code
			if d.Exit != 0 {
				exit = d.Exit
			}
		}
		if code == "ARGUMENT" {
			exit = 2
		}
		if errors.Is(err, context.Canceled) {
			code = "CANCELLED"
		}
	}
	name := inv.command
	if name == "" && command != nil {
		name = strings.TrimPrefix(command.CommandPath(), "yss ")
	}
	if inv.json {
		envelope := domain.Envelope{OutputVersion: 1, Version: domain.Version, ProtocolVersion: domain.ProtocolVersion, Command: name, Profile: inv.profile, Status: status, Code: code, Result: inv.result}
		if err != nil {
			envelope.Diagnostic = &domain.Diagnostic{SchemaVersion: 1, Code: code, Summary: err.Error(), Cause: err.Error()}
		}
		if e := json.NewEncoder(stdout).Encode(envelope); e != nil {
			fmt.Fprintln(stderr, e)
			return 1
		}
	} else if err != nil {
		fmt.Fprintln(stderr, err)
	} else {
		b, e := json.MarshalIndent(inv.result, "", "  ")
		if e != nil {
			fmt.Fprintln(stderr, e)
			return 1
		}
		fmt.Fprintln(stdout, string(b))
	}
	return exit
}
func argument(err error) error { return &domain.Error{Code: "ARGUMENT", Message: err.Error(), Exit: 2} }
func newRoot(inv *invocation, stdout, stderr io.Writer) *cobra.Command {
	root := &cobra.Command{Use: "yss", Short: "安装和同步 YSS 技能与项目，生成后端脚手架，升级程序。", SilenceErrors: true, SilenceUsage: true}
	root.SetOut(stdout)
	root.SetErr(stderr)
	root.CompletionOptions.DisableDefaultCmd = true
	root.PersistentFlags().BoolVar(&inv.json, "json", false, "输出 JSON")
	root.Flags().BoolVarP(&inv.version, "version", "V", false, "显示程序版本")
	root.SetFlagErrorFunc(func(c *cobra.Command, e error) error { return argument(e) })
	root.Args = func(c *cobra.Command, args []string) error {
		if len(args) > 0 {
			return argument(fmt.Errorf("未知命令: %s", args[0]))
		}
		return nil
	}
	root.RunE = func(c *cobra.Command, args []string) error {
		if inv.version {
			inv.executed = true
			inv.command = "version"
			inv.result = map[string]any{"version": domain.Version, "protocolVersion": domain.ProtocolVersion, "source": domain.BuildProvenance()}
			return nil
		}
		return c.Help()
	}
	root.SetHelpCommand(&cobra.Command{Use: "help [命令]", Short: "显示命令帮助", RunE: func(c *cobra.Command, args []string) error {
		target, remaining, e := root.Find(args)
		if e != nil {
			return argument(e)
		}
		if len(remaining) > 0 {
			return argument(fmt.Errorf("未知帮助命令: %s", strings.Join(args, " ")))
		}
		return target.Help()
	}})
	add := func(parent *cobra.Command, use, short string, run func(context.Context, []string) (any, string, error)) *cobra.Command {
		cmd := &cobra.Command{Use: use, Short: short, Args: cobra.NoArgs}
		cmd.RunE = func(c *cobra.Command, args []string) error {
			inv.executed = true
			inv.command = strings.TrimPrefix(c.CommandPath(), "yss ")
			var e error
			inv.result, inv.profile, e = run(c.Context(), args)
			return e
		}
		parent.AddCommand(cmd)
		return cmd
	}
	projectFlags := func(cmd *cobra.Command, o *project.Options, write bool) {
		cmd.Flags().StringVar(&o.Root, "root", ".", "项目目录")
		cmd.Flags().StringVar(&o.Profile, "profile", "", "Profile: spec 或 backend")
		if write {
			cmd.Flags().BoolVar(&o.DryRun, "dry-run", false, "仅预览，不写入")
		}
	}
	for _, name := range []string{"init", "attach", "sync"} {
		name := name
		o := &project.Options{Variables: map[string]string{}}
		cmd := add(root, name, map[string]string{"init": "在空目录初始化项目。", "attach": "接入已有目录，保留用户文件。", "sync": "同步受管文件或切换安装范围。"}[name], func(ctx context.Context, _ []string) (any, string, error) {
			r, e := project.Run(ctx, name, *o)
			return r, r.Profile, e
		})
		projectFlags(cmd, o, true)
		if name == "init" || name == "attach" {
			_ = cmd.MarkFlagRequired("root")
			_ = cmd.MarkFlagRequired("profile")
		}
		cmd.Flags().StringVar(&o.Installation, "installation", "", "安装范围: project 或 skills；默认沿用已有范围")
		for _, item := range []struct{ flag, key string }{{"project-name", "projectName"}, {"business-domain", "businessDomain"}, {"team-size", "teamSize"}} {
			value := new(string)
			cmd.Flags().StringVar(value, item.flag, "", "项目文档占位值")
			key := item.key
			prev := cmd.PreRunE
			cmd.PreRunE = func(c *cobra.Command, args []string) error {
				if prev != nil {
					if e := prev(c, args); e != nil {
						return e
					}
				}
				o.Variables[key] = *value
				return nil
			}
		}
	}
	skills := &cobra.Command{Use: "skills", Short: "查询和安装技能。", Args: cobra.NoArgs, RunE: func(c *cobra.Command, args []string) error { return c.Help() }}
	root.AddCommand(skills)
	for _, action := range []string{"list", "ensure"} {
		action := action
		o := &project.Options{}
		cmd := add(skills, action, map[string]string{"list": "列出当前 Profile 可用与已安装技能。", "ensure": "安装指定技能及其必要资源。"}[action], func(ctx context.Context, args []string) (any, string, error) {
			if action == "list" {
				r, e := project.Skills(*o)
				return r, o.Profile, e
			}
			o.Skills = args
			r, e := project.Run(ctx, "skills", *o)
			return r, r.Profile, e
		})
		projectFlags(cmd, o, action == "ensure")
		if action == "ensure" {
			cmd.Use = "ensure <技能...>"
			cmd.Args = cobra.MinimumNArgs(1)
		}
	}
	doctorOptions := &project.Options{}
	doctor := add(root, "doctor", "查看安装来源、文件差异与未完成事务。", func(ctx context.Context, _ []string) (any, string, error) {
		r, e := project.Doctor(ctx, *doctorOptions)
		return r, doctorOptions.Profile, e
	})
	projectFlags(doctor, doctorOptions, false)
	var recoveryRoot string
	var recoveryDry bool
	recoverCmd := add(root, "recover", "恢复未完成文件事务；不覆盖后续用户修改。", func(ctx context.Context, _ []string) (any, string, error) {
		r, e := project.Recover(ctx, recoveryRoot, recoveryDry)
		return r, "", e
	})
	recoverCmd.Flags().StringVar(&recoveryRoot, "root", ".", "项目或工具目录")
	recoverCmd.Flags().BoolVar(&recoveryDry, "dry-run", false, "仅查看事务状态")
	scaffoldGroup := &cobra.Command{Use: "scaffold", Short: "生成 DDD 或 MVC 后端工程。", Args: cobra.NoArgs, RunE: func(c *cobra.Command, args []string) error { return c.Help() }}
	root.AddCommand(scaffoldGroup)
	add(scaffoldGroup, "catalog", "列出支持的平台和必需输入。", func(ctx context.Context, _ []string) (any, string, error) {
		r, e := scaffold.DescribeCatalog(ctx)
		return r, "", e
	})
	for _, architecture := range []string{"ddd", "mvc"} {
		noExample := false
		o := &scaffold.Options{Architecture: architecture, WithExample: &noExample}
		cmd := add(scaffoldGroup, architecture, "生成业务空骨架，不自动执行 Maven。", func(ctx context.Context, _ []string) (any, string, error) {
			r, e := scaffold.Run(ctx, *o)
			return r, "", e
		})
		for _, f := range []struct {
			name string
			dest *string
		}{{"project-name", &o.ProjectName}, {"base-package", &o.BasePackage}, {"output-dir", &o.OutputDir}, {"platform-profile", &o.PlatformProfile}, {"spring-boot-version", &o.SpringBootVersion}, {"java-version", &o.JavaVersion}, {"group-id", &o.GroupID}, {"project-version", &o.ProjectVersion}, {"parent-group-id", &o.ParentGroupID}, {"parent-artifact-id", &o.ParentArtifactID}, {"parent-version", &o.ParentVersion}, {"yss-components-version", &o.YssComponentsVersion}} {
			cmd.Flags().StringVar(f.dest, f.name, "", "工程输入；使用实际平台与 Maven 坐标")
			_ = cmd.MarkFlagRequired(f.name)
		}
		cmd.Flags().BoolVar(&o.DryRun, "dry-run", false, "仅预览生成文件，不写入")
	}
	upgradeOptions := &updater.UpgradeRequest{}
	upgrade := add(root, "upgrade", "在线升级或安装指定本地发行包。", func(ctx context.Context, _ []string) (any, string, error) {
		r, e := (updater.UpgradeClient{}).Run(ctx, *upgradeOptions)
		return r, "", e
	})
	upgrade.Flags().StringVar(&upgradeOptions.To, "to", "", "目标版本")
	upgrade.Flags().StringVar(&upgradeOptions.ToolRoot, "tool-root", "", "程序安装目录")
	upgrade.Flags().StringVar(&upgradeOptions.Artifact, "artifact", "", "本地发行归档")
	upgrade.Flags().StringVar(&upgradeOptions.SHA256, "sha256", "", "本地归档的 SHA-256")
	upgrade.Flags().BoolVar(&upgradeOptions.DryRun, "dry-run", false, "只读预览，不下载或写入")
	add(root, "version", "显示程序版本。", func(context.Context, []string) (any, string, error) {
		return map[string]any{"version": domain.Version, "protocolVersion": domain.ProtocolVersion, "source": domain.BuildProvenance()}, "", nil
	})
	return root
}
