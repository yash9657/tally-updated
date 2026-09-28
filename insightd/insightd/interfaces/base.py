"""The six pipeline-stage Protocols + a StageError.

A Protocol per stage lets the pipeline accept EITHER a deterministic fake or a
real adapter with no shared base class. Depends only on contracts/ — never on
adapters or optional deps.
"""

from __future__ import annotations

from typing import Protocol, runtime_checkable

from ..contracts.draft import PurchaseVoucherDraft
from ..contracts.pipeline import (
    ClassifiedDoc,
    InferredInvoice,
    NormalizedDoc,
    OCRResult,
    RawUpload,
    RedactedOCR,
    RedactionReport,
    TenantContext,
)


class StageError(Exception):
    """Raised by a stage when it cannot produce a valid output. Carries the
    stage name so the pipeline can attribute the failure."""

    def __init__(self, stage: str, message: str) -> None:
        self.stage = stage
        super().__init__(f"[{stage}] {message}")


@runtime_checkable
class Ingest(Protocol):
    def normalize(self, upload: RawUpload) -> NormalizedDoc: ...


@runtime_checkable
class OCRExtractor(Protocol):
    def extract(self, doc: NormalizedDoc) -> OCRResult: ...


@runtime_checkable
class PIIRedactor(Protocol):
    def redact(self, ocr: OCRResult) -> tuple[RedactedOCR, RedactionReport]: ...


@runtime_checkable
class Classifier(Protocol):
    def classify(self, ocr: RedactedOCR, ctx: TenantContext) -> ClassifiedDoc: ...


@runtime_checkable
class Inferencer(Protocol):
    def infer(
        self,
        redacted_ocr: RedactedOCR,
        cls: ClassifiedDoc,
        ctx: TenantContext,
    ) -> InferredInvoice: ...


@runtime_checkable
class VoucherDraftMapper(Protocol):
    def to_draft(
        self,
        inferred: InferredInvoice,
        cls: ClassifiedDoc,
        ctx: TenantContext,
        *,
        redaction: RedactionReport,
        mean_confidence: float,
        source: str,
        company_id: str,
        ocr: RedactedOCR | None = None,
        pre_validations: list | None = None,
    ) -> PurchaseVoucherDraft: ...
