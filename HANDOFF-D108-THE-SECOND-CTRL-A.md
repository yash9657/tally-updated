# HANDOFF — D108: A SECOND Ctrl+A KEYS A SECOND INVOICE

**Written:** 2026-08-31, working tree CLEAN at `be3eb52` · **Branch:** `session/t32-and-close-7-3`
**Read time: 9 minutes.** ⭐ **Every claim below was MEASURED and the command is beside it.**

⛔ **THIS FILE ASSUMES YOU KNOW NOTHING ABOUT THIS REPOSITORY.** It is self-contained.
§1–§2 get you a running app you can safely break. §3 is the defect. §4 is the mechanism.
**§5 is the decision you must make before writing code.** §6 is what must not break.

---

## 0. ⭐ THE ONE-LINE VERSION

**Key a sales invoice, press Ctrl+A twice, and the customer is billed twice.** Nothing is
re-keyed between the presses, nothing warns, and the screen looks identical before and after
the first press. Registered **2026-08-31** as **D108 (HIGH)** in `HANDOFF-PARITY-AUDIT.md`.

⭐⭐ **AND THE PRODUCT ALREADY SPECIFIES THE CORRECT BEHAVIOUR IN ITS OWN CORPUS.**
`corpora/keystroke/voucher-ctrl-a-accepts.json` says, in its description field:

> *"Ctrl+A accepts/saves the whole voucher screen and **returns to a fresh voucher entry
> screen** (Tally keeps the voucher form open for rapid bulk entry after save)"*

The keystroke machine **does** return a fresh frame. The form does not follow it. **You are
not designing new behaviour — you are making the product do what its corpus already says.**

⚠️ **CLOSING THIS VERY LIKELY CLOSES D107 TOO** (§4.3). They are one mechanism with two
symptoms. Do not fix them separately.

---

## 1. THE STATE — verbatim, and what each number should be

```bash
cd /Users/yashbhalgat/code/tally-updated
git log --oneline -1          # be3eb52 audit(D108-D114): the six undriven areas, driven…
git status --porcelain        # (empty)
```

**Gate baselines at `be3eb52`. Any red is yours.**

⚠️ **EVERY LINE BELOW IS RUN FROM THE REPO ROOT** and each `cd` is contained in its own
subshell for that reason — `cd frontend && … "$(cat frontend/.demo-company-id)"` looks right
and cannot work, because after the `cd` that path no longer exists.

```bash
(cd backend && go build ./... && go vet ./... && go test ./... -count=1)  # all green
bash backend/scripts/test_residue.sh                                     # OK: no residue
(cd frontend && pnpm exec tsc --noEmit)                                  # clean
(cd frontend && pnpm exec vitest run)                                    # 1836 passed / 113 files
(cd frontend && pnpm verify:demo "$(cat .demo-company-id)")              # 34/34  ⚠️ id is an ARGUMENT
node frontend/scripts/drive-t4-slice2b-matching.mjs                      # 35/35, tenant reaped
node frontend/scripts/drive-t12-gstr9.mjs                                # 33/33
node frontend/scripts/drive-t11-budgets.mjs                              # 36/36
python3 corpora/validate.py                                              # OK
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md            # 114 · 101 · 9 · 0 · 4
```

⛔ **THE BACKEND SUITE AND THE DRIVES NEED THE SERVICES UP (§2.1). The vitest and tsc lines
do not.**

⚠️ **THE REGISTER PRINTS `4 UNREADABLE` AND THAT IS NOT WORK WAITING FOR YOU.** D49, D80,
D82 and D84 are all CLOSED; the parser cannot read their status cells. ⛔ **Do not "fix" the
parser casually** — it needs its own pass. See `HANDOFF-USABILITY-DAY-TWO.md` §1.

⚠️ **`9 OPEN` is correct and is not rot:** D99 and D107 were open before, and D108–D114 were
all registered by the audit that produced this file.

---

## 2. ⛔ THE TRAPS THAT WILL COST YOU AN HOUR

### 2.1 ⛔⛔ A STALE `cored` IS INDISTINGUISHABLE FROM A BROKEN FIX — AND `pkill` MISSES IT

