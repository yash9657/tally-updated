"""Draft routes — the ONLY mutating endpoints write to the draft store, never to
cored. There is NO route that POST/PUT/PATCH/DELETEs a cored /vouchers path.

  POST /companies/{company_id}/drafts                 -> run pipeline, store + return draft
  GET  /companies/{company_id}/drafts                 -> slim list for the F9 bill inbox
  GET  /companies/{company_id}/drafts/{id}            -> hydrate the F9 screen (RLS-scoped)
  POST /companies/{company_id}/drafts/{id}/claim       -> RESERVE the draft for ONE
       poster, atomically, BEFORE any voucher exists. The loser gets 409 and must
       POST NOTHING. See ``claim_draft``.
  POST /companies/{company_id}/drafts/{id}/claim/release -> give a claim back when
       the voucher POST failed, so the bill is not stranded. See ``release_draft_claim``.
  POST /companies/{company_id}/drafts/{id}/accepted   -> RECORD that the human's
       client posted a cored voucher from this draft; moves it to the terminal
       ``accepted`` status. insightd still writes nothing to cored — see
       ``accept_draft`` and the DraftStatus comment in contracts/draft.py.

⛔ THE ORDER OF THOSE THREE IS THE FIX FOR A DUPLICATE THAT SURVIVED FOUR EARLIER
ONES. claim -> post -> confirm. Every earlier layer asked "has this draft been
accepted?" BEFORE the voucher was posted, when the answer was still truthfully "no";
two browsers raced on one draft therefore both posted, and only the second MARKING
was refused (409) — after the money had already moved. Reserving first is the only
ordering in which the loser can be stopped before it writes. See store.py.

⛔ EVERY ROUTE BELOW IS GUARDED BY ``require_proxy`` (register row D49, closed
2026-08-24). It is attached to the ROUTER, once, so a route added here cannot
forget it. What it checks, and what it does NOT:

  * the request carries cored's service credential, so it came through the proxy;
  * ``X-Insightd-Company`` — the company cored's PDP already authorized for the
    caller — equals the ``company_id`` in the path.

⚠️ SO THE PATH VALUE IS STILL WHAT SCOPES THE STORE AND THE RLS GUC, AND THAT IS
NOW SAFE for the first time: a caller may only name a company the proxy vouched
for. Before this, ``pg_store`` set the tenant GUC from a value the anonymous
caller typed, so RLS enforced whichever scope the caller chose. See api/authn.py.

⚠️ A CLAIM TOKEN IS STILL NOT A CREDENTIAL. It is an opaque concurrency value
whose only power is to release or renew the claim it already holds; the
authorisation is the guard above, and these routes would be exactly as safe if
the claim token were printed on a billboard.
"""

from __future__ import annotations

import base64
import datetime as _dt
import json
import uuid
from typing import Literal

from fastapi import APIRouter, BackgroundTasks, Depends, HTTPException
from pydantic import BaseModel, ConfigDict

from ..adapters.ocr_paddle import real_ocr_enabled
from ..contracts.cored_map import Money, VoucherDraftBody
from ..contracts.draft import (
    ConfidenceMap,
    DraftStatus,
    PurchaseVoucherDraft,
    RedactionReportSummary,
)
from ..contracts.pipeline import NormalizedDoc, RawUpload, TenantContext
from ..pipeline import Pipeline
from .authn import require_proxy
from .deps import get_draft_store, get_pipeline
from .store import DraftStore

# ⛔ THE GUARD IS ON THE ROUTER, NOT ON SIX DECORATORS. A route added to this file
# inherits it; a route that needed to opt OUT would have to say so in a diff.
# ⚠️ It is a route dependency and NOT middleware on purpose — routing must run
# first, so a path insightd does not serve still answers 404 rather than 401.
# test_api.py's ledger-write-endpoint proof depends on that ordering: it POSTs to
# a cored /vouchers path and asserts 404/405. ⚠️ Do not name that test in full
# here — test_hitl_invariant.py greps this tree for a write-client token that its
# own name contains, and cannot tell a citation from a client.
router = APIRouter(dependencies=[Depends(require_proxy)])


