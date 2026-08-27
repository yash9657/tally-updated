# HANDOFF — THE LAST ONE

**Updated:** 2026-08-27 (late) · **Branch:** `session/t32-and-close-7-3` · **Tree:** clean
**Tracks C and B2 both landed** — `HANDOFF.md` **§1.20** and **§1.21**. ⭐ **ONE track left: D,
the offline entry screen (§3).** It is the only one of the four that is a DESIGN problem rather
than a typing problem, so §0 below is now mostly about not re-paying hazards.
**Read time:** ~12 minutes. Every claim was MEASURED, most of it re-verified 2026-08-26/27; each has
the command beside it. ⛔ **§0.1 is the part to read first — eight of its nine items cost real time.**

---

> # ⭐ RESUMING COLD? READ THIS BOX, THEN §0, THEN §3. NOTHING ELSE IS URGENT.
>
> **Where the tree is.** Branch `session/t32-and-close-7-3`, and the last two commits are the two
> tracks that just shipped:
>
> ```
> HEAD     feat(B2): Alt+M saves the PDF and hands your OWN mail client a draft
> HEAD~1   feat(C):  Gateway > Print Templates writes a company's own layout   (e5a3e1c)
> ```
>
> (⚠️ B2 is named by position rather than by SHA on purpose: this box ships INSIDE that commit, so
> any hash written here is invalidated by the amend that would be needed to correct it. `git log
> --oneline -3` is the authority.)
>
> **What is DONE.** Four of the four tracks this file's ancestors tracked (`T32`, `B1`, `C`, `B2`) are
> shipped, driven in a browser, and folded into `HANDOFF.md` §1.18–§1.21. **Nothing from them is
> outstanding.** ⛔ Do not re-open the two decisions in the box below; both shipped.
>
> **What is LEFT, and it is one thing:** **track D, the offline entry screen — §3.**
>
> ⛔⛔ **D IS NOT A TYPING TRACK AND THIS IS THE ONE THING TO GET RIGHT ABOUT IT.** The server engine
> is complete (`POST .../sync/commit`, the cloud sequencer, HLC timestamps, per-intent idempotency,
> conflict/freeze handling) and there is **NO CLIENT AT ALL** —
> `/usr/bin/grep -rln "sync/commit\|syncCommit" frontend/src/` is **empty**. So the work is a DESIGN
> problem: **§3.2's four questions must be answered before any code is written.** If you find yourself
> building a queue before answering them, stop.
>
> ⚠️ **AND KNOW THE HONEST LIMIT BEFORE YOU START.** A service worker and an offline app shell are a
> real piece of work SEPARATE from the queue screen. It is legitimate — and probably right — to ship
> the queue + reconciliation UI first, which is useful the moment a commit fails or a connection drops
> mid-session, and leave the FeaturesScreen row honest about the shell. **Say which you did.**
>
> ### The first ten minutes, in order
>
> | # | Do this | Why |
> |---|---|---|
> | 1 | `git log --oneline -3` · `git status --porcelain` | Confirm you are at the commit above with a clean tree |
> | 2 | Check the ports **and the AGE of `cored`** (§0) | ⛔ A stale `cored` was found **twice** in the last session — once at start, once mid-session, where it cost a whole failed browser run showing 404s for routes that existed. `make run-cored` is `go run`, so restarting costs one second |
> | 3 | Re-measure the gate table in §0 | It is the baseline you will be judged against; do not assume it |
> | 4 | Read **§0.1** — nine hazards, eight of which cost real time | Items 6–9 are new this session and every one was paid for in a failed probe |
> | 5 | Read **§3** and answer **§3.2's four questions** in writing | This is the track. The answers are the deliverable before the code is |
>
> ### What is still blocked on a HUMAN, not on us
>
> ⛔ **A real GST-REGISTERED TallyPrime XML export** (`HANDOFF.md` §10 item 1) — still the commercial
> blocker, still one e-mail. `classifyTaxLedger`, `inferRate` and the GSTR-1 B2B join have **never met
> real GST data**; the only real file the importer was fixed against is a grain trader, GST-free and
> zero-rated. **Do not touch those three until it arrives** — speculative changes destroy the evidence
> the file would give you. The `Alt+O` import screen (§1.18) turns receiving it from an afternoon into
> ten minutes.
>
> ⛔ **IRP / GSTN / e-Way Bill sandbox credentials** (§10 item 4). If they arrive, the IRN screen stops
> being groundwork and becomes a filing, and the e-way bill becomes real **with no change to either
> payload builder**. Re-order immediately. ⚠️ Note the Rule 48(4) QR row is also what
> `VoucherScreen.stockVoucher.test.tsx`'s pinned "Not built" assertion now names — **whoever makes it
> true must re-point that assertion in the same change** (§1.21.6 in `HANDOFF.md` explains why it was
> pointed there).

