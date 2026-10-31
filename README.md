# yss

<!-- YSS_CURRENT_STATUS_START -->
当前源码版本：`3.0.0`。固定模板：backend `v5.0.0`、spec `v5.0.0`。现役 Profile：`backend`、`spec`。协议版本：`1`。
<!-- YSS_CURRENT_STATUS_END -->

English: [quickstart](docs/quickstart.en.md).

统一 Spec、Backend 的 Go CLI。当前身份见上方生成信息。命令入口迁至 Cobra，提供 Bash、Zsh、Fish、PowerShell 补全；参数、中文帮助与生成参考来自同一命令树，保留 JSON envelope、MCP 与保存计划保护。新建和接管默认采用治理安装（`governed`），治理版本为 2；只需独立技能时显式选择 `--installation skills`。Spec 包含必要产品设计，Backend 承担技术设计与专职交付；独立 Design 身份及兼容入口已移除，旧 Design 实例升级前应保留原版本与原始资产。新增治理合同、任务候选及执行事实校验、适用复核和 pending review 事务发布；帮助见 `yss --help`、`yss migrate --help`、`yss contract publish-review --help`。Frontend 与 `--stack` 继续退役，旧文件保持保留语义。接口见 [技能按需查询](docs/skills-discovery.md)，发行资格见 [兼容边界](docs/compatibility.md)。

本版本发行目标为 macOS ARM64 与 Windows x64，各平台资格以最终固定二进制的实际收据为准。macOS 项目及程序事务要求归档与目标位于同一支持原子文件克隆的 APFS 卷；不支持克隆或跨卷返回 `UNPORTED`。已有用户修改及旧零字节事务临时文件保留并阻断恢复。Windows 权限与恢复边界见 [迁移清单](docs/porting-status.md)。

工程固定 Go 1.27.1；机器默认版本较低时使用 `GOTOOLCHAIN=go1.27.1`，下载工具链属于构建准备，编译后的 CLI 无此依赖。

帮助和离线教程：

```sh
yss -h
yss init --help
yss update apply -h
yss help lifecycle route
yss help tutorial
yss completion --help
yss completion zsh --help
```

帮助不要求项目身份，不读取项目或访问网络。每个命令和子命令提供用途、参数、必要条件和示例；未知帮助路径返回 `ARGUMENT`（退出 2）。即使传入 `--json`，帮助仍输出文本。

Shell 补全支持 `bash`、`zsh`、`fish`、`powershell`，例如 `yss completion zsh > /tmp/_yss`。生成命令仅输出脚本；各 Shell 的加载步骤见 `yss completion <Shell> --help`。Bash 需要先加载 bash-completion，Zsh 需要先初始化 compinit；CLI 不自动安装或改写 Shell 配置。完整离线参考由同一帮助源生成，见 [CLI 帮助与案例](docs/cli-help.md)。

长参数支持 `--参数 值` 和 `--参数=值`，布尔只接受小写 `true/false`。使用 `--` 传入以短横线开头的位置参数，如 `yss check --root ./demo-spec -- -example.yaml`；它仍须符合原命令的数量与路径限制。

直接生成后端工程使用 `yss scaffold ddd` 或 `yss scaffold mvc`，无需先初始化 Harness；完整参数见 `yss scaffold ddd --help`。由程序内置的 Go 实现直接生成（见 [docs/scaffold-go.md](docs/scaffold-go.md)），不依赖 Node.js 或 Python；目录保护需要 PATH 中的 Git。模板和 Maven Wrapper 来自二进制内置的固定 Spec Bundle，不需要本地模板仓库或联网下载技能。

```sh
yss scaffold mvc --project-name order-service --base-package com.yss.orders \
  --output-dir ./backend --platform-profile spring-boot-3.5-jdk17 \
  --spring-boot-version 3.5.16 --java-version 17 \
  --group-id com.yss --project-version 1.0.0-SNAPSHOT \
  --parent-group-id com.yss.cloud --parent-artifact-id yss-cloud-microservice \
  --parent-version 3.2.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT --json
```

