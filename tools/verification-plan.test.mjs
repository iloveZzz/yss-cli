import test from "node:test";
import assert from "node:assert/strict";
import { selectVerification } from "./verification-plan.mjs";

test("版本、固定 Bundle 和来源锁更新选择专项验证", () => {
  const plan = selectVerification({ paths: ["internal/domain/domain.go", "internal/bundle/assets/bundles.json.gz", "docs/source-lock.json"], versionOnly: true, sourcePaths: [".agents/skills/yss-ddd-scaffold-generator/scripts/generate_scaffold.mjs"] });
  assert.equal(plan.scope, "impacted");
  assert.ok(plan.go.some(group => group.packages.includes("./internal/bundle")));
  assert.ok(plan.go.some(group => group.packages.includes("./internal/domain")));
  assert.ok(plan.sourceSuites.includes("backend-scaffolds"));
  assert.ok(!plan.go.some(group => group.packages.includes("./...")));
  assert.ok(!plan.race.some(group => group.packages.includes("./...")));
  assert.equal(plan.nativeRequired, true);
});

test("事务、共享核心和未知路径扩大为全量", () => {
  for (const file of ["internal/transaction/journal.go", "internal/bundle/bundle.go", "internal/domain/domain.go", "unknown/runtime.conf"]) {
    const plan = selectVerification({ paths: [file] });
    assert.equal(plan.scope, "full", file);
    assert.deepEqual(plan.go, [{ packages: ["./..."] }]);
    assert.deepEqual(plan.race, [{packages:["./..."]}]);
  }
});

test("局部 Go 实现消费反向依赖闭包", () => {
  const graph = { "./internal/format": [], "./internal/cli": ["./internal/format"], "./cmd/yss": ["./internal/cli"] };
  const plan = selectVerification({ paths: ["internal/format/format.go"], importGraph: graph });
  assert.equal(plan.scope, "impacted");
  assert.deepEqual(plan.go[0].packages, ["./cmd/yss", "./internal/cli", "./internal/format"]);
});

test("未知模板影响和显式全量要求不会静默跳过", () => {
  assert.equal(selectVerification({ paths: ["docs/source-lock.json"], sourcePaths: ["scripts/lib/approval-current.mjs"] }).scope, "full");
  assert.equal(selectVerification({ paths: ["README.md"], forceFull: true }).scope, "full");
  assert.equal(selectVerification({ paths: ["README.md"] }).nativeRequired, false);
});

test("帮助实现不能按派生资产绕过核心，冻结 fixture 不冒充生产变化",()=>{assert.equal(selectVerification({paths:["internal/bundle/helpview.go"]}).scope,"full");assert.equal(selectVerification({paths:["docs/source-lock.json"],sourcePaths:["tests/fixtures/upstream-source/scripts/lib/approval-record.mjs"]}).scope,"impacted");});
test("批准业务输入只在 AST 证明变更边界时使用专项",()=>{const file="internal/governance/spec_baseline.go";assert.equal(selectVerification({paths:[file]}).scope,"full");assert.equal(selectVerification({paths:[file],goChanges:{[file]:{SharedChanged:false,Functions:["verifySpecBaselineSource"]}}}).scope,"impacted");assert.equal(selectVerification({paths:[file],goChanges:{[file]:{SharedChanged:false,Functions:["exportSpecBaseline"]}}}).scope,"full");});

test("导入、副作用或未证明的空函数变化不能选择专项",()=>{const file="internal/governance/spec_baseline.go";for(const change of [{SharedChanged:false,Functions:[]},{SharedChanged:true,Functions:["verifySpecBaselineSource"]}])assert.equal(selectVerification({paths:[file],goChanges:{[file]:change}}).scope,"full");});

test("登记的合同编译与本地 Plan Schema 消费者完整进入专项",()=>{const plan=selectVerification({sourcePaths:["scripts/lib/implementation-contract-compiler.mjs",".template-spec/process/schemas/lifecycle-registry.schema.json"]});assert.equal(plan.scope,"impacted");assert.deepEqual(plan.sourceSuites,["implementation-contracts","specialist-business-inputs"]);assert.equal(plan.nativeRequired,true);assert.deepEqual(plan.unknown,[]);});
