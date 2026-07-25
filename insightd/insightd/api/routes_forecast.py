"""Forecast route (WS5b, wave 3) — a READ-ONLY statistical cash-flow estimate.

  POST /companies/{company_id}/forecast/cashflow

The caller supplies the observed history (either a ready weekly net-cash series or
raw day-book rows to aggregate); insightd forecasts the next N weeks with
Holt-Winters and returns it alongside the naive baseline. insightd holds NO cored
client on this path — the history is injected — so the read-only boundary is
trivially strong (there is nothing to write with). Wiring a live, service-token
cored day-book fetch is a disclosed follow-up; the plan explicitly permits the
injected-frame input.

The forecast is a STATISTICAL ESTIMATE, never a paisa-exact ledger value (the
response carries estimate=true) — it is complementary to cored's deterministic
WS5a open-bill projection, not a substitute.
"""

from __future__ import annotations

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, ConfigDict

from ..forecast.service import forecast_cashflow, weekly_net_series

router = APIRouter()


class ForecastRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    weeks: int = 4
    seasonal_periods: int = 4
    # Provide EITHER a ready weekly series OR raw day-book rows (iso_date, net).
    weekly_net_cash: list[float] | None = None
    daybook: list[tuple[str, float]] | None = None


@router.post("/companies/{company_id}/forecast/cashflow")
def forecast_cashflow_route(company_id: str, body: ForecastRequest) -> dict:
    if body.weeks <= 0:
        raise HTTPException(status_code=400, detail="weeks must be > 0")
    if body.weekly_net_cash is not None:
        series = list(body.weekly_net_cash)
    elif body.daybook is not None:
        series = weekly_net_series(body.daybook)
    else:
        raise HTTPException(
            status_code=400,
            detail="provide either weekly_net_cash or daybook",
        )
    if len(series) < 3:
        raise HTTPException(
            status_code=422,
            detail="need at least 3 weeks of history to forecast",
        )
    result = forecast_cashflow(series, weeks=body.weeks, seasonal_periods=body.seasonal_periods)
    result["company_id"] = company_id
    return result
