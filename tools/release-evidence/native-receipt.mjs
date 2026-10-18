#!/usr/bin/env node
// 本机程序安装范围的原生 receipt：只实际执行 version 与所有现役 bundle inspect；checks 为空，不冒充 smoke/recovery/plugin。
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { parseArgs } from 'node:util';
const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const { values } = parseArgs({ options: { binary: { type: 'string' }, out: { type: 'string' }, 'artifact-prefix': { type: 'string' }, commit: { type: 'string' }, 'cli-root': { type: 'string' }, 'template-commit': { type: 'string' }, 'binary-sha256': { type: 'string' }, version: { type: 'string' }, 'template-version': { type: 'string' } } });
assert.ok(values.binary && values.out && values['cli-root'] && values['artifact-prefix']);
const cliRoot = fs.realpathSync(values['cli-root']);
const git = (...args) => { const r = spawnSync('git', ['--no-optional-locks', '-C', cliRoot, ...args], { encoding: 'utf8' }); assert.equal(r.status, 0, r.stderr); return r.stdout.trim(); };
// 版本、模板提交与模板版本缺省取自仓库自身（domain.go 与 docs/source-lock.json）；显式传入时必须与仓库一致。
const repoVersion = fs.readFileSync(path.join(cliRoot, 'internal/domain/domain.go'), 'utf8').match(/^const Version = "([^"]+)"$/m)?.[1];
assert.ok(repoVersion, 'internal/domain/domain.go 中找不到 const Version');
const sourceLock = JSON.parse(fs.readFileSync(path.join(cliRoot, 'docs/source-lock.json'), 'utf8'));
assert.ok([2, 3, 5].includes(sourceLock.schemaVersion), 'unsupported committed source-lock schema');
const profiles = sourceLock.schemaVersion === 5 ? ['spec', 'backend'] : ['spec', 'design', 'backend'];
assert.deepEqual(Object.keys(sourceLock.profiles).sort(), [...profiles].sort());
const lockCommits = new Set(Object.values(sourceLock.profiles).map(p => p.templateCommit));
const lockVersions = new Set(Object.values(sourceLock.profiles).map(p => p.templateVersion));
assert.equal(lockCommits.size, 1, '所有现役 Profile 的模板提交必须一致'); assert.equal(lockVersions.size, 1, '所有现役 Profile 的模板版本必须一致');
values.commit ??= git('rev-parse', 'HEAD');
values['template-commit'] ??= [...lockCommits][0];
values['template-version'] ??= [...lockVersions][0];
values.version ??= repoVersion;
for (const key of ['commit','template-commit']) assert.match(values[key] || '', /^[a-f0-9]{40}$/);
assert.match(values['binary-sha256'] || '', /^[a-f0-9]{64}$/);
const state = () => ({ head: git('rev-parse', 'HEAD'), status: git('status', '--porcelain=v1', '--untracked-files=all'), files: git('ls-files', '--stage') });
const before = state();
assert.equal(before.head, values.commit); assert.equal(before.status, '');
const out = fs.realpathSync(values.out);
assert.ok(path.relative(cliRoot, out).startsWith('..'), 'evidence must be outside the CLI checkout');
const binary = fs.realpathSync(values.binary);
assert.ok(fs.lstatSync(binary).isFile() && !fs.lstatSync(binary).isSymbolicLink());
const platform = `${{ darwin: 'darwin', linux: 'linux', win32: 'windows' }[process.platform]}/${{ x64: 'amd64', arm64: 'arm64' }[process.arch]}`;
assert.equal(platform, 'darwin/arm64', 'actual supported release runner required');
assert.equal(values['artifact-prefix'], 'native-' + platform.replace('/', '-'));
assert.deepEqual(fs.readdirSync(out), []);
const initialHash = hash(fs.readFileSync(binary));
assert.equal(initialHash, values['binary-sha256']);
const expectedVersion = repoVersion;
assert.equal(expectedVersion, values.version);
const descriptor = file => ({ path: values['artifact-prefix'] + '/' + path.relative(out, file).split(path.sep).join('/'), sha256: hash(fs.readFileSync(file)) });
function execute(id, args) {
  const started = Date.now();
  const result = spawnSync(binary, args, { cwd: out, timeout: 300000, maxBuffer: 128 * 1024 * 1024, env: { ...process.env, NODE_OPTIONS: '', NODE_PATH: '' } });
  const stdout = path.join(out, id + '.stdout.log'), stderr = path.join(out, id + '.stderr.log');
  fs.writeFileSync(stdout, result.stdout || Buffer.alloc(0)); fs.writeFileSync(stderr, result.stderr || Buffer.alloc(0));
  assert.equal(result.error, undefined); assert.equal(result.signal, null); assert.equal(result.status, 0, result.stderr?.toString('utf8'));
  return { row: { id, command: [binary, ...args], status: 'passed', exitCode: 0, signal: null, inputDrift: false, unexecuted: [], durationMs: Date.now() - started, stdout: descriptor(stdout), stderr: descriptor(stderr) }, output: result.stdout };
}
const receipt = { schemaVersion: 1, platform, runtimePlatform: platform, cliCommit: values.commit, sourceState: 'committed', binarySha256: initialHash, binaryBeforeSha256: initialHash, status: 'running', exitCode: null, inputDrift: null, unexecuted: [], checks: [], runtimeVerification: 'native-runner' };
try {
  const version = execute('native-version', ['version', '--json']);
  const value = JSON.parse(version.output);
  assert.equal(value.status, 'ok'); assert.equal(value.code, 'OK'); assert.equal(value.protocolVersion, 1);
  assert.equal(value.result.cliCommit, values.commit); assert.equal(value.result.sourceState, 'committed'); assert.equal(value.result.version, expectedVersion);
  const versionFile = path.join(out, 'native-version.json'); fs.writeFileSync(versionFile, version.output);
  Object.assign(receipt, { cliVersion: value.result.version, protocolVersion: value.result.protocolVersion, version: descriptor(versionFile), versionCommand: version.row });
  const lockFile = path.join(cliRoot, 'docs/source-lock.json'), lock = sourceLock;
  receipt.sourceLockSha256 = hash(fs.readFileSync(lockFile));
  const gitSource = values['template-version'].startsWith('git:');
  for (const source of Object.values(lock.profiles)) {
    assert.equal(source.templateCommit, values['template-commit']); assert.equal(source.templateVersion, values['template-version']);
    if (gitSource) assert.ok(!source.templateTag, 'git 来源不得携带不同的标签'); else assert.equal(source.templateTag, 'v' + values['template-version']);
  }
  receipt.bundles = {}; receipt.bundleCommands = [];
  for (const profile of profiles) {
    const observed = execute('native-inspect-' + profile, ['bundle', 'inspect', '--profile', profile, '--json']);
    const envelope = JSON.parse(observed.output);
    assert.equal(envelope.status, 'ok'); assert.equal(envelope.code, 'OK'); assert.equal(envelope.protocolVersion, 1);
    const bundle = Object.fromEntries(['templateVersion', 'templateCommit', 'sourceState', 'sourceSnapshotHash', 'manifestHash', 'bundleHash'].map(k => [k, envelope.result[k]]));
    assert.equal(bundle.templateCommit, lock.profiles[profile].templateCommit); assert.equal(bundle.templateVersion, values['template-version']); assert.equal(bundle.sourceState, 'committed');
    for (const k of ['sourceSnapshotHash', 'manifestHash', 'bundleHash']) assert.match(bundle[k], /^[a-f0-9]{64}$/);
    const file = path.join(out, 'native-inspect-' + profile + '.json'); fs.writeFileSync(file, observed.output);
    receipt.bundles[profile] = bundle; receipt.bundleCommands.push({ ...observed.row, report: descriptor(file) });
  }
  receipt.status = 'passed'; receipt.exitCode = 0;
} catch (error) {
  receipt.status = 'failed'; receipt.exitCode = 1; receipt.error = error.stack || error.message; process.exitCode = 1;
} finally {
  receipt.binaryAfterSha256 = hash(fs.readFileSync(binary));
  receipt.inputDrift = receipt.binaryAfterSha256 !== initialHash || JSON.stringify(state()) !== JSON.stringify(before);
  if (receipt.inputDrift) { receipt.status = 'failed'; receipt.exitCode = 1; process.exitCode = 1; }
  fs.writeFileSync(path.join(out, 'native-receipt.json'), JSON.stringify(receipt, null, 2) + '\n');
  console.log(JSON.stringify({ status: receipt.status, platform, binarySha256: initialHash, checks: receipt.checks.length }));
}
