#!/usr/bin/env python3
"""Golden-corpora validator.

Loads every JSON fixture under this directory, checks it parses and conforms to
the shapes documented in corpora/README.md, and re-derives the arithmetic for
the tax cases so a wrong expected value fails CI. Exits non-zero on any problem.

Usage:
    python3 corpora/validate.py

The validator is intentionally dependency-free (stdlib only) so it runs in any
CI image. Money is handled with decimal.Decimal (never float) to match the
backend's fixed-point contract.
"""

from __future__ import annotations

import json
import sys
import xml.etree.ElementTree as ET
from decimal import ROUND_HALF_UP, Decimal, InvalidOperation
from pathlib import Path
from typing import Any

ROOT = Path(__file__).resolve().parent

# Number of decimal places money strings carry across the corpora.
MONEY_DP = Decimal("0.0001")

# Voucher F-key shortcuts that are valid voucher types in the keystroke corpus.
KNOWN_VOUCHER_TYPES = {
    "sales",
    "purchase",
    "payment",
    "receipt",
    "journal",
    "contra",
    "credit-note",
    "debit-note",
}

KNOWN_SCREENS = {"gateway", "voucher", "report", "goto-overlay"}

RULESET_VERSION = "gst-2024-09"
KEYMAP_PROFILE = "tallyprime"


class ValidationError(Exception):
    """Raised when a fixture violates the documented contract."""


_errors: list[str] = []


def fail(path: Path, msg: str) -> None:
    """Record a validation failure against a specific file."""
    _errors.append(f"{path.relative_to(ROOT)}: {msg}")


def load_json(path: Path) -> Any:
    """Parse a JSON file, recording a failure (and returning None) on error."""
    try:
        with path.open(encoding="utf-8") as fh:
            return json.load(fh)
    except (OSError, json.JSONDecodeError) as exc:
        fail(path, f"does not parse as JSON: {exc}")
        return None


def money(path: Path, field: str, value: Any) -> Decimal | None:
    """Validate that *value* is a 4dp decimal string and return it as Decimal."""
    if not isinstance(value, str):
        fail(path, f"{field} must be a decimal STRING, got {type(value).__name__}")
        return None
    try:
        dec = Decimal(value)
    except InvalidOperation:
        fail(path, f"{field} is not a valid decimal: {value!r}")
        return None
    # Enforce the 4-decimal-place money convention from README.
    if value != f"{dec.quantize(MONEY_DP):f}" and dec != dec.quantize(MONEY_DP):
        fail(path, f"{field} must use 4 decimal places, got {value!r}")
        return None
    return dec


def require(path: Path, obj: dict, keys: list[str]) -> bool:
    """Record a failure for each missing key; return True only if all present."""
    ok = True
    for key in keys:
        if key not in obj:
            fail(path, f"missing required field {key!r}")
            ok = False
    return ok


def state_code(path: Path, field: str, value: Any) -> None:
    """Validate a GST 2-digit string state code."""
    if not isinstance(value, str) or len(value) != 2 or not value.isdigit():
        fail(path, f"{field} must be a 2-digit GST state code string, got {value!r}")


def derive_tax(case_input: dict) -> dict[str, Decimal]:
    """Reference tax derivation. Mirrors the backend's pure TaxDerive contract.

    Intra-state (supplier state == place of supply) splits the rate into equal
    CGST and SGST halves; inter-state puts the full rate into IGST. Each money
    component is rounded HALF-UP to 4 decimal places per line/component, then
    summed (the same order the backend must use for bit-for-bit replay).
    """
    intra = case_input["supplierStateCode"] == case_input["placeOfSupplyStateCode"]
    reverse_charge = bool(case_input.get("reverseCharge", False))

    cgst = Decimal("0")
    sgst = Decimal("0")
    igst = Decimal("0")
    taxable_total = Decimal("0")

    for line in case_input["lines"]:
        taxable = Decimal(line["taxableValue"])
        rate = Decimal(line["gstRatePct"])
        taxable_total += taxable
        if rate == 0:
            continue
        if intra:
            half = (taxable * (rate / 2) / 100).quantize(MONEY_DP, rounding=ROUND_HALF_UP)
            cgst += half
            sgst += half
        else:
            full = (taxable * rate / 100).quantize(MONEY_DP, rounding=ROUND_HALF_UP)
            igst += full

    total_tax = cgst + sgst + igst
    # Under reverse charge the supplier does not collect GST, so the invoice
    # total charged to the recipient excludes tax.
    invoice_total = taxable_total if reverse_charge else taxable_total + total_tax
    return {
        "cgst": cgst,
        "sgst": sgst,
        "igst": igst,
        "totalTax": total_tax,
        "invoiceTotal": invoice_total,
    }


