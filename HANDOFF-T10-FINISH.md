# HANDOFF — T9 AND §3 ARE **DONE AND DRIVEN**. T10's PRODUCT IS BUILT; ITS **PROOF** IS NOT.

**Written:** 2026-08-25 · **Branch:** `session/t32-and-close-7-3` · **Tree:** clean, 3 commits ahead
of `3b116c8`

> ## ⛔ READ THIS BOX FIRST — IT IS THE WHOLE STATE OF THE WORLD
>
> | Lane | State | Where the proof is |
> |---|---|---|
> | **T9** FIFO/LIFO | ✅ **DONE** | `d574aa2`, driven **21/21** (`frontend/scripts/drive-valuation.mjs`) |
> | **§3** inventory on import | ✅ **DONE** | `64887c4`, driven **10/10** (`frontend/scripts/drive-tally-import.mjs`) |
> | **T10** stock groups + compound units | 🟡 **BUILT, UNPROVEN** | `9cc1fed` — backend + screens complete and green; **four things missing, all listed in §1** |
>
> **Every gate is green right now:** `go build`/`go vet` clean · `go test ./... -timeout 60m`
> exit 0 · `pnpm exec tsc --noEmit` clean · `pnpm vitest run` **1548 / 92 files** ·
> `pnpm verify:demo` **34/34** · `python3 corpora/validate.py` OK (inventory=11, migration=6).
>
> ⛔ **`HANDOFF-NEXT-BUILD.md` §2 AND §3 ARE NOW STALE — they describe T9 and §3 as the next
> track.** They are done. `git rm HANDOFF-T10-FINISH.md` and fold §2/§3 into `HANDOFF.md` when
> the four items below land, exactly as that file asks.

---

## 0. WHAT LANDED, IN ONE PASS, SO YOU DO NOT RE-DERIVE IT

Read the three commit messages (`git log -3`) — they are long on purpose and they carry the
reasoning. The one-line map:

| Commit | What is now true |
|---|---|
| `d574aa2` **T9** | `WEIGHTED_AVG` / `FIFO` / `LIFO` per item. Chosen on the item master form, changed with **Alt+V on the Stock Summary**. Lot queue lives in `internal/inventory/lots.go`, in the **POSITION's state** (never in the valuer — `ReplayAll` hands ONE valuer to every position). Migration **0078** widens the CHECK. `PUT .../stock-items/{id}/valuation-method` + `GET .../inventory-vocabulary`. 4 corpus fixtures, re-derived independently in `corpora/validate.py`. |
| `64887c4` **§3** | The Tally importer brings in items, units, godowns, batches, opening positions and **quantities**. `internal/migration/inventory.go` is the whole lane. An export with **no `STOCKITEM` masters carries no opening stock**, so the first sale of older goods **aborts the import** naming both ways forward. `corpora/migration/07-tallyprime-inventory-shapes.xml` (UTF-16LE, BOM, CRLF) + an oracle that **re-parses the XML with its own structs**. |
| `9cc1fed` **T10** | Migration **0079**: `stock_group` (LTREE, like `account_group`) + nullable `stock_item.stock_group_id`; `unit.base_unit_id` + `conversion_factor`. Group subtotals are **the whole branch** and therefore **do not sum** to closing stock. A compound quantity is **stored in the item's own unit** and restated at report time. `POST`/`GET /stock-groups`; `POST /units` takes `base_unit_symbol` + `conversion_factor` (a **decimal string**); the item body takes `stock_group_name`. Screens: group-subtotal table + Basis + base-unit column on the Stock Summary, a `stock-group` MasterKind, a served unit picker. |

### The three rules these lanes each paid for once

1. **VERIFYING THAT THE PIECES EXIST IS NOT VERIFYING THAT THEY COMPOSE** (the payroll lesson,
   inherited). T9's valuers, CHECK constraint and corpus were all green while `POST /stock-items`
   had no `valuation_method` field — nobody had FIFO. The probe that pins it now
   (`TestAnItemCreatedAsFIFOOverHTTPIsValuedAsFIFO`) **reads the master back** rather than trusting
   the 201, because "accepted and discarded" answers 201 (register row D43).
