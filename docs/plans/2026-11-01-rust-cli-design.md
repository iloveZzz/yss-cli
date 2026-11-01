# YSS CLI 纯 Rust 重写设计

状态：用户已于 2026-11-01 批准设计，并确认将 Rust 组件与工具调研建议补充到本 Spec；实施计划同步更新，尚未开始产品代码实现。

本方案依据 CLI 仓库提交 `8c5e1a90ff83ac83a0640a64b8c8109c3fc5a5e6`、当前现役命令和本次对话。它是新主版本的设计，不表示 Rust 工程、依赖、平台验证或发行已经完成。

## 1. 已确定的方向与本方案的提议

用户已确定：用纯 Rust 全量替换 Go，不采用 Bun 运行时，不考虑向下兼容，并先取得设计方案。此前比较过纯 Bun、纯 Rust和 Bun＋Rust；用户选择纯 Rust。

“纯 Rust”在本方案中指 YSS CLI 与 Bundle 生成逻辑均由 Rust 实现，发行程序不调用 Go、Bun、Node 或 Python。系统 API、操作系统动态库、Git 及必要构建链接器不因此被排除。Java 脚手架仍生成 Java 工程，不改成 Rust 项目。

以下提议已随整份设计获得用户批准；后续按该边界制定和执行实施计划：

| 项目 | 提议及理由 |
| --- | --- |
| 工程组织 | 一个 Cargo package、一个 library、两个 binary；与约一万行现有实现的规模相称 |
| 对外程序 | 仅发行 `yss` / `yss.exe`；`yss-bundle` 只在构建环境运行 |
| 功能范围 | 保留全部现役命令的用途，不恢复退役命令 |
| 数据格式 | 新格式身份与版本，不读取旧项目状态、旧事务或旧发行包 |
| 执行方式 | 文件流程同步执行；HTTP 下载局部使用异步，支持超时和取消 |
| 事务 | 一个共享文件事务模块；新建整个工程采用暂存目录发布 |
| 发行目标 | 设计覆盖 macOS/Linux/Windows 的 x64、ARM64；逐目标实际验证后声明支持 |
| 包体 | 含模板可执行文件以不超过 15 MiB 为初始优化目标，不为体积减少保护或功能 |

工作量属于整个 CLI 的重写项目。它包括核心程序、Bundle 工具、模板输入对接、CI 和使用文档，不能以替换参数解析器或编译成功作为完成。

## 2. 当前依据与方案比较

当前 Go 源码共 63 个文件、9,579 行：脚手架约 30%，文件事务与路径保护约 31%，升级器约 14%，命令入口约 3%。现有 Bundle 文件为 3,054,590 字节，约 2.91 MiB。没有持久测试套件；仓库允许在临时隔离材料中完成必要验证。

| 已比较的方案 | 主要优点 | 对本项目的代价 | 结论 |
| --- | --- | --- | --- |
| 纯 Rust | 一套运行时实现，系统操作可集中控制，原生发行 | 全量重写与 Rust 构建环境 | 用户已选择 |
| 纯 Bun / TypeScript | 模板与规则处理方便，可单文件发行 | 事务算法须重新证明，包含 JS 运行时 | 不采用 |
| Bun＋Rust 运行时 | TS 规则配合原生执行核心 | 双构建、协议、版本配对及双侧错误处理 | 不采用 |
| Bun 构建工具＋Rust 程序 | 用户运行时仍为 Rust | 两种语言维护；当前没有必须保留的 TS 构建工具 | 本次不引入 |

对纯 Rust 内部结构，采用单 package 模块化；暂不拆多 crate workspace。没有独立发布或多产品复用需求，不建立插件框架、通用工作流引擎或逐函数 trait 包装。

现有 `jsonx.go` 和 `collate.go` 中约 544 行承担 JavaScript 顺序、输出和排序模拟。新实现替换这些职责，不追求与旧程序逐字节一致。输入校验、路径保护与实际生成规则仍须保留。

另有未实施草案 [CLI 模板收敛配套优化](./2026-11-01-cli-template-convergence.md)。其显式依赖、安装模式隔离及 Git 边界问题作为输入；本设计不沿用其 Go metadata 兼容要求，也不授权执行或覆盖该草案。实现前按实际仓库状态重新核对并行改动。

## 3. 目标、范围与明确排除项

目标是让现役 YSS 安装、技能分发、Java 骨架生成和程序升级全部由 Rust 完成，并让文件所有权、冲突和失败恢复有明确行为。

| 命令 | 新版本承担的工作 |
| --- | --- |
| `init` | 在不存在或空的目标根安装明确指定的 Profile |
| `attach` | 接入已有目录，保留用户内容，未知受管目标冲突时停止 |
| `sync` | 使用本程序内嵌 Bundle 同步；支持 project/skills 切换 |
| `skills list` / `skills ensure` | 查询技能；按显式依赖闭包补装 |
| `scaffold catalog` | 查看 Bundle 中的平台与必需输入 |
| `scaffold ddd` / `scaffold mvc` | 创建全新 Java 后端工程，不自动运行 Maven |
| `upgrade` | 从固定 GitLab 或显式本地归档安装/升级 CLI |
| `doctor` | 只读诊断项目或工具安装状态 |
| `recover` | 只恢复本 Rust 格式的未完成文件事务 |
| `version` / `help` | 无需项目安装即可查看版本、来源与帮助 |

