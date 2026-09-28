"""HSN / SAC code validation — structure, and the tenant's own history.

⛔ THERE IS NO HSN MASTER IN THIS BUILD, AND NONE IS INVENTED HERE. The CBIC
publishes the HSN/SAC directory; no copy of it exists in this repository or in
cored's migrations (checked: the only HSN column is the free-text one on
stock_item and voucher_gst_line). HANDOFF-FEATURE-LOCK.md §1.1 — "never invent
a rate, threshold, clause letter, CODE or keystroke; cite it or refuse" — rules
out a list typed from memory, and a wrong "this code does not exist" is a
refusal of a correct bill. So "existence" is answered from the two sources of
EVIDENCE that are in front of us, and the gap is stated:

  1. STRUCTURE (always): HSN codes are 4, 6 or 8 digits and begin with a
     chapter 01-98; SAC codes are 4 or 6 digits beginning 99. Chapter 77 is reserved
     in the Harmonized System and has no codes, so a leading 77 is a misread.
     A code failing structure is ``fail`` — it cannot be any real code. (SAC
     is 4 or 6: the first draft of this file said "6 only" and failed every
     services bill in the corpus, which print the 4-digit heading.)
  2. THE TENANT'S HISTORY (when supplied): ``TenantContext.known_hsn`` is the
     set of codes this company has used on its own stock items and vouchers. A
     code in it is ``pass``. A structurally valid code NOT in it is
     ``unverified`` — reported, never failed: a new supplier line is ordinary.

The directory check itself is OWED (d123 §6): it needs the published file
loaded as reference data, with its version, not a constant.
"""

from __future__ import annotations

import re

_DIGITS = re.compile(r"^\d+$")


def hsn_structure_ok(code: str) -> tuple[bool, str]:
    """(ok, why) for one code's STRUCTURE only."""
    c = code.strip().replace(" ", "").replace(".", "")
    if not c:
        return False, "empty"
    if not _DIGITS.match(c):
        return False, "an HSN/SAC code is digits only"
    if c.startswith("99"):
        # SAC: a 4-digit heading (9983) or a 6-digit service code (998311).
        # Bills print both; the heading is what most small suppliers use.
        if len(c) not in (4, 6):
            return False, "a SAC code (services, chapter 99) is 4 or 6 digits"
        return True, "SAC structure"
    if len(c) not in (4, 6, 8):
        return False, "an HSN code is 4, 6 or 8 digits"
    chapter = int(c[:2])
    if chapter < 1 or chapter > 98:
        return False, f"chapter {c[:2]} does not exist"
    if chapter == 77:
        return False, "chapter 77 is reserved in the Harmonized System and has no codes"
    return True, "HSN structure"


def validate_line_hsn(idx: int, code: str | None, known: set[str]):
    from ..contracts.draft import Validation

    field = f"line[{idx}].hsn"
    if not code:
        return Validation(check="hsn", outcome="not_run", field=field, detail="no HSN/SAC on this line")
    ok, why = hsn_structure_ok(code)
    if not ok:
        return Validation(check="hsn", outcome="fail", field=field,
                          detail=f"{code!r} cannot be an HSN/SAC code: {why}")
    norm = code.strip().replace(" ", "").replace(".", "")
    if norm in known or any(norm.startswith(k) or k.startswith(norm) for k in known if len(k) >= 4):
        return Validation(check="hsn", outcome="pass", field=field,
                          detail="the code (or its heading) is in this company's own history")
    return Validation(
        check="hsn", outcome="unverified", field=field,
        detail=f"{why} holds, but this company has not used the code before and no HSN directory "
               "is loaded in this build to confirm it exists",
    )