class CreateDraftRequest(BaseModel):
    """JSON body for POST /companies/{id}/drafts.

    Either a base64 document (content_b64 + content_type) for the real pipeline,
    or a benchmark/test fixture carried inline (the fakes key off it).
    """

    model_config = ConfigDict(extra="forbid")

    source: str = "upload"
    content_b64: str | None = None
    content_type: str = "application/pdf"
    filename: str = "upload.pdf"
    tenant: TenantContext
    # Optional inline fixture (deterministic fakes / benchmark harness path).
    fixture: dict | None = None


class DraftSummary(BaseModel):
    """Slim per-draft projection for the F9 bill-inbox list.

    DERIVED server-side from ``PurchaseVoucherDraft`` (the real contract has no
    top-level party / total / confidence):
      - ``party``        <- ``body.party`` or '' (body.party is Optional[str]).
      - ``grand_total``  <- ``derived_gst.invoice_total`` when derived_gst is
                            present else '0.0000' (Money STRING; derived_gst is
                            Optional and frequently None for low_confidence
                            drafts — treat '0.0000' as 'unknown total').
      - ``confidence``   <- ``confidence.overall`` (float).
    draft_id / company_id / created_at / source / status map directly.
    """

    model_config = ConfigDict(extra="forbid")

    draft_id: str
    company_id: str
    created_at: str
    source: Literal["upload", "whatsapp", "email"]
    status: DraftStatus
    party: str
    grand_total: Money  # fixed-4 decimal STRING; '0.0000' means unknown total
    confidence: float
    # ⛔ THE ACCEPTANCE LINK, IN THE LIST, ON PURPOSE. An accepted draft is NOT
    # removed from this collection: hiding the row would leave an operator who is
    # sure they uploaded a bill with nowhere to look, and an operator who cannot
    # see what happened re-does it — which is the duplicate this whole lifecycle
    # exists to stop. So the inbox can render "already posted as Purchase 3"
    # instead of silently dropping it, and instead of the row that WAS shown
    # after a successful accept: "Pune Fabricators · 21240.0000 · ready · 95%".
    # None for every draft that has not been accepted.
    accepted_voucher_no: str | None = None
    accepted_voucher_id: str | None = None


def _summarize(draft: PurchaseVoucherDraft) -> DraftSummary:
    return DraftSummary(
        draft_id=draft.draft_id,
        company_id=draft.company_id,
        created_at=draft.created_at,
        source=draft.source,
        status=draft.status,
        party=draft.body.party or "",
        grand_total=(
            draft.derived_gst.invoice_total
            if draft.derived_gst is not None
            else "0.0000"
        ),
        confidence=draft.confidence.overall,
        accepted_voucher_no=draft.accepted_voucher_no,
        accepted_voucher_id=draft.accepted_voucher_id,
    )


@router.post(
    "/companies/{company_id}/drafts",
    response_model=PurchaseVoucherDraft,
    status_code=201,
)
def create_draft(
    company_id: str,
    body: CreateDraftRequest,
    background: BackgroundTasks,
    pipeline: Pipeline = Depends(get_pipeline),
    store: DraftStore = Depends(get_draft_store),
) -> PurchaseVoucherDraft:
    ctx = body.tenant
    if ctx.company_id != company_id:
        raise HTTPException(
            status_code=400, detail="tenant.company_id must match the path company_id"
        )

    normalized: NormalizedDoc | None = None
    if body.fixture is not None:
        # Deterministic path: build the NormalizedDoc straight from the fixture so
        # the fakes can read its canned OCR/inference blocks.
        normalized = _normalized_from_fixture(body.fixture, body.source, ctx)
        upload = RawUpload(
            content_b64=base64.b64encode(b"fixture").decode("ascii"),
            content_type="application/pdf",
            filename=body.filename,
            source=body.source,  # type: ignore[arg-type]
        )
    else:
        if not body.content_b64:
            raise HTTPException(
                status_code=400, detail="either content_b64 or fixture is required"
            )
        upload = RawUpload(
            content_b64=body.content_b64,
            content_type=body.content_type,  # type: ignore[arg-type]
            filename=body.filename,
            source=body.source,  # type: ignore[arg-type]
        )

    # ENQUEUE-AND-POLL when the recogniser is the slow, real one (WS10 §8.6
    # item 5). The spike measured ~45 s for a 12 MP phone photo, which cannot
    # sit inside an HTTP request: the client would time out, retry, and OCR the
    # same bill twice.
    #
    # ONE RESPONSE SHAPE EITHER WAY. A placeholder draft is stored and returned
    # immediately in status "processing" and the pipeline runs in the
    # background; with the deterministic fakes the run finishes inside the
    # request and the draft returned is already terminal. So a client always
    # does the same thing — read the status, poll while it is "processing" — and
    # never has to know which mode the server is in.
    if real_ocr_enabled():
        placeholder = _processing_placeholder(ctx, body.source, body.filename)
        store.put(placeholder)
        background.add_task(_run_and_store, pipeline, store, upload, ctx, normalized, placeholder.draft_id)
        return placeholder

    draft = pipeline.run(upload, ctx, normalized=normalized)
    store.put(draft)
    return draft


