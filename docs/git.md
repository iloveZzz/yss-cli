# yss git

`yss git` 用系统 git 为 YSS 工程提供仓库状态、基线、环境、策略与提交审计。当前为第一阶段：全部只读。

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

`--root` 必须是 Git 仓库顶层。目录含 `yss-project.yaml` 时先核验项目身份；普通 Git 仓库也可使用。

## 只读边界

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

- 本地写入：`branch start`、`commit`、`policy set`、`hooks install`、`release tag`，走 `--plan --out` / `--apply --plan-file`；计划绑定 HEAD、status 摘要和路径摘要，任一变化即拒绝。
- 外部副作用：`push`，需要保存计划和 `--confirm-remote <名称>`。
- 模板仓同步提供 `git-policy.yaml` 默认值、schema 与 Skill 消费者。
