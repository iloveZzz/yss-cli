# WorkBuddy Skills Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 YSS Spec、Backend 实例增加可追踪、可更新、可校验的 WorkBuddy 项目级 Skills 支持。

**Architecture:** 保留 `.agents/skills` 作为唯一来源，生成 `.codebuddy/skills` 普通文件投影，复用现有 CLI 计划、事务和锁。文件就绪查询与桌面宿主发现分别处理；无可靠宿主接口时保持自动观测未知。

**Tech Stack:** Go（仓库文档要求 1.27.1）、Node.js `>=24 <25`、YAML/JSON、Git、WorkBuddy 桌面端；不增加生产依赖。

**Spec:** [WorkBuddy Skills 支持设计规范](../specs/2026-10-21-workbuddy-skills-design.md)。执行者先读规范与两个仓库 `AGENTS.md`；该规范是本会话设计的持久化基线。

## Global Constraints

- 唯一维护来源保持 `.agents/skills`；WorkBuddy 运行时标识为 `workbuddy`，项目投影根为 `.codebuddy/skills`。
- WorkBuddy 投影为完整普通文件资源树；不使用宿主根符号链接，也不增加 WorkBuddy 专属技能正文副本。
- Spec 的目标投影清单为 `[.claude/skills, .codebuddy/skills]`；Backend 为 `[.codebuddy/skills]`。不恢复已退役的 Codex、Cursor、Pi 投影。
- 旧项目只在显式同步并应用保存计划后接入 WorkBuddy；查询不自动安装或迁移。不新增平台选择命令。
- `.codebuddy` 目录仅允许分发 `skills` 子树；账号、settings、MCP、日志、缓存和用户配置均不随 Bundle 分发。
- 沿用现有锁、事务、摘要及权限合同。新增 runtime 本身不要求提升锁 schema；不假定旧二进制兼容新增 runtime。
- Go 测试使用临时目录与小型内联数据，不访问网络；单测约 1 秒内、单包 30 秒内、全量 5 分钟硬上限。
- 本计划已获会话授权并完成离线实现；下列复选框记录实际执行状态。桌面实机与正式来源步骤仍未完成。任务检查点不授予提交、推送或发布权限。

## Review Focus

1. 目标目录含用户文件、暂存修改或悬空链接：同步和回滚均保留原内容；由任务 2、5 的冲突测试覆盖。
2. Backend overlay 或 CLI 内嵌消费者与根模板不同步：最终发行实例仍能验证 runtime/lock；由任务 3、4 的组合及 native 测试覆盖。
3. 同名用户技能或桌面加载旧副本：不得因文件 ready 而宣称调用预期来源；由任务 0、6、8 的宿主证据检查覆盖。
4. Spec 多运行时与 `--full`：不漏选所需投影，不恢复退役平台，不把参数接受当作安装；由任务 4、5 的闭包及 CLI 测试覆盖。
5. 资源仅权限变化或 CRLF 变化：规范化摘要相同不代表原字节和权限相同；由任务 2、4、5 的反例覆盖。

## 工作目录、依赖与交付

- `CLI` 指执行时隔离检出的 `yss-cli` 仓库；`TPL` 指同一任务的 `yss-spec-project-template` 隔离检出。当前只读基线为 CLI `7e889632799036c7112f794792bb7338cfc522fa`、TPL `cd5f4580e545440974bc3d35baef11660f583aaf`。
- 实施时使用 `superpowers:using-git-worktrees` 建立两个仓库的隔离工作区；发现用户新改动先纳入基线，禁止 reset、清理或借用其暂存区。
- 下文文件路径按所属仓库相对根解释。证据目录位于仓库外的 `work/`，每次运行使用新目录；不在用户 HOME 创建实验文件。
- 依赖：`0 → 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8`。任务 4 的独立 Go 单测可在任务 2、3 进行时准备，真实 Bundle 装配必须等模板与 CLI 消费者一致。
- 每项结束核对范围、测试和 diff，保留可复核检查点；有提交授权时才提交该任务的明确文件，不使用 `git add .`。

### Task 0: 用 WorkBuddy 实机确认接入前提

