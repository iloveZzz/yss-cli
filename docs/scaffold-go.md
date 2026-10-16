# yss scaffold 的 Go 实现

`yss scaffold ddd|mvc` 由 `internal/scaffold` 直接生成，不再把 Bundle 导出到临时目录后调用 `node` 运行模板仓技能脚本。Go 运行时只依赖二进制内置的固定 Spec Bundle 和 `git`（生成目录保护），不依赖 Node.js 或 Python。

本文先于实现写成，记录生成器行为合同、拆包与对拍口径；实现完成后按对拍结果回写（§10、§12）。

## 1. 事实源与边界

| 项 | 说明 |
| --- | --- |
| 事实源 | 模板仓固定提交（`docs/source-lock.json` 的 `templateCommit`，当前 `981f7399ef6c77b515045a588e708be70af5fab3`）中的 `.agents/skills/yss-ddd-scaffold-generator`、`.agents/skills/yss-layered-mvc-scaffold-generator` 及其 `scripts/lib/*.mjs` 导入闭包 |
| 基准实现 | 上述 Node 生成器；模板仓脚本保持不动，仅用于 `tools/scaffold-parity/` 对拍 |
| 资产来源 | 内嵌 Spec Bundle（§8），不新增第二份副本 |
| 目标 | 成功路径输出与 Node 逐字节一致（文件集合、内容、权限、符号链接、空目录），失败路径同样拒绝且不留产物 |
| 不改动 | 治理语义（`internal/governance/contracts/contract_semantic.go` 对生成器文件集合的 provenance 绑定）；模板仓；`.yss/scaffold-generation.json` 的字段与语义 |

## 2. 输入

CLI 参数沿用现有 `scaffoldValues` / `scaffoldFlags`，帮助文本结构、JSON 返回结构和退出码保持兼容。

| 参数 | 独立模式 | 正式模式 | 说明 |
| --- | --- | --- | --- |
| `--project-name` | 必填 | 必填 | `^[a-z][a-z0-9-]*$` |
| `--base-package` | 必填 | 必填 | `^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)*$` |
| `--output-dir` | 必填 | 必填 | 生成到 `<output-dir>/<project-name>`；该目录必须不存在 |
| `--platform-profile` `--spring-boot-version` `--java-version` | 必填 | 禁止 | 取自内置平台目录，必须与目录配方精确一致 |
| `--group-id` `--project-version` `--parent-group-id` `--parent-artifact-id` `--parent-version` `--yss-components-version` | 必填 | 必须完整且与合同一致 | 仅允许 `[A-Za-z0-9_.-]`；独立模式版本须为精确 `x.y.z[-qualifier]` |
| `--contract-file` `--contract-id` `--contract-version` `--approval-ref` `--compiler-draft-ref` `--persisted-ref` | 禁止 | 必填 | 出现任一项即进入正式模式；与 `--standalone` 互斥 |
| `--standalone` | 可省略 | 禁止 | 无合同参数时恒为独立模式 |
| `--force` | 拒绝 | 拒绝 | initialize-only，CLI 层以 ARGUMENT 拒绝 |

CLI 对独立模式追加“不带参考示例”（`Options.WithExample=false`）；正式模式不设置，由合同 `reference_example` 决定。`internal/scaffold` 仍完整支持 `WithExample`，供合同路径和对拍驱动使用。

## 3. 输出文件集合

### 3.1 DDD（`yss-ddd-scaffold-generator`）

`<name>/`：`pom.xml`、`README.md`、`mvnw`、`mvnw.cmd`、`.mvn/**`（Maven Wrapper，含 `maven-wrapper.jar`）、`db/`（空目录）、`.yss/scaffold-generation.json`，以及 `<name>-domain|application|infrastructure|adapter|bootstrap` 五个模块（各含 `pom.xml` 和 `src/main/java/<包路径>`、`src/main/resources`、`src/test/java/<包路径>` 目录）、`<name>-adapter/<name>-web`（`pom.xml`、`src/main/java/<包路径>/rest`）。`bootstrap` 另含 `<Name>Application.java`、`application.yml`、`application-scaffold-local.yml`、`logback-spring.xml`、`ArchitectureRulesTest.java`、`PlatformIntegrationTest.java`、`application-scaffold-test.yml`。

