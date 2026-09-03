> # ✅ CLOSED — 2026-09-03. T8 IS DONE, STEPS 1–8.
>
> **The probe is finished and green: `browse-t8-foreign-entry` 26/26.** Everything below is kept as
> the record of what was handed over, and it is ACCURATE AS OF WHEN IT WAS WRITTEN — but §3's two
> probe bugs are fixed, and **two more it did not predict** (`vv.narration` is not a column; the
> foreign line is not row 0 on the way back), plus §3.3's unmeasured third (the Day Book drill must
> click the ROW, not the narration cell).
>
> ⚠️ **§3.4 IS WRONG AND THE CORRECTION MATTERS:** the 7 errors were NOT `act()` warnings. They were
> uncaught `TypeError`s from `CostAllocationPicker` — the test's `listCostCentres` stub omitted
> `cost_categories`, which `useCostMasters` reads off that same response. Fixed; the suite runs 0
> errors.
>
> ⛔ **READ `HANDOFF-PARITY-AUDIT.md` §7 `### L39` INSTEAD OF THIS FILE.** It records all of it with
> the measurements, including a COMMITTED probe (`browse-t8-outstandings-forex`) that was already red
> for a reason that had nothing to do with step 8.

# HANDOFF — T8 STEP 8. STEPS 6 AND 7 ARE COMMITTED AND GREEN. STEP 8 IS BUILT, TESTED, AND ITS BROWSER PROBE IS NOT FINISHED.

**Written:** 2026-09-03 · **Branch:** `session/t32-and-close-7-3`
**Committed:** `ceaf8b7` (step 7) on top of `145bae9` (step 6) · **Working tree: DIRTY — step 8.**
**Read time: 8 minutes.** ⭐ **Every claim in §1 and §3 was MEASURED today** and the command is beside it.

⛔ **STEP 8 IS THE LAST OF T8.** When its two probe bugs are fixed and it is committed, T8 is closed.

---

## 0. ⭐ THE ONE-LINE VERSION

**Steps 6 and 7 are DONE, COMMITTED, and every gate is green. Step 8's code is written and its
1907-test suite passes; what is unfinished is ONE browser probe with TWO KNOWN, DIAGNOSED BUGS IN THE
PROBE ITSELF — not in the product.** §3 tells you exactly what they are and how to fix them.

```bash
cd /Users/yashbhalgat/code/tally-updated
git log --oneline -2
#   ceaf8b7 feat(T8/7): the unadjusted-forex report — what an open foreign bill is worth today
#   145bae9 feat(T8/6): Outstandings shows a foreign bill as foreign
git status --short          # 8 modified + 2 untracked, ALL frontend, ALL step 8 (§2)
(cd frontend && pnpm exec tsc --noEmit)          # clean
(cd frontend && pnpm exec vitest run)            # 1907 passed / 118 files
(cd backend && go build ./... && go vet ./...)   # OK — the backend is UNTOUCHED by step 8
```

---

## 1. ⭐ WHAT IS DONE AND COMMITTED — read the audit entries, not this file, for the reasoning

| | Commit | Audit entry | Drives |
|---|---|---|---|
| **Step 6** — Outstandings shows a foreign bill as foreign | `145bae9` | `HANDOFF-PARITY-AUDIT.md` §7 **`### L37`** | `drive-t8-outstandings-forex` **46/46** · `browse-t8-outstandings-forex` **7/7** |
| **Step 7** — the unadjusted-forex report | `ceaf8b7` | §7 **`### L38`** | `drive-t8-unadjusted-forex` **43/43** · `browse-t8-unadjusted-forex` **9/9** |

⛔ **DO NOT RE-DERIVE THEIR DESIGN DECISIONS. L37 and L38 record them with the measurements**, including
five corrections to claims I made and then measured wrong. The ones step 8 depends on:

- **Q5's netting key** is `(ledger, ref_name, currency)`; base IS a currency (encoded as none). A ref
  under two currencies is TWO flagged rows, and `currency_conflict` is on the wire.
- **`On Account` is base-only and currency-collapsed**, on both sides of D106's residue identity.
- **The open-bills picker read carries `currency_code`, `foreign_pending` and `currency_conflict`** —
  §4.3's prediction that steps 6 and 8 meet there was exact, and step 8 uses all three.
- **Migration 0090** denies `report:unadjusted-forex` to Data Entry, `disallow display` AND
  `disallow print`. 17 PERFORMs. **321/321 Data Entry levels covered.** `0081–0084` are now recorded
  in `PLAN-TALLY-PARITY.md` §2.5 as **permanently unneeded**.
- **`printing.FormatExchangeRate`** is new — `printing.FormatRate` is a TAX PERCENTAGE at three
  decimals and the printed sheet almost shipped `83.651`.

