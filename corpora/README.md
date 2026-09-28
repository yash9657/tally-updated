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

### `06-tallyprime-real-shapes.xml` — the shapes a REAL export has

⛔ **Every other file in this directory was written by us, and that is exactly the
limitation this one exists to cover.** A hand-authored envelope proves the importer can
read what an integrator would SEND Tally; it proves nothing about what TallyPrime EMITS.
When a genuine export was finally obtained, seven defects surfaced at once — three of
them silent — and none of them were reachable from the five corpora above.

This fixture is that file's SHAPE, anonymised (invented parties, invented amounts),
byte-for-byte in the form Tally writes:

| In the fixture | Because a real export |
|---|---|
| **UTF-16LE with a BOM**, CRLF | is UTF-16, with no `<?xml?>` to say so |
| `&#4;` inside field values | uses U+0004 as a marker, which XML 1.0 forbids |
| `BODY > IMPORTDATA > REQUESTDESC / REQUESTDATA` | nests the payload one level deeper, under different names, than every published example |
| revenue only inside `ALLINVENTORYENTRIES.LIST > ACCOUNTINGALLOCATIONS.LIST` | keeps an item invoice's sales lines on the stock lines, not beside the party |
| a round-off whose `ISDEEMEDPOSITIVE` contradicts its `AMOUNT` sign | states the side twice and means the sign |
| `GROUP` masters, user-defined and built-in | carries the whole chart of accounts, and the user has added to it |
| `Cash-in-hand` against the seeded `Cash-in-Hand` | spells two of Tally's own groups with a lowercase h |
| an opening balance on `CASH` | names ledgers the company is already seeded with |
| `LEDGSTREGDETAILS.LIST` superseding a stale `PARTYGSTIN` | keeps a DATED registration history and lets the flat field go stale |
| the state as `Karnataka`, never as `29` | records states by NAME and never as a code |
| a bill-wise breakup that does not sum to its opening | is not always internally consistent |

It has **no `.expected.json`** on purpose — it is not a reconciliation corpus. It is
gated by `TestTallyPrimeShapesCorpus`, which asserts the specific consequence of each
row above, so a failure names the shape that regressed rather than a total that moved.

⛔ **The real file is NOT in this repository and must not be.** An export is a real
company's books — every counter-party, their GSTIN, what they owe. The end-to-end gate
against one is opt-in by path and skips without it:

```
CORED_TALLY_MASTERS_XML=…/Master.xml CORED_TALLY_VOUCHERS_XML=…/Transactions.xml \
  go test ./internal/migration/ -run RealTallyExport -v
```

Its oracle is not a fixture either: it recomputes the entire trial balance from the XML
by plain arithmetic and requires cored's own reporting to match, ledger by ledger, to the
paisa — so it cannot be quietly restated to match a regression.

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

## 6. `gstr2b/` — GSTR-2A/2B reconciliation (consumed by `backend/internal/compliance/itc`)

**The match result is never stored.** It is a function of four versioned inputs — the books, the ingested statement, the IMS action log and the policy — and goes stale the instant any of them moves, exactly like a stock valuation. These fixtures are therefore the only pinned form of that function, and `validate.py` re-derives every one of them from `input` alone through an **independent Python implementation of the matcher** (`_derive_gstr2b_expected`), written from the written specification rather than transcribed from the Go. A fixture whose `expected` is wrong fails here even if the Go agreed with it.

