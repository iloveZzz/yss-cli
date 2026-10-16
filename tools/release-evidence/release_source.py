"""从仓库自身读取发行身份：程序版本取自 internal/domain/domain.go，模板版本/提交取自 docs/source-lock.json。

只读辅助模块，不放宽任何校验；读取不到或三个 Profile 不一致时直接抛错。
"""
import json
import re
from pathlib import Path

PROFILES = ('spec', 'design', 'backend')


def repo_version(cli_root):
    text = (Path(cli_root) / 'internal/domain/domain.go').read_text(encoding='utf-8')
    found = re.search(r'^const Version = "([^"]+)"$', text, re.M)
    if not found:
        raise ValueError('internal/domain/domain.go 中找不到 const Version')
    return found.group(1)


def template_identity(cli_root):
    """返回 (templateCommit, templateVersion)；三个 Profile 必须完全一致。"""
    lock = json.loads((Path(cli_root) / 'docs/source-lock.json').read_text(encoding='utf-8'))
    if sorted(lock['profiles']) != sorted(PROFILES):
        raise ValueError('docs/source-lock.json 必须恰好包含 spec/design/backend 三个 Profile')
    commits = {lock['profiles'][p]['templateCommit'] for p in PROFILES}
    versions = {lock['profiles'][p]['templateVersion'] for p in PROFILES}
    if len(commits) != 1 or len(versions) != 1:
        raise ValueError('三个 Profile 的模板提交/版本不一致，无法按单一模板身份发行')
    return commits.pop(), versions.pop()