**Files:** Create `CLI/docs/workbuddy-support.md`（安装与能力边界）；实验夹具及原始日志放仓库外 `work/workbuddy-probe-<唯一标识>/`，不用真实用户项目。

**Interfaces:** 消费官方目录与宿主版本；产出版本绑定的目录发现结果、入口选择 `AGENTS.md` 或 `CODEBUDDY.md`、刷新操作、来源/同名优先级、可调用工具，以及 `supported-probe / failed / not-run` 结果。报告必须区分模型自述与可核验实际读文件/执行记录。

- [ ] 在隔离工作区准备一个仅含 `SKILL.md`、带唯一标记的 `references/probe.txt` 和只输出标记的脚本的技能，初始只放 `.codebuddy/skills/yss-workbuddy-probe`；记录原字节、权限、cwd、桌面版本与系统。
- [ ] 在 WorkBuddy 新任务中显式调用技能，核对实际读取的投影路径、参考文件标记和脚本输出；未安装投影时的对照任务应无法取得该标记。不能用 CodeBuddy CLI 替代桌面验证。
- [ ] 分别验证根 `AGENTS.md` 的可观察指令是否自动生效、文件更新后的刷新操作、同名用户技能优先级、`disable-model-invocation` 和 `user-invocable` 实际行为。使用专用测试用户配置做同名实验，不改真实全局技能。
- [ ] 仅当 `AGENTS.md` 未自动生效时测试 `CODEBUDDY.md` 的薄引导；既有文件场景验证不覆盖。记录首版唯一选定入口，后续任务按该结果执行，不同时维护两套入口规则。
- [x] 写入支持边界及复现步骤。目录或入口验证失败，停止依赖它的任务并修订设计；无桌面环境记 `not-run`，可准备测试与离线实现，但任务 0、8 和支持声明均保持未完成。

**Exit:** 有原始证据支持目录与入口决策；否则不把假设固化为发行支持。

### Task 1: 固定运行时合同及新旧参数边界

**Files:** Create `CLI/internal/domain/skill_runtime.go`、`skill_runtime_test.go`；后续调用者迁移到这两个映射函数。注册表、投影常量及锁的联动修改由任务 2、3 完成。

**Interfaces:** 新增 `domain.SkillRuntimeRoot(runtime string) (string, bool)` 与 `domain.SkillRuntimeForRoot(root string) (string, bool)`，只映射 `claude/codex/cursor/pi/workbuddy` 与对应 `.<name>/skills`（WorkBuddy 对应 `.codebuddy/skills`）；canonical `.agents/skills` 不算平台。注册表仍为 schema 3，锁仍为 version 3。

- [x] 写失败测试 `TestWorkBuddyRuntimeRoot`：`workbuddy → .codebuddy/skills` 且反向一致；空值、`WorkBuddy`、`codebuddy`、越界路径和 `.agents/skills` 均不被当作新增平台；原四个平台映射不变。
- [x] 在 CLI 根运行 `go test -tags yss_nobundle -timeout 5m ./internal/domain -run 'WorkBuddyRuntimeRoot'`，确认因映射未实现而失败；实现两个纯映射函数后重跑至通过。
- [x] 为四个历史平台逐项断言原映射不变；验证纯映射函数不读取目录、不扫描宿主、不接受别名。
- [x] 运行 `go test -tags yss_nobundle -timeout 5m ./internal/domain`，期望退出 0；本项只交付共用映射，不提前放宽 Bundle 或 CLI 参数验证。
- [x] 保存检查点；文档注明 `workbuddy` 是宿主标识，`.codebuddy` 是存储路径，不能作为另一个宿主的支持承诺。

**Exit:** 映射接口稳定；新旧平台与错误输入的边界有测试。

### Task 2: 安全生成普通文件投影

**Files:** Create `TPL/scripts/lib/workbuddy-projection.mjs`、`.template-source/tooling/node/test/workbuddy-projection.test.mjs`；Modify 根及 `profiles/backend/scripts/lib/skill-supply-chain.mjs`、`.template-source/tooling/node/test/skill-supply-chain.test.mjs`。依赖生成锁的集成断言集中在任务 3 更新。

