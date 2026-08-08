"""The pinned DRAFT contract — the human-approval boundary.

``PurchaseVoucherDraft`` is what insightd returns and then STOPS. ``body`` is a
ready-to-POST cored payload, but insightd NEVER POSTs it. The frontend hydrates
the F9 Purchase screen from ``body`` + ``confidence`` (low-confidence fields
amber, focus order from ``unresolved``); the operator presses Ctrl+A and the
EXISTING frontend voucher API submits, at which point cored re-derives GST
authoritatively and writes the immutable ledger entry attributed to the human.

The draft NEVER carries raw PAN / GSTIN / Aadhaar / IFSC — only masked tags.

Frozen, import-stable, NO optional deps (pydantic only).
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict

from .cored_map import Money, VoucherDraftBody

_FORBID = ConfigDict(extra="forbid")

# 'ready'              -> every field cleared its floor; Ctrl+A is one keystroke.
# 'low_confidence_manual' -> open the form blank-but-prefilled for full manual
#                            review rather than as an accept-ready draft.
# "processing" is the ENQUEUE-AND-POLL state (WS10 §8.6 item 5). Real OCR takes
# up to ~45 s on a phone photo, which cannot sit in an HTTP request, so an upload
# returns immediately with a draft in this state and the client polls until it
# leaves. With the deterministic fakes the pipeline finishes inside the request
# and the draft is already terminal on return — ONE response shape either way, so
# a client never has to know which mode the server is in.
#
# ⛔⛔ 'accepted' IS THE TERMINAL, ONCE-ONLY STATE, AND ITS ABSENCE WAS A MONEY
# DEFECT. Until it existed there was NO state a consumed draft could be moved to:
# a draft that had already become a cored purchase voucher stayed "ready", stayed
# in the F9 Bill Inbox, and could be re-opened and accepted again. The frontend
# grew two client-side guards for that (a page-lifetime memo, and a day-book
# lookup by supplier invoice number + date), and a BROWSER RE-DRIVE AFTER THAT
# FIX still produced TWO REAL VOUCHERS for one supplier bill: the draft whose
# invoice number the OCR never read has nothing for the day-book layer to match
# on, and a page reload empties the memo. The truth has to live somewhere a reload
# cannot reach, which means here.
#
# insightd RECORDS the acceptance; it never performs one. The voucher is written
# by the human's own client against cored (that is the whole HITL boundary), and
# this state plus ``accepted_voucher_id`` / ``accepted_voucher_no`` is insightd
# remembering WHICH voucher a draft became. That is deliberately a link and not
# merely a hidden row: an operator who cannot see what happened to a bill re-does
# it, so the inbox must be able to say "already posted as Purchase 3".
DraftStatus = Literal["processing", "ready", "low_confidence_manual", "accepted"]

FieldSource = Literal["ocr", "classifier", "inferencer", "derived", "default"]


class FieldConfidence(BaseModel):
    """Confidence for one draft field."""

    model_config = _FORBID

    value_present: bool
    confidence: float  # 0.0 .. 1.0
    source: FieldSource


class ConfidenceMap(BaseModel):
    """Per-field confidence, bucketed header vs line-items, with rolled-up
    overalls. ``overall = min(overall_header, overall_line_items)``."""

    model_config = _FORBID

    # header keys: invoice_no, invoice_date, supplier, place_of_supply,
    # reverse_charge, totals
    header: dict[str, FieldConfidence]
    # per line: description, hsn, qty, rate, taxable_value, gst_rate_pct, ledger
    line_items: list[dict[str, FieldConfidence]]
    overall_header: float
    overall_line_items: float
    overall: float


class RedactionReportSummary(BaseModel):
    """Counts only — proves PII was scrubbed; carries NO raw values."""

    model_config = _FORBID

    any_redacted: bool
    counts_by_type: dict[str, int]
    total: int


class GSTAmounts(BaseModel):
    """cgst/sgst/igst RE-derived by insightd against cored's ruleset for DISPLAY
    ONLY. The human's POST re-derives server-side authoritatively; these are
    never trusted as the source of truth."""

    model_config = _FORBID

    cgst: Money
    sgst: Money
    igst: Money
    total_tax: Money
    invoice_total: Money


class PurchaseVoucherDraft(BaseModel):
    """The proposal. Returned by insightd; never POSTed by insightd."""

    model_config = _FORBID

    draft_id: str
    company_id: str  # tenant scope — drafts are per-company like every cored write
    created_at: str
    source: Literal["upload", "whatsapp", "email"]
    status: DraftStatus
    body: VoucherDraftBody  # ready-to-POST payload
    confidence: ConfidenceMap
    redaction: RedactionReportSummary  # counts only; proves PII scrubbed
    unresolved: list[str]  # fields needing human attention (drives F9 focus order)
    derived_gst: GSTAmounts | None = None  # display-only re-derivation

    # --- the acceptance link (set together with status 'accepted') -----------
    #
    # WHICH VOUCHER THIS DRAFT BECAME. Recorded, never created, by insightd — see
    # the DraftStatus comment. All three are None for every non-accepted draft,
    # and they DEFAULT to None so a row stored before this field existed still
    # rehydrates under ``extra='forbid'`` (pg_store keeps the whole draft as jsonb
    # and validates it strictly on the way back out).
    #
    # ``accepted_voucher_no`` may legitimately be "" — cored allocates a purchase
    # voucher number inside the write transaction and a client that could not read
    # it back must still be able to record the id. It is a DISPLAY string; the id
    # is the identity.
    accepted_voucher_id: str | None = None
    accepted_voucher_no: str | None = None
    accepted_at: str | None = None  # ISO-8601 UTC, when the acceptance was recorded

    # --- the POSTING CLAIM (a short-lived lease taken BEFORE the voucher POST) --
    #
    # ⛔⛔ FIFTH DOOR, DRIVEN IN A BROWSER AFTER FOUR EARLIER FIXES. Everything
    # above records an acceptance AFTER the fact, and four layers all answered the
    # same question ("has this draft been accepted?") at the same WRONG MOMENT —
    # before the voucher was posted, when the honest answer was still "no". TWO
    # SEPARATE BROWSERS (separate localStorage, no shared session memo) were raced on
    # ONE numberless draft and BOTH posted:
    #
    #   BROWSER A: Saved / No: 11  POST …/vouchers/purchase 201  POST …/accepted 200
    #   BROWSER B: Saved / No: 12  POST …/vouchers/purchase 201  POST …/accepted 409
    #
    # The compare-and-set in the store did exactly what it was built to do — the
    # SECOND marking was refused — but by then the second voucher was already in the
    # books: 27000.0000 + 4860.0000 IGST, twice. A check-then-act guard cannot be
    # fixed by adding a fifth check; the ORDERING has to change. So a poster now
    # RESERVES the draft first (``claim_for_accept``), posts second, and confirms
    # third (``mark_accepted``). The loser of the claim is refused BEFORE it posts
    # anything at all — not even a ledger master.
    #
    # WHY A LEASE AND NOT A PERMANENT FLAG. If claiming were permanent, a browser
    # that died between the claim and the POST would leave a bill NOBODY can ever
    # post and no route to release — a worse defect than the duplicate it prevents.
    # So the claim EXPIRES (``claim_expires_at``, CLAIM_TTL_SECONDS in api/store.py),
    # is released explicitly when the POST fails, and can be renewed by the SAME
    # ``claim_token`` (a retry from the page that already holds it). A claim is NOT a
    # status: an unconfirmed claim leaves the draft ``ready`` on purpose, because the
    # bill really is still unposted and must stay postable.
    #
    # All four are None for an unclaimed draft, and they DEFAULT so a row written
    # before this field existed still rehydrates under ``extra='forbid'``.
    claim_token: str | None = None
    claimed_by: str | None = None  # display-only label ("who/what is posting it")
    claimed_at: str | None = None  # ISO-8601 UTC
    claim_expires_at: str | None = None  # ISO-8601 UTC; past == no live claim
