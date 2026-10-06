# 旧入口与 Spec API 兼容矩阵

本页保留早期 alpha 的固定观察记录；下文中的旧消费者和未覆盖清单是当时快照，不能视为本次退役的当前状态。当前统一 Bundle、插件迁移和历史恢复合同见 [退役合同](../docs/cli-retirement.md)。本次按实际消费者迁移，未使用的旧成功 JSON/私有 API 全等测试可退出；当前接口及真实旧实例恢复验证继续作为必要项。

当前可交付的是保留旧默认实现的兼容分发层和显式原生 API。四个旧 npm 包、插件包和全局入口没有被修改、安装或替换。完整旧接口等价迁移尚未完成，不能依据这些测试宣布兼容完成或稳定发布。

## 接口与执行选择

主入口可调用 `compat.Run(ctx, alias, args, stdout, stderr) int`；alias 为四个旧 package/bin 名。默认兼容选择只提供已证明的 discovery 与拒绝合同：Spec Error Envelope v1、专用 CLI Error v1、migrate Error v1；其它旧操作返回显式 `UNPORTED`，退出 1。不会落入原生写入或调用旧解释器。

显式 `--native` 调用原生 Go CLI，保留原生 `outputVersion=1`、`protocolVersion=1` envelope，不声称旧成功 schema 等价。Profile 由 alias 固定；禁止用户再指定 `--profile`。`--dry-run` 映射到原生 `--plan`，`recover` 无 `--apply` 只检查状态；事务命令先核验项目家族。中断初始化尚未形成身份文件时，兼容入口不会猜测家族；该恢复边界待统一恢复身份合同闭合。

原生迁移将 `migrate plan --output` 映射为 `migrate --plan --out`，将 `migrate apply --plan` 映射为 `migrate --apply --plan-file`。原生 `force`、`prune`、`migrate-layout`、`git-init`、`agent-runtime`、`issue-tracker`、示例开关、外部 archive/resolutions 参数明确拒绝为 `UNPORTED`，不忽略。非初始化写入仍消费保存的摘要绑定计划。

`compat/spec-api` 是私有、未发布的 opt-in CommonJS 包。保留同步 `API_VERSION=1`、`projectDoctor`、`projectDiff`、`templatePlan`、`templateApply`、`toErrorEnvelope`；前四个旧成功 schema 尚未迁移，Go 返回 `UNPORTED`，JS 同步抛出 `YSS_UNPORTED`。`toErrorEnvelope` 直接保存旧顺序匹配与显式 `YSS_` code 优先语义，可在没有 Go 时使用。

JS 使用 `YSS_BINARY` 或 PATH 上 `yss`，以 `spawnSync` 调用 `yss compat-api <method>`，无 shell、无隐式安装、无静默退回旧实现。主入口调用 `RunAPI(ctx, method, stdin, stdout, stderr) int`。请求限制 4 MiB，输出限制 32 MiB，等待最多 120 秒；缺二进制、非协议 JSON、错误退出码不一致都会同步抛出规范错误。

新的显式命名空间为 `native.run({profile,args})` 和 `native.snapshot(profile)`。`snapshot` 提供离线固定来源事实：四 Profile、导入的旧 CLI/template version、CLI SHA、template SHA、`sourceState`、snapshot/manifest hash、manifest 与 distribution。它不生成旧 `readTemplateSnapshot()` 的私有路径/encodedPaths/requestedRef 返回形状。

## 已观察的差分

对照源码根为 `/Users/zhudaoming/Projects/yss-spec-project-template`。测试从固定旧入口实际执行 Node oracle，仅在隔离临时项目进行写入；Go 执行路径不依赖 Node/Python。JSON 按解析后的完整值比较，文本和 stderr 按字节比较，退出码逐项比较。

| 旧入口 | 固定版本 | 当前 CLI SHA | discovery/拒绝矩阵 | 原生只读 init 计划 |
|---|---|---|---|---|
| create-yss-spec | 3.5.10 | 1610087396307767383c57e2fd13385766204e1c | 8/8 已观察一致 | 使用真实 Spec 快照，未创建目标目录 |
| create-yss-harness-design | 0.8.17 | 0e859f7df95f5d2ade8a316b4d211bb9f5e770ad | 8/8 已观察一致 | 使用真实 Design 快照，未创建目标目录 |
| create-yss-harness-backend | 0.4.21 | ad4ed2a8b38876952544feda676f2f4d509f832b | 8/8 已观察一致 | 使用真实 Backend 快照，未创建目标目录 |
| create-yss-harness-frontend | 0.3.21 | 620e2ccec97b8c39cabf03913cf65391eb0c32a1 | 8/8 已观察一致 | 使用真实 Frontend 快照，未创建目标目录 |

