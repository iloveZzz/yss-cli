"""External evidence recorder; never changes a source repository."""
import hashlib,json,os,pathlib,subprocess,sys,time
record_path=pathlib.Path(sys.argv[1])
cwd=pathlib.Path(sys.argv[2])
roots=[pathlib.Path(p) for p in json.loads(sys.argv[3])]
command=sys.argv[4:]
def observe(root):
    names=subprocess.check_output(['git','-C',str(root),'ls-files','--cached','--others','--exclude-standard','-z']).split(b'\0')
    digest=hashlib.sha256(); count=0
    for raw in sorted(set(filter(None,names))):
        path=root/os.fsdecode(raw)
        if not path.exists() and not path.is_symlink():
            digest.update(raw+b'\0missing\0');continue
        if path.is_dir(): continue
        mode=path.lstat().st_mode&0o777
        kind=b'link' if path.is_symlink() else b'file'
        content=os.fsencode(os.readlink(path)) if path.is_symlink() else path.read_bytes()
        digest.update(raw+b'\0'+kind+b'\0'+str(mode).encode()+b'\0'+hashlib.sha256(content).digest());count+=1
    return {'head':subprocess.check_output(['git','-C',str(root),'rev-parse','HEAD'],text=True).strip(),'status':subprocess.check_output(['git','-C',str(root),'status','--porcelain=v1','--untracked-files=all'],text=True),'files':count,'input_digest':digest.hexdigest()}
before={str(p):observe(p) for p in roots}
started=time.time()
log_path=record_path.with_suffix('.log')
stdout_path=record_path.with_suffix('.stdout');stderr_path=record_path.with_suffix('.stderr')
with stdout_path.open('xb') as output,stderr_path.open('xb') as errors:
    completed=subprocess.run(command,cwd=cwd,stdout=output,stderr=errors)
log_path.write_bytes(stdout_path.read_bytes()+stderr_path.read_bytes())
after={str(p):observe(p) for p in roots}
result={'kind':'development-validation-not-release','command':command,'cwd':str(cwd),'exit_code':completed.returncode,'elapsed_seconds':round(time.time()-started,3),'before':before,'after':after,'input_drift':before!=after,'log':str(log_path),'stdout_ref':str(stdout_path),'stderr_ref':str(stderr_path),'actual_signal':-completed.returncode if completed.returncode<0 else None}
record_path.write_text(json.dumps(result,ensure_ascii=False,indent=2)+'\n')
print(json.dumps({k:v for k,v in result.items() if k not in ('before','after')},ensure_ascii=False))
sys.exit(completed.returncode or (1 if result['input_drift'] else 0))
