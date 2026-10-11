"""Performance receipt contracts, including real native child processes."""
import importlib.util
from pathlib import Path
import sys
import unittest

spec = importlib.util.spec_from_file_location("performance", Path(__file__).with_name("size-performance.py"))
performance = importlib.util.module_from_spec(spec)
spec.loader.exec_module(performance)


class PerformanceTest(unittest.TestCase):
    def test_native_rss_units_and_missing_measurement(self):
        self.assertEqual(performance.rss_bytes(1234, "Linux"), 1263616)
        self.assertEqual(performance.rss_bytes(1234, "Darwin"), 1234)
        with self.assertRaises(ValueError):
            performance.rss_bytes(0, "Linux")

    def test_target_median_does_not_replace_tail_latency_gate(self):
        old = {"medianMs": 100, "p95Ms": 100, "medianPeakRssBytes": 1000}
        new = {"medianMs": 70, "p95Ms": 130, "medianPeakRssBytes": 700}
        checks = performance.compare(old, new, "frontend", .2, .2)
        self.assertTrue(checks["medianTimePassed"])
        self.assertTrue(checks["memoryPassed"])
        self.assertFalse(checks["latencyPassed"])
        self.assertFalse(performance.compare(old, {**old, "medianPeakRssBytes": 1001}, "help", .2, .2)["memoryPassed"])

    def test_actual_process_measurement_failure_and_timeout(self):
        sample = performance.measure(Path(sys.executable), ["-c", "x=bytearray(2**20)"])
        self.assertEqual(sample["exitCode"], 0)
        self.assertGreater(sample["peakRssBytes"], 0)
        with self.assertRaises(RuntimeError):
            performance.measure(Path(sys.executable), ["-c", "raise SystemExit(7)"])
        with self.assertRaises(TimeoutError):
            performance.measure(Path(sys.executable), ["-c", "import time; time.sleep(10)"], timeout=.05)


if __name__ == "__main__":
    unittest.main()
