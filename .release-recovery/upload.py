"""Upload only missing, preverified CLI 2.7.1 assets to one fixed existing draft.

This operations-only script never creates, deletes, overwrites, or publishes a
release or asset. The unchanged publish workflow verification must run first.
"""
import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
from urllib.parse import quote

REPOSITORY = 'iloveZzz/yss-cli'
TAG = 'v2.7.1'
BINDING_KEYS = {'schemaVersion', 'repository', 'tag', 'sourceCommit', 'notesCommit', 'evidenceRunId', 'draftReleaseId'}


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def validate_bindings(value):
    require(set(value) == BINDING_KEYS and value['schemaVersion'] == 1, 'binding schema differs')
    require(value['repository'] == REPOSITORY and value['tag'] == TAG, 'wrong repository or release tag')
    for name in ('sourceCommit', 'notesCommit'):
        require(isinstance(value[name], str) and re.fullmatch('[0-9a-f]{40}', value[name]), name + ' must be a full commit')
    for name in ('evidenceRunId', 'draftReleaseId'):
        require(type(value[name]) is int and value[name] > 0, name + ' must be a positive integer')
    return value


def git(*args):
    return subprocess.check_output(['git', *args], text=True).strip()


class GitHubAPI:
    def __init__(self, directory):
        self.directory = directory
        self.sequence = 0

    def __call__(self, endpoint, *args):
        self.sequence += 1
        name = f'api-{self.sequence:04d}'
        command = ['gh', 'api', endpoint, *args]
        save(self.directory, name + '.request.json', {'command': command})
        result = subprocess.run(command, capture_output=True, text=True)
        (self.directory / (name + '.stdout.log')).write_text(result.stdout)
        (self.directory / (name + '.stderr.log')).write_text(result.stderr)
        save(self.directory, name + '.result.json', {'exitCode': result.returncode})
        require(result.returncode == 0, f'GitHub API failed; see {name} records')
        return json.loads(result.stdout)


def save(directory, name, value):
    (directory / name).write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')


def expected_assets(upload):
    names = [f'yss_{TAG[1:]}_darwin_arm64.tar.gz', f'yss_{TAG[1:]}_windows_amd64.zip',
             'checksums.json', 'build-only-checksums.json', 'native-receipt.json',
             'release-gate.json', 'release-input.json', 'SHA256SUMS']
    require({p.name for p in upload.iterdir()} == set(names), 'upload file set differs')
    result = {}
    for name in names:
        path = upload / name
        require(path.is_file() and not path.is_symlink(), 'asset must be an ordinary file: ' + name)
        result[name] = {'size': path.stat().st_size, 'digest': 'sha256:' + sha(path.read_bytes())}
    seen = set()
    for line in (upload / 'SHA256SUMS').read_text().splitlines():
        fields = line.split()
        require(len(fields) == 2, 'malformed SHA256SUMS line')
        digest, name = fields
        require(name in names[:-1] and name not in seen, 'unexpected or duplicate SHA256SUMS entry')
        require(result[name]['digest'] == 'sha256:' + digest, 'local digest mismatch: ' + name)
        seen.add(name)
    require(seen == set(names[:-1]), 'SHA256SUMS file set differs')
    return result


def verify_workflow_identity(evidence_run, workflow):
    require(workflow['path'] == '.github/workflows/release-evidence.yml' and evidence_run['workflow_id'] == workflow['id'], 'unexpected evidence workflow identity')
    require(evidence_run['event'] == 'workflow_dispatch' and evidence_run['path'].split('@', 1)[0] == workflow['path'], 'unexpected evidence workflow path or trigger')


def inspect_release(release, binding, expected, notes, *, complete=False):
    require(release['id'] == binding['draftReleaseId'] and release['tag_name'] == TAG, 'draft ID or tag differs')
    require(release['draft'] is True and release['prerelease'] is False, 'release must remain a non-prerelease draft')
    require((release.get('body') or '').rstrip('\n') == notes.rstrip('\n'), 'draft notes differ from fixed notes commit')
    assets = {item['name']: item for item in release['assets']}
    require(len(assets) == len(release['assets']), 'duplicate remote asset names')
    require(set(assets) <= set(expected), 'unexpected remote assets')
    if complete:
        require(set(assets) == set(expected), 'incomplete remote asset set')
    for name, item in assets.items():
        require(item['state'] == 'uploaded', 'remote asset is not fully uploaded: ' + name)
        require(all(item.get(k) == v for k, v in expected[name].items()), 'remote asset size or digest conflict: ' + name)
    return assets


