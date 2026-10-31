# 固定原生资产装配

装配器默认资格范围为 `local-platform-impacted-consumers`；当前发行证据流程显式选择下述 `local-platform-program-install`，独立验收平台为 `darwin/arm64`。六平台仍是支持列表；装配不会隐式要求其他平台运行。

```sh
go run ./tools/package --native-candidate-manifest INPUT.json --required-platforms darwin/arm64 NEW_CANDIDATE_DIR
go run ./tools/package --native-manifest INPUT.json --required-platforms darwin/arm64 NEW_RELEASE_DIR
```

两条命令都要求当前 CLI 来源仓 clean 且 committed。`release.Input` schema v1 包含完整 `Identity`、七个分发文档、明确 `requiredPlatforms`、`qualificationScope`、所选平台的原始 binary 和 native receipt。库调用者必须独立提供 `Expected` 的身份、文档、平台及资格范围；输入不能自行裁剪。所有 `FileRef`（含原收据的依赖）相对最外层 INPUT 目录，须绑定 raw SHA-256；非普通文件、符号链、路径逃逸、输入漂移和既有输出拒绝。

## 本机程序安装范围（`local-platform-program-install`）

2.3.1 起新增更窄的资格范围，用 `go run ./tools/package --native-manifest INPUT.json --qualification-scope local-platform-program-install --required-platforms darwin/arm64 NEW_RELEASE_DIR` 选择；默认仍是 `local-platform-impacted-consumers`。该范围只要求：

* 每个所选平台的原生 receipt 含同 binary 的 version envelope 与 固定来源锁中所有现役 Profile 的 bundle inspect 原始 envelope，`checks` 为空数组（不要求 `native-smoke`、`native-recovery`、`plugin-native-smoke`）；
* 同源 `releaseGate` 的唯一 check 为 `program-installation`，沿用上文的四个固定 case、候选归档物理 SHA、两个不同工具目录的公开 `update plan/apply` 实际执行与 payload 摘要校验。

该范围**不覆盖**模板消费者检查、原生命令面 smoke、适用事务的中断恢复矩阵与插件 smoke。装配只校验这些证据的真实性与一致性，不因范围收窄放宽单项校验；发行说明必须列出未覆盖项。其余平台、固定文档、来源锁与二进制身份校验不变。

固定来源锁只接受 schema 5，逐 Profile 绑定受支持的稳定模板版本和完整提交。重发前的同名提交、更早产品版本和不受信来源均拒绝；版本号不能替代来源身份。装配继续核验独立 `Expected`、固定文档原字节及摘要、实际 native inspect 和输入漂移。

每个平台必须保留同 binary 的实际 version envelope、`versionCommand`、spec/backend 两个 `bundleCommands` 原始 inspect envelope、开始/结束 SHA、`platform == runtimePlatform`、native-runner 身份、退出 0、无中断、无漂移及空 `unexecuted`。三项 native 检查完整消费现役 `native-smoke` 命令集合、当前平台的 `native-recovery` 矩阵和 `plugin-native-smoke` 的当前 label multiset。后端插件 case 的 identity/binding、只读业务 bytes/mode、预览、整体回退、重复恢复及物理日志摘要必须完整。原插件跨范围 deferred 项保持原样，不冒充已执行。

候选包不读取 `releaseGate`，仍完整校验上述 native 接受证据和所有 payload。它输出 `stableReady: false`、`runtimeVerification: passed`，不携五项稳定证明字段。用于实际公开 `yss update plan/apply` 验证，不能作为发行通过证据。

正式包要求同源 `releaseGate` receipt，对于 `local-platform-impacted-consumers`，资格范围相同且两项 check 完整实际通过：`program-installation`、`scoped-template-consumers`。每项原 report 是严格 `GateExecution` schema v1 / kind `stable-release-gate-execution`，绑定 Identity、平台、binary、资格范围、固定 `RequiredGateCoverage`、显式 `signal: null` / `error: null`、真实退出 0、无漂移、空未执行集合以及完整 `sourceReports` 原字节依赖。

