# YSS CLI

YSS 使用 Rust 安装项目模板、按需补装 Skills、生成 Java / Vue 空骨架和更新自身。CLI 命令与工作台界面由单个 `yss`（Windows 为 `yss.exe`）提供，无需 Go、Bun、Node 或 Python；当前源码的 Pi Agent 作业另需本机 Pi 1.5.2、1.6.0 或 1.6.1 及其运行环境。处理 Git 工作树时需要 Git；生成 Java 工程不执行 Maven，实际构建需要对应 JDK。

当前源码版本为 Rust `3.18.4`，发行目标为 macOS ARM64 与 Windows x86_64，实际内嵌模板和发行产物以 GitHub Release 为准。旧 Go 安装请使用新目录安装 Rust 版本。`spec`、`backend`、`frontend` 是三种 Profile，`project` 和 `skills` 是项目内的两种安装范围。

`3.2.0` 新增 `frontend` Profile，构建时需使用包含该 Profile 的模板。此前的 `3.1.4` 构建仅支持 `spec`/`backend`，模板文档更新不会改变已安装程序；使用前查看实际 `yss help init`。

## 快速开始

先查看当前程序版本、命令和初始化参数。新项目使用 `init`，已有工程使用 `attach`；先加 `--dry-run` 预览，确认后去掉该参数执行。

```text
yss version
yss --help
yss help init
yss init --root ./my-project --profile spec --dry-run
yss init --root ./my-project --profile spec
yss skills list --root ./my-project
yss sync --root ./my-project --dry-run
```

初始化、已有项目接入、技能、脚手架、升级及参数示例见[命令与输出](docs/cli-help.md)。写命令直接执行；`--dry-run` 只预览，不创建根目录、锁、缓存或保存计划。未知文件不接管，即使内容相同；受管文件本地修改与来源修改冲突时整次拒绝。事务中断后先 `recover`，已提交事务只重试安全清理。

```text
pnpm --dir ui install --frozen-lockfile
pnpm --dir ui typecheck
pnpm --dir ui lint
pnpm --dir ui build
cargo run --locked --bin yss-bundle -- --source-root /absolute/template --out assets/bundle.tar.gz
cargo build --locked --release --bin yss
```

Rust 工具链锁定在 `rust-toolchain.toml`。Bundle 来自实际模板内容并随程序内嵌，必须先生成。只分发用户程序，内部 `yss-bundle` 用于构建和打包。

- [命令与输出](docs/cli-help.md)
- [构建、发行包与安装](docs/build-install.md)
- [格式与平台范围](docs/compatibility.md)
- [技能选择与所有权](docs/skills-discovery.md)
- [Java / Frontend 脚手架](docs/scaffold.md)

`3.3.0` 新增 `yss scaffold frontend --project-name my-frontend --output-dir .`，内嵌 Template `5.3.1`；先核对实际 `yss scaffold frontend --help`。它生成最小 Vue 工程，不安装依赖；`init/attach --profile frontend` 仍只安装技能及资料。生成与运行边界见 [脚手架说明](docs/scaffold.md)。

`3.3.3` 更新内嵌 Template 为 `5.3.4`，分发三个 Profile 的技能地图及六个安装入口导航，明确 Spec 的 handoff 交接职责，并完善第三方问题、Agent 文档事实来源与前端边界检查方法。默认技能仍为 Spec 16 项、Backend 41 项、Frontend 13 项，CLI 命令和依赖保持不变。公开技能与 CLI Bundle 使用各自真实来源记录。

`3.5.0` 内嵌 Template `5.5.0`，Spec 默认安装 17 项技能并作为产品 Home 的工程会话入口，Backend/Frontend 默认集合为 42/13。项目仓库地图由项目维护；从 Home 进行明确研发时核实目标工程规则与上游版本。此版本同时交付主分支的 Spec 命名改进、SQLite 工作台存储和损坏数据保护；既有命令接口保持。

`3.6.0` 内嵌 Template `5.6.0`，增加初始化和脚手架完成后的 `nextSteps` 指引，明确 Home 关系与 `attach --installation skills` 的后续动作；完整有效且已清理的 committed 事务在目录身份变化后保留为只读历史。工作台 Markdown 预览支持 Mermaid 图。实际模板来源以发行记录为准。

