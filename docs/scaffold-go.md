# yss scaffold 的 Go 实现

`yss scaffold ddd|mvc` 由 `internal/scaffold` 直接生成，不再导出 Bundle 后调用 `node` 运行模板仓技能脚本。Go 运行时只依赖二进制内置的固定 Spec Bundle 和 `git`（目录保护），不依赖 Node.js、Python。

本文先于实现写成，记录生成器行为合同、拆包与对拍口径；实现与本文冲突时以对拍结果为准并回写本文。

## 1. 事实源与边界

| 项 | 说明 |
| --- | --- |
| 事实源 | 模板仓固定提交（`docs/source-lock.json` 的 `templateCommit`，当前 `981f7399ef6c77b515045a588e708be70af5fab3`）中的 `.agents/skills/yss-ddd-scaffold-generator`、`.agents/skills/yss-layered-mvc-scaffold-generator` 及其 `scripts/lib/*.mjs` 导入闭包 |
| 基准实现 | 上述 Node 生成器；模板仓脚本保留不动，仅用于 `tools/scaffold-parity/` 对拍 |
| 资产来源 | 内嵌 Spec Bundle（见 §8），不新增第二份副本 |
| 目标 | 成功路径输出与 Node 逐字节一致（文件集合、内容、权限、符号链接），失败路径同样拒绝且不留产物 |
| 不改动 | 治理语义（`internal/governance/contracts/contract_semantic.go` 对生成器文件集合的 provenance 绑定）；本任务不改模板仓，不改变 `.yss/scaffold-generation.json` 的字段与语义 |

## 2. 输入

CLI 参数沿用现有 `scaffoldValues` / `scaffoldFlags`，帮助文本、JSON 返回结构和退出码保持兼容。

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

CLI 对非正式模式追加 `--without-example`（不附带参考示例）；正式模式不传该开关，由合同 `reference_example` 决定。`internal/scaffold` 仍支持 `WithExample`，供合同路径使用。

## 3. 输出文件集合

### 3.1 DDD（`yss-ddd-scaffold-generator`）

目录 `<name>`：`pom.xml`、`README.md`、`mvnw`、`mvnw.cmd`、`.mvn/**`（Maven Wrapper，含 `maven-wrapper.jar`）、`db/`（空目录）、`.yss/scaffold-generation.json`，以及 `<name>-domain|application|infrastructure|adapter|bootstrap` 五个模块（各含 `pom.xml` 与 `src/main/java/<包路径>`、`src/main/resources`、`src/test/java/<包路径>` 目录）、`<name>-adapter/<name>-web`（`pom.xml`、`src/main/java/<包路径>/rest`）。`bootstrap` 另含 `<Name>Application.java`、`application.yml`、`application-scaffold-local.yml`、`logback-spring.xml`、`ArchitectureRulesTest.java`、`PlatformIntegrationTest.java`、`application-scaffold-test.yml`。

### 3.2 分层 MVC（`yss-layered-mvc-scaffold-generator`）

`pom.xml`、`README.md`、Maven Wrapper（取自 DDD 技能的 `assets/wrapper`）、`.yss/scaffold-generation.json`，以及模块 `server`、`service`、`repository`（按合同能力可加 `adapter`、`client`、`feign-client`；独立模式固定三个核心模块）。每个模块含 `pom.xml` 和 `package-info.java`；`server` 另含启动类、`application.yml`、`application-scaffold-local.yml`、测试 YAML、`<Name>ApplicationTest.java`、`LayeredMvcArchitectureTest.java`、`PlatformIntegrationTest.java`。

### 3.3 参考示例（`reference_example.enabled=true`）

在 `examples/reference/` 下生成独立 reactor：同一生成器再渲染一份骨架（项目名 `<name>-reference`，包名 `<base>.reference`），并由 DDD 技能 `assets/reference/common` 与 `domain-driven|layered-mvc` 模板叠加。MVC 的 web 模块 POM 追加 `yss-component-exception` 依赖，bootstrap/server POM 中 `h2` 的 `test` 作用域改为 `runtime`。

## 4. 模板渲染规则

