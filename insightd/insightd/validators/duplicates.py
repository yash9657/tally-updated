"""Duplicate-bill detection across a company's drafts.

The SAME supplier bill arriving twice — photographed on WhatsApp and emailed as a
PDF, or uploaded again after a reload — is two drafts of ONE payable. cored
refuses the second POST when both carry the same draft id (0077) or the same
request (0100); neither can see two DIFFERENT drafts of one bill, because to
cored they are two different requests. This is the check that can, and it runs
where both drafts are visible: in the draft store, at the moment a new draft is
stored.

THE KEY IS THE BILL'S OWN IDENTITY, AS PERSISTED: supplier (the matched ledger
id, else the scrubbed party name, case-folded), supplier invoice number (the
POST-REDACTION voucher_no — D85's fingerprinted placeholder keeps two
PII-shaped numbers apart), invoice date, and the derived invoice total.

  * All four equal → ``fail``: a duplicate of that draft.
  * Number missing on either side, the other three equal → ``fail`` too, worded
    as a POSSIBLE duplicate. A numberless bill has no stronger identity, and a
    human confirming "yes, two bills" costs one keystroke where a missed
    duplicate costs a payable.
  * Otherwise ``pass``.

It only LOWERS: a flagged draft goes to manual review with the other draft
named; nothing is merged, hidden or deleted.
"""

from __future__ import annotations


def _identity(draft) -> tuple[str, str, str, str]:
    body = draft.body
    supplier = body.party_ledger_id or (body.party or "").strip().casefold()
    number = (body.voucher_no or "").strip()
    date = (body.voucher_date or "").strip()
    total = draft.derived_gst.invoice_total if draft.derived_gst else ""
    return supplier, number, date, total


def find_duplicates(draft, others) -> list:
    """Return ``Validation`` results for ``draft`` against ``others`` (a
    company's other drafts). Drafts still ``processing`` carry no body worth
    comparing and are skipped, as is ``draft`` itself."""
    from ..contracts.draft import Validation

    supplier, number, date, total = _identity(draft)
    if not supplier or not date or not total:
        return [Validation(check="duplicate_bill", outcome="not_run", field="duplicate",
                           detail="the draft has no supplier, date or total to compare on")]
    strong: list[str] = []
    possible: list[str] = []
    for o in others:
        if o.draft_id == draft.draft_id or o.status == "processing":
            continue
        s2, n2, d2, t2 = _identity(o)
        if (s2, d2, t2) != (supplier, date, total):
            continue
        if number and n2:
            if number == n2:
                strong.append(o.draft_id)
        else:
            possible.append(o.draft_id)
    if strong:
        return [Validation(check="duplicate_bill", outcome="fail", field="duplicate",
                           detail="the same supplier, invoice number, date and total as draft(s) "
                                  + ", ".join(sorted(strong)),
                           related_draft_ids=sorted(strong))]
    if possible:
        return [Validation(check="duplicate_bill", outcome="fail", field="duplicate",
                           detail="POSSIBLE duplicate — same supplier, date and total, and no invoice number "
                                  "to tell them apart, as draft(s) " + ", ".join(sorted(possible)),
                           related_draft_ids=sorted(possible))]
    return [Validation(check="duplicate_bill", outcome="pass", field="duplicate",
                       detail="no other draft of this company has this bill's identity")]


def apply_duplicate_check(draft, others):
    """Return ``draft`` with the duplicate validation recorded and, on a hit,
    degraded to manual review with ``duplicate`` first in the focus order."""
    results = find_duplicates(draft, others)
    validations = [v for v in draft.validations if v.check != "duplicate_bill"] + results
    update = {"validations": validations}
    if any(v.outcome == "fail" for v in results):
        unresolved = ["duplicate"] + [u for u in draft.unresolved if u != "duplicate"]
        update["unresolved"] = unresolved
        if draft.status == "ready":
            update["status"] = "low_confidence_manual"
    return draft.model_copy(update=update)
