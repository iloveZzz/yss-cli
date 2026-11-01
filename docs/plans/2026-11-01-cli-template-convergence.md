# CLI 模板收敛配套优化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: 使用 `superpowers:executing-plans` 按任务顺序执行，并用下方复选框记录实际完成项。本侧会话禁止子代理；不得调用 `subagent-driven-development`，也不得沿用主线程的执行、提交或发布授权。

**Goal:** 让 CLI 正确消费本轮收敛后的模板，补齐显式技能依赖、安装模式隔离、子模块保护与退役提示，同时保留现有精简命令和安全同步行为。

**Architecture:** 继续由 Go Bundle 生成器读取两套分发清单，运行时只消费程序内嵌资产。必需依赖和退役说明写入现有清单，安装模式分别计算资源闭包；项目写入与事务层共用一个 Git 边界检查。分两批实施：A 修正 CLI 核心行为，B 对接稳定模板输入并验证实际安装与迁移。

**Tech Stack:** 现有 Go 模块、标准库、Cobra、Git、JSON 和 gzip Bundle；沿用 `go.mod` 的 `go 1.27.1`，不新增依赖、命令或常驻服务。

**Spec:** 本文“行为合同”是本次方案的完整范围；依据本侧会话的 CLI 审查，以及模板计划 [legacy-reference-cleanup-plan](/Users/zhudaoming/.codex/worktrees/matt-skills-convergence/yss-spec-project-template/.template-source/design/2026-11-01-legacy-reference-cleanup-plan.md)。执行时同时读取 CLI 的 [AGENTS.md](/Users/zhudaoming/Projects/yss-cli/AGENTS.md)、[当前命令](/Users/zhudaoming/Projects/yss-cli/docs/cli-help.md) 和 [安装兼容说明](/Users/zhudaoming/Projects/yss-cli/docs/compatibility.md)。模板计划仅提供输入范围，不授权继续其实施任务。

**状态：** 已获批并在主任务窗口完成任务 1–5；独立审查发现的 1 项 Important 已修复并定向复验。改动未提交、未发布，未升级真实用户实例。CLI 调研基线为 `8c5e1a90ff83ac83a0640a64b8c8109c3fc5a5e6`。执行前核对实际工作树，不能覆盖之后的用户修改。按 CLI 仓库约定，`docs/superpowers` 只作历史资料，所以本次新计划保存于 `docs/plans`。

## Global Constraints

- 使用简体中文；只修改本计划明确列出的文件，执行授权不包含提交、推送、安装到用户工具目录或发布。
- 不恢复生命周期、批准包、旧插件、保存计划接口、`--plan` 或 `--apply`；`--dry-run` 只读取和预览，不写项目或事务目录。
- 保留输入校验、路径与符号链接保护、内容摘要、所有权、事务、恢复和并发检查；有用户修改时不得强制删除或覆盖。
- 不维护或分发脚本与测试套件。必要验证材料只放在临时隔离副本中，用后清理；不向源码仓提交 `_test.go`、检查脚本或报告框架。
- 不提升项目 metadata schema：继续读取 3、4、5，并由现有迁移写为 6。Bundle 保持 schema 6；新增字段为可省略的扩展，并验证旧归档摘要兼容。
- Bundle 必须记录实际模板 HEAD、工作树状态与摘要；不得将 dirty 输入宣称为 committed，不改写 `docs/template-release.json`，不虚构 1.3.2 或其他正式发行标签。
- B 批只允许在模板主线程不再修改相关清单后，向明确授权的模板工作树提交本计划所列两份 JSON 的局部改动；不得改动当前正在工作的父线程目录。
- Go 改动使用 `gofmt`；实际 CLI 构建先运行现有 Go Bundle 生成器，再运行 `CGO_ENABLED=0 go build -trimpath`。编译成功不等于迁移、跨平台运行或发行成功。

## Review Focus

1. 两个安装模式为同一技能声明不同资源时，切换不能混用另一模式的路径；旧归档仍能读取。由任务 1 验证。
2. 某技能只在普通文字或反例中出现，不应成为自动安装依赖；非默认技能的无效依赖也必须被发现。由任务 1、4 验证。
3. 不存在或未初始化的 gitlink、带空格的挂载名、失效 Git 元数据不能被当作普通目录；显式选择已初始化子仓时仍可正常安装。由任务 2 验证。
4. 退役技能任何平台副本存在用户修改时，同步应无业务文件写入；替代提示不能触发安装或执行。由任务 3、5 验证。
5. 模板源码、CLI 内嵌 Bundle、项目安装基线是三个不同状态；旧实例、旧 metadata 和预览必须分别验证。由任务 5 验证。

