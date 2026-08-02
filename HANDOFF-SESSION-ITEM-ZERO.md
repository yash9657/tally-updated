# SESSION HANDOFF — Item Zero is paid. Start at §4 order 1.

> **This file is a SESSION handoff, not a plan.** The plan of record is
> `HANDOFF-FEATURE-LOCK.md`; read that for what to build next and why. This file exists only
> to tell you what happened in the session of **2026-07-31 → 2026-08-02**, what is trustworthy,
> and the traps that cost time. Delete it once you have read it and started work.

---

## 1. STATE — verified, not assumed

| | |
|---|---|
| Branch | `master`, HEAD **`50fea52`**, **tree clean** |
| Migrations | **0068** (applied; 0067 and 0068 each also `psql -f` re-run to prove re-runnability) |
| Backend | **1937 pass / 0 SKIP / 0 FAIL** (`go test ./... -count=1 -v`) |
| Frontend | **448 pass** |
| insightd | 107 pass |
| corpora | OK (tax=14 tds=15 tcs=5 keystroke=32 isolation=13 migration=6 forecast=4 inventory=6 gstr2b=17 ims=12 payroll=19) |
| `make verify` | **exit 0** |

**The long runs were bracketed** with a live `psql` query before and after, per
`HANDOFF-FEATURE-LOCK.md` §1.1. The numbers are real, not a skip census.

### Four commits, all of Item Zero

```
50fea52  step 4  the TDS/TCS register, and an honest refusal to file
a47ff8a  step 3  TCS on the post path and s.394(2) declarations
ecc9de5  step 2  record the deductor status from F11
ca58685  step 1  liability, cumulation and persistence on the post path
```

---

## 2. WHAT ITEM ZERO ACTUALLY WAS, AND WHAT IT IS NOW

Migrations 0049/0050 and three compliance engines shipped **tested and uncalled**.
`ledger.Post` still called the per-transaction `TDSDerive`. All four steps are now done:

1. **TDS post path** — deductor status read for the **preceding** tax year → `MustDeduct` →
   refuse when unrecorded → sum the s.393(1) aggregate → `TDSDeriveCumulative` → verify the
   posted leg → write `tds_deduction` **in the same transaction**, including zero-amount rows.
2. **Deductor status route + F11 panel** — `GET/PUT /deductor-status`, so liability can be
   recorded without raw SQL.
3. **TCS post path + s.394(2) declarations** — `TCSDerive` on Sales, persisted to
   **`tcs_collection`** (migration 0067), plus `GET/POST /tcs-declarations` and a second F11
   panel.
4. **The register + the return refusal** — `GET /tds-register`, migration **0068**'s ACL
   counterweight, and a screen at `Alt+G > TDS`.

**`FeaturesScreen`'s TDS/TCS row is still `off`, deliberately.** The numbers are correct,
stored and visible; the return cannot be filed and the deposit is not tracked. Do not flip it
until there is a register **and** a return.

---

## 3. ⛔ THE THREE THINGS I DID NOT BUILD, AND WHY. Read before "finishing" T6.

### 3.1 The return file — BLOCKED ON A HUMAN, not on work

`docs/design/t6-model.md` §6.5: the Form 140 layout and the Protean/TIN file format
**were never read** — `incometaxindia.gov.in` returns **403 to every automated request** from
this environment. Invariant **I-7** forbids emitting a file without a `fileFormatVersion`
traced to a read primary source.

Enough is known from commentary to produce something file-shaped (caret-delimited,
`FH`/`BH`/`CD`/`DD`, CRLF, 2dp amounts, 4dp rate, `ddmmyyyy`). **None of it is trustworthy on
the payment-code list**, and one of those same sources carried pre-Finance-Act-2025 thresholds
that the Act text disproves. **A wrong code files a contractor payment as professional fees
under a real person's PAN.**

So `reporting.ReturnFileRefusal()` returns a structured "no" that the register carries on every
response: `available:false`, empty `format_version`, a named blocker list with where to fetch
each document, and `validated:false` **always** — the FVU needs a JRE that does not exist here.

