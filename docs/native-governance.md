# 原生治理校验使用合同

本文件记录 `1.0.0-alpha.4` 本地预发布候选的治理接口；整批验收须以冻结输入、独立审查和原始验证记录为依据。实现检查通过不会创建批准、设置 Ticket 状态、授予实现或发布资格。

所有接口读取项目本地资产；不执行资产中登记的验证命令。`check` 保留结构校验语义，`verify` 消费相应领域规则，不能用 `--schema` 绕过。默认完整 CI 与显式有限 CI 分开报告。

```sh
yss lifecycle verify --root /project --checkpoint docs/.scratch/feature/checkpoint.yaml --json
yss contract verify --root /project --kind slice --file docs/contract.json --checkpoint docs/.scratch/feature/checkpoint.yaml --json
yss contract verify --root /project --kind scaffold --file docs/scaffold.json --json
yss contract verify --root /project --kind task --file docs/review-task.json --json
yss evidence verify --root /project --kind approval --file docs/approval.json --checkpoint docs/.scratch/feature/checkpoint.yaml --json
yss evidence verify --root /project --kind user-decision --file docs/decision.json --requirements docs/current-decision-requirements.json --json
yss evidence verify --root /project --kind verification --file docs/verification.json --json
yss handoff verify --root /project --kind package --package docs/handoff --json
yss handoff verify --root /project --kind consumption --file docs/consumption.json --consumer tactical --json
yss project-ci check --root /project --runtime-store off --json
yss project-ci verify --root /project --base FULL_40_HEX_COMMIT --runtime-store off --json
yss project-ci check --root /project --scope native-go --json
```

会签的当前期待来自 checkpoint 或正式审查任务。用户决定须提供一个独立消费者：`--requirements` 事项数组、`--checkpoint` 的当前 human review，或 `--task` 的正式任务。待验记录、批准包或回复不能反向定义自己的 subject、范围与 owner。延续证明使用 `--continuation`，独立事项须以 `continuation_ref` 指向该证明。

后端交付和战略交接的专项验证报告需要当前独立消费绑定。正式任务必须完整核验，符合当前专业工作单元与消费者路由，并以其已声明预期证据与实际结果证据共同指向唯一来源记录，再核对报告引用与摘要。普通 Plan 任务的证据声明不能替代专业消费依据。已完成后端终点仅在内部只读消费审计中重验当前 checkpoint、终点记录与完整交付证据；公开任务校验保留原有终点恢复禁令。缺少期待或存在多个候选时拒绝，不能从待验报告反找一个来源证明自己。

历史会签、任务包或 checkpoint 结构检查使用 `--history`；报告范围为历史结构，不满足当前批准或流转。未知版本、未迁移规则或缺少当前阶段能力返回明确拒绝；未来阶段尚未适用的能力不要求预装。

部分领域必须核验固定工具的来源摘要，可用 `--tool-root /fixed-tool-source` 指定只读工具源码。它只提供被观察的工具事实，不能补齐项目或 Handoff 来源中的 Context、注册表、批准、Schema 等权威资产。Frontend Git 模板使用显式 `--template-checkout /checkout`；登记 repository、commit、Git tree/blob 与实际合同分别校验，不执行生成器。ZIP 包在内存中建立只读来源视图，不解压；路径、Unicode 折叠冲突、链接、数量、大小和清单不一致均拒绝。

一次调用共享读取视图，报告实际读取的文件描述、摘要、扫描集合和 Git 观测。物理文件在读取前后绑定文件身份、修改时间、大小和权限，结束前重读文件与集合；同字节替换、新增、删除或摘要变化导致拒绝。注册一个尚不存在的目标仅观察缺失，不授予其父目录的读取权限。适用的在线探针仅执行固定协议的只读 GET，绑定地址、响应合同与环境输入，并在文件最终核验前重新检查；不运行资产中登记的命令。所有结果包含 `approval_created=false`，失败也保留报告。

| 退出码 | 含义 |
|---|---|
| 0 | 当前请求的检查通过，有限或历史范围仍按报告标注 |
| 1 | 门禁、批准、证据或语义拒绝 |
| 2 | 输入、能力、取消或执行异常 |

统一 JSON envelope 版本 1、运行协议 1；结果包含逐项检查、诊断、输入绑定、适用性与覆盖范围。完整 CI 本批只支持运行存储 `off`。默认工作流、旧四 CLI/API、插件、Skill 入口和业务验证命令不在本批切换范围。

有意比旧实现更严格的差异必须在验收矩阵登记：没有独立消费者的用户决定不再自证；RFC 3339 时间拒绝非标准日期，机器验证不接受未来执行时间；当前流转不能用带阻塞诊断的派生阅读视图证明已就绪；缺失本地用户决定策略不能解释为无需批准；来源 v2 注册表须为已支持版本且处于 active 状态，冻结其摘要不能代替版本与状态校验。版本数字按精确 JSON 整数值比较，允许 `1.0` 与 `1e0`，拒绝被浮点舍入掩盖的小数差异和越界值；旧 JavaScript 摘要的数值规范化合同另行保留。

Slice 的 `verify` 必须消费当前 checkpoint 中对应批准，或显式 `--approval-ref`；多工作单元默认逐项验证，`--unit` 可选择已声明工作单元。缺少批准不能按结构检查通过，历史结构仍使用 `check`。

完整 CI 的 `--checkpoint` 和 `--task` 只追加必需入口及其依赖，不能缩减已登记扫描范围。CI 直接调用已实现的脚手架、验证证据、Slice 与 Handoff 专属校验器。闭合 Handoff 包用包内捕获的来源 Context/规则验证，消费者继续使用接收项目本地权威资产；包损坏不能通过略过其内部文件逃避校验。

Handoff 消费合同拒绝未知版本；v2 及之后的收据必须绑定实际接收 Profile 及已选择的消费能力。目录包与 ZIP 统一采用 Unicode full case fold 检查文件及原始引用碰撞，包含旧 JavaScript final-sigma 碰撞；ß/SS 等也拒绝，属于已登记的更严格差异。
