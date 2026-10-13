"""Field-level F1 for document extraction (W1a, d124).

WHAT IS COUNTED. Every labelled field of every document — header fields
(supplier GSTIN, invoice date, totals; account number, opening balance) and
every field of every line (an invoice's item lines, a statement's transactions)
— is one decision. After normalisation:

    TP  gold non-empty, prediction equal
    FN  gold non-empty, prediction missing OR different
    FP  prediction non-empty, and missing from gold OR different

so a WRONG value costs twice (an FP and an FN), and that is deliberate: an
extractor that reads the CGST figure into the IGST field has made two errors a
reviewer must undo, not one.

⚠️ A ZERO AMOUNT IS "EMPTY". An intra-State invoice has no IGST; its label is
0.00 and a model that leaves the field blank has not missed anything. A model
that states a NON-zero IGST on it has invented a figure, and that is an FP.

WHY NORMALISE, AND WHY NOT MORE. The same invoice date arrives as 12/06/2025,
12-Jun-2025 and June 12, 2025; the same total as 1,18,000.00, 118,000.00 and
₹ 118000/-. None of those differences is an extraction error, and a scorer that
counted them would rank models by how they punctuate. Beyond that the
normalisers stop: "Acme Traders" and "Acme Trading Co" are DIFFERENT parties,
and fuzzy name matching here would score the exact error that posts a bill to
the wrong ledger as a hit.

LINES are aligned before they are scored: each gold line is paired with at most
one predicted line, greedily by the number of fields that agree (ties broken by
position), so a model that returns the right lines in a different order is not
punished, and one that splits or merges lines pays for every field it lost.
"""

from __future__ import annotations

import json
import re
from dataclasses import dataclass, field
from datetime import date
from decimal import Decimal, InvalidOperation
from pathlib import Path
from typing import Any, Iterable

from . import CORPORA

EXTRACTION_DIR = CORPORA / "extraction"

HEADER_TYPES = {
    # invoices
    "supplier_name": "text", "supplier_gstin": "code", "buyer_name": "text", "buyer_gstin": "code",
    "invoice_no": "code", "invoice_date": "date", "place_of_supply": "state",
    "taxable_value": "money", "cgst": "money", "sgst": "money", "igst": "money",
    "round_off": "money", "invoice_total": "money",
    # bank statements
    "bank": "text", "account_number": "code", "ifsc": "code", "statement_from": "date",
    "statement_to": "date", "opening_balance": "money", "closing_balance": "money",
}
LINE_TYPES = {
    "invoice": {"description": "text", "hsn": "code", "qty": "number", "unit": "text", "rate": "money",
                "amount": "money", "gst_rate_pct": "number"},
    "bank_statement": {"date": "date", "narration": "text", "ref": "code", "debit": "money",
                       "credit": "money", "balance": "money"},
}

_STATE_CODES = {
    "maharashtra": "27", "karnataka": "29", "gujarat": "24", "delhi": "07", "tamil nadu": "33",
    "uttar pradesh": "09", "west bengal": "19", "telangana": "36", "kerala": "32", "rajasthan": "08",
}
_MONTHS = {m: i for i, m in enumerate(
    ["jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"], 1)}


def _money(v: Any) -> str:
    s = str(v).strip()
    s = re.sub(r"(?i)(₹|rs\.?|inr|/-|\s|,|dr$|cr$)", "", s)
    if not s:
        return ""
    try:
        d = Decimal(s).quantize(Decimal("0.01"))
    except InvalidOperation:
        return "INVALID:" + str(v)
    return "" if d == 0 else str(d)


def _number(v: Any) -> str:
    s = str(v).strip().replace(",", "").rstrip("%")
    if not s:
        return ""
    try:
        return str(Decimal(s).normalize())
    except InvalidOperation:
        return "INVALID:" + str(v)


def _date(v: Any) -> str:
    """Day-first, always: this is an Indian document, and 05/06/2025 is 5 June."""
    s = str(v).strip()
    if not s:
        return ""
    m = re.fullmatch(r"(\d{4})-(\d{2})-(\d{2})", s)
    if m:
        y, mo, d = map(int, m.groups())
        return _iso(y, mo, d, s)
    m = re.fullmatch(r"(\d{1,2})[/.\-](\d{1,2})[/.\-](\d{2}|\d{4})", s)
    if m:
        d, mo, y = map(int, m.groups())
        return _iso(y + 2000 if y < 100 else y, mo, d, s)
    m = re.fullmatch(r"(\d{1,2})[\s\-]([A-Za-z]{3,9})[\s\-,]*(\d{4})", s)
    if m:
        return _iso(int(m.group(3)), _MONTHS.get(m.group(2)[:3].lower(), 0), int(m.group(1)), s)
    m = re.fullmatch(r"([A-Za-z]{3,9})\s+(\d{1,2}),?\s+(\d{4})", s)
    if m:
        return _iso(int(m.group(3)), _MONTHS.get(m.group(1)[:3].lower(), 0), int(m.group(2)), s)
    return "INVALID:" + s


def _iso(y: int, mo: int, d: int, raw: str) -> str:
    try:
        return date(y, mo, d).isoformat()
    except ValueError:
        return "INVALID:" + raw


def _code(v: Any) -> str:
    s = re.sub(r"\s", "", str(v)).upper()
    return "" if s in ("", "0") else s


