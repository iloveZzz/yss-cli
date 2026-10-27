package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/helpview"
)

type helpTopic struct {
	summary, usage, options, examples, notes string
	positional                               bool
}

var helpTopics = map[string]helpTopic{
	"init": {
		summary:  "创建固定模板来源的项目实例。",
		usage:    "--profile <spec|backend> --root <目录> [--project-name <名称>]",
		options:  "--profile       必需，选择 Spec 或 Backend\n--root          项目目录；默认当前目录，推荐显式指定新目录\n--project-name  项目名称\n--business-domain / --team-size  模板变量\n--installation skills|governed  默认 skills；完整治理显式选择 --installation governed\n--full          仅扩大所选安装方式的资源集合，不切换安装方式\n--plan --out <新文件>  只生成计划；默认直接初始化\n--apply --plan-file <文件>  应用已保存计划\n--issue-tracker <local-markdown|github|gitlab>  Spec Tracker 配置",
		examples: "yss init --profile spec --root ./demo-spec --project-name 演示项目\nyss init --profile backend --root ./demo-backend --plan --out /tmp/yss-init-plan.json",
		notes:    "拒绝覆盖用户文件；初始化使用程序内置 Bundle，无需 Node/Python 或网络。已有工程请先查看 yss attach --help。",
	},
}

func renderHelp(args []string) (string, error) {
	return renderHelpFrom(newRootCommand(nil, io.Discard, io.Discard), args)
}

func commandTopic(cmd *cobra.Command) helpTopic {
	key := strings.TrimPrefix(cmd.CommandPath(), "yss ")
	topic, ok := helpTopics[key]
	if !ok && cmd.HasParent() {
		parentKey := strings.TrimPrefix(cmd.Parent().CommandPath(), "yss ")
		topic = helpTopics[parentKey]
		topic.usage = strings.TrimPrefix(topic.usage, cmd.Name()+" ")
	}
	if strings.HasPrefix(key, "completion") {
		topic.usage = ""
		topic.notes = "只向 stdout 输出脚本，不读取项目或访问网络，也不自动安装或修改 Shell 配置。命令、选项和静态枚举由 CLI 提供；路径候选由 Shell 枚举，不查询项目 ID、Ticket 或业务资产。即使指定 --json，仍输出脚本文本。"
		topic.options = "脚本类型由必选子命令 bash、zsh、fish 或 powershell 指定。以下加载命令在对应 Shell 中执行，仅对当前会话生效；长期启用请按自己的 Shell 配置加载保存的脚本，升级 yss 后重新生成。"
		if key == "completion" {
			topic.usage = "<bash|zsh|fish|powershell>"
		}
		switch key {
		case "completion bash":
			topic.examples = "# 先按当前环境加载 bash-completion（需提供 _get_comp_words_by_ref 等函数）\nyss completion bash > /tmp/yss-completion.bash\nsource /tmp/yss-completion.bash"
		case "completion zsh":
			topic.examples = "autoload -Uz compinit\ncompinit\nyss completion zsh > /tmp/_yss\nsource /tmp/_yss"
		case "completion fish":
			topic.examples = "yss completion fish | source"
		case "completion powershell":
			topic.examples = "yss completion powershell | Out-String | Invoke-Expression"
		default:
			topic.examples = "yss completion bash > /tmp/yss-completion.bash\nyss completion zsh > /tmp/_yss\nyss completion fish > /tmp/yss-completion.fish\nyss completion powershell > ./yss-completion.ps1"
		}
	}
	if key == "git policy set" {
		topic.usage = "--key <字段> --value <值> --plan --out <新文件> | --apply --plan-file <文件>"
	}
	topic.summary = cmd.Short
	if cmd.Example != "" {
		topic.examples = cmd.Example
	}
	return topic
}

