# Home Repo 多项目协调与跨宿主 Skills Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让产品 Home 能按正确工程、工作树与技能版本协调多个项目，并补齐五宿主适配说明及经验证的原生技能入口。

**Architecture:** Template 继续维护共享技能与 Profile 覆盖，CLI 在有效 Profile 内容形成后派生必要宿主视图。Home 复用已有仓库地图与任务记录；新入口沿用现有安装事务，并阻止与保留的本地 canonical 定制静默分叉。宿主发现与工具能力分别实测，不引入中央注册或进程调度层。

**Tech Stack:** Rust/Cargo `1.99.1`、现有 Bundle/安装事务、Markdown、JSON；临时验证使用现有 Python 3、Node 和 Git。本轮不修改前端应用。

**Spec:** [完整设计](../specs/2026-11-15-home-repo-multi-project-host-skills-design.md)。执行者先读设计与本计划，再检查两仓当前状态。

## Global Constraints

- 保持 Profile 为 `spec / backend / frontend`，安装类型为 `project / skills`；默认技能数量分别为 `17 / 42 / 13`。
- 一个 YSS 安装根只使用一个 Profile；`skills` 安装也不支持把多个 Profile 合并进同一收据。
- 不新增命令、Profile、收据 schema、宿主注册表、常驻同步服务或跨宿主进程调度器；不新增运行时依赖。
- 共享技能源保留在 `.agents/skills`；Backend 保留文件级覆盖；工程内保存完整已采用载荷及合法收据。
- 不改技能名称、不把工程技能批量搬入 Home 或用户全局目录；不通过删除收据、锁或事务绕过保护。
- 保留现有 `.workbuddy` 普通文件副本；新增宿主视图只能来自当前 Profile 的有效 `.agents` 内容，不增加新的手工内容源。
- 保留未知文件、用户定制、摘要、安装所有权、事务及并发保护；远端与本地路径必须在实际执行环境解析。
- 不新增持久测试套件、包装脚本或检查资产；必要探针放在仓库外临时目录，交付记录实际证据及未验证范围。
- 保留两仓已有未提交改动；本轮不修改真实业务仓、用户全局配置或已安装 CLI。不包含提交、推送、部署、正式发行；历史标签记录不改。

## Review Focus

1. **RF1：同名 Profile / worktree 技能串用。** Backend 必须取得自己的覆盖及专有资源，Frontend 只取得白名单；旧主 checkout 不能遮蔽目标 worktree。由 Task 3、4 验证。
2. **RF2：新增视图碰到未知文件或本地 canonical 定制。** 整次操作冲突且不写入，不能接管同字节未知文件，也不能静默产生两种内容。由 Task 3 验证。
3. **RF3：路径有空格、资源相对路径、同仓多根及缺少挂载。** 资源基准、命令 cwd 和工程身份准确，不越过子模块或使用错误目录；父子根没有自动隔离承诺。由 Task 4、5 验证。
4. **RF4：输入变更、任务中断、mock 或部分工程完成。** 恢复后核实新输入，只重做受影响部分，整体完成必须有必要集成证据。由 Task 1、5 验证。
5. **RF5：文件已安装但宿主未注册、工具不可用或缓存未刷新。** 分开报告，允许明确的显式读取/串行方式；不能虚报五宿主原生成功。由 Task 2、4 验证。

---

## 文件结构与职责

以下缩写仅用于本计划，不是新增产品配置：

- `C=/Users/zhudaoming/.codex/worktrees/home-git-skills-cli/yss-cli`
- `T=/Users/zhudaoming/.codex/worktrees/home-git-skills-template/yss-spec-project-template`
- `P` 为执行开始时用 `mktemp -d` 创建的仓库外临时探针目录；命令中的 `$probe_root` 指向它。

执行时设置 `template_root` 为上述 T，工作目录为 C；`baseline_yss` 指向 `P/baseline/yss`，`candidate_yss` 指向 `C/target/aarch64-apple-darwin/release/yss`。Task 2 后设置 `wb_projection` 为其实际结果值。临时脚本的入口分别为 `check_bundle.py <bundle-path> <workbuddyProjection>` 和 `check_install.py <baseline-yss> <candidate-yss>`，脚本执行失败必须以非零退出码结束。

