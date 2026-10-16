# 治理收敛清单：从「Go 镜像 + 绑定 Node 摘要」到「Go 为准」

本文是阶段一（仅 CLI 仓库）的盘点与分批计划。决策已定：**以 Go 实现为治理规则的唯一事实源**；模板仓的 Node 校验脚本将来变薄、改为调用 `yss`（需要改模板仓，属于阶段二，不在本阶段实施）。本文不改变任何规则，只记录现状、依据与后续批次。

- 盘点基线：本仓 `main` 的 `0d8a752`（程序 2.3.1）。文中的文件路径、行号均指该提交；后续批次改动会让行号漂移，以符号和文件名为准。
- 模板对照：固定提交 `981f7399ef6c77b515045a588e708be70af5fab3`（`docs/source-lock.json` 的 spec Profile，v4.4.1）。对照方式是只读 `git archive` 到仓外临时目录，不修改模板仓。
- 「未验证」表示本次没有用黑盒夹具或对拍确认，只是读代码得到的判断；不能当作结论使用。
- 本文不属于发行包固定文档集合，可随开发修改；`README.md`、`docs/source-lock.json`、`docs/compatibility.md`、`docs/porting-status.md`、`docs/native-governance.md`、`docs/cli-retirement.md`、`compat/README.md` 本阶段不动。

## 1. 摘要

| 主题 | 结论 |
|---|---|
| 固定 SHA-256 绑定 | 共 9 个摘要，落在 7 个 Node 文件、3 处 Go 代码。其中 2 个（研究校验器、竞品比较模块）计划在批 1 改为语义校验；其余 7 个属于阅读渲染闭包，需要先做渲染对拍，见 §2 与 §5 |
| 已确认的既有缺口 | ① 阅读渲染闭包里 `contract-views.mjs` 的绑定落后于固定模板（Go 当前拒绝 4.4.1 的该文件）；② Go 拒绝模板写出的真实竞品研究记录（摘要前缀写法不一致），批 1 修复 |
| 「尚未迁移」类拒绝 | 代码里字面含「尚未迁移/未迁移」的共 25 处，`UNPORTED` 错误码共 57 处引用；绝大部分是**不可达的兜底、版本/策略未知时的 fail-closed、或与 Node 一致的能力边界**。真正应由 Go 补齐的缺口集中在 runtime-store、stage `refresh` 及相关过滤、project-ci 的计划/安装范围，见 §3 |
| `nodeDelegation` 34 条 | 33 条对应模板 `scripts/lib/native-evidence.mjs` 的能力协商；固定提交里这些入口的 Node 领域实现已被删除，Node 只剩薄壳，**没有可比对的 Node 语义**，Go 已是唯一实现。1 条（`user-decision.from-record.v1`）在固定提交里没有协商点。完整性只能依赖历史对拍记录与薄壳回放，见 §4 |
| 第一批 | 研究校验器与竞品模块的摘要绑定 → 语义校验，并修复竞品输入闭包的摘要前缀比较；实现与对拍证据在后续 PR，本文随之更新，方法见 §6 |

## 2. 固定 SHA-256 绑定（9 个）

范围判定：在 `internal/`、`cmd/` 的非测试 Go 代码里搜索 64 位十六进制字面量，结果只有三处文件；`safefs.Digest(...)` 与常量比较的位置也只有这三处。其余 `Digest` 调用都是「记录里的摘要必须等于当前字节」的一致性检查，不绑定特定版本，不在本表内。

