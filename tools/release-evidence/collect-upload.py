"""把已验证的发行产物整理进新的 upload 目录并生成 SHA256SUMS；不上传、不发布。

产物来源与 2.3.x 的发布内容一致：darwin 稳定包、Windows 仅构建 ZIP、checksums.json、build-only-checksums.json、
native-receipt.json、release-gate.json、release-input.json。每个文件复制后逐字节复核 SHA-256。
"""
import argparse, hashlib, json, shutil
from pathlib import Path
from release_source import repo_version

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--evidence-dir', required=True)
parser.add_argument('--out', default=None, help='缺省为 <evidence-dir>/upload，必须不存在')
parser.add_argument('--cli-root', required=True)
options = parser.parse_args()
E = Path(options.evidence_dir).resolve()
out = Path(options.out).absolute() if options.out else E / 'upload'
assert not out.exists() and not out.is_symlink(), 'upload 目录必须是新目录'
version = repo_version(options.cli_root)
sources = [  # (发布文件名, 来源相对证据目录)；顺序即 SHA256SUMS 顺序
    (f'yss_{version}_darwin_arm64.tar.gz', f'release-final/yss_{version}_darwin_arm64.tar.gz'),
    (f'yss_{version}_windows_amd64.zip', f'windows-artifact/yss_{version}_windows_amd64.zip'),
    ('checksums.json', 'release-final/checksums.json'),
    ('build-only-checksums.json', 'windows-artifact/build-only-checksums.json'),
    ('native-receipt.json', 'native-darwin-arm64/native-receipt.json'),
    ('release-gate.json', 'release-gates/release-gate.json'),
    ('release-input.json', 'release-input.json'),
]
sha = lambda p: hashlib.sha256(p.read_bytes()).hexdigest()
for _, ref in sources:
    p = E / ref
    assert p.is_file() and not p.is_symlink(), '缺少产物（对应步骤未成功执行）: ' + ref
out.mkdir()
lines = []
for name, ref in sources:
    shutil.copyfile(E / ref, out / name)
    assert sha(out / name) == sha(E / ref), '复制后摘要不一致: ' + name
    lines.append(f'{sha(out / name)}  {name}\n')
(out / 'SHA256SUMS').write_text(''.join(lines))
print(json.dumps({'status': 'collected', 'upload': str(out), 'files': [n for n, _ in sources] + ['SHA256SUMS']}))
