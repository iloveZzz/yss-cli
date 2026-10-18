# YSS CLI Cobra 命令层 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 Cobra 统一 YSS CLI 的命令声明、解析、帮助与基础补全，保留现有业务、安全和机器接口合同。

**Architecture:** 每次调用创建独立命令树及调用状态；Cobra 负责路由和解析，命令族函数继续调用现有业务包，原输出逻辑统一呈现结果。先整体切换解析，再逐族移除旧集中分发；中间批次只用于开发，全部完成后才形成一个对外候选。

**Tech Stack:** Go `1.27.1`；Cobra `v1.10.2`；pflag `v1.0.9`；标准库 `testing`。首期不引入 Testify、Viper、simplecobra、mock 或 suite。

**Spec:** [已审阅通过的设计](../specs/2026-10-18-cobra-command-layer-design.md)。执行者须先读设计和本计划。

## Global Constraints

- 用户已调用 executing-plans 授权执行本计划。唯一实现目标为 `/Users/zhudaoming/yss-cli`；核对基线 `c10acc98f1d0ef9002a341bdc5cc822a4edc11dc`、版本 `2.4.0`。不得误用 `/Users/zhudaoming/Projects/yss-cli`。
- 开始实施时按 worktree 技能检查现有隔离环境；记录 HEAD、索引和未提交文件，保留本设计及计划。使用 `codex/` 分支；不覆盖、reset、clean、stash 或恢复其他工作。
- 复用既有授权；本计划不包含提交、推送、合并、安装到用户环境或发布。每批完成后记录验证与差异，后续获得提交授权时再形成提交。
- 保留 `Run`、`RunWithTerminal`、`Invoke`、`ServeMCP`、`HelpGuide` 签名，保留 outputVersion=`1`、protocolVersion=`1`、顶层 `command` 和原退出码；失败 result、诊断和事务回执不得丢失。
- 每次独立创建 Command、FlagSet、绑定值、重复次数与结果。`options` 及业务 map 只含实际提供的值；保留缺省、空值、显式 `false` 的区别。
- 严格布尔只接受小写 `true/false`；重复参数继续按命令的原规则拒绝或后值覆盖；短参数只保留独立 `-h`、`-V`。新增 `--` 终止选项，不能增加位置参数配额或绕过路径检查。
- 帮助优先于执行参数校验；隐藏补全协议优先于帮助/输出预扫描。帮助、补全及 MCP 原始协议不包业务 envelope。
- 不改变 `project/governance/transaction/gitops/updater/scaffold/bundle` 规则、保存格式、固定来源或授权边界。若接入必须改变这些规则，先报告具体影响并修订设计。
- 测试用标准库、小型内联数据和临时目录，不联网。单测试约 1 秒、单包 30 秒、全量 5 分钟硬上限；不恢复回放、差分预言机、长跑恢复或性能套件。
- 每批只跑命中行为的测试；有 Bundle 的最终全量通过后，不重复跑无 Bundle 全量。生成脚本通过与各 Shell 实际运行分别记录。

## Review Focus

1. `Invoke` 遇到 `--`：强制 JSON 开关必须在终止符之前；后面的 `--help/--json` 仍是位置参数。由 Task 3 覆盖。
2. 值与命令同名、含空格、以短横线开头，或选项跨越多级命令：规范化不能吞值、换序或提升 `--` 后的子命令。由 Task 2 覆盖。
3. 先运行带显式 `false`、重复选项或错误的调用，再执行无参数调用：MCP 连续调用与 `check` 嵌套执行不能共享状态。由 Task 3 覆盖。
4. 父命令既有默认查询又有写入子命令：`git policy` 与 `git policy set` 等不能混用配额、参数范围或保存计划资格。由 Task 4、5 覆盖。
5. `__complete` 尾词为 `--help`、`--json` 或空串：只返回补全候选和协议指令，不执行帮助、业务、JSON 输出或 MCP 服务。由 Task 6 覆盖。

---

## 文件与接口

路径均相对 CLI 仓根。沿用现有命令族文件；不为每个叶子新建文件，也不建立 DSL 或新的命令注册中心。

