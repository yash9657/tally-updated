"""The deterministic fakes — zero network, zero model, zero key, zero heavy dep.

Each fake is keyed off fixture content carried in the document's ``source_meta``
(populated from a benchmark corpus fixture or a test). They are fully
deterministic so the accuracy floor is reproducible in CI.

  * FakeIngest        — passes a fixture's pre-rendered page list through verbatim.
  * FakeOCRExtractor  — returns the fixture's canned OCR block; honors the
                        ``ocr_quality`` knob ('clean'|'degraded') for adversarial
                        low-confidence cases.
  * FakePIIRedactor   — delegates to the pure boundary recognizers (the floor).
  * FakeClassifier    — rule over redacted text + masked-GSTIN ledger lookup.
  * FakeInferencer    — deterministic parse of the canned OCR into line items.

The classifier + inferencer call ``boundary.assert_clean`` first, EXACTLY like
the real adapters, so the PII boundary is exercised on the fake path too.
"""

from __future__ import annotations

import json
from decimal import Decimal, InvalidOperation

from ..contracts.pipeline import (
    ClassifiedDoc,
    InferredInvoice,
    InferredTotals,
    LedgerGuess,
    LineItem,
    NormalizedDoc,
    OCRLine,
    OCRPage,
    OCRResult,
    PageImage,
    RawUpload,
    RedactedOCR,
    RedactionReport,
    SupplierMatch,
    TenantContext,
)
from ..redaction import recognizers
from ..redaction.boundary import Boundary

# A fixture's structured payload travels in source_meta["fixture_json"] as a
# JSON string so the typed NormalizedDoc stays a clean pydantic model.


def _fixture(doc_or_meta) -> dict:
    meta = (
        doc_or_meta.source_meta
        if isinstance(doc_or_meta, NormalizedDoc)
        else doc_or_meta
    )
    raw = meta.get("fixture_json") if meta else None
    return json.loads(raw) if raw else {}


# --------------------------------------------------------------------------- #
# Ingest (trivial / real — no imaging libs needed for the fake)
# --------------------------------------------------------------------------- #


class FakeIngest:
    """Passes a fixture's pre-rendered page list verbatim. Deterministic 1:1."""

    def normalize(self, upload: RawUpload) -> NormalizedDoc:
        # The test/benchmark harness builds the NormalizedDoc directly and the
        # pipeline can be entered post-ingest; but when a RawUpload is given we
        # honor a fixture embedded as JSON in the b64 content for determinism.
        try:
            payload = json.loads(upload.content_b64)
        except (ValueError, json.JSONDecodeError):
            payload = {}
        fx = payload.get("fixture", payload)
        pages = [
            PageImage(
                png_b64=p.get("png_b64", ""),
                width=p.get("width", 1),
                height=p.get("height", 1),
                dpi=p.get("dpi", 200),
            )
            for p in fx.get("input", {}).get("pages", [{"png_b64": "", "width": 1, "height": 1}])
        ]
        return NormalizedDoc(
            pages=pages,
            page_count=len(pages),
            source=upload.source,
            source_meta={"fixture_json": json.dumps(fx)} if fx else {},
        )


# --------------------------------------------------------------------------- #
# OCR
# --------------------------------------------------------------------------- #


class FakeOCRExtractor:
    """Returns the fixture's canned OCR block verbatim, honoring ocr_quality."""

    def extract(self, doc: NormalizedDoc) -> OCRResult:
        fx = _fixture(doc)
        ocr = fx.get("input", {}).get("ocr", {})
        lines_in = ocr.get("lines", [])
        lines = [
            OCRLine(
                text=ln["text"],
                bbox=ln.get("bbox", [0, 0, 0, 0]),
                confidence=float(ln.get("confidence", ocr.get("mean_confidence", 0.97))),
            )
            for ln in lines_in
        ]
        full_text = ocr.get("full_text") or "\n".join(ln.text for ln in lines)

        mean = ocr.get("mean_confidence")
        if mean is None:
            mean = (
                sum(ln.confidence for ln in lines) / len(lines) if lines else 0.0
            )
        # The ocr_quality knob deterministically pins adversarial low confidence.
        if fx.get("ocr_quality") == "degraded":
            mean = min(mean, 0.55)

        return OCRResult(
            pages=[OCRPage(lines=lines)],
            full_text=full_text,
            mean_confidence=float(mean),
            tenant=doc.tenant,
            source=doc.source,
        )


