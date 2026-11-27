# 桌面应用构建与发行

桌面应用工程位于 `desktop/`，与独立 `yss` CLI 分别安装。桌面应用通过随包的 `yss` 程序提供工作台服务，不应修改签名应用包中的程序文件，也不自动更改终端 PATH。工作台数据不属于安装目录；卸载前仍须核对安装器的实际行为。

## 从现有工作台打开桌面版

启动「YSS 工作台」后，应用优先在自己的窗口打开现有工作台界面。左侧栏底部的「桌面」会在主窗口内切换到桌面设置。「返回工作台」返回先前页面。macOS 的应用菜单、菜单栏图标和 Windows 托盘也提供入口。桌面设置会显示桌面版本、工作台实际版本和服务来源。

在 macOS 上，点击菜单栏的 Y 图标可查看当前连接和作业计数，并打开工作台或设置。右键可打开简短菜单，按 Esc 可收起面板。也可以从「窗口 → 菜单栏概览」打开面板。应用菜单中的「桌面设置」使用 `⌘,`，文件菜单中的「打开工作台」和「关闭窗口」分别使用 `⌘O` 和 `⌘W`。关闭窗口后服务继续运行，需要结束桌面应用时从「退出」协调作业后确认。

关闭普通主窗口时会记住位置和大小。记录中的屏幕不可用时，主窗口会在当前可用屏幕内重新定位。菜单栏面板每次按菜单栏图标定位；开机自动启动后的主窗口保持隐藏，直到用户主动打开。

桌面版默认使用独立 CLI 的工作台数据目录。若该目录正由缺少桌面协议的旧 CLI 服务使用，桌面设置会给出处理指引。先在原工作台处理活动作业和队列，再从原入口正常结束旧服务，并在桌面设置里重试。桌面随后通过随包服务打开同一目录。使用自定义目录时，可在设置中选择已有工作台目录。

macOS 随包服务会从当前账号的登录 shell 恢复查找工具所需的环境，用于发现本机 Pi、Git、`gh` 和 `glab`。登录配置不可用或超时时继续使用应用继承的环境。Windows x64 随包服务会读取当前用户和系统的工具路径，并分别探测 Pi 基础作业和离线命令。就绪状态和失败原因可在本机诊断中查看。

## 候选构建

`.github/workflows/desktop-build.yml` 在 macOS ARM64 和 Windows x86_64 原生 runner 构建候选。工作流使用 `ui/pnpm-lock.yaml`、根目录和桌面目录的 Cargo 锁文件，从固定模板提交生成内嵌 Bundle，再为目标架构构建 `yss`，复制为 `desktop/binaries/yss-<target-triple>`（Windows 加 `.exe`）。Tauri CLI 将对应程序作为 sidecar 打包。

候选工作流使用 pnpm store 和 Cargo 第三方依赖缓存。每次从当前源码、UI 构建结果和固定模板提交重新构建 sidecar，再从安装包读取其中的程序，核对架构、摘要和构建版本。该工作流只上传 CI artifact，显式关闭更新产物和代码签名。`candidate.json` 标明签名状态及产物大小，`yss-build.json` 和 `bundle-build.json` 分别记录实际程序和模板来源，`SHA256SUMS` 校验安装包。

本地构建先完成根目录的前端和 Bundle 步骤，模板路径必须指向实际的 Git 工作树；不要将工作树内容标记成另一个版本的正式标签。以下示例仅用于 macOS ARM64 候选，不发布：

```text
pnpm --dir ui install --frozen-lockfile
pnpm --dir ui build
cargo run --locked --bin yss-bundle -- --source-root /absolute/yss-spec-project-template --out assets/bundle.tar.gz
cargo build --locked --release --bin yss --target aarch64-apple-darwin
mkdir -p desktop/binaries
cp target/aarch64-apple-darwin/release/yss desktop/binaries/yss-aarch64-apple-darwin
```

然后在 `desktop/` 目录使用锁定版本的 Tauri CLI 构建。正式签名和 updater 需要对应真实配置与密钥，不应将密钥放入仓库：

```text
npm install --prefix /absolute/tauri-cli --no-save --no-package-lock --ignore-scripts @tauri-apps/cli@2.12.6
node /absolute/tauri-cli/node_modules/@tauri-apps/cli/tauri.js build --ci --no-sign --no-bundle --target aarch64-apple-darwin --config '{"bundle":{"createUpdaterArtifacts":false}}' -- --locked
CI=true TAURI_BUNDLER_DMG_IGNORE_CI=true node /absolute/tauri-cli/node_modules/@tauri-apps/cli/tauri.js bundle --ci --no-sign --target aarch64-apple-darwin --bundles dmg --config '{"bundle":{"createUpdaterArtifacts":false}}'
```

