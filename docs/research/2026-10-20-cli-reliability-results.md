# yss-cli 可靠性优化交付与验证

日期：2026-10-20。基线：v2.5.2 / `bcc43077f2152ea71b24c7e4b084a3d29ead5d20`。实现分支：`fix/reliability-v2.5.2`。代码验收提交：`374bcf2`；本报告为后续文档提交，不改变受验收的 Go 源码。

八项确认问题已实现修复，六批改动各自包含回归用例。JSON envelope / 协议 v1、两个现役 Profile、固定模板 v4.6.4 与项目/对象存储布局保持既有合同。版本常量仍为 2.5.2，未推送、打发行标签或正式发布。

## 六批交付

| 批次 / 提交 | 问题 | 行为与兼容说明 | 实际回归 |
|---|---|---|---|
| 1 / `fa5bf73` | TRACKER-01、03 | 计划 v2 绑定 API 来源、Ticket 范围、原始输入和远端观察；严格解析并重建合法语义。v1 返回 `PLAN`、退出 1，提示重新生成。错误保留已知副作用及未知结果，不自动重试。 | 改写后重算摘要、目标 Issue、重复项/JSON 字段、未知字段、旧计划、来源变化与重定向、CLI 部分回执、连接中断未知结果。 |
| 2 / `099aecb` | CORE-01 | 复用事务锁与封存机制，验证 program-update 未发布准备；支持旧完整计划无准备头。安全准备返回 `sealed`，不写程序目标；项目 Profile 校验保持严格。 | 准备取消→封存→重试；重复恢复；缺计划、非空 WAL、用户修改、多个准备状态均拒绝并保留材料。 |
| 3 / `4953e44` | TRACKER-02 | 每次远端写前复核 Ticket/Issue；本地事务沿用原始字节和权限守卫。标签改为只增删五态，写后查询对账。无法将多个远端操作原子化，部分结果据实返回。 | 预检/写前/写后本地漂移、并发新增无关标签、本地回写失败；无关标签与用户修改不被覆盖。 |
| 4 / `a91740a` | CORE-02、03 | 归档按平台安全打开普通文件并复核身份/大小；运行对象在同目录唯一临时文件写完、校验和 Sync，再以不覆盖方式发布。Unix 同步目录，不支持必要原语返回 `UNPORTED`。 | FIFO、符号链接、目录、超限归档；写/Sync 失败不发布、同摘要竞争、损坏既存对象保留并提供诊断。 |
| 5 / `7330b48` | MCP-01 | MCP 拥有可关闭输入，取消时关闭并等待读取协程结束；继续串行调用，取消后不输出工具结果。内部调用者须提供 `io.ReadCloser`。 | 空闲取消、调用期间取消、EOF、读取错误、输入关闭及无迟到输出。 |
| 6 / `033cc99` | DOC-01 / 工程保障 | 修正当前版本/Profile/测试说明；由权威常量、来源锁和清单生成当前事实块，CI 核验。覆盖率门槛 10%→30%。补 Unix 真实继承 stdin 的取消处理和子进程回归。 | 文档生成/只读检查、完整帮助生成；真实继承管道 SIGTERM 退出。新测试使用小夹具。 |

应用旧 Tracker 计划时，请重新运行 `yss tracker sync --root <项目> --plan --out <项目外新文件>`。未知远端结果先运行 `yss tracker status --root <项目> --fetch --json` 对账，再生成新计划。`remote_changed` 表示已确认的远端改变；是否仍有未确认副作用以 `remote_state` 判断。

## 独立审查与补充修复

对 `bcc43077..033cc99` 完成一次独立只读审查：2 项 Important，无 Critical、无另列 Minor。统一在 `374bcf2` 修复，两组新回归先 RED 再 GREEN：

