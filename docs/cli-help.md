# YSS CLI 使用与命令参考

本指南适用于 Rust YSS CLI `3.18.1` 源码，正式产物以 GitHub Release 为准。`3.18.1` 为命令参数与工作台进度请求提供可定位的错误提示，并将工单配置迁移为项目自有。`3.18.0` 支持 Pi 连续会话、按轮次审阅和接收成果，并在隔离续办中提供两侧材料版本。`3.17.1` 支持在工作台为本次 Pi Agent 新运行搜索和选择本机模型。`3.17.0` 统一工作台加载反馈，完善 Pi 配置目录与模型就绪检查，并在 Spec 详情标明关联资料仍采用的已确认版本。`3.16.1` 更新工作台输入聚焦以及内嵌模板原型示例；命令接口不变。`3.16.0` 按 Spec 的实际材料、检查范围和目标版本处理确认及交付，并补充 Pi 就绪诊断。`3.15.0` 修正技能安装状态，并完善失效答复复查和续办成果校验。`3.14.0` 更新 Spec 自动发现、已构建原型预览和 Pi 排队前的 Git 检查。`3.13.0` 在 macOS ARM64 切换到 Pi Agent 并更新执行会话。`3.12.0` 增加自动生命周期汇总和 `workbench progress` 上报入口。`3.11.0` 增加工作台工程维护、隔离运行、成果接收和备份恢复。`3.3.0` 新增 `scaffold frontend`。`3.2.0` 已支持 `frontend` Profile，构建时需使用对应模板。此前的 `3.1.4` 构建仅支持 `spec` 和 `backend`。先运行 `yss version` 和 `yss --help`，以实际命令、参数和内嵌模板为准。

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
| 本机 Spec 与工程工作台 | `workbench` | 个人数据、工程维护与隔离运行入口 |
| 查询任务和生命周期 | `workbench snapshot` / `query` / `lifecycle` | 已纳管记录 |
| 上报生命周期进展 | `workbench progress set-goal` / `report` / `confirm` / `reconcile` | 主 Spec 的目标、证据和明确确认 |
| 更新 CLI 本身 | `upgrade` | CLI 工具目录，与项目同步分开执行 |

`yss help <命令>` 或 `yss <命令> --help` 查看命令帮助，例如 `yss skills ensure --help`。确认子命令帮助列出 `--dry-run` 后，可加该参数预览。查看结果后去掉该参数执行。预览不创建目标、锁或可保存的执行计划。在线升级预览仍会访问发行信息。使用独立的 `yss version --json` 核对正在运行的程序及内嵌 Bundle。

## 初始化或接入项目

首次使用先确定两个选项：

| 选项 | 值 | 内容 |
| --- | --- | --- |
| `--profile` | `spec` | 业务规格、产品设计与产品 Home 工程会话入口 |
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

这些命令负责安装文件与技能，完整工程接入由 Agent 按已安装的 `setup-yss-harness/references/project-onboarding.md` 协同完成。仅安装、升级 CLI 或补装技能，不自动初始化 Git、配置远程或设置 Issue tracker，也不能把安装成功报告成工程协作已就绪。

用户要求完整工程初始化或接入时，依次核实以下事项；已有信息与有效配置直接复用：

1. 检查真实 Git 根、worktree/submodule、当前改动和 fetch/push remote。目标目录没有 `.git` 不代表不属于父仓；monorepo 子工程不另建嵌套仓。
2. 已知远程先核实项目身份、访问情况与是否已有内容。非空远程先复用 checkout 或按请求 clone，再 attach/sync；访问失败不能按空仓生成。新的独立空工程可先 init，或先 scaffold 再 attach，然后按授权建立本地仓；已有本地仓直接复用。先执行 `git init` 会让目录不再满足 init 的空目录要求，此时使用 attach。
3. 核实并按授权配置远程。已有不同地址、多个 remote 或 fetch/push 目标不同时先明确选择，不静默覆盖；配置本地 remote 不等于托管项目已创建或可访问。完整接入不自动包含提交、推送或创建远程项目。
4. 配置目标工程的 Issue tracker，按下文规则保留既有选择或采用可核实的首次默认；分别报告项目、Issues 功能及访问能力。
5. 按需复用或补齐 Home 地图与入口，链接稳定工程身份和实际配置入口；没有 Home 时无需补造关系。

