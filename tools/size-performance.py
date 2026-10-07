#!/usr/bin/env python3
"""Measure native macOS CLI processes; optionally enforce a frozen baseline."""
import argparse
import datetime
import hashlib
import json
import math
import os
from pathlib import Path
import re
import statistics
import subprocess
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


def measure(binary, args):
    command = ["/usr/bin/time", "-l", str(binary), *args]
    start = time.perf_counter_ns()
    run = subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
                         text=True, timeout=120)
    elapsed = (time.perf_counter_ns() - start) / 1e6
    match = re.search(r"(\d+)\s+maximum resident set size", run.stderr)
    if run.returncode or not match:
        raise RuntimeError(f"measurement failed: {command}: exit {run.returncode}: {run.stderr}")
    return {"command": command, "exitCode": run.returncode, "elapsedMs": elapsed,
            "peakRssBytes": int(match[1]), "timeOutput": run.stderr}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path)
    parser.add_argument("--baseline-binary", type=Path)
    parser.add_argument("--out", type=Path, help="new directory outside the repository")
    parser.add_argument("--samples", type=int, default=10)
    parser.add_argument("--min-memory-reduction", type=float, default=0)
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
    if not 0 <= args.min_memory_reduction < 1 or (args.min_memory_reduction and not args.baseline_binary):
        parser.error("memory reduction must be in [0,1) and needs a baseline")
    if os.uname().sysname != "Darwin":
        parser.error("RSS byte measurement currently requires macOS /usr/bin/time -l")
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
    report = {"schemaVersion": 1, "kind": "cli-size-performance",
              "createdAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "platform": "darwin/" + os.uname().machine,
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
                threshold = latency_limit(old["p95Ms"])
                checks = {"p95LimitMs": threshold, "latencyPassed": new["p95Ms"] <= threshold}
                if name in ("backend", "frontend"):
                    rss_limit = old["medianPeakRssBytes"] * (1 - args.min_memory_reduction)
                    checks.update({"medianPeakRssLimitBytes": rss_limit,
                                   "memoryReduction": 1 - new["medianPeakRssBytes"] / old["medianPeakRssBytes"],
                                   "memoryPassed": new["medianPeakRssBytes"] <= rss_limit})
                passed = passed and checks["latencyPassed"] and checks.get("memoryPassed", True)
                result["checks"] = checks
            report["results"][name] = result
            print(f"{name}: {result['candidate']['medianMs']:.1f}ms, "
                  f"RSS {result['candidate']['medianPeakRssBytes'] / 1e6:.1f}MB", flush=True)
    finally:
        report["input_drift"] = any(digest(binary) != identities[label]["sha256"]
                                    for label, binary in binaries.items())
        complete = len(report["results"]) == len(commands)
        if complete and passed and not report["input_drift"]:
            report["status"] = "passed" if args.baseline_binary else "measured"
        (out / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    if report["status"] == "failed":
        raise SystemExit(1)


if __name__ == "__main__":
    main()
