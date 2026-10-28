# yss quickstart (English)

`yss` is the offline CLI for YSS spec-driven projects. It installs standalone skills by default, supports governed project instances from an embedded template bundle, and checks lifecycle routes, contracts and evidence. Most user-facing messages are in Simplified Chinese; JSON output (`--json`) is stable and language-neutral.

## 1. Install and verify

Download the release archive for your platform from [GitHub Releases](https://github.com/iloveZzz/yss-cli/releases), verify its SHA-256, then:

```sh
yss version --json
yss capabilities --json
```

Already installed? `yss upgrade --check --json` shows the newest stable version.

## 2. Create a project

```sh
yss init --profile spec --root ./demo --project-name Demo
yss doctor --root ./demo --json
```

Profiles: `spec` (requirements, product design and integrated delivery), `backend` (dedicated backend delivery). The default `skills` installation includes the selected skills and their required `.template-source` / `.template-spec` references; those files do not enable governance. Use `yss attach` for an existing repository and `yss sync` to upgrade an instance to the bundle shipped with your CLI. Every write supports `--plan --out <file>` first, then `--apply --plan-file <file>`.

## 3. Your first task: the daily path

Lifecycle routing requires a `governed` installation. Create it in a new directory before following the governance examples below:

```sh
yss init --profile spec --installation governed --root ./demo-governed --project-name Demo
```

Write one Markdown task record (goal, acceptance, implementation repository, rollback) in that project, then ask for a route:

```sh
yss lifecycle route --root ./demo-governed --task .work/demo-1/task.md \
  --implementation-root /path/to/impl-repo --base HEAD --view agent --json
```

- `daily`: implement, test and get an independent review, recording everything in the same task record.
- `governed`: the change touches migration, cross-repository, release or similar risk; follow the full lifecycle.
- `needs-info`: facts are missing; investigate, update the record and route again.

`--base` accepts a full commit SHA or a ref such as `HEAD`; the result echoes `base_ref` and the resolved `base_sha`. The record's `baseline_sha` must still be the full SHA.

When the work is done:

```sh
yss lifecycle verify-daily --root ./demo-governed --task .work/demo-1/task.md \
  --implementation-root /path/to/impl-repo --base <full SHA> --json
```

## 4. Check governance assets

```sh
yss check --changed --root ./demo-governed                     # changed YAML/JSON assets, one report
yss verify --gate plan-approved --checkpoint .work/demo/checkpoint.yaml --root ./demo-governed
```

`check` recognises approval records, checkpoints, context reconciliations, OpenAPI draft validation
and scaffold decision records, and runs the matching existing `verify` command for each. Every finding
carries `code`, `file`, `pointer`, `message`, `fix` and a `command` to reproduce it. Assets that need
current context (such as user decisions) are reported as `not-evaluated`, so the result is `incomplete`,
never `passed`. Neither command approves anything or authorises execution.

## 5. Use it from an MCP client

`yss mcp --root <governed-project>` serves read-only tools over stdio (JSON-RPC, one message per line):
`lifecycle_route`, `verify_daily`, `lifecycle_status`, `lifecycle_query`, `context_query`,
`contract_view`, `explain`, `check`, `trace`, `verify_gate`. Each tool returns the same JSON envelope as the
command line; nothing writes, commits or pushes, and tool arguments cannot change `--root`.

```json
{"mcpServers": {"yss": {"command": "yss", "args": ["mcp", "--root", "."]}}}
```

## 6. Look things up

```sh
yss explain gate.plan-approved        # stable lifecycle IDs and process terms
yss help                              # all commands
yss help errors <CODE>                # what an error code means and how to recover
```

Exit codes: `0` passed, `1` rejected, `2` input or capability problem (cannot decide).

## Further reading

- [Full command reference](cli-help.md) (Chinese)
- [Tests and CI](ci-verification.md) (Chinese)
- [Template user guide](https://github.com/iloveZzz/yss-spec-project-template/blob/main/.template-spec/user-guide/用户手册索引.md). The files installed locally depend on the installation mode and selected resources.

## 独立技能安装（新安装合同）

Bundle5 / metadata5 / plan4 支持一次性的 `skills` 或 `governed` 安装选择。新建默认 skills；旧来源 Bundle3/4 保持 governed，不能凭新参数假定旧来源已经包含普通入口。

```sh
yss init --root /path/to/new-project --profile spec --installation skills --plan --out /outside/project/init-plan.json --json
yss init --root /path/to/new-project --apply --plan-file /outside/project/init-plan.json --json
```

`init`、`attach` 可选择安装方式，`migrate --installation skills|governed` 显式转换已有 metadata4/5 实例；未给选择的 migrate 保留旧治理迁移语义。`sync` 保持现有安装方式，不接受该参数。应用只消费保存计划，不能另加 installation 改写选择。正式 `profile prepare` 继续生成 governed 目标。`--full` 只扩展当前安装方式的技能，不切换是否治理。

普通技能由宿主按 SKILL.md 名称与描述发现，不要求先 route、resolve-ready、tracker setup 或创建 checkpoint。`skills resolve` 仍是原受管就绪验证，不是通用本地技能发现器。正式操作在 skills 工程返回未启用治理；真实工程测试、授权范围、分发完整性与写入冲突检查保留。

迁移计划保留原任务、批准、checkpoint 及用户文件；活动或无法核验的正式绑定拒绝转换。规划后输入变化需要重新规划。具体固定来源以 `yss version --json` 和该版本发行说明为准；升级程序不会自动同步已有项目。