| # | Go 位置 | 所绑 Node 文件 | 摘要（前 12 位） | 与固定模板 4.4.1 的关系 | 用途 | 脚本字节变化时 Go 的行为 | 状态 |
|---|---|---|---|---|---|---|---|
| 1 | `internal/governance/research_semantic.go` `verifyResearchCompletionSemantic` | `.agents/skills/yss-research/scripts/validate-research-package.mjs` | `2b6f07d3a47a` | 相等 | 研究收尾（`work-unit.maintenance-research`）核验记录里的校验器必须是 Go 镜像过的那一版 | `Unavailable`，码 `CAPABILITY`，退出码 2，「本地研究校验规则版本尚未迁移」；该工作单元的流转被拦住 | 批 1 待实施 |
| 2 | `internal/governance/research_competitive_semantic.go` `validateCompetitiveResearchSemantic` | `.agents/skills/yss-research/scripts/lib/competitive-analysis.mjs` | `d52f89500b59` | 相等 | 含 `competitive_analysis` 扩展的研究包；Go 镜像了结构、引用与受管输出的逐字节比较 | 同上，「本地竞品校验规则尚未迁移」 | 批 1 待实施 |
| 3 | `internal/governance/reading/build.go` `readingToolClosure` | `scripts/lib/reading-view-bundle.mjs` | `2aab86301d37` / `8d35fe88e6d6` | 第二个相等（`cfc275ba`，10-09）；第一个对应 `3cc18284`（10-01） | 阅读渲染工具闭包指纹。Go 自己重算 `reading/*.review.md` 与导航区，再与 Node 写入的清单、输出逐字节比较，指纹保证「Go 镜像的就是这个渲染器」 | `Unavailable`，码 `CAPABILITY`，「阅读渲染规则版本未迁移: <ref>」；仅 `reading-policy.yaml` 为 `mode: managed` 且命中 checkpoint 时发生（默认新实例是 `manual`，不触发） | 待办（批 2） |
| 4 | 同上 | `scripts/lib/contract-views.mjs` | `f88b6f5da3fe` | **不等**：固定模板为 `cc93bdd9b126`。绑定对应 `3cc18284`（10-01），其后 `1afff73a`（10-12，导入层级调整）与 `d6b6fb0d`（10-13，前端整线移除）又改过该文件 | 同上 | 同上。**已确认**：在 v4.4.1 的 spec 实例上直接调用 `readingToolClosure`，结果为「阅读渲染规则版本未迁移: scripts/lib/contract-views.mjs」。即启用 managed 阅读的 4.4.1 项目，其流转校验会一律 `Unavailable` | 待办（批 2，已确认缺口） |
| 5 | 同上 | `scripts/lib/reading-view-adapters.mjs` | `a749e7a86e0b` | 相等 | 同上 | 同上 | 待办（批 2） |
| 6 | 同上 | `scripts/lib/reading-view-markdown.mjs` | `5b55e7627183` | 相等 | 同上 | 同上 | 待办（批 2） |
| 7 | 同上 | `scripts/lib/lifecycle-presentation.mjs` | `beb03307591a` / `8dbed3b7c403` | 第二个相等（`cfc275ba`）；第一个对应 `14694d4d`（10-05） | 同上 | 同上 | 待办（批 2） |

按「摘要个数」计为 9：研究 1 + 竞品 1 + 阅读 7（`reading-view-bundle`、`lifecycle-presentation` 各 2 个，其余 3 个文件各 1 个）。第 3、7 行带两个摘要，所以表中 7 行对应 9 个摘要。

闭包展开的细节（`readingToolClosure`）：从 `scripts/lib/reading-view-bundle.mjs` 与 `scripts/contract` 出发，按 `import` 相对路径递归；**只有上表 5 个文件有指纹**，其余被导入的文件（如 `validation-phase.mjs`、`strategic-handoff-io.mjs`、`slice-contract.mjs`、`reading-view-policy.mjs`、`reading-view-model.mjs`）只记录当前摘要，不要求等于某版本。闭包上限 2048 个文件。

### 同类但不是摘要常量的绑定（不计入 9 个）

这些是 Go 持有或生成 Node 字节，不是「运行时比对固定摘要」，列出以免遗漏：

- `internal/bundle/native/instance-metadata.mjs`、`internal/bundle/work-layout-assets/scripts/lib/*.mjs`：Go 内嵌并写入实例的 Node 脚本字节（`native_upgrade.go`、`native_work_layout.go` 负责生成与升级判定）。
- `internal/governance/contracts/contract_semantic.go`（约 72–82 行）：脚手架 provenance 的「生成器/校验器文件清单」，对记录里登记的摘要做一致性比对，不绑定固定版本。

### 对 9 个绑定的处理原则

1. 绑定只是「历史保险」：它防止 Node 脚本升级后 Go 悄悄沿用旧规则。要让 Go 成为唯一事实源，就必须先证明 Go 语义与固定版本 Node 脚本等价，再把「脚本字节必须等于某摘要」改为「脚本字节必须与登记记录一致 + Go 语义校验」。
2. 这个改动的代价要说清：改后，脚本字节与固定版本不同的输入**不再被 `Unavailable` 拦下**，而是交给 Go 语义判断。所以 Go 若与 Node 有分歧，分歧会从「拒绝」变成「按 Go 的规则放行或拒绝」。批 1 因此要求：先做对拍并如实记录 Go 偏松/偏严的全部差异，偏松的先修成不松于 Node。
3. 没有证明等价的绑定（阅读渲染闭包）保持不动，写入待办。

## 3. 「尚未迁移」类拒绝

### 统计口径