将 `mvc` 改为 `ddd` 可生成 DDD 架构；版本示例须替换为自己的精确平台与可获取的 Maven 坐标。输出目标是 `./backend/order-service`，必须不存在；已有目录、符号链接和 `--force` 均拒绝。独立命令生成业务空骨架，禁用技能的独立参考示例，不初始化 Git、治理实例或批准记录。显式传入合同或批准参数时保留技能的正式校验，缺失或无效合同不会回退到独立模式。

JSON 返回 `projectRoot`、`manifestPath`、生成状态和 Bundle 来源；仅生成成功时 `verification.status=not-executed`。准备 JDK 和外部 Maven settings 后，从生成工程根运行 `./mvnw validate`、`./mvnw test`、`./mvnw package`，构建结果与生成结果分别记录。CLI 不复制用户仓库凭据，也不自动执行构建。正式合同模式的合同、架构决定、用户决定和设计前置校验由内置治理核心完成，不再需要 Python3、`jsonschema`、`referencing`。

内嵌 Bundle 归档（`internal/bundle/assets/bundles.json.gz`）不进入版本库。`docs/template-release.json` 指定模板发行标签；构建前用 `tools/resolve-template.mjs` 从公开标签生成一次仓外来源记录，设置 `YSS_SOURCE_SNAPSHOT` 后运行 `node tools/fetch-bundle.mjs`。归档摘要须与已提交的 `internal/bundle/assets/bundles.json.gz.sha256` 一致；同一次构建复用同一来源记录。缺少归档直接 `go build` 会报 `pattern assets/bundles.json.gz: no matching files found`。

默认缓存只有通过来源校验后才会复用；准备失败的目录保留供排查，错误信息给出其路径，重试会使用新的隔离目录。

编译：`CGO_ENABLED=0 go build -trimpath -o bin/yss ./cmd/yss`。测试：`go test -tags yss_nobundle -timeout 5m ./...`（纯逻辑），生成内嵌 Bundle 后 `go test -timeout 5m ./...`（含真实实例集成用例）。编译或测试成功都不代表发行验收通过。详见 [测试与 CI 状态](docs/ci-verification.md)。

安装诊断分别呈现文件对齐、已安装技能完整性和工作流验收。`doctor.readiness.skills` 只核验当前 Spec metadata5 已声明的技能及运行时，`workflow` 保持 `not-evaluated`；其他 Profile / 历史代际不新增技能核验义务。`skills resolve` 按 `result.status` 判断 ready / missing / blocked，退出 0 只表示查询完成；Claude 与 WorkBuddy 的投影检查不证明宿主已发现技能。

以下示例选择完整治理安装，后续才能使用 Context 与生命周期命令。只需独立技能时显式选择 `--installation skills`，无需先建立 Ticket、checkpoint 或治理批准。

```sh
yss init --profile spec --root ./my-project --project-name 项目名称
yss doctor --root ./my-project --json
yss sync --root ./my-project --plan --out /tmp/yss-sync-plan.json
yss sync --root ./my-project --apply --plan-file /tmp/yss-sync-plan.json
yss context verify --root ./my-project --json
yss lifecycle query --root ./my-project --id work-unit.entry-triage --json
yss git base --root ./my-project --json
```

程序版本、协议和 Profile 模板版本分别存储。以 2.3.1 为基线：不再支持旧版本实例的迁移，只含旧家族 metadata 而没有统一 CLI metadata 的目录会被 `doctor` 等命令拒绝（`LEGACY_INSTANCE_UNSUPPORTED`）。计划绑定项目根、快照、生成结果与输入摘要；修改后重新计算摘要的任意计划仍会被拒绝。

现役 Spec/Backend 的 Bundle 由 Go 构建工具读取同一固定模板提交及其有序覆盖层，嵌入程序供原生能力离线运行。Node/Python 治理脚本按各自职责继续维护，必要消费者逐项迁移和验证。重发前 CLI 和模板来源不属于兼容范围；识别到旧实例时停止受影响写入并保留文件。阶段批准仍由当前生命周期合同决定。

现役 Profile 的重复内容在单个私有归档中按原始字节 SHA-256 共享；公开 Bundle、文件权限、ownership、初始变体和来源身份保持完整。构建门禁要求归档 ≤10,000,000 字节、六平台各二进制 ≤35,000,000 字节，格式与测量方法见 [模板存储与体积验证](docs/bundle-storage.md)。

