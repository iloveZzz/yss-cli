"""从 darwin/arm64 稳定包中原样取出 release-manifest.json，作为 Windows 仅构建打包的“同提交 Mac 来源清单”。

2.3.x 实际使用的是稳定包内的清单（含 stableReady/releaseGate 等绑定字段）。只读取归档，不改动任何输入；输出文件必须是新文件。stdout 输出取出文件的 SHA-256（供 --source-manifest-sha256 独立核对）。
"""
import argparse, hashlib, json, tarfile
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--archive', required=True, help='稳定包 tar.gz（darwin/arm64，release-final 下）')
parser.add_argument('--out', required=True, help='新的 mac-release-manifest.json 路径')
options = parser.parse_args()
archive, out = Path(options.archive), Path(options.out)
assert archive.is_file() and not archive.is_symlink(), '候选归档必须是普通文件'
assert out.parent.is_dir() and not out.exists() and not out.is_symlink(), '输出必须是已有目录下的新文件'
with tarfile.open(archive, 'r:gz') as t:
    members = [m for m in t.getmembers() if m.name == 'release-manifest.json']
    assert len(members) == 1 and members[0].isfile(), '归档必须恰有一个普通文件 release-manifest.json'
    data = t.extractfile(members[0]).read()
manifest = json.loads(data)
assert manifest['platform'] == 'darwin/arm64' and manifest['sourceState'] == 'committed', '需要 darwin/arm64 且 committed 的清单'
with out.open('xb') as f:
    f.write(data)
print(json.dumps({'status': 'extracted', 'file': str(out), 'sha256': hashlib.sha256(data).hexdigest(),
                  'cliVersion': manifest['cliVersion'], 'cliCommit': manifest['cliCommit']}))
