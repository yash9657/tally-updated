"""THE PROVING TEST for the cross-border PII boundary (no optional deps).

A SpyLLM implements the Classifier + Inferencer Protocols and records every
payload passed to it. We run the full pipeline with the SpyLLM injected and
assert:
  (1) detect_all over captured payloads is EMPTY for all four entity types
  (2) the masked placeholders ARE present (redaction happened)
  (3) RedactionReport counts match the planted entities (recall=1.0)
  (4) a raw GSTIN injected DOWNSTREAM of redaction makes assert_clean raise
"""

from __future__ import annotations

import pytest

from insightd.adapters.fakes import (
    FakeIngest,
    FakeInferencer,
    FakeOCRExtractor,
    FakePIIRedactor,
)
from insightd.adapters.mapper import PureVoucherDraftMapper
from insightd.contracts.pipeline import (
    ClassifiedDoc,
    InferredInvoice,
    RedactedOCR,
    SupplierMatch,
    TenantContext,
)
from insightd.pipeline import Pipeline
from insightd.redaction import recognizers
from insightd.redaction.boundary import Boundary, PIIBoundaryViolation

from conftest import load_fixture, normalized_from_fixture


class SpyLLM:
    """Records every payload, asserting clean first (like the real adapters)."""

    def __init__(self, boundary: Boundary) -> None:
        self.boundary = boundary
        self.captured: list = []

    def classify(self, ocr: RedactedOCR, ctx: TenantContext) -> ClassifiedDoc:
        payload = [ocr.full_text, [ln.text for p in ocr.pages for ln in p.lines]]
        self.captured.append(payload)
        self.boundary.assert_clean(payload)
        return ClassifiedDoc(
            doc_type="purchase_invoice",
            supplier_match=SupplierMatch(ledger_id="L-PARTY-1", name="Acme", match_confidence=0.98),
            is_purchase=True,
            classify_confidence=0.97,
        )

    def infer(self, redacted_ocr: RedactedOCR, cls, ctx) -> InferredInvoice:
        payload = [redacted_ocr.full_text]
        self.captured.append(payload)
        self.boundary.assert_clean(payload)
        return InferredInvoice(
            invoice_no="INV-2025-0042",
            invoice_date="2025-05-11",
            place_of_supply_state_code="27",
            line_items=[],
        )


def _spy_pipeline():
    boundary = Boundary()
    spy = SpyLLM(boundary)
    pipe = Pipeline(
        ingest=FakeIngest(),
        ocr=FakeOCRExtractor(),
        pii=FakePIIRedactor(),
        classifier=spy,
        inferencer=spy,
        mapper=PureVoucherDraftMapper(),
    )
    return pipe, spy


def test_no_raw_pii_reaches_the_faked_cross_border_call(upload):
    fx = load_fixture("intra-state-18-1line-clean")
    ctx = TenantContext.model_validate(fx["input"]["tenant"])
    pipe, spy = _spy_pipeline()
    pipe.run(upload, ctx, normalized=normalized_from_fixture(fx, ctx))

    assert spy.captured, "SpyLLM should have been called"
    # (1) zero raw PII across every captured payload, all four types.
    leaked = recognizers.detect_all(spy.captured)
    assert leaked == {}, f"raw PII reached the LLM: {leaked}"


def test_masked_placeholders_are_present(upload):
    fx = load_fixture("intra-state-18-1line-clean")
    ctx = TenantContext.model_validate(fx["input"]["tenant"])
    pipe, spy = _spy_pipeline()
    pipe.run(upload, ctx, normalized=normalized_from_fixture(fx, ctx))
    joined = " ".join(t for payload in spy.captured for t in _flatten(payload))
    # redaction happened, not just stripping
    assert "<<GSTIN:" in joined
    assert "<<PAN:" in joined
    assert "<<AADHAAR:" in joined
    assert "<<IFSC:" in joined


def test_redaction_report_recall_matches_planted(upload):
    fx = load_fixture("intra-state-18-1line-clean")
    ctx = TenantContext.model_validate(fx["input"]["tenant"])
    pipe, _ = _spy_pipeline()
    draft = pipe.run(upload, ctx, normalized=normalized_from_fixture(fx, ctx))
    planted = fx["input"]["planted_pii"]
    counts = draft.redaction.counts_by_type
    for etype, vals in planted.items():
        assert counts.get(etype, 0) >= len(vals), f"recall miss for {etype}"


def test_fail_closed_guard_fires_on_downstream_raw_gstin():
    boundary = Boundary()
    # a raw GSTIN injected downstream of redaction must raise.
    with pytest.raises(PIIBoundaryViolation):
        boundary.assert_clean(["here is a leaked value 27ABCDE1234F1Z5 in the prompt"])


def test_assert_clean_passes_on_already_redacted_text():
    boundary = Boundary()
    # masked tags are NOT raw PII -> no raise
    boundary.assert_clean(["supplier <<GSTIN:ab12>> pan <<PAN:cd34>>"])


def _flatten(x):
    if isinstance(x, str):
        yield x
    elif isinstance(x, (list, tuple)):
        for i in x:
            yield from _flatten(i)
