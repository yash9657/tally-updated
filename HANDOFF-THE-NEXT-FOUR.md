# HANDOFF — THE NEXT FOUR TRACKS

**Written:** 2026-08-25 · **Updated:** 2026-08-26 (track A spent) · **Branch:** `session/t32-and-close-7-3`
**Read time:** ~15 minutes. Everything below was MEASURED today; every claim has the command beside it.

> ## ⛔⛔ THE ONE THING THAT DECIDES HOW BIG THIS WORK IS
>
> **THREE OF THESE FOUR TRACKS ARE MISSING FRONT DOORS, NOT MISSING FEATURES.**
>
> | Track | The engine | The gap |
> |---|---|---|
> | ~~**A** Migrate from Tally~~ | ✅ **DONE 2026-08-26 — `Alt+O` and Gateway > Import.** `HANDOFF.md` §1.18 | ~~no screen~~ **closed** |
> | **B** Export to PDF | ✅ a full PDF renderer, 10 report layouts, `usePrint.ts` | Alt+E offers **csv / excel-xml only** |
> | **C** Print templates | ✅ `SaveOverrides` is written, tested, upserts | ⛔ **NO ROUTE CALLS IT** |
> | **D** Offline entry | ✅ sequencer, HLC, idempotency, conflict/freeze outcomes | **no client at all** |
>
> ⛔ **THIS IS THIS REPOSITORY'S MOST EXPENSIVE RECURRING BUG AND IT HAS NOW HAPPENED FIVE TIMES** —
> order processing (whole backend, no screen), payroll (`SetSalaryComponent`/`SetStatutoryElection`
> had no route, so every pay run answered 422), T14's census (lived in `testdb`, unreachable), and
> now C and A. `HANDOFF.md` §1.15's rule is the one all four of these exist to satisfy:
> **A ROUTE TABLE IS NOT A SURFACE.** Verifying that the pieces exist is not verifying that they compose.
>
> **Only ONE of the four (B's e-mail half) needs a decision before code. Everything else is surface.**
>
> ### ⛔ WHAT TRACK A COST, AND WHAT IT LEFT FOR B, C AND D
>
> It came in at **no Go changes**, 26 vitest cases and a 25/25 browser drive — and eleven executed
> mutations, **three of which failed in the PROBE rather than the product.** Read `HANDOFF.md` §1.18's
> *"three things the mutation proofs found"* before writing a probe in any track below. The two that
> generalise:
>
> 1. ⛔ **A REGISTRATION POINT WITH NO TEST THAT CAN SEE IT.** Deleting the screen from AppShell's
>    `COMPANY_SCOPED_SCREENS` broke **nothing** — that set is only consulted when `company === null`,
>    and every test passes an `initialCompany`. **The same hole is open for the other fourteen screens
>    in that set.** Tracks C and D both add a company-scoped screen: write the company-less case.
> 2. ⛔ **jsdom IS MISSING MORE THAN YOU THINK.** `File.text()`, `File.arrayBuffer()`, `File.stream()`
>    and `Blob.arrayBuffer()` are **all absent**. A mutation that reaches for one fails for the wrong
>    reason and 16 tests go red for nothing. Read a Blob with `FileReader`; simulate `.text()` with
>    `TextDecoder`. Track C's PDF assertions will hit this.

---

## 0. RUN-FIRST — the stack, and the numbers to beat

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
lsof -nP -iTCP:8087 -sTCP:LISTEN -t   # and 5173, 5433
```

⛔ **SYMBOL-CHECK THE RUNNING `cored` BEFORE BELIEVING ANY DRIVE.** `make run-cored` is `go run`, so a
stale process is indistinguishable from a fix that does not work. For a track that changes no Go code,
check that it is *recent*; for one that does, grep the binary for a new string:

```bash
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"
strings "$(ps -o command= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)" | awk '{print $1}')" | grep -c <new-symbol>
```

⛔ **VITE MUST BE ON `:5173`.** WebAuthn origin matching is exact; a vite that took `:5174` signs in
fine and then fails every write with `PASSKEY STEP-UP FAILED`.

**The gate baseline, RE-MEASURED 2026-08-26 after track A — these are the numbers to beat:**

| Gate | Command | Value now |
|---|---|---|
| backend build + vet | `cd backend && go build ./... && go vet ./...` | clean |
| backend tests | `cd backend && go test ./... -timeout 60m` | **exit 0** |
| typecheck | `cd frontend && pnpm exec tsc --noEmit` | clean |
| frontend tests | `cd frontend && pnpm vitest run` | **1610 pass / 95 files** |
| live demo | `cd frontend && pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT |
| corpora | `python3 corpora/validate.py` | OK (migration=6, inventory=11) |
| everything | `make verify` | exit 0 |

