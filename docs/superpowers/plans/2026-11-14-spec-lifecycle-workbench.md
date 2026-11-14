# YSS Spec 任务与全生命周期工作台 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在现有本机工作台中完整查询和推进已纳管 Spec 及其具体任务，展示从调研到交付的八阶段记录、来源与真实历史。

**Architecture:** 扩展现有 Rust 数据模型与单一 mutation 入口，兼容升级个人状态，查询与生命周期组装供 HTTP/CLI 共用。Vue 在现有项目、列表/看板和详情内增量实现，保留只读项目访问、revision 冲突保护和原有主题。

**Tech Stack:** Rust 1.99.1、现有 Axum/serde/cap-std/Tokio；Vue 3.5.40、TypeScript 5.9.3、Vite 8.3.5、Tailwind 4.3.3、Reka 2.11.0、VueUse 15.0.0；Node ^24.14.1、pnpm 10.15.0。沿用锁文件，不增加依赖。

**Spec:** [本次增量设计](../specs/2026-11-14-spec-lifecycle-workbench-design.md)。执行者同时读取设计和当前 docs/workbench.md；新字段、枚举、查询规则与页面行为按设计，不恢复历史流程。状态：用户批准后已按 Native 实施并完成必要验证；代码保留在工作树，未提交或发行。独立审查的三项重要问题已修复；Spec 完成时剩余任务数提示作为次要项保留。

## Global Constraints

- 服务只监听 127.0.0.1，默认端口 0；继续校验会话令牌、Host、写请求 Origin。
- 工作台数据只写独立用户目录；项目、源 Markdown、.yss 安装收据保持只读。
- 保留既有锁、revision、文件身份复验、原子发布、错误恢复和严格 JSON 解码。
- 主来源读取上限 2 MiB，HTTP 请求上限 1 MiB，状态文件上限 16 MiB、100 万 JSON 节点，revision 不超过 9007199254740991。
- 扫描保留 10000 候选项/16 层限制，不穿越 symlink、子模块或嵌套 Git 工作树；默认不自动导入。
- 复用 Rust 1.99.1、Vue 3.5.40、TypeScript 5.9.3、Vite 8.3.5、Tailwind 4.3.3、Reka 2.11.0、VueUse 15.0.0；Node ^24.14.1、pnpm 10.15.0 只参与开发构建。
- 不新增依赖、数据库、Pinia、通用流程引擎、运行时 Node 或 CDN；复用当前锁文件。
- 验证材料只放系统临时目录或临时模块，结束移除；不维护或分发包装脚本、持久测试套件或额外测试报告。
- 先构建 UI、生成真实来源 assets/bundle.tar.gz，再按 docs/build-install.md 执行锁定工具链 release 构建；不伪造模板标签。
- 未另获对应授权，不提交、推送、合并、部署或发行；不覆盖原工作树已有修改。

## Review Focus

1. 已有 v1 数据只读打开、首次写入失败或重复启动：原字节、ID 和 revision 可保留，备份不覆盖，未知版本不降级；Task 1 的 migration_failure_preserves_bytes。
2. 已到交接阶段但没有更早证据、任务全部完成或 Spec 重开：不补造阶段完成，不自动关闭关联对象；Task 2 的 independent_progress_and_legacy_history。
3. 拖拽后卡片被筛选隐藏、双窗口同时保存、请求中继续输入或切换任务：不覆盖数据、不丢草稿、不误报保存；Task 4/5 的 draft_generation_and_conflict、board_move_filtered_out。
4. 父项目停用、保存视图引用未知 ID、终态默认隐藏：完整结果可找回，未知筛选不能变成全项目；Task 3 的 query_scope_and_parity。
5. 原文件删除、项目移动、同一时间有多条历史、来源读取失败：任务和阶段仍可读，历史保持追加次序，异常不改变业务状态；Task 2/7 的 source_loss_and_order、packaged_lifecycle_roundtrip。

---

## 文件与责任

