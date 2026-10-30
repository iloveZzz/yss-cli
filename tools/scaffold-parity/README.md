# scaffold-parity：Go 实现与 Node 生成器逐字节对拍

对 `yss scaffold ddd|mvc` 的 Go 实现（`internal/scaffold`）与模板仓固定提交中的 Node 生成器做黑盒对拍。**这是构建后校验，不是单元测试，也不代表发行验收**；它只回答一个问题：同一输入下，两边产物是否逐字节相同。仅用于开发与 CI，不进入发行包；`yss` 运行时不依赖 Node.js 或 Python，Node/Python 在这里只是对拍基准的运行条件。

## 用法

```sh
node tools/fetch-bundle.mjs                       # 内嵌归档不入库，编译前先生成
CGO_ENABLED=0 go build -o /仓库外/路径/yss ./cmd/yss
node tools/scaffold-parity/main.mjs \
  --bin /仓库外/路径/yss \
  --template-root /仓库外/模板仓   # 必须是 docs/source-lock.json 固定提交的干净检出
```

| 选项 | 说明 |
| --- | --- |
| `--bin` | 被测 `yss` 二进制 |
| `--template-root` | 模板仓检出；HEAD 必须等于 `docs/source-lock.json` 的 `profiles.spec.templateCommit`，且已跟踪文件无改动 |
| `--only a,b` | 只运行 id 等于或以其开头的场景，如 `--only formal-,ddd-example` |
| `--list` | 列出场景和期望后退出 |
| `--verbose` | 对双方拒绝的场景打印两侧拒绝原因，便于确认拒绝出自预期原因 |
| `--keep` | 成功时也保留工作目录（失败总是保留） |
| `--work-dir` | 指定新的工作目录（不得已存在） |

工作目录、模板与输出都使用真实路径（macOS 的 `/tmp` 是符号链接，会让 PATH 校验失败）。全部通过退出 0，任何场景有差异退出 1，前置条件不满足退出 2。

## 对拍怎么做

1. 前置检查：模板提交固定且干净；Node 的 ICU 区域为 `en-US`（生成器用 `localeCompare` 排序）；用 `yss bundle export` 导出内嵌 Bundle，确认影响输出的输入（两个技能目录、指纹绑定的库文件、平台目录与证据、下游技能目录）与模板仓逐字节、逐可执行位相同。
2. 每个场景在 `<work>/scenarios/<id>/out` 里依次运行 Node 基准和 Go 实现，两次使用**同一个输出目录路径**（每次运行前重建，运行后改名为 `out-node` / `out-go`），所以合同摘要、`contract_file_ref` 这类含绝对路径的内容无需放行。
3. 递归快照并逐项比较：文件集合、类型、权限位（`mode & 07777`）、内容 SHA-256、符号链接目标，空目录也计入。
4. 失败场景要求双方都拒绝，并且各自留下的目录树（含 staging 残留）完全一致。
5. Go 侧经 CLI 运行（`yss scaffold … --json`，并核对 JSON 信封）；CLI 不暴露的开关（独立模式的参考示例、候选平台目录）经 `driver/` 调用同一个 `internal/scaffold.Run`。Node 侧经 `generate_scaffold.mjs`；候选目录场景经模板仓自带的 `scripts/fixtures/backend-scaffold/generate-candidate.mjs`。

两侧环境相同：`LANG/LC_ALL=en_US.UTF-8`、`TZ=UTC`、`USER=scaffold-parity`、`umask 022`、隔离的 `HOME` 与 Git 配置，`YSS_NATIVE_BINARY` 指向被测二进制（Node 生成器的原生治理校验委托给它）。

## 登记差异（默认零差异）

允许的差异只能写在 `lib.mjs` 的 `REGISTRY`，每项给出路径、原因和改写规则，并要求两侧都真的匹配、值落在对拍窗口内；匹配不到或出现多次即失败。当前只有一项：

| id | 路径 | 原因 |
| --- | --- | --- |
| `generated-at` | `.yss/scaffold-generation.json` | 清单 `generated_at` 是生成时刻（UTC，毫秒），必然不同；其余字节必须相同 |

