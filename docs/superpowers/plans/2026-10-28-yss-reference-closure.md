# YSS 初始化引用闭包实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` when implementing this plan inline. Use checkbox (`- [ ]`) steps to track work; independent tasks may be delegated with nonoverlapping write scopes and reviewed before integration.

**Goal:** 修复 Spec / Backend 各安装方式中的必要入口和条件依赖，使实际启用的能力可读取正确资产，同时保留独立技能与治理安装的边界。

**Architecture:** 在模板源修正 canonical 内容、分发清单和 Backend overlay，使用现有分发、技能投影和按需补装机制；用小型离线回归覆盖可判定的合同，再从同一不可变候选模板树构建 Bundle，在隔离实例中验收四种安装和相关条件功能。现有实例同步与正式来源合同保持不变。

**Tech Stack:** Node.js 24、Go 1.27.1（`go.mod`）、Git、JSON/YAML、现有 YSS CLI 与模板检查脚本。

**Spec:** [已批准设计](../specs/2026-10-28-yss-reference-closure-design.md)。

## 全局约束

- 以下 `TEMPLATE_ROOT`、`CLI_ROOT` 和 `AUDIT_ROOT` 是执行时指向隔离工作树与仓外新目录的绝对路径；保护用户现有未提交内容，先检查两仓状态，再按现有 worktree 流程隔离。`AUDIT_ROOT` 不得复用既有证据目录。
- 默认 `skills` 不安装 `.template-source`、`.template-spec` 或治理身份；`governed` 不安装 `.template-source`。历史政策不改写，不恢复数字人角色或旧前端绑定，不凭文本路径匹配安装全部资产。
- 模板共享技能改 canonical `.agents/skills`；Backend 使用内核、`profiles/_specialist`、`profiles/backend` 的组合及现有同步器，不手改生成投影。
- Go 测试使用 `t.TempDir()`、小型内联 fixture、不联网；单测试约 1 秒、单包 30 秒、全量 `-timeout 5m`。真实 Bundle 的四种初始化和竞品场景放在独立候选验收，不在每个 Go 测试中重建真实模板。
- 本地候选不得覆盖正式来源锁或归档摘要，不冒称 committed 或正式发行；提交、推送、打 tag 和发布须当前会话另有授权。正式发行证据不由普通测试产生。
- 使用符合 `go.mod` 的 Go 工具链完成 Bundle 和 Go 验收。缺工具、Python `jsonschema` / `referencing` 或旧来源跳过均记录为未完成，不作为通过。

## Review Focus

1. 已安装的 `yss-research` 再次 ensure 时，其治理资源闭包仍需补齐；见任务 3、6。
2. 缺少模板或模块时不得产生部分竞品输出，也不能把普通研究降级为治理流程；见任务 3、6。
3. Backend 当前政策必须消费真实用户回复；被排除的旧角色表、空白模板和历史字段不能自行构成批准；见任务 2。
4. 相对链接、条件分支、动态路径、可选配置和运行数据不能被一条通用文本正则等同为初始化必需；见任务 1、4、6。
5. 候选来源、计划后用户修改、旧实例安装方式及保护文件必须保持绑定；旧 Bundle 的跳过或通过不能证明候选；见任务 5、6。

## 文件与责任

- 模板源 `.template-source/distribution/bundle-profile.json`：Spec 初始与按需资源合同。
- 模板源 `.agents/skills/yss-product-lifecycle/`、`yss-design-system/`、`yss-research/`：实例与模板维护边界、条件研究入口；现有同步器产生平台投影。
- 模板源 `profiles/backend/.template-spec/`、`profiles/backend/.agents/skills/` 及共享实现合同技能：Backend 现行政策、跨端边界与历史兼容。
- 模板源 `tests/spec-standalone-fixture.test.mjs`、`tests/spec-role-free-guidance.test.mjs`、`tests/frontend-removed.test.mjs`、`.agents/skills/yss-research/tests/competitive-scenarios.test.mjs`：各自已有的针对性验证；按实际需要补充紧凑测试。
- CLI `internal/bundle/`、`internal/project/` 和 `internal/cli/`：只在现有分发或选择机制不能实现合同的情况下修改；相邻 `*_test.go` 负责对应小型回归。
- 仓外 `AUDIT_ROOT`：候选锁、Bundle、隔离二进制、临时实例和实际检查记录；不得把它当作正式发行包。

## 任务 1：Spec 治理入口与模板维护边界

**文件：** 修改模板源 `.template-source/distribution/bundle-profile.json`、`.agents/skills/yss-product-lifecycle/SKILL.md`、`.agents/skills/yss-design-system/SKILL.md`；测试使用 `tests/spec-role-free-guidance.test.mjs` 和 `tests/spec-standalone-fixture.test.mjs`。

