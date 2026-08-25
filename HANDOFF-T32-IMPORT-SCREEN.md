# HANDOFF — THE MIGRATE-FROM-TALLY SCREEN

**Written:** 2026-08-25 · **Branch:** `session/t32-and-close-7-3` · **Tree:** clean at `b5c61b7`
**Read time:** ~10 minutes. Everything below was MEASURED today; every claim has the command beside it.

> ## ⛔⛔ THE FOUR THINGS THAT CHANGE WHAT YOU BUILD. READ THE BOX, THEN §2.
>
> | # | Measured fact | What it decides |
> |---|---|---|
> | 1 | **Every real TallyPrime export is UTF-16LE.** `file -b corpora/migration/06-*.xml` → *"Unicode text, UTF-16, little-endian, with CRLF"*. And `CoredClient.importTallyXML` declares its body as **`xml: string`** (`api/client.ts:1288`) | ⛔ **`await file.text()` DECODES UTF-16 BYTES AS UTF-8 AND DESTROYS THE FILE.** The server's BOM sniffer (`integration/tallyxml.go:951`) then never fires, because what arrived is UTF-8-encoded mojibake. **Send the `File` object itself.** `http.postRaw` already takes `BodyInit`, so this is a *type* change, not a plumbing one — §3. |
> | 2 | **`Alt+O` IS Tally's own Import key** (`RESEARCH.md:415`, `:1221` — *"Gateway of Tally → Import (Alt+O in TallyPrime)"*) and it is bound in this build **ONLY on the `bank-reconciliation` screen**, whose own comment says it is *"Tally's documented global for import; scoped to this screen because a statement belongs to ONE bank account"* | The global `Alt+O` is **free and is the right key**. `resolveAction` (`keymaps/tallyprime.ts:915`) checks `screenBindings[screen]` **first**, so a global binding cannot break bank reconciliation. Do not invent a key. |
> | 3 | **The mock already implements `importTallyXML` and returns honest ZEROES** — it does not parse XML at all (`api/mock.ts`, the `async importTallyXML` block) | Screen tests that need a populated report must **inject a stub client**, exactly as `DataScreen.test.tsx` does. Do not teach the mock to fake an import: a second XML parser in TypeScript is a thing no importer has ever read. |
> | 4 | **`vouchers_posted` cannot tell you the migration worked.** The response carries `stock_lines`, `opening_positions` and four `*_ensured` arrays precisely because **a trial balance cannot see quantity** (`server.go:3509` says so at length) | ⛔ The screen's job is not "show a success toast". It must put `stock_lines` and `opening_positions` **next to** the voucher count, and treat `opening_positions: 0` with non-zero `stock_lines` as a WARNING. §4. |

---

