# 测试与 CI 状态

<!-- YSS_CURRENT_STATUS_START -->
当前源码版本：`2.8.3`。固定模板：backend `v4.8.3`、spec `v4.8.3`。现役 Profile：`backend`、`spec`。协议版本：`1`。
<!-- YSS_CURRENT_STATUS_END -->

## Go 测试

本仓曾移除全部自动化测试；现以轻量 Go 测试重新建立回归保障。历史上的差分、预言机录制、原生验收回执、分片调度与性能测试**不恢复**。

```sh
go test -tags yss_nobundle -timeout 5m ./...          # 纯逻辑测试，无需内嵌 Bundle
node tools/fetch-bundle.mjs && go test -timeout 5m ./...  # 含用内嵌 Bundle 初始化真实实例的集成用例
```

约束：

- 全量硬上限 5 分钟（`-timeout 5m`，CI 步骤 `timeout-minutes: 5`），超时即失败，不放宽上限；单个测试约 1 秒内，单个包 30 秒内。
- 只用 `t.TempDir()` 或测试结束即删除的临时目录与内联小数据；不访问网络，不读取用户 git 配置（测试内 git 命令显式传入身份并设 `GIT_CONFIG_GLOBAL`）。
- 实现仓路由用例需要 Git 2.44+（`--no-lazy-fetch`）。本地 git 过旧时跳过；CI 设 `YSS_TEST_REQUIRE_GIT=1`，过旧即失败。
- 覆盖率按 `-coverpkg=./internal/...` 统计，CI 设棘轮下限 30%，补测试后上调，不下调。总体覆盖率不替代关键异常窗口的行为回归。

当前覆盖：语义化版本区间、可移植路径与符号链接拒绝、Git 策略/审计/提交信息 trailer、三方合并候选、`lifecycle route` 的前置参数检查，以及在真实 spec 实例上的路由判定（daily、governed 标签、排除影响、未登记影响、正式绑定不可降级、前端拒绝、缺事实与摘要不匹配）。

测试通过不代表正式发行验收通过，也不生成验收回执。发行装配仍要求既有真实证据。

## CI

`Build and generated artifacts` workflow 先以 Python 3.12 运行发行工具离线回归（`python3 -m unittest discover -s tools/release-evidence -p 'test_*.py'`，步骤上限 5 分钟），再执行生产包构建、候选来源记录、格式／静态检查、Go 测试、构建后黑盒冒烟，以及从固定 Git 来源重建 Bundle → 帮助视图 → 完整帮助的逐字节检查。发行工具测试使用临时 fixture 与受控假 transport，不访问外部 API、不发布；不恢复原生验收工具，也不生成正式发行通过回执。每次运行均检查全部生成链，避免生成器传递依赖或工具链变化漏检。性能与兼容性证据在仓库外维护，见 [性能合同](artifact-performance.md)。

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

- 对 spec、backend 两个现役 Profile（另检查 design 被拒绝）：`init --plan/--apply`、`doctor`、`diff`、`context verify`、`skills list`、`assets list`、`recover`、`rollback`（查询与 `--apply`）、`sync --plan/--apply`；断言退出码为 0 且 envelope `status=ok`、`code=OK`，以及少量结果字段；回退后确认项目身份已移除。
- `yss git`：在临时仓库和本地裸远端（`file://`）上走 `branch start`、`commit`（只提交列出路径并加 Ticket trailer，已安装钩子拒绝缺 trailer 的普通提交）、`hooks install`、`policy set`、`audit`、`release tag`、`push --plan/--apply`；`--confirm-remote` 缺失或不符、受保护分支 push／commit 被拒绝；计划阶段不触达远端。

约束：