`make run-cored` is `go run`, so the listening process is a compiled child with a different
command line. **Found stale ten times before this file was written, including at the start of
the session that produced it** — cored had started at 15:34:40 and the last backend commit was
15:39:28, so the running binary was **288 seconds older than the code**.

⛔ **DO NOT USE STARTUP DURATION AS THE TEST.** With a warm Go build cache a genuinely fresh
`cored` also listens in about a second. ⭐ **CHECK THE PROCESS START TIME AGAINST THE CLOCK,
AND AGAINST YOUR LAST BACKEND COMMIT:**

```bash
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # is this NOW?
git log -1 --format='%ci' -- backend/                        # older than the process?
```

Kill by PORT, always, and start **cored first**:

```bash
kill -9 $(lsof -nP -iTCP:8087 -sTCP:LISTEN -t) 2>/dev/null
kill -9 $(lsof -nP -iTCP:8091 -sTCP:LISTEN -t) 2>/dev/null
sleep 2
nohup make run-cored > /tmp/cored.log 2>&1 &
for i in $(seq 1 90); do curl -sf localhost:8087/healthz >/dev/null && break; sleep 1; done
nohup make run-statutoryd > /tmp/statutoryd.log 2>&1 &   # AFTER cored
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"; date
```

⛔ **CORED FIRST.** A cored restart mints a new ephemeral signing key, invalidating the
service token `statutoryd` minted from it. Both failures present as a bare `401`.

**Ports:** cored `:8087`, insightd `:8088`, statutoryd `:8091`, vite `:5173`.

⚠️ **This defect is entirely CLIENT-SIDE.** You can reproduce and fix it with vitest alone.
You need the services only for the browser re-drive in §7, which you must still do.

### 2.2 ⚠️ VITE IS `localhost`, NOT `127.0.0.1`

`--strictPort`; WebAuthn origin matching is exact and vite binds `::1`. Use
`http://localhost:5173/`.

### 2.3 ⛔⛔ AN EMPTY `?company=` MINTS A FRESH TENANT AND EVERY SCREEN LOOKS CLEAN

The app bootstraps an empty company called **"My Company"** when no company is named. **Two
audit rows were wrongly logged as passing that way.** Always pass `?company=<uuid>`, and read
the **id**, not the name — there are ~95 companies called "Demo Traders".

### 2.4 ⛔ DIRTY A SCRATCH BOOK, NEVER THE DEMO ONE

```bash
(cd frontend && pnpm seed:demo -- --name "D108 Scratch")   # -> .d108-scratch-company-id
```

⭐ **The name and the id file move together.** With **no** `--name` the seeder writes
`.demo-company-id`, which every gate reads — that is how ten identically-named books
accumulated. After any session re-run `pnpm verify:demo "$(cat frontend/.demo-company-id)"`
and expect **34/34**. If it is not, you drove the wrong company.

⚠️ A scratch book seeded 2026-08-31 and already carrying D108's duplicates is
`7c3ab7e9-de38-45d7-b887-f348d91f93df`. **Make a fresh one.**

### 2.5 ⚠️ THE FIRST WRITE OF A SESSION CARRIES A WEBAUTHN CEREMONY

The Owner level requires step-up. In a hand-driven browser the first
`POST /v1/…/vouchers/*` returns **403** and the ceremony runs; that 403 is not a bug. Install
a virtual authenticator over CDP **before the first navigation**:

```js
const cdp = await page.context().newCDPSession(page);
await cdp.send("WebAuthn.enable", { enableUI: false });
await cdp.send("WebAuthn.addVirtualAuthenticator", { options: { protocol: "ctap2",
  transport: "internal", hasResidentKey: true, hasUserVerification: true,
  isUserVerified: true, automaticPresenceSimulation: true } });
```

⚠️ **THIS TRAP IS HOW D108 WAS FOUND.** The pending ceremony made it impossible to tell
whether the first Ctrl+A had landed, so a second was pressed. **That is exactly the operator's
position**, which is why the accident and the defect are the same thing.

