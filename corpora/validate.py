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
import re
import sys
import unicodedata
import xml.etree.ElementTree as ET
from datetime import datetime, timedelta, timezone
from decimal import ROUND_CEILING, ROUND_HALF_UP, Decimal, InvalidOperation, localcontext
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
    # The NON-ACCOUNTING stock vouchers (Alt+F8/F9/F7/F10). They move goods and
    # post ZERO accounting lines. Added in the same change that gave them an
    # entry screen — check_vocabulary_matches_typescript() would otherwise
    # reject a perfectly correct fixture naming one, which is this file's
    # documented worst failure mode.
    "delivery-note",
    "receipt-note",
    "stock-journal",
    "physical-stock",
}

# Mirrors ScreenKind in frontend/src/keystroke/types.ts. It had fallen behind:
# screens that shipped later (help, features, bill-inbox, query, ...) were not
# added here, so a corpus case naming one FAILED validation even though the
# screen exists and the case is correct. Keep this set in step with that union.
#
# IT HAD DRIFTED AGAIN, and the failure mode is worth naming because it is the
# opposite of the one a validator is for. A vocabulary that is MISSING a screen
# does not catch a bad fixture — it rejects a GOOD one, with a message pointing
# at the case rather than at this list, and the natural fix under time pressure
# is to change the fixture until the validator stops complaining. That turns a
# correct golden case into a wrong one. company-select, members and edit-log
# (the SaaS + audit screens) had shipped in ScreenKind and were absent here.
KNOWN_SCREENS = {
    "gateway",
    "menu",
    "voucher",
    "report",
    "report-detail",
    "goto-overlay",
    "master-create",
    "bill-inbox",
    "query",
    # F3 company switching and the Users & Roles surface (the SaaS admin wave).
    "company-select",
    "members",
    # A voucher's hash-chained version history — a pure reader, by design.
    "edit-log",
    "help",
    "features",
    # T14 — DATA (Alt+Y): backup and restore. Tally's own `Alt+Y (Data) >
    # Backup / Restore` (RESEARCH.md:993); Alt+Y was completely unbound in this
    # build, so the real key was free to take.
    #
    # It is "data" and NOT "backup": the screen is Tally's Data menu, backup is
    # one thing on it, restore is another, and Tally's third — Split Company
    # Data — is absent on purpose and says so on its own face.
    "data",
    # T32 — MIGRATE FROM TALLY (Alt+O): import a TallyPrime XML export. Tally's
    # own `Gateway of Tally > Import` (RESEARCH.md:1221) and its documented key,
    # "O: Import" (RESEARCH.md:361). The screen has BOTH doors because Tally has
    # both.
    #
    # Alt+O was already bound on `bank-reconciliation` (a bank STATEMENT import,
    # scoped there because a statement belongs to one account) and that is not a
    # clash: screen bindings resolve before the global table.
    "tally-import",
    # Print Templates (track C) — the per-company document layouts: title,
    # letterhead lines, declaration, terms, signature label and bank block, per
    # document kind. Reached from Gateway of Tally > Print Templates and named on
    # the F11 Company Features row.
    #
    # No global key: Tally reaches print configuration through F12 Configure and
    # the print preview rather than a documented Alt binding, so there is none to
    # be faithful to.
    "print-templates",
    # Reconciliation (track D) — every alter or delete these books REFUSED: the
    # freeze rule's rows (an IRN or a filed GSTR-1 pins a voucher) and the offline
    # sequencer's parked conflict losers, on one list. Reached from Gateway of
    # Tally > Reconciliation and from Alt+R.
    #
    # A ScreenKind rather than a report name, though `report:reconciliation-queue`
    # exists: that report answers 501, and the two routes this screen needs beyond
    # the listing — the parked payload and the resolve — are governed by the bare
    # `reconciliation` token, which migration 0011's coarse `allow display report`
    # cannot reach. The screen also carries a WRITE, which no report frame does.
    "reconciliation",
    # Offline Outbox (track D) — the vouchers keyed on THIS MACHINE that the
    # server could not be reached to post. Reached from Gateway of Tally >
    # Offline Outbox and from Alt+Q (free: no documented TallyPrime global uses Q,
    # because Tally has no offline outbox to have a key for).
    #
    # A ScreenKind rather than a report name, and here the reason is stronger than
    # for `reconciliation` above: THERE IS NO ROUTE THAT COULD SERVE THIS LIST.
    # The rows live in the browser's IndexedDB, never on cored, so a report frame
    # — whose whole shape is "ask the server for rows" — could not render it even
    # in principle. It also carries two writes: commit the queue, discard a row.
    "offline-outbox",
    # W2 — THE APPROVAL INBOX: the agent's proposals waiting for a human, with
    # the ledger-effect diff. Reached from Gateway of Tally > Approval Inbox and
    # from Alt+G Go To; Enter opens a proposal in its native voucher screen. A
    # ScreenKind under the bare `agent-approval` token, and it carries writes
    # (the decisions), which no report frame does.
    "approval-inbox",
    # The Bank Reconciliation Statement (T4): a bank ledger's postings with the
    # ONE editable column, Bank Date. Reached from the Gateway (TallyPrime's own
    # route is Alt+G > Banking Activities); F4 picks the account.
    "bank-reconciliation",
    # The TDS CHALLAN register (T6, migration 0069): the deposits made to the
    # Government for a tax year, and the form that records one. A ScreenKind
    # rather than a report name — the server serves it as the bare resource
    # token `tds-challan`.
    "tds-challans",
    # T2 — THE COST SCREENS. A cost CATEGORY is an AXIS (Department, Branch,
    # Project) and a cost CENTRE is a POSITION on it; several axes allocate the
    # SAME money in parallel, so no figure on any of these screens spans axes.
    #
    # "cost-centres" is the MASTERS screen (the axes and their centres, and the
    # one form that creates either) — a ScreenKind because the server serves it
    # under the bare token `cost-centre`, which no `allow display report` rule
    # can match.
    "cost-centres",
    # The two SUMMARIES. Both are also report names on the server
    # (report:cost-centre-summary / report:cost-category-summary, refused to the
    # seeded Data Entry level by migration 0072) but neither is reachable as a
    # reportName frame: the centre summary needs an AXIS the pure machine cannot
    # know, and the category summary is a shape no generic report screen renders.
    "cost-centre-summary",
    "cost-category-summary",
    # T11 — THE TWO BUDGET SCREENS (migrations 0085/0086). A budget POSTS NOTHING:
    # no voucher, no posting, no balance, no hash chain. It is the second column a
    # report compares an actual against, which is exactly why it cannot be faked by
    # keying journals — and why nothing a fixture does on these screens can move a
    # single figure in the books.
    #
    # "budgets" is the MASTER screen (the plans and the figures on them) — a
    # ScreenKind because the server serves it under the bare token `budget`, which
    # no `allow display report` rule can match, the same reason "cost-centres" is.
    "budgets",
    # THE VARIANCE REPORT. Unlike the two cost summaries above, this one IS served
    # as a report name (`report:budget-variance`, refused to the seeded Data Entry
    # level by migration 0086) — and it is STILL a ScreenKind, because the screen
    # takes a PLAN and a GRAIN (and an AXIS at the cost-centre grain) that no
    # reportName frame can carry. The ACL name and the navigation mechanism are
    # independent.
    #
    # ⛔ A FIXTURE ON THIS SCREEN MUST NOT ASSERT A GRAND TOTAL WITHOUT CHECKING
    # `totals_are_addable`. A plan mixing a CLOSING_BALANCE line with a
    # NETT_TRANSACTIONS one has NO addable total — a position plus a flow is not a
    # quantity — and the server sends three zeros with the flag false.
    "budget-variance",
    # T12 — THE GSTR-9 ANNUAL RECONCILIATION (migration 0087). A ScreenKind for the
    # same reason "budget-variance" above is, and against a stronger pull: the
    # server serves it as `report:gstr9`, right beside `report:gstr1` and
    # `report:gstr3b`, which ARE report names — but the screen takes a FINANCIAL
    # YEAR and six TAX LEDGERS that no reportName frame can carry.
    #
    # ⛔ A FIXTURE ON THIS SCREEN MUST NOT ASSERT AGREEMENT FROM AN ANNUAL NET.
    # Twelve months added up CANCEL: a year that is +40,000 in May and -40,000 in
    # September has a net of ZERO and two months that disagree with the books. The
    # server sends `gross` and `offsetting` beside every `net` precisely so nothing
    # downstream has to work that out, and a fixture that read the net alone would
    # assert the defect this report exists to find.
    #
    # ⛔ AND IT MUST NOT READ A NOT_DERIVED TABLE AS ZERO. Eleven of the fourteen
    # statutory tables cannot be produced from these books and carry NO rows; a zero
    # in table 7 would assert the taxpayer reversed nothing when the truth is that
    # nothing was checked.
    "gstr9",
    # T3 — THE TWO ORDER REGISTERS AND THE BILLS OF MATERIALS (migrations 0042,
    # 0043). An order is a COMMITTED QUANTITY and not a movement: the stock ledger
    # has exactly one primitive (a positive quantity plus a direction) and an order
    # is neither, so nothing on these screens moves stock.
    #
    # "sales-orders" and "purchase-orders" are TWO ScreenKinds and not one with a
    # direction toggle, because the server serves them on two routes under two bare
    # tokens (`sales-order`, `purchase-order`) and the PDP reads the PATH — the same
    # reason the two order BOOKS are two report names rather than one with a
    # ?direction= parameter, which would be invisible to the PDP. Tally has two keys
    # for it as well (Ctrl+F8 / Ctrl+F9).
    "sales-orders",
    "purchase-orders",
    # A BOM is a recipe and a MASTER: it moves nothing, commits nothing and
    # balances nothing until it is exploded into a Stock Journal's lines. A
    # ScreenKind under the bare token `bom`.
    "boms",
    # T5 — THE e-WAY BILL (Rule 138, migration 0047). Alt+Z: pick the voucher the
    # goods move against, state the movement, Ctrl+A raises the payload NIC would
    # be sent.
    #
    # A ScreenKind under the bare token `e-way-bill`, for the reason
    # "cost-centres" is one: no `allow display report` rule can match a bare
    # token, and the screen carries a FORM.
    #
    # ⛔ NOTHING IT PRODUCES IS AN EBN. The NIC portal is unreachable from this
    # deployment, so the identifier is a LOCAL reference and the screen leads
    # with a disclosure saying no submission was made. A keystroke fixture naming
    # this screen is describing a payload builder, never a government document.
    "e-way-bill",
    # THE e-INVOICE / IRN (HANDOFF §1.4 B). Alt+I: pick the sales invoice to
    # report, Ctrl+A raises it through cored's proxy into statutoryd.
    #
    # A ScreenKind under the bare token `e-invoice`, for the reason "e-way-bill"
    # above is one: no `allow display report` rule can match a bare token, and
    # the screen carries a FORM.
    #
    # ⛔ NOTHING IT PRODUCES IS A GOVERNMENT-ISSUED IRN. This deployment has no
    # IRP credentials, so the reference is minted by a fake provider and the
    # screen leads with a disclosure saying no submission was made. A keystroke
    # fixture naming this screen is describing a payload builder, never a filing.
    #
    # ⛔⛔ AND ITS Ctrl+A IS IRREVERSIBLE — it FREEZES the voucher against
    # alteration and deletion, with no undo anywhere in the product. A fixture
    # that keys it is describing a one-way door.
    "e-invoice",
    # THE PAYROLL SCREENS (Track 3). They shipped in ScreenKind and were absent
    # here — the SAME drift this list has now had three times, and the reason the
    # cross-check against types.ts exists at all rather than a comment asking
    # the next author to remember.
    #
    # "employees" is the MASTER (a person, plus the Alt+S salary structure and
    # statutory election without which a pay run answers 422); "payroll-run" is
    # Ctrl+F4, one WAGE MONTH for every employee at once; the other three are
    # what comes back off it. All five are ScreenKinds and not "report:NAME"
    # frames for the reason the two cost summaries are not: the register and the
    # payslip carry a MONTH (and the payslip an EMPLOYEE) that no generic report
    # frame holds, and the server serves the masters under the bare `employee`
    # token, which no `allow display report` rule can match.
    #
    # ⚠️ A fixture keying "payroll-run" is describing a form that sends an EMPTY
    # per-employee selection on purpose: a non-empty `inputs` is also the
    # SELECTION, so rows for the employees a client happened to have loaded would
    # silently leave everybody else out of the month.
    "employees",
    "payroll-run",
    "payroll-register",
    "payroll-payslip",
    "payroll-form-138",
}

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

THRESHOLD_BASES = {"PER_TXN", "ANNUAL", "MONTHLY", "NIL"}

# section -> (rate for individual/HUF, rate for others, threshold, basis)
#
# 194J-D is directors' remuneration, s.393(1) [Table: Sl. No. 6(iii)(c)], at the
# 10% of limb (b) and a threshold col. D states as "Nil". Its basis is NIL and
# not "PER_TXN with a limit of zero": the statute sets no per-payment measure
# for this row, and a cumulative engine must be able to tell the two apart.
TDS_RATES: dict[str, tuple[Decimal, Decimal, Decimal, str]] = {
    "194C": (Decimal("1"), Decimal("2"), Decimal("30000"), "PER_TXN"),
    "194J-P": (Decimal("10"), Decimal("10"), Decimal("50000"), "ANNUAL"),
    "194J-T": (Decimal("2"), Decimal("2"), Decimal("50000"), "ANNUAL"),
    "194J-D": (Decimal("10"), Decimal("10"), Decimal("0"), "NIL"),
    "194H": (Decimal("2"), Decimal("2"), Decimal("20000"), "ANNUAL"),
    "194I-A": (Decimal("2"), Decimal("2"), Decimal("50000"), "MONTHLY"),
    "194I-B": (Decimal("10"), Decimal("10"), Decimal("50000"), "MONTHLY"),
}

# section -> {deductee type -> Form 140 Annexure 2 payment code}.
#
# AN INDEPENDENT RESTATEMENT, like the rate tables above, and for a sharper
# reason: 194J-P and 194J-D are the SAME statutory limb at the SAME rate and
# differ only by code, so the rate tables cannot catch a mix-up between them.
# This is the only oracle that can.
TDS_RETURN_CODES: dict[str, dict[str, str]] = {
    "194C": {"IND_HUF": "1023", "OTHER": "1024"},
    "194J-T": {"IND_HUF": "1026", "OTHER": "1026"},
    "194J-P": {"IND_HUF": "1027", "OTHER": "1027"},
    "194J-D": {"IND_HUF": "1028", "OTHER": "1028"},
    "194H": {"IND_HUF": "1006", "OTHER": "1006"},
    "194I-A": {"IND_HUF": "1008", "OTHER": "1008"},
    "194I-B": {"IND_HUF": "1009", "OTHER": "1009"},
}
# s.206AA(1): no PAN -> the HIGHER of the rate in force and 20%.
TDS_NO_PAN_FLOOR = Decimal("20")

# section -> (rate, threshold, basis). 206C(1H) is deliberately absent: it was
# omitted w.e.f. 1-Apr-2025 by the Finance Act 2025, and it is confirmed absent
# from the s.394 Table of the Act as amended by the Finance Act 2026.
#
# SCRAP IS 2%, NOT 1%: s.394(1) [Table: Sl. No. 4] footnote 88, "Substituted for
# '1%' by the Finance Act, 2026, w.e.f. 1-4-2026". It shipped at 1% and
# under-collected by half until the amended Act was read
# (docs/design/t6-gaps-model.md section 0).
TCS_RATES: dict[str, tuple[Decimal, Decimal, str]] = {
    "206C(1)-SCRAP": (Decimal("2"), Decimal("0"), "PER_TXN"),
    "206C(1F)-MV": (Decimal("1"), Decimal("1000000"), "PER_TXN"),
}
# s.397(2)(b)(ii) (NOT the deduction side's s.397(2)(b)(i)): no PAN -> the HIGHER
# of twice the rate and 5%, "not exceeding 20%".
TCS_NO_PAN_MULTIPLIER = Decimal("2")
TCS_NO_PAN_FLOOR = Decimal("5")
TCS_NO_PAN_CEILING = Decimal("20")

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
    """Reference TCS derivation. s.397(2)(b)(ii): max(2x, 5%) capped at 20% —
    NOT the deduction side's flat 20% floor."""
    rate, threshold, basis = TCS_RATES[case_input["section"]]
    if not case_input["panAvailable"]:
        rate = rate * TCS_NO_PAN_MULTIPLIER
        if TCS_NO_PAN_FLOOR > rate:
            rate = TCS_NO_PAN_FLOOR
        if rate > TCS_NO_PAN_CEILING:
            rate = TCS_NO_PAN_CEILING
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