def _run_and_store(
    pipeline: Pipeline,
    store: DraftStore,
    upload: RawUpload,
    ctx,
    normalized: NormalizedDoc | None,
    draft_id: str,
) -> None:
    """Run the pipeline off-request and replace the placeholder.

    A FAILURE MUST NOT LEAVE THE DRAFT STUCK IN "processing" — a spinner that
    never resolves is the worst outcome for a human-in-the-loop queue, because
    nobody can tell it apart from slow. The placeholder is replaced by a manual
    draft carrying the reason instead.
    """
    try:
        draft = pipeline.run(upload, ctx, normalized=normalized)
        draft = draft.model_copy(update={"draft_id": draft_id})
        store.put(draft)
    except Exception as exc:  # noqa: BLE001 - the reason must reach the operator
        failed = _processing_placeholder(ctx, upload.source, upload.filename)
        store.put(
            failed.model_copy(
                update={
                    "draft_id": draft_id,
                    "status": "low_confidence_manual",
                    "unresolved": [f"processing failed: {exc}"],
                }
            )
        )


@router.get(
    "/companies/{company_id}/drafts",
    response_model=list[DraftSummary],
)
def list_drafts(
    company_id: str,
    store: DraftStore = Depends(get_draft_store),
) -> list[DraftSummary]:
    # RLS-style: an unknown company simply yields an empty collection (never a
    # 404 on the collection — that would leak existence).
    return [_summarize(d) for d in store.list_for_company(company_id)]


@router.get(
    "/companies/{company_id}/drafts/{draft_id}",
    response_model=PurchaseVoucherDraft,
)
def get_draft(
    company_id: str,
    draft_id: str,
    store: DraftStore = Depends(get_draft_store),
) -> PurchaseVoucherDraft:
    draft = store.get(company_id, draft_id)
    if draft is None:
        # RLS-style: a draft from another company_id is indistinguishable from
        # a non-existent one.
        raise HTTPException(status_code=404, detail="draft not found")
    return draft


class ClaimDraftRequest(BaseModel):
    """Who is about to post this draft, and under what token.

    Carries a concurrency token and a display label and NOTHING else — no body, no
    postings, no amounts, exactly like RecordAcceptRequest. There is nothing in it
    insightd could post even if it wanted to, so the HITL boundary is untouched.

    ⚠️ NO CLIENT-SETTABLE TTL, ON PURPOSE. The lease length is the server's
    (``store.CLAIM_TTL_SECONDS``): a client that could ask for a 24-hour claim could
    strand a bill for a day, and one that could ask for a 1 ms claim would defeat the
    guard it is asking for. Tests drive the store directly to exercise expiry.
    """

    model_config = ConfigDict(extra="forbid")

    # Opaque, generated per PAGE by the client (see draftAccept.ts). Per-page and
    # not per-profile because two TABS of one browser are two racers.
    claim_token: str
    # Display only, so a refused operator can be told who/what holds it. Never
    # trusted, never used for authorisation: the authorisation is require_proxy,
    # and this string is a label the losing racer is shown.
    claimed_by: str = ""


class ReleaseClaimRequest(BaseModel):
    """Give back the claim this token holds. Only the holder may release."""

    model_config = ConfigDict(extra="forbid")

    claim_token: str