Profile 为 `spec`、`backend`，安装类型为 `project`、`skills`。新安装默认 project，已有 Rust 安装沿用记录值。现有参数名称继续使用，原因是无需重新学习；不保证旧帮助文本、JSON字段、错误码或参数边缘行为兼容。DDD/MVC 保留当前必填的工程、Java/Spring Boot 平台、父 POM 与 YSS 组件坐标；不新增示例开关、交互向导或隐含平台版本。

不做旧 Go 状态迁移、双引擎运行、旧命令转译、强制覆盖、自动合并用户编辑、保存/加载项目执行计划、插件服务、在线拉取模板、自动提交推送或恢复退役生命周期能力。

以下情况属于失败，即使程序能编译或运行：覆盖用户修改；把未知文件登记为自己拥有；越过子模块边界；泄露鉴权信息；把未完成事务报告为成功；构建空 Bundle 发行包；把未验证平台写成受支持。

## 4. 模块与调用边界

建议目录职责如下；实际文件拆分由实现者根据代码规模决定，不逐个照搬 Go 文件。

| 模块 | 负责 | 允许依赖 |
| --- | --- | --- |
| `cli` / `output` | clap 参数、帮助、输出协议、退出码 | 各用例模块及模型 |
| `model` / `error` | 有类型的 Profile、所有权、描述符、格式和错误 | Serde、标准库 |
| `bundle` | Bundle 生成、加载、校验、安装视图与依赖闭包 | codec、fs 的只读能力、Git |
| `project` | init/attach/sync/skills/doctor 的改动决策 | bundle、transaction、fs |
| `scaffold` | 平台校验、DDD/MVC 渲染、整个新目录发布 | bundle、codec、fs |
| `updater` | 固定来源、包校验、工具安装、平台选择 | network、transaction、fs |
| `transaction` | 加锁、准备、持久化、应用、回滚、恢复 | model、codec、fs |
| `fs` / `platform` | 根路径、Git 边界、文件身份、锁和原生发布 | 标准库、经验证的 cap-std/cap-fs-ext 及必要目标平台库 |
| `codec` | 严格 JSON/YAML 输入与确定性写出 | Serde、YAML 解析器 |

`project` 与 `updater` 使用事务模块，不直接改业务文件。`scaffold` 仅在私有暂存目录中生成文件，最终通过禁止覆盖的目录发布操作进入目标路径，不复用已有项目的逐文件更新流程。

核心接口围绕完整行为组织：计算 `ChangeSet`、提交事务、读取状态、恢复事务、发布新目录。`ChangeSet` 只存在于内存，包含预期原状态、目标内容、权限与只读输入约束。不同层不以动态 JSON map 传递已知结构。

操作类型用枚举区分创建、替换、删除和创建目录，避免用多个布尔值表达非法组合。`Missing` 与文件描述符分开建模。`TransactionPhase`、`Ownership`、`Profile`、`InstallationKind` 和可机读错误码使用枚举。

CLI 统一输出；核心模块不自行打印成功消息、不调用 process exit。文件句柄/锁通过作用域释放，事务提交和回滚显式执行。不会把 Drop 当作断电或 SIGKILL 恢复机制。

## 5. 输入、输出与格式身份

所有新持久格式包含 `format` 和 `formatVersion`，两者必须匹配，不借用旧 `schemaVersion` 判断新旧状态。初始格式版本均为 1，身份分别为 `yss.project`、`yss.tool`、`yss.bundle`、`yss.release`、`yss.transaction`。

| 资产 | 建议位置及主要内容 |
| --- | --- |
| 项目安装记录 | `.yss/project.json`：Profile、安装范围、变量、Bundle 来源、用户显式选择的技能、受管路径与安装基线 |
| 工具安装记录 | `.yss/tool.json`：CLI 版本、Rust target、发行归档摘要、受管载荷描述符 |
| 事务记录 | `.yss/transactions/<id>/`：不可变计划、前后内容对象、执行日志及阶段 |
| 互斥文件 | `.yss/lock`；独立于可清理事务，不通过删除锁文件解锁 |
| 脚手架来源 | `.yss/scaffold.json`：生成输入、平台、模板来源与生成文件描述符 |
| Bundle | `assets/bundle.tar.gz`；构建生成，不进入版本库 |
| 发行归档 | 根下 `yss`/`yss.exe` 与 `release-manifest.json`；安装记录由安装器生成 |

项目根与工具根不能同时声明两种安装身份。根目录出现旧 `.yss.json`、旧家族身份、旧工具收据、或无法识别的 YSS 控制状态时，只读说明“不支持旧安装格式”，不写新记录掩盖它。只识别旧标记以保护文件，不实现旧内容迁移。

JSON 输入拒绝重复键、尾随文档、非法 UTF-8、非法 Unicode 转义及超界数字；固定结构拒绝未知字段，真正可扩展的映射在类型中显式列出。YAML 只接受单文档、字符串键，拒绝重复键、锚点/别名、merge key、显式自定义标签和非有限数字。读取遵守大小及深度上限；归档/累计解压沿用 256 MiB，发行 manifest 沿用 8 MiB，YAML 深度不超过现有 512。

已知数值字段使用有范围的整数。版本、摘要和 Maven 坐标是字符串。通用资产数字保留十进制精度，不先转换成 f64 再用于摘要或生成文件。Serde 的默认反序列化能力不能替代这些校验。

新 CLI JSON 输出为单个对象，固定包含 `format: yss.output`、`formatVersion: 1`、`command`、`status`、`result`、`error`。错误有 code、中文 message、可定位 path/context；不输出 token、完整鉴权 URL 或未脱敏网络错误。stdout 放结果，stderr 放人类诊断。成功退出 0，参数错误 2，其他失败 1；取消也返回非零并有明确 code。帮助/版本通过统一出口处理。

