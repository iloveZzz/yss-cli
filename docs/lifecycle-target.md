# 功能推进目标使用手册

Spec 是“综合研发主控”，Design 是“产品与业务设计”；后端和前端承担技术设计与专职交付。启用 `lifecycle-target-v1` 政策的新正式 Spec 功能默认推进到完整业务验收。用户要求“先完成 Spec”或“先完成设计”时，保存对应的本次目标，到点停止；后续在同一功能上继续。日常任务仍使用既有 `daily/governed` 分流。

帮助入口：`yss lifecycle target --help`、`yss lifecycle status --help`、`yss help examples lifecycle target`、`yss help tutorial spec`。帮助和教程仅输出离线文本；下面的操作需要当前实例与真实资产。

## 先确认能力和当前功能

运行 `yss version --json`、`yss capabilities --json` 查看 CLI 来源及能力，再核验目标实例的政策。程序版本号、内置模板来源和实例能力是不同事实；仅凭 `1.3.2` 等版本字符串不能判断目标功能已可用。

功能通过当前 checkpoint、Tracker 和唯一登记的 `map.md` 定位，不能按目录名或邻近工程推断。本文以已登记的 `feature.example`、`.work/example/checkpoint.json` 为例；新工程常用 `.work`，旧工程须按实际 `tracker.root` 和登记替换路径。目标固定写入该功能包的 `progression-target.json`。

缺少新能力的旧原生实例保留原行为：`status` 查询不会自动写目标或改 checkpoint；`target` 拒绝设置并提示显式同步。程序和模板分别处理：支持当前命令的 CLI 到位后，再审阅并应用实例同步计划。

```sh
yss sync --root ./spec --plan --out /tmp/spec-sync-plan.json --json
yss sync --root ./spec --apply --plan-file /tmp/spec-sync-plan.json --json
yss lifecycle target --root ./spec --checkpoint .work/example/checkpoint.json --json
```

`sync` 用于原生实例的模板同步；旧 metadata 的显式迁移使用 `migrate`，程序升级使用 `upgrade`。同步冲突须先处置，不能用无冲突计划替代业务资产修改授权。

## 选择本次停点

| JSON `target` | 当前证据达到条件 |
| --- | --- |
| `spec-approved` | 当前 Spec 基线批准有效，业务 Ticket 草案及需求／验收覆盖可核验。 |
| `product-design-completed` | 适用产品设计审查、验证、批准以及业务 Ticket 正式化闭合。产品设计不适用时须核验当前依据，不生成空原型。 |
| `backend-deliverable` | 当前后端实现、批准合同、适用接口、独立审查、构建和真实契约／部署验证闭合。 |
| `frontend-accepted` | 当前前端实现、适用还原验证和独立验收闭合；前端不适用另核批准影响评估及 Slice。 |
| `business-accepted` | Spec 主控完成同一业务范围内适用交付的统一验收。 |

区分三个结论：本次目标达到、Profile 职责完成、业务整体完成。短目标达到不会把 checkpoint 改成整个业务完成；业务验收也不自动提交、合并或发布。

完整 Spec 默认 `business-accepted`。已有 `plan-to-backend` 永久职责默认 `backend-deliverable`，只允许表中前三目标；修改目标不能扩大职责。专职工程只读显示 `profile-terminal`，这是本端职责终点，不是第六个可写枚举。目标写入只限支持当前政策的 Spec 主控。

## 保存目标，再按当前工作单元推进

例如用户要求“先完成 Spec”，在项目内创建 `.work/example/tmp/target-input.json`：

```json
{
  "schema_version": 1,
  "kind": "lifecycle-progression-target",
  "feature_id": "feature.example",
  "checkpoint_ref": ".work/example/checkpoint.json",
  "target": "spec-approved",
  "intent_source": "用户要求先完成 Spec",
  "consumers": []
}
```

```sh
yss lifecycle target --root ./spec --checkpoint .work/example/checkpoint.json --json
yss lifecycle target --root ./spec --checkpoint .work/example/checkpoint.json --input .work/example/tmp/target-input.json --plan --out /tmp/spec-target-plan.json --json
yss lifecycle target --root ./spec --apply --plan-file /tmp/spec-target-plan.json --json
yss lifecycle status --root ./spec --checkpoint .work/example/checkpoint.json --json
```

JSON v1 必须包含示例中的七个字段。`feature_id` 和 `checkpoint_ref` 必须匹配显式当前 checkpoint；`intent_source` 记录用户指令来源；`consumers` 必须是数组，本地推进用 `[]`。没有 `--target` 命令行选项，目标通过 `--input` JSON 设置。输入不能占用正在修改的 `progression-target.json`，也不能添加阶段、批准、完成状态或 `ready-for-agent` 字段。

草稿使用功能包已有的临时目录。在同一 Git 工程中，先从实际工程根运行 `git check-ignore .work/example/tmp/target-input.json` 确认已有规则覆盖，再编辑草稿；CLI 不会自动添加忽略规则。保存计划建议放工程外，新 `--out` 文件必须不存在。保留输入草稿直到 apply 完成，供输入漂移核验。

`--plan` 保存可审阅计划，`--apply` 重新编译并核验原计划。apply 只传 `--root`、`--apply`、`--plan-file` 和需要的输出参数，不同时传 `--checkpoint`、`--input`、`--plan` 或 `--out`。设置目标不执行实现；实际推进继续由 `yss-product-lifecycle` 和对应负责人消费当前准入。

## 从 Spec 续推到设计，再到业务验收