def validate_tax(path: Path, case: dict) -> None:
    """Validate a tax-derivation case and re-check its arithmetic."""
    if not require(path, case, ["id", "description", "rulesetVersion", "input", "expected"]):
        return
    if case["rulesetVersion"] != RULESET_VERSION:
        fail(path, f"rulesetVersion must be {RULESET_VERSION!r}, got {case['rulesetVersion']!r}")

    ci = case["input"]
    if not require(path, ci, ["supplierStateCode", "placeOfSupplyStateCode", "reverseCharge", "lines"]):
        return
    state_code(path, "input.supplierStateCode", ci["supplierStateCode"])
    state_code(path, "input.placeOfSupplyStateCode", ci["placeOfSupplyStateCode"])
    if not isinstance(ci["reverseCharge"], bool):
        fail(path, "input.reverseCharge must be a boolean")
    if not isinstance(ci["lines"], list) or not ci["lines"]:
        fail(path, "input.lines must be a non-empty array")
        return
    for i, line in enumerate(ci["lines"]):
        if not require(path, line, ["taxableValue", "gstRatePct"]):
            return
        money(path, f"input.lines[{i}].taxableValue", line["taxableValue"])
        if not isinstance(line["gstRatePct"], str):
            fail(path, f"input.lines[{i}].gstRatePct must be a string")

    exp = case["expected"]
    if not require(path, exp, ["cgst", "sgst", "igst", "totalTax", "invoiceTotal"]):
        return
    declared = {}
    for field in ("cgst", "sgst", "igst", "totalTax", "invoiceTotal"):
        dec = money(path, f"expected.{field}", exp[field])
        if dec is None:
            return
        declared[field] = dec

    try:
        computed = derive_tax(ci)
    except (KeyError, InvalidOperation) as exc:
        fail(path, f"could not re-derive tax: {exc}")
        return

    for field, want in computed.items():
        got = declared[field]
        if got != want:
            fail(
                path,
                f"expected.{field}={got:f} is arithmetically WRONG; "
                f"reference derivation = {want:f}",
            )


def validate_keystroke(path: Path, case: dict) -> None:
    """Validate a keystroke-conformance case."""
    if not require(path, case, ["id", "description", "keymapProfile", "startScreen", "events", "expect"]):
        return
    if case["keymapProfile"] != KEYMAP_PROFILE:
        fail(path, f"keymapProfile must be {KEYMAP_PROFILE!r}, got {case['keymapProfile']!r}")
    if case["startScreen"] not in KNOWN_SCREENS:
        fail(path, f"startScreen {case['startScreen']!r} not in vocabulary {sorted(KNOWN_SCREENS)}")
    if not isinstance(case["events"], list) or not case["events"]:
        fail(path, "events must be a non-empty array")
    else:
        for i, ev in enumerate(case["events"]):
            if not isinstance(ev, dict) or "type" not in ev:
                fail(path, f"events[{i}] must be an object with a 'type'")
                continue
            if ev["type"] == "key" and "combo" not in ev:
                fail(path, f"events[{i}] of type 'key' must carry a 'combo'")
            if ev["type"] == "select" and "menuItem" not in ev:
                fail(path, f"events[{i}] of type 'select' must carry a 'menuItem'")

    expect = case["expect"]
    if not isinstance(expect, dict) or "screen" not in expect:
        fail(path, "expect must be an object containing at least 'screen'")
        return
    if expect["screen"] not in KNOWN_SCREENS:
        fail(path, f"expect.screen {expect['screen']!r} not in vocabulary {sorted(KNOWN_SCREENS)}")
    if "voucherType" in expect and expect["voucherType"] not in KNOWN_VOUCHER_TYPES:
        fail(path, f"expect.voucherType {expect['voucherType']!r} not in {sorted(KNOWN_VOUCHER_TYPES)}")
    if "buttonBarContains" in expect and not isinstance(expect["buttonBarContains"], list):
        fail(path, "expect.buttonBarContains must be an array of label strings")


def validate_isolation(path: Path, case: dict) -> None:
    """Validate a tenant-isolation scenario."""
    if not require(path, case, ["id", "description", "seed", "session", "action", "expect"]):
        return
    seed = case["seed"]
    if not isinstance(seed, dict) or "companyA" not in seed or "companyB" not in seed:
        fail(path, "seed must declare integer companyA and companyB row counts")
    else:
        for key in ("companyA", "companyB"):
            if not isinstance(seed[key], int) or seed[key] < 0:
                fail(path, f"seed.{key} must be a non-negative integer")
    session = case["session"]
    if not isinstance(session, dict) or "role" not in session or "setGuc" not in session:
        fail(path, "session must declare 'role' and boolean 'setGuc'")
    elif not isinstance(session["setGuc"], bool):
        fail(path, "session.setGuc must be a boolean")
    if not isinstance(case["action"], str) or not case["action"].strip():
        fail(path, "action must be a non-empty SQL string")
    expect = case["expect"]
    if not isinstance(expect, dict) or not expect:
        fail(path, "expect must be a non-empty object")
        return
    known_assertions = {
        "rowCountIs",
        "rowCountMax",
        "rowsAffected",
        "errorContains",
        "rlsBypassed",
        "rlsBypassed",
        "companyBRowCountAfter",
    }
    if not known_assertions.intersection(expect):
        fail(path, f"expect must contain at least one known assertion key from {sorted(known_assertions)}")


