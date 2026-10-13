# 兼容与验收边界

呈现接口：终端默认中文，非终端维持原结果；`--human` 强制中文，`--json` 保持版本 1 envelope 和领域 result，二者互斥。`--diagnostics` 必须同 `--json` 使用，仅在失败时添加可选顶层 `diagnostic`（schemaVersion=1）。新呈现参数由入口消费，不传给治理校验或事务执行器。历史别名保留冻结协议；帮助优先处理并始终输出离线文本。

程序兼容基线：1.2.0；运行协议和 JSON envelope：1；升级计划：v2；Bundle 和 .yss.json：v3（保留 native metadata v1/v2 识别与历史原生事务恢复）。旧保存计划可以诊断，应用返回 `PLAN_VERSION`，须重新生成。内部来源锁支持 schema v2/v3；当前三个 Profile 的同源覆盖层组合构建使用 v3，历史 v2 仍可读取。每目录一个 Profile；旧家族 metadata 同时存在或与显式 Profile 矛盾时拒绝。现役旧 CLI 恢复基线分别为 Spec 3.5.10、Design 0.8.17、Backend 0.4.21；固定来源见 source-lock.json。Frontend 独立 Profile 已退役，旧实例拒绝并保留文件。

`attach` 仅接管没有 YSS metadata 的已有工程；原生实例返回 `SYNC_REQUIRED`，旧 CLI 实例返回 `MIGRATION_REQUIRED`。`attach`、`sync`、`migrate plan` 共用完整资产规划；显式 `--base-bundle` 读取离线材料，`--review-out` 导出候选，`--resolution-file` 结合原保存计划重建决议。三方候选须经过决议；固定来源和生成资产不接受任意合并。应用回执分别给出 `fileApplication` 与 `verification`；原生校验失败整体还原。现有 Context、Tracker、业务与批准资产保留。

迁移规则的适用来源二选一：精确来源（模板 commit 与快照摘要），或模板版本区间（`from.versions`，`min` 含、`max` 不含，语义化版本）。实例的模板版本先查 `internal/bundle/assets/version-table.json`，再看 metadata 自己记录的语义化版本；两者都没有时版本区间规则不适用，计划的 `notices` 如实报告，不猜测。对照表由 `tools/version-table.mjs` 对 2026-10-06 之后的来源锁与内嵌 Bundle 历史一次性生成，各 Profile 按首次出现的先后记为 `4.0.0-pre.N`；之前的旧 CLI 实例不在表内，版本区间规则对它们不适用。规则提议的 `delete` 在计划里带 `resolutionOptions`：用 `--resolution-file` 选择 `keep-local` 即拒绝删除，文件原样保留、升级不失败，同一条规则下登记过的保留决定在之后的计划里继续生效；不选则按计划删除。`--review-out` 的 `conflicts.json` 一并列出这些删除项。

Bundle 生产器按固定 Git 对象取源；`native-upgrade-v3` 兼容生成器使用 CLI 中已审查的输入，为旧快照提供 v3 metadata 读取脚本和升级指引。`nativeTransforms` 记录源字节与输出摘要，归档保留两者；原模板提交及快照摘要仍指向旧固定来源，生成器来源另由 producer 记录。当前工作树 producer 只能交付 implementation-ready，不能作为 committed-source 发行证据。

已实现的原生范围：Profile 身份识别、离线快照、初始化、只读差异、接管/同步/迁移计划、计划应用、文件事务、恢复与整体回滚、Skill/阶段资产补装基础、Context 校验与摘要、注册表查询、Schema结构检查、安全ZIP/XML以及SQLite运行记录。

Context 的 check/verify 对合法 schema v1 template-source 支持只读校验并返回可复用 context_snapshot；实例身份检测保持原边界，模板源携带实例 metadata 会被拒绝。三个 Profile 的 JS 消费者只负责固定二进制传输和 envelope/摘要校验。

当前治理 verify、完整 project-ci、Stage 写入、离线程序安装与 JS native transport 的边界见 porting-status.md。两个插件迁至固定二进制与公开 Bundle 接口；必要 Node/Python 治理工具继续独立维护。旧成功 JSON、私有 JS API 和停用参数仅按实际消费者迁移，不全量仿制；停用项明确拒绝并提供替代命令。独立 Go Bundle 生产不再调用旧 Node 私有导出模块。

Schema：默认2020-12与format断言、本地闭包引用、禁止网络检索、JSON重复键、YAML重复键/alias/多文档拒绝。现实Schema的Python正则字符类使用固定Unicode15兼容区间；未支持的高级语法返回SCHEMA_REGEX_INCOMPATIBLE，不静默放宽。当前Python基线缺可选date-time检查器，Go会拒绝其原本误接受的无时区时间；这是显式严格化，尚不能标为完全等价。JSON/YAML非法UTF和不成对surrogate被拒绝。

恢复：原字节、类型、权限与计划摘要持久保存；WAL先于目标写入。后续用户修改导致恢复或整体回滚拒绝，避免覆盖。Git index、业务目录和嵌套Git仓库不属于模板写范围。原有CLI的历史事务格式不能由新事务格式冒充；历史中断首先走旧CLI恢复。

可支持的平台 CI 使用GitHub官方提供的runner标签，见[官方列表](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)。流水线准备好不等于已经执行；本地交叉编译不能替代原生运行、取消、权限和恢复验证。

稳定版条件按 [退役合同](cli-retirement.md) 验收：必要能力和现役消费者闭合、本次明确发行平台实际运行通过、固定提交上本次受影响的 CLI、插件和安装契约通过。每份发行资产的 stableReady 结论由其发行 manifest、本次发行平台原生收据和明示范围的门禁摘要共同给出；源码版本和交叉编译不能代替这些证据。提交、推送、tag 和发布分别按授权执行。

2.0.0 的发行目标为 darwin/arm64 与 windows/amd64。macOS 归档与目标须位于同一支持原子克隆的 APFS 卷。macOS 从已校验的归档文件描述符原子克隆完整临时文件，再设置权限与同步；目标存在、来源或目标链接及摘要变化仍拒绝，安装文件与归档不共享可修改的 inode。不支持克隆或跨卷返回 UNPORTED，不退回普通复制；无法据此保证不受支持文件系统上的旧事务可恢复。旧版本已留下的未知或零字节事务临时文件仍保留并阻断。Windows 保留现有实现，须在真实 Windows runner 完成该平台原生、安装与恢复检查；Linux 不在本版本发行目标内。

本次资格范围为 local-platform-impacted-consumers；无关全仓回归及可复用重复检查按用户调整裁剪，未执行项不作完整模板通过声明。
