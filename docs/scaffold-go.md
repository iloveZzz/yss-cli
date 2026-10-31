# 直接生成后端工程

`yss scaffold ddd` 和 `yss scaffold mvc` 使用内嵌模板中的后端资产生成工程，无需初始化 Harness、需求合同、设计批准或阶段门禁。使用 Go 实现，不调用 Node 或 Python 包装脚本。

```sh
yss scaffold catalog
yss scaffold ddd --help
yss scaffold mvc --help
```

先从 catalog 选择真实平台配方，再提供工程名、Java 包名、输出目录及精确 Maven 坐标。以下值只是调用示例，版本须替换成 catalog 中存在且本地 Maven 仓库可获取的组合：

```sh
yss scaffold mvc --project-name order-service --base-package com.yss.orders \
  --output-dir ./backend --platform-profile spring-boot-3.5-jdk17 \
  --spring-boot-version 3.5.16 --java-version 17 \
  --group-id com.yss --project-version 1.0.0-SNAPSHOT \
  --parent-group-id com.yss.cloud --parent-artifact-id yss-cloud-microservice \
  --parent-version 3.2.0-SNAPSHOT --yss-components-version 3.2.0-SNAPSHOT --dry-run
```

确认预览后去掉 `--dry-run`；DDD 工程将 `mvc` 改为 `ddd`。目标是 `<output-dir>/<project-name>`，保护已有目录、符号链接和用户内容，不使用强制覆盖。CLI 不自动初始化 Git 或执行 Maven 构建。

生成成功只表示文件已生成。需要验证工程时，准备所选 JDK 和已有 Maven settings，从生成工程根优先运行 `./mvnw`；缺少私服坐标或配置时如实记录缺口，不改写为已验证成功。
