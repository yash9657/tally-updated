"""PII MUST NOT REACH THE BOOKS — the boundary on the path that PERSISTS.

⛔⛔ THE DEFECT THIS PINS WAS DRIVEN IN A BROWSER, NOT IMAGINED.
A PAN placed in the OCR-inferred invoice number was written verbatim into
``voucher_version.voucher_no``, and a raw Aadhaar sitting in a line description
became a PERMANENT LEDGER MASTER NAME — while the F9 draft panel told the
operator "PII redacted: 4 (PAN 1, IFSC 1, GSTIN 1, AADHAAR 1)". Both statements
were true of the text handed to the model and false of the values handed to the
ledger, which is append-only: a posted voucher cannot be un-posted and a master
cannot be un-created.

⛔ AND THE CLASS IS NOT "TWO FIELDS". It is "a value the pipeline redacted for
DISPLAY is un-redacted on the path that PERSISTS", so this file asserts the
PROPERTY over the whole assembled body and adds a STRUCTURAL gate:
``test_every_draft_body_field_is_classified`` walks the pydantic models and fails
if a field exists that nobody decided is free text or structured. A field added
to ``cored_map.py`` without that decision turns this suite red rather than
quietly becoming the next PII-bearing master name. (Same shape as the
authorization-peek gate — HANDOFF-PARITY-AUDIT.md §7.2 — where four rounds of
"here is the list of hostile inputs" were each bypassed by the next input.)

⛔ THE OTHER HALF OF THE PROPERTY IS THAT MONEY IS NEVER SCRUBBED. The Aadhaar
recognizer is a 12-digit run gated on Verhoeff, and a fixed-4 money string can
contain one. ``test_a_money_value_is_never_scrubbed`` is that control: without it
a "scrub everything" implementation would pass every other test in this file and
one day eat an amount.
"""

from __future__ import annotations

from insightd.adapters import mapper as mappermod
from insightd.contracts import cored_map
from insightd.redaction import persistence
from insightd.redaction.recognizers import verhoeff_checksum
from insightd.contracts.pipeline import (
    ClassifiedDoc,
    InferredInvoice,
    LedgerRef,
    LineItem,
    RedactionReport,
    SupplierMatch,
    TenantContext,
)

# A structurally valid PAN, a Verhoeff-valid Aadhaar and a structurally valid
# GSTIN. Synthetic — they are the shapes the recognizers key on, and the whole
# point is that they must never survive into a persisted value.
PAN = "ABCDE1234F"
AADHAAR = "23456789012" + str(verhoeff_checksum("23456789012"))
IFSC = "HDFC0004321"


def _ctx() -> TenantContext:
    return TenantContext(
        company_id="co-1",
        ledgers=[
            LedgerRef(id="L-SUP", name="Acme Components", kind="party", state_code="27"),
        ],
        cgst_ledger_id="L-CGST",
        sgst_ledger_id="L-SGST",
        igst_ledger_id="L-IGST",
        home_state_code="27",
    )


def _draft(*, invoice_no: str, description: str, party: str = "Acme Components",
           hsn: str | None = "8471", taxable: str = "1000.0000"):
    inferred = InferredInvoice(
        invoice_no=invoice_no,
        invoice_date="2025-05-11",
        supplier_gstin_masked="<<GSTIN:ab12>>",
        place_of_supply_state_code="27",
        line_items=[
            LineItem(
                description=description,
                hsn=hsn,
                qty="2.0000",
                rate="500.0000",
                taxable_value=taxable,
                gst_rate_pct="18.0000",
            )
        ],
    )
    cls = ClassifiedDoc(
        doc_type="purchase_invoice",
        is_purchase=True,
        supplier_match=SupplierMatch(
            ledger_id="L-SUP", name=party, match_confidence=0.98
        ),
        classify_confidence=0.97,
    )
    return mappermod.PureVoucherDraftMapper().to_draft(
        inferred,
        cls,
        _ctx(),
        # The OCR boundary found four things, exactly as the driven case reported.
        redaction=RedactionReport(
            counts_by_type={"PAN": 1, "IFSC": 1, "GSTIN": 1, "AADHAAR": 1},
            any_redacted=True,
        ),
        mean_confidence=0.96,
        source="upload",
        company_id="co-1",
    )