```jsonc
{
  "id": "01-exact-match",
  "description": "... what would be WRONG if this case were absent ...",
  "input": {
    "period": "012026",                  // MMYYYY, the GSTN `fp` form
    "claimDate": "2026-02-20",           // optional; only then is our own s.16(4) test applied
    "asOn": "2026-02-18T10:00:00+05:30", // optional; required when a document carries imsActions
    "filedAt": null,                     // the GSTR-3B freeze instant, if filed
    "books": [{
      "voucherId": "v1", "voucherNo": "PUR-1", "voucherDate": "2026-01-10",
      "partyGstin": "27AAAAA0000A1Z5",
      "supplierInvoiceNo": "INV-001", "supplierInvoiceDate": "2026-01-10",
      "docType": "I",                    // I invoice | C credit note | D debit note
      "pos": "27", "reverseCharge": false,
      "itcEligibility": "ELIGIBLE",      // ELIGIBLE | BLOCKED_17_5 | CAPITAL_GOODS | ISD
      "taxableValue": "10000.0000", "cgst": "900.0000", "sgst": "900.0000", "igst": "0.0000"
    }],
    "statement": {
      "kind": "GSTR2B", "returnPeriod": "012026", "genDate": "2026-02-14",
      "documents": [{
        "docId": "s1", "section": "B2B", "rejected": false,
        "supplierGstin": "27AAAAA0000A1Z5", "supplierFiledDate": "2026-02-09",
        "docNo": "INV-001", "docDate": "2026-01-10", "docType": "I",
        "docValue": "11800.0000", "pos": "27", "reverseCharge": false,
        "itcAvailable": true,            // GSTN's own itcavl verdict — required, never inferred
        "taxableValue": "10000.0000", "cgst": "900.0000", "sgst": "900.0000", "igst": "0.0000",
        "origDocNo": null, "origDocDate": null,     // B2BA/CDNRA only
        "imsActions": [{ "action": "REJECT", "actedAt": "2026-02-16T09:00:00+05:30" }]
      }]
    }
  },
  "expected": {
    "rejectedStatementRows": ["s1"],     // docRejdata rows: VIEW-ONLY, never in matching or ITC
    "findings": [{ "code": "DOC_NO_INVALID", "ref": "v1", "docNo": "...", "strictKey": "..." }],
    "lines": [{
      "status": "MATCHED", "fields": [], "notComparable": "",
      "books": "v1", "statement": "s1", "candidates": [],
      "delta": { "txval": "0.0000", "camt": "0.0000", "samt": "0.0000", "iamt": "0.0000" },
      "effect": { "avlRow": "OTH", "avl": {...}, "revRul": {...}, "revOth": {...}, "inelg": {...} },
      "s164Expiry": "2026-11-30"
    }],
    "totals": { "itcAvlIsrc": {...}, "itcAvlOth": {...}, "itcRevRul": {...},
                "itcRevOth": {...}, "itcInelgOth": {...}, "itcNet": {...},
                "postedInputTax": {...} }
  }
}
```

`camt`/`samt`/`iamt` are the GSTN key names for CGST/SGST/IGST already used by `gstr.Table4Row`. **Cess is not modelled** anywhere in this family — the 3B builder emits the `cess-not-modelled` disclosure. `totals` is deliberately a fixed set of zero-filled objects rather than the 3B arrays: mapping them onto `itc_avl` / `itc_rev` / `itc_inelg` rows (and the emit-only-if-non-empty rule) is the return builder's job, not the corpus's.

**Lines are sorted by `(books ?? "~", statement ?? "~")`** so paired and books-only lines come first in voucher order and statement-only lines follow in document order. The order is part of the contract; a matcher whose output depends on database row order is not reproducible.

### 6.1 The rules, and the citation grade of each

