# HANDOFF — THE DEFECT BACKLOG IS EMPTY. THIS IS A **BUILD** PLAN, NOT A FIX PLAN.

**Written:** 2026-08-25 · **Branch:** `session/t32-and-close-7-3` (4 commits, tree clean)

> ## ⚠️ WHY THIS FILE EXISTS WHEN `HANDOFF.md` §11 SAYS NOT TO WRITE ONE
>
> §11's rule is *"update `HANDOFF.md` in place rather than write another"*, and its evidence is
> twelve superseded files that each re-derived context the last already held. This file was asked
> for explicitly, so it is built to be **cheap to retire**, exactly as the last one was (and that
> one WAS retired, the day its first item landed):
>
> - it holds **no analysis that is not already at a code site or in `HANDOFF-PARITY-AUDIT.md`**;
> - it holds only the **ORDER**, the **run-first steps**, and the **traps**;
> - ⛔ **fold each item into `HANDOFF.md` §1.11 and `git rm` this file as it lands. Do not grow it.**

---

## 0. THE HEADLINE — READ THIS FIRST, IT CHANGES WHAT YOU DO

> ### ✅ 2026-08-25 (LATER) — §1 PAYROLL SCREENS HAS LANDED. THE NEXT TRACK IS §2 (T9 + T10 + §3).
>
> ⛔ **AND IT CORRECTED THIS FILE'S OWN §0 IN THE ONE DIRECTION §0 WARNS ABOUT.** The table below
> says there are no known defects — checked by parsing, and true of the DEFECT REGISTER. It was not
> true of the product: `payrolldb.SetSalaryComponent` and `SetStatutoryElection` had no route, so
> the entire payroll module could not pay anybody over HTTP, and no register anywhere had a row for
> it because nobody had tried to perform the task. See §1 and `HANDOFF.md` §1.15.
>
> **The rule that adds to "VERIFY, DO NOT INHERIT": verifying that the pieces EXIST is not verifying
> that they COMPOSE.** "Eight routes are mounted" is not a measurement of anything an operator can
> do. Perform the whole task through the public surface, or it has not been checked.

**There are no known defects.** Re-derived by parsing on 2026-08-25, not by reading a heading:

| Register | State |
|---|---|
| §7.1 defect register | **97 registered · 97 closed · 0 open · 0 partial** |
| §7.2 back-dated cutoff | **7 doors, all 7 closed** (door 7 found and closed 2026-08-25) |
| §7.3 | **A ✅ B ✅ C ✅ D ✅** — all four closed |
| §7.4 "three defects deliberately NOT fixed" | ⛔ **all three were STALE. All three are fixed.** |

⛔ **THE ONLY PERMANENTLY UNSHIPPABLE THING IS T18 (rule 48(4) QR).** Its payload is signed with the
Income Tax Department's private key, which we do not have and cannot have. **Reject any session that
reports it done.** This has come back three times.

> ### ⛔⛔ THE MOST IMPORTANT THING THIS FILE HAS TO TELL YOU
>
> **On 2026-08-25 FOUR separate "still open / known broken" claims in these docs were checked against
> the code and THREE OF THEM WERE FALSE**, plus one performance figure that was wrong by ~10×:
>
> | Claim | Reality |
> |---|---|
> | "Row 20 — a company can never be given a stock item from the keyboard" | Fixed and driven 4 days earlier |
> | "closing stock replays in lexical `voucher_no` order" | The named line does not exist; fixed |
> | "`allow_negative_stock` can be written by NOTHING" | Route + handler + UI panel all exist |
> | "the XML parse tree is several times the text, >1 GiB heap" | Tree is **0.1×** the document; measured |
>
> And one claim that said something was FIXED was also false, in the dangerous direction: T32 recorded
> the back-dated cutoff's "seventh door" as closed. It was not — the fix sat downstream of a gate that
> still could not read the bytes, and a real Tally file walked straight through it into a closed period.
>
> **⛔ THE RULE, AND IT IS THE ONLY ONE THAT MATTERS IN THIS REPO: VERIFY, DO NOT INHERIT.** Before you
> act on any sentence in any handoff — including this one — run the grep or the test that proves it.
> Every one of the four above took under two minutes to disprove.

---

## 0.1 RUN-FIRST — the stack, and the two traps that cost real time

```
make dev-db                       # postgres :5433 (container tally_pg)
make run-cored                    # :8087
make run-insightd                 # :8088  (needs insightd/.venv + psycopg)
cd frontend && VITE_LIVE=1 pnpm dev --port 5173
```

