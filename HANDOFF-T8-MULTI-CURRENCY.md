# HANDOFF — T8 MULTI-CURRENCY. THE NEXT TRACK, AND IT IS A SCHEMA TRACK.

**Written:** 2026-08-27 (after `1ed6e1f`, offline entry piece one) · **Branch:** `session/t32-and-close-7-3`
**Read time: 6 minutes.** ⭐ **Every claim below was MEASURED today, and the command is beside it.**
`HANDOFF.md` §5.4 is the one-line version; this file is the scouting report.

⛔ **`git rm` THIS FILE when T8 lands.** `HANDOFF.md` §11's rule is *update in place rather than
write another*, and there are 26 `HANDOFF-*.md` files on disk. This one exists because a new TRACK
needs a plan — the precedent is `HANDOFF-OFFLINE-ENTRY.md` — and it is built to be retired.

---

## 0. ⛔⛔ WHY THIS IS THE NEXT TRACK, AND WHY IT CHANGED

The previous order put **offline entry** first, for a stated reason: *"it is the only feature in a
half-built state — the engine, the TypeScript client and the reconciliation UI all exist and nothing
can reach them."* ⭐ **That reason has been CONSUMED** (`HANDOFF.md` §1.23): a dropped connection now
queues the voucher, `Alt+Q` commits it, and the `FeaturesScreen` row honestly names the one half that
is missing. Nothing is unreachable.

What remains of offline entry is a **service worker and an app shell** — infrastructure whose hard
part is the auth/boot path, with no accounting value — and `HANDOFF-OFFLINE-ENTRY.md` itself calls it
*"a separate track and the honest one to defer"*. So T8 moves to the front: **it is the largest
genuine gap for a real business. Anyone who imports or exports is blocked today.**

⚠️ **THE ORDERING WAS RE-DECIDED DELIBERATELY, NOT INHERITED.** If you disagree, the argument for the
service worker is that it is the differentiator against Tally's LAN-only model. It is now item 2.

---

## 1. ⛔⛔ WHAT EXISTS TODAY: NOTHING. THIS IS MEASURED, NOT ESTIMATED.

**Do not start by looking for the currency code. There is none.**

```
# No table, anywhere, for a currency or a rate:
$ psql … -tAc "SELECT table_name FROM information_schema.tables WHERE table_schema='public'
               AND (table_name ILIKE '%curr%' OR table_name ILIKE '%forex%'
                 OR table_name ILIKE '%rate%' OR table_name ILIKE '%exchange%')"
  -> EMPTY

# The only currency COLUMNS in the entire database:
  company.base_currency        company.currency_symbol
  company.currency_formal_name company.decimal_places
  …and every other "%rate%" column is a unit PRICE or a tax PERCENTAGE
  (order_line.rate, stock_movement.rate, stock_item.gst_rate_pct, tds_deduction.rate_pct, …)

# The Go side has no multi-currency logic at all. Every hit is the word "conCURRENCy":
$ /usr/bin/grep -rni "currency" backend/internal --include='*.go' | grep -vi concurren
  -> identity.go (stores base_currency, defaults "INR"), server.go + handler_company_get.go
     (accept it, echo it back). NOTHING ELSE.

# No gain/loss ledger has ever existed:
$ /usr/bin/grep -rni "forex\|exchange gain\|exchange loss" backend/internal --include='*.go'
  -> EMPTY
```

And the product already says so, correctly — `FeaturesScreen.tsx:115`:

> **Multi-currency** · `off` · *"Not built yet — the company base currency is the only one. Its
> SYMBOL, formal name and decimal places are configurable per company; forex rates and a second
> currency are not."*

⭐ **So this is a schema + engine + surface track, in that order.** It is the first such track since
T9/T10, and unlike those it CANNOT be done without a migration. **Next migration number: `0080`**
(`ls backend/migrations/*.sql` → 63 files, last `0079_stock_groups_and_compound_units.sql`).

---

## 2. ⛔⛔ THE THREE FACTS THAT CONSTRAIN THE DESIGN. READ THESE BEFORE DESIGNING ANYTHING.

### 2.1 ⛔⛔ `posting.amount` MUST STAY THE BASE AMOUNT. A DEFERRED DB TRIGGER DEPENDS ON IT.