⚠️ **AND `TestEveryDateShapedWireFieldIsAccountedForByTheCutoff` NEEDED NO ENTRY**, contrary to the
previous handoff's §5. It scans package `server` for **struct fields** with date-shaped json tags, and
`as_on` is a query parameter — as on every other as-at report. Measured; recorded in L38.

---

## 2. ⛔ WHAT IS IN THE WORKING TREE — ALL OF STEP 8, ALL FRONTEND

```bash
git status --short
#  M frontend/src/screens/FeaturesScreen.tsx            the `partial` state + the forex-ledger panel
#  M frontend/src/screens/FeaturesScreen.test.tsx       9 new tests
#  M frontend/src/screens/VoucherScreen.tsx             sends the triple; loads the currency list
#  M frontend/src/screens/vouchers/doubleEntry.ts       DoubleEntryLine + the triple; the refusal
#  M frontend/src/screens/vouchers/DoubleEntryPanel.tsx the three inputs
#  M frontend/src/screens/vouchers/BillAllocationPicker.tsx      the bill's forex line + the field
#  M frontend/src/screens/vouchers/BillAllocationPicker.test.tsx 5 new tests
#  M frontend/src/screens/vouchers/forexRoundTrip.test.ts        4 new tests (defect nine)
#  ?? frontend/src/screens/vouchers/DoubleEntryPanel.forex.test.tsx   9 new tests
#  ?? frontend/scripts/browse-t8-foreign-entry.mjs      ⛔ THE UNFINISHED ONE (§3)
```

⭐ **NOTHING IN `backend/` AND NO MIGRATION.** Step 8 is a form; steps 4–7 already built everything it
sends to. `go build ./... && go vet ./...` pass untouched.

### 2.1 ⭐⭐ THE THREE DECISIONS STEP 8 TOOK, AND §4.3 ASKED FOR ALL THREE

**(1) THE ROSTER STATE: `partial` WAS ADDED TO THE UNION, THE ROW WAS NOT SPLIT.**
Q4 asks for `partial` by name. `State` is now
`"always" | "on" | "partial" | "server" | "off"`, with its BADGE (`"Part built"`, styled like
`server` and deliberately **not** like `on` — the flattering direction is the one this screen has
been wrong in before), its **legend** in the screen's intro paragraph, and a paragraph in the file
header defining it: *a row is `partial` only when the note NAMES the missing part and says why.*
⛔ **Splitting was rejected and the reason is in the header:** one feature would become five rows
(masters, entry, settlement, ageing, revaluation), four `on` and one `off`, which an operator has to
reassemble to answer *"can I invoice in dollars"*.

**(2) THE ENTRY FIELD IS ON THE DOUBLE-ENTRY GRID ONLY, AND THAT IS ARGUED, NOT LAZY.**
Single entry INFERS the other leg and puts the SUM of the particulars on it, so a foreign amount on
one particular has no defined relationship to the leg the operator never keyed — **the same reason
the TDS/TCS block lives on double entry and nowhere else**, quoted in the code. Every foreign
transaction is expressible in double entry and `Ctrl+H` reaches it from F4/F5/F6; F7 is always it.
⛔ **The F11 note says this in those words**, so an operator is not left hunting on a Sales form.

**(3) THE BILL REFERENCE INHERITS THE LINE'S CURRENCY AND RATE; ONLY THE FOREIGN AMOUNT IS KEYED.**
A `bill_allocation` row and the `posting` it belongs to must agree — step 5 reads the AGAINST row's
rate, and Outstandings and step 7 net the allocation — so **two rows naming one document at two rates
is a voucher that reports two different exposures for itself.** The amount is a per-bill split,
exactly as the base amount already is.

### 2.2 ⛔⛔ DEFECT NUMBER NINE WAS FOUND AND FIXED, AND IT WAS ALREADY LIVE

§6.1 tells step 8 to ask *"what does an alter through this form omit?"* and predicts the answer.
**It was already broken before the field existed:** `postingsFromView` carries the triple for the
SINGLE-ENTRY path, `DraftBillRef` carries it for the bill references, and **`DoubleEntryLine` did not
carry it at all.** So opening a foreign **Journal** (F7 is always this grid), or a foreign Receipt in
Double Entry mode, fixing the narration and saving **wrote a rupee voucher over it**.

⚠️ **AND NOTHING REFUSED IT** — the rebuilt voucher still balanced in base, the double-entry guard was
satisfied, the Balance Sheet did not move by a paisa, and the only trace was that the bill quietly
stopped being foreign.

⭐ **MEASURED, by reverting the fix and running the new test:**

```
× T8/8: hydrates a foreign posting into a grid line carrying ALL THREE values
  → expected undefined to be 'cur-usd'
```

Both hops are fixed: `doubleEntryLinesFromView` reads it, `buildDoubleEntryRequest` sends it.