`3.7.0` 内嵌 Template `5.7.0`，新建 Backend 默认采用 YSS `4.0.0-SNAPSHOT`、Spring Boot `4.1.2` 和 Java `17`。MVC/DDD 只需五项工程参数，可显式选择其他版本或完整自定义坐标。技能包含 11 项 Boot4 组件来源索引，配方继续标注实际验证范围。

`3.8.0` 内嵌 Template `5.8.0`，整合数据库分析技能的同源模型、精炼说明和浅色 Excel 规范，增强 llm-wiki 的证据维护、来源变化和巡检，并提供可选 QMD 检索指引。命令接口与三个 Profile 的默认技能集合保持兼容；QMD 工具与模型由使用方按实际授权配置，不随 CLI 分发。

`3.9.0` 内嵌 Template `5.9.0`，增加来源核验、项目接续、技能改版对照和 PR/MR 持续跟进，补充用户手册与培训材料。数据库技能完善单表 DSL、复杂 XML、实际方言、JSON 绑定、部分加载更新、引用并发保护和多数据库验证指引。三个 Profile 的默认技能集合为 Spec 17、Backend 42、Frontend 13 项。

`3.10.0` 内嵌 Template `5.10.0`，完善 Spec / Issue 接续、Git / remote / Issue tracker 工程接入，并为 Frontend 默认加入共享 `implement` / `to-tickets`，默认技能集合为 Spec 17、Backend 42、Frontend 15 项。CLI 帮助与 `nextSteps` 补充完整工程接入指引，区分文件安装与 Git、远程、tracker 配置，并保留已有工程配置；命令接口与依赖保持不变。

`3.11.0` 内嵌 Template `5.11.0`，工作台增加工程维护、产品 Home 绑定、Codex 隔离运行与成果接收、可恢复草稿、确认版本比较和在线一致备份。模板补充复杂研发组织、环境交付与运行反馈接续，以及客户跟进全生命周期 Demo 和工作台指南。默认技能集合为 Spec 17、Backend 42、Frontend 15 项。

`3.12.0` 内嵌 Template `5.12.0`，工作台按目标、阶段、参与工程和实际证据自动汇总生命周期进度，支持版本适用性核实、人工更正、关键确认和历史追溯。新增 `workbench progress set-goal/report/confirm/reconcile`，供原 Agent 或终端持续上报；已绑定的 PR/MR 与 CI 状态参与跟进。模板统一进度上报约定，并完善后端模型与分层规则；默认技能集合保持 17/42/15 项。

`3.13.0` 内嵌 Template `5.13.0`，macOS ARM64 的新 Agent 作业切换到本机 Pi 1.5.2，提供隔离工作区、按次命令授权、离线命令沙箱和可续接会话。执行详情按会话、工具步骤、问答和成果组织，保留 Codex 历史审阅与成果接收；Windows 的 Pi 运行入口受平台限制。模板新增中文技能导航，调整业务分析、规格、界面设计及研发写作入口，补充 Spec 归属提示和 PO/Mapper 生成工具。默认技能集合为 Spec 18、Backend 43、Frontend 16 项。

`3.14.0` 内嵌 Template `5.14.0`。工作台启动和登记项目时自动发现主 Spec，识别同目录的交互说明和已构建原型。Pi 作业排队前核对所有参与工程的 Git HEAD，并要求显式带入尚未提交的主规格。模板细化 Spec 两项确认与研发写入前的 Git 核对。

`3.18.4` 内嵌 Template `5.18.4`。工作台将主要入口移至左侧图标栏，首页可查看待办、执行记录和规格推进情况。项目详情汇总已登记规格与待登记候选项，规格详情展示本次目标的进展、阻塞和上次核验时间。

`3.18.3` 内嵌 Template `5.18.3`。工作台采用紧凑的浅色毛玻璃布局，支持查询生命周期状态、核实下一步及确认阶段。Pi 按工程选择读取、编辑或执行权限，可查看关联 GitLab CI 作业证据并续接历史运行。

`3.18.2` 内嵌 Template `5.18.2`。Pi Agent 在所选原工程运行，每轮默认只读，可明确授权编辑并审阅本轮变化。会话支持排入后续任务，工作台在后台核对来源并分页查询。

`3.18.1` 内嵌 Template `5.18.1`。参数错误给出对应帮助命令，工作台进度上报区分已登记目标摘要与当前产物摘要。项目同步保留自有的 Issue tracker 配置。

