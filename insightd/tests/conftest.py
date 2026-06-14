"""Shared test fixtures + helpers. Core deps only (no anthropic/paddle/presidio)."""

from __future__ import annotations

import json
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
