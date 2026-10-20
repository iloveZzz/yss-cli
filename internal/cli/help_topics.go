package cli

import (
	"strings"

	"github.com/iloveZzz/yss-cli/internal/gitops"
)

const projectFlags = "--root <目录>  项目根，默认当前目录；推荐显式指定\n--profile <spec|backend>  实例类型"
const upgradeFlags = "--review-out <新目录>  项目外审查材料与决议模板\n--base-bundle <路径>  离线历史 Bundle 完整材料\n--resolution-file <文件>  与 --plan --plan-file 原计划一起重新规划"
const planFlags = "--plan --out <新文件>  生成保存计划，输出文件必须不存在\n--apply --plan-file <文件>  应用保存计划；输入变化时拒绝"
const governanceNotes = "读取当前项目的治理资产；校验通过不会创建批准或授予实现、发布权限。必要资产、身份和依赖必须已存在，能力缺失返回 UNPORTED。"

// The registry is shared by root, group and leaf help. It describes public
// behavior only; command execution and its authority checks remain unchanged.
func init() {
	helpTopics["contract publish-review"] = helpTopic{"事务发布 pending task3/bundle3 审阅草案。", "--root <目录> --file <准备输入>", "--file  真实当前 checkpoint 字节、四个 pending 草案文件及可选 Plan 控制登记", "yss contract publish-review --root ./demo-spec --file pending-review.json", "仅新治理实例。输出目录须不存在；不创建批准。实际任务校验失败时由原生事务恢复，不能绕过未完成事务。", false}

	helpTopics["migrate"] = helpTopic{"显式迁移 Spec 或 Backend 的 metadata3 治理实例。", "--root <目录> [--profile spec] --plan --out <新文件>；应用：--apply --plan-file <文件>", projectFlags + "\n--review-out / --base-bundle / --resolution-file  复用同步冲突审阅与决议", "yss migrate --root ./old-spec --profile spec --plan --out /tmp/governance-migration.json\nyss migrate --root ./old-spec --profile spec --apply --plan-file /tmp/governance-migration.json", "只支持统一 metadata3 / identity1 / policy1；未知格式原件保留。封存原身份、政策和记录。旧批准通过 --history 按原语义只读；不授予新资格。未完成迁移可 recover，已完成后不支持降级。", false}

	helpTopics["contract view"] = helpTopic{"按当前 Slice 或唯一工作单元阅读；不验证批准、不授予执行权限。", "--root <目录> --kind slice --file <合同> [--view review|task|full] [--unit <ID>] [--json]", projectFlags, "yss contract view --root ./demo-spec --kind slice --file ./slice.yaml --view task --unit work-unit.slice-backend", "默认 review；文本为 Markdown，JSON 只含一份结构化内容。task 必须指定唯一工作单元。", false}
	helpTopics["attach"] = helpTopic{"首次接管已有工程的模板受管资产；使用 sync 同步模板。", "--root <目录> --profile <Profile> --plan --out <新文件>", projectFlags + "\n" + planFlags + "\n" + upgradeFlags + "\n--full  选择完整资源集合\n--binding-file <文件>  将插件 binding 纳入同一计划", "yss attach --profile backend --root ./existing-backend --plan --out /tmp/yss-attach-plan.json\nyss attach --root ./existing-backend --apply --plan-file /tmp/yss-attach-plan.json", "保留业务目录、CONTEXT.md 和用户 .github；定制冲突须先处置。", false}
	helpTopics["sync"] = helpTopic{"将项目模板升级到本 CLI 内置固定 Bundle。", "--root <目录> --plan --out <新文件>", projectFlags + "\n" + planFlags + "\n" + upgradeFlags, "yss sync --root ./demo-spec --plan --out /tmp/yss-sync-plan.json\nyss sync --root ./demo-spec --apply --plan-file /tmp/yss-sync-plan.json", "这是项目模板升级。升级 CLI 程序使用 yss upgrade；不会自动迁移旧 metadata。", false}
	for _, action := range []string{"doctor", "diff"} {
		summary := "检查项目身份、受管基线和冲突。"
		if action == "diff" {
			summary = "查看当前文件相对固定模板的差异和同步计划。"
		}
		helpTopics[action] = helpTopic{summary, "--root <目录> [--profile <Profile>] [--json]", projectFlags, "yss " + action + " --root ./demo-spec --json", "只读。遇到业务定制先查看诊断。", false}
	}
	helpTopics["recover"] = helpTopic{"查询或恢复未完成的项目事务。", "--root <目录> [--apply]", projectFlags + "\n--apply  执行保护性恢复；默认只查询", "yss recover --root ./demo-spec --json\nyss recover --root ./demo-spec --apply --json", "程序安装事务使用 yss update recover --tool-root <目录>。", false}
	helpTopics["rollback"] = helpTopic{"查询或整体回退最近一次成功项目事务。", "--root <目录> [--apply]", projectFlags + "\n--apply  执行回退；默认只查询", "yss rollback --root ./demo-spec --json\nyss rollback --root ./demo-spec --apply --json", "后续用户修改会阻止覆盖。程序版本回退使用 yss update rollback。", false}
	helpTopics["version"] = helpTopic{"查看 CLI、协议和固定来源身份。", "[--json]", "也可使用 yss -V 或 yss --version", "yss version --json", "不需要项目目录。", false}
	helpTopics["explain"] = helpTopic{"解释流程术语或生命周期稳定 ID。", "<术语或稳定ID> [--root <目录>]", "<术语或稳定ID>  例如 Slice Implementation Contract、gate.plan-approved 或 plan-approved\n--root  项目根，默认当前目录", "yss explain gate.plan-approved\nyss explain 工作单元 --json", "只读当前项目的流程术语表与生命周期注册表；业务术语见根 CONTEXT.md。最多返回 10 条。", true}
	helpTopics["check"] = helpTopic{"识别实例资产并调用对应 verify 做结构与当前校验。", "<文件>... | --changed [--base <Git引用>] [--checkpoint <文件>] [--root <目录>]", "<文件>  一个或多个项目内 YAML/JSON 资产\n--changed  工作树相对 HEAD 的改动与未跟踪文件\n--base <Git引用>  与 --changed 同用，并入 base..HEAD 的已提交改动\n--checkpoint <文件>  需要当前上下文的资产（如用户决定）使用它", "yss check --changed --root ./demo-spec\nyss check .work/feature/approval.yaml --json", "只聚合现有 verify，不新增校验语义；批准与 checkpoint 使用本地 Schema 做结构检查，execution_authorization=not-evaluated；历史读取须另行显式选择原快照。模板目录与未识别文件列为 skipped，缺上下文列为 not-evaluated，均不算通过。", true}
	helpTopics["verify"] = helpTopic{"核验一个门禁所需的当前 checkpoint 与检查依赖。", "--gate <门禁ID> --checkpoint <文件> [--root <目录>]", "--gate <门禁ID>  如 gate.plan-approved 或 plan-approved\n--checkpoint <文件>  当前 checkpoint", "yss verify --gate plan-approved --checkpoint .work/feature/checkpoint.yaml --root ./demo-spec", "依次运行 lifecycle verify 与 contract verify --kind gate-checks；只核验不批准，不授予执行。", false}
	helpTopics["mcp"] = helpTopic{"以 MCP stdio 服务提供只读治理工具。", "[--root <目录>]", "--root  固定的项目根，默认当前目录；工具参数不能改写", "yss mcp --root ./demo-spec", "工具：lifecycle_route、verify_daily、lifecycle_status、lifecycle_query、context_query、contract_view、explain、check、trace、verify_gate。全部只读，结果是与命令行相同的 JSON envelope；不暴露写入、提交或推送。客户端配置示例：{\"mcpServers\":{\"yss\":{\"command\":\"yss\",\"args\":[\"mcp\",\"--root\",\".\"]}}}", false}
	registerGroup("tracker", "把本地 Ticket 五态镜像到已链接的 GitHub Issue 标签。", "--root <目录>", "--root  项目根，默认当前目录", "yss tracker status --root ./demo-spec --json", "本地 Markdown 始终是权威；只改链接 Issue 的五态标签，不创建、关闭或评论 Issue。远端读写需要 GH_TOKEN；GITHUB_API_URL 可指向 GitHub Enterprise。", map[string]helpTopic{
		"status": {"列出本地 Ticket 的五态与链接；--fetch 读取远端标签并报告是否一致。", "[--fetch]", "--fetch  读取链接 Issue 的标签（需 GH_TOKEN）；只读", "yss tracker status --root ./demo-spec --fetch --json", "", false},
		"link":   {"在 Ticket 的 Status 行后写入 Tracker-Link；默认只预览。", "--ticket <文件> --issue <owner/repo#编号> [--apply]", "--ticket  tracker 根下的 parent-ticket.md 或 issues/*.md\n--issue  owner/repo#编号\n--apply  以当前字节为守卫事务写入；不覆盖已有不同链接", "yss tracker link --root ./demo-spec --ticket .work/feat/parent-ticket.md --issue acme/app#12 --apply --json", "", false},
		"sync":   {"把已链接 Ticket 的五态同步到 Issue 标签：先保存计划，再确认远端后应用。", "--plan [--out <新文件>] [--ticket <文件>]；应用：--apply --plan-file <文件> --confirm-remote github", "--plan / --out  读取远端并保存计划（文件须在项目外）\n--ticket  只同步一个 Ticket\n--apply --plan-file  复核本地字节与远端标签/更新时间未变后写入\n--confirm-remote github  用户确认写入 GitHub；缺少时不触达远端", "yss tracker sync --root ./demo-spec --plan --out /tmp/tracker-plan.json\nyss tracker sync --root ./demo-spec --apply --plan-file /tmp/tracker-plan.json --confirm-remote github", "需要 issue-tracker.md 的 remote_mirror: true（或主 tracker 为 github）。计划 v2 绑定 API 来源与 Ticket 范围，旧计划须重新生成；应用重建合法改动，只逐项操作五态并写后对账。失败保留部分完成项与 remote_state；unknown 时先查询对账，不自动重放。成功后在未漂移的 Ticket 写入 Tracker-Synced 行。", false},
	})
	helpTopics["trace"] = helpTopic{"汇总功能包的需求→验收→切片→验证→提交追溯矩阵与缺口。", "--feature <功能包> [--implementation-root <Git根> --since <引用> [--until <引用>]] [--html <新文件>] [--strict]", "--feature  tracker 根下的功能包名\n--implementation-root / --since / --until  读取实现仓提交，按 Ticket trailer 关联 slice_id\n--html  另写静态矩阵页（项目外的新文件）\n--strict  存在缺口时以 TRACE_GAPS 失败（退出码 1）", "yss trace --root ./demo-spec --feature apply-flow --json\nyss trace --root ./demo-spec --feature apply-flow --implementation-root /repo/app --since main~20 --html /tmp/trace.html", "只读：识别业务票、垂直切片 Ticket 与 Slice 合同，缺口包括无切片/无验证的验收、无 Ticket 或未对应切片的提交、Slice 依据摘要已变化。结论不代表批准、完成或可发布。", false}
	helpTopics["capabilities"] = helpTopic{"查看原生能力、治理接口与发行证据边界。", "[--json]", "--json  输出机器可读能力清单", "yss capabilities --json", "支持清单不等于某个平台已通过原生发行验收。", false}
	helpTopics["upgrade"] = helpTopic{"从 GitHub 下载并事务安装稳定版 CLI。", "[--check] [--to <稳定版本>] [--tool-root <目录>] [--json]", "--check  只检查版本，不下载、不写入\n--to <版本>  指定稳定版，如 1.3.0 或 v1.3.0；默认最新稳定版\n--tool-root <目录>  显式工具目录；默认识别实际运行二进制的受管目录", "yss upgrade --check\nyss upgrade\nyss upgrade --to 1.3.0 --tool-root ./tools/yss --json", "固定来源 iloveZzz/yss-cli。校验失败、降级、冲突或未完成事务均拒绝。恢复: yss update recover --tool-root <目录>；回退: yss update rollback --tool-root <目录>。1.0.0 首次安装新版使用离线 update，见 yss help tutorial。", false}
	registerGroup("update", "安装、恢复或回退指定本地发行包。", "--tool-root <目录>", "--tool-root <目录>  必需；工具目录不能是项目或 Git 仓库根", "yss update status --tool-root ./tools/yss --json", "保留离线安装接口；每次写入绑定摘要和程序事务。", map[string]helpTopic{
		"plan":     {"生成离线程序安装计划（默认动作）。", "--artifact <归档> --sha256 <摘要> [--out <新文件>]", "--artifact <文件>  本机平台的 .tar.gz 或 .zip\n--sha256 <摘要>  归档 SHA-256\n--out <新文件>  保存计划到工具目录外；默认只输出计划", "yss update plan --tool-root ./tools/yss --artifact /path/yss.tar.gz --sha256 <SHA-256> --out /tmp/yss-install-plan.json --json", "归档必须来自可信固定来源。", false},
		"apply":    {"应用保存的程序安装计划。", "--plan-file <文件>", "--plan-file <文件>  必需；读取 update plan 生成的计划", "yss update apply --tool-root ./tools/yss --plan-file /tmp/yss-install-plan.json --json", "保持文件权限；用户改动及输入漂移会阻止安装。", false},
		"status":   {"只读诊断安装一致性及程序事务状态。", "[--json]", "仅查询，不写入；展示安装记录、逐文件摘要和权限差异。\ninstallationConsistent 只表示安装检查结果，不授予发行资格。", "yss update status --tool-root ./tools/yss --json", "", false},
		"recover":  {"恢复唯一未完成的程序事务。", "[--json]", "仅处理 program-update；恢复前核验受管范围", "yss update recover --tool-root ./tools/yss --json", "已发布事务开始恢复后完成还原；未发布的完整程序准备验证空 WAL、范围和当前基线后封存为 sealed。旧完整计划可恢复，缺计划或冲突材料返回 STATE 并保留，不能靠重复执行清除。", false},
		"rollback": {"回退最近一次成功程序安装。", "[--json]", "仅回退最近成功 program-update", "yss update rollback --tool-root ./tools/yss --json", "后续用户改动会阻止整体回退。", false},
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
	registerGroup("bundle", "读取或导出完整固定 Bundle 与 manifest。", "--profile <Profile>", "--profile <spec|backend>  当前分发", "yss bundle inspect --profile spec --json", "无需项目或网络；公开资产接口供插件及构建消费者使用。", map[string]helpTopic{
		"inspect": {"检查 Bundle 身份、来源及摘要。", "[--json]", "不支持 --out；只读", "yss bundle inspect --profile spec --json", "", false},
		"export":  {"导出 Bundle 全部 bytes、mode 和 manifest。", "--out <新目录>", "--out <新目录>  必需且不能已存在", "yss bundle export --profile spec --out /tmp/yss-spec-bundle --json", "", false},
	})
	registerGovernanceHelp()
	registerGroup("profile", "准备独立的下游 Profile 工程并登记显式关联。", "--root <源工程>", projectFlags, "yss profile prepare --root ./spec --backend-root ./backend --plan --out /tmp/delivery-prepare.json", "已有普通工程使用 attach；关联回退不回退下游工程。", map[string]helpTopic{
		"prepare": {"为显式外部 Backend 生成或执行初始化保存计划。", "--backend-root <目录> --plan --out <新文件>", planFlags + "\n--checkpoint <文件>  可选，绑定当前工作及来源证据", "yss profile prepare --root ./spec --backend-root ./backend --plan --out /tmp/delivery-prepare.json\nyss profile prepare --root ./spec --apply --plan-file /tmp/delivery-prepare.json", "全部目标预检后登记关联，再依次初始化；失败保留已成功工程，按原计划重试剩余步骤。初始化不授予阶段实施资格。", false},
	})
	for group, conditions := range map[string]string{
		"contract": "--kind <slice|slice-draft|slice-execution-result|task-baseline|task-candidate|task-selection|architecture-evidence|governance-write|continuation-comparison|work-unit-user-decision|scaffold|task|openapi-validation|backend-delivery-terminal|backend-review|scaffold-decisions|slice-preflight|business-tickets|plan-review-task|plan-review-control|plan-entry|implementation-scope|checkpoint-user-decisions|checkpoint-approvals|gate-checks|scaffold-user-decision>\n--file / --checkpoint  资产与独立消费期待\n--approval-ref / --unit  slice、slice-preflight\ntask-baseline  只读计算已登记工程的实际Git基线摘要，不授予写入资格\ntask-candidate  只读计算实际当前候选，不执行命令、不授予批准或执行资格\n--task-ref <相对路径>  仅 task3 not-started/no-result 文档运输；执行和完成禁止覆盖身份\n--task <完成task3>  仅 slice-execution-result；真实结果须登记于同一完成任务\n--history  仅 task；历史结构不授予当前放行\n--mode <draft|formal> / --review-ref <路径>  仅 business-tickets；默认 draft，报告不授予批准\n--gate  仅 gate-checks；当前 state 以 --file 提供\n--allow-template  仅 openapi-validation、scaffold-decisions；允许模板记录\n--checkpoint  backend-delivery-terminal 的当前功能 checkpoint；终点引用由它推出",
		"evidence": "--kind <approval|user-decision|verification|context-reconciliation>\n--file / --checkpoint / --task  资产与独立消费期待\n--gate / --boundary / --require-approved / --history  仅 approval\n--requirements / --continuation  仅 user-decision\n--approval-ref  仅 verification",
		"handoff":  "--kind <package|consumption|backend-delivery|source|delivery|source-approval>\n--file / --checkpoint  资产与独立消费期待\n--package  package/backend-delivery 支持目录或 ZIP；delivery 使用目录\n--file  source 的 Handoff 文件或 source-approval 的绑定文件；source-approval 另需 --gate\n--source-context-snapshot  仅 source-approval；显式读取固定 source-context.snapshot.md，不按缺失文件推断\n--consumer  仅 consumption",
	} {
		key := group + " verify"
		topic := helpTopics[key]
		topic.options = projectFlags + "\n" + conditions + "\n--home / --run-dir / --tool-root / --template-checkout  固定依赖来源"
		helpTopics[key] = topic
	}
	registerScaffoldHelp()
	registerGitHelp()
	initializeArguments()
}

func registerGitHelp() {
	registerGroup("git", "查看仓库状态、基线、环境、策略与审计；按计划开分支、提交、装钩子、改策略、打本地标签。", "--root <仓库顶层>", "--root <目录>  Git 仓库顶层，默认当前目录\n--profile <Profile>  可选；存在 yss-project.yaml 时先核验身份", "yss git status --root ./demo-spec --json", "查询子命令只读。写入子命令（branch start、commit、hooks install、policy set、release tag）只在保存计划后执行：--plan --out 生成，--apply --plan-file 应用；计划绑定 HEAD、状态摘要和涉及文件的摘要，任一变化即拒绝。不访问远端、不推送。策略读取 "+gitops.PolicyRef+"，缺失时使用默认值。结果不代表可合并、可发布或阶段批准。", map[string]helpTopic{
		"status":         {"分支、上游差异、改动路径及进行中的合并/变基。", "[--task <Ticket>] [--json]", "--task <Ticket>  报告当前分支名是否包含该 Ticket", "yss git status --root ./demo-spec --task FEAT-142 --json", "", false},
		"base":           {"解析 HEAD 与默认分支的合并基线，输出完整 SHA。", "[--branch <分支>] [--json]", "--branch <分支>  默认取策略 default_branch；先本地分支，再 origin/<分支>", "yss git base --root ./impl --json", "输出的 base 可直接用于 yss lifecycle route --base。", false},
		"submodules":     {"列出子模块状态及其本地入口文件。", "[--json]", "只读；不初始化、不拉取子模块", "yss git submodules --root ./demo-spec --json", "子模块是独立实现仓，进入前先读其 AGENTS.md。", false},
		"doctor":         {"检查 Git 版本、提交身份、远端、历史深度与策略文件。", "[--json]", "status 为 ok/warn/fail；只读", "yss git doctor --root ./demo-spec --json", "", false},
		"guard":          {"事务前检查：未提交修改、进行中的 Git 操作、未完成 yss 事务。", "[--json]", "就绪返回 status=clear；否则以 CONFLICT 失败并附明细", "yss git guard --root ./demo-spec --json", "只报告，不暂存、不还原、不清理任何文件。", false},
		"policy":         {"显示生效的 Git 策略；policy set 经计划修改一个字段。", "[set --key <字段> --value <值> --plan --out <新文件>] [--json]", "无子命令时只读。字段：default_branch、protected_branches、branch_pattern、ticket_trailer、ticket_pattern、review_trailer、require_signed\n--key / --value  set：字符串可直接写，列表用 JSON 数组，布尔写 true/false\n" + planFlags, "yss git policy --root ./demo-spec --json\nyss git policy set --root ./demo-spec --key protected_branches --value '[\"main\",\"release\"]' --plan --out /tmp/policy-plan.json\nyss git policy set --root ./demo-spec --apply --plan-file /tmp/policy-plan.json", "未知字段或非法正则会被拒绝，不回退默认值。set 整文件按固定格式重写，原注释不保留。", true},
		"branch":         {"branch start：按 Ticket 生成符合策略的分支名并切换。", "start --task <Ticket> --slug <短名> [--type <类型>] [--from <引用>] --plan --out <新文件>", "--task <Ticket>  须符合 ticket_pattern\n--slug <短名>  小写字母、数字、连字符\n--type <类型>  默认 feat\n--from <引用>  起点，默认策略默认分支\n" + planFlags, "yss git branch start --root ./demo-spec --task FEAT-142 --slug order-refund --plan --out /tmp/branch-plan.json\nyss git branch start --root ./demo-spec --apply --plan-file /tmp/branch-plan.json", "分支名为 <类型>/<Ticket>-<短名>。结果的 base 可用于 yss lifecycle route --base；未提交改动随工作区保留。", true},
		"commit":         {"只提交明确列出的路径，并追加 Ticket trailer。", "--task <Ticket> --message <主题> --paths <路径,路径…> --plan --out <新文件>", "--task <Ticket>  须符合 ticket_pattern\n--message <主题>  单行，1–200 字\n--paths <路径,…>  仓库相对路径，逗号分隔\n" + planFlags, "yss git commit --root ./demo-spec --task FEAT-142 --message \"支持部分退款\" --paths src/refund,docs/refund.md --plan --out /tmp/commit-plan.json\nyss git commit --root ./demo-spec --apply --plan-file /tmp/commit-plan.json", "范围外改动列入计划的 excluded，不暂存、不提交。拒绝：受保护分支、分离 HEAD、进行中的合并/变基、未解决冲突。计划文件须保存在仓库之外。", false},
		"hooks":          {"hooks install：安装 commit-msg 钩子并设置本地 core.hooksPath。", "install --plan --out <新文件>", planFlags, "yss git hooks install --root ./demo-spec --plan --out /tmp/hooks-plan.json\nyss git hooks install --root ./demo-spec --apply --plan-file /tmp/hooks-plan.json", "脚本写入 .githooks/（纳入版本控制）；原 core.hooksPath 或 .git/hooks/commit-msg 先被链式调用；已有非 yss 的 .githooks/commit-msg 不覆盖。找不到 yss 时钩子跳过检查。其他克隆需各自安装。", true},
		"release":        {"release tag：在默认分支的干净 HEAD 上创建本地带注释标签。", "tag --tag-version <版本> [--evidence-file <路径>] --plan --out <新文件>", "--tag-version <版本>  如 2.2.0，标签为 v2.2.0\n--evidence-file <路径>  可选，已纳入版本控制的文件，摘要写入标签信息\n" + planFlags, "yss git release tag --root ./demo-spec --tag-version 2.2.0 --plan --out /tmp/tag-plan.json\nyss git release tag --root ./demo-spec --apply --plan-file /tmp/tag-plan.json", "只创建本地标签，不推送。要求当前在 default_branch、工作区干净；require_signed 时使用签名标签。", true},
		"push":           {"推送当前分支或一个本地标签；需保存计划并由用户确认远端。", "--remote <远端名> [--tag <标签>] --plan --out <新文件>；应用： --apply --plan-file <文件> --confirm-remote <远端名>", "--remote <远端名>  必需\n--tag <标签>  改推本地带注释标签 vX.Y.Z，而不是当前分支\n--confirm-remote <远端名>  仅 --apply，须与计划中的远端同名\n" + planFlags, "yss git push --root ./demo-spec --remote origin --plan --out /tmp/push-plan.json\nyss git push --root ./demo-spec --apply --plan-file /tmp/push-plan.json --confirm-remote origin", "计划阶段不联网：按本地远端跟踪引用审计待推送提交（ticket、自审、签名），受保护分支、分离 HEAD、已分叉或落后一律拒绝。不使用 --force，远端拒绝时原样报告。没有 --confirm-remote 时不触达远端。", false},
		"verify-message": {"校验提交信息文件的 Ticket trailer（commit-msg 钩子调用）。", "--file <提交信息文件> [--json]", "--file <文件>  Git 传给钩子的提交信息文件", "yss git verify-message --root ./demo-spec --file .git/COMMIT_EDITMSG", "合并、Revert、fixup!/squash! 提交跳过检查。", false},
		"audit":          {"按策略审计提交范围：Ticket trailer、自审、签名、保护分支直接提交。", "--since <引用> [--until <引用>] [--json]", "--since <引用>  必需，起始（不含）\n--until <引用>  默认 HEAD", "yss git audit --root ./demo-spec --since v2.1.0 --json", "有发现时 status=attention，退出码仍为 0。require_signed 为 true 时才调用 gpg 读取签名。", false},
		"changelog":      {"按 Ticket 汇总提交范围内的变更。", "--since <引用> [--until <引用>] [--json]", "--since <引用>  必需，起始（不含）\n--until <引用>  默认 HEAD", "yss git changelog --root ./demo-spec --since v2.1.0 --json", "无 Ticket 的提交归入 unlinked。", false},
	})
	// The action word reads better before the shared --root.
	for key, action := range map[string]string{"git branch": "start", "git hooks": "install", "git release": "tag", "git policy": "set"} {
		topic := helpTopics[key]
		topic.usage = strings.Replace(topic.usage, "--root <仓库顶层> "+action, action+" --root <仓库顶层>", 1)
		helpTopics[key] = topic
	}
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
			"无 plan/apply：只读核验目标、显式交接与完成依据\n--checkpoint  Tracker/map 唯一登记的当前功能 checkpoint\n--input  项目内 JSON：schema_version、kind、feature_id、checkpoint_ref、target、intent_source、consumers\nJSON target 四选一：spec-approved / product-design-completed / backend-deliverable / business-accepted\nconsumers  本地推进用 []；独立专职端显式给 profile、绝对 root、同功能 checkpoint_ref，每种 Profile 最多一个\n--plan --out  保存新计划；输入草稿保留到 apply 完成，计划建议放工程外\n--apply --plan-file  仅消费原保存计划；不要同时传 checkpoint/input/plan/out",
			"yss lifecycle target --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --json\nyss lifecycle target --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --input .work/feature/tmp/target-input.json --plan --out /tmp/target-plan.json --json\nyss lifecycle target --root ./demo-spec --apply --plan-file /tmp/target-plan.json --json\nyss lifecycle status --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --json",
			"新正式 Spec 默认 business-accepted；plan-to-backend 的职责上限只允许前三目标。仅支持 lifecycle-target-v1 政策的 Spec 主控可写；专职只读 profile-terminal 不是可写枚举。写入仅限 progression-target.json 及必要事务记录，不改 checkpoint、批准、冻结包或 Receipt，不授 ready-for-agent。达到目标后停止本次下游写入，保留真实 next_work_unit；续推修改 JSON target/intent_source，再 plan/apply 并重验，从首个合法未完成工作单元继续。能力缺失的旧原生实例先显式 sync；帮助教程见 yss help tutorial spec。草稿使用已有忽略规则覆盖的功能 tmp 目录，不自动添加 ignore。",
			false,
		},
		"verify":       {"核验当前 checkpoint 的领域门禁。", "--checkpoint <文件>", "--checkpoint / --file  当前 checkpoint\n--history  仅历史结构，不授予当前放行\n--home / --run-dir / --tool-root / --template-checkout  独立依赖来源", "yss lifecycle verify --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --json", "", true},
		"route":        dailyHelp("只读判定日常或正式交付路径。", "route"),
		"verify-daily": dailyHelp("核验同一日常任务的当前差异、测试和适用复核。", "verify-daily"),
	})
	stage := map[string]helpTopic{
		"query": {"查询阶段或 checkpoint 中的工作项。", "[--checkpoint <文件>] [--id <ID>]", "无 checkpoint: --id / --stage  阶段 ID\n有 checkpoint: --id  工作项 ID；--stage / --work-unit 筛选工作项（结果 items）", "yss stage query --root ./demo-spec --id stage.spec-architecture --json", "", true},
	}
	for _, action := range []string{"status", "check"} {
		stage[action] = helpTopic{"读取并校验当前阶段工作项。", "--checkpoint <文件>", "--checkpoint / --file  已存在的 checkpoint", "yss stage " + action + " --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --json", governanceNotes, true}
	}
	for _, action := range []string{"register", "update", "plan"} {
		stage[action] = helpTopic{"生成阶段工作项写入计划。", "--checkpoint <文件> --items <JSON文件> [--apply --plan-file <文件>]", "--checkpoint / --file  已存在的 JSON checkpoint；YAML 仅支持只读\n--items / --item  合同规定的工作项 JSON\n默认只输出计划；保存计划必须位于项目内\n--apply --plan-file  应用已保存计划\n--refresh  漂移项（沿依赖传递）除 cancelled 外重置为 pending 并标记 recheck_required，旧绑定保留为历史；可不带 --items", "yss stage " + action + " --root ./demo-spec --checkpoint .work/feature/checkpoint.json --items docs/work-items.json > ./demo-spec/docs/stage-plan.json\nyss stage apply --root ./demo-spec --plan-file docs/stage-plan.json --json", "使用项目本地 stage-tracking 合同；计划保存时不加 --json，应用时核验输入摘要。不创建阶段批准。", true}
	}
	stage["apply"] = helpTopic{"事务应用已保存的阶段工作项计划。", "--plan-file <文件>", "--plan-file  项目内保存的原始写入计划", "yss stage apply --root ./demo-spec --plan-file docs/stage-plan.json --json", governanceNotes + "\n提交后若该 checkpoint 已启用 managed 阅读视图，会立即渲染并在结果 reading_update 中报告（unchanged/rendered/failed；未启用为 manual）；渲染失败不回滚已提交的阶段写入，按 recovery 提示运行 yss reading render。", true}
	registerGroup("stage", "查询、登记或更新既有阶段工作项。", "--root <目录>", projectFlags, "yss stage query --root ./demo-spec --id stage.spec-architecture --json", governanceNotes, stage)
	registerGroup("reading", "只读核验或查询 managed 阅读视图。", "--root <目录>", projectFlags, "yss reading check-views --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --json", governanceNotes+"\n阅读视图是来源快照，不授予执行权限；check-views / affected / plan-enable 只读；render / apply-enable 经事务写入阅读目录、清单与 map.md 的受管区。", map[string]helpTopic{
		"check-views":  {"只读核验 checkpoint 的阅读视图是否与当前来源一致。", "--checkpoint <文件> [--required]", "--checkpoint  已登记的 checkpoint\n--required  即使策略未启用也按 managed 要求核验", "yss reading check-views --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --json", "status 为 manual（未启用）、current 或 stale；stale 以退出码 1 返回，结果含 blockers 与恢复提示。", false},
		"render":       {"渲染 managed 阅读视图：输出文件、清单与 map.md 的导航受管区。", "--checkpoint <文件> [--dry-run] 或 --all [--dry-run]", "--checkpoint  已登记的 checkpoint\n--all  渲染策略登记的全部 checkpoint，每个一个事务\n--dry-run  只返回将写的文件与摘要，不写任何文件", "yss reading render --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --dry-run --json", "写入经事务层完成并在提交后重新核验，失败整体回滚，可用 yss recover/rollback 处理中断。来源有阻断诊断、输出或导航被人手改、清单无效或旧版日志残留时拒绝且不写任何文件。清单版本由实例的清单 schema 决定：接受 v2 时写 v2（renderer 为 yss-reading 规则版本，不含 Node 渲染器闭包），否则写与 Node 逐字节相同的 v1；已有 v2 清单不会降级。", false},
		"plan-enable":  {"规划启用 managed 阅读视图：新的阅读策略与首次渲染的预览。", "--checkpoint <文件> [--output <新文件>]", "--checkpoint  待启用的 checkpoint\n--output  把计划保存为项目内新文件；不保存则只输出到结果", "yss reading plan-enable --root ./demo-spec --checkpoint .work/feature/checkpoint.yaml --output .work/feature/tmp/reading-enable.json --json", "只读。计划含 policy_after、将写的全部文件（operations）与 preview，并绑定当前输入摘要；不写任何文件。旧版 Node 计划不能由 apply-enable 应用，须重新生成。", false},
		"apply-enable": {"应用已保存的启用计划：同一事务写入阅读策略与全部阅读文件。", "<计划文件>", "计划文件  项目内由 plan-enable 保存的 JSON", "yss reading apply-enable --root ./demo-spec .work/feature/tmp/reading-enable.json --json", "写入前重新规划并要求计划摘要一致（READING_ENABLE_PLAN_STALE 时重新 plan-enable）；策略与渲染文件同一事务，提交后重新核验，失败整体回滚。已应用且视图当前一致时返回 unchanged。", true},
		"affected":     {"列出受某个资产变化影响的 managed checkpoint。", "--asset <路径> --kind <domain-strategy|stage-decision-package|checkpoint|tracking-migration>", "--asset  变化的资产路径\n--kind  资产类型；其他类型返回空集", "yss reading affected --root ./demo-spec --asset .work/feature/plan/domain-strategy.yaml --kind domain-strategy --json", "未启用 managed 策略时返回空集。", false},
	})
	for _, group := range []string{"contract", "evidence", "handoff"} {
		kinds := "slice|slice-draft|slice-execution-result|task-baseline|task-candidate|task-selection|architecture-evidence|governance-write|continuation-comparison|work-unit-user-decision|scaffold|task|openapi-validation|backend-delivery-terminal|backend-review|scaffold-decisions|slice-preflight|business-tickets|plan-review-task|plan-review-control|plan-entry|implementation-scope|checkpoint-user-decisions|checkpoint-approvals|gate-checks|scaffold-user-decision"
		example := "yss contract verify --root ./demo-spec --kind scaffold --file docs/scaffold.json --json"
		if group == "evidence" {
			kinds = "approval|user-decision|verification|context-reconciliation"
			example = "yss evidence verify --root ./demo-spec --kind verification --file docs/verification.json --json"
		}
		if group == "handoff" {
			kinds = "package|consumption|backend-delivery|source|delivery|source-approval"
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
		ci[action] = helpTopic{"生成 CI 安装计划：默认完整治理，--scope native-go 为有限检查。", "--cli-source <路径> [--scope native-go] [--provider github]", "--scope native-go  只装有限检查；省略时安装与默认 project-ci check 相同的完整治理检查\n--provider github  当前唯一支持的 provider，默认 github\n--branch  默认项目配置分支或 main\n--cli-source  必需；项目内已存在的固定 Go CLI 源码目录\n--additional-path  CI 补充路径\n默认只输出计划；--apply --plan-file 应用保存计划", "yss project-ci " + action + " --root ./demo-spec --cli-source vendor/yss-cli > ./demo-spec/docs/ci-plan.json\nyss project-ci apply --root ./demo-spec --plan-file docs/ci-plan.json --json", "先准备项目内 vendor/yss-cli 固定源码；路径示例不会自动取得源码。有限 CI（--scope native-go）不替代完整治理；应用时 --scope 须与计划一致；保存原始计划时不加 --json。", true}
	}
	ci["apply"] = helpTopic{"应用保存的 CI 安装计划。", "--plan-file <文件> [--scope native-go]", "--plan-file  项目内原始写入计划\n--scope native-go  计划为有限检查时必需；须与计划范围一致", "yss project-ci apply --root ./demo-spec --plan-file docs/ci-plan.json --json", governanceNotes, true}
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
	run["begin"] = helpTopic{"创建运行记录并返回所有权 token。", "[--kind <类型>] [--input <值>] [--report-dir <仓外绝对目录>]", "--home  独立 SQLite 运行存储\n--kind  默认 command\n--input  原字节存为共享 gzip 对象并登记引用（≤16 MiB），结果回显 input_digest\n--report-dir  正式报告目录，须为仓外绝对路径且无符号链接，以 report-dir:<路径> 保护", "yss runtime begin --root ./demo-spec --kind command --json", "保存返回的 id/token；之后 event、complete 和 pin 操作都须提供 token。输入对象完整写入、校验与文件同步后原子发布，损坏的已有对象保留并报 RUNTIME_INTEGRITY。", true}
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
}

