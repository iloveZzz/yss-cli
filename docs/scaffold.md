# DDD / MVC 脚手架

先查看 `yss scaffold catalog --json`。明确选择平台、精确 Spring Boot/Java 版本、父 POM 和 YSS BOM 版本；目录中的配方证据是模板来源记录，不是本次构建验证结果。

```text
yss scaffold ddd --project-name sample-app --base-package com.example.sample --output-dir /absolute/output --platform-profile spring-boot-3.5-jdk17 --spring-boot-version 3.5.16 --java-version 17 --group-id com.example --project-version 1.0.0-SNAPSHOT --parent-group-id com.yss.cloud --parent-artifact-id yss-cloud-microservice --parent-version 3.2.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT
```

替换 `ddd` 为 `mvc` 使用同一输入。工程名为 kebab-case，包名为合法 Java 包；拒绝版本范围、占位符和不匹配的平台。所有列出的坐标参数必填，不猜最新版本。

DDD 包含 domain、application、infrastructure、adapter/web、bootstrap；MVC 包含 server、service、repository。复用 Bundle 中的 Maven Wrapper 与 Java 模板，生成业务空骨架，不加入业务示例。`.yss/scaffold.json` 记录上下文、来源、平台与文件摘要，清单不包含自身。

实际生成要求输出父目录已存在。目标 `<output-dir>/<project-name>` 必须不存在，包括拒绝已有空目录；全树校验后同父目录暂存发布。`--dry-run` 不创建输出父目录。生成不会自动执行 Git 或 Maven。

构建时从生成工程根运行 `./mvnw -DskipTests package`；Windows 使用 `mvnw.cmd`，并准备匹配的 JDK 和 Maven 私服环境。CLI 3.1.0 的临时开发验证曾使用 JDK 17.0.16、Boot 3.5.16、YSS 3.2.0-SNAPSHOT，DDD/MVC 均构建成功；其他版本或组合仍需单独验证。固定作者与 UTC 时间的渲染结果可重复，实际生成仅采集一次时间。
