// 当前 Spec/Backend 实例各走一遍 init → 查询类命令 → sync → 回退。
import fs from 'node:fs';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {check} from './lib.mjs';

export const PROFILES = ['spec', 'backend'];
const SPEC_CORE = ['yss-product-lifecycle', 'to-spec', 'grilling', 'domain-modeling', 'i-have-adhd'];
const SPEC_ON_DEMAND = ['yss-research', 'yss-prototype-stage', 'prototype-review', 'yss-design-system', 'setup-yss-harness'];

function specWorkflow(root, metadata) {
 check(metadata.distribution.specWorkflow==='matt-spec-v1','governed Spec must declare matt-spec-v1');
 check(Array.isArray(metadata.distribution.installedStages)&&metadata.distribution.installedStages.length===0,'new Spec must not install legacy stages');
 for(const ref of ['yss-project.yaml','.template-spec/process/harness-profile.yaml','.template-spec/process/spec-workflow.json','.template-spec/agents/issue-tracker.md','.template-spec/templates/spec-template.md'])check(fs.existsSync(path.join(root,ref)),`new Spec resource missing ${ref}`);
 for(const ref of ['.template-spec/process/orchestration-contract.yaml','.agents/skills/implement','.agents/skills/yss-implementation-contract-compiler'])check(!fs.existsSync(path.join(root,ref)),`new Spec unexpectedly installs legacy implementation resource ${ref}`);
 const policy=JSON.parse(fs.readFileSync(path.join(root,'.template-spec/process/spec-workflow.json')));
 check(policy.schemaVersion===1&&policy.profile==='spec'&&policy.specWorkflow==='matt-spec-v1','new Spec workflow policy must match its declaration');
}

function specReadiness(session, root, skills) {
 const metadata=JSON.parse(fs.readFileSync(path.join(root,'.yss.json')));
 for(const runtime of ['',...metadata.distribution.runtimes]) {
  const args=['skills','resolve',...skills,'--root',root,'--json'];
  if(runtime)args.push('--agent-runtime',runtime);
  const resolved=session.expectOk(args,{label:'installed skill readiness'}).result;
  check(resolved.status==='ready'&&resolved.missing.length===0&&resolved.issues.length===0,'installed skills and declared projections must be ready');
 }
 const doctor=session.expectOk(['doctor','--root',root,'--json']).result;
 check(doctor.readiness.skills.status==='ready'&&doctor.readiness.workflow==='not-evaluated','doctor must separate installed skills from workflow acceptance');
}