---

## 3. ⛔⛔ THE DEFECT, AS MEASURED

Driven 2026-08-31 in a browser on scratch book `7c3ab7e9…`, **nothing re-keyed between the two
presses**, each a single keystroke. Oracle is `psql`, not the screen.

| Class | keystrokes | result |
|---|---|---|
| **Sales** | `F8` · Acme Traders · taxable `10000` · rate `18` · HSN `7326` · `Ctrl+A` · `Ctrl+A` | **Sales No. 4 AND No. 5**, each `10000.0000` + `900.0000` CGST + `900.0000` SGST |
| **Contra** | `F4` · HDFC Bank · Dr Cash `5000` · `Ctrl+A` · `Ctrl+A` | **Contra 2 and Contra 3** — the same 5,000.00 moved twice |
| **Receipt Note** | `Alt+F9` · Steel Suppliers · MS Angle `100` @ `45` · a third press on a screen already reading `Saved  No: 7` | **RN No. 8**; voucher count **24 → 25** |

```bash
psql "postgres://postgres:postgres@localhost:5433/tally?sslmode=disable" -X -A -F' | ' -c "
SELECT vv.voucher_no, vt.name, vv.taxable_value, vv.cgst, vv.sgst, vv.created_at
FROM voucher_version vv
JOIN voucher_head vh ON vh.company_id=vv.company_id AND vh.current_version_id=vv.id
JOIN voucher_type vt ON vt.id=vv.voucher_type_id
WHERE vv.company_id='<your scratch id>' ORDER BY vv.created_at DESC LIMIT 5;"
```

⛔ **THE COST IS NOT A TIDINESS PROBLEM.** One sale keyed, **two tax invoices on the books**,
the customer billed twice and the **GST output liability doubled**. There is **no delete path
in this product** — the ledger is append-only, so the recovery is a credit note, by hand,
after somebody notices.

⚠️ **THE DUPLICATES PROPAGATE INTO THE REPORTS THAT ARE SUPPOSED TO CATCH THEM.** Measured:
both Contras appear in the Bank Reconciliation un-cleared list and made a genuine bank row
**AMBIGUOUS** in the Alt+S matcher; both Sales invoices sit in Acme's ledger drill and in
Outstandings. The matcher's refusal to guess is correct — but the ambiguity it is refusing to
resolve was manufactured by this defect.

### 3.1 ⭐ WHY AN OPERATOR PRESSES Ctrl+A TWICE

**Because the screen after a save is identical to the screen before it.** Every field still
holds what was keyed; the only change is a small `Saved  No: 5` in the header — and **D107
established that the word `Saved` on this screen is not trustworthy**, because it also appears
when the server refused. An operator who is not sure whether it landed does the natural thing.

---

## 4. THE MECHANISM — verified line by line

### 4.1 The machine replaces the frame, and means to

`frontend/src/keystroke/navMachine.ts`, the `accept` reducer's create arm (~**1815–1826**):

```js
if (active.screen === "voucher") {
  const type = active.voucherType ?? DEFAULT_LAST_VOUCHER;
  const fresh = newVoucherFrame(type, "create", ctx.entryModeByType[type]);
  fresh.saved = true;                       // navMachine.ts:1824
  stack[stack.length - 1] = fresh;
  return { stack };
}
```

The comment above it states the intent: *"Ctrl+A accepts the voucher and re-opens a fresh form
for bulk entry."* ⛔ **THE ALTER ARM IS DIFFERENT AND MUST STAY THAT WAY** — an alter frame
keeps its identity (`return {}`), which is **D89**'s fix: a refused alter used to become a
create and mint a second invoice. **Do not touch the alter arm.**

### 4.2 The form does not follow, because it is component state

- `frontend/src/screens/VoucherScreen.tsx:1684` — `const [form, setForm] = useState<VoucherForm>(emptyForm)`
- `frontend/src/screens/vouchers/StockVoucherScreen.tsx:71-76` — five separate `useState`s
  (`lines`, `date`, `narration`, `party`, `voucherNo`)