⛔⛔ **VITE MUST BE ON `:5173`.** `server.go` reads `AUTH_WEBAUTHN_ORIGIN` defaulting to
`http://localhost:5173` and WebAuthn origin matching is EXACT — the port is part of it. A vite that
quietly took `:5174` signs in fine and then fails **every write** with `PASSKEY STEP-UP FAILED`, which
reads exactly like a step-up regression and is not one. Free the port
(`lsof -nP -iTCP:5173 -sTCP:LISTEN`) or start cored with `AUTH_WEBAUTHN_ORIGIN=http://localhost:5174`.

⚠️ **Use `127.0.0.1`, not `localhost`, in probes.** `curl http://localhost:8087/healthz` HANGS on this
machine while `http://127.0.0.1:8087/healthz` answers 200 (cored binds `*:8087` v6, insightd binds
`127.0.0.1:8088` v4). This has been mis-diagnosed as a dead service twice.

⛔ **SYMBOL-CHECK THE cored BINARY BEFORE BELIEVING ANY DRIVE.** `make run-cored` is `go run`, so a
stale process is indistinguishable from a fix that does not work:

```
strings "$(ps -o command= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)" | awk '{print $1}')" \
  | grep -c <a-symbol-your-change-introduced>      # 0 means you are driving yesterday's server
```

### The gates, as measured 2026-08-25

```
frontend  pnpm exec tsc --noEmit                                  clean
frontend  pnpm vitest run                                         1487 / 87 files
frontend  pnpm verify:demo "$(cat frontend/.demo-company-id)"     34/34
backend   go build ./...  ·  go vet ./...                         clean
backend   go test ./... -timeout 60m                              exit 0
```

⚠️ `go test ./...` needs postgres and takes ~5 minutes. Budget for it; do not skip it and inherit the
last session's claim — that is exactly how door 7 survived.

---

## 1. ✅ **DONE 2026-08-25 — PAYROLL SCREENS.** Folded into `HANDOFF.md` §1.15; read that, not this.

> ## ⛔⛔ AND READ WHY THIS SECTION'S OWN PREMISE WAS WRONG — IT IS THE MOST USEFUL THING IT PRODUCED
>
> Everything below that was CHECKED was true: eight routes mounted, all PDP-mapped, `internal/payroll`
> complete with tests, no payroll screen anywhere. **And "pure frontend" was still false.**
>
> `payrolldb.SetSalaryComponent` and `SetStatutoryElection` — the two writers a pay run cannot
> proceed without — had **NO ROUTE AT ALL**, so an employee created over HTTP could never be paid:
> `POST /vouchers/payroll` answered 422 *"no statutory election is in force"* for every caller.
> Every payroll test in `internal/server` lays both children down by calling the Go SERVICE, and
> `handler_payroll_test.go`'s fixture says so in a comment — so the suite bypassed the exact seam
> that was missing, and a green run said nothing about whether the module worked.
>
> ⛔ **THE RULE THIS ADDS TO §0's "VERIFY, DO NOT INHERIT": VERIFYING THAT THE PIECES EXIST IS NOT
> VERIFYING THAT THEY COMPOSE.** Counting mounted routes measures nothing. Perform the whole task
> through the public surface, or you have not checked it. The probe that now pins payroll
> (`TestAnEmployeeCreatedOverHTTPCanBePaidOverHTTP`) makes no `s.payroll.*` call at all, and it was
> run against the broken build first — predicted 422, observed 422.
>
> **Shipped:** 4 routes + a vocabulary route (`handler_payroll_structure.go`, mapped to the EXISTING
> `employee` token so migration 0035 already covers them); 5 screens; Gateway entries; Ctrl+F4 bound
> at last. Two further defects that only the BROWSER found — the register never published its wage
> month to the frame (so the payslip drill arrived empty), and Form 138 pre-checked every deductor
> field but not the challan. Driven end to end: **34/34**, `frontend/scripts/drive-payroll.mjs`.
>
> ⚠️ **NOT built, and F11 says so:** per-employee attendance/declarations on the pay run (an empty
> `inputs` is sent DELIBERATELY — a non-empty one is also the SELECTION), and altering or reversing a
> posted run.

<details>
<summary>The original plan, kept only because its reasoning is what the correction above is about.</summary>

**Why first:** it is the biggest module in the product that a user cannot see at all, and it is
**pure frontend**. Nothing needs designing, refactoring or deciding — the engine, the statutory rates,
the returns and the routes are built and tested.

### VERIFIED 2026-08-25, not inherited

**Eight routes are already mounted** (`internal/server/server.go:1184-1191`):

```
POST /employees            GET /employees
POST /vouchers/payroll     GET /payroll-register
GET  /payroll-payslip      GET /payroll-form-138
GET  /payroll-pf-ecr       GET /payroll-esi-return
```

`internal/payroll/` holds the engine (`payrun.go`, `wages.go`, `pf.go`, `esi.go`, `incometax.go`,
`rates.go`, `rounding.go`), each with tests, plus `payrolldb` and `payrollfile`.

