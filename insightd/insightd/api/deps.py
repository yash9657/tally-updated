"""Dependency-injected Pipeline + store. Fake by default in tests/CI.

A test overrides ``app.dependency_overrides[get_pipeline]`` to inject a fully-fake
pipeline (or a SpyLLM one). Nothing here imports a cored write client.
"""

from __future__ import annotations

import threading
from typing import Any

from ..config import Settings, get_settings
from ..pipeline import Pipeline, build_pipeline
from .store import DraftStore, get_store

_pipeline: Pipeline | None = None


def get_pipeline() -> Pipeline:
    global _pipeline
    if _pipeline is None:
        _pipeline = build_pipeline()
    return _pipeline


# Draft-store selection. Default (no INSIGHTD_DATABASE_URL) is the in-memory
# DraftStore, so tests/CI need zero db deps. With a DSN set we build the
# RLS-scoped PGDraftStore ONCE and reuse it; psycopg is imported only on that
# branch, keeping it out of the core import graph (mirrors how the anthropic /
# paddleocr adapters stay lazy).
_pg_store: Any | None = None
_pg_lock = threading.Lock()


def get_draft_store() -> Any:
    """The active draft store: PG-backed when a DSN is configured, else memory.

    The return type is intentionally unnarrowed: ``PGDraftStore`` duck-types the
    ``DraftStore`` surface (``put`` / ``get`` / ``list_for_company``) without
    inheriting from it — the in-memory class owns a lock and dicts the PG one
    must not — so annotating ``-> DraftStore`` would be a lie. FastAPI does not
    validate what a ``Depends`` returns, so the draft routes are unaffected.
    """
    global _pg_store
    dsn = get_settings().database_url
    if not dsn:
        return get_store()
    with _pg_lock:
        if _pg_store is None:
            from .pg_store import PGDraftStore  # lazy: keeps psycopg optional

            store = PGDraftStore(dsn)
            # Fail LOUD on an unmigrated schema rather than silently degrading
            # to the in-memory store, which would look healthy while dropping
            # every draft on restart.
            store.verify_ready()
            _pg_store = store
        return _pg_store


def get_active_settings() -> Settings:
    return get_settings()


__all__ = [
    "DraftStore",
    "Pipeline",
    "Settings",
    "get_active_settings",
    "get_draft_store",
    "get_pipeline",
]
