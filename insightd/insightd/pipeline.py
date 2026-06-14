"""Pipeline orchestrator — the hard-wired stage ordering + PII chokepoint.

    Ingest -> OCR -> PIIRedactor -> (Classifier | Inferencer) -> Mapper

The PII redaction step is BEFORE any stage that can make a cross-border call.
Only the redacted payload is handed to the classifier/inferencer; raw OCR text
never leaves this function. Stage instances are injected (real or fake) by the
config factory; the default is fakes (no key, no network, no heavy dep).

Depends only on contracts/ + interfaces/ + redaction/ + adapters (the pure
core). It imports NO cored write client and constructs NO /vouchers URL.
"""

from __future__ import annotations

import json
from dataclasses import dataclass

from .adapters import fakes as _fakes
from .contracts.draft import PurchaseVoucherDraft
from .contracts.pipeline import (
    NormalizedDoc,
    RawUpload,
    TenantContext,
)
from .interfaces.base import (
    Classifier,
    Inferencer,
    Ingest,
    OCRExtractor,
    PIIRedactor,
    VoucherDraftMapper,
)
from .redaction.boundary import Boundary


@dataclass
class Pipeline:
    ingest: Ingest
    ocr: OCRExtractor
    pii: PIIRedactor
    classifier: Classifier
    inferencer: Inferencer
    mapper: VoucherDraftMapper

    def run(
        self,
        upload: RawUpload,
        ctx: TenantContext,
        *,
        normalized: NormalizedDoc | None = None,
    ) -> PurchaseVoucherDraft:
        """Run the full pipeline and return a DRAFT proposal. Never POSTs."""
        # 1. Ingest (normalize). Callers may pre-supply a NormalizedDoc (the
        #    benchmark harness does this with canned pages).
        doc = normalized if normalized is not None else self.ingest.normalize(upload)
        if doc.tenant is None:
            doc = doc.model_copy(update={"tenant": ctx})

        # 2. OCR.
        ocr = self.ocr.extract(doc)

        # 3. PII REDACTION — the cross-border boundary. From here on only the
        #    redacted payload may be handed to a stage that can make an LLM call.
        redacted, report = self.pii.redact(ocr)

        # Thread the originating fixture to the deterministic fake inferencer
        # (no-op for real adapters; the stash is keyed by the redacted object).
        fixture = _extract_fixture(doc)
        if fixture:
            _fakes.stash_fixture(redacted, fixture)

        try:
            # 4. Classify (cross-border-capable). The adapter re-asserts clean.
            cls = self.classifier.classify(redacted, ctx)

            # 5. Infer (cross-border-capable). The adapter re-asserts clean.
            inferred = self.inferencer.infer(redacted, cls, ctx)
        finally:
            _fakes._FIXTURE_STASH.pop(id(redacted), None)

        # 6. Map to the cored Purchase draft (PURE — no network, no model).
        draft = self.mapper.to_draft(
            inferred,
            cls,
            ctx,
            redaction=report,
            mean_confidence=ocr.mean_confidence,
            source=doc.source,
            company_id=ctx.company_id,
        )
        return draft


def _extract_fixture(doc: NormalizedDoc) -> dict:
    raw = doc.source_meta.get("fixture_json") if doc.source_meta else None
    if not raw:
        return {}
    try:
        return json.loads(raw)
    except (ValueError, json.JSONDecodeError):
        return {}


def build_pipeline(settings=None) -> Pipeline:
    """Construct a Pipeline from settings via the stage factory. A shared
    Boundary is injected into the LLM stages so the PII chokepoint is one
    object."""
    from .config import StageFactory, get_settings

    settings = settings or get_settings()
    factory = StageFactory(settings)
    boundary = Boundary()
    return Pipeline(
        ingest=factory.ingest(),
        ocr=factory.ocr(),
        pii=factory.pii(),
        classifier=factory.classifier(boundary),
        inferencer=factory.inferencer(boundary),
        mapper=factory.mapper(),
    )