## 行为合同

### 当前事实与改动边界

调研发现：`producer.go` 根据 Markdown 中的 `.agents/skills/<ID>` 推测技能依赖；`yss-repository` 的纯名称引用不会进入闭包。`skillReferences` 最后写入全局 `SkillRequirements`，存在两个安装模式互相覆盖的问题；`installedSkills` 在闭包计算前生成。后两项是条件性缺陷，不能把当前默认集合恰好未触发写成已发生故障。

项目与事务当前主要检查嵌套 `.git` 标记，缺少对空 gitlink 的完整识别。已有脚手架的 `internal/scaffold/gitscope.go` 可作为行为参考，但其 detached HEAD、`--force` 和脚手架布置策略不应整体搬到项目安装。

现有同步已经支持：删除未修改的退役受管文件；保护用户所有文件；遇到修改后的退役受管文件时整体返回冲突。保留这套删除与保护决策，只增加可理解的诊断。

本轮不做：运行时注册表编译器、条件执行引擎、第三方技能下载、旧技能执行别名、自动迁移全部用户实例、新清理命令、更新器重构或历史文档大规模删除。`platform_configuration` 与现有安装安全字段继续保留。

### 1. 必需依赖与安装集合

- 在每个 `InstallationPolicy` 增加 `SkillDependencies map[string][]string`，JSON 名 `skillDependencies`。只列安装时必须带入的技能；任务条件、建议与历史引用不编码为依赖。
- 新生成器要求四个安装集合都显式提供该对象；`{}` 表示无必需技能依赖。字段缺失或为 `null` 返回包含清单路径与安装模式的错误，不回退 Markdown 推测。旧归档的读取与旧模板源码重新生成是不同兼容范围。
- 资源路径继续只由 `skillReferences` 声明；不创建另一份资源清单。删除 `skillDependency` 正则推断逻辑。
- `Installation` 增加 `SkillRequirements map[string]Requirement`，JSON 名 `skillRequirements,omitempty`。新生成器为全部可选技能生成一项，即使该项依赖为空，保证“显式空集合”不会在序列化后与旧格式混淆。
- `ForInstallation(kind)` 使用该安装模式的 `SkillRequirements`。旧归档没有此字段时，用旧全局记录中的技能依赖，并从该模式的 `SkillReferences` 重建路径依赖。返回的视图不得与另一模式共享会被修改的 map 或 slice。
- 保留顶层 `Bundle.SkillRequirements` 作为 project 模式的兼容快照；生成时复制自 project，不能继续被 skills 模式覆盖。新运行逻辑以选择后的安装视图为准。
- 校验所有可选技能的声明，包括非默认技能：名称合法、技能与资源存在；拒绝自环与依赖环，并给出环路径。重复边去重、结果排序。目录式资源和越界路径按现有文件校验拒绝。
- 先计算闭包和平台副本，再从最终 `Initial` 的规范 `.agents/skills/<ID>/SKILL.md` 重算 `Distribution.installedSkills`，不得重复计算平台副本。

### 2. Git 写入边界

- 增加 `internal/gitscope/protect.go`，对外只提供 `func Check(root, ref string) error`；`root` 是明确选择的安装根，`ref` 是根内相对路径。复用现有 `domain.Fail("PROTECTED", ...)` 表达已识别的保护边界，Git 读取异常返回可区分的错误。
- 同时识别祖先仓库 index 的 mode `160000`、`.gitmodules` 声明和嵌套 `.git`。使用 Git 的 NUL 分隔输出和配置读取能力处理路径，不复制脚手架中的行正则解析器；忽略外部 `GIT_*` 环境污染。
- 目标为未初始化挂载点、其子路径，或从父仓穿入嵌套仓库时拒绝。用户明确选择已初始化子仓或 worktree 作为根时允许，但仍保护其内部嵌套仓库。不增加“项目安装必须附着分支”的条件。
- 普通非 Git 目录仍可安装，不要求系统必须安装 Git；发现 Git 元数据但无法可靠读取时拒绝写入，不把读取失败解释为无子模块。
- `project.protect` 与 `transaction.guard` 复用此检查。事务创建安装根、锁与控制目录前也检查根及控制路径；恢复、删除和逐文件替换沿用相同边界。不能用当前事务的提交后 `validate` 回调代替写入前检查。
- 保持现有重检位置，防止预览后边界变化被忽略；不宣称 CLI 锁能与其他 Git 进程形成原子互斥。

