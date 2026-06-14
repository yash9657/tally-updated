"""The fakes are fully deterministic: same fixture -> same draft (modulo the
random draft_id/created_at), zero network/model/key."""

from __future__ import annotations

from insightd.adapters.fakes import FakeOCRExtractor
from insightd.benchmark.harness import build_fake_pipeline
from insightd.contracts.pipeline import TenantContext

from conftest import load_fixture, normalized_from_fixture


def _run(fid, upload):
    fx = load_fixture(fid)
    ctx = TenantContext.model_validate(fx["input"]["tenant"])
    pipe = build_fake_pipeline()
    return pipe.run(upload, ctx, normalized=normalized_from_fixture(fx, ctx))


def _stable(draft):
    d = draft.model_dump()
    d.pop("draft_id", None)
    d.pop("created_at", None)
    return d


def test_same_fixture_same_draft(upload):
    a = _run("intra-state-18-1line-clean", upload)
    b = _run("intra-state-18-1line-clean", upload)
    assert _stable(a) == _stable(b)


def test_clean_fixture_is_ready(upload):
    d = _run("intra-state-18-1line-clean", upload)
    assert d.status == "ready"
    assert d.body.party_ledger_id == "L-PARTY-1"
    # GST re-derived (display only): 18% intra -> 9+9
    assert d.derived_gst.cgst == "90.0000"
    assert d.derived_gst.sgst == "90.0000"
    assert d.derived_gst.igst == "0.0000"


def test_interstate_uses_igst(upload):
    d = _run("inter-state-18-1line-clean", upload)
    assert d.derived_gst.igst != "0.0000"
    assert d.derived_gst.cgst == "0.0000"


def test_reverse_charge_invoice_total_excludes_tax(upload):
    d = _run("reverse-charge-18-clean", upload)
    # taxable 1000 @18 RCM -> invoice_total == taxable (supplier doesn't collect)
    assert d.derived_gst.invoice_total == "1000.0000"
    assert d.body.gst.reverse_charge is True


def test_ocr_quality_knob_degrades_confidence():
    fx = load_fixture("adv-degraded-scan")
    ctx = TenantContext.model_validate(fx["input"]["tenant"])
    doc = normalized_from_fixture(fx, ctx)
    ocr = FakeOCRExtractor().extract(doc)
    assert ocr.mean_confidence <= 0.55


def test_postings_balance(upload):
    """Double-entry sanity: total debits == total credits in the draft body."""
    from decimal import Decimal

    d = _run("intra-state-18-1line-clean", upload)
    deb = sum(Decimal(p.amount) for p in d.body.postings if p.dr_cr == "D")
    cred = sum(Decimal(p.amount) for p in d.body.postings if p.dr_cr == "C")
    assert deb == cred, (deb, cred)