- DDD 模板 `{{key}}`：按顺序逐键 `replaceAll`，键序为平台变量（`boot_version`、`java_version`、`java_range`、`web_starter`、`web_test_starter`、`aop_starter`、`swagger_artifact`、`<versions 键>_version`）、`project_name`、`application_class_name`、`base_package`、`group_id`、`project_version`、`parent_*`、`yss_components_version`、`project_description`、`author`、`date`、`db_dependency`。`author` 取环境变量 `USER`（缺省 `yss-team`），`date` 为 UTC 日期。`java_version` 在 JDK 8 渲染为 `1.8`，`java_range` 形如 `[17,18)`。
- MVC 的 POM、启动类、YAML、README 在生成器内以字符串拼接；`architecture-rules-test.java.template` 只替换 `{{base_package}}`。每个文件以 `put` 写出：缺少结尾换行则补一个。
- 参考示例模板使用另一组变量（`web_module`、`run_module`、`app_module`、`storage_module`、`*_package`、`validation_namespace`、`wire_profile`、`result_meta_schema` 等），正则 `{{[a-z_]+}}` 一次替换，未知键原样保留；路径中的占位符同样渲染并去掉 `.template`。`wire_profile` 的摘要和 `result_meta_schema` 来自 `.agents/skills/yss-dto/references/openapi-wire-profile.yaml`。
- 生成后扫描全部非二进制文件，包含 `{{` 或 `root/root` 即失败（`生成文件包含未替换占位符或明文凭据`）。
- 平台目录与摘要：`platformDigest` 为对键排序后的 JSON 做 SHA-256；`JSON.stringify` 语义（紧凑、不转义 `<>&`、非 ASCII 原样）由 `internal/scaffold` 内的有序 JSON 实现复刻。
- 排序：Node 清单与摘要多处使用 `localeCompare`（ICU `en-US`）。Go 以内置的 ASCII 排序表复刻（主级：标点＜符号＜数字＜字母，字母不分大小写，同主级小写在前）；对拍工具启动时断言 Node 的 `Intl.Collator` 区域为 `en-US`。在其他区域运行的 Node 生成器可能得到不同的 `template_digest`，Go 固定为 `en-US`。

## 5. 版本与平台校验

- 平台目录：内置 `.template-spec/engineering/backend-platforms.json`（`schema_version=2`、`kind=backend-platform-catalog`），按 `profile id` + `spring_boot_version` 精确匹配，同 id 多补丁版本必须指定版本，Spring Boot 版本须为精确补丁号。
- 独立模式：`--java-version` 必须等于配方的 `java_version`；Maven 坐标仅含 `[A-Za-z0-9_.-]`；`project_version`、`parent_version`、`yss_components_version` 必须是精确版本（拒绝 x、latest、版本范围）。`platform_configuration.recipe_digest = platformDigest({profile, coordinates})`，`platform_verification` 恒为 `unverified`。
- 正式模式：合同必须是 schema v4/v5 的 `project-scaffold-contract`，绑定对应 `scaffold_kind`；JSON Schema、架构决定、用户决定、设计前置（Technical Design、数据/API 决定、工程合同批准）、平台已验证兼容证据由内置治理核心（`internal/governance`，与 `yss contract verify --kind scaffold` 同源）校验，Go 生成器在此基础上复核命令行元数据与合同一致、写范围、验证命令、模块闭包等生成器自有约束。不再需要 Python `jsonschema`。
- 生成目录保护：目标存在（含符号链接）拒绝；输出目录所在 Git 仓库中 gitlink/子模块挂载点、空 gitlink、detached HEAD 拒绝覆盖。需要 `git`，规则取自 `repository-scope-policy.mjs`。

## 6. 文件权限与写入流程

1. 在 `<output-dir>` 下 `mkdtemp` 创建 `.<name>.staging-XXXXXX`（0700），全部内容写入其中的 `<name>/`。
2. 普通文件 `0666 & ~umask`，目录 `0777 & ~umask`；Maven Wrapper 取内置 Bundle 记录的权限（`mvnw` 0755，其余 0644），`.mvn` 与 `.mvn/wrapper` 目录 0755。
3. 校验通过后 `rename` 到 `<output-dir>/<name>`，删除 staging；任何失败都会删除 staging，不留部分产物。
4. 不创建符号链接。

## 7. `.yss/scaffold-generation.json`

两种模式的字段顺序与取值由生成器固定，Go 复刻为有序 JSON（2 空格缩进，末尾换行）。

