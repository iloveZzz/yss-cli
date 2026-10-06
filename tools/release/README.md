# 固定原生资产装配

`go run ./tools/package --native-manifest <manifest.json> <仓库外新目录>` 复用六个原生 runner 已验证的二进制字节，不重新构建，不发布。原 `go run ./tools/package <仓库外新目录>` 仍交叉编译，并保持 `stableReady: false`。

输入以 `release.Input` 为 schema v1：固定稳定 CLI 版本、协议、完整提交、`sourceState: committed`、源锁 SHA、四 Profile 的 `BundleIdentity`、七个分发文档、六个 `Artifact`、完整发行 `releaseGate`。CLI 入口要求当前来源仓 clean，输入身份和文档必须与当前源码及内嵌 Bundle 一致。所有 `FileRef.path`，包括收据内部的路径，统一相对最外层 manifest 的目录；所有文件需 SHA-256，符号链、绝对路径和路径逃逸被拒绝。

每个平台收据为 `release.Receipt`：同一身份、`platform == runtimePlatform`、`runtimeVerification: native-runner`、二进制开始/结束 SHA、真实 `version --json` 原始 envelope、`status: passed`、观测退出 0、`inputDrift: false` 和非 null 的空 `unexecuted`。必需 `checks` 为 `native-smoke`、`native-recovery`、`plugin-native-smoke`，每项包含原 report 和 stdout/stderr 的路径、SHA 及实际成功状态。CI 的 `versionCommand`、四 `bundleCommands` 和 GitHub run 标识可登记；登记的命令必须通过并与原始 envelope 完全一致。

装配检查当前 native-smoke 的完整 82 命令集合；恢复检查 Unix 的四 Profile × cancel/terminate/kill，Windows 的四 Profile × cancel/kill，必须观察目标写入后中断并验证整体、重复恢复。插件检查两 Profile 的完整成功、输入及二进制保护和逐命令日志。插件报告的跨范围 deferred 项保持原样，不能作为本平台的必需项通过证据。

发行门禁收据需同源且四项完整通过：`full-template-integration`、`cli-integration`、`legacy-recovery`、`real-project-isolation`。原始门禁 report 必须包含实际成功状态或退出码，显式失败、漂移和未执行均拒绝。门禁收据由外部发布验证者生成；工具校验绑定和完整性，不代替独立审查或授权，也不能凭文件哈希证明报告作者的真实性。

通过后输出四 tar.gz、两 zip 和 `checksums.json`，二进制归档权限为 0755，文档为 0644，时间固定以保持重复摘要。完整输入在写入前后复核；输出目录和文件均排他创建，失败不会覆盖已有资产，`checksums.json` 最后写入。遇到失败时保留原证据，只有实际装配退出 0 的完整输出可以进入后续发行核算。

定向验证：`go test ./tools/package ./tools/release -count=1 -timeout 5m`。测试中的小 Go 二进制是交叉编译的格式解析夹具，不是六平台实际运行验收。