| rule | grade |
| --- | --- |
| Match key is (supplier GSTIN, normalised doc no, **doc type**, doc date, taxable value) | design decision |
| Taxable value EXACT; per-head tax tolerance `1.00` absolute, only once taxable is exact, reported as the distinct `MATCHED_ROUNDING` | design decision, derived from per-line rounding in `compliance.TaxDerive` |
| Date tolerance ZERO days | forced by s.16(4) — a 7-day band crosses a financial year |
| The 2B window is on the supplier's **filing** date, not the document date | [CITED-PRIMARY] GSTN GSTR-2B Advisory, 12-10-2021, Q.4 |
| 2B generation begins after "13th midnight" → the half-open `[.., 14th 00:00 IST)` boundary | [CITED-PRIMARY] same advisory, Q.2 |
| 2B is recomputable without limit; recomputation is mandatory when an action changed | [CITED-PRIMARY] GSTN IMS FAQ 27 + advisory key-point 2 |
| The action set freezes when GSTR-3B is filed | [CITED-PRIMARY] GSTN IMS FAQ 12 |
| `docRejdata` rows are view-only | [CITED-PRIMARY] GSTN IMS FAQ 24 |
| RCM / IMPG / ISD bypass IMS and still reach 2B | [CITED-PRIMARY] GSTN IMS FAQ 8 and 18 |
| In the same statement only the AMENDED record counts; for credit notes the amendment always prevails | [CITED-PRIMARY] 2B advisory key-point 7 / IMS FAQ 13 and 14 |
| GSTN marks `itcavl="N"` for s.16(4) and the PoS rule ONLY, and expressly disclaims the rest — **2B does not flag s.17(5)** | [CITED-PRIMARY] 2B advisory |
| Table 4 shape: 4(D) is "Other Details" and is **not** subtracted from 4(C); s.17(5) is reversed through 4(B)(1) | [CITED-PRIMARY] Notification 14/2022 + Circular 170/02/2022 |
| Invoice number capped at 16 characters | [CITED-PRIMARY] rule 46(b) |
| The 2B JSON key names and section codes used here | [CITED-SECONDARY] — public documentation, **not** verified against the GSTN offline utility, which is not in this repo |
| The s.17(5) clause letters carried in `itcBlockedClause` | [CITED-SECONDARY] free text on purpose, so re-verification is a doc change and not a migration |
| Every date boundary is evaluated in IST | **INFERRED, NOT CITED** — no GSTN or CBIC document read for this work states a timezone |

### 6.2 What the corpus deliberately does NOT gate

Rate-level (per-HSN, per-rate) matching is **out of scope**: `voucher_gst_line` carries line-grain detail and a 2B document may nest `itms[]`, so a multi-rate invoice whose per-rate split is wrong still passes an invoice-level comparison. Rule 42/43 apportionment is caller-supplied and unexercised here — `E` (exempt supply value) and `F` (total turnover in the state) are not derivable from our own return, and an engine fed `E=0` returns `D1=0` for every taxpayer, which is a confidently wrong zero. The books-side disagreement finding (we say time-barred, the statement says available) is implemented on both sides but has no fixture.

| id | proves |
| --- | --- |
| `01-exact-match` | the happy path; without it, reversing everything passes every other case |
| `02-rounding-within-tolerance` | 50 paisa is `MATCHED_ROUNDING` carrying the delta, and 4(A) takes the BOOKS figure |
| `03-value-mismatch` | the differing fields are NAMED and only the EXCESS is reversed; also raises `DOC_NO_INVALID` |
| `04-date-mismatch-fy-boundary` | 31-Mar vs 01-Apr is two s.16(4) expiries — why the date tolerance is zero |
| `05-leading-zero-near-match` | `INV-001` vs `INV1` is TWO unpaired lines, never a pair |
| `06-ambiguous-duplicate-number` | more than one candidate pairs NOTHING; all participants go `AMBIGUOUS` |
| `07-missing-in-2b` | the haircut is a 4(B)(2) line and (I1) stays exact |
| `08-missing-in-books` | a statement-only document contributes NOTHING to 4(A) |
| `09-credit-note-not-paired-to-invoice` | doc type is in the key: a CDNR is never paired with the invoice it adjusts |
| `10-rcm-never-reversed` | RCM is `NOT_COMPARABLE`, lands in ISRC, and is never reversed for being unmatched |
| `11-rejected-docs-excluded` | GSTN's `docRejdata` and our own IMS REJECT are different things with different effects |
| `12-prior-fy-invoice-in-current-2b` | the window is on the supplier's filing date |
| `13-amendment-supersedes-original` | the original contributes nothing and is not an ambiguous duplicate |
| `14-blocked-17-5` | s.17(5) is a classification input, reversed once, in 4(B)(1) |
| `15-time-barred-16-4` | never-available ITC is EXCLUDED from 4(A) and reported in 4(D)(2), keeping (I1) exact |
| `16-legacy-no-supplier-doc-no` | `NOT_COMPARABLE` is not `MISSING_IN_2B`, and produces zero reversal |

## 7. `ims/` — the IMS clock, scope and s.16(4) (consumed by `backend/internal/compliance/itc`)

