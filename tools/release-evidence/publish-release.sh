#!/usr/bin/env bash
# Workflow transport: one creation attempt, followed exclusively by GET readback.
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
command=(gh release create "$tag" "${assets[@]}" --repo "$GITHUB_REPOSITORY" --verify-tag
         --title "yss-cli $version" --notes-file "$record_dir/notes.md" --latest)
python3 - "$record_dir/create.request.json" "${command[@]}" <<'PY'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps(sys.argv[2:])+'\n')
PY
set +e
"${command[@]}" > "$record_dir/create.stdout.log" 2> "$record_dir/create.stderr.log"
create_code=$?
set -e
python3 - "$record_dir/create.result.json" "$create_code" <<'PY'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({'exitCode': int(sys.argv[2])})+'\n')
PY
set +e
python3 "$helper" readback "${args[@]}" > "$record_dir/readback.json" 2> "$record_dir/readback.stderr.log"
readback_code=$?
set -e
python3 - "$record_dir/readback.result.json" "$readback_code" <<'PY'
import json, pathlib, sys
pathlib.Path(sys.argv[1]).write_text(json.dumps({'exitCode': int(sys.argv[2])})+'\n')
PY
if (( create_code != 0 || readback_code != 0 )); then
    echo 'Release creation or readback failed; remote state requires review. No further write attempted.' >&2
    exit 1
fi
echo 'Release creation and complete readback passed.'