* 程序安装来源是 schema 1 / kind `native-program-installation`：四个固定 case，两个独立工具目录实际执行 update plan/apply 及成功 envelope/log SHA、候选归档物理 SHA、安装 payload 摘要和业务模式保护。`PayloadSHA256` 对 binary 与七文档按 ref 排序，累积 `ref + NUL + rawSHA + NUL + decimalMode + LF`；不含会加入证明的 `release-manifest.json`。候选和正式包的 binary/docs/full inspect 保持一致。
* 模板消费者来源是 schema 1 / kind `scoped-template-consumer-verification`，只验证九个固定受影响检查。保留原 array 的实际命令、source SHA、cwd、退出和 combined log；通过 spec/backend 两个固定来源及 clean 证明核验复用。它不声称完整模板或真实项目迁移已执行。

九项按以下顺序绑定精确命令。`profiles.projection` 验证现役组合目录和无投影边界；`handoff.source-lock` 验证当前 Bundle 来源、字节与封装所有权，不再执行已退役的工具锁脚本。本地化后的设计技能由 `skills.source-contract` 验证锁读取、实例漂移及 canonical 登记边界，不再声称具有旧 design-agent 上游权威。调用方须按当前 `RequiredGateCoverage` 生成真实执行数组与日志，旧 `skills.upstream-source` 报告或旧命令不能换名复用。

| 固定 ID | 命令 |
| --- | --- |
| `skills.projection` | `scripts/sync-skills --check` |
| `skills.lock` | `scripts/update-skill-lock --check` |
| `profiles.projection` | `node --test --test-concurrency=1 tests/profile-tree.test.mjs tests/advance-maintenance-root.test.mjs` |
| `handoff.source-lock` | `node --test --test-concurrency=1 tests/verification-artifacts.test.mjs tests/strategic-handoff-sealed-owner.test.mjs` |
| `lifecycle.registry` | `scripts/verify-lifecycle-registry` |
| `skills.registry` | `scripts/verify-skill-registry` |
| `affected-source-contract-tests` | `node --test --test-concurrency=1 tests/read-only-intake.test.mjs tests/verification-execution.test.mjs tests/instance-metadata.test.mjs .template-source/tooling/node/test/retirement.test.mjs` |
| `source.whitespace` | `git diff --check` |
| `skills.source-contract` | `node --test --test-concurrency=1 tests/skill-lock-readers.test.mjs .template-source/tooling/node/test/skill-supply-chain.test.mjs` |

正式包输出完整原 inspect `.result`，并绑定 `sourceLockSha256`、`requiredPlatforms`、`supportedPlatforms`、`nativeReceiptSha256`、`releaseGateSha256` 五项证明。归档模式 binary 0755 / 文档 0644，固定时间保持可重复；写入前后复核所有已消费输入，排他创建输出。默认原交叉编译入口仍 `stableReady: false`。工具不执行发布、批准或凭哈希证明报告作者真实性。

未来完整模板模式需显式 `--gate-basis`，资格范围 `complete-template-release`，独立 BEFORE 编译计划、调用与输入摘要，并保留完整模板与 Spec/Backend 两个 CLI 集成的独立原始来源。此模式不是本轮默认前置。

本仓保留轻量 Go 测试和发行工具离线回归，范围见[测试与 CI 状态](../../docs/ci-verification.md)。这些测试不生成正式发行回执；当前 `local-platform-program-install` 证据由下述发行脚本在真实目标平台生成，其他资格范围仍须提供其完整证据。构建检查可使用 `CGO_ENABLED=0 go build ./tools/package ./tools/release`，构建成功不代表原生平台运行验收。

发版证据（原生 receipt、程序安装、门禁、Windows 仅构建包及公开在线升级验证）的生成脚本与完整执行顺序见 [tools/release-evidence](../release-evidence/README.md)。