func renderHelpFrom(root *cobra.Command, args []string) (string, error) {
	if len(args) > 0 && args[0] == "help" {
		args = args[1:]
	}
	if len(args) == 0 {
		return rootHelpFrom(root), nil
	}
	if args[0] == "tutorial" {
		if len(args) > 2 {
			return "", argumentError("教程只接受一个主题；查看 yss help tutorial")
		}
		topic := ""
		if len(args) == 2 {
			topic = args[1]
		}
		return renderTutorial(topic)
	}
	if args[0] == "examples" {
		return renderExamples(args[1:])
	}
	if args[0] == "errors" {
		if len(args) > 2 {
			return "", argumentError("错误帮助只接受一个错误码")
		}
		code := ""
		if len(args) == 2 {
			code = args[1]
		}
		return renderErrorHelp(code)
	}
	key := strings.Join(args, " ")
	cmd, rest := lookupCommand(root, args)
	topic := commandTopic(cmd)
	if cmd == root || (len(rest) > 0 && !(len(args) > 2 && topic.positional)) {
		return "", &domain.Error{Code: "ARGUMENT", Message: "未知帮助命令或子命令: " + key + "；运行 yss --help 查看可用命令", Exit: 2}
	}
	key = strings.TrimPrefix(cmd.CommandPath(), "yss ")
	children := []string{}
	for _, child := range cmd.Commands() {
		if !child.Hidden {
			children = append(children, child.Name()+"  "+child.Short)
		}
	}
	sort.Strings(children)
	subcommands := ""
	if len(children) > 0 {
		subcommands = "\n\n子命令:\n" + strings.Join(children, "\n")
	}
	return fmt.Sprintf("yss %s — %s\n──────────────────────\n%s\n\n用法: %s%s\n\n前置条件:\n%s\n%s\n\n最小示例:\n%s\n\n参数:\n%s\n\n参数说明与条件:\n%s\n\n预期结果:\n%s\n\n下一步:\n%s\n\n常见错误:\n%s\n详细示例: yss help examples %s", domain.Version, key, topic.summary, strings.TrimSpace("yss "+key+" "+topic.usage), subcommands, prerequisite(key), topic.notes, safeExampleText(topic.examples), renderCommandOptions(cmd), topic.options, expectedResult(key), nextReading(key), strings.Join(commonErrorCodes(key), "、")+"；yss help errors <错误码>", key), nil
}

