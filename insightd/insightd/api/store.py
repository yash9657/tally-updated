"""The draft store — append-only proposals, never ledger rows.

Drafts are per-company scoped (RLS-style): a draft_id from another company_id is
invisible (returns None). In-memory by default; a Postgres backend can implement
the same tiny interface. The store NEVER calls cored and holds no write client.
"""

from __future__ import annotations

import datetime as _dt
import threading
from typing import Literal, NamedTuple

from ..contracts.draft import PurchaseVoucherDraft

# The outcome of a mark-accepted attempt. It is an ENUM and not a bool because the
# route has to tell three different HTTP answers apart, and because the caller of
# the route (the F9 accept path) needs "already accepted" to be distinguishable
# from "recorded" — that distinction IS the duplicate guard.
#
#   "recorded"         -> this call performed the ready -> accepted transition.
#   "already_accepted" -> a previous call did; the returned draft carries the
#                         voucher link the FIRST acceptance recorded, and the
#                         caller must NOT read this as success.
#   "not_postable"     -> the draft is still "processing": its body is empty by
#                         construction, so nothing can have posted it, and
#                         recording an acceptance against it would be a lie.
#   "not_found"        -> unknown draft, or a draft belonging to another company
#                         (indistinguishable, on purpose — see get()).
MarkOutcome = Literal["recorded", "already_accepted", "not_postable", "not_found"]


class MarkAcceptedResult(NamedTuple):
    outcome: MarkOutcome
    draft: PurchaseVoucherDraft | None


def now_iso() -> str:
    """UTC now, ISO-8601. Shared with pg_store so both backends stamp
    ``accepted_at`` identically."""
    return _dt.datetime.now(_dt.timezone.utc).isoformat()


# ===========================================================================
# THE POSTING CLAIM — RESERVE BEFORE POSTING, NOT CHECK BEFORE POSTING
#
# ⛔⛔ WHY THIS EXISTS, AFTER FOUR EARLIER FIXES THAT ALL HELD. ``mark_accepted``
# below is a real compare-and-set and it did its job: raced from TWO SEPARATE
# BROWSERS on one numberless draft, the second marking was refused with 409. AND
# THE BOOKS STILL GOT THE BILL TWICE — vouchers 11 and 12, 27000.0000 + 4860.0000
# IGST each — because BOTH browsers asked "is this draft accepted?" BEFORE either
# had posted, both were truthfully told "no", both posted, and only THEN did one
# lose the CAS. A fifth check at that same moment would lose the same race; the
# ordering is the defect.
#
# So the lifecycle is now three steps and the CAS moved to the FIRST one:
#   1. CLAIM   ``claim_for_accept``  — atomic; the loser must post NOTHING
#   2. POST    the voucher, in cored, by the human's client
#   3. CONFIRM ``mark_accepted``     — the terminal 'accepted' state (unchanged)
#
# ⛔ AND THE CRASH WINDOW IS HANDLED ON PURPOSE, because it is the reason this
# ordering is not free. A claim that never expired would strand a bill forever if
# the browser died between 1 and 2: an operator with a bill they can never post and
# no way to release it is WORSE than the duplicate. Three deliberate releases:
#   (a) the client RELEASES the claim when the POST fails (``release_claim``) —
#       this covers the overwhelmingly common failure (the server refused the
#       voucher) with no wait at all;
#   (b) the claim EXPIRES after CLAIM_TTL_SECONDS — this covers a hard crash /
#       killed tab, at the cost of a bounded wait;
#   (c) the SAME ``claim_token`` may re-claim (renewing the lease) — a retry from
#       the page that already holds the claim is never locked out by itself.
# The residue this leaves is stated in full on ``claim_for_accept``.
#
# ⛔ THE DECISION IS A PURE FUNCTION, SHARED BY BOTH STORES, AND THAT IS
# STRUCTURAL. In-memory and Postgres differ ONLY in how they take the lock (a
# threading.Lock vs SELECT … FOR UPDATE in one transaction). Duplicating this state
# machine in each would let the two drift, and a test of one would stop being
# evidence about the other — which is exactly how a "fixed" guard grows a new door.
# ===========================================================================

# How long a claim survives without being confirmed or released. It must comfortably
# exceed one cored voucher POST (tens of ms locally, and the frontend's own fetch
# gives up long before this) and it must be short enough that a crashed browser does
# not block the operator for long. 120 s satisfies both.
CLAIM_TTL_SECONDS = 120

