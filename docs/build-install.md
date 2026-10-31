# 构建、打包、安装与手动发布

以下操作由 Agent 按本次授权直接执行，不依赖包装脚本、测试套件、资格回执或统一门禁。构建与安装可在授权范围内完成；创建 Release、上传资产与推送标签须有对应发布授权。构建成功只说明得到二进制，未运行的平台不宣称运行验证通过。

## 从真实模板工作树构建

准备 `go.mod` 要求的 Go、Git 与模板仓目录。先查看模板实际状态，生成 Bundle 后构建；输出放在源码仓之外：

```sh
git -C /path/to/yss-spec-project-template rev-parse HEAD
git -C /path/to/yss-spec-project-template status --short
go run -tags yss_nobundle ./tools/bundle --source-root /path/to/yss-spec-project-template --out internal/bundle/assets
CGO_ENABLED=0 go build -trimpath -o /outside/yss ./cmd/yss
/outside/yss version
/outside/yss help
```

生成器直接读取模板工作树，包含清单允许的已跟踪文件和未忽略的新文件；使用实际 HEAD、工作树状态及内容摘要记录来源。修改过的工作树记录为 `working-tree`，不能写成已提交来源。`docs/template-release.json` 保持既有标签值；本地生成不要求编造新正式标签。Bundle 归档不入库，不能把使用 `yss_nobundle` 构建的空资产程序当作发行包。

如需其他平台，显式设置 `GOOS`、`GOARCH`，Windows 输出名为 `yss.exe`。例如：

```sh
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -o /outside/windows-amd64/yss.exe ./cmd/yss
```

这条命令是交叉编译，不代表已在 Windows 运行。实际可用平台以构建与实际使用结果为准，不生成额外资格标记。

## 手工准备 schema 2 程序包

归档命名固定为 `yss_VERSION_GOOS_GOARCH.tar.gz`，Windows 使用 `.zip`。包根至少包含当前平台的 `yss`（Windows 为 `yss.exe`）和 `release-manifest.json`；可附带文档，但每个载荷都必须登记在 `files`。归档内不放 `installation.json`，它由安装器生成。

manifest 字段如下；摘要均为实际字节的 SHA-256：

| 字段 | 内容 |
| --- | --- |
| `schemaVersion` | 数字 `2` |
| `cliVersion` | 与该二进制 `version` 输出一致的 SemVer |
| `platform` | `GOOS/GOARCH`，例如 `darwin/arm64` |
| `binarySha256` | 包内程序摘要 |
| `cliCommit` | 可选，实际构建 CLI 的 Git HEAD |
| `sourceState` | 可选，实际 `committed` 或 `working-tree` 状态 |
| `source`、`bundles` | 可选来源信息；只写实际掌握的数据，不作为发行资格 |
| `files` | 除 manifest 自身以外，所有载荷路径到描述符的映射 |

每个描述符为 `{"type":"file","digest":"<SHA-256>","mode":493}`。程序必须 `mode: 493`（八进制 0755）；文档通常 `mode: 420`（0644）。manifest 自身不放入 `files`。不包含 stableReady、nativeReceipt、releaseGate、qualification 等退休字段；不要求来源锁或来源文档。

下面是 macOS ARM64 的最小包示例。需有 `jq`、`shasum`、`tar`；它们只用于手工打包，发行 CLI 不依赖它们。`version` 必须按刚构建程序的实际输出填写；`source_state` 按 `git status --short` 的实际结果填写，不能把有修改的源码标为 committed。`/outside/package-darwin-arm64` 须不存在：

```sh
version=3.0.2
target_os=darwin
target_arch=arm64
cli_commit=$(git rev-parse HEAD)
source_state=committed
if test -n "$(git status --porcelain)"; then source_state=working-tree; fi
git status --short
mkdir /outside/package-darwin-arm64
cp /outside/yss /outside/package-darwin-arm64/yss
chmod 755 /outside/package-darwin-arm64/yss
binary_sha=$(shasum -a 256 /outside/package-darwin-arm64/yss | cut -d ' ' -f 1)
jq -n --arg version "$version" --arg platform "$target_os/$target_arch" \
  --arg digest "$binary_sha" --arg commit "$cli_commit" --arg state "$source_state" \
  '{schemaVersion:2,cliVersion:$version,platform:$platform,binarySha256:$digest,cliCommit:$commit,sourceState:$state,files:{yss:{type:"file",digest:$digest,mode:493}}}' \
  > /outside/package-darwin-arm64/release-manifest.json
tar -czf "/outside/yss_${version}_${target_os}_${target_arch}.tar.gz" \
  -C /outside/package-darwin-arm64 yss release-manifest.json
```

Linux 的步骤相同，替换平台、原生或交叉编译的二进制与暂存路径。Windows 包使用对应的 `yss.exe`、`platform: windows/amd64`（或实际架构）及 `files["yss.exe"]`。在新建且只含这两个文件的暂存目录内，用 ZIP 保存普通文件，例如在提供 `zip` 的打包机运行：

```sh
cd /outside/package-windows-amd64
zip /outside/yss_3.0.2_windows_amd64.zip yss.exe release-manifest.json
```

Windows 包里的二进制摘要必须来自该 `yss.exe`，不能复用另一平台的值。使用 `zip` 打包前将暂存程序权限设为 0755；避免加密、符号链接、目录项、绝对路径或 `../`。归档及总解压载荷均限制在 256 MiB 内。

## 校验清单与安装