| 口径 | 数量 | 说明 |
|---|---|---|
| 非测试 Go 里含「尚未迁移」或「未迁移」的行 | 25 | 含 Go 文案和 `internal/cli/help.go` 的说明文字 |
| `"UNPORTED"` 错误码引用 | 57 | 含 `domain.Fail`、`Unavailable`、事务层与结果字段 |
| `Unavailable(` 调用 | 246 | `SemanticSession.Unavailable` 表示「无法判定」，退出码 2；不等于「未迁移」 |
| `MIGRATION_REQUIRED` | 3 处 | `handoff_semantic.go` 两处（前代 Handoff v3 协议，已按设计不再支持）；`worklayout` 的 `WORK_LAYOUT_MIGRATION_REQUIRED`（工作布局版本需显式迁移）|

246 个 `Unavailable` 的错误码分布：`CAPABILITY` 71、`INPUT` 39、`PATH` 32、`INPUT_DRIFT` 22、`IDENTITY` 19、`ARGUMENT` 19、`UNPORTED` 13，其余为 `PLAN_REVIEW_POLICY_INVALID`、`HANDOFF_POLICY_CAPABILITY` 等 1–6 个的小类。其中 `CAPABILITY` 里最多的是「未知版本/未知策略/策略缺失」时的 fail-closed、资源上限（扫描 5 万项、固定树 256 MiB 等）与宿主能力（缺 git、git 过旧），这些是合理的能力边界。

### 分类

- **边界**：合理的能力边界或与 Node 一致的明确拒绝，保留。
- **兜底**：未知动作/参数的兜底分支。CLI 预检先于治理层拒绝（已验证：`yss xml foo`、`archive foo`、`context foo`、`contract foo`、`lifecycle check`、`stage refresh` 都在预检返回 `ARGUMENT`），所以这些分支实际**不可达**。措辞带「尚未迁移」会误导，后续可改文案，不涉及行为。
- **缺口**：Node 有对应能力、Go 缺，应由 Go 补齐。
- **未验证**：只读了代码，没有黑盒确认。

