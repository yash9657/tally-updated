# T8 — multi-currency: the model

**Written:** 2026-08-27, at `356bb69`, before any T8 code.
**Precedent:** `d-offline-entry-model.md` — a design document first, because the questions are
answerable in writing and answering them wrongly costs a migration.

⭐ **Every claim below has the command that produced it beside it.** Absences were produced with
`/usr/bin/grep`, per `HANDOFF.md` §9 — the shell's `grep` is a `ugrep` shim that silently skips files
it judges binary.

---

## 0. THE HEADLINE, BEFORE THE SIX ANSWERS

### 0.1 THE SCOPE IS SET, AND IT WAS SET BY THE USER — NOT RECALLED FROM TALLY

`HANDOFF-T8-MULTI-CURRENCY.md` §3 is correct and load-bearing: **`RESEARCH.md` pins nothing here.**

```
$ /usr/bin/grep -n -i "multi-currency\|multicurrency\|forex\|exchange rate\|foreign currency" RESEARCH.md
  1090:- **Document formatting:** … company-branded layouts, multi-currency, multi-copy.
```

**One hit, about print formats.** So the four §5 questions were put to the user and answered:

| Question | ⭐ ANSWER |
|---|---|
| How many rates per currency? | **One, per (currency, effective date).** The voucher stores its OWN rate and that stored rate is authoritative for that voucher; the table exists for DEFAULTS and for the revaluation report |
| One foreign currency or many? | **MANY.** So a currency master is a table of rows, and no report may have a single column called "foreign" |
| Forex Gain/Loss ledger | **The operator chooses an existing ledger.** Nothing is auto-created. A company that has not chosen one is REFUSED the realised posting, with a message naming what to do |
| Is the report enough for v1? | **YES — report only.** Realised gain/loss on settlement is IN. Unrealised is a REPORT that posts nothing, and the `FeaturesScreen` row must say exactly that |

⛔ **Anything below that reads like TallyPrime behaviour and is not cited to `RESEARCH.md` or to the
answers above is a DESIGN DECISION taken here, not parity.** Labelled as such where it occurs.

### 0.2 ⛔⛔ ONE BACKEND GAP THE SCOUTING REPORT DID NOT FIND, AND IT BREAKS Q5

`HANDOFF-T8-MULTI-CURRENCY.md` §4 Q3 says `bill_allocation` needs the foreign amount and rate "not
just `posting`". **True, and incomplete.** Outstandings does not read `bill_allocation` alone — it
nets a UNION of TWO tables:

```
$ sed -n '/FROM bill_allocation/,+25p' backend/internal/reporting/outstanding.go
   … FROM bill_allocation b JOIN voucher_version vv … JOIN voucher_head vh …
   ) ba  JOIN ledger l … GROUP BY ba.ledger_id, l.name, g.name, ba.ref_name
$ /usr/bin/grep -n "openingBranch\|ledger_opening_bill" backend/internal/reporting/outstanding.go
  125: … (ledger, ref_name) from TWO sources — bill_allocation rows belonging to the …
  234:   SELECT ob.ledger_id, ob.ref_name, ob.dr_cr, ob.amount, ob.due_date   ← ledger_opening_bill
```

And `ledger_opening_bill` has the same single money column, with no currency:

```
$ psql … -tAc "SELECT column_name||' '||data_type FROM information_schema.columns
               WHERE table_name='ledger_opening_bill' ORDER BY ordinal_position"
  id · company_id · ledger_id · ref_name · ref_type · dr_cr · amount numeric · due_date · created_at
```

⛔ **PUT THE FOREIGN COLUMNS ON `bill_allocation` ONLY AND A FOREIGN PARTY'S OPENING BILL SILENTLY
BECOMES A BASE BILL.** Its ₹ figure is real, its foreign column is blank, and the two net together
into one `ref_name` group — which is exactly the failure `HANDOFF-T8-MULTI-CURRENCY.md` §4 Q5 names:
*"a single-currency column that silently mixes them."* **Both tables get the columns, in 0080.**