---

> ## ✅ BOTH BLOCKING DECISIONS WERE MADE (user, 2026-08-26) AND BOTH ARE NOW SHIPPED.
>
> Kept as the record of what was decided and why, so nobody reopens either.
> **Neither applies to track D** — D's own open questions are §3.2.
>
> | Decision | Answer | What shipped |
> |---|---|---|
> | **Does this product send mail?** | ⭐ **NO — a `mailto:` HANDOFF.** The PDF downloads, then the operator's own mail client opens on a pre-subjected draft. They attach the file and send. | `Alt+M` (§1.21). **No SMTP, no `Sender`, no attachment concept, no `Kind`, no bounce story, no migration** — `internal/notify` was **not touched at all**. ⚠️ **ONE CORRECTION TO THIS ROW: the draft is NOT "pre-addressed".** No party e-mail address exists anywhere in the schema (measured), so it opens with an empty `To:` and the screen says why. |
> | **The invoice logo** | ⭐ **SHIP THE FOUR FIELDS THAT EXIST; RENAME THE ROW to drop "logo".** | Gateway > Print Templates (§1.20). No image storage, no upload route, no size/type policy, **no change to the PDF renderer** — and the row now names what exists. |
>
> ## THE ONE THAT IS LEFT
>
> | # | Track | Size | Why here |
> |---|---|---|---|
> | **1** | **D — offline entry screen** (§3) | ⛔ large, and it is **thinking, not typing** | The engine is complete and there is **no client at all**. ⛔ **Four questions must be answered before any code** — §3.2 |
>
> ✅ **C AND B2 ARE BOTH SPENT.** C: `HANDOFF.md` §1.20, F11 row renamed to drop "logo",
> `drive-c-print-templates.mjs` 33/33. B2: `HANDOFF.md` §1.21, `Alt+M`, the pinned test re-pointed at
> the Rule 48(4) QR row, `drive-b2-email-document.mjs` 20/20. §§1–2 below are a marker; both sections
> are deleted.
>
> ⛔ **BUDGET THE THINKING, NOT THE TYPING.** D is not a small track and it is not mostly code. If you
> find yourself writing a queue before answering §3.2, stop.

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

**The gate baseline, RE-MEASURED 2026-08-27 after C **and** B2 — these are the numbers to beat:**