### 3. 退役诊断

- 在现有 `Policy`、`Bundle` 增加 `RetiredSkills map[string]RetiredSkill`，JSON 名 `retiredSkills,omitempty`；`RetiredSkill` 仅有 `Replacements []string`（`replacements`）和 `Reason string`（`reason`）。无替代技能允许空数组，原因不能为空。
- 校验退役 ID 不与当前技能重叠，替代 ID 在当前 profile 可选集合中存在；不允许别名链。来源只有分发清单，不解析历史迁移 Markdown，不把四个旧 ID 硬编码到 Go。
- `SkillFiles` 请求已声明退役 ID 时返回 `SKILL_RETIRED`，消息包含原因及替代 ID；仍不安装替代技能。未声明的未知名称保留现有未知技能错误行为。
- 增加项目诊断类型 `UnavailableSkill`：`ID string`、`State string`（`retired|unavailable`）、`Paths []string`、`Replacements []string`、`Reason string`，JSON 均为对应 camelCase。路径与记录排序。
- `func unavailableSkills(root string, b *bundle.Bundle, managed map[string]identitymeta.Managed) ([]UnavailableSkill, error)` 汇总当前 Bundle 不再提供的已登记技能；有退役声明为 `retired`，其余为 `unavailable`。同时检查已知退役 ID 在当前 Bundle/metadata 能确定的平台根下的本地副本，以显示已不受管但仍存在的旧入口；不遍历任意用户目录。
- `skills list` 保留 `profile`、`skills`、`installed`，新增 `unavailableSkills`；`project.Result` 增加同名字段，供 sync 预览、成功和冲突结果展示。现有 `Changes`、`Conflicts`、`Preserved` 继续承担逐文件动作解释，不再增加迁移状态机。
- 诊断不得参与删除授权：没有 metadata 所有权记录的本地旧文件只提示；有用户修改仍按原行为整体拒绝同步。

### 4. 本轮模板数据

四个安装集合都增加以下两组必需依赖；没有列出的技能不因正文提及而自动带入。将方法技能随依赖分发不等于自动执行它们。

| 技能 | `skillDependencies` 值 |
| --- | --- |
| `yss-repository` | `alibaba-java-code-style`、`lombok`、`mapstruct`、`yss-mybatis` |
| `yss-openapi-governance` | `yss-dto` |

`yss-distributed-id` 只在实际采用分布式 ID 时使用，不作为 repository 的必装依赖；`implement` 的工程技能选择、研究与原型等条件路由也不展开为必装全家桶。

两套 profile 清单分别写入相同四项退役说明：

| 旧 ID | `replacements` | `reason` |
| --- | --- | --- |
| `yss-openapi-draft-review` | `yss-openapi-governance` | OpenAPI 只读审查已合并到 yss-openapi-governance。 |
| `yss-implementation-contract-compiler` | `to-tickets`、`implement` | 任务拆解与实现按当前请求开展，不再编译实现合同。 |
| `cross-repo-implementation-routing` | `implement` | 仓库范围与跨仓分工由 implement 按本次授权处理。 |
| `implementation-repo-onboarding` | `implement` | 工程识别、只读调查与子模块保护已并入 implement。 |

不修改默认技能数组。模板计划的基准为 Spec 可选 58、Backend 可选 57，默认数组长度分别为 project/skills：7/10、4/4；这些是输入基准，实际安装数应由闭包计算。若稳定模板已发生额外合法变更，核对差异后记录真实输入，不能为了匹配数字回退主线程修改。

## 文件范围与职责

以下 CLI 路径均相对 `/Users/zhudaoming/Projects/yss-cli`；`T` 仅代表执行时明确授权、已稳定的模板工作树，不能默认等于正在工作的父线程目录。

