# 迁移覆盖与切换清单

本工程当前为 `1.0.0-alpha.4`。本表记录当前能力及实际限制，不授予稳定发布资格。四旧 CLI 的退役范围、现役消费者、历史恢复和发行条件见 [退役合同](cli-retirement.md)；必要 Node/Python 治理工具继续维护。

| 能力 | 当前实现 | 切换条件 |
|---|---|---|
| 四种 Profile 初始化、接管、差异、同步 | 独立 Go 固定来源 Bundle v2、只读计划、摘要绑定应用；拒绝混家族和未知身份 | 四 Profile 实例矩阵及现役消费者通过后切换；停用参数明确拒绝和替代说明 |
| 迁移和恢复 | 显式旧→Go metadata 转换；原字节、mode、WAL、对象归档；最新迁移 kind 回退 | 旧事务由旧执行器恢复；历史布局、外部归档与手工冲突解决仍未迁移 |
| 程序安装 | `update plan/apply/status/recover/rollback`；指定 `--tool-root`、发行包 SHA-256；独立安装记录和持久归档 | 只接受显式离线发行包；恢复锁内匹配 `program-update`，回退最近一次成功安装；无网络自动查询、全局 npm 更新或发布动作；其余平台恢复仍需实跑 |
| Skill/阶段资源补装 | 已登记的资源/Skill 闭包；Spec selected lock 更新 | Inspect 暴露闭包和 unsupportedReason，必要消费者出现 UNPORTED 即阻断 |
| Context、生命周期 | 唯一根合同、词汇、摘要、快照核验；当前登记状态查询；已接入当前 checkpoint、阶段、批准、决定与下一路由原生校验 | 本地预发布验收以冻结报告为准；校验不创建批准或授予实现资格 |
| Stage work | JSON checkpoint 的 register/update/apply、语义校验、依赖/stale/证据检查 | 自动创建 checkpoint、refresh、YAML 写入及外部 Schema 引用仍未迁移 |
| contract/evidence/handoff | `check` 保留结构合同；`verify` 已接入批准、真实决定、task、Slice、scaffold、验证结果、目录/虚拟 ZIP 包与消费收据原生语义 | 未知类型或尚未覆盖的协议分支明确拒绝；缺少本地规则或当前期待明确拒绝，禁止 Schema 替代领域规则 |
| runtime | 纯 Go SQLite v1，inspect/begin/event/complete；run/events/commands/pins 只读查询；Go token 的幂等 pin/unpin，终态可用 | inputs/reports/checkpoint/清理、备份/恢复等旧能力仍走旧路径；只读 WAL 明确拒绝，避免生成 sidecar；保护标记不删除证据、不授予批准 |
| project-ci | 默认完整治理；显式 `--scope native-go` 保留有限检查；完整范围包含 checkpoint/task/approval/next-route/Slice/scaffold/验证证据/Handoff/evidence refs/基线保护 | 本批完整检查仅支持 runtime-store off，默认旧工作流不切换 |
| 四旧入口及 Spec JS API | 历史 discovery/拒绝与显式 native transport | 按消费者迁移至版本化 envelope；无人消费的旧成功 schema/API 不全量仿制 |
| 插件 | 固定二进制、bundle inspect/export、事务绑定 init/migrate/sync/recover/rollback | 固定来源打包、旧实例桥接、实际取消/恢复/回退和完整交接验收闭合后发行 |

Schema 已固定 `jsonschema/v6`、`yaml/v3`、Unicode15 Python 字符类和离线引用闭包。差分语料包含现实 Schema、JSON/YAML 标量、format、动态/嵌套 ID 引用。旧 Python 环境缺少可选 date-time checker 时的误接受在 Go 中严格拒绝，属于显式兼容差异。高级正则不能支持时明确拒绝，不静默放宽。

治理接口使用统一报告和退出码 0/1/2，失败保留逐项诊断及实际输入绑定，固定 `approval_created=false`。读取后重新核验文件、mode、扫描集合、Git 与适用在线事实。只检查既有证据，不执行业务命令。固定工具事实通过实际本地字节或显式 `--tool-root` 消费，不补齐缺失的项目权威资产。接口与严格差异见 [原生治理校验合同](native-governance.md)。本地整批验收与六平台原生运行、旧入口完整兼容和稳定发布门禁分别记录。

受管写入有计划重建、输入摘要、进程锁、fsync、WAL、归档与原子目标替换。事务计划使用 schema v2，在锁内分配持久递增 sequence；创建时间只用于展示，回拨系统时钟不改变恢复与最新成功事务的顺序。早期 alpha 的无序号 v1 归档明确拒绝，须用对应旧 alpha 执行器恢复，不猜历史顺序。已观察文件在写前及提交前核验；文件集合中并发新增输入的隔离仍需补齐，不能把进程锁解释为整个文件系统隔离。

Windows 文件模式按 readonly/write 能力解释，不声称支持 POSIX execute 位；现有 readonly 删除、替换及 readonly 输出当前明确 `UNPORTED`。Windows 持久目录 flush 与所有六平台的原生取消/恢复/权限测试尚须验证。

SQLite 原生记录采用短 busy 等待：数据库持续被其他连接占用时明确返回 `RUNTIME_BUSY`，调用者可重试幂等动作；已取消的上下文返回 `CANCELLED`。当前不模拟旧执行器的 5 秒锁等待。已有 WAL 数据库不隐式转换 journal mode，查询及记录保护动作在打开前明确拒绝；旧 WAL 能力继续使用旧执行器。

稳定切换前需完成本次已确认的必要能力与消费者、固定二进制/Skills/投影/CI/Bundle、真实旧实例恢复、六个平台原生验证，以及固定提交来源和不可裁剪集成门禁。当前不声称稳定目标完成。
