# IDEA 集成协议 v1

IDEA 插件通过本协议取得固定目录、真实文件计划和落盘核查结果；用户确认原计划后，CLI 才执行对应写入。本文记录基于正式 CLI `v2.7.6`、源码 `28a8493711b7e2154cb6b3f6ff47a04ea80db16c` 的适配，供 CLI 与插件实现者核对同一契约。

本接口从 `2.8.0` 提供。正式归档必须绑定最终提交和实际校验值；工作树候选只用于开发联调，接口存在不代表发行或平台验收通过。

## 固定来源与协议版本

本轮保留现有模板 `v4.7.6`、提交 `6e782dc8724ff3b7c94fa964540f44bc3b266938`。`docs/source-lock.json` 保持 schema5；Spec、Backend 继续使用 Bundle5 与 metadata5，安装方式仍为 `skills` 或 `governed`。本次只补充 CLI 与插件之间的接口，不重建安装身份协议，也不改写旧实例或保存计划的版本。

| 接口 | 版本与约束 |
| --- | --- |
| JSON envelope | `outputVersion=1`、`protocolVersion=1`；保留 `version`、`command`、`profile`、`status`、`code`、`result` 和可选 `diagnostic`。`command` 为命令组，例如 `scaffold`、`project`、`bundle`。 |
| IDEA 能力声明 | `capabilities.result.ideaIntegration.schemaVersion=1`；声明 `profiles`、`installations` 及逐操作 `plan`、`boundApply`、`safeCancel`。 |
| 绑定 CLI 的 AI 保存计划 | 显式 `--bind-cli --plan --out` 生成 `kind=installation-plan`、`schemaVersion=5`。 |
| 普通 AI 保存计划 | 不传 `--bind-cli` 时保留当前 Bundle5 的 `schemaVersion=4` 及既有使用方式。 |
| Java 保存计划 | `kind=scaffold-plan`、`schemaVersion=1`、`protocolVersion=1`。 |
| 固定目录 | `kind=scaffold-catalog`、`schemaVersion=1`。 |
| 项目身份检查 | `kind=project-inspection`、`schemaVersion=1`。 |

`ideaIntegration` 仅向插件声明 `spec`、`backend` 与 `skills`、`governed`。`init`、`attach`、`scaffold` 的写入均声明 `safeCancel=false`；插件停止后续步骤，但不能据此强杀当前写进程或宣称自动回滚。只读、下载与规划能否中断，由实际调用及其结果判断。

## AI 安装：显式绑定 CLI 的计划

插件规划新建安装或已有项目首次接入时，显式传入安装方式和 `--bind-cli`：

```sh
yss init --profile spec --installation skills --root <绝对目标根> \
  --project-name <项目名称> --bind-cli --plan --out <项目外新计划文件> --json

yss attach --profile backend --installation governed --root <绝对项目根> \
  --project-name <项目名称> --bind-cli --plan --out <项目外新计划文件> --json
```

`--bind-cli` 仅用于 `init`、`attach`，必须与 `--plan --out` 一起使用，不得与 `--apply` 混用。规划保留当前路径、身份、安装方式、文件冲突及技能校验；选择 `skills` 不产生治理批准，选择 `governed` 不跳过现行治理规则。

保存计划和公开计划同时包含 `kind`、`schemaVersion`、`protocolVersion`、`command`、`root`、`profile`、`installation`、`cli`、`bundleHash`、`templateCommit`、`snapshotHash`、`digest` 及文件变化、冲突和阻断。`cli` 记录 `version`、`commit`、`sourceState`、`executableSha256`；`bundleHash` 绑定完整固定 Bundle，不使用所选安装视图重新计算的摘要替代它。保存计划继续携带既有输入和重建信息。

用户确认公开计划后，插件保留原保存计划及原 `digest`，调用：

```sh
yss init --profile spec --root <相同目标根> --apply \
  --plan-file <原计划文件> --expected-digest <原确认摘要> --json
```

接入计划以相同方式调用 `attach`。如另传 `installation`，它必须与保存计划一致。CLI 先比较确认摘要，再按当前可执行文件 SHA、CLI 来源、完整 Bundle 摘要、模板提交、快照和现场输入重建计划；不一致时拒绝，不重新解释用户的确认范围。

AI schema5 计划缺少 `--expected-digest` 返回 `PLAN_REQUIRED`，确认摘要不匹配返回 `INPUT_DRIFT`。旧版计划保留既有版本处理；普通 schema4 计划不因本接口新增而强制要求该参数，显式提供时仍必须匹配。未知或不受支持的计划不能自动转换版本或改写摘要。

## Java 工程：内存预演与同计划应用

```sh
yss scaffold catalog --json
yss scaffold ddd <工程及精确平台参数> --plan --out <项目外新计划文件> --json
yss scaffold mvc <工程及精确平台参数> --plan --out <项目外新计划文件> --json

yss scaffold ddd --apply --plan-file <原计划文件> \
  --expected-digest <原确认摘要> --json
```

`mvc` 的应用调用使用相同架构名称。应用保存计划不得夹带新的工程参数，命令架构必须与计划一致；`--expected-digest` 必须非空且等于原计划摘要。原有直接生成入口与显式正式合同校验继续保留，插件不据此新增正式合同交互。

