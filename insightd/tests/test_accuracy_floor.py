"""CI gate: the accuracy harness on the FAKES must clear every hard floor.

No models, no keys. A manual `--real` calibration mode (paddle+anthropic) is
intentionally NOT invoked here — it does not gate merges.
"""

from __future__ import annotations

import json

from insightd.benchmark import report as report_mod
from insightd.benchmark import validate as validate_mod


def test_corpus_validates():
    assert validate_mod.main() == 0


def test_accuracy_floors_pass():
    rpt, ok = report_mod.evaluate()
    assert ok, json.dumps(rpt, indent=2)


def test_individual_floors():
    rpt, _ = report_mod.evaluate()
    assert rpt["header_accuracy"] >= 0.96
    assert rpt["line_item_micro_f1"] >= 0.92
    assert rpt["line_count_accuracy"] >= 0.95
    assert rpt["gst_reconciliation"] == 1.0
    assert rpt["pii_recall"] == 1.0
    assert rpt["degrade_recall"] == 1.0


def test_no_false_ready_on_adversarial():
    """Cardinal sin: a degraded/adversarial fixture must NEVER be 'ready'."""
    rpt, _ = report_mod.evaluate()
    cardinal = [f for f in rpt["failures"] if "CARDINAL SIN" in f]
    assert not cardinal, cardinal


def test_at_least_20_clean_and_8_adversarial():
    from insightd.benchmark.harness import load_fixtures

    fixtures = load_fixtures()
    clean = [f for f in fixtures if f["ground_truth"]["expected_status"] == "ready"]
    adv = [f for f in fixtures if f["ground_truth"]["expected_status"] == "low_confidence_manual"]
    assert len(clean) >= 20, len(clean)
    assert len(adv) >= 8, len(adv)