**Interfaces:** 新增 `syncWorkBuddyProjection({ repositoryRoot, source, target, check = false })`，返回 `{status: 'created'|'unchanged'|'updated'|'checked'}`；冲突抛出带 `WORKBUDDY_PROJECTION_CONFLICT` 前缀的错误。只接受仓内 canonical 与同 ID 的 WorkBuddy 路径。现有 `syncSkills({check,hashCache})`、`updateSkillLock(...)` 签名不变；CLI 实例事务不调用此源维护 helper。

- [x] 写失败测试：缺目标生成普通目录；所有资源字节与可执行位一致；相同内容不重写；目标与 Git HEAD 基线相同、canonical 更新时允许更新；未跟踪占用、暂存/未暂存修改、额外文件、文件/目录链接和悬空链接均冲突且原内容不变。
- [x] 写权限/CRLF 反例：相同规范化 hash 但 raw bytes 或 mode 不同不能判定 no-op；任一路径越界或源资源含不支持链接时失败，错误不得触发清理目标。
- [x] 运行 `node --test --test-concurrency=1 .template-source/tooling/node/test/workbuddy-projection.test.mjs`，确认缺接口的失败后实现 helper。源维护更新用 Git HEAD 中目标树的原字节与 mode 判断基线；新未提交目标若已等于 canonical 可 no-op，否则冲突，不将锁更新作为覆盖许可。
- [x] 在 `syncSkills` 的 `.codebuddy/skills` 分支调用 helper，并在通用 obsolete 删除前拦截 WorkBuddy：仅基线匹配的受跟踪生成项可移除，修改或非受管残留报告冲突。实例身份下该分支只允许 `--check`；写操作提示走 CLI 事务。Claude 原分支保持原行为。
- [x] 根 `PROJECTION_ROOTS` 改为 `['.claude/skills','.codebuddy/skills']`，Backend 改为 `['.codebuddy/skills']`；扩展实例 roots 白名单。更新复制同步器到临时目录的单测夹具，同时复制新 helper；保留 Claude 原行为断言。
- [x] 运行 `node --test --test-concurrency=1 .template-source/tooling/node/test/workbuddy-projection.test.mjs .template-source/tooling/node/test/skill-supply-chain.test.mjs`，期望全部通过。此为生成器单元检查点；注册表、锁和 Profile 的成套集成在任务 3 完成前不得装配真实 Bundle。

**Exit:** WorkBuddy 投影具有普通文件、可更新及冲突保护测试；旧平台生成行为未放宽。

### Task 3: 对齐两个 Profile 的锁、分发与消费者

**Files:** Modify `TPL/.template-source/distribution/bundle-profile.json`、`profiles/backend/.template-source/distribution/bundle-profile.json`、根及 Backend 的 `.template-spec/agents/yss-skill-registry.yaml`、`scripts/lib/skill-registry.mjs`、`scripts/lib/profile-distribution.mjs`、`profiles/backend/scripts/lib/instance-distribution.mjs`、`scripts/lib/instance-metadata.mjs`、`scripts/lib/export-skills.mjs`、`profiles/_specialist/scripts/lib/export-skills.mjs`、`.template-source/process/template-verification-profiles.yaml`；Generate 根及 Backend `skills-lock.json`、WorkBuddy 投影；Create `tests/workbuddy-entry.test.mjs`；Test `.template-source/tooling/node/test/skill-registry.test.mjs`、`tests/claude-entry.test.mjs`、`tests/skill-sync-efficiency.test.mjs`、`tests/instance-metadata.test.mjs`、`tests/profile-distribution.test.mjs`、`tests/native-instance-drift.test.mjs`、`tests/agent-hook.test.mjs`、`tests/public-skill-export.test.mjs`、`tests/harness-skill-scope.test.mjs`、`tests/scenarios/verify-lifecycle-context-query-scenarios.mjs`。

**Interfaces:** `nativeInstalledSkillRefs(metadata,lock,options)` 签名不变，识别 workbuddy；`createLifecycleContextQuery(...)` 从注册表获取 runtime，不另建隐式默认。root/targets/managedFiles 集合必须一致，归一化 hash 算法不变。

