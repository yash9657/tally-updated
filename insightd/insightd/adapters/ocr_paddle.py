"""Real OCR — PaddleOCR (self-hosted, in-India, no key). Lazy imports only.

Runs PP-OCRv-class detect+recognize on each PageImage and emits text + bbox +
per-line confidence. ``import paddleocr`` happens INSIDE the methods, so the
testable core never needs paddle and the 83-test suite stays heavy-dep-free.

WRITTEN AGAINST paddleocr 3.7.0 (pinned in requirements-ocr.txt). The previous
body targeted the 2.x API and did not run at all on 3.x — the feasibility spike
observed both failures verbatim (docs/spikes/ws10-paddleocr-spike.md §4.7):

    PaddleOCR(use_angle_cls=True, lang=..., show_log=False)
      -> ValueError: Unknown argument: show_log
    engine.ocr(arr, cls=True)
      -> TypeError: PaddleOCR.predict() got an unexpected keyword argument 'cls'

and the result shape changed from ``[[[box, (text, conf)], ...]]`` to a list of
dict-like results carrying PARALLEL ``rec_texts`` / ``rec_scores`` /
``rec_polys`` arrays. That is why `requirements-ocr.txt` is now pinned rather
than floored at ``>=2.7``: a range spanning the 2->3 boundary resolves to an API
this code cannot use.

TWO THINGS THE SPIKE MEASURED THAT SHAPE THIS FILE:

  * DOWNSCALE FIRST. A 12 MP phone photo took ~45 s; downscaling to ~1240 px
    wide cost ZERO accuracy for a 2.3x speedup (§8.2). So the resize is not an
    optimisation to add later, it is part of the call.
  * CONFIDENCE IS NOT A REVIEW SIGNAL — not the mean, and not per line. A
    corrupted money value (`300.00` read as `300:00`) came back at 0.985 (§8.4).
    This module still REPORTS confidence, because it is real information about
    the recogniser, but nothing downstream may route human review on it. The
    structural checks in adapters/mapper.py are what decide review.
"""

from __future__ import annotations

import base64
import io
import os

from ..contracts.pipeline import (
    NormalizedDoc,
    OCRLine,
    OCRPage,
    OCRResult,
)

# The width the spike found to be the sweet spot: no accuracy cost, 2.3x faster.
# Images NARROWER than this are left alone — upscaling invents detail.
TARGET_WIDTH_PX = 1240


def real_ocr_enabled() -> bool:
    """Whether the real recogniser is switched on.

    Default OFF. Real OCR needs a 139 MB model cache under ``$HOME/.paddlex``
    that must be pre-warmed as an explicit deploy step (§8.6 item 6), so it can
    never be the default in an environment that has not done so.
    """
    return os.getenv("INSIGHTD_USE_REAL_OCR", "").strip() in {"1", "true", "yes"}


class PaddleOCRExtractor:
    def __init__(self, lang: str = "en") -> None:
        self._lang = lang
        self._engine = None  # lazily constructed on first extract

    def _ensure_engine(self):
        if self._engine is None:
            from paddleocr import PaddleOCR  # lazy

            # NOTE the constructor is deliberately MINIMAL. The default one also
            # loads a document-preprocessor (orientation + UVDoc unwarping) that
            # the minimal path never downloads, and constructing it against a
            # cache warmed for this configuration fails hard offline (§4.6).
            # Whatever configuration production uses must be the one that was
            # pre-warmed — adding a flag here silently reintroduces a network
            # dependency at boot.
            self._engine = PaddleOCR(lang=self._lang)
        return self._engine

    @staticmethod
    def _downscale(img):
        """Resize to TARGET_WIDTH_PX wide, preserving aspect. Never upscales."""
        from PIL import Image  # lazy

        if img.width <= TARGET_WIDTH_PX:
            return img
        height = round(img.height * TARGET_WIDTH_PX / img.width)
        return img.resize((TARGET_WIDTH_PX, height), Image.LANCZOS)

    def extract(self, doc: NormalizedDoc) -> OCRResult:
        from PIL import Image  # lazy
        import numpy as np  # lazy

        engine = self._ensure_engine()
        pages: list[OCRPage] = []
        all_text: list[str] = []
        confs: list[float] = []

        for page in doc.pages:
            img = Image.open(io.BytesIO(base64.b64decode(page.png_b64))).convert("RGB")
            # Scale factor back to ORIGINAL page coordinates: the caller's bboxes
            # must describe the page they uploaded, not the one we resized.
            original_width = img.width
            img = self._downscale(img)
            scale = original_width / img.width if img.width else 1.0

            arr = np.array(img)
            lines: list[OCRLine] = []
            for res in engine.predict(arr) or []:
                texts = _field(res, "rec_texts")
                scores = _field(res, "rec_scores")
                polys = _field(res, "rec_polys")
                # PARALLEL arrays, zipped. `strict` is deliberately not used:
                # a recogniser that returned ragged arrays should surface as a
                # short result to inspect, not an exception mid-document.
                for text, score, poly in zip(texts, scores, polys):
                    xs = [float(pt[0]) * scale for pt in poly]
                    ys = [float(pt[1]) * scale for pt in poly]
                    lines.append(
                        OCRLine(
                            text=str(text),
                            bbox=[min(xs), min(ys), max(xs), max(ys)],
                            confidence=float(score),
                        )
                    )
                    all_text.append(str(text))
                    confs.append(float(score))
            pages.append(OCRPage(lines=lines))

        mean_conf = sum(confs) / len(confs) if confs else 0.0
        return OCRResult(
            pages=pages,
            full_text="\n".join(all_text),
            mean_confidence=mean_conf,
            tenant=doc.tenant,
            source=doc.source,
        )


def _field(res, name: str):
    """Read one parallel array off a 3.x OCRResult.

    3.x results are dict-LIKE: subscriptable, and in some builds also attribute
    accessible. Both are tried so a minor-version change in how the result is
    exposed does not break the adapter the way the 2->3 change did.
    """
    try:
        return res[name] or []
    except (TypeError, KeyError, IndexError):
        return getattr(res, name, None) or []
