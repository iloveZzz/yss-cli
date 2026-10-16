import sys,json,hashlib,stat,subprocess,time,datetime,tarfile,argparse,platform,zipfile,re
from pathlib import Path
from release_source import repo_version
parser=argparse.ArgumentParser(description='Actual native public installation, local-scope 5 cases')
parser.add_argument('--input',required=True);parser.add_argument('--archive',required=True);parser.add_argument('--out',required=True);parser.add_argument('--platform',required=True);parser.add_argument('--cli-root',required=True)
parser.add_argument('--published-sha256',help='Independent expected public archive SHA256; additionally run the real online upgrade using its extracted executor')
options=parser.parse_args()
if options.published_sha256 is not None:
 if not re.fullmatch('[a-f0-9]{64}',options.published_sha256):parser.error('--published-sha256 must be an exact lowercase SHA256')
 if hashlib.sha256(Path(options.archive).read_bytes()).hexdigest()!=options.published_sha256:parser.error('public archive differs from independent --published-sha256')
input_file=Path(options.input).resolve();R=input_file.parent
inp=json.loads(input_file.read_text());target_platform=options.platform
C=Path(options.cli_root).resolve()
assert subprocess.check_output(['git','-C',str(C),'rev-parse','HEAD'],text=True).strip()==inp['cliCommit']
assert subprocess.check_output(['git','-C',str(C),'status','--porcelain=v1','--untracked-files=all'],text=True)==''
assert all(hashlib.sha256((C/ref).read_bytes()).hexdigest()==value['sha256'] for ref,value in inp['documents'].items())
actual_platform=('windows' if sys.platform=='win32' else sys.platform)+'/'+{'arm64':'arm64','aarch64':'arm64','AMD64':'amd64','x86_64':'amd64'}.get(platform.machine(),platform.machine())
assert target_platform in ['darwin/arm64','windows/amd64'] and actual_platform==target_platform,'real matching runner required'
assert inp['cliVersion']==repo_version(C) and inp['sourceState']=='committed' and set(inp['bundles'])=={'spec','design','backend'}
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
if options.published_sha256 is not None:
 report['online_commands']=[]
 report['public_upgrade']={'status':'not-started','expected_archive_sha256':options.published_sha256,'required_checks':['public-executor','online-install','installed-identity-receipt-status','rollback-repeat-recover','user-file-preservation'],'checks':[]}