2. **AN ORACLE THAT CANNOT SEE THE DEFECT IS NOT AN ORACLE.** §3 survived a whole real-file gate
   because a **trial balance cannot see quantity**. If you touch inventory, your oracle is
   quantities or values, never "the statements balance".
3. **A POSITION IS `(item, godown, batch)` OR IT IS NOTHING.** §3's own worst bug, found by
   measurement mid-lane: an opening written to `Main Location` while the file's own movements were
   **untracked** put the opening in one position and every issue in another. Do not "normalise"
   `Main Location` to `""` anywhere — a test that does collapses two real positions into one map
   key and hides exactly this.

---

## 1. 🥇 FINISH T10 — FOUR ITEMS, IN THIS ORDER, ~HALF A SESSION

Everything below is *proof*, not product. The feature works: create a group over HTTP or from
`Alt+G > Create Stock Group`, give items to it, and the Stock Summary subtotals the branch;
create `1 BOX = 12 Nos` and an item in BOX closes at `4.0000` with `48.0000 Nos` beside it.

### 1.1 ⛔ THE HONESTY ROW — AND IT BREAKS TWO TESTS ON PURPOSE

`frontend/src/screens/FeaturesScreen.tsx:323` still reads:

```
name: "Stock groups and compound units (Box of 12)",
state: "off",
note: "Not modelled. Stock items are flat and units are simple symbols with no conversion.",
```

Both halves of that are now false. Flip it to `"on"` and write the note the way the T9 row
(`:296`) was written — what works, what the operator must know, and what is refused.

⛔ **TWO HONESTY TESTS ASSERT THAT ROW IS "Not built" AND WILL FAIL. THAT IS THE GATE WORKING,
NOT COLLATERAL DAMAGE:**

- `frontend/src/screens/VoucherScreen.itemInvoice.test.tsx:671`
- `frontend/src/screens/VoucherScreen.stockVoucher.test.tsx:960`

They exist so a row cannot flip without somebody looking. **⚠️ AFTER THIS FLIP THERE IS NO `off`
ROW LEFT IN THE INVENTORY GROUP AT ALL** (all ten are `on`), so re-point each assertion at a row
that is still genuinely absent and say why in a comment, as those tests already do for the two
re-pointings they have survived. The remaining `off` rows anywhere are: Multi-currency (`:115`),
Interest on overdue bills (`:125`), Budgets (`:130`), GSTR-9 (`:261`), SSO/OIDC (`:411`), Rule
48(4) QR (`:426`), e-mail/PDF export (`:474`), print templates (`:485`), Backup/restore (`:495`).

⚠️ **Do not delete the assertions.** "If every row said On the screen would be decoration" is that
test's own reasoning and it is right.

### 1.2 FRONTEND TESTS THAT FAIL ON THE BROKEN BUILD

The suite is green because **nothing new asserts T10 in the UI**. Write these two files in the idiom
of their siblings (`StockSummaryScreen.valuation.test.tsx` is the closest model — it drives the
**mock client end to end** rather than stubbing a response, which is what makes it able to catch a
mock that labels a feature it does not compute):

**a) `src/screens/reports/StockSummaryScreen.groups.test.tsx`** — against `createMockClient`:
create two groups (parent + child), three items (one in each group, one ungrouped), buy some of
each, render, and assert

- the subtotal table (`data-testid="stock-group-subtotals"`, rows `stock-group:<name>`, the
  ungrouped bucket is `stock-group:`) shows the **branch** total for the parent;
- the footer says the rows must not be added;
- `stock-base-qty:<item>` shows `48.0000 Nos` for an item in a compound unit and an **em dash**
  for a simple one.

⛔ **THE MOCK ALREADY COMPUTES BOTH** (`src/api/mock.ts` — `replayStock` walks the parent chain for
the branch rollup and multiplies the factor after the item fold). It is a **second implementation**
of the server's rule, which is the point: if the two disagree, one is wrong.

