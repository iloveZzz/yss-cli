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

`Load(profile)` 保持原接口。精确嵌入的私有压缩字节只读使用，避免 `embed.FS.ReadFile` 在每次加载时复制压缩资产。缓存只保存不可变解压字节，键包含私有格式标识和压缩字节 SHA-256，保留并发合并、容量限制及失败不缓存。每次独立解析元数据和对象池，只还原所选 Profile；同次加载可共享不可变内容字符串，调用者拥有独立的可变 Bundle。解析直接使用 `json.Unmarshal`，避免流式 Decoder 再复制整份 JSON 到临时缓冲。

加载继续执行原有路径、内容摘要、权限、ownership、转换记录和 BundleHash 校验。逻辑摘要用标准库 JSON Encoder 直接写入 SHA-256，去掉 Encoder 添加的末尾换行，产生与原 `json.Marshal` 完全相同的摘要字节，避免另分配整份 JSON 副本。未知存储版本、缺失引用、错误内容、截断 gzip 和解压超限均失败。解压上限仍为 512 MiB；构建另限制压缩归档为 10,000,000 字节。帮助、版本不加载模板。

`SnapshotHash`、`ManifestHash`、`BundleHash` 和来源记录是逻辑身份；私有归档和可执行文件 SHA-256 是物理身份。改变存储编码只能改变后者。公开 `bundle inspect/export` 及旧 `--base-bundle` 完整 JSON 读取路径保持原格式。程序版本及二进制绑定按新产物更新；旧保存计划仍需通过现有重建与漂移校验，插件通过公开升级流程更新 binding。

## 可重复验证

在源码仓运行：

```sh
go run ./tools/bundle --source-root /absolute/template-source --lock docs/source-lock.json --out internal/bundle/assets
go test -count=1 ./internal/bundle ./tools/release
go run ./tools/package /absolute/new-package-directory
python3 tools/size-performance.py --self-check
python3 tools/size-performance.py --binary /absolute/new/yss --baseline-binary /absolute/old/yss --min-memory-reduction 0.30 --out /absolute/new-performance-directory
go test ./internal/bundle -run '^$' -bench '^BenchmarkLoad$' -benchmem -benchtime=10x
```

打包输出的 `size-report.json` 记录各平台二进制字节数和摘要、内嵌归档字节数与摘要；二进制超过 35,000,000 字节立即失败。正式或候选装配也检查二进制上限。公开安装包、checksums 与 release manifest 的结构保持原有形式。

性能工具当前使用 macOS 原生 `/usr/bin/time -l`，RSS 单位为字节。每个命令先预热每个二进制一次，再至少运行十个独立进程，成对测量并交替先后顺序；报告保留实际命令、退出码、每次耗时和峰值 RSS、摘要与输入漂移状态。p95 用 nearest-rank；新 p95 必须 ≤ `max(旧 p95 × 1.10, 旧 p95 + 5ms)`，Backend / Frontend 的 RSS 中位数按指定降低比例验收。未来比较已优化版本时可按任务设置比例，默认仅要求内存不增长。无旧二进制时状态为 `measured`，明确记录未执行基线比较，不声称通过优化比例。

真实语料等价测试用 `YSS_BUNDLE_BASELINE_ROOT=/absolute/frozen-old-gzip-directory` 启用，逐 Profile 比较完整 Bundle。全套测试用锁定模板作为 `YSS_LEGACY_ORACLE_ROOT` 与 `YSS_SCHEMA_CORPUS_ROOT`，适用平台另跑原生 smoke / recovery；race 仅在明确排查并发问题时手动执行。交叉编译仅证明构建和体积，运行资格仍按仓库原生平台要求取得。

CI 上传六平台体积报告与本机性能测量报告。本地工作树的结果属于实现验证；最终 committed SHA 的原生收据、插件 binding、正式安装与回退证明需在提交后重新生成。程序回滚沿用现有升级事务恢复旧二进制及安装文档，不增加项目资产迁移。

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
