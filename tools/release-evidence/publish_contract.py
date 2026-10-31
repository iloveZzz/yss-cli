"""Read-only release contract checks. GitHub writes belong to publish-release.sh."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import zipfile

REPOSITORY = 'iloveZzz/yss-cli'
WORKFLOW = '.github/workflows/release-evidence.yml'
PUBLISH_WORKFLOW = '.github/workflows/publish-release.yml'
# Review permission and token bindings again before accepting changed workflow bytes.
PUBLISH_WORKFLOW_SHA256 = 'ce44380729a9291d018d0e9a92294c9cc0b26c454ea00328ef775c38a47121b7'
SCOPE = 'local-platform-program-install'
DOCUMENTS = ('docs/source-lock.json', 'docs/compatibility.md', 'docs/porting-status.md',
             'docs/native-governance.md', 'docs/cli-retirement.md', 'compat/README.md', 'README.md')
PREPARE_STEPS = ('01-fetch-bundle', '02-build-darwin', '03-native-receipt', '04-prepare-native-input',
                 '05-package-candidate', '06-run-program-installation', '07-prepare-gate', '08-package-stable',
                 '09-extract-mac-manifest', '10-package-windows', '11-check-outputs', '12-collect-upload',
                 '13-source-unchanged')


def require(condition, message):
    if not condition:
        raise ValueError(message)


def sha(data):
    return hashlib.sha256(data).hexdigest()


def git(root, *args):
    return subprocess.check_output(['git', '-C', str(root), *args])


def ordinary(root, ref):
    require(isinstance(ref, str) and ref and '\\' not in ref and ':' not in ref and
            all(p not in ('', '.', '..') for p in ref.split('/')), 'unsafe evidence path')
    p = root / ref
    require(not Path(ref).is_absolute() and p.resolve() == p.absolute() and p.is_file() and
            not p.is_symlink(), 'ordinary evidence file required: ' + ref)
    return p.read_bytes()


def load(root, ref):
    return json.loads(ordinary(root, ref))


def remote_source(api, prefix, tag):
    obj = api(prefix + '/git/ref/tags/' + tag)['object']
    for _ in range(5):
        if obj['type'] == 'commit':
            require(re.fullmatch('[0-9a-f]{40}', obj['sha']), 'remote source must be a full commit')
            return obj['sha']
        require(obj['type'] == 'tag', 'unexpected tag object')
        obj = api(prefix + '/git/tags/' + obj['sha'])['object']
    raise ValueError('tag nesting exceeds bound')


def publisher_context(root, env, api):
    require(env.get('GITHUB_ACTIONS') == 'true' and env.get('GITHUB_REPOSITORY') == REPOSITORY and
            env.get('GITHUB_EVENT_NAME') == 'workflow_dispatch', 'same-repository workflow_dispatch required')
    for key in ('GITHUB_REPOSITORY_ID', 'GITHUB_RUN_ID', 'GITHUB_RUN_ATTEMPT'):
        require(re.fullmatch('[1-9][0-9]*', env.get(key, '')), 'explicit publisher context required: ' + key)
    operations = git(root, 'rev-parse', 'HEAD').decode().strip()
    require(operations == env.get('GITHUB_SHA') and not git(root, 'status', '--porcelain=v1', '--untracked-files=all'),
            'clean dispatched checkout required')
    workflow_bytes = git(root, 'show', operations + ':' + PUBLISH_WORKFLOW)
    require(sha(workflow_bytes) == PUBLISH_WORKFLOW_SHA256 and ordinary(root, PUBLISH_WORKFLOW) == workflow_bytes,
            'reviewed publisher workflow permission and token contract required')
    prefix = 'repos/' + REPOSITORY
    repo = api(prefix)
    require(repo['full_name'] == REPOSITORY and repo['id'] == int(env['GITHUB_REPOSITORY_ID']), 'publisher repository differs')
    run = api(prefix + '/actions/runs/' + env['GITHUB_RUN_ID'])
    workflow = api(prefix + '/actions/workflows/publish-release.yml')
    require(run['id'] == int(env['GITHUB_RUN_ID']) and run['run_attempt'] == int(env['GITHUB_RUN_ATTEMPT']) and
            run['head_sha'] == operations and run['status'] == 'in_progress' and run['conclusion'] is None,
            'actual active publisher run at operations commit required')
    require(workflow['path'] == PUBLISH_WORKFLOW and run['workflow_id'] == workflow['id'] and
            run['path'].split('@', 1)[0] == PUBLISH_WORKFLOW and run['event'] == 'workflow_dispatch',
            'publisher workflow identity differs')
    require(all(run[k]['full_name'] == REPOSITORY and run[k]['id'] == repo['id']
                for k in ('repository', 'head_repository')), 'publisher run repository differs')
    return operations


def remote_evidence(api, prefix, run_id, source):
    repo = api(prefix)
    require(repo['full_name'] == REPOSITORY, 'same evidence repository required')
    run = api(prefix + '/actions/runs/' + run_id)
    workflow = api(prefix + '/actions/workflows/release-evidence.yml')
    require(run['id'] == int(run_id) and run['head_sha'] == source and run['status'] == 'completed' and
            run['conclusion'] == 'success', 'completed successful prepare run at source required')
    require(workflow['path'] == WORKFLOW and run['workflow_id'] == workflow['id'] and
            run['path'].split('@', 1)[0] == WORKFLOW and run['event'] == 'workflow_dispatch',
            'evidence workflow identity differs')
    require(all(run[k]['full_name'] == REPOSITORY and run[k]['id'] == repo['id']
                for k in ('repository', 'head_repository')), 'evidence repository differs')
    artifacts = api(prefix + '/actions/runs/' + run_id + '/artifacts?per_page=100')
    require(artifacts['total_count'] == len(artifacts['artifacts']) <= 100, 'incomplete artifact listing')
    chosen = [a for a in artifacts['artifacts'] if a['name'] == 'yss-release-evidence']
    require(len(chosen) == 1 and chosen[0]['expired'] is False, 'unique unexpired prepare artifact required')
    artifact = chosen[0]
    require(type(artifact['id']) is int and artifact['id'] > 0 and
            re.fullmatch('sha256:[0-9a-f]{64}', artifact.get('digest') or ''), 'artifact identity or digest missing')
    arun = artifact['workflow_run']
    require(arun['id'] == int(run_id) and arun['head_sha'] == source and
            arun['repository_id'] == arun['head_repository_id'] == repo['id'], 'artifact run identity differs')
    jobs = api(prefix + '/actions/runs/' + run_id + '/jobs?per_page=100')
    require(jobs['total_count'] == len(jobs['jobs']) <= 100, 'incomplete job listing')
    mac = [j for j in jobs['jobs'] if j['name'] == 'mac']
    require(len(mac) == 1 and mac[0]['status'] == 'completed' and mac[0]['conclusion'] == 'success', 'successful Mac prepare job required')
    steps = mac[0]['steps']
    for name, conclusion in [('Generate and validate release evidence', 'success'),
                             ('Download original evidence', 'skipped'),
                             ('Verify public upgrade with independent digest', 'skipped')]:
        selected = [s for s in steps if s['name'] == name]
        require(len(selected) == 1 and selected[0]['status'] == 'completed' and
                selected[0]['conclusion'] == conclusion, 'run is not the prepare phase: ' + name)
    return {k: artifact[k] for k in ('id', 'name', 'digest', 'size_in_bytes')}


def release_candidates(api, prefix, tag):
    # A successful authenticated listing sees drafts too. A 404 or any other API
    # failure is never interpreted as absence.
    candidates = []
    for page in range(1, 101):
        releases = api(prefix + f'/releases?per_page=100&page={page}')
        require(isinstance(releases, list), 'release listing is unknown')
        candidates.extend(r for r in releases if r['tag_name'] == tag)
        if len(releases) < 100:
            return candidates
    raise ValueError('release listing exceeds bound')


def no_release(api, prefix, tag):
    require(not release_candidates(api, prefix, tag), 'release already exists (draft or published)')


def write_payloads(tag, source, notes):
    values = {
        'create.payload.json': dict(tag_name=tag, target_commitish=source, name='yss-cli ' + tag[1:],
                                    body=notes.decode('utf-8'), draft=True, prerelease=False, make_latest='false'),
        'publish.payload.json': dict(draft=False, prerelease=False, make_latest='true'),
    }
    return {name: (json.dumps(value) + '\n').encode('utf-8') for name, value in values.items()}


def verify_write_payloads(records, tag, source, notes):
    for name, expected in write_payloads(tag, source, notes).items():
        require(ordinary(records, name) == expected, 'write payload bytes differ: ' + name)


def expected_assets(evidence, tag):
    names = {f'yss_{tag[1:]}_darwin_arm64.tar.gz', f'yss_{tag[1:]}_windows_amd64.zip',
             'checksums.json', 'build-only-checksums.json', 'native-receipt.json',
             'release-gate.json', 'release-input.json'}
    upload = evidence / 'upload'
    require({p.name for p in upload.iterdir()} == names | {'SHA256SUMS'}, 'upload file set differs')
    expected = {name: dict(size=len(data), digest='sha256:' + sha(data))
                for name in sorted(names | {'SHA256SUMS'}) for data in [ordinary(upload, name)]}
    seen = set()
    for line in ordinary(upload, 'SHA256SUMS').decode('utf-8').splitlines():
        fields = line.split()
        require(len(fields) == 2, 'malformed SHA256SUMS')
        digest, name = fields
        require(name in names and name not in seen and expected[name]['digest'] == 'sha256:' + digest,
                'payload digest, name or duplicate differs')
        seen.add(name)
    require(seen == names, 'seven payload digests required')
    return expected


def local_evidence(root, evidence, tag, source, api):
    version = tag[1:]
    version_file = git(root, 'show', source + ':internal/domain/domain.go').decode()
    found = re.search(r'^const Version = "([^"]+)"$', version_file, re.M)
    require(found and found[1] == version, 'source version differs from tag')
    config = json.loads(git(root, 'show', source + ':docs/template-release.json'))
    template_tag = config.get('tag')
    require(set(config) == {'tag'} and re.fullmatch(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', template_tag or ''), 'stable template release tag required')
    inp = load(evidence, 'release-input.json')
    lock_doc = inp['documents']['docs/source-lock.json']
    lock_bytes = ordinary(evidence, lock_doc['path'])
    require(sha(lock_bytes) == lock_doc['sha256'], 'generated source snapshot digest differs')
    lock = json.loads(lock_bytes)
    require(lock['schemaVersion'] in (2, 3, 5), 'unsupported source-lock')
    profiles = {'spec', 'backend'} if lock['schemaVersion'] == 5 else {'spec', 'design', 'backend'}
    require(set(lock['profiles']) == profiles, 'source-lock profile set differs')
    require(len({p['templateCommit'] for p in lock['profiles'].values()}) == 1 and
            len({p['templateVersion'] for p in lock['profiles'].values()}) == 1, 'template identity must be consistent across profiles')
    require(all(p.get('templateTag') == template_tag and p['templateVersion'] == template_tag[1:] for p in lock['profiles'].values()), 'snapshot must bind the declared template release')
    require(remote_source(api, 'repos/iloveZzz/yss-spec-project-template', template_tag) ==
            next(iter(lock['profiles'].values()))['templateCommit'], 'published template tag differs from source snapshot')
    checks = load(evidence, 'release-final/checksums.json')
    require(checks['version'] == version and checks['cliCommit'] == source and
            checks['sourceLockSha256'] == sha(lock_bytes) and checks['sourceState'] == 'committed', 'release source identity differs')
    bundles = checks['bundles']
    require(set(bundles) == profiles, 'bundle profile set differs')
    for profile in profiles:
        row, pin = bundles[profile], lock['profiles'][profile]
        require(pin['profile'] == profile and row['templateCommit'] == pin['templateCommit'] and
                row['templateVersion'] == pin['templateVersion'] and row['sourceState'] == 'committed' and
                re.fullmatch('[0-9a-f]{40}', row['templateCommit']), 'fixed template identity differs')
        require(all(re.fullmatch('[0-9a-f]{64}', row[k]) for k in
                    ('sourceSnapshotHash', 'manifestHash', 'bundleHash')), 'bundle digest missing')
    require(checks['stableReady'] is True and checks['pending'] == [] and
            checks['qualificationScope'] == SCOPE and checks['requiredPlatforms'] == ['darwin/arm64'], 'Mac qualification differs')
    log = load(evidence, 'orchestrator/run-log.json')
    require(log['command'] == 'prepare' and log['status'] == 'passed' and log['notExecuted'] == [] and
            [s['id'] for s in log['steps']] == list(PREPARE_STEPS) and
            all(s['status'] == 'passed' and s.get('exitCode', 0) == 0 for s in log['steps']) and
            log['preflight']['cliCommit'] == source and log['preflight']['version'] == version, 'complete same-source prepare required')
    native = load(evidence, 'native-input.json')
    receipt = load(evidence, 'native-darwin-arm64/native-receipt.json')
    gate = load(evidence, 'release-gates/release-gate.json')
    windows = load(evidence, 'windows-artifact/build-only-checksums.json')
    for value in (inp, native, receipt, gate):
        require(value['cliCommit'] == source and value.get('cliVersion', value.get('version')) == version and
                value['sourceLockSha256'] == sha(lock_bytes) and value['sourceState'] == 'committed' and
                value['bundles'] == bundles, 'input/platform identity differs')
    require(windows['cliCommit'] == source and windows['version'] == version and windows['sourceState'] == 'committed' and
            windows['sourceLockSha256'] == sha(lock_bytes) and set(windows['bundles']) == profiles, 'Windows source identity differs')
    for profile, identity in bundles.items():
        inspection, pin = windows['bundles'][profile], lock['profiles'][profile]
        require({k: inspection[k] for k in identity} == identity and inspection['profile'] == profile and
                inspection['schemaVersion'] in ((4, 5) if lock['schemaVersion'] == 5 else (2, 3)) and
                inspection['producer'] == lock['producer'] and inspection['sourcePolicy']['path'] == pin['policyPath'] and
                inspection['sourcePolicy']['digest'] == pin['policyHash'], 'Windows manifest template inspection differs')
    for value in (inp, native, gate):
        require(value['qualificationScope'] == SCOPE, 'input qualification differs')
    identity_keys = ('cliVersion', 'protocolVersion', 'cliCommit', 'sourceState', 'sourceLockSha256', 'bundles')
    require(all({k: v[k] for k in identity_keys} == {k: inp[k] for k in identity_keys}
                for v in (native, receipt, gate)), 'platform/input protocol identity differs')
    require(inp['requiredPlatforms'] == native['requiredPlatforms'] == ['darwin/arm64'] and
            inp['artifacts'] == native['artifacts'] and [a['platform'] for a in inp['artifacts']] == ['darwin/arm64'], 'input platform set differs')
    require(native['releaseGate'] == {'path': '', 'sha256': ''}, 'native candidate must not claim a final gate')
    for value in (receipt, gate):
        require(value['status'] == 'passed' and value['exitCode'] == 0 and value['inputDrift'] is False and
                value['unexecuted'] == [], 'receipt or gate is incomplete')
    require(receipt['platform'] == receipt['runtimePlatform'] == 'darwin/arm64' and
            receipt['runtimeVerification'] == 'native-runner', 'native Mac receipt required')
    require(len(gate['checks']) == 1 and gate['checks'][0]['id'] == 'program-installation' and
            all(c['status'] == 'passed' and c['exitCode'] == 0 and c['inputDrift'] is False and
                c['unexecuted'] == [] for c in gate['checks']), 'installation gate differs')
    artifact = inp['artifacts'][0]
    binary = sha(ordinary(evidence, artifact['binary']['path']))
    require(artifact['binary']['sha256'] == receipt['binarySha256'] == receipt['binaryBeforeSha256'] ==
            receipt['binaryAfterSha256'] == binary, 'native binary digest differs')
    receipt_sha = sha(ordinary(evidence, 'native-darwin-arm64/native-receipt.json'))
    gate_sha = sha(ordinary(evidence, 'release-gates/release-gate.json'))
    require(artifact['receipt']['path'] == 'native-darwin-arm64/native-receipt.json' and
            artifact['receipt']['sha256'] == receipt_sha and inp['releaseGate'] ==
            {'path': 'release-gates/release-gate.json', 'sha256': gate_sha} and
            checks['releaseGateSha256'] == gate_sha and
            checks['inputManifestSha256'] == sha(ordinary(evidence, 'release-input.json')), 'input digest binding differs')
    require(set(inp['documents']) == set(native['documents']) == set(DOCUMENTS), 'input document set differs')
    for name in DOCUMENTS:
        doc = inp['documents'][name]
        require(doc == native['documents'][name] and sha(ordinary(evidence, doc['path'])) == doc['sha256'] and
                ordinary(evidence, doc['path']) == (lock_bytes if name == 'docs/source-lock.json' else git(root, 'show', source + ':' + name)), 'input document differs: ' + name)
    require(len(checks['artifacts']) == 1, 'single Mac artifact required')
    mac = checks['artifacts'][0]
    require(mac['platform'] == 'darwin/arm64' and mac['nativeRuntimeVerified'] is True and
            mac['archive'] == f'yss_{version}_darwin_arm64.tar.gz' and mac['nativeReceiptSha256'] == receipt_sha and
            mac['sha256'] == sha(ordinary(evidence, 'release-final/' + mac['archive'])), 'Mac artifact binding differs')
    candidate = load(evidence, 'candidate/checksums.json')
    require(candidate['stableReady'] is False and candidate['pending'] == ['program-installation'] and
            candidate['qualificationScope'] == SCOPE, 'candidate qualification differs')
    require(windows['stableReady'] is False and windows['nativeRuntimeVerified'] is False and
            windows['runtimeVerification'] == 'not-executed' and len(windows['artifacts']) == 1, 'Windows must remain build-only')
    win = windows['artifacts'][0]
    require(win['platform'] == 'windows/amd64' and win['compiled'] is True and win['nativeRuntimeVerified'] is False and
            win['archive'] == f'yss_{version}_windows_amd64.zip' and
            win['sha256'] == sha(ordinary(evidence, 'windows-artifact/' + win['archive'])), 'Windows artifact binding differs')
    with zipfile.ZipFile(evidence / 'windows-artifact' / win['archive']) as archive:
        manifest = json.loads(archive.read('release-manifest.json'))
    require(manifest['stableReady'] is False and manifest['runtimeVerification'] == 'not-executed' and
            manifest['platform'] == 'windows/amd64', 'Windows archive qualification differs')
    expected = expected_assets(evidence, tag)
    copies = {mac['archive']: 'release-final/' + mac['archive'], win['archive']: 'windows-artifact/' + win['archive'],
              'checksums.json': 'release-final/checksums.json', 'build-only-checksums.json': 'windows-artifact/build-only-checksums.json',
              'native-receipt.json': 'native-darwin-arm64/native-receipt.json',
              'release-gate.json': 'release-gates/release-gate.json', 'release-input.json': 'release-input.json'}
    require(all(ordinary(evidence / 'upload', name) == ordinary(evidence, ref) for name, ref in copies.items()), 'upload source bytes differ')
    return expected


def check(root, evidence, tag, run_id, notes_commit, env, api, *, published=False):
    require(re.fullmatch(r'v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)', tag or ''), 'explicit stable tag required')
    require(re.fullmatch('[1-9][0-9]*', run_id or ''), 'explicit prepare run required')
    require(re.fullmatch('[0-9a-f]{40}', notes_commit or ''), 'explicit full notes_commit required')
    operations = publisher_context(root, env, api)
    source = git(root, 'rev-parse', 'refs/tags/' + tag + '^{commit}').decode().strip()
    prefix = 'repos/' + REPOSITORY
    require(remote_source(api, prefix, tag) == source, 'remote tag source differs')
    for left, right in ((source, notes_commit), (notes_commit, operations)):
        result = subprocess.run(['git', '-C', str(root), 'merge-base', '--is-ancestor', left, right], capture_output=True)
        require(result.returncode == 0, 'source/notes/operations ancestry differs')
    ref = 'docs/releases/' + tag + '.md'
    notes = git(root, 'show', notes_commit + ':' + ref)
    require(git(root, 'show', operations + ':' + ref) == notes and ordinary(root, ref) == notes and
            source.encode() in notes, 'exact committed notes or full source differs')
    artifact = remote_evidence(api, prefix, run_id, source)
    assets = local_evidence(root, evidence, tag, source, api)
    if not published:
        no_release(api, prefix, tag)
    evidence_digests = {p.relative_to(evidence).as_posix(): sha(ordinary(evidence, p.relative_to(evidence).as_posix()))
                        for p in sorted(evidence.rglob('*')) if not p.is_dir()}
    return dict(sourceCommit=source, notesCommit=notes_commit, operationsCommit=operations,
                notesSha256=sha(notes), evidenceRunId=int(run_id), evidenceArtifact=artifact,
                expectedAssets=assets, evidenceDigests=evidence_digests)


def verify_asset(item, name, meta):
    require(item['name'] == name and type(item['id']) is int and item['id'] > 0 and item['state'] == 'uploaded' and
            all(item.get(k) == value for k, value in meta.items()), 'remote asset size or digest differs: ' + name)


def verify_assets(release, expected, *, complete=True):
    assets = {a['name']: a for a in release['assets']}
    require(len(assets) == len(release['assets']) and set(assets) <= set(expected), 'remote asset set differs')
    require(not complete or set(assets) == set(expected), 'remote asset set is incomplete')
    for name, item in assets.items():
        verify_asset(item, name, expected[name])


def verify_draft(release, release_id, tag, notes, expected, *, complete):
    require(release['id'] == release_id and release['tag_name'] == tag and release['draft'] is True and
            release['prerelease'] is False, 'new release must remain the exact non-prerelease draft')
    require(isinstance(release.get('body'), str) and release['body'].encode('utf-8') == notes, 'draft notes bytes differ')
    verify_assets(release, expected, complete=complete)


def verify_release(release, latest, tag, notes, expected):
    require(type(release['id']) is int and release['id'] > 0 and release['tag_name'] == tag and
            release['draft'] is False and release['prerelease'] is False, 'release must be formal and non-prerelease')
    require(isinstance(release.get('body'), str) and release['body'].encode('utf-8') == notes, 'remote notes bytes differ')
    verify_assets(release, expected)
    require(latest['id'] == release['id'] and latest['tag_name'] == tag, 'latest points to another release')


class GitHub:
    """Only GET API requests; preserve raw bytes and exit statuses outside checkout."""
    def __init__(self, records):
        self.records = records
        self.sequence = len(list(records.glob('api-*.result.json')))

    def raw(self, endpoint):
        self.sequence += 1
        stem = self.records / f'api-{self.sequence:04d}'
        command = ['gh', 'api', '--method', 'GET', endpoint]
        stem.with_suffix('.request.json').write_text(json.dumps(command) + '\n')
        with stem.with_suffix('.stdout.log').open('xb') as out, stem.with_suffix('.stderr.log').open('xb') as err:
            result = subprocess.run(command, stdout=out, stderr=err)
        stem.with_suffix('.result.json').write_text(json.dumps(dict(exitCode=result.returncode)) + '\n')
        require(result.returncode == 0, 'GitHub read failed; see ' + stem.name)
        return stem.with_suffix('.stdout.log').read_bytes()

    def __call__(self, endpoint):
        return json.loads(self.raw(endpoint))


def extract_archive(data, destination):
    import io
    destination.mkdir()
    with zipfile.ZipFile(io.BytesIO(data)) as archive:
        names = set()
        for item in archive.infolist():
            parts = item.filename.rstrip('/').split('/')
            require(item.filename and not item.filename.startswith('/') and '\\' not in item.filename and
                    all(p not in ('', '.', '..') and ':' not in p for p in parts) and item.filename not in names and
                    not stat.S_ISLNK(item.external_attr >> 16), 'unsafe or duplicate artifact member')
            names.add(item.filename)
        archive.extractall(destination)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('mode', choices=('download', 'check', 'draft', 'asset', 'readback'))
    for name in ('tag', 'run-id', 'notes-commit', 'record-dir'):
        parser.add_argument('--' + name, required=True)
    parser.add_argument('--release-id')
    parser.add_argument('--asset-name')
    parser.add_argument('--complete', action='store_true')
    options = parser.parse_args()
    root, records = Path.cwd().resolve(), Path(options.record_dir).resolve()
    require(records != root and root not in records.parents, 'records must be outside checkout')
    api = GitHub(records)
    evidence = records / 'evidence'
    prefix = 'repos/' + REPOSITORY
    if options.release_id is not None:
        require(re.fullmatch('[1-9][0-9]*', options.release_id), 'release ID must be numeric')
    if options.mode == 'asset':
        before = json.loads((records / 'before.json').read_bytes())
        require(options.asset_name in before['expectedAssets'], 'unexpected asset response')
        item = load(records, 'upload-' + options.asset_name + '.stdout.log')
        verify_asset(item, options.asset_name, before['expectedAssets'][options.asset_name])
        return
    if options.mode == 'draft':
        require(options.release_id, 'known newly created release ID required')
        before = json.loads((records / 'before.json').read_bytes())
        notes = ordinary(records, 'notes.md')
        require(sha(notes) == before['notesSha256'] and expected_assets(evidence, options.tag) == before['expectedAssets'], 'frozen upload inputs changed')
        require(git(root, 'rev-parse', 'HEAD').decode().strip() == before['operationsCommit'] and
                not git(root, 'status', '--porcelain=v1', '--untracked-files=all'), 'operations checkout changed')
        require(remote_source(api, prefix, options.tag) == before['sourceCommit'], 'remote tag changed during upload')
        current = api(prefix + '/releases/' + options.release_id)
        verify_draft(current, int(options.release_id), options.tag, notes, before['expectedAssets'], complete=options.complete)
        if options.complete:
            facts = check(root, evidence, options.tag, options.run_id, options.notes_commit, os.environ, api, published=True)
            require(facts == before and 'sha256:' + sha((records / 'artifact.zip').read_bytes()) == facts['evidenceArtifact']['digest'],
                    'publish inputs or remote identity changed')
        verify_write_payloads(records, options.tag, before['sourceCommit'], notes)
        print(json.dumps(current, sort_keys=True))
        return
    if options.mode == 'download':
        # Obtain remote identity before downloading by numeric artifact ID.
        require(re.fullmatch(r'v[0-9]+\.[0-9]+\.[0-9]+', options.tag or '') and
                re.fullmatch('[1-9][0-9]*', options.run_id or ''), 'explicit tag/run required')
        publisher_context(root, os.environ, api)
        source = remote_source(api, prefix, options.tag)
        artifact = remote_evidence(api, prefix, options.run_id, source)
        data = api.raw(prefix + f'/actions/artifacts/{artifact["id"]}/zip')
        require('sha256:' + sha(data) == artifact['digest'] and len(data) == artifact['size_in_bytes'], 'downloaded artifact digest or size differs')
        (records / 'artifact.zip').write_bytes(data)
        extract_archive(data, evidence)
    published = options.mode == 'readback'
    observed = {}
    read_errors = []
    if published:
        # Preserve obtainable release/asset identities even if tag/local checks
        # fail later, or one of these independent GET requests is unavailable.
        try:
            if options.release_id:
                observed['release'] = api(prefix + '/releases/' + options.release_id)
            else:
                # Create may have succeeded without returning its ID. A tag
                # lookup excludes drafts; the authenticated list preserves all
                # obtainable candidates without selecting a new write target.
                candidates = release_candidates(api, prefix, options.tag)
                require(len(candidates) == 1, 'unknown create result: unique observed release required')
                observed['release'] = candidates[0]
        except Exception as error:
            read_errors.append(str(error))
        for key, endpoint in [('latest', prefix + '/releases/latest')]:
            try:
                observed[key] = api(endpoint)
            except Exception as error:
                read_errors.append(str(error))
    facts = check(root, evidence, options.tag, options.run_id, options.notes_commit, os.environ, api, published=published)
    require('sha256:' + sha((records / 'artifact.zip').read_bytes()) == facts['evidenceArtifact']['digest'], 'artifact archive changed')
    notes = git(root, 'show', options.notes_commit + ':docs/releases/' + options.tag + '.md')
    if options.mode == 'download':
        (records / 'notes.md').write_bytes(notes)
        for name, data in write_payloads(options.tag, facts['sourceCommit'], notes).items():
            with (records / name).open('xb') as output:
                output.write(data)
    else:
        require((records / 'notes.md').read_bytes() == notes, 'frozen notes changed')
    if published:
        require(not read_errors, 'release readback is unknown: ' + '; '.join(read_errors))
        before = json.loads((records / 'before.json').read_bytes())
        require(facts == before, 'local inputs or remote identity changed after creation')
        require(not options.release_id or observed['release']['id'] == int(options.release_id), 'created release ID differs')
        verify_release(observed['release'], observed['latest'], options.tag, notes, facts['expectedAssets'])
    verify_write_payloads(records, options.tag, facts['sourceCommit'], notes)
    print(json.dumps(facts, sort_keys=True))


if __name__ == '__main__':
    main()
