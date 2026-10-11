import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { runCommands } from "./verification-runner.mjs";

test("外层超时清理另建进程组的治理子进程", { skip: process.platform === "win32" }, async t => {
  const outDir = fs.mkdtempSync(path.join(os.tmpdir(), "yss-verification-detached-"));
  const pidFile = path.join(outDir, "child.pid");
  t.after(() => {
    if (fs.existsSync(pidFile)) { try { process.kill(-Number(fs.readFileSync(pidFile)), "SIGKILL"); } catch {} }
    fs.rmSync(outDir, { recursive: true, force: true });
  });
  const program = `const {spawn}=require('node:child_process');
    const child=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{detached:true,stdio:'ignore'});
    require('node:fs').writeFileSync(${JSON.stringify(pidFile)},String(child.pid));
    setInterval(()=>{},1000);`;
  const report = await runCommands([{ id: "detached", command: process.execPath, args: ["-e", program] }], {
    outDir, timeoutMs: 300, output: () => {},
  });
  assert.equal(report.commands[0].status, "timed_out");
  const pid = Number(fs.readFileSync(pidFile));
  // A reparented zombie has terminated; the container's init may reap it later.
  const { execFileSync } = await import("node:child_process");
  let state = "";
  try { state = execFileSync("ps", ["-p", String(pid), "-o", "stat="], { encoding: "utf8" }).trim(); } catch {}
  assert.ok(!state || state.startsWith("Z"), `detached child still running: ${state}`);
});

test("退出码为零但没有匹配测试也必须失败", async t => {
  const outDir = fs.mkdtempSync(path.join(os.tmpdir(), "yss-verification-"));
  t.after(() => fs.rmSync(outDir, { recursive: true, force: true }));
  const report = await runCommands([{ id: "empty", command: process.execPath, args: ["-e", ""], goJSON: true,
    expectedTests: [{ package: "example/unit", test: "TestRequired" }] }], { outDir, output: () => {} });
  assert.notEqual(report.exitCode, 0);
  assert.deepEqual(report.commands[0].missingTests, ["example/unit/TestRequired"]);
});

test("真实子进程失败保留事件、耗时与未执行命令，不报成功", async t => {
  const outDir = fs.mkdtempSync(path.join(os.tmpdir(), "yss-verification-"));
  t.after(() => fs.rmSync(outDir, { recursive: true, force: true }));
  const program = `
    console.log(JSON.stringify({Action:'run',Package:'example/unit',Test:'TestContract'}));
    console.log(JSON.stringify({Action:'fail',Package:'example/unit',Test:'TestContract',Elapsed:0.25}));
    console.error('fixture failure');
    process.exitCode = 7;
  `;
  const result = await runCommands([
    { id: "unit", command: process.execPath, args: ["-e", program], goJSON: true },
    { id: "later", command: process.execPath, args: ["-e", "process.exit(0)"] },
  ], { outDir, failFast: true, output: () => {} });
  assert.equal(result.exitCode, 7);
  assert.deepEqual(result.commands.map(row => row.status), ["failed", "unexecuted"]);
  assert.equal(result.tests[0].test, "TestContract");
  assert.equal(result.tests[0].status, "fail");
  assert.equal(result.tests[0].elapsed, 0.25);
  assert.match(fs.readFileSync(path.join(outDir, "commands.log"), "utf8"), /fixture failure/);
  assert.equal(fs.readFileSync(path.join(outDir, "test-events.jsonl"), "utf8").trim().split("\n").length, 2);
  assert.equal(JSON.parse(fs.readFileSync(path.join(outDir, "test-timings.json"))).exitCode, 7);
});

test("超时中止整组子进程并记录中断测试与心跳", { skip: process.platform === "win32" }, async t => {
  const outDir = fs.mkdtempSync(path.join(os.tmpdir(), "yss-verification-"));
  t.after(() => fs.rmSync(outDir, { recursive: true, force: true }));
  let output = "";
  const program = `
    const {spawn}=require('node:child_process');
    console.log(JSON.stringify({Action:'run',Package:'example/unit',Test:'TestWaiting'}));
    spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{stdio:'inherit'});
    setInterval(()=>{},1000);
  `;
  const report = await runCommands([{ id: "timeout", command: process.execPath, args: ["-e", program], goJSON: true }], {
    outDir, timeoutMs: 150, heartbeatMs: 20, output: text => { output += text; },
  });
  assert.notEqual(report.exitCode, 0);
  assert.equal(report.commands[0].status, "timed_out");
  assert.equal(report.tests[0].status, "interrupted");
  assert.match(output, /active=example\/unit\/TestWaiting/);
  assert.ok(report.commands[0].elapsed < 3, "descendant kept output pipes open");
});
