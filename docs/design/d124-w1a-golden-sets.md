# D124 — W1a: the golden sets, built with no spend and no keys

**Written:** 2026-09-28, **after the code landed and every figure in it was measured.** Lane W1a of
`PLAN-AGENTIC.md` W1, as split out by `HANDOFF-AGENTIC-AFTER-W0.md` §4.1: the suites are fixtures and
a harness; only the *runs* need providers. This records what was built, the decisions it took, how
each gate was proven able to fail, and what is **OWED** — stated, not buried.

⭐ Every figure below was printed by a command run on branch `agentic/w1a` against a private database
(`tally_w1a`), never the shared `tally` one. The commands are in §7.

---

## 1. What exists now

| Deliverable (PLAN-AGENTIC W1) | Built | Where |
|---|---|---|
| 200-task tau-style suite, en / hi / Hinglish, judged by **ledger state**, pass^5 | **Yes, complete.** 200 tasks; the harness runs any `Agent`; proven end to end against a real cored + Postgres | `corpora/tau/` (fixtures + `generate.py`), `backend/internal/evals/tau/` (harness) |
| Extraction set: 100 invoices + 50 statements (HDFC, ICICI, SBI, Axis, Kotak), field-level labels | **Yes, SYNTHETIC** (see §4) — 150 documents, field-level F1 scorer | `corpora/extraction/`, `insightd/insightd/evals/extraction.py` |
| 2–3k-row Hinglish narration embedding eval | **Yes, SYNTHETIC** — 2,342 judged rows over 1,278 narrations and 218 queries; recall@k / MRR / nDCG@10 / triplet scorer | `corpora/embedding/`, `insightd/insightd/evals/retrieval.py` |
| Bake-off across GLM-5.3 / DeepSeek V4 Pro / Qwen3.8 / Sarvam / Gemma; routing decision | **No — W1b**, blocked on handoff §6.1 (spend + keys) | — |

Census (`python3 corpora/validate.py`):
`corpora validation OK (tax=14, tds=15, tcs=5, keystroke=33, isolation=13, migration=6, forecast=4, inventory=11, gstr2b=17, ims=12, payroll=19, tau=200, extraction=150, embedding=2342)`
— the three new families are pinned **exactly** in `MIN_COUNTS`, like `gstr2b`, so a deleted case
fails CI instead of shrinking a denominator.

No migration (the W1a row in `PLAN-TALLY-PARITY.md` §2.5 says why), no route, no ACL rule, no new Go
module (`git diff -- backend/go.mod backend/go.sum` = 0 lines), no Python dependency, no frontend
change, no cassette (Tier 0 still replays **3 interactions across 2 cassettes**).

---

## 2. The tau suite — decisions

**2.1 A task is a seed, an utterance and an EXPECTED STATE.** `corpora/tau/<id>.json` carries the
starting book as ordinary API steps (`seed.steps`), the utterance, the expected canonical state of
every task-owned master and every voucher, and — for a question — `outputs`, the figures the answer
must state. Composition (printed by `TestSuiteReferenceAgentSolvesEveryTask -v`):

| family | tasks | | language | tasks |
|---|---|---|---|---|
| voucher_entry (sales/purchase GST, payment, receipt, contra, journal) | 50 | | en | 65 |
| item_invoice (sale 1–3 lines, purchase; stock seeded by a purchase) | 30 | | hi (Devanagari) | 68 |
| bank_match (clear a leg, book + clear charges, interest + two clearances) | 30 | | hinglish | 67 |
| gst_returns (GSTR-3B 3.1(a), post-then-3B, GSTR-1 HSN row) | 25 | | | |
| nl_query (trial-balance figures) | 30 | | | |
| master_creation (party with GSTIN, bank with opening, stock item) | 25 | | | |
| guard (must NOT write) | 10 | | | |

**2.2 ⛔ The expected state is COMPUTED, never RECORDED.** `generate.py` derives every leg in Python
`Decimal` (GST per line, HALF-UP at 4 dp, intra split in halves), and `validate.py` re-derives it a
second time with its own `derive_tax`. Recording the expectation from a run of this engine would make
the reference agent's pass a tautology. Computed, the pass is a claim: **the Go engine reproduces a
Python-derived state to the paisa on all 200 tasks.** On the first full run it did so on 195; the
other five were a harness decision (2.5), not an arithmetic disagreement.

