"""Every (path, methods) the app serves, whichever FastAPI is installed.

⛔ WHY THIS EXISTS. The route-table invariants (HITL: no route writes to cored
/vouchers; only allow-listed routes mutate; the forecast plane is gone) used to
iterate ``app.routes`` and read ``route.path`` / ``route.methods``. FastAPI
0.14x keeps an included router as a lazy ``_IncludedRouter`` with NEITHER
attribute, so on a fresh CI install every one of those loops walked a list of
objects with no path and no methods and PASSED — the HITL boundary test
asserted nothing (measured: ``set()`` of mutating paths on fastapi 0.142.0,
green on the 0.136 dev venv). The only reason it surfaced at all is the one
test that also demands ``/drafts`` be present.

So the walk resolves included routers and mounts itself, carrying prefixes,
and ``flat_routes`` REFUSES to return an empty table: a walker that finds
nothing is a broken walker, never a clean app.
"""

from __future__ import annotations

from typing import Iterable


def _walk(routes: Iterable, prefix: str, out: list[tuple[str, frozenset[str]]]) -> None:
    for r in routes:
        # FastAPI >= 0.14x: an included router, resolved lazily.
        inner = getattr(r, "original_router", None)
        if inner is not None:
            ctx = getattr(r, "include_context", None)
            p = prefix + (getattr(ctx, "prefix", "") or "") + (getattr(inner, "prefix", "") or "")
            _walk(getattr(inner, "routes", []), p, out)
            continue
        path = getattr(r, "path", None)
        # A Mount (or a sub-application) carries its own route list.
        sub = getattr(r, "routes", None)
        if sub and path is not None and not getattr(r, "methods", None):
            _walk(sub, prefix + path, out)
            continue
        if path is None:
            raise AssertionError(f"route table walker does not understand {type(r).__name__}; "
                                 "teach tests/route_table.py rather than skipping it")
        out.append((prefix + path, frozenset(getattr(r, "methods", None) or ())))


def flat_routes(app) -> list[tuple[str, frozenset[str]]]:
    out: list[tuple[str, frozenset[str]]] = []
    _walk(app.routes, "", out)
    assert out, "the route walker found no routes at all — the walker is broken, not the app"
    return out
