# 2.0.0 双平台原生发行工具（仓外）

仅准备工具，尚未取得本轮原生或发行资格。实际运行前，独立审查工具，将本目录提交为固定工具 SHA（W），并由主控冻结 clean CLI 2.0.0 提交 C、带 v4.0.2 tag 的模板提交 T、三 Profile Bundle/source-lock、二进制摘要。所有来源 checkout 与运行目录分离；禁止 native override。`source-lineage.json` 记录固定 Git/历史仓外来源与原始摘要。historical/ 只作可审计参考，不能运行旧版本硬编码工具。

## 精确范围

- 每个平台：native-smoke 62 命令；native-recovery Mac 9 / Windows 6 个真实目标变更后中断；plugin-native-smoke 原有 44 label、2 个 plugin case。
- 两个平台都实际运行安装 5 cases。Windows 仅观测 owner-write/read-only 属性与归档规范模式，未执行 NTFS ACL。
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

`.github/workflows/windows-native-release.yml` 只在显式 workflow_dispatch 时执行。push 分支事件仅用于工具分支 workflow 注册，job 跳过。需审查后单独推送固定工具分支；本轮未 push、未 dispatch，也未证明新 workflow 已注册成功。CLI 与模板 checkout 均用显式完整 C/T，工具 checkout 用 W，且 W 必须等于实际 workflow SHA。

首次传 cli_commit/template_commit/tools_commit，在 windows-latest 实际构建、跑全部三个 native 检查，经当前 `go run ./tools/package --native-candidate-manifest --required-platforms windows/amd64` 校验后，真实运行安装5并上传原始 evidence。不能用 Mac 的 Windows 交叉编译输出充作这里的执行结果。

发布后的安装回读传相同 C/T/W、此前成功 native run 的 native_run_id 以及正式 Windows zip 的已独立确认 published_sha256；下载并核 SHA 后使用该 run 的同源 input/binary 实际执行安装5。若旧 artifact 不可用或同源绑定不符，拒绝；不伪造或重新标记通过。

## 双平台装配

保留 Windows artifact 原目录与摘要，向共同证据根导入 `build-windows/`、`native-windows-amd64/`；原 Windows installation/report 与 candidate/public 包另存完整原根，不覆盖 Mac 的同名文件。所有 native receipt 内相对依赖仍以共同输入目录为根，拷贝后核全部原 SHA。

```sh
python3 "$TOOLS/prepare-native-input.py" --cli-root "$C_ROOT" --cli-commit "$C" --template-commit "$T" --evidence-root "$R" --artifact darwin/arm64 build-darwin/yss native-darwin-arm64/native-receipt.json --artifact windows/amd64 build-windows/yss.exe native-windows-amd64/native-receipt.json --output-name native-input-dual.json
python3 "$TOOLS/prepare-release-gates.py" --input "$R/native-input-dual.json" --cli-root "$C_ROOT" --platform darwin/arm64 --program-report "$R/program-installation/report.json" --program-execution "$R/program-runner.json" --legacy-report "$R/legacy36/legacy-recovery.json" --legacy-execution "$R/legacy-runner.json" --scoped-report "$R/scoped-template/report.json" --scoped-execution "$R/scoped-runner.json" --out "$R/release-gates"
CGO_ENABLED=0 go -C "$C_ROOT" run ./tools/package --native-manifest "$R/release-input.json" --required-platforms darwin/arm64,windows/amd64 "$R/release-final"
```

最后仍由固定生产 CLI 的现役 assembler 独立 Expected/平台集合/来源/文档/Bundle/日志/漂移校验裁决。工具准备、语法编译、输入拒绝和历史来源核对不产生平台资格，任何失败保留原日志并中止受影响装配。未执行提交、发布、全局安装或产品项目迁移。
