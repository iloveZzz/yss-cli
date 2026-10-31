# 安装类型与兼容范围

现役 Profile 只有 `spec` 和 `backend`。每个 Profile 提供 `project`、`skills` 两种安装类型；新安装默认 `project`，已有工程沿用保存值，`sync --installation project|skills` 可显式切换。切换仍核验受管文件的原内容，用户修改或未知文件不会被强制覆盖。

当前项目 metadata 为 schema 6。已有 metadata 3、4、5 通过本次 `sync` 读取并迁移到 6；旧安装类型按兼容映射处理。迁移先做 dry-run，确认涉及目录与文件，再运行实际同步。旧插件绑定已经退役，不重新执行绑定或恢复插件专属命令。

```sh
yss doctor --root /absolute/project
yss sync --root /absolute/project --dry-run
yss sync --root /absolute/project
```

程序包使用 `release-manifest.json` schema 2。新升级器不接受 schema 1 作为待安装归档；已安装的旧收据与 manifest 仅按必要字段读取，以核验原有文件所有权和完整性。首次切换到本版由 Agent 按 [安装说明](build-install.md) 安装完整包；旧 CLI 在线升级器对新格式不保证兼容。

Bundle 生成器记录实际模板 HEAD、工作树状态及内容摘要；不以固定标签替代实际工作树来源。项目同步只消费已内嵌的 Bundle，不在运行时猜测或拉取模板最新版。`docs/template-release.json` 保留既有标签，不表示该标签包含当前未发布改动。

CLI 不提供阶段门禁、批准、合同审查、插件运行服务或保存计划。模板中的业务内容与技能按实际源资产分发；CLI 不替用户做批准、提交、推送或发布。路径、摘要、权限、事务与文件保护属于安装安全边界，继续保留。