# --------------------------------------------------------------------------- #
# The two driven values.
# --------------------------------------------------------------------------- #


def test_a_PAN_in_the_invoice_number_never_becomes_the_voucher_number():
    d = _draft(invoice_no=f"INV/{PAN}/77", description="Steel widgets")
    assert PAN not in (d.body.voucher_no or ""), (
        "a PAN reached voucher_no — this string is copied into "
        "voucher_version.voucher_no, which is immutable once posted"
    )
    assert "PAN" in (d.body.voucher_no or "")  # the tag says WHAT was removed
    # The surrounding text survives, so the operator can still recognise the bill.
    assert d.body.voucher_no.startswith("INV/")
    assert d.body.voucher_no.endswith("/77")


def test_an_AADHAAR_in_a_line_description_never_becomes_a_LEDGER_MASTER_NAME():
    d = _draft(invoice_no="INV-1", description=f"Consultancy for {AADHAAR}")
    names = [p.ledger_name for p in d.body.postings]
    assert all(AADHAAR not in n for n in names), (
        f"a raw Aadhaar reached a ledger_name ({names}) — on the Alt+C path this "
        "string IS the master that gets created, and a master cannot be un-created"
    )
    assert any("AADHAAR REDACTED" in n for n in names)


def test_the_supplier_display_name_is_scrubbed_too():
    # Not one of the two reported call sites, and that is the point: `party` is
    # the Alt+C master name for the SUPPLIER.
    d = _draft(invoice_no="INV-1", description="Widgets", party=f"{PAN} Traders")
    assert PAN not in (d.body.party or "")
    party_legs = [p for p in d.body.postings if p.dr_cr == "C"]
    assert all(PAN not in p.ledger_name for p in party_legs)


def test_an_IFSC_in_the_hsn_is_scrubbed():
    d = _draft(invoice_no="INV-1", description="Widgets", hsn=IFSC)
    assert d.body.gst is not None
    assert all(IFSC not in (ln.hsn or "") for ln in d.body.gst.lines)


# --------------------------------------------------------------------------- #
# What the operator is TOLD, which is the half the panel got wrong.
# --------------------------------------------------------------------------- #


def test_a_redacted_persisted_field_is_named_and_forces_review():
    d = _draft(invoice_no=f"INV/{PAN}/77", description=f"Consultancy {AADHAAR}")
    assert "invoice_no" in d.unresolved, (
        "a masked voucher number must be named so the operator retypes it"
    )
    assert "line[0].ledger_name" in d.unresolved
    assert d.status == "low_confidence_manual", (
        "a draft carrying a redacted voucher number must never present itself as "
        "'Ready — press Ctrl+A to accept'"
    )


def test_the_panel_count_covers_the_values_that_PERSIST():
    # The panel renders redaction.total and counts_by_type verbatim. Before this
    # fix it said "4 (PAN 1, IFSC 1, GSTIN 1, AADHAAR 1)" — the cross-border
    # boundary's work only — while the PAN and the Aadhaar below were untouched in
    # the values that persist.
    clean = _draft(invoice_no="INV-1", description="Widgets")
    assert clean.redaction.total == 4

    dirty = _draft(invoice_no=f"INV/{PAN}/77", description=f"Consultancy {AADHAAR}")
    assert dirty.redaction.total == 6, (
        "the persistence boundary removed two more values and the panel's count "
        "must say so — an operator reading '4' would believe the books were clean"
    )
    assert dirty.redaction.counts_by_type["PAN"] == 2
    assert dirty.redaction.counts_by_type["AADHAAR"] == 2