| 文件 | 操作与责任 |
|---|---|
| `T/.agents/skills/setup-yss-harness/SKILL.md` | 修改：导航到便携性、地图和宿主参考 |
| `T/.agents/skills/setup-yss-harness/references/project-portability.md` | 修改：工程身份、布局、输入版本和运行边界 |
| `T/.agents/skills/setup-yss-harness/references/repositories-template.md` | 修改：稳定工程地图及多 Backend / Web / Mobile 例子 |
| `T/.agents/skills/setup-yss-harness/references/host-compatibility.md` | 新增：五宿主发现/执行边界与实际证据 |
| `T/.agents/skills/implement/references/repository-scope.md` | 修改：委派、恢复、输入失效、共享资源、反馈与部分完成 |
| `T/.template-spec/agents/profile-skills.md` | 修改：职责和跨工程路径；同步 Backend、Frontend 同路径副本 |
| `T/.template-spec/user-guide/cli/tutorial-spec.md` | 修改：产品 Home 多项目例子 |
| `T/profiles/_specialist/.template-spec/user-guide/cli/tutorial-spec.md` | 按自身内容增量修改，不覆盖成根教程 |
| `T/.workbuddy/skills/setup-yss-harness/`、`T/profiles/backend/.workbuddy/skills/setup-yss-harness/`、`T/profiles/frontend/.workbuddy/skills/setup-yss-harness/` | 同步上述 Setup 4 个文件，保持普通完整副本 |
| `T/.workbuddy/skills/implement/references/repository-scope.md`、`T/profiles/backend/.workbuddy/skills/implement/references/repository-scope.md` | 同步共享叶子，不改 Backend 专有规则 |
| `T/profiles/backend/skills-lock.json` | 只重算 Setup / Implement 的有效内容哈希，保留上游字段 |
| `T/profiles/backend/.template-source/distribution/bundle-profile.json`、`T/profiles/frontend/.template-source/distribution/bundle-profile.json` | 放行 Claude 技能视图，保持默认集合和其它目录保护 |
| `T/.template-source/distribution/bundle-profile.json` | 仅在 WorkBuddy 探针要求 `.codebuddy` 时，与其它两个清单一起增加限定视图和对应 renderPaths |
| `C/src/bundle/build.rs` | 新增私有视图派生函数；在 Profile 覆盖之后调用 |
| `C/src/project/plan.rs` | 新增首次视图与保留 canonical 的冲突检查，复用已有计划数据 |
| `C/src/bundle/selection.rs` | 仅在 `.codebuddy` 分支触发时扩展既有路径识别 |
| `C/docs/skills-discovery.md` | 分发、升级冲突、显式读取与原生发现的真实边界 |
| `C/docs/build-install.md` | 仅按本轮实际变化补构建/采用说明，不改历史发行事实 |
| `P/check_bundle.py`、`P/check_install.py`、`P/fixtures/`、`P/results/` | 临时验证及证据；不加入源码仓或分发包 |

不用修改 `src/project/state.rs`、命令定义、archive schema、依赖锁、UI、默认技能成员或退役命令。已有 `.claude` 根链接不改；不新增 Backend Setup 主源或 Frontend 技能主源。

## Task 1：补齐 Home 多项目工作方法

**Files:** 上表 Setup 已有三文件、共享 `repository-scope.md`、三份能力地图、两份教程及对应 WorkBuddy 副本、Backend 锁文件。

**Interfaces:**

- Consumes：设计 §3–5 的工程身份、输入版本和职责规则；当前仓库地图格式及安装语义。
- Produces：三个 Profile 均可通过自己的 Setup 获取通用方法；Spec/Backend Implement 获得详细工程执行规则。Frontend 不需要安装 Implement 或引用其叶子。