Issue tracker 位于消费工程 `.template-spec/agents/issue-tracker.md`，与 Git remote 分别维护。已有有效 tracker（含集中项目、Local Markdown、root、历史字段与用户正文）继续沿用，除非用户明确要求更改。尚无已确认 tracker 时优先使用本次明确选择；仅对完整新工程、且已核实唯一 GitHub/GitLab 代码托管项目的情况，默认采用该项目 Issues，并向用户明确报告实例与项目。模板占位、登录默认仓、CLI 发行仓和模板仓不构成业务目标；多候选、集中管理意图或平台不明时说明缺口，不猜测、不因远程失败自动改用 Local Markdown。后续票据操作读取目标工程的 `.template-spec/agents/issue-tracker-operations.md`；配置文件和共享方法是否可安装，以实际 CLI 内嵌 Bundle 为准。

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

`skills list` 和 `skills ensure` 的 `--root` 指向已接入的项目。`skills list --json` 返回当前 Profile 可选技能 `result.available`，以及按技能名索引的受管状态 `result.installed`。受管状态按项目 `.agents/skills/<名称>/SKILL.md` 的安装收据和现场文件判断，工作台使用同一结果。

| 工程现场 | CLI 状态 | 工作台 |
| --- | --- | --- |
| 收据没有登记入口，工程也没有可核实的普通入口文件，包括只有借用资料的情况 | `not-installed` | 未安装 |
| 入口由工程自行维护，收据只登记了借用资料或没有登记此目录的文件 | `unmanaged` | 工程自行维护 |
| 受管入口及其目录内的受管文件齐全，与收据匹配 | `installed` | 已安装 |
| 受管入口或该技能的一份受管文件缺失，或相应位置被目录、链接等占用 | `missing` | 文件缺失 |
| 受管文件齐全，其中有本地修订 | `modified` | 本地定制 |

查询只核实工程中的普通文件，保留并跳过链接指向的内容。需要定位收据差异时，可用 `yss doctor --root <工程> --json`。例如入口文件被同名目录替换，技能状态为 `missing`，doctor 会将这个仍被占用的路径列为 `modified`。要给出点名写法，还需确认目标宿主启用的入口及其来源。宿主通过链接或个人目录发现的技能，可以与工程的安装状态不同。`skills ensure <名称...>` 可以一次补装一个或多个技能及其所需依赖和资源。

退役技能目录由链接占用时，`result.diagnostics[].paths` 会指出链接本身的位置。查询保留这个现场，同时列出其他技能的状态。

`sync --dry-run` 预览当前 CLI **内嵌**的模板和技能会给项目带来的变更。`result.changes` 列出计划动作，`result.status` 为 `unchanged` 时无需同步。要改变安装范围，可先运行 `yss sync --root ./my-spec --installation skills --dry-run`，检查移出新范围的旧受管文件是否会删除，再按预期执行。受管文件发生冲突时按诊断逐项核实并保留项目定制。

## 生成 Java 工程

先使用 `yss scaffold catalog` 查看内嵌默认配方、平台和证据。MVC 与 DDD 共用参数补全规则，当前默认 YSS `4.0.0-SNAPSHOT` / Boot `4.1.2` / Java `17`。简短命令只需五项工程参数，工程自己的版本继续显式指定。

```sh
yss scaffold mvc --project-name sample-app --base-package com.example.sample --output-dir . --group-id com.example --project-version 1.0.0-SNAPSHOT --dry-run
yss scaffold ddd --project-name sample-app --base-package com.example.sample --output-dir . --group-id com.example --project-version 1.0.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT --dry-run
```

第二条命令选用当前唯一的 Parent3.2 / Boot3.5.16 / Java17 配方。将 `ddd` 与 `mvc` 互换可选择工程结构。完整自定义示例同样适用。

```sh
yss scaffold ddd --project-name sample-app --base-package com.example.sample --output-dir . --group-id com.example --project-version 1.0.0-SNAPSHOT --platform-profile spring-boot-3.5-jdk17 --spring-boot-version 3.5.16 --java-version 17 --parent-group-id com.example --parent-artifact-id custom-parent --parent-version 1.0.0 --yss-components-version 3.2.0-SNAPSHOT --dry-run
```

实际生成时输出父目录必须存在，`<输出父目录>/<工程名>` 必须不存在，即使是空目录也不能覆盖。生成不自动运行 Maven。