def _text(v: Any) -> str:
    s = str(v).casefold().replace("&", " and ")
    s = re.sub(r"[^\w\s]", " ", s)
    return re.sub(r"\s+", " ", s).strip()


def _state(v: Any) -> str:
    s = str(v).strip()
    m = re.match(r"(\d{2})", s)
    if m:
        return m.group(1)
    return _STATE_CODES.get(s.casefold(), _text(s))


NORMALISERS = {"money": _money, "number": _number, "date": _date, "code": _code, "text": _text,
               "state": _state}


def normalise(kind: str, value: Any) -> str:
    if value is None:
        return ""
    return NORMALISERS[kind](value)


@dataclass
class Counts:
    tp: int = 0
    fp: int = 0
    fn: int = 0

    def add(self, other: "Counts") -> None:
        self.tp += other.tp
        self.fp += other.fp
        self.fn += other.fn

    @property
    def precision(self) -> float:
        return self.tp / (self.tp + self.fp) if self.tp + self.fp else 0.0

    @property
    def recall(self) -> float:
        return self.tp / (self.tp + self.fn) if self.tp + self.fn else 0.0

    @property
    def f1(self) -> float:
        p, r = self.precision, self.recall
        return 2 * p * r / (p + r) if p + r else 0.0


def _decide(kind: str, gold: Any, pred: Any) -> Counts:
    g, p = normalise(kind, gold), normalise(kind, pred)
    c = Counts()
    if g and p == g:
        c.tp = 1
        return c
    if g:
        c.fn = 1
    if p:
        c.fp = 1
    return c


def _align(gold: list[dict], pred: list[dict], types: dict[str, str]) -> list[tuple[int | None, int | None]]:
    def agree(g: dict, p: dict) -> int:
        n = 0
        for f, k in types.items():
            gv = normalise(k, g.get(f))
            if gv and gv == normalise(k, p.get(f)):
                n += 1
        return n

    scored = sorted(((-agree(g, p), gi, pi) for gi, g in enumerate(gold) for pi, p in enumerate(pred)))
    gfree, pfree, pairs = set(range(len(gold))), set(range(len(pred))), []
    for neg, gi, pi in scored:
        if neg < 0 and gi in gfree and pi in pfree:
            pairs.append((gi, pi))
            gfree.discard(gi)
            pfree.discard(pi)
    pairs += [(gi, None) for gi in sorted(gfree)] + [(None, pi) for pi in sorted(pfree)]
    return pairs


@dataclass
class Report:
    overall: Counts = field(default_factory=Counts)
    header: Counts = field(default_factory=Counts)
    lines: Counts = field(default_factory=Counts)
    by_field: dict[str, Counts] = field(default_factory=dict)
    by_kind: dict[str, Counts] = field(default_factory=dict)
    documents: int = 0

    def _bump(self, key: str, kind: str, c: Counts, is_line: bool) -> None:
        self.overall.add(c)
        (self.lines if is_line else self.header).add(c)
        self.by_field.setdefault(key, Counts()).add(c)
        self.by_kind.setdefault(kind, Counts()).add(c)

    def summary(self) -> dict[str, float]:
        out = {"documents": self.documents, "micro_f1": round(self.overall.f1, 6),
               "header_f1": round(self.header.f1, 6), "line_f1": round(self.lines.f1, 6),
               "precision": round(self.overall.precision, 6), "recall": round(self.overall.recall, 6)}
        for k, c in sorted(self.by_kind.items()):
            out[f"{k}_f1"] = round(c.f1, 6)
        return out


def score_document(doc: dict, pred: dict | None, report: Report) -> None:
    """Score one document's prediction ({"fields": {...}, "lines": [...]})."""
    pred = pred or {}
    kind = doc["kind"]
    gold = doc["labels"]
    report.documents += 1
    pf = pred.get("fields") or {}
    for f in sorted(set(gold["fields"]) | set(pf)):
        if f not in HEADER_TYPES:
            # A field the schema does not know is still a claim the model
            # made; it is an FP if non-empty, never silently ignored.
            if str(pf.get(f) or "").strip():
                report._bump(f, kind, Counts(fp=1), False)
            continue
        report._bump(f, kind, _decide(HEADER_TYPES[f], gold["fields"].get(f), pf.get(f)), False)
    types = LINE_TYPES[kind]
    gl, pl = gold["lines"], pred.get("lines") or []
    for gi, pi in _align(gl, pl, types):
        g = gl[gi] if gi is not None else {}
        p = pl[pi] if pi is not None else {}
        for f, k in types.items():
            report._bump("line." + f, kind, _decide(k, g.get(f), p.get(f)), True)


def load_documents(directory: Path = EXTRACTION_DIR) -> list[dict]:
    docs = [json.loads(p.read_text(encoding="utf-8")) for p in sorted(directory.glob("*.json"))]
    if not docs:
        raise FileNotFoundError(f"no extraction fixtures under {directory}")
    return docs


def evaluate(predict, docs: Iterable[dict] | None = None) -> Report:
    """Score `predict(document_text, kind) -> {"fields":..., "lines":...}` over the set.

    `predict` sees the document TEXT and its kind, never the labels.
    """
    report = Report()
    for doc in docs if docs is not None else load_documents():
        score_document(doc, predict(doc["document"]["text"], doc["kind"]), report)
    return report
