"""Rolling-origin backtest + the MERGE GATE (WS5b).

The forecast is only allowed to ship if it EARNS its complexity: on held-out data
its error must beat the naive last-value baseline by a margin. This module runs a
walk-forward (rolling-origin) backtest and exposes the pass/fail predicate the
pytest gate asserts. It is deterministic (no RNG).
"""

from __future__ import annotations

from .model import holt_winters_forecast, naive_forecast

# The margin by which Holt-Winters must beat the naive baseline's MAPE. A forecast
# that cannot clear this on the synthetic seasonal fixtures FAILS the gate.
DEFAULT_MARGIN = 0.20


def mape(actual: list[float], predicted: list[float]) -> float:
    """Mean absolute percentage error. Zero actuals use a denominator of 1.0 so a
    genuinely-zero target does not blow the ratio up to infinity (a documented,
    standard sMAPE-adjacent guard)."""
    if len(actual) != len(predicted) or not actual:
        raise ValueError("mape: actual/predicted must be equal, non-empty length")
    total = 0.0
    for a, p in zip(actual, predicted):
        denom = abs(a) if abs(a) > 1e-9 else 1.0
        total += abs(a - p) / denom
    return total / len(actual)


def rolling_origin_mape(
    series: list[float],
    seasonal_periods: int = 4,
    min_train: int | None = None,
    horizon: int = 1,
) -> tuple[float, float]:
    """Walk-forward backtest. From each origin t in [min_train, len-horizon], fit
    on series[:t] and forecast `horizon` steps, comparing to the true next values.
    Returns (holt_winters_mape, naive_mape) aggregated over all origins.

    min_train defaults to 2 full seasons (so the seasonal model is exercised).
    """
    n = len(series)
    if min_train is None:
        min_train = max(3, 2 * seasonal_periods)
    if n < min_train + horizon:
        raise ValueError(
            f"rolling_origin_mape: need >= {min_train + horizon} points, got {n}"
        )

    hw_actual: list[float] = []
    hw_pred: list[float] = []
    nv_actual: list[float] = []
    nv_pred: list[float] = []
    for t in range(min_train, n - horizon + 1):
        train = series[:t]
        truth = series[t : t + horizon]
        hw = holt_winters_forecast(train, horizon, seasonal_periods=seasonal_periods)
        nv = naive_forecast(train, horizon)
        hw_actual.extend(truth)
        hw_pred.extend(hw)
        nv_actual.extend(truth)
        nv_pred.extend(nv)

    return mape(hw_actual, hw_pred), mape(nv_actual, nv_pred)


def beats_baseline(hw_mape: float, naive_mape: float, margin: float = DEFAULT_MARGIN) -> bool:
    """True when Holt-Winters' MAPE is at least `margin` fraction lower than the
    naive baseline's (e.g. margin 0.20 => hw <= 0.80 * naive). If the naive MAPE is
    ~0 (a flat series), no model can beat it by a margin, and this returns False —
    correctly, a flat series does not justify a seasonal model."""
    if naive_mape <= 1e-9:
        return False
    return hw_mape <= (1.0 - margin) * naive_mape
