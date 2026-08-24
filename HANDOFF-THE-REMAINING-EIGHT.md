# HANDOFF — THE REMAINING EIGHT: the register's last rows, in order, with the oracle for each

Written 2026-08-24, after **L17 closed D95** (the forecast plane's ACL counterweight, migration 0076).
This **supersedes `HANDOFF-CLOSE-THE-FINAL-NINE.md`**, whose position 1 is done. It is the DRIVER: what
to do next, in what order, what proves it, what will go wrong. Execute it top to bottom.

> ⛔⛔ **TWO ROWS IN HERE ARE MATERIALLY BIGGER OR DIFFERENT THAN THE REGISTER SAYS, AND BOTH WERE
> RE-SCOPED BY READING THE CODE RATHER THAN THE ROW.** D49 is **9 routes, not 7** (13 counting the
> auto-docs), and **D81's half B is not a "pop"** — it is a frame REPLACEMENT, it is already closed for
> ALTER frames, and it carries an **unanswered design question** (§6.7). §3 has both in full. **Read
> §3 before estimating anything.**

> ⚠️ **THE OPEN COUNT FINALLY FELL WITHOUT A NEW ROW APPEARING — the first lane in three.** Do not read
> that as the pattern changing: D95 was an ACL migration with the design already decided, which is the
> narrowest kind of lane there is. **L15 found 3 rows while closing 4. L16 found 3 while closing 6.**
> Plan for the next build-shaped lane to surface something.

---

## ⛔ THE COUNT, RE-DERIVED BY PARSING §7.1 — NOT COPIED FROM A HEADER

**96 registered · 88 CLOSED · 8 OPEN · 0 PARTIAL.** `88 + 8 = 96`.
(`93 · 81 · 8 · 4` before L16 · `94 · 85 · 9 · 0` after L16 p0 · `96 · 87 · 9 · 0` after L16 p1 ·
this after L17.)

```bash
python3 - <<'PY'
import io,re,collections
s=io.open("HANDOFF-PARITY-AUDIT.md",encoding="utf-8").read().split("\n")
a=[n for n,l in enumerate(s) if l.startswith("## 7.1")][0]
b=[n for n,l in enumerate(s) if n>a and l.startswith("### ⛔⛔ §7.2")][0]
c=collections.Counter(); rows={}
for l in s[a:b]:
    m=re.match(r'^\| \*?\*?(D\d+)\*?\*? \|', l)
    if not m: continue
    # ⛔ THE STATUS IS THE LAST CELL. Never scan the whole LINE — see below.
    cells=[x.strip() for x in l.strip().strip("|").split(" | ")]
    st_cell=cells[-1]
    st=("CLOSED" if "✅" in st_cell else "PARTIAL" if "PARTIAL" in st_cell
        else "OPEN" if "OPEN" in st_cell else "?")
    c[st]+=1; rows[m.group(1)]=st
print(c, "total", sum(c.values()))
print("OPEN   :", " · ".join(k for k,v in rows.items() if v=="OPEN"))
print("PARTIAL:", " · ".join(k for k,v in rows.items() if v=="PARTIAL") or "(none)")
print("?      :", " · ".join(k for k,v in rows.items() if v=="?") or "(none)")
PY
```

⛔⛔ **THIS SNIPPET READS THE STATUS COLUMN, NOT THE LINE, AND EVERY EARLIER VERSION WAS WRONG.** CLOSED
rows say "OPEN" in their prose (D28, D84); a registered row said "PARTIAL" in its prose; and two rows
written in L16 broke it again — one carried a literal ` | ` inside its status cell (parsed `?`), one said
"marked ✅" in prose (parsed CLOSED while reading OPEN). See **§5.21**.
⚠️ **VALIDATE ANY PARSER CHANGE BY RE-DERIVING THE OLD NUMBER FROM THE OLD FILE.** ⚠️ **And it prints a
`?` bucket on purpose** — a row matching nothing must be SEEN, not folded into a plausible total.

**OPEN (8):** D8 · D49 · D81 · D83 · D85 · D86 · D94 · D96
**PARTIAL: none**, and there should never be another: **`PARTIAL` is how a hidden build survives for
three sessions** (that was D13). A row is CLOSED, or OPEN, or TWO rows.

---

## THE FILES, AND WHICH ONE WINS

| File | Role |
|---|---|
| **this file** | ⛔ **the order, the state, the oracle per row, the traps.** Start here. |
| **`HANDOFF-RECON-LAST-16.md` §3** | the `file:line` ANCHORS for D8/D83/D85/D86. ⚠️ **D49's and D81's anchors in §3 below SUPERSEDE it** — they were re-verified at HEAD and the recon's are stale in three places (`PARITY-AUDIT.md:903`'s `routes_drafts.py:192-219`; RECON §3.10's `draftAccept.ts:1172` and `:1243`). ⚠️ **Its expected values have been wrong three times.** |
| `HANDOFF-PARITY-AUDIT.md` §7.1 / §7.3 / §7.5 | the register (re-parse it), the three non-register defects B/C/D (§8), and the drive ledger (append one section per lane) |
| `HANDOFF-CLOSE-THE-REGISTER.md` §4 + **§6 below** | ⛔ **the user's decisions. AUTHORITY, not suggestion.** Disagreeing is disagreeing with the USER — ask again, do not re-decide. ⚠️ **Unless the PREMISE is false — see §5.2 and D90.** |
| `HANDOFF-CLOSE-THE-FINAL-NINE.md` | ⚠️ the PREVIOUS driver. Position 1 (D95) done; **its §3 row 1 holds D95's full reasoning**. Trap list carried forward here in full. |
| `.claude/HANDOFF.md` | the SESSION handoff. ⚠️ **Gitignored — per-machine, never in a diff.** |

---

## 0. THE BAR. Every row owes BOTH proofs.

1. an **executed** mutation proof whose failure was **predicted then observed**, and
2. a drive in a **real browser** (or on the wire, for a server-side defect) against a cored binary
   **verified fresh BY SYMBOL**, with the **books read back** — assert SIDES and LEDGER NAMES,
   never totals.

Then: §7.1 row marked, §7.5 appended, committed. **One lane per commit.**

### ⛔⛔ COMMIT THE FIX **BEFORE** MUTATION-PROVING IT
A proof's own `git checkout --` reverts the file it mutated. On 2026-08-21 that wiped an uncommitted fix
which was then committed in its reverted state. `f48e353` exists purely to obey this rule. It has been
broken twice. **Commit, then prove.** (Held six times in L15, twice in L16, once in L17.)

### ⛔ AND MARK THE ROW IN §7.1 IN THE SAME PASS
L15 shipped D87's fix, tests, drive and §7.5 entry with the register row still reading `⛔ OPEN`.
**Re-parse §7.1 AFTER closing a row, and after WRITING one** — writing rows is what has broken the
parser twice (§5.21).

### ⛔ WRITE THE MUTATION'S PREDICTION DOWN BEFORE YOU RUN IT
L16 put both predictions in the drive's commit message and observed exactly them. L17's M1 confirmed its
primary prediction **and corrected a second one** — I predicted a test would fail on a row count; it
passed, because 13 of the 14 seeders run UNCONDITIONALLY after the guard and only the voucher classes sit
inside it. **The prediction is the experiment**, and without one a proof cannot correct you (§5.3).

### ⛔ AND ASK OF EVERY ARM: WOULD IT PASS **BEFORE** MY CHANGE? — §5.23
D95's stated oracle was **half vacuous and half impossible** and looked perfectly reasonable written
down. This is now the cheapest check in the file. Run it on every arm before writing it.

---

## 1. THE ORDER

Cost-and-dependency ordered. **Four hard constraints, and they are not preferences:**
**D49 → D85** (D85 changes insightd's wire shape; it must land on the FINAL transport) · **D86 → D94**
(both move the Outstandings report) · **D8 ALONE** (it re-prices the balance sheet) · **D83 LAST of the
builds** (the only genuinely concurrent probe).

| # | Row | Sev | Why here |
|---|---|---|---|
| **1** | **D81** | MED | **Scoped to two halves with two working shapes to copy** — but ⛔ **half B has an unanswered DESIGN question (§6.7): ASK BEFORE BUILDING.** Half A is buildable today. §3. |
| 2 | **D49** | MED | ⛔ **9 routes, not 7 — 13 with the auto-docs.** Largest of the wave. **Before D85.** |
| 3 | **D85** | MED | **moves pinned numbers.** ALONE, **after D49.** |
| 4 | **D8** | HIGH | ⛔ **moves the BALANCE SHEET.** ALONE, release note, serialise with T9. |
| 5 | **D86** | MED | **moves Outstandings**, inverts a Go test by design. ALONE, **before D94.** |
| 6 | **D94** | HIGH | the payables door. **After D86.** ✅ **L16 built this exact shape twice — copy `ForecastScreens.tsx`.** |
| 7 | **D83** | LOW | the register's proposed index **WILL NOT WORK.** The only concurrent probe. |
| 8 | **D96** | LOW | ⛔ **A QUESTION BEFORE A BUILD (§6.8), and the honest answer may be DELETION.** |

**Must be ALONE:** D85, D8, D86, D83.  **Must be SERIALISED:** D49→D85 · D86→D94.
⛔ **TWO ROWS NEED THE USER BEFORE CODE MOVES: D81's half B (§6.7) and D96 (§6.8).** Raise both early —
D81 is position 1, and its half A can proceed while the answer is pending.

⚠️ **D94 NEEDS THE NEXT MIGRATION NUMBER.** D95 took **0076**; the next free is **0077**, and it must
derive its seeder restatement from **0076's body** (14 PERFORMs), not from 0073. Re-check the highest
restatement at the moment you write the file.

---

## 2. WHAT THE LAST THREE LANES ESTABLISHED THAT CHANGES HOW YOU WORK

Full records: `HANDOFF-PARITY-AUDIT.md` §7.5, entries **L16 position 0**, **L16 position 1**, **L17**.

### ⛔⛔ 1. A DISCLOSURE, REFUSAL OR CAPTION CAN BE FULLY PRESENT AND FULLY WRONG (§5.19)
L16's M3 totalled the cash-flow screen's three basis sources into one figure — the one design it exists
to refuse. The page still said "THIS IS AN ESTIMATE", still carried the method verbatim, still listed
every `not_modelled` line. **Every one of those arms passed while the numbers lied.** Only the per-source
COUNTS and a FOOTING against a different report caught it. ⛔ **Applies directly to D85's refusal advice,
D86's `measureBasis` caption and D94's PDP rule.**

### ⛔ 2. FOOT AGAINST SOMETHING THAT IS NOT THE SAME HANDLER
D22's load-bearing arm compared figures **read off the screen** against the **Outstandings route**
(9+0+3 = 12 = 8+4). Every other arm had both sides from one handler. ⛔ **D85, D86 and D94 all have an
obvious same-handler comparison available and it will prove nothing.**

### ⛔ 3. HALF AN ORACLE CAN BE VACUOUS AND THE OTHER HALF IMPOSSIBLE (§5.23)
D95's print arms were green **before** the migration; its "an Owner reads all four" control **could not
pass** (an Owner gets 501). Both looked reasonable written down.

### ⚠️ 4. DOCUMENTATION THAT AGREES WITH ITSELF IS STILL UNVERIFIED (§5.24)
Eight migration headers say the coarse rule matches "at specificity 1". **It is 2.** Propagated because
each was derived from the last and the conclusion it supports is right. ⚠️ **The recon agent flagged it
as `uncertain` rather than repeating it — which is the only reason it got checked.**

### ✅ 5. ON FANNING OUT: READING PARALLELISES, WRITING DOES NOT
L17 ran 4 read-only recon agents concurrently (~344k subagent tokens, ~9 min) and it paid for itself
three times over — **two findings changed the work before a line was written.** ⛔ **But everything that
edits a file, rebuilds cored, runs a migration or drives a browser stayed serial**, because there is ONE
database, ONE cored, ONE vite and ONE tenant (§10). **Recon fans out; proof does not.**
⚠️ **And instruct read-only agents explicitly** — they have write tools.

---

## 3. PER ROW — state, the change, THE ORACLE

⛔ **D81's and D49's anchors below were re-verified at HEAD and SUPERSEDE the recon's.** D8/D83/D85/D86
still point at `HANDOFF-RECON-LAST-16.md` §3 entries 11–14 — ⚠️ **and those are unverified since L14.**
⛔ **Search for the SYMBOL, never jump to the line.** ⛔ **Use `/usr/bin/grep`** — the shell alias is
ugrep and returns **silently empty** on `frontend/src/api/mock.ts`. ⚠️ **Use ABSOLUTE paths**; `cd`
persists between Bash calls in this harness and has cost real time twice.

### 1 · D81 — a server refusal leaves masters behind AND throws away the keying · ⛔ ASK §6.7 FIRST
⛔⛔ **THE ROW'S OWN WORDING IS WRONG IN TWO PLACES AND THE PATHS IN EVERY PRIOR DRIVER ARE WRONG TOO.**
- ⚠️ **THE FILES ARE `frontend/src/screens/VoucherScreen.tsx` and
  `frontend/src/screens/VoucherScreen.masterGate.test.tsx`.** There is no `screens/vouchers/VoucherScreen.tsx`
  and no `masterGate.test.tsx`. Every earlier driver said otherwise.
- ⚠️ **HALF B IS NOT A "POP".** On a voucher CREATE frame the accept reducer **REPLACES** the frame with a
  fresh empty one (`navMachine.ts:1284-1291`, `stack[len-1] = fresh`). **Only the master-create arm pops**
  (`:1233`). A fix written to "stop the pop" is reading the wrong branch.

**STATE, VERIFIED AT HEAD:**
- ✅ **HALF A IS ALREADY DONE ON THE DRAFT PATH.** `onAccept` has TWO catches. The draft one
  (`VoucherScreen.tsx:2475`) already renders `DraftAcceptFailedError`'s minted-master sentence. **Only the
  non-draft catch at `:2518` is defective** — it renders `Error: ${err.message}` and names nothing.
  ⛔ **Do not "fix" the draft branch or count it as evidence the row is open.**
- ✅ **HALF B IS ALREADY CLOSED FOR *ALTER* FRAMES.** The D89 guard (`navMachine.ts:1281-1283`) keeps an
  alter frame put. **Half B is CREATE-frame-only.** ⚠️ **A test arm that drills a voucher from the Day
  Book and asserts the frame survives PASSES TODAY and proves nothing.**
- **FIVE builders, FOUR of which mint** (earlier drivers said four): `buildSingleEntryRequest:1772`
  (ensureLedger 1794, 1803) · `buildDoubleEntryRequest:1915` (1946) · `buildPlainRequest:2180`
  (**the only non-minting one, synchronous**) · `buildGstRequest:2213` (ensureGstTaxLedgers 2235;
  ensureLedger 2243, 2258, 2278) · `buildItemRequest:2349` (2373; 2380, 2385).
- `resolveMasters:956` · the non-draft send `:2509` · **the file's own written admission of D81 at
  `:941-946`** — ⛔ **that comment must be updated by the fix or it will contradict the new behaviour.**
- **No `DELETE /ledgers` exists.** Five DELETE routes total (`server.go:753, 786, 1098, 1100, 1108`);
  ⚠️ `:786` takes a `ledgerID` in its path but clears a bank date — the one that could be misread.

**THE SHAPES TO COPY — all in `draftAccept.ts`:** `DraftAcceptFailedError:654` (carries
`readonly createdLedgers`) · `ledgersMintedByThisAccept:1560` (diffs BY ID, only at positions the
resolver filled) · **the UNCACHED snapshot `:1694`** · the rethrow `:1709` (⚠️ guard
`if (minted.length === 0) throw err;` first).
⛔⛔ **THE SNAPSHOT MUST BE A DIRECT `client.listLedgers`, NOT `gstVoucher`'s `ledgerIndex`.**
`ensureLedger` writes newly minted ids straight into that shared index (`gstVoucher.ts:413`), so a "before"
picture taken from the cache **comes back empty and half A silently reports nothing.**

⛔⛔ **HALF B IS A DESIGN GAP, NOT A WIRING OVERSIGHT — AND THAT IS §6.7's QUESTION.** `onAccept` returns
**void** on every path, so the dispatcher gets `undefined`, returns `{ran:true}`, and the key reaches the
machine. AppShell's D92 refusal path (`AppShell.tsx:307`) is **live and correct — it is simply never
reached.** And it cannot be reached as-is: `screenRefusal` is **synchronous by construction**
(`ScreenActions.tsx:232`) while the accept POSTs **asynchronously**, and `effect.pending`
(`ScreenActions.tsx:194`) is awaited by AppShell **at `:321-330`, AFTER `onKeyDown(ev)` at `:316` has
already moved the machine. **So a server refusal is not knowable at the moment the handler must return.**

**ORACLE:** provoke a REAL server refusal (a back-dated cutoff is easiest; a duplicate master name is
easier still since D90) on a **CREATE** frame, and: `GET /ledgers` shows the minted master · the Day Book
shows **no** voucher · the status line **NAMES** the master · **and the keyed values are still in the
fields.** ⛔ **Both halves in one oracle or the row is not closed.**
⚠️ **Extend `VoucherScreen.masterGate.test.tsx:287/390/505` — do NOT invert them.** They assert masters
ARE created ("delayed, not removed"). ⛔ **`:505` must NOT gain `"Purchases"` back** — that expectation
previously encoded closed defect **D79**, and restoring it re-pins it.
⚠️ **The structural test `:142` reads `VoucherScreen.tsx` AS TEXT**, hard-codes the five builder names,
asserts no `ensure*` outside a builder region, and **scopes its regions by the `const onAccept = useCallback(`
regex** — renaming a builder, adding a sixth, hoisting an `ensure*`, or moving `onAccept` above the
builders all break it.
⚠️ **UNVERIFIED (recon could not run tests):** whether the mock client can be made to REFUSE a voucher
POST after ledgers are created. **Without that hook a non-draft test arm may not be expressible against
the existing mock** — check before promising a unit-level proof.

### 2 · D49 — insightd authenticates nobody · ⛔ 9 ROUTES, NOT 7 · BEFORE D85
⛔⛔ **THE REGISTER'S "7" IS WRONG AND SO IS EVERY DRIVER SINCE.** It is **9 application routes**, and
**13** counting FastAPI's auto-docs. Verified two ways — every decorator in the package, and the LIVE
`/openapi.json`. ⚠️ `HANDOFF-RECON-LAST-16.md:44` was more honest ("7 (8)") and later drivers regressed.

**THE NINE** (⛔ 7 take `company_id` in the PATH; that path value is the *only* tenant scope):
`POST /companies/{id}/drafts` · `GET …/drafts` · `GET …/drafts/{draft_id}` · `POST …/claim` ·
`POST …/claim/release` · `POST …/accepted` (all `routes_drafts.py` decorators 130/225/238/286/364/468) ·
`POST /companies/{id}/forecast/cashflow` (`routes_forecast.py:54`) · `GET /healthz` (`app.py:61`) ·
`GET /benchmark/last` (`app.py:84`).
**PLUS FOUR NOBODY HAS COUNTED:** `/docs`, `/redoc`, `/openapi.json`, `/docs/oauth2-redirect` — **all
answer 200 and publish the entire schema.** They are absent from `openapi.paths`, which is why an
openapi-based count misses them. `docs_url=None`/`redoc_url=None` are not set (`app.py:39-46`).

**ZERO AUTH ANYWHERE:** no middleware, no `Depends` on a security scheme, no router `dependencies=`; the
only `Depends` are `get_pipeline`/`get_draft_store`. Live `/openapi.json` reports `securitySchemes: None`.
⚠️ The nearest thing to a check is `routes_drafts.py:143` comparing the BODY's `company_id` to the PATH's
— **both from the same anonymous caller.** The `claim_token` is documented in two places as a concurrency
value that **authorises nothing**.
⛔ **RLS DOES NOT HELP.** `pg_store.py:67` sets the GUC from the path value, so RLS enforces whatever
scope the caller typed.
⚠️ **The loopback bind is an ACCIDENT, not a control:** `Makefile:126` runs uvicorn with no `--host`
(default 127.0.0.1). Adding `--host 0.0.0.0`, or running vite with `--host`, exposes all 13 with no code
change and no test failing. ⛔ **`infra/k8s/.../networkpolicy-default-deny.yaml:86` already CLAIMS "only
cored may call it" — aspirational: there is NO cored proxy** (`server.go:363` is a comment). cored *does*
proxy statutoryd, so the pattern exists in-tree.
⛔ **THE VITE `/companies` PREFIX IS A NAMESPACE GRAB** (`vite.config.ts:22`): it routes EVERY same-origin
`/companies/*` to insightd, while cored's own router is `/v1/companies/{companyID}`. **A proxy route added
at `/companies` will be sent to insightd instead of cored — a fix that appears to do nothing in the browser
while every unit test passes.**

⛔⛔ **THE BIGGEST TRAP IS SILENT, NOT LOUD, AND IT RE-OPENS A MONEY DEFECT.** **All four** insightd-backed
layers of the duplicate-bill guard **fail OPEN on any non-409 error**: `claimDraftForPost` warns and
`return null` (`:1443-1449`) · `draftAlreadyAcceptedInsightd` returns null (`:1269-1277`) ·
`releaseClaimAfterFailure` never throws (`:1479-1488`) · `recordAccepted` is fire-and-forget (`:1787-1792`).
**A 401 lands in every one.** So if insightd starts refusing before the clients learn to send a credential,
the five-door guard silently collapses to its client-side layers, **the accept still succeeds**, and the
only evidence is a `console.warn`. ⛔ **A GATE THAT CHECKS "ACCEPT STILL WORKS" WILL BE GREEN WHILE THE
MONEY DEFECT IS RE-OPENED. Assert the claim POST reached insightd (a 200 on `.../claim`) — never that
Ctrl+A still saves.**
⚠️ **The lifecycle is FIVE routes, not three:** the three POSTs plus `GET drafts/{id}` and, on the
no-draftId path, `GET drafts` plus one GET per candidate (the find-by-body fallback `draftAccept.ts:1200-1216`).
⚠️ **Preserve the 409 CONTRACT:** `conflictCode` (`:1352-1357`) reads `err.body.detail.code` and only for
status 409. The three codes are minted at `routes_drafts.py:338 / :426 / :440`. **A proxy that reshapes the
409 body or its status turns three specific refusals into the fail-open branch.**

**SCOPE:** 9 routes + the bind + **2 client transports** (`api/insightd.ts:50` and a **deliberately
duplicated** resolver at `draftAccept.ts:1173`, flagged at `:1116-1123` as a copy that must not drift) +
**2 more callers in `frontend/scripts`** (`seed-demo.mts:1774`, `e2e-ocr-prefill.mts:101`) + the vite proxy
+ healthz + its tests. ⚠️ **Neither transport can send a credential today** — `createHttpClient` emits only
`opts.headers` and no insightd caller passes any. "Inherit the bearer" is not a one-liner.

**TESTS — ONE IS DESIGNED TO BREAK, ONE BREAKS FOR THE WRONG REASON:**
- ✅ `tests/test_api.py:40 test_healthz_DISCLOSES_that_there_is_no_authentication` **pins the known-bad
  state on purpose.** Its own message demands the row and the test change together, and that **the commit
  say what a token is checked AGAINST.** `/healthz` returns `"authentication": "none"` (`app.py:80`) and
  `"tenant_scope": "caller-supplied company_id in the path"` (`:81`) so the posture is a fact on the wire.
- ⛔ `tests/test_api.py:109 test_no_ledger_write_endpoint_exists` asserts `404/405` on an **unauthenticated**
  POST. **A global 401-before-routing turns it red without the HITL boundary having moved** — a false
  alarm in the one file that proves insightd cannot write to the ledger. **Anticipate it in the commit.**
- ⚠️ `test_get_draft_other_company_is_404` and `test_pg_store.py:614` encode "another company's data is
  indistinguishable from absent" **as a property of the PATH scope**; under auth that must be re-expressed,
  and 401-before-404 changes which assertion fires.
- ⚠️ `test_hitl_invariant.py`'s `_ALLOWED_MUTATING_PATHS` is a **5-entry allowlist** with a comment
  forbidding widening it to silence a failure. **Any new mutating route (a token exchange, a session route)
  turns it red BY DESIGN.**
- 55 test functions across 6 files issue header-free requests.

**ORACLE:** an unauthenticated call to **each** route is REFUSED, and a call for another tenant's
`company_id` cannot read it — **on the wire, per route.** ⛔ **Nine refusals, not one and an argument that
the rest are the same shape.** Plus: the claim POST still reaches insightd on a normal accept (the
anti-fail-open arm above), and `/docs` + `/openapi.json` no longer publish the schema anonymously.
⚠️ **UNVERIFIED:** whether the forecast router is even mounted without `statsmodels` (`app.py:54-59` is a
`try/except ImportError`, so a deploy without it serves **8**); the in-cluster bind host (**no Dockerfile
exists in the repo** and `/readyz` 404s, so `infra/k8s/base/insightd.yaml` is not a usable reference).

### 3 · D85 — the duplicate-bill guard keys on the POST-redaction number · RECON §3.11 · ALONE · AFTER D49
✅ **DECIDED: key on a PRE-REDACTION HASH.** Every PII-shaped invoice number collapses to the constant
`[PAN REDACTED]`, so two different PAN-shaped numbers from one supplier on one date are the same bill to
the check, and the second accept is refused. Its own advice — *"key its own supplier invoice number"* —
**cannot work**, because any number that redacts lands on the same token.
⛔ **The hash must be taken on the PII boundary's OWN INPUT:** `insightd/insightd/pipeline.py` is the last
place the raw number exists, so the hash travels **with the draft as a field**. That is an **insightd
wire-shape change**, and `pii_boundary_strict` plus M23 guard that boundary.
⛔ **THE REFUSAL'S ADVICE MUST CHANGE WITH THE KEY — AND §5.19 IS ABOUT EXACTLY THIS.** The current
sentence tells the operator to do something that cannot work. A new key with the old advice is a refusal
that is *fully present and fully wrong*: it reads as helpful and sends the operator down a dead end.
**Assert on the ADVICE, not on the presence of a refusal.**
**ORACLE:** two DIFFERENT PAN-shaped invoice numbers from one supplier on one date **both post** — and
the same number twice is still refused (the control arm; without it the fix could simply be "guard
removed").
⚠️ It **moves pinned numbers** — `test_pii_persistence.py`'s redaction counts and the F9 panel text.
**Predict the moved number OUT LOUD in the commit message before running the gate**; if the gate stays
green, assume the binary was not rebuilt before assuming the probe is not an oracle (§5.17).

### 4 · D8 — closing stock replayed in LEXICAL voucher_no order · RECON §3.12 · ⛔ ALONE
An issue keyed after a receipt on the same date is costed at the pre-receipt average. Measured in a
browser: Closing Stock **25,909.3478** where entry order gives **25,307.8797** — 601.4681 too high. The
valuation half is correct; the ORDERING half is wrong, and **no existing fixture can fail on it.**
⛔ **THIS MOVES THE BALANCE SHEET** — closing stock feeds it — so it re-prices real books on the next
report read. **Serialise with feature T9 (FIFO/LIFO)** and ship a release note.
⛔ **CHEAPEST CHECK, BEFORE CHANGING A LINE:** add ONLY the new failing fixture and the new
`valuation_test.go` subcase, and run the full backend + corpora suites. **Exactly one Go subcase and the
new fixture should fail.** If anything else fails, the cost model is wrong and the row needs re-scoping
before any code moves.
✅ **D77 is done, so posting order is stable** — this row's drive reads reports whose leg order no longer
wobbles.
**ORACLE:** the same books, replayed in ENTRY order, price the issue at the post-receipt average — and the
figure the browser showed moves to the one entry order gives. ⚠️ **Read it in a browser, not only from Go:
`verify:demo` pins 34 checks and this row moves money that several of them foot.**

### 5 · D86 — Outstandings bounds at today, the Balance Sheet does not · RECON §3.13 · ⛔ ALONE · BEFORE D94
✅ **DECIDED: OUTSTANDINGS STOPS BOUNDING AT TODAY** — deliberately NOT the Balance Sheet, so
`verify:demo` needs no re-pin. One bill dated in the future gave BS **109950.0000** vs Outstandings
**108770.0000**, while `?as_on` in the future made them agree.
⛔⛔ **THREE PLACES MOVE TOGETHER, AND MISSING ONE RE-OPENS D20:** (1) the report; (2)
`internal/query/whitelist.go`'s `EntityOutstandings` skeleton, which D20's fix made MIRROR the report —
`drive-l5-nlq.mjs` will catch it; (3) `QueryScreen`'s `measureBasis` caption, which says future-dated
bills are excluded and becomes **FALSE**. ⚠️ **That caption is a §5.19 case**: it is prose that will still
be present, still well-worded, and wrong.
**ORACLE:** post a future-dated bill; the two reports AGREE with no `as_on`.
⚠️ It **inverts a Go test by design** — say so in the commit rather than deleting the test (§5.16).
⚠️ **AND IT MOVES WHAT D94 RENDERS**, which is why D94 comes after it.

### 6 · D94 — the payables ageing report has NO DOOR · AFTER D86 · anchors inline
⛔ **THE SERVER ALREADY SERVES IT AND NOTHING IN THE PRODUCT CAN ASK.** Measured on the wire 2026-08-23
against a binary verified fresh by symbol: `?kind=PAYABLE` → **200 · total 1,04,050.0000 · 2 creditors ·
4 bills** (`Steel Suppliers` 80,450.0000 / 3 bills · `Delhi Parts` 23,600.0000 / 1 bill), full per-bill
arrays; `?kind=payable` also accepted; `?kind=BOTH` → **400 `invalid kind (RECEIVABLE|PAYABLE)`**.
The client builds `const query: { as_on?: string } = {}` and **`kind` is never a member**
(`ReportScreens.tsx`, the Outstandings fetch). **One report name (`"Outstandings"`), one screen
(`report:Outstandings`), one PDP resource.**
⛔ **IT IS NOT A `?kind=` TOGGLE, AND THAT IS THE ENTIRE REASON IT IS A ROW.** `routeAction`
(`auth_middleware.go`, the `case "/outstandings"` arm) maps the **flat literal path** to the resource, so
the PDP decides on the NAME and **a query parameter is invisible to it**. A toggle would make *"the sales
clerk may see what customers owe us but not what we owe suppliers"* an **unwritable rule**, and those two
lists belong to two different jobs.
⛔ **AND A NEW REPORT NAME ARRIVES PRE-GRANTED** — same mechanism as D95, so this row owes an ACL
counterweight of its own. **See §1's warning about the migration number.**
**THE BUILD — the Order Book's treatment end to end** (`0073_order_book_acl.sql` is the template):
(1) a second **FLAT LITERAL route** beside `/outstandings` — additive, so **no existing `acl_rule` row is
invalidated**; (2) its `routeAction` arm; (3) the name in `authz.KnownReportNames` (`vocabulary.go`);
(4) the printed form — ⚠️ **`printOutstandings` already parses `kind`, and `outstandingKindParam` is
SHARED with the JSON handler on purpose**, so both directions print from one parser; (5) **the ACL migration — ⛔ D95 TOOK 0076, so this is 0077 and it must derive its
seeder restatement from 0076's body (FOURTEEN PERFORMs), not from 0073**; (6) the client: the report name in `keystroke/types.ts`, the screen registration in
`defaultScreens.tsx`, the `REPORT_SLUGS` + `REPORT_PERIOD_SHAPE` entries in `printTarget.ts` (both
`Record<ReportName, …>`, so **tsc names them for you**), the `AS_AT_ONLY_REPORTS` membership, and the
screen sending `kind`.
✅ **L16 JUST DID THIS EXACT SHAPE TWICE — COPY `ForecastScreens.tsx` AND ITS COMMIT.** The four
registries, the mock arm, the 403-as-a-decision panel and the `REPORT_DOORS` sweep are all already
solved there.
**ORACLE, TWO ARMS, AND THE SECOND IS THE POINT OF THE ROW:**
(a) a keyboard path reaches the payables ageing in a browser and the figures match `?kind=PAYABLE` on the
wire; **(b) ⛔ a level with `disallow display report:<payables-name>` is REFUSED it WHILE STILL BEING
ALLOWED the receivables book.** **That second arm is the one a `?kind=` toggle could never pass**, and it
is what proves the row was understood rather than merely closed.
⚠️ **`?kind=` MUST KEEP WORKING** on the existing route: `printOutstandings` and the NLQ surface
(`whitelist.go`'s `EntityOutstandings`, whose `nature` filter already answers both directions) both depend
on it. **This row adds a NAME; it does not remove a parameter.**
⚠️ **AND THERE IS AN EXISTING ALIAS THAT LIES.** `navMachine.ts`'s `REPORT_ALIASES` maps
**`"bills payable" → "Outstandings"`** — the RECEIVABLE report. An operator typing the words for payables
gets receivables today. **Re-point it in this lane**, and note that L16 deliberately refused to add a
misleading alias for the same class of reason (§6.5).

### 7 · D83 — two concurrent posts share one hand-typed bill number · RECON §3.14 · LAST
⛔ **THE REGISTER'S PROPOSED FIX WILL NOT WORK.** `bill_allocation` cannot carry the index, because
duplicate names are legal for AGAINST / ON_ACCOUNT / caller-named openers. It needs
`pg_advisory_xact_lock` or a new table.
⚠️ **The SEQUENTIAL case — what an operator actually produces — is already closed.** This is the
concurrent one, under READ COMMITTED, and it is the only row in the wave needing a genuinely concurrent
probe.
✅ **L15 BUILT THE PATTERN YOU NEED HERE.** D90's `refuseIfNameTaken` is a pre-check that RACES with a
unique index behind it, and `migrations/master_name_index_census_test.go` exists precisely because **no
gate could see the index missing** — the pre-check shadowed it for serial creates. ⛔ **Read that test
before designing this row:** it is the same shape (an application check that is not the authority) and it
names the trap.
⛔ **AND EXPECT THE MUTATION NOT TO REPRODUCE.** That is what happened on D90 — `contract:cored` reported
14/14 with two of three indexes missing — and the non-reproduction is what bought the census test. **A
mutation that will not break anything is a finding (§5.4), and on this row it is the LIKELY outcome.**
**ORACLE:** two simultaneous posts of the same bill number on one party — one succeeds, one is refused.
**On the wire**; there is no mock equivalent for concurrency, and the mock says so at the method.

### 8 · D96 — insightd's statistical forecast has no renderer · ⛔ A QUESTION FIRST · LAST
⚠️ **THIS IS D72's ONE SURVIVING CLAUSE AND IT ALMOST DIED SILENTLY.** D72 closed 2026-08-21 (its money
half was already fixed — `ForecastRequest` takes fixed-4 `Money` strings, gated with a non-vacuity arm)
and its remainder, *"still not reachable from the keyboard"*, was folded into D22. L16 then closed D22 by
building a door over **cored's** `/cash-flow`. ⛔ **That is a different service, a different endpoint and
a deliberately different method** — cored's own `method` string says *"deterministic; no statistics, no
model"*. **Wiring one did not wire the other.**
✅ Its disclosure work is already done on its side (`estimate: true` plus its own method).
⛔⛔ **ANSWER THIS BEFORE WRITING A LINE: SHOULD IT EXIST AT ALL?** cored's projection is explainable to
an auditor bill by bill. A statistical forecast rendered anywhere near it invites exactly the confusion
the Cash Flow screen's whole disclosure block exists to prevent. Three honest outcomes:
**(a) its own report NAME** (the PDP argument again — a different method is a different authority, and it
would need its own ACL counterweight per D95); **(b) deletion**, which closes the row for free and
removes a maintained path nobody can reach; **(c) leave it, documented as internal-only.**
**This is a product decision. RAISE IT; do not quietly build it.**
⚠️ **If the answer is (b), the row still owes a proof** — that nothing imports it and no route serves it,
asserted by a test rather than by a grep in a commit message.

---

## 4. THE STACK, AND HOW TO KNOW IT IS REAL

```bash
cd /Users/yashbhalgat/code/tally-updated
docker compose up -d db
pg_isready -h localhost -p 5433 -t 10
PGPASSWORD=postgres psql -h localhost -p 5433 -U postgres -d tally -tAc "select 1"

# ⛔ MIGRATIONS ARE NOT APPLIED ON BOOT. `make run-cored` serves; it does not migrate.
#    L15 added 0075 and spent ten minutes wondering why the index was absent.
make migrate                                   # -> "migrations applied (59 files)"

# ⛔ kill by PORT OWNER — the compiled child's argv is a temp path, so a command
#    pattern misses it and the stale binary keeps the port while /healthz says 200.
#    Kill the go-run PARENT too, or it respawns nothing and you debug a ghost.
for p in 8087 8088 8091; do
  pid=$(lsof -nP -iTCP:$p -sTCP:LISTEN | tail -1 | awk '{print $2}')
  [ -n "$pid" ] && kill "$(ps -o ppid= -p $pid | tr -d ' ')" "$pid"
done
(nohup make run-cored      > /tmp/cored.log      2>&1 < /dev/null &)   # wait for :8087 FIRST
(nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 < /dev/null &)
(nohup make run-insightd   > /tmp/insightd.log   2>&1 < /dev/null &)
cd frontend && (nohup pnpm dev > /tmp/vite.log 2>&1 < /dev/null &)

# ⛔ VERIFY THE RUNNING BINARY BY SYMBOL. 0 = STALE = STOP. A TCP connect is not liveness.
# ⚠️ Poll for the PORT before resolving the pid: `ps -o command= -p ""` prints a USAGE
#    ERROR and the symbol count then reads 0, which looks exactly like a stale binary.
until lsof -nP -iTCP:8087 -sTCP:LISTEN >/dev/null 2>&1; do sleep 1; done
PID=$(lsof -nP -iTCP:8087 -sTCP:LISTEN | tail -1 | awk '{print $2}')
BIN=$(ps -o command= -p "$PID" | awk '{print $1}')
strings -a "$BIN" | grep -c 'p.amount DESC, p.id'            # D77  (want 2)
strings -a "$BIN" | grep -c 'identity: set company features' # D5   (want 1)
strings -a "$BIN" | grep -c 'is not a GST registration type' # D93  (want 1)
strings -a "$BIN" | grep -c 'apart from capitalisation'      # D90  (want 1)

# SEED, and take the id from the LOG (never .demo-company-id)
cd frontend && pnpm seed:demo --insightd > /tmp/seed.log 2>&1
grep -oE "company id: [0-9a-f-]{36}" /tmp/seed.log | head -1 | awk '{print $3}' > /tmp/drive-company-id
pnpm run verify:demo "$(cat /tmp/drive-company-id)"     # -> ALL CHECKS PASSED (34/34)
```

### ✅ THE DRIVE SET IS GATE-SAFE — MEASURED 2026-08-23, END TO END
```
fresh seed -> verify:demo 34/34
   run all seven drives + contract:cored
verify:demo 34/34          <- the whole set leaves the tenant level
```
⛔ **THAT WAS NOT TRUE UNTIL THIS HANDOFF WAS WRITTEN, AND IT DIRECTLY CONTRADICTED §5.15.**
`drive-l14b-d77.mjs` posted an 11,800 invoice to `Acme Traders` — a bill-by-bill party — **with no
bill reference**, so it landed as an unallocated ON-ACCOUNT balance: the Balance Sheet counts it (a
ledger balance) and Outstandings does not (bill-wise). `verify:demo`'s **"BS Sundry Debtors ==
Outstandings (receivable)"** then failed by exactly that amount, on a tenant the probe does not tear
down. **A MONEY failure, which the next session reads as a regression in its own lane.** Fixed by
giving the fixture the bill reference a real invoice carries. **Re-run this end-to-end check after
adding any probe that POSTS.**

### THE DRIVE SET — all green at HEAD, 2026-08-23
```bash
# always from the REPO ROOT; the harness resolves against frontend/
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l13-masters.mjs  # 33/33
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l14-cheap.mjs    # 17/17 (D82/D89/D80)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l14b-d77.mjs     # 8/8   (D77)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15-d5.mjs       # 22/22 (D5/D43)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15c-d92.mjs     # 7/7   (D92)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15d-d87.mjs     # 13/13 (D87, both doors)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15f-d90.mjs     # 8/8   (D90)
CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l16-d22.mjs      # 34/34 (D22/D52/D23)
node frontend/scripts/drive-l17-d95.mjs        # 20/20 (D95) — ⛔ takes NO `CO`: it makes its OWN tenant
                                              #   with its OWN Data Entry member and REAPS it (D91 pattern)
cd frontend && pnpm contract:cored                     # 14/14 — ⛔ NO `CO`, it makes its own tenant (D91)
```

### ⚠️ TWO THINGS ABOUT THE L16 PROBE THAT APPLY TO EVERY PROBE YOU WRITE
- **It takes ~6 minutes** and the 2-minute foreground Bash cap kills it. **Run every drive with
  `run_in_background: true`** and wait on a marker in its log, not on a fixed sleep.
- ⛔ **It READS ONLY** — no posts, no masters — which is why it cannot shift `verify:demo`. **A probe that
  posts or mints owes the §5.14 reap and the §5.15 end-to-end re-check.**

### ⚠️ A PROBE CAN FINISH AND STILL NOT EXIT — AND `pkill` BY SCRIPT NAME KILLS ITS PARENT
Measured 2026-08-23: `drive-l16-d22.mjs` printed **34/34 probes ok** and then sat for 20 minutes with
**zero** `Chrome for Testing` processes alive — finished, but holding an unclosed handle. ⛔ **Judge a
drive by its PRINTED SUMMARY, not by whether the process exited**, and check
`pgrep -fl "Chrome for Testing"` before concluding it is still working.
⛔⛔ **AND DO NOT `pkill -f <script-name>`: the pattern also matches the WRAPPER that launched it.** Doing
that killed the whole drive-set job mid-sequence and cost the last two gates a re-run. Kill by PID.

### Gates, measured at this handoff
`pnpm vitest run` **1443 / 87 files** · `pnpm exec tsc --noEmit` **clean** (a SEPARATE gate) ·
`go test ./... -count=1` **0 FAIL / 0 SKIP** (bracket with a live `psql` — ⚠️ a down database makes
every DB test SKIP and `go test` still exits 0, so the `0 SKIP` count is the load-bearing number) ·
`pnpm contract:cored` **14/14** · `pnpm verify:demo` **34/34 on a FRESH seed** ·
`make verify` **exit 0, run ALONE** — ⚠️ **its output contains four `ApiError: 404` lines and the word
`FAIL` is absent; those four are `draftAccept`'s own `console.warn` from the tests that deliberately
exercise D15's insightd-unreachable window.** Do not read them as failures. `make verify` must still be (beside anything else `internal/sync` hits its 600s timeout and
`EWayBillScreen.test.tsx` fails on a missing `role="radio"` while passing standalone).

### ⚠️ MIGRATIONS: 60 APPLIED, HIGHEST `0076_forecast_report_acl` (2026-08-24)
⛔ **`make migrate` SKIPS AN ALREADY-STAMPED FILE.** To re-apply a migration you are mutating,
`DELETE FROM schema_migrations WHERE version='<file stem>'` first — then `make migrate`, and **restore it
the same way afterwards.** L17's M1 proof depended on this and it is the only way to mutation-prove a
migration at all.

### ⚠️ THE DEV DATABASE IS NOT PRISTINE
- **`CLUSTER posting` was run** (D77's oracle needs a heap rewrite). Harmless; the fix makes reads
  deterministic regardless — but it is why the table's physical order is not insertion order.
- ⚠️ **220 companies (2026-08-23) and nothing reaps them**, so `GET /v1/me` measures **0.5–3.5s** and the browser
  bootstrap is occasionally slower than any fixed wait — `openApp`'s tenant assertion polls to 40s for
  exactly that reason. Reaping needs a written script: `company` has **14 non-cascading FKs**.
  ⚠️ **81 of them are called "Demo Traders"** — see §5.13.
- ⚠️ **`"No Company"` in the chrome is a LOADING state, not a resolved one.**

### ⛔⛔ THE MACHINE — CHECK THIS BEFORE BLAMING THE APP
```bash
ps -Ao pcpu,pid,comm -r | head -12
pkill -9 -f "Google Chrome for Testing"       # orphaned probe browsers
cat ~/.claude/machine-health/ci-slots/*.owner # who actually holds the CI slots
```
⚠️ **`ci-slot` NAMES A STALE HOLDER, AND DEAD PIDS DO NOT MEAN A WEDGED QUEUE.** The semaphore is an
`flock`, which the OS releases on death — it **cannot** wedge on a dead holder.
⚠️ **`make run-cored` coming up in "1s" is NORMAL** — the build is cached. Judge the binary by its
symbols, never by how fast the port opened.
⚠️ **`timeout` is not installed on this machine.** Use `Bash(run_in_background)` or an `until` loop.

---

## 5. WHAT WILL GO WRONG

### 5.1 ⛔⛔ A RECON IS FOR FINDING THE CODE, NEVER FOR FILLING IN THE EXPECTED VALUE
D84 measured all eleven of its expectations on the wire instead of trusting the recon, and two were
wrong. **L15 did the same for D87 and two of five were wrong** — `credit_days` was accepted at 201 and
discarded, and `pan`/`credit_days` were not readable back — **on a row whose server side the recon
called done.** Three sessions running, ten minutes of `curl` has overturned a documented claim.
**Every remaining row's "the server answers X" claim is unverified until you fire it at the server.**

### 5.2 ⛔⛔ A MEASUREMENT IS EVIDENCE, NOT PROOF — THE COUNTERWEIGHT TO 5.1
§5.1 stands. **D90 supplies the other half.** D84 measured that cored ACCEPTS a duplicate stock item
name, contradicting the recon. That overturning was written into the corpus `$provenance`, repeated
across three handoffs, used to register **D90**, and used to frame a question **the user answered**.
It was false — `UNIQUE (company_id, name)` has been on `stock_item` since migration **0005**.
**The corrective is not to measure less** — re-measuring found the real defect underneath. **The
corrective is that "I measured it" is a claim like any other, and the measurements that feel like
DISCOVERIES are the ones to re-run.**

### 5.3 ⛔ THE MUTATION PROOF WILL SOMETIMES CORRECT **YOU**, NOT THE CODE
D77's commit asserted a cost-split misattribution that was plausible, traced, and **false**. L15's
M13 predicted cored would silently coerce a bad enum label; **it refuses** — and the refusal leaked
`SQLSTATE`, which became **D93**. **Write the claim down before the proof, so the proof can kill it.**

### 5.4 ⛔ A MUTATION THAT WILL NOT REPRODUCE IS A FINDING, NOT A FAILED STEP
D90's predicted mutation — drop the normalised indexes, watch the corpus go red — **did not
reproduce.** `contract:cored` reported **14/14 with two of three indexes missing**, because a Go
pre-check shadows the index for serial creates. The index is the only thing that refuses the second of
two **concurrent** creates, and **no gate could see its absence.** The non-reproduction bought
`migrations/master_name_index_census_test.go`. **When a mutation refuses to break something, find out
what is holding it up before writing a better mutation.**

### 5.5 ⛔ THE STATED ORACLE MAY NOT WORK — CHECK THE MECHANISM IS LIVE
D77's prescribed oracle (*"GET, `VACUUM ANALYZE`, GET again"*) does not move a non-total sort. It
takes a **heap rewrite**. **Fix:** if a probe's mechanism is probabilistic, add a **PRECONDITION arm**
that proves the mechanism fired for THIS fixture.

### 5.6 ⛔ A PROBE THAT PASSES FIRST TRY IS THE ONE TO DISTRUST
D82's suite was green THROUGH a duplicate-invoice defect. D80's probe passed asserting nothing about
the mode. D77's passed comparing side and amount only. **Print the field you are asserting on, not a
prefix of the page.**

### 5.7 ⚠️ A PROBE THAT READS THE WRONG FIELD NAME PASSES VACUOUSLY — **THREE TIMES IN ONE SESSION**
| Probe | Read | The wire/DOM actually has |
|---|---|---|
| D77 (L14b) | `p.ledger_name` | `p.ledger` |
| D5 (L15) | `stock_item_name` | `item` |
| D92 (L15c) | `body.innerText` for an input's value | **input VALUES are not in `innerText`** |
⛔ **All three are one shape: a projection that does not exist, COMPARED instead of THROWN on.** An
absent projection has a falsy value that compares cleanly against anything. **Make the probe THROW.**

### 5.8 ⛔ ASK WHAT A **SECOND** KEYPRESS DOES
Nothing in this repo had ever pressed the accept key twice on one frame, and that is where D89 lived.
**For every row whose oracle is a REFUSAL, drive the operator's next keystroke too. The refusal is not
the end of the story; the remedy is.**

### 5.9 ⛔ WHEN A ROW HAS A STRUCTURAL GUARD, READ WHAT THE GUARD **COMPARES**
D80 shipped a dead picker entry *past* a test written to prevent exactly that: two of its three
clauses checked the machine's real routing tables; the third certified the broken entry.
⛔ **D93 IS THE SAME LESSON FROM THE OTHER SIDE:** `drive-l13-masters.mjs` asserts `!/SQLSTATE/` on a
refusal from the ledger-create route, and **what it compared was one arm of four.** **Read what the
guard does NOT compare, and ask why those siblings are exempt.**
⛔ **AND D5's M4:** mapping a route to `authz.Display` instead of `authz.Alter` kept
`TestEveryCompanyRouteIsMappedAndInVocabulary` **GREEN** while handing a Data Entry clerk the
inventory escape hatch. **The structural gate catches an UNMAPPED route and is blind to a MIS-mapped
one.**

### 5.10 ⛔ ANSWER FROM THE ROW, NEVER FROM THE REQUEST
D5's **M2b** is the cleanest evidence in the repo: with the read-back replaced by an echo, the
RESPONSE assertion went **green** while the column stayed wrong. That is D43's exact mechanism, **one
line away at all times**, and **only an out-of-band oracle can see it.** Every remaining row that
writes a setting owes this check. **A renamed constraint has the same shape** — see D90.

### 5.11 ⚠️ FOR A CLIENT-SIDE MUTATION, VERIFY **VITE**, NOT THE BINARY
The §4 symbol check proves what cored is running and says nothing about a refusal that never leaves
the browser. Curl the module out of vite and count the mutated construct — and **calibrate against
`git show HEAD:<file> | grep -c`**, because a construct that also appears in COMMENTS never reaches
zero.

### 5.12 ⚠️ TWO CLIENT/AUTH TRAPS
- **`createRestClient`'s auth option is `headers`, not `getAuthHeaders`, and a wrong key is silently
  ignored.** Symptom: `401 missing bearer token` on every row.
- **`auth.mts`'s `authHeaders()` mints for a fresh RANDOM subject** — right for a script that creates
  its own company, wrong for one handed an existing tenant. Mint for `browser-dev-owner`, **once per
  run**. ⚠️ **`contract:cored` now does the opposite ON PURPOSE** (D91): it creates its own tenant, so
  it mints a random subject — and that gives it a second layer, because a caller who re-points it at
  someone's books with `CO` gets eleven loud `403`s instead of eleven quiet writes.

### 5.13 ⚠️ NEVER GROUP BY A DISPLAY NAME IN THIS DATABASE
A duplicate-hunting query grouped by `company.name` and reported **188 collisions**; the real number
was **6**. **81 of the 220 companies are called "Demo Traders"** — one per re-seed. **Group by
`company_id`.**

### 5.14 ⛔⛔ A PROBE THAT CREATES A MASTER IN THE DEMO TENANT MUST REAP IT
**D91 closed this for `contract:cored` and did NOT close the class** — the very next drive script
written in the same session re-introduced it, and `verify:demo` went 34/34 → 31/34 on one party left
behind. **The cost is misattribution, not the failing check:** the next session reads seven failures,
two of them money failures, as a regression in its own lane. The reap must **assert it worked** and
**refuse to run on a master that has postings**.
⚠️ **RE-SEED BEFORE JUDGING `verify:demo`.** Seven failures on a stale tenant is the NORMAL state.

### 5.15 ⛔⛔ RE-RUN THE **WHOLE** DRIVE SET AT THE END OF A LANE
D87 broke **two** probes hours after both were green:
- `getByText("Sundry Debtors")` began matching TWO elements, because D87's party block carries the
  prose *"a state code is REQUIRED under Sundry Debtors and Sundry Creditors"*. Playwright's strict
  mode throws. **Pick a group with `getByRole("option", …)`, never by text.**
- and `drive-l13-masters.mjs` — **the file that FOUND D87** — went comprehensively red, because its
  fixture party is created under Sundry Debtors with no state code, so the very first create was
  refused and every downstream arm failed. ⛔ **That does not read as a stale probe. It reads as D36
  and D47 having regressed.**
⚠️ **Every drive that creates a PARTY through the FORM now needs a state code.** Measured 2026-08-23:
only `drive-l13-masters` and `drive-l15c-d92` use the form path; the other five that mention party
groups go through the API, where the client-side refusal does not apply.
⛔⛔ **AND FOLLOWING THIS RULE IS WHAT EXPOSED THE NEXT ONE.** Re-running the set turned `verify:demo`
from 34/34 to 33/34 with a MONEY failure — `drive-l14b-d77` was posting to a bill-wise party with no
bill reference (see §4). **Advice to re-run everything is a trap unless the set is gate-safe**, so the
set was made gate-safe in the same pass and the end-to-end check is now in §4.

### 5.16 ⛔ WHEN A ROW'S OWN DRIVE ASSERTED THE DEFECT, INVERT IT — DO NOT DELETE IT
`drive-l13-masters.mjs` found D87 and asserted *"the ledger form offers NO GSTIN field"*. Those arms
are now **inverted**, not removed: **a deleted arm leaves nothing that would notice a REVERT**, an
inverted one fails the day the field disappears and still explains why it has to be there.

### 5.17 ⚠️ THE NUMBER-MOVING ROWS MAY MOVE MORE THAN PREDICTED — OR NOTHING
If D85/D8/D86's gate stays GREEN, assume the binary was not rebuilt and cored not restarted **before**
assuming the probe is not an oracle. **Predict the moved number out loud in the commit message.**

### 5.18 ⚠️ A FULL-SUITE FAILURE UNDER LOAD MAY BE A FLAKE — CHECK, DON'T ASSUME
**Structural argument first** (does the test even touch your change?), then the standalone run, then
the re-run. Known members: `internal/sync`, `EWayBillScreen`, `VoucherScreen.draft.test.tsx`.

### 5.23 ⛔⛔ ASK OF EVERY ARM: WOULD IT PASS **BEFORE** MY CHANGE? AND CAN THE POSITIVE ARM SUCCEED?
**D95's stated oracle was half vacuous and half impossible, and both halves looked perfectly reasonable
written down.** The driver said: *"Data Entry 403 on display AND print for both, and an Owner still reads
all four."*
- **The two `print` arms passed before the migration existed.** Print was already default-denied for that
  level (0038 seeds no `allow print report:`, the coarse grant is `display`-only, `default_access` is
  `none`). Four green arms, two of which proved nothing.
- **The Owner arm could not pass at all.** Printing those two reports is unimplemented — an Owner gets
  **501**, and a test actively requires that. **Asserting 200 would have failed on CORRECT code**, which
  is the worst kind of wrong probe: it sends the next session hunting a defect that is not there.
⛔ **SO RUN BOTH CHECKS ON EVERY ARM BEFORE WRITING IT:** (1) *would this be green on the commit before
mine?* — if yes it is a regression guard, label it as one and do not count it as proof; (2) *can the
positive/control arm actually reach its success state?* — check what the happy path really returns
instead of assuming 200. This is §5.6 ("a probe that passes first try is the one to distrust") turned
into two questions you can ask before running anything.

### 5.24 ⚠️ DOCUMENTATION THAT AGREES WITH ITSELF IS STILL UNVERIFIED — EIGHT HEADERS, ONE WRONG NUMBER
Eight migration headers state that the coarse `allow display report` rule matches "at specificity 1".
**It scores 2.** `internal/authz`'s `ruleSpecificity` SUMS an exact-ACTION point and a resource weight
(0 wildcard / 1 exact resource / 2 exact `resource:name`), so the coarse rule is 1+1 and `report:NAME`
is 1+2. The error propagated because **each header was derived from the last** and the CONCLUSION it
supports is correct, so nothing ever contradicted it.
⛔ **THIS IS §5.2's LESSON FROM A THIRD DIRECTION.** §5.1 says a recon's expected values are unverified;
§5.2 says your own measurements are claims too. **§5.24: so is a comment that eight files agree on.**
⚠️ **The recon agent put this in its `uncertain` list rather than asserting it, which is the only reason
it got checked** — an agent that had confidently repeated the headers would have been believed.

### 5.25 ⚠️ REAPING A TENANT: `company` HAS FIFTEEN NON-CASCADING FKs, AND THE OBVIOUS FOUR ARE NOT ENOUGH
§4 warns that reaping needs a script. Here is the measured one for a **book-less** tenant, in order —
the four-table version fails on `notification_outbox_company_id_fkey`, because creating a company writes
an outbox row:
```
acl_rule · company_membership · company_invite · notification_outbox · ledger · godown · unit ·
voucher_type · account_group · security_level · company
```
✅ A failed reap inside `BEGIN/COMMIT` **rolls back cleanly**, so a wrong order costs a run and not a
half-deleted tenant. ⚠️ `account_group` self-references via `parent_id` and the single-statement DELETE
handles it. ⛔ **A tenant WITH BOOKS needs more than this** — `drive-l17-d95.mjs` therefore REFUSES to
reap one that has any voucher versions, and says so rather than deleting books.

### 5.19 ⛔⛔ A DISCLOSURE CAN BE FULLY PRESENT AND FULLY WRONG — DON'T ASSERT ON ITS PRESENCE
**L16's M3 is the cleanest evidence in the repo for this.** The Cash Flow screen's disclosure was mutated
to TOTAL its three settlement-date sources into one figure — the single design the screen exists to
refuse. The page still said **"THIS IS AN ESTIMATE"**, still carried the server's `method` sentence
verbatim, still listed all four `not_modelled` lines, still named the 30-day convention by its day count.
**Every one of those arms passed while the numbers lied.** Only the three per-source COUNTS and the
FOOTING caught it — 4 failures out of 34, exactly as predicted.
⛔ **SO A PROBE BUILT OUT OF "IS THE DISCLOSURE ON THE PAGE?" ASSERTIONS IS GREEN THROUGH A FALSE
DISCLOSURE.** Assert on the NUMBERS it discloses, and foot them against something that is not the same
handler. This generalises past disclosures: it is §5.10 ("answer from the row, never from the request")
applied to prose instead of a database column.

### 5.20 ⚠️ THE §5.11 VITE CHECK HAS A RACE, AND IT READS EXACTLY LIKE A DEAD MUTATION
M3's first `curl` of the mutated module out of vite counted **0** occurrences — which looks precisely like
"the mutation never landed", and the disciplined next step (§5.4) would have been to go find out what was
holding it up. **It had landed.** The curl beat the dev server's re-transform; a second curl counted 3
against a HEAD baseline of 0. ⛔ **CURL TWICE, or `grep -c` the file ON DISK first** — the disk said 3
immediately. A one-shot vite count is not evidence of anything.

### 5.21 ⛔ A STATUS CELL MUST CARRY EXACTLY ONE MARKER AND NO CELL SEPARATOR
§0's count snippet was hardened in position 0 to read the STATUS COLUMN instead of the line. **Writing
three rows in position 1 then broke it twice more, in two new ways, both in the ROW TEXT:**
- **D22's status cell contained a literal ` | `**, which split it into two cells. The last cell had no
  marker, so the row parsed as `?`.
- **D96's status prose contained the words "marked ✅"**, so the row parsed as CLOSED while saying OPEN.
⚠️ **The `?` bucket is what caught the first one.** A parser that silently binned it would have printed a
total that still looked plausible. **Fix the row, never the parser** — and re-run the count after writing
any row, not only after closing one.

### 5.22 ⚠️ CHECK YOUR OWN PROSE AS HARD AS YOU CHECK THE RECON'S
While correcting F11's feature note, L16 added "the Bank Reconciliation Statement" to the list of things
Alt+E exports. **It does not** — `grep -rln useExportAction frontend/src/screens` lists eight files and no
BRS screen is among them. Caught before shipping, by grepping a claim I had just written myself. §5.1 says
the recon's claims are unverified until fired; **the same applies to the sentence you are typing.** And in
the same edit a pinned count moved (`NINE` → `ELEVEN` reports in the Alt+G list) whose enumeration had
**already** been double-counting four of its own members before anyone touched it.

---

## 6. THE USER'S DECISIONS — 1-6 ✅ ANSWERED (DO NOT RE-ASK) · ⛔ 7 AND 8 ARE OPEN QUESTIONS

1. **T32:** ✅ **No public corpus of genuine Tally exports exists.** What GitHub carries
   (`TallyConnector`, `nodejs-tally-integration`, `PHP-to-tally-integration`, `tally-prime-api-docs`)
   is **hand-authored REQUEST envelopes** — the import direction, written by integrators, never
   produced by the program whose output T32 must parse. ⛔ **Do not let one into the repo as a
   fixture.** ✅ **THE REAL PATH: TallyPrime Educational** — free, unlicensed, indefinite, and it
   exports XML. ⚠️ In Educational mode voucher DATES are restricted to the **1st, 2nd and 31st**, so
   build fixtures on it for **SHAPE**, never for date-ordering or period boundaries. ⛔ **STILL THE
   COMMERCIAL BLOCKER until a file is actually in the repo.**
2. **D87's scope:** ✅ the three + PAN + credit days. **DONE (L15d).**
3. **D90:** ✅ refuse duplicates. ⛔ **Its premise was false; the user was re-asked with the real
   finding and chose to normalise all three master kinds. DONE (L15f).**
4. **D95 (the ACL counterweight for the forecast plane):** ✅ **DENY BOTH, MATCHING PRECEDENT. Answered
   2026-08-23.** The migration writes `disallow display` **and** `disallow print` for **both**
   `report:cash-flow` and `report:anomalies`, with **NO allow seeded** — identical in shape to 0072 (cost
   reports) and 0073 (order books). ⛔ **The reasoning the user endorsed, recorded so it is not re-litigated:**
   a forward cash position is a MANAGEMENT report, and a tenant that wants a senior clerk reading it writes an
   explicit `allow display report:cash-flow` through the admin API — a decision they make, visible in
   `acl_rule`, rather than a default nobody chose. ⚠️ **So do NOT seed an allow back**, and do not treat the
   two tokens differently.
5. **THE ALIAS THAT WAS DELIBERATELY NOT ADDED (L16, and it is a PRECEDENT not just a fact):**
   `"cash flow statement"` and `"funds flow"` are **not** aliases of the Cash Flow report. In Tally those
   name a HISTORIC statement of cash that actually moved; ours is a forward projection of invented
   settlement dates. ⛔ **AN ALIAS THAT SILENTLY ANSWERS A DIFFERENT QUESTION IS WORSE THAN NO ALIAS** —
   the operator reads projected figures as recorded ones. The picker leaves the phrase unmatched and they
   look again. ⚠️ **APPLY THIS TO D94:** `REPORT_ALIASES` currently maps `"bills payable" → "Outstandings"`,
   which is the RECEIVABLE report — the same defect, already shipped.
6. **D50's fourth arm** stays answered-with-disclosure. Raise again only if the user asks for a
   refusal — the only honest way puts the caller's own party names in the prompt, reversing the
   current safety posture.

---

7. ⛔ **D81's HALF B — UNANSWERED, AND IT BLOCKS HALF OF POSITION 1. ASK BEFORE BUILDING.**
   A server refusal must stop the voucher CREATE frame being replaced with a fresh empty one, but
   **`screenRefusal` is SYNCHRONOUS by construction while the accept POSTs ASYNCHRONOUSLY**, and
   AppShell forwards the key to the machine (`AppShell.tsx:316`) BEFORE it awaits `effect.pending`
   (`:321-330`). So the refusal is not knowable at the moment `onAccept` must return. **Three shapes,
   and this is a product decision, not a wiring choice:**
   **(a)** a new ASYNC-refusal field on `ScreenActionResult`, and AppShell defers the machine until
   `pending` settles — ⚠️ **changes keystroke latency for every screen**, not just this one;
   **(b)** make the voucher accept reducer STAY PUT unconditionally, exactly as the ALTER branch already
   does (`navMachine.ts:1281`), and let the screen advance the frame itself on success — ⚠️ narrower, but
   it moves who owns "the entry succeeded";
   **(c)** leave half B open and ship half A alone, disclosing the orphan without saving the keying.
   ⚠️ **Half A is buildable TODAY and needs no answer**, so position 1 can start either way.
8. **D96 — should insightd's statistical forecast exist at all?** ⛔ **A QUESTION BEFORE A BUILD.**
   cored's projection is explainable to an auditor bill by bill; a statistical forecast beside it invites
   exactly the confusion the Cash Flow screen's disclosure block exists to prevent. **(a)** its own report
   NAME (a different method is a different authority — and it would need its own ACL counterweight per
   D95); **(b)** DELETION, which closes the row for free and removes a maintained path nobody can reach;
   **(c)** leave it, documented as internal-only. **Raise it; do not quietly build it.**

## 7. WHAT L15-L17 CHANGED THAT THE REMAINING ROWS TOUCH

| Change | Who it affects |
|---|---|
| `screenRefusal()` / `ScreenActionResult.refused` (D92) | ⛔ **D81 half B** — live and correct, and **never reached**, because `onAccept` returns void. See §6.7. |
| the D89 guard keeping an ALTER frame put (`navMachine.ts:1281`) | ⛔ **D81** — half B is **CREATE-frame-only**; an alter arm passes today |
| `DraftAcceptFailedError` + `ledgersMintedByThisAccept` + the uncached snapshot | ⛔ **D81 half A** — the shapes to copy, and the cache trap that makes the diff come back empty |
| the ledger party block + 6 client-side refusals (D87) | **any probe creating a party through the FORM** needs a state code (§5.15) |
| `pan` + `credit_days` on `ledgerReq` and `GET /ledgers` | ⚠️ **D85** — `credit_days` is one of the three basis sources |
| migration **0075** — normalised master-name uniqueness | **D83** (same pre-check-vs-authority shape) |
| `masters` refusals are sentences, not driver errors (D93) | ⚠️ **D81 and D85 both assert on refusals** |
| ⛔ **`REPORT_NAMES` gained `"Cash Flow"` and `"Anomalies"` (L16)** | **D94** — `printTarget.ts`'s two `Record<ReportName,…>` tables are TOTAL, so **tsc names every registry you forgot**, and `REPORT_DOORS` derives from `REPORT_NAMES` and **mounts every report frame and fires Ctrl+P at it** |
| `FrameOverlays.tsx`'s `AS_AT_ONLY_REPORTS` / `FULL_PERIOD_REPORTS` | **D94** (payables is `as-at`) — ⚠️ **a name in NEITHER list is told its F2 period does nothing** |
| ⛔ **migration `0076` (D95) is the new highest restatement — FOURTEEN PERFORMs** | **D94's ACL migration is 0077 and derives from 0076**, not 0073 |
| the `forecast_report_acl_grants()` / `seed_data_entry_forecast_report_rules` pair | **D94** — copy it; ⚠️ a seeder must be named `seed_data_entry_\w+` or `seeder_census_test.go` cannot see it |
| `ForecastScreens.tsx` — two report screens, mock arms, and a 403-as-a-decision panel | ✅ **D94** — L16 built this exact shape twice and L17 proved the refusal panel against a real 403. **Copy it.** |
| `printTarget.test.ts`'s display-only arm now names `gstr1` | **D94** — only `gstr1`/`gstr3b`/`itc`/`reconciliation-queue` are still unrefused AND unimplemented; taking one narrows that set |
| `handler_print_test.go`'s `printDisallowedBySeededMigrations` (22 entries) | **D94** — a new seeded print refusal must **declare what it discloses** or the count assertion fails |

---

## 8. ALSO OPEN, OUTSIDE THE REGISTER — §7.3 B, C, D

Do not let these fall off the end. **B is a double-post and C is a tenancy leak.**

- **B** — the durable pending-confirm retry: one supplier bill, two vouchers, reproduced in a browser.
  The CONFIRM is fire-and-forget and only `console.warn`s, so if it fails after the voucher lands the
  only durable record of the acceptance is lost. Needs a durable retry or a transactional record —
  **a design decision, not a patch.** ⚠️ **Probably the same thing as D15's residue — §2 settles it.**
- **C** — a `?company=` that fails the client-side uuid regex is **silently discarded and somebody
  else's books are opened instead.** Real, populated books; nothing on screen looks wrong.
- **D** — a well-formed but NONEXISTENT `?company=` **wedges permanently**: cored answers 403 for a
  company that does not exist (membership is checked before any handler can 404), so
  `isDeadCompanyId`'s 404 arm is dead code against this backend. Needs an actionable notice.

---

## 9. AFTER THE REGISTER — the user's "once done" phase

`FeaturesScreen.tsx` is the honest ledger. **11 rows read `off`:** T8 multi-currency, T9 FIFO/LIFO,
T10 stock groups + compound units, T11 budgets, T12 GSTR-9, T13 interest, T14 backup/restore/split,
T16 per-company print templates, export-email/PDF, SSO, and ⛔ **T18 rule 48(4) QR, which is
PERMANENTLY unshippable — the payload is signed with the Income Tax Department's private key. If a
session reports T18 done, reject it.** **2 rows read `server`** (the payroll screens — pure frontend,
`internal/payroll` is already in the archtest allowed set — and offline sync).

⛔ **T32 Tally XML import is the COMMERCIAL BLOCKER** (§6.1 has the path).
⚠️ **T9 must be serialised with D8** (§3 row 5).
⚠️ **T10 widens D90's stock-item index** to `(company_id, group_id, lower(btrim(name)))` — a
**widening** migration that admits rows the current one refuses and can never fail on existing data.

---

## 10. ON PARALLELISM — asked, answered, re-examined 2026-08-22, unchanged

**The rule "reading fans out, driving does not" is TRUE but has been OVER-APPLIED.** The real
constraint is that there is ONE database, ONE cored, ONE vite and ONE tenant, and a mutation proof
**edits a file, rebuilds the binary and restarts it** — so two agents doing that concurrently
manufacture false green for each other.

⛔ **BUT EVERY ONE OF THOSE IS ALREADY AN OVERRIDABLE VARIABLE:** `CORED_PORT`, `INSIGHTD_PORT`,
`STATUTORYD_PORT`, `DATABASE_URL`, `ADMIN_DATABASE_URL`. Nobody has spent the hour to turn that into
`make stack-up SLOT=n`.

**What it would cost:** the Makefile part is easy; the work is that **the probe scripts hardcode their
endpoints**. ⚠️ **RE-COUNTED 2026-08-23 AFTER L16, WITH THE COMMAND, BECAUSE THIS NUMBER HAS NOW DRIFTED
IN EVERY DRIVER THAT QUOTED IT** (24/44, then 30/42, now this):
```bash
# the drive/probe scripts — the ones that actually block a second stack
/usr/bin/grep -rl 'localhost:8087\|localhost:5173' frontend/scripts | wc -l   # 28 files
/usr/bin/grep -ro 'localhost:8087\|localhost:5173' frontend/scripts | wc -l   # 43 occurrences
# the whole tree, minus node_modules and the .playwright-mcp ARTIFACT dir
/usr/bin/grep -rl 'localhost:8087\|localhost:5173' --exclude-dir=node_modules \
  --exclude-dir=.playwright-mcp . | wc -l                                     # 52 files
```
⛔ **DO NOT COUNT WITHOUT `--exclude-dir=.playwright-mcp`.** It holds Playwright trace artifacts and
reports **90,033** occurrences across 30 files — a number that would make this look like a rewrite. That
is the §5.13 lesson in a new costume: **the denominator is as easy to get wrong as the numerator.**
Beyond the scripts, `seed:demo` writes the single global
`/tmp/drive-company-id`, and migrating into a fresh empty database is a path nothing exercises. Note
`internal/platform/testdb` is a **teardown** harness over the SHARED dev database, not per-test
isolation — which is why even `go test` cannot safely run twice at once.

✅ **D91 IS THE FIRST STEP OF THAT WORK AND IT IS ALREADY DONE.** `contract:cored` now provisions its
own tenant through the product's own route. **That is the pattern the rest would follow.**

**What it would buy on THESE NINE — re-derived 2026-08-23, and it is now MORE than it was**, because the
two client-heavy rows that forced serialisation (D22+D52) are done: **D95** (a migration + a wire
oracle), **D49** (insightd, a separate service), **D8** and **D86** (backend-only — and "ALONE" stops
meaning "serial" once each has its own stack) and **D83** (backend-only) are all genuinely concurrent
once slots exist. ⚠️ **That is five of the nine, so call it ~3× rather than 5×:** the DEPENDENCIES in §1
do not go away when the stacks multiply — **D49 still has to finish before D85 starts, and D86 before
D94**, because those are orderings in the PRODUCT and not contention for a port. **D81 and D94 both
churn client screens and should stay serial with each other.**
**The user's standing decision as of 2026-08-22 is to keep going serially.** Raise it again only if
they ask.