def test_a_clean_bill_is_unchanged():
    # THE CONTROL. A path that redacts everything looks identical to a working
    # one; without this, "no PII in the books" would pass on an implementation
    # that mangles every invoice number.
    d = _draft(invoice_no="PF/2026-27/0117", description="MS Angle 50x50x5mm")
    assert d.body.voucher_no == "PF/2026-27/0117"
    assert any(p.ledger_name == "MS Angle 50x50x5mm" for p in d.body.postings)
    assert d.redaction.total == 4
    # `invoice_no` may legitimately be unresolved here for the ORDINARY reason
    # (this fixture supplies no per-field confidence, so the header floor emits
    # it). What must NOT appear is the marker only the persistence guard adds.
    assert "line[0].ledger_name" not in d.unresolved


# --------------------------------------------------------------------------- #
# ⛔ MONEY IS NEVER SCRUBBED.
# --------------------------------------------------------------------------- #


def test_a_money_value_is_never_scrubbed():
    # 180000000000.0000 contains the 12-digit run "180000000000". If that run is
    # ever Verhoeff-valid AND money is walked by the scrubber, an AMOUNT becomes
    # "[AADHAAR REDACTED]" — silently, and only for some amounts. The classifier
    # is what forbids it, so assert the classification directly and then assert an
    # amount survives a full mapping.
    assert persistence.classify_field("amount") == "structured"
    assert persistence.classify_field("taxable_value") == "structured"
    assert persistence.classify_field("voucher_date") == "structured"
    assert persistence.classify_field("ledger_id") == "structured"

    d = _draft(invoice_no="INV-1", description="Widgets", taxable="180000000000.0000")
    debits = [p.amount for p in d.body.postings if p.dr_cr == "D"]
    assert "180000000000.0000" in debits


# --------------------------------------------------------------------------- #
# ⛔ THE STRUCTURAL GATE — a new field must ACCOUNT FOR ITSELF.
# --------------------------------------------------------------------------- #


def test_every_draft_body_field_is_classified():
    """Walk the draft-body models; every field must be TEXT or STRUCTURED.

    This is the test that makes the fix a CLASS fix rather than two patches. Add
    a field to VoucherDraftBody / PostingDraft / GSTDraft / GSTLineDraft and this
    fails until somebody decides whether it is free text a human reads (scrub it)
    or money/date/uuid/code (never scrub it).
    """
    models = [
        cored_map.VoucherDraftBody,
        cored_map.PostingDraft,
        cored_map.GSTDraft,
        cored_map.GSTLineDraft,
    ]
    for model in models:
        for name in model.model_fields:
            kind = persistence.classify_field(name)  # raises if unclassified
            assert kind in ("text", "structured")


def test_the_classifier_REFUSES_an_unknown_field():
    # Non-vacuous: without this, a classify_field that returned "structured" for
    # everything would satisfy the walk above and guard nothing.
    import pytest

    with pytest.raises(persistence.UnclassifiedPersistedField):
        persistence.classify_field("supplier_email")


def test_the_body_rescan_is_FAIL_CLOSED():
    # The last line of defence: even if a future field is assembled without going
    # through the guard, the assembled body is re-scanned and refuses to ship raw
    # PII. Build a body by hand (bypassing the mapper entirely) and prove it.
    import pytest

    from insightd.redaction.persistence import PersistedPIIViolation

    body = cored_map.VoucherDraftBody(
        voucher_class="purchase",
        party="Acme",
        voucher_no=f"INV/{PAN}",  # never went through PersistGuard
        voucher_date="2025-05-11",
        postings=[
            cored_map.PostingDraft(
                ledger_name="Purchase", dr_cr="D", amount="1000.0000"
            )
        ],
    )
    with pytest.raises(PersistedPIIViolation):
        persistence.assert_body_clean(body)
