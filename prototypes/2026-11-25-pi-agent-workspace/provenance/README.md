# shadcn-vue 组件来源

原型使用 New York v4 registry，固定提交 [`d2684d84`](https://github.com/unovue/shadcn-vue/tree/d2684d84b945d0ef8a33793a285cb418d2c2d6e4/apps/v4/registry/new-york-v4/ui)。逐文件源码路径、原始 SHA-256 和适配后 SHA-256 见 [组件清单](shadcn-vue.json)。本地适配将 `cn` 和 registry 内部组件导入指向原型路径。许可证见 [MIT](SHADCN-LICENSE.txt)。

Chat 采用 Message Scroller、Message 和 Bubble，Prompt Input 采用 Input Group，工具步骤采用 Trace，命令输出采用纯文本 Code Block。Code Block 仅保留纯文本版的组件和导出，避免将语法高亮器及其语言包引入示例。模型选择组合使用项目所需的 shadcn-vue 控件。原型对话、运行状态和样例数据在本地编排。

上游组合用法见 [AI Elements](https://shadcn-vue.com/docs/ai-elements)。

独立预览由固定的 [vite-plugin-singlefile 2.3.4](https://www.npmjs.com/package/vite-plugin-singlefile/v/2.3.4) 在构建时将 JavaScript 和 CSS 内联到 `dist/index.html`。构建工具的许可证见 [MIT](VITE-PLUGIN-SINGLEFILE-LICENSE.txt)。