⚠️ **`make verify` DOES NOT RUN `tsc`.** They are different gates and each has caught a real error the
other missed.

### What landed immediately before this file

**T32 migrate from Tally** — `Alt+O`. `HANDOFF.md` **§1.18**, and the box above for what it cost.
**T14 backup / restore** — `Alt+Y` > Data. `HANDOFF.md` **§1.17**.

⛔ Read §1.17's *"three rules this lane paid for"* AND §1.18's *"three things the mutation proofs
found"* before writing a probe in any track below. Between them they cost three sessions, and §1.18's
first item is a hole that is **still open** for fourteen screens.

---

## 1. THE SHARED REGISTRATION SURFACE — read once, use in tracks A, C and D

Three of the four tracks add a **screen**. `HANDOFF.md` §7.2 is the authority; **T14's screen commit
`b5c61b7` is the worked example — read its diff, it touches exactly these files and nothing else.**

| # | File | What |
|---|---|---|
| 1 | `keystroke/types.ts` | `ScreenKind` union (⚠️ **here, not `screenRegistry.tsx`**) |
| 2 | `keystroke/types.ts` | `KeymapAction` union |
| 3 | `keystroke/keymaps/tallyprime.ts` | the global binding |
| 4 | `keystroke/keymaps/tallyprime.ts` | `actionLabel()` case (⚠️ **here, not `AppShell.tsx`**) |
| 5 | `keystroke/navMachine.ts` | `case "<action>": return pushOnce("<screen>")` |
| 6 | `keystroke/buttonBar.ts` | a bar for the screen |
| 7 | `app/AppShell.tsx` | the **company-scoped** set — omit it and `useCompanyID()` THROWS before the screen renders anything |
| 8 | `app/AppShell.tsx` | the title case (`default` is "Tally", silently wrong) |
| 9 | `app/AppShell.tsx` | the hint strip case |
| 10 | `app/defaultScreens.tsx` | the adapter + the registry entry |
| 11 | `corpora/validate.py` | **`KNOWN_SCREENS`** (~line 66) — ⛔ **outside `frontend/` entirely**, and a `ScreenKind` it does not list **fails `make verify`**. ⚠️ CO-OWNED: **ADD only, re-read immediately before editing** |