稳定的输出协议便于新消费者使用，但本次不兼容旧 envelope。JSON 意图须在参数解析失败时仍被识别，避免 `--json` 错误路径变成不可解析文本。

## 6. Bundle、模板输入与来源

模板内容继续来自 `yss-spec-project-template`；Spec 与 Backend 的权威技能源及平台投影保持原有内容来源。Rust 仓库不另维护一份 Java 模板。

构建工具显式接收模板工作树路径；按实际 Git HEAD、工作树状态、允许分发文件和真实字节生成 Bundle。包含允许的已跟踪文件与未忽略新文件；安全解析技能投影，禁止任意越界链接。dirty 来源标为 working-tree。构建前后核对输入快照，输入变化则失败，不能生成混合时点 Bundle。不改写既有正式标签记录。

输入使用现有两套 `.template-source/distribution/bundle-profile.json` 路径。新生成器要求每个安装类型分别显式声明 `skillDependencies` 和资源 `skillReferences`，不从 Markdown 提及推断必需依赖。缺失新必需字段直接报告源路径，不回退旧推断。

独立审阅已只读确认：当前 Spec 与 Backend 清单均为 schemaVersion 6，两个安装类型已有 `skillDependencies`、`skillReferences`，且存在 `retiredSkills`。这是字段存在的事实，尚未完成全部闭包和资源有效性验证。Rust 生成器直接消费并严格校验这些现有声明，只在发现确切差异时局部更新已授权源清单；不重新添加整套字段。CLI 收敛草案仍标为未实施，不能据模板字段存在推导旧 Go CLI 已正确支持它们。

依赖闭包校验全部可选技能，拒绝不存在的节点、自环和依赖环，结果去重且稳定排序。project 与 skills 分别计算资源，不互相覆盖。`skills ensure` 保存用户请求的技能集合；`sync` 使用当前安装范围的默认集合与仍可用的显式集合重新计算闭包。默认集合不因一次 ensure 固化为永久显式选择。

模板清单中的退役说明只用于诊断，不把 replacements 自动变成安装请求。若源尚无退役数据，报告 unavailable，不硬编码历史技能映射。

归档采用一个 manifest 加 `objects/<sha256>` 原始字节对象，按摘要去重；去掉旧 Base64 JSON 存储层。路径到摘要、权限和所有权放在 manifest。归档条目按稳定顺序输出，固定 mtime、uid/gid 等无关元数据；不打包符号链接、设备文件或外部路径。

摘要规则只定义一次：

1. 文件摘要是原始字节的 SHA-256，不归一化用户换行。
2. manifest 使用固定字段、排序映射、UTF-8 与固定末尾换行写出；摘要针对最终保存字节。
3. 来源快照针对排序后的路径/描述符结构；不包含随机临时路径和本次构建时间。
4. Bundle 摘要针对最终归档字节，保存在生成报告和编译常量中，不写入自身造成循环。
5. 事务计划摘要针对落盘原始字节，读取时不通过重新序列化猜测旧字节。

`include_bytes!` 只放在 `yss` binary 的嵌入入口；library 的加载器接收字节。`yss-bundle` 可独立编译，不需要空 Bundle feature。缺少 Bundle 时发行 binary 编译失败；生成器不能提供一个可误发行的空资产 yss。

## 7. 项目同步与所有权决策

新项目仍使用来源声明的 managed、managed-customizable、generated、user-owned、protected 分类；类型明确映射到下表行为。user-owned/protected 是只初始化一次的内容；已有内容不覆盖，退役时不删除。新生成的项目记录不继承旧的本地解决记录。

每个受管文件保存最后一次成功安装的完整描述符：内容摘要、可观察权限及类型。只有事务提交后才更新安装基线。来源未改变与用户本地改变可以独立判断，不把用户内容重新登记为模板基线。

| 当前路径 | 目标/来源情况 | 决策 |
| --- | --- | --- |
| 不存在，且从未受管 | 本次需要该文件 | 创建；按来源登记归属 |
| 已存在但无本安装的所有权记录 | 目标为受管文件 | 冲突；即使内容相同也不自动接管 |
| 已存在 | user-owned/protected | 保留；不取得新的受管所有权 |
| 当前状态等于安装基线 | 目标改变 | 安全更新 |
| 用户已修改或删除 | 目标仍等于安装基线 | 保留本地状态，doctor 显示差异 |
| 用户已修改或删除 | 目标也要改变 | 整体冲突，不写业务文件及安装记录 |
| 受管文件退役，当前等于基线 | 不再需要 | 删除文件并移除记录 |
| 受管文件退役，当前已不存在 | 不再需要 | 仅移除记录 |
| 受管文件退役，用户已修改 | 不再需要 | 整体冲突，保留文件与记录 |
| user-owned/protected 退役 | 任意 | 保留内容，移除不再适用的管理记录 |

`--dry-run` 不创建根、锁、事务、缓存或保存计划。执行时重新读取，不能把过去的预览作为覆盖许可。预检中发现冲突应在任何业务文件写入前返回全部已发现冲突。加锁后的漂移也停止提交。

不存在根的 init 可先在同文件系统的私有暂存根构建完整安装，再禁止覆盖发布。已存在空根和 attach/sync 使用事务模块；不得为了 init 删除并重建用户给出的目录。

## 8. 文件保护与事务恢复

### 8.1 文件保护

