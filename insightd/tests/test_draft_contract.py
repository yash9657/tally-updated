"""The pinned DRAFT contract — frozen shape, extra='forbid', masked-only."""

from __future__ import annotations

import pytest
from pydantic import ValidationError

from insightd.contracts.cored_map import GSTDraft, PostingDraft, VoucherDraftBody
from insightd.contracts.draft import (
    ConfidenceMap,
    FieldConfidence,
    GSTAmounts,
    PurchaseVoucherDraft,
    RedactionReportSummary,
)


def _minimal_draft() -> PurchaseVoucherDraft:
    return PurchaseVoucherDraft(
        draft_id="d1",
        company_id="c1",
        created_at="2026-06-13T00:00:00Z",
        source="upload",
        status="ready",
        body=VoucherDraftBody(
            postings=[PostingDraft(ledger_id="L1", ledger_name="X", dr_cr="D", amount="100.0000")],
            gst=GSTDraft(ruleset_version="gst-2024-09", lines=[]),
        ),
        confidence=ConfidenceMap(
            header={"invoice_no": FieldConfidence(value_present=True, confidence=0.9, source="inferencer")},
            line_items=[],
            overall_header=0.9,
            overall_line_items=0.9,
            overall=0.9,
        ),
        redaction=RedactionReportSummary(any_redacted=True, counts_by_type={"GSTIN": 1}, total=1),
        unresolved=[],
        derived_gst=GSTAmounts(cgst="0.0000", sgst="0.0000", igst="0.0000", total_tax="0.0000", invoice_total="100.0000"),
    )


def test_draft_roundtrips_via_json():
    d = _minimal_draft()
    j = d.model_dump_json()
    d2 = PurchaseVoucherDraft.model_validate_json(j)
    assert d2 == d


def test_status_is_a_literal():
    with pytest.raises(ValidationError):
        d = _minimal_draft().model_dump()
        d["status"] = "approved"
        PurchaseVoucherDraft.model_validate(d)


def test_extra_fields_forbidden():
    with pytest.raises(ValidationError):
        d = _minimal_draft().model_dump()
        d["sneaky"] = True
        PurchaseVoucherDraft.model_validate(d)


def test_voucher_class_fixed_purchase():
    body = VoucherDraftBody(postings=[], gst=None)
    assert body.voucher_class == "purchase"


def test_money_is_string():
    p = PostingDraft(ledger_id=None, ledger_name="N", dr_cr="C", amount="1234.5600")
    assert isinstance(p.amount, str)
    # ledger_id None == proposed new master (Alt+C path)
    assert p.ledger_id is None


def test_overall_is_min_of_buckets_by_convention():
    cm = ConfidenceMap(
        header={}, line_items=[],
        overall_header=0.8, overall_line_items=0.95, overall=0.8,
    )
    assert cm.overall == min(cm.overall_header, cm.overall_line_items)