@router.post(
    "/companies/{company_id}/drafts/{draft_id}/claim",
    response_model=PurchaseVoucherDraft,
)
def claim_draft(
    company_id: str,
    draft_id: str,
    body: ClaimDraftRequest,
    store: DraftStore = Depends(get_draft_store),
) -> PurchaseVoucherDraft:
    """RESERVE this draft for one poster, atomically, BEFORE any voucher exists.

    ⛔⛔ THIS ROUTE IS THE FIFTH DOOR CLOSED, AND IT IS AN ORDERING CHANGE RATHER
    THAN A FIFTH CHECK. Rounds 1–3 built four layers (a session memo, a durable
    localStorage memo, a day-book lookup, and the terminal ``accepted`` state below).
    A browser re-drive then raced TWO SEPARATE BROWSERS — separate localStorage, no
    shared session memo, on a draft with no invoice number and no date, so the day
    book had nothing to match on — and got TWO REAL VOUCHERS:

        BROWSER A: Saved / No: 11  POST …/vouchers/purchase 201  POST …/accepted 200
        BROWSER B: Saved / No: 12  POST …/vouchers/purchase 201  POST …/accepted 409

    27000.0000 + 4860.0000 IGST, twice. The compare-and-set worked perfectly; it just
    ran after both POSTs. Both browsers asked "is this draft accepted?" while neither
    had posted, both were truthfully told "no", and the loser only found out it had
    lost once its voucher was already in the books.

    So the poster now RESERVES first: whoever wins this call posts, and whoever loses
    is told 409 and MUST NOT POST A VOUCHER AT ALL. Losing here costs nothing, because
    the frontend claims before it creates ledger masters too.

    Answers:
      200 -> the caller holds the claim and may post. Idempotent for the SAME
             ``claim_token`` (a retry renews the lease rather than being refused).
      404 -> unknown draft, or another company's (indistinguishable, RLS-style).
      409 ``draft_being_posted``     -> someone else holds a LIVE claim. The detail
             carries who and when it lapses, so the operator is told the bill is
             already being posted rather than a bare "conflict".
      409 ``draft_already_accepted`` -> the draft is terminal; it IS a voucher. Same
             detail shape as the accepted route, with the voucher link.
      409 ``draft_still_processing`` -> an empty placeholder body; nothing to post.
    """
    result = store.claim_for_accept(
        company_id, draft_id, body.claim_token, body.claimed_by
    )
    current = result.draft
    if result.outcome == "not_found" or current is None:
        raise HTTPException(status_code=404, detail="draft not found")
    if result.outcome == "claimed_elsewhere":
        raise HTTPException(
            status_code=409,
            detail={
                "code": "draft_being_posted",
                "message": (
                    "this bill is already being posted"
                    + (f" by {current.claimed_by}" if current.claimed_by else "")
                    + (
                        f" (since {current.claimed_at})"
                        if current.claimed_at
                        else ""
                    )
                    + ". No voucher has been posted from this attempt. If the other "
                    "attempt fails or is abandoned the claim lapses at "
                    f"{current.claim_expires_at} and the bill can be posted again."
                ),
                "draft_id": draft_id,
                "claimed_by": current.claimed_by,
                "claimed_at": current.claimed_at,
                "claim_expires_at": current.claim_expires_at,
            },
        )
    if result.outcome == "already_accepted":
        raise HTTPException(status_code=409, detail=_already_accepted_detail(draft_id, current))
    if result.outcome == "not_postable":
        raise HTTPException(status_code=409, detail=_still_processing_detail(draft_id))
    return current  # outcome == "claimed"