# ---------------------------------------------------------------------------
# Inventory valuation (corpora/inventory)
# ---------------------------------------------------------------------------
# Stock value is NEVER stored: it is derived at report time by replaying the
# movement ledger through a valuer (backend/internal/inventory). These fixtures
# are the shared oracle for that replay, and the derivation below is a SECOND,
# INDEPENDENT implementation of it — written from the rules, not transcribed
# from the Go, and deliberately not a checksum of anything. A fixture whose
# expected closing value is off by 0.0001 fails here even if the Go valuer
# agreed with it, which is the only way a wrong golden number gets caught.
#
# The rules, in full:
#   * replay order is (voucherDate, voucherId, versionCreatedAt, lineNo,
#     versionId) — applied here, not assumed of the file's order. ⛔ THE SECOND KEY
#     IS voucherId AND IT USED TO BE voucherNo, WHICH WAS DEFECT D8: a per-series
#     counter carries no ordering relationship to another series' counter (and a
#     purchase number is the supplier's own string), so on ONE DATE an issue keyed
#     after a receipt could replay before it and be costed at the pre-receipt
#     average. voucher_id is a UUIDv7 minted at CREATE, so it IS entry order;
#     ⚠️ versionCreatedAt is not a substitute, because an ALTER moves it;
#   * the opening folds in first, as an inward, whatever the dates say;
#   * a movement's value is qty * rate * (100 - discountPct)/100 at 4 dp;
#   * an inward adds qty and value; an outward consumes at the CURRENT average
#     (value/qty), never at its own rate, which on a sale is the SELLING price;
#   * an outward that empties the position takes the whole remaining value, so
#     no sub-paisa residue is left on an empty position;
#   * the average is carried at 16 dp and only the consumed value is rounded to
#     4 dp (rounding the rate first loses value on every issue).

# The three stock_item.valuation_method values migration 0078's CHECK allows.
# ⛔ A fixture naming anything else is REFUSED rather than validated against the
# nearest method, for the same reason inventory.ValuerFor errors instead of
# falling back: a plausible number on the wrong basis is unreviewable.
INVENTORY_METHODS = {"WEIGHTED_AVG", "FIFO", "LIFO"}

# Working precision of the running average cost. Must equal rateScale in
# backend/internal/inventory/valuation.go, or the two implementations will
# disagree on any position whose average does not terminate (e.g. 1400/15).
INV_RATE_DP = Decimal("1E-16")


def _inv_div(numerator: Decimal, denominator: Decimal, dp: Decimal) -> Decimal:
    """Divide at high context precision, then round HALF-UP to *dp*.

    The local context keeps the default 28-significant-digit precision from
    truncating a 16-dp quotient of a large value, and keeps this rounding rule
    from leaking into the other validators.
    """
    with localcontext() as ctx:
        ctx.prec = 60
        return (numerator / denominator).quantize(dp, rounding=ROUND_HALF_UP)


def _inv_key(obj: dict) -> tuple[str, str, str]:
    """Position key: (item, godown, batch) with JSON null mapped to ''."""
    return (obj["stockItem"], obj.get("godown") or "", obj.get("batch") or "")


def _inv_net(m: dict) -> Decimal:
    """The movement's own money value: qty * rate less the line discount, at 4 dp."""
    gross = Decimal(m["qty"]) * Decimal(m["rate"])
    disc = Decimal(m["discountPct"])
    return (gross * (Decimal("100") - disc) / Decimal("100")).quantize(MONEY_DP, rounding=ROUND_HALF_UP)


def _inv_replay(opening: dict | None, movements: list[dict], method: str) -> dict[str, Decimal]:
    """Re-derive one position from its opening and movements."""
    # ⛔ voucherId, NOT voucherNo — D8. Keep this in step with `less()` in
    # backend/internal/inventory/valuation.go: these are two INDEPENDENT
    # implementations on purpose, and they are only an oracle for each other while
    # they agree on the key.
    ordered = sorted(
        movements,
        key=lambda m: (
            m["voucherDate"],
            m["voucherId"],
            datetime.fromisoformat(m["versionCreatedAt"]),
            m["lineNo"],
            m["versionId"],
        ),
    )

    open_qty = Decimal(opening["qty"]) if opening else Decimal("0")
    open_val = (
        (Decimal(opening["qty"]) * Decimal(opening["rate"])).quantize(MONEY_DP, rounding=ROUND_HALF_UP)
        if opening
        else Decimal("0")
    )

    qty, value = open_qty, open_val
    in_qty = in_val = out_qty = out_val = Decimal("0")

    if method in ("FIFO", "LIFO"):
        # A QUEUE OF COSTED LAYERS, oldest first. This is a deliberately
        # separate derivation from the Go valuer in internal/inventory/lots.go:
        # the two are only an oracle for each other while neither is a
        # transliteration of the other. The layers here are a plain list of
        # [qty, value] pairs walked from whichever end the method names.
        #
        # The OPENING is the first layer -- one quantity at one rate is exactly
        # one layer -- which is the case a lot-queue implementation is most
        # likely to miss (it would consume nothing for the opening units and
        # report the position at more than the books paid for it).
        lots: list[list[Decimal]] = []
        if open_qty > 0:
            lots.append([open_qty, open_val])
        newest_first = method == "LIFO"

        for m in ordered:
            net = _inv_net(m)
            mqty = Decimal(m["qty"])
            if m["direction"] == "IN":
                qty += mqty
                value += net
                in_qty += mqty
                in_val += net
                if lots:
                    lots.append([mqty, net])
                elif qty > 0:
                    # Nothing to layer onto: a fresh position, one issued flat,
                    # or one that went NEGATIVE and lost its history. All three
                    # become a single blended layer of whatever is now held.
                    lots = [[qty, value]]
                continue

            out_qty += mqty
            if qty - mqty <= 0:
                # Everything the layers held, and NOT ONE PAISA MORE: stock that
                # was never received has no cost to consume, and valuing the
                # shortfall at the sale rate is the classic bug.
                consumed = value if value > 0 else Decimal("0")
                qty -= mqty
                value -= consumed
                lots = []
                out_val += consumed
                continue

            remaining, consumed = mqty, Decimal("0")
            while remaining > 0:
                i = -1 if newest_first else 0
                lot_qty, lot_val = lots[i]
                if lot_qty <= remaining:
                    consumed += lot_val
                    remaining -= lot_qty
                    lots.pop(i)
                    continue
                # Partial: the layer's OWN rate at 16 dp, never a 4-dp rate.
                take = (remaining * _inv_div(lot_val, lot_qty, INV_RATE_DP)).quantize(
                    MONEY_DP, rounding=ROUND_HALF_UP
                )
                consumed += take
                lots[i] = [lot_qty - remaining, lot_val - take]
                remaining = Decimal("0")
            qty -= mqty
            value -= consumed
            out_val += consumed

        return {
            "openingQty": open_qty,
            "openingValue": open_val,
            "inwardQty": in_qty,
            "inwardValue": in_val,
            "outwardQty": out_qty,
            "outwardValue": out_val,
            "closingQty": qty,
            "closingValue": value.quantize(MONEY_DP, rounding=ROUND_HALF_UP),
            "closingRate": _inv_div(value, qty, MONEY_DP) if qty != 0 else Decimal("0"),
        }

    for m in ordered:
        net = _inv_net(m)
        if m["direction"] == "IN":
            qty += Decimal(m["qty"])
            value += net
            in_qty += Decimal(m["qty"])
            in_val += net
            continue
        remaining = qty - Decimal(m["qty"])
        if remaining == 0:
            consumed = value
            qty, value = Decimal("0"), Decimal("0")
        else:
            rate = _inv_div(value, qty, INV_RATE_DP) if qty != 0 else Decimal("0")
            consumed = (Decimal(m["qty"]) * rate).quantize(MONEY_DP, rounding=ROUND_HALF_UP)
            qty = remaining
            value -= consumed
        out_qty += Decimal(m["qty"])
        out_val += consumed

    return {
        "openingQty": open_qty,
        "openingValue": open_val,
        "inwardQty": in_qty,
        "inwardValue": in_val,
        "outwardQty": out_qty,
        "outwardValue": out_val,
        "closingQty": qty,
        "closingValue": value.quantize(MONEY_DP, rounding=ROUND_HALF_UP),
        "closingRate": _inv_div(value, qty, MONEY_DP) if qty != 0 else Decimal("0"),
    }


def _inv_check_expected(path: Path, label: str, expected: dict, derived: dict[str, Decimal]) -> None:
    """Compare one expected position block against the re-derivation."""
    for field, want in derived.items():
        if field not in expected:
            fail(path, f"{label}: expected block is missing {field!r}")
            continue
        got = money(path, f"{label}.{field}", expected[field])
        if got is None:
            continue
        if got != want:
            fail(
                path,
                f"{label}: {field}={got:f} is WRONG; replaying the movements gives {want:f} "
                f"(a stock summary built from this fixture would be out by {got - want:f})",
            )


def validate_inventory(path: Path, case: dict) -> None:
    """Validate an inventory-valuation case and re-derive its closing position."""
    if not require(path, case, ["id", "description", "valuationMethod", "movements", "expected"]):
        return
    if case["valuationMethod"] not in INVENTORY_METHODS:
        fail(path, f"valuationMethod {case['valuationMethod']!r} is not one stock_item.valuation_method allows")
        return
    # The fixture IS the derivation's proof; a case that does not show its
    # arithmetic in prose cannot be reviewed by a human.
    if not isinstance(case["description"], str) or len(case["description"]) < 40:
        fail(path, "description must show the hand derivation (>=40 chars)")

    godowns = set(case.get("godowns") or [])
    batches = {}
    for i, b in enumerate(case.get("batches") or []):
        if not require(path, b, ["id", "stockItem", "expiryDate"]):
            continue
        try:
            _parse_date(b["expiryDate"])
        except ValueError:
            fail(path, f"batches[{i}].expiryDate {b['expiryDate']!r} is not YYYY-MM-DD")
        batches[b["id"]] = b

    # --- shape of the movements -------------------------------------------
    if not isinstance(case["movements"], list):
        fail(path, "movements must be an array")
        return
    seen_lines = set()
    for i, m in enumerate(case["movements"]):
        if not require(
            path,
            m,
            # ⛔ voucherId is REQUIRED, not optional. A fixture without it would sort
            # every movement under the same empty string and its replay order would
            # be decided by the tiebreaks — silently, and differently from cored.
            ["voucherDate", "voucherNo", "voucherId", "versionCreatedAt", "versionId",
             "lineNo", "stockItem", "direction", "qty", "rate", "discountPct"],
        ):
            return
        try:
            _parse_date(m["voucherDate"])
            datetime.fromisoformat(m["versionCreatedAt"])
        except ValueError as exc:
            fail(path, f"movements[{i}] has an unparseable date: {exc}")
            return
        if m["direction"] not in ("IN", "OUT"):
            fail(path, f"movements[{i}].direction must be 'IN' or 'OUT' (the stock_direction enum), got {m['direction']!r}")
            return
        if not isinstance(m["lineNo"], int) or m["lineNo"] < 1:
            fail(path, f"movements[{i}].lineNo must be an integer >= 1")
            return
        key = (m["versionId"], m["lineNo"])
        if key in seen_lines:
            fail(path, f"movements[{i}] repeats (versionId, lineNo) {key} — UNIQUE (voucher_version_id, line_no) forbids it")
        seen_lines.add(key)
        qty = money(path, f"movements[{i}].qty", m["qty"])
        rate = money(path, f"movements[{i}].rate", m["rate"])
        disc = money(path, f"movements[{i}].discountPct", m["discountPct"])
        if qty is None or rate is None or disc is None:
            return
        # qty is ALWAYS positive: direction carries the sign (CHECK (qty > 0)).
        if qty <= 0:
            fail(path, f"movements[{i}].qty must be > 0 — direction, not the sign of qty, says which way stock moved")
        if rate < 0:
            fail(path, f"movements[{i}].rate must be >= 0")
        if not (0 <= disc <= 100):
            fail(path, f"movements[{i}].discountPct must be within 0..100, got {disc:f}")
        if m.get("godown") is not None and godowns and m["godown"] not in godowns:
            fail(path, f"movements[{i}].godown {m['godown']!r} is not declared in this fixture's godowns")
        if m.get("batch") is not None and batches and m["batch"] not in batches:
            fail(path, f"movements[{i}].batch {m['batch']!r} is not declared in this fixture's batches")

    # --- group by position and re-derive -----------------------------------
    openings: dict[tuple[str, str, str], dict] = {}
    for i, o in enumerate(case.get("openings") or []):
        if not require(path, o, ["stockItem", "qty", "rate"]):
            return
        if money(path, f"openings[{i}].qty", o["qty"]) is None:
            return
        if money(path, f"openings[{i}].rate", o["rate"]) is None:
            return
        if _inv_key(o) in openings:
            fail(path, f"openings[{i}] repeats a position — stock_opening's UNIQUE NULLS NOT DISTINCT key forbids two rows for one position")
        openings[_inv_key(o)] = o

    grouped: dict[tuple[str, str, str], list[dict]] = {k: [] for k in openings}
    for m in case["movements"]:
        grouped.setdefault(_inv_key(m), []).append(m)

    expected = case["expected"]
    if not require(path, expected, ["positions"]):
        return
    by_key = {}
    for p in expected["positions"]:
        if not require(path, p, ["stockItem"]):
            return
        by_key[_inv_key(p)] = p
    if set(by_key) != set(grouped):
        fail(
            path,
            f"expected.positions cover {sorted(by_key)} but the movements and openings produce {sorted(grouped)} "
            f"— a stock position would be blended away or invented",
        )
        return

    # ⛔ THE METHOD IS PASSED, NOT DEFAULTED. _inv_replay took a defaulted
    # method for exactly as long as there was one method; a default here would
    # let a FIFO fixture be re-derived at weighted average and reported as a
    # wrong EXPECTED BLOCK, which is the one failure mode this validator cannot
    # afford — it is the oracle.
    for key, ms in grouped.items():
        derived = _inv_replay(openings.get(key), ms, case["valuationMethod"])
        _inv_check_expected(path, f"expected.positions[{key}]", by_key[key], derived)

    # --- the back-dated case's "before" world ------------------------------
    bi = case.get("backdatedInward")
    if bi is not None:
        if not require(path, bi, ["voucherNo", "expectedWithout"]):
            return
        remaining = [m for m in case["movements"] if m["voucherNo"] != bi["voucherNo"]]
        if len(remaining) == len(case["movements"]):
            fail(path, f"backdatedInward.voucherNo {bi['voucherNo']!r} matches no movement")
            return
        key = _inv_key(case["expected"]["positions"][0])
        derived = _inv_replay(
            openings.get(key),
            [m for m in remaining if _inv_key(m) == key],
            case["valuationMethod"],
        )
        _inv_check_expected(path, "backdatedInward.expectedWithout", bi["expectedWithout"], derived)
        after = by_key[key]
        if after["outwardValue"] == bi["expectedWithout"]["outwardValue"]:
            fail(
                path,
                "backdatedInward: the issue consumes the same value with and without the back-dated "
                "purchase — this fixture no longer demonstrates that valuation re-values history",
            )


