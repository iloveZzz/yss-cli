# 构建、发行包与安装

当前是 Rust `4.0.0-dev` 开发实现，未提交、推送或创建正式 Release。工具链为 Rust/Cargo 1.99.1，Cargo.lock 固定依赖。用户程序内嵌模板资源，构建过程需要 Rust 工具链及目标平台链接工具；用户运行时不依赖 Go/Bun/Node/Python。

## 本地构建

模板源必须是明确的 Git 工作树，生成器读取实际文件；支持未提交的修改和删除，记录 HEAD、工作树状态、内容摘要。源在生成过程中发生变化会拒绝本次构建；不要把旧标签指向工作树。

```text
cargo run --locked --bin yss-bundle -- --source-root /absolute/yss-spec-project-template --out assets/bundle.tar.gz
cargo fmt --all -- --check
cargo clippy --locked --all-targets -- -D warnings
cargo build --locked --release --bin yss --target aarch64-apple-darwin
```

Bundle 缺失时用户程序编译失败；内部 Bundle 工具可先独立构建。只分发 `yss`，不分发 `yss-bundle`。JSON 输出不混入进度日志，`version --json` 展示实际构建来源。

## 打包

```text
cargo run --locked --bin yss-bundle -- package --binary target/aarch64-apple-darwin/release/yss --version 4.0.0-dev --target aarch64-apple-darwin --out /absolute/dist/yss_4.0.0-dev_aarch64-apple-darwin.tar.gz
shasum -a 256 /absolute/dist/yss_4.0.0-dev_aarch64-apple-darwin.tar.gz
```

输出父目录应存在，已有归档不会被覆盖。内部工具读取实际 ELF/Mach-O/PE 程序的专用构建记录，核对目标架构、版本、Bundle 和源码来源，不能通过参数伪造来源或执行外平台程序。压缩包只包含程序与 `release-manifest.json`；Windows 使用 ZIP。以真实归档生成统一 SHA256SUMS，完整归档和逐条载荷均校验 SHA-256、长度、路径与模式。

归档和累计解压上限均为 256 MiB，清单为 8 MiB。拒绝额外载荷、重复/别名路径、链接、设备、加密、尾随流和错误目标。15 MiB 是体积目标，不以削弱文件保护换取大小。

## 安装和升级

首次安装由 Agent 下载、校验归档并在临时目录安全取得新程序，再使用它安装到独立目录：

```text
<已校验的新程序> upgrade --artifact /absolute/archive.tar.gz --sha256 <实测摘要> --tool-root /absolute/new-tool --dry-run --json
<已校验的新程序> upgrade --artifact /absolute/archive.tar.gz --sha256 <实测摘要> --tool-root /absolute/new-tool --json
/absolute/new-tool/yss doctor --root /absolute/new-tool --json
```

工具目录不能位于 Git 工作树内，不能兼作项目；初装仅新目录或空目录。默认工具根为实际运行程序所在目录，必须已有 `.yss/tool.json` Rust 收据。现有 Go 工具目录只读拒绝，不修改旧程序或旧收据。确认新目录后，按用户的安装授权配置 PATH 或入口链接。

已有 Rust 安装可离线更新或运行 `yss upgrade --to <正式SemVer> --tool-root <目录>`。不允许降级；同版本且同归档、程序完整时 unchanged；同版本来源不同或受管程序已修改会拒绝。`--to` 只接受稳定版本，可带 v；本地开发包可省略 `--to`。预览不会创建目标、锁、事务材料、缓存或执行计划。

在线来源固定为 `https://gitlab.yss.test:9443/yss/yss-cli`，需要显式 `YSS_GITLAB_CA_FILE` 与受限 PAT `YSS_GITLAB_TOKEN`。只信任指定 CA，保留主机校验，禁用隐式代理和自动重定向；逐跳核验同源同项目 URL 后才附令牌。在线预览仅元数据、SHA256SUMS 和 HEAD；真实下载受长度、摘要、取消及超时限制。不支持来源覆盖、GitHub 回退或日常升级的 Job Token。

## 恢复

所有已有根的更新共用安装锁和事务日志，收据最后写入。未提交事务完整预检后回滚，用户在中断后修改的未知状态会产生冲突；已提交事务不会回滚。`recover --dry-run` 只读。