### 0.3 ⚠️ AND A CORRECTION TO `HANDOFF.md` §6, FOUND WHILE TAKING A NUMBER

§6 says *"Highest number in the tree is **0073**"* and that `0073_order_book_acl.sql` is the
highest-numbered restatement of `seed_company_auth_defaults` with **13 PERFORMs**. **Both are stale.**

```
$ ls backend/migrations/*.sql | wc -l                      →  63
$ ls backend/migrations/*.sql | tail -1                    →  0079_stock_groups_and_compound_units.sql
$ for f in 0071 0072 0073 0076; do awk '/CREATE OR REPLACE FUNCTION seed_company_auth_defaults/,
      /^\$\$ LANGUAGE plpgsql/' …$f*.sql | /usr/bin/grep -c PERFORM; done
  0071 → 11 · 0072 → 12 · 0073 → 13 · 0076 → 14
```

⭐ **DERIVE FROM `0076_forecast_report_acl.sql`, 14 in-body PERFORMs.** The count is monotonic across
0071→0076, so no predecessor's rules were dropped — that is §6.2 step 2 run on the CURRENT base.
⚠️ Note `/usr/bin/grep -c PERFORM` on the whole *file* gives 22 for 0071 and 20 for 0076, which reads
as if 0076 dropped rules. It did not: 0071 also PERFORMs the seeder for existing companies outside
the function body. **Count the BODY, not the file.**

---

## 1. THE SIX ANSWERS

### Q1 — Where does the rate live? ⭐ BOTH PLACES, AND THEY HAVE DIFFERENT JOBS

| | `currency_rate` table | The rate stored ON the voucher |
|---|---|---|
| Keyed by | (company, currency, effective_date) | the posting / bill allocation row |
| Job | supply a DEFAULT when keying; supply "the rate as on" for the revaluation report | **be the historical truth of that voucher, for ever** |
| May it change? | yes — new rows are added as rates move | ⛔ **NEVER.** `posting` is immutable anyway (§Q2) |

⛔ **A LATER RATE MUST NEVER MOVE AN EARLIER VOUCHER.** That is why the voucher's rate is stored and
not looked up. The table is a convenience and a report input, never a join the ledger reads.

**Rate precision — the §2.3 decision, taken explicitly:**

- ⭐ **`NUMERIC(19,10)`, and quoted as BASE UNITS PER ONE FOREIGN UNIT** (`1 USD = 83.6500000000 INR`).
  The direction matters: quoting base-per-foreign makes the number LARGE for a strong currency, where
  4 dp would have quantised it.
- ⛔ **10 dp is deliberately generous and it costs nothing.** 4 dp is wrong for the reason
  `HANDOFF-T8-MULTI-CURRENCY.md` §2.3 gives, and 6 dp is still wrong for a weak currency: quoting IDR
  base-per-foreign gives `0.003333…`, where 6 dp carries a relative error of ~1.5e-4. At 10 dp every
  currency in use is exact to well past the paisa.
- ⛔⛔ **A RATE IS NOT `Money` AND MUST NOT REUSE `parseFixed4`.** `Money` is fixed-4
  (`api/types.ts:15`) and `parseFixed4` **truncates** the fifth digit onward
  (`screens/vouchers/itemInvoice.ts:82` — *"Digits beyond the fourth are TRUNCATED, not rounded"*).
  Feed it a rate and it silently becomes a different rate. **A distinct `Rate` type and a distinct
  parse/format pair, with a test that asserts a 10-dp rate survives a round trip.**

### Q2 — Which amount is authoritative? ⛔ THE BASE AMOUNT. CLOSED.

Not re-opened. The evidence, re-run today:

```
$ sed -n '150,175p' backend/migrations/0002_core_schema.sql
  CREATE TRIGGER posting_no_mutate BEFORE UPDATE OR DELETE ON posting …
  CREATE OR REPLACE FUNCTION check_double_entry() …
    SELECT COALESCE(SUM(amount) FILTER (WHERE dr_cr='D'),0), … FROM posting WHERE voucher_version_id=v_id;
    IF debit <> credit THEN RAISE EXCEPTION 'double-entry violation …'
$ psql … "…columns WHERE table_name='posting'"
  id · company_id · voucher_version_id · ledger_id · dr_cr · amount numeric · created_at
```

