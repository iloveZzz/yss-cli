import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import release_source


class SourceSnapshotTests(unittest.TestCase):
    def test_external_build_snapshot_is_used_for_all_release_document_reads(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve() / 'checkout'
            root.mkdir()
            snapshot = Path(directory).resolve() / 'source-snapshot.json'
            lock = {'schemaVersion': 5, 'profiles': {
                p: {'profile': p, 'templateCommit': 'a' * 40, 'templateVersion': '5.0.0'}
                for p in ('spec', 'backend')}}
            snapshot.write_text(json.dumps(lock))
            with patch.dict(os.environ, {'YSS_SOURCE_SNAPSHOT': str(snapshot)}):
                self.assertEqual(release_source.source_document(root, 'docs/source-lock.json'), snapshot)
                self.assertEqual(release_source.repo_profiles(root), ('spec', 'backend'))
                self.assertEqual(release_source.template_identity(root), ('a' * 40, '5.0.0'))
                self.assertEqual(release_source.source_document(root, 'README.md'), root / 'README.md')

    def test_external_build_snapshot_must_be_absolute_and_not_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory).resolve() / 'checkout'
            root.mkdir()
            snapshot = Path(directory).resolve() / 'snapshot.json'
            snapshot.write_text('{}')
            link = Path(directory).resolve() / 'link.json'
            link.symlink_to(snapshot)
            for value in ('relative.json', str(link), str(root / 'missing.json')):
                with self.subTest(value=value), patch.dict(os.environ, {'YSS_SOURCE_SNAPSHOT': value}):
                    with self.assertRaises(ValueError):
                        release_source.source_document(root, 'docs/source-lock.json')

    def test_prepare_consumes_external_snapshot_when_repository_has_no_lock(self):
        import hashlib
        import subprocess
        import sys

        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory).resolve()
            root, evidence = base / 'cli', base / 'evidence'
            root.mkdir()
            evidence.mkdir()
            def git(*args):
                return subprocess.check_output(['git', '-C', str(root), *args], text=True).strip()
            git('init', '-q')
            git('config', 'user.email', 'test@example.invalid')
            git('config', 'user.name', 'Test')
            docs = ['docs/compatibility.md', 'docs/porting-status.md', 'docs/native-governance.md',
                    'docs/cli-retirement.md', 'compat/README.md', 'README.md']
            for ref in docs + ['internal/domain/domain.go']:
                target = root / ref
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_text('const Version = "3.0.0"\n' if ref.endswith('domain.go') else 'test\n')
            git('add', '.')
            git('commit', '-qm', 'fixture')
            commit = git('rev-parse', 'HEAD')
            snapshot = base / 'snapshot.json'
            snapshot.write_text(json.dumps({'schemaVersion': 5, 'profiles': {
                p: {'profile': p, 'templateCommit': 'a'*40, 'templateVersion': '5.0.0'}
                for p in ('spec', 'backend')}}))
            sha = lambda data: hashlib.sha256(data).hexdigest()
            binary = b'candidate input only'
            (evidence / 'binary').write_bytes(binary)
            receipt = dict(status='passed', exitCode=0, inputDrift=False, unexecuted=[],
                platform='darwin/arm64', runtimePlatform='darwin/arm64', runtimeVerification='native-runner',
                cliVersion='3.0.0', protocolVersion=1, cliCommit=commit, sourceState='committed',
                sourceLockSha256=sha(snapshot.read_bytes()), binarySha256=sha(binary),
                binaryBeforeSha256=sha(binary), binaryAfterSha256=sha(binary), bundles={
                    p: {'templateCommit': 'a'*40, 'templateVersion': '5.0.0', 'sourceState': 'committed'}
                    for p in ('spec', 'backend')})
            (evidence / 'receipt.json').write_text(json.dumps(receipt))
            env = {**os.environ, 'YSS_SOURCE_SNAPSHOT': str(snapshot)}
            subprocess.run([sys.executable, str(Path(__file__).with_name('prepare-native-input.py')),
                '--cli-root', str(root), '--evidence-root', str(evidence), '--artifact',
                'darwin/arm64', 'binary', 'receipt.json'], env=env, check=True, capture_output=True)
            self.assertFalse((root / 'docs/source-lock.json').exists())
            self.assertEqual((evidence / 'input-documents/docs/source-lock.json').read_bytes(), snapshot.read_bytes())
