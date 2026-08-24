"""FastAPI app factory.

Endpoints:
  POST /companies/{id}/drafts            (the ONLY mutating route; writes drafts)
  GET  /companies/{id}/drafts/{draft_id}
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

⛔⛔ AND THERE IS NO AUTHENTICATION ON ANY OF THEM. Every route above is scoped by
the caller-supplied ``company_id`` in the PATH and by nothing else, so any caller
who can reach this process can list and fully hydrate any tenant's drafts —
supplier names, invoice numbers, dates, posting ledger names, amounts and GST
state codes. The Postgres RLS in ``pg_store`` does not help: it pins the row
filter to a GUC that this process sets FROM THAT SAME PATH PARAMETER, so it
enforces whichever scope the caller chose.

⚠️ ``/healthz`` says so, in ``authentication`` below. That is a DISCLOSURE and
NOT A FIX — it exists for the same reason the cash-flow projection now carries
``estimate: true``: a surface whose posture cannot be read off the wire gets
assumed to be the safe one. Closing it is a design decision about how insightd is
REACHED (validate a cored-minted bearer here, or make cored the only door and
never expose this process to a browser), not a token bolted onto these routes —
see the D49 recommendation. Do not "fix" this by adding a shared secret: a secret
the browser holds is not a credential, and it would still not say WHICH company
the caller may read.
"""

from __future__ import annotations

from fastapi import FastAPI

from ..config import active_adapter_set, get_settings
from .routes_drafts import router as drafts_router


def create_app() -> FastAPI:
    app = FastAPI(
        title="insightd",
        description=(
            "AI insight plane: document -> purchase-voucher DRAFT. Read-only data "
            "relationship with cored; no ledger write path."
        ),
        version="0.1.0",
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
            # ⛔ THE POSTURE THAT WAS ONLY EVER WRITTEN IN A DOCSTRING. Two of the
            # three lines above report a SAFE property of this process; a reader
            # who got both would reasonably assume the third. It is stated here so
            # that "insightd is unauthenticated" is a fact on the wire — checkable
            # by a deployer, by the seed script, and by whoever eventually closes
            # D49 — rather than a sentence in a file nobody reads before exposing
            # a port. It is a constant on purpose: the day it can say anything
            # else, that is because the decision was made and implemented, and the
            # test that pins this value is where the change has to be argued.
            "authentication": "none",
            "tenant_scope": "caller-supplied company_id in the path",
        }

    @app.get("/benchmark/last")
    def benchmark_last() -> dict:
        # Read-only, ops/CI visibility only. Lazily import so the API module has
        # no benchmark dependency at load time.
        from ..benchmark.report import last_report

        return last_report()

    return app


app = create_app()