> **⚠️ IF YOU ARE ASKED TO "JUST GENERATE THE RETURN": the answer is no, and the reason is in
> `internal/reporting/tds_return_refusal.go`. Fetch the two documents first** (§7.3 of the
> plan; the URLs are in `t6-model.md` §6.5). It is a ten-minute browser task and it unblocks
> everything.

### 3.2 The challan register — NOT blocked, and it is the biggest remaining hole

No `tds_challan` table exists, so **nothing tracks what has been DEPOSITED** — and it is the
deposit, not the deduction, that stops interest running under s.396. The register says so in
its `disclosures` rather than showing a blank "Challan" column, because a blank would read as
"not deposited" for every row.

This needs no unread document. It is `t6-model.md` §1.5 and it is the honest next piece of T6.

### 3.3 Two TCS gaps, both over-collecting (the recoverable direction)

- **No seller-liability test.** s.394(1) charges "every person, being a seller"; that
  definition has not been read against primary text here, so every company that keys a `tcs`
  block collects. TDS has `MustDeduct`; TCS has no equivalent.
- **A credit note reversing a collected sale is not handled.** It needs a negative collection,
  which migration 0067 forbids by CHECK, and a return filing a negative line is a Form 143
  question.

---

## 4. THE FIVE TRAPS THAT COST TIME. Do not re-derive these.

### 4.1 ⚠️ A REAL UNDER-DEDUCTION WAS FOUND IN SHIPPED CODE

`TDSDeriveCumulative` returned early whenever the per-payment limb fired, so the aggregate limb
was never evaluated. Five ₹20,000 contractor bills (₹1,00,000, untaxed) then a ₹40,000 bill
collected **₹800 against the ₹2,800** due on the entire ₹1,40,000 — and the first five bills
stayed untaxed for ever.

**The tell was non-monotonicity:** had that sixth bill been ₹20,000 it would have caught up
₹2,400, so a *larger* payment collected *less* tax. Fixed;
`TestPerPaymentLimbDoesNotSuppressTheCatchUp` pins both the amount and the monotonicity.

This is `HANDOFF-FEATURE-LOCK.md` §1.2's lesson a second time. **When wiring an engine, check
the arithmetic at the seam — the unit tests beside it can all pass on a case the seam never
reaches.**

### 4.2 ⚠️ FOUR TESTS I WROTE WERE INITIALLY VACUOUS, AND ONLY MUTATION CAUGHT THEM

- An alter of **₹80,000 → ₹90,000** gives the *same* answer with or without the
  self-cumulation bug — wherever the prior base is fully taxed, the catch-up formula and the
  per-payment one agree. Rebuilt on sub-threshold bills, where they diverge.
- **Two HTTP tests were green while sending an invalid `dr_cr`.** Every domain refusal on that
  route is a 422, so a body malformed in an unrelated way passes a bare code check.
  **Asserting the status code alone proves nothing — assert the reason.**
- A screen test matched `/Form 140 layout/`, which appears in two elements. A substring shared
  by two elements asserts neither.

### 4.3 THE TWO TAX-YEAR RULES ARE OPPOSITE. Do not "make them consistent."

| Lookup | Year used | Statute |
|---|---|---|
| Deductor status | the year **BEFORE** the payment | s.402(37)(b), "the tax year immediately preceding" |
| s.394(2) declaration | the **SALE'S OWN** year | it governs the year it was furnished for |

Reusing `deductorStatusTaxYear` for the declaration lookup **silently ignores every declaration
ever filed** — mutation-proved. Both directions have named tests.

### 4.4 A PAN-LESS s.394(2) DECLARATION IS NOT "NO DECLARATION"

s.397(2)(f)(i) invalidates it; s.397(2)(g) then requires the **penal 5%** — not the base 2% and
not the exemption. Collapsing a NULL PAN into "absent" in the lookup charges 2% —
mutation-proved, and the failure message read `declaration: NONE`, exactly the collapse the
code comment warns about. `tcs_collection.declaration_outcome` is three-valued for this reason.