- [x] **Step 1：记录执行基线。** 在两仓执行 `git status --short`、`git rev-parse HEAD`；把真实差异和必要未跟踪输入摘要保存到 `P/results/baseline.json`。若上一轮改动已被合并，按实际状态继续，不将记录中的旧 HEAD 当回滚目标。另保存现有 Bundle 与已验证候选二进制供升级对照，记录摘要；不覆盖安装到用户路径的程序。
- [x] **Step 2：修改 Setup 通用参考。** `project-portability.md` 增加单根单 Profile、并列目录/同仓多工程、稳定身份与临时 worktree、父子根无跨根锁；`repositories-template.md` 给 Home、两 Backend、Web、Mobile、共享 UI 的一张关系表，表中只保存稳定关系。新 API 引用必须指向权威工程，不能复制一份。
- [x] **Step 3：修改执行范围参考。** 在现有 `repository-scope.md` 增量加入“目标/工程/输入/技能/责任/结果”的委派信息，输入变化后的受影响结果失效、简短恢复记录、端口/服务/输出归属、mock 与部分完成、条件性的发布顺序。不给普通任务增加合同、审批包或强制记录流程。
- [x] **Step 4：连接能力地图和教程。** 三份 `profile-skills.md` 保持内容一致；根教程展示多项目步骤；specialist 教程保留自身裁剪，补回到 Home 的反馈方式。所有 Profile 的共用链接指向 Setup 自身参考；Frontend 不新增 Implement 叶子依赖。
- [x] **Step 5：同步有效副本与哈希。** Setup 的 3 个 WorkBuddy 副本等于共享源；Spec Implement 等于共享源，Backend Implement 等于共享源叠加 Backend 覆盖。按现有维护说明，用 Node `localeCompare` 排序、路径 NUL 内容 NUL、文本 CRLF 归一化及规定忽略项计算 Backend 两个 effectiveHash；不改 upstreamHash，不给其它锁补字段。
- [x] **Step 6：逐场景审阅新方法。** 用设计 §8 的场景逐条走读：同仓两个 backend、目标 worktree 与主仓同名技能、未提交 OpenAPI 变更、只有 Web mock 完成、恢复后端口已被别人占用。预期每例均能明确“选谁、读什么、能改哪里、尚缺什么”；此处不写匹配文案字符串的持久测试。检查包内链接实际存在，Setup 的副本逐字一致（RF3、RF4）。

## Task 2：确定五宿主路径与 WorkBuddy 分发分支

**Files:** 新增 `T/.agents/skills/setup-yss-harness/references/host-compatibility.md`，更新 Setup 入口及其 3 份副本、Backend Setup effectiveHash；临时 `P/fixtures/workbuddy-*`、`P/results/host-preflight.*`。

**Interfaces:**

- Consumes：设计 §5 的四行 WorkBuddy 决策表、§6 的五宿主矩阵；已有客户端公开帮助。
- Produces：宿主参考，以及 `P/results/host-preflight.json` 的本次事实字段 `host`、`version`、`entry`、`nativeDiscovery`、`explicitRead`、`evidence`；`workbuddyProjection` 只取 `keep-workbuddy`、`add-codebuddy`、`unverified`。这是临时证据，不是产品 schema。

- [x] **Step 1：复核版本和可用能力。** 对 Codex / Claude / Pi / DSH 使用本机 `--version` / `--help`；WorkBuddy 读取实际 App 版本。不得把 Claude Desktop 版本代替 Claude Code，不安装/升级客户端，不读取凭据以探测登录。
- [x] **Step 2：检查 DSH 子 profile 的实际参数。** 执行 `dsh --profile headless --help`，只记录公开参数；不要输出环境变量或配置内容。若无法确认新会话与可用工具控制，后续用 GUI 新会话和合成项目；不猜 `--json`、`--cwd` 或 `--read-only`。
- [x] **Step 3：用 WorkBuddy GUI 做三组原生发现探针。** 新建三份一次性项目，分别只有 `.workbuddy/skills/lookup-sample`、只有 `.codebuddy/skills/lookup-sample`、两者同名但标记不同；每组使用独立新会话。`SKILL.md` 指向 `references/marker.txt`，真正标记只在文件里，提示词不得泄露答案。保存技能列表/调用与资源读取证据及准确工作目录；没有导出能力时用可见截图。原生调用必须证明来自目录注册，显式文件读取另记。
- [x] **Step 4：按设计表确定唯一分支。** 当前 `.workbuddy` 能原生工作即 `keep-workbuddy`；只有 `.codebuddy` 成功才 `add-codebuddy`；不可用或证据不充分为 `unverified`。不因官方文档有路径就判定本机成功，不因浏览器找不到页面就判定客户端失败。结果直接决定 Task 3 是否改 selection 和 Spec 清单，不新增运行时开关。
- [x] **Step 5：写宿主参考并闭合分发。** 列出版本基线、工程根/外部路径、同名来源、刷新方式、规则与资源基准、工具权限边界。Codex 的 `--add-dir` 不写成注册技能；Pi/DSH 不增加镜像目录；WorkBuddy 只记录本机分支与限制。加入来源链接与检查日期，同步副本和哈希；未执行项明确保留（RF5）。

