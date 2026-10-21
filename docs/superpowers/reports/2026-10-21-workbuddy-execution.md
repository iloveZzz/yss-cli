# WorkBuddy Skills 执行报告

日期：2026-10-21。用户已批准本轮实现并执行。使用 Superpowers 的 executing-plans、using-git-worktrees 与 TDD 流程，顺序实现，最后独立复核。

下文主体保留首次离线交付时的历史状态；后续用户已授权检查并合入本地 main，提交与合入前检查结果见文末补充记录。

**结果：文件与分发实现完成，最终实验构建及本地回归通过；WorkBuddy 桌面支持尚未验收。** 无提交、推送、合并或发布。任务 0、8 的桌面步骤以及正式固定来源刷新仍为未执行。

## 交付位置与范围

| 项目 | 隔离工作区 | 基线 |
|---|---|---|
| CLI | `/workspace/work/workbuddy/cli` | `7e889632799036c7112f794792bb7338cfc522fa` |
| 模板 | `/workspace/work/workbuddy/template` | `cd5f4580e545440974bc3d35baef11660f583aaf` |

两个工作区均使用 `feat/workbuddy-skills` 分支，修改保持未提交。原工作区及用户暂存区未被替换。设计规范见 `../specs/2026-10-21-workbuddy-skills-design.md`，执行状态见 `../plans/2026-10-21-workbuddy-skills.md`。

已实现：

- 运行时 `workbuddy` 精确映射到 `.codebuddy/skills`；`.agents/skills` 继续作为唯一维护来源。
- Spec 声明 Claude、WorkBuddy 投影，Backend 保持完整分发且仅声明 WorkBuddy。WorkBuddy 投影为普通文件，包含完整资源与执行权限；不分发 `.codebuddy` 私有配置。
- 模板生成器保护未跟踪占用、暂存/未暂存修改、删除及链接；受管实例通过 CLI 保存计划与事务更新。
- CLI 的 init、按需安装、`--full`、assets ensure、显式 sync、resolve、冲突检测及恢复使用同一资源闭包和锁合同。Backend 升级刷新实际 runtimes 与 installedSkills。
- 模板消费者与 CLI 内嵌消费者均识别 WorkBuddy。资源原字节及权限必须与 canonical 相符，规范化摘要算法保持不变。
- `skills resolve --agent-runtime workbuddy` 报告文件状态。桌面发现明确返回 `unknown`，不执行 CodeBuddy 二进制来替代桌面观测。
- 安装、查询、升级顺序与限制已写入 `docs/workbuddy-support.md`，命令帮助已重新生成。

## 最终构建来源

实验二进制：`/workspace/work/workbuddy/yss`，版本 `2.6.0-dev.1`，CLI sourceState 为 `working-tree`；二进制 SHA-256 为 `693fc92bc1d3d18e44ead88084d7b655874df119bb112ddded958732b5e9c982`。

最终模板快照通过独立 `GIT_INDEX_FILE` 捕获，没有修改实际暂存区或创建提交：

- sourceTree：`3e77bd7a924a6e52a2aa2b21e7aeec9db9716f19`
- 本地来源锁：`/workspace/work/workbuddy/source-lock.working-tree.json`，schema 5；Spec、Backend 均明确声明 working-tree。
- Bundle SHA-256：`35f74c528590065125f508894a31eeb24f8322c2bf66ac58e7531ae763ccdbe9`
- 压缩归档：7,176,928 字节；JSON：31,463,122 字节。
- 独立保留归档：`/workspace/work/workbuddy/workbuddy-candidate-bundles.json.gz` 及同名 `.sha256` 文件。
- 原始构建说明：`/workspace/work/workbuddy/bundle-build-final.log`。

正式 `docs/source-lock.json` 和已跟踪的固定归档摘要均未更新。正式帮助视图仍绑定正式来源；本轮只根据命令元数据更新 `docs/cli-help.md`。实验包不授予发行资格，也不能直接作为正式固定来源制品提交。

