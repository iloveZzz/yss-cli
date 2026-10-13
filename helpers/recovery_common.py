import hashlib, json, os, pathlib, re, stat, subprocess, time, shutil

Q = pathlib.Path(__file__).resolve().parent
NODE = shutil.which('node')

def digest(data): return hashlib.sha256(data).hexdigest()
def require(value, message):
    if not value: raise RuntimeError(message)
def save(file, value): file.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')
def ordinary(file):
    file = pathlib.Path(file)
    s = file.lstat()
    require(stat.S_ISREG(s.st_mode) and not file.is_symlink() and s.st_nlink == 1 and file.resolve() == file.absolute(), 'ordinary independent file required: ' + str(file))
    return file
def owned(path):
    p = pathlib.Path(path).absolute()
    require(p != Q and p.is_relative_to(Q) and p.resolve() == p, 'WRITE_SCOPE_VIOLATION: ' + str(p))
    return p
def descriptor(path):
    p = pathlib.Path(path)
    try: s = p.lstat()
    except FileNotFoundError: return {'type': 'absent'}
    mode = stat.S_IMODE(s.st_mode)
    if stat.S_ISLNK(s.st_mode): return {'type': 'symlink', 'link': os.readlink(p), 'mode': mode}
    if stat.S_ISREG(s.st_mode): return {'type': 'file', 'sha256': digest(p.read_bytes()), 'mode': mode}
    if stat.S_ISDIR(s.st_mode): return {'type': 'directory', 'mode': mode}
    raise RuntimeError('unsupported file type: ' + str(p))
def tree(root):
    root = pathlib.Path(root); out = {'.': descriptor(root)}
    def visit(directory):
        for p in sorted(directory.iterdir()):
            d = descriptor(p); out[p.relative_to(root).as_posix()] = d
            if d['type'] == 'directory': visit(p)
    visit(root); return out
def restored_inventory(inventory):
    # Only native transaction history and its newly created parent are additive.
    # Git, metadata, business, modes, symlinks and all other assets stay checked.
    return {k: v for k, v in inventory.items() if k != '.yss' and k != '.yss/transactions' and not k.startswith('.yss/transactions/')}
def environment(output):
    output = pathlib.Path(output); tmp = output / 'tmp'; tmp.mkdir(exist_ok=True)
    return {**os.environ, 'PYTHONDONTWRITEBYTECODE': '1', 'TMPDIR': str(tmp), 'npm_config_cache': str(output / 'npm-cache'), 'npm_config_update_notifier': 'false', 'YSS_RUNTIME_HOME':str(output / 'runtime-store'), 'GIT_OPTIONAL_LOCKS': '0'}
def frozen(binary, sha256, binding):
    binding = ordinary(binding); b = json.loads(binding.read_bytes())
    require(b.get('authorized_by_root') is True, 'BINARY_NOT_FROZEN: root has not authorized this fixed stable input')
    require(re.fullmatch('[a-f0-9]{64}', sha256 or '') is not None, 'BINARY_NOT_FROZEN: sha256 required')
    binary = ordinary(binary)
    require(str(binary.absolute()) == b.get('binary') and sha256 == b.get('binary_sha256') and digest(binary.read_bytes()) == sha256, 'stable binary binding mismatch')
    require(b.get('version') == '2.0.0' and b.get('source_state') == 'committed' and re.fullmatch('[a-f0-9]{40}', b.get('cli_commit') or ''), 'stable committed provenance missing')
    require(b.get('root_authorization_ref'), 'root freeze authorization reference missing')
    require(set(b.get('template_commits',{}))=={'spec','design','backend'} and b.get('template_version')=='4.0.2', 'current three-profile/template version binding required')
    commits=set(b['template_commits'].values());require(len(commits)==1 and re.fullmatch('[a-f0-9]{40}',next(iter(commits))) is not None,'one exact composite template commit required')
    for key,expected in [('cli_root',b['cli_commit']),('template_root',next(iter(commits)))]:
        root=pathlib.Path(b[key]).resolve()
        require(subprocess.check_output(['git','-C',str(root),'rev-parse','HEAD'],text=True).strip()==expected,'fixed source HEAD mismatch')
        require(subprocess.check_output(['git','-C',str(root),'status','--porcelain=v1','--untracked-files=all'],text=True)=='','fixed source must be clean')
    
    return b
def new_output(path):
    path = owned(path)
    require(not path.exists() and not path.is_symlink(), 'fresh output directory required')
    path.mkdir(parents=True)
    require(path.resolve() == path, 'output directory alias refused')
    return path
def native_value(stdout, code, expected='OK'):
    value = json.loads(stdout)
    require(value.get('outputVersion') == 1 and value.get('protocolVersion') == 1, 'native envelope/protocol mismatch')
    require(value.get('code') == expected, 'expected native code ' + expected + ': ' + str(value))
    require(code >= 0 and ((code == 0) == (expected == 'OK')), 'native actual exit mismatches typed result')
    require(value.get('status') == ('ok' if expected == 'OK' else 'error'), 'native status mismatch')
    return value
def command(args, output, rows, expected='OK', legacy=False, timeout=300, legacy_expected_exit=0):
    for flag in ['--root', '--target-dir']:
        if flag in args: owned(args[args.index(flag) + 1])
    n = len(rows) + 1; stem = pathlib.Path(output) / 'logs' / ('command-%04d' % n); stem.parent.mkdir(exist_ok=True)
    start = time.monotonic(); timed_out = False
    try:
        p = subprocess.run(args, cwd=output, env=environment(output), capture_output=True, timeout=timeout); stdout=p.stdout; stderr=p.stderr; code=p.returncode
    except subprocess.TimeoutExpired as e:
        stdout=e.stdout or b''; stderr=e.stderr or b''; code=124; timed_out=True
    stem.with_suffix('.stdout').write_bytes(stdout); stem.with_suffix('.stderr').write_bytes(stderr)
    row={'args': args, 'actual_exit_code': code, 'actual_exit_code_observed': True, 'actual_signal': -code if code < 0 else None, 'timed_out': timed_out, 'timeout_seconds':timeout, 'duration_ms':round((time.monotonic()-start)*1000), 'expected_code':None if legacy else expected, 'stdout_ref':str(stem.with_suffix('.stdout')), 'stdout_sha256':digest(stdout), 'stderr_ref':str(stem.with_suffix('.stderr')), 'stderr_sha256':digest(stderr)}; rows.append(row)
    require(not timed_out and code >= 0, 'command timeout or signal: ' + str(args))
    if legacy:
        require(legacy_expected_exit is None or code == legacy_expected_exit, 'legacy command exit changed: ' + stdout.decode(errors='replace')[-1200:] + stderr.decode(errors='replace')[-1200:])
        try: value=json.loads(stdout)
        except ValueError: value=None
    else: value=native_value(stdout, code, expected)
    return value, code, row
def verify_stable(binary, binding, output, rows):
    value, _, _ = command([str(binary), 'version', '--json'], output, rows)
    v=value['result']; require(v.get('version') == binding['version'] and v.get('sourceState') == 'committed' and v.get('cliCommit') == binding['cli_commit'], 'actual public stable version provenance mismatch')
    inspections={}
    for profile in ['spec', 'design', 'backend']:
        value, _, _=command([str(binary),'bundle','inspect','--profile',profile,'--json'],output,rows)
        v=value['result']; require(v.get('profile') == profile and v.get('sourceState') == 'committed' and v.get('schemaVersion') == 3, 'actual bundle provenance mismatch')
        require(v.get('templateCommit') == binding['template_commits'][profile], 'actual bundle template commit mismatch'); inspections[profile]=v
    return inspections