| 文件 | 职责与归属 |
| --- | --- |
| `go.mod`、`go.sum` | Task 2 精确增加依赖，审查实际传递依赖，不降级其他依赖 |
| `internal/cli/cli.go` | 公共入口、原输出及错误转换；最终删除 `parse`、集中式 `execute` |
| 新 `internal/cli/command_root.go` | 当前调用状态、根装配、公共处理适配、核心命令绑定 |
| 新 `internal/cli/command_flags.go` | 小型 string/bool 参数绑定、出现次数、枚举与实际提供值；不复制领域规则 |
| 新 `internal/cli/command_input.go` | 从同一命令树识别路径，兼容帮助/错误输出/参数位置；不承担第二套解析 |
| `internal/cli/commands.go` | 保留中文错误、建议、宽度计算和必要的按值约束；删除独立命令表、机械白名单及初始化 |
| 新 `internal/cli/project_commands.go`、新 `internal/cli/recovery_commands.go` | 原集中分发中的项目/资源逻辑和恢复/回退校验，完整搬迁 |
| 新 `internal/cli/governance_commands.go` | 治理命令声明与原身份、上下文、语义报告处理；消费既有 ArgumentKeys |
| 新 `internal/cli/program_commands.go`、新 `internal/cli/tracker_commands.go` | 原程序更新、在线升级、Tracker 模式与保护 |
| `internal/cli/git_commands.go`、`git_write.go`、`profile_commands.go`、`scaffold.go`、`bundle.go` | 在原族文件绑定命令；保留已有处理函数和防护 |
| `internal/cli/help.go`、`help_topics.go`、`documentation.go` | 树决定路径/参数；保留中文叙述、教程、案例和 HelpGuide 接口 |
| `internal/cli/mcp.go`、`check.go` | 仍经同一公共入口执行；处理终止符、连续和嵌套调用 |
| 新 `internal/cli/completion.go` | 四种脚本生成及静态候选；无项目查询或执行 |
| 新 `internal/cli/command_contract_test.go`、`command_input_test.go`、`command_invocation_test.go`、`command_writes_test.go`、`completion_test.go` | 轻量兼容与新增行为测试；依赖 Bundle 的案例另放带 `!yss_nobundle` 的 `command_writes_integration_test.go` |
| 新 `docs/command-migration.md`；`docs/compatibility.md`、`docs/cli-help.md` | 清单、异常输入差异、批次证据及兼容说明；cli-help 只由现有工具生成 |

内部交接约定（不向业务包暴露 Cobra）：

```go
type invocation struct {
    o options
    command string
    result any
    profile string
    executed bool // 调用业务处理前置为 true，区分参数失败与业务失败
    rawOutput bool // 帮助、补全、MCP 独占自己的输出
}
type preparedInput struct {
    args []string
    helpArgs []string
    help bool
    completion bool
    fallback options // 仅解析失败时消费；不计入正常解析的出现次数
}
func newRootCommand(inv *invocation, stdout, stderr io.Writer) *cobra.Command
func (inv *invocation) run(cmd *cobra.Command, args []string,
    handler func(context.Context, options) (any, string, error)) error
func writeOutcome(ctx context.Context, stdout, stderr io.Writer,
    terminal func(io.Writer) bool, inv *invocation, err error) int
func projectRoot(o options) (string, error)
```

`inv.run` 从所选路径与 Cobra 位置参数组装完整 `o.args`（不含根名 yss），不改 `o.rawArgs`；收集实际提供值、执行适用输入约束，再保存 handler 的三项结果。`writeOutcome` 从现有入口提取呈现逻辑，不建立新的输出协议。`projectRoot` 只搬迁空值、root/target-dir 优先级及绝对路径处理，不探测项目身份。

族装配统一签名为 `func addXCommands(root *cobra.Command, inv *invocation)`，X 为 `Core/Project/Governance/Git/Program/Tracker/Help`；补全使用 `addCompletionCommand` 同参。现有 `gitCommand(ctx, root, o)` 等签名保留，以局部闭包适配 `inv.run`。新增 `projectCommand/recoveryCommand/governanceCommand/programCommand/trackerCommand` 的签名均为 `(ctx context.Context, o options) (any, string, error)`；`traceCommand(ctx context.Context, root string, o options) (any, error)` 承接 trace 的原逻辑。

## 迁移覆盖清单

当前生成帮助有 **117 个路径、34 个顶层组**，它是核对输入，不是完整执行叶子数。Task 1 必须与实际执行及消费者对齐后逐项登记。