### 3.2 分层 MVC（`yss-layered-mvc-scaffold-generator`）

`pom.xml`、`README.md`、Maven Wrapper（取自 DDD 技能的 `assets/wrapper`）、`.yss/scaffold-generation.json`，以及模块 `server`、`service`、`repository`（正式合同按能力可加 `adapter`、`client`、`feign-client`；独立模式固定三个核心模块）。每个模块含 `pom.xml` 和 `package-info.java`；`server` 另含启动类、`application.yml`、`application-scaffold-local.yml`、测试 YAML、`<Name>ApplicationTest.java`、`LayeredMvcArchitectureTest.java`、`PlatformIntegrationTest.java`。

### 3.3 参考示例（`reference_example.enabled=true`）

在 `examples/reference/` 下生成独立 reactor：同一生成器再渲染一份骨架（项目名 `<name>-reference`，包名 `<base>.reference`），并由 DDD 技能 `assets/reference/common` 与 `domain-driven|layered-mvc` 模板叠加。MVC 的 web 模块 POM 追加 `yss-component-exception` 依赖，bootstrap/server POM 中 `h2` 的 `test` 作用域改为 `runtime`。

## 4. 模板渲染规则

- DDD 模板 `{{key}}`：按键序逐键 `replaceAll`，键序为平台变量（`boot_version`、`java_version`、`java_range`、`web_starter`、`web_test_starter`、`aop_starter`、`swagger_artifact`、`<versions 键>_version`）、`project_name`、`application_class_name`、`base_package`、`group_id`、`project_version`、`parent_*`、`yss_components_version`、`project_description`、`author`、`date`、`db_dependency`。`author` 取环境变量 `USER`（缺省 `yss-team`），`date` 为 UTC 日期。JDK 8 渲染 `java_version=1.8`，`java_range` 形如 `[17,18)`。
- MVC 的 POM、启动类、YAML、README 在生成器内以字符串拼接（父 POM 与 `PlatformIntegrationTest` 的常量文本由 Node 生成器逐字提取）；`architecture-rules-test.java.template` 只替换 `{{base_package}}`。MVC 文件以 `put` 写出：缺少结尾换行则补一个。
- 参考示例模板使用另一组变量（`web_module`、`run_module`、`app_module`、`storage_module`、`*_package`、`validation_namespace`、`wire_profile`、`result_meta_schema` 等），正则 `{{[a-z_]+}}` 一次替换，未知键原样保留；路径中的占位符同样渲染并去掉 `.template`。`wire_profile` 的摘要和 `result_meta_schema` 来自 `.agents/skills/yss-dto/references/openapi-wire-profile.yaml`。
- DDD 生成后扫描全部非二进制文件，包含 `{{` 或 `root/root` 即失败（`生成文件包含未替换占位符或明文凭据`）；MVC 生成器没有这一步，保持不变。
- `platformDigest` 为对象键递归排序后的 JSON 做 SHA-256；`JSON.stringify` 语义（紧凑/缩进、不转义 `<>&`、非 ASCII 原样、整数键优先的属性顺序、数字格式）由 `internal/scaffold/jsonx.go` 的有序 JSON 复刻。
- 排序：清单与摘要多处使用 `localeCompare`（ICU `en-US`）。Go 以内置的 ASCII 排序表复刻（主级：空白＜标点＜符号＜数字＜字母，字母不分大小写；同主级小写在前；兄弟名逐层排序、目录原地展开）。对拍启动时断言 Node 的 `Intl.Collator` 区域为 `en-US`。其他 ICU 区域的 Node 生成器可能得到不同的 `template_digest`，Go 固定为 `en-US`。