- [x] 写反例：runtime 为 workbuddy 但 lock 无对应根，或投影缺受管资源必须失败；重复 runtime、错误根及 targets 不一致失败；多运行时未指定宿主继续返回 `runtime-required`；公共技能导出不泄露 `.codebuddy/skills`。运行对应测试，确认在注册与分发尚未更新时失败。
- [x] 更新根注册表 `instance_distribution.runtimes` 为 `[claude,workbuddy]`、两张 root 映射精确对应；Backend `agent_runtime_roots` 仅 workbuddy。扩展 `RUNTIME_OF_ROOT` 并保持重复/未知校验；Backend 保持 full 模式。拆分“唯一 Claude”旧集成断言，保留 Claude symlink 与退役投影不恢复合同。
- [x] 两个 manifest 的 `allowFiles` 增加 `.codebuddy/skills`，保留 `.codebuddy` 的 `excludeRootEntries`，不加入整根 allowRootEntries。现有 Go `includedByManifest` 和 Node `shipped` 都先匹配 allowFiles，可仅放行该子树且无须增加 schema。反例放入 `.codebuddy/private.json`、settings、MCP 和缓存，全部不得出包。
- [x] 按 canonical 的渲染清单镜像 WorkBuddy 对应资源路径；元数据 mapping 加入 workbuddy，Profile 校验正则加入 codebuddy；若任务 0 选定 CODEBUDDY 入口，增加最小引导及 manifest/入口测试，否则不生成该文件。
- [x] 使用既有 `update-skill-lock`、`sync-skills` 入口生成锁及投影，再运行 `node scripts/sync-skills --check`。Backend 在同一 Node 进程中调用 `scripts/lib/profile-tree.mjs` 的 `composeProfileTree('backend',{root})`，用 `spawnSync` 在组合树执行同相对生成及检查入口，并在进程退出前将锁及生成副本复制回 `profiles/backend`；该临时树会在进程退出时清理。禁止把 Backend overlay 当完整项目运行生成器，也不增加另一套永久组装器。
- [x] 更新变更选择合同，使 `.codebuddy/skills/**`、新 helper 和两个 profile 消费者能命中相应检查；运行 `bash scripts/verify-template-fast --plan`（该入口实际为 Bash wrapper），只执行该计划命中且与改动相关的必要检查。
- [x] 在模板根运行：`node --test --test-concurrency=1 .template-source/tooling/node/test/skill-registry.test.mjs tests/workbuddy-entry.test.mjs tests/claude-entry.test.mjs tests/skill-sync-efficiency.test.mjs tests/instance-metadata.test.mjs tests/profile-distribution.test.mjs tests/native-instance-drift.test.mjs tests/agent-hook.test.mjs tests/public-skill-export.test.mjs tests/harness-skill-scope.test.mjs tests/skill-lock-readers.test.mjs`；运行 `node tests/scenarios/verify-lifecycle-context-query-scenarios.mjs` 验证多运行时明确选择。期望各命令退出 0。

**Exit:** 根与组合 Backend 合同一致；真实分发仍须任务 7 验证，不能以组合目录存在替代 Bundle 验收。

### Task 4: 扩展 CLI Bundle 生产与内嵌消费者

**Files:** Modify `CLI/internal/bundle/render.go`、`skill_lock.go`、`closure.go`、`producer.go`、`skill_composition.go`、`native/instance-metadata.mjs`；Test `render_test.go`、`native_upgrade_test.go`；Create `internal/bundle/distribution.go`、`internal/bundle/workbuddy_test.go`。

**Interfaces:** 复用任务 1 的 domain 映射；`SelectedSourceLock(data []byte,skills,runtimes []string)([]byte,error)`、`buildSelective(...) error`、`RenderedSkillLock(...)` 和 `nativeUpgradeSource(...)` 保持现有签名。新增 `FullSkillDistribution(files map[string]File)(map[string]any,error)`，从最终文件中的锁与实际资源生成 `mode=profile-full`、runtimes 和去重排序的 installedSkills；字段是实发清单，不改变 full 安装语义。