| 参数 | 说明 |
| --- | --- |
| `--project-name` | 必填，工程名使用小写 kebab-case，也用于目标目录名 |
| `--base-package` | 必填，Java 基础包名，例如 `com.example.sample` |
| `--output-dir` | 必填，输出父目录，实际生成时须已存在 |
| `--group-id` / `--project-version` | 必填，工程自己的 Maven groupId 和版本 |
| `--platform-profile` | 可选，`scaffold catalog` 中的平台标识 |
| `--spring-boot-version` | 可选，与所选平台匹配的精确 Spring Boot 版本 |
| `--java-version` | 可选，与所选平台匹配的整数 Java 主版本，如 `17` |
| `--parent-group-id` / `--parent-artifact-id` / `--parent-version` | 可选，父 POM 的 Maven 坐标和精确版本 |
| `--yss-components-version` | 可选，YSS 组件 BOM 的精确版本 |
| `--dry-run` | 只预览，不创建输出目录或工程 |

七项技术参数按显式值共同约束。默认配方满足约束时补齐，否则只接受唯一的完整配置。多个候选会列出可区分参数和值。只给 Boot3 平台时需进一步选择 3.0 或 3.2。没有匹配配方时，自定义 Parent G/A/V、BOM 版本须完整，并用显式平台、Boot、Java 约束唯一定位平台。完整显式旧命令继续保留原值，包括 Boot4.1.1。拒绝空技术值、版本范围、占位符及平台/Java 不匹配。

catalog 的 `defaultRecipeId` 可为 `null`，旧目录按唯一匹配补全。`requiredFlags` 只有五项工程参数。生成及 dry-run 的 `result.input` 和实际生成收据的 `input` 展示完整解析值。普通报告和 `--json` 相同。

catalog 的 recipe 返回 `verificationScope`，旧记录缺少范围时为 `{}`，`evidenceStatus` 和 `blockers` 保持原值。生成报告按 Parent 完整 GAV、`com.yss.cloud:yss-components-bom:<版本>`、平台标识和 Boot 精确版本匹配记录。只有匹配记录为 `verified` 且范围非空时省略 `warnings`。其他合法组合会收到状态/范围限制、跨平台未验证或没有登记记录的非阻断提示，仍可生成。新 4.0 配方仍为 `candidate`。本地源码构件的 MVC/DDD 构建、启动监听及代表性 H2/PostgreSQL 消费已验证。DDD 缺失路由返回 500/A0101，仅计监听观察。远程构件发布与解析、Windows 原生运行仍未验证。

普通报告和 `--json` 包含相同诊断信息，`warnings` 为空时省略。它仅说明历史 catalog 证据，不证明当前 SNAPSHOT 字节或本次目标工程构建与运行通过。`nextSteps` 保留后续行动指引，dry-run 时仍为 `[]`。

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

项目的 `doctor --json` 对照安装收据和当前文件，返回 `result.installation`、`result.differences` 和 `result.transactions.pending`。`installation` 为 `null` 表示没有项目安装收据，`status: ok` 表示诊断命令执行成功。要判断当前 Bundle 会更新哪些文件，使用 `sync --dry-run`。

先用 `doctor` 核对待恢复事务，再用 `recover --dry-run` 查看拟恢复的操作。`recover` 处理未提交事务，对已提交事务只重试安全清理。中断后用户若又修改了相关文件，按实际冲突诊断处理并保留现场。

3.6.0 支持保留目录身份变化后的已提交历史：事务元数据须完整有效，私有材料须全部清理。`doctor` 的事务状态新增 `retainedHistory` ID 数组，历史仍列在 `transactions`，不进入 `pending`；没有历史时输出 `[]`。仅有这类历史时，`recover` 返回 unchanged，不创建锁、不写日志、不清理原材料。身份不匹配的未完成或 rolled-back 事务、有残留或损坏的记录仍拒绝处理。

保留历史不代表项目文件可覆盖。`sync`、`skills ensure` 和工具升级仍执行原有收据、摘要、文件所有权及冲突检查；本地定制与新模板冲突时仍返回 `CONFLICT`。

## 安装结果与完整工程接入指引

3.6.0 在 init/attach 和三种 scaffold 的成功报告中首次新增 `nextSteps`。当前源码细化了完整工程接入提示；旧安装程序不会因模板文档更新获得新文案或新资源，须核对实际版本、帮助与内嵌 Bundle。

