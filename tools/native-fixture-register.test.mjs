import test from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { execFileSync } from "node:child_process";
import { fileURLToPath, pathToFileURL } from "node:url";
import { bindNativeFixtureSource } from "./native-fixture-register.mjs";

for (const aliasRoot of [false, true]) test(`仅固定 fixture 的生成器绑定 nativeSeed，磁盘来源保持原样 (${aliasRoot ? "符号链接来源" : "原路径"})`, t => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "yss-fixture-adapter-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const source = `const nativeSeed='native-root',family='backend';
    const platformSourceFingerprint=(family,root='oracle-root')=>root;
    export default {source_fingerprint:platformSourceFingerprint(family)};`;
  const fixture = path.join(root, "scripts/fixtures/existing-backend/fixture.mjs");
  fs.mkdirSync(path.dirname(fixture), { recursive: true });
  fs.writeFileSync(fixture, source);
  const ordinary = path.join(root, "ordinary.mjs");
  fs.writeFileSync(ordinary, source);
  const oracleRoot = aliasRoot ? root + "-alias" : root;
  if (aliasRoot) {
    fs.symlinkSync(root, oracleRoot, process.platform === "win32" ? "junction" : "dir");
    t.after(() => fs.rmSync(oracleRoot, { force: true }));
  }
  const adapter = fileURLToPath(new URL("./native-fixture-register.mjs", import.meta.url));
  const run = file => execFileSync(process.execPath, ["--import", adapter, "--input-type=module", "-e",
    `import data from ${JSON.stringify(pathToFileURL(file).href)}; console.log(data.source_fingerprint)`], {
    env: { ...process.env, YSS_LEGACY_ORACLE_ROOT: oracleRoot }, encoding: "utf8",
  }).trim();
  assert.equal(run(fixture), "native-root");
  assert.equal(run(ordinary), "oracle-root");
  assert.equal(fs.readFileSync(fixture, "utf8"), source);
  assert.throws(() => bindNativeFixtureSource("upstream changed"), /source changed/);
  assert.throws(() => bindNativeFixtureSource(source + source), /source changed/);
});