仓库根为当前工作树 /Users/zhudaoming/.codex/worktrees/spec-workbench/yss-cli；以下路径均相对仓库根。

| 文件 | 操作与责任 |
|---|---|
| src/workbench/model.rs | 修改：v2 DTO、枚举、动作、查询/生命周期投影类型 |
| src/workbench/migration.rs | 新增：严格 v1 解码与内存升级，不写磁盘 |
| src/workbench/store.rs | 修改：版本选择、v2 校验、首次写前备份，保留原子发布 |
| src/workbench/history.rs | 新增：从 service.rs 移入实际字段差异与目标记录，统一事件语义 |
| src/workbench/tasks.rs | 新增：任务批量创建、编辑与父归属校验 |
| src/workbench/lifecycle.rs | 新增：阶段记录维护、八节点投影、原顺序历史 |
| src/workbench/query.rs | 新增：唯一过滤/排序规则、视图偏好维护 |
| src/workbench/service.rs、sources.rs、mod.rs | 修改：动作分发、快照扩展、资源健康复用和模块导出 |
| src/workbench/server.rs、src/cli.rs | 修改：只读 query/lifecycle HTTP 与 CLI、命令信封名 |
| ui/src/services/workbench.ts、ui/src/hooks/useWorkbench.ts | 修改：DTO/transport、刷新入口；不叠加另一套缓存 |
| ui/src/views/SpecDetail/components/TaskList.vue、TaskEditor.vue、LifecyclePanel.vue | 新增：具体任务及阶段记录交互 |
| ui/src/views/SpecDetail/hooks/useTaskEditor.ts | 新增：任务草稿、提交基线、generation/离开保护 |
| ui/src/views/SpecDetail/schemas/tasks.ts | 新增：每行标题批量录入解析，无状态推断 |
| ui/src/views/SpecDetail/index.vue、hooks/useSpecEditor.ts | 修改：新区域、任务定位、共享未保存提醒 |
| ui/src/views/Specs/hooks/useWorkItems.ts、useWorkActions.ts | 新增：查询请求所有者、卡片窄字段动作与弹层 |
| ui/src/views/Specs/hooks/useSpecFilters.ts、schemas/filter.ts | 修改：URL 编解码；移除被后端取代的过滤实现 |
| ui/src/views/Specs/components/SpecBoard.vue、SavedViews.vue | 修改/新增：双对象看板、分组、移动、保存查询 |
| ui/src/components/WorkItemRows.vue、HistoryList.vue | 新增/修改：统一行展示、正确的事件目标名称 |
| ui/src/views/Specs/index.vue、Workbench/index.vue、ProjectDetail/index.vue | 修改：查询结果接入、可行动聚合与项目范围 |
| ui/src/views/ProjectDetail/components/DiscoverSpecs.vue | 修改：当前候选全选、完整/部分结果与纳管范围 |
| ui/src/App.vue、router/index.ts、styles/main.css | 按需修改：导航、返回上下文、已有主题内的布局 |
| README.md、docs/workbench.md、docs/cli-help.md、ui/DESIGN.md | 更新实际交付能力、迁移恢复、命令与交互说明 |

不修改模板仓、不改安装收据、不重建脚手架。旧 SpecRows 保留给需要 SpecView 的旧页面，直到对应调用者迁入 WorkItemRows；不得直接删掉仍使用的组件。

## 验证与执行约定

Native 按 Task 1–7 顺序实施，任务之间通过下述接口衔接。每项先写最小临时断言，观察行为失败，再完成实现与复验。计划列出的测试是实施要求，不是当前已经通过的结果。

Rust 临时断言放对应模块内的 cfg(test) scratch_checks，用 cargo test --locked --lib scratch_checks -- --nocapture；结束移出交付差异。临时纯前端断言和 HTTP/CLI 驱动放 mktemp -d 创建的目录，路径记录在当前会话，不创建仓库测试套件。浏览器使用临时项目和独立 --data-dir。

