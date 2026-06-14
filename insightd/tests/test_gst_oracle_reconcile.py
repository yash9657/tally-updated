"""Cross-oracle GST reconciliation: insightd.TaxDerive == the backend tax/ golden.

The accuracy benchmark re-derives ``derived_gst`` for every draft using
``insightd.adapters.tax.tax_derive`` (the LLM-supplied GST is never trusted —
spec risk #2). For the draft to match what cored writes on Ctrl+A, insightd's
derivation must be paisa-identical to cored's TaxDerive engine.

This test pins that guarantee against the SHARED golden corpus the Go backend
already gates on: ``corpora/tax/*.json``. Each case carries the exact
cgst/sgst/igst/totalTax/invoiceTotal the Go engine must produce; we feed the
same inputs to insightd's derivation and assert equality as decimal strings.

If insightd's tax engine ever drifts from cored's ruleset, this fails — catching
the divergence in insightd CI before a draft can disagree with the ledger.
"""

from __future__ import annotations

import json
from decimal import Decimal
from pathlib import Path

import pytest

from insightd.adapters import tax as taxmod

# corpora/ sits at the repo root, two levels above the insightd package dir.
_REPO_ROOT = Path(__file__).resolve().parents[2]
_TAX_CORPUS = _REPO_ROOT / "corpora" / "tax"


def _cases():
    if not _TAX_CORPUS.is_dir():
        return []
    return sorted(_TAX_CORPUS.glob("*.json"))


def _ids(paths):
    return [p.stem for p in paths]


@pytest.mark.skipif(not _cases(), reason="corpora/tax golden corpus not present")
@pytest.mark.parametrize("case_path", _cases(), ids=_ids(_cases()))
def test_insightd_taxderive_matches_cored_golden(case_path: Path):
    case = json.loads(case_path.read_text())
    ci = case["input"]
    exp = case["expected"]

    result = taxmod.tax_derive(
        supplier_state_code=ci["supplierStateCode"],
        place_of_supply_state_code=ci["placeOfSupplyStateCode"],
        reverse_charge=bool(ci.get("reverseCharge", False)),
        lines=[
            taxmod.TaxLine(
                Decimal(str(ln["taxableValue"])),
                Decimal(str(ln["gstRatePct"])),
            )
            for ln in ci["lines"]
        ],
    )

    # Compare as decimal strings (never float) — paisa-exact, mirroring the wire.
    assert taxmod.fixed4(result.cgst) == _f4(exp["cgst"]), "cgst"
    assert taxmod.fixed4(result.sgst) == _f4(exp["sgst"]), "sgst"
    assert taxmod.fixed4(result.igst) == _f4(exp["igst"]), "igst"
    assert taxmod.fixed4(result.total_tax) == _f4(exp["totalTax"]), "totalTax"
    assert taxmod.fixed4(result.invoice_total) == _f4(exp["invoiceTotal"]), "invoiceTotal"


def test_corpus_is_actually_present():
    """Guard against a silently-empty parametrization: the golden corpus must
    exist and carry the rate cases the benchmark relies on."""
    cases = _cases()
    assert cases, "corpora/tax golden corpus is missing — cross-reconcile is vacuous"
    ids = set(_ids(cases))
    # a representative slice the benchmark leans on (intra split + inter IGST)
    for needed in ("intra-state-b2b-18", "inter-state-b2b-18"):
        assert needed in ids, f"expected golden case {needed!r} in corpora/tax"


def _f4(v) -> str:
    return f"{Decimal(str(v)).quantize(Decimal('0.0001')):f}"
