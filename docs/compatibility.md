# 兼容与验收边界

程序：1.0.0；运行协议：1；Bundle 和 .yss.json 为 schema v2（保留 native metadata v1 校验兼容）。每目录一个 Profile；旧家族 metadata 同时存在或与显式 Profile 矛盾时拒绝。历史旧 CLI 基线分别为 Spec 3.5.10、Design 0.8.17、Backend 0.4.21、Frontend 0.3.21；固定来源见 source-lock.json。

已实现的原生范围：Profile 身份识别、离线快照、初始化、只读差异、接管/同步/迁移计划、计划应用、文件事务、恢复与整体回滚、Skill/阶段资产补装基础、Context 校验与摘要、注册表查询、Schema结构检查、安全ZIP/XML以及SQLite运行记录。

当前治理 verify、完整 project-ci、Stage 写入、离线程序安装与 JS native transport 的边界见 porting-status.md。两个插件迁至固定二进制与公开 Bundle 接口；必要 Node/Python 治理工具继续独立维护。旧成功 JSON、私有 JS API 和停用参数仅按实际消费者迁移，不全量仿制；停用项明确拒绝并提供替代命令。独立 Go Bundle 生产不再调用旧 Node 私有导出模块。

Schema：默认2020-12与format断言、本地闭包引用、禁止网络检索、JSON重复键、YAML重复键/alias/多文档拒绝。现实Schema的Python正则字符类使用固定Unicode15兼容区间；未支持的高级语法返回SCHEMA_REGEX_INCOMPATIBLE，不静默放宽。当前Python基线缺可选date-time检查器，Go会拒绝其原本误接受的无时区时间；这是显式严格化，尚不能标为完全等价。JSON/YAML非法UTF和不成对surrogate被拒绝。

恢复：原字节、类型、权限与计划摘要持久保存；WAL先于目标写入。后续用户修改导致恢复或整体回滚拒绝，避免覆盖。Git index、业务目录和嵌套Git仓库不属于模板写范围。原有CLI的历史事务格式不能由新事务格式冒充；历史中断首先走旧CLI恢复。

可支持的平台 CI 使用GitHub官方提供的runner标签，见[官方列表](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)。流水线准备好不等于已经执行；本地交叉编译不能替代原生运行、取消、权限和恢复验证。

稳定版条件按 [退役合同](cli-retirement.md) 验收：必要能力和现役消费者闭合、本次明确发行平台实际运行通过、固定提交上本次受影响的 CLI、插件和安装契约通过。每份发行资产的 stableReady 结论由其发行 manifest、本次发行平台原生收据和明示范围的门禁摘要共同给出；源码版本和交叉编译不能代替这些证据。提交、推送、tag 和发布分别按授权执行。

1.0.0 本次仅发行并验证 darwin/arm64，六平台齐备不再是条件。其他平台已有结果按实际状态保留，未通过的平台不提供合格稳定包。

本次资格范围为 local-platform-impacted-consumers；无关全仓回归及可复用重复检查按用户调整裁剪，未执行项不作完整模板通过声明。
