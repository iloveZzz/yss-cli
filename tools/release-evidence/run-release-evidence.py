#!/usr/bin/env python3
"""按固定顺序串起发版证据的全部本地步骤；任一步失败立即停止，其余步骤如实记为 not-run。

子命令：
  prepare        构建 → 原生 receipt → 候选包 → 程序安装 → 发行门禁 → 稳定包 → Windows 仅构建包 → 产物核对 → upload 目录。
  verify-public  发布后：用独立获得的 --published-sha256 对公开归档做真实在线升级验证。

不会打 tag、不会创建 GitHub Release、不会 push；这些对外动作由人确认后手动执行（见 README）。
脚本不放宽任何校验，也不伪造回执：每个步骤只有真实退出码为 0 才记 passed。
记录写在 <证据目录>/orchestrator/run-log.json，每步结束即落盘。
"""
import argparse, datetime, hashlib, json, os, platform, re, subprocess, sys, time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from release_source import repo_version  # noqa: E402

SCOPE = 'local-platform-program-install'
PLATFORM = 'darwin/arm64'
sha = lambda p: hashlib.sha256(Path(p).read_bytes()).hexdigest()
now = lambda: datetime.datetime.now(datetime.timezone.utc).isoformat()


class StepFailed(Exception):
    pass


class Run:
    def __init__(self, command, evidence, cli_root):
        self.evidence, self.cli_root = evidence, cli_root
        self.dir = evidence / 'orchestrator'
        self.log_path = self.dir / 'run-log.json'
        self.log = {'schemaVersion': 1, 'kind': 'release-evidence-run', 'command': command, 'status': 'running',
                    'startedAt': now(), 'finishedAt': None, 'cliRoot': str(cli_root), 'evidenceDir': str(evidence),
                    'host': {'platform': sys.platform, 'machine': platform.machine()}, 'steps': []}

    def save(self):
        tmp = self.log_path.with_suffix('.tmp')
        tmp.write_text(json.dumps(self.log, ensure_ascii=False, indent=2) + '\n')
        os.replace(tmp, self.log_path)

    def plan(self, steps):
        self.log['steps'] = [{'id': s['id'], 'description': s['description'], 'status': 'not-run'} for s in steps]
        self.save()

    def row(self, step_id):
        return next(r for r in self.log['steps'] if r['id'] == step_id)

    def execute(self, step):
        row = self.row(step['id'])
        row['status'], row['startedAt'] = 'running', now()
        self.save()
        began = time.monotonic()
        try:
            if 'check' in step:  # 进程内检查，失败抛 StepFailed
                row['detail'] = step['check']()
                code = 0
            else:
                argv = step['argv']() if callable(step['argv']) else step['argv']
                cwd = step.get('cwd', self.cli_root)
                row.update(argv=argv, cwd=str(cwd))
                so, se = self.dir / (step['id'] + '.stdout'), self.dir / (step['id'] + '.stderr')
                with so.open('xb') as o, se.open('xb') as e:
                    proc = subprocess.run(argv, cwd=cwd, stdout=o, stderr=e)
                row.update(stdout=so.name, stderr=se.name, stdoutSha256=sha(so), stderrSha256=sha(se))
                code = proc.returncode
                row['exitCode'] = code if code >= 0 else None
                row['signal'] = -code if code < 0 else None
                if code == 0 and step.get('after'):
                    row['detail'] = step['after'](so.read_text())
            row['status'] = 'passed' if code == 0 else 'failed'
        except KeyboardInterrupt:
            row['status'] = 'cancelled'
            raise
        except Exception as exc:  # 含 StepFailed、断言、找不到文件
            row['status'], row['error'] = 'failed', f'{type(exc).__name__}: {exc}'
        finally:
            row['durationMs'] = round((time.monotonic() - began) * 1000)
            self.save()
        if row['status'] != 'passed':
            raise StepFailed(step['id'])

    def finish(self, status):
        self.log['status'], self.log['finishedAt'] = status, now()
        self.log['notExecuted'] = [r['id'] for r in self.log['steps'] if r['status'] in ('not-run', 'running', 'cancelled')]
        self.save()


def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args], text=True).strip()


def preflight(C, E):
    """前置条件检查，不放宽任何一项；通过后返回摘要。"""
    problems = []
    if sys.platform != 'darwin' or platform.machine() not in ('arm64', 'aarch64'):
        problems.append(f'需要真实 darwin/arm64 运行机，当前为 {sys.platform}/{platform.machine()}')
    for tool, probe in (('go', ['go', 'version']), ('node', ['node', '--version']), ('python3', ['python3', '--version']), ('git', ['git', '--version'])):
        try:
            out = subprocess.run(probe, capture_output=True, text=True, check=True)
        except (OSError, subprocess.CalledProcessError):
            problems.append(f'找不到可用的 {tool}')
            continue
        if tool == 'node' and int(re.match(r'v(\d+)', out.stdout).group(1)) < 24:
            problems.append('需要 Node 24 或更高: ' + out.stdout.strip())
    if git(C, 'rev-parse', '--show-toplevel') != str(C):
        problems.append('--cli-root 必须是 Git 仓库根目录')
    if git(C, 'status', '--porcelain=v1', '--untracked-files=all'):
        problems.append('源码仓库不干净：必须是已提交的干净源码')
    if E.exists():
        problems.append('证据目录已存在：必须使用全新目录')
    if E == C or C in E.parents:
        problems.append('证据目录必须在仓库之外')
    if problems:
        raise StepFailed('; '.join(problems))
    return {'cliCommit': git(C, 'rev-parse', 'HEAD'), 'version': repo_version(C), 'go': subprocess.check_output(['go', 'version'], text=True).strip(),
            'node': subprocess.check_output(['node', '--version'], text=True).strip(), 'python': platform.python_version()}