所有写路径，包括根、控制目录、暂存、备份和恢复路径，走相同保护入口。相对路径使用正斜杠，拒绝绝对路径、空组件、点组件、回退、Windows 设备名、尾随点/空格、控制字符、大小写/文件系统别名碰撞，以及文件与目录的父子冲突。保留 Unicode 路径，不自行采用有损转写。

使用已打开父目录/文件句柄和 no-follow/reparse-point 检查落实路径约束，不能只在开始时做字符串检查。提交前后核验实际对象身份和摘要。持有 CLI 锁只排斥其他 YSS 写入者，不宣称可以锁住编辑器、Git 或具有同等权限的恶意进程。

优先验证 cap-std/cap-fs-ext 是否能承接句柄访问和路径约束；其采用条件及缺口处理见 §11.2。第三方库不改变本节保护语义，Git 边界、安装所有权与身份复验仍由 YSS 负责。

Git 边界同时考虑嵌套 .git、Git index 的 gitlink 和 .gitmodules，覆盖未初始化子模块。明确选择已初始化子仓或 worktree 为根可以安装，不能从父根穿越进入。普通非 Git 目录不因缺少 Git 被拒绝；发现 Git 现场但不能可靠检查时拒绝写入。调用 Git 使用 argv 和 NUL 分隔输出，排除影响仓库定位的外部 GIT_* 环境变量，不执行 shell 拼接。

### 8.2 写入协议

事务是“逐文件受保护提交＋整体可恢复”，不承诺多个项目文件瞬间同时变更。安装记录作为最后一个业务操作提交，成功必须包含最终状态核验。

| 阶段 | 已持久化事实 | 允许的后续行为 |
| --- | --- | --- |
| preparing | 准备身份、目标根、操作范围；可能有不完整私有对象 | 不触碰业务文件；可核验后清理自己的准备现场 |
| prepared | 不可变计划、全部前后对象、对象摘要已落盘 | 可以进入 applying，也可放弃准备 |
| applying | 每次操作前先记录执行意图，完成后记录结果 | 继续应用；失败进入回滚；中断后供 recover 判断 |
| committed | 所有目标与安装记录核验成功，提交标记持久化 | 不自动恢复；按所有权规则清理私有临时对象 |
| rolling-back | 已记录回滚意图 | 逆序恢复；冲突则保留现场 |
| rolled-back | 全部原状态核验成功 | 可清理本事务材料 |

每个根同时最多一个未完成事务。未知状态、多个待恢复现场、日志截断无法定位、摘要不匹配或根身份不符时停止，不猜测执行顺序。

执行顺序：

1. 只读计算 ChangeSet，检查全部路径与冲突。
2. 检查控制路径后获取系统文件锁，重新检查业务基线、只读输入、Git 边界和已有待恢复事务。
3. 先发布准备身份，再创建私有对象；保存并同步原内容、新内容、计划及目录项。准备失败不修改业务文件。
4. 每次修改前持久化意图；使用完整且已刷盘的候选文件执行原生发布；修改后重读描述符并记录结果。
5. 文件操作完成后写入安装记录，核验整个结果，再持久化 committed。
6. 可恢复的普通错误或取消尝试逆序回滚；回滚失败保留日志并返回 recovery-required，不能只返回原始错误掩盖现场。

临时文件可能在任意字节位置被中断。准备身份及日志必须先声明其私有范围；不凭文件名或“不完整摘要”认领任意文件。执行阶段的候选对象完成性与发布身份要可验证。平台实现可以延续原子克隆/硬链接发布，或采用经过中断验证的其他算法；不能退化成直接覆写目标。新建目录的创建意图也登记；回滚只删除本事务创建且仍为空的目录。

### 8.3 恢复语义

`recover` 默认将未提交事务回滚至原状态，不继续完成旧输入。它先加锁并整体预检所有涉及文件、对象和原生中间状态，随后才执行逆序恢复。

目标等于 before 时无需修改；等于 after 时允许恢复 before；两者都不等时作为后续用户修改，整体停止。已确认的原生中间状态按单独记录处理，不能把任意缺失文件都解释为“事务造成”。恢复过程中再次漂移则停止并保留已执行进度，不声称全量回滚完成。

已 committed 的事务不自动撤销。损坏或未知事务只诊断，不提供 force。`recover --dry-run` 只读取。进程终止后系统锁自动释放，事务日志仍保留；二者不是同一个状态。

### 8.4 平台持久化范围

Unix 文件与必要目录项均同步。Windows 验证文件刷新、命名操作和进程中断恢复；在未证明目录项断电持久性前，不声称与 Unix 相同的掉电保证。只读属性或不支持的文件系统操作不能通过未登记 chmod/属性修改绕过，返回明确 unsupported 并保留现场。

## 9. Java 脚手架

平台目录、DDD/MVC 模板、Maven wrapper 和参考资源仍从内嵌 Bundle 读取。校验项目名、Java 包名、精确 Maven 坐标、Spring Boot/Java 匹配及必需输入。只生成业务空骨架，不自动构建、执行依赖下载或加入未请求示例。

使用有类型的生成上下文，作者、UTC 日期/时间只采集一次并贯穿同一生成任务；验证时允许固定上下文。文件内容不继续模拟 JS 对象枚举或 ICU 排序，清单采用新稳定顺序。当前模板主要使用 `{{key}}` 替换，本次保留小型严格替换器，不增加条件、循环或继承语法，也不引入 MiniJinja/Handlebars。

先渲染并校验全部输出，检查未替换占位符、禁止的样例凭据、路径与文件/目录冲突；然后在目标同文件系统的私有暂存目录写入并刷盘。最终通过禁止覆盖的原生目录发布，已有目标即使为空也不覆盖。空目录与可执行权限通过生成树显式表达，不依赖归档目录项。