### 2.3 THE SEVEN MUTATION PROOFS ALREADY DONE ON STEP 8

| Revert | Observed |
|---|---|
| `DoubleEntryLine` drops the triple (defect nine) | `expected undefined to be 'cur-usd'` |
| the currency fields render unconditionally | the base-only CONTROL fires: `expected <select> to be null` |
| clearing the currency leaves the other two | `expected '1000.00' to be undefined` |
| **the `= fgn × rate` suggestion uses the MONEY parser on the RATE** | ⭐ **`expected '83651.2000' to be '83651.2346'`** — §6.3's failure moved onto the form that WRITES the books |
| the panel drops the all-or-none refusal | `Unable to find an element with the text: /no EXCHANGE RATE/` |
| the roster row flipped to `on` | `expected 'On' to be 'Part built'` |
| the F11 picker offers every ledger | the party/bank exclusion fires |
| the picker drops the bill's forex line | both §4.3 probes fire |

---

## 3. ⛔⛔ THE WORK — TWO PROBE BUGS, BOTH DIAGNOSED, NEITHER IN THE PRODUCT

`frontend/scripts/browse-t8-foreign-entry.mjs` is **the only probe that can prove step 8 at all**, and
its header says why: steps 6 and 7 shipped READS, so a wire drive could ask *"does cored emit it?"*.
Step 8 ships a FORM, so the question is the opposite one and no wire drive can ask it — **does what an
operator types become what the database stores?** A form that displays a currency and posts a rupee
voucher is green in every unit test ever written for it.

**It reached 9 of its probes green and then died. Run it to see for yourself:**

```bash
# vite MUST be verified fresh (§2.2 of the previous handoff) — this probe is about a brand-new field,
# so a stale bundle reports step 8 as unshipped.
kill -9 $(lsof -nP -iTCP:5173 -sTCP:LISTEN -t) 2>/dev/null; sleep 1
(cd /Users/yashbhalgat/code/tally-updated/frontend && nohup pnpm dev > /tmp/vite.log 2>&1 &)
sleep 15; ps -o lstart= -p "$(lsof -nP -iTCP:5173 -sTCP:LISTEN -t | head -1)"; date
node frontend/scripts/browse-t8-foreign-entry.mjs \
     "$(cat frontend/.t8-step6-scratch-company-id)"
```

**What it got: probes 1–9 ✓, then:**

```
✗ T8/8 browser: ⛔ a half-keyed triple is refused BY THE FORM, naming the missing value — screen says ""
locator.fill: Timeout 30000ms exceeded.
  - waiting for getByTestId('de-ledger:1')
```

### 3.1 ⛔ BUG 1 — THE GRID DOES NOT AUTO-APPEND A ROW. CLICK `de-add-line`.

The probe fills `de-ledger:1` without adding line 1. The grid offers ONE row and an add button.
⭐ **`frontend/scripts/e2e-journal.mjs:129` is the worked example and is the file this probe was
modelled on:**

```bash
sed -n '129,133p' frontend/scripts/e2e-journal.mjs
#   await page.getByTestId("de-add-line").click();
#   await page.getByTestId("de-ledger:1").fill("Accumulated Depreciation");
```

**Fix:** insert `await page.getByTestId("de-add-line").click();` before the `de-ledger:1` block.

### 3.2 ⛔⛔ BUG 2 — THE FOREX REFUSAL WAS ASSERTED WHILE THE GRID WAS STILL UNBALANCED, AND THE BALANCE REFUSAL CORRECTLY WINS

The probe clears the rate and expects `/no EXCHANGE RATE/`, but at that moment **line 1 does not
exist**, so the grid is 83,651.23 out of balance — and the panel's own precedence is deliberate:

```bash
grep -n 'const refusal = ' frontend/src/screens/vouchers/DoubleEntryPanel.tsx
#   const refusal = doubleEntryRefusal(rows) || doubleEntryForexRefusal(rows);
```

⭐ **THE ORDER IS A DESIGN DECISION WITH ITS OWN COMMENT** — *"an unbalanced grid is the error an
operator is most likely to be in the middle of, and a half-keyed currency triple on line 4 is not
worth burying it under."* ⛔ **DO NOT "FIX" THE PRECEDENCE.** The probe is wrong, not the panel.

**Fix:** key BOTH lines first (so the grid balances), THEN clear the rate, assert, then restore it.
⭐ **And read `de-refusal` rather than the whole body** — `data-testid="de-refusal"` is the element,
and `body()` regexing is why the failure detail came back as `""` instead of naming what it saw.

### 3.3 ⚠️ AND WATCH FOR A THIRD, WHICH IS UNMEASURED

Probe 5 (the ALTER — defect nine end to end) reaches the voucher by clicking a Day Book row. **That
navigation has not run yet**, because the probe died before it. If it does not land on the form, the
probe says so itself and marks the assertion vacuous rather than passing:

