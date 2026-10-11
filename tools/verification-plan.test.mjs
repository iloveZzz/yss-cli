import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { PACKAGE_TIMEOUT, SLOW_TESTS, SLOW_TIMEOUT, goTestArgs, selectVerification } from "./verification-plan.mjs";

test("版本、固定 Bundle 和来源锁更新选择专项验证", () => {
  const plan = selectVerification({ paths: ["internal/domain/domain.go", "internal/bundle/assets/bundles.json.gz", "docs/source-lock.json"], versionOnly: true, sourcePaths: [".agents/skills/yss-ddd-scaffold-generator/scripts/generate_scaffold.mjs"] });
  assert.equal(plan.scope, "impacted");
  assert.ok(plan.go.some(group => group.packages.includes("./internal/bundle")));
  assert.ok(plan.go.some(group => group.packages.includes("./internal/domain")));
  assert.ok(plan.sourceSuites.includes("backend-scaffolds"));
  assert.ok(!plan.go.some(group => group.packages.includes("./...")));
  assert.deepEqual(plan.race, []);
  assert.equal(plan.nativeRequired, true);
});

test("事务、共享核心和未知路径扩大为全量", () => {
  for (const file of ["internal/transaction/journal.go", "internal/bundle/bundle.go", "internal/domain/domain.go", "unknown/runtime.conf"]) {
    const plan = selectVerification({ paths: [file] });
    assert.equal(plan.scope, "full", file);
    assert.deepEqual(plan.go, [{ packages: ["./..."] }]);
    assert.deepEqual(plan.race, []);
  }
});

test("局部 Go 实现消费反向依赖闭包", () => {
  const graph = { "./internal/format": [], "./internal/cli": ["./internal/format"], "./cmd/yss": ["./internal/cli"] };
  const plan = selectVerification({ paths: ["internal/format/format.go"], importGraph: graph });
  assert.equal(plan.scope, "impacted");
  assert.deepEqual(plan.go[0].packages, ["./cmd/yss", "./internal/cli", "./internal/format"]);
  assert.deepEqual(plan.race, []);
});

test("未知模板影响和显式全量要求不会静默跳过", () => {
  assert.equal(selectVerification({ paths: ["docs/source-lock.json"], sourcePaths: ["scripts/lib/approval-current.mjs"] }).scope, "full");
  const full = selectVerification({ paths: ["README.md"], forceFull: true });
  assert.equal(full.scope, "full");
  assert.deepEqual(full.go, [{ packages: ["./..."] }]);
  assert.deepEqual(full.race, []);
  assert.equal(selectVerification({ paths: ["README.md"] }).nativeRequired, false);
});

test("帮助实现不能按派生资产绕过核心，冻结 fixture 不冒充生产变化",()=>{assert.equal(selectVerification({paths:["internal/bundle/helpview.go"]}).scope,"full");assert.equal(selectVerification({paths:["docs/source-lock.json"],sourcePaths:["tests/fixtures/upstream-source/scripts/lib/approval-record.mjs"]}).scope,"impacted");});
test("批准业务输入只在 AST 证明变更边界时使用专项",()=>{const file="internal/governance/spec_baseline.go";assert.equal(selectVerification({paths:[file]}).scope,"full");assert.equal(selectVerification({paths:[file],goChanges:{[file]:{SharedChanged:false,Functions:["verifySpecBaselineSource"]}}}).scope,"impacted");assert.equal(selectVerification({paths:[file],goChanges:{[file]:{SharedChanged:false,Functions:["exportSpecBaseline"]}}}).scope,"full");});

test("导入、副作用或未证明的空函数变化不能选择专项",()=>{const file="internal/governance/spec_baseline.go";for(const change of [{SharedChanged:false,Functions:[]},{SharedChanged:true,Functions:["verifySpecBaselineSource"]}])assert.equal(selectVerification({paths:[file],goChanges:{[file]:change}}).scope,"full");});

test("前端当前候选绑定覆盖批准与直接消费者，未证明的共享变化回退全量",()=>{const file="internal/governance/frontend_implementation_semantic.go",plan=selectVerification({paths:[file],goChanges:{[file]:{SharedChanged:false,Functions:["frontendImplementationCurrent"]}}});assert.equal(plan.scope,"impacted");assert.deepEqual(plan.race,[]);for(const name of ["TestLocalFrontendSpecialistPublicAnalysisAndApprovedWorker","TestFrontendImplementationPlaceholdersUseWorkLayout","TestTaskFrontendDelivery","TestApprovalCurrent","TestReviewInput"])assert.ok(new RegExp(plan.go[0].run).test(name));for(const change of [{SharedChanged:false,Functions:[]},{SharedChanged:true,Functions:["frontendImplementationCurrent"]},{SharedChanged:false,Functions:["progressionFrontendImplementationCompletion"]}])assert.equal(selectVerification({paths:[file],goChanges:{[file]:change}}).scope,"full");});