- 独立模式：`standaloneConfiguration` 的全部键，随后 `source_fingerprint`、`generated_tree_digest`、`bootstrap_main_class`、`bootstrap_main_source`、`generator{id,template_digest}`、`ownership{generated_files,user_owned_globs}`，再追加 `schema_version=2`、`kind=standalone-backend-scaffold`、`generation_mode=standalone-generation`、`completion_level=generated`、`platform_verification=unverified`、`maven_coordinates_source=user-supplied`、`generation_input`、`input_digest`、`reference_verification`、`verification_commands`、`verification{status:not-executed}`、`readiness{lifecycle_approved:false,ready_for_agent:false}`、`generated_at`。
- 正式模式：`source_fingerprint`、`generated_tree_digest`、`platform_verification=verified`、`platform_configuration`、`schema_version`、可选 `reference_example`/`reference_verification`、`kind=backend-scaffold`、架构身份、合同与批准引用、`design_prerequisites`、`generator`、`ownership`、`readiness`（下游技能摘要、引擎基线与合同摘要、架构规则摘要）、`generation_policy`、`verification_commands`、`generated_at` 等（DDD 与 MVC 字段集合略有不同，以对拍为准）。
- `source_fingerprint.generator_digest|verifier_digest` 取生成器与校验器源码文件（含 `.mjs` 脚本）的字节摘要，因此内置 Bundle 必须继续携带这些模板仓文件；Go 只把它们当作字节读取，不执行。
- `generated_at` 为 `Date.toISOString()` 格式（UTC，毫秒，`Z`），是对拍中唯一登记放行的差异（见 `tools/scaffold-parity/README.md`）。

`CLI` JSON 返回 `projectRoot`、`manifestPath`、`generatorSkill`、`architecture`、`generationMode`、`completionLevel`、`verification`、`bundleHash`、`sourceState`，字段与此前一致。

## 8. 拆包与资产

| 包/文件 | 职责 |
| --- | --- |
| `internal/scaffold`（API） | `Run(ctx, Options)`、`Result`、`Error{Code,Message}`；不依赖 `internal/cli` |
| `internal/scaffold/source.go` | 资产来源：从内置 Spec Bundle 读取模板、Wrapper、`scripts/lib` 与技能源码字节 |
| `jsonx.go` `collate.go` | JS 兼容的有序 JSON 与 `localeCompare` 排序 |
| `platform.go` | 平台目录、`platformDigest`、变量、`PlatformIntegrationTest` 文本 |
| `fingerprint.go` | `source_fingerprint`、`generated_tree_digest`、模板目录摘要 |
| `gitscope.go` | `repository-scope-policy.mjs` 的生成目录保护子集 |
| `ddd.go` `mvc.go` `example.go` `manifest.go` `contract.go` | 两种生成器、参考示例、清单与正式合同校验 |

模板、Wrapper（含 `maven-wrapper.jar`）、`openapi-wire-profile.yaml`、平台目录全部来自内置 Bundle（`bundle.Load("spec")` 的 `Files`），**没有**新增 `go:embed` 资产，因此不存在第二份副本或漂移。治理 provenance 对生成器文件集合的绑定（`contract_semantic.go`）继续指向 Bundle 中的同一批文件，保持不变。

## 9. 错误码与退出码

| 场景 | code | 退出码 |
| --- | --- | --- |
| 命令/参数错误（非 ddd\|mvc、重复参数、缺必填、`--force`、模式冲突） | `ARGUMENT` | 2 |
| 缺少 `git` | `CAPABILITY` | 2 |
| 生成器校验或写入失败（目标已存在、平台不匹配、占位符残留、正式合同拒绝等） | `EXECUTION` | 与现有 EXECUTION 一致 |
| 取消 | `CANCELLED` | 沿用 |

消息文本尽量沿用 Node 生成器的中文原文（`❌ 生成失败` 前缀除外，由 CLI 统一加 `<skill> 生成失败: `）。

## 10. 对拍

`tools/scaffold-parity/`（Node，仅开发/CI，不进入发行包，不是运行依赖）在相同输入下分别运行 Node 基准实现与新二进制，比较文件集合、SHA-256、权限位、符号链接与空目录。允许差异只能登记在表中，默认零差异。矩阵与登记见该目录 README。**对拍属构建后黑盒校验，不代表发行验收**，也不恢复已删除的测试体系；CI 中单独成 job，仅 ubuntu 运行，失败不影响发行包。

## 11. 与治理 provenance 的关系

`contract_semantic.go` 通过 `contractPlatformFingerprint` 对 `generate_scaffold.mjs`、`assets/**` 等文件集合计算摘要，并要求 Bundle 提供这些字节。Go 生成器**不改变**该绑定：Bundle 继续携带 Node 脚本，Go 计算出的 `source_fingerprint` 必须与治理侧一致，对拍同时覆盖该一致性。