## 5. 版本与平台校验

- 平台目录：内置 `.template-spec/engineering/backend-platforms.json`（`schema_version=2`、`kind=backend-platform-catalog`），按 `profile id` + `spring_boot_version` 精确匹配，同 id 多补丁版本必须指定版本，Spring Boot 版本须为精确补丁号。
- 独立模式：`--java-version` 必须等于配方的 `java_version`；Maven 坐标仅含 `[A-Za-z0-9_.-]`；`project_version`、`parent_version`、`yss_components_version` 必须是精确版本（拒绝 x、latest、版本范围）。`platform_configuration.recipe_digest = platformDigest({profile, coordinates})`，`platform_verification` 恒为 `unverified`。
- 正式模式：合同必须是 schema v4/v5 的 `project-scaffold-contract`，绑定对应 `scaffold_kind`。
  1. 生成器自有约束（在 Go 中按 Node 顺序复刻）：参数与合同一致（合同/批准/草稿/持久化引用、项目名、包名、Maven 坐标、`target_output_dir`）、`allowed_write_paths`、固定验证命令、`work_unit`、`approval`、`generation_policy`、profile 与模块闭包（DDD 固定五模块，MVC 按能力排序）、参考示例摘要与 `--with-example` 一致性。
  2. 合同 JSON Schema、架构决定、真实用户决定、设计前置（Technical Design、数据/API 决定、工程合同批准记录）由内置治理核心校验，不再需要 Python `jsonschema`。入口是 `governance.VerifyScaffoldInputs`，注册为内部语义校验 `scaffold-inputs`：它是从 `verifyScaffoldSemantic`（`yss contract verify --kind scaffold`）中**原样抽出**的“平台资格以外”的部分，`contract verify` 的行为不变，不对外暴露新的 `--kind`。治理核心从**治理根**（合同所在目录向上最近的含 `yss-project.yaml` 的目录）读取 Schema、技能资产和批准记录，正式模式因此要求该项目实例已安装设计校验所需的技能资产（与 `contract verify` 一致）。
  3. 平台资格由 Go 生成器自己判定：`resolveBackendPlatform` / `assertContractPlatform` 的移植，含 verified 证据逐项核对（报告字节摘要、配方摘要、源码指纹、Maven 阶段、证据工件）。固定模板提交上唯一 `verified` 的条目因生成器源码指纹漂移（`requalify candidate`）两侧都会拒绝；证据重新资格后两侧才会成功。
  4. 候选平台：`Options.PlatformCatalog` + `Options.AllowCandidate` 对应 Node 的 `platformOptions {catalog, candidate:true}`，清单 `platform_verification` 记为 `candidate`。仅供对拍驱动与维护使用，CLI 从不设置。
- 生成目录保护：目标存在（含符号链接）拒绝；输出目录所在 Git 仓库中 gitlink/子模块挂载点、空 gitlink、detached HEAD 拒绝覆盖。需要 `git`，规则取自 `repository-scope-policy.mjs`（`gitscope.go`）；调用 `git` 时清除全部 `GIT_*` 环境变量，与 yss 其余 Git 调用一致。

## 6. 文件权限与写入流程

1. 在 `<output-dir>` 下创建（`MkdirAll`）输出目录，并 `mkdtemp` 一个 `.<name>.staging-XXXXXX`（0700）；整个项目先在内存中生成并完成占位符等校验，再写入其中的 `<name>/`。
2. 普通文件 `0666 & ~umask`，目录 `0777 & ~umask`；Maven Wrapper 取内置 Bundle 记录的权限（`mvnw` 0755，其余 0644），`.mvn` 及其子目录 0755。
3. `rename` 到 `<output-dir>/<name>`，随后删除 staging；任何失败都会删除 staging，不留部分产物（已创建的输出目录保留，与 Node 相同）。
4. 不创建符号链接。

