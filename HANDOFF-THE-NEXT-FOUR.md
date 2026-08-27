# HANDOFF — B2, TRACK C, TRACK D (two of the four are spent)

**Written:** 2026-08-25 · **Updated:** 2026-08-26 — **tracks A and B1 landed; both open decisions ANSWERED**
**Branch:** `session/t32-and-close-7-3` · **Tree:** clean at `13e0ba8`
**Read time:** ~15 minutes. Everything below was MEASURED; every claim has the command beside it.

> ## ✅✅ THE TWO DECISIONS ARE MADE. THEY WERE THE ONLY THINGS BLOCKING CODE.
>
> The user answered both on 2026-08-26. **Do not re-litigate either.**
>
> | Decision | Answer | Consequence |
> |---|---|---|
> | **B2 — does this product send mail?** | ⭐ **NO — `mailto:` HANDOFF.** Download the PDF, then open the operator's own mail client with a pre-addressed, pre-subjected draft. They attach the file and send. | **No SMTP, no `Sender`, no attachment concept, no bounce story, no migration.** `internal/notify` is NOT touched. ⛔ And the honest limit must be **on the screen**: a web page CANNOT attach a file to a `mailto:` draft, so the operator does that themselves and the product says so. §3 |
> | **C — the logo** | ⭐ **SHIP THE FOUR THAT EXIST, RENAME THE ROW to drop "logo".** | No image storage, no upload route, no renderer change. The FeaturesScreen row stops promising a logo and says why. §4.2 |
>
> ## ⛔ WHAT IS LEFT, IN ORDER
>
> | Track | Shape | Blocked on |
> |---|---|---|
> | **B2** e-mail a document | `mailto:` handoff. Small. Decision made | nothing |
> | **C** per-company print templates | ⛔ **ONE ROUTE + ONE SCREEN.** `SaveOverrides` is written, tested, and **NOTHING CALLS IT** | nothing |
> | **D** offline entry screen | The only one that is a **design problem**. Engine complete, no client at all | nothing — but budget the thinking |

---

## 0. RUN-FIRST — the stack, and the numbers to beat

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
lsof -nP -iTCP:8087 -sTCP:LISTEN -t   # and 5173, 5433
```

⛔ **CHECK THE AGE OF THE RUNNING `cored` BEFORE BELIEVING ANY DRIVE.** `make run-cored` is `go run`,
so a stale process is indistinguishable from a fix that does not work. **This session found one:
`cored` had been up since before the previous session's own backend commit.** Restart it if in doubt —
it costs one second.

```bash
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"      # is it NEWER than the last backend commit?
kill <pid> && (nohup make run-cored > /tmp/cored.log 2>&1 &)    # comes up in ~1s
# For a track that DOES change Go code, grep the binary for a new string:
strings "$(ps -o command= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)" | awk '{print $1}')" | /usr/bin/grep -c <new-symbol>
```

⛔ **VITE MUST BE ON `:5173`.** WebAuthn origin matching is exact; a vite that took `:5174` signs in
fine and then fails every write with `PASSKEY STEP-UP FAILED`.

**The gate baseline, RE-MEASURED 2026-08-26 at `13e0ba8` — these are the numbers to beat:**

| Gate | Command | Value now |
|---|---|---|
| backend build + vet | `cd backend && go build ./... && go vet ./...` | clean |
| backend tests | `cd backend && go test ./... -timeout 60m` | **exit 0** |
| typecheck | `cd frontend && pnpm exec tsc --noEmit` | clean |
| frontend tests | `cd frontend && pnpm vitest run` | **1628 pass / 96 files** |
| live demo | `cd frontend && pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT |
| corpora | `python3 corpora/validate.py` | OK (migration=6, inventory=11) |
| everything | `make verify` | exit 0 |
| T32 drive | `node frontend/scripts/drive-t32-import-screen.mjs` | **25/25**, tenant torn down |
| B1 drive | `node frontend/scripts/drive-b1-pdf-export.mjs` | **15/15**, tenant torn down |

