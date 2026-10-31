# 命令与日常操作

CLI 只保留下表命令。使用 `yss help`、`yss <命令> --help` 或 `yss help <命令>` 查看实际参数；`yss --version` 查询程序版本，`yss --help` 查看帮助。命令帮助不需要安装项目。

| 命令 | 用途 |
| --- | --- |
| `init` | 在目标目录安装指定 Profile |
| `attach` | 将指定 Profile 接入已有工程 |
| `sync` | 同步项目到当前 CLI 内嵌模板，可切换安装类型 |
| `skills list` | 查询已安装项目中的可选及已安装技能 |
| `skills ensure` | 按技能 ID 补装技能 |
| `scaffold catalog` | 查询后端平台目录 |
| `scaffold ddd`、`scaffold mvc` | 直接生成后端工程 |
| `upgrade` | 在线或离线升级 CLI 程序 |
| `doctor` | 检查项目安装与文件状态 |
| `recover` | 按事务现场执行受保护恢复 |
| `version` | 查询 CLI 版本 |
| `help` | 查看命令帮助 |

`init`、`attach` 显式提供 `--root` 与 `--profile spec|backend`。新安装默认 `project`；`skills` 只安装技能及其入口、依赖资源。现有项目默认沿用记录值。

```sh
yss init --root ./demo --profile spec --installation project --dry-run
yss init --root ./demo --profile spec --installation project
yss attach --root ./existing --profile backend --installation skills --dry-run
yss attach --root ./existing --profile backend --installation skills
yss sync --root ./demo --installation skills --dry-run
yss sync --root ./demo --installation skills
yss doctor --root ./demo
```

写操作的 `--dry-run` 仅显示改动预览，不写项目或保存计划。实际写入时重新计算并检查输入；用户修改、未知所有权、危险路径或并发冲突会拒绝覆盖。不能把预览当作将来输入不变的保证。遇到中断先保留现场，运行 `yss recover --help` 查看恢复参数，不手工删除锁、收据或事务资料。

```sh
yss upgrade --dry-run
yss upgrade --to 3.0.2 --dry-run
yss upgrade --tool-root /absolute/new-yss
yss upgrade --artifact /absolute/yss_VERSION_OS_ARCH.tar.gz --sha256 <64位摘要> --tool-root /absolute/new-yss --dry-run
```

在线 dry-run 读取发行元数据与 checksum 文本，不下载归档、不写安装目录；离线 dry-run 读取并核验本地归档。`--to` 接受稳定版本号及 `v` 前缀；离线指定时必须与包内版本一致。升级拒绝降级，拒绝覆盖用户修改的受管文件。显式工具目录可用于首次安装；默认目录取实际运行程序所在目录，必须存在可识别安装记录。

旧 `update`、`bundle`、`completion`、`context`、`lifecycle`、`contract`、`profile`、`handoff`、`runtime`、`tracker`、`mcp`、`check`、`verify`、`trace`、`git` 等入口退役。`--plan`、`--apply`、`--out`、`--plan-file` 不再用于保存或执行项目计划。未知命令与退役参数返回错误，不自动转译或执行。