| # | 位置 | 触发条件 | 分类 | 说明 |
|---|---|---|---|---|
| 1 | `research_semantic.go` | 研究校验器字节 ≠ 固定摘要 | 绑定 | 批 1 计划移除 |
| 2 | `research_competitive_semantic.go` | 竞品模块字节 ≠ 固定摘要 | 绑定 | 批 1 计划移除 |
| 3 | `reading/build.go` `readingToolClosure` | 渲染闭包文件字节 ≠ 指纹 | 绑定 + **缺口** | `contract-views.mjs` 指纹已落后于固定模板，见 §2 |
| 4 | `governance.go` 59 | 非 verify 动作带 `--history`/`--require-approved` | 边界 | 禁止用结构校验冒充历史/批准语义；verify 路径已支持 `--history`（`lifecycle verify`）。未验证：Node 是否有这些参数用于 check 类动作 |
| 5 | `governance.go` 115 | 未知治理 group/action | 兜底 | 预检先拒绝，不可达（`yss contract foo` 已验证） |
| 6 | `xml.go` 16 | xml 动作不是 inspect/query | 兜底 | 预检先拒绝（已验证） |
| 7 | `transport.go` 95、308 | archive 动作不是 pack/unpack/verify | 兜底 | 预检先拒绝（已验证） |
| 8 | `context.go` 64 | context 动作不是 check/verify/query | 兜底 | 预检先拒绝（已验证） |
| 9 | `lifecycle/query.go` 83、238 | 生命周期/资产动作不在公开集合 | 兜底 | `lifecycle check` 预检拒绝已验证；其余未验证 |
| 10 | `lifecycle/query.go` 242 | `contract|evidence|handoff` 的 `AssetCheck` 里 `verify` | 兜底/疑似死代码 | `governance.go` 在到达前已把 `verify` 分到 `semanticRun`；未验证：确认无其他调用路径后可删 |
| 11 | `lifecycle/query.go` 275 | `check` 结果里 `semantic_validation: "UNPORTED"` | 边界 | 明确标出 check 只做 Schema 结构校验，不冒充语义校验 |
| 12 | `lifecycle/query.go` 290 | 参数不在已实现集合 | 边界 | 拒绝而非忽略未知参数，符合「不得降级忽略」 |
| 13 | `runtime.go` 99 | 运行库是 WAL 模式 SQLite | 边界 | Go 拒绝改动数据库模式/sidecar。Node `runtime-store.mjs` 以 `journal_mode=DELETE` 创建库（见 `PRAGMA journal_mode=DELETE`），因此与 Node 自己的库不冲突；WAL 库来自别处 |
| 14 | `runtime.go` 256、259（动作） | runtime 动作不在已实现集合 | 兜底 | 未验证预检是否先拒绝 |
| 15 | `runtime.go` 259、331 | `runtime begin --input / --report-dir` | **缺口** | Node `RuntimeStore.begin({kind,input,reportDir})` 登记输入对象并为报告目录加 pin；Go 完全不支持（黑盒已验证：`yss runtime begin --input x` 与 `--report-dir y` 均返回 `UNPORTED`）。`project-ci` 完整范围也只支持 `--runtime-store off`（`project_ci_semantic.go` 159） |
| 16 | `runtime_queries.go` 120 | 结果字段 `reference_scan: "UNPORTED"` | **缺口** | run 查询结果里引用扫描未实现；未验证：Node 对应输出形状 |
| 17 | `stage.go` 58 | 存在旧资产事务文件 | 边界 | 旧运行时事务需由旧运行时恢复；2.3.1 起已不提供旧→新迁移 |
| 18 | `stage.go` 150、154 | 写入需显式 JSON checkpoint；缺 checkpoint | 边界 | 设计选择（YAML 仅只读兼容，不从模板推断阶段）。未验证：Node `stage-tracking` 同样限制 |
| 19 | `stage.go` 208 | `register` 种子含 Go 未支持字段 | **缺口（小）** | 当前允许 12 个字段；未验证：Node `planTracking` 接受的字段全集 |
| 20 | `stage.go` 291 | `update` 补丁含非执行状态字段 | 边界（未验证） | 「update 只修改执行状态与明确证据」像是有意收窄，待对 Node 确认 |
| 21 | `stage.go` 318、545 | stage 写入/动作不在 register/update/plan/apply | 兜底 | 预检先拒绝（`stage refresh` 已验证） |
| 22 | `stage.go` 509 | `stage query/status/check --stage/--work-unit` 过滤 | **缺口（小）** | 黑盒已验证：`yss stage query --checkpoint x.json --stage a` 返回 `UNPORTED`。未验证：Node 是否有该过滤 |
| 23 | `stage.go` 549、613 | `refresh`（保留历史并重置执行态） | **缺口** | Node `stage-tracking plan --refresh` 与 `refreshTracking` 存在（`stage-tracking.mjs`、`stage-tracking-migration.mjs`）；Go 完全拒绝。入口形态与 Node 不同：Node 的 `--refresh` 是布尔，Go 的 CLI 把 `--refresh` 当作带值参数（`yss stage plan --refresh` 缺值时返回 `ARGUMENT`），任何值最终都被 `UNPORTED` 拒绝；`yss stage refresh` 子命令不存在 |
| 24 | `stage.go` 571 | 旧/未知 stage 计划不能由 Go 应用 | 边界 | 计划种类、版本、根必须匹配，是防篡改的一部分 |
| 25 | `kernel/stage.go` 545 | stage Schema 含外部 `$ref` | 边界 | 外部引用没有绑定事务输入闭包，拒绝而非放行 |
| 26 | `project_ci.go` 353 | 已存在旧 CI 安装/事务文件 | 边界 | 「不能冒充等价升级」 |
| 27 | `project_ci.go` 358 | `--provider` 不是 github | 边界 | Node `planProjectCi` 同样是「首版仅支持 --provider github」，一致 |
| 28 | `project_ci.go` 460 | `plan/install/apply` 未带 `--scope native-go` | **缺口（范围）/文案** | 黑盒已验证：默认范围的 `project-ci check` 通过（`scope: complete-governance`），`check --scope native-go` 通过；而 `project-ci plan` 不带 scope 返回 `UNPORTED`，文案列出 6 项「尚未迁移」的深层门禁（`unportedProjectChecks`），与默认 `check` 已覆盖这些项的事实不符；`plan --scope native-go` 另需 `--cli-source`。未验证：Node `planProjectCi` 是否有 scope 概念 |
| 29 | `project_ci.go` 464 | 带 `--base/--runtime-store/--recover` | 边界 | 拒绝而不是降级忽略 |
| 30 | `project_ci.go` 474、494 | project-ci 未知动作；未知/旧计划不能应用 | 兜底/边界 | 同 24 |
| 31 | `lifecycle/daily.go`（113、334、358、378、381、406–432、792） | 日常路线策略缺失/未知版本/Profile 未启用/能力集不符/实现仓含 gitlink | 边界 | 全部是 fail-closed：策略来自实例里的合同，不识别就拒绝，不是缺功能。792 的 gitlink 是「单仓日常范围无法证明」 |
| 32 | `lifecycle/daily_api.go` 501 | wire profile 版本或身份不支持 | 边界 | 同上 |
| 33 | `cli/cli.go` 585 | Bundle 里技能需求带 `UnsupportedReason` | 边界 | 由固定 Bundle 声明，如实暴露 |
| 34 | `transaction/transaction.go` 401、621 | Windows 上只读文件的删除/替换/恢复 | 边界（平台） | 合同尚未在 Windows 验证，明确拒绝；非治理规则 |
| 35 | `transaction/replacement_darwin.go` 47 | macOS 要求归档与目标在同一支持克隆的文件系统 | 边界（平台） | |
| 36 | `kernel/semantic_core.go` 561 | `Verify` 的 kind 没有已注册校验器 | 边界 | 「尚缺原生语义校验能力」，防止未覆盖的 kind 被当成通过 |
| 37 | `project_ci_semantic.go` 438 | 声称批准/完成的资产没有专属校验器 | 边界/可能缺口 | 完整 CI 对无法识别类型的资产 fail-closed；未验证：哪些真实资产类型会命中 |