- [x] 增加 `TestSelectedSourceLockProjectsWorkBuddyRuntime`：roots 为 `['.codebuddy/skills']`、targets 为 canonical+该根；来源与未知元数据保留；重复或未知 runtime 拒绝。
- [x] 增加 `TestWorkBuddySelectiveClosure`：raw 有技能 a/b、初始仅 a 时，只保留 a 的 canonical 和所选投影完整资源；不选 workbuddy 时不得将该根漏入 Initial；重复/未知 runtime 拒绝；`--full` 闭包可补全。
- [x] 增加 `TestFullSkillDistributionWorkBuddy`、`TestFullSkillDistributionRejectsUnknownProjection`、`TestWorkBuddyBackendDistribution`：Backend runtimes 与 lock roots 匹配、installedSkills 为所有锁内实际分发技能，空投影、未知/重复根、缺失资源拒绝；原无投影 full 来源仍兼容。
- [x] 增加 `TestWorkBuddySkillTreeValidation` 和 native 测试：资源缺失、字节/权限漂移、错误根拒绝；Bundle4 最终嵌入 metadata 消费者接受 workbuddy 的一致组合并拒绝不一致。包括同一最小夹具分别通过模板及嵌入消费者，不用整仓快照。
- [x] 运行 `go test -tags yss_nobundle -timeout 5m ./internal/bundle -run 'WorkBuddy|FullSkillDistribution|SelectedSourceLock|Native'`，确认新增测试失败后实现映射与闭包。`buildSelective` 从仅 Claude 改为验证已声明 roots，并对 canonical 和所选平台统一按 skill ID 筛选，不自动放开所有历史投影。
- [x] Backend producer 通过已分发锁和文件推导元数据，填入 `profile-full` distribution；组合外部技能仅为当前 Profile 声明的投影根生成副本。保持未知字段、hash 和 mode 合同；不用改历史迁移根或扩大源码 symlink 支持。
- [x] 重跑本项命令；查看生成 File entries 为普通文件并确认所有镜像资源进入 hash 校验，保存检查点。

**Exit:** 生产端与最终内嵌消费者均接受一致的新 runtime，不依赖手工修补实例文件。

### Task 5: 接入安装、升级、查询与回滚

**Files:** Modify `CLI/internal/project/distribution.go`、`upgrade.go`、`skills_query.go`、按失败用例需要调整既有 `plan.go`；Modify `internal/cli/commands.go`、`project_commands.go`、`help_topics.go`；Create `internal/project/workbuddy_test.go`、`internal/cli/workbuddy_test.go`。

**Interfaces:** `ResolveSkills(id,b,requested,runtime,when)` 签名不变。增加私有 `runtimeSkillRoot(runtime string)(string,bool)` 作为任务 1 映射的查询入口；保留 legacy runtime 兼容路径。空 runtime 维持现有 canonical 查询；显式 workbuddy 必须核验本地安装声明、lock targets、受管记录和投影，未登记时 `blocked`，无写副作用。

- [x] 写 `TestWorkBuddyResolveStates`：一致→ready；从未安装且相关路径无占用→missing；已声明丢失、未登记 runtime、snapshot 不匹配、资源/权限漂移或未受管占用→blocked。未知 runtime 为 ARGUMENT；空 runtime 不改变既有语义。
- [x] 写 `TestWorkBuddyEnsureAndSyncPlan`：ensure 包含请求技能依赖的 canonical 与已声明投影；显式 sync 新增投影、锁及 metadata 在同一计划；Bundle 无实际投影不登记 workbuddy；查询前后目录和元数据无写入。增加 `TestBackendSyncRefreshesFullRuntimeMetadata`：旧 Backend 仅含 mode，升级后实际 runtimes/installedSkills 与新锁一致；仍分发全部 Bundle 资产，不转 selected，不改变历史维护技能迁移判断。
- [x] 写 `TestWorkBuddyConflictAndRollback`：用户编辑、额外文件、悬空链接阻断；未决冲突不能 apply；计划后文件再变化拒绝执行；失败恢复及回滚不删除非受管文件或覆盖新修改，复用既有事务测试夹具。
- [x] 写 `TestWorkBuddyCLISelection`：skills resolve 的 `--agent-runtime workbuddy` 被接受并进入正常身份校验；`codebuddy`/未知值在读项目前被拒；帮助/补全与 choices 一致；参数并不安装宿主；`--full`、assets ensure 和 skills ensure 都经闭包补齐所选投影，不恢复 Codex/Cursor/Pi 副本。lifecycle route 继续拒绝其未声明的 `--agent-runtime`。
- [x] 运行 `go test -tags yss_nobundle -timeout 5m ./internal/project ./internal/cli -run 'WorkBuddy'` 看新增测试失败；接入 domain 映射、`completeTargets`、`skillDeclared`、foreign file 分类与 scoped tree 识别。不要把 `.codebuddy` 盲目加进固定历史迁移清单。
- [x] `distributionFor` 的 profile-full 分支从目标 `b.Distribution` 刷新实发 runtimes/installedSkills，不能继续返回旧 metadata 空字段；保留完整锁，`completeTargets` 保持 full 分发。更新 agent-runtime 参数及帮助，保留原枚举并增加 workbuddy；不向 lifecycle 命令新增该 flag。修正“新实例无投影”过时说明，重跑本项及既有回归。