init/attach 的成功提示指向 `setup-yss-harness` 的 `references/project-onboarding.md`，说明真实 Git 边界、本地仓复用或创建、远程核实、Issue tracker 和按需 Home 的接续工作。仅安装 CLI/skills 不自动进入完整接入。现有 `configure-home`、`review-repository-role`、`review-upstream` ID 保留，新增 `complete-project-onboarding` 与 `configure-issue-tracker` 文字指引；Spec project 按实际职责核实 Home，Spec skills 保持原仓职责，Backend/Frontend 复用已知上游入口。init/attach 的后续指引由 Agent 按实际工程继续处理。工作台“产品 Home”可读取已有地图并保存本机绑定，地图内容由项目继续维护。

scaffold 仅生成源码，需要技能时建议显式 `attach --profile backend|frontend --installation skills`，随后按同一工程接入参考继续已授权工作。原 `attach-skills` ID 与可解析命令保留，并附加上述两条接入文字指引。返回的 `command` 包含 `program` 和独立字符串数组 `args`，不应拼接为 shell 字符串；program 是调用名，Agent 应沿用本次已核验的二进制绝对路径执行 args。非 UTF-8 路径省略 command，保留文字指引。生成成功后 attach 失败时保留源码与原入口，按实际冲突处理。

dry-run、sync、skills ensure 的 `nextSteps` 为 `[]`；错误或取消仍返回原错误，无成功指引。普通报告与 JSON 的 result 含相同字段，外层 `yss.output` 仍为版本 1。新安装仍默认 project，范围切换需显式指定；skills 也会安装宿主入口，可能与已有规则冲突。

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

命令行参数解析失败时，`ARGUMENT` 提示缺失、冲突或不合法的公开参数信息。`error.context.helpCommand` 指向对应命令的帮助。普通错误正文也提示该命令。

常见错误码：`ARGUMENT`、`UNSUPPORTED_FORMAT`、`INVALID_INPUT`、`SKILL_RETIRED`、`CONFLICT`、`UNSAFE_PATH`、`LOCKED`、`CONCURRENT_CHANGE`、`RECOVERY_REQUIRED`、`CANCELLED`、`NETWORK`、`NOT_READY`、`DIGEST_MISMATCH`、`UNSUPPORTED_PLATFORM`、`VERSION_CONFLICT`、`IO`。网络错误不会回显令牌、任意远程 URL 或远程错误正文。

遇到不支持的参数先核对版本和帮助；遇到文件、路径或摘要冲突时保留现场。旧插件、`plan/apply` 和通用回滚接口不属于该版本的命令范围。

CLI `3.3.0` 新增 `yss scaffold frontend --project-name my-frontend --output-dir .`，先核对实际 `yss scaffold frontend --help`。它生成最小 Vue 工程，不安装依赖；`init/attach --profile frontend` 仍只安装技能及资料。生成与运行边界见 [脚手架说明](scaffold.md)。

## 个人 Spec 工作台

`yss workbench` 启动本机浏览器工作台；`--no-open` 只打印入口，`--port 0` 自动分配端口，`--data-dir <绝对路径>` 指定个人数据目录。服务本身不接受 `--json`。关闭浏览器后活动作业继续运行，退出 CLI 会停止服务并中断作业。旧版已安装程序不会自动获得本轮源码增强。

```sh
yss workbench snapshot --json
yss workbench query --q "$YSS_SPEC_TITLE" --activity all --include-closed --json
yss workbench query --entity task --stage verification --json
yss workbench query --entity spec --activity all --include-closed --json
yss workbench query --entity spec --content-status ready --reminder ready --json
yss workbench query --entity spec --needs-reconfirmation --json
yss workbench query --entity spec --progress-basis effective --attention needsReview --json
yss workbench lifecycle --spec <SpecID> --json
```

`query` 支持 `--entity spec|task`、`--q`、可重复的 `--project` / `--spec` / `--stage` / `--status`、`--activity active|inactive|all`、`--include-closed`、`--source-problems`、`--sort updatedDesc|titleAsc`，以及可重复的 `--content-status unmarked|draft|ready|confirmed`、`--reminder decision|research|overdue|writeback|ready` 和布尔参数 `--needs-reconfirmation`。`--progress-basis effective` 使用自动证据与当前版本人工更正汇总的有效进度；缺省 `personal` 延续个人标记语义。`--attention` 可重复传入 `awaitingConfirmation`、`unverified`、`needsSync`、`needsReview`、`conflict`。默认启用记录、隐藏终态、最近更新排序；显式选择 done/canceled 时会包含终态。snapshot/query/lifecycle/request 只读、不创建缺失目录。参数完整枚举以 `yss workbench query --help` 为准。

