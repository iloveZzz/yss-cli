# 原型组件与构建来源

页面使用 shadcn-vue 的 New York v4 组件。组件复制自 YSS 设计技能随附快照，固定上游版本为 `67c9a3926dc0a854507b325c6337ff2210d16379`。逐文件来源和 SHA-256 见 [组件清单](shadcn-vue.json)，上游许可证见 [MIT](SHADCN-LICENSE.txt)。已适配的文件同时记录原始与本地摘要。

Dialog Footer 的按钮导入指向本原型的组件目录。Sheet 的关闭控件使用中文可访问名称和尺寸适配标记。UI 色彩、圆角和排版采用 YSS 工作台的 [基础色值](../src/tokens.css) 与 [shadcn-vue 映射](../src/theme.css)。

构建配置与依赖锁沿用同仓的 Pi Agent 工作区原型。Node 需满足 `^20.19.0 || >=22.12.0`。使用 pnpm 10.32.1 执行锁定安装、类型检查与构建。

```sh
corepack pnpm@10.32.1 install --frozen-lockfile
corepack pnpm@10.32.1 typecheck
corepack pnpm@10.32.1 build
```

构建时使用 `vite-plugin-singlefile@2.3.4` 将 CSS 与 JavaScript 内联到 `dist/index.html`，许可证见 [MIT](VITE-PLUGIN-SINGLEFILE-LICENSE.txt)。直接使用浏览器打开该文件即可离线体验。预览、原站模拟页及仓库读取场景均使用本地样例数据。