Each case carries a `kind` that selects the derivation: `clock`, `pending`, `applicability`, `annexure`, `s164`. `validate.py` re-derives all five with `datetime` and `timezone(timedelta(hours=5, minutes=30))` — genuinely independent arithmetic, not a restatement.

```jsonc
{ "id": "01-deemed-accept-boundary-jan-2026", "kind": "clock", "description": "...",
  "input": { "period": "012026", "statementGenDate": null, "filedAt": null,
             "actions": [{ "action": "ACCEPT", "actedAt": "2026-02-10T09:00:00+05:30" }],
             "probes": [{ "asOn": "2026-02-13T23:59:59.999999999+05:30" }] },
  "expected": { "cutoff": "2026-02-14T00:00:00+05:30",
                "resolution": "2026-02-14T00:00:00+05:30",
                "states": ["NO_ACTION"] } }
```

**Two instants, not one.** `resolution` is when the 2B is generated — no action resolves to `DEEMED_ACCEPTED` from that instant. `freeze` (`filedAt`) is when GSTR-3B was filed; after it no action counts. Between them the 2B is recomputable without limit. There are **five** states — `NO_ACTION` is distinct from `ACCEPTED` until generation resolves it — and `RESET` clears the log back to no-action so the timer applies again.

`asOn` values are written at nanosecond precision to match the Go boundary test verbatim. Python truncates them to microseconds, which is safe **only** because every boundary here falls on a whole second: a fixture whose answer turned on a sub-microsecond difference would be untestable from Python and must not be written.

| id | proves |
| --- | --- |
| `01-deemed-accept-boundary-jan-2026` | the boundary instant before and after, in IST **and** in UTC — the pair that bites a local-zone clock |
| `02-february-short-month` | calendar-month addition, not "+30 days" |
| `03-december-year-rollover` | the month increment carries the year |
| `04-generation-slipped-past-the-14th` | Dec-2024 generated on 16-Jan-2025 — the case that bites a hard-coded 14 |
| `05-explicit-reject-beats-deemed-accept` | acting on the 15th still counts |
| `06-action-after-filing-is-ignored` | the freeze instant makes a filed return reproducible |
| `07-latest-action-wins` | last-write-wins over an append-only log, evaluated as of an instant |
| `08-pending-forbidden-pre-oct-2025` | the four restricted categories, [CITED-PRIMARY] |
| `09-pending-allowed-from-oct-2025` | effective-dated relaxation with a one-period expiry, [CITED-SECONDARY] **VERIFY BEFORE RELEASE** |
| `10-ims-not-applicable` | IMPG/IMPGSEZ/ISD/ISDA and every reverse-charge row bypass IMS |
| `11-annexure-a-decision-table` | GSTN's decision table in full, carrying GSTN's own "not a statement of law" disclaimer |
| `12-s164-expiry-boundaries` | 31-Mar vs 01-Apr, 30-Nov vs 01-Dec (inclusive), a de-linked debit note, an early annual return |

The Annexure-A table in `validate.py` is **reconstructed from the behaviour the IMS advisory and FAQ describe**, not transcribed from the annexure PDF, which is not in this repository. It is `[CITED-SECONDARY]` and marked VERIFY BEFORE RELEASE.

## 8. `_poison/` — the poisoned-fixture tripwire

`gstr2b/` and `ims/` each carry a `_poison/` subdirectory. Every file in it is a **valid-shaped case whose `expected` block is deliberately wrong** — a near-match recorded as a match, a `docRejdata` row summed into table 4(A), a credit note paired with the invoice it adjusts, a boundary off by one instant, a UTC instant answered differently from the identical IST instant, a resolution hard-coded to the 14th. `validate.py`'s `check_poison()` walks these directories explicitly and **fails if any poisoned fixture validates**, with the message:

> `poisoned fixture <name> validated — the oracle has become tautological; validate.py is restating the fixture instead of re-deriving it`

**This is a test OF THE VALIDATOR, not of the data.** It exists because it has already happened once: a corpus oracle was "fixed" by editing it to agree with the mapper it was supposed to check, after which the metric agreed with itself forever and detected nothing. The moment a `_derive_*` function is rewritten to `return case["expected"]`, or a comparison is relaxed until it stops complaining, every poison case starts passing and CI goes red naming all of them.

