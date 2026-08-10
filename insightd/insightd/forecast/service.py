"""Forecast orchestration: build a weekly net-cash series and forecast it.

The forecast is a STATISTICAL ESTIMATE, not a posted ledger amount — the response
carries ``estimate: true`` and names its ``method``, which is the disclosure
cored's own deterministic WS5a open-bill projection lacks. It is deliberately kept
separate from that projection: this module extrapolates the observed net-cash
*trend + seasonality*, which is a different, complementary question.

⛔⛔ MONEY CROSSES THIS MODULE'S EDGES AS A FIXED-4 DECIMAL STRING, NEVER A FLOAT.
This module used to take ``list[float]`` and answer ``round(x, 2)`` floats, and
that was a defect waiting for a client: every other money value in this system —
cored's wire, ``contracts/cored_map.Money``, ``adapters/tax``, the frontend's
``types.ts`` — is a fixed-4 STRING precisely because a float silently loses paisa,
and a JSON float loses it twice (once in Python, once in the browser's
double-precision parse). Wiring the route as it stood would have reintroduced
paisa loss into a product that has been careful about it everywhere else, so the
contract was fixed BEFORE anything could depend on it.

⛔ AND THE FLOATS DO NOT GO AWAY — THEY GET CONFINED. statsmodels is a
floating-point library and there is no Decimal Holt-Winters. So the conversion
happens in exactly one place, ``_to_float`` / ``_to_money`` below, at the edge of
the model: strings in, floats through statsmodels, fixed-4 strings back out. The
AGGREGATION of day-book rows into weeks — which is ordinary addition of real
posted amounts, not statistics — is done in Decimal and never touches a float,
because summing 52 weeks of floats is where the paisa would actually be lost.

⚠️ A quantised estimate is still an estimate. Fixed-4 here means "the same
representation as every other money value in this system so it can be handled
uniformly", NOT "paisa-exact". ``estimate: true`` is what says which it is.
"""

from __future__ import annotations

from datetime import date
from decimal import ROUND_HALF_UP, Decimal, InvalidOperation

from .model import holt_winters_forecast, naive_forecast

#: A fixed-4 decimal value transported as a string (e.g. "1234.5600"), matching
#: ``contracts.cored_map.Money`` and the frontend's ``Money``.
Money = str

_Q = Decimal("0.0001")


def money(d: Decimal) -> Money:
    """Quantise a Decimal to the system's fixed-4 string form (half-up)."""
    return f"{d.quantize(_Q, rounding=ROUND_HALF_UP):.4f}"


def parse_money(value: Money) -> Decimal:
    """Parse a fixed-4 money string. Raises ValueError on anything else.

    Strict on purpose: silently coercing a malformed amount to 0 would turn a
    typo into a forecast nobody could explain.
    """
    try:
        return Decimal(str(value))
    except (InvalidOperation, ValueError) as exc:
        raise ValueError(f"not a decimal money value: {value!r}") from exc


def _to_float(amounts: list[Decimal]) -> list[float]:
    """⛔ THE ONLY PLACE MONEY BECOMES A FLOAT. See the module docstring."""
    return [float(a) for a in amounts]


def _to_money(values: list[float]) -> list[Money]:
    """⛔ THE ONLY PLACE A MODEL OUTPUT BECOMES MONEY AGAIN."""
    return [money(Decimal(repr(v))) for v in values]


def weekly_net_series(entries: list[tuple[str, Money]]) -> list[Money]:
    """Aggregate (ISO-date, signed-net-amount) rows into a CONTIGUOUS weekly
    series (7-day buckets from the earliest date). Empty weeks are "0.0000" so the
    series has no gaps — Holt-Winters needs an evenly-spaced index.

    The amounts are real posted movements, so they are summed in DECIMAL. This is
    the addition a float would actually cost paisa on.
    """
    if not entries:
        return []
    parsed = sorted((date.fromisoformat(d), parse_money(a)) for d, a in entries)
    start = parsed[0][0]
    last = parsed[-1][0]
    n_weeks = ((last - start).days // 7) + 1
    series = [Decimal(0)] * n_weeks
    for d, amount in parsed:
        idx = (d - start).days // 7
        series[idx] += amount
    return [money(a) for a in series]


def forecast_cashflow(
    series: list[Money],
    weeks: int = 4,
    seasonal_periods: int = 4,
) -> dict:
    """Forecast the next `weeks` of weekly net cash, alongside the naive baseline
    (so the caller can see the model's lift). Returns a JSON-ready dict whose
    money is fixed-4 STRINGS."""
    if weeks <= 0:
        raise ValueError("forecast_cashflow: weeks must be > 0")
    observed = _to_float([parse_money(x) for x in series])
    hw = holt_winters_forecast(observed, weeks, seasonal_periods=seasonal_periods)
    nv = naive_forecast(observed, weeks) if observed else [0.0] * weeks
    return {
        "history_weeks": len(series),
        "forecast_weeks": weeks,
        "seasonal_periods": seasonal_periods,
        "method": "holt-winters-additive",
        # ⛔ THE DISCLOSURE. A projected number that does not say it is projected
        # is indistinguishable from a ledger balance on screen.
        "estimate": True,
        "basis": (
            "Holt-Winters additive extrapolation of the OBSERVED weekly net-cash "
            "series supplied by the caller. It assumes the recent trend and "
            "seasonality continue; it knows nothing about open bills, due dates or "
            "anything not already in that history. Amounts are fixed-4 strings for "
            "uniformity with the rest of the system, NOT because they are "
            "paisa-exact."
        ),
        "forecast": _to_money(hw),
        "naive_baseline": _to_money(nv),
    }
