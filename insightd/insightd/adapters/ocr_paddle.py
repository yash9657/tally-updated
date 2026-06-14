"""Real OCR — PaddleOCR (self-hosted, in-India, no key). Lazy imports only.

Runs PP-OCRv-class detect+recognize on each PageImage and emits text + bbox +
per-line confidence. ``import paddleocr`` happens INSIDE __init__/extract so the
testable core never needs paddle.
"""

from __future__ import annotations

import base64
import io

from ..contracts.pipeline import (
    NormalizedDoc,
    OCRLine,
    OCRPage,
    OCRResult,
)


class PaddleOCRExtractor:
    def __init__(self, lang: str = "en") -> None:
        self._lang = lang
        self._engine = None  # lazily constructed on first extract

    def _ensure_engine(self):
        if self._engine is None:
            from paddleocr import PaddleOCR  # lazy

            self._engine = PaddleOCR(use_angle_cls=True, lang=self._lang, show_log=False)
        return self._engine

    def extract(self, doc: NormalizedDoc) -> OCRResult:
        from PIL import Image  # lazy
        import numpy as np  # lazy

        engine = self._ensure_engine()
        pages: list[OCRPage] = []
        all_text: list[str] = []
        confs: list[float] = []

        for page in doc.pages:
            img = Image.open(io.BytesIO(base64.b64decode(page.png_b64))).convert("RGB")
            arr = np.array(img)
            result = engine.ocr(arr, cls=True)
            lines: list[OCRLine] = []
            for block in result or []:
                for det in block or []:
                    box, (text, conf) = det
                    xs = [pt[0] for pt in box]
                    ys = [pt[1] for pt in box]
                    bbox = [min(xs), min(ys), max(xs), max(ys)]
                    lines.append(
                        OCRLine(text=text, bbox=bbox, confidence=float(conf))
                    )
                    all_text.append(text)
                    confs.append(float(conf))
            pages.append(OCRPage(lines=lines))

        mean_conf = sum(confs) / len(confs) if confs else 0.0
        return OCRResult(
            pages=pages,
            full_text="\n".join(all_text),
            mean_confidence=mean_conf,
            tenant=doc.tenant,
            source=doc.source,
        )
