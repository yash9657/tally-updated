"""Tests for GET /companies/{id}/drafts — the slim DraftSummary list route.

Drives the real FastAPI app via TestClient with a per-test isolated DraftStore
(mirrors test_api.py). DraftSummary is DERIVED server-side; these assert the
exact derivations and the RLS-style empty-collection contract.
"""

from __future__ import annotations

from fastapi.testclient import TestClient

from insightd.api.app import create_app
from insightd.api.deps import get_draft_store
from insightd.api.store import DraftStore
from insightd.contracts.draft import (
    ConfidenceMap,
    PurchaseVoucherDraft,
    RedactionReportSummary,
)
from insightd.contracts.cored_map import VoucherDraftBody

from conftest import load_fixture


def _client():
    app = create_app()
    store = DraftStore()
    app.dependency_overrides[get_draft_store] = lambda: store
    return TestClient(app), store


def _post_fixture(client, fid):
    fx = load_fixture(fid)
    ctx = fx["input"]["tenant"]
    body = {"source": "upload", "tenant": ctx, "fixture": fx}
    return client.post(f"/companies/{ctx['company_id']}/drafts", json=body)


def test_list_returns_summaries_for_created_drafts():
    client, _ = _client()
    r1 = _post_fixture(client, "intra-state-18-1line-clean")
    r2 = _post_fixture(client, "inter-state-18-1line-clean")
    assert r1.status_code == 201, r1.text
    assert r2.status_code == 201, r2.text
    full1, full2 = r1.json(), r2.json()
    assert full1["company_id"] == "CO-DEMO"

    g = client.get("/companies/CO-DEMO/drafts")
    assert g.status_code == 200, g.text
    summaries = g.json()
    assert isinstance(summaries, list)
    assert len(summaries) == 2

    by_id = {s["draft_id"]: s for s in summaries}
    assert set(by_id) == {full1["draft_id"], full2["draft_id"]}

    s1 = by_id[full1["draft_id"]]
    # exact fields present and no extras (extra='forbid' on the model)
    assert set(s1) == {
        "draft_id",
        "company_id",
        "created_at",
        "source",
        "status",
        "party",
        "grand_total",
        "confidence",
        # The acceptance link — None until the draft is accepted. It is in the
        # LIST projection so the inbox can say "already posted as Purchase 3"
        # rather than dropping the row (see DraftSummary).
        "accepted_voucher_no",
        "accepted_voucher_id",
    }
    assert s1["accepted_voucher_no"] is None
    assert s1["accepted_voucher_id"] is None
    # direct projections
    assert s1["company_id"] == full1["company_id"]
    assert s1["created_at"] == full1["created_at"]
    assert s1["source"] == full1["source"]
    assert s1["status"] == full1["status"]
    # DERIVED: party <- body.party or ''
    assert s1["party"] == (full1["body"].get("party") or "")
    # DERIVED: confidence <- confidence.overall (float)
    assert isinstance(s1["confidence"], float)
    assert s1["confidence"] == full1["confidence"]["overall"]
    # grand_total stays a fixed-4 Money STRING
    assert isinstance(s1["grand_total"], str)


def test_grand_total_derives_from_derived_gst_when_present():
    client, _ = _client()
    r = _post_fixture(client, "intra-state-18-1line-clean")
    full = r.json()
    assert full["derived_gst"] is not None, "expected a clean draft to derive GST"
    g = client.get("/companies/CO-DEMO/drafts")
    s = g.json()[0]
    # grand_total mirrors derived_gst.invoice_total verbatim (Money string)
    assert s["grand_total"] == full["derived_gst"]["invoice_total"]


def test_grand_total_falls_back_to_zero_string_when_no_derived_gst():
    client, _ = _client()
    # a degraded/adversarial scan routes to low_confidence_manual and may carry
    # no derived_gst; the summary must then surface '0.0000' (a STRING), not crash.
    r = _post_fixture(client, "adv-degraded-scan")
    full = r.json()
    g = client.get("/companies/CO-DEMO/drafts")
    s = g.json()[0]
    if full["derived_gst"] is None:
        assert s["grand_total"] == "0.0000"
    else:
        assert s["grand_total"] == full["derived_gst"]["invoice_total"]
    assert isinstance(s["grand_total"], str)


def _draft_without_gst(draft_id: str, company_id: str = "CO-DEMO") -> PurchaseVoucherDraft:
    """A minimal draft with derived_gst=None and body.party=None to drive the
    grand_total fallback ('0.0000') and the party-empty-string derivation."""
    return PurchaseVoucherDraft(
        draft_id=draft_id,
        company_id=company_id,
        created_at="2026-06-14T00:00:00Z",
        source="upload",
        status="low_confidence_manual",
        body=VoucherDraftBody(party=None, postings=[]),
        confidence=ConfidenceMap(
            header={},
            line_items=[],
            overall_header=0.4,
            overall_line_items=0.3,
            overall=0.3,
        ),
        redaction=RedactionReportSummary(
            any_redacted=False, counts_by_type={}, total=0
        ),
        unresolved=["supplier"],
        derived_gst=None,
    )


def test_grand_total_and_party_fallbacks_when_fields_absent():
    client, store = _client()
    store.put(_draft_without_gst("D-NO-GST"))
    g = client.get("/companies/CO-DEMO/drafts")
    assert g.status_code == 200
    s = g.json()[0]
    # derived_gst is None -> '0.0000' STRING; body.party is None -> '' string
    assert s["grand_total"] == "0.0000"
    assert s["party"] == ""
    assert s["status"] == "low_confidence_manual"
    assert s["confidence"] == 0.3


def test_list_is_company_scoped_empty_for_unknown_company():
    client, _ = _client()
    _post_fixture(client, "intra-state-18-1line-clean")
    # RLS-style: another company's collection is empty, NOT a 404
    g = client.get("/companies/OTHER-CO/drafts")
    assert g.status_code == 200
    assert g.json() == []
