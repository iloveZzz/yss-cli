import hashlib,json,subprocess,shutil,argparse
from pathlib import Path

parser=argparse.ArgumentParser(description='Wrap only observed current raw reports for the current assembler')
for name in ['input','cli-root','platform','program-report','program-execution','legacy-report','legacy-execution','scoped-report','scoped-execution','out']:parser.add_argument('--'+name,required=True)
options=parser.parse_args();input_file=Path(options.input).resolve();R=input_file.parent;C=Path(options.cli_root).resolve()

load=lambda p:json.loads(p.read_text())
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
inp=load(input_file)
identity={k:inp[k] for k in ['cliVersion','protocolVersion','cliCommit','sourceState','sourceLockSha256','bundles']}
artifact=next(x for x in inp['artifacts'] if x['platform']==options.platform);binary=artifact['binary']['sha256']
assert sha(R/artifact['binary']['path'])==binary
assert inp['cliVersion']=='2.0.0' and set(inp['bundles'])=={'spec','design','backend'}
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=C,text=True).strip()==identity['cliCommit']
assert subprocess.check_output(['git','status','--porcelain=v1','--untracked-files=all'],cwd=C,text=True)==''
scope='local-platform-impacted-consumers'
# This release qualifies only Mac; the separate Windows archive is build-only.
assert inp['qualificationScope']==scope and inp['requiredPlatforms']==['darwin/arm64']
assert [x['platform'] for x in inp['artifacts']]==['darwin/arm64']
assert options.platform=='darwin/arm64','legacy36 gate is bound to the actual Mac binary'
out=Path(options.out).resolve();out.relative_to(R);assert out!=R;out.mkdir()
def ref(p):
 p=p.resolve();assert p.is_file() and not p.is_symlink()
 return {'path':p.relative_to(R).as_posix(),'sha256':sha(p)}
def write(p,o):
 assert not p.exists();p.write_text(json.dumps(o,ensure_ascii=False,indent=2)+'\n')
def observed(p):
 o=load(p)
 code=o.get('exit_code',o.get('actual_exit_code'))
 assert code==0 and o['input_drift'] is False
 assert o.get('actual_signal') is None
 return o
checks=[]
definitions=[
 ('program-installation',Path(options.program_report).resolve(),Path(options.program_execution).resolve()),
 ('legacy-recovery',Path(options.legacy_report).resolve(),Path(options.legacy_execution).resolve()),
 ('scoped-template-consumers',Path(options.scoped_report).resolve(),Path(options.scoped_execution).resolve())]
program_cases=['install-plan-read-only','install-apply','rollback','recover','user-file-preservation']
legacy_cases=['warehouse-fixed-executor-init','legacy-requires-explicit-migration','migration-plan-read-only','migration-apply','native-doctor','migration-rollback','fixed-executor-maintains-restored-instance','repeat-rollback','legacy-pending-migration-refusal','unfinished-fixed-executor-recovery','modified-after-apply-rollback-refusal','protection-bytes-and-mode']
scoped_cases=['skills.projection','skills.lock','profiles.projection','handoff.source-lock','lifecycle.registry','skills.registry','affected-source-contract-tests','source.whitespace','skills.source-contract']
for gate_id,path,execution in definitions:
 report=load(path);observation=observed(execution)
 assert report['schema_version']==1 and report['status']=='passed' and report['input_drift'] is False and report['unexecuted']==[]
 deps=[]
 if gate_id=='program-installation':
  assert report['binary_sha256']==binary and report['cli_commit']==identity['cliCommit']
  assert report['required_cases']==program_cases
  coverage=program_cases
  assert len(report['cases'])==5 and all(x['passed'] for x in report['cases'])
  deps.append(R/report['candidate_archive']['path'])
  for row in report['commands']+report['provenance_commands']:
   assert row['actual_exit_code_observed'] and row['actual_exit_code']==0 and row['actual_signal'] is None
   for k in ['stdout','stderr']:
    p=R/row[k];assert sha(p)==row[k+'_sha256'];deps.append(p)
  stdout=Path(observation['stdout_ref']);stderr=Path(observation['stderr_ref'])
 elif gate_id=='legacy-recovery':
  assert report['binary_sha256']==binary and report['required_cases']==legacy_cases
  coverage=[p+'/'+c for p in ['spec','design','backend'] for c in legacy_cases]
  assert len(report['cases'])==36 and sorted(x['family']+'/'+x['case'] for x in report['cases'])==sorted(coverage)
  for row in report['cases']:
   assert row['passed'];p=path.parent/row['log_ref'];assert sha(p)==row['log_sha256'];deps.append(p)
  for row in report['packages']:
   p=Path(row['ref']);assert sha(p)==row['sha256'];target=R/'historical-package-dependencies'/p.name;target.parent.mkdir(exist_ok=True);shutil.copyfile(p,target);assert sha(target)==row['sha256'];deps.append(target)
  deps.extend([execution,Path(observation['stdout_ref']),Path(observation['stderr_ref']),path.parent/'public-validator.execution.json',path.parent/'public-validator.stdout',path.parent/'public-validator.stderr'])
  stdout=Path(observation['stdout_ref']);stderr=Path(observation['stderr_ref'])
 else:
  assert report['required_cases']==scoped_cases and report['reused'] is True
  assert report['current_sources']=={k:v['templateCommit'] for k,v in identity['bundles'].items()}
  coverage=scoped_cases
  p=R/report['original_execution']['path'];assert sha(p)==report['original_execution']['sha256'];deps.append(p)
  original=load(p);assert len(original)==9 and all(x['exit_code']==0 for x in original)
  for row in report['checks']:
   p=R/row['log']['path'];assert sha(p)==row['log']['sha256'];deps.append(p)
  stdout=Path(observation['stdout_ref']);stderr=Path(observation['stderr_ref'])
 sources=[{'kind':report['kind'],'schemaVersion':1,'report':ref(path)}]
 if gate_id=='scoped-template-consumers': deps.extend([R/x['path'] for x in report['observation_dependencies']])
 sources.extend({'kind':'evidence-file','schemaVersion':0,'report':ref(p)} for p in dict.fromkeys(deps))
 gate={'schemaVersion':1,'kind':'stable-release-gate-execution','gateId':gate_id,'qualificationScope':scope,**identity,'platform':options.platform,'binarySha256':binary,'status':'passed','exitCode':0,'signal':None,'error':None,'inputDrift':False,'unexecuted':[],'coverage':coverage,'sourceReports':sources}
 target=out/(gate_id+'.json');write(target,gate)
 checks.append({'id':gate_id,'command':observation.get('command',observation.get('args')),'status':'passed','exitCode':0,'signal':None,'inputDrift':False,'unexecuted':[],'report':ref(target),'stdout':ref(stdout),'stderr':ref(stderr)})
receipt={'schemaVersion':1,'qualificationScope':scope,**identity,'status':'passed','exitCode':0,'inputDrift':False,'unexecuted':[],'checks':checks}
write(out/'release-gate.json',receipt)
release_input={**inp,'releaseGate':ref(out/'release-gate.json')}
write(R/'release-input.json',release_input)
assert sha(R/artifact['binary']['path'])==binary
print(json.dumps({'status':'prepared-from-observed-passed-reports','gates':len(checks),'coverage':{x['gateId']:len(x['coverage']) for x in [load(out/(g[0]+'.json')) for g in definitions]},'cli_commit':identity['cliCommit']}))
