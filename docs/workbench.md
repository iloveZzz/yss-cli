# 个人 Spec 工作台

工作台汇总本机多个项目中已关联的 Markdown Spec、具体任务和八阶段生命周期。没有云账号、团队权限或自动阶段推断；阶段、状态、下一步、阻塞说明和待决备注均是个人记录，资源引用同时展示来源声明与个人添加的内容。来源文件保持只读，不写项目的 `.yss`。

```text
yss workbench
yss workbench --no-open --port 0
yss workbench snapshot --json
yss workbench query --entity task --stage verification --json
yss workbench lifecycle --spec <SpecID> --json
yss workbench --data-dir /absolute/personal-data
```

默认只监听 `127.0.0.1`，端口由系统分配，打开带临时会话的浏览器链接。无法自动打开时从终端复制完整链接。会话令牌在 URL fragment 中传递，页面读取后清理 URL，并保存于当前浏览器标签页会话；每次服务重启令牌更换。不要把完整启动链接分享给其他人。按 Ctrl-C 停止，正在执行的原子写入完成后释放锁。

`workbench` 持续服务不接受 `--json`；`workbench snapshot --json` 使用现有 `yss.output` 信封，command 为 `workbench.snapshot`。快照只读，不创建缺失的数据目录。

## 使用

1. 项目页登记本机绝对目录。路径别名合并为同一个项目，不同 Git worktree 分开登记。数据目录不能在 Git 工作树或登记项目内。
2. 在项目页选择相对目录发现 `.md` 文件，默认不勾选；也可输入相对路径手工关联。已关联文件（含停用记录）不能重复登记，需恢复原记录。
3. Spec 详情保存个人阶段和状态。新 Spec 都是“未标记”；阻塞必须有说明；完成仅代表个人完成。选择完成时会提示仍启用且未完成／未取消的任务数，不会自动关闭任务。正文、安全预览、文件时间和可访问性独立于个人标记。
4. “Spec 与任务”切换对象，搜索、项目、Spec、阶段、状态与启用范围由同一个查询实现处理；多选条件内是 OR，条件间是 AND。列表和看板共用结果，默认隐藏已完成与已取消；显式选择终态会显示相应记录。看板按状态或阶段分组，支持桌面拖动及键盘／触屏“移动到…”菜单；不支持列内排序。每列和列表先显示 50 项，可继续展开。
5. 项目移动后编辑根目录；主文件改名后编辑 Spec 相对路径。ID、历史和个人标记保留。停用不删除，可在显示停用记录后恢复。

6. Spec 详情可单条或按行批量录入 1–200 个任务，共用选定阶段及来源引用。新任务均为待处理；原文的 `- [x]` 只是标题文字，不推断完成。任务可维护独立阶段、状态、阻塞说明、备注、资源及启用状态；不支持子任务嵌套或跨 Spec 移动。
7. 生命周期展示调研、分析、规格、原型、交接、实施、验证、交付八个节点。当前关注阶段、Spec 状态、任务状态和阶段进度分别维护。阶段进度可为未标记、进行中、阻塞、已完成、已跳过；阻塞／跳过须填说明，可回退、重开或并行。无记录的节点明确显示“尚未记录”，不会补造历史。任务完成不会自动完成阶段或 Spec；交付标记不等于发布、部署或研发验收。
8. 保存视图记录当前查询、布局及分组。临时更改不自动更新收藏，移除收藏只删除查询偏好。工作台展示进行中、阻塞和待验证任务；“待验证”仅表示任务阶段为验证且未关闭，没有自动运行验证。

项目页“选择当前未关联项”只选择本次发现结果，默认仍不勾选。部分扫描结果会持续提示限制。全局与项目页显示已纳管、已关闭和停用数量；“全部记录”可找回关闭和停用对象，但仍只覆盖已登记项目和手工纳管的 Spec，不代表磁盘全部文件或 Linear 中的事项。

详情可切换渲染预览与原始 Markdown；搜索同时匹配项目名称、父 Spec 名称、标题、来源路径、下一步、阻塞和自身备注。Markdown 仅支持 UTF-8，单文件 2 MiB；不执行 HTML、不加载图片。HTTP(S) 外部链接不探测；项目内相对 Markdown 链接和关联交互文档可阅读。文档接口允许已授权项目内用户主动打开的 Markdown，仍拒绝绝对路径、越界、链接和子模块。已构建的本地 HTML 原型在独立本地端口预览，工作台不执行构建命令或任意系统代码；预览隔离于工作台会话和 API，拒绝越界、链接及源码入口。目录扫描最多处理 10,000 个候选目录项，深度 16；遇到超限或无法访问时提示结果不完整。忽略安装/依赖/构建目录，不穿越 symlink、嵌套 Git 工作树或子模块。根身份及 Git 边界检查独立于候选枚举预算。