def source_unchanged(C, commit):
    def check():
        if git(C, 'rev-parse', 'HEAD') != commit or git(C, 'status', '--porcelain=v1', '--untracked-files=all'):
            raise StepFailed('源码仓库在运行期间发生了变化（HEAD 或工作区漂移）')
        return {'cliCommit': commit, 'clean': True}
    return check


def prepare(options):
    C = Path(options.cli_root).resolve()
    E = Path(options.evidence_dir).absolute()
    E = E.parent.resolve() / E.name
    py, T = 'python3', HERE
    info = preflight(C, E)  # 失败时尚未创建任何目录，无处记录；直接以非 0 退出并打印原因
    version, commit = info['version'], info['cliCommit']
    E.mkdir(parents=False)
    (E / 'orchestrator').mkdir()
    (E / 'native-darwin-arm64').mkdir()  # native-receipt 要求一个已存在的空目录
    run = Run('prepare', E, C)
    run.log['preflight'] = info
    candidate = E / 'candidate' / f'yss_{version}_darwin_arm64.tar.gz'
    final = E / 'release-final' / f'yss_{version}_darwin_arm64.tar.gz'
    mac_manifest = E / 'mac-release-manifest.json'
    state = {}

    def binary_sha():
        return sha(E / 'build-darwin' / 'yss')

    def remember_manifest_sha(stdout):
        state['mac_manifest_sha256'] = json.loads(stdout)['sha256']
        return state.copy()

    fetch = ['node', 'tools/fetch-bundle.mjs'] + (['--source-root', options.template_source_root] if options.template_source_root else [])
    steps = [
        {'id': '01-fetch-bundle', 'description': '生成并校验内嵌 Bundle 归档（不入库）', 'argv': fetch},
        {'id': '02-build-darwin', 'description': '构建 darwin/arm64 二进制并记录 build-receipt', 'argv': ['node', 'tools/build-candidate.mjs', '--root', str(C), '--out', str(E / 'build-darwin')]},
        {'id': '03-native-receipt', 'description': '原生 receipt：只执行 version 与来源锁中全部现役 Profile 的 bundle inspect，checks 为空',
         'argv': lambda: ['node', str(T / 'native-receipt.mjs'), '--binary', str(E / 'build-darwin' / 'yss'), '--out', str(E / 'native-darwin-arm64'),
                          '--artifact-prefix', 'native-darwin-arm64', '--cli-root', str(C), '--binary-sha256', binary_sha()]},
        {'id': '04-prepare-native-input', 'description': '生成 native-input.json（不授予资格）',
         'argv': [py, str(T / 'prepare-native-input.py'), '--cli-root', str(C), '--evidence-root', str(E),
                  '--artifact', PLATFORM, 'build-darwin/yss', 'native-darwin-arm64/native-receipt.json']},
        {'id': '05-package-candidate', 'description': '候选包（stableReady=false，仅供真实安装验证）',
         'argv': ['go', 'run', './tools/package', '--native-candidate-manifest', str(E / 'native-input.json'),
                  '--qualification-scope', SCOPE, '--required-platforms', PLATFORM, str(E / 'candidate')]},
        {'id': '06-run-program-installation', 'description': '对候选包实际执行离线程序安装五项（plan/apply/rollback/recover/用户文件保护）',
         'argv': [py, str(T / 'run-observed.py'), str(E / 'program-runner.json'), str(C), json.dumps([str(C)]),
                  py, str(T / 'run-program-installation.py'), '--input', str(E / 'native-input.json'), '--archive', str(candidate),
                  '--out', str(E / 'program-installation'), '--platform', PLATFORM, '--cli-root', str(C)]},
        {'id': '07-prepare-gate', 'description': '仅包装实际观察到的 passed 报告，生成 release-gate 与 release-input',
         'argv': [py, str(T / 'prepare-gate.py'), '--input', str(E / 'native-input.json'), '--cli-root', str(C), '--platform', PLATFORM,
                  '--program-report', str(E / 'program-installation' / 'report.json'), '--program-execution', str(E / 'program-runner.json'),
                  '--out', str(E / 'release-gates')]},
        {'id': '08-package-stable', 'description': '稳定包（装配器完整校验 releaseGate 与全部输入）',
         'argv': ['go', 'run', './tools/package', '--native-manifest', str(E / 'release-input.json'),
                  '--qualification-scope', SCOPE, '--required-platforms', PLATFORM, str(E / 'release-final')]},
        {'id': '09-extract-mac-manifest', 'description': '取出稳定包 release-manifest.json 作为 Windows 打包的同提交来源清单',
         'argv': [py, str(T / 'extract-release-manifest.py'), '--archive', str(final), '--out', str(mac_manifest)], 'after': remember_manifest_sha},
        {'id': '10-package-windows', 'description': 'Windows 仅构建打包（stableReady=false，不授予在线升级资格）',
         'argv': lambda: [py, str(T / 'package-windows-artifact.py'), '--cli-root', str(C), '--source-manifest', str(mac_manifest),
                          '--source-manifest-sha256', state['mac_manifest_sha256'], '--out', str(E / 'windows-artifact')]},
        {'id': '11-check-outputs', 'description': '只读核对资格结论与摘要绑定',
         'argv': [py, str(T / 'check-outputs.py'), '--evidence-dir', str(E), '--cli-root', str(C)]},
        {'id': '12-collect-upload', 'description': '整理 upload 目录并生成 SHA256SUMS（不上传）',
         'argv': [py, str(T / 'collect-upload.py'), '--evidence-dir', str(E), '--cli-root', str(C)]},
        {'id': '13-source-unchanged', 'description': '确认源码仓库 HEAD 与工作区在全过程中未变化', 'check': source_unchanged(C, commit)},
    ]
    run.plan(steps)
    return drive(run, steps, E)