⭐ **So `amount` keeps meaning exactly what it means today, and the foreign figures go BESIDE it.**
A multi-currency journal balances in BASE and need not balance in any foreign currency — which is the
second, independent reason base cannot be derived.

**The three columns, on `posting` AND `bill_allocation` AND `ledger_opening_bill`:**

```sql
ALTER TABLE posting
  ADD COLUMN currency_code  TEXT           NULL,
  ADD COLUMN foreign_amount NUMERIC(19,4)  NULL,
  ADD COLUMN rate           NUMERIC(19,10) NULL,
  ADD CONSTRAINT posting_forex_all_or_none CHECK (
       (currency_code IS NULL AND foreign_amount IS NULL AND rate IS NULL)
    OR (currency_code IS NOT NULL AND foreign_amount IS NOT NULL AND rate IS NOT NULL));
```

- ⭐ **NULL MEANS BASE.** No backfill — `posting_no_mutate` forbids one, and every voucher already in
  the books IS base-currency, so absent-means-base is the truthful encoding rather than a workaround.
  (`ALTER TABLE … ADD COLUMN … NULL` is DDL and does not fire a row-level `BEFORE UPDATE` trigger.)
- ⛔ **THE `CHECK` IS §5.4's "THREE STORED VALUES" MADE A CONSTRAINT.** Two-of-three is the state
  where a report can compute a plausible wrong number, so the database refuses it.
- ⚠️ `foreign_amount` is `NUMERIC(19,4)` — a foreign amount IS money and 4 dp is right for it. Only
  the RATE needs 10.

> #### ⭐ THIS SHAPE WAS PROVED ON A SCRATCH DATABASE BEFORE IT WAS WRITTEN DOWN — WITH A CONTROL
>
> `createdb t8_probe`, the real `posting_immutable()` / `posting_no_mutate` pair from
> `0002_core_schema.sql:150`, two rows, then:
>
> | Assertion | Result |
> |---|---|
> | ⛔ **CONTROL** — `UPDATE posting SET amount = 1` | **REFUSED**: `posting is immutable; UPDATE is not permitted`. ⭐ The trigger was ARMED for the next line, which is the whole point of the control |
> | `ALTER TABLE … ADD COLUMN … NULL` + the `CHECK` | **`ALTER TABLE`** — DDL does not fire a row-level `BEFORE UPDATE` trigger |
> | Existing rows | both survived, `amount` intact, three NULLs — **NULL means base, no backfill** |
> | `CHECK` on two-of-three (`USD`, `1.0000`, rate NULL) | **REFUSED**: `violates check constraint "posting_forex_all_or_none"` |
> | `CHECK` on three-of-three, rate `83.6500000000` | accepted, and the 10th dp **round-tripped exactly** |
>
> ⚠️ Without the control the ALTER succeeding proves nothing — a trigger that was never armed and a
> trigger that correctly ignores DDL look identical. `dropdb t8_probe` afterwards.

### Q3 — REALISED gain/loss ⭐ IT IS A THIRD POSTING ON THE SETTLEMENT VOUCHER

A foreign bill raised at one rate and settled at another. The difference is knowable at settlement
because both rates are stored:

- the **`NEW` ref** row (`bill_allocation.ref_type = 'NEW'`) carries the bill's historical rate;
- the **`AGAINST` ref** row on the Receipt/Payment carries the settlement-date rate.

```
$ psql … "SELECT enumlabel …" → bill_ref_type: NEW, AGAINST, ADVANCE, ON_ACCOUNT
```

`realised = foreign_settled × (rate_settlement − rate_bill)`, posted to the operator-chosen ledger as
an **extra posting on the settlement voucher**, so the voucher still balances in base and
`check_double_entry` passes untouched.