## 7. `.yss/scaffold-generation.json`

两种模式的字段顺序和取值由生成器固定，Go 以有序 JSON 复刻（2 空格缩进、末尾换行）。

- 独立模式：`standaloneConfiguration` 的全部键，随后 `source_fingerprint`、`generated_tree_digest`、`bootstrap_main_class`、`bootstrap_main_source`、`generator{id,template_digest}`、`ownership{generated_files,user_owned_globs}`，再追加 `schema_version=2`、`kind=standalone-backend-scaffold`、`generation_mode=standalone-generation`、`completion_level=generated`、`platform_verification=unverified`、`maven_coordinates_source=user-supplied`、`generation_input`、`input_digest`、`reference_verification`、`verification_commands`、`verification{status:not-executed}`、`readiness{lifecycle_approved:false,ready_for_agent:false}`、`generated_at`。
- 正式模式：`source_fingerprint`、`generated_tree_digest`、`platform_verification`、`platform_configuration`、`schema_version`、可选 `reference_example`/`reference_verification`、`kind=backend-scaffold`、架构身份、合同与批准引用、`design_prerequisites`、`generator`、`ownership`、`readiness`（下游技能摘要、引擎基线/架构 Profile 与合同摘要、架构规则摘要）、`generation_policy`、`verification_commands`、`generated_at` 等。DDD 与 MVC 的键序、`contract_digest` 是否带 `sha256:` 前缀等细节不同，以对拍为准；`design_prerequisites` 的版本/影响取合同前置绑定（治理核心已确认其与批准文档一致）。
- `source_fingerprint.generator_digest|verifier_digest` 取生成器与校验器源码（含 `.mjs` 脚本）的字节摘要，所以内置 Bundle 必须继续携带这些模板仓文件；Go 只把它们当字节读取，不执行。
- MVC 的 `generator.template_digest` 覆盖整个 MVC 技能目录（含脚本与测试文件）；DDD 的覆盖 `assets/`。
- `generated_at` 为 `Date.toISOString()` 格式，是对拍中唯一登记放行的差异。

CLI JSON 返回 `projectRoot`、`manifestPath`、`generatorSkill`、`architecture`、`generationMode`、`completionLevel`、`verification`、`bundleHash`、`sourceState`，字段与此前一致；正式模式清单没有 `verification` 字段，返回 `null`，与此前一致。

## 8. 拆包与资产

| 文件 | 职责 |
| --- | --- |
| `internal/scaffold/scaffold.go` | `Options`/`Result`、`Run`、staging 与发布、占位符扫描 |
| `source.go` | 资产来源接口；从内嵌 Spec Bundle（`bundle.Load("spec")`）读取模板、Wrapper、`scripts/lib` 与技能源码字节 |
| `jsonx.go` `collate.go` `tree.go` | JS 兼容的有序 JSON；`localeCompare` 排序；内存中的产物树与提交 |
| `platform.go` `platform_resolve.go` `platform_smoke.go` | 平台目录、摘要、变量；平台解析与 verified 证据核对；`PlatformIntegrationTest` 文本 |
| `fingerprint.go` | `source_fingerprint`、`generated_tree_digest`、目录摘要 |
| `gitscope.go` | `repository-scope-policy.mjs` 的生成目录保护子集 |
| `standalone.go` `example.go` | 独立模式输入与清单；参考示例 |
| `ddd.go` `ddd_manifest.go` `mvc.go` `mvc_parent_pom.go` `mvc_manifest.go` | 两个生成器 |
| `formal.go` `formal_gen.go` | 正式合同校验与清单 |
| `internal/governance/scaffold_inputs.go` | `VerifyScaffoldInputs`：治理核心的 scaffold 输入校验入口 |

