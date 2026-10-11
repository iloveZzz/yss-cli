#!/usr/bin/env python3
"""Measure native Linux/macOS processes against an explicit binary baseline."""
import argparse
import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import signal
import statistics
import subprocess
import tempfile
import time


def digest(file):
    h = hashlib.sha256()
    with file.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def summarize(samples):
    elapsed = sorted(s["elapsedMs"] for s in samples)
    return {"medianMs": statistics.median(elapsed),
            "p95Ms": elapsed[math.ceil(len(elapsed) * .95) - 1],
            "medianPeakRssBytes": statistics.median(s["peakRssBytes"] for s in samples)}


def latency_limit(old):
    return max(old * 1.10, old + 5)


def rss_bytes(value, system):
    if system not in ("Linux", "Darwin") or value <= 0:
        raise ValueError("positive per-process RSS on Linux/macOS is required")
    return int(value) * (1024 if system == "Linux" else 1)


def measure(binary, args, timeout=120):
    command = [str(binary), *args]
    start = time.perf_counter_ns()
    # wait4 reports this child, unlike RUSAGE_CHILDREN's cumulative maximum.
    # A file prevents child stderr from filling a pipe while we wait for rusage.
    with tempfile.TemporaryFile() as stderr:
        with subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=stderr,
                              start_new_session=True) as process:
            timed_out = False
            while True:
                pid, status, usage = os.wait4(process.pid, os.WNOHANG)
                if pid:
                    process.returncode = os.waitstatus_to_exitcode(status)
                    break
                if (time.perf_counter_ns() - start) / 1e9 > timeout:
                    timed_out = True
                    os.killpg(process.pid, signal.SIGKILL)
                    _, status, usage = os.wait4(process.pid, 0)
                    process.returncode = os.waitstatus_to_exitcode(status)
                    break
                time.sleep(.001)
            stderr.seek(0)
            error = stderr.read().decode("utf-8", errors="replace")
    if timed_out:
        raise TimeoutError(f"measurement timed out after {timeout}s: {command}")
    elapsed = (time.perf_counter_ns() - start) / 1e6
    if process.returncode:
        raise RuntimeError(f"measurement failed: {command}: exit {process.returncode}: {error}")
    return {"command": command, "exitCode": process.returncode, "elapsedMs": elapsed,
            "peakRssBytes": rss_bytes(usage.ru_maxrss, os.uname().sysname),
            "stderr": error, "measurement": "wait4"}


def compare(old, new, name, memory_reduction=0, time_reduction=0):
    target = name in ("backend", "frontend")
    rss_limit = old["medianPeakRssBytes"] * (1 - memory_reduction if target else 1)
    median_limit = old["medianMs"] * (1 - time_reduction)
    return {"p95LimitMs": latency_limit(old["p95Ms"]),
            "latencyPassed": new["p95Ms"] <= latency_limit(old["p95Ms"]),
            "medianPeakRssLimitBytes": rss_limit,
            "memoryReduction": 1 - new["medianPeakRssBytes"] / old["medianPeakRssBytes"],
            "memoryPassed": new["medianPeakRssBytes"] <= rss_limit,
            "medianTimeLimitMs": median_limit if target and time_reduction else None,
            "medianTimePassed": not (target and time_reduction) or new["medianMs"] <= median_limit}


