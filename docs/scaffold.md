# Java / Frontend 脚手架

新建后端工程默认采用 catalog 的 `defaultRecipeId`，当前为 YSS `4.0.0-SNAPSHOT`、Spring Boot `4.1.2`、Java `17`。Parent 为 `com.yss.cloud:yss-cloud-microservice:4.0.0-SNAPSHOT`，BOM 为 `com.yss.cloud:yss-components-bom:4.0.0-SNAPSHOT`。先查看 `yss scaffold catalog --json` 核对实际内嵌版本与配方证据。

以下五项工程参数必填，工程自己的 `--project-version` 始终使用显式值。

```sh
yss scaffold mvc --project-name sample-app --base-package com.example.sample --output-dir /absolute/output --group-id com.example --project-version 1.0.0-SNAPSHOT --dry-run
```

替换 `mvc` 为 `ddd` 使用同一规则。选择旧组件版本时增加一个参数，当前目录可唯一补齐 Parent3.2 / Boot3.5.16 / Java17。

```sh
yss scaffold mvc --project-name sample-app --base-package com.example.sample --output-dir /absolute/output --group-id com.example --project-version 1.0.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT --dry-run
```

平台、Boot、Java、Parent G/A/V、YSS BOM 版本七项技术参数均可选。所有显式值优先，默认配方满足全部约束时补齐缺项，否则只补齐唯一的完整配方。同值配置去重后仍有多个候选时，错误列出可区分参数和值。例如只选 `spring-boot-3.5-jdk17` 会在 3.0 与 3.2 之间产生歧义，需要明确 BOM 或 Parent 版本。旧 catalog 没有默认指针时 `defaultRecipeId` 返回 `null`，按唯一匹配补全，有歧义时需补充参数。

完整显式命令仍可使用自定义 Parent 和 BOM。没有匹配配方时，须明确 Parent G/A/V、BOM 版本，并通过显式平台、Boot、Java 约束唯一定位平台。缺失值会逐项提示，不推断 Parent 版本等于 BOM 版本。

```sh
yss scaffold ddd --project-name sample-app --base-package com.example.sample --output-dir /absolute/output --group-id com.example --project-version 1.0.0-SNAPSHOT --platform-profile spring-boot-3.5-jdk17 --spring-boot-version 3.5.16 --java-version 17 --parent-group-id com.example --parent-artifact-id custom-parent --parent-version 1.0.0 --yss-components-version 3.2.0-SNAPSHOT --dry-run
```

工程名为 kebab-case，包名为合法 Java 包。拒绝空技术值、版本范围、占位符和不匹配的平台。完整显式旧命令保留输入，包括 Boot4.1.1。解析仅使用内嵌目录。

`catalog.requiredFlags` 只有五项工程参数。生成及 dry-run 的 `result.input` 展示解析后的完整参数，生成收据保存相同输入。普通报告和 `--json` 展示相同值。

每个 recipe 的 `verificationScope` 原样展示已登记的验证范围，旧记录没有范围时输出 `{}`。`evidenceStatus` 与 `blockers` 保留该 recipe 的原始记录。只有 Parent 完整 GAV、YSS BOM 完整 GAV、平台标识和 Boot 精确版本全部匹配，且状态为 `verified`、范围非空时，生成报告才省略 `warnings`。

精确匹配但状态或范围受限、坐标仅登记在其他平台、以及自定义 Parent/BOM 没有登记记录时，普通报告和 `--json` 都提供相同的非阻断 `warnings`，合法输入仍可生成。提示描述 catalog 的历史证据，不能证明当前 SNAPSHOT 字节或本次目标工程的构建与运行通过。新 4.0 配方仍为 `candidate`。本地源码构件的 MVC/DDD 构建、启动监听及代表性 H2/PostgreSQL 消费已验证。DDD 缺失路由返回 500/A0101，仅计监听观察。远程构件发布与解析、Windows 原生运行仍未验证。`nextSteps` 仅用于可执行后续指引，dry-run 时仍为 `[]`。

DDD 包含 domain、application、infrastructure、adapter/web、bootstrap；MVC 包含 server、service、repository。复用 Bundle 中的 Maven Wrapper 与 Java 模板，生成业务空骨架，不加入业务示例。`.yss/scaffold.json` 记录上下文、来源、平台与文件摘要，清单不包含自身。

实际生成要求输出父目录已存在。目标 `<output-dir>/<project-name>` 必须不存在，包括拒绝已有空目录；全树校验后同父目录暂存发布。`--dry-run` 不创建输出父目录。生成不写入 Git、不执行 Maven；Git 工作树中的路径校验可能执行只读 Git 探测。

构建时从生成工程根运行 `./mvnw -DskipTests package`；Windows 使用 `mvnw.cmd`，并准备匹配的 JDK 和 Maven 私服环境。CLI 3.1.0 的临时开发验证曾使用 JDK 17.0.16、Boot 3.5.16、YSS 3.2.0-SNAPSHOT，DDD/MVC 均构建成功；其他未登记验证范围仍需单独验证。上述历史记录与本轮 4.0 配方的局部证据分别保留。固定作者与 UTC 时间的渲染结果可重复，实际生成仅采集一次时间。

## Frontend

CLI 3.3.0 新增此入口；CLI 3.2.0 只支持 Frontend Profile 安装，不包含生成命令，先查看实际帮助：

```sh
yss scaffold frontend --help
yss scaffold catalog --json
yss scaffold frontend --project-name my-frontend --output-dir /absolute/output --dry-run
yss scaffold frontend --project-name my-frontend --output-dir /absolute/output
```

参数只有工程名、输出父目录及可选 dry-run；全局 `--json` 沿用现有输出信封。父目录在预览和生成时都必须已存在，目标即使为空也拒绝。工程名沿用小写 kebab-case，设备名和不安全路径拒绝。预览完成资源/路径校验且零写入；不保存可应用计划。

Vue 3、VueUse、TypeScript、Vite、Tailwind、Button/Input 与 ESLint 形成最小工程。Node/pnpm 从 package.json 声明读取并在 catalog.frontend 展示；请求、OpenAPI、mock 与测试按任务接入。生成只修改 package.name，其余文件保留原始字节和模式；Vue 插值、CSS/JS 花括号不会按 Java 模板处理。

`.yss/scaffold.json` 使用独立 frontend 架构输入，记录来源、Bundle 摘要、文件/目录（不自哈希）。它不授权 sync 管理应用源码。需要技能时单独运行 `yss attach --root /absolute/output/my-frontend --profile frontend --installation skills`。

生成不需要 Node、pnpm 或网络；非 Git 父目录不需 Git，Git 工作树保留只读边界校验。进入工程后按其 README 执行冻结安装、lint、typecheck、build 和 dev，不能将生成成功当作运行通过。

发布前失败只清理已确认快照；无法安全删除的暂存路径在普通错误和 JSON stagingPath/cleanup 中报告。系统 rename 已完成后失败标记 renameApplied=true、phase=published，目标保留并报告实际路径；这些暂存目录不是 recover 事务，不自动重试或覆盖目标。