Replacing the frame **object** does not remount the screen **component**, so every keyed value
survives. **The frame is fresh; the form is not.**

⛔⛔ **D81 ALREADY RECORDED THIS MECHANISM AND CLOSED IT AS GOOD NEWS.** Its row reads *"the
form is component state and the frame replacement never cleared it"* — and that was the right
call, because D81 asked only about a **refusal**, where keeping the operator's keying is
correct. **Nobody asked what it does on a SUCCESS.** Read that row before you start; it is the
reason this survived.

### 4.3 ⭐ THIS IS D107'S MECHANISM TOO — fix them together

`VoucherScreen.tsx:2899-2906` renders:

```jsx
{status ? (<span role="status">{status}</span>)
  : snapshot.saved ? (<span role="status">Saved</span>) : null}
```

`snapshot.saved` is the frame flag set at `navMachine.ts:1824`. Its own declaration
(`navMachine.ts:632`, mirrored onto the snapshot at `:2170`) says what it means:

> *"True once this voucher frame has been accepted at least once (Ctrl+A)."*

**That is a keypress fact, not a persistence fact.** D107 is that flag surviving onto the
"fresh" frame; **D108 is the data surviving onto it.** One mechanism, two symptoms.

⭐ **THE FACT YOU ACTUALLY WANT ALREADY EXISTS AND IS SERVER-DERIVED:** `savedNo` /
`savedVoucherID` (`VoucherScreen.tsx:1713,1726`; `savedNo`/`savedID` at
`StockVoucherScreen.tsx:76,83`), set **only** from a `VoucherResponse`.

⚠️ **`StockVoucherScreen` ALREADY PROVES THE ASYMMETRY IS AN OVERSIGHT:** `usePrintAction` at
`:362` reads `loadedVoucherID ?? savedID`, while the accept handler at `:253` reads
`ctxRef.current.voucherID`, which is `loadedVoucherID` and therefore **always null on a
create**. The same fact is consulted for **printing** and ignored for **saving**.

### 4.4 ⭐ THE CLEARING IDIOM ALREADY EXISTS, TWICE

`VoucherScreen.tsx` already clears the form in exactly this shape at **:1786** (the
class-change effect, F8→F9 on a create frame) and **:2739** (`onCancelVoucher`, Alt+X):

```js
setForm(emptyForm());
setStatus(null);
setSavedNo(null);
setSavedVoucherID(null);
setTaxResult(null);
```

`emptyForm()` is at `VoucherScreen.tsx:338`. ⚠️ **It resets the date to `today()`** — which is
right for Alt+X and is one of the decisions in §5. `StockVoucherScreen` has **no** such idiom
(no Alt+X handler at all), so its reset must be written.

---

## 5. ⛔⛔ THE DECISIONS — make these BEFORE writing code

### 5.1 What is cleared, and what survives

⛔ **NOT EVERYTHING SHOULD RESET, AND ONE OF THESE IS ALREADY A FIXED DEFECT.**

| | Decision | Why |
|---|---|---|
| party, amounts, lines, GST fields, bill refs, items | **CLEAR** | this is the defect |
| **entry mode** | **KEEP** | it lives in `ctx.entryModeByType` (`navMachine.ts:667`) precisely because a fresh frame carries no config and *"an operator keying a run of double-entry payments was silently reverted to single entry after EVERY save"*. ⛔ Already fixed once — do not re-break it |
| **voucher type** | **KEEP** | the reducer already passes `active.voucherType` into the fresh frame |
| **date** | ⚠️ **YOU DECIDE** | `emptyForm()` resets it to `today()`. **Tally keeps the date for the next voucher in a run** — a shopkeeper keying Monday's invoices on Wednesday re-types the date on every one if you reset it. Keeping it is a change to `emptyForm()`'s contract at its other two call sites, so it needs a variant rather than an edit |

### 5.2 ⛔ What the operator sees after a save — this is the D107 half

If you clear `savedNo` along with the form, **the screen shows nothing at all after a
successful save**, and the operator has less confirmation than before. That is worse.

⭐ **THE COHERENT ANSWER, and it closes both rows:**

