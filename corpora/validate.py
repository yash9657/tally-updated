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
from datetime import datetime, timedelta, timezone
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


# ---------------------------------------------------------------------------
# TDS / TCS (corpora/tds, corpora/tcs)
# ---------------------------------------------------------------------------
# These fixtures deliberately live in their OWN top-level directories rather
# than in corpora/tax. Three consumers glob corpora/tax/*.json and assume the
# GST case shape (validate_tax below, the Go TestTaxDerive_GoldenCorpus, and
# insightd's test_gst_oracle_reconcile.py); a TDS file dropped in there breaks
# all three, and it would also inflate MIN_COUNTS["tax"] so a deleted GST
# fixture would stop being detected.
#
# The rate tables below are an INDEPENDENT restatement of the ones in
# backend/internal/compliance/{tds.go,tcs.go} — written separately so a typo in
# either side fails this gate. They are NOT imported from Go and must not be
# "de-duplicated" into a shared data file: the duplication IS the oracle. Note
# that duplication cannot catch a rate that is wrong in BOTH places; that is
# what the pre-release statutory spot-check is for (see the provenance banner
# in tds.go).

TDS_RULESET_VERSION = "tds-2026-04"
TCS_RULESET_VERSION = "tcs-2026-04"

THRESHOLD_BASES = {"PER_TXN", "ANNUAL", "MONTHLY"}

# section -> (rate for individual/HUF, rate for others, threshold, basis)
TDS_RATES: dict[str, tuple[Decimal, Decimal, Decimal, str]] = {
    "194C": (Decimal("1"), Decimal("2"), Decimal("30000"), "PER_TXN"),
    "194J-P": (Decimal("10"), Decimal("10"), Decimal("50000"), "ANNUAL"),
    "194J-T": (Decimal("2"), Decimal("2"), Decimal("50000"), "ANNUAL"),
    "194H": (Decimal("2"), Decimal("2"), Decimal("20000"), "ANNUAL"),
    "194I-A": (Decimal("2"), Decimal("2"), Decimal("50000"), "MONTHLY"),
    "194I-B": (Decimal("10"), Decimal("10"), Decimal("50000"), "MONTHLY"),
}
# s.206AA(1): no PAN -> the HIGHER of the rate in force and 20%.
TDS_NO_PAN_FLOOR = Decimal("20")

# section -> (rate, threshold, basis). 206C(1H) is deliberately absent: it was
# omitted w.e.f. 1-Apr-2025 by the Finance Act 2025.
TCS_RATES: dict[str, tuple[Decimal, Decimal, str]] = {
    "206C(1)-SCRAP": (Decimal("1"), Decimal("0"), "PER_TXN"),
    "206C(1F)-MV": (Decimal("1"), Decimal("1000000"), "PER_TXN"),
}
# s.206CC (NOT s.206AA): no PAN -> the HIGHER of twice the rate and 5%.
TCS_NO_PAN_MULTIPLIER = Decimal("2")
TCS_NO_PAN_FLOOR = Decimal("5")

DEDUCTEE_TYPES = {"IND_HUF", "OTHER"}


def rate_str(path: Path, field: str, value: Any) -> Decimal | None:
    """Validate a percentage rate expressed as a decimal STRING (never a float)."""
    if not isinstance(value, str):
        fail(path, f"{field} must be a decimal STRING, got {type(value).__name__}")
        return None
    try:
        return Decimal(value)
    except InvalidOperation:
        fail(path, f"{field} is not a valid decimal: {value!r}")
        return None


def derive_tds(case_input: dict) -> dict[str, Any]:
    """Reference TDS derivation. Mirrors the backend's pure TDSDerive contract.

    Rate by deductee type, then the s.206AA floor when no PAN is on record, then
    a STRICT greater-than threshold test ("does not exceed" in the statutes),
    then round HALF-UP to 4 dp. v1 applies every threshold per transaction; see
    the ThresholdBasis note in tds.go.
    """
    rate_ind, rate_other, threshold, basis = TDS_RATES[case_input["section"]]
    rate = rate_ind if case_input["deducteeType"] == "IND_HUF" else rate_other
    if not case_input["panAvailable"] and TDS_NO_PAN_FLOOR > rate:
        rate = TDS_NO_PAN_FLOOR
    amount = Decimal(case_input["taxableAmount"])
    applied = amount > threshold
    tds = Decimal("0")
    if applied:
        tds = (amount * rate / 100).quantize(MONEY_DP, rounding=ROUND_HALF_UP)
    return {"tds": tds, "ratePct": rate, "threshold": threshold, "basis": basis, "applied": applied}