`YSS_SPEC_TITLE` 使用要定位的规格名称。`query` 从 `result.items[]` 读取匹配的 `specId`、`projectId`。`lifecycle` 的 `result.stages` 展示个人阶段与任务计数，`result.progress.stages` 展示基于证据和当前目标的有效阶段进度。单纯查看使用 `lifecycle`，`progress reconcile` 会保存写入回执并重新核对已登记事实。

首次登记时，`register --root` 指定工程根，`--source` 使用相对该根的主 Spec 路径。成功后从 `result.specId` 和 `result.projectId` 取得身份。接续时使用 `workbench request --spec-id "$YSS_PROGRESS_SPEC_ID" --json`。`result.text` 供 Agent 或人员直接阅读，`result.projectRoot`、`result.sourcePath` 和 `result.questions` 可供结构化处理。使用自定义工作台目录的所有命令沿用同一 `--data-dir`。

### 上报生命周期进展

`3.12.0` 新增以下入口，安装程序须先核实 `yss workbench progress --help`。

```sh
yss workbench --data-dir /absolute/personal-data progress set-goal --input /absolute/goal.json --request-id goal-request-001 --json
yss workbench --data-dir /absolute/personal-data progress report --input /absolute/report.json --request-id report-request-001 --json
yss workbench --data-dir /absolute/personal-data progress confirm --input /absolute/confirmation.json --request-id confirm-request-001 --json
yss workbench --data-dir /absolute/personal-data progress reconcile --spec-id <SpecID> --request-id reconcile-request-001 --json
```

`--input -` 从标准输入读取 JSON，最大 1 MiB。写入沿用同一 `data-dir`，优先交给运行中的服务；离线写入使用现有独占锁。`--request-id` 标识一次逻辑操作，重试沿用原 ID 和原载荷。同 ID 不同内容返回冲突。响应不确定时先查询回执，命令错误中的 `requestId` 可用于核对。每次成功响应返回统一信封和事务 revision。

`set-goal` 的 JSON 示例见对应 `--help`。使用 `result.spec.content.artifactDigest` 作为新目标的 `specDigest`。选择至少一个适用阶段，目标类型为 `environment` 时填写 `target.reference`。上报前对同一数据目录运行 `yss workbench lifecycle --spec "$YSS_PROGRESS_SPEC_ID" --json`，`YSS_PROGRESS_SPEC_ID` 使用已登记的 Spec ID。从 `result.spec.record.id`、`result.progress.goalVersion` 和 `result.progress.goal.specDigest` 分别取得报告的 `specId`、`goalVersion` 和目标采用的 `specDigest`。

以下 `report.json` 示例用于目标范围内的规格阶段。替换占位值并按实际进展填写。`id` 标识一份报告，重试使用原报告 `id`、`--request-id` 和原 JSON，新报告使用新的 `id` 和 `--request-id`。

```json
{
  "id": "<本次报告的稳定 ID>",
  "specId": "<实际 Spec ID>",
  "goalVersion": "<实际目标版本>",
  "specDigest": "<目标采用的 Spec 摘要>",
  "stage": "spec",
  "role": "spec",
  "progress": "doing",
  "validity": "current",
  "summary": "<已取得的实际进展>"
}
```

`role` 为 `spec` 时可省略 `projectId`，`backend`、`frontend` 和 `integration` 须填目标中与该角色匹配的实际工程 ID。按实际情况附 `scopeRefs`、`versions` 和 `evidence`。每个证据项填写 `kind`、`label`、`reference` 和可选的 `outcome`。`versions` 可从 `result.progress.currentVersions` 选择本次采用的工程版本，工作树摘要覆盖 Git diff 与未忽略的未跟踪文件。

局部报告可追加 `inputPaths`。执行检查前使用 `yss workbench progress observe-inputs --project <工程ID> --path <工程相对路径> --json`，可重复指定 `--path`。将输出的 `result.inputs` 和 `result.version` 分别加入 `inputPaths` 和 `versions` 数组。核实检查前后采用的是同一份输入，保留检查前采集的 `observedDigest`。输入覆盖实际共享代码、配置及生成输入，包括被 Git 忽略的文件。保存报告时会校验摘要，变化后需要重新检查。联调、合并和环境结果继续核对精确版本组合。

