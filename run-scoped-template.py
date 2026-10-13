import json,hashlib,stat,subprocess,time,os,sys
from pathlib import Path
import argparse
parser=argparse.ArgumentParser(description='Execute current scoped9 against one fixed clean three-profile source')
for key in ['template-root','input','out','binary','cli-root']:parser.add_argument('--'+key,required=True)
options=parser.parse_args();T=Path(options.template_root).resolve();input_file=Path(options.input).resolve();B=input_file.parent;inp=json.loads(input_file.read_text())
assert inp['cliVersion']=='2.0.0' and inp['sourceState']=='committed' and set(inp['bundles'])=={'spec','design','backend'}
EXPECTED={p:v['templateCommit'] for p,v in inp['bundles'].items()};assert len(set(EXPECTED.values()))==1
assert all(v['templateVersion']=='4.0.2' for v in inp['bundles'].values())
ROOTS={p:T for p in ['spec','design','backend']}
binary=Path(options.binary).resolve();binary_sha=hashlib.sha256(binary.read_bytes()).hexdigest()
assert any(a['binary']['sha256']==binary_sha for a in inp['artifacts'])
R=Path(options.out).resolve();R.relative_to(B);assert R!=B and not R.exists() and not R.is_relative_to(T);R.mkdir()
ENV={**os.environ,'GIT_OPTIONAL_LOCKS':'0','YSS_NATIVE_BINARY':str(binary),'YSS_NATIVE_BINARY_SHA256':binary_sha,'YSS_NATIVE_SOURCE_ROOT':str(Path(options.cli_root).resolve())}
assert ENV.get('YSS_NATIVE_BINARY_OVERRIDE')!='1','release scope forbids native override'
def git(root,*args):return subprocess.check_output(['git','--no-optional-locks','-c','diff.autoRefreshIndex=false',*args],cwd=root,text=True,env=ENV).strip()
def observe(root):
 inventory={}
 for item in subprocess.check_output(['git','--no-optional-locks','ls-files','--cached','--others','--exclude-standard','-z'],cwd=root,env=ENV).split(b'\0'):
  if not item:continue
  ref=item.decode();p=root/ref
  try:s=p.lstat()
  except FileNotFoundError:inventory[ref]={'type':'missing'};continue
  if stat.S_ISREG(s.st_mode):inventory[ref]={'type':'file','sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'mode':stat.S_IMODE(s.st_mode)}
  elif stat.S_ISLNK(s.st_mode):inventory[ref]={'type':'symlink','target':os.readlink(p),'mode':stat.S_IMODE(s.st_mode)}
 return {'root':str(root),'head':git(root,'rev-parse','HEAD'),'status':git(root,'status','--porcelain=v1','--untracked-files=all'),'inventory':inventory}
COMMANDS=[['scripts/sync-skills','--check'],['scripts/update-skill-lock','--check'],['node','--test','--test-concurrency=1','tests/profile-tree.test.mjs','tests/advance-maintenance-root.test.mjs'],['node','--test','--test-concurrency=1','tests/verification-artifacts.test.mjs','tests/strategic-handoff-sealed-owner.test.mjs'],['scripts/verify-lifecycle-registry'],['scripts/verify-skill-registry'],['node','--test','--test-concurrency=1','tests/read-only-intake.test.mjs','tests/verification-execution.test.mjs','tests/instance-metadata.test.mjs','.template-source/tooling/node/test/retirement.test.mjs'],['git','diff','--check'],['node','--test','--test-concurrency=1','tests/skill-lock-readers.test.mjs','.template-source/tooling/node/test/skill-supply-chain.test.mjs']]
LABELS=['skills.projection','skills.lock','profiles.projection','handoff.source-lock','lifecycle.registry','skills.registry','affected-source-contract-tests','source.whitespace','skills.source-contract']
if (R/'original-execution.json').exists():raise SystemExit('拒绝覆盖已有9项原始证据')
BEFORE={p:observe(r) for p,r in ROOTS.items()}
for p,v in BEFORE.items():
 if v['root']!=git(ROOTS[p],'rev-parse','--show-toplevel') or v['head']!=EXPECTED[p] or v['status']:raise SystemExit('拒绝不匹配/不clean的固定模板来源:'+p)
(R/'source-before.json').write_text(json.dumps(BEFORE,ensure_ascii=False,indent=2)+'\n')
rows=[];fail=None;timing=time.monotonic(); details=[]
(R/'original-execution.json').write_text('[]\n')
assert git(Path(options.cli_root),'rev-parse','HEAD')==inp['cliCommit'] and not git(Path(options.cli_root),'status','--porcelain=v1','--untracked-files=all')
for i,command in enumerate(COMMANDS):
 current={p:(git(r,'rev-parse','HEAD'),git(r,'status','--porcelain=v1','--untracked-files=all')) for p,r in ROOTS.items()}
 if any(head!=EXPECTED[p] or status for p,(head,status) in current.items()):fail='source-before-command-drift';break
 log=f'{i+1:02d}-{LABELS[i]}.log'; print('START',i+1,' '.join(command),flush=True);started=time.monotonic()
 with (R/log).open('xb') as stream: result=subprocess.run(command,cwd=T,stdout=stream,stderr=subprocess.STDOUT,env=ENV)
 row={'command':command,'cwd':str(T),'source_sha':EXPECTED['spec'],'seconds':time.monotonic()-started,'exit_code':result.returncode,'log':log};rows.append(row)
 (R/'original-execution.json').write_text(json.dumps(rows,ensure_ascii=False,indent=2)+'\n')
 details.append({'original_index':i,'id':LABELS[i],'actual_exit_code_observed':True,'actual_exit_code':result.returncode,'actual_signal':-result.returncode if result.returncode<0 else None,'log':log,'log_sha256':hashlib.sha256((R/log).read_bytes()).hexdigest()})
 print('END',i+1,'exit',result.returncode,'seconds',round(row['seconds'],3),flush=True)
 if result.returncode:fail='command-failed';break
AFTER={p:observe(r) for p,r in ROOTS.items()};(R/'source-after.json').write_text(json.dumps(AFTER,ensure_ascii=False,indent=2)+'\n')
DRIFT={p:[ref for ref in sorted(BEFORE[p]['inventory'].keys()|AFTER[p]['inventory'].keys()) if BEFORE[p]['inventory'].get(ref)!=AFTER[p]['inventory'].get(ref)] for p in ROOTS}
clean=all(AFTER[p]['head']==EXPECTED[p] and not AFTER[p]['status'] for p in ROOTS);drift=any(DRIFT.values()) or not clean or hashlib.sha256(binary.read_bytes()).hexdigest()!=binary_sha
status='passed' if len(rows)==9 and fail is None and not drift else 'failed'
with (R/'combined.log').open('xb') as combined:
 for row in rows:
  combined.write(('COMMAND '+json.dumps(row['command'],ensure_ascii=False)+'\n').encode());combined.write((R/row['log']).read_bytes());combined.write(b'\n')
evidence={'schema_version':1,'kind':'scoped-template-actual-command-observation','status':status,'qualification_scope':'local-platform-impacted-consumers','current_sources':EXPECTED,'source_state':'committed','inputs_current_git_clean':clean,'source_before':str(R/'source-before.json'),'source_after':str(R/'source-after.json'),'input_drift':drift,'changed':DRIFT,'seconds':time.monotonic()-timing,'failure':fail,'commands':details,'unexecuted':[LABELS[i] for i in range(len(rows),9)],'original_execution_sha256':hashlib.sha256((R/'original-execution.json').read_bytes()).hexdigest(),'combined_log_sha256':hashlib.sha256((R/'combined.log').read_bytes()).hexdigest(),'release_receipt_created':False}
(R/'evidence.json').write_text(json.dumps(evidence,ensure_ascii=False,indent=2)+'\n');print(json.dumps({k:v for k,v in evidence.items() if k in ['status','seconds','failure','input_drift','unexecuted','inputs_current_git_clean']},ensure_ascii=False),flush=True)
# The wrapper reuses this invocation's newly observed original array; no historical qualification is imported.
def file_ref(file):return {'path':file.relative_to(B).as_posix(),'sha256':hashlib.sha256(file.read_bytes()).hexdigest()}
report={'schema_version':1,'kind':'scoped-template-consumer-verification','qualification_scope':'local-platform-impacted-consumers','status':status,'input_drift':drift,'unexecuted':[LABELS[i] for i in range(len(rows),9)],'failure':fail,'source_state':'committed','inputs_current_git_clean':clean,'reused':True,'current_sources':EXPECTED,'required_cases':LABELS,'original_execution':file_ref(R/'original-execution.json'),'checks':[{'id':LABELS[i],'original_index':i,'status':'passed' if row['exit_code']==0 else 'failed','actual_exit_code_observed':True,'actual_exit_code':row['exit_code'],'actual_signal':-row['exit_code'] if row['exit_code']<0 else None,'args':row['command'],'log':file_ref(R/row['log'])} for i,row in enumerate(rows)],'observation_dependencies':[file_ref(R/name) for name in ['source-before.json','source-after.json','evidence.json','combined.log']]}
(R/'report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
raise SystemExit(0 if status=='passed' else 1)
