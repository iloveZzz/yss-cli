"""Small offline publisher fixtures; no external API calls or release writes."""
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
import zipfile

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location('publish_contract', HERE / 'publish_contract.py')
contract = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(contract)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def put(root, name, value):
    p = root / name
    p.parent.mkdir(parents=True, exist_ok=True)
    p.write_bytes(value if isinstance(value, bytes) else (json.dumps(value) + '\n').encode())
    return p


class Fixture:
    tag = 'v8.9.10'
    repo = 'iloveZzz/yss-cli'

    def __init__(self, directory):
        self.root = directory / 'repo'
        self.root.mkdir()
        self.evidence = directory / 'evidence'
        self.evidence.mkdir()
        self.git('init', '-q')
        self.git('config', 'user.name', 'Fixture')
        self.git('config', 'user.email', 'fixture@example.invalid')
        self.lock = {'schemaVersion': 5, 'producer': dict(version='bundle-producer-v4', commit='1'*40, sourceState='committed'),
                     'profiles': {p: {'profile': p, 'templateCommit': 'a' * 40,
                     'templateVersion': '7.8.9', 'policyPath': p+'/policy.json', 'policyHash': '2'*64} for p in ('spec', 'backend')}}
        put(self.root, 'docs/source-lock.json', self.lock)
        for name in contract.DOCUMENTS:
            if name != 'docs/source-lock.json':
                put(self.root, name, b'fixture document\n')
        put(self.root, 'internal/domain/domain.go', b'const Version = "8.9.10"\n')
        self.source = self.commit('source')
        self.git('tag', self.tag)
        self.notes = ('# Future release\nSource: ' + self.source + '\n').encode()
        put(self.root, 'docs/releases/' + self.tag + '.md', self.notes)
        self.notes_commit = self.commit('notes')
        put(self.root, 'operations.txt', b'publisher execution\n')
        self.operations = self.commit('operations')
        self.env = dict(os.environ, GITHUB_ACTIONS='true', GITHUB_REPOSITORY=self.repo,
                        GITHUB_EVENT_NAME='workflow_dispatch', GITHUB_SHA=self.operations,
                        PYTHONDONTWRITEBYTECODE='1')
        bundles = {p: dict(templateCommit='a'*40, templateVersion='7.8.9', sourceState='committed',
                          sourceSnapshotHash='b' * 64, manifestHash='c' * 64, bundleHash='d' * 64)
                   for p in ('spec', 'backend')}
        identity = dict(cliVersion='8.9.10', cliCommit=self.source, sourceState='committed',
                        sourceLockSha256=sha((self.root / 'docs/source-lock.json').read_bytes()),
                        protocolVersion=1, bundles=bundles)
        binary = b'fixture binary'
        put(self.evidence, 'build-darwin/yss', binary)
        receipt = dict(identity, status='passed', exitCode=0, inputDrift=False, unexecuted=[],
                       platform='darwin/arm64', runtimePlatform='darwin/arm64', runtimeVerification='native-runner',
                       binarySha256=sha(binary), binaryBeforeSha256=sha(binary), binaryAfterSha256=sha(binary))
        put(self.evidence, 'native-darwin-arm64/native-receipt.json', receipt)
        gate = dict(identity, qualificationScope=contract.SCOPE, status='passed', exitCode=0,
                    inputDrift=False, unexecuted=[], checks=[dict(id='program-installation', status='passed',
                    exitCode=0, inputDrift=False, unexecuted=[])])
        put(self.evidence, 'release-gates/release-gate.json', gate)
        inp = dict(identity, qualificationScope=contract.SCOPE, requiredPlatforms=['darwin/arm64'],
                   artifacts=[dict(platform='darwin/arm64', binary=dict(path='build-darwin/yss', sha256=sha(binary)),
                   receipt=dict(path='native-darwin-arm64/native-receipt.json', sha256=sha((self.evidence / 'native-darwin-arm64/native-receipt.json').read_bytes())))],
                   releaseGate=dict(path='release-gates/release-gate.json', sha256=sha((self.evidence / 'release-gates/release-gate.json').read_bytes())))
        inp['documents'] = {}
        for name in contract.DOCUMENTS:
            data = (self.root / name).read_bytes()
            put(self.evidence, 'input-documents/' + name, data)
            inp['documents'][name] = dict(path='input-documents/' + name, sha256=sha(data))
        put(self.evidence, 'release-input.json', inp)
        put(self.evidence, 'native-input.json', dict(inp, releaseGate=dict(path='', sha256='')))
        mac_name = 'yss_8.9.10_darwin_arm64.tar.gz'
        win_name = 'yss_8.9.10_windows_amd64.zip'
        mac = b'fixture archive'
        put(self.evidence, 'release-final/' + mac_name, mac)
        checks = dict(version='8.9.10', cliCommit=self.source, sourceState='committed', bundles=bundles,
                      sourceLockSha256=identity['sourceLockSha256'], stableReady=True, pending=[],
                      qualificationScope=contract.SCOPE, requiredPlatforms=['darwin/arm64'],
                      releaseGateSha256=inp['releaseGate']['sha256'], inputManifestSha256=sha((self.evidence / 'release-input.json').read_bytes()),
                      artifacts=[dict(platform='darwin/arm64', archive=mac_name, sha256=sha(mac),
                      nativeRuntimeVerified=True, nativeReceiptSha256=inp['artifacts'][0]['receipt']['sha256'])])
        put(self.evidence, 'release-final/checksums.json', checks)
        put(self.evidence, 'candidate/checksums.json', dict(checks, stableReady=False, pending=['program-installation']))
        buffer = io.BytesIO()
        with zipfile.ZipFile(buffer, 'w') as z:
            z.writestr('release-manifest.json', json.dumps(dict(stableReady=False, runtimeVerification='not-executed', platform='windows/amd64')))
        win = buffer.getvalue()
        put(self.evidence, 'windows-artifact/' + win_name, win)
        windows = dict(checks, stableReady=False, nativeRuntimeVerified=False, runtimeVerification='not-executed',
                       artifacts=[dict(platform='windows/amd64', archive=win_name, sha256=sha(win), compiled=True, nativeRuntimeVerified=False)])
        # Existing Windows proof uses full manifest inspections; stable proof
        # and native inputs use the six-field BundleIdentity projection.
        windows['bundles'] = {p: dict(b, profile=p, schemaVersion=4, producer=self.lock['producer'],
                              sourcePolicy=dict(kind='committed', path=self.lock['profiles'][p]['policyPath'], digest='2'*64))
                              for p, b in bundles.items()}
        put(self.evidence, 'windows-artifact/build-only-checksums.json', windows)
        steps = [dict(id=i, status='passed', exitCode=0) for i in contract.PREPARE_STEPS]
        put(self.evidence, 'orchestrator/run-log.json', dict(command='prepare', status='passed', notExecuted=[], steps=steps, preflight=dict(cliCommit=self.source, version='8.9.10')))
        for name, source in [(mac_name, 'release-final/' + mac_name), (win_name, 'windows-artifact/' + win_name),
                             ('checksums.json', 'release-final/checksums.json'), ('build-only-checksums.json', 'windows-artifact/build-only-checksums.json'),
                             ('native-receipt.json', 'native-darwin-arm64/native-receipt.json'), ('release-gate.json', 'release-gates/release-gate.json'),
                             ('release-input.json', 'release-input.json')]:
            put(self.evidence, 'upload/' + name, (self.evidence / source).read_bytes())
        self.sums()
        self.data = {
            'repository': dict(id=12, full_name=self.repo, permissions=dict(push=True)),
            'tag': dict(object=dict(type='commit', sha=self.source)),
            'run': dict(id=123, head_sha=self.source, status='completed', conclusion='success', workflow_id=20,
                        event='workflow_dispatch', path='.github/workflows/release-evidence.yml',
                        repository=dict(id=12, full_name=self.repo), head_repository=dict(id=12, full_name=self.repo)),
            'workflow': dict(id=20, path='.github/workflows/release-evidence.yml'),
            'artifacts': dict(total_count=1, artifacts=[dict(id=321, name='yss-release-evidence', expired=False,
                              digest='sha256:' + 'e' * 64, size_in_bytes=2000,
                              workflow_run=dict(id=123, head_sha=self.source, repository_id=12, head_repository_id=12))]),
            'jobs': dict(total_count=1, jobs=[dict(name='mac', status='completed', conclusion='success', steps=[
                    dict(name='Generate and validate release evidence', status='completed', conclusion='success'),
                    dict(name='Download original evidence', status='completed', conclusion='skipped'),
                    dict(name='Verify public upgrade with independent digest', status='completed', conclusion='skipped')])]),
            'releases': []}

    def git(self, *args):
        return subprocess.check_output(['git', '-C', str(self.root), *args], stderr=subprocess.DEVNULL).decode().strip()

    def commit(self, message):
        self.git('add', '.')
        self.git('commit', '-qm', message)
        return self.git('rev-parse', 'HEAD')

    def sums(self):
        upload = self.evidence / 'upload'
        put(upload, 'SHA256SUMS', ''.join(sha(p.read_bytes()) + '  ' + p.name + '\n'
            for p in sorted(upload.iterdir()) if p.name != 'SHA256SUMS').encode())

    def rebind_receipt(self):
        digest = sha((self.evidence / 'native-darwin-arm64/native-receipt.json').read_bytes())
        for name in ('native-input.json', 'release-input.json'):
            value = json.loads((self.evidence / name).read_bytes())
            value['artifacts'][0]['receipt']['sha256'] = digest
            put(self.evidence, name, value)
        checks = json.loads((self.evidence / 'release-final/checksums.json').read_bytes())
        checks['artifacts'][0]['nativeReceiptSha256'] = digest
        checks['inputManifestSha256'] = sha((self.evidence / 'release-input.json').read_bytes())
        put(self.evidence, 'release-final/checksums.json', checks)
        for name, ref in [('native-receipt.json', 'native-darwin-arm64/native-receipt.json'),
                          ('checksums.json', 'release-final/checksums.json'), ('release-input.json', 'release-input.json')]:
            put(self.evidence, 'upload/' + name, (self.evidence / ref).read_bytes())
        self.sums()

    def api(self, endpoint):
        prefix = 'repos/' + self.repo
        if endpoint == prefix:
            return copy.deepcopy(self.data['repository'])
        if '/git/ref/' in endpoint or '/git/tags/' in endpoint:
            return copy.deepcopy(self.data['tag'])
        if '/actions/workflows/' in endpoint:
            return copy.deepcopy(self.data['workflow'])
        if '/artifacts?' in endpoint:
            return copy.deepcopy(self.data['artifacts'])
        if '/jobs?' in endpoint:
            return copy.deepcopy(self.data['jobs'])
        if '/actions/runs/' in endpoint:
            return copy.deepcopy(self.data['run'])
        if '/releases?' in endpoint:
            return copy.deepcopy(self.data['releases'])
        if '/releases/latest' in endpoint:
            return copy.deepcopy(self.data['latest'])
        if '/releases/tags/' in endpoint:
            return copy.deepcopy(self.data['release'])
        raise AssertionError(endpoint)

    def check(self):
        return contract.check(self.root, self.evidence, self.tag, '123', self.notes_commit, self.env, self.api)


class PublishContractTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.f = Fixture(Path(self.temp.name))

    def test_future_version_and_exact_notes(self):
        facts = self.f.check()
        self.assertEqual(facts['sourceCommit'], self.f.source)
        self.assertEqual(facts['notesSha256'], sha(self.f.notes))
        self.assertEqual(len(facts['expectedAssets']), 8)

    def test_explicit_inputs_and_commit_relationships(self):
        for field, value in [('tag', ''), ('tag', 'v2.7.1-rc1'), ('notes_commit', ''),
                             ('notes_commit', 'HEAD'), ('notes_commit', self.f.source)]:
            old = getattr(self.f, field)
            setattr(self.f, field, value)
            with self.subTest(field=field, value=value), self.assertRaises((ValueError, subprocess.CalledProcessError)):
                self.f.check()
            setattr(self.f, field, old)
        self.f.notes_commit = self.f.operations
        self.f.git('checkout', '-q', '--orphan', 'unrelated')
        self.f.operations = self.f.commit('unrelated')
        self.f.env['GITHUB_SHA'] = self.f.operations
        with self.assertRaises((ValueError, subprocess.CalledProcessError)):
            self.f.check()

    def test_remote_identity_and_unknown_queries_fail_closed(self):
        changes = [('repository', 'full_name', 'wrong/repo'), ('repository', 'permissions', dict(push=False)),
                   ('run', 'head_sha', 'f'*40), ('run', 'status', 'in_progress'), ('run', 'conclusion', 'failure'),
                   ('run', 'event', 'push'), ('run', 'workflow_id', 999), ('workflow', 'path', '.github/workflows/other.yml'),
                   ('run', 'head_repository', dict(id=12, full_name='wrong/repo'))]
        for obj, key, value in changes:
            old = self.f.data[obj][key]
            self.f.data[obj][key] = value
            with self.subTest(obj=obj, key=key), self.assertRaises(ValueError):
                self.f.check()
            self.f.data[obj][key] = old
        for error in ('404', '403', 'timeout', 'unknown response'):
            def failed(_):
                raise ValueError(error)
            with self.subTest(error=error), self.assertRaises(ValueError):
                contract.check(self.f.root, self.f.evidence, self.f.tag, '123', self.f.notes_commit, self.f.env, failed)

    def test_artifact_phase_and_existing_release(self):
        for mutate in [lambda d: d['artifacts'].update(total_count=0, artifacts=[]),
                       lambda d: d['artifacts']['artifacts'][0].update(expired=True),
                       lambda d: d['artifacts']['artifacts'].append(copy.deepcopy(d['artifacts']['artifacts'][0])),
                       lambda d: d['artifacts']['artifacts'][0]['workflow_run'].update(id=456),
                       lambda d: d['jobs']['jobs'][0]['steps'][0].update(conclusion='skipped'),
                       lambda d: d['releases'].append(dict(tag_name=self.f.tag, draft=True))]:
            original = copy.deepcopy(self.f.data)
            mutate(self.f.data)
            with self.assertRaises(ValueError):
                self.f.check()
            self.f.data = original

    def test_local_evidence_drift(self):
        cases = [('orchestrator/run-log.json', 'command', 'verify-public'),
                 ('orchestrator/run-log.json', 'steps', []),
                 ('release-final/checksums.json', 'pending', ['missing']),
                 ('release-final/checksums.json', 'qualificationScope', 'all-platforms'),
                 ('release-final/checksums.json', 'sourceLockSha256', 'f'*64),
                 ('release-final/checksums.json', 'version', '0.0.1'),
                 ('windows-artifact/build-only-checksums.json', 'stableReady', True),
                 ('windows-artifact/build-only-checksums.json', 'nativeRuntimeVerified', True)]
        for ref, key, value in cases:
            path = self.f.evidence / ref
            original = path.read_bytes()
            data = json.loads(original)
            data[key] = value
            put(self.f.evidence, ref, data)
            with self.subTest(ref=ref, key=key), self.assertRaises(ValueError):
                self.f.check()
            path.write_bytes(original)
        put(self.f.evidence, 'upload/extra.txt', b'extra')
        with self.assertRaises(ValueError):
            self.f.check()
        (self.f.evidence / 'upload/extra.txt').unlink()
        put(self.f.evidence, 'upload/SHA256SUMS', b'bad')
        with self.assertRaises(ValueError):
            self.f.check()

    def test_template_and_digest_drift_with_self_consistent_upload(self):
        for ref, update in [('checksums.json', lambda d: d['bundles']['spec'].update(templateCommit='f'*40)),
                            ('build-only-checksums.json', lambda d: d['artifacts'][0].update(sha256='f'*64)),
                            ('release-input.json', lambda d: d.update(cliCommit='f'*40))]:
            path = self.f.evidence / 'upload' / ref
            original = path.read_bytes()
            data = json.loads(original)
            update(data)
            put(path.parent, ref, data)
            self.f.sums()
            with self.subTest(ref=ref), self.assertRaises(ValueError):
                self.f.check()
            path.write_bytes(original)
            self.f.sums()

    def test_clean_checkout_and_note_bytes(self):
        put(self.f.root, 'docs/releases/' + self.f.tag + '.md', self.f.notes.rstrip(b'\n'))
        self.f.operations = self.f.commit('notes drift')
        self.f.env['GITHUB_SHA'] = self.f.operations
        with self.assertRaises(ValueError):
            self.f.check()

    def test_platform_protocol_identity_cannot_drift_with_rebound_digests(self):
        receipt = json.loads((self.f.evidence / 'native-darwin-arm64/native-receipt.json').read_bytes())
        receipt['protocolVersion'] = 99
        put(self.f.evidence, 'native-darwin-arm64/native-receipt.json', receipt)
        self.f.rebind_receipt()
        with self.assertRaises(ValueError):
            self.f.check()

    def test_native_candidate_cannot_claim_final_gate(self):
        native = json.loads((self.f.evidence / 'native-input.json').read_bytes())
        native['releaseGate'] = dict(path='release-gates/release-gate.json', sha256='f'*64)
        put(self.f.evidence, 'native-input.json', native)
        with self.assertRaises(ValueError):
            self.f.check()

    def test_readback_all_fields_and_missing_remote_digest(self):
        facts = self.f.check()
        release = dict(id=456, draft=False, prerelease=False, tag_name=self.f.tag,
                       body=self.f.notes.decode(), assets=[dict(id=i+10, name=name, state='uploaded', **meta)
                       for i, (name, meta) in enumerate(facts['expectedAssets'].items())])
        contract.verify_release(release, release, self.f.tag, self.f.notes, facts['expectedAssets'])
        mutations = [lambda r: r.update(draft=True), lambda r: r.update(prerelease=True),
                     lambda r: r.update(tag_name='v0.0.1'), lambda r: r.update(body=r['body'].rstrip('\n')),
                     lambda r: r['assets'].pop(), lambda r: r['assets'].append(dict(r['assets'][0], name='extra')),
                     lambda r: r['assets'][0].update(digest=None), lambda r: r['assets'][0].update(digest='sha256:'+'f'*64)]
        for mutate in mutations:
            changed = copy.deepcopy(release)
            mutate(changed)
            with self.assertRaises(ValueError):
                contract.verify_release(changed, release, self.f.tag, self.f.notes, facts['expectedAssets'])
        with self.assertRaises(ValueError):
            contract.verify_release(release, dict(release, id=999), self.f.tag, self.f.notes, facts['expectedAssets'])


