#!/usr/bin/env node
// Build provenance only: successful compilation is not runtime qualification.
import fs from 'node:fs';
import {fixedGoEnvironment,GO_ENV_KEYS,checkModuleInputs,checkPackageInputs} from './go-environment.mjs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {execFileSync} from 'node:child_process';
import {fileURLToPath} from 'node:url';
import {verifyTrustedRegistry} from './trusted-registry.mjs';

const digest = data => createHash('sha256').update(data).digest('hex');
const ARCHIVE = 'internal/bundle/assets/bundles.json.gz';

export function buildCandidate({root, out}) {
  root = fs.realpathSync(root);
  if (!out) throw new Error('--out must name a new directory outside the source checkout');
  out = path.resolve(out);
  fs.mkdirSync(path.dirname(out), {recursive:true});
  out = path.join(fs.realpathSync(path.dirname(out)), path.basename(out));
  const rel = path.relative(root,out);
  if (!rel || (!rel.startsWith('..'+path.sep) && rel !== '..' && !path.isAbsolute(rel))) throw new Error('--out must be outside the source checkout');
  fs.mkdirSync(out);
  const git = args => execFileSync('git',args,{cwd:root,encoding:'utf8'}).trim();
  const inventory = () => {
    const refs = execFileSync('git',['ls-files','--cached','--others','--exclude-standard','-z'],{cwd:root,encoding:'utf8'}).split('\0').filter(Boolean);
    // 内嵌归档不入库，但属于编译输入：必须存在且与入库摘要一致，并并入清单。
    if(!refs.includes(ARCHIVE)) refs.push(ARCHIVE);
    return Object.fromEntries([...new Set(refs)].sort().map(ref => {
      const file=path.join(root,ref);
      if(!fs.existsSync(file)) return [ref,null];
      const info=fs.lstatSync(file);
      if(!info.isFile()) throw new Error(`build input is not a file: ${ref}`);
      return [ref,{sha256:digest(fs.readFileSync(file)),executable:!!(info.mode&0o111)}];
    }));
  };
  const receipt={schemaVersion:1,kind:'cli-build-receipt',status:'failed',root,createdAt:new Date().toISOString(),nativeRuntimeVerified:false,builderSha256:digest(fs.readFileSync(fileURLToPath(import.meta.url)))};
  const env=fixedGoEnvironment();
  receipt.environmentHelperSha256=digest(fs.readFileSync(new URL('./go-environment.mjs',import.meta.url)));
  try {
    checkModuleInputs(root,env);
    const recorded=fs.readFileSync(path.join(root,ARCHIVE+'.sha256'),'utf8').trim().split(/\s+/)[0];
    if(!fs.existsSync(path.join(root,ARCHIVE))) throw new Error('内嵌归档缺失，请先运行 node tools/fetch-bundle.mjs');
    receipt.embeddedArchive={path:ARCHIVE,sha256:digest(fs.readFileSync(path.join(root,ARCHIVE))),recordedSha256:recorded};
    if(receipt.embeddedArchive.sha256!==recorded) throw new Error('内嵌归档与入库摘要不一致，请运行 node tools/fetch-bundle.mjs --force');
    const trusted=verifyTrustedRegistry(root,fs.readFileSync(path.join(root,ARCHIVE)));
    receipt.trustedSources=trusted.registry.archives.map(entry=>({path:`internal/bundle/assets/trusted/${entry.deltaFile}`,sha256:entry.deltaSha256}));
    receipt.commit=git(['rev-parse','HEAD']);
    receipt.sourceState=git(['status','--porcelain']) ? 'working-tree' : 'committed';
    receipt.inputs=inventory(); receipt.inputDigest=digest(JSON.stringify(receipt.inputs));
    checkPackageInputs(root,env,receipt.inputs);
    receipt.goVersion=execFileSync('go',['version'],{cwd:root,env,encoding:'utf8'}).trim();
    const target=JSON.parse(execFileSync('go',['env','-json',...GO_ENV_KEYS],{cwd:root,env,encoding:'utf8'}));
    receipt.goEnvironment=target;
    receipt.platform=target.GOOS+'/'+target.GOARCH;
    const binary=path.join(out,target.GOOS==='windows'?'yss.exe':'yss');
    const flags=['build','-mod=readonly','-trimpath','-buildvcs=false','-ldflags',`-s -w -X github.com/iloveZzz/yss-cli/internal/domain.BuildCommit=${receipt.commit} -X github.com/iloveZzz/yss-cli/internal/domain.BuildSourceState=${receipt.sourceState}`,'-o',binary,'./cmd/yss'];
    receipt.command=['go',...flags]; receipt.cgoEnabled='0';
    execFileSync('go',flags,{cwd:root,env,stdio:['ignore','pipe','pipe']});
    receipt.binary={path:binary,bytes:fs.statSync(binary).size,sha256:digest(fs.readFileSync(binary))};
    checkPackageInputs(root,env,inventory());
    receipt.inputDrift=receipt.inputDigest!==digest(JSON.stringify(inventory())) || receipt.commit!==git(['rev-parse','HEAD']);
    if(receipt.inputDrift) throw new Error('build inputs changed while compiling');
    if(receipt.binary.bytes>35_000_000) throw new Error('binary exceeds 35,000,000 bytes');
    receipt.status='built';
    return receipt;
  } catch(error) {receipt.error=error.message;throw error}
  finally {fs.writeFileSync(path.join(out,'build-receipt.json'),JSON.stringify(receipt,null,2)+'\n')}
}

if(process.argv[1]&&path.resolve(process.argv[1])===fileURLToPath(import.meta.url)) {
  try {
    const options={root:path.resolve(path.dirname(fileURLToPath(import.meta.url)),'..')};
    const args=process.argv.slice(2);
    for(let i=0;i<args.length;i+=2){if(!['--root','--out'].includes(args[i])||!args[i+1])throw new Error('usage: build-candidate.mjs [--root DIR] --out DIR');options[args[i].slice(2)]=args[i+1]}
    const r=buildCandidate(options);console.log(JSON.stringify({status:r.status,platform:r.platform,binary:r.binary}));
  } catch(error){console.error(error.message);process.exitCode=1}
}