⛔ **WHICH LEDGER: THE OPERATOR'S CHOICE, AND ABSENCE IS A REFUSAL, NOT A DEFAULT.**
`company_forex_settings.forex_gain_loss_ledger_id`. A settlement that would realise a difference on a
company that has not chosen one is **refused, naming the setting** — never posted to a guessed ledger
and never silently absorbed into the party balance. (Silent absorption is precisely the §6-item-2
class of hole this track exists to close.)

> ### ⛔⛔ IT IS A TABLE, NOT A COLUMN ON `company` — AND A CENSUS FOUND THAT, NOT TASTE
> This document's first draft said `company.forex_gain_loss_ledger_id`. It migrated cleanly and
> broke `internal/platform/tenant`'s delete-order census:
>
> ```
> tenant: foreign-key cycle among {account_group, ledger}: no order exists that removes children first
> ```
>
> ⭐ **The cycle is a LAYERING INVERSION, not a graph technicality:**
> `company → ledger → account_group → (company_id) → company`. `company` is the tenant ROOT and every
> scoped table is its child, so the moment the root points at one of its own descendants **no tenant
> can be torn down** — which breaks backup/restore (T14) and every harness that mints a company.
> ⚠️ **Nothing about the column was wrong at the SQL level.** It is exactly the defect that ships
> silently and resurfaces as *"tenant delete sometimes fails"*. Shape now mirrors
> `company_tds_profile` (0070): the key IS the company.

⚠️ **`ADVANCE` and `ON_ACCOUNT` have no bill rate to difference against.** They are foreign-tagged and
carried, and they realise nothing until they are allocated. State this rather than computing zero.

### Q4 — UNREALISED gain/loss ⭐ A REPORT THAT POSTS NOTHING, IN v1

Per the user's answer. The report reads open bills — the same UNION §0.2 describes — and shows, per
bill: foreign pending, the bill's rate, the rate as on the report date, the base figure carried in the
books, and the difference.

⛔ **IT POSTS NOTHING, AND THE PRODUCT MUST SAY SO ON THE SCREEN AND IN F11.** The revaluation voucher
is deferred, and §2.2 already proves it can only ever BE a voucher (postings are immutable), so
deferring it costs no design debt — the schema for v1 is the schema it would need.

⚠️ **THIS IS THE ROW MOST AT RISK OF THE `FeaturesScreen` FAILURE MODE:** *"a toggle that flips a flag
with nothing behind it is worse than no toggle at all."* The v1 row is `partial`, not `on`, and its
note says the unrealised difference is REPORTED and not POSTED, in those words.

### Q5 — What does OUTSTANDINGS show for a foreign bill? ⭐ SEPARATE COLUMNS, AND A DISCLOSED CONFLICT

⛔ **NEVER ONE COLUMN.** Today `Bill.Pending` is a single `decimal.Decimal`
(`backend/internal/reporting/outstanding.go:48`) and every bill lands in it. A foreign bill's base
figure belongs there — it still ties to the Balance Sheet — and the foreign figure gets its **own two
fields**: `CurrencyCode` and `ForeignPending`, blank for a base bill.

⛔⛔ **AND THE NETTING KEY GAINS THE CURRENCY.** Group by `(ledger, ref_name, currency_code)`, not
`(ledger, ref_name)`. A `ref_name` that appears under two different currencies is a **data error, not
a sum** — it must be DISCLOSED on the report, because netting USD against INR under one bill number
produces a number that is wrong in a way nothing on the page reveals. Same class as track D's
provisional-number problem: **two different things must never share a column.**

⚠️ **THE GRAND TOTAL AND THE AGEING SLABS STAY BASE-ONLY.** Summing mixed currencies is meaningless
and a total that silently did it would tie to nothing. This is a design decision, not parity.

### Q6 — GST on an export invoice ⛔ DESIGN ONLY. BLOCKED, AND STAYING BLOCKED.

`HANDOFF-T8-MULTI-CURRENCY.md` §6 item 1 stands: **do not touch `classifyTaxLedger`, `inferRate` or
the GSTR-1 B2B join.** They have never met real GST data, and `HANDOFF.md` §10 item 1 is still the
commercial blocker.