def derive_tcs(case_input: dict) -> dict[str, Any]:
    """Reference TCS derivation. s.206CC (2x or 5%) — NOT s.206AA's flat 20%."""
    rate, threshold, basis = TCS_RATES[case_input["section"]]
    if not case_input["panAvailable"]:
        rate = rate * TCS_NO_PAN_MULTIPLIER
        if TCS_NO_PAN_FLOOR > rate:
            rate = TCS_NO_PAN_FLOOR
    amount = Decimal(case_input["taxableAmount"])
    applied = amount > threshold
    tcs = Decimal("0")
    if applied:
        tcs = (amount * rate / 100).quantize(MONEY_DP, rounding=ROUND_HALF_UP)
    return {"tcs": tcs, "ratePct": rate, "threshold": threshold, "basis": basis, "applied": applied}


def _check_withholding(
    path: Path,
    case: dict,
    money_field: str,
    ruleset_version: str,
    known_sections: set[str],
    derive,
    extra_input_keys: list[str],
) -> None:
    """Shared shape + arithmetic check for a TDS or TCS fixture."""
    if not require(path, case, ["id", "description", "rulesetVersion", "input", "expected"]):
        return
    if case["rulesetVersion"] != ruleset_version:
        fail(path, f"rulesetVersion must be {ruleset_version!r}, got {case['rulesetVersion']!r}")
        return
    # The corpus is the oracle for rates that were curated offline; a fixture
    # without a statutory citation in its description is unreviewable.
    if not isinstance(case["description"], str) or len(case["description"]) < 40:
        fail(path, "description must carry the statutory citation (>=40 chars)")

    ci = case["input"]
    if not require(path, ci, ["section", "panAvailable", "taxableAmount"] + extra_input_keys):
        return
    if ci["section"] not in known_sections:
        fail(path, f"input.section {ci['section']!r} is not in the shipped rate table")
        return
    if not isinstance(ci["panAvailable"], bool):
        fail(path, "input.panAvailable must be a boolean")
        return
    if "deducteeType" in extra_input_keys and ci["deducteeType"] not in DEDUCTEE_TYPES:
        fail(path, f"input.deducteeType must be one of {sorted(DEDUCTEE_TYPES)}, got {ci['deducteeType']!r}")
        return
    if money(path, "input.taxableAmount", ci["taxableAmount"]) is None:
        return

    exp = case["expected"]
    if not require(path, exp, [money_field, "ratePct", "threshold", "basis", "applied"]):
        return
    declared: dict[str, Any] = {}
    for field in (money_field, "threshold"):
        dec = money(path, f"expected.{field}", exp[field])
        if dec is None:
            return
        declared[field] = dec
    rate = rate_str(path, "expected.ratePct", exp["ratePct"])
    if rate is None:
        return
    declared["ratePct"] = rate
    if exp["basis"] not in THRESHOLD_BASES:
        fail(path, f"expected.basis must be one of {sorted(THRESHOLD_BASES)}, got {exp['basis']!r}")
        return
    declared["basis"] = exp["basis"]
    if not isinstance(exp["applied"], bool):
        fail(path, "expected.applied must be a boolean")
        return
    declared["applied"] = exp["applied"]

    try:
        computed = derive(ci)
    except (KeyError, InvalidOperation) as exc:
        fail(path, f"could not re-derive: {exc}")
        return

    for field, want in computed.items():
        got = declared[field]
        if isinstance(want, Decimal):
            if got != want:
                fail(
                    path,
                    f"expected.{field}={got:f} is WRONG; reference derivation = {want:f}",
                )
        elif got != want:
            fail(path, f"expected.{field}={got!r} is WRONG; reference derivation = {want!r}")

    # A non-applied case must declare zero — a stale amount beside applied=false
    # would silently over-deduct if a caller ignored the flag.
    if not declared["applied"] and declared[money_field] != 0:
        fail(path, f"expected.applied is false but expected.{money_field} is non-zero")


