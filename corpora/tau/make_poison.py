#!/usr/bin/env python3
"""Write corpora/tau/_poison/: tau tasks that are WRONG in one named way each.

corpora/validate.py's check_poison() runs validate_tau over every file here and
fails if any of them validates — the tripwire against a validator that has
been relaxed until it restates its fixtures. Each poison is a real generated
task with ONE defect a hand-edit could plausibly introduce, and its id equals
its file name so that the only thing wrong with it is the named defect.

    python3 corpora/tau/make_poison.py
"""

from __future__ import annotations

import copy
import importlib.util
import json
from decimal import Decimal
from pathlib import Path

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("tau_generate", HERE / "generate.py")
gen = importlib.util.module_from_spec(spec)
spec.loader.exec_module(gen)
TASKS = {t["id"]: t for t in gen.build()}


def poison(prefix: str, new_id: str, why: str, mutate) -> dict:
    src_id = min(i for i in TASKS if i.startswith(prefix))
    t = copy.deepcopy(TASKS[src_id])
    t["id"] = new_id
    t["description"] = "POISON — " + why
    mutate(t)
    return t


def shift_tax_into_sales(t):
    # Balanced, and wrong: one rupee moved from the CGST leg to the sales leg.
    v = t["expected"]["vouchers"][-1]
    for p in v["postings"]:
        if p["ledger"] == "Output CGST":
            p["amount"] = gen.f4(Decimal(p["amount"]) - 1)
        if p["ledger"] == "Sales @18%":
            p["amount"] = gen.f4(Decimal(p["amount"]) + 1)


def igst_output_off_by_one(t):
    outs = t["expected"]["outputs"]
    outs[0] = f"{Decimal(outs[0]) + 1:.2f}"


def balance_answer_wrong(t):
    t["expected"]["outputs"] = [f"{Decimal(t['expected']['outputs'][0]) + 100:.2f}"]


def hindi_in_latin(t):
    t["utterance"] = "Is invoice ko Sales mein daal do."


def gstin_bad_checksum(t):
    for l in t["expected"]["ledgers"]:
        if l["gstin"]:
            last = l["gstin"][-1]
            l["gstin"] = l["gstin"][:-1] + ("A" if last != "A" else "B")
            break


def unbalanced(t):
    t["expected"]["vouchers"][-1]["postings"][0]["amount"] = "1.0000"


POISONS = [
    poison("ve-sale-", "poison-tax-leg-moved-to-sales", "CGST leg one rupee short, sales one rupee over (still balanced)", shift_tax_into_sales),
    poison("gst-3b-0", "poison-3b-taxable-off-by-one", "GSTR-3B 3.1(a) taxable output one rupee high", igst_output_off_by_one),
    poison("nlq-bank-", "poison-balance-off-by-hundred", "stated bank balance does not follow from the books", balance_answer_wrong),
    poison("ve-sale-001-", "poison-hindi-in-latin-script", "a 'hi' task whose utterance is not Devanagari", hindi_in_latin),
    poison("mc-party-", "poison-gstin-checksum", "a party GSTIN with a broken check character", gstin_bad_checksum),
    poison("ve-payment-", "poison-voucher-unbalanced", "an expected voucher whose legs do not balance", unbalanced),
]


def main() -> int:
    out = HERE / "_poison"
    out.mkdir(exist_ok=True)
    for p in out.glob("*.json"):
        p.unlink()
    for t in POISONS:
        (out / f"{t['id']}.json").write_text(json.dumps(t, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"wrote {len(POISONS)} poison tasks")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