| 构造器 | 必须覆盖的现有顶层组 | 模式核对 |
| --- | --- | --- |
| Core | `version capabilities explain check verify trace mcp` | trace 的 HTML 导出另按本地写验证；mcp 独占 stdio |
| Project | `init attach sync diff doctor skills assets bundle scaffold profile recover rollback` | 区分查询、计划、导出、默认初始化、保存计划 apply、恢复/回退 |
| Governance | `context lifecycle stage reading contract evidence handoff project-ci runtime archive xml` | 按 action/参数区分，不能整族视为只读 |
| Git | `git` | 含 `branch start`、`hooks install`、`release tag`、`policy set`；保留 policy 查询 |
| Program | `update upgrade` | 含 update 默认 plan 及 plan/apply/status/recover/rollback；无项目参数继承 |
| Tracker | `tracker` | 含默认 status、status --fetch、link、sync 的计划和 apply；联网单独识别 |

另覆盖 `help [路径]`、`help tutorial [主题]`、`help examples [路径]`、`help errors [错误码]`、`-h/-V/--help/--version`；新增 `completion bash|zsh|fish|powershell` 与框架隐藏补全入口，不自动暴露为 MCP 工具。

## Task 1：固定消费者与兼容案例

**Files:** Create `docs/command-migration.md`、`internal/cli/command_contract_test.go`。Read `cli.go/commands.go/help_topics.go`、`internal/mcp/tools.go`、`tools/smoke/main.mjs`、文档及模板消费者；不改模板仓。

**Interfaces:** Consumes 现有 `RunWithTerminal(ctx, args, stdout, stderr, terminal) int`。Produces 测试辅助 `captureCommand(t *testing.T, ctx context.Context, args []string, tty bool) (int, string, string)`，在测试内调用 `t.Helper()`，仅封装缓冲区与 TTY；生成清单不成为运行时注册表。

- [ ] **1. 核对环境与所有消费者。** 记录 HEAD/status、Go/Node/Git 版本、Bundle 是否就绪；确认 Git 2.44+。逐项登记路径、参数作用域、位置配额、父命令行为、重复规则、副作用、消费者来源和验收测试。模板只读检索当前脚本/技能中的 argv；不重建图索引，不扫描无关历史。无法枚举外部用户脚本的限制如实登记。
- [ ] **2. 写已有合同表驱动测试。** `TestCLIContractFlags` 覆盖：`--json version` 与 `version --json` 成功；`version --json=false --json=true` 后值生效；`--json=1/TRUE`、未知短参数、缺值、升级带 root 返回 `ARGUMENT/2`。semantic、scaffold、upgrade 的重复参数都返回 `ARGUMENT/2`。用 `t.TempDir()` 作不存在根，确保这些负例在项目读取或网络之前失败。
- [ ] **3. 固定帮助及输出。** `TestCLIContractHelp` 用 `lifecycle route --root <不存在根> --task --help --apply`、`scaffold ddd --help --force` 断言 `code == 0`、中文文本、`stderr == ""`、根仍不存在；未知帮助路径 `code == 2`。`TestCLIContractOutput` 断言 version 的 TTY 中文、管道原结果、JSON 成功/参数失败的 `outputVersion/protocolVersion == 1`、`command == "version"`、status/code/result 类型、stderr 为空；`--human=false --json` 合法。`version --bogus --json --diagnostics` 即使在解析失败时也返回 JSON 和诊断；不带 diagnostics 的同一错误不附加诊断。
- [ ] **4. 在基线运行这些已有行为。** `go test -tags yss_nobundle ./internal/cli -run '^TestCLIContract' -count=1 -timeout 30s`。预期 PASS；这是特征确认，不虚构 RED。若实际与设计预期不同，先登记原行为并解决兼容判断，不能把期望改成新框架默认值。此批不安装 Cobra。

## Task 2：一次切换解析，建立每次独立命令树

**Files:** Modify `go.mod/go.sum`、`cli.go`、`commands.go`、`help.go/help_topics.go/documentation.go`、现有族文件；Create `command_root.go/command_flags.go/command_input.go`、上述新族文件、`command_input_test.go`。

**Interfaces:** Consumes Task 1 清单与辅助。Produces `invocation/newRootCommand/inv.run/writeOutcome/projectRoot`、全部 `addXCommands`；此批未抽离的业务叶子可暂由局部闭包调用旧 `execute`，一条命令只经 Cobra 解析一次，禁止解析失败后重试旧入口。