1. 当前证据支持 `spec-approved` 后，本次推进停止下游写入；保留 checkpoint 的真实 `next_work_unit`。
2. 用户要求继续设计时，将同一草稿的 `target` 改为 `product-design-completed`，更新 `intent_source`，重新生成并审阅新计划。
3. 设计完成后要求继续前后端及业务验收时，将目标改为 `business-accepted`，更新来源，再 plan/apply。

```sh
yss lifecycle target --root ./spec --checkpoint .work/example/checkpoint.json --input .work/example/tmp/target-input.json --plan --out /tmp/design-target-plan.json --json
yss lifecycle target --root ./spec --apply --plan-file /tmp/design-target-plan.json --json
yss lifecycle status --root ./spec --checkpoint .work/example/checkpoint.json --json
```

上例运行前须已把草稿改为设计目标。后续业务目标采用同一流程和新的计划路径，例如 `/tmp/business-target-plan.json`。CLI 没有自动实施的“继续”命令；每次改目标后重新核验已有证据，由生命周期 Skill 从首个合法且未完成的工作单元续推。

有效批准与稳定业务 Ticket ID 可复用。只修改目标配置不会修改 checkpoint、批准、冻结交接包或 Receipt 原字节；真实资产、Context 或批准依据变化仍按既有合同使受影响证据失效，须先补齐当前证据。

## 本地实现或显式独立消费者

`consumers: []` 使用单 Spec 主线：Spec 批准 → 适用产品设计 → 业务正式化 → 技术与接口合同 → 实现 → 独立验证 → 业务验收。本地批准资产直接消费，代码写入已登记实现仓，无需另外创建三个治理工程或自导自入。技术设计由适用前后端工程路线承担；后端目标核验当前真实交付证据，不因目标本身强制创建战略交接包。

选择独立专职工程时，将消费者显式写入同一输入：

```json
"consumers": [
  {"profile": "design", "root": "/absolute/design", "checkpoint_ref": ".work/example/checkpoint.json"},
  {"profile": "backend", "root": "/absolute/backend", "checkpoint_ref": ".work/example/checkpoint.json"},
  {"profile": "frontend", "root": "/absolute/frontend", "checkpoint_ref": ".work/example/checkpoint.json"}
]
```

这是上面完整 JSON 的字段替换示例。每种 Profile 最多绑定一个消费者；`root` 为目标工程的规范绝对路径，不能指向主控自身，checkpoint 路径相对于该目标工程。已有目标须核验真实 Profile、同一 `feature_id` 及唯一当前登记。尚未创建的工程可以登记未来意图，但身份、接收、Context、Receipt 和交付未核验前不能报完成。初始化和绑定分别消费既有计划及交接接口，配置消费者不等于创建或批准它。

- Spec → Design：复用已批准 SpecBaseline、导入 Receipt 和目标 Context 对账，继承有效 Plan／Spec。
- Spec／Design → Backend、Frontend：消费同一冻结战略基线，各端完成接收、对账和实现准入。
- Backend → Frontend：消费当前后端包、接口与运行证据。
- 专职交付 → Spec：主控消费显式同功能 checkpoint、Receipt 和交付引用，统一计算业务验收。

前端可提前核对战略输入、准备工程设计和计划；有后端／API／数据依赖时，正式实现等待当前后端交付与批准合同。纯 UI 路线须核验有依据的不适用记录。产品设计不适用和生产前端不适用分别判定，不能用其中一个代替另一个。

## 读取结果和处理等待

`lifecycle target` 不带写入参数，以及 `lifecycle status`，都只读消费当前证据，不启动实现、业务测试或回归。JSON 命令结果中：

| 字段 | 如何使用 |
| --- | --- |
| `progression.target`、`target_source`、`intent_source` | 本次目标、默认／功能意图来源和原指令说明。 |
| `progression.status`、`reached`、`reason` | 当前是否达到及证据理由；状态为 `pending`、`reached`、`not-applicable` 或 `blocked`。不适用必须已有当前依据。 |
| `progression.completion.milestone/profile/business` | 分别查看本次目标、本端职责和整体业务，避免把短目标当最终交付。阶段核验另见 `completion.stage`。 |
| `coordination` | 仅汇总显式绑定消费者的当前来源、接收、版本和专职交付。 |
| `next_action` | 实际承接者、工程、checkpoint、工作单元和等待原因；不是执行授权。 |

`next_action.kind=target-reached` 表示本次停点已达到，仍保留真实 `next_work_unit`。`await-consumer` 表示等待已绑定端，`verify-current-inputs` 要先处理当前证据问题。缺证据时待核验，过期 Receipt、版本失配、错功能／Profile 或 Context 冲突不能误报完成。

## 漂移、恢复与回退

篡改计划、输入或被观察资产变化时 apply 拒绝执行；先保留现场并处理诊断，再生成新计划。成功计划在其他观察输入仍当前时可幂等重试。必要事务材料进入既有恢复／回退协议，目标意图始终不授予批准或实现资格，也不进入批准／交接证据闭包。

```sh
yss recover --root ./spec --json
yss recover --root ./spec --apply --json
yss rollback --root ./spec --json
yss rollback --root ./spec --apply --json
```

普通 `recover`、`rollback` 默认只读查询，增加 `--apply` 执行保护性恢复或最近成功事务的整体回退；须先确认实际事务范围，后续用户修改会阻止覆盖。它们与程序事务的 `update recover/rollback`、迁移事务的 `migrate recover/rollback` 是不同入口。
