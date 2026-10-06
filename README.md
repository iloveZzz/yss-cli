# yss

统一 Spec、Design、Backend、Frontend 的 Go CLI。当前版本为 1.1.0，提供上下文帮助、离线教程和稳定版在线升级；保留日常交付路由、验证与四类固定模板快照。发行资格见 [兼容边界](docs/compatibility.md)。

工程固定 Go 1.27.1；机器默认版本较低时使用 `GOTOOLCHAIN=go1.27.1`，下载工具链属于构建准备，编译后的 CLI 无此依赖。

帮助和离线教程：

```sh
yss -h
yss init --help
yss update apply -h
yss help lifecycle route
yss help tutorial
```

帮助不要求项目身份，不读取项目或访问网络。每个命令和子命令提供用途、参数、必要条件和示例；未知帮助路径返回 `ARGUMENT`（退出 2）。即使传入 `--json`，帮助仍输出文本。

编译：`CGO_ENABLED=0 go build -trimpath -o bin/yss ./cmd/yss`。测试：`go test -count=1 ./...`；适用平台增加 `-race`。

```sh
yss init --profile spec --root ./my-project --project-name 项目名称
yss doctor --root ./my-project --json
yss sync --root ./my-project --plan --out /tmp/yss-sync-plan.json
yss sync --root ./my-project --apply --plan-file /tmp/yss-sync-plan.json
yss context verify --root ./my-project --json
yss lifecycle query --root ./my-project --id work-unit.entry-triage --json
```

程序版本、协议、Profile 模板版本和旧 CLI 兼容版本分别存储。已有旧实例需要先 doctor/diff，再显式 migrate；不会用 1.0.0 与旧 Spec 3.5.10 比较并误判降级。计划绑定项目根、快照、生成结果与输入摘要；修改后重新计算摘要的任意计划仍会被拒绝。

四类 Bundle 由 Go 构建工具直接读取四个固定模板提交，嵌入程序供原生能力离线运行。Node/Python 治理脚本按各自职责继续维护，必要消费者逐项迁移和验证。旧 CLI 仅用于明确的历史识别、迁移、恢复及拒绝测试。阶段批准仍由当前生命周期合同决定。

```sh
go run ./tools/bundle --source-root /absolute/template-source --lock docs/source-lock.json --out internal/bundle/assets
yss bundle inspect --profile spec --json
yss bundle export --profile spec --out /absolute/new-bundle-directory --json
```

来源锁、Bundle v2、原生 metadata v2 分别记录模板提交、统一 CLI 身份、资产摘要和受管基线。两个插件消费固定二进制及公开 Bundle；binding、身份与受管文件进入同一保存计划和事务。详见 [退役合同](docs/cli-retirement.md)。

离线程序安装与项目迁移分开，先生成计划，再显式应用：

```sh
yss update plan --tool-root ./tools/yss --artifact /path/yss.tar.gz --sha256 <SHA-256> --out /path/install-plan.json --json
yss update apply --tool-root ./tools/yss --plan-file /path/install-plan.json --json
yss update status --tool-root ./tools/yss --json
yss update recover --tool-root ./tools/yss --json
yss update rollback --tool-root ./tools/yss --json
yss migrate plan --root ./old-project --out /path/migrate-plan.json --json
yss migrate apply --root ./old-project --plan-file /path/migrate-plan.json --json
yss migrate rollback --root ./old-project --json
```

程序恢复仅消费 `program-update` 事务；回退只针对最近一次成功程序安装，不跨过后来的成功事务。恢复开始还原后会完成保护性还原，避免取消把工具目录留在混合状态；进程被强制结束时可再次执行恢复。用户后来修改的文件会使整体回退停止。

运行记录使用独立 SQLite 目录。`runtime run/events/commands/pins --id <运行 ID>` 为只读查询；`runtime pin/unpin --id <运行 ID> --token <begin 返回的 token> --reason <原因>` 只变更该运行的保护记录，完成后仍可操作。记录查询和保护标记不表示生命周期批准，也不删除证据文件。