⛔ **THREE OF THESE SHIP THE KEY COMPLETELY DEAD WITH NO ERROR AND NO STATUS LINE** — the keymap
binding (#3), the navMachine case (#5), the registry entry (#10). T14 mutation-proved each
**separately**; do the same. An unbound key is not inert: F5 opens a Payment over your form, F9 a Purchase.

⚠️ **`SCREEN_ACTIONS` in `AppShell.tsx` is a DIFFERENT list** and is only for actions the *active screen*
owns (Alt+O on bank reconciliation, Alt+C on the cost masters). **Opening a screen goes through the
navMachine, not through `SCREEN_ACTIONS`.** Adding a navigation action there is wrong. But if a track
adds a key the screen itself handles, that map is a registration point and its omission is silent —
`server.go`'s own comment records Alt+O shipping dead with every other piece correct.

### 1.1 How a screen is REACHED besides its key

⛔ **`Alt+G` Go To IS NOT A DOOR.** The picker is hard-wired to `["Vouchers", ...REPORT_NAMES]`, so Cost
Centres, Bank Reconciliation, TDS Challans, the order registers and the BOM screen are all absent from
it and present on the **Gateway** (`audit-harness.mjs`'s `openFromGateway` exists because two audit
rows once named the wrong key and were logged as unreachable). `navMachine.ts` has a `*_LABEL` const
per Gateway entry — follow that pattern.

### 1.2 Client methods go in THREE files

`api/client.ts` (**declare it here first** — `rest.ts` and `mock.ts` then fail to compile until both
implement it; use that), `api/rest.ts`, `api/mock.ts`.

⛔ **THE MOCK MUST BE HONEST, NOT HELPFUL.** It is a SECOND implementation on purpose where it replays
domain logic (FIFO, group rollups), and an honest refusal everywhere else — T14's `restoreCompany`
throws *"the mock has no database to create one in"*, and `importTallyXML` returns zeroes and says it
does not parse XML. Never fake a payload a real backend has never produced.

---

## 2. ~~TRACK A — MIGRATE FROM TALLY~~ ✅ SPENT 2026-08-26

**`HANDOFF.md` §1.18 has it in full, and `HANDOFF-T32-IMPORT-SCREEN.md` is `git rm`'d** — it was
written to be retired. `Alt+O` and `Gateway of Tally > Import` both open the screen; the FeaturesScreen
row reads **On** and its *"there is NO SCREEN for it"* clause is gone.

⛔ **THE ONE THING TO CARRY FORWARD: `Blob.text()` IS A UTF-8 DECODE AND `FileReader.readAsText` IS
NOT.** The first BOM-sniffs and recovers UTF-16 correctly; the second is what `.text()` does and it
destroys a real export. Both measured in §1.18. Any future file-upload screen faces the same fork.

⛔ **AND THE HALF STILL OUTSTANDING IS A HUMAN'S: ASK FOR THE GST-REGISTERED TALLY EXPORT**
(`HANDOFF.md` §10 item 1). `classifyTaxLedger`, `inferRate` and the GSTR-1 B2B join have still never
met real GST data — **do not touch them until it arrives.**

---

## 3. TRACK B — EXPORT TO PDF, AND E-MAIL A DOCUMENT

> ## ⛔ SPLIT THIS ROW IN TWO BEFORE YOU START. THEY ARE NOT ONE FEATURE.
> `FeaturesScreen.tsx` has one row reading *"Export: e-mail a document, and export to PDF"*. **PDF
> export is nearly free and e-mail needs a product decision and a transport.** Shipping the first and
> flipping the joint row to `on` would claim e-mail works. T14 split the backup/split-company row for
> exactly this reason and `FeaturesScreen.test.tsx` has the pattern to copy.

### 3.1 What already exists — measured

| Fact | Value | Command |
|---|---|---|
| PDF renderer | `internal/printing` — a pure leaf, deterministic bytes, Rule 46 validated | archtest: its allowed set is the EMPTY map |
| voucher print | `GET .../print/vouchers/{class}/{voucherID}` | `server.go:1384` |
| report print | `GET .../print/reports/{name}` | `server.go:1387` |
| **reports that actually print** | **10 of 30** report names | the `switch name` in `handler_print_reports.go:130` |
| the other 20 | answer an **honest 501** | the `default` arm |
| client side | `printVoucher` / `printReport` → `http.getBlob` | `api/rest.ts:242` |
| the browser side | `usePrint.ts` — opens the PDF in a tab, falls back to a download when pop-ups are blocked | `screens/usePrint.ts:44` |
| Alt+E export | **`"csv" | "excel-xml"` only** | `export/sheet.ts:90` |
| PDF in the export path | **none** — `grep -rn pdf src/export/` is empty | |

**The ten that print:** `trial-balance`, `balance-sheet`, `profit-and-loss`, `day-book`,
`outstandings`, `bills-payable`, `stock-summary`, `payroll-register`, `payroll-payslip`,
`bank-reconciliation`.

### 3.2 Slice B1 — PDF as an export format (small, ship it first)

The PDF **bytes already exist**. What is missing is that `Alt+E` never offers them: `ExportFormat` is
`"csv" | "excel-xml"`, and the print path is a separate key (`Ctrl+P`) that opens a tab rather than
saving a file.

- Widen `ExportFormat` with `"pdf"`, and in `useExportAction` route that format to the **existing**
  `printReport` / `printVoucher` blob rather than to `sheet.ts`.
- ⛔ **A REPORT THAT DOES NOT PRINT MUST SAY SO, NOT OFFER PDF.** 20 of 30 answer 501. Offering PDF on
  a screen where it 501s is a menu item that fails after the operator chooses it. The screen knows its
  own report name; gate the option on it.
- ⚠️ **Reuse `usePrint.ts`'s sink and `export/download.ts`'s anchor.** Do not invent a third download
  path — T14 already had to be told this and reused the second.

**Proof:** an export test that asserts the `pdf` format calls `printReport` and **never** `sheet.ts`,
plus one asserting a 501-only report does not offer the option. ⛔ **Mutation:** offer `pdf`
unconditionally and watch the second test name the report.

### 3.3 Slice B2 — e-mail. ⛔ THIS ONE NEEDS A DECISION BEFORE CODE

**What exists, measured:** `internal/notify` is a real, durable, transactional outbox — `Enqueue` runs
**inside the caller's transaction** (roll back the invite and the notification rolls back with it),
at-least-once delivery, a lease, status transitions, and `StatusesByRef`. It is genuinely good.

**What does not exist:**

- ⛔ **NO SMTP.** The only `Sender` is `LogSender`, and its comment is deliberate: *"REAL SMTP IS
  CONFIGURATION, NOT CODE… it is the honest statement that this deployment does not send mail."*
- ⛔ **ONE Kind: `KindCompanyInvite`** (`notify.go:41`). There is no document kind.
- ⛔ **NO ATTACHMENT CONCEPT.** `Message` carries `Recipient`, `SubjectLine`, `Body`. A PDF is not
  representable.

**So "e-mail an invoice" is three separate things, and the first is not an engineering question:**

| # | Question | Note |
|---|---|---|
| **1** | **Does this product send mail at all?** Or does "e-mail a document" mean a `mailto:` handoff to the operator's own mail client with the PDF already downloaded? | The second is honest, needs no transport, no deliverability story and no bounce handling — and it is what a lot of accounting software actually does. **Decide this before writing anything.** |
| **2** | If it sends: which `Sender`, and where does the config live? | The seam is already there. This is a deployment decision, not a refactor |
| **3** | Attachments, and a `Kind` for a document | `Message` has to grow; the outbox does not |

⛔ **DO NOT BUILD A HALF-SEND.** `internal/notify`'s own rule is that *"nothing in this package calls
'delivered to nobody' a send"* — an addressless message is settled as a terminal failure carrying
`NoAddressReason`, **never** as `sent`, because returning nil was once read as success and it cost a
bug. A document e-mail that logs and reports "sent" would re-introduce exactly that.

**Recommendation:** ship **B1** and split the row. Leave the e-mail half reading `Not built` with the
decision written into the note, the way the split-company row now does.

---

## 4. TRACK C — PER-COMPANY PRINT TEMPLATES

> ## ⛔ THE WRITE ALREADY EXISTS AND NOTHING CAN REACH IT.
> `printingdb.Store.SaveOverrides` (`printing/printingdb/template.go:153`) is fully implemented — a
> real `INSERT … ON CONFLICT (company_id, doc_kind) DO UPDATE`, with `updated_at` set on both paths so
> *"when did our invoice layout last change?"* is answerable from the row. It is **tested** (six
> `INSERT INTO print_template` sites in `template_test.go`).
>
> ```
> grep -rn "SaveOverrides" backend/internal/server/     ->  EMPTY
> psql -tAc "SELECT count(*) FROM print_template"       ->  0
> ```
>
> **No route calls it. Not one row exists in the entire database.** This is `payrolldb`'s bug exactly.
> **This track is ONE ROUTE plus ONE SCREEN.** It is not "build a templating system".

### 4.1 What the table holds — measured

```
psql -tAc "SELECT column_name||':'||data_type FROM information_schema.columns
           WHERE table_name='print_template' ORDER BY ordinal_position"
```

`id · company_id · doc_kind · title · declaration · terms · signature_label ·
header_lines[] · footer_lines[] · bank_lines[] · show_bank_details · created_at · updated_at`

**`doc_kind` vocabulary** (migration 0037's `print_template_doc_kind_known` CHECK, read from the live
catalog — ⛔ **do not hardcode a TypeScript copy**, that is the drift bug this repo has had three times):

```
tax-invoice · bill-of-supply · delivery-note · receipt · payment ·
credit-note · debit-note · report
```

### 4.2 ⛔ THERE IS NO LOGO, AND THE FEATURES ROW SAYS THERE IS

```
psql -tAc "SELECT table_name||'.'||column_name FROM information_schema.columns
           WHERE table_schema='public' AND (column_name LIKE '%logo%' OR column_name LIKE '%image%')"
   -> EMPTY
```

`FeaturesScreen.tsx:492` names the row **"Per-company print templates (logo, address block, footer,
declarations)"**. Three of those four are columns. **A logo is not.** It needs a new migration, a
binary column or object store, an upload route, a size/type policy, and the PDF renderer taught to
place it.

