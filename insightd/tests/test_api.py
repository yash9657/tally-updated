"""API integration tests via httpx TestClient using the fakes."""

from __future__ import annotations

from fastapi.testclient import TestClient

from insightd.api.app import create_app
from insightd.api.authn import COMPANY_HEADER, SERVICE_TOKEN_HEADER
from insightd.api.deps import get_draft_store
from insightd.api.store import DraftStore

from conftest import TEST_SERVICE_TOKEN, ProxyTestClient, load_fixture


def _client():
    app = create_app()
    # isolate the store per app instance
    store = DraftStore()
    app.dependency_overrides[get_draft_store] = lambda: store
    return ProxyTestClient(app), store


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


def test_healthz_DISCLOSES_what_a_token_is_checked_AGAINST():
    """The posture a deployer cannot otherwise read off this process.

    ⛔ THIS TEST USED TO PIN A KNOWN-BAD STATE ON PURPOSE — ``authentication:
    "none"`` and ``tenant_scope: "caller-supplied company_id in the path"`` — and
    its message demanded that the value could only move together with the
    mechanism, and that the commit SAY WHAT A TOKEN IS CHECKED AGAINST.

    ⛔ IT MOVED (register row D49, 2026-08-24), AND HERE IS THE ANSWER IT ASKED
    FOR. cored is the only door. The browser calls
    ``/v1/companies/{id}/drafts...`` on cored with its own bearer; cored verifies
    the JWT, rejects a revoked session, requires membership, evaluates
    (Display|Create, "purchase") against the caller's level, and only then
    forwards here presenting a service credential shared by exactly those two
    processes plus the company its PDP just authorized. insightd checks the
    credential in constant time and requires the authorized company to equal the
    ``company_id`` in the path. It holds no keys, verifies no signatures, and
    re-implements none of cored's PDP.

    ⚠️ THE PIN IS STILL A PIN. Nothing may change these two strings without
    changing what they describe — including "improving" the wording.
    """
    client, _ = _client()
    data = client.get("/healthz").json()
    assert data["authentication"] == "cored-proxy service token", (
        "if this changed, insightd's posture changed — update the D49 row and "
        "this test together, and say what a token is checked AGAINST"
    )
    assert data["tenant_scope"] == (
        "company authorized by cored's PDP (X-Insightd-Company == path company_id)"
    )
    # The ONE anonymous route, named on the wire so a deployer can see the whole
    # unauthenticated surface without reading this file.
    assert data["unauthenticated_routes"] == ["/healthz"]


# ===========================================================================
# THE REFUSALS (register row D49). Driven with a PLAIN TestClient, which
# presents nothing — the ProxyTestClient the rest of this file uses always
# presents a valid credential and therefore can never observe a refusal.
# ===========================================================================

# Every company-scoped route, with the method it answers and a body that would be
# ACCEPTED if the caller were authorized. ⛔ THE BODIES MATTER: a route that
# refuses a malformed body with a 422 before the credential is checked would make
# this whole table vacuous, so each entry is a request that could otherwise work.
_GUARDED_ROUTES = [
    ("POST", "/companies/CO-DEMO/drafts", {"source": "upload", "tenant": {"company_id": "CO-DEMO"}, "fixture": None, "content_b64": "eA=="}),
    ("GET", "/companies/CO-DEMO/drafts", None),
    ("GET", "/companies/CO-DEMO/drafts/D-1", None),
    ("POST", "/companies/CO-DEMO/drafts/D-1/claim", {"claim_token": "t", "claimed_by": "x"}),
    ("POST", "/companies/CO-DEMO/drafts/D-1/claim/release", {"claim_token": "t"}),
    ("POST", "/companies/CO-DEMO/drafts/D-1/accepted", {"voucher_id": "V-1", "voucher_no": "1"}),
]


def _plain_client():
    """A client that presents NO credential at all."""
    app = create_app()
    store = DraftStore()
    app.dependency_overrides[get_draft_store] = lambda: store
    return TestClient(app), store


def _drive(client, method, path, body, headers=None):
    if method == "GET":
        return client.get(path, headers=headers or {})
    return client.post(path, json=body, headers=headers or {})


def test_every_company_scoped_route_refuses_an_anonymous_caller():
    """⛔ SIX REFUSALS, ROUTE BY ROUTE — not one and an argument that the rest are
    the same shape. That argument is exactly how ``/benchmark/last`` and the four
    auto-doc routes stayed uncounted through three written scopings of this row.
    """
    client, _ = _plain_client()
    for method, path, body in _GUARDED_ROUTES:
        r = _drive(client, method, path, body)
        assert r.status_code == 401, f"{method} {path} answered {r.status_code}, not 401: {r.text[:200]}"
        assert "service token" in r.json()["detail"]


def test_a_wrong_service_token_is_refused():
    client, _ = _plain_client()
    for method, path, body in _GUARDED_ROUTES:
        r = _drive(client, method, path, body, {SERVICE_TOKEN_HEADER: "not-the-token", COMPANY_HEADER: "CO-DEMO"})
        assert r.status_code == 401, f"{method} {path} answered {r.status_code}"


