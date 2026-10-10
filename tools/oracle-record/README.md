# 预言机裁决录制

`internal/governance` 的差异比对会让固定模板源的 Node 预言机对同一输入给出裁决，再与 Go 的裁决比较。
每条断言冷启动一次 Node 与 Python，慢，而且预言机会随锁定模板漂移。本工具把裁决录成
`internal/governance/testdata/oracle/*.json`，测试默认回放。

```bash
# 录制（需要固定模板的干净检出，并已安装其 Node 工具依赖）
git -C <模板> checkout --detach <docs/source-lock.json 的 spec templateCommit>
pnpm --dir <模板>/.template-source/tooling/node install --frozen-lockfile
go run ./tools/oracle-record --template-root <模板>

# 只回放核验已有 fixture
go run ./tools/oracle-record --check
```

模式由 `YSS_ORACLE_MODE` 选择：

| 值 | 行为 |
|---|---|
| `fixture` | 只读 fixture，不需要 Node。未设置且没有 `YSS_LEGACY_ORACLE_ROOT` 时的默认值 |
| `live` | 每次调用真实预言机，需要 `YSS_LEGACY_ORACLE_ROOT`。设置了该变量且未指定模式时的默认值，与此前行为一致 |
| `record` | live 并写入 fixture，由本工具驱动 |

CI 若全局导出了 `YSS_LEGACY_ORACLE_ROOT`，仍会走 live；想用 fixture 请同时设置 `YSS_ORACLE_MODE=fixture`。

fixture 不会悄悄过期：

- 文件头记录录制时的模板提交，与 `docs/source-lock.json` 的 spec 不一致即失败并提示重录。
- 调用身份包含测试根目录的全部文件摘要，而这些权威资产来自内嵌 Bundle；Bundle 或测试输入一变就找不到对应裁决，同样失败并提示重录。

已登记的脚本见 `oracle_fixture_test.go` 的 `oracleFixtureScripts`；未登记的脚本（如 `verify-user-decision`）保持
“只在设置预言机根时才调用”。新增脚本时在该表登记，并在 `tools/oracle-record` 的 `fixtureScripts` 里同步。
