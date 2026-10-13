"""The statistical forecast plane is GONE, and this is what keeps it gone (D96).

===========================================================================
⛔⛔ THIS IS A DELETION ORACLE, AND A DELETION IS EXACTLY THE CHANGE THAT ROTS
    BACK IN WITHOUT ONE.
===========================================================================
``POST /companies/{id}/forecast/cashflow`` — a Holt-Winters estimate over an
injected history frame — was mounted on insightd behind ``try: import statsmodels``
and **no client ever called it**. Register row D96 asked whether it should exist at
all; the user's answer on 2026-08-24 was to DELETE it, and the reasoning is worth
having here rather than only in a handoff:

    cored's WS5a projection is explainable to an auditor BILL BY BILL — its own
    ``method`` string says "deterministic; no statistics, no model". A second
    cash-forward number, produced by a different method and carrying no auditor
    story, invites exactly the confusion the Cash Flow screen's disclosure block
    exists to prevent. A forecast nobody can explain is worse than no forecast.

⛔ THE ROW OWED A TEST AND NOT A GREP IN A COMMIT MESSAGE. A `git rm` proves the
files were removed on one day; it proves nothing about the next person who adds
``routes_forecast.py`` back because a plan document still describes WS5b. So the
absence is asserted three ways — the PACKAGE is unimportable, the ROUTE TABLE and
the published SCHEMA carry no forecast path, and NO SOURCE FILE mentions the
deleted module. Any one of them alone is weaker than it looks: a module can exist
unimported, a route can exist unpublished, and an import can survive a route.

⚠️ AND THE MOUNT WAS `try/except ImportError`, WHICH IS WHY A ROUTE-TABLE CHECK ON
ITS OWN WOULD HAVE BEEN VACUOUS HERE. Before the deletion, this whole file would
have PASSED on any machine without statsmodels installed — the route simply is not
mounted there — while the code, the dependency file and the tests were all still in
the tree. That is the exact shape of a test that looks green and guards nothing.
The package and source-scan arms below are what make it real, and they hold on
every machine.

⛔ IF YOU ARE RE-ADDING A STATISTICAL FORECAST, THIS TEST IS WHERE THAT DECISION
HAS TO BE ARGUED — and it needs its own report NAME and its own ACL counterweight
(the D95/migration-0076 pattern), because a second method is a second authority.
"""

from __future__ import annotations

import importlib
import pathlib

import pytest

from insightd.api.app import create_app

from route_table import flat_routes

PKG = pathlib.Path(__file__).resolve().parent.parent / "insightd"
REPO = pathlib.Path(__file__).resolve().parent.parent


@pytest.mark.parametrize(
    "module",
    ["insightd.forecast", "insightd.forecast.service", "insightd.api.routes_forecast"],
)
def test_the_forecast_modules_are_not_importable(module: str):
    """The CODE is gone, not merely unmounted."""
    with pytest.raises(ModuleNotFoundError):
        importlib.import_module(module)


def test_no_route_serves_a_forecast():
    """The ROUTE TABLE carries nothing forecast-shaped, by any verb."""
    app = create_app()
    offenders = [f"{sorted(methods)} {path}" for path, methods in flat_routes(app) if "forecast" in path]
    assert offenders == [], f"the forecast plane is back on the route table: {offenders}"


def test_the_published_schema_carries_no_forecast():
    """And it is absent from what insightd PUBLISHES.

    Separate from the route-table arm on purpose: `/openapi.json` is the surface a
    deployer, an integrator or an attacker actually reads, and the two can disagree
    (a mounted route excluded from the schema, or a stale cached schema).
    """
    schema = create_app().openapi()
    assert not [p for p in schema["paths"] if "forecast" in p], schema["paths"].keys()


def test_nothing_in_the_package_still_imports_the_forecast_module():
    """NOTHING IMPORTS IT — the half a route check cannot see.

    A dangling `from ..forecast.service import ...` would be an ImportError at load
    time, and the app factory's old mount SWALLOWED ImportError. So a leftover
    import could have taken a real route down and been reported as "statsmodels is
    not installed here".
    """
    offenders = []
    for src in sorted(PKG.rglob("*.py")):
        text = src.read_text(encoding="utf-8")
        for line in text.splitlines():
            stripped = line.strip()
            if not (stripped.startswith("import ") or stripped.startswith("from ")):
                continue
            if "forecast" in stripped:
                offenders.append(f"{src.relative_to(PKG)}: {stripped}")
    assert offenders == [], offenders


def test_the_optional_dependency_file_is_gone_too():
    """The dependency went with the feature.

    Leaving `requirements-forecast.txt` behind would keep statsmodels an installable
    part of this service and leave the next reader believing the plane is merely
    switched off.
    """
    assert not (REPO / "requirements-forecast.txt").exists()