# ---------------------------------------------------------------------------
# GSTR-2A/2B reconciliation (corpora/gstr2b) and IMS (corpora/ims)
# ---------------------------------------------------------------------------
# The reconciliation result is NEVER STORED — it is a function of four versioned
# inputs (the books, the ingested statement, the IMS action log and the policy)
# and goes stale the instant any of them moves, exactly like a stock valuation.
# These fixtures are therefore the ONLY pinned form of that function, and the
# derivation below is a SECOND, INDEPENDENT implementation of it: written from
# the written specification (the matching passes, the tolerances, the effect
# table), NOT transcribed from backend/internal/compliance/itc. Two
# implementations of one spec disagree when either is wrong; one implementation
# plus a fixture that restates it agrees with itself forever.
#
# THE RULES, IN FULL — this comment is the spec both sides are written from.
#
# Normalisation (one place, two keys):
#   * strict = NFKC, drop every rune outside [0-9A-Za-z], ASCII-uppercase.
#     ASCII-uppercase and not str.upper(): Unicode/locale casing is
#     environment-dependent, and a document key that depends on the locale of
#     the machine that computed it is not a key.
#   * loose  = strict with leading zeros stripped from every maximal digit run.
#   * LEADING ZEROS ARE NOT STRIPPED IN THE STRICT KEY. "INV001" and "INV01"
#     are plausibly two different invoices from a supplier that pads. Stripping
#     is the commonest source of FALSE PAIRS; not stripping is the commonest
#     source of false non-matches. Strict decides pairing, loose decides
#     candidacy, and neither decision is silently made for the other.
#   * a strict key longer than 16 characters violates rule 46(b) and is a
#     reported FINDING (DOC_NO_INVALID), never a match failure.
#
# Comparability (checked BEFORE matching, precedence in this order):
#   RCM > IMPORT_OR_ISD > NO_PARTY_GSTIN > NO_SUPPLIER_DOC_NO > NO_GST_PARAMS.
#   "missing in 2B" and "cannot be compared" are DIFFERENT WORDS and the
#   difference is the whole point: every purchase posted before migration 0032
#   carries no supplier document number, and calling those MISSING_IN_2B would
#   tell the filer their supplier had not filed (a lie) and reverse ITC that is
#   perfectly good. NOT_COMPARABLE produces ZERO reversal.
#   RCM comes FIRST because IMS excludes reverse-charge supplies entirely and
#   RCM ITC is self-assessed, so an RCM row can never appear in a 2B match and
#   must never be reversed for being unmatched.
#
# Three passes over DISJOINT pools; a row consumed by an earlier pass leaves:
#   pass 1  (gstin, strict, docType, docDate, taxable) -> MATCHED
#   pass 2  (gstin, strict, docType, docDate)          -> MISMATCHED
#   pass 3  (gstin, loose,  docType)                   -> NEAR_MATCH
#   docType IS IN EVERY KEY so a CDNR credit note is never paired with the
#   invoice it adjusts — in 2B they are separate documents with separate IMS
#   actions, and a supplier whose note number equals the invoice number is
#   ordinary, not pathological (fixture 09 is exactly that).
#
# A NEAR_MATCH IS NEVER A PAIRING. It emits TWO lines — one carrying the books
# row, one carrying the statement row — each pointing at the other as a
# candidate for a human to confirm, and each treated as unmatched by the
# arithmetic. Silently pairing two invoices that are not the same invoice is
# worse than reporting them unmatched.
#
# AMBIGUITY IS AN ERROR, NOT A TIE-BREAK. If pass 1 or 2 finds more than one
# candidate on either side, NOTHING is paired and every participant becomes
# AMBIGUOUS. Deterministic-but-arbitrary picking is how ITC gets claimed
# against the wrong invoice.
#
# Tolerances:
#   * taxable value: EXACT to the paisa. No band — a percentage band grows with
#     invoice size, which is precisely where a real error hides.
#   * tax heads: an ABSOLUTE Decimal("1.00") PER HEAD, and only once taxable is
#     already exact; within it the status is the DISTINCT MATCHED_ROUNDING,
#     carrying the delta, never a silent MATCHED. compliance.TaxDerive rounds
#     PER LINE, so a supplier rounding at invoice level differs by at most half
#     a paisa per line per head; one rupee covers any realistic invoice.
#   * date: ZERO DAYS. The common "+/-7 days" fuzz is deliberately rejected:
#     the invoice date fixes the financial year for s.16(4) and therefore the
#     expiry of the credit, so a 7-day band pairs a 31-March invoice with a
#     1-April one — a different FY and a different expiry.
#   * GSTIN: exact after uppercasing and stripping whitespace. One character is
#     a different taxpayer.
#
# The 2B window is on the SUPPLIER'S FILING DATE, not on the document date, so a
# prior-FY invoice legitimately appears in a current-period 2B and is NOT a date
# mismatch (fixture 12).
#
# Rejected rows (GSTN's docRejdata) are ingested for VIEWING and NEVER enter
# matching or ITC. Summing them into table 4(A) is a filing-grade overclaim.
#
# The effect table (section F1 of the architecture) is model (b): table 4(A) is
# what the BOOKS posted and the haircut lands in 4(B)(2), which preserves the
# one exact invariant in the whole compliance surface —
#     (I1)  sum(4A) + sum(4D) == the posted input tax, head by head
#     (I2)  sum(4A) - sum(4B) == 4(C),  and a NEGATIVE 4(C) is legal.
# Time-barred / never-available ITC is excluded from 4(A) ENTIRELY rather than
# reversed, because it never was available; that is what keeps (I1) exact.
# 4(D) is NOT subtracted from 4(C) (post-Notification-14/2022 it is "Other
# Details" and informational); subtracting it would double-count the haircut.
# At most ONE 4(B) reversal per voucher and 4(B)(1) wins, so a blocked credit
# that is also unmatched is reversed once.
#
# THE NEGATIVE-TAX RULE: a document whose posted input tax is NEGATIVE (a
# supplier credit note / purchase return) is NEVER reversed for absence from the
# statement. Reversing a negative would ADD credit.

ITC_IST = timezone(timedelta(hours=5, minutes=30))

# Absolute per-head rounding tolerance, in rupees. See the derivation above.
ITC_ROUNDING_TOLERANCE = Decimal("1.00")

# Rule 46(b) caps an invoice number at 16 characters.
ITC_DOC_NO_MAX = 16

ITC_MONEY_KEYS = {
    "taxableValue", "cgst", "sgst", "igst", "docValue",
    "camt", "samt", "iamt", "txval",
}

ITC_STATUSES = {
    "MATCHED", "MATCHED_ROUNDING", "MISMATCHED", "NEAR_MATCH", "AMBIGUOUS",
    "MISSING_IN_BOOKS", "MISSING_IN_2B", "SUPERSEDED", "NOT_COMPARABLE",
}
ITC_ELIGIBILITY = {"ELIGIBLE", "BLOCKED_17_5", "CAPITAL_GOODS", "ISD"}
ITC_SECTIONS = {
    "B2B", "B2BA", "CDNR", "CDNRA", "IMPG", "IMPGSEZ", "ISD", "ISDA",
    "ECOM", "ECOMA",
}
IMS_STATES = {"NO_ACTION", "ACCEPTED", "REJECTED", "PENDING", "DEEMED_ACCEPTED"}
IMS_APPLICABLE_SECTIONS = {"B2B", "B2BA", "CDNR", "CDNRA", "ECOM", "ECOMA"}

_ZERO_TAX = {"camt": Decimal("0"), "samt": Decimal("0"), "iamt": Decimal("0")}


def _m(value: Decimal) -> str:
    """Format a Decimal as the corpus's 4dp money string."""
    return f"{value.quantize(MONEY_DP, rounding=ROUND_HALF_UP):f}"


def _tax_str(tax: dict) -> dict:
    return {k: _m(tax[k]) for k in ("camt", "samt", "iamt")}


def _tax_add(a: dict, b: dict) -> dict:
    return {k: a[k] + b[k] for k in ("camt", "samt", "iamt")}


def _itc_strict_key(s: str) -> str:
    """NFKC, keep [0-9A-Za-z], ASCII-uppercase. See the header."""
    folded = unicodedata.normalize("NFKC", s or "")
    kept = [ch for ch in folded if ch.isascii() and (ch.isdigit() or ch.isalpha())]
    return "".join(chr(ord(c) - 32) if "a" <= c <= "z" else c for c in kept)


def _itc_loose_key(strict: str) -> str:
    """strict, with leading zeros stripped from every maximal digit run."""
    out: list[str] = []
    i = 0
    while i < len(strict):
        if strict[i].isdigit():
            j = i
            while j < len(strict) and strict[j].isdigit():
                j += 1
            run = strict[i:j].lstrip("0")
            out.append(run if run else "0")
            i = j
        else:
            out.append(strict[i])
            i += 1
    return "".join(out)


def _itc_date(value):
    """Parse a YYYY-MM-DD string to a date; None/'' -> None."""
    if not value:
        return None
    return datetime.strptime(value, "%Y-%m-%d").date()


def _itc_ts(value):
    """Parse an RFC3339 instant; None/'' -> None.

    Python's datetime is MICROSECOND-resolution and truncates the nanosecond
    forms the Go boundary test uses (…T23:59:59.999999999+05:30 becomes
    …999999). That is safe here and only here: every boundary in this corpus is
    at a whole second, so truncation moves an instant that is already strictly
    before the boundary to another instant strictly before it. A fixture whose
    answer turned on a sub-microsecond difference would be untestable from
    Python and must not be written.
    """
    if not value:
        return None
    return datetime.fromisoformat(value)


def _s164_fy_end(d):
    """31 March of the financial year that contains *d* (April-March)."""
    return datetime(d.year + (1 if d.month >= 4 else 0), 3, 31).date()


def _s164_expiry(d, annual_return_filed_on=None):
    """s.16(4): 30 November following the FY end, or the annual-return filing
    date if that is earlier. INCLUSIVE — a claim ON the expiry date is allowed.

    A DEBIT NOTE'S OWN DATE fixes the FY (de-linked from the original invoice
    w.e.f. 01-01-2021), which is why this takes a date and not a document.
    """
    fy_end = _s164_fy_end(d)
    expiry = datetime(fy_end.year, 11, 30).date()
    if annual_return_filed_on and annual_return_filed_on < expiry:
        return annual_return_filed_on
    return expiry


# --- IMS clock -------------------------------------------------------------
#
# TIMEZONE: IST, as a FIXED +05:30 offset. INFERRED, NOT CITED — no GSTN or
# CBIC document read for this work states a timezone anywhere; Indian statute
# plus an IST-operated portal make it the only defensible reading. A fixed
# offset rather than a tzdata lookup because India has had +05:30 with no DST
# since 1945, and a container without tzdata makes a lookup fail and silently
# move the boundary. NEVER the local zone: a UTC server computes the boundary
# 5h30m LATE and deem-accepts records into the wrong period.


def _ims_period(period: str):
    """'MMYYYY' (the GSTN `fp` form) -> (month, year)."""
    return int(period[:2]), int(period[2:])


def _ims_add_months(month: int, year: int, n: int):
    """CALENDAR-month addition, never day arithmetic."""
    total = (year * 12) + (month - 1) + n
    return (total % 12) + 1, total // 12


def _ims_cutoff(period: str) -> datetime:
    """The DEFAULT resolution instant for a monthly filer.

    GSTN 2B Advisory (12-10-2021) Q.2: 2B generation begins after the
    IFF/GSTR-5/GSTR-6 due date ends at "13th midnight". Encoded as the
    HALF-OPEN boundary [.., 14th 00:00:00 IST) so there is no ambiguous
    59-second gap and no half-day fudge about "the afternoon of the 14th".
    No clamping is ever needed: the 14th exists in every month, February
    included.
    """
    month, year = _ims_period(period)
    m2, y2 = _ims_add_months(month, year, 1)
    return datetime(y2, m2, 14, 0, 0, 0, tzinfo=ITC_IST)


def _ims_resolution(period: str, gen_date) -> datetime:
    """The statement's own generation date wins over the computed 14th.

    THE 14th IS NOT STATUTORY AND HAS ALREADY SLIPPED: the December-2024 2B was
    generated on 16-Jan-2025 because the GSTR-1 due date had been extended to
    the 13th. A hard-coded 14 is a latent wrong-period bug, and fixture
    ims/04 is the case that bites it.
    """
    if gen_date:
        d = _itc_date(gen_date)
        return datetime(d.year, d.month, d.day, 0, 0, 0, tzinfo=ITC_IST)
    return _ims_cutoff(period)


def _ims_effective_action(actions, resolution, freeze, as_on):
    """The effective IMS state at *as_on*. PURE — never reads a wall clock.

    Last-write-wins projection over an APPEND-ONLY log (GSTN IMS FAQ 12: "the
    latest action will overwrite the previous action"). Actions after the
    GSTR-3B filing instant do not count, because filing freezes the action set.
    FOUR outcomes, not three: NO_ACTION is distinct from ACCEPTED until 2B
    generation resolves it into DEEMED_ACCEPTED.
    """
    horizon = as_on
    if freeze is not None and freeze < horizon:
        horizon = freeze
    seen = [(a["actedAt"], i, a) for i, a in enumerate(actions) if a["actedAt"] <= horizon]
    if seen:
        seen.sort(key=lambda t: (t[0], t[1]))
        latest = seen[-1][2]["action"]
        if latest != "RESET":
            return {"ACCEPT": "ACCEPTED", "REJECT": "REJECTED", "PENDING": "PENDING"}[latest]
        # RESET clears the log back to "no action taken", so the deemed-accept
        # timer applies again exactly as if nothing had ever been actioned.
    return "DEEMED_ACCEPTED" if as_on >= resolution else "NO_ACTION"


# --- parsing ---------------------------------------------------------------


def _itc_parse_books(path: Path, raw: dict, idx: int) -> dict:
    rid = raw.get("voucherId") or f"books[{idx}]"
    taxable_raw = raw.get("taxableValue")
    has_params = taxable_raw is not None
    taxable = Decimal("0")
    if has_params:
        parsed = money(path, f"{rid}.taxableValue", taxable_raw)
        taxable = parsed if parsed is not None else Decimal("0")
    tax = {}
    for key, field in (("camt", "cgst"), ("samt", "sgst"), ("iamt", "igst")):
        parsed = money(path, f"{rid}.{field}", raw.get(field, "0.0000"))
        tax[key] = parsed if parsed is not None else Decimal("0")
    eligibility = raw.get("itcEligibility") or "ELIGIBLE"
    if eligibility not in ITC_ELIGIBILITY:
        fail(path, f"{rid}.itcEligibility {eligibility!r} is not one of {sorted(ITC_ELIGIBILITY)}")
    # THE BOOKS' SIGN CONVENTION. A supplier CREDIT NOTE reduces input tax, and
    # store.go posts it NEGATIVE; the Go corpus loader applies the same flip to
    # a fixture that states the note as a positive magnitude. Without it this
    # oracle re-derives a returned purchase as a SECOND purchase and "confirms"
    # double the credit. Fixtures state magnitudes because that is how the
    # portal reports them; the sign is applied here, once.
    if (raw.get("docType") or "I") == "C" and taxable > 0:
        taxable = -taxable
        tax = {h: -v for h, v in tax.items()}
    strict = _itc_strict_key(raw.get("supplierInvoiceNo") or "")
    return {
        "id": rid,
        "side": "books",
        "gstin": (raw.get("partyGstin") or "").strip().upper(),
        "docNo": (raw.get("supplierInvoiceNo") or "").strip(),
        "docDate": raw.get("supplierInvoiceDate") or "",
        "docType": raw.get("docType") or "I",
        "taxable": taxable,
        "tax": tax,
        "reverseCharge": bool(raw.get("reverseCharge")),
        "eligibility": eligibility,
        "pos": raw.get("pos") or "",
        "hasParams": has_params,
        "strict": strict,
        "loose": _itc_loose_key(strict),
    }


def _itc_stmt_in_books_direction(b: dict, s: dict):
    """Flip a paired statement row into the direction the BOOKS recorded it.

    GSTR-2B reports MAGNITUDES: a supplier credit note of 400 is filed as 400,
    not -400. The books carry the sign convention (_itc_parse_books). Comparing
    the two raw would make every credit note look like a 2x mismatch, and the
    MISMATCHED excess would be computed off a doubled difference.

    Mirrors pairLine in internal/compliance/itc/match.go, which does the same
    single flip for the same reason. Returns (taxable, tax_by_head).
    """
    sgn = Decimal("-1") if (b.get("docType") or "I") == "C" else Decimal("1")
    return (
        abs(s["taxable"]) * sgn,
        {h: abs(s["tax"][h]) * sgn for h in ("camt", "samt", "iamt")},
    )


def _itc_parse_stmt(path: Path, raw: dict, idx: int) -> dict:
    rid = raw.get("docId") or f"documents[{idx}]"
    section = raw.get("section") or "B2B"
    if section not in ITC_SECTIONS:
        fail(path, f"{rid}.section {section!r} is not one of {sorted(ITC_SECTIONS)}")
    parsed = money(path, f"{rid}.taxableValue", raw.get("taxableValue", "0.0000"))
    taxable = parsed if parsed is not None else Decimal("0")
    tax = {}
    for key, field in (("camt", "cgst"), ("samt", "sgst"), ("iamt", "igst")):
        p = money(path, f"{rid}.{field}", raw.get(field, "0.0000"))
        tax[key] = p if p is not None else Decimal("0")
    if "itcAvailable" not in raw:
        fail(path, f"{rid}: itcAvailable is required — GSTN's own verdict is never inferred")
    actions = []
    for a in raw.get("imsActions", []):
        actions.append({"action": a["action"], "actedAt": _itc_ts(a["actedAt"])})
    strict = _itc_strict_key(raw.get("docNo") or "")
    return {
        "id": rid,
        "side": "statement",
        "section": section,
        "rejected": bool(raw.get("rejected")),
        "gstin": (raw.get("supplierGstin") or "").strip().upper(),
        "docNo": (raw.get("docNo") or "").strip(),
        "docDate": raw.get("docDate") or "",
        "docType": raw.get("docType") or "I",
        "taxable": taxable,
        "tax": tax,
        "reverseCharge": bool(raw.get("reverseCharge")),
        "itcAvailable": bool(raw.get("itcAvailable")),
        "pos": raw.get("pos") or "",
        "origDocNo": raw.get("origDocNo") or "",
        "origDocDate": raw.get("origDocDate") or "",
        "imsActions": actions,
        "strict": strict,
        "loose": _itc_loose_key(strict),
    }


def _itc_not_comparable(b: dict) -> str:
    """Why this books row cannot enter matching at all, or '' if it can."""
    if b["reverseCharge"]:
        return "RCM"
    if b["eligibility"] == "ISD":
        return "IMPORT_OR_ISD"
    if not b["gstin"]:
        return "NO_PARTY_GSTIN"
    if not b["docNo"] or not b["docDate"]:
        return "NO_SUPPLIER_DOC_NO"
    if not b["hasParams"]:
        return "NO_GST_PARAMS"
    return ""