`confirm` 的 JSON 示例见对应 `--help`。确认取同一响应的 `result.progress.goalVersion`、原样 `result.progress.goal.scope` 和 `result.progress.evidenceDigest`。`source` 记录用户明确决定的出处。规格、验证和最终目标确认由真实用户决定产生。阶段证据、目标或版本变化后重新核实适用性。人工更正在页面填写原因、绑定当前版本，可恢复自动判定。

详细 JSON 约定随模板分发到 `.template-spec/agents/workbench-progress.md`。工作台生成的 Agent 请求也携带当前身份和实际上报程序路径。能力缺失或报告失败时继续原已授权工作，记录待同步材料后核实补报。

### 核实一个 Spec 的关联材料

```sh
yss workbench review-materials --input /absolute/material-review.json --request-id material-review-001 --json
```

先对同一数据目录运行 `yss workbench lifecycle --spec "$YSS_PROGRESS_SPEC_ID" --json`。`specId` 取 `result.spec.record.id`，`observedDigest` 取 `result.spec.content.materialReviewDigest`。该摘要覆盖当前来源、已确认版本和已有材料绑定。绑定或来源发生变化时重新读取并核实。材料目标路径取 `result.spec.resources` 各项的 `record.target`，或 `result.spec.content.materialChanges` 各项的 `target`。

以下 `material-review.json` 示例记录一项采用新版的材料核实。替换占位值，`reason` 写本次具体核实依据。相同写入重试沿用 `--request-id` 和原 JSON。

```json
{
  "specId": "<实际 Spec ID>",
  "observedDigest": "<当前材料核实摘要>",
  "reason": "<具体核实依据>",
  "adoptLatest": ["<已关联且当前可读的本地材料路径>"]
}
```

- `adoptLatest` 填本次采用的新版材料路径，材料须已关联且当前可读。`keepConfirmed` 填仍采用原确认版本的共用资料或依赖的路径，须有原确认且当前材料可核对。每个路径只选一种处理方式。
- 普通核实省略 `bindings`，沿用已有绑定。调整绑定时传入完整显式绑定列表，每项填当前已关联的 `target` 和 `usage`。可选值为 `reference`（普通参考）、`follow`（整项属于本 Spec 的确认材料）和 `dependency`（实际依赖）。`dependency` 填可核对的 `scopeRef`，文本可另填 `section`，其 `start` 和 `end` 使用唯一完整行。候选对比和确认快照采用该选区，包含起止行。

该命令记录适用性和材料采用依据。新版内容确认仍在工作台由用户完成。已有确认、交付决定和问题答复继续按各自版本读取。`progress set-goal`、`progress report`、`progress confirm` 和 `review-materials` 的字段格式错误会提示对应的 `--help`，缺少顶层必填字段时会列出字段名。

当前源码使用 SQLite 保存个人数据。旧 JSON v1/v2/v3 的纯查询保持只读；首次启动新版工作台或执行写入命令时先备份原字节并迁移，即使该业务操作最后没有状态变化也会完成迁移。迁移后 `state.json` 仅为存储标记，旧版程序不能读写同一数据目录。纳管范围、独立进度语义、来源边界及恢复方法见[工作台说明](workbench.md)。

### 当前源码的浏览器操作

- 工程详情和“设置”的“工程维护”提供诊断、技能、接入、同步、脚手架、恢复和升级。先查看执行内容，核对后执行，作业在“执行记录”中查看或停止。根、Bundle、参数或文件变化时重新预览。
- “项目 → 产品 Home”导入现有 `REPOSITORIES.md` 或等价 README，核对字段后绑定本机工程。识别有歧义时提供字段映射或人工核对条目，原地图继续维护关系。
- Spec 详情“运行 Pi Agent”选择参与工程和要带入的未提交文件，在隔离 worktree 运行。启动或续接时可授权本次作业使用离线命令。到执行详情答复问题、停止或续接，结束后按冻结差异逐文件接收，再刷新原 Spec。
- Spec 信息、问题答复和阶段进度可恢复已保存的本机草稿，并发修改按字段核对合并。内容区提供确认版本差异，旧记录缺少正文基线时保留其原确认历史。
- “设置 → 备份与恢复”在线生成一致备份，恢复到新的或空的个人数据目录后，按返回命令打开该目录核对。纯 CLI 或旧版的离线完整目录备份步骤见工作台说明。

