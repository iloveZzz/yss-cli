# WorkBuddy 项目级 Skills

运行时标识为 `workbuddy`，项目技能目录为 `.codebuddy/skills`。普通文件资源树由 `.agents/skills` 生成，技能正文只维护 canonical 来源。

CLI 2.7.0 的[来源锁](source-lock.json)固定模板 `v4.7.0`，为 Spec、Backend 提供 WorkBuddy 文件分发和就绪查询支持。本版本的平台资格与实际验证范围以对应发行说明及其证据为准。WorkBuddy 桌面端尚未实机验收，没有已确认的桌面版本支持组合。

## 安装与更新

使用同一固定来源的 CLI 与内嵌 Bundle；CLI 2.7.0 对应模板 `v4.7.0`。安装或升级前核对该平台的实际发行资格；旧二进制和旧 Bundle 不会因为安装 WorkBuddy 自动获得适配。

Spec 按需安装技能及其依赖，并分发 Claude、WorkBuddy 两个声明投影。Backend 保持 `profile-full`，分发该 Profile 的完整技能集合；不恢复退役的 Codex、Cursor、Pi 副本。

已有项目先升级 CLI，再核对并应用显式同步计划：

```sh
yss sync --root /path/to/project --plan --out /outside/project/new-sync-plan.json --json
yss sync --root /path/to/project --apply --plan-file /outside/project/new-sync-plan.json --json
```

计划文件须不存在，应用时消费实际保存计划。冲突按现有范围授权处理，路径占用、投影修改、来源不匹配和受管文件丢失不能强制覆盖。实例端不直接运行模板源 `scripts/sync-skills` 写入 WorkBuddy 投影。

## 调用

在 WorkBuddy 中选择项目根目录。首次会话显式要求读取根 `AGENTS.md` 和 `CONTEXT.md`，再按项目入口加载任务政策。自动入口、`CODEBUDDY.md` 引导和热刷新尚未验证，不默认启用新的入口文件。

```sh
yss skills resolve code-review --agent-runtime workbuddy --root /path/to/project --json
```

消费 `result.status`：

- `ready`：canonical、所选投影、来源和受管状态符合文件合同；读取返回的 canonical `entryPath` 并绑定 `contentDigest`。
- `missing`：技能从未安装且路径无占用，按既有 `skills ensure --plan / --apply` 补齐后重验。
- `blocked`：处理报告中的原因；查询不会安装平台、隐式同步、迁移或覆盖文件。

省略 `--agent-runtime` 保留 canonical 查询；显式 workbuddy 要求实例已登记该运行时，参数本身不安装平台。原 Codex/Cursor/Pi 显式参数保留兼容语义。就绪不授予业务实施或批准权限。

## 宿主发现与限制

文件就绪、桌面发现和实际调用分别报告。当前没有经过验证的 WorkBuddy desktop catalog 接口：

```sh
node scripts/inspect-skill-discovery --runtime workbuddy --cwd /path/to/project
```

该观测返回 `unknown`、退出 2，不要求 `--binary`。不能用 CodeBuddy CLI 的版本或目录清单替代 WorkBuddy 桌面证据，未知观测不会转为通过。

| 能力 | 当前边界 |
|---|---|
| 普通文件分发、来源锁、资源、权限、冲突和恢复 | CLI 实现和自动化检查覆盖；实际验证范围见本版本发行说明及检查记录 |
| 桌面显式调用、资源相对路径、刷新与同名优先级 | 待实机验证 |
| `AGENTS.md` 自动加载 | 待验证，首次显式引导读取 |
| `disable-model-invocation`、`user-invocable` | 保留元数据，桌面执行语义待验证 |
| Claude/Codex 专属变量与工具名 | 不保证等价映射，缺能力时暂停受影响步骤 |
| Hooks、fork、子代理 | 不作为首版关键流程依赖 |
| 外部 CLI、Node/Python/Java 等运行时 | 按技能要求检测，不因技能可读推定依赖存在 |
| 技能市场、ZIP、用户级安装 | 不在本轮范围 |

## 验收记录

当前桌面支持矩阵没有已验收组合。CLI 和分发检查的环境与结果见本版本发行说明及检查记录；这些检查不代表对应平台的 WorkBuddy 桌面可用。

后续实机每个组合记录：WorkBuddy desktop 版本、可取得的执行核心版本、系统与架构、cwd、CLI/Bundle 来源、技能摘要、有效配置、入口与刷新方式，以及实际调用证据。Spec、Backend 分别验证入口、显式调用、references/脚本、只读 CLI、更新、同名与关闭技能、权限不足和取消。未执行用例标记 `not-run`。

既有仓外 `work/workbuddy/` 的实验二进制、工作树锁和原始验证日志保留为历史材料。本版本固定来源见[来源锁](source-lock.json)，验证要求见[测试与 CI 状态](ci-verification.md)，原生资格与公开包验证按[发行证据流程](../tools/release-evidence/README.md)取得；实际结果和证据引用在本版本发行说明中登记。

## 官方依据

- [WorkBuddy 配置兼容说明](https://www.workbuddy.cn/docs/workbuddy/From-Beginner-to-Expert-Guide/Function-Description/Setting)
- [CodeBuddy Skills 规范](https://www.workbuddy.cn/docs/cli/skills)

目录规范是设计依据，WorkBuddy 桌面行为以对应版本的实机记录为准。
