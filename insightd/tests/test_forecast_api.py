"""Route tests for POST /companies/{id}/forecast/cashflow (WS5b).

⛔ MONEY ON THIS WIRE IS A FIXED-4 DECIMAL STRING, NEVER A JSON FLOAT, and this
file is where that is pinned. The route used to declare ``weekly_net_cash:
list[float]`` and answer ``round(x, 2)`` floats — a paisa-loss defect that had
simply not been triggered yet because no client calls the route. Every other money
value in this system is a fixed-4 string; wiring this one as it stood would have
put a float back into a money path.

Skipped without statsmodels (the route is only mounted when it is installed).
"""

from __future__ import annotations

import pytest

pytest.importorskip("statsmodels")

from fastapi.testclient import TestClient  # noqa: E402

from insightd.api.app import create_app  # noqa: E402
from insightd.forecast.fixtures import gate_fixtures  # noqa: E402
from insightd.forecast.service import money  # noqa: E402
from decimal import Decimal  # noqa: E402


def _client():
    return TestClient(create_app())


def _as_money(series) -> list[str]:
    """The gate fixtures are a MODELLING series (floats, by nature). The WIRE is
    money, so the caller converts — which is exactly what a real client does."""
    return [money(Decimal(repr(x))) for x in series]


def test_forecast_route_returns_estimate_with_baseline():
    _, series, sp = gate_fixtures()[0]
    client = _client()
    r = client.post(
        "/companies/co-1/forecast/cashflow",
        json={"weekly_net_cash": _as_money(series), "weeks": 4, "seasonal_periods": sp},
    )
    assert r.status_code == 200, r.text
    body = r.json()
    assert body["company_id"] == "co-1"
    assert body["estimate"] is True  # a statistical estimate, not a ledger value
    assert len(body["forecast"]) == 4
    assert len(body["naive_baseline"]) == 4
    assert body["history_weeks"] == len(series)


def test_every_money_value_on_the_wire_is_a_fixed4_STRING():
    # ⛔ THE MONEY GATE. A float here is a defect even when the number looks right.
    _, series, sp = gate_fixtures()[0]
    r = _client().post(
        "/companies/co-1/forecast/cashflow",
        json={"weekly_net_cash": _as_money(series), "weeks": 4, "seasonal_periods": sp},
    )
    assert r.status_code == 200, r.text
    body = r.json()
    for key in ("forecast", "naive_baseline"):
        for v in body[key]:
            assert isinstance(v, str), f"{key} carries a JSON float: {v!r}"
            # fixed-4: exactly four decimal places, parseable as a Decimal.
            assert v.split(".")[1:] and len(v.split(".")[1]) == 4, v
            Decimal(v)


def test_a_float_amount_is_REFUSED_rather_than_silently_narrowed():
    # Non-vacuous: without this, a contract that still accepted floats would pass
    # every test above (pydantic would happily coerce 1000.0 -> "1000.0").
    r = _client().post(
        "/companies/co-1/forecast/cashflow",
        json={"weekly_net_cash": [1000.0, 2000.0, 3000.0], "weeks": 2},
    )
    assert r.status_code == 422, (
        "a bare JSON float was accepted as money — that is the paisa loss this "
        f"contract exists to stop (got {r.status_code}: {r.text})"
    )


def test_forecast_route_aggregates_daybook_rows():
    # Three weeks of day-book rows -> a 3-week series -> a forecast. The amounts
    # are real posted movements, so they are summed in Decimal.
    daybook = [
        ["2025-01-01", "1000.0000"],
        ["2025-01-03", "200.0000"],   # same week as the first row
        ["2025-01-09", "1500.0000"],  # week 2
        ["2025-01-16", "900.0000"],   # week 3
    ]
    r = _client().post(
        "/companies/co-1/forecast/cashflow",
        json={"daybook": daybook, "weeks": 2, "seasonal_periods": 2},
    )
    assert r.status_code == 200, r.text
    assert r.json()["history_weeks"] == 3


def test_the_daybook_weekly_aggregation_is_paisa_exact():
    # Three amounts whose float sum is NOT their decimal sum. 0.1 + 0.2 in binary
    # floating point is 0.30000000000000004; in Decimal it is exactly 0.3.
    from insightd.forecast.service import weekly_net_series

    series = weekly_net_series(
        [("2025-01-01", "0.1000"), ("2025-01-02", "0.2000"), ("2025-01-03", "0.3000")]
    )
    assert series == ["0.6000"], (
        f"weekly aggregation lost paisa: {series} — the sum of real posted "
        "movements must be Decimal, never float"
    )


def test_forecast_route_rejects_too_short_history():
    r = _client().post(
        "/companies/co-1/forecast/cashflow",
        json={"weekly_net_cash": ["1.0000", "2.0000"]},
    )
    assert r.status_code == 422


def test_forecast_route_requires_some_history():
    r = _client().post("/companies/co-1/forecast/cashflow", json={"weeks": 4})
    assert r.status_code == 400


def test_the_response_STATES_that_it_is_an_estimate_and_on_what_basis():
    # The disclosure cored's own deterministic projection lacks (D23). A projected
    # number that does not say it is projected reads as a ledger balance.
    _, series, sp = gate_fixtures()[0]
    body = _client().post(
        "/companies/co-1/forecast/cashflow",
        json={"weekly_net_cash": _as_money(series), "weeks": 4, "seasonal_periods": sp},
    ).json()
    assert body["estimate"] is True
    assert body["method"] == "holt-winters-additive"
    assert "assumes" in body["basis"].lower()
    assert "not because they are" in body["basis"].lower()