- [ ] **1. 写新增输入案例并确认 RED。** `TestCLIOptionTerminator`：`version -- --help --json` 返回 `2`、stdout 为空、stderr 含 `ARGUMENT`；`git -- policy` 返回 `ARGUMENT/2`，不能提升为子命令。临时根写 `-example.yaml` 内容 `name: other\n`，`check --root <根> --json -- -example.yaml` 返回 `0`、result.status=`no-assets`。先运行 `go test -tags yss_nobundle ./internal/cli -run '^TestCLIOptionTerminator$' -count=1 -timeout 30s`；现解析器不支持 `--`，预期相关案例 FAIL。
- [ ] **2. 精确接入依赖。** 在隔离实现仓运行 `go get github.com/spf13/cobra@v1.10.2 github.com/spf13/pflag@v1.0.9`。核对新增及传递依赖，无 Testify、无无关升级/降级；只在依赖实际变化需要时 tidy。
- [ ] **3. 实现参数绑定和入口兼容。** `command_flags.go` 提供 `addStringFlag(cmd *cobra.Command, inv *invocation, name, placeholder, description string, choices ...string)`、`addBoolFlag(cmd *cobra.Command, inv *invocation, name, description string)`；小型 pflag.Value 记录 Set 次数并严格检查布尔，不预填业务 map。占位符/枚举作为 flag 元数据供验证、帮助与补全共用。`command_input.go` 的 `prepareInput(root *cobra.Command, rawArgs []string) (preparedInput, error)` 返回规范化 args、帮助路径、帮助/隐藏补全模式和解析失败用呈现选择；这些字段只属于本次调用，正常解析不把预扫描算作参数出现。
- [ ] **4. 装配全部现有声明并接公共入口。** 从旧初始化逐项搬入各族 `addXCommands`，不再双写新旧命令表。公共呈现参数可在根复用，项目参数仅绑定适用命令；按值约束（含 kind、skills list --details）继续执行。版本使用原结果；自定义帮助保留正文，但路径、子命令、参数表及 HelpGuide 从树读取。关闭默认可见 completion（`CompletionOptions.DisableDefaultCmd=true`），由 Task 6 提供约定入口；不增加框架默认短参数。设置 `SilenceErrors/SilenceUsage`；在 FlagErrorFunc、Args 和业务调用边界按阶段映射错误，不匹配英文报错串。上下文交给 ExecuteContext。输出原样提取到 `writeOutcome`；帮助/MCP 保留原通道。旧 `parse` 在此批删除。
- [ ] **5. 补兼容边界测试。** `TestCLIInputPlacement` 覆盖值为 `version`、含空格目录、`--root=-dir`、选项位于 `git branch start` 各级前后；断言原始诊断 command 保持原 argv 顺序。`TestCLIFlagPresence` 断言显式空值被拒、`false` 不当作缺省/互斥真值、严格布尔和组合短参数仍拒绝。`TestCommandTreeCoverage` 用 Task 1 的明确路径清单检查全部入口、默认动作、三级路径及合法帮助，无需执行每条业务命令。
- [ ] **6. 验证切换。** `go test -tags yss_nobundle ./internal/cli -run '^(TestCLIContract|TestCLIOptionTerminator|TestCLIInputPlacement|TestCLIFlagPresence|TestCommandTreeCoverage)' -count=1 -timeout 30s` 预期 PASS。代码核对 `parse` 无调用，树是唯一解析入口；尚未迁移 handler 在清单注明，不能对外发布。

## Task 3：直连只读处理，验证 Invoke、MCP 与嵌套调用

**Files:** Modify `command_root.go`、`project_commands.go`、`governance_commands.go`、`git_commands.go`、`tracker_commands.go`、`cli.go`、`mcp.go`；Create `command_invocation_test.go`；复用 `check*_test.go/mcp_integration_test.go/route_integration_test.go`。

**Interfaces:** Consumes Task 2 调用状态及适配。Produces `projectCommand/governanceCommand/trackerCommand` 与 `traceCommand`（签名见接口节）；保留 `checkCommand(ctx, root, o) (any, error)`、`gateVerifyCommand(ctx, root, o) (any, error)`、`Invoke(ctx, args) (json.RawMessage, int)`、`ServeMCP(ctx, root, in, out) error`。

