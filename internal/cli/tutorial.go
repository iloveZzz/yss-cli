package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/iloveZzz/yss-cli/internal/helpview"
)

var tutorialTopics = []string{"quickstart", "governed", "daily", "spec", "design", "backend", "frontend", "maintenance"}
var placeholderPattern = regexp.MustCompile(`<[^<>\n]+>`)

func safeExampleText(s string) string {
	// Placeholders must not become shell input/output redirection when copied.
	positions := placeholderPattern.FindAllStringIndex(s, -1)
	var out strings.Builder
	start := 0
	for _, p := range positions {
		out.WriteString(s[start:p[0]])
		quoted := p[0] > 0 && (s[p[0]-1] == '\'' || s[p[0]-1] == '"')
		if !quoted {
			out.WriteByte('"')
		}
		out.WriteString(s[p[0]:p[1]])
		if !quoted {
			out.WriteByte('"')
		}
		start = p[1]
	}
	out.WriteString(s[start:])
	return out.String()
}

func prerequisite(key string) string {
	group := strings.Fields(key)[0]
	switch group {
	case "init":
		return "选择一个 Profile 和不存在或空的新目录；需要审阅写范围时先 --plan --out，再应用保存计划。"
	case "attach":
		return "已有普通工程；先检查定制与受管范围，再保存接管计划。"
	case "version", "capabilities", "bundle", "upgrade", "update":
		return "无需项目身份。安装/恢复操作使用独立工具目录；程序写入按对应保存计划或明确恢复范围执行。"
	case "compat", "compat-api":
		return "固定历史别名或已登记的原生传输请求；保留旧接口的兼容边界。"
	case "contract", "evidence", "handoff", "stage":
		return "合法项目身份，以及命令所需的当前合同、checkpoint 或证据；资产来源与消费范围必须可核验。"
	case "lifecycle":
		return "查询注册表需项目身份；状态/verify 消费当前 checkpoint；daily 接口需同一任务、已确认实现仓和完整基线。"
	case "runtime", "archive", "xml":
		return "准备对应运行标识、存储、归档或 XML 文件；查看本子命令的范围与限制。"
	default:
		return "合法项目身份及所需输入；项目根默认当前目录，推荐显式 --root。"
	}
}

