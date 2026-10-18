"""Build only windows/amd64; never issue a native receipt or stable checksums."""
import argparse
import gzip
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import struct
import subprocess
import time
import zipfile

from release_source import repo_version, template_identity, repo_profiles

DOCS = ['docs/source-lock.json', 'docs/compatibility.md', 'docs/porting-status.md',
        'docs/native-governance.md', 'docs/cli-retirement.md', 'compat/README.md', 'README.md']
sha = lambda data: hashlib.sha256(data).hexdigest()


def require(condition, message):
    if not condition:
        raise ValueError(message)


def ordinary(path):
    require(path.is_file() and not path.is_symlink() and path.absolute() == path.resolve(),
            'ordinary canonical file required: ' + str(path))
    return path.read_bytes()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for name in ['cli-root', 'source-manifest', 'source-manifest-sha256', 'out']:
        parser.add_argument('--' + name, required=True)
    for name in ['cli-commit', 'template-commit', 'template-version']:
        parser.add_argument('--' + name, help='缺省取自仓库 HEAD / docs/source-lock.json；显式传入时必须一致')
    args = parser.parse_args()
    C = Path(args.cli_root).absolute()
    require(C == C.resolve() and C.is_dir(), 'canonical CLI checkout required')
    git = lambda *argv: subprocess.check_output(['git', *argv], cwd=C, text=True).strip()
    require(Path(git('rev-parse', '--show-toplevel')).resolve() == C, 'CLI root must be Git root')
    version = repo_version(C)
    lock_commit, lock_version = template_identity(C)
    args.cli_commit = args.cli_commit or git('rev-parse', 'HEAD')
    args.template_commit = args.template_commit or lock_commit
    args.template_version = args.template_version or lock_version
    require(args.template_commit == lock_commit and args.template_version == lock_version,
            'template identity differs from docs/source-lock.json')
    require(re.fullmatch('[a-f0-9]{40}', args.cli_commit) and
            re.fullmatch('[a-f0-9]{40}', args.template_commit), 'full expected C/T required')
    require(args.template_version == 'git:' + args.template_commit or
            re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', args.template_version),
            'expected template version must be stable semver or exact git:T')

    def clean():
        require(git('rev-parse', 'HEAD') == args.cli_commit and
                not git('status', '--porcelain=v1', '--untracked-files=all'), 'fixed clean C required')

    clean()
    source_path = Path(args.source_manifest).absolute()
    source_bytes = ordinary(source_path)
    require(re.fullmatch('[a-f0-9]{64}', args.source_manifest_sha256) and
            sha(source_bytes) == args.source_manifest_sha256, 'independent source manifest SHA mismatch')
    source = json.loads(source_bytes)
    require(source['schemaVersion'] == 1 and source['cliVersion'] == version and
            source['protocolVersion'] == 1 and source['cliCommit'] == args.cli_commit and
            source['sourceState'] == 'committed' and source['platform'] == 'darwin/arm64',
            'same-C Mac source manifest required; its runtime status grants no Windows qualification')
    files = {ref: ordinary(C / ref) for ref in DOCS}
    for ref, content in files.items():
        require(source['files'][ref] == {'type': 'file', 'digest': sha(content), 'mode': 0o644},
                'source manifest document differs from C: ' + ref)
    lock = json.loads(files['docs/source-lock.json'])
    require(lock['schemaVersion'] in [2, 3, 5] and set(lock['profiles']) == set(repo_profiles(C)) and
            set(source['bundles']) == set(repo_profiles(C)), 'exact active Profile sources required')
    require(source['sourceLockSha256'] == sha(files['docs/source-lock.json']), 'source-lock SHA mismatch')
    embedded_path = C / 'internal/bundle/assets/bundles.json.gz'
    embedded = ordinary(embedded_path)
    require(len(embedded) <= 10_000_000, 'embedded archive size limit exceeded')
    packed = json.loads(gzip.decompress(embedded))
    require(set(packed['profiles']) == set(repo_profiles(C)), 'embedded Profile set mismatch')
    for profile in sorted(set(repo_profiles(C))):
        row, inspection, bundled = lock['profiles'][profile], source['bundles'][profile], packed['profiles'][profile]
        require(row['profile'] == profile and row['templateCommit'] == args.template_commit and
                row.get('templateVersion') == args.template_version, 'expected T/version differs from lock')
        if re.fullmatch(r'[0-9]+\.[0-9]+\.[0-9]+', args.template_version):
            require(row.get('templateTag') == 'v' + args.template_version, 'stable source requires exact version tag')
        else:
            require(not row.get('templateTag'), 'git source must not carry a different tag')
        require(inspection['profile'] == profile and inspection['schemaVersion'] in ([4] if lock['schemaVersion'] == 5 else [2, 3]) and
                inspection['templateCommit'] == args.template_commit and
                inspection['templateVersion'] == args.template_version and
                inspection['sourceState'] == 'committed', 'Mac inspection source differs from expected T')
        for key in ['schemaVersion', 'profile', 'templateVersion', 'templateCommit', 'sourceState',
                    'sourceSnapshotHash', 'manifestHash', 'bundleHash', 'producer', 'sourcePolicy']:
            require(inspection[key] == bundled[key], 'Mac inspection differs from committed Bundle: ' + key)
        require(inspection['producer'] == lock['producer'] and
                inspection['sourcePolicy']['path'] == row['policyPath'] and
                inspection['sourcePolicy']['digest'] == row['policyHash'], 'Bundle source policy differs from lock')
    out = Path(args.out).absolute()
    require(out.parent.is_dir() and out.parent == out.parent.resolve() and not out.exists() and
            not out.is_symlink(), 'new output under an existing canonical parent required')
    require(not out.is_relative_to(C) and not out.is_relative_to(Path(__file__).resolve().parent),
            'output must be outside source and tools checkouts')
    out.mkdir()
    commands = []

    def run(label, argv, env):
        started = time.monotonic()
        result = subprocess.run(argv, cwd=C, env=env, capture_output=True)
        for stream in ['stdout', 'stderr']:
            (out / (label + '.' + stream)).write_bytes(getattr(result, stream))
        commands.append({'argv': argv, 'cwd': str(C), 'exit_code': result.returncode,
                         'duration_seconds': time.monotonic() - started,
                         'environment': {k: env[k] for k in ['GOOS', 'GOARCH', 'CGO_ENABLED', 'GOTOOLCHAIN'] if k in env},
                         **{s: {'path': label + '.' + s, 'sha256': sha(getattr(result, s))} for s in ['stdout', 'stderr']}})
        (out / 'commands.json').write_text(json.dumps(commands, indent=2) + '\n')
        require(result.returncode == 0, label + ' failed; see retained command logs')
        return result.stdout

    env = {**os.environ, 'GOOS': 'windows', 'GOARCH': 'amd64', 'CGO_ENABLED': '0', 'GOTOOLCHAIN': 'local'}
    run('build', ['node', str(C / 'tools/build-candidate.mjs'), '--root', str(C), '--out', str(out / 'build')], env)
    receipt_path = out / 'build/build-receipt.json'
    receipt = json.loads(ordinary(receipt_path))
    require(receipt['status'] == 'built' and receipt['commit'] == args.cli_commit and
            receipt['sourceState'] == 'committed' and receipt['platform'] == 'windows/amd64' and
            receipt['cgoEnabled'] == '0' and receipt['inputDrift'] is False and
            receipt['nativeRuntimeVerified'] is False, 'Windows build receipt mismatch')
    required_go = re.search(r'^go ([0-9.]+)$', ordinary(C / 'go.mod').decode(), re.M).group(1)
    require(receipt['goEnvironment']['GOVERSION'] == 'go' + required_go, 'Go must exactly match fixed source')
    require(receipt['builderSha256'] == sha(ordinary(C / 'tools/build-candidate.mjs')) and
            receipt['environmentHelperSha256'] == sha(ordinary(C / 'tools/go-environment.mjs')), 'build tool drift')
    binary_path = out / 'build/yss.exe'
    binary = ordinary(binary_path)
    require(receipt['binary'] == {'path': str(binary_path), 'bytes': len(binary), 'sha256': sha(binary)} and
            len(binary) <= 35_000_000, 'built binary digest/size mismatch')
    pe = struct.unpack_from('<I', binary, 0x3c)[0]
    require(binary[:2] == b'MZ' and binary[pe:pe+4] == b'PE\x00\x00' and
            struct.unpack_from('<H', binary, pe+4)[0] == 0x8664 and
            struct.unpack_from('<H', binary, pe+24)[0] == 0x20b, 'Windows amd64 PE32+ required')
    info = run('build-info', ['go', 'version', '-m', str(binary_path)], env).decode()
    require(info.splitlines()[0].endswith('go' + required_go) and
            all('\tbuild\t' + setting in info for setting in ['GOOS=windows', 'GOARCH=amd64', 'CGO_ENABLED=0']),
            'binary build info differs from Windows build receipt')
    files['yss.exe'] = binary
    descriptors = {ref: {'type': 'file', 'digest': sha(data), 'mode': 0o755 if ref == 'yss.exe' else 0o644}
                   for ref, data in files.items()}
    manifest = {'schemaVersion': 1, 'cliVersion': version, 'protocolVersion': 1, 'platform': 'windows/amd64',
                'cgo': False, 'sourceState': 'committed', 'cliCommit': args.cli_commit, 'bundles': source['bundles'],
                'sourceLockSha256': sha(files['docs/source-lock.json']), 'binarySha256': sha(binary),
                'stableReady': False, 'nativeRuntimeVerified': False, 'runtimeVerification': 'not-executed',
                'files': descriptors}
    files['release-manifest.json'] = (json.dumps(manifest, ensure_ascii=False, indent=2) + '\n').encode()
    archive = out / ('yss_' + version + '_windows_amd64.zip')
    with zipfile.ZipFile(archive, 'x', compression=zipfile.ZIP_DEFLATED) as z:
        for ref in sorted(files):
            entry = zipfile.ZipInfo(ref, date_time=(1980, 1, 1, 0, 0, 0))
            entry.create_system = 3
            entry.compress_type = zipfile.ZIP_DEFLATED
            entry.external_attr = (stat.S_IFREG | (0o755 if ref == 'yss.exe' else 0o644)) << 16
            z.writestr(entry, files[ref])
    with zipfile.ZipFile(archive) as z:
        require(z.namelist() == sorted(files), 'archive entry set mismatch')
        for ref, data in files.items():
            require(z.read(ref) == data and stat.S_IMODE(z.getinfo(ref).external_attr >> 16) ==
                    (0o755 if ref == 'yss.exe' else 0o644), 'archive bytes or mode mismatch')
    clean()
    require(ordinary(source_path) == source_bytes and ordinary(embedded_path) == embedded, 'source input drift')
    for ref, facts in receipt['inputs'].items():
        p = C / ref
        require(facts and sha(ordinary(p)) == facts['sha256'] and bool(p.stat().st_mode & 0o111) == facts['executable'],
                'build input drift: ' + ref)
    proof = {'schemaVersion': 2, 'version': version, 'cliCommit': args.cli_commit, 'sourceState': 'committed',
             'bundles': source['bundles'], 'sourceLockSha256': manifest['sourceLockSha256'], 'stableReady': False,
             'nativeRuntimeVerified': False, 'runtimeVerification': 'not-executed', 'inputDrift': False,
             'pending': ['windows-native-runtime-not-requested', 'windows-online-upgrade-not-qualified'],
             'sourceManifest': {'path': str(source_path), 'sha256': sha(source_bytes)},
             'buildReceipt': {'path': 'build/build-receipt.json', 'sha256': sha(ordinary(receipt_path))},
             'embeddedArchive': {'path': 'internal/bundle/assets/bundles.json.gz', 'sha256': sha(embedded), 'bytes': len(embedded)},
             'artifacts': [{'platform': 'windows/amd64', 'archive': archive.name, 'sha256': sha(archive.read_bytes()),
                            'bytes': archive.stat().st_size, 'binarySha256': sha(binary), 'compiled': True,
                            'nativeRuntimeVerified': False}]}
    (out / 'build-only-checksums.json').write_text(json.dumps(proof, ensure_ascii=False, indent=2) + '\n')
    print(json.dumps({'status': 'build-only-packaged', 'archive': str(archive), 'sha256': proof['artifacts'][0]['sha256'],
                      'stableReady': False, 'nativeRuntimeVerified': False, 'runtimeVerification': 'not-executed'}))


if __name__ == '__main__':
    main()