**Do one of these two, deliberately, and say which in the commit:**

- **(a) Ship the four that exist and RENAME THE ROW** to drop "logo", with a note saying a logo needs
  image storage the product does not have. ⭐ **Recommended** — it is truthful and it is a quarter of
  the work.
- **(b) Build the logo too**, as its own slice with its own migration.

⛔ **What must NOT happen is shipping (a) and leaving the row promising a logo.** That is the failure
mode the whole FeaturesScreen exists to prevent.

### 4.3 The route

`PUT /v1/companies/{companyID}/print-templates/{docKind}` (+ a `GET` so the form can hydrate;
`LoadOverrides` already exists and **is** wired — the printer reads it).

**TWO-TOUCH RULE — the route is inert without all four:**

1. `routeAction()` in `auth_middleware.go` — add the arms;
2. `authz.KnownResources` in `authz/vocabulary.go` — the token;
3. gate: `go test ./internal/server/ -run TestEveryCompanyRouteIsMappedAndInVocabulary`;
4. archtest: `internal/server` already reaches `internal/printing/printingdb`, so **no new edge**.

⛔ **THE TOKEN. Model it as `company`, not `report`, and not a new token.** A print template is company
configuration — the same authority as the F11 feature settings, which already emit
`(Alter, "company")`. ⚠️ **NOT `report:NAME`**: migration 0011 seeds every Data Entry level a coarse
`allow display report`, so a report-shaped token is granted to every operator in every tenant the day
it ships, and this one rewrites what the company's invoices say. Migration 0022 seeds Data Entry
`allow display company` (a **Display**), so `alter company` stays default-DENIED for them — which is
the right answer and **needs no ACL migration**. Being a write, it inherits the Owner level's WebAuthn
step-up and correctly does **not** inherit the back-dated cutoff (a layout carries no date).

