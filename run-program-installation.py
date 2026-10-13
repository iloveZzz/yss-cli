import sys,json,hashlib,stat,subprocess,time,datetime,tarfile,argparse,platform,zipfile
from pathlib import Path
parser=argparse.ArgumentParser(description='Actual native public installation, local-scope 5 cases')
parser.add_argument('--input',required=True);parser.add_argument('--archive',required=True);parser.add_argument('--out',required=True);parser.add_argument('--platform',required=True);parser.add_argument('--cli-root',required=True)
options=parser.parse_args();input_file=Path(options.input).resolve();R=input_file.parent
inp=json.loads(input_file.read_text());target_platform=options.platform
C=Path(options.cli_root).resolve()
assert subprocess.check_output(['git','-C',str(C),'rev-parse','HEAD'],text=True).strip()==inp['cliCommit']
assert subprocess.check_output(['git','-C',str(C),'status','--porcelain=v1','--untracked-files=all'],text=True)==''
assert all(hashlib.sha256((C/ref).read_bytes()).hexdigest()==value['sha256'] for ref,value in inp['documents'].items())
actual_platform=('windows' if sys.platform=='win32' else sys.platform)+'/'+{'arm64':'arm64','aarch64':'arm64','AMD64':'amd64','x86_64':'amd64'}.get(platform.machine(),platform.machine())
assert target_platform in ['darwin/arm64','windows/amd64'] and actual_platform==target_platform,'real matching runner required'
assert inp['cliVersion']=='2.0.0' and inp['sourceState']=='committed' and set(inp['bundles'])=={'spec','design','backend'}
artifact=next(a for a in inp['artifacts'] if a['platform']==target_platform)
binary=R/artifact['binary']['path'];binary_name='yss.exe' if sys.platform=='win32' else 'yss'
archive=Path(options.archive).resolve();archive.relative_to(R)
out=Path(options.out).resolve();out.relative_to(R);assert out!=R and not out.exists();out.mkdir()

sha=lambda p:hashlib.sha256(Path(p).read_bytes()).hexdigest()
now=lambda:datetime.datetime.now(datetime.timezone.utc).isoformat()
def descriptor(p):
 s=p.lstat();assert stat.S_ISREG(s.st_mode) and not p.is_symlink()
 observed=stat.S_IMODE(s.st_mode)
 canonical=(0o444 if not observed&0o200 else (0o755 if p.name=='yss.exe' else 0o644)) if sys.platform=='win32' else observed
 return {'type':'file','mode':canonical,'sha256':sha(p)}
def inventory(p):
 rows={}
 for f in sorted(p.rglob('*')):
  s=f.lstat();assert not f.is_symlink()
  rows[f.relative_to(p).as_posix()]={'type':'directory','mode':stat.S_IMODE(s.st_mode)} if f.is_dir() else descriptor(f)
 return rows
def control(rows):return {k:v for k,v in rows.items() if k!='.yss' and not k.startswith('.yss/')}
def digest(rows):return hashlib.sha256(''.join(k+'\0'+v['sha256']+'\0'+str(v['mode'])+'\n' for k,v in sorted(rows.items())).encode()).hexdigest()
def write(p,v):p.write_text(json.dumps(v,ensure_ascii=False,indent=2)+'\n')
coverage=['install-plan-read-only','install-apply','rollback','recover','user-file-preservation']
report={'schema_version':1,'kind':'native-program-installation','fixture':False,'status':'running','platform':target_platform,'binary_sha256':sha(binary),'cli_commit':inp['cliCommit'],'cli_version':inp['cliVersion'],'source_lock_sha256':inp['sourceLockSha256'],'candidate_archive':{'path':archive.relative_to(R).as_posix(),'sha256':sha(archive)},'input_drift':False,'unexecuted':coverage.copy(),'required_cases':coverage,'cases':[],'commands':[],'provenance_commands':[],'started_at':now(),'error':None,'inventory_contract':'Whole control inventory excludes durable .yss transaction history. Empty docs and compat directories exist before preview. Raw file modes and bytes are measured.'}
inputs=[binary,archive,input_file,Path(__file__).resolve()]+[R/v['path'] for v in inp['documents'].values()]
before={str(p):descriptor(p) for p in inputs};write(out/'inputs-before.json',before)
def call(args,exe=binary):
 n=len(report['commands'])+len(report['provenance_commands'])+1
 so=out/f'{n:02}.stdout.log';se=out/f'{n:02}.stderr.log';began=time.monotonic()
 with so.open('xb') as o,se.open('xb') as e:
  proc=subprocess.run([str(exe)]+args,cwd=out,stdout=o,stderr=e,timeout=180)
 row={'args':[str(exe)]+args,'cwd':str(out),'actual_exit_code_observed':True,'actual_exit_code':proc.returncode if proc.returncode>=0 else None,'actual_signal':-proc.returncode if proc.returncode<0 else None,'timed_out':False,'duration_ms':round((time.monotonic()-began)*1000),'stdout':so.relative_to(R).as_posix(),'stderr':se.relative_to(R).as_posix(),'stdout_sha256':sha(so),'stderr_sha256':sha(se),'error':None}
 report['commands' if args[0]=='update' else 'provenance_commands'].append(row)
 assert proc.returncode==0,(args,proc.returncode,se.read_text())
 value=json.loads(so.read_bytes());assert value['status']=='ok' and value['code']=='OK'
 if args[0]=='update':assert descriptor(user)==business['user-data/readonly.txt']
 return value['result']
