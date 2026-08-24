"""The SECOND redaction boundary — the one on the path that WRITES TO THE BOOKS.

``boundary.py`` masks PII on the way OUT of the country (the cross-border LLM
call). This module masks PII on the way IN to the LEDGER, and it exists because
those are two different paths and only one of them was guarded.

⛔⛔ DRIVEN, NOT HYPOTHETICAL. A PAN placed in the OCR-inferred invoice number was
written verbatim into ``voucher_version.voucher_no``, and a raw Aadhaar in a line
description became a PERMANENT LEDGER MASTER NAME — while the F9 draft panel told
the operator "PII redacted: 4 (PAN 1, IFSC 1, GSTIN 1, AADHAAR 1)". The panel's
claim was true about the text handed to the model and FALSE about exactly the
values that persist. A master name cannot be un-created and a posted voucher
cannot be un-posted; the ledger is append-only by design, so this is the one
place where "we will scrub it later" is not available.

⛔ THE CLASS, AND WHY THIS IS NOT A FIX AT THE TWO REPORTED CALL SITES.
The class is "a value the pipeline redacted for DISPLAY is un-redacted on the
path that PERSISTS". Fixing ``invoice_no`` and ``description`` would leave
``party``, ``narration``, ``postings[].ledger_name`` and every field a later wave
adds. So this module does not take a list of fields — it CLASSIFIES every
string-bearing field of the draft body, and a field it cannot classify is a hard
error (see :func:`classify_field` and ``test_pii_persistence.py``, which walks the
pydantic models and fails on an unclassified field). That is the same shape as the
authorization-peek gate in HANDOFF-PARITY-AUDIT.md §7.2: assert the PROPERTY and
make a new field ACCOUNT for itself, because four rounds of "here is the list of
hostile inputs" were each bypassed by the next input off the list.

⛔⛔ AND THE OTHER HALF OF THE PROPERTY: MONEY IS NEVER SCRUBBED.
``AADHAAR_RE`` is ``\\b\\d{12}\\b`` gated on a Verhoeff checksum, and a fixed-4
money string such as ``"180000000000.0000"`` contains a 12-digit run with a word
boundary on each side. Running the recognizers over a money field would therefore
one day replace an AMOUNT with a placeholder — silently, and only for some
amounts. Money, dates, uuids and 2-digit state codes are classified STRUCTURED
and are never touched; only free text a human reads is scrubbed.

The placeholder is deliberately NOT the boundary's ``<<PAN:ab12>>`` co-reference
tag. Nobody co-references a voucher number: this tag is read by an OPERATOR who
has to retype the field, so it says what was removed in words, and it cannot be
mistaken for a real invoice number or a real ledger name. Every scrubbed field is
also reported in ``unresolved`` by the mapper, which degrades the draft to
``low_confidence_manual`` — a redacted voucher number must never be auto-ready.

⛔⛔ AND THE TAG CARRIES A FINGERPRINT, BECAUSE THE WORDS ALONE REFUSED REAL BILLS.
That is D85, and it was found by DRIVING, not by reading. A tag of words only is a
CONSTANT: every PII-shaped invoice number collapsed to the one string
``[PAN REDACTED]``. The client's duplicate-supplier-bill guard
(``draftAccept.ts``'s ``assertNotAlreadyOnTheBooks``) asks the DAY BOOK whether a
purchase for this party, on this date, under this ``voucher_no`` is already
posted — and the day book stores only ``voucher_no``. So two bills from one
supplier on one date whose numbers were DIFFERENT PANs were the SAME bill to that
check, and the second was refused, advising the operator to *"key its own supplier
invoice number"* — which could not work, because any number that redacts landed on
the same token. ⛔ THE DISCRIMINATOR THEREFORE HAS TO LIVE IN THE VALUE THAT
PERSISTS: a hash carried alongside the draft cannot close that layer, because the
day book has no field to carry it.

⛔ IT IS UNSALTED ON PURPOSE — THE OPPOSITE CHOICE FROM ``boundary.py``, WHICH
SALTS PER REQUEST. There, unlinkability across documents is the point. Here the
guard REQUIRES linkability: re-uploading the SAME bill must produce the SAME
``voucher_no``, or the day-book layer stops recognising the duplicate and the money
defect that layer exists to stop comes back. A per-request salt here would silently
disable one of the five layers while every test still passed.

⛔ AND IT IS DELIBERATELY SHORT (``_FINGERPRINT_HEX``). It only has to tell apart
the handful of bills one supplier issues on one date; every extra character is
extra help to anyone brute-forcing the removed value back out of the append-only
books. Four hex characters leave roughly ten thousand issued PANs per fingerprint.
⚠️ THE RESIDUAL, STATED RATHER THAN HIDDEN: for an entity whose REAL domain is
small — an IFSC, with ~1.7e5 live branch codes — a fingerprint of any usable width
narrows it to a couple of candidates, so such a value is protected by the REMOVAL
of the value and not by the fingerprint. ⛔ Do not widen this, and never log a
fingerprint beside the party name.

Pure-python — no optional deps, deterministic, no salt (a salted hash would make
two runs of the same document produce two different ledger names, and would break
the day-book guard described above).
"""

