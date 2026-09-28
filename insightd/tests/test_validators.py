"""W5 validators and provenance — each check pinned in BOTH directions, and
the pipeline-level claim that a failed check sends a draft to a human.

Design memo: docs/design/d123-w5-documents-bank.md §3.
"""

from __future__ import annotations

from decimal import Decimal

import pytest

from insightd.adapters import tax as taxmod
from insightd.contracts.pipeline import InferredInvoice, InferredTotals, LineItem, OCRLine, OCRPage, OCRResult, TenantContext
from insightd.pipeline import build_pipeline
from insightd.provenance import ground, grounding_validations
from insightd.redaction import recognizers
from insightd.validators.duplicates import apply_duplicate_check, find_duplicates
from insightd.validators.gstin import check_character, gstin_checksum_valid, validate_document_gstins
from insightd.validators.hsn import hsn_structure_ok, validate_line_hsn
from insightd.validators.totals import validate_totals

from conftest import load_fixture, normalized_from_fixture


# --------------------------------------------------------------------------- #
# GSTIN
# --------------------------------------------------------------------------- #

def test_gstin_check_character_on_a_published_example_and_the_corpus():
    # 27AAPFU0939F1ZV is the example GSTIN used in GSTN's own documentation.
    assert check_character("27AAPFU0939F1Z") == "V"
    assert gstin_checksum_valid("27AAPFU0939F1ZV")
    # The corpus GSTIN, corrected in W5: structurally valid, and now arithmetically.
    assert gstin_checksum_valid("27ABCDE1234F1Z0")
    # ⛔ The spelling the corpus USED to carry passes the recogniser and fails the
    # check character — the reason the corpus had to change.
    assert recognizers.gstin_structurally_valid("27ABCDE1234F1Z5")
    assert not gstin_checksum_valid("27ABCDE1234F1Z5")


@pytest.mark.parametrize("i", range(14))
def test_every_single_character_misread_is_caught(i):
    g = "27AAPFU0939F1ZV"
    for c in "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ":
        if c == g[i]:
            continue
        bad = g[:i] + c + g[i + 1:]
        if recognizers.gstin_structurally_valid(bad):
            assert not gstin_checksum_valid(bad), bad


def test_document_validation_reports_counts_never_the_value():
    v = validate_document_gstins("Supplier GSTIN 27ABCDE1234F1Z5\nBuyer GSTIN 27AAPFU0939F1ZV")
    assert v.outcome == "fail" and v.field == "supplier"
    assert "1 of 2" in v.detail
    assert recognizers.detect_all(v.detail) == {}
    assert validate_document_gstins("GSTIN 27AAPFU0939F1ZV").outcome == "pass"
    assert validate_document_gstins("no tax id here").outcome == "not_run"


# --------------------------------------------------------------------------- #
# HSN / SAC
# --------------------------------------------------------------------------- #

@pytest.mark.parametrize("code,ok", [
    ("7326", True), ("732690", True), ("73269099", True), ("9983", True), ("998311", True),
    ("73", False), ("73269", False), ("9983111", False), ("0012", False), ("7701", False),
    ("ABCD", False), ("", False),
])
def test_hsn_structure(code, ok):
    assert hsn_structure_ok(code)[0] is ok


def test_hsn_history_passes_and_a_new_code_is_unverified_never_failed():
    assert validate_line_hsn(0, "73269099", {"7326"}).outcome == "pass"
    assert validate_line_hsn(0, "8471", {"7326"}).outcome == "unverified"
    assert validate_line_hsn(0, "7701", {"7701"}).outcome == "fail"
    assert validate_line_hsn(2, None, set()).outcome == "not_run"


# --------------------------------------------------------------------------- #
# Totals
# --------------------------------------------------------------------------- #

def _inv(total, taxable="1000.0000", round_off=None, cgst=None):
    return InferredInvoice(
        line_items=[LineItem(description="x", taxable_value="1000.0000", gst_rate_pct="18.0000")],
        totals=InferredTotals(taxable=taxable, invoice_total=total, round_off=round_off, cgst=cgst),
    )


def _derived(taxable="1000.0000", rate="18.0000", intra=True):
    return taxmod.tax_derive(
        supplier_state_code="27" if intra else "29", place_of_supply_state_code="27",
        reverse_charge=False,
        lines=[taxmod.TaxLine(taxable_value=Decimal(taxable), gst_rate_pct=Decimal(rate))],
    )


def _outcome(results, check):
    return next(v.outcome for v in results if v.check == check)


