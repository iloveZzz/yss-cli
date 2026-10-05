# 统一 Go CLI 架构研究基线

## Research Scope

四类 CLI、治理脚本、旧 JavaScript API 与纯 Go 依赖。固定来源见新工程 docs/source-lock.json。

## Executive Read

可以统一程序入口和公共核心；稳定版仍需逐项完成行为等价与原生平台验证。

## Findings

- claim-architecture：四 Profile 统一到一个命令和共享核心有现成公共实现依据；Spec 公开接口须单独适配。
- claim-validation：Go 依赖提供实现原生校验和存储的 API；须用差分测试证明现有语义等价，不能仅换语言宣称等价。
- claim-transactions：写入迁移必须保留持久事务与回滚约束，并保护业务文件和用户修改。

## Counter-Signals

现有 Python FormatChecker 与 YAML 合同不能靠安装方式消除；Schema 默认格式策略和标量行为须显式实现。Spec API 与插件内部模块引用扩大兼容范围。

## Source Map

- evidence-core：/Users/zhudaoming/Projects/yss-spec-project-template/.template-source/cli-core/family.mjs（FAMILIES/CHECKS）
- evidence-spec：/Users/zhudaoming/Projects/yss-spec-project-template/submodules/create-yss-spec/src/api/index.js（API_VERSION/module.exports）
- evidence-python：/Users/zhudaoming/Projects/yss-spec-project-template/scripts/lib/json-schema.mjs（validateJsonSchemas/Python Draft202012Validator）
- evidence-tx：/Users/zhudaoming/Projects/yss-spec-project-template/.template-source/cli-core/transaction.mjs（persistent transaction journal）
- evidence-schema：https://pkg.go.dev/github.com/santhosh-tekuri/jsonschema/v6@v6.0.3（AssertFormat/UseLoader/UseRegexpEngine）
- evidence-yaml：https://pkg.go.dev/go.yaml.in/yaml/v3@v3.0.5（Compatibility/Node）
- evidence-sqlite：https://pkg.go.dev/modernc.org/sqlite@v1.60.1（Overview）

## Decision Handoff

用户已授权独立 yss-cli 本地实现，采用一个 Go 核心和四 Profile。程序版本、运行协议、Profile 模板与旧 CLI 基线分开管理。当前不提交、推送或发布。

## Evidence Limitations

源码事实与依赖 API 仅支撑可实施性。性能、所有旧版本兼容和 Windows/Linux 原生运行须后续实测；不采用语言推断性能。