| 文件 | 操作与职责 |
| --- | --- |
| `internal/bundle/producer.go` | 修改 policy 类型、显式依赖读取、每模式闭包、最终计数与退役元数据传递 |
| `internal/bundle/bundle.go` | 修改安装视图、图校验、旧归档兼容、退役类型与选择诊断 |
| `internal/gitscope/protect.go` | 新建共享 Git 边界检查，仅服务实际写入保护 |
| `internal/project/project.go` | 接入保护、汇总退役记录并扩展 list/sync 输出；不重写同步删除规则 |
| `internal/transaction/transaction.go` | 接入事务写入前、控制目录与恢复路径检查；不改变事务协议 |
| `tools/bundle/README.md` | 说明显式声明、模式隔离、实际覆盖顺序和生成输入要求 |
| `docs/skills-discovery.md` | 说明必需依赖、退役提示与实际内嵌来源 |
| `docs/compatibility.md` | 区分旧项目 metadata、旧 Bundle 读取和新生成器输入兼容 |
| `docs/build-install.md` | 补充模板收敛后重建与实际验证方法，不改写发布事实 |
| `T/.template-source/distribution/bundle-profile.json` | B 批局部添加 Spec 依赖及退役元数据 |
| `T/profiles/backend/.template-source/distribution/bundle-profile.json` | B 批局部添加 Backend 依赖及退役元数据 |
| `internal/bundle/assets/bundles.json.gz` | B 批由生成器重建的忽略产物，不加入 Git |
| 临时 CLI 副本中的 `internal/*/convergence_temp_test.go` 与临时安装目录 | 仅验证；全部清理，不形成仓库资产 |

## 批次 A：CLI 行为修正

### Task 1：显式依赖、安装模式隔离与闭包计数

**Files:** 修改 `internal/bundle/producer.go`、`internal/bundle/bundle.go`、`tools/bundle/README.md`、`docs/compatibility.md`；临时验证位于隔离副本 `internal/bundle/convergence_temp_test.go`。

**Interfaces:** 消费现有 `BuildRoot(ctx context.Context, root string) (map[string]*Bundle, error)`、`Requirement`、`InstallationPolicy`；产出行为合同 1 的两个新增字段。保留 `ForInstallation(kind string) (*Bundle, error)`、`SkillFiles(names []string) (map[string]File, error)`、`WriteBuilt(out string, bundles map[string]*Bundle) (*StorageReport, error)` 签名。

- [x] **Step 1：建立临时验证副本与基线。** 复制本次 CLI 源码及实际旧归档到仓外新建临时目录；记录当前改动，保证后续复制不会覆盖用户文件。为新逻辑添加临时用例，输入与断言如下；新字段尚不存在时的编译失败也需明确记录，不能当作业务失败复现。

  | 临时用例 | 精确输入与断言 |
  | --- | --- |
  | `TestInstallationRequirementsIsolated` | A 的 project 资源为 `refs/project.md`、skills 资源为 `refs/skills.md`；两个视图各只选对应资源，修改一个视图不能改变另一个 |
  | `TestExplicitDependenciesOnly` | A 正文同时出现纯名称 B 与反例路径 `.agents/skills/C/SKILL.md`；声明 A→B、B→D；只选择 A 时规范技能集合恰为 A/B/D，C 不安装 |
  | `TestInstalledSkillsAfterClosure` | 默认 A，A→B→D，多个平台副本；`installedSkills` 恰为排序后的 A/B/D，无重复 |
  | `TestInvalidOptionalDependency` | 非默认 X→不存在的 Y，或 X 的资源不存在；构建也必须失败并指出 X 与失效目标 |
  | `TestDependencyCycle` | A→B→A、自环 A→A 均拒绝并展示环；重复 A→B 只产生一份 B |
  | `TestExplicitPolicyRequired` | 缺字段/null 均失败；空对象成功；错误明确清单路径与安装模式 |
  | `TestLegacyArchiveCompatible` | 用修改前真实 schema 6 归档执行解包、摘要验证、两种安装视图与再打包读取；均成功；不得改写旧文件摘要迎合新实现 |

- [x] **Step 2：运行临时用例并记录基线失败。** 在临时 CLI 副本执行 `go test -tags yss_nobundle ./internal/bundle -run 'Test(InstallationRequirementsIsolated|ExplicitDependenciesOnly|InstalledSkillsAfterClosure|InvalidOptionalDependency|DependencyCycle|ExplicitPolicyRequired|LegacyArchiveCompatible)$' -count=1`；新行为至少有一项失败，旧归档兼容用例应保留为回归约束。
- [x] **Step 3：实现字段与模式视图。** 按合同 1 修改类型及 `ForInstallation`，新字段使用 `omitempty` 保持旧 JSON 缺字段时的摘要表示；分别复制 map 和 slice，保留旧全局字段的读取兼容。
- [x] **Step 4：替换生成逻辑并校验全图。** 删除 Markdown 正则依赖推断，按四个显式声明计算闭包，再生成实际 `installedSkills`；先验证全部可选技能，再写归档，错误时不得产生可用的半成品 Bundle。
- [x] **Step 5：更新生成器与兼容说明。** 写清“新生成器需要新 policy，已生成旧 Bundle 仍可读”；Backend 覆盖顺序准确描述为共享内核→`profiles/_specialist`→`profiles/backend`，共享范围仅为当前 `inKernelScope` 的三个目录。
- [x] **Step 6：验证并检查差异。** 将实际改动同步进临时副本，`gofmt` 后重跑 Step 2；全部 PASS，确认旧归档真实输入未被替换。本任务的最终源码不包含临时用例。

