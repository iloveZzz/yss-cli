# 发版证据生成脚本

本目录把 2.3.0/2.3.1 实际使用过的发版证据脚本版本化，使下一次发版可在任意干净的 darwin/arm64 机器上重复执行。它们只**生成并核对**证据，交给 `tools/release` 装配器消费：

* 不授予资格，不放宽任何校验，不伪造回执。未执行、取消、失败如实记录；任一步失败立即停止。
* 不打 tag，不创建 GitHub Release，不 push。这些对外动作由人确认后手动执行（见[人工发布](#人工发布)）。
* 只依赖本仓库文件和命令行参数，不读取 `~/.yss-harness` 等旧路径。程序版本取自 `internal/domain/domain.go`，模板版本与提交取自 `docs/source-lock.json`，均无硬编码。
* 当前资格范围为 `local-platform-program-install`：darwin/arm64 在固定来源上实际执行 version、来源锁声明的全部现役 bundle inspect 和离线程序安装五项（plan 只读、apply、rollback、recover、用户文件保护）。Windows x64 只构建与打包，`stableReady=false`，不具备在线升级资格。

SourceLock v2/v3 继续要求 Spec/Design/Backend 三个 Profile；v5 要求 Spec/Backend 两个 Profile 与 Bundle4，集合缺失、额外或混用均拒绝。安装器和装配器按同一固定锁核验，真实安装 case、源码/摘要与平台资格要求不变。旧 CLI 不支持 v5 来源锁时，需下载并核验新包内执行器后引导隔离安装；不声称所有旧版本都可直接自升级。

## 前置条件

* 真实的 darwin/arm64 运行机（不得以交叉编译代替）。
* Go（版本须与 `go.mod` 完全一致，`build-candidate` 会核对）、Node 24 或更高、Python 3、Git。
* **已提交且干净**的源码：`git status --porcelain --untracked-files=all` 为空。发版提交必须已经包含新的 `Version`、`docs/source-lock.json` 以及本目录脚本；运行期间不得再改动仓库。
* 内嵌 Bundle 归档（不入库）：先运行 `node tools/fetch-bundle.mjs`。总控脚本会替你执行这一步；复用已有模板仓时给总控脚本加 `--template-source-root`。
* **证据目录必须在仓库之外，且必须全新**（不存在）。脚本会拒绝已存在的目录和仓库内的目录。

## 一键执行

```bash
EVIDENCE="$HOME/yss-release-evidence/v$(sed -n 's/^const Version = "\(.*\)"$/\1/p' internal/domain/domain.go)"
python3 tools/release-evidence/run-release-evidence.py prepare --evidence-dir "$EVIDENCE"
```

总控脚本按下表顺序执行，每步写 `$EVIDENCE/orchestrator/<步骤>.stdout|stderr`，汇总写 `$EVIDENCE/orchestrator/run-log.json`（每步结束即落盘）。步骤状态只有 `passed`、`failed`、`cancelled`、`not-run`；只有真实退出码为 0 才是 `passed`；失败后其余步骤保持 `not-run`，并列入 `notExecuted`。前置条件不满足时在创建任何目录之前就以非 0 退出。

## 执行顺序与输出位置

下表同时是手动逐步执行的顺序。除总控脚本外，所有命令都在仓库根目录执行，路径用显式参数；zsh 不会对未加引号的变量分词，所以不要把参数塞进一个变量再展开。

| 步骤 | 命令（脚本） | 输出（均在 `$EVIDENCE` 下） |
| --- | --- | --- |
| 1 | `node tools/fetch-bundle.mjs` | 仓库内 `internal/bundle/assets/bundles.json.gz`（被 Git 忽略） |
| 2 构建二进制 | `node tools/build-candidate.mjs --root "$PWD" --out "$EVIDENCE/build-darwin"` | `build-darwin/yss`、`build-receipt.json` |
| 3 原生 receipt | `native-receipt.mjs` | `native-darwin-arm64/`：`native-receipt.json`、version 与现役 Profile inspect 的报告和日志 |
| 4 | `prepare-native-input.py` | `native-input.json`、`input-documents/` |
| 5 候选包 | `go run ./tools/package --native-candidate-manifest ... --qualification-scope local-platform-program-install` | `candidate/`（`stableReady=false`） |
| 6 程序安装 | `run-observed.py` 包装 `run-program-installation.py` | `program-runner.json|log|stdout|stderr`、`program-installation/report.json` 及逐命令日志 |
| 7 | `prepare-gate.py` | `release-gates/`、`release-input.json` |
| 8 稳定包 | `go run ./tools/package --native-manifest ...` | `release-final/`（`stableReady=true`） |
| 9 | `extract-release-manifest.py` | `mac-release-manifest.json`（稳定包内清单的原样副本，并输出其 SHA-256） |
| 10 Windows | `package-windows-artifact.py` | `windows-artifact/`：ZIP、`build-only-checksums.json`、构建日志 |
| 11 | `check-outputs.py`（只读） | 标准输出 JSON；任一项不符即失败 |
| 12 | `collect-upload.py` | `upload/`：两个归档、四份 JSON 证据与 `SHA256SUMS` |

### 手动逐步命令

```bash
EVIDENCE="$HOME/yss-release-evidence/v$(sed -n 's/^const Version = "\(.*\)"$/\1/p' internal/domain/domain.go)"
VERSION="$(sed -n 's/^const Version = "\(.*\)"$/\1/p' internal/domain/domain.go)"
mkdir -p "$(dirname "$EVIDENCE")" && mkdir "$EVIDENCE" "$EVIDENCE/native-darwin-arm64"

node tools/fetch-bundle.mjs
node tools/build-candidate.mjs --root "$PWD" --out "$EVIDENCE/build-darwin"

BINARY_SHA256="$(shasum -a 256 "$EVIDENCE/build-darwin/yss" | cut -d' ' -f1)"
node tools/release-evidence/native-receipt.mjs --binary "$EVIDENCE/build-darwin/yss" --out "$EVIDENCE/native-darwin-arm64" --artifact-prefix native-darwin-arm64 --cli-root "$PWD" --binary-sha256 "$BINARY_SHA256"

python3 tools/release-evidence/prepare-native-input.py --cli-root "$PWD" --evidence-root "$EVIDENCE" --artifact darwin/arm64 build-darwin/yss native-darwin-arm64/native-receipt.json

go run ./tools/package --native-candidate-manifest "$EVIDENCE/native-input.json" --qualification-scope local-platform-program-install --required-platforms darwin/arm64 "$EVIDENCE/candidate"

python3 tools/release-evidence/run-observed.py "$EVIDENCE/program-runner.json" "$PWD" "[\"$PWD\"]" python3 tools/release-evidence/run-program-installation.py --input "$EVIDENCE/native-input.json" --archive "$EVIDENCE/candidate/yss_${VERSION}_darwin_arm64.tar.gz" --out "$EVIDENCE/program-installation" --platform darwin/arm64 --cli-root "$PWD"

python3 tools/release-evidence/prepare-gate.py --input "$EVIDENCE/native-input.json" --cli-root "$PWD" --platform darwin/arm64 --program-report "$EVIDENCE/program-installation/report.json" --program-execution "$EVIDENCE/program-runner.json" --out "$EVIDENCE/release-gates"

go run ./tools/package --native-manifest "$EVIDENCE/release-input.json" --qualification-scope local-platform-program-install --required-platforms darwin/arm64 "$EVIDENCE/release-final"

python3 tools/release-evidence/extract-release-manifest.py --archive "$EVIDENCE/release-final/yss_${VERSION}_darwin_arm64.tar.gz" --out "$EVIDENCE/mac-release-manifest.json"
# 上一条输出的 sha256 即下面的 MANIFEST_SHA256；这里用独立命令再算一遍作为核对
MANIFEST_SHA256="$(shasum -a 256 "$EVIDENCE/mac-release-manifest.json" | cut -d' ' -f1)"
python3 tools/release-evidence/package-windows-artifact.py --cli-root "$PWD" --source-manifest "$EVIDENCE/mac-release-manifest.json" --source-manifest-sha256 "$MANIFEST_SHA256" --out "$EVIDENCE/windows-artifact"

python3 tools/release-evidence/check-outputs.py --evidence-dir "$EVIDENCE" --cli-root "$PWD"
python3 tools/release-evidence/collect-upload.py --evidence-dir "$EVIDENCE" --cli-root "$PWD"
```

手动执行时没有总控脚本的失败即停和记录，须自己保证上一步成功再执行下一步，并保留各命令的输出。`--cli-commit`、`--template-commit`、`--template-version` 三个参数缺省取自仓库 HEAD 与 `docs/source-lock.json`；显式传入时必须与仓库一致，否则脚本拒绝。

## 脚本一览

| 文件 | 作用 |
| --- | --- |
| `run-release-evidence.py` | 总控：`prepare`（步骤 1–12 并确认源码未漂移）与 `verify-public`（发布后在线升级验证） |
| `native-receipt.mjs` | 本机原生 receipt：只实际执行 `version` 与三个 `bundle inspect`，`checks` 为空，不冒充 smoke/recovery/plugin |
| `prepare-native-input.py` | 绑定二进制、receipt 与七个分发文档，生成 `native-input.json`（资格范围 `local-platform-program-install`） |
| `run-observed.py` | 外部观察记录器：记录前后源码输入摘要、真实退出码与信号；不改动源码仓库 |
| `run-program-installation.py` | 对候选包（或带 `--published-sha256` 时对公开包）实际执行安装五项，并逐命令保留日志与摘要 |
| `prepare-gate.py` | 只包装本次实际观察到的 passed 报告，生成 `release-gate.json` 与 `release-input.json` |
| `extract-release-manifest.py` | 从稳定包原样取出 `release-manifest.json` |
| `package-windows-artifact.py` | 只构建 windows/amd64 并打包，输出 `build-only-checksums.json`；不签发 receipt，不产生稳定 checksums |
| `check-outputs.py` | 只读核对：稳定包 `stableReady=true`、资格范围、`pending` 为空、绑定摘要一致；候选包与 Windows 包 `stableReady=false` 且未声称原生运行 |
| `collect-upload.py` | 把已验证产物复制进新的 `upload/` 并生成 `SHA256SUMS` |
| `release_source.py` | 读取程序版本与模板身份的共用辅助模块 |

## 人工发布

以下动作对外可见，**由人确认后手动执行**，脚本不会代劳。先确认 `git rev-parse HEAD` 与证据中的提交一致，且 `check-outputs.py` 通过：

```bash
python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["cliCommit"])' "$EVIDENCE/native-input.json"
git rev-parse HEAD
```

两条输出必须相同。发布说明按实际证据如实撰写（Mac ARM64 实际通过的项目；Windows 仅构建与打包、无在线升级资格；固定来源提交），不得写没有执行过的验证。

```bash
git tag -a "v$VERSION" -m "yss-cli $VERSION"
git push origin "v$VERSION"
gh release create "v$VERSION" "$EVIDENCE"/upload/* --verify-tag --title "yss-cli $VERSION" --notes-file /absolute/path/release-notes.md
```

## 发布后的公开在线升级验证

下载已发布的 darwin/arm64 归档，用**独立获得**的 SHA-256 对公开包执行真实的 `yss upgrade --to <版本>`、安装身份核对、回滚/重复恢复和用户文件保护验证。独立的摘要取自发布前本地生成的 `upload/SHA256SUMS`（也可与 `gh release view` 报告的资产摘要交叉核对），不要从刚下载的文件上现算：

```bash
mkdir "$EVIDENCE/public-download"
gh release download "v$VERSION" --pattern "yss_${VERSION}_darwin_arm64.tar.gz" --dir "$EVIDENCE/public-download"
PUBLISHED_SHA256="$(grep "yss_${VERSION}_darwin_arm64.tar.gz" "$EVIDENCE/upload/SHA256SUMS" | cut -d' ' -f1)"
python3 tools/release-evidence/run-release-evidence.py verify-public --evidence-dir "$EVIDENCE" --public-archive "$EVIDENCE/public-download/yss_${VERSION}_darwin_arm64.tar.gz" --published-sha256 "$PUBLISHED_SHA256"
```

输出为 `$EVIDENCE/public-installation/`、`public-program-runner.json` 和 `orchestrator/verify-public-log.json`。公开包摘要与本地构建不一致、升级失败或任何断言失败时，脚本以非 0 退出并如实记录，不得改写为通过。

## 复核与已知边界

* 证据只对生成时的源码、工具、参数和环境成立；任何输入变化都使受影响证据失效，须在全新证据目录重新执行。
* 本目录脚本在 2.3.x 实际脚本的基础上只做了参数化（版本、模板版本与提交取自仓库）和路径解耦，校验逻辑未放宽。
* 运行机限定 darwin/arm64；其他平台的运行验收不在当前资格范围内，交叉编译不作为平台运行验收。