### 4.4 The screen

Reached from **F11 Company Features**, which is where a Tally operator looks for company configuration
and where the three existing real controls already live (TDS Deductor Details is the pattern —
`FeaturesScreen.tsx`, "the FIRST of the three REAL CONTROLS on this screen").

⚠️ **A doc kind with no override row is not empty — it prints the built-in layout.** The form must show
what will actually print, and "clear this override" must be reachable, or a company that sets a
declaration once can never go back to the default.

**Proof:** an HTTP test that PUTs an override and asserts **the printed PDF changes** — not that the
row was written. ⛔ **Mutation:** have the handler drop `declaration` and watch the print assertion
name it. A test that only reads the row back cannot see a printer that ignores the override.

---

## 5. TRACK D — THE OFFLINE ENTRY SCREEN

> ## ⛔ THIS IS THE ONLY ONE OF THE FOUR THAT IS A DESIGN PROBLEM.
> The engine is complete and there is **no client at all**:
> `grep -rln "sync/commit\|syncCommit" frontend/src/` → **empty**.
> Budget the thinking, not the typing.

### 5.1 What exists — measured

`POST /v1/companies/{companyID}/sync/commit` (`server.go`), the cloud sequencer (`internal/sync`), HLC
timestamps, per-intent idempotency, and conflict/freeze handling.

