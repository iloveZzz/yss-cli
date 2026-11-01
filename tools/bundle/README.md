# 模板 Bundle

从当前模板工作树直接生成内嵌归档，无 Node、Python、标签或来源锁依赖：

```sh
go run -tags yss_nobundle ./tools/bundle --source-root /path/to/yss-spec-project-template --out internal/bundle/assets
```

生成器读取 Git 已跟踪文件和未忽略的新文件，按 Spec / Backend 分发清单筛选。Backend 按共享内核→`profiles/_specialist`→`profiles/backend` 顺序覆盖。共享内核仅为 `.agents/skills`、`.template-spec/process`、`.template-spec/templates`，不会继承根 user-guide、agents 或 engineering 目录。不会注入阶段、治理合同、脚本或修改技能内容。

Bundle schema 6 只包含 `project`、`skills` 两种安装。`Files` 保存完整可选资产（包括脚手架资源），`Initial` 保存项目默认选择；技能安装使用自己的入口、默认技能和引用。`SkillFiles` 返回所选技能、平台副本和资源依赖。

来源记录包含实际 HEAD、政策原文摘要、筛选后源码快照摘要、清单摘要和完整 Bundle 摘要。工作树有任何修改时标记 `working-tree`，版本为 `worktree:<快照摘要>`；干净工作树标记 `committed`，版本为 `snapshot:<快照摘要>`，不会猜测发行版本。归档以摘要共享相同内容，限制压缩及解压体积，并在加载时校验文件 bytes、路径、权限和 ownership。

`yss_nobundle` 仅供生成器自举，不能用于发行 CLI。

每个安装集合必须显式提供 `skillDependencies` 对象；无必需依赖时写 `{}`，缺失或 `null` 拒绝生成。只声明安装必需技能，正文提及、条件路由和反例不会自动成为依赖。资源路径继续由 `skillReferences` 声明。生成器检查所有可选技能、资源和依赖环；重复依赖去重并排序。

`project` 与 `skills` 分别保存 `skillRequirements`，技能闭包和平台副本补全后才统计 `installedSkills`。顶层依赖保留 project 兼容快照；运行时以所选安装视图为准。已生成旧 schema 6 归档继续可读，其路径依赖按所选模式重建；用新生成器重新生成旧模板源码需要先更新分发清单。