取消、磁盘不足和发布冲突只清理能确认归属的暂存内容；原目标保持不变。不支持的文件系统能力明确失败，不 fallback 到会暴露半个工程的复制发布。

## 10. 程序升级、发行与 Windows 可执行文件

固定来源继续为 `https://gitlab.yss.test:9443/yss/yss-cli`，显式 CA 与令牌，不增加来源覆盖选项或 GitHub fallback。正式版本与精确 target 共同选择资产。GitLab 项目 ID 每次按完整身份核验取得，不硬编码。

网络客户端仅信任配置的 CA，保留主机校验；只向本次核验的同源、同项目资产 URL 发送凭据，重定向逐跳重新核验。错误输出不携带任意远程 URL 或令牌。在线 upgrade 使用受限 PAT；CI Job Token 仅用于既有受支持 CI 来源通道，不把未验证的在线 Job Token 模式列为支持。

在线 dry-run 读取元数据、SHA256SUMS 与 HEAD，不下载归档或写安装目录；离线 dry-run 读取本地归档完成校验。实际安装在确认安装根、版本及现有所有权后下载，边读边限制长度；校验完整归档、逐文件摘要、目标平台和载荷清单，再交事务模块。无正式 Release 返回 not-ready，不自动创建发行。

发行包只包含当前目标程序与 manifest；有明确需要的文档才纳入，并必须登记。tar.gz/ZIP 拒绝链接、设备、加密条目、路径冲突、未登记载荷、超限解压及非法尾随流。禁止降级，等版本且安装完整时报告 unchanged，不提供 force 重装覆盖用户文件。归档名称使用 Rust target triple，避免仅凭 OS/ARCH 混淆 GNU 与 musl。

首次 Rust 安装使用新工具目录，不通过 Go 在线升级器过渡。后续 Rust 格式内的 upgrade 默认更新实际运行程序所属、具有合法工具记录的目录；显式 --tool-root 可安装到新目录。工具根不能是项目根或 Git 仓库根。

运行中可执行文件替换单列为事务操作：

- Unix 使用同文件系统完整候选的原子替换，不写入运行中程序的原 inode。
- Windows 在提交前完整准备新程序；为旧运行程序安排事务内的已登记保留路径，先安全移走旧名字，再禁止覆盖发布新名字。两步之间的缺失状态必须有明确日志和备份，不能报告成跨平台原子替换。
- Windows 原进程仍运行时可保留旧二进制，下一次命令只在确认其归属且句柄允许后清理；不能把清理失败当作安装失败，也不能提前删除恢复材料。清理状态与安装提交状态分开表达。
- 若目标被其他句柄占用、原生步骤不支持或执行中漂移，停止并回滚/保留现场；不修改注册表、PATH 或另启动未经验证的脚本。
- 替换后校验磁盘程序和安装记录，再提交。此时原进程仍是旧版本，结果要区分当前执行版本与新安装版本。
- 在名字缺失或程序无法启动的极端中断点，可从可信新归档中取出的独立 yss 执行 `recover --root <工具目录>`。恢复始终使用未完成日志，不手工复制覆盖。

`self-replace` 等库可作为平台实现参考，不直接把其成功返回等同于 YSS 事务提交，也不引入绕开事务日志的自更新路径。具体使用安全平台封装还是可审计库留给实现者；上述可观察行为及所有失败中间态固定不变。

## 11. Rust 工具链、组件复用与体积

使用 Rust 2024 edition，固定一个经实现验证的 stable 工具链及 Cargo.lock。最小版本至少覆盖所用标准库文件锁 API，最终版本取依赖实际要求；不为了设计文档现在安装工具链或编造依赖锁。

### 11.1 已选择的复用方式

用户已确认将本轮调研建议补充到设计：复用适合当前需求的基础库，保留 YSS 的所有权规则、事务编排及来源约束。组件数量、代码减少比例和体积收益不预先承诺。

| 比较方式 | 取舍 | 本次结论 |
| --- | --- | --- |
| 标准库＋直接平台封装 | 依赖较少，但需自行维护更多句柄、平台和取消适配 | 标准库能力继续优先；作为候选库不满足要求时的窄范围实现 |
| 成熟基础库＋YSS 规则 | 减少重复适配，需锁定版本、控制 feature 并验证行为边界 | 采用此方向；cap-std 单独保留先验证再采用的条件 |
| 通用模板、更新或发行框架 | 能打包更多功能，但带入额外语法、流程或独立临时文件生命周期 | 不作为本次主路径；不改变既定模板、事务和固定 GitLab 来源 |

### 11.2 运行时组件及边界

| 能力 | 选择 | 使用边界 |
| --- | --- | --- |
| 参数与帮助 | clap derive | 使用可返回错误的解析入口，统一 JSON/帮助/退出码 |
| 模型与 JSON | serde / serde_json | 保留严格重复键、数字、未知字段及资源限制校验 |
| 输入错误定位 | serde_path_to_error | 复用 Serde 字段路径定位，转换为 YSS 错误模型；不直接输出可能含敏感值的原始错误 |
| 错误 | thiserror | 核心错误有稳定 code，避免只靠文本分类 |
| 摘要、版本 | sha2、semver | 不自行实现摘要或版本比较 |
| 归档 | tar、flate2 的 Rust 后端、仅必要 ZIP feature | 严格载荷、路径、大小、尾随流和摘要约束仍由 YSS 执行 |
| YAML | saphyr-parser，替换原 yaml-rust2 首选 | 使用事件及标量文本构建严格输入；不引入完整 YAML 对象层或停维的 serde_yaml |
| 网络 | reqwest＋rustls | Tokio 仅在网络等待、取消及流式下载处运行；固定来源和 CA 不由库默认值决定 |
| 取消与信号 | tokio-util::CancellationToken＋ctrlc | 同步流程检查取消标志，网络等待取消通知；信号回调只请求取消，事务显式回滚 |
| 句柄与路径访问 | cap-std＋cap-fs-ext，先验证 | 仅在 fs 模块内使用；不足处保留 rustix/windows-sys 窄封装，不建立两套可切换后端 |
| 文件锁 | 标准库 File 锁 API | 保留持久锁文件及作用域释放，dry-run 不创建锁 |
| 随机标识和时间 | 标准/已有依赖的公开 API 优先，必要时引入小型专用库 | 实际使用的 crate 显式声明，不依赖未声明传递依赖 |

