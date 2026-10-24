# 默认直接使用技能 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking. 执行方式由用户选择；当前不派发实现代理。

**Goal:** 普通工程直接使用独立技能，新建项目默认不安装强制治理入口，正式治理与现有实例保持明确兼容边界。

**Architecture:** 模板拥有技能与分发合同；CLI 消费固定来源，实现一次安装选择和按需加载。普通任务不经过 daily/standard/Authorities，正式入口保留完整语义；既有事务引擎处理安装、同步和显式迁移。

**Tech Stack:** Markdown Skills、Node 24、Go、现有 YAML/JSON 元数据、Git 和 transaction plan/apply；不增加依赖或常驻服务。

**Spec:** [模板主设计](../../../../yss-spec-project-template/.template-source/contracts/2026-10-23-skill-first-optimization-design.md)、[CLI 技术设计](../specs/2026-10-23-skill-first-optimization-design.md)。设计与本计划均于 2026-10-24 获用户确认，采用本会话顺序实施及最终独立审阅。

## 执行状态（2026-10-24）

Tasks 1–6 已实现并完成受影响验证；Task 7 的本地候选装配、全量 Go 测试、构建、vet、格式、帮助一致性及黑盒冒烟已通过。独立审阅的三项 P1 已在同一轮修复，以失败→通过回归和修复后全量验证关闭。没有遗留 Minor。

用户继续推进后，两仓已完成本地实现提交，来源锁刷新为真实模板提交（尚未推送）。固定提交装配和派生文件验收见 `/workspace/skill-first/RESULTS.md`。未推送、合并或发布，未迁移真实用户项目。原 checklist 保留批准时实施步骤，下文的待选执行方式文字为历史背景，当前以本段和结果记录为准。

## Global Constraints

- 模板基线 `9988c1d1e2fae0f868c6dfb32ae7c7119011ee7e`；CLI 基线 `7b92c7784ec553cee253b34a78e19088b9a22cce`。路径 `template/`、`cli/` 分别代表两仓根。
- Profile 仅 Spec/Backend；不新增业务 Profile、不新增第四条 lite 路线、不新建本地发现命令或资格注册系统。
- 普通任务强制治理文件、checkpoint、人员注册均为 0；会话已有授权不转成额外表单。
- 仅改 canonical `.agents/skills`，用原生成器更新投影和锁；不覆盖用户工作，不改写历史批准及失败。
- `skills resolve` 的 ready/missing/blocked、既有 envelope 与退出码含义不变；旧正式命令不回退成普通任务。
- Go 行为变更使用临时小数据、无网络测试；单例约 1 秒内、单包 30 秒内、全量 5 分钟；`CGO_ENABLED=0`；Git 路由用例要求 Git 2.44+。
- 不制造自动 commit、推送或发布；每项提交步骤只有得到相应 Git 授权后才执行，否则保留可审阅工作区。
- 实施开始使用 using-git-worktrees 技能隔离工作区；复制本次已批准设计与计划时保留原未提交文件，不清理根工作区。

## Review Focus

1. 新 CLI 更新旧正式项目不能套用新建默认值；Task 4 的 `TestSyncPreservesInstallation` 覆盖。
2. 保存计划后改入口、安装选择或新增活动任务，apply 必须拒绝；Task 5 的漂移用例覆盖。
3. 普通入口仍被运行时投影或专业参考重新引回治理；Task 1/3 的任务样例和初始化资产断言覆盖。
4. 老 CLI 读新元数据不能忽略新选择后执行错误写入；Task 2 的版本拒绝及旧计划兼容用例覆盖。
5. 仅改检查选择可能漏掉生成消费者或删除路径；Task 6 的新增、删除、重命名、无基线用例覆盖。

---

## 安装接口与跨任务合同（本计划提出，审阅后实施）

