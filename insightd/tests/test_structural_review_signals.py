"""Review is decided by ARITHMETIC, not by OCR confidence.

THE FINDING THIS ENCODES. The WS10 feasibility spike measured real PaddleOCR
against real images and its most consequential result was negative: the
recogniser is CONFIDENTLY WRONG, and it corrupts MONEY values specifically.
`mean_confidence` was useless as a review signal — and so was PER-LINE
confidence. A corrupted `300.00`, read as `300:00`, came back at **0.985**
(docs/spikes/ws10-paddleocr-spike.md §8.4); on an unseen bill four numeric
tokens were mangled while the recogniser stayed sure of itself.

So a draft must not be waved through because confidence was high. What catches a
mangled money value is arithmetic that stops working once a digit or separator
is corrupted, and these tests use the SPIKE'S OWN observed corruptions —
`45:50`, `1-000`, `300:00` — as the inputs.

Confidence is still allowed to ESCALATE to manual: that direction is
conservative and costs nothing. It is simply never trusted in the other
direction, which is the direction that puts a wrong number in a ledger.
"""

from __future__ import annotations

from decimal import Decimal

import pytest

from insightd.adapters import mapper as mappermod
from insightd.adapters import tax as taxmod
from insightd.contracts.pipeline import (
    ClassifiedDoc,
    InferredInvoice,
    InferredTotals,
    LedgerRef,
    LineItem,
    LedgerGuess,
    RedactionReport,
    SupplierMatch,
    TenantContext,
)


def _ctx() -> TenantContext:
    return TenantContext(
        company_id="co-1",
        ledgers=[
            LedgerRef(id="L-PUR", name="Raw Material Purchases", kind="purchase"),
            LedgerRef(id="L-SUP", name="Acme Components", kind="party", state_code="27"),
        ],
        cgst_ledger_id="L-CGST",
        sgst_ledger_id="L-SGST",
        igst_ledger_id="L-IGST",
        home_state_code="27",
    )


def _draft(line: LineItem, *, totals: InferredTotals | None = None, gstin: str | None = "GSTIN::abc"):
    inferred = InferredInvoice(
        invoice_no="INV-1",
        invoice_date="2025-05-11",
        supplier_gstin_masked=gstin,
        place_of_supply_state_code="27",
        line_items=[line],
        totals=totals or InferredTotals(),
        per_field_confidence=dict(HEADER_CONF),
    )
    cls = ClassifiedDoc(
        doc_type="purchase_invoice",
        is_purchase=True,
        supplier_match=SupplierMatch(
            ledger_id="L-SUP", name="Acme Components", match_confidence=0.99
        ),
        classify_confidence=0.99,
    )
    return mappermod.PureVoucherDraftMapper().to_draft(
        inferred,
        cls,
        _ctx(),
        redaction=RedactionReport(),
        # DELIBERATELY HIGH. Every test here passes a confidence the old logic
        # would have treated as "no review needed".
        mean_confidence=0.985,
        source="upload",
        company_id="co-1",
    )


# A line the resolver is happy with: a matched purchase ledger at high
# confidence, so `unresolved` is empty and the only thing left deciding the
# status is what this file is about.
GOOD_LINE = LineItem(
    description="Item",
    hsn="8471",
    qty="2.0000",
    rate="500.0000",
    taxable_value="1000.0000",
    gst_rate_pct="18.0000",
    ledger_guess=LedgerGuess(ledger_id="L-PUR", name="Raw Material Purchases", confidence=0.97),
)

# The header fields the mapper checks against HEADER_FIELD_FLOOR. Supplied so
# the control reaches "ready"; every corruption test then differs from the
# control in EXACTLY one money value.
HEADER_CONF = {
    "invoice_no": 0.97,
    "invoice_date": 0.97,
    "place_of_supply": 0.97,
    "place_of_supply_state_code": 0.97,
}


def test_a_clean_bill_at_high_confidence_is_READY():
    # The control. Without it every test below would pass on an implementation
    # that simply marked everything manual.
    d = _draft(GOOD_LINE)
    assert d.status == "ready", d.unresolved


@pytest.mark.parametrize(
    "field,corrupted",
    [
        # The exact corruptions the spike observed on a real bill (§8.3):
        ("taxable_value", "45:50"),  # colon for a decimal point
        ("rate", "1-000"),           # hyphen for a thousands separator
        ("taxable_value", "300:00"),  # the one that came back at 0.985
    ],
)
def test_a_corrupted_money_field_forces_review_DESPITE_high_confidence(field, corrupted):
    line = GOOD_LINE.model_copy(update={field: corrupted})
    d = _draft(line)
    assert d.status == "low_confidence_manual", (
        f"{field}={corrupted!r} was accepted as READY at confidence 0.985 — this is "
        "exactly the failure the spike measured: OCR is confidently wrong about money"
    )


def test_a_total_that_does_not_FOOT_forces_review():
    # Two independent paths to the same number. A digit corrupted anywhere in the
    # lines makes them disagree, and the LLM cannot talk its way past arithmetic.
    d = _draft(GOOD_LINE, totals=InferredTotals(invoice_total="9999.0000"))
    assert d.status == "low_confidence_manual"


def test_a_document_with_NO_GSTIN_forces_review():
    d = _draft(GOOD_LINE, gstin=None)
    assert d.status == "low_confidence_manual"


def test_the_structural_check_names_WHAT_is_wrong():
    # An operator needs the reason, not just a verdict.
    line = GOOD_LINE.model_copy(update={"taxable_value": "300:00"})
    inferred = InferredInvoice(
        invoice_no="INV-1",
        supplier_gstin_masked="GSTIN::abc",
        place_of_supply_state_code="27",
        line_items=[line],
    )
    derived = taxmod.tax_derive(
        supplier_state_code="27",
        place_of_supply_state_code="27",
        reverse_charge=False,
        lines=[taxmod.TaxLine(Decimal("0"), Decimal("18"))],
    )
    defects = mappermod._structural_defects(inferred, derived)
    assert any("taxable_value" in d and "300:00" in d for d in defects), defects


def test_confidence_may_still_ESCALATE_but_never_waves_through():
    # The asymmetry stated plainly: a LOW confidence still forces review (cheap,
    # conservative), while a HIGH one grants nothing on its own — proven by the
    # corruption tests above, which all run at 0.985.
    inferred = InferredInvoice(
        invoice_no="INV-1",
        supplier_gstin_masked="GSTIN::abc",
        place_of_supply_state_code="27",
        line_items=[GOOD_LINE],
        per_field_confidence=dict(HEADER_CONF),
    )
    cls = ClassifiedDoc(
        doc_type="purchase_invoice",
        is_purchase=True,
        supplier_match=SupplierMatch(ledger_id="L-SUP", name="Acme", match_confidence=0.99),
        classify_confidence=0.99,
    )
    low = mappermod.PureVoucherDraftMapper().to_draft(
        inferred, cls, _ctx(), redaction=RedactionReport(),
        mean_confidence=0.10, source="upload", company_id="co-1",
    )
    assert low.status == "low_confidence_manual"
