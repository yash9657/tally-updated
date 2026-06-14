"""The single cross-border chokepoint.

``redact_for_llm`` masks PII in an OCR result before it can reach a cross-border
LLM call, emitting a RedactionReport. ``assert_clean`` re-scans an OUTGOING
payload (system prompt + every message content block, recursively) and raises
``PIIBoundaryViolation`` if ANY PAN/GSTIN/Aadhaar/IFSC slipped through — the
fail-closed guard. Every real LLM adapter calls ``assert_clean`` as the FIRST
line of its method, before touching the anthropic client.

Masking uses non-reversible salted-hash tags (``<<GSTIN:ab12>>``): the same
entity gets the same tag within a request so co-reference survives, but the
value cannot be reconstructed. The salt is per-request and never stored.

Pure-python — no optional deps.
"""

from __future__ import annotations

import hashlib
import secrets

from ..contracts.pipeline import (
    OCRLine,
    OCRPage,
    OCRResult,
    RedactedOCR,
    RedactionEntity,
    RedactionReport,
)
from . import recognizers


class PIIBoundaryViolation(Exception):
    """Raised when raw PII is detected in a payload bound for a cross-border
    call. Fail-closed: the call MUST NOT proceed."""

    def __init__(self, found: dict[str, list[str]]) -> None:
        types = ", ".join(sorted(found))
        self.found = found
        super().__init__(
            f"PII boundary violation: raw {types} detected in outgoing payload "
            f"(values withheld). The cross-border call is refused."
        )


class Boundary:
    """A per-request boundary. Holds the per-request salt so co-reference tags
    are stable within one document but unlinkable across requests."""

    def __init__(self, salt: str | None = None) -> None:
        # per-request, never stored
        self._salt = salt or secrets.token_hex(8)

    def _tag(self, etype: str, value: str) -> str:
        h = hashlib.sha256((self._salt + "|" + value).encode("utf-8")).hexdigest()[:4]
        return f"<<{etype}:{h}>>"

    def _mask_text(self, text: str, entities: list[RedactionEntity], page: int) -> str:
        matches = recognizers.detect(text)
        if not matches:
            return text
        # rebuild left-to-right replacing each span with its salted tag.
        out: list[str] = []
        cursor = 0
        for m in matches:
            out.append(text[cursor : m.start])
            tag = self._tag(m.type, m.value)
            out.append(tag)
            cursor = m.end
            entities.append(
                RedactionEntity(
                    type=m.type, page=page, bbox=None, placeholder=tag, source="regex"
                )
            )
        out.append(text[cursor:])
        return "".join(out)

    def redact_for_llm(self, ocr: OCRResult) -> tuple[RedactedOCR, RedactionReport]:
        """Mask every PII span in ``ocr`` and return the redacted copy + report.

        This is the floor: the pure-python recognizers run UNCONDITIONALLY. A
        presidio adapter may ADD entities on top, but never gates these.
        """
        entities: list[RedactionEntity] = []
        new_pages: list[OCRPage] = []
        for pidx, page in enumerate(ocr.pages):
            new_lines: list[OCRLine] = []
            for line in page.lines:
                masked = self._mask_text(line.text, entities, pidx)
                # attach bbox of the line to the entities just added on this line
                for ent in entities:
                    if ent.page == pidx and ent.bbox is None and ent.placeholder in masked:
                        ent.bbox = list(line.bbox)
                new_lines.append(
                    OCRLine(text=masked, bbox=line.bbox, confidence=line.confidence)
                )
            new_pages.append(OCRPage(lines=new_lines))

        full_text = self._mask_text(ocr.full_text, [], 0)

        counts: dict[str, int] = {}
        for ent in entities:
            counts[ent.type] = counts.get(ent.type, 0) + 1

        report = RedactionReport(
            entities=entities,
            counts_by_type=counts,
            any_redacted=bool(entities),
        )
        redacted = OCRResult(
            pages=new_pages,
            full_text=full_text,
            mean_confidence=ocr.mean_confidence,
            tenant=ocr.tenant,
            source=ocr.source,
        )
        return redacted, report

    def assert_clean(self, payload) -> None:
        """Fail-closed guard. Recursively re-scan ``payload`` (strings, dicts,
        lists, pydantic models — incl. system prompt + message content blocks)
        and raise PIIBoundaryViolation if any raw PAN/GSTIN/Aadhaar/IFSC remains.

        Every LLM adapter MUST call this before touching its client."""
        found = recognizers.detect_all(payload)
        if found:
            raise PIIBoundaryViolation(found)
