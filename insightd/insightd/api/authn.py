"""THE CREDENTIAL. insightd is reached through cored and by nothing else.

⛔⛔ WHAT A TOKEN IS CHECKED AGAINST, WHICH IS THE QUESTION ``/healthz``'s pinned
test demanded an answer to before its value could change (register row D49):

  * ``X-Insightd-Service-Token`` is compared, in constant time, against
    ``INSIGHTD_SERVICE_TOKEN`` — a credential shared by exactly two processes,
    cored and this one. It is NOT a user identity and proves nothing about a
    person; it proves the request came through the proxy.
  * ``X-Insightd-Company`` is the company **cored's PDP already authorized** for
    the caller who sent it, and it must equal the ``company_id`` in the path.
    THAT is what replaces "caller-supplied company_id in the path": the path
    value is still what scopes the store and the RLS GUC, but a request may only
    name a company the proxy vouched for.

So the authority is cored's own middleware — a verified JWT, a live (non-revoked)
session, membership in that company, and the (action, resource) rule for it. This
file duplicates NONE of that, and deliberately: a second copy of cored's PDP in
another language is the shape this repo has been burned by, and a signature would
prove only WHO, never WHICH COMPANY.

⛔ WHY THIS IS NOT THE "SHARED SECRET" ``api/app.py`` REFUSED. That refusal was
about a secret the BROWSER holds — which is not a credential, because anything
that can read the page can read it, and it still would not say which company the
caller may read. Neither clause applies here: this secret exists only in two
server processes and never reaches a client, and the company question is answered
by the PDP before the request is forwarded. The browser now authenticates to
cored, with its own bearer, exactly as it does for every other route.

⛔ FAIL-CLOSED, IN ALL THREE DIRECTIONS:
  * No ``INSIGHTD_SERVICE_TOKEN`` configured => every guarded route answers 503.
    An unconfigured process REFUSES; it never serves open. (cored takes the
    harder line and refuses to boot — insightd cannot, because ``/healthz`` must
    stay answerable for a deployer to find out WHY it is refusing.)
  * A missing or wrong token => 401.
  * A missing company header, or one that disagrees with the path => 403.

⚠️ IT IS A ROUTE DEPENDENCY AND NOT MIDDLEWARE, ON PURPOSE. Routing must happen
FIRST, so that a path this service does not serve still answers 404 rather than
401. ``tests/test_hitl_invariant.py`` and test_api.py's ledger-write-endpoint proof
establish that insightd has no such route by POSTing to one and asserting
404/405; a global 401-before-routing would turn the product's proof that the HITL
boundary exists into a statement about headers. ⚠️ That test is deliberately NOT
named in full anywhere in this package: test_hitl_invariant.py greps the tree for
a write-client token that the test's own name contains.
"""

from __future__ import annotations

import hmac

from fastapi import Header, HTTPException, Path

from ..config import get_settings

# The two headers cored sets. Named here so the proxy on the other side and the
# test harness that stands in for it cannot drift from this file.
SERVICE_TOKEN_HEADER = "X-Insightd-Service-Token"
COMPANY_HEADER = "X-Insightd-Company"


def _require_service_token(presented: str | None) -> None:
    configured = (get_settings().service_token or "").strip()
    if not configured:
        # NOT 401. A deployer who forgot the token must be told the SERVICE is
        # unconfigured, not that their credential was rejected — those send you
        # to two different files.
        raise HTTPException(
            status_code=503,
            detail=(
                "insightd has no INSIGHTD_SERVICE_TOKEN configured, so it refuses every "
                "company-scoped request. It is reached through cored's proxy, which must "
                "present the same token; set INSIGHTD_SERVICE_TOKEN on BOTH processes."
            ),
        )
    if not presented or not hmac.compare_digest(presented, configured):
        raise HTTPException(
            status_code=401,
            detail=(
                "missing or invalid service token: insightd is reached through cored's "
                f"proxy, which presents {SERVICE_TOKEN_HEADER}"
            ),
        )


def require_proxy(
    company_id: str = Path(...),
    x_insightd_service_token: str | None = Header(default=None),
    x_insightd_company: str | None = Header(default=None),
) -> str:
    """Guard every company-scoped route. Returns the authorized company id.

    The return value is the company the PROXY asserted, which by the check below
    is byte-identical to the path parameter — so a handler may keep reading the
    path value (they all do) without that being a caller-supplied scope any more.
    """
    _require_service_token(x_insightd_service_token)
    if not x_insightd_company:
        raise HTTPException(
            status_code=403,
            detail=(
                f"no authorized company: {COMPANY_HEADER} is set by cored's proxy after its "
                "PDP has authorized the caller for that company, and insightd will not scope "
                "a request by a company nobody vouched for"
            ),
        )
    if x_insightd_company != company_id:
        # 403 and not 400: this is "you may not read that", and the two values
        # disagreeing means the request did not come from the proxy that
        # authorized it. Never leak which of the two exists.
        raise HTTPException(
            status_code=403,
            detail="authorized company does not match the company in the path",
        )
    return company_id


def require_service_token(
    x_insightd_service_token: str | None = Header(default=None),
) -> None:
    """Guard a route that is NOT company-scoped (``/benchmark/last``).

    The accuracy report names no tenant, but it is this service's own
    ops/CI surface and there is no reason for it to be readable by anyone who can
    open a socket. Only the token applies — there is no company to assert.
    """
    _require_service_token(x_insightd_service_token)


__all__ = [
    "COMPANY_HEADER",
    "SERVICE_TOKEN_HEADER",
    "require_proxy",
    "require_service_token",
]
