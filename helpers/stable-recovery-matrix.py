#!/usr/bin/env python3
"""Real public fixed legacy executors and native migration/recovery acceptance.
Does not import private legacy modules, construct journals, or edit source trees.
"""
import argparse, datetime, hashlib, json, os, pathlib, shutil, signal, stat, subprocess, sys, time, tarfile

HERE = pathlib.Path(__file__).resolve().parent
RUN = HERE
import recovery_common as RC
FAMILIES = ['spec', 'design', 'backend']
REQUIRED = ['warehouse-fixed-executor-init', 'legacy-requires-explicit-migration', 'migration-plan-read-only', 'migration-apply', 'native-doctor', 'migration-rollback', 'fixed-executor-maintains-restored-instance', 'repeat-rollback', 'legacy-pending-migration-refusal', 'unfinished-fixed-executor-recovery', 'modified-after-apply-rollback-refusal', 'protection-bytes-and-mode']
META = {p: '.yss-template.json' if p == 'spec' else '.yss-harness-' + p + '.json' for p in FAMILIES}
parser = argparse.ArgumentParser()
parser.add_argument('--binary', required=True)
parser.add_argument('--binary-sha256', required=True)
parser.add_argument('--output', required=True)
parser.add_argument('--workspace', required=True)
parser.add_argument('--template-root', required=True)
parser.add_argument('--binding', default=str(HERE / 'binding.json'))
parser.add_argument('--pin-file', default=str(RUN / 'legacy-executors-fixed.json'))
parser.add_argument('--node', default=RC.NODE)
options = parser.parse_args()
if sys.platform != 'darwin' or __import__('platform').machine() != 'arm64': raise RuntimeError('legacy36 requires actual darwin/arm64 runner')
RC.Q=pathlib.Path(options.workspace).resolve();assert RC.Q.is_dir() and not (RC.Q/'.git').exists()
BINARY = pathlib.Path(options.binary).absolute()
binding = RC.frozen(BINARY, options.binary_sha256, options.binding)
assert pathlib.Path(options.template_root).resolve()==pathlib.Path(binding['template_root']).resolve()
for key in ['cli_root','template_root']:
 source=pathlib.Path(binding[key]).resolve();assert not RC.Q.is_relative_to(source) and not source.is_relative_to(RC.Q),'legacy workspace must not overlap sources'
OUTPUT = RC.new_output(options.output)
RC.Q=OUTPUT
NODE = options.node
pin_file = RC.ordinary(options.pin_file)
require_pin_sha = binding['legacy_pin_sha256']
if RC.digest(pin_file.read_bytes()) != require_pin_sha: raise RuntimeError('fixed legacy pins drift')
pins = json.loads(pin_file.read_bytes())
assert len(pins)==3 and [p['package'] for p in pins]==['create-yss-spec','create-yss-harness-design','create-yss-harness-backend']
pin_by_family = dict(zip(FAMILIES, pins))
REPORT = OUTPUT / 'legacy-recovery.json'
run_stamp = datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S')
base = OUTPUT / 'fixtures'
base.mkdir()
LOGS = OUTPUT / 'logs'
LOGS.mkdir()
report = {'schema_version': 1, 'kind': 'native-legacy-recovery-matrix', 'fixture': False, 'source_state': 'committed', 'binary_ref': str(BINARY), 'binary_sha256': options.binary_sha256, 'status': 'running', 'families': FAMILIES, 'required_cases': REQUIRED, 'input_drift': False, 'unexecuted': [p + '/' + c for p in FAMILIES for c in REQUIRED], 'packages': [], 'cases': [], 'fixture_directory': str(base), 'node_ref': NODE, 'executed_at': datetime.datetime.now(datetime.timezone.utc).isoformat()}


def digest(data): return hashlib.sha256(data).hexdigest()
def require(value, message):
    if not value: raise RuntimeError(message)
def save(): REPORT.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n')
def descriptor(file):
    s = file.lstat()
    require(stat.S_ISREG(s.st_mode), 'expected ordinary protected file ' + str(file))
    return {'sha256': digest(file.read_bytes()), 'mode': stat.S_IMODE(s.st_mode)}
def tree(root):
    out = {}
    for file in sorted(root.rglob('*')):
        s = file.lstat(); ref = file.relative_to(root).as_posix()
        if file.is_symlink(): out[ref] = {'type': 'symlink', 'link': os.readlink(file)}
        elif file.is_file(): out[ref] = descriptor(file)
    return out