共享临时 fixture：项目 p1/p2 的 ID 分别为 32 个 1/2，Spec s1/s2 为 32 个 a/b；标题为“订单 Spec”“回访 Spec”，根指向两个系统临时目录，主文件 docs/specs/order.md、docs/specs/visit.md。v1 固定 revision=7，s1.stage=handoff、status=doing、非空 nextAction；两条 at 相同但 ID 逆序的旧历史按写入顺序排列。v2 fixture 由该 v1 转换，任务按各步骤创建，不伪造第三方业务数据。

任务末尾仅整理本任务差异并记录完成；提交步骤只有收到明确 Git 提交授权才执行。当前不存在自动提交授权。

### Task 1: v2 数据兼容与恢复

**Files:** 修改 model.rs、store.rs、mod.rs，以及 service.rs 中旧 ChangeEvent 的构造；新增 migration.rs。临时测试在 migration.rs/store.rs 的 scratch_checks。

**Interfaces:**
- Consumes: 当前 codec::{decode_json,encode_json}、Limits、Root、FileSnapshot、Store::commit、read_state。
- Produces: 设计第 5/6 节的持久记录及查询 DTO；WorkbenchState::default() 返回版本 2、revision=0、空集合。Snapshot/MutationResult 的新增字段与业务 Action 在 Task 2 接入，收藏 Action 在 Task 3 接入，避免提前增加无实现的 match 分支。
- Produces: migration::decode_state(bytes: &[u8], limits: Limits) -> Result<WorkbenchState>；严格识别 v1/v2，拒绝其他版本，不落盘。
- Preserves: Store::open(&Path) -> Result<Store>、Store::state() -> &WorkbenchState、Store::commit(u64, WorkbenchState) -> Result<u64>、read_state(&Path) -> Result<WorkbenchState>。

- [x] **Step 1: 添加 migration_failure_preserves_bytes 与 migration_roundtrip 临时断言。**

~~~rust
let before = std::fs::read(data.join("state.json")).unwrap();
let state = read_state(&data).unwrap();
assert_eq!((state.schema_version, state.revision), (2, 7));
assert_eq!(state.specs[0].stage, Stage::Handoff);
assert!(state.tasks.is_empty() && state.stage_records.is_empty());
assert_eq!(std::fs::read(data.join("state.json")).unwrap(), before);
assert_eq!(state.history.iter().map(|e| &e.id).collect::<Vec<_>>(), old_ids);
~~~

增加首次有效 mutation 升级后精确备份原字节、no-op/非法请求不备份、已有异字节备份拒绝、外部改动冲突、未知版本/重复键/16 MiB/100 万节点超限保持原字节断言；备份创建失败和备份成功后发布失败分开验证。

- [x] **Step 2: 运行上述临时断言，确认当前版本仅支持 v1、缺少新结构而失败。** 缺少符号的编译错误只证明未实现；实现后必须实际观察数据断言执行。
- [x] **Step 3: 实现 decode_state 与 v2 校验，并接入 Store。** 使用严格 legacy DTO 保留旧字段值域，不让 v1 文件夹带新增枚举；先通过 codec 结构/重复键校验再识别版本。持有原 FileSnapshot；容量、revision、原文件核验通过后独占创建摘要命名备份，再复验并复用现有原子 rename。验证任务归属、阶段唯一性、资源、名称和状态说明；savedView 中未知过滤 ID 允许保留。现有项目/Spec 事件构造补 taskId/lifecycleStage=null，保证本任务单独可编译。
- [x] **Step 4: 重跑断言及 cargo fmt --all -- --check。** 预期全部断言通过、旧源字节与备份一致；先修复本任务失败，再进入 Task 2。

### Task 2: 任务推进、阶段记录和真实历史

**Files:** 新增 tasks.rs、lifecycle.rs、history.rs；修改 service.rs、sources.rs、mod.rs/model.rs 的实现衔接。临时测试在 tasks.rs/lifecycle.rs。