启用新版 `profile_guidance` 政策的实例在 `lifecycle status` 返回下游建议、工程状态、输入状态和命令。Spec 在本工程完成产品设计，按明确交付责任接续独立 Backend；正式交接继续核验原来源、批准、当前 Receipt 与 Context 对账。没有新政策的实例保持原状态查询，并提示同步。独立工程只按显式目录登记到 `.yss-profile-links.json`，`sync` 保留该文件，不根据邻近目录猜测关联。

```sh
yss profile prepare --root ./spec --backend-root ./backend --plan --out /tmp/delivery-prepare.json
yss profile prepare --root ./spec --apply --plan-file /tmp/delivery-prepare.json
yss handoff export --root ./spec --kind spec-baseline --checkpoint .work/feature/checkpoint.yaml --out /tmp/spec-baseline
```

初始化和 Spec 导入分别保存计划、分别执行事务。接入后由 Backend 主控建立自己的 checkpoint，绑定接收记录、完成目标 Context 对账，再核验进入条件；源 checkpoint 只作为只读证据。联合执行先预检全部目标，再登记关联并依次初始化，失败保留成功工程，按原保存计划重试；源关联回退不回退下游工程。登记和导入都是最新事务，迁移回退不能跨过它们，也不能在撤销关联后跳过该屏障。

```sh
# 模板仓须检出 docs/template-release.json 指定的正式标签
node tools/resolve-template.mjs --source-root /absolute/template-source --out /absolute/new-source-snapshot.json
export YSS_SOURCE_SNAPSHOT=/absolute/new-source-snapshot.json
node tools/fetch-bundle.mjs --source-root /absolute/template-source
yss bundle inspect --profile spec --json
yss bundle export --profile spec --out /absolute/new-bundle-directory --json
```

模板升级时更新 `docs/template-release.json` 的发行标签，并从该标签重新生成归档和派生帮助。来源记录在构建时生成，不作为第二份需要维护的版本配置；发行包仍携带兼容记录供已有升级器读取。预发布候选可用 `--candidate-commit <完整SHA>` 生成独立记录，不能作为正式标签已验证的证据。

构建来源记录为两个 Profile 保存本次标签解析结果；新建使用 Bundle5、metadata5 与项目保存计划 v4。历史来源和 metadata 按各自兼容边界读取，旧项目保存计划须重新生成；运行协议与 JSON envelope 保持 v1。两个插件消费固定二进制及公开 Bundle；binding、身份与受管文件进入同一保存计划和事务。详见 [兼容边界](docs/compatibility.md) 和 [退役合同](docs/cli-retirement.md)。

离线程序安装与项目迁移分开，先生成计划，再显式应用：

```sh
yss update plan --tool-root ./tools/yss --artifact /path/yss.tar.gz --sha256 <SHA-256> --out /path/install-plan.json --json
yss update apply --tool-root ./tools/yss --plan-file /path/install-plan.json --json
yss update status --tool-root ./tools/yss --json
```

程序升级失败时保留现场，在新的工具目录重新安装；程序升级不提供恢复或回退命令。

运行记录使用独立 SQLite 目录。`runtime run/events/commands/pins --id <运行 ID>` 为只读查询；`runtime pin/unpin --id <运行 ID> --token <begin 返回的 token> --reason <原因>` 只变更该运行的保护记录，完成后仍可操作。记录查询和保护标记不表示生命周期批准，也不删除证据文件。

当前 checkpoint、批准、真实用户决定、实现合同、验证证据、战略交接及默认完整 project-ci 使用原生 Go 校验，接口见 [治理校验合同](docs/native-governance.md)。这些检查只消费既有资产，不执行业务验证命令或创建批准。

完整覆盖表、平台限制及剩余切换条件见 [迁移清单](docs/porting-status.md)。`compat`、`compat-api` 与 `create-yss-*` 入口已移除（见 [compat/README.md](compat/README.md)）。

通用交叉打包入口（会构建全部平台）：`go run ./tools/package <工程外新目录>`。包中分别记录“交叉编译”和“原生运行验证”；未提交源码和缺少平台运行证据的包只能用于预发布试用。

