# 来源与许可

最小工程改编自 SCRM scrm-frontend 提交 `a5a331548bf7d2d4c46153506c4b823b4bcd14eb` 的 frontend scaffold 资源。未复制业务页面、请求演示、生成 API、mock、补丁、脚本或测试套件。

Button、Input 和 cn 工具保留 shadcn-vue 原始字节；`copied-files.json` 只登记实际保留的文件，`registry-manifest.json` 保留上游固定 revision 与组件目录信息（不是本工程已安装组件清单）。MIT 许可见 `SHADCN-LICENSE.txt`。

Pi 会话采用 shadcn-vue 的 Message、Message Scroller、Trace 和 Questionnaire。`registry-manifest.json` 的 `additional_sources` 登记实际引入的源码、固定 revision、原始摘要和适配后摘要。适配使用本地 cn、Button、Reka Collapsible 和文本图标。“回到最新”将键盘焦点移入会话区域，即时跳转后可以立刻向上滚动。

DESIGN 与 `src/styles/tokens.css` 来自 YSS 模板的共享标准亮色主题；必要边界 #7d8aa0、辅助文字 #62728c、新列表 48px/14/22。theme/main 只映射当前 Button/Input 所需角色和密度，未携带原型布局。YSS 许可见 `YSS-DESIGN-LICENSE.txt`。生成时只结构化修改 package.name，其他资源按 Bundle 原字节复制；准确来源由 `.yss/scaffold.json` 记录。

依赖及其许可证通过包管理器安装。请求与 OpenAPI 按任务选用，不内置于最小工程。

`components.json` 为 YSS 编写的工程配置，描述上述现有源码，不是上游复制文件，不加入 `copied-files.json`。后续按需取得的新组件在目标应用中记录其固定来源、原始摘要与实际适配；不能把本文件或旧清单当成其已安装/验证记录。

工作台页面使用 New York v4 的表格、页签、表单、弹层及反馈组件。新增源码的固定 revision、原始字节和本地适配摘要记录在 `registry-manifest.json` 的 `additional_sources`。组件复用本地别名和 Tesla Light tokens，Lucide 图标随应用内嵌。

`SingleSelect.vue` 和 `ModalDialog.vue` 是本工程组合组件。Select 保留空选项与表单校验，Dialog 保留保存期间的关闭保护、草稿确认和触发点焦点返回；两者通过 Reka 管理叠放和键盘交互。
