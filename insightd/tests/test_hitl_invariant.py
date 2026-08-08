"""Human-in-the-loop is STRUCTURAL: insightd has no ledger-write path.

Asserts:
  * the FastAPI route table contains zero routes that write to cored /vouchers
  * the only mutating route writes to the draft store, returning a draft
  * no insightd module imports a cored write client
  * no module top-level-imports anthropic / paddleocr / presidio (import-lint)
"""

from __future__ import annotations

import ast
import pathlib

from insightd.api.app import create_app

PKG = pathlib.Path(__file__).resolve().parent.parent / "insightd"


# The POST routes insightd is allowed to expose. /drafts writes the DRAFT store
# (never cored). /forecast/cashflow (WS5b, mounted only when statsmodels is
# installed) writes NOTHING — it is a pure read-only computation that uses POST
# only to carry the history frame in its body; test_forecast_no_cored_write.py
# separately proves it holds no cored write path.
#
# /drafts/{id}/accepted moves a draft to its terminal 'accepted'
# status and records WHICH cored voucher the human posted from it — it is told an
# outcome by the client that did the write, and its request model
# (RecordAcceptRequest) deliberately carries nothing but that voucher's id/no, so
# there is nothing in it insightd could post even if it wanted to. It exists
# because its absence let one supplier bill become two vouchers across a page
# reload; see routes_drafts.accept_draft.
#
# /drafts/{id}/claim and /drafts/{id}/claim/release are CONCURRENCY-RESERVATION
# routes, and they are allowed here for a narrower reason than the two above: they
# mutate ONLY the claim lease on a draft row (claim_token / claimed_by / claimed_at /
# claim_expires_at) and cannot touch a posting, an amount or a status. ``accepted``
# above records an outcome; these two record *who is about to attempt one*. They exist
# because /accepted — a correct compare-and-set — still let one supplier bill into the
# books twice: it necessarily runs AFTER the voucher is posted, so two browsers racing
# on one draft both posted and only the second MARKING was refused. Reserving before
# the post is the only ordering in which the loser can be stopped before it writes.
# See routes_drafts.claim_draft.
#
# ⚠️ A CLAIM TOKEN AUTHORISES NOTHING — IT IS NOT A CREDENTIAL. insightd has no
# authentication at all (a separate, known high finding), and adding these routes
# neither improves nor worsens that: the token is an opaque concurrency value whose
# only power is to release or renew the claim it already holds. Nothing is readable,
# writable or postable by virtue of holding one, and ClaimDraftRequest /
# ReleaseClaimRequest are `extra="forbid"` with no field insightd could act on.
#
# ⛔ THIS ALLOWLIST IS NOT A FORMALITY AND MUST NOT BE WIDENED TO SILENCE A FAILURE.
# It exists so that a route which writes to a cored /vouchers path can never appear in
# insightd unnoticed — that is the whole HITL boundary. Every entry above is here
# because someone argued, in writing, that it writes only to the DRAFT store. A new
# entry with no such argument is a bug, not a config change.
_ALLOWED_MUTATING_PATHS = {
    "/companies/{company_id}/drafts",
    "/companies/{company_id}/drafts/{draft_id}/claim",
    "/companies/{company_id}/drafts/{draft_id}/claim/release",
    "/companies/{company_id}/drafts/{draft_id}/accepted",
    "/companies/{company_id}/forecast/cashflow",
}


def test_no_route_targets_cored_vouchers_write():
    app = create_app()
    for route in app.routes:
        path = getattr(route, "path", "")
        methods = getattr(route, "methods", set()) or set()
        # No route may target a cored /vouchers path at all.
        assert "/vouchers" not in path, f"forbidden route: {path}"
        # The only mutating routes are the drafts write + the read-only forecast.
        mutating = methods & {"POST", "PUT", "PATCH", "DELETE"}
        if mutating:
            assert path in _ALLOWED_MUTATING_PATHS, f"unexpected mutating route: {methods} {path}"


def test_only_allowed_routes_mutate():
    app = create_app()
    paths = {
        getattr(r, "path", "")
        for r in app.routes
        if (getattr(r, "methods", set()) or set()) & {"POST", "PUT", "PATCH", "DELETE"}
    }
    # Every mutating path is allowlisted, and /drafts is always present.
    assert paths <= _ALLOWED_MUTATING_PATHS, paths
    assert "/companies/{company_id}/drafts" in paths


def _module_sources():
    for p in PKG.rglob("*.py"):
        yield p, p.read_text()


def test_no_top_level_heavy_imports():
    """Import-lint: anthropic/paddleocr/presidio must NEVER be imported at module
    top level anywhere — they are lazy-imported inside adapter methods only."""
    heavy = {"anthropic", "paddleocr", "paddlepaddle", "presidio_analyzer", "presidio_anonymizer", "PIL", "pdf2image"}
    offenders = []
    for path, src in _module_sources():
        tree = ast.parse(src, filename=str(path))
        for node in ast.walk(tree):
            # only flag module-level imports (col_offset == 0 in the module body)
            if isinstance(node, (ast.Import, ast.ImportFrom)):
                # determine if this import is at module top level (not nested in a
                # function/method): top-level statements have a parent of Module.
                pass
        # walk the module body explicitly for top-level imports
        for node in tree.body:
            names = []
            if isinstance(node, ast.Import):
                names = [a.name.split(".")[0] for a in node.names]
            elif isinstance(node, ast.ImportFrom) and node.module:
                names = [node.module.split(".")[0]]
            for n in names:
                if n in heavy:
                    offenders.append(f"{path.name}: top-level import of {n}")
    assert not offenders, offenders


def test_no_cored_write_client_imported():
    """No insightd module may import a cored write client (e.g. a 'rest'/'voucher'
    POST client). We forbid any import that names a voucher-write surface."""
    forbidden_tokens = ("cored_client", "voucher_client", "ledger_write", "rest_post_voucher")
    offenders = []
    for path, src in _module_sources():
        low = src.lower()
        for tok in forbidden_tokens:
            if tok in low:
                offenders.append(f"{path.name}: references {tok}")
        # also: no construction of a cored /vouchers URL anywhere
        if "/vouchers/purchase" in src and path.name not in ("draft.py", "cored_map.py"):
            # the contract docstrings may MENTION the path; code must not build it
            for line in src.splitlines():
                if "/vouchers/purchase" in line and not line.lstrip().startswith("#") and '"' in line and "post" in line.lower():
                    offenders.append(f"{path.name}: constructs a /vouchers POST url")
    assert not offenders, offenders


def test_package_imports_without_heavy_deps():
    """Importing the package + core modules must work with none of the optional
    extras installed (proves lazy imports)."""
    import importlib

    for mod in (
        "insightd",
        "insightd.config",
        "insightd.pipeline",
        "insightd.api.app",
        "insightd.adapters.mapper",
        "insightd.adapters.fakes",
        "insightd.redaction.boundary",
        # importing the real-adapter MODULES must also work (lazy imports inside)
        "insightd.adapters.classifier_haiku",
        "insightd.adapters.inferencer_opus",
        "insightd.adapters.pii_presidio",
        "insightd.adapters.ocr_paddle",
        "insightd.adapters.ingest_real",
    ):
        importlib.import_module(mod)