# --------------------------------------------------------------------------- #
# PII redaction (the floor — pure recognizers)
# --------------------------------------------------------------------------- #


class FakePIIRedactor:
    """Delegates to the always-on pure recognizers via a per-call Boundary."""

    def redact(self, ocr: OCRResult) -> tuple[RedactedOCR, RedactionReport]:
        return Boundary().redact_for_llm(ocr)


# --------------------------------------------------------------------------- #
# Classifier
# --------------------------------------------------------------------------- #


class FakeClassifier:
    """Deterministic rule over fixture text. Calls boundary.assert_clean first
    (exactly like the real Haiku adapter) so the PII guard is exercised."""

    def __init__(self, boundary: Boundary | None = None) -> None:
        self.boundary = boundary or Boundary()

    def classify(self, ocr: RedactedOCR, ctx: TenantContext) -> ClassifiedDoc:
        # EXACTLY like the real adapter: re-scan the outgoing payload first. This
        # is the proving point — the fake LLM only ever sees redacted text.
        self.boundary.assert_clean([ocr.full_text, [p for p in ocr.pages]])

        fx = _FIXTURE_STASH.get(id(ocr), {})
        spec = fx.get("classify", {})

        text = ocr.full_text
        upper = text.upper()
        # doc_type: from fixture override, else token heuristic.
        if "doc_type" in spec:
            is_invoice = spec["doc_type"] == "purchase_invoice"
        else:
            is_invoice = any(
                tok in upper for tok in ("TAX INVOICE", "GSTIN", "HSN", "GST")
            ) and "DELIVERY CHALLAN" not in upper and "QUOTATION" not in upper
        is_purchase = bool(spec.get("is_purchase", is_invoice))

        # Supplier match: the fixture states the resolution deterministically —
        #   {"supplier_ledger_id": "<id>"}     -> exact masked-GSTIN match
        #   {"supplier": "ambiguous"|"none"}   -> no auto-select (party=None)
        # This mirrors the real Haiku exact-masked-GSTIN lookup without depending
        # on the per-request random salt.
        match = self._match_supplier(spec, ctx, text)

        doc_type = "purchase_invoice" if is_invoice else "other"
        classify_conf = float(spec.get("confidence", 0.97 if is_invoice else 0.2))

        return ClassifiedDoc(
            doc_type=doc_type,
            supplier_match=match,
            is_purchase=is_invoice and is_purchase,
            fields_present={
                "gstin": "<<GSTIN:" in text,
                "hsn": "HSN" in upper,
            },
            classify_confidence=classify_conf,
        )

    @staticmethod
    def _match_supplier(spec: dict, ctx: TenantContext, text: str) -> SupplierMatch:
        ledger_id = spec.get("supplier_ledger_id")
        gstin_masked = FakeClassifier._first_masked_gstin(text)
        if ledger_id:
            led = next((l for l in ctx.ledgers if l.id == ledger_id), None)
            if led is not None:
                return SupplierMatch(
                    ledger_id=led.id,
                    name=led.name,
                    gstin_masked=gstin_masked,
                    match_confidence=float(spec.get("supplier_confidence", 0.98)),
                )
        # ambiguous / none / missing-gstin -> leave ledger_id None (Alt+C path).
        name = None
        for led in ctx.ledgers:
            if led.kind == "party" and led.name and led.name.upper() in text.upper():
                name = led.name
                break
        return SupplierMatch(
            ledger_id=None,
            name=name,
            gstin_masked=gstin_masked,
            match_confidence=float(spec.get("supplier_confidence", 0.4)),
        )

    @staticmethod
    def _first_masked_gstin(text: str) -> str | None:
        import re

        m = re.search(r"<<GSTIN:[0-9a-f]{4}>>", text)
        return m.group(0) if m else None


# --------------------------------------------------------------------------- #
# Inferencer
# --------------------------------------------------------------------------- #


