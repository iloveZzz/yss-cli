# yss git

`yss git` 用系统 git 为 YSS 工程提供仓库状态、基线、环境、策略与提交审计。查询子命令只读；写入子命令只在保存计划后执行，且从不访问远端。

## 子命令

| 子命令 | 作用 |
| --- | --- |
| `status [--task <Ticket>]` | 分支、上游差异、改动路径、进行中的合并/变基；可报告分支名是否包含 Ticket |
| `base [--branch <分支>]` | `merge-base HEAD <默认分支>` 的完整 SHA，供 `yss lifecycle route --base` 使用 |
| `submodules` | 子模块状态及其本地入口（`AGENTS.md`/`CLAUDE.md`） |
| `doctor` | Git 版本（≥ 2.31）、提交身份、远端、浅克隆、换行/钩子/LFS 配置、策略文件 |
| `guard` | 事务前检查；有未提交路径、进行中的 Git 操作或未完成 yss 事务时以 `CONFLICT` 失败 |
| `policy` | 生效策略及来源 |
| `audit --since <引用> [--until <引用>]` | 按策略审计：Ticket trailer、自审、签名（可选）、保护分支直接提交 |
| `changelog --since <引用> [--until <引用>]` | 按 Ticket 汇总提交 |

| `verify-message --file <文件>` | 校验提交信息末段的 Ticket trailer（供 commit-msg 钩子调用） |

`--root` 必须是 Git 仓库顶层。目录含 `yss-project.yaml` 时先核验项目身份；普通 Git 仓库也可使用。

## 写入子命令（plan / apply）

| 子命令 | 作用 |
| --- | --- |
| `branch start --task <Ticket> --slug <短名> [--type <类型>] [--from <引用>]` | 创建并切换到 `<类型>/<Ticket>-<短名>`，起点默认策略默认分支；输出 `base` 供 `lifecycle route --base` |
| `commit --task <Ticket> --message <主题> --paths <路径,…>` | 只提交列出的路径，追加 `Ticket:` trailer；范围外改动保持原状 |
| `hooks install` | 写入 `.githooks/commit-msg` 并设置本地 `core.hooksPath` |
| `policy set --key <字段> --value <值>` | 修改策略文件的一个字段 |
| `release tag --tag-version <版本> [--evidence-file <路径>]` | 在默认分支的干净 HEAD 上创建本地带注释标签 `v<版本>` |

```sh
yss git commit --root ./repo --task FEAT-142 --message "支持部分退款" --paths src/refund --plan --out /tmp/commit-plan.json
yss git commit --root ./repo --apply --plan-file /tmp/commit-plan.json
```

- 计划绑定 HEAD、状态摘要和涉及文件的内容摘要。`--apply` 从计划里的参数重新推导计划并比较摘要；任何变化返回 `BASELINE`，不执行。篡改过的计划返回 `PLAN`。
- 计划文件必须保存在仓库之外（`--out` 在仓库内会被拒绝），否则保存它会改变它所绑定的状态。
- `commit` 拒绝：受保护分支、分离 HEAD、进行中的合并/变基、未解决冲突、缺少 `user.name`/`user.email`。路径必须是规范的仓库相对路径，不能以 `-`、`:` 开头，不含 `..`、`.git`、逗号；git 以 `--literal-pathspecs` 运行。Git 钩子失败时暂存区可能已更新，错误信息会提示。
- `hooks install` 把原 `core.hooksPath` 或 `.git/hooks/commit-msg` 作为链上第一环；已有的非 yss `.githooks/commit-msg` 不覆盖；找不到 `yss` 时钩子跳过检查（不阻断没有安装 CLI 的协作者）。`.githooks/` 随仓库提交，`core.hooksPath` 是本地配置，每个克隆各自安装。
- `policy set` 按固定格式整文件重写，原有注释不保留；`--value` 对字符串可直接写，列表用 JSON 数组，布尔写 `true`/`false`。
- `release tag` 要求在 `default_branch` 上且工作区干净；`--evidence-file` 须已纳入版本控制，其 SHA-256 写入标签信息。只创建本地标签。
- `hooks install` 与 `policy set` 经 yss 文件事务写入，事务状态在 `.yss/transactions`；该目录未被忽略时会让工作区显示为未提交。
- 写入执行器只放行固定形状的 `switch -c`、`add -A --`、`commit --only -F -`、`tag`、`config --local core.hooksPath`，其余写入一律拒绝；继承的 `GIT_DIR`/`GIT_WORK_TREE`/`GIT_INDEX_FILE` 等会被清除，用户自己的提交身份、签名和钩子配置保持生效。`require_signed: true` 时提交使用 `-S`、标签使用 `-s`。

## 查询的只读边界

- 执行器只放行白名单中的查询子命令；`config` 只读、`remote` 只列出、`submodule` 只 `status`。
- 剥离继承的 `GIT_*` 与 `PAGER` 环境变量，固定 `LC_ALL=C`、`GIT_TERMINAL_PROMPT=0`、`GIT_OPTIONAL_LOCKS=0`，禁用 fsmonitor、外部 diff 与 textconv。
- 用户提供的引用先经 `rev-parse --verify --end-of-options` 解析为完整 SHA；以 `-` 开头的引用直接拒绝。
- 单次调用 2 分钟超时、输出上限 64MiB。
- 不写工作区、索引、引用或配置，不访问远端。`require_signed: true` 时 `audit` 会按用户 Git 配置调用 gpg 读取签名状态。
- 审计与 guard 只是报告：`audit` 有发现时 `status=attention`，退出码仍为 0；结果不代表可合并、可发布或阶段批准。

## 策略文件

可选，位于 `.template-spec/process/git-policy.yaml`；缺失时使用默认值。未知字段、非法分支名、非法正则均以 `INPUT` 拒绝，不回退默认值。

```yaml
schema_version: 1
default_branch: main
protected_branches: [main]
branch_pattern: "^(feat|fix|chore|docs|refactor|test)/[A-Za-z0-9._-]+$"
ticket_trailer: Ticket
ticket_pattern: "^[A-Z][A-Z0-9]*-[0-9]+$"
review_trailer: Reviewed-by
require_signed: false
```

Trailer 按 Git 规则只从提交信息最后一段读取。

## 后续阶段（未实现）

- 外部副作用：`push`（含标签），需要保存计划和 `--confirm-remote <名称>`。
- 模板仓同步提供 `git-policy.yaml` 默认值、schema 与 Skill 消费者。
