package cli

const projectFlags = "--root <目录>  项目根，默认当前目录；推荐显式指定\n--profile <spec|design|backend|frontend>  必需或从项目身份检测"
const upgradeFlags = "--review-out <新目录>  项目外审查材料与决议模板\n--base-bundle <路径>  离线历史 Bundle 完整材料\n--resolution-file <文件>  与 --plan --plan-file 原计划一起重新规划"
const planFlags = "--plan --out <新文件>  生成保存计划，输出文件必须不存在\n--apply --plan-file <文件>  应用保存计划；输入变化时拒绝"
const governanceNotes = "读取当前项目的治理资产；校验通过不会创建批准或授予实现、发布权限。必要资产、身份和依赖必须已存在，能力缺失返回 UNPORTED。"

// The registry is shared by root, group and leaf help. It describes public
// behavior only; command execution and its authority checks remain unchanged.
func init() {
	helpTopics["contract view"] = helpTopic{"按当前 Slice 或唯一工作单元阅读；不验证批准、不授予执行权限。", "--root <目录> --kind slice --file <合同> [--view review|task|full] [--unit <ID>] [--json]", projectFlags, "yss contract view --root ./demo-spec --kind slice --file ./slice.yaml --view task --unit work-unit.slice-backend", "默认 review；文本为 Markdown，JSON 只含一份结构化内容。task 必须指定唯一工作单元。", false}
	helpTopics["attach"] = helpTopic{"首次接管已有工程的模板受管资产；原生实例使用 sync，旧实例使用 migrate。", "--root <目录> --profile <Profile> --plan --out <新文件>", projectFlags + "\n" + planFlags + "\n" + upgradeFlags + "\n--full  选择完整资源集合\n--binding-file <文件>  将插件 binding 纳入同一计划", "yss attach --profile backend --root ./existing-backend --plan --out /tmp/yss-attach-plan.json\nyss attach --root ./existing-backend --apply --plan-file /tmp/yss-attach-plan.json", "保留业务目录、CONTEXT.md 和用户 .github；定制冲突须先处置。", false}
	helpTopics["sync"] = helpTopic{"将项目模板升级到本 CLI 内置固定 Bundle。", "--root <目录> --plan --out <新文件>", projectFlags + "\n" + planFlags + "\n" + upgradeFlags, "yss sync --root ./demo-spec --plan --out /tmp/yss-sync-plan.json\nyss sync --root ./demo-spec --apply --plan-file /tmp/yss-sync-plan.json", "这是项目模板升级。升级 CLI 程序使用 yss upgrade；不会自动迁移旧 metadata。", false}
	for _, action := range []string{"doctor", "diff"} {
		summary := "检查项目身份、受管基线和冲突。"
		if action == "diff" {
			summary = "查看当前文件相对固定模板的差异和同步计划。"
		}
		helpTopics[action] = helpTopic{summary, "--root <目录> [--profile <Profile>] [--json]", projectFlags, "yss " + action + " --root ./demo-spec --json", "只读。旧实例需要显式 migrate；遇到业务定制先查看诊断。", false}
	}
	helpTopics["recover"] = helpTopic{"查询或恢复未完成的项目事务。", "--root <目录> [--apply]", projectFlags + "\n--apply  执行保护性恢复；默认只查询", "yss recover --root ./demo-spec --json\nyss recover --root ./demo-spec --apply --json", "程序安装事务使用 yss update recover --tool-root <目录>。", false}
	helpTopics["rollback"] = helpTopic{"查询或整体回退最近一次成功项目事务。", "--root <目录> [--apply]", projectFlags + "\n--apply  执行回退；默认只查询", "yss rollback --root ./demo-spec --json\nyss rollback --root ./demo-spec --apply --json", "后续用户修改会阻止覆盖。程序版本回退使用 yss update rollback。", false}
	helpTopics["version"] = helpTopic{"查看 CLI、协议和固定来源身份。", "[--json]", "也可使用 yss -V 或 yss --version", "yss version --json", "不需要项目目录。", false}
	helpTopics["capabilities"] = helpTopic{"查看原生能力、治理接口与发行证据边界。", "[--json]", "--json  输出机器可读能力清单", "yss capabilities --json", "支持清单不等于某个平台已通过原生发行验收。", false}
	helpTopics["upgrade"] = helpTopic{"从 GitHub 下载并事务安装稳定版 CLI。", "[--check] [--to <稳定版本>] [--tool-root <目录>] [--json]", "--check  只检查版本，不下载、不写入\n--to <版本>  指定稳定版，如 1.3.0 或 v1.3.0；默认最新稳定版\n--tool-root <目录>  显式工具目录；默认识别实际运行二进制的受管目录", "yss upgrade --check\nyss upgrade\nyss upgrade --to 1.3.0 --tool-root ./tools/yss --json", "固定来源 iloveZzz/yss-cli。校验失败、降级、冲突或未完成事务均拒绝。恢复: yss update recover --tool-root <目录>；回退: yss update rollback --tool-root <目录>。1.0.0 首次安装新版使用离线 update，见 yss help tutorial。", false}
	registerGroup("update", "安装、恢复或回退指定本地发行包。", "--tool-root <目录>", "--tool-root <目录>  必需；工具目录不能是项目或 Git 仓库根", "yss update status --tool-root ./tools/yss --json", "保留离线安装接口；每次写入绑定摘要和程序事务。", map[string]helpTopic{
		"plan":     {"生成离线程序安装计划（默认动作）。", "--artifact <归档> --sha256 <摘要> [--out <新文件>]", "--artifact <文件>  本机平台的 .tar.gz 或 .zip\n--sha256 <摘要>  归档 SHA-256\n--out <新文件>  保存计划到工具目录外；默认只输出计划", "yss update plan --tool-root ./tools/yss --artifact /path/yss.tar.gz --sha256 <SHA-256> --out /tmp/yss-install-plan.json --json", "归档必须来自可信固定来源。", false},
		"apply":    {"应用保存的程序安装计划。", "--plan-file <文件>", "--plan-file <文件>  必需；读取 update plan 生成的计划", "yss update apply --tool-root ./tools/yss --plan-file /tmp/yss-install-plan.json --json", "保持文件权限；用户改动及输入漂移会阻止安装。", false},
		"status":   {"只读诊断安装一致性及程序事务状态。", "[--json]", "仅查询，不写入；展示安装记录、逐文件摘要和权限差异。\ninstallationConsistent 只表示安装检查结果，不授予发行资格。", "yss update status --tool-root ./tools/yss --json", "", false},
		"recover":  {"恢复唯一未完成的程序事务。", "[--json]", "仅处理 program-update；恢复前核验受管范围", "yss update recover --tool-root ./tools/yss --json", "保护性恢复开始后完成还原，可重复执行；用户改动时停止覆盖。", false},
		"rollback": {"回退最近一次成功程序安装。", "[--json]", "仅回退最近成功 program-update", "yss update rollback --tool-root ./tools/yss --json", "后续用户改动会阻止整体回退。", false},
	})
	registerGroup("migrate", "显式迁移旧实例 metadata、受管基线及 binding。", "--root <目录>", projectFlags, "yss migrate plan --root ./old-project --out /tmp/yss-migrate-plan.json --json", "旧未完成事务先由仓外固定旧执行器恢复；原 metadata 字节进入回退材料。", map[string]helpTopic{
		"plan":     {"生成只读迁移计划（默认动作）。", "--out <新文件>", "--out <新文件>  保存迁移计划", "yss migrate plan --root ./old-project --out /tmp/yss-migrate-plan.json --json", "", false},
		"apply":    {"应用迁移计划。", "--plan-file <文件>", "--plan-file <文件>  必需；身份、基线与 binding 同一事务", "yss migrate apply --root ./old-project --plan-file /tmp/yss-migrate-plan.json --json", "", false},
		"status":   {"查询迁移事务。", "[--json]", "只读查询", "yss migrate status --root ./old-project --json", "", false},
		"recover":  {"恢复未完成迁移事务。", "[--json]", "仅消费项目迁移范围", "yss migrate recover --root ./old-project --json", "", false},
		"rollback": {"恢复迁移前实例。", "[--json]", "最近成功迁移，拒绝覆盖后续用户修改", "yss migrate rollback --root ./old-project --json", "", false},
	})
	for _, group := range []string{"skills", "assets"} {
		identifier := "Skill 名称"
		sample := "yss-research"
		meaning := "Skill 及其依赖闭包"
		if group == "assets" {
			identifier = "阶段 ID"
			sample = "stage.spec-architecture"
			meaning = "阶段资源和 Skill 闭包"
		}
		registerGroup(group, "查询或补装 "+meaning+"。", "--root <目录>", projectFlags, "yss "+group+" list --root ./demo-spec --json", "可用标识来自当前 Profile 的内置 Bundle；不猜测能力闭包。", map[string]helpTopic{
			"list":   {"列出当前 Profile 支持的标识。", "[--json]", "只读查询", "yss " + group + " list --root ./demo-spec --json", "", false},
			"ensure": {"补装指定 " + meaning + "。", "<标识...> --plan --out <新文件>", "位置参数: " + identifier + "；支持多个\n" + planFlags, "yss " + group + " ensure " + sample + " --root ./demo-spec --plan --out /tmp/yss-resource-plan.json\nyss " + group + " --root ./demo-spec --apply --plan-file /tmp/yss-resource-plan.json", "须先 list 确认该 Profile 支持相应标识。", true},
		})
	}
	helpTopics["skills list"] = helpTopic{"列出当前 Profile 支持的标识；--details 返回注册信息及安装声明。", "[--details] [--json]", "--details  不核验整个技能库；调用前使用 resolve", "yss skills list --root ./demo-spec --json\nyss skills list --details --root ./demo-spec --json", "普通 list 输出保持兼容。详情不是技能就绪证明。", false}
	helpTopics["skills resolve"] = helpTopic{"只读核验所选内置技能及命中的上下文依赖。", "<标识...> --agent-runtime codex [--when <条件列表>] [--json]", "位置参数: 已登记技能 ID 或别名；支持多个并去重\n--agent-runtime codex  必需\n--when <条件列表>  逗号分隔；只接受已登记条件", "yss skills resolve code-review codebase-design --agent-runtime codex --root ./demo-spec --json\nyss skills resolve code-review --agent-runtime codex --when lifecycle-document-output --root ./demo-spec --json", "消费 result.status：ready 才读取 entryPath；missing 在已有授权内 ensure plan/apply 后重新 resolve；blocked 停止受影响调用。查询退出 0 不授予调用、实施或批准权限。", true}
	registerGroup("bundle", "读取或导出完整固定 Bundle 与 manifest。", "--profile <Profile>", "--profile <spec|design|backend|frontend>  必需", "yss bundle inspect --profile spec --json", "无需项目或网络；公开资产接口供插件及构建消费者使用。", map[string]helpTopic{
		"inspect": {"检查 Bundle 身份、来源及摘要。", "[--json]", "不支持 --out；只读", "yss bundle inspect --profile spec --json", "", false},
		"export":  {"导出 Bundle 全部 bytes、mode 和 manifest。", "--out <新目录>", "--out <新目录>  必需且不能已存在", "yss bundle export --profile spec --out /tmp/yss-spec-bundle --json", "", false},
	})
	registerGovernanceHelp()
	registerGroup("profile", "准备独立的下游 Profile 工程并登记显式关联。", "--root <源工程>", projectFlags, "yss profile prepare --root ./design --backend-root ./backend --frontend-root ./frontend --plan --out /tmp/delivery-prepare.json", "已有普通工程使用 attach；关联回退不回退下游工程。", map[string]helpTopic{
		"prepare": {"生成或执行单端、联合初始化保存计划。", "[--design-root <目录>] [--backend-root <目录>] [--frontend-root <目录>] --plan --out <新文件>", planFlags + "\n--checkpoint <文件>  可选，绑定当前工作及来源证据", "yss profile prepare --root ./spec --design-root ./design --plan --out /tmp/design-prepare.json\nyss profile prepare --root ./design --backend-root ./backend --frontend-root ./frontend --plan --out /tmp/delivery-prepare.json\nyss profile prepare --root ./design --apply --plan-file /tmp/delivery-prepare.json", "全部目标预检后登记关联，再依次初始化；失败保留已成功工程，按原计划重试剩余步骤。初始化不授予阶段实施资格。", false},
	})
	helpTopics["handoff export"] = helpTopic{"导出当前已批准的 Spec 基线及来源证据。", "--root <Spec工程> --kind spec-baseline --checkpoint <当前检查点> --out <新目录>", projectFlags + "\n--kind spec-baseline\n--checkpoint <文件>\n--out <新目录>", "yss handoff export --root ./spec --kind spec-baseline --checkpoint .work/feature/checkpoint.yaml --out /tmp/spec-baseline", "保留源批准、业务票稳定 ID 和原始字节摘要；导出不会推进源阶段。", false}
	helpTopics["handoff import"] = helpTopic{"将批准 Spec 基线接入独立 Design。", "--root <Design工程> --kind spec-baseline --package <目录包> --plan --out <新计划>", projectFlags + "\n--kind spec-baseline\n--package <目录包>\n" + planFlags, "yss handoff import --root ./design --kind spec-baseline --package /tmp/spec-baseline --plan --out /tmp/spec-import.json\nyss handoff import --root ./design --kind spec-baseline --apply --plan-file /tmp/spec-import.json", "初始化与导入分别保存计划、分别执行事务。完成目标 Context 对账及接入核验后由主控登记当前工作。", false}
	for group, conditions := range map[string]string{
		"contract": "--kind <slice|scaffold|task|frontend-delivery>\n--file / --checkpoint  资产与独立消费期待\n--approval-ref / --unit  slice；frontend-delivery 的 --unit 限批准的前端工作单元\n--slice / --phase  仅 frontend-delivery；本地输入 --file 与 --checkpoint 指同一当前 checkpoint\n--phase <preflight|design|contract|inputs|implementation|verification>  准备阶段不授实现资格\n--history  仅 task；历史结构不授予当前放行",
		"evidence": "--kind <approval|user-decision|verification>\n--file / --checkpoint / --task  资产与独立消费期待\n--gate / --boundary / --require-approved / --history  仅 approval\n--requirements / --continuation  仅 user-decision\n--approval-ref  仅 verification",
		"handoff":  "--kind <package|consumption|spec-baseline>\n--file / --checkpoint  资产与独立消费期待\n--package  package 包或 spec-baseline 基线包；spec-baseline 的 --package 与 --file 互斥\n--consumer  仅 consumption",
	} {
		key := group + " verify"
		topic := helpTopics[key]
		topic.options = projectFlags + "\n" + conditions + "\n--home / --run-dir / --tool-root / --template-checkout  固定依赖来源"
		helpTopics[key] = topic
	}
	initializeCommands()
}