**Interfaces:**
- Consumes: Task 1 的 v2 DTO/Store；现有 sources::{text,spec_view,now}、validate_resources。
- Produces: history::EventTarget {project_id:String,spec_id:Option<String>,task_id:Option<String>,lifecycle_stage:Option<Stage>}。
- Produces: history::record_change<T: Serialize>(state: &mut WorkbenchState, target: EventTarget, before: Option<&T>, after: &T, at: &str) -> Result<bool>；仅实际业务差异追加事件。
- Produces: tasks::add_tasks(state: &mut WorkbenchState, spec_id: &str, drafts: Vec<TaskDraft>, at: &str) -> Result<Vec<String>>。
- Produces: tasks::edit_task(state: &mut WorkbenchState, task_id: &str, edit: TaskEdit, at: &str) -> Result<bool>。
- Produces: lifecycle::set_stage_record(state: &mut WorkbenchState, record: StageRecord, at: &str) -> Result<bool>；忽略调用方 updatedAt，使用 at。
- Produces: lifecycle::build_lifecycle(snapshot: &Snapshot, spec_id: &str) -> Result<LifecycleView>。
- Produces: sources::resource_views(resources: &[ResourceRef], root: &Result<Root>) -> Vec<ResourceView>，从现有 spec_view 提取原逻辑，供 Spec 与 Task 共用；Snapshot/MutationResult 新字段及任务/阶段/窄字段 Action 在本任务同时接入全部调用者。
- Preserves: service::mutate(&mut Store, MutationRequest) -> Result<MutationResult>、snapshot(&WorkbenchState,&Cancellation) -> Result<Snapshot>。

- [x] **Step 1: 添加 independent_progress_and_legacy_history、source_loss_and_order。** 创建三个不同阶段任务；以下为核心结果断言：

~~~rust
assert_eq!(added_ids.len(), 3);
assert_eq!(state.tasks[0].status, Status::Todo);
assert_eq!(view.stages.len(), 8);
assert!(view.stages.iter().all(|s| s.record.is_none()));
assert_eq!(state.specs[0].stage, Stage::Handoff);
// 将全部任务设为 done 后再次读取：
assert_eq!(state.specs[0].status, Status::Doing);
assert!(state.stage_records.is_empty());
~~~

再断言：空标题/201 条/任一非法资源使整批不入库；blocked/skipped 无说明拒绝；unmarked 不能成为阶段记录键；同一阶段重复保存不增加 revision/历史；多个 doing 阶段同时保留；Spec done 后任务保持原状态；取消/停用/恢复无级联重写；旧 editSpec 不清空新集合。删除源文件后仍能编辑现有个人记录、资源变 missing；两条同时间历史保留写入顺序。

- [x] **Step 2: 运行临时断言，确认缺少动作及生命周期投影而失败。**
- [x] **Step 3: 实现任务/阶段/历史模块并扩展 mutate/snapshot。** addTasks 先验证整批，再创建 ID 和事件；所有动作最终一次 Store.commit。任务/阶段实际修改更新父 Spec.updatedAt，no-op 不更新。setWorkStatus/setWorkStage 与详情编辑复用校验；不复制字段规则。StageRecord 由动作内构建，不能接受外部 updatedAt。snapshot 复用 sources 的资源检查，保留 history 原序；不足以定位的旧事件显示原 ID，不篡改记录。
- [x] **Step 4: 重跑临时断言。** 预期全过程无源文件写入、一次批量仅增加一个 revision、每项任务有自己的创建事件；检查旧项目/Spec 编辑和访问语义仍成立。

### Task 3: 统一查询、收藏视图与 CLI

**Files:** 新增 query.rs；修改 model.rs、service.rs、server.rs、cli.rs、mod.rs。临时测试在 query.rs，HTTP/CLI 驱动在系统临时目录。

