# HANDOFF — WHAT CAN BE PICKED UP WITHOUT WAITING FOR ANYBODY

**Written:** 2026-08-27, at `fcc55c0` · **Branch:** `session/t32-and-close-7-3`
**Read time: 6 minutes.** ⭐ **Every number below was MEASURED today and the command is beside it.**

⛔ **THIS FILE ANSWERS ONE QUESTION:** *what is there to do right now that needs nothing from the repo
owner?* It names the next task, the one after it, and the traps that will cost you an hour each if you
meet them cold. §7 lists what IS blocked on a human, so that nobody re-discovers it as a surprise.

⚠️ **THIS IS THE 28th `HANDOFF-*.md`.** That is the problem `HANDOFF-FEATURE-LOCK-LINE.md` §10 records,
and adding to it needs a justification: this file exists to be the **only one a new session opens**.
§10 below says what it supersedes and what to delete.

---

## 1. ⭐ STATE RIGHT NOW — MEASURED, NOT REMEMBERED

```bash
git log --oneline -1                                          # -> fcc55c0
python3 docs/parse-defect-register.py HANDOFF-PARITY-AUDIT.md # -> 102 · 96 · 2 · 0 (+4 unreadable, all closed)
ls backend/migrations/*.sql | tail -1                         # -> 0080_multi_currency.sql
```

| Thing | Value |
|---|---|
| Defect register | **102 registered · 100 closed · 2 open** (`D99` open by decision, `D102` newly registered) |
| Feature roster | **54 rows: 45 `on`, 7 `off`, 1 `always`, 1 `server`** |
| Migration high-water | **0080** |
| Backend gates | `go build ./... && go vet ./... && go test ./... -timeout 60m` → **exit 0 at `fcc55c0`**, re-run today |
| Frontend gates | `tsc --noEmit` clean · **1784 tests / 109 files** · `verify:demo` **34/34** · corpora **OK** |

⭐ **THE MIGRATION PATH IS NO LONGER A BLOCKER.** D98 (a refused import was not atomic and the recovery
double-posted), D101 (vouchers Tally excludes were posted as live money) and D100 (the export recipe)
were fixed 2026-08-27 with mutation proofs whose failures were **predicted then observed** and with
browser re-drives (15/16 and 12/12). ⛔ **That was the item `HANDOFF.md` §10 called the commercial
blocker. It is closed.** A business can now bring its books in.

---

## 2. ⭐⭐ THE NEXT TASK: **T11 BUDGETS**. NOTHING BLOCKS IT.

The row argues its own case: *"A budget is a comparison dimension rather than a posting, so it changes
no voucher and no balance — which is exactly why it cannot be faked by keying journals: there is no
second column for any report to compare an actual against."*

⭐ **THAT SENTENCE IS ALSO THE ESTIMATE.** A budget posts nothing, so it inherits **none** of the
machinery that makes everything else in this repo expensive: no immutable voucher path, no hash chain,
no back-dated cutoff, no double-entry trigger, no GST derivation. It is a master plus a report.

**Scope, from `PLAN-TALLY-PARITY.md:472`:** budget-vs-actual on **groups, ledgers and — since T2
shipped — cost centres.**

### ⛔ THE ONE TRAP, AND IT IS A SECURITY TRAP: THE REPORT NAME

**A new report called `report:budget-variance` is READABLE BY DATA ENTRY THE DAY YOU SHIP IT, and you
will not notice.** Verified 2026-08-27 — `backend/migrations/0011_auth_acl.sql:249` seeds the Data Entry
level a **coarse rule with a BARE resource**:

```sql
VALUES (gen_random_uuid(), p_company_id, de_level_id, 'allow', 'display', 'report');
```

`internal/authz` matches that against **any** `report:NAME` at specificity 1 — so a report name is
**granted by default**, not denied. This is exactly **D95**, which was found the same way and is on the
register.

⛔ **SO T11 NEEDS AN ACL MIGRATION AND IT IS NOT OPTIONAL.** Contrast with T6, whose `tds-challan` and
`tds-profile` are **bare resource tokens** — Data Entry's `default_access = 'none'` denies those with
zero rules written. The shape decides the default, and `report:` is the dangerous shape.

