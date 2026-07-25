"""Deterministic statistical cash-flow forecast (WS5b).

A thin, DETERMINISTIC wrapper over statsmodels Holt-Winters (additive trend +
optional additive seasonality) over a weekly net-cash-movement series, plus the
naive last-value baseline it must beat. There is NO LLM here and NO cored write
path — this module imports only numpy/statsmodels and stdlib.

Determinism: statsmodels' `fit()` is deterministic given a fixed series and
`initialization_method="estimated"` (no random restarts). We never seed an RNG
because none is used; a determinism test pins that two fits of the same series
return byte-identical forecasts.
"""

from __future__ import annotations

from statsmodels.tsa.holtwinters import ExponentialSmoothing


def naive_forecast(series: list[float], periods: int) -> list[float]:
    """The last-value baseline: repeat the final observation `periods` times.

    This is the floor Holt-Winters must beat by >=20% MAPE in the backtest gate.
    """
    if not series:
        raise ValueError("naive_forecast: series is empty")
    if periods <= 0:
        raise ValueError("naive_forecast: periods must be > 0")
    return [float(series[-1])] * periods


def holt_winters_forecast(
    series: list[float],
    periods: int,
    seasonal_periods: int = 4,
) -> list[float]:
    """Additive Holt-Winters forecast for the next `periods` weeks.

    Uses additive trend + additive seasonality when the series is long enough for
    seasonality to be identifiable (>= 2 full seasons); otherwise it falls back to
    additive trend only. Deterministic (no random restarts).
    """
    if periods <= 0:
        raise ValueError("holt_winters_forecast: periods must be > 0")
    n = len(series)
    if n < 3:
        # Too short for a trend model — degrade to the naive baseline rather than
        # letting statsmodels raise.
        return naive_forecast(series, periods)

    use_seasonal = seasonal_periods >= 2 and n >= 2 * seasonal_periods
    if use_seasonal:
        model = ExponentialSmoothing(
            series,
            trend="add",
            seasonal="add",
            seasonal_periods=seasonal_periods,
            initialization_method="estimated",
        )
    else:
        model = ExponentialSmoothing(
            series,
            trend="add",
            seasonal=None,
            initialization_method="estimated",
        )
    fit = model.fit()
    forecast = fit.forecast(periods)
    return [float(x) for x in forecast]