**Interfaces:**
- Consumes: Snapshot v2、WorkQuery、WorkItem、QueryResult、SavedView，Task 2 的 build_lifecycle。
- Produces: query::normalize_query(query: WorkQuery) -> Result<WorkQuery>。
- Produces: query::query(snapshot: &Snapshot, request: &WorkQuery) -> Result<QueryResult>。
- Produces: query::save_view(state: &mut WorkbenchState, view_id: Option<&str>, name: String, query: WorkQuery, layout: Layout, group_by: GroupBy, at: &str) -> Result<String>。
- Produces: query::remove_view(state: &mut WorkbenchState, view_id: &str) -> Result<bool>。
- Produces: POST /api/query 和 GET /api/specs/{id}/lifecycle；CLI query/lifecycle 参数、默认值及命令名按设计第 6 节。

- [x] **Step 1: 添加 query_scope_and_parity。** 构造 active/停用父项、done/canceled/blocked、来源丢失和同时间标题的结果：

~~~rust
assert_eq!(query(&snap, &all_active_tasks).unwrap().total, 3);
assert_eq!(query(&snap, &unknown_project).unwrap().total, 0);
assert_eq!(normalize_query(done_filter).unwrap().include_closed, true);
assert_eq!(query(&snap, &blocker_text_filter).unwrap().items[0].id, blocked_id);
assert_eq!(query(&snap, &same_timestamp_filter).unwrap().items[0].id, lower_id);
~~~

all_active_tasks 为 entity=task、activity=active、includeClosed=true，其余数组空；fixture 正好三条有效启用任务，另有停用父项目下一条。分别断言 q 命中自身备注/父 Spec 名，不命中父备注；stage/status 多值 OR、字段间 AND；inactive 是有效启用反集；sourceProblemsOnly 不把 external 算异常。收藏往返保存布局/分组、非法名称拒绝、更新 no-op 不改 revision、删除收藏不改任务。

- [x] **Step 2: 运行临时断言，确认不存在统一查询和命令而失败。**
- [x] **Step 3: 实现 query 模块与 HTTP/CLI 接线。** query 为 Snapshot 上纯函数，无目录写入；CLI 统一 read_state → snapshot → query/build_lifecycle。修改 Command::name() 的 Workbench 分支逐项匹配，不能继续把任意子命令标成 snapshot。saveView/removeView 经现有 mutation 锁及 revision，偏好不产生业务历史。
- [x] **Step 4: 运行 Rust 与 HTTP/CLI 断言。** 对同一静态 fixture，断言 HTTP query 与 yss workbench query --json 的 items/total 相等（忽略 readAt）；lifecycle 规格不存在为明确 404/CLI 错误；未知筛选返回空。验证无 token 401、外来 Origin/Host 403、未知字段 400、旧 revision 409；不存在数据目录的 query 不创建目录。旧 snapshot 信封仍为 workbench.snapshot。

### Task 4: Spec 详情内的具体任务与生命周期

**Files:** 修改 services/workbench.ts、useWorkbench.ts、SpecDetail/index.vue、HistoryList.vue；新增 TaskList.vue、TaskEditor.vue、LifecyclePanel.vue、useTaskEditor.ts、schemas/tasks.ts；按需要调整 useSpecEditor.ts。

**Interfaces:**
- Consumes: Task 2/3 DTO、窄字段 Action、现有 useWorkbench().submit(action, expectedRevision) 和 ModalDialog。
- Produces: queryWorkItems(query: WorkQuery, signal?: AbortSignal): Promise<QueryResult>；loadLifecycle(specId: string, signal?: AbortSignal): Promise<LifecycleView>。
- Produces: parseTaskTitles(text: string): string[]，忽略空行、trim，其余内容原样保留，超过 200 在表单校验报错。
- Produces: useTaskEditor(specId: Readonly<Ref<string>>, taskId: Readonly<Ref<string | null>>)；返回 draft、dirty、error、saving、save、reset，save 使用快照 revision 与提交时内容，不独立注册路由 guard。
- Extends: useSpecEditor(externalDirty?: Readonly<Ref<boolean>>)；现有 allowLeave 使用 dirty 或 externalDirty，增加返回 allowLeave 和 reset；SpecDetail 将任务/阶段弹层 dirty 汇总后传入，保留一个路由及 beforeunload 提醒所有者。
- Produces: TaskList props {specId:string}；TaskEditor props {specId:string,taskId:string|null}、emit saved/close/dirty；LifecyclePanel props {specId:string}、emit saved/dirty；dirty 事件载荷为 boolean。

