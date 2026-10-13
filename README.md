# 2.0.0 Mac 原生验收与 Windows 构建产物（仓外）

仅准备工具，尚未取得本轮原生或发行资格。实际运行前，独立审查工具，将本目录提交为固定工具 SHA（W），并由主控冻结 clean CLI 2.0.0 提交 C、带 v4.0.2 tag 的模板提交 T、三 Profile Bundle/source-lock、二进制摘要。所有来源 checkout 与运行目录分离；禁止 native override。`source-lineage.json` 记录固定 Git/历史仓外来源与原始摘要。historical/ 只作可审计参考，不能运行旧版本硬编码工具。

首个工具提交 `07bd997dd3a73a0f52231ef8636a86e7eeb14988` 已登记 workflow；本次调整前的已固定工具基线 W2 为 `788cbd7e50b7a0f10733bdd245b90c1ef36448be`。本次单 Mac 范围和 Windows 构建工具仍须独立审查，由主控冻结新的 W 后执行，不沿用修改前的 hash。

## 精确范围

- 本次仅 Mac darwin/arm64：native-smoke 62 命令、native-recovery 9 个真实目标变更后中断、plugin-native-smoke 原有 44 label / 2 个 plugin case，以及真实安装 5 cases。
- Windows amd64 只交叉构建与打包；不运行 Windows native、安装、恢复、在线升级或 NTFS ACL 验收，不授予原生资格。
- 发布后仅 Mac 以公开归档内执行器真实运行 `upgrade --to 2.0.0 --tool-root <隔离目录> --json`。同一次 helper 调用先保留离线安装5，再记录独立 online 命令、安装收据/状态、精确归档摘要、回滚与重复恢复；不重跑 native3、legacy36 或 scoped9。
- Mac 一次 legacy36：3 Profile × 12 case，保留真实旧包、SIGKILL、MIGRATION_REQUIRED / LEGACY_INTERRUPTED / CONCURRENT 与真实恢复，不制造 journal。
- scoped9 逐条使用当前 tools/release/local_evidence.go 的命令。其 report 的 reused=true 仅表示包装本次新执行的 original-execution 数组，绝不复用历史通过资格。
- 正式 local scope gate 绑定 Mac 的 installation5、legacy36、scoped9；requiredPlatforms 精确为 darwin/arm64。Windows 不进入 native input 或 Mac checksums.json，只输出独立 build-only-checksums.json。

## Mac 执行顺序（本轮尚未执行）

下列 C_ROOT/T_ROOT/TOOLS 是固定、clean 的绝对 checkout，C/T/W 为对应完整 SHA。R 是另一个全新仓外证据目录。先由主控替换所有变量；这些命令不接受占位符充作 provenance。先建空 R，并在真实 runner 准备当前 CLI go.mod 对应的 Go、Node 24、Python 3.12 与 jsonschema 4.23.0；BINARY_SHA 从 build-receipt.json 读取并再核实际文件摘要。

```sh
CGO_ENABLED=0 node "$C_ROOT/tools/build-candidate.mjs" --root "$C_ROOT" --out "$R/build-darwin"
mkdir "$R/native-darwin-arm64"
node "$TOOLS/tools/native-release-receipt.mjs" --binary "$R/build-darwin/yss" --binary-sha256 "$BINARY_SHA" --cli-root "$C_ROOT" --template-root "$T_ROOT" --commit "$C" --template-commit "$T" --tools-commit "$W" --version 2.0.0 --template-version 4.0.2 --out "$R/native-darwin-arm64" --artifact-prefix native-darwin-arm64
python3 "$TOOLS/prepare-native-input.py" --cli-root "$C_ROOT" --cli-commit "$C" --template-commit "$T" --evidence-root "$R" --artifact darwin/arm64 build-darwin/yss native-darwin-arm64/native-receipt.json
CGO_ENABLED=0 go -C "$C_ROOT" run ./tools/package --native-candidate-manifest "$R/native-input.json" --required-platforms darwin/arm64 "$R/candidate"
```

