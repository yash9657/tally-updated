"""FastAPI app factory.

Endpoints:
  POST /companies/{id}/drafts            (the ONLY mutating route; writes drafts)
  GET  /companies/{id}/drafts/{draft_id}
  GET  /healthz                          (active adapter set per stage)
  GET  /benchmark/last                   (read-only accuracy report)

There is NO route that writes to the cored ledger.
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
        }

    @app.get("/benchmark/last")
    def benchmark_last() -> dict:
        # Read-only, ops/CI visibility only. Lazily import so the API module has
        # no benchmark dependency at load time.
        from ..benchmark.report import last_report

        return last_report()

    return app


app = create_app()
