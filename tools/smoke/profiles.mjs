// 当前 Spec/Backend 实例各走一遍 init → 查询类命令 → sync → 回退。
import fs from 'node:fs';
import path from 'node:path';
import {check} from './lib.mjs';

export const PROFILES = ['spec', 'backend'];
const SPEC_CORE = ['yss-product-lifecycle', 'setup-yss-harness', 'yss-research', 'grilling', 'domain-modeling', 'yss-stage-decision', 'i-have-adhd', 'code-review'];

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
  session.expectOk(['init', '--installation', 'governed', '--profile', profile, '--root', root, ...name, '--plan', '--out', initPlan, '--json'], {label: 'init --plan'});
  check(fs.existsSync(initPlan), `init --plan 未写出计划文件: ${initPlan}`);
  check(!fs.existsSync(root), 'init --plan 不应创建项目目录');
  const applied = session.expectOk(['init', '--profile', profile, '--root', root, '--apply', '--plan-file', initPlan, '--json'], {label: 'init --apply'});
  check(applied.result?.status === 'applied', `init --apply 结果应为 applied，实际 ${applied.result?.status}`);
  check(fs.existsSync(path.join(root, 'yss-project.yaml')) || fs.existsSync(path.join(root, '.yss')), '项目实例缺少身份文件或事务目录');

  if(profile==='spec') {
   const metadata=JSON.parse(fs.readFileSync(path.join(root,'.yss.json')));
   for(const id of SPEC_CORE)check(metadata.distribution.installedSkills.includes(id),`governed core missing ${id}`);
   specReadiness(session,root,SPEC_CORE);
  }

  const json = '--json';
  session.expectOk(['doctor', '--root', root, json], {label: 'doctor'});
  session.expectOk(['diff', '--root', root, json], {label: 'diff'});
  session.expectOk(['context', 'verify', '--root', root, json], {label: 'context verify'});
  const skills = session.expectOk(['skills', 'list', '--root', root, json], {label: 'skills list'});
  check(Array.isArray(skills.result) && skills.result.length > 0, 'skills list 应返回非空标识列表');
  // backend 没有阶段资源，result 为 null；只有 spec 返回登记表。
  const assets = session.expectOk(['assets', 'list', '--root', root, json], {label: 'assets list'});
  if (profile === 'spec') check(assets.result && Object.keys(assets.result).length > 0, 'spec 的 assets list 应返回非空登记表');
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
}

export function runSkillsProfile(session, profile, tempRoot) {
 const root=path.join(tempRoot,`skills-${profile}`),plans=path.join(tempRoot,`skills-plans-${profile}`);
 fs.mkdirSync(plans,{recursive:true});
 const plan=path.join(plans,'init.json');
 session.expectOk(['init','--root',root,'--profile',profile,'--plan','--out',plan,'--json'],{label:'skills default init plan'});
 session.expectOk(['init','--root',root,'--apply','--plan-file',plan,'--json'],{label:'skills init apply'});
 const metadata=JSON.parse(fs.readFileSync(path.join(root,'.yss.json')));
 check(metadata.distribution.installation==='skills','default installation must be skills');
 for(const ref of ['yss-project.yaml','CONTEXT.md','.template-spec'])check(!fs.existsSync(path.join(root,ref)),`ordinary installation unexpectedly requires ${ref}`);
 session.expectOk(['doctor','--root',root,'--json'],{label:'skills doctor without governance'});
 if(profile==='spec')specReadiness(session,root,['implement','code-review']);
 const sync=path.join(plans,'sync.json');
 session.expectOk(['sync','--root',root,'--plan','--out',sync,'--json']);
 const result=session.expectOk(['sync','--root',root,'--apply','--plan-file',sync,'--json']);
 check(result.result.status==='unchanged','fresh ordinary sync must be unchanged');
 session.expectRejected(['lifecycle','query','--root',root,'--json'],{label:'ordinary project cannot run formal governance'});
 if(profile==='spec') {
  const migration=path.join(plans,'governed.json');
  session.expectOk(['migrate','--root',root,'--installation','governed','--plan','--out',migration,'--json']);
  session.expectOk(['migrate','--root',root,'--apply','--plan-file',migration,'--json']);
  const stages={
   'stage.product-design':['yss-prototype-stage','prototype-review','yss-design-system'],
   'stage.system-data-engineering':['yss-technical-design','yss-implementation-contract-compiler'],
   'stage.ticket-formalization':['yss-implementation-contract-compiler'],
  };
  for(const [stage,skills] of Object.entries(stages)) {
   const plan=path.join(plans,stage+'.json');
   session.expectOk(['assets','ensure',stage,'--root',root,'--plan','--out',plan,'--json']);
   session.expectOk(['assets','ensure','--root',root,'--apply','--plan-file',plan,'--json']);
   specReadiness(session,root,skills);
   const repeat=session.expectOk(['assets','ensure',stage,'--root',root,'--plan','--json']).result;
   check(repeat.changes.length===0,`stage ${stage} must be idempotent`);
  }
 }

 if(profile==='spec')for(const installation of ['skills','governed']) {
  const fullRoot=path.join(tempRoot,`full-${installation}-spec`);
  session.expectOk(['init','--root',fullRoot,'--profile','spec','--installation',installation,'--full','--json'],{label:`full ${installation} init`});
  const full=JSON.parse(fs.readFileSync(path.join(fullRoot,'.yss.json')));
  check(full.distribution.installation===installation,'full must preserve selected installation');
  for(const ref of ['yss-project.yaml','CONTEXT.md','.template-spec'])check(fs.existsSync(path.join(fullRoot,ref))===(installation==='governed'),'full governance boundary differs from installation');
  const supported=session.expectOk(['skills','list','--root',fullRoot,'--json']).result;
  check(supported.every(id=>full.distribution.installedSkills.includes(id)),'full must install all supported Profile skills');
  if(installation==='governed') {
   const fixed=session.expectOk(['bundle','inspect','--profile','spec','--json']).result;
   for(const ref of Object.keys(fixed.files))check(fs.existsSync(path.join(fullRoot,ref)),`full governed resource missing ${ref}`);
  }
  specReadiness(session,fullRoot,full.distribution.installedSkills);
 }
}
