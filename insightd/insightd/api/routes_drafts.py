"""Draft routes — the ONLY mutating endpoint writes to the draft store, never to
cored. There is NO route that POST/PUT/PATCH/DELETEs a cored /vouchers path.

  POST /companies/{company_id}/drafts        -> run pipeline, store + return draft
  GET  /companies/{company_id}/drafts        -> slim list for the F9 bill inbox
  GET  /companies/{company_id}/drafts/{id}   -> hydrate the F9 screen (RLS-scoped)
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
from .deps import get_draft_store, get_pipeline
from .store import DraftStore

router = APIRouter()


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
