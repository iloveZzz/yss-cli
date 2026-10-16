# yss-cli 开发约定

本工程是普通 Go CLI 源码仓，路径相对本仓根，使用简体中文维护文档。本地维护不要求产品实例的 yss-project.yaml、CONTEXT.md、阶段 checkpoint 或 Slice，不补造这些文件。父级 YSS 产品路由不定义本仓流程；共同授权、用户工作保护与允许写范围继续有效。模板治理资产继续来自固定来源快照；不在本工程重新定义生命周期门禁、批准或业务规则。

Go 代码使用 gofmt。内嵌 Bundle 归档不入库，编译前先运行 `node tools/fetch-bundle.mjs`（摘要见 `internal/bundle/assets/bundles.json.gz.sha256`，刷新固定来源时用 `node tools/artifacts.mjs generate --scope all` 更新）。维护时使用 `CGO_ENABLED=0 go build ./...` 检查构建；构建成功只证明可编译，不能称为测试通过或正式发行验收通过。

行为改动须有 Go 测试：`go test -timeout 5m ./...`（无 Bundle 时加 `-tags yss_nobundle` 跑纯逻辑测试）。测试必须轻量快速：单个测试约 1 秒内、单个包 30 秒内、全量 5 分钟硬上限；只用 `t.TempDir()` 与小型内联数据，不访问网络，不引入差分回放、预言机录制、基准或长跑恢复类测试。实现仓路由用例需要 Git 2.44+，CI 以 `YSS_TEST_REQUIRE_GIT=1` 禁止跳过。

CI 另有：`gofmt -l` 与 `go vet -composites=false ./...`（关闭 composites 是因 `internal/governance` 既有 unkeyed 字面量告警）；`node tools/smoke/main.mjs --bin <二进制>` 对已构建二进制做黑盒冒烟，只断言主路径退出码与 envelope 状态。测试与冒烟通过都不代表正式发行验收通过，也不生成验收回执。详见 [测试与 CI 状态](docs/ci-verification.md)。

CLI 的业务校验、事务恢复、来源与摘要检查，以及发行包证据校验属于产品和装配能力，继续保留。发行工具仍消费既有证据合同；删除测试不产生通过回执，也不授予发行资格。真实证据仅在源码、工具、参数、环境及所有输入一致时可复用；未执行、取消与失败如实记录。发布构建 CGO_ENABLED=0。程序版本、协议版本、各 Profile 模板来源和旧 CLI 兼容版本分别记录。不得把交叉编译作为平台运行验收。

提交、推送、正式发布按当前会话的用户授权执行。未覆盖的能力明确返回未迁移状态，不得回退成放宽校验或自动批准。
