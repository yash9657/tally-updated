"""API integration tests via httpx TestClient using the fakes."""

from __future__ import annotations

from fastapi.testclient import TestClient

from insightd.api.app import create_app
from insightd.api.deps import get_draft_store
from insightd.api.store import DraftStore

from conftest import load_fixture


def _client():
    app = create_app()
    # isolate the store per app instance
    store = DraftStore()
    app.dependency_overrides[get_draft_store] = lambda: store
    return TestClient(app), store


def _post_fixture(client, fid):
    fx = load_fixture(fid)
    ctx = fx["input"]["tenant"]
    body = {"source": "upload", "tenant": ctx, "fixture": fx}
    return client.post(f"/companies/{ctx['company_id']}/drafts", json=body)


def test_healthz_reports_adapter_set():
    client, _ = _client()
    r = client.get("/healthz")
    assert r.status_code == 200
    data = r.json()
    assert data["status"] == "ok"
    assert data["adapters"]["classifier"] == "fake"
    assert data["adapters"]["mapper"] == "pure"
    assert data["cored_relationship"] == "read-only"


def test_post_draft_returns_201_and_ready():
    client, _ = _client()
    r = _post_fixture(client, "intra-state-18-1line-clean")
    assert r.status_code == 201, r.text
    draft = r.json()
    assert draft["status"] == "ready"
    assert draft["body"]["voucher_class"] == "purchase"
    assert draft["body"]["party_ledger_id"] == "L-PARTY-1"
    # masked-only: no raw GSTIN anywhere in the response
    assert "27ABCDE1234F1Z5" not in r.text


def test_adversarial_post_routes_to_manual():
    client, _ = _client()
    r = _post_fixture(client, "adv-degraded-scan")
    assert r.status_code == 201
    assert r.json()["status"] == "low_confidence_manual"


def test_get_draft_roundtrip():
    client, _ = _client()
    r = _post_fixture(client, "intra-state-18-1line-clean")
    draft_id = r.json()["draft_id"]
    g = client.get(f"/companies/CO-DEMO/drafts/{draft_id}")
    assert g.status_code == 200
    assert g.json()["draft_id"] == draft_id


def test_get_draft_other_company_is_404():
    client, _ = _client()
    r = _post_fixture(client, "intra-state-18-1line-clean")
    draft_id = r.json()["draft_id"]
    # RLS-style scoping: another company_id cannot see it
    g = client.get(f"/companies/OTHER-CO/drafts/{draft_id}")
    assert g.status_code == 404


def test_company_id_mismatch_rejected():
    client, _ = _client()
    fx = load_fixture("intra-state-18-1line-clean")
    body = {"source": "upload", "tenant": fx["input"]["tenant"], "fixture": fx}
    r = client.post("/companies/WRONG-CO/drafts", json=body)
    assert r.status_code == 400


def test_no_ledger_write_endpoint_exists():
    client, _ = _client()
    # there is no route that writes to cored's ledger
    r = client.post("/companies/CO-DEMO/vouchers/purchase", json={})
    assert r.status_code in (404, 405)