- 全部状态位于 `mktemp` 后经 `realpath` 的临时目录，计划文件在被测仓库之外；被测二进制以 `yss` 名称置于 `PATH` 最前，供钩子调用。
- `GIT_CONFIG_GLOBAL` 指向临时文件、`GIT_CONFIG_NOSYSTEM=1`、`HOME` 也在临时目录。`yss` 会清除全部 `GIT_*` 变量并按 `HOME` 读取全局配置，所以 `GIT_CONFIG_GLOBAL` 与 `$HOME/.gitconfig` 为同一文件。不触碰用户的全局或系统 git 配置。
- 任一断言失败即以非 0 退出，并保留临时目录供排查（`--keep` 可在成功时也保留）。
- CI 只在 ubuntu 运行；本地（如 macOS）运行不构成其他平台的运行验收，也不构成发行验收。
- 不含 fixture、预言机、录制或回执；通过只说明主路径在当前构建上能走通，不代表测试通过，也不替代原生、恢复、插件或安装等发行门禁。冒烟保持黑盒主路径范围；行为回归写成 Go 测试，不扩写进冒烟。

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

当前轻量 Go 测试、黑盒冒烟和生成检查由 `artifacts.yml` 执行；`scaffold-parity.yml` 独立执行 Go/Node 对拍。早期原生验收 workflow 的删除属于历史事实，不能解释为当前测试 workflow 已删除。仓库 ruleset/required checks 需单独核实，不能从本地工作流文件推断。

发行装配工具仍要求真实证据，产品安装与升级继续校验发行资格。当前[发行证据工具](../tools/release-evidence/README.md)在真实目标平台生成 `local-platform-program-install` 范围的原生与安装回执，`release-evidence.yml` 执行准备及发布后验证；普通 Go 测试和黑盒冒烟不生成这些回执。其他资格范围须提供对应的完整证据，不能用构建或局部测试替代。

## 未提交模板的本地装配

`tools/bundle` 接受来源锁 v4，用于模板维护的本地验收。两个现役 Profile 与存在时的共享技能来源均须声明 `sourceState: working-tree`、基线 `templateCommit` 和已捕获的 Git `sourceTree`，Profile 的 `templateVersion` 为 `worktree:<sourceTree>`，producer 同样为 working-tree。捕获树时使用独立 `GIT_INDEX_FILE`；不修改使用者暂存区、不创建提交。组合锁仍必须匹配 canonical 配置摘要与技能内容摘要。

本地锁保存为 `docs/source-lock.working-tree.json`，使用 `go run ./tools/bundle --source-root <Spec 根仓> --lock docs/source-lock.working-tree.json --out internal/bundle/assets` 重建嵌入资产。旧 v2/v3 锁仍只接受提交来源，v4 不支持混合来源。正式来源锁 `docs/source-lock.json` 保留原固定提交；发布工具继续拒绝 working-tree 包。获得提交授权后，按既有固定来源刷新流程重建正式锁与资产，不能只改状态字段。

## 可靠性回归与文档事实

Tracker 回归覆盖计划 v2、重算摘要的非法计划、API 来源、部分回执、未知写入结果、正文读取取消、本地事务原始错误码与恢复回执、原始字节/权限守卫及并发无关标签；程序回归覆盖准备取消、旧完整计划、封存重试及保留未知状态。文件发布回归用小数据与逐调用故障注入验证失败不发布、竞争不覆盖，MCP 用 Pipe 及继承 stdin 的子进程检查取消、EOF 与读取收尾。全部使用临时数据和本地模拟服务，不访问外部 API。

`go run ./tools/docstatus` 从生产版本常量、来源锁和 Profile 清单生成 README、本表及迁移清单的当前事实块；`--check` 只读核验，CI 必须执行。修改当前事实请先改对应权威来源，再重新生成，不手改生成块。

真实 Bundle 的命令层集成测试继续复用共享初始化实例；新增 Tracker/MCP 回归使用小夹具，不增加真实 Bundle 初始化。性能诊断用 `go test -json` 的用例耗时，不恢复长跑基准，也不放宽时限。