func registerGroup(group, summary, usage, options, examples, notes string, children map[string]helpTopic) {
	helpTopics[group] = helpTopic{summary, "<子命令> " + usage, options, examples, notes, false}
	for action, child := range children {
		child.usage = usage + " " + child.usage
		child.options = options + "\n" + child.options
		if child.notes == "" {
			child.notes = notes
		}
		helpTopics[group+" "+action] = child
	}
}

func registerGovernanceHelp() {
	registerGroup("context", "查询或校验唯一 CONTEXT.md 及词汇快照。", "--root <目录>", projectFlags, "yss context verify --root ./demo-spec --json", governanceNotes+"\n合法 schema v1 模板源可只读校验；check/verify 返回 context_snapshot，不创建批准。", map[string]helpTopic{
		"query":  {"查询稳定词汇。", "[--id <术语ID>]", "--id / --term-refs  选择术语\n--allowed-context-ids  限定责任区", "yss context query --root ./demo-spec --json", "", true},
		"verify": {"校验当前词汇和可选快照。", "[--snapshot <文件>]", "--snapshot / --file  待验快照\n--term-refs / --allowed-context-ids  独立消费范围", "yss context verify --root ./demo-spec --json", "", true},
		"check":  {"校验词汇结构。", "[--file <文件>]", "--file / --snapshot  可选快照", "yss context check --root ./demo-spec --json", "", true},
	})
	registerGroup("lifecycle", "查询当前生命周期、配置本次推进目标并核验门禁。", "--root <目录>", projectFlags, "yss lifecycle query --root ./demo-spec --id work-unit.entry-triage --json", governanceNotes+"\n新正式 Spec 默认推进到 business-accepted；先完成 Spec 或设计时，用 target 保存本次目标，后续仍由生命周期 Skill 推进。", map[string]helpTopic{
		"query":  {"查询注册表中的稳定 ID。", "[--id <ID>]", "--id / --work-unit / --stage  选择注册对象", "yss lifecycle query --root ./demo-spec --id work-unit.entry-triage --json", "", true},
		"status": {"只读核验 checkpoint、本次目标与显式消费者的当前证据。", "--checkpoint <文件>", "--checkpoint / --file  当前 checkpoint\nprogression  本次目标及本端职责/整体业务完成结论\ncoordination  显式同功能消费者的当前来源、接收及交付\nnext_action  承接者、工程、工作单元和等待原因", "yss lifecycle status --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --json", "查询不写入、不启动实现或业务测试。达到短目标仍保留 checkpoint.next_work_unit；next_action 不是执行授权。旧实例未启用目标政策时保留旧行为，须显式 sync 后核验能力。", true},
		"target": {
			"读取或事务设置 Spec 单功能的本次推进目标。",
			"--checkpoint <文件> [--plan --input <JSON> --out <新计划>] 或 --apply --plan-file <原计划>",
			"无 plan/apply：只读核验目标、显式交接与完成依据\n--checkpoint  Tracker/map 唯一登记的当前功能 checkpoint\n--input  项目内 JSON：schema_version、kind、feature_id、checkpoint_ref、target、intent_source、consumers\nJSON target 五选一：spec-approved / product-design-completed / backend-deliverable / frontend-accepted / business-accepted\nconsumers  本地推进用 []；独立专职端显式给 profile、绝对 root、同功能 checkpoint_ref，每种 Profile 最多一个\n--plan --out  保存新计划；输入草稿保留到 apply 完成，计划建议放工程外\n--apply --plan-file  仅消费原保存计划；不要同时传 checkpoint/input/plan/out",
			"yss lifecycle target --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --json\nyss lifecycle target --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --input .work/feature/tmp/target-input.json --plan --out /tmp/target-plan.json --json\nyss lifecycle target --root ./demo-spec --apply --plan-file /tmp/target-plan.json --json\nyss lifecycle status --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --json",
			"新正式 Spec 默认 business-accepted；plan-to-backend 的职责上限只允许前三目标。仅支持 lifecycle-target-v1 政策的 Spec 主控可写；专职只读 profile-terminal 不是可写枚举。写入仅限 progression-target.json 及必要事务记录，不改 checkpoint、批准、冻结包或 Receipt，不授 ready-for-agent。达到目标后停止本次下游写入，保留真实 next_work_unit；续推修改 JSON target/intent_source，再 plan/apply 并重验，从首个合法未完成工作单元继续。能力缺失的旧原生实例先显式 sync；帮助教程见 yss help tutorial spec。草稿使用已有忽略规则覆盖的功能 tmp 目录，不自动添加 ignore。",
			false,
		},
		"verify":       {"核验当前 checkpoint 的领域门禁。", "--checkpoint <文件>", "--checkpoint / --file  当前 checkpoint\n--history  仅历史结构，不授予当前放行\n--home / --run-dir / --tool-root / --template-checkout  独立依赖来源", "yss lifecycle verify --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --json", "", true},
		"route":        dailyHelp("只读判定日常或正式交付路径。", "route"),
		"verify-daily": dailyHelp("核验同一日常任务的当前差异、测试和独立审查。", "verify-daily"),
	})
	stage := map[string]helpTopic{
		"query": {"查询阶段或 checkpoint 中的工作项。", "[--checkpoint <文件>] [--id <ID>]", "无 checkpoint: --id / --stage  阶段 ID\n有 checkpoint: --id  工作项 ID；--work-unit / --stage 筛选尚未支持", "yss stage query --root ./demo-spec --id stage.spec-architecture --json", "", true},
	}
	for _, action := range []string{"status", "check"} {
		stage[action] = helpTopic{"读取并校验当前阶段工作项。", "--checkpoint <文件>", "--checkpoint / --file  已存在的 checkpoint", "yss stage " + action + " --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --json", governanceNotes, true}
	}
	for _, action := range []string{"register", "update", "plan"} {
		stage[action] = helpTopic{"生成阶段工作项写入计划。", "--checkpoint <文件> --items <JSON文件> [--apply --plan-file <文件>]", "--checkpoint / --file  已存在的 JSON checkpoint；YAML 仅支持只读\n--items / --item  合同规定的工作项 JSON\n默认只输出计划；保存计划必须位于项目内\n--apply --plan-file  应用已保存计划\n--refresh 尚未支持，返回 UNPORTED", "yss stage " + action + " --root ./demo-spec --checkpoint .work/feature/checkpoint.json --items docs/work-items.json > ./demo-spec/docs/stage-plan.json\nyss stage apply --root ./demo-spec --plan-file docs/stage-plan.json --json", "使用项目本地 stage-tracking 合同；计划保存时不加 --json，应用时核验输入摘要。不创建阶段批准。", true}
	}
	stage["apply"] = helpTopic{"事务应用已保存的阶段工作项计划。", "--plan-file <文件>", "--plan-file  项目内保存的原始写入计划", "yss stage apply --root ./demo-spec --plan-file docs/stage-plan.json --json", governanceNotes, true}
	registerGroup("stage", "查询、登记或更新既有阶段工作项。", "--root <目录>", projectFlags, "yss stage query --root ./demo-spec --id stage.spec-architecture --json", governanceNotes, stage)
	for _, group := range []string{"contract", "evidence", "handoff"} {
		kinds := "slice|scaffold|task|frontend-delivery"
		example := "yss contract verify --root ./demo-spec --kind scaffold --file docs/scaffold.json --json"
		if group == "evidence" {
			kinds = "approval|user-decision|verification"
			example = "yss evidence verify --root ./demo-spec --kind verification --file docs/verification.json --json"
		}
		if group == "handoff" {
			kinds = "package|consumption"
			example = "yss handoff verify --root ./demo-spec --kind package --package docs/handoff --json"
		}
		registerGroup(group, "校验 "+group+" 的结构或原生领域语义。", "--root <目录>", projectFlags, example, governanceNotes, map[string]helpTopic{
			"check":  {"执行显式 Schema 结构校验。", "--file <文件> --schema <Schema>", "--file  待验资产\n--schema  项目本地 Schema", "yss " + group + " check --root ./demo-spec --file .template-spec/process/lifecycle-registry.yaml --schema .template-spec/process/schemas/lifecycle-registry.schema.json --json", "示例文件需按对应 Schema 先创建。结构校验不替代领域 verify。", true},
			"verify": {"执行当前领域规则和独立消费者绑定校验。", "--kind <类型> [--file <文件>]", "--kind <" + kinds + ">\n--file / --package  待验文件或交接包\n--checkpoint / --task / --requirements  独立消费期待\n--approval-ref / --unit / --consumer / --gate / --boundary  当前消费选择\n--history  历史结构；--continuation  授权延续校验\n--tool-root / --template-checkout  固定依赖来源", example, "", true},
		})
	}
	ci := map[string]helpTopic{
		"check":  {"核验完整治理或显式有限 CI。", "[--scope native-go]", "--scope native-go  有限原生检查；默认完整治理\n--runtime-store off  当前完整治理支持的存储模式\n--checkpoint / --task  追加当前消费入口", "yss project-ci check --root ./demo-spec --runtime-store off --json", "有限通过不代表完整治理通过。", true},
		"verify": {"按完整 Git 基线核验 CI。", "--base <完整SHA>", "--base  实现仓完整基线 SHA\n--runtime-store off\n--checkpoint / --task  追加当前消费入口", "yss project-ci verify --root ./demo-spec --base <完整40位SHA> --runtime-store off --json", "", true},
	}
	for _, action := range []string{"install", "plan"} {
		ci[action] = helpTopic{"生成有限原生 CI 安装计划。", "--scope native-go --cli-source <路径> [--provider github]", "--scope native-go  必需\n--provider github  当前唯一支持的 provider，默认 github\n--branch  默认项目配置分支或 main\n--cli-source  必需；项目内已存在的固定 Go CLI 源码目录\n--additional-path  CI 补充路径\n默认只输出计划；--apply --plan-file 应用保存计划", "yss project-ci " + action + " --root ./demo-spec --scope native-go --cli-source vendor/yss-cli > ./demo-spec/docs/ci-plan.json\nyss project-ci apply --root ./demo-spec --scope native-go --plan-file docs/ci-plan.json --json", "先准备项目内 vendor/yss-cli 固定源码；路径示例不会自动取得源码。有限 CI 不替代完整治理；保存原始计划时不加 --json。", true}
	}
	ci["apply"] = helpTopic{"应用有限原生 CI 保存计划。", "--scope native-go --plan-file <文件>", "--scope native-go  必需\n--plan-file  项目内原始写入计划", "yss project-ci apply --root ./demo-spec --scope native-go --plan-file docs/ci-plan.json --json", governanceNotes, true}
	ci["transition"] = helpTopic{"核验工作单元流转条件。", "--scope native-go --checkpoint <文件> [--next-work-unit <ID>]", "--scope native-go  必需\n--checkpoint / --file  当前 checkpoint\n--current-work-unit / --next-work-unit  默认读取 checkpoint", "yss project-ci transition --root ./demo-spec --scope native-go --checkpoint .work/feature/checkpoint.yaml --json", "仅核验已存在的当前门禁，不创建批准、不自动推进工作单元。", true}
	registerGroup("project-ci", "核验或配置项目 CI。", "--root <目录>", projectFlags, "yss project-ci check --root ./demo-spec --runtime-store off --json", governanceNotes, ci)
	run := map[string]helpTopic{}
	for _, action := range []string{"inspect", "run", "events", "commands", "pins"} {
		usage := "--id <运行ID>"
		example := "yss runtime " + action + " --root ./demo-spec --id <运行ID> --json"
		if action == "inspect" {
			usage = "[--json]"
			example = "yss runtime inspect --root ./demo-spec --json"
		}
		run[action] = helpTopic{"只读查询运行记录。", usage, "--home  独立 SQLite 运行存储；默认用户运行存储\n--id  已登记运行 ID", example, "运行记录不授予生命周期批准或执行授权。", true}
	}
	run["begin"] = helpTopic{"创建运行记录并返回所有权 token。", "[--kind <类型>]", "--home  独立 SQLite 运行存储\n--kind  默认 command\n--input / --report-dir 尚未支持，返回 UNPORTED", "yss runtime begin --root ./demo-spec --kind command --json", "保存返回的 id/token；之后 event、complete 和 pin 操作都须提供 token。", true}
	run["event"] = helpTopic{"追加运行事件。", "--id <ID> --token <token> --type <类型> [--value <JSON>]", "--id / --token  begin 返回的所有权凭证\n--type  必需\n--value  单个 JSON 值，默认 null", "yss runtime event --root ./demo-spec --id <ID> --token <token> --type progress --value '{\"message\":\"checked\"}' --json", governanceNotes, true}
	run["complete"] = helpTopic{"以实际退出码结束运行记录。", "--id <ID> --token <token> --status <终态> --exit-code <整数>", "--id / --token  begin 返回的所有权凭证\n--status  已知终态\n--exit-code  实际整数退出码，passed 必须为 0", "yss runtime complete --root ./demo-spec --id <ID> --token <token> --status passed --exit-code 0 --json", governanceNotes, true}
	for _, action := range []string{"pin", "unpin"} {
		run[action] = helpTopic{"维护运行记录保护标记。", "--id <ID> --token <token> --reason <理由>", "--id / --token  begin 返回的所有权凭证\n--reason  必需，记录保护标记变更理由", "yss runtime " + action + " --root ./demo-spec --id <ID> --token <token> --reason '审查材料' --json", governanceNotes, true}
	}
	registerGroup("runtime", "管理独立运行记录与保护标记。", "--root <目录>", projectFlags, "yss runtime inspect --root ./demo-spec --json", governanceNotes, run)
	registerGroup("archive", "安全打包、读取或核验 ZIP 资产。", "--root <目录>", projectFlags, "yss archive verify --root ./demo-spec --file docs/package.zip --json", "拒绝路径逃逸、链接、碰撞和超限资产。", map[string]helpTopic{
		"pack":   {"打包指定项目目录。", "--source <目录> --output <文件>", "--source  输入目录\n--output  仓外绝对路径，输出 ZIP", "yss archive pack --root ./demo-spec --source docs/handoff --output /tmp/yss-handoff.zip --json", "", true},
		"unpack": {"安全解包到新位置。", "--file <ZIP> --output <目录>", "--file  输入 ZIP\n--output  仓外绝对路径，目标须不存在", "yss archive unpack --root ./demo-spec --file docs/handoff.zip --output /tmp/yss-unpacked --json", "", true},
		"verify": {"只读核验归档结构。", "--file <ZIP>", "--file / --source  待验 ZIP", "yss archive verify --root ./demo-spec --file docs/handoff.zip --json", "", true},
	})
	registerGroup("xml", "读取 Maven project XML。", "--root <目录>", projectFlags, "yss xml inspect --root ./demo-backend --file pom.xml --json", "仅支持 Maven project XML；禁止 DTD 和外部实体，不执行外部代码。", map[string]helpTopic{
		"inspect": {"读取 Maven project 结构。", "--file <文件>", "--file  项目本地 Maven XML，大小上限 16 MiB", "yss xml inspect --root ./demo-backend --file pom.xml --json", "", true},
		"query":   {"查询 Maven project 读取结果。", "--file <文件>", "--file  项目本地 Maven XML，大小上限 16 MiB", "yss xml query --root ./demo-backend --file pom.xml --json", "", true},
	})
	helpTopics["compat"] = helpTopic{"显式旧命令兼容适配。", "<旧命令> [参数]", "旧命令: create-yss-spec / create-yss-harness-design / create-yss-harness-backend / create-yss-harness-frontend", "yss compat create-yss-spec --help", "仅维护已登记兼容行为；新增用户操作优先使用原生 yss 命令。未迁移行为返回明确错误，详见 compat/README.md。", true}
	helpTopics["compat-api"] = helpTopic{"供现役 JavaScript 消费者使用的原生传输接口。", "<API标识>", "从 stdin 读取版本化请求 JSON", "yss help compat-api", "调用形状见 compat/README.md；不复刻无人消费的私有 CommonJS API。", true}
	for _, p := range []string{"spec", "design", "backend", "frontend"} {
		alias := "create-yss-harness-" + p
		if p == "spec" {
			alias = "create-yss-spec"
		}
		helpTopics["compat "+alias] = helpTopic{"固定旧别名的显式原生适配。", "--native <命令> [参数]", "--native  使用原生协议 1；其余旧成功行为可能返回 UNPORTED", "yss compat " + alias + " --native doctor --target-dir ./demo-" + p + " --json", "优先使用 yss doctor --root；兼容说明见 compat/README.md。", true}
	}
	for _, method := range []string{"native.snapshot", "native.run", "toErrorEnvelope", "projectDoctor", "projectDiff", "templatePlan", "templateApply"} {
		helpTopics["compat-api "+method] = helpTopic{"版本化兼容传输方法。", "< request.json", "stdin  单个 JSON object，上限 4 MiB", "yss compat-api " + method + " < request.json", "请求形状见 compat/README.md；旧成功方法 projectDoctor/projectDiff/templatePlan/templateApply 返回 UNPORTED。", false}
	}
}

