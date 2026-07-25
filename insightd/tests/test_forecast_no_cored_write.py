"""Structural pin (archtest-equivalent for the Python plane): the forecast plane
holds NO cored write path.

insightd's whole thesis is that it can never write to the ledger. The WS5b
forecast module reads history (injected, or in a future follow-up a read-only
cored GET) and returns an estimate — it must import no cored write client and
issue no mutating HTTP verb. We assert this by scanning the source of every
forecast module + the forecast route for mutating-verb / write-client markers.
This runs WITHOUT statsmodels (pure source scan), so it always gates.
"""

from __future__ import annotations

from pathlib import Path

import insightd

PKG_ROOT = Path(insightd.__file__).resolve().parent

FORECAST_SOURCES = [
    PKG_ROOT / "forecast" / "model.py",
    PKG_ROOT / "forecast" / "backtest.py",
    PKG_ROOT / "forecast" / "service.py",
    PKG_ROOT / "forecast" / "fixtures.py",
    PKG_ROOT / "forecast" / "__init__.py",
    PKG_ROOT / "api" / "routes_forecast.py",
]

# Markers that would indicate a write to cored (any mutating HTTP verb, or a
# cored voucher write path). httpx.get is allowed (read-only); httpx.post/put/... is not.
FORBIDDEN = [
    "/vouchers",
    ".post(",
    ".put(",
    ".patch(",
    ".delete(",
    "postPurchaseVoucher",
    "RunInTx",
]


def test_forecast_sources_have_no_cored_write_path():
    for src in FORECAST_SOURCES:
        assert src.exists(), f"missing forecast source {src}"
        # The forecast ROUTE legitimately declares @router.post for ITS OWN
        # endpoint; that is not a cored write. Scan for cored-write markers only,
        # excluding the route decorator line.
        text = src.read_text()
        for marker in FORBIDDEN:
            if marker == ".post(" and src.name == "routes_forecast.py":
                # Allow the route to register its own POST endpoint, but nothing else.
                # There must be exactly one .post( — the @router.post decorator.
                assert text.count(".post(") <= 1, (
                    f"{src.name} has more than its own @router.post — a cored write leaked in"
                )
                continue
            assert marker not in text, f"{src.name} contains a cored-write marker {marker!r}"