⚠️ **AND THE POLICY QUESTION IS ALREADY ON THE ASK LIST** (§7 item 4): `0072` deliberately decided a
**cost report** is not for a data-entry clerk. A budget-variance report is the same class of question.
**Follow `0072`'s precedent — deny it — and say so in the migration body**; it is reversible with one
rule if the owner disagrees, and the failure direction matters: over-denying is a support ticket,
under-denying is a clerk reading the company's margins.

### ⛔⛔ TAKE THE MIGRATION NUMBER FROM THE END. THE RESERVATION TABLE IS A TRAP.

`PLAN-TALLY-PARITY.md` §2.5 calls itself *"the only authority"* and still reserves **T11 0056–0057** as
free. ⛔ **Those low reservations are abandoned in practice** — T8 did not take 0051, it took **0080**;
T9 took 0078 and T10 took 0079, not 0053/0054.

```bash
ls backend/migrations/*.sql | tail -1        # take the NEXT number after this
# derive any seeder restatement from the highest-NUMBERED one:
for f in backend/migrations/*.sql; do c=$(awk '/CREATE OR REPLACE FUNCTION seed_company_auth_defaults/,/^\$\$ LANGUAGE plpgsql/' "$f" | grep -c PERFORM); [ "$c" -gt 0 ] && echo "$(basename $f) -> $c"; done | tail -3
# -> 0072 -> 12 · 0073 -> 13 · 0076 -> 14      DERIVE FROM 0076 (14 PERFORMs)
```

⛔ **WHY A LOW NUMBER IS A SECURITY BUG AND NOT A STYLE POINT:** `migrations.Apply` runs files in
`sort.Strings` order and **the highest-numbered restatement of `seed_company_auth_defaults` is the only
body that survives**. A budget ACL written at 0056 is overwritten on every fresh database and **the
rule simply does not exist**. That is precisely the defect `0071` exists to repair, and it shipped
undetected once already. **Update §2.5 in place when you take a number.**

---

## 3. THEN **T12 GSTR-9**. ALSO UNBLOCKED — ITS INPUTS ARE BUILT AND DRIVEN.

GSTR-1, GSTR-3B and the ITC engine all exist and are driven. ⛔ **It is a RECONCILIATION, not a
derivation.** The row is explicit: it *"must be able to state where the three disagree instead of
smoothing it, which is why it is not a report we can generate by summing twelve months and calling it
done."*

⭐ **TREAT "WHERE THEY DISAGREE" AS THE DELIVERABLE, NOT AS AN ERROR PATH.** A GSTR-9 that reconciles
silently is worth less than one that names the twelve-rupee gap and which return it came from.

⚠️ Same ACL trap as §2 — it is a `report:` name. ⚠️ And keep the disclosure discipline: **not
filing-ready without portal-schema verification**, and the screen must say so.

Independent of T11 and of T8. Either order.

---

## 4. THEN THE STALE-EVIDENCE AUDIT — ONE SESSION, NO NEW CODE

Rows **20** (stock items/units), **23** (Stock Summary) and **26** (inventory vouchers) were driven
**before** T9 (FIFO/LIFO, `0078`) and T10 (stock groups + compound units, `0079`) landed underneath
them. **No defect is alleged.** The drives simply no longer attest to the code beneath them.

⭐ The method is settled and the tooling is committed: `frontend/scripts/audit-harness.mjs`, and
`HANDOFF-PARITY-AUDIT.md` §7.5 L27 is the worked example of an audit lane.

---

## 5. ⚠️ T13 INTEREST — THE BUILD IS BLOCKED, THE DESIGN NOTE IS NOT

The engine needs statutory citations that have to be fetched by a human (§7 item 2). **But the shape
can be settled today, and it is not what the plan says it is.**

