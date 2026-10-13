"""Per-field provenance: WHERE on the document each draft value was found.

Design memo: docs/design/d123-w5-documents-bank.md §3.2.

WHAT THIS GIVES THE OPERATOR, AND WHAT IT GIVES THE REVIEW DECISION
------------------------------------------------------------------
For the operator: ``{value, page, bbox, confidence}`` per field, so the Bill
Inbox can put their eye on the one line of the scan the number came from
instead of asking them to re-read the page. That is what "exception-only
review" costs in practice — a field count, and a box to look at for each.

For the review decision: a value the model returned that appears NOWHERE in the
OCR text is a value the model did not read. That is the WS10 spike's decisive
case in reverse: OCR produced ``300:00``, and a model "helpfully" normalising it
to ``300.00`` produces a clean-looking number with no evidence under it. The
search here is literal, so that number is UNGROUNDED, and for the invoice total
— which every bill prints — that is a ``fail`` that sends the draft to a human.

THE SEARCH IS DETERMINISTIC AND DELIBERATELY NARROW
---------------------------------------------------
  * money is matched as a NUMBER: every numeric token on a line is parsed
    (lakh commas stripped) and compared with ``Decimal`` equality, so
    ``1,18,000.00`` grounds ``118000.0000`` and ``300:00`` grounds nothing;
  * a date is matched against the renderings Indian bills actually print
    (dd/mm/yyyy, dd-mm-yyyy, dd.mm.yyyy, dd/mm/yy, dd-Mon-yyyy, dd Mon yyyy,
    yyyy-mm-dd) of the ONE date the model returned — never parsed out of the
    line, which would reopen the dd/mm vs mm/dd question;
  * text (invoice number, HSN, party) is a case-insensitive whole-token match;
  * the invoice TOTAL, on a bill that labels one, is matched only on the
    labelled line (or the line under a bare label) — any-line matching let a
    quantity, a rate or an HSN code "ground" a misread total.

A model-claimed location (``InferredInvoice.field_evidence``) is used only when
this search finds nothing, and is marked ``source="model"``, ungrounded.

It searches the REDACTED OCR — the text the model saw — and copies no OCR text
into the draft. Pure python; no model, no network.
"""

from __future__ import annotations

import datetime as _dt
import re
from decimal import Decimal, InvalidOperation

from .contracts.draft import FieldProvenance, Validation

# A number token that is NOT a piece of a larger compound. ⛔ Without the
# lookarounds, OCR's "300:00" yielded the token "300", which equals 300.00 —
# the exact WS10 misread grounded itself (caught by
# test_the_ws10_misread_is_ungrounded...). A digit run glued to another by
# ':', '/', '.', or '-' (a misread separator, a date, a range) is no token.
_NUM = re.compile(r"(?<![\w:/])(?<!\d[.\-])-?\d[\d,]*(?:\.\d+)?(?![\w]|[.:/\-]\d)")


def _numbers(text: str) -> list[Decimal]:
    out = []
    for m in _NUM.finditer(text):
        try:
            out.append(Decimal(m.group(0).replace(",", "")))
        except InvalidOperation:
            continue
    return out


def _date_renderings(iso: str) -> list[str]:
    try:
        d = _dt.date.fromisoformat(iso)
    except (TypeError, ValueError):
        return []
    mon = d.strftime("%b")
    return [
        d.strftime("%d/%m/%Y"), d.strftime("%d-%m-%Y"), d.strftime("%d.%m.%Y"),
        d.strftime("%d/%m/%y"), f"{d.day:02d}-{mon}-{d.year}", f"{d.day:02d} {mon} {d.year}",
        f"{d.day} {mon} {d.year}", d.isoformat(),
    ]


def _token_in(needle: str, hay: str) -> bool:
    n = re.sub(r"\s+", " ", needle.strip()).casefold()
    if not n:
        return False
    h = re.sub(r"\s+", " ", hay).casefold()
    return re.search(r"(?<![\w])" + re.escape(n) + r"(?![\w])", h) is not None