from __future__ import annotations

import hashlib

from . import recognizers
from .boundary import PIIBoundaryViolation


class PersistedPIIViolation(PIIBoundaryViolation):
    """Raw PII survived into a value bound for the LEDGER. Fail-closed.

    A SUBCLASS of the cross-border violation rather than the same class, because
    the two refusals mean different things and the operator-facing sentence must
    not say "the cross-border call is refused" about a value that was never going
    abroad. Anything already catching ``PIIBoundaryViolation`` still catches this.

    ⚠️ Reaching this is a PROGRAMMING error, not a document problem: it means a
    field was assembled without going through :class:`PersistGuard`. Refusing the
    draft is the right answer anyway — the alternative is writing the PII — and
    ``routes_drafts._run_and_store`` turns it into a visible
    ``low_confidence_manual`` draft carrying the reason on the background path.
    """

    def __init__(self, found: dict[str, list[str]]) -> None:
        types = ", ".join(sorted(found))
        self.found = found
        # Skip the parent's message, keep its `found` contract.
        Exception.__init__(
            self,
            f"persisted-value PII violation: raw {types} would have been written "
            f"to the books (values withheld). The draft is refused.",
        )


class UnclassifiedPersistedField(Exception):
    """A draft-body field exists that nobody decided was text or structured.

    Fail-closed ON PURPOSE. The alternative — defaulting to "structured, do not
    scrub" — is how the next OCR-derived string field silently becomes the next
    permanent PII-bearing master name; and defaulting the other way would one day
    scrub a money value. Neither default is safe, so there is none.
    """


#: Fields whose value is FREE TEXT read by a human: they are OCR/LLM output, they
#: can contain anything the document contained, and they end up as a voucher
#: number, a party name, a narration or a LEDGER MASTER NAME. Scrubbed.
TEXT_FIELDS: frozenset[str] = frozenset(
    {
        "party",  # supplier display name -> Alt+C creates a master with it
        "voucher_no",  # the supplier's own invoice number -> voucher_version.voucher_no
        "narration",  # free text, stored verbatim on the voucher
        "ledger_name",  # ⛔ PostingDraft: becomes a PERMANENT ledger master
        "hsn",  # OCR-read tariff code; free text until a human confirms it
    }
)

#: Fields whose value is STRUCTURED — money, an ISO date, a uuid, a 2-digit GST
#: state code, an enum. ⛔ NEVER scrubbed: see the money warning in the module
#: docstring. These carry no free text, so they cannot carry PII either.
STRUCTURED_FIELDS: frozenset[str] = frozenset(
    {
        "voucher_class",
        "voucher_type_id",
        "party_ledger_id",
        "voucher_date",
        "ledger_id",
        "dr_cr",
        "amount",
        "taxable_value",
        "gst_rate_pct",
        "ruleset_version",
        "supplier_state_code",
        "place_of_supply_state_code",
        "reverse_charge",
        "exempt",
        "cgst_ledger_id",
        "sgst_ledger_id",
        "igst_ledger_id",
        # nested models — walked, not scrubbed as values
        "postings",
        "gst",
        "lines",
    }
)


def classify_field(name: str) -> str:
    """Return "text" or "structured" for a draft-body field, or raise.

    ⛔ The raise is the gate. ``test_pii_persistence.py`` walks every pydantic
    model in the draft body and calls this for every field, so adding a field to
    ``cored_map.py`` without deciding what it is turns the insightd suite RED
    rather than shipping an unguarded persisted value.
    """
    if name in TEXT_FIELDS:
        return "text"
    if name in STRUCTURED_FIELDS:
        return "structured"
    raise UnclassifiedPersistedField(
        f"draft-body field {name!r} is neither TEXT nor STRUCTURED. Decide: is it "
        "free text a human reads (add it to TEXT_FIELDS — it will be scrubbed of "
        "PII before it can become a voucher field or a master name), or is it "
        "money/date/uuid/code (add it to STRUCTURED_FIELDS — it must NEVER be "
        "scrubbed, because the Aadhaar recognizer matches a 12-digit run and would "
        "one day eat an amount)?"
    )


#: How many hex characters of the fingerprint reach the tag. ⛔ DO NOT WIDEN IT.
#: The width is a PRIVACY control, not a collision budget: four characters already
#: tell apart far more bills than one supplier issues on one date, and every extra
#: character narrows a brute-force search of the removed value. It happens to match
#: ``boundary.py``'s tag width; that is a coincidence and not a contract — that one
#: is salted per request and this one must never be (module docstring).
_FINGERPRINT_HEX = 4