## 验证结果

环境为 Linux/amd64、Go 1.27.1、Node 24.19.0。真实 Go 路径为 `/workspace/.cloud-onboarding/tools/go-1.27.1/bin`；`/usr/bin/go` 不是 Go 编译器。使用独立 GOCACHE、GOMODCACHE，夹具执行使用 umask 022。

| 检查 | 结果 | 仓外原始日志 |
|---|---|---|
| 最终模板相关套件 | 161 通过，0 失败、0 跳过 | `template-final-tests.log` |
| 原生实例漂移套件 | 62 通过，0 失败、0 跳过 | `native-drift-tests.log` |
| Agent hook 回归 | 19 通过，0 失败、0 跳过 | `agent-hook-tests.log` |
| 生命周期查询与技能预检场景 | 通过 | `lifecycle-context-tests.log` |
| 模板根 `sync-skills --check` | 退出 0 | `skill-sync-check-final.log` |
| `go test -tags yss_nobundle -timeout 5m ./...` | 最终全量通过 | `go-pure-final-tests.log` |
| 含最终新 Bundle 的全量 Go 测试 | 最终全量通过，CLI 包 16.098 秒 | `go-bundle-final-tests.log` |
| `go vet -composites=false ./...` | 退出 0 | `go-vet-final.log` |
| `CGO_ENABLED=0 go build ./...` 与最终二进制构建 | 退出 0 | `go-build-all-final.log`、`cli-build-final.log` |
| `gofmt -l .`、两仓 `git diff --check` | 退出 0，无问题输出 | 执行工具结果 |
| 生成帮助及 `helpdocs --reference --check` | 退出 0 | `helpdocs-generate.log`、`helpdocs-check-final.log` |
| 最终二进制 Spec/Backend 主路径冒烟 | SMOKE PASS，含 init、查询、sync、rollback | `cli-smoke-final.log` |
| 最终二进制更新两个 WorkBuddy 实例 | 两端 sync applied，resolve ready | `final-workbuddy-cli-summary.json` |

日志均位于 `/workspace/work/workbuddy/`。原生漂移套件在初版真实二进制形成后执行；最后修复的投影保护、原字节/权限校验另由最终 161 项模板套件和完整 Go 套件覆盖。未将多个存在重叠的套件相加宣称独立用例总数。

真实 CLI 实验还覆盖 Spec 按需 ensure、Backend 完整安装及文件修改保护：修改 WorkBuddy 副本后 resolve 返回 `blocked/skill-drift`，同步保存计划列为 conflict，用户修改内容保留。证据为 `spec-edited-resolve.result.json`、`spec-conflict-plan.result.json` 等实验文件。`--full` 和 assets ensure 闭包由 `TestWorkBuddyCLIInstallationClosures` 使用实际新 Bundle 验证。

完整 Bundle 测试使用以下真实来源绑定，未放宽来源验证：

```sh
go test -ldflags '-X github.com/iloveZzz/yss-cli/internal/domain.BuildCommit=7e889632799036c7112f794792bb7338cfc522fa -X github.com/iloveZzz/yss-cli/internal/domain.BuildSourceState=working-tree' -timeout 5m ./...
```

早期完整 Bundle 测试因 `go test` 二进制缺少 VCS 来源而出现 BASELINE 失败；改为上述真实来源绑定后完整通过。早期模板夹具因环境默认 umask 0077 出现权限失败；限定夹具 umask 后通过。最终汇总曾发现旧 Backend 测试仍断言无投影，已按新的 roots、targets 和单 runtime 自动选择合同更新，随后 161 项全部通过。上述失败不作为通过证据。

## 独立复核与修复

独立审查使用全新上下文只读 reviewer，发现 3 个 Important、0 个 Critical；随后完成一轮修复，每个问题均先复现失败，再验证通过：