What is recorded now, for when the file arrives:

- GSTR-1 needs **INR**, so the base amount the schema already makes authoritative (Q2) is the figure
  the return reads. ⭐ **Q2's answer is what makes Q6 tractable later** — no separate conversion.
- ⛔ **CITE THE RULE BEFORE RELYING ON IT.** Rule 34 CGST (the CBIC-notified rate for the date of the
  invoice) is the *recollected* answer, and this document does not have it cited. It is a QUESTION.
  ⚠️ Note it may differ from the rate the operator keyed for the books — in which case the invoice and
  the return legitimately use two different rates, and the schema must be able to say so. **The v1
  columns do not encode that.** Leave it; do not pre-build for an uncited rule.

---

## 2. WHAT 0080 CONTAINS — AND WHY THE ACL NUMBER IS SEPARATE

Per `HANDOFF.md` §6: ⛔ **RESERVE BLOCKS FOR TABLES, TAKE THE SEEDER NUMBER AT THE END.**

**Tables** (from the free-for-tables block — 0048, 0051–0061, 0065, 0066 are free; 0041/0044 are
absent from the tree entirely):

1. `currency` — the master. (company_id, code, symbol, formal_name, decimal_places). Many per company.
2. `currency_rate` — (company_id, currency_code, effective_date, rate `NUMERIC(19,10)`).
3. The three columns + `CHECK` on **`posting`**, **`bill_allocation`**, **`ledger_opening_bill`**.
4. `company_forex_settings` — key IS the company, one row. ⛔ **A TABLE, not columns on `company`;
   see Q3's box for the delete-order cycle that proves why.**

⭐ **AND THERE IS DELIBERATELY NO `forex_enabled` FLAG.** The presence of a currency master IS the
enablement: every foreign column is FK-bound to `currency`, so a company with no currency rows cannot
key a foreign voucher at all. A boolean beside that is a **second source of truth**, free to be off
while currencies exist, and every screen would then have to decide which it believed. F11 reports the
count. This is also `FeaturesScreen`'s own doctrine honoured rather than worked around — *a toggle
that flips a flag with nothing behind it is worse than no toggle at all.*

**The ACL number, taken LAST, derived from `0076_forecast_report_acl.sql` (§0.3):**

⛔⛔ **THE UNADJUSTED-FOREX REPORT NEEDS AN ACL MIGRATION IN THE SAME COMMIT — MANDATORILY.**
`HANDOFF.md` §6.1: migration 0011 seeds a coarse `allow display report` matching *any* `report:NAME`,
so **a new report is GRANTED to Data Entry the moment it exists.** The refusal is the thing that must
be written, and it must write **both** `disallow display` **and** `disallow print` — a report that
cannot be displayed but can be printed is a leak with a paper trail.

⭐ **The currency MASTER needs no ACL row** — §6.1's bare-resource-token rule: Data Entry's
`default_access = 'none'` denies a token never mentioned.

---

## 3. WHERE THE SURFACE GOES

- **`"currency"` is a new `MasterKind`, not a screen.** Today's set is exactly
  `ledger · group · voucher-type · unit · stock-item · stock-group` (`keystroke/types.ts:463`), and
  `types.ts:474` states the test: *MasterCreateScreen's shape is "a name, one picker, some details"*.
  A currency is a code, a symbol, a formal name and decimals — it fits, and unlike a cost centre it
  needs no axis to exist.
- **A new `ScreenKind` needs 15 registration points and `tsc` enforces NONE** (`HANDOFF.md` §7.2).
- **Report name — check the collision before using it.** `REPORT_NAMES` today is
  `Balance Sheet · Profit & Loss A/c · Trial Balance · Day Book · Outstandings · Stock Summary ·
  TDS / TCS Register · Order Book · Sales Order Book · Purchase Order Book · Cash Flow · Anomalies ·
  Bills Payable`. ⛔ **A label that is a SUBSTRING of a shipped one is a silent collision** — check
  the candidate against `REPORT_NAMES` **and** `GatewayScreen.tsx` at build time, in both directions.
