"""Deterministic statistical cash-flow forecast (WS5b, wave 3).

Holt-Winters over a weekly net-cash series, backtest-gated (must beat the naive
last-value baseline by >=20% MAPE) and determinism-tested. READ-ONLY: this
package reads history (in production, cored's day-book over HTTP GET) and never
writes to the ledger — a pytest assertion pins that it imports no cored write
client.
"""

from .backtest import beats_baseline, mape, rolling_origin_mape
from .model import holt_winters_forecast, naive_forecast
from .service import forecast_cashflow, weekly_net_series

__all__ = [
    "beats_baseline",
    "mape",
    "rolling_origin_mape",
    "holt_winters_forecast",
    "naive_forecast",
    "forecast_cashflow",
    "weekly_net_series",
]
