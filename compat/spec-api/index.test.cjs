"use strict";
const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const api = require("./index.cjs");

test("API v1 retains synchronous export and Error Envelope contracts", () => {
  assert.equal(api.API_VERSION, 1);
  for (const name of ["projectDoctor", "projectDiff", "templatePlan", "templateApply", "toErrorEnvelope"]) assert.equal(typeof api[name], "function");
  const cases = [["模板元数据无法解析", "YSS_METADATA_INVALID"], ["detached HEAD", "YSS_GIT_PROTECTED"], ["不支持的参数：--wat", "YSS_ARGUMENT_INVALID"], ["unexpected", "YSS_COMMAND_FAILED"]];
  for (const [message, code] of cases) assert.deepEqual(api.toErrorEnvelope(new Error(message)), { schemaVersion: 1, ok: false, error: { code, message } });
  assert.equal(api.toErrorEnvelope(Object.assign(new Error("custom"), { code: "YSS_CUSTOM_FAILURE" })).error.code, "YSS_CUSTOM_FAILURE");
});
test("missing binary throws a normalized error and cannot silently succeed", () => {
  const previous = process.env.YSS_BINARY;
  process.env.YSS_BINARY = path.join(os.tmpdir(), "yss-missing-binary-" + process.pid);
  try { assert.throws(() => api.projectDoctor(), error => error.code === "YSS_RUNTIME_UNAVAILABLE" && api.toErrorEnvelope(error).schemaVersion === 1); }
  finally { if (previous === undefined) delete process.env.YSS_BINARY; else process.env.YSS_BINARY = previous; }
});
test("a different executable cannot silently supply a valid API result", () => {
  const previous = process.env.YSS_BINARY;
  process.env.YSS_BINARY = process.execPath;
  try { assert.throws(() => api.projectDoctor(), error => error.code === "YSS_PROTOCOL_MISMATCH"); }
  finally { if (previous === undefined) delete process.env.YSS_BINARY; else process.env.YSS_BINARY = previous; }
});
test("real Go subprocess refuses uncovered legacy semantics synchronously", { skip: !process.env.YSS_COMPAT_API_HELPER }, () => {
  for (const name of ["projectDoctor", "projectDiff", "templatePlan", "templateApply"]) {
    assert.throws(() => api[name]({ targetDir: ".", force: true, prune: true }), error => error.code === "YSS_UNPORTED" && error.envelope.schemaVersion === 1 && error.envelope.ok === false);
  }
});
test("stable snapshot API exposes fixed-source provenance for all four profiles", { skip: !process.env.YSS_COMPAT_API_HELPER }, () => {
  for (const profile of ["spec", "design", "backend", "frontend"]) {
    const envelope = api.native.snapshot(profile);
    assert.equal(envelope.profile, profile);
    assert.equal(envelope.result.profile, profile);
    assert.equal(envelope.result.schemaVersion, 1);
    assert.equal(envelope.result.sourceState, "committed");
    assert.match(envelope.result.templateCommit, /^[a-f0-9]{40}$/);
    assert.match(envelope.result.snapshotHash, /^[a-f0-9]{64}$/);
    assert.match(envelope.result.manifestHash, /^[a-f0-9]{64}$/);
  }
});
test("explicit native namespace uses a real Go read-only plan and errors", { skip: !process.env.YSS_COMPAT_API_HELPER }, () => {
  const base = fs.realpathSync(fs.mkdtempSync(path.join(os.tmpdir(), "yss-js-api-")));
  const target = path.join(base, "project");
  try {
    const result = api.native.run({ profile: "spec", args: ["init", "--target-dir", target, "--project-name", "JS bridge", "--business-domain", "Data", "--plan"] });
    assert.equal(result.protocolVersion, 1);
    assert.equal(result.status, "ok");
    assert.equal(result.result.command, "init");
    assert.equal(fs.existsSync(target), false);
    assert.throws(() => api.native.run({ profile: "backend", args: ["init", "--target-dir", target, "--force"] }), error => error.code === "YSS_UNPORTED");
  } finally { fs.rmSync(base, { recursive: true, force: true }); }
});
