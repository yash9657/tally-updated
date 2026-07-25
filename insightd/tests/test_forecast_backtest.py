"""WS5b MERGE GATE: the Holt-Winters forecast must EARN its complexity.

On >=3 seeded synthetic seasonal fixtures a rolling-origin backtest must show its
MAPE beating the naive last-value baseline by >=20% — otherwise this test FAILS
(the forecast is not worth shipping). Plus a determinism pin. Skipped when
statsmodels (requirements-forecast.txt) is not installed, mirroring how the PG
tests skip without psycopg.
"""

from __future__ import annotations

import pytest

pytest.importorskip("statsmodels")

from insightd.forecast.backtest import (  # noqa: E402
    DEFAULT_MARGIN,
    beats_baseline,
    mape,
    rolling_origin_mape,
)
from insightd.forecast.fixtures import gate_fixtures  # noqa: E402
from insightd.forecast.model import holt_winters_forecast  # noqa: E402


def test_backtest_beats_naive_on_every_fixture():
    fixtures = gate_fixtures()
    assert len(fixtures) >= 3, "the gate needs >=3 seasonal fixtures"
    for name, series, seasonal_periods in fixtures:
        hw_mape, naive_mape = rolling_origin_mape(
            series, seasonal_periods=seasonal_periods, horizon=1
        )
        assert beats_baseline(hw_mape, naive_mape, DEFAULT_MARGIN), (
            f"{name}: Holt-Winters MAPE {hw_mape:.4f} does not beat naive "
            f"{naive_mape:.4f} by >= {DEFAULT_MARGIN:.0%}"
        )


def test_forecast_is_deterministic():
    """Same series in -> byte-identical forecast out (no RNG, no random restarts)."""
    _, series, sp = gate_fixtures()[0]
    a = holt_winters_forecast(series, 4, seasonal_periods=sp)
    b = holt_winters_forecast(series, 4, seasonal_periods=sp)
    assert a == b


def test_beats_baseline_predicate_is_non_vacuous():
    # A model that IS the naive baseline (0% improvement) must NOT pass the gate.
    assert beats_baseline(0.80, 1.0, 0.20) is True   # exactly 20% better -> pass
    assert beats_baseline(0.81, 1.0, 0.20) is False  # 19% better -> fail
    assert beats_baseline(0.0, 0.0, 0.20) is False   # a flat series justifies no model


def test_mape_guards_zero_actuals():
    assert mape([0.0, 0.0], [0.0, 0.0]) == 0.0
    # a zero actual uses denominator 1.0, so it contributes the absolute error
    assert mape([0.0], [1.0]) == 1.0
