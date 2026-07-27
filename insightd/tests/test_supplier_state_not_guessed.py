"""An UNMATCHED supplier must not produce a confident intra-state split.

THE BUG THIS PINS. `to_draft` used to compute

    supplier_state = inferred.place_of_supply_state_code   # "POS == supplier on intra"

whenever the supplier ledger did not resolve. That is circular — it ASSUMES an
intra-state supply in order to derive one — so EVERY unmatched bill came back as
CGST + SGST. The number looked derived and was a coin flip: for an out-of-state
vendor it puts input tax in the wrong two ledgers and misstates GSTR-3B table 4.

There is no honest fallback available. The supplier's GSTIN — whose first two
digits ARE the state code — is masked before the LLM ever sees it (the PII
boundary, working as designed). So an unmatched supplier has an UNKNOWN state,
the derivation reaches no intra conclusion, and the draft says so.

Three corpus fixtures encoded the old answer as ground truth (`party_ledger_id:
null` beside `cgst 90 / sgst 90` — a self-contradiction), and the corpus ORACLE
in validate.py confirmed it by supplying a supplier state of "27" from a corpus
convention the mapper does not have. Both were corrected. This file is the
direct, non-corpus assertion so the invariant does not depend on either.
"""

from __future__ import annotations

from decimal import Decimal

from insightd.adapters import mapper as mappermod
from insightd.adapters import tax as taxmod
from insightd.contracts.pipeline import (
    ClassifiedDoc,
    InferredInvoice,
    LedgerRef,
    LineItem,
    RedactionReport,
    SupplierMatch,
    TenantContext,
)


def _ctx(*, with_party: bool) -> TenantContext:
    ledgers = [
        LedgerRef(id="L-PUR-RM", name="Raw Material Purchases", kind="purchase"),
    ]
    if with_party:
        # A supplier in KARNATAKA (29) while the place of supply is MAHARASHTRA
        # (27): a genuinely INTER-state purchase, which only a resolved ledger
        # can reveal.
        ledgers.append(
            LedgerRef(id="L-SUP", name="Acme Components", kind="party", state_code="29")
        )
    return TenantContext(
        company_id="co-1",
        ledgers=ledgers,
        cgst_ledger_id="L-CGST",
        sgst_ledger_id="L-SGST",
        igst_ledger_id="L-IGST",
        home_state_code="27",
    )


def _inferred() -> InferredInvoice:
    return InferredInvoice(
        invoice_no="INV-1",
        invoice_date="2025-05-11",
        supplier_gstin_masked="GSTIN::abcd1234",  # masked — carries no state
        place_of_supply_state_code="27",
        line_items=[
            LineItem(
                description="Item",
                hsn="8471",
                qty="2.0000",
                rate="500.0000",
                taxable_value="1000.0000",
                gst_rate_pct="18.0000",
            )
        ],
    )


def _draft(*, with_party: bool):
    cls = ClassifiedDoc(
        doc_type="purchase_invoice",
        is_purchase=True,
        supplier_match=SupplierMatch(
            ledger_id="L-SUP" if with_party else None,
            name="Acme Components" if with_party else None,
            match_confidence=0.95 if with_party else 0.10,
        ),
        classify_confidence=0.95,
    )
    return mappermod.PureVoucherDraftMapper().to_draft(
        _inferred(),
        cls,
        _ctx(with_party=with_party),
        redaction=RedactionReport(),
        mean_confidence=0.95,
        source="upload",
        company_id="co-1",
    )


def test_unmatched_supplier_does_not_claim_an_intra_state_split():
    d = _draft(with_party=False)
    g = d.derived_gst
    assert g.cgst == "0.0000", (
        "an unmatched supplier produced a CGST leg — the state is unknown, so an "
        "intra-state split cannot be asserted"
    )
    assert g.sgst == "0.0000"
    # The tax AMOUNT is unaffected; only the split is unasserted.
    assert g.total_tax == "180.0000"
    assert g.igst == "180.0000"


def test_unmatched_supplier_names_BOTH_party_and_supplier_as_unresolved():
    d = _draft(with_party=False)
    # Distinct claims: the party is who to credit, the supplier state is what
    # splits the tax. A human can resolve one without the other.
    assert "party" in d.unresolved
    assert "supplier" in d.unresolved, (
        "the draft must SAY the supplier state is unknown; the tax split shown "
        "is a default, not a derivation"
    )
    assert d.status == "low_confidence_manual"


def test_a_MATCHED_supplier_still_derives_from_its_ledger_state():
    # The control. Without this the test above would pass on an implementation
    # that never derived an intra-state split at all.
    d = _draft(with_party=True)
    assert "supplier" not in d.unresolved
    # Supplier 29 vs place of supply 27 -> INTER-state, from the ledger.
    assert d.derived_gst.igst == "180.0000"
    assert d.derived_gst.cgst == "0.0000"

    # And a supplier in the SAME state as the place of supply is intra — proving
    # the derivation still reaches CGST+SGST when it legitimately can.
    res = taxmod.tax_derive(
        supplier_state_code="27",
        place_of_supply_state_code="27",
        reverse_charge=False,
        lines=[taxmod.TaxLine(Decimal("1000.0000"), Decimal("18.0000"))],
    )
    assert taxmod.fixed4(res.cgst) == "90.0000"
    assert taxmod.fixed4(res.sgst) == "90.0000"


def test_the_masked_gstin_cannot_supply_the_state():
    # Why there is no fallback: the one field that encodes the supplier's state
    # is masked before it reaches inference, by design.
    inf = _inferred()
    assert inf.supplier_gstin_masked is not None
    assert not inf.supplier_gstin_masked[:2].isdigit(), (
        "if a raw GSTIN ever reaches this model, the PII boundary has been "
        "breached — and the fallback question would need revisiting"
    )