⚠️ **`make verify` DOES NOT RUN `tsc`.** Different gates, and each has caught a real error the other
missed — **including this session**: `TallyImportScreen.test.tsx` was green in vitest and had **8 real
`tsc` errors**.

---

## ⛔ 0.1 FIVE HAZARDS THIS SESSION PAID FOR. READ THIS SECTION OR RE-PAY THEM.

### 1. ⛔⛔ THE SHELL'S `grep` IS A `ugrep` SHIM THAT SILENTLY SKIPS FILES — AND IT LIED

`grep` in this environment is a shell **function** wrapping `ugrep -G --ignore-files --hidden -I`
(see the snapshot in `~/.claude-premium/shell-snapshots/`). `-I` skips any file it judges **binary**,
and a single NUL byte is enough.

`frontend/src/api/mock.ts` contained a **raw NUL** as a map-key separator, so **the whole 8,672-line
mock was invisible to every search**: `grep -n importTallyXML src/api/mock.ts` returned NOTHING while
the method sat at line 7309, fully implemented. Fixed in `05c9775` (the separator is now the escape
`\u0000`, byte-identical at runtime).

> ⛔ **THIS REPOSITORY'S HANDOFFS USE `grep … -> EMPTY` AS EVIDENCE THAT SOMETHING IS UNWIRED.**
> Against a file the shim skips, that evidence is worthless in the dangerous direction. **Use
> `/usr/bin/grep` whenever an absence is load-bearing.** Every "EMPTY" claim in §4 below was
> re-verified with `/usr/bin/grep` and `psql` for exactly this reason.

### 2. ⛔ A REGISTRATION POINT WITH NO TEST THAT CAN SEE IT — STILL OPEN FOR 14 SCREENS

Deleting `"tally-import"` from `AppShell.tsx`'s `COMPANY_SCOPED_SCREENS` broke **nothing**: 25 of 25
tests stayed green. That set is consulted **only when `company === null`** (`needsCompany &&
!companyReady` picks the "Preparing company…" placeholder), and every test passes an
`initialCompany`. Without the entry the screen renders company-less and `useCompanyID()` **throws
during render**, taking the shell down.

`TallyImportScreen.test.tsx` now has the company-less case (`CompanyProvider` does not bootstrap
unless asked, so it is deterministic). ⚠️ **NOTHING COVERS IT FOR THE OTHER FOURTEEN SCREENS IN THAT
SET.** Tracks C and D each add a company-scoped screen: **write that test.**

### 3. ⛔ jsdom IS MISSING MORE OF `File`/`Blob` THAN YOU THINK

**`File.text()`, `File.arrayBuffer()`, `File.stream()` and `Blob.arrayBuffer()` are ALL absent.**
Measured. Consequences:

- read a Blob's bytes with **`FileReader.readAsArrayBuffer`**;
- a mutation that calls one of them **fails for the wrong reason** — the first `.text()` mutation
  threw a `TypeError` and turned 16 tests red while proving nothing about encodings. Simulate
  `.text()` with **`new TextDecoder("utf-8").decode(bytes)`**, which is what the spec says it does.

⚠️ **AND `FileReader.readAsText` IS NOT `Blob.text()`.** Measured both ways on the UTF-16 fixture:
`readAsText` **BOM-sniffs and decodes correctly**; `TextDecoder("utf-8")` yields mojibake. A
developer who checked with a FileReader would have concluded that sending a string is fine and
shipped the version that is not.

### 4. ⛔ A GATE OVER A SERVER DECISION IS A **GENERATED GOLDEN**, NEVER A LIST IN TYPESCRIPT

B1 needed to know which reports actually print (10 of 30; the rest answer 501).
`internal/server`'s **`TestPrintedReportLayoutsGolden`** requests all thirty, reads the answers, and
emits `frontend/src/api/goldens/printed-report-layouts.json`. The frontend reads that. **Measured
from the router, not typed.** It is the `tds-register-prose` mechanism (`internal/reporting`).

