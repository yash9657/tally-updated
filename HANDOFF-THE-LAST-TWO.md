# HANDOFF — THE LAST TWO

**Updated:** 2026-08-27 (late) · **Branch:** `session/t32-and-close-7-3` · **Tree:** clean
**Track C landed** — see `HANDOFF.md` **§1.20**. Two tracks left: **B2** (§2) and **D** (§3).
**Read time:** ~12 minutes. Every claim was MEASURED, most of it re-verified 2026-08-26/27; each has
the command beside it. ⛔ **§0.1 is the part to read first — six of its seven items cost real time.**

> ## ✅ BOTH BLOCKING DECISIONS ARE MADE (user, 2026-08-26). DO NOT RE-LITIGATE EITHER.
>
> | Decision | Answer | What it removes from the work |
> |---|---|---|
> | **Does this product send mail?** | ⭐ **NO — a `mailto:` HANDOFF.** The PDF downloads, then the operator's own mail client opens with a pre-addressed, pre-subjected draft. They attach the file and send. | **No SMTP, no `Sender`, no attachment concept, no `Kind`, no bounce story, no migration.** `internal/notify` is **not touched at all**. |
> | **The invoice logo** | ⭐ **SHIP THE FOUR FIELDS THAT EXIST; RENAME THE ROW to drop "logo".** | No image storage, no upload route, no size/type policy, **no change to the PDF renderer**. |
>
> ## THE TWO, IN THE RECOMMENDED ORDER
>
> | # | Track | Size | Why here |
> |---|---|---|---|
> | **1** | **B2 — e-mail a document** (§2) | small | Decision made; B1 already leaves the PDF on disk. ⚠️ It is the one that **breaks a pinned test on purpose** |
> | **2** | **D — offline entry screen** (§3) | ⛔ large, and it is **thinking, not typing** | The engine is complete and there is **no client at all**. Four questions must be answered before any code |
>
> ⚠️ **THE ORDER IS A RECOMMENDATION, NOT A DEPENDENCY.** Neither B2 nor D touches anything the other
> needs. Take them in either order; do **not** interleave them in one commit.
>
> ✅ **TRACK C IS SPENT** — folded into `HANDOFF.md` §1.20, F11 row renamed, `drive-c-print-templates.mjs`
> 33/33. §1 below is a marker; the section is deleted.

### ⛔ AND ONE THING THAT IS NOT A TRACK AND COSTS ONE E-MAIL

**Ask for a real TallyPrime XML export from a GST-REGISTERED company** (`HANDOFF.md` §10 item 1).
The import screen shipped (`Alt+O`, §0.2), so receiving that file went from an afternoon to ten
minutes — but `classifyTaxLedger`, `inferRate` and the GSTR-1 B2B join have **never met real GST
data**: the only real file this importer was ever fixed against is a grain trader, GST-free and
zero-rated. ⛔ **Do not touch those three until it arrives.** Speculative changes now destroy the
evidence the file will give you. It is the commercial blocker.

---

## 0. RUN-FIRST — the stack, and the numbers to beat

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
lsof -nP -iTCP:8087 -sTCP:LISTEN -t   # and 5173, 5433
```

⛔ **CHECK THE AGE OF THE RUNNING `cored` BEFORE BELIEVING ANY DRIVE.** `make run-cored` is `go run`,
so a stale process is indistinguishable from a fix that does not work — **and a stale one was found
this way**, serving a binary older than the previous session's own backend commit. Restarting costs
one second.

```bash
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"      # NEWER than the last backend commit?
kill <pid> && (nohup make run-cored > /tmp/cored.log 2>&1 &)    # up in ~1s
# For a track that DOES change Go code, grep the binary for a new string:
strings "$(ps -o command= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)" | awk '{print $1}')" | /usr/bin/grep -c <new-symbol>
```

⛔ **VITE MUST BE ON `:5173`.** WebAuthn origin matching is exact; a vite that took `:5174` signs in
fine and then fails every write with `PASSKEY STEP-UP FAILED`.

**The gate baseline, RE-MEASURED 2026-08-27 after track C — these are the numbers to beat:**

| Gate | Command | Value now |
|---|---|---|
| backend build + vet | `cd backend && go build ./... && go vet ./...` | clean |
| backend tests | `cd backend && go test ./... -timeout 60m` | **exit 0** |
| typecheck | `cd frontend && pnpm exec tsc --noEmit` | clean |
| frontend tests | `cd frontend && pnpm vitest run` | **1662 pass / 98 files** |
| live demo | `cd frontend && pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT |
| corpora | `python3 corpora/validate.py` | OK (migration=6, inventory=11) |
| everything | `make verify` | exit 0 |
| T32 drive | `node frontend/scripts/drive-t32-import-screen.mjs` | **25/25**, tenant torn down |
| B1 drive | `node frontend/scripts/drive-b1-pdf-export.mjs` | **15/15**, tenant torn down |
| C drive | `node frontend/scripts/drive-c-print-templates.mjs` | **33/33**, tenant torn down |