在归档所在目录生成标准 `SHA256SUMS`。多个平台的归档应列入同一文件；不要把清单自身计入。以下通配符应只匹配本次版本的实际归档：

```sh
cd /outside
shasum -a 256 yss_3.0.2_*.tar.gz yss_3.0.2_*.zip > SHA256SUMS
shasum -a 256 -c SHA256SUMS
```

若本次只有 tar.gz，去掉不存在的 ZIP 参数；只有 ZIP 时同理。Linux 也可使用 `sha256sum`。SHA256SUMS 的每行是 64 位小写摘要、空格与归档文件名，不使用 checksums.json。

首次切换到本版，由 Agent 使用刚构建或已可信取得的新 CLI 执行完整包安装。旧 CLI 的在线升级器可能要求旧格式，不能保证识别 schema 2。不要只覆盖旧可执行文件或手改旧安装收据；选择新的工具目录并用新 CLI 生成安装记录：

```sh
/outside/yss upgrade --artifact /outside/yss_3.0.2_darwin_arm64.tar.gz \
  --sha256 <SHA256SUMS中的该归档摘要> --tool-root /absolute/new-yss --dry-run
/outside/yss upgrade --artifact /outside/yss_3.0.2_darwin_arm64.tar.gz \
  --sha256 <SHA256SUMS中的该归档摘要> --tool-root /absolute/new-yss
/absolute/new-yss/yss version
```

确认实际版本后，将该目录加入 PATH 或把已有入口链接到新程序；保留旧目录便于排查。Windows 使用新目录内的 `yss.exe`。安装器校验归档和载荷摘要、目标平台、版本、路径与权限，核验原安装所有权，使用锁和事务阻止并发覆盖；发现用户修改时停止，不提供强制覆盖。

已安装本版后：

```sh
yss upgrade --dry-run
yss upgrade
yss upgrade --to 3.0.2 --tool-root /absolute/yss --dry-run
```

在线来源固定为 `https://gitlab.yss.test:9443/yss/yss-cli` 的正式 GitLab Release。每次先按完整路径解析项目，再核验正数 ID、完整路径与 Web URL，ID 仅采用本次完整身份核验结果，不复用旧实例的 37/38 等映射；URL 中的数值 ID 必须匹配本次已核验 ID。显式设置 `YSS_GITLAB_CA_FILE` 为受信 CA PEM 文件，`YSS_GITLAB_TOKEN` 由安全环境提供；不把 token 放进 URL、命令参数、日志或产物。在线升级需要具备项目详情、Release 与资产读取权限的受限 PAT。代码保留 `YSS_GITLAB_CI_AUTH=1` 且 `CI_SERVER_URL` 精确匹配时的显式 Job Token 模式，但当前 GitLab 不向 Job Token 开放项目详情 API；该模式在项目核验 HTTP 404 时会返回 `NOT_READY` 并停止，不能据此宣称支持在线升级。不自动选择其他凭据，不向日常 CI 注入 PAT。

日常 CI 只用 `CI_JOB_TOKEN` 访问受支持的固定 `/api/v4/job` 核验本作业项目、job ID 与提交 SHA；跨仓 Git 读取使用固定规范 HTTPS 项目路径、显式 CA、限定凭据助手，并禁用私有来源代理和重定向，不请求不受支持的项目详情 API。这个 CI 读取通道不改变在线升级器的项目身份核验合同。

只向固定项目内已核验的 Release 和资产地址发送鉴权；拒绝 HTTP、跨域、其他项目、任意来源覆盖及 GitHub fallback。无来源或 Release（HTTP 404）明确返回未就绪，不自动创建发行或标签。dry-run 读取发行元数据、`SHA256SUMS`，并以 HEAD 核对资产长度，不下载归档、不写目录。离线 dry-run 读取本地包核验。`--to`、包内版本和目标平台须一致；拒绝降级。升级失败保留现场，优先选择新的工具目录重新安装。

## 获得授权后手动发布

当前 GitLab 尚无正式 Release 时，在线安装与升级不可用；本地构建仅为开发构建。发行操作需要单独的真实授权，不从构建或 CI 成功推导发布授权，不自动创建标签，不改写 `docs/template-release.json` 或已有发布标签。

授权发布时，在固定 `yss/yss-cli` 项目核验动态项目 ID、完整路径与 Web URL 后，用 GitLab Release API 上传实际 schema 2 归档与统一 `SHA256SUMS`。资产链接只使用同源、同项目的上传文件路径 `/yss/yss-cli/uploads/<32位十六进制>/<文件名>` 或 Generic Package 路径 `/api/v4/projects/<已核验ID>/packages/generic/yss/v<版本>/<文件名>`；可提供同一版本的 `direct_asset_url`（`/yss/yss-cli/-/releases/v<版本>/downloads/<文件名>`）。资产服务须支持 HEAD 并提供准确 `Content-Length`，否则在线升级会拒绝。所有请求显式验证私有 CA，不关闭 TLS，凭据只通过安全环境与请求头传递。已有同名资产先读取并取得替换授权，不默认覆盖。

从已核验正式 Release 手动取包时，先固定版本、平台、实际资产 URL 与摘要，在仓外新目录保存并校验 `SHA256SUMS`，再使用新 CLI 的离线 `upgrade` 安装。不要执行不明来源或未核验的程序。发行说明写实际支持与未验证范围；交叉编译、CI 运行、原生平台运行和正式发行分别报告。
