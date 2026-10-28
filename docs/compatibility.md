# 兼容与验收边界

呈现接口：终端默认中文，非终端维持原结果；`--human` 强制中文，`--json` 保持版本 1 envelope 和领域 result，二者互斥。`--diagnostics` 必须同 `--json` 使用，仅在失败时添加可选顶层 `diagnostic`（schemaVersion=1）。新呈现参数由入口消费，不传给治理校验或事务执行器。历史别名保留冻结协议；帮助优先处理并始终输出离线文本。

程序版本与协议见实际二进制来源；本地候选不代表正式发行。当前 CLI 的固定模板使用 Bundle5 / metadata5，项目保存计划为 v4，仅支持 Spec/Backend。新建默认 `skills`，完整治理显式选择 `governed`；治理安装采用 identity2 / Profile3 / governance2。旧项目保存计划必须重新生成。SourceLock5 构建两个 Profile；历史 SourceLock2/3/4 仅描述原版本来源，不作为当前生产输入。旧统一 metadata3 / identity1 实例可读，task/stage 新写入需要显式 `migrate`；Design、Frontend、旧 dev 及退役产品设计插件输入明确拒绝，不提供转为 Spec 的迁移。旧家族 metadata、更早或未知格式不猜测迁移，文件保持原样。

当前 CLI 的部分离线帮助及生成参考仍有旧 Design 转 Spec、默认初始化后直接进入治理的提示。当前 Design 输入不受支持；治理命令示例应先选择 `governed`。这些旧提示不能扩大本页声明的迁移范围。

新版 Bundle 的 `skills` 安装包含已选技能引用所需的 `.template-spec`、`.template-source` 文件及其依赖，这些文件本身不启用治理身份或阶段能力；已有实例经审阅并应用 `sync` 计划补齐受管资源。

`attach` 仅接管没有 YSS metadata 的已有工程；原生实例返回 `SYNC_REQUIRED`，旧 CLI 实例返回 `MIGRATION_REQUIRED`。`attach`、`sync`、`migrate --plan` 共用完整资产规划；显式 `--base-bundle` 读取离线材料，`--review-out` 导出候选，`--resolution-file` 结合原保存计划重建决议。三方候选须经过决议；固定来源和生成资产不接受任意合并。应用回执分别给出 `fileApplication` 与 `verification`；原生校验失败整体还原。现有 Context、Tracker、业务与批准资产保留。

迁移规则的适用来源二选一：精确来源（模板 commit 与快照摘要），或模板版本区间（`from.versions`，`min` 含、`max` 不含，语义化版本）。实例的模板版本只读取 metadata 中记录的语义化 `templateVersion`；缺失或不是合法语义化版本时，版本区间规则不适用，计划的 `notices` 如实报告，不从提交或旧版本对照表推测。规则提议的 `delete` 在计划里带 `resolutionOptions`：用 `--resolution-file` 选择 `keep-local` 即拒绝删除，文件原样保留、升级不失败，同一条规则下登记过的保留决定在之后的计划里继续生效；不选则按计划删除。`--review-out` 的 `conflicts.json` 一并列出这些删除项。

Bundle 生产器按固定 Git 对象取源；`native-upgrade-v3` 兼容生成器使用 CLI 中已审查的输入，为旧快照提供 v3 metadata 读取脚本和升级指引。Bundle4 及后续版本保留固定模板的 Skill 与升级协议说明，不用历史内嵌文案覆盖；必要 metadata 消费脚本转换继续执行。Bundle1/2/3 保持原说明兼容转换。`nativeTransforms` 记录源字节与输出摘要，归档保留两者；原模板提交及快照摘要仍指向旧固定来源，生成器来源另由 producer 记录。当前工作树 producer 只能交付 implementation-ready，不能作为 committed-source 发行证据。

已实现的原生范围：Profile 身份识别、离线快照、初始化、只读差异、接管/同步/迁移计划、计划应用、文件事务、恢复与整体回滚、Skill/阶段资产补装基础、Context 校验与摘要、注册表查询、Schema结构检查、安全ZIP/XML以及SQLite运行记录。

Context 的 check/verify 对合法 schema v1 template-source 支持只读校验并返回可复用 context_snapshot；实例身份检测保持原边界，模板源携带实例 metadata 会被拒绝。当前 Spec/Backend 的 JS 消费者只负责固定二进制传输和 envelope/摘要校验；历史 Design 由匹配的旧执行器按原协议读取。