def protected(root, family):
    return {ref: descriptor(root / ref) for ref in [META[family], 'CONTEXT.md', 'yss-project.yaml', '.git/index', 'src/business.txt', 'src/executable-business.sh', '.github/workflows/user.yml']}
def assert_protected(root, expected, evidence, phase):
    actual = {ref: descriptor(root / ref) for ref in expected}
    require(actual == expected, phase + ': protected bytes/mode changed')
    evidence.append({'kind': 'protection-check', 'phase': phase, 'root': str(root), 'expected': expected, 'actual': actual, 'passed': True})
command_rows = []
def execute(args, evidence, expected='OK', old=False, check_code=True):
    value, code, row = RC.command(args, OUTPUT, command_rows, expected=expected, legacy=old, timeout=300)
    evidence.append({'kind': 'command', **row, 'exit_code': code, 'stdout': pathlib.Path(row['stdout_ref']).read_text(), 'stderr': pathlib.Path(row['stderr_ref']).read_text()})
    save()
    return value, code

def record(family, case, evidence, exit_code, expected):
    log = LOGS / (family + '-' + case + '.jsonl')
    log.write_text(''.join(json.dumps(e, ensure_ascii=False) + '\n' for e in evidence))
    row = {'family': family, 'case': case, 'exit_code': exit_code, 'expected_code': expected, 'passed': True, 'log_ref': log.relative_to(OUTPUT).as_posix(), 'log_sha256': digest(log.read_bytes()), 'legacy_package_sha256': pin_by_family[family]['packageSha256'], 'executed_at': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    report['cases'].append(row); report['unexecuted'].remove(family + '/' + case); save()
    print(family + ' ' + case + ' passed', flush=True)
def native(root, family, *args): return [str(BINARY), *args, '--root', str(root), '--profile', family, '--json']
def old(pin, root, *args): return [NODE, pin['binary'], *args, '--target-dir', str(root), '--json']

installed_before = {}
installed_roots = {}
private_executor_before = {}
active_evidence = []
active_case = None
try:
    require(digest(BINARY.read_bytes()) == options.binary_sha256, 'native binary digest mismatch')
    report['stable_bundle_inspections'] = RC.verify_stable(BINARY, binding, OUTPUT, command_rows)
    report['commands'] = command_rows
    report['root_binding'] = binding
    report['root_binding_sha256'] = digest(pathlib.Path(options.binding).read_bytes())
    for family, pin in pin_by_family.items():
        archive = pathlib.Path(pin['packagePath'])
        require(digest(archive.read_bytes()) == pin['packageSha256'], 'legacy archive digest mismatch')
        installed_module = pathlib.Path(pin['binary']).parents[1]
        with tarfile.open(archive) as package:
            for member in package.getmembers():
                if not member.isfile(): continue
                ref = pathlib.PurePosixPath(member.name).relative_to('package')
                require('..' not in ref.parts and not ref.is_absolute(), 'legacy archive path escape')
                file = installed_module / ref
                require(file.is_file() and not file.is_symlink() and file.read_bytes() == package.extractfile(member).read(), 'installed legacy executor differs from fixed package: ' + str(ref))
        installed_root = pathlib.Path(pin['binary']).parents[3]
        installed_roots[family] = installed_root
        installed_before[family] = tree(installed_root)
        copy = OUTPUT / 'executors' / pin['package']
        copy.parent.mkdir(exist_ok=True)
        shutil.copytree(installed_root, copy, symlinks=True)
        pin['binary'] = str(copy / pathlib.Path(pin['binary']).relative_to(installed_root))
        private_executor_before[family] = tree(copy)
        report['packages'].append({'family': family, 'ref': str(archive), 'sha256': pin['packageSha256'], 'version': pin['version'], 'cli_commit': pin['cliCommit']})
    save()
    for family, pin in pin_by_family.items():
        root = base / family; protection_evidence = []
        done = {row['case'] for row in report['cases'] if row['family'] == family}
        if 'warehouse-fixed-executor-init' not in done:
            active_case = family + '/warehouse-fixed-executor-init'; active_evidence = []
            args = [NODE, pin['binary']] + ([] if family == 'spec' else ['init']) + ['--project-name', '真实固定包恢复矩阵' + family, '--business-domain', '隔离恢复验证', '--team-size', '3', '--target-dir', str(root), '--issue-tracker', 'github'] + (['--agent-runtime', 'codex'] if family == 'spec' else [])
            _, code = execute(args, active_evidence, old=True)
            require(not (root / '.yss.json').exists(), 'legacy init produced native metadata')
            execute(['git', 'init', '--quiet', str(root)], active_evidence, old=True)
            for ref, data, mode in [('src/business.txt', b'Business bytes protected by migration\n', 0o600), ('src/executable-business.sh', b'#!/bin/sh\nexit 0\n', 0o751), ('.github/workflows/user.yml', b'name: User workflow\n', 0o600)]:
                file = root / ref; file.parent.mkdir(parents=True, exist_ok=True); file.write_bytes(data); os.chmod(file, mode)
            execute(['git', '-C', str(root), 'add', '--', 'src/business.txt', 'src/executable-business.sh', '.github/workflows/user.yml'], active_evidence, old=True)
            guard = protected(root, family)
            interrupted_root = base / (family + '-interrupted')
            shutil.copytree(root, interrupted_root, symlinks=True)
            # Copying a Git checkout changes inode ctime. Synchronize the isolated
            # clone's public Git index before binding its raw-byte protection guard.
            execute(['git', '-C', str(interrupted_root), 'add', '--', 'src/business.txt', 'src/executable-business.sh', '.github/workflows/user.yml'], active_evidence, old=True)
            interrupted_guard = protected(interrupted_root, family)
            record(family, 'warehouse-fixed-executor-init', active_evidence, code, None)
        else:
            guard = protected(root, family)
            interrupted_root = base / (family + '-interrupted')
            interrupted_guard = protected(interrupted_root, family)
        whole_before = RC.tree(root)
        for case, args, expected in [
            ('legacy-requires-explicit-migration', native(root, family, 'sync'), 'MIGRATION_REQUIRED'),
            ('migration-plan-read-only', native(root, family, 'migrate', 'plan', '--out', str(base / (family + '-native-plan.json'))), 'OK'),
            ('migration-apply', native(root, family, 'migrate', 'apply', '--plan-file', str(base / (family + '-native-plan.json'))), 'OK'),
            ('native-doctor', native(root, family, 'doctor'), 'OK'),
            ('migration-rollback', native(root, family, 'migrate', 'rollback'), 'OK'),
            ('fixed-executor-maintains-restored-instance', old(pin, root, 'doctor'), None),
            ('repeat-rollback', native(root, family, 'migrate', 'rollback'), 'OK')]:
            if case in done: continue
            active_case = family + '/' + case; active_evidence = []
            before = tree(root) if case == 'migration-plan-read-only' else None
            whole_readonly_before = RC.tree(root) if case in ['legacy-requires-explicit-migration', 'repeat-rollback', 'fixed-executor-maintains-restored-instance'] else None
            _, code = execute(args, active_evidence, expected=expected, old=expected is None)
            if before is not None:
                require(tree(root) == before, 'native plan mutated project')
                active_evidence.append({'kind': 'read-only-tree-check', 'before_digest': digest(json.dumps(before, sort_keys=True).encode()), 'after_digest': digest(json.dumps(tree(root), sort_keys=True).encode()), 'passed': True})
            if case in ['migration-apply', 'migration-rollback', 'repeat-rollback']:
                assert_protected(root, guard, protection_evidence, case)
            if case in ['migration-rollback', 'repeat-rollback']:
                require(not (root / '.yss.json').exists(), 'rollback retained native metadata')
                require(RC.restored_inventory(RC.tree(root)) == RC.restored_inventory(whole_before), 'whole project bytes/type/mode did not restore')
            if whole_readonly_before is not None:
                require(RC.tree(root) == whole_readonly_before, 'refusal/doctor/repeated rollback wrote project including transaction history')
            record(family, case, active_evidence, code, expected)

        if 'unfinished-fixed-executor-recovery' not in done:
            # Create a genuine unfinished migration using only its pinned public CLI.
            active_case = family + '/legacy-pending-migration-refusal'; active_evidence = []
            m = json.loads((interrupted_root / META[family]).read_text())
            forbidden = {META[family], 'CONTEXT.md', 'yss-project.yaml', '.template-spec/process/harness-profile.yaml', 'docs/process/harness-profile.yaml', 'AGENTS.md', 'README.md', 'skills-lock.json', '.gitignore'}
            available = [ref for ref in sorted(m['managedFiles']) if ref not in forbidden and (interrupted_root / ref).is_file() and not (interrupted_root / ref).is_symlink() and not ref.startswith(('.git/', 'src/', '.github/'))]
            remove_count = min(200 if family == 'spec' else 300, len(available))
            require(remove_count >= 100, 'insufficient genuine managed removal set')
            removed = available[:remove_count]
            removed_before = {ref: descriptor(interrupted_root / ref) for ref in removed}
            for ref in removed: (interrupted_root / ref).unlink()
            active_evidence.append({'kind': 'managed-removal-setup', 'root': str(interrupted_root), 'count': len(removed), 'files': removed_before})
            interrupted_before = tree(interrupted_root)
            oldplan = base / (family + '-old-interrupt-plan.json'); archive = base / (family + '-old-interrupt-archive')
            value, _ = execute(old(pin, interrupted_root, 'migrate', 'plan', '--output', str(oldplan), '--archive-dir', str(archive)), active_evidence, old=True)
            planned = json.loads(oldplan.read_text())
            require(len(planned['operations']) >= remove_count, 'old public plan did not restore removed assets')
            require(tree(interrupted_root) == interrupted_before, 'old public plan wrote target')
            argv = old(pin, interrupted_root, 'migrate', 'apply', '--plan', str(oldplan))
            killed_out = LOGS / (family + '-killed-old-apply.stdout'); killed_err = LOGS / (family + '-killed-old-apply.stderr')
            start = time.monotonic(); first_ref = None
            with killed_out.open('wb') as stdout, killed_err.open('wb') as stderr:
                proc = subprocess.Popen(argv, stdout=stdout, stderr=stderr, cwd=OUTPUT, env=RC.environment(OUTPUT))
                while proc.poll() is None and time.monotonic() - start < 180:
                    first_ref = next((ref for ref in removed if (interrupted_root / ref).is_file()), None)
                    if first_ref is not None:
                        os.kill(proc.pid, signal.SIGKILL); break
                    time.sleep(0.001)
                if proc.poll() is None: proc.kill()
                killed_exit = proc.wait(timeout=10)
            require(first_ref is not None and killed_exit == -signal.SIGKILL, 'did not observe and kill real old target write')
            journals = list((interrupted_root / '.yss-harness-state').rglob('journal.json'))
            pending = [{'ref': f.relative_to(interrupted_root).as_posix(), 'sha256': digest(f.read_bytes()), 'phase': json.loads(f.read_text()).get('phase')} for f in journals if json.loads(f.read_text()).get('phase') in ['backup', 'apply', 'verify', 'recovery-failed']]
            require(pending, 'killed public executor left no real pending journal')
            active_evidence.append({'kind': 'actual-sigkill', 'args': argv, 'exit_code': killed_exit, 'signal': 'SIGKILL', 'first_target_write': first_ref, 'elapsed_ms': round((time.monotonic() - start) * 1000), 'old_plan_operations': len(planned['operations']), 'pending': pending, 'stdout': killed_out.read_text(), 'stderr': killed_err.read_text()})
            assert_protected(interrupted_root, interrupted_guard, protection_evidence, 'actual-old-executor-interrupted')
            before_refusal = tree(interrupted_root)
            _, code = execute(native(interrupted_root, family, 'migrate', 'plan'), active_evidence, expected='LEGACY_INTERRUPTED')
            require(tree(interrupted_root) == before_refusal, 'native pending refusal wrote legacy instance')
            record(family, 'legacy-pending-migration-refusal', active_evidence, code, 'LEGACY_INTERRUPTED')

            active_case = family + '/unfinished-fixed-executor-recovery'; active_evidence = []
            recovered, _ = execute(old(pin, interrupted_root, 'migrate', 'recover', '--apply'), active_evidence, old=True)
            require(recovered and recovered.get('status') == 'recovered', 'old public recover did not report recovered')
            require(tree(interrupted_root).get(META[family]) == interrupted_before.get(META[family]), 'old recovery altered original metadata bytes/mode')
            for ref in removed: require(not (interrupted_root / ref).exists(), 'old recovery did not restore pre-apply missing asset state')
            assert_protected(interrupted_root, interrupted_guard, protection_evidence, 'fixed-old-recover')
            repair_plan = base / (family + '-old-repair-plan.json'); repair_archive = base / (family + '-old-repair-archive')
            execute(old(pin, interrupted_root, 'migrate', 'plan', '--output', str(repair_plan), '--archive-dir', str(repair_archive)), active_evidence, old=True)
            execute(old(pin, interrupted_root, 'migrate', 'apply', '--plan', str(repair_plan)), active_evidence, old=True)
            for ref, expected in removed_before.items(): require(descriptor(interrupted_root / ref) == expected, 'old public repair asset mismatch ' + ref)
            execute(old(pin, interrupted_root, 'doctor'), active_evidence, old=True)
            # A successful legacy repair legitimately updates legacy metadata, so bind
            # the exact repaired snapshot as native migration/rollback's baseline.
            repaired_guard = protected(interrupted_root, family)
            require(all(repaired_guard[ref] == interrupted_guard[ref] for ref in interrupted_guard if ref != META[family]), 'public legacy repair modified protected business/identity/index')
            newplan = base / (family + '-repaired-native-plan.json')
            execute(native(interrupted_root, family, 'migrate', 'plan', '--out', str(newplan)), active_evidence)
            execute(native(interrupted_root, family, 'migrate', 'apply', '--plan-file', str(newplan)), active_evidence)
            execute(native(interrupted_root, family, 'doctor'), active_evidence)
            assert_protected(interrupted_root, repaired_guard, protection_evidence, 'native-migration-after-fixed-repair')
            _, code = execute(native(interrupted_root, family, 'migrate', 'rollback'), active_evidence)
            require(not (interrupted_root / '.yss.json').exists(), 'repaired migration rollback retained native metadata')
            assert_protected(interrupted_root, repaired_guard, protection_evidence, 'native-rollback-after-fixed-repair')
            execute(old(pin, interrupted_root, 'doctor'), active_evidence, old=True)
            record(family, 'unfinished-fixed-executor-recovery', active_evidence, code, 'OK')

        active_case = family + '/modified-after-apply-rollback-refusal'; active_evidence = []
        refusal_root = base / (family + '-after-apply-refusal-' + run_stamp)
        init_args = [NODE, pin['binary']] + ([] if family == 'spec' else ['init']) + ['--project-name', '真实回退拒绝' + family, '--business-domain', '受管修改保护', '--team-size', '3', '--target-dir', str(refusal_root), '--issue-tracker', 'github'] + (['--agent-runtime', 'codex'] if family == 'spec' else [])
        execute(init_args, active_evidence, old=True)
        execute(['git', 'init', '--quiet', str(refusal_root)], active_evidence, old=True)
        for ref, data, mode in [('src/business.txt', b'Business bytes protected by migration\n', 0o600), ('src/executable-business.sh', b'#!/bin/sh\nexit 0\n', 0o751), ('.github/workflows/user.yml', b'name: User workflow\n', 0o600)]:
            file = refusal_root / ref; file.parent.mkdir(parents=True, exist_ok=True); file.write_bytes(data); os.chmod(file, mode)
        execute(['git', '-C', str(refusal_root), 'add', '--', 'src/business.txt', 'src/executable-business.sh', '.github/workflows/user.yml'], active_evidence, old=True)
        refusal_guard = protected(refusal_root, family)
        refusal_plan = base / (family + '-after-apply-plan-' + run_stamp + '.json')
        execute(native(refusal_root, family, 'migrate', 'plan', '--out', str(refusal_plan)), active_evidence)
        execute(native(refusal_root, family, 'migrate', 'apply', '--plan-file', str(refusal_plan)), active_evidence)
        assert_protected(refusal_root, refusal_guard, protection_evidence, 'fresh-native-apply-before-rollback-refusal')
        original_plan = json.loads(refusal_plan.read_text())
        forbidden = {META[family], 'CONTEXT.md', 'yss-project.yaml', '.template-spec/process/harness-profile.yaml', 'docs/process/harness-profile.yaml', 'AGENTS.md', 'README.md', 'skills-lock.json', '.gitignore'}
        candidate = next(c['path'] for c in original_plan['changes'] if c['path'] not in forbidden and c['ownership'] == 'managed' and (refusal_root / c['path']).is_file() and c['before']['type'] == 'file' and c['after']['type'] == 'file' and c['before']['digest'] != c['after']['digest'])
        target = refusal_root / candidate; target.write_bytes(target.read_bytes() + b'\n# actual post-apply user change\n'); modified = descriptor(target)
        before = tree(refusal_root)
        active_evidence.append({'kind': 'actual-post-apply-modification-before-first-rollback', 'path': candidate, 'modified': modified, 'root': str(refusal_root), 'legacy_metadata': descriptor(refusal_root / META[family]), 'native_metadata': descriptor(refusal_root / '.yss.json')})
        refused, code = execute(native(refusal_root, family, 'migrate', 'rollback'), active_evidence, expected='CONCURRENT', check_code=False)
        require(descriptor(target) == modified and tree(refusal_root) == before, 'refused rollback overwrote user change or partially restored assets/metadata/state')
        assert_protected(refusal_root, refusal_guard, protection_evidence, 'post-apply-first-rollback-refusal')
        active_evidence.append({'kind': 'whole-tree-rollback-refusal-check', 'path': candidate, 'actual': modified, 'tree_unchanged': True, 'binding_applicability': 'plain public CLI instance; plugin-specific binding migration is separately verified'})
        require(code != 0 and refused.get('code') == 'CONCURRENT', 'rollback refusal classification mismatch: actual ' + str(refused))
        record(family, 'modified-after-apply-rollback-refusal', active_evidence, code, 'CONCURRENT')
        active_case = family + '/protection-bytes-and-mode'; active_evidence = protection_evidence
        assert_protected(root, guard, active_evidence, 'post-rollback-modification-refusal')
        record(family, 'protection-bytes-and-mode', active_evidence, 0, None)
    require(not report['unexecuted'], 'required cases remain unexecuted')
    report['input_drift'] = digest(BINARY.read_bytes()) != options.binary_sha256 or any(digest(pathlib.Path(pin['packagePath']).read_bytes()) != pin['packageSha256'] or tree(installed_roots[family]) != installed_before[family] or tree(pathlib.Path(pin['binary']).parents[3]) != private_executor_before[family] for family, pin in pin_by_family.items())
    require(not report['input_drift'], 'binary, archive or installed executor input drift')
    report['status'] = 'passed'
except Exception as e:
    report['status'] = 'failed'; report['failure'] = str(e); report['failed_case'] = active_case
    if active_evidence:
        file = LOGS / ('failure-' + (active_case or 'setup').replace('/', '-') + '.jsonl')
        file.write_text(''.join(json.dumps(x, ensure_ascii=False) + '\n' for x in active_evidence))
        report['failure_log_ref'] = file.relative_to(OUTPUT).as_posix(); report['failure_log_sha256'] = digest(file.read_bytes())
    print(str(e), file=sys.stderr, flush=True)
finally:
    report['finished_at'] = datetime.datetime.now(datetime.timezone.utc).isoformat(); report['commands'] = command_rows; save()
matrix_exit = 0 if report['status'] == 'passed' and not report['input_drift'] else 1
validator_actual_exit = None
if matrix_exit == 0:
    args = [NODE, str(HERE / 'validate-matrix.mjs'), '--report', str(REPORT), '--sha256', digest(REPORT.read_bytes()), '--binary-sha256', options.binary_sha256, '--template-root', options.template_root]
    started = time.monotonic()
    verified = subprocess.run(args, capture_output=True, cwd=OUTPUT, env=RC.environment(OUTPUT), timeout=300)
    validator_actual_exit = verified.returncode
    (OUTPUT / 'public-validator.stdout').write_bytes(verified.stdout)
    (OUTPUT / 'public-validator.stderr').write_bytes(verified.stderr)
    RC.save(OUTPUT / 'public-validator.execution.json', {'args': args, 'actual_exit_code': verified.returncode, 'duration_ms': round((time.monotonic()-started)*1000), 'report_sha256':digest(REPORT.read_bytes()), 'stdout_sha256':digest(verified.stdout), 'stderr_sha256':digest(verified.stderr)})
    if verified.returncode != 0: matrix_exit = 1
print(json.dumps({'status':report['status'], 'report':str(REPORT), 'actual_case_count':len(report['cases']), 'unexecuted':report['unexecuted'], 'public_validator_actual_exit':validator_actual_exit,'aggregate_exit':matrix_exit}, ensure_ascii=False), flush=True)
sys.exit(matrix_exit)
