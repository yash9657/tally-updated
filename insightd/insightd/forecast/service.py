"""Forecast orchestration: build a weekly net-cash series and forecast it.

The forecast is a STATISTICAL ESTIMATE, not a posted ledger amount, so it is
carried as rounded floats (never claimed to be paisa-exact). It is deliberately
kept separate from cored's deterministic WS5a projection (open-bill due-date
bucketing): this module extrapolates the observed net-cash *trend + seasonality*,
which is a different, complementary question.
"""

from __future__ import annotations

from datetime import date

from .model import holt_winters_forecast, naive_forecast


def weekly_net_series(entries: list[tuple[str, float]]) -> list[float]:
    """Aggregate (ISO-date, signed-net-amount) rows into a CONTIGUOUS weekly
    series (7-day buckets from the earliest date). Empty weeks are 0.0 so the
    series has no gaps — Holt-Winters needs an evenly-spaced index.
    """
    if not entries:
        return []
    parsed = sorted((date.fromisoformat(d), float(a)) for d, a in entries)
    start = parsed[0][0]
    last = parsed[-1][0]
    n_weeks = ((last - start).days // 7) + 1
    series = [0.0] * n_weeks
    for d, amount in parsed:
        idx = (d - start).days // 7
        series[idx] += amount
    return series


def forecast_cashflow(
    series: list[float],
    weeks: int = 4,
    seasonal_periods: int = 4,
) -> dict:
    """Forecast the next `weeks` of weekly net cash, alongside the naive baseline
    (so the caller can see the model's lift). Returns a JSON-ready dict."""
    if weeks <= 0:
        raise ValueError("forecast_cashflow: weeks must be > 0")
    hw = holt_winters_forecast(series, weeks, seasonal_periods=seasonal_periods)
    nv = naive_forecast(series, weeks) if series else [0.0] * weeks
    return {
        "history_weeks": len(series),
        "forecast_weeks": weeks,
        "seasonal_periods": seasonal_periods,
        "method": "holt-winters-additive",
        "estimate": True,  # a statistical estimate, NOT a paisa-exact ledger value
        "forecast": [round(x, 2) for x in hw],
        "naive_baseline": [round(x, 2) for x in nv],
    }