当前 checkpoint、批准、真实用户决定、实现合同、验证证据、战略交接及默认完整 project-ci 使用原生 Go 校验，接口见 [治理校验合同](docs/native-governance.md)。这些检查只消费既有资产，不执行业务验证命令或创建批准。

完整覆盖表、平台限制及剩余切换条件见 [迁移清单](docs/porting-status.md)。旧命令兼容与显式原生 API 用法见 [兼容适配](compat/README.md)。

本地打包六个平台：`go run ./tools/package <工程外新目录>`。包中分别记录“交叉编译”和“原生运行验证”；未提交源码和缺少平台运行证据的包只能用于预发布试用。

1.1.0 的发行平台为本机 `darwin/arm64`。只有该平台的固定二进制、真实验收收据和本次必要门禁通过，才装配正式包；其他平台的历史结果保留，未验证或失败结果不会标记为通过。六平台齐备不作为本次发行条件。

本次发行资格为 `local-platform-impacted-consumers`，包括本机原生接口、插件、恢复与安装契约；不包含无关全模板回归。已有失败及未执行项如实保留。

## 入门教程与 CLI 在线升级

在新目录选择一个 Profile，然后检查当前实例：

```sh
yss init --profile spec --root ./demo-spec --project-name 演示项目
yss init --profile design --root ./demo-design --project-name 演示设计
yss init --profile backend --root ./demo-backend --project-name 演示后端
yss init --profile frontend --root ./demo-frontend --project-name 演示前端
yss doctor --root ./demo-spec --json
yss diff --root ./demo-spec --json
```

项目模板同步使用 `sync` 的保存计划；资源补装先列出当前 Profile 实际支持的标识：

```sh
yss sync --root ./demo-spec --plan --out /tmp/yss-sync-plan.json
yss sync --root ./demo-spec --apply --plan-file /tmp/yss-sync-plan.json
yss skills list --root ./demo-spec --json
yss skills ensure yss-harness-upgrade --root ./demo-spec --plan --out /tmp/yss-skill-plan.json
yss skills --root ./demo-spec --apply --plan-file /tmp/yss-skill-plan.json
yss assets list --root ./demo-spec --json
```

计划输出文件必须不存在。输入变化或定制冲突时，先处理诊断并重新生成计划。旧实例需要显式 `migrate plan/apply`；旧未完成事务先用对应仓外固定旧执行器恢复。

升级 CLI 程序：

```sh
yss upgrade --check
yss upgrade
yss upgrade --to 1.1.0 --json
yss upgrade --tool-root ./tools/yss --json
```

默认查询 `iloveZzz/yss-cli` 的最新正式 Release，校验当前平台的发行资格、归档大小、SHA-256 与包内来源，再事务安装。`--to` 接受 `1.1.0` 或 `v1.1.0`，仅支持稳定版本并拒绝降级；`--version` 继续查询程序自身版本。`--check` 只查询，不下载或写入。

默认通过实际可执行文件解析安装根。例如 `~/.local/bin/yss` 链接至 `~/.local/share/yss/yss` 时，更新受管目录并保留链接。裸复制二进制缺少安装收据时会拒绝自动覆盖；显式指定空工具目录可首次安装。已是目标稳定包时不下载、不创建事务。未完成事务、用户修改、输入漂移或发行校验失败均停止安装。

`--json` 沿用 envelope v1 / protocol v1，`command` 为 `upgrade`，`result` 提供 `status`、`currentVersion`、`targetVersion`、`updateAvailable`、`platform`、`toolRoot`、`releaseUrl`、`archiveSha256` 及实际安装时的 `transaction`。检查状态为 `checked`；安装状态为 `installed` / `upgraded` / `unchanged`。参数错误退出 2；升级拒绝或失败退出 1，错误码包含 `VERSION`、`INSTALLATION`、`STATE`、`CONFLICT`、`ARTIFACT`、`DIGEST`、`NETWORK`、`CANCELLED`。应用期间沿用事务的 `INPUT_DRIFT` 和并发保护。