def fingerprint(value: str) -> str:
    """A short, UNSALTED, deterministic fingerprint of a removed PII value.

    ⛔ Unsalted is load-bearing, not lazy. The day-book duplicate check keys on the
    persisted ``voucher_no`` and has nothing else to key on, so the SAME bill
    re-uploaded must redact to the SAME string. See the module docstring.
    """
    return hashlib.sha256(value.encode("utf-8")).hexdigest()[:_FINGERPRINT_HEX]


def placeholder(entity_type: str, value: str) -> str:
    """The tag written in place of a persisted PII value.

    WORDS FIRST, then a fingerprint. The words are for the operator, who has to
    look at this in the F9 form and retype the field — ``<<PAN:ab12>>`` tells them
    nothing about what to do. The fingerprint is for the duplicate-bill guard: it
    is derived from the value that was removed, so two DIFFERENT PANs no longer
    produce the same persisted voucher number (D85). It is not a co-reference tag
    and nothing resolves it back to a value.
    """
    return f"[{entity_type} REDACTED {fingerprint(value)}]"


def scrub(value: str | None) -> tuple[str | None, dict[str, int]]:
    """Mask every PII span in one FREE-TEXT value bound for the books.

    Returns the masked value and the per-type counts of what was removed. A value
    with no PII is returned unchanged and reports ``{}`` — so a caller can use an
    empty dict to mean "this field was clean" without comparing strings.
    """
    if not value:
        return value, {}
    matches = recognizers.detect(value)
    if not matches:
        return value, {}
    out: list[str] = []
    counts: dict[str, int] = {}
    cursor = 0
    for m in matches:
        out.append(value[cursor : m.start])
        out.append(placeholder(m.type, m.value))
        counts[m.type] = counts.get(m.type, 0) + 1
        cursor = m.end
    out.append(value[cursor:])
    return "".join(out), counts


class PersistGuard:
    """Accumulates what was scrubbed on the persistence path for ONE draft.

    The mapper threads one of these through assembly so that three things stay in
    agreement, which is the whole point of the defect this closes:

      * the VALUE that persists is masked;
      * the FIELD is named in ``unresolved`` (so the F9 panel tells the operator
        which box to retype, and the status decision degrades the draft to
        ``low_confidence_manual`` — a draft carrying a redacted voucher number
        must never present itself as "Ready — press Ctrl+A to accept");
      * the COUNT is folded into ``redaction.counts_by_type``, so the panel's
        "PII redacted: N (PAN 1, …)" line covers the values that persist rather
        than only the text that went to the model.
    """

    def __init__(self) -> None:
        self.counts: dict[str, int] = {}
        self.fields: list[str] = []

    def clean(self, field: str, value: str | None) -> str | None:
        """Scrub one field's value, recording the field name and the counts.

        ``field`` is the name the OPERATOR sees in the unresolved list, so it is
        the draft-facing path (``"invoice_no"``, ``"line[0].ledger_name"``), not
        the pydantic attribute name.
        """
        masked, counts = scrub(value)
        if not counts:
            return value
        for t, n in counts.items():
            self.counts[t] = self.counts.get(t, 0) + n
        if field not in self.fields:
            self.fields.append(field)
        return masked

    @property
    def any_redacted(self) -> bool:
        return bool(self.counts)

    def merged_counts(self, base: dict[str, int]) -> dict[str, int]:
        """Fold these counts into the OCR-boundary counts for the draft summary."""
        out = dict(base)
        for t, n in self.counts.items():
            out[t] = out.get(t, 0) + n
        return out


def assert_body_clean(body) -> None:
    """FAIL-CLOSED re-scan of an assembled draft body. The last line of defence.

    Mirrors ``Boundary.assert_clean`` for the other boundary: the scrub above is
    where PII is removed, and this is where we prove it was. It walks the model's
    TEXT fields only — walking everything would re-introduce the money hazard the
    module docstring warns about — and raises if a raw PAN/GSTIN/Aadhaar/IFSC
    survived into a value that is about to be stored and later posted.

    It is deliberately not a no-op even though ``PersistGuard`` already ran: a
    future field could be assembled from a source the mapper forgot to route
    through the guard, and the difference between "we scrub the fields we
    remembered" and "no PII can reach the books" is exactly the defect class.
    """
    found = recognizers.detect_all(list(_text_values(body)))
    if found:
        raise PersistedPIIViolation(found)


def _text_values(obj):
    """Yield every TEXT-classified string in a draft body, recursively.

    Every field is classified on the way past — an unclassified one raises — so
    this doubles as the structural gate: a new field in ``cored_map.py`` cannot
    reach the books without somebody deciding what it is.
    """
    dumped = obj.model_dump() if hasattr(obj, "model_dump") else obj
    if isinstance(dumped, dict):
        for name, value in dumped.items():
            kind = classify_field(name)
            if isinstance(value, (list, tuple)):
                for item in value:
                    yield from _text_values(item)
                continue
            if isinstance(value, dict):
                yield from _text_values(value)
                continue
            if kind == "text" and isinstance(value, str):
                yield value
        return
    if isinstance(dumped, (list, tuple)):
        for item in dumped:
            yield from _text_values(item)