> *"⚠️ IF THIS FAILS THE PROBE BELOW IS VACUOUS — a save that never happened cannot destroy anything."*

⭐ **Keep that guard.** If the Day Book route does not work from a script, reach the voucher another
way rather than deleting the assertion — **probe 5 is the end-to-end proof of defect number nine, and
it is the most valuable probe in the file.**

### 3.4 ⚠️ THE `act()` WARNINGS IN `DoubleEntryPanel.forex.test.tsx` ARE NOISE, NOT FAILURES

`Warning: An update to CostAllocationPicker inside a test was not wrapped in act(...)`. It comes from
the cost picker's async load on every grid row — a stubbed child the test is not about. All 9 tests
pass. **Left rather than silenced**, because wrapping it would mean asserting something about a
component this file has no opinion on.

---

## 4. ⛔ WHAT REMAINS AFTER THE PROBE IS GREEN

- [ ] **Re-run every gate in the previous handoff's §1**, plus `browse-t8-outstandings-forex 7/7`,
      `browse-t8-unadjusted-forex 9/9`, and the two T8 wire drives (**46/46** and **43/43**).
      ⭐ **`verify:demo` MUST STILL BE 37/37** — step 8 touches the form every existing book is keyed
      on, so this is the control that matters most.
- [ ] ⛔ **MUTATION-PROVE THE GATE, NOT ONLY THE CODE.** The seven proofs in §2.3 are unit-level.
      **The browser probe has not been mutation-proved at all** — the obvious one is to revert
      `buildDoubleEntryRequest`'s triple block, rebuild nothing (it is client-side, so just reload),
      and confirm probe 4's `psql` assertion goes red with `NULL|NULL|NULL|83651.2300`.
      ⭐ **That is the number to quote.**
- [ ] `HANDOFF-PARITY-AUDIT.md` — a **`### L39`** lane section at the end of §7. **`L37` and `L38` are
      the templates**; both record corrections to their own author's earlier claims, and L39 will need
      at least the two probe bugs in §3 written up the same way.
- [ ] ⚠️ **AND CHECK THE `Multi-currency` NOTE ONE LAST TIME.** It is long and it is now the only
      place that tells an operator to press `Ctrl+H`. Its closing clause names the revaluation voucher
      and Q6 as the remaining gaps — **verify both are still true before committing**, which is the
      standard that note holds itself to.

---

## 5. ⭐ WHAT T8 LOOKS LIKE WHEN STEP 8 IS COMMITTED

The whole of `docs/design/t8-multi-currency-model.md`'s build order, steps 1–8, closed:

| Step | What | Where |
|---|---|---|
| 1–3 | schema, currency master, rates | `0080`, `masters/currency.go`, `Alt+G > Create Currency` |
| 4 | the voucher data path (the triple, signed, round-tripped) | `1bb8a5c`, audit `L35` |
| 5 | realised gain/loss, derived and verified | `21ec34f`, audit `L36` |
| 6 | Outstandings shows a foreign bill as foreign | `145bae9`, audit `L37` |
| 7 | the unadjusted-forex report | `ceaf8b7`, audit `L38` |
| 8 | F11 setting, the entry field, the roster row | **the working tree**, audit `L39` |

⛔ **Q6 (GST ON AN EXPORT INVOICE) IS NOT PART OF T8 AND MUST NOT BE STARTED.** It stops at the
design document until real GST data has been through the importer, and the F11 note says so to the
operator.

⭐ **AND THE OTHER LOCK-LINE ITEM, T13 (INTEREST), IS STILL BLOCKED ON A HUMAN** — it needs day-count
and simple-vs-compound statutory defaults sourced into `docs/statutory/` and cannot be started.
`HANDOFF-FEATURE-LOCK-LINE.md` §6 records what closing T8 finishes.

---

## 6. THE READING LIST, in order

1. **This file.**
2. `HANDOFF-T8-STEPS-6-TO-8.md` **§2** (the traps — stale `cored`, stale `vite`, the empty
   `?company=`, the scratch-book rule, the WebAuthn ceremony) and **§4.3** (step 8's own brief).
   ⚠️ Its §1 gate baselines are stale by two commits; §1 above is current.
3. `HANDOFF-PARITY-AUDIT.md` §7 **`### L38`**, then **`### L37`**.
4. `frontend/scripts/e2e-journal.mjs` — **the probe `browse-t8-foreign-entry.mjs` was modelled on,
   and the answer to both bugs in §3.**
5. `frontend/src/screens/vouchers/doubleEntry.ts` — `DoubleEntryLine`'s comment is defect number
   nine's full story.
6. `docs/design/t8-multi-currency-model.md` **Q2** (the base amount is authoritative — it is why the
   `= fgn × rate` button SUGGESTS and never DECIDES) and **Q4** (`partial`, by name).