func exampleKind(key string) string {
	group := strings.Fields(key)[0]
	if group == "contract" || group == "evidence" || group == "handoff" || group == "stage" || strings.Contains(key, "lifecycle verify") || key == "lifecycle status" || key == "lifecycle route" || group == "compat-api" {
		return "需要当前资产"
	}
	return "直接运行（替换路径；写入示例在独立演示目录执行）"
}
func expectedResult(key string) string {
	if strings.Contains(key, "verify") || strings.HasPrefix(key, "project-ci") {
		return "返回当前范围的逐项检查与诊断；仅在当前检查通过时得到通过结论。核验不执行业务测试或创建批准。"
	}
	if key == "init" {
		return "形成所选 Profile 的项目身份与受管基线；--plan 模式只生成计划。"
	}
	if strings.Contains(key, "plan") || key == "sync" || key == "attach" || strings.HasPrefix(key, "stage register") || strings.HasPrefix(key, "stage update") {
		return "输出可审阅计划；应用保存计划时重新核验当前输入。"
	}
	if strings.Contains(key, "recover") || strings.Contains(key, "rollback") {
		return "显示所属事务及实际恢复结果；按子命令标明的只读/写入语义执行。"
	}
	return "返回实际查询或操作结果及适用范围；查询成功不代表阶段批准。"
}
func nextReading(key string) string {
	switch strings.Fields(key)[0] {
	case "init", "attach":
		return "yss doctor --help；核对身份后用 lifecycle 查询当前入口。"
	case "doctor", "diff", "sync", "migrate", "recover", "rollback":
		return "根据实际诊断选择计划、恢复或迁移入口；应用后再 doctor/diff。"
	case "contract", "stage", "evidence", "handoff", "lifecycle", "project-ci":
		return "yss lifecycle status --help；消费当前证据和权威退出条件后，由对应负责人继续。"
	case "upgrade", "update":
		return "yss update status --help；程序来源一致后，独立规划项目模板同步。"
	case "skills", "assets":
		return "先 list 核对当前 Profile 支持的标识，再按保存计划补装。"
	default:
		return "查看同组命令帮助和当前查询结果，选择需要的下一项操作。"
	}
}
func commonErrorCodes(key string) []string {
	group := strings.Fields(key)[0]
	if group == "upgrade" || group == "update" {
		return []string{"ARGUMENT", "NETWORK", "ARTIFACT", "INSTALLATION", "INTERRUPTED", "CONCURRENT", "VERSION"}
	}
	if group == "contract" || group == "evidence" || group == "handoff" || group == "lifecycle" || group == "stage" || group == "project-ci" {
		return []string{"ARGUMENT", "IDENTITY", "INPUT", "SCHEMA_VALIDATION", "GOVERNED_REQUIRED", "UNPORTED"}
	}
	return []string{"ARGUMENT", "IDENTITY", "PATH", "CONFLICT", "INPUT_DRIFT", "UNPORTED"}
}
func renderExamples(args []string) (string, error) {
	if len(args) == 0 {
		keys := make([]string, 0, len(commands))
		for k := range commands {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return "YSS 示例索引\n使用 yss help examples <命令/子命令> 查看适用条件、完整命令、预期结果与失败恢复。\n\n" + strings.Join(keys, "\n"), nil
	}
	key := strings.Join(args, " ")
	spec, ok := commands[key]
	if !ok {
		return "", argumentError("未知示例命令: " + key + "；查看 yss help examples")
	}
	return fmt.Sprintf("%s — %s\n示例类型：%s\n\n适用条件：%s\n输入材料：%s\n\n完整命令（占位符须替换为当前真实输入）：\n%s\n\n预期结果：%s\n下一步：%s\n失败恢复：保留输入与现场；查看 %s 或 yss help errors，处理后重新核验/规划。", key, spec.help.summary, exampleKind(key), prerequisite(key), spec.help.options, safeExampleText(spec.help.examples), expectedResult(key), nextReading(key), helpCommand(args)), nil
}

func renderTutorial(topic string) (string, error) {
	if topic == "" {
		var out strings.Builder
		out.WriteString("YSS 完整离线教程\n阅读主题：" + strings.Join(tutorialTopics, "、") + "\n所有帮助仅输出文本；命令由使用者在适当目录执行。内部测试案例不是生产批准或真实交付证据。\n\n")
		for _, name := range tutorialTopics {
			section, e := renderTutorial(name)
			if e != nil {
				return "", e
			}
			out.WriteString(section + "\n\n")
		}
		return out.String(), nil
	}
	switch topic {
	case "quickstart":
		return quickstartTutorial, nil
	case "daily":
		return dailyTutorial(), nil
	case "governed":
		return governedTutorial("spec")
	case "spec", "design", "backend", "frontend":
		return profileTutorial(topic)
	case "maintenance":
		return maintenanceIntroduction + safeExampleText(maintenanceTutorial), nil
	default:
		return "", argumentError("未知教程主题: " + topic + "；可选: " + strings.Join(tutorialTopics, "、"))
	}
}

const quickstartTutorial = `快速上手
前置条件：在独立新目录选择一个 Profile；spec 综合治理，design 战略与产品设计交接，backend/frontend 专职交付。
输入材料：项目名称与目标目录；已有工程改用 attach 的保存计划。

  yss version --json
  yss capabilities --json
  yss init --profile spec --root ./demo-spec --project-name 演示项目 --plan --out ./demo-spec-init-plan.json
  yss init --profile spec --root ./demo-spec --apply --plan-file ./demo-spec-init-plan.json
  yss doctor --root ./demo-spec --human
  yss context verify --root ./demo-spec --json
  yss lifecycle query --root ./demo-spec --id work-unit.entry-triage --json

预期结果：计划保存、初始化形成身份与基线；后续查询返回当前输入状态。
下一步：yss help tutorial governed；符合日常政策时另读 daily。
失败恢复：目录错误查看 yss help errors IDENTITY；未完成原生事务先 yss recover --root ./demo-spec --json，再在已确认恢复范围内增加 --apply。
计划文件必须是新文件。终端默认中文；管道保留原格式；--human 强制中文；--json --diagnostics 在失败时附加结构化诊断。`

func dailyTutorial() string {
	v, e := helpview.Load("spec")
	source := "来源摘要：待核验"
	if e == nil {
		source = fmt.Sprintf("来源摘要：template=%s；policy=%s；日常能力=%t", v.TemplateCommit, v.PolicySHA256, v.DailyEnabled)
	}
	return `日常交付（Spec）
` + source + `
前置条件：当前 Spec 实例启用日常政策；同一任务有需求与验收、单一实现仓、已确认完整基线、适用 Skills、实际测试、独立审查和回滚依据。
输入材料：docs/daily-task.md 的同一 Ticket/PR 证据区；实际实现仓；已确认的40位 SHA。格式消费项目 .agents/skills/yss-product-lifecycle/references/daily-delivery.md。
顺序：需求与验收 → YSS 技术技能 → 实现 → 测试 → 独立审查 → verify-daily。

  yss lifecycle route --root ./demo-spec --task docs/daily-task.md --implementation-root "/实际实现仓" --base "<已确认40位SHA>" --json --diagnostics
  yss lifecycle verify-daily --root ./demo-spec --task docs/daily-task.md --implementation-root "/实际实现仓" --base "<已确认40位SHA>" --json --diagnostics

预期结果：route 返回 daily、governed 或 needs-info 及原因。
  daily：满足政策，按同一记录实现、实际测试及独立审查，再 verify-daily。
  governed：已有正式绑定或命中排除风险，从最近可信正式阶段继续。
  needs-info：缺事实先调查，补齐同一任务记录后再次 route。
下一步：verify-daily 通过后按当前范围交付；它核验已有记录，不执行业务测试或创建批准。
失败恢复：缺独立审查、过期测试或差异变化时补当前证据；已有正式任务不降级。其他 Profile 以实际政策与能力为准。
内部测试中的模拟日志和审查记录仅验证协议；公开项目的实际命令、独立审查和批准由对应负责人取得。`
}

func governedTutorial(profile string) (string, error) {
	v, e := helpview.Load(profile)
	if e != nil {
		return "", e
	}
	var out strings.Builder
	fmt.Fprintf(&out, "正式生命周期（%s）\n来源摘要：template=%s；registry=%s；profile=%s；policy=%s\n前置条件：合法项目身份，从当前任务最近可信阶段继续；阶段触发与退出条件由该 Profile 固定模板及项目当前资产核验。\n输入材料：当前 checkpoint、已确认战略/Spec/合同和相应证据。路径示例使用新项目 .work；旧项目按 tracker.root 替换。以下需要当前资产的命令在材料齐备后执行。\n\n", profile, v.TemplateCommit, v.RegistrySHA256, registeredDigest(v.ProfileSHA256), registeredDigest(v.PolicySHA256))
	root := "./demo-" + profile
	for i, stage := range v.Stages {
		fmt.Fprintf(&out, "%d. %s（%s）\n目标：%s\n", i+1, stage.Name, stage.ID, stage.Goal)
		fmt.Fprintf(&out, "  yss stage query --root %s --id %s --json\n", root, stage.ID)
		switch stage.ID {
		case "stage.entry-triage", "stage.harness-entry":
			fmt.Fprintf(&out, "  yss doctor --root %s --json\n  yss context verify --root %s --json\n  yss lifecycle query --root %s --id %s --json\n", root, root, root, v.EntryWorkUnit)
		case "stage.plan":
			fmt.Fprintf(&out, "  yss stage register --root %s --checkpoint .work/feature/checkpoint.json --items docs/work-items.json > %s/docs/stage-plan.json\n  yss stage apply --root %s --plan-file docs/stage-plan.json --json\n", root, root, root)
			out.WriteString("  工作项 JSON 与 checkpoint 由当前任务准备；重定向保存原始计划，不加 --json envelope。Plan 撰写和批准由对应 Skill/负责人完成。\n")
		case "stage.spec-architecture":
			fmt.Fprintf(&out, "  yss stage update --root %s --checkpoint .work/feature/checkpoint.json --items docs/work-items.json > %s/docs/stage-plan-next.json\n  yss lifecycle verify --root %s --checkpoint .work/feature/checkpoint.json --json --diagnostics\n", root, root, root)
			out.WriteString("  更新计划经审阅后使用 stage apply；Spec 由批准的战略输入承接。\n")
		case "stage.product-design":
			fmt.Fprintf(&out, "  yss assets list --root %s --json\n  yss lifecycle status --root %s --checkpoint .work/feature/checkpoint.yaml --json\n", root, root)
			out.WriteString("  仅命中产品设计影响时使用原型与设计技能；未命中项按权威条件说明适用性。\n")
		case "stage.system-data-engineering":
			fmt.Fprintf(&out, "  yss contract verify --root %s --kind scaffold --file docs/scaffold.json --json --diagnostics\n  yss handoff verify --root %s --kind package --package docs/handoff --json\n", root, root)
			out.WriteString("  API：OAS 3.1 YAML Draft → 锁定工具校验 → 独立 Review → Freeze → 实现与契约测试；由 OpenAPI 技能及负责人完成。\n")
		case "stage.ticket-formalization":
			if profile == "design" {
				fmt.Fprintf(&out, "  yss handoff verify --root %s --kind package --package docs/handoff --json --diagnostics\n", root)
				out.WriteString("  Design 正式化业务级 Ticket 并交接批准且当前的业务方案；研发合同与实现由下游接收方完成。\n")
				break
			}
			fmt.Fprintf(&out, "  yss contract verify --root %s --kind slice --file docs/contract.json --checkpoint .work/feature/checkpoint.yaml --json --diagnostics\n", root)
			out.WriteString("  输入为批准且当前的 Slice 合同及其消费证据；校验器不创建批准或设置 ready-for-agent。\n")
		case "stage.technical-design", "stage.frontend-engineering-design":
			fmt.Fprintf(&out, "  yss handoff verify --root %s --kind package --package docs/handoff --json --diagnostics\n  yss lifecycle status --root %s --checkpoint .work/feature/checkpoint.yaml --json\n", root, root)
			out.WriteString("  消费当前上游接收与工程约束；适用的技术/前端设计 Skill 及负责人完成设计、独立审查和批准。输入通过不授予实现资格。\n")
		case "stage.implementation-repository-preparation":
			fmt.Fprintf(&out, "  yss contract verify --root %s --kind scaffold --file docs/scaffold.json --json --diagnostics\n", root)
			out.WriteString("  由工程接入或脚手架 Skill 在批准且当前的工程合同下接入真实实现仓；先核对 Scaffold/Preparation 证据。\n")
		case "stage.slice-contract":
			fmt.Fprintf(&out, "  yss contract verify --root %s --kind slice --file docs/contract.json --checkpoint .work/feature/checkpoint.yaml --json --diagnostics\n", root)
			out.WriteString("  消费批准且当前的 Slice 合同、条件化交接与工程输入；编译器不创建批准。\n")
		case "stage.vertical-slice-implementation", "stage.slice-implementation":
			fmt.Fprintf(&out, "  yss skills list --root %s --json\n  yss lifecycle status --root %s --checkpoint .work/feature/checkpoint.yaml --json\n", root, root)
			out.WriteString("  仅按批准合同的写范围实现；需要的 Skill 闭包来自当前合同。前端在实际实现仓 pnpm，后端优先根 ./mvnw；记录真实测试并由独立审查者审查。\n")
		case "stage.verification-release-retrospective", "stage.verification":
			fmt.Fprintf(&out, "  yss evidence verify --root %s --kind verification --file docs/verification.json --json --diagnostics\n  yss project-ci verify --root %s --base \"<已确认40位SHA>\" --runtime-store off --json\n  yss lifecycle verify --root %s --checkpoint .work/feature/checkpoint.yaml --json\n", root, root, root)
			out.WriteString("  核验消费已有证据；真实发布、推送与外部动作消费相应授权，复盘保留实际结论。\n")
		}
		fmt.Fprintf(&out, "预期结果：查询/核验当前输入与适用条件。\n下一步条件：%s\n失败恢复：查看当前报告的逐项诊断，从最近可信阶段补齐受影响材料；yss help errors。\n\n", stage.Exit)
	}
	if len(v.ReferenceStages) > 0 {
		out.WriteString("其他登记阶段仅供跨 Profile 引用，本 Profile 不执行：\n")
		for _, stage := range v.ReferenceStages {
			fmt.Fprintf(&out, "  %s（%s）\n", stage.Name, stage.ID)
		}
	}
	return out.String(), nil
}

func registeredDigest(value string) string {
	if value == "" {
		return "尚未登记"
	}
	return value
}

func profileTutorial(profile string) (string, error) {
	v, e := helpview.Load(profile)
	if e != nil {
		return "", e
	}
	responsibility := map[string]string{"spec": "综合治理；按正式生命周期或当前日常政策分流。", "design": "战略与产品设计交接；形成当前职责内的战略、设计和交接材料。", "backend": "消费战略交接及批准工程合同，完成后端专职交付。", "frontend": "核验战略与后端交接输入，在批准合同内完成前端交付。"}[profile]
	root := "./demo-" + profile
	intro := fmt.Sprintf("Profile %s\n职责：%s\n日常政策启用：%t\n\n前置条件：独立新目录，或先 attach 接管已有工程；已有任务从最近可信接入点恢复。\n输入材料：项目名称、当前职责对应的合同/交接材料。\n\n  yss init --profile %s --root %s --project-name 演示项目\n  yss doctor --root %s --json\n  yss context verify --root %s --json\n  yss lifecycle query --root %s --id %s --json\n  yss assets list --root %s --json\n  yss skills list --root %s --json\n\n预期结果：仅安装该 Profile 支持的资源，返回实际来源与输入状态。\n下一步：按以下固定 Profile 顺序继续；交接核验见 yss handoff verify --help。\n失败恢复：身份、依赖或交接材料不匹配时补当前输入；未支持能力明确返回 UNPORTED。\n\n", profile, responsibility, v.DailyEnabled, profile, root, root, root, root, v.EntryWorkUnit, root, root)
	stages, err := governedTutorial(profile)
	return intro + stages, err
}

const maintenanceIntroduction = `维护与恢复
前置条件：分别确认程序、项目模板、旧实例及插件绑定的来源与事务范围。
程序升级：upgrade；模板同步：sync；旧实例迁移：migrate。
普通 recover/rollback 默认只读，增加 --apply 执行；migrate recover/rollback 与 update recover/rollback 本身写入。
先核对状态及归档，在已确认范围内恢复。已绑定插件使用对应插件 project-upgrade-plan/apply 或 project-migration-plan/apply。
预期结果：计划、应用与恢复均绑定实际输入；后续用户修改阻止覆盖。
下一步：核对 doctor/diff 或 update status，保留本次真实验证与恢复材料。
失败恢复：冲突与输入漂移先保留现场，再检查诊断并重新规划；原始旧事务由对应固定旧执行器恢复。

`