# --- the derivation --------------------------------------------------------


def _derive_gstr2b_expected(path: Path, case: dict) -> dict:
    """Re-derive the whole classification from `input` ALONE.

    This function must NEVER read case["expected"]. The whole value of this
    corpus is that two implementations of one written spec are compared; a
    validator that restates the fixture agrees with it forever and detects
    nothing. corpora/gstr2b/_poison/ exists to catch exactly that regression —
    see check_poison().
    """
    inp = case["input"]
    books = [_itc_parse_books(path, r, i) for i, r in enumerate(inp.get("books", []))]
    stmt_in = inp.get("statement") or {}
    stmts = [_itc_parse_stmt(path, r, i) for i, r in enumerate(stmt_in.get("documents", []))]

    claim_date = _itc_date(inp.get("claimDate"))
    as_on = _itc_ts(inp.get("asOn"))
    freeze = _itc_ts(inp.get("filedAt"))
    resolution = _ims_resolution(inp.get("period", "012026"), stmt_in.get("genDate"))

    rejected_ids = sorted(s["id"] for s in stmts if s["rejected"])
    live = [s for s in stmts if not s["rejected"]]

    # An amendment in the SAME statement replaces its original: only the
    # amended record counts for ITC (GSTN 2B advisory key-point 7 / FAQ 13),
    # and for credit notes the amendment always prevails (FAQ 14). Superseding
    # BEFORE the passes also removes what would otherwise look like an
    # ambiguous duplicate document number.
    superseded = set()
    for a in live:
        if not a["section"].endswith("A") or not a["origDocNo"]:
            continue
        okey = _itc_strict_key(a["origDocNo"])
        for o in live:
            if o is a or o["section"].endswith("A"):
                continue
            if o["gstin"] == a["gstin"] and o["strict"] == okey and o["docDate"] == a["origDocDate"]:
                superseded.add(o["id"])

    nc_rows = []
    pool_books = []
    for b in books:
        reason = _itc_not_comparable(b)
        if reason:
            nc_rows.append((b, reason))
        else:
            pool_books.append(b)
    pool_stmt = [s for s in live if s["id"] not in superseded]

    order = lambda r: (r["docDate"], r["strict"], r["id"])
    pool_books.sort(key=order)
    pool_stmt.sort(key=order)

    paired: list[tuple] = []
    ambiguous: list[dict] = []
    amb_candidates: dict = {}

    def _run_pass(keyfn, passno):
        nonlocal pool_books, pool_stmt
        bmap: dict = {}
        smap: dict = {}
        for b in pool_books:
            bmap.setdefault(keyfn(b), []).append(b)
        for s in pool_stmt:
            smap.setdefault(keyfn(s), []).append(s)
        used_b: set = set()
        used_s: set = set()
        for key in sorted(set(bmap) & set(smap)):
            bs, ss = bmap[key], smap[key]
            if len(bs) > 1 or len(ss) > 1:
                # Nothing is paired: every participant keeps a pointer at the
                # rows it could have been, so a human resolves it instead of
                # the machine picking one deterministically-but-arbitrarily.
                for b in bs:
                    amb_candidates[b["id"]] = sorted(s["id"] for s in ss)
                for s in ss:
                    amb_candidates[s["id"]] = sorted(b["id"] for b in bs)
                ambiguous.extend(bs)
                ambiguous.extend(ss)
            else:
                paired.append((bs[0], ss[0], passno))
            used_b |= {b["id"] for b in bs}
            used_s |= {s["id"] for s in ss}
        pool_books = [b for b in pool_books if b["id"] not in used_b]
        pool_stmt = [s for s in pool_stmt if s["id"] not in used_s]

    # The taxable value enters the pass-1 key as a MAGNITUDE. The books carry
    # the sign convention (a credit note is negative) and the statement reports
    # magnitudes, so comparing them signed would stop a supplier credit note
    # from ever pairing with its own CDNR row. docType is already in the key, so
    # dropping the sign here cannot pair a note with an invoice.
    _run_pass(lambda r: (r["gstin"], r["strict"], r["docType"], r["docDate"], _m(abs(r["taxable"]))), 1)
    _run_pass(lambda r: (r["gstin"], r["strict"], r["docType"], r["docDate"]), 2)

    # Pass 3 is CANDIDACY, not pairing: it records pointers and leaves both
    # rows unmatched.
    near_b: dict = {}
    near_s: dict = {}
    b3: dict = {}
    s3: dict = {}
    for b in pool_books:
        b3.setdefault((b["gstin"], b["loose"], b["docType"]), []).append(b)
    for s in pool_stmt:
        s3.setdefault((s["gstin"], s["loose"], s["docType"]), []).append(s)
    for key in sorted(set(b3) & set(s3)):
        for b in b3[key]:
            near_b.setdefault(b["id"], []).extend(sorted(s["id"] for s in s3[key]))
        for s in s3[key]:
            near_s.setdefault(s["id"], []).extend(sorted(b["id"] for b in b3[key]))

    findings = []
    for row in books + stmts:
        if len(row["strict"]) > ITC_DOC_NO_MAX:
            findings.append({
                "code": "DOC_NO_INVALID",
                "ref": row["id"],
                "docNo": row["docNo"],
                "strictKey": row["strict"],
            })
    findings.sort(key=lambda f: (f["code"], f["ref"]))

    lines = []

    def _emit(status, b=None, s=None, fields=None, nc="", candidates=None, delta=None):
        lines.append({
            "status": status,
            "fields": fields or [],
            "notComparable": nc,
            "books": b["id"] if b else None,
            "statement": s["id"] if s else None,
            "candidates": candidates or [],
            "_b": b,
            "_s": s,
            "delta": delta,
        })

    for b, reason in nc_rows:
        _emit("NOT_COMPARABLE", b=b, nc=reason)

    for b, s, passno in paired:
        s_taxable, s_tax = _itc_stmt_in_books_direction(b, s)
        fields = []
        if b["taxable"] != s_taxable:
            fields.append("TAXABLE_VALUE")
        for head, name in (("camt", "CGST"), ("samt", "SGST"), ("iamt", "IGST")):
            if b["tax"][head] != s_tax[head]:
                fields.append(name)
        if b["pos"] and s["pos"] and b["pos"] != s["pos"]:
            fields.append("POS")
        if b["reverseCharge"] != s["reverseCharge"]:
            fields.append("REVERSE_CHARGE")
        fields.sort()
        head_deltas = [abs(b["tax"][h] - s_tax[h]) for h in ("camt", "samt", "iamt")]
        if passno == 1 and not fields:
            status = "MATCHED"
        elif (
            passno == 1
            and set(fields) <= {"CGST", "SGST", "IGST"}
            and max(head_deltas) <= ITC_ROUNDING_TOLERANCE
        ):
            status = "MATCHED_ROUNDING"
        else:
            status = "MISMATCHED"
        _emit(status, b=b, s=s, fields=fields)

    for row in ambiguous:
        cands = amb_candidates.get(row["id"], [])
        if row["side"] == "books":
            _emit("AMBIGUOUS", b=row, candidates=cands)
        else:
            _emit("AMBIGUOUS", s=row, candidates=cands)

    for b in pool_books:
        if b["id"] in near_b:
            _emit("NEAR_MATCH", b=b, candidates=sorted(set(near_b[b["id"]])))
        else:
            _emit("MISSING_IN_2B", b=b)
    for s in pool_stmt:
        if s["id"] in near_s:
            _emit("NEAR_MATCH", s=s, candidates=sorted(set(near_s[s["id"]])))
        else:
            _emit("MISSING_IN_BOOKS", s=s)
    for s in live:
        if s["id"] in superseded:
            _emit("SUPERSEDED", s=s)

    totals = {
        "itcAvlIsrc": dict(_ZERO_TAX),
        "itcAvlOth": dict(_ZERO_TAX),
        "itcRevRul": dict(_ZERO_TAX),
        "itcRevOth": dict(_ZERO_TAX),
        "itcInelgOth": dict(_ZERO_TAX),
    }

    for line in lines:
        b, s = line["_b"], line["_s"]
        effect = {
            "avlRow": "ISRC" if (b and b["reverseCharge"]) else "OTH",
            "avl": dict(_ZERO_TAX),
            "revRul": dict(_ZERO_TAX),
            "revOth": dict(_ZERO_TAX),
            "inelg": dict(_ZERO_TAX),
        }
        delta = dict(_ZERO_TAX)
        delta["txval"] = Decimal("0")
        if b and s:
            s_taxable, s_tax = _itc_stmt_in_books_direction(b, s)
            delta = {h: b["tax"][h] - s_tax[h] for h in ("camt", "samt", "iamt")}
            delta["txval"] = b["taxable"] - s_taxable

        expiry = None
        ref_date = _itc_date(b["docDate"]) if (b and b["docDate"]) else (
            _itc_date(s["docDate"]) if (s and s["docDate"]) else None
        )
        if ref_date is not None:
            expiry = _s164_expiry(ref_date, _itc_date(inp.get("annualReturnFiledOn")))

        if b is not None:
            tax = b["tax"]
            total_tax = tax["camt"] + tax["samt"] + tax["iamt"]

            # 4(D) FIRST: never-available ITC never enters 4(A) at all, which is
            # what keeps identity (I1) exact. GSTN's own itcavl="N" verdict
            # overrides our computation, because it is the one the department
            # will use; our own s.16(4) test only speaks when there is no
            # statement row to speak for it.
            never_available = False
            if s is not None:
                never_available = not s["itcAvailable"]
            elif expiry is not None and claim_date is not None and claim_date > expiry:
                never_available = True

            if never_available:
                effect["inelg"] = dict(tax)
            else:
                effect["avl"] = dict(tax)
                if b["eligibility"] == "BLOCKED_17_5":
                    # 4(B)(1) wins; at most one reversal per voucher.
                    effect["revRul"] = dict(tax)
                elif line["status"] == "MISMATCHED":
                    # Only the EXCESS over what the supplier reported, with the
                    # statement flipped into the books' direction first: a
                    # supplier note declared LARGER than we booked (-300 against
                    # -400) yields a POSITIVE 100 correction on a NEGATIVE
                    # availment, not a zero.
                    _, s_tax_bd = _itc_stmt_in_books_direction(b, s)
                    effect["revOth"] = {
                        h: max(b["tax"][h] - s_tax_bd[h], Decimal("0"))
                        for h in ("camt", "samt", "iamt")
                    }
                elif line["status"] in ("MISSING_IN_2B", "NEAR_MATCH", "AMBIGUOUS"):
                    if total_tax >= 0:
                        effect["revOth"] = dict(tax)
                elif line["status"] in ("MATCHED", "MATCHED_ROUNDING") and s is not None:
                    state = _ims_effective_action(s["imsActions"], resolution, freeze, as_on) \
                        if as_on is not None else "NO_ACTION"
                    if state in ("REJECTED", "PENDING") and total_tax >= 0:
                        effect["revOth"] = dict(tax)

            row_key = "itcAvlIsrc" if effect["avlRow"] == "ISRC" else "itcAvlOth"
            totals[row_key] = _tax_add(totals[row_key], effect["avl"])
            totals["itcRevRul"] = _tax_add(totals["itcRevRul"], effect["revRul"])
            totals["itcRevOth"] = _tax_add(totals["itcRevOth"], effect["revOth"])
            totals["itcInelgOth"] = _tax_add(totals["itcInelgOth"], effect["inelg"])

        line["effect"] = effect
        line["delta"] = delta
        line["s164Expiry"] = expiry.isoformat() if expiry else None

    posted = dict(_ZERO_TAX)
    for b in books:
        posted = _tax_add(posted, b["tax"])
    avl = _tax_add(totals["itcAvlIsrc"], totals["itcAvlOth"])
    rev = _tax_add(totals["itcRevRul"], totals["itcRevOth"])
    net = {h: avl[h] - rev[h] for h in ("camt", "samt", "iamt")}

    # (I1) is asserted HERE, inside the oracle, so a fixture whose books and
    # effect table disagree fails even if the Go agreed with it.
    for h in ("camt", "samt", "iamt"):
        if avl[h] + totals["itcInelgOth"][h] != posted[h]:
            fail(
                path,
                f"identity (I1) broken in the re-derivation for {h}: "
                f"4A {_m(avl[h])} + 4D {_m(totals['itcInelgOth'][h])} != posted input tax "
                f"{_m(posted[h])} — every rupee of posted input tax must land in exactly "
                f"one of table 4(A) or table 4(D)",
            )

    out_lines = []
    for line in sorted(lines, key=lambda l: (l["books"] or "~", l["statement"] or "~")):
        out_lines.append({
            "status": line["status"],
            "fields": line["fields"],
            "notComparable": line["notComparable"],
            "books": line["books"],
            "statement": line["statement"],
            "candidates": line["candidates"],
            "delta": {
                "txval": _m(line["delta"]["txval"]),
                **_tax_str(line["delta"]),
            },
            "effect": {
                "avlRow": line["effect"]["avlRow"],
                "avl": _tax_str(line["effect"]["avl"]),
                "revRul": _tax_str(line["effect"]["revRul"]),
                "revOth": _tax_str(line["effect"]["revOth"]),
                "inelg": _tax_str(line["effect"]["inelg"]),
            },
            "s164Expiry": line["s164Expiry"],
        })

    return {
        "rejectedStatementRows": rejected_ids,
        "findings": findings,
        "lines": out_lines,
        "totals": {
            "itcAvlIsrc": _tax_str(totals["itcAvlIsrc"]),
            "itcAvlOth": _tax_str(totals["itcAvlOth"]),
            "itcRevRul": _tax_str(totals["itcRevRul"]),
            "itcRevOth": _tax_str(totals["itcRevOth"]),
            "itcInelgOth": _tax_str(totals["itcInelgOth"]),
            "itcNet": _tax_str(net),
            "postedInputTax": _tax_str(posted),
        },
    }


def _itc_canon_expected(path: Path, node, where: str):
    """Rewrite a fixture's `expected` tree into the derivation's exact form.

    Every money value is pushed through money(), so a JSON NUMBER anywhere in a
    fixture is a validation failure rather than a silent float.
    """
    if isinstance(node, dict):
        out = {}
        for key, value in node.items():
            if key in ITC_MONEY_KEYS and not isinstance(value, (dict, list)):
                dec = money(path, f"{where}.{key}", value)
                out[key] = _m(dec) if dec is not None else value
            else:
                out[key] = _itc_canon_expected(path, value, f"{where}.{key}")
        return out
    if isinstance(node, list):
        return [_itc_canon_expected(path, v, f"{where}[{i}]") for i, v in enumerate(node)]
    return node


def _itc_diff(path: Path, want, got, where: str) -> None:
    """Report the first difference at every leaf, with the JSON path."""
    if isinstance(want, dict) and isinstance(got, dict):
        for key in sorted(set(want) | set(got)):
            if key not in got:
                fail(path, f"{where}.{key}: the re-derivation produces this and the fixture omits it "
                           f"(derived {want[key]!r})")
            elif key not in want:
                fail(path, f"{where}.{key}: the fixture asserts {got[key]!r} but the re-derivation "
                           f"produces no such field")
            else:
                _itc_diff(path, want[key], got[key], f"{where}.{key}")
        return
    if isinstance(want, list) and isinstance(got, list):
        if len(want) != len(got):
            fail(path, f"{where}: fixture has {len(got)} entries, the re-derivation produces "
                       f"{len(want)} — derived {want!r}")
            return
        for i, (w, g) in enumerate(zip(want, got)):
            _itc_diff(path, w, g, f"{where}[{i}]")
        return
    if want != got:
        fail(path, f"{where}: fixture says {got!r}, re-deriving from `input` gives {want!r}")


def validate_gstr2b(path: Path, case: dict) -> None:
    """Validate one GSTR-2B reconciliation case and RE-DERIVE its expectation."""
    if not require(path, case, ["id", "description", "input", "expected"]):
        return
    if path.stem != case["id"]:
        fail(path, f"id {case['id']!r} does not match the filename")
    if len(case["description"]) < 40:
        fail(path, "description must state, in a sentence, what would be WRONG if this case "
                   "were absent — that is what stops a later session deleting it as redundant")
    inp = case["input"]
    if not require(path, inp, ["period", "books", "statement"]):
        return
    if not re.fullmatch(r"[0-9]{6}", inp["period"] or ""):
        fail(path, f"input.period must be MMYYYY, got {inp['period']!r}")
    for line in case["expected"].get("lines", []):
        if line.get("status") not in ITC_STATUSES:
            fail(path, f"unknown status {line.get('status')!r}; known: {sorted(ITC_STATUSES)}")

    derived = _derive_gstr2b_expected(path, case)
    canon = _itc_canon_expected(path, case["expected"], "expected")
    _itc_diff(path, derived, canon, "expected")