func dailyHelp(summary, action string) helpTopic {
	return helpTopic{summary, "--task <Markdown> --implementation-root <Git根> --base <完整SHA>", "--task  同一任务的需求、验收及证据\n--implementation-root  已确认实现仓 Git 根\n--base  完整40位基线 SHA；不接受缩写", "yss lifecycle " + action + " --root ./demo-spec --task docs/daily-task.md --implementation-root /path/implementation --base <完整40位SHA> --json", "只支持已启用对应政策的 Spec 实例；其他 Profile 不支持日常路径。已有正式任务不得降级。", false}
}

const maintenanceTutorial = `YSS 离线入门教程

1. 查看程序与选择模板
   yss version --json
   yss capabilities --json
   yss init --profile spec --root ./demo-spec --project-name 演示项目
   也可选择 design、backend、frontend；在各自的新目录初始化。

2. 检查实例（只读）
   yss doctor --root ./demo-spec --json
   yss diff --root ./demo-spec --json

3. 升级项目模板（先检查计划，再应用）
   yss sync --root ./demo-spec --plan --out /tmp/yss-sync-plan.json
   yss sync --root ./demo-spec --apply --plan-file /tmp/yss-sync-plan.json
   计划文件须不存在；输入或定制冲突变化时重新生成计划。

4. 查询和补装资源
   yss skills list --root ./demo-spec --json
   yss skills ensure yss-research --root ./demo-spec --plan --out /tmp/yss-skill-plan.json
   yss skills --root ./demo-spec --apply --plan-file /tmp/yss-skill-plan.json
   yss assets list --root ./demo-spec --json
   使用 list 的实际标识；阶段资源以当前 Profile 登记为准。

5. 旧实例迁移
   yss doctor --root ./old-project --json
   yss migrate plan --root ./old-project --out /tmp/yss-migrate-plan.json --json
   yss migrate apply --root ./old-project --plan-file /tmp/yss-migrate-plan.json --json
   yss migrate rollback --root ./old-project --json
   旧未完成事务先由对应仓外固定旧执行器恢复，不能跳过恢复直接迁移。

6. 升级 CLI 程序
   yss upgrade --check
   yss upgrade
   yss upgrade --to 1.3.0 --tool-root ./tools/yss --json
   自动识别实际运行的受管工具目录；未受管裸二进制须显式选择新工具目录。

   1.0.0 没有 upgrade 命令：首次进入新版从
   https://github.com/iloveZzz/yss-cli/releases 下载本机包和 checksums.json，
   读取其中对应归档 SHA-256，再执行现有离线入口：
   yss update plan --tool-root ./tools/yss --artifact /path/yss_1.3.0_darwin_arm64.tar.gz --sha256 <SHA-256> --out /tmp/yss-install-plan.json --json
   yss update apply --tool-root ./tools/yss --plan-file /tmp/yss-install-plan.json --json
   ./tools/yss/yss version --json
   将该受管目录的 yss 链接到 PATH 后即可使用 upgrade。

7. 程序恢复和回退
   yss update status --tool-root ./tools/yss --json
   yss update recover --tool-root ./tools/yss --json
   yss update rollback --tool-root ./tools/yss --json
   自动安装用户应使用 upgrade 输出的 toolRoot，示例目录只是显式安装案例。
   后续用户修改会阻止覆盖；程序事务与项目 sync/migrate 事务分别恢复。

   出现“受管程序文件与安装清单不一致”时：
   先对错误中实际 toolRoot 执行 update status，查看 diagnostic.files 的
   expected/actual 摘要、权限以及 recordedVersion/runningVersion。
   运行版本仅在查询当前执行程序所在目录时比较；其他目录不会混用版本。
   有未完成程序事务先检查状态与归档，再执行 update recover。
   没有未完成事务但文件或版本不一致时，保留旧目录，选择不存在的新目录：
   yss upgrade --tool-root ./tools/yss-clean
   ./tools/yss-clean/yss version --json
   ./tools/yss-clean/yss update status --tool-root ./tools/yss-clean --json
   确认一致后再将 PATH 入口指向新目录的完整受管安装。
   离线安装使用第 6 步的 update plan/apply，tool-root 改为新目录。
   不要只替换 yss 文件、改写安装收据或删除旧归档；回退仍可能因后续修改而拒绝。

8. 治理入口
   yss context verify --root ./demo-spec --json
   yss lifecycle query --root ./demo-spec --id work-unit.entry-triage --json
   yss lifecycle route --help
   日常路由需已确认任务、实现仓和完整基线；机器校验不创建批准。

每个命令都有 -h / --help，例如 yss update apply -h。
本教程只输出文本；示例需由用户在适当目录显式执行。
`