DMG 背景包含同一逻辑尺寸的 1× 和 2× 图像，窗口尺寸和图标落点由 `desktop/tauri.conf.json` 配置。Tauri 在 `CI=true` 时默认跳过 Finder 布局，候选与签名构建都在打包步骤设置 `TAURI_BUNDLER_DMG_IGNORE_CI=true`，使安装窗口写入布局。打包的 macOS 会话需要允许构建进程控制 Finder。

Windows 候选使用 `x86_64-pc-windows-msvc`，先生成 `desktop/binaries/yss-x86_64-pc-windows-msvc.exe`，再将 bundle 目标改为 `nsis`。Windows 候选中用于 `yss` 的 Cargo build 使用 `RUSTFLAGS=-C target-feature=+crt-static`，在独立桌面应用构建前清除此环境变量。NSIS 使用简体中文原生向导和 YSS 安装及卸载图标，按当前用户安装，缺少 WebView2 时提供联网引导。启用防降级配置的安装器发现已有较新版本时，禁止直接覆盖安装。

## 桌面发行与 CLI latest

既有 CLI 在线升级读取 `iloveZzz/yss-cli` 的 GitHub `/releases/latest`，要求正式 `v<SemVer>` 标签和 CLI 发行资产。桌面版可与 CLI 独立发布，但必须使用 `desktop-v<SemVer>` 标签，并在创建或更新 Release 时显式设置 `make_latest=false`；桌面更新使用单独的稳定版发现流程，不能把仓库 `/latest` 当成桌面最新版本。

正式桌面发行使用真实的 macOS Developer ID 签名和公证、Windows 受信代码签名，以及与系统签名分别管理的 Tauri updater 密钥。更新端应要求签名绑定版本（`requireSignedVersion`），并验证更新源、平台、版本和签名。初次下载的 Windows 程序即使正确签名，也可能出现 SmartScreen 信誉提示。

`.github/workflows/desktop-signed-candidate.yml` 仅支持手动触发，输入的稳定版本须与 `desktop/Cargo.toml` 和 `desktop/tauri.conf.json` 一致。它不创建 Release，也不修改 GitHub `latest`；只有两个平台均成功后才组装同一候选 artifact。工作流将产物文件名转换为 ASCII 名称，但不更改签名更新包的字节；合并时用应用内嵌公钥验证两个 updater 签名及受签版本，生成 `latest.json` 和 `SHA256SUMS`。Apple DMG 另行公证并装订，不能仅以应用包已公证推断 DMG 已公证。

手动触发前需要在 GitHub 仓库配置以下 secrets；缺少任何本流程必需的密钥时，对应构建失败，不会被标成已签名：

- `YSS_DESKTOP_UPDATE_PUBKEY`：Tauri updater 公钥内容；编译进应用，也用于核对候选签名。
- `TAURI_SIGNING_PRIVATE_KEY`、`TAURI_SIGNING_PRIVATE_KEY_PASSWORD`：与上述公钥配对的 updater 私钥及密码，独立于系统代码签名。
- `APPLE_CERTIFICATE`、`APPLE_CERTIFICATE_PASSWORD`：Base64 编码的 Developer ID Application `.p12` 及其导出密码。
- `APPLE_SIGNING_IDENTITY`：Apple Developer ID Application 签名身份。
- `APPLE_API_KEY`、`APPLE_API_ISSUER`、`APPLE_API_KEY_P8`：App Store Connect 团队 API Key ID、Issuer ID 和 Base64 编码的 `.p8` 私钥。构建时在 runner 的临时目录写入受限权限文件，供 Tauri 和 `notarytool` 使用，结束后删除；私钥内容不作为命令参数。
- `WINDOWS_CERTIFICATE`、`WINDOWS_CERTIFICATE_PASSWORD`：Base64 编码的 Windows 受信 `.pfx` 及其导出密码；证书需含私钥。

另将仓库变量 `YSS_WINDOWS_TIMESTAMP_URL` 设置为证书服务商支持 RFC 3161 的 HTTPS 时间戳地址。真实证书、私钥和密码只保存在受控 secrets 中；密钥是否可用、签名身份是否可信及公证是否成功，以实际运行结果为准。

在获得对应发行授权和凭证后，先构建并核对双平台安装包、更新包、签名、来源和摘要，再以非 latest 方式发布桌面 Release。发布前后分别读取 GitHub `/releases/latest`，并用实际 CLI 的升级预览核对其仍解析到原有 CLI 发行；如果不一致，停止并报告，不自动修改远端发行。不要从无签名的 CI 候选补写记录，声称其为已签名或已公证产物。

macOS 首次下载的安装包需实际验证 Gatekeeper、公证及可写位置的应用更新；Windows 需实际验证当前用户安装、WebView2 缺失时联网安装、升级和卸载。目标系统为 macOS 12 最终维护版本及以上、Windows 10 22H2 和 Windows 11；具体兼容和发布范围以各平台实际验证结果为准。
