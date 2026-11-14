# YSS Spec 任务与全生命周期工作台设计

日期：2026-11-14。状态：本次增量设计，供用户审阅，尚未实施。

用户已确认：个人、本机多项目；全部 Spec 任务可视化；参考 Linear 看板；生命周期覆盖“调研 → 分析 → 规格 → 原型 → 交接 → 实施 → 验证 → 交付”，允许跳过、回退、并行，Spec 下可拆具体任务。此前选定的 Native 执行方式保留。以下数据结构、交互和技术取舍是本方案建议，不冒充用户逐项确认。

实现位置：当前 yss-cli 工作树。配套 [实施计划](../plans/2026-11-14-spec-lifecycle-workbench.md)。本文件是本次请求的新方案，不恢复同目录历史流程、门禁或退役命令。

## 1. 目标与已有基础

让用户在同一个本机工作台回答：所有已纳管 Spec 和具体任务有哪些、分别推进到哪里、卡在什么地方、接下来处理什么、依据与实际变化在哪里。

现有实现已有项目登记、Markdown 关联、个人阶段/状态、列表/六列看板、原文预览、来源健康、历史和 CLI snapshot。增量保留这些能力及未提交工作，不重搭应用。

“全部”指已登记项目、已关联 Spec 和已录入任务；不能把未登记目录、未接入的远程 tracker 或未识别文档宣传为已经覆盖。页面同时显示纳管范围、结果数量、关闭/停用隐藏数量；提供“查看全部记录”。扫描结果不完整时继续显示警告。

成功场景：两个本机项目各登记 Spec；一项 Spec 下建立三个独立任务，分属规格、实施、验证阶段；跨项目查询并在看板推进一个任务；在生命周期中看到并行、跳过、回退和真实变更；关闭重开后记录保留，CLI 返回相同筛选结果。源文件丢失时个人数据与历史仍可查看。

## 2. Approaches

| 路线 | 可交付能力 | 取舍 |
|---|---|---|
| 只增强现有 Spec 看板 | 查询、分组、阶段轨迹 | 改动少，但无法分别推进同一 Spec 中的具体任务 |
| Spec + 单层任务 + 阶段记录 | 总览、独立任务处理、完整流程、可追溯记录 | 本方案推荐；需要数据升级，但沿用现有服务、界面和锁 |
| 接入 Linear 作为任务主库 | 成熟任务模型、协作、自动化 | 引入账号、网络和双向同步，不符合本轮本机个人优先的范围 |

用户要求直接进入 writing-plans，本方案将第二条作为待审阅的实施建议；并未将其写成已经单独批准的技术选型。

