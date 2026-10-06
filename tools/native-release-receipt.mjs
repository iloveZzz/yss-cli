#!/usr/bin/env node
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { parseArgs } from 'node:util';

const hash = bytes => createHash('sha256').update(bytes).digest('hex');
const { values } = parseArgs({ options: { binary: { type: 'string' }, 'template-root': { type: 'string' }, out: { type: 'string' }, 'artifact-prefix': { type: 'string' }, commit: { type: 'string' } } });
assert.ok(values.binary && values.out && values['template-root'] && values['artifact-prefix'] && /^[a-f0-9]{40}$/.test(values.commit || ''));
const binary = fs.realpathSync(values.binary), out = fs.realpathSync(values.out), template = fs.realpathSync(values['template-root']);
const platform = `${{ darwin: 'darwin', linux: 'linux', win32: 'windows' }[process.platform]}/${{ x64: 'amd64', arm64: 'arm64' }[process.arch]}`;
assert.equal(values['artifact-prefix'], 'native-' + platform.replace('/', '-'));
assert.equal(fs.existsSync(path.join(out, 'native-receipt.json')), false);
const initialHash = hash(fs.readFileSync(binary)), checks = [];
const descriptor = file => {
  const rel = path.relative(out, file).split(path.sep).join('/');
  assert.ok(rel && !rel.startsWith('../') && !path.isAbsolute(rel));
  assert.ok(fs.lstatSync(file).isFile() && !fs.lstatSync(file).isSymbolicLink());
  return { path: values['artifact-prefix'] + '/' + rel, sha256: hash(fs.readFileSync(file)) };
};
function execute(id, executable, args) {
  const started = Date.now();
  const result = spawnSync(executable, args, { timeout: 900000, maxBuffer: 128 * 1024 * 1024, env: { ...process.env, NODE_OPTIONS: '', NODE_PATH: '' } });
  const stdout = path.join(out, id + '.stdout.log'), stderr = path.join(out, id + '.stderr.log');
  fs.writeFileSync(stdout, result.stdout || Buffer.alloc(0)); fs.writeFileSync(stderr, result.stderr || Buffer.alloc(0));
  const row = { id, command: [executable, ...args], status: result.status === 0 && !result.signal && !result.error ? 'passed' : 'failed', exitCode: result.status, signal: result.signal, inputDrift: false, unexecuted: [], durationMs: Date.now() - started, stdout: descriptor(stdout), stderr: descriptor(stderr) };
  assert.equal(result.error, undefined, `${id}: ${result.error?.message}`); assert.equal(result.signal, null, `${id}: ${result.signal}`); assert.equal(result.status, 0, `${id}: ${result.stderr?.toString('utf8')}`);
  return { row, output: result.stdout };
}
const receipt = { schemaVersion: 1, platform, runtimePlatform: platform, cliCommit: values.commit, sourceState: 'committed', binarySha256: initialHash, binaryBeforeSha256: initialHash, status: 'running', exitCode: null, inputDrift: null, unexecuted: ['native-smoke', 'native-recovery', 'plugin-native-smoke'], checks, githubRunId: process.env.GITHUB_RUN_ID, githubRunAttempt: process.env.GITHUB_RUN_ATTEMPT, runtimeVerification: 'native-runner' };
try {
  const version = execute('native-version', binary, ['version', '--json']);
  const value = JSON.parse(version.output); assert.equal(value.status, 'ok'); assert.equal(value.code, 'OK'); assert.equal(value.protocolVersion, 1);
  assert.equal(value.result.cliCommit, values.commit); assert.equal(value.result.sourceState, 'committed'); assert.equal(value.result.version, '1.0.0');
  const versionFile = path.join(out, 'native-version.json'); fs.writeFileSync(versionFile, version.output);
  Object.assign(receipt, { cliVersion: value.result.version, protocolVersion: value.result.protocolVersion, version: descriptor(versionFile), versionCommand: version.row });
  const lockFile = path.resolve('docs/source-lock.json');
  const lock = JSON.parse(fs.readFileSync(lockFile));
  receipt.sourceLockSha256 = hash(fs.readFileSync(lockFile));
  receipt.bundles = {};
  receipt.bundleCommands = [];
  for (const profile of ['spec', 'design', 'backend', 'frontend']) {
    const observed = execute('native-inspect-' + profile, binary, ['bundle', 'inspect', '--profile', profile, '--json']);
    const envelope = JSON.parse(observed.output);
    assert.equal(envelope.status, 'ok'); assert.equal(envelope.code, 'OK'); assert.equal(envelope.protocolVersion, 1);
    const bundle = Object.fromEntries(['templateVersion', 'templateCommit', 'sourceState', 'sourceSnapshotHash', 'manifestHash', 'bundleHash'].map(key => [key, envelope.result[key]]));
    assert.equal(bundle.templateCommit, lock.profiles[profile].templateCommit); assert.equal(bundle.templateVersion, lock.profiles[profile].templateVersion); assert.equal(bundle.sourceState, 'committed');
    for (const key of ['sourceSnapshotHash', 'manifestHash', 'bundleHash']) assert.match(bundle[key], /^[a-f0-9]{64}$/);
    const file = path.join(out, 'native-inspect-' + profile + '.json'); fs.writeFileSync(file, observed.output);
    receipt.bundles[profile] = bundle; receipt.bundleCommands.push({ ...observed.row, report: descriptor(file) });
  }
  const plans = [
    ['native-smoke', 'go', ['run', './tools/native-smoke', binary, path.join(out, 'yss-native-smoke')], 'yss-native-smoke'],
    ['native-recovery', 'go', ['run', './tools/native-recovery', binary, path.join(out, 'yss-native-recovery')], 'yss-native-recovery'],
    ['plugin-native-smoke', process.execPath, ['tools/plugin-native-smoke.mjs', '--binary', binary, '--binary-sha256', initialHash, '--template-root', template, '--out', path.join(out, 'yss-plugin-native-smoke')], 'yss-plugin-native-smoke'],
  ];
  for (const [id, executable, args, folder] of plans) {
    const result = execute(id, executable, args), file = path.join(out, folder, 'report.json'), report = JSON.parse(fs.readFileSync(file));
    assert.equal(report.status, 'passed'); assert.equal(report.input_drift, false); assert.equal(report.binary_sha256, initialHash);
    if (id === 'native-smoke') {
      assert.equal(report.platform, platform); assert.equal(report.results.length, 82); assert.ok(report.results.every(row => row.exitCode === 0 && row.status === 'ok'));
      for (const row of report.results) assert.equal(hash(fs.readFileSync(path.join(out, folder, row.envelopeRef))), row.sha256);
    } else if (id === 'native-recovery') {
      assert.equal(report.platform, platform); assert.equal(report.unexecuted.length, 0); assert.equal(report.cases.length, process.platform === 'win32' ? 8 : 12);
      assert.ok(report.cases.every(row => row.status === 'passed' && row.observed_after_target_mutation && row.binding_and_identity_restored_together && row.repeated_recovery));
    } else {
      assert.equal(report.binary_drift, false); assert.equal(report.cases.length, 2); assert.ok(report.cases.every(row => row.status === 'passed'));
      assert.deepEqual(report.cases.map(row => row.profile).sort(), ['design', 'spec']);
      const labels = ['go-head', 'go-tree', 'template-head', 'template-clean', 'go-before-tracked', 'go-before-files', 'template-before-tracked', 'template-before-files', 'binary-version', 'python-jsonschema', 'go-after-tracked', 'go-after-files', 'template-after-tracked', 'template-after-files'];
      for (const profile of ['spec', 'design']) for (const command of ['inspect', 'build', 'staged-inspect', 'verify', 'doctor', 'preview', 'apply', 'binding-check', 'readonly-project-recover', 'readonly-project-rollback', 'whole-rollback', 'repeat-project-rollback', 'repeat-project-recover', 'repeat-project-rollback', 'repeat-project-recover']) labels.push(profile + '-' + command);
      assert.deepEqual(report.commands.map(row => row.label).sort(), labels.sort(), 'plugin public acceptance requires all 44 observed commands');
      assert.ok(report.commands.every(row => row.exit_observed === true && row.exit_code === 0 && row.signal === null && row.error === null));
      assert.equal(report.source_lock_sha256, receipt.sourceLockSha256); assert.equal(report.go_head, receipt.cliCommit); assert.equal(report.template_head, receipt.bundles.spec.templateCommit);
      for (const key of ['version', 'protocolVersion', 'cliCommit', 'sourceState']) assert.equal(report.binary_version[key], value.result[key]);
      for (const source of ['go', 'template']) {
        assert.match(report.inputs_before[source].sha256, /^[a-f0-9]{64}$/);
        assert.deepEqual(report.inputs_after[source], report.inputs_before[source]);
      }
      for (const item of report.cases) {
        for (const key of ['readonly_preview', 'readonly_recovery', 'whole_atomic_rollback', 'repeated_recovery']) assert.equal(item[key], true);
        assert.ok(typeof item.init_plan_id === 'string' && item.init_plan_id.length > 0);
        for (const key of ['installed_metadata', 'installed_binding']) { assert.equal(item[key].type, 'file'); assert.match(item[key].sha256, /^[a-f0-9]{64}$/); }
        assert.deepEqual(item.business_after, item.business_before);
        assert.deepEqual(item.business_before.map(row => row.ref).sort(), ['.github/workflows/user-owned-readonly.yml', 'src/user-owned/readonly.txt']);
        for (const row of item.business_before) { assert.equal(row.type, 'file'); assert.equal(row.mode, 0o444); assert.match(row.sha256, /^[a-f0-9]{64}$/); }
      }
      for (const row of report.commands) for (const stream of ['stdout', 'stderr']) assert.equal(hash(fs.readFileSync(path.join(out, folder, row[stream].file))), row[stream].sha256);
    }
    checks.push({ ...result.row, report: descriptor(file) }); receipt.unexecuted = receipt.unexecuted.filter(item => item !== id);
  }
  receipt.binaryAfterSha256 = hash(fs.readFileSync(binary)); receipt.inputDrift = receipt.binaryAfterSha256 !== initialHash; assert.equal(receipt.inputDrift, false);
  receipt.status = 'passed'; receipt.exitCode = 0;
} catch (error) {
  receipt.status = 'failed'; receipt.exitCode = 1; receipt.error = error.stack || error.message; process.exitCode = 1;
} finally {
  receipt.binaryAfterSha256 = hash(fs.readFileSync(binary)); receipt.inputDrift = receipt.binaryAfterSha256 !== initialHash;
  if (receipt.inputDrift) { receipt.status = 'failed'; receipt.exitCode = 1; process.exitCode = 1; }
  fs.writeFileSync(path.join(out, 'native-receipt.json'), JSON.stringify(receipt, null, 2) + '\n');
  console.log(JSON.stringify({ status: receipt.status, platform, binarySha256: initialHash, checks: checks.length, unexecuted: receipt.unexecuted }));
}