- [x] **Step 1: 在系统临时目录写 task_titles_and_progress、draft_generation_and_conflict 断言。**

~~~ts
assert.deepEqual(parseTaskTitles('  写规格 \n\n - [x] 已有验收项 '),
  ['写规格', '- [x] 已有验收项'])
assert.equal(savedPayload.drafts[1].stage, 'verification')
assert.equal(afterSaveWhileTyping.dirty, true)
assert.equal(afterSwitchingTask.draft.title, '新任务草稿')
assert.equal(afterConflict.draft.title, '保留当前输入')
~~~

状态初值由后端 todo 决定；界面预览必须提示没有导入源状态。补充阶段无记录/并行/跳过说明/重开、停用父项只读、源丢失仍可看任务和历史、退出弹层取消后草稿保留的断言或浏览器步骤。

- [x] **Step 2: 运行临时断言，先观察当前缺少任务解析/编辑逻辑的失败。** 纯解析用 node --experimental-strip-types <临时目录>/check-task-titles.ts，脚本直接导入仅含纯函数及 type import 的 schemas/tasks.ts；草稿/请求竞态在真实浏览器中用两个标签页和延迟响应验证，不依赖可能已被清理的旧临时脚本，不新增测试依赖。
- [x] **Step 3: 实现 transport、任务表单与八阶段面板。** 对 DTO 做与原 loadSnapshot 相同的基本数组/revision 校验；TaskEditor/LifecyclePanel 向父页 emit dirty(boolean)，关闭和任务切换经父页 allowLeave，不叠加第二个路由 guard。任务/阶段保存后，仅在 Spec 表单不 dirty 时 reset 其快照基线；仍 dirty 则保留旧 revision，后续保存正常提示冲突。生命周期先展示缺省八节点与显式记录，不根据 stage 索引补完成色。?taskId 定位不匹配当前 Spec 时显示不存在，不回退打开另一项任务。标签列表作为单一数组，替换现有键盘导航写死的 3 和末项索引，确保五个区域均可键盘访问。
- [x] **Step 4: 执行 pnpm --dir ui typecheck、pnpm --dir ui lint，跑临时断言和实际浏览器关键路径。** 验证单条/批量新增、查看来源、阶段标记、两页冲突、保存中继续输入、切换对象、键盘焦点；不能只凭编译宣布 UI 行为通过。

### Task 5: 共用查询的列表、看板与保存视图

**Files:** 新增 useWorkItems.ts、useWorkActions.ts、SavedViews.vue、WorkItemRows.vue；修改 useSpecFilters.ts、schemas/filter.ts、SpecBoard.vue、Specs/index.vue、styles/main.css。

**Interfaces:**
- Consumes: Task 3 QueryResult/WorkQuery，Task 4 queryWorkItems、useWorkbench().submit。
- Produces: useWorkItems(query: Readonly<Ref<WorkQuery>>) 返回 result、loading、error、refresh；负责自己的 AbortController 和请求 generation。
- Produces: encodeWorkQuery(query: WorkQuery, layout: Layout, groupBy: GroupBy, viewId?: string): LocationQueryRaw；decodeWorkQuery(query: LocationQuery): {query:WorkQuery,layout:Layout,groupBy:GroupBy,viewId?:string}。
- Produces: useWorkActions() 返回 move(item: WorkItem, dimension: 'status'|'stage', value: Status|Stage, expectedRevision: number): Promise<void>、阻塞说明弹层状态、confirm/cancel；expectedRevision 来自所选项所在 QueryResult，弹层保留该值。
- Produces: SpecBoard props {items:WorkItem[],groupBy:GroupBy,revision:number}；WorkItemRows props {items:WorkItem[]}。

