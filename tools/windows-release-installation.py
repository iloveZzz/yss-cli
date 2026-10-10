"""Run the pinned Windows package's public installation transaction on Windows."""
import hashlib
import json
import os
import platform
import stat
import subprocess
import sys
import time
import zipfile
from pathlib import Path, PurePosixPath


def sha(data):
    return hashlib.sha256(data).hexdigest()


def write(file, value):
    file.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def inventory(root):
    return {
        file.relative_to(root).as_posix(): {
            "sha256": sha(file.read_bytes()), "mode": stat.S_IMODE(file.stat().st_mode)
        }
        for file in sorted(root.rglob("*")) if file.is_file()
    }


def main():
    archive, out = map(lambda value: Path(value).resolve(), sys.argv[1:])
    assert sys.platform == "win32" and platform.machine().lower() in ("amd64", "x86_64")
    out.mkdir()
    bootstrap = out / "bootstrap"
    bootstrap.mkdir()
    expected = {
        "yss.exe", "release-manifest.json", "README.md", "docs/source-lock.json",
        "docs/compatibility.md", "docs/porting-status.md", "docs/native-governance.md",
        "docs/cli-retirement.md", "compat/README.md"
    }
    with zipfile.ZipFile(archive) as package:
        members = package.infolist()
        assert len(members) == len(expected) and {item.filename for item in members} == expected
        for item in members:
            ref = PurePosixPath(item.filename)
            assert not ref.is_absolute() and ".." not in ref.parts and "\\" not in item.filename
            assert not item.flag_bits & 1 and not stat.S_ISLNK(item.external_attr >> 16)
            assert item.file_size <= 35_000_000
        files = {item.filename: package.read(item) for item in members}
    manifest = json.loads(files["release-manifest.json"])
    assert manifest["platform"] == "windows/amd64" and manifest["sourceState"] == "committed"
    assert manifest["binarySha256"] == sha(files["yss.exe"])
    assert set(manifest["files"]) == expected - {"release-manifest.json"}
    for ref, data in files.items():
        if ref != "release-manifest.json":
            descriptor = manifest["files"][ref]
            assert descriptor == {"type": "file", "digest": sha(data), "mode": 0o755 if ref == "yss.exe" else 0o644}
            if ref != "yss.exe":
                assert data == Path(ref).read_bytes(), f"document differs from fixed source: {ref}"
        target = bootstrap / ref
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
    head = subprocess.check_output(["git", "rev-parse", "HEAD"], text=True).strip()
    assert head == manifest["cliCommit"]
    assert subprocess.check_output(["git", "status", "--porcelain"]) == b""
    binary = bootstrap / "yss.exe"
    tool = out / "tool"
    (tool / "user-data").mkdir(parents=True)
    for directory in ("docs", "compat"):
        (tool / directory).mkdir()
    user = tool / "user-data" / "readonly.txt"
    user.write_bytes(b"user-owned read-only bytes\x00\n")
    user.chmod(0o444)
    baseline = inventory(tool)
    report = {
        "schemaVersion": 1, "kind": "windows-program-installation", "qualificationScope": "windows-program-installation",
        "platform": "windows/amd64", "runtimePlatform": "windows/amd64", "cliCommit": head,
        "cliVersion": manifest["cliVersion"], "sourceState": "committed", "binarySha256": sha(files["yss.exe"]),
        "sourceLockSha256": sha(files["docs/source-lock.json"]), "candidateArchiveSha256": sha(archive.read_bytes()),
        "githubRunId": os.environ.get("GITHUB_RUN_ID"), "githubRunAttempt": os.environ.get("GITHUB_RUN_ATTEMPT"),
        "status": "running", "exitCode": None, "inputDrift": False,
        "unexecuted": ["install-plan-read-only", "install-apply", "rollback", "recover", "user-file-preservation"],
        "commands": [], "cases": [], "error": None,
        "modeSemantics": "Windows read-only attribute; NTFS ACL verification not executed",
    }

    def call(args, executable=binary):
        start = time.monotonic()
        result = subprocess.run([str(executable), *map(str, args), "--json"], capture_output=True, timeout=180)
        row = {"command": [str(executable), *map(str, args), "--json"], "exitCode": result.returncode,
               "durationMs": round((time.monotonic() - start) * 1000)}
        for stream in ("stdout", "stderr"):
            data = getattr(result, stream)
            log = out / f"{len(report['commands']) + 1:02}.{stream}.log"
            log.write_bytes(data)
            row[stream] = {"path": log.name, "sha256": sha(data)}
        report["commands"].append(row)
        assert result.returncode == 0, (args, result.stderr.decode("utf-8", errors="replace"))
        envelope = json.loads(result.stdout)
        assert envelope["status"] == "ok" and envelope["code"] == "OK"
        assert inventory(tool)["user-data/readonly.txt"] == baseline["user-data/readonly.txt"]
        return envelope["result"]

    def passed(case):
        report["cases"].append({"case": case, "status": "passed"})
        report["unexecuted"].remove(case)

    try:
        version = call(["version"])
        assert version["version"] == manifest["cliVersion"] and version["cliCommit"] == head
        assert version["sourceState"] == "committed"
        for profile in ("spec", "design", "backend", "frontend"):
            assert call(["bundle", "inspect", "--profile", profile]) == manifest["bundles"][profile]
        planfile = out / "plan.json"
        call(["update", "plan", "--tool-root", tool, "--artifact", archive, "--sha256", report["candidateArchiveSha256"], "--out", planfile])
        assert inventory(tool) == baseline
        passed("install-plan-read-only")
        call(["update", "apply", "--tool-root", tool, "--plan-file", planfile])
        for ref, data in files.items():
            assert (tool / ref).read_bytes() == data
        assert call(["version"], tool / "yss.exe") == version
        status = call(["update", "status", "--tool-root", tool], tool / "yss.exe")
        assert status["installationConsistent"] is True
        passed("install-apply")
        call(["update", "rollback", "--tool-root", tool])
        assert {ref: value for ref, value in inventory(tool).items() if not ref.startswith(".yss/")} == baseline
        passed("rollback")
        restored = inventory(tool)
        call(["update", "recover", "--tool-root", tool])
        call(["update", "recover", "--tool-root", tool])
        assert inventory(tool) == restored
        passed("recover")
        assert inventory(tool)["user-data/readonly.txt"] == baseline["user-data/readonly.txt"]
        passed("user-file-preservation")
        report["status"], report["exitCode"] = "passed", 0
    except Exception as error:
        report["status"], report["exitCode"], report["error"] = "failed", 1, repr(error)
        raise
    finally:
        report["inputDrift"] = binary.read_bytes() != files["yss.exe"] or sha(archive.read_bytes()) != report["candidateArchiveSha256"]
        if report["inputDrift"]:
            report["status"], report["exitCode"] = "failed", 1
        write(out / "report.json", report)
        print(json.dumps({key: report[key] for key in ("status", "platform", "exitCode", "inputDrift", "unexecuted")}))
    assert report["status"] == "passed"


if __name__ == "__main__":
    main()