- [ ] **1. 先写终止符 RED 案例。** `TestInvokeOptionTerminator` 调用 `Invoke(ctx, []string{"version", "--", "--help"})`，断言 `exit == 2`、输出为 JSON envelope、code=`ARGUMENT`、command=`version`，没有文本帮助。`check` 的短横线文件案例也经 Invoke 成功返回 JSON；原追加 JSON 的行为须先暴露失败。
- [ ] **2. 搬迁完整共享前置与只读入口。** 将原项目/治理/Tracker 分支原样提取至族 handler，先把清单中确认只读的叶子直接绑定；共享 handler 的写模式保护一起保留，未验证写叶子仍由短期旧分发转入同一函数，不能复制两套实现。保留 semantic 特殊路径、context template-source 只读例外、身份/上下文校验、argN、报告 Profile、doctor/skills/资源选择规则。核心 version/capabilities/explain/check/verify、trace 无 html、Git 查询、Tracker 非 fetch 查询直达原处理。
- [ ] **3. 修正 Invoke 的强制呈现位置。** 把强制 `--json` 放在首个 `--` 之前、其他真实选项之后；没有终止符时仍追加。不改变 MCP schema、安全值限制、工具白名单或 ServeMCP 固定根，不从树生成工具。
- [ ] **4. 写隔离、取消及输出失败用例。** `TestCLIInvocationIsolation` 顺序执行成功、严格布尔错误、重复拒绝、显式 false 后再 version，断言下一次参数、诊断、结果和呈现均无残留；不宣称任意业务并发安全。`TestCLICancelledContext`：已取消 version 返回 `CANCELLED/1`，semantic route 仍遵守原错误退出与报告。`TestCLIErrorResultAndReceipt` 针对共享 `writeOutcome` 传入真实 `checkFailure` 和 `transaction.Result{}` 失败：保留 ErrorResult；回执缺省补 `fileApplication=not-applied`、`verification=not-run`，JSON 字段及错误通道不变。这是小型呈现单测，不引入恢复套件。
- [ ] **5. 验证公共调用。** 无 Bundle：`go test -tags yss_nobundle ./internal/cli ./internal/mcp -run '^(TestInvokeOptionTerminator|TestCLIInvocationIsolation|TestCLICancelledContext|TestCLIErrorResultAndReceipt|TestCheck|TestProtocolHandshakeAndErrors|TestToolsAreReadOnlyAndStrict|TestArgvConversion|TestToolCallPassesEnvelopeAndExit)' -count=1 -timeout 30s`。有固定 Bundle 后：`YSS_TEST_REQUIRE_GIT=1 go test ./internal/cli -run '^(TestMCPToolMatchesCommandLine|TestCheck|TestRoute|TestStandard)' -count=1 -timeout 30s`。预期 PASS；保留 MCP 与直接调用的原始 JSON 文本相等断言，不改成宽松 JSONEq。嵌套 check 案例确认父级结果不被子调用覆盖。

## Task 4：直连本地写入，保留全部事务与输入保护

**Files:** Modify `project_commands.go/recovery_commands.go/governance_commands.go`、`profile_commands.go/scaffold.go/bundle.go`、`command_root.go/tracker_commands.go/cli.go`；Create `command_writes_test.go`、`command_writes_integration_test.go`。

**Interfaces:** Consumes 已迁移族 handler 与原 `profileCommand/scaffoldCommand/bundleCommand`。Produces `recoveryCommand(ctx context.Context, o options) (any, string, error)`；所有本地写模式直接绑定现有族 handler，不改变事务函数签名。

