package cli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/width"

	"github.com/iloveZzz/yss-cli/internal/domain"
	"github.com/iloveZzz/yss-cli/internal/governance"
)

// Command metadata supplies help and preflight, while the existing executors
// retain required inputs, authority checks and transaction guards.
type argumentSpec struct {
	placeholder, description string
	boolean                  bool
	choices                  []string
}
type commandSpec struct {
	help    helpTopic
	options []string
	choices map[string][]string
}

var argumentSpecs = map[string]argumentSpec{}
var commands = map[string]commandSpec{}
var projectOptionNames = strings.Fields("root target-dir profile json help version project-name business-domain team-size plan out apply plan-file binding-file full issue-tracker")
var commonOptionNames = []string{"help", "json", "version"}

func initializeCommands() {
	// Every flag accepted by the existing dispatchers has a registered type.
	for _, key := range strings.Fields("root target-dir tool-root out output source artifact plan-file binding-file checkpoint file schema snapshot package items item task requirements implementation-root template-checkout cli-source report-dir home run-dir") {
		argumentSpecs[key] = argumentSpec{placeholder: "<路径>", description: "文件或目录路径"}
	}
	for _, key := range strings.Fields("profile project-name business-domain team-size issue-tracker sha256 to id term-refs allowed-context-ids work-unit stage kind gate boundary consumer approval-ref unit scope current-work-unit next-work-unit provider branch additional-path base runtime-store input token reason type value status exit-code refresh native") {
		argumentSpecs[key] = argumentSpec{placeholder: "<值>", description: "按当前命令说明指定"}
	}
	for _, key := range strings.Fields("json plan apply help version check include-example-docs force history require-approved continuation recover full") {
		argumentSpecs[key] = argumentSpec{description: "布尔开关，可使用 =true 或 =false；默认 false", boolean: true}
	}
	defineArgument("profile", "<Profile>", "项目模板类型；项目命令可从身份检测，init 必需", "spec", "design", "backend", "frontend")
	defineArgument("root", "<目录>", "项目根，默认当前目录；仅项目命令")
	defineArgument("target-dir", "<目录>", "项目根的兼容参数；优先使用 --root")
	defineArgument("tool-root", "<目录>", "程序安装目录；update 必需，upgrade 默认识别运行目录")
	defineArgument("out", "<新路径>", "计划文件或导出目录；目标须不存在，按命令选择")
	defineArgument("plan-file", "<文件>", "消费已有计划；写入前核验输入摘要")
	defineArgument("artifact", "<归档>", "本机平台 .tar.gz 或 .zip 发行包")
	defineArgument("sha256", "<SHA-256>", "发行包的 64 位十六进制 SHA-256")
	defineArgument("to", "<稳定版本>", "在线升级目标，如 1.1.0 或 v1.1.0；默认最新稳定版")
	defineArgument("issue-tracker", "<追踪器>", "Spec Tracker 配置", "local-markdown", "github", "gitlab")
	defineArgument("provider", "<提供方>", "CI 提供方，默认 github", "github")
	defineArgument("base", "<完整SHA>", "已确认实现仓的完整 Git 基线")
	defineArgument("exit-code", "<整数>", "实际退出码；成功状态必须为 0")
	defineArgument("value", "<JSON>", "单个 JSON 值，默认 null")
	defineArgument("kind", "<类型>", "当前命令的领域类型；runtime begin 默认 command")
	defineArgument("status", "<终态>", "运行记录的终态；成功状态必须匹配退出码 0")
	defineArgument("project-name", "<名称>", "项目名称，作为模板变量")
	defineArgument("business-domain", "<领域>", "项目业务领域，作为模板变量")
	defineArgument("team-size", "<规模>", "团队规模，作为模板变量")

	for _, row := range []struct{ key, description string }{
		{"json", "执行结果输出 JSON；帮助始终输出文本"},
		{"help", "显示当前命令帮助；不读取项目、不联网、不写入"},
		{"version", "显示 CLI 版本及来源；不需要项目"},
		{"plan", "只生成计划，使用 --out 保存；不应用项目修改"},
		{"apply", "应用已保存计划或显式恢复；写入前检查冲突"},
		{"check", "仅查询在线版本；不下载发行包、不写入"},
		{"full", "init/attach 安装完整资源；默认初始分发集合"},
	} {
		spec := argumentSpecs[row.key]
		spec.description = row.description
		argumentSpecs[row.key] = spec
	}
	for key, topic := range helpTopics {
		parts := strings.Fields(key)
		opts := append([]string{}, commonOptionNames...)
		switch parts[0] {
		case "init", "attach", "sync", "doctor", "diff", "migrate", "skills", "assets":
			opts = append(opts, projectOptionNames...)
			if len(parts) > 1 && parts[0] == "migrate" && (parts[1] == "status" || parts[1] == "recover" || parts[1] == "rollback") {
				opts = append(append([]string{}, commonOptionNames...), "root", "target-dir", "profile", "apply")
			}
		case "recover", "rollback":
			opts = append(opts, "root", "target-dir", "profile", "apply")
		case "upgrade":
			opts = append(opts, "check", "to", "tool-root")
		case "update":
			opts = append(opts, "tool-root")
			if len(parts) == 1 || parts[1] == "plan" || parts[1] == "apply" {
				opts = append(opts, "artifact", "sha256", "plan", "apply", "out", "plan-file")
			}
		case "bundle":
			opts = append(opts, "profile")
			if len(parts) == 1 || parts[1] == "export" {
				opts = append(opts, "out")
			}
		case "compat", "compat-api":
			opts = append(opts, "native")
		case "version", "capabilities":
		case "contract", "evidence", "handoff", "lifecycle":
			if len(parts) > 1 && parts[1] == "verify" {
				for _, k := range verificationOptions(parts[0], "") {
					opts = append(opts, k)
				}
			} else {
				action := "status"
				if len(parts) > 1 {
					action = parts[1]
				}
				for _, k := range governance.ArgumentKeys(parts[0], action) {
					if k != "arg0" {
						opts = append(opts, k)
					}
				}
			}
		default:
			action := "status"
			if len(parts) > 1 {
				action = parts[1]
			}
			for _, k := range governance.ArgumentKeys(parts[0], action) {
				if k != "arg0" {
					opts = append(opts, k)
				}
			}
		}
		seen := map[string]bool{}
		unique := []string{}
		for _, k := range opts {
			if !seen[k] {
				seen[k] = true
				unique = append(unique, k)
			}
		}
		sort.Strings(unique)
		commands[key] = commandSpec{help: topic, options: unique, choices: commandChoices(key)}
	}
}
func defineArgument(key, placeholder, description string, choices ...string) {
	argumentSpecs[key] = argumentSpec{placeholder, description, false, choices}
}
func booleanOptions() map[string]bool {
	out := map[string]bool{}
	for key, spec := range argumentSpecs {
		out[key] = spec.boolean
	}
	return out
}
func optionNames() []string {
	out := []string{}
	for k := range argumentSpecs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
func argumentError(message string) error {
	return &domain.Error{Code: "ARGUMENT", Message: message, Exit: 2}
}
func helpCommand(args []string) string {
	if len(args) > 0 && args[0] == "help" {
		args = args[1:]
	}
	if len(args) == 0 {
		return "yss --help"
	}
	if _, ok := commands[args[0]]; !ok {
		return "yss --help"
	}
	key := args[0]
	if len(args) > 1 {
		if _, ok := commands[key+" "+args[1]]; ok {
			key += " " + args[1]
		}
	}
	return "yss " + key + " --help"
}
func suggestions(input string, candidates []string) string {
	best := 3
	matches := []string{}
	for _, candidate := range candidates {
		d := editDistance(input, candidate)
		if d < best {
			best = d
			matches = []string{candidate}
		} else if d == best && d < 3 {
			matches = append(matches, candidate)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	if len(matches) > 3 {
		matches = matches[:3]
	}
	return "；您可能想使用: " + strings.Join(matches, "、")
}
func editDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	prev := make([]int, len(y)+1)
	for j := range prev {
		prev[j] = j
	}
	for i, r := range x {
		row := make([]int, len(y)+1)
		row[0] = i + 1
		for j, s := range y {
			cost := 0
			if r != s {
				cost = 1
			}
			row[j+1] = min(row[j]+1, prev[j+1]+1, prev[j]+cost)
		}
		prev = row
	}
	return prev[len(y)]
}
func unknownOption(key string, candidates []string) error {
	names := []string{}
	for _, k := range candidates {
		names = append(names, "--"+k)
	}
	return argumentError("未知或不支持的参数: --" + key + suggestions("--"+key, names))
}
func validateArguments(command string, o options) error {
	if o.values["version"] == "true" {
		return nil
	}
	spec, ok := commands[command]
	if !ok {
		names := []string{}
		for k := range commands {
			if !strings.Contains(k, " ") {
				names = append(names, k)
			}
		}
		return argumentError("未知命令: " + command + suggestions(command, names) + "；在线升级 CLI 使用 yss upgrade，离线安装使用 yss update")
	}
	key := command
	if len(o.args) > 1 {
		child := command + " " + o.args[1]
		if selected, found := commands[child]; found {
			key, spec = child, selected
		} else {
			children := []string{}
			for k := range commands {
				if strings.HasPrefix(k, command+" ") {
					children = append(children, strings.TrimPrefix(k, command+" "))
				}
			}
			if len(children) > 0 {
				return argumentError("未知子命令: " + child + suggestions(o.args[1], children))
			}
			return argumentError("命令 " + command + " 不接受位置参数: " + o.args[1])
		}
	}
	if len(o.args) > 2 && !spec.help.positional {
		return argumentError("命令 " + key + " 不接受额外位置参数")
	}
	if len(o.args) > 2 && command != "skills" && command != "assets" && command != "compat" {
		positional := false
		for _, k := range governance.ArgumentKeys(command, o.args[1]) {
			if k == "arg0" {
				positional = true
			}
		}
		if !positional || len(o.args) > 3 {
			return argumentError("命令 " + key + " 不支持这些额外位置参数")
		}
	}
	allowed := map[string]bool{}
	effectiveOptions := spec.options
	if strings.HasSuffix(key, " verify") {
		group := strings.Fields(key)[0]
		if group == "contract" || group == "evidence" || group == "handoff" || group == "lifecycle" {
			effectiveOptions = append(append([]string{}, commonOptionNames...), verificationOptions(group, o.values["kind"])...)
		}
	}
	for _, k := range effectiveOptions {
		allowed[k] = true
	}
	keys := []string{}
	for k := range o.values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !allowed[k] {
			return unknownOption(k, effectiveOptions)
		}
		flag := argumentSpecs[k]
		if choices, ok := spec.choices[k]; ok {
			flag.choices = choices
		}
		v := o.values[k]
		if !flag.boolean && strings.TrimSpace(v) == "" {
			return argumentError("参数不能为空: --" + k + " " + flag.placeholder)
		}
		if len(flag.choices) > 0 {
			found := false
			for _, c := range flag.choices {
				if v == c {
					found = true
				}
			}
			if !found {
				return argumentError(fmt.Sprintf("参数 --%s %s 的值无效: %s；可选值: %s", k, flag.placeholder, v, strings.Join(flag.choices, "|")))
			}
		}
		if k == "exit-code" {
			if _, err := strconv.Atoi(v); err != nil {
				return argumentError("--exit-code <整数> 必须为实际整数退出码")
			}
		}
	}
	return nil
}

func renderOptions(spec commandSpec) string {
	labels := []string{}
	descriptions := []string{}
	columnWidth := 30
	for _, key := range spec.options {
		flag := argumentSpecs[key]
		if choices, ok := spec.choices[key]; ok {
			flag.choices = choices
		}
		label := "--" + key
		if key == "help" {
			label = "-h, --help"
		}
		if key == "version" {
			label = "-V, --version"
		}
		if flag.placeholder != "" {
			label += " " + flag.placeholder
		}
		description := flag.description
		if len(flag.choices) > 0 {
			description += "；可选值: " + strings.Join(flag.choices, "|")
		}
		labels = append(labels, label)
		descriptions = append(descriptions, description)
		columnWidth = max(columnWidth, terminalWidth(label))
	}
	rows := []string{}
	for i, label := range labels {
		rows = append(rows, "  "+label+strings.Repeat(" ", columnWidth-terminalWidth(label)+2)+descriptions[i])
	}
	return strings.Join(rows, "\n")
}
func terminalWidth(s string) int {
	columns := 0
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		switch width.LookupRune(r).Kind() {
		case width.EastAsianWide, width.EastAsianFullwidth:
			columns += 2
		default:
			columns++
		}
	}
	return columns
}