小结：**缺口**共 5 类——runtime-store 输入对象与报告保护（15）、run 引用扫描（16）、stage `refresh`（23）及其小项（19、22）、project-ci 计划/安装范围与文案（28）；加上阅读渲染闭包（3）。其余为边界、兜底或死代码。

## 4. `capabilities.nodeDelegation`（34 条）

### 机制

`yss capabilities --json` 的 `result.nodeDelegation`（`internal/cli/cli.go` `nodeDelegation`）是令牌列表。模板仓 `scripts/lib/native-evidence.mjs` 里 `NEEDS` 把每个委托入口映射到一组令牌，`assertNativeCapabilities` 在缺令牌时给出「请升级 yss」并**拒绝，不回退另一套校验器**。所以令牌是「这条命令行行为已发布」的契约，不是 Node 语义本身。

关键事实（固定提交 `981f7399`）：

- `native-evidence.mjs` 首行即声明「领域校验在固定的 yss 可执行文件中运行，本模块只传输输入并采纳其观测到的依赖」。33 条令牌对应的入口（`verify-approval-record`、`verify-user-decision`、`verify-lifecycle-checkpoint`、`slice-contract`、`verify-scaffold-architecture-decisions`、`complete-backend-delivery`、`backend-delivery`、`strategic-handoff`、`spec-baseline`、`verify-strategic-handoff-consumption` 等）的 Node 领域实现在 AR-09～AR-12c 已被删除，只剩薄壳。
- 模板的 `tests/go-validator-replay.test.mjs` 在固定提交上只有 5 个用例（历史摘要里曾有 196 个），Node 一侧已没有可对拍的领域实现。
- 因此「Node 语义是否已由 Go 完整实现」在固定提交上**无法再用 Node 对拍回答**：Go 是唯一实现，完整性只能依据历史对拍记录（模板 `.template-source/contracts/architecture-iteration-v1-ar09…ar12c-summary.md`）、薄壳回放与黑盒检查。如需重新对拍，要使用更早的模板提交（例如 AR-10 前后，`40926d1` 一带）里仍存在的 Node 实现，这属于批 5 的工作。

### 逐条

「依据」一列：**历史对拍**指模板仓摘要记录当时与 Node 对拍/回放的类别数；**薄壳回放**指本次在基线二进制上运行模板固定提交的 `tests/` 的结果（见 §7 表，Node 仅用于开发期验证）；**未覆盖**指摘要里明确写出的没对拍到的情形。所有「历史对拍」均未在本仓重跑，属二手依据。