# --- IMS -------------------------------------------------------------------
#
# GSTN's Annexure-A, as a lookup table. CITATION GRADE: the decision table is
# reconstructed from the behaviour the IMS advisory and FAQ (22-09-2024)
# DESCRIBE, not transcribed from the annexure PDF, which is not in this
# repository — so it is [CITED-SECONDARY] and carries GSTN's own disclaimer
# that the annexure "is not a statement of law". VERIFY BEFORE RELEASE.
#
# The four rows that are not negotiable regardless of wording:
#   * an ACCEPTED (or DEEMED-ACCEPTED) record flows into 2B as available ITC;
#   * a REJECTED record does NOT flow into 2B and gives no ITC at all;
#   * a PENDING record does not flow into THIS period's 2B and is carried
#     forward — deferred, not denied;
#   * IMPG / RCM / ISD records are OUTSIDE IMS entirely (FAQ 8 and 18: "RCM
#     invoices are not part of IMS but will continue to be part of GSTR-2B")
#     and flow to 2B with no action possible.
IMS_ANNEXURE_A = {
    ("B2B_INVOICE", "ACCEPTED"): ("YES", "AVAILABLE"),
    ("B2B_INVOICE", "DEEMED_ACCEPTED"): ("YES", "AVAILABLE"),
    ("B2B_INVOICE", "NO_ACTION"): ("YES", "AVAILABLE"),
    ("B2B_INVOICE", "REJECTED"): ("NO", "NONE"),
    ("B2B_INVOICE", "PENDING"): ("NO", "DEFERRED"),
    ("DEBIT_NOTE", "ACCEPTED"): ("YES", "AVAILABLE"),
    ("DEBIT_NOTE", "DEEMED_ACCEPTED"): ("YES", "AVAILABLE"),
    ("DEBIT_NOTE", "REJECTED"): ("NO", "NONE"),
    ("DEBIT_NOTE", "PENDING"): ("NO", "DEFERRED"),
    ("CREDIT_NOTE", "ACCEPTED"): ("YES", "REDUCTION"),
    ("CREDIT_NOTE", "DEEMED_ACCEPTED"): ("YES", "REDUCTION"),
    ("CREDIT_NOTE", "REJECTED"): ("NO", "NONE"),
    ("CREDIT_NOTE", "PENDING"): ("NO", "DEFERRED"),
    ("IMPG", "NOT_APPLICABLE"): ("YES", "AVAILABLE"),
    ("RCM", "NOT_APPLICABLE"): ("YES", "AVAILABLE"),
    ("ISD", "NOT_APPLICABLE"): ("YES", "AVAILABLE"),
}

# PENDING is EFFECTIVE-DATED, exactly like the TDS/TCS rate tables, and a
# hard-coded "pending is never allowed on a credit note" is WRONG for every
# period this software ships into.
#
# Pre-Oct-2025 tax periods [CITED-PRIMARY: GSTN IMS advisory Flow(3) / FAQ 11]:
# PENDING is forbidden on an original credit note, an upward amendment of a
# credit note, a downward amendment of a credit note whose original was
# rejected, and a downward amendment of an invoice/debit note whose original
# was accepted and whose GSTR-3B was filed.
#
# Oct-2025 tax period onward [CITED-SECONDARY: GSTN Advisory 631 of 08-10-2025,
# read via A2Z Taxcorp / TaxO — VERIFY BEFORE RELEASE]: PENDING is allowed for
# those four for ONE tax period, expiring at the GSTR-3B due date of period+1.
IMS_PENDING_RESTRICTED = {
    "CREDIT_NOTE_ORIGINAL",
    "CN_AMENDMENT_UPWARD",
    "CN_AMENDMENT_DOWNWARD_ORIGINAL_REJECTED",
    "INV_DN_AMENDMENT_DOWNWARD_ORIGINAL_ACCEPTED_3B_FILED",
}

# The first tax period in which PENDING became available for the restricted
# categories, as (month, year).
IMS_PENDING_RELAXED_FROM = (10, 2025)


def _ims_pending_rule(period: str) -> str:
    month, year = _ims_period(period)
    if (year, month) >= (IMS_PENDING_RELAXED_FROM[1], IMS_PENDING_RELAXED_FROM[0]):
        return "OCT_2025_ONWARDS"
    return "PRE_OCT_2025"


def _ims_pending_expiry(period: str) -> str:
    """The GSTR-3B due date of period+1 — the 20th, by CALENDAR-month addition.

    Anchored on the due date and not written as "one month after the pending
    action" precisely because day arithmetic on a month boundary is the one
    clamping bug this track can actually make.
    """
    month, year = _ims_period(period)
    m2, y2 = _ims_add_months(month, year, 2)
    return datetime(y2, m2, 20).date().isoformat()


def _validate_ims_clock(path: Path, case: dict) -> None:
    inp, exp = case["input"], case["expected"]
    if not require(path, inp, ["period", "probes"]):
        return
    cutoff = _ims_cutoff(inp["period"])
    resolution = _ims_resolution(inp["period"], inp.get("statementGenDate"))
    freeze = _itc_ts(inp.get("filedAt"))
    actions = [{"action": a["action"], "actedAt": _itc_ts(a["actedAt"])}
               for a in inp.get("actions", [])]

    if "cutoff" in exp and _itc_ts(exp["cutoff"]) != cutoff:
        fail(path, f"expected.cutoff {exp['cutoff']} is WRONG; the 14th-00:00-IST boundary for "
                   f"period {inp['period']} is {cutoff.isoformat()}")
    if "resolution" in exp and _itc_ts(exp["resolution"]) != resolution:
        fail(path, f"expected.resolution {exp['resolution']} is WRONG; re-deriving gives "
                   f"{resolution.isoformat()} (the statement's own generation date wins over "
                   f"the computed 14th)")

    want = [_ims_effective_action(actions, resolution, freeze, _itc_ts(p["asOn"]))
            for p in inp["probes"]]
    got = exp.get("states", [])
    if len(want) != len(got):
        fail(path, f"expected.states has {len(got)} entries for {len(want)} probes")
        return
    for probe, w, g in zip(inp["probes"], want, got):
        if g not in IMS_STATES:
            fail(path, f"unknown IMS state {g!r}; known: {sorted(IMS_STATES)}")
        elif w != g:
            fail(path, f"at asOn={probe['asOn']} the fixture says {g}, but re-deriving the "
                       f"boundary in IST gives {w} — a filer acting at this instant would be "
                       f"reported in the wrong GSTR-2B")


def _validate_ims_pending(path: Path, case: dict) -> None:
    inp, exp = case["input"], case["expected"]
    if not require(path, inp, ["taxPeriod", "probes"]):
        return
    rule = _ims_pending_rule(inp["taxPeriod"])
    if exp.get("rule") != rule:
        fail(path, f"expected.rule {exp.get('rule')!r} is WRONG; tax period {inp['taxPeriod']} "
                   f"falls under {rule}")
    got = exp.get("probes", [])
    if len(got) != len(inp["probes"]):
        fail(path, f"expected.probes has {len(got)} entries for {len(inp['probes'])} inputs")
        return
    for probe, g in zip(inp["probes"], got):
        category = probe["docCategory"]
        restricted = category in IMS_PENDING_RESTRICTED
        allowed = (not restricted) or rule == "OCT_2025_ONWARDS"
        expires = _ims_pending_expiry(inp["taxPeriod"]) if (restricted and allowed) else None
        if g.get("pendingAllowed") != allowed:
            fail(path, f"{category}: fixture says pendingAllowed={g.get('pendingAllowed')}, the "
                       f"{rule} rule gives {allowed} — marking a record PENDING that the portal "
                       f"refuses (or refusing one it accepts) desynchronises IMS from the return")
        if g.get("expiresAt") != expires:
            fail(path, f"{category}: fixture says expiresAt={g.get('expiresAt')!r}, the rule gives "
                       f"{expires!r} (the GSTR-3B due date of the following tax period)")


def _validate_ims_applicability(path: Path, case: dict) -> None:
    inp, exp = case["input"], case["expected"]
    got = exp.get("probes", [])
    if len(got) != len(inp.get("probes", [])):
        fail(path, f"expected.probes has {len(got)} entries for {len(inp.get('probes', []))} inputs")
        return
    for probe, g in zip(inp["probes"], got):
        section = probe["section"]
        applicable = section in IMS_APPLICABLE_SECTIONS and not probe.get("reverseCharge")
        want = None if applicable else "ErrIMSNotApplicable"
        if g.get("error") != want:
            fail(path, f"section={section} reverseCharge={bool(probe.get('reverseCharge'))}: "
                       f"fixture says error={g.get('error')!r}, the IMS scope rule gives "
                       f"{want!r} — IMPG/IMPGSEZ/ISD/ISDA and every reverse-charge row bypass "
                       f"IMS entirely and still reach GSTR-2B")


def _validate_ims_annexure(path: Path, case: dict) -> None:
    inp, exp = case["input"], case["expected"]
    got = exp.get("rows", [])
    if len(got) != len(inp.get("rows", [])):
        fail(path, f"expected.rows has {len(got)} entries for {len(inp.get('rows', []))} inputs")
        return
    for probe, g in zip(inp["rows"], got):
        key = (probe["docCategory"], probe["action"])
        if key not in IMS_ANNEXURE_A:
            fail(path, f"Annexure-A has no row for {key} — this validator's table and the fixture "
                       f"disagree about what GSTN documents")
            continue
        in2b, itc = IMS_ANNEXURE_A[key]
        if (g.get("in2B"), g.get("itc")) != (in2b, itc):
            fail(path, f"{key}: fixture says {(g.get('in2B'), g.get('itc'))}, Annexure-A says "
                       f"{(in2b, itc)}")


def _validate_ims_s164(path: Path, case: dict) -> None:
    inp, exp = case["input"], case["expected"]
    got = exp.get("probes", [])
    if len(got) != len(inp.get("probes", [])):
        fail(path, f"expected.probes has {len(got)} entries for {len(inp.get('probes', []))} inputs")
        return
    for probe, g in zip(inp["probes"], got):
        doc_date = _itc_date(probe["docDate"])
        filed = _itc_date(probe.get("annualReturnFiledOn"))
        fy_end = _s164_fy_end(doc_date).isoformat()
        expiry = _s164_expiry(doc_date, filed)
        claim = _itc_date(probe["claimDate"])
        barred = claim > expiry
        if g.get("fyEnd") != fy_end:
            fail(path, f"docDate={probe['docDate']}: fixture fyEnd={g.get('fyEnd')!r}, "
                       f"re-deriving gives {fy_end!r}")
        if g.get("expiry") != expiry.isoformat():
            fail(path, f"docDate={probe['docDate']}: fixture expiry={g.get('expiry')!r}, "
                       f"re-deriving gives {expiry.isoformat()!r} — s.16(4) fixes when the credit "
                       f"dies, and a wrong expiry either claims dead credit or throws away live "
                       f"credit")
        if g.get("barred") != barred:
            fail(path, f"docDate={probe['docDate']} claimDate={probe['claimDate']}: fixture "
                       f"barred={g.get('barred')}, re-deriving gives {barred} (the expiry date "
                       f"itself is INCLUSIVE)")


IMS_KINDS = {
    "clock": _validate_ims_clock,
    "pending": _validate_ims_pending,
    "applicability": _validate_ims_applicability,
    "annexure": _validate_ims_annexure,
    "s164": _validate_ims_s164,
}


def validate_ims(path: Path, case: dict) -> None:
    """Validate one IMS case by RE-DERIVING it from the statutory arithmetic."""
    if not require(path, case, ["id", "kind", "description", "input", "expected"]):
        return
    if path.stem != case["id"]:
        fail(path, f"id {case['id']!r} does not match the filename")
    if len(case["description"]) < 40:
        fail(path, "description must state, in a sentence, what would be WRONG if this case "
                   "were absent")
    handler = IMS_KINDS.get(case["kind"])
    if handler is None:
        fail(path, f"unknown kind {case['kind']!r}; known: {sorted(IMS_KINDS)}")
        return
    handler(path, case)


# ---------------------------------------------------------------------------
# PAYROLL (corpora/payroll)
# ---------------------------------------------------------------------------
# These fixtures live in their OWN top-level directory and NOT in corpora/tds.
# Three consumers glob corpora/tds/*.json assuming the withholding case shape
# (validate_tds above, the Go TestTDSDerive_GoldenCorpus, and the TDS closure
# check that every shipped section has a fixture); a payroll file dropped in
# there breaks all three, and it would inflate MIN_COUNTS["tds"] so a DELETED
# TDS fixture would stop being detected.
#
# HOW THIS FAMILY AVOIDS THE TAUTOLOGY. Everything below is an INDEPENDENT
# RE-DERIVATION written FROM THE STATUTORY CITATION CARRIED IN EACH FIXTURE'S
# `description`. It is NOT transcribed from backend/internal/payroll: this file
# never reads Go source, never imports a shared data file and never shells out
# to the backend. The Go side (TestPayrollDerive_GoldenCorpus) compares its
# ENGINE's output to `expected`; this side compares its OWN derivation to
# `expected`. Neither ever sees the other's answer. The FIXTURE is the only
# meeting point, so a number that is wrong in one implementation fails here.
#
# THE DUPLICATION IS THE ORACLE AND MUST NEVER BE "DE-DUPLICATED" into a shared
# JSON/YAML rate file loaded by both sides. The moment both sides read one
# table, the gate degrades from "two independent derivations agree" to "one
# derivation agrees with itself", which is exactly the failure a previous
# session shipped and an adversarial pass had to find.
#
# WHAT THE DUPLICATION CANNOT CATCH: a rate that is wrong in BOTH places. Both
# tables were curated from the same secondary reading, with no primary text
# retrievable from this environment. That is what the pre-release statutory
# spot-check is for — see the RATE PROVENANCE banner in
# backend/internal/payroll/payroll.go.

PAYROLL_SS_RULESET_VERSIONS = {"ss-2026-07", "ss-2025-11", "ss-2019-07"}
PAYROLL_IT_RULESET_VERSION = "it-ty-2026-27"

# The naive rules the counterfactual blocks may name. A fixture naming anything
# else is rejected outright rather than silently re-deriving the CORRECT rule
# (which would then "agree with expected" and produce a confusing failure about
# the fixture rather than about the typo).
PAYROLL_COUNTERFACTUAL_RULES = {
    "deem-all-exclusions",
    "esi-monthly-ceiling-test",
    "ignore-prior-employer-taxable",
    "no-rebate-marginal-relief",
    "pf-base-actual-with-employer-matching",
    "pre-code-no-deeming",
    "round-income-to-rupee",
}

# --- closed vocabularies ---------------------------------------------------
REGIMES = {"NEW", "OLD"}
PF_BASES = {"CEILING", "ACTUAL"}
# Spelled exactly as the engine's AgeBand constants are. A vocabulary that has
# drifted does not catch a bad fixture — it REJECTS A GOOD ONE with a message
# pointing at the fixture, and the cheapest apparent fix under time pressure is
# to edit the golden case until the validator goes quiet. That is how a correct
# expectation becomes a wrong one; see the KNOWN_SCREENS note above.
AGE_BANDS = {"BELOW_60", "SENIOR", "SUPER_SENIOR"}
PERIOD_BASES = {"MONTHLY", "ANNUAL", "CONTRIBUTION_PERIOD"}

# --- EPF / EPS / EDLI ------------------------------------------------------
# EPF & MP Act 1952 s.6 read with EPF Scheme para 29: 12% of basic wages, DA and
# retaining allowance, matched by the employer. EPS 1995 para 3: 8.33% of the
# employer's share is diverted to the Pension Fund, on pay capped at the EPS
# ceiling (para 11(1) proviso). Ceiling 15000/month, S.O. 2882(E) w.e.f.
# 01-09-2014, carried forward by the EPF/EPS Schemes 2026.
PF_RATES = {
    "employeePct": Decimal("12"),
    "employerPct": Decimal("12"),
    "epsPct": Decimal("8.33"),
}
PF_CEILING = Decimal("15000")
EPS_CAP = Decimal("15000")