**2.3 What the judge compares, and what it deliberately does not** (`state.go`). Ledgers: name,
group, GSTIN, state, opening. Stock items: name, unit, HSN, rate. Vouchers: class, date, every leg
(ledger, side, amount, and the **bank date** on a reconciled bank leg) and every stock line. ⛔ **Not**
voucher number, narration or the free-text `party` label: none moves a balance, and a judge that
compared them would measure how closely an agent imitates the fixture author's typing. Comparison is
a **multiset** of canonical JSON — a missing entity and an unexpected one both fail; there is no
partial credit.

**2.4 The judge reads through the agent's door.** `Snapshot` uses the same `Books` HTTP surface the
agent writes through (`/ledgers`, `/stock-items`, `/day-book`, `/vouchers/{id}`,
`/bank-reconciliation/{id}`), and `internal/evals/tau` is `{}` in `allowedDeps`: it cannot import the
ledger, so it cannot read the books by a door the agent does not have.

**2.5 Born-with masters are judged too.** The harness snapshots a fresh company *before* seeding and
requires those defaults unchanged; a task that legitimately changes one (Cash with an opening balance)
**restates it by name** (`overlay`, `judge.go`). This was the first run's 5 failures.

**2.6 The agent never sees the answer key.** `Agent.Run` receives a `Prompt` (id, language, today,
utterance) and `Books`; never the `Task`. The scripted agents receive their plans at construction.

**2.7 Three agents, three pinned counts** (`tau_db_test.go`). Reference must pass 200/200; **Null**
must pass *exactly* the 10 guard tasks (more would mean a task that cannot fail by doing nothing;
fewer, a guard whose seed the judge cannot see as unchanged); **Wrong** must pass 0 — it follows the
plan with one realistic slip per write (date +1 day, a transposed master name), says every figure one
rupee off, and on a guard task makes the tempting write (`naive_plan`: delete a voucher, pay an
injected "Urgent Vendor", bill the wrong one of two "Sharma"s, post after "don't post"). A write no
slip applies to is *dropped*, never executed as written — and `TestWrongAgentPerturbsEveryWrite`
measured **167 writes perturbed, 0 dropped**. The wrong agent's 200 failures were **200 clean runs,
0 errors**, so they are evidence about the judge's eye, not about crash handling.

**2.8 pass^k is the unbiased estimator** C(c,k)/C(n,k) averaged over tasks (`harness.go`), in exact
rational arithmetic. Proven through real trials, not only in a unit test: a *flaky* agent (reference
on even trials, wrong on odd) over one task per family×language cell (18 tasks, k=5) printed
`pass^1=0.6000 pass^2=0.3000 pass^3=0.1000 pass^4=0.0000 pass^5=0.0000`, and every trial is a fresh
company (trials sharing one is mutation M4 below).

Measured runs:
- `k=1`, reference: `agent=reference tasks=200 k=1 all-trials-passed=200 pass^1=1.0000` (5.8–14 s)
- `TAU_K=5`, reference: `tasks=200 k=5 all-trials-passed=200 … pass^5=1.0000 (46.0s)` — 1,000 companies
- null: `all-trials-passed=10 pass^1=0.0500`; wrong: `all-trials-passed=0 pass^1=0.0000`

**2.9 For W8 Tier 1/2 and W1b.** A model-backed agent implements `tau.Agent` in its own package and
reaches a model only through `internal/llm`; `tau.Run(ctx, Config{K: 5, Workers, NewCompany}, tasks,
agent)` returns a `Report` with `PassHatK(k)`, `By("family"|"language")` and per-trial reasons. The
company factory lives in the test (it must call `testdb.Cleanup`, which production code may not
import); a nightly against a live stack needs its own factory — owed, §6.

---

## 3. The answer reader — one decision worth recording

`AnswerFigures` reads `59,000`, `5,90,000.50`, `1.18 lakh`, `2 crore` and Devanagari digits
(`₹५९,०००`). ⛔ **"Cr" is NOT a multiplier**: in an Indian ledger `59,000 Cr` is a credit balance, and
reading it as crore would turn the most common way a balance is written into a figure 10⁷ too large.
Figures are **magnitudes** (a hyphen in prose is a date separator far more often than a minus); a
balance's *direction* is judged by the ledger state, not the prose.

