# 模板存储与体积验证

CLI 1.3.1 在一个可执行文件中保留 Spec、Design、Backend、Frontend 的完整离线模板。存储改造不更换 source lock 固定的模板提交、来源政策或 producer 谱系，不改变公开 Bundle v3、metadata v3、保存计划 v2、历史 source lock v2 和 envelope / protocol v1。

## 私有归档 v1

`internal/bundle/assets/bundles.json.gz` 是唯一嵌入资产，解压后为：

```json
{
  "storageFormatVersion": 1,
  "profiles": {"spec": {}, "design": {}, "backend": {}, "frontend": {}},
  "objects": {"<原始文件字节的 SHA-256>": "<Base64 内容>"}
}
```

`profiles` 保存原 Bundle 的全部结构，只有文件记录的 `data` 置为空字符串。路径、`digest`、`mode`、`ownership`、初始变体和转换来源分别保留；`files`、`initial`、`NativeTransforms.Source` 均通过 `digest` 引用对象。空文件也必须引用合法的空内容摘要。对象相同不要求路径、权限或归属相同；同路径的不同变体可以引用不同对象。

生产入口先验证完整 Bundle 和逻辑摘要，复制内容容器后提取对象，拒绝同摘要的不同编码内容。按标准库 JSON 的确定性键序编码，用 `gzip.BestCompression` 和固定头压缩；生成结果经真实加载器验证后才以临时文件和重命名替换归档。输入 Bundle 不被改写。同一固定输入重复生成，归档字节与摘要必须一致。

`Load(profile)` 保持原接口。精确嵌入的私有压缩字节只读使用，避免 `embed.FS.ReadFile` 在每次加载时复制压缩资产。解压缓存以私有格式标识和压缩字节 SHA-256 为键，保留并发合并、容量限制及失败不缓存。内嵌归档另缓存已解析对象池和四个 Profile 的已验证 Bundle，外部归档仍逐次验证。Load 返回独立的可变容器副本，只共享不可变内容字符串。解析直接使用 `json.Unmarshal`，避免流式 Decoder 再复制整份 JSON 到临时缓冲。

加载继续执行原有路径、内容摘要、权限、ownership、转换记录和 BundleHash 校验。逻辑摘要和公开导出共用 Bundle 专用分段 writer，依 Bundle 字段顺序和省略规则逐项输出，文件 map 排序后逐记录编码，叶节点仍使用标准库 JSON。摘要不含换行，字节必须与 `json.Marshal` 完全一致；公开导出添加一个换行。读写共用 128 MiB 公开快照上限，实际读取也受限。未知存储版本、缺失引用、错误内容、截断 gzip 和解压超限均失败。解压上限仍为 512 MiB；构建另限制压缩归档为 10,000,000 字节。帮助、版本不加载模板。

`SnapshotHash`、`ManifestHash`、`BundleHash` 和来源记录是逻辑身份；私有归档和可执行文件 SHA-256 是物理身份。改变存储编码只能改变后者。公开 `bundle inspect/export` 及旧 `--base-bundle` 完整 JSON 读取路径保持原格式。程序版本及二进制绑定按新产物更新；旧保存计划仍需通过现有重建与漂移校验，插件通过公开升级流程更新 binding。

## 构建与体积记录

在源码仓运行：

```sh
go run ./tools/bundle --source-root /absolute/template-source --lock docs/source-lock.json --out internal/bundle/assets
go run ./tools/package /absolute/new-package-directory
```

打包输出的 `size-report.json` 记录各平台二进制字节数和摘要、内嵌归档字节数与摘要；二进制超过 35,000,000 字节立即失败。正式或候选装配也检查二进制上限。公开安装包、checksums 与 release manifest 的结构保持原有形式。

本仓的 Bundle 测试、基准测试、性能测量工具及测试 CI 已移除，见 [测试与 CI 状态](ci-verification.md)。交叉编译和体积记录不代表原生平台运行或性能验收。

程序回滚沿用现有升级事务恢复旧二进制及安装文档，不增加项目资产迁移。

## 组合技能来源锁 v3

Backend / Frontend 不再把共享技能副本存入 Git。`tools/bundle` 读取各端固定提交中的流程、专有技能和 `.template-source/profile-skills-source.json`，再读取 `skillsSource` 指定 Spec 提交中的清单、共享目录及适配材料。所有来源为指定 Git 对象，本地生成目录和未提交文件不参与构建。

```json
"skillsSource": {
  "sourcePath": ".",
  "templateCommit": "<Spec完整40位提交>",
  "configurationPath": ".template-source/profile-skill-sync.json",
  "configurationHash": "<清单原始字节SHA256>"
}
```

内部锁顶层 `schemaVersion` 为 3；Spec / Design 仍保留原来源结构。Backend / Frontend 的子项目来源锁必须与上述字段一致，并核验生成目录的路径、字节和权限摘要。`manifest.skillComposition` 记录 `sourceTemplateCommit`、清单路径/摘要、`skillsDigest` 和技能选择；公开 Bundle v3 与现有安装、ownership、选择性安装和升级事务不变。旧 v2 锁用于重建其已有完整副本，不允许夹带组合来源字段。

共享源先提交，再更新并提交两端来源锁，然后更新 CLI 固定来源及内嵌归档，最后更新父仓 gitlink。工作树锁不是正式构建输入；测试 fixture 的提交不替代真实交付来源。源码准备的忽略规则和入口不会带入业务实例。
# 固定产物的统一生成与检查

`node tools/artifacts.mjs check --scope derived --out <仓库外新证据目录>` 检查当前归档派生的四个帮助视图与完整帮助；不证明归档与来源锁一致。

`node tools/artifacts.mjs check --scope all --source-root <固定来源仓根> --out <仓库外新证据目录>` 从 `docs/source-lock.json` 的 Git 提交重建全部产物，再逐字节比较。将 `check` 改成 `generate` 才会写回产物。来源不齐时明确失败，不回退到最新 HEAD。

工具固定子进程 umask 为 0022，先在证据目录生成候选，再通过 Go overlay 重新编译下一层工具，使 `go:embed` 读取候选。帮助固定包含 `--reference`。检查归档 10,000,000 B 预算和公开快照 128 MiB 上限（包括最后一个换行）。普通文件的非执行权限差异不构成内容漂移；发布前仍核对完整目标身份，以发现并发编辑。

生成使用 Git 工作区中的 `yss-artifacts.lock` 防止同时发布；替换前重新核对输入和目标。文件逐项原子替换，不承诺跨文件崩溃原子性。中断后保留收据，确认没有生成进程运行再移除该运行的残留锁，运行 `check --scope all` 确认真实状态。不要自动删除未知锁或覆盖检查期间的人工修改。