⚠️ **`make verify` DOES NOT RUN `tsc`.** Different gates, and each has caught a real error the other
missed — including last session, where a new test file was green in vitest with **8 real `tsc`
errors**.

### 0.2 What landed immediately before this file

| Commit | What |
|---|---|
| `05c9775` | **A raw NUL byte** in `mock.ts` had made 8,672 lines **invisible to grep**. §0.1 item 1 |
| `d60546c` | **T32** — `Alt+O` and `Gateway > Import` import a TallyPrime export. `HANDOFF.md` **§1.18**. No Go code changed |
| `13e0ba8` | **B1** — F12 offers "Export (Alt+E) as PDF"; Alt+E **saves** it (Ctrl+P still **opens** it). `HANDOFF.md` **§1.19** |
| _this_ | **C** — Gateway > Print Templates writes a per-company layout; the F11 row is RENAMED to drop "logo". `HANDOFF.md` **§1.20** |

⛔ **Read `HANDOFF.md` §1.17's *"three rules this lane paid for"*, §1.18's *"three things the mutation
proofs found"*, and §0.1 below before writing a probe.** Between them they cost four sessions.

---

## ⛔ 0.1 SEVEN HAZARDS ALREADY PAID FOR. READ THIS OR RE-PAY THEM.

### 1. ⛔⛔ THE SHELL'S `grep` IS A `ugrep` SHIM THAT SILENTLY SKIPS FILES — AND IT LIED

`grep` here is a shell **function** wrapping `ugrep -G --ignore-files --hidden -I` (see
`~/.claude-premium/shell-snapshots/`). `-I` skips any file it judges **binary**, and one NUL byte is
enough.

`frontend/src/api/mock.ts` held a **raw NUL** as a map-key separator, so the whole 8,672-line mock was
invisible: `grep -n importTallyXML src/api/mock.ts` returned **nothing** while the method sat at line
7309, fully implemented. Fixed in `05c9775`.

> ⛔ **THIS REPOSITORY'S HANDOFFS USE `grep … -> EMPTY` AS EVIDENCE THAT SOMETHING IS UNWIRED.**
> Against a skipped file that evidence is worthless in the dangerous direction. **Use `/usr/bin/grep`
> whenever an absence is load-bearing.** Track C's "nothing calls SaveOverrides" claim was
> re-verified that way before a line was written, and it held.

### 2. ⛔ A REGISTRATION POINT NO TEST CAN SEE — STILL OPEN FOR 13 SCREENS

Deleting a screen from `AppShell.tsx`'s `COMPANY_SCOPED_SCREENS` broke **nothing**: 25 of 25 tests
stayed green. That set is consulted **only when `company === null`** (`needsCompany && !companyReady`
picks the "Preparing company…" placeholder) and every test passes an `initialCompany`. Without the
entry the screen renders company-less and `useCompanyID()` **throws during render**, taking the shell
down.

`TallyImportScreen.test.tsx` and `PrintTemplateScreen.test.tsx` now have that case (`CompanyProvider`
does not bootstrap unless asked, so it is deterministic — copy either). ⚠️ **NOTHING COVERS IT FOR THE
OTHER THIRTEEN SCREENS IN THAT SET.** **Track D adds one: write that test.**

### 3. ⛔ jsdom IS MISSING MORE OF `File`/`Blob` THAN YOU THINK

**`File.text()`, `File.arrayBuffer()`, `File.stream()` and `Blob.arrayBuffer()` are ALL absent.**
Measured. So:

