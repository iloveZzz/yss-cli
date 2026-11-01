# YSS CLI

YSS 使用 Rust 安装项目模板、按需补装 Skills、生成 Java 空骨架和更新自身。用户运行时分发单个 `yss`（Windows 为 `yss.exe`），无需 Go、Bun、Node 或 Python。处理 Git 工作树时需要 Git；生成 Java 工程不执行 Maven，实际构建需要对应 JDK。

当前是 `4.0.0-dev` 开发实现，尚未正式发布。与旧 Go 安装不兼容：使用新目录，不迁移旧收据或事务。`spec`/`backend` 是两种 Profile；`project`/`skills` 是项目内的两种安装范围。

```text
yss init --root /absolute/new-project --profile spec --installation skills
yss attach --root /absolute/existing-project --profile backend --installation skills
yss skills list --root /absolute/project
yss skills ensure yss-validation --root /absolute/project
yss sync --root /absolute/project --dry-run
yss sync --root /absolute/project
yss doctor --root /absolute/project
yss recover --root /absolute/project
```

写命令直接执行；`--dry-run` 只预览，不创建根目录、锁、缓存或保存计划。未知文件不接管，即使内容相同；受管文件本地修改与来源修改冲突时整次拒绝。事务中断后先 `recover`，已提交事务只重试安全清理。

```text
cargo run --locked --bin yss-bundle -- --source-root /absolute/template --out assets/bundle.tar.gz
cargo build --locked --release --bin yss
```

Rust 工具链锁定在 `rust-toolchain.toml`。Bundle 来自实际模板内容并随程序内嵌，必须先生成。只分发用户程序，内部 `yss-bundle` 用于构建和打包。

- [命令与输出](docs/cli-help.md)
- [构建、发行包与安装](docs/build-install.md)
- [格式与平台范围](docs/compatibility.md)
- [技能选择与所有权](docs/skills-discovery.md)
- [DDD/MVC 脚手架](docs/scaffold.md)