## 0. RUN-FIRST — the stack, and the numbers to beat

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
lsof -nP -iTCP:8087 -sTCP:LISTEN -t   # and 5173, 5433
```

⛔ **SYMBOL-CHECK THE RUNNING `cored` BEFORE BELIEVING ANY DRIVE.** `make run-cored` is `go run`, so a
stale process is indistinguishable from a fix that does not work. Nothing in this track changes the
backend, so the check is that it is *recent*, not that it holds a new symbol:

```bash
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"
```

⛔ **VITE MUST BE ON `:5173`.** WebAuthn origin matching is exact; a vite that took `:5174` signs in
fine and then fails every write with `PASSKEY STEP-UP FAILED`.

**The gate baseline, measured 2026-08-25 at `b5c61b7` — these are the numbers to beat:**

| Gate | Command | Value now |
|---|---|---|
| backend build + vet | `cd backend && go build ./... && go vet ./...` | clean |
| backend tests | `cd backend && go test ./... -timeout 60m` | **exit 0** |
| typecheck | `cd frontend && pnpm exec tsc --noEmit` | clean |
| frontend tests | `cd frontend && pnpm vitest run` | **1580 pass / 94 files** |
| live demo | `cd frontend && pnpm verify:demo "$(cat .demo-company-id)"` | **34/34** ⚠️ takes the id as an ARGUMENT |
| corpora | `python3 corpora/validate.py` | OK (migration=6, inventory=11) |
| everything | `make verify` | exit 0 |

⚠️ **`make verify` DOES NOT RUN `tsc`.** They are different gates and each has caught a real error the
other missed.

---

## 1. WHAT THIS TRACK IS, AND WHY IT IS THE NEXT ONE

`POST /v1/companies/{id}/import/tally-xml` has existed since the migration lane. It parses a real
TallyPrime envelope, ensures groups/ledgers/stock items/units/godowns/batches, posts every voucher
through the **foundation ledger path** (the sole writer), routes `ACTION=Alter|Delete` by Tally GUID
so a re-sync amends rather than duplicates, and imports the **goods**.

**There is no screen.** `grep -rn 'tally-xml' frontend/src/ | grep -v test` returns three hits and
all three are in `api/` — the client method exists and **nothing calls it**. An operator cannot
migrate their own books from the keyboard; migrating means `curl`.

That is this repository's most expensive recurring bug, and it has now happened three times:

- **order processing** — whole backend, no screen, FeaturesScreen row said "Not built";
- **payroll** — complete engine, two routes missing, every pay run answered 422 (`HANDOFF.md` §1.15);
- **T14 backup** — the census existed in `testdb` and nothing in the product could reach it.

⛔ **`HANDOFF.md` §1.15's rule, which this track exists to satisfy: A ROUTE TABLE IS NOT A SURFACE.**

### Why now, specifically

The real Tally XML export for a **GST-registered** company has not arrived yet. That is fine and it
is the point: this track is **not blocked on it**, and it is what makes that file usable the hour it
lands — drag it in, read the report, instead of hand-rolling a `curl` with the right content type.

⚠️ **AND KNOW WHAT THE PENDING FILE WILL TEST.** The real export this importer was fixed against is a
**grain trader — GST-free and zero-rated**. So `classifyTaxLedger`, `inferRate` and the GSTR-1 B2B
join have **never met real GST data**. Do not "fix" them speculatively in this track; build the screen
that will show you what they do.

---

## 2. THE GROUND TRUTH — measured 2026-08-25, with the commands

Re-run every one of these before you trust them (§8 rule: **VERIFY, DO NOT INHERIT** — this file
included).

### 2.1 The route, and its registration (all four touches already exist)

| Fact | Value | Command |
|---|---|---|
| route | `POST .../import/tally-xml` | `server.go:1324` |
| `routeAction` arm | `(authz.Create, "import")` | `auth_middleware.go:2064` |
| vocabulary | `"import"` is in `KnownResources` | `authz/vocabulary.go` |
| body cap | **128 MiB**, and `bodyCapFor` matches this route by name | `server.go:121`, `:3932` |

⛔ **NOTHING ON THE SERVER NEEDS TO CHANGE.** This is a frontend track. If you find yourself editing
`internal/server`, stop and re-read — the one exception is §6's optional item.

### 2.2 Who may use it

```
psql … -tAc "SELECT sl.name FROM acl_rule r JOIN security_level sl ON sl.id=r.security_level_id
             WHERE r.resource='import'"      -> EMPTY
