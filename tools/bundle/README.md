# 固定模板源码 Bundle Producer

运行 `go run ./tools/bundle --source-root <四模板的根> --lock docs/source-lock.json --out internal/bundle/assets`。

Producer 只读取 `templateCommit` 指定的完整 Git commit 对象，不读取 dirty worktree 内容，也不加载四个旧 CLI 的 JS 模块。分发政策默认位于每个模板的 `.template-source/distribution/bundle-profile.json`，必须在同一固定提交内，并以 `policyHash` 锁定原始政策 bytes。迁移期间可用 `policyPath: builtin:<profile>` 与显式摘要；输出诚实标识 `sourcePolicy.kind: bootstrap`。这只支持本地迁移验证，最终固定源应锁定模板内的政策。

Source lock 使用 `schemaVersion: 2`；四个 `profiles` 的 key 必须为 `spec`、`design`、`backend`、`frontend`。每项包含 `profile`、相对于 `--source-root` 的 `sourcePath`、`templateCommit`、独立 `templateVersion`（默认 `git:<templateCommit>`）、`policyPath`、`policyHash`。`legacy` 记录原 CLI 的版本、提交和原摘要，仅用于历史谱系。`producer` 记录生产工具版本及来源；统一 CLI 的 runtime build commit 由二进制 build provenance 提供，不反嵌根 CLI 的下一提交，也不与根模板的 yss-cli gitlink 形成循环。

政策是声明式入口规则：`manifest` 定义允许根、排除项、渲染入口和 ownership；Spec 的 `common` 与 `stages` 定义资产入口。模块 imports、Schema refs 与 Skill 资源引用通过 Go 计算闭包。允许根下新增已提交文件会进入下一个完整 bundle，无需冻结全文件名清单。仅将 `.codex/.cursor/.pi/skills/<name>` 指向同名 `.agents/skills/<name>` 的安全 Git 投影展开为普通文件；其他源符号链及不可移植路径拒绝。

`bundle.Inspect(profile)` 返回同二进制完整 manifest、provenance、分发和每个文件的摘要、权限、ownership、字节长度，不含私有源码入口。`bundle.Export(ctx,profile,out)` 要求新普通目录，拒绝符号链祖先、已有目标及不安全路径；输出全部原始资产 bytes/mode 和 `.yss-bundle.json`（Inspection）。它不替代 init 的实例变量渲染。

程序 JSON 协议、bundle schema v2、模板版本与 legacy lineage 分开。新的实例入口文案使用 `yss assets/skills ensure --plan --out` 和 `--apply --plan-file`，保留生物人审批和业务资产保护边界。完整旧源 oracle 比对已覆盖文件 bytes/mode/ownership、Spec 初始资产及阶段/Skill 依赖；原生命令文案是明确记录的行为迁移差异。
