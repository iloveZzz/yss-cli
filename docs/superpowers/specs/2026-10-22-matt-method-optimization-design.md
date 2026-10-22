# Matt 思想增强后续优化设计（CLI）

日期：2026-10-22。状态：**approved-in-conversation**。用户已批准本设计及执行计划，当前按工作包实施、独立 review、修复和复验；实际结果与范围见关联实现 PR。计划批准不表示合并、发布或实例验证完成。

用户追加指令（2026-10-22）：关联模板 T1 的本地与远端公共 slow 验证硬上限由 300 秒调整为 1000 秒，工作流 job 总上限由 20 分钟调整为 30 分钟，作为本轮后续实施约束；原 300 秒预算下的真实失败、取消及未执行记录继续保留，不改判为通过。

CLI 工作基线：`75ef66aff6c38481f06b233fdefc8958b37f83f4`；正式 v2.7.1 源码：`5cc28f9c43788fdc618197f3ad2689336a5e425a`。固定模板仍为 v4.7.1 / `a435d0693b374e58f803b61817c9d8f9402c72e9`。

配套：[本仓候选执行计划](../plans/2026-10-22-matt-method-optimization.md)；[模板方案 PR #112（设计与执行计划）](https://github.com/iloveZzz/yss-spec-project-template/pull/112)。

## 1. 目标与范围

本轮方案把现有增强继续转化为可维护、可观察、可验证的能力：先修复发行入口与诊断接线，再用真实任务观察决定准备体验和跨 Slice 检查的投入。不能把新的流程文案、更多命令或合成用例通过当作用户效率已经提高。

| 批次 | 问题与交付 | 本文件定位 |
| --- | --- | --- |
| C1 | 日常 publisher 被固定 v2.7.1 恢复器替代；恢复通用入口并退役一次性恢复器 | 可审阅的实施方案 |
| C2 | 发行 runner 未传既有 `--diagnostics`；失败日志缺请求阶段和 HTTP 状态上下文 | 可审阅的最小接线方案 |
| C3 | 日常准备范围有限；原话与复核范围绑定尚有使用摩擦 | 后续设计任务，尚不扩展语法 |
| C4 | 现有集合核验限同一 Slice；跨 Slice 无集合级只读预检 | 后续设计任务，尚不创建调度器 |

CLI 复用 route/query、`next_action`、runtime 记录、task3、独立复核证据和最终输入复核；不新增第二套生命周期、批准机制、发布状态库或自动执行循环。模板拥有政策与合同，CLI 消费固定来源。

## 2. 现状与取舍

当前 `publish-release.yml` 调用 `.release-recovery/upload.py`，绑定固定 tag、源码、说明提交、prepare run 和 draft release ID。它正确拒绝已转正式的 v2.7.1 release，但已不适合作为后续版本的通用入口。

直接还原旧文件会恢复历史 `v2.5.3` 默认值，并保留说明仅“包含源码 SHA”等不足；把固定恢复器泛化则扩大紧急操作权限。建议把严格绑定机制用于通用 publisher，核验历史可追溯性后退役固定恢复器。保留一个必拒绝的活跃恢复入口没有当前用途；真正出现新的恢复需求时再独立设计人工入口。

HTTP 403 的诊断能力已存在于 `internal/updater/online.go`；runner 未启用呈现。建议只接已有开关与证据记录，不新增重试、鉴权策略或日志平台。此前失败 run `39648279181` 和后续成功 run `39649025501` 都保留；不能据此推断第一次失败的具体根因。

C3/C4 先做设计的原因是合同与使用场景尚未收敛。尤其自然语言请求当前采用有限语法，这是明确支持边界；不能用关键词匹配、改写用户原话或猜测范围来“修好”它。

## 3. C1：通用发布入口与固定恢复器退役

### 3.1 输入与职责

正常入口保留 `.github/workflows/publish-release.yml` 和显式 `workflow_dispatch`。`tag`、`evidence_run_id` 必填且无历史版本默认值；拟新增必填 `notes_commit`，限定完整 40 位 SHA。该新增输入属于本方案，当前工作流尚未提供。

通用入口恢复后，先核对现有固定恢复器的历史提交、文件摘要、prepare run、draft/release 身份与正式资产仍可追溯，再从现役树删除 `.release-recovery/bindings.json` 和 `upload.py` 及其调用。不修改原绑定、不删除 Git 历史或远端资产；不新增固定恢复 workflow，不清理用户文件。

只把可独立测试的正常发布前检查抽入拟新增 `tools/release-evidence/publish_contract.py`；复用现有元数据和证据文件，不另设发布状态机或证据格式。网络读取和实际写入仍由工作流承担。

### 3.2 精确绑定与写前检查

拟采用以下绑定合同（proposed）：S 为待发布 tag 在同一仓库远端解析出的提交，N 为显式说明提交，O 为本次工作流实际执行提交。检查要求如下：

1. tag 满足稳定版本格式；本地及远端 tag 均解析到 S；源码版本与 tag 一致。工作流 checkout 干净且 HEAD 与本次运行的 `GITHUB_SHA` 一致。
2. S 是 N 的祖先，N 是 O 的祖先；`docs/releases/<tag>.md` 在 N 与 O 的字节相同，内容标明完整 S。发布读取已核验的说明字节，不能临时从浮动分支读取。
3. prepare run 属于本仓 `.github/workflows/release-evidence.yml`，已完成且成功，HEAD 为 S；核验确为 prepare 产物，而非同 HEAD 的其他 workflow 或 verify-public 结果。
4. `yss-release-evidence` artifact 唯一、未过期，从该 run 下载到新的隔离目录；输入、平台、总控步骤、最终 checksums 和上传集合完整互相匹配。
5. 保留精确 8 个上传文件、7 个 payload 摘要、源码/版本/固定模板来源、`pending=[]`、Mac 资格与 Windows build-only 约束。不能只看某个 `stableReady` 布尔值。
6. 同 tag 的任何 release（draft 或正式）已存在时拒绝正常发布；查询权限不足、网络失败和未知结果不等于“不存在”。禁止覆盖资产、删除 release 或移动 tag。
7. 同仓同 tag 使用一致并发组且不取消正在执行的写入。写入前再次观察远端 tag、run/产物身份、release 不存在及本地输入摘要；变化或无法确认即停止。

正常路径只有一次显式创建 release 与资产上传。部分成功、超时或远端结果不明时保留真实响应、命令退出状态和可取得的 release/asset 身份，停止后另行设计绑定此次真实状态的恢复方案；不能自动重试创建或切换历史恢复器。GitHub 写入并非原子事务，本设计不作此保证。

正常稳定发行恢复原 `--latest` 语义。创建后只读回读 release 为正式、非 prerelease，tag 仍解析到 S，说明与已核验原字节一致，恰有预期 8 个资产且各摘要与本地上传字节一致，latest 指向本次 release。远端摘要不可取得、回读不一致或结果未知均记为失败待核实，保留实际远端事实，不再写入、覆盖资产或补设 latest；本方案不保证不同版本并发发布的全局原子性。

历史恢复器仅接受原仓、原 tag、原 S/N/prepare run/数字 draft ID，要求 draft 未发布、说明与已有资产一致；只上传缺失资产，拒绝冲突、额外资产及未知写入结果。这些原约束保留在固定历史提交中，不能为绕过已发布状态而改写。通用 publisher 复用来源、说明、产物和未知结果的严格检查；正常创建不接收旧 draft ID，也不获得补传权限。

### 3.3 C1 验收

- 正向：隔离本地 fixture 模拟一个不同于 v2.7.1 的未来稳定版本，完整同源证据仅产生预期的一次创建意图；不调用 GitHub 写 API。
- 反向：错仓/错 workflow/错 phase/错 HEAD、缺失或过期/重复 artifact、tag 漂移、版本或来源不符、说明漂移、摘要或文件集合不符、已有 release、未知查询结果均在写入前拒绝。
- 历史恢复器与原绑定仍可通过固定提交及摘要审计；现役树无旧恢复器调用或固定 v2.7.1 的恢复入口。可控部分写失败保存观测事实且不触发第二次写入。
- 本地假 API 覆盖创建后仍是 draft/prerelease、tag/说明变化、资产缺失/多余/摘要不符、latest 指向错误和回读失败；均保留失败事实且没有第二次写入。正常用例确认 `--latest` 与完整只读回读。
- 最终实际发布另依当时有效授权和真实 prepare 证据执行；方案与本地 fixture 通过不构成发行资格。

## 4. C2：发行 runner 消费现有诊断

修改 `tools/release-evidence/run-program-installation.py` 的真实在线 `upgrade` 调用，增加已有 `--diagnostics`。继续由现有 `call` 保存 stdout/stderr、摘要、数值退出码、耗时及超时状态，失败即停。模板公共 `ci-gate --mode slow` 按追加指令采用 1000 秒验证上限、30 分钟 job 总上限，准备不计入验证预算；模板普通检查单项 300 秒、发布 prepare 1000 秒、runner 在线命令 960 秒和 Go 全量测试 5 分钟上限各自保持。

复用 `UpgradeClient.HTTPClient` 的可注入 transport，在 Go 测试中返回本地构造的 403 响应，不访问网络。应观察 `RELEASE_HTTP_STATUS`、`phase=request`、`httpStatus=403` 和固定发行来源；保留 `NETWORK` 错误类别与退出码 1，不记录鉴权凭据。

runner 的轻量离线测试用受控假执行器验证实际 argv 含开关、原始失败输出被保留、摘要与文件匹配、后续在线步骤未运行。CLI 输出开关的已有合同测试继续证明不带开关不增加诊断。成功 envelope、安装/回滚语义和发行资格不变。

每次真实验收使用新证据目录；runner 字节变化会改变来源摘要，旧回执不能搬用或改写成新 runner 的证据。保留第一次失败和重跑结果，分别注明源码、工具、输入、环境和运行 ID。

## 5. C3：准备适配与原话范围绑定（后续设计）

既有 `internal/governance/lifecycle/daily_prepare.go` 仅支持无 API 变化的 daily，Backend 要求 standalone；它只刷新已选择引用的摘要，输出只读、未创建批准、证据未评价。既有 route/query/agent view 已能给缺口和下一步，不能另造重复命令。

先依据 T4 的真实任务观察，区分文件定位、引用/摘要准备、原话采集和真正政策缺口。复用现有输出可以解决的摩擦，优先改适配和说明；需扩展合同的部分先由模板仓设计并独立审阅，再固定来源迁入 CLI。

后续设计须确定原始消息的不可变引用/摘要、实际用户身份、范围选择与来源的可核验关系。机器可以给候选绑定和缺口，不能把候选变成批准、补写用户话语或把含糊表述当作独立复核要求。

本方案未确定新 JSON schema、CLI 参数或 MCP 暴露方式，不存在可直接调用的“通用准备”命令。该阶段产物是设计补充和正反例，不是功能完成。旧有限语法仍有效；未覆盖输入保留缺口，不能降级复核要求。

## 6. C4：跨 Slice 只读集合预检（后续设计）

模板现有 `validateTaskPackageSet` 限同一 Slice，必须保持该约束。未来集合检查在各任务现有验证之外组合只读观察，不能修改单 Slice 检查以假装已经覆盖全局。

设计输入复用现有 Ticket 依赖、task3、真实工程根/基线、批准来源、写范围、被观察输入和共享资源；必须讨论跨 Slice 标识、依赖环、缺失/过期输入、写写与写读冲突、同一实际根的别名以及共享 checkpoint/集成写入者。

输出只陈述观测时的冲突、阻断和不确定项，并保存输入绑定。通过表示该次预检未发现已覆盖冲突，不表示获准派发、获得锁或未来执行无冲突；真正执行前仍须最终输入复核、原批准及串行集成规则。

与模板采用同一验证案例：2 个独立任务加 1 个依赖任务。覆盖独立任务可并行观察、依赖任务等待、串行集成，以及分支测试已通过但集成后输入漂移使原证据失效；最终候选仍须满足全部 AC，并重验受影响的单元与集成检查。

不创建 worker 队列、自动调度/续跑、全局锁服务或新状态库。未观察资源不能视为隔离成功；新 worktree 不继承其他工程根的批准。具体入口、规模上限、结果 schema 和持续观测方式由后续设计决定。

## 7. 依赖、平台资格与验证

执行顺序：**T1 / C1 可并行 → T2 → C2 → T3 平台指标 → T4 真实任务观察 → C3 / C4 后续设计**。T/C 为两仓计划批次，不是已有产品命令。C3/C4 通常可独立设计，C4 默认复用现有 provenance；仅发现必须由 C3 解决的具体合同缺口时登记依赖。每批均先设计、实施、独立 review、修复、复验，关闭阻断项后才进入依赖它的下一批。

T1/C1 恢复各自维护入口；T2 先建立可信检查与证据表达；C2 才让新增真实失败带足够诊断；T3/T4 形成平台和任务观察基线。C3/C4 如改变治理合同，应先完成模板侧合同与对应验证，再通过正式来源刷新进入 CLI，不能手改嵌入产物。

| 证据维度 | 必须保留的范围 |
| --- | --- |
| Mac ARM64 | 当前 `local-platform-program-install`；`requiredPlatforms=['darwin/arm64']`；已有安装资格不扩展成用户真实项目全部升级资格 |
| Windows x64 | build-only；`stableReady=false`，原生安装/完整升级未验收；交叉编译和局部 Go 测试不改此结论 |
| WorkBuddy | `.workbuddy/skills` 文件投影与旧受管目录迁移可单独检查；桌面发现、触发与真实调用必须另有宿主证据 |
| 任务效能 | 真实耗时、等待、返工、重复核验、失败与未完成都记录；无可比样本时不宣称改善比例 |

本次方案 PR 只做文档差异、链接、来源和任务覆盖检查，不运行产品命令或发布流程。未来行为改动遵守 AGENTS：轻量离线测试，单例约 1 秒、包约 30 秒、全量 Go 5 分钟硬上限，Git 2.44+；保留 CI 的 30% 覆盖率下限，不把覆盖率数字单独解释为风险等级。

## 8. 风险与回滚

C1 的主要风险是错误绑定仍被当作完整证据，及远端写入期间竞争；以失败即拒绝、写前复查、同 tag 串行和真实失败记录缩小范围。若恢复后发现错误，停止日常入口并回退通用入口变更，保持固定恢复器退役；不能回滚远端 tag/release 或重新启用旧恢复器掩盖问题。

C2 的风险是失败日志增加敏感内容、调用行为意外变化或旧证据被误用；限定既有结构化字段、保持原错误语义、重新生成真实回执。回退开关接线即可恢复旧呈现，但应保留新增失败证据。

C3/C4 的风险是把准备/预检误称为授权或并行保障。未完成模板合同审阅与正反例验证前维持当前能力；若未来实施失败，回退新增适配或集合入口，保留原 route、单 Slice 校验和真实历史记录。

## 9. 固定来源

- [当前发布入口](https://github.com/iloveZzz/yss-cli/blob/75ef66aff6c38481f06b233fdefc8958b37f83f4/.github/workflows/publish-release.yml)；[固定恢复绑定](https://github.com/iloveZzz/yss-cli/blob/75ef66aff6c38481f06b233fdefc8958b37f83f4/.release-recovery/bindings.json)。
- [发行 runner](https://github.com/iloveZzz/yss-cli/blob/5cc28f9c43788fdc618197f3ad2689336a5e425a/tools/release-evidence/run-program-installation.py)；[HTTP 诊断](https://github.com/iloveZzz/yss-cli/blob/5cc28f9c43788fdc618197f3ad2689336a5e425a/internal/updater/online.go)。
- [daily 准备](https://github.com/iloveZzz/yss-cli/blob/5cc28f9c43788fdc618197f3ad2689336a5e425a/internal/governance/lifecycle/daily_prepare.go)；[原话范围核验](https://github.com/iloveZzz/yss-cli/blob/5cc28f9c43788fdc618197f3ad2689336a5e425a/internal/governance/approval/review_requirement.go)。
- [同 Slice 集合与并行边界](https://github.com/iloveZzz/yss-spec-project-template/blob/a435d0693b374e58f803b61817c9d8f9402c72e9/.template-spec/process/subagent-collaboration.md)。