## Task 3：从有效 Profile 派生宿主入口并保护升级

**Files:** `C/src/bundle/build.rs`、`C/src/project/plan.rs`、Backend/Frontend 两份分发清单、`C/docs/skills-discovery.md`；Task 2 为 `add-codebuddy` 时再改 `C/src/bundle/selection.rs` 和三份清单的对应目录/renderPaths。临时验证脚本 `P/check_bundle.py`、`P/check_install.py`。

**Interfaces:**

- Consumes：现有 `BundleFile { descriptor, ownership, render }`、`SourceManifest`、`Bundle::select()`、`plan()` 内的 `operations` 和 `guards`；Task 2 的固定分支结果。
- Produces：私有 `fn add_skill_views(files: &mut BTreeMap<RelPath, BundleFile>, policy: &SourceManifest) -> Result<()>`，不变的 Bundle/收据格式；新增视图进入原有闭包、诊断、事务路径。
- Produces：私有 `fn new_skill_view_conflicts(operations: &[Operation], guards: &BTreeMap<RelPath, FileSnapshot>) -> Result<BTreeSet<RelPath>>`，在 `plan()` 原有路径遍历之后、汇总 `conflicts` 之前将结果加入现有冲突集合。不改变 `pub fn plan(bundle: &Bundle, request: &ProjectRequest) -> Result<ProjectPlan>`。

- [x] **Step 1：准备可失败的 Bundle 检查。** 临时 `check_bundle.py` 读取 `manifest.json` 和 `objects/<sha256>`，参数为 Bundle 路径与 WorkBuddy 分支。按以下断言执行旧 Bundle，预期 Backend/Frontend 缺少 Claude 视图失败，而默认数量和既有 Spec 检查通过。

```python
assert {p: len(m["profiles"][p]["installations"][k]["defaults"])
        for p in ("spec", "backend", "frontend")} == {
            "spec": 17, "backend": 42, "frontend": 13}  # k 分别为 project、skills
assert backend_files[".claude/skills/implement/SKILL.md"]["descriptor"] == \
       backend_files[".agents/skills/implement/SKILL.md"]["descriptor"]
assert backend_files[".claude/skills/database-analysis-docs/SKILL.md"]["descriptor"] == \
       backend_files[".agents/skills/database-analysis-docs/SKILL.md"]["descriptor"]
assert ".claude/skills/implement/SKILL.md" not in frontend_files
```

另外对三个 Profile 的每个 canonical 技能文件检查视图 descriptor 与 render、资源字节、对象存在性；有效 ownership 来自目标策略。包含 `yss-design-system` 两个渲染文件及可执行资源 mode。仅在 `add-codebuddy` 分支对 `.codebuddy` 重复同一检查（RF1）。