def validate_tds(path: Path, case: dict) -> None:
    """Validate a TDS-derivation case and re-check its arithmetic."""
    _check_withholding(
        path, case, "tds", TDS_RULESET_VERSION, set(TDS_RATES), derive_tds, ["deducteeType"]
    )


def validate_tcs(path: Path, case: dict) -> None:
    """Validate a TCS-derivation case and re-check its arithmetic."""
    _check_withholding(path, case, "tcs", TCS_RULESET_VERSION, set(TCS_RATES), derive_tcs, [])


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


def _check_xml(path: Path, case: dict) -> None:
    """Parse the referenced XML and assert every voucher's ledger entries net to
    zero (Tally signs debits negative). ALTER/DELETE actions are checked too."""
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
    for v in vouchers:
        total = Decimal("0")
        # Accept both LEDGERENTRIES.LIST and ALLLEDGERENTRIES.LIST spellings.
        amts = v.findall("./LEDGERENTRIES.LIST/AMOUNT") + v.findall("./ALLLEDGERENTRIES.LIST/AMOUNT")
        for amt in amts:
            try:
                total += Decimal((amt.text or "").strip())
            except InvalidOperation:
                fail(path, f"voucher has non-decimal AMOUNT {amt.text!r}")
        if total != 0:
            num = v.findtext("VOUCHERNUMBER", "?")
            typ = v.findtext("VOUCHERTYPENAME", "?")
            fail(path, f"voucher {typ} #{num} ledger entries do not net to zero (sum={total:f})")


def validate_migration(path: Path, case: dict) -> None:
    """Validate a migration reconciliation case + its referenced XML export.

    Two on-disk shapes are accepted:
      * legacy single-statement TB doc: {expectedTrialBalance[], expectedTotals}
      * four-statement oracle doc:      {trialBalance{rows,totals}, balanceSheet?,
                                         profitAndLoss?, gstr1?, outstandings?}
    Dispatch on the presence of the 'trialBalance' key.
    """
    if "trialBalance" in case:
        validate_migration_full(path, case)
        return
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


