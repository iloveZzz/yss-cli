# 固定模板源码 Bundle Producer

日常使用 `node tools/fetch-bundle.mjs`（取得源码、生成、校验摘要）。直接运行时：`go run -tags yss_nobundle ./tools/bundle --source-root <固定模板仓根> --lock docs/source-lock.json --out internal/bundle/assets`（`yss_nobundle` 让工具在归档不存在时也能编译）。归档不入库，仅入库其摘要 `bundles.json.gz.sha256`。

生成单个 `bundles.json.gz`，私有存储格式 v1 将 Spec/Backend 的 `files`、`initial` 与 `NativeTransforms.Source` 内容汇入共享对象池；每个路径继续保存摘要、权限、ownership 和来源信息。构建工具的摘要输出单独使用 schema v4，`profiles` 保留各逻辑 Bundle 身份，`storage` 记录格式版本、文件名、SHA-256、压缩字节数、JSON 字节数和唯一对象数。公开导出仍为完整 Bundle v4，不接受私有归档作为 `--base-bundle` 输入。详见 [内部存储合同](../../docs/bundle-storage.md)。

Producer 只读取 `templateCommit` 指定的完整 Git commit 对象，不读取 dirty worktree 内容，也不加载四个旧 CLI 的 JS 模块。分发政策默认位于每个模板的 `.template-source/distribution/bundle-profile.json`，必须在同一固定提交内，并以 `policyHash` 锁定原始政策 bytes。迁移期间可用 `policyPath: builtin:<profile>` 与显式摘要；输出诚实标识 `sourcePolicy.kind: bootstrap`。这只支持本地迁移验证，最终固定源应锁定模板内的政策。

当前 Source lock 使用 `schemaVersion: 5`；`profiles` 仅包含 `spec`、`backend`。每项包含 `profile`、相对于 `--source-root` 的 `sourcePath`、`templateCommit`、独立 `templateVersion`（默认 `git:<templateCommit>`）、`policyPath`、`policyHash`。`legacy` 记录原 CLI 的版本、提交和原摘要，仅用于历史谱系。`producer` 记录生产工具版本及来源；统一 CLI 的 runtime build commit 由二进制 build provenance 提供，不反嵌根 CLI 的下一提交，也不与根模板的 yss-cli gitlink 形成循环。

政策是声明式入口规则：`manifest` 定义允许根、排除项、渲染入口和 ownership；Spec 的 `common` 与 `stages` 定义资产入口。模块 imports、Schema refs 与 Skill 资源引用通过 Go 计算闭包。允许根下新增已提交文件会进入下一个完整 bundle，无需冻结全文件名清单。仅将 `.codex/.cursor/.pi/skills/<name>` 指向同名 `.agents/skills/<name>` 的安全 Git 投影展开为普通文件；其他源符号链及不可移植路径拒绝。

`bundle.Inspect(profile)` 返回同二进制完整 manifest、provenance、分发和每个文件的摘要、权限、ownership、字节长度，不含私有源码入口。`bundle.Export(ctx,profile,out)` 要求新普通目录，拒绝符号链祖先、已有目标及不安全路径；输出资产 bytes/mode、`.yss-bundle.json`（Inspection）和 `.yss-bundle.snapshot.json`（完整 JSON Bundle，包含 full/initial 变体与来源材料）。它不替代 init 的实例变量渲染。`attach`、`sync`、`migrate --plan` 的 `--base-bundle` 可显式读取该导出目录或完整 JSON 文件，最大 128MiB；默认不联网，不接受内部 gzip 资产作为公开输入。

程序 JSON envelope 与运行协议继续使用 v1；当前 Bundle/metadata 使用 v4，项目保存计划 v3，根 identity2、Profile3、governance2，来源锁 v5 仅包含 Spec/Backend。旧统一 metadata3 / identity1 / policy1 通过显式 migrate 保存原政策和记录；旧 npm 家族、Design、Frontend 或未知代际不猜测迁移。保存计划跨执行器或代际重新生成，保留准确诊断。当前 Bundle4 的 Skill 与升级协议说明来自固定模板，producer 不再用旧说明覆盖；metadata 消费脚本仍作必要兼容转换。历史 Bundle1/2/3 的原说明转换与原字节记录保持，不把历史批准变成新资格。

## 内核 + 覆盖层来源

来源锁里的 Profile 条目可以带 `overlayPaths`（有序列表，例如 `["profiles/_specialist", "profiles/backend"]`）。这时该 Profile 不再读取整个子仓，而是从同一仓库、同一提交读取：

- 内核目录：`.agents/skills`、`scripts`、`.template-spec/process`、`.template-spec/templates`；
- 覆盖层目录：`overlayPaths` 里每个目录下的全部内容，按去掉前缀后的路径依次参与组合。

同一路径出现多次时，后面的覆盖层覆盖前面的，覆盖层覆盖内核。Profile 的分发清单（`policyPath` 指向的 `bundle-profile.json`，通常放在覆盖层里）同时约束两边：内核里不属于该 Profile 的文件，在清单的 `excludePaths` 里列出即可，被排除的路径不会被读取或校验。覆盖层路径不能落在内核目录里或包含内核目录、不能重复，也不能与 `skillsSource` 同时使用。没有 `overlayPaths` 的条目仍按原来的整仓来源构建。

覆盖层目录里的 `.gitignore` 只管该目录之下的路径，会让覆盖层文件在提交时被忽略；覆盖层文件需要被提交时，不要用它忽略覆盖层自己的内容。
