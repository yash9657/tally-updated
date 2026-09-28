"""GSTIN check-character validation.

A GSTIN is fifteen characters: a two-digit state code, the holder's PAN, an
entity number, a default 'Z', and a CHECK CHARACTER computed from the first
fourteen by the mod-36 scheme GSTN publishes (the Luhn mod-N algorithm over the
alphabet 0-9A-Z). ``redaction/recognizers.gstin_structurally_valid`` checks the
SHAPE; this checks the ARITHMETIC, which is what catches an OCR misread — one
wrong character in the first fourteen changes the check character with
certainty for single substitutions.

⛔ IT RUNS ON THE RAW OCR, BEFORE THE PII BOUNDARY, AND ITS RESULT CARRIES NO
VALUE. The GSTIN is masked before any model sees it (the boundary working as
designed), so the only place the checksum CAN be computed is here, in-process,
India-side. What leaves is a count and an outcome — never the GSTIN. See
pipeline.py's call site.

⚠️ THE CORPUS HAD TO CHANGE FOR THIS TO BE TRUE. The benchmark corpus used
``27ABCDE1234F1Z5``, which is structurally a GSTIN and arithmetically not one
(its check character is ``0``). A validator that passed it would be a validator
that checks nothing; one that failed it made every clean corpus bill a review.
The corpus now uses ``27ABCDE1234F1Z0`` — still fictitious, now internally
consistent — and tests/test_validators.py pins both spellings.
"""

from __future__ import annotations

from ..redaction import recognizers

_ALPHABET = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"


def check_character(first14: str) -> str | None:
    """The GSTIN check character for the first fourteen characters, or None
    when they contain a character outside 0-9A-Z."""
    if len(first14) != 14:
        return None
    total = 0
    for i, ch in enumerate(first14):
        v = _ALPHABET.find(ch)
        if v < 0:
            return None
        # Factors alternate 1, 2, 1, 2 … from the LEFT; each product is folded
        # back into base 36 (quotient + remainder) before summing.
        p = v * (1 if i % 2 == 0 else 2)
        total += p // 36 + p % 36
    return _ALPHABET[(36 - total % 36) % 36]


def gstin_checksum_valid(g: str) -> bool:
    """True when ``g`` is structurally a GSTIN AND its check character is right."""
    g = g.strip().upper()
    if not recognizers.gstin_structurally_valid(g):
        return False
    return check_character(g[:14]) == g[14]


def validate_document_gstins(raw_text: str):
    """Validate every GSTIN-shaped token in a document's RAW text.

    Returns a ``Validation`` whose detail is COUNTS ONLY. ``fail`` when any
    GSTIN on the document fails its checksum — the supplier's or the buyer's,
    because the recogniser cannot tell which is which and a misread in either
    means the OCR of that region is not to be trusted; ``not_run`` when the
    document carries none (the mapper's own "no supplier GSTIN" defect already
    covers that case).
    """
    from ..contracts.draft import Validation

    found = [m.value for m in recognizers.detect(raw_text) if m.type == "GSTIN"]
    # A GSTIN-SHAPED token that the recogniser refused (bad state code, etc.) is
    # not counted: it is not a GSTIN at all, and the structural gate is the
    # recogniser's decision, not this one's.
    if not found:
        return Validation(
            check="gstin_checksum",
            outcome="not_run",
            field="supplier",
            detail="no GSTIN found on the document",
        )
    bad = [g for g in found if not gstin_checksum_valid(g)]
    if bad:
        return Validation(
            check="gstin_checksum",
            outcome="fail",
            field="supplier",
            detail=(
                f"{len(bad)} of {len(found)} GSTIN(s) on the document fail the check character — "
                "a character was misread; the supplier must be confirmed by a human"
            ),
        )
    return Validation(
        check="gstin_checksum",
        outcome="pass",
        field="supplier",
        detail=f"{len(found)} GSTIN(s) on the document carry a valid check character",
    )