每个入口的 8 个观察是：help、help+json、version、version+json、sync 未知参数、sync 参数缺值、migrate 非法动作、migrate 重复参数。Spec help/version 的 `--json` 仍为旧文本；没有擅自转换为新 envelope。此矩阵不代表所有参数排列已经等价。

真实 Spec 样本执行了旧 init（含 codex runtime）、旧 sync JSON、原生 diff、原生 sync 的 `MIGRATION_REQUIRED` 拒绝、原生 migrate plan/apply、原生 sync 计划、原生 whole rollback。迁移和 rollback 均核验原 `.yss-template.json` 的完整字节不变；回退移除本次新增 `.yss.json`。另一个真实原生 Spec 样本验证 init 保存计划→apply→doctor→sync 计划、跨家族 diff/rollback 拒绝、无 apply 的 recover 只读。真实 JS→Go subprocess 验证公开 exports、错误规范、固定来源读取、原生计划、缺二进制与错误执行器拒绝。

## 明确未覆盖

| 合同 | 当前边界 | 后续验收条件 |
|---|---|---|
| Spec Plan v1 / ApplyResult v1 / Doctor v1 | 返回 YSS_UNPORTED | 完整 operation、ownership、merge、generator、lifecycle、migration、warnings、stats 与旧 oracle 等价 |
| 专用 CLI preview/applied/healthy schema | 返回 UNPORTED | unchanged/preserve、conflictDetails、retired/prunable、core provenance、checks 与事务回执等价 |
| 默认旧 init/attach/sync 写入 | 未切换默认；Go adapter 拒绝 | 四 Profile 真实旧成功路径、定制/业务资产保护、metadata 与 mode 差分证明 |
| force/prune/布局迁移/runtime 选择/tracker/示例/Git init | 原生 alias 明确 UNPORTED | 独立政策语义与拒绝场景证明；不得只消除参数报错 |
| 旧 migrate plan/archive/receipt/WAL/recover/rollback 格式 | 不消费；返回 UNPORTED | 老格式兼容读取、版本基线、整树/Git 摘要与恢复拒绝合同 |
| update/upgrade npm 安装 | 原生 UNPORTED；旧默认保留 | 独立原生发行与可验证安装/回退合同 |
| Spec skills/assets 的旧结果与 runtime add | 旧选择 UNPORTED | 安装集合、stage 资产、投影/锁与输出差分 |
| 旧成功 API 的同步返回值 | 旧四方法明确 YSS_UNPORTED | 与 CLI 同一完整合同；不能把原生 plan 重命名冒充旧结果 |
| 三专用 Profile 的真实旧 init→原生迁移写入样本 | 本执行者未观察；只有真实计划与快照 API | 主控补专用真实实例差分，不能由 Spec 单样本外推 |
| Windows/Linux 平台运行 | 本执行者未运行 | 平台真实行为、信号、路径/锁/恢复与桥接退出码证据 |

## 插件稳定 API 接入证据

Backend plugin 的 `project.mjs:57-61` 和 `pack-cli.mjs:19-23` 当前 `createRequire` 私有 `src/template/instance-runtime.js`，读取快照并检查版本/SHA/hash。可先将**只读固定来源检查**改为 `native.snapshot('spec')` 的 version/SHA/hash 合同，binary 本身仍由插件绑定的原生发行 hash 验证；不能仅相信版本字符串。新 `templateVersion` 对应导入的旧 pin.version，统一 CLI 的 `cliVersion` 独立比较。打包脚本还使用 encodedPaths，必须继续保留旧 archive 打包路径，直到原生 bundle/export 合同可用。

Design plugin 的 `project.mjs:51-67` 仍调用旧 Node bin，并要求 `.yss-harness-design.json` schema 2、旧 coreDigest。不能只换执行命令：需要单独迁移 project-binding 与 `.yss.json` schema、固定版本和新事务回执消费者；本次没有修改插件或其 pin。

验证命令：`YSS_LEGACY_SOURCE=/Users/zhudaoming/Projects/yss-spec-project-template GOTOOLCHAIN=go1.27.1 go test -count=1 -v ./internal/compat`，以及 `GOTOOLCHAIN=go1.27.1 go vet ./internal/compat`。没有 `YSS_LEGACY_SOURCE` 时，Go 测试明确 skip 两个外部 oracle 测试；不能把 skip 记为差分通过。最终输出见本目录 `verification.log` 与 `workflow-result.json`；本记录不替代主控的独立审查与 Fresh Verification。