⚠️ **Two limits the wave-1 review found.** (1) "every expected figure appears somewhere" was satisfied
by a *shotgun* answer — the whole trial balance — so a verbose model passed every read-only task. The
judge now also bounds the **money-shaped figures the task did not ask for** (₹/Rs, digit grouping, two
decimals, lakh/crore, or a plain integer of 4+ digits that is not a year) at one per expected output,
minimum one (`TestJudgeRefusesAShotgunAnswer`; the reference agent still solves every task). (2) For a
READ-ONLY task the ledger state is "nothing changed", so a wrong Dr/Cr in the prose is **still not
judged**. Fixing that needs a side per output in all 204 fixtures (`outputs` → `{amount, side}`) and a
prose side reader; it is OWED, not done.

---

## 4. The extraction set — ⛔ SYNTHETIC, and why that is stated five times

We cannot obtain 100 real invoices or any real bank statement, and inventing provenance for one would
poison every number measured against it. So all 150 are **generated**; each says `synthetic: true`, a
`SYNTHETIC` provenance line, and — on the page itself — a first line reading `SYNTHETIC TEST DOCUMENT
— NOT A REAL INVOICE` or `SYNTHETIC TEST FIXTURE — NOT ISSUED BY <BANK>`. Statement layouts follow the
column headings commonly seen on each bank's statements (order, date style, digit grouping); they are
not copies, carry no logo, and are labelled as *layout families*.

What makes them more than templates: five invoice layouts with **label vocabulary drawn per document**
("Invoice No.:", "Inv No:", "Tax Invoice No." …), two-column headers flattened onto one line,
**OCR-damaged amounts** on the noisy scans (0→O, 1→l, 5→S on the total and taxable value; the label
stays the true value and the evidence records the damaged surface), an **unprinted place of supply**
on half the retail memos (the label is the statutory inference from the GSTIN), three digit-grouping
styles, and **wrapped narrations** on SBI/Kotak. Every header label carries its `evidence` surface, and
`validate.py` proves it GROUNDED (the surface occurs in the text and normalises back to the label).

**Scorer** (`extraction.py`): TP/FP/FN per field after normalisation; a wrong value costs an FP *and*
an FN; a zero amount is empty (inventing an IGST on an intra-State bill is an FP); lines are aligned
greedily by agreeing fields before scoring. Normalisers stop at formatting: "Acme Traders" ≠ "Acme
Trading Co", and 05/06/2025 is 5 June.

Pinned floor (`test_regex_floor_is_pinned`, a deterministic regex reader in `baseline_extract.py`):
`micro_f1 0.877864, header_f1 0.80865, line_f1 0.895871, invoice_f1 0.748015, bank_statement_f1
0.954576`. Before the variation above was added, the same reader scored **header F1 1.0** — a set that
could not separate two models at the top — which is why it was added. ⚠️ This floor is **not** "the
current Opus pipeline" W5's gate names; that number needs W1b.

---

## 5. The narration retrieval eval — what it measures

1,278 synthetic narrations across four registers (bank strings `UPI/…/MSEDCL/…`, English voucher
narrations, Hinglish, Devanagari) over 30 intents; 218 queries (en 72 / hi 73 / hinglish 73); 2,342
judgments: grade 2 = the entry asked for (intent **and** party/month), 1 = right intent wrong key,
0 = a **judged hard negative** — the *direction* pair ("Sharma Traders **se** aaya" vs "**ko** diya")
and near-intents (electricity bill vs electrical repairs, GST paid vs refund, rent paid vs received).
Every grade-2 document of every query is judged (`validate.py` checks completeness), because an
unjudged relevant document is scored as a miss for the model that finds it.

Pinned figures (`test_evals_retrieval.py`): oracle 1.0 on every metric; seeded random `recall@10
0.007487, triplet 0.519728`; char-n-gram lexical floor `recall@1 0.931193, recall@10 0.293968, mrr
0.964832, ndcg@10 0.375904, triplet 0.558503, xlang_recall@10 0.124792`. **The shape is the finding:**
lexical matching finds the same-script paraphrase first and almost nothing across scripts, and its
direction test is barely above a coin toss. `xlang_recall@10` and `triplet` are the numbers W3's
embedding decision should be read on; MRR and recall@1 are flattered by construction and say so in
the module docstring.

---

