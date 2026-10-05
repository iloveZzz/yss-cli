# alpha.2 历史实现与验证记录

日期：2026-10-05。当前版本：`1.0.0-alpha.2`，运行协议 1；独立工程位于 `/Users/zhudaoming/Projects/yss-cli`，Git 分支 `codex/unified-go-cli`，尚无提交。**已实现可运行的预发布核心；整份重构方案未完成，稳定发布验收未通过。**

四种固定 Profile 快照、统一 Go 命令、原生读写核心、离线程序安装与恢复回退已经落地。完整能力及未迁移边界见 [迁移清单](/Users/zhudaoming/Projects/yss-cli/docs/porting-status.md)。原模板仓库、旧 npm 默认执行器、插件和 Skills 的现有执行路径尚未切换。

## 当前候选与验证范围

本轮执行、测试、打包文档和 JavaScript transport 共 87 个输入，摘要为 `acbf4055880457c50d4384da56023c2c51c7ccdf55c81f17b0400f97ab47b5b5`。验证前后 bytes/mode/size 均一致，`input_drift=false`；来源状态为 `working-tree`。本页是观察报告，不进入发行包或这 87 个执行输入。

| 范围 | 本轮实际结果 | 验收限制 |
|---|---|---|
| Go 全包 `go test -json -count=1 -race ./...` | exit 0；9 个有测试包通过，401 个测试/子测试通过，0 个测试 skip | 主机显式启用了固定旧 CLI、Node YAML、Python Schema 与 Stage 对照 oracle；4 个无测试包的 package skip 不计为测试跳过 |
| `go vet ./...`、`go mod verify` | exit 0 | 非固定提交发布门禁 |
| macOS arm64 | 四 Profile 已迁移命令共 82 次执行均 exit 0；子进程 PATH 为空 | 原生主机；仅覆盖已迁移命令，不代表完整治理 |
| Linux arm64 | 同一 82 次命令均 exit 0 | OrbStack arm64 Linux VM，scratch 容器，无网络、根文件系统只读、PATH 为空；仅证据目录可写 |
| Linux amd64 | 同一 82 次命令均 exit 0 | 在上述 arm64 VM 上转译执行；不能算 amd64 原生硬件验收 |
| 两种 Linux 架构的事务、治理、升级测试 | 各 26 个事务、34 个治理、21 个升级顶层测试通过，exit 0 | 各跳过 1 个需要 Node 的 Stage 开发 oracle，该 oracle 已在主机全包测试运行；首次治理测试遗漏 fixture 的失败日志保留，补只读 fixture 挂载后通过 |
| 程序升级/恢复/回退 | 独立审查真实 alpha.1→alpha.2 升级、整批 bytes/mode 回退、重复回退及真实 CLI SIGKILL 后恢复 | 漂移拒绝、跨事务 kind 与路径范围保护；其余平台恢复仍需原生验证 |
| SQLite 查询与 pin/unpin | token、终态、重复执行、未知/过期 run、整数/Unicode、损坏 JSON、并发锁与取消样本通过 | 已有 WAL 在打开前明确拒绝；持续占用返回 RUNTIME_BUSY，已取消返回 CANCELLED；不模拟旧执行器 5 秒等待 |
| 六平台预发布包 | macOS/Linux/Windows × amd64/arm64，CGO0 编译、SHA、manifest 与包中文档引用验证通过 | macOS amd64 无可用 Rosetta；Windows 两平台未运行；六平台 workflow 已准备但未执行 |

82 次命令包含 2 个全局命令与每 Profile 20 次命令调用，覆盖初始化、诊断、差异、补装、Context/状态、运行记录查询与保护标记等原生能力。它是测试调用数，不是 82 个互异产品命令。程序升级、真实进程中断、回退、拒绝和旧实例迁移由单元/集成及独立消费样本覆盖。

发布包不把交叉编译标为原生验证通过：包内 `nativeRuntimeVerified=false` 保留构建时事实；后续实际运行证据在本页及集中记录单独绑定。Linux 烟测二进制与对应包内二进制 SHA 一致。工程 `bin/yss` 保留调试符号，发行包以 `-ldflags=-s -w` 编译；两者绑定相同执行输入。

## 兼容与保护证据

当前全包测试重新执行了旧入口 discovery/拒绝输出与退出码对照、四种真实旧 init→Go 迁移/整体回退以及 JavaScript→Go transport。旧成功 JSON/API、插件私有模块消费者、force/prune 与定制合并尚未全部闭合，默认旧执行器保留。

