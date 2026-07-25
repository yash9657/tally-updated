"""Deterministic synthetic seasonal cash-flow fixtures for the backtest gate.

Each fixture is a weekly net-cash series with a clear trend + additive season +
small deterministic noise — the regime where Holt-Winters SHOULD beat the naive
last-value baseline. Generation is seeded (numpy default_rng), so the series and
therefore the gate are reproducible run to run.
"""

from __future__ import annotations

import math

import numpy as np


def seasonal_series(
    n: int,
    base: float,
    slope: float,
    amplitude: float,
    period: int,
    noise_sd: float,
    seed: int,
) -> list[float]:
    """A trend + additive-seasonal series with seeded Gaussian noise."""
    rng = np.random.default_rng(seed)
    out: list[float] = []
    for i in range(n):
        seasonal = amplitude * math.sin(2 * math.pi * (i % period) / period)
        noise = float(rng.normal(0.0, noise_sd))
        out.append(base + slope * i + seasonal + noise)
    return out


def gate_fixtures() -> list[tuple[str, list[float], int]]:
    """The >=3 seeded fixtures the backtest gate runs. Each entry is
    (name, series, seasonal_periods). Chosen so a naive last-value forecast is
    materially worse than Holt-Winters (trend + season the baseline cannot track).
    """
    return [
        ("rising-q4", seasonal_series(n=32, base=1000, slope=40, amplitude=300, period=4, noise_sd=15, seed=1), 4),
        ("rising-q13", seasonal_series(n=52, base=500, slope=25, amplitude=400, period=13, noise_sd=20, seed=2), 13),
        ("steep-q4", seasonal_series(n=28, base=2000, slope=80, amplitude=250, period=4, noise_sd=10, seed=3), 4),
        ("gentle-q6", seasonal_series(n=36, base=800, slope=30, amplitude=350, period=6, noise_sd=12, seed=4), 6),
    ]
