"""Real Ingest — pypdf/Pillow normalization. Lazy imports only.

PDF -> rasterize each page to PNG (capped 2576px long edge, the Opus 4.8 vision
max). Image -> single-page normalize + EXIF-orient. Heavy imaging deps (pillow,
pdf2image) live behind the [ocr] extra and are imported INSIDE the methods, so
the testable core never pulls them in.
"""

from __future__ import annotations

import base64
import io

from ..contracts.pipeline import NormalizedDoc, PageImage, RawUpload

MAX_LONG_EDGE = 2576  # Opus 4.8 vision long-edge cap


class RealIngest:
    def normalize(self, upload: RawUpload) -> NormalizedDoc:
        raw = base64.b64decode(upload.content_b64)
        if upload.content_type == "application/pdf":
            pages = self._rasterize_pdf(raw)
        else:
            pages = [self._normalize_image(raw)]
        return NormalizedDoc(
            pages=pages,
            page_count=len(pages),
            source=upload.source,
            source_meta={"filename": upload.filename},
        )

    def _rasterize_pdf(self, raw: bytes) -> list[PageImage]:
        # pdf2image renders pages to PIL images (needs poppler at runtime).
        from pdf2image import convert_from_bytes  # lazy

        images = convert_from_bytes(raw, dpi=200)
        out: list[PageImage] = []
        for img in images:
            out.append(self._encode(self._cap(img)))
        return out

    def _normalize_image(self, raw: bytes) -> PageImage:
        from PIL import Image, ImageOps  # lazy

        img = Image.open(io.BytesIO(raw))
        img = ImageOps.exif_transpose(img)  # EXIF orient
        img = img.convert("RGB")
        return self._encode(self._cap(img))

    def _cap(self, img):
        from PIL import Image  # lazy

        w, h = img.size
        long_edge = max(w, h)
        if long_edge > MAX_LONG_EDGE:
            scale = MAX_LONG_EDGE / long_edge
            img = img.resize((int(w * scale), int(h * scale)), Image.LANCZOS)
        return img

    def _encode(self, img) -> PageImage:
        buf = io.BytesIO()
        img.save(buf, format="PNG")
        b64 = base64.b64encode(buf.getvalue()).decode("ascii")
        return PageImage(png_b64=b64, width=img.width, height=img.height, dpi=200)
