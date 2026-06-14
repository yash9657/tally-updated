"""Real Classifier — Claude Haiku 4.5. Lazy `import anthropic` only.

Sends ONLY redaction-cleaned OCR text + the tenant's masked supplier list. Calls
``boundary.assert_clean`` as the FIRST line before touching the client — the
fail-closed cross-border guard. Vision is NOT used here (text+layout only ->
cheap). Reads ``response.parsed_output``.

Not exercised by tests (no key). Lazy-imported by string via the config factory.
"""

from __future__ import annotations

from ..config import CLASSIFIER_MODEL
from ..contracts.pipeline import ClassifiedDoc, RedactedOCR, TenantContext
from ..redaction.boundary import Boundary


class HaikuClassifier:
    def __init__(self, boundary: Boundary, model: str = CLASSIFIER_MODEL) -> None:
        self.boundary = boundary
        self.model = model
        self._client = None

    def _ensure_client(self):
        if self._client is None:
            import anthropic  # lazy

            self._client = anthropic.Anthropic()
        return self._client

    def classify(self, ocr: RedactedOCR, ctx: TenantContext) -> ClassifiedDoc:
        # Masked supplier list — only masked GSTIN tags ever cross the border.
        suppliers = [
            {"ledger_id": led.id, "name": led.name, "gstin_masked": led.gstin_masked}
            for led in ctx.ledgers
            if led.kind == "party"
        ]
        system = (
            "You classify a redacted Indian invoice. The text has had all PAN / "
            "GSTIN / Aadhaar / IFSC values replaced with <<TYPE:hash>> tags. Decide "
            "doc_type and match the supplier against the provided masked-GSTIN "
            "ledger list. Never emit a raw PAN/GSTIN/Aadhaar/IFSC."
        )
        messages = [
            {
                "role": "user",
                "content": [
                    {"type": "text", "text": f"OCR (redacted):\n{ocr.full_text}"},
                    {
                        "type": "text",
                        "text": f"Tenant supplier ledgers (masked): {suppliers}",
                    },
                ],
            }
        ]

        # FAIL-CLOSED cross-border guard — re-scan the entire outgoing payload.
        self.boundary.assert_clean([system, messages])

        client = self._ensure_client()
        response = client.messages.parse(
            model=self.model,
            max_tokens=2048,
            system=system,
            messages=messages,
            output_format=ClassifiedDoc,
        )
        return response.parsed_output