**Exit:** 用户通过既有事务接入，ready 只表示文件合同，用户修改和旧平台受保护。

### Task 6: 如实处理桌面宿主发现与技能限制

**Files:** Modify `TPL/scripts/inspect-skill-discovery`、`scripts/lib/skill-discovery.mjs`、`.template-source/tooling/node/test/skill-discovery.test.mjs`；Update `CLI/docs/workbuddy-support.md`、`docs/skills-discovery.md`。

**Interfaces:** `observeRuntime({runtime,binary,cwd,...})` 签名保留；workbuddy 分支在执行任何 `binary --version` 前返回 `{runtime:'workbuddy',version:null,cwd,status:'unknown',entries:[],reason:'workbuddy-desktop-catalog-unavailable',...既有基础字段}`。仅 workbuddy 不要求 `--binary`；其余 runtime 参数要求保持。`assessDiscovery` 不接受手工声明代替真实观测，不改变阻断语义。

- [x] 写失败测试：无桌面接口时为 unknown，不调用 CodeBuddy 二进制；扫描目录、CLI catalog、模型自述不能变成 observed；必需技能缺宿主观测仍 blocked。
- [x] 写来源反例：同名技能的资源 hash 不同、version/cwd 不匹配、缺依赖或有效策略摘要，均不能得到可用结论。复用 `resourceIdentity` 的完整资源及可执行位核验。
- [x] 运行 `node --test --test-concurrency=1 .template-source/tooling/node/test/skill-discovery.test.mjs` 验证失败后实现 workbuddy 分支及参数差异，再运行至通过。
- [x] 用已有技能清单检查平台专属工具、变量、手动调用、Hooks、fork 和外部 CLI 依赖；在支持文档逐项列出已证实兼容或明确受限的能力。不批量改写 canonical frontmatter，不为尚未验证的能力增加自动降级。
- [x] 写清“文件 ready、宿主 unknown、人工实机验证”的并存含义。若任务 0 找到可靠桌面 catalog 接口，另补有原始响应的接口设计与测试后替换此分支，不临时伪造 observed。

**Exit:** 未知状态如实可见；首版人工调用证据独立存在，不授予自动发现资格。

### Task 7: 装配新 Bundle 并执行针对性回归

**Files:** Modify 既有固定来源工具按合同生成的 `CLI/docs/source-lock.json`（仅满足正式来源资格时）、`internal/bundle/assets/bundles.json.gz.sha256`、帮助视图及 `docs/cli-help.md`；本地 working-tree 锁与日志按仓库既有规则存放，归档本体不提交。

**Interfaces:** 使用现有 source-lock / artifact 工具，无新增发行入口。`tools/artifacts.mjs` 要求 `--source-root` 和仓库外且不存在的 `--out`。

