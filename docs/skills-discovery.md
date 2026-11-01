# 技能选择与所有权

`spec` 与 `backend` 各自从 Bundle 读取安装范围、默认技能、依赖与资源。`project` 安装技能及必要项目文档；`skills` 安装技能、宿主入口和必要引用资源。两者都是项目目录内的受管安装，不是全局技能安装。

```text
yss skills list --root /absolute/project --json
yss skills ensure yss-validation --root /absolute/project --dry-run
yss skills ensure yss-validation --root /absolute/project
yss sync --root /absolute/project --installation project
```

收据仅保存用户显式选择，默认技能与传递依赖每次重算。模式切换沿用显式选择；已退役或不可用技能返回原因及建议替代，不自动选择替代技能。Bundle 校验全部可选技能的依赖图，拒绝缺失引用、循环和跨模式混用。

文件处理以原安装摘要为基线：未知文件即使内容相同也不接管；来源未改变时保留本地修改及删除；来源与本地同时变化则整次冲突。受管文件退役时仅删除未改动版本，已修改版本产生冲突；user-owned/protected 文件保留。重复同步应返回 unchanged。

模板与技能只来自当前程序内嵌 Bundle，不自动拉取 main。`skills list` 报告物理文件状态；文件存在不代表宿主已实际加载。独立或全局技能由原安装来源维护，不能对宿主全局技能目录运行项目 init/attach/sync。