inputs=[binary,archive,input_file,Path(__file__).resolve(),Path(__file__).resolve().with_name('release_source.py')]+[R/v['path'] for v in inp['documents'].values()]
before={str(p):descriptor(p) for p in inputs};write(out/'inputs-before.json',before)
def call(args,exe=binary,channel=None,timeout=180):
 n=len(report['commands'])+len(report['provenance_commands'])+len(report.get('online_commands',[]))+1
 so=out/f'{n:02}.stdout.log';se=out/f'{n:02}.stderr.log';began=time.monotonic()
 timed_out=False;exit_code=None;signal=None
 with so.open('xb') as o,se.open('xb') as e:
  try:
   proc=subprocess.run([str(exe)]+args,cwd=out,stdout=o,stderr=e,timeout=timeout)
   exit_code=proc.returncode if proc.returncode>=0 else None;signal=-proc.returncode if proc.returncode<0 else None
  except subprocess.TimeoutExpired:timed_out=True
 row={'args':[str(exe)]+args,'cwd':str(out),'actual_exit_code_observed':not timed_out,'actual_exit_code':exit_code,'actual_signal':signal,'timed_out':timed_out,'duration_ms':round((time.monotonic()-began)*1000),'stdout':so.relative_to(R).as_posix(),'stderr':se.relative_to(R).as_posix(),'stdout_sha256':sha(so),'stderr_sha256':sha(se),'error':'command timed out' if timed_out else None}
 report[channel or ('commands' if args[0]=='update' else 'provenance_commands')].append(row)
 assert not timed_out and exit_code==0,(args,exit_code,signal,timed_out,se.read_text())
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
 if options.published_sha256 is not None:
  assert manifest['stableReady'] is True and manifest['runtimeVerification']=='passed','public upgrade requires the actual stable release archive'
  assert manifest['sourceLockSha256']==inp['sourceLockSha256'] and set(manifest['bundles'])=={'spec','design','backend'}
  assert all(all(manifest['bundles'][profile][key]==value for key,value in expected.items()) for profile,expected in inp['bundles'].items())
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
 if options.published_sha256 is not None:
  online=report['public_upgrade'];online['status']='running'
  executor_dir=out/'public-executor';executor_dir.mkdir()
  public_exe=executor_dir/binary_name
  public_exe.write_bytes(archive_files[binary_name][0]);public_exe.chmod(0o755)
  public_before=descriptor(public_exe);assert public_before['sha256']==artifact['binary']['sha256']
  online['executor']={'path':public_exe.relative_to(R).as_posix(),'sha256':sha(public_exe),'source_archive_sha256':sha(archive),'source_member':binary_name}
  online['checks'].append({'check':'public-executor','passed':True})
  online_tool=out/'online-tool';(online_tool/'user-data').mkdir(parents=True)
  for name in ['docs','compat']:(online_tool/name).mkdir(mode=0o755)
  online_user=online_tool/'user-data'/'readonly.txt';online_user.write_bytes(b'online user-owned read-only bytes\x00\n');online_user.chmod(0o444)
  online_business=descriptor(online_user);online_baseline=inventory(online_tool);write(out/'online-baseline-inventory.json',online_baseline)
  def online_call(args,exe=public_exe):
   result=call(args,exe,channel='online_commands',timeout=960 if args[0]=='upgrade' else 180)
   assert descriptor(online_user)==online_business and descriptor(public_exe)==public_before
   return result
  installed=online_call(['upgrade','--to',inp['cliVersion'],'--tool-root',str(online_tool),'--json'])
  assert installed['status']=='installed' and installed['currentVersion']=='' and installed['targetVersion']==inp['cliVersion'] and installed['updateAvailable'] is False
  assert installed['platform']==target_platform and Path(installed['toolRoot']).resolve()==online_tool
  assert installed['releaseUrl']=='https://github.com/iloveZzz/yss-cli/releases/tag/v'+inp['cliVersion']
  assert installed['archiveSha256']==options.published_sha256,'online archive drifted from the independently verified public download'
  assert installed['transaction']['transactionId'] and installed['transaction']['kind']=='program-update'
  online['checks'].append({'check':'online-install','passed':True,'archive_sha256':installed['archiveSha256'],'transaction':installed['transaction']})
  actual={ref:descriptor(online_tool/ref) for ref in payload};assert actual==payload
  assert (online_tool/'release-manifest.json').read_bytes()==archive_files['release-manifest.json'][0]
  version=online_call(['version','--json'],online_tool/binary_name)
  assert version['version']==inp['cliVersion'] and version['cliCommit']==inp['cliCommit'] and version['sourceState']=='committed'
  for profile in ['spec','design','backend']:
   observed=online_call(['bundle','inspect','--profile',profile,'--json'],online_tool/binary_name)
   assert all(observed[k]==v for k,v in inp['bundles'][profile].items())
  receipt_path=online_tool/'installation.json';receipt=json.loads(receipt_path.read_bytes())
  assert receipt=={'schemaVersion':1,'cliVersion':inp['cliVersion'],'protocolVersion':manifest['protocolVersion'],'platform':target_platform,'archiveDigest':options.published_sha256,'sourceState':'committed','stableReady':True}
  installed_inventory=inventory(online_tool);write(out/'online-installed-inventory.json',installed_inventory)
  status=online_call(['update','status','--tool-root',str(online_tool),'--json'])
  assert status['installed'] is True and status['installation']==receipt and status['installationConsistent'] is True
  assert status['recoveryRequired'] is False and status['projectMigrationPerformed'] is False and status['diagnostic']['consistent'] is True
  assert inventory(online_tool)==installed_inventory,'update status must be read-only'
  online['checks'].append({'check':'installed-identity-receipt-status','passed':True,'receipt':receipt,'receipt_sha256':sha(receipt_path),'payload_sha256':digest(actual)})
  rollback=online_call(['update','rollback','--tool-root',str(online_tool),'--json'])
  assert rollback['status']=='rolled-back' and rollback['transactionId']==installed['transaction']['transactionId']
  assert control(inventory(online_tool))==online_baseline
  restored=inventory(online_tool)
  repeated=online_call(['update','rollback','--tool-root',str(online_tool),'--json']);assert repeated['status']=='unchanged'
  for _ in range(2):
   recovered=online_call(['update','recover','--tool-root',str(online_tool),'--json']);assert recovered['status']=='unchanged'
  assert inventory(online_tool)==restored
  final_status=online_call(['update','status','--tool-root',str(online_tool),'--json'])
  assert final_status['installed'] is False and final_status['recoveryRequired'] is False and inventory(online_tool)==restored
  online['checks'].append({'check':'rollback-repeat-recover','passed':True,'whole_inventory_restored':True,'repeated_rollback':True,'repeated_recover':True})
  assert descriptor(online_user)==online_business and descriptor(user)==business['user-data/readonly.txt']
  online['checks'].append({'check':'user-file-preservation','passed':True,'before':online_business,'after':descriptor(online_user)})
  assert [c['check'] for c in online['checks']]==online['required_checks']
  write(out/'online-restored-inventory.json',restored);online['status']='passed'
 report['status']='passed'
except Exception as exc:
 report['status']='failed';report['error']=repr(exc)
 if options.published_sha256 is not None:report['public_upgrade']['status']='failed'
finally:
 after={str(p):descriptor(p) for p in inputs};write(out/'inputs-after.json',after)
 report['input_drift']=after!=before
 if report['input_drift']:report['status']='failed'
 report['finished_at']=now();write(out/'report.json',report)
 print(json.dumps({'status':report['status'],'cases':len(report['cases']),'commands':len(report['commands']),'input_drift':report['input_drift'],'unexecuted':report['unexecuted']}))
raise SystemExit(0 if report['status']=='passed' else 1)