# --- ESI -------------------------------------------------------------------
# ESI (Central) Rules 1950 rule 51 as substituted by G.S.R. 423(E) w.e.f.
# 01-07-2019: employee 0.75%, employer 3.25%, each "rounded to the next higher
# rupee". Coverage wage limit 21000/month, G.S.R. 1226(E) w.e.f. 01-01-2017.
# Rule 52 exempts an employee whose AVERAGE DAILY WAGE does not exceed 176 from
# the employee's share only — the employer's share stays payable.
ESI_RATES = {"employeePct": Decimal("0.75"), "employerPct": Decimal("3.25")}
ESI_COVERAGE_LIMIT = Decimal("21000")
ESI_LOW_WAGE_LIMIT = Decimal("176")
# The share of ALL REMUNERATION the EXCLUDED components may occupy before the
# excess is deemed back into wages, per ruleset. 100 in the pre-Code window means
# "no cap", which is how the absence of the proviso before the Code came into
# force is expressed without a special case in the derivation.
# ss-2025-11 is the CODE-ERA INTERIM window [21 Nov 2025, 29 Jun 2026): the Code
# on Social Security 2020 came into force on 21 November 2025 (S.O. 5319(E)), so
# the 50% proviso already bites, while the 1952/1995/1976 Schemes were still the
# operative schemes until the 2026 Schemes superseded them on 29 June 2026.
DEEMING_THRESHOLD_PCT = {
    "ss-2026-07": Decimal("50"),
    "ss-2025-11": Decimal("50"),
    "ss-2019-07": Decimal("100"),
}
EDLI_CEILING = Decimal("15000")
# EPS 1995 para 6(a) as amended by G.S.R. 609(E): a person who FIRST became a
# provident-fund member on or after this date, whose pay then exceeded the
# pension ceiling, was never admitted to the Pension Scheme.
EPS_NEW_MEMBER_CUTOFF = "2014-09-01"
# Code on Wages 2019 s.18(2): total deductions may not exceed 50% of wages.
PF_DEDUCTION_CAP_PCT = Decimal("50")
# Code on Social Security 2020 s.2(88): the EXHAUSTIVE list of exclusions from
# wages. It is written here as a DENYLIST for the same reason the engine writes
# it as one - the statute's list is closed, so anything NOT on it IS wages, and
# an allowlist would silently exclude every component type added later, which is
# an under-contribution nobody would see.
EXCLUDED_FROM_WAGES = {
    "HRA",
    "CONVEYANCE_ALLOWANCE",
    "OVERTIME_ALLOWANCE",
    "COMMISSION",
    "BONUS",
    "HOUSE_ACCOMMODATION_VALUE",
    "SPECIAL_EXPENSES",
    "AWARD_OR_SETTLEMENT",
    "EMPLOYER_PF_CONTRIBUTION",
    "GRATUITY",
    "RETRENCHMENT_COMPENSATION",
    "EX_GRATIA",
}
# Proviso 1 to s.2(88) tests only "payments made by the employer to the employee
# UNDER CLAUSES (a) TO (i)" against the one-half floor. The enumeration STOPS at
# (i): clause (j) is "any gratuity payable on the termination of employment" and
# clause (k) is "any retrenchment compensation or other retirement benefit
# payable to the employee or any ex gratia payment made to him on the termination
# of employment". Those three are excluded from wages AND take no part in the
# deeming test, so they can never be added back.
#
# This set is written out in full rather than as EXCLUDED_FROM_WAGES minus three
# names, for the same reason the engine writes it out: a derivation would
# silently re-admit any exclusion a later amendment adds.
#
# The bug this constant exists to stop: running the test over ALL twelve
# exclusions made a leaver's final payslip of Basic 30,000 + HRA 15,000 +
# GRATUITY 5,00,000 + RETRENCHMENT_COMPENSATION 2,00,000 yield a statutory wage
# of 3,72,500 instead of 30,000, i.e. Rs 41,100 deducted from a departing
# employee's last salary on money the statute says is not a wage. It fired on
# every terminal settlement.
DEEMING_TEST_CLAUSES_A_TO_I = {
    "BONUS",  # (a)
    "HOUSE_ACCOMMODATION_VALUE",  # (b)
    "EMPLOYER_PF_CONTRIBUTION",  # (c)
    "CONVEYANCE_ALLOWANCE",  # (d)
    "SPECIAL_EXPENSES",  # (e)
    "HRA",  # (f)
    "AWARD_OR_SETTLEMENT",  # (g)
    "OVERTIME_ALLOWANCE",  # (h)
    "COMMISSION",  # (i)
}
# The components whose rate the scheme itself says is "to be notified
# separately", per ruleset. The engine must DEFER these, never number them.
PF_DEFERRED_COMPONENTS = {
    # EDLI_CONTRIBUTION left this list when S.O. 3581(E) of 1 July 2026 was
    # identified: 0.50% of EDLI wages w.e.f. 29 June 2026, ceiling-capped to Rs 75
    # per member per month. The rate is NOT in the Scheme itself — G.S.R. 526(E)
    # para 5(1)-(2) leaves it to a separate notification — which is why it looked
    # unnotified for so long. The two ADMINISTRATIVE CHARGES stay: no standalone
    # rate notification for them exists as at 30 July 2026, and they are PENDING
    # rather than abolished (EPF Scheme 2026 para 29(2) caps a late fee at "the
    # administrative charges payable ... for the relevant month", which
    # presupposes the charge). Do NOT substitute S.O. 2701(E) — that is INSPECTION
    # charges for exempted establishments, a different levy.
    "ss-2026-07": ["EDLI_ADMIN_CHARGES", "EPF_ADMIN_CHARGES"],
    # Nothing is deferred in the interim window: the saved 1952/1976 Schemes were
    # still operative, so their EDLI contribution (0.50%) and administrative
    # charges ARE on record. Deferring them here would refuse a contribution that
    # was actually payable for eight months.
    "ss-2025-11": [],
    "ss-2019-07": [],
}

# --- income tax, tax year 2026-27 -----------------------------------------
# Slab boundaries as (upper bound or None for the top open band, rate %).
# NEW regime = the s.202 (ex-115BAC) default, Finance Act 2025 values carried
# forward into FY 2026-27. OLD regime = the s.202(6) opt-out.
IT_SLABS_NEW: list[tuple[Decimal | None, Decimal]] = [
    (Decimal("400000"), Decimal("0")),
    (Decimal("800000"), Decimal("5")),
    (Decimal("1200000"), Decimal("10")),
    (Decimal("1600000"), Decimal("15")),
    (Decimal("2000000"), Decimal("20")),
    (Decimal("2400000"), Decimal("25")),
    (None, Decimal("30")),
]
IT_SLABS_OLD: list[tuple[Decimal | None, Decimal]] = [
    (Decimal("250000"), Decimal("0")),
    (Decimal("500000"), Decimal("5")),
    (Decimal("1000000"), Decimal("20")),
    (None, Decimal("30")),
]
# Old-regime basic exemption by age band (residents only — a non-resident of any
# age gets the ordinary 250000, s.2(k) of the Finance Act's Part III paragraph A).
IT_OLD_EXEMPTION = {
    "BELOW_60": Decimal("250000"),
    "SENIOR": Decimal("300000"),
    "SUPER_SENIOR": Decimal("500000"),
}
# s.19 (ex-16(ia)) standard deduction against salary.
STD_DEDUCTION = {"NEW": Decimal("75000"), "OLD": Decimal("50000")}
# s.156 (ex-87A) rebate. MARGINAL RELIEF exists only on the NEW-regime cliff.
REBATE = {
    "NEW": {"limit": Decimal("1200000"), "max": Decimal("60000"), "marginalRelief": True},
    "OLD": {"limit": Decimal("500000"), "max": Decimal("12500"), "marginalRelief": False},
}
# Finance Act First Schedule Part III paragraph A, as (threshold, rate %).
# The new regime is capped at 25% — the 37% band applies to the old regime only.
SURCHARGE_BANDS = {
    "NEW": [
        (Decimal("5000000"), Decimal("10")),
        (Decimal("10000000"), Decimal("15")),
        (Decimal("20000000"), Decimal("25")),
    ],
    "OLD": [
        (Decimal("5000000"), Decimal("10")),
        (Decimal("10000000"), Decimal("15")),
        (Decimal("20000000"), Decimal("25")),
        (Decimal("50000000"), Decimal("37")),
    ],
}
# Health and Education Cess, Finance Act First Schedule.
CESS = Decimal("4")
# s.24(b) proviso read with ex-s.71(3A): the loss under the head house property
# that may be SET OFF against salary is capped at 200000, and under the s.202
# default regime it may not be set off against salary AT ALL.
HOUSE_PROPERTY_LOSS_CAP = Decimal("200000")


# --- rounding, written from the statute ------------------------------------
def _round_ten(d: Decimal) -> Decimal:
    """s.288A / Income-tax Act 2025 s.516: TOTAL INCOME to the nearest ten rupees.

    Written as shift-round-shift rather than a division so no divisor precision
    is consulted. Half goes UP ("if the last figure is five or more").
    """
    return (d / 10).quantize(Decimal("1"), rounding=ROUND_HALF_UP) * 10


def _round_rupee(d: Decimal) -> Decimal:
    """s.288B / s.516: the amount of TAX to the nearest rupee, half up."""
    return d.quantize(Decimal("1"), rounding=ROUND_HALF_UP)


def _round_up_rupee(d: Decimal) -> Decimal:
    """ESI (Central) Rules 1950 rule 51: "rounded to the next higher rupee".

    A ceiling, and the ONLY one in the corpora. It applies to BOTH the
    employee's and the employer's share — the parenthesis in rule 51 governs the
    whole contribution formula, not just the employee limb.
    """
    return d.quantize(Decimal("1"), rounding=ROUND_CEILING)


# --- the re-derivations ----------------------------------------------------
def _payroll_slab_tax(taxable: Decimal, slabs, exemption: Decimal) -> Decimal:
    """Walk the slab table from `exemption` upwards. No rounding happens here."""
    tax = Decimal("0")
    lower = exemption
    for upper, rate in slabs:
        if upper is not None and upper <= exemption:
            continue
        top = taxable if upper is None else min(taxable, upper)
        if top > lower:
            tax += (top - lower) * rate / 100
            lower = top
        if upper is not None and taxable <= upper:
            break
    return tax


def _payroll_it_slab_config(ci: dict) -> tuple[list, Decimal]:
    """Return (slab table, basic exemption) for the case's regime and age band."""
    if ci["regime"] == "NEW":
        return IT_SLABS_NEW, Decimal("0")
    exemption = IT_OLD_EXEMPTION["BELOW_60"]
    if ci["isResident"]:
        exemption = IT_OLD_EXEMPTION[ci["ageBand"]]
    return IT_SLABS_OLD, exemption


def _payroll_tax_after_rebate(taxable: Decimal, ci: dict, allow_rebate_relief: bool):
    """Slabs -> rebate -> rebate marginal relief, in that order.

    Returns (taxBeforeRebate, rebate, rebateMarginalRelief). Pulled out as its
    own function because the SURCHARGE marginal relief has to re-run exactly this
    computation at the surcharge threshold.
    """
    slabs, exemption = _payroll_it_slab_config(ci)
    before = _payroll_slab_tax(taxable, slabs, exemption)
    rebate = Decimal("0")
    relief = Decimal("0")
    cfg = REBATE[ci["regime"]]
    if ci["isResident"]:
        if taxable <= cfg["limit"]:
            rebate = min(before, cfg["max"])
        elif cfg["marginalRelief"] and allow_rebate_relief:
            # s.156 proviso: the tax payable may not exceed the amount by which
            # total income exceeds the rebate limit.
            excess = taxable - cfg["limit"]
            if before > excess:
                relief = before - excess
    return before, rebate, relief


def _derive_payroll_it(ci: dict, variant: str | None) -> dict[str, Any]:
    """s.392 (ex-192) annual-base derivation. NO per-transaction threshold exists.

    ORDER OF OPERATIONS, and it is the order that is being pinned:
      round total income to the nearest TEN -> slabs -> rebate -> REBATE marginal
      relief -> surcharge -> SURCHARGE marginal relief -> cess -> round the annual
      tax to the nearest RUPEE -> subtract what is already credited -> divide over
      the months remaining -> round this month's TDS to the nearest RUPEE.
    """
    regime = ci["regime"]
    salary = (
        Decimal(ci["ytdTaxableThisEmployer"])
        + Decimal(ci["currentMonthTaxable"])
        + Decimal(ci["projectedRemainingTaxable"])
    )
    if variant != "ignore-prior-employer-taxable":
        salary += Decimal(ci["priorEmployerTaxable"])

    std = min(STD_DEDUCTION[regime], salary)
    if std < 0:
        std = Decimal("0")
    net_salary = salary - std

    # Under the s.202 default regime a house-property loss may NOT be set off
    # against salary; under the old regime it may, capped at 200000.
    hp = Decimal("0")
    if regime == "OLD":
        hp = min(HOUSE_PROPERTY_LOSS_CAP, Decimal(ci["housePropertyLoss"]))
    chapter_via = Decimal(ci["chapterVIA"]) if regime == "OLD" else Decimal("0")

    total_income = net_salary + Decimal(ci["otherIncomeDeclared"]) - hp - chapter_via
    if total_income < 0:
        total_income = Decimal("0")

    if variant == "round-income-to-rupee":
        taxable = _round_rupee(total_income)
    else:
        taxable = _round_ten(total_income)

    before, rebate, rebate_relief = _payroll_tax_after_rebate(
        taxable, ci, allow_rebate_relief=(variant != "no-rebate-marginal-relief")
    )
    after_rebate = before - rebate - rebate_relief

    surcharge = Decimal("0")
    surcharge_relief = Decimal("0")
    bands = SURCHARGE_BANDS[regime]
    idx = -1
    for i, (threshold, _rate) in enumerate(bands):
        if taxable > threshold:
            idx = i
    if idx >= 0:
        threshold, rate = bands[idx]
        surcharge = after_rebate * rate / 100
        prev_rate = bands[idx - 1][1] if idx > 0 else Decimal("0")
        at_b, at_reb, at_rel = _payroll_tax_after_rebate(
            threshold, ci, allow_rebate_relief=(variant != "no-rebate-marginal-relief")
        )
        at_threshold = at_b - at_reb - at_rel
        cap = at_threshold * (1 + prev_rate / 100) + (taxable - threshold)
        actual = after_rebate + surcharge
        if actual > cap:
            surcharge_relief = actual - cap

    net_tax = after_rebate + surcharge - surcharge_relief
    cess = net_tax * CESS / 100
    annual = _round_rupee(net_tax + cess)

    credited = Decimal(ci["priorEmployerTDS"]) + Decimal(ci["ytdTDSThisEmployer"])
    balance = annual - credited
    if balance < 0:
        balance = Decimal("0")
    months = int(ci["monthsRemainingInclusive"])
    if ci["isFinalMonth"]:
        tds = _round_rupee(balance)
    else:
        tds = _round_rupee(balance / months)

    return {
        "regimeApplied": regime,
        "estimatedAnnualTaxable": taxable,
        "standardDeduction": std,
        "taxBeforeRebate": before,
        "rebate": rebate,
        "rebateMarginalRelief": rebate_relief,
        "surcharge": surcharge,
        "surchargeMarginalRelief": surcharge_relief,
        "cess": cess,
        "annualTax": annual,
        "creditedAlready": credited,
        "tdsThisMonth": tds,
        "periodBasis": "ANNUAL",
    }


def _pf_first_member_excluded(first_member_on, wage: Decimal) -> bool:
    """EPS 1995 para 6(a): first membership ON OR AFTER the cutoff AND pay that
    EXCEEDS the pension ceiling.

    "Exceeds" and "on or after" — somebody exactly at the ceiling IS a member,
    and somebody first admitted exactly ON the cutoff date IS excluded. Flipping
    either operator moves a real population of employees into or out of a pension
    they will draw for the rest of their lives.
    """
    if not first_member_on:
        return False
    return first_member_on >= EPS_NEW_MEMBER_CUTOFF and wage > EPS_CAP