## 6. ⛔ OWED — stated, not buried

1. **The bake-off runs (W1b).** No model has been scored on any of the three sets. Blocked on handoff
   §6.1 (spend, keys) and §6.4 (licences). `nlq.DefaultModel`/`DefaultProvider` stay unchanged until
   it has numbers.
2. **A real-document extraction set.** Consented, redacted, CA-reviewed invoices and statements. The
   synthetic set measures the scorer and the easy half of extraction; it cannot measure a real scan's
   layout entropy. No figure measured on it may be quoted as field accuracy on customer documents.
3. **Human review of the Hindi and Hinglish text.** Written by the generator's author, not by native
   speakers or by transcribing real user utterances. The utterances are plausible, not sampled.
4. **Breadth the suite does not yet have:** voucher alteration and credit notes, TDS/TCS, bill-wise
   allocation, multi-currency, statement *import* (bank_match tasks set bank dates on book entries;
   they do not ingest a statement file), units other than `Nos` (units are not in the canonical
   state), and multi-turn tasks (tau-bench's simulated user). NL query tasks read trial-balance
   figures only; period-filtered questions ("sales in May") are owed.
5. **A live-stack company factory** for a Tier 2 nightly (`CompanyFactory` over a real `*http.Client`
   plus a teardown that does not depend on `testdb`).
6. **No CI job runs `TAU_K=5`.** The default DB test is k=1 (200 companies, ~6–14 s); the k=5 figure
   above was run by hand.

---

## 7. How each gate was proven able to fail

Every mutation was applied, run, observed failing, and restored byte-for-byte (a scripted runner that
asserts the restore). Go runs used `ADMIN_DATABASE_URL`/`DATABASE_URL` → `tally_w1a`.

| # | Mutation | Caught by |
|---|---|---|
| M1 | judge ignores UNEXPECTED entities | `TestJudge` (extra/duplicate voucher, extra master judged passing); wrong agent then passed 7 guard tasks |
| M2 | snapshot never attaches bank dates | reference fell to **170/200** |
| M3 | answers never checked for figures | null passed **57** (pinned 10); wrong passed **47** |
| M4 | trials of one task share a company | harness failure on the 4th trial's seed (duplicate ledger) — loud, not a quiet pass^5 |
| V1 | validate_tau stops comparing tax legs with derive_tax | `poison-tax-leg-moved-to-sales` validated → check_poison red |
| V2 | extraction evidence need not occur in the text | `poison-ungrounded-invoice-no` validated → red |
| V3 | embedding completeness check removed | `qrels-unjudged-relevant` validated → red |
| V4 | a committed tau fixture hand-edited | `check_generator_drift`: "differs from generate.py's output" |
| V5 | F1: a wrong value costs only an FN | `test_a_wrong_value_costs_an_fp_and_an_fn`, pinned floor |
| V6 | recall@k over \|rel\| instead of min(k, \|rel\|) | oracle no longer 1.0; both pinned floors |
| V7 | date normaliser month-first | four normaliser cases and the pinned floor |

Poison tripwires (`_poison/`, run by `check_poison`): tau 6, extraction 4, embedding 3 — each rejected
for its **own** named reason (checked individually, e.g. `sales 2025-06-02: expected CGST leg
311.5000 but derive_tax gives 312.5000`).

Gates at HEAD of `agentic/w1a`:
- `cd backend && go build ./... && go vet ./...` — clean; offline build (`GOPROXY=off`) clean
- `go test ./internal/evals/... ./internal/archtest/ ./internal/testhygiene/` against `tally_w1a` — ok
  (tau 45.3 s)
- `CORED_SKIP_DB=1 go test ./...` — every package ok **except `internal/server`**, whose four
  GSTR-3B backward-compatibility pins refuse to skip under `CORED_SKIP_DB` by design
  (`handler_gstr3b_test.go:276`, `handler_itc_test.go:566`); untouched by W1a. The DB-backed full
  suite was not run: its DSNs are hard-coded to the shared `tally` database, which this lane must not
  migrate.
- `make tier0` — ok; `replayed 3 interactions across 2 cassettes` (unchanged)
- `python3 corpora/validate.py` — the census line in §1
- `cd insightd && .venv/bin/python -m pytest` — **197 passed** (166 before; +31 W1a)
- frontend — no file touched; not re-run