**历史发行记录（2.1.0）：** 当时交付 `darwin/arm64` 与 `windows/amd64` 两种产物。本次仅 Mac ARM64 执行原生、安装与恢复验收，合格包由现有装配器生成；Windows x64 仅构建、打包并核验来源与摘要，保持 `stableReady=false`、`nativeRuntimeVerified=false`，不宣称运行验收通过。Windows 可核对独立 `build-only-checksums.json` 后解压运行；本次 `upgrade --to 2.1.0` 的稳定平台范围仅含 Mac，Windows 在线升级仍明确拒绝。Windows 只读文件操作的 `UNPORTED` 限制保持。

上述 2.1.0 历史发行资格为 `local-platform-impacted-consumers`，包括本机原生接口、插件、恢复与安装契约；不包含无关全模板回归。已有失败及未执行项如实保留。

## 入门教程与 CLI 在线升级

在新目录选择一个 Profile 和安装方式。新建和接管默认使用 `governed`；独立技能安装显式选择 `--installation skills`：

从 CLI `2.8.3` 起，`skills` 安装同时包含已选技能引用所需的 `.template-spec`、`.template-source` 文件及其依赖；这些参考文件不启用治理身份或正式治理能力。已有项目在使用对应新版 CLI 后，通过审阅并应用 `yss sync` 计划获取新的受管引用资产；`2.8.2` 及更早版本不包含此新清单。

```sh
yss init --profile spec --root ./demo-spec --project-name 演示项目
yss init --profile backend --root ./demo-backend --project-name 演示后端
yss doctor --root ./demo-spec --json
yss diff --root ./demo-spec --json
```

