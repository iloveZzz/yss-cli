# YSS CLI 纯 Rust 重写 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: 按用户选定方式使用 `superpowers:executing-plans` 或 `superpowers:subagent-driven-development`，逐任务实施；步骤用 `- [ ]` 跟踪。用户已选择本会话执行；按任务实施，不委派实现，不提交或发布。

**Goal:** 用纯 Rust 替换 YSS CLI 和 Bundle 生成器的 Go 实现，完成全部现役能力、文件保护、分发与使用资料的切换。

**Architecture:** 一个 Cargo package、一个 library、两个 binary：发行 `yss`，内部使用 `yss-bundle`。项目与升级先生成内存改动集，再交共享事务模块；全新项目和脚手架采用同文件系统暂存目录发布。CLI 负责参数和输出，业务模块不打印或退出进程。

**Tech Stack:** Rust 2024、Cargo；clap、Serde、serde_json、serde_path_to_error、thiserror、sha2、semver、tar/flate2、ZIP、saphyr-parser、reqwest/rustls；tokio-util::CancellationToken＋ctrlc。Tokio 运行时限于网络边界；cap-std/cap-fs-ext 先验证适配能力，必要原语用 std/rustix/windows-sys。工具链、实际引入的依赖版本及 feature 随对应任务核验并锁定。

**Spec:** [已批准设计](./2026-11-01-rust-cli-design.md)。用户已批准包含组件/工具补充的完整设计，并明确要求构建本实施计划；以下是该版本的执行安排。实现已在隔离工作树 `codex/rust-cli` 开始，逐项记录实际结果。

## Global Constraints

- 本计划主要操作仓库 `/Users/zhudaoming/Projects/yss-cli`，以下未注明仓库的文件路径均相对此根。设计依据 HEAD 为 `8c5e1a90ff83ac83a0640a64b8c8109c3fc5a5e6`；执行开始重新检查实际 HEAD、工作树与并行工作，不覆盖已有修改。
- 模板仓库 `/Users/zhudaoming/Projects/yss-spec-project-template` 只修改本计划明确列出的相关技能、投影及确有证据需要调整的分发字段；Java 资产保留权威来源。另一个 `2026-11-01-cli-template-convergence.md` 草案不随本计划执行。
- 全量保留 init、attach、sync、skills list/ensure、scaffold catalog/ddd/mvc、upgrade、doctor、recover、version/help 的用途及现役参数名称；不兼容旧状态、事务、发行包或 JSON 输出。旧身份只检测并拒绝，不迁移、不接管。
- 发行程序与 Bundle 生成器不调用 Go、Bun、Node、Python。系统 API、Git、链接器、CI 内联构建胶水及所生成的 Java 工程不受此限制。只发行 `yss`。
- 新格式身份及版本：`yss.project`、`yss.tool`、`yss.bundle`、`yss.release`、`yss.transaction` 均为 `formatVersion: 1`；输出为 `yss.output`、版本 1。
- 压缩归档及累计解压各不超过 `256 MiB`；发行 manifest 不超过 `8 MiB`；YAML 深度上限 `512`。具体类型采用明确的更小限制或上述硬上限，不存在无界读取路径。
- `--dry-run` 不创建根、锁、事务、缓存或计划。在线升级预览只读元数据、SHA256SUMS 和 HEAD；离线预览读取并校验现有归档。
- 不覆盖未知归属或用户修改，不穿越子模块，不提供 force/保存计划接口，不建立插件、服务、多 crate workspace 或通用流程框架。
- 固定在线来源 `https://gitlab.yss.test:9443/yss/yss-cli`；显式受信 CA、受限 PAT、逐跳核验来源。不增加 GitHub 回退、动态来源配置或在线 Job Token 支持承诺。
- 六个设计目标：`aarch64-apple-darwin`、`x86_64-apple-darwin`、`x86_64-unknown-linux-musl`、`aarch64-unknown-linux-musl`、`x86_64-pc-windows-msvc`、`aarch64-pc-windows-msvc`。交叉编译与原生运行分别记录，不擅自减少范围。
- release 配置起点：`opt-level = "s"`、`lto = "thin"`、`codegen-units = 1`、`strip = "symbols"`，保留默认 unwind。含 Bundle 的程序以 `15 MiB` 为初始优化目标，不是已有测量或硬性功能裁剪理由。
- 遵守仓库“不维护或分发脚本与测试套件”：必要断言放临时隔离副本，验证后清理。通用技能中的持久 TDD 文件与逐任务 commit 步骤不适用；提交、安装到用户环境、推送、tag、发行仍需对应真实授权。
- 组件遵守 Spec §11：复用基础能力，YSS 保留严格输入、所有权、WAL 与恢复判断；不把通用原子写入、自更新或发行框架直接替代这些规则。cap-std 不满足要求时回到窄范围平台实现，不增加双后端，不减弱保护。
- cargo-bloat、cargo-machete、cargo-deny 只用于相关开发检查，cross 仅在满足容器构建条件时使用；均不随 yss 分发，不新增包装脚本、持久测试套件或统一流程门禁。
- 执行环境已安装并核验 Rust/Cargo 1.99.1（aarch64-apple-darwin）。包体与其他平台结果随对应任务记录，不推定通过。

## Review Focus

1. 预检后父目录被替换为链接、大小写/Unicode 别名或 Git 子模块变化：停止写入，不穿越新边界。由任务 3、5 验证。
2. 在准备对象写到一半、WAL 意图落盘后或安装记录替换期间强杀：恢复有可证明的归属及前后状态，不清理同名用户文件。由任务 5、6 验证。
3. 用户删除受管文件、未知文件恰好同内容、project/skills 切换及显式技能退役：保留正确基线，不偷偷接管或自动安装 replacements。由任务 7 验证。
4. 在线重定向、伪造长度、超大解压、重复键/路径、尾随流：拒绝非法输入，凭据不外发，输出不泄密。由任务 2、9、10 验证。
5. Windows 正在运行的程序被移至备份后、新程序尚未发布即终止，或旧句柄阻止删除：恢复遵循日志，不把清理失败当成已提交安装失败。任务 3 提前验证原语，任务 11 验证完整事务。

## 文件与责任

| 文件或目录 | 责任 |
| --- | --- |
| `Cargo.toml`、`Cargo.lock`、`rust-toolchain.toml`、`build.rs`、`.gitignore` | 依赖、工具链、release 配置、真实编译来源与生成物忽略 |
| `src/lib.rs`、`src/bin/yss.rs`、`src/bin/yss-bundle.rs` | library 出口、用户程序、内部 Bundle/发行包生产入口 |
| `src/model.rs`、`src/error.rs`、`src/cancel.rs` | 共享有类型模型、可机读错误、取消通知 |
| `src/cli.rs`、`src/output.rs` | 参数解析、用例调度、统一输出和退出码 |
| `src/codec.rs` | 有界严格 JSON/YAML 与确定性编码 |
| `src/fs/mod.rs`、`src/fs/git.rs`、`src/fs/publish.rs`、`src/fs/platform/{unix,windows}.rs` | 路径/句柄保护、Git 边界、文件锁、原生文件/目录发布 |
| `src/bundle/{mod,build,manifest,selection}.rs` | Bundle 读写、来源快照、内容对象、安装视图与技能闭包 |
| `src/transaction/{mod,journal,apply,recover}.rs` | ChangeSet、操作枚举、事务对象、WAL、提交、回滚和恢复 |
| `src/project/{mod,state,plan}.rs` | 安装身份、所有权决策、技能选择与诊断 |
| `src/scaffold/{mod,context,ddd,mvc}.rs` | 平台与输入、生成上下文、两种 Java 工程渲染 |
| `src/updater/{mod,release,installation}.rs`、`src/network.rs` | 包格式/打包、工具收据、安装/升级、固定 GitLab |
| `assets/bundle.tar.gz` | 忽略的构建产物；仅 `yss` binary 通过 `include_bytes!` 引入 |
| `.gitlab-ci.yml`、`AGENTS.md`、`README.md`、现役 `docs/*.md` | CI、实际能力、构建与安装说明 |
| 模板两套分发清单及 release/setup 技能 | 保持原内容源，更新 Rust 操作说明并同步真实平台副本 |