`3.18.0` 内嵌 Template `5.18.0`。Pi 工作区支持连续会话、待处理筛选、按轮次审阅成果、差异选行返工和逐文件接收。续办分别提示原工程和隔离稿的材料版本，默认沿用上轮模型。macOS ARM64 支持 Pi 1.6.1。

`3.17.1` 内嵌 Template `5.17.1`。工作台可搜索本机 Pi 的模型，为本次新运行指定模型或恢复 Pi 默认模型。连接测试和启动使用本次选择，历史会话续接使用原模型。模型与 ChatGPT 登录方式不兼容时，工作台给出具体提示。

`3.17.0` 内嵌 Template `5.17.0`。工作台统一组件和加载反馈，展示 Pi 实际使用的配置目录、模型及凭证检查结果。关联资料更新时，Spec 详情展示当前仍采用的已确认版本。模板增加业务规格正文审查，并补充 FormKit Schema 集成指引和 VueUse 参考资料。

`3.16.1` 内嵌 Template `5.16.1`，更新原型单边框焦点与长列表/编辑示例，并将工作台原生输入和下拉控件统一为单边框焦点；错误、键盘及强制颜色提示保留。

`3.16.0` 内嵌 Template `5.16.0`。工作台按每份 Spec 实际采用的材料和检查范围判断确认，保留已交付目标历史，并支持比较本轮确认材料或当前来源。Pi 执行前核对运行版本、沙箱和就绪状态。Frontend 默认安装 19 项技能。

`3.15.0` 内嵌 Template `5.15.0`。CLI 和工作台按技能入口与安装收据区分未安装、已安装、自行维护、文件缺失和本地定制。工作台复查失效答复，并在接收续办成果时校验关联决定与采用版本。默认技能集合为 Spec 18 项、Backend 43 项、Frontend 18 项。

源码构建工作台资源需要 Node 24（^24.14.1）和 pnpm 10.15.0；浏览器界面内嵌到用户程序，Pi 作业使用本机 Pi 的运行环境。内部 `yss-bundle` 可在没有 `ui/dist` 时构建；`yss` 和 `cargo clippy --all-targets` 必须先构建 UI。

## 个人 Spec 工作台

配套 Template 5.18.4，提供自动生命周期汇总、主 Spec 登记、关联文档与原型阅读、草稿问题提醒、答复与延期、Agent 回写及范围版本确认。三个 Profile 均可取得所需设计模板。来源声明与个人工作标记分别保存，草稿始终可阅读。

```text
yss workbench
yss workbench --no-open
yss workbench snapshot --json
yss workbench register --root /absolute/project --source docs/spec/users/spec.md --json
```

`3.11.0` 增加工程维护、产品 Home 导入绑定、Codex 隔离运行、可恢复草稿、确认版本差异和在线一致备份。Spec 列表、状态／阶段看板与个人生命周期记录继续保留，已有任务可通过 CLI 查询。

工程详情和“设置”提供工程维护，“项目”页可导入原仓库地图并绑定本机工程。在 macOS ARM64 上，可从 Spec 详情使用本机 Pi 1.5.2、1.6.0 或 1.6.1 在所选原工程运行。运行或续接前可核对模型和凭证，按需测试连接。每轮默认只读，选择“编辑原工程”后文件写入立即生效，可在执行详情审阅变化、答复问题、停止或续接。历史隔离成果仍可逐文件接收。

问题答复保存后显示待回写，可复制要求或授权 Pi 在原工程更新规格与原型。支持草稿的表单显示本机保存与冲突恢复入口，新确认版本保存正文与资源快照，旧确认缺少正文时明确提示。设置页可在线备份个人数据，并恢复到新的或空的个人数据目录。关闭浏览器后作业继续，退出 CLI 会中断活动作业。工作台说明列出完整操作与旧版离线备份的适用步骤。[工作台说明](docs/workbench.md)

当前源码使用 `workbench.sqlite3` 保存个人数据。旧 JSON v1/v2/v3 在首次启动新版工作台或执行写入命令时自动备份并迁入 SQLite，即使该业务写入最终没有状态变化也会完成迁移；纯查询继续只读旧文件。迁移保留已有记录，`state.json` 随后仅作为存储标记保留；旧版程序不能读写迁移后的目录。
