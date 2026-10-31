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