#   "claimed"          -> THIS caller holds the claim and may post. Also returned
#                         when the same claim_token renews its own live claim.
#   "claimed_elsewhere"-> a DIFFERENT, still-live claim holds it. ⛔ The caller must
#                         not post a voucher; the draft is already being posted.
#   "already_accepted" -> terminal; the voucher exists. Carries the link.
#   "not_postable"     -> still "processing": an empty body, nothing to post.
#   "not_found"        -> unknown draft, or another company's (indistinguishable).
ClaimOutcome = Literal[
    "claimed", "claimed_elsewhere", "already_accepted", "not_postable", "not_found"
]

#   "released"         -> the claim this token held is gone; the draft is postable.
#   "not_claimed"      -> there was no live claim. Idempotent success, NOT an error:
#                         a client that releases twice, or whose claim had already
#                         expired, must not be handed a failure it cannot act on.
#   "claimed_elsewhere"-> a live claim held by ANOTHER token. ⛔ REFUSED, and that
#                         refusal is load-bearing: if any caller could release any
#                         claim, "release" would itself be a way to re-open the race
#                         while the holder is mid-POST.
#   "already_accepted" -> terminal; there is a real voucher. Nothing to release.
#   "not_found"        -> unknown draft, or another company's.
ReleaseOutcome = Literal[
    "released", "not_claimed", "claimed_elsewhere", "already_accepted", "not_found"
]


class ClaimResult(NamedTuple):
    outcome: ClaimOutcome
    draft: PurchaseVoucherDraft | None


class ReleaseResult(NamedTuple):
    outcome: ReleaseOutcome
    draft: PurchaseVoucherDraft | None


def _parse_iso(ts: str | None) -> _dt.datetime | None:
    """Parse one of OUR OWN ISO-8601 stamps, or None when it is unusable.

    Every value this sees was written by ``now_iso()`` (UTC, offset-aware), but a
    hand-edited or migrated jsonb row can carry anything at all, and an unparseable
    expiry must degrade to "no live claim" rather than raise: a crash here would be
    on the path of the human's only write. A naive stamp is read as UTC so it
    compares against ``now_iso()`` instead of raising on offset-mixing.
    """
    if not ts:
        return None
    try:
        parsed = _dt.datetime.fromisoformat(ts)
    except ValueError:
        return None
    if parsed.tzinfo is None:
        return parsed.replace(tzinfo=_dt.timezone.utc)
    return parsed


def claim_is_live(draft: PurchaseVoucherDraft, at: str | None = None) -> bool:
    """True when this draft is claimed and the claim has NOT lapsed.

    Both halves matter. No token = never claimed. A token whose ``claim_expires_at``
    is in the past = a lapsed lease, which MUST read as unclaimed or a crashed
    browser would strand the bill permanently (see the module comment). A token with
    no parseable expiry is treated as LAPSED for the same reason — the failure mode of
    "unclaimable forever" is worse than the failure mode of "claimable again".
    """
    if not draft.claim_token:
        return False
    expires = _parse_iso(draft.claim_expires_at)
    if expires is None:
        return False
    now = _parse_iso(at) or _dt.datetime.now(_dt.timezone.utc)
    return now < expires


def decide_claim(
    current: PurchaseVoucherDraft,
    claim_token: str,
    claimed_by: str = "",
    at: str | None = None,
    ttl_seconds: int = CLAIM_TTL_SECONDS,
) -> ClaimResult:
    """PURE: what claiming this draft does. The ONE copy of the rule.

    Called by both stores while the row is locked, so the read it does and the write
    the caller performs are one atomic step.
    """
    if current.status == "accepted":
        return ClaimResult("already_accepted", current)
    if current.status == "processing":
        return ClaimResult("not_postable", current)
    if claim_is_live(current, at) and current.claim_token != claim_token:
        return ClaimResult("claimed_elsewhere", current)
    # Fresh, lapsed, released — or a RENEWAL by the token that already holds it.
    stamp = at or now_iso()
    started = _parse_iso(stamp) or _dt.datetime.now(_dt.timezone.utc)
    expires = started + _dt.timedelta(seconds=ttl_seconds)
    return ClaimResult(
        "claimed",
        current.model_copy(
            update={
                "claim_token": claim_token,
                "claimed_by": claimed_by,
                # A renewal keeps the ORIGINAL claimed_at (that is when this poster
                # started work; the operator being refused wants to know how long it
                # has been going, not when the last keep-alive landed) and moves only
                # the expiry.
                "claimed_at": current.claimed_at
                if current.claim_token == claim_token and current.claimed_at
                else stamp,
                "claim_expires_at": expires.isoformat(),
            }
        ),
    )