`migrations/0002_core_schema.sql:157` — `check_double_entry()`, a **deferred** constraint trigger:

```sql
SELECT COALESCE(SUM(amount) FILTER (WHERE dr_cr = 'D'), 0),
       COALESCE(SUM(amount) FILTER (WHERE dr_cr = 'C'), 0)
  FROM posting WHERE voucher_version_id = v_id;
IF debit <> credit THEN RAISE EXCEPTION 'double-entry violation …'
```

`posting` has exactly one money column:

```
$ psql … -tAc "SELECT column_name||' '||data_type FROM information_schema.columns
               WHERE table_name='posting' ORDER BY ordinal_position"
  id · company_id · voucher_version_id · ledger_id · dr_cr · amount numeric · created_at
```

⛔ **THIS IS §5.4's "three stored values, not two plus arithmetic" MADE CONCRETE.** Put the FOREIGN
amount in `amount` and you break, in one commit: every existing voucher's balance check, the
incrementally-maintained `ledger_balance` table, the Trial Balance, the Balance Sheet and the P&L.
The foreign amount and the rate go **beside** `amount`, never instead of it — a new column pair on
`posting` (or a companion table), with `amount` remaining the authoritative base figure the trigger
sums. ⚠️ **A multi-currency journal balances in BASE and need not balance in any foreign currency**,
which is the second reason base is authoritative and not derived.

### 2.2 ⛔⛔ POSTINGS ARE IMMUTABLE, SO REVALUATION IS A NEW VOUCHER — NOT AN UPDATE.

`migrations/0002_core_schema.sql:150` — `posting_no_mutate`, `BEFORE UPDATE OR DELETE ON posting`,
calling `posting_immutable()`.

Two consequences, and they are both good news:

1. **You cannot retro-fit a foreign amount onto existing rows.** Whatever you add is populated
   forward only; every voucher already in the books is base-currency and stays that way. Design the
   columns as nullable-and-absent-means-base rather than planning a backfill.
2. ⭐ **Unrealised gain/loss CANNOT be implemented by rewriting postings, so it must be a
   REVALUATION VOUCHER.** That is also the accounting-correct answer, and it is why §5.4 says
   *"realised vs unrealised gain/loss are different postings"* — the schema was going to force it
   anyway.

### 2.3 ⛔ AN EXCHANGE RATE IS NOT FIXED-4, AND EVERY MONEY VALUE IN THIS PRODUCT IS.

