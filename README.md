# yss

统一 Spec、Design、Backend、Frontend 的 Go CLI。当前本地预发布版本为 1.0.0-alpha.3，正在分批替换原有执行链；稳定发布目标和剩余覆盖见 [兼容边界](docs/compatibility.md)。

工程固定 Go 1.27.1；机器默认版本较低时使用 `GOTOOLCHAIN=go1.27.1`，下载工具链属于构建准备，编译后的 CLI 无此依赖。

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