def decide_release(
    current: PurchaseVoucherDraft, claim_token: str, at: str | None = None
) -> ReleaseResult:
    """PURE: what releasing this claim does. The ONE copy of the rule.

    ⛔ THIS IS THE CONTROL THAT STOPS THE FIX BECOMING A WORSE BUG. Without it a
    voucher POST that the server refuses (a back-dated cutoff, an unbalanced entry)
    would leave the draft claimed until the TTL lapsed, so the operator who just fixed
    the problem would be told their own bill is "already being posted".
    """
    if current.status == "accepted":
        return ReleaseResult("already_accepted", current)
    if not claim_is_live(current, at):
        # Nothing to do, and NOT an error: the claim may have lapsed on its own, or
        # this may be a retried release. Report the draft as it stands.
        return ReleaseResult("not_claimed", current)
    if current.claim_token != claim_token:
        return ReleaseResult("claimed_elsewhere", current)
    return ReleaseResult(
        "released",
        current.model_copy(
            update={
                "claim_token": None,
                "claimed_by": None,
                "claimed_at": None,
                "claim_expires_at": None,
            }
        ),
    )


def decide_accept(
    current: PurchaseVoucherDraft,
    voucher_id: str,
    voucher_no: str = "",
    at: str | None = None,
) -> MarkAcceptedResult:
    """PURE: what CONFIRMING an acceptance does. The ONE copy of the rule.

    ⛔ A CONFIRM IS NOT REFUSED FOR HOLDING A LAPSED CLAIM, AND THAT IS DELIBERATE.
    This call carries a REAL cored voucher id: the books have already changed, and
    recording that is simply true. Refusing it because the lease had expired in the
    meantime would leave the draft postable and hand the NEXT poster a duplicate —
    the very outcome. So the terminal state always wins, and the claim fields are
    cleared with it (an accepted draft is nobody's work in progress).
    """
    if current.status == "accepted":
        return MarkAcceptedResult("already_accepted", current)
    if current.status == "processing":
        return MarkAcceptedResult("not_postable", current)
    return MarkAcceptedResult(
        "recorded",
        current.model_copy(
            update={
                "status": "accepted",
                "accepted_voucher_id": voucher_id,
                "accepted_voucher_no": voucher_no,
                "accepted_at": at or now_iso(),
                # ALL FOUR claim fields, not just the two the lease is decided on.
                # ``claim_is_live`` only reads claim_token/claim_expires_at, so
                # leaving claimed_by/claimed_at behind changed no decision — but it
                # left an accepted draft reporting "being posted by F9 …" in the very
                # 409 detail an operator reads to find out what happened to a bill.
                "claim_token": None,
                "claimed_by": None,
                "claimed_at": None,
                "claim_expires_at": None,
            }
        ),
    )


