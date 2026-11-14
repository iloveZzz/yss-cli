# YSS 工作台前端

由 YSS CLI Frontend starter 生成，复用本地 Button/Input、Vue 3、Tailwind 4 与 Reka。当前应用主题为 Tesla Light，见 DESIGN.md。

在仓库根执行 `pnpm --dir ui install --frozen-lockfile`，再运行 `typecheck`、`lint`、`build`。`ui/dist` 由 Rust 用户程序内嵌。联调与正常运行使用 `cargo build --locked --bin yss` 后的 `target/debug/yss workbench --no-open`；Vite 单独启动没有工作台会话及 API。

页面通过同源服务读写个人数据，来源项目只读。共享快照和请求生命周期归 useWorkbench；编辑草稿持有打开时 revision，后台刷新不覆盖输入。来源预览单独取消旧请求。无远程字体、图片、CDN 或持久测试套件。