当前源码的 Pi 运行入口支持 macOS ARM64 和本机 Pi 1.5.2 或 1.6.0。启动和续接表单显示模型及本机凭证检查结果，并提供按需连接测试。Codex 历史记录可继续审阅和接收成果。远端关联读取采用 gh/glab 的已有认证。完整工作台说明提供界面入口、成果接收、进程与磁盘版本及恢复步骤。[工作台说明](workbench.md)

### 主 Spec、问题回复与确认（3.4.1 起）

```text
yss workbench register --root /absolute/project --source docs/spec/2026-11-14-user-batch-import/2026-11-14-user-batch-import-spec.md --json
yss workbench request --spec-id <SpecID> --json
yss workbench respond --spec-id <SpecID> --question-id OQ-01 --question-digest <SHA256> --kind answer --answer "用户已明确给出的答复" --json
yss workbench respond --spec-id <SpecID> --question-id OQ-02 --question-digest <SHA256> --kind defer --reason "等待外部方案" --impact "本期不提供导入" --excluded-scope "批量导入" --review-at 2026-12-01 --review-utc-offset-minutes 480 --json
```

上述示例使用默认个人数据目录；若工作台以 `--data-dir` 启动，所有查询和写入命令都须沿用相同目录，例如 `yss workbench --data-dir /absolute/personal-data register ...`。Agent 应直接沿用工作台生成的更新要求中的目录，不另选默认库。

`register` 按项目与主文件路径复用登记，保留启用状态并更新来源资源。新需求无约定时用固定创建日期与具体英文主题组成 key，主文件为 `docs/spec/<key>/<key>-spec.md`；旧 `docs/spec/topic/spec.md` 等合法路径继续支持。本次源码只归并无自身元数据的同目录同 key `-interaction.md` / `-design.md`，且要求主文档 resources 明确本地引用，kind 分别为 interaction / supplement 或 other。旧 `interaction.md → spec.md` 保留无元数据推断；旧主文件有元数据时须明确引用该本地路径，兼容原 kind。自身有有效元数据的文件、其他参考资料不因被引用而归并。个人资源和正文链接不代表新附件归属；旧无元数据文件仍可据链接推断阅读资源。

发现扫描遇识别错误会告警并跳过该候选；主文件 register/refresh 停用符合归属条件的旧附件记录，刷新附件本身不合并，个人备注、答复、确认和历史不删除。改名先核实新路径未被启用/停用记录占用，更新文件与资源链接，再编辑原 Spec 的主路径，最后 register 新路径并核对原 ID；不新增登记后再合并。编辑改路径指向已归属附件会拒绝。路径变化会使旧确认不再匹配，历史仍保留；详情见[工作台说明](workbench.md)。这些识别规则是本次源码行为，不能仅凭帮助中有同名命令就推断旧安装版本支持。

`respond` 只能记录用户真实答复或已同意的延期，不能代作业务决定；要求当前问题摘要，返回 `result.responseId`。延期须有原因、影响、排除范围，以及 `--review-at YYYY-MM-DD` 或 `--review-when 条件`；日期按 `--review-utc-offset-minutes`（默认 UTC）解释，关键问题须完整排除其受影响范围。`request` 的 `result.text` 保留当前问题数组。按 `question.id`、`digest` 和用途定位有效的 `response.id`，按本次授权回写尚未采纳的有效答复或延期。已采纳的决定供引用。若本次授权修订规则且业务前提确有变化，保留受影响的原问题 ID 并更新题意或影响，复读适用性后继续。变更、被替换或到期的记录单列历史原文和复查原因。工作台中选择「沿用原答复」后，仍需点击「保存答复」才产生当前问题的新答复 ID。

运行中的工作台通过个人目录中私有 `session.json` 调用既有本地鉴权接口写入，保持单进程锁与 revision 校验；服务未运行才直接打开 Store。不要分享该会话文件。内容确认仅由工作台用户明确操作，绑定范围和产物摘要，不能从个人 Status=done 推断。

这些能力从 3.4.1 起提供，未升级的旧版不会自动拥有；先以实际 `yss workbench --help` 核对。
