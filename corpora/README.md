# Golden Corpora — the CI gates

These datasets are the **ground truth** that gate every merge. They are authored from `RESEARCH.md` (cited Tally behavior) and real TallyPrime, and are intentionally language-neutral JSON so both the Go backend and the TS frontend consume the *same* expectations.

**Money is always a decimal string** (e.g. `"180.0000"`), never a JSON number — to forbid float drift across languages. State codes are GST 2-digit strings (e.g. `"27"` = Maharashtra).

## 1. `tax/` — Tax-derivation regression (consumed by `backend` TaxDeriveEngine tests)

Each file is one case. The backend's pure `TaxDerive(input, ruleset)` must reproduce `expected` exactly.

```jsonc
{
  "id": "intra-state-b2b-18",
  "description": "Intra-state B2B sale @18% -> CGST 9% + SGST 9% (place of supply == supplier state)",
  "rulesetVersion": "gst-2024-09",
  "input": {
    "supplierStateCode": "27",
    "placeOfSupplyStateCode": "27",
    "reverseCharge": false,
    "lines": [{ "taxableValue": "1000.0000", "gstRatePct": "18.00", "hsn": "8471" }]
  },
  "expected": {
    "cgst": "90.0000", "sgst": "90.0000", "igst": "0.0000",
    "totalTax": "180.0000", "invoiceTotal": "1180.0000"
  }
}
```

## 2. `keystroke/` — Keystroke-conformance (consumed by `frontend` XState machine tests)

Drives the navigation state machine from `startScreen` through `events`; asserts resulting `screen`, focused `field`, and visible `buttonBar`. Mirrors TallyPrime grammar (sec 4 + sec 22 of `RESEARCH.md`).

```jsonc
{
  "id": "gateway-f8-opens-sales",
  "description": "From Gateway of Tally, F8 opens the Sales (Accounting) voucher",
  "keymapProfile": "tallyprime",
  "startScreen": "gateway",
  "events": [{ "type": "key", "combo": "F8" }],
  "expect": { "screen": "voucher", "voucherType": "sales", "buttonBarContains": ["F8 Sales", "F9 Purchase"] }
}
```

## 3. `isolation/` — Tenant-isolation fuzz (consumed by `backend` security integration tests)

Adversarial scenarios that MUST NOT leak across tenants. The harness seeds rows for two companies, then runs `action` under the stated session and asserts `expect`.

```jsonc
{
  "id": "select-without-guc-returns-zero",
  "description": "Querying a tenant table with no app.current_company_id GUC set must return zero rows (default-deny RLS), not all rows",
  "seed": { "companyA": 2, "companyB": 3 },
  "session": { "role": "tally_app", "setGuc": false },
  "action": "SELECT count(*) FROM voucher_version",
  "expect": { "rowCountIs": 0 }
}
```

## 4. `migration/` — Tally-import reconciliation (consumed by `backend` migration tests)

A fixture pair: a Tally XML export sample and the expected post-import trial balance. The importer must reproduce the trial balance **to the paisa**. (Phase 0 ships the format + a stub fixture; the real importer lands in Phase 1.)

```jsonc
{
  "id": "smoke-3-voucher-tb",
  "description": "Three vouchers imported from a Tally XML export must produce this exact trial balance",
  "tallyExportFile": "smoke-3-voucher.xml",
  "expectedTrialBalance": [
    { "ledger": "Cash", "debit": "5000.0000", "credit": "0.0000" },
    { "ledger": "Sales", "debit": "0.0000", "credit": "5000.0000" }
  ],
  "expectedTotals": { "debit": "5000.0000", "credit": "5000.0000", "balanced": true }
}
```

### Four-statement oracle (`<id>.expected.json`)

Phase 1 adds a richer oracle paired with each `<id>.xml`: besides the trial
balance it pins the **Balance Sheet, Profit & Loss, GSTR-1 rollups and bill-wise
Outstandings**, reconciled by `migration.ReconcileFull`. The pinned Go shape is
`migration.ExpectedReconciliation` (`backend/internal/migration/expected.go`).
Dispatch is on the presence of the `trialBalance` key; `validate.py` re-checks the
oracle's own arithmetic (SUM(Dr)==SUM(Cr); Assets==Liabilities; BS.netProfit ==
PL.netProfit; GSTR-1 taxTotals == sum of HSN rows) independently of any Go code.

```jsonc
{
  "id": "02-multistate-gst",
  "tallyExportFile": "02-multistate-gst.xml",
  "gstin": "29ZZZZZ0000Z1Z5", "stateCode": "29",
  "period": { "from": "2025-05-01", "to": "2025-05-31" },
  "taxLedgers": { "cgst": "Output CGST", "sgst": "Output SGST", "igst": "Output IGST" },
  "trialBalance": { "rows": [ /* {ledger,debit,credit} */ ], "totals": { "debit": "...", "credit": "...", "balanced": true } },
  "balanceSheet":  { "assetsTotal": "...", "liabilitiesTotal": "...", "netProfit": "...", "balanced": true },
  "profitAndLoss": { "incomeTotal": "...", "expenseTotal": "...", "netProfit": "..." },
  "gstr1": { "fp": "052025", "taxTotals": { "taxable": "...", "cgst": "...", "sgst": "...", "igst": "..." },
             "hsn": [ /* {hsn,rate,taxable,cgst,sgst,igst} */ ],
             "b2cs": [ /* {splyTy,pos,rate,...} */ ], "b2b": [ /* {ctin,...} */ ], "balanced": true },
  "outstandings": { "asOn": "2025-06-30", "receivables": [ /* {ledger,pending,bills[]} */ ] }
}
```

**The oracle is INDEPENDENT**: every expected number is derived by hand from the
input vouchers (double-entry + GST slab arithmetic) and shown in the `description`
field — NEVER by dumping importer output. The five datasets are:

| id | proves |
| --- | --- |
| `01-opening-trading` | opening balances fold into TB/BS; BS.netProfit == PL.netProfit (no GST) |
| `02-multistate-gst`  | GSTR-1 B2B/B2CS/HSN + TaxTotals tie to output-tax postings; intra(CGST/SGST) vs inter(IGST) via the WS3 company-state default |
| `03-billwise-outstandings` | per-party + per-bill Pending/ageing; debtor TB == sum of open bills (needs `<BILLALLOCATIONS.LIST>` import) |
| `04-edited-deleted`  | GUID idempotency (re-import = no-op); ALTER current-version-wins; DELETE contributes zero (needs ACTION=Alter/Delete import) |
| `05-rounding-edge`   | paisa-exact at a half-paisa rounding boundary; imported tax must match the engine (`deriveAndVerifyGST`) |
