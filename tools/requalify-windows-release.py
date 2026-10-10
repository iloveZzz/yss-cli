"""Bind unchanged release payloads to existing macOS and native Windows evidence."""
import copy
import gzip
import hashlib
import io
import json
import stat
import subprocess
import sys
import tarfile
import zipfile
from pathlib import Path


def sha(data):
    return hashlib.sha256(data).hexdigest()


def encode(value):
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode()


def payload(file):
    if file.suffix == ".zip":
        with zipfile.ZipFile(file) as archive:
            entries = archive.infolist()
            assert all(not item.flag_bits & 1 and stat.S_ISREG(item.external_attr >> 16) for item in entries)
            assert all(item.file_size <= 35_000_000 for item in entries)
            files = {item.filename: archive.read(item) for item in entries}
    else:
        with tarfile.open(file, "r:gz") as archive:
            entries = archive.getmembers()
            assert all(item.isfile() and item.size <= 35_000_000 for item in entries)
            files = {item.name: archive.extractfile(item).read() for item in entries}
    manifest = json.loads(files["release-manifest.json"])
    expected = {"README.md", "docs/source-lock.json", "docs/compatibility.md", "docs/porting-status.md",
                "docs/native-governance.md", "docs/cli-retirement.md", "compat/README.md"}
    binary = "yss.exe" if manifest["platform"] == "windows/amd64" else "yss"
    assert len(entries) == 9 and set(files) == expected | {binary, "release-manifest.json"}
    assert set(manifest["files"]) == expected | {binary}
    for ref, descriptor in manifest["files"].items():
        assert descriptor == {"type": "file", "digest": sha(files[ref]), "mode": 0o755 if ref == binary else 0o644}
    assert manifest["binarySha256"] == sha(files[binary])
    return files, manifest


def pack(file, files, binary):
    if file.suffix == ".zip":
        with zipfile.ZipFile(file, "x", compression=zipfile.ZIP_DEFLATED, compresslevel=9) as archive:
            for ref, data in sorted(files.items()):
                item = zipfile.ZipInfo(ref, (1980, 1, 1, 0, 0, 0))
                item.create_system = 3
                item.external_attr = (stat.S_IFREG | (0o755 if ref == binary else 0o644)) << 16
                item.compress_type = zipfile.ZIP_DEFLATED
                archive.writestr(item, data)
    else:
        with file.open("xb") as raw, gzip.GzipFile(fileobj=raw, mode="wb", mtime=0) as compressed:
            with tarfile.open(fileobj=compressed, mode="w") as archive:
                for ref, data in sorted(files.items()):
                    item = tarfile.TarInfo(ref)
                    item.size, item.mode = len(data), 0o755 if ref == binary else 0o644
                    archive.addfile(item, io.BytesIO(data))