- [x] 固定两仓最终输入。没有提交/推送授权时，按 `docs/ci-verification.md` 的 working-tree 锁流程用独立 `GIT_INDEX_FILE` 捕获不可变 sourceTree；正式锁保持原样。禁止用旧固定 Bundle 的通过结果证明本次新增内容。
- [ ] 满足既有正式来源资格时，先预演 `node tools/refresh-source-lock.mjs --source-root "$TPL_ROOT" --profile all`；核对 Spec/Backend 的提交、overlay、policyHash 与来源，按工具 `--apply` 更新锁，不手改来源身份。`TPL_ROOT` 为执行者隔离模板仓绝对路径。
- [x] 正式来源路径运行 `node tools/artifacts.mjs generate --scope all --source-root "$TPL_ROOT" --out "$WB_ARTIFACT_OUT"`；`WB_ARTIFACT_OUT` 是每次唯一、仓外的新目录。working-tree 路径按现有本地装配入口执行，结果始终标记实验包。
- [x] 对本次新 Bundle 分别验证 Spec/Backend init、ensure、显式 sync、resolve、入口及渲染资源，特别核对最终实例的嵌入 metadata 消费者；测试使用小夹具，黑盒只选必要主路径，不新增整仓快照或长跑恢复测试。
- [x] 运行 CLI 必要检查：`go test -tags yss_nobundle -timeout 5m ./...`；有本次完整归档后再运行 `go test -timeout 5m ./...`、`gofmt -l .`、`go vet -composites=false ./...`、`CGO_ENABLED=0 go build ./...`。成功期望为退出 0，gofmt 无文件列表；局部集成缺环境如实记录。
- [ ] 对正式来源生成结果运行 `node tools/artifacts.mjs check --scope all --source-root "$TPL_ROOT" --out "$WB_CHECK_OUT"`，使用另一个新目录。核对摘要、帮助与来源一致；工作树实验的检查结果不得混入正式资格。

**Exit:** 本次输入对应的 Bundle、测试和构建记录可复核；未满足正式来源条件时仅交付实验包及限制。

### Task 8: 完成桌面验收与支持文档

**Files:** Update `CLI/docs/workbuddy-support.md`、`docs/skills-discovery.md`、两个模板 Profile 的使用说明；实机原始证据放仓库外，不把账号凭证或完整用户日志入库。

**Interfaces:** 支持矩阵每行含 WorkBuddy 版本、系统/架构、Profile、CLI 版本/来源、Bundle 来源、技能资源摘要、入口方式、刷新操作、已通过用例、自动观测状态和证据位置。

- [ ] 在任务 0 的桌面环境用任务 7 的新 Bundle 创建两个独立项目，重新验证自动入口、显式技能调用、references/脚本访问与只读 CLI；核对实际源路径和标记，不能复用旧夹具调用结果。
- [ ] 验证更新后新任务使用新内容；同名用户技能、关闭技能、权限不足、取消、外部运行时缺失有可解释结果，关键步骤不得悄悄跳过。自动观测无可靠接口时仍 unknown。
- [x] 对照规范逐项记录 pass/fail/not-run 及证据。文件测试成功而桌面用例未跑时，交付标记“文件与分发验证完成，桌面支持未验收”。
- [x] 更新安装、查询、显式 sync、冲突处理、旧 CLI 升级顺序和支持矩阵。旧 CLI 未实际验证的兼容组合标记未知，不能推断为支持。
- [x] 按 `superpowers:requesting-code-review` 做全分支审查，聚焦五项 Review Focus 与跨仓来源；修复实际发现后仅重跑受影响检查。未授权不提交、不推送、不发布。

**Exit:** 只声明实际通过的版本/系统/Profile；所有失败和未执行项保留，不把构建成功或自动文件校验当作桌面支持证明。

## 完成定义与执行交接

计划评审后选择执行方式。建议 **Native：同一会话顺序实施，最终独立审查**，因为两个仓库的锁、Profile 分发和内嵌消费者耦合紧密；任务 4 的局部测试准备可并行，但跨仓装配串行。

若选择逐任务子代理方式，执行前加载 `superpowers:subagent-driven-development`；若当前插件未提供该技能，不假装已调用，改由用户选 Native 并使用 `superpowers:executing-plans`。无论方式如何，每项只在明确完成并附证据后勾选。

执行状态（2026-10-21）：任务 1–6 的离线实现与回归完成；任务 7 完成本地 working-tree 实验装配；任务 0 与任务 8 的桌面实机部分未执行。独立代码复核及一轮 Important 修复完成，未提交、推送或发布。详细结果见 [执行报告](../reports/2026-10-21-workbuddy-execution.md)。