1. 已删除投影可能被误认为首次生成而重建：新增 HEAD 与 index 存在性检查，保护暂存及未暂存删除。
2. 未登记的悬空链接可能被通用清理分支提前删除：根及 Backend 的 WorkBuddy 清理改为报告冲突并保留链接。
3. Spec 仅比较规范化摘要，可能接受 CRLF 或权限不同的投影：Bundle 与 resolve 同时核验 canonical 和投影原字节及 mode。

补充实际 Bundle 的 `--full`、assets ensure 闭包覆盖，复用既有共享初始化夹具。修复后完成受影响回归及最终完整套件，没有无限循环复核。

延期的 Minor：确定性 WorkBuddy 副本映射到 canonical 行为检查，当前变更选择约 389 个检查，存在冗余。完整资源身份比较已保护副本一致性；本轮不优化选择器，后续优化须保留 canonical 行为变更的检查覆盖。

## 执行调整与代价

1. 云端技能辅助脚本无法作为本地脚本执行，改用 `progress.md` 与真实命令日志记账。代价：没有自动 task-start/task-done 工具。
2. 两个隔离 worktree 保持未提交。代价：复核消费工作树 diff，尚无提交可引用。
3. `verify-template-fast` 是 Bash wrapper，按 `bash scripts/verify-template-fast --plan` 调用。代价：仅纠正计划命令。
4. 文档写来源锁 v4，当前消费者实际要求 schema 5；本地锁按真实代码合同装配。代价：制品仍为 working-tree 实验包。
5. 夹具使用 umask 022，仅将隔离 worktree 已跟踪权限规范为 Git 0644/0755。代价：不改变产品或原工作区权限。
6. 缺少桌面入口证据，不添加 `CODEBUDDY.md`，文档先要求显式读取 `AGENTS.md` 和 `CONTEXT.md`。代价：首次使用需手动引导。
7. 完整 Bundle 的 Go 测试注入真实 HEAD 与 working-tree 来源信息。代价：本地测试命令须保留来源参数。
8. 确定性 WorkBuddy 副本使用专属输入映射和完整资源身份对比。代价：canonical 行为变更仍需进入原行为检查，选择器暂有冗余。
9. requesting-code-review 云端包当时不可用，按 executing-plans 的要求使用新上下文独立只读全分支 reviewer。代价：没有该打包辅助工具；独立复核实际完成。
10. 部分计划测试名合并为小型表驱动测试、子测试及共享既有 CLI 夹具。代价：用测试文件与行为映射核对覆盖，不能仅按计划函数名检索。

## 未执行与剩余验收

桌面设备离线，没有取得 WorkBuddy 版本绑定的实际读取或执行证据。任务 0、8 的目录发现、自动入口、显式调用、references/脚本、刷新、同名优先级、关闭技能、权限和取消等桌面步骤均未执行。`desktop-observation.json` 如实保存 `status=unknown`、`version=null`、`reason=workbuddy-desktop-catalog-unavailable`；这与文件 resolve 为 ready 可以同时成立。

接下来需在可用的 WorkBuddy 桌面环境按计划任务 0、8 对 Spec、Backend 分别验收；若目录或入口假设不成立，应修订实现再完成对应测试。当前没有已验收的 WorkBuddy 版本/系统组合。旧 CLI 实际兼容、其他系统原生运行和正式发行资格均未知。

没有执行正式来源提交、固定来源刷新、正式 artifacts generate/check、推送或发布。获得后续相应授权并满足来源资格后，仍须遵循仓库既有正式装配流程，不能将本报告或本地构建替代发行证据。

## 后续授权：检查并合入本地 main

用户随后明确要求「检查完成并入主分支」，本轮据此执行本地提交与合并，不更新远端或发布。原工作区及其未跟踪文档保留；本地 main 使用上述两个隔离工作区。

模板远端 main 已推进至 `71922dcf293351c0b2b40caeed4873d99f310139`，包含四个维护提交。已合入功能分支，唯一冲突为检查路由的并行新增，保留双方规则。最终模板候选为 `8ef10742db8ff5f2685adab56e56067bd81f1481`。CLI 功能提交为 `37f02c831c7cdcb03c80707f067ba499685b6b6f`，固定归档摘要修复提交为 `8a65e1046a14e11a4f2c9ee69766d6852f83052a`。