The **leading underscore is load-bearing**: `directory.glob("*.json")` in `main()`'s loop does not recurse, so poison files are invisible to the family counts and to the Go corpus loader. The counts printed in the summary are therefore honest, and `MIN_COUNTS` for these two families is set to the **exact** number of real fixtures (16 and 12) so that deleting any one of them fails CI rather than quietly shrinking the gate.

---

## 8. `payroll/` — Payslip arithmetic (consumed by `backend/internal/payroll` and by `validate.py`)

**People get paid from this.** A wrong number here is somebody's short salary or
somebody's tax notice, so this family is checked harder than any other.

Four computations share one directory and are dispatched **on the id prefix** —
there is no `kind` discriminator, because a discriminator can disagree with the
filename and then two validators pick different code paths for the same file:

| prefix | engine | what it pins |
| --- | --- | --- |
| `wages-` | `StatutoryWages` | Code on Social Security 2020 s.2(88), including the **50% deeming add-back**. This leaf is UPSTREAM of both PF and ESI, so an error in it is systematic across a whole workforce |
| `pf-` | `PFDerive` | EPF / EPS / EDLI. **EPF wages, EPS wages and EDLI wages are three different numbers** with three ECR columns and are never collapsed |
| `esi-` | `ESICoverage` + `ESIDerive` | the contribution-period **coverage latch**, rule 52's one-sided low-wage exemption, rule 51's ceiling on **both** shares |
| `it-` | `IncomeTaxDerive` | s.392 (ex-192) on an **annual** base — no per-transaction threshold exists anywhere in it |

```jsonc
{
  "id": "it-new-regime-rebate-cliff-marginal-relief",   // == the filename stem
  "description": "… the statutory citation AND the hand derivation, in full …",
  "rulesetVersion": "it-ty-2026-27",                    // or "ss-2026-07" / "ss-2019-07"
  "input":    { /* per-prefix; every extra key is a validation FAILURE */ },
  "expected": { /* money as 4-dp decimal STRINGS, rates as bare decimal strings */ },
  "counterfactual": {                                   // five fixtures carry one
    "rule": "no-rebate-marginal-relief",
    "why":  "… the CONSEQUENCE of the naive rule, in rupees …"
  }
}
```

A `description` must be **≥ 40 characters and carry both the citation and the
derivation**, because a payroll fixture is simultaneously a legal claim and a
computation, and neither half is reviewable without the other.

### Why this family is not tautological

Three independent mechanisms, all of them required:

1. **Two derivations that meet only at the fixture.** `validate.py`'s
   `derive_payroll` is written from the citations in the fixtures — it never
   reads Go source, never imports a shared rate file and never shells out to the
   backend. `TestPayrollDerive_GoldenCorpus` compares the **engine** to
   `expected`. Neither side ever sees the other's answer. The module-level
   tables (`PF_RATES`, `IT_SLABS_NEW`, `SURCHARGE_BANDS`, …) are duplicated
   **on purpose** and must never be "de-duplicated" into a shared data file: the
   duplication *is* the oracle. It cannot catch a rate that is wrong in **both**
   places — that is what the pre-release statutory spot-check is for.
2. **Counterfactuals.** Five fixtures re-derive under the **naive** rule and
   fail if it **agrees** with `expected`, so a fixture that has stopped
   demonstrating its claim is detected rather than trusted:
   `esi-monthly-ceiling-test`, `ignore-prior-employer-taxable`,
   `no-rebate-marginal-relief`, `pf-base-actual-with-employer-matching`,
   `round-income-to-rupee`.
3. **Closure in both directions**, in the Go test: every shipped ruleset version
   must be cited by a fixture; every slab band in each regime must be reached
   into by one; and every `ConfUnverified` rate must refuse to be consumed, with
   the engine's deferral set matching what the fixtures declare — so a rate added
   to `rates.go` without a fixture fails the build.

