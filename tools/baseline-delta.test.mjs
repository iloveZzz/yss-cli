import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {gunzipSync, gzipSync} from 'node:zlib';
import {createBaselineDelta, reconstructBaseline} from './baseline-delta.mjs';

test('真实 5.0 基线可从不同当前对象集合重建，篡改被拒绝', () => {
  const baseline = readFileSync(new URL('../internal/bundle/assets/baseline-5.0.0.json.gz', import.meta.url));
  const current = JSON.parse(gunzipSync(baseline));
  const keys = Object.keys(current.objects);
  for (const key of keys.slice(0, 25)) delete current.objects[key];
  current.objects['synthetic-new-object'] = 'bmV3';
  const changed = gzipSync(Buffer.from(JSON.stringify(current)));
  const delta = createBaselineDelta(changed, baseline);
  assert.deepEqual(reconstructBaseline(changed, delta), gunzipSync(baseline));
  assert.ok(delta.length < 2_000_000, `差量过大: ${delta.length}`);
  const wrong = JSON.parse(gunzipSync(changed));
  wrong.objects[keys[100]] = 'dGFtcGVyZWQ=';
  assert.throws(() => reconstructBaseline(gzipSync(Buffer.from(JSON.stringify(wrong))), delta), /基线摘要/);
  assert.throws(() => createBaselineDelta(changed, Buffer.from('corrupt')), /基线归档摘要/);
});
