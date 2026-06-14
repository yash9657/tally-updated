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


def test_no_route_targets_cored_vouchers_write():
    app = create_app()
    for route in app.routes:
        path = getattr(route, "path", "")
        methods = getattr(route, "methods", set()) or set()
        # No route may target a cored /vouchers path at all.
        assert "/vouchers" not in path, f"forbidden route: {path}"
        # The only mutating method is POST /drafts.
        mutating = methods & {"POST", "PUT", "PATCH", "DELETE"}
        if mutating:
            assert path.endswith("/drafts"), f"unexpected mutating route: {methods} {path}"


def test_only_post_drafts_mutates():
    app = create_app()
    mutating = [
        (sorted(getattr(r, "methods", set()) or set()), getattr(r, "path", ""))
        for r in app.routes
        if (getattr(r, "methods", set()) or set()) & {"POST", "PUT", "PATCH", "DELETE"}
    ]
    paths = {p for _, p in mutating}
    assert paths == {"/companies/{company_id}/drafts"}, paths


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