- [ ] **1. 固定写入负例。** 无 Bundle 的 `TestCLIScaffoldInputGuards` 断言重复、force=true、合同参数与 standalone=true 均 `ARGUMENT/2`，且无输出目录。integration 文件中的 `TestCLILocalPlanGuards` 在有效临时实例上断言 sync/skills ensure/profile prepare 带 apply 但缺保存计划返回 `PLAN_REQUIRED`，实例文件未变；lifecycle target 带 input 但无 plan/apply 返回 `PLAN_REQUIRED`。`TestCLIParentActionScope` 断言清单中父命令的原默认结果或原拒绝（不是统一补 status），并检查 reading render 不给 checkpoint/all 返回 `ARGUMENT/2`、apply-enable 不给计划位置参数返回 `ARGUMENT/2`，不产生阅读视图。
- [ ] **2. 原样搬迁并直连。** 项目/资源处理保留保存计划 command/root/profile 绑定、禁止参数覆盖、重新规划决议、资源闭包、默认 init apply；恢复/回退保留 preparation、事务 kind 白名单及每种 validator。绑定所有本地治理写叶子、Profile、脚手架、Bundle 导出、trace HTML、Tracker 本地 link；不调用旧顶层 execute。
- [ ] **3. 加小型成功与漂移测试。** `TestCLIProjectPlanGuards` 使用固定 Bundle 临时初始化、保存计划、最小 apply 成功案例；修改计划绑定根或受管输入后拒绝，用户已有文件保留。`TestCLIExportNoOverwrite`：bundle export 缺 out 返回 `ARGUMENT/2`，trace HTML 已存在的项目外目标返回 `EXISTS` 且原字节不变；查询模式不生成导出文件。`TestCLILocalWriteModes`：runtime 在临时 home 执行 begin→event→complete（status=passed、exit-code=0），再 inspect，断言记录及状态一致；archive 对一个文本文件目录 pack 到项目外，由测试辅助复制 ZIP 至临时项目内再 verify/unpack，保持原来源范围约束，内容一致且已有解包目标拒绝；Tracker link preview 不改 Ticket、apply 只更新声明链接。全部使用明确临时 home/output，不接触用户存储；已有 Tracker 领域测试不重写。复用 `runYSS/checkProject` 的用例须放 `!yss_nobundle` 文件，普通测试不能引用这些辅助。
- [ ] **4. 验证本地批次。** `go test -tags yss_nobundle ./internal/cli -run '^TestCLIScaffoldInputGuards$' -count=1 -timeout 30s`；其余上述案例放 integration 文件，由 `YSS_TEST_REQUIRE_GIT=1 go test ./internal/cli -run '^(TestCLILocalPlanGuards|TestCLIParentActionScope|TestCLIProjectPlanGuards|TestCLIExportNoOverwrite|TestCLILocalWriteModes|TestProjectCI|TestStage)' -count=1 -timeout 30s` 执行。预期 PASS，记录实际子例数量与未覆盖模式，不增加每条业务规则的重复测试。

## Task 5：直连 Git、远端 Tracker 和程序更新

**Files:** Modify `git_commands.go/git_write.go`、`program_commands.go/tracker_commands.go`、`cli.go`；Extend `command_writes_test.go` 和依赖 Bundle 的 integration 文件。

**Interfaces:** Consumes `gitCommand(ctx context.Context, root string, o options) (any, string, error)`、原 gitWrite，以及 `updater.UpgradeClient.Run/ReadPlan/Apply/Status/Recover/Rollback`。Produces `programCommand(ctx context.Context, o options) (any, string, error)`，程序命令不经过项目身份探测。

- [ ] **1. 固定三级动作和远端保护。** `TestCLIGitWriteGuards` 用小型本地临时 Git 仓，`branch start/hooks install/release tag/policy set/commit/push` 无 plan/apply 返回 `PLAN_REQUIRED`；只写一个最小 policy 计划并 apply，验证 policy 查询仍按原只读接口返回。缺三级 action、错误 action、额外位置参数返回 `ARGUMENT/2`。push 读取内联合法保存计划但缺 confirm-remote 时返回 `CAPABILITY`，不得执行远端操作；用户工作区保持原样。
- [ ] **2. 固定升级与 Tracker 边界。** `TestCLIProgramScope`：upgrade 重复参数和 root、update status 的 artifact/项目参数均 `ARGUMENT/2`；update apply 缺 plan-file 返回 `PLAN_REQUIRED`，显式工具根不符返回 `PLAN`。`TestCLITrackerRemoteGuards`：sync 无 plan/apply 返回 `PLAN_REQUIRED`；只有 confirm-remote 返回 `ARGUMENT/2`；临时实例启用镜像、`t.Setenv("GH_TOKEN", "test-token")` 后，`sync --apply --plan-file <占位路径>` 无 confirm-remote 返回 `CAPABILITY`，在读计划和 HTTP 之前失败。使用临时 API 配置指向本地关闭端口，避免意外使用用户凭据或公网；`status --fetch` 作为联网模式登记。全部负例不访问 GitHub/远端，不把联网成功作为本轮测试前提。
- [ ] **3. 搬迁原高影响分支并直连。** programCommand 保留 online upgrade 的参数/重复检查，offline update 各动作允许项、固定来源、摘要、工具根、保存计划和恢复/回退；保留 gitWrite 的 kind、输入漂移和远端确认；Tracker 仍调用原 tracker.Run。缺失的保护不能用 Cobra 的通用互斥替代，也不新增“全局 root”来方便绑定。
- [ ] **4. 验证本批。** `go test -tags yss_nobundle ./internal/cli -run '^(TestCLIGitWriteGuards|TestCLIProgramScope)' -count=1 -timeout 30s`；依赖实例的 Tracker 例用 `go test ./internal/cli -run '^TestCLITrackerRemoteGuards$' -count=1 -timeout 30s`。预期 PASS。核对旧 execute 不再有仍被叶子调用的分支；不测试真实推送、线上升级、远端同步，不产生外部变更。

