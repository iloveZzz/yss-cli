// Independently pinned baseline JSON is reconstructed from current shared objects.
import {createHash} from 'node:crypto';
import {gunzipSync, gzipSync} from 'node:zlib';

const sha = value => createHash('sha256').update(value).digest('hex');
const marker = ',"objects":{';
const digest = value => /^[a-f0-9]{64}$/.test(value || '');
const unpack = value => {
  if (!Buffer.isBuffer(value) || value.length > 10_000_000) throw new Error('归档超过大小限制');
  return gunzipSync(value,{maxOutputLength:512*1024*1024});
};

export function reconstructBaseline(currentGzip, deltaGzip, expectedRawSha256) {
  if (!digest(expectedRawSha256)) throw new Error('缺少受信基线原始摘要');
  const current = JSON.parse(unpack(currentGzip));
  const delta = JSON.parse(unpack(deltaGzip));
  if (delta.version !== 1 || !Array.isArray(delta.keys) || !delta.missing || typeof delta.missing !== 'object') throw new Error('基线差量格式错误');
  const objects = delta.keys.map(key => {
    const value = Object.hasOwn(delta.missing,key) ? delta.missing[key] : current.objects[key];
    if (typeof value !== 'string' || !/^[a-f0-9]{64}$/.test(key)) throw new Error('基线摘要所需对象缺失');
    return JSON.stringify(key)+':'+JSON.stringify(value);
  });
  const raw = Buffer.concat([Buffer.from(delta.prefix,'base64'), Buffer.from(objects.join(',')+'}}')]);
  if(raw.length>512*1024*1024)throw new Error('基线超过大小限制');
  if (sha(raw) !== expectedRawSha256) throw new Error('基线摘要不匹配');
  return raw;
}

export function createBaselineDelta(currentGzip, baselineGzip, expectedArchiveSha256, expectedRawSha256) {
  if (!digest(expectedArchiveSha256) || sha(baselineGzip) !== expectedArchiveSha256) throw new Error('基线归档摘要不匹配');
  const baselineRaw = unpack(baselineGzip);
  if (!digest(expectedRawSha256) || sha(baselineRaw) !== expectedRawSha256) throw new Error('基线摘要不匹配');
  const text = baselineRaw.toString('utf8'), index = text.lastIndexOf(marker);
  if (index < 0) throw new Error('基线对象格式错误');
  const baseline = JSON.parse(text), current = JSON.parse(unpack(currentGzip));
  const keys = Object.keys(baseline.objects), missing = {};
  for (const key of keys) if (baseline.objects[key] !== current.objects[key]) missing[key] = baseline.objects[key];
  const delta = gzipSync(Buffer.from(JSON.stringify({version:1,prefix:Buffer.from(text.slice(0,index+marker.length)).toString('base64'),keys,missing})), {level:9});
  reconstructBaseline(currentGzip, delta, expectedRawSha256);
  return delta;
}
