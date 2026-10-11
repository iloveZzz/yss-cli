import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { assignTasks, assertCoverage, SLOW_TESTS, buildTaskManifest, executeTask, collectReports, discoverTests } from "./verification-tasks.mjs";

test("真实 Go 发现到执行与汇总闭环拒绝空报告、遗漏、重复和输入漂移", async t => {
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), "yss-task-contract-"));
  const outDir = fs.mkdtempSync(path.join(os.tmpdir(), "yss-task-report-"));
  const fixture = path.join(outDir, "reading.json");
  fs.writeFileSync(fixture, "original fixture");
  const previousFixture = process.env.YSS_READING_ORACLE_FIXTURE;
  process.env.YSS_READING_ORACLE_FIXTURE = fixture;
  t.after(() => { if (previousFixture === undefined) delete process.env.YSS_READING_ORACLE_FIXTURE; else process.env.YSS_READING_ORACLE_FIXTURE = previousFixture; });
  const previousReport = process.env.YSS_PROFILE_GUIDANCE_REPORT;
  process.env.YSS_PROFILE_GUIDANCE_REPORT = path.join(outDir, "generated-report.json");
  t.after(() => { if (previousReport === undefined) delete process.env.YSS_PROFILE_GUIDANCE_REPORT; else process.env.YSS_PROFILE_GUIDANCE_REPORT = previousReport; });
  t.after(() => { fs.rmSync(cwd, { recursive: true, force: true }); fs.rmSync(outDir, { recursive: true, force: true }); });
  fs.writeFileSync(path.join(cwd, "go.mod"), "module example.com/contract\n\ngo 1.24\n");
  fs.writeFileSync(path.join(cwd, "contract_test.go"), 'package contract\nimport ("testing";"os")\nfunc TestRuns(t *testing.T) { if err:=os.WriteFile(os.Getenv("YSS_PROFILE_GUIDANCE_REPORT"),[]byte("report"),0600);err!=nil {t.Fatal(err)} }\nfunc TestOptional(t *testing.T) { t.Skip("recorded optional test") }\n');
  fs.mkdirSync(path.join(cwd, "docs"));
  fs.writeFileSync(path.join(cwd, "docs/source-lock.json"), "{}");
  const git = args => execFileSync("git", args, { cwd, stdio: "pipe" });
  git(["init"]); git(["add", "."]); git(["-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-m", "fixture"]);
  const manifest = buildTaskManifest({ go: [{ packages: ["./..."] }, { packages: ["."], run: "TestRuns" }] }, [], { cwd });
  assert.equal(manifest.inventory[0].tests.length, 2);
  const report = await executeTask(manifest, "checks", { cwd, outDir });
  assert.equal(collectReports(manifest, [report]).skipped[0].test, "TestOptional");
  assert.throws(() => collectReports(manifest, []), /缺失/);
  assert.throws(() => collectReports(manifest, [report, report]), /重复/);
  assert.throws(() => collectReports(manifest, [{ ...report, commands: [] }]), /未通过/);
  assert.throws(() => collectReports(manifest, [{ ...report, tests: [] }]), /未完成/);
  assert.throws(() => discoverTests([{ packages: ["."], run: "NoMatch" }], cwd), /未匹配/);
  const previousFlags = process.env.GOFLAGS;
  process.env.GOFLAGS = "-tags=changed_verification_inputs";
  try { await assert.rejects(() => executeTask(manifest, "checks", { cwd, outDir }), /重新生成/); }
  finally { if (previousFlags === undefined) delete process.env.GOFLAGS; else process.env.GOFLAGS = previousFlags; }
  fs.writeFileSync(fixture, "changed fixture");
  await assert.rejects(() => executeTask(manifest, "checks", { cwd, outDir }), /重新生成/);
  fs.writeFileSync(fixture, "original fixture");
  fs.appendFileSync(path.join(cwd, "contract_test.go"), "// changed\n");
  await assert.rejects(() => executeTask(manifest, "checks", { cwd, outDir }), /重新生成/);
});

test("全量分片包含生命周期，重叠选择去重且每项测试恰好执行一次", () => {
  const inventory = [
    { package: "./internal/cli", importPath: "example/internal/cli", tests: ["TestHelp", "ExampleVersion"] },
    { package: "./internal/project", importPath: "example/internal/project", tests: ["TestInstall"] },
    { package: "./internal/governance", importPath: "example/internal/governance", tests: ["TestApproval", "TestTask", "TestSource", ...SLOW_TESTS[0].names] },
  ];
  const tasks = assignTasks(inventory, []);
  assertCoverage(inventory, tasks);
  assert.ok(tasks.find(task => task.id === "lifecycle").commands.some(c => c.expectedTests.some(t => t.test === SLOW_TESTS[0].names[0])));
  assert.ok(tasks.filter(task => task.id.startsWith("governance-")).length > 1);
  const duplicate = structuredClone(tasks);
  duplicate[0].commands.push(tasks.find(t => t.id === "cli").commands[0]);
  assert.throws(() => assertCoverage(inventory, duplicate), /重复/);
  assert.throws(() => assertCoverage(inventory, tasks.filter(t => t.id !== "lifecycle")), /遗漏/);
});
