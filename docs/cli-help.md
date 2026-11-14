# YSS CLI 使用与命令参考

本指南适用于 Rust YSS CLI `3.4.1` 源码，正式产物以 GitHub Release 为准。`3.3.0` 新增 `scaffold frontend`；`3.2.0` 已支持 `frontend` Profile，构建时需使用对应模板；此前的 `3.1.4` 构建仅支持 `spec`/`backend`。先运行 `yss version` 和 `yss --help`，命令、参数和内嵌模板以实际输出为准。模板文档更新不会改变已安装的程序。

## 选择操作

| 场景 | 命令 | 目标 |
| --- | --- | --- |
| 新建项目 | `init` | 不存在的目录或空目录 |
| 接入已有工程 | `attach` | 尚未接入 YSS 的已有目录 |
| 更新项目内资源 | `sync` | 已接入 YSS 的项目 |
| 查看或补装技能 | `skills list` / `skills ensure` | 已接入 YSS 的项目 |
| 新建 Java 后端骨架 | `scaffold catalog` / `scaffold ddd` / `scaffold mvc` | 全新的工程目录 |
| 新建 Vue 前端骨架 | `scaffold frontend` | 已有父目录下全新的工程目录 |
| 排查差异或恢复 | `doctor` / `recover` | 受管项目目录或 CLI 工具目录 |
| 本机 Spec 与任务可视化 | `workbench` | 个人数据目录，项目来源只读 |
| 查询任务和生命周期 | `workbench snapshot` / `query` / `lifecycle` | 已纳管记录 |
| 更新 CLI 本身 | `upgrade` | CLI 工具目录，与项目同步分开执行 |

`yss help <命令>` 或 `yss <命令> --help` 查看命令帮助，例如 `yss skills ensure --help`。写入命令默认执行；支持预览的命令可加 `--dry-run`，查看结果后重新运行并去掉该参数。预览不创建目标、锁或可保存的执行计划；在线升级预览仍会访问发行信息。

## 初始化或接入项目

首次使用先确定两个选项：

| 选项 | 值 | 内容 |
| --- | --- | --- |
| `--profile` | `spec` | 业务规格与产品设计 |
| `--profile` | `backend` | Java 后端研发 |
| `--profile` | `frontend` | Vue 3 前端研发 |
| `--installation` | `project` | 技能与必要项目资料；新安装默认值 |
| `--installation` | `skills` | 技能、宿主入口及必要引用资源；仍属于项目内安装，不是全局安装 |

初始化新项目：

```sh
yss init --root ./my-spec --profile spec --dry-run
yss init --root ./my-spec --profile spec
```

`init` 的目标必须不存在或为空；目标不存在时，其父目录必须已经存在。接入普通已有项目改用 `attach`，例如：

```sh
yss attach --root ./existing-service --profile backend --installation skills --dry-run
yss attach --root ./existing-service --profile backend --installation skills
```

已有 YSS 安装不能再次 `init` 或 `attach`；改用 `sync`。未知文件不会自动接管，遇到冲突应查看诊断并保留用户文件。

### 项目参数

| 参数 | 说明 |
| --- | --- |
| `--root <目录>` | `init`、`attach` 必填；`sync`、`skills` 和 `doctor` 默认当前目录 `.`。`recover` 也默认 `.` |
| `--profile spec\|backend\|frontend` | `init`、`attach` 必填；`sync`、`skills list`、`skills ensure` 和项目 `doctor` 可选，指定时须与已安装类型一致，不能用于切换类型 |
| `--installation project\|skills` | `init`、`attach` 新安装默认 `project`；`sync` 默认沿用原范围，显式指定时切换范围 |
| `--project-name <名称>` | `init`、`attach`、`sync` 的模板变量；新安装默认使用目标目录名 |
| `--business-domain <领域>` | 同上，模板中的业务领域；新安装默认 `待补充` |
| `--team-size <规模>` | 同上，模板中的团队规模；新安装默认 `待补充` |
| `--dry-run` | `init`、`attach`、`sync` 和 `skills ensure` 可用，只预览本次操作 |

已有安装的项目变量默认沿用原值；传入变量参数才更新对应值。项目变量不能为空，不能包含控制字符或模板占位符。带空格的值请按当前 Shell 的规则引用。

## 查看技能和同步

```sh
yss skills list --root ./my-spec
yss skills ensure setup-yss-harness --root ./my-spec --dry-run
yss skills ensure setup-yss-harness --root ./my-spec
yss sync --root ./my-spec --dry-run
yss sync --root ./my-spec
```

`skills list` 显示当前 Profile 的可用技能及安装状态。`skills ensure <名称...>` 至少接收一个技能名，也可连续提供多个名称；它会安装所选技能及所需依赖和资源。技能是否可选以实际列表为准，安装文件不等于宿主已经加载技能。

`sync` 使用当前 CLI **内嵌**的模板和技能，不会自动拉取模板仓库。要改变安装范围，可先预览 `yss sync --root ./my-spec --installation skills --dry-run`；确认后去掉 `--dry-run`。受管文件发生冲突时查看诊断，不要强制覆盖。

