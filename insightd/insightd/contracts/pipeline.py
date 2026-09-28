"""Intermediate stage payloads that flow between pipeline stages.

All pydantic, all contracts-only (no optional deps). These are NOT the wire
contract (that's draft.py) — they are the in-process shapes the six stages hand
to each other:

    RawUpload -> [Ingest] -> NormalizedDoc -> [OCR] -> OCRResult
        -> [PIIRedactor] -> (RedactedOCR, RedactionReport)
        -> [Classifier] -> ClassifiedDoc
        -> [Inferencer] -> InferredInvoice
        -> [Mapper] -> PurchaseVoucherDraft (draft.py)
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict

from .cored_map import Money

_FORBID = ConfigDict(extra="forbid")

ContentType = Literal["image/png", "image/jpeg", "application/pdf"]
Source = Literal["upload", "whatsapp", "email"]


# --------------------------------------------------------------------------- #
# Tenant context (read-only, masked) carried alongside the document.
# --------------------------------------------------------------------------- #


class LedgerRef(BaseModel):
    """A tenant chart-of-accounts entry, masked for cross-border safety."""

    model_config = _FORBID

    id: str
    name: str
    gstin_masked: str | None = None  # masked tag, never the raw GSTIN
    state_code: str | None = None
    kind: Literal["party", "purchase", "expense", "tax", "other"] = "other"


class TenantContext(BaseModel):
    """Per-company read-only context for classification/inference/mapping."""

    model_config = _FORBID

    company_id: str
    ledgers: list[LedgerRef] = []
    cgst_ledger_id: str | None = None
    sgst_ledger_id: str | None = None
    igst_ledger_id: str | None = None
    # The buyer's own state — used to decide intra vs inter-state place of supply.
    home_state_code: str | None = None
    # W5 — the HSN/SAC codes this company has used on its own stock items and
    # vouchers. The ONLY "existence" evidence for an HSN in this build: there is
    # no HSN directory loaded (validators/hsn.py says why). Empty = unknown, and
    # every structurally valid code then reads ``unverified``, never ``fail``.
    known_hsn: list[str] = []


# --------------------------------------------------------------------------- #
# Ingest
# --------------------------------------------------------------------------- #


class RawUpload(BaseModel):
    model_config = _FORBID

    # base64 of the raw file bytes (kept as b64 so the model is JSON-safe).
    content_b64: str
    content_type: ContentType
    filename: str
    source: Source = "upload"


class PageImage(BaseModel):
    model_config = _FORBID

    png_b64: str
    width: int
    height: int
    dpi: int = 200


class NormalizedDoc(BaseModel):
    model_config = _FORBID

    pages: list[PageImage]
    page_count: int
    source_meta: dict[str, str] = {}
    source: Source = "upload"
    # The tenant context rides with the doc through the pipeline.
    tenant: TenantContext | None = None
    # Opaque fixture key for the deterministic fakes; never used by real adapters.
    fixture_id: str | None = None


# --------------------------------------------------------------------------- #
# OCR
# --------------------------------------------------------------------------- #


class OCRLine(BaseModel):
    model_config = _FORBID

    text: str
    bbox: list[float]  # [x0, y0, x1, y1]
    confidence: float  # 0..1


class OCRPage(BaseModel):
    model_config = _FORBID

    lines: list[OCRLine]


class OCRResult(BaseModel):
    model_config = _FORBID

    pages: list[OCRPage]
    full_text: str
    mean_confidence: float
    # carried through so downstream stages keep the tenant + source.
    tenant: TenantContext | None = None
    source: Source = "upload"


# RedactedOCR is structurally an OCRResult whose text has PII spans masked.
RedactedOCR = OCRResult


# --------------------------------------------------------------------------- #
# PII redaction report
# --------------------------------------------------------------------------- #


class RedactionEntity(BaseModel):
    model_config = _FORBID

    type: Literal["PAN", "GSTIN", "AADHAAR", "IFSC", "BANK_ACCOUNT"]
    page: int
    bbox: list[float] | None = None
    placeholder: str  # the masked tag inserted, e.g. "<<GSTIN:ab12>>"
    source: Literal["regex", "presidio"]


class RedactionReport(BaseModel):
    model_config = _FORBID

    entities: list[RedactionEntity] = []
    counts_by_type: dict[str, int] = {}
    any_redacted: bool = False


# --------------------------------------------------------------------------- #
# Classification
# --------------------------------------------------------------------------- #


class SupplierMatch(BaseModel):
    model_config = _FORBID

    ledger_id: str | None = None
    name: str | None = None
    gstin_masked: str | None = None
    match_confidence: float = 0.0

    # The ledgers the supplier could be, when the match was AMBIGUOUS rather
    # than absent. "Which ledger to credit" and "what state the supplier is in"
    # are separate questions: two ledgers for the same vendor's two branches in
    # one state leave the first unanswered and the second perfectly clear. Empty
    # means no candidate was identified at all — a vendor not in these books —
    # and then the state really is unknown. Ids are validated against the tenant
    # context before use, so a hallucinated id cannot invent a state.
    candidate_ledger_ids: list[str] = []


class ClassifiedDoc(BaseModel):
    model_config = _FORBID

    doc_type: Literal["purchase_invoice", "other"]
    supplier_match: SupplierMatch
    is_purchase: bool
    fields_present: dict[str, bool] = {}
    classify_confidence: float


# --------------------------------------------------------------------------- #
# Inference
# --------------------------------------------------------------------------- #


class LedgerGuess(BaseModel):
    model_config = _FORBID

    ledger_id: str | None = None
    name: str | None = None
    confidence: float = 0.0


class LineItem(BaseModel):
    model_config = _FORBID

    description: str
    hsn: str | None = None
    qty: Money | None = None
    unit: str | None = None
    rate: Money | None = None
    taxable_value: Money
    gst_rate_pct: Money
    ledger_guess: LedgerGuess = LedgerGuess()


class InferredTotals(BaseModel):
    """LLM-reported totals. NOTE: cgst/sgst/igst here are NOT trusted — the
    mapper RE-derives them against cored's ruleset. Only taxable + invoice_total
    are echoed for a sanity/foot check."""

    model_config = _FORBID

    taxable: Money | None = None
    cgst: Money | None = None
    sgst: Money | None = None
    igst: Money | None = None
    invoice_total: Money | None = None
    # W5 — the "Round Off" line the bill prints, signed (+0.37 / -0.40). The
    # totals validator allows exactly this and no tolerance; see
    # validators/totals.py for the implied-round-off rule when it is absent.
    round_off: Money | None = None


class FieldEvidence(BaseModel):
    """Where the MODEL says it read a field — carried, never trusted.

    An image-native model (the open-weight adapter's contract) can return the
    page and box it read a value from. provenance.py prefers its own
    deterministic OCR search and uses this only when that search finds nothing,
    marking the result ``source="model"`` and ungrounded.
    """

    model_config = _FORBID

    page: int
    bbox: list[float]


class InferredInvoice(BaseModel):
    model_config = _FORBID

    invoice_no: str | None = None
    invoice_date: str | None = None
    supplier_gstin_masked: str | None = None
    place_of_supply_state_code: str | None = None
    reverse_charge: bool = False
    exempt: bool = False
    line_items: list[LineItem] = []
    totals: InferredTotals = InferredTotals()
    per_field_confidence: dict[str, float] = {}
    # W5 — optional model-claimed locations, keyed by the same field paths as
    # PurchaseVoucherDraft.provenance. See FieldEvidence.
    field_evidence: dict[str, FieldEvidence] = {}