FAKE_GH = r'''#!/usr/bin/env python3
import json, os, pathlib, sys
state_path = pathlib.Path(os.environ['FAKE_GH_STATE'])
state = json.loads(state_path.read_text())
args = sys.argv[1:]
state['calls'].append(args)
scenario = state['scenario']
def done(value=None, code=0):
    state_path.write_text(json.dumps(state))
    if value is not None:
        print(json.dumps(value))
    sys.exit(code)
if args[:2] == ['release', 'create']:
    state['writes'] += 1
    state['createArgv'] = args
    assert '--latest' in args and '--verify-tag' in args
    assert args[2] == state['tag']
    notes = pathlib.Path(args[args.index('--notes-file') + 1]).read_text()
    assert notes == state['notes']
    assets = []
    for name, metadata in state['expected'].items():
        assert any(pathlib.Path(a).name == name and pathlib.Path(a).is_file() for a in args[3:] if not a.startswith('--'))
        assets.append(dict(id=len(assets)+10, name=name, state='uploaded', **metadata))
    release = dict(id=456, tag_name=state['tag'], body=notes, draft=False, prerelease=False, assets=assets)
    state['data']['release'] = release
    state['data']['latest'] = dict(id=456, tag_name=state['tag'])
    if scenario == 'partial-write':
        release['assets'].pop()
    if scenario in ('partial-write', 'unknown-write'):
        print('actual create response was interrupted', file=sys.stderr)
        done(code=7)
    if scenario == 'draft': release['draft'] = True
    if scenario == 'prerelease': release['prerelease'] = True
    if scenario == 'notes-after': release['body'] = notes.rstrip('\n')
    if scenario == 'missing-asset': release['assets'].pop()
    if scenario == 'extra-asset': release['assets'].append(dict(assets[0], name='extra'))
    if scenario == 'digest-after': release['assets'][0]['digest'] = None
    if scenario == 'latest-after': state['data']['latest']['id'] = 999
    print('https://example.invalid/releases/456')
    done()
assert args[:3] == ['api', '--method', 'GET'], args
endpoint = args[3]
data = state['data']
if endpoint.endswith('/zip'):
    state_path.write_text(json.dumps(state))
    sys.stdout.buffer.write(pathlib.Path(state['archive']).read_bytes())
    sys.exit(0)
if '/git/ref/' in endpoint:
    state['tagReads'] += 1
    value = data['tag']
    if scenario == 'tag-before' and state['tagReads'] >= 3:
        value = dict(object=dict(type='commit', sha='f'*40))
    if scenario == 'tag-after' and state['writes']:
        value = dict(object=dict(type='commit', sha='f'*40))
    done(value)
if '/actions/workflows/' in endpoint: done(data['workflow'])
if '/artifacts?' in endpoint:
    value = data['artifacts']
    if scenario == 'artifact-before' and state['tagReads'] >= 3:
        value['artifacts'][0]['id'] = 999
    done(value)
if '/jobs?' in endpoint: done(data['jobs'])
if '/actions/runs/' in endpoint: done(data['run'])
if '/releases?' in endpoint:
    if scenario in ('404', '403', 'network'):
        print('real query failure: ' + scenario, file=sys.stderr)
        done(code=1)
    if scenario == 'exists': done([dict(tag_name=state['tag'], draft=True)])
    if scenario in ('notes-before', 'payload-before'):
        target = pathlib.Path(state['records']) / ('notes.md' if scenario == 'notes-before' else 'evidence/upload/native-receipt.json')
        if target.exists(): target.write_bytes(target.read_bytes()+b'changed')
    done(data['releases'])
if '/releases/latest' in endpoint: done(data['latest'])
if '/releases/tags/' in endpoint:
    if scenario == 'readback-error':
        print('actual readback failure', file=sys.stderr)
        done(code=1)
    done(data['release'])
done(data['repository'])
'''


