import {test} from 'node:test';
import assert from 'node:assert/strict';
import {createHash} from 'node:crypto';
import {gzipSync} from 'node:zlib';
import {createBaselineDelta,reconstructBaseline} from './baseline-delta.mjs';

const sha = value => createHash('sha256').update(value).digest('hex');
test('受信差量可跨当前版本重建并拒绝对象或摘要篡改',()=>{
 const key='a'.repeat(64),raw=Buffer.from(JSON.stringify({storageFormatVersion:2,profiles:{},objects:{[key]:'YWJj'}}));
 const baseline=gzipSync(raw),current=gzipSync(Buffer.from(JSON.stringify({objects:{[key]:'YWJj'}})));
 const delta=createBaselineDelta(current,baseline,sha(baseline),sha(raw));
 assert.deepEqual(reconstructBaseline(current,delta,sha(raw)),raw);
 const tampered=gzipSync(Buffer.from(JSON.stringify({objects:{[key]:'dGFtcGVyZWQ='}})));
 assert.throws(()=>reconstructBaseline(tampered,delta,sha(raw)),/摘要/);
 assert.throws(()=>createBaselineDelta(current,baseline,'f'.repeat(64),sha(raw)),/摘要/);
});