def verify_public(options):
    C = Path(options.cli_root).resolve()
    E = Path(options.evidence_dir).resolve()
    archive = Path(options.public_archive).resolve()
    assert (E / 'native-input.json').is_file(), '证据目录缺少 native-input.json：需使用 prepare 生成的目录'
    assert not (E / 'public-installation').exists() and not (E / 'orchestrator' / 'verify-public-log.json').exists(), '公开验证输出必须是新的'
    assert archive.is_file() and E in archive.parents, '公开归档必须是证据目录内的普通文件（先下载到 public-download/）'
    if not re.fullmatch('[a-f0-9]{64}', options.published_sha256):
        sys.exit('--published-sha256 必须是 64 位小写十六进制')
    (E / 'orchestrator').mkdir(exist_ok=True)
    run = Run('verify-public', E, C)
    run.log_path = E / 'orchestrator' / 'verify-public-log.json'
    steps = [
        {'id': 'p1-public-upgrade', 'description': '用独立获得的 --published-sha256 对公开归档做真实在线升级验证',
         'argv': ['python3', str(HERE / 'run-observed.py'), str(E / 'public-program-runner.json'), str(C), json.dumps([str(C)]),
                  'python3', str(HERE / 'run-program-installation.py'), '--input', str(E / 'native-input.json'), '--archive', str(archive),
                  '--out', str(E / 'public-installation'), '--platform', PLATFORM, '--cli-root', str(C),
                  '--published-sha256', options.published_sha256]},
        {'id': 'p2-source-unchanged', 'description': '确认源码仓库未变化', 'check': source_unchanged(C, git(C, 'rev-parse', 'HEAD'))},
    ]
    run.plan(steps)
    return drive(run, steps, E)


def drive(run, steps, E):
    try:
        for step in steps:
            run.execute(step)
    except StepFailed as exc:
        run.finish('failed')
        print(json.dumps({'status': 'failed', 'failedStep': str(exc), 'log': str(run.log_path), 'notExecuted': run.log['notExecuted']}, ensure_ascii=False), file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        run.finish('cancelled')
        print(json.dumps({'status': 'cancelled', 'log': str(run.log_path), 'notExecuted': run.log['notExecuted']}, ensure_ascii=False), file=sys.stderr)
        return 130
    run.finish('passed')
    print(json.dumps({'status': 'passed', 'log': str(run.log_path), 'evidenceDir': str(E)}, ensure_ascii=False))
    return 0


def main():
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest='command', required=True)
    p = sub.add_parser('prepare')
    p.add_argument('--evidence-dir', required=True, help='仓库外的全新目录（不得已存在）')
    p.add_argument('--cli-root', default=str(HERE.parent.parent))
    p.add_argument('--template-source-root', help='传给 tools/fetch-bundle.mjs --source-root，复用已有模板仓')
    v = sub.add_parser('verify-public')
    v.add_argument('--evidence-dir', required=True, help='prepare 生成的证据目录')
    v.add_argument('--cli-root', default=str(HERE.parent.parent))
    v.add_argument('--public-archive', required=True, help='已下载的公开 darwin/arm64 归档（位于证据目录内）')
    v.add_argument('--published-sha256', required=True, help='独立获得的公开归档 SHA-256（小写 64 位十六进制）')
    options = parser.parse_args()
    try:
        code = prepare(options) if options.command == 'prepare' else verify_public(options)
    except StepFailed as exc:  # 前置条件失败：尚无证据目录
        print(json.dumps({'status': 'failed', 'failedStep': 'preflight', 'problems': str(exc)}, ensure_ascii=False), file=sys.stderr)
        code = 1
    sys.exit(code)


if __name__ == '__main__':
    main()