**The intent** (`syncIntentReq`, `server.go:3542`):

```
idempotency_key · client_voucher_id · op · class · provisional_no
hlc · node_id · base_version · payload
```

**The outcome, one per intent** (`server.go:3611`):

```
idempotency_key · client_voucher_id · status · final_voucher_no
version_no · reconciliation_id · detail
```

**The four terminal statuses** (`sync/types.go:95`):

| Status | Means |
|---|---|
| `COMMITTED` | applied; a CREATE minted a new voucher and a **final** number |
| `CONFLICT` | a stale-base or no-live-head ALTER/DELETE was refused, with a reconciliation row |
| `FROZEN` | the voucher is pinned by an external reference (IRN / filed GSTR-1) and cannot be superseded |
| `DUPLICATE` | this idempotency key (or this CREATE id) was already applied — the stored outcome is returned and **nothing is re-applied** |

⛔ **`syncIntentReq.Payload` IS a `voucherReq`, and `buildVoucherInput` is the SINGLE translator.**
Anything the online voucher path carries, the offline path carries for free — including the once-only
AI-bill `draft_id`. **Do not add a parallel type.** `server.go` warns about this three times and it has
caused two silent-drop defects.

### 5.2 The decision the screen cannot avoid

**A provisional number is not a voucher number.** The operator keys `provisional_no` offline and the
sequencer assigns the **final** one at commit. So the screen must answer:

1. **What does a queued voucher look like** before it has been sequenced — and how is it visibly *not*
   a posted voucher? Showing a provisional number in the same field a final number appears in is how
   somebody reads it out to a customer.
2. **What happens to a `CONFLICT`?** There is a `reconciliation_id` and a `detail`. The operator has to
   be able to see what the server has and what they keyed, and choose. That is a screen of its own.
3. **What happens to a `FROZEN`?** It cannot be superseded, ever. This is a dead end that must be
   explained, not retried.
4. **Where does the queue live?** IndexedDB / localStorage — and ⚠️ **it is unencrypted books on a
   shared machine.** Decide and write it down.

⚠️ **AND KNOW THE HONEST LIMIT:** `FeaturesScreen` currently says *"There is no offline-capable client
in this build — this application needs the server — so nothing here can be keyed on a train."* A
service worker and an offline app shell are a real piece of work **separate from** the queue screen. It
is legitimate to ship the queue + reconciliation UI first (useful the moment a commit fails or a
connection drops mid-session) and leave the row honest about the shell. **Say which you did.**

**Proof:** ⛔ **do not assert a specific NON-TERMINAL status name** in a probe (`HANDOFF.md`'s standing
rule) — the four above are terminal and safe to pin. The drive must produce a **real** `CONFLICT` by
committing a stale `base_version`, not by stubbing one.