- ⚠️ **`ledger_balance` IS MAINTAINED INCREMENTALLY** (`ledger.go:2338`, `INSERT … ON CONFLICT …
  credit = credit + EXCLUDED`) and it sums `posting.amount`. ⭐ **Because Q2 leaves `amount` alone,
  T8 does not move the Balance Sheet at all** — unlike T9. `verify:demo`'s 34/34 should hold
  unchanged, and **if it moves, that is a defect, not a re-pin.** That is a free oracle: use it.

---

## 4. THE ORACLE FOR THIS TRACK — NAMED BEFORE THE FIRST TEST

⛔ **A FOREX TEST WHOSE ONLY ASSERTION IS THAT THE VOUCHER POSTED PROVES NOTHING.**
Two of the three stored values can be wrong while the voucher balances perfectly.

Every forex test asserts, separately:

1. the **base** amount (and that the voucher balances in base),
2. the **foreign** amount,
3. the **rate**,
4. and — for a settlement — the **realised difference** and **which ledger took it**.

⚠️ **And the free oracle from §3:** `verify:demo` 34/34 must be UNCHANGED by every T8 commit. A T8
change that moves the Balance Sheet has touched `posting.amount`, which Q2 forbids.

### 4.1 ⭐ WHAT 0080 IS ACTUALLY GUARDED BY — `internal/ledger/forex_schema_test.go`

Four tests, each mutation-proved against a deliberately broken schema (predicted, then observed,
then reverted and re-run green):

| Test | Mutation | Predicted → observed |
|---|---|---|
| base currency has no master | `DROP TRIGGER currency_not_base` | `'INR'` accepted → **FAIL** ✅ |
| all-or-none across the triple | the `CHECK` replaced with `CHECK (true)` | two-of-three accepted → **FAIL** ✅ |
| a rate keeps 10 decimals | `rate TYPE NUMERIC(19,4)` | `0.0033333333` → `0.0033` → **FAIL** ✅ |
| base is the summed column | `check_double_entry` pointed at `foreign_amount` | base-balanced voucher **REFUSED** → **FAIL** ✅ |

⛔ **THE FOURTH IS THE ONE THAT MATTERS, AND IT IS A PAIR.** A voucher that balances in BASE but not
in foreign must COMMIT; a voucher that balances in FOREIGN but not in base must be REFUSED. Only the
second says *which column the trigger actually sums* — a suite with the first alone would pass just
as happily if the trigger had been pointed at `foreign_amount` all along. ⭐ The mutation proved
itself non-vacuous by reporting `debit 1000.0000 <> credit 920.0000`: the FOREIGN figures, so the
mutation demonstrably took effect.

---

## 5. THE BUILD ORDER THAT FOLLOWS

1. ⭐ **MOVED TO THE AUDIT LANE — `HANDOFF-AUDIT-THE-UNDRIVEN.md` row U1.** Its premise was wrong
   (§6) and settling it needs a `Alt+O` drive plus a REAL Tally forex export, which is that lane's
   job and `HANDOFF.md` §10 item 1's ask. It is no longer blocking T8.
2. ✅ **DONE (`20dd96f`) — the 0080 block: the tables, the columns, the `CHECK`.** `verify:demo`
   34/34 unchanged, 7035 existing postings all base, Dr = Cr untouched. Four schema invariants
   mutation-proved (§4.1).