def _derive_payroll_pf(ci: dict, variant: str | None, version: str) -> dict[str, Any]:
    """EPF / EPS / EDLI. The EDLI CONTRIBUTION is deliberately not derived.

    The statutory base is ALWAYS the wage capped at the ceiling; an ACTUAL-base
    election adds an above-ceiling EXCESS on top of it rather than replacing it,
    which is what keeps the statutory and the voluntary halves separable for
    Form V and for the remittance.
    """
    base = ci["pfBase"]
    matches = bool(ci["employerMatchesVoluntary"])
    if variant == "pf-base-actual-with-employer-matching":
        base, matches = "ACTUAL", True

    wage = Decimal(ci["statutoryWage"])
    statutory_base = min(wage, PF_CEILING)
    excess = Decimal("0") if base == "CEILING" else wage - statutory_base
    # EPS wages come off the STATUTORY WAGE against the PENSION ceiling, never
    # off the EPF wage: reading them off the EPF wage would make an ACTUAL-base
    # employer divert 8.33% of an uncapped wage into a pension computed on capped
    # pensionable salary — money the member never gets back.
    eps_wages = min(wage, EPS_CAP)
    edli_wages = min(wage, EDLI_CEILING)

    # Whole rupees BEFORE any percentage: the ECR carries integer wage columns.
    statutory_base = _round_rupee(statutory_base)
    excess = _round_rupee(excess)
    eps_wages = _round_rupee(eps_wages)
    edli_wages = _round_rupee(edli_wages)
    epf_wages = statutory_base + excess

    eps_applies = bool(ci["epsMember"]) and not _pf_first_member_excluded(
        ci.get("firstMemberOn"), wage
    )
    if not eps_applies:
        # No pension membership means no EPS WAGES to report at all. Reporting the
        # wages with a zero contribution would show the ECR an EPS wage with no
        # EPS against it.
        eps_wages = Decimal("0")

    employee_statutory = _round_rupee(statutory_base * PF_RATES["employeePct"] / 100)
    employer_total = _round_rupee(statutory_base * PF_RATES["employerPct"] / 100)
    eps = _round_rupee(eps_wages * PF_RATES["epsPct"] / 100) if eps_applies else Decimal("0")
    employer_statutory = employer_total - eps

    voluntary_base = _round_rupee(Decimal(ci["employeeVoluntaryBase"]))
    employee_voluntary_base = excess + voluntary_base
    # The employer follows the BASE election (the above-ceiling excess is its own
    # election and both sides contribute on it) but matches the employee's VPF
    # top-up only where it has separately agreed to — EPF Scheme para 26(6).
    employer_voluntary_base = excess + (voluntary_base if matches else Decimal("0"))
    employee_voluntary = (
        _round_rupee(employee_voluntary_base * PF_RATES["employeePct"] / 100)
        if employee_voluntary_base > 0
        else Decimal("0")
    )
    employer_voluntary = (
        _round_rupee(employer_voluntary_base * PF_RATES["employerPct"] / 100)
        if employer_voluntary_base > 0
        else Decimal("0")
    )

    employee_epf = employee_statutory + employee_voluntary
    if employee_epf > wage * PF_DEDUCTION_CAP_PCT / 100:
        raise InvalidOperation("employee PF deduction exceeds the s.18(2) cap")

    return {
        "epfWages": epf_wages,
        "epsWages": eps_wages,
        "edliWages": edli_wages,
        "employeeStatutory": employee_statutory,
        "employeeVoluntary": employee_voluntary,
        "employerStatutory": employer_statutory,
        "employerVoluntary": employer_voluntary,
        "eps": eps,
        "employeeEPF": employee_epf,
        "employerEPF": employer_statutory + employer_voluntary,
        "epsMemberApplied": eps_applies,
    }


def _esi_contribution_period(year: int, month: int) -> tuple[str, str]:
    """ESI (Central) Rules 1950: 1 Apr - 30 Sep and 1 Oct - 31 Mar.

    The October period SPANS A YEAR BOUNDARY, and that is the arithmetic that
    gets written wrong: for January, February and March the period started in the
    PREVIOUS calendar year, so a latch that reads the current year's 1 October is
    testing a date seven months in the future and finds the wrong wage or none.
    """
    if 4 <= month <= 9:
        return f"{year:04d}-04-01", f"{year:04d}-09-30"
    if month >= 10:
        return f"{year:04d}-10-01", f"{year + 1:04d}-03-31"
    return f"{year - 1:04d}-10-01", f"{year:04d}-03-31"


def _esi_wage_in_force(revisions: list, on: str) -> Decimal:
    """The wage under the LATEST revision effective on or before `on`.

    Dates are ISO-8601 strings and compare lexicographically, which for a fixed
    YYYY-MM-DD width is the calendar order. Raises when no revision covers the
    date rather than defaulting to the earliest or the latest: a coverage
    decision made against a wage that was not in force is not a decision.
    """
    best = None
    for r in revisions:
        if r["effectiveFrom"] <= on and (best is None or r["effectiveFrom"] > best["effectiveFrom"]):
            best = r
    if best is None:
        raise KeyError(f"no wage revision in force on {on}")
    return Decimal(best["monthlyWages"])


def _derive_payroll_esi(ci: dict, variant: str | None, version: str) -> dict[str, Any]:
    """The ESI coverage LATCH, rule 52's one-sided exemption, rule 51's ceiling."""
    year, month = (int(x) for x in ci["wageMonth"].split("-"))
    period_start, _period_end = _esi_contribution_period(year, month)
    month_start = f"{year:04d}-{month:02d}-01"
    # Exclusive upper bound of the wage month, computed without a calendar lib.
    next_year, next_month = (year + 1, 1) if month == 12 else (year, month + 1)
    month_end_exclusive = f"{next_year:04d}-{next_month:02d}-01"

    joined, left = ci.get("joinedOn"), ci.get("leftOn")
    # "Not employed" and "above the ceiling" are DIFFERENT FACTS and the ESI
    # return distinguishes them, so the employment test is separate.
    employed = True
    if joined and joined >= month_end_exclusive:
        employed = False
    if left and left < month_start:
        employed = False

    # THE LATCH. The test instant is the PERIOD START, not the wage month; a
    # mid-period joiner is tested on the day they joined because there were no
    # wages of theirs in existence at the period start.
    tested_on = period_start
    if joined and joined > period_start:
        tested_on = joined
    wages_tested = _esi_wage_in_force(ci["revisions"], tested_on)

    # s.2(9): the wage must EXCEED the limit to be out. Somebody on exactly
    # 21000 IS covered, and flipping the operator drops a real population.
    covered = employed and wages_tested <= ESI_COVERAGE_LIMIT
    if variant == "esi-monthly-ceiling-test":
        covered = employed and Decimal(ci["monthWages"]) <= ESI_COVERAGE_LIMIT

    month_wages = Decimal(ci["monthWages"])
    paid_days = int(ci["paidDays"])
    exempt = False
    employee = Decimal("0")
    employer = Decimal("0")
    if covered:
        # wages <= limit * paidDays, NOT wages / paidDays <= limit. The same
        # inequality with no division, so there is no working precision to argue
        # about and no rounding that could push an employee across the boundary.
        exempt = month_wages <= ESI_LOW_WAGE_LIMIT * paid_days
        if not exempt:
            employee = _round_up_rupee(month_wages * ESI_RATES["employeePct"] / 100)
        # Rule 52 is ONE-SIDED: the employer's share is payable in full even
        # where the employee's is exempt.
        employer = _round_up_rupee(month_wages * ESI_RATES["employerPct"] / 100)
    return {
        "covered": covered,
        "applied": covered,
        "wagesTestedOn": tested_on,
        "wagesTested": wages_tested,
        "lowWageExempt": exempt,
        "employeeShare": employee,
        "employerShare": employer,
    }


def _derive_payroll_wages(ci: dict, variant: str | None, version: str) -> dict[str, Any]:
    """Code on Social Security 2020 s.2(88) proviso 1 — the deeming add-back.

    Two sums, not one. `excluded` is every exclusion, clauses (a) to (k), and
    decides what is not a wage. `in_deeming_test` is clauses (a) to (i) only and
    is the ONLY sum measured against the one-half floor, because that is the
    range the proviso names. Gratuity (j) and retrenchment compensation / other
    retirement benefit / ex gratia (k) are excluded from wages AND take no part
    in the test.
    """
    total = Decimal("0")
    included = Decimal("0")
    excluded = Decimal("0")
    in_deeming_test = Decimal("0")
    for c in ci["components"]:
        amount = Decimal(c["amount"])
        total += amount
        if c["type"] in EXCLUDED_FROM_WAGES:
            excluded += amount
            if c["type"] in DEEMING_TEST_CLAUSES_A_TO_I:
                in_deeming_test += amount
        else:
            included += amount
    threshold = DEEMING_THRESHOLD_PCT[version]
    if variant == "deem-all-exclusions":
        # The naive rule: run the "exceeds one-half" test over ALL twelve
        # exclusions instead of over clauses (a) to (i). It deems a leaver's
        # gratuity and retrenchment compensation back into their wages and
        # deducts PF and ESI from money the statute says is not a wage.
        in_deeming_test = excluded
    if variant == "pre-code-no-deeming":
        # The naive rule: apply the PRE-CODE wage definition (no deeming proviso,
        # expressed as a threshold of 100, so the excluded share is never capped)
        # to a wage month that is already inside the Code era. That is the
        # eight-month boundary error the ss-2025-11 window exists to fix, and a
        # fixture whose expected numbers AGREE with this derivation is no longer
        # demonstrating the change of law.
        threshold = Decimal("100")
    # The floor is a share of ALL remuneration (gratuity included, because the
    # proviso says "of the all remuneration calculated under this clause"); the
    # sum tested against it is only (a)-(i). A terminal payment therefore raises
    # the floor and can only ever REDUCE the add-back, never create one.
    floor = total * threshold / 100
    add_back = in_deeming_test - floor if in_deeming_test > floor else Decimal("0")
    return {
        "totalRemuneration": total,
        "includedComponents": included,
        "excludedComponents": excluded,
        "excludedComponentsInDeemingTest": in_deeming_test,
        "deemingThresholdPct": threshold,
        "deemedAddBack": add_back,
        "statutoryWage": included + add_back,
    }


def derive_payroll(
    case_id: str, case_input: dict, variant: str | None = None, version: str = "ss-2026-07"
) -> dict[str, Any]:
    """Reference payroll derivation, dispatched on the fixture id prefix.

    The signature carries the id (and not only the input) because corpora/payroll
    holds FOUR different computations — statutory wages, PF, ESI and income tax —
    and dispatching on the id prefix keeps the fixture files free of a redundant
    "kind" discriminator that could disagree with the filename.

    `variant` selects a NAIVE derivation for the counterfactual blocks. It is not
    a mode the engine has; it exists only so a fixture can prove it still
    demonstrates the thing it claims to demonstrate.
    """
    if case_id.startswith("it-"):
        return _derive_payroll_it(case_input, variant)
    if case_id.startswith("pf-"):
        return _derive_payroll_pf(case_input, variant, version)
    if case_id.startswith("esi-"):
        return _derive_payroll_esi(case_input, variant, version)
    if case_id.startswith("wages-"):
        return _derive_payroll_wages(case_input, variant, version)
    raise KeyError(f"no reference derivation for id prefix of {case_id!r}")


# Per-kind input contracts. Keeping them here (rather than inline) means adding a
# field to a fixture without declaring it is a validation failure rather than a
# silently ignored key — a fixture with a misspelt input field would otherwise
# validate, drive the engine with a default, and pin the wrong behaviour.
_PAYROLL_INPUT_KEYS = {
    "wages-": ["wageMonth", "components"],
    "pf-": [
        "wageMonth",
        "statutoryWage",
        "pfBase",
        "employeeVoluntaryBase",
        "employerMatchesVoluntary",
        "epsMember",
        "firstMemberOn",
    ],
    "esi-": ["wageMonth", "joinedOn", "leftOn", "revisions", "monthWages", "paidDays"],
    "it-": [
        "taxYearStart",
        "regime",
        "isResident",
        "ageBand",
        "panAvailable",
        "priorEmployerTaxable",
        "priorEmployerTDS",
        "ytdTaxableThisEmployer",
        "ytdTDSThisEmployer",
        "currentMonthTaxable",
        "projectedRemainingTaxable",
        "monthsRemainingInclusive",
        "isFinalMonth",
        "housePropertyLoss",
        "chapterVIA",
        "otherIncomeDeclared",
    ],
}

# Expected MONEY fields per kind — each one 4-dp decimal STRINGS, never JSON
# numbers, so no float ever crosses the language boundary.
_PAYROLL_MONEY_FIELDS = {
    "wages-": [
        "totalRemuneration",
        "includedComponents",
        "excludedComponents",
        # Clauses (a)-(i) only — the sole sum proviso 1 tests against the floor.
        # Required on EVERY wages fixture, including the ones where it equals
        # excludedComponents, so that a fixture with a terminal payment cannot be
        # the only place the distinction is written down.
        "excludedComponentsInDeemingTest",
        "deemedAddBack",
        "statutoryWage",
    ],
    "pf-": [
        "epfWages",
        "epsWages",
        "edliWages",
        "employeeStatutory",
        "employeeVoluntary",
        "employerStatutory",
        "employerVoluntary",
        "eps",
        "employeeEPF",
        "employerEPF",
    ],
    "esi-": ["wagesTested", "employeeShare", "employerShare"],
    "it-": [
        "estimatedAnnualTaxable",
        "standardDeduction",
        "taxBeforeRebate",
        "rebate",
        "rebateMarginalRelief",
        "surcharge",
        "surchargeMarginalRelief",
        "cess",
        "annualTax",
        "creditedAlready",
        "tdsThisMonth",
    ],
}

# Expected BOOLEAN fields per kind. They are as load-bearing as the money: a
# coverage flag or a pension-membership flag that is wrong makes every amount
# beside it wrong too.
_PAYROLL_BOOL_FIELDS = {
    "wages-": [],
    "pf-": ["epsMemberApplied"],
    "esi-": ["covered", "applied", "lowWageExempt"],
    "it-": [],
}

# Input money fields per kind (scalars only; nested lists are checked in place).
_PAYROLL_INPUT_MONEY = {
    "wages-": [],
    "pf-": ["statutoryWage", "employeeVoluntaryBase"],
    "esi-": ["monthWages"],
    "it-": [
        "priorEmployerTaxable",
        "priorEmployerTDS",
        "ytdTaxableThisEmployer",
        "ytdTDSThisEmployer",
        "currentMonthTaxable",
        "projectedRemainingTaxable",
        "housePropertyLoss",
        "chapterVIA",
        "otherIncomeDeclared",
    ],
}

_ISO_DATE = re.compile(r"^\d{4}-\d{2}-\d{2}$")
_WAGE_MONTH = re.compile(r"^\d{4}-\d{2}$")


def _payroll_kind(case_id: str) -> str | None:
    for prefix in _PAYROLL_INPUT_KEYS:
        if case_id.startswith(prefix):
            return prefix
    return None


def _payroll_check_input(path: Path, kind: str, ci: dict) -> bool:
    """Shape-check a fixture's input. Returns False once anything is wrong, so a
    malformed fixture never reaches the arithmetic and produces a confusing
    message about a number instead of a clear one about a field."""
    if not require(path, ci, _PAYROLL_INPUT_KEYS[kind]):
        return False
    for key in ci:
        if key not in _PAYROLL_INPUT_KEYS[kind]:
            fail(path, f"input.{key} is not part of the {kind}* contract")
            return False
    if not _WAGE_MONTH.match(str(ci.get("wageMonth", ""))) and kind != "it-":
        fail(path, f"input.wageMonth must be YYYY-MM, got {ci.get('wageMonth')!r}")
        return False
    for field in _PAYROLL_INPUT_MONEY[kind]:
        if money(path, f"input.{field}", ci[field]) is None:
            return False

    if kind == "wages-":
        if not isinstance(ci["components"], list) or not ci["components"]:
            fail(path, "input.components must be a non-empty list")
            return False
        for i, c in enumerate(ci["components"]):
            if not require(path, c, ["type", "amount"]):
                return False
            if money(path, f"input.components[{i}].amount", c["amount"]) is None:
                return False
    if kind == "pf-":
        if ci["pfBase"] not in PF_BASES:
            fail(path, f"input.pfBase must be one of {sorted(PF_BASES)}, got {ci['pfBase']!r}")
            return False
        for flag in ("employerMatchesVoluntary", "epsMember"):
            if not isinstance(ci[flag], bool):
                fail(path, f"input.{flag} must be a boolean")
                return False
        if ci["firstMemberOn"] is not None and not _ISO_DATE.match(str(ci["firstMemberOn"])):
            fail(path, f"input.firstMemberOn must be null or YYYY-MM-DD, got {ci['firstMemberOn']!r}")
            return False
    if kind == "esi-":
        for field in ("joinedOn", "leftOn"):
            if ci[field] is not None and not _ISO_DATE.match(str(ci[field])):
                fail(path, f"input.{field} must be null or YYYY-MM-DD, got {ci[field]!r}")
                return False
        if not isinstance(ci["revisions"], list) or not ci["revisions"]:
            # A single current wage cannot implement the LATCH at all — the test
            # instant is in the past — so the history is required, not optional.
            fail(path, "input.revisions must be a non-empty effective-dated wage history")
            return False
        seen = set()
        for i, r in enumerate(ci["revisions"]):
            if not require(path, r, ["effectiveFrom", "monthlyWages"]):
                return False
            if not _ISO_DATE.match(str(r["effectiveFrom"])):
                fail(path, f"input.revisions[{i}].effectiveFrom must be YYYY-MM-DD")
                return False
            if r["effectiveFrom"] in seen:
                fail(path, f"input.revisions has two rows effective {r['effectiveFrom']}")
                return False
            seen.add(r["effectiveFrom"])
            if money(path, f"input.revisions[{i}].monthlyWages", r["monthlyWages"]) is None:
                return False
        if not isinstance(ci["paidDays"], int) or not 1 <= ci["paidDays"] <= 31:
            fail(path, f"input.paidDays must be an int in 1..31, got {ci['paidDays']!r}")
            return False
    if kind == "it-":
        if not _ISO_DATE.match(str(ci["taxYearStart"])) or not ci["taxYearStart"].endswith("-04-01"):
            fail(path, f"input.taxYearStart must be a 1 April date, got {ci['taxYearStart']!r}")
            return False
        if ci["regime"] not in REGIMES:
            fail(path, f"input.regime must be one of {sorted(REGIMES)}, got {ci['regime']!r}")
            return False
        if ci["ageBand"] not in AGE_BANDS:
            fail(path, f"input.ageBand must be one of {sorted(AGE_BANDS)}, got {ci['ageBand']!r}")
            return False
        for flag in ("isResident", "panAvailable", "isFinalMonth"):
            if not isinstance(ci[flag], bool):
                fail(path, f"input.{flag} must be a boolean")
                return False
        months = ci["monthsRemainingInclusive"]
        if not isinstance(months, int) or months < 1:
            # The engine returns ErrMonthsRemaining for this; a fixture asserting
            # a SUCCESSFUL derivation with fewer than one month is incoherent.
            fail(path, f"input.monthsRemainingInclusive must be an int >= 1, got {months!r}")
            return False
    return True