当前治理 verify、完整 project-ci、Stage 写入、离线程序安装与 JS native transport 的边界见 porting-status.md。两个插件迁至固定二进制与公开 Bundle 接口；必要 Node/Python 治理工具继续独立维护。旧成功 JSON、私有 JS API 和停用参数仅按实际消费者迁移，不全量仿制；停用项明确拒绝并提供替代命令。独立 Go Bundle 生产不再调用旧 Node 私有导出模块。

Schema：默认2020-12与format断言、本地闭包引用、禁止网络检索、JSON重复键、YAML重复键/alias/多文档拒绝。现实Schema的Python正则字符类使用固定Unicode15兼容区间；未支持的高级语法返回SCHEMA_REGEX_INCOMPATIBLE，不静默放宽。当前Python基线缺可选date-time检查器，Go会拒绝其原本误接受的无时区时间；这是显式严格化，尚不能标为完全等价。JSON/YAML非法UTF和不成对surrogate被拒绝。

恢复：原字节、类型、权限与计划摘要持久保存；WAL先于目标写入。后续用户修改导致恢复或整体回滚拒绝，避免覆盖。Git index、业务目录和嵌套Git仓库不属于模板写范围。原有CLI的历史事务格式不能由新事务格式冒充；历史中断首先走旧CLI恢复。

可支持的平台 CI 使用GitHub官方提供的runner标签，见[官方列表](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)。流水线准备好不等于已经执行；本地交叉编译不能替代原生运行、取消、权限和恢复验证。

稳定版条件按 [退役合同](cli-retirement.md) 验收：必要能力和现役消费者闭合、本次明确发行平台实际运行通过、固定提交上本次受影响的 CLI、插件和安装契约通过。每份发行资产的 stableReady 结论由其发行 manifest、本次发行平台原生收据和明示范围的门禁摘要共同给出；源码版本和交叉编译不能代替这些证据。提交、推送、tag 和发布分别按授权执行。

2.1.0 的发行目标为 darwin/arm64 与 windows/amd64。macOS 归档与目标须位于同一支持原子克隆的 APFS 卷。macOS 从已校验的归档文件描述符原子克隆完整临时文件，再设置权限与同步；目标存在、来源或目标链接及摘要变化仍拒绝，安装文件与归档不共享可修改的 inode。不支持克隆或跨卷返回 UNPORTED，不退回普通复制；无法据此保证不受支持文件系统上的旧事务可恢复。旧版本已留下的未知或零字节事务临时文件仍保留并阻断。按本次明确授权，Mac ARM64 保留实际原生、安装与恢复验收；Windows x64 仅交付同源构建包及独立摘要，不执行该平台运行验收。Windows 包标记 stableReady=false、nativeRuntimeVerified=false，下载核验后解压使用；现有在线升级资格检查保持，Windows 不列入本次 requiredPlatforms。Linux 不在本版本发行目标内。

本次资格范围为 local-platform-impacted-consumers；无关全仓回归及可复用重复检查按用户调整裁剪，未执行项不作完整模板通过声明。


## Cobra 命令层

命令、参数、帮助及补全从每次调用新建的 Cobra 命令树产生。版本、JSON envelope（outputVersion=1、protocolVersion=1）、退出码、中文终端与管道呈现、MCP 白名单和保存计划保护继续沿用原合同。严格布尔仅接受小写 true/false；semantic、scaffold、upgrade 拒绝重复参数，其他命令保留后值覆盖。未提供、显式空值与 false 继续分别处理。

新增 `--` 结束选项解析。例如 `yss check --root <项目> -- -example.yaml`；终止符后的 `--help`、`--json`、`-V` 都是位置参数，不能成为新子命令，也不放宽数量和路径范围。Invoke 将其强制 JSON 选项插在终止符之前；MCP 输入仍拒绝以短横线开头的值。

`yss completion bash|zsh|fish|powershell` 向 stdout 生成脚本，安装位置与 Shell 配置由用户选择；CLI 不自动安装。Bash 的交互补全需要 bash-completion 提供 `_get_comp_words_by_ref` 等辅助；脚本生成不自动安装这些依赖。补全提供命令、选项、静态枚举和路径类型，不读取 Ticket、项目业务资产或网络。普通值默认禁用文件回退。帮助、补全脚本与隐藏补全协议仍输出原始文本/协议，不附加业务 envelope。

四种脚本可生成不等于四种 Shell 都已运行验证；实际平台与执行结果见当前候选记录。首期测试继续使用 Go 标准库，不引入 Testify。

治理版本边界：