class DraftStore:
    """In-memory, per-company-scoped, append-only.

    ⚠️ IN-MEMORY MEANS THE ACCEPTANCE RECORD DIES WITH THE PROCESS, and that is a
    weaker guarantee than it looks: restart insightd and every accepted draft is
    gone from this store entirely (so is every other draft, which is why the
    inbox looks empty rather than lying). The DURABLE deployment is
    ``pg_store.PGDraftStore``, selected by INSIGHTD_DATABASE_URL — see
    ``deps.get_draft_store``. Both implement ``claim_for_accept`` /
    ``release_claim`` / ``mark_accepted`` by calling the SAME pure decision
    functions above, so the two backends cannot drift apart in semantics; only the
    PG one survives a restart.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        # company_id -> draft_id -> draft
        self._by_company: dict[str, dict[str, PurchaseVoucherDraft]] = {}

    def put(self, draft: PurchaseVoucherDraft) -> None:
        with self._lock:
            self._by_company.setdefault(draft.company_id, {})[draft.draft_id] = draft

    def get(self, company_id: str, draft_id: str) -> PurchaseVoucherDraft | None:
        with self._lock:
            return self._by_company.get(company_id, {}).get(draft_id)

    def list_for_company(self, company_id: str) -> list[PurchaseVoucherDraft]:
        with self._lock:
            return list(self._by_company.get(company_id, {}).values())

    def claim_for_accept(
        self,
        company_id: str,
        draft_id: str,
        claim_token: str,
        claimed_by: str = "",
        at: str | None = None,
        ttl_seconds: int = CLAIM_TTL_SECONDS,
    ) -> ClaimResult:
        """STEP 1 OF THREE: reserve this draft BEFORE its voucher is posted.

        ⛔ THIS IS THE COMPARE-AND-SET THAT USED TO RUN TOO LATE. See the module
        comment: two browsers raced on one draft, both were correctly told the draft
        was not yet accepted, and both posted. Whoever loses HERE has posted nothing
        yet, so the refusal costs the books nothing at all — not even an orphan ledger
        master, because the frontend claims before it resolves ledgers.

        ⚠️ THE RESIDUE, STATED. A claim is a LEASE, not a lock, so:
          * a browser that dies between the claim and the POST blocks the bill for at
            most ``ttl_seconds`` (the alternative — a permanent claim — is a bill that
            can NEVER be posted, which is worse);
          * if a POST somehow outlives the lease, another poster may take the draft
            over and duplicate it. That window is why the TTL is far larger than any
            voucher POST, and why a failed POST releases the claim immediately instead
            of waiting for the lease to lapse.
        """
        with self._lock:
            drafts = self._by_company.get(company_id)
            current = None if drafts is None else drafts.get(draft_id)
            if current is None:
                return ClaimResult("not_found", None)
            result = decide_claim(current, claim_token, claimed_by, at, ttl_seconds)
            if result.outcome == "claimed" and result.draft is not None:
                drafts[draft_id] = result.draft
            return result

    def release_claim(
        self,
        company_id: str,
        draft_id: str,
        claim_token: str,
        at: str | None = None,
    ) -> ReleaseResult:
        """Give back a claim whose POST never happened (or failed).

        ⛔ THE ANTI-STRANDING CONTROL. Without it, every refused voucher POST would
        leave the operator locked out of their own bill until the lease lapsed.
        """
        with self._lock:
            drafts = self._by_company.get(company_id)
            current = None if drafts is None else drafts.get(draft_id)
            if current is None:
                return ReleaseResult("not_found", None)
            result = decide_release(current, claim_token, at)
            if result.outcome == "released" and result.draft is not None:
                drafts[draft_id] = result.draft
            return result

    def mark_accepted(
        self,
        company_id: str,
        draft_id: str,
        voucher_id: str,
        voucher_no: str = "",
        at: str | None = None,
    ) -> MarkAcceptedResult:
        """STEP 3 OF THREE: COMPARE-AND-SET the draft into terminal ``accepted``.

        ⛔ THE READ AND THE WRITE ARE ONE ATOMIC STEP, AND THAT IS THE WHOLE
        POINT. A route that did ``get()``, inspected the status, then ``put()``
        would let two concurrent accepts of the same draft both see "ready" and
        both report success. The draft routes are sync ``def``s, so Starlette runs
        them in a THREADPOOL: two accepts really can be in here at the same time.

        ⛔ BUT IT IS NO LONGER THE ONLY GUARD, BECAUSE IT CANNOT BE. Driven from two
        browsers, this CAS refused the second MARKING and the second VOUCHER was
        already posted — the answer it gives is about a write that has already
        happened. ``claim_for_accept`` is the guard that runs in time; this one
        records the outcome and closes the draft.

        Never overwrites an existing acceptance: the FIRST voucher recorded is the
        one that stays, and a second attempt is told about it (with the link) so
        the operator can be shown what actually happened.
        """
        with self._lock:
            drafts = self._by_company.get(company_id)
            current = None if drafts is None else drafts.get(draft_id)
            if current is None:
                return MarkAcceptedResult("not_found", None)
            result = decide_accept(current, voucher_id, voucher_no, at)
            if result.outcome == "recorded" and result.draft is not None:
                drafts[draft_id] = result.draft
            return result


_store: DraftStore | None = None


def get_store() -> DraftStore:
    global _store
    if _store is None:
        _store = DraftStore()
    return _store