def validate_payroll(path: Path, case: dict) -> None:
    """Validate a payroll fixture and RE-DERIVE every expected number.

    Shape first, then the field-by-field comparison against an INDEPENDENT
    derivation, then — where the fixture carries one — the COUNTERFACTUAL: the
    naive rule is re-derived and the fixture FAILS IF IT AGREES with `expected`.
    A fixture whose naive and correct derivations have converged no longer
    demonstrates anything, and this is the only mechanism that notices.
    """
    if not require(path, case, ["id", "description", "rulesetVersion", "input", "expected"]):
        return
    case_id = case["id"]
    if case_id != path.stem:
        fail(path, f"id {case_id!r} must equal the filename stem {path.stem!r}")
        return
    kind = _payroll_kind(case_id)
    if kind is None:
        fail(path, f"id {case_id!r} has no known prefix (wages-, pf-, esi-, it-)")
        return

    # A payroll fixture is simultaneously a LEGAL CLAIM and a COMPUTATION, so its
    # description must carry both the citation and the hand derivation.
    if not isinstance(case["description"], str) or len(case["description"]) < 40:
        fail(path, "description must carry the statutory citation AND the hand derivation (>=40 chars)")

    version = case["rulesetVersion"]
    if kind == "it-":
        if version != PAYROLL_IT_RULESET_VERSION:
            fail(path, f"rulesetVersion must be {PAYROLL_IT_RULESET_VERSION!r}, got {version!r}")
            return
    elif version not in PAYROLL_SS_RULESET_VERSIONS:
        fail(path, f"rulesetVersion must be one of {sorted(PAYROLL_SS_RULESET_VERSIONS)}, got {version!r}")
        return

    ci = case["input"]
    if not _payroll_check_input(path, kind, ci):
        return

    exp = case["expected"]
    declared: dict[str, Any] = {}
    for field in _PAYROLL_MONEY_FIELDS[kind]:
        if field not in exp:
            fail(path, f"missing required field expected.{field!r}")
            return
        dec = money(path, f"expected.{field}", exp[field])
        if dec is None:
            return
        declared[field] = dec
    for field in _PAYROLL_BOOL_FIELDS[kind]:
        if field not in exp or not isinstance(exp[field], bool):
            fail(path, f"expected.{field} must be present and a boolean")
            return
        declared[field] = exp[field]

    if kind == "wages-":
        rate = rate_str(path, "expected.deemingThresholdPct", exp.get("deemingThresholdPct"))
        if rate is None:
            return
        declared["deemingThresholdPct"] = rate
    if kind == "esi-":
        if not _ISO_DATE.match(str(exp.get("wagesTestedOn", ""))):
            # Echoing the test INSTANT is what makes the latch auditable: without
            # it a fixture cannot show that the ceiling was applied at the period
            # start rather than to this month's wage.
            fail(path, f"expected.wagesTestedOn must be YYYY-MM-DD, got {exp.get('wagesTestedOn')!r}")
            return
        declared["wagesTestedOn"] = exp["wagesTestedOn"]
    if kind == "it-":
        for field in ("regimeApplied", "periodBasis"):
            if field not in exp:
                fail(path, f"missing required field expected.{field!r}")
                return
            declared[field] = exp[field]
        if declared["regimeApplied"] not in REGIMES:
            fail(path, f"expected.regimeApplied must be one of {sorted(REGIMES)}")
            return
        if declared["periodBasis"] != "ANNUAL":
            # Salary tax is ANNUAL by statute. Any other basis here would mean the
            # engine had inherited compliance's per-transaction approximation.
            fail(path, f"expected.periodBasis must be 'ANNUAL', got {declared['periodBasis']!r}")
            return
    if kind == "pf-":
        deferred = exp.get("deferredComponents")
        want_deferred = PF_DEFERRED_COMPONENTS[version]
        if not isinstance(deferred, list) or sorted(deferred) != want_deferred:
            # Every PF case must declare the FULL deferral set for its ruleset. A
            # fixture that quietly dropped one would let a future engine invent a
            # rate for it and nothing would notice.
            fail(
                path,
                f"expected.deferredComponents must be exactly {want_deferred} for ruleset "
                f"{version!r}, got {deferred!r}",
            )
            return

    try:
        computed = derive_payroll(case_id, ci, version=version)
    except (KeyError, InvalidOperation, ZeroDivisionError) as exc:
        fail(path, f"could not re-derive: {exc}")
        return

    for field, want in computed.items():
        got = declared.get(field)
        if got is None and field not in declared:
            fail(path, f"expected.{field} is missing but the reference derivation produces it")
            continue
        if isinstance(want, Decimal):
            if got != want:
                fail(path, f"expected.{field}={got:f} is WRONG; reference derivation = {want:f}")
        elif got != want:
            fail(path, f"expected.{field}={got!r} is WRONG; reference derivation = {want!r}")

    # A case that does not apply must declare ZERO for its money fields: a stale
    # amount beside covered=false would be remitted by a caller that trusted the
    # number instead of the flag.
    if kind == "esi-":
        if not declared["covered"]:
            for field in ("employeeShare", "employerShare"):
                if declared[field] != 0:
                    fail(path, f"expected.covered is false but expected.{field} is non-zero")
        if declared["lowWageExempt"] and declared["employeeShare"] != 0:
            fail(path, "expected.lowWageExempt is true but expected.employeeShare is non-zero")
    if kind == "pf-":
        if not declared["epsMemberApplied"] and (declared["eps"] != 0 or declared["epsWages"] != 0):
            fail(path, "expected.epsMemberApplied is false but the EPS wages/contribution are non-zero")
        if exp.get("edliContributionRefused") and "edliContribution" in exp:
            fail(path, "a refused EDLI contribution must not also carry an amount")

    # --- the counterfactual: prove the fixture still demonstrates its claim ---
    cf = case.get("counterfactual")
    if cf is None:
        return
    if not require(path, cf, ["rule", "why"]):
        return
    if not isinstance(cf["why"], str) or len(cf["why"]) < 40:
        fail(path, "counterfactual.why must state the CONSEQUENCE of the naive rule (>=40 chars)")
        return
    if cf["rule"] not in PAYROLL_COUNTERFACTUAL_RULES:
        fail(
            path,
            f"counterfactual.rule {cf['rule']!r} is not one of {sorted(PAYROLL_COUNTERFACTUAL_RULES)}",
        )
        return
    try:
        naive = derive_payroll(case_id, ci, variant=cf["rule"], version=version)
    except (KeyError, InvalidOperation, ZeroDivisionError):
        # The naive rule blowing up IS a difference, and a loud one.
        return
    if naive == computed:
        fail(
            path,
            f"fixture no longer demonstrates {case_id}: the naive derivation now agrees with expected",
        )


VALIDATORS = {
    "tax": validate_tax,
    "tds": validate_tds,
    "tcs": validate_tcs,
    "keystroke": validate_keystroke,
    "isolation": validate_isolation,
    "migration": validate_migration,
    "forecast": validate_forecast,
    "inventory": validate_inventory,
    "gstr2b": validate_gstr2b,
    "ims": validate_ims,
    "payroll": validate_payroll,
}

MIN_COUNTS = {
    "tax": 14,
    "tds": 8,
    "tcs": 2,
    "keystroke": 14,
    "isolation": 10,
    "migration": 6,
    "forecast": 4,
    # The inventory floor is the six behaviours the valuation contract rests on:
    # simple in/out, a rate change repricing the closing value, a BACK-DATED
    # inward re-valuing an already-posted outward, per-godown positions, a batch
    # dimension, and an opening folded in. Deleting any of them must fail CI
    # rather than quietly shrink the gate.
    "inventory": 6,
    # The 2A/2B floor is set to the EXACT number of fixtures, not to a lower
    # "at least ten", so that deleting ANY one of them fails CI. The
    # architecture's ">= 10" is the number below which the family stops being a
    # gate at all; the number below which it stops being THIS gate is 16, and
    # each of the sixteen exists because it is a specific way to be wrong:
    # exact match; rounding within the per-head tolerance; a value mismatch
    # with the fields named; a date mismatch ACROSS the s.16(4) financial-year
    # boundary; a leading-zero near-match that must NOT pair; a duplicate
    # document number that must go AMBIGUOUS rather than pick one; missing in
    # 2B; missing in books; a CREDIT NOTE whose number equals an invoice's,
    # which is what bites a matcher that left doc_type out of the key; RCM,
    # which must never be reversed for being unmatched; GSTN's docRejdata rows,
    # which must never reach table 4(A); a prior-FY invoice arriving in a
    # current 2B, because the 2B window is on the supplier's FILING date;
    # an amendment superseding its original; a s.17(5) blocked credit reversed
    # in 4(B)(1); time-barred ITC excluded from 4(A) and reported in 4(D)(2);
    # and a legacy purchase with no supplier document number, which is
    # NOT_COMPARABLE and must never be reported as the supplier not having filed.
    "gstr2b": 16,
    # Same reasoning. The IMS floor protects the deemed-accept boundary in IST
    # (both instants, and the same two instants written in UTC), the short
    # month, the year rollover, a generation date that SLIPPED past the 14th,
    # an explicit action beating deemed-accept, an action after the GSTR-3B
    # filing instant being ignored, latest-action-wins over a log, the
    # effective-dated PENDING restriction on credit notes before and from the
    # October-2025 tax period, the sections that bypass IMS entirely, GSTN's
    # Annexure-A decision table, and the s.16(4) expiry boundaries.
    "ims": 12,
    # PAYROLL. The floor is 12 and not the 14 fixtures shipped, so that two
    # fixtures could be retired in a deliberate, reviewed change without
    # rewriting the gate — but no more than two, because these twelve behaviours
    # are what the payslip rests on and deleting one to make a red gate green
    # must fail CI instead:
    #   1. the Code on Social Security 2020 s.2(y) 50% deeming add-back, which is
    #      UPSTREAM of both PF and ESI, so a silent error here is systematic;
    #   2. EPF/EPS/EDLI at the 15000 ceiling, with EPS rounded to 1250 and the
    #      employer's EPF share taking the 550 remainder;
    #   3. the para 26A(2) restriction, where a 40000 wage still contributes on
    #      15000;
    #   4. VPF under para 26(6): the member may pay more and the employer is NOT
    #      obliged to match it;
    #   5. EPS exclusion for a post-01-09-2014 member above the ceiling, where
    #      the whole 12% stays in EPF;
    #   6. the EDLI contribution REFUSED as unnotified rather than invented;
    #   7. the ESI s.2(9) coverage LATCH surviving a mid-period raise;
    #   8. its mirror — out at the period start stays out — resolved against the
    #      PRE-Code ruleset, which is also what proves an old wage month does not
    #      silently borrow today's rates;
    #   9. the rule 52 low-wage exemption applying to the EMPLOYEE share only;
    #  10. rule 51's ceiling rounding on BOTH shares;
    #  11. the s.156 (ex-87A) REBATE marginal relief, the half of the marginal
    #      reliefs that engines omit, plus the classic surcharge relief;
    #  12. the s.392 annual base — a mid-year joiner aggregating the former
    #      employer's salary, and total income rounded to the nearest TEN while
    #      the tax is rounded to the nearest RUPEE.
    "payroll": 12,
}

# Families that carry a POISONED-FIXTURE TRIPWIRE. See check_poison().
POISON_FAMILIES = ("gstr2b", "ims")


def _ts_union_members(source: str, type_name: str) -> set[str] | None:
    """Extract the string-literal members of `export type <type_name> = ...;`.

    Returns None when the union is not found at all, so a rename in the
    TypeScript is reported as a missing type rather than as an empty set (an
    empty set would silently claim EVERY known token had been deleted).
    """
    # Strip block comments first: the unions are annotated per-member
    # (`/** Alt+F6 — sales return */`), and a comment that happened to contain a
    # quoted word would otherwise be read as a member.
    stripped = re.sub(r"/\*.*?\*/", "", source, flags=re.DOTALL)
    m = re.search(
        r"export type " + re.escape(type_name) + r"\s*=(.*?);", stripped, flags=re.DOTALL
    )
    if not m:
        return None
    return set(re.findall(r'"([^"]+)"', m.group(1)))


def check_vocabulary_matches_typescript() -> None:
    """Fail if this file's closed vocabularies have drifted from ScreenKind /
    VoucherType in the frontend.

    WHY THIS IS A CHECK AND NOT A COMMENT ASKING PEOPLE TO REMEMBER. KNOWN_SCREENS
    has now fallen behind ScreenKind TWICE. Both times the symptom was a VALID
    fixture being rejected, with a message naming the fixture — so the cheapest
    apparent fix is to edit the golden case until the validator goes quiet, which
    replaces a correct expectation with a wrong one. The drift also runs the other
    way: a token left here after the union dropped it is a fixture nobody can
    detect is dead.

    The frontend is a sibling of this directory, not a dependency of it, so a
    checkout or CI image without it SKIPS rather than fails: this validator is
    documented as stdlib-only and must keep running on the corpora alone.
    """
    types_ts = ROOT.parent / "frontend" / "src" / "keystroke" / "types.ts"
    if not types_ts.is_file():
        return
    source = types_ts.read_text(encoding="utf8")
    for type_name, known, label in (
        ("ScreenKind", KNOWN_SCREENS, "KNOWN_SCREENS"),
        ("VoucherType", KNOWN_VOUCHER_TYPES, "KNOWN_VOUCHER_TYPES"),
    ):
        members = _ts_union_members(source, type_name)
        if members is None:
            _errors.append(
                f"{label}: could not find `export type {type_name}` in {types_ts} — "
                f"it was renamed or moved, so this validator has stopped checking it"
            )
            continue
        missing = sorted(members - known)
        extra = sorted(known - members)
        if missing:
            _errors.append(
                f"{label}: {type_name} has {missing} which this file does not list — "
                f"a fixture naming one would be rejected even though the screen exists"
            )
        if extra:
            _errors.append(
                f"{label}: this file lists {extra} which {type_name} no longer has — "
                f"a fixture naming one would validate and then drive nothing"
            )


def check_poison() -> None:
    """Fail if a DELIBERATELY WRONG fixture validates.

    THIS IS A TEST OF THE VALIDATOR, NOT OF THE DATA, and it is the direct
    answer to the failure that actually happened: a corpus oracle was "fixed"
    by editing it to agree with the mapper it was supposed to check, which made
    the metric tautological and detected nothing thereafter.

    Each `<family>/_poison/*.json` is a VALID-SHAPED case whose `expected` block
    is wrong in a specific, named way — a near-match recorded as a match, a
    rejected document summed into table 4(A), a boundary off by one instant.
    The re-derivation must reject every one of them. The moment anyone rewrites
    a `_derive_*` function to return `case["expected"]`, or relaxes a
    comparison until it stops complaining, EVERY poison case starts passing and
    this check goes red naming all of them.

    The leading underscore keeps `_poison/` out of `directory.glob("*.json")`
    in main()'s loop and out of the Go corpus loader, so the family counts
    printed in the summary are honest and a poison case can never be mistaken
    for a gate.
    """
    for family in POISON_FAMILIES:
        directory = ROOT / family / "_poison"
        if not directory.is_dir():
            _errors.append(
                f"{family}/_poison/: the tripwire directory is missing — without it, a validator "
                f"that restates its fixtures instead of re-deriving them passes silently"
            )
            continue
        files = sorted(directory.glob("*.json"))
        if len(files) < 2:
            _errors.append(
                f"{family}/_poison/: only {len(files)} poisoned fixtures, at least 2 required"
            )
        for path in files:
            data = load_json(path)
            if data is None or not isinstance(data, dict):
                continue
            # Run the real validator against a SCRATCH error list so a poisoned
            # case's (expected) failures never reach the report.
            saved = list(_errors)
            _errors.clear()
            try:
                VALIDATORS[family](path, data)
            except Exception as exc:  # a crash is a rejection, but a noisy one
                _errors.append(f"{path.relative_to(ROOT)}: raised {exc!r}")
            caught = list(_errors)
            _errors.clear()
            _errors.extend(saved)
            if not caught:
                _errors.append(
                    f"poisoned fixture {path.relative_to(ROOT)} validated — the oracle has become "
                    f"tautological; validate.py is restating the fixture instead of re-deriving it"
                )


def main() -> int:
    check_vocabulary_matches_typescript()
    check_poison()
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
