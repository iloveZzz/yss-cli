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
	return fmt.Sprintf("yss %s — %s\n%s\n\n用法: yss %s %s%s\n\n参数:\n%s\n-h, --help      显示本帮助；不访问项目或网络\n--json          执行结果使用 JSON；帮助始终为文本\n\n示例:\n%s\n\n%s", domain.Version, key, topic.summary, key, topic.usage, subcommands, topic.options, topic.examples, topic.notes), nil
}

func rootHelp() string {
	return "yss " + domain.Version + ` — Spec / Design / Backend / Frontend 统一入口
用法: yss <命令> [参数]
帮助: yss -h | yss <命令/子命令> --help | yss help <命令/子命令>

项目: init attach doctor diff sync migrate recover rollback
资源: skills assets bundle
程序: version capabilities upgrade update
治理: context lifecycle stage contract evidence handoff project-ci
工具: runtime archive xml compat compat-api

快速上手（选择一个 Profile，在新目录运行）:
yss init --profile spec --root ./demo-spec --project-name 演示项目
yss init --profile design --root ./demo-design --project-name 演示设计
yss init --profile backend --root ./demo-backend --project-name 演示后端
yss init --profile frontend --root ./demo-frontend --project-name 演示前端
yss doctor --root ./demo-spec --json
yss diff --root ./demo-spec --json

模板升级: yss sync --root ./demo-spec --plan --out /tmp/yss-sync-plan.json
          yss sync --root ./demo-spec --apply --plan-file /tmp/yss-sync-plan.json
CLI 升级: yss upgrade --check；yss upgrade

常用参数: --root（默认当前目录）、--profile、--json
帮助仅输出文本；不读取项目、访问网络或创建资产。
完整离线教程: yss help tutorial
具体示例: yss init -h；yss update apply --help；yss lifecycle route -h
能力与限制: yss capabilities --json；未迁移的行为返回 UNPORTED。
`
}