**接口：** Spec `common.files` 指定治理初始入口；`skills` 入口仍使用技能内部普通参考，不取得模板治理目录。

- [ ] **写失败检查。** 在现有测试中检查：Spec `common.files` 包含 `.template-spec/process/agent-entry-policy.md`；生命周期的项目治理事实源不要求 `.template-source/process/maintenance-intensity.yaml`；设计系统的项目实例更新说明不要求写 `.template-source/design/design-system-sync.yaml`，模板源条件可以保留。普通 starter 继续只解析已安装技能内的参考。
- [ ] **确认红灯。** 在 `TEMPLATE_ROOT` 运行 `node --test tests/spec-role-free-guidance.test.mjs tests/spec-standalone-fixture.test.mjs`；记录新增检查因现有清单或说明失败，其他失败如实记录。
- [ ] **修改。** 将入口文件加入 Spec `common.files`；把维护强度和设计系统同步要求分别限制于模板源场景。检查入口必读依赖，对后续条件依赖使用现有阶段或技能补装，不将文件内所有链接一律升级为 common。
- [ ] **验证。** 重跑上述两项测试；断言新增检查通过。实际候选中的入口和条件依赖由任务 6 再验证。

## 任务 2：Backend 当前治理政策与跨端引用

**文件：** 修改 `profiles/backend/.template-spec/process/agent-entry-policy.md`、`profiles/backend/.agents/skills/harness-orchestrator/SKILL.md` 和 `references/user-decisions.md`；检查并按实际组合更新 `profiles/backend/.template-spec/user-guide/产品生命周期工作流.md`、`.template-spec/templates/vertical-slice-ticket-template.md`、`.template-spec/templates/subagent-task-package-template.md`、共享及 Backend `yss-implementation-contract-compiler`、Backend `yss-stage-decision` 和 `profiles/backend/.template-spec/process/orchestration/domain/planning.yaml`。修改 Backend 分发清单时使用 `profiles/backend/.template-source/distribution/bundle-profile.json`。

**接口：** 当前用户决定政策为 `.template-spec/process/orchestration/domain/review.yaml#user_decision_policy`，schema 与 verifier 使用其 `user_decision_evidence`；历史角色表只供旧政策读取。Backend 职责由 `harness-profile.yaml`、战略交接和交付预检合同定义。

- [ ] **写失败检查。** 扩展 `tests/spec-role-free-guidance.test.mjs` 或新增 `tests/backend-reference-boundary.test.mjs`，使用现有 `composeProfileTree` / 分发判断，对实际分发的现行入口检查：不要求 `.template-spec/agents/digital-human-roles.yaml`；用户决定协议引用 `review.yaml#user_decision_policy` 及现行 schema；跨端入口不引用不存在的 `.template-spec/process/frontend-backend-delivery.md`。保留历史分支检查，不能全局删除旧格式支持。
- [ ] **确认红灯。** 执行 `node --test tests/spec-role-free-guidance.test.mjs tests/frontend-removed.test.mjs`，以及新增测试文件（若创建）；记录具体旧引用导致的失败。
- [ ] **修正用户决定。** 根据 `review.yaml` 与 `scripts/verify-user-decision` / 原生消费者调整现行入口、工作流、两种任务模板及 Backend 用户决定协议；不增加会签或批准。由于协议现要求空白 `.template-spec/process/templates/user-decision-template.yaml` 且该模板明确不是批准证据，从 Backend `excludePaths` 移除该路径并随治理实例分发；保留 `responses: []` 与真实回复验证，不分发旧角色表。
- [ ] **修正跨端及迁移。** 根据 Backend `harness-profile.yaml`、`strategic-handoff-package.md` 和 `delivery-preflight.md` 修正两个 Profile 实际分发的跨端引用；已退役 `frontend_delivery` 不恢复，历史记录仍可读取。Backend planning 合同和技能声明支持旧 Plan 按当前模板重整；共享 `plan-migration.md` 又包含 Spec 四阶段语义，因此为 Backend 在 `profiles/backend/.template-spec/process/plan-migration.md` 提供同路径覆盖，仅表达当前 Backend 合同支持的重整、引用和校验规则，并从 Backend `excludePaths` 移除该路径。验证当前 Plan 模板/合同依赖；不能把旧批准当成新批准或套用 Spec 专有门禁。
- [ ] **验证。** 重跑上述测试及 `node --test tests/backend-delivery-terminal.test.mjs`；使用仓库已有用户决定场景检查无真实回复时拒绝、有效回复按原合同消费。记录实际场景命令和退出码，不将空白模板当作批准。