- 在现有 `init`、`attach`、`migrate` 增加 `--installation skills|governed`。省略值的新建 init/attach 为 skills；已有受管实例沿用保存值，缺值的旧实例解释为 governed。
- `sync` 不接受该选择参数；`--full` 只扩展当前安装方式中的技能，不改变是否治理。现有 `profile prepare` 是正式源工程准备，继续显式生成 governed 目标，不随默认值改变。
- apply 仅消费保存计划；不同时接受 `--installation`。`migrate` 未给该参数时保留原 metadata3→4 迁移语义；显式给参数时使用新的安装方式迁移逻辑，不复用旧迁移的身份假设。
- 新增 Go `type InstallationKind string`，常量 `InstallationSkills="skills"`、`InstallationGoverned="governed"`，放 `internal/project/installation.go`；分发持久化字段为 `distribution.installation`。这是安装状态，不写入普通任务记录。
- 新安装合同使用 Bundle schema 5、metadata schema 5、plan schema 4；旧 3/4 metadata、旧支持的 Bundle、旧 plan 保持原读写语义。新版本号防止旧 CLI 忽略安装差异。更新 schema 配对消费者时必须清点 identitymeta、Bundle validator、原生脚本、项目 Detect 和治理 loader 的数字版本分支，不能只扩大顶层接受范围。增加字段必须进入内容摘要及事务重算；不修改全局 envelope/protocolVersion。
- Bundle5 新增 `installations`，只包含 `skills` 与 `governed` 两键。每项是 `{initial: map[path]File, distribution: object}`；原 `Files` 保存可分发资产全集，原 `Initial`/`Distribution` 保留 governed 兼容视图。每项独立生成相符的 skills-lock 与入口，不用删文件前缀推断集合。
- 模板通过现有 `.template-source/distribution/bundle-profile.json` 声明两套物料选择。普通集合采用明确 allowlist，不分发强制治理政策、角色表、阶段 schema、checkpoint 模板或生命周期默认技能；按请求仍可独立安装专业技能。
- 普通初始共享技能为 `implement`、`code-review`；Spec/Backend 的工程说明分别生成。正式初始集合沿当前政策。独立技能携带自己实际引用的参考及脚本，不要求 whole-profile 安装。

## Task 1：核心技能可独立使用（A）

**Files:** Modify `template/.agents/skills/{implement,code-review,yss-research,maintaining-skills}/SKILL.md` 及其实际可达 references；`template/scripts/lib/export-skills.mjs`；Test `template/tests/public-skill-export.test.mjs`；Create `template/.template-source/contracts/skill-first-task-cases.md`。

**Interfaces:** Consumes 已批准主设计 §4–5；Produces 可独立安装的技能树，入口与实际必需资源闭合。没有新 CLI 接口；既有正式编排器负责正式合同，通用技能不反向申请资格。

- [ ] 增加导出资源断言：导出所选技能的真实引用资源存在，不强制携带生命周期资产；复用当前 export fixture，不添加只匹配文字的测试。
- [ ] 运行 `node --test tests/public-skill-export.test.mjs`，记录改动前实际失败或已覆盖结果，不制造失败。
- [ ] 精简上述入口与调用链；通用研究复用单文件来源说明，不自动创建审计包；显式正式审计保留在对应参考中。普通 review 无 tracker/Spec 文件时用当前验收，确无依据时明确相关轴限制。
- [ ] 记录并实际评估五个样例：已授权修复；无独立 Spec 的 review；无 tracker 的研究；无关技能损坏；明确要求正式阶段复核。每例写实际路径、结果、未覆盖项，不声称静态测试证明模型行为。
- [ ] 用 `scripts/sync-skills`、`scripts/update-skill-lock` 同步实际受影响派生；重跑导出测试及 fast 计划选中的相关检查。此批不改变既有根入口，不宣称全项目默认切换完成。
- [ ] 审阅差异；在已获 Git 授权时提交 `refactor: make core skills independently usable`。

## Task 2：模板分发合同与版本化消费（B1）

**Files:** Modify `template/.template-source/distribution/bundle-profile.json`、Backend 对应文件；Create `template/.template-source/distribution/entries/{spec,backend}-skills-AGENTS.md`、`skills-CLAUDE.md`；Modify `cli/internal/bundle/{bundle.go,producer.go}`、`cli/internal/identitymeta/native.go`；Create `cli/internal/bundle/installations.go`、`installations_test.go`；Modify `cli/internal/identitymeta/versions_test.go`。

**Interfaces:** Produces `bundle.Installation{Initial map[string]File, Distribution map[string]any}`、`Bundle.Installations map[string]Installation`；`(*Bundle).ForInstallation(kind string) (*Bundle,error)` 返回不修改原 Bundle 的选中视图，未知/缺失值返回错误；旧 Bundle 仅能明确请求 governed。

