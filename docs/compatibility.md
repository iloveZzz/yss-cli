# 兼容与验收边界

程序：1.0.0-alpha.2；运行协议：1；统一 metadata：.yss.json schema 1。每目录一个 Profile；旧家族 metadata 同时存在或与显式 Profile 矛盾时拒绝。旧模板/CLI 基线分别为 Spec 3.5.10、Design 0.8.17、Backend 0.4.21、Frontend 0.3.21；来源提交和快照摘要见 source-lock.json。

已实现的原生范围：Profile 身份识别、离线快照、初始化、只读差异、接管/同步/迁移计划、计划应用、文件事务、恢复与整体回滚、Skill/阶段资产补装基础、Context 校验与摘要、注册表查询、Schema结构检查、安全ZIP/XML以及SQLite运行记录。

完整深层 contract/evidence/handoff verify、阶段门禁、完整 project-ci、网络程序自动升级、旧成功JSON/API适配及插件内部模块消费者替换仍在迁移清单。已有原生 Stage 写入、scoped CI、离线程序安装及显式 JS native transport，逐项边界见 porting-status.md。原有CLI与治理脚本继续保留；这部分可能需要Node/Python。原型、Wiki、可视化、业务脚手架和模板源维护导出工具按计划独立执行。Node导出工具只用于构建固定开发基线，不被CLI运行时调用。

Schema：默认2020-12与format断言、本地闭包引用、禁止网络检索、JSON重复键、YAML重复键/alias/多文档拒绝。现实Schema的Python正则字符类使用固定Unicode15兼容区间；未支持的高级语法返回SCHEMA_REGEX_INCOMPATIBLE，不静默放宽。当前Python基线缺可选date-time检查器，Go会拒绝其原本误接受的无时区时间；这是显式严格化，尚不能标为完全等价。JSON/YAML非法UTF和不成对surrogate被拒绝。

恢复：原字节、类型、权限与计划摘要持久保存；WAL先于目标写入。后续用户修改导致恢复或整体回滚拒绝，避免覆盖。Git index、业务目录和嵌套Git仓库不属于模板写范围。原有CLI的历史事务格式不能由新事务格式冒充；历史中断首先走旧CLI恢复。

原生六平台CI使用GitHub官方提供的runner标签，见[官方列表](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)。流水线准备好不等于已经执行；本地交叉编译不能替代原生运行、取消、权限和恢复验证。

稳定版条件：完整能力清单闭合、旧入口/API/插件消费验证通过、所有日常治理无解释器验证通过、六个平台实际运行通过、固定提交模板/CLI集成与不可裁剪发布门禁通过。当前stableReady=false；提交、推送、tag和发布尚未授权。
