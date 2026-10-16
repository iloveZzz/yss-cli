# 测试与 CI 状态

本仓已移除全部自动化测试源码及对应测试 CI：

- Go 的单元、集成、差分、恢复、生命周期和基准测试，以及 Node 测试。
- 测试专用 `testdata`、预言机录制与 fixture 适配工具。
- 变更验证选择器、分片调度、测试执行与报告工具。
- 原生冒烟、恢复、插件验收、验收回执及性能测试工具。
- `Native verification` workflow 和测试环境准备 action，包括 `prepare`、全部 `verify` 分片与最终 `native` job。

当前仓库没有自动测试 workflow，也没有恢复上述任何一类测试。CLI 运行时的治理校验、来源与摘要检查、事务恢复及内嵌模板属于产品能力，仍然保留。兼容桥、Bundle／帮助生成和打包工具也保留。

`Build and generated artifacts` workflow 执行生产包构建、候选来源记录、格式／静态检查、构建后黑盒冒烟，以及从固定 Git 来源重建 Bundle → 帮助视图 → 完整帮助的逐字节检查。不执行 Go/Node 测试，不恢复原生验收工具，也不生成正式发行通过回执。每次运行均检查全部生成链，避免生成器传递依赖或工具链变化漏检。性能与兼容性证据在仓库外维护，见 [性能合同](artifact-performance.md)。

## 格式与静态检查

workflow 在 Bundle 归档生成后运行：

- `gofmt -l .`：有任何输出即失败，列出未格式化的文件。
- `go vet -composites=false ./...`：关闭 `composites` 分析器，其余分析器保留。`internal/governance` 存在既有的 “struct literal uses unkeyed fields” 告警（`contracts.ContractPlanEntry`、`backendAuthorization`、`kernel.Binding`、`kernel.TrackingEntry`），为了不为通过 vet 大规模改动业务代码而暂不纳入。清理这些字面量后应去掉该开关。

这两项只发现格式和常见静态错误，不验证行为。

## 构建后黑盒冒烟

`tools/smoke/` 是对已构建 `yss` 二进制的黑盒冒烟脚本（Node，无第三方依赖），**不是**单元测试、集成测试或发行验收：

```sh
node tools/fetch-bundle.mjs
CGO_ENABLED=0 go build -o /仓库外/路径/yss ./cmd/yss
node tools/smoke/main.mjs --bin /仓库外/路径/yss [--only profiles|git] [--keep]
```

覆盖范围：

- 对 spec、design、backend 三个 Profile：`init --plan/--apply`、`doctor`、`diff`、`context verify`、`skills list`、`assets list`、`recover`、`rollback`（查询与 `--apply`）、`sync --plan/--apply`；断言退出码为 0 且 envelope `status=ok`、`code=OK`，以及少量结果字段；回退后确认项目身份已移除。
- `yss git`：在临时仓库和本地裸远端（`file://`）上走 `branch start`、`commit`（只提交列出路径并加 Ticket trailer，已安装钩子拒绝缺 trailer 的普通提交）、`hooks install`、`policy set`、`audit`、`release tag`、`push --plan/--apply`；`--confirm-remote` 缺失或不符、受保护分支 push／commit 被拒绝；计划阶段不触达远端。

约束：

- 全部状态位于 `mktemp` 后经 `realpath` 的临时目录，计划文件在被测仓库之外；被测二进制以 `yss` 名称置于 `PATH` 最前，供钩子调用。
- `GIT_CONFIG_GLOBAL` 指向临时文件、`GIT_CONFIG_NOSYSTEM=1`、`HOME` 也在临时目录。`yss` 会清除全部 `GIT_*` 变量并按 `HOME` 读取全局配置，所以 `GIT_CONFIG_GLOBAL` 与 `$HOME/.gitconfig` 为同一文件。不触碰用户的全局或系统 git 配置。
- 任一断言失败即以非 0 退出，并保留临时目录供排查（`--keep` 可在成功时也保留）。
- CI 只在 ubuntu 运行；本地（如 macOS）运行不构成其他平台的运行验收，也不构成发行验收。
- 不含 fixture、预言机、录制或回执；通过只说明主路径在当前构建上能走通，不代表测试通过，也不替代原生、恢复、插件或安装等发行门禁。新增检查须保持这个范围，不要扩写成测试套件。

## scaffold 对拍

`tools/scaffold-parity/` 把 `yss scaffold ddd|mvc` 的 Go 实现与模板仓固定提交中的 Node 生成器逐字节对拍（文件集合、SHA-256、权限位、符号链接、空目录），场景矩阵和登记差异见该目录 README，设计见 [scaffold-go.md](scaffold-go.md)：

```sh
node tools/fetch-bundle.mjs
CGO_ENABLED=0 go build -o /仓库外/路径/yss ./cmd/yss
node tools/scaffold-parity/main.mjs --bin /仓库外/路径/yss --template-root /仓库外/固定提交的模板仓
```

它是构建后黑盒校验，**不是**单元测试或发行验收，不进入发行包。Node（以及正式合同场景所需的 Python `jsonschema`/`referencing`）只是对拍基准的运行条件，`yss` 运行时不依赖它们。CI 里它是独立工作流 `.github/workflows/scaffold-parity.yml` 的单个 job，只在 ubuntu 运行，失败不影响 `artifacts.yml` 的构建与发行包。

## 本地构建

工程使用 Go 1.27.1：

```sh
node tools/fetch-bundle.mjs   # 内嵌归档不入库，编译前先生成
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