| # | 令牌 | 模板消费点（固定提交） | Go 公开接口 | Node 语义状态与依据 |
|---|---|---|---|---|
| 1 | `user-decision.from-record.v1` | **无协商点**：`NEEDS.userDecision` 不含它；`native-user-decision.mjs` 直接传 `--from-record` | `evidence verify --kind user-decision --from-record` | Go 已实现。令牌无人协商：旧 yss 缺该参数时会收到 `ARGUMENT` 而不是「请升级」。建议并入 `NEEDS` 或移除令牌（阶段二） |
| 2 | `user-decision.requirements-object.v1` | `verify-user-decision`、`native-user-decision.mjs` | `evidence verify --kind user-decision --requirements <对象>` | 历史对拍：用户决定 17 组 ×（实例、模板源仓根）、延续模式（AR-09）。固定提交无 Node 实现 |
| 3 | `user-decision.validated-rows.v1` | 同上 | `coverage.user_decision_validated` | 同上 |
| 4 | `approval.expected.v1` | `verify-approval-record`、`native-approval.mjs` | `evidence verify --kind approval --expected` | 历史对拍：批准记录 8 类、期望由 yss 计算 10 组（AR-09）；AR-10 补带已批准门禁 8 类 |
| 5 | `approval.binding.v1` | 同上 | `--binding` | 同上 |
| 6 | `approval.rule-rows.v1` | 同上 | `coverage.approval_validated` | 同上 |
| 7 | `task.directory-write-scope.v1` | `verify-digital-human-task-package` | `contract verify --kind task` | 历史对拍：任务包 7 类（AR-10）。薄壳回放 `go-validator-replay` 通过 |
| 8 | `lifecycle.legacy-untracked-checkpoint.v1` | `verify-lifecycle-checkpoint` | `lifecycle verify --checkpoint` | 历史对拍：checkpoint 一致率量测（AR-10）。**未覆盖**：Plan 聚合、业务票通过审查组合、执行范围终点 `loadExecutionScope`、`--history` |
| 9 | `handoff.package-identity.v1` | `strategic-handoff`（`--bundle`） | `handoff verify --kind package` | 历史对拍：8 类（AR-11） |
| 10 | `handoff.consumption-report.v1` | `verify-strategic-handoff-consumption` | `handoff verify --kind consumption` | 历史对拍：10 类（AR-11）。已知保留差异：Go 要求战术合同带 `schema_version`，Node 对未写版本的放行 |
| 11 | `handoff.spec-baseline-package.v1` | `spec-baseline`、`strategic-handoff` | `handoff verify --kind spec-baseline --package` | 历史对拍：spec-baseline 场景（AR-11） |
| 12 | `evidence.context-reconciliation.v1` | `context-reconciliation.mjs` | `evidence verify --kind context-reconciliation` | 历史对拍：10 类（AR-11） |
| 13 | `contract.openapi-validation.v1` | `verify-openapi-draft-validation-record`、`api-contract-decision.mjs` | `contract verify --kind openapi-validation [--allow-template]` | 历史对拍：17 类含 `--allow-template`（AR-12） |
| 14 | `contract.backend-terminal.v1` | `complete-backend-delivery`（kind `backend-delivery-terminal`） | `contract verify --kind backend-delivery-terminal` | 历史对拍：真实 backend 夹具多类（AR-12）。**未覆盖**：`local-evidence` 模式 |
| 15 | `handoff.backend-delivery-package.v1` | `backend-delivery` | `handoff verify --kind backend-delivery --package` | 历史对拍：8 类（AR-12） |
| 16 | `contract.scaffold-decisions.v1` | `verify-scaffold-architecture-decisions` | `contract verify --kind scaffold-decisions [--allow-template]` | 历史对拍：14 类（AR-12） |
| 17 | `contract.slice-preflight-report.v1` | `slice-contract` | `contract verify --kind slice-preflight` | 历史对拍：v3 夹具（AR-12）。**未覆盖**：v2 合同结构层（`missing:*`、`work-unit-*`）与新鲜度层若干原因码，仅代码对照 |
| 18 | `contract.business-tickets-report.v1` | `business-tickets.mjs`、`verify-business-tickets` | `contract verify --kind business-tickets [--mode] [--review-ref]` | 见 §7（薄壳回放结果） |
| 19 | `governance.validation-dependencies.v1` | `nativeEvidenceVerify` 对每次调用附加 | 所有 verify 的 `validation_dependencies`、`git_inputs` | 协议层（受控依赖回执），非领域语义。固定提交的 Node 侧对回执做越界与复验检查 |
| 20 | `handoff.source-view.v1` | `strategic-handoff.mjs` | `handoff verify --kind source` | 历史对拍见 AR-11；细节未验证 |
| 21 | `handoff.delivery-view.v1` | `strategic-handoff.mjs` | `handoff verify --kind delivery --package` | 同上 |
| 22 | `handoff.source-approval.v1` | `strategic-handoff.mjs` | `handoff verify --kind source-approval --gate` | AR-11 记录 `sourceApproval` 约占 approval/user-decision 进程内回退的 47%，随 consumption 一并迁移；对拍细节未验证 |
| 23 | `handoff.source-context-snapshot.v1` | `strategic-handoff.mjs` | `handoff verify --kind source-approval --source-context-snapshot` | 未验证 |
| 24 | `handoff.spec-baseline-source-view.v1` | `spec-baseline`、`strategic-handoff` | `handoff verify --kind spec-baseline --checkpoint` | 历史对拍见第 11 条 |
| 25 | `contract.spec-baseline-binding.v1` | `spec-baseline.mjs` | `contract verify --kind spec-baseline-binding [--no-reconciliation]` | 未验证 |
| 26 | `contract.plan-entry.v1` | `native-approval.mjs`、`plan-spec-entry.mjs` | `contract verify --kind plan-entry` | **未覆盖**：Plan 聚合（AR-10 记录）；其余未验证 |
| 27 | `contract.gate-checks.v1` | `lifecycle-controls.mjs`、`native-approval.mjs` | `contract verify --kind gate-checks --gate` | 未验证 |
| 28 | `contract.checkpoint-approvals.v1` | `native-approval.mjs` | `contract verify --kind checkpoint-approvals` | 历史对拍：带已批准门禁 8 类（AR-10） |
| 29 | `contract.implementation-scope.v1` | `native-user-decision.mjs` | `contract verify --kind implementation-scope` | 未验证 |
| 30 | `contract.checkpoint-user-decisions.v1` | `native-approval.mjs` | `contract verify --kind checkpoint-user-decisions` | 未验证 |
| 31 | `contract.scaffold-user-decision.v1` | `native-user-decision.mjs` | `contract verify --kind scaffold-user-decision` | 未验证 |
| 32 | `contract.business-tickets-source-views.v1` | `business-tickets.mjs`（与第 18 条同属 `NEEDS.businessTickets`） | `contract verify --kind business-tickets` 的来源视图 | 同第 18 条 |
| 33 | `user-decision.result.v1` | `verify-user-decision`、`native-user-decision.mjs` | `coverage.user_decision_result` | 同第 2 条 |
| 34 | `template-source.verify-root.v1` | `verify-lifecycle-checkpoint`（`NEEDS.checkpoint`） | 模板源仓根上的 `lifecycle verify`（`internal/cli/cli.go` 的 `repository_mode: template-source` 识别） | 未验证具体对拍；Go 对模板源仓根的身份检查见 `cli.go`（拒绝携带实例 metadata 的模板源） |

