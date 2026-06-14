"""Pure-python PII detectors — the always-on redaction floor.

No presidio, no optional deps. These detect the entities that PLAN.md requires
to never leave India: PAN, GSTIN, Aadhaar, IFSC, and long bank-account digit
runs adjacent to an IFSC.

Recall over the planted-PII corpus is gated at 1.0 in CI; the Aadhaar Verhoeff
checksum gate keeps ordinary 12-digit invoice numbers from being false-redacted.
"""

from __future__ import annotations

import re
from dataclasses import dataclass

# --------------------------------------------------------------------------- #
# Patterns
# --------------------------------------------------------------------------- #

# PAN: 5 letters, 4 digits, 1 letter.
PAN_RE = re.compile(r"\b[A-Z]{5}[0-9]{4}[A-Z]\b")

# GSTIN: 2-digit state code + 10-char PAN + entity digit + 'Z' + checksum char.
GSTIN_RE = re.compile(r"\b[0-3][0-9][A-Z]{5}[0-9]{4}[A-Z][0-9A-Z]Z[0-9A-Z]\b")

# IFSC: 4 bank letters + mandatory '0' + 6 alphanumerics.
IFSC_RE = re.compile(r"\b[A-Z]{4}0[A-Z0-9]{6}\b")

# Candidate 12-digit Aadhaar (validated by Verhoeff below).
AADHAAR_RE = re.compile(r"\b\d{12}\b")

# A long digit run (9-18) that may be a bank account number; only treated as PII
# when an IFSC is also present nearby (see detect_all).
BANK_ACCT_RE = re.compile(r"\b\d{9,18}\b")

EntityType = str  # "PAN" | "GSTIN" | "AADHAAR" | "IFSC" | "BANK_ACCOUNT"
ENTITY_TYPES = ("PAN", "GSTIN", "AADHAAR", "IFSC", "BANK_ACCOUNT")


# --------------------------------------------------------------------------- #
# Verhoeff checksum (Aadhaar) — cuts false positives on plain 12-digit numbers.
# --------------------------------------------------------------------------- #

_VERHOEFF_D = (
    (0, 1, 2, 3, 4, 5, 6, 7, 8, 9),
    (1, 2, 3, 4, 0, 6, 7, 8, 9, 5),
    (2, 3, 4, 0, 1, 7, 8, 9, 5, 6),
    (3, 4, 0, 1, 2, 8, 9, 5, 6, 7),
    (4, 0, 1, 2, 3, 9, 5, 6, 7, 8),
    (5, 9, 8, 7, 6, 0, 4, 3, 2, 1),
    (6, 5, 9, 8, 7, 1, 0, 4, 3, 2),
    (7, 6, 5, 9, 8, 2, 1, 0, 4, 3),
    (8, 7, 6, 5, 9, 3, 2, 1, 0, 4),
    (9, 8, 7, 6, 5, 4, 3, 2, 1, 0),
)
_VERHOEFF_P = (
    (0, 1, 2, 3, 4, 5, 6, 7, 8, 9),
    (1, 5, 7, 6, 2, 8, 3, 0, 9, 4),
    (5, 8, 0, 3, 7, 9, 6, 1, 4, 2),
    (8, 9, 1, 6, 0, 4, 3, 5, 2, 7),
    (9, 4, 5, 3, 1, 2, 6, 8, 7, 0),
    (4, 2, 8, 6, 5, 7, 3, 9, 0, 1),
    (2, 7, 9, 3, 8, 0, 6, 4, 1, 5),
    (7, 0, 4, 6, 9, 1, 3, 2, 5, 8),
)


def verhoeff_valid(number: str) -> bool:
    """Return True if ``number`` (a digit string) passes the Verhoeff checksum.
    The Aadhaar 12-digit number embeds a Verhoeff check digit as its last digit."""
    if not number.isdigit():
        return False
    c = 0
    for i, ch in enumerate(reversed(number)):
        c = _VERHOEFF_D[c][_VERHOEFF_P[i % 8][int(ch)]]
    return c == 0