## Task 6：补全、文档和旧入口收敛

**Files:** Create `completion.go/completion_test.go`；Modify `command_root.go/command_flags.go/command_input.go`、`help.go/help_topics.go/documentation.go/commands.go/cli.go`、`docs/compatibility.md/docs/command-migration.md`。`tools/helpdocs/main.go` 保留接口，仅必要适配；生成 `docs/cli-help.md`。

**Interfaces:** Consumes 最终命令树和 flag 元数据。Produces `addCompletionCommand(root *cobra.Command, inv *invocation)`；保留 `HelpGuide(reference bool) (string, error)`，供现有生成器使用。普通参数注册时关联静态枚举或 Cobra 的文件/目录指令，默认禁用文件回退。

- [ ] **1. 写补全 RED 测试。** `TestCLICompletionScripts`：四种 `completion <shell>` 均 `code == 0`、stderr 为空、stdout 含对应 Shell 的 Cobra 脚本标记；未知 shell `ARGUMENT/2`。`TestCLICompletionProtocol`：`__complete`、`__completeNoDesc` 对空尾词、`--help`、`--json` 只返回候选和末尾 `:<directive>`，不出现 envelope/帮助标题；静态枚举正确、非路径无文件回退、路径返回对应指令。`TestCLICompletionHasNoEffects` 在含非法项目身份的临时目录请求 `init/upgrade/mcp` 补全仍立即返回、目录未变，验证没有身份探测和业务启动。运行对应测试确认新增补全尚不存在而 FAIL。
- [ ] **2. 实现脚本和候选。** 使用 Cobra 的 Bash V2、Zsh、Fish、PowerShell 生成 API；只写 stdout。隐藏协议在普通入口兼容处理之前分流；从树和已给 kind 的现有规则提供候选，不读项目 ID/Ticket/阶段资产，不联网；文件路径由 Shell 按指令枚举。不安装脚本或修改用户 Shell 配置。
- [ ] **3. 删除所有临时旧路径。** 删除集中式 execute、所有回调它的闭包、独立 commands 表/initializeCommands、机械 validateArguments/optionCandidates 等旧实现；原业务按值规则搬至拥有该约束的位置，中文建议等通用函数继续复用。最终仅存在族命令声明与现有领域约束。`helpTopics` 可保留正文，但不得决定路径存在性；新增同族命令不再同时修改帮助注册、CLI 白名单和顶层分发。
- [ ] **4. 生成并检查帮助。** `node tools/fetch-bundle.mjs` 准备锁定 Bundle；`go run ./tools/helpdocs --reference` 更新 cli-help，`go run ./tools/helpdocs --reference --check` 预期退出 `0`。当前基线未发现嵌入块；若消费者核对发现既有目标，以原 `--embed --out` 更新该目标，不新增第二生成器。compatibility 说明 `--`、四种脚本、重复策略及 shell 安装由用户完成；清单全部标记已迁移并登记已知差异。
- [ ] **5. 验证树与补全。** `go test -tags yss_nobundle ./internal/cli -run '^(TestCLICompletion|TestCommandTreeCoverage|TestCLIContract|TestCLIOptionTerminator|TestInvokeOptionTerminator)' -count=1 -timeout 30s` 预期 PASS。只对本机现有 Shell 执行语法与候选检查；Bash/Zsh 可各运行一次补全，Fish/PowerShell 缺失记未执行，不安装来凑通过。源码核对所有公开入口直接落到族 handler，旧解析/分发符号无剩余定义和调用。

## Task 7：形成统一候选与交接证据

**Files:** Update `docs/command-migration.md` 验证记录；其余只在发现本次回归时修改对应文件。Consume 现有 `.github/workflows/artifacts.yml`、`tools/build-candidate.mjs`、`tools/smoke/main.mjs`、`tools/artifacts.mjs`，不另建 CI/发布流程。

