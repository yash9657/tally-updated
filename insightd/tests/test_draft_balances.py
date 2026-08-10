"""A DRAFT ALWAYS BALANCES. Debits == credits, whatever the tenant context says.

⛔⛔ THE DEFECT THIS PINS WAS DRIVEN THROUGH THE BILL INBOX'S OWN FRONT DOOR.
A taxed bill filed through the F9 Bill Inbox came back with NO input-tax posting
legs and was unbalanced by exactly the tax — debits 1000.0000 against a credit of
1180.0000 — so cored refused it after the operator had reviewed and accepted it,
and nothing on the screen said why. The cause was three lines in the mapper of the
form ``if derived.cgst > 0 and ctx.cgst_ledger_id:``: when the caller supplied no
tax-ledger ids the tax was SILENTLY DROPPED FROM THE DEBITS while the party credit
kept carrying the full invoice total.

The reachable cause of the empty ids was the screen (``BillInboxScreen`` sent
``tenant: { company_id }`` and nothing else, so the pipeline saw an empty chart of
accounts, the supplier was never matched and there were no tax ledgers to post
to). That is fixed there. This file pins the OTHER half, which is the one that
makes the class dead: an incomplete tenant context must cost you a LEDGER ID —
a question a human can answer in the F9 form — and must never cost you MONEY.

⛔ RULE 4, THE ORACLE RULE: these tests assert SIDES and LEDGER NAMES, not a
total. A reversed voucher balances to the same paisa, so "debits == credits" alone
would pass on a draft that credited the purchase account and debited the supplier.
"""

from __future__ import annotations

from decimal import Decimal

from insightd.adapters import mapper as mappermod
from insightd.contracts.pipeline import (
    ClassifiedDoc,
    InferredInvoice,
    LedgerRef,
    LineItem,
    RedactionReport,
    SupplierMatch,
    TenantContext,
)


def _inferred(pos: str = "27") -> InferredInvoice:
    return InferredInvoice(
        invoice_no="PF/2026-27/0117",
        invoice_date="2026-07-10",
        supplier_gstin_masked="<<GSTIN:ab12>>",
        place_of_supply_state_code=pos,
        line_items=[
            LineItem(
                description="MS Angle 50x50x5mm",
                hsn="7216",
                qty="2.0000",
                rate="500.0000",
                taxable_value="1000.0000",
                gst_rate_pct="18.0000",
                # A resolved purchase ledger, so the only thing the tenant
                # context decides in these tests is the TAX side.
            )
        ],
    )


def _full_tenant(supplier_state: str = "27") -> TenantContext:
    """What the Bill Inbox now sends: this company's real chart of accounts."""
    return TenantContext(
        company_id="co-1",
        ledgers=[
            LedgerRef(id="L-SUP", name="Pune Fabricators", kind="party",
                      state_code=supplier_state),
            LedgerRef(id="L-PUR", name="Raw Material Purchases", kind="purchase"),
            LedgerRef(id="L-CGST", name="Input CGST", kind="tax"),
            LedgerRef(id="L-SGST", name="Input SGST", kind="tax"),
            LedgerRef(id="L-IGST", name="Input IGST", kind="tax"),
        ],
        cgst_ledger_id="L-CGST",
        sgst_ledger_id="L-SGST",
        igst_ledger_id="L-IGST",
        home_state_code="27",
    )


def _bare_tenant() -> TenantContext:
    """⛔ EXACTLY what BillInboxScreen used to send: a company id and nothing
    else. Kept as a test input on purpose — the screen is fixed, but any caller
    can still build one of these and the draft must survive it."""
    return TenantContext(company_id="co-1")


def _draft(ctx: TenantContext, *, matched: bool, pos: str = "27"):
    cls = ClassifiedDoc(
        doc_type="purchase_invoice",
        is_purchase=True,
        supplier_match=SupplierMatch(
            ledger_id="L-SUP" if matched else None,
            name="Pune Fabricators" if matched else None,
            match_confidence=0.98 if matched else 0.4,
        ),
        classify_confidence=0.97,
    )
    return mappermod.PureVoucherDraftMapper().to_draft(
        _inferred(pos),
        cls,
        ctx,
        redaction=RedactionReport(),
        mean_confidence=0.96,
        source="upload",
        company_id="co-1",
    )


def _sides(draft) -> tuple[Decimal, Decimal]:
    d = sum((Decimal(p.amount) for p in draft.body.postings if p.dr_cr == "D"), Decimal(0))
    c = sum((Decimal(p.amount) for p in draft.body.postings if p.dr_cr == "C"), Decimal(0))
    return d, c


# --------------------------------------------------------------------------- #
# The invariant.
# --------------------------------------------------------------------------- #


def test_a_draft_with_NO_tenant_ledgers_still_balances():
    d = _draft(_bare_tenant(), matched=False)
    debits, credits = _sides(d)
    assert debits == credits == Decimal("1180.0000"), (
        f"debits {debits} vs credits {credits} — the tax was dropped from the "
        "debits while the party credit kept the whole invoice total. This is the "
        "1000.0000-vs-1180.0000 the browser drive produced."
    )