saphyr-parser 提供 YAML 事件和标量文本，适合现有严格解析需求。yaml-rust2 维护者说明其保持基础维护，并建议只需要解析器的使用者考虑 saphyr-parser；新系列 API 稳定性较低，因此实施时核验并锁定版本，不照抄 README 示例版本。单文档、重复键、别名/锚点、标签、深度、节点数量及十进制精度仍由 YSS 校验，不因采用解析器删除这些规则。[维护者说明](https://github.com/Ethiraric/yaml-rust2/issues/26)、[Saphyr 项目](https://github.com/saphyr-rs/saphyr)

serde_path_to_error 只补充反序列化错误的字段路径，不提供重复键策略、输入大小限制或日志脱敏。它放在 codec 边界，与现有严格 JSON 检查组合使用。[字段定位说明](https://github.com/dtolnay/path-to-error)

cap-std 围绕已打开目录句柄操作文件，适合作为文件保护层候选；但 cap-fs-ext 的 `follow(No)` 约束路径最后一个组件，不能据此声称全部父目录都不跟随链接。实施时验证逐级目录访问、根及父目录身份、reparse point、路径别名、目录 no-replace 发布和平台刷新行为。Git 边界、安装归属与漂移检查仍属 YSS。不能满足全部语义的原语用窄范围平台实现补足；若整体集成增加复杂度或无法满足保护要求，则保留原 std/rustix/windows-sys 方案并记录依据，不降低保护要求。[cap-std](https://github.com/sunfishcode/cap-std)、[follow 具体语义](https://github.com/sunfishcode/cap-std/blob/main/cap-fs-ext/src/open_options_follow_ext.rs)

取消对象保持 YSS 的小接口，内部复用 CancellationToken；文件用例不因此改成异步。ctrlc 按实际信号范围配置，Unix 需要 SIGTERM/SIGHUP 时显式核验 termination feature。恢复使用独立上下文，不能从已取消的 token 派生一个同样已取消的 child_token 后立即放弃回滚。第三方取消通知不替代 WAL、提交或恢复。[CancellationToken](https://docs.rs/tokio-util/latest/tokio_util/sync/struct.CancellationToken.html)、[ctrlc](https://github.com/Detegr/rust-ctrlc)

YSS 不自行实现密码学、TLS、通用归档格式或完整 YAML parser。YSS 自有 unsafe 仅限平台模块，并记录操作系统前提；第三方库的内部实现仍需按实际版本评估。Rust 类型安全和库封装都不能代替文件系统竞态与事务验证。

### 11.3 仅用于开发与构建的工具

| 工具 | 用途 | 限制 |
| --- | --- | --- |
| [cargo-bloat](https://github.com/RazrFalcon/cargo-bloat) | 定位主要代码体积及依赖贡献 | 分析以代码段为主且归属有估计成分；不能替代完整程序、Bundle 和压缩包的字节测量 |
| [cargo-machete](https://github.com/bnjbvr/cargo-machete) | 找出疑似未使用依赖 | 可能误报；结合 cfg、生成代码和实际构建确认，不自动删除 |
| [cargo-deny](https://github.com/EmbarkStudios/cargo-deny) | 按需检查依赖来源、已知公告、重复版本和许可证配置 | 配置依据实际项目约束；不增加统一流程门禁或把工具通过等同于行为正确 |
| [cross](https://github.com/cross-rs/cross) | 在具备容器 runner 时简化 Linux musl 交叉构建 | 固定工具/镜像；模拟运行不代表原生验证，具体使用条件见 §12 |

这些工具不随 yss 发行，不是用户运行时依赖，也不要求现在安装。直接调用其 CLI，不引入包装脚本或持久测试套件；具体工具版本、feature 与检查范围在执行对应任务时确定。

### 11.4 本次不引入的替代路径

- **atomic-write-file/tempfile 直接承担事务**：单文件完整替换或临时材料管理不包含 YSS 的多文件 WAL、所有权、最后安装记录及恢复判断。不用其成功返回代替事务提交；若仅复用底层原语，仍须证明临时对象能被预先登记、核验及恢复。[atomic-write-file](https://github.com/andreacorbellini/rust-atomic-write-file)、[tempfile 持久化限制](https://docs.rs/tempfile/latest/tempfile/struct.NamedTempFile.html)
- **self-replace 接管整条升级路径**：其 Windows 实现会创建并启动用于清理的程序副本，临时文件有自身生命周期；本次仅参考原生行为，不把这些动作隐藏在 YSS 日志之外。[实现与限制](https://docs.rs/self-replace/latest/self_replace/)
- **cargo-dist 接管 GitLab CI**：调研时官方自动 CI 后端仅支持 GitHub；保留现有 GitLab CI 与内部包生产接口，不为接入框架改变来源或重建发行流程。这不表示该工具不能被定制调用，而是当前不值得增加这层集成。[CI 配置](https://axodotdev.github.io/cargo-dist/book/reference/config.html#ci)
- **MiniJinja/Handlebars 增加模板语言**：现有需求以简单占位符为主；保留严格替换器，条件、循环和继承留待真实模板需求出现后再决定。[当前渲染实现](../../internal/scaffold/platform.go)、[MiniJinja 能力](https://github.com/mitsuhiko/minijinja)

### 11.5 体积与验证

release 初始建议：`opt-level = "s"`、`lto = "thin"`、`codegen-units = 1`、`strip = "symbols"`；保留默认 panic unwind，不以 abort 代替恢复机制。实际比较 opt-level 3/s/z 后选取合适配置，不承诺 size 模式必然更小。

现有 2.91 MiB Bundle 下，8～20 MiB 可执行文件、5～12 MiB 压缩包仅是本次工程估算；15 MiB 是优化目标，不是验收硬阈值。报告每个实际 target 的最终体积、模板摘要与构建配置，不能用空模板数字替代全量发行大小。

组件引入前后的比较使用同一目标、工具链、Bundle 和构建配置。区分裸程序、代码段、嵌入数据与压缩包大小；尚无 Rust 实测，不填组件节省比例或新增 MiB。能力验证继续使用 §14 的临时隔离材料，覆盖 YAML 拒绝规则、字段定位及脱敏、路径保护、网络取消和回滚独立性。

## 12. 构建、平台与 CI

构建步骤由 Agent 或 CI 直接调用，不引入包装脚本：

1. 核验明确的 CLI 源码与模板工作树身份。
2. 运行 `cargo run --locked --bin yss-bundle -- --source-root <模板根> --out assets/bundle.tar.gz`。
3. 运行 `cargo build --locked --release --bin yss --target <target>`，编译时注入真实 CLI/模板来源。
4. 将最终程序与 release-manifest.json 打包，生成真实 SHA256SUMS。
5. 获得对应授权后才上传正式 Release；本设计不授权发行。

正常构建不自动 git clone、读取当前目录不明配置或请求在线发行。来源缺失记为 unknown 并在开发产物中展示，不能伪称 committed。生成 Bundle 的明确来源与体积报告保留在构建输出。

| 系统 | 设计目标 |
| --- | --- |
| macOS | aarch64-apple-darwin、x86_64-apple-darwin |
| Linux | x86_64-unknown-linux-musl、aarch64-unknown-linux-musl |
| Windows | x86_64-pc-windows-msvc、aarch64-pc-windows-msvc |

Linux 首选 musl，避免依赖某个较新的 glibc；这仍需验证 TLS 依赖、链接器、DNS 和目标环境。各系统最低版本在实现阶段按 Rust target、依赖及实际环境确定并写入发行说明；不从本机可运行推导全部版本支持。

目标矩阵是实现范围，不是当前支持声明。每个拟正式发行的 target 须实际验证关键文件操作、生成与升级；未验证目标如保留构建产物，必须明确是未验证产物，不进入支持列表。现有只有 Linux 的 CI 不足以证明 Windows/macOS 行为。

.gitlab-ci.yml 改为固定 Rust 工具链、Cargo 缓存、fmt/clippy/build 与必要实际 CLI 调用；保留固定 GitLab CA、Job Token 与来源身份核验。CI 的 YAML/shell 调度不属于用户发行运行时；删除 Go 生成/编译步骤，不能为了“纯 Rust”删掉来源校验。

有 Docker/Podman runner 时，可用 cross 简化两个 Linux musl target 的交叉编译，固定工具版本及实际镜像摘要；没有容器能力时使用经验证的原生/交叉工具链。cross 的模拟测试不替代本方案的原生文件锁、发布和恢复验证，也不替代 macOS SDK、Windows MSVC 或相应原生环境。[cross 构建与测试说明](https://github.com/cross-rs/cross)

## 13. 切换顺序与修改范围

先完成新 Rust 格式与文件操作的一个纵向闭环，再扩大覆盖；避免先批量翻译整个 Go 目录。文件系统基础任务中先验证 cap-std 的适配能力及 Windows 运行中程序的移动、禁止覆盖发布、句柄占用和中断现场，不把这些原语假设留到完整升级器完成后才检查。完整 Windows 升级及日志恢复仍在升级任务中接通；早期验证不是升级功能已完成。

| 顺序 | 可观察产出 |
| --- | --- |
| 1 | Cargo 工程、类型、CLI 输出和真实 Bundle 生成/读取；version/help/catalog 可用 |
| 2 | 文件保护、锁和事务：成功、失败、中断、恢复都可在临时根中观察 |
| 3 | init/attach/sync/skills/doctor/recover；四种 Profile×安装类型和切换 |
| 4 | DDD/MVC 完整生成与程序在线/离线升级，覆盖平台差异 |
| 5 | 构建分发、技能说明、文档和清单对齐，移除 Go 生产路径 |

开发期间可在隔离分支或 worktree 保留旧实现作为行为资料；不制作运行时双引擎。最终切换后移除 Go 源码、go.mod/go.sum、Go Bundle 生成器和 Go 构建入口；保留历史发布事实，不批量重写历史文档。

CLI 仓库修改范围为源代码、Cargo 配置、生成资产忽略规则、CI、AGENTS 和现役 README/docs。新增设计仅保存本文件，不改动已有收敛草案。

模板仓后续先验证两套现有分发清单，仅在与新生成器的具体约束不符时局部调整；需要配套更新 release-yss-cli 与 setup-yss-harness 的实际说明及受影响平台副本/清单。Java 模板内容与其他技能只在确有生成规则需要时调整。跨仓改动必须进入后续明确实施范围，不因本设计自动获得模板发布或其他会话改动授权。

首次使用采用新工具目录和新建项目/明确清理后的接入目录。旧项目、旧工具和旧事务保留，不自动清除。.yss 旧标记导致拒绝时给出明确位置，由用户另行决定旧实例处理；没有隐藏的“接管旧项目”路径。

## 14. 验证范围与交付判断

遵守当前仓库约定：不新增持久测试套件、检查脚本或批准包；必要行为验证使用临时隔离副本和断言，用后清理。实际执行记录可以作为简洁交付说明，不伪造已完成验证。编译与静态检查不能代替以下行为验证。

| 范围 | 必须观察的结果 |
| --- | --- |
| 命令与错误 | 必填参数、未知参数、JSON 错误、帮助、取消及退出码符合新设计 |
| Bundle | 相同明确输入可稳定重建；来源真实；缺失依赖/资源、环和模式串用被拒绝 |
| 安装 | 两个 Profile×两个范围、attach 冲突、ensure 闭包、范围切换正确 |
| 用户内容 | 修改、删除、未知同内容文件及退役文件严格按所有权表处理 |
| dry-run | 不创建根、锁、事务、归档缓存，不下载在线资产 |
| 路径与并发 | symlink/reparse、gitlink、嵌套仓、大小写碰撞、并发占用与输入漂移被拒绝 |
| 事务 | 在准备对象、记录意图、替换前后、安装记录及提交标记处注入中断；recover 不覆盖后续修改 |
| 脚手架 | DDD/MVC 文件、POM、包名、wrapper 权限和平台规则正确，目标被抢占时不覆盖 |
| 升级 | 固定来源、CA、错误重定向、摘要、错误平台、降级与受管程序修改被拒绝 |
| 平台 | 原生文件锁、发布、执行权限、Windows 运行中替换及恢复符合声明 |
| 分发 | 全量内嵌 Bundle；从干净环境运行不要求 Go/Bun/Node/Python |

中断验证覆盖进程强杀；断电持久性需要另外的环境和证据，不能等同。脚手架使用根 Maven Wrapper 进行代表性工程构建时，区分生成错误与私有依赖不可用；未实际构建不称 Java 工程构建通过。

允许实施者自行决定：内部文件拆分、纯函数分解、平台库的最终选取、信号对象具体实现、缓存细节、兼容目标的确切编译器版本、验证临时材料形式，以及不改变本方案行为的错误中文措辞。

需要用户重新决定的变化：减少现役功能或设计平台范围、接受覆盖用户文件、引入旧格式接管、增加运行时语言、改变固定发行来源，或把 Windows 原地升级降为必须人工换目录。不能以实现难度自行修改这些边界。

## 15. 资料、状态与下一步

本次依据包括当前 CLI 的 [命令说明](../cli-help.md)、[构建安装](../build-install.md)、[兼容说明](../compatibility.md)、[Bundle 实现](../../internal/bundle/bundle.go)、[项目逻辑](../../internal/project/project.go)、[事务](../../internal/transaction/transaction.go)、[升级器](../../internal/updater/updater.go)。这些是设计输入，尚未被更新为 Rust 使用文档。

官方技术参考（查阅于 2026-11-01）：

- [Rust File 文件锁与同步](https://doc.rust-lang.org/std/fs/struct.File.html)：优先评估标准库能力，仍需核验目标平台行为。
- [Rust 平台支持](https://doc.rust-lang.org/rustc/platform-support.html)：编译器 target 支持不等于本产品已验证。
- [Cargo release 配置](https://doc.rust-lang.org/cargo/reference/profiles.html)：体积与速度选项须实测。
- [clap 可返回错误的解析入口](https://docs.rs/clap/latest/clap/trait.Parser.html)。
- [reqwest 客户端配置](https://docs.rs/reqwest/latest/reqwest/struct.ClientBuilder.html)。
- [saphyr-parser 事件 API](https://docs.rs/saphyr-parser/latest/saphyr_parser/parser/enum.Event.html)：新的 YAML 解析首选；严格语义、资源限制和数值规则仍由 YSS 校验。
- [self-replace 平台行为](https://docs.rs/self-replace/latest/self_replace/)：仅作为运行中替换的实现参考。

前次已验证 Bun 的临时样例，不是本方案的实现证据。本机尚无已安装 Rust 工具链；没有 Rust 构建、跨平台事务结果或正式包体实测。

2026-11-01 已完成原设计的独立可实施性审阅，结论为没有必须补问才能规划的重大缺口。审阅指出的模板字段现状已修正；审阅不等于 Rust 编译、事务安全或原生平台行为已经验证。

同日按用户确认补充 §11 的组件/工具选型、§12 的 cross 使用条件及 §13 的早期平台验证顺序，并同步实施计划。补充依据为本次已读取的官方文档、GitHub 源码和维护者说明；未新装依赖或运行验证，不将原设计审阅扩称为对新增组件的独立验证。

用户已于 2026-11-01 批准本设计并要求进入下一步，现进入 writing-plans，实施安排见[纯 Rust 重写实施计划](./2026-11-01-rust-cli-implementation.md)。设计批准不代表获得提交、安装到用户环境、推送或发布授权。两份文档保存在 docs/plans，因为本仓把 docs/superpowers 划为历史区；不自动提交。
