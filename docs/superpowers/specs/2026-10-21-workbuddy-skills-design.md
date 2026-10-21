# WorkBuddy Skills 支持设计规范

状态：规划基线，来自本会话已展示的设计；未实施、未实机验收。用户随后要求构建执行计划，未授权发布、推送或自动提交。

## 1. 目标与范围

在 YSS 的 Spec、Backend 项目中支持 WorkBuddy 桌面端代码开发场景，复用现有技能、项目治理规则、来源锁和安装事务。支持范围按 WorkBuddy 版本、操作系统及 Profile 分别记录。

首版交付项目级技能发现与调用适配；不包括用户级安装、技能市场、ZIP 发布、自动安装 WorkBuddy、新 MCP、Hooks 或子代理能力全面对齐。

## 2. 基线与事实

2026-10-21 检查时，CLI HEAD 为 `7e889632799036c7112f794792bb7338cfc522fa`，模板 HEAD 为 `cd5f4580e545440974bc3d35baef11660f583aaf`，工作树均无变更。此处记录源码基线，不代表 CLI 当前 Bundle 来源；当前正式来源锁另有固定提交，实施时必须分别核对。

- canonical 根为 `.agents/skills`；根模板的投影为 `.claude/skills`，Backend overlay 投影为空。
- 两个分发政策均排除 `.codebuddy`；Spec Bundle 初始闭包仅接受 `claude`，Backend 使用 `profile-full`。
- CLI 内嵌的 `native/instance-metadata.mjs` 会替换模板同名消费者；两个来源均需适配。
- 模板当前同步器可能使用符号链接并删除重建漂移目标，不满足 WorkBuddy 普通副本与冲突保护要求。
- WorkBuddy 官方说明代码开发时兼容用户级 `.codebuddy` 配置，并引用 CodeBuddy 目录规范。CodeBuddy 项目技能规范使用 `.codebuddy/skills`。这不等于桌面端所有行为已验证。

## 3. 核心决策

1. 唯一维护来源保持 `.agents/skills`；WorkBuddy 运行时标识为 `workbuddy`，项目投影根为 `.codebuddy/skills`。
2. WorkBuddy 投影为完整普通文件资源树；不使用宿主根符号链接，也不增加 WorkBuddy 专属技能正文副本。
3. Spec 的目标投影清单为 `[.claude/skills, .codebuddy/skills]`；Backend 为 `[.codebuddy/skills]`。不恢复已退役的 Codex、Cursor、Pi 投影。
4. Spec 保持技能按需安装：只给已安装技能及其依赖生成两个已声明投影；`--full` 仍通过闭包完成投影。Backend 保持 `profile-full`，分发其实际技能集合的 WorkBuddy 投影。
5. 旧项目只在显式同步并应用保存计划后接入 WorkBuddy；查询不自动安装或迁移。不新增平台选择命令。
6. `.codebuddy` 目录仅允许分发 `skills` 子树；账号、settings、MCP、日志、缓存和用户配置均不随 Bundle 分发。
7. 沿用现有锁、事务、摘要及权限合同。新增 runtime 本身不要求提升锁 schema；不假定旧二进制兼容新增 runtime。

## 4. 入口与宿主验证

实机验证先检查项目级 `.codebuddy/skills`、资源相对路径、刷新方式、同名优先级、显式调用限制，以及 `AGENTS.md` 是否自动加载。

若 WorkBuddy 自动读取 `AGENTS.md`，不增加入口文件。若不读取，验证 `CODEBUDDY.md`，仅在确认有效后分发薄引导：先读取同目录 `AGENTS.md`，按其要求读取 `CONTEXT.md` 和适用政策；入口读取失败时停止依赖它的写入。已有用户文件不得覆盖。

不能从 CodeBuddy CLI 的版本、`/skills` 输出、IDE 文档或模型自述推断 WorkBuddy 桌面端已加载技能。若项目级加载失败，暂停依赖该路径的实现，修订规范；不自动退到用户级目录。