**Interfaces:** Consumes 已完成清单、全部公共接口、既有生成链。Produces 与当前源码输入摘要绑定的构建/检查报告和一个本机候选二进制；不是发布批准、跨平台运行证明或正式验收回执。

- [ ] **1. 固定验证输入。** 确认 34 组、全部旧路径/默认动作/三级动作及新增补全均有归属；记录候选 HEAD、未提交 diff、输入摘要和工具版本。以 `export EVIDENCE_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/yss-cobra-check.XXXXXX")"` 创建本轮仓外目录；后续 candidate/artifacts 子目录须不存在。选定并记录绝对 `TEMPLATE_SOURCE_ROOT`，核验其包含 `docs/source-lock.json` 所有完整提交及 sourcePath，固定来源不得更新为最新主干；如缺失，使用现有固定来源准备流程补齐，不修改用户模板工作树。准备锁定 Bundle：`node tools/fetch-bundle.mjs --source-root "$TEMPLATE_SOURCE_ROOT"`，核对归档与入库 SHA-256 一致。测试用临时 Git 身份隔离，不改用户全局配置；黑盒 smoke 同时设置临时 HOME/.gitconfig 与 GIT_CONFIG_GLOBAL，因 CLI 会清除 GIT_*。
- [ ] **2. 一次执行最终必要检查。** 在固定输入不变的情况下依次运行：`gofmt -l .`（输出为空）、`CGO_ENABLED=0 go build ./...`、`go vet -composites=false ./...`、`node tools/check-no-pinned-digests.mjs`；`CGO_ENABLED=0 YSS_TEST_REQUIRE_GIT=1 go test -timeout 5m -coverpkg=./internal/... -coverprofile="$EVIDENCE_ROOT/cover.out" ./...`。由执行器给整个测试命令设置 300 秒墙钟截止，不能仅以每个包的 Go timeout 代替全量上限；读取 `go tool cover -func`，沿用现有 internal 覆盖率下限 `10%`，不下调。预期均退出 `0`；记录测试 skipped，不能以跳过替代路由测试通过。
- [ ] **3. 构建并冒烟同一候选。** `EVIDENCE_ROOT` 是已有的仓外专用空目录，子路径尚不存在。`CGO_ENABLED=0 node tools/build-candidate.mjs --out "$EVIDENCE_ROOT/candidate"`；从 build-receipt.json 读取实际二进制路径，再运行 `node tools/smoke/main.mjs --bin <该绝对路径>`。预期 built、smoke 退出 `0`；working-tree 候选如实记录，不冒称 committed/released。最终输入若再变化，先判断受影响检查并重做对应证据。
- [ ] **4. 运行完整既有生成链。** `node tools/artifacts.mjs check --scope all --source-root "$TEMPLATE_SOURCE_ROOT" --out "$EVIDENCE_ROOT/artifacts"`；out 必须全新、仓外。预期 receipt.status=`checked`、所有实际子命令成功，不能只看到报告文件就认定通过。不得覆盖历史证据或以部分 derived 检查代替 all。
- [ ] **5. 审查并交接。** 按已选执行方式做最终变更审查；核对无旧入口、无安全规则删减、无 Testify/无关依赖、无遗漏消费者。交付候选路径/摘要、清单、实际命令及结果、Shell/平台未执行项与剩余风险；后续用户决定提交、合并及发行。失败则保留修改与日志并修复受影响项，不自动回退或清理工作。

## 计划审阅与执行方式

本计划推荐 **Native：当前 Agent 按顺序执行 Task 1–7，最后做一次独立上下文的整体复核**。各批反复修改入口、参数及族绑定，顺序执行能减少交接和重复上下文；不应并行写这些共享文件。用户也可选择逐项子代理实施与复核，代价是每项新建实现及审查上下文。用户已选择本会话顺序实施；实际进度和验证以迁移记录及执行 ledger 为准。

自检：设计 Q1–Q10 与第 3–8 节均已落到任务；五项 Review Focus 都有具名测试；Task 1 的已有行为预期基线通过，新增 `--`、Invoke 终止符及补全案例先确认失败。最终完整测试、静态检查、构建、冒烟及生成链各安排一次，批次中不重复整套执行。以上为计划编写时的安排；实际执行命令、结果及限制以迁移记录和仓外证据为准。