Schema/YAML 差分包括 74 个现实 Schema 编译、30 个 Python 样本、27 个 Node YAML 样本；此前独立 Unicode15 逐标量与反例审查保留为背景证据。Go 对旧 Python 环境缺少可选 date-time checker 时误接受的无时区日期时间严格拒绝，这是显式兼容差异。结构校验不替代当前上下文、批准和完整门禁。

文件保护覆盖持久计划/WAL、实际 kill→recover、bytes/mode、Git 暂存区、业务文件、Context、并发漂移和人工修改拒绝。最新事务按锁内持久 sequence 排序；时间只作展示。Windows readonly 删除/替换/输出仍明确 UNPORTED，目录 flush 等行为须原生验证。

[基线清单](/Users/zhudaoming/Projects/yss-cli/docs/baseline-inventory.json)记录 333 个代码模块/启动器：327 Node、5 Python、1 shell/launcher，包含库、fixture、测试和模板源维护工具，不能视为 333 个用户命令。该清单还保存四旧 CLI 和两套当前插件的词法消费者；词法引用不构成执行证明。

## 当前交付证据

- [集中观测记录](/Users/zhudaoming/.yss-harness/runtime/5391a3c4185fae417c28ef908ade1508f7dbb75bdbec2c8283412e6bb15ef967/maintenance/research/2026-10-05-unified-go-cli/extension-r6/verification-summary.json)
- [执行输入核对](/Users/zhudaoming/.yss-harness/runtime/5391a3c4185fae417c28ef908ade1508f7dbb75bdbec2c8283412e6bb15ef967/maintenance/research/2026-10-05-unified-go-cli/extension-r6/candidate-after.json)
- [独立审查报告](/Users/zhudaoming/.yss-harness/runtime/5391a3c4185fae417c28ef908ade1508f7dbb75bdbec2c8283412e6bb15ef967/maintenance/research/2026-10-05-unified-go-cli/extension-r6/review/report.md)
- [alpha.2 六包与 SHA](/Users/zhudaoming/.yss-harness/runtime/5391a3c4185fae417c28ef908ade1508f7dbb75bdbec2c8283412e6bb15ef967/maintenance/research/2026-10-05-unified-go-cli/packages-alpha2/checksums.json)

先前 `final-native`、`packages-alpha1-reviewed` 及其他 alpha.1 目录是历史验证/升级样本；本轮 alpha.2 发行包只引用 `packages-alpha2`。当前原模板仓库 tracked Git 和暂存区保持不变。仅删除了本任务创建且 ID 核对一致的 5 个 Docker 镜像标签；构建上下文、日志与发行包保留。

## 性能观测

以下为历史 alpha.1 的 macOS arm64 样本，不是本轮 alpha.2 新测量：固定 Node `24.21.0`，使用 `/usr/bin/time -l`，每样本新进程，未清 OS 文件缓存，期间有其他验证负载。版本查询 12 个样本中位数为 Go 10.2 ms / 14.5 MiB，Node 39.2 ms / 52.8 MiB。

| 初始化 | Go 秒 / 峰值 MiB | 旧 Node 秒 / 峰值 MiB |
|---|---:|---:|
| Spec | 3.77 / 348.3 | 1.30 / 109.3 |
| Design | 19.86 / 183.7 | 26.64 / 331.3 |
| Backend | 30.90 / 587.98 | 34.74 / 401.20 |
| Frontend | 42.31 / 682.73 | 50.47 / 405.61 |

安装文件数、输出合同和事务持久化不同，不能从这组样本推导相同合同下的整体性能优势。原始记录见[历史性能数据](/Users/zhudaoming/.yss-harness/runtime/5391a3c4185fae417c28ef908ade1508f7dbb75bdbec2c8283412e6bb15ef967/maintenance/research/2026-10-05-unified-go-cli/final-native/performance/report.json)。alpha.2 单包约 54 MiB，以 checksums 中的 bytes 为准。

## 剩余稳定切换条件

完成完整治理语义与运行存储能力、旧成功 JSON/API 及插件消费者、固定版本启动器/Skills/投影/完整项目 CI/四类分发快照；完成全部日常命令无解释器验证和六个平台原生权限、取消、中断恢复；固定提交后运行完整模板与 CLI 发布门禁。当前不宣布稳定版本或整份方案完成，未提交、推送、打 tag、正式发布或全局安装。

六平台 matrix 使用[官方 runner 标签](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)。工程固定[Go 1.27.1](https://go.dev/doc/devel/release#go1.27.1)；编译后的已迁移能力无需 Go、Node 或 Python。