def test_the_missing_tax_leg_is_PROPOSED_not_dropped():
    d = _draft(_bare_tenant(), matched=False)
    # Supplier state unknown -> IGST (the tax AMOUNT is unaffected by the split;
    # see test_supplier_state_not_guessed.py).
    igst = [p for p in d.body.postings if p.ledger_name == "Input IGST"]
    assert len(igst) == 1
    assert igst[0].dr_cr == "D", "input tax is a DEBIT on a purchase — it is tax we may reclaim"
    assert igst[0].amount == "180.0000"
    assert igst[0].ledger_id is None, (
        "with no tenant tax ledger the id is genuinely unknown; the NAME carries "
        "the master to create, exactly as the party leg has always done"
    )
    assert "gst.igst_ledger" in d.unresolved, (
        "the operator must be told WHICH ledger is missing — 'gst' alone cannot "
        "distinguish three of them"
    )
    assert d.status == "low_confidence_manual"


def test_the_full_tenant_context_resolves_the_tax_ledger_ids():
    # THE CONTROL. Without it the assertions above would pass on a mapper that
    # never resolved a tax ledger at all.
    d = _draft(_full_tenant(), matched=True)
    debits, credits = _sides(d)
    assert debits == credits == Decimal("1180.0000")
    cgst = [p for p in d.body.postings if p.ledger_name == "Input CGST"]
    sgst = [p for p in d.body.postings if p.ledger_name == "Input SGST"]
    assert [p.ledger_id for p in cgst] == ["L-CGST"]
    assert [p.ledger_id for p in sgst] == ["L-SGST"]
    assert not [u for u in d.unresolved if u.startswith("gst.")]


# --------------------------------------------------------------------------- #
# The SPLIT — the second half of the same root cause (an INTRA bill read as IGST).
# --------------------------------------------------------------------------- #


def test_an_INTRA_state_bill_is_IGST_when_the_caller_sends_no_ledgers():
    # NOT a bug in tax.py, and this documents why. `tax_derive` only concludes
    # intra when BOTH state codes are non-None and equal; with no ledgers the
    # supplier can never be matched, so supplier_state stays None and the else
    # branch books the whole tax as IGST — for a 27 -> 27 supply.
    d = _draft(_bare_tenant(), matched=False)
    assert d.derived_gst.igst == "180.0000"
    assert d.derived_gst.cgst == "0.0000"
    assert "supplier_state" in d.unresolved, (
        "the draft must SAY the split is a default rather than a derivation"
    )


def test_the_SAME_bill_with_the_tenant_ledgers_derives_CGST_plus_SGST():
    # ⛔ THE ASSERTION THAT MAKES THE CLIENT FIX MEANINGFUL. Same document, same
    # place of supply; the only difference is that the caller sent this company's
    # chart of accounts, which is what BillInboxScreen now does.
    d = _draft(_full_tenant(supplier_state="27"), matched=True)
    assert (d.derived_gst.cgst, d.derived_gst.sgst, d.derived_gst.igst) == (
        "90.0000",
        "90.0000",
        "0.0000",
    )
    assert "supplier_state" not in d.unresolved
    names = {p.ledger_name for p in d.body.postings}
    assert {"Input CGST", "Input SGST"} <= names
    assert "Input IGST" not in names


def test_an_INTER_state_bill_with_the_tenant_ledgers_is_still_IGST():
    # The second control: the split must follow the STATES, not the presence of
    # ledgers. A supplier in 29 shipping to 27 is inter-state.
    d = _draft(_full_tenant(supplier_state="29"), matched=True)
    assert d.derived_gst.igst == "180.0000"
    assert d.derived_gst.cgst == "0.0000"
    igst = [p for p in d.body.postings if p.ledger_name == "Input IGST"]
    assert [p.ledger_id for p in igst] == ["L-IGST"]


# --------------------------------------------------------------------------- #
# RULE 4: sides and names, not a total.
# --------------------------------------------------------------------------- #


def test_the_ORIENTATION_is_a_purchase_and_not_a_reversed_sale():
    # A fully reversed voucher balances to the same paisa, so the balance
    # assertions above cannot see orientation. On a PURCHASE: the supplier is
    # CREDITED the gross, the expense/purchase head and the INPUT tax are DEBITED.
    d = _draft(_full_tenant(), matched=True)
    by_side = {p.dr_cr for p in d.body.postings if p.ledger_name == "Pune Fabricators"}
    assert by_side == {"C"}, "the supplier must be CREDITED on a purchase"
    debits = {p.ledger_name for p in d.body.postings if p.dr_cr == "D"}
    assert "MS Angle 50x50x5mm" in debits or "Raw Material Purchases" in debits
    assert {"Input CGST", "Input SGST"} <= debits
    assert not any(n.startswith("Output ") for n in debits), (
        "OUTPUT tax on a purchase would be tax this company does not owe"
    )
