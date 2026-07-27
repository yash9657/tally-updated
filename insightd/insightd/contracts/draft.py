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
DraftStatus = Literal["processing", "ready", "low_confidence_manual"]

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