预演只在内存中渲染，不创建真实目标或尚不存在的输出父目录，不通过生成后删除模拟计划。保存计划包含：

- `cli`、`bundle`：实际可执行文件 SHA、CLI 来源、固定 Spec Bundle 与其模板、快照、清单摘要。
- `options`、`projectRoot`：规范化输入与绝对目标。Java 目标为 `output-dir/project-name`。
- `renderContext`：一次捕获的 `author`、`date`、`generatedAt`；应用复用原值，不受之后的用户环境或日期变化影响。
- `files`、`dirs`：完整文件与目录描述，包含生成 manifest 本身、内容 SHA 和权限规则。
- `digest`、`blockers`、`readyToApply`：确认对象、阻断及当前可应用状态。

应用重新核对同一 CLI、Bundle、渲染上下文和完整计划摘要后才创建 staging 输出，并以不覆盖已有目标的方式发布。已有目标、符号链接、路径越界、Git 子模块或目录归属等保护继续生效；`--force` 不受支持。生成不隐式初始化 Git、安装 AI 资产或运行 Maven。

权限按计划区分 `fixed` 与 `platform-default`。Wrapper 等显式模式纳入 `fixed` 核查；普通文件和目录遵循平台及用户 `umask`，不为匹配计划而放宽权限。Windows 只核对其实际可表达的权限语义，不宣称已验证 POSIX 执行位。

## 固定目录与字段

`scaffold catalog` 返回 `bundles`、`profiles`、`installations`、`architectures`、`platforms`、`fields`。目录取自当前执行 CLI 的固定 Bundle；插件不另外维护生成版本表，也不自由拼接目录组合。

平台项包含 `platformProfile`、`springBootVersion`、`javaVersion`、Parent 坐标、`yssComponentsVersion`、`architectures`、`standaloneAllowed`、`verification`、`blockers`、`verificationBlockers`。`standaloneAllowed` 表示当前生成器是否允许完整组合；`verification=unverified` 及其原因单独保留，允许生成不等于 Maven 构建或平台验收通过。字段项给出实际 `flag`、必填性、选项、适用操作和说明；CLI 执行最终参数校验。

## 身份检查与实际结果核查

```sh
yss project inspect --root <绝对项目根> --json
yss project verify --plan-file <原AI计划文件> --json
yss scaffold verify --plan-file <原Java计划文件> --json
```

`project inspect` 返回 `root`、`identityState`、可信身份中的 `profile`、`installation`、`repositoryMode`、`transactionState` 和 `notices`。

| 字段 | 值与含义 |
| --- | --- |
| `identityState` | `absent`、`native`、`legacy`、`invalid`、`unsupported`；只有身份核验成功后才报告可信 Profile 与安装方式。 |
| `transactionState` | `clear`、`pending`、`blocked`、`unknown`；检查不隐式恢复或清理事务。 |
| 核查 `outcome` | `not-applied`、`complete`、`incomplete`、`unknown`；同时返回 `missing`、`mismatched`、`notices`。 |

身份分类不授予接入、迁移或恢复写入资格；`absent` 也不代替 `init`、`attach` 的最终保护检查。`verify` 观察原计划拥有的实际文件、身份及适用事务状态，不重新应用计划，不删除用户后加文件。AI 核查还保留安装方式、技能就绪和适用治理 Context 校验；Java 后加的 `.idea` 或业务文件作为通知，不要求删除。

消费者必须同时检查进程退出码、JSON envelope 和业务结果。只读查询正常退出仍可能返回 `invalid` 身份或 `incomplete` 输出；`exit=0`、目录存在、日志含“成功”均不能单独标记完成。进程取消、异常或返回 `CANCELLED` 后，插件仍须使用原计划核查实际输出；不能把取消直接显示为零写入。

AI 应用结果中的 `planDigest` 保留内部事务计划摘要语义，不等于用户确认的安装计划 `digest`。插件一直持有确认时的保存计划、文件 SHA、目标与原确认摘要，应用和核查前后检查其完整性，不用事务摘要替换它。

## 发行与消费边界

插件正常入口默认查询本仓最新正式稳定发行，再固定本次使用的具体版本、平台归档 URL、归档 SHA、可执行文件路径与 SHA、协议及两个 Profile 的 Bundle 摘要。计划、应用和核查始终使用同一已校验文件；新的预演才重新查询最新发行。下载、缓存或 CLI 文件变化不能绕过完整性与能力检查；本地工作树候选只用于显式开发联调，不填入正式发行资源。

新增接口的主要消费者为 `internal/cli` 的命令和能力声明、`internal/project` 的保存计划与安装后核查、`internal/scaffold` 的目录与渲染/发布/核查，以及独立 IDEA 插件。模板 metadata reader、Bundle 打包/克隆/摘要链继续消费原 schema5 契约，本轮不改变这些版本。

本文不替代构建、原生安装、插件 GUI 或 Windows 验证记录。正式装配继续要求现行来源与证据合同；Mac 程序安装资格不能代替插件原生流程，Windows 交叉构建不能写成原生通过。最新版缺少真实归档或必要能力时继续阻断；不得使用 PATH 程序、假 URL 或假摘要补齐清单。
