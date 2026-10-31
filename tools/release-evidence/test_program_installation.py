"""Offline runner regressions. Temporary fixture output is not release evidence."""
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest


RUNNER = Path(__file__).with_name('run-program-installation.py').resolve()
FAILURE_STDERR = b'fixture HTTP 403 stderr\n'
FAKE_CLI = r'''
import json,os,sys,tarfile
from pathlib import Path
config=json.loads(Path(os.environ['PROGRAM_INSTALLATION_FIXTURE']).read_text())
args=sys.argv[1:]
def option(name):return args[args.index(name)+1]
def emit(result):print(json.dumps({'status':'ok','code':'OK','result':result}))
def install(root):
 with tarfile.open(config['archive'],'r:gz') as archive:
  for member in archive.getmembers():
   path=root/member.name;path.parent.mkdir(parents=True,exist_ok=True)
   path.write_bytes(archive.extractfile(member).read());path.chmod(member.mode)
if args[0]=='version':emit(config['version'])
elif args[:2]==['bundle','inspect']:emit(config['bundles'][option('--profile')])
elif args[0]=='upgrade':
 if config['fail_upgrade']:
  value={'status':'error','code':'NETWORK','result':{},'message':'GitHub returned HTTP 403'}
  if '--diagnostics' in args:value['diagnostic']={'id':'RELEASE_HTTP_STATUS','context':{'source':'https://api.github.com/repos/iloveZzz/yss-cli/releases/tags/v9.8.7','phase':'request','httpStatus':403}}
  print(json.dumps(value));sys.stderr.write('fixture HTTP 403 stderr\n');sys.exit(1)
 root=Path(option('--tool-root'));install(root)
 (root/'installation.json').write_text(json.dumps(config['receipt']))
 result=dict(config['upgrade_result']);result['toolRoot']=str(root);emit(result)
elif args[0]=='update':
 root=Path(option('--tool-root'));command=args[1]
 if command=='plan':
  result={'transactionId':'fixture-transaction','artifact':option('--artifact')}
  Path(option('--out')).write_text(json.dumps(result));emit(result)
 elif command=='apply':install(root);emit({'transactionId':'fixture-transaction'})
 elif command=='status':
  installed=(root/'yss').exists()
  result={'installed':installed,'reinstallRequired':False}
  if installed:result.update(installation=config['receipt'],installationConsistent=True,projectMigrationPerformed=False,diagnostic={'consistent':True})
  emit(result)
 else:raise AssertionError(args)
else:raise AssertionError(args)
'''


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


class ProgramInstallationTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.repo = self.root / 'repo'
        (self.repo / 'internal/domain').mkdir(parents=True)
        (self.repo / 'docs').mkdir()
        (self.repo / 'internal/domain/domain.go').write_text('const Version = "9.8.7"\n')
        profiles = {p: {'profile': p} for p in ('spec', 'backend')}
        (self.repo / 'docs/source-lock.json').write_text(json.dumps({'schemaVersion': 5, 'profiles': profiles}))
        document = b'fixture document\n'
        (self.repo / 'docs/fixture.txt').write_bytes(document)
        for args in (['init', '--quiet'], ['add', '.'], ['-c', 'user.name=Fixture', '-c', 'user.email=fixture@example.invalid', '-c', 'commit.gpgsign=false', 'commit', '--quiet', '-m', 'fixture']):
            subprocess.run(['git', '-C', str(self.repo)] + args, check=True, capture_output=True)
        commit = subprocess.check_output(['git', '-C', str(self.repo), 'rev-parse', 'HEAD'], text=True).strip()
        self.evidence = self.root / 'temporary-fixture-output'
        self.evidence.mkdir()
        self.binary = self.evidence / 'yss'
        self.binary.write_text('#!' + sys.executable + '\n' + FAKE_CLI)
        self.binary.chmod(0o755)
        document_path = self.evidence / 'input-documents/docs/fixture.txt'
        document_path.parent.mkdir(parents=True)
        document_path.write_bytes(document)
        payload = {'yss': (self.binary.read_bytes(), 0o755), 'docs/fixture.txt': (document, 0o644)}
        manifest = {
            'platform': 'darwin/arm64', 'cliCommit': commit, 'cliVersion': '9.8.7', 'protocolVersion': 1,
            'sourceState': 'committed', 'stableReady': True, 'runtimeVerification': 'passed',
            'sourceLockSha256': sha(self.repo / 'docs/source-lock.json'), 'bundles': profiles,
            'files': {name: {'type': 'file', 'digest': hashlib.sha256(body).hexdigest(), 'mode': mode} for name, (body, mode) in payload.items()},
        }
        self.archive = self.evidence / 'fixture.tar.gz'
        with tarfile.open(self.archive, 'w:gz') as archive:
            for name, (body, mode) in dict(payload, **{'release-manifest.json': (json.dumps(manifest).encode(), 0o644)}).items():
                info = tarfile.TarInfo(name)
                info.size, info.mode = len(body), mode
                archive.addfile(info, io.BytesIO(body))
        self.input = self.evidence / 'native-input.json'
        self.input.write_text(json.dumps({
            'cliCommit': commit, 'cliVersion': '9.8.7', 'sourceState': 'committed', 'bundles': profiles,
            'sourceLockSha256': manifest['sourceLockSha256'],
            'documents': {'docs/fixture.txt': {'sha256': sha(document_path), 'path': 'input-documents/docs/fixture.txt'}},
            'artifacts': [{'platform': 'darwin/arm64', 'binary': {'path': 'yss', 'sha256': sha(self.binary)}}],
        }))
        self.config_path = self.root / 'fake-cli.json'
        self.config = {
            'archive': str(self.archive), 'fail_upgrade': False, 'bundles': profiles,
            'version': {'version': '9.8.7', 'cliCommit': commit, 'sourceState': 'committed'},
            'receipt': {'schemaVersion': 1, 'cliVersion': '9.8.7', 'protocolVersion': 1, 'platform': 'darwin/arm64', 'archiveDigest': sha(self.archive), 'sourceState': 'committed', 'stableReady': True},
            'upgrade_result': {'status': 'installed', 'currentVersion': '', 'targetVersion': '9.8.7', 'updateAvailable': False, 'platform': 'darwin/arm64', 'releaseUrl': 'https://github.com/iloveZzz/yss-cli/releases/tag/v9.8.7', 'archiveSha256': sha(self.archive), 'transaction': {'transactionId': 'fixture-transaction', 'kind': 'program-update'}},
        }
        self.out = self.evidence / 'run'

    def run_fixture(self):
        self.config_path.write_text(json.dumps(self.config))
        args = ['--input', str(self.input), '--archive', str(self.archive), '--out', str(self.out), '--platform', 'darwin/arm64', '--cli-root', str(self.repo), '--published-sha256', sha(self.archive)]
        # Only the native-host check is simulated. The runner and every fake CLI
        # command execute in subprocesses and write their own stdout/stderr.
        wrapper = "import platform,runpy,sys; platform.machine=lambda:'arm64'; sys.platform='darwin'; sys.path.insert(0,sys.argv[1]); sys.argv=sys.argv[2:]; runpy.run_path(sys.argv[0],run_name='__main__')"
        return subprocess.run([sys.executable, '-c', wrapper, str(RUNNER.parent), str(RUNNER)] + args, env=dict(os.environ, PROGRAM_INSTALLATION_FIXTURE=str(self.config_path)), capture_output=True, timeout=15)

    def report(self):
        return json.loads((self.out / 'report.json').read_bytes())

    def test_http_403_keeps_real_logs_and_stops_online_steps(self):
        self.config['fail_upgrade'] = True
        completed = self.run_fixture()
        self.assertEqual(completed.returncode, 1, completed.stderr)
        report = self.report()
        self.assertEqual(report['status'], 'failed')
        self.assertFalse(report['input_drift'])
        self.assertEqual(report['unexecuted'], [])  # Local cases actually ran.
        self.assertEqual(report['public_upgrade']['status'], 'failed')
        self.assertEqual([check['check'] for check in report['public_upgrade']['checks']], ['public-executor'])
        self.assertEqual(len(report['online_commands']), 1)
        row = report['online_commands'][0]
        self.assertEqual(row['args'][1:], ['upgrade', '--to', '9.8.7', '--tool-root', str(self.out / 'online-tool'), '--json', '--diagnostics'])
        self.assertEqual(row['actual_exit_code'], 1)
        self.assertTrue(row['actual_exit_code_observed'])
        self.assertFalse(row['timed_out'])
        self.assertIsNone(row['actual_signal'])
        self.assertGreaterEqual(row['duration_ms'], 0)
        stdout, stderr = self.evidence / row['stdout'], self.evidence / row['stderr']
        self.assertEqual(stderr.read_bytes(), FAILURE_STDERR)
        self.assertEqual(row['stdout_sha256'], sha(stdout))
        self.assertEqual(row['stderr_sha256'], sha(stderr))
        envelope = json.loads(stdout.read_bytes())
        self.assertEqual((envelope['status'], envelope['code']), ('error', 'NETWORK'))
        self.assertEqual(envelope['diagnostic']['context']['httpStatus'], 403)
        self.assertFalse((self.out / 'online-installed-inventory.json').exists())
        self.assertFalse((self.out / 'online-restored-inventory.json').exists())

    def test_failed_run_directory_cannot_be_reused(self):
        self.config['fail_upgrade'] = True
        self.assertEqual(self.run_fixture().returncode, 1)
        before = {path.relative_to(self.out): path.read_bytes() for path in self.out.rglob('*') if path.is_file()}
        repeated = self.run_fixture()
        self.assertEqual(repeated.returncode, 1)
        after = {path.relative_to(self.out): path.read_bytes() for path in self.out.rglob('*') if path.is_file()}
        self.assertEqual(before, after)
        self.assertEqual(len(self.report()['online_commands']), 1)

    def test_success_keeps_result_and_all_existing_checks(self):
        completed = self.run_fixture()
        self.assertEqual(completed.returncode, 0, completed.stderr)
        report = self.report()
        self.assertEqual(report['status'], 'passed')
        self.assertEqual(report['required_cases'], ['install-plan-read-only', 'install-apply', 'reinstall', 'user-file-preservation'])
        commands = report['commands']
        self.assertEqual([row['args'][2] for row in commands], ['plan', 'apply', 'plan', 'apply'])
        roots = [row['args'][row['args'].index('--tool-root') + 1] for row in commands]
        self.assertEqual(roots[0], roots[1])
        self.assertEqual(roots[2], roots[3])
        self.assertNotEqual(roots[0], roots[2])
        self.assertFalse(report['input_drift'])
        self.assertEqual(report['unexecuted'], [])
        online = report['public_upgrade']
        self.assertEqual(online['status'], 'passed')
        self.assertEqual([check['check'] for check in online['checks']], online['required_checks'])
        row = report['online_commands'][0]
        envelope = json.loads((self.evidence / row['stdout']).read_bytes())
        expected = dict(self.config['upgrade_result'], toolRoot=str(self.out / 'online-tool'))
        self.assertEqual(envelope, {'status': 'ok', 'code': 'OK', 'result': expected})
        self.assertTrue(all(row['actual_exit_code'] == 0 and not row['timed_out'] for row in report['online_commands']))


if __name__ == '__main__':
    unittest.main()
