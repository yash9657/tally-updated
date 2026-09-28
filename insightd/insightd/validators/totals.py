"""Totals reconcile: taxable + taxes ± round-off = total, and the lines add up.

Two INDEPENDENT paths to one number is the check a model cannot talk its way
past: the bill's printed total (read by OCR/the model) against the total the
lines DERIVE (re-computed by adapters/tax.py against cored's ruleset). Either
path corrupted — a digit misread, a line dropped, a rate hallucinated — and the
two stop agreeing.

ROUND-OFF IS NOT A TOLERANCE, AND THE DIFFERENCE IS THE WHOLE RULE. Indian
invoices routinely round the payable to the rupee (a "Round Off" line), so a
bill whose total is 1 paisa to 99 paise off its lines is often CORRECT. A
tolerance would accept every such gap; this accepts exactly one:

  * a STATED round-off (``InferredTotals.round_off``) of magnitude at most
    0.50, after which the arithmetic must hold to the paisa; or
  * an IMPLIED round-off, allowed only when the printed total is a WHOLE RUPEE
    and equals the derived total rounded half-up to the rupee — the one number
    rounding could have produced. 1,180.37 derived and 1,180.00 printed passes;
    1,180.37 derived and 1,181.00 printed does not, because rounding cannot
    move 37 paise up.

Money is ``Decimal`` from strings throughout. No float.
"""

from __future__ import annotations

from decimal import ROUND_HALF_UP, Decimal, InvalidOperation

_MAX_ROUND_OFF = Decimal("0.50")


def _dec(s) -> Decimal | None:
    if s is None or s == "":
        return None
    try:
        return Decimal(str(s))
    except (InvalidOperation, ValueError):
        return None


def validate_totals(inferred, derived):
    """Validate an InferredInvoice's stated totals against the derivation.

    Returns a list of ``Validation`` — one per check that could run.
    """
    from ..contracts.draft import Validation

    out: list[Validation] = []
    t = inferred.totals

    # 1. The lines add up to the stated taxable value.
    stated_taxable = _dec(t.taxable)
    line_sum = sum((_dec(li.taxable_value) or Decimal(0) for li in inferred.line_items), Decimal(0))
    if stated_taxable is None:
        out.append(Validation(check="lines_sum_to_taxable", outcome="not_run", field="totals",
                              detail="the document states no taxable total"))
    elif stated_taxable != line_sum:
        out.append(Validation(check="lines_sum_to_taxable", outcome="fail", field="totals",
                              detail=f"the lines add up to {line_sum}, the document states taxable {stated_taxable}"))
    else:
        out.append(Validation(check="lines_sum_to_taxable", outcome="pass", field="totals",
                              detail=f"the lines add up to the stated taxable {stated_taxable}"))

    # 2. Each STATED tax component equals the re-derived one. Components the
    #    document does not state are not compared (not_run), never assumed zero.
    stated = {"cgst": _dec(t.cgst), "sgst": _dec(t.sgst), "igst": _dec(t.igst)}
    compared = {k: v for k, v in stated.items() if v is not None}
    if not compared:
        out.append(Validation(check="tax_matches_rate", outcome="not_run", field="totals",
                              detail="the document states no tax amounts"))
    else:
        bad = [f"{k} stated {v}, derived {getattr(derived, k)}" for k, v in compared.items()
               if v != getattr(derived, k)]
        if bad:
            out.append(Validation(check="tax_matches_rate", outcome="fail", field="totals",
                                  detail="; ".join(bad)))
        else:
            out.append(Validation(check="tax_matches_rate", outcome="pass", field="totals",
                                  detail="every stated tax amount equals rate × taxable"))

    # 3. taxable + taxes ± round-off = total.
    stated_total = _dec(t.invoice_total)
    computed = derived.invoice_total
    if stated_total is None:
        out.append(Validation(check="total_reconciles", outcome="not_run", field="totals",
                              detail="the document states no invoice total"))
        return out
    round_off = _dec(getattr(t, "round_off", None))
    if round_off is not None:
        if abs(round_off) > _MAX_ROUND_OFF:
            out.append(Validation(check="total_reconciles", outcome="fail", field="totals",
                                  detail=f"a round-off of {round_off} is more than rounding to the rupee can produce"))
        elif computed + round_off != stated_total:
            out.append(Validation(check="total_reconciles", outcome="fail", field="totals",
                                  detail=f"{computed} + round-off {round_off} = {computed + round_off}, "
                                         f"the document states {stated_total}"))
        else:
            out.append(Validation(check="total_reconciles", outcome="pass", field="totals",
                                  detail=f"{computed} + round-off {round_off} = {stated_total}"))
        return out
    if stated_total == computed:
        out.append(Validation(check="total_reconciles", outcome="pass", field="totals",
                              detail=f"taxable + tax = {stated_total}"))
        return out
    rupee = computed.quantize(Decimal("1"), rounding=ROUND_HALF_UP)
    if stated_total == stated_total.quantize(Decimal("1")) and stated_total == rupee:
        out.append(Validation(check="total_reconciles", outcome="pass", field="totals",
                              detail=f"{computed} rounded to the rupee is the stated {stated_total} "
                                     f"(implied round-off {stated_total - computed})"))
        return out
    out.append(Validation(check="total_reconciles", outcome="fail", field="totals",
                          detail=f"the lines derive {computed}, the document states {stated_total}, "
                                 "and no round-off to the rupee explains the difference"))
    return out