- [x] **Step 2：实现有效内容投影。** 在 `build()` 完成 shared → specialist → profile overlay 和 `objects/files` 构造之后、`installations` 之前调用 `add_skill_views()`。静态宿主集合仅 `.claude`，条件分支触发才包括 `.codebuddy`。快照遍历 canonical 后再插入目标，避免边遍历边修改；使用 `RelPath::new()` 校验路径并复用 descriptor。目标清单未放行则不生成；已存在且完整 `BundleFile` 相等则复用，不等或没有 canonical 对应来源则拒绝。对应 render 不一致也拒绝，不能生成未渲染的副本。不改 kernel、源软链白名单、对象内容或 archive 格式。
- [x] **Step 3：调整分发策略。** Backend/Frontend 的 `allowFiles` 限定放行 `.claude/skills`，删除相矛盾的该项排除，保留 `.claude` 其它配置保护和 canonical 白名单。`add-codebuddy` 时在三 Profile 仅放行 `.codebuddy/skills`，在 `skill_for_path()` 的已有目录枚举补 `.codebuddy`；Spec 为 `yss-design-system/SKILL.md`、`references/high-fidelity-default-theme.md` 补对应 `.codebuddy` renderPaths。其它 Profile 仍使用其自身 canonical 的渲染策略。
- [x] **Step 4：构建候选并验证投影。** 在 C 执行 `cargo run --locked --bin yss-bundle -- --source-root "$template_root" --out assets/bundle.tar.gz`，再运行 `python3 "$probe_root/check_bundle.py" assets/bundle.tar.gz "$wb_projection"`。预期全部断言通过；另在一次性模板副本制造不同字节的宿主同路径和无 canonical 的宿主文件，构建均应失败，不能污染真实 T。记录实际源 HEAD、working-tree 状态和摘要。
- [x] **Step 5：准备升级分叉反例。** 用基线 CLI 在临时 Backend 根创建旧合法安装，修改 `.agents/skills/implement/references/spec-feedback.md`（本轮不改该源叶子），使旧同步本来会保留此定制。临时 `check_install.py` 以参数接收基线/候选程序绝对路径，使用 `subprocess` 参数数组，不拼 shell；新增断言要求 `CONFLICT` 且零写入。
- [x] **Step 6：确认反例失败。** 确保当前 UI 构建产物可用，执行 `cargo build --locked --release --bin yss --target aarch64-apple-darwin` 取得加入投影但尚无保护的候选；用它对 Step 5 夹具执行同步预览。预期新断言失败，实际暴露“保留 canonical、创建不同 Claude 视图”；不执行这次有分叉的真实同步。
- [x] **Step 7：加入局部一致性保护。** `new_skill_view_conflicts()` 仅枚举拟 `Operation::Create` 的 `.claude/skills/<skill>/<file>`（及启用时 `.codebuddy`）。映射到 canonical，以 `guards` 为当前状态，再由已有 Create/Replace/Delete 操作得出 canonical 的最终 descriptor；与新视图 `RenderedFile` 的实际 bytes/mode descriptor 比较。不同或最终缺失时返回相关 canonical 与目标路径，调用方沿用原 `CONFLICT` 汇总。不得新读文件、写收据或复制用户定制；不改变其它三方冲突，不给已有视图建立持续联动；Sync/Ensure 共用此检查。
- [x] **Step 8：运行安装保护用例。** 重新执行 `cargo build --locked --release --bin yss --target aarch64-apple-darwin` 编入新保护。`python3 "$probe_root/check_install.py" "$baseline_yss" "$candidate_yss"` 必须覆盖下表；每个拒绝用例比较操作前后全部夹具文件字节/模式以及收据，预览额外比较文件清单，不能新增锁/事务（RF2）。

| 临时用例 | 精确预期 |
|---|---|
| 三 Profile × 两模式新建 | 默认 17/42/13；所选技能的 canonical 与新视图完整；doctor 无受管差异 |
| 从 `skills list` 选一个可用非默认技能再 ensure | closure 与引用资源完整，新视图随技能出现；Frontend 不出现 Backend 技能 |
| 干净旧安装 sync dry-run、真实 sync、再次 sync | 预览零写入；实际新增正确视图；随后 unchanged |
| 新视图路径已有未知文件，内容不同或恰好相同 | 两者都 `CONFLICT`，整次零写入，不自动接管 |
| 旧 canonical 修改且新模板该源未变，或本地删除 | 新增视图同步 `CONFLICT`；保留原文件/删除状态及旧收据 |
| 上一行通过 `skills ensure setup-yss-harness` 进入同一规划管线 | 仍 `CONFLICT`，不能绕开新保护 |
| 新模板改变 canonical，旧工程该文件未改 | canonical 与新视图同时升级，不被新保护误拒绝 |
| 既有受管宿主文件本地改动，新模板又改该文件 | 保留现有三方冲突语义，零覆盖 |
| 旧收据已有宿主文件，本地删除且上游未变 | 保留既有删除语义，不当成首次新增或自动恢复 |
| 不相关自建技能 / 自定义宿主配置 | 原字节与模式保留，不被 broad allow 纳入 |
| 一份合法新 Git clone | 载荷和收据可用，doctor 后 sync 无额外差异；runtime 文件不作为共享输入 |