3. ✅ **DONE — the currency `MasterKind` + the rate write path.** `masters/currency.go`,
   `GET/POST /currencies` and `POST /currencies/rates`, the typed client, the faithful mock and
   `Alt+G > Create Currency`. **Driven 15/15 in a real browser**; five service tests, four mutations.
   ⭐ **AND THE `FeaturesScreen` ROW WAS SPLIT RATHER THAN FLIPPED** — `Multi-currency` stays `off`
   because you still cannot key a foreign voucher, and the masters got a row of their own that is
   wholly true. Same treatment as the offline pair, for the same reason.

   > ⛔ **THREE REGISTRATION FACTS WORTH CARRYING, ALL MEASURED:**
   >
   > 1. ⭐ **A NEW `MasterKind` IS NOT LIKE A NEW `ScreenKind`.** `HANDOFF.md` §7.2 says a
   >    `ScreenKind` needs 15 registrations and `tsc` enforces NONE. A `MasterKind` is the
   >    opposite: `SECOND_FIELD`, `NAME_LABEL`, `NOUN`, `DETAIL_FOCUS` and `masterGoto.test.ts`
   >    are all `Record<MasterKind, …>`, so **`tsc` named every missing one.** The Go To test's
   >    own comment says that is exactly what it is for — a master form no keystroke reaches is
   >    register row 20, for five kinds at once.
   > 2. ⛔ **THE ROUTE STILL NEEDS `isMasterPath`, AND NOTHING TYPE-CHECKS THAT.** `routeAction`
   >    default-denies an unmapped `{companyID}` route, so a new route with no entry compiles,
   >    passes every Go test (they call the SERVICE, not the route) and is **403 for the Owner**.
   >    Only an HTTP call through the real middleware catches it — probe 1 of the drive.
   > 3. ⭐ **A NEW DATE-SHAPED WIRE FIELD IS A REGISTRATION POINT, AND NOTHING WARNS YOU.**
   >    `TestEveryDateShapedWireFieldIsAccountedForByTheCutoff` scans package `server` BY SHAPE
   >    and failed the build on both `currencyRateReq.effective_date` **and the response field
   >    `currencyListItem.latest_rate_date`**. It demands a decision plus a WRITTEN REASON, because
   >    this exact gap shipped open three times (`wage_month`, `deposit_date`, `bank_date`) and
   >    **two of the three shipped with a comment asserting the control was in place.**
   >    ⛔ A rate's `effective_date` is deliberately NOT judged by the cutoff, and the reason that
   >    makes it safe is Q1's design: back-dating a rate moves no posted voucher, because a voucher
   >    stores its own. ⚠️ **If a later track ever makes the ledger read `currency_rate`, that
   >    classification becomes false** — the entry says so, in the file.
4. **Foreign entry on a voucher** — the three values stored, the client's `Rate` type NOT `Money`.
5. **Realised gain/loss on settlement**, including the REFUSAL when no ledger is chosen.
6. **Outstandings gains its two fields and the currency in its netting key**, incl. the mixed-currency
   `ref_name` disclosure.
7. **The unadjusted-forex report** + its ACL migration, same commit, `disallow display` AND
   `disallow print`.
8. **F11 last** — enforcement first, switch second — and the `FeaturesScreen` row says `partial` and
   names the missing half.

⛔ **Q6 IS NOT IN THIS LIST.** It stops at this document until real GST data has been through the
importer.


---

## 6. ⛔⛔ THE PROBE THAT OVERTURNED THE SCOUTING REPORT'S §6 ITEM 2

`HANDOFF-T8-MULTI-CURRENCY.md` §6 item 2 says the importer *"ignores currency entirely… A real Tally
file containing foreign-currency vouchers imports them as if the amounts were rupees — **silently**,
with no disclosure."* The ABSENCE it cites is real:

```
$ /usr/bin/grep -rni "currency" backend/internal/migration/*.go | /usr/bin/grep -vi concurren
  -> EMPTY          (and the XML types in internal/integration/tallyxml.go model no currency either)
```

⛔ **BUT "NO CURRENCY CODE" DOES NOT IMPLY "SILENT". IT WAS NEVER MEASURED. IT IS NOW, AND IT IS
HALF FALSE.** A throwaway `TestProbeForeignAmountToday` against the UNCHANGED build:

| `<AMOUNT>` | `TallyLedgerEntry.Magnitude()` today |
|---|---|
| `83650.00` | `83650` ✅ |
| `$ 1000.00` | ⛔ **ERROR** — `bad AMOUNT "$ 1000.00": can't convert … to decimal` |
| `1000.00 USD` | ⛔ **ERROR** — same shape |
| `1000.00/USD` | ⛔ **ERROR** — same shape |
| `83,650.00` | ⛔ **ERROR** — ⚠️ **and this one is NOT about currency at all. See below.** |

