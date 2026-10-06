# 固定原生资产装配

本轮稳定范围是 `local-platform-impacted-consumers`，独立平台范围为本机 `darwin/arm64`。六平台仍是支持列表；装配不会隐式要求其他平台运行。

```sh
go run ./tools/package --native-candidate-manifest INPUT.json --required-platforms darwin/arm64 NEW_CANDIDATE_DIR
go run ./tools/package --native-manifest INPUT.json --required-platforms darwin/arm64 NEW_RELEASE_DIR
```

两条命令都要求当前 CLI 来源仓 clean 且 committed。`release.Input` schema v1 包含完整 `Identity`、七个分发文档、明确 `requiredPlatforms`、`qualificationScope`、所选平台的原始 binary 和 native receipt。库调用者必须独立提供 `Expected` 的身份、文档、平台及资格范围；输入不能自行裁剪。所有 `FileRef`（含原收据的依赖）相对最外层 INPUT 目录，须绑定 raw SHA-256；非普通文件、符号链、路径逃逸、输入漂移和既有输出拒绝。

每个平台必须保留同 binary 的实际 version envelope、`versionCommand`、四个 `bundleCommands` 原始 inspect envelope、开始/结束 SHA、`platform == runtimePlatform`、native-runner 身份、退出 0、无中断、无漂移及空 `unexecuted`。三项 native 检查完整消费 `native-smoke` 的 82 条命令、当前平台的 `native-recovery` 矩阵和 `plugin-native-smoke` 的 44 条 label multiset。插件两 case 的 identity/binding、只读业务 bytes/mode、预览、整体回退、重复恢复及物理日志摘要必须完整。原插件跨范围 deferred 项保持原样，不冒充已执行。

候选包不读取 `releaseGate`，仍完整校验上述 native 接受证据和所有 payload。它输出 `stableReady: false`、`runtimeVerification: passed`，不携五项稳定证明字段。用于实际公开 `yss update plan/apply/rollback/recover` 验证，不能作为发行通过证据。

正式包要求同源 `releaseGate` receipt，资格范围相同且三项 check 完整实际通过：`program-installation`、`legacy-recovery`、`scoped-template-consumers`。每项原 report 是严格 `GateExecution` schema v1 / kind `stable-release-gate-execution`，绑定 Identity、平台、binary、资格范围、固定 `RequiredGateCoverage`、显式 `signal: null` / `error: null`、真实退出 0、无漂移、空未执行集合以及完整 `sourceReports` 原字节依赖。

* 程序安装来源是 schema 1 / kind `native-program-installation`：五个固定 case，真实 update 四行动及成功 envelope/log SHA、候选归档物理 SHA、安装 payload 摘要和业务模式保护。`PayloadSHA256` 对 binary 与七文档按 ref 排序，累积 `ref + NUL + rawSHA + NUL + decimalMode + LF`；不含会加入证明的 `release-manifest.json`。候选和正式包的 binary/docs/full inspect 保持一致。
* 旧实例恢复保留 schema 1 / kind `native-legacy-recovery-matrix` 原始报告，四个固定旧公开包、四 Profile × 原 12 case 的 48 项、实际日志、精确 `MIGRATION_REQUIRED` / `LEGACY_INTERRUPTED` / `CONCURRENT`，拒绝 fixture、自述成功、缺项或错误码混淆。旧 tgz 和逐 case 物理日志作为 schema 0 `evidence-file` 依赖保留。
* 模板消费者来源是 schema 1 / kind `scoped-template-consumer-verification`，只验证九个固定受影响检查。保留原 array 的实际命令、source SHA、cwd、退出和 combined log；通过当前四固定来源及 clean 证明核验复用。它不声称完整模板或真实项目迁移已执行。

正式包输出完整原 inspect `.result`，并绑定 `sourceLockSha256`、`requiredPlatforms`、`supportedPlatforms`、`nativeReceiptSha256`、`releaseGateSha256` 五项证明。归档模式 binary 0755 / 文档 0644，固定时间保持可重复；写入前后复核所有已消费输入，排他创建输出。默认原交叉编译入口仍 `stableReady: false`。工具不执行发布、批准或凭哈希证明报告作者真实性。

未来完整模板模式需显式 `--gate-basis`，资格范围 `complete-template-release`，独立 BEFORE 编译计划、调用与输入摘要，并保留原完整模板/四 CLI/旧恢复/真实实例来源。此模式不是本轮默认前置。

定向验证：`go test ./tools/package ./tools/release -count=1 -timeout 5m`。测试小 binary 为格式解析夹具，不能称原生平台运行证据。
