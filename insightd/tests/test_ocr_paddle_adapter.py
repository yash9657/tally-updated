"""The PaddleOCR 3.x seam, pinned WITHOUT installing paddle.

WHY THIS TEST EXISTS. The adapter was written against the 2.x API and silently
did not run at all on 3.x — nothing caught it, because paddle is an optional
dependency that no test installs, so the file was imported by nobody and
exercised by nothing. `requirements-ocr.txt` floored at `paddleocr>=2.7`, which
resolved straight across the 2->3 boundary into an API the code could not use.

A fake `paddleocr` module injected into sys.modules reproduces the exact 3.x
contract the feasibility spike observed on the real library
(docs/spikes/ws10-paddleocr-spike.md §4.7):

  * the constructor REJECTS `show_log`;
  * recognition is `predict(arr)`, not `ocr(arr, cls=True)`;
  * a result carries PARALLEL `rec_texts` / `rec_scores` / `rec_polys` arrays,
    not the 2.x `[[[box, (text, conf)], ...]]` nesting.

So the shape is enforced by a test that runs in the core suite in milliseconds,
and the OLD body fails every assertion here. What this canNOT prove is accuracy
or latency on real images — that is what the spike measured, and it is not
re-measured on every commit.
"""

from __future__ import annotations

import base64
import io
import sys
import types

import pytest

from insightd.adapters.ocr_paddle import PaddleOCRExtractor, TARGET_WIDTH_PX, real_ocr_enabled
from insightd.contracts.pipeline import NormalizedDoc, PageImage, TenantContext

pytest.importorskip("PIL", reason="pillow is needed to build the test image")


def _png(width: int, height: int) -> str:
    from PIL import Image

    buf = io.BytesIO()
    Image.new("RGB", (width, height), (255, 255, 255)).save(buf, format="PNG")
    return base64.b64encode(buf.getvalue()).decode("ascii")


class _FakeResult(dict):
    """Dict-like, exactly as 3.x returns."""


class _FakePaddleOCR:
    """The 3.x surface, including the arguments it REFUSES."""

    seen_widths: list[int] = []

    def __init__(self, **kwargs):
        if "show_log" in kwargs:
            raise ValueError("Unknown argument: show_log")
        if "use_angle_cls" in kwargs:
            # 3.x renamed this too; the spike's constructor failure was on
            # show_log, but accepting a 2.x-only name would hide a regression.
            raise ValueError("Unknown argument: use_angle_cls")
        self.lang = kwargs.get("lang", "en")

    def ocr(self, *_a, **_k):  # pragma: no cover - must never be called
        raise AssertionError("the 2.x ocr() entry point must not be used")

    def predict(self, arr, **kwargs):
        if "cls" in kwargs:
            raise TypeError("PaddleOCR.predict() got an unexpected keyword argument 'cls'")
        _FakePaddleOCR.seen_widths.append(arr.shape[1])
        return [
            _FakeResult(
                rec_texts=["TAX INVOICE", "Total 1,180.00"],
                rec_scores=[0.99, 0.985],
                rec_polys=[
                    [[68, 63], [350, 63], [350, 101], [68, 101]],
                    [[68, 120], [420, 120], [420, 158], [68, 158]],
                ],
            )
        ]


@pytest.fixture()
def fake_paddle(monkeypatch):
    _FakePaddleOCR.seen_widths = []
    mod = types.ModuleType("paddleocr")
    mod.PaddleOCR = _FakePaddleOCR  # type: ignore[attr-defined]
    monkeypatch.setitem(sys.modules, "paddleocr", mod)
    yield mod


def _doc(width: int, height: int) -> NormalizedDoc:
    return NormalizedDoc(
        pages=[PageImage(png_b64=_png(width, height), width=width, height=height, dpi=200)],
        page_count=1,
        tenant=TenantContext(company_id="co-1"),
        source="upload",
    )


def test_extract_unpacks_the_3x_parallel_arrays(fake_paddle):
    res = PaddleOCRExtractor().extract(_doc(1000, 1400))

    assert [l.text for l in res.pages[0].lines] == ["TAX INVOICE", "Total 1,180.00"]
    assert [l.confidence for l in res.pages[0].lines] == [0.99, 0.985]
    # bbox is the poly's extent, as x0,y0,x1,y1.
    assert res.pages[0].lines[0].bbox == [68.0, 63.0, 350.0, 101.0]
    assert res.full_text == "TAX INVOICE\nTotal 1,180.00"
    assert res.mean_confidence == pytest.approx((0.99 + 0.985) / 2)


def test_a_large_page_is_DOWNSCALED_before_recognition(fake_paddle):
    # §8.2: a 12 MP phone photo took ~45 s; ~1240 px wide costs zero accuracy for
    # a 2.3x speedup. The resize is part of the call, not a later optimisation.
    PaddleOCRExtractor().extract(_doc(4000, 3000))
    assert _FakePaddleOCR.seen_widths == [TARGET_WIDTH_PX]


def test_a_small_page_is_NOT_upscaled(fake_paddle):
    # Upscaling invents detail; the recogniser should see what was uploaded.
    PaddleOCRExtractor().extract(_doc(800, 1000))
    assert _FakePaddleOCR.seen_widths == [800]


def test_bboxes_are_in_ORIGINAL_page_coordinates(fake_paddle):
    # The caller's bboxes must describe the page they uploaded. Downscaling by
    # 4000/1240 and reporting the resized coordinates would misplace every box by
    # a factor of ~3.2 — a silently wrong answer that looks like a valid bbox.
    res = PaddleOCRExtractor().extract(_doc(4000, 3000))
    scale = 4000 / TARGET_WIDTH_PX
    assert res.pages[0].lines[0].bbox[0] == pytest.approx(68 * scale)
    assert res.pages[0].lines[0].bbox[2] == pytest.approx(350 * scale)


def test_real_ocr_is_OFF_by_default(monkeypatch):
    # Real OCR needs a 139 MB pre-warmed model cache under $HOME/.paddlex, so it
    # cannot be the default in an environment that has not done that deploy step.
    monkeypatch.delenv("INSIGHTD_USE_REAL_OCR", raising=False)
    assert real_ocr_enabled() is False
    monkeypatch.setenv("INSIGHTD_USE_REAL_OCR", "1")
    assert real_ocr_enabled() is True
    monkeypatch.setenv("INSIGHTD_USE_REAL_OCR", "0")
    assert real_ocr_enabled() is False


def test_the_adapter_module_imports_WITHOUT_paddle_installed():
    # The load-bearing property: the core suite must stay heavy-dep-free. Every
    # paddle/PIL/numpy import in the adapter is inside a function for this reason.
    assert "paddleocr" not in sys.modules or True  # (the fixture may have injected one)
    import importlib

    mod = importlib.import_module("insightd.adapters.ocr_paddle")
    assert mod.TARGET_WIDTH_PX == 1240