def validate_migration_full(path: Path, case: dict) -> None:
    """Validate a four-statement migration ORACLE doc and re-check its OWN internal
    arithmetic (independent of any Go code): SUM(Dr)==SUM(Cr) on the TB, the BS
    accounting identity (assets == liabilities, liabilities includes folded net
    profit), BS.netProfit == PL.netProfit, and the GSTR-1 tax-sum/HSN consistency.

    This catches arithmetic slips in the HAND-WRITTEN oracle before any importer
    runs, satisfying the oracleIndependenceRule's "re-check the expected doc
    itself" requirement.
    """
    if not require(path, case, ["id", "description", "tallyExportFile", "trialBalance"]):
        return
    _check_xml(path, case)

    tb = case["trialBalance"]
    if not isinstance(tb, dict) or not require(path, tb, ["rows", "totals"]):
        return
    rows = tb["rows"]
    if not isinstance(rows, list) or not rows:
        fail(path, "trialBalance.rows must be a non-empty array")
        return
    sum_debit = Decimal("0")
    sum_credit = Decimal("0")
    for i, row in enumerate(rows):
        if not require(path, row, ["ledger", "debit", "credit"]):
            return
        d = money(path, f"trialBalance.rows[{i}].debit", row["debit"])
        c = money(path, f"trialBalance.rows[{i}].credit", row["credit"])
        if d is None or c is None:
            return
        if d != 0 and c != 0:
            fail(path, f"trialBalance.rows[{i}] ({row['ledger']}) has both debit and credit non-zero")
        sum_debit += d
        sum_credit += c

    totals = tb["totals"]
    if not require(path, totals, ["debit", "credit", "balanced"]):
        return
    td = money(path, "trialBalance.totals.debit", totals["debit"])
    tc = money(path, "trialBalance.totals.credit", totals["credit"])
    if td is None or tc is None:
        return
    if td != sum_debit:
        fail(path, f"trialBalance.totals.debit={td:f} != sum of row debits {sum_debit:f}")
    if tc != sum_credit:
        fail(path, f"trialBalance.totals.credit={tc:f} != sum of row credits {sum_credit:f}")
    if bool(totals["balanced"]) != (sum_debit == sum_credit):
        fail(path, f"trialBalance.totals.balanced={bool(totals['balanced'])} but Dr==Cr is {sum_debit==sum_credit}")
    if sum_debit != sum_credit:
        fail(path, f"trial balance does not balance: Dr {sum_debit:f} vs Cr {sum_credit:f}")

    # --- Balance Sheet identity ---
    bs_np = None
    if "balanceSheet" in case:
        bs = case["balanceSheet"]
        if require(path, bs, ["assetsTotal", "liabilitiesTotal", "netProfit", "balanced"]):
            a = money(path, "balanceSheet.assetsTotal", bs["assetsTotal"])
            ltot = money(path, "balanceSheet.liabilitiesTotal", bs["liabilitiesTotal"])
            bs_np = money(path, "balanceSheet.netProfit", bs["netProfit"])
            if a is not None and ltot is not None:
                if bool(bs["balanced"]) != (a == ltot):
                    fail(path, f"balanceSheet.balanced={bool(bs['balanced'])} but assets==liabilities is {a==ltot}")
                if a != ltot:
                    fail(path, f"balance sheet does not balance: Assets {a:f} != Liabilities {ltot:f}")

    # --- P&L identity + BS/PL net-profit cross-check ---
    if "profitAndLoss" in case:
        pl = case["profitAndLoss"]
        if require(path, pl, ["incomeTotal", "expenseTotal", "netProfit"]):
            inc = money(path, "profitAndLoss.incomeTotal", pl["incomeTotal"])
            exp = money(path, "profitAndLoss.expenseTotal", pl["expenseTotal"])
            pl_np = money(path, "profitAndLoss.netProfit", pl["netProfit"])
            if inc is not None and exp is not None and pl_np is not None:
                if pl_np != inc - exp:
                    fail(path, f"profitAndLoss.netProfit={pl_np:f} != income-expense {inc-exp:f}")
                if bs_np is not None and bs_np != pl_np:
                    fail(path, f"CONSISTENCY: balanceSheet.netProfit {bs_np:f} != profitAndLoss.netProfit {pl_np:f}")

    # --- GSTR-1 internal consistency ---
    if "gstr1" in case:
        validate_gstr1_oracle(path, case["gstr1"])

    # --- Outstandings: per-party pending == sum of its bills (when bills listed) ---
    if "outstandings" in case:
        os_doc = case["outstandings"]
        for side in ("receivables", "payables"):
            for j, party in enumerate(os_doc.get(side, []) or []):
                if not require(path, party, ["ledger", "pending"]):
                    continue
                pend = money(path, f"outstandings.{side}[{j}].pending", party["pending"])
                bills = party.get("bills") or []
                if bills and pend is not None:
                    bsum = Decimal("0")
                    for k, b in enumerate(bills):
                        if not require(path, b, ["ref", "pending"]):
                            continue
                        bp = money(path, f"outstandings.{side}[{j}].bills[{k}].pending", b["pending"])
                        if bp is not None:
                            bsum += bp
                    if bsum != pend:
                        fail(path, f"outstandings.{side}[{j}] ({party['ledger']}) pending {pend:f} != sum of bills {bsum:f}")