Money is compared as **decimals, not strings**: `money()` here is a *max*-4-dp
check, so `"1234"` is a legal fixture value while Go renders `"1234.0000"`, and a
string comparison would reject a correct fixture.

### The fourteen fixtures

| id | proves |
| --- | --- |
| `wages-fifty-percent-deeming-adds-back` | excluded pay over half of all remuneration is deemed back into wages; without it every PF and ESI figure downstream is understated |
| `pf-at-ceiling-statutory-only` | 12% / 8.33% at the 15,000 ceiling: 1800, EPS 1250, employer residual 550 |
| `pf-above-ceiling-base-ceiling` | para 26A(2): a 40,000 wage gives the **identical** answer — the cap decides, not the wage |
| `pf-above-ceiling-voluntary-employee-only` | para 26(6): the member may pay more and the employer need not match |
| `pf-eps-excluded-new-member-above-ceiling` | para 6(a): a post-01-09-2014 member above the ceiling was never in the pension scheme, so the whole 12% stays in EPF |
| `pf-edli-contribution-refused-unnotified` | the EDLI Scheme 2026 rate is "to be notified separately" — the engine emits a **deferral**, not the superseded 1976 figure |
| `esi-covered-mid-period-raise-continues` | s.2(9): coverage **latches** at the period start and survives a mid-period raise |
| `esi-above-ceiling-at-period-start-stays-out` | the mirror — and it cites the **pre-Code** ruleset, proving an old wage month resolves to the law in force then |
| `esi-low-wage-employee-share-exempt` | rule 52 exempts the **employee's** share only; the employer still pays |
| `esi-rounding-both-shares-up` | rule 51 rounds **both** shares to the next higher rupee — the only ceiling rounding in the codebase |
| `it-new-regime-rebate-cliff-marginal-relief` | the s.156 rebate-cliff relief engines omit; without it everyone from 12,00,000 to ~12,70,589 is over-taxed |
| `it-new-regime-surcharge-fifty-lakh-marginal-relief` | the classic surcharge relief, computed against the tax **at** the threshold |
| `it-mid-year-joiner-prior-employer-aggregated` | ex-s.192(2) / Form 12B: the new employer deducts on the **aggregate**; this case also exercises the old regime and Chapter VI-A |
| `it-income-rounds-to-nearest-ten` | the **two** roundings with **different units** — total income to the nearest ten, tax to the nearest rupee |

**Not validator-verified.** Nothing in this family has been through the
NSDL/Protean FVU, the EPFO Unified Portal or the ESIC employer portal; none of
them can run in this environment. Every rate was curated from secondary sources.
The corpus is a **substitute** for validation, not evidence of it — see the RATE
PROVENANCE banner in `backend/internal/payroll/payroll.go`.

## 9. `tau/`, `extraction/`, `embedding/` — the W1a agent golden sets (d124)

⛔ **ALL THREE ARE SYNTHETIC AND GENERATED.** Each has a `generate.py` beside it;
`validate.py`'s `check_generator_drift` fails if a committed file differs from
what its generator emits, so the way to change a fixture is to change the
generator. Every file declares `"synthetic": true` and a `SYNTHETIC` provenance
line, and every extraction document says so in its **first line of text**.
Nothing in them came from a customer's books. Design and figures:
`docs/design/d124-w1a-golden-sets.md`.

| family | count (printed by `validate.py`) | consumed by | re-derived here |
|---|---|---|---|
| `tau/` | 200 task files | `backend/internal/evals/tau` (Go harness, pass^k) | balance, GST legs via `derive_tax`, every question's figure from the expected books, GSTIN checksums, script vs language |
| `extraction/` | 150 documents (100 invoices, 50 statements) | `insightd/insightd/evals/extraction.py` (field-level F1) | every header label GROUNDED in the text via its evidence surface; invoice arithmetic and tax; statement running balance |
| `embedding/` | 2,342 judged rows (1,278 narrations, 218 queries) | `insightd/insightd/evals/retrieval.py` (recall@k, MRR, nDCG, triplet) | every grade re-derived from intent/key metadata; no relevant document left unjudged |

Each family has a `_poison/` directory (see §8) and a `make_poison.py` that
writes it.