模板、Wrapper（含 `maven-wrapper.jar`）、`openapi-wire-profile.yaml`、平台目录与证据、下游技能目录全部来自内嵌 Bundle，**没有**新增 `go:embed` 资产，因此不存在第二份副本或漂移。仅有的“复制”是两段由 Node 生成器逐字提取的常量文本（MVC 父 POM、`PlatformIntegrationTest`），由对拍保证与基准一致。

## 9. 错误码与退出码

| 场景 | code | 退出码 |
| --- | --- | --- |
| 命令/参数错误（非 ddd\|mvc、重复参数、缺必填、`--force`、模式冲突、参数缺值） | `ARGUMENT` | 2 |
| 缺少 `git` | `CAPABILITY` | 2 |
| 生成器校验或写入失败（目标已存在、平台不匹配、占位符残留、正式合同拒绝等） | `EXECUTION` | 1 |
| 取消 | `CANCELLED` | 沿用 |

消息沿用 Node 生成器的中文原文，由 CLI 统一加 `<skill> 生成失败: ` 前缀（此前是 `<skill> 生成失败 (exit status 1): ❌ 生成失败: …`）；正式合同里由治理核心拒绝的情形带 `脚手架合同校验未通过: ` 前缀和治理核心自己的诊断。

## 10. 对拍

`tools/scaffold-parity/`（Node，仅开发/CI，不进入发行包，不是运行依赖）在相同输入下分别运行 Node 基准与新二进制，比较文件集合、SHA-256、权限位、符号链接与空目录。允许差异只能登记在表中，默认零差异；矩阵与登记见该目录 README。**对拍属构建后黑盒校验，不代表发行验收**，也不恢复已删除的测试体系；CI 中单独成 job（`.github/workflows/scaffold-parity.yml`），仅 ubuntu 运行，失败不影响发行包。

固定模板提交 `981f7399ef6c` 上的结果：100 个场景（独立 DDD/MVC × 5 个平台 Profile、命名/坐标/输出位置、参考示例、拒绝；正式合同 v4/v5、数据/API 决定、MVC 全部能力组合、拒绝），失败 0，比较 1430 个文件、6,106,484 字节；唯一使用的登记差异是 `generated-at`。

## 11. 与治理 provenance 的关系

`contract_semantic.go` 通过 `contractPlatformFingerprint` 对 `generate_scaffold.mjs`、`assets/**` 等文件集合计算摘要，并要求 Bundle 提供这些字节。Go 生成器**不改变**该绑定：Bundle 继续携带 Node 脚本，Go 计算出的 `source_fingerprint` 与 Node 基准逐字节一致（对拍的清单比较覆盖）。本任务对该文件的唯一改动是把 `verifyScaffoldSemantic` 拆成 `verifyScaffoldCommon` / `scaffoldBackendInputs` / `scaffoldPlatformQualification` 三段并新增 `scaffold-inputs` 校验，`contract verify --kind scaffold` 的检查顺序、拒绝码和输出不变。

## 12. 实现取舍（均不改变成功路径的输出）

- 平台目录的结构校验是子集：保留 `schema_version/kind/重复/精确补丁版本` 检查，不复刻 `validateComponentCatalog` 对组件能力、证据与 Boot3 策略的全部断言；目录来自固定 Bundle，不随用户输入变化。
- `validateArchitectureIdentity` 只复刻生成器用到的部分（架构族/Profile/生成器匹配、H2 验证库、digest 形态）；Skill 注册表中的能力/模块闭包断言已被生成器自己的模块闭包检查覆盖。
- 独立模式不重放 `assertStandaloneManifest` 对自己刚生成的清单的自检（输入摘要一致性），它对生成结果是恒真式。
- 正式合同的 Schema 与批准文档校验由 Go 治理核心完成，拒绝的**文案**与 Python `jsonschema` 不同；拒绝的**结论**一致，由对拍的 `--verbose` 可人工核对。
- Node 在非 `en-US` 的 ICU 区域会得到不同的目录排序（从而不同的 `template_digest`），Go 固定为 `en-US`。
