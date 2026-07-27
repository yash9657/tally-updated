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

## 5. `inventory/` — Stock valuation (consumed by `backend/internal/inventory`)

**Stock value is never stored.** No column holds a derived stock value or a valuation rate: value is derived at report time by replaying the movement ledger through a pure valuer (`inventory.Replay` / `inventory.ReplayAll`). These fixtures are the shared oracle for that replay — `TestWeightedAverageCorpus` on the Go side, and an **independent Python re-derivation** in `validate.py` (`_inv_replay`) that re-computes every expected number from the movements, so a wrong golden value fails CI even if the Go valuer agreed with it.

A position is `(stockItem, godown, batch)`; a `null` dimension is the SQL NULL and is a *different* position from a named one (`IS NOT DISTINCT FROM` grouping). `qty` is **always positive** — `direction` (`IN`/`OUT`, the `stock_direction` enum) carries the sign. Replay order is `(voucherDate, voucherNo, versionCreatedAt, lineNo, versionId)` and the *replayer applies it itself*, so a caller cannot change the answer by reading rows in a different order.

```jsonc
{
  "id": "02-rate-change-reprices-closing",
  "description": "... the hand derivation, shown in full — a fixture that does not show its arithmetic cannot be reviewed ...",
  "valuationMethod": "WEIGHTED_AVG",
  "godowns": ["main-location"],                                  // optional; membership is checked
  "batches": [{ "id": "b-2609", "stockItem": "vaccine", "expiryDate": "2026-09-30" }], // optional
  "openings": [{ "stockItem": "widget", "godown": null, "batch": null, "qty": "10.0000", "rate": "90.0000" }],
  "movements": [
    { "voucherDate": "2026-04-01", "voucherNo": "PUR-1", "versionCreatedAt": "2026-04-01T10:00:00+05:30",
      "versionId": "PUR-1/v1", "lineNo": 1, "stockItem": "widget", "godown": null, "batch": null,
      "direction": "IN", "qty": "10.0000", "rate": "100.0000", "discountPct": "0.0000" }
  ],
  "expected": { "positions": [
    { "stockItem": "widget", "godown": null, "batch": null,
      "openingQty": "...", "openingValue": "...", "inwardQty": "...", "inwardValue": "...",
      "outwardQty": "...", "outwardValue": "...",
      "closingQty": "...", "closingValue": "...", "closingRate": "..." } ] }
}
```

`outwardValue` is what the issues **consumed at cost**, not what they sold for; `openingValue + inwardValue - outwardValue == closingValue` holds for every position (`TestValueIsConserved`). `versionId` is an opaque token, used only as the final tiebreak that makes the replay order total.

| id | proves |
| --- | --- |
| `01-simple-in-out` | the base case: one purchase, one issue at the running average |
| `02-rate-change-reprices-closing` | a second purchase re-prices every unit on hand; landed cost is net of `discount_pct` |
| `03-backdated-inward-revalues-outward` | **why value is not materialised** — a purchase *dated before* an already-posted issue changes that issue's cost (1000.0000 → 750.0000) with the issue untouched. Carries a `backdatedInward` block holding the "before" world, which both oracles replay too |
| `04-per-godown-positions` | one item in two godowns is two positions with two costs; blending them changes the money |
| `05-batch-with-expiry` | batch is part of the position key; the expiry date is master metadata and never enters the arithmetic |
| `06-opening-folded-in` | `stock_opening` folds in as the first inward, and a recurring average (1400/15) is carried at 16 dp so only the *consumed value* is rounded to 4 dp |
