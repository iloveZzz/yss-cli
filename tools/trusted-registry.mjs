// The committed registry, source locks and delta hashes are the trust root.
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {gunzipSync,gzipSync} from 'node:zlib';
import {fileURLToPath} from 'node:url';
import {createBaselineDelta,reconstructBaseline} from './baseline-delta.mjs';

const sha = value => createHash('sha256').update(value).digest('hex');
const hash = value => /^[a-f0-9]{64}$/.test(value || '');
const commit = value => /^[a-f0-9]{40}$/.test(value || '');
const version = value => /^(?:5|[6-9]|[1-9][0-9]+)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)$/.test(value || '');
const retired = '1e49127842c358caf552af8ebec58f438a35c5a6';
const registryRef = 'internal/bundle/assets/trusted/registry.json';
function readSource(root,ref) {
 const file=path.join(fs.realpathSync(root),ref), info=fs.lstatSync(file);
 if(!info.isFile()||info.isSymbolicLink()||info.size>10_000_000||fs.realpathSync(file)!==file)throw new Error(`受信来源不是合法普通文件: ${ref}`);
 return fs.readFileSync(file);
}

export function verifyTrustedRegistry(root, currentGzip) {
 const registry = JSON.parse(readSource(root,registryRef));
 if (!registry || Object.keys(registry).sort().join(',') !== 'archives,schemaVersion' || registry.schemaVersion !== 1 || !Array.isArray(registry.archives)) throw new Error('受信来源登记无效');
 const seen = new Set(), files = [];
 for (const entry of registry.archives) {
  if (!entry || Object.keys(entry).sort().join(',') !== 'archiveSha256,commit,deltaFile,deltaSha256,producerCommit,rawSha256,sourceLock,version' || !version(entry.version) || !commit(entry.commit) || entry.commit === retired || !commit(entry.producerCommit) || !hash(entry.archiveSha256) || !hash(entry.rawSha256) || !hash(entry.deltaSha256) || entry.sourceLock !== `docs/trusted-sources/${entry.version}.json` || entry.deltaFile !== `${entry.version}.delta.gz` || seen.has(entry.version)) throw new Error('受信来源条目无效或重复');
  seen.add(entry.version);
  const lock = JSON.parse(readSource(root,entry.sourceLock));
  if (lock.schemaVersion !== 5 || lock.producer?.sourceState !== 'committed' || lock.producer?.commit !== entry.producerCommit || Object.keys(lock.profiles || {}).sort().join(',') !== 'backend,spec' || Object.entries(lock.profiles).some(([name,p])=>p.profile !== name || p.sourceState !== 'committed' || p.templateCommit !== entry.commit || p.templateVersion !== entry.version || p.templateTag !== `v${entry.version}` || !hash(p.policyHash))) throw new Error('受信来源锁与登记不一致');
  const ref = `internal/bundle/assets/trusted/${entry.deltaFile}`, delta = readSource(root,ref);
  if (sha(delta) !== entry.deltaSha256) throw new Error('受信差量摘要不一致');
  const raw = reconstructBaseline(currentGzip,delta,entry.rawSha256), archive = JSON.parse(raw);
  if (Object.keys(archive.profiles || {}).sort().join(',') !== 'backend,spec' || Object.entries(archive.profiles).some(([name,p])=>p.profile !== name || p.templateCommit !== entry.commit || p.templateVersion !== entry.version || p.sourceState !== 'committed' || p.producer?.commit !== entry.producerCommit || p.producer?.sourceState !== 'committed' || p.sourcePolicy?.digest !== lock.profiles[name].policyHash)) throw new Error('受信 Bundle 身份与登记不一致');
  files.push(ref,entry.sourceLock);
 }
 return {registry,files};
}

// Produce reviewable replacement deltas in memory; callers decide where to save them.
export function refreshTrustedRegistry(root, oldCurrent, newCurrent) {
 const {registry}=verifyTrustedRegistry(root,oldCurrent), outputs={};
 for(const entry of registry.archives){
  const previous=readSource(root,`internal/bundle/assets/trusted/${entry.deltaFile}`);
  const raw=reconstructBaseline(oldCurrent,previous,entry.rawSha256), archive=gzipSync(raw,{level:9});
  const delta=createBaselineDelta(newCurrent,archive,sha(archive),entry.rawSha256);
  entry.deltaSha256=sha(delta);outputs[entry.deltaFile]=delta;
 }
 return {registry,outputs};
}