- [x] **Step 1: 添加 query_url_roundtrip、board_move_filtered_out 和查询竞态临时断言。**

~~~ts
assert.deepEqual(decodeWorkQuery(encodeWorkQuery(q, 'board', 'stage')).query, q)
assert.equal(afterCanceledBlockedMove.commandCount, 0)
assert.equal(afterMove.command.action.kind, 'setWorkStatus')
assert.equal(afterMove.command.expectedRevision, displayedRevision)
assert.equal(afterConflict.items[0].status, 'doing')
assert.equal(afterOldQueryResolves.result.queryKey, newestQueryKey)
~~~

覆盖同列移动 no-op、转 blocked 必须填说明、转 done 后离开当前过滤显示成功解释、转阶段不改状态、同时间 ID 次序、savedView 保存/更新/移除、不同项目筛选保持、50→100 条展开且总数不变。

- [x] **Step 2: 运行临时断言，确认旧界面缺少所需行为而失败。**
- [x] **Step 3: 接入服务端查询和双对象看板。** 删除 filter.ts 的旧过滤算法，保留 URL 编解码职责。query 只由 useWorkItems 请求；filters 变化或 snapshot 被刷新替换时使旧请求失效，涵盖同 revision 的来源健康变化。结果 revision 与当前 snapshot 不同则刷新快照并重新查询，不把不同版本对象混合编辑。卡片选择时捕获其结果 revision；菜单与桌面原生拖放调用相同 move，移动前不乐观修改持久状态。URL 完整保存当前条件，不把收藏临时修改偷偷写回。
- [x] **Step 4: 执行前端类型/lint、临时断言和浏览器验证。** 桌面拖动、键盘菜单、触屏尺寸菜单、筛选后卡片消失、409、保存视图后重启、返回保留查询与滚动均通过；拖放失败或取消能恢复可操作焦点。

### Task 6: 全局工作台、项目范围与纳管完整性

**Files:** 修改 Workbench/index.vue、ProjectDetail/index.vue、DiscoverSpecs.vue、App.vue、router/index.ts；复用 Task 4/5 的列表、任务定位和查询入口。

**Interfaces:**
- Consumes: WorkQuery、Snapshot v2、useWorkItems、WorkItemRows、既有 discoverSpecs/addSpec。
- Produces: 工作台/项目页的每个“查看”入口使用 encodeWorkQuery，项目上下文写 projectIds；任务链接使用 /specs/:specId?taskId=:id 并附 return。
- Produces: “查看全部记录”链接为 activity=all、includeClosed=true，并清除 statuses；保留当前项目范围。

- [x] **Step 1: 写 dashboard_scope 与 discovery_selection 临时浏览器断言。** fixture 含两个项目、三个有效任务、一个停用父项任务、done/canceled 和来源异常；断言数字对应点开后的实际结果，不把 Spec 和子任务相加成一个“完成率”。首次扫描 selected.length=0；选择当前候选仅包含未关联项；complete=false 的提示选择后仍保留。
- [x] **Step 2: 运行浏览器步骤，记录当前缺少任务汇总/全部范围入口的失败。**
- [x] **Step 3: 接入全局与项目视图。** 从同一 Snapshot/QueryResult 按 status/stage 做数量展示；“待验证”指任务 stage=verification 且未终结，不暗示验证已执行。每项显示对象类型及父 Spec；停用/关闭隐藏数量使用明确名称。候选“选择当前未关联项”尊重扫描范围、已关联状态及 busy，保留现有逐项导入失败后的已成功数量和剩余选择。
- [x] **Step 4: 浏览器复验两个项目切换、全部记录、恢复父项、任务定位返回与不完整扫描。** 检查 390px 下计数、筛选、列表/看板和对话框可操作，状态有文字，不依赖颜色。