- read a Blob's bytes with **`FileReader.readAsArrayBuffer`**;
- a mutation that calls one **fails for the WRONG REASON** — the first `.text()` mutation threw a
  `TypeError` and turned 16 tests red while proving nothing about encodings. Simulate `.text()` with
  **`new TextDecoder("utf-8").decode(bytes)`**, which is what the spec says it does.

⚠️ **AND `FileReader.readAsText` IS NOT `Blob.text()`**: the first BOM-sniffs and decodes UTF-16
correctly, the second is a plain UTF-8 decode and produces mojibake. Both measured. A developer who
checked with a FileReader would have concluded that sending a string is fine and shipped the version
that is not. **B2 touches file handling; keep this in mind.**

### 4. ⛔ A GATE OVER A SERVER DECISION IS A **GENERATED GOLDEN**, NEVER A LIST IN TYPESCRIPT

B1 needed to know which reports actually print (10 of 30; the rest answer 501).
`internal/server`'s **`TestPrintedReportLayoutsGolden`** requests all thirty, reads the answers, and
emits `frontend/src/api/goldens/printed-report-layouts.json`, which the frontend consumes. **Measured
from the router, not typed.** It is `internal/reporting`'s `tds-register-prose` mechanism.

⚠️ **ITS GENERATOR SKIPS WITHOUT POSTGRES**, and a skip is indistinguishable from a pass in `go
test`'s exit code — so `exportPdf.test.tsx` asserts the golden's **shape** (the two lists partition
all 30, neither is empty, anchors on the right side) in a suite that always runs.

