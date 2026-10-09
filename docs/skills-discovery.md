# 内置技能按需查询

适用 spec、backend、frontend 的固定 Bundle 内共享技能。自然语言选技能由 Agent 完成，CLI 确定性解析 ID、已登记别名和上下文依赖；不下载外部技能。

```sh
yss skills list --root /path/to/project --json
yss skills list --details --root /path/to/project --json
yss skills resolve code-review codebase-design --agent-runtime codex --root /path/to/project --json
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

仅展开 `context-required` 和命中 `--when` 的 `context-conditional`，不展开 review-only、coordination-only、component-dependency 或角色技能集合。资源与 Codex 投影逐文件核对来源、原字节和权限；未选技能不做内容核验。

Agent 收到 missing 后，将 `missing` 集合交给现有事务入口：

```sh
yss skills ensure code-review codebase-design --root /path/to/project --plan --out /outside/project/new-plan.json --json
yss skills ensure code-review codebase-design --root /path/to/project --apply --plan-file /outside/project/new-plan.json --json
```

既有任务授权覆盖范围且计划无冲突才应用；安装后重新 resolve。整体 ready 后，在当前会话读取 `entryPath` 并记录 `contentDigest`，不依赖原生技能目录刷新。`invocation.invocation_mode=user` 仍需用户明确调用；就绪不授予业务批准、阶段资格或实施权限。文件变化后重验。

受管文件丢失、内容/权限/投影漂移、路径占用、来源不匹配均为 blocked，不强制覆盖、隐式 sync 或 migrate。1.3.4 将新指引绑定到已提交的固定 Bundle；新建实例直接消费。既有实例需显式核验并通过 sync 更新受管指引，不在查询时隐式升级。当前发行及原生验收范围为 darwin/arm64，运行时就绪核验仅支持 Codex；其他平台与运行时未取得本次验收资格。
