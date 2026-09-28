"""A deterministic regex extractor — the FLOOR the extraction scorer is proven against.

⚠️ THIS IS NOT "THE CURRENT PIPELINE". PLAN-AGENTIC W5's gate compares an
open-weight extractor with the current Opus pipeline, and that comparison needs
a live provider (W1b). This extractor exists for three narrower reasons:

  1. it proves the labels are REACHABLE from the text — a label no rule can
     find is one no model can find either, and it would cap every score;
  2. it gives the scorer a non-trivial, non-perfect input, so a regression that
     made the scorer return 1.0 (or 0.0) for everything is visible;
  3. it is the number a model must beat to be worth its tokens.

It reads labelled lines ("Invoice No.:", "IFSC:") and '|'-separated tables by
their header names. It knows nothing of any particular fixture.
"""

from __future__ import annotations

import re

_GSTIN = re.compile(r"\b\d{2}[A-Z]{5}\d{4}[A-Z][1-9A-Z]Z[0-9A-Z]\b")
_AMOUNT = r"(-?[\d,]+\.\d{2})"


def _after(text: str, labels: list[str], pattern: str = r"(.+?)(?:\s{2,}|$)") -> str:
    for lab in labels:
        m = re.search(re.escape(lab) + r"\s*" + pattern, text, re.MULTILINE)
        if m:
            return m.group(1).strip()
    return ""


def _invoice(text: str) -> dict:
    lines = text.splitlines()
    gstins = _GSTIN.findall(text)
    f = {
        "supplier_name": lines[1].strip("* ").strip() if len(lines) > 1 else "",
        "supplier_gstin": gstins[0] if gstins else "",
        "buyer_gstin": gstins[1] if len(gstins) > 1 else "",
        "invoice_no": _after(text, ["Invoice No.:", "Bill No:", "Invoice #:"], r"(\S+)"),
        "invoice_date": _after(text, ["Dated:", "Dt:", "Date of Invoice:"]),
        "buyer_name": _after(text, ["Buyer (Bill to):", "Cust:", "Billed To:"]),
        "place_of_supply": _after(text, ["Place of Supply:", "POS:"], r"(\d{2})"),
        "taxable_value": _after(text, ["Taxable Value:", "Taxable", "Sub-total:"], _AMOUNT),
        "cgst": _after(text, ["CGST:", "CGST", "Add: CGST"], _AMOUNT),
        "sgst": _after(text, ["SGST:", "SGST", "Add: SGST"], _AMOUNT),
        "igst": _after(text, ["IGST:", "Add: IGST"], _AMOUNT),
        "round_off": _after(text, ["Round Off:", "Round off"], _AMOUNT),
        "invoice_total": _after(text, ["Total: ₹", "NET AMT", "Invoice Total (INR):"], _AMOUNT),
    }
    out_lines = []
    for ln in lines:
        cells = [c.strip() for c in ln.split("|")]
        # classic table: Sl | Description | HSN | Qty Unit | Rate | per | Amount
        if len(cells) == 7 and cells[0].isdigit():
            qty, _, unit = cells[3].partition(" ")
            out_lines.append({"description": cells[1], "hsn": cells[2], "qty": qty, "unit": unit,
                              "rate": cells[4], "amount": cells[6]})
    return {"fields": f, "lines": out_lines}


_COLS = [
    ("date", ("Txn Date", "Tran Date", "Transaction Date", "Date")),
    ("narration", ("Narration", "Transaction Remarks", "Description", "PARTICULARS")),
    ("ref", ("Chq./Ref.No.", "Cheque Number", "Ref No./Cheque No.", "CHQNO", "Chq/Ref No")),
    ("debit", ("Withdrawal", "Debit", "DR")),
    ("credit", ("Deposit", "Credit", "CR")),
    ("balance", ("Closing Balance", "Balance", "BAL")),
]

_BANK_RE = re.compile(r"NOT ISSUED BY (.+?) —")


def _statement(text: str) -> dict:
    f = {
        "account_number": _after(text, ["Account No:"], r"(\d+)"),
        "ifsc": _after(text, ["IFSC:"], r"([A-Z]{4}0[A-Z0-9]{6})"),
        "statement_from": _after(text, ["Statement From:"]),
        "statement_to": _after(text, ["To:"]),
        "opening_balance": _after(text, ["Opening Balance:"], r"([\d,]+\.\d{2})"),
        "closing_balance": _after(text, ["Closing Balance:"], r"([\d,]+\.\d{2})"),
        "bank": (_BANK_RE.search(text).group(1) if _BANK_RE.search(text) else ""),
    }
    rows = [ln for ln in text.splitlines() if "|" in ln]
    if not rows:
        return {"fields": f, "lines": []}
    head = [c.strip() for c in rows[0].split("|")]
    idx: dict[str, int] = {}
    for name, keys in _COLS:
        for i, h in enumerate(head):
            if i in idx.values():
                continue
            if any(h == k or h.startswith(k + " ") or h.startswith(k + "(") or (k in h and len(k) > 3)
                   for k in keys):
                idx[name] = i
                break
    out = []
    for ln in rows[1:]:
        cells = [c.strip() for c in ln.split("|")]
        if len(cells) != len(head):
            continue
        out.append({k: cells[i] for k, i in idx.items()})
    return {"fields": f, "lines": out}


def extract(text: str, kind: str) -> dict:
    return _invoice(text) if kind == "invoice" else _statement(text)