Windows 使用日志登记的候选、旧程序保留路径和发布步骤；当前进程仍可能是旧版本，磁盘目标与清理状态分别报告。旧 exe 仍被占用时可提交并报告 cleanup pending，之后执行 recover 重试清理。若中断导致主入口暂缺，使用从已核验归档取得的独立新程序执行 `recover --root <工具根>`。Windows 原生中断与运行中替换尚未验证，不能据交叉检查宣称可正式发行。

## CI 与平台证据

CI 镜像锁定 `rust:1.99.1-trixie@sha256:09027f873d45b8ecd1dc86f1fa13b7a62b875c961f017a0e331267b350430137`，摘要已从 registry 核验。保留固定 GitLab CA、`/api/v4/job` Job Token 身份核验及受限模板读取；不向 CI 注入 PAT。

在 CI 配置 `YSS_TEMPLATE_COMMIT` 为包含配套 Rust 技能说明的真实完整提交。`docs/template-release.json` 保留既有 v5.0.2 历史标签；未授权创建新模板提交或标签，本地工作树内容不冒充已发布模板。

现有 `yss-linux` 作业按 ARM64 原生主机校验。其他原生作业使用实际注册 runner 的变量 `YSS_RUNNER_LINUX_X86_64`、`YSS_RUNNER_MACOS_ARM64`、`YSS_RUNNER_MACOS_X86_64`、`YSS_RUNNER_WINDOWS_ARM64`、`YSS_RUNNER_WINDOWS_X86_64`；变量缺失意味着对应目标未验证，不代表整个矩阵完成。CI artifacts 不自动发布为正式 Release。

本机 macOS ARM64 已执行项目、文件保护、恢复、离线安装和脚手架代表路径；DDD/MVC Maven 构建已通过。网络使用临时 TLS 服务验证预览、身份、重定向、截断及取消，未验证真实 GitLab 在线发行。Windows 原生函数已交叉类型检查，完整 Windows 构建当前受本机缺少 Windows SDK 头文件阻挡；原生 Windows/Linux x86_64/macOS Intel 结果仍需各自环境提供。

## 本机体积与运行实测

同一 Rust 1.99.1、aarch64-apple-darwin、源码快照及 2,097,919 字节 Bundle，thin LTO、单 codegen unit、移除符号。下表为优化级别比较，时间是本机热启动中位数（version 30 次，catalog 7 次），包含进程启动；不能外推其他平台。

| opt-level | 代码段字节 | 裸程序字节 | tar.gz 字节 | version ms | catalog ms |
| --- | ---: | ---: | ---: | ---: | ---: |
| 3 | 3,256,484 | 6,870,928 | 4,385,602 | 4.70 | 88.62 |
| s | 2,747,540 | 7,202,800 | 4,162,140 | 4.85 | 140.62 |
| z | 2,093,564 | 6,495,552 | 4,097,898 | 4.92 | 181.48 |

最终采用 `3`：catalog 用时约为 `z` 的一半，裸程序增加约 0.36 MiB，仍低于 15 MiB 目标。表中包摘要会随真实来源记录改变；最终交付包以 dist/SHA256SUMS 和对应清单为准。`cargo tree -e features` 已检查，代码段用系统 size 工具读取；未执行 cargo-bloat、machete、deny，不据此声称完成依赖安全审计。

实际宿主 macOS 27.0.1 ARM64。Mach-O 声明 minOS 11.0，未在 macOS 11 实机验证；动态依赖为系统库/Framework。Windows x64 平台原语类型检查通过；完整 Windows 程序受本机缺少 SDK 头文件阻挡。已拉取的 Linux 构建镜像可启动，但 OrbStack 后续停止，未完成 Linux 程序构建或运行。

最终开发包 `yss_4.0.0-dev_aarch64-apple-darwin.tar.gz`：裸程序 6,870,928 字节（6.55 MiB），压缩包 4,385,600 字节（4.18 MiB）。归档 SHA-256：`826b5c64761a8d8c00f58cffc942cd5c710218a12cc2acd5e685cb16b3ea3476`。包仅用于当前开发工作树，不是正式 Release。

在空 PATH 下已实际执行 help/version/catalog、init/attach/sync/skills/doctor/recover、DDD/MVC 生成、离线预览/初装/重复升级。输入和目标使用非 Git 临时目录，未调用 Go/Bun/Node/Python；涉及 Git 工作树的保护性操作仍需系统 Git，缺失时拒绝继续。Rust 程序本身不会启动解释器来完成这些流程；Maven 编译所需 JDK/Maven 环境另算。
