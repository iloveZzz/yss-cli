"""从仓库自身读取发行身份：程序版本取自 internal/domain/domain.go，模板版本/提交取自构建生成的 YSS_SOURCE_SNAPSHOT。

只读辅助模块，不放宽任何校验；读取不到或所有现役 Profile 不一致时直接抛错。
"""
import json
import os
import re
from pathlib import Path

def source_document(cli_root, ref):
    if ref != 'docs/source-lock.json' or not os.environ.get('YSS_SOURCE_SNAPSHOT'):
        return Path(cli_root) / ref
    snapshot = Path(os.environ['YSS_SOURCE_SNAPSHOT'])
    if not snapshot.is_absolute() or snapshot.resolve() != snapshot or not snapshot.is_file() or snapshot.is_symlink():
        raise ValueError('YSS_SOURCE_SNAPSHOT must be a canonical ordinary file')
    return snapshot


def repo_profiles(cli_root):
    lock = json.loads(source_document(cli_root, 'docs/source-lock.json').read_text(encoding='utf-8'))
    schema = lock.get('schemaVersion')
    if schema not in (2, 3, 5):
        raise ValueError('unsupported committed source-lock schema')
    profiles = ('spec', 'backend') if schema == 5 else ('spec', 'design', 'backend')
    if set(lock['profiles']) != set(profiles) or any(lock['profiles'][p].get('profile') != p for p in profiles):
        raise ValueError('source-lock Profile set does not match its schema')
    return profiles


def repo_version(cli_root):
    text = (Path(cli_root) / 'internal/domain/domain.go').read_text(encoding='utf-8')
    found = re.search(r'^const Version = "([^"]+)"$', text, re.M)
    if not found:
        raise ValueError('internal/domain/domain.go 中找不到 const Version')
    return found.group(1)


def template_identity(cli_root):
    """返回 (templateCommit, templateVersion)；所有现役 Profile 必须完全一致。"""
    lock = json.loads(source_document(cli_root, 'docs/source-lock.json').read_text(encoding='utf-8'))
    profiles = repo_profiles(cli_root)
    commits = {lock['profiles'][p]['templateCommit'] for p in profiles}
    versions = {lock['profiles'][p]['templateVersion'] for p in profiles}
    if len(commits) != 1 or len(versions) != 1:
        raise ValueError('所有现役 Profile 的模板提交/版本不一致，无法按单一模板身份发行')
    return commits.pop(), versions.pop()