@router.post(
    "/companies/{company_id}/drafts/{draft_id}/claim/release",
    response_model=PurchaseVoucherDraft,
)
def release_draft_claim(
    company_id: str,
    draft_id: str,
    body: ReleaseClaimRequest,
    store: DraftStore = Depends(get_draft_store),
) -> PurchaseVoucherDraft:
    """Give a claim back because the voucher POST did not happen.

    ⛔⛔ THIS ROUTE IS THE CONTROL THAT STOPS THE CLAIM BECOMING A WORSE DEFECT THAN
    THE DUPLICATE. Claiming before posting means a claim can outlive the attempt that
    took it, and a draft stranded claimed forever is a bill an operator can NEVER post
    with no way to release it. Three things prevent that: this route (called by the
    client the moment a POST fails), the lease expiry in ``store.CLAIM_TTL_SECONDS``
    (which covers a browser that died and could not call this), and the fact that the
    SAME token may simply re-claim.

    Answers:
      200 -> released, or there was nothing to release (idempotent: a claim that had
             already lapsed, or a repeated release, is a success — handing a client a
             failure it cannot act on would only make it retry).
      404 -> unknown draft, or another company's.
      409 ``draft_claimed_by_another`` -> a LIVE claim held by a different token. ⛔
             Refused on purpose: if any caller could release any claim, "release"
             would itself be a way to re-open the race while the holder is mid-POST.
      409 ``draft_already_accepted``   -> terminal; a real voucher exists. There is
             nothing to release and pretending otherwise would invite a second post.
    """
    result = store.release_claim(company_id, draft_id, body.claim_token)
    current = result.draft
    if result.outcome == "not_found" or current is None:
        raise HTTPException(status_code=404, detail="draft not found")
    if result.outcome == "claimed_elsewhere":
        raise HTTPException(
            status_code=409,
            detail={
                "code": "draft_claimed_by_another",
                "message": (
                    "this claim is held by another poster and cannot be released from "
                    "here; it lapses on its own at "
                    f"{current.claim_expires_at}"
                ),
                "draft_id": draft_id,
                "claim_expires_at": current.claim_expires_at,
            },
        )
    if result.outcome == "already_accepted":
        raise HTTPException(status_code=409, detail=_already_accepted_detail(draft_id, current))
    return current  # "released" or "not_claimed" — both are a success


def _already_accepted_detail(draft_id: str, current: PurchaseVoucherDraft) -> dict:
    """The 409 body for "this draft IS a voucher already".

    ONE copy, shared by the claim and the accept routes, because the operator-facing
    sentence has to name the SAME voucher whichever door they arrived through — and
    two copies of it would eventually name two different things.
    """
    return {
        "code": "draft_already_accepted",
        "message": (
            "this draft has already been accepted as cored voucher "
            f"{current.accepted_voucher_no or current.accepted_voucher_id}"
        ),
        "draft_id": draft_id,
        "accepted_voucher_id": current.accepted_voucher_id,
        "accepted_voucher_no": current.accepted_voucher_no,
        "accepted_at": current.accepted_at,
    }


def _still_processing_detail(draft_id: str) -> dict:
    """The 409 body for "this draft has an empty placeholder body"."""
    return {
        "code": "draft_still_processing",
        "message": (
            "this draft is still processing and carries no postings, so no voucher "
            "can have been posted from it"
        ),
        "draft_id": draft_id,
    }


class RecordAcceptRequest(BaseModel):
    """What the human's client tells insightd AFTER cored accepted the voucher.

    It carries the cored voucher's identity and NOTHING else — no body, no
    postings, no amounts. insightd is being told an outcome, not asked to compute
    one, and giving this route anything it could act on would start eroding the
    HITL boundary.
    """

    model_config = ConfigDict(extra="forbid")

    voucher_id: str
    # cored allocates the purchase voucher number inside its write transaction;
    # a client that could not read it back may still record the id, so this is
    # optional and DISPLAY-ONLY. The id is the identity.
    voucher_no: str = ""