| 组合 | 当前行为 |
| --- | --- |
| Bundle5 + metadata5 + 项目保存计划 v4 | 当前新建 Spec/Backend；默认 skills，显式 governed 启用完整治理；治理内按适用检查与真实自检执行，独立复核取决于当前要求 |
| Bundle4 + metadata4 + identity2 + Profile3 + governance2 | 历史治理安装；sync 保持原安装方式，转换安装方式须显式 migrate |
| metadata3 + identity1 + 原 Profile/政策 | 原语义读取；显式 migrate 保存计划、审阅冲突后事务应用，不直接创建旧 task/stage |
| Design4.5 multipart container2 | 原 role policy；仅确切旧 Design 身份且无 governance_versions 时识别，不等于新治理2 |
| 迁移后的旧记录 | --history 显式选择 metadata 绑定的完整原快照；当前读取不回退、不重写批准 |
| 新政策 Handoff5 / package2 | 显式 context2、approval3 和任务来源快照；重算 Git 对象、基线、候选与执行事实，源仓不可用时仍只读验包；目标先对账，不授予实现资格 |
| 原政策 Handoff4 / package1 / Receipt2 | 按包内原签署政策读取；实际 Receipt2 在显式迁移后保持原字节并重读 |
| 历史 task1 / task2 | task1 的 --history 保留原模板维护限制；task2 只读分诊按原政策核验，issued 不等于已执行或已完成 |
| 缺失或漂移的原政策快照 | 明确拒绝 --history，不回退到当前政策或当前批准资格 |
| 已完成治理迁移的 downgrade | DOWNGRADE_UNSUPPORTED；原件和新记录保留，未完成迁移使用原事务 recover |
| 旧 CLI + 新身份 | 拒绝不支持的 schema/字段；以实际退出和文件未变作为错配证据 |

新实例不分发岗位/数字人注册表；真实用户决定、当前合同范围、原始摘要、实际执行和未知外部规则边界继续生效。旧 SourceLock、原始政策和审批字节只作为原协议输入，不用于新资格。程序升级、项目迁移及发布分别授权。

## 独立技能安装（新安装合同）

Bundle5 / metadata5 / plan4 支持一次性的 `skills` 或 `governed` 安装选择。新建默认 skills；Spec 默认包含 life 路由、澄清、研究、领域、规格草稿、中文写作、审查与显式实现入口，Backend 默认开发与审查入口。具体集合由固定 Bundle 政策决定，to-spec 和 implement 保持用户显式调用。独立 ensure 仅选择技能内闭包；条件依赖继续先 resolve --when，再显式 ensure missing 集合，不新增 ensure --when。已有独立项目仅在显式 sync 保存/应用计划时采纳目标默认，程序升级本身不写项目；文件、元数据、锁和有效投影同时更新，旧锁或运行时异常仍拒绝。旧来源 Bundle3/4 保持 governed，不能凭新参数假定旧来源已经包含普通入口。

```sh
yss init --root /path/to/new-project --profile spec --installation skills --plan --out /outside/project/init-plan.json --json
yss init --root /path/to/new-project --apply --plan-file /outside/project/init-plan.json --json
```

`init`、`attach` 可选择安装方式，`migrate --installation skills|governed` 显式转换已有 metadata4/5 实例；未给选择的 migrate 保留旧治理迁移语义。`sync` 保持现有安装方式，不接受该参数。应用只消费保存计划，不能另加 installation 改写选择。正式 `profile prepare` 继续生成 governed 目标。`--full` 只扩展当前安装方式的技能，不切换是否治理。

安装诊断分别呈现文件对齐、已安装技能完整性和工作流验收。`doctor.readiness.skills` 只核验当前 Spec metadata5 已声明的技能及运行时，`workflow` 保持 `not-evaluated`；其他 Profile / 历史代际不新增技能核验义务。`skills resolve` 按 `result.status` 判断 ready / missing / blocked，退出 0 只表示查询完成；Claude 与 WorkBuddy 的投影检查不证明宿主已发现技能。

普通技能由宿主按 SKILL.md 名称与描述发现，不要求先 route、resolve-ready、tracker setup 或创建 checkpoint。`skills resolve` 仍是原受管就绪验证，不是通用本地技能发现器。正式操作在 skills 工程返回未启用治理；真实工程测试、授权范围、分发完整性与写入冲突检查保留。

迁移计划保留原任务、批准、checkpoint 及用户文件；活动或无法核验的正式绑定拒绝转换。规划后输入变化需要重新规划。该安装合同已进入 CLI 2.7.3 的固定发行 Bundle；已有项目仍以自身 metadata、安装方式和已应用计划为准，升级程序不会自动转换项目。