class PublisherBoundaryTest(unittest.TestCase):
    def run_scenario(self, scenario):
        with tempfile.TemporaryDirectory() as temporary:
            base = Path(temporary)
            f = Fixture(base)
            records = base / 'records'
            archive_path = base / 'artifact.zip'
            with zipfile.ZipFile(archive_path, 'w') as archive:
                for path in sorted(f.evidence.rglob('*')):
                    if path.is_file():
                        archive.write(path, path.relative_to(f.evidence).as_posix())
            f.data['artifacts']['artifacts'][0].update(digest='sha256:' + sha(archive_path.read_bytes()), size_in_bytes=archive_path.stat().st_size)
            executable = put(base, 'bin/gh', FAKE_GH.encode())
            executable.chmod(0o755)
            state_path = put(base, 'state.json', dict(data=f.data, expected=contract.expected_assets(f.evidence, f.tag),
                calls=[], writes=0, tagReads=0, scenario=scenario, archive=str(archive_path),
                tag=f.tag, notes=f.notes.decode(), records=str(records)))
            env = dict(f.env, PATH=str(executable.parent) + os.pathsep + os.environ['PATH'], FAKE_GH_STATE=str(state_path))
            result = subprocess.run(['bash', str(HERE / 'publish-release.sh'), f.tag, '123', f.notes_commit, str(records)],
                                    cwd=f.root, env=env, capture_output=True)
            state = json.loads(state_path.read_text())
            before = scenario in ('exists', '404', '403', 'network', 'tag-before', 'artifact-before', 'notes-before', 'payload-before')
            self.assertEqual(state['writes'], 0 if before else 1, result.stderr.decode())
            self.assertEqual(result.returncode == 0, scenario == 'success', result.stderr.decode())
            if not before:
                self.assertEqual(json.loads((records / 'create.result.json').read_text())['exitCode'],
                                 7 if scenario in ('partial-write', 'unknown-write') else 0)
                self.assertTrue((records / 'create.stdout.log').exists())
                self.assertTrue((records / 'create.stderr.log').exists())
                self.assertTrue(any('/releases/tags/' in a[-1] for a in state['calls'] if a[0] == 'api'),
                                'preserve obtainable remote release facts even if another readback check fails')
            if scenario in ('partial-write', 'unknown-write'):
                self.assertIn('actual create response', (records / 'create.stderr.log').read_text())
                self.assertTrue(any('/releases/tags/' in a[-1] for a in state['calls'] if a[0] == 'api'))

    def test_artifact_extraction_rejects_unsafe_paths(self):
        for name in ('../escape', '/escape', 'dir/../../escape'):
            with tempfile.TemporaryDirectory() as temporary:
                buffer = io.BytesIO()
                with zipfile.ZipFile(buffer, 'w') as archive:
                    archive.writestr(name, b'unsafe')
                with self.assertRaises(ValueError):
                    contract.extract_archive(buffer.getvalue(), Path(temporary) / 'out')


# Separate cases keep each test small and show the exact failed contract.
for _scenario in ('success', 'exists', '404', '403', 'network', 'tag-before', 'artifact-before',
                  'notes-before', 'payload-before', 'partial-write', 'unknown-write', 'draft', 'prerelease',
                  'tag-after', 'notes-after', 'missing-asset', 'extra-asset', 'digest-after', 'latest-after', 'readback-error'):
    setattr(PublisherBoundaryTest, 'test_boundary_' + _scenario.replace('-', '_'),
            lambda self, scenario=_scenario: self.run_scenario(scenario))


if __name__ == '__main__':
    unittest.main()