func rootHelp() string { return rootHelpFrom(newRootCommand(nil, io.Discard, io.Discard)) }
func rootHelpFrom(root *cobra.Command) string {
	var out strings.Builder
	fmt.Fprintf(&out, "yss %s — Spec / Backend 统一入口\n──────────────────────\n\n用法: yss [选项] <命令> [参数]\n\nProfile 职责:\n  spec 综合研发主控；支持目标政策的完整正式功能默认推进业务验收。\n  Spec 包含必要产品设计；backend 承担技术设计与专职交付。\n  旧 Design 保留原记录，当前不提供转为 Spec 的迁移。\n  本次目标、本端职责与整体业务分别核验；旧实例先核对能力并显式同步。\n\n快速上手（默认 skills 独立技能安装；选择一个 Profile，在独立新目录运行）:\n", domain.Version)
	for _, p := range []string{"spec", "backend"} {
		fmt.Fprintf(&out, "  yss init --profile %s --root ./demo-%s --project-name 演示项目\n", p, p)
	}
	out.WriteString("  yss doctor --root ./demo-spec --human\n  yss skills list --root ./demo-spec --details --json\n  完整治理使用 --installation governed；--full 只扩大当前安装方式的资源。\n\n治理安装的生命周期导航:\n")
	if v, e := helpview.Load("spec"); e == nil {
		names := []string{}
		for _, s := range v.Stages {
			names = append(names, s.Name)
		}
		out.WriteString("  正式：" + strings.Join(names, " → ") + "\n")
		fmt.Fprintf(&out, "  固定模板：%s；来源摘要：%s\n", v.TemplateCommit, v.RegistrySHA256)
	} else {
		out.WriteString("  生命周期视图待核验：" + e.Error() + "\n")
	}
	out.WriteString("  日常：需求与验收 → 技术技能 → 实现 → 测试 → 适用复核 → verify-daily（依实例当前政策）\n  教程：yss help tutorial governed | yss help tutorial daily\n  先 Spec、再设计、再交付：yss help tutorial spec\n  目标设置与续推：yss lifecycle target --help | yss help examples lifecycle target\n\n通用选项:\n  -h, --help       显示离线帮助\n  -V, --version    显示程序及来源\n  --human          强制中文摘要；终端默认中文，管道保持原格式\n  --json           输出机器协议 1；与 --human 互斥\n  --diagnostics    与 --json 同用，失败时附加诊断\n\n命令:\n")
	for _, group := range []struct {
		label string
		names []string
	}{
		{"项目", []string{"init", "scaffold", "profile", "attach", "doctor", "diff", "sync", "migrate", "recover", "rollback"}},
		{"资源", []string{"skills", "assets", "bundle"}},
		{"Git", []string{"git"}},
		{"程序", []string{"version", "capabilities", "upgrade", "update"}},
		{"治理", []string{"explain", "check", "verify", "mcp", "tracker", "trace", "context", "lifecycle", "stage", "reading", "contract", "evidence", "handoff", "project-ci"}},
		{"工具", []string{"runtime", "archive", "xml", "completion"}},
	} {
		fmt.Fprintf(&out, "  [%s]\n", group.label)
		for _, name := range group.names {
			fmt.Fprintf(&out, "    %-16s %s\n", name, func() string { cmd, _ := lookupCommand(root, []string{name}); return cmd.Short }())
		}
	}
	out.WriteString("  help [命令/子命令]  显示对应帮助；help tutorial 查看完整离线教程\n\n项目常用参数（程序升级不接受这些参数）:\n  --root <目录>    项目根，默认当前目录\n  --profile <Profile>  spec|backend；init 必需，其余从身份检测\n\n")
	out.WriteString(`升级、同步与恢复:
  yss upgrade --check                         查询 CLI 稳定版本
  yss upgrade                                 升级 CLI 程序
  yss update status --tool-root ./tools/yss    诊断离线安装与事务
  yss sync --root ./demo-spec --plan --out /tmp/yss-sync-plan.json
  yss sync --root ./demo-spec --apply --plan-file /tmp/yss-sync-plan.json

参数规则:
  长参数接受 --参数 值 或 --参数=值；含空格的值用引号包围。
  -h、-V 独立使用；不支持 -v 或 -hV 等组合短参数。
  布尔值只接受小写 true/false；未提供、空值和 false 含义不同。
  治理语义命令、scaffold、upgrade 拒绝重复参数；其他命令以后值为准。
  -- 结束选项解析；其后的 --help、--json 等都作为位置参数，原数量及路径限制仍生效。
  示例：yss check --root ./demo-spec -- -example.yaml

Shell 补全:
  yss completion bash|zsh|fish|powershell  输出对应脚本（四选一，不输入竖线）。
  yss completion zsh --help               查看该 Shell 的生成与加载示例。
  Bash 先加载 bash-completion；Zsh 先初始化 compinit。CLI 不自动安装或修改配置。

帮助: yss -h | yss <命令/子命令> --help | yss help <命令/子命令>
完整离线教程: yss help tutorial
按主题阅读: yss help tutorial quickstart|daily|governed|spec|backend|maintenance
示例索引: yss help examples [命令/子命令]
错误索引: yss help errors [错误码]
入口区别: upgrade 升级程序；sync 同步模板；recover 恢复未完成事务；rollback 回退最近成功事务。
能力与限制: yss capabilities --json；未迁移行为返回 UNPORTED。
帮助不读取项目、不访问网络、不创建资产。
`)
	return out.String()
}