### Task 2：保护未初始化子模块与事务写入路径

**Files:** 新建 `internal/gitscope/protect.go`；修改 `internal/project/project.go`、`internal/transaction/transaction.go`；只读参考 `internal/scaffold/gitscope.go`。临时用例分别放入隔离副本的 `internal/gitscope`、`internal/transaction`。

**Interfaces:** 产出 `gitscope.Check(root, ref string) error`；保留 `project.protect(root, ref string) error`、`transaction.guard(root, ref string) error` 的调用合同。事务锁、journal、恢复协议及 scaffold 公共接口保持不变。

- [x] **Step 1：准备真实 Git 场景并验证缺口。** 在临时目录创建本地仓库，以 `git update-index --add --cacheinfo 160000,<有效提交SHA>,<挂载路径>` 创建 gitlink，不访问网络。将 `project.protect` 与事务入口的当前结果记录为基线，禁止在真实子模块上试写。
- [x] **Step 2：添加临时边界断言。** `TestGitBoundaryMatrix` 覆盖：只有 index 的空挂载、只有 `.gitmodules` 的挂载、不存在的挂载、挂载下多级路径、带空格/非 ASCII 名称、嵌套独立仓库，均返回保护错误；普通非 Git 根和明确选择的已初始化子仓/worktree 根成功。`TestGitReadFailure` 断言 Git 缺失但无元数据时允许，已有元数据损坏/无法读取时拒绝；环境中的 `GIT_DIR` 不得改变目标仓判断。
- [x] **Step 3：实现并接入共享检查。** 按合同 2 实现 `Check`，复用 `safefs` 路径检查。事务 `safeRoot` 在 `MkdirAll` 前核验根与控制路径；`guard` 在现有逐文件校验处核验边界。`project.protect` 同时覆盖预览和恢复路径。不要删除现有 `.yss` 自保护及 symlink 检查。
- [x] **Step 4：验证事务与预览行为。** `TestProtectedRootHasNoWrites` 对已存在/不存在的空挂载执行事务，断言无目标文件、无新增 `.yss`，不存在的挂载不被创建。`TestBoundaryChangedAfterPreview` 在预览后将目标改为 gitlink，重新执行时拒绝；`TestRecoveryProtectsGitlink` 对待恢复路径新增 gitlink 后恢复，断言边界内无写入且保留现场。
- [x] **Step 5：运行定向验证。** 临时副本执行 `go test -tags yss_nobundle ./internal/gitscope ./internal/transaction ./internal/project -run 'Test(GitBoundaryMatrix|GitReadFailure|ProtectedRootHasNoWrites|BoundaryChangedAfterPreview|RecoveryProtectsGitlink)$' -count=1`；全部 PASS。复核新检查没有对普通用户目录或明确选择的子仓引入附着分支要求。

### Task 3：展示退役与不可用技能，保留同步保护

**Files:** 修改 `internal/bundle/producer.go`、`internal/bundle/bundle.go`、`internal/project/project.go`、`docs/skills-discovery.md`；临时用例位于隔离副本 `internal/bundle` 与 `internal/project`。

**Interfaces:** 消费任务 1 的安装视图及现有 `identitymeta.Managed`；产出合同 3 的 `RetiredSkill`、`UnavailableSkill`、`unavailableSkills` 和新增 JSON 字段。保留 `Skills(o Options) (any, error)`、`Run(ctx context.Context, command string, o Options) (Result, error)` 签名；无需改动 CLI 错误框架。

