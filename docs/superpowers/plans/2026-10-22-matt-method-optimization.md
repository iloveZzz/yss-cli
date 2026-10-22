# Matt 思想增强后续优化 Implementation Plan（CLI）

> **For agentic workers:** 实施时逐项使用 `superpowers:executing-plans` 或可用的 `superpowers:subagent-driven-development`；每批完成独立 review、修复、复验后推进。用户已批准执行本计划；C3/C4 仍按后续设计条件推进。

**Goal:** 恢复通用发布入口、接通已有失败诊断，并由真实观察驱动后续准备和集合预检设计。

**Architecture:** 复用现有发行证据、结构化诊断和治理检查。固定恢复器核验历史可追溯性后退役，严格绑定检查接入正常入口；模板拥有治理合同，CLI 只消费固定来源。C3/C4 的交付是后续设计补充，须另行审阅后才能排入产品实现。

**Tech Stack:** GitHub Actions、Python 标准库、Go、Node 现有装配工具；不增加外部测试服务或发布状态数据库。

**Spec:** [CLI 设计](../specs/2026-10-22-matt-method-optimization-design.md)。

日期：2026-10-22。状态：**approved-in-conversation**。基线 `75ef66aff6c38481f06b233fdefc8958b37f83f4`；正式源码 v2.7.1 / `5cc28f9c43788fdc618197f3ad2689336a5e425a`。下列清单保留批准时目标；实际进度与证据由方案 PR 执行表及关联实现 PR 记录，预期命令不代表已执行。

用户追加指令（2026-10-22）：关联模板 T1 的本地与远端公共 slow 验证硬上限由 300 秒调整为 1000 秒；配套将工作流 job 总上限由 20 分钟调整为 30 分钟，为准备和报告留余量，作为本轮后续实施约束；原 300 秒预算下的真实失败、取消及未执行记录继续保留，不改判为通过。

