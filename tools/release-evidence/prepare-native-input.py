# 由固定 v1.3.6 证据输入准备器改写；不授予任何资格。版本与模板身份取自仓库，不硬编码。
import argparse,json,hashlib,shutil,subprocess,re
from pathlib import Path
from release_source import source_document,repo_version,template_identity,repo_profiles
parser=argparse.ArgumentParser()
parser.add_argument('--cli-root',required=True);parser.add_argument('--evidence-root',required=True)
for name in ['cli-commit','template-commit','template-version']:parser.add_argument('--'+name,help='缺省取自仓库 HEAD / YSS_SOURCE_SNAPSHOT；显式传入时必须一致')
parser.add_argument('--artifact',nargs=3,action='append',required=True,metavar=('PLATFORM','BINARY_REF','RECEIPT_REF'))
parser.add_argument('--output-name',default='native-input.json')
options=parser.parse_args();C=Path(options.cli_root).resolve();R=Path(options.evidence_root).resolve();assert R.is_dir()
version=repo_version(C);lock_commit,lock_version=template_identity(C)
options.cli_commit=options.cli_commit or subprocess.check_output(['git','rev-parse','HEAD'],cwd=C,text=True).strip()
options.template_commit=options.template_commit or lock_commit;options.template_version=options.template_version or lock_version
assert options.template_commit==lock_commit and options.template_version==lock_version,'template identity differs from docs/source-lock.json'
assert re.fullmatch('[a-f0-9]{40}',options.cli_commit) and re.fullmatch('[a-f0-9]{40}',options.template_commit)
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=C,text=True).strip()==options.cli_commit
assert subprocess.check_output(['git','status','--porcelain=v1','--untracked-files=all'],cwd=C,text=True)==''
assert options.output_name==Path(options.output_name).name and not (R/options.output_name).exists()
sha=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
def ordinary(ref):
 assert ref and '\\' not in ref and ':' not in ref and all(x not in ['', '.', '..'] for x in ref.split('/'))
 p=R/ref;assert not Path(ref).is_absolute() and '..' not in Path(ref).parts and p.resolve()==p.absolute() and p.is_file() and not p.is_symlink()
 return p
keys=['cliVersion','protocolVersion','cliCommit','sourceState','sourceLockSha256','bundles'];identity=None;artifacts=[];seen=set()
for platform,binary_ref,receipt_ref in options.artifact:
 assert platform in ['darwin/arm64','windows/amd64'] and platform not in seen;seen.add(platform)
 binary=ordinary(binary_ref);receipt=ordinary(receipt_ref);rc=json.loads(receipt.read_text())
 assert rc['status']=='passed' and rc['exitCode']==0 and rc['inputDrift'] is False and rc['unexecuted']==[]
 assert rc['platform']==platform and rc['runtimePlatform']==platform and rc['runtimeVerification']=='native-runner'
 current={k:rc[k] for k in keys}
 assert current['cliVersion']==version and current['cliCommit']==options.cli_commit and current['sourceState']=='committed'
 assert set(current['bundles'])==set(repo_profiles(C))
 assert all(v['templateCommit']==options.template_commit and v['templateVersion']==options.template_version and v['sourceState']=='committed' for v in current['bundles'].values())
 if identity is None:identity=current
 else:assert identity==current,'cross-platform source identity differs'
 assert sha(binary)==rc['binarySha256']==rc['binaryBeforeSha256']==rc['binaryAfterSha256']
 artifacts.append({'platform':platform,'binary':{'path':binary_ref,'sha256':sha(binary)},'receipt':{'path':receipt_ref,'sha256':sha(receipt)}})
inp={'schemaVersion':1,**identity,'documents':{},'requiredPlatforms':sorted(seen),'qualificationScope':'local-platform-program-install','artifacts':artifacts,'releaseGate':{'path':'','sha256':''}}
for ref in ['docs/source-lock.json','docs/compatibility.md','docs/porting-status.md','docs/native-governance.md','docs/cli-retirement.md','compat/README.md','README.md']:
 source=source_document(C,ref);assert source.is_file() and not source.is_symlink();p=R/'input-documents'/ref;p.parent.mkdir(parents=True,exist_ok=True)
 if p.exists():assert not p.is_symlink() and p.read_bytes()==source.read_bytes()
 else:shutil.copyfile(source,p);p.chmod(0o644)
 inp['documents'][ref]={'path':p.relative_to(R).as_posix(),'sha256':sha(p)}
assert inp['documents']['docs/source-lock.json']['sha256']==inp['sourceLockSha256']
with (R/options.output_name).open('x') as f:f.write(json.dumps(inp,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'status':'input-prepared-not-qualified','file':str(R/options.output_name),'platforms':sorted(seen),'cliCommit':identity['cliCommit']}))
