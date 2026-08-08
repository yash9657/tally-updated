"""The TERMINAL draft lifecycle — POST /companies/{id}/drafts/{id}/accepted.

⛔ WHY THIS FILE EXISTS. A purchase draft that had already become a cored voucher
stayed ``status: "ready"`` and stayed in the F9 Bill Inbox, because insightd had no
state to move it to and no route that could move it. The frontend answered with two
CLIENT-SIDE guards (a page-lifetime memo keyed on the draft, and a day-book lookup
keyed on supplier invoice number + date). A browser re-drive after that fix produced
TWO REAL VOUCHERS from one supplier bill: the draft whose invoice number the OCR
never read has nothing for the day-book layer to match on, and a page RELOAD empties
the memo. A guard whose only copy of the truth lives in the client is beaten by
reloading the client.

So these tests are about the SERVER's memory:
  * the transition is real, terminal, and records WHICH voucher the draft became;
  * a SECOND acceptance is refused with the FIRST voucher's identity, never
    silently re-recorded (an operator told "Saved" twice cannot tell a blocked
    duplicate from a written one);
  * the transition is COMPARE-AND-SET, so two concurrent accepts cannot both win;
  * an accepted draft is no longer offered as ``ready`` — and is still LISTED, with
    the voucher link, because an operator who cannot see what happened re-does it;
  * a ``processing`` placeholder cannot be marked accepted at all;
  * insightd is still told an outcome and never asked to post one.

⚠️ THE STORE UNDER TEST HERE IS IN-MEMORY, so what is proven is the SEMANTICS, not
durability: this record dies with the process. The durable backend is
``pg_store.PGDraftStore`` (INSIGHTD_DATABASE_URL), whose ``mark_accepted`` locks the
row with ``SELECT … FOR UPDATE`` inside one transaction; tests/test_pg_store.py
covers it in the live-PG group.
"""

from __future__ import annotations

import threading

from fastapi.testclient import TestClient

from insightd.api.app import create_app
from insightd.api.deps import get_draft_store
from insightd.api.store import DraftStore
from insightd.contracts.cored_map import PostingDraft, VoucherDraftBody
from insightd.contracts.draft import (
    ConfidenceMap,
    GSTAmounts,
    PurchaseVoucherDraft,
    RedactionReportSummary,
)

COMPANY = "CO-DEMO"


def _client():
    app = create_app()
    store = DraftStore()
    app.dependency_overrides[get_draft_store] = lambda: store
    return TestClient(app), store


def _ready_draft(
    draft_id: str = "D-1",
    company_id: str = COMPANY,
    voucher_no: str | None = None,
    status: str = "ready",
) -> PurchaseVoucherDraft:
    """A ready, balanced, inter-state purchase draft.

    ``voucher_no`` DEFAULTS TO None on purpose: the numberless draft is the exact
    shape the browser re-drive duplicated, because it is the one the frontend's
    day-book guard cannot see. Money stays a fixed-4 STRING throughout.
    """
    return PurchaseVoucherDraft(
        draft_id=draft_id,
        company_id=company_id,
        created_at="2026-07-12T04:00:00+00:00",
        source="upload",
        status=status,  # type: ignore[arg-type]
        body=VoucherDraftBody(
            party="Pune Fabricators",
            party_ledger_id="ledger-pune",
            voucher_no=voucher_no,
            voucher_date="2026-07-12",
            postings=[
                PostingDraft(
                    ledger_id="ledger-purchases",
                    ledger_name="Raw Material Purchases",
                    dr_cr="D",
                    amount="5000.0000",
                ),
                PostingDraft(
                    ledger_id="ledger-igst",
                    ledger_name="Input IGST",
                    dr_cr="D",
                    amount="900.0000",
                ),
                PostingDraft(
                    ledger_id="ledger-pune",
                    ledger_name="Pune Fabricators",
                    dr_cr="C",
                    amount="5900.0000",
                ),
            ],
        ),
        confidence=ConfidenceMap(
            header={}, line_items=[], overall_header=0.95, overall_line_items=0.95, overall=0.95
        ),
        redaction=RedactionReportSummary(any_redacted=False, counts_by_type={}, total=0),
        unresolved=[],
        derived_gst=GSTAmounts(
            cgst="0.0000",
            sgst="0.0000",
            igst="900.0000",
            total_tax="900.0000",
            invoice_total="5900.0000",
        ),
    )


