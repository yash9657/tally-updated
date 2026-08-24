"""FastAPI app factory.

Endpoints — EIGHT, and the count has been wrong in writing three times, so it is
enumerated here rather than summarised:
  POST /companies/{id}/drafts                            (writes the DRAFT store)
  GET  /companies/{id}/drafts
  GET  /companies/{id}/drafts/{draft_id}
  POST /companies/{id}/drafts/{draft_id}/claim
  POST /companies/{id}/drafts/{draft_id}/claim/release
  POST /companies/{id}/drafts/{draft_id}/accepted
  GET  /healthz                          (active adapter set per stage)
  GET  /benchmark/last                   (read-only accuracy report)

There is NO route that writes to the cored ledger.

⛔ AND THERE IS NO STATISTICAL FORECAST HERE ANY MORE (register row D96, deleted
2026-08-24 by the user's decision). ``POST /companies/{id}/forecast/cashflow`` — a
Holt-Winters estimate over an injected history frame — was mounted here behind a
``try: import statsmodels`` and **no client ever called it**. It was deleted rather
than wired, and the reason is not "it was unused":

  cored's WS5a projection is explainable to an auditor BILL BY BILL — its own
  ``method`` string says *"deterministic; no statistics, no model"*. A second
  cash-forward number, produced by a different method and carrying no auditor
  story, invites exactly the confusion the Cash Flow screen's whole disclosure
  block exists to prevent. A forecast nobody can explain is worse than no forecast.

⚠️ SO IT NEEDS NO REPORT NAME AND NO ACL COUNTERWEIGHT — contrast D95/migration
0076, which existed precisely because the forecast plane's two cored reports ARE
reachable. There is nothing here left to deny.
⛔ ``tests/test_forecast_plane_is_gone.py`` is what keeps it gone. If you are about
to re-add a statistical forecast, that test is where the decision has to be argued.

⛔⛔ CORED IS THE ONLY DOOR (register row D49, closed 2026-08-24). Every
company-scoped route above is guarded by ``api/authn.require_proxy``, and
``/benchmark/last`` by the service token alone. The browser never talks to this
process: it calls ``/v1/companies/{id}/drafts...`` on cored with its own bearer,
cored verifies the JWT, checks the session is live, checks membership, evaluates
(Display|Create, "purchase") against the level's rules, and only then forwards
here with the authorized company in a header.

⚠️ WHAT CHANGED, PRECISELY. The path ``company_id`` is still what scopes the
store and the RLS GUC — that part is unchanged and did not need to change. What
changed is that a caller may only NAME a company somebody vouched for, so the GUC
is no longer set from a value an anonymous caller typed. Before this, RLS
faithfully enforced whichever scope the caller chose, which is why it was never a
second layer.

⚠️ AND WHY THIS IS NOT THE SHARED SECRET THIS DOCSTRING USED TO REFUSE. The
refusal stands as written — a secret the BROWSER holds is not a credential, and
it says nothing about which company the caller may read. Neither clause applies
to a credential held only by cored and this process, for a request whose company
cored's PDP has already decided. See api/authn.py, which is where that argument
lives in full.

⛔ THE AUTO-DOCS ARE GONE, AND THEY WERE FOUR MORE ANONYMOUS ROUTES. ``/docs``,
``/redoc``, ``/openapi.json`` and ``/docs/oauth2-redirect`` all answered 200 and
published the entire schema — every draft field, every redaction-report shape —
and they are absent from ``openapi.paths``, which is why every count of this
service's exposure missed them. ``openapi_url=None`` unmounts all four in one
knob (FastAPI gates /docs and /redoc on it too). ``create_app().openapi()`` still
builds the schema in-process, which is what the tests and an operator use.
"""

from __future__ import annotations

from fastapi import Depends, FastAPI

from ..config import active_adapter_set, get_settings
from .authn import require_service_token
from .routes_drafts import router as drafts_router


def create_app() -> FastAPI:
    app = FastAPI(
        title="insightd",
        description=(
            "AI insight plane: document -> purchase-voucher DRAFT. Read-only data "
            "relationship with cored; no ledger write path. Reached through cored's "
            "draft proxy and by nothing else."
        ),
        version="0.1.0",
        # ⛔ THE FOUR AUTO-DOC ROUTES ARE NOT MOUNTED. See the module docstring:
        # they published the whole schema to anyone who could open a socket, they
        # never appear in `openapi.paths`, and no runtime client reads them.
        # openapi_url=None is the single knob that removes /openapi.json, /docs,
        # /docs/oauth2-redirect and /redoc, because FastAPI gates the other three
        # on it. tests/test_api.py pins all four at 404.
        openapi_url=None,
    )

    app.include_router(drafts_router)

    @app.get("/healthz")
    def healthz() -> dict:
        settings = get_settings()
        return {
            "status": "ok",
            "service": "insightd",
            "adapters": active_adapter_set(settings),
            "pii_boundary_strict": settings.pii_boundary_strict,
            # cored relationship is read-only; we report it but hold no write client
            "cored_relationship": "read-only",
            # ⛔ THE POSTURE, ON THE WIRE, BECAUSE A SURFACE WHOSE POSTURE
            # CANNOT BE READ GETS ASSUMED TO BE THE SAFE ONE. These two keys
            # spent the whole of D49's open life saying "none" and
            # "caller-supplied company_id in the path" — deliberately, as a
            # disclosure — with a test pinning those exact values so that whoever
            # closed the row would find it RED and have to argue the change.
            #
            # ⛔ THE DAY CAME (2026-08-24), AND WHAT A TOKEN IS CHECKED AGAINST
            # IS THE ANSWER THAT TEST DEMANDED: the service credential cored's
            # draft proxy presents, and the company cored's PDP authorized for
            # the caller — carried in X-Insightd-Company and required to equal
            # the path's company_id. It is NOT a user identity and NOT verified
            # crypto: insightd holds no keys and re-implements none of cored's
            # PDP. The pin moved WITH the mechanism, in one commit, which is the
            # only way it was ever allowed to move.
            "authentication": "cored-proxy service token",
            "tenant_scope": "company authorized by cored's PDP (X-Insightd-Company == path company_id)",
            # Unauthenticated on purpose, and the ONLY route that is: a liveness
            # probe a deployer cannot read is a deployer who cannot find out why
            # the service is refusing. It names no tenant and no draft.
            "unauthenticated_routes": ["/healthz"],
        }

    # ⚠️ THE ACCURACY REPORT TAKES THE TOKEN TOO. It names no tenant, so there is
    # no company to assert and require_proxy would have nothing to check — but it
    # is this service's own ops surface and there is no reason for it to be
    # readable by anyone who can open a socket.
    @app.get("/benchmark/last", dependencies=[Depends(require_service_token)])
    def benchmark_last() -> dict:
        # Read-only, ops/CI visibility only. Lazily import so the API module has
        # no benchmark dependency at load time.
        from ..benchmark.report import last_report

        return last_report()

    return app


app = create_app()