test("登记的合同编译与本地 Plan Schema 消费者完整进入专项",()=>{const plan=selectVerification({sourcePaths:["scripts/lib/implementation-contract-compiler.mjs",".agents/skills/yss-implementation-contract-compiler/references/compiler-contract.yaml",".template-spec/process/schemas/lifecycle-registry.schema.json"]});assert.equal(plan.scope,"impacted");assert.deepEqual(plan.sourceSuites,["implementation-contracts","specialist-business-inputs"]);assert.equal(plan.nativeRequired,true);assert.deepEqual(plan.unknown,[]);});

test("共享前端边界及直接准备/派发调用者单独变化仍核验编译与派发",()=>{for(const name of ["frontend-delivery-boundary","slice-contract","slice-contract-preparation","slice-task-package","task-package"]){const plan=selectVerification({sourcePaths:[`scripts/lib/${name}.mjs`]});assert.ok(plan.sourceSuites.includes("implementation-contracts"),name);assert.equal(plan.nativeRequired,true);}});

test("核心目录下仅测试文件或 testdata 的变化只核验本包，不升为全量", () => {
  for (const [file, pkg] of [
    ["internal/governance/oracle_fixture_test.go", "./internal/governance"],
    ["internal/governance/testdata/oracle/approval.json", "./internal/governance"],
    ["internal/bundle/skill_lock_test.go", "./internal/bundle"],
    ["internal/updater/sub/deep_test.go", "./internal/updater/sub"],
    ["internal/project/testdata/fixtures/a/b.txt", "./internal/project"],
    ["tools/bundle/main_test.go", "./tools/bundle"],
    ["internal/transaction/journal_test.go", "./internal/transaction"],
    ["internal/domain/domain_test.go", "./internal/domain"],
  ]) {
    const plan = selectVerification({ paths: [file] });
    assert.equal(plan.scope, "impacted", file);
    assert.deepEqual(plan.go, [{ packages: [pkg] }], file);
    assert.deepEqual(plan.vet, [pkg], file);
    assert.ok(plan.reasons.some(reason => reason.includes("仅测试文件或 testdata")), file);
  }
});

test("测试文件与生产代码、go.mod 或未知路径一起变化仍然全量", () => {
  for (const mixed of [
    ["internal/governance/oracle_fixture_test.go", "internal/governance/approval_semantic.go"],
    ["internal/bundle/skill_lock_test.go", "internal/bundle/bundle.go"],
    ["internal/governance/oracle_fixture_test.go", "go.mod"],
    ["internal/governance/oracle_fixture_test.go", "unknown/runtime.conf"],
  ]) assert.equal(selectVerification({ paths: mixed }).scope, "full", mixed.join(" + "));
  assert.equal(selectVerification({ paths: ["go.sum"] }).scope, "full");
});

test("非核心目录的测试文件沿用原有的包加反向导入闭包", () => {
  const graph = { "./internal/format": [], "./internal/cli": ["./internal/format"] };
  const plan = selectVerification({ paths: ["internal/format/format_test.go"], importGraph: graph });
  assert.equal(plan.scope, "impacted");
  assert.deepEqual(plan.go[0].packages, ["./internal/cli", "./internal/format"]);
});

test("完整选择不跳过慢测试，--slow 仍可作诊断专项", () => {
  const [full] = goTestArgs({ packages: ["./..."] });
  assert.ok(full.includes(`-timeout=${PACKAGE_TIMEOUT}`) && PACKAGE_TIMEOUT !== "60m");
  assert.ok(!full.includes("-skip"));
  assert.ok(!full.includes("-run"));
  const [focused] = goTestArgs({ packages: ["./internal/governance"], run: "Approval|Task" });
  assert.equal(focused[focused.indexOf("-run") + 1], "Approval|Task");
  assert.ok(!focused.includes("-skip"));
  const slow = goTestArgs({}, { slow: true });
  assert.equal(slow.length, SLOW_TESTS.length);
  for (const [index, args] of slow.entries()) {
    assert.ok(args.includes(`-timeout=${SLOW_TIMEOUT}`));
    assert.equal(args.at(-1), SLOW_TESTS[index].package);
    assert.ok(!args.includes("-skip"));
    for (const name of SLOW_TESTS[index].names) assert.match(name, new RegExp(args[args.indexOf("-run") + 1]));
  }
});

test("独立生命周期分片的测试在源码里确实存在", () => {
  const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
  for (const item of SLOW_TESTS) {
    const directory = path.join(root, item.package);
    const source = fs.readdirSync(directory).filter(file => file.endsWith("_test.go")).map(file => fs.readFileSync(path.join(directory, file), "utf8")).join("\n");
    for (const name of item.names) assert.match(source, new RegExp(`func ${name}\\(t \\*testing\\.T\\)`), `${item.package} 缺少 ${name}`);
  }
});
