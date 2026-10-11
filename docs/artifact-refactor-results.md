# 生成产物重构交付与验收记录

本次适配 `main@5195fa05ef6ca5bd01f7dcdf48866ba50a06a698`，保留该基线已有的 Bundle 解码缓存。运行时验收对象为 `134d355ef8a50dfb773d0bf97ef39c9457c7c7b7`；之后仅补充本报告，不将其他提交或二进制冒认为已测对象。

原始开发工作区保持不变。测试源码、测量工具、原始样本和生成收据位于仓库外的 `/workspace/scratch/yss-refactor/`。CI 仅编译生产包并检查生成产物，不恢复已删除的测试 workflow。本文中的通过是本次明确列出的检查结果，不授予正式发行资格。

## 交付拆分

| 阶段 | PR / 实现提交 | 已实现内容 |
|---|---|---|
| RF-01 | [#2](https://github.com/iloveZzz/yss-cli/pull/2) / `124bf77` | 候选构建、来源清单、工具链与二进制身份收据；仓库外测量合同 |
| RF-02 | [#3](https://github.com/iloveZzz/yss-cli/pull/3) / `821a69c` | 统一 derived/all、generate/check；依赖顺序重新编译嵌入候选产物；完整帮助与容量检查；构建/生成 CI |
| RF-03 | [#4](https://github.com/iloveZzz/yss-cli/pull/4) / `1ae5202` | 操作内以声明摘要和实际编码内容为键复用校验事实；复用尺寸，保持 Load 可变容器隔离 |
| RF-04 | [#5](https://github.com/iloveZzz/yss-cli/pull/5) / `406b853` | 技能文件一次分桶；共享树哈希核心；渲染复用已解码字节 |
| RF-05 | [#6](https://github.com/iloveZzz/yss-cli/pull/6) / `725ec8f` | Bundle 分段 JSON writer；摘要与导出复用；统一包含换行的 128 MiB 上限；实际读取受限 |
| RF-07 | [#7](https://github.com/iloveZzz/yss-cli/pull/7) / `092c69e`、`c445b11`、`134d355` | 固定 Go 隐式输入并核对实际编译输入；归档构建专用正则延迟初始化；集成证据与回滚清单 |

各 PR 依次建立在上一项上，未自动合并。RF-06 NativeTransform 来源摘要修复、新 Snapshot/压缩格式和跨仓策略调整未混入本次变更。

## 最终原始基线对比

Linux amd64、Go 1.27.1、CGO_ENABLED=0、同机同输入；每命令/每二进制预热一次后取 20 个独立进程样本，交替先后顺序。记录 CPU 亲和性、CPU 配额、Go 运行环境、编译参数和源码/二进制/测量工具摘要。RSS 使用独立轻量父进程的单进程 wait4 结果；Linux KiB 转为 bytes，避免大型证据清单在父进程中占用内存污染短命令的 RSS。p95 使用 nearest-rank。

下表 RSS 单位为十进制 MB（1,000,000 B），耗时单位为 ms。基线为上述原始 main，候选为上述已测提交。

| 命令 | 耗时中位数：基线 → 候选 | 耗时下降 | 峰值 RSS 中位数：基线 → 候选 | RSS 下降 | p95：基线 → 候选 |
|---|---|---|---|---|---|
| help | 11.4 → 10.4 | 8.5% | 16.61 → 16.25 | 2.2% | 12.5 → 11.7 |
| version | 11.1 → 10.9 | 1.6% | 16.48 → 16.25 | 1.4% | 14.9 → 15.7 |
| spec | 1205.0 → 590.2 | 51.0% | 420.32 → 160.10 | 61.9% | 1494.4 → 685.3 |
| design | 719.1 → 506.0 | 29.6% | 250.97 → 147.91 | 41.1% | 848.5 → 632.3 |
| backend | 2019.1 → 802.6 | 60.2% | 550.87 → 160.71 | 70.8% | 2490.5 → 1060.4 |
| frontend | 2116.1 → 887.8 | 58.0% | 565.49 → 166.42 | 70.6% | 2616.3 → 1032.1 |

全部命令满足 `候选 p95 ≤ max(基线 p95 × 1.10, 基线 p95 + 5ms)`；Backend/Frontend 的耗时和 RSS 均超过 20% 降幅目标，其他命令的 RSS 中位数没有回归。原始证据为 `evidence/paired-acceptance-integration/report.json`，输入无漂移，状态为 passed。

## 逐 PR 直接基线检查

首轮对七个版本正反序交替采样，每版本/命令 20 个独立进程样本，出现以下六个 p95 超限组合，整轮 `paired-direct-20/report.json` 保持 failed。首轮结束后，将全部失败组合、100 样本数量、一次预热、交替顺序、工具摘要和判定规则写入 `paired-confirmation-100/plan.json`，随后仅执行一次扩大样本复核。

| PR / 命令 | 首轮候选 p95 / 门槛（ms） | 100 样本复核 p95：直接基线 → 候选（ms） | 复核门槛（ms） | 结果 |
|---|---|---|---|---|
| RF02 / version | 27.71 / 18.61 | 12.50 → 12.66 | 17.50 | 通过 |
| RF02 / spec | 2192.13 / 1879.08 | 1872.88 → 1724.81 | 2060.17 | 通过 |
| RF04 / help | 39.14 / 39.03 | 46.51 → 50.77 | 51.51 | 通过 |
| RF04 / spec | 1971.21 / 1803.83 | 1817.76 → 1687.65 | 1999.54 | 通过 |
| RF04 / frontend | 2030.92 / 2024.64 | 2329.49 → 2285.12 | 2562.44 | 通过 |
| RF07 / design | 806.96 / 775.02 | 698.48 → 699.74 | 768.33 | 通过 |

复核全部通过，没有输入漂移或未执行项。分批资格表 `paired-confirmation-100/qualification.json` 对每个 PR/命令标明证据批次：RF-01、RF-03、RF-05 全部使用首轮 20 样本；RF-02 的 version/spec、RF-04 的 help/spec/frontend、RF-07 的 design 使用 100 样本复核，其余命令仍使用首轮证据。

这是披露了首轮失败的分批验收，不是“一整轮一次全部通过”。只复核失败项存在按结果选择的统计限制；没有挑选单个样本，没有混合 20 与 100 样本计算一个 p95，也没有放宽门槛。首轮记录、复核前计划及两批原始样本均保留。


逐 PR 只对 p95 执行硬门槛并记录 RSS；最终集成另对原始基线强制执行全部 RSS 和两项 20% 目标。共享队列中间版本的同一组进程样本用于两侧相邻 PR 比较，各 PR 比较不能称为统计独立。

## 生成、兼容与容量

已恢复 Spec 根仓及 Design、Backend、Frontend 三个来源仓，并取得 source-lock 固定的全部对象。完整 all 检查从这些 Git 对象重建归档，再用 Go overlay 将候选归档、候选帮助视图分别嵌入新编译的下游工具；完整帮助固定 `--reference`。各阶段收据保存在独立目录。

最新完整生成收据为 `evidence/rf07-accepted-all/receipt.json`，状态 checked；归档、四个帮助视图及 `docs/cli-help.md` 均逐字节不变。归档为 **8,903,344 B**，SHA-256 为 `5858a4cbac98ef86b7cf725dabe0607f5d2457d63e7ed55049c7ec7513aedb24`，小于 10,000,000 B。

公开快照保留现有字段、顺序、省略规则及结尾换行，容量上限为 **134,217,728 B**：

| Profile | 含换行的公开快照字节数 | BundleHash |
|---|---|---|
| spec | 73,074,334 | `2a3e842d8c745c5cb7773324d5df94cd15ff1f5213b3a2a97044a15e61c0d691` |
| design | 29,906,906 | `a46186f8643e07ea855bfbdd0fcced3a9fe6e407ede180a3fd46c134d16442a0` |
| backend | 110,899,046 | `f4e1d2445a53e8f8071085b0cad823a3c267b9914326b832cbb1ce0b4998c923` |
| frontend | 122,842,147 | `dd3227d9f026ce7d680672aac0534b4c356a4e31d343246cc4f998e382c8067c` |

已测候选二进制为 **29,708,448 B**，小于 35,000,000 B；SHA-256 为 `3f18e12f3719562e3600569d6eae39cf59e6f62d843744cf72dfa2df9cb0e801`。来源、完整编译输入与命令见 `evidence/frozen-rf07-accepted-build/build-receipt.json`。

仓库外实际执行的兼容检查覆盖：

- 四 Profile 的标准库 JSON 与分段 writer 逐字节比较、BundleHash 保持、真实导出后读回、旧 v1/v2 读取；help 与四 Profile inspect 的基线/候选公开输出逐字节一致。
- 相同声明摘要但不同内容仍被拒绝、不同编码表示分别核验、路径与权限不因 memo 省略、Load 可变容器修改隔离。
- 技能来源及投影漂移、空树、忽略项、变量、二进制与 CRLF、Unicode 路径和 Node/Go 跨语言树摘要向量。
- 精确容量边界、实际读取上限、短写、取消、错误传播、新导出目录清理和链接拒绝。
- 真正重新编译的微型 Go embed 生成链、检查不修复手改产物、缺来源失败、生成锁和并发目标保护；拒绝隐式 Go 输入、忽略的编译输入、来源链接及本地 module replace。
- Linux/macOS RSS 单位换算、失败/超时/取消、输入不一致拒绝、缺二进制保留失败收据，以及父进程内存不污染子进程 RSS。

外部 Bundle 检查、Node 生成/构建检查和测量器检查的日志分别为 `rf07-final-bundle-checks.log`、`rf07-tools-final.log`、`performance-tool-final-checks.log`。这些是本次针对变更执行的外部兼容用例，不等于恢复或执行已删除的全仓历史测试。生产包另执行 `CGO_ENABLED=0 go build ./...`。

[已测提交的 GitHub 构建与完整生成检查](https://github.com/iloveZzz/yss-cli/actions/runs/38138451113) 真实完成且成功。macOS 原生 CLI 性能没有执行；单位换算用例和可移植测量代码不替代该平台的原生验收。

## 保留的失败与审查记录

最初的测量器存在父进程清单内存污染短命令 RSS 的问题，相关运行已标记不可用于验收，见 `paired-integration/invalidation.json`。修正测量器后的 `paired-native-integration` 完成但未通过：帮助 RSS 中位数增加 12 KiB。随后将仅归档生产需要的正则初始化延迟到实际调用，兼容检查和完整来源重建再次通过，最终集成测量重新执行。上述原始失败记录均保留，不从最终报告中删除。

Standards 审查关闭了隐式 Go 输入、实际编译输入核对和存储文档不准确等发现；Spec 审查关闭了不兼容测量输入、输入消失时失败收据、RSS 单位及子进程取消回收等发现。两条审查线没有未关闭的具体缺陷；静态审查不代替执行证据。

## 使用与回滚

维护入口见 [构建与性能合同](artifact-performance.md) 和 [固定产物生成说明](bundle-storage.md)。例如：

```sh
node tools/build-candidate.mjs --out /absolute/new-build-evidence
node tools/artifacts.mjs check --scope derived --out /absolute/new-derived-evidence
node tools/artifacts.mjs check --scope all --source-root /absolute/fixed-template-source --out /absolute/new-all-evidence
```

将 check 改成 generate 才会写回产物。生成中断后保留失败收据，先确认进程已结束、确认残留锁归属，再执行完整 all 检查；不根据部分文件推定成功。

| 回滚对象 | 实现提交 | 回滚后的检查 |
|---|---|---|
| 归档专用正则延迟初始化 | `134d355` | 生产构建、来源闭包兼容向量、all 检查、短命令 RSS 重新测量 |
| 分段编码和快照 I/O | `725ec8f` | 生产构建、四 Profile 编码/BundleHash/导出读回、容量与失败用例、性能重测 |
| 技能索引和解码复用 | `406b853` | 生产构建、技能摘要/投影/跨语言向量、all 检查 |
| 操作内校验事实复用 | `1ae5202` | 生产构建、内容与元数据拒绝、Load 隔离、四 Profile 公开输出 |

按反向依赖顺序回退需要撤销的性能提交，评审 revert diff 后重新执行对应检查；不要通过修改黄金数据消除摘要差异。RF-01/RF-02 的生成/构建检查和 RF-07 的来源输入加固可独立保留。

本次未改变公开资产格式，也没有项目资产迁移；代码回退不需要改写用户项目。各原始提交均保留，PR 未自动合并或发布。

外部证据索引为 `/workspace/scratch/yss-refactor/evidence/index.json`，可移交证据包为 `/workspace/scratch/yss-refactor/delivery/yss-refactor-evidence.tar.gz`。包内含验证源码、原始样本、生成/构建收据、日志、失败历史和校验清单；大体积二进制及重建暂存产物保留在原证据目录，以收据摘要绑定。