**b) `src/screens/masters/MasterCreateScreen.stockGroup.test.tsx`** (or new cases in the existing
file, which is where the T9 cases went) — `Alt+G > Create Stock Group` reaches the form; the parent
picker offers only groups **this company has** (there are no built-ins, so a hardcoded list would
be a list the server refuses); `master-item-stock-group` sends `stock_group_name`; and on the unit
form `master-unit-base` + `master-unit-conversion` send `base_unit_symbol` +
`conversion_factor` **as a decimal string**, with half a conversion refused before the request.

⛔ **RUN EACH NEW PROBE AGAINST THE BROKEN BUILD FIRST** (§8 rule 2, and it earned its place twice
in this lane). The two mutations that prove these tests:

```
# the group rollup counts only DIRECT items → parent prints smaller than its child
internal/reporting/stock_summary.go, rollupStockGroups: for i := len(labels)-1; i < len(labels); i++
# the conversion is applied to the STORED figure → closing_qty reads 48 where 4 was keyed
internal/reporting/stock_summary.go, after the fold: out[i].ClosingQty = out[i].ClosingBaseQty
```

Both were measured on the way in (predicted → observed) and both failure messages already name the
money consequence.

### 1.3 THE BROWSER DRIVE

`frontend/scripts/drive-valuation.mjs` (21/21) and `drive-tally-import.mjs` (10/10) are the models
— copy one. It must create a **throwaway company of its own** (never `Demo Traders`: a purchase
moves the trial balance and residue drops `verify:demo` below 34/34), then:

`Alt+G > Create Stock Group` twice (parent, then child) → `Alt+G > Create Unit` for `1 BOX = 12
Nos` → `Alt+G > Create Stock Item` in the child group **and in BOX** → post a purchase over HTTP →
open the Stock Summary and read the **branch subtotal** and the **restated quantity** off the page.

⛔ **THE THREE TRAPS THAT COST REAL TIME AND ARE ALREADY SOLVED IN THE HARNESS:**

- **`altMac(letter)`, never `press("Alt+g")`** — macOS composes Option+G into `©`, and every
  Alt binding in this product was once found dead on macOS while every browser test passed.
- **Vite MUST be on `:5173`** (WebAuthn origin matching is exact; a vite that took `:5174` signs
  in fine and then fails every write with `PASSKEY STEP-UP FAILED`).
- **Symbol-check the running `cored`** before believing a drive — `make run-cored` is `go run`, so
  a stale process is indistinguishable from a fix that does not work:
  `strings "$(ps -o command= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)" | awk '{print $1}')" | grep -c stock-groups`

### 1.4 THE RELEASE NOTE

`docs/RELEASE-NOTES.md` — the T9 and §3 notes at the top are the shape and the register. T10's note
must lead with the two things an operator will act on:

- a group subtotal is **the whole branch**, so **the group lines overlap and must not be added**;
- a quantity in a compound unit is **stored in that unit** — `4 BOX`, not `48 Nos` — and the
  restatement is a derived column, so **nothing in your books changes if a conversion factor is
  corrected later**, which is the reason it is done that way.

---

## 2. THEN: THE NEXT TRACK — `HANDOFF-NEXT-BUILD.md` §4 ONWARDS

Unchanged and still right: **T14 backup/restore/split** (⛔ reuse `internal/platform/testdb`'s
delete-plan census — it is anti-rot by construction; a second hand-written list of tenant tables is
how backup quietly drops one, and **that census caught `stock_group` in this very lane**), then the
**offline entry screen** (§5), then the long tail (§6: T8 → T11 → T13 → T12 → T16 → SSO last).

⛔ **`HANDOFF-NEXT-BUILD.md` §7 ("parse once" on the Tally import) is now MORE valuable than it
was**, and its prize has grown: `POST /import/tally-xml` tokenises the body three times, and §3 has
just added a fourth consumer of the same bytes (the inventory masters pass). The prize is still not
the memory — it is that the peeks and the handler become **structurally unable to disagree about the
file**, which is what every one of §7.2's seven doors has been.