- [x] **Step 1：添加退役验证输入。** `TestRetiredSkillSelection`：old→new 的 Bundle 请求 old 返回 `SKILL_RETIRED`，错误包含 new，选择结果为空；未知 typo 仍为未知技能错误。`TestRetirementValidation`：旧 ID 仍可选、替代 ID 不存在、空原因、替代别名链均拒绝；无替代但原因明确时接受。
- [x] **Step 2：实现清单传递与选择诊断。** 按合同 3 透传并校验退役声明，使用现有 `domain.Fail`；不得把 replacements 接入 `SkillFiles` 的递归安装路径。
- [x] **Step 3：实现本地清单与同步结果。** 增加 `unavailableSkills`，当前可用技能名单保持原语义。退役信息在 sync 的成功、预览、冲突结果都可见；读取失败不得被忽略，结果顺序固定。
- [x] **Step 4：增加本地记录断言。** `TestUnavailableSkillInventory`：metadata 中 retired old 与未声明的 disappeared 分别为 `retired`/`unavailable`；已知旧 ID 的本地非受管副本可见但无删除操作；对路径完全不相关的第三方技能不扫描、不声明退役。
- [x] **Step 5：补齐文档并验证。** 更正“项目内嵌模板”为“当前运行 CLI 的内嵌 Bundle”；说明替代仅供选择、修改过的退役文件阻止同步。临时副本运行 `go test -tags yss_nobundle ./internal/bundle ./internal/project -run 'Test(RetiredSkillSelection|RetirementValidation|UnavailableSkillInventory)$' -count=1`，全部 PASS；完整文件删除/冲突行为留给任务 5 使用真实 CLI 验证。

## 批次 B：模板输入对接与实际迁移验证

### Task 4：对接稳定模板的两套分发清单

**Files:** 只修改 `T/.template-source/distribution/bundle-profile.json` 与 `T/profiles/backend/.template-source/distribution/bundle-profile.json`；不得改 skills 本文、注册表、lock、平台副本或模板主线程的计划状态。

**Interfaces:** 消费任务 1 的 `skillDependencies` 与任务 3 的 `retiredSkills`；产出可被 `BuildRoot` 读取的四种安装声明。根路径 `T` 必须是本次明确授权、停止并发修改这些清单的实际输入。

- [x] **Step 1：固定本次输入。** 核对 T 的 HEAD、工作树状态、两份清单与迁移说明；确认四个退役 ID 已从当前技能集合移除，替代 ID 确实存在。若主线程还在修改清单，此任务保持未完成，先完成 A 批；不为继续而覆盖其文件。
- [x] **Step 2：局部添加声明。** 按“本轮模板数据”表向四个安装集合增加必需依赖，向两套 profile 增加退役映射。保留原有 `skills`、`files`、`entries` 和 `skillReferences`，不重写整个清单或引入历史退役全集。
- [x] **Step 3：验证结构与选择。** 用现有生成器输出到新的临时目录；调用任务 1 的 `ForInstallation`/`SkillFiles` 验证四组合：只补装 repository 得到自身及四个必需技能；补装 governance 得到自身与 yss-dto；不带入 distributed-id、旧四 ID 或已删除审查模板。
- [x] **Step 4：核对来源和默认集合。** 输出来源状态与实际输入一致；四组默认数组逐项、逐序与 Step 1 一致，新增依赖只影响最终闭包；可选总数按真实源核对。资源都存在，平台副本内容一致，Backend 未误继承根 user-guide/agents/engineering 目录。

### Task 5：重建内嵌资产，验证四种安装与旧实例迁移

**Files:** 生成被忽略的 `internal/bundle/assets/bundles.json.gz`；修改 `docs/build-install.md`、`docs/compatibility.md` 的必要说明。所有 CLI 可执行文件、旧/新实例和验证断言均在仓外临时目录。

**Interfaces:** 消费任务 4 的稳定模板 T、任务 1–3 的 CLI；继续使用现有 `init`、`attach`、`sync`、`skills list|ensure`、`doctor`、`recover`、`version`、`help` 与 `--json`，不增加 flags。

- [x] **Step 1：准备真实旧实例。** 使用与任务 1 保存归档匹配的真实旧 CLI；若需构建，则在临时隔离副本还原归档记录的模板提交，先用基线 CLI 的生成器生成旧 Bundle，再按既有命令构建旧 CLI，核对旧技能集合与来源。原始旧归档继续用于兼容回归，不被新生成结果替换。若旧来源无法还原，明确该项未验证，不用手拼文件冒充旧安装。为四组合创建旧实例并补装四个旧技能。另准备合法 metadata 3/4/5 的最小临时实例，按各版本真实格式构造，不能只改 schema 数字冒充兼容验证。
- [x] **Step 2：构建新 CLI。** 在实施工作树执行下列现有命令；`T` 和 `CLI_OUT` 指向本次明确的模板目录与仓外临时二进制，运行前记录实际值。资产生成失败时不得继续使用之前残留的归档。

  ```sh
  go run -tags yss_nobundle ./tools/bundle --source-root "$T" --out internal/bundle/assets
  CGO_ENABLED=0 go build -trimpath -o "$CLI_OUT" ./cmd/yss
  "$CLI_OUT" version
  "$CLI_OUT" help
  ```