## 5. 生成、分发与冲突

模板源生成器可从 canonical 派生普通副本。已有副本与 canonical 完全一致时不重写；副本与上次 Git HEAD 基线一致时允许随 canonical 更新；目标存在本地修改、暂存修改、占用、链接或无法确认来源时拒绝覆盖。实例端安装与更新统一使用 CLI 计划及事务，不能直接运行源维护同步器替代事务。

运行时集合、`projectionRoots`、共享技能 `targets`、实际文件和受管记录必须一致。Backend 也必须写入与锁匹配的 runtime 与 installedSkills 元数据；不能沿用空 runtime 配带非空投影锁。

源锁记录上游 revision、原摘要和未知元数据继续保留。资源树摘要与原字节、可执行位分别按既有合同检查；权限变化不能仅因规范化树摘要一致而放行。

## 6. 查询与可用性

保留 `skills list / resolve / ensure` 和 JSON envelope 的现有职责。新增 `workbuddy` 仅表示所选投影的文件校验，不为旧实例参数赋予新安装权限。

- `ready`：所选技能及依赖、canonical、所选投影、受管状态和固定来源符合合同。
- `missing`：该技能从未安装且所有相关路径无占用；已登记文件丢失是阻断。
- `blocked`：来源、权限、受管状态、投影或路径发生冲突。

文件校验、宿主发现、实际调用是三个不同结果。没有桌面端可靠目录接口时，自动宿主观测返回 `unknown`，不得用目录扫描冒充发现，不得修改现有评估器使未知观测通过。人工实机报告独立记录，不覆盖机器阻断结果；自动发现资格留待有可靠证据后单独验证。

## 7. 更新、兼容与回滚

新增投影、锁与元数据进入同一保存计划。冲突保留用户修改与非受管文件。移除受管副本或事务回滚仍执行原字节、权限和来源守卫。

旧 CLI 遇到新增 runtime 必须明确失败，不能静默忽略；对实际仍可运行的旧版记录升级顺序与限制。不承诺旧二进制对未知 runtime 都能给出友好错误；无法满足时文档要求先升级 CLI。

正式 Bundle 只消费固定来源。未提交模板可按仓库已有 working-tree 锁合同做本地装配，但不得把本地实验包标成正式发行；正式来源刷新需满足既有提交、推送和版本资格。

## 8. 验收

自动化覆盖投影、Profile 分发、锁及元数据、闭包、resolve、冲突、回滚、查询只读和旧平台回归。Go 测试使用临时目录与小型内联数据，不访问网络。

实机记录 WorkBuddy 桌面版本、内置执行核心版本（可取得时）、操作系统、cwd、CLI 与 Bundle 来源、技能摘要、有效配置和实际调用证据。Spec、Backend 分别验证入口读取、技能调用、资源访问、只读 CLI、更新刷新、同名冲突、权限不足与取消。

通过文件测试只获得代码与文件分发验证结果；通过实机用例后只声明相应版本/平台/Profile 的技能支持。缺少可靠 catalog API 时仍保留自动发现 `unknown`，不能宣称自动就绪已验收。

## 9. 来源

- [WorkBuddy 配置兼容说明](https://www.workbuddy.cn/docs/workbuddy/From-Beginner-to-Expert-Guide/Function-Description/Setting)
- [CodeBuddy 目录规范](https://www.workbuddy.cn/docs/cli/codebuddy-dir)
- [CodeBuddy Skills 规范](https://www.workbuddy.cn/docs/cli/skills)
- [WorkBuddy 技能说明](https://www.workbuddy.cn/docs/workbuddy/From-Beginner-to-Expert-Guide/Function-Description/Skills-Market)
- [开放平台技能包规范](https://open.workbuddy.cn/docs/skill)

这些官方资料在本会话前一轮调研中已读取；运行时行为最终以绑定版本的 WorkBuddy 实机证据为准。