def source_identity(root):
    refs = ["docs/source-lock.json", "internal/bundle/assets/bundles.json.gz"]
    return {"root": str(root),
            "commit": subprocess.check_output(["git", "-C", str(root), "rev-parse", "HEAD"], text=True).strip(),
            "inputs": {ref: digest(root / ref) for ref in refs}}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path)
    parser.add_argument("--baseline-binary", type=Path)
    parser.add_argument("--out", type=Path, help="new directory outside the repository")
    parser.add_argument("--samples", type=int, default=10)
    parser.add_argument("--min-memory-reduction", type=float, default=0)
    parser.add_argument("--min-inspect-time-reduction", type=float, default=0)
    parser.add_argument("--baseline-source-root", type=Path, help="bind the baseline binary to its build source")
    parser.add_argument("--self-check", action="store_true")
    args = parser.parse_args()
    if args.self_check:
        samples = [{"elapsedMs": n, "peakRssBytes": n * 10} for n in range(1, 21)]
        assert summarize(samples) == {"medianMs": 10.5, "p95Ms": 19, "medianPeakRssBytes": 105}
        assert latency_limit(10) == 15
        assert math.isclose(latency_limit(100), 110)
        print("size-performance self-check passed")
        return
    if not args.binary or not args.out or args.samples < 10:
        parser.error("--binary, --out and at least ten samples are required")
    if any(not 0 <= value < 1 for value in (args.min_memory_reduction, args.min_inspect_time_reduction)):
        parser.error("reduction targets must be in [0,1)")
    if (args.min_memory_reduction or args.min_inspect_time_reduction or args.baseline_source_root) and not args.baseline_binary:
        parser.error("reduction targets and baseline source require --baseline-binary")
    if os.uname().sysname not in ("Linux", "Darwin"):
        parser.error("per-process RSS measurement requires Linux or macOS")
    root = Path(__file__).resolve().parents[1]
    out = args.out.resolve()
    if out == root or root in out.parents:
        parser.error("output must be outside the repository")
    out.mkdir(parents=True, exist_ok=False)
    binaries = {"candidate": args.binary.resolve()}
    if args.baseline_binary:
        binaries["baseline"] = args.baseline_binary.resolve()
    identities = {}
    for label, binary in binaries.items():
        identities[label] = {"path": str(binary), "sha256": digest(binary),
                             "bytes": binary.stat().st_size,
                             "versionEnvelope": json.loads(subprocess.check_output(
                                 [str(binary), "version", "--json"]))}
    commands = {"help": ["--help"], "version": ["version", "--json"]}
    commands.update({profile: ["bundle", "inspect", "--profile", profile, "--json"]
                     for profile in ("spec", "design", "backend", "frontend")})
    sources = {"candidate": source_identity(root)}
    if args.baseline_source_root:
        sources["baseline"] = source_identity(args.baseline_source_root.resolve())
    report = {"schemaVersion": 1, "kind": "cli-size-performance",
              "createdAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "platform": os.uname().sysname.lower() + "/" + os.uname().machine,
              "pythonVersion": os.sys.version, "measurement": "wait4",
              "sources": sources,
              "buildInfo": {label: subprocess.check_output(["go", "version", "-m", str(binary)], text=True)
                            for label, binary in binaries.items()},
              "samplesPerCommand": args.samples, "warmupsPerBinaryAndCommand": 1,
              "sampleOrder": "paired, alternating first binary",
              "rssUnit": "bytes", "p95Method": "nearest-rank",
              "inputs": identities, "results": {}, "status": "failed",
              "input_drift": False, "unexecuted": [] if args.baseline_binary else ["baseline-comparison"]}
    passed = 0 < identities["candidate"]["bytes"] <= 35_000_000
    report["binarySizePassed"] = passed
    try:
        for name, command in commands.items():
            rows = {label: [] for label in binaries}
            for binary in binaries.values():
                subprocess.run([str(binary), *command], stdout=subprocess.DEVNULL,
                               stderr=subprocess.PIPE, check=True, timeout=120)
            for sample in range(args.samples):
                order = list(binaries)
                if sample % 2:
                    order.reverse()
                for label in order:
                    rows[label].append(measure(binaries[label], command))
            result = {label: {**summarize(samples), "samples": samples}
                      for label, samples in rows.items()}
            if "baseline" in result:
                old, new = result["baseline"], result["candidate"]
                checks = compare(old, new, name, args.min_memory_reduction, args.min_inspect_time_reduction)
                passed = passed and all(checks[key] for key in ("latencyPassed", "memoryPassed", "medianTimePassed"))
                result["checks"] = checks
            report["results"][name] = result
            print(f"{name}: {result['candidate']['medianMs']:.1f}ms, "
                  f"RSS {result['candidate']['medianPeakRssBytes'] / 1e6:.1f}MB", flush=True)
    finally:
        report["input_drift"] = any(digest(binary) != identities[label]["sha256"]
                                    for label, binary in binaries.items())
        report["input_drift"] |= any(source_identity(Path(source["root"])) != source for source in sources.values())
        complete = len(report["results"]) == len(commands)
        if complete and passed and not report["input_drift"]:
            report["status"] = "passed" if args.baseline_binary else "measured"
        (out / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    if report["status"] == "failed":
        raise SystemExit(1)


if __name__ == "__main__":
    main()