- [ ] 写 `TestBundleInstallationViews`：两集合入口与锁对应，skills 无治理文件，选择视图不修改全集；写 `TestInstallationRejectsBadReferences`：越界、无资源、未知选择均拒绝。
- [ ] 写 `TestMetadataInstallationVersionPairs`：metadata5 只接受 Bundle5 和明确 skills/governed；旧版本缺该字段仍按旧合同；未知 schema 拒绝。历史来源字段在 schema5 保持可核验，不因升级抛弃。
- [ ] 运行 `go test -tags yss_nobundle -timeout 30s ./internal/bundle ./internal/identitymeta`，确认新增行为尚未实现。
- [ ] 实现上述类型、选择和验证；producer 消费模板声明，生成两套独立入口、锁与投影。Bundle 摘要包含 installations，所有文件仍校验来源、模式与资源闭合。
- [ ] 普通入口只描述工程、授权范围、技能与实际验证；不要求 yss-project/CONTEXT/tracker 成为使用技能的前提。正式入口保持当前合同。
- [ ] 增加一个在旧 Bundle4 基线运行的兼容断言：旧验证器拒绝 Bundle5；新验证器读取旧 Bundle 仍产生 governed 视图，不能猜 skills。
- [ ] 重跑相关测试，审阅生成合同；有 Git 授权时提交 `feat: define versioned installation distributions`。单独发布本批不能切换默认行为。

## Task 3：初始化与正式准备的安装选择（B2）

**Files:** Create `cli/internal/project/installation.go`、`installation_test.go`；Modify `cli/internal/project/{plan.go,upgrade.go,distribution.go,profile_prepare.go}`；Modify `cli/internal/cli/{commands.go,project_commands.go,command_flags.go}`；Test `cli/internal/cli/command_input_test.go`、`cli/internal/project/profile_prepare_test.go`。

**Interfaces:** `ResolveInstallation(id *Identity, command string, requested InstallationKind) (InstallationKind,error)` 处理本计划的默认与保留规则；`PlanningOptions.Installation InstallationKind`；`Plan.Installation InstallationKind`，JSON `installation`；现有 `buildCore` 通过 Task 2 选择 Bundle 视图。新 plan4 的 Public/Save/Read/Apply 全程绑定选择。

- [ ] 写 `TestResolveInstallation` 表：新建缺省 skills、显式 governed、旧实例缺字段 governed、未知拒绝、sync 显式选择拒绝。
- [ ] 写 `TestInitInstallationAssets`：Spec/Backend × skills/governed；skills 无生命周期 registry/checkpoint/强制路由入口，包含所选 canonical、运行时投影与相符锁；governed 保留必要资产。
- [ ] 写 CLI 用例：未知选择、apply 搭配选择、非法命令搭配选择均为参数错误；`--full` 不切换方式。
- [ ] 运行两个相关包定向测试，确认新用例因缺行为失败。
- [ ] 实现选择、计划持久化和申请入口；保留 `Build(...)` 原签名，内部通过 PlanningOptions 传参，不把选择塞进任意模板变量逃避校验。
- [ ] 在 `profile_prepare.go` 显式以 governed 创建目标；扩展 `TestPrepareProfilesKeepsGovernedTargets`，验证普通默认值不影响正式源准备。
- [ ] 在 Task 2 Bundle5 小型临时实例上规划并应用两类 init；确认实际目录内容、入口和元数据，不只检查计划文案。重跑相关测试；有授权时提交本批。

## Task 4：同步和普通工程加载解耦（C1）

**Files:** Modify `cli/internal/project/{identity.go,plan.go,distribution.go,upgrade.go}`、`cli/internal/identitymeta/native.go`；Modify `cli/internal/governance/kernel/semantic_core.go`；Create `cli/internal/project/installation_sync_test.go`；Modify `cli/internal/project/skills_query.go` 仅必要兼容点。

**Interfaces:** Consumes Task 3 安装字段；Produces 受管工程识别与治理识别的显式分支。skills 元数据只需工程来源和受管状态，不强制 Profile 治理文件；正式 `Authorities()` 检测 skills 安装时返回 `GOVERNANCE_NOT_ENABLED`，不绕过治理校验后继续执行。

