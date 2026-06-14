"""cored mirror models — 1:1 with what a HUMAN POSTs to cored.

These mirror ``backend/internal/server/server.go``'s voucherReq / gstReq and
``frontend/src/api/types.ts`` (VoucherRequest / GSTRequest / PostingRequest /
GSTLineRequest). The draft's ``body`` is assembled from these so it drops
straight into the existing frontend voucher API with no translation.

Conventions (non-negotiable, mirroring types.ts):
  - Money / quantities / rates are fixed-4 decimal STRINGS (e.g. "1234.5600").
    Never a float — that silently loses paisa.
  - dr_cr is the backend's canonical "D"/"C".
  - state codes are 2-digit GST strings.

Depends on nothing but pydantic.
"""

from __future__ import annotations

from typing import Literal

from pydantic import BaseModel, ConfigDict

# A fixed-4 decimal value transported as a string (e.g. "1234.5600").
Money = str

_FORBID = ConfigDict(extra="forbid")


class PostingDraft(BaseModel):
    """One leg of the double-entry voucher (mirrors PostingRequest)."""

    model_config = _FORBID

    # None when the ledger is a *proposed* new master (Alt+C on-the-fly create);
    # ledger_name then carries the human-facing name to create.
    ledger_id: str | None = None
    ledger_name: str
    dr_cr: Literal["D", "C"]
    amount: Money


class GSTLineDraft(BaseModel):
    """One GST line (mirrors GSTLineRequest). NOTE: the LLM supplies
    taxable_value + rate + hsn ONLY; cored re-derives the tax server-side."""

    model_config = _FORBID

    taxable_value: Money
    gst_rate_pct: Money
    hsn: str | None = None


class GSTDraft(BaseModel):
    """The GST block (mirrors GSTRequest)."""

    model_config = _FORBID

    ruleset_version: str  # pinned to cored's, e.g. "gst-2024-09"
    supplier_state_code: str | None = None
    place_of_supply_state_code: str | None = None
    reverse_charge: bool = False
    exempt: bool = False
    lines: list[GSTLineDraft]
    cgst_ledger_id: str | None = None  # from tenant config
    sgst_ledger_id: str | None = None
    igst_ledger_id: str | None = None


class VoucherDraftBody(BaseModel):
    """Exactly what a human would POST to /companies/{id}/vouchers/purchase
    (mirrors VoucherRequest, class fixed = purchase)."""

    model_config = _FORBID

    voucher_class: Literal["purchase"] = "purchase"
    voucher_type_id: str | None = None
    party_ledger_id: str | None = None
    party: str | None = None  # supplier display name (for Alt+C if ledger_id is None)
    # supplier's invoice no (Purchase: supplier ref, not auto-numbered)
    voucher_no: str | None = None
    voucher_date: str | None = None
    narration: str | None = None
    postings: list[PostingDraft]
    gst: GSTDraft | None = None