export function runProfile(session, profile, tempRoot) {
  session.log(`[profile:${profile}]`);
  const root = path.join(tempRoot, `project-${profile}`);
  const plans = path.join(tempRoot, `plans-${profile}`);
  fs.mkdirSync(plans, {recursive: true});
  const name = ['--project-name', 'Smoke'];

  const initPlan = path.join(plans, 'init.json');
  session.expectOk(['init', '--profile', profile, '--root', root, ...name, '--plan', '--out', initPlan, '--json'], {label: 'init --plan'});
  check(fs.existsSync(initPlan), `init --plan 未写出计划文件: ${initPlan}`);
  check(!fs.existsSync(root), 'init --plan 不应创建项目目录');
  const applied = session.expectOk(['init', '--profile', profile, '--root', root, ...name, '--json'], {label: 'default init'});
  check(applied.result?.status === 'applied', `init 结果应为 applied，实际 ${applied.result?.status}`);
  const metadata=JSON.parse(fs.readFileSync(path.join(root,'.yss.json')));
  check(metadata.distribution.installation==='governed','default init must record governed');
  check(fs.existsSync(path.join(root,'yss-project.yaml')),'default init missing yss-project.yaml');
  check(fs.existsSync(path.join(root,'CONTEXT.md'))===(profile==='backend'),'default init Context boundary differs from Profile');

  if(profile==='spec') {
   specWorkflow(root,metadata);
   const core=[...SPEC_CORE,...(metadata.distribution.installedSkills.includes('handoff')?['handoff']:[])];
   check(metadata.distribution.installedSkills.length===core.length&&core.every(id=>metadata.distribution.installedSkills.includes(id)),'governed Spec must install only its core skills');
   specReadiness(session,root,core);
  }

  const json = '--json';
  session.expectOk(['doctor', '--root', root, json], {label: 'doctor'});
  session.expectOk(['diff', '--root', root, json], {label: 'diff'});
  if(profile==='backend')session.expectOk(['context', 'verify', '--root', root, json], {label: 'context verify'});
  const skills = session.expectOk(['skills', 'list', '--root', root, json], {label: 'skills list'});
  check(Array.isArray(skills.result) && skills.result.length > 0, 'skills list 应返回非空标识列表');
  if(profile==='spec') {
   // 新 Spec 使用技能与 tracker；旧阶段、交接和自动安装方式转换必须明确拒绝。
   for(const args of [['assets','list'],['lifecycle','status'],['stage','status'],['handoff','check'],['profile','prepare']])session.expectRejected([...args,'--root',root,json],{code:'SPEC_COMMAND_NOT_APPLICABLE',label:`new Spec rejects ${args.join(' ')}`});
   const stagePlan=path.join(plans,'stage.json');
   session.expectRejected(['assets','ensure','stage.product-design','--root',root,'--plan','--out',stagePlan,json],{code:'SPEC_COMMAND_NOT_APPLICABLE',label:'new Spec rejects stage ensure'});
   check(!fs.existsSync(stagePlan),'rejected stage ensure must not write a plan');
   const migration=path.join(plans,'skills.json');
   session.expectRejected(['migrate','--root',root,'--installation','skills','--plan','--out',migration,json],{code:'SPEC_MANUAL_MIGRATION_REQUIRED',label:'new Spec cannot automatically migrate to skills'});
   check(!fs.existsSync(migration),'rejected Spec migration must not write a plan');
  } else session.expectOk(['assets', 'list', '--root', root, json], {label: 'assets list'});
  const recover = session.expectOk(['recover', '--root', root, json], {label: 'recover'});
  check(recover.result?.transactions?.some(item => item.kind === 'init' && item.phase === 'committed'), 'recover 应列出已提交的 init 事务');
  session.expectOk(['rollback', '--root', root, json], {label: 'rollback（只查询）'});

  const syncPlan = path.join(plans, 'sync.json');
  session.expectOk(['sync', '--root', root, '--plan', '--out', syncPlan, json], {label: 'sync --plan'});
  const synced = session.expectOk(['sync', '--root', root, '--apply', '--plan-file', syncPlan, json], {label: 'sync --apply'});
  check(synced.result?.status === 'unchanged', `刚初始化的实例 sync 应为 unchanged，实际 ${synced.result?.status}`);

  // 回退最近一次成功事务（init），然后确认项目身份已被移除。
  const rolledBack = session.expectOk(['rollback', '--root', root, '--apply', json], {label: 'rollback --apply'});
  check(rolledBack.result?.status === 'rolled-back', `rollback --apply 结果应为 rolled-back，实际 ${rolledBack.result?.status}`);
  session.expectRejected(['doctor', '--root', root, json], {code: 'IDENTITY', label: '回退后 doctor 找不到项目身份'});

  const existing=path.join(tempRoot,`attach-${profile}`), attachPlan=path.join(plans,'attach.json');
  fs.mkdirSync(existing);
  const business=path.join(existing,'business.txt');
  fs.writeFileSync(business,'user business');
  const context=path.join(existing,'CONTEXT.md'), contextBody='# Existing business context\nProject-owned terms and decisions.\n';
  if(profile==='spec')fs.writeFileSync(context,contextBody,{mode:0o444});
  const contextMode=profile==='spec'?fs.statSync(context).mode&0o777:null;
  session.expectOk(['attach','--profile',profile,'--root',existing,'--plan','--out',attachPlan,json],{label:'default attach --plan'});
  check(fs.readdirSync(existing).length===(profile==='spec'?2:1)&&fs.readFileSync(business,'utf8')==='user business','attach planning must preserve existing files');
  if(profile==='spec')check(fs.readFileSync(context,'utf8')===contextBody&&(fs.statSync(context).mode&0o777)===contextMode,'attach planning must preserve ordinary Context bytes and mode');
  check(JSON.parse(fs.readFileSync(attachPlan)).installation==='governed','default attach plan must select governed');
  session.expectOk(['attach','--root',existing,'--apply','--plan-file',attachPlan,json],{label:'default attach --apply'});
  const attached=JSON.parse(fs.readFileSync(path.join(existing,'.yss.json')));
  check(attached.distribution.installation==='governed','saved attach plan must preserve governed');
  for(const ref of ['yss-project.yaml','CONTEXT.md'])check(fs.existsSync(path.join(existing,ref)),`default attach missing ${ref}`);
  check(fs.readFileSync(business,'utf8')==='user business','attach must preserve user bytes');
  if(profile==='spec') {
   specWorkflow(existing,attached);
   check(fs.readFileSync(context,'utf8')===contextBody&&(fs.statSync(context).mode&0o777)===contextMode,'attach must preserve ordinary Context bytes and mode');
   const ensure=path.join(plans,'optional-skills.json');
   session.expectOk(['skills','ensure',...SPEC_ON_DEMAND,'--root',existing,'--plan','--out',ensure,json],{label:'Spec optional skills plan'});
   session.expectOk(['skills','--root',existing,'--apply','--plan-file',ensure,json],{label:'Spec optional skills apply'});
   specReadiness(session,existing,SPEC_ON_DEMAND);
   const repeat=session.expectOk(['skills','ensure',...SPEC_ON_DEMAND,'--root',existing,'--plan',json],{label:'Spec optional skills repeat'}).result;
   check(repeat.changes.length===0,'Spec optional skills ensure must be idempotent');
  }
}