⛔ **AND §8 IS STILL THE ONE THING ONLY A HUMAN CAN DO: GET A GST-BEARING TALLY EXPORT.** The real
file this importer was fixed against is a **grain trader — GST-free and zero-rated**, so
`classifyTaxLedger`, `inferRate` and the GSTR-1 B2B join remain exactly as tested as they were
before any real file arrived. §3 did **not** change that. Ask for a `Masters.xml` **and** a Day
Book from ONE company that charges GST, with intra-state and inter-state invoices and at least one
purchase with input tax.

---

## 3. THE MAP OF WHAT THESE THREE LANES TOUCHED

Read these before changing anything near them; each file's own header carries the reasoning.

```
backend/internal/inventory/lots.go              T9  FIFO/LIFO. The queue is in the POSITION's state.
backend/internal/inventory/valuation.go         T9  ValuerFor's three arms; `less()` is the replay order.
backend/internal/masters/stock_valuation.go     T9  the switch + its audit row (a RESTATEMENT, not an edit).
backend/internal/masters/inventory_ensure.go    §3  case-insensitive resolve-or-create; SetStockOpening (DO NOTHING).
backend/internal/masters/stock_group.go         T10 the LTREE hierarchy.
backend/internal/migration/inventory.go         §3  the whole goods lane: masters pass + per-voucher stock lines.
backend/internal/integration/tallyxml.go        §3  STOCKITEM/UNIT/GODOWN/STOCKGROUP + BATCHALLOCATIONS + SplitQtyUnit.
backend/internal/reporting/stock_summary.go     T9+T10 the item fold, the GROUP fold, the restatement.
backend/migrations/0078_valuation_methods.sql   T9
backend/migrations/0079_stock_groups_and_compound_units.sql   T10 (⛔ policy MUST be named tenant_isolation)
frontend/src/screens/reports/StockSummaryScreen.tsx          Alt+V panel, Basis, groups, base unit
frontend/src/screens/masters/MasterCreateScreen.tsx          costing method, stock group, compound unit
frontend/src/api/mock.ts                        ⛔ REPLAYS FIFO/LIFO and the group/base derivations. Keep it that way.
corpora/inventory/08..11*.json                  T9  four fixtures, two independent oracles
corpora/migration/07-tallyprime-inventory-shapes.xml         §3+T10, UTF-16LE + BOM + CRLF
```

### The four tests that will tell you fastest if you broke something

```
go test ./internal/inventory/                                    # pure, no DB, 0.3s
go test ./internal/migration/ -run TallyPrimeInventoryShapes     # the quantity oracle
go test ./internal/server/ -run 'FIFO|Valuation|StockGroup|Compound|FillsTheWarehouse'
cd frontend && pnpm vitest run src/screens/reports src/screens/masters
```

---

## 4. THE RULES THAT ARE NOT NEGOTIABLE (restated because they are what makes this repo work)

1. **Both proofs, for every fix.** An EXECUTED mutation proof whose failure was **predicted, then
   observed**, *and* a drive in a real browser. A probe that has never failed proves nothing.
2. ⛔ **Run every new probe against the BROKEN build first.** This lane did it six times and two of
   those (the mock that labels FIFO while folding an average; the purity of `Apply`) would
   otherwise have shipped green and wrong.
3. **Assert a PROPERTY, not a list of hostile inputs.** Two pinned tests were re-pointed from a
   LIST to the property this lane (`ValuerFor` rejects "the five methods with no valuer", the
   schema guard asserts "a closed enumeration whose members all have a valuer") — do that rather
   than editing a list each time the product grows.
4. ⚠️ **Vary the axis the product actually varies on.** Every real Tally export is UTF-16LE and no
   test body ever was; that is how door 7 survived three suites. Both new corpus fixtures are
   UTF-16LE with a BOM and CRLF.
5. **Do not assert a specific non-terminal status name** in a probe.
6. ⛔ **VERIFY, DO NOT INHERIT.** Including this file. Every claim in §0's table has a command
   beside it; run it.