- [x] **Step 3：验证四种新安装。** 对 `spec/backend × project/skills` 各执行一次空目录 init 和已有普通工程 attach，分别先 dry-run 再实际执行；用临时断言核对预览前后目录树完全一致，实际生成的默认技能、依赖资源、平台副本、`.yss.json` 与对应 Bundle 一致，用户 README/CONTEXT 等原内容保留。
- [x] **Step 4：验证按需补装与模式切换。** 每组执行 `skills ensure yss-repository` 和 `skills ensure yss-openapi-governance`，核对依赖集合；请求四个退役 ID 均返回 `SKILL_RETIRED` 且无写入。分别验证 project→skills、skills→project 的资源选择；已有用户修改仍阻止覆盖。
- [x] **Step 5：验证同步退役与迁移。** 用同一旧实例的独立临时副本执行以下用例，避免一个用例清理了另一个的输入：

  | 输入 | 必须观察到的结果 |
  | --- | --- |
  | 四个旧技能均为未修改的受管文件 | 预览列出退役信息和删除；实际同步移除旧受管文件与记录，不自动补装 replacements |
  | 仅规范 SKILL.md 被修改，或仅某个平台副本被修改 | 整体冲突，全部业务文件和 metadata 字节不变；无成功迁移声明 |
  | `Applied` 已偏离 `Baseline`，本地当前内容恰好等于 `Applied` | 仍当作用户内容保护，不能误删 |
  | 旧文件为 user-owned，或本地存在但无管理记录 | 内容保留，诊断可见，无删除动作 |
  | metadata 3、4、5 | 先只读预览，实际同步后为 6；保留既有所有权与摘要校验，冲突实例不迁移 |
  | 预览后插入 gitlink 或嵌套仓库 | 实际命令重新检查并拒绝，受保护路径没有写入 |
  | 仍由旧 CLI 运行 `skills list`/`doctor` | 不声称已获得新 Bundle；修改模板源或另一个二进制不会自动更新实例 |

- [x] **Step 6：核对文档、来源与交付范围。** 更新构建文档，解释模板源、运行 CLI 内嵌 Bundle、项目 metadata 三者的区别；说明源码生成器接口需配套更新，不能宣称旧已发行程序支持新增字段。保留发布记录，归档不入库，不安装到真实用户项目。
- [x] **Step 7：收尾实际验证。** 重跑受后续改动影响的临时定向用例，检查 `git diff --check` 和实际文件范围；清理本任务临时用例与实例，保留用户指定的构建输出。交付说明真实成功/失败场景、来源标识及未验证平台，不将 cross-compile 或编译成功写成原生运行成功。

## 执行顺序与完成条件

按 Task 1→2→3→4→5 顺序执行。A 批可使用合成 policy 验证，不依赖主线程完成；B 批必须取得稳定模板输入。若只能完成 A 批，应明确“核心改动完成，模板集成和真实迁移未完成”，不能宣称本计划完成。

完成意味着：显式依赖和安装模式行为通过临时断言；Git 边界在安装、事务和恢复中得到实际验证；新 Bundle 来源真实、四种安装与退役迁移满足表中预期；受影响文档同步。提交、推送、发行和用户实例升级均不属于完成条件。

## 计划自查

- 六类审查结论分别落入任务 1（依赖、模式、计数）、2（Git 保护）、3（诊断）、4–5（模板与 Bundle）及各任务文档步骤，没有单独增加维护框架。
- JSON 字段与 Go 接口在前文统一定义，旧归档新增字段采用可省略形式，并有真实旧归档摘要回归；任务之间不使用未定义的命令。
- 五项 Review Focus 都有对应输入与断言；保护用户修改、非受管文件和旧 metadata 没有因退役而放宽。
- 侧会话最初仅编写计划；当前复选项依据下方主任务执行记录更新，不代表已提交或发布。


## 主任务执行记录（2026-11-01）

