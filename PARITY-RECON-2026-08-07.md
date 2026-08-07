# PARITY AUDIT — READ-ONLY RECON OUTPUT, 2026-08-07

> Produced by an 18-agent read-only workflow (2.6M subagent tokens, 0 errors) during
> the 2026-08-07 audit session, covering the rows that had never been driven.
>
> ## ⛔ EVERY ENTRY BELOW IS A PREDICTION, NOT A MEASUREMENT.
> Predictions are traced to file:line but were produced by READING. This repo's
> standing rule is that a row flips on BROWSER evidence only, and it holds here.
>
> **Known hit rate so far:** four of these were driven in a browser the same day.
> Three were CONFIRMED exactly (the voucher Date being unkeyable, the item-invoice
> alter dropping stock, the item-grid godown default) and one was KILLED (the claim
> that a no-op alter destroys stock on *every* path — the accounting path preserves
> it). Treat the rest as leads with a good but imperfect record.
>
> Where a row has since been driven, `HANDOFF-PARITY-AUDIT.md` §7's log is the
> authority and this file is history.

## ROW 3 — Bill-wise details — all FOUR reference types (New Ref / Agst Ref / Advance / On Account)

**Reachable from the keyboard today:** yes, with one important caveat. All four ref types ARE offered and ARE keyable: BillAllocationPicker.tsx:45-51 builds the `<select>` from every key of REF_TYPE_LABEL, so NEW/AGAINST/ADVANCE/ON_ACCOUNT are all selectable on both panels (raise: VoucherScreen.tsx:1857 on Sales/Purchase; settle: VoucherScreen.tsx:2325 per single-entry particular, and DoubleEntryPanel.tsx:131). The Alt-key-on-macOS defect is fixed — normalize.ts:113-122 falls back to `ev.code` (`/^Key([A-Z])$/`) whenever altKey is set — so Alt+G / Alt+F2 / Alt+P are live. THE CAVEAT: the panel renders ONLY when the chosen ledger has maintain_bill_by_bill=true (VoucherScreen.tsx:1855, 2322); all five Demo Traders parties have it set, so this is fine here but a browser-created party will NOT get the panel (see Row 5). AND: the Outstandings SCREEN cannot be used as the oracle — ReportScreens.tsx:558 emits party rows only (no `bills` are ever rendered), and ReportScreens.tsx:550-552 never sends `kind`, so outstandingKindParam (handler_print_reports.go:568-570) defaults to RECEIVABLE and the PAYABLE side (Delhi Parts, Steel Suppliers) is unreachable on screen entirely.

**Keystrokes**

```
Setup for the collision (Demo Traders, company 1dcc0335-e1e4-442f-bafb-0aa48993488c; counters today: Purchase next_no=3, Payment next_no=3, Sales next_no=4, Receipt next_no=2).

A) TWO NEW REFS ON ONE INVOICE (single voucher, most reachable):
  F8  → Sales form. Party field: type `Acme Traders`, Enter (maintain_bill_by_bill=t, so the "Bill-wise references" panel renders — data-testid=`bill-refs-panel`).
  Key the invoice value 11800 (GST accounting mode or plain, either).
  In the panel: the auto NEW ref already carries 11800.0000. Set `billrefs-amount:0` = 5900.0000, `billrefs-due:0` = 2026-09-06.
  Click/Tab to `billrefs-add` (+ reference) → a second NEW row appears. `billrefs-rest:1` (gives it 5900.0000), `billrefs-due:1` = 2026-10-06.
  Remainder must read "referenced in full (11800.0000)". Ctrl+A to accept.

B) ON ACCOUNT / ADVANCE COLLIDING ACROSS TWO SERIES (two vouchers):
  F9 → Purchase. Party `Delhi Parts` (Sundry Creditors, bill-by-bill=t). Value 10000. Leave the defaulted NEW ref at 10000.0000. Ctrl+A. → this voucher gets voucher_no "3" (Purchase counter is at 3).
  F5 → Payment (single entry). Party/bank field = `Bank`. Particular row 1 ledger = `Delhi Parts`, amount 4000. The per-row picker renders. `billrefs-add` → a row defaulted to AGAINST; change `billrefs-type:0` to **On Account** (the select clears refName). `billrefs-rest:0` → 4000.0000. Ctrl+A. → this voucher ALSO gets voucher_no "3" (Payment counter is independently at 3).
  Repeat with **Advance** instead of On Account for the ADVANCE half of the row.

C) AS-ON RECONCILIATION:
  Alt+G → type `Outstandings` → Enter. Alt+F2 → set To = 2026-04-30 → Enter. Read the Total.
  Alt+G → `Balance Sheet` → Enter, Alt+F2 → To = 2026-04-30 → read Sundry Debtors.
```

**Oracle**

```
PRIMARY (psql, sides + names + value — never the total):

-- A) two NEW refs on one invoice: are they two distinct bills?
SELECT ba.ref_name, ba.ref_type, ba.dr_cr, ba.amount, ba.due_date, vv.voucher_no
  FROM bill_allocation ba
  JOIN voucher_version vv ON vv.id = ba.voucher_version_id
  JOIN voucher_head vh ON vh.current_version_id = vv.id AND vh.is_deleted = false
  JOIN ledger l ON l.id = ba.ledger_id
 WHERE l.name = 'Acme Traders' AND ba.company_id = '1dcc0335-e1e4-442f-bafb-0aa48993488c'
 ORDER BY ba.created_at;
CORRECT: two rows with DIFFERENT ref_name (Tally: "4" and "4-1", or the operator's own names), each D 5900.0000, due 2026-09-06 and 2026-10-06.
DEFECT: two rows with the SAME ref_name (both the bare voucher number, e.g. '4'), D 5900 each.

-- and what the report then makes of them (this is reporting.Outstandings' own GROUP BY):
SELECT l.name, ba.ref_name,
       SUM(CASE WHEN ba.dr_cr='D' THEN ba.amount ELSE -ba.amount END) AS pending,
       MIN(ba.due_date) AS due
  FROM (SELECT b.ledger_id,b.ref_name,b.dr_cr,b.amount,b.due_date FROM bill_allocation b
          JOIN voucher_version vv ON vv.id=b.voucher_version_id
          JOIN voucher_head vh ON vh.company_id=vv.company_id AND vh.voucher_id=vv.voucher_id AND vh.current_version_id=vv.id
         WHERE vh.is_deleted=false AND b.company_id='1dcc0335-e1e4-442f-bafb-0aa48993488c'
        UNION ALL
        SELECT ob.ledger_id,ob.ref_name,ob.dr_cr,ob.amount,ob.due_date FROM ledger_opening_bill ob
         WHERE ob.company_id='1dcc0335-e1e4-442f-bafb-0aa48993488c') ba
  JOIN ledger l ON l.id=ba.ledger_id
 GROUP BY 1,2 ORDER BY 1,2;
CORRECT: two bill rows of 5900.0000 with due 2026-09-06 and 2026-10-06.
DEFECT: ONE bill row of 11800.0000 with due 2026-09-06 — the second instalment's ageing is gone.

-- B) On Account / Advance colliding with the purchase's own bill:
Run the same grouped query filtered to l.name='Delhi Parts'.
CORRECT: bill '3' (or 'PUR-3') C 10000.0000 STILL FULLY OUTSTANDING, plus a SEPARATE On Account row of D 4000.0000 (Tally shows it as an unallocated "On Account" line, never merged into a bill).
DEFECT: a single row ref_name='3' with pending 6000.0000 — the on-account payment has silently settled a purchase invoice it was never applied to. Confirm the two ref_names are literally equal:
SELECT ba.ref_name, ba.ref_type, ba.dr_cr, ba.amount, vt.name AS vtype, vv.voucher_no
  FROM bill_allocation ba JOIN voucher_version vv ON vv.id=ba.voucher_version_id
  JOIN voucher_type vt ON vt.id=vv.voucher_type_id JOIN ledger l ON l.id=ba.ledger_id
  JOIN voucher_head vh ON vh.current_version_id=vv.id AND vh.is_deleted=false
 WHERE l.name='Delhi Parts';
→ two rows, vtype 'Purchase' and 'Payment', both ref_name '3'.

-- C) Outstandings vs Balance Sheet at a mid-year as-on:
Sundry Debtors as at 2026-04-30 (what the Balance Sheet computes):
SELECT l.name, l.opening_balance, l.opening_dc,
       COALESCE(SUM(CASE WHEN p.dr_cr='D' THEN p.amount ELSE -p.amount END)
                FILTER (WHERE vv.voucher_date <= DATE '2026-04-30'),0) AS mvmt
  FROM ledger l JOIN account_group g ON g.id=l.group_id
  LEFT JOIN posting p ON p.ledger_id=l.id
  LEFT JOIN voucher_version vv ON vv.id=p.voucher_version_id
  LEFT JOIN voucher_head vh ON vh.current_version_id=vv.id AND vh.is_deleted=false
 WHERE l.company_id='1dcc0335-e1e4-442f-bafb-0aa48993488c' AND g.name='Sundry Debtors'
 GROUP BY 1,2,3;
MEASURED NOW: Acme 120000 opening + 59000 movement, Mumbai Metals 0 → Sundry Debtors = 179,000.00.
MEASURED NOW from the Outstandings query above (ASSET nature): INV-001 24,000 + INV-003 29,680 + OB/ACME/07 70,000 + OB/ACME/11 50,000 + Mumbai Metals INV-002 80,240 = 253,920.00.
CORRECT: the two agree to the paisa at 2026-04-30 (179,000.00 — INV-003 is dated 2026-06-19 and the Mumbai credit note 2026-07-08, both AFTER the as-on).
DEFECT: Outstandings screen `os-total` reads 253,920.00 against a Balance Sheet Sundry Debtors of 179,000.00 — 74,920.00 apart, and the "As on 2026-04-30" caption underneath asserts they are the same date.

SECOND-BEST ORACLE (report, not screen): Alt+P on Outstandings → the printed ageing (handler_print_reports.go, shares outstandingKindParam) is the only surface that renders the per-BILL breakdown; the on-screen report renders party rows only.
```

**Predictions**

- **[high]** Two NEW references on one invoice collapse into a SINGLE bill, because post() stamps the same voucher number on every unnamed reference and Outstandings groups by (ledger_id, ref_name). The second instalment's due date is swallowed by MIN(due_date), so an 11,800 invoice split 5,900 @30d / 5,900 @60d ages entirely at the 30-day date.
  - *evidence:* backend/internal/ledger/ledger.go:1752-1759 — `for i := range stamped { if stamped[i].RefName == "" { stamped[i].RefName = in.VoucherNo } }` with NO uniqueness check and no suffixing; backend/internal/reporting/outstanding.go:158 `GROUP BY ba.ledger_id, l.name, g.name, ba.ref_name` and :141 `to_char(MIN(ba.due_date),'YYYY-MM-DD')`; frontend/src/screens/vouchers/BillAllocationPicker.tsx:294-299 — the `+ reference` button hands back `emptyBillRef("NEW")` in raise mode, i.e. a SECOND unnamed NEW ref, and billAllocation.ts:144-160 sends both with `ref_name: ""`. The DB already shows the bare-integer stamp in use: bill_allocation rows with ref_name '2' and '4' equal to voucher_no '2'/'4'.
  - *how to tell:* REAL: the two bill_allocation rows come back with an IDENTICAL ref_name (the bare voucher number) and the grouped query yields one row, pending 11800.0000, due = the earlier of the two dates. NOT REAL: two rows with distinct ref_names and two separate pendings of 5900.0000 with their own due dates. The paisa total is IDENTICAL either way — this defect is invisible to any total-based check, which is exactly why the oracle must read ref_name and due_date, not the sum.
- **[high]** ADVANCE and ON_ACCOUNT are stored as ordinary named bills, not as unallocated money, because they take the same unnamed→voucher-number stamp as NEW. Since every seeded voucher type has NULL prefix/suffix/width (bare integers) and each type has its OWN counter, an On-Account payment silently NETS AGAINST an unrelated invoice whose number happens to match. In Demo Traders the Purchase and Payment counters are BOTH at 3 right now, so this fires on the very next two vouchers.
  - *evidence:* backend/internal/ledger/ledger.go:1756 stamps in.VoucherNo on ADVANCE and ON_ACCOUNT identically to NEW (validateBillAllocations, ledger.go:2529-2531, exempts only AGAINST from the blank-name rule); backend/internal/ledger/numbering.go:123-137 allocateVoucherNo keys the counter on (company, voucher_type, fin_year) — a PER-TYPE series; numbering.go:167-176 formatVoucherNo emits the bare integer when prefix/suffix/width are unset, and `SELECT name,prefix,suffix,width FROM voucher_type` returns NULL for all three on every seeded type; reporting/outstanding.go:158 ignores ref_type entirely when grouping. billAllocation.ts:150 only skips a blank name for AGAINST, so ON_ACCOUNT/ADVANCE go out with ref_name "".
  - *how to tell:* REAL: after the Purchase then the Payment-On-Account, `SELECT ba.ref_name, vt.name FROM bill_allocation ...` returns TWO rows both with ref_name='3', one from vtype 'Purchase' (C 10000) and one from vtype 'Payment' (D 4000), and the grouped query shows ONE Delhi Parts bill of C 6000.0000. NOT REAL: the Payment's row carries a distinct name (or a NULL/sentinel that the report keeps separate) and Delhi Parts shows bill '3' at the full C 10000.0000 alongside a separate On Account line of D 4000.0000. Again the party TOTAL is 6000.0000 either way and the Balance Sheet ties — only the per-bill sides and names expose it.
- **[high]** Outstandings honours `as_on` for AGEING but not for INCLUSION: the query has no voucher_date predicate at all, so a bill raised after the as-on date is still reported. Setting the report period To = 2026-04-30 makes Outstandings show 253,920.00 against a Balance Sheet Sundry Debtors of 179,000.00 — 74,920.00 of receivables that do not exist yet on that date, under a caption that says "As on 2026-04-30".
  - *evidence:* backend/internal/reporting/outstanding.go:137-161 — the FROM clause joins bill_allocation → voucher_version → voucher_head with only `WHERE vh.is_deleted = false`; asOnTime is consumed solely by ageBill (outstanding.go:180) and echoed as out.AsOn. Contrast backend/internal/reporting/reporting.go:138-139 (`AND ($2::date IS NULL OR vv.voucher_date <= $2::date)`), which is what the Balance Sheet uses. frontend/src/screens/reports/ReportScreens.tsx:546-552 does send `as_on` from the F2/Alt+F2 period-To, so the operator has every reason to believe the two dates agree.
  - *how to tell:* REAL: with Alt+F2 To = 2026-04-30, `os-total` = 253,920.00 while the Balance Sheet at the same date shows Sundry Debtors 179,000.00; the offending rows are INV-003 (Sales dated 2026-06-19, 29,680) and Mumbai Metals INV-002 net of a Credit Note dated 2026-07-08. NOT REAL: `os-total` = 179,000.00, INV-003 absent, and Mumbai Metals showing the pre-credit-note 94,400.00. Both figures were measured against the live DB, so the CORRECT and DEFECT numbers are already pinned.
- **[high]** The Outstandings screen can never show the PAYABLE side, and never shows the per-bill breakdown, so no on-screen surface can be used to verify any of the four ref types on a supplier. Every bill-wise defect on Delhi Parts / Steel Suppliers / Pune Fabricators is invisible in the UI.
  - *evidence:* frontend/src/screens/reports/ReportScreens.tsx:549-553 builds `const query: { as_on?: string } = {}` — `kind` is never a member and never sent; backend/internal/server/handler_print_reports.go:568-570 `case "", "RECEIVABLE", "receivable": return reporting.Receivables`. ReportScreens.tsx:556-568 maps `data.parties` to party rows only; the `bills` array the handler emits (server.go:2698-2706) is never read anywhere in the screen (grep for `.bills` in ReportScreens.tsx returns nothing). navMachine.ts:78-79 aliases "bills payable" → the same "Outstandings" screen, so Alt+G "Bills Payable" lands on the receivables report.
  - *how to tell:* REAL: Alt+G → "Bills Payable" → Enter shows the same rows as "Outstandings" (Acme Traders / Mumbai Metals, ASSET-nature parties), Steel Suppliers and Delhi Parts absent, and pressing Enter on a party row does NOT expand to its references. NOT REAL: the payables alias renders Steel Suppliers 68,000 C and Delhi Parts, and a party row expands to its ref_name/pending/overdue lines.

## ROW 5 — Opening balances, incl. a bill-wise opening breakup on a party ledger created with Alt+C

**Reachable from the keyboard today:** NO for the headline case, and this is the finding. frontend/src/screens/masters/MasterCreateScreen.tsx:63-70 is the entire Alt+C ledger path and it sends exactly `{ name: n, group_name: p }` — no opening_balance, no opening_dc, no opening_bills, no maintain_bill_by_bill, even though CreateLedgerRequest (api/types.ts:488-495) declares all four and the server accepts them (server.go:1329-1360). MASTER_FIELDS (navMachine.ts:49-50) includes a third field called "details", but the component renders nothing for it — only a Name input and the group SelectionList (MasterCreateScreen.tsx:126-152). Worse, NO screen anywhere in frontend/src calls `client.setLedgerOpening` (declared at api/client.ts:132, routed at server.go:538) — a grep across frontend/src outside api/ for `setLedgerOpening` / `opening_bills` / `opening_balance` returns zero hits. So there is NO keyboard path to an opening balance, let alone a bill-wise opening breakup, on ANY ledger — new or seeded. The seeded opening bills (104 rows across companies) exist only because seed-demo wrote them via the API. Row 5's C-leg (the as-on reconciliation of the SEEDED opening bills) IS reachable and is where the drivable defect lives.

**Keystrokes**

```
A) THE ALT+C PATH ITSELF (this is the whole finding — try to key it and see there is nowhere to type it):
  F8 → Sales. Move the cursor to the Party field. Alt+C → the inline master-create frame pushes (navMachine.ts:1129-1133, screen "master-create", masterKind "ledger").
  Type Name = `New Debtor Co`. Enter (advances the MASTER_FIELDS cursor: name → group → details). Pick `Sundry Debtors` from the Under (group) SelectionList. Enter again → the cursor reaches the `details` field.
  Look for an Opening Balance field, a Dr/Cr toggle, and a bill-wise breakup sub-form. Ctrl+A to accept.

B) WHAT THE NEW LEDGER IS ACTUALLY WORTH:
  Alt+G → `Outstandings` → Enter. Alt+G → `Balance Sheet` → Enter.

C) THE OPENING BILLS THAT DO EXIST (seeded, so this half IS drivable):
  Alt+G → `Outstandings` → Enter, then Alt+F2 → To = 2026-03-31 (one day BEFORE company.books_beginning_from = 2026-04-01) → Enter. Read the total.
  Alt+G → `Balance Sheet` → Enter, Alt+F2 → To = 2026-03-31 → read Sundry Debtors / Sundry Creditors.
```

**Oracle**

```
PRIMARY (psql):

-- A) did Alt+C write ANY opening at all?
SELECT l.name, g.name AS grp, l.opening_balance, l.opening_dc, l.maintain_bill_by_bill,
       (SELECT count(*) FROM ledger_opening_bill ob WHERE ob.ledger_id = l.id) AS opening_bills
  FROM ledger l JOIN account_group g ON g.id = l.group_id
 WHERE l.name = 'New Debtor Co';
CORRECT: whatever opening balance and breakup the operator keyed, with opening_bills > 0 and SUM(ob.amount signed by dr_cr) = opening_balance signed by opening_dc.
DEFECT: one row, opening_balance 0.0000, opening_dc 'D', maintain_bill_by_bill = FALSE, opening_bills = 0 — the master-create form had no field for any of it.

-- and the knock-on: the party cannot even carry a bill afterwards
The bill-wise panel on a Sales to 'New Debtor Co' will not render at all, because VoucherScreen.tsx:1855 gates on maintain_bill_by_bill and CreateLedgerRequest.maintain_bill_by_bill is never sent either.

-- C) opening bills BEFORE books_beginning_from — Outstandings vs Balance Sheet:
SELECT ob.ref_name, ob.ref_type, ob.dr_cr, ob.amount, ob.due_date, l.name,
       l.opening_balance, l.opening_dc
  FROM ledger_opening_bill ob JOIN ledger l ON l.id = ob.ledger_id
 WHERE ob.company_id = '1dcc0335-e1e4-442f-bafb-0aa48993488c';
MEASURED NOW: Acme Traders OB/ACME/24-25/07 D 70,000 due 2026-04-20 and OB/ACME/24-25/11 D 50,000 due 2026-05-05 (opening 120,000 D); Steel Suppliers OB/STEEL/24-25/42 C 40,000 and OB/STEEL/24-25/55 C 28,000 (opening 68,000 C).
CORRECT at as-on 2026-03-31: Outstandings receivables = 0.00, matching a Balance Sheet that has dropped the opening (reporting.go:196-205 sets foldOpening=false when asOn < books_beginning_from = 2026-04-01, so Sundry Debtors reads 0.00).
DEFECT at as-on 2026-03-31: Outstandings still shows Acme Traders 120,000.00 (both OB refs, D side) against a Balance Sheet Sundry Debtors of 0.00.

-- B) the signed-vs-unsigned validator (API-level; the orchestrator can prove it with one curl, no UI needed):
PUT /v1/companies/{id}/ledgers/{ledgerID}/opening with
  {"opening_balance":"120000.0000","opening_dc":"D",
   "opening_bills":[{"ref_name":"A","dr_cr":"D","amount":"60000.0000"},
                    {"ref_name":"B","dr_cr":"C","amount":"60000.0000"}]}
CORRECT: 400/422 — the signed breakup nets to 0 D, not 120,000 D.
DEFECT: 200, and then
SELECT SUM(CASE WHEN dr_cr='D' THEN amount ELSE -amount END) FROM ledger_opening_bill WHERE ledger_id = '<id>';
  → 0.0000, while `SELECT opening_balance, opening_dc FROM ledger WHERE id='<id>'` → 120000.0000 D. Outstandings shows the party at 0.00, the Balance Sheet at 120,000.00.
The mirror case: the SAME body with {"A": D 150000, "B": C 30000} against opening_balance 120000 D is the ORDINARY Tally shape (an invoice plus an advance held) and CORRECT is 200 — DEFECT is a 400 saying "sums to 180000.0000 but the opening balance is 120000.0000".
```

**Predictions**

- **[high]** An Alt+C-created party ledger silently gets opening_balance 0, no opening bills, and maintain_bill_by_bill = false — so it not only misses its opening bill, it can never carry ANY bill reference, because the voucher form's bill-wise panel is gated on that flag. This is the same shape as the original 'no screen ever built a bill reference' defect, moved one level up into the master.
  - *evidence:* frontend/src/screens/masters/MasterCreateScreen.tsx:66-69 — `await client.createLedger(companyID, { name: n, group_name: p })`, the complete request; the type at frontend/src/api/types.ts:488-497 offers opening_balance / opening_dc / opening_bills / maintain_bill_by_bill and none are passed. frontend/src/keystroke/navMachine.ts:49-50 declares MASTER_FIELDS = ["name","group","details"] but MasterCreateScreen renders no "details" control. frontend/src/screens/VoucherScreen.tsx:1855 and :2322 both gate BillAllocationPicker on `maintain_bill_by_bill`. backend/internal/masters/masters.go:361-374 inserts maintain_bill_by_bill from l.MaintainBillByBill, which arrives false.
  - *how to tell:* REAL: after Ctrl+A, `SELECT opening_balance, opening_dc, maintain_bill_by_bill FROM ledger WHERE name='New Debtor Co'` → 0.0000 / D / false, and `SELECT count(*) FROM ledger_opening_bill WHERE ledger_id=...` → 0; then keying a Sales to that party renders NO element with data-testid `bill-refs-panel`. NOT REAL: the master-create frame shows an Opening Balance field and a breakup sub-form, and the row comes back with what was keyed.
- **[high]** The opening-bill breakup validator sums amounts UNSIGNED while the Outstandings report nets them SIGNED by dr_cr. It therefore (a) REFUSES the ordinary Tally case — an opening made of an invoice plus an advance held on account — and (b) ACCEPTS a D/C-cancelling breakup that makes Outstandings read 0.00 for a party the Balance Sheet carries at the full opening balance. The dr_cr column exists, is honoured by the report, and is ignored by the only check that guards it.
  - *evidence:* backend/internal/masters/masters.go:315-333 (CreateLedger) — `for i, b := range l.OpeningBills { ... sum = sum.Add(b.Amount) }` at :327 with `if !sum.Equal(l.OpeningBalance)` at :329; b.DrCr is validated for spelling at :323 but NEVER enters the sum. The identical bug is duplicated in SetLedgerOpening at masters.go:445-460 (:453 / :455), which does not even validate DrCr's spelling. The consumer nets by side: backend/internal/reporting/outstanding.go:117-120 `SUM(CASE WHEN ba.dr_cr='D' THEN ba.amount ELSE -ba.amount END)`. migration 0017 line 44 explicitly blesses ADVANCE opening bills ('ADVANCE covers money held on account at migration time') — i.e. exactly the opposite-side row the validator refuses.
  - *how to tell:* REAL: the PUT with {A: D 60000, B: C 60000} against opening 120000 D returns 200 and the signed SUM over ledger_opening_bill is 0.0000 while ledger.opening_balance is 120000.0000 D; AND the PUT with {A: D 150000, B: C 30000} against opening 120000 D returns 400 with the message 'sums to 180000.0000 but the opening balance is 120000.0000'. NOT REAL: the first is refused and the second is accepted. Note the seeded data cannot expose this — Acme's two opening bills are both D and Steel's are both C, so unsigned and signed happen to agree, which is precisely why it has survived.
- **[medium]** Outstandings folds in ledger_opening_bill unconditionally, with none of the books-beginning-from guard the Balance Sheet applies to ledger.opening_balance. Asked for a date before the books began, Outstandings reports the entire opening ageing while the Balance Sheet correctly reports nothing.
  - *evidence:* backend/internal/reporting/outstanding.go:151-155 — the `UNION ALL SELECT ob.ledger_id, ob.ref_name, ob.dr_cr, ob.amount, ob.due_date FROM ledger_opening_bill ob` arm carries no date predicate whatsoever. The Balance Sheet's side of the same figure DOES guard it: backend/internal/reporting/reporting.go:174-205, `if asOn, ok := to.(string); ok && asOn < begins { foldOpening = false }`, whose comment records that the live verifier already caught this exact class ('a Trial Balance dated 2020 showing 520000 of opening capital on a company whose books begin in 2026'). Demo Traders' books_beginning_from is 2026-04-01 and it carries 120,000 D of Acme opening bills and 68,000 C of Steel Suppliers opening bills.
  - *how to tell:* REAL: Alt+F2 To = 2026-03-31 on Outstandings shows Acme Traders 120,000.00 (refs OB/ACME/24-25/07 and /11) while the Balance Sheet at the same date shows Sundry Debtors 0.00. NOT REAL: Outstandings shows 0.00 at 2026-03-31 and both reports agree. Confidence is medium only because this compounds with the as-on-inclusion defect on Row 3 — if that one is fixed by adding a voucher_date filter without also date-guarding the opening arm, THIS one survives the fix and will read exactly 120,000.00 (Acme) / 68,000.00 (Steel) at 2026-03-31. Assert the ref_names, not the total: OB/ACME/24-25/07 and OB/ACME/24-25/11 appearing at all is the signal.

## ROW 20 — Stock items + units as masters (Alt+C) — can a unit still be omitted?

**Reachable from the keyboard today:** NO. `masterKind` is only ever produced with the literal "ledger" (keystroke/keymaps/tallyprime.ts:81 is the ONLY masterKind literal in the whole keymap). The Gateway menu (GatewayScreen.tsx:4-62) has no Masters/Stock Items/Units entry, and the Go To picker offers only ["Vouchers", ...REPORT_NAMES] (app/defaultScreens.tsx:57-63). MasterCreateScreen's "unit" and "stock-item" arms (lines 86-96) are dead code.

**Keystrokes**

```
From Gateway of Tally press Alt+C. Then F8 → Ctrl+H → "Item Invoice" → put focus in the Item select → Alt+C again. (Also: Alt+G and read the whole picker list; arrow through the whole Gateway menu.)
```

**Oracle**

```
Screen: read the <h1> of the pushed frame — it renders `Create {kind}` (MasterCreateScreen.tsx:130-132). Correct = "Create stock item" when Alt+C is pressed on an item field. DB oracle for whether anything was created: `SELECT si.name, COALESCE(u.symbol,'(none)') AS unit, si.valuation_method FROM stock_item si LEFT JOIN unit u ON u.id=si.unit_id WHERE si.company_id='<co>' ORDER BY si.name;` — run before and after. Correct = a new row with the unit the operator chose.
```

**Predictions**

- **[high]** Alt+C can NEVER open a stock-item or unit form. Pressed anywhere — including on the Item cell of the item-invoice grid — it pushes a LEDGER-create frame titled "Create ledger". There is no keystroke, Gateway entry or Go To entry in the build that reaches masterKind "stock-item" or "unit", so a company with no stock items can never be given one from the keyboard.
  - *evidence:* frontend/src/keystroke/keymaps/tallyprime.ts:81 `"Alt+C": { kind: "createMaster", masterKind: "ledger" }` (only masterKind literal in the file); frontend/src/screens/GatewayScreen.tsx:4-62 (no masters entry); frontend/src/app/defaultScreens.tsx:59 `options={["Vouchers", ...REPORT_NAMES]}`; frontend/src/screens/masters/MasterCreateScreen.tsx:86-96 (unreachable arms)
  - *how to tell:* If real: the pushed screen's heading reads "Create ledger" and its parent picker is LEDGER_PARENT_GROUPS; `SELECT count(*) FROM stock_item WHERE company_id='<co>'` is unchanged after Ctrl+A. If not real: the heading reads "Create stock item" and a new stock_item row appears.