class FakeInferencer:
    """Deterministic parse of the fixture's canned inference block. Calls
    boundary.assert_clean first, mirroring the real Opus adapter."""

    def __init__(self, boundary: Boundary | None = None) -> None:
        self.boundary = boundary or Boundary()

    def infer(
        self,
        redacted_ocr: RedactedOCR,
        cls: ClassifiedDoc,
        ctx: TenantContext,
    ) -> InferredInvoice:
        self.boundary.assert_clean([redacted_ocr.full_text])

        fx = self._fixture_from_ocr(redacted_ocr)
        inf = fx.get("input", {}).get("inferred")
        if inf is None:
            # derive from ground_truth shape if the fixture only ships GT (the
            # fake "perfectly" reads a clean scan; adversarial degrade is driven
            # by ocr_quality/missing fields, scored separately).
            inf = self._from_ground_truth(fx)

        line_items = [
            LineItem(
                description=li.get("description", ""),
                hsn=li.get("hsn"),
                qty=_f4(li.get("qty")),
                unit=li.get("unit"),
                rate=_f4(li.get("rate")),
                taxable_value=_f4(li.get("taxable_value")) or "0.0000",
                gst_rate_pct=_f4(li.get("gst_rate_pct")) or "0.0000",
                ledger_guess=LedgerGuess(
                    ledger_id=li.get("ledger_id"),
                    name=li.get("ledger_name") or li.get("description"),
                    confidence=float(li.get("ledger_confidence", 0.95 if li.get("ledger_id") else 0.4)),
                ),
            )
            for li in inf.get("line_items", [])
        ]

        totals = inf.get("totals", {})
        return InferredInvoice(
            invoice_no=inf.get("invoice_no"),
            invoice_date=inf.get("invoice_date"),
            supplier_gstin_masked=cls.supplier_match.gstin_masked,
            place_of_supply_state_code=inf.get("place_of_supply_state_code"),
            reverse_charge=bool(inf.get("reverse_charge", False)),
            exempt=bool(inf.get("exempt", False)),
            line_items=line_items,
            totals=InferredTotals(
                taxable=_f4(totals.get("taxable")),
                invoice_total=_f4(totals.get("invoice_total")),
            ),
            per_field_confidence=inf.get(
                "per_field_confidence",
                {
                    "invoice_no": 0.96 if inf.get("invoice_no") else 0.0,
                    "invoice_date": 0.96 if inf.get("invoice_date") else 0.0,
                    "place_of_supply_state_code": 0.95
                    if inf.get("place_of_supply_state_code")
                    else 0.0,
                    "reverse_charge": 0.97,
                    "totals": 0.98,
                },
            ),
        )

    @staticmethod
    def _fixture_from_ocr(ocr: RedactedOCR) -> dict:
        # The pipeline stashes the originating fixture against the redacted OCR
        # object (keyed by id) right after redaction. Fallback: empty.
        return _FIXTURE_STASH.get(id(ocr), {})

    @staticmethod
    def _from_ground_truth(fx: dict) -> dict:
        gt = fx.get("ground_truth", {})
        header = gt.get("header", {})
        return {
            "invoice_no": header.get("invoice_no"),
            "invoice_date": header.get("invoice_date"),
            "place_of_supply_state_code": header.get("place_of_supply_state_code"),
            "reverse_charge": header.get("reverse_charge", False),
            "exempt": header.get("exempt", False),
            "line_items": [
                {**li, "ledger_id": li.get("ledger_id"), "ledger_name": li.get("description")}
                for li in gt.get("line_items", [])
            ],
            "totals": {
                "taxable": header.get("totals", {}).get("taxable"),
                "invoice_total": header.get("totals", {}).get("invoice_total"),
            },
        }


# A small stash so the inferencer can recover the fixture from the redacted OCR
# object the pipeline threads through (the redacted OCR is a fresh object, so we
# key by id and populate it in the pipeline).
_FIXTURE_STASH: dict[int, dict] = {}


def stash_fixture(ocr_obj, fixture: dict) -> None:
    _FIXTURE_STASH[id(ocr_obj)] = fixture


def _f4(v) -> str | None:
    if v is None:
        return None
    try:
        return f"{Decimal(str(v)):.4f}"
    except (InvalidOperation, ValueError):
        return str(v)