Money is `NUMERIC(19,4)` server-side and exact BigInt-scaled **fixed-4** arithmetic client-side
(`screens/vouchers/itemInvoice.ts:62` — *"QUANTITY IS MONEY. Every qty / rate / discount / amount is
a fixed-4…"*), and `Money` is a `string` end to end (`api/types.ts:15`) precisely so nothing rounds
in transit.

⛔ **A RATE NEEDS MORE DECIMALS THAN THAT AND THE PRODUCT HAS NO PRECEDENT FOR ONE.** 1 INR ≈
0.0120 USD; at 4 dp a rate quantises to 0.0120 and a ₹50,00,000 invoice moves by thousands. **Choose
the rate's precision explicitly, write down why, and decide whether the rate is fixed-4-with-more-
digits or a distinct scaled type** — do not let it inherit `Money` by default. ⚠️ And whichever way
it goes, `parseFixed4`/`formatFixed4` are the functions a careless reuse will reach for first.

---

## 3. ⛔⛔ THE BIGGEST RISK ON THIS TRACK IS NOT CODE. RESEARCH.md DOES NOT PIN TALLY'S BEHAVIOUR.

```
$ /usr/bin/grep -n -i "multi-currency\|forex\|exchange rate" RESEARCH.md
  1090:- **Document formatting:** … company-branded layouts, multi-currency, multi-copy.
```

**One passing mention, about print formats.** Nothing on the masters, the keystrokes, the rate types,
the gain/loss ledger, the unadjusted-forex report, or where any of it sits in the menus.

⚠️ **AND NOTE WHAT THIS FILE THEREFORE DOES NOT TELL YOU.** Everything the author of this file
"knows" about TallyPrime's multi-currency UI — that it has more than one rate type, what its
gain/loss report is called, which menu holds the currency master — is **UNCITED RECOLLECTION, not
research**, and this repository has been burned by exactly that before (`HANDOFF.md` §7.2: a screen
with no documented key gets NO key rather than an invented one). ⛔ **Treat every such statement as a
QUESTION for §5, not as a requirement.**

⛔ **THIS REPOSITORY'S DISCIPLINE IS TO CITE `RESEARCH.md` FOR EVERY KEYSTROKE AND EVERY BEHAVIOUR**
(see `HANDOFF.md` §7.2's note that a screen with no documented key gets NO key rather than an invented
one — that is why Print Templates has no Alt binding). **For T8 there is nothing to cite.**

> ### ⭐ SO THE FIRST DELIVERABLE IS A DESIGN DOCUMENT, NOT CODE — AND IT WORKED LAST TIME.
> Track D was run exactly this way: `docs/design/d-offline-entry-model.md` answered four questions in
> writing, with a command beside every claim, **before** any code. It caught two backend gaps that
> would have made the screen unbuildable. Do the same here as
> `docs/design/t8-multi-currency-model.md`, and ⛔ **ask the user for the Tally behaviour you cannot
> cite rather than inventing it** — §5 below is the list.

---

## 4. THE SIX QUESTIONS THE DESIGN DOC MUST ANSWER

| # | Question | ⛔ What makes it hard |
|---|---|---|
| **Q1** | **Where does the rate live?** On the voucher, on each posting, or in a rate table keyed by (currency, date)? And **how many rate types** does this product need — one, or several? | A voucher's rate is HISTORICAL and must never move when a later rate is entered. A rate table is needed anyway for revaluation. Probably both — and then the voucher's stored rate is authoritative for that voucher, and the table is only for revaluation and defaults. ⛔ The number of rate types is a §5 question, not a recollection |
| **Q2** | **Which amount is authoritative?** | ⛔ **ANSWERED ALREADY, BY §2.1: THE BASE AMOUNT.** Do not re-open it. What is left is where the foreign amount and rate are stored beside it |
| **Q3** | **REALISED gain/loss:** a foreign bill settled at a different rate | It arises on the SETTLEMENT voucher (a Receipt/Payment against a bill), so the difference is computed from the bill's original rate — which means `bill_allocation` needs the foreign amount and rate too, not just `posting`. Which ledger takes it, and is it created on demand or seeded? (⚠️ neither exists — §1) |
| **Q4** | **UNREALISED gain/loss:** period-end revaluation | ⛔ A NEW VOUCHER (§2.2). Who triggers it — an operator action, or a report that offers it? Is it reversed at the start of the next period (Tally's convention is that unadjusted forex is a REPORT, not a posting, until you post it)? **This is the question most likely to be got wrong by building the easy half** |
| **Q5** | **What does OUTSTANDINGS show for a foreign bill?** | It is the report an operator uses to chase money. Foreign, base, or both? A single-currency column that silently mixes them is the failure mode, and it is the same class as the provisional-number problem track D solved: two different things must never share a column |
| **Q6** | **GST on an export invoice** | Zero-rated, foreign currency, and GSTR-1 needs **INR**. Which rate? ⛔ **CITE THE RULE** (Rule 34 CGST: the rate notified by CBIC for the date of the invoice — confirm before relying on this sentence). ⚠️ **AND SEE §6: DO NOT TOUCH THE TAX CLASSIFIERS YET** |

---

## 5. ⛔ WHAT TO ASK THE USER BEFORE BUILDING — THEY ARE THE DOMAIN SOURCE

1. **How many exchange rates does a currency need in this product — one, or several by purpose?**
   ⛔ Ask; do not assume a number from memory of Tally's screens (§3).
2. **Is a forex-enabled company allowed to have MULTIPLE foreign currencies, or one?** It changes the
   masters and every report column.
3. **Does the operator expect a "Forex Gain/Loss" ledger created for them, or do they choose one?**
4. **Is the unadjusted-forex REPORT enough for v1, with posting the revaluation deferred?** ⭐ This is
   the highest-value question: it may cut the track roughly in half while remaining honest, exactly
   as B1's e-mail half was split off and the offline row was split.

---

## 6. ⛔ THE TWO THINGS ON THIS TRACK THAT ARE ALREADY BLOCKED

1. ⛔⛔ **DO NOT TOUCH `classifyTaxLedger`, `inferRate` OR THE GSTR-1 B2B JOIN.** They have **never
   met real GST data** — the only real file the importer was fixed against is a grain trader, GST-free
   and zero-rated — and speculative changes destroy the evidence the awaited file would give.
   `HANDOFF.md` §10 item 1 is still the commercial blocker. **Q6 therefore stops at the design
   document until that file arrives.**
2. ⚠️ **A DATA-FIDELITY HOLE THAT EXISTS TODAY, FOUND WHILE SCOUTING THIS TRACK:**

   ```
   $ /usr/bin/grep -rni "currency" backend/internal/migration/*.go | grep -vi concurren
     -> EMPTY
   ```

   **The TallyPrime XML importer ignores currency entirely.** A real Tally file containing
   foreign-currency vouchers imports them as if the amounts were rupees — silently, with no
   disclosure. ⛔ **It is not in the defect register** (that register is row-by-row over
   keyboard-reachable features, and this is a fidelity gap nobody has tried to perform). ⭐ **The
   cheap honest fix, worth doing EVEN IF T8 is deferred: make the importer DISCLOSE a foreign-currency
   voucher rather than absorb it** — `TallyImportScreen` already has a disclosure surface for exactly
   this shape of finding.

---

## 7. WHERE THE SURFACE GOES — THE REGISTRATION FACTS, MEASURED

- **A "Currency" master is a NEW `MasterKind`.** Today's set is exactly
  `ledger · group · voucher-type · unit · stock-item · stock-group`
  (`keystroke/types.ts:463`). ⭐ **`MasterCreateScreen`'s shape is "a name, one picker, some
  details", which fits a currency (code, symbol, formal name, decimals) exactly** — so this is a
  MasterKind and NOT a screen of its own. `types.ts:473` states that test in as many words, and
  contrasts it with cost centres, which needed their own screen because they cannot exist without an
  axis.
- **A new `ScreenKind` needs 15 registration points and `tsc` enforces NONE of them.**
  `HANDOFF.md` §7.2 is the list. Proved again this session: adding `"offline-outbox"` to the union
  compiled cleanly with every single registration missing.
- **F11 Company Features** is where a per-company "enable multi-currency" belongs, and
  `FeaturesScreen`'s doctrine is the constraint: ⛔ *"a toggle that flips a flag with nothing behind
  it is worse than no toggle at all."* Wire the enforcement first, the switch second.
- **Reports that will move:** Trial Balance, Balance Sheet, P&L (via `ledger_balance`, which is
  maintained incrementally — `ledger.go:2336`, `INSERT … ON CONFLICT … credit = credit + EXCLUDED`),
  Outstandings (Q5), and the printed invoice. ⚠️ **A valuation-style change MOVES THE SHEET, so
  re-pin `verify:demo` deliberately** — that is the lesson §5.4 records against T9.

---

## 8. RUN FIRST — ten minutes, and skipping it has burned sessions

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
git log --oneline -3 && git status --porcelain
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # NEWER than the last backend commit?
```

⛔ **CHECK THE AGE OF `cored`.** `make run-cored` is `go run`, so a stale process is
indistinguishable from a fix that does not work. **Found stale four times, including at the start of
the session that wrote this file.** ⚠️ Run it **FROM THE REPO ROOT**.

⛔ **VITE MUST BE ON `:5173`** — WebAuthn origin matching is exact.

⛔⛔ **NEVER RUN `make verify` CONCURRENTLY WITH A BROWSER DRIVE.** They share the dev database. Done
once in the previous session, it produced two failures that were pure artefacts: the residue check
reported **+4 companies** and `internal/sync` hit its **600s timeout** under the contention. Both
looked like real regressions and neither was.

**The gate baseline to beat** (measured 2026-08-27 at `1ed6e1f`):

| Gate | Command | Value |
|---|---|---|
| backend | `cd backend && go build ./... && go vet ./... && go test ./... -timeout 60m` | clean · **exit 0**, 36 pkgs |
| typecheck | `cd frontend && pnpm exec tsc --noEmit` | clean |
| frontend tests | `cd frontend && pnpm vitest run` | **1784 pass / 109 files** |
| live demo | `cd frontend && pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT |
| corpora | `python3 corpora/validate.py` | OK (keystroke=33, migration=6, inventory=11) |
| everything | `make verify` | exit 0 |
| drives | `d-offline-queue · d-reconciliation · t32 · b1 · c · b2` | **27/27 · 29/29 · 25/25 · 15/15 · 33/33 · 20/20** |

⚠️ **`make verify` DOES NOT RUN `tsc`.** Each gate has caught a real error the other missed —
including, last session, a required field added to a store snapshot that vitest ignored and `tsc`
refused.

---

## 9. THE RULES THAT ARE NOT NEGOTIABLE

1. **Both proofs, for every change.** A mutation proof whose failure was **predicted, then observed**,
   *and* a drive in a real browser. ⛔ **A mutation proof proves the TEST; only the browser proves the
   FIX** — last session the browser found the only real defect, and every unit test was green.
2. ⛔ **Run every new probe against the BROKEN build first.** Last session: 20 mutations, 20
   predictions matched — after **one MISMATCH that was the MUTATION's fault**. It set
   `err.status < 500` on an `instanceof ApiError` guard, which dropped the value into a different
   branch that behaved the same way: the code changed, the tests stayed green, and it proved nothing.
   **A vacuous mutation is indistinguishable from a passing test.**
3. ⛔ **AN ORACLE THAT CANNOT SEE THE DEFECT IS NOT AN ORACLE.** For T8 the trap is obvious and
   worth naming now: a forex test whose only assertion is that the voucher POSTED proves nothing —
   **assert the BASE amount, the FOREIGN amount and the RATE separately**, because two of the three
   can be wrong while the voucher balances perfectly.
4. ⛔⛔ **A FIXED SLEEP IN FRONT OF AN ASSERTION IS A FLAKE, AND IT LIES IN THE DANGEROUS DIRECTION.**
   Two probes written that way reported false DATA-LOSS claims last session. Poll.
5. ⚠️ **DRIVE ON A THROWAWAY COMPANY, NEVER `Demo Traders`**, tear it down with the catalog-derived
   sweep (`drive-d-offline-queue.mjs` is the copy-me), and ⛔ **wrap the whole drive in one `try` so a
   throw still reaches the teardown** — a drive that died after 19 green probes leaked a tenant.
   ⛔ **Never `DELETE FROM company` alone**: it strands ACL rows and `/v1/me` then answers 500 for
   **every** session.
6. ⚠️ **macOS COMPOSES Option+<letter>.** Any new Alt binding must be driven with the harness's
   `altMac("x")` and added to `normalize.test.ts`'s `MAC_COMPOSED` table. **Free letters after Alt+Q
   went: B, F, H, J, K, N, T, U.**
7. ⛔ **A GATEWAY LABEL THAT IS A SUBSTRING OF A SHIPPED ONE IS A SILENT COLLISION.** Check any new
   label against `GatewayScreen.tsx`'s list **AND** `REPORT_NAMES` before using it.
8. **Assert a PROPERTY, not a list.**
9. ⛔ **USE `/usr/bin/grep` WHENEVER AN ABSENCE IS LOAD-BEARING.** The shell's `grep` is a `ugrep`
   shim that silently skips files it judges binary; one NUL byte once hid 8,672 lines. Every
   `-> EMPTY` in this file was produced with `/usr/bin/grep`.
10. ⛔ **VERIFY, DO NOT INHERIT — including this file.** Every claim has a command beside it. Run
    them; they took ten minutes to produce and they are the reason this file is short.

---

## 10. THE BACKLOG AFTER THIS

`Offline entry piece two — the service worker` (`HANDOFF-OFFLINE-ENTRY.md`) → `T11 budgets` →
`T13 interest on overdue bills` → `T12 GSTR-9` (⚠️ **defer until real GST data has been through the
importer** — it is an annual return that reads everything the untested tax paths produce) →
`SSO/OIDC last`. ⚠️ **And §4.3's data-loss audit is still the oldest unfinished item in
`HANDOFF.md`**: four tables were cleared on the WRONG question and have never been re-asked the right
one. It is the cheapest data-loss work left, and it is a good warm-up.
