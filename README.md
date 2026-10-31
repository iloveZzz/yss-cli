# yss

yss 是用于安装项目模板、按需补装技能和生成 Java 后端骨架的 Go CLI。现役 Profile 为 `spec`、`backend`；新项目默认 `--installation project`，仅安装技能时选择 `skills`。

```sh
yss init --root ./demo --profile spec --dry-run
yss init --root ./demo --profile spec
yss doctor --root ./demo
yss skills list --root ./demo
yss sync --root ./demo --dry-run
yss sync --root ./demo
```

已有工程使用 `attach --root <目录> --profile spec|backend`。`init`、`attach` 均须提供 root 和 profile；覆盖冲突会停止写入。既有项目沿用已保存的安装类型，`sync --installation project|skills` 可显式切换。写入命令的 `--dry-run` 只预览本次改动，不保存待应用计划；确认后重新运行原命令并去掉该参数。

当前命令只有：`init`、`attach`、`sync`、`skills list|ensure`、`scaffold ddd|mvc|catalog`、`upgrade`、`doctor`、`recover`、`version`、`help`。`--version` 与 `--help` 提供版本和帮助。旧生命周期、批准、合同、插件绑定、保存计划及 `update` 等命令已经退役，调用会失败；不提供隐式兼容执行。

```sh
yss help
yss skills ensure --help
yss scaffold catalog
yss scaffold mvc --help
yss upgrade --dry-run
yss upgrade
```

程序升级与项目同步是两件事：`upgrade` 更新 CLI，`sync` 将内嵌模板同步到项目。在线升级来自固定 GitHub 仓库的正式 Release，校验标准 `SHA256SUMS`、manifest schema 2、归档与载荷摘要及目标平台。离线升级使用 `--artifact <归档> --sha256 <摘要>`。首次切换到本版应由 Agent 按[构建、打包与安装](docs/build-install.md)安装，旧 CLI 的在线升级器不保证识别新包。

从模板工作树直接构建，无 Node/Python 包装脚本：

```sh
go run -tags yss_nobundle ./tools/bundle --source-root /path/to/yss-spec-project-template --out internal/bundle/assets
CGO_ENABLED=0 go build -trimpath -o /outside/yss ./cmd/yss
```

Go 版本按 `go.mod` 准备。生成的 Bundle 不进入版本库；`yss_nobundle` 仅用于生成器自举，不能用于发行 CLI。程序不要求阶段门禁、批准包、checkpoint 或持久测试套件；业务输入校验、路径保护、摘要、文件所有权及事务并发保护继续生效。未运行的构建或验证不宣称成功。

- [命令与日常操作](docs/cli-help.md)
- [构建、打包、安装与手动发布](docs/build-install.md)
- [安装类型与兼容范围](docs/compatibility.md)
- [技能查询与补装](docs/skills-discovery.md)
- [后端脚手架](docs/scaffold-go.md)
- [文档目录与历史资料](docs/README.md)