- [ ] 写 `TestSkillsSyncWithoutGovernanceAssets`：metadata5 skills 工程删除/从未创建 yss-project、harness-profile、CONTEXT、registry，sync 仍正确规划，冲突检查保留。
- [ ] 写 `TestSyncPreservesInstallation`：metadata3/4 governed、metadata5 governed、metadata5 skills 均保留方式；旧项目不因目标 Bundle 默认 skills 改变入口。旧 schema3 的已有显式治理迁移要求保留。
- [ ] 写 `TestGovernanceRejectsSkillsInstallation` 和 `TestResolveSkillsCompatibility`：正式命令不放行，resolve 原输出与阻断语义保持。
- [ ] 运行相关 project/kernel 测试，确认原强制 Context 或治理加载造成的实际失败。
- [ ] 拆开 project Detect/buildCore 对工程身份和治理 Context 的需求；只在 governed 分支保留现行正式前提。勿删除 transaction.Status、来源摘要和路径检查。
- [ ] 核对命令入口也未提前强制治理身份；不修改 daily/standard 准入规则，不添加普通任务新 verifier。
- [ ] 重跑定向测试，核对实际文件无新增治理资产；有授权时提交本批。

## Task 5：已有实例显式迁移（C2）

**Files:** Create `cli/internal/project/installation_migration.go`、`installation_migration_test.go`；Modify `cli/internal/project/{governance_migration.go,plan.go,recovery.go}`、`cli/internal/cli/project_commands.go`；Create `template/.template-spec/process/installation-migration.md`。

**Interfaces:** `BuildInstallationMigration(ctx context.Context, root string, target InstallationKind) (*Plan,error)`，由 `migrate --installation` 调用，保存 plan4，沿原 ApplyContext/transaction 应用。`InstallationMigration` 记录 source/target、保留路径、阻断引用和观察输入；这是一次操作预览，不是任务状态库。未给选择时原 migrate 路径不变。

- [ ] 模板先写最小迁移合同：只改变选中项目的受管入口/安装元数据；不删除任务、批准、checkpoint、历史政策、用户修改或 Git 引用；不跨项目自动迁移。
- [ ] 规定首次迁移仅支持当前可核验 metadata4/5；metadata3 先执行原有显式迁移，不把两种转换串成隐式动作。目标重复选择返回无须转换；缺事实返回具体无法迁移原因。
- [ ] 活动绑定取自真实 tracker.root 与正式引用；使用原政策验证。任一 checkpoint 未完成、未核验、悬空绑定或解析失败都列 blocker，不能按不存在/已结束处理；已完成记录也必须按原政策核验。扫描目录集合与缺失输入均加入计划观察，防止计划后新增工作。
- [ ] 写 `TestInstallationMigrationPreservesHistory`、`TestActiveBindingsBlockMigration`、`TestMigrationRejectsUnknownSource`，分别断言历史字节与用户文件不变、活动/缺失引用阻断、metadata3 明确拒绝此转换。
- [ ] 写 `TestMigrationApplyRejectsInputDrift`：plan 后修改 AGENTS/元数据、新增任务、删除或改变 checkpoint 任一情况不得应用；写 `TestMigrationRollbackPreservesUserEdits`，回滚冲突不覆盖后续编辑。
- [ ] 先运行失败用例，再实现 BuildInstallationMigration；借用原事务观察/保存/重算，不新建 apply 引擎。governed→skills 保留原正式资产；skills→governed 明确安装完整资产，不给历史任务编造批准。旧迁移归档机制只扩展兼容所需条件，不借用它删除原件。
- [ ] 实际 plan/apply/rollback 临时项目验证；旧 migrate 用例不变；有授权时提交本批。

## Task 6：维护入口与 CI 精简（D）

**Files:** Modify `template/.template-spec/process/agent-entry-policy.md` 模板维护段、`.agents/skills/maintaining-skills/SKILL.md`、`scripts/lib/ci-gate.mjs`、`.template-source/process/template-verification-profiles.yaml`；Test `template/tests/{ci-gate,maintenance-iteration,advance-maintenance-root}.test.mjs`。

**Interfaces:** 复用 `runGate({root,mode,base,reportDir,steps,...})` 和现有 fast 影响计划；直接消费现有影响映射与选中检查 ID，不为 STEPS 增设第二份路径映射。未知范围不当作空变更，报告选择原因；不实现跨进程缓存。

