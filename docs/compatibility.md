# 格式与平台范围

本次为纯 Rust 重写，不支持旧 Go 安装格式、旧工具收据、旧事务、旧发行包或旧插件。旧目录保留原状，使用独立新目录安装；不提供迁移、兼容分支或通用回滚。

| 对象 | 身份 | 位置 |
| --- | --- | --- |
| 项目收据 | `yss.project` | `.yss/project.json` |
| 工具收据 | `yss.tool` | `.yss/tool.json` |
| 内嵌资源 | `yss.bundle` | 构建生成 `assets/bundle.tar.gz` |
| 发行清单 | `yss.release` | 归档根 `release-manifest.json` |
| 写入事务 | `yss.transaction` | `.yss/transactions/<id>` |
| 脚手架收据 | `yss.scaffold` | 生成工程 `.yss/scaffold.json` |
| CLI JSON | `yss.output` | stdout 单对象 |

以上格式都使用 `formatVersion: 1`，不复用旧 schemaVersion。输入采用严格 JSON/YAML 校验；未知版本、重复键、YAML 别名或标签、超深或过大内容被拒绝。

| 设计目标 | 完整 Rust target |
| --- | --- |
| macOS ARM64 | `aarch64-apple-darwin` |
| macOS Intel | `x86_64-apple-darwin` |
| Linux musl ARM64 | `aarch64-unknown-linux-musl` |
| Linux musl x86_64 | `x86_64-unknown-linux-musl` |
| Windows ARM64 | `aarch64-pc-windows-msvc` |
| Windows x86_64 | `x86_64-pc-windows-msvc` |

设计目标不表示全部已实机验证或有正式发行。当前开发验证与环境限制见构建文档。Windows 保留完整文件 ID，更新使用日志登记的旧程序保留、候选发布和恢复步骤；仍被旧进程占用的文件可能需要之后清理。

拒绝穿越符号链接、Windows reparse、路径别名、Git 子模块及嵌套工作树；明确指定的独立 worktree 根可以作为项目。普通非 Git 目录不要求 Git；Git 工作树内必须能可靠核验边界。
