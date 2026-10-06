# 四旧 CLI 退役合同

统一入口版本为 `yss 1.0.0`；稳定资产的放行由完整发行证据决定，版本号本身不表示平台验证通过。按验证推进 Spec → Design → Backend → Frontend，真实项目仅使用隔离副本，原地迁移另行安排。

`tools/bundle` 独立读取模板根及三个 Agent 模板源的固定 Git 对象，保留 bytes、mode、ownership、渲染、initial/full 集合及阶段/Skill 闭包。来源锁 schema v2 绑定来源提交和策略摘要；公共 `bundle inspect/export` 提供资产及 manifest。旧 CLI 版本和提交仅作为历史基线。

Bundle 与 native metadata schema v2 分开记录模板及统一 CLI 身份。`working-tree` 候选不能冒充已提交来源。最终生态发行清单汇总源码、Bundle、平台二进制、插件及恢复包摘要，保存为仓外发行产物，不循环嵌入参与仓库的提交哈希。

旧实例先 doctor/diff，再保存显式 migrate plan；apply 重建计划并核验输入。旧 metadata 原字节保留。两个固定 binding 路径、旧 receipt、身份及受管文件受同一事务保护；Backend 保持 `plan-to-backend`。未完成旧事务返回 `LEGACY_INTERRUPTED`，先由仓外固定旧执行器恢复。

已绑定项目的来源更新通过对应公开插件的 upgrade/migration plan/apply 提交新 binding。直接 `yss sync` 若会更换模板、Bundle 或实际二进制，返回 `BINDING_REQUIRED`；已有 identity 与 binding 来源不一致返回 `BINDING_CONFLICT`。同来源资源补装继续可用，已有 receipt 的 bytes/mode 始终绑定保存计划输入。

历史插件覆盖只接受代码内固定版本、归档插件摘要、receipt/source/core/file bytes 和 mode 政策。项目提供的哈希不能扩展政策。旧桥接没有公开 rollback 时，隔离恢复工具必须先核验所有 current/before 字节和权限再恢复；不得宣称旧执行器具有不存在的公开接口。

实际取消返回 `CANCELLED`；重复恢复幂等。完整恢复后的 init 可保留归档重新预演，只允许原受管文件的空父目录。回退遇到后续用户修改返回 `CONCURRENT`，停止覆盖。必要能力的 `UNPORTED`、缺证据或输入漂移阻断对应阶段。

项目 `recover` / `rollback` 默认只读，执行时增加 `--apply`；`migrate recover` / `migrate rollback` 沿用显式写入入口。首次 init 在身份写入前中断时，先执行 `yss recover --root <项目根> --profile <家族> --json` 检查，再增加 `--apply` 恢复。准备阶段尚未写入目标时，结果为 `sealed`：保留原始恢复材料，收据声明 `noTargetWrites`，核验通过后可重新保存 init 计划。已发布事务仍走整体恢复，后续用户字节或权限变化会阻止覆盖。

算法、原生无解释器、真实旧恢复矩阵分别记录。正式旧恢复矩阵覆盖四 Profile 各 12 项，绑定二进制、固定旧包和日志摘要。合成 fixture、偶然 `INPUT_DRIFT`、交叉编译和 workflow 配置均不能替代真实目标通过。

1.0.0 稳定发行范围为本机 darwin/arm64；发行前完成该平台的原生运行、权限、取消、恢复，以及固定提交上的不可裁剪模板和 CLI 集成门禁；要求实际 exit 0、`input_drift=false`、必需项无未执行。移除旧 gitlink 后还须在干净 checkout 完成生产、构建、插件、分发及发布验证。

不执行 npm unpublish。本地从未公开发布的固定恢复版本必须另存可获取渠道，不能声称它已在 npm。保留上一版二进制、Bundle、来源锁、插件、旧 gitlink SHA 和恢复材料；不使用 reset/clean 覆盖工作。提交、推送、tag、稳定发布、npm deprecated、旧仓 archive 分别需要授权。

用户已将 1.0 平台验收限定为本机；其他平台 CI 及产物齐备不阻断本次发布。工具的 requiredPlatforms 必须与独立 caller 固定范围一致，禁止通过输入自行删减已选平台。