def passed(case,**values):
 report['cases'].append({'case':case,'passed':True,**values});report['unexecuted'].remove(case)
try:
 assert sha(binary)==artifact['binary']['sha256']
 payload={}
 archive_files={}
 if sys.platform=='win32':
  with zipfile.ZipFile(archive) as z:
   for m in z.infolist():
    assert not m.flag_bits&1 and not m.is_dir() and not stat.S_ISLNK(m.external_attr>>16)
    assert m.filename not in archive_files and not Path(m.filename).is_absolute() and '..' not in Path(m.filename).parts and '\\' not in m.filename
    archive_files[m.filename]=(z.read(m), (m.external_attr>>16)&0o777)
 else:
  with tarfile.open(archive,'r:gz') as t:
   for m in t.getmembers():
    assert m.isfile() and not m.issym() and not m.islnk() and m.name not in archive_files
    assert not Path(m.name).is_absolute() and '..' not in Path(m.name).parts
    archive_files[m.name]=(t.extractfile(m).read(),m.mode&0o777)
 assert set(archive_files)==set([binary_name,'release-manifest.json']+list(inp['documents']))
 manifest=json.loads(archive_files['release-manifest.json'][0])
 assert manifest['platform']==target_platform and manifest['cliCommit']==inp['cliCommit'] and manifest['cliVersion']==inp['cliVersion'] and manifest['sourceState']=='committed'
 for name,(body,mode) in archive_files.items():
  if name=='release-manifest.json':continue
  expected_mode=0o755 if name==binary_name else 0o644
  assert mode==expected_mode and manifest['files'][name]=={'type':'file','digest':hashlib.sha256(body).hexdigest(),'mode':expected_mode}
  expected=artifact['binary']['sha256'] if name==binary_name else inp['documents'][name]['sha256']
  assert hashlib.sha256(body).hexdigest()==expected
  payload[name]={'type':'file','mode':expected_mode,'sha256':expected}
 report['mode_semantics']='Windows owner-write/read-only attribute and canonical archive executable mode; NTFS ACL not executed' if sys.platform=='win32' else 'exact POSIX 0777 permissions'

 report['package_payload_sha256']=digest(payload)
 tool=out/'tool';(tool/'user-data').mkdir(parents=True)
 for name in ['docs','compat']:(tool/name).mkdir(mode=0o755)
 user=tool/'user-data'/'readonly.txt';user.write_bytes(b'user-owned read-only bytes\x00\n');user.chmod(0o444)
 baseline=inventory(tool);business={'user-data/readonly.txt':descriptor(user)};write(out/'baseline-inventory.json',baseline)
 planfile=out/'plan.json';plan=call(['update','plan','--tool-root',str(tool),'--artifact',str(archive),'--sha256',sha(archive),'--out',str(planfile),'--json'])
 assert json.loads(planfile.read_text())==plan and inventory(tool)==baseline
 passed('install-plan-read-only',whole_tree_unchanged=True,saved_plan_sha256=sha(planfile))
 applied=call(['update','apply','--tool-root',str(tool),'--plan-file',str(planfile),'--json'])
 actual={ref:descriptor(tool/ref) for ref in payload};assert actual==payload
 report['installed_raw_modes']={ref:stat.S_IMODE((tool/ref).stat().st_mode) for ref in payload}
 version=call(['version','--json'],tool/binary_name);assert version['version']==inp['cliVersion'] and version['cliCommit']==inp['cliCommit'] and version['sourceState']=='committed'
 for profile in ['spec','design','backend']:
  observed=call(['bundle','inspect','--profile',profile,'--json'],tool/binary_name);assert all(observed[k]==v for k,v in inp['bundles'][profile].items())
 passed('install-apply',installed_payload_sha256=digest(actual),transaction_id=applied['transactionId'])
 rb=call(['update','rollback','--tool-root',str(tool),'--json']);assert control(inventory(tool))==baseline
 passed('rollback',whole_inventory_restored=True,transaction_id=rb['transactionId'])
 recovery_before=inventory(tool)
 call(['update','recover','--tool-root',str(tool),'--json']);call(['update','recover','--tool-root',str(tool),'--json'])
 assert inventory(tool)==recovery_before
 passed('recover',repeated_recovery=True,no_overwrite=True)
 business_after={'user-data/readonly.txt':descriptor(user)};assert business==business_after
 passed('user-file-preservation',business_before=business,business_after=business_after)
 write(out/'restored-inventory.json',inventory(tool))
 report['status']='passed'
except Exception as exc:
 report['status']='failed';report['error']=repr(exc)
finally:
 after={str(p):descriptor(p) for p in inputs};write(out/'inputs-after.json',after)
 report['input_drift']=after!=before
 if report['input_drift']:report['status']='failed'
 report['finished_at']=now();write(out/'report.json',report)
 print(json.dumps({'status':report['status'],'cases':len(report['cases']),'commands':len(report['commands']),'input_drift':report['input_drift'],'unexecuted':report['unexecuted']}))
raise SystemExit(0 if report['status']=='passed' else 1)