CLI 工作区为 `/Users/zhudaoming/.codex/worktrees/cli-template-convergence/yss-cli`，分支 `codex/cli-template-convergence`，基线及 HEAD 均为 `8c5e1a90ff83ac83a0640a64b8c8109c3fc5a5e6`。原 CLI 主仓仍为 main，只有接手前的未跟踪计划目录，原内嵌归档未变。模板输入为本计划明确的 matt-skills-convergence 工作树，实际 HEAD `e8e4fdb5e59bccae0935e390e44077b7d6e888d8`，状态 `working-tree`。

| 范围 | 实际结果 |
| --- | --- |
| 依赖和安装视图 | 新行为先复现失败再修复；显式声明、模式资源隔离、可选依赖全图校验、环检测、闭包计数及原始 schema 6 归档校验通过 |
| Git 保护 | 空/不存在 gitlink、仅 `.gitmodules`、空格/中文路径、嵌套仓、损坏/缺失 Git、环境污染、预览后变更、事务恢复及 `.git` 根保护通过；显式初始化子仓和 detached worktree 允许 |
| 退役诊断 | `SKILL_RETIRED`、未知技能、退役声明合法性、未受管平台入口、不可用技能、固定排序与读取失败验证通过；不自动安装替代技能 |
| 模板集成 | 仅两份清单增加依赖与退役字段；剥离新增字段后与本轮前 JSON 完全相同。Spec 可选 58、Backend 可选 57；默认安装数分别 7/10、4/4。repository 闭包为自身加四项依赖，governance 为自身加 yss-dto |
| 新安装和切换 | 四组合均实际执行 init、attach、dry-run、ensure、双向模式切换；初始文件、依赖资源、平台内容、metadata 与 Bundle 核对，用户 README/CONTEXT 保留；修改过的切换入口拒绝覆盖 |
| 真实旧实例 | 在临时目录还原模板提交并用基线 CLI 生成旧归档，与原始保存归档逐字节相同；用该旧 CLI 安装四组合并补装四个旧技能。新 CLI 能清理未修改退役受管文件；规范入口/平台副本修改或 Applied 偏离 Baseline 时整体冲突，业务文件及 metadata 不变；user-owned 和无记录文件保留 |
| metadata 3–5 | 六个最小构造实例先通过历史 `ac65318` 的 reader，再验证新 CLI 预览不写、迁移到 6、保留用户内容、冲突不迁移和摘要损坏拒绝。它们是兼容格式夹具，不声称是真实用户历史实例 |
| 最终检查 | 临时四包 17 个定向 Test* 全部通过；60 条真实 CLI 场景记录通过；两仓 `git diff --check` 通过。没有新增持久测试套件或脚本 |

最终 Bundle 归档 SHA-256 为 `33b090ff8d4a9469b00d119c84c7f5371c2e6fab15f411117fe2783bf05d7a0a`，schema 6，压缩 2,951,087 bytes；记录实际 working-tree 来源，未改写发行标签。使用 Go 1.27.1 在 darwin/arm64 按顺序运行 Go Bundle 生成器及 `CGO_ENABLED=0 go build -trimpath`，实际执行新 CLI 的 version/help。生成归档保留为忽略产物。

### 审查修正与实现裁定

- 一次独立审查发现 1 项 Important/P1：macOS 上 `.AGENTS` gitlink 被 `.agents` 写入路径绕过。新增 index/gitmodules × 存在/不存在挂载点的 8 条断言先失败，修复为逐组件 `EqualFold` 后全部通过；4 个真实 CLI 组合及审查员原始场景也拒绝写入。该保护在大小写敏感卷上同样保守拒绝仅大小写不同的挂载路径，已写入兼容说明。没有剩余已确认的 Critical/Important。
- `.git` 内部目录不能通过显式 root 绕过相对路径保护；新增复现后修复，普通子仓/worktree 行为仍通过。
- 临时兼容夹具包含对应版本的 Bundle 字段、安装范围和 unavailable 基线来源；未仅更换 schema 数字。skills 模式下不再分发的 user-owned README 会退出管理，但本地字节保留，这是原有同步语义；临时断言据此修正。
- 迁移副本复制真实旧 CLI 的业务文件与 metadata；完成态事务归档留在原根，因为事务绑定绝对路径。待恢复行为另在原根通过真实事务验证。
- 按仓库约定使用仓外临时用例、记录、实例和二进制，结束后清理；实施工作树不保留检查框架。用户指定范围之外的模板改动、原 CLI 工作树和安装实例均未改写。

未提交、推送、发布或安装到用户工具目录；未验证 Linux/Windows 原生运行、完整历史治理实例或真实用户工程升级。