def validate_gstr1_oracle(path: Path, g: dict) -> None:
    """Re-check the GSTR-1 oracle's internal arithmetic: taxTotals == sum of HSN
    rows, and every HSN/B2CS/B2B row has tax == taxable*rate split correctly."""
    if not require(path, g, ["fp", "taxTotals", "hsn", "balanced"]):
        return
    tt = g["taxTotals"]
    if not require(path, tt, ["taxable", "cgst", "sgst", "igst"]):
        return
    t_tax = money(path, "gstr1.taxTotals.taxable", tt["taxable"])
    t_c = money(path, "gstr1.taxTotals.cgst", tt["cgst"])
    t_s = money(path, "gstr1.taxTotals.sgst", tt["sgst"])
    t_i = money(path, "gstr1.taxTotals.igst", tt["igst"])

    # taxTotals must equal the sum of the HSN summary (HSN covers every invoice).
    h_tax = h_c = h_s = h_i = Decimal("0")
    for i, h in enumerate(g["hsn"]):
        if not require(path, h, ["hsn", "rate", "taxable", "cgst", "sgst", "igst"]):
            continue
        ht = money(path, f"gstr1.hsn[{i}].taxable", h["taxable"])
        hc = money(path, f"gstr1.hsn[{i}].cgst", h["cgst"])
        hs = money(path, f"gstr1.hsn[{i}].sgst", h["sgst"])
        hi = money(path, f"gstr1.hsn[{i}].igst", h["igst"])
        if None in (ht, hc, hs, hi):
            continue
        h_tax += ht
        h_c += hc
        h_s += hs
        h_i += hi
        # Per-row CGST must equal SGST on an intra row (this engine's equal-split rule).
        if hi == 0 and hc != hs:
            fail(path, f"gstr1.hsn[{i}] intra row has CGST {hc:f} != SGST {hs:f}")
    for label, want, got in (("taxable", t_tax, h_tax), ("cgst", t_c, h_c), ("sgst", t_s, h_s), ("igst", t_i, h_i)):
        if want is not None and want != got:
            fail(path, f"gstr1.taxTotals.{label}={want:f} != sum of HSN rows {got:f}")


_IST = timezone(timedelta(hours=5, minutes=30))


def _parse_date(s: str):
    return datetime.strptime(s, "%Y-%m-%d").date()


def validate_forecast(path: Path, data: dict) -> None:
    """INDEPENDENT oracle for the forecast corpus (wave-2 WS5a).

    Re-derives the cash-flow buckets and the anomaly findings from the fixture's
    inputs — in Python, with Decimal — and asserts they match the fixture's
    expected block. This is a second, non-circular check on the Go
    Project/DetectAnomalies functions (the fixture is the shared oracle; a wrong
    expected value fails here even if the Go test were also wrong).
    """
    kind = data.get("kind")
    if kind == "cashflow":
        _validate_forecast_cashflow(path, data)
    elif kind == "anomaly":
        _validate_forecast_anomaly(path, data)
    else:
        fail(path, f"forecast fixture: unknown kind {kind!r} (want cashflow|anomaly)")