- 本地事务失败保留原始 cause/code 和可用的 `transaction` 回执；`local_error_code` 单独报告本地错误，即使先发生远端错误也不会丢失。CLI 回归覆盖最后一次远端操作后漂移、准备阶段取消、待恢复事务、远端与本地同时失败。
- 读取 HTTP 正文时取消保留 `CANCELLED` 和取消原因；写请求仍保留 `remote_state=unknown`，只发一次请求。GET/POST 使用本地模拟服务验证。

## 实际验证

环境：Linux amd64、Go 1.27.1、Node 24.19.0、Git 2.52.0，`CGO_ENABLED=0`、`YSS_TEST_REQUIRE_GIT=1`。全量测试未与生成构建竞争执行。

| 检查 | 结果 |
|---|---|
| 各批定向 Go 测试及新增失败用例 | RED→GREEN；见各批与审查日志。 |
| `timeout 300s go test -count=1 -json -timeout 5m -coverpkg=./internal/... -coverprofile=… ./...` | 通过；529 个测试通过事件（含子测试），无失败或跳过测试；internal 覆盖率 **31.6%**。CLI 包 **15.076 秒**，其余已测试包均小于 7 秒；未放宽全量上限。 |
| `go build ./...` | 通过。 |
| `go vet -composites=false ./...` | 通过，沿用既有配置。 |
| `gofmt -l .` / `git diff --check` | 无输出，通过。 |
| `go run ./tools/docstatus --check` / `go run ./tools/helpdocs --reference --check` | 通过。 |
| `node tools/check-no-pinned-digests.mjs` | 通过。 |
| 候选构建与主路径黑盒冒烟 | 通过；Linux amd64 二进制 31,916,192 字节，低于既有 35,000,000 字节限制。 |
| `node tools/artifacts.mjs check --scope all --source-root … --out …` | 通过；固定 Bundle → 两个帮助视图 → 完整帮助及大小检查全部一致；归档 7,064,037 字节。 |
| 真实 CLI MCP 进程：ping 后保持 stdin 打开、发送 SIGTERM | 通过；约 8 毫秒退出，退出码 1（保持既有取消行为），无迟到 stdout。 |
| macOS ARM64 / Windows x64 | updater 与 MCP 测试程序交叉编译通过；**未执行原生定向验证**，交叉编译不替代原生验收。 |

最终候选 SHA-256：`64071c2523ca1eb69fe2564910a8732c78da18e242fd5b731cec0d3204bdf2ec`。内嵌 Bundle SHA-256 保持 `179ed0e039426fd3ad508d390e7ae6d4bff4394a8d3fdcb987ecaa89ab40f48c`。

取舍：检查发现既有真实 Bundle 集成夹具已经通过 `sync.Once` 共享初始化，因此保留必要集成断言；新增 Tracker/MCP 用例使用小数据，不新增真实 Bundle 初始化。本轮未声称已完成额外 Bundle 性能改造；将来包耗时回升时，再依据逐例测量调整热点。

验收日志和构建/生成来源记录保存在本地 `/workspace/scratch/yss-cli-implementation-evidence/`：`delivery-tests.jsonl`、`delivery-coverage.out`、`review-red.log`、`review-green.log`、`progress.md`、`candidate/build-receipt.json`、`artifacts/receipt.json`、`smoke.log`、`mcp-process.json`。这些记录说明本次执行结果，不授予发行资格。

## 未执行验证与后续工作

- 补 macOS ARM64、Windows x64 原生文件系统与 MCP 定向验证。
- 验证 SQLite、对象与 owner 文件之间的断电持久化顺序；本轮 Unix 文件/目录同步不构成跨资产完整断电保证。
- 独立设计存储诊断/修复工具，区分临时残片与被引用证据；本轮不自动覆盖损坏对象。
- 完善平台证据展示、核实脚手架对拍 required checks；再基于修改热点与实测推进拆分和性能优化。

未执行真实 GitHub 写入、正式发行资格验收或正式发布。所有回归使用临时数据与本地模拟服务。