def validate_migration(path: Path, case: dict) -> None:
    """Validate a migration reconciliation case + its referenced XML export."""
    if not require(path, case, ["id", "description", "tallyExportFile", "expectedTrialBalance", "expectedTotals"]):
        return
    xml_path = path.parent / case["tallyExportFile"]
    if not xml_path.exists():
        fail(path, f"tallyExportFile {case['tallyExportFile']!r} does not exist next to fixture")
        return
    try:
        tree = ET.parse(xml_path)
    except ET.ParseError as exc:
        fail(path, f"referenced XML {xml_path.name} does not parse: {exc}")
        return
    root = tree.getroot()
    if root.tag != "ENVELOPE":
        fail(path, f"XML root must be <ENVELOPE>, got <{root.tag}>")
    vouchers = root.findall(".//VOUCHER")
    if not vouchers:
        fail(path, "XML contains no <VOUCHER> elements")
    # Each voucher's ledger entries must net to zero (Tally signs debits negative).
    for v in vouchers:
        total = Decimal("0")
        for amt in v.findall("./LEDGERENTRIES.LIST/AMOUNT"):
            try:
                total += Decimal((amt.text or "").strip())
            except InvalidOperation:
                fail(path, f"voucher has non-decimal AMOUNT {amt.text!r}")
        if total != 0:
            num = v.findtext("VOUCHERNUMBER", "?")
            typ = v.findtext("VOUCHERTYPENAME", "?")
            fail(path, f"voucher {typ} #{num} ledger entries do not net to zero (sum={total:f})")

    tb = case["expectedTrialBalance"]
    if not isinstance(tb, list) or not tb:
        fail(path, "expectedTrialBalance must be a non-empty array")
        return
    sum_debit = Decimal("0")
    sum_credit = Decimal("0")
    for i, row in enumerate(tb):
        if not require(path, row, ["ledger", "debit", "credit"]):
            return
        d = money(path, f"expectedTrialBalance[{i}].debit", row["debit"])
        c = money(path, f"expectedTrialBalance[{i}].credit", row["credit"])
        if d is None or c is None:
            return
        if d != 0 and c != 0:
            fail(path, f"expectedTrialBalance[{i}] ({row['ledger']}) has both debit and credit non-zero")
        sum_debit += d
        sum_credit += c

    totals = case["expectedTotals"]
    if not require(path, totals, ["debit", "credit", "balanced"]):
        return
    td = money(path, "expectedTotals.debit", totals["debit"])
    tc = money(path, "expectedTotals.credit", totals["credit"])
    if td is None or tc is None:
        return
    if td != sum_debit:
        fail(path, f"expectedTotals.debit={td:f} != sum of row debits {sum_debit:f}")
    if tc != sum_credit:
        fail(path, f"expectedTotals.credit={tc:f} != sum of row credits {sum_credit:f}")
    declared_balanced = bool(totals["balanced"])
    actually_balanced = sum_debit == sum_credit
    if declared_balanced != actually_balanced:
        fail(path, f"expectedTotals.balanced={declared_balanced} but debit==credit is {actually_balanced}")
    if not actually_balanced:
        fail(path, f"trial balance does not balance: Dr {sum_debit:f} vs Cr {sum_credit:f}")


VALIDATORS = {
    "tax": validate_tax,
    "keystroke": validate_keystroke,
    "isolation": validate_isolation,
    "migration": validate_migration,
}

MIN_COUNTS = {"tax": 14, "keystroke": 14, "isolation": 10, "migration": 1}


def main() -> int:
    counts: dict[str, int] = {k: 0 for k in VALIDATORS}
    for category, validator in VALIDATORS.items():
        directory = ROOT / category
        if not directory.is_dir():
            _errors.append(f"{category}/: directory is missing")
            continue
        for path in sorted(directory.glob("*.json")):
            data = load_json(path)
            if data is None:
                continue
            if not isinstance(data, dict):
                fail(path, "top-level JSON must be an object")
                continue
            counts[category] += 1
            validator(path, data)

    for category, minimum in MIN_COUNTS.items():
        if counts[category] < minimum:
            _errors.append(
                f"{category}/: only {counts[category]} cases, contract requires at least {minimum}"
            )

    if _errors:
        print("CORPORA VALIDATION FAILED:", file=sys.stderr)
        for err in _errors:
            print(f"  - {err}", file=sys.stderr)
        return 1

    summary = ", ".join(f"{cat}={counts[cat]}" for cat in VALIDATORS)
    print(f"corpora validation OK ({summary})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