## 主 Spec 与内容确认

一个主 `spec.md` 关联业务正文、交互说明与原型，草稿始终可阅读。`register` 是可重复的登记/刷新入口，保留现有主 Spec 的启用状态；只有同目录且没有独立 `yss-spec` 元数据的 `interaction.md` 可自动合并并停用旧记录，个人备注和历史保留。引用其他独立规格不会将其停用。来源元数据管理的资源会随刷新新增、移除或改路径，个人手工资源保留；主文件中的单个 JSON 注释块维护产物和问题：

```markdown
<!-- yss-spec {"schemaVersion":1,"scope":"用户新增与编辑","contentStatus":"draft","resources":[{"kind":"interaction","label":"交互说明","targetType":"local","target":"docs/spec/users/interaction.md"},{"kind":"prototype","label":"可操作原型","targetType":"local","target":"prototypes/users/dist/index.html"}],"questions":[{"id":"OQ-01","title":"谁可以停用用户？","kind":"decision","impact":"决定停用入口权限","recommendation":"仅管理员","blocking":true,"affectedScope":"停用用户","status":"open","resolution":"","appliedDecisionId":null}]} -->
```

`contentStatus` 由 Agent 声明 `draft` 或 `ready`（缺省草稿）；个人内容状态派生为未标记、草稿、可确认、已确认，与个人工作 Status 分开。`resources.target` 是项目相对路径；远端资源需本地快照才能锁定确认版本。事实问题由 Agent 查证并写明 resolution，业务问题通过工作台或已获授权的 `respond` 保存真实答复。回复先显示待回写；Agent 落实正文、AC、交互和原型后，在原问题保留 ID，写入 `status:resolved`、`resolution` 和实际采用的 `appliedDecisionId`。问题语义变化会要求重新核对，回复历史不丢弃。

延期须记录原因、影响、排除范围、复查日期或条件，Agent 回写为 `deferred` 并引用实际回复 ID。关键问题只能在完整排除其受影响范围后确认剩余范围；确认文案明确列出排除内容。到期按答复保存的时区计算；条件延期会持续展示复查条件，由用户核对条件是否已满足。打开/刷新时显示待决定、待回写与到期信息，没有后台推送。

只有 Agent 标记 ready、来源可读且无未答/待回写/到期/变更待核对事项，才能明确确认当前范围与版本。摘要覆盖正文、关联文档和原型可服务目录内的实际静态资产；任何字节变化都会重新要求确认（含排版变化，比仅识别实质变更更保守）。确认历史保留，Status=done 不代表内容已确认。仅暂停问题影响的范围，未受影响工作可继续。

## 数据与恢复

个人数据使用 `ProjectDirs::from("com", "ysstech", "yss-workbench").data_local_dir()`；终端启动信息显示实际路径。目录中 `state.json` 保存 schemaVersion=3、revision、项目、Spec、任务、阶段记录、收藏视图、问题答复、范围确认和 before/after 历史，`lock` 保护单服务进程。备份时先退出服务，复制完整数据目录；恢复时在服务退出状态下恢复同版本数据文件。

保存使用 revision 校验、同目录临时文件和原子 rename。两个浏览器页面保存同一旧版本时拒绝覆盖；保留输入并可载入最新标记后重做修改。外部直接修改状态文件会阻止后续覆盖，需重启核实。损坏、未知版本或超 16 MiB 文件保持原样并报错，不自动重置。写入前也按读取时的 JSON 结构限制校验（最多 100 万节点）；达到字节或结构容量上限都拒绝写入并保留原文件。写入结果不明时先刷新核实；不要反复点击保存。无永久删除或自动清理。

刷新失败时保留上次快照并显示读取时间。源目录丢失、断开或文件删除不会删除个人状态。浏览器重载会丢失未保存草稿，离开编辑页前提供提醒。

## 构建

源码构建顺序见 [构建说明](build-install.md)。UI 使用 Vue 3、TypeScript、Vite、pnpm；Rust 用 AssetLoader 注入静态资源，只有用户二进制内嵌 `ui/dist`。构建摘要覆盖 UI 源码、配置、锁文件和实际产物。发布包仍只包含 `yss` 与发行清单，没有额外运行时服务。

正式平台沿用 macOS ARM64 与 Windows x64。各次实际构建、原生运行和 CI 结果分别说明，未运行的平台不能视为验证通过。

## v1/v2 升级与恢复

