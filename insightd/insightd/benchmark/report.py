"""Accuracy report + hard-floor gate (CI-consumable).

Floors (PLAN.md L1607):
  header_accuracy        >= 0.96
  line_item_micro_f1     >= 0.92
  line_count_accuracy    >= 0.95
  gst_reconciliation     == 1.0   (deterministic — any miss is a mapping bug)
  pii_recall             == 1.0   (planted-PII recall gates CI)
  degrade_recall         == 1.0   (zero tolerance for silent mis-post)

`evaluate()` runs the harness and returns (report, ok). A false 'ready' on an
adversarial fixture FAILS the build even if clean accuracy passes.
"""

from __future__ import annotations

import json
from pathlib import Path

from . import harness

REPORT_PATH = Path(__file__).resolve().parent / "last_report.json"

FLOORS = {
    "header_accuracy": 0.96,
    "line_item_micro_f1": 0.92,
    "line_count_accuracy": 0.95,
    "gst_reconciliation": 1.0,
    "pii_recall": 1.0,
    "degrade_recall": 1.0,
}


def evaluate(pipeline=None) -> tuple[dict, bool]:
    scores = harness.run(pipeline)
    report = scores.to_report()
    breaches = []
    for metric, floor in FLOORS.items():
        val = report.get(metric, 0.0)
        if val < floor:
            breaches.append({"metric": metric, "value": val, "floor": floor})
    report["floors"] = FLOORS
    report["breaches"] = breaches
    ok = not breaches
    report["passed"] = ok
    return report, ok


def write_report(report: dict) -> None:
    REPORT_PATH.write_text(json.dumps(report, indent=2) + "\n")


def last_report() -> dict:
    if REPORT_PATH.exists():
        return json.loads(REPORT_PATH.read_text())
    report, _ = evaluate()
    write_report(report)
    return report


def main() -> int:
    report, ok = evaluate()
    write_report(report)
    print(json.dumps(report, indent=2))
    if not ok:
        print("ACCURACY FLOOR BREACHED")
        return 1
    print("ALL FLOORS PASSED")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