共 34 条，与 `internal/cli/cli.go` 的 `nodeDelegation` 切片逐条一致，顺序相同（切片末尾空行后是第 34 条）。

### 本节结论

1. 33 条令牌在固定模板提交里都有委托入口，且 Node 领域实现已删，Go 为唯一实现；这一点与「以 Go 为唯一事实源」的方向一致。
2. `user-decision.from-record.v1` 没有协商点，属于声明与消费不一致，低风险，可在阶段二统一。
3. 历史对拍记录列出的**未覆盖**项（Plan 聚合、业务票通过审查组合、`loadExecutionScope`、`--history`、`local-evidence`、Slice v2 结构层）是后续对拍的优先对象；它们在 Go 里是否正确**未验证**。
4. 令牌是粗粒度契约（`*.v1`），不描述参数细节；阶段二调整薄壳时，不能只靠令牌判断「旧 yss 能否承接新参数」。

## 5. 分批计划

原则：每批只动一类边界清晰的规则；先对拍、后改动；任何拒绝都不放宽；出现 Go 比 Node 松的差异，先把 Go 修到不松于 Node 再谈删除保险。「依赖模板仓变更」指是否必须先改模板仓。

| 批 | 范围 | 风险 | 验证方式 | 依赖模板仓变更 |
|---|---|---|---|---|
| 1（随后续 PR 实施） | 研究校验器与竞品模块摘要 → 语义校验；修竞品输入闭包的摘要前缀比较 | 低。对拍 4609 夹具 + 25 个端到端收尾夹具；脚本字节不变时 Go 输出逐字节相同 | §6 | 否 |
| 2 | 阅读渲染闭包 5 个文件的 7 个绑定。先修 `contract-views.mjs` 落后问题：用固定模板的 Node 渲染器对同一 managed checkpoint 生成 `reading/*`，Go 的 `reading-transition` 校验必须逐字节通过；通过后再把「指纹」改为「摘要与清单登记一致」 | 中。渲染器有 5 个文件、约 500 行 Node，输出逐字节比较；前端整线移除可能改变渲染分支 | 在 managed 项目上：Node 生成 → Go 校验接受；对每类变异（改一个字节、少/多文件、过期清单）Go 与 Node 的 `check-views` 结论一致 | 否（读固定提交即可） |
| 3 | stage `refresh`、`--stage/--work-unit` 过滤、`register` 种子字段（§3 的 19、22、23） | 中。涉及写入与 checkpoint | 对拍 Node `stage-tracking plan --refresh` 的计划；写入走既有事务 | 否 |
| 4 | project-ci 计划/安装的范围与文案（28）；核对 `unportedProjectChecks` 是否过期 | 低～中 | 黑盒：默认范围 `plan/install/apply` 的 envelope；对拍 Node `planProjectCi` | 否 |
| 5 | runtime-store：`begin --input/--report-dir`、run 引用扫描、project-ci `--runtime-store` | 高。涉及 SQLite、pin、报告保护 | 对拍 Node `runtime-store.mjs` 的行为；需要新的事务/并发验证 | 否 |
| 6 | 令牌与文案清理：`user-decision.from-record.v1`、不可达兜底分支措辞、`lifecycle/query.go` 242 疑似死代码 | 低，文案类会改变 envelope 的 `message` | 黑盒 envelope 对比（仅文案差异） | 阶段二协同 |
| 7 | 历史未覆盖项的对拍：Plan 聚合、业务票通过审查组合、`loadExecutionScope`、`--history`、`local-evidence`、Slice v2 | 取决于结果 | 用更早模板提交里仍存在的 Node 实现作为预言机 | 否 |
| 阶段二 | 模板仓 Node 脚本变薄、改为调用 `yss`；移除模板侧 `NEEDS` 令牌协商对旧字节的假设 | 高 | 另行设计 | **是**（本任务不做） |

