# 内置技能按需查询

适用 Spec、Backend 的固定 Bundle 内共享技能。自然语言选技能由 Agent 完成，CLI 确定性解析 ID、已登记别名和上下文依赖；不下载外部技能。

```sh
yss skills list --root /path/to/project --json
yss skills list --details --root /path/to/project --json
yss skills resolve code-review codebase-design --agent-runtime workbuddy --root /path/to/project --json
yss skills resolve code-review --agent-runtime codex --when lifecycle-document-output --root /path/to/project --json
```

普通 list 保持原字符串数组。详情结果的 `skills` 提供 `id`、`aliases`、`description`、`invocation`、`source` 和 `installation`；`verification=metadata-only` 表示没有核验技能树。`source.kind=builtin` 表示固定分发来源，`source.lock` 保留原上游来源和 revision。

resolve 沿用 `outputVersion=1` JSON envelope。有效查询退出 0，必须消费 **`result.status`**；参数、身份或执行失败沿用现有错误 envelope 和非零退出。未知、退役、废弃及非内置共享技能拒绝解析，不能进入自动补装。当前就绪核验需要可验证的原生受管状态；旧实例返回阻断，不自动迁移。

| 字段 | 含义 |
|---|---|
| `readOnly` | 恒为 true；查询不创建计划、缓存或事务 |
| `canonicalIds` | 去重后的依赖顺序；仅所选技能及其上下文依赖 |
| `when` / `dependencies` | 生效条件及依赖理由；条件是逗号分隔的已登记名称 |
| `status` | `ready`、`missing` 或 `blocked`；闭包中任一阻断使整体阻断 |
| `missing` | 从未安装且路径无占用的规范技能 ID 集合 |
| `issues` | 受影响技能、问题代码及原因 |
| `skills` | 每项注册信息及就绪状态；整体就绪后才读取和调用 |
| `entryPath` | 仅 ready 项返回的已核验绝对 canonical SKILL.md 路径 |
| `contentDigest` | 入口原字节 SHA-256，供当前会话的实际读取记录绑定 |
| `effectiveHash` | 使用现有锁算法计算的规范化资源树摘要；权限另由受管描述核验 |

仅展开 `context-required` 和命中 `--when` 的 `context-conditional`，不展开 review-only、coordination-only、component-dependency 或角色技能集合。资源与显式选择的运行时投影逐文件核对来源、原字节和权限；未选技能不做内容核验。WorkBuddy 参数需要实例通过显式同步登记该 runtime；文件 ready 不代表桌面发现已验证。省略参数时仅核验 canonical，原 Codex/Cursor/Pi 参数保留兼容语义。

Agent 收到 missing 后，将 `missing` 集合交给现有事务入口：

```sh
yss skills ensure code-review codebase-design --root /path/to/project --plan --out /outside/project/new-plan.json --json
yss skills ensure code-review codebase-design --root /path/to/project --apply --plan-file /outside/project/new-plan.json --json
```

既有任务授权覆盖范围且计划无冲突才应用；安装后重新 resolve。整体 ready 后，在当前会话读取 `entryPath` 并记录 `contentDigest`，不依赖原生技能目录刷新。`invocation.invocation_mode=user` 仍需用户明确调用；就绪不授予业务批准、阶段资格或实施权限。文件变化后重验。

受管文件丢失、内容/权限/投影漂移、路径占用、来源不匹配均为 blocked，不强制覆盖或隐式 sync。1.3.4 将新指引绑定到已提交的固定 Bundle；新建实例直接消费。既有实例需显式核验并通过 sync 更新受管指引，不在查询时隐式升级。历史发行及原生验收范围为 darwin/arm64；本轮增加 WorkBuddy 项目文件适配及离线校验，不增加桌面原生验收资格。宿主 catalog 无可靠接口时返回 unknown，详见 [WorkBuddy 接入与边界](workbuddy-support.md)。

## 独立技能安装（新安装合同）

Bundle5 / metadata5 / plan4 支持一次性的 `skills` 或 `governed` 安装选择。新建默认 skills；旧来源 Bundle3/4 保持 governed，不能凭新参数假定旧来源已经包含普通入口。

```sh
yss init --root /path/to/new-project --profile spec --installation skills --plan --out /outside/project/init-plan.json --json
yss init --root /path/to/new-project --apply --plan-file /outside/project/init-plan.json --json
```

`init`、`attach` 可选择安装方式，`migrate --installation skills|governed` 显式转换已有 metadata4/5 实例；未给选择的 migrate 保留旧治理迁移语义。`sync` 保持现有安装方式，不接受该参数。应用只消费保存计划，不能另加 installation 改写选择。正式 `profile prepare` 继续生成 governed 目标。`--full` 只扩展当前安装方式的技能，不切换是否治理。

普通技能由宿主按 SKILL.md 名称与描述发现，不要求先 route、resolve-ready、tracker setup 或创建 checkpoint。`skills resolve` 仍是原受管就绪验证，不是通用本地技能发现器。正式操作在 skills 工程返回未启用治理；真实工程测试、授权范围、分发完整性与写入冲突检查保留。

迁移计划保留原任务、批准、checkpoint 及用户文件；活动或无法核验的正式绑定拒绝转换。规划后输入变化需要重新规划。以上是新合同的开发能力说明，不表示固定发行 Bundle 或用户项目已升级。