- **[high]** Even if the stock-item form were reached, it CANNOT set a unit — it silently hard-codes "Nos". PARENT_OPTIONS["stock-item"] and PARENT_OPTIONS["unit"] are empty arrays, so the SelectionList is never rendered (`parentOptions.length > 0` is false), `parent` stays "", and the create call sends `unit_symbol: p || "Nos"`. A Kg item is impossible; every item is Numbers.
  - *evidence:* frontend/src/screens/masters/MasterCreateScreen.tsx:26-27 (`unit: []`, `"stock-item": []`), :93 `unit_symbol: p || "Nos"`, :147 `{parentOptions.length > 0 ? … : null}`
  - *how to tell:* If real: `SELECT si.name, u.symbol FROM stock_item si LEFT JOIN unit u ON u.id=si.unit_id` shows symbol='Nos' for every UI-created item regardless of intent, and the form shows no unit field at all. If not real: a unit picker is on screen and the chosen symbol lands in unit_id.
- **[medium]** A BLANK UNIT is still creatable over HTTP and is refused only much later, at IRN time. masters.CreateStockItem writes unit_id NULL whenever unit_symbol is "" — there is no NOT NULL, no CHECK and no server-side requirement. The e-invoice builder is the only thing that notices, and by then the invoice is already posted.
  - *evidence:* backend/internal/masters/masters.go:800-811 (`if si.UnitSymbol != "" { …resolve… }`, unitID stays nil otherwise) and :812-816 (INSERT with a nil unit_id); backend/internal/einvoice/einvoice.go:711 refuses `unit == nil || TrimSpace(*unit)==""` with ErrNoUnitOfMeasure
  - *how to tell:* If real: `POST /v1/companies/{co}/stock-items {"name":"NoUnit Widget"}` answers 201 and `SELECT name, unit_id FROM stock_item WHERE name='NoUnit Widget'` shows unit_id IS NULL; a Sales item invoice on it posts fine and only Alt+I fails. If not real: the POST is 422 naming unit_symbol.
- **[high]** The item-invoice empty state gives the operator a route that does not exist, so the dead end is also mis-signposted.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:2476-2481 — "This company has no stock items yet — create one (Alt+C from Gateway > Masters) before keying an item invoice."; there is no Masters entry in GatewayScreen.tsx:4-62
  - *how to tell:* If real: following the instruction literally (Gateway, Alt+C) lands on "Create ledger". If not real: a Masters menu exists on the Gateway.

## ROW 21 — Item Invoice mode (Ctrl+H) on F8/F9 — stock_movement qty AND VALUE, server-derived postings

**Reachable from the keyboard today:** YES for create — Ctrl+H is a plain Ctrl combo (tallyprime.ts:51) and entryModesFor offers "Item Invoice" for sales/purchase (voucherConfig.ts:296-304). The ALTER path is reachable from the Day Book drill (navMachine.ts:563, :600).

**Keystrokes**

```
CREATE: F8 → Ctrl+H → select "Item Invoice" → Enter; key Party, Place of Supply, Item 1 = a seeded item, Qty 10, Rate 100, Disc 10 → Ctrl+A. Repeat with F9 for Purchase. ALTER (the important one): Alt+G → Day Book → arrow to the invoice just saved → Enter (drill to alter) → change ONLY the narration → Ctrl+A.
```

**Oracle**

```
psql on the movement ledger joined to names — never a total:
```sql
SELECT vv.voucher_no, sm.line_no, si.name AS item, COALESCE(g.name,'(NULL godown)') AS godown,
       COALESCE(b.name,'(NULL batch)') AS batch, sm.direction, sm.qty, sm.rate, sm.discount_pct, sm.amount
  FROM stock_movement sm
  JOIN voucher_head vh ON vh.company_id=sm.company_id AND vh.current_version_id=sm.voucher_version_id
  JOIN voucher_version vv ON vv.id=sm.voucher_version_id
  JOIN stock_item si ON si.id=sm.stock_item_id
  LEFT JOIN godown g ON g.id=sm.godown_id
  LEFT JOIN stock_batch b ON b.id=sm.batch_id
 WHERE vh.voucher_id='<vid>' AND vh.is_deleted=false ORDER BY sm.line_no;
```
and the accounting SIDES BY NAME:
```sql
SELECT l.name, p.dr_cr, p.amount FROM posting p
  JOIN voucher_head vh ON vh.company_id=p.company_id AND vh.current_version_id=p.voucher_version_id
  JOIN ledger l ON l.id=p.ledger_id WHERE vh.voucher_id='<vid>' ORDER BY l.name;
```
Correct for a 10 @ 100 less 10% SALE: exactly one OUT movement with qty=10.0000, rate=100.0000, discount_pct=10.0000, amount=900.0000 (VALUE, not just qty), plus DR <customer>, CR Domestic Sales 900.0000, CR Output CGST/SGST. Correct AFTER the narration-only alter: the SAME one movement row, unchanged.
```

**Predictions**

- **[high]** ALTERING A SAVED ITEM INVOICE SILENTLY DESTROYS ITS STOCK. The Day Book drill builds the alter frame with NO entryMode, so effectiveEntryMode returns the class default "accounting-invoice" for every GST class; the item grid is not rendered and Ctrl+A routes to buildGstRequest, which sends postings + gst and NO `inventory`. The new voucher_version therefore carries ZERO stock_movement rows while the money is identical to the paisa (taxableValue and gst_rate_pct are both hydrated from the view), so nothing is out of balance and no layer errors.
  - *evidence:* frontend/src/keystroke/navMachine.ts:563 and :600 `stack.push(newVoucherFrame(row.voucherType, "alter"))` — third arg omitted, and navMachine.ts:345-362 leaves `config` unset when entryMode is undefined; frontend/src/screens/vouchers/voucherConfig.ts:343-346 `if (cfg.showGst) { if (chosen==="item-invoice" …) …; return "accounting-invoice"; }`; frontend/src/screens/VoucherScreen.tsx:730 `effectiveEntryMode(type, snapshot.config?.entryMode)` and :1573-1577 `entryMode === "item-invoice" ? buildItemRequest(f) : cfg.showGst ? buildGstRequest(f) : …`; VoucherScreen.tsx:312 hydrates `taxableValue: view.taxable_value`; backend has no guard — internal/ledger/ledger.go:2068-2071 simply writes whatever movements the request carried
  - *how to tell:* If real: run the movement query before and after the narration-only alter — before it returns 1 row (qty 10, amount 900), after it returns ZERO rows, while the posting query returns the same DR/CR names and the same 900.00/tax to the paisa, and Stock Summary's closing qty for that item jumps back by 10 with the closing VALUE moving with it. If not real: the item grid is visible on the alter screen and the movement row survives with line_no=1 under the NEW current_version_id.
- **[medium]** On the CREATE path the server-derived postings are correct in orientation for BOTH F8 and F9, and the movement carries a real VALUE (amount = qty×rate less discount, rounded once). This half of the row should PASS — flag it only if a name/side assertion fails.
  - *evidence:* backend/internal/ledger/item_invoice.go:124-140 (invoiceOrientation table: Purchase = party CR / goods DR / tax DR), :362-383 (one `taxable` written to both the movement amount and the GST line), :401-431; frontend/src/screens/vouchers/gstVoucher.ts:158-165 (purchase baseLedger "Purchases"/Purchase Accounts, taxKind "input"); frontend/src/screens/VoucherScreen.tsx:1468-1489
  - *how to tell:* If it passes: an F9 item invoice shows CR <supplier>, DR Purchases, DR Input CGST/SGST and an IN movement with amount=900.0000. If it fails: any Output* ledger on an F9, or a movement with amount=0.0000 while rate is non-zero.

## ROW 22 — Godowns and batches — a line keyed with a godown AND a batch must land on the right position

**Reachable from the keyboard today:** Godown: YES (a select exists in the item grid). BATCH: NO — there is no batch control on any screen in the build.

**Keystrokes**

```
F8 → Ctrl+H → Item Invoice → key Party + Item + Qty 5 + Rate 100 WITHOUT touching the Godown select (leave it as it opens) → Ctrl+A. Then F9 (purchase) the same way. Then look for ANY batch control: Tab through every cell of the item grid, and through Alt+F7 / Alt+F8 / Alt+F9 / Alt+F10 grids.
```

**Oracle**

```
Position-level, names not totals:
```sql
SELECT si.name AS item, COALESCE(g.name,'(NULL godown)') AS godown,
       COALESCE(b.name,'(NULL batch)') AS batch, sm.direction, sm.qty, sm.amount
  FROM stock_movement sm
  JOIN voucher_head vh ON vh.company_id=sm.company_id AND vh.current_version_id=sm.voucher_version_id
  JOIN stock_item si ON si.id=sm.stock_item_id
  LEFT JOIN godown g ON g.id=sm.godown_id
  LEFT JOIN stock_batch b ON b.id=sm.batch_id
 WHERE vh.voucher_id='<vid>';
```
Second oracle (the report the row feeds): GET /stock-summary and look at `rows[].godown` / `rows[].batch`. Correct = godown 'Main Location' (where every seeded position sits) and, when a batch was keyed, that batch's name — NOT '(NULL godown)' and not a "(no location)" row on the screen.
```

**Predictions**

- **[high]** THE FIXED GODOWN-DEFAULT BUG HAS AN UNFIXED TWIN IN THE ITEM-INVOICE GRID. The orchestrator's fix (defaultGodownOf / applyDefaultGodown) landed only in StockVoucherScreen; ItemInvoicePanel's godown <select> still opens on value "" ("— not tracked —") and VoucherScreen's masters loader never calls defaultGodownOf. So an F9 item-invoice purchase keyed without touching the select writes godown_id NULL, creating a phantom untracked position while all the company's stock sits at Main Location.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:2546-2563 (`<option value="">— not tracked —</option>`, `value={l.godownId}` with no default) and :797-820 (the loader sets stockItems/godowns and applies NO default), vs the fix at frontend/src/screens/vouchers/StockVoucherScreen.tsx:113 `applyDefaultGodown(setLines, defaultGodownOf(gd.godowns))` and :502-506; frontend/src/screens/vouchers/itemInvoice.ts:210-222 `emptyItemLine()` sets `godownId: ""`; frontend/scripts/seed-demo.mts:1286 posts every seeded movement with `godown_id: mainLocation.id`
  - *how to tell:* If real: the movement query returns godown='(NULL godown)' and the Stock Summary grows a second position row rendered as "(no location)" alongside the Main Location one for the same item. If not real: godown='Main Location' and the select shows Main Location pre-selected when the screen opens.
- **[high]** THE SAME DEFAULT MAKES AN ITEM-INVOICE SALE FAIL OUTRIGHT. The negative-stock check is per (item, godown, batch) position, so an F8 sale keyed at the default "— not tracked —" is measured against the NULL position, which holds 0 — the post is refused with a message naming godown '-'. The operator sees a negative-stock error for an item the warehouse is full of.
  - *evidence:* backend/internal/ledger/stock.go:349-416 (positions keyed by godown/batch; `godown "-"` in NegativeStockError.Error at :197-209) and migration 0025's check_stock_not_negative (`so.godown_id IS NOT DISTINCT FROM NEW.godown_id`); frontend/src/screens/VoucherScreen.tsx:2554 (default "")
  - *how to tell:* If real: Ctrl+A on an F8 item invoice with the untouched godown returns 422 "negative stock refused for item <X> (godown -, batch -): closing balance would be -5" and NO voucher_head row appears. If not real: the sale posts and the movement query shows godown='Main Location'.
- **[high]** A BATCH CAN NEVER BE KEYED AT ALL. There is no batch column in the item-invoice grid, no batch select in StockVoucherScreen, no "batch" member of MasterKind, and `listStockBatches` has zero callers outside the API module — so `batchId` is hard-wired "" everywhere and stock_movement.batch_id is always NULL. Row 22's "a godown AND a batch" case is untestable from the keyboard, and F11 advertises the feature as on.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:2498-2520 (grid header: Item, Godown, Qty, Rate, Disc %, Amount — no Batch) and the only `batch` hits in the file are :359 (alter re-hydration) — nothing sets it; frontend/src/screens/vouchers/itemInvoice.ts:214 and stockVoucher.ts:155 both `batchId: ""`; frontend/src/api/rest.ts:551 `listStockBatches` with no non-API caller; frontend/src/keystroke/types.ts:277-278 MasterKind has no "batch"; frontend/src/screens/FeaturesScreen.tsx:281-283 claims "a line may name a batch/lot"
  - *how to tell:* If real: `SELECT count(*) FROM stock_movement WHERE company_id='<co>' AND batch_id IS NOT NULL` is 0 after every keyboard-driven voucher, and no cell labelled Batch exists in any grid snapshot. If not real: a Batch select is present and its selection lands in batch_id.

## ROW 23 — Stock Summary must show closing VALUE, not just closing quantity

**Reachable from the keyboard today:** YES — Gateway entry (GatewayScreen.tsx:11) and a REPORT_NAMES member, with a registered screen. Note migration 0026 DENIES report:stock-summary to the seeded Data Entry level, so drive it as Owner or the screen renders the 403 panel (stock-summary-denied).

**Keystrokes**

```
Gateway of Tally → arrow to "Stock Summary" → Enter. (Also Alt+G → "Stock Summary".) F2 to set the as-on date; Alt+E to export; Ctrl+P to print.
```

**Oracle**

```
Read the screen's own columns first — the table must carry BOTH `Closing Qty` and `Closing Value` per position and a "Stock-in-Hand" total (data-testid `stock-total-value`). Then confirm against the wire, not the screen: `GET /v1/companies/{co}/stock-summary?as_on=YYYY-MM-DD` and check `rows[].closing_qty`, `rows[].closing_value`, `rows[].outward_value` and `total_value` are all non-empty fixed-4 strings. Cross-check the value is at COST, not sale price: buy 60 @ 940, sell 25 @ any sale rate → outward_value must be 23500.0000 (25×940), closing_qty 35 and closing_value 32900.0000. There is deliberately no stored column to read — `SELECT * FROM stock_movement` gives you qty/rate/amount only; value is replayed.
```

**Predictions**

- **[medium]** This row should PASS on the value question: the screen has a Closing Value column per position AND per item AND a Stock-in-Hand total, all fed from server-replayed values, with outward booked at COST rather than at the sale price. The likely finding here is a clean green, and the row's real value in this audit is as the DETECTOR for rows 21/22 rather than as a defect of its own.
  - *evidence:* frontend/src/screens/reports/StockSummaryScreen.tsx:207 (`Closing Value` header), :223 (`{formatMoney(it.closing_value)}`), :240, :249-251 (`stock-total-value`); backend/internal/server/handler_stock_summary.go:85-86, :132-133, :99; backend/internal/reporting/stock_summary.go:565-593 (replay via stockPositionsAsOn, TotalValue folded from rows, no SUM(value) anywhere)
  - *how to tell:* If it passes: every position row shows a non-zero Closing Value beside its Closing Qty and the Stock-in-Hand total equals the sum of the item Closing Values. If it fails: a value column renders "0.00" while Closing Qty is non-zero, or outward_value equals qty × the SALE rate (which would make gross profit come out at zero).
- **[high]** Stock Summary is the cheapest oracle for the row-21 alter defect and the row-22 godown-default defect, and it will show both as legible symptoms rather than as errors: a "(no location)" position row appearing beside the Main Location one, and an item's Closing Qty/Value snapping back after a narration-only alter.
  - *evidence:* frontend/src/screens/reports/StockSummaryScreen.tsx:231-234 renders `{row.godown || "(no location)"}`; backend/internal/reporting/stock_summary.go:70-76 documents the NULL-dimension position as a first-class row
  - *how to tell:* If real: after the row-22 keystrokes the table shows two child rows under one item — 'Main Location' and '(no location)'; after the row-21 alter the item's Closing Qty rises by the sold quantity with the value moving with it. If not real: exactly one child row per item and the position is unchanged by the alter.

## ROW 24 — Negative stock — refused, or allowed per allow_negative_stock, and the FLAG MUST GOVERN IT

**Reachable from the keyboard today:** Refusal: YES. Opting IN: NO — allow_negative_stock is settable by nothing in the product.

**Keystrokes**

```
Refusal path: F8 → Ctrl+H → Item Invoice → select the item, set Godown = Main Location EXPLICITLY, Qty = (closing qty + 1) → Ctrl+A. Flag path: F11 (Features) → find "Negative stock" → try to toggle it. Then re-key the same over-sale.
```

**Oracle**

```
The refusal: Ctrl+A must produce a 422 whose body reads `negative stock refused for item <name> (godown Main Location, batch -): closing balance would be -1`, and `SELECT count(*) FROM voucher_head WHERE company_id='<co>' AND voucher_id='<new>'` must be 0 — nothing partially written. The flag: `SELECT name, allow_negative_stock FROM company WHERE id='<co>';` before and after whatever F11 offers. Correct = the value flips to true from the product, and the identical over-sale then COMMITS, leaving `SELECT si.name, sm.direction, sm.qty FROM stock_movement …` with a real OUT row and a negative closing position.
```

**Predictions**

- **[high]** THE FLAG IS UNREACHABLE, SO ONLY HALF OF THE RULE EXISTS. `allow_negative_stock` appears in exactly three non-test places — the migration that adds it, the Go pre-check that reads it, and the trigger that reads it. There is no HTTP route, no handler field, no masters/identity setter and no UI control that writes it; companyReq at company-create does not carry it. It is permanently false for every tenant, so the "allowed per the company's flag" arm of this row cannot be exercised at all, and F11 states the opposite.
  - *evidence:* grep for allow_negative_stock across backend+frontend returns only backend/migrations/0025_inventory_schema.sql:258/:361, backend/internal/ledger/stock.go:358 and a comment at nonaccounting.go:79 — no writer anywhere; backend/internal/server/server.go:1188-1224 handleCreateCompany passes Name/FYStartDate/BaseCurrency/StateCode/GSTIN/address/books-beginning/currency only; frontend/src/screens/FeaturesScreen.tsx:290-294 claims "A company can opt in per-company" with state "always"
  - *how to tell:* If real: F11 shows "Negative stock" as a read-only note with no toggle, and `SELECT allow_negative_stock FROM company` stays false no matter what is keyed; the only way to flip it is a hand-written UPDATE. If not real: some screen or route writes the column and the second over-sale attempt commits.