**And `frontend/src/screens/` contains NO payroll screen.** The only frontend mentions of "payroll"
are a keymap entry, a print target and a voucher-class label — i.e. the shell already knows the word
and there is nothing behind it.

### What to build

Employees list + create · pay run (F-key, wage month) · payslip · the register · the four statutory
files. Follow `TDSChallansScreen.tsx` and `BankReconciliationScreen.tsx` — they are the closest
existing shape (a list, a create frame, a report, a file download).

### ⛔ Traps

- **The pay run is under the back-dated cutoff, via `wage_month`, not a `voucher_date`.** A pay run for
  a closed month is a 403 and the screen must show it as a period lock, not a generic error. See
  `peekPayrollVoucherDate` and `auth_payroll_cutoff_test.go` — that test is the contract.
- **Print/download routes answer `application/pdf` or a file, not JSON.** Use the `getBlob` path in
  `rest.ts` (as `printVoucher`/`printReport` do). Sending them through the JSON path corrupts the
  document into a lossy string.
- ⚠️ **Budget 2 sessions.** This is §1.11's own estimate and the module is large.

**Definition of done:** an employee created from the keyboard, a pay run posted, a payslip and the
register read back **through the product's own screens**, and each of the four statutory files
downloaded. Plus a browser drive, per §8.

⚠️ *(There are THREE statutory files, not four — the ECR, the ESI return and Form 138. The fourth
name in `authz.KnownReportNames` is the PAYSLIP, which is a report and not a file. All three
downloaded, and the payslip renders and prints.)*

</details>

---

## 2. 🥇 T9 FIFO/LIFO → then T10 STOCK GROUPS + COMPOUND UNITS — **START HERE NOW**

⛔ **SERIALISE THESE TWO** — both touch the movement primitive.

### ⚠️ THE BLOCKER ON T9 IS GONE, AND THE OLD DOCS SAY OTHERWISE

Every previous handoff says T9 must be serialised with "the closing-stock lexical-order fix" because
that fix moves the Balance Sheet. **That fix has landed.** `inventory/valuation.go`'s `less()` orders
on `(voucher_date, VOUCHER_ID, version_created_at, line_no, voucher_version_id)` — the lexical
`voucher_no` comparison the docs name **does not exist any more.** Verify in ten seconds:

```
grep -rn "a.VoucherNo < b.VoucherNo" backend/internal/inventory/     # returns nothing
```

**So T9 is a new arm in `ValuerFor` plus fixtures — not a schema change and not a coupled fix.**
`ValuerFor` currently returns only `WeightedAverage{}` and errors on anything else; the replay key it
feeds is already correct and already re-pinned.

⚠️ **A valuation change still MOVES THE BALANCE SHEET**, so `verify:demo` must be re-pinned
deliberately and the change explained in `docs/RELEASE-NOTES.md`. That part of the old instruction
stands.

---

## 3. INVENTORY MASTERS AND QUANTITIES ON IMPORT — **do it WITH T9, in the same lane**

### VERIFIED 2026-08-25

`integration.TallyInventoryEntry` exists and is parsed (`tallyxml.go:344-351`, both
`ALLINVENTORYENTRIES.LIST` and `INVENTORYENTRIES.LIST`). **`grep -rn "TallyInventoryEntry"
backend/internal/` shows it is referenced NOWHERE in `internal/migration`.** So the importer reads
every stock line off the wire and throws it away. Today an import brings in the **accounting value and
nothing else** — no item, no quantity, no batch, no godown.

The real export the importer was fixed against carried **no `STOCKITEM`, `UNIT`, `GODOWN`,
`VOUCHERTYPE` or `COSTCENTRE` masters at all**, while its vouchers reference **227 stock lines with
batches and godowns**. So the masters have to be **inferred from the vouchers**, not read from a
masters file.

⛔⛔ **AND IT IS INVISIBLE IN A TRIAL BALANCE, WHICH IS WHY IT SURVIVED T32.** The rupee value is
right, so the statement balances; a trader whose closing stock sits on the Balance Sheet has a Stock
Summary with nothing in it. **DO NOT USE A BALANCED TRIAL BALANCE AS EVIDENCE HERE — the oracle has to
be QUANTITIES.**

✅ **Not blocked by masters-from-the-keyboard.** Row 20 said stock items and units could never be
created from the keyboard; that was stale and is fixed (L13, driven — a **Kg** item created and read
back as `Kg`). So creating them from an import is completing a path, not half a feature.

---

## 4. T14 BACKUP / RESTORE / SPLIT COMPANY

Table stakes: Tally users expect a company to be a file they can carry.