```

**No seeded level names `import`**, and Data Entry's `default_access` is `'none'`, so the route is
**default-DENIED for every seeded non-Owner level** and an Owner is allowed. That is the right shape
and it needs **no ACL migration**. ⛔ The screen must render the 403 as **the decision it is**, the way
`TDSChallansScreen` does — not as a bug.

⚠️ It is a **write** (`authz.Create`), so it inherits the Owner level's **WebAuthn step-up** and the
**back-dated cutoff**. `TestTheCutoffSeesEveryTallyFileTheImporterCanRead` and
`TestBackDatedCutoffRefusesAUTF16TallyImportIntoAClosedPeriod` already pin the second. **A refusal
because the file contains a voucher before the cutoff is a REAL answer the screen must show.**

### 2.3 The response — and the four figures that matter

`ImportTallyXMLResponse` (`api/types.ts:1887`) is already declared, already correct, and its doc
comments already carry the argument. The fields:

```
source_company                                   vouchers_posted / _altered / _deleted / _skipped
groups_ensured[]  ledgers_ensured[]  warnings[]  posted[]
stock_lines  opening_positions
stock_items_ensured[] units_ensured[] godowns_ensured[] batches_ensured[]
```

⛔ **`warnings` IS PART OF THE RESULT, NOT A LOG LINE.** `server.go:3483` says why: the importer
carries on past what it cannot represent exactly — an opening bill-wise breakup that does not add up,
a ledger whose Tally group is not in the file — and *"an operator who is told only '121 vouchers
posted' will believe the migration was lossless when parts of it were not."*

### 2.4 The fixtures you will drive with

```
corpora/migration/01-opening-trading.xml            ASCII
corpora/migration/02-multistate-gst.xml             ASCII
corpora/migration/03-billwise-outstandings.xml      ASCII
corpora/migration/04-edited-deleted.xml             ASCII
corpora/migration/05-rounding-edge.xml              ASCII
corpora/migration/06-tallyprime-real-shapes.xml     ⛔ UTF-16LE + BOM + CRLF
corpora/migration/07-tallyprime-inventory-shapes.xml ⛔ UTF-16LE + BOM + CRLF
```

⛔ **DRIVE AND TEST WITH 06 OR 07, NEVER WITH 01–05.** The ASCII fixtures pass whether or not you got
the encoding right, so a probe built on them **cannot see the defect in box item 1** — which is this
track's whole technical risk. That is the inventory lane's rule 2 (`HANDOFF.md` §1.16) applied here:
*an oracle that cannot see the defect is not an oracle.*

---

## 3. ⛔ SLICE 0 — THE UPLOAD MUST CARRY BYTES, NOT A STRING

### 3.1 The defect, before you write the screen

`CoredClient.importTallyXML(companyID: string, xml: string, …)` takes a **string**. A `File` picked in
a browser is bytes. The obvious screen code is:

```ts
const xml = await file.text();          // ⛔ DECODES AS UTF-8
await client.importTallyXML(companyID, xml);
```

For `06-tallyprime-real-shapes.xml` that reads UTF-16LE bytes as UTF-8, produces a string full of
replacement characters and interleaved NULs, and `fetch` then re-encodes **that** as UTF-8. The server
receives bytes with no recognisable BOM and no `<` in the first position, so
`decodeTallyBytes` (`integration/tallyxml.go:951`) passes them through untouched and the parse fails —
**on the only file shape a real customer will ever send.**

### 3.2 The change

`http.postRaw` already declares `body: BodyInit` (`api/http.ts:68`) and `rest.ts:1031` passes it
straight through, so **the plumbing is already correct**. Widen the *type* in three files — declare it
in `client.ts` first and let the other two fail to compile, which is the pattern this repo uses on
purpose:

- `api/client.ts` — `xml: string | Blob`
- `api/rest.ts` — no body change needed once the type widens
- `api/mock.ts` — the parameter is already `_xml` and unused

### 3.3 The proof for slice 0

⛔ **A unit test that round-trips the REAL encoding.** Read `corpora/migration/06-*.xml` as bytes,
wrap it in a `File`, hand it to a stub client, and assert **the bytes that reach `postRaw` are
byte-identical to the file on disk** — specifically that they still begin `FF FE`.

⛔ **MUTATION:** change the screen to `await file.text()` and confirm that test goes red **and** that
the same test built on `01-opening-trading.xml` stays green. Both halves must be executed. The second
half is what proves the fixture choice is load-bearing rather than incidental.

---

## 4. THE SLICES, IN ORDER

Each slice ends green and useful. **Do not start slice 3 until slice 2 is driven in a browser.**

### Slice 1 — the screen, and the key

`Alt+O` (Tally's own, and free globally — box item 2) opening a **`tally-import`** screen. Full
registration surface in §5.

The screen is a file picker, an import button, and a report. It is closer to `DataScreen.tsx` than to
any voucher form: **it has nothing to "accept"**, so do not offer `Ctrl+A`.

⚠️ **REUSE THE FILE-INPUT PATTERN, DO NOT INVENT A THIRD.** `frontend/src/screens/DataScreen.tsx`
(T14) has one, including the `ev.target.value = ""` reset that makes re-picking the same file after a
failure fire again — which is the most natural thing an operator does after a refusal.

### Slice 2 — the report, and the two numbers that are the point

⛔ **`vouchers_posted` ALONE IS THE FAILURE MODE THIS SCREEN EXISTS TO PREVENT.** Render, together:

| Show | Because |
|---|---|
| `vouchers_posted` / `_altered` / `_deleted` / `_skipped` | the accounting |
| **`stock_lines`** | ⛔ the ONLY figure that distinguishes *"imported the accounting"* from *"imported the trade"*. Zero on a file full of invoices is the tell |
| **`opening_positions`** | ⚠️ zero **alongside** non-zero `stock_lines` is a WARNING, not a clean result: the export carried no opening stock, so every position starts at nil |
| `warnings[]` | in full, never a count. §2.3 |
| the four `*_ensured` arrays | what was CREATED rather than merely referenced |

⛔ **AND THE SCREEN MUST SAY WHAT A CLEAN-LOOKING REPORT STILL DOES NOT PROVE.** The FeaturesScreen
row already carries the sentence and it belongs on this screen too: *never treat an empty warning list
as the only thing to check, and never treat a balanced trial balance as proof the migration worked —
check the Stock Summary.*

### Slice 3 — the refusals, verbatim

Every one of these is a real answer, not an error state, and each names the fact that is wrong:

- **the whole-import abort**: an export with **no `STOCKITEM` masters carries no opening stock**, so
  the first sale of goods bought before the export window has nothing to issue and the **entire import
  is refused**, naming both ways forward. The importer will not invent an opening position and will
  not switch this company's negative-stock setting on its behalf.
- **403** — this level may not import (§2.2). Render it as the decision it is.
- **the back-dated cutoff** — the file contains a voucher before the closed period (§2.2).
- **413** — over 128 MiB.

⛔ **RENDER THE SERVER'S SENTENCE.** `DataScreen.tsx`'s `messageOf()` is the pattern and its comment is
the argument: a client that replaces a refusal with "import failed" takes away the one thing the
operator needs to know what to do next.

### Slice 4 — the FeaturesScreen row

`frontend/src/screens/FeaturesScreen.tsx:518` currently reads **`state: "server"`** — *"POST
.../import/tally-xml, and there is NO SCREEN for it — an operator cannot migrate their own books from
the keyboard, which is why this row says Server only."*

Flip it to `on`, **delete that clause**, and name the key. ⚠️ Everything else in that (very long) note
is still true and hard-won — **do not rewrite it wholesale**; edit the one clause that stops being
true and leave the rest.

⚠️ **CHECK THE PINNED "still not built" ASSERTION.** `VoucherScreen.stockVoucher.test.tsx:967` pins
`/^Export: e-mail a document/` as its Not-built row and asserts `getAllByText("Not built").length > 1`.
This change flips a **`server`** row to `on` and removes no Not-built row, so both hold — **verify
that rather than assuming it**, the way T14 did.

---

## 5. THE REGISTRATION SURFACE — miss one and the key ships COMPLETELY DEAD

`HANDOFF.md` §7.2 is the authority. **T14's screen commit `b5c61b7` is the worked example — read its
diff, it touches exactly these files.**

| # | File | What |
|---|---|---|
| 1 | `keystroke/types.ts` | `ScreenKind` union — add `"tally-import"` (⚠️ **here, not `screenRegistry.tsx`**) |
| 2 | `keystroke/types.ts` | `KeymapAction` union — add `{ kind: "tallyImport" }` |
| 3 | `keystroke/keymaps/tallyprime.ts` | the **global** `"Alt+O": { kind: "tallyImport" }` |
| 4 | `keystroke/keymaps/tallyprime.ts` | `actionLabel()` case (⚠️ **here, not `AppShell.tsx`**) |
| 5 | `keystroke/navMachine.ts` | `case "tallyImport": return pushOnce("tally-import")` |
| 6 | `keystroke/buttonBar.ts` | a bar for the screen |
| 7 | `app/AppShell.tsx` | the **company-scoped** set — omit it and `useCompanyID()` THROWS before the screen can render anything |
| 8 | `app/AppShell.tsx` | the title case (`default` is "Tally", which is silently wrong) |
| 9 | `app/AppShell.tsx` | the hint strip case |
| 10 | `app/defaultScreens.tsx` | the adapter + the registry entry |
| 11 | `corpora/validate.py` | **`KNOWN_SCREENS`** (line ~66) — ⛔ **outside `frontend/` entirely**, and a `ScreenKind` it does not list **fails `make verify`**. ⚠️ CO-OWNED: **ADD only, and re-read it immediately before editing** |

⛔ **THREE OF THESE SHIP THE KEY DEAD WITH NO ERROR AND NO STATUS LINE** — the keymap binding (#3), the
navMachine case (#5) and the registry entry (#10). T14 mutation-proved each **separately**; do the
same. An unbound key is not inert: F5 opens a Payment over your form, F9 a Purchase.

⚠️ **`SCREEN_ACTIONS` in `AppShell.tsx` is NOT needed here.** That map is for actions the *active
screen* owns (Alt+O on bank reconciliation is one). Opening a screen is a **navigation** action and
goes through the navMachine. Adding it to `SCREEN_ACTIONS` would be wrong.

### 5.1 How is the screen REACHED besides the key?

⛔ **`Alt+G` Go To IS NOT A DOOR.** The picker is hard-wired to `["Vouchers", ...REPORT_NAMES]`, so
Cost Centres, Bank Reconciliation, TDS Challans, the order registers and the BOM screen are all absent
from it and present on the **Gateway** — and two audit rows once named the wrong key and were logged as
unreachable (`audit-harness.mjs`, `openFromGateway`).

Tally's own path is **`Gateway of Tally → Import`** (`RESEARCH.md:1221`), so **add a Gateway entry**
(`navMachine.ts` has a `*_LABEL` const per entry — `BANK_RECON_LABEL`, `TDS_CHALLANS_LABEL`, … — follow
that pattern). T14's Data screen deliberately has **no** Gateway entry because `Alt+Y` is global in
Tally too; this one should have both, because Tally has both.

---

## 6. WHAT NOT TO DO

1. ⛔ **Do not teach the mock to parse Tally XML.** It returns honest zeroes and says so in a comment.
   A second XML parser in TypeScript is unvalidated bytes a test could pin, and the real one is
   `internal/integration`.
2. ⛔ **Do not add a progress bar you cannot honour.** The route is a single POST that returns when the
   whole import has committed; there is no progress channel. A spinner is honest, a percentage is not.
3. ⛔ **Do not parse or validate the XML in the browser** to "fail fast". The encoding sniffing, the
   envelope nesting (`IMPORTDATA/REQUESTDESC/REQUESTDATA`), the dated GST registrations and the
   `ACTION=Alter|Delete` GUID routing are all server-side and hard-won. A client-side pre-check is a
   second implementation that will disagree.
4. ⚠️ **Do not change `internal/server`.** The only defensible backend edit in this track is *optional*
   and is a `created_by` default; the route already reads it from the query string.
5. ⛔ **Do not touch `classifyTaxLedger` / `inferRate` / the GSTR-1 B2B join.** They are untested
   against real GST data and the file that will test them is not here yet. Speculative changes now
   destroy the evidence that file is going to give you.

---

## 7. DEFINITION OF DONE

`HANDOFF.md` §12 in full, plus these four:

1. ⛔ **THE UTF-16 PROOF, BOTH HALVES** (§3.3): a test on `06-*.xml` that goes red under
   `await file.text()`, **and** the same test on an ASCII fixture that stays green under it.
2. ⛔ **BOTH PROOFS, as always.** An EXECUTED mutation proof whose failure was **predicted, then
   observed** — one per dead-key registration (§5) — *and* a drive in a real browser.
3. ⛔ **A BROWSER DRIVE THAT IMPORTS THROUGH THE SCREEN.** ⚠️ `frontend/scripts/drive-tally-import.mjs`
   already exists and imports **over HTTP** before looking at the Stock Summary — it does **not** drive
   an import screen, because there was none. Copy its structure (it creates its own tenant); make the
   new one `setInputFiles` the UTF-16 fixture **through the screen** and read `stock_lines` and
   `opening_positions` **off the page**.
   ⛔ **RUN ON A THROWAWAY COMPANY, NEVER `Demo Traders`** — an import moves a trial balance, and
   residue drops `verify:demo` below 34/34. T14's `drive-t14-data.mjs` **tears its tenant down**; copy
   that too.
4. ⛔ **THE FEATURES ROW FLIPS ON BROWSER EVIDENCE** (§4 slice 4), with the "no screen" clause deleted
   and the key named.

---

## 8. THE RULES THAT ARE NOT NEGOTIABLE

1. **Both proofs, for every fix.** An EXECUTED mutation proof whose failure was **predicted, then
   observed**, *and* a drive in a real browser.
2. ⛔ **Run every new probe against the BROKEN build first.** A probe that has never failed proves nothing.
3. ⛔ **A MUTATION THAT SILENTLY DOES NOTHING LOOKS EXACTLY LIKE A TEST THAT PASSED.** T14 burned two
   attempts guessing schema names; both "passed". Print something from inside a mutation.
4. ⚠️ **`macOS composes Option+<letter>`.** Drive `Alt+O` with the harness's `altMac("o")`, never
   `press("Alt+o")`. Every Alt binding in this product was once found dead on macOS while every browser
   test passed.
5. **Assert a PROPERTY, not a list.** Here the property is *"the bytes that left the browser are the
   bytes on disk"* — not "the import returned 200".
6. ⛔ **VERIFY, DO NOT INHERIT — including this file.** Every claim in §2 has a command beside it.

---

## 9. WHEN THIS LANDS

- Fold it into `HANDOFF.md` **§1.18** (T14 took §1.17), flip `HANDOFF.md` §1.11's row for T32, and
  **`git rm` this file** — it is built to be retired, exactly as `HANDOFF-T14-BACKUP-RESTORE.md` was.
- Add a `docs/RELEASE-NOTES.md` note leading with what an operator will act on: **`Alt+O` imports a
  TallyPrime XML export**, and **a clean-looking report is not proof — check `stock_lines` and the
  Stock Summary.**
- ⛔ **THEN ASK FOR THE GST-REGISTERED EXPORT AGAIN** (`HANDOFF.md` §10 item 1). It costs one email,
  it is the commercial blocker, and this screen is what turns it from an afternoon into ten minutes.

---

## 10. THE NEXT THREE AFTER THIS ONE

Measured from the FeaturesScreen's own `state` field today
(`grep -n 'state: "off"' frontend/src/screens/FeaturesScreen.tsx`):

| Next | Row | Why it is next |
|---|---|---|
| 1 | **Export: e-mail a document, and export to PDF** | Every business emails invoices daily. The PDF renderer, `GET /print/reports/{name}` and `usePrint.ts` already exist, so this is mostly surface |
| 2 | **Per-company print templates** (logo, address block, footer) | The override table exists and **nothing writes to it**; the first thing a customer asks after seeing their invoice |
| 3 | **Offline entry screen** (`HANDOFF-NEXT-BUILD.md` §5) | The differentiator against Tally's LAN-only model. Engine built (`internal/sync`, `POST /sync/commit`, HLC). ⚠️ It is a screen **plus a decision**: how does a browser present queued intents that have not been sequenced? |

⛔ **Still blocked on a human, not on us:** the **Rule 48(4) QR code** needs IRP credentials — same gate
as the GST export. Do not start it.