⚠️ **AND ITS GENERATOR SKIPS WITHOUT POSTGRES**, where a skip is indistinguishable from a pass in
`go test`'s exit code — so `exportPdf.test.tsx` asserts the golden's **shape** (the two lists
partition all 30, neither is empty, anchors on the right side) in a suite that always runs.

> ⛔ **TRACK C HAS THE IDENTICAL PROBLEM.** `print_template.doc_kind`'s CHECK vocabulary is a server
> decision the screen needs. **Generate it. Do not hardcode a TypeScript copy** — §4.1.

### 5. ⚠️ ONE DOWNLOAD ANCHOR, AND ONE WAY TO REACH A SCREEN IN A DRIVE

- **`export/download.ts`'s `downloadBlob` is now the ONLY download anchor.** There were **three**
  (`downloadSheet`, `usePrint`'s pop-up fallback, `DataScreen`) and they disagreed: two revoked the
  object URL on a 60s timer **and said why** (*"revoking before the browser has read the blob
  produces an empty file"*), the third revoked **synchronously in a `finally`**. All three now revoke
  late. **Do not write a fourth.**
- **In a browser drive: `openFromGateway` drives the GATEWAY menu (screens); report NAMES live behind
  `Alt+G`.** The B1 drive used the wrong one and never left the Gateway — it opened a Sales Voucher
  and three probes failed. `drive-b1-pdf-export.mjs`'s `goTo` is the Alt+G form;
  `drive-t32-import-screen.mjs` uses `openFromGateway`, correctly, because its screen is not a report
  name. **Playwright:** `page.keyboard.press("Alt+KeyE")` works for a global keymap binding; use the
  harness's `altMac("x")` for anything macOS **composes**.

---

## 1. THE SHARED REGISTRATION SURFACE — read once, use in tracks C and D

`HANDOFF.md` §7.2 is the authority. **Two worked examples now: T14's screen commit `b5c61b7` and
T32's `d60546c`.** Read either diff — they touch exactly these files.

| # | File | What |
|---|---|---|
| 1 | `keystroke/types.ts` | `ScreenKind` union (⚠️ **here, not `screenRegistry.tsx`**) |
| 2 | `keystroke/types.ts` | `KeymapAction` union |
| 3 | `keystroke/keymaps/tallyprime.ts` | the global binding |
| 4 | `keystroke/keymaps/tallyprime.ts` | `actionLabel()` case (⚠️ **here, not `AppShell.tsx`**) |
| 5 | `keystroke/navMachine.ts` | `case "<action>": return pushOnce("<screen>")` |
| 6 | `keystroke/buttonBar.ts` | a bar for the screen |
| 7 | `app/AppShell.tsx` | the **company-scoped** set — omit it and `useCompanyID()` THROWS. ⛔ **§0.1 item 2: no test sees this unless you write the company-less one** |
| 8 | `app/AppShell.tsx` | the title case (`default` is "Tally", silently wrong) |
| 9 | `app/AppShell.tsx` | the hint strip case |
| 10 | `app/defaultScreens.tsx` | the adapter + the registry entry |
| 11 | `corpora/validate.py` | **`KNOWN_SCREENS`** (~line 66) — ⛔ **outside `frontend/` entirely**, and a `ScreenKind` it does not list **fails `make verify`**. ⚠️ CO-OWNED: **ADD only, re-read immediately before editing** |
| +2 | `screens/GatewayScreen.tsx` + a `*_LABEL` in `navMachine.ts` | **only if the screen gets a Gateway door** (T32 did; T14 did not) |

⛔ **THREE OF THESE SHIP THE KEY COMPLETELY DEAD WITH NO ERROR AND NO STATUS LINE** — the keymap
binding (#3), the navMachine case (#5), the registry entry (#10). T14 and T32 each mutation-proved
them **separately**; do the same. An unbound key is not inert: F5 opens a Payment over your form.

⚠️ **`SCREEN_ACTIONS` in `AppShell.tsx` is a DIFFERENT list**, only for actions the *active screen*
owns. **Opening a screen goes through the navMachine.** But a key the screen itself handles must be
registered there and its omission is **silent** — `server.go`'s own comment records `Alt+O` shipping
dead with every other piece correct.

### 1.1 How a screen is REACHED besides its key

⛔ **`Alt+G` Go To IS NOT A DOOR** for a ScreenKind. The picker is hard-wired to
`["Vouchers", ...REPORT_NAMES]`, so Cost Centres, Bank Reconciliation, TDS Challans, the order
registers, the BOM screen and T32's importer are all absent from it and present on the **Gateway**.
A `*_LABEL` const per Gateway entry must be matched **BEFORE** the `canonicalReportName` fallback —
otherwise the label pushes a report frame of that name.

### 1.2 Client methods go in THREE files

`api/client.ts` (**declare it here first** — `rest.ts` and `mock.ts` then fail to compile until both
implement it; use that), `api/rest.ts`, `api/mock.ts`.

⛔ **THE MOCK MUST BE HONEST, NOT HELPFUL.** A second implementation where it replays domain logic
(FIFO, group rollups), an honest refusal everywhere else — `restoreCompany` throws *"the mock has no
database to create one in"*, `importTallyXML` returns zeroes and says it does not parse XML. **Never
fake a payload a real backend has never produced.**

---

## 2. ~~TRACK A — MIGRATE FROM TALLY~~ ✅ SPENT 2026-08-26 (`d60546c`)

**`HANDOFF.md` §1.18 has it in full**; `HANDOFF-T32-IMPORT-SCREEN.md` is `git rm`'d.
`Alt+O` and `Gateway of Tally > Import` both open it; the FeaturesScreen row reads **On**.

⛔ **THE HALF STILL OUTSTANDING IS A HUMAN'S: ASK FOR THE GST-REGISTERED TALLY EXPORT** (`HANDOFF.md`
§10 item 1). `classifyTaxLedger`, `inferRate` and the GSTR-1 B2B join have **never met real GST
data** — the file the importer was fixed against is a grain trader, GST-free and zero-rated. **Do not
touch those three until it arrives.** One email; it is the commercial blocker.

## 2.1 ~~TRACK B1 — EXPORT TO PDF~~ ✅ SPENT 2026-08-26 (`13e0ba8`)

**`HANDOFF.md` §1.19 has it in full.** F12 offers "Export (Alt+E) as PDF"; Alt+E **saves** the
server's document (Ctrl+P still **opens** it). Offered only on the 10 reports with a layout, gated by
the generated golden of §0.1 item 4. The status line **names the Ctrl+F filter that was not applied**,
because a PDF is the whole statement and not the rows on screen.

---

## 3. TRACK B2 — E-MAIL A DOCUMENT. ✅ DECIDED: `mailto:` HANDOFF.

> ## ✅ THE DECISION (user, 2026-08-26): **THIS PRODUCT DOES NOT SEND MAIL.**
>
> "E-mail a document" means: the PDF is downloaded, and the operator's **own mail client** opens with
> a pre-addressed, pre-subjected, pre-bodied draft. They attach the file and send it.
>
> ⛔ **SO `internal/notify` IS NOT TOUCHED AT ALL.** No `Sender`, no SMTP, no attachment concept, no
> new `Kind`, no migration, no deliverability story, no bounce handling. If you find yourself editing
> `internal/notify`, **stop and re-read this box.**

### 3.1 What that costs, and the one thing that must be on the screen

⛔ **A WEB PAGE CANNOT ATTACH A FILE TO A `mailto:` DRAFT.** There is no such thing in the `mailto:`
URL scheme and no browser implements one. So the flow is inherently two steps, and **the screen must
say so before the operator clicks** — otherwise they send an invoice e-mail with no invoice on it and
find out from the customer.

The honest shape:

1. the PDF is **downloaded first** (B1's `downloadBlob` already does exactly this — reuse it, do not
   write a fifth path);
2. **then** the draft opens. ⛔ **ITS BODY MUST NOT SAY "the invoice is attached"**, because at that
   moment it is not. Word the body so it is true of a draft nobody has attached to yet, and put the
   *instruction* in the UI rather than in the e-mail;
3. the screen **names the downloaded file**, so the operator knows which file to attach.

⚠️ **AND `mailto:` HAS LIMITS WORTH KNOWING BEFORE DESIGNING THE BODY.** URL length is capped in
practice (keep well under ~2 KB), the body needs `encodeURIComponent`, and a newline is `%0D%0A`. A
long templated body silently truncates in some clients — keep it short and put nothing load-bearing
at the end of it.

### 3.2 Where it hangs, and what it must NOT claim

- **The key.** Tally's Alt+E menu lists "e-mail this document" as one of its export choices, and
  `UNBUILT_FORMATS` in `export/sheet.ts` still names it. The natural home is therefore the **same F12
  export block B1 added** — but ⛔ **it is an ACTION, not a FORMAT.** `ExportTarget` is
  `"csv" | "excel-xml" | "pdf"` and every one of those writes a file; e-mail *also* writes a file and
  then does something else. Decide deliberately whether it is a fourth `ExportTarget` or its own key,
  and **say which in the commit**. A fourth "format" that silently opens a mail client is a surprise.
- ⛔ **REMOVE `"e-mail this document"` FROM `UNBUILT_FORMATS`** the day it ships — the rule `Alt+O`
  and B1's PDF both followed. (It is currently `["JPEG", "HTML", "e-mail this document"]`.)
- ⛔ **THE FEATURESCREEN ROW** is currently **`Export: e-mail a document` — `off`**, and its note is
  long and already states this decision and its alternative. **Edit the clause that stops being true;
  do not rewrite the note.**
- ⚠️⚠️ **A PINNED TEST WILL BREAK, AND IT IS THE ONLY ONE THAT WILL.**
  `VoucherScreen.stockVoucher.test.tsx:967` pins `/^Export: e-mail a document/` as its Not-built row
  and asserts `getAllByText("Not built").length > 1`. T32 and B1 both checked it and were safe;
  **B2 is not.** Re-point it at another genuinely-absent row **in the same change**, and say so.
  `FeaturesScreen.test.tsx` has both patterns.
- ⛔ **AND THE ROW MUST NOT READ "On" AS IF THE PRODUCT SENDS MAIL.** Name the mechanism in the row
  itself — e.g. *"E-mail a document (opens your mail client)"* — so nobody reads a green badge as an
  SMTP feature. The note keeps the reason the transport does not exist.

**Proof:** a test asserting the `mailto:` URL is built with an encoded subject and body **and that
the PDF was downloaded first** — both, *in order*; a draft with no file downloaded is the failure.
⛔ **Mutation:** open the draft without downloading, and watch the ordering assertion name it.
⚠️ **In jsdom, `window.open` / `location.href` must be stubbed** — assert the URL that was requested,
never that a client opened.

---

## 4. TRACK C — PER-COMPANY PRINT TEMPLATES

> ## ⛔ THE WRITE ALREADY EXISTS AND NOTHING CAN REACH IT. RE-VERIFIED 2026-08-26.
>
> `printingdb.Store.SaveOverrides` (`printing/printingdb/template.go:153`) is fully implemented — a
> real `INSERT … ON CONFLICT (company_id, doc_kind) DO UPDATE`, `updated_at` set on both paths, and
> **tested**.
>
> ```
> /usr/bin/grep -rn 'SaveOverrides' backend/internal/server/    -> EMPTY (exit 1)
> /usr/bin/grep -rn 'SaveOverrides' backend/ | grep -v _test    -> only its own definition + comment
> psql -tAc "SELECT count(*) FROM print_template"               -> 0
> ```
>
> **No route calls it. Not one row exists in the entire database.** This is `payrolldb`'s bug
> exactly. ⛔ **THIS TRACK IS ONE ROUTE PLUS ONE SCREEN.** It is not "build a templating system".
>
> ✅ **AND THE READ SIDE IS ALREADY WIRED**, which is what makes the proof easy: `LoadOverrides` is
> consumed in **three** places — `printingdb/document.go:1218` and `:1924` (the two document paths)
> and `handler_print_reports.go:181` (the report letterhead). **The printer already honours
> overrides.** Write one, print, and the bytes change.

### 4.1 What the table holds — MEASURED 2026-08-26

```
psql -tAc "SELECT column_name||':'||data_type FROM information_schema.columns
           WHERE table_name='print_template' ORDER BY ordinal_position"
```

```
id:uuid · company_id:uuid · doc_kind:text · title:text · declaration:text · terms:text ·
signature_label:text · header_lines:ARRAY · footer_lines:ARRAY · bank_lines:ARRAY ·
show_bank_details:boolean · created_at · updated_at
```

**`doc_kind` vocabulary** — read from the live catalog (migration 0037's
`print_template_doc_kind_known` CHECK):

```
tax-invoice · bill-of-supply · delivery-note · receipt · payment · credit-note · debit-note · report
```

⛔ **DO NOT HARDCODE A TYPESCRIPT COPY OF THAT LIST.** That is the drift bug this repo has had three
times, and **B1 already built the answer**: generate a golden the way
`TestPrintedReportLayoutsGolden` does (§0.1 item 4) from the Go `DocKind` constants or the CHECK
itself, and have the frontend consume it. ⚠️ Remember the counterweight: if the generator needs
Postgres it **skips silently**, so assert the golden's shape in a vitest that always runs.

### 4.2 ✅ THE LOGO — DECIDED: SHIP THE FOUR THAT EXIST, RENAME THE ROW

```
psql -tAc "SELECT table_name||'.'||column_name FROM information_schema.columns
           WHERE table_schema='public' AND (column_name LIKE '%logo%' OR column_name LIKE '%image%')"
   -> EMPTY          (re-verified 2026-08-26: there is NO image storage anywhere in the schema)
```

`FeaturesScreen.tsx` names the row **"Per-company print templates (logo, address block, footer,
declarations)"**. Three of those four are columns. **A logo is not.**

> ## ✅ THE DECISION (user, 2026-08-26): **(a) SHIP THE FOUR, DROP "logo" FROM THE ROW.**
>
> Rename the row so it stops promising a logo, with a note saying a logo would need image storage the
> product does not have. **No migration, no binary column, no object store, no upload route, no
> size/type policy, and no change to the PDF renderer.**
>
> ⛔ **WHAT MUST NOT HAPPEN IS SHIPPING THE FOUR AND LEAVING THE ROW PROMISING A LOGO.** That is the
> exact failure mode the whole FeaturesScreen exists to prevent. **Mutation-prove it**: leave "logo"
> in the row name and watch a test name it.

### 4.3 The route

`PUT /v1/companies/{companyID}/print-templates/{docKind}` (+ a `GET` so the form can hydrate).

**TWO-TOUCH RULE — the route is inert without all four:**

1. `routeAction()` in `auth_middleware.go` — add the arms;
2. `authz.KnownResources` in `authz/vocabulary.go` — the token;
3. gate: `go test ./internal/server/ -run TestEveryCompanyRouteIsMappedAndInVocabulary`;
4. archtest: `internal/server` already reaches `internal/printing/printingdb`, so **no new edge**.

⛔ **THE TOKEN. Model it as `company`, not `report`, and not a new token.** A print template is
company configuration — the same authority as the F11 feature settings, which already emit
`(Alter, "company")`.

⚠️ **NOT `report:NAME`**: migration 0011 seeds every Data Entry level a coarse `allow display
report`, so a report-shaped token is granted to every operator in every tenant the day it ships —
and this one rewrites what the company's invoices say. Migration 0022 seeds Data Entry `allow
display company` (a **Display**), so `alter company` stays **default-DENIED** for them, which is the
right answer and **needs no ACL migration**. Being a write, it inherits the Owner level's WebAuthn
step-up and correctly does **not** inherit the back-dated cutoff (a layout carries no date).

### 4.4 The screen

Reached from **F11 Company Features**, where a Tally operator looks for company configuration and
where the three existing real controls live (**TDS Deductor Details is the pattern** —
`FeaturesScreen.tsx`, *"the FIRST of the three REAL CONTROLS on this screen"*).

⚠️ **A DOC KIND WITH NO OVERRIDE ROW IS NOT EMPTY — IT PRINTS THE BUILT-IN LAYOUT.** `LoadOverrides`
returns the zero value with **no error** for an absent row (migration 0037 says so). So:

- the form must show **what will actually print**, not blank fields that read as "this company has no
  declaration";
- **"clear this override" must be reachable**, or a company that sets a declaration once can never go
  back to the default.

**Proof:** ⛔ **an HTTP test that PUTs an override and asserts THE PRINTED PDF CHANGES** — not that
the row was written. The read side is already wired (§4's box), so this is a genuine end-to-end
oracle. ⛔ **Mutation:** have the handler drop `declaration` and watch the print assertion name it.
**A test that only reads the row back cannot see a printer that ignores the override.**

---

## 5. TRACK D — THE OFFLINE ENTRY SCREEN

> ## ⛔ THE ONLY ONE OF THE FOUR THAT IS A DESIGN PROBLEM.
> The engine is complete and there is **no client at all**:
> `/usr/bin/grep -rln "sync/commit\|syncCommit" frontend/src/` → **empty**.
> **Budget the thinking, not the typing.**

### 5.1 What exists — measured

`POST /v1/companies/{companyID}/sync/commit` (`server.go`), the cloud sequencer (`internal/sync`),
HLC timestamps, per-intent idempotency, and conflict/freeze handling.

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
Anything the online voucher path carries, the offline path carries for free — including the
once-only AI-bill `draft_id`. **Do not add a parallel type.** `server.go` warns about this three
times and it has caused two silent-drop defects.

### 5.2 The decision the screen cannot avoid

**A provisional number is not a voucher number.** The operator keys `provisional_no` offline and the
sequencer assigns the **final** one at commit. So the screen must answer:

1. **What does a queued voucher look like** before it has been sequenced — and how is it visibly
   *not* a posted voucher? Showing a provisional number in the same field a final number appears in
   is how somebody reads it out to a customer.
2. **What happens to a `CONFLICT`?** There is a `reconciliation_id` and a `detail`. The operator must
   be able to see what the server has and what they keyed, and choose. That is a screen of its own.
3. **What happens to a `FROZEN`?** It cannot be superseded, ever. A dead end that must be
   **explained, not retried**.
4. **Where does the queue live?** IndexedDB / localStorage — and ⚠️ **it is unencrypted books on a
   shared machine.** Decide and write it down.

⚠️ **AND KNOW THE HONEST LIMIT.** `FeaturesScreen` currently says *"There is no offline-capable
client in this build — this application needs the server — so nothing here can be keyed on a
train."* A service worker and an offline app shell are a real piece of work **separate from** the
queue screen. It is legitimate to ship the queue + reconciliation UI first (useful the moment a
commit fails or a connection drops mid-session) and leave the row honest about the shell.
**Say which you did.**

**Proof:** ⛔ **do not assert a specific NON-TERMINAL status name** in a probe (`HANDOFF.md`'s
standing rule) — the four above are terminal and safe to pin. The drive must produce a **real**
`CONFLICT` by committing a stale `base_version`, not by stubbing one.

---

## 6. THE RULES THAT ARE NOT NEGOTIABLE

1. **Both proofs, for every fix.** An EXECUTED mutation proof whose failure was **predicted, then
   observed**, *and* a drive in a real browser.
2. ⛔ **Run every new probe against the BROKEN build first.** A probe that has never failed proves
   nothing.
3. ⛔ **A MUTATION THAT SILENTLY DOES NOTHING LOOKS EXACTLY LIKE A TEST THAT PASSED.** **Assert the
   anchor occurs exactly once before replacing it, and print the byte delta** — this session caught
   one mutation whose anchor matched **zero** times (an escaped emoji in the anchor string), and it
   would otherwise have been logged as a pass.
4. ⛔ **AN ORACLE THAT CANNOT SEE THE DEFECT IS NOT AN ORACLE** (`HANDOFF.md` §1.16). Applied here:
   track C must assert the **printed bytes**, not the stored row.
5. ⛔ **A REFUSAL FOR THE WRONG REASON IS A VACUOUS PROBE.** Three of this session's failures were in
   the PROBE, not the product — a `.text()` mutation that failed because jsdom lacks the method, a
   drive that compared a raw byte count against a **localised** one (`"15,328"`), and a drive that
   pressed a key on a screen that had not finished loading. **Make sure the thing you are testing is
   the thing that refuses.**
6. ⚠️ **macOS COMPOSES Option+<letter>.** Drive every Alt binding with the harness's `altMac("x")`,
   never `press("Alt+x")`. Every Alt binding in this product was once found dead on macOS while
   every browser test passed.
7. ⚠️ **DRIVE ON A THROWAWAY COMPANY, NEVER `Demo Traders`**, and **tear it down**.
   `drive-t32-import-screen.mjs` and `drive-b1-pdf-export.mjs` are the copy-mes (each creates a
   tenant and drops it with a catalog-derived sweep). Residue drops `verify:demo` below 34/34.
8. **Assert a PROPERTY, not a list.**
9. ⛔ **VERIFY, DO NOT INHERIT — including this file.** Every claim above has a command beside it.
   Run them. ⚠️ **With `/usr/bin/grep`** — see §0.1 item 1.

### 6.1 The mutation harness this session used — worth rebuilding

A ~120-line python script (in the session scratchpad, **not committed**) holding a table of
`(name, file, anchor, replacement, test files, predicted-RED, predicted-GREEN)`. For each entry it:

1. **asserts the anchor occurs exactly once**, aborting loudly otherwise (rule 3);
2. writes the mutation and **asserts it applied**, printing the file and byte delta;
3. runs the named tests and reports failures **by name**;
4. reverts and **asserts the file is byte-identical**;
5. **compares predicted against observed** and prints `PROVED` or `MISMATCH`.

**17 mutations were run this session and every one ended up matching its prediction** — after two of
the predictions themselves were found wrong, which is exactly why they are written down first.

---

## 7. WHEN EACH ONE LANDS

- Fold it into `HANDOFF.md` (**§1.20** onward; T32 took §1.18, B1 took §1.19), flip its `HANDOFF.md`
  §1.11 row (T32 is row 5, B1 is row 6, so offline entry is now row 7), and update the FeaturesScreen
  row **in the same change that makes it true** — that is the screen's own rule: a row flips when an
  operator gets a SCREEN, not when they got a route.
- Add a `docs/RELEASE-NOTES.md` note leading with what an operator will act on.
- ⛔ **SHRINK THIS FILE as tracks land** — `HANDOFF.md` §11's rule is *update in place rather than
  write another*, and its evidence is twelve superseded files that each re-derived context the last
  already held. Delete a spent track's section; do not leave it as prose.

### Still blocked on a human, not on us

- **The GST-registered Tally XML export** — the importer's screen shipped without it;
  `classifyTaxLedger`, `inferRate` and the GSTR-1 B2B join still need it and **must not be touched
  until it arrives**.
- **IRP / GSTN / e-Way Bill sandbox credentials** — the **Rule 48(4) QR code** row cannot be built
  without them. If they arrive, the IRN screen stops being groundwork and becomes a filing, and the
  e-way bill becomes real **with no change to either payload builder**. Re-order immediately.

### The rest of the backlog, after these three

`Multi-currency (T8)` → `Budgets (T11)` → `Interest on overdue bills (T13)` → `GSTR-9 (T12)` →
`SSO/OIDC last`. `HANDOFF.md` §5.4 states the one thing not to get wrong in each. ⚠️ **GSTR-9 is
worth deferring until real GST data has been through the importer** — it is an annual return and it
reads everything the untested tax paths produce.