候选仅用于安装实测，stableReady=false。`run-observed.py` 是原有来源观察器，包装后续每个真实 gate 命令；第 3 参数为固定 C_ROOT/T_ROOT/TOOLS 的绝对路径 JSON 数组。记录输出路径必须全新，stdout/stderr/exit/drift 保留。

```sh
python3 "$TOOLS/run-observed.py" "$R/program-runner.json" "$C_ROOT" "$SOURCE_ROOTS_JSON" python3 "$TOOLS/run-program-installation.py" --input "$R/native-input.json" --archive "$R/candidate/yss_2.0.0_darwin_arm64.tar.gz" --out "$R/program-installation" --platform darwin/arm64 --cli-root "$C_ROOT"
python3 "$TOOLS/run-observed.py" "$R/legacy-runner.json" "$C_ROOT" "$SOURCE_ROOTS_JSON" python3 "$TOOLS/helpers/stable-recovery-matrix.py" --binary "$R/build-darwin/yss" --binary-sha256 "$BINARY_SHA" --workspace "$R" --output "$R/legacy36" --binding "$R/binding.json" --pin-file "$TOOLS/helpers/legacy-executors-fixed.json" --template-root "$T_ROOT" --node "$NODE_ABS"
python3 "$TOOLS/run-observed.py" "$R/scoped-runner.json" "$T_ROOT" "$SOURCE_ROOTS_JSON" python3 "$TOOLS/run-scoped-template.py" --input "$R/native-input.json" --template-root "$T_ROOT" --cli-root "$C_ROOT" --binary "$R/build-darwin/yss" --out "$R/scoped-template"
```

legacy binding 按 binding.example.json 的字段由主控生成；例子默认不授权且无有效 SHA，因此不能执行。三个旧 tgz、installed executor、snapshot 当前可读且摘要已核对，实际执行仍重新校验。旧 installed package 的每个归档文件须与 tgz 相等，再拷入隔离 executor；旧包或二进制不可悄悄替换。

## Windows 构建产物（本次不做运行验收）

仅构建 windows/amd64，禁止调用会遍历六平台的普通 `go run ./tools/package <out>`。新脚本调用固定 clean C 的现有 build-candidate，记录真实 Windows 构建收据、PE / Go build info、二进制和归档摘要。输入的 Mac release-manifest 来自同 C 的候选或最终包；其独立 SHA 须由主控核定。脚本逐项绑定 C、独立预期 T/version、source-lock、七份文档及已提交三 Profile Bundle 来源，不以 manifest 的 passed 或 stableReady 授予 Windows 资格。

```sh
python3 "$TOOLS/package-windows-artifact.py" --cli-root "$C_ROOT" --cli-commit "$C" --template-commit "$T" --template-version 4.0.2 --source-manifest "$MAC_MANIFEST" --source-manifest-sha256 "$MAC_MANIFEST_SHA" --out "$R/windows-artifact"
```

最终发行必须传固定 v4.0.2 的 T 与最终 C。内部维护 P2 试打包只能显式传其自身独立 SHA 和 `git:<S>` 版本，不能冒充最终来源。ZIP 保留普通生产打包器的 9 个条目、排序、Deflate、1980 时间及 binary 0755 / docs 0644。`release-manifest.json` 与独立 `build-only-checksums.json` 均明确 `stableReady:false`、`nativeRuntimeVerified:false`、`runtimeVerification:not-executed`。本次 Windows 使用方式为核对独立校验清单后下载、解压和使用，不声称已运行验证。

现有 online upgrade 要求当前平台已获 native 资格；Windows 不在本次 Mac-only requiredPlatforms，因此会安全拒绝，不能将 build-only 清单改名为 checksums.json 或与 Mac native 清单合并以绕过。