# ---------------------------------------------------------------------------
# The transition
# ---------------------------------------------------------------------------


def test_a_fresh_draft_carries_no_acceptance_link():
    """The link fields default to None, so "accepted" is never accidentally true.

    They also DEFAULT, which is what lets a draft row stored before these fields
    existed rehydrate under ``extra='forbid'``.
    """
    client, store = _client()
    store.put(_ready_draft())
    got = client.get(f"/companies/{COMPANY}/drafts/D-1").json()
    assert got["status"] == "ready"
    assert got["accepted_voucher_id"] is None
    assert got["accepted_voucher_no"] is None
    assert got["accepted_at"] is None


def test_recording_an_acceptance_makes_the_draft_terminal_and_names_the_voucher():
    client, store = _client()
    store.put(_ready_draft())

    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/accepted",
        json={"voucher_id": "v-3", "voucher_no": "3"},
    )
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["status"] == "accepted"
    assert body["accepted_voucher_id"] == "v-3"
    assert body["accepted_voucher_no"] == "3"
    assert body["accepted_at"], "an acceptance must be stamped with a time"
    # The draft's own payload is untouched — insightd recorded an outcome, it did
    # not rewrite the proposal.
    assert body["body"] == _ready_draft().model_dump()["body"]

    # And it is the STORE that changed, not just the response: a fresh GET (i.e. a
    # reloaded page) sees the terminal state. THIS is the part no client-side memo
    # can do.
    again = client.get(f"/companies/{COMPANY}/drafts/D-1").json()
    assert again["status"] == "accepted"
    assert again["accepted_voucher_no"] == "3"


def test_a_numberless_draft_is_marked_just_the_same():
    """⛔ THE DRIVEN CASE. The bill whose invoice number OCR never read is exactly
    the one the client-side day-book guard is blind to, so the server-side record
    must not depend on a number either."""
    client, store = _client()
    d = _ready_draft(voucher_no=None)
    assert d.body.voucher_no is None
    store.put(d)
    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/accepted", json={"voucher_id": "v-3"}
    )
    assert r.status_code == 200, r.text
    assert r.json()["status"] == "accepted"
    # voucher_no is optional (cored allocates it in its own transaction).
    assert r.json()["accepted_voucher_no"] == ""


def test_a_second_acceptance_is_refused_and_carries_the_first_voucher():
    """⛔ THE MONEY ASSERTION AT THIS LAYER. The second attempt must be told about
    the first voucher, and must NOT overwrite it: a refusal that looked like a
    success is what let one bill become vouchers 3 AND 4."""
    client, store = _client()
    store.put(_ready_draft())
    first = client.post(
        f"/companies/{COMPANY}/drafts/D-1/accepted",
        json={"voucher_id": "v-3", "voucher_no": "3"},
    )
    assert first.status_code == 200

    second = client.post(
        f"/companies/{COMPANY}/drafts/D-1/accepted",
        json={"voucher_id": "v-4", "voucher_no": "4"},
    )
    assert second.status_code == 409, second.text
    detail = second.json()["detail"]
    assert detail["code"] == "draft_already_accepted"
    assert detail["accepted_voucher_id"] == "v-3"
    assert detail["accepted_voucher_no"] == "3"
    assert "3" in detail["message"]

    # The FIRST acceptance is the one that stands. v-4 was never recorded.
    stored = store.get(COMPANY, "D-1")
    assert stored is not None
    assert stored.accepted_voucher_id == "v-3"
    assert stored.accepted_voucher_no == "3"