拒绝场景的错误文案不比较（Node 带 `❌` 前缀，Go 带技能名前缀；正式合同里由 Python `jsonschema` 与 Go 治理核心各自产生的 Schema 文案也不同），只比较“双方都拒绝且无产物差异”，`--verbose` 可人工核对原因。

## 场景矩阵

`--list` 列出全部场景；数量随平台目录自动变化（每个平台 Profile × 架构各一个）。

### 独立模式（`--standalone` 可省略，不带合同参数）

| 组 | 场景 |
| --- | --- |
| 平台 | 目录中每个平台 Profile 的 DDD、MVC（Boot 2.7/JDK8、3.5/JDK17、3.5/JDK21、4.1/JDK17、4.1/JDK21） |
| 命名 | 单字符项目名与单段包名；含数字和连字符的项目名、深层包名 |
| 坐标 | 正式版本、带限定符版本（`1.2.3-RC1`、`9.8.7-beta.2`）、含下划线/连字符的 groupId |
| 输出位置 | 父级目录不存在时自动创建；在普通 Git 仓库内生成 |
| 参考示例 | 独立模式启用 `--with-example`（Boot 3.5/JDK17、2.7/JDK8、4.1/JDK21），经 `driver/` |
| 拒绝 | 目标已存在；目标是 git-submodule 挂载点；未知 Profile；Boot 补丁版本不符；Java 版本与配方不符；版本范围/`x`；`latest`；项目名非 kebab-case；包名含大写；Maven 坐标含非法字符 |

### 正式合同模式（带合同参数）

对拍先用被测 CLI 实际 `yss init` 一个 Backend 工程，核对其与内嵌 Bundle 的来源，再为各场景复制安装资产。模板仓测试工具生成合成合同、架构决定、用户决定及设计前置；对拍使用当前任务、审查和 approval3 合同生成测试证据，并让两侧消费同一份输入。这些夹具只用于机制对拍，不代表任何真实批准。

固定模板提交上，真实目录里唯一 `verified` 的平台条目（Boot 3.5/JDK17 + YSS 3.2.0-SNAPSHOT）的资格证据与生成器源码指纹漂移（`requalify candidate`），所以 Node 的生产路径对任何合同都会拒绝。成功路径因此走“候选目录”：Node 用模板仓的 `generate-candidate` 夹具入口，Go 用 `Options.PlatformCatalog` + `AllowCandidate`（只在 `driver/` 里设置，CLI 不暴露）。

| 组 | 场景 |
| --- | --- |
| 合同版本 | DDD/MVC 合同 v4（Boot 2.7 与 3.5）；v5 启用/禁用参考示例 |
| 设计前置 | 数据决定 required、API 决定 required、两者都 required（含冻结 OpenAPI、校验记录、Draft 评审） |
| MVC 能力 | `external-integration`、`published-client`、`feign-client`、全能力组合；全能力组合再配 Boot 3.5 |
| 真实目录 | 合同绑定真实 verified 条目：期望两侧结论一致（当前都因指纹漂移拒绝；证据重新资格后两侧都应成功且逐字节相同） |
| 拒绝 | 候选合同交给真实目录；`--contract-id/--contract-version/--approval-ref/--persisted-ref/--project-name`、Maven 坐标与合同不一致；正式模式带独立平台参数；合同未批准/非当前版本；项目根不在 `allowed_write_paths`；验证命令被改；旧 schema 版本；架构决定摘要漂移；`target_output_dir` 不符；v5 参考示例摘要漂移；MVC 模块闭包与能力不一致 |

## 结果摘要（固定模板 `981f7399ef6c`）

```
场景 100，失败 0；比较文件 1430，字节 6106484
登记差异使用: generated-at
```

DDD/MVC 的成功场景全部逐字节一致（含 Maven Wrapper `maven-wrapper.jar` 的内容与权限），拒绝场景两侧一致拒绝且无产物。每次变更生成器、模板来源或平台目录后应重新运行；CI 里它是单独的 `scaffold-parity` 工作流（见 `.github/workflows/scaffold-parity.yml`），只在 ubuntu 运行，失败不影响发行包。
