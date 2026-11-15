# 技能选择与所有权

`spec`、`backend` 与 `frontend` 各自从 Bundle 读取安装范围、默认技能、依赖与资源。`3.2.0` 源码新增 `frontend` Profile，此前的 `3.1.4` 构建不支持；正式产物以 GitHub Release 为准。`project` 安装技能及必要项目文档；`skills` 安装技能、宿主入口和必要引用资源。两者都是项目目录内的受管安装，不是全局技能安装。

```text
yss skills list --root /absolute/project --json
yss skills ensure yss-validation --root /absolute/project --dry-run
yss skills ensure yss-validation --root /absolute/project
yss sync --root /absolute/project --installation project
```

收据仅保存用户显式选择，默认技能与传递依赖每次重算。模式切换沿用显式选择；已退役或不可用技能返回原因及建议替代，不自动选择替代技能。Bundle 校验全部可选技能的依赖图，拒绝缺失引用、循环和跨模式混用。

文件处理以原安装摘要为基线：未知文件即使内容相同也不接管；来源未改变时保留本地修改及删除；来源与本地同时变化则整次冲突。受管文件退役时仅删除未改动版本，已修改版本产生冲突；user-owned/protected 文件保留。重复同步应返回 unchanged。

模板与技能只来自当前程序内嵌 Bundle，不自动拉取 main。`skills list` 报告物理文件状态；文件存在不代表宿主已实际加载。独立或全局技能由原安装来源维护，不能对宿主全局技能目录运行项目 init/attach/sync。

## 工程 Git 快照与技能恢复

共享受管安装时，保存实际采用的完整技能、宿主入口、必要引用资源及 `.yss/project.json`。收据记录 Profile、安装范围、来源和相对路径基线，不绑定固定的本机工程根；缺少项目收据时，sync 拒绝继续；残留控制目录可能先报无法识别，attach 也不会自动接管已有同路径文件。存在 `.yss/scaffold.json` 时可保留生成来源，它不能替代项目收据。

新 clone 使用已核实的同一程序/Bundle，分别执行 doctor、skills list 和 sync 预览。doctor 只比较收据与当前文件；同来源、完整且未修改的安装还应由 sync 确认 unchanged。新 Bundle 会重新选择默认技能与依赖，不应把升级到最新版本当作旧安装的精确重现。保留实际 `version --json` 的来源记录；同版本号不保证同一 Bundle。

项目自建技能由所属工程维护，`skills list` 不是这些技能的完整目录索引。未进入收据或当前选择路径的文件由 sync 保留；将来与 Bundle 发生同路径冲突时，继续按现有文件保护处理。Home 按目标工程、工作树和实际技能路径选择版本，读取正文和资源不代表宿主原生注册或工具依赖已经可用。


## Profile 有效内容与 Claude 入口

本轮开发构建在共享内容、specialist 与 Profile 覆盖合并后，从有效 `.agents/skills` 派生 `.claude/skills` 普通文件。Backend 使用自己的覆盖及资源，Frontend 保持自己的可选范围；默认数量仍为 Spec 17、Backend 42、Frontend 13。对象内容、文件模式、渲染策略及目标所有权沿用 Bundle 和安装事务，不依赖源仓的根软链。既有视图与有效内容不同、存在孤立视图或渲染策略不一致时，Bundle 构建拒绝继续。

首次新增 Claude 视图时，还会比较本次计划结束后的 canonical 内容。如果旧安装中保留的 `.agents` 定制或删除状态与新视图不一致，sync 和 ensure 整次返回冲突且不写入。先审阅并统一采用版本，再重新预览；不要删除收据或未知文件来绕过保护。已有宿主视图仍沿用三方同步规则，多个副本不形成持续自动联动。

## Home 与不同宿主

一个安装根使用一个 Profile。同仓多工程按真实子目录各自安装，Home 保存稳定工程地图，任务另行确定实际 checkout/worktree、输入版本和文件责任。父子安装根没有跨根锁或自动所有权仲裁；不能把子模块当普通目录管理。

Home 读取外部工程时，明确读取目标规则、技能正文及资源；相对资源以技能目录为基准，执行命令使用目标工程 cwd。安装文件存在、宿主原生发现、正确来源与工具权限分别核实。Codex 的 `--add-dir` 仅扩展可写目录，不能当技能注册参数。Claude 使用自身技能目录；Pi 和 DeepSeek Harness 复用 canonical 路径；WorkBuddy 保留 `.workbuddy` 入口。具体版本、缓存与实测限制见安装载荷 `setup-yss-harness/references/host-compatibility.md`。

这些行为属于本轮源码及其实际构建 Bundle，不代表旧发行产物已升级。工程提交完整采用载荷和合法收据；用户全局配置与运行期文件不作为产品共享输入。
