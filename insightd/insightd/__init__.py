"""insightd — the AI insight plane.

A read-only document-to-purchase-voucher DRAFT pipeline. insightd ingests an
invoice (image/PDF), runs OCR -> PII redaction -> classification -> inference,
and emits a ``PurchaseVoucherDraft`` — a ready-to-POST payload that a human
reviews and submits via the EXISTING frontend voucher API. insightd itself has
NO ledger write path and holds NO cored write credential: the human-in-the-loop
boundary is structural, not a runtime check.

Importing this package pulls in ONLY core deps (pydantic / fastapi / httpx /
pypdf). The heavy optional deps (anthropic, paddleocr, presidio) are
lazy-imported inside their real adapters and resolved by string at runtime, so
``import insightd`` works with none of them installed.
"""

__version__ = "0.1.0"
