# YSS CLI

YSS 使用 Rust 安装项目模板、按需补装 Skills、生成 Java / Vue 空骨架和更新自身。用户运行时分发单个 `yss`（Windows 为 `yss.exe`），无需 Go、Bun、Node 或 Python。处理 Git 工作树时需要 Git；生成 Java 工程不执行 Maven，实际构建需要对应 JDK。

当前源码版本为 Rust `3.3.0`，内嵌模板以实际 Bundle 为准；发行目标为 macOS ARM64 与 Windows x86_64，正式产物以 GitHub Release 为准。与旧 Go 安装不兼容：使用新目录，不迁移旧收据或事务。`spec`/`backend`/`frontend` 是三种 Profile；`project`/`skills` 是项目内的两种安装范围。

`3.2.0` 新增 `frontend` Profile，构建时需使用包含该 Profile 的模板。此前的 `3.1.4` 构建仅支持 `spec`/`backend`，模板文档更新不会改变已安装程序；使用前查看实际 `yss help init`。

## 快速开始

先查看当前程序版本、命令和初始化参数。新项目使用 `init`，已有工程使用 `attach`；先加 `--dry-run` 预览，确认后去掉该参数执行。

```text
yss version
yss --help
yss help init
yss init --root ./my-project --profile spec --dry-run
yss init --root ./my-project --profile spec
yss skills list --root ./my-project
yss sync --root ./my-project --dry-run
```

初始化、已有项目接入、技能、脚手架、升级及参数示例见[命令与输出](docs/cli-help.md)。写命令直接执行；`--dry-run` 只预览，不创建根目录、锁、缓存或保存计划。未知文件不接管，即使内容相同；受管文件本地修改与来源修改冲突时整次拒绝。事务中断后先 `recover`，已提交事务只重试安全清理。

```text
cargo run --locked --bin yss-bundle -- --source-root /absolute/template --out assets/bundle.tar.gz
cargo build --locked --release --bin yss
```

Rust 工具链锁定在 `rust-toolchain.toml`。Bundle 来自实际模板内容并随程序内嵌，必须先生成。只分发用户程序，内部 `yss-bundle` 用于构建和打包。

- [命令与输出](docs/cli-help.md)
- [构建、发行包与安装](docs/build-install.md)
- [格式与平台范围](docs/compatibility.md)
- [技能选择与所有权](docs/skills-discovery.md)
- [Java / Frontend 脚手架](docs/scaffold.md)

`3.3.0` 新增 `yss scaffold frontend --project-name my-frontend --output-dir .`，内嵌 Template `5.3.1`；先核对实际 `yss scaffold frontend --help`。它生成最小 Vue 工程，不安装依赖；`init/attach --profile frontend` 仍只安装技能及资料。生成与运行边界见 [脚手架说明](docs/scaffold.md)。
