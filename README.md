# 2.0.0 双平台原生发行工具（仓外）

仅准备工具，尚未取得本轮原生或发行资格。实际运行前，独立审查工具，将本目录提交为固定工具 SHA（W），并由主控冻结 clean CLI 2.0.0 提交 C、带 v4.0.2 tag 的模板提交 T、三 Profile Bundle/source-lock、二进制摘要。所有来源 checkout 与运行目录分离；禁止 native override。`source-lineage.json` 记录固定 Git/历史仓外来源与原始摘要。historical/ 只作可审计参考，不能运行旧版本硬编码工具。

首个工具提交 `07bd997dd3a73a0f52231ef8636a86e7eeb14988` 已冻结推送并登记 workflow，尚未执行本轮 native。发布后 online 验收补充须独立审查并冻结为后继 W2；本文执行时的 W 必须取主控确认的最终工具提交，不沿用修改前的 hash。

## 精确范围

- 每个平台：native-smoke 62 命令；native-recovery Mac 9 / Windows 6 个真实目标变更后中断；plugin-native-smoke 原有 44 label、2 个 plugin case。
- 两个平台都实际运行安装 5 cases。Windows 仅观测 owner-write/read-only 属性与归档规范模式，未执行 NTFS ACL。
- 发布后两平台另以公开归档内执行器真实运行 `upgrade --to 2.0.0 --tool-root <隔离目录> --json`。同一次 helper 调用先保留离线安装5，再记录独立 online 命令、安装收据/状态、精确归档摘要、回滚与重复恢复；不重跑 native3、legacy36 或 scoped9。
- Mac 一次 legacy36：3 Profile × 12 case，保留真实旧包、SIGKILL、MIGRATION_REQUIRED / LEGACY_INTERRUPTED / CONCURRENT 与真实恢复，不制造 journal。
- scoped9 逐条使用当前 tools/release/local_evidence.go 的命令。其 report 的 reused=true 仅表示包装本次新执行的 original-execution 数组，绝不复用历史通过资格。
- 正式 local scope gate 绑定 Mac 的 installation5、legacy36、scoped9；Windows installation5 独立保留为实际平台安装证据。两平台 native receipt 均由当前 assembler 消费，不换用旧 Windows qualification 聚合器。

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

## Windows

`.github/workflows/windows-native-release.yml` 只在显式 workflow_dispatch 时执行。push 分支事件仅用于工具分支 workflow 注册，job 跳过。首个 W 已登记；W2 仍待独立审查、提交和推送，本次补充不 dispatch。CLI 与模板 checkout 均用显式完整 C/T，工具 checkout 用 W，且 W 必须等于实际 workflow SHA。

首次传 cli_commit/template_commit/tools_commit，在 windows-latest 实际构建、跑全部三个 native 检查，经当前 `go run ./tools/package --native-candidate-manifest --required-platforms windows/amd64` 校验后，真实运行安装5并上传原始 evidence。不能用 Mac 的 Windows 交叉编译输出充作这里的执行结果。

发布后的安装回读传相同 C/T/W、此前成功 native run 的 native_run_id 以及正式 Windows zip 的已独立确认 published_sha256；下载并核 SHA 后，用该 run 的同源 input/binary 实际执行安装5，同时传 `--published-sha256`，从该公开 zip 解出独立执行器并运行真正 online upgrade。若旧 artifact 不可用或同源绑定不符，拒绝；不伪造或重新标记通过。候选分支不传该选项，保留原离线5场景。

## 发布后的公开在线安装（两平台必做）

只有正式 2.0.0 已发布且双平台公开包、校验/证明与独立预期已核对后，才执行这一段。`PUBLIC_SHA` 来自已经审核的最终发行资产清单，不能由待测归档或同一次远端清单自行设为预期。helper 会先核该独立 SHA，再要求包内 stableReady、完整三 Profile 来源与固定 native input 一致；候选包不能充作公开验收。