def test_totals_reconcile_exactly_and_refuse_a_misread():
    assert _outcome(validate_totals(_inv("1180.0000"), _derived()), "total_reconciles") == "pass"
    assert _outcome(validate_totals(_inv("1108.0000"), _derived()), "total_reconciles") == "fail"
    assert _outcome(validate_totals(_inv("1180.0000", taxable="1100.0000"), _derived()),
                    "lines_sum_to_taxable") == "fail"
    assert _outcome(validate_totals(_inv("1180.0000", cgst="80.0000"), _derived()), "tax_matches_rate") == "fail"
    assert _outcome(validate_totals(_inv("1180.0000", cgst="90.0000"), _derived()), "tax_matches_rate") == "pass"


def test_round_off_is_a_rule_not_a_tolerance():
    # 1003.00 @ 18% = 1183.54 derived.
    d = _derived(taxable="1003.0000")
    inv = lambda total, ro=None: InferredInvoice(  # noqa: E731
        line_items=[LineItem(description="x", taxable_value="1003.0000", gst_rate_pct="18.0000")],
        totals=InferredTotals(taxable="1003.0000", invoice_total=total, round_off=ro))
    # implied: printed whole rupee = derived rounded half-up
    assert _outcome(validate_totals(inv("1184.00"), d), "total_reconciles") == "pass"
    # ⛔ rounding cannot move 54 paise DOWN to 1183
    assert _outcome(validate_totals(inv("1183.00"), d), "total_reconciles") == "fail"
    # stated round-off must make the arithmetic hold to the paisa
    assert _outcome(validate_totals(inv("1184.00", "0.46"), d), "total_reconciles") == "pass"
    assert _outcome(validate_totals(inv("1184.00", "0.45"), d), "total_reconciles") == "fail"
    # and cannot exceed what rounding to the rupee produces
    assert _outcome(validate_totals(inv("1184.14", "0.60"), d), "total_reconciles") == "fail"


# --------------------------------------------------------------------------- #
# Provenance
# --------------------------------------------------------------------------- #

def _ocr(*lines):
    return OCRResult(
        pages=[OCRPage(lines=[OCRLine(text=t, bbox=[1, i * 10, 100, i * 10 + 8], confidence=0.9 - i / 100)
                              for i, t in enumerate(lines)])],
        full_text="\n".join(lines), mean_confidence=0.9)


def test_grounding_finds_money_as_a_number_and_dates_in_printed_forms():
    ocr = _ocr("Invoice No: INV-7", "Date: 02/04/2026", "Grand Total 1,18,000.00")
    prov = ground({"totals": ("money", "118000.0000"), "invoice_date": ("date", "2026-04-02"),
                   "invoice_no": ("text", "INV-7")}, ocr)
    assert prov["totals"].grounded and prov["totals"].page == 0 and prov["totals"].bbox == [1, 20, 100, 28]
    assert prov["invoice_date"].grounded and prov["invoice_date"].bbox == [1, 10, 100, 18]
    assert prov["invoice_no"].grounded
    assert prov["totals"].value == "118000.0000"


def test_the_ws10_misread_is_ungrounded_and_a_labelled_total_makes_it_a_fail():
    """OCR read 300:00; a model normalising it to 300.00 has no evidence."""
    ocr = _ocr("Grand Total 300:00")
    prov = ground({"totals": ("money", "300.00")}, ocr)
    assert not prov["totals"].grounded
    v = grounding_validations(prov, ocr)
    assert [x.outcome for x in v if x.field == "totals"] == ["fail"]
    # With NO total label on the document there is no evidence either way.
    ocr2 = _ocr("some line")
    assert [x.outcome for x in grounding_validations(ground({"totals": ("money", "300.00")}, ocr2), ocr2)
            if x.field == "totals"] == ["unverified"]


# --------------------------------------------------------------------------- #
# The pipeline: validators DECIDE the review
# --------------------------------------------------------------------------- #

def _run(fid, mutate=None, ctx_update=None):
    fx = load_fixture(fid)
    if mutate:
        mutate(fx)
    tenant = fx["input"]["tenant"] | (ctx_update or {})
    ctx = TenantContext(**tenant)
    from insightd.contracts.pipeline import RawUpload
    up = RawUpload(content_b64="eA==", content_type="application/pdf", filename="x.pdf")
    return build_pipeline().run(up, ctx, normalized=normalized_from_fixture(fx, ctx))


