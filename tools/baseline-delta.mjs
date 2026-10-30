// Baseline is reconstructed from its exact original JSON prefix and objects.
// Objects are shared only when both key and value match the current archive.
import {createHash} from 'node:crypto';
import {gunzipSync, gzipSync} from 'node:zlib';

const baselineArchiveSHA = '80ee2f064729dae37fb79d0334e19df7f89b84f5db5474e61cb3a5cb212ba83d';
const baselineRawSHA = '271b9b9b46017f636d0cd4f6ffb9b85b098568b5c2369789f89bfb52ce82baca';
const sha = b => createHash('sha256').update(b).digest('hex');
const marker = ',"objects":{';

export function reconstructBaseline(currentGzip, deltaGzip) {
  const current = JSON.parse(gunzipSync(currentGzip));
  const delta = JSON.parse(gunzipSync(deltaGzip));
  if (delta.version !== 1 || !Array.isArray(delta.keys)) throw new Error('基线差量格式错误');
  const objects = delta.keys.map(key => {
    const value = Object.hasOwn(delta.missing,key) ? delta.missing[key] : current.objects[key];
    if (typeof value !== 'string' || !/^[a-f0-9]{64}$/.test(key)) throw new Error('基线摘要所需对象缺失');
    return JSON.stringify(key)+':'+JSON.stringify(value);
  });
  const raw = Buffer.concat([Buffer.from(delta.prefix,'base64'), Buffer.from(objects.join(',')+'}}')]);
  if (sha(raw) !== baselineRawSHA) throw new Error('基线摘要不匹配');
  return raw;
}

export function createBaselineDelta(currentGzip, baselineGzip) {
  if (sha(baselineGzip) !== baselineArchiveSHA) throw new Error('基线归档摘要不匹配');
  const baselineRaw = gunzipSync(baselineGzip);
  if (sha(baselineRaw) !== baselineRawSHA) throw new Error('基线摘要不匹配');
  const text = baselineRaw.toString('utf8');
  const index = text.lastIndexOf(marker);
  if (index < 0) throw new Error('基线对象格式错误');
  const baseline = JSON.parse(text);
  const current = JSON.parse(gunzipSync(currentGzip));
  const keys = Object.keys(baseline.objects);
  const missing = {};
  for (const key of keys) if (baseline.objects[key] !== current.objects[key]) missing[key] = baseline.objects[key];
  const delta = gzipSync(Buffer.from(JSON.stringify({version:1,prefix:Buffer.from(text.slice(0,index+marker.length)).toString('base64'),keys,missing})), {level:9});
  reconstructBaseline(currentGzip, delta);
  return delta;
}