def verhoeff_checksum(payload: str) -> int:
    """Compute the Verhoeff check digit for an 11-digit payload (helper to mint
    valid synthetic Aadhaar numbers for the corpus)."""
    c = 0
    for i, ch in enumerate(reversed(payload)):
        c = _VERHOEFF_D[c][_VERHOEFF_P[(i + 1) % 8][int(ch)]]
    # inverse of c
    inv = (0, 4, 3, 2, 1, 5, 6, 7, 8, 9)
    return inv[c]


# --------------------------------------------------------------------------- #
# GSTIN structural validation — checks the embedded PAN + state-code range.
# --------------------------------------------------------------------------- #


def gstin_structurally_valid(g: str) -> bool:
    if len(g) != 15:
        return False
    state = g[:2]
    if not state.isdigit() or not (1 <= int(state) <= 38):
        return False
    pan = g[2:12]
    if not PAN_RE.fullmatch(pan):
        return False
    if g[13] != "Z":
        return False
    return True


# --------------------------------------------------------------------------- #
# Match model
# --------------------------------------------------------------------------- #


@dataclass(frozen=True)
class Match:
    type: EntityType
    value: str
    start: int
    end: int


def _find(pattern: re.Pattern[str], text: str, etype: str) -> list[Match]:
    return [Match(etype, m.group(0), m.start(), m.end()) for m in pattern.finditer(text)]


def detect(text: str) -> list[Match]:
    """Detect all PII matches in ``text``, longest-first, non-overlapping.

    GSTIN is matched before PAN so the PAN embedded inside a GSTIN is not
    double-redacted. Bank-account runs are only emitted when an IFSC co-occurs
    (an IFSC is the signal that a nearby long digit run is an account number).
    """
    raw: list[Match] = []
    raw += [m for m in _find(GSTIN_RE, text, "GSTIN") if gstin_structurally_valid(m.value)]
    raw += _find(PAN_RE, text, "PAN")
    raw += _find(IFSC_RE, text, "IFSC")
    raw += [m for m in _find(AADHAAR_RE, text, "AADHAAR") if verhoeff_valid(m.value)]

    has_ifsc = any(m.type == "IFSC" for m in raw)
    if has_ifsc:
        for m in _find(BANK_ACCT_RE, text, "BANK_ACCOUNT"):
            # Skip 12-digit runs that are valid Aadhaar (already covered) and any
            # run that is part of an already-found entity.
            raw.append(m)

    # Resolve overlaps: prefer longer matches; within equal length prefer the
    # earlier type-priority (GSTIN > PAN > IFSC > AADHAAR > BANK_ACCOUNT).
    priority = {t: i for i, t in enumerate(ENTITY_TYPES)}
    raw.sort(key=lambda m: (-(m.end - m.start), m.start, priority.get(m.type, 99)))

    chosen: list[Match] = []
    taken: list[tuple[int, int]] = []
    for m in raw:
        if any(not (m.end <= s or m.start >= e) for s, e in taken):
            continue
        chosen.append(m)
        taken.append((m.start, m.end))

    chosen.sort(key=lambda m: m.start)
    return chosen


def detect_all(texts) -> dict[str, list[str]]:
    """Detect across one string or any (possibly nested) iterable of strings.

    Returns a dict mapping entity type -> list of raw values found. Used by the
    boundary's ``assert_clean`` to re-scan an OUTGOING payload recursively.
    """
    out: dict[str, list[str]] = {t: [] for t in ENTITY_TYPES}
    for s in _iter_strings(texts):
        for m in detect(s):
            out[m.type].append(m.value)
    # drop empty buckets for a clean assert
    return {k: v for k, v in out.items() if v}


def _iter_strings(obj):
    if obj is None:
        return
    if isinstance(obj, str):
        yield obj
        return
    if isinstance(obj, dict):
        for v in obj.values():
            yield from _iter_strings(v)
        return
    if isinstance(obj, (list, tuple, set)):
        for v in obj:
            yield from _iter_strings(v)
        return
    # pydantic models / arbitrary objects — best-effort stringify of __dict__.
    if hasattr(obj, "model_dump"):
        yield from _iter_strings(obj.model_dump())
        return
    # primitives that aren't strings carry no PII text.
    return


def any_pii(texts) -> bool:
    return bool(detect_all(texts))
