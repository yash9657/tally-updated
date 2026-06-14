"""Dependency-injected Pipeline + store. Fake by default in tests/CI.

A test overrides ``app.dependency_overrides[get_pipeline]`` to inject a fully-fake
pipeline (or a SpyLLM one). Nothing here imports a cored write client.
"""

from __future__ import annotations

from ..config import Settings, get_settings
from ..pipeline import Pipeline, build_pipeline
from .store import DraftStore, get_store

_pipeline: Pipeline | None = None


def get_pipeline() -> Pipeline:
    global _pipeline
    if _pipeline is None:
        _pipeline = build_pipeline()
    return _pipeline


def get_draft_store() -> DraftStore:
    return get_store()


def get_active_settings() -> Settings:
    return get_settings()
