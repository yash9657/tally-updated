#!/usr/bin/env python3
"""Write corpora/extraction/_poison/: extraction fixtures wrong in one named way each.

check_poison() in corpora/validate.py requires every one to be REJECTED. Each
is a real generated document with a single defect of the kind that sneaks into
a hand-labelled set: a label the page does not support, a total that does not
foot, a statement whose running balance breaks, a tax head moved to the wrong
column.

    python3 corpora/extraction/make_poison.py
"""

from __future__ import annotations

import copy
import importlib.util
import json
from decimal import Decimal
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("ext_generate", HERE / "generate.py")
gen = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gen)
DOCS = {d["id"]: d for d in gen.build()}


def poison(src: str, new_id: str, why: str, mutate) -> dict:
    d = copy.deepcopy(DOCS[src])
    d["id"] = new_id
    d["layout"] = d["layout"] + " (POISON: " + why + ")"
    mutate(d)
    return d


def ungrounded_invoice_no(d):
    d["labels"]["fields"]["invoice_no"] = "INV-DOES-NOT-EXIST"
    d["evidence"]["invoice_no"] = "INV-DOES-NOT-EXIST"


def total_not_footing(d):
    f = d["labels"]["fields"]
    new = f"{Decimal(f['invoice_total']) + 10:.2f}"
    f["invoice_total"] = new
    d["evidence"].pop("invoice_total", None)
    d["document"]["text"] += f"Grand Total (revised): {new}\n"
    d["evidence"]["invoice_total"] = new


def balance_break(d):
    ln = d["labels"]["lines"][2]
    ln["balance"] = f"{Decimal(ln['balance']) + 1:.2f}"


def tax_in_wrong_head(d):
    f = d["labels"]["fields"]
    f["igst"] = f"{Decimal(f['cgst']) + Decimal(f['sgst']):.2f}"
    f["cgst"] = f["sgst"] = "0.00"
    d["evidence"].pop("cgst", None)
    d["evidence"].pop("sgst", None)


def main() -> int:
    intra = next(i for i, d in sorted(DOCS.items()) if d["kind"] == "invoice"
                 and Decimal(d["labels"]["fields"]["cgst"]) > 0 and d["layout"] == "classic-table")
    poisons = [
        poison("inv-001", "poison-ungrounded-invoice-no", "label not supported by the page", ungrounded_invoice_no),
        poison("inv-002", "poison-total-does-not-foot", "total is taxable + tax + 10", total_not_footing),
        poison("bank-hdfc-01", "poison-running-balance-break", "third line's balance one rupee off", balance_break),
        poison(intra, "poison-intra-tax-labelled-igst", "intra-State tax labelled as IGST", tax_in_wrong_head),
    ]
    out = HERE / "_poison"
    out.mkdir(exist_ok=True)
    for p in out.glob("*.json"):
        p.unlink()
    for d in poisons:
        (out / f"{d['id']}.json").write_text(json.dumps(d, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"wrote {len(poisons)} poison documents")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
