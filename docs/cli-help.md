# 命令与输出

以下适用于 Rust `3.1.0`，以当前程序 `yss help` 与各子命令 `--help` 为准。`--json` 为全局选项，`--version`/`-V` 等价于 `version`。

| 命令 | 输入与行为 |
| --- | --- |
| `init` | `--root`、`--profile spec|backend` 必填；只接受新目录或空目录 |
| `attach` | `--root`、`--profile` 必填；保留普通已有工程及未知文件 |
| `sync` | `--root` 默认 `.`；沿用安装身份和变量，显式 `--installation` 切换范围 |
| `skills list` | `--root` 默认 `.`；列出可用、默认、显式选择、依赖闭包和实际安装状态 |
| `skills ensure <名称...>` | 位置参数至少一个；安装技能及依赖、引用资源 |
| `scaffold catalog` | 只读平台、精确版本和已有配方证据 |
| `scaffold ddd` / `mvc` | 按明确平台与 Maven 坐标生成全新工程，详见脚手架文档 |
| `doctor` | `--root` 默认 `.`；识别项目或工具收据，显示文件差异与事务 |
| `recover` | `--root` 默认 `.`；恢复未提交事务，已提交事务仅清理 |
| `upgrade` | 在线默认 GitHub，可选 `--source github|gitlab`、`--to <正式版本>`；离线 `--artifact` 与 `--sha256` 必须成对；`--tool-root` 可选 |
| `version` | 实际版本、target、CLI 来源及 Bundle 摘要 |

`init/attach/sync` 可使用 `--installation project|skills`、`--project-name`、`--business-domain`、`--team-size`。安装后不能切换 Profile。新安装默认 project。

写命令支持 `--dry-run`，仅计算本次预览。预览结果不保存为可执行计划；之后直接执行时重新读文件。在线升级预览仅访问元数据、SHA256SUMS 和 HEAD；离线预览校验完整本地归档。`--source` 仅适用于在线升级，不与 `--artifact` 混用。没有 `--force` 或旧 plan/apply 接口。

JSON stdout 始终为单个对象：

```json
{"format":"yss.output","formatVersion":1,"command":"version","status":"ok","result":{},"error":null}
```

失败时 `status` 为 error，result 为 null，error 包含 code、中文 message、path、context。默认人类错误写 stderr。成功退出 0；参数错误 2；其他失败及取消 1。网络错误不会回显令牌、任意远程 URL 或远程错误正文。

主要错误码：ARGUMENT、UNSUPPORTED_FORMAT、INVALID_INPUT、SKILL_RETIRED、CONFLICT、UNSAFE_PATH、LOCKED、CONCURRENT_CHANGE、RECOVERY_REQUIRED、CANCELLED、NETWORK、NOT_READY、DIGEST_MISMATCH、UNSUPPORTED_PLATFORM、VERSION_CONFLICT、IO。