项目模板同步使用 `sync` 的保存计划并保持已有安装方式；转换安装方式须显式使用 `migrate --installation skills|governed`，适用版本及活动任务限制见[兼容边界](docs/compatibility.md#独立技能安装新安装合同)。升级 CLI 程序不会自动同步或转换项目。资源补装先列出当前 Profile 实际支持的标识：

```sh
yss sync --root ./demo-spec --plan --out /tmp/yss-sync-plan.json
yss sync --root ./demo-spec --apply --plan-file /tmp/yss-sync-plan.json
yss skills list --root ./demo-spec --json
yss skills ensure yss-research --root ./demo-spec --plan --out /tmp/yss-skill-plan.json
yss skills --root ./demo-spec --apply --plan-file /tmp/yss-skill-plan.json
yss assets list --root ./demo-spec --json
```

安装和项目就绪由 `setup-yss-harness` 技能编排。已发布二进制的 Bundle 标识以 `skills list` 为准；源码更名进入内嵌分发需要后续固定来源发行。

同步要求 Git 已有提交且工作区干净（含未跟踪文件和子模块），拒绝覆盖被忽略的目标；计划绑定 HEAD。同步失败时核对回执中的 `possibleNewPaths` 和 `possibleNewControlPaths`，人工处理实际新增的文件和目录，再用 Git 恢复已跟踪文件；同步不提供恢复或回滚。计划输出文件必须不存在。输入变化或定制冲突时，先处理诊断并重新生成计划。

升级 CLI 程序：

```sh
yss upgrade --check
yss upgrade
yss upgrade --to 1.3.0 --json
yss upgrade --tool-root ./tools/yss --json
```

默认查询 `iloveZzz/yss-cli` 的最新正式 Release，校验当前平台的发行资格、归档大小、SHA-256 与包内来源，再事务安装。`--to` 接受 `1.3.0` 或 `v1.3.0`，仅支持稳定版本并拒绝降级；`--version` 继续查询程序自身版本。`--check` 只查询，不下载或写入。

默认通过实际可执行文件解析安装根。例如 `~/.local/bin/yss` 链接至 `~/.local/share/yss/yss` 时，更新受管目录并保留链接。裸复制二进制缺少安装收据时会拒绝自动覆盖；显式指定空工具目录可首次安装。已是目标稳定包时不下载、不创建事务。未完成事务、用户修改、输入漂移或发行校验失败均停止安装。

`--json` 沿用 envelope v1 / protocol v1，`command` 为 `upgrade`，`result` 提供 `status`、`currentVersion`、`targetVersion`、`updateAvailable`、`platform`、`toolRoot`、`releaseUrl`、`archiveSha256` 及实际安装时的 `transaction`。检查状态为 `checked`；安装状态为 `installed` / `upgraded` / `unchanged`。参数错误退出 2；升级拒绝或失败退出 1，错误码包含 `VERSION`、`INSTALLATION`、`STATE`、`CONFLICT`、`ARTIFACT`、`DIGEST`、`NETWORK`、`CANCELLED`。应用期间沿用事务的 `INPUT_DRIFT` 和并发保护。

程序升级失败时保留现有目录，使用新的工具目录重新安装，并在核验后调整 PATH。`yss update status --tool-root <目录> --json` 可只读检查安装状态。

## 命令帮助与安装一致性诊断

`yss` 或 `yss --help` 按快速上手、生命周期、常用命令及维护入口展示中文导航；`yss <命令/子命令> --help` 和 `yss help <命令/子命令>` 展示用途、前置条件、最小示例、参数、预期结果、下一步及错误。`-h` 显示帮助，`-V` 等同于 `--version`。帮助始终输出文本，不读取项目、不访问网络、不写入资产。

交互终端默认中文摘要；管道和重定向保留原始结果。`--human` 强制中文，与 `--json` 互斥；`--json --diagnostics` 仅在失败时附加顶层诊断，保留原错误码、退出码、协议版本和 `result`。原因、处理和复验区分已确认、可能及待核验事项，并标记命令的只读、写入或联网行为。没有版本依据时显示“尚未登记”。

```sh
yss init --help
yss help update apply
yss upgrade --help
yss help tutorial
yss -V --json
```

完整离线教程和命令索引见 [生成帮助指南](docs/cli-help.md)。按主题使用 `yss help tutorial daily` 或 `yss help tutorial backend`；按问题使用 `yss help examples sync` 或 `yss help errors INPUT_DRIFT`。阶段名称及退出条件消费固定 Bundle 注册表，执行范围和顺序消费固定 Profile 路由；Design 的下游兼容登记不授予本 Profile 实现资格。

治理资产的统一入口是 `yss check` 与 `yss verify --gate`：`yss check --changed` 按字段识别改动的批准记录、checkpoint、上下文对账、OpenAPI 草稿验证与脚手架决策，逐个调用对应的现有 `verify`，汇总成一份报告；每条发现都带 `code`、`file`、`pointer`、`message`、`fix` 与可复现的 `command`。`yss verify --gate <门禁> --checkpoint <文件>` 依次运行 `lifecycle verify` 与 `contract verify --kind gate-checks`。两者只聚合、不新增校验语义：模板目录与未识别文件列为 skipped，缺当前上下文（如用户决定）列为 not-evaluated，整体为 incomplete 而非通过，均不授予批准或执行。

`yss mcp --root <项目>` 以 MCP stdio 服务提供只读工具（lifecycle_route、verify_daily、lifecycle_status、lifecycle_query、context_query、contract_view、explain、check、trace、verify_gate），结果与命令行同一 JSON envelope；工具参数不能改写 `--root`，不暴露写入、提交或推送。服务拥有标准输入，取消时关闭输入并结束读取，空闲 stdio 也可正常终止。能力清单以 `mcp-stdio-read-only-v1` 声明。

`yss tracker status|link|sync` 把本地 Ticket 五态镜像到已链接 GitHub Issue 的同名标签：本地 Markdown 始终权威；`link` 写入 `Tracker-Link: github:owner/repo#N`；`sync` 先 `--plan --out` 读取远端保存计划（项目外），再 `--apply --plan-file --confirm-remote github` 复核本地字节与远端标签/更新时间未变后写入，成功后记录 `Tracker-Synced`。计划 v2 绑定规范化 API 来源和 Ticket 选择范围，应用时重建合法改动；旧 v1 计划返回 `PLAN`，须重新规划。只按标签接口逐项添加或移除五态，保留其他标签，不创建、关闭或评论 Issue；需 `GH_TOKEN`，`GITHUB_API_URL` 可指向 GitHub Enterprise。失败 envelope 保留部分完成项、`remote_changed`、`remote_state=unchanged|changed|unknown` 与本地记录状态；本地失败另保留 `local_error_code` 和可用的 `transaction` 回执，保持原始错误码与取消原因。结果未知时先对账再重新规划，不自动重放。git 策略 `ticket_pattern: github` 接受 `#123` / `owner/repo#123`，分支名使用 `gh-123`。

`yss trace --feature <功能包>` 只读汇总业务票（FR/AC）、垂直切片 Ticket 与 Slice 合同的验证映射，给出每个验收的切片、验证与提交；加 `--implementation-root --since [--until]` 时按 Ticket trailer 把实现仓提交关联到 `slice_id`。缺口包括无切片/无验证的验收、无 Ticket 或未对应切片的提交、Slice 依据摘要在冻结后变化；`--html` 另写静态矩阵页（项目外新文件），`--strict` 有缺口时以 `TRACE_GAPS` 失败。MCP 同名工具 `trace` 只读、不写 HTML。

支持 `lifecycle-target-v1` 的 Spec 实例默认推进到业务验收，也可先到 Spec 或产品设计后续推；目标设置、职责边界和状态字段见 [功能推进目标](docs/lifecycle-target.md)。旧实例需显式同步，目标达到不授予合并或发布权限。

更新 Bundle 后运行 `go run ./tools/helpview`，再运行 `go run ./tools/helpdocs --reference` 保留完整命令与错误参考；两者追加 `--check` 验证来源与生成内容一致。模板统一指南的帮助块使用同一生成器的 `--embed --out <指南文件>` 同步。文档案例只说明协议，不构成真实批准。

原生命令的未知命令、子命令、选项、无效取值或缺少参数值返回 `ARGUMENT`，退出码为 `2`，同时给出帮助入口和适用的拼写建议。比如 `yss upadate` 会建议 `update`，并说明在线程序升级使用 `upgrade`。建议不会自动执行。已有升级错误码和 JSON envelope 版本保持不变；其他治理和兼容消费者保留自身领域校验。

- `yss upgrade`：下载并事务安装稳定版 CLI；`--check` 只查询在线版本。
- `yss update plan/apply/status`：离线程序安装与诊断。
- `yss sync`：将项目模板更新到程序内置固定 Bundle。

`yss update status --tool-root <实际安装目录> --json` 保留原有安装与事务信息，并新增 `installationConsistent` 和 `diagnostic`。诊断包含工具目录、安装记录版本、manifest 版本、逐文件 `expected/actual` 类型/摘要/权限、原因与下一步建议。`runningProgramMatchesRoot` 为 true 时才记录和比较 `runningVersion`；查询其他安装目录不会把当前程序版本当成该目录版本。

查询成功表示获得了状态事实；`installed: true` 表示存在可识别安装记录，不能单独证明文件一致。`installationConsistent: true` 也不授予正式发行资格或项目批准。诊断不会改写安装清单、覆盖文件或自动运行恢复命令。

遇到“受管程序文件与安装清单不一致”时，先查询错误中给出的实际目录。保留原目录与归档，选择不存在的新目录安装整个程序包：

```sh
# ./tools/yss-clean 必须不存在；下面是新安装目录示例。
yss upgrade --tool-root ./tools/yss-clean
./tools/yss-clean/yss version --json
./tools/yss-clean/yss update status --tool-root ./tools/yss-clean --json
```

核验一致后再调整 PATH 入口。离线案例见 `yss help tutorial`。只替换二进制、改写旧收据或强制覆盖会破坏来源依据；现有摘要、来源、冲突及事务保护继续生效。

Backend 支持 `standalone` 本地业务事实与 `upstream` 权威来源两种输入。日常 Ticket 的 `business_input` 记录 `mode`、`side`、`ref`、当前 `digest`、显式 `conflicts: []`。上游审批仍消费真实当前批准，冲突回交权威方；本端分析不扩大另一端代码写范围，本端完成不等于跨端业务验收。高风险与已有正式任务仍走 governed。

独立后端骨架使用生成器 `--standalone` 并明确精确平台、Maven 坐标及输出目录；无需 Harness 合同，不生成业务代码、批准或 ready-for-agent。Maven 优先根 `./mvnw` 与现有 settings 配置；缺配置询问路径/仓库信息并保留待验证骨架。