配套：[模板执行计划](https://github.com/iloveZzz/yss-spec-project-template/pull/112)。

## 全局约束

- 用户已批准实施本计划，使用独立工作树与实现 PR；固定来源、正式版本、合入及发布继续按实际工作包和有效具体授权执行。C3/C4 的未来接口先完成专项设计，不提前扩大。
- 普通 Go 源码仓遵循根 AGENTS，不补建产品实例、checkpoint 或 Slice。
- Mac 资格保持 `local-platform-program-install` 与 `requiredPlatforms=['darwin/arm64']`；Windows x64 保持 build-only、`stableReady=false`。
- `CGO_ENABLED=0`；Git 2.44+；测试使用临时小数据和可控 transport，不访问网络。单例约 1 秒、包约 30 秒、全量 Go 5 分钟硬上限。
- 模板公共 `ci-gate --mode slow` 按追加指令采用 1000 秒验证上限、30 分钟 job 总上限，准备不计入验证预算，为准备和报告保留 800 秒余量。模板普通检查单项 300 秒、发布 prepare 1000 秒、runner 在线命令 960 秒和 Go 全量测试 5 分钟上限各自保持。
- 既有 JSON envelope、退出码、来源/摘要检查、真实用户授权和最后输入复核不放宽；`.codebuddy` 不恢复为受支持目录。
- 失败、取消、未执行和未知结果如实记录，不补造回执，不覆盖旧失败，不以新说明改变旧证据结论。

## Review 重点

1. 远端 404、权限不足和网络失败是否被混作 release 不存在：C1 必须提供分别可控的反例。
2. tag/说明在初次检查后变化，或同 HEAD 的错误 workflow/phase 产物：C1 必须在实际写入前阻断。
3. 403 早期退出后 stdout 摘要、后续未执行状态是否仍可信：C2 验证原始文件和实际退出语义。
4. 自然语言中引用、否定、范围缺失是否被当作用户复核授权：C3 设计必须保留原话并列出拒绝示例。
5. 跨 Slice 看似不同路径实际共享根/输入，或预检后漂移：C4 设计不能宣称全局派发安全。

## 顺序与批次交付

**T1 / C1 并行 → T2 → C2 → T3 平台指标 → T4 真实任务观察 → C3 / C4 后续设计。** T 批次见模板计划。C1 无需等待新的模板版本；C3/C4 通常独立设计，只有具体合同缺口才建立相互依赖。如改变治理合同，先模板设计/实现验证，再固定来源进入 CLI。

每批单独形成可审阅变更，review 同时检查“符合设计”和“遵循仓库约定”。阻断问题修复后复验受影响项；新增改动引入的新风险决定是否扩大测试，不无理由重复全量。

原方案文档核验与当前实际实施分别记录在方案 PR；运行非发布 CI 和定向测试按批准计划执行，不把原文档核验冒充产品验证。

## C1：恢复通用 publisher，退役固定恢复器

**目标文件：**

- 修改 `.github/workflows/publish-release.yml`、`tools/release-evidence/README.md`。
- 新增候选 `tools/release-evidence/publish_contract.py`、`tools/release-evidence/test_publish_contract.py`；不新增活跃恢复 workflow。
- 核验历史可追溯性后删除现役 `.release-recovery/upload.py`、`.release-recovery/bindings.json`；不得修改原绑定以使已发布 v2.7.1 再次通过。

**输入/输出：** 消费显式 tag、prepare run、候选新增的 notes_commit，以及现有 metadata/8 文件上传集合；输出通过或失败及已观察的事实。检查器不调用写 API，不产生新的发行资格。

- [ ] 先写离线 fixture 失败用例：另一未来稳定版本的完整合法输入可以通过；旧固定 tag 默认、缺 notes_commit 和错误提交关系被拒绝。
- [ ] 增加错仓、错 workflow、错 phase、失败/未完成 run、HEAD 不符、过期/重复/缺 artifact、模板/版本/摘要漂移和上传多余文件反例。
- [ ] 增加 release 已存在、API 查询不明、检查后 tag/说明变化、部分写失败和未知写结果反例；断言不创建或不进行第二次写入，真实输出保留。
- [ ] 增加假 API 创建后回读反例：draft/prerelease、tag/说明字节变化、资产缺失/多余/摘要不符、latest 错指和读取失败；断言记失败待核实且没有第二次写入。
- [ ] 运行拟新增的 `python3 -m unittest discover -s tools/release-evidence -p 'test_publish_contract.py'`；确认首先因缺少预期检查失败。这是计划新增测试，当前尚不存在。
- [ ] 抽取最小只读检查器，保留设计第 3 节全部绑定；使用现有 API 元数据字段，不建立新发布状态格式或通用恢复引擎。
- [ ] 恢复正常 publisher：稳定 tag 无默认、显式说明提交、精确 run/产物校验、同 tag 串行、写前复查、已有 release 拒绝；仅在全部通过后执行一次正常创建。
- [ ] 恢复稳定发行 `--latest` 策略；创建后只读核对正式/non-prerelease、tag→S、原说明字节、精确 8 资产及逐一摘要、latest 指向。回读不一致或未知只记录失败待核实，禁止自动修补或覆盖。
- [ ] 核对固定恢复器在 `75ef66aff6c38481f06b233fdefc8958b37f83f4` 的文件摘要、原绑定、prepare run 与正式 Release/资产可追溯；将明确引用记入 README，不改写历史证据。
- [ ] 从现役树移除固定恢复器两文件及调用，保留 Git 历史和远端资产；确认正常入口没有自动回退，也没有替代的固定版本恢复 workflow。
- [ ] 使用本地假 `gh`/受控响应验证工作流调用边界，禁止真实 GitHub 写入；确认正常发布使用已核验说明和资产，失败 artifact 由 `if: always()` 保存。
- [ ] 更新 README：正常发布、旧恢复器退役、未知远端结果停止后另提恢复方案和实际发布授权边界；不把 fixture 通过写成正式发行验收。
- [ ] 重跑定向测试与 `git diff --check`；人工核对工作流参数引用、路径和写权限，测试证据注明无外部发布。
- [ ] 独立 review：审阅正常路径严格绑定、旧恢复器退役的历史可追溯性、拟新增 S/N/O 关系和 API 错误分类；修复后复验全部受影响反例。
- [ ] 形成 C1 独立变更；只有 review 阻断项关闭后记录该批完成，后续合入或实际发布遵从届时有效授权。

**AC：** 不同未来版本能使用正常路径，所有设计反例失败即停；固定恢复器历史可审计但不再处于现役路径。正常稳定发行使用 `--latest` 且只读回读完整一致才成功。无旧默认值、无覆盖/移动 tag、无自动 fallback；失败后的远端现状不会被伪造为成功。

**回滚：** 停止正常入口，回退通用入口变更并保持固定恢复器退役；已观察的远端副作用和日志保留，不能用回滚提交操作线上 release。

## C2：在线发行验证启用现有 diagnostics

**前置：** T2 已形成可信检查与证据表达，C1 已完成 review/复验；不要求先扩展治理合同。

**目标文件：**

- 修改 `tools/release-evidence/run-program-installation.py` 与必要的 README。
- 新增候选 `tools/release-evidence/test_program_installation.py`、`internal/updater/online_test.go`。
- 复用 `internal/updater/online.go`、`internal/cli/cli.go`、`internal/cli/command_contract_test.go`，非必要不改变产品实现。

**输入/输出：** 真实 runner 给既有 upgrade 增加 `--diagnostics`；输出继续使用原 envelope 和原始日志，失败退出码、超时和 unexecuted 事实不变。

- [ ] 先写 Go 反例，用 `UpgradeClient.HTTPClient` 的假 transport 返回 403；断言诊断标识、request 阶段、状态码、来源和原 `NETWORK` 类别，且不会读取真实网络。
- [ ] 写 runner 离线用例：受控假执行器捕获 argv 并输出 403 失败 envelope，断言必须包含 `--diagnostics`、记录退出码 1、日志摘要正确及后续步骤未运行。
- [ ] 加成功/失败对照，确认不改变正常 result、失败重跑不得覆盖前次目录、不产生未经运行的通过项；凭据不写入结构化诊断。
- [ ] 运行新增 Python 定向用例并确认未接开关时失败；运行 `go test -tags yss_nobundle -timeout 30s ./internal/updater ./internal/cli` 核对既有 CLI 开关合同与新增 transport 用例。
- [ ] 只给当前在线 upgrade argv 增加 `--diagnostics`；仅在测试确有需要时抽取可测的 runner 小函数，不扩大到重试/鉴权/通用执行器重构。
- [ ] 执行 `python3 -m unittest discover -s tools/release-evidence -p 'test_program_installation.py'` 及上述 Go 定向测试；确认原始 stdout/stderr 与摘要来自真实假执行器运行而非测试补写结果。
- [ ] 更新说明，注明首次 403 原因仍未知、既有失败不覆盖、新工具摘要必须对应新证据；不要求为本修复立刻重发 v2.7.1。
- [ ] 独立 review 诊断字段、退出/超时语义、错误后未执行状态和证据隔离；修复后复验受影响项，再形成独立变更。

**AC：** 可控 403 提供既有结构化诊断，成功合同不变；失败记录完整且无自动重试。未运行新原生验收前，历史发行记录仍绑定旧 runner。

**回滚：** 回退 argv 接线及配套说明，不删除失败证据、不恢复失真的通过状态。

## T3/T4 交接：平台与真实任务观察

**文件：** 复用模板计划定义的指标/观察记录；本仓只链接对应记录，不新建平行效能数据库。

- [ ] 核对 T3 把文件投影、宿主发现、真实调用、程序安装和项目迁移分开；Windows build-only 不被 Mac 结果覆盖。
- [ ] 核对 T4 记录真实任务的上下文、工具/模板版本、耗时、等待、返工、重复检查及失败/未完成，不仅收集成功样本。
- [ ] review 观察结论和样本局限；只有出现明确重复摩擦才确定 C3/C4 后续设计优先级，未证明的提升维持未证明。

## C3：准备适配与原话范围绑定设计补充

**目标文件：** 先补充本设计/计划相应章节；未来实现候选涉及 `internal/governance/lifecycle/daily_prepare.go`、`daily_agent.go`、`internal/governance/approval/review_requirement.go`，本任务不修改这些产品文件。

**依赖与复用：** T3/T4 观察、模板政策所有权；复用 `lifecycle route`、`lifecycle query`、已有 route 输入准备和 agent view。具体调用参数以现有帮助为准，不制造新命令。

- [ ] 从真实任务记录抽出准备摩擦，分别归入已有输出可解决、宿主接线缺失和必须扩展合同三类，标明来源及发生频次。
- [ ] 比较只改说明/适配与扩展合同的成本；优先使用当前 daily/no-API/Backend standalone 范围，其他场景保持未覆盖。
- [ ] 与模板仓联合设计不可变原话来源、真实用户身份、范围选择和摘要绑定；给明确请求、引用、否定、歧义范围、旧消息失效的正反例。
- [ ] 定义准备结果不能产生批准、执行授权或核验通过；输入漂移保留缺口，不能通过重写原话或改成较弱路线规避复核。
- [ ] 独立 review 设计及反例，修复后复核；决定是否另提模板合同/CLI 实现 PR，并在该新计划中固定 schema、语法、文件和轻量测试。

**AC：** 得到可审阅的设计补充、可比较方案和可验证反例；没有声称通用准备已实现。未批准后续合同时，有限原话语法与既有证据核验不变。

## C4：跨 Slice 只读集合预检设计补充

**目标文件：** 先补充本设计/计划；联合模板现有 `.template-spec/process/subagent-collaboration.md` 与 task 集合核验的所属设计，不在本批写 CLI 产品代码。

**依赖与复用：** T4 已形成实际需求；默认复用现有 provenance，可独立于 C3 设计，只有发现必须由 C3 解决的具体合同缺口时才登记依赖。复用 Ticket 依赖、task3、当前根/基线、读写范围、runtime 和最终 freshness 检查；保持现有同 Slice 验证函数约束。

- [ ] 列出集合要观察的现有字段、来源和未观察信息；先用小型真实任务案例确认是否确需跨 Slice 检查，避免重复 route/query。
- [ ] 设计外层只读组合：每项先走原有验证，集合另查依赖环、缺项/陈旧输入、写写/写读冲突、根别名和共享 checkpoint/集成写者。
- [ ] 给出可隔离正例，以及重叠路径、共享输入被改、新 worktree 无原批准、外部资源未知和检查后漂移反例；定义阻断/不确定的表达。
- [ ] 采用模板同一组“2 个独立任务 + 1 个依赖任务”案例，验证依赖等待与串行集成；补充分支已通过但集成漂移使旧证据失效的反例，要求最终候选覆盖全部 AC 并重验受影响检查。
- [ ] 明确通过仅表示观测时未发现已覆盖冲突，不授予执行、加锁或并发资格；不创建队列、自动续跑或新状态库。
- [ ] 独立 review 设计、复杂度上限和反例，修复后复核；后续实现须先模板合同、再 CLI 固定来源消费，并另列明确入口与测试。

**AC：** 形成边界清楚的后续设计；同 Slice 校验不被放宽，无法观察的输入不被标为隔离成功。未经后续方案批准不进入实现。

## 未来实现批次的最终验证与交付

- [ ] C1/C2 实施后先完成相应 Python/workflow 定向检查；仅新增 Go 测试时运行相关包，涉及 Go 产品行为变更时再按 AGENTS 执行 `go test -timeout 5m ./...`；无 Bundle 时使用 `-tags yss_nobundle` 并如实注明范围。
- [ ] 依据实际差异选择既有检查：Go 改动运行 `gofmt -l`、既有 vet/build；来源/版本/帮助改动运行对应 `node tools/check-no-pinned-digests.mjs`、`go run ./tools/docstatus --check`。C1 的 Python/workflow 变更不强制扩展为全量 Go 构建测试。
- [ ] 需要装配/生成变更时先 `node tools/fetch-bundle.mjs --source-root <固定模板目录>`，再使用既有 `node tools/artifacts.mjs check --scope all --source-root <固定模板目录> --out <仓外目录>`；生成只走官方入口，保留覆盖率门槛。
- [ ] 需要验证构建入口时复用 `node tools/build-candidate.mjs --out <仓外目录>` 与 `node tools/smoke/main.mjs --bin <构建的二进制>`；不把冒烟称为原生发行资格。
- [ ] 整批 review 核对设计覆盖、失败证据、平台声明和跨仓依赖；修复后按影响复验，再依据届时授权处理合入/发行。外部发布失败单列事实，不靠修改记录完成计划。
