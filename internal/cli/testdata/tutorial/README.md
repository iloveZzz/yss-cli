# 内部 CLI 教程材料

这些材料供 `TestTutorialStageRawPlanAndEnvelopeBoundary` 验证阶段登记、原始计划保存/应用、过期输入和批准边界。它们不是业务 Spec、批准、实际项目证据或可直接移植的工程。

测试先初始化隔离 Spec 实例，再安装需要的当前阶段资源，复制 JSON checkpoint 与 items。计划通过原始 stdout 保存；`--json` envelope 另行检验，不能充当计划。真实项目按对应 Skill 准备当前资产、实际测试、独立审查及批准。