1. **Clear the form data** — so a second Ctrl+A cannot duplicate.
2. **KEEP `savedNo` / `savedVoucherID` / `status`** — these are server facts. The header then
   reads `Saved · No: 5` over a **blank, ready** form, which is both a true confirmation and
   an invitation to key the next one.
3. **DELETE the `snapshot.saved` fallback branch** at `VoucherScreen.tsx:2903`. Once the
   confirmation comes from `savedNo`, the keypress-derived branch has nothing left to do —
   and that is D107.

⚠️ **D107's register row warns you not to delete that branch without establishing what it was
for.** This is that establishment: it existed so a frame reset after a real save still read
"Saved" once `status` cleared. **`savedNo` carries that fact honestly**, so the branch is
redundant rather than load-bearing — but say so in the commit message, because the row asks.

⚠️ **Decide when `No: 5` disappears.** Leaving it until the next save is defensible; so is
clearing it on the first keystroke of the next voucher. Pick one and pin it.

### 5.3 ⛔ Clear on CREATE only — never on an alter

The success handler is shared. On an **alter**, D89 deliberately leaves the frame in place so a
second Ctrl+A re-PUTs the same `voucher_id` (harmless). Clearing there would blank a voucher
the operator is still editing.

`VoucherScreen.tsx:1852` builds `editCtx` as `{ type, mode, voucherID? }` and the accept path
captures `const ctx = ctxRef.current` **synchronously before the request**, with a comment
explaining why. **Condition your clear on that captured `ctx`** — `ctx.mode !== "alter"` — and
not on a value re-read after the promise resolves.

### 5.4 ⚠️ There are THREE success sites, not one

| file | line | path |
|---|---|---|
| `VoucherScreen.tsx` | **2675** | the ordinary voucher accept |
| `VoucherScreen.tsx` | **2574** | the **draft** accept (Bill Inbox → F9), which also sets `setLoadedDraft(null)` |
| `StockVoucherScreen.tsx` | **272** | all four stock vouchers |

⛔ **The 2026-08-07 audit already recorded "a 2nd Ctrl+A posts a DUPLICATE voucher" on the
DRAFT path** (`HANDOFF-PARITY-AUDIT.md`, the row-30 drive log). It was never separately
registered and is the same defect. **Do all three or the row is not closed.**

---

## 6. ⛔⛔ WHAT MUST NOT BREAK — the controls, already checked

**Both were read against this fix on 2026-08-31 and neither blocks it.** Do not weaken either.

| control | what it asserts | why the fix keeps it green |
|---|---|---|
| `screens/VoucherScreen.gstAlter.test.tsx:533` — *"⛔ CONTROL: two accepts on a CREATE frame really do key two invoices"* | `keySale(10000)` then `keySale(20000)` → two POSTs | it **RE-KEYS different values** between the accepts. That is bulk entry and it is correct. It does **not** assert that an *unchanged* second accept posts twice. `keySale` sets party, taxable, rate and HSN explicitly, so it passes whether or not the form cleared |
| `keystroke/phase1.test.ts:304` — *"⛔ CONTROL: a CREATE frame still re-opens fresh for bulk entry"* | screen/mode/voucherType after Ctrl+A | a cleared form is still a create voucher frame of the same type |

⛔ **DO NOT "FIX" THIS BY REFUSING A SECOND ACCEPT.** That breaks bulk entry, and control 1
exists precisely to catch it. Its comment says so: *"a guard written as 'never accept the same
frame twice' would satisfy the test above and break exactly that."*

### 6.1 ⚠️ The `saved` flag itself is PINNED — do not remove it from the machine

| pin | what it wants |
|---|---|
| `corpora/keystroke/voucher-ctrl-a-accepts.json:13` | `"saved": true` after `F8, Ctrl+A` |
| `keystroke/keystroke.test.ts:213` | `expect(snap.saved).toBe(true)` **and** `expect(snap.field).toBe("date")` |