| Gate | Command | Value now |
|---|---|---|
| backend build + vet | `cd backend && go build ./... && go vet ./...` | clean |
| backend tests | `cd backend && go test ./... -timeout 60m` | **exit 0** |
| typecheck | `cd frontend && pnpm exec tsc --noEmit` | clean |
| frontend tests | `cd frontend && pnpm vitest run` | **1690 pass / 99 files** |
| live demo | `cd frontend && pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT |
| corpora | `python3 corpora/validate.py` | OK (migration=6, inventory=11) |
| everything | `make verify` | exit 0 |
| T32 drive | `node frontend/scripts/drive-t32-import-screen.mjs` | **25/25**, tenant torn down |
| B1 drive | `node frontend/scripts/drive-b1-pdf-export.mjs` | **15/15**, tenant torn down |
| C drive | `node frontend/scripts/drive-c-print-templates.mjs` | **33/33**, tenant torn down |
| B2 drive | `node frontend/scripts/drive-b2-email-document.mjs` | **20/20**, tenant torn down |

⚠️ **`make verify` DOES NOT RUN `tsc`.** Different gates, and each has caught a real error the other
missed — including last session, where a new test file was green in vitest with **8 real `tsc`
errors**.

### 0.2 What landed immediately before this file

| Commit | What |
|---|---|
| `05c9775` | **A raw NUL byte** in `mock.ts` had made 8,672 lines **invisible to grep**. §0.1 item 1 |
| `d60546c` | **T32** — `Alt+O` and `Gateway > Import` import a TallyPrime export. `HANDOFF.md` **§1.18**. No Go code changed |
| `13e0ba8` | **B1** — F12 offers "Export (Alt+E) as PDF"; Alt+E **saves** it (Ctrl+P still **opens** it). `HANDOFF.md` **§1.19** |
| `e5a3e1c` | **C** — Gateway > Print Templates writes a per-company layout; the F11 row is RENAMED to drop "logo". `HANDOFF.md` **§1.20** |
| _this_ | **B2** — `Alt+M` saves the PDF and opens the operator's OWN mail client. No Go code. `HANDOFF.md` **§1.21** |

⛔ **Read `HANDOFF.md` §1.17's *"three rules this lane paid for"*, §1.18's *"three things the mutation
proofs found"*, and §0.1 below before writing a probe.** Between them they cost four sessions.

---

## ⛔ 0.1 NINE HAZARDS ALREADY PAID FOR. READ THIS OR RE-PAY THEM.

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
that is not. ✅ **B2 shipped without needing either** — it hands the `File` nowhere and reads no
bytes in the browser; the PDF goes straight from the client to `downloadBlob`. Keep this in mind
if D's queue ever holds attachments.

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
  ✅ **B2 used it and wrote no fourth** — `useEmailAction` calls `downloadBlob` and nothing else.
  ⛔ **Do not write a fourth.**
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

### 8. ⛔ A BROWSER'S EVENT ORDER IS **NOT** YOUR CODE'S CALL ORDER

B2's drive asserted that Chromium's `download` event arrived before the `mailto:` request, and it
**failed against provably correct code**. Chromium emits a `mailto:` navigation request synchronously
from the renderer, while a `download` has to cross to the browser process and start writing a file —
so `["draft", "file:…"]` is what a CORRECT build produces there.

⛔ **"FIXING" THE PRODUCT TO SATISFY THAT WOULD HAVE BEEN REORDERING CORRECT CODE TO PLEASE A BROKEN
ORACLE.** Assert strict ordering where it is deterministic — in vitest, with both sinks pushing to one
synchronous array — and let the browser prove only that each effect really happens.

⚠️ **AND `location.href` CANNOT BE STUBBED IN CHROMIUM.** It is `[LegacyUnforgeable]`, so
`Object.defineProperty(window.location, "href", …)` throws *"Cannot redefine property"* — measured.
What DOES work, and is better, is `page.on("request")`: a `mailto:` assignment fires a genuine request
event carrying the full URL, so a drive can assert the real encoded URL rather than a stub.

### 9. ⛔ `buttons.test.ts` CANNOT SEE A DECORATIVE **GLOBAL** KEY ON A BAR

Adding `item("Alt+M", "E-Mail")` to the TDS challan bar — a screen with no printable document, which
is why it carries no Ctrl+P — left the whole suite **GREEN**. "No advertised button is decorative"
checks that a bar's combo RESOLVES to a built action and is a known side effect, and a GLOBAL binding
in the side-effect list satisfies both on **every** screen.

⚠️ **Ctrl+P HAS THE IDENTICAL HOLE AND ALWAYS HAS**, and so will any future global that is also a
screen action. If a track adds one, assert its bar placement DIRECTLY as a property — B2's
`emailDocument.test.tsx` reads the screen list from `corpora/validate.py`'s `KNOWN_SCREENS` so a
screen added later cannot escape the invariant.

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

# §§1–2. ✅ TRACKS C AND B2 — BOTH SPENT. THEY SHIPPED 2026-08-27.

Folded into `HANDOFF.md` **§1.20** (C) and **§1.21** (B2), which carry the whole of both. These
sections are deleted per §5's rule rather than left as prose. What generalises is already in §0.1
below; in one line each:

**C — per-company print templates.** The field grid is **RAGGED** (`RenderReport` reads 3 of the 7
overridable fields), so a form built from the obvious reading stores a declaration no statement will
ever print, with no error anywhere. The golden that decides which fields bite is **measured by
rendering** and needs **no Postgres**, so it fixes §0.1 item 4's skip hazard rather than inheriting
it. The oracle was the **printed PDF**, never the stored row. The logo was cut and the F11 row
renamed, because the schema has no image storage anywhere.

**B2 — e-mail a document.** `Alt+M`, its own key rather than a fourth `ExportTarget`, because Tally's
top menu carries `E: Export` and `M: E-mail` separately. **A web page cannot attach a file to a
`mailto:` draft**, so the one failure mode is a draft opening with no file saved — hence the proof is
an **ORDERING** test, and no draft opens at all if the save fails. ⚠️ **The plan's "pre-addressed"
draft was wrong**: no party e-mail exists anywhere in the schema, so there is no recipient and the
screen says so.

⛔ **THE NUMBERING SKIPS TO §3 ON PURPOSE.** Track D's own text cross-references §3.2, so it keeps its
section number; renumbering it to §1 would break every internal pointer in the one section still live.

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

- Fold it into `HANDOFF.md` (**§1.22** onward — T32 took §1.18, B1 §1.19, C §1.20, B2 §1.21), flip
  its `HANDOFF.md` §1.11 row (C is row 7 and B2 row 8, so **offline entry is row 9**), and update the
  FeaturesScreen row
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