## 生成 Java 工程

先使用 `yss scaffold catalog` 查看当前内嵌资源中的平台标识、精确版本和配方证据，再选择 `ddd` 或 `mvc`。示例使用目录中列出的一个平台组合；实际版本和 Maven 坐标应根据项目环境确认：

```sh
yss scaffold ddd --project-name sample-app --base-package com.example.sample --output-dir . --platform-profile spring-boot-3.5-jdk17 --spring-boot-version 3.5.16 --java-version 17 --group-id com.example --project-version 1.0.0-SNAPSHOT --parent-group-id com.yss.cloud --parent-artifact-id yss-cloud-microservice --parent-version 3.2.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT --dry-run
```

将 `ddd` 改为 `mvc` 可选择 MVC 结构。实际生成时输出父目录必须存在，`<输出父目录>/<工程名>` 必须不存在，即使是空目录也不能覆盖。生成不自动运行 Maven。

| 参数 | 说明 |
| --- | --- |
| `--project-name` | 工程名，小写 kebab-case；也用于生成目标目录名 |
| `--base-package` | Java 基础包名，例如 `com.example.sample` |
| `--output-dir` | 输出父目录；实际生成时须已存在 |
| `--platform-profile` | `scaffold catalog` 中的平台标识 |
| `--spring-boot-version` | 与所选平台匹配的精确 Spring Boot 版本 |
| `--java-version` | 与所选平台匹配的 Java 主版本，如 `17` |
| `--group-id` / `--project-version` | 生成工程的 Maven groupId 和版本 |
| `--parent-group-id` / `--parent-artifact-id` / `--parent-version` | 父 POM 的 Maven 坐标和精确版本 |
| `--yss-components-version` | YSS 组件 BOM 的精确版本 |
| `--dry-run` | 只预览，不创建输出目录或工程 |

除 `--dry-run` 外，上表参数对 `ddd` 和 `mvc` 均为必填。不要把 catalog 中的配方记录当成本次工程的构建结果。

## 更新 CLI、诊断和恢复

在线升级默认使用 GitHub 正式发行；`--source gitlab` 可显式选择固定的 GitLab 来源。GitLab 需要配置 `YSS_GITLAB_CA_FILE` 和有读取权限的 `YSS_GITLAB_TOKEN`，失败后不会自动切换来源。以下尖括号内容需替换为实际值：

```text
yss upgrade --to <正式版本> --tool-root <工具目录> --dry-run
yss upgrade --artifact <发行归档> --sha256 <归档摘要> --tool-root <工具目录> --dry-run
yss doctor --root <项目或工具目录>
yss recover --root <项目或工具目录> --dry-run
```

| 参数 | 说明 |
| --- | --- |
| `upgrade --source github\|gitlab` | 在线来源，默认 `github`；不能与 `--artifact` 同用 |
| `upgrade --to <版本>` | 指定正式版本；在线省略时查询来源的 latest 发行并校验是否为正式版本，不符合时须显式指定版本；离线时如果指定则须与归档版本一致 |
| `upgrade --tool-root <目录>` | CLI 工具目录；省略时使用当前程序所在目录，该目录须已有 Rust 安装收据；首次安装须显式指定新目录或已有空目录 |
| `upgrade --artifact <归档>` | 本地发行归档；须与 `--sha256` 一起提供 |
| `upgrade --sha256 <摘要>` | 本地归档的 64 位小写 SHA-256 |
| `upgrade --dry-run` | 在线查询发行信息或离线校验归档，不安装程序 |
| `doctor --root <目录>` | 检查项目或工具安装及本地差异；默认当前目录 |
| `recover --root <目录> --dry-run` | 预览项目或工具的事务恢复；默认当前目录，去掉 `--dry-run` 执行 |

工具目录不能位于 Git 工作树内，也不能兼作项目目录。首次安装应使用已核验的新程序和发行归档，明确指定新目录或已有空目录作为工具目录；旧 Go 安装与 Rust 格式不兼容。升级 CLI 后，项目只有另行执行 `sync` 才会使用新程序内嵌的资源。

`recover` 处理未提交事务；对已提交事务只重试安全清理，不是已完成操作的通用回滚。中断后用户若又修改了相关文件，恢复可能报告冲突；保留现场并按诊断处理。

## 通用选项与输出

| 参数 | 说明 |
| --- | --- |
| `-h` / `--help` | 查看当前命令帮助 |
| `-V` / `--version` | 显示版本、目标平台、CLI 源码来源和内嵌 Bundle 摘要；建议单独运行 `yss --version` 或 `yss version`，带子命令时仍会先校验其必填项 |
| `--json` | 返回统一 JSON 信封；`--json=false` 关闭该格式 |

例如 `yss --json help init` 或 `yss init --help --json` 返回的 `result.help` 是帮助文本。JSON 信封的格式为：

```json
{"format":"yss.output","formatVersion":1,"command":"version","status":"ok","result":{},"error":null}
```