Mac 在保留原 native input 的证据根 R 下使用全新 public 与 public-installation 目录；公开下载不传认证信息：

```sh
mkdir "$R/public"
curl --fail --location --connect-timeout 15 --max-time 300 --retry 2 https://github.com/iloveZzz/yss-cli/releases/download/v2.0.0/yss_2.0.0_darwin_arm64.tar.gz --output "$R/public/yss_2.0.0_darwin_arm64.tar.gz"
python3 "$TOOLS/run-observed.py" "$R/public-program-runner.json" "$C_ROOT" "$SOURCE_ROOTS_JSON" python3 "$TOOLS/run-program-installation.py" --input "$R/native-input.json" --archive "$R/public/yss_2.0.0_darwin_arm64.tar.gz" --published-sha256 "$PUBLIC_SHA" --out "$R/public-installation" --platform darwin/arm64 --cli-root "$C_ROOT"
```

Windows 使用上述 published workflow 分支。两平台 helper 将公开包内已核验的 binary 写入 `public-installation/public-executor/`，其摘要必须等于独立 native input；使用该执行器对另一个 `online-tool/` 执行 `upgrade --to 2.0.0`，不传本地 archive，不改远端 URL，不使用网络替身。首次安装必须实际返回 installed，不能用 checked/unchanged 冒充。结果的 archiveSha256、安装 receipt 的 archiveDigest 都必须等于传入 PUBLIC_SHA；已装 binary/文档/manifest、version/commit、三 Bundle、只读 update status 一起核验。随后用公开执行器 rollback，重做一次 rollback 和两次 recover，核整个受控目录与用户只读文件恢复、无覆盖。

原离线5仍在 `cases`/`commands`，online 结果单独放 `public_upgrade`/`online_commands`，总 status 必须两者均通过。逐命令 stdout/stderr/退出/信号/超时/耗时均保留；网络失败或摘要漂移保留失败报告，不降级为离线通过。原始源和工具观察仍由 `run-observed.py`/固定 Windows workflow 提供。此工具补充没有执行真实公开安装，不声称 2.0.0 已存在或已验收；发布后失败须报告已发布但验收未完成。

## 双平台装配

保留 Windows artifact 原目录与摘要，向共同证据根导入 `build-windows/`、`native-windows-amd64/`；原 Windows installation/report 与 candidate/public 包另存完整原根，不覆盖 Mac 的同名文件。所有 native receipt 内相对依赖仍以共同输入目录为根，拷贝后核全部原 SHA。

```sh
python3 "$TOOLS/prepare-native-input.py" --cli-root "$C_ROOT" --cli-commit "$C" --template-commit "$T" --evidence-root "$R" --artifact darwin/arm64 build-darwin/yss native-darwin-arm64/native-receipt.json --artifact windows/amd64 build-windows/yss.exe native-windows-amd64/native-receipt.json --output-name native-input-dual.json
python3 "$TOOLS/prepare-release-gates.py" --input "$R/native-input-dual.json" --cli-root "$C_ROOT" --platform darwin/arm64 --program-report "$R/program-installation/report.json" --program-execution "$R/program-runner.json" --legacy-report "$R/legacy36/legacy-recovery.json" --legacy-execution "$R/legacy-runner.json" --scoped-report "$R/scoped-template/report.json" --scoped-execution "$R/scoped-runner.json" --out "$R/release-gates"
CGO_ENABLED=0 go -C "$C_ROOT" run ./tools/package --native-manifest "$R/release-input.json" --required-platforms darwin/arm64,windows/amd64 "$R/release-final"
```

最后仍由固定生产 CLI 的现役 assembler 独立 Expected/平台集合/来源/文档/Bundle/日志/漂移校验裁决。工具准备、语法编译、输入拒绝和历史来源核对不产生平台资格，任何失败保留原日志并中止受影响装配。未执行提交、发布、全局安装或产品项目迁移。