@router.post(
    "/companies/{company_id}/drafts/{draft_id}/accepted",
    response_model=PurchaseVoucherDraft,
)
def accept_draft(
    company_id: str,
    draft_id: str,
    body: RecordAcceptRequest,
    store: DraftStore = Depends(get_draft_store),
) -> PurchaseVoucherDraft:
    """RECORD that a cored voucher was posted from this draft; terminal state.

    ⛔ STEP 3 OF THREE, AND NO LONGER THE GUARD. It is now preceded by
    ``claim_draft`` (step 1), because this call — however atomic — necessarily runs
    AFTER the voucher exists, and two browsers racing therefore both posted before
    either could be refused here. Confirming clears the claim as well as setting the
    terminal state: an accepted draft is nobody's work in progress. A confirm is NEVER
    refused for holding a lapsed claim — it carries a real voucher id, so the books
    have already changed and recording that is simply true; refusing would leave the
    draft postable and hand the next poster a duplicate.

    ⛔⛔ THIS ROUTE EXISTS BECAUSE ITS ABSENCE WAS A MONEY DEFECT, AND A BROWSER
    RE-DRIVE PROVED THE CLIENT-SIDE FIX INSUFFICIENT. A draft that had already
    become a purchase voucher stayed ``ready`` and stayed in the F9 Bill Inbox, so
    re-opening it and pressing Ctrl+A again posted the same supplier bill twice —
    a duplicated payable and duplicated input tax. The frontend answered that with
    two client-side guards (a page-lifetime memo, and a day-book lookup keyed on
    supplier invoice number + date). Driven in a browser, that fix held for a bill
    WITH a number and failed for one without: the day book has nothing to match on
    and a page RELOAD empties the memo, and two real vouchers (3 and 4, both
    5000.0000 + 900.0000 IGST) came out of one bill. A guard whose only copy of the
    truth is in the client is defeated by reloading the client.
    (See frontend/src/screens/vouchers/draftAccept.ts.)

    ⛔ insightd STILL WRITES NOTHING TO CORED. It does not post, it does not
    verify, it does not re-derive: the human's client did the write and this call
    reports the result. That is why the transition is a plain state change plus a
    link, and why the request body cannot carry a voucher payload.

    Answers:
      200 -> recorded; the returned draft is terminal (``status: "accepted"``) and
             carries ``accepted_voucher_id`` / ``accepted_voucher_no``.
      404 -> unknown draft, or another company's (indistinguishable, RLS-style).
      409 -> ALREADY accepted. The detail carries the voucher the FIRST acceptance
             recorded so the caller can tell the operator exactly which voucher
             this bill already is. The recorded link is NOT overwritten — a second
             accept must never look like a success, because an operator told
             "Saved" twice cannot tell a blocked duplicate from a written one.
      409 -> still ``processing``: the placeholder body is empty by construction,
             so nothing can have posted it and recording an acceptance would be a
             lie about the books.
    """
    result = store.mark_accepted(
        company_id, draft_id, body.voucher_id, body.voucher_no
    )
    if result.outcome == "not_found":
        raise HTTPException(status_code=404, detail="draft not found")
    current = result.draft
    if current is None:
        # Only "not_found" carries no draft, and it is handled above. Guarding with
        # a plain `if` rather than `assert` on purpose: an assert vanishes under
        # `python -O`, and this branch decides what reaches an operator.
        raise HTTPException(status_code=404, detail="draft not found")
    if result.outcome == "already_accepted":
        raise HTTPException(
            status_code=409, detail=_already_accepted_detail(draft_id, current)
        )
    if result.outcome == "not_postable":
        raise HTTPException(status_code=409, detail=_still_processing_detail(draft_id))
    return current  # outcome == "recorded": the UPDATED, terminal draft


def _processing_placeholder(ctx, source: str, filename: str) -> PurchaseVoucherDraft:
    """A draft that exists, is visibly NOT DONE, and cannot be mistaken for one.

    Its body is empty and its status is "processing", so the Bill Inbox can list
    it (an upload that vanished until OCR finished would look like a failure)
    while nothing downstream can post it: the F9 accept path needs real ledger
    ids and there are none.
    """
    return PurchaseVoucherDraft(
        draft_id=str(uuid.uuid4()),
        company_id=ctx.company_id,
        created_at=_dt.datetime.now(_dt.timezone.utc).isoformat(),
        source=source,  # type: ignore[arg-type]
        status="processing",
        body=VoucherDraftBody(voucher_class="purchase", postings=[]),
        confidence=ConfidenceMap(
            header={}, line_items=[], overall_header=0.0, overall_line_items=0.0, overall=0.0
        ),
        redaction=RedactionReportSummary(any_redacted=False, counts_by_type={}, total=0),
        unresolved=[f"reading {filename or 'the uploaded document'}…"],
    )


def _normalized_from_fixture(
    fixture: dict, source: str, ctx: TenantContext
) -> NormalizedDoc:
    pages = [
        {
            "png_b64": p.get("png_b64", ""),
            "width": p.get("width", 1),
            "height": p.get("height", 1),
            "dpi": p.get("dpi", 200),
        }
        for p in fixture.get("input", {}).get("pages", [{"png_b64": ""}])
    ]
    return NormalizedDoc(
        pages=pages,
        page_count=len(pages),
        source=source,  # type: ignore[arg-type]
        tenant=ctx,
        source_meta={"fixture_json": json.dumps(fixture)},
        fixture_id=fixture.get("id"),
    )
