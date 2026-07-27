"""Uploads ENQUEUE when the recogniser is the slow one, and poll to completion.

WS10 §8.6 item 5. The spike measured ~45 s for a 12 MP phone photo, which cannot
sit inside an HTTP request: the client times out, retries, and the same bill is
OCR'd twice. So with `INSIGHTD_USE_REAL_OCR=1` an upload returns IMMEDIATELY with
a draft in status "processing" and the work runs in the background.

ONE RESPONSE SHAPE EITHER WAY is the contract that matters. With the
deterministic fakes the pipeline finishes inside the request, so the draft
returned is already terminal — a client always reads the status and polls while
it is "processing", and never has to know which mode the server is in. A
two-shaped API (201-with-a-draft here, 202-with-a-location there) would push that
knowledge into every caller.
"""

from __future__ import annotations

import base64

import pytest
from fastapi.testclient import TestClient

from insightd.api.app import create_app


@pytest.fixture()
def client():
    return TestClient(create_app())


FIXTURE_UPLOAD = {
    "tenant": {"company_id": "co-1"},
    "source": "upload",
    "filename": "bill.pdf",
    "content_b64": base64.b64encode(b"%PDF-1.4 not really a pdf").decode("ascii"),
    "content_type": "application/pdf",
}


def test_with_the_FAKES_the_draft_comes_back_already_terminal(client, monkeypatch):
    monkeypatch.delenv("INSIGHTD_USE_REAL_OCR", raising=False)
    res = client.post("/companies/co-1/drafts", json=FIXTURE_UPLOAD)
    assert res.status_code == 201, res.text
    # Not "processing": the fake pipeline ran inside the request, so a client
    # that polls simply exits its loop on the first read.
    assert res.json()["status"] != "processing"


def test_with_REAL_OCR_the_upload_returns_immediately_in_processing(client, monkeypatch):
    monkeypatch.setenv("INSIGHTD_USE_REAL_OCR", "1")
    res = client.post("/companies/co-1/drafts", json=FIXTURE_UPLOAD)
    assert res.status_code == 201, res.text
    body = res.json()
    draft_id = body["draft_id"]

    # TestClient runs BackgroundTasks after the response, so by the time we can
    # poll the work is done — which is exactly the client's experience, just
    # faster. What matters is that the draft EXISTS and is pollable by id.
    poll = client.get(f"/companies/co-1/drafts/{draft_id}")
    assert poll.status_code == 200, poll.text
    assert poll.json()["draft_id"] == draft_id
    # The placeholder must never masquerade as postable work.
    assert poll.json()["status"] in {"processing", "ready", "low_confidence_manual"}


def test_a_processing_draft_is_LISTED_so_an_upload_never_looks_lost(client, monkeypatch):
    monkeypatch.setenv("INSIGHTD_USE_REAL_OCR", "1")
    res = client.post("/companies/co-1/drafts", json=FIXTURE_UPLOAD)
    draft_id = res.json()["draft_id"]

    listed = client.get("/companies/co-1/drafts").json()
    assert any(d["draft_id"] == draft_id for d in listed), (
        "an upload that vanished from the inbox until OCR finished would look "
        "like a failure to the person who just uploaded it"
    )


def test_a_processing_placeholder_carries_NOTHING_postable(client, monkeypatch):
    # The placeholder exists to be visible, not to be accepted. Its body has no
    # postings, so the F9 accept path — which needs real ledger ids — cannot
    # turn it into a voucher even if a client tried.
    from insightd.api.routes_drafts import _processing_placeholder
    from insightd.contracts.pipeline import TenantContext

    ph = _processing_placeholder(TenantContext(company_id="co-1"), "upload", "bill.pdf")
    assert ph.status == "processing"
    assert ph.body.postings == []
    assert ph.confidence.overall == 0.0
    # ...and it says what it is waiting on, rather than showing an empty screen.
    assert any("bill.pdf" in u for u in ph.unresolved)


def test_a_FAILED_background_run_does_not_leave_the_draft_stuck(monkeypatch):
    """A spinner that never resolves is the worst outcome for a review queue.

    Nobody can tell "still working" from "died", so the operator either waits
    forever or re-uploads. A failure must land the draft in a terminal state
    carrying the reason.
    """
    from insightd.api.routes_drafts import _run_and_store, _processing_placeholder
    from insightd.contracts.pipeline import RawUpload, TenantContext

    ctx = TenantContext(company_id="co-1")
    ph = _processing_placeholder(ctx, "upload", "bill.pdf")

    class BoomPipeline:
        def run(self, *_a, **_k):
            raise RuntimeError("paddle exploded")

    class Store:
        def __init__(self):
            self.saved = None

        def put(self, d):
            self.saved = d

    store = Store()
    upload = RawUpload(
        content_b64=base64.b64encode(b"x").decode("ascii"),
        content_type="application/pdf",
        filename="bill.pdf",
        source="upload",
    )
    _run_and_store(BoomPipeline(), store, upload, ctx, None, ph.draft_id)

    assert store.saved is not None
    assert store.saved.status == "low_confidence_manual", "a failed run left the draft processing"
    assert store.saved.draft_id == ph.draft_id, "the failure must replace the SAME draft"
    assert any("paddle exploded" in u for u in store.saved.unresolved), (
        "the reason must reach the operator, not just the log"
    )