⭐ **Removing `ScreenFrame.saved` / `NavSnapshot.saved` would turn the corpus red.** You do not
need to: the flag is a legitimate *machine* fact ("this frame was accepted"). **What changes is
that the SCREEN stops rendering it as the word `Saved`.** Only three places touch it —
`navMachine.ts:1824` (set), `navMachine.ts:2208` (mirror onto the snapshot), and
`VoucherScreen.tsx:2903` (the render). **Delete the third only.**

---

## 7. DEFINITION OF DONE

- [ ] **A failing test written FIRST**, asserting the *unchanged* second accept posts **once**
      — the case neither existing control covers. Both cases pinned side by side: re-keyed →
      two vouchers; untouched → one.
- [ ] ⛔ **MUTATION-PROVED.** Revert the fix and watch the new test go red with the number the
      browser measured (**2** vouchers, not 1). A test that passes before the fix is not a test.
      This repo's register treats an un-mutated test as unproven — see D98 and D101 for the
      house standard.
- [ ] All **three** success sites done (§5.4), including the draft path.
- [ ] `StockVoucherScreen` too — it has no clearing idiom and its reset must be written.
- [ ] **D107 closed in the same commit**: the `snapshot.saved` fallback at
      `VoucherScreen.tsx:2903` deleted, with the commit stating **what it was for** and why
      `savedNo` replaces it honestly (§5.2). ⚠️ D107's row explicitly asks for this sentence.
- [ ] ⛔ **RE-DRIVEN IN A BROWSER, not only in vitest**, on a **fresh scratch book** (§2.4),
      on a cored verified fresh (§2.1). The oracle is `psql` voucher counts, not the screen —
      the screen is what lied in the first place. Drive **Sales, Contra and a stock voucher**,
      the three classes §3 measured, and assert the count rises by exactly **one** per keyed
      voucher.
- [ ] ⚠️ **Drive the CONTROL in the browser too:** key a second, different invoice after the
      first and confirm it still posts. Bulk entry is the thing most likely to be broken by
      this fix, and it is the thing a unit test is least likely to catch.
- [ ] `(cd frontend && pnpm verify:demo "$(cat .demo-company-id)")` still **34/34** — proof
      you dirtied the right book.
- [ ] Gates in §1 re-run and **stated in the commit message, with every deferral named**.
- [ ] `HANDOFF-PARITY-AUDIT.md` rows **D108 and D107** moved to CLOSED with the measurement,
      the mutation proof and the browser re-drive recorded in the house format.

---

## 8. ⛔ WHAT IS *NOT* IN THIS ROW — so you do not scope-creep

| Item | Why not now |
|---|---|
| **D113** GSTR-9 blocked on the demo book | HIGH and independent. ⛔ **Needs a DECISION first** — whether 3.1(a) may go negative at all, or whether an excess credit note carries forward the way the portal expects. Do not relax the comparison without that answer |
| **D109** exports flatten a two-level report | independent; a Level column is a small change but it is a format decision |
| **D110–D112, D114** | small truth-telling rows, unrelated mechanism |
| **Payroll** | the largest gap in the 2026-08-31 pass — the seeder creates **no employees and no pay runs**, so Pay Run, payslips, Form 138 and every EPF/ESI computation are still undriven. Its own session |
| **T13 interest engine · EPF/EDLI admin charges · portal verification · DPDP · T4 slice 2c** | all still blocked for the reasons `HANDOFF-USABILITY-DAY-TWO.md` §8 gives |
| **The 4 UNREADABLE register rows** | leave them. A parser rewrite that reconciles by counting looks right and classifies D13 off the wrong cell |

---

## 9. THE READING LIST, in order

1. **This file.**
2. `HANDOFF-PARITY-AUDIT.md` — rows **D108**, **D107**, then **D81** (§4.2: why the mechanism
   was seen and closed as good news) and **D89** (why the alter arm must not be touched).
3. `frontend/src/keystroke/navMachine.ts:1789-1826` — the reducer and its comment.
4. `corpora/keystroke/voucher-ctrl-a-accepts.json` — **the specification you are implementing.**
5. `docs/usability-day-2026-08-30.md` §6 — how D108 was found, and §6.6 for what that pass did
   not cover.