def test_a_valid_token_without_an_authorized_company_is_refused():
    """The credential alone is not enough. It says the request came through the
    proxy; it does not say WHICH company the caller may read — which is the half
    of this that a bolted-on shared secret could never have supplied."""
    client, _ = _plain_client()
    for method, path, body in _GUARDED_ROUTES:
        r = _drive(client, method, path, body, {SERVICE_TOKEN_HEADER: TEST_SERVICE_TOKEN})
        assert r.status_code == 403, f"{method} {path} answered {r.status_code}"
        assert COMPANY_HEADER in r.json()["detail"]


def test_an_authorized_company_that_disagrees_with_the_path_is_refused():
    """⛔ THIS IS THE ARM THAT REPLACES "caller-supplied company_id in the path".

    A caller who reached the proxy for their OWN company and then rewrote the path
    to somebody else's is refused HERE, by the disagreement, rather than served
    whatever the path said. 403 and not 404: the two values disagreeing is an
    authorization failure, and it must not leak whether either company exists.
    """
    client, _ = _plain_client()
    for method, path, body in _GUARDED_ROUTES:
        r = _drive(client, method, path, body, {SERVICE_TOKEN_HEADER: TEST_SERVICE_TOKEN, COMPANY_HEADER: "SOMEBODY-ELSE"})
        assert r.status_code == 403, f"{method} {path} answered {r.status_code}"
        assert "does not match" in r.json()["detail"]


def test_benchmark_report_is_not_anonymous():
    """The eighth route, and it was uncounted twice because it is not company
    scoped. There is no company to assert, so only the credential applies."""
    plain, _ = _plain_client()
    assert plain.get("/benchmark/last").status_code == 401
    ok = plain.get("/benchmark/last", headers={SERVICE_TOKEN_HEADER: TEST_SERVICE_TOKEN})
    assert ok.status_code == 200, ok.text


def test_an_UNCONFIGURED_insightd_refuses_everything_rather_than_serving_it_open(monkeypatch):
    """⛔ FAIL-CLOSED, AND 503 RATHER THAN 401 ON PURPOSE.

    A deployer who forgot INSIGHTD_SERVICE_TOKEN must be told the SERVICE is
    unconfigured, not that their credential was rejected — those two sentences
    send you to two different files. What must never happen is the third option:
    an unconfigured process deciding it has nothing to check and serving the
    drafts to whoever asked.
    """
    import insightd.config as config_mod

    monkeypatch.setattr(config_mod, "_settings", config_mod.Settings(database_url=None, service_token=None))
    client, _ = _plain_client()
    for method, path, body in _GUARDED_ROUTES:
        r = _drive(client, method, path, body, {SERVICE_TOKEN_HEADER: TEST_SERVICE_TOKEN, COMPANY_HEADER: "CO-DEMO"})
        assert r.status_code == 503, f"{method} {path} answered {r.status_code}"
        assert "INSIGHTD_SERVICE_TOKEN" in r.json()["detail"]
    # ⚠️ AND /healthz STILL ANSWERS — that is the whole reason insightd refuses
    # per-request instead of refusing to boot the way cored does. A process that
    # will not start cannot tell anyone why.
    assert client.get("/healthz").status_code == 200


def test_the_four_auto_doc_routes_no_longer_publish_the_schema():
    """⛔ FOUR ROUTES NOBODY HAD COUNTED, ALL 200, ALL PUBLISHING THE WHOLE SCHEMA.

    They are absent from ``openapi.paths``, which is why every openapi-based count
    of this service's exposure missed them — including two written scopings of
    D49. One knob (``openapi_url=None``) unmounts all four.
    """
    client, _ = _plain_client()
    for path in ("/openapi.json", "/docs", "/redoc", "/docs/oauth2-redirect"):
        assert client.get(path).status_code == 404, f"{path} is still served"
    # ⚠️ THE SCHEMA ITSELF IS NOT LOST, only the anonymous route to it. This is
    # what tests/test_forecast_plane_is_gone.py reads.
    assert create_app().openapi()["info"]["title"] == "insightd"


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
    """⛔ AND IT MUST STAY 404/405 UNDER AUTHENTICATION, WHICH IS WHY THE
    CREDENTIAL IS A ROUTE DEPENDENCY AND NOT MIDDLEWARE.

    This is the product's proof that insightd has no ledger-write path: it POSTs
    to one and observes that nothing answers. A global 401-before-routing would
    turn that proof into a statement about headers — every unserved path would
    answer 401, and this test would go red without the HITL boundary having moved
    an inch. The guard therefore hangs off the drafts ROUTER, so Starlette matches
    the route first and an unmatched path is still 404.

    ⚠️ IT IS DRIVEN BOTH WAYS for that reason: credentialed and not. Both answer
    404, because there is nothing there to authorize.
    """
    client, _ = _client()
    r = client.post("/companies/CO-DEMO/vouchers/purchase", json={})
    assert r.status_code in (404, 405), r.status_code
    plain, _ = _plain_client()
    r2 = plain.post("/companies/CO-DEMO/vouchers/purchase", json={})
    assert r2.status_code in (404, 405), (
        f"an unserved path answered {r2.status_code} to an anonymous caller — the "
        "credential is running BEFORE routing, which breaks the HITL proof above"
    )
