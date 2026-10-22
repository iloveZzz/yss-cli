#!/usr/bin/env bash
# Workflow transport: one draft creation, explicit uploads and publication; no cleanup writes.
set -euo pipefail
umask 022
export PYTHONDONTWRITEBYTECODE=1
[[ $# == 4 ]]
tag=$1
run_id=$2
notes_commit=$3
record_dir=$4
helper="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)/publish_contract.py"
python3 - "$record_dir" <<'PY'
from pathlib import Path
import sys
root, records = Path.cwd().resolve(), Path(sys.argv[1]).resolve()
if records == root or root in records.parents:
    raise ValueError('records must be outside checkout')
PY
mkdir -- "$record_dir"
args=(--tag "$tag" --run-id "$run_id" --notes-commit "$notes_commit" --record-dir "$record_dir")
python3 "$helper" download "${args[@]}" > "$record_dir/before.json" 2> "$record_dir/download.stderr.log"
python3 "$helper" check "${args[@]}" > "$record_dir/ready.json" 2> "$record_dir/check.stderr.log"
cmp -- "$record_dir/before.json" "$record_dir/ready.json"
version=${tag#v}
upload="$record_dir/evidence/upload"
assets=("$upload/yss_${version}_darwin_arm64.tar.gz" "$upload/yss_${version}_windows_amd64.zip"
        "$upload/checksums.json" "$upload/build-only-checksums.json" "$upload/native-receipt.json"
        "$upload/release-gate.json" "$upload/release-input.json" "$upload/SHA256SUMS")
# gh release create performs hidden DELETE cleanup on upload/publish errors.
# Use explicit HTTP operations so every attempted write is visible and finite.
write_api() {
    local label=$1 method=$2 endpoint=$3 input=$4 content_type=$5 code
    local command=(gh api --method "$method" "$endpoint" --header "Content-Type: $content_type" --input "$input")
    python3 - "$record_dir/$label.request.json" "${command[@]}" <<'PYLOG'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps(sys.argv[2:])+'\n')
PYLOG
    [[ $? == 0 ]] || return 1
    "${command[@]}" > "$record_dir/$label.stdout.log" 2> "$record_dir/$label.stderr.log"
    code=$?
    python3 - "$record_dir/$label.result.json" "$code" <<'PYLOG'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({'exitCode': int(sys.argv[2])})+'\n')
PYLOG
    [[ $? == 0 ]] || return 1
    return "$code"
}
release_id=''
perform_writes() {
    local code identity_code name path
    write_api create POST "repos/$GITHUB_REPOSITORY/releases" "$record_dir/create.payload.json" application/json
    code=$?
    # Preserve an obtainable ID even if the process reported failure; it is
    # only a readback target in that case, never permission to resume writes.
    release_id="$(python3 - "$record_dir/create.stdout.log" 2> "$record_dir/create-identity.stderr.log" <<'PYID'
import json, pathlib, sys
value=json.loads(pathlib.Path(sys.argv[1]).read_bytes())['id']
if type(value) is not int or value <= 0:
    raise ValueError('created release ID must be numeric')
print(value)
PYID
)"
    identity_code=$?
    (( code == 0 )) || return "$code"
    (( identity_code == 0 )) || return 1
    for path in "${assets[@]}"; do
        name=${path##*/}
        python3 "$helper" draft "${args[@]}" --release-id "$release_id" > "$record_dir/draft-before-$name.json" 2> "$record_dir/draft-before-$name.stderr.log" || return 1
        write_api "upload-$name" POST "https://uploads.github.com/repos/$GITHUB_REPOSITORY/releases/$release_id/assets?name=$name" "$path" application/octet-stream || return $?
        python3 "$helper" asset "${args[@]}" --asset-name "$name" > "$record_dir/verified-$name.log" 2> "$record_dir/verified-$name.stderr.log" || return 1
    done
    python3 "$helper" draft "${args[@]}" --release-id "$release_id" --complete > "$record_dir/draft-complete.json" 2> "$record_dir/draft-complete.stderr.log" || return 1
    write_api publish PATCH "repos/$GITHUB_REPOSITORY/releases/$release_id" "$record_dir/publish.payload.json" application/json
}
set +e
perform_writes
write_code=$?
set -e
python3 - "$record_dir/write-sequence.result.json" "$write_code" <<'PYLOG'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({'exitCode': int(sys.argv[2])})+'\n')
PYLOG
read_args=("${args[@]}")
if [[ -n "$release_id" ]]; then
    read_args+=(--release-id "$release_id")
fi
set +e
python3 "$helper" readback "${read_args[@]}" > "$record_dir/readback.json" 2> "$record_dir/readback.stderr.log"
readback_code=$?
set -e
python3 - "$record_dir/readback.result.json" "$readback_code" <<'PY'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({'exitCode': int(sys.argv[2])})+'\n')
PY
if (( write_code != 0 || readback_code != 0 )); then
    echo 'Release creation or readback failed; remote state requires review. No further write attempted.' >&2
    exit 1
fi
echo 'Release creation and complete readback passed.'