- [ ] 捕获一次实际 `scripts/verify-template-fast --plan` 与 CI 步骤选择；列出已确认相同输入的重复项。无法确认重复的不因估算耗时删除。
- [ ] 写真实执行器 fake 调用断言：纯说明不运行无关 registry/lock；技能或分发源改变命中相关检查；新增、删除、重命名路径均按影响选择；无可靠基线时报告缺失输入并停止受影响检查，不空过、不自动扩成全量。
- [ ] 运行 `node --test tests/ci-gate.test.mjs`，确认选择行为失败；实现最小选择/去重，同次执行使用实际检查与输入去重。
- [ ] 普通维护说明去掉必须 checkpoint/候选/回执的前提；明确可主动调用快照命令。保留 `advance-maintenance-iteration` 的显式工具功能，不将其 validator 改成成功。
- [ ] 复用 maintenance-iteration/advance-maintenance-root 用例，断言普通验证入口无 Git checkpoint 副作用，显式快照拒绝覆盖行为保留。
- [ ] 同步受影响技能投影和锁，运行相关 Node 测试和 fast 定向检查；有授权时提交本批。

## Task 7：装配、帮助和最终验收（E）

**Files:** Modify `cli/docs/{quickstart.en.md,skills-discovery.md,compatibility.md}`、`internal/cli/{help.go,help_topics.go}`、`internal/bundle/native/{setup-contract.md,setup-operation-contract.md,setup-project-operations.md,project-operations.md}`、`tools/smoke/profiles.mjs`；模板对应初始化/升级技能的实际引用参考；生成目标含 `cli/docs/source-lock.json` 与现有 artifacts 声明的派生文件。

**Interfaces:** 消费 Tasks 1–6 的最终已提交模板来源；声明支持 Bundle5/metadata5/plan4，旧版本能力保持原义。无新全局协议版本、无本地发现命令。

- [ ] 清点上述 native 入口与模板 canonical 调用链，确认没有要求普通任务 resolve-ready、route 或 tracker setup 的残余强制前提；以实际任务样例核对，不全局字符串删除。
- [ ] 更新帮助、能力与兼容说明：`--installation` 的合法命令、默认值、apply 只读保存计划、sync 不转换；保留原 CLI 拒绝路径。
- [ ] 用固定模板源码按现有 `tools/artifacts.mjs` / `tools/fetch-bundle.mjs` 的帮助与约定生成装配；工作树候选只作开发验证，正式来源锁只能绑定真实提交，不手填预计 SHA。
- [ ] 执行 `go test -timeout 5m ./...`、`CGO_ENABLED=0 go build ./...`、`gofmt -l`、`go vet -composites=false ./...`；无 Bundle 的 yss_nobundle 只作中间检查，不替代本批装配验证。
- [ ] 用实际构建二进制运行 `node tools/smoke/main.mjs --bin <实际二进制绝对路径>`，覆盖两类 init、旧正式 sync、显式迁移和输入漂移；模板按已提交影响范围执行选中检查。正式发行检查另属发布任务。
- [ ] 核对主设计 §10 的 13 个场景逐项关联 Task 1–6 的结果及真实样例。未运行、宿主未观察及没有自然任务样本分别列明，不宣称已证明效率提升。
- [ ] 进行整批代码审阅，修复后重验受影响范围；有授权时提交；合并、推送、发布等待对应实际授权。

## 顺序、完成声明与自检

依赖为 **1 → 2 → 3 → 4 → 5 → 7**，Task 6 在技能前提明确后可独立推进，最终汇入 7。两仓联动的 Tasks 2–5 为同一可发布能力，不能将中间 schema 支持宣传为默认去门禁已完成。

主设计 §4–5 对应 Task 1；§3/8 对应 2–5；§6/7 对应 4–6；§9 对应批次依赖；§10 对应各用例与 7；§11 对应真实样例记录与最终披露。保留历史、权限、分发完整性及旧协议拒绝路径均有测试归属。

本计划未运行测试、未生成发行证据、未提交代码。验收命令是未来实施步骤。设计确认不替代本计划审阅；执行方式尚未选定。建议本会话由主 Agent 顺序实施，任务接口依赖集中，最终单独审阅整批变化；也可由用户明确选择逐任务代理实施与审阅。