export function runSkillsProfile(session, profile, tempRoot) {
 const root=path.join(tempRoot,`skills-${profile}`),plans=path.join(tempRoot,`skills-plans-${profile}`);
 fs.mkdirSync(plans,{recursive:true});
 const plan=path.join(plans,'init.json');
 session.expectOk(['init','--installation','skills','--root',root,'--profile',profile,'--plan','--out',plan,'--json'],{label:'explicit skills init plan'});
 session.expectOk(['init','--root',root,'--apply','--plan-file',plan,'--json'],{label:'skills init apply'});
 const metadata=JSON.parse(fs.readFileSync(path.join(root,'.yss.json')));
 check(metadata.distribution.installation==='skills','explicit installation must be skills');
 for(const ref of ['yss-project.yaml','CONTEXT.md'])check(!fs.existsSync(path.join(root,ref)),`ordinary installation unexpectedly requires ${ref}`);
 const fixed=session.expectOk(['bundle','inspect','--profile',profile,'--json']).result;
 const references=['.template-source/process/MATT-POCOCK-ENGINEERING-SKILLS.md','.template-spec/process/orchestration-contract.yaml','.template-spec/agents/issue-tracker.md'];
 // Bundles predating reference maps do not contain the marker source file.
 if(process.env.YSS_TEST_REQUIRE_SKILL_REFERENCES==='1')check(!!fixed.files[references[0]],'new Bundle must include declared skill references');
 if(fixed.files[references[0]])for(const ref of references) {
  const absolute=path.join(root,ref), record=metadata.managedFiles?.[ref];
  check(fixed.files[ref]&&fs.lstatSync(absolute).isFile()&&record?.lastApplied?.digest,`declared reference missing: ${ref}`);
  check(createHash('sha256').update(fs.readFileSync(absolute)).digest('hex')===record.lastApplied.digest,`declared reference drifted: ${ref}`);
 }

 session.expectOk(['doctor','--root',root,'--json'],{label:'skills doctor without governance'});
 const roots=profile==='spec'?['yss-product-lifecycle','grilling','yss-research','domain-modeling','to-spec','i-have-adhd','code-review','implement']:['implement','code-review'];
 if(metadata.distribution.installedSkills.includes('handoff'))roots.push('handoff');
 check(metadata.distribution.installedSkills.length===roots.length&&roots.every(id=>metadata.distribution.installedSkills.includes(id)),'default skills must match Profile starter roots');
 if(profile==='spec')specReadiness(session,root,roots);
 if(profile==='backend') {
  const ensure=path.join(plans,'to-spec.json');
  session.expectOk(['skills','ensure','to-spec','--root',root,'--plan','--out',ensure,'--json']);
  session.expectOk(['skills','--root',root,'--apply','--plan-file',ensure,'--json']);
  const resolved=session.expectOk(['skills','resolve','to-spec','--root',root,'--json']).result;
  check(resolved.status==='ready'&&!resolved.issues.length&&!resolved.missing.length,'Backend explicit Spec dependencies must be ready');
  check(resolved.canonicalIds.includes('i-have-adhd'),'Backend writing reference dependency must be selected');
  const entry=path.join(root,'.agents/skills/to-spec/SKILL.md');
  for(const match of fs.readFileSync(entry,'utf8').matchAll(/\]\(([^)]+)\)/g)) {
   const ref=match[1].split('#')[0];if(!ref||ref.includes('://'))continue;
   check(fs.statSync(path.resolve(path.dirname(entry),ref)).isFile(),`Backend explicit Spec reference must exist: ${ref}`);
  }
 }

 const sync=path.join(plans,'sync.json');
 session.expectOk(['sync','--root',root,'--plan','--out',sync,'--json']);
 const result=session.expectOk(['sync','--root',root,'--apply','--plan-file',sync,'--json']);
 check(result.result.status==='unchanged','fresh ordinary sync must be unchanged');
 session.expectRejected(['lifecycle','query','--root',root,'--json'],{label:'ordinary project cannot run formal governance'});
 if(profile==='spec') {
  const migration=path.join(plans,'governed.json');
  session.expectRejected(['migrate','--root',root,'--installation','governed','--plan','--out',migration,'--json'],{code:'SPEC_MANUAL_MIGRATION_REQUIRED',label:'skills cannot automatically migrate to new Spec'});
  check(!fs.existsSync(migration),'rejected skills migration must not write a plan');
  check(JSON.parse(fs.readFileSync(path.join(root,'.yss.json'))).distribution.installation==='skills'&&!fs.existsSync(path.join(root,'yss-project.yaml')),'rejected migration must preserve ordinary installation');
 }

 if(profile==='spec')for(const installation of ['skills','governed']) {
  const fullRoot=path.join(tempRoot,`full-${installation}-spec`);
  session.expectOk(['init','--root',fullRoot,'--profile','spec','--installation',installation,'--full','--json'],{label:`full ${installation} init`});
  const full=JSON.parse(fs.readFileSync(path.join(fullRoot,'.yss.json')));
  check(full.distribution.installation===installation,'full must preserve selected installation');
  check(fs.existsSync(path.join(fullRoot,'yss-project.yaml'))===(installation==='governed'),'full governance identity boundary differs from installation');
  check(!fs.existsSync(path.join(fullRoot,'CONTEXT.md')),'full Spec must not generate legacy Context');
  const supported=session.expectOk(['skills','list','--root',fullRoot,'--json']).result;
  check(supported.every(id=>full.distribution.installedSkills.includes(id)),'full must install all supported Profile skills');
  if(installation==='governed') {
   specWorkflow(fullRoot,full);
   check(supported.length===SPEC_CORE.length+SPEC_ON_DEMAND.length&&[...SPEC_CORE,...SPEC_ON_DEMAND].every(id=>supported.includes(id)),'full governed Spec must expose its core and on-demand skill catalog');
   for(const ref of ['DESIGN.md','.template-spec/design/tokens/theme.json','scripts/lib/source-context-snapshot.mjs'])check(fs.existsSync(path.join(fullRoot,ref)),`full Spec optional resource missing ${ref}`);
  }
  specReadiness(session,fullRoot,full.distribution.installedSkills);
 }
}