失败时 `status` 为 `error`、`result` 为 `null`；`error` 包含错误码、中文消息及可用的路径和上下文。未启用 `--json` 时，普通错误写入 stderr。成功退出码为 0，参数错误为 2，其他失败及取消为 1。

常见错误码：`ARGUMENT`、`UNSUPPORTED_FORMAT`、`INVALID_INPUT`、`SKILL_RETIRED`、`CONFLICT`、`UNSAFE_PATH`、`LOCKED`、`CONCURRENT_CHANGE`、`RECOVERY_REQUIRED`、`CANCELLED`、`NETWORK`、`NOT_READY`、`DIGEST_MISMATCH`、`UNSUPPORTED_PLATFORM`、`VERSION_CONFLICT`、`IO`。网络错误不会回显令牌、任意远程 URL 或远程错误正文。

遇到不支持的参数先核对版本和帮助；遇到文件、路径或摘要冲突时保留现场。旧插件、`plan/apply` 和通用回滚接口不属于该版本的命令范围。

CLI `3.3.0` 新增 `yss scaffold frontend --project-name my-frontend --output-dir .`，先核对实际 `yss scaffold frontend --help`。它生成最小 Vue 工程，不安装依赖；`init/attach --profile frontend` 仍只安装技能及资料。生成与运行边界见 [脚手架说明](scaffold.md)。

## 个人 Spec 工作台（当前源码新增）

`yss workbench` 启动本机浏览器工作台；`--no-open` 只打印入口，`--port 0` 自动分配端口，`--data-dir <绝对路径>` 指定个人数据目录。服务本身不接受 `--json`。旧版已安装程序不会自动获得这些命令。

```sh
yss workbench snapshot --json
yss workbench query --entity task --stage verification --json
yss workbench query --entity spec --activity all --include-closed --json
yss workbench lifecycle --spec <SpecID> --json
```

`query` 支持 `--entity spec|task`、`--q`、可重复的 `--project` / `--spec` / `--stage` / `--status`、`--activity active|inactive|all`、`--include-closed`、`--source-problems`、`--sort updatedDesc|titleAsc`。默认启用记录、隐藏终态、最近更新排序；显式选择 done/canceled 时会包含终态。snapshot/query/lifecycle/request 只读、不创建缺失目录；register/respond 写入个人数据。参数完整枚举以 `yss workbench query --help` 为准。

当前源码数据格式 v3 可只读打开 v1/v2，首次有效写入前备份原字节；旧程序不能直接读取 v3。纳管范围、独立进度语义、来源边界及恢复方法见[工作台说明](workbench.md)。

### 主 Spec、问题回复与确认（3.4.1 起）

```text
yss workbench register --root /absolute/project --source docs/spec/topic/spec.md --json
yss workbench request --spec-id <SpecID> --json
yss workbench respond --spec-id <SpecID> --question-id OQ-01 --question-digest <SHA256> --kind answer --answer "用户已明确给出的答复" --json
yss workbench respond --spec-id <SpecID> --question-id OQ-02 --question-digest <SHA256> --kind defer --reason "等待外部方案" --impact "本期不提供导入" --excluded-scope "批量导入" --review-at 2026-12-01 --review-utc-offset-minutes 480 --json
```

上述示例使用默认个人数据目录；若工作台以 `--data-dir` 启动，所有查询和写入命令都须沿用相同目录，例如 `yss workbench --data-dir /absolute/personal-data register ...`。Agent 应直接沿用工作台生成的更新要求中的目录，不另选默认库。

`register` 复用已登记项目和主 Spec，保留现有主 Spec 的启用状态，并更新正文元数据中的资源。仅同目录且没有独立 `yss-spec` 元数据的 `interaction.md` 可自动合并为附属文档并停用旧记录；其他参考规格保留独立记录，个人备注、任务、确认和历史不删除。登记文档有独立 `yss-spec` 元数据时保留自身来源；否则同目录有 `spec.md` 时以它为主来源，发现目录包括 `docs/spec`。未提供元数据时可从相对 Markdown 链接及同目录 `interaction.md` 识别资源。

`respond` 只能记录用户真实答复或已同意的延期，不能代作业务决定；要求当前问题摘要，返回 `result.responseId`。延期须有原因、影响、排除范围，以及 `--review-at YYYY-MM-DD` 或 `--review-when 条件`；日期按 `--review-utc-offset-minutes`（默认 UTC）解释，关键问题须完整排除其受影响范围。`request` 的 `result.text` 含主文件、资源、最新回复 ID 与摘要，供 Agent 实际回写正文、AC、交互和原型后引用。答复不等于解决。

运行中的工作台通过个人目录中私有 `session.json` 调用既有本地鉴权接口写入，保持单进程锁与 revision 校验；服务未运行才直接打开 Store。不要分享该会话文件。内容确认仅由工作台用户明确操作，绑定范围和产物摘要，不能从个人 Status=done 推断。

这些能力从 3.4.1 起提供，未升级的旧版不会自动拥有；先以实际 `yss workbench --help` 核对。