## 任务 3：审计研究的条件依赖

**文件：** 修改 `.agents/skills/yss-research/SKILL.md`、`references/governed-research.md`；必要时修改 Spec `bundle-profile.json` 的阶段或技能资源及相邻 CLI 小型选择测试。场景样本复用 `.agents/skills/yss-research/tests/competitive-scenarios.test.mjs`。

**接口：** 普通研究不读取治理资产。审计式竞品研究需可信治理根，使用 `competitive-matrix-template.md`、`competitive-analysis-template.md`、`scripts/lib/json-schema.mjs`、`validation-phase.mjs` 和递归导入；`skills ensure yss-research` 在治理安装下补资源，即使技能本身已经安装。

- [ ] **写失败检查。** 增加小型选择 fixture：已安装技能但缺其治理资源时，ensure 计划包含缺失资源；应用后资源可读；独立 `skills` ensure 不产生 `.template-spec`。为现有竞品场景增加缺模板和缺模块的负向检查：失败有原因且不创建部分输出。
- [ ] **确认红灯。** 运行新增对应的定向 Node 测试和 `go test -tags yss_nobundle -timeout 5m ./internal/project ./internal/bundle`；记录失败或工具不可用，不用跳过代替红灯。
- [ ] **修改。** 在研究入口写清普通和审计式分支及治理补装前提。检查现有 `stage.plan` 的模板前缀和 `SkillRequirements` / `SelectSkillResources` 对脚本模块及动态模板的处理；只对实际缺失的资源添加显式清单或修复补装逻辑，不往默认 `skills` 复制治理目录。
- [ ] **验证。** 重跑定向测试；候选治理实例上的真实渲染、校验和缺依赖场景留给任务 6。不得声称一个仅检查路径存在的 fixture 已验证完整运行链。

## 任务 4：生成投影与模板定向验证

**文件：** 修改仅由现有工具生成的模板投影和锁；使用 `.template-source/agents/skills-maintenance.md` 与 `scripts/verify-template-fast`。

**接口：** `.agents/skills` 是共享内容事实源；Backend 同步器按现有三层组合生成 Profile 投影。

- [ ] **生成并核对。** 在 `TEMPLATE_ROOT` 依次运行 `scripts/sync-skills`、`scripts/update-skill-lock`、`node .template-source/scripts/manage-profile-skills.mjs sync --profile backend`、`node .template-source/scripts/manage-profile-skills.mjs lock --profile backend`；检查 diff 仅包含对应 canonical、投影和锁的预期变化。
- [ ] **校验。** 执行上述四个命令各自的 `--check` 形式，以及 `scripts/verify-template-fast --plan`，按输出中与本次变更有关的检查执行；另运行任务 1–3 的定向检查。若计划缺映射或环境输入，记录并修复映射后重跑受影响项，不宣称全量发行验收。

## 任务 5：建立可追溯的候选 Bundle

**文件：** 不修改正式 `docs/source-lock.json` 和已跟踪归档摘要；候选锁、捕获索引及归档仅写入 `AUDIT_ROOT`。使用 `tools/bundle`、`internal/bundle/producer.go`、`internal/identitymeta/native.go`。

**接口：** 当前 source lock schema 5；每个 Profile 的 `sourceState=working-tree`、`sourceTree=<真实 Git tree>`、`templateVersion=worktree:<同一 tree>`、`templateCommit=<真实基线 commit>`，producer 也是 `working-tree`；每个 `policyHash` 是该 tree 内各自 policy 字节的 SHA-256。当前锁的正式 `templateTag` 必须从候选副本移除，若存在 `skillsSource` 也须真实绑定其 working-tree 对象及摘要。

- [ ] **捕获来源。** 在隔离模板工作树用独立 `GIT_INDEX_FILE` 从其真实 HEAD 建临时索引，加入本次候选文件并 `git write-tree`，不改用户索引；记录 HEAD、tree、工作树差异和两份 policy 文件的 tree 内 SHA-256。模板发生后续改动则重新捕获并重新构建受影响证据。
- [ ] **构建锁。** 从正式来源锁复制到 `AUDIT_ROOT`，保留两个 Profile、overlay 和真实基线 commit，按上面的接口设置 working-tree 字段、删除 `templateTag`、重算 policy 摘要；不得修改正式锁。运行 `go run -tags yss_nobundle ./tools/bundle --source-root "$TEMPLATE_ROOT" --lock "$AUDIT_ROOT/source-lock.json" --out "$AUDIT_ROOT/bundle"`；核对输出两个 Profile 的 tree 版本、sourceState 和 Bundle 摘要。
- [ ] **构建实际候选 CLI。** 仅在隔离 CLI 工作树提供候选归档，记录归档 SHA-256 和 CLI 真实 HEAD；按实际构建来源使用 `-ldflags` 设置 `github.com/iloveZzz/yss-cli/internal/domain.BuildCommit` 和 `BuildSourceState=working-tree`，执行 `CGO_ENABLED=0 go build -o "$AUDIT_ROOT/yss" ./cmd/yss`。运行 `"$AUDIT_ROOT/yss" version --json` 核对真实 working-tree 来源。`tools/build-candidate.mjs` 要求正式摘要一致，不用它把未提交候选伪装为正式构建。