- [x] **Step 9：更新采用说明。** `docs/skills-discovery.md` 写清新入口来自 Bundle 的有效 Profile，本地定制可导致迁移冲突，已有多个副本不会持续自动同步；普通任务按实际来源选择。对外不宣称新 release 已发布。检查 `.claude`/可选 `.codebuddy` 的跨技能引用仍能从工程 canonical 资源找到，不擅自按宿主改写已有绝对/根相对引用。

## Task 4：以真实宿主验证发现、工作树和资源来源

**Files:** 只更新 Task 2 的宿主参考、对应副本/哈希及 CLI 采用说明中的实测事实；夹具和日志在 P。若发现代码问题，回到 Task 3 对应逻辑修复并重跑受影响用例。

**Interfaces:**

- Consumes：候选 CLI 创建的安装和 Task 2 的宿主路径分支。
- Produces：`P/results/hosts.json`，逐宿主记录版本、cwd、载荷路径、nativeDiscovery、explicitRead、selectedSource、resourceRead、rulesRead、toolAvailability、证据路径；状态只用 passed/failed/unverified，不用单个“支持=true”掩盖不同层级。

- [x] **Step 1：生成可识别夹具。** Home、Backend 主 checkout、带空格路径的 Backend worktree、Frontend 各放独立规则标记、技能标记、reference 标记；主 checkout 与 worktree 同名技能内容不同。标记随机生成，不能出现在任务 prompt 的期望答案里。所用目标必须来自任务当前路径，不能只看 Git HEAD。
- [x] **Step 2：验证 Codex。** 从合成 Home 显式读取外部 worktree 的规则、`SKILL.md` 与其 reference；再从工程 cwd 单独验证 `.agents` 原生目录发现。沿用已核实的 `exec --ephemeral --sandbox read-only --json -o` 参数，保存命令与事件；只有实际注册/自动技能选择证据才标记 nativeDiscovery，通过显式路径读取不计入原生成功。确认版本后使用实际绝对程序路径（RF1、RF3）。
- [ ] **Step 3：验证 Claude Code。** 从 Home 使用真实目标 `--add-dir`；从实际注册目录限定命令调用同名技能，不猜命令前缀。再做去掉 Skill 工具的显式读取对照。新会话核实工作树、reference 和工程规则；仅加载技能不算规则已加载。

```sh
claude --print --no-session-persistence \
  --output-format stream-json --verbose \
  --permission-mode dontAsk --permission-prompts none \
  --tools "Read,Glob,Grep,Skill" --allowedTools "Read,Glob,Grep,Skill" \
  --strict-mcp-config --mcp-config '{"mcpServers":{}}' \
  --add-dir "$probe_target" "$probe_prompt"
```

执行器 cwd 为合成 Home。参数以 Task 2 复核为准；既有 hooks/管理策略仍可能生效，不能称为 OS 沙箱或完全配置隔离。若需要的工具被拒绝，保留失败原因，不放开无关权限。

- [ ] **Step 4：验证 Pi。** 在目标工程 cwd 先验证默认 `.agents` 发现（需要的项目信任仅限合成夹具）；从 Home 用以下明确子集调用 `/skill:lookup-sample`，再用普通 prompt 做显式读取对照。读取 stdout 的 JSON 事件序列，不能按单个 JSON 对象解析。

```sh
pi --print --mode json --no-session \
  --no-extensions --no-mcp --offline --no-skills \
  --skill "$probe_target/.agents/skills/lookup-sample" \
  --tools read,grep,find,ls "$probe_prompt"
```

`--offline` 仅关闭启动自动联网，仍会发模型请求；工具白名单不是 OS 沙箱。不要把本机扩展提供的 `--plan` 当成 Pi 原生只读参数。

