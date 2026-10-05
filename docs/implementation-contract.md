# 统一 Go CLI 本地实现合同

授权来源：2026-10-05 用户明确要求实现统一 Go CLI 方案。目的：一个 yss 命令承接四种 Profile 和项目治理。正式发布、提交和推送不在当前授权内。

当前工程在维护运行目录构建并校验，随后落入 /Users/zhudaoming/Projects/yss-cli。共同 API：

- internal/schema：Parse([]byte) (any,error)，LoadFile(string) (any,error)，Validate(schemaPath,dataPath string) ([]Issue,error)。禁用网络引用，启用 format，拒绝重复键及别名。
- internal/transaction：Operation {Path string, Data []byte, Mode uint32, Delete bool, Before *domain.Descriptor}；Apply(root,kind string,ops []Operation) (Result,error)，Recover(root string) (Result,error)，Rollback(root string) (Result,error)，Status(root string) (Result,error)。事务信息在 .yss/transactions 下；不修改 Git index。
- internal/governance：Run(group,action,root string,args map[string]string) (any,error)。项目本地资产为权威输入，禁止推测批准与阶段顺序。
- internal/domain：共享 Profile、Descriptor、版本、错误和 JSON envelope。

并行写范围：schema 执行者仅 internal/schema；transaction 执行者仅 internal/transaction；governance 执行者仅 internal/governance。主控负责其余工程文件、依赖锁和最终集中验证；实现者不充当独立审查者。

项目源根、模板根、工具根显式传入。任何未迁移能力须返回明确的 UNPORTED 状态；现有旧实现继续保留。本合同准备完成不代表整个重构完成或达到稳定发布目标。
