# 技能查询与补装

先初始化或接入项目，再查询该项目内嵌模板中的技能。`skills list` 要求已有安装根，不把任意目录当作全局技能仓库。

```sh
yss init --root ./demo --profile spec --installation skills
yss skills list --root ./demo
yss skills ensure --root ./demo <技能ID> --dry-run
yss skills ensure --root ./demo <技能ID>
```

将 `<技能ID>` 替换为 list 返回的真实 ID；ensure 接受所需 ID，按依赖与平台投影补齐技能，不凭名字下载第三方技能。多个 ID 的写法以 `yss skills ensure --help` 为准。技能已经齐备时不需要重复补装；用户修改产生冲突时保留现场并处理诊断。

完整项目安装使用 `--installation project`；仅需技能使用 `skills`。已有项目可通过 `sync --installation project|skills --dry-run` 先预览切换。旧 `skills query|resolve` 与插件绑定不再使用。

当前模板的 Spec `project` 与 `skills` 默认安装相同的 16 项：`setup-yss-harness`、`yss-product-lifecycle`、`yss-research`、`yss-stage-decision`、`grilling`、`domain-modeling`、`wayfinder`、`to-spec`、`yss-design-system`、`prototype`、`prototype-review`、`to-tickets`、`triage`、`llm-wiki`、`i-have-adhd`、`handoff`。`implement`、`code-review` 仍可查询和按需补装。Backend 默认仍为安装、实现、代码审查与 handoff 四项。

完整 Spec 交付规格、产品原型及说明，技术设计与实现由研发承担。研究与竞品方法统一在 `yss-research`，产品设计和 UI 原型统一在 `yss-design-system`；`competitive-intelligence` 和 `yss-prototype-stage` 已从新技能库删除，不提供别名或迁移入口。预装不改变显式调用和外部动作授权。

实际集合来自二进制内嵌 Bundle；修改模板后必须重新打包构建才会进入新程序，已有发行程序不自动获得该集合。文件安装成功不等于宿主已经重新加载或执行技能。