⛔ **Must be tenant-exact.** **Reuse `internal/platform/testdb`'s delete-plan census** — it already
enumerates every tenant table and is anti-rot by construction, so it cannot silently miss a table a
later migration added. Writing a second list of "all the tenant tables" is how backup quietly drops one.

---

## 5. OFFLINE ENTRY SCREEN

The differentiator against Tally's LAN-only model, and the engine is built (`internal/sync`,
`POST /sync/commit`, HLC timestamps, the cloud sequencer). It is **a screen plus a decision**: how does
a browser present queued intents that have not been sequenced yet?

⚠️ `syncIntentReq.Payload` IS a `voucherReq` and `buildVoucherInput` is the single translator, so
anything the online voucher path carries the offline path carries for free — **including the
once-only AI-bill `draft_id`.** Do not add a parallel type; that has been warned about three times in
`server.go` and has caused two silent-drop defects.

---

## 6. THE LONG TAIL, IN ORDER

T8 multi-currency → T11 budgets → T13 interest → T12 GSTR-9 → T16 print templates → **SSO/OIDC last**.
`HANDOFF.md` §5.4 states the one thing not to get wrong in each.

---

## 7. SMALL, PARALLEL, NOT A TRACK

**"Parse once" on the Tally import (~half a session).** Today `POST /import/tally-xml` tokenises the
body **three times** — `drainBody` buffers it, then `peekTallyXMLVoucherDate` parses it, then
`peekTallyXMLSupersededGUIDs` parses it again, then the handler parses it a third time. Parse ONCE
from `r.Body` in the auth middleware (**after** the membership check at `auth_middleware.go:251`, so it
is not an unauthenticated sink), memoise the envelope on the request context, and let both peeks and
the handler read that one envelope.

⛔ **The prize is NOT the memory.** It is that the peeks and the handler become **structurally unable
to disagree about the file** — which is what every one of §7.2's seven doors has been.

⚠️ **The old instruction here ("stream it, never hold the document, then drop `maxXMLBody`") is WRONG
and is corrected in `server.go`'s `maxXMLBody` comment with the measurement.** Streaming removes the
smallest of the three costs and would make us tokenise more often. Do not re-derive the "gigabyte"
claim from any older doc.

---

## 8. 🔴 THE ONE THING ONLY A HUMAN CAN DO — **ASK ON DAY ONE**

**Get a GST-BEARING Tally export.** The real file the importer was fixed against is a **grain trader —
GST-free and zero-rated. Not one of its 121 vouchers carries a CGST/SGST/IGST line.** So
`classifyTaxLedger`, `inferRate` and the GSTR-1 B2B join are **exactly as tested as they were before a
real file ever arrived**: against our own hand-authored corpora only.

⚠️ **THIS IS A DIFFERENT ASK FROM THE ONE ALREADY ANSWERED. Do not let a session report T32 done on
the strength of the grain file.**

**Ask for, precisely:** a `Masters.xml` **and** a Day Book / `Transactions.xml` from ONE company that
**charges GST** — ideally with intra-state (CGST+SGST) *and* inter-state (IGST) invoices, and at least
one purchase with input tax. One month of real volume is enough.

⛔ **Tell them it is a real ledger and say what happens to it.** The first file is deliberately NOT in
the repo (3,706 named counter-parties with GSTINs and addresses). Same rule: the real-file gate is
opt-in by path and an anonymised fixture carries the shapes into CI.

```
CORED_TALLY_MASTERS_XML=…/Masters.xml CORED_TALLY_VOUCHERS_XML=…/Transactions.xml \
  go test ./internal/migration/ -run RealTallyExport -v
```

⛔ **Expect defects, and expect silent ones.** The first real file scored **seven, three of them
silent**. Keep the oracle's property that it **shares no code with the importer**: re-derive
CGST/SGST/IGST from the XML independently. Never compare the importer to itself.

---

## 9. THE RULES THAT ARE NOT NEGOTIABLE (§8, restated because they are what makes this repo work)

1. **Both proofs, for every fix.** An EXECUTED mutation proof whose failure was **predicted, then
   observed**, *and* a drive in a real browser. A probe that has never failed proves nothing.
2. ⛔ **Run every new probe against the BROKEN build first.** Twice now an arm has been green for the
   wrong reason and only measurement found it — most recently `the refusal RECONCILED insightd`, which
   passed on the broken build because the second browser simply succeeded.
3. **Assert a PROPERTY, not a list of hostile inputs.** Rounds 1–4 of the cutoff each asserted a list
   and the next bypass was always off the list.
4. ⚠️ **Vary the axis the product actually varies on.** Door 7 survived three thorough test suites
   because every test body in all three was UTF-8 and no real Tally file is.
5. **Do not assert a specific non-terminal status name** in a probe — it fails on a fixture detail and
   says nothing about the defect.