def test_the_transition_is_compare_and_set_under_concurrency():
    """Two accepts of one draft racing in the threadpool: exactly ONE may win.

    ⛔ MUTATION-SENSITIVE BY DESIGN. Rewrite ``DraftStore.mark_accepted`` as
    get() → inspect → put() (the obvious implementation, and the one a reader is
    tempted to "simplify" it into) and both threads read "ready", both write, and
    both return 200 — the same duplicate, one layer down.
    """
    store = DraftStore()
    store.put(_ready_draft())
    outcomes: list[str] = []
    lock = threading.Lock()
    start = threading.Barrier(8)

    def attempt(i: int) -> None:
        start.wait()
        res = store.mark_accepted(COMPANY, "D-1", f"v-{i}", str(i))
        with lock:
            outcomes.append(res.outcome)

    threads = [threading.Thread(target=attempt, args=(i,)) for i in range(8)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    assert outcomes.count("recorded") == 1, outcomes
    assert outcomes.count("already_accepted") == 7, outcomes


def test_an_unknown_draft_is_a_404_and_another_companys_is_indistinguishable():
    client, store = _client()
    store.put(_ready_draft(company_id="OTHER-CO"))
    # unknown id
    r = client.post(
        f"/companies/{COMPANY}/drafts/nope/accepted", json={"voucher_id": "v-9"}
    )
    assert r.status_code == 404
    # a real draft, wrong company: same answer, no existence leak
    r2 = client.post(
        f"/companies/{COMPANY}/drafts/D-1/accepted", json={"voucher_id": "v-9"}
    )
    assert r2.status_code == 404
    # …and it was NOT marked in the other company either.
    other = store.get("OTHER-CO", "D-1")
    assert other is not None and other.status == "ready"


def test_a_processing_placeholder_cannot_be_marked_accepted():
    """Its body is empty by construction ("nothing downstream can post it"), so an
    acceptance recorded against it would be a lie about the books."""
    client, store = _client()
    store.put(_ready_draft(status="processing"))
    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/accepted", json={"voucher_id": "v-3"}
    )
    assert r.status_code == 409, r.text
    assert r.json()["detail"]["code"] == "draft_still_processing"
    stored = store.get(COMPANY, "D-1")
    assert stored is not None and stored.status == "processing"


def test_the_route_cannot_be_handed_a_voucher_payload():
    """extra='forbid' on RecordAcceptRequest: insightd is told an OUTCOME. A route
    that accepted postings/amounts would be the first crack in the HITL boundary."""
    client, store = _client()
    store.put(_ready_draft())
    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/accepted",
        json={"voucher_id": "v-3", "postings": [{"amount": "5900.0000"}]},
    )
    assert r.status_code == 422, r.text
    assert store.get(COMPANY, "D-1").status == "ready"  # nothing happened


def test_voucher_id_is_required():
    client, store = _client()
    store.put(_ready_draft())
    r = client.post(f"/companies/{COMPANY}/drafts/D-1/accepted", json={})
    assert r.status_code == 422
    assert store.get(COMPANY, "D-1").status == "ready"


# ---------------------------------------------------------------------------
# The inbox, after an acceptance
# ---------------------------------------------------------------------------


def test_an_accepted_draft_is_no_longer_offered_as_ready_but_is_still_listed():
    """⛔ BOTH HALVES MATTER.

    'no longer ready' is the fix: the row the browser showed after a SUCCESSFUL
    accept was "Pune Fabricators · 21240.0000 · ready · 95%", which is an
    invitation to post the bill again.

    'still listed, with the link' is the deliberate choice against merely hiding
    it: an operator who uploaded a bill and can no longer find it anywhere re-does
    it, which is the same duplicate arriving by a different route.
    """
    client, store = _client()
    store.put(_ready_draft(draft_id="D-1"))
    store.put(_ready_draft(draft_id="D-2"))

    client.post(
        f"/companies/{COMPANY}/drafts/D-1/accepted",
        json={"voucher_id": "v-3", "voucher_no": "3"},
    )

    rows = client.get(f"/companies/{COMPANY}/drafts").json()
    by_id = {r["draft_id"]: r for r in rows}
    assert set(by_id) == {"D-1", "D-2"}, "an accepted draft must stay visible"

    assert by_id["D-1"]["status"] == "accepted"
    assert by_id["D-1"]["status"] != "ready"
    assert by_id["D-1"]["accepted_voucher_no"] == "3"
    assert by_id["D-1"]["accepted_voucher_id"] == "v-3"

    # The untouched draft is unaffected — the transition is per-draft.
    assert by_id["D-2"]["status"] == "ready"
    assert by_id["D-2"]["accepted_voucher_no"] is None

    # And the ready queue an inbox would offer now holds only D-2.
    assert [r["draft_id"] for r in rows if r["status"] == "ready"] == ["D-2"]


