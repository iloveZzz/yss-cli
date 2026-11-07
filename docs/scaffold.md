# Java / Frontend 脚手架

先查看 `yss scaffold catalog --json`。明确选择平台、精确 Spring Boot/Java 版本、父 POM 和 YSS BOM 版本；目录中的配方证据是模板来源记录，不是本次构建验证结果。

```text
yss scaffold ddd --project-name sample-app --base-package com.example.sample --output-dir /absolute/output --platform-profile spring-boot-3.5-jdk17 --spring-boot-version 3.5.16 --java-version 17 --group-id com.example --project-version 1.0.0-SNAPSHOT --parent-group-id com.yss.cloud --parent-artifact-id yss-cloud-microservice --parent-version 3.2.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT
```

替换 `ddd` 为 `mvc` 使用同一输入。工程名为 kebab-case，包名为合法 Java 包；拒绝版本范围、占位符和不匹配的平台。所有列出的坐标参数必填，不猜最新版本。

DDD 包含 domain、application、infrastructure、adapter/web、bootstrap；MVC 包含 server、service、repository。复用 Bundle 中的 Maven Wrapper 与 Java 模板，生成业务空骨架，不加入业务示例。`.yss/scaffold.json` 记录上下文、来源、平台与文件摘要，清单不包含自身。

实际生成要求输出父目录已存在。目标 `<output-dir>/<project-name>` 必须不存在，包括拒绝已有空目录；全树校验后同父目录暂存发布。`--dry-run` 不创建输出父目录。生成不写入 Git、不执行 Maven；Git 工作树中的路径校验可能执行只读 Git 探测。

构建时从生成工程根运行 `./mvnw -DskipTests package`；Windows 使用 `mvnw.cmd`，并准备匹配的 JDK 和 Maven 私服环境。CLI 3.1.0 的临时开发验证曾使用 JDK 17.0.16、Boot 3.5.16、YSS 3.2.0-SNAPSHOT，DDD/MVC 均构建成功；其他版本或组合仍需单独验证。固定作者与 UTC 时间的渲染结果可重复，实际生成仅采集一次时间。

## Frontend

当前开发源码新增此入口，尚未随新版发行；不要把 CLI 3.2.0 的 Profile 安装能力当作 scaffold 支持，先查看实际帮助：

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
