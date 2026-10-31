# 模板 Bundle

从当前模板工作树直接生成内嵌归档，无 Node、Python、标签或来源锁依赖：

```sh
go run -tags yss_nobundle ./tools/bundle --source-root /path/to/yss-spec-project-template --out internal/bundle/assets
```

生成器读取 Git 已跟踪文件和未忽略的新文件，按 Spec / Backend 分发清单筛选。Backend 保留共享技能、process/templates 内核及 `profiles/backend` 覆盖层，覆盖层优先。不会注入阶段、治理合同、脚本或修改技能内容。

Bundle schema 6 只包含 `project`、`skills` 两种安装。`Files` 保存完整可选资产（包括脚手架资源），`Initial` 保存项目默认选择；技能安装使用自己的入口、默认技能和引用。`SkillFiles` 返回所选技能、平台副本和资源依赖。

来源记录包含实际 HEAD、政策原文摘要、筛选后源码快照摘要、清单摘要和完整 Bundle 摘要。工作树有任何修改时标记 `working-tree`，版本为 `worktree:<快照摘要>`；干净工作树标记 `committed`，版本为 `snapshot:<快照摘要>`，不会猜测发行版本。归档以摘要共享相同内容，限制压缩及解压体积，并在加载时校验文件 bytes、路径、权限和 ownership。

`yss_nobundle` 仅供生成器自举，不能用于发行 CLI。
