import {test} from 'node:test';
import assert from 'node:assert/strict';
import {mkdtempSync, mkdirSync, copyFileSync, writeFileSync, readFileSync, realpathSync, rmSync} from 'node:fs';
import {spawnSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';
import {extractBaselineGenerator} from './fetch-bundle.mjs';

test('固定 CLI 3.0 源码缺失时提示取得指定提交', t => {
  const root = mkdtempSync(path.join(realpathSync(os.tmpdir()), 'yss-missing-generator-'));
  t.after(() => rmSync(root, {recursive: true, force: true}));
  assert.equal(spawnSync('git', ['init', '--quiet', root]).status, 0);
  assert.throws(() => extractBaselineGenerator(root, root), /d67bc777198174a92cc3f58e4f26ce9f0daef2d9/);
});

test('基线归档使用独立摘要，不覆盖当前 Bundle', t => {
  const root = mkdtempSync(path.join(realpathSync(os.tmpdir()), 'yss-fetch-baseline-'));
  t.after(() => rmSync(root, {recursive: true, force: true}));
  mkdirSync(path.join(root, 'tools'));
  mkdirSync(path.join(root, 'internal/bundle/assets'), {recursive: true});
  const source = path.dirname(fileURLToPath(import.meta.url));
  for (const file of ['fetch-bundle.mjs', 'go-environment.mjs', 'baseline-delta.mjs']) copyFileSync(path.join(source, file), path.join(root, 'tools', file));
  const assets = path.join(root, 'internal/bundle/assets');
  const current = Buffer.from('current bundle');
  const baseline = Buffer.from('trusted baseline');
  writeFileSync(path.join(assets, 'bundles.json.gz'), current);
  writeFileSync(path.join(assets, 'bundles.json.gz.sha256'), createHash('sha256').update(current).digest('hex'));
  writeFileSync(path.join(assets, 'baseline-5.0.0.json.gz'), baseline);
  writeFileSync(path.join(assets, 'baseline-5.0.0.json.gz.sha256'), createHash('sha256').update(baseline).digest('hex'));
  const result = spawnSync(process.execPath, [path.join(root, 'tools/fetch-bundle.mjs'), '--baseline', '--source-root', path.join(root, 'unavailable')], {encoding: 'utf8'});
  assert.equal(result.status, 0, result.stderr);
  assert.equal(JSON.parse(result.stdout).status, 'up-to-date');
  assert.deepEqual(readFileSync(path.join(assets, 'bundles.json.gz')), current);
  assert.deepEqual(readFileSync(path.join(assets, 'baseline-5.0.0.json.gz')), baseline);
  writeFileSync(path.join(assets, 'baseline-5.0.0.json.gz'), 'corrupt');
  const unavailable = path.join(root, 'not-a-directory');
  writeFileSync(unavailable, 'do not overwrite');
  const rejected = spawnSync(process.execPath, [path.join(root, 'tools/fetch-bundle.mjs'), '--baseline', '--source-root', unavailable], {encoding: 'utf8'});
  assert.notEqual(rejected.status, 0);
  assert.equal(readFileSync(path.join(assets, 'baseline-5.0.0.json.gz'), 'utf8'), 'corrupt');
  assert.deepEqual(readFileSync(path.join(assets, 'bundles.json.gz')), current);
  const normal = spawnSync(process.execPath, [path.join(root, 'tools/fetch-bundle.mjs'), '--source-root', unavailable], {encoding: 'utf8'});
  assert.notEqual(normal.status, 0, '默认准备必须检查基线归档');
});
