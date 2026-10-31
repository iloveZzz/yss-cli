"""只包装本次实际观察到的 program-installation 原始报告，生成 release-gate 与 release-input。"""
import hashlib,json,subprocess,argparse
from pathlib import Path
parser=argparse.ArgumentParser()
for name in ['input','cli-root','platform','program-report','program-execution','out']:parser.add_argument('--'+name,required=True)
o=parser.parse_args();input_file=Path(o.input).resolve();R=input_file.parent;C=Path(o.cli_root).resolve()
load=lambda p:json.loads(p.read_text());sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
inp=load(input_file);scope='local-platform-program-install'
identity={k:inp[k] for k in ['cliVersion','protocolVersion','cliCommit','sourceState','sourceLockSha256','bundles']}
assert inp['qualificationScope']==scope and inp['requiredPlatforms']==['darwin/arm64'] and o.platform=='darwin/arm64'
assert [x['platform'] for x in inp['artifacts']]==['darwin/arm64']
artifact=inp['artifacts'][0];binary=artifact['binary']['sha256'];assert sha(R/artifact['binary']['path'])==binary
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=C,text=True).strip()==identity['cliCommit']
assert subprocess.check_output(['git','status','--porcelain=v1','--untracked-files=all'],cwd=C,text=True)==''
out=Path(o.out).resolve();out.relative_to(R);assert out!=R;out.mkdir()
def ref(p):
 p=p.resolve();assert p.is_file() and not p.is_symlink();return {'path':p.relative_to(R).as_posix(),'sha256':sha(p)}
def write(p,v):
 assert not p.exists();p.write_text(json.dumps(v,ensure_ascii=False,indent=2)+'\n')
report_path=Path(o.program_report).resolve();report=load(report_path);obs=load(Path(o.program_execution).resolve())
assert obs['exit_code']==0 and obs['input_drift'] is False and obs.get('actual_signal') is None
cases=['install-plan-read-only','install-apply','reinstall','user-file-preservation']
assert report['schema_version']==1 and report['status']=='passed' and report['input_drift'] is False and report['unexecuted']==[]
assert report['binary_sha256']==binary and report['cli_commit']==identity['cliCommit'] and report['required_cases']==cases
assert len(report['cases'])==len(cases) and all(x['passed'] for x in report['cases'])
deps=[R/report['candidate_archive']['path']]
for row in report['commands']+report['provenance_commands']:
 assert row['actual_exit_code_observed'] and row['actual_exit_code']==0 and row['actual_signal'] is None
 for k in ['stdout','stderr']:
  p=R/row[k];assert sha(p)==row[k+'_sha256'];deps.append(p)
stdout=Path(obs['stdout_ref']);stderr=Path(obs['stderr_ref'])
sources=[{'kind':report['kind'],'schemaVersion':1,'report':ref(report_path)}]+[{'kind':'evidence-file','schemaVersion':0,'report':ref(p)} for p in dict.fromkeys(deps)]
gate={'schemaVersion':1,'kind':'stable-release-gate-execution','gateId':'program-installation','qualificationScope':scope,**identity,'platform':o.platform,'binarySha256':binary,'status':'passed','exitCode':0,'signal':None,'error':None,'inputDrift':False,'unexecuted':[],'coverage':cases,'sourceReports':sources}
target=out/'program-installation.json';write(target,gate)
check={'id':'program-installation','command':obs['command'],'status':'passed','exitCode':0,'signal':None,'inputDrift':False,'unexecuted':[],'report':ref(target),'stdout':ref(stdout),'stderr':ref(stderr)}
write(out/'release-gate.json',{'schemaVersion':1,'qualificationScope':scope,**identity,'status':'passed','exitCode':0,'inputDrift':False,'unexecuted':[],'checks':[check]})
write(R/'release-input.json',{**inp,'releaseGate':ref(out/'release-gate.json')})
print(json.dumps({'status':'prepared-from-observed-passed-report','cli_commit':identity['cliCommit']}))