### Task 7: 集成构建、恢复演练与交付说明

**Files:** 更新 README.md、docs/workbench.md、docs/cli-help.md、ui/DESIGN.md；仅在实际构建需要时修改 docs/build-install.md，不改版本/正式模板标签。临时所有测试与 fixture 完成后清理。

**Interfaces:**
- Consumes: Task 1–6 的完整 UI、API、CLI，现有 AssetLoader 与构建流程。
- Produces: 包含 UI 的本机 release 候选程序，以及真实完成范围与已知限制说明。

- [x] **Step 1: 写 packaged_lifecycle_roundtrip 临时驱动。** 使用隔离 v1 fixture：只读查询不改字节 → 首次新增任务自动备份/升级 → 三个阶段的任务 → 阶段并行/跳过/重开 → 收藏/全记录查询 → Ctrl-C → 重启；断言 ID、revision、历史次序、筛选结果保持。移动根目录、删除主文件后个人数据仍存在；源文件摘要前后不变；CLI query/lifecycle 与 HTTP 对应结果一致。
- [x] **Step 2: 执行必要工程检查及本机 release 构建。**

~~~sh
pnpm --dir ui install --frozen-lockfile
pnpm --dir ui typecheck
pnpm --dir ui lint
pnpm --dir ui build
cargo run --locked --bin yss-bundle -- --source-root /absolute/verified-template-checkout --out assets/bundle.tar.gz
cargo fmt --all -- --check
cargo clippy --locked --all-targets -- -D warnings
cargo build --locked --release --bin yss --target aarch64-apple-darwin
~~~

/absolute/verified-template-checkout 必须在实施时替换为实际核验的模板 Git checkout；优先由 docs/build-install.md 当前固定提交创建只读临时 checkout，不改用户修改中的模板仓。记录实际 HEAD、工作树状态和摘要；Bundle 生成失败不得沿用旧包并声称本次生成成功。

- [x] **Step 3: 使用生成的 release 程序运行 roundtrip 与浏览器关键交互。** 用户运行环境 PATH 不含 Node；复验旧 workbench snapshot、旧命令 help/version/catalog，HTTP 会话/来源/大小限制和源只读。无依赖变化时不重复跑无关安装全矩阵。分别记录 macOS 原生、Windows 或外部 CI 的实际范围。
- [x] **Step 4: 更新使用与恢复说明，清除临时测试修改并检查最终差异。** 明确八阶段是个人模型、纳管覆盖范围、任务录入和来源、无自动执行/外部同步、v2 备份与旧版恢复限制。确认不残留测试模块/脚本、无原始临时数据、无未授权文件。按 Native 流程做一次独立整体验证审查，处理确认的问题，再只复验受影响项；不将编译等同于功能或发行成功。

## 自审结果

- 目标覆盖：任务/阶段 → Task 1/2/4；查询/CLI/收藏 → Task 3/5；Linear 式看板 → Task 5；跨项目“全部” → Task 6；来源、迁移、并发与完整交付 → Task 1/2/7。
- 类型一致：Stage/Status 与 StageProgress 分离；TaskDraft 不含导入状态；TaskEdit 不改归属；QueryResult 与 UI/CLI 共用；旧 editSpec 不承载新集合。
- Review Focus 五项均已分配到明确的临时断言或浏览器路径。
- 没有新增审批流程、任务执行器、远程集成或持久测试套件；现有 dirty 工作不作为可丢弃基线。
- 原计划中的检查已在本机实施，实际范围与限制见 `docs/workbench.md`；不将计划中的成功条件视为跨平台或发行证据。后续 review 修复已复验查询刷新、保存输入保护、时间排序及 Spec 完成提示。

## Execution Handoff

实施方式保留 Native，不再次选择。按 writing-plans 的执行交接要求先请用户审阅本计划及关联设计，确认范围符合意图后使用 superpowers:executing-plans 连续实施；实施授权不自动包含 Git 提交、推送、部署或发行。