func dailyHelp(summary, action string) helpTopic {
	topic := helpTopic{summary, "--task <Markdown> --implementation-root <Git根> --base <SHA或引用>", "--task  同一任务的需求、验收及证据\n--implementation-root  已确认实现仓 Git 根\n--base  完整40位 SHA，或实现仓内可解析的引用（如 HEAD、main）；结果回显 base_ref 与解析后的 base_sha，记录里的 baseline_sha 仍写完整 SHA", "yss lifecycle " + action + " --root ./demo-spec --task docs/daily-task.md --implementation-root /path/implementation --base HEAD --json", "只支持对应政策已启用且能力可核验的 Profile 实例；未启用 Profile 或旧 CLI 不支持日常路径。政策启用 standard 时，记录可声明 delivery_path: standard（加一页 Spec-lite 与一次生物人决定），verify-daily 同样核验；已有正式任务不得降级。", false}
	if action == "route" {
		topic.usage += " [--prepare-inputs]"
		topic.options += "\n--prepare-inputs  只读准备 skills、inputs 及 Backend standalone 输入摘要；仅无 API 变化的 daily，不能与 --view 同用；不更新测试、审查或批准"
	}
	return topic
}

const maintenanceTutorial = `YSS 离线入门教程

1. 查看程序与选择模板
   yss version --json
   yss capabilities --json
   yss init --profile spec --root ./demo-spec --project-name 演示项目
   也可选择 backend；在各自的新目录初始化。

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

5. 升级 CLI 程序
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
   后续用户修改会阻止覆盖；程序事务与项目 sync 事务分别恢复。

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
