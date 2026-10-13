# 测试与 CI 状态

本仓已移除全部自动化测试源码及对应测试 CI：

- Go 的单元、集成、差分、恢复、生命周期和基准测试，以及 Node 测试。
- 测试专用 `testdata`、预言机录制与 fixture 适配工具。
- 变更验证选择器、分片调度、测试执行与报告工具。
- 原生冒烟、恢复、插件验收、验收回执及性能测试工具。
- `Native verification` workflow 和测试环境准备 action，包括 `prepare`、全部 `verify` 分片与最终 `native` job。

当前仓库没有自动测试 workflow。CLI 运行时的治理校验、来源与摘要检查、事务恢复及内嵌模板属于产品能力，仍然保留。兼容桥、Bundle／帮助生成和打包工具也保留。

`Build and generated artifacts` workflow 只执行生产包构建、候选来源记录，以及从固定 Git 来源重建 Bundle → 帮助视图 → 完整帮助的逐字节检查。不执行 Go/Node 测试，不恢复原生验收工具，也不生成正式发行通过回执。每次运行均检查全部生成链，避免生成器传递依赖或工具链变化漏检。性能与兼容性证据在仓库外维护，见 [性能合同](artifact-performance.md)。

## 本地构建

工程使用 Go 1.27.1：

```sh
CGO_ENABLED=0 go build ./...
CGO_ENABLED=0 go build -trimpath -o bin/yss ./cmd/yss
```

构建成功只证明源码能够编译，不代表功能测试或正式发行验收通过。历史测试日志、兼容观察和固定来源清单保留为历史记录，不代表当前版本通过测试。

## GitHub 与发行衔接

本改动进入目标分支后，该分支不再通过已删除的 workflow 启动测试。已经排队或运行的 Actions 不会因本地删除文件自动取消。

如果分支保护或 ruleset 仍要求 `native` 检查，仓库管理员需要移除该必需检查；删除 workflow 不会自动修改仓库设置。

发行装配工具仍要求原有真实证据，产品安装与升级继续校验发行资格。当前仓库已无自动生成这些验收回执的测试工具；需要正式装配时，必须另行提供符合现有合同的证据。删除测试不等同于跳过装配校验或生成通过结果。

## 未提交模板的本地装配

`tools/bundle` 接受来源锁 v4，用于模板维护的本地验收。三个现役 Profile 与存在时的共享技能来源均须声明 `sourceState: working-tree`、基线 `templateCommit` 和已捕获的 Git `sourceTree`，Profile 的 `templateVersion` 为 `worktree:<sourceTree>`，producer 同样为 working-tree。捕获树时使用独立 `GIT_INDEX_FILE`；不修改使用者暂存区、不创建提交。组合锁仍必须匹配 canonical 配置摘要与技能内容摘要。

本地锁保存为 `docs/source-lock.working-tree.json`，使用 `go run ./tools/bundle --source-root <Spec 根仓> --lock docs/source-lock.working-tree.json --out internal/bundle/assets` 重建嵌入资产。旧 v2/v3 锁仍只接受提交来源，v4 不支持混合来源。正式来源锁 `docs/source-lock.json` 保留原固定提交；发布工具继续拒绝 working-tree 包。获得提交授权后，按既有固定来源刷新流程重建正式锁与资产，不能只改状态字段。
