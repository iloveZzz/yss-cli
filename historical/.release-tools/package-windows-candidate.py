"""Package the standard Windows build without granting release qualification."""
import json
import subprocess
import sys
from pathlib import Path
from release_qualification import encode, pack, payload, sha

build, out = map(lambda arg: Path(arg).resolve(), sys.argv[1:])
receipt = json.loads((build / 'build-receipt.json').read_bytes())
commit = subprocess.check_output(['git', 'rev-parse', 'HEAD'], text=True).strip()
assert commit == 'c398eb50336283aacad6342e26176777a4e16c46'
assert subprocess.check_output(['git', 'status', '--porcelain']) == b''
assert receipt['commit'] == commit and receipt['sourceState'] == 'committed'
assert receipt['status'] == 'built' and not receipt['inputDrift']
assert receipt['platform'] == 'windows/amd64' and receipt['cgoEnabled'] == '0'
for ref, descriptor in receipt['inputs'].items():
    assert sha(Path(ref).read_bytes()) == descriptor['sha256'], ref
binary = build / 'yss.exe'
content = binary.read_bytes()
assert sha(content) == receipt['binary']['sha256'] and len(content) <= 35_000_000

def call(*args):
    result = subprocess.run([str(binary), *args, '--json'], capture_output=True, check=True)
    envelope = json.loads(result.stdout)
    assert envelope['status'] == 'ok' and envelope['code'] == 'OK'
    return envelope['result']

version = call('version')
assert version['version'] == '1.3.7' and version['cliCommit'] == commit and version['sourceState'] == 'committed'
bundles = {profile: call('bundle', 'inspect', '--profile', profile) for profile in ['spec', 'design', 'backend', 'frontend']}
files = {'yss.exe': content}
for ref in ['README.md', 'docs/source-lock.json', 'docs/compatibility.md', 'docs/porting-status.md', 'docs/native-governance.md', 'docs/cli-retirement.md', 'compat/README.md']:
    files[ref] = Path(ref).read_bytes()
manifest = {'schemaVersion': 1, 'cliVersion': '1.3.7', 'protocolVersion': 1, 'platform': 'windows/amd64', 'cgo': False, 'sourceState': 'committed', 'cliCommit': commit, 'bundles': bundles, 'binarySha256': sha(content), 'stableReady': False, 'runtimeVerification': 'pending', 'files': {ref: {'type': 'file', 'digest': sha(data), 'mode': 0o755 if ref == 'yss.exe' else 0o644} for ref, data in files.items()}}
files['release-manifest.json'] = encode(manifest)
out.mkdir()
archive = out / 'yss_1.3.7_windows_amd64.zip'
pack(archive, files, 'yss.exe')
observed_files, observed_manifest = payload(archive)
assert observed_files == files and observed_manifest == manifest
assert subprocess.check_output(['git', 'status', '--porcelain']) == b''
(out / 'checksums-trial.json').write_bytes(encode({'schemaVersion': 2, 'version': '1.3.7', 'cliCommit': commit, 'sourceState': 'committed', 'stableReady': False, 'pending': ['windows-native-installation'], 'artifacts': [{'platform': 'windows/amd64', 'archive': archive.name, 'sha256': sha(archive.read_bytes()), 'bytes': archive.stat().st_size, 'compiled': True, 'nativeRuntimeVerified': False}]}))
print(sha(archive.read_bytes()), archive)