## 任务 6：真实初始化、补装与同步验收

**文件：** 复用 `internal/cli/installation_integration_test.go`、`tests/cli-retirement.test.mjs` 和竞品场景作方法与数据参考；新增验收驱动（如需）位于隔离模板仓 `tests/initialization-reference-closure.test.mjs`，它仅消费显式候选二进制路径和 `AUDIT_ROOT`，不下载或发布。

**接口：** 每次 `init` 使用新目录；`assets ensure` / `skills ensure` 先 `--plan --out <新绝对路径>`，再 `--apply --plan-file <同一路径>`。实例 `.yss.json` 必须绑定任务 5 候选的 `templateSourceState`、`templateVersion`、`templateCommit` 和 `distribution.installation`。

- [ ] **四种初始化。** 分别对 `spec`、`backend` 执行默认 `skills` 和显式 `--installation governed` 初始化；每个实例核对上述来源元数据。`skills` 中 `.template-spec`、`.template-source` 和治理身份文件不存在；两种 `governed` 无 `.template-source`，Spec 的 `agent-entry-policy.md` 可读，Backend 的角色政策、决定模板与跨端入口满足任务 2 合同。
- [ ] **治理补装。** 在 Spec 治理实例执行 `assets ensure stage.spec-architecture` 的 plan/apply，验证相应 Spec 模板；对已安装的 `yss-research` 执行 `skills ensure` plan/apply，验证任务 3 声明的模板和递归模块。计划不得擅自写实例；补装后通过现有查询确认技能及路径，不把空计划当作缺失资源已安装。
- [ ] **运行竞品场景。** 在该实例复用既有 `both` 有效输入运行竞品渲染和校验，断言两份产物成功；在两个各自没有输出产物的新隔离副本中分别移除一个模板、一个传递模块，断言非零、诊断指向缺失且无部分输出。普通研究的独立入口不加载该场景；无治理上下文时入口说明明确需要治理而不假造 CLI 错误码。
- [ ] **验证用户决定与同步保护。** 用既有有效/无真实回复场景检查 Backend 现行验证链；对两个安装方式做同步 plan/apply，核对安装方式、用户文件和历史记录保持；在计划后改变受管输入，断言 apply 拒绝过期计划并保留该用户修改。
- [ ] **核验证据。** 每个结果绑定候选 Bundle、二进制、模板 tree、命令和退出码。真实候选被覆盖为旧 pin 或环境变量指定其他二进制、旧来源测试被跳过、Python 校验依赖缺失均标记未完成；候选 override 不等于正式 pin 或发行通过。

## 任务 7：受影响回归与交付

**文件：** 两仓本次 diff；`docs/ci-verification.md` 和模板 `scripts/verify-template-fast --plan` 为验证规则来源。

**接口：** 结果区分实现、测试、构建、候选初始化和正式发行资格；后者不由本计划授予。

- [ ] **执行相关检查。** CLI 行为变化执行 `gofmt -l`、`go test -tags yss_nobundle -timeout 5m ./...`；具备候选 Bundle 后执行 `go test -timeout 5m ./...`、`CGO_ENABLED=0 go build ./...` 和 `go vet -composites=false ./...`。候选二进制受影响时按现有 `node tools/smoke/main.mjs --bin <候选>` 冒烟；其通过只证明声明的退出码和 envelope，不替代任务 6。
- [ ] **审阅和报告。** 对照设计和两仓 diff 检查每个引用的分类、生成投影、来源及兼容性；记录实际命令、结果、未覆盖范围和环境阻塞。用户未授权时不提交、推送、打 tag 或发布。
- [ ] **正式来源条件。** 普通模板提交不自动刷新 CLI；只有已获得授权、模板新 tag 符合发行资格时，先运行 `node tools/refresh-source-lock.mjs --source-root "$TEMPLATE_ROOT" --profile all --tag <新tag>` 预演，再遵循工具帮助及仓库流程更新来源锁和重建产物。若条件未满足，保留工作树候选和未完成状态。