### 4.5 THE GATES THAT CAUGHT ME (they work — expect them)

- `testdb`'s teardown census — a new table needs a **`buildFixture` row** or five tests fail
  by name.
- `TestInternalImportBoundaries` — I widened `internal/masters → internal/compliance` with the
  reason recorded beside the ledger's identical edge. **If you prefer masters stay db-only, the
  fallback is a `compliance/deductor` service package alongside `gstr`/`itc`.**
- `printTarget.ts`'s `Record<ReportName, string>` — the TYPE SYSTEM caught the missing print
  slug. A new report name must be added there.
- The **deferred-print census** and the **seeded disallow-print census** both assert counts.
  Adding a report name bumps both; update the reasoning, not just the number.

---

## 5. DECISIONS I MADE THAT YOU MAY WANT TO REVISIT

1. **Split step 2.** The original bundled `/tcs-declarations` with `/deductor-status`. I moved
   declarations to step 3, because `TCSInput.Declaration` had no caller and a route for it
   would have been another helping of the exact "correct code nothing calls" Item Zero exists
   to pay off.
2. **No ACL migration for steps 2 and 3.** Data Entry's `default_access` is `none` and no
   coarse rule reaches `deductor-status` or `tcs-declaration`, so both are already refused.
   Migration 0068 WAS needed for the register, because 0011's coarse `allow display report`
   reaches every `report:NAME`. All three are pinned by auth-matrix tests.
3. **`deductor-status` and `tcs-declaration` are separate tokens.** One is a self-assessment
   deciding a year of our own withholding; the other is a document that arrives from a customer
   and zeroes tax on their sales. An owner may reasonably delegate one and not the other.
4. **No `return_code` column on `tcs_collection`.** The two TCS sections are distinct rows at
   distinct rates, so `section` determines the code uniquely and a later migration can backfill
   safely — unlike 194J-P vs 194J-D, which is why 0050 needed one.
5. **The register refuses an undated period** rather than defaulting to "this quarter". A
   correctly rendered register for the wrong quarter is worse than a refusal.

---

## 6. MACHINE — read this before a long run

Mid-session **Docker wedged**: `psql` and `docker ps` hung while the CPU sat 94% idle. Cause
was **host memory exhaustion** — OrbStack's VM was configured for 8 GB on a 16 GB box and macOS
had swapped it out from under itself. One mutation run burned ~10 minutes hitting the go-test
timeout against a frozen DB.

Fixed via the `machine-health` skill: 13 Spotlight exclusions applied, the **idle `ci-runner`
VM stopped** (load 0.00, no runner services up, holding ~4 GB), and the VM cap lowered
**8192 → 6144 MiB**. Compressor went 5.7 GB → 2.8 GB.

> **⚠️ `ci-runner` IS STILL STOPPED.** Start it before a push that expects CI:
> `orb start ci-runner`.

---

## 7. HOUSEKEEPING

- **`PLAN-TALLY-PARITY.md` §2.3 says NEVER `git add -A`.** I used it for steps 1–3 before
  reading that line (the commits contain only intended files — I checked `git status` before
  each). Step 4 was staged with explicit paths. **Follow the rule.**
- Migration blocks: **0067** and **0068** are consumed by T6 and recorded in §2.5. Group B's
  blocks now continue from **0069**.
- `docs/design/t6-model.md` §9.1's "eight of eight" claim was already known-false before this
  session (§7.2 of that memo) and I did **not** fix it. Still open.

---

## 8. WHERE TO START

`HANDOFF-FEATURE-LOCK.md` §4, **order 1**: **T29 bank statement import** (memo written:
`docs/design/t4-slice2-model.md`, migrations 0062–0064) — the other half of Wave 1.

If you would rather finish T6 first, **§3.2 (the challan register) is the piece that needs no
unread document** and closes the largest remaining hole in the feature.
