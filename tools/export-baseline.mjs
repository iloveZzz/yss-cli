// 模板源维护工具；不属于项目治理运行链。
import fs from 'node:fs';
import path from 'node:path';
import {createRequire} from 'node:module';
import {createHash} from 'node:crypto';
import {gzipSync} from 'node:zlib';
import {execFileSync} from 'node:child_process';
import {pathToFileURL,fileURLToPath} from 'node:url';
const source=path.resolve(process.argv[2]||'');
if(!process.argv[2]) throw new Error('需要显式模板源根');
const output=path.resolve(path.dirname(fileURLToPath(import.meta.url)),'../internal/bundle/assets');
fs.mkdirSync(output,{recursive:true});
const hash=b=>createHash('sha256').update(b).digest('hex');
const dirs={spec:'create-yss-spec',design:'create-yss-strategic-design',backend:'create-yss-harness-backend',frontend:'create-yss-harness-frontend'};
const vars={projectName:'__YSS_PROJECT_NAME__',businessDomain:'__YSS_BUSINESS_DOMAIN__',teamSize:'__YSS_TEAM_SIZE__',issueTracker:'local-markdown',includeExampleDocs:false,agentRuntime:'codex'};
const lock={schemaVersion:1,sourceRootRevision:execFileSync('git',['-C',source,'rev-parse','HEAD'],{encoding:'utf8'}).trim(),profiles:{}};
for(const [profile,dir] of Object.entries(dirs)){
 const root=path.join(source,'submodules',dir);
 if(execFileSync('git',['-C',root,'status','--porcelain'],{encoding:'utf8'}).trim())throw new Error('CLI 必须是干净固定源码: '+profile);
 const pkg=JSON.parse(fs.readFileSync(path.join(root,'package.json')));
 const snapshot=JSON.parse(fs.readFileSync(path.join(root,'template.snapshot.json')));
 const manifestBytes=fs.readFileSync(path.join(root,'template.manifest.json'));
 const manifest=JSON.parse(manifestBytes);
 let all,initial,distribution;
 if(profile==='spec'){
  const require=createRequire(path.join(root,'package.json'));
  const runtime=require('./src/template/instance-runtime.js');
  const ownership=require('./src/template/ownership-runtime.js');
  const extract=variables=>Object.fromEntries(ownership.applyOwnershipToOperations(runtime.buildDesiredManagedOperations('/__yss_export_only__',variables,'init')).filter(o=>o.type!=='mkdir').map(o=>[o.relativePath,{data:Buffer.from(o.desiredContent??fs.readFileSync(o.sourcePath)).toString('base64'),digest:hash(Buffer.from(o.desiredContent??fs.readFileSync(o.sourcePath))),mode:fs.statSync(o.sourcePath).mode&0o777,ownership:o.ownership}]));
  all=extract({...vars,distribution:{mode:'legacy-all'}});
  initial=extract(vars);
  distribution=require('./src/template/distribution-runtime.js').distributionForVariables(vars,runtime.BUNDLED_TEMPLATE_ROOT);
 }else{
  const {loadBundle,render}=await import(pathToFileURL(path.join(root,'vendor/cli-core/bundle.mjs')));
  const {userOwned,preserved}=await import(pathToFileURL(path.join(root,'vendor/cli-core/family.mjs')));
  const b=loadBundle(root);
  all=Object.fromEntries([...render(b,vars)].map(([ref,item])=>[ref,{data:item.bytes.toString('base64'),digest:hash(item.bytes),mode:item.baseline.mode,ownership:userOwned(ref,b.family)?'user-owned':preserved(ref)?'managed-customizable':'managed'}]));
  initial=all;distribution={mode:'profile-full'};
 }
 const provenance={profile,legacyVersion:pkg.version,cliCommit:execFileSync('git',['-C',root,'rev-parse','HEAD'],{encoding:'utf8'}).trim(),templateCommit:snapshot.templateCommit,sourceState:snapshot.sourceState,sourceSnapshotHash:snapshot.snapshotHash,manifestHash:hash(manifestBytes)};
 lock.profiles[profile]=provenance;
 let stageRequirements={},skillRequirements={};
 if(profile==='spec'){
  const require=createRequire(path.join(root,'package.json'));
  const ar=require('./src/template/asset-runtime.js');
  for(const [stage,entry] of Object.entries(ar.STAGES)) {
   const d={...distribution,installedStages:[...new Set([...distribution.installedStages,stage])],installedSkills:[...new Set([...distribution.installedSkills,...entry.skills])]};
   let paths;
   for(let attempt=0;attempt<100;attempt++){
    try{paths=[...ar.assetPaths(path.join(root,'template'),d)].sort();break;}
    catch(e){if(!e.requiredSkill||d.installedSkills.includes(e.requiredSkill))throw e;d.installedSkills.push(e.requiredSkill);}
   }
   if(!paths)throw new Error('无法收敛阶段依赖: '+stage);
   stageRequirements[stage]={paths,skills:d.installedSkills.filter(s=>!distribution.installedSkills.includes(s))};
  }
  for(const name of [...new Set(Object.keys(all).filter(ref=>/^\.agents\/skills\/[^.][^/]*\/SKILL\.md$/.test(ref)).map(ref=>ref.split('/')[2]))]){
   const d={...distribution,installedSkills:[...new Set([...distribution.installedSkills,name])],resourceSkills:[name]};
   let paths,unsupportedReason;
   for(let attempt=0;attempt<100;attempt++){
    try{paths=[...ar.assetPaths(path.join(root,'template'),d)].sort();break;}
    catch(e){if(!e.requiredSkill||d.installedSkills.includes(e.requiredSkill)){unsupportedReason=e.message;break;}d.installedSkills.push(e.requiredSkill);}
   }
   if(!paths&&!unsupportedReason)throw new Error('无法收敛 Skill 依赖: '+name);
   skillRequirements[name]={paths:paths||[],skills:d.installedSkills.filter(s=>!distribution.installedSkills.includes(s)),unsupportedReason};
  }
 }
 const payload={schemaVersion:1,...provenance,distribution,manifest,stageRequirements,skillRequirements,initial:profile==='spec'?initial:null,files:all};
 const bytes=Buffer.from(JSON.stringify(payload));
 fs.writeFileSync(path.join(output,profile+'.json.gz'),gzipSync(bytes,{level:9,mtime:0}));
 process.stdout.write(profile+': '+Object.keys(initial).length+' initial / '+Object.keys(all).length+' total, '+bytes.length+' bytes\n');
}
fs.mkdirSync(path.join(output,'../../../docs'),{recursive:true});
fs.writeFileSync(path.join(output,'../../../docs/source-lock.json'),JSON.stringify(lock,null,2)+'\n');