`.github/workflows/windows-native-release.yml` 保留历史原生验收工具，本次不 dispatch。其 native / installation / published-online 分支均不属于本次要求，也不将历史结果搬作本次资格。

## 发布后的 Mac 公开在线安装

只有正式 2.0.0 已发布且 Mac 公开包、校验/证明与独立预期已核对后，才执行这一段。`PUBLIC_SHA` 来自已经审核的最终发行资产清单，不能由待测归档或同一次远端清单自行设为预期。helper 会先核该独立 SHA，再要求包内 stableReady、完整三 Profile 来源与固定 native input 一致；候选包不能充作公开验收。

Mac 在保留原 native input 的证据根 R 下使用全新 public 与 public-installation 目录；公开下载不传认证信息：

```sh
mkdir "$R/public"
curl --fail --location --connect-timeout 15 --max-time 300 --retry 2 https://github.com/iloveZzz/yss-cli/releases/download/v2.0.0/yss_2.0.0_darwin_arm64.tar.gz --output "$R/public/yss_2.0.0_darwin_arm64.tar.gz"
python3 "$TOOLS/run-observed.py" "$R/public-program-runner.json" "$C_ROOT" "$SOURCE_ROOTS_JSON" python3 "$TOOLS/run-program-installation.py" --input "$R/native-input.json" --archive "$R/public/yss_2.0.0_darwin_arm64.tar.gz" --published-sha256 "$PUBLIC_SHA" --out "$R/public-installation" --platform darwin/arm64 --cli-root "$C_ROOT"
```

Mac helper 将公开包内已核验的 binary 写入 `public-installation/public-executor/`，其摘要必须等于独立 native input；使用该执行器对另一个 `online-tool/` 执行 `upgrade --to 2.0.0`，不传本地 archive，不改远端 URL，不使用网络替身。首次安装必须实际返回 installed，不能用 checked/unchanged 冒充。结果的 archiveSha256、安装 receipt 的 archiveDigest 都必须等于传入 PUBLIC_SHA；已装 binary/文档/manifest、version/commit、三 Bundle、只读 update status 一起核验。随后用公开执行器 rollback，重做一次 rollback 和两次 recover，核整个受控目录与用户只读文件恢复、无覆盖。

原离线5仍在 `cases`/`commands`，online 结果单独放 `public_upgrade`/`online_commands`，总 status 必须两者均通过。逐命令 stdout/stderr/退出/信号/超时/耗时均保留；网络失败或摘要漂移保留失败报告，不降级为离线通过。原始源和工具观察由 `run-observed.py` 提供。此工具补充没有执行真实公开安装，不声称 2.0.0 已存在或已验收；发布后失败须报告已发布但验收未完成。

## Mac 最终装配

只消费本次单 Mac native input 以及三个真实 gate 的当前原始报告；保留每项来源、日志与漂移校验。

```sh
python3 "$TOOLS/prepare-release-gates.py" --input "$R/native-input.json" --cli-root "$C_ROOT" --platform darwin/arm64 --program-report "$R/program-installation/report.json" --program-execution "$R/program-runner.json" --legacy-report "$R/legacy36/legacy-recovery.json" --legacy-execution "$R/legacy-runner.json" --scoped-report "$R/scoped-template/report.json" --scoped-execution "$R/scoped-runner.json" --out "$R/release-gates"
CGO_ENABLED=0 go -C "$C_ROOT" run ./tools/package --native-manifest "$R/release-input.json" --required-platforms darwin/arm64 "$R/release-final"
```

固定生产 CLI 的现役 assembler 仍以独立 Expected、平台集合、来源、文档、Bundle、日志及漂移校验裁决 Mac 资格。Windows ZIP 和 build-only-checksums 单独发布，不进入该装配。工具准备、语法编译、参数拒绝、交叉构建和内部 P2 试包不产生平台运行资格；失败保留原日志并停止受影响产物。本文步骤不代表已执行提交、发布、全局安装或产品项目迁移。