def test_a_clean_bill_stays_ready_and_carries_provenance():
    d = _run("intra-state-18-1line-clean")
    assert d.status == "ready", d.unresolved
    assert d.provenance["invoice_no"].grounded
    assert d.provenance["invoice_no"].page == 0
    assert d.provenance["line[0].taxable_value"].grounded
    assert {v.check for v in d.validations} >= {"gstin_checksum", "total_reconciles", "hsn", "grounded"}
    assert all(v.outcome != "fail" for v in d.validations)


def test_a_misread_gstin_sends_the_bill_to_a_human():
    def corrupt(fx):
        for ln in fx["input"]["ocr"]["lines"]:
            ln["text"] = ln["text"].replace("27ABCDE1234F1Z0", "27ABCDE1234F1Z5")
        if fx["input"]["ocr"].get("full_text"):
            fx["input"]["ocr"]["full_text"] = fx["input"]["ocr"]["full_text"].replace("27ABCDE1234F1Z0", "27ABCDE1234F1Z5")
    d = _run("intra-state-18-1line-clean", corrupt)
    assert d.status == "low_confidence_manual"
    assert "supplier" in d.unresolved
    assert d.confidence.header["supplier"].confidence == 0.0


def test_an_impossible_hsn_zeroes_the_line_field():
    def bad_hsn(fx):
        fx["input"]["inferred"]["line_items"][0]["hsn"] = "7701"
    d = _run("intra-state-18-1line-clean", bad_hsn)
    assert d.status == "low_confidence_manual"
    assert "line[0].hsn" in d.unresolved
    assert d.confidence.line_items[0]["hsn"].confidence == 0.0


def test_no_provenance_value_carries_pii():
    d = _run("intra-state-18-1line-clean")
    assert recognizers.detect_all([p.value for p in d.provenance.values()]) == {}
    assert recognizers.detect_all([v.detail for v in d.validations]) == {}


# --------------------------------------------------------------------------- #
# Duplicates
# --------------------------------------------------------------------------- #

def test_the_same_bill_twice_is_flagged_and_sent_to_review():
    a = _run("intra-state-18-1line-clean")
    b = _run("intra-state-18-1line-clean")
    assert b.status == "ready"
    b2 = apply_duplicate_check(b, [a])
    assert b2.status == "low_confidence_manual"
    assert b2.unresolved[0] == "duplicate"
    dup = [v for v in b2.validations if v.check == "duplicate_bill"][0]
    assert dup.outcome == "fail" and dup.related_draft_ids == [a.draft_id]
    # Itself is never its own duplicate, and a different bill is not one.
    assert find_duplicates(a, [a])[0].outcome == "pass"
    other = _run("intra-state-28-1line-clean")
    assert find_duplicates(other, [a])[0].outcome == "pass"


def test_a_numberless_bill_matching_on_everything_else_is_a_possible_duplicate():
    a = _run("intra-state-18-1line-clean")
    b = _run("intra-state-18-1line-clean")
    b = b.model_copy(update={"body": b.body.model_copy(update={"voucher_no": None})})
    v = find_duplicates(b, [a])[0]
    assert v.outcome == "fail" and "POSSIBLE" in v.detail


def test_a_stored_pre_w5_draft_still_rehydrates():
    """validations/provenance DEFAULT empty — a row stored before W5 validates."""
    d = _run("intra-state-18-1line-clean")
    raw = d.model_dump()
    raw.pop("validations"); raw.pop("provenance")
    from insightd.contracts.draft import PurchaseVoucherDraft
    back = PurchaseVoucherDraft.model_validate(raw)
    assert back.validations == [] and back.provenance == {}


def test_a_total_that_is_a_quantity_rate_hsn_or_rate_percent_is_not_grounded():
    """The wave-1 review: any number printed on the bill grounded the total."""
    ocr = _ocr("Grand Total 1,18,000.00", "Qty 5000 @ 20.00", "HSN 8471 GST 18%")
    for wrong in ("5000", "20", "8471", "18"):
        prov = ground({"totals": ("money", wrong)}, ocr)
        assert not prov["totals"].grounded, wrong
        assert prov["totals"].bbox is None, wrong
        assert [x.outcome for x in grounding_validations(prov, ocr) if x.field == "totals"] == ["fail"], wrong
    # The real total still grounds on its labelled line ...
    assert ground({"totals": ("money", "118000")}, ocr)["totals"].bbox == [1, 0, 100, 8]
    # ... including a two-line layout where the label stands alone.
    two = _ocr("Qty 5000 @ 20.00", "Grand Total", "1,18,000.00")
    p = ground({"totals": ("money", "118000")}, two)["totals"]
    assert p.grounded and p.bbox == [1, 20, 100, 28]
    assert not ground({"totals": ("money", "5000")}, two)["totals"].grounded