// Finite values belong to their command; project-defined IDs remain unrestricted.
func commandChoices(key string) map[string][]string {
	switch key {
	case "contract verify":
		return map[string][]string{"kind": {"slice", "scaffold", "task"}}
	case "evidence verify":
		return map[string][]string{"kind": {"approval", "user-decision", "verification"}}
	case "handoff verify":
		return map[string][]string{"kind": {"package", "consumption"}}
	case "runtime complete":
		return map[string][]string{"status": {"passed", "success", "completed", "ok", "failed", "failure", "cancelled", "canceled", "timed-out", "timeout", "error"}}
	}
	return nil
}

// Public verify flags are narrower than the structural dispatch superset.
// Keep kind-specific selectors out of unrelated interfaces before reading assets.
func verificationOptions(group, kind string) []string {
	options := strings.Fields("root target-dir profile json file checkpoint home run-dir tool-root template-checkout")
	kinds := []string{kind}
	if group == "lifecycle" {
		return append(options, "history")
	}
	options = append(options, "kind")
	if kind == "" {
		kinds = commandChoices(group + " verify")["kind"]
	}
	for _, k := range kinds {
		switch group + "." + k {
		case "contract.slice":
			options = append(options, "approval-ref", "unit")
		case "contract.task":
			options = append(options, "history")
		case "evidence.approval":
			options = append(options, "require-approved", "history", "gate", "boundary", "task")
		case "evidence.user-decision":
			options = append(options, "requirements", "continuation", "task")
		case "evidence.verification":
			options = append(options, "approval-ref", "task")
		case "handoff.package":
			options = append(options, "package")
		case "handoff.consumption":
			options = append(options, "consumer")
		}
	}
	return options
}