批 2 必须先于阶段二：阶段二会改写 `scripts/lib/*.mjs` 的字节，而阅读渲染指纹还绑着旧版字节，改后 managed 阅读会全部 `Unavailable`。批 1 完成后，研究/竞品脚本变薄不会再被摘要拦住。

## 6. 第一批（计划与验证方法）

### 改动范围

| 文件 | 现状 | 计划 |
|---|---|---|
| `research_semantic.go` | 校验器字节 ≠ `2b6f…0078` → `Unavailable CAPABILITY` | 仍读取校验器并要求其摘要与验证记录一致（`s.Basis`），不再要求等于固定摘要；规则由 `validateResearchPackageSemantic` 执行 |
| `research_competitive_semantic.go` | 竞品模块字节 ≠ `d52f…57e` → `Unavailable CAPABILITY` | 同上，仍读取该模块；规则由 Go 语义函数执行 |
| `research_semantic.go` 竞品输入闭包比较 | 按规范化字节比较；模板写入 `sha256:<hex>`、Go 期望裸十六进制，二者永不相等，**真实竞品研究记录一律被拒** | 比较摘要值（去掉可选 `sha256:` 前缀）；ref、顺序、摘要值仍逐项一致 |

不变的部分：记录里 `command`、`validator.ref`、`exit_code`、时间、stdout/stderr 绑定、`evidence_refs` 登记、Context 不适用说明、阻断项为空、继续维护授权。

### 验证方法（开发期；夹具与脚本在仓外，不入库，不构成测试体系）

1. 对基线二进制初始化 spec 实例；用模板固定提交的 `validate-research-package.mjs` 作预言机（Node 仅开发期使用，不成为运行依赖）。
2. 生成研究包夹具：基础有效包（无扩展、matrix、report、both 四种）；对每个 JSON 路径做 11 类变异；手写语义场景（Claim/证据/反证引用、审计、profile 与 claim_kind、brief 标题与 Claim 引用、slug、非 JSON/BOM/重复键、竞品状态与限定条件、受管输出缺失/漂移/双标记/符号链接/目录/CRLF/BOM/非法 UTF-8 等）。
3. 同一夹具分别交给 Node 校验器与 Go `validateResearchPackageSemantic`，只比较接受/拒绝；Go 偏松的差异必须先修。
4. 端到端收尾夹具（检查点 + 验证记录）：Node 一侧用模板 `validateResearchCompletion`，Go 一侧用 `verifyResearchCompletionSemantic`；再对「校验器与竞品模块各追加一行注释」的实例重做一遍（字节变、语义不变），用来观测摘要闸门。
5. 改动前后各跑一遍，字节未改的实例输出（含逐文件输入清单）必须逐字节相同；字节被改的实例，改后的接受/拒绝与错误码必须与字节未改的实例一致。
6. `go build ./...`、`gofmt -l .`、`go vet -composites=false ./...`、`node tools/smoke/main.mjs --bin <新二进制>`。

结果记录在实现 PR 的说明里，并回写本节。

## 7. 辅助证据：模板固定提交的 Node 测试对基线二进制的结果

仅作发现问题的线索，不是验收：在基线二进制（`0d8a752` 构建）上运行模板固定提交 `tests/` 中涉及 `yss` 的文件，`YSS_NATIVE_BINARY` 指向该二进制。

（结果在批 1 PR 说明里补充；运行环境需要真实 `HOME` 下的 Python `jsonschema`，否则会出现与产品无关的 `python3 EPIPE` 失败。）

## 8. 未验证与限制

- §3 中标「未验证」的条目只读了代码。
- §4 的「历史对拍」全部来自模板仓文档的转述，未在本仓重跑；摘要里 `196` 个回放用例在固定提交中已被删减，无法复现。
- 阅读渲染闭包的「Go 渲染与 Node 渲染逐字节一致」尚未验证（批 2）。
- 本阶段没有恢复任何测试体系；对拍夹具与脚本在仓外，只在 PR 说明里描述方法。
