#!/usr/bin/env node
// Real public-entry acceptance on the current runner; no cross-platform claims.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { createHash } from 'node:crypto';
import { spawnSync } from 'node:child_process';
import { parseArgs } from 'node:util';
import { fileURLToPath, pathToFileURL } from 'node:url';

const sha = value => createHash('sha256').update(value).digest('hex');
const now = () => new Date().toISOString();
const mode = value => process.platform === 'win32' ? (value & 0o200 ? 0o644 : 0o444) : value & 0o777;
const within = (file, root) => { const relative = path.relative(root, file); return !path.isAbsolute(relative) && relative !== '..' && !relative.startsWith('..' + path.sep); };
const exists = file => { try { fs.lstatSync(file); return true; } catch (error) { if (error.code === 'ENOENT') return false; throw error; } };
const descriptor = file => { const stat = fs.lstatSync(file); assert.ok(!stat.isSymbolicLink()); return { type: stat.isFile() ? 'file' : stat.isDirectory() ? 'directory' : 'unsupported',
  mode: mode(stat.mode), observed_mode: stat.mode & 0o777, ...(stat.isFile() ? { sha256: sha(fs.readFileSync(file)) } : {}) }; };

async function main() {
  const { values } = parseArgs({ options: { binary: { type: 'string' }, 'binary-sha256': { type: 'string' }, 'template-root': { type: 'string' }, out: { type: 'string' } } });
  assert.ok(values.binary && values['template-root'] && values.out, 'usage: --binary <fixed executable> --template-root <fixed clean checkout> --out <new directory> [--binary-sha256 <fixed digest>]');
  const binary = fs.realpathSync(path.resolve(values.binary)), template = fs.realpathSync(path.resolve(values['template-root']));
  const goRoot = fs.realpathSync(path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..'));
  const proposed = path.resolve(values.out), out = path.join(fs.realpathSync(path.dirname(proposed)), path.basename(proposed));
  assert.equal(exists(out), false, 'output directory must not exist');
  assert.equal(within(out, template) || within(out, goRoot) || within(template, out) || within(goRoot, out), false, 'output must be separate from both source checkouts');
  assert.equal(descriptor(binary).type, 'file'); assert.equal(descriptor(template).type, 'directory');
  const binaryHash = sha(fs.readFileSync(binary));
  if (values['binary-sha256']) assert.equal(binaryHash, values['binary-sha256'], 'fixed binary digest mismatch');
  fs.mkdirSync(out);
  const report = { schemaVersion: 1, kind: 'native-plugin-public-smoke', status: 'running', started_at: now(), finished_at: null,
    platform: { os: process.platform, arch: process.arch, node: process.version }, binary, binary_sha256: binaryHash, template_root: template, go_root: goRoot,
    mode_semantics: process.platform === 'win32' ? 'owner-write/read-only attribute; ACL verification unexecuted' : 'exact POSIX 0777 permissions',
    commands: [], cases: [], input_drift: null, binary_drift: null, release_ready: false,
    unexecuted: ['other-platform runner execution', 'Windows ACL verification', 'installed Codex session and provider discovery', 'real product delivery', 'publication'] };
  const save = () => fs.writeFileSync(path.join(out, 'report.json'), JSON.stringify(report, null, 2) + '\n');
  save();
  function run(label, executable, args, { cwd = goRoot, json = true, native = false } = {}) {
    const row = { label, executable, args, cwd, started_at: now(), finished_at: null, expected_exit_code: 0, exit_observed: false };
    report.commands.push(row); save();
    const result = spawnSync(executable, args, { cwd, timeout: 300000, maxBuffer: 128 * 1024 * 1024,
      env: { ...process.env, NODE_OPTIONS: '', NODE_PATH: '' } });
    const prefix = String(report.commands.length).padStart(3, '0') + '-' + label;
    for (const stream of ['stdout', 'stderr']) {
      const raw = result[stream] || Buffer.alloc(0), file = prefix + '.' + stream + '.log'; fs.writeFileSync(path.join(out, file), raw);
      row[stream] = { file, sha256: sha(raw), bytes: Buffer.byteLength(raw) };
    }
    Object.assign(row, { finished_at: now(), exit_observed: result.status !== null, exit_code: result.status, signal: result.signal, error: result.error?.message || null }); save();
    assert.equal(result.error, undefined, `${label}: ${result.error?.message}`);
    assert.equal(result.signal, null, `${label}: interrupted`);
    assert.equal(result.status, 0, `${label}: ${(result.stderr?.length ? result.stderr : result.stdout)?.toString('utf8')}`);
    if (!json) return result.stdout.toString('utf8');
    const output = JSON.parse(result.stdout.toString('utf8'));
    if (native) { assert.equal(output.outputVersion, 1); assert.equal(output.protocolVersion, 1); assert.equal(output.status, 'ok'); assert.equal(output.code, 'OK'); }
    return output;
  }
  function inputs(root, label) {
    const tracked = new Set(run(label + '-tracked', 'git', ['ls-files', '-z', '--cached'], { cwd: root, json: false }).split('\0').filter(Boolean));
    const refs = [...new Set(run(label + '-files', 'git', ['ls-files', '-z', '--cached', '--others', '--exclude-standard'], { cwd: root, json: false }).split('\0').filter(Boolean))].sort();
    const rows = refs.filter(ref => path.resolve(root, ref) !== binary).map(ref => {
      const file = path.join(root, ref);
      if (!exists(file)) {
        assert.ok(tracked.has(ref), 'untracked source disappeared: ' + ref);
        return { ref, type: 'missing' };
      }
      const stat = fs.lstatSync(file);
      if (stat.isSymbolicLink()) {
        // Canonical tracked Skill projections are source inputs. Record only
        // the link itself; project/asset/binary descriptors still reject links.
        const target = fs.readlinkSync(file, { encoding: 'buffer' }), parts = ref.split('/');
        assert.ok(tracked.has(ref) && parts.length === 3 && parts[0].startsWith('.') && parts[1] === 'skills', 'unexpected source symlink: ' + ref);
        assert.equal(target.toString('utf8'), '../../.agents/skills/' + parts[2], 'noncanonical Skill projection: ' + ref);
        return { ref, type: 'symlink', mode: mode(stat.mode), observed_mode: stat.mode & 0o777, link_target: target.toString('utf8'), sha256: sha(target) };
      }
      const item = { ref, ...descriptor(file) }; assert.notEqual(item.type, 'unsupported'); return item;
    });
    return { sha256: sha(JSON.stringify(rows)), files: rows.length, inventory: rows };
  }
  function inventory(root, prefix = '') {
    return fs.readdirSync(path.join(root, prefix)).sort().flatMap(name => {
      const ref = prefix ? prefix + '/' + name : name, item = { ref, ...descriptor(path.join(root, ref)) };
      assert.notEqual(item.type, 'unsupported');
      return item.type === 'directory' ? [item, ...inventory(root, ref)] : [item];
    });
  }
  let initial = null;
  try {
    const sourceLockBytes = fs.readFileSync(path.join(goRoot, 'docs/source-lock.json'));
    const sourceLock = JSON.parse(sourceLockBytes); report.source_lock_sha256 = sha(sourceLockBytes);
    report.go_head = run('go-head', 'git', ['rev-parse', 'HEAD'], { json: false }).trim();
    report.go_tree = run('go-tree', 'git', ['rev-parse', 'HEAD^{tree}'], { json: false }).trim();
    report.template_head = run('template-head', 'git', ['rev-parse', 'HEAD'], { cwd: template, json: false }).trim();
    assert.match(sourceLock.profiles.spec.templateCommit, /^[a-f0-9]{40}$/);
    assert.equal(report.template_head, sourceLock.profiles.spec.templateCommit, 'template checkout must match fixed source-lock spec commit');
    assert.equal(run('template-clean', 'git', ['status', '--porcelain', '--untracked-files=all'], { cwd: template, json: false }).trim(), '', 'template checkout must be clean');
    const { parseDocument } = await import(pathToFileURL(path.join(template, 'scripts/vendor/yaml.mjs')).href);
    const identity = parseDocument(fs.readFileSync(path.join(template, 'yss-project.yaml'), 'utf8'), { uniqueKeys: true });
    assert.equal(identity.errors.length, 0); const sourceIdentity = identity.toJS({ maxAliasCount: 0 });
    assert.equal(sourceIdentity.schema_version, 1); assert.equal(sourceIdentity.repository_mode, 'template-source');
    initial = { go: inputs(goRoot, 'go-before'), template: inputs(template, 'template-before') };
    report.inputs_before = initial; save();
    report.binary_version = run('binary-version', binary, ['version', '--json'], { native: true }).result;
    run('python-jsonschema', 'python3', ['-c', 'import sys,jsonschema,json; print(json.dumps({"version":sys.version.split()[0],"jsonschema":jsonschema.__version__}))']);
    for (const [profile, name, receipt] of [['spec', 'yss-backend-delivery', '.yss-backend-plugin.json'], ['design', 'yss-product-design', '.yss-product-design-plugin.json']]) {
      const parent = path.join(out, profile); fs.mkdirSync(parent);
      const plugin = path.join(parent, name), target = path.join(parent, 'project'), planFile = path.join(parent, 'saved-plugin-plan.json');
      const cli = (label, args) => run(profile + '-' + label, process.execPath, [path.join(plugin, 'scripts/plugin.mjs'), ...args]);
      const checked = run(profile + '-inspect', binary, ['bundle', 'inspect', '--profile', profile, '--json'], { native: true }).result;
      assert.equal(checked.sourceState, 'committed'); assert.equal(checked.templateCommit, sourceLock.profiles[profile].templateCommit);
      const built = run(profile + '-build', process.execPath, [path.join(template, '.template-source/plugins', name, 'build.mjs'), '--binary', binary, '--output', plugin]);
      assert.equal(built.result, 'built'); assert.equal(built.native.binarySha256, binaryHash);
      const expectedPath = process.platform === 'win32' ? 'assets/tool/yss.exe' : 'assets/tool/yss'; assert.equal(built.native.binaryPath, expectedPath);
      const staged = run(profile + '-staged-inspect', path.join(plugin, expectedPath), ['bundle', 'inspect', '--profile', profile, '--json'], { native: true }).result;
      assert.equal(staged.bundleHash, checked.bundleHash); assert.equal(staged.templateCommit, checked.templateCommit);
      assert.equal(cli('verify', ['verify']).result, 'verified'); assert.equal(cli('doctor', ['doctor']).result, 'diagnostics-passed');
      const plan = cli('preview', ['project-plan', '--target-dir', target, '--project-name', 'Native plugin smoke', '--business-domain', '治理验收', '--issue-tracker', 'github']);
      assert.equal(exists(target), false, 'preview must not create a target'); assert.equal(plan.schema_version, 2);
      fs.writeFileSync(planFile, JSON.stringify(plan));
      assert.equal(cli('apply', ['project-apply', '--plan', planFile]).result, 'applied');
      assert.equal(cli('binding-check', ['project-check', '--target-dir', target]).result, 'binding-matched');
      const metadata = descriptor(path.join(target, '.yss.json')), binding = descriptor(path.join(target, receipt));
      const businesses = ['src/user-owned/readonly.txt', '.github/workflows/user-owned-readonly.yml'];
      for (const ref of businesses) {
        const file = path.join(target, ref); assert.equal(exists(file), false); fs.mkdirSync(path.dirname(file), { recursive: true });
        fs.writeFileSync(file, 'User-owned bytes must survive native rollback.\n', { flag: 'wx' }); fs.chmodSync(file, 0o444);
      }
      const businessBefore = businesses.map(ref => ({ ref, ...descriptor(path.join(target, ref)) }));
      for (const command of ['project-recover', 'project-rollback']) {
        const before = inventory(target); cli('readonly-' + command, [command, '--target-dir', target]);
        assert.deepEqual(inventory(target), before, 'default recovery/rollback must not change any bytes or representable modes');
      }
      cli('whole-rollback', ['project-rollback', '--target-dir', target, '--apply']);
      for (const ref of ['.yss.json', receipt, ...(profile === 'spec' ? ['.yss-execution-scope.yaml'] : [])]) assert.equal(exists(path.join(target, ref)), false, 'whole rollback must remove identity/binding/scope together');
      assert.deepEqual(businesses.map(ref => ({ ref, ...descriptor(path.join(target, ref)) })), businessBefore);
      for (const command of ['project-rollback', 'project-recover', 'project-rollback', 'project-recover']) {
        const before = inventory(target); cli('repeat-' + command, [command, '--target-dir', target, '--apply']);
        assert.deepEqual(inventory(target), before, 'repeated completed rollback/recovery must preserve the full inventory');
        assert.equal(exists(path.join(target, '.yss.json')), false); assert.equal(exists(path.join(target, receipt)), false);
      }
      report.cases.push({ profile, plugin: name, status: 'passed', init_plan_id: plan.plan_id, installed_metadata: metadata, installed_binding: binding,
        business_before: businessBefore, business_after: businesses.map(ref => ({ ref, ...descriptor(path.join(target, ref)) })),
        readonly_preview: true, readonly_recovery: true, whole_atomic_rollback: true, repeated_recovery: true }); save();
    }
    report.inputs_after = { go: inputs(goRoot, 'go-after'), template: inputs(template, 'template-after') };
    report.input_drift = initial.go.sha256 !== report.inputs_after.go.sha256 || initial.template.sha256 !== report.inputs_after.template.sha256;
    try { report.binary_after_sha256 = sha(fs.readFileSync(binary)); report.binary_drift = report.binary_after_sha256 !== binaryHash; }
    catch (error) { report.status = 'failed'; process.exitCode = 1; report.binary_drift = true; report.binary_observation_error = error.message; }
    assert.equal(report.input_drift, false); assert.equal(report.binary_drift, false);
    assert.equal(report.cases.length, 2); assert.ok(report.commands.every(row => row.exit_observed && row.exit_code === 0 && row.signal === null));
    report.status = 'passed';
  } catch (error) { report.status = 'failed'; report.error = error.stack || error.message; process.exitCode = 1; }
  finally {
    if (report.status !== 'passed' && initial) {
      try { report.inputs_after = { go: inputs(goRoot, 'failed-go-after'), template: inputs(template, 'failed-template-after') };
        report.input_drift = initial.go.sha256 !== report.inputs_after.go.sha256 || initial.template.sha256 !== report.inputs_after.template.sha256; } catch (error) { report.observation_error = error.message; }
    }
    try { report.binary_after_sha256 = sha(fs.readFileSync(binary)); report.binary_drift = report.binary_after_sha256 !== binaryHash; }
    catch (error) { report.status = 'failed'; process.exitCode = 1; report.binary_drift = true; report.binary_observation_error = error.message; }
    if (report.binary_drift) { report.status = 'failed'; process.exitCode = 1; report.error ||= 'binary changed during acceptance'; }
    report.finished_at = now(); save(); console.log(JSON.stringify({ status: report.status, platform: report.platform, report: path.join(out, 'report.json'), input_drift: report.input_drift, binary_drift: report.binary_drift }));
  }
}

main().catch(error => { console.error(error.stack || error.message); process.exitCode = 1; });