- [ ] **Step 5：验证 DSH 和 WorkBuddy。** DSH 仅用 Task 2 核实过的 headless 参数或 GUI 新会话；先在工程 cwd 验证 `.agents`，Home 跨仓再验证已确认的临时 customSkillDirs 或显式读取，不改用户配置。WorkBuddy 用 Task 2 同一实际模式验证候选安装及外部工程读取。两者记录真实 skill provider/tool 和 GUI/事件证据，禁止套用 Claude 参数（RF5）。
- [x] **Step 6：核对同名与缓存反例。** 对实际能运行的每个宿主，旧主 checkout 标记不得出现；reference 与规则必须各自正确。修改目标技能资源后用该宿主已确认的 reload 或新会话重读，确认新标记；外部目录不存在时应报告访问失败，不回退主仓。缺登录/模型/工具时保留 unverified 和原因，不批量宣称成功。
- [x] **Step 7：回写真实证据并检查闭包。** 更新宿主参考的版本/日期/能力分层，记录仍未验证的平台；同步 Setup 的 3 份 WorkBuddy 副本及 Backend effectiveHash。若文案修订改变了 Bundle 输入，Task 5 必须重新生成最终候选，不能把旧摘要写成最终结果。

## Task 5：验证多项目任务闭环并完成交付核对

**Files:** 本轮已有内容文件；临时 `P/fixtures/coordination/`、`P/results/coordination.*`、`P/results/final.*`。不新增产品工作流资产。

**Interfaces:**

- Consumes：Task 1 的方法、Task 3 的候选和保护、Task 4 的宿主能力证据。
- Produces：实际改动与验证范围、剩余缺口、最终构建来源；不是正式发行或真实业务交付。

- [x] **Step 1：建立最小多工程夹具。** 使用现有 Python 标准库创建 Home、Customer API、Order API、Web、Mobile 的独立 Git 仓，以及一份 Git 根为容器的 `spec/services/api/apps/web` 单仓布局。用候选安装相应 Profile；共享契约定义 `/customers?status=active`，Customer API 提供本地可验证响应，Web/Mobile 以最小客户端或脚本消费；不用生成真实业务脚手架或引入依赖。
- [x] **Step 2：从 Home 执行一次受限跨工程任务。** 在已通过 Task 4 的一个宿主中要求将过滤结果从 2 项改为 1 项，明确授权只改 Customer API 与 Web。先从文件和调用关系判断 Mobile 是否需要修改，不以地图存在为由动 Order API。验证真实响应与 Web 消费结果均为 1，未授权工程的 Git 差异为空；多 Agent 若不可用改为串行，不启动其它 harness 进程。若依赖判断发现 Mobile 也需修改，先记录真实影响并保持未授权仓不变，不能伪称整体兼容完成。
- [x] **Step 3：注入输入变化和中断。** 契约 A 的任务完成一部分后，在夹具内生成未提交契约 B，并保存简短进度后新开会话恢复；预期重新读取 B，原 A 证据仍标明来源，受影响消费者待更新。只给 mock 时报告 mock；用夹具自身的隔离 PATH 或合成挂载入口模拟缺少能力，预期报告对应步骤未完成，不移动系统工具、不安装工具或回退主 checkout（RF4、RF5）。
- [x] **Step 4：验证工程与运行边界。** 对同仓 sibling 安装根分别执行 doctor/sync；同根再次 attach 或请求其它 Profile 应拒绝。检查受管物理路径无交集；父子根场景只检查当前安装/受管集合与宿主根选择，不声称存在跨根锁。另在夹具建立真实子模块边界，验证不会当普通路径覆盖。启动两个带不同随机身份的临时服务，使用独立端口，确认被测服务来自目标目录；只关闭自己启动的进程。涉及的临时 Git 提交/分支仅在夹具内创建（RF3）。
- [x] **Step 5：构建最终候选并做针对性复验。** 所有内容、镜像和哈希落定后，依次运行下列命令。若 UI 产物缺失或与当前 UI 源不符，先按现有构建说明执行 `pnpm --dir ui build`；未改 UI 不扩大到浏览器全量测试。

```sh
cargo fmt --all
cargo run --locked --bin yss-bundle -- --source-root "$template_root" --out assets/bundle.tar.gz
cargo fmt --all -- --check
cargo clippy --locked --all-targets -- -D warnings
cargo build --locked --release --bin yss --target aarch64-apple-darwin
python3 "$probe_root/check_bundle.py" assets/bundle.tar.gz "$wb_projection"
```