参考 Linear 的同数据列表/看板、按属性分组、快捷处理、保存查询与父子工作项。仅借鉴交互，不连接或修改用户的 Linear 数据：[看板](https://linear.app/docs/board-layout)、[保存视图](https://linear.app/docs/custom-views)、[父子任务](https://linear.app/docs/parent-and-sub-issues)。

## 3. 产品对象与范围

~~~mermaid
flowchart LR
    P[本机项目] --> S[Spec]
    S --> T[具体任务：单层，可独立推进]
    S --> L[八阶段生命周期：显式记录]
    S --> R[规格与关联产物：只读来源]
    T --> R
    L --> R
    S --> H[实际变更历史]
    T --> H
    L --> H
~~~

Spec 仍是一项需求的总入口，主文件与稳定 ID 保持。Task 是 Spec 下可独立推进的具体工作，只有一层，不新建任务的子任务、依赖调度器或通用流程编辑器。Task 不独立拥有项目，项目由所属 Spec 确定；本轮不支持跨 Spec 移动，避免来源与历史归属歧义。

任务可单条创建、每行一个标题批量录入、编辑、停用和恢复；一次批量 1–200 条，全部校验通过后一次原子写入，失败不部分添加。空行忽略，其余行 trim；保留标题原文，不将勾选符号转换成状态。确认前展示标题列表和数量，允许修改。新任务为 todo，阶段由用户选择，默认当前 Spec 关注阶段；原始列表中的完成标记不自动导入为 done。

本轮不扫描 Markdown checkbox 并自动生成任务：真实模板的 checkbox 可能是验收条件，且没有统一稳定任务 ID。已有任务通过批量录入并关联原任务清单纳管；来源是可见的本地文件或 HTTP(S) 链接，不修改源文件。现有 nextAction 不自动拆成任务，避免重复和语义误判。

任务、阶段和 Spec 的完成相互独立。所有任务 done 不自动完成 Spec，Spec done 不关闭剩余任务；界面展示“仍有 N 项未完成任务”，但不增加审批或材料门禁。停用 Spec/项目使下属任务退出默认视图，保留其原始 active/status，恢复父项后重新可见。

不包含：Agent/终端执行、自动部署、外部 tracker 同步、多人权限、AI 推断进度、源文件写回、工时、路线图日期、递归任务、通用工作流配置。这里的“处理”是创建/维护任务、调整阶段或状态、记录阻塞及下一步、查看依据，不声称执行了开发或验证。

## 4. 生命周期语义

Stage 稳定值按顺序为 unmarked、research、analysis、spec、prototype、handoff、implementation、verification、delivery。中文为未标记、调研、分析、规格、原型、交接、实施、验证、交付。旧 handoff 保持“交接”，不能迁移成最后的 delivery。

Spec.stage 继续表示“当前关注阶段”，任务拥有自己的 stage。流程中的八个阶段节点各有独立 StageProgress：unmarked 未记录、doing 进行中、blocked 受阻、done 已完成、skipped 已跳过。unmarked 只作为记录状态，不是第九个业务阶段。

- 缺少阶段记录时显示“未记录”，不保存八个空对象。
- 阶段可并行进行、任意重开或跳过；blocked、skipped 必须有非空说明。
- 从未记录直接到 done 可以记录，但不补造开始时间或推断之前的阶段已完成。
- 调整阶段记录不改变 Spec.stage；“设为关注阶段”是明确的独立操作。
- 任务切换 stage 或 status 不改变阶段记录；节点显示该阶段任务状态计数辅助判断。
- 完成和跳过都表示个人记录。交付阶段 done 不代表自动发布、接收方验收或上线成功。
- 旧历史中的 stage 变化仅标为“关注阶段变化”；源文件 mtime 不作为流程事件。

流程展示上部八节点概览、下部所选阶段任务/说明/资源；实际历史另按记录顺序展示。无历史显示“接入前过程未记录”。不展示推断耗时、综合百分比或自动生成的完成勾。对任务可显示“已完成 2 / 3，已取消 1”，明确只是数量。

## 5. 数据定义与升级

所有新 JSON 继续 camelCase、严格未知字段拒绝；ID 继续 32 位小写十六进制，时间继续 UTC RFC3339。新增业务记录放在工作台数据目录，不放入项目或安装收据。

| 类型 | 字段 |
|---|---|
| TaskRecord | id、specId、title、stage、status、blocker、notes、resources: ResourceRef[]、active、createdAt、updatedAt |
| TaskDraft | title、stage、resources: ResourceRef[]；新建 status=todo、blocker/notes 为空、active=true |
| TaskEdit | title、stage、status、blocker、notes、resources、active；不含 specId |
| StageRecord | specId、stage（禁止 unmarked）、progress: StageProgress、note、resources: ResourceRef[]、updatedAt |
| SavedView | id、name、query: WorkQuery、layout: list/board、groupBy: status/stage、createdAt、updatedAt |
| WorkbenchState v2 | 保留 revision/projects/specs/history，schemaVersion=2；新增 tasks、stageRecords、savedViews |
| ChangeEvent v2 | 保留旧字段；新增 taskId: string/null、lifecycleStage: Stage/null；两者不能同时非空 |
| TaskView | record: TaskRecord、resources: ResourceView[] |
| Snapshot v2 | 保留原字段；新增 tasks: TaskView[]、stageRecords、savedViews；history 使用 v2 事件 |
| MutationResult v2 | 保留 revision/projectId/specId；新增 taskIds: string[]、viewId: string/null |

Task 与 Stage 资源复用 ResourceRef 和现有路径/URL 校验。重复标题允许；批量预览提示同 Spec 已有同名任务，提交按钮防重，服务器不会因请求重试自动去重。写入结果不明时先查询核实，不能盲重试。阶段记录以 (specId,stage) 唯一；任务引用必须存在；新历史引用与所属项目/Spec 一致。

旧 v1 严格按旧结构解析，再在内存转换到 v2：原 ID、revision、个人字段、历史次序全部保留，新集合为空、旧事件新字段为 null。不从当前阶段或旧变更补建阶段完成记录。

read_state、snapshot、query、lifecycle 都只读，不能创建目录、写备份或升级磁盘。Store 继续保存读取时的 FileSnapshot。首次有效 mutation 发布 v2 前，在数据目录以独占创建方式保存原字节至 state.v1-backup-<SHA256>.json；已有同名、同字节普通文件可复用，链接/异字节/写失败拒绝升级。检查容量、revision 和外部变更之后才备份；备份后再次验证源快照，再原子发布。

无效请求/no-op 不升级。备份成功而发布失败时保留 v1 和备份，下次安全重试。未知版本、重复键、损坏、越界和外部修改仍拒绝并保持原文件。v2 不能直接由旧程序读取；恢复备份会回到升级前数据，必须先保留当前 v2 文件，不能自动降级或覆盖。

历史顺序采用 state.history 的追加顺序；snapshot 不再以随机 ID 打散同时间事件。新任务和阶段变更会更新所属 Spec.updatedAt，方便发现真实活动；不伪造 Spec 字段变更。重复保存、访问不新增业务历史。任务停用与恢复写历史；保存视图是偏好操作，更新 revision，不混入 Spec 业务历史。

## 6. 操作与共享查询

沿用 POST /api/commands、MutationRequest.expectedRevision 和 Store 原子写入；新增 Action：

~~~text
addTasks       { specId, drafts: TaskDraft[] }
editTask       { taskId, edit: TaskEdit }
setStageRecord { specId, stage, progress, note, resources }
setWorkStatus  { entity: spec|task, id, status, blocker }
setWorkStage   { entity: spec|task, id, stage }
saveView       { viewId: string|null, name, query, layout, groupBy }
removeView     { viewId }
~~~

setWorkStatus/setWorkStage 是窄字段动作，卡片操作不提交一份可能过期的完整 Spec。原 editSpec 保留，不能清空任务、阶段或视图。任务新增、编辑及阶段记录要求父项目/Spec 启用；在“含停用”视图可以读记录，先恢复父项才能操作子项。

所有业务编辑复用同一字段校验、变更记录和 revision 事务。blocked 的工作项要求 blocker；其他状态保留原 blocker 并标为历史说明。drag、菜单和表单保存经过同一动作。

WorkQuery 精确定义：

对应类型名称固定为 WorkEntity（spec/task）、ActivityScope（active/inactive/all）、WorkSort（updatedDesc/titleAsc）、Layout（list/board）、GroupBy（status/stage）；Rust 枚举和 TypeScript 联合类型使用相同 wire 值。下列数组、布尔及字符串字段均为必需字段，CLI/URL 解析在调用前补齐默认值，HTTP 不用省略字段表示不同语义。

~~~text
entity: spec|task                         默认 spec
q: string                                默认 ""
projectIds: string[]; specIds: string[]    默认 []（不限制）
stages: Stage[]; statuses: Status[]        默认 []（不限制）
activity: active|inactive|all              默认 active
includeClosed: bool                       默认 false
sourceProblemsOnly: bool                  默认 false
sort: updatedDesc|titleAsc                 默认 updatedDesc
~~~

不同字段 AND，同字段数组 OR。q 为 trim 后不区分大小写的单个连续子串，匹配标题、项目名、Spec 名、主文件路径、下一步、阻塞、备注；任务同时匹配自身标题/阻塞/备注及父 Spec 名/路径，不把父 Spec 的备注扩散为所有任务命中。本轮不读正文全文检索。

有效 active = 项目、Spec、任务自身均启用；inactive 为其反集，all 不限制。done/canceled 默认隐藏；显式 statuses 包含终态则规范化 includeClosed=true。关闭终态显示会清除终态条件。未知/已删除过滤 ID 返回空结果及页面提示，不降级为全项目。sourceProblemsOnly 对任务考虑父 Spec 和自身资源，对 Spec 考虑主来源及自身资源；HTTP(S) 外部链接不探测。

updatedDesc 使用 updatedAt 降序、ID 升序；titleAsc 使用 title.to_lowercase() 的 Unicode 字符串序、ID 升序。所有排序和过滤在 Rust 一个 query 模块完成，UI/CLI 不各写一份规则。

WorkItem 字段：entity、id、projectId、projectName、specId、specTitle、sourcePath、title、stage、status、nextAction、blocker、notes、active（有效启用）、updatedAt、hasSourceProblem。任务 nextAction 为空；列表用标题描述要做的事，资源从 Snapshot 中按 ID 取。QueryResult = {revision,readAt,total,items: WorkItem[]}。

新增 POST /api/query：WorkQuery → QueryResult（只读，仍验证会话和 Origin）；GET /api/specs/{id}/lifecycle：LifecycleView = {revision,spec: SpecView,stages: LifecycleStageView[],tasks: TaskView[],events: ChangeEvent[]}。每个 LifecycleStageView = {stage,record: StageRecord|null,taskCounts:{total,unmarked,todo,doing,blocked,done,canceled}}；计数只包含任务自身 active=true 的项，不受默认终态隐藏影响。

保留 snapshot 命令，并增加：

~~~text
yss workbench query --entity spec|task [--q TEXT] [--project ID]...
  [--spec ID]... [--stage STAGE]... [--status STATUS]...
  [--activity active|inactive|all] [--include-closed] [--source-problems]
  [--sort updatedDesc|titleAsc] [--data-dir ABS] [--json]
yss workbench lifecycle --spec ID [--data-dir ABS] [--json]
~~~

command 分别为 workbench.query、workbench.lifecycle，使用既有 yss.output。未登记 ID 返回明确不存在；query 过滤未知 ID 得到空结果。CLI 本轮不新增写命令或“执行任务”命令。

## 7. 页面与交互

沿用 ui/DESIGN.md 的 Tesla Light、现有导航、组件、样式、单请求状态所有者。新增内容不是视觉改版，不复制 Linear 品牌或引入远程资源。

| 页面 | 增量 |
|---|---|
| 工作台 | 汇总 Spec 和任务的进行中/阻塞；显示待验证任务、最近真实活动；每个数字可点入相同查询 |
| 项目主页 | Spec/任务两种范围；启用/关闭/停用数量；现有候选发现增加“选择当前未关联项”，默认仍不选 |
| 全部 Spec（/specs） | Spec/任务对象切换；列表/看板共享查询；状态/阶段分组；保存视图、全记录入口 |
| Spec 详情 | 增加“任务”“生命周期”；保留正文、标记、产物、历史；通过 ?taskId=ID 打开任务编辑 |

全局看板每次只看 Spec 或任务，避免父子重复计数。状态分列六列；阶段分列九列（含未标记），按上述稳定顺序；可隐藏空列，默认保留。任务卡显示父 Spec、项目、阶段、状态、阻塞、资源异常，Spec 卡附任务计数。

拖动只修改当前分组对应字段：状态列改 status，阶段列改 stage，不提供列内手工排序。桌面鼠标拖放是便捷入口；菜单“移动到…”必须完整支持键盘及触屏，不能只实现拖拽。移到 blocked 弹窗填写/确认阻塞说明；取消不修改。保存成功后刷新，卡片进入被筛选隐藏的状态时显示“已移动，该项不再符合当前筛选”。冲突或失败保持原显示，不能误报完成。

详情保存时捕获提交内容与 revision；保存期间继续输入保持 dirty，切换任务时旧请求不能更新新表单。阶段/任务弹层和路由离开使用现有 ModalDialog，由详情页统一收集 dirty 并控制离开提醒；新增组件不使用 window.confirm。动作成功后刷新快照和当前查询；旧响应不能覆盖新筛选。保留上次成功结果、读取时间与错误提示，401 停止请求。

SavedView 保存查询、布局和分组，名称 trim 后 1–80 字符；重复名称允许，以 ID 区分。URL 持有完整当前查询、布局、分组及可选 viewId；打开收藏先载入其配置，之后修改筛选仅影响临时视图，显式“更新视图”才写入。删除收藏不删除任何工作项。切换对象清除不适用的任务定位，不重置项目筛选。

查询结果全部返回但页面分段呈现：列表先显示 50 条、每次“加载更多”增加 50；看板每列同样 50。总数显示完整结果，计数/查询不截断。页面切换与返回保存 URL 和滚动上下文，不引入表格/虚拟滚动框架。

空态、无匹配、未登记、读取失败、父项停用、来源丢失、旧数据阶段未记录要有不同文案。保留可见焦点、文字状态、键盘对话框、恢复焦点、减少动画偏好及 390px 可操作布局。

## 8. Global Constraints

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

## 9. 验收与实证边界

必须演示：v1 只读打开不改字节 → 首次有效写保留备份并升级 → 建立跨阶段任务 → 查询/保存视图 → 状态与阶段看板移动 → 阶段并行/跳过/重开 → 终态/停用完整查询 → CLI 与 UI 结果一致 → 重启保留数据。

重点异常：源文件删除后仍可维护个人记录；两个标签页编辑拒绝覆盖；保存时继续输入保留草稿；取消阻塞弹窗不改状态；重复保存不追加历史；未知数据版本原样保留；仅新增阶段枚举不能自动产生旧阶段完成记录。

实施时需验证必要 Rust/HTTP/CLI 断言、前端类型/lint/build、实际浏览器关键交互及桌面/390px 视觉。本方案没有生成高保真原型，不称为已完成的 UI 还原；最终以本次交互规格和既有视觉系统检查。macOS 原生结果与 Windows/外部 CI 结果分别报告，未运行不得声称通过。

调研依据还包括当前 model/store/service/query 缺口，以及模板中的 spec-template.md、business-ticket-template.md、Backend vertical-slice-ticket-template.md 和 issue-tracker.md。模板没有统一全生命周期状态，故本方案的八阶段是个人工作台显示模型，不是恢复强制研发流程。
