"""只读核对发行产物的资格结论是否与证据一致；不修改任何文件，不放宽 tools/release 装配器已做的校验。

核对：darwin 稳定包 checksums.json 为 stableReady=true 且资格范围正确、pending 为空；候选包 stableReady=false；
Windows 仅构建包 stableReady=false 且未声称原生运行；各归档摘要、绑定的 receipt/gate/input 摘要与磁盘文件一致；
版本、提交与 source-lock 摘要同当前仓库一致。任何一项不符即以非 0 退出。
"""
import argparse, hashlib, json, subprocess, sys, zipfile
from pathlib import Path
from release_source import repo_version

SCOPE = 'local-platform-program-install'
parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--evidence-dir', required=True)
parser.add_argument('--cli-root', required=True)
options = parser.parse_args()
E, C = Path(options.evidence_dir).resolve(), Path(options.cli_root).resolve()
sha = lambda p: hashlib.sha256(Path(p).read_bytes()).hexdigest()
load = lambda ref: json.loads((E / ref).read_text())
version = repo_version(C)
commit = subprocess.check_output(['git', '-C', str(C), 'rev-parse', 'HEAD'], text=True).strip()
lock_sha = sha(C / 'docs/source-lock.json')
checks = []


def check(name, condition):
    checks.append({'check': name, 'passed': bool(condition)})


stable = load('release-final/checksums.json')
check('stable: stableReady=true', stable['stableReady'] is True)
check('stable: qualificationScope', stable['qualificationScope'] == SCOPE)
check('stable: requiredPlatforms=[darwin/arm64]', stable['requiredPlatforms'] == ['darwin/arm64'])
check('stable: pending 为空', stable['pending'] == [])
check('stable: version/commit/sourceLock 与仓库一致', stable['version'] == version and stable['cliCommit'] == commit and stable['sourceLockSha256'] == lock_sha)
check('stable: sourceState=committed', stable['sourceState'] == 'committed')
row = stable['artifacts'][0]
check('stable: 单一 darwin/arm64 且原生运行已验证', len(stable['artifacts']) == 1 and row['platform'] == 'darwin/arm64' and row['nativeRuntimeVerified'] is True)
check('stable: 归档摘要', row['sha256'] == sha(E / 'release-final' / row['archive']))
check('stable: 绑定 native-receipt 摘要', row['nativeReceiptSha256'] == sha(E / 'native-darwin-arm64/native-receipt.json'))
check('stable: 绑定 release-gate 摘要', stable['releaseGateSha256'] == sha(E / 'release-gates/release-gate.json'))
check('stable: 绑定 release-input 摘要', stable['inputManifestSha256'] == sha(E / 'release-input.json'))

candidate = load('candidate/checksums.json')
check('candidate: stableReady=false 且 pending=[program-installation]', candidate['stableReady'] is False and candidate['pending'] == ['program-installation'])
check('candidate: 资格范围', candidate['qualificationScope'] == SCOPE)

win = load('windows-artifact/build-only-checksums.json')
wrow = win['artifacts'][0]
check('windows: stableReady=false', win['stableReady'] is False)
check('windows: 未声称原生运行', win['nativeRuntimeVerified'] is False and win['runtimeVerification'] == 'not-executed' and wrow['nativeRuntimeVerified'] is False)
check('windows: 仅 windows/amd64 且已编译', len(win['artifacts']) == 1 and wrow['platform'] == 'windows/amd64' and wrow['compiled'] is True)
check('windows: version/commit/sourceLock 与仓库一致', win['version'] == version and win['cliCommit'] == commit and win['sourceLockSha256'] == lock_sha)
check('windows: 归档摘要', wrow['sha256'] == sha(E / 'windows-artifact' / wrow['archive']))
with zipfile.ZipFile(E / 'windows-artifact' / wrow['archive']) as z:
    manifest = json.loads(z.read('release-manifest.json'))
check('windows: ZIP 内清单 stableReady=false', manifest['stableReady'] is False and manifest['runtimeVerification'] == 'not-executed' and manifest['platform'] == 'windows/amd64')

failed = [c['check'] for c in checks if not c['passed']]
print(json.dumps({'status': 'failed' if failed else 'passed', 'failed': failed, 'checks': checks}, ensure_ascii=False, indent=2))
sys.exit(1 if failed else 0)