v1/v2 只读打开时在内存中补充 v3 结构，原文件、ID、revision、状态和历史次序不变；v1 未记录的阶段进度保持未知，v2 已有阶段记录保留。首次有效写入前，按原文件字节 SHA-256 创建 `state.v<原版本>-backup-<sha256>.json`，核验备份后原子发布 v3；无变化或非法动作不升级。备份已存在时只接受字节完全相同的文件，不覆盖异内容、链接或目录；备份失败不发布状态。

异常退出可能留下 `state-*.tmp` 或 `backup-*.tmp`，它们不作为有效状态或备份读取。若备份已完成而状态未发布，重启后仍读原版本，下次有效写入复用经过核验的备份。损坏或未知版本保留原文件并报错。

旧版程序不能读取 v3。确需回退时先退出服务、保留整个当前 v3 数据目录，再将经过摘要核验的原版本备份复制到另一个独立数据目录的 `state.json`，用旧程序的 `--data-dir` 指向它。原版本备份只有升级前的数据，新任务、阶段、收藏及升级后的编辑不会出现在旧版里；不要覆盖当前 v3 原件。

## 只读查询

```text
yss workbench query --entity task --project <ProjectID> --status doing --status blocked --json
yss workbench query --entity spec --activity all --include-closed --json
yss workbench query --entity task --q 接口 --stage implementation --stage verification --sort titleAsc --json
yss workbench query --source-problems --json
yss workbench lifecycle --spec <SpecID> --json
```

`--project`、`--spec`、`--stage`、`--status` 可重复；未知项目／Spec ID 返回空结果，不扩大范围。`--activity active|inactive|all` 默认 active；任务有效启用同时要求任务、父 Spec、父项目启用。排序支持 `updatedDesc`（默认）和 `titleAsc`，相同键以 ID 稳定排序。外部链接不计为来源异常。CLI、HTTP 和界面复用相同查询语义；读取不创建数据目录。

JSON 信封 command 分别为 `workbench.snapshot`、`workbench.query`、`workbench.lifecycle`。query 返回 revision、readAt、total、items；lifecycle 返回 Spec、八节点、任务和真实历史。HTTP 的 `/api/query`、`/api/specs/{id}/lifecycle` 使用相同的本机会话保护。没有通过 CLI 自动执行任务、提交 Git 或写回第三方平台的功能。

## 3.4.0 原发行记录

以下为 3.4.0 既有发行的范围和检查记录；上文新增主 Spec、问题闭环、内容确认与静态原型预览从 3.4.1 起提供，该版本的实际验证结果见对应 Release。

CLI 3.4.0 提供个人 Spec 工作台，源码已合入主分支。模板 Bundle 固定来源为 `a4981d20ab992ae62693262cb4791e1628140877`（Template 5.4.1），由干净检出生成；其他工作树未提交的主题改动不属于该发行来源。前端纯模型与 URL 编解码独立于会话启动，用户运行时无需 Node。

验证材料使用系统临时目录，不向仓库加入持久测试套件或检查脚本；目录可能被系统清理。工作台原生运行范围为本机 macOS ARM64；Windows 仅执行发行构建和静态包检查，未验证原生交互、断电或硬件故障。各版本 CI 实际结果见对应 Release。演示项目均为临时验证材料，实际业务项目没有被自动纳管。

实际检查：9 项临时 Rust 场景，覆盖 v1 无写读取、备份／发布失败、容量上限、任务原子批量、独立进度及统一查询；release 程序的升级→写入→重启往返、HTTP/CLI 对照和会话／Host／Origin／大小／409；浏览器的双窗口冲突、保存中输入、看板拖动与菜单、收藏、部分扫描、项目返回、第 75 项展开与滚动恢复、390px 布局及键盘导航。类型、lint、UI 生产构建、cargo fmt/clippy 和 macOS ARM64 release 构建通过。编译与本机检查不等于正式发行。

独立审查确认的问题已修复：项目范围返回、展开结果保持、项目默认终态过滤；来源变化但 revision 不变时刷新查询；项目保存期间保护输入；按实际时间倒序排列不同精度／时区的时间戳；Spec 完成时提示剩余未完成任务数。新增临时检查中，查询刷新与竞态 3/3、时间排序 14/14 通过；浏览器复验了来源丢失／恢复、保存中禁用、失败后保留输入，以及完成提示不改变任务记录。

实施取舍：验证材料留在系统临时目录，可能被系统清理；升级备份先写临时文件再完整发布，异常终止可能留下不会被当作状态读取的临时文件；纯前端模型单独放入 `workbench-model.ts`，便于不启动会话的解析校验，增加一个文件但保持原导出兼容。