def test_a_low_confidence_manual_draft_can_also_be_accepted():
    """It is a POSTABLE proposal (prefilled for full manual review), just not an
    accept-ready one, and the human may still post it. Only 'processing' is barred."""
    client, store = _client()
    store.put(_ready_draft(status="low_confidence_manual"))
    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/accepted",
        json={"voucher_id": "v-7", "voucher_no": "7"},
    )
    assert r.status_code == 200, r.text
    assert r.json()["status"] == "accepted"


def test_the_accepted_status_survives_a_json_roundtrip_through_the_store():
    """The PG backend stores the whole draft as jsonb and revalidates it under
    ``extra='forbid'`` on the way out; the new fields must survive that."""
    d = _ready_draft()
    accepted = d.model_copy(
        update={
            "status": "accepted",
            "accepted_voucher_id": "v-3",
            "accepted_voucher_no": "3",
            "accepted_at": "2026-07-12T05:00:00+00:00",
        }
    )
    back = PurchaseVoucherDraft.model_validate_json(accepted.model_dump_json())
    assert back == accepted
    assert back.status == "accepted"


def test_a_draft_stored_before_the_link_fields_existed_still_rehydrates():
    """Forward-compat for rows already in insightd_draft: the three link fields are
    absent from that JSON and must default rather than fail validation."""
    legacy = _ready_draft().model_dump()
    for k in ("accepted_voucher_id", "accepted_voucher_no", "accepted_at"):
        legacy.pop(k)
    back = PurchaseVoucherDraft.model_validate(legacy)
    assert back.status == "ready"
    assert back.accepted_voucher_id is None


# ---------------------------------------------------------------------------
# THE POSTING CLAIM — reserve before posting, not check before posting
#
# ⛔ WHY THESE EXIST SEPARATELY FROM THE ACCEPT TESTS ABOVE. Every test above proves
# ``mark_accepted`` is a correct compare-and-set — and it IS. It still let one supplier
# bill into the books twice, because it necessarily runs AFTER the voucher is posted:
#
#   BROWSER A: Saved / No: 11  POST …/vouchers/purchase 201  POST …/accepted 200
#   BROWSER B: Saved / No: 12  POST …/vouchers/purchase 201  POST …/accepted 409
#
# The 409 was correct and far too late — 27000.0000 + 4860.0000 IGST, twice. So the CAS
# moved in FRONT of the post: claim -> post -> confirm. These tests are about the claim,
# and about the control that stops it becoming a WORSE defect than the duplicate (a bill
# stranded reserved forever, which an operator could never post and never release).
# ---------------------------------------------------------------------------


def test_a_fresh_draft_is_unclaimed_and_a_claim_reserves_it():
    client, store = _client()
    store.put(_ready_draft())

    before = client.get(f"/companies/{COMPANY}/drafts/D-1").json()
    assert before["claim_token"] is None
    assert before["claim_expires_at"] is None

    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-A", "claimed_by": "browser A"},
    )
    assert r.status_code == 200
    body = r.json()
    assert body["claim_token"] == "tok-A"
    assert body["claimed_by"] == "browser A"
    assert body["claim_expires_at"] is not None
    # ⛔ A CLAIM IS NOT A STATUS. The bill really is still unposted, so it must stay
    # postable — a claim that changed the status would hide it from the inbox and make
    # a lapsed lease look like a lost bill.
    assert body["status"] == "ready"


def test_a_second_poster_is_refused_and_told_the_bill_is_being_posted():
    client, store = _client()
    store.put(_ready_draft())
    client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-A", "claimed_by": "browser A"},
    )

    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-B", "claimed_by": "browser B"},
    )
    assert r.status_code == 409
    detail = r.json()["detail"]
    assert detail["code"] == "draft_being_posted"
    # The operator is told it is BEING posted (transient, and nothing was written by
    # this attempt) rather than that it is already posted (terminal).
    assert "already being posted" in detail["message"]
    assert "No voucher has been posted from this attempt" in detail["message"]
    assert detail["claimed_by"] == "browser A"
    assert detail["claim_expires_at"] is not None
    # The holder is UNCHANGED — a refused claim must not steal the lease.
    assert store.get(COMPANY, "D-1").claim_token == "tok-A"


