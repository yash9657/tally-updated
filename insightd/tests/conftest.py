"""Shared test fixtures + helpers. Core deps only (no anthropic/paddle/presidio)."""

from __future__ import annotations

import json
import os
import sys
from pathlib import Path

import pytest

# Make the package importable when running pytest from the repo without install.
ROOT = Path(__file__).resolve().parent.parent
if str(ROOT) not in sys.path:
    sys.path.insert(0, str(ROOT))

from insightd.contracts.pipeline import (  # noqa: E402
    NormalizedDoc,
    RawUpload,
    TenantContext,
)

CORPUS_DIR = ROOT / "insightd" / "benchmark" / "corpus"

# The DSN the ENVIRONMENT supplied, captured ONCE at import — before the autouse
# fixture below deletes it from os.environ. tests/test_pg_store.py's module-scoped
# `dsn` fixture reads THIS, not os.environ, so a CI-supplied DSN still selects the
# database the live PG group runs against. Read it any later and it is already gone.
ENV_DATABASE_URL = os.environ.get("INSIGHTD_DATABASE_URL")


@pytest.fixture(autouse=True)
def _in_memory_draft_store_by_default(request, monkeypatch):
    """Every test starts with the DRAFT STORE UNCONFIGURED, and must leave the
    PG-store cache empty.

    WHY THIS EXISTS. ``insightd.api.deps`` memoises the PGDraftStore in a
    MODULE-LEVEL global (``_pg_store``) keyed on nothing at all. Combine that with
    ``Makefile:17``, which exports ``INSIGHTD_DATABASE_URL`` into every ``make
    insightd-test`` run, and two things go wrong that have nothing to do with the
    code under test:

      1. Any test that builds the real app without overriding ``get_draft_store``
         (``test_forecast_api.py``, ``test_hitl_invariant.py``,
         ``test_enqueue_and_poll.py``) quietly connects to the SHARED DEV DATABASE
         and writes real ``insightd_draft`` rows — 61 stray ``co-1`` drafts, at the
         last count — while appearing to run against fakes.
      2. It populates ``_pg_store`` for the rest of the process. The later
         ``test_pg_store.py::test_a_configured_dsn_actually_selects_the_pg_store``
         restores, via ``MonkeyPatch.undo()``, the value that was there BEFORE it
         ran — that live store — and its closing ``assert deps_mod._pg_store is
         None`` fails. The failing test is not the guilty one, which is why
         ``make verify`` has been red at HEAD for a reason nobody could locate.

    So: delete the env var, pin ``config._settings`` to a DSN-less Settings, and
    reset the cache. The isolation belongs HERE, in the harness, not in the
    Makefile — ``run-insightd`` genuinely needs that env var and the durable store
    is the deliberate production default.

    THE TEARDOWN ASSERT IS THE POINT, not a formality. Without it the surviving
    assertion at the end of ``test_a_configured_dsn_actually_selects_the_pg_store``
    degrades into a restatement of ``MonkeyPatch.undo()`` semantics — it would stay
    green even if every test in the suite leaked a live store. Asserting here fails
    the LEAKING test, by name, at its own boundary.
    """
    import insightd.api.deps as deps_mod
    import insightd.config as config_mod

    monkeypatch.delenv("INSIGHTD_DATABASE_URL", raising=False)
    monkeypatch.setattr(config_mod, "_settings", config_mod.Settings(database_url=None))
    monkeypatch.setattr(deps_mod, "_pg_store", None)
    yield
    assert deps_mod._pg_store is None, (
        f"{request.node.nodeid} left a live PGDraftStore in insightd.api.deps._pg_store. "
        "That store is a process-wide global: every later test in this run will be "
        "served from it, writing to a real database instead of the in-memory fake. "
        "Override get_draft_store (app.dependency_overrides) or reset deps._pg_store."
    )


def load_fixture(fid: str) -> dict:
    return json.loads((CORPUS_DIR / f"{fid}.json").read_text())


def normalized_from_fixture(fx: dict, ctx: TenantContext) -> NormalizedDoc:
    pages = [
        {"png_b64": p.get("png_b64", ""), "width": p.get("width", 1),
         "height": p.get("height", 1), "dpi": p.get("dpi", 200)}
        for p in fx["input"]["pages"]
    ]
    return NormalizedDoc(
        pages=pages,
        page_count=len(pages),
        source="upload",
        tenant=ctx,
        source_meta={"fixture_json": json.dumps(fx)},
        fixture_id=fx["id"],
    )


@pytest.fixture
def upload() -> RawUpload:
    return RawUpload(
        content_b64="Zml4dHVyZQ==",
        content_type="application/pdf",
        filename="x.pdf",
        source="upload",
    )