def _locate(ocr, match) -> FieldProvenance | None:
    for pidx, page in enumerate(ocr.pages):
        for line in page.lines:
            if match(line.text):
                return FieldProvenance(
                    page=pidx, bbox=list(line.bbox), confidence=float(line.confidence),
                    grounded=True, source="ocr",
                )
    return None


def ground(fields: dict[str, tuple[str, str | None]], ocr, evidence=None) -> dict[str, FieldProvenance]:
    """Locate each field on the document.

    ``fields`` maps a field path to ``(kind, value)`` where kind is ``money``,
    ``date`` or ``text``. ``evidence`` is the model's optional
    ``field_evidence``. A field with no value gets no entry.
    """
    evidence = evidence or {}
    out: dict[str, FieldProvenance] = {}
    for path, (kind, value) in fields.items():
        if value in (None, ""):
            continue
        prov = None
        if ocr is not None:
            if kind == "money":
                try:
                    want = Decimal(str(value))
                except InvalidOperation:
                    want = None
                if want is not None:
                    if path == "totals" and document_labels_a_total(ocr):
                        # ⛔ THE TOTAL MUST BE FOUND ON THE TOTAL'S LINE. Matched
                        # against every line, a model that returned the line
                        # quantity, a rate, the HSN code or the GST percentage
                        # as the invoice total was "grounded" on that line and
                        # the operator was shown a box around it (wave-1
                        # review). A bill that labels its total is held to it.
                        prov = _locate_total(ocr, want)
                    else:
                        prov = _locate(ocr, lambda t, w=want: w in _numbers(t))
            elif kind == "date":
                renders = _date_renderings(value)
                prov = _locate(ocr, lambda t, r=renders: any(_token_in(x, t) for x in r))
            else:
                prov = _locate(ocr, lambda t, v=value: _token_in(v, t))
        if prov is None and path in evidence:
            ev = evidence[path]
            prov = FieldProvenance(page=ev.page, bbox=list(ev.bbox), confidence=0.0,
                                   grounded=False, source="model")
        if prov is None:
            prov = FieldProvenance(grounded=False, source="none")
        prov.value = str(value)
        out[path] = prov
    return out


# A line that LABELS the bill's total. When the document has one and the
# model's total is on none of the document's lines, the model's number did not
# come from the bill: that is a FAIL. When the document prints no total label at
# all (a torn page; the synthetic corpus, whose OCR blocks carry no total line),
# there is no evidence either way and the field is only ``unverified`` — failing
# it would make "no total printed" indistinguishable from "total misread".
_TOTAL_LABEL = re.compile(r"(?i)\b(grand\s+total|invoice\s+total|total\s+amount|amount\s+payable|net\s+amount|total)\b")


def _locate_total(ocr, want: Decimal) -> FieldProvenance | None:
    """``want`` on a line that labels the total — or, where the label stands
    alone with no number (a two-line layout: "Grand Total" / "1,18,000.00"),
    on the line directly after it."""
    for pidx, page in enumerate(ocr.pages):
        label_alone = False
        for line in page.lines:
            labelled = _TOTAL_LABEL.search(line.text) is not None
            if (labelled or label_alone) and want in _numbers(line.text):
                return FieldProvenance(
                    page=pidx, bbox=list(line.bbox), confidence=float(line.confidence),
                    grounded=True, source="ocr",
                )
            label_alone = labelled and not _numbers(line.text)
    return None


def document_labels_a_total(ocr) -> bool:
    if ocr is None:
        return False
    return any(_TOTAL_LABEL.search(line.text) for page in ocr.pages for line in page.lines)


def grounding_validations(prov: dict[str, FieldProvenance], ocr=None) -> list[Validation]:
    must_ground = {"totals"} if document_labels_a_total(ocr) else set()
    out = []
    for path, p in sorted(prov.items()):
        if p.grounded:
            out.append(Validation(check="grounded", outcome="pass", field=path,
                                  detail=f"found on page {p.page + 1}"))
        elif path in must_ground:
            out.append(Validation(check="grounded", outcome="fail", field=path,
                                  detail="the value was not found on the line that labels the bill's total — "
                                         "the model did not read it off the bill's total"))
        else:
            out.append(Validation(check="grounded", outcome="unverified", field=path,
                                  detail="not found verbatim in the document's text"))
    return out