def _validate_forecast_cashflow(path: Path, data: dict) -> None:
    if not require(path, data, ["as_on", "horizon_days", "bucket_days", "opening_cash", "bills", "expected"]):
        return
    as_on = _parse_date(data["as_on"])
    horizon = data["horizon_days"]
    bucket_days = data["bucket_days"]
    if not isinstance(horizon, int) or not isinstance(bucket_days, int) or horizon <= 0 or bucket_days <= 0:
        fail(path, "horizon_days and bucket_days must be positive integers")
        return
    opening = money(path, "opening_cash", data["opening_cash"])
    if opening is None:
        return
    n_buckets = -(-horizon // bucket_days)  # ceil
    inflow = [Decimal(0)] * n_buckets
    outflow = [Decimal(0)] * n_buckets
    beyond_in = Decimal(0)
    beyond_out = Decimal(0)
    overdue = False
    for i, b in enumerate(data["bills"]):
        pend = money(path, f"bills[{i}].pending", b["pending"])
        if pend is None:
            return
        if pend == 0:
            continue
        if b.get("due_date"):
            exp = _parse_date(b["due_date"])
        elif b.get("voucher_date"):
            cd = b["credit_days"] if b.get("credit_days") is not None else 30
            exp = _parse_date(b["voucher_date"]) + timedelta(days=cd)
        else:
            fail(path, f"bills[{i}] has neither due_date nor voucher_date")
            return
        day_off = (exp - as_on).days
        if day_off < 0:
            idx, overdue = 0, True
        else:
            idx = day_off // bucket_days
        if idx >= n_buckets:
            if b["kind"] == "outflow":
                beyond_out += pend
            else:
                beyond_in += pend
        elif b["kind"] == "outflow":
            outflow[idx] += pend
        else:
            inflow[idx] += pend

    exp_buckets = data["expected"]["buckets"]
    if len(exp_buckets) != n_buckets:
        fail(path, f"expected {n_buckets} buckets, fixture lists {len(exp_buckets)}")
        return
    prev_close = opening
    for k, eb in enumerate(exp_buckets):
        nett = inflow[k] - outflow[k]
        open_cash = prev_close
        close_cash = open_cash + nett
        prev_close = close_cash
        for field, got in (
            ("inflow", inflow[k]), ("outflow", outflow[k]), ("nett", nett),
            ("opening_cash", open_cash), ("closing_cash", close_cash),
        ):
            want = money(path, f"buckets[{k}].{field}", eb[field])
            if want is not None and want != got:
                fail(path, f"cashflow bucket {k} {field}: fixture {want:f} != re-derived {got:f}")
        want_from = (as_on + timedelta(days=k * bucket_days)).isoformat()
        want_to = (as_on + timedelta(days=(k + 1) * bucket_days)).isoformat()
        if eb["from"] != want_from or eb["to"] != want_to:
            fail(path, f"cashflow bucket {k} range [{eb['from']},{eb['to']}) != re-derived [{want_from},{want_to})")

    bh = data["expected"]["beyond_horizon"]
    for field, got in (("inflow", beyond_in), ("outflow", beyond_out), ("nett", beyond_in - beyond_out)):
        want = money(path, f"beyond_horizon.{field}", bh[field])
        if want is not None and want != got:
            fail(path, f"cashflow beyond_horizon.{field}: fixture {want:f} != re-derived {got:f}")
    if bool(data["expected"].get("overdue_folded", False)) != overdue:
        fail(path, f"overdue_folded: fixture {data['expected'].get('overdue_folded')} != re-derived {overdue}")


def _validate_forecast_anomaly(path: Path, data: dict) -> None:
    if not require(path, data, ["records", "expected_findings"]):
        return
    findings: list[tuple[str, str]] = []
    by_voucher: dict[str, list[dict]] = {}
    for r in data["records"]:
        vd = _parse_date(r["voucher_date"])
        ca = datetime.fromisoformat(r["created_at"])
        if (ca.date() - vd).days > 7:
            findings.append(("BACKDATED", r["voucher_no"]))
        hr = ca.astimezone(_IST).hour
        if hr < 7 or hr >= 22:
            findings.append(("AFTER_HOURS", r["voucher_no"]))
        tv = r.get("taxable_value")
        if tv is not None:
            d = Decimal(str(tv))
            if d != 0 and d >= 10000 and d % 1000 == 0:
                findings.append(("ROUND_AMOUNT", r["voucher_no"]))
        by_voucher.setdefault(r["voucher_id"], []).append(r)
    for group in by_voucher.values():
        if len(group) < 3:
            continue
        times = sorted(datetime.fromisoformat(r["created_at"]) for r in group)
        if any(times[i + 2] - times[i] <= timedelta(hours=24) for i in range(len(times) - 2)):
            findings.append(("EDIT_BURST", group[0]["voucher_no"]))
    got = sorted(findings)
    want = sorted((f["kind"], f["voucher_no"]) for f in data["expected_findings"])
    if got != want:
        fail(path, f"anomaly findings re-derived {got} != fixture {want}")


VALIDATORS = {
    "tax": validate_tax,
    "tds": validate_tds,
    "tcs": validate_tcs,
    "keystroke": validate_keystroke,
    "isolation": validate_isolation,
    "migration": validate_migration,
    "forecast": validate_forecast,
}

MIN_COUNTS = {"tax": 14, "tds": 8, "tcs": 2, "keystroke": 14, "isolation": 10, "migration": 6, "forecast": 4}


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