def main():
    original, native, previous, out = map(lambda value: Path(value).resolve(), sys.argv[1:])
    assert subprocess.check_output(["git", "status", "--porcelain"]) == b""
    assembler = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    proof = json.loads((original / "checksums.json").read_bytes())
    trial = json.loads((original / "checksums-trial.json").read_bytes())
    assert proof["version"] == "1.3.5" and proof["schemaVersion"] == 2 and proof["stableReady"] is True
    assert proof["sourceState"] == "committed" and proof["pending"] == []
    assert proof["requiredPlatforms"] == ["darwin/arm64"] and len(proof["artifacts"]) == 1
    raw_report = (native / "report.json").read_bytes()
    report = json.loads(raw_report)
    assert report["kind"] == report["qualificationScope"] == "windows-program-installation"
    assert report["status"] == "passed" and report["exitCode"] == 0 and report["inputDrift"] is False
    assert report["error"] is None and report["unexecuted"] == []
    assert report["platform"] == report["runtimePlatform"] == "windows/amd64"
    assert report["cliCommit"] == proof["cliCommit"] and report["cliVersion"] == proof["version"]
    assert report["sourceLockSha256"] == proof["sourceLockSha256"]
    assert sorted(row["case"] for row in report["cases"]) == sorted([
        "install-plan-read-only", "install-apply", "rollback", "recover", "user-file-preservation"])
    assert all(row["status"] == "passed" for row in report["cases"])
    assert report["githubRunId"] and report["githubRunAttempt"]
    assert len(report["commands"]) == 13
    for row in report["commands"]:
        assert row["exitCode"] == 0
        for stream in ("stdout", "stderr"):
            ref = row[stream]["path"]
            assert Path(ref).name == ref
            raw = (native / ref).read_bytes()
            assert sha(raw) == row[stream]["sha256"]
            if stream == "stdout":
                envelope = json.loads(raw)
                assert envelope["status"] == "ok" and envelope["code"] == "OK"
    old_gate = (previous / "release-gates-corrected/release-gate.json").read_bytes()
    old_native = (previous / "native-darwin-arm64/native-receipt.json").read_bytes()
    assert sha(old_gate) == proof["releaseGateSha256"]
    assert sha(old_native) == proof["artifacts"][0]["nativeReceiptSha256"]
    artifacts = [proof["artifacts"][0], next(row for row in trial["artifacts"] if row["platform"] == "windows/amd64")]
    frozen = {}
    for artifact in artifacts:
        file = original / artifact["archive"]
        assert file.name == artifact["archive"] and sha(file.read_bytes()) == artifact["sha256"]
        files, manifest = payload(file)
        assert manifest["cliCommit"] == proof["cliCommit"] and manifest["cliVersion"] == proof["version"]
        assert sha(files["docs/source-lock.json"]) == proof["sourceLockSha256"]
        for profile, identity in proof["bundles"].items():
            assert {key: manifest["bundles"][profile][key] for key in identity} == identity
        if artifact["platform"] == "windows/amd64":
            assert sha(files["yss.exe"]) == report["binarySha256"]
            assert artifact["sha256"] == report["candidateArchiveSha256"]
        frozen[artifact["platform"]] = (files, manifest)
    assert all(data == frozen["windows/amd64"][0][ref] for ref, data in frozen["darwin/arm64"][0].items()
               if ref not in ("yss", "release-manifest.json"))
    out.mkdir()
    inputs = {"schemaVersion": 1, "kind": "windows-release-requalification-input", "assemblerCommit": assembler,
              "assemblerScriptSha256": sha(Path(__file__).read_bytes()), "cliCommit": proof["cliCommit"],
              "originalChecksumsSha256": sha((original / "checksums.json").read_bytes()),
              "originalArtifacts": artifacts, "windowsNativeReceiptSha256": sha(raw_report)}
    (out / "requalification-input.json").write_bytes(encode(inputs))
    (out / "native-receipt-windows-amd64.json").write_bytes(raw_report)
    (out / "native-receipt-darwin-arm64.json").write_bytes(old_native)
    (out / "release-gate-before-windows.json").write_bytes(old_gate)
    (out / "checksums-before-windows.json").write_bytes((original / "checksums.json").read_bytes())
    with zipfile.ZipFile(out / "windows-installation-evidence.zip", "x", zipfile.ZIP_DEFLATED) as evidence:
        for file in sorted(native.iterdir()):
            assert file.is_file() and not file.is_symlink()
            evidence.write(file, file.name)
    gate = {
        "schemaVersion": 1, "kind": "platform-release-qualification-index", "qualificationScope": "platform-qualified-installation",
        "cliCommit": proof["cliCommit"], "sourceState": "committed", "status": "passed", "inputDrift": False,
        "input": {"path": "requalification-input.json", "sha256": sha(encode(inputs))},
        "platforms": {
            "darwin/arm64": {"qualificationScope": proof["qualificationScope"], "reused": True,
                "releaseGate": {"path": "release-gate-before-windows.json", "sha256": sha(old_gate)},
                "nativeReceipt": {"path": "native-receipt-darwin-arm64.json", "sha256": sha(old_native)}},
            "windows/amd64": {"qualificationScope": "windows-program-installation", "reused": False,
                "nativeReceipt": {"path": "native-receipt-windows-amd64.json", "sha256": sha(raw_report)},
                "evidence": {"path": "windows-installation-evidence.zip", "sha256": sha((out / "windows-installation-evidence.zip").read_bytes())},
                "githubRunUrl": "https://github.com/iloveZzz/yss-cli/actions/runs/" + report["githubRunId"],
                "coverage": [row["case"] if row["case"] != "recover" else "recover-noop" for row in report["cases"]],
                "unverified": ["interrupted-recovery", "legacy-project-migration", "full-template-suite", "NTFS-ACL"],
                "knownLimitation": "managed read-only file replacement/deletion remains UNPORTED"},
        },
    }
    raw_gate = encode(gate)
    (out / "release-qualification.json").write_bytes(raw_gate)
    proof = copy.deepcopy(proof)
    proof.update(qualificationScope=gate["qualificationScope"], requiredPlatforms=list(frozen),
                 releaseGateSha256=sha(raw_gate), inputManifestSha256=sha(encode(inputs)), artifacts=[])
    for platform_name, (files, original_manifest) in frozen.items():
        manifest = copy.deepcopy(original_manifest)
        receipt = sha(raw_report) if platform_name == "windows/amd64" else sha(old_native)
        manifest.update(stableReady=True, runtimeVerification="passed", sourceLockSha256=proof["sourceLockSha256"],
                        requiredPlatforms=list(frozen), supportedPlatforms=proof["supportedPlatforms"],
                        nativeReceiptSha256=receipt, releaseGateSha256=sha(raw_gate))
        output_files = {**files, "release-manifest.json": encode(manifest)}
        binary = "yss.exe" if platform_name == "windows/amd64" else "yss"
        name = next(row["archive"] for row in artifacts if row["platform"] == platform_name)
        pack(out / name, output_files, binary)
        check, observed_manifest = payload(out / name)
        assert check == output_files and observed_manifest == manifest
        assert all(check[ref] == raw for ref, raw in files.items() if ref != "release-manifest.json")
        proof["artifacts"].append({"platform": platform_name, "archive": name, "sha256": sha((out / name).read_bytes()),
            "bytes": (out / name).stat().st_size, "compiled": False, "nativeRuntimeVerified": True,
            "binarySha256": sha(files[binary]), "nativeReceiptSha256": receipt})
    (out / "checksums.json").write_bytes(encode(proof))
    print(json.dumps({"releaseGateSha256": sha(raw_gate), "artifacts": proof["artifacts"]}, indent=2))


if __name__ == "__main__":
    main()