- **[medium]** The refusal itself works and the two enforcers AGREE, but the message will misname the position whenever the row-22 default fires: the Go pre-check and the SQL trigger use the same opening + net-of-current-versions formula and both fail closed on an unreadable company row; they differ only in the deliberate own-voucher exclusion (the pre-check excludes the voucher being altered because the head pointer has not moved yet, the trigger excludes nothing because it fires at COMMIT).
  - *evidence:* backend/internal/ledger/stock.go:349-416 and :450-474 (closingPosition, `$5::uuid IS NULL OR vh.voucher_id <> $5`, `COALESCE(v_allow,false)` equivalent at :356-366) vs backend/migrations/0025_inventory_schema.sql:353-405 (check_stock_not_negative, same joins, `IF COALESCE(v_allow,false)`); single funnel at backend/internal/ledger/ledger.go:2068-2071
  - *how to tell:* If they agree: over-selling from Main Location returns the Go message naming 'Main Location', and altering an existing OUT 10 down to OUT 4 is ACCEPTED (the pre-check's exclusion working) rather than refused as a further withdrawal of 4. If they disagree: the alter-down is refused, or the refusal arrives as a raw SQLSTATE P0001 from the trigger instead of the friendly Go message — meaning the pre-check missed a case the trigger caught.
- **[medium]** A Physical Stock shortage is intentionally NOT treated as negative stock and will commit with the flag false — do not report that as a defect.
  - *evidence:* backend/internal/ledger/nonaccounting.go:76-83 ("a shortage commits even when company.allow_negative_stock is false … the count is a FACT about the shelf")
  - *how to tell:* If as designed: Alt+F10 counting 3 where the book says 13 posts an OUT delta of 10 and leaves the position at exactly 3 (>= 0). If broken: the count is refused as negative stock, or the delta leaves a position other than the counted figure.

## ROW 1 — Double-entry vouchers — SIX classes (F4 Contra, F5 Payment, F6 Receipt, F7 Journal, F8 Sales, F9 Purchase), each keyed then Ctrl+A

**Reachable from the keyboard today:** yes — all six are bound at the gateway scope with bare F-keys (tallyprime.ts:30-35), no Alt, so the macOS Option-composition defect does not touch them. CAVEAT: on a Mac the F4-F9 row is media keys unless 'Use F1..F12 as standard function keys' is on; that is a host setting, not a code defect. PARTIALLY reachable in a second sense: the Date and Narration fields of every one of the six render as non-editable <span>s (see prediction 1), so two of the five fields the field-cursor walks cannot be keyed at all.

**Keystrokes**

```
From the Gateway, one class at a time: F4 / F5 / F6 / F7 / F8 / F9. Each opens VoucherScreen directly (frontend/src/keystroke/keymaps/tallyprime.ts:30-35, all six are {kind:"voucher"} at the gateway scope, no Alt involved so macOS Option-composition does not apply). In the form: type the party name into the Account / Party A/c Name SelectionList (autoFocus fires when snapshot.field==="party-ledger"), then Tab (nextField) into the panel. F4/F5/F6 -> SingleEntryPanel: type into data-testid="single-ledger:0" and "single-amount:0", '+ Add particular' for more. F7 -> DoubleEntryPanel: data-testid="de-ledger:0"/"de-amount:0" plus the side selector, second row for the credit. F8/F9 -> GstPanel: data-testid="gst-taxable", "gst-rate", "gst-pos". Then Ctrl+A. ⛔ There is NO keystroke that reaches the Date or the Narration on any of the six — F2 only moves a highlight (navMachine.ts:1173-1180); see prediction 1.
```

**Oracle**

```
psql -h localhost -p 5433 -U postgres -d tally. SIDES AND NAMES, never a total:

select vv.voucher_no, vt.parent_class, vv.voucher_date, coalesce(vv.narration,'<NULL>') narr,
       l.name ledger, g.name grp, p.dr_cr, p.amount
from posting p
join voucher_version vv on vv.id = p.voucher_version_id
join voucher_head vh on vh.current_version_id = vv.id
join voucher_type vt on vt.id = vv.voucher_type_id
join ledger l on l.id = p.ledger_id
join account_group g on g.id = l.group_id
where vv.company_id='1e42b056-171f-4ae6-9395-eb2da6801b90'
order by vv.created_at desc, p.dr_cr, p.amount desc limit 24;

CORRECT looks like, per class: CONTRA — Cr <the account named> / Dr <the other cash-or-bank>, and BOTH ledgers' grp must be 'Bank Accounts' or 'Cash-in-Hand'. PAYMENT — Cr bank / Dr expense. RECEIPT — Dr bank / Cr the debtor or income (⛔ NOT 'Dr <the previous voucher's customer>' — that is the already-found leak). JOURNAL — exactly the two sides keyed, in the keyed order. SALES — Dr party (Sundry Debtors) / Cr Domestic Sales / Cr Output CGST+SGST. PURCHASE — Cr party (Sundry Creditors) / Dr Purchases / Dr Input CGST+SGST. Also read voucher_date and narration on EVERY row: predictions 1 and 2 say voucher_date is always CURRENT_DATE-in-UTC and narration is always '' regardless of what the operator meant to key.
```

**Predictions**

- **[high]** THE VOUCHER DATE CANNOT BE KEYED ON ANY OF THE SIX CLASSES. The Date row renders `editable={draftRebuildable}`, and `draftRebuildable` is hard-false outside OCR draft mode — so on every F4/F5/F6/F7/F8/F9 frame Date is a read-only <span> with no onChange reachable. F2 (`{kind:"date"}`) only sets `active.field="date"`, i.e. it moves a highlight onto a span. Every browser-keyed voucher therefore posts dated `today()` and NOTHING an operator can press changes it: no back-dating, no forward-dating, no next-FY voucher (which is what makes Row 7 undrivable).
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:1753-1760 `<FieldRow name="Date" active={snapshot.field === "date"} value={form.date} editable={draftRebuildable} .../>`; :1694 `const draftRebuildable = isDraftMode ? canRebuildDraft(loadedDraft.draft.body) : false;`; :1836-1848 FieldRow renders `<span>` when `editable` is falsy; frontend/src/keystroke/navMachine.ts:1173-1180 `case "date": ... active.field = "date"` and nothing else
  - *how to tell:* REAL: key an F6 Receipt on any day, press F2, type '2027-05-01', Ctrl+A — the psql oracle shows `voucher_date` = the machine's UTC today, not 2027-05-01, and the DOM node for Date is a <span> not an <input> (no data-testid outside draft mode). NOT REAL: voucher_date comes back 2027-05-01 and `select count(*) from voucher_number_counter where fin_year=2027` becomes 1.
- **[high]** THE NARRATION CANNOT BE KEYED EITHER, on any class, for the identical reason — same `editable={draftRebuildable}` prop. So every browser-keyed voucher is posted with narration ''. And a test is certifying the gap: VoucherScreen.doubleEntry.test.tsx has a block commented `// Edit only the narration and save.` whose body edits nothing at all — it fires Ctrl+A and asserts the postings survived. The test name describes an interaction the UI does not support.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:1795-1802 `<FieldRow name="Narration" ... editable={draftRebuildable} .../>`; :1694; frontend/src/screens/VoucherScreen.doubleEntry.test.tsx:529-534 (comment says 'Edit only the narration and save', next statement is `fireEvent.keyDown(app, { key: "a", ctrlKey: true })`)
  - *how to tell:* REAL: `select voucher_no, narration from voucher_version where company_id='1e42b056-…' order by created_at desc limit 6` returns NULL/'' for every browser-keyed row while the seeded rows carry real text. NOT REAL: the narration you typed is on the row.
- **[high]** THE FORM IS NOT CLEARED AFTER Ctrl+A WHEN THE CLASS DOES NOT CHANGE. navMachine's accept branch mints a fresh frame and its comment says it 're-opens a fresh form for bulk entry', but VoucherScreen's only reset effect is guarded by `if (formClassRef.current === type) return;` — and Ctrl+A keeps the same type. So after saving, every field (party, partyLedgerId, particulars, GST rate, place of supply, taxable value, billRefs, cost allocations) is still populated on screen. Two consequences: (a) a second Ctrl+A with no typing silently posts a SECOND, byte-identical voucher under a fresh auto number; (b) an operator keying invoice #2 of the same class inherits invoice #1's GST rate / POS / narration state. This is the SAME state-leak class as the already-fixed 'Receipt after a Sale debited the customer' defect, with the cross-class half fixed and the same-class half left open.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:896-907 (`formClassRef` guard — returns early when type is unchanged); frontend/src/keystroke/navMachine.ts:1094-1103 (`// Ctrl+A accepts the voucher and re-opens a fresh form for bulk entry.` -> `newVoucherFrame(type, "create", …)`, same `type`); VoucherScreen.tsx:1595-1601 (the success handler sets savedNo/status only — it never calls `setForm(emptyForm())`); contrast :1643-1647 where Alt+X DOES call `setForm(emptyForm())`
  - *how to tell:* REAL: F6, Account=HDFC Bank, particular 'Acme Traders' 5000, Ctrl+A (header shows 'Saved  No: N'), then press Ctrl+A again touching nothing. The oracle shows TWO Receipts, voucher_no N and N+1, with identical ledger/side/amount rows, and `select next_no from voucher_number_counter where fin_year=2026` advanced by 2. NOT REAL: the second Ctrl+A posts nothing (or the form is visibly blank after the first save).
- **[medium]** `today()` is UTC-derived, so the default (and, per prediction 1, ONLY) voucher date is wrong for an Indian operator between 00:00 and 05:30 IST — it stamps YESTERDAY. On 1 April this crosses the financial year: a voucher keyed at 02:00 IST on 2027-04-01 is dated 2027-03-31 and consumes the FY-2026 series instead of opening FY 2027. Because the date is not editable the operator has no way to correct it.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:132-134 `function today(): string { return new Date().toISOString().slice(0, 10); }` (toISOString is UTC); :256 `date: today()` in emptyForm; backend/internal/ledger/numbering.go:183-192 finYearOf keys the counter off that same date
  - *how to tell:* REAL: `TZ=Asia/Kolkata` browser at 01:00 local — key any voucher, and the oracle's `voucher_date` is one day behind the operator's calendar. NOT REAL: the date matches the local calendar day (i.e. the code uses a local-date formatter). Cheap desk check without waiting for 01:00: run `node -e "console.log(new Date().toISOString().slice(0,10))"` with TZ=Asia/Kolkata at any hour before 05:30 IST.

## ROW 4 — Credit Note (Alt+F6) and ⛔ DEBIT NOTE (Alt+F5) — the Debit Note has never been driven end to end

**Reachable from the keyboard today:** yes — Alt+F5/Alt+F6 are bound at gateway scope with `code` F5/F6, which normalize.ts explicitly preserves (normalize.test.ts:79-81 pins comboFromEvent({key:"F5",code:"F5",altKey:true}) === "Alt+F5"), so the Option-composes-to-¬ defect that killed every Alt+<letter> does NOT apply. They are also named on the button bar now (buttonBar.ts:43-51), closing §4 lead 6. ⚠️ HOST CAVEAT, not a code defect: on macOS Option+F5 is a system accessibility shortcut (VoiceOver/Zoom) and F5/F6 are media keys unless the standard-function-keys setting is on — Playwright's synthetic key will pass where a human's fingers do not.

**Keystrokes**

```
From the Gateway: Alt+F5 -> Debit Note, Alt+F6 -> Credit Note (frontend/src/keystroke/keymaps/tallyprime.ts:115-116, gateway scope). Both now appear on the button bar (frontend/src/keystroke/buttonBar.ts:50-51), so they are advertised as well as bound. Both open VoucherScreen with cfg.showGst=true, cfg.itemInvoice=false, so effectiveEntryMode clamps to "accounting-invoice" and GstPanel renders. DEBIT NOTE drive: Alt+F5, party = 'Steel Suppliers' (a real Sundry Creditor in Demo Traders), Tab, gst-taxable = 2500, gst-rate = 18, gst-pos = 27 (Demo Traders is Maharashtra -> intra-state, expect CGST+SGST), Ctrl+A. CREDIT NOTE drive: Alt+F6, party = 'Mumbai Metals' (a real Sundry Debtor), gst-taxable = 12000, gst-rate = 18, gst-pos = 27, Ctrl+A. ⛔ Both parties maintain_bill_by_bill=true in the demo company, so the bill-refs panel WILL render — check its side label (prediction 3).
```

**Oracle**

```
THE MONEY, against the seeded controls of the same class — which I read out of the live DB so the comparison is exact:

select vv.voucher_no, vt.parent_class, l.name ledger, g.name grp, p.dr_cr, p.amount
from posting p join voucher_version vv on vv.id=p.voucher_version_id
join voucher_head vh on vh.current_version_id=vv.id
join voucher_type vt on vt.id=vv.voucher_type_id
join ledger l on l.id=p.ledger_id join account_group g on g.id=l.group_id
where vv.company_id='1e42b056-171f-4ae6-9395-eb2da6801b90'
  and vt.parent_class in ('DEBIT_NOTE','CREDIT_NOTE')
order by vt.parent_class, vv.created_at, p.dr_cr;

SEEDED CONTROLS (verified present today):
  DEBIT_NOTE  no.1  Dr Steel Suppliers / Cr Raw Material Purchases / Cr Input CGST / Cr Input SGST
  CREDIT_NOTE no.1  Cr Mumbai Metals   / Dr Domestic Sales         / Dr Output IGST

CORRECT for the browser-keyed DEBIT NOTE: Dr Steel Suppliers 2950 (Sundry Creditors) / Cr Purchases 2500 (Purchase Accounts) / Cr Input CGST 225 / Cr Input SGST 225. ⛔ The base ledger will read 'Purchases', not 'Raw Material Purchases' — that is expected (the client ensures its own name) and is NOT the defect. FAILURE looks like Cr Steel Suppliers, or Output CGST/SGST instead of Input, or Domestic Sales as the base.

CORRECT for the browser-keyed CREDIT NOTE: Cr Mumbai Metals 14160 / Dr Domestic Sales 12000 / Dr Output CGST 1080 / Dr Output SGST 1080.

SECOND ORACLE — the return the note lands in:
  GSTR-1 for the period must contain the CREDIT NOTE (party group nature ASSET) and must NOT contain the supplier-side DEBIT NOTE (nature LIABILITY). gstr1.go:414-416 is the discriminator.
```

**Predictions**

- **[high]** THE DEBIT NOTE IS COVERED BY THE SAME ORIENTATION TABLE ON BOTH SIDES AND HAS NO PATH OF ITS OWN — this is the direct answer to the row's question, and it is a PASS prediction. Client: GST_ORIENTATION["debit-note"] = {partySide DR, baseSide CR, taxSide CR, taxKind 'input', baseLedger Purchases/Purchase Accounts, partyGroup Sundry Creditors}, consumed by the one builder every showGst class routes through. Server: orientationFor(DebitNote) = {party Debit, goods Credit, tax Credit}, and validatePartyOrientation consults that same function on CREATE and ALTER. The two tables agree. VoucherScreen also passes orientation.taxKind into ensureGstTaxLedgers and orientation.partyGroup into the party ensure, so the Input ledgers and the Sundry-Creditors grouping are both class-derived, not sales-shaped leftovers.
  - *evidence:* frontend/src/screens/vouchers/gstVoucher.ts:178-185 (the debit-note row) and :513-547 (builder reads o.partySide/o.baseSide/o.taxSide with no default); frontend/src/screens/VoucherScreen.tsx:1392-1412 (gstOrientationFor(type) -> ensureGstTaxLedgers(…, orientation.taxKind) -> ensureLedger(groupName: orientation.partyGroup)); backend/internal/ledger/item_invoice.go:135-138 `case DebitNote: return invoiceOrientation{party: Debit, goods: Credit, tax: Credit}`; backend/internal/ledger/ledger.go:2437-2441 + :1138 (validatePartyOrientation called from post(), which both CREATE and ALTER route through)
  - *how to tell:* REAL (no defect): the oracle shows Dr Steel Suppliers / Cr Purchases / Cr Input CGST / Cr Input SGST — the mirror of the seeded control. DEFECT: any of Cr Steel Suppliers (the server should have 422'd with ErrPartyOrientation), or Output CGST/SGST on the tax legs (the server would NOT have refused this — see prediction 2), or 'Domestic Sales' as the base ledger.
- **[high]** THE SERVER'S ORIENTATION GUARD IS PARTY-LEG-ONLY, SO THE DEBIT NOTE'S TAX KIND AND BASE LEDGER ARE CLIENT-ENFORCED AND NOTHING ELSE. validatePartyOrientation iterates postings looking only for `p.LedgerID == in.PartyLedgerID` and returns on the first match; the tax legs and the expense leg are never inspected, and its own header says so. Nothing anywhere refuses a Debit Note that credits 'Output CGST' instead of 'Input CGST' — the voucher balances, deriveAndVerifyGST sums amounts without reading dr_cr, and the deferred trigger re-checks the same balance. This is exactly the shape of the already-found item-mode defect where 'the direction was right, the account was wrong'.
  - *evidence:* backend/internal/ledger/ledger.go:2424-2436 (`⚠️ IT CHECKS THE PARTY LEG ONLY, AND THAT IS A DELIBERATE FLOOR`) and :2452-2470 (loop returns on the party match); :2402-2409 (validatePostings only asks Dr==Cr; deriveAndVerifyGST 'sums posting amounts WITHOUT reading dr_cr')
  - *how to tell:* REAL: intercept the Alt+F5 POST body and swap the two Input tax ledger_ids for the company's Output CGST/SGST ids (leave the sides alone) — cored answers 201 and the oracle shows `Cr Output CGST` on a DEBIT_NOTE, i.e. the company's output liability reduced by a purchase return. NOT REAL: cored 422s. (Do this by editing the request, not the client code — it is the server property under test.)
- **[high]** THE BILL-REFERENCE PANEL LABELS A CREDIT NOTE'S PARTY LEG WITH THE WRONG SIDE — a surviving hard-coded sales answer that the GST_ORIENTATION fix missed. `raiseLeg.drCr` is literally `type === "purchase" ? "CR" : "DR"`, so a credit-note gets "DR" while GST_ORIENTATION["credit-note"].partySide is "CR". The WIRE is unaffected (buildGstVoucherRequest builds its allocations from `o.partySide`), so this is a screen that contradicts the voucher it is about to post: the panel header renders 'Bill-wise references for Mumbai Metals (14160.0000 DR)' over a posting that will be CR. Debit Note reads DR and is right by coincidence, which is why one class's bug hid.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:1267-1271 `const raiseLeg: BillBearingLeg = { ledgerId: form.partyLedgerId, // A purchase credits the creditor; everything else debits the debtor.  drCr: type === "purchase" ? "CR" : "DR", …}`; frontend/src/screens/vouchers/gstVoucher.ts:167-174 (credit-note partySide "CR"); gstVoucher.ts:597-600 (the wire uses `o.partySide`, so only the display is wrong); frontend/src/screens/vouchers/BillAllocationPicker.tsx:146-147 (leg.drCr is rendered in the header) — and leg.drCr is used NOWHERE else in raise mode
  - *how to tell:* REAL: on Alt+F6 with Mumbai Metals picked, the data-testid="billrefs" header reads '… DR' while the posted row in `bill_allocation` (join it to posting) reads dr_cr='C'. NOT REAL: the header reads 'CR'. Also read back `select ba.ref_name, ba.ref_type, ba.dr_cr, ba.amount from bill_allocation ba join voucher_version vv on vv.id=ba.voucher_version_id where vv.company_id='1e42b056-…' order by vv.created_at desc limit 4` to confirm the WIRE side is C (if it is D, this is far worse than a label).
- **[medium]** ALTERING A CREDIT OR DEBIT NOTE DESTROYS ANY COST-CENTRE SPLIT ON IT. `buildGstRequest` is the accept builder for all four showGst classes and it never attaches `cost_allocations`, while `posting_cost_allocation` is keyed on voucher_version_id — so the alter restates the voucher without them and they are gone. The other two builders (single-entry, double-entry) both DO re-send them, which is what makes this an omission rather than a design.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:1352-1443 (buildGstRequest — no buildCostAllocations call anywhere in it), contrast :1046-1058 (single) and :1183-1195 (double) which both do; backend/internal/server/voucher_readback_audit_test.go:733-745 names this exact gap: `buildGstRequest … credit-note / debit-note ⛔ DOES NOT`
  - *how to tell:* REAL: POST a credit-note over HTTP carrying a cost_allocation, drill it from the Day Book, press Ctrl+A changing nothing — `select count(*) from posting_cost_allocation pca join posting p on p.id=pca.posting_id join voucher_version vv on vv.id=p.voucher_version_id join voucher_head vh on vh.current_version_id=vv.id where vh.voucher_id='<id>'` drops to 0, and the Cost Centre Summary loses the amount while the Trial Balance is unchanged. NOT REAL: the count survives the alter.
- **[medium]** NOTHING STOPS A DEBIT NOTE BEING RAISED ON A SUNDRY DEBTOR, AND THAT SILENTLY INFLATES GSTR-1. The party picker offers one list — PARTY_GROUPS — for every class, so Alt+F5's Party A/c Name lists 'Acme Traders' and 'Mumbai Metals' (debtors) alongside the creditors. GSTR-1's note discriminator keys on the party group's NATURE, not on the voucher class, so a debit note against a debtor is reported as an OUTWARD supply. The class-correct `partyGroup` in GST_ORIENTATION only governs a FREE-TYPED new party; a picked existing one bypasses it entirely.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:95-101 (`const PARTY_GROUPS` — one list, all classes) and :1404-1412 (`orientation.partyGroup` is inside the `f.partyLedgerId || ensureLedger(...)` fallback, i.e. skipped when a party is picked); backend/internal/compliance/gstr/gstr1.go:412-417 (`OR (vt.parent_class IN ('CREDIT_NOTE','DEBIT_NOTE') AND pg.nature = 'ASSET')`)
  - *how to tell:* REAL: Alt+F5, pick 'Acme Traders' (Sundry Debtors) instead of a supplier, 1000 @ 18, Ctrl+A — no warning appears, cored answers 201, and the GSTR-1 cdnr section for that period gains a note that is really a purchase return. NOT REAL: the picker on a Debit Note offers only Sundry Creditors, or the save is refused.

## ROW 6 — Single-entry mode (Ctrl+H) on Payment / Receipt / Contra — one primary leg plus N counter-legs

**Reachable from the keyboard today:** yes — Ctrl+H is bound globally at `{kind:"changeMode"}` (tallyprime.ts:51), navMachine opens the overlay for any voucher frame (navMachine.ts:1164-1170), and entryModesFor(payment|receipt|contra) returns a real two-option list with `mode` non-null on both (voucherConfig.ts:312-315). Single entry is also the DEFAULT for these three, so the panel is reachable without pressing Ctrl+H at all. ⚠️ The particulars' ledger input is a plain <input list="single-entry-ledgers">, so it is keyboard-reachable but its completions are wrong (prediction 2).

**Keystrokes**

```
F5 (or F6, or F4) from the Gateway, then Ctrl+H — a 'Change Mode' dialog (role="dialog", aria-label="Change Mode", data-testid="change-mode-overlay") lists 'Single Entry  ← current' and 'Double Entry'. Pick with the SelectionList, no confirming Enter. Single is already the default for these three (effectiveEntryMode returns cfg.entry). Then: party field = the bank/cash account; data-testid="single-ledger:0" + "single-amount:0" for the first particular; data-testid="single-add-line" for a second; Ctrl+A. ⛔ Drive at least TWO particulars, and drive CONTRA as well as Payment/Receipt — Contra is the class the shared group-guess is wrong for (prediction 1), and 'the default is always right for exactly one of them'.
```

**Oracle**

```
TWO queries; the second is the one that catches this row's defect.

(a) sides and names of the posting set — a single-entry Payment must be exactly ONE leg on cfg.primarySide carrying the SUM, and N legs opposite:
select vv.voucher_no, vt.parent_class, l.name ledger, g.name grp, g.nature, p.dr_cr, p.amount
from posting p join voucher_version vv on vv.id=p.voucher_version_id
join voucher_head vh on vh.current_version_id=vv.id
join voucher_type vt on vt.id=vv.voucher_type_id
join ledger l on l.id=p.ledger_id join account_group g on g.id=l.group_id
where vv.company_id='1e42b056-171f-4ae6-9395-eb2da6801b90'
  and vt.parent_class in ('CONTRA','PAYMENT','RECEIPT')
order by vv.created_at desc, p.dr_cr;

(b) ⛔ THE LEDGER THE VOUCHER MINTED, AND ITS GROUP'S NATURE — this is the oracle a posting-only check is blind to, because the voucher balances either way:
select l.name, g.name grp, g.nature, g.is_pl, l.created_at
from ledger l join account_group g on g.id=l.group_id
where l.company_id='1e42b056-171f-4ae6-9395-eb2da6801b90'
order by l.created_at desc limit 6;

CORRECT for a CONTRA: both ledgers' grp in ('Bank Accounts','Cash-in-Hand','Bank OD A/c'), nature ASSET (or LIABILITY for an OD), is_pl=false. WRONG (prediction 1): the new counter-leg lands in 'Indirect Expenses', nature EXPENSE, is_pl=true.

THIRD ORACLE, the report a business reads: the P&L must not list the contra's counter-account at all, and the Balance Sheet's Cash-in-Hand/Bank Accounts must move by the transferred amount on both sides.
```

**Predictions**

- **[high]** A CONTRA CREATES ITS COUNTER-ACCOUNT AS AN EXPENSE. The group a free-typed particular is created under is guessed from `cfg.primarySide`, which is "CR" for BOTH Payment and Contra — so the Contra branch silently inherits the Payment answer, 'Indirect Expenses'. A contra's counter-leg is by definition another cash or bank account; there is no contra whose other side is an expense. Result: F4, Account = Cash, particular = a bank the company has not got a ledger for yet, 25000, Ctrl+A -> a new ledger 'Petty Cash Box'/'ICICI Current' is minted under Indirect Expenses (EXPENSE, is_pl=true) and 25,000 leaves the Balance Sheet's cash and appears in the P&L as an expense. The voucher balances to the paisa, cored answers 201, and there is no server-side check that a Contra touches only cash/bank ledgers — ledger.go's only mention of the rule is a comment on the Class constant. The in-code comment beside the guess reasons about payments and receipts and never mentions contra.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:1023-1029 `const id = await ensureLedger(client, companyID, { name: l.ledger.trim(), // A payment's counter-ledgers are usually expenses, a receipt's usually income. … groupName: cfg.primarySide === "CR" ? "Indirect Expenses" : "Direct Incomes" })`; frontend/src/screens/vouchers/voucherConfig.ts:92-103 (contra primarySide "CR") vs :104-115 (payment primarySide "CR") — the two classes are indistinguishable to that ternary; backend/internal/ledger/ledger.go:56-57 (`// Contra (F4): transfers between cash/bank accounts only.` — a comment, with no validator anywhere: no `case Contra` appears in any validate* function)
  - *how to tell:* REAL: oracle (b) shows the new ledger with grp='Indirect Expenses', nature='EXPENSE', is_pl=true, and the P&L for the period gains a line for it. NOT REAL: it lands in Bank Accounts / Cash-in-Hand with nature ASSET, or the save is refused. ⛔ Do NOT clear this row on the posting query alone — oracle (a) shows a perfectly balanced Cr Cash / Dr <name> either way. Control leg: repeat with an EXISTING bank name typed exactly ('HDFC Bank') — ensureLedger resolves before it creates, so no new ledger appears and the group stays right; that proves the defect is the CREATE path, not the whole class.
- **[high]** THE PARTICULARS' COMPLETION LIST OFFERS THE WRONG LEDGERS ON THE TWO CLASSES THAT USE IT MOST. `<datalist id="single-entry-ledgers">` is filled from `parties`, i.e. the PARTY_GROUPS-filtered list (Sundry Debtors/Creditors, Cash-in-Hand, Bank Accounts, Bank OD A/c). But a Payment's particulars are expenses and a Receipt's are incomes, and no expense or income ledger is in that list — so on the classes where the counter-leg is an expense the operator gets zero completions and must free-type, which is precisely the input that then hits the wrong-group guess above. Conversely on a Contra, where the completion WOULD be right, the list is the only one that is right by accident.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:2344-2350 (`<datalist id="single-entry-ledgers">{parties.map(…)}`) and :2274 (`list="single-entry-ledgers"` on the particular input); :95-101 `const PARTY_GROUPS = ["Sundry Debtors","Sundry Creditors","Cash-in-Hand","Bank Accounts","Bank OD A/c"]`
  - *how to tell:* REAL: F5 Payment, focus data-testid="single-ledger:0", type 'E' — the dropdown offers no 'Electricity'/'Rent'/'Salaries' even though `select l.name from ledger l join account_group g on g.id=l.group_id where g.name like 'Indirect Expenses'` shows the company has them. NOT REAL: the company's expense ledgers appear as completions.
- **[medium]** THE SAME WRONG-GROUP GUESS EXISTS ONE BRANCH OVER, IN THE Ctrl+H > DOUBLE ENTRY PATH, KEYED ON THE ROW'S SIDE INSTEAD OF THE CLASS — so switching a Contra to Double Entry does not escape it: a Dr row creates under 'Indirect Expenses' and a Cr row under 'Indirect Incomes'. This is the 'fixing the bug is not fixing the class' pattern the repo already documented for ensureOutputTaxLedgers: two call sites, same helper, same guess, one file apart.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:1157-1167 (`groupName: l.side === "DR" ? "Indirect Expenses" : "Indirect Incomes"`), compare :1028 — the two guesses disagree with each other on the credit side ('Direct Incomes' vs 'Indirect Incomes') for what may be the same ledger; frontend/src/screens/vouchers/gstVoucher.ts:354-364 (the repo's own statement of the two-call-site lesson)
  - *how to tell:* REAL: F4 Contra, Ctrl+H > Double Entry, key Dr 'New Vault A/c' 5000 / Cr 'Cash' 5000, Ctrl+A — oracle (b) shows 'New Vault A/c' under Indirect Expenses. Also: key the SAME new name once via single entry on a Payment (-> Indirect Expenses) and once via double entry on a Receipt's credit row (-> Indirect Incomes) in two different companies, and the two companies disagree about what that ledger is. NOT REAL: both paths land it somewhere defensible, or refuse.

## ROW 7 — Voucher numbering series — per (class, financial year), monotonic, MUST RESTART next FY

**Reachable from the keyboard today:** NO for the half the row is named for. The monotonic-within-FY half is drivable and I expect it to pass. The FY RESTART is unreachable from the keyboard today: the voucher date is rendered as a non-editable <span> on every accounting class (VoucherScreen.tsx:1753-1760 + :1694), F2 only moves the field highlight (navMachine.ts:1173-1180), there is no period/date overlay in VoucherScreen's render (only 'change-mode' is rendered, :1795-1797), and the DB confirms the consequence — every counter row in the demo company is fin_year 2026 and there is not one FY-2027 row. Mark row 7 PARTIALLY REACHABLE and record the reason as a date-entry gap, not a numbering gap.

**Keystrokes**

```
MONOTONIC-WITHIN-FY half (drivable): F8, party, gst-taxable 1000, gst-rate 18, gst-pos 27, Ctrl+A — read the 'No:' chip; then Ctrl+A again (the form is not cleared, prediction row 1/3) or F5-then-F8 to force a fresh form, key a second, Ctrl+A. Repeat for F9 and F7 to prove the series are INDEPENDENT per class. Alt+2 (Duplicate) on a drilled voucher clears form.voucherNo and forces a new allocation. ⛔ FY-RESTART half: THERE IS NO KEYSTROKE. F2 in a voucher only highlights the Date span; the Date is not an input; the 'No.' row is rendered with active={false} and no onChange, so a manual override cannot be keyed either. The only way to reach FY 2027 is an HTTP POST with an explicit voucher_date — which is not the keyboard, and so not this row.
```

**Oracle**

```
THE COUNTER TABLE ITSELF, which is the only place the invariant lives:

select vt.name, vt.parent_class, vt.numbering_method, vt.prefix, vt.suffix, vt.width,
       c.fin_year, c.next_no
from voucher_type vt
left join voucher_number_counter c on c.voucher_type_id = vt.id
where vt.company_id='1e42b056-171f-4ae6-9395-eb2da6801b90'
order by vt.name, c.fin_year;

I ran this already; the state to diff against is: every accounting type AUTOMATIC, prefix/suffix/width all NULL, and EXACTLY ONE fin_year row per type — 2026 — with next_no Sales 4, Journal 9, Credit Note 3, Payment 3, Purchase 3, Contra 2, Debit Note 2, Receipt 2.

AND THE NUMBERS ACTUALLY STAMPED, per class per FY:
select vt.parent_class, extract(year from vv.voucher_date - interval '3 months') fy_start,
       vv.voucher_no, vv.voucher_date
from voucher_version vv join voucher_head vh on vh.current_version_id=vv.id
join voucher_type vt on vt.id=vv.voucher_type_id
where vv.company_id='1e42b056-171f-4ae6-9395-eb2da6801b90'
order by vt.parent_class, vv.voucher_date;

CORRECT: within one (class, FY) the numbers are strictly increasing with no repeat; ACROSS classes the same integer may repeat (independent series — that is the point); a second FY, if one could be reached, starts again at 1. FAILURE: two vouchers of the same class and FY sharing a number, or a number that jumped, or FY 2027 continuing from 2026's next_no.
```

**Predictions**

- **[high]** THE FY-RESTART INVARIANT CANNOT BE EXERCISED FROM THE KEYBOARD, SO ROW 7 IS AT BEST HALF-MEASURABLE — and the reason is the Date field, not the allocator. finYearOf keys the counter off in.VoucherDate, and the ONLY value the browser can put there is `today()`. Every browser-keyed voucher for the whole of a financial year lands in one fin_year bucket. The allocator's restart branch is unreachable from the app it ships in.
  - *evidence:* backend/internal/ledger/numbering.go:183-192 (finYearOf(voucherDate)) and backend/internal/ledger/ledger.go:1683-1701 (vDateForFY := in.VoucherDate, defaulted to time.Now() when empty); frontend/src/screens/VoucherScreen.tsx:1753-1760 (Date `editable={draftRebuildable}`), :1694, :132-134 (`today()`); frontend/src/keystroke/navMachine.ts:1173-1180; live DB: `select distinct fin_year from voucher_number_counter` for Demo Traders returns {2026} only
  - *how to tell:* REAL: no sequence of keys produces a voucher_version with a voucher_date outside the current FY, and `select count(*) from voucher_number_counter where fin_year <> 2026` stays 0 no matter what is keyed. NOT REAL: the Date row accepts typing (it would be an <input>) and a 2027-04-xx voucher mints a fin_year=2027 counter row starting at 1. The Go-level restart itself is covered by backend/internal/ledger/numbering_test.go — that is a UNIT proof, not a parity proof, and this row must not be scored green on it.
- **[high]** MANUAL / AUTOMATIC_MANUAL NUMBERING HAS NO KEYBOARD PATH EITHER. The 'No.' row renders `active={false}` with no `onChange` and displays `displayNo || "auto"`, so an operator can never supply a voucher number. numbering.go's whole 'MANUAL OVERRIDE DOES NOT BUMP THE COUNTER' invariant, and the methodManual error branch, are therefore dead from the UI — the entire three-of-four numbering methods are unreachable. Every seeded type is AUTOMATIC, which is why nothing has noticed.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:1762-1767 `<FieldRow name="No." active={false} value={displayNo || "auto"} testId="voucher-no-field" />` (no `editable`, no `onChange`); :1689 `const displayNo = savedNo ?? form.voucherNo;` — form.voucherNo is only ever set by hydration or cleared by Alt+2 (:1661); backend/internal/ledger/numbering.go:17-32 (the override + MANUAL invariants); live DB: numbering_method='AUTOMATIC' on all 13 seeded types
  - *how to tell:* REAL: focus the No. row by any means and type — nothing changes; the DOM node is a <span data-testid="voucher-no-field">. On a fresh F8, it reads literally 'auto'. NOT REAL: it is an <input> and a typed value survives to `voucher_version.voucher_no` while `next_no` does NOT advance.
- **[medium]** THE DISPLAY NUMBER CARRIES NO FY AND NO CLASS TOKEN, SO THE RESTART — IF IT EVER HAPPENED — WOULD MINT A SECOND VOUCHER LITERALLY NUMBERED '1' THAT NO REPORT OR PRINTED DOCUMENT COULD TELL FROM THE FIRST. Every seeded voucher_type has prefix, suffix AND width NULL, and formatVoucherNo emits the bare integer; numbering.go states outright that FY tokens are not interpolated. There is no UNIQUE constraint on voucher_no, by design. So Sales/2026-27 no.1 and Sales/2027-28 no.1 are the same string in the Day Book, in Outstandings' ref_name (which post() stamps from voucher_no), and on the invoice.
  - *evidence:* backend/internal/ledger/numbering.go:36-39 (`we do not interpolate FY tokens here`), :170-178 (formatVoucherNo), :41-45 (`DUPLICATES are allowed this increment (no UNIQUE constraint)`); backend/internal/ledger/ledger.go:1750-1757 (`stamped[i].RefName = in.VoucherNo` — the bill reference inherits the bare number); live DB: prefix/suffix/width all NULL on all 13 types of Demo Traders
  - *how to tell:* REAL: the 'No:' chip after Ctrl+A on F8 reads a bare '4' (not 'INV/2026-27/4'), and `select prefix, suffix, width from voucher_type where company_id='1e42b056-…'` is all NULL. Then note that `select ref_name from bill_allocation` for browser-keyed invoices holds that same bare integer — two FYs would collide in Outstandings' (ledger_id, ref_name) grouping and net two unrelated bills into one. NOT REAL: the number renders with an FY-bearing prefix.
- **[high]** WITHIN ONE FY THE ALLOCATOR IS CORRECT AND RACE-SAFE — a PASS prediction, so the row is not scored red for the wrong reason. One `INSERT … ON CONFLICT (company_id, voucher_type_id, fin_year) DO UPDATE SET next_no = next_no + 1 RETURNING next_no - 1`, executed inside post()'s transaction, serializes concurrent allocations on the PK row lock; it is never decremented, ALTER carries the number forward and never allocates, and DELETE leaves a gap. Two browser tabs keying F8 at once must get distinct consecutive numbers.
  - *evidence:* backend/internal/ledger/numbering.go:123-138 (the single atomic upsert); backend/internal/ledger/ledger.go:1677-1701 (allocate only on OpCreate with empty voucher_no) and :1702-1721 (OpAlter carries prevNo forward, never allocates)
  - *how to tell:* REAL: key three F8 invoices; voucher_no runs 4,5,6 and next_no goes 4->7 with no repeat. Then drill one from the Day Book, Ctrl+A — its voucher_no is unchanged and next_no does NOT move. NOT REAL: an alter mints a new number, or two vouchers of the same class share one. ⛔ Note the ONE genuine gap in the allocate branch: it is guarded by `in.VoucherTypeID != uuid.Nil`, and defaultVoucherTypeID returns Nil-with-no-error when the company has no system type for the class — such a voucher posts with a NULL voucher_no and no number at all. All 13 types exist in Demo Traders so this will not fire there; it would fire on a company created before migration 0018/0029 that missed the backfill. Test it, if at all, on a scratch company, not the demo.

## ROW 14 — GST derivation (CGST/SGST vs IGST) driven INTRA- and INTER-state, and the supply's appearance in GSTR-1 b2b + table-12 HSN

**Reachable from the keyboard today:** PARTIALLY — NO. The voucher half is reachable (F8, global binding in keystroke/keymaps/tallyprime.ts). The REPORT half is not: no screen, no keystroke and no menu entry anywhere renders GSTR-1 or GSTR-3B. `generateGSTR1` exists on the client (api/rest.ts:772, api/client.ts:926) and its only caller in the whole tree is api/api.test.ts:66. There is NO GSTR-3B client method at all — `grep -rni gstr3b frontend/src` returns nothing outside the backend. Yet FeaturesScreen.tsx:140 advertises "GST (CGST/SGST/IGST derivation, GSTR-1, GSTR-3B)" as a feature. Row 14 therefore cannot be driven end-to-end from the Gateway; the return must be read with curl or psql.

**Keystrokes**

```
From Gateway: F8 (Sales). Fill Party = "Acme Buyer", Taxable Value = 10000, GST Rate % = 18, Place of Supply = 29 (Demo Traders' home state is 29 — psql: select state_code from company where name='Demo Traders' -> 29). Ctrl+A. Repeat with Place of Supply = 27 for the INTER-state leg. NOTE: the GST panel is exactly three inputs (data-testid gst-taxable / gst-rate / gst-pos, VoucherScreen.tsx:1944-1981) — there is no HSN box and no party-GSTIN box on this form. GSTR-1 / GSTR-3B ARE NOT REACHABLE FROM THE KEYBOARD AT ALL (see `reachable`): read them with curl GET /v1/companies/{id}/gstr1?from=2026-04-01&to=2027-03-31 (server.go:2743) or with the psql oracle below.
```

**Oracle**

```
POSTINGS, by side and by ledger NAME — never a total.

select l.name, p.dr_cr, p.amount from posting p join ledger l on l.id=p.ledger_id join voucher_version vv on vv.id=p.voucher_version_id join voucher_head vh on vh.current_version_id=vv.id where vv.voucher_no='<no>' order by p.dr_cr desc, l.name;

CORRECT for PoS=29 (intra): D <party> 11800 | C Sales 10000 | C 'Output CGST' 900 | C 'Output SGST' 900, and NO row naming 'Output IGST'.
CORRECT for PoS=27 (inter): D <party> 11800 | C Sales 10000 | C 'Output IGST' 1800, and NO 'Output CGST'/'Output SGST' row. (Ledger names confirmed present in the DB: Output CGST/SGST/IGST, Input CGST/SGST/IGST.)

GSTR-1 table 12 (the exact SQL loadHSNRows runs, gstr1.go:491-518) — assert the hsn_sc STRING, not the money:
select gl.hsn hsn_sc, gl.gst_rate_pct rt, sum(gl.taxable_value*sgn) txval, sum(gl.cgst*sgn) camt, sum(gl.sgst*sgn) samt, sum(gl.igst*sgn) iamt from voucher_gst_line gl join voucher_version vv on vv.id=gl.voucher_version_id join voucher_head vh on vh.company_id=vv.company_id and vh.voucher_id=vv.voucher_id and vh.current_version_id=vv.id join voucher_type vt on vt.id=vv.voucher_type_id left join ledger pl on pl.id=vv.party_ledger_id left join account_group pg on pg.id=pl.group_id cross join lateral (select case when vt.parent_class='CREDIT_NOTE' then -1 else 1 end) s(sgn) where vh.is_deleted=false and (vt.parent_class='SALES' or (vt.parent_class in ('CREDIT_NOTE','DEBIT_NOTE') and pg.nature='ASSET')) group by 1,2 order by 2,1;
CORRECT = every hsn_sc is a non-empty code. I ran this read-only just now and it ALREADY returns a row with hsn_sc = '' carrying 308000.0000 taxable / 22320 CGST / 22320 SGST / 10800 IGST.

GSTR-1 b2b membership: the supply reaches b2b only when the party is `registered` (gstr1.go:456, isRegistered at :891) — i.e. the party ledger carries a GSTIN. Oracle: select l.name, l.gstin, l.gst_registration_type from ledger l where l.id=<party>. If gstin is null/'' the sale is B2CS/B2CL, NOT b2b, and no screen ever says so.
```

**Predictions**

- **[high]** The accounting-mode GST form has NO HSN input, so every GST sale keyed from the keyboard stores voucher_gst_line.hsn = '' — a blank HSN is not an operator mistake, it is the only outcome the UI can produce.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:1944-1981 — the whole GST panel is three <input>s: gst-taxable (1950), gst-rate (1961), gst-pos (1972). `form.gst.hsn` exists in state and is seeded from the server (VoucherScreen.tsx:305) but is never bound to any control; gstVoucher.ts:556 then sends `...(gst.hsn ? { hsn: gst.hsn } : {})`, i.e. omits it. migrations/0016_voucher_gst_line.sql:42 `hsn TEXT NOT NULL DEFAULT ''`.
  - *how to tell:* Key an F8 GST sale, then: select line_no, hsn, gst_rate_pct, taxable_value from voucher_gst_line where voucher_version_id=<new version>. If the defect is real, hsn = '' (empty string, not null). If not, hsn carries a code — which would mean an HSN box exists somewhere I did not find.
- **[high]** LEAD 5 CONFIRMED: a blank HSN produces a GSTR-1 table-12 row with hsn_sc = "", and nothing anywhere refuses it. Three separate comments in this repo assert the opposite — that a blank HSN 'takes the supply OUT of the table-12 summary' — and all three are false.
  - *evidence:* backend/internal/compliance/gstr/gstr1.go:485-487 states the intent outright ("gl.hsn = '' is included rather than skipped"); the GROUP BY at :516 buckets '' like any code; build() at :718-722 sets `HsnSc: hr.hsn` with no guard. Contradicted by backend/internal/ledger/ledger.go:712-713, frontend/src/api/types.ts:996 and frontend/src/screens/VoucherScreen.tsx:293, which all claim a blank HSN leaves the summary. migrations/0016_voucher_gst_line.sql:11-12 already records that this exact bug shipped once ("gstr1.go consequently hardcoded hsnKey{hsn: \"\"} and every generated return carried \"hsn_sc\": \"\". HSN is mandatory in a real GSTR-1 table 12") — 0016 fixed the SOURCE of the blank, not the emission of it. WHERE THE REFUSAL BELONGS: BOTH, and neither has it. (a) SERVER is the authority — compliance/tax.go:143-151 copies line.HSN into LineTax with no validation, and ledger.go:2044-2050 inserts it; the refusal belongs in ledger.post()'s GST branch beside the existing deriveAndVerifyGST, as a named sentinel (ErrHSNRequired) on any gst.lines entry with a blank HSN on a SALES/CREDIT_NOTE/DEBIT_NOTE class. (b) CLIENT must gain the field before the server can refuse, or every F8 sale becomes unkeyable — an HSN input in the gst-panel at VoucherScreen.tsx:1970 plus a pre-send check in gstVoucher.ts:556.
  - *how to tell:* Run the table-12 oracle SQL above BEFORE and AFTER keying the sale. If real, a row with hsn_sc = '' appears (or its txval grows by 10000). It is ALREADY true of the shipped data: 52 gst lines with hsn='' totalling 443,000.0000 of taxable value across live vouchers; 308,000 of that survives the table-12 WHERE clause. If not real, no empty-hsn bucket comes back and the POST was refused with a named error.
- **[medium]** Place of Supply is unvalidated end to end: left blank on a company whose home state is 29, BOTH the client and the server compute intra=false and the sale is posted as IGST — an inter-state supply to nowhere — and it lands in GSTR-1 b2cs/b2cl keyed on an empty pos.
  - *evidence:* backend/internal/ledger/ledger.go:2004 `pos = nullIfEmpty(in.GST.PlaceOfSupplyStateCode)` is the ONLY place blankness is handled and it just stores NULL; backend/internal/compliance/tax.go:135 `intra := input.SupplierStateCode == input.PlaceOfSupplyStateCode` -> "29" == "" is false -> IGST. frontend/src/screens/vouchers/gstDerive.ts:74-76 agrees (its guard only rejects a blank SUPPLIER state), so no 422 mismatch fires. gstr1.go:687/697 then key b2clByPOS / b2csKey on in.pos, which is ''.
  - *how to tell:* Key an F8 sale with Taxable 10000, Rate 18, Place of Supply LEFT EMPTY, Ctrl+A. If real: the posting oracle shows C 'Output IGST' 1800 with no CGST/SGST row, and `select place_of_supply, igst from voucher_version where voucher_no='<no>'` returns (NULL, 1800.0000) — an IGST liability on a voucher that names no destination state. If not real, the save is refused with a sentence naming the place of supply.
- **[medium]** invoice.intra() is derived from `igst > 0`, not from the two state codes, so a 0%-rated / exempt INTER-state B2C invoice above the b2cl threshold is classified as INTRA and filed in b2cs with SplyTy "INTRA" instead of in b2cl (table 5A).
  - *evidence:* backend/internal/compliance/gstr/gstr1.go:360-362 `func (in invoice) intra() bool { return !in.igst.GreaterThan(decimal.Zero) }`. The b2cl arm at gstr1.go:685 requires `!in.intra() && in.total().GreaterThan(b2clThreshold)`, and the b2cs default at :694-700 stamps sply = "INTRA" from the same predicate. A nil-rated inter-state supply has igst = 0 by construction (tax.go:153-157 short-circuits a zero rate before the intra/inter branch), so intra() is unconditionally true for it.
  - *how to tell:* Key an F8 sale to a party with NO GSTIN: Taxable 300000, Rate 0, Place of Supply 27 (home is 29). Then curl GET .../gstr1?from=&to= . If real: the invoice appears under "b2cs" with "sply_ty":"INTRA" and "pos":"27" — a self-contradictory row — and "b2cl" is absent. If not real, it appears under b2cl keyed on pos 27.
- **[medium]** The client and server disagree on the intra/inter test when the SUPPLIER state is blank, so on any company with an empty state_code a GST sale with a blank place of supply is rejected with an opaque derived-tax mismatch rather than a sentence naming the missing registration.
  - *evidence:* frontend/src/screens/vouchers/gstDerive.ts:74-76 `intra = supplierStateCode.trim() !== "" && supplierStateCode.trim() === placeOfSupplyStateCode.trim()` (non-empty guard) versus backend/internal/compliance/tax.go:135 `intra := input.SupplierStateCode == input.PlaceOfSupplyStateCode` (no guard). ledger.go:1297-1303 back-fills SupplierStateCode from company.state_code, which for several live companies is '' (psql: E2E Edit-Loop Co, E2E Numbering Co, E2E OCR Co, Stat E2E Co, Report Co all have state_code = ''). With both sides blank the server says intra (CGST/SGST) and the client said inter (IGST).
  - *how to tell:* Switch to a company whose state_code is '' and key an F8 GST sale with Place of Supply left blank. If real: 422 whose message is about the client's tax lines diverging from the server's derivation, with nothing pointing at the unset company state. If not real: it saves, and `select cgst,sgst,igst from voucher_version` shows which side won.

## ROW 16 — e-Way Bill (Alt+Z) — and LEAD 1, the step-up client swap that wipes the screen

**Reachable from the keyboard today:** YES for the screen. Alt+Z is bound globally and macOS Option composition is handled by code-based normalization. UNCLEAR whether the LEAD-1 symptom reproduces on every run: it depends on whether React flushes the client-swap effect BEFORE or AFTER the retry POST resolves (see prediction 3) — but every ordering produces the SAME operator-visible outcome, a keypress that does nothing.

**Keystrokes**

```
Alt+Z from anywhere (global binding, frontend/src/keystroke/keymaps/tallyprime.ts:193). The macOS Option-composes defect is FIXED — normalize.ts:114-119 falls back to `ev.code` matching /^Key([A-Z])$/, so Option+Z arriving as "Ω" still resolves to Alt+Z. Then: click a radio in "The consignment's document" (the picker lists only classes in GOODS_CLASSES, EWayBillScreen.tsx:80-86 — pick an ITEM invoice, not an accounting-mode Sales, or the server refuses it for having no stock lines). Type into "Approximate distance (km)". Ctrl+A.

TO DRIVE LEAD 1 you must make this the FIRST WRITE OF A FRESH SESSION — sign out, sign in, go straight to Alt+Z, and press Ctrl+A with the distance field LEFT BLANK. That 403 STEPUP_REQUIRED -> passkey -> retry -> 422 sequence is the only thing that replaces the session token and hence the `client` object (App.tsx:114-139).
```

**Oracle**

```
The screen is the WEAKEST oracle and here it is the thing under test, so pair it with the wire.

1. Network: the retry POST /v1/companies/{id}/e-way-bills must be visible in devtools returning 422 with a JSON body carrying `blockers` / `violations` / `basis` (the arrays EWayBillScreen.tsx:288-296 unpacks). THE DEFECT IS: that 422 arrived and the screen shows nothing.
2. On the SUCCESS path, the artefact is server-side, so read it: select local_reference, distance_km, validity_days, valid_until, vehicle_number, over_dimensional_cargo, generation_count from e_way_bill where voucher_id = '<uuid>' order by generation_count desc. Assert distance_km EQUALS WHAT WAS TYPED — not merely that a row exists.
3. Assert the stock side too, per the oracle rule: select si.name, sm.direction, sm.qty, sm.amount from stock_movement sm join stock_item si on si.id=sm.stock_item_id join voucher_version vv on vv.id=sm.voucher_version_id where vv.voucher_no='<no>' — a bill raised against a voucher whose VALUE is wrong still balances.
```

**Predictions**

- **[high]** LEAD 1 CONFIRMED UNFIXED on EWayBillScreen. Its load effect is keyed on `client` and clears the server's refusal UNCONDITIONALLY; EInvoiceScreen fixed exactly this by gating the reset on the SELECTION having changed, and EWayBillScreen received no part of that fix.
  - *evidence:* frontend/src/screens/EWayBillScreen.tsx:194-233 — deps are `[client, companyID, selectedID]` (line 233) and lines 201-202 are a bare `setRefusal(""); setRefusalDetail([]);` with no guard of any kind. Compare frontend/src/screens/EInvoiceScreen.tsx:136 (`const shownForRef = useRef("")`) and :218-226 (`const selectionChanged = shownForRef.current !== selectedID; shownForRef.current = selectedID; if (selectionChanged) { setRefusal(""); setRetryable(false); setWriteDenied(false); setRaised(null); setPinned(null); }`). `shownForRef` occurs in EInvoiceScreen.tsx and NOWHERE ELSE in frontend/src. EInvoiceScreen.test.tsx:246-342 is the regression suite for it and has no EWayBill counterpart. THE EDIT I WOULD MAKE (not made): add the same `shownForRef` ref to EWayBillScreen and wrap lines 201-202 in `if (shownForRef.current !== selectedID)`.
  - *how to tell:* Fresh session, Alt+Z, select an item invoice, leave distance BLANK, Ctrl+A. If real: the passkey ceremony runs, devtools shows the retry POST answering 422 with a `blockers` array naming the unstated distance, and the screen's role="alert" block is EMPTY — the key looks dead. Press Ctrl+A a SECOND time (no step-up now, so no client swap) and the identical refusal renders and STAYS: that second press is the control that proves the swap is the cause. If not real, the refusal renders on the first press.
- **[medium]** A second, distinct unconditional write in the same effect: the GET's success handler overwrites the operator's TYPED consignment — distance, vehicle, mode, transporter, ODC and both pincodes — from the previously-raised bill on every effect re-run, so a client swap mid-raise silently replaces what they keyed. EInvoiceScreen has no analogue because it has no form, so the shownForRef fix as written would not have covered this even if it had been copied over.
  - *evidence:* frontend/src/screens/EWayBillScreen.tsx:207-218 — inside `.then((b) => {...})`, `setDistance(String(b.distance_km)); setVehicle(b.vehicle_number); setMode(...); setTransporterID(...); setTransporterName(...); setOdc(b.over_dimensional_cargo); setFromPincode(...); setToPincode(...)`. Guarded only by `current()` (line 206), which is a staleness check, not an intent check. The effect re-runs on `client` identity change (line 233).
  - *how to tell:* Raise a bill for voucher A with distance 100 (this also burns the session's step-up). Sign out and back in. Alt+Z, select A — the form pre-fills 100. Change it to 250 and press Ctrl+A. If real: after the ceremony the distance box reads 100 again, and the e_way_bill row for the new generation_count has distance_km = 100 (or the raise is discarded entirely). Oracle: select generation_count, distance_km, validity_days from e_way_bill where voucher_id='<A>' order by generation_count — a new row at 100 km, or no new row at all, both falsify "the operator's 250 was used". If not real, distance_km = 250.
- **[medium]** There is a THIRD path to the same blank screen that shownForRef does NOT close, and it is present on BOTH screens including the supposedly-fixed EInvoiceScreen: the load effect bumps `generationRef` on every run, so if it re-runs while the raise is still in flight, the raise's own generation check throws away the result — the bill, the ack number and the refusal alike.
  - *evidence:* EWayBillScreen.tsx:199 `const mine = ++generationRef.current;` inside the effect, plus the cleanup at :230-232 which bumps again; the raise closure captured its own `mine` at :252 and bails at :271 (success) and :278 (failure) on `generationRef.current !== mine`. EInvoiceScreen is identical: :191 in the effect, :243-245 cleanup, :273 claim, :279/:283 bail. EInvoiceScreen.test.tsx:306-342 only exercises the ordering where the client swaps AFTER the raise has already settled (it awaits `findByRole("alert")` before calling rerender), so the in-flight ordering is untested on both screens.
  - *how to tell:* Same drive as prediction 1, but watch which failure it is. If the generation guard is the culprit, the second Ctrl+A control ALSO shows nothing on the very first attempt of a session yet the server row EXISTS: select count(*) from e_way_bill where voucher_id='<uuid>' returns 1 while the screen shows no "Raised" section. That psql count is the discriminator — a wiped-by-reset failure leaves no row on the 422 path, whereas a discarded-by-generation failure on the SUCCESS path leaves a row the operator cannot see.
- **[medium]** MembersScreen is the only OTHER screen with the same shape. Its `refresh` unconditionally clears `error`, is keyed on `client`, is fired by a bare effect, and `error` is the very banner a write refusal lands in. Every other client-keyed loader in the tree is clean because it keeps write state in separate variables the loader never touches.
  - *evidence:* SAME SHAPE: frontend/src/screens/MembersScreen.tsx:183-209 (`const refresh = useCallback(async () => { setLoading(true); setError(null); ... }, [client, companyID])`, reset at :185) + :211-213 (`useEffect(() => { void refresh(); }, [refresh])`); the write path `run` at :240-254 puts its refusal into the SAME `setError(explain(e))` at :249. CLEAN, verified by reading each: EInvoiceScreen.tsx:150-173 (resets only listError/denied; refusal/retryable/writeDenied are separate and untouched), TDSChallansScreen.tsx:218-242 (same split; refusal/createDenied/notice untouched), CostCentresScreen.tsx:164-186, BomsScreen.tsx:110-131, OrderRegisterScreen.tsx:227-247 (refusal/writeDenied/notice declared at :214-216 and never reset by load), BankStatementImportPanel.tsx:190-200, BankReconciliationScreen.tsx:102-142 (sets error, never blanket-clears it). GUARDED BY A SEED REF so a client swap does not re-fetch at all: VoucherScreen.tsx:918-937 and vouchers/StockVoucherScreen.tsx:157-183.
  - *how to tell:* Fresh session. Gateway -> "Users & Roles" -> attempt a level change that the PDP will refuse (or that 409s). If real: the passkey ceremony runs, the retry returns non-2xx, and the red banner is empty — repeat the same action a second time in the same session and the banner appears and stays. If not real, the banner shows on the first attempt. For the clean screens the same drive shows the refusal on the first press, which is the negative control.

## ROW 17 — TDS/TCS — record a challan and allocate it; the challan's unallocated balance must fall

**Reachable from the keyboard today:** YES for record-and-allocate-in-one-keystroke. NO for "record a challan and THEN allocate it" — there is no allocate-later route (see prediction 1). BLOCKED ON DATA as the DB stands: the only company holding TDS deductions is `T6 Drive Co` (9466b85c-917c-4bc7-a5d6-8211d1d3199d, state 27) and all three of its 2026 deductions are already paid, so the picker will render the `tds-allocatable-paid:*` spans and no tickable row. On `Demo Traders` the year-2026 register is empty and the picker renders `tds-allocatable-empty`. A TDS-bearing voucher must be keyed first.

**Keystrokes**

```
From Gateway: Down x8 to "TDS Challans" (GATEWAY_MENU order is Vouchers, Bill Inbox, Query, Day Book, Balance Sheet, Profit & Loss A/c, Stock Summary, Bank Reconciliation, TDS Challans — screens/GatewayScreen.tsx:4-22), Enter (navMachine.ts:688-695 intercepts the label before the report fallback). Type 2026 into "Tax year begins in" and press Enter. Alt+C (screen-scoped, keymaps/tallyprime.ts:366-370) opens the deposit form. Fill Deposit date 2026-07-07, BSR code 0001234, Challan serial 05130, Tax 20000, Minor head radio "200", Tax year 2026, Recorded by <name>. TICK a row in "What this deposit paid" (checkbox aria-label = "Pay the <amount> deducted on voucher <no> under <section> from <deductee> on <date>"). Ctrl+A.
```

**Oracle**

```
select c.challan_no, c.tax_rupees, coalesce(sum(a.allocated_amount_rupees),0) allocated, c.tax_rupees - coalesce(sum(a.allocated_amount_rupees),0) unallocated, count(a.*) n, string_agg(l.name||'/'||a.section||'/leg'||a.leg_seq||'='||a.allocated_amount_rupees, ' | ') who from tds_challan c left join tds_challan_allocation a on a.company_id=c.company_id and a.challan_id=c.id left join tds_deduction d on d.company_id=a.company_id and d.voucher_id=a.voucher_id and d.section=a.section and d.leg_seq=a.leg_seq left join ledger l on l.id=d.deductee_ledger_id group by c.id,c.challan_no,c.tax_rupees order by 1;

Assert the `who` column — the DEDUCTEE NAME, the SECTION and the LEG — not just that `unallocated` moved. A wrong allocation to a different deductee's leg for the same rupees leaves `unallocated` at exactly the right figure. I ran this read-only: the two existing challans read 05122 tax 32000 -> allocated 32000, who = "Chitra Consulting/194J-P/leg0=20000.0000 | Meera Legal/194J-P/leg0=12000.0000"; 05123 tax 9000 -> allocated 9000, who = "Chitra Consulting/194J-P/leg0=9000.0000". Both unallocated 0.0000.

Cross-check the screen against the server's own field: data-testid="tds-challan-unallocated:<challan_no>" renders UnallocatedTaxRupees RAW at 4 dp (TDSChallansScreen.tsx:1025-1027), so it must match the SQL to the paisa.
```

**Predictions**

- **[high]** The unallocated balance can only ever fall at CREATE time and can NEVER be reduced afterwards: there is no route to attach an allocation to an existing challan, so "record a challan and allocate it" is not two steps and cannot be driven as two steps.
  - *evidence:* backend/internal/server/server.go:846-847 registers exactly two routes — `r.Get("/tds-challans", ...)` and `r.Post("/tds-challans", ...)`. No PATCH, no PUT, no /allocations sub-route. backend/internal/masters/tds_challan.go:495-506 refuses a tax-bearing challan with zero allocations and says so in as many words: "this slice has no allocate-later route, so a challan stored without its allocations could not be completed afterwards". The screen's whole write path is the single `client.createTDSChallan` at TDSChallansScreen.tsx:348-361.
  - *how to tell:* Record a valid challan with allocations. Then try to add another allocation to it: there is no key, no button and no field on ChallanCard (TDSChallansScreen.tsx:943-1036) that does it, and Alt+C opens a NEW deposit form. If the checklist row means the two-step reading, mark it unreachable. Oracle proof it is not merely a UI omission: `curl -X PATCH .../tds-challans/<id>` returns 405.
- **[medium]** Nothing caps the sum of allocations at the challan's tax, so a challan can "pay" many times what it deposited and the unallocated balance goes NEGATIVE — and the screen pre-excuses exactly that reading, which is what hides it.
  - *evidence:* backend/internal/masters/tds_challan.go:495-516 is the entire allocation-vs-tax validation and it checks only two things: positive tax with zero allocations (:500) and zero tax with allocations (:511). Nothing anywhere compares sum(Allocations) to TaxRupees — grep for TaxRupees in that file returns no comparison against an allocation total. tds_challan.go:940 then computes `UnallocatedTaxRupees = TaxRupees.Sub(AllocatedTotalRupees)` with no floor. frontend/src/screens/TDSChallansScreen.tsx:1029-1033 tells the operator in advance that a non-zero figure "is not an error" and "can also be negative", so a -40,000 reads as the documented paise remainder. The per-deduction guards that DO exist (ErrTDSAllocationMismatch at :144, ErrDeductionAlreadyPaid at :152) constrain each allocation individually and cannot see the total. This is precisely the over-claim ErrDuplicateTDSChallanCIN's own comment (tds_challan.go:114-123) exists to prevent — "the return's batch total is the sum of the challan deposits, so a duplicate over-claims a deposit that never happened" — reachable here without any duplicate.
  - *how to tell:* On T6 Drive Co, key a new TDS voucher so at least two unpaid deductions exist, then record ONE challan with Tax = 1 and tick BOTH. If real: the server answers 201 and the oracle returns unallocated strongly negative (e.g. tax_rupees 1.0000, allocated 41000.0000, unallocated -40999.0000) with both deductees named in `who`; the screen prints that negative under tds-challan-unallocated and captions it as normal. If not real, the POST is refused with a sentence naming the deposit's tax box.
- **[high]** Row 17 cannot be driven at all on the database as it stands, and the screen's explanation of why will point the operator at the wrong cause.
  - *evidence:* psql: the only rows in tds_deduction belong to company 9466b85c-917c-4bc7-a5d6-8211d1d3199d (`T6 Drive Co`), tax_year_start 2026, 3 rows totalling 41000.0000 — and the oracle above shows all three already allocated across challans 05122 and 05123. tds_challan_allocation_one_per_deduction (referenced at tds_challan.go:150) makes each one un-tickable, so the picker renders `tds-allocatable-paid:*` spans (TDSChallansScreen.tsx:898-900) for every row and setChosen can never become non-empty; a tax-bearing challan then hits the refusal at tds_challan.go:500. On any other company the picker instead renders `tds-allocatable-empty` (TDSChallansScreen.tsx:848), whose text blames "the deduction behind it is not in these books yet".
  - *how to tell:* Open TDS Challans on the default company, type 2026, Alt+C. If real: the "What this deposit paid" table is absent and data-testid="tds-allocatable-empty" is on screen, so Ctrl+A with any Tax figure returns the server's zero-allocation refusal. Fix by keying a TDS-liable Payment/Journal first (a party with a TDS profile), then re-check `select voucher_id, section, leg_seq, tds_amount_rupees, tax_year_start from tds_deduction where company_id=<current>` before opening the form.

## ROW 8 — Cost centres + BOTH cost summary reports (Cost Centre Summary / Cost Category Summary)

**Reachable from the keyboard today:** yes from the GATEWAY only. NO — not from Alt+G. The Go To picker's option list is hard-wired to `["Vouchers", ...REPORT_NAMES]` (defaultScreens.tsx:59) and REPORT_NAMES (types.ts:285-333) contains neither cost screen; SelectionList is built with allowCreate=false so a typed string that matches nothing is not selectable at all (SelectionList.tsx:89-92).

**Keystrokes**

```
From Gateway (root frame): type `cost centre summary` into the SEARCH combobox, ArrowDown until the highlighted row is exactly "Cost Centre Summary", Enter (one Enter both selects and drills — SelectionList deliberately does not stopPropagation, SelectionList.tsx:110-118). The axis picker appears (demo has 2 axes) -> type `Department`, Enter. F2 -> Period from `2026-04-01`, to `2026-08-31`, Enter/Apply. Then Esc back to Gateway, type `cost category summary`, ArrowDown to it, Enter, F2 same period. On the Category Summary, ArrowDown/ArrowUp move the row cursor (DrillRows.tsx:110-119) and Enter drills into that axis's Cost Centre Summary carrying the period (navMachine.ts:603-635). Alt+C on Gateway > Cost Centres opens the master create panel; Ctrl+A accepts.
```

**Oracle**

```
psql, scoped to the company the browser is on (newest company row):
\set cid '<company uuid>'
-- (A) THE PER-(POSTING,CATEGORY) TRUTH. Assert LEDGER NAMES + SIDE + VALUE, never a total:
select cat.name axis, ctr.name centre, l.name ledger, p.dr_cr, p.amount posting_amt, a.amount alloc_amt
  from posting_cost_allocation a
  join posting p on p.id=a.posting_id
  join ledger l on l.id=p.ledger_id
  join cost_category cat on cat.id=a.cost_category_id
  join cost_centre ctr on ctr.id=a.cost_centre_id
 where a.company_id=:'cid' order by 1,2;
On the seeded demo this returns exactly 6 rows: Branch/Bengaluru Rent D 45000 -> 31500; Branch/Mumbai Rent D 45000 -> 13500; Department/Admin Electricity D 8600 -> 3440; Department/Admin Rent D 45000 -> 18000; Department/Sales Electricity D 8600 -> 5160; Department/Sales Rent D 45000 -> 27000.
-- (B) THE PER-AXIS SUM (what the report must show):
select cat.name axis, sum(a.amount) from posting_cost_allocation a join cost_category cat on cat.id=a.cost_category_id where a.company_id=:'cid' group by 1;
=> Branch 45000.0000, Department 53600.0000.
CORRECT looks like: Cost Centre Summary/Department shows own_debit Sales 32160.0000 and Admin 21440.0000, `cost-centre-summary-total-debit` = 53600.0000, `cost-centre-summary-allocated` = 53600.0000, `cost-centre-summary-difference` = 0.0000, reconciles = true; switching the axis with F4 to Branch shows Bengaluru 31500 / Mumbai 13500, total 45000.0000. The number 98600 (53600+45000) MUST NOT APPEAR ANYWHERE on either screen or in either Alt+E export, and the Category Summary must render `cost-category-summary-no-total` and no footer row.
```

**Predictions**

- **[high]** Neither cost report is reachable from Alt+G (Go To). The Go To picker lists only ["Vouchers", ...REPORT_NAMES] and neither "Cost Centre Summary", "Cost Category Summary" nor "Cost Centres" is in REPORT_NAMES; worse, resolveGotoSelection ALWAYS builds a `{screen:"report", reportName}` frame, so even if a label were added to the list it would land on UnwiredReportScreen ("not available in this build") rather than on the built, ACL-gated screen.
  - *evidence:* frontend/src/app/defaultScreens.tsx:59 `options={["Vouchers", ...REPORT_NAMES]}`; frontend/src/keystroke/types.ts:285-333 (REPORT_NAMES ends at Sales/Purchase Order Book); frontend/src/keystroke/navMachine.ts:430-438 `resolveGotoSelection` -> `{ screen: "report", reportName }`; navMachine.ts:176-187 states the three cost labels are deliberately NOT report names.
  - *how to tell:* Press Alt+G and type `cost`. If real: the listbox shows ZERO options (only "Vouchers"+the 10 report names are candidates) and Enter does nothing. If not real: a "Cost Centre Summary" row appears and Enter opens the axis picker. Repeat with `bank` for row 11.
- **[high]** Ctrl+F (filter) and F12 (detailed) are INERT on both cost summaries — the three ScreenKind screens were added to the F2 `period` case but not to `filter`/`detailed`, which still gate on isReportScreen = report|report-detail only. The Cost Category Summary nevertheless populates DrillRow.values specifically for filtering, so that plumbing is dead — and the values it publishes are the raw fixed-4 wire strings ("53600.0000") while the cells render formatMoney ("53,600.00"), which contradicts DrillRows' documented contract that values is "the DISPLAY text, exactly as rendered".
  - *evidence:* frontend/src/keystroke/navMachine.ts:496-497 `isReportScreen = report|report-detail`; :874-879 the period case explicitly adds bank-reconciliation + both cost screens; :885-888 `case "filter": if (isReportScreen) return openOverlay("filter")` — no such addition; frontend/src/screens/reports/CostCategorySummaryScreen.tsx:317-321 `values: [r.cost_category, r.allocable_debit, ...]` (raw) vs :330-347 formatMoney cells; frontend/src/screens/reports/DrillRows.tsx:35-41.
  - *how to tell:* On the Cost Category Summary press Ctrl+F: if real, no filter overlay appears and the shell reports the key does not apply on this screen (compare with Day Book, where Ctrl+F opens the overlay). Same for F12.
- **[high]** The two cost reports silently render NOTHING (only the F2 prompt) when pushed from the Gateway, because the Gateway push carries no config and both screens refuse to request without BOTH dates — only the drill from the Category Summary copies the period. An operator who reaches the Cost Centre Summary from the Gateway sees the axis picker, picks an axis, and then gets a paragraph instead of a report.
  - *evidence:* frontend/src/keystroke/navMachine.ts:708-717 pushes `{ screen: "cost-centre-summary" }` with no config; CostCentreSummaryScreen.tsx:203-208 `if (!axisID || !from || !to) { setData(null); return; }` and :419-431 renders `cost-centre-summary-period-prompt`; contrast navMachine.ts:627-634 where the drill DOES copy periodFrom/periodTo.
  - *how to tell:* Gateway > Cost Centre Summary > pick Department: if real, `[data-testid=cost-centre-summary-period-prompt]` is present and no table is drawn until F2 is used. Not a wrong number — a reachability/usability finding to note before assuming the report is broken.

## ROW 9 — Cost allocation during entry across TWO axes (both complete, both independent)

**Reachable from the keyboard today:** yes. The picker is rendered on BOTH grids: single entry VoucherScreen.tsx:2311-2315 and double entry DoubleEntryPanel.tsx:115-119. It is deliberately absent in ITEM-invoice mode (server generates those postings).

**Keystrokes**

```
F5 (Payment). Party/Account = `HDFC Bank`. Particular 0: ledger `Rent`, amount `1000`. On that row press the `allocate` link (data-testid `alloc:0-add`), then: axis select `alloc:0-axis:0` -> Department, centre `alloc:0-centre:0` -> Sales, amount `alloc:0-amount:0` -> `600`; `alloc:0-more` -> axis Department / centre Admin / `rest` button (`alloc:0-rest:1`); `alloc:0-more` again -> axis Branch / centre Bengaluru / `700`; `alloc:0-more` -> axis Branch / centre Mumbai / `alloc:0-rest:3`. `alloc:0-remainders` must show TWO lines, both "allocated in full (1000.0000)", and `alloc:0-parallel` must say "analysed on 2 axes IN PARALLEL". Ctrl+A to accept. Then re-open the same voucher (Day Book > Enter) and Ctrl+A again with NOTHING changed — the alter round-trip is the half that historically destroys splits. Same drill on F7 (Journal) via DoubleEntryPanel (`de-ledger:0`, `de-amount:0`, `alloc:0-*`).
```

**Oracle**

```
psql, after the accept and again after the no-op alter:
select vv.voucher_no, vv.version_no, l.name ledger, p.dr_cr, p.amount posting_amt,
       cat.name axis, ctr.name centre, a.amount alloc_amt
  from posting_cost_allocation a
  join posting p on p.id=a.posting_id
  join voucher_version vv on vv.id=p.voucher_version_id
  join voucher_head vh on vh.current_version_id=vv.id and vh.is_deleted=false
  join ledger l on l.id=p.ledger_id
  join cost_category cat on cat.id=a.cost_category_id
  join cost_centre ctr on ctr.id=a.cost_centre_id
 where vv.voucher_id='<the new voucher_id>' order by cat.name, ctr.name;
CORRECT: FOUR rows, all with ledger = 'Rent' and dr_cr = 'D' (never 'HDFC Bank' — that is the primaryLegCount off-by-one), Branch/Bengaluru 700 + Branch/Mumbai 300 = 1000, Department/Sales 600 + Department/Admin 400 = 1000. Per-axis sum must equal 1000.0000 EXACTLY, and the two axes must NOT be summed to 2000 anywhere.
After the no-op alter: the SAME four rows, now hanging off the NEW version's posting ids —
select count(*) from posting_cost_allocation a join posting p on p.id=a.posting_id join voucher_version vv on vv.id=p.voucher_version_id join voucher_head vh on vh.current_version_id=vv.id where vv.voucher_id='<id>';  -- must still be 4, not 0.
```

**Predictions**

- **[high]** A voucher whose cost split is INCOMPLETE on one axis is refused by the server (422) only AFTER the client has already created every ledger it named. `buildSingleEntryRequest` calls ensureLedger for the party and each particular before the request is sent, and there is no client-side gate on allocationRefusal — VoucherScreen imports only allocationsFromView/buildCostAllocations. So Ctrl+A on a partial axis leaves an orphan LEDGER in the chart of accounts with no voucher behind it — the exact failure buildDoubleEntryRequest documents and guards against for the BALANCE, but neither builder guards against for allocations.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:1012-1035 (ensureLedger for party + every row) then :1060 buildCostAllocations, :1084 return req — validation happens server-side in backend/internal/ledger/cost_allocation.go:275-297; VoucherScreen.tsx:49-50 imports allocationsFromView + buildCostAllocations and NOT allocationRefusal (which exists at frontend/src/screens/vouchers/costAllocation.ts:196); contrast VoucherScreen.tsx:1100-1113 "VALIDATE BEFORE RESOLVING, AND THE ORDER OF THESE TWO BLOCKS IS A CORRECTNESS PROPERTY" — which only calls doubleEntryRefusal (balance), not allocationRefusal.
  - *how to tell:* Snapshot `select count(*) from ledger where company_id=:'cid'` and `select count(*) from voucher_head where company_id=:'cid'`. Key a Payment with a BRAND NEW ledger name (e.g. `Marketing Spend Q3`) for 1000, allocate Department/Sales 400 only, Ctrl+A. If real: the server answers 422 (the picker's `alloc:0-refusal` was already on screen), voucher_head count is UNCHANGED, and `select name from ledger where name='Marketing Spend Q3'` returns a row. If not real: no ledger row exists.
- **[medium]** The voucher READ-BACK orders postings `ORDER BY p.dr_cr DESC, p.amount DESC` with NO tiebreak on p.id, and cost_allocations[].posting_seq is derived from that order. Two postings on the same side with the SAME amount are therefore ordered arbitrarily, so an ALTER round trip can re-attach a departmental split to the wrong ledger — landing on a real ledger with a plausible amount, on a voucher that still balances and a Cost Centre Summary that still foots. The bank-reconciliation leg ordinal on the same table takes the opposite (correct) choice, ORDER BY p.id.
  - *evidence:* backend/internal/ledger/ledger.go:841-846 `ORDER BY p.dr_cr DESC, p.amount DESC` feeding postingSeqByID at :858; backend/internal/banking/banking.go:62-65 and backend/internal/reporting/bankrecon.go:164-167 both use `ORDER BY p.id` for the analogous ordinal; frontend/src/screens/VoucherScreen.tsx:418-439 matches allocations by that seq.
  - *how to tell:* Key a Payment with TWO particulars of the SAME amount and DIFFERENT ledgers (Rent 5000, Electricity 5000) and allocate ONLY Rent (Department/Sales 5000). Save, note `select l.name from posting_cost_allocation a join posting p on p.id=a.posting_id join ledger l on l.id=p.ledger_id where ...` = 'Rent'. Now open the voucher and Ctrl+A with nothing changed. If real: the allocation's ledger comes back 'Electricity' (or the GET's `postings` array order differs between two identical GETs). If not real: it stays 'Rent'. Also check the raw GET twice — /vouchers/{id} postings[] order must be identical on both calls.
- **[medium]** The picker's per-axis remainder counts a split that has an AMOUNT but no CENTRE chosen, so an axis can read "allocated in full" while the split is silently dropped by buildCostAllocations — the fallback text refusal at costAllocation.ts:213-222 is the only thing standing between that and a 422, and it is rendered but not enforced on Ctrl+A.
  - *evidence:* frontend/src/screens/vouchers/costAllocation.ts:163-167 ("SUMMED EVEN WHEN THE CENTRE IS UNPICKED") and :113-116 (buildCostAllocations skips any split with a blank costCentreId); VoucherScreen.tsx does not call allocationRefusal.
  - *how to tell:* On a 1000 particular, add Department/Sales 600 and a second Department split with amount 400 but the centre left at "— centre —". `alloc:0-remainder:Department` will read "allocated in full (1000.0000)" while `alloc:0-refusal` names the missing centre. Press Ctrl+A anyway. If real: the server refuses with "totals 600.0000 on cost category ... but the posting is 1000.0000" and (per the previous prediction) any new ledger is still created. Oracle: no posting_cost_allocation rows for that voucher and no voucher_head row.

## ROW 11 — Bank Reconciliation — does marking a line move the reconciled balance, and does it survive a reload

**Reachable from the keyboard today:** yes from the Gateway. NO from Alt+G — "Bank Reconciliation" is not in REPORT_NAMES so the Go To list cannot offer it, and resolveGotoSelection would push a `report` frame anyway (exactly the trap navMachine.ts:147-155 warns about). F4 on the statement re-opens the bank picker; the write itself is Alt-free.

**Keystrokes**

```
⚠️ NOT Alt+G. From Gateway: type `bank` into the SEARCH combobox ("Bank Reconciliation" is the only match), Enter. The demo has exactly ONE bank ledger (HDFC Bank), so it opens straight onto the statement (F4 would otherwise offer the picker). F2 -> set only "To" (the panel says so: `period-as-at-note`) to `2026-08-31`, Apply. Then click the Bank Date cell of a row — `[data-testid^="brs-bankdate:"]` — type a date >= that row's voucher_date, then Tab/click away to fire onBlur (the input commits on BLUR only, BankReconciliationScreen.tsx:530-534). Read `brs-per-bank`, `brs-uncleared-net`, `brs-difference`, `brs-balanced`. Then press Esc to the Gateway and re-enter the screen (or reload the page) and read them again.
```

**Oracle**

```
THE WRITE IS SERVER-SIDE AND IS THE ONLY TRUSTWORTHY ORACLE. Before/after each tick:
select vv.voucher_no, vv.voucher_date, l.name ledger, p.dr_cr, p.amount,
       br.leg_no, br.bank_date, br.cleared_amount, br.cleared_dr_cr, br.reconciled_by
  from bank_reconciliation br
  join ledger l on l.id = br.ledger_id
  join voucher_head vh on vh.voucher_id = br.voucher_id and vh.is_deleted=false
  join voucher_version vv on vv.id = vh.current_version_id
  join posting p on p.voucher_version_id = vv.id and p.ledger_id = br.ledger_id
 where br.company_id = :'cid' order by vv.voucher_date;
The table is currently EMPTY for every company in the shared DB (`select count(*) from bank_reconciliation` = 0), so any row appearing is yours.
CORRECT: exactly one row per tick, cleared_dr_cr matching the posting's dr_cr and cleared_amount matching p.amount TO THE PAISA (that pair is what makes a later ALTER detectable — bankrecon.go:264-270), reconciled_by='ui'. And on the screen: the ticked line moves from the Un-cleared table to the Cleared table, `brs-uncleared-net` changes by that leg's SIGNED contribution (HDFC Bank is ASSET/debit-natured, so a D leg reduces uncleared_net by its amount and a C leg increases it), `brs-per-bank` = books - uncleared_net moves by the same amount, and `brs-difference` stays 0.0000 with `brs-balanced` = "balanced". After Esc + re-entry the SAME state must be re-rendered from the server.
```

**Predictions**

- **[high]** Ticking a line with a bank date LATER than the F2 "as at" date persists on the server (200, row written) but the screen shows no change at all: the statement re-reads with `br.bank_date <= as_on` in the JOIN, so the line stays in Un-cleared and every balance is unchanged — and because the row keeps the same React key inside the same LineTable the uncontrolled <input defaultValue> is never re-applied, so the Bank Date cell KEEPS DISPLAYING the date on a row the report classifies as un-cleared. Nothing on the page says why.
  - *evidence:* backend/internal/reporting/bankrecon.go:186-190 (`AND ($2::date IS NULL OR br.bank_date <= $2::date)` in the LEFT JOIN); frontend/src/screens/BankReconciliationScreen.tsx:69 `const asOn = snapshot.config?.periodTo`, :215-245 setBankDate -> load(), :494-497 `key={`${l.voucher_id}:${l.leg_no}`}` and :523-535 `defaultValue={l.bank_date}` (defaultValue is mount-only in React).
  - *how to tell:* F2 -> To = `2026-06-30`. Tick the 2026-06-05 C 20000 leg with bank date `2026-07-10`. If real: `brs-per-bank` / `brs-uncleared-net` are byte-identical to before, the row is still under "Un-cleared", its date box still shows 2026-07-10, and psql shows a bank_reconciliation row with bank_date=2026-07-10. If not real: either the write did not happen (no psql row) or the screen moved the line. Then set F2 To = 2026-08-31 and confirm the SAME row now appears under Cleared — that is the proof it was persisted all along.
- **[high]** Every statement row is labelled by voucher_no alone, which is NOT unique on one bank ledger — numbering is per voucher class — so the screen shows several rows all reading "Vch No 1" with no class column to tell them apart, and the DOM ids collide: data-testid `brs-line:1:0` / `brs-bankdate:1:0` and aria-label "Bank date for voucher 1" each match THREE different vouchers on the demo's HDFC Bank. A driver selecting by test id will tick a different voucher than it thinks it is ticking.
  - *evidence:* frontend/src/screens/BankReconciliationScreen.tsx:497 `data-testid={`brs-line:${l.voucher_no}:${l.leg_no}`}`, :525-526 aria-label/testid both keyed on voucher_no; the rendered columns at :484-491 are Date/Particulars/Vch No/Amount/Bank Date/Age with NO voucher class. Verified in the live DB: on ledger 019fdd23-c2d4-7e15-914e-5bb0258b75a3 (HDFC Bank, company be33e0c6-…) there are three legs with voucher_no '1' (2026-04-02 D 100000, 2026-05-20 D 50000, 2026-05-30 D 35000) and three with '2'.
  - *how to tell:* Count matches for `[data-testid="brs-bankdate:1:0"]`. If real: 3 elements (Playwright strict mode will error, or a naive .first() ticks the 2026-04-02 leg). Confirm with the oracle: after ticking "voucher 1" check WHICH voucher_id got the bank_reconciliation row — assert on voucher_date + amount, never on voucher_no.
- **[medium]** A REFUSED tick leaves the screen lying. setBankDate's failure path shows the server message but never reloads, and the Bank Date <input> is uncontrolled, so a date the 0045 trigger rejected (bank_date earlier than the voucher date) stays visible in the cell with the row still under Un-cleared — the operator sees a date they believe they entered and nothing was stored.
  - *evidence:* frontend/src/screens/BankReconciliationScreen.tsx:222-228 `fail` = setError + setBusy(false), no load(); :523-535 uncontrolled input with defaultValue; backend/migrations/0045_bank_reconciliation.sql:174-181 raises when the bank date precedes voucher_head.voucher_date; backend/internal/server/handler_bank_recon.go:147-153 passes that message through as a 400.
  - *how to tell:* On the 2026-06-05 leg enter bank date `2026-05-01`. If real: `brs-row-error` appears with the trigger's sentence, the date box still reads 2026-05-01, and `select * from bank_reconciliation where company_id=:'cid'` returns NO row for that voucher. If not real: the cell reverts to blank.
- **[medium]** With no F2 date set the screen requests the statement with as_on omitted, so the server answers "everything ever posted" and echoes as_on = "" — the header renders "As at " with nothing after it, while age_days is silently computed against TODAY. A reconciliation printed/read in that state states no date at all.
  - *evidence:* frontend/src/api/rest.ts:183-190 (`asOn ? { as_on: asOn } : undefined`); backend/internal/server/handler_bank_recon.go:94 `r.URL.Query().Get("as_on")` -> ""; backend/internal/reporting/bankrecon.go:201 `out := BankReconciliation{... AsOn: asOn}` and :296-302 `resolveAsOn(asOn)` falls back to today for ageing; frontend/src/screens/BankReconciliationScreen.tsx:345-348 renders `As at {brs.as_on}`.
  - *how to tell:* Open the screen WITHOUT pressing F2 and read `[data-testid="brs-as-on"]`. If real: it reads "As at" with an empty date (and the Age column still shows plausible day counts). If not real: it shows a resolved date.

## ROW 27 — Sales orders (Ctrl+F8) / purchase orders (Ctrl+F9) / BOM explode (Alt+X) — order book Ordered / Delivered / Outstanding, and the invariant that AN ORDER MOVES NO STOCK

**Reachable from the keyboard today:** YES for both registers and the BOM screen. Ctrl+F8/Ctrl+F9 are bound globally (tallyprime.ts:198-199), are NOT held back on the registers themselves (tallyprime.ts:608-614), are dispatched by the shell, and are in ALWAYS_MEANINGFUL (AppShell.tsx:165-170) so pressing one on the register you are already on still reports success. Alt+C / Alt+W / Alt+C / Alt+X are all present in the shell's SCREEN_ACTION map (AppShell.tsx:141-145), which is the registration point that killed Alt+O silently. Alt+X survives the macOS Option-compose bug because baseKeyOf reads ev.code (normalize.ts:113-123). Two caveats: (1) the BOM screen has NO key at all — its only door is the Gateway entry "Bills of Materials"; (2) Ctrl+F8 is a real macOS system shortcut ("move focus to the status menus") so a human with Full Keyboard Access on may never see it reach the page — comboFromEvent maps metaKey to Ctrl (normalize.ts:132) so Cmd+F8 is an equivalent fallback worth trying if Ctrl+F8 appears dead.

**Keystrokes**

```
REGISTERS: from Gateway press Ctrl+F8 (Sales Orders) / Ctrl+F9 (Purchase Orders). Both are bound at frontend/src/keystroke/keymaps/tallyprime.ts:198-199 -> navMachine.ts:990 -> defaultScreens.tsx (sales-orders / purchase-orders). CREATE: Alt+C (screen override tallyprime.ts:618) opens the form; Tab -> "Order number", Tab -> "Order date" (date input, no default), Tab -> "Customer"/"Supplier" <select>, Tab -> "Narration", Tab -> line 1 "Item on line 1" <select>, "Quantity on line 1", "Rate on line 1", "Due date on line 1"; Ctrl+A accepts (OrderRegisterScreen.tsx:563). SELECT A ROW: Tab to the "Select order <no>" button in the register table (OrderRegisterScreen.tsx:995-1003), Enter. PRE-CLOSE: Alt+W (tallyprime.ts:621) -> type the required reason -> Ctrl+A. BOM: Esc back to Gateway, arrow to "Bills of Materials" (GatewayScreen.tsx:58 -> navMachine.ts:744), Enter; Alt+C defines a recipe; Alt+X explodes the highlighted one (tallyprime.ts:654), type the batch qty, Ctrl+A. ORDER BOOKS: Alt+G -> type "Sales Order Book" / "Purchase Order Book" -> Enter (types.ts:312-333); F2 on the book sets the as-at date (only "To" applies, FrameOverlays.tsx:250-252); Alt+E exports. NOTE F2 is deliberately dead on the two registers and on the BOM screen (tallyprime.ts:625, 635, 656).
```

**Oracle**

```
A. THE INVARIANT (an order moves no stock). Snapshot before and after keying an order:
  SELECT count(*) FROM stock_movement;
  SELECT count(*) FROM posting;
  SELECT count(*) FROM voucher_head;
Correct = ALL THREE UNCHANGED across Ctrl+F8 -> Alt+C -> Ctrl+A, while order_head/order_line gain rows. Structurally there is no column that could carry it:
  SELECT column_name FROM information_schema.columns WHERE table_name='stock_movement';
Correct = no order_id / order_line_id column; the ONLY orders<->vouchers table is order_fulfilment, written by internal/ledger inside the voucher transaction (fulfilment.go:332).

B. Ordered / Delivered / Outstanding, WITH VALUE and with the party's GROUP (never a total):
  SELECT oh.direction, oh.order_no, oh.order_date, l.name AS party, ag.name AS party_group,
         ol.line_no, si.name AS item,
         ol.qty AS ordered, ol.rate,
         COALESCE((SELECT SUM(f.qty) FROM order_fulfilment f
                     JOIN voucher_head vh ON vh.company_id = f.company_id
                                         AND vh.current_version_id = f.voucher_version_id
                     JOIN voucher_version vv ON vv.id = f.voucher_version_id
                    WHERE f.order_line_id = ol.id AND vh.is_deleted = false
                      AND vv.voucher_date <= DATE '2026-09-30'), 0) AS delivered,
         ol.qty - COALESCE((SELECT SUM(f.qty) FROM order_fulfilment f
                     JOIN voucher_head vh ON vh.company_id = f.company_id
                                         AND vh.current_version_id = f.voucher_version_id
                     JOIN voucher_version vv ON vv.id = f.voucher_version_id
                    WHERE f.order_line_id = ol.id AND vh.is_deleted = false
                      AND vv.voucher_date <= DATE '2026-09-30'), 0) AS outstanding
    FROM order_line ol
    JOIN order_head oh ON oh.id = ol.order_id
    JOIN ledger l ON l.id = oh.party_ledger_id
    JOIN account_group ag ON ag.id = l.group_id
    JOIN stock_item si ON si.id = ol.stock_item_id
   WHERE oh.company_id = :company AND oh.is_closed = false
   ORDER BY oh.direction, oh.order_no, ol.line_no;
MEASURED TODAY on company 71fe4e15-f420-4a2d-b9c6-88f8ef69d401: SALES | SO-BROWSER-1 | 2026-06-01 | HDFC Bank | **Bank Accounts** | 1 | MS Angle 50x50 | ordered 20.0000 | rate 500.0000 | delivered 8.0000 | outstanding 12.0000. So the Sales Order Book at as_on=2026-09-30 must read Ordered 20.0000 / Delivered 8.0000 / Outstanding 12.0000 / Rate 500.00 / Outstanding Value 6,000.00 / Due "none agreed", total 6,000.00. At as_on=2026-08-05 (before the fulfilling Delivery Note of 2026-08-06) the SAME line must read Delivered 0.0000 / Outstanding 20.0000 / Value 10,000.00 — that date pair is the only thing that proves the as-at bound is applied to the DELIVERY and not only to the order date.

C. BOM explosion arithmetic (report+field oracle, no SQL can do it). Recipe on that company: bom_head "Standard", finished good SS Sheet 2mm, output_qty 3.0000, one component MS Angle 50x50 qty 10.0000. Alt+X with qty 7 must return component qty 23.3333 (10*7/3 rounded once). 23.3330 means the ratio was computed first and truncated — the permanent, always-short error orders/bom.go:334-357 exists to prevent. Also read note[0] verbatim.
```

**Predictions**

- **[high]** The order's PARTY is picked from the WHOLE chart of accounts — nothing anywhere restricts it to Sundry Debtors (sales) / Sundry Creditors (purchase), so a sales order can be, and already has been, committed to a bank ledger. Tally's Party field on an order is restricted to those two groups.
  - *evidence:* frontend/src/screens/OrderRegisterScreen.tsx:253-256 loads `client.listLedgers(companyID)` with no filter and renders every row as an <option> (OrderRegisterScreen.tsx:735-741); backend/internal/orders/order.go:156-165 Validate only refuses uuid.Nil; backend/internal/server/handler_orders.go:124-129 only parses the uuid; the sole DB guard is the FK order_head_party_fk (order.go:571). LIVE PROOF already in the shared DB: SELECT oh.order_no, l.name, ag.name FROM order_head oh JOIN ledger l ON l.id=oh.party_ledger_id JOIN account_group ag ON ag.id=l.group_id  ->  SO-BROWSER-1 | HDFC Bank | Bank Accounts (company 71fe4e15-f420-4a2d-b9c6-88f8ef69d401).
  - *how to tell:* REAL: Ctrl+F8, Alt+C, open the "Customer" <select> — it lists Cash, HDFC Bank, Sales Accounts, Output CGST etc. alongside Acme Traders; choose HDFC Bank, fill a line, Ctrl+A -> 201, and the oracle SQL in B returns party_group='Bank Accounts' for a SALES order. NOT REAL: the select offers only ledgers whose group chain reaches Sundry Debtors, or the POST returns 422 naming the group.
- **[high]** Nothing in the order path writes stock_movement, posting, voucher_head or ledger_balance — the invariant holds and is enforced mechanically, not by convention. I predict this row comes back CLEAN and the audit's effort is better spent on the party and the fulfilment link.
  - *evidence:* backend/internal/archtest/boundaries_test.go:150 pins `"internal/orders": {"internal/platform/db": true}` — an empty-but-for-db import set, so an edge to internal/ledger will not compile; backend/internal/orders/order.go:1-43 and backend/internal/reporting/orderbook.go:9-16 state the same rule; backend/internal/ledger/nonaccounting.go:127-152 refuses the classes SalesOrder/PurchaseOrder outright with ErrOrderVouchersNotModelled, so no voucher route can carry one either.
  - *how to tell:* REAL (clean): counts of stock_movement / posting / voucher_head are byte-identical before and after Ctrl+F8 -> Alt+C -> Ctrl+A, and information_schema shows no order column on stock_movement. NOT CLEAN: stock_movement gains rows, or a voucher_head row appears with a voucher_type named 'Sales Order' — in which case also check the Day Book, because a movement written here would corrupt closing stock and therefore the Balance Sheet.
- **[high]** The fulfilment picker's candidate list claims to put the OLDEST commitment first but sorts by ORDER NUMBER as a raw string, so with more than nine orders (or any two numbering schemes) it offers the wrong order first — and the operator links the shipment to it.
  - *evidence:* frontend/src/screens/vouchers/FulfilmentPicker.tsx:153-157: comment "Oldest commitment first: the order that has been waiting longest is the one an operator shipping today means to discharge" immediately above `list.sort((a, b) => a.orderNo.localeCompare(b.orderNo))`. `order_date` IS available on the OrderView the picker already holds (readOrderHead projects it, backend/internal/orders/order.go:492-498) and is simply not used.
  - *how to tell:* REAL: create two OPEN sales orders for the same stock item — SO-2 dated 2026-04-01 and SO-10 dated 2026-09-01 — then Alt+F8 (Delivery Note), pick that item, click "link (2)": the dropdown lists SO-10 before SO-2, i.e. newest first, contradicting the comment. NOT REAL: SO-2 is listed first.
- **[high]** Alt+X's first explanatory note is a malformed sentence: it prints the recipe's YIELD where the component/finished-good phrase belongs, so it reads "3.0000 of SS Sheet 2mm yields 3.0000". The arithmetic is right; the sentence printed on the artefact is not.
  - *evidence:* backend/internal/orders/bom.go:373-378 — format "...: %s of %s yields %s, so this batch of %s multiplies every component by %s/%s" with args (b.OutputQty, b.StockItem, b.OutputQty, wanted, wanted, b.OutputQty); the second %s is the FINISHED GOOD, so OutputQty is printed on both sides of "yields". Rendered by the screen verbatim (frontend/src/screens/masters/BomsScreen.tsx renders notes unaltered; handler_orders.go:475-480 tells the client to render them).
  - *how to tell:* REAL: Gateway -> Bills of Materials -> highlight "Standard" -> Alt+X -> qty 7 -> Ctrl+A; note 1 reads "Scaled from the recipe as written: 3.0000 of SS Sheet 2mm yields 3.0000, so this batch of 7 multiplies every component by 7/3.0000." NOT REAL: it reads "10.0000 of MS Angle 50x50 yields 3.0000 of SS Sheet 2mm" or similar. Check the component qty in the same response is 23.3333 — that part I expect to be correct.

## ROW 29 — Closing Stock on the Balance Sheet at two dates — it must move the sheet, the sheet must still balance, and the P&L must agree

**Reachable from the keyboard today:** YES, but only half of it. The Balance Sheet is a Gateway entry and F2 sets its as-at date (it is in AS_AT_ONLY_REPORTS, FrameOverlays.tsx:233-238, so the dialog correctly says only "To" applies). The Closing Stock LINE, however, is not reachable in the default view: the BS renders it nested under Current Assets > Stock-in-Hand (reporting.go:868-875, group tree confirmed in the DB: Stock-in-Hand's parent is Current Assets, which is a root ASSET group), so flattenNodes gives it depth 3, and useReportView drops every row with depth > 1 unless `detailed` is set (reportView.ts:158-162). Alt+F5 / Alt+F1 is therefore a REQUIRED keystroke for this row, not an optional one.

**Keystrokes**

```
From the Gateway, arrow down to "Balance Sheet" (GatewayScreen.tsx:8) and press Enter. Press F2 (on a report frame F2 is `period`, tallyprime.ts:266-267), Tab to "Period to", type 2026-06-30, Tab to Apply, Enter. THEN PRESS Alt+F5 (report override, tallyprime.ts:270) or Alt+F1 (global, tallyprime.ts:52) for Detailed — WITHOUT IT THE "Closing Stock" ROW IS NOT ON SCREEN AT ALL (see prediction 1). Read the row under Current Assets > Stock-in-Hand, and read the "Assets" and "Liabilities" section totals (data-testid bs-assets-total / bs-liabilities-total). Repeat with F2 -> "Period to" = 2026-09-30. Cross-check the same figure two other ways: Esc to Gateway, arrow to "Stock Summary", Enter, F2 -> same To (StockSummaryScreen.tsx:48 reads periodTo as as_on); and Gateway -> "Profit & Loss A/c", Enter, F2 with FROM LEFT BLANK and To = the same date (ReportScreens.tsx:470-477 sends both bounds), where "Closing Stock" sits at section level and IS visible without Detailed.
```

**Oracle**

```
THE FIGURE IS NOT IN A COLUMN — closing stock is derived by replaying the movement ledger through a weighted-average valuer (backend/internal/reporting/closing_stock.go:133), so the SQL oracle is the REPLAY INPUT plus a hand fold; the report-level oracle is the identity between three surfaces.

A. The inputs, in the exact order the code replays them (voucher_date, voucher_no AS A STRING, version created_at, line_no — backend/internal/inventory/valuation.go:155-170):
  SELECT vv.voucher_date, vt.name AS class, vv.voucher_no, vv.created_at,
         si.name AS item, sm.direction, sm.qty, sm.rate
    FROM stock_movement sm
    JOIN voucher_version vv ON vv.id = sm.voucher_version_id
    JOIN voucher_head vh ON vh.company_id = vv.company_id
                        AND vh.voucher_id = vv.voucher_id
                        AND vh.current_version_id = vv.id
    JOIN voucher_type vt ON vt.id = vv.voucher_type_id
    JOIN stock_item si ON si.id = sm.stock_item_id
   WHERE vh.is_deleted = false AND vv.company_id = :company
     AND vv.voucher_date <= DATE '2026-09-30'
   ORDER BY vv.voucher_date, vv.voucher_no, vv.created_at, sm.line_no;
(The vh join on current_version_id is load-bearing: without it an ALTERed voucher is counted twice and the sheet STILL BALANCES.)

B. MEASURED TODAY on company 71fe4e15-f420-4a2d-b9c6-88f8ef69d401 the ledger is: MS Angle 50x50 IN 120@25.75 (04-10), IN 100@12.50 (05-15), OUT 30 (06-30), IN 40@88 (07-01), IN 50@400 (RN no.5, 08-06 00:01:25), OUT 8 (DN no.4, 08-06 00:02:16), OUT 35 (09-10); SS Sheet 2mm IN 60@940 (05-20), OUT 25 (08-15). Folding that gives:
  as at 2026-06-30: MS Angle 190 qty / 3,748.1818 value + SS Sheet 60 / 56,400.0000  => CLOSING STOCK 60,148.1818
  as at 2026-09-30: MS Angle 237 qty / 23,539.1323 value + SS Sheet 35 / 32,900.0000 => CLOSING STOCK 56,439.1323
CORRECT LOOKS LIKE: the Balance Sheet's "Closing Stock" node prints exactly those two figures at those two dates (they differ, so the sheet MOVES); Assets.Total - Liabilities.Total = 0 at both; the Stock Summary's TotalValue at the same date EQUALS the same figure to the paisa; and the P&L's "Closing Stock" (income side) with from blank and to = the same date equals it too. ⛔ DO NOT ACCEPT "the sheet balances" AS EVIDENCE: the same S is added to Assets and to net profit, so it cancels and IsBalanced() is true for ANY value of S, including a badly wrong one — reporting/closing_stock.go:26-46 says so in as many words. Assert the NODE against the Stock Summary, and assert the QUANTITY (237 / 35) separately from the VALUE, because the two predictions below move value while leaving quantity identical.
```

**Predictions**

- **[high]** On the Balance Sheet the "Closing Stock" row is INVISIBLE in the default (condensed) view — you see only a "Current Assets" subtotal that silently contains it — while on the Profit & Loss the identically-named row is always visible. An operator comparing the two statements, or exporting the BS with Alt+E, gets no Closing Stock line from the sheet at all.
  - *evidence:* backend/internal/reporting/reporting.go:868-875 injects the node into the Stock-in-Hand group (depth 3 after frontend/src/screens/reports/ReportScreens.tsx:398-408 calls flattenNodes(nodes, 1)); frontend/src/screens/reports/reportView.ts:158-162 `if (!cfg.detailed) out = out.filter((r) => (r.depth ?? 0) <= 1)`. The P&L's terms are SECTION-level appends instead (reporting.go:676-690), so they land at depth 1 and always render — the asymmetry is even documented at reporting.go:643-647 without noticing what it costs the reader. Alt+E exports only `visible` rows, so the exported BS omits it too.
  - *how to tell:* REAL: open Balance Sheet, F2 -> To = 2026-09-30; no row anywhere says "Closing Stock" and no row says "Stock-in-Hand"; press Alt+F5 and both appear under Current Assets. Alt+E before pressing Alt+F5 writes a CSV with no Closing Stock line. NOT REAL: the row is on screen straight away.
- **[medium]** Closing stock VALUE is replayed in the wrong order for vouchers that share a date: the sort key compares voucher_no AS A STRING, across INDEPENDENT numbering series. On 2026-08-06 this company has Receipt Note no.5 (IN 50 @ 400, entered 00:01:25) and Delivery Note no.4 (OUT 8, entered 00:02:16); "4" < "5", so the ISSUE is replayed BEFORE the RECEIPT — the exact inverse of the order they were entered. The 8 units are costed at the pre-receipt average 31.6008 instead of 97.3864, and the Balance Sheet's Closing Stock is 458.56 too HIGH, with quantity identical either way.
  - *evidence:* backend/internal/inventory/valuation.go:155-170 `func less(a, b Movement) bool { ... if a.VoucherNo != b.VoucherNo { return a.VoucherNo < b.VoucherNo } ... }` — a raw string compare, ranked ABOVE VersionCreatedAt, and voucher_no is per-SERIES (voucher_number_counter), so a Delivery Note's 4 and a Receipt Note's 5 are unrelated numbers being ordered against each other. Same defect within one series once numbers reach two digits: "10" < "9". Live data confirming the collision: SELECT vt.name, vv.voucher_no, vv.created_at, sm.direction, sm.qty, sm.rate ... WHERE vv.voucher_date='2026-08-06' AND vv.company_id='71fe4e15-...' -> Receipt Note | 5 | 00:01:25 | IN | 50 | 400 ; Delivery Note | 4 | 00:02:16 | OUT | 8 | 500.
  - *how to tell:* REAL: Balance Sheet at as_on=2026-09-30 (Alt+F5 to reveal it) shows Closing Stock 56,439.1323 and Stock Summary at the same date shows MS Angle 50x50 closing qty 237.0000 / value 23,539.1323. NOT REAL (i.e. replayed in entry order) : 55,980.5682 and 23,080.5682. Either way the QUANTITY is 237.0000 — a quantity-only check cannot tell the two apart, which is the whole point. Sharper repro if you can post: key two Receipt Notes for one item on ONE date at very different rates so their series numbers straddle a lexical boundary (no.9 at 10 and no.10 at 1000), then an issue on the next day; the issue's cost, and therefore Closing Stock, changes depending only on how the numbers sort as text.
- **[medium]** The Balance Sheet's double-booking refusal is DATE-DEPENDENT and kills the WHOLE statement with a 500, not just the stock line: the same books render at one as-at date and fail outright at the next. Any operator who creates an ordinary ledger under Stock-in-Hand (Alt+C) and gives it a balance loses the Balance Sheet entirely from the first day a stock movement exists.
  - *evidence:* backend/internal/reporting/reporting.go:786-826 — the refusal fires only inside `if !closing.IsZero()`, so it is gated on the AS-AT DATE; backend/internal/server/server.go:2612-2615 maps that error to http.StatusInternalServerError, so the client sees a 500 rather than a statement with a stated problem. The reachable state already exists in the shared DB: company 229060e3-60e6-4ee9-a113-2c0a13b30c77 ("Lens2 DoubleBook Co") has a posted 60,000.0000 Dr on ledger "Opening Stock" under group Stock-in-Hand AND one live inward movement of 10 @ 100 dated 2026-05-01.
  - *how to tell:* REAL: for that company GET /v1/companies/229060e3-.../balance-sheet?as_on=2026-04-30 returns 200 with assets including the full 60,000 manual stock (closing = 0, refusal not reached), while ?as_on=2026-05-01 returns 500 carrying "company books stock twice — ledgers under \"Stock-in-Hand\" carry 60000.0000 while the inventory ledger derives a closing stock of 1000.0000". In the browser the same flip appears as the Balance Sheet screen going from a rendered sheet to an error panel purely on an F2 date change. NOT REAL: both dates return 200, or the failing one returns a 422/409 naming the problem rather than a 500.
- **[medium]** The Balance Sheet and the P&L agree on the Closing Stock FIGURE at every date (both call the same closingStockValue with the same date), but their NET PROFIT lines diverge the moment F2's "From" is filled in — the BS ignores From by design while the P&L honours it — and nothing on either screen says the two net-profit numbers are answering different questions.
  - *evidence:* backend/internal/reporting/reporting.go:667 (P&L: closingStockValue(ctx, tx, to)) and reporting.go:781 (BS: closingStockValue(ctx, tx, asOn)) are the same fold; but reporting.go:975-977 computes bs.NetProfit with openingBS hard-coded to decimal.Zero while ProfitAndLoss subtracts openingStockValue(from) (reporting.go:663-665), and the BS's income/expense come from loadClosingBalances with no lower bound. reporting.go:966-970 admits the cross-check oracle only compares the two UNBOUNDED.
  - *how to tell:* REAL: set F2 From = 2026-07-01, To = 2026-09-30. The Balance Sheet's "Profit & Loss A/c (Current Period)" line and the P&L's "Net Profit" differ (by opening stock plus every pre-July posting), while the two "Closing Stock" figures are identical to the paisa. Leave From blank and the two net profits converge. NOT REAL: the two net-profit figures match with From set, which would mean one of the statements is ignoring a bound it claims to honour.

## ROW 30 — Bill Inbox — OCR draft accepted into a real voucher, PII still redacted

**Reachable from the keyboard today:** yes — `Bill Inbox` is GATEWAY_MENU[1] (frontend/src/screens/GatewayScreen.tsx:6) and navMachine.ts:655-658 drills to screen `bill-inbox` before the report fallback. Two caveats: with zero drafts the SelectionList is not rendered at all (BillInboxScreen.tsx:275-286) so nothing is autofocused and the fixture textarea/file input need Tab; and the `/companies` path is proxied to insightd :8088 by vite.config.ts:22 with NO auth on insightd, so the screen works only while insightd is up.

**Keystrokes**

```
Gateway of Tally (combobox autofocused) → type `Bill Inbox` → Enter → Enter (drill). On the Bill Inbox: Tab (file input) → Tab (Fixture JSON textarea) → paste the contents of /Users/yashbhalgat/code/tally-updated/insightd/insightd/benchmark/corpus/intra-state-18-1line-clean.json → click/Space `Create draft` → the row appears in `Pending bills` → Enter on it (SelectionList) → F9 Purchase opens in draft mode with the draft panel → Ctrl+A to accept. Screen source: frontend/src/screens/BillInboxScreen.tsx:280-307, drill in frontend/src/keystroke/navMachine.ts:650-659.
```

**Oracle**

```
(1) Read the DRAFT itself, not the screen: `curl -s localhost:8088/companies/<COMPANY_UUID>/drafts/<DRAFT_ID> | jq '{postings:.body.postings, gst:.derived_gst, party:.body.party, voucher_no:.body.voucher_no, red:.redaction}'`. CORRECT looks like: four postings — Dr <purchase ledger> 1000.0000, Dr `Input CGST` 90.0000, Dr `Input SGST` 90.0000, Cr <supplier> 1180.0000 — and derived_gst {cgst:90.0000, sgst:90.0000, igst:0.0000} for this MH→MH fixture. (2) After Ctrl+A, psql: `SELECT l.name, p.dr_cr, p.amount FROM posting p JOIN ledger l ON l.id=p.ledger_id JOIN voucher_version vv ON vv.id=p.voucher_version_id JOIN voucher_head vh ON vh.current_version_id=vv.id WHERE vv.company_id='<CO>' AND vh.is_deleted=false ORDER BY vv.created_at DESC LIMIT 6;` — CORRECT is the same four rows, sides and NAMES as above, `Input CGST`/`Input SGST` on the DEBIT side (never `Output …`), supplier on CREDIT. (3) The junk-master oracle, run whether or not the accept succeeded: `SELECT l.name, g.name AS grp, l.created_at FROM ledger l JOIN account_group g ON g.id=l.group_id WHERE l.company_id='<CO>' ORDER BY l.created_at DESC LIMIT 5;` — CORRECT is that a FAILED accept leaves NO new ledger row.
```

**Predictions**

- **[high]** Every draft created through the Bill Inbox screen is missing its INPUT-TAX POSTING LEGS and is therefore unbalanced by exactly the tax, so Ctrl+A can never post a taxed bill. The screen sends `tenant: { company_id: companyID }` and nothing else; routes_drafts.py uses `ctx = body.tenant` verbatim (the fixture's own `input.tenant` block is ignored), so ctx.cgst/sgst/igst_ledger_id are all None and ctx.ledgers is []. The mapper appends each tax debit only `if derived.X > 0 and ctx.X_ledger_id`, but appends the party CREDIT unconditionally at `derived.invoice_total`. Result: debits 1000.0000, credit 1180.0000.
  - *evidence:* frontend/src/screens/BillInboxScreen.tsx:150-154 and :190-196 (`tenant: { company_id: companyID }`); insightd/insightd/api/routes_drafts.py:109 (`ctx = body.tenant`); insightd/insightd/adapters/mapper.py:287-313 (tax legs gated on `ctx.*_ledger_id`) vs :315-322 (party credit = `derived.invoice_total` ungated); backend/internal/ledger/ledger.go:2359-2385 validatePostings rejects debit != credit
  - *how to tell:* REAL: the draft JSON's `body.postings` has exactly 2 entries and no ledger_name containing `Input`, and Ctrl+A leaves `Error: …double-entry violation: debit 1000.0000 != credit 1180.0000` (or, on the fixture path, an earlier `invalid ledger id` from the non-UUID `L-PUR-RM`) with NO new row in `voucher_version`. NOT REAL: four postings including the two Input tax debits, and psql shows the four-row voucher.
- **[high]** An INTRA-state bill is derived as IGST. Because ctx.ledgers is empty the supplier can never match a ledger, so `supplier_state` stays None; tax.py only concludes intra when BOTH state codes are non-None and equal, so the else-branch books the whole 180.0000 as IGST. The draft panel will show `IGST 180.0000 / CGST 0.0000 / SGST 0.0000` for a fixture whose own title and OCR say Place of Supply 27, supplier state 27.
  - *evidence:* insightd/insightd/adapters/mapper.py:194-199 (supplier_state resolved only from `ctx.ledgers`) and :229-237; insightd/insightd/adapters/tax.py:56-60 (`intra = supplier is not None and pos is not None and equal`); rendered at frontend/src/screens/VoucherScreen.tsx:2165-2179
  - *how to tell:* REAL: `jq .derived_gst` on the draft returns igst "180.0000", cgst "0.0000", and `data-testid="draft-derived-gst"` shows IGST 180.0000; `unresolved` contains `supplier_state`. NOT REAL: cgst 90.0000 / sgst 90.0000 / igst 0.0000.
- **[medium]** A FAILED Ctrl+A still writes to the chart of accounts. `acceptPurchaseDraft` calls `resolveDraftLedgers` — which calls `ensureLedger` (a real POST /ledgers) for the null party and for every null posting ledger — BEFORE the voucher POST is attempted. So an accept that the server then rejects leaves orphan masters behind (here a `Sundry Creditor` under `Sundry Creditors`, because `body.party` is None once the supplier could not be matched).
  - *evidence:* frontend/src/screens/vouchers/draftAccept.ts:274-320 (`resolveDraftLedgers` ensures the party first, then each posting) then :332-336 (POST after); frontend/src/screens/vouchers/gstVoucher.ts:294-309 (`ensureLedger` issues `client.createLedger`); mapper.py:315-322 sets `ledger_name` to "Sundry Creditor" when `party_name` is None
  - *how to tell:* REAL: after a failed Ctrl+A, `SELECT l.name, g.name FROM ledger l JOIN account_group g ON g.id=l.group_id WHERE l.company_id='<CO>' ORDER BY l.created_at DESC LIMIT 3;` shows a freshly created `Sundry Creditor` (group `Sundry Creditors`) while `voucher_version` gained no row. NOT REAL: no new ledger row, or ledger creation happens only after a 201 voucher.
- **[medium]** The redaction does NOT hold on the values that become the voucher, and the panel's "PII redacted: N" badge is not a statement about them. The boundary redacts OCR TEXT and `assert_clean` is only ever applied to OUTBOUND LLM payloads; nothing re-scans the returned draft. On the default (all-fakes) path the inferencer reads the RAW fixture out of `_FIXTURE_STASH`, which the pipeline populates from `doc.source_meta` — the pre-redaction object — so `invoice_no`, line `description` and hence `PostingDraft.ledger_name` bypass the boundary entirely and land in `voucher_version.voucher_no` and in a permanent ledger master name.
  - *evidence:* insightd/insightd/pipeline.py:69-71 (`fixture = _extract_fixture(doc)` — the RAW doc — then `stash_fixture(redacted, fixture)`); insightd/insightd/adapters/fakes.py:256-282 (FakeInferencer reads `_FIXTURE_STASH`, `li.get("description")` → `LedgerGuess.name`); insightd/insightd/redaction/boundary.py:119-127 (assert_clean is outbound-only); insightd/insightd/adapters/mapper.py:256-264 (`ledger_name = guess.name or li.description`) and :342 (`voucher_no=inferred.invoice_no`); no test in insightd/tests/test_pii_boundary.py:81-124 asserts the draft body is clean
  - *how to tell:* REAL: edit a copy of the fixture so `input.inferred.invoice_no` is `ABCDE1234F` (a PAN) and leave the OCR lines untouched; the draft returns `redaction.counts_by_type.PAN` counting only the OCR occurrences while `body.voucher_no == "ABCDE1234F"` verbatim, and the same raw string reaches `SELECT voucher_no FROM voucher_version` on any voucher posted from it. NOT REAL: `body.voucher_no` comes back as `<<PAN:xxxx>>` or the draft is refused.
- **[medium]** insightd has no authentication and scopes drafts by the caller-supplied company_id alone, and the vite dev proxy exposes it same-origin at `/companies/*`. Any company_id can list and hydrate another tenant's OCR drafts — supplier names, invoice numbers and amounts.
  - *evidence:* insightd/insightd/api/routes_drafts.py:192-219 (list/get keyed only on the path `company_id`; no Depends on any auth); frontend/src/api/insightd.ts:1-11 ("insightd routes are NOT under /v1", no bearer); frontend/vite.config.ts:22 (`"/companies": { target: INSIGHTD }`)
  - *how to tell:* REAL: `curl -s localhost:8088/companies/<OTHER_COMPANY_UUID>/drafts` (no token) returns that tenant's DraftSummary rows with real party names and grand_total. NOT REAL: 401/403, or an empty list for a company whose drafts exist.

## ROW 31 — Ask your books (NL query) — answer must match the report; cross-tenant probe must be refused

**Reachable from the keyboard today:** yes — `Query` is GATEWAY_MENU[2] (frontend/src/screens/GatewayScreen.tsx:7), drilled at frontend/src/keystroke/navMachine.ts:664-667. Friction worth noting: QueryScreen.tsx:120-127 gives the input no autoFocus (the Gateway combobox has just unmounted), so a keyboard-only operator lands with focus on <body> and must Tab; and the `Show how this was answered` panel that carries the DSL and date window is COLLAPSED by default (QueryScreen.tsx:53, :232), so the wrong period in the next prediction is hidden unless it is expanded.

**Keystrokes**

```
Gateway of Tally → type `Query` → Enter → Enter (drill; navMachine.ts:664-667). On the Query screen the input is NOT autofocused, so: Tab to `Ask your books` → type `sales this month` → Enter (submits the form). Then click `Show how this was answered` (data-testid `query-how-toggle`) to reveal `query-sql-preview` and `query-dsl`. Repeat with `sales last month`, `total purchases`, and the cross-tenant probe `what were Beta Ltd's sales this month`.
```

**Oracle**

```
Same figure, two ways. (a) The NL answer's `total_sales` cell. (b) psql, which is the trustworthy one: `SELECT vv.voucher_no, vt.name AS vtype, vv.taxable_value, vv.cgst, vv.sgst, vv.igst FROM voucher_version vv JOIN voucher_head vh ON vh.company_id=vv.company_id AND vh.current_version_id=vv.id AND vh.is_deleted=false LEFT JOIN voucher_type vt ON vt.id=vv.voucher_type_id WHERE vv.company_id='<CO>' AND vt.name='Sales' AND vv.voucher_date BETWEEN '<from>' AND '<to>';` — plus the SIDE-and-NAME oracle the DSL never touches: `SELECT l.name, p.dr_cr, sum(p.amount) FROM posting p JOIN ledger l ON l.id=p.ledger_id JOIN voucher_version vv ON vv.id=p.voucher_version_id JOIN voucher_head vh ON vh.current_version_id=vv.id WHERE vv.company_id='<CO>' AND vh.is_deleted=false AND l.name='Sales' GROUP BY 1,2;` CORRECT is that the NL total equals the Sales-ledger CREDIT total for the period. For the tenant question: `SELECT relname, relrowsecurity, relforcerowsecurity FROM pg_class WHERE relname IN ('company','account_group','ledger','voucher_type','voucher_version','voucher_head','posting','ledger_balance','bill_allocation');` — CORRECT is t/t on all nine.
```

**Predictions**

- **[high]** "sales last month" (and "last quarter", "last year") silently answers for the CURRENT period. `relativeRange` has no `last` branch at all; the first case is `contains(norm,"this month") || contains(norm,"month")`, so the substring `month` in "last month" matches and the window becomes the 1st..last of TODAY's month. The operator gets a confident table for a period they did not ask about.
  - *evidence:* backend/internal/query/translate.go:196-210 (`relativeRange`: only `this month|month`, `this quarter|quarter`, `this year|year`; no `last`/`previous`), reached from :148-159 (the `sales` case)
  - *how to tell:* REAL: ask `sales last month`, expand `Show how this was answered` — `query-dsl` shows `date_range.from` = the 1st of the CURRENT month and `to` = its last day, and the `month` dimension column renders the current YYYY-MM. NOT REAL: the DSL window is the previous month, or the question 422s as untranslatable.
- **[high]** "sales this month" / "total purchases" do not answer what a Sales or Purchase register answers: they sum `vv.taxable_value`, which is stamped ONLY when a GST block was supplied (`if gstActive { taxable = subTaxable(...) }`) and is SQL NULL otherwise. A sale keyed as a plain accounting/single-entry voucher with no GST is silently EXCLUDED from the total, and the tax component is excluded from every included one — so the figure is neither the invoice total nor the full sales figure, with no disclosure of either.
  - *evidence:* backend/internal/query/translate.go:148-159 (metric `sum(taxable_value)`); backend/internal/query/whitelist.go:243-246 ("these four columns are NULLable and are only stamped when GST derivation ran"); backend/internal/ledger/ledger.go:2003-2010 (`if gstActive { taxable = subTaxable(tax, in) }` — otherwise the column is left NULL)
  - *how to tell:* REAL: post two sales in the current month — one GST invoice of 10,000 + 1,800 tax, one non-GST sale of 5,000 — then ask `sales this month`. The answer reads 10000.0000 while `SELECT l.name,p.dr_cr,sum(p.amount) … WHERE l.name='Sales'` shows Sales CREDIT 15,000.0000, and `SELECT voucher_no, taxable_value FROM voucher_version …` shows the non-GST voucher's taxable_value as NULL. NOT REAL: the answer is 15000.0000, or the response names the basis ("GST taxable value only").
- **[medium]** A cross-tenant probe is not REFUSED — it is silently re-answered against the caller's own company. The translator is keyword-only and has no notion of a named counterparty, so `what were Beta Ltd's sales this month` falls through `contains(norm,"sales")` and returns company A's sales table with nothing in the response saying the named tenant was ignored. (The DATA is genuinely safe — the compiled SQL carries no company predicate at all and scoping rests entirely on FORCE RLS plus the `app.current_company_id` GUC set by RunInReadOnlyTx, which is real enforcement, not a prompt instruction. The defect is the missing refusal, not a leak.)
  - *evidence:* backend/internal/query/translate.go:113-159 (keyword switch; no entity/party recognition, no refusal branch); backend/internal/query/whitelist.go:179, :217, :266, :313 (skeletons contain no `company_id` predicate); backend/internal/platform/db/*.go:184-199 (`RunInReadOnlyTx` sets the GUC then relies on RLS); backend/migrations/0002_core_schema.sql:213-228 (ENABLE + FORCE + tenant_isolation on company/account_group/ledger/voucher_type/voucher_version/posting/ledger_balance)
  - *how to tell:* REAL: the probe returns 200 with company A's own sales rows and no warning; `query-sql-preview` shows a FROM/WHERE with no company_id term. Then the enforcement half: the pg_class query above returns t/t for all nine tables (so no rows crossed), and repeating the same POST against `/v1/companies/<B>/query` with A's session returns 403/400 `company id mismatch` (backend/internal/server/server.go:3067-3086). NOT REAL: the probe 422s with "could not understand the question", or any pg_class row shows relforcerowsecurity=f — the latter would upgrade this to a genuine leak.
- **[medium]** There is no SQL-injection surface to find here, and that is worth recording as a checked negative rather than left implied: every user value is bound as `$N` and every identifier is a constant drawn from the whitelist; `like`, `in` and the date bounds are all parameterised, `limit` is emitted as `%d` from a clamped int, and order-by is restricted to selected dimensions/metric aliases. Execution is additionally inside `BEGIN … READ ONLY`.
  - *evidence:* backend/internal/query/compile.go:159 (`nextArg` → `$N`), :183-185 (IN / binary ops use placeholders only), :200-206 (date bounds `$N::date`), :242 (`LIMIT %d` from an int); backend/internal/query/whitelist.go:105-131 (all exprs are constants); backend/internal/platform/db/*.go:190 (`pgx.TxOptions{AccessMode: pgx.ReadOnly}`)
  - *how to tell:* REAL (i.e. safe): submit a filter value of `' OR 1=1 --` via a typed `dsl` POST — `sql_preview` still shows `l.name = $1` and the row count does not widen. NOT SAFE: `sql_preview` contains the literal text of the value, or the result set widens beyond the filter.

## ROW 32 — Cash-flow projection / anomalies — must DISCLOSE it is a projection and STATE its basis

**Reachable from the keyboard today:** no. The routes exist and are wired (backend/internal/server/server.go:902-903, auth_middleware.go:1354-1357 — the "deliberately NOT registered" comment at handler_forecast.go:5-20 is stale). But the FRONTEND has nothing: `grep -rn -i 'cash.flow|cashflow|anomal' frontend/src` returns only screens/FeaturesScreen.tsx:351 (the capability claim) and screens/printTarget.test.ts:250 (a test string). No GATEWAY_MENU entry (GatewayScreen.tsx:4-60), no report-name mapping, and no client method in frontend/src/api (`grep -rn 'cashFlow|anomalies' frontend/src/api` → nothing). So the disclosure cannot be "rendered, not just present in the API" — there is no renderer at all.

**Keystrokes**

```
NONE EXIST. There is no Gateway entry, no report name, and no keymap binding. The nearest reachable evidence is Gateway → type `Features` (F11) → the row `Cash-flow projection and anomaly detection` marked `state: "on"`. To exercise the feature at all the orchestrator must leave the keyboard: `curl -s 'localhost:8087/v1/companies/<CO>/cash-flow?as_on=2026-08-07&horizon_days=90&bucket=week'` and `curl -s 'localhost:8087/v1/companies/<CO>/anomalies'`.
```

**Oracle**

```
Two parts. (1) DISCLOSURE — read the JSON body: `curl -s '…/cash-flow?…' | jq 'keys'`. CORRECT is that a field states this is a PROJECTION and states its BASIS (open bill due dates; `voucher_date + credit_days`, defaulting to 30 days when neither is present; opening cash from the cash/bank ledgers as at as_on) and how many bills were dated by that default. (2) CONSERVATION against a report: `SELECT l.name, ba.ref_name, ba.due_date, sum(ba.amount) FROM bill_allocation ba JOIN voucher_head vh ON vh.company_id=ba.company_id AND vh.current_version_id=ba.voucher_version_id AND vh.is_deleted=false JOIN ledger l ON l.id=ba.ledger_id JOIN account_group g ON g.id=l.group_id WHERE ba.company_id='<CO>' GROUP BY 1,2,3;` — CORRECT is that sum(bucket inflows)+beyond_horizon.inflow equals the ASSET-nature pending total and sum(outflows)+beyond.outflow the LIABILITY one, with the bills that have NULL due_date landing 30 days after their voucher_date.
```

**Predictions**

- **[high]** The cash-flow response carries NO disclosure that it is a projection and NO statement of its basis. The only self-describing field on the wire is `overdue_folded_into_first_bucket`; there is no `estimate`, no `basis`, no `method`, no default-credit-days note — even though the engine silently invents a settlement date for every bill that has neither a due_date nor credit_days (`voucher_date + 30`). A reader cannot tell a due-dated bill from an assumed one.
  - *evidence:* backend/internal/server/handler_forecast.go:105-118 (`cashFlowJSON` — the complete key set: as_on, horizon_days, bucket_days, opening_cash, buckets, beyond_horizon, overdue_folded_into_first_bucket); backend/internal/forecast/forecast.go:60-71 (`expectedDate()`: due_date, else voucher_date+credit_days, else `defaultCreditDays` = 30) and :93-103 (the `Projection` struct has no basis/estimate field)
  - *how to tell:* REAL: `curl …/cash-flow | jq 'keys'` returns exactly those seven keys and none of them contains the words projection/estimate/basis/assumed; post one bill with NULL due_date and confirm it lands 30 days out with nothing in the response saying so. NOT REAL: the body carries an `estimate: true` / `basis` / `assumptions` block naming the 30-day default and counting the bills it was applied to.
- **[high]** FeaturesScreen advertises "Cash-flow projection and anomaly detection" as `state: "on"` with a note that names no door, while every other AI-native row in the same group names one ("Gateway > Bill Inbox", "Gateway > Query"). The capability list overstates what an operator can reach.
  - *evidence:* frontend/src/screens/FeaturesScreen.tsx:340-354 (Bill Inbox note "Gateway > Bill Inbox", Query note "Gateway > Query", cash-flow note "Deterministic projection + rule-based anomalies, with an optional statistical forecast." — no path); frontend/src/screens/GatewayScreen.tsx:4-60 (no such entry)
  - *how to tell:* REAL: on the Features screen the row reads `on`, and no keystroke sequence from the Gateway reaches any screen showing bucket/inflow/outflow figures. NOT REAL: some Gateway or Display-More-Reports entry opens a cash-flow screen.
- **[medium]** The `optional statistical forecast` half (insightd) is unreachable too, and it carries money as JS/Python FLOATS rather than the fixed-4 strings the rest of the system pins — so wiring it as-is would reintroduce the paisa loss the Alt+E export defect already cost this repo. It does at least set `estimate=true`, which cored's own projection does not.
  - *evidence:* insightd/insightd/api/routes_forecast.py:29-36 (`weekly_net_cash: list[float]`, `daybook: list[tuple[str, float]]`), :13-16 ("the response carries estimate=true"); no caller — `grep -rn 'forecast' frontend/src/api` returns nothing
  - *how to tell:* REAL: `curl -s -XPOST localhost:8088/companies/<CO>/forecast/cashflow -d '{"weekly_net_cash":[1000.10,1000.20,1000.30],"weeks":2}' -H 'content-type: application/json' | jq` returns bare JSON numbers (not "1000.1000"-style strings) alongside `estimate: true`, and no frontend file references the route. NOT REAL: the route returns fixed-4 strings, or a screen consumes it.

## ROW 33 — Multiple users + security levels (invite; membership appears; invitee PENDING until claimed)

**Reachable from the keyboard today:** yes, partially — the SCREEN is reachable from the keyboard (Gateway arrow + Enter, navMachine.ts:668-674, label at navMachine.ts:143); every ACTION on it (invite, apply level, revoke, level editor) is a <button>/<input> with no keystroke binding, so the invite itself cannot be keyed. The second half of the row — 'the invitee is PENDING until claimed' — is NOT reachable at all: nothing in the browser can claim it (see prediction 2).

**Keystrokes**

```
Sign in on the LoginScreen (first screen in live mode, App.tsx:141): type `browser-dev-owner`, Tab, type `tally-demo-2026`, Enter. Then at the Gateway press Down until the highlighted row is `Users & Roles` (11th entry, GatewayScreen.tsx:58) and press Enter. On the screen the invite form is MOUSE-ONLY: click [data-testid=invite-subject], type a fresh subject e.g. `recon-invitee-01`, pick a level in [data-testid=invite-level], click [data-testid=invite-submit]. (There is no keymap entry that opens this screen and no key that submits the form — everything below the Gateway drill is pointer-driven.)
```

**Oracle**

```
psql -h localhost -p 5433 -U postgres -d tally.  (a) MEMBERSHIP DID NOT APPEAR: `select subject,status from company_invite where company_id='<C>' and subject='recon-invitee-01';` -> one row, status 'pending'; and `select count(*) from company_membership m join app_user u on u.id=m.user_id where m.company_id='<C>' and u.subject='recon-invitee-01';` -> 0. Correct = invite pending, NO membership.  (b) THE DELIVERY ORACLE, which is the one that matters: `select recipient, status, sent_at, last_error from notification_outbox where kind='company_invite' order by created_at desc limit 1;` — correct would be a row whose recipient contains '@' OR whose status is NOT 'sent'. Today it is expected to read recipient='recon-invitee-01' (no '@'), status='sent'. Cross-check the running cored log for the line `notify: [company_invite] NO EMAIL ON FILE for subject "recon-invitee-01"`.
```

**Predictions**

- **[high]** Every invite created from Users & Roles is delivered to NOBODY and the screen nevertheless reports 'invitation sent'. The invite form sends no email address; notify falls back to the subject as the recipient; LogSender sees no '@', logs 'NO EMAIL ON FILE', and returns nil; settle() therefore writes status='sent'; deliveryText() renders that as 'invitation sent'. This is exactly the failure the screen's own header says it exists to prevent ('a company that believed it had onboarded somebody who never heard about it').
  - *evidence:* frontend/src/screens/MembersScreen.tsx:264-267 posts only `{subject, security_level_name}` — the InviteMemberRequest `email` field (api/admin.ts:88-95) is never populated and the form has no email input (MembersScreen.tsx:520-551). backend/internal/notify/notify.go:136-141 `recipient := email; if recipient == "" { recipient = subject }`; notify.go:69-78 HasAddress() is false without an '@'; notify.go:94-104 LogSender.Send logs and `return nil`; notify.go:330-340 settle() writes `status='sent', sent_at=now()` whenever sendErr==nil; MembersScreen.tsx:90-106 deliveryText maps 'sent' -> 'invitation sent'. Already true of live data: `select count(*) filter (where status='sent' and recipient not like '%@%') from notification_outbox` = 37.
  - *how to tell:* If real: the new notification_outbox row has recipient with no '@' and status='sent', sent_at non-null, last_error='', while the cored log line for it says NO EMAIL ON FILE / nothing was sent — and the invites list renders 'invitation sent'. If not real: either the row's status is 'failed'/'pending', or the UI renders 'no invitation sent — tell them yourself'.
- **[high]** A PENDING invite can never be claimed from this product. There is no set-a-first-password screen anywhere in the frontend, and POST /auth/password requires a bearer that only the dev-gated POST /auth/token can mint, so an invited subject can never reach POST /auth/login and therefore never reaches GET /v1/me, which is the only thing that converts an invite into a membership.
  - *evidence:* frontend: `setPassword` is exported (src/api/auth.ts:155, src/auth/index.ts:23) and imported by NO screen — only auth/auth.test.tsx. LoginScreen.tsx has sign-in fields only (lines 98-124). backend/internal/server/auth_login.go:76-97 states the limit verbatim ('THERE IS NO PRODUCTION PATH TO SET A **FIRST** PASSWORD'). The claim seam is handleMe (handler_admin.go:660-690), reachable only with a valid bearer.
  - *how to tell:* If real: after the invite, no browser action can make the row move from company_invite to company_membership — `select status from company_invite where subject='recon-invitee-01'` stays 'pending' forever, and the Pending-invites panel never empties. If not real, some screen would offer a password-set flow and the invite would clear on that user's first sign-in.

## ROW 34 — Custom levels (Disallow/Allow) — SIGN IN AS THAT LEVEL AND BE REFUSED

**Reachable from the keyboard today:** yes — this is the one row of the six whose 'sign in as that level' half really is drivable today, because seed:tenancy already provisioned `demo-data-entry` with a password (app_user + user_credential rows verified in psql). The LEVEL EDITOR that authors a custom level (MembersScreen level-new / level-edit) is Owner-only and pointer-only, and note that saving it is a write on the Owner level, so it needs Row 36's step-up to succeed first.

**Keystrokes**

```
Sign out (Chrome header button [data-testid=signed-in-as] > 'Sign out' — pointer only, no key). On the LoginScreen type `demo-data-entry`, Tab, `tally-demo-2026`, Enter. This identity already exists with a credential and holds the seeded 'Data Entry' level (92 acl_rule rows, default_access 'none') in five companies named 'Demo Traders' — psql-verified, so the row can be driven TODAY without provisioning anything. Press F3, arrow to a `Demo Traders · Data Entry` row, Enter. Then: Gateway > `Day Book` > Enter (should render), then Enter on any Day Book row to drill into the voucher (this is the refusal). Control: Gateway > `Stock Summary` (or Alt+G > Stock Summary) — `disallow display report:stock-summary` is seeded, so it must 403.
```

**Oracle**

```
Two oracles, in order of trust.  (1) SERVER: tail cored's request log / DevTools Network for `GET /v1/companies/<C>/vouchers/<id>` and `GET /v1/companies/<C>/day-book`. Correct for a level holding `allow display sales` + `allow alter sales` is 200 on the voucher GET. Expected today: 403 `{"error":"default access: none"}`.  (2) psql, to prove the rule set that produced it: `select effect,action,resource from acl_rule where security_level_id=(select security_level_id from company_membership where company_id='<C>' and user_id=(select id from app_user where subject='demo-data-entry')) and resource in ('voucher','sales') order by 1,2;` — correct would show a row granting the token the route actually asks for. Today it shows `allow alter sales`, `allow create sales`, `allow display sales` and NOTHING naming `voucher`.  Never assert on the screen re-rendering: a 403 here paints an empty alter form that looks like a slow load.
```

**Predictions**

- **[high]** Data Entry can CREATE a sales invoice but can never READ ONE BACK, and `allow alter sales` is dead in practice. routeAction maps GET /vouchers/{id} and /vouchers/{id}/history to the class-agnostic resource token `voucher`, which no seeded Data Entry rule names, so default_access 'none' denies it — and the browser's alter flow GETs the voucher before it can hydrate the form. As a corollary all sixteen `allow display <class>` rules on that level match no route at all: they are exactly the 'rule an administrator can write that can never match' the middleware's own comments warn about.
  - *evidence:* backend/internal/server/auth_middleware.go:463-471 (GET /vouchers/{id} -> `return authz.Display, "voucher", "", true`; history the same) vs auth_middleware.go:435-445 (POST /vouchers/{class} -> `authz.Resource(class)`). backend/internal/authz/vocabulary.go:97 `"voucher", // an individual voucher (get/delete are class-agnostic)`. backend/internal/authz/authz.go:186-195 no-match falls to default_access 'none' -> deny("default access: none"). frontend/src/screens/VoucherScreen.tsx:918-937 the alter frame calls `client.getVoucher(companyID, loadedVoucherID)` before it seeds the form. psql: the 92 Data Entry rules contain no row with resource='voucher'.
  - *how to tell:* If real: Enter on a Day Book row produces `GET .../vouchers/<id>` -> 403 with body `default access: none`, the alter screen stays blank, and Alt+L (edit log) 403s identically — while `POST .../vouchers/sales` from the same session returns 201. If not real, the GET returns 200 and the alter form fills with the drilled voucher's party and amount.
- **[medium]** Disallow does NOT beat Allow in general — only at EQUAL specificity, and specificity is a SUM of two independent axes (action-exact 0/1 plus resource 0/1/2), so a narrower allow silently overrides a broader disallow. `disallow display report` (spec 2) is defeated by `allow display report:day-book` (spec 3). An administrator who writes the coarse disallow to close a whole class of reports will have it punctured by any pre-existing specific allow, with no warning anywhere.
  - *evidence:* backend/internal/authz/authz.go:205-220 (`spec := 0; if actExact { spec++ }; spec += resSpec` where resSpec is 0/1/2 from matchResource, authz.go:234-252) and authz.go:153-183 (highest spec wins outright; the Disallow tie-break at :165-168 only fires on `spec == bestSpec`). The header comment at authz.go:15-18 states the intended rule as 'most-specific rule match wins; DISALLOW beats ALLOW at equal specificity', so the behaviour is by design — the defect is that the browser rule editor (MembersScreen.tsx:722-725) advertises it to the administrator as 'disallow beats allow' with the qualifier in small print.
  - *how to tell:* Author a custom level with exactly two rules — `disallow display report` and `allow display report:day-book` — assign it, sign in as its holder, and GET /day-book. If real: 200 (the allow wins) while every other report 403s. Control, which must go the other way: replace the second rule with `allow * report:day-book` (spec 2, a tie) and the same GET must now 403 with 'disallowed by rule'. If the first case also 403s, the specificity sum is not doing what authz.go:214-219 says.

## ROW 35 — Switch company (F3) — the whole session must re-scope, no row of the old company may leak

**Reachable from the keyboard today:** yes — F3 is a real bound action on every screen and the picker is fully keyboard-driven (SelectionList with autoFocus). The one thing that is NOT keyboard-reachable is signing out to change identity: that is a pointer-only button in the Chrome header (Chrome.tsx:128).

**Keystrokes**

```
Signed in as `browser-dev-owner` / `tally-demo-2026`. Press F3 from the Gateway (keymaps/tallyprime.ts:88 -> navMachine.ts:836-839, pushOnce('company-select')). The picker lists every company from GET /v1/me (browser-dev-owner is a member of dozens — psql-verified). Arrow to a row whose name differs from the one in the top bar, press Enter. Confirm the top bar name changed (Chrome.tsx:95, AppShell.tsx:355) and that the stack unwound to the Gateway (navMachine.ts:1316-1318). THEN reload the page (browser reload / F5 outside the app) and read the top bar again.
```

**Oracle**

```
(1) BROWSER STATE, read in DevTools: `localStorage.getItem('tally.companyId')` immediately after the F3 switch. Correct = the uuid of the company you just switched TO. Expected = still the uuid of the company you switched FROM.  (2) THE ROW ORACLE, after the reload: post nothing yet — instead read `select c.name, v.voucher_no, v.voucher_date from voucher v join company c on c.id=v.company_id order by v.created_at desc limit 5;` and compare with what the Day Book on screen shows; then key one F8 Sales, Ctrl+A, and run `select c.name from voucher v join company c on c.id=v.company_id where v.id='<the new voucher id>';` — correct is the company you selected with F3, and the failure mode is that it names the OLD one.  (3) For the second prediction: BEFORE signing in as demo-data-entry run `select count(*) from company_membership m join security_level sl on sl.id=m.security_level_id join app_user u on u.id=m.user_id where u.subject='demo-data-entry' and sl.is_owner;` (today: 0) and `select max(created_at) from company;`. Sign in. Re-run both.
```

**Predictions**

- **[high]** An F3 switch is never persisted, so it does not survive a reload — and the reload silently returns the operator to the PREVIOUS company while the top bar, the Day Book and every subsequent posting follow it. CompanySelectScreen calls setCompany only; persistCompanyId() has exactly two callers, both inside the one-shot bootstrap effect, and that effect is blocked from re-running by `bootstrapped.current`.
  - *evidence:* frontend/src/screens/CompanySelectScreen.tsx:88-96 (`const view = await client.getCompany(row.id); setCompany({...}); onSelect(COMPANY_SWITCHED_SENTINEL)` — no persist call anywhere in the file). frontend/src/app/CompanyContext.tsx:142-149 persistCompanyId, called ONLY at :202 and :257 inside the bootstrap effect; :188 `if (!bootstrap || company || bootstrapped.current) return;`. Sign-out clears the key (AuthContext.tsx:183) but a switch never rewrites it.
  - *how to tell:* If real: localStorage['tally.companyId'] is unchanged by the F3 switch, and after a reload the top bar shows the OLD company name and a voucher keyed post-reload lands in the OLD company_id. If not real, the key holds the newly selected uuid and the reload keeps you where F3 put you.
- **[high]** The session bootstrap never consults GET /v1/me, so any signed-in user with no persisted company id is silently handed a BRAND-NEW company that they OWN instead of the company they are a member of. For an invited Data Entry operator this means their first sign-in creates an empty tenant, makes them its Owner (seed_company_auth_defaults), and hides the books they were actually invited to behind an F3 they have no reason to press.
  - *evidence:* frontend/src/app/CompanyContext.tsx:187-270 — the bootstrap reads localStorage/?company= and otherwise goes straight to `client.createCompany({name:'My Company', ...})` at :251-256; `client.getMe()` appears only in CompanySelectScreen.tsx:43. The same effect also treats a 403 on the persisted id as 'dead id, mint a new one' (isUnusablePersistedId, CompanyContext.tsx:135-140), so a stale localStorage entry left by a previous user on the same browser produces the identical outcome — the hazard the COMPANY_STORAGE_KEY comment at :57-65 documents for sign-out but not for tab-close (the token lives in sessionStorage, the company id in localStorage).
  - *how to tell:* If real: after signing in as demo-data-entry, `select count(*) ... where u.subject='demo-data-entry' and sl.is_owner` goes 0 -> 1, a fresh `company` row named 'My Company' appears with created_at = now, and the top bar reads 'My Company' rather than 'Demo Traders'. If not real, the shell opens one of the five Demo Traders companies demo-data-entry already belongs to and no company row is created.
- **[low]** LoadedVoucher and LoadedDraft survive an F3. Both providers sit under AppShell, inside a CompanyProvider whose remount key is the USER id, not the company id, so setCompany does not remount them; the navMachine unwinds the stack but nothing clears the stashed DayBookEntry / OCR draft from the previous company.
  - *evidence:* frontend/src/app/AppShell.tsx:190-194 (LoadedVoucherProvider > LoadedDraftProvider wrap the screen area); frontend/src/App.tsx:145 `key={session.user.id || session.user.subject}`; frontend/src/app/LoadedVoucher.tsx:38-48 plain useState with no company awareness; navMachine.ts:1316-1318 unwinds the stack only.
  - *how to tell:* If real: DevTools React state on LoadedVoucherProvider still holds the old company's DayBookEntry (voucher_id, party, amount) after the switch. It is unlikely to leak a ROW on screen, because the alter frame re-fetches with the NEW companyID (VoucherScreen.tsx:921) and RLS turns that into a 404/403 — so treat a 404 on a drill immediately after an F3 as the symptom, not as a separate bug.

## ROW 36 — WebAuthn step-up for writes — 403 STEPUP_REQUIRED, ceremony, retry succeeds

**Reachable from the keyboard today:** yes, and unavoidably so — every company's Owner level is seeded requires_webauthn=true (psql-verified for both 'Demo Traders' and 'My Company'), so the FIRST write of every browser session hits it. The interceptor is wired (App.tsx:207-244), the ceremony is real (auth/webauthn.ts), and RPID/Origin default to localhost / http://localhost:5173, which is exactly the Vite dev origin (server.go:227-228) — so the usual origin-mismatch failure should NOT occur here.

**Keystrokes**

```
Sign in as `browser-dev-owner` / `tally-demo-2026` (this token carries NO amr — auth_login.go:266-274). Attach a CDP virtual authenticator FIRST (WebAuthn.enable + addVirtualAuthenticator, ctap2/internal, hasResidentKey true) or the ceremony cannot complete. Then key the first write of the session: F8, Enter through the party field, fill the amount, Ctrl+A. The 403 fires before the voucher — on the on-demand POST .../ledgers. Watch the Chrome header for [data-testid=stepup-pending] ('Confirm the passkey prompt') and, on failure, [data-testid=stepup-failed].
```

**Oracle**

```
(1) NETWORK, in order — the whole contract is the sequence: `POST /v1/companies/<C>/ledgers` -> 403 body `{"code":"STEPUP_REQUIRED","error":"webauthn step-up required"}`; then `POST /auth/webauthn/assert/begin` 200; (first time only) `POST /auth/webauthn/register/begin` + `/register/finish` 200; `POST /auth/webauthn/assert/finish` 200 with a token; then the SAME `POST .../ledgers` replayed -> 201. (2) psql: `select id,user_id,sign_count,created_at from webauthn_credential order by created_at desc limit 2;` — one new row after registration, and sign_count must strictly increase after each later assertion (`select sign_count from webauthn_credential where id='<id>'`). (3) The proof the retry actually wrote: `select l.name from ledger l where l.company_id='<C>' order by l.created_at desc limit 3;` — the on-demand ledger must EXIST, not merely have been attempted.
```

**Predictions**

- **[medium]** An Owner who cannot complete a passkey ceremony is permanently read-only with NO in-app remedy: the Owner level is the only level in a fresh company, it is seeded requires_webauthn=true, the level editor disables the WebAuthn checkbox for the Owner level, and the server 422s any Owner patch that is not the cutoff. The only escape is a direct UPDATE on security_level.
  - *evidence:* frontend/src/screens/MembersScreen.tsx:693-706 (`disabled={draft.ownerLocked}` on [data-testid=level-webauthn]) and :297-306 (owner patch body is `{ backdated_cutoff }` only, with the 422 explained at :648-654). backend seeds requires_webauthn=t on the Owner level (migration 0011 seed_company_auth_defaults; psql confirms `Owner | t | full | t` for every company). authz.go:278-282 webauthnDeny fires on every Create/Alter/Delete.
  - *how to tell:* If real: with no virtual authenticator attached, EVERY write in the session ends at 403 STEPUP_REQUIRED, the header shows PASSKEY STEP-UP FAILED, and no path through Users & Roles can turn the requirement off (the checkbox is greyed, and forcing the PUT returns 422). psql control: `select requires_webauthn from security_level where company_id='<C>' and is_owner` stays true. If not real, the checkbox is live for the Owner level and the PUT succeeds.
- **[medium]** A cancelled/refused passkey prompt is indistinguishable from 'this device holds none of these credentials', so a user who presses Escape on the prompt is walked into a REGISTRATION ceremony rather than an error, silently enrolling an extra credential on every cancel.
  - *evidence:* frontend/src/auth/webauthn.ts:356-362 (`if (isNotAllowed(err)) return null` — NotAllowedError covers refusal, timeout and no-matching-credential) and :368-387, where a null assertion unconditionally calls `register(deps, container)`; the trade-off is acknowledged in the comment at :383-386.
  - *how to tell:* If real: cancel the assert prompt once (CDP: set the virtual authenticator to reject, or setUserVerified false + isUserConsenting false) and then check `select count(*) from webauthn_credential where user_id=(select id from app_user where subject='browser-dev-owner');` — the count grows by one per cancel, and the register/begin+finish pair appears in the network log after each cancel. If not real, a cancel yields StepUpFailedError with no register/* calls and the credential count is unchanged.

## ROW 37 — Back-dated entry cutoff — a voucher before the cutoff is refused BY NAME, one after it still posts

**Reachable from the keyboard today:** partially — the CUTOFF can be set from the keyboard-reached Users & Roles screen (buttons are pointer-only). The REFUSAL can only be produced on the stock vouchers (Alt+F7/F8/F9/F10) and on the TDS challan deposit date (Gateway > TDS Challans > Alt+C). It CANNOT be produced on any of the six accounting vouchers F4–F9, because their Date field is not an input at all — so the row's headline case is unreachable on the vouchers a Tally operator would use for it.

**Keystrokes**

```
SET THE CUTOFF (Owner only, and this save itself needs Row 36's step-up): Gateway > Down to `Users & Roles` > Enter; click [data-testid=level-edit:Owner]; type a date into [data-testid=level-cutoff] (e.g. today minus 30 days, YYYY-MM-DD); click [data-testid=level-save]. Confirm the levels list now reads `· no writes before <date>` (MembersScreen.tsx:581-583).  TEST THE REFUSAL — NOT on F8/F9, which cannot be back-dated (see prediction 1). Use Alt+F7 (Stock Journal): the StockVoucherScreen's Date row is a real <input> ([data-testid=stock-date]). Clear it, type a date BEFORE the cutoff, fill one source and one destination line with a rate, Ctrl+A.  CONTROL: repeat with a date AFTER the cutoff; it must post.
```

**Oracle**

```
(1) SERVER, by name: the refused request must answer 403 with body `{"error":"voucher date is before the back-dated cutoff"}` (authz.go:270). A bare 403 with a different reason means the cutoff is not what refused it.  (2) psql SIDES + VALUE, never a total: `select l.name, sm.direction, sm.quantity, sm.value from stock_movement sm join stock_item l on l.id=sm.stock_item_id where sm.company_id='<C>' order by sm.created_at desc limit 6;` — after the REFUSED attempt there must be ZERO new rows; after the CONTROL there must be a matched OUT/IN pair whose `value` is non-zero on both sides. Also `select id, voucher_date, class from voucher where company_id='<C>' order by created_at desc limit 3;` to confirm the control's stored date is the one you typed, not today.  (3) The cutoff itself: `select name, backdated_cutoff from security_level where company_id='<C>';`.
```

**Predictions**

- **[high]** No accounting voucher can be back-dated from this UI at all. On F4–F9 the Date row renders a read-only <span> — FieldRow only becomes an <input> when `editable` is true, and `editable={draftRebuildable}`, which is false outside F9 bill-inbox draft mode. F2 is bound to 'date' and moves the highlight onto that span, so the key answers and nothing can be typed: a silently dead field rather than a silently dead key. Every F8/F9/F5/F6/F7 voucher is therefore dated `today()`.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:1754-1761 (`<FieldRow name="Date" ... editable={draftRebuildable} ...>`), :1694 (`const draftRebuildable = isDraftMode ? canRebuildDraft(...) : false`), :1883-1922 (FieldRow renders <input> only when editable, else <span>), :132-134 (`today()`), :1038/:1143/:1324 (`voucher_date: f.date`). keystroke/keymaps/tallyprime.ts:52 `F2: { kind: "date" }`; keystroke/navMachine.ts:1173-1181 only sets `active.field = "date"`. Contrast StockVoucherScreen.tsx:315 + :623-647, where the Date row IS an input.
  - *how to tell:* If real: on F8, pressing F2 then typing 2026-01-01 leaves the Date row showing today's date, the DOM node at that row is a <span> not an <input>, and after Ctrl+A `select voucher_date from voucher order by created_at desc limit 1` equals today — never the typed date. If not real, the row is focusable and the typed date is stored.
- **[medium]** The cutoff is inert for any voucher_date Postgres accepts but Go's strict layout does not. peekVoucherDate parses with exactly "2006-01-02" and returns nil on failure, and cutoffDeny skips entirely on a nil date — while the same string is handed to Postgres as a raw value for a `date` column, which accepts `2026-4-1`, `2026-04-01T00:00:00Z` and `01/04/2026`. So a client can post INTO a locked period simply by loosening the date's spelling. The pay-run peek fails CLOSED for precisely this reason (beforeEveryCutoff); the ordinary voucher peek fails OPEN.
  - *evidence:* backend/internal/server/server.go:3191-3203 (peekVoucherDate: `time.Parse("2006-01-02", probe.VoucherDate)`, `return nil` on error); backend/internal/authz/authz.go:265-273 (`if level.BackdatedCutoff == nil || !req.Action.IsWrite() || req.VoucherDate == nil { return Decision{}, false }`); backend/internal/ledger/ledger.go:1970 (`vDate any = nullIfEmpty(in.VoucherDate)` — the string goes to the driver unparsed); contrast server.go:3263-3284 (peekPayrollVoucherDate returns beforeEveryCutoff() on any parse failure) and the comment at server.go:3255-3262 explaining why.
  - *how to tell:* Not keyboard-reachable — curl it. With the cutoff set to 2026-08-01, POST /v1/companies/<C>/vouchers/journal with `"voucher_date":"2026-7-1"` (or "2026-07-01T00:00:00Z"). If real: 201, and `select voucher_date from voucher where id='<new>'` reads 2026-07-01 — inside the locked period. If not real: 403 'voucher date is before the back-dated cutoff', or a 400 from the ledger rejecting the format. Control that must refuse: the same body with "2026-07-01".

## ROW 38 — Password login + lockout — N bad passwords locks, and it unlocks as configured

**Reachable from the keyboard today:** yes — the login screen is the only fully keyboard-driven screen in the product and every step of this row is keyable. The UNLOCK half is not reachable by any keystroke: see prediction 1.

**Keystrokes**

```
On the LoginScreen (App.tsx:141; the screen is fully keyboard-driven — autoFocus on User name, Tab to Password, Enter submits): type a subject you are willing to lose for 15 minutes, Tab, type a wrong password, Enter. Repeat. Attempts 1-4 must render `data-failure="invalid"`; attempt 5 must flip to `data-failure="locked"` with 'ACCOUNT LOCKED … Try again after <RFC3339>'. Then type the CORRECT password and Enter — it must still say locked.  ⚠️ DO NOT DRIVE THIS AGAINST `browser-dev-owner`. It is the only identity with a browser password and an Owner membership; locking it costs 15 minutes of the whole audit and there is no unlock (prediction 1). Use `demo-data-entry`, or a throwaway subject.
```

**Oracle**

```
psql, on the two tables the policy actually reads.  (1) THE LOCK: `select subject, failed_attempts, locked_until, locked_until > now() as locked from app_user where subject='<S>';` — correct after the 5th failure is failed_attempts=5 and locked_until ≈ now()+15m. (2) THE COUNTER SEMANTICS, which is where this has bitten before: `select at, succeeded from auth_login_attempt where subject='<S>' order by at desc limit 10;` — there must be exactly 5 `succeeded=false` rows and no row for the attempts you make WHILE locked (see prediction 2). (3) THE RESET: after a successful login, `select failed_attempts, locked_until from app_user where subject='<S>'` must read 0 / NULL, and one further bad password must NOT re-lock (FailuresInWindow counts only failures after the last success — lockout_store.go:63-79).  Never trust the screen alone: the 423 and the 401 are rendered by the same <p>.
```

**Predictions**

- **[high]** There is no unlock anywhere in the product. ClearLock has exactly one caller — Locker.RecordSuccess — which is only reached by a SUCCESSFUL login, and handleLogin checks IsLocked and returns 423 before the password is ever compared. So a locked account cannot be unlocked by its owner, by an Owner of the company, by any HTTP route, or by any screen; the only remedies are waiting out the 15 minutes or an UPDATE on app_user.
  - *evidence:* backend/internal/authn/lockout.go:91-96 (RecordSuccess is the sole ClearLock caller — grep across the whole backend returns only lockout.go:95); backend/internal/server/auth_login.go:206-217 (step (b): `locked, until, err := s.lockout.IsLocked(...)` -> `writeLocked` before step (c) reads the credential); handleSetPassword deliberately does NOT clear a lock (auth_login.go:318-322). No admin route in handler_admin.go touches locked_until, and MembersScreen renders no lock state at all.
  - *how to tell:* If real: after the lock, the correct password still answers 423 with the SAME `until`, no button or key anywhere clears it, and `select locked_until from app_user where subject='<S>'` stays fixed until it expires. Positive control that the expiry path works: `select locked_until, locked_until > now() from app_user where subject='<S>'` after 15 minutes -> false, then the correct password answers 200 and the row goes to 0/NULL. If not real, some admin surface would expose an unlock.
- **[medium]** Attempts made WHILE an account is locked are not audited: handleLogin returns writeLocked from step (b) without calling RecordAttempt, so auth_login_attempt has no record of the traffic that arrives during the lock window — the exact period an operator investigating an attack most wants to see.
  - *evidence:* backend/internal/server/auth_login.go:209-217 — the IsLocked branch writes the 423 and returns; RecordAttempt is only reached via lockout.RecordFailure (auth_login.go:238) and lockout.RecordSuccess (:252), both below it. authn/lockout.go:76-88 IsLocked is a pure read.
  - *how to tell:* If real: `select count(*) from auth_login_attempt where subject='<S>'` is unchanged after three further sign-in attempts made while locked_until > now(), even though each returned 423. If not real, the count grows by three with succeeded=false.
- **[high]** The FIFTH bad password answers 423, not 401 — RecordFailure appends the attempt and THEN counts, so the threshold trips on the attempt that reaches it rather than the one after. A verifier that expects '5 x 401 then a 6th 423' will report a false failure.
  - *evidence:* backend/internal/authn/lockout.go:111-133 (`RecordAttempt(...false...)` at :112, then FailuresInWindow at :117, then `if failures >= l.policy.MaxFailures` at :121); DefaultLockoutPolicy MaxFailures=5 (lockout.go:24-31); auth_login.go:238-248 turns nowLocked into writeLocked immediately.
  - *how to tell:* If real: the login screen's error <p> carries data-failure="invalid" for attempts 1-4 and data-failure="locked" for attempt 5, and app_user.failed_attempts reads exactly 5. If not real, attempt 5 is still 'invalid' and the lock appears on attempt 6 with failed_attempts=6.

## ROW 40 — Printed documents (Ctrl+P) on each of the seven document classes

**Reachable from the keyboard today:** yes for six voucher classes; the seventh document (Bill of Supply) is a Sales voucher that charges no tax. Keys: F8 sales, Alt+F6 credit-note, Alt+F5 debit-note, F6 receipt, F5 payment, Alt+F8 delivery-note (frontend/src/keystroke/keymaps/tallyprime.ts:31-36,113-114 and the Alt+F7..F10 stock block). printTarget.ts:32-39 offers Ctrl+P on exactly those six and REFUSES the other seven locally with a written reason (printTarget.ts:49-62) — no HTTP call. Server side, printingdb.docKindForClass (backend/internal/printing/printingdb/document.go:423-446) accepts exactly the same six and splits Sales into KindTaxInvoice / KindBillOfSupply, so all seven DocKinds have a real layout in internal/printing (invoice.go:151-153 for the two invoice kinds, voucherdoc.go:215-232 for the other five). NOTHING falls back to a generic layout: an unknown kind is ErrUnsupportedVoucherKind -> 400. So 'has a layout' is not the risk here; 'the loader refuses to assemble the document' is.

**Keystrokes**

```
Per class: from Gateway press the class key (F8 / Alt+F6 / Alt+F5 / F6 / F5 / Alt+F8), fill the form, Ctrl+A to save, then Ctrl+P on the just-saved voucher (printTarget.ts:190-202 prints from the create response id). For Bill of Supply: F8 with the GST panel left at 0% / no tax. For Debit Note and Credit Note also try the alter path (Alt+G > Day Book > Enter on the note > Ctrl+P).
```

**Oracle**

```
PRIMARY (HTTP, since the PDF is the artefact): capture the network response of GET /v1/companies/{c}/print/vouchers/{class}/{id}. CORRECT = 200, Content-Type: application/pdf, body starts %PDF-1. and ends %%EOF, Content-Disposition names the class and the voucher number. DEFECT = 400/422 with a printingdb/printing sentence, or a PDF whose party block names the wrong ledger.
SECOND ORACLE (psql, read-only), which is what tells you WHY:
  -- what the note claims to adjust
  SELECT vv.voucher_no, vv.payload->>'class' AS class, vv.supplier_invoice_no, vv.supplier_invoice_date,
         ba.ref_type, ba.ref_name
    FROM voucher_version vv
    JOIN voucher_head vh ON vh.company_id=vv.company_id AND vh.current_version_id=vv.id
    LEFT JOIN bill_allocation ba ON ba.voucher_version_id=vv.id
   WHERE vv.payload->>'class' IN ('Credit Note','Debit Note');
  -- who the money voucher says the party is, vs which ledgers it actually posted
  SELECT vv.voucher_no, vv.payload->>'class' AS class, pl.name AS party_ledger_on_voucher,
         p.dr_cr, l.name AS posted_ledger, p.amount
    FROM voucher_version vv
    JOIN voucher_head vh ON vh.company_id=vv.company_id AND vh.current_version_id=vv.id
    JOIN posting p ON p.voucher_version_id=vv.id
    JOIN ledger l ON l.id=p.ledger_id
    LEFT JOIN ledger pl ON pl.id=vv.party_ledger_id
   WHERE vv.payload->>'class' IN ('Receipt','Payment');
CORRECT for a Receipt = party_ledger_on_voucher is the CUSTOMER and the DR leg is the bank. DEFECT = party_ledger_on_voucher IS the bank ledger (same name as the DR leg), which is what the PDF prints under 'Received From'.
```

**Predictions**

- **[high]** Ctrl+P on a DEBIT NOTE keyed through this app can NEVER produce a PDF. It always answers 422 'a Debit Note cannot be printed without the number of the original invoice it adjusts'. printingdb takes the against-invoice number for a debit note from voucher_version.supplier_invoice_no, and NO HTTP request can ever set that column: voucherReq has no field for it, so ledger.VoucherInput.Inward is nil on every voucher cored has ever created outside a Go test.
  - *evidence:* backend/internal/printing/printingdb/document.go:1738-1743 `doc.AgainstNumber = h.supplierDocNo` (read from vv.supplier_invoice_no at document.go:541); backend/internal/printing/voucherdoc.go:281-295 refuses when AgainstNumber or AgainstDate is empty; backend/internal/server/server.go:1659-1743 `voucherReq` — no inward/supplier_invoice field anywhere in the struct; backend/internal/ledger/ledger.go:248 `Inward *InwardDocument` has no HTTP writer (grep for SupplierInvoiceNo outside _test files returns only ledger/hash.go, ledger/inward_doc.go and the readers in printingdb/itc); `grep -rn supplier_invoice frontend/src` returns nothing.
  - *how to tell:* REAL: the status line reads 'Could not print Debit Note: ... the number of the original invoice it adjusts ...' and the network tab shows 422; `SELECT count(*) FROM voucher_version WHERE supplier_invoice_no IS NOT NULL;` returns 0. NOT REAL: a 200 with a PDF whose 'Against Invoice No.' box carries a real number — which would mean something outside the HTTP API populated that column.
- **[high]** Ctrl+P on a CREDIT NOTE keyed the default way also 422s, for the sibling reason: the note's against-invoice number is read only from a bill allocation whose ref_type is AGAINST, but the voucher form AUTO-DEFAULTS a single full-value reference of type NEW the moment a bill-by-bill party is chosen. A credit note therefore OPENS a new bill instead of naming the invoice it reverses, and prints nothing. It only prints if the operator manually switches the picker to 'Agst Ref' and types the original invoice number.
  - *evidence:* backend/internal/printing/printingdb/document.go:1727-1736 (`if b.refType == "AGAINST"` is the only source of doc.AgainstNumber for KindCreditNote); frontend/src/screens/vouchers/billAllocation.ts:110-120 `defaultNewRef` returns `refType: "NEW"`; frontend/src/screens/VoucherScreen.tsx:1301-1312 the useEffect that installs it whenever `hasRaiseParty`; frontend/src/screens/vouchers/BillAllocationPicker.tsx:301 new rows default to NEW in mode="raise", which is the mode credit-note gets (VoucherScreen.tsx:1237-1240, cfg.showGst true for credit-note per voucherConfig.ts:169-180).
  - *how to tell:* REAL: after keying Alt+F6 with the default bill panel, `SELECT ref_type, ref_name FROM bill_allocation ba JOIN voucher_version vv ON vv.id=ba.voucher_version_id WHERE vv.payload->>'class'='Credit Note';` returns NEW, and Ctrl+P answers 422 naming 'the number of the original invoice it adjusts'. NOT REAL: the row comes back AGAINST with the sale's invoice number, and a PDF renders with an 'Against Invoice No. ... dated ...' box.
- **[medium]** The printed RECEIPT and PAYMENT vouchers name the BANK as the counterparty. In single-entry mode the form puts the bank in BOTH `party` and `party_ledger_id`, and printingdb builds the document's Party block from party_ledger_id — so the PDF reads 'Received From: <the bank>' / 'Paid To: <the bank>' instead of the customer or supplier, whose ledger is one of the particular rows.
  - *evidence:* frontend/src/screens/VoucherScreen.tsx:1036-1043 (`party: accountName` where accountName is `f.party.trim()`, the field labelled 'Account'; `req.party_ledger_id = f.partyLedgerId`) and :1018-1021 where the SAME accountID is posted as `cfg.primarySide` (the bank leg); frontend/src/screens/vouchers/voucherConfig.ts:103-127 gives Receipt/Payment partyLabel 'Account' with the hint 'Debit the bank/cash, credit the party'; backend/internal/printing/printingdb/document.go:1636-1655 builds doc.Party from h.partyLedgerID; backend/internal/printing/voucherdoc.go:541-551 partyLabel() renders 'Received From' / 'Paid To'.
  - *how to tell:* REAL: the psql query above shows party_ledger_on_voucher equal to the ledger on the DR leg (the bank) for a Receipt, and the PDF's 'Received From' block carries the bank's name (and its address/GSTIN if the bank master has one). NOT REAL: party_ledger_id points at the customer and the DR leg is the bank — two different ledgers — and the PDF names the customer.
- **[medium]** Ctrl+P on a DELIVERY NOTE 422s whenever the free-text 'Party A/c Name' field was left blank, because the stock form never requires it and printing.validateVoucherDoc requires a party on every voucher document. The refusal will read 'a delivery note with no delivery address cannot be delivered'.
  - *evidence:* frontend/src/screens/vouchers/stockVoucher.ts:186-189 and :234 (`party` is an OPTIONAL descriptive string; buildStockVoucherRequest only throws when no ITEM line is keyed, :217-227); frontend/src/screens/vouchers/StockVoucherScreen.tsx:323-328 renders it as a plain FieldRow with no validation; backend/internal/printing/voucherdoc.go:256-262 refuses when v.Party is nil or its Name is blank; backend/internal/printing/printingdb/document.go:1650-1655 only sets doc.Party from h.partyName when that string is non-empty.
  - *how to tell:* REAL: key Alt+F8 with one item line and the Party field empty, Ctrl+A, Ctrl+P -> 422 naming 'the goods it accompanies'/'delivery address (consignee)'. Re-key with a party typed and it renders. NOT REAL: a PDF comes back with the consignee block blank or filled from somewhere else.
- **[high]** Every printed voucher's download filename carries the raw voucher UUID appended after the class and number, contrary to pdfFilename's own doc comment, which says it uses 'the FIRST non-empty' part. The code concatenates ALL parts. Cosmetic, but it means the PDF does not name itself cleanly and the comment is a lie a future edit will act on.
  - *evidence:* backend/internal/server/handler_print.go:117 `writePDF(w, pdfFilename(pathClass, number, voucherID.String()), pdf)` against handler_print.go:269-271 'using the FIRST non-empty ones and falling back through them', and the loop at :280-290 which appends every non-empty part joined by '-'.
  - *how to tell:* REAL: Content-Disposition on any successful voucher print is e.g. `sales-INV-2026-0041-1f2e3d4c-....pdf` (96-char cap). NOT REAL: it is `sales-INV-2026-0041.pdf` with no UUID.

## ROW 42 — Printed statements (Ctrl+P) per report — which reports have a real layout

**Reachable from the keyboard today:** yes for the nine reports the client can name. printTarget.ts:78-102 maps exactly nine screen report names to slugs plus bank-reconciliation (printTarget.ts:250-255). Server inventory (backend/internal/server/handler_print_reports.go:131-155): REAL LAYOUTS = trial-balance, balance-sheet, profit-and-loss, day-book, outstandings, stock-summary, bank-reconciliation, payroll-register, payroll-payslip. EVERYTHING ELSE in authz.KnownReportNames auto-mounts a route and answers 501 with a written sentence — of those, three are reachable from a screen: tds-register, sales-order-book, purchase-order-book. Three screens refuse LOCALLY with no HTTP call at all (cost-centre-summary, cost-category-summary, cost-centres — printTarget.ts:274-294), as do e-invoice and e-way-bill (:296-310) and bank-reconciliation before an account is picked (:243-249). payroll-register / payroll-payslip have layouts but NO screen, so they are unreachable from the keyboard.

**Keystrokes**

```
Alt+G, type the report name, Enter, then F2 to set a NARROW period (e.g. 2026-07-01 to 2026-07-31), then Ctrl+P. Do this for: Day Book, Trial Balance, Balance Sheet, Profit & Loss A/c, Outstandings, Stock Summary. Then the three 501s: TDS / TCS Register, Sales Order Book, Purchase Order Book — Alt+G, name, Enter, Ctrl+P. Then Gateway > Bank Reconciliation, F4 pick a bank, Ctrl+P (and Ctrl+P BEFORE picking, to see the local refusal). On the Trial Balance also press F5 (group-wise) before Ctrl+P.
```

**Oracle**

```
PRIMARY: the PDF bytes. Verify %PDF-1. .. %%EOF, then read the SUBTITLE line and the row count out of the PDF text (backend/internal/printing/pdftext.go is the repo's own extractor and is the same one the golden tests use). CORRECT for a period report = the subtitle repeats the F2 window ('From 01-Jul-2026 to 31-Jul-2026') and the row count equals the rows on screen. DEFECT = 'For all dates in the books' / 'As at the latest date in the books' with more rows than the screen shows.
SECOND ORACLE (psql, to size the discrepancy):
  SELECT count(*) FROM voucher_version vv JOIN voucher_head vh ON vh.company_id=vv.company_id AND vh.current_version_id=vv.id WHERE vh.is_deleted=false;                       -- what the PDF will list
  SELECT count(*) FROM voucher_version vv JOIN voucher_head vh ON vh.company_id=vv.company_id AND vh.current_version_id=vv.id WHERE vh.is_deleted=false AND vv.voucher_date BETWEEN '2026-07-01' AND '2026-07-31';  -- what the screen shows
For the 501s the oracle is the HTTP status + the status-line sentence, verbatim.
```

**Predictions**

- **[high]** Ctrl+P IGNORES THE F2 PERIOD ON EVERY REPORT. The print target carries a `query` only for bank-reconciliation, so /print/reports/{slug} is fetched with no from/to/as_on; the server's reportDate() treats a missing param as an empty string (not an error, not a default), and reporting.DayBook's `($1 = '' OR ...)` predicate then applies no filter. The printed Day Book lists EVERY live voucher and is subtitled 'For all dates in the books' while the screen shows the F2 window; the printed Trial Balance / Outstandings / Stock Summary say 'As at the latest date in the books'.
  - *evidence:* frontend/src/screens/printTarget.ts:216-230 returns `{kind:"report", slug, label}` with no `query` for every report screen (only the bank-recon branch at :250-255 sets one); frontend/src/screens/usePrint.ts:96 `client.printReport(company.id, target.slug, target.query)`; backend/internal/server/server.go:2556-2565 `reportDate` returns ("", true) for a missing param; backend/internal/reporting/daybook.go:78-79 `AND ($1 = '' OR vv.voucher_date >= $1::date)`; backend/internal/server/handler_print_reports.go:293-305 printPeriodSubtitle -> 'For all dates in the books', :283-288 printAsOnSubtitle -> 'As at the latest date in the books'. The screens DO scope: ReportScreens.tsx:128-137 (Day Book sends from/to) and :244 (Trial Balance sends periodTo as as_on).
  - *how to tell:* REAL: with F2 set to one month, the PDF subtitle reads 'For all dates in the books' and its row count matches the unfiltered psql count, not the filtered one. NOT REAL: the subtitle repeats the F2 dates and the counts match the filtered query.
- **[medium]** The TDS / TCS Register and both Order Books are the only screen-reachable reports that answer 501. Ctrl+P on each must surface the server's sentence verbatim on the status line ('printing the tds-register report is not implemented yet: the ACL rule for print report:tds-register is honoured, but no printed layout has been specified for it...'). The risk to check is that it reaches the status line rather than dying silently, because usePrint awaits an async handler.
  - *evidence:* frontend/src/screens/printTarget.ts:85 and :100-101 map all three to slugs deliberately so the 501 can be shown; backend/internal/server/handler_print_reports.go:148-155 the `default:` 501 branch; frontend/src/screens/usePrint.ts:99-108 turns the ApiError into 'Could not print <label>: <server message>'; frontend/src/app/AppShell.tsx:283-289 awaits `effect.pending` so an async refusal reaches the status line.
  - *how to tell:* REAL as designed: the status line carries the full server sentence and the network tab shows 501. DEFECT: the key does nothing visible, or the status line reads 'this key does nothing here' (which would mean the pending promise was dropped).
- **[medium]** The printed Stock Summary and the on-screen Stock Summary are DIFFERENT REPORTS. The PDF has ten columns — Opening/Inward/Outward/Closing x Qty and Value — while the screen (and therefore Alt+E) has seven and shows a QUANTITY under 'Inward' beside a MONEY figure under 'Outward (at cost)'. An operator comparing paper to screen cannot reconcile them, and only the paper shows Inward Value.
  - *evidence:* backend/internal/server/handler_print_reports.go:700-712 (ten Column headers) and :662-680 (the ten cells per row); frontend/src/screens/reports/StockSummaryScreen.tsx:198-212 (seven <th>) and :314-322 (the same seven export headers); frontend/src/api/types.ts:1153-1157 and :1179-1182 show the API returns inward_qty, inward_value, outward_qty AND outward_value — the screen consumes two of the four.
  - *how to tell:* REAL: the PDF's header row extracted by pdftext contains 'Inward Value' and 'Opening Qty' and the screen's table has neither. Also watch for a 500 here: ten columns on landscape can trip printing.ErrRowWiderThanTable / ErrFigureColumnTooNarrow on a long item name (handler_print.go:244-250 turns those into 500 and logs them verbatim). NOT REAL: both surfaces show the same column set.
- **[medium]** The printed Day Book carries NO amount total, and the footer sentence justifying that is factually false for this build: it says 'an altered voucher appears as both its original and its restatement', but reporting.DayBook joins on vh.current_version_id = vv.id, so exactly one row per live voucher is ever returned. The paper therefore withholds a total on a stated reason that the query contradicts.
  - *evidence:* backend/internal/server/handler_print_reports.go:541-551 (the Footer text and the 'DELIBERATELY NO COLUMN TOTAL' comment); backend/internal/reporting/daybook.go:71-80 — `JOIN voucher_head vh ON ... AND vh.current_version_id = vv.id WHERE vh.is_deleted = false` returns one version per voucher and drops tombstones entirely.
  - *how to tell:* REAL: alter a saved voucher, then `SELECT vv.version_no, vv.op FROM voucher_version vv JOIN voucher_head vh ON vh.current_version_id=vv.id WHERE vh.voucher_id='<id>';` returns ONE row, and the printed Day Book shows that voucher once while the footer claims it would appear twice. NOT REAL: the PDF lists the voucher twice (original + restatement), in which case the footer is right and the missing total is justified.

## ROW 43 — Export (Alt+E) — wire precision and CSV-injection neutralisation

**Reachable from the keyboard today:** yes, and there is exactly ONE export path in the whole frontend. Alt+E -> keymap (keystroke/keymaps/tallyprime.ts:56) -> SCREEN_ACTIONS.exportScreen (app/AppShell.tsx:93) -> useExportAction (export/useExportAction.ts:54) -> downloadSheet (export/download.ts:47) -> sheet.ts. Eleven screens register it: the six report-frame screens through useReportView (reports/reportView.ts:242), plus report-detail drill-downs, Stock Summary (:69), TDS/TCS Register (:112), Cost Centre Summary (:283), Cost Category Summary (:130) and both Order Books (:211). No screen advertises Alt+E without a handler (BANK_RECON_BAR, ORDER_REGISTER_BAR and TDS_CHALLAN_BAR all omit it). Alt+E on macOS now resolves via ev.code (keystroke/normalize.ts:110-118), so the Option-composes-'´' failure is closed. The only OTHER file-producing paths are Ctrl+P's PDF (screens/usePrint.ts:43) and the server-side payroll file writers (backend/internal/payroll/payrolldb/export.go), neither of which is Alt+E.

**Keystrokes**

```
(1) Alt+G > Trial Balance > Enter, F2 set a period, Alt+E. (2) Same on Day Book, Balance Sheet, Profit & Loss A/c, Outstandings, Stock Summary, TDS / TCS Register (F2 first — it refuses without one), Sales Order Book, Purchase Order Book, Cost Centre Summary (F4 pick an axis first), Cost Category Summary. (3) INJECTION: create a ledger named `=cmd|'/C calc'!A0` (Alt+C from a voucher, or key it as a counter-ledger on F6 so ensureLedger creates it), post something to it, then Alt+E the Trial Balance. (4) F12 > toggle 'Export as Excel XML', then Alt+E again on the same report to exercise the second writer. (5) Drill: Enter on a Trial Balance row, then Alt+E on the detail.
```

**Oracle**

```
PRIMARY (bytes of the downloaded file, not the screen): open the .csv in a hex/plain viewer.
CORRECT: starts with the UTF-8 BOM EF BB BF; every field is quoted; every money/date/voucher-number cell is the Excel string-literal form `"=""11800.0000"""` (i.e. `="11800.0000"` after CSV unquoting) with FOUR decimals and NO thousands comma; lines end CRLF; a ledger name starting = + - @ TAB or CR is wrapped the same way.
DEFECT (the exact shape this row lost paisa in before) is preserved on disk at /Users/yashbhalgat/code/tally-updated/.playwright-mcp/Day-Book.csv — 100 bytes, no BOM, no `="..."`, `"11,800.00"` with a grouping comma and two decimals. That file predates the T15 fix (commit 3f209ce, 2026-08-06) and is the regression signature: if a fresh Alt+E reproduces those bytes, the fix is gone.
SECOND ORACLE (psql), to prove the exported figure is the BOOK figure and not a rounded one:
  SELECT l.name, p.dr_cr, p.amount FROM posting p JOIN ledger l ON l.id=p.ledger_id ORDER BY l.name;
  -- and for the stock export:
  SELECT si.name, sm.direction, SUM(sm.qty) AS qty, SUM(sm.amount) AS value
    FROM stock_movement sm JOIN stock_item si ON si.id=sm.stock_item_id GROUP BY 1,2 ORDER BY 1,2;
CORRECT = the CSV cell equals p.amount character for character (4 dp). Assert SIDE and LEDGER NAME, never a total.
```

**Predictions**

- **[high]** Both properties are PRESENT in the current code and should hold when driven: money/dates/voucher numbers are emitted as `="…"` string literals from the fixed-4 WIRE strings (never formatMoney), and any text cell starting = + - @ TAB or CR is wrapped the same way. Every report screen supplies `exportCells`; the display-string fallback in exportRowFor is therefore never taken by a report that shows figures. This prediction is here to be FALSIFIED by a driven file rather than assumed.
  - *evidence:* frontend/src/export/sheet.ts:200-225 (FORMULA_TRIGGERS, isFormulaDangerous, forceText, csvCellBody), :246-256 toCsv quotes every field and uses CRLF, :249 headers go through the same guard; export/download.ts:37,72 prepends the BOM; reports/reportView.ts:176-179 exportRowFor falls back to display text ONLY when exportCells is absent; every money row supplies them — ReportScreens.tsx:266,279-283,362,454,560,573 and ReportDetailScreen.tsx:126-131,161-167.
  - *how to tell:* REAL (fixed): the Trial Balance CSV row is `"Acme Traders","Sundry Debtors","=""11800.0000""",""` and the file starts EF BB BF. NOT REAL (regressed): it is `"Acme Traders","11,800.00"` — the .playwright-mcp/Day-Book.csv shape.
- **[medium]** The Stock Summary export is BLIND TO INWARD VALUE — the exact blindness this repo was burned by. The API returns inward_qty, inward_value, outward_qty and outward_value, but the sheet writes only inward_qty (under a header reading 'Inward') and outward_value (under 'Outward (at cost)'). A Stock Journal that destroys VALUE while leaving quantity unchanged moves inward_value/outward_value and is invisible in this file, and the two adjacent columns silently mix a quantity with a money figure.
  - *evidence:* frontend/src/screens/reports/StockSummaryScreen.tsx:276-288 and :290-300 (cells: exactCell(it.inward_qty) then moneyCell(it.outward_value)) against the headers at :314-322 ('Inward','Outward (at cost)'); frontend/src/api/types.ts:1153-1157 and :1179-1182 declare all four fields; backend/internal/server/handler_stock_summary.go:81-82,125-126 emits inward_value on the wire; the PRINTED report does carry it (backend/internal/server/handler_print_reports.go:704-707).
  - *how to tell:* REAL: the exported Stock Summary CSV has seven columns and no cell anywhere equals SUM(sm.amount) for direction='IN' from the psql query above, while the Ctrl+P PDF for the same report does have an 'Inward Value' column carrying it. NOT REAL: an inward-value column is present in the CSV.
- **[medium]** A ledger name beginning with a formula trigger is neutralised by wrapping and NOT by stripping or prefixing, so the exported cell still reads back as the exact ledger name. The guard is startsWith-only, which matches the OWASP trigger set — verify that a name whose trigger is not first (e.g. `Acme =SUM(A1)`) is correctly left ALONE, and that the SpreadsheetML writer relies on ss:Type="String" instead of the wrapper.
  - *evidence:* frontend/src/export/sheet.ts:202-204 `FORMULA_TRIGGERS.some(c => value.startsWith(c))`; :212-214 forceText doubles inner quotes; :377-402 toSpreadsheetML writes `<Data ss:Type="String">` with the raw value and no wrapper (deliberate, per the comment at :365-376); the rejected apostrophe-prefix alternative is argued at :55-58.
  - *how to tell:* REAL: with a ledger named `=cmd|'/C calc'!A0`, the CSV field is `"=""=cmd|'/C calc'!A0"""` and parseCsv (sheet.ts:266) round-trips it back to the original string; the .xml variant is `<Data ss:Type="String">=cmd|'/C calc'!A0</Data>`. NOT REAL: the cell is a bare `"=cmd|'/C calc'!A0"` (executes on open) or a mangled `'=cmd...` (data silently edited).
- **[high]** Ctrl+P's own refusal on the Cost Centre Summary tells the operator 'Alt+E is not built here either; read it on screen' — which is false: that screen registers a real Alt+E handler and writes a file. A driven Ctrl+P then Alt+E on the same screen produces two contradictory answers.
  - *evidence:* frontend/src/screens/printTarget.ts:276-279 (the refusal string ending 'Alt+E is not built here either; read it on screen.') against frontend/src/screens/reports/CostCentreSummaryScreen.tsx:283-301 `useExportAction(... return costCentreSummarySheet(data))` and keystroke/buttonBar.ts:229-235, whose own comment says 'Alt+E JOINED THIS BAR THE DAY IT BECAME REAL'.
  - *how to tell:* REAL: on Gateway > Cost Centre Summary (F4 pick an axis, F2 set a period), Ctrl+P prints that sentence to the status line and Alt+E immediately afterwards downloads `Cost-Centre-Summary-<axis>-<from>-to-<to>.csv`. NOT REAL: Alt+E answers 'this key does nothing here'.