⛔⛔ **THERE IS NO SINGLE "STATUTORY DEFAULT". THERE ARE TWO REGIMES, AND THE PLAN LINE
(`PLAN-TALLY-PARITY.md:474`, *"configuration with statutory defaults — cite them"*) IMPLIES ONE GLOBAL
SETTING. IT CANNOT BE ONE.** Checked 2026-08-27:

| Counterparty | What the law says | What the product must do |
|---|---|---|
| **A micro/small enterprise supplier** | **MSMED Act 2006 s.16: compound interest, MONTHLY RESTS, at THREE TIMES the RBI Bank Rate**, running from the appointed day (s.15's 45-day rule). ⛔ **ss.15–23 OVERRIDE any contract** — a clause saying "no interest on delay" is void | **Not configuration at all.** Fixed, mandatory, and not overridable in the UI |
| **Everybody else** | **No statutory default exists.** It is whatever the invoice terms say | Configuration with **NO default**, per party. Tally's own shape: simple/compound, 30-day month vs calendar vs 365-day year |

⭐ **SO "SIMPLE VS COMPOUND AND THE DAY-COUNT BASIS" HAS A DIFFERENT ANSWER PER PARTY, AND THE MSME
ANSWER IS MONTHS, NOT DAYS.** Building one global toggle would be wrong in both directions: overridable
where the law forbids it, and defaulted where nothing authorises a default.

⚠️ **THE RBI BANK RATE IS NOT THE REPO RATE.** They are different numbers and are conflated constantly.
It must be stored as **dated configuration**, not a constant — it moves, and interest for a past month
uses the rate notified then.

**Writable today:** `docs/design/t13-interest-model.md` carrying the two-regime split, the per-party
rule resolution, and the dated-rate table shape. Then the build is a one-sitting job once §7 item 2
lands. ⛔ **Do not build the engine first and retro-fit the citations** — that is how a number nobody
can defend ends up on a customer's demand letter.

---

## 6. ⚠️ D102 — ONE DECISION, THEN AN HOUR OF WORK

The production JWKS verifier **does not check `aud`**. Confirmed in the type, not inferred from a
comment: `authn.Claims` binds `sub`/`email`/`companies`/`amr`/`iss`/`exp`/`iat`/`jti` and has **no `aud`
member**, so the claim is never parsed. Pinning `iss` scopes acceptance to a *realm*, and every client
in a Keycloak realm mints tokens with the same `iss`.

**MEDIUM only because of deployment shape** — the JWKS path is live only when `AUTH_JWKS_URL` is set,
and nothing ships that way today. **It becomes HIGH the day anybody points it at a shared realm.**

⛔ **THE ONE DECISION: what happens when `AUTH_AUDIENCE` is unset?** `buildAuth`'s `AUTH_ISSUER` branch
is the precedent and it **refuses to boot**, with a sentence explaining why. Follow it.
⛔ **And `aud` must NOT be bound as a plain `string`** — RFC 7519 permits an array, which is the form
Keycloak emits, and a `string` field drops it silently.
⚠️ **Move the realm constraint out of the source comment** into `docs/SESSION_HANDOFF_WAVE2.md`, which
is the only operator-facing production recipe and currently says nothing about it.

⚠️ Whether this sits inside the feature-lock line is a judgement call the owner has not made: **Tally
has no SSO**, so it is not a parity item — but it is a defect in shipped code, not a missing feature.

---

## 7. ⛔ WHAT IS BLOCKED ON THE REPO OWNER — DO NOT SCHEDULE ENGINEERING AGAINST THESE

| # | Ask | What it unblocks | State |
|---|---|---|---|
| **1** | ⛔⛔ **IRP / GSTN sandbox credentials** | The **Rule 48(4) QR row** — the only item on the board that **no amount of engineering can close** | ⛔ **HARD-BLOCKED, AND WORSE THAN "SLOW".** See below |
| **2** | **MSMED Act ss.15–16 text + the RBI Bank Rate**, into `docs/statutory/` | **T13** (§5). Without them the engine invents numbers nobody can cite | ⭐ **NOT blocked by anything** — ordinary public documents, no registration, no geo-fence. **This is the ask worth chasing** |
| **3** | **One real business day on the app** | Still the highest-value item that is unmeasurable any other way | open |
| **4** | **Is a cost/budget report something a data-entry clerk should see?** `0072` says no, deliberately | A one-line ACL decision — business call, not technical. **§2 proceeds on `0072`'s precedent unless told otherwise** | open |
| **5** | CGST Rule 138 text · a JRE + Protean's RPU · EPF/EDLI charge notification | T5 citations · calling the TDS return *validated* · ~₹500/establishment/month accruing unbooked | open, see `HANDOFF.md` §10 |

### ⛔⛔ ASK 1 IS GEO-FENCED, NOT MERELY SLOW. MEASURED 2026-08-27.

```
www.nic.in                                → 302   ✅ NIC itself is fine
einv-apisandbox.nic.in  (164.100.69.129)  → TIMEOUT on 443 AND 80
einvoice1.gst.gov.in    (164.100.69.11)   → TIMEOUT          ← same subnet
www.gst.gov.in          (103.83.78.25)    → 200   ✅ commercial host
einvoice6.gst.gov.in    (18.65.5.167)     → 200   ✅ CloudFront (IRIS IRP)
```

Both failures are on NIC's **164.100.69.x** government subnet and are **silent drops, not refusals**,
while every GST host on a commercial CDN answers. That is a **source-IP geography filter**.

⛔ **DO NOT RE-DIAGNOSE THIS AS DOWNTIME, A BROWSER PROBLEM OR BOT-BLOCKING.** `docs/statutory/README.md`
documents a *different* failure — `incometaxindia.gov.in` 403s automated requests and a human with a
browser gets straight through. **This one is not defeated by a browser.** Routes around it: an
India-exit VPN, someone in India completing the one-time OTP registration, or a private IRP such as
IRIS at `einvoice6.gst.gov.in`.

⚠️⚠️ **AND IT IS AN ARCHITECTURE CONSTRAINT, NOT JUST A SIGNUP ANNOYANCE.** Production IRP access needs
the **calling server's outbound IP whitelisted**, so wherever this product runs must be able to reach
that fenced network — Indian hosting, an Indian egress path, or routing IRP calls through a GSP.
**That is recorded nowhere else and it affects the deployment story.**

⭐ **NOTHING ELSE IS BLOCKED BY ASK 1, AND THE QR ROW ALREADY BEHAVES CORRECTLY**: any invoice carrying
an IRN prints, in bold, *"QR code not printed - this copy is NOT the e-invoice to be issued under rule
48(4); obtain the signed QR from the IRP"* — because a QR generated from anything other than the IRP's
signed payload *"would be a forgery of a government signature"*. That is the right behaviour
indefinitely, not a stopgap.

---

## 8. ⛔ DELIBERATE NON-GOALS — DO NOT BUILD THESE, AND DO NOT COUNT THEM AGAINST THE LOCK

| Row | Why it is outside |
|---|---|
| **Split company data** | Tally splits by financial year because it loads the whole company into RAM (`RESEARCH.md:987`). **This product keeps unlimited multi-year history queryable at once, which is a thing it competes on.** Building split would be a workaround for a limit we do not have |
| **Single sign-on** | **Tally has no SSO.** Counting it against Tally parity is a category error. ⚠️ But its shipped half's defect is real and registered — **D102, §6** |
| **Offline app shell / service worker** | A product bet, not a parity gap — Tally is a desktop binary. The queue half **is** built and driven clean (23/23 + 27/27); the row honestly says `server` |

⚠️ **AND ONE REAL THING WITH NO ROW AT ALL: ARCHIVE-AND-CARRY-FORWARD.** Closing a financial year into a
**read-only archive company** plus a fresh one carrying opening balances — for statutory year-end and
for handing a closed year to an auditor. It sits next door to the split-company non-goal, it is **not
built**, and **the FeaturesScreen does not disclose it**. ⛔ **Give it a row whichever way it is
decided: an undisclosed gap is the one failure mode that screen exists to prevent.**

---

## 9. ⛔ RUN FIRST — THE TRAPS THAT COST AN HOUR EACH

```bash
# ports: cored 8087 · insightd 8088 · statutoryd 8091 · vite 5173 · postgres 5433
git log --oneline -3 && git status --porcelain
ps -o lstart= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)"   # NEWER than the last backend commit?
```

1. ⛔ **CHECK THE AGE OF `cored`.** `make run-cored` is `go run`, so a stale process is indistinguishable
   from a fix that does not work. **Found stale five times.** A cored restart **invalidates statutoryd's
   minted token**, so the two restart together, **cored first**.
2. ⛔ **VITE MUST BE ON `:5173`** — WebAuthn origin matching is exact.
   ⭐ **FIXED TODAY, AND THE FAILURE WAS INVERTED FROM WHAT YOU EXPECT:** 5173 was held by a **week-old**
   dev server while the process launched with `--port 5173` had silently fallen back to **5174**. Start
   it with **`--strictPort`** so it fails loudly instead of taking the wrong port. ⚠️ It binds `::1` —
   reachable as `localhost`, **not** `127.0.0.1`. The harness uses `http://localhost:5173/`, so this is
   fine; a script that hard-codes `127.0.0.1` will not be.
3. ⚠️ **`FeaturesScreen.tsx` IS SHARED WITH THE T8 LANE.** Announce before editing. Rows are one per
   line so conflicts resolve trivially; a silent double-edit does not.
4. ⚠️ **`make verify` DOES NOT RUN `tsc`**, and `tsc` does not run what `make verify` runs. Each has
   caught a real error the other missed. ⛔ `make verify` invokes `make migrate` — **the T8 lane owns the
   schema this week**, so check before running it.
5. ⛔ **T8 multi-currency is IN FLIGHT on another lane.** Working forward from `0080`. **Do not start
   it from here.** The row is deliberately still `off` because you cannot yet key a foreign-currency
   voucher, settle a foreign bill, or see an exchange gain or loss.

---

## 10. THE ORDER TO WORK IN, AND WHAT TO DELETE

1. ⭐ **T11 Budgets** (§2) — nothing blocks it. Take a migration number **from the end**, and write the
   ACL rule or Data Entry reads it.
2. **T12 GSTR-9** (§3) — independent of T11 and T8.
3. **`docs/design/t13-interest-model.md`** (§5) — the two-regime split, writable today.
4. **D102** (§6) — one decision, then an hour.
5. **The stale-evidence audit** (§4) — rows 20 / 23 / 26. One session.
6. **T13 build** — once §7 ask 2 lands.
7. **T8** lands on its own lane's schedule.

⛔ **Steps 1–5 are parallelisable across disjoint files.** The only hard ordering constraint anywhere is
between §5's design note and T13's build, and it is external (the citations), not technical.

### ⚠️ HOUSEKEEPING — READ THIS BEFORE ADDING A 29th HANDOFF

```bash
ls HANDOFF-*.md | wc -l    # -> 28, this file included
```

**Live and worth keeping:** `HANDOFF.md` (the fold-everything file) · `HANDOFF-PARITY-AUDIT.md` (the
defect register and drive ledger — the audit's actual product) · **this file** ·
`HANDOFF-T8-MULTI-CURRENCY.md` (until that lane lands) · `PLAN-TALLY-PARITY.md` · `RESEARCH.md` ·
`docs/design/*` · `docs/statutory/*`.

⭐ **`HANDOFF-FEATURE-LOCK-LINE.md` IS NOW DOWN TO ONE OPEN QUESTION** — §6, where to draw the lock
line — because its §7 steps 1 and 2 are done and its §3 defects are closed. **Everything else in it
that is still live has been carried into this file.** `git rm` it once the line is agreed.

⛔ **Deleting the other spent handoffs is a judgement about what documentation a team keeps and it
belongs to whoever owns the repo.** But it is a real cost: a session that opens the wrong one loses an
hour, and this repository has already shipped two false regressions and one "69 open defects" report to
a user that way. ⛔ **Re-derive the spent list before deleting — do not trust `HANDOFF.md` §11's eleven
names.**