def test_the_same_token_may_renew_its_own_claim_and_keeps_its_original_claimed_at():
    client, store = _client()
    store.put(_ready_draft())
    first = client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-A", "claimed_by": "browser A"},
    ).json()

    again = client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-A", "claimed_by": "browser A"},
    )
    # ⛔ IDEMPOTENT FOR THE SAME TOKEN. A retry from the page that already holds the
    # claim must never be locked out by itself, or one failed attempt would cost the
    # operator the whole TTL on their own bill.
    assert again.status_code == 200
    assert again.json()["claimed_at"] == first["claimed_at"]  # when work STARTED
    assert again.json()["claim_expires_at"] >= first["claim_expires_at"]


def test_exactly_one_of_many_concurrent_claims_wins():
    """⛔ THE ASSERTION THAT THE WHOLE ORDERING RESTS ON.

    The draft routes are sync ``def``s, so Starlette runs them in a THREADPOOL: these
    claims really are concurrent. If two could win, two browsers would both post, which
    is precisely the defect — and the reason a fifth *check* could not have fixed it.
    """
    client, store = _client()
    store.put(_ready_draft())

    codes: list[int] = []
    lock = threading.Lock()

    def claim(i: int) -> None:
        r = client.post(
            f"/companies/{COMPANY}/drafts/D-1/claim",
            json={"claim_token": f"tok-{i}", "claimed_by": f"browser {i}"},
        )
        with lock:
            codes.append(r.status_code)

    threads = [threading.Thread(target=claim, args=(i,)) for i in range(12)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    assert codes.count(200) == 1, codes
    assert codes.count(409) == 11, codes
    # And the one live claim belongs to a real caller, not to a torn write.
    assert store.get(COMPANY, "D-1").claim_token in {f"tok-{i}" for i in range(12)}


def test_a_lapsed_claim_reads_as_unclaimed_so_a_dead_browser_cannot_strand_a_bill():
    client, store = _client()
    store.put(_ready_draft())
    # A browser claimed the draft and died: it never posted and never released. The
    # lease is driven directly because the TTL is deliberately not client-settable.
    store.claim_for_accept(
        COMPANY, "D-1", "tok-dead", "the dead browser", at="2020-01-01T00:00:00+00:00"
    )

    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-next", "claimed_by": "the next poster"},
    )
    # ⛔ A PERMANENT CLAIM WOULD BE WORSE THAN THE DUPLICATE: a bill nobody can ever
    # post, with no route to release it. The lease lapses instead.
    assert r.status_code == 200
    assert r.json()["claim_token"] == "tok-next"


def test_an_unparseable_expiry_degrades_to_unclaimed_rather_than_locking_the_bill():
    client, store = _client()
    draft = _ready_draft()
    store.put(
        draft.model_copy(
            update={"claim_token": "tok-x", "claim_expires_at": "not-a-timestamp"}
        )
    )
    # A hand-edited or migrated jsonb row must not become an unpostable bill, and must
    # not raise on the path of the human's only write.
    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-new", "claimed_by": "next"},
    )
    assert r.status_code == 200


def test_releasing_gives_the_bill_straight_back_without_waiting_for_the_ttl():
    client, store = _client()
    store.put(_ready_draft())
    client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-A", "claimed_by": "browser A"},
    )

    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim/release",
        json={"claim_token": "tok-A"},
    )
    assert r.status_code == 200
    assert r.json()["claim_token"] is None
    assert r.json()["status"] == "ready"
    # ⛔ THE CONTROL THAT STOPS THE FIX BECOMING A WORSE BUG. The operator who just
    # fixed a refused voucher must not be told their own bill is "already being
    # posted", so the next poster gets it immediately.
    assert (
        client.post(
            f"/companies/{COMPANY}/drafts/D-1/claim",
            json={"claim_token": "tok-B", "claimed_by": "browser B"},
        ).status_code
        == 200
    )


def test_only_the_holder_may_release_a_live_claim():
    client, store = _client()
    store.put(_ready_draft())
    client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-A", "claimed_by": "browser A"},
    )

    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim/release",
        json={"claim_token": "tok-B"},
    )
    # ⛔ REFUSED ON PURPOSE. If any caller could release any claim, "release" would
    # itself be a way to re-open the race while the holder is mid-POST.
    assert r.status_code == 409
    assert r.json()["detail"]["code"] == "draft_claimed_by_another"
    assert store.get(COMPANY, "D-1").claim_token == "tok-A"