不预建空模块。上表在相应交付物开始时创建文件；可按实际代码规模合并私有辅助文件，但保持下列模块公开接口。Go 文件只作行为参考，不逐行翻译。

## 公共接口与临时验证约定

- `model.rs` 定义 `Profile::{Spec,Backend}`、`InstallationKind::{Project,Skills}`、五种 `Ownership`、`RelPath`、`Sha256`、`FileDescriptor`、`BuildInfo`、`SourceProvenance`、`RenderedTree`。`FileDescriptor` 含原始字节 SHA-256、长度与可观察模式；`RenderedTree` 显式保存稳定排序的文件及空目录。
- `fs` 定义 `FileIdentity`、`RootIdentity`、`FileSnapshot`、`CheckedFile`、`RootLock`；快照区分不存在、文件、目录及危险对象，业务描述符与原生身份同时参与重检。CheckedFile 含同一次受保护读取的 bytes 与 snapshot；身份不能仅用路径或时间戳代替。
- `transaction::{ChangeSet, Operation, TransactionPhase, TransactionReport, TransactionStatus}` 在任务 5 定义。ChangeSet 含有序 `Operation::{Create,Replace,Delete,CreateDir}`、fs 快照表达的预期状态、目标字节/模式、只读输入约束及最后的安装记录操作。project/updater 从 transaction 导入，不让 model 反向依赖 fs/transaction。事务线上计划只持对象引用，不把整个文件重复嵌入 JSON。
- `error.rs` 提供 `type Result<T> = std::result::Result<T, YssError>`。`ErrorCode` 至少覆盖 `ARGUMENT`、`UNSUPPORTED_FORMAT`、`INVALID_INPUT`、`CONFLICT`、`UNSAFE_PATH`、`LOCKED`、`CONCURRENT_CHANGE`、`RECOVERY_REQUIRED`、`CANCELLED`、`NETWORK`、`NOT_READY`、`DIGEST_MISMATCH`、`UNSUPPORTED_PLATFORM`、`VERSION_CONFLICT`、`IO`；字段固定为 code/message/path/context，context 只放可公开的有类型信息。
- `cancel::Cancellation` 可 Clone，提供 `new() -> Cancellation`、`cancel(&self)`、`check(&self) -> Result<()>`、`is_cancelled(&self) -> bool`、`async fn cancelled(&self)`。内部复用 tokio-util::CancellationToken；同步文件流程只查询状态，网络等待 cancelled。用户 binary 持有进程级 token，并通过 `cancel::install_signal_handler(cancel: Cancellation) -> Result<()>` 只注册一次 handler；library 的 cli::run 接收 token，不在每次调用时注册全局 handler。取消触发正常错误路径；回滚使用独立恢复上下文，不能从已取消 token 派生 child_token 后立即放弃恢复。
- 所有 `*Request`、`*Plan`、`*Report` 由所属模块定义为有类型结构，不用动态 JSON 跨模块传递业务对象。ProjectRequest 是 init/attach/sync/ensure 的枚举，ProjectPlan 包含根路径及无变更、ChangeSet 或 RenderedTree；UpgradeRequest 区分在线/离线并携带 dry_run，UpgradePlan 的预览分支没有可执行写操作；ScaffoldRequest 区分 DDD/MVC。报告可以增加诊断，但不得泄露凭据。
- 执行时建立临时 `YSS_PROBE_ROOT`，复制**实际工作树**及当前未提交 Rust 文件，排除 `.git`、`target`、生成发行物；不用 `git archive HEAD` 遗漏正在实现的文件。用临时 `tests/<任务名>.rs` 保存断言；需私有断点时只修改这个副本，不给产品加入故障注入开关。
- 通用验证命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test <用例名>` 只针对临时副本。Windows PowerShell 使用 `$env:YSS_PROBE_ROOT` 或实际绝对路径，测试必须在所声明的原生环境执行。文件中下列 `assert` 是该临时用例应实现的精确判定；测试辅助构造函数不是产品 API。
- 从任务 4 起，临时副本先生成真实 Bundle 才构建用户 binary。每次只运行本任务必要验证；`cargo fmt --all -- --check`、`cargo clippy --locked --lib --bin yss-bundle -- -D warnings` 在代码可构建后使用，最终再覆盖两个 binary。
- 临时材料不得进入版本库、Bundle 或发行物。保留实际命令、结果和未验证项的简短交付记录即可，不增加持久验证框架或报告模板。

## 任务 1：建立可独立构建的 Rust 核心与输出入口

**Consumes / 前置依赖:** 无前置 Rust 模块；读取 Spec、现役 cmd/yss/main.go 与 internal/cli/cli.go 的参数和行为。新增组件按本任务实际需要声明依赖。

**Files:** 创建 `Cargo.toml`、`Cargo.lock`、`rust-toolchain.toml`、`build.rs`、`src/lib.rs`、`src/{model,error,cancel,cli,output}.rs`、`src/bin/yss-bundle.rs`；修改 `.gitignore`。参考 `cmd/yss/main.go`、`internal/cli/cli.go`。
**Interfaces:** 产出上述共享模型、取消接口；`cli::run(args: Vec<OsString>, bundle_bytes: &[u8], build: &BuildInfo, cancel: &Cancellation, out: &mut dyn Write, err: &mut dyn Write) -> i32`；`output::write<T: Serialize>(out: &mut dyn Write, envelope: &OutputEnvelope<T>) -> Result<()>`。
**交付物:** library 与内部 binary 可编译，参数/输出协议有断言；尚未接入的命令不返回伪成功。
**临时验证入口:** `tests/cli_output.rs`，命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test cli_output`。预期退出 0，且：退出码和 JSON envelope 断言通过，连续调用的取消状态隔离；library/内部 binary 可在资产不存在时构建。

- [x] 核对两仓实际状态、AGENTS 和现役参数，记录此轮操作文件；保留用户草案及并行修改。
- [x] 执行阶段安装并核验一个实际可用 stable 工具链，锁定精确版本与依赖；设置 Rust 2024 和 release 配置。记录 `rustc -Vv`、`cargo -V`；不凭文档版本猜已安装工具链或最低 OS。
- [x] 创建任务 1 所属的 model/error/output 类型及 Cancellation 薄封装；实现 `cancel::install_signal_handler` 供用户 binary 调用，cli::run 仅使用注入 token，保证临时用例多次调用不会重装全局 handler。同步文件流程无需启动 Tokio 运行时；core 不打印、不调用 process exit。
- [x] 用 clap 的可返回错误入口完成全命令参数模型及输出 envelope；先识别 `--json` 意图，help/version 不加载 Bundle。`status` 使用 `ok|error`，成功 error 为 null、失败 result 为 null；成功 0、参数 2、其余失败 1。
- [x] 实现 `build.rs` 的来源采集：Cargo 版本/target、真实 CLI HEAD、dirty 与源内容快照；显式排除生成物和临时资料。来源不可取得时标为 unknown 开发产物，不伪称 committed。Bundle 存在时生成其编译摘要，缺失时允许内部工具构建；不联网、不生成假 Bundle。
- [x] 在临时 `tests/cli_output.rs` 验证 `assert_eq!(bad_arg_exit, 2)`、`assert_eq!(cancel_exit, 1)`、`assert_eq!(parse_json(stdout)["format"], "yss.output")`；错误参数带 `--json` 时 stdout 恰好一个 JSON 值，help/version 用空输入 Bundle 仍成功，token 不出现在任一输出中；不同 Cancellation 的两次 run 调用互不继承取消状态。
- [x] 运行临时 `cli_output` 用例、`cargo check --locked --lib --bin yss-bundle` 与格式检查；确认缺少资产时没有可发行的空壳 `yss`。

