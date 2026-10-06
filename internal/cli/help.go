package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
)

type helpTopic struct {
	summary, usage, options, examples, notes string
	positional                               bool
}

var helpTopics = map[string]helpTopic{
	"init": {
		summary:  "创建固定模板来源的项目实例。",
		usage:    "--profile <spec|design|backend|frontend> --root <目录> [--project-name <名称>]",
		options:  "--profile       必需，选择四类模板之一\n--root          项目目录；默认当前目录，推荐显式指定新目录\n--project-name  项目名称\n--business-domain / --team-size  模板变量\n--full          安装该 Profile 的完整资源，默认初始分发集合\n--plan --out <新文件>  只生成计划；默认直接初始化\n--apply --plan-file <文件>  应用已保存计划\n--issue-tracker <local-markdown|github|gitlab>  Spec Tracker 配置",
		examples: "yss init --profile spec --root ./demo-spec --project-name 演示项目\nyss init --profile design --root ./demo-design --plan --out /tmp/yss-init-plan.json",
		notes:    "拒绝覆盖用户文件；初始化使用程序内置 Bundle，无需 Node/Python 或网络。已有工程请先查看 yss attach --help。",
	},
}

func renderHelp(args []string) (string, error) {
	if len(args) > 0 && args[0] == "help" {
		args = args[1:]
	}
	if len(args) == 0 {
		return rootHelp(), nil
	}
	if len(args) == 1 && args[0] == "tutorial" {
		return tutorial, nil
	}
	key := strings.Join(args, " ")
	topic, ok := helpTopics[key]
	if !ok && len(args) > 2 {
		key = strings.Join(args[:2], " ")
		topic, ok = helpTopics[key]
		ok = ok && topic.positional
	}
	if !ok {
		return "", &domain.Error{Code: "ARGUMENT", Message: "未知帮助命令或子命令: " + key + "；运行 yss --help 查看可用命令", Exit: 2}
	}
	children := []string{}
	for path, child := range helpTopics {
		if strings.HasPrefix(path, key+" ") {
			children = append(children, strings.TrimPrefix(path, key+" ")+"  "+child.summary)
		}
	}
	sort.Strings(children)
	subcommands := ""
	if len(children) > 0 {
		subcommands = "\n\n子命令:\n" + strings.Join(children, "\n")
	}
	spec := commands[key]
	return fmt.Sprintf("yss %s — %s\n──────────────────────\n%s\n\n用法: yss %s %s%s\n\n参数:\n%s\n\n参数说明与条件:\n%s\n\n示例:\n%s\n\n%s", domain.Version, key, topic.summary, key, topic.usage, subcommands, renderOptions(spec), topic.options, topic.examples, topic.notes), nil
}

func rootHelp() string {
	var out strings.Builder
	fmt.Fprintf(&out, "yss %s — Spec / Design / Backend / Frontend 统一入口\n──────────────────────\n\n用法: yss [选项] <命令> [参数]\n\n通用选项:\n  -h, --help       显示帮助\n  -V, --version    显示 CLI 版本及来源\n  --json           执行结果输出 JSON；帮助始终输出文本\n\n命令:\n", domain.Version)
	for _, group := range []struct {
		label string
		names []string
	}{
		{"项目", []string{"init", "attach", "doctor", "diff", "sync", "migrate", "recover", "rollback"}},
		{"资源", []string{"skills", "assets", "bundle"}},
		{"程序", []string{"version", "capabilities", "upgrade", "update"}},
		{"治理", []string{"context", "lifecycle", "stage", "contract", "evidence", "handoff", "project-ci"}},
		{"工具", []string{"runtime", "archive", "xml", "compat", "compat-api"}},
	} {
		fmt.Fprintf(&out, "  [%s]\n", group.label)
		for _, name := range group.names {
			fmt.Fprintf(&out, "    %-16s %s\n", name, commands[name].help.summary)
		}
	}
	out.WriteString("  help [命令/子命令]  显示对应帮助；help tutorial 查看完整离线教程\n\n项目常用参数（程序升级不接受这些参数）:\n  --root <目录>    项目根，默认当前目录\n  --profile <Profile>  spec|design|backend|frontend；init 必需，其余从身份检测\n\n快速上手（选择一个 Profile，在新目录运行）:\n")
	for _, profile := range []string{"spec", "design", "backend", "frontend"} {
		fmt.Fprintf(&out, "  yss init --profile %s --root ./demo-%s --project-name 演示项目\n", profile, profile)
	}
	out.WriteString(`  yss doctor --root ./demo-spec --json
  yss diff --root ./demo-spec --json

升级案例:
  yss upgrade --check                         查询 CLI 稳定版本
  yss upgrade                                 升级 CLI 程序
  yss update status --tool-root ./tools/yss    诊断离线安装与事务
  yss sync --root ./demo-spec --plan --out /tmp/yss-sync-plan.json
  yss sync --root ./demo-spec --apply --plan-file /tmp/yss-sync-plan.json

帮助: yss -h | yss <命令/子命令> --help | yss help <命令/子命令>
完整离线教程: yss help tutorial
能力与限制: yss capabilities --json；未迁移行为返回 UNPORTED。
帮助不读取项目、不访问网络、不创建资产。
`)
	return out.String()
}