---

## 6. THE RULES THAT ARE NOT NEGOTIABLE

1. **Both proofs, for every fix.** An EXECUTED mutation proof whose failure was **predicted, then
   observed**, *and* a drive in a real browser.
2. ⛔ **Run every new probe against the BROKEN build first.** A probe that has never failed proves nothing.
3. ⛔ **A MUTATION THAT SILENTLY DOES NOTHING LOOKS EXACTLY LIKE A TEST THAT PASSED.** T14 burned two
   attempts guessing schema names (the table is `voucher_head`, not `voucher`; its key is `voucher_id`,
   not `id`) and both "passed". **Print something from inside a mutation.**
4. ⛔ **AN ORACLE THAT CANNOT SEE THE DEFECT IS NOT AN ORACLE** (`HANDOFF.md` §1.16). Applied here:
   track A had to drive a UTF-16 fixture (it did — §1.18); track C must assert the **printed bytes**,
   not the stored row.
5. ⛔ **A REFUSAL FOR THE WRONG REASON IS A VACUOUS PROBE.** T14's truncation check passed green
   because a *different* guard answered first. Make sure the thing you are testing is the thing that
   refuses.
6. ⚠️ **macOS COMPOSES Option+<letter>.** Drive every Alt binding with the harness's `altMac("x")`,
   never `press("Alt+x")`. Every Alt binding in this product was once found dead on macOS while every
   browser test passed.
7. ⚠️ **DRIVE ON A THROWAWAY COMPANY, NEVER `Demo Traders`**, and **tear it down** —
   `frontend/scripts/drive-t14-data.mjs` is the copy-me (it creates a tenant, restores a second one,
   and drops both). Residue drops `verify:demo` below 34/34.
8. **Assert a PROPERTY, not a list.**
9. ⛔ **VERIFY, DO NOT INHERIT — including this file.** Every claim above has a command beside it. Run them.

---

## 7. WHEN EACH ONE LANDS

- Fold it into `HANDOFF.md` (**§1.18** onward; T14 took §1.17), flip its `HANDOFF.md` §1.11 row, and
  update the FeaturesScreen row **in the same change that makes it true** — that is the screen's own
  rule: a row flips when an operator gets a SCREEN, not when they got a route.
- Add a `docs/RELEASE-NOTES.md` note leading with what an operator will act on.
- ⛔ **`git rm` the track's plan file.** `HANDOFF-T32-IMPORT-SCREEN.md` and this file are both built to
  be retired. `HANDOFF.md` §11's rule is *update it in place rather than write another*, and its
  evidence is twelve superseded files that each re-derived context the last already held. **Do not grow
  this file — shrink it as tracks land.**
- ⛔ **ASK FOR THE GST-REGISTERED TALLY EXPORT** (`HANDOFF.md` §10 item 1) — track A is spent and this
  is the half still outstanding. One email; it is the commercial blocker, and the screen now turns
  receiving it from an afternoon into ten minutes.

### Still blocked on a human, not on us

- **The GST-registered Tally XML export** — the screen shipped without it; `classifyTaxLedger`,
  `inferRate` and the GSTR-1 B2B join still need it, and must not be touched until it arrives.
- **IRP / GSTN / e-Way Bill sandbox credentials** — the **Rule 48(4) QR code** row cannot be built
  without them. If they arrive, the IRN screen stops being groundwork and becomes a filing, and the
  e-way bill becomes real **with no change to either payload builder**. Re-order immediately.

### The rest of the backlog, after these four

`Multi-currency (T8)` → `Budgets (T11)` → `Interest on overdue bills (T13)` → `GSTR-9 (T12)` →
`SSO/OIDC last`. `HANDOFF.md` §5.4 states the one thing not to get wrong in each. ⚠️ **GSTR-9 is worth
deferring until real GST data has been through the importer** — it is an annual return and it reads
everything the untested tax paths produce.
