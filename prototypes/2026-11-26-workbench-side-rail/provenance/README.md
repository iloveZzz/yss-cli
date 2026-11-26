# 原型依赖来源

Badge、Button、Card、Dialog、Dropdown Menu、Separator、Table 和 Tabs 复用 YSS CLI 工作台 `ui/src/components/ui` 中的现有源码，保留工作台已经采用的本地适配。来源为 [YSS CLI ee0aaaa](https://github.com/iloveZzz/yss-cli/tree/ee0aaaaa334fc433280373d02c8e2c5b5b2823ab/ui/src/components/ui)，上游为 shadcn-vue New York v4 固定提交 [67c9a39](https://github.com/unovue/shadcn-vue/tree/67c9a3926dc0a854507b325c6337ff2210d16379/apps/v4/registry/new-york-v4/ui)。 [组件清单](shadcn-vue.json) 列出实际复用的文件、上游原始摘要和原型当前摘要。

许可证见 [shadcn-vue MIT](SHADCN-LICENSE.txt) 和 [YSS 设计 MIT](YSS-DESIGN-LICENSE.txt)。

原型样式直接引用正式工作台的 [设计变量](../../../ui/src/styles/tokens.css)、[组件主题映射](../../../ui/src/styles/theme.css) 和 [动效](../../../ui/src/styles/motion.css)。构建时读取同一份主题源码，离线成品内联所需样式。

构建采用 [vite-plugin-singlefile 2.3.4](https://www.npmjs.com/package/vite-plugin-singlefile/v/2.3.4)，把 JavaScript 和 CSS 内联到 `dist/index.html`。构建工具的许可证见 [MIT](VITE-PLUGIN-SINGLEFILE-LICENSE.txt)。