> ✅ **TRACK C HAD THE IDENTICAL PROBLEM AND THIS IS WHERE IT GOT BETTER.** Its golden
> (`print-template-surface.json`) is emitted by RENDERING each document kind twice per field and
> comparing the PDF bytes, and it needs **no Postgres** — so unlike the one above it **cannot hide
> behind a skip**, and a false "this field prints" is not a thing the measurement can produce. Copy
> **that** one (`internal/printing/template_surface_golden_test.go`) in preference to this one, and copy
> its counterweight too (`printTemplateSurface.test.ts` asserts the artefact's shape as PROPERTIES).

### 5. ⚠️ ONE DOWNLOAD ANCHOR, AND TWO WAYS TO REACH A SCREEN IN A DRIVE

- **`export/download.ts`'s `downloadBlob` is the ONLY download anchor.** There were **three**
  (`downloadSheet`, `usePrint`'s pop-up fallback, `DataScreen`) and they disagreed: two revoked the
  object URL on a 60s timer **and said why** (*"revoking before the browser has read the blob produces
  an empty file"*), the third revoked **synchronously in a `finally`**. All three now revoke late.
  ⛔ **B2 needs a download. Do not write a fourth.**
- **In a drive: `openFromGateway` drives the GATEWAY menu (ScreenKinds); report NAMES live behind
  `Alt+G`.** The B1 drive used the wrong one, never left the Gateway, opened a Sales Voucher and
  failed three probes. `drive-b1-pdf-export.mjs`'s `goTo` is the Alt+G form;
  `drive-t32-import-screen.mjs` uses `openFromGateway` correctly. **Playwright:**
  `page.keyboard.press("Alt+KeyE")` works for a global binding; use the harness's `altMac("x")` for
  anything macOS **composes**.

### 6. ⚠️ CLICKING A GATEWAY LABEL IN A DRIVE DOES NOT OPEN IT — USE `openFromGateway`

The `SelectionList`'s selection stays on its **default**, so `Enter` activates whatever was already
highlighted rather than the label that was clicked. T32's drive carries this warning and track C's
drive walked into it anyway, costing a run: the screen simply never appeared and the failure looked
like a dead registration point.

⛔ **Use the harness's `openFromGateway(label)`**, which drives the Gateway's own search box — the way
a keyboard operator gets there. (And it is still NOT `Alt+G`: that picker is hard-wired to
`["Vouchers", ...REPORT_NAMES]`, so no ScreenKind is in it. §0.1 item 5 already says so.)

⚠️ **AND WHEN A PROBE SEARCHES PDF BYTES, USE SINGLE TOKENS WITH NO SPACES.** The renderer wraps a long
string across several show-text operands, so a whole sentence never appears contiguously in the byte
stream and a search for one fails against a document that is perfectly correct — rule 5 again.

### 7. ⛔ NEVER `DELETE FROM company` ALONE. IT BREAKS `/v1/me` FOR EVERY SESSION.

A hand-rolled `DELETE FROM company WHERE id=…` for a throwaway probe tenant left **13 voucher_type, 28
account_group, 9 ledger, 96 acl_rule and 1 company_membership** rows behind. `GET /v1/me` then answered
**500** for every session — *"authz: user companies (resolve …): no rows in result set"* — and it looked
like a defect in the track being worked on.

⛔ **ALWAYS USE THE CATALOG-DERIVED SWEEP** the drive scripts carry: loop every `public` BASE TABLE with
a `company_id` column under `session_replication_role = replica`, then delete the company. Copy it from
`drive-c-print-templates.mjs` or `drive-b1-pdf-export.mjs`. The drives get this right; a one-line manual
delete does not, and the damage is not visible until a screen fails to load.

---

### 0.1.1 The shared registration surface — only if a track adds a SCREEN (D does)

`HANDOFF.md` §7.2 is the authority. **Two worked examples: `b5c61b7` (T14's Data screen) and
`d60546c` (T32's importer).** Read either diff — it touches exactly these files and nothing else.

| # | File | What |
|---|---|---|
| 1–2 | `keystroke/types.ts` | `ScreenKind` union, then `KeymapAction` union (⚠️ **here, not `screenRegistry.tsx`**) |
| 3–4 | `keystroke/keymaps/tallyprime.ts` | the global binding, then the `actionLabel()` case (⚠️ **here, not `AppShell.tsx`**) |
| 5 | `keystroke/navMachine.ts` | `case "<action>": return pushOnce("<screen>")` |
| 6 | `keystroke/buttonBar.ts` | a bar for the screen |
| 7 | `app/AppShell.tsx` | the **company-scoped** set — ⛔ **§0.1 item 2: no test sees this unless you write the company-less one** |
| 8–9 | `app/AppShell.tsx` | the title case (`default` is "Tally", silently wrong), then the hint strip case |
| 10 | `app/defaultScreens.tsx` | the adapter + the registry entry |
| 11 | `corpora/validate.py` | **`KNOWN_SCREENS`** (~line 66) — ⛔ **outside `frontend/` entirely**; a `ScreenKind` it does not list **fails `make verify`**. ⚠️ CO-OWNED: **ADD only, re-read immediately before editing** |
| +2 | `screens/GatewayScreen.tsx` + a `*_LABEL` in `navMachine.ts` | **only if the screen gets a Gateway door** (T32 did; T14 did not) |

⛔ **THREE OF THESE SHIP THE KEY COMPLETELY DEAD, WITH NO ERROR AND NO STATUS LINE** — the keymap
binding (#3), the navMachine case (#5), the registry entry (#10). T14 and T32 each mutation-proved
them **separately**; do the same. An unbound key is not inert: F5 opens a Payment over your form.

⚠️ **`SCREEN_ACTIONS` in `AppShell.tsx` is a DIFFERENT list**, only for actions the *active screen*
owns. **Opening a screen goes through the navMachine.** But a key the screen itself handles must be
registered there and its omission is **silent** — `server.go`'s own comment records `Alt+O` shipping
dead with every other piece correct.

⛔ **`Alt+G` Go To IS NOT A DOOR for a ScreenKind.** The picker is hard-wired to
`["Vouchers", ...REPORT_NAMES]`. A `*_LABEL` const per Gateway entry must be matched **BEFORE** the
`canonicalReportName` fallback, or the label pushes a report frame of that name.

⛔ **CLIENT METHODS GO IN THREE FILES**: `api/client.ts` (**declare it here first** — `rest.ts` and
`mock.ts` then fail to compile until both implement it; use that), `api/rest.ts`, `api/mock.ts`.
**THE MOCK MUST BE HONEST, NOT HELPFUL** — a second implementation where it replays domain logic
(FIFO, group rollups), an honest refusal everywhere else. `restoreCompany` throws *"the mock has no
database to create one in"*; `importTallyXML` returns zeroes and says it does not parse XML. **Never
fake a payload a real backend has never produced.**

---

# 1. ✅ TRACK C — SPENT. IT SHIPPED 2026-08-27.

**Folded into `HANDOFF.md` §1.20**, which carries the whole of it. This section is deleted per §5's
rule rather than left as prose. The four things worth carrying forward are already in §0.1 below
(items 6 and 7) and in `HANDOFF.md` §1.20.1–§1.20.9; in one line each:

- the field grid is **RAGGED** — `RenderReport` reads 3 of the 7 overridable fields, and a form built
  from the obvious reading stores a declaration no statement will ever print, with no error anywhere;
- the golden that decides which fields bite is **MEASURED by rendering** and **needs no Postgres**, so
  it fixes §0.1 item 4's skip hazard rather than inheriting it;
- the oracle was the **printed PDF**, never the stored row — and the drive's strongest probe is that
  after clearing, the PDF is **byte-identical** to the pre-override one;
- **the logo was cut and the F11 row renamed**, because the schema has no image storage anywhere.

# 2. TRACK B2 — E-MAIL A DOCUMENT. ✅ DECIDED: `mailto:` HANDOFF.

> ## ✅ THE DECISION: **THIS PRODUCT DOES NOT SEND MAIL.**
>
> The PDF downloads, and the operator's **own mail client** opens with a pre-addressed,
> pre-subjected, pre-bodied draft. They attach the file and send it.
>
> ⛔ **SO `internal/notify` IS NOT TOUCHED AT ALL.** No `Sender`, no SMTP, no attachment concept, no
> new `Kind`, no migration, no deliverability story, no bounce handling. **If you find yourself
> editing `internal/notify`, stop and re-read this box.**
>
> (For the record, so nobody reopens it: `internal/notify` is a real, durable, transactional outbox —
> `Enqueue` runs INSIDE the caller's transaction, at-least-once delivery, a lease, status transitions.
> It is genuinely good and it is **not** the missing piece. What is missing is a transport, and the
> decision above says we are not building one.)

## 2.1 The limit that must be ON THE SCREEN

⛔ **A WEB PAGE CANNOT ATTACH A FILE TO A `mailto:` DRAFT.** There is no such thing in the `mailto:`
URL scheme and no browser implements one. The flow is **inherently two steps**, and the screen must
say so **before** the operator clicks — otherwise they send an invoice e-mail with no invoice on it
and find out from the customer.

The honest shape:

1. the PDF is **downloaded first** — B1's `downloadBlob` already does exactly this; **reuse it**
   (§0.1 item 5);
2. **then** the draft opens. ⛔ **ITS BODY MUST NOT SAY "the invoice is attached"**, because at that
   moment it is not. Word the body so it is true of a draft nobody has attached to yet, and put the
   *instruction* in the UI rather than in the e-mail;
3. the screen **names the downloaded file**, so the operator knows which file to attach.

⚠️ **`mailto:` LIMITS, BEFORE YOU DESIGN THE BODY.** URL length is capped in practice (keep well
under ~2 KB), the body needs `encodeURIComponent`, and a newline is `%0D%0A`. A long templated body
**silently truncates** in some clients — keep it short and put nothing load-bearing at the end.

## 2.2 Where it hangs, and what it must not claim

- **The key.** Tally's Alt+E menu lists "e-mail this document" among its export choices, and
  `UNBUILT_FORMATS` in `export/sheet.ts` still names it (currently
  `["JPEG", "HTML", "e-mail this document"]`). The natural home is the **same F12 export block B1
  added** — but ⛔ **it is an ACTION, not a FORMAT.** `ExportTarget` is `"csv" | "excel-xml" | "pdf"`
  and every one writes a file; e-mail *also* writes a file and then does something else. **Decide
  deliberately** whether it is a fourth `ExportTarget` or its own key, and **say which in the
  commit**. A fourth "format" that silently opens a mail client is a surprise.
- ⛔ **REMOVE `"e-mail this document"` FROM `UNBUILT_FORMATS`** the day it ships — the rule `Alt+O`
  and B1's PDF both followed.
- **The FeaturesScreen row** is currently **`Export: e-mail a document` — `off`**, and its note is
  long and already states this decision *and* its alternative. **Edit the clause that stops being
  true; do not rewrite the note.**
- ⛔ **AND THE ROW MUST NOT READ "On" AS IF THE PRODUCT SENDS MAIL.** Name the mechanism in the row
  itself — e.g. *"E-mail a document (opens your mail client)"* — so nobody reads a green badge as an
  SMTP feature. The note keeps the reason the transport does not exist.

## 2.3 ⚠️⚠️ THE ONE PINNED TEST THIS TRACK WILL BREAK — ON PURPOSE

`VoucherScreen.stockVoucher.test.tsx:967` pins `/^Export: e-mail a document/` as its "Not built" row
and then asserts `getAllByText("Not built").length > 1`. **T32 and B1 both checked it and were safe;
B2 is not.**

⛔ **RE-POINT IT AT ANOTHER GENUINELY ABSENT ROW IN THE SAME CHANGE**, keep the plural property, and
**do not delete the test**. Its own comment (updated 2026-08-27) now says so at the site, and explains
that it is the last row that file can name. `FeaturesScreen.test.tsx` has both patterns — the flip and
the split.

**Proof:** a test asserting the `mailto:` URL is built with an encoded subject and body **and that the
PDF was downloaded first** — both, *in order*. A draft with no file downloaded is the failure mode.
⛔ **Mutation:** open the draft without downloading, and watch the ordering assertion name it.
⚠️ **In jsdom `window.open` / `location.href` must be stubbed** — assert the URL that was requested,
never that a client opened.

---

# 3. TRACK D — THE OFFLINE ENTRY SCREEN

> ## ⛔ THE ONLY ONE OF THE THREE THAT IS A DESIGN PROBLEM.
> The engine is complete and there is **no client at all**:
> `/usr/bin/grep -rln "sync/commit\|syncCommit" frontend/src/` → **empty**.
> **Budget the thinking, not the typing.** Answer §3.2's four questions before writing code.

## 3.1 What exists — measured

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
| `FROZEN` | the voucher is pinned by an external reference (IRN / filed GSTR-1) and **cannot ever** be superseded |
| `DUPLICATE` | this idempotency key (or this CREATE id) was already applied — the stored outcome is returned and **nothing is re-applied** |

⛔ **`syncIntentReq.Payload` IS a `voucherReq`, and `buildVoucherInput` is the SINGLE translator.**
Anything the online voucher path carries, the offline path carries for free — including the once-only
AI-bill `draft_id`. **Do not add a parallel type.** `server.go` warns about this three times and it
has caused two silent-drop defects.

## 3.2 The four questions the screen cannot avoid

**A provisional number is not a voucher number.** The operator keys `provisional_no` offline; the
sequencer assigns the **final** one at commit. So:

1. **What does a queued voucher look like** before it is sequenced — and how is it visibly *not* a
   posted voucher? Showing a provisional number in the same field a final number appears in is how
   somebody reads it out to a customer.
2. **What happens to a `CONFLICT`?** There is a `reconciliation_id` and a `detail`. The operator has
   to see what the server holds and what they keyed, and **choose**. That is a screen of its own.
3. **What happens to a `FROZEN`?** It can never be superseded. A dead end that must be **explained,
   not retried**.
4. **Where does the queue live?** IndexedDB / localStorage — and ⚠️ **it is unencrypted books on a
   shared machine.** Decide, and write the decision down.

⚠️ **AND KNOW THE HONEST LIMIT.** `FeaturesScreen` currently says *"There is no offline-capable client
in this build — this application needs the server — so nothing here can be keyed on a train."* A
service worker and an offline app shell are a real piece of work **separate from** the queue screen.
It is legitimate to ship the queue + reconciliation UI first — useful the moment a commit fails or a
connection drops mid-session — and leave the row honest about the shell. **Say which you did.**

**Proof:** ⛔ **do not assert a specific NON-TERMINAL status name** in a probe (`HANDOFF.md`'s standing
rule); the four above are terminal and safe to pin. The drive must produce a **real** `CONFLICT` by
committing a stale `base_version`, **not** by stubbing one.

---

## 4. THE RULES THAT ARE NOT NEGOTIABLE

1. **Both proofs, for every fix.** An EXECUTED mutation proof whose failure was **predicted, then
   observed**, *and* a drive in a real browser.
2. ⛔ **Run every new probe against the BROKEN build first.** A probe that has never failed proves
   nothing.
3. ⛔ **A MUTATION THAT SILENTLY DOES NOTHING LOOKS EXACTLY LIKE A TEST THAT PASSED.** **Assert the
   anchor occurs exactly once before replacing it, and print the byte delta.** Last session caught a
   mutation whose anchor matched **zero** times (an escaped emoji in the anchor string); it would
   otherwise have been logged as a pass.
4. ⛔ **AN ORACLE THAT CANNOT SEE THE DEFECT IS NOT AN ORACLE** (`HANDOFF.md` §1.16). Track C is the
   worked example: its tests assert the **printed PDF**, never the stored row — the row had been
   written correctly by a tested store for months while no document was affected by it.
5. ⛔ **A REFUSAL FOR THE WRONG REASON IS A VACUOUS PROBE.** Three of last session's failures were in
   the PROBE, not the product: a `.text()` mutation that failed because jsdom lacks the method, a
   drive that compared a raw byte count against a **localised** one (`"15,328"`), and a drive that
   pressed a key on a screen that had not finished loading.
6. ⚠️ **macOS COMPOSES Option+<letter>.** Drive every Alt binding with the harness's `altMac("x")`,
   never `press("Alt+x")`. Every Alt binding in this product was once found dead on macOS while every
   browser test passed.
7. ⚠️ **DRIVE ON A THROWAWAY COMPANY, NEVER `Demo Traders`**, and **tear it down**.
   `drive-t32-import-screen.mjs` and `drive-b1-pdf-export.mjs` are the copy-mes — each creates a
   tenant and drops it with a catalog-derived sweep. Residue drops `verify:demo` below 34/34.
8. **Assert a PROPERTY, not a list.**
9. ⛔ **VERIFY, DO NOT INHERIT — including this file.** Every claim above has a command beside it.
   Run them, **with `/usr/bin/grep`** (§0.1 item 1).

### 4.1 The mutation harness — worth rebuilding, it is ~120 lines

A python script (kept in the session scratchpad, **not committed**) holding a table of
`(name, file, anchor, replacement, test files, predicted-RED, predicted-GREEN)`. Per entry it:

1. **asserts the anchor occurs exactly once**, aborting loudly otherwise (rule 3);
2. writes the mutation and **asserts it applied**, printing the file and byte delta;
3. runs the named tests and reports failures **by name**;
4. reverts and **asserts the file is byte-identical**;
5. **compares predicted against observed** and prints `PROVED` or `MISMATCH`.

**17 mutations ran last session and all 17 ended up matching their predictions** — after two of the
predictions were themselves found wrong, which is exactly why they are written down first.

---

## 5. WHEN EACH ONE LANDS

- Fold it into `HANDOFF.md` (**§1.21** onward — T32 took §1.18, B1 §1.19, track C §1.20), flip its
  `HANDOFF.md` §1.11 row (C is row 7, so **offline entry is row 8** and B2 lives in row 9), and update
  the FeaturesScreen row
  **in the same change that makes it true** — the screen's own rule: a row flips when an operator gets
  a SCREEN, not when they got a route.
- Add a `docs/RELEASE-NOTES.md` note leading with what an operator will act on.
- ⛔ **SHRINK THIS FILE as tracks land.** `HANDOFF.md` §11's rule is *update in place rather than write
  another*, and its evidence is twelve superseded files that each re-derived context the last already
  held. **Delete a spent track's section; do not leave it as prose.** (This file was renamed from
  `HANDOFF-THE-NEXT-FOUR.md` when the fourth track landed — do the same when it is down to one.)

### Still blocked on a human, not on us

- **The GST-registered Tally XML export** — see the box at the top. One e-mail.
- **IRP / GSTN / e-Way Bill sandbox credentials** — the **Rule 48(4) QR code** row cannot be built
  without them. If they arrive, the IRN screen stops being groundwork and becomes a filing, and the
  e-way bill becomes real **with no change to either payload builder**. Re-order immediately.

### The rest of the backlog, after these three

`Multi-currency (T8)` → `Budgets (T11)` → `Interest on overdue bills (T13)` → `GSTR-9 (T12)` →
`SSO/OIDC last`. `HANDOFF.md` §5.4 states the one thing not to get wrong in each. ⚠️ **GSTR-9 is worth
deferring until real GST data has been through the importer** — it is an annual return and it reads
everything the untested tax paths produce.
