# yss quickstart (English)

`yss` is the offline CLI for YSS spec-driven projects. It creates and upgrades governed project instances from an embedded template bundle, and checks lifecycle routes, contracts and evidence. Most user-facing messages are in Simplified Chinese; JSON output (`--json`) is stable and language-neutral.

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

Profiles: `spec` (integrated control repository), `design` (product and business design), `backend` (backend delivery). Use `yss attach` for an existing repository and `yss sync` to upgrade an instance to the bundle shipped with your CLI. Every write supports `--plan --out <file>` first, then `--apply --plan-file <file>`.

## 3. Your first task: the daily path

Write one Markdown task record (goal, acceptance, implementation repository, rollback) in the project, then ask for a route:

```sh
yss lifecycle route --root ./demo --task .work/demo-1/task.md \
  --implementation-root /path/to/impl-repo --base HEAD --view agent --json
```

- `daily`: implement, test and get an independent review, recording everything in the same task record.
- `governed`: the change touches migration, cross-repository, release or similar risk; follow the full lifecycle.
- `needs-info`: facts are missing; investigate, update the record and route again.

`--base` accepts a full commit SHA or a ref such as `HEAD`; the result echoes `base_ref` and the resolved `base_sha`. The record's `baseline_sha` must still be the full SHA.

When the work is done:

```sh
yss lifecycle verify-daily --root ./demo --task .work/demo-1/task.md \
  --implementation-root /path/to/impl-repo --base <full SHA> --json
```

## 4. Check governance assets

```sh
yss check --changed --root ./demo                     # changed YAML/JSON assets, one report
yss verify --gate plan-approved --checkpoint .work/demo/checkpoint.yaml --root ./demo
```

`check` recognises approval records, checkpoints, context reconciliations, OpenAPI draft validation
and scaffold decision records, and runs the matching existing `verify` command for each. Every finding
carries `code`, `file`, `pointer`, `message`, `fix` and a `command` to reproduce it. Assets that need
current context (such as user decisions) are reported as `not-evaluated`, so the result is `incomplete`,
never `passed`. Neither command approves anything or authorises execution.

## 5. Use it from an MCP client

`yss mcp --root <project>` serves read-only tools over stdio (JSON-RPC, one message per line):
`lifecycle_route`, `verify_daily`, `lifecycle_status`, `lifecycle_query`, `context_query`,
`contract_view`, `explain`, `check`, `verify_gate`. Each tool returns the same JSON envelope as the
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
- Template user guide: start at `.template-spec/user-guide/用户手册索引.md` in your project.
