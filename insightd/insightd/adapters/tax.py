"""Pure-python re-derivation of cored's TaxDerive — for DISPLAY-ONLY GST.

Reproduces backend/internal/compliance/tax.go to the paisa:
  - intra-state (supplier state == place-of-supply state): per line, per
    component, half = round_half_up(taxable * (rate/2) / 100, 4dp); CGST and
    SGST each accumulate the half (rounded INDEPENDENTLY per line).
  - inter-state: per line IGST = round_half_up(taxable * rate / 100, 4dp).
  - zero-rate lines contribute no tax.
  - invoice_total = taxable_total under reverse charge, else taxable + total_tax.

The mapper uses this ONLY for the draft's derived_gst (display). cored
re-derives authoritatively on the human's POST.
"""

from __future__ import annotations

from dataclasses import dataclass
from decimal import ROUND_HALF_UP, Decimal

_Q = Decimal("0.0001")
_HUNDRED = Decimal(100)
_TWO = Decimal(2)


def _round(d: Decimal) -> Decimal:
    return d.quantize(_Q, rounding=ROUND_HALF_UP)


def fixed4(d: Decimal) -> str:
    return f"{_round(d):.4f}"


@dataclass(frozen=True)
class TaxLine:
    taxable_value: Decimal
    gst_rate_pct: Decimal


@dataclass(frozen=True)
class TaxResult:
    cgst: Decimal
    sgst: Decimal
    igst: Decimal
    total_tax: Decimal
    invoice_total: Decimal
    taxable_total: Decimal


def tax_derive(
    *,
    supplier_state_code: str | None,
    place_of_supply_state_code: str | None,
    reverse_charge: bool,
    lines: list[TaxLine],
) -> TaxResult:
    intra = (
        supplier_state_code is not None
        and place_of_supply_state_code is not None
        and supplier_state_code == place_of_supply_state_code
    )

    cgst = Decimal(0)
    sgst = Decimal(0)
    igst = Decimal(0)
    taxable_total = Decimal(0)

    for ln in lines:
        taxable_total += ln.taxable_value
        if ln.gst_rate_pct == 0:
            continue
        if intra:
            half = _round(ln.taxable_value * (ln.gst_rate_pct / _TWO) / _HUNDRED)
            cgst += half
            sgst += half
        else:
            full = _round(ln.taxable_value * ln.gst_rate_pct / _HUNDRED)
            igst += full

    total_tax = cgst + sgst + igst
    invoice_total = taxable_total if reverse_charge else taxable_total + total_tax

    return TaxResult(
        cgst=_round(cgst),
        sgst=_round(sgst),
        igst=_round(igst),
        total_tax=_round(total_tax),
        invoice_total=_round(invoice_total),
        taxable_total=_round(taxable_total),
    )
