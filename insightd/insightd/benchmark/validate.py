"""Corpus validator — mirrors corpora/validate.py conventions.

Checks every fixture: parses, conforms to the documented shape, money is fixed-4
decimal STRINGS, state codes are 2-digit, planted PII is valid-format (Aadhaar
passes Verhoeff), and the ground-truth derived_gst re-derives correctly against
the embedded tax oracle (== cored TaxDerive). Stdlib + insightd core only.

Run:  python -m insightd.benchmark.validate
"""

from __future__ import annotations

import json
import sys
from decimal import Decimal, InvalidOperation
from pathlib import Path

from ..adapters import tax as taxmod
from ..redaction import recognizers

CORPUS_DIR = Path(__file__).resolve().parent / "corpus"
RULESET = "gst-2024-09"

_errors: list[str] = []


def fail(fid: str, msg: str) -> None:
    _errors.append(f"{fid}: {msg}")


def is_money4(s) -> bool:
    if not isinstance(s, str):
        return False
    try:
        Decimal(s)
    except (InvalidOperation, ValueError):
        return False
    return "." in s and len(s.split(".")[1]) == 4


def validate_fixture(fx: dict) -> None:
    fid = fx.get("id", "<no id>")
    for key in ("id", "description", "rulesetVersion", "ocr_quality", "input", "ground_truth"):
        if key not in fx:
            fail(fid, f"missing top-level key '{key}'")
    if fx.get("rulesetVersion") != RULESET:
        fail(fid, f"rulesetVersion must be {RULESET}")
    if fx.get("ocr_quality") not in ("clean", "degraded"):
        fail(fid, "ocr_quality must be 'clean' or 'degraded'")

    inp = fx.get("input", {})
    gt = fx.get("ground_truth", {})

    # The fake inferencer must read a DISTINCT input.inferred payload, never the
    # circular _from_ground_truth fallback (which would make the header/line-item
    # accuracy floor measure ground-truth echoed to itself). Forbid the fallback
    # at the corpus boundary so a future fixture can't silently go circular.
    if "inferred" not in inp:
        fail(fid, "input.inferred missing (would trigger the circular GT fallback in FakeInferencer)")

    # planted PII valid-format check
    planted = inp.get("planted_pii", {})
    for g in planted.get("GSTIN", []):
        if not recognizers.gstin_structurally_valid(g):
            fail(fid, f"planted GSTIN not structurally valid: {g}")
    for a in planted.get("AADHAAR", []):
        if not recognizers.verhoeff_valid(a):
            fail(fid, f"planted Aadhaar fails Verhoeff: {a}")
    for p in planted.get("PAN", []):
        if not recognizers.PAN_RE.fullmatch(p):
            fail(fid, f"planted PAN invalid: {p}")
    for i in planted.get("IFSC", []):
        if not recognizers.IFSC_RE.fullmatch(i):
            fail(fid, f"planted IFSC invalid: {i}")

    # money fields
    header = gt.get("header", {})
    totals = header.get("totals", {})
    for k, v in totals.items():
        if not is_money4(v):
            fail(fid, f"header.totals.{k} not fixed-4 string: {v!r}")
    pos = header.get("place_of_supply_state_code")
    if pos is not None and (not isinstance(pos, str) or len(pos) != 2 or not pos.isdigit()):
        fail(fid, f"place_of_supply_state_code not 2-digit: {pos!r}")

    for i, li in enumerate(gt.get("line_items", [])):
        for k in ("taxable_value", "gst_rate_pct"):
            if not is_money4(li.get(k)):
                fail(fid, f"line_items[{i}].{k} not fixed-4 string: {li.get(k)!r}")

    # GST reconciliation against the oracle
    lines = gt.get("line_items", [])
    if lines:
        # The fixture encodes intra vs inter purely via pos vs supplier: the
        # corpus convention is that every supplier is in MH/27 unless the place
        # of supply differs.
        #
        # EXCEPT when the supplier was never matched. Then its state is not
        # known — the ledger that would carry it was not resolved, and the
        # GSTIN that would encode it is masked before the LLM. Applying the
        # convention there would hand the oracle a fact the mapper does not
        # have, and the oracle would "independently" confirm a split nobody can
        # derive. (It did exactly that: this check passed for as long as the
        # mapper fell back to supplier_state = place_of_supply, i.e. assumed
        # intra in order to derive intra.)
        supplier_state = None if header.get("party_ledger_id") is None else "27"
        res = taxmod.tax_derive(
            supplier_state_code=supplier_state,
            place_of_supply_state_code=pos,
            reverse_charge=bool(header.get("reverse_charge", False)),
            lines=[taxmod.TaxLine(Decimal(l["taxable_value"]), Decimal(l["gst_rate_pct"])) for l in lines],
        )
        dg = gt.get("derived_gst", {})
        for comp, val in (("cgst", res.cgst), ("sgst", res.sgst), ("igst", res.igst)):
            if dg.get(comp) != taxmod.fixed4(val):
                fail(fid, f"derived_gst.{comp} {dg.get(comp)} != oracle {taxmod.fixed4(val)}")

    if gt.get("expected_status") not in ("ready", "low_confidence_manual"):
        fail(fid, "expected_status invalid")


def _is_inter(fx) -> bool:
    return fx["ground_truth"]["header"].get("place_of_supply_state_code") != "27"


def main() -> int:
    fixtures = list(CORPUS_DIR.glob("*.json"))
    if not fixtures:
        print(f"::error:: no fixtures in {CORPUS_DIR}")
        return 1
    for p in sorted(fixtures):
        try:
            fx = json.loads(p.read_text())
        except json.JSONDecodeError as e:
            fail(p.name, f"JSON parse error: {e}")
            continue
        validate_fixture(fx)

    if _errors:
        for e in _errors:
            print(f"::error:: {e}")
        print(f"\n{len(_errors)} validation error(s) across {len(fixtures)} fixtures")
        return 1
    print(f"OK: {len(fixtures)} fixtures valid")
    return 0


if __name__ == "__main__":
    sys.exit(main())
