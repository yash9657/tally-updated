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
response carries estimate=true and names its method + basis) — it is complementary
to cored's deterministic WS5a open-bill projection, not a substitute.

⛔⛔ MONEY ON THIS WIRE IS A FIXED-4 DECIMAL STRING, NEVER A JSON FLOAT.
``weekly_net_cash`` and ``daybook``'s amounts used to be ``float``, and the
response answered rounded floats. Every other money value in this system is a
fixed-4 string (``contracts/cored_map.Money``, cored's own wire, the frontend's
``types.ts``), for the reason that a float silently loses paisa — and a JSON float
loses it twice, once here and once in the browser's double-precision parse. This
route is not reachable from the client yet; the contract was fixed FIRST so that
wiring it cannot reintroduce the loss. See forecast/service.py for where the
floats are confined (statsmodels is a floating-point library; the conversion
happens at one edge, and the day-book aggregation is Decimal throughout).

⚠️ STILL NOT REACHABLE FROM THE KEYBOARD, and that is a separate open item: no
client calls this route. Fixing the money type does not make it a door.
"""

from __future__ import annotations

from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, ConfigDict

from ..contracts.cored_map import Money
from ..forecast.service import forecast_cashflow, weekly_net_series

router = APIRouter()


class ForecastRequest(BaseModel):
    model_config = ConfigDict(extra="forbid")

    weeks: int = 4
    seasonal_periods: int = 4
    # Provide EITHER a ready weekly series OR raw day-book rows (iso_date, net).
    # ⛔ Money is a fixed-4 decimal STRING on both. See the module docstring.
    weekly_net_cash: list[Money] | None = None
    daybook: list[tuple[str, Money]] | None = None


@router.post("/companies/{company_id}/forecast/cashflow")
def forecast_cashflow_route(company_id: str, body: ForecastRequest) -> dict:
    if body.weeks <= 0:
        raise HTTPException(status_code=400, detail="weeks must be > 0")
    if body.weekly_net_cash is not None:
        series = list(body.weekly_net_cash)
    elif body.daybook is not None:
        try:
            series = weekly_net_series(body.daybook)
        except ValueError as exc:
            # A malformed amount is the caller's error and must be NAMED. Coercing
            # it to zero would produce a forecast nobody could explain.
            raise HTTPException(status_code=422, detail=str(exc)) from exc
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
    try:
        result = forecast_cashflow(
            series, weeks=body.weeks, seasonal_periods=body.seasonal_periods
        )
    except ValueError as exc:
        raise HTTPException(status_code=422, detail=str(exc)) from exc
    result["company_id"] = company_id
    return result