def test_releasing_nothing_is_an_idempotent_success():
    client, store = _client()
    store.put(_ready_draft())
    # Never claimed, and a repeated release. Handing a client a failure it cannot act
    # on would only make it retry.
    assert (
        client.post(
            f"/companies/{COMPANY}/drafts/D-1/claim/release",
            json={"claim_token": "tok-A"},
        ).status_code
        == 200
    )
    client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-A", "claimed_by": "A"},
    )
    client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim/release", json={"claim_token": "tok-A"}
    )
    assert (
        client.post(
            f"/companies/{COMPANY}/drafts/D-1/claim/release",
            json={"claim_token": "tok-A"},
        ).status_code
        == 200
    )


def test_claiming_an_already_accepted_draft_is_refused_and_names_the_voucher():
    client, store = _client()
    store.put(_ready_draft())
    store.mark_accepted(COMPANY, "D-1", "voucher-11", "11")

    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-B", "claimed_by": "browser B"},
    )
    assert r.status_code == 409
    detail = r.json()["detail"]
    # TERMINAL, and it names the voucher — this is the tighter window the client's
    # find() cannot see, because the other poster can finish between find and claim.
    assert detail["code"] == "draft_already_accepted"
    assert detail["accepted_voucher_no"] == "11"


def test_confirming_clears_the_claim_because_an_accepted_draft_is_nobodys_wip():
    client, store = _client()
    store.put(_ready_draft())
    client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-A", "claimed_by": "browser A"},
    )

    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/accepted",
        json={"voucher_id": "voucher-11", "voucher_no": "11"},
    )
    assert r.status_code == 200
    body = r.json()
    assert body["status"] == "accepted"
    # ALL FOUR lease fields, not just the two the decision reads: a stale claimed_by
    # would otherwise appear in the very 409 detail an operator reads to find out what
    # happened to a bill.
    assert body["claim_token"] is None
    assert body["claimed_by"] is None
    assert body["claimed_at"] is None
    assert body["claim_expires_at"] is None


def test_a_confirm_is_never_refused_for_holding_a_lapsed_claim():
    client, store = _client()
    store.put(_ready_draft())
    store.claim_for_accept(
        COMPANY, "D-1", "tok-A", "browser A", at="2020-01-01T00:00:00+00:00"
    )

    # ⛔ THE VOUCHER ALREADY EXISTS — this call carries a real cored voucher id, so the
    # books have already changed and recording that is simply true. Refusing would
    # leave the draft postable and hand the NEXT poster a duplicate.
    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/accepted",
        json={"voucher_id": "voucher-11", "voucher_no": "11"},
    )
    assert r.status_code == 200
    assert r.json()["status"] == "accepted"


def test_a_processing_placeholder_cannot_be_claimed():
    client, store = _client()
    store.put(_ready_draft(status="processing"))
    r = client.post(
        f"/companies/{COMPANY}/drafts/D-1/claim",
        json={"claim_token": "tok-A", "claimed_by": "A"},
    )
    assert r.status_code == 409
    assert r.json()["detail"]["code"] == "draft_still_processing"


def test_claim_and_release_are_company_scoped_and_reject_a_payload():
    client, store = _client()
    store.put(_ready_draft())
    # Another company's draft is indistinguishable from a missing one (RLS-style).
    assert (
        client.post(
            "/companies/CO-OTHER/drafts/D-1/claim",
            json={"claim_token": "tok-A"},
        ).status_code
        == 404
    )
    assert (
        client.post(
            "/companies/CO-OTHER/drafts/D-1/claim/release",
            json={"claim_token": "tok-A"},
        ).status_code
        == 404
    )
    # ⛔ THERE IS NOTHING IN A CLAIM insightd COULD POST. `extra="forbid"` keeps it
    # that way: a claim token is a concurrency value, not a credential, and the route
    # must never grow a body it could act on.
    assert (
        client.post(
            f"/companies/{COMPANY}/drafts/D-1/claim",
            json={
                "claim_token": "tok-A",
                "postings": [{"ledger_id": "x", "dr_cr": "D", "amount": "1.0000"}],
            },
        ).status_code
        == 422
    )
    # And the TTL is the SERVER's: a client that could ask for a 24-hour lease could
    # strand a bill for a day, and one asking for 1 ms would defeat its own guard.
    assert (
        client.post(
            f"/companies/{COMPANY}/drafts/D-1/claim",
            json={"claim_token": "tok-A", "ttl_seconds": 86400},
        ).status_code
        == 422
    )
