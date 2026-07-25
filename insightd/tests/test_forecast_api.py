"""Route tests for POST /companies/{id}/forecast/cashflow (WS5b).

Skipped without statsmodels (the route is only mounted when it is installed).
"""

from __future__ import annotations

import pytest

pytest.importorskip("statsmodels")

from fastapi.testclient import TestClient  # noqa: E402

from insightd.api.app import create_app  # noqa: E402
from insightd.forecast.fixtures import gate_fixtures  # noqa: E402


def _client():
    return TestClient(create_app())


def test_forecast_route_returns_estimate_with_baseline():
    _, series, sp = gate_fixtures()[0]
    client = _client()
    r = client.post(
        "/companies/co-1/forecast/cashflow",
        json={"weekly_net_cash": series, "weeks": 4, "seasonal_periods": sp},
    )
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["company_id"] == "co-1"
    assert body["estimate"] is True  # a statistical estimate, not a ledger value
    assert len(body["forecast"]) == 4
    assert len(body["naive_baseline"]) == 4
    assert body["history_weeks"] == len(series)


def test_forecast_route_aggregates_daybook_rows():
    # Three weeks of day-book rows -> a 3-week series -> a forecast.
    daybook = [
        ["2025-01-01", 1000.0],
        ["2025-01-03", 200.0],   # same week as the first row
        ["2025-01-09", 1500.0],  # week 2
        ["2025-01-16", 900.0],   # week 3
    ]
    r = _client().post(
        "/companies/co-1/forecast/cashflow",
        json={"daybook": daybook, "weeks": 2, "seasonal_periods": 2},
    )
    assert r.status_code == 200, r.text
    assert r.json()["history_weeks"] == 3


def test_forecast_route_rejects_too_short_history():
    r = _client().post(
        "/companies/co-1/forecast/cashflow",
        json={"weekly_net_cash": [1.0, 2.0]},
    )
    assert r.status_code == 422


def test_forecast_route_requires_some_history():
    r = _client().post("/companies/co-1/forecast/cashflow", json={"weeks": 4})
    assert r.status_code == 400