本轮独立审查发现并关闭了一个 Important：当前生产器重建旧固定模板来源时，已跟踪归档摘要不再匹配。通过现有 `artifacts generate --scope all` 重建并用另一新目录执行 `check --scope all` 验证；跟踪摘要更新为 `a7f83f3221c495bdc689d32f057052029bb6c89d1a731874b88e1b1fe1e42078`。正式来源锁仍保持原模板 `38952a72…`，默认固定 Bundle 尚不包含 WorkBuddy 投影；本地 WorkBuddy 候选单独保存，未将实验来源混入固定制品。

合入检查还实际发现并修复了以下问题：

- 新增投影继承 canonical 的既有空白。保留 canonical 原字节，仅对两棵生成副本设置 Git whitespace 属性；完整资源身份检查继续执行。初步空白清理已撤销，最终 canonical 相对上游无改动。
- `node --check` 的目标文件名被误当作执行 Vue 构建工具。预检输入排除 syntax 任务，任务本身与 Node 版本检查仍保留；真实 Vue 执行仍要求工具链。真实子进程回归先失败后通过，相关 51 项通过。
- 为四个已知历史说明/拒绝夹具登记精确投影文件例外，为一个布局拒绝夹具登记精确文件、原文及次数；不豁免目录，私有和未知文件继续扫描。
- 清理已有实际测试覆盖的过期台账，将新增 WorkBuddy 检查后的数量基线上调为 148；没有减少 required checks、Gate 或 CI 守卫。
- Backend 的退役技能物理目录检查扩展到声明投影根。新增拒绝用例先失败后通过，相关八项回归及工作布局检查通过。

上述增量均由独立只读 reviewer 复核，无未关闭的 Critical/Important。新增延期 Minor：非默认 umask 下，完整权限比较与 Git 0644/0755 基线可能安全拒绝未编辑副本更新；无数据丢失风险，后续单独处理。此前的检查选择冗余 Minor 继续延期。

本轮最终证据均保存于 `/workspace/work/workbuddy/`：

| 检查 | 实际结果 | 证据 |
|---|---|---|
| 模板 `ci-gate --mode pr --base 71922dcf…` | 外层全部步骤通过；fast 398 个任务通过；8 个 slow 按现行策略延后 | `premerge-template-gate-pass/report.json`、`verification/report.json` |
| 当前固定来源全量 Go 测试 | 通过 | `premerge-go-fixed-tests.log` |
| 最终已提交 WorkBuddy 模板候选全量 Go 测试 | 通过，使用仓外归档 overlay，正式来源锁不变 | `merged-workbuddy-go-tests.log` |
| 固定来源二进制完整黑盒冒烟 | SMOKE PASS，含 Profile 和 Git 主路径 | `premerge-cli-smoke.log` |
| 文档事实、禁止钉死摘要与格式检查 | 通过 | 执行工具结果及既有日志 |
| 本地 WorkBuddy 候选实际 sync/resolve | Spec、Backend 均 applied/ready | `merged-local-workbuddy-summary.json` |

模板合入检查使用仓库固定的 CLI 提交 `cc408fd0…` 和匹配的平台摘要，未使用二进制 override。仅执行当前普通合入合同，没有自动升级为 release 验证；fast 的通过不代表桌面或发行验收。

最新本地 WorkBuddy 候选来源锁为 `source-lock.merged-local.json`，生产器绑定 CLI `8a65e104…`，模板绑定 `8ef10742…`；构建时两者均为已提交来源。候选 Bundle SHA-256 为 `5bda5c30a37b0b06edf80e398d151bc499645845fbbb4a81ea6bc42b8a83f430`，二进制为 `yss-workbuddy-local`。这些提交与制品仍仅在本地；桌面验收、正式固定模板来源切换和发布继续未完成。
