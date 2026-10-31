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

项目 metadata 兼容、旧 Bundle 读取与模板源码生成是三个范围。Bundle 仍为 schema 6，旧归档摘要不改写；缺少模式级依赖的旧归档沿用技能依赖，并按所选安装模式重建资源路径。新生成器要求四个安装集合显式提供 `skillDependencies` 对象，旧源码需更新清单后才能重新生成。项目 metadata 继续读取 3–5，并通过原有所有权与摘要检查后迁移到 6。

安装和事务写入同时检查 Git index 的 gitlink、`.gitmodules` 声明及嵌套 `.git`；未初始化或不存在的挂载点不会被创建为普通目录。挂载路径按组件保守折叠大小写，因此仅大小写不同的路径也受保护。显式选择已初始化子仓或 worktree 根可以安装，不要求附着分支。普通非 Git 目录无需 Git；存在 Git 元数据但无法可靠读取时拒绝写入。实际执行和恢复会重检边界，CLI 锁不与外部 Git 操作形成原子互斥。

当前源码的在线升级默认 GitHub `iloveZzz/yss-cli`，通过 `--source gitlab` 显式选用原私有 GitLab；离线归档不接受 `--source`。两个平台保持独立身份、认证与资产校验，不自动回退或双发。旧二进制仍保留其原有来源行为，须以实际 `upgrade --help` 为准。