最终 `version --json` 应报告实际来源、状态和摘要；不改 `docs/template-release.json`。若从 Task 3 后只改说明，复验最终载荷与受影响 Setup 安装资源；若代码、策略、render 或冲突保护变化，重跑对应安装/宿主用例。不能把早期候选结果泛称最终全部验收。

- [x] **Step 6：核对差异与交付事实。** 两仓运行 `git diff --check`，逐项对照 baseline，确认无真实业务仓/用户配置/全局 CLI 改动、无持久探针、无依赖/格式/默认集合变化。记录实际通过与失败项，区分编译、安装验证、宿主调用和发行。清理本任务临时服务；移除可重建夹具与脚本前保留必要输出和实际命令。仅在得到对应授权时另行提交/推送/发行。

## 执行顺序与完成条件

Task 1 → Task 2 → Task 3 → Task 4 → Task 5。Task 1 的文档校对可与只读宿主资料核实并行，但两者共同修改 Setup 副本和哈希，落盘须统一责任人。Task 3 的 Bundle 与安装保护在同一候选上一起评审，不能只交付新增目录。

完成实施意味着代码与文档一致、相关保护用例通过、逐宿主能力有真实状态、必要多工程闭环有证据。某宿主缺少登录或可用工具时，代码部分可以完成，但该宿主实测项仍未完成；交付必须明确，不能改成“全部支持”。复选状态按本轮实际执行更新；宿主阻塞项保留未完成，详见执行状态。

## 计划自审记录

主 Agent 对照设计自行完成覆盖、步骤、接口、风险和篇幅检查；没有委派计划自审。设计 §3–4 对应 Task 1/5，§5 对应 Task 2/3，§6 对应 Task 2/4，§7 对应 Task 1/2/4，§8 对应 Task 3–5。RF1–RF5 均有具体用例。

自审修正的重点：Frontend 不引用未安装 Implement；Claude 投影必须在 Profile 覆盖之后；render 元数据与字节一起验证；本地 canonical 定制与新增视图分叉必须拒绝；父子根不能假定共享锁；WorkBuddy 采用实测分支，不伪造 CLI 或先宣称原生成功。剩余不确定性是执行环境能力，已给出明确验证动作和失败语义，不是留待实现者自行决定的架构空白。

## 执行状态（2026-11-15）

Task 1–3 已实施并验证：默认集合 17/42/13 不变；6985 项 Bundle 检查、3 个构建拒绝反例、18 个安装/升级场景通过。Task 5 的五仓协作、契约 A→未提交 B 续办、真实 HTTP/消费者以及同仓/父子根/子模块边界已通过。无持久测试资产写入源码仓。

Task 4 分层结果：Codex 正确路径读取、刷新和缺失路径检查通过，但无 catalog 注入证据；WorkBuddy `.workbuddy` 原生菜单及资源读取通过，显式读取候选载荷/更新资源/缺失路径通过。同名菜单会含其他项目，重启不能保证隔离，必须核实注入路径。`.codebuddy` 单入口未取得当前项目调用证据，保留既有 `.workbuddy` 分发。

Claude 仅验证附加 worktree 的技能元数据注册，后续因未登录未运行；Pi 元数据发现后模型订阅拒绝；DSH 缺凭据且无发现成功证据。Task 4 Step 3–5 因这些外部条件未全部完成，不调整用户登录或配置。Step 6 只覆盖实际能调用的宿主，不承诺热重载或全平台行为。

最终格式检查、Clippy、release 构建及候选新装复验通过；独立最终评审未发现 Critical、Important 或 Minor 问题。详细证据记录于仓库外临时验证目录；未提交、推送、部署、正式发行，保留两个工作树及此前改动。

## 后续集成授权（2026-11-15）

用户随后授权真实场景验收、提交并合入两仓本地主分支、清理本次分支与工作树。真实 SCRM 安装快照完成定制冲突及无收据保护验证，真实源保持不变；18 场景与最终 Bundle 检查重新通过。CI 模板来源须随本次模板提交更新，既有正式标签记录不变。本段覆盖前述“不包含提交/合并”的本轮初始范围，不包含远端推送或正式发行。
