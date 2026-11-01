# 技能查询与补装

先初始化或接入项目，再查询当前运行 CLI 的内嵌 Bundle 为该项目 Profile 提供的技能。`skills list` 要求已有安装根，不把任意目录当作全局技能仓库。

```sh
yss init --root ./demo --profile spec --installation skills
yss skills list --root ./demo
yss skills ensure --root ./demo <技能ID> --dry-run
yss skills ensure --root ./demo <技能ID>
```

将 `<技能ID>` 替换为 list 返回的真实 ID；ensure 接受所需 ID，按依赖与平台投影补齐技能，不凭名字下载第三方技能。多个 ID 的写法以 `yss skills ensure --help` 为准。技能已经齐备时不需要重复补装；用户修改产生冲突时保留现场并处理诊断。

完整项目安装使用 `--installation project`；仅需技能使用 `skills`。已有项目可通过 `sync --installation project|skills --dry-run` 先预览切换。旧 `skills query|resolve` 与插件绑定不再使用。

当前模板的 Spec `project` 与 `skills` 默认安装相同的 16 项：`setup-yss-harness`、`yss-product-lifecycle`、`yss-research`、`yss-stage-decision`、`grilling`、`domain-modeling`、`wayfinder`、`to-spec`、`yss-design-system`、`prototype`、`prototype-review`、`to-tickets`、`triage`、`llm-wiki`、`i-have-adhd`、`handoff`。`implement`、`code-review` 仍可查询和按需补装。Backend 的 `project` / `skills` 均默认安装 41 项，可用库共 53 项，另有 12 项按需补装。

完整 Spec 交付规格、产品原型及说明，技术设计与实现由研发承担。研究与竞品方法统一在 `yss-research`，产品设计和 UI 原型统一在 `yss-design-system`；`competitive-intelligence` 和 `yss-prototype-stage` 已从新技能库删除，不提供别名或迁移入口。预装不改变显式调用和外部动作授权。

Backend 承接 Spec 的规则、验收和产品原型说明，按需完成实现 Slice 拆分、架构/技术/详细设计、脚手架、开发与测试。`implement` 组织当前研发任务，`to-tickets` 细化 Slice，`yss-technical-design` 统一设计深度，`yss-backend-testing` 负责一般测试；DDD 建模才使用 `yss-tactical-design`，MVC 保留既有 Service 边界。普通补测不强制 TDD，局部修复不要求完整文档链。

完整 Backend 交付包括有源码依据的构建部署手册，优先维护已有 README/运维说明，结构参考 `.template-spec/templates/build-deploy-guide-template.md`。部署当前只交付说明，不执行部署或创建 CI/CD、云资源。预装和方法选择不授予提交、推送、交接、部署或索引维护权限。

实际集合来自二进制内嵌 Bundle；修改模板后必须重新打包构建才会进入新程序，已有发行程序不自动获得该集合。文件安装成功不等于宿主已经重新加载或执行技能。

必需依赖来自分发清单的 `skillDependencies`，资源来自当前安装模式的 `skillReferences`；正文提及不会触发安装，也不会自动执行方法技能。

`skills list` 保留 `profile`、`skills`、`installed`，并通过 `unavailableSkills` 列出已登记但当前 Bundle 不再提供的技能，以及已知退役技能在已识别平台根下的本地入口。记录包含 ID、状态、路径、原因和替代建议；sync 的预览、成功及冲突结果也包含该字段。请求已声明退役 ID 返回 `SKILL_RETIRED`，替代技能只供选择，不自动补装。

同步只移除未修改的退役受管文件；任一规范入口或平台副本有用户修改时整体拒绝。无管理记录或 user-owned 的旧文件保留，诊断不授予删除权限。更新模板源、重建另一个 CLI 或查询列表均不会自动升级现有项目；运行的二进制决定可用 Bundle，实际 sync 才更新项目安装记录。