export function addTrustedArchive(root,current,archive,expectedArchiveSha,sourceLock,expectedLockSha) {
 const {registry}=verifyTrustedRegistry(root,current);
 if(!hash(expectedArchiveSha)||sha(archive)!==expectedArchiveSha||!hash(expectedLockSha)||sha(sourceLock)!==expectedLockSha)throw new Error('登记来源归档或来源锁摘要不匹配');
 const lock=JSON.parse(sourceLock), profiles=lock.profiles || {}, spec=profiles.spec, backend=profiles.backend;
 const v=spec?.templateVersion, c=spec?.templateCommit;
 if(lock.schemaVersion!==5||!version(v)||!commit(c)||c===retired||!commit(lock.producer?.commit)||lock.producer?.sourceState!=='committed'||Object.keys(profiles).sort().join(',')!=='backend,spec'||Object.entries(profiles).some(([name,p])=>p.profile!==name||p.sourceState!=='committed'||p.templateVersion!==v||p.templateCommit!==c||p.templateTag!==`v${v}`||!hash(p.policyHash))||registry.archives.some(e=>e.version===v))throw new Error('待登记来源锁无效或版本重复');
 if(archive.length>10_000_000||sourceLock.length>10_000_000)throw new Error('待登记来源超过大小限制');
 const raw=gunzipSync(archive,{maxOutputLength:512*1024*1024}), data=JSON.parse(raw);
 if(Object.keys(data.profiles||{}).sort().join(',')!=='backend,spec'||Object.entries(data.profiles).some(([name,p])=>p.profile!==name||p.templateVersion!==v||p.templateCommit!==c||p.sourceState!=='committed'||p.producer?.commit!==lock.producer.commit||p.producer?.sourceState!=='committed'||p.sourcePolicy?.digest!==profiles[name].policyHash))throw new Error('待登记 Bundle 与来源锁不一致');
 const entry={version:v,commit:c,sourceLock:`docs/trusted-sources/${v}.json`,producerCommit:lock.producer.commit,archiveSha256:expectedArchiveSha,rawSha256:sha(raw),deltaFile:`${v}.delta.gz`,deltaSha256:''};
 const delta=createBaselineDelta(current,archive,expectedArchiveSha,entry.rawSha256);entry.deltaSha256=sha(delta);
 registry.archives.push(entry);
 return {registry,outputs:{[entry.deltaFile]:delta,[`${v}.json`]:sourceLock}};
}

if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url)){
 try{
  const [action,...args]=process.argv.slice(2), options={};
  if(args.length%2)throw new Error('参数不完整');
  for(let i=0;i<args.length;i+=2){if(!args[i].startsWith('--')||Object.hasOwn(options,args[i]))throw new Error('参数重复或无效');options[args[i]]=args[i+1];}
  const root=fs.realpathSync(path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..'));
  const requested=path.resolve(options['--out']||''), out=path.join(fs.realpathSync(path.dirname(requested)),path.basename(requested));
  const rel=path.relative(root,out);if(!options['--out']||!rel||(!rel.startsWith('..'+path.sep)&&rel!=='..'&&!path.isAbsolute(rel)))throw new Error('输出必须是仓外全新目录');
  let result;
  if(action==='refresh'&&Object.keys(options).sort().join(',')==='--new-current,--old-current,--out') result=refreshTrustedRegistry(root,fs.readFileSync(options['--old-current']),fs.readFileSync(options['--new-current']));
  else if(action==='add'&&Object.keys(options).sort().join(',')==='--archive,--archive-sha,--current,--out,--source-lock,--source-lock-sha') result=addTrustedArchive(root,fs.readFileSync(options['--current']),fs.readFileSync(options['--archive']),options['--archive-sha'],fs.readFileSync(options['--source-lock']),options['--source-lock-sha']);
  else throw new Error('usage: trusted-registry.mjs add|refresh with explicit input paths and --out');
  fs.mkdirSync(out);fs.writeFileSync(path.join(out,'registry.json'),JSON.stringify(result.registry,null,2)+'\n',{flag:'wx'});
  for(const [name,data] of Object.entries(result.outputs))fs.writeFileSync(path.join(out,name),data,{flag:'wx'});
  console.log(JSON.stringify({status:'prepared-for-review',out,versions:result.registry.archives.map(e=>e.version)}));
 }catch(error){console.error(error.message);process.exitCode=1;}
}