def remote_source(binding, api):
    obj = api(f'repos/{REPOSITORY}/git/ref/tags/{TAG}')['object']
    for _ in range(5):
        if obj['type'] == 'commit':
            require(obj['sha'] == binding['sourceCommit'], 'remote release tag source differs')
            return
        require(obj['type'] == 'tag', 'unexpected release tag object type')
        obj = api(f'repos/{REPOSITORY}/git/tags/{obj["sha"]}')['object']
    raise ValueError('release tag nesting exceeds bound')


def run(options, record, directory):
    api = GitHubAPI(directory)
    binding_bytes = options.bindings.read_bytes()
    binding = validate_bindings(json.loads(binding_bytes))
    record['bindings'] = binding
    record['bindingsSha256'] = sha(binding_bytes)
    require(os.environ.get('GITHUB_ACTIONS') == 'true', 'run only in GitHub Actions')
    require(os.environ.get('GITHUB_REPOSITORY') == REPOSITORY, 'wrong Actions repository')
    require(os.environ.get('GITHUB_EVENT_NAME') == 'workflow_dispatch', 'manual workflow dispatch required')
    require(os.environ.get('RELEASE_TAG') == TAG, 'workflow tag input differs from fixed binding')
    require(os.environ.get('EVIDENCE_RUN_ID') == str(binding['evidenceRunId']), 'workflow evidence input differs from fixed binding')
    head = git('rev-parse', 'HEAD')
    require(head == os.environ.get('GITHUB_SHA'), 'checkout differs from dispatched operations commit')
    require(not git('status', '--porcelain=v1', '--untracked-files=all'), 'operations checkout must be clean')
    binding_ref = options.bindings.resolve().relative_to(Path.cwd().resolve()).as_posix()
    require(subprocess.check_output(['git', 'show', 'HEAD:' + binding_ref]) == binding_bytes, 'bindings must be committed in operations ref')
    require(git('rev-parse', TAG + '^{commit}') == binding['sourceCommit'], 'local release tag source differs')
    subprocess.run(['git', 'merge-base', '--is-ancestor', binding['sourceCommit'], binding['notesCommit']], check=True)
    subprocess.run(['git', 'merge-base', '--is-ancestor', binding['notesCommit'], head], check=True)
    notes_ref = 'docs/releases/' + TAG + '.md'
    notes = subprocess.check_output(['git', 'show', binding['notesCommit'] + ':' + notes_ref], text=True)
    require(Path(notes_ref).read_text() == notes and binding['sourceCommit'] in notes, 'checkout notes differ from pinned notes or omit source')
    record.update(operationsCommit=head, notesSha256=sha(notes.encode()), scriptSha256=sha(Path(__file__).read_bytes()))
    remote_source(binding, api)

    evidence_run = api(f'repos/{REPOSITORY}/actions/runs/{binding["evidenceRunId"]}')
    save(directory, 'evidence-run.json', evidence_run)
    require(evidence_run['id'] == binding['evidenceRunId'] and evidence_run['head_sha'] == binding['sourceCommit'], 'evidence run source differs')
    require(evidence_run['status'] == 'completed' and evidence_run['conclusion'] == 'success', 'evidence run must have completed successfully')
    workflow = api(f'repos/{REPOSITORY}/actions/workflows/release-evidence.yml')
    save(directory, 'evidence-workflow.json', workflow)
    verify_workflow_identity(evidence_run, workflow)
    require(evidence_run['repository']['full_name'] == REPOSITORY and evidence_run['head_repository']['full_name'] == REPOSITORY, 'evidence repository differs')
    artifacts = api(f'repos/{REPOSITORY}/actions/runs/{binding["evidenceRunId"]}/artifacts?per_page=100')
    save(directory, 'evidence-artifacts.json', artifacts)
    require(artifacts['total_count'] <= 100, 'artifact listing exceeds bounded page')
    chosen = [a for a in artifacts['artifacts'] if a['name'] == 'yss-release-evidence']
    require(len(chosen) == 1 and chosen[0]['expired'] is False, 'unique unexpired prepare artifact required')
    record['evidenceArtifact'] = {k: chosen[0].get(k) for k in ('id', 'name', 'digest', 'size_in_bytes')}
    log = json.loads((options.evidence_dir / 'orchestrator/run-log.json').read_text())
    require(log['command'] == 'prepare' and log['status'] == 'passed' and log.get('notExecuted') == [], 'complete prepare evidence required')
    require(log['steps'] and all(s['status'] == 'passed' for s in log['steps']), 'prepare contains non-passed steps')
    checks = json.loads((options.evidence_dir / 'release-final/checksums.json').read_text())
    require(checks['cliCommit'] == binding['sourceCommit'] and checks['version'] == TAG[1:], 'release evidence source or version differs')
    require(checks['stableReady'] is True and checks['pending'] == [], 'release is not ready')
    require(checks['requiredPlatforms'] == ['darwin/arm64'] and checks['qualificationScope'] == 'local-platform-program-install', 'qualification scope differs')
    upload = options.evidence_dir / 'upload'
    expected = expected_assets(upload)
    require(json.loads((upload / 'checksums.json').read_text()) == checks, 'upload checksums differ from release-final')
    windows = json.loads((upload / 'build-only-checksums.json').read_text())
    require(windows['cliCommit'] == binding['sourceCommit'] and windows['stableReady'] is False, 'Windows build-only scope differs')
    record['expectedAssets'] = expected
    release_path = f'repos/{REPOSITORY}/releases/{binding["draftReleaseId"]}'
    before = api(release_path)
    save(directory, 'release-before.json', before)
    inspect_release(before, binding, expected, notes)
    save(directory, 'upload-receipt.json', record)

    for name, metadata in expected.items():
        # Re-read before every write; refuse release publication or conflicting assets.
        current = api(release_path)
        assets = inspect_release(current, binding, expected, notes)
        if name in assets:
            record['actions'].append({'name': name, 'status': 'matched-existing', 'assetId': assets[name]['id']})
        else:
            remote_source(binding, api)
            path = upload / name
            require({'size': path.stat().st_size, 'digest': 'sha256:' + sha(path.read_bytes())} == metadata, 'local asset changed before upload')
            # Numeric release ID is the write target; no tag-based target lookup or overwrite.
            endpoint = f'https://uploads.github.com/{release_path}/assets?name={quote(name, safe="")}'
            action = {'name': name, 'status': 'attempting'}
            record['actions'].append(action)
            save(directory, 'upload-receipt.json', record)
            try:
                item = api(endpoint, '--method', 'POST', '--header', 'Content-Type: application/octet-stream', '--input', str(path))
                save(directory, 'uploaded-' + name + '.json', item)
                require(item['name'] == name and item['state'] == 'uploaded' and all(item.get(k) == v for k, v in metadata.items()), 'upload response size or digest differs')
                action.update(status='uploaded', assetId=item['id'])
            except BaseException as error:
                action.update(status='unknown-write', error=str(error))
                save(directory, 'upload-receipt.json', record)
                if not isinstance(error, KeyboardInterrupt):
                    try:
                        save(directory, 'release-after-failed-' + name + '.json', api(release_path))
                    except Exception as read_error:
                        action['readbackError'] = str(read_error)
                raise
        save(directory, 'upload-receipt.json', record)
    after = api(release_path)
    save(directory, 'release-after.json', after)
    inspect_release(after, binding, expected, notes, complete=True)
    remote_source(binding, api)
    record.update(status='passed', draft=True, releaseUrl=after['html_url'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--bindings', type=Path, required=True)
    parser.add_argument('--evidence-dir', type=Path, required=True)
    parser.add_argument('--record-dir', type=Path, required=True)
    options = parser.parse_args()
    directory = options.record_dir.resolve()
    require(Path.cwd().resolve() not in directory.parents and directory != Path.cwd().resolve(), 'records must be outside checkout')
    directory.mkdir()  # A rerun receives a fresh runner directory; never overwrite evidence.
    record = {'schemaVersion': 1, 'kind': 'cli-draft-upload', 'status': 'running', 'actions': [], 'startedAt': datetime.datetime.now(datetime.timezone.utc).isoformat()}
    try:
        run(options, record, directory)
    except BaseException as error:
        record.update(status='cancelled' if isinstance(error, KeyboardInterrupt) else 'failed', error=str(error))
        raise
    finally:
        record['finishedAt'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
        save(directory, 'upload-receipt.json', record)
    print(json.dumps(record))


if __name__ == '__main__':
    main()
