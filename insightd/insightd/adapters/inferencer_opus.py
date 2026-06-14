"""Real Inferencer — Claude Opus 4.8. Lazy `import anthropic` only.

Sends the redacted text + the cored GST ruleset version + the tenant's masked
chart-of-accounts for ledger/HSN mapping. Calls ``boundary.assert_clean`` FIRST.
Uses adaptive thinking + high effort. Reads ``response.parsed_output``.

GST AMOUNTS ARE NOT TRUSTED from the LLM — the mapper RE-derives them against
cored's ruleset. The model supplies taxable_value + rate + place_of_supply only.

A vision block (the redacted/rendered page) is allowed ONLY when raw-PII bboxes
are pixel-masked first; the default path here is redacted-text-only.

Not exercised by tests (no key). Lazy-imported by string via the config factory.
"""

from __future__ import annotations

from ..config import INFERENCER_MODEL, RULESET_VERSION
from ..contracts.pipeline import (
    ClassifiedDoc,
    InferredInvoice,
    RedactedOCR,
    TenantContext,
)
from ..redaction.boundary import Boundary


class OpusInferencer:
    def __init__(self, boundary: Boundary, model: str = INFERENCER_MODEL) -> None:
        self.boundary = boundary
        self.model = model
        self._client = None

    def _ensure_client(self):
        if self._client is None:
            import anthropic  # lazy

            self._client = anthropic.Anthropic()
        return self._client

    def infer(
        self,
        redacted_ocr: RedactedOCR,
        cls: ClassifiedDoc,
        ctx: TenantContext,
    ) -> InferredInvoice:
        chart = [
            {"ledger_id": led.id, "name": led.name, "kind": led.kind}
            for led in ctx.ledgers
        ]
        system = (
            "You extract a structured purchase invoice from REDACTED Indian-invoice "
            "OCR text (PAN/GSTIN/Aadhaar/IFSC are <<TYPE:hash>> tags). Extract line "
            "items, HSN, taxable values, GST rate %, place of supply, reverse "
            f"charge. The cored GST ruleset is '{RULESET_VERSION}'. DO NOT compute "
            "CGST/SGST/IGST amounts — they are re-derived downstream; supply "
            "taxable_value, gst_rate_pct and place_of_supply only. Map each line to "
            "a purchase/expense ledger from the tenant chart of accounts. Never emit "
            "a raw PAN/GSTIN/Aadhaar/IFSC."
        )
        messages = [
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": f"OCR (redacted):\n{redacted_ocr.full_text}"},
                    {"type": "text", "text": f"Tenant chart of accounts (masked): {chart}"},
                    {"type": "text", "text": f"Classifier supplier match: {cls.supplier_match.model_dump()}"},
                ],
            }
        ]

        # FAIL-CLOSED cross-border guard — re-scan the entire outgoing payload
        # (system prompt + every message content block, incl. any image block).
        self.boundary.assert_clean([system, messages])

        client = self._ensure_client()
        response = client.messages.parse(
            model=self.model,
            max_tokens=8000,
            thinking={"type": "adaptive"},
            output_config={"effort": "high"},
            system=system,
            messages=messages,
            output_format=InferredInvoice,
        )
        return response.parsed_output