⭐ **SO A CURRENCY-DECORATED AMOUNT IS ALREADY LOUD — IT REFUSES THE FILE.** Not silent. The
importer cannot absorb `$ 1000.00` as ₹1,000 because `decimal.NewFromString` will not read it.
⚠️ **This matters for the fix:** the work is NOT "stop absorbing", it is **"say what was refused in
words an operator can act on"** — today the message is `can't convert $ 1000.00 to decimal`, which
names a symptom and not a cause.

⛔ **AND THE SILENT SHAPE IS A DIFFERENT ONE, WHICH THIS PROBE CANNOT RULE OUT:** an export where
`<AMOUNT>` carries the plain BASE rupee figure and the forex detail (rate, foreign value) sits in
SIBLING elements the parser drops. There the books are **correct in rupees** and only the foreign
detail is lost — a real fidelity gap, but a much smaller and quieter one than §6 item 2 claims.
⛔ **Deciding which shape TallyPrime actually emits needs a REAL FOREX EXPORT, which this repository
does not have** — the same missing-evidence blocker as `HANDOFF.md` §10 item 1. **Do not guess the
element names.** `HANDOFF-T8-MULTI-CURRENCY.md` §3 is explicit that recollection of Tally's XML is
not research, and `SplitQtyUnit`'s doc comment is the only *evidenced* statement in this repository
about how Tally decorates a value (`"100.00 BAG"`, `"82.50/BAG"`).

### 6.1 ⭐ THE NAME-FREE DISCLOSURE, AND IT IS MEASURED AT ZERO COST

Both shapes are covered without naming a single Tally forex element, by capturing what the parser
does not model (`xml:",any"`) and disclosing the distinct element NAMES — aggregated, not per
occurrence — in the `Result.Warnings` field that already exists for exactly this
(`migration.go:89` — *"⛔ A CLEAN IMPORT IS NOT THE SAME AS AN EMPTY Warnings"*).

**The noise question was measured, not assumed:**

```
$ python3 …  # every element name in corpora/migration/*.xml vs every xml:"…" tag in tallyxml.go
  distinct element names in corpora: 36
  modeled by the Go structs       : 85
  UNMODELLED, by frequency        : (none)
```

⭐ **ZERO unmodelled elements across all eight corpora, including `06-tallyprime-real-shapes.xml`.**
So the disclosure is SILENT on everything that exists today and fires only on something genuinely
new — which is the only kind of warning an operator keeps reading.

⚠️ **THE HONEST LIMIT OF THAT NUMBER:** the corpora are curated files this repository wrote. A real
TallyPrime export is verbose and will carry element kinds these eight do not. That is an argument for
**aggregating by name** (bounded by the number of distinct kinds, a few dozen at worst) and against
one warning per occurrence — never an argument that the real number is also zero.

### 6.2 ⚠️ AN UNRELATED FRAGILITY THE SAME PROBE FOUND — RECORDED, NOT FIXED

`Magnitude()` refuses `83,650.00`. `SplitQtyUnit` **strips thousands separators on purpose**, and
says why: *"Commas are thousands separators in Tally's own output and are not part of the number."*
So the codebase states that Tally emits comma-grouped numbers, and the three AMOUNT/OPENINGBALANCE
readers (`Magnitude`, `OpeningSide`, `OpeningMagnitude`) do not strip them while the quantity reader
does.

⛔ **IT IS LATENT, NOT ACTIVE, AND IT IS NOT A T8 DEFECT.** The real 121-voucher export imported
cleanly, so `AMOUNT` carried no commas in that file; the documented comma habit may be confined to
quantity/rate strings. **Do not "fix" it speculatively** — accepting commas everywhere would also
make a genuinely malformed value readable. ⭐ **Log it against the defect register with the probe
above as its evidence, and settle it when a second real export arrives.**