程序恢复、回退使用结果中的实际 `toolRoot`：

```sh
yss update status --tool-root ./tools/yss --json
yss update recover --tool-root ./tools/yss --json
yss update rollback --tool-root ./tools/yss --json
```

恢复和回退保留受管文件及权限；后续用户修改会使整体覆盖停止。项目 `sync/migrate` 与 CLI 程序安装分别使用自己的事务入口。

## 从 1.0.0 首次安装新版（macOS arm64）

`1.0.0` 没有 `upgrade`。从 [GitHub Release](https://github.com/iloveZzz/yss-cli/releases) 下载 `yss_1.1.0_darwin_arm64.tar.gz` 和 `checksums.json`，读取对应归档的 SHA-256；使用现有入口：

```sh
yss update plan --tool-root ./tools/yss --artifact /path/yss_1.1.0_darwin_arm64.tar.gz --sha256 <SHA-256> --out /tmp/yss-install-plan.json --json
yss update apply --tool-root ./tools/yss --plan-file /tmp/yss-install-plan.json --json
./tools/yss/yss version --json
```

已有受管安装应将 `--tool-root` 指向原工具目录；首次安装使用空目录。将新目录中的 `yss` 链接到 PATH 后即可直接运行 `yss upgrade`。独立 Node/Python 治理工具按各自职责继续维护。


## 命令帮助与安装一致性诊断

`yss` 或 `yss --help` 显示中文分组命令表、每条命令的用途与上手案例；`yss <命令/子命令> --help` 和 `yss help <命令/子命令>` 展示参数格式、默认值、条件及案例。`-h` 显示帮助，`-V` 等同于 `--version`。帮助始终输出文本，不读取项目、不访问网络、不写入资产。

```sh
yss init --help
yss help update apply
yss upgrade --help
yss help tutorial
yss -V --json
```

原生命令的未知命令、子命令、选项、无效取值或缺少参数值返回 `ARGUMENT`，退出码为 `2`，同时给出帮助入口和适用的拼写建议。比如 `yss upadate` 会建议 `update`，并说明在线程序升级使用 `upgrade`。建议不会自动执行。已有升级错误码和 JSON envelope 版本保持不变；其他治理和兼容消费者保留自身领域校验。

- `yss upgrade`：下载并事务安装稳定版 CLI；`--check` 只查询在线版本。
- `yss update plan/apply/status/recover/rollback`：离线程序安装、诊断及事务恢复。
- `yss sync`：将项目模板更新到程序内置固定 Bundle。

`yss update status --tool-root <实际安装目录> --json` 保留原有安装与事务信息，并新增 `installationConsistent` 和 `diagnostic`。诊断包含工具目录、安装记录版本、manifest 版本、逐文件 `expected/actual` 类型/摘要/权限、原因与下一步建议。`runningProgramMatchesRoot` 为 true 时才记录和比较 `runningVersion`；查询其他安装目录不会把当前程序版本当成该目录版本。

查询成功表示获得了状态事实；`installed: true` 表示存在可识别安装记录，不能单独证明文件一致。`installationConsistent: true` 也不授予正式发行资格或项目批准。诊断不会改写安装清单、覆盖文件或自动运行恢复命令。

遇到“受管程序文件与安装清单不一致”时，先查询错误中给出的实际目录。有未完成程序事务，检查状态和归档后使用 `update recover`。没有未完成事务但文件或版本漂移时，保留原目录与归档，选择不存在的新目录安装整个程序包：

```sh
# ./tools/yss-clean 必须不存在；下面是新安装目录示例。
yss upgrade --tool-root ./tools/yss-clean
./tools/yss-clean/yss version --json
./tools/yss-clean/yss update status --tool-root ./tools/yss-clean --json
```

核验一致后再调整 PATH 入口。离线案例见 `yss help tutorial`。只替换二进制、改写旧收据或强制覆盖会破坏来源和回退依据；现有摘要、来源、冲突及事务保护继续生效。