## 任务 2：严格编解码与新格式身份

**Consumes / 前置依赖:** 任务 1 的 `error::Result<T>`、YssError 与 Serde 基础依赖；通用解析器不依赖项目、升级或文件事务模块。

**Files:** 创建 `src/codec.rs`；扩展 `src/model.rs`、`src/error.rs`。参考 `internal/schema/{parse,yaml}.go`、`internal/identitymeta/native.go`。
**Interfaces:** `codec::decode_json<T: DeserializeOwned>(bytes: &[u8], limits: Limits) -> Result<T>`；`decode_yaml(bytes: &[u8], limits: Limits) -> Result<StrictValue>`；`encode_json<T: Serialize>(value: &T) -> Result<Vec<u8>>`。`Limits` 明确字节、深度、节点及数值 token 长度，`StrictValue` 保留十进制文本语义。
**交付物:** 所有后续状态、清单、网络输入都通过同一严格入口。
**临时验证入口:** `tests/codec_inputs.rs`，命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test codec_inputs`。预期退出 0，且：合法资产读取成功；列出的非法输入全部拒绝；数值、字段路径及脱敏断言成立。

- [x] 完成 JSON 重复键检测 Visitor 和有界读取；固定结构 `deny_unknown_fields`，拒绝非法 UTF-8/Unicode、尾随值及已知整数溢出。使用保留十进制表示的解析路径（serde_json 开启 arbitrary_precision），不能先解析成覆盖重复键的普通 map。结构化状态/manifest 上限 8 MiB，通用资产不超过 256 MiB；JSON 深度 128、单文档节点 1,000,000、单个数值 token 4,096 字节，均在分配或递归前检查。
- [x] 用 saphyr-parser 事件与标量文本落实单文档/字符串键及设计中的拒绝规则；锁定实际验证版本，不照抄 README 示例版本或保留第二套 YAML parser。限制深度 512，节点及数值 token 限制同上，数字不经 f64 丢精度；YSS 仍负责重复键、别名/锚点、标签和资源规则，映射/数组在每次增长前检查，遇到拒绝规则立即停止后续处理。
- [x] 完成固定身份/version 校验与确定性编码：映射稳定排序、UTF-8、一个末尾换行，摘要针对最终字节。可扩展 vars/context 明确建模，不放宽外层结构。
- [x] 在 codec 的类型反序列化入口接入 serde_path_to_error，提取字段路径并映射成 YssError；保留严格输入检查，不直接输出含输入值的底层错误。
- [x] 在临时 `tests/codec_inputs.rs` 断言重复键、重复文档、别名/锚点/merge、自定义标签、非有限数、513 层 YAML 和溢出整数全部失败；`assert_eq!(round_trip_decimal("9007199254740993.25"), "9007199254740993.25")`；同一值重复编码字节相同，额外尾随 JSON 失败。
- [x] 在 `codec_inputs` 增加字段定位断言：嵌套 version 类型错误时 `assert_eq!(error_field_path, "dependencies.serde.version")`；恶意输入包含秘密值时 `assert!(!public_error.contains(secret))`。运行该临时用例，并用合法 Spec/Backend 清单验证现有字段可读取；源数据异常定位源路径，不添加宽松回退。

## 任务 3：文件系统保护、Git 边界与全新目录发布

**Consumes / 前置依赖:** 任务 1 的 model::{RelPath, FileDescriptor, RenderedTree}、Cancellation 与 Result；任务 2 的严格编解码用于需要读取的控制状态。第三方 cap 句柄只在 fs 内流动。

**Files:** 创建 `src/fs/mod.rs`、`src/fs/git.rs`、`src/fs/publish.rs`、`src/fs/platform/mod.rs`、`src/fs/platform/unix.rs`、`src/fs/platform/windows.rs`。参考 `internal/safefs/*`、`internal/scaffold/gitscope.go` 和 `publish_*.go`。
**Interfaces:** `fs::inspect(root: &Path, path: &RelPath) -> Result<FileSnapshot>`；`fs::read_checked(root: &Path, path: &RelPath, limit: u64) -> Result<CheckedFile>`；`fs::lock_root(root: &Path) -> Result<RootLock>`；`fs::publish_new_tree(target: &Path, tree: &RenderedTree, cancel: &Cancellation) -> Result<PublishReport>`。调用者负责在执行阶段调用 lock/publish；read-only 检查不得隐式创建路径。
**交付物:** 上层统一使用受保护读写原语；新目录只能完整、禁止覆盖地发布。
**临时验证入口:** `tests/fs_boundaries.rs`，命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test fs_boundaries`。预期退出 0，且：外部目录与已存在目标不变，非法边界被拒绝，锁竞争符合预期。Windows 另运行 platform_publish_probe，结果单独注明原生环境。

- [x] 实现 `RelPath` 与整棵树的路径验证，覆盖设计 `8.1` 全部拒绝项；检测文件/目录冲突、实际文件系统别名与大小写碰撞，保留 Unicode。
- [x] 在临时副本先验证 cap-std/cap-fs-ext 的目录句柄、逐级 no-follow、reparse/身份读取及平台能力；明确 follow(No) 只约束最后组件。仅适配满足设计的原语，缺口由 std/rustix/windows-sys 窄封装补足；整体不合适时保留原平台方案并记录证据，不维护双后端。
- [x] 实现上述验证选定的句柄访问、no-follow/reparse 检查、原生身份快照与操作前后校验；YSS 自有 unsafe 只留在 platform。read_checked 在同一受保护句柄读取限长字节并核验读取前后身份/状态，返回 bytes 与对应 snapshot。创建 .yss、对象、临时文件、备份与恢复都走同一保护能力；Git 边界和安装归属不委托 cap-std 判断。
- [x] 实现 Git 探测：同时检查嵌套 .git、index 160000、.gitmodules；argv/NUL 调用，清理影响定位的 GIT_*。普通非 Git 根允许，检测到 Git 却不能可靠判断时失败。
- [x] 优先使用 std::fs::File 锁 API 实现持续存在的 `.yss/lock` 系统锁与事务无关的锁释放；锁定前先确认控制目录归属。锁文件不删除解锁，获取不到锁返回 `LOCKED`，不额外引入重复锁库。
- [x] 实现同文件系统私有 staging、显式目录/模式、完整写入和刷新、原生 no-replace 目录发布；不支持时返回明确错误，不复制回退。只清理身份可验证的私有材料。
- [x] 在临时 `tests/fs_boundaries.rs` 验证非法路径、链接父目录替换、大小写/Unicode 别名、未初始化子模块、显式 worktree 根、无 Git 普通目录及探测失败；`assert_eq!(outside_tree_before, outside_tree_after)`。两进程竞争只能一个获锁；目录目标即使为空也有 `assert!(publish.is_err())`，原目录身份不变。
- [ ] 在原生 Windows 临时 `tests/platform_publish_probe.rs` 前移验证运行中 exe 移动、目标占用、禁止覆盖发布、只读属性，以及旧已移动/新尚未发布时的强杀现场；断言占用/冲突不覆写既有内容，已移动旧 exe 可核验。此处仅验证原语，不声称完整事务恢复完成。
- [ ] 运行可用平台的 `fs_boundaries` 和上述 Windows 原语验证；登记不可用环境及需跨平台重复的用例，不把 cfg 编译或 macOS 结果当成其他平台验证。可继续无关工作，但未经验证的原语不作为已证明的安全前提。


## 任务 4：Bundle 生成、严格加载与技能视图

**Consumes / 前置依赖:** 任务 2 的 `codec::decode_json<T>(bytes: &[u8], limits: Limits) -> Result<T>`、encode_json；任务 3 的 `fs::read_checked(root: &Path, path: &RelPath, limit: u64) -> Result<CheckedFile>` 及已实现的受保护 Git 探测；用户 binary 使用任务 1 的 cli::run 与信号安装接口。

**Files:** 创建 `src/bundle/{mod,build,manifest,selection}.rs`、`src/bin/yss.rs`、`src/scaffold/mod.rs`；修改 `src/bin/yss-bundle.rs`、`src/cli.rs`、`build.rs`。参考 `tools/bundle/main.go`、`internal/bundle/*`、`internal/scaffold/catalog.go`；读取模板两套 `.template-source/distribution/bundle-profile.json`。
**Interfaces:** `bundle::build(source_root: &Path, out: &Path) -> Result<BundleBuildReport>`；`bundle::load(bytes: &[u8]) -> Result<Bundle>`；`Bundle::select(profile: Profile, installation: InstallationKind, explicit: &BTreeSet<String>) -> Result<Selection>`；`scaffold::catalog(bundle: &Bundle) -> Result<CatalogReport>`。Selection 含默认、显式、闭包及各自资源来源，不混淆三者。
**交付物:** 内部工具独立构建，用户 binary 内嵌真实完整 Bundle。
**临时验证入口:** `tests/bundle_inputs.rs`，命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test bundle_inputs`。预期退出 0，且：同输入字节确定、依赖隔离及非法源拒绝；真实 Bundle 可构建 yss，缺失资产的用户 binary 构建失败。

- [x] 直接消费现有 schemaVersion 6 清单中两个安装类型的 skillDependencies、skillReferences、retiredSkills；对全部可选技能校验缺失、自环、环路及资源路径，禁止 Markdown 推断依赖或自动采用 replacements。
- [x] 实现来源读取与受限技能投影解析；采集 HEAD、dirty、允许跟踪/未忽略文件及快照，构建前后重检。相同内容不因时间、临时路径或遍历顺序改变快照。
- [x] 实现 `yss.bundle` manifest、原始 `objects/<sha256>` 去重、确定性 tar.gz 元数据与归档长度限制；Bundle 摘要写生成报告及编译常量，不写入自身。
- [x] 实现加载器逐条校验登记路径、对象摘要、模式、引用完整性、无额外非法条目；拒绝链接、设备、重复、越界、超限及尾随数据。loader 不依赖编译内嵌入口。
- [x] 接入 `cargo run --locked --bin yss-bundle -- --source-root <模板绝对路径> --out assets/bundle.tar.gz`；只有 `src/bin/yss.rs` include_bytes，并创建/注册进程 token 后传入任务 1 的 cli::run。启动错误也经统一输出处理。删掉资产后内部工具仍可构建，用户 binary 编译必须失败。
- [x] 实现 `scaffold::catalog` 的有类型只读资产查询并接入 CLI，列出真实平台配方及必填参数；此处不生成 Java 文件，任务 8 复用同一平台输入。
- [x] 在临时 `tests/bundle_inputs.rs` 断言同一输入两次生成 `assert_eq!(archive_a, archive_b)`；源文件改变必改快照，构建途中修改输入失败；重复内容仅一个对象；任意非默认技能依赖环也失败；project/skills 的依赖资源隔离。
- [x] 运行 `bundle_inputs` 并用实际模板生成一次 Bundle、`cargo build --locked --bin yss`；运行 version/help/scaffold catalog，核对其来源与生成报告一致、目录内容来自真实 Bundle，未接入业务不宣称已完成。

## 任务 5：持久事务准备与受保护提交

**Consumes / 前置依赖:** 任务 2 的 decode_json/encode_json；任务 3 的 `fs::inspect(root: &Path, path: &RelPath) -> Result<FileSnapshot>`、read_checked、`fs::lock_root(root: &Path) -> Result<RootLock>` 及已验证的内部文件发布原语；Cancellation 来自任务 1。

**Files:** 创建 `src/transaction/{mod,journal,apply}.rs`；按需补充 `src/model.rs` 中不依赖 I/O 的描述符及 fs 的内部原生发布能力。参考 `internal/transaction/{transaction,preparation,material,temporary_write,directories}.go`。
**Interfaces:** 定义并导出 `transaction::{ChangeSet, Operation, TransactionPhase, TransactionReport, TransactionStatus}`；`transaction::apply(root: &Path, changes: ChangeSet, cancel: &Cancellation) -> Result<TransactionReport>`；`transaction::status(root: &Path) -> Result<TransactionStatus>`。root 为已存在目录；新根采用任务 3 的整树发布。TransactionPhase 精确采用 preparing/prepared/applying/committed/rolling-back/rolled-back 六阶段。
**交付物:** 事务准备、日志、文件变更、最后安装记录与提交可单独观察和校验；恢复由下一任务完成后才向业务开放写入。
**临时验证入口:** `tests/transaction_apply.rs`，命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test transaction_apply`。预期退出 0，且：成功事务 after 状态可核验，准备前业务树不变，失败/强杀现场保留可辨认材料。

- [x] 定义 `yss.transaction` 的准备身份、不可变计划、原始计划摘要、before/after 对象与追加日志；准备身份先于私有对象。私有范围、根身份、候选文件及创建目录身份必须可证明；atomic-write-file/tempfile 的单文件提交或 Drop 清理不替代本协议。
- [x] 实现只读全量预检后获取锁，再重检基线、只读输入、Git 边界和 pending 状态；不接受未知、多个或损坏的未完成事务。
- [x] 实现准备对象完整写入、摘要复验及文件/必要目录同步；业务文件在 prepared 前完全不变，截断准备对象不等于合法 after。
- [x] 实现每次变更的 durable intent → 完整候选原生发布 → 身份/摘要复验 → 完成日志；Create/Replace/Delete/CreateDir 各有明确前置状态，不直接 truncate 目标。
- [x] 实现安装记录最后提交、全结果核验后 durable committed；本任务只在临时根验证，尚无回滚时失败必须保留现场并返回 RECOVERY_REQUIRED，不伪称已回滚。任务 6 接通显式回滚后，才允许任务 7/9 暴露完整写命令。
- [ ] 在临时 `tests/transaction_apply.rs` 验证两个写者、加锁前后漂移、准备磁盘不足、候选截断、安装记录写失败；`assert_eq!(business_before_prepared, original_tree)`；成功时所有实际描述符等于 after，安装记录不先于其他业务操作。
- [x] 用临时副本插入确定性暂停点，分别在准备身份发布后、私有对象写入中、意图后、目标发布后、安装记录后、committed 标记持久化前后强杀；保留这些临时现场供任务 6 验证。产品中不保留注入开关。

## 任务 6：回滚、恢复与取消

**Consumes / 前置依赖:** 任务 5 的 `transaction::status(root: &Path) -> Result<TransactionStatus>`、事务类型及 journal 内部格式；任务 3 的锁、快照、受保护读取和发布能力。恢复与 apply 共用同一套日志和对象，不再定义另一个事务协议。

**Files:** 创建 `src/transaction/recover.rs`；修改 `src/transaction/{mod,journal,apply}.rs`、`src/cancel.rs`。参考 `internal/transaction/{pending,recovery_kind,replacement_*}.go`。
**Interfaces:** `transaction::recover(root: &Path, dry_run: bool, cancel: &Cancellation) -> Result<TransactionReport>`；apply 内部调用同一回滚算法。TransactionReport 区分 committed、rolled-back、recovery-required 与清理状态。
**交付物:** 崩溃后可通过独立进程恢复；失败不会掩盖事务现场。
**临时验证入口:** `tests/transaction_recover.rs`，命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test transaction_recover`。预期退出 0，且：before/after 判断和完整回滚成立，漂移不覆盖，已提交不撤销，取消后的独立回滚可执行。

- [x] 实现 recover 的加锁、整体预检与原始计划/对象校验：当前 before 无操作，当前 after 逆序恢复，其他状态在任何恢复写入前整体拒绝。
- [x] 实现部分回滚进度与重入；恢复期间新漂移则停止。已 committed 不撤销；损坏/未知/多事务/根身份不符只诊断，不 force。
- [x] 实现 preparing 私有对象清理与目录清理，只凭已持久化身份及范围认领；创建目录仅在仍属本事务且为空时删除。名字像临时文件但无归属证据时保留。
- [x] 完成普通错误/取消显式回滚，组合报告原始错误与回滚错误；ctrlc 回调只调用 Cancellation::cancel，不在 handler 或 Drop 中执行事务。核验 Unix termination feature 所需信号与 Windows 控制事件；回滚不复用已取消 token 或其 child_token。
- [x] 在临时 `tests/transaction_recover.rs` 对任务 5 的强杀现场运行独立 recover；`assert_eq!(recovered_tree, original_tree)`。另验证用户修改一处导致全量预检零写入、恢复中再次修改保留进度、截断日志拒绝、committed 保持 after、同名非私有文件不删除。
- [x] 增加取消断言：请求 token 已取消时回滚仍能完成，`assert_eq!(rolled_back_tree, original_tree)`；验证 `recover --dry-run` 前后完整文件树/控制目录快照一致。运行 `transaction_apply` 和 `transaction_recover`，只以实测进程终止恢复结论交付，不称断电验证完成。

## 任务 7：项目安装、同步、技能选择与诊断

**Consumes / 前置依赖:** 任务 4 的 `Bundle::select(profile: Profile, installation: InstallationKind, explicit: &BTreeSet<String>) -> Result<Selection>`；任务 5/6 的 apply/status/recover；任务 3 的 inspect/read_checked 和 `fs::publish_new_tree(target: &Path, tree: &RenderedTree, cancel: &Cancellation) -> Result<PublishReport>`。ChangeSet 从 transaction 导入。

**Files:** 创建 `src/project/{mod,state,plan}.rs`；接入 `src/cli.rs`。参考 `internal/project/project.go`、`internal/domain/*`。
**Interfaces:** `project::plan(bundle: &Bundle, request: &ProjectRequest) -> Result<ProjectPlan>`；`project::execute(plan: ProjectPlan, cancel: &Cancellation) -> Result<ProjectReport>`；`project::list_skills(root: &Path, bundle: &Bundle) -> Result<SkillsReport>`；`project::doctor(root: &Path, bundle: &Bundle) -> Result<DoctorReport>`。ProjectPlan 区分无变更、已有根 ChangeSet、全新根 RenderedTree。
**交付物:** init/attach/sync/skills/项目 doctor/recover 可用；全部写入只经过任务 3、5、6。
**临时验证入口:** `tests/project_ownership.rs`，命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test project_ownership`。预期退出 0，且：所有权表及两 Profile×两安装类型通过，未知同内容文件不接管，dry-run 无落盘。

- [x] 实现 `.yss/project.json` 与安装身份检测，读取实际旧 marker 常量列入拒绝集合；旧 .yss.json、家族标记、工具收据、未知 .yss 都只读拒绝，不用新记录掩盖。
- [x] 按设计 `7` 的完整决策表实现 plan，保留 lastInstalled 基线与实际内容的差别；未知同内容受管文件也冲突，user-owned/protected 保留现有内容且不取得新受管所有权。
- [x] 实现 defaults＋explicit 的闭包与 project/skills 切换；仅保存用户请求的 explicit，缺失/退役显式项作诊断，不安装 replacements；清理文件与记录遵循同一所有权表。
- [x] 实现 init 根语义：不存在根整树发布，已有空根事务写入，非空根拒绝；attach 不删已有根。所有冲突先整体汇报，执行按只读约束重检，成功后才更新基线。
- [x] 实现 list/doctor 的只读诊断、pending 提示及 recover CLI 调度；doctor 工具安装分支留给任务 9，help 不要求项目存在。
- [x] 在临时 `tests/project_ownership.rs` 表驱动覆盖设计 `7` 每一行，包括本地删除；`assert_eq!(failed_sync_tree, before)`，`assert_eq!(unknown_same_bytes.code, CONFLICT)`。验证两 Profile×两安装类型、模式往返、显式/默认集合变化及退役技能说明。
- [x] 运行 `project_ownership`；逐个写命令验证 dry-run 不创建不存在根、锁或缓存；重放一次成功 sync 返回 unchanged，执行与之前预览发生漂移时拒绝。

## 任务 8：Java 平台目录与 DDD/MVC 脚手架

**Consumes / 前置依赖:** 任务 4 的 Bundle 与 `scaffold::catalog(bundle: &Bundle) -> Result<CatalogReport>`；任务 2 的严格资产读取；任务 3 的 `fs::publish_new_tree(target: &Path, tree: &RenderedTree, cancel: &Cancellation) -> Result<PublishReport>`。

**Files:** 创建 `src/scaffold/{context,ddd,mvc}.rs`；修改 `src/scaffold/mod.rs`、`src/cli.rs`。参考 `internal/scaffold/{catalog,platform,source,render,tree,ddd,mvc,*manifest,mvc_parent_pom}.go` 和 Bundle 中的 Java 资产。
**Interfaces:** 复用任务 4 的 `scaffold::catalog(bundle: &Bundle) -> Result<CatalogReport>`；产出 `scaffold::render(bundle: &Bundle, request: &ScaffoldRequest, context: &GenerationContext) -> Result<RenderedTree>`；`scaffold::generate(bundle: &Bundle, request: &ScaffoldRequest, cancel: &Cancellation) -> Result<ScaffoldReport>`。GenerationContext 注入一次采集的作者、UTC 时间与来源。
**交付物:** 两种业务空骨架均完整生成；复用渲染/发布基础，不引入模板副本或 JS 顺序模拟。
**临时验证入口:** `tests/scaffold_output.rs`，命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test scaffold_output`。预期退出 0，且：DDD/MVC 的文件/坐标/权限及确定性成立，目标竞争不覆盖；Maven 构建另报实际结果。

- [x] 基于任务 4 的 catalog 实现生成输入上下文：工程名、Java 包名、平台 profile、Java/Spring Boot 精确匹配、group/version、父 POM 与 YSS 组件坐标；拒绝缺参、非法标识、版本范围/占位符和不匹配平台。保留小型严格占位符替换器，不引入 MiniJinja/Handlebars 或新增模板语法。
- [x] 实现 DDD 渲染，保持所选平台模块、POM、资源、wrapper、空目录和模式；不加入未请求示例，不执行 Git/Maven。
- [x] 实现 MVC 渲染，复用同一上下文和校验；保留其模块/父 POM 关系，不复制一套日期、路径或 XML 转义实现。
- [x] 实现全树未替换占位符、禁止样例凭据与路径冲突检查，生成 `.yss/scaffold.json`；其文件清单排除自身以避免摘要循环，身份使用 `format: yss.scaffold`、`formatVersion: 1`。generate 在全部校验后调用 publish_new_tree。
- [x] 在临时 `tests/scaffold_output.rs` 对 DDD/MVC 使用固定上下文；`assert_eq!(tree_a, tree_b)`、`assert!(unresolved_placeholders.is_empty())`、`assert!(business_examples.is_empty())`；断言模块/POM 坐标、包路径、wrapper 可执行模式及空目录与所选资产一致。
- [ ] 验证目标被抢占、已有空目录、取消和磁盘不足不覆盖原目标；dry-run 不创建 output-dir。对代表性生成工程从其根运行 `./mvnw -DskipTests package`（Windows 对应 mvnw.cmd），如缺 JDK/私服依赖记录真实失败，不篡改版本绕过。
- [x] 运行 `scaffold_output`，通过 CLI 实际生成一次 DDD 和 MVC；生成正确与 Maven 构建成功分开交付。

## 任务 9：新发行包与离线工具安装/升级

**Consumes / 前置依赖:** 任务 2 的 codec；任务 3 的 inspect/read_checked/publish_new_tree；任务 5/6 的 `transaction::apply(root: &Path, changes: ChangeSet, cancel: &Cancellation) -> Result<TransactionReport>` 及状态/恢复。ReleaseInfo、VerifiedRelease、UpgradePlan 归 updater；不依赖网络成功才能验证离线路径。

**Files:** 创建 `src/updater/{mod,release,installation}.rs`；修改 `src/bin/yss-bundle.rs`、`src/cli.rs`。参考 `internal/updater/{updater,installation,archive_open*,version}.go`。
**Interfaces:** `updater::prepare(request: &UpgradeRequest, build: &BuildInfo, cancel: &Cancellation) -> Result<UpgradePlan>`；`updater::execute(plan: UpgradePlan, cancel: &Cancellation) -> Result<UpgradeReport>`；`updater::doctor(root: &Path) -> Result<ToolDoctorReport>`；`updater::release::package(binary: &Path, info: &ReleaseInfo, out: &Path) -> Result<PackageReport>`；`updater::release::verify(archive: &[u8], expected_sha: &Sha256, expected_target: &str) -> Result<VerifiedRelease>`。VerifiedRelease 含已校验 manifest 与载荷，字段私有且只能由 verifier 构造；提供 `manifest(&self) -> &ReleaseManifest` 供版本核对，`into_payload_tree(self) -> RenderedTree` 供安装规划。UpgradePlan 区分只读预览、无变更、新根整树、已有根事务。
**交付物:** Rust 格式包可生产、验证和离线安装；现有 Go 包被明确拒绝。
**临时验证入口:** `tests/offline_upgrade.rs`，命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test offline_upgrade`。预期退出 0，且：新包生产/核验/安装闭环成立；错误包、旧格式、降级、漂移全部零写入。

- [x] 定义 ReleaseManifest 并实现 `yss.release`：版本、精确 target、载荷路径/摘要/模式/长度与真实来源；归档只放 yss/yss.exe 和 release-manifest.json，不预置安装收据。命名 `yss_<version>_<target>.tar.gz`，Windows 为 .zip。
- [x] 在已有内部 binary 增加 `yss-bundle package --binary <path> --version <version> --target <triple> --out <archive>`，复用 package API 生产一致 manifest 和摘要报告；保持任务 4 的无子命令 Bundle 生成调用可用，不增第三个工具或发布脚本。
- [x] 实现 release::verify 统一校验归档摘要、目标、合法版本、逐条载荷与模式；读取本地文件前限长，解压累计限长，拒绝未知载荷、重复/别名路径、链接、设备、加密、超限、尾随流及 manifest 超过 8 MiB。SHA256SUMS 使用真实归档字节，prepare 另核对 --to 及安装版本。
- [x] 实现 `.yss/tool.json`、根身份及当前受管载荷预检；工具根不兼作项目/Git 根，初装仅新/空目录。默认根来自实际运行程序，必须已有新格式收据；显式新根不依赖旧程序。
- [x] 实现版本语义和 UpgradePlan：稳定 `--to` 可带 v，离线版本必须一致；拒绝降级和受管漂移，等版本且完整返回 unchanged。新根整树发布，已有空根/受管根使用事务，收据最后写；运行中 Windows 程序在任务 11 前明确 unsupported，不当完成。
- [x] 在临时 `tests/offline_upgrade.rs` 验证 package→verify→install→doctor 闭环；`assert_eq!(installed_payload_sha, manifest_sha)`；错误摘要/target/降级/旧格式/用户改动均零写入，等版本 unchanged。构造 256 MiB 边界、超限解压与尾随流，判定符合硬上限。
- [x] 运行 `offline_upgrade`；用临时目标验证首次安装、后续更新、离线 dry-run；不安装到用户 PATH 或现役工具目录。

## 任务 10：固定 GitLab 在线升级与网络取消

**Consumes / 前置依赖:** 任务 9 的 `updater::release::verify(archive: &[u8], expected_sha: &Sha256, expected_target: &str) -> Result<VerifiedRelease>` 及 prepare/execute；任务 1 的 `Cancellation::cancelled()`。network 仅返回已核验的来源信息/字节，不能直接写安装根。

**Files:** 创建 `src/network.rs`；扩展 `src/updater/mod.rs`、`src/cli.rs`。参考 `internal/updater/{gitlab,online}.go`。
**Interfaces:** `network::resolve_release(version: Option<&Version>, target: &str, auth: &GitlabAuth, cancel: &Cancellation) -> Result<RemoteRelease>`；`network::download(release: &RemoteRelease, auth: &GitlabAuth, cancel: &Cancellation) -> Result<Vec<u8>>`。GitlabAuth 为内存秘密，不实现含令牌的 Debug/Serialize；RemoteRelease 只在网络模块核验后构造。
**交付物:** online prepare 复用离线包验证与安装，不另建升级执行路径。
**临时验证入口:** `tests/online_upgrade.rs`，命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test online_upgrade`。预期退出 0，且：固定来源/凭据边界、取消及流限制成立，在线预览没有归档 GET 和写盘；真实 GitLab 连通性另报。

- [x] 构造只信任显式 CA 的 rustls 客户端，保留 hostname 校验，关闭隐式来源/代理绕行；设置有限连接/请求/整体操作时限、逐跳重定向次数和读取上限。局部 Tokio 通过 Cancellation::cancelled 与请求/流等待竞争响应取消；文件流程保持同步，不启动网络运行时。
- [x] 按固定完整项目路径查询并核对正数 project ID、web_url、版本、Release 和资产身份；逐跳限制同源同项目，并仅向允许目标附鉴权。输入令牌不得进入 URL 或诊断。
- [x] 实现在线预览只读元数据、SHA256SUMS、HEAD；实际下载前先检查目标根、当前版本/归属，下载后调用任务 9 的 release::verify，并将包内版本与已核验的 RemoteRelease 版本对照。不存在正式发行返回 NOT_READY，不创建 Release。
- [x] 实现流式长度累计及摘要、取消/超时、未匹配 Content-Length 的错误；bounded Vec 最大 256 MiB，不创建磁盘缓存；解压同样有累计限制。
- [x] 在临时副本注入私有 transport，不暴露产品 origin override；`tests/online_upgrade.rs` 模拟错误项目身份、跨源/同源异项目重定向、恶意错误体、慢流/截断/超长。`assert!(foreign_requests.iter().all(|r| !r.has_token()))`、`assert!(!output.contains(secret))`。
- [x] 运行 `online_upgrade`；`assert_eq!(preview_archive_get_count, 0)`、`assert_eq!(preview_disk_writes, 0)`，取消返回非零。若现有凭据允许，额外只读验证真实 GitLab 元数据/HEAD；无凭据或无 Rust Release 如实记录，不把模拟结果说成线上通路通过。

## 任务 11：Windows 运行中更新及平台恢复闭环

**Consumes / 前置依赖:** 任务 3 的 Windows 原语证据与窄平台 API；任务 5/6 的日志、apply/recover；任务 9 的 UpgradePlan。扩展 transaction 内部步骤，保持公共 apply/recover 签名不变。

**Files:** 修改 `src/fs/platform/windows.rs`、`src/transaction/{journal,apply,recover}.rs`、`src/updater/installation.rs`。
**Interfaces:** 在事务内部增加有类型 `ExecutableReplacement` 原生步骤记录，包含原目标、旧程序保留路径、新候选身份及每一步 before/after；公共 apply/recover/UpgradePlan 签名保持不变。
**交付物:** Windows 能原地更新运行中的 Rust CLI；不改成要求用户人工换目录。
**临时验证入口:** `tests/windows_replace.rs`，命令 `cargo test --locked --manifest-path "$YSS_PROBE_ROOT/Cargo.toml" --test windows_replace`。预期退出 0，且：必须在 Windows 原生进程验证运行中更新和关键中断点恢复；其他平台结果不能替代。

- [ ] 复用任务 3 的 Windows 原语证据；尚缺原生结果或集成改变了共享/命名方式时补足对应验证。选用已证明的原语接入事务，不把 self-replace 的清理副本、子进程或临时文件生命周期隐藏在 WAL 外。
- [x] 实现完整候选先准备、持久化旧程序保留路径、旧 exe 原生移动、新 exe no-replace 发布、磁盘目标及收据核验、提交；每次可能留下空档的动作有单独 intent/完成记录。
- [x] 实现 recover 对明确原生中间态的预检和回滚；目标缺失必须有该记录证明，不能把任意 Missing 都当本事务造成。
- [x] 实现旧进程句柄仍存在时保留旧 exe、之后按归属安全清理；报告 committed 与 cleanup pending 分开，明确当前进程仍旧版本但磁盘已新版。不改 PATH/注册表，不用未登记属性修改绕过失败。
- [ ] 在 Windows 临时 `tests/windows_replace.rs` 验证目标正在执行、并发句柄、rename 失败、旧已移动/新未发布时强杀、发布后/收据前强杀、回滚再中断；`assert_eq!(recovered_installed_version, before_version)`，成功时 `assert_eq!(disk_version, after_version)` 且旧进程可正常退出。
- [ ] 用从已核验归档提取的独立可信新程序执行 `recover --root <工具根>`，证明主入口缺失时仍可恢复；无额外 helper binary 分发。
- [ ] 在六个目标的原生环境重复相关 fs/事务/脚手架/升级代表用例，记录实际 OS/文件系统/架构。不可用环境是未完成验证项，交叉构建或模拟器结果不得冒充原生验证；不得据此删减目标或宣布全部完成。

## 任务 12：Cargo CI、跨平台产物与来源

**Consumes / 前置依赖:** 任务 4 的 Bundle 生成 CLI、任务 9 的 package CLI 与 verify、任务 11 的原生平台验证结果；工具只放构建环境，构建结果与原生验证分别登记。

**Files:** 修改 `.gitlab-ci.yml`、`Cargo.toml`、`build.rs`、`.gitignore`；参考现有 `docs/template-release.json` 但不伪造标签记录。
**Interfaces:** CI 使用任务 4 的 Bundle 入口、任务 9 的 package 入口与相同 verifier；不定义另一套 manifest/schema/来源算法。
**交付物:** CI 使用 Rust 生成 Bundle、构建、核验与打包，结果能对应实际 CLI/模板源码。
**验证入口:** 实际模板生成 Bundle 后运行 `cargo build --locked --release --bin yss --target <target>`；对实际归档运行 `<新 yss> upgrade --artifact <归档> --sha256 <实测 SHA256> --tool-root <临时工具根> --dry-run --json`。预期构建退出 0、包核验成功且临时工具根未创建；每个 target 单独记录。

- [x] 检查实际 runner/工具链可用性，选定并核验 Rust 构建镜像及 digest；保留固定 CA、CI_SERVER_URL、Job Token 的 /api/v4/job 身份核验与受限模板 Git 拉取逻辑，不给 CI 注入 PAT。不用 cargo-dist 改建固定 GitLab 流程。
- [x] 核验现有固定模板 tag 的内容是否满足任务 4 输入；若不满足，使用明确记录的实际模板 commit 构建开发产物，不悄悄把旧 tag 指向工作树。正式模板 tag 的创建/变更仍需相应授权。
- [x] 替换 Go 构建步骤为真实 Bundle 生成、`cargo fmt --all -- --check`、`cargo clippy --locked --all-targets -- -D warnings` 和 `cargo build --locked --release --bin yss --target <target>`；Cargo.lock 固定，缓存按工具链/锁文件/target 隔离，不缓存凭据，不隐式 fetch 最新依赖或网络生成模板。
- [ ] 配置可实际运行的六目标构建与原生验证作业；有 Docker/Podman runner 时优先评估 cross 构建两个 Linux musl target，锁定工具版本和实际镜像摘要；否则使用已核验的工具链。macOS/Windows SDK、链接器及原生验证单独保留，cross 模拟结果不替代。缺少 runner 标为待解决，不用无效 tag 或手工跳过成功掩盖。
- [x] 用 package 入口生成归档，再用实际新 CLI 离线 dry-run 验证；按真实产物生成 SHA256SUMS，CI artifacts 不自动变成正式 Release，不推送/tag/上传发行。
- [x] 对实际依赖图按需运行 `cargo tree -e features`、`cargo machete`、`cargo deny check`，使用符合项目约束的配置；核对生成代码/cfg 引起的误报及工具运行前后 Cargo.lock，不自动删依赖或添加统一门禁。工具只装于开发/构建环境，不进入发行物。
- [x] 运行已能运行的 CI 对应命令，核对来源、target、归档/载荷摘要和 Bundle 内容；用 cargo-bloat 辅助定位代码体积，分析需要时在临时构建保留符号，最终发行程序另行实测。在相同工具链/target/Bundle 下比较 opt-level 3/s/z 和组件 feature；分别记录代码段、完整裸程序、压缩包大小及启动时间。超出 15 MiB 先检查 features/重复依赖，不删保护能力或使用未评估压缩壳。
- [x] 从不含 Go/Bun/Node/Python 的运行环境执行 help/version/catalog、项目安装及代表性脚手架；检查动态依赖与最低 OS 实际证据，区分构建工具依赖和用户运行依赖。

## 任务 13：切换现役文档、同步技能并移除 Go

**Consumes / 前置依赖:** 任务 1–12 已实现的实际帮助、格式、构建入口、来源与运行结果；只按实际可用能力更新两仓现役资料。完整 Go 删除不能提前替代尚未实现的功能。

**Files — CLI:** 修改 `AGENTS.md`、`README.md`、`docs/{README,cli-help,build-install,compatibility,skills-discovery}.md`；将 `docs/scaffold-go.md` 的现役说明迁为 `docs/scaffold.md` 并更新现役链接。删除 `cmd/yss/*.go`、`internal/` 的已替代 Go 实现/内嵌旧资产、`tools/bundle/main.go`、`tools/bundle/README.md`、`go.mod`、`go.sum`，实际删除前核对目录无新无关文件。
**Files — 模板:** 修改 `.agents/skills/release-yss-cli/SKILL.md`、`.agents/skills/setup-yss-harness/SKILL.md`；同步 `.workbuddy/skills/{release-yss-cli,setup-yss-harness}/SKILL.md` 和 `profiles/backend/.workbuddy/skills/setup-yss-harness/SKILL.md`。核对关联 `agents/openai.yaml` 的说明；`.claude/skills` 两项为指向源的链接，保留链接。两套分发清单只修有证据的字段/引用/摘要差异，不重建已有依赖表。
**Interfaces:** 文档、技能及 CI 命令以本次实际 Rust binary 的帮助、manifest 和构建结果为准。
**交付物:** 工作仓及现役分发入口全部转为 Rust，历史资料保留；不存在依赖 Go 的隐含现役路径。
**验证入口:** `cargo fmt --all -- --check`、`cargo clippy --locked --all-targets -- -D warnings` 退出 0；以重新生成的真实 Bundle 完成任务 12 的 build/package/离线预览，并重跑受删除或资料更新影响的临时用例。检查实际 diff，不能用无测试仓库的空 cargo test 代替行为验证。

- [x] 在真实工作树核对所有 Go 调用者、构建指令、导出/生成物及脚手架链接；确认任务 1–12 的实际完成状态和剩余环境问题，再删除已被替代实现，保留历史 docs 与用户草案。
- [x] 更新 CLI 现役文档：Rust 构建顺序、target 命名、新状态格式、旧安装只读拒绝、新目录初装、固定 GitLab、dry-run、冲突/恢复及 Windows 空档恢复。尚未正式发布的能力明确称开发构建，不宣称旧安装已支持。
- [x] 更新两项模板技能源及真实副本，移除当前残留的 Go/GOOS/GOARCH、schema 2、默认 GitHub 和 --source 建议，替换为已实现命令；保持权限/摘要/CA/发行授权要求，不发布或同步到用户全局目录。
- [x] 核对技能链接、复制副本和两安装类型引用闭包；只在确认清单需要内容变更时修改，重新生成最终 Bundle，避免发行程序仍内嵌旧技能说明。
- [x] 在临时副本执行最终各命令代表流程及错误路径，重复仅限 Go 删除/技能重生成可能影响的检查；运行 `cargo fmt --all -- --check`、`cargo clippy --locked --all-targets -- -D warnings`、真实 Bundle→release build→package→离线预览。
- [x] 核验现役源码/CI/构建文档不再要求 Go，Bundle、源码和程序的来源一致；命令目的、包格式、权限、事务与六目标结果均能回指实际证据。历史 Go 文本不为“零匹配”而删除。
- [x] 清理临时验证副本与自产临时根，检查最终 diff 没有测试套件、包装脚本、凭据或无关改动；按用户选定执行方式完成最终审查，报告实际修改、检查和未完成项，不自动提交或发行。

## 组件采用的判断与交付顺序

| 决定 | 在哪项任务落实 | 可核验结果与处理方式 |
| --- | --- | --- |
| saphyr-parser＋serde_path_to_error | 2 | 通过相同严格输入用例及字段定位/脱敏断言；不能以解析器默认行为放宽 Spec |
| cap-std/cap-fs-ext | 3 | 先验证逐级 no-follow、身份、reparse 和发布；适配通过后采用，缺口用窄平台原语；整体不适合时保留原 std/rustix/windows-sys 方案 |
| CancellationToken＋ctrlc | 1、4、6、10 | binary 注册信号，library 注入取消对象；慢网络请求可取消，已取消请求的回滚仍有独立上下文 |
| cross | 12 | 仅在实际容器 runner 与两个 Linux musl target 构建验证后使用；原生平台结果分开报告 |
| cargo-bloat/machete/deny | 12 | 针对真实依赖和产物运行；核对误报、配置及 Cargo.lock 变化，不把诊断工具当产品正确性的证明 |

| 任务范围 | 可交付结果 |
| --- | --- |
| 1–4 | Rust 工程、严格输入、文件保护基础和真实内嵌 Bundle；version/help/catalog 可用，Windows 原语已有验证结果或明确缺口 |
| 5–6 | 可观察的准备/提交/失败/恢复闭环；完成后才接通项目及工具写入 |
| 7–8 | 项目安装/同步/技能/诊断，以及 DDD/MVC 两种脚手架 |
| 9–11 | 同一包校验器支撑离线/在线升级，Windows 运行中替换及恢复接通 |
| 12–13 | 真实多目标产物、配套资料和技能同步，移除 Go 生产路径 |

这些是交付顺序，不新增批准包或流程门禁。未提供的外部环境不需要靠猜测补齐：Windows/macOS/Linux 原生结果、GitLab CA/PAT 与正式 Release、JDK/Maven 私服分别对应任务 3/11/12、10、8 的实际验证。环境缺失时保存明确未完成项，继续可独立完成的工作；不伪造版本、runner 或成功记录。


## 执行顺序与完成边界

推荐顺序为 `1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 10 → 11 → 12 → 13`。项目、脚手架与升级共享格式、文件保护、事务和 Bundle，故采用一份计划；这些任务是同一交付的可检查增量，不是可独立发布的子产品。各任务的 Consumes 列明真实前置依赖；外部环境缺失时可继续不依赖该结果的工作，不擅自把未完成项勾为完成。

任务 3 包含提前进行的 Windows 原语验证，任务 11 完成其事务集成与升级恢复闭环；不会把前期验证等同于完整升级已实现。首次可用的开发 binary 出现在任务 4；项目写入须任务 6 的恢复完成后才启用。全部功能重写、Go 删除与分发资料切换完成才可称代码迁移完成；原生平台、线上来源或私有 Maven 构建未验证时，分别列出限制，不用编译成功替代。

Windows 原地升级失败、平台环境不可用、模板清单有真实问题时，先定位并保留已完成工作。仅在需改变已批准行为或必须由用户提供外部环境/凭据时询问；不为常规实现选择反复请求批准。

本轮已按获批准的完整 Spec 自审：模块/输出对应任务 1–2，Bundle 对应 4，所有权对应 7，文件保护与恢复对应 3/5/6/11，脚手架对应 8，升级对应 9–11，组件选择与验证对应 1/2/3/6/10/12，构建分发对应 12，语言及资料切换对应 13。五项 Review Focus 均有归属用例；已校正 ChangeSet 归属、取消对象注入及线上/离线共同 verifier，公共类型和接口在生产任务定义后再消费。该自审不是实现、测试或平台通过证明。


## 执行结果（2026-11-01）

Rust 功能、Go 替换、现役文档和模板两项技能源/副本已落入隔离工作树，未提交或发行。已完成的实现项与本机验证分别勾选；未勾项不等于未写代码，其中有环境缺口及尚未覆盖的细分故障点。

macOS 27.0.1 ARM64：31 项临时断言通过，另通过 18 个进程强杀/回滚/并发场景、256 MiB 解压边界与超限断言、实际首次安装与后续版本升级/降级拒绝；DDD/MVC 均用 JDK 17.0.16 成功 Maven 构建。最终审查发现的“预检后新出现的程序文件被覆盖”已先复现失败，再修复为保留原 Missing 基线并验证通过。所有验证材料仅位于临时副本，不新增持久测试套件。

实现选择：cap-std/cap-fs-ext 配合原生发布；完整 16 字节文件 ID；从 ELF/Mach-O/PE 专用段读取真实构建来源；按同源产物比较采用 opt-level=3。绝对祖先初次检查别名，后续以逐级 no-follow 与身份复验防止替换，目标内部路径仍逐次检查别名；不承诺持续检测新出现但未替换既有目录的祖先同名别名。

剩余环境验证：Windows 原生运行中替换及中断恢复、Linux 两架构和 macOS Intel；真实 GitLab 在线 Release/CA/PAT；实际 CI pipeline。Windows 原语已交叉类型检查，但完整构建因缺 SDK 头文件未通过；OrbStack 停止后未执行 Linux CLI 构建。模板 CI 仍需指向包含配套技能修改的真实提交，不改写固定 v5.0.2 标签。未验证真实断电持久性、最早受支持 OS 或其他 Maven 配方。

实现与验证方式调整：恢复强杀用临时独立进程驱动完成；网络和 ZIP 私有入口在临时副本中注入断言，不开放产品故障参数。体积分析用实际 Mach-O 段大小及 Cargo feature tree，未安装 cargo-bloat/machete/deny，不作依赖漏洞/许可审计结论。构建及原生平台结果分开报告，完整六目标验证没有勾成完成。
