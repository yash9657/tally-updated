# D125 — W4 insight engine v1 and the morning digest: what is built, what is owed

**Written:** 2026-09-28, on branch `agentic/w4` (from `session/t32-and-close-7-3` @ `4e7cfff`), after the
code landed. **Every figure below was printed by a command run in this lane**, against the private
database `tally_w2_w4`; the command is named beside it.

Driver: `PLAN-AGENTIC.md` §3 W4 and RESEARCH-AGENTIC D8 (deterministic detectors, expected-value
ranking, capped digests, LLM narration from `payload_json` by key only). Binding rules:
`IMPLEMENTATION_PLAN.md` §1.1 (no new Go module, no new frontend package, core
`requirements.txt` untouched), migration block **0102–0104** (`PLAN-TALLY-PARITY.md` §2.5, row updated),
`PLAN-AGENTIC.md` §2.

⛔ **W4 IS FIVE ENGINEER-WEEKS AND THIS LANE BUILT THE DETERMINISTIC HALF OF IT, WITH GATES.** Fifteen
insight types are detected by Go, every row proves its own numbers, the ranker, caps, suppression,
feedback, digest, narrator with a number-provenance gate, scheduler, routes and inbox screen exist and
are tested. **Nothing model-backed is built** (§6): no LightGBM/SHAP, survival, BG/NBD, ALS, Chronos or
statsmodels forecast, no backtest, no CA panel, no WhatsApp. Nobody should read "W4" in a commit log as
"W4 done".

---

## 1. What landed

| Slice | Where | Lines (`wc -l`) |
|---|---|---|
| Type registry, evidence gate, store, detectors, ranker, digest, poller | `backend/internal/insights/` (NEW) | 3,493 impl / 777 test |
| Narrator + number-provenance check | `backend/internal/insights/narrate/` (NEW) | 309 / 297 |
| Routes (10, bare token `insights`) | `backend/internal/server/insights_http.go` | 345 / 144 |
| Migrations | `0102_insights.sql` (406), `0103_ledger_credit_limit.sql` (26) | — |
| Insights inbox screen + client | `frontend/src/screens/InsightsScreen.tsx`, `frontend/src/api/insights.ts` | 436 + 128 / 175 |
| Small edits | `forecast.NewServiceOver` (reuse a tx), `masters.Ledger.CreditLimit` + `POST /ledgers credit_limit`, `cored serve` starts the poller, archtest, tier0 pins, testdb fixture, Features row | — |

`git diff 4e7cfff --shortstat` (before this ADR): **49 files changed, 7,408 insertions(+), 34 deletions(-)**.
`git diff 4e7cfff -- backend/go.mod backend/go.sum frontend/package.json frontend/pnpm-lock.yaml
insightd/requirements.txt | wc -l`: **0**. insightd is not touched.

## 2. Decisions

- **D125-1. An insight that cannot reproduce its numbers is never stored.** Every candidate carries
  `evidence_sql` (a Go constant per type, never model text) + `evidence_args`; `store()` re-runs it
  (under a savepoint) BEFORE the insert and requires every evidenced payload key **decimal-equal** (no
  rounding; SQL must state its own) and the voucher set identical. A disagreement is `Refused`, counted
  in `insight_run.stats.evidence_refused` with the figure-by-figure diff, and the night goes on. The
  test gate proves the constants on a seeded book; the write path proves them on the tenant's book on
  the night. The 0102 CHECK `insight_evidence_is_one_select` is the first wall; `VerifyStored` runs in
  `RunInReadOnlyTx`.
- **D125-2. Where the detector's figure comes from a report, the evidence is a second, independent
  derivation.** `cash_shortfall_13w` takes its figures from `forecast.CashFlow` (i.e.
  `reporting.Outstandings`) and its evidence re-nets bill_allocation + opening bills + the On Account
  residue in SQL with the projection's date rule. `credit_limit_breach` reads `ledger_balance`; its
  evidence sums postings. `customer_slower_than_norm` takes pending from Outstandings; its evidence
  re-nets. Fact-backed types (price creep, reorder now, dead stock, customer slower's p90) read W3's
  **immutable** `entity_facts` rows by id (W3's gate reconciles them to their reports) and re-derive
  only what the detector computed.
- **D125-3. The real-time queue is a trigger on `voucher_head`, and detection runs after the commit.**
  Every write path (F-keys, agent dispatcher, sync, importer) moves voucher_head, so one SECURITY INVOKER
  trigger cannot be forgotten by a future path; it commits or rolls back with the voucher. NOTIFY is not
  durable, so it is the outbox + polling (the agent poller's shape). A detector can never refuse or slow
  a voucher.
- **D125-4. The nightly run is ONE transaction per (company, business day)**: advisory lock →
  `insight_run` existence check → `profile.Refresh` (W3's owed scheduler, now called) → 10 detectors →
  retire what cleared → `insight_run` insert. `profile.Refresh`, reporting and forecast are all handed a
  runner that REUSES the transaction (`forecast.NewServiceOver`, added), so a night reads one moment of
  the books (D122-9's rule). `insight_run_one_per_day` is the idempotency; the lock only saves the loser
  the work. Digest composition is a separate, idempotent transaction; narration runs OUTSIDE any
  transaction.
- **D125-5. The scheduler is the repo's pattern, not River.** `insights.Poller` ticks every 5 s in
  `cored serve`: `insight_event_pending_companies()` → `ProcessEvents` (claimed `FOR UPDATE SKIP LOCKED`),
  and after 02:00 IST `insight_nightly_due_companies(today)` → `RunNightly` → `ComposeDigest`. River is a
  module dependency (§1.1).
- **D125-6. The model never sees a value and never writes a number.** The narrator sends each item's
  title and payload KEYS with labels and units — no figure and no name (so no tenant data leaves for
  narration). The model returns `{{key}}` templates. `Accept` refuses any Unicode numeral
  (`unicode.IsNumber`: ASCII, Devanagari, full-width, superscripts, fractions), English / romanised-Hindi
  / Devanagari number and magnitude words, a `%` not after a pct key, an unknown or foreign key, broken
  braces, and a wrong line count/order; then renders from `payload_json` and runs `CheckRendered` — every
  number token in the rendered text must be a number token of that item's payload values (grouping
  commas ignored; a closed list of form names — GSTR-2B, Form 140, Rule 138 — is exempt). Any refusal →
  the deterministic template, recorded as `narrator='template'` with `narration_rejected`.
- **D125-7. One bare token `insights`, and it is GOVERNANCE to an agent.** Reads Display; verdict, digest
  and run-now Create; suppressions Alter. Default-denied for every seeded non-Owner level with zero
  acl_rule rows (0091 row). `actions/risk.go` classes the reads as governance reads and the writes as
  governance: the digest is model text, and an agent giving verdicts would train its own ranker. This
  also keeps `internal/agent`'s readsource table (W2-breadth's) untouched — exposing insight tools to the
  agent is theirs to decide (§6).
- **D125-8. The ranker's terms are stated as what they are** (`rank.go` header). impact = the
  evidence-checked rupee figure; p_real = Beta posterior per (tenant, type) from a **declared prior**
  (`TypeDef.PriorReal/PriorFalse`, this build's judgement — 19:1 for TDS-unpaid, 7:3 for the reorder
  heuristic) where "useful" and "not useful" both count as real and only "wrong" as false; actionability
  = declared constant; novelty = 0.5^k over the recipient's last 14 days of digests; affinity =
  2(1+u)/(2+u+n). A type with p_real < **0.70** (the plan's "false-positive rate below 30%") leaves the
  digest and stays in the inbox. Per-type weekly caps; ≤5 items is also a CHECK.
- **D125-9. A credit limit had nowhere to live.** 0103 adds `ledger.credit_limit` (NULL = no limit, 0 =
  no credit — deliberately distinct), through `masters.CreateLedger` (audited when set) and
  `POST /ledgers credit_limit`. No ledger-form field yet (§6).
- **D125-10. Supplier document numbers are normalised only by `itc.NormalizeDocNo`** (0032's rule). The
  duplicate-bill detector groups by it in Go; its evidence re-derives, from the books, that the named
  vouchers are live purchases of the party carrying those numbers as keyed, and the amount.

## 3. The fifteen types (`insights.Types`)

| Type | Cadence | Figures (evidence-checked) | voucher_ids |
|---|---|---|---|
| credit_limit_breach | on commit | credit_limit, balance, excess | the ledger's latest 200 vouchers |
| duplicate_bill | on commit | amount, bills_with_same_number | every copy |
| gst_place_of_supply_mismatch | on commit | tax | the invoice |
| hsn_rate_mismatch | on commit | line_rate, master_rate, taxable, tax_difference | the voucher |
| eway_bill_needed | on commit | invoice_value, threshold (Rule 138, at-or-above), penalty_floor (s.122: max(10,000, tax)) | the invoice |
| customer_slower_than_norm | nightly | p90_days (W3 fact), overdue_bills, oldest_days, overdue_amount | the bills' raising vouchers |
| supplier_not_in_gstr2b | nightly | invoices, itc_at_risk | those purchases |
| supplier_price_creep (≥ +5%) | nightly | first/last rate, drift_pct (W3 facts), qty_window, extra_cost | the purchases |
| customer_reorder_overdue (simple interval rule, NOT BG/NBD) | nightly | median_gap_days, days_since_last, last_invoice_date, invoices, typical_invoice | the invoices |
| stock_reorder_now (cover < 14 d) | nightly | closing_qty, velocity, days_of_cover (W3 facts), horizon, shortfall_qty, last_purchase_rate, reorder_value | the last purchase |
| dead_stock (≥ 90 d idle) | nightly | closing_qty (fact), idle_days, last_purchase_rate, value at that rate | the last purchase |
| cash_shortfall_13w | nightly | week_from/to, opening_cash, inflow/outflow through, closing_cash, shortfall | none — whole book (stated) |
| gst_liability_estimate (1st–20th, previous month, unfiled) | nightly | due_date, output_tax, itc_available, net_payable, itc_carried_forward | top 200 by tax |
| tds_not_deposited | nightly | due_date (7th / 30 Apr), days_late, deductions, unpaid_tds | the deduction vouchers |
| backdated_edits (> 30 d back, last 7 d, by user) | nightly | week_from/to, entries, vouchers, oldest_voucher_date, amount | those vouchers |

## 4. Gates and their figures

| Gate | Command | Result |
|---|---|---|
| **Evidence reproduces every figure** | `go test ./internal/insights/ -run TestEveryInsightReproducesItsNumbers -v` | `real-time: 26 events, 0 skipped, outcomes map[inserted:6 superseded:1]`; night 1 and night 2 with **no refusal**; **`verified 37 insight rows across 15 types, every figure equal to the paisa`** — the test fails unless all 15 types are among the verified |
| Tamper detection | `TestEvidenceGateCatchesATamperedPayload` | a payload +0.01 and a dropped voucher id both fail `Verify` |
| Idempotent night | `TestNightlyIsIdempotentPerCompanyAndDay` | second run reports `already_ran`, one `insight_run` row |
| Real-time queue | `TestAVoucherEnqueuesExactlyOneEventPerVersion` | 1 event per post, drained once |
| Ranker / feedback / suppression | `TestFeedbackMovesPRealAndSuppressionFilters`, `TestRankerTerms`, `TestDigestIsStoredOnceAndNoveltyDecays` | suppressed type leaves the digest; 3 "wrong" on a 7:3 type → p_real 7/13 < 0.70; second compose returns the first digest; yesterday's fingerprints at novelty 0.5 |
| **Number provenance** | `go test ./internal/insights/narrate/ -v` | **`23 adversarial narrations, every one refused`** (literal, grouped, equal-to-payload literal, `60k`, words, lakh/crore on a key, Hinglish, Devanagari digits and words, full-width, superscript, ½, "double", % on money, bare %, invented key, another item's key, broken/stray braces, a written date, a year, too few lines, wrong order); every one of the 15 templates passes both checks; a bad model answer falls back to the template with the reason; **no payload value of ≥ 4 characters appears in the request sent to the model** |
| Cassette replay from the input | `TestNarrateCassette` | key re-derived from `BuildRequest(items)`; replays through the real `llm.Client` and the real gate → `narrator=model` |
| Tier 0 | `make tier0` / `-run TestTier0 -v` | **`replayed 15 interactions across 5 cassettes in 3 directories, features [agent.decide extraction insight.narrate nlq.translate]`** (pins moved 4/14 → 5/15) |
| ACL | `TestDataEntryIsRefusedTheInsightEngine` | seeded Data Entry 403 on all 10 routes (after a Trial Balance control); Owner through on all 10 |
| HTTP end-to-end | `TestInsightsOverHTTP` | a limit-1000 party, a 1500 sale over HTTP, `POST /insights/run` → `balance 1500.00 limit 1000.00 excess 500.00`, evidence `reproduces: true`, digest `narrator=template` composed once, `wrong` dismisses, bad verdict 422, negative `credit_limit` 400 |
| Screen | `pnpm exec vitest run InsightsScreen` | `Tests 8 passed (8)`: Gateway and Alt+G doors, figures verbatim, U / W, S (refused without reason) / L, D, V (evidence + stored SQL), Tab + Enter drills into the voucher (alteration), 403 names the authority |

### Mutations run (each observed red, then restored)

| Mutation | Caught by |
|---|---|
| cash evidence drops the On Account posting residue | night refused: `closing_cash: payload -71952.00, evidence -73868.0000; inflow_through: payload 1888.00, evidence -71094.0000 …` and "no verified insight of type(s) [cash_shortfall_13w]" — ⚠️ **this mutation found a real bug first**: a refusal returned an error that rolled back the WHOLE night; fixed (`862a2ed`) so a refusal is counted and the night goes on |
| price-creep arithmetic `(last−first)×(qty+1)` | refused at write: `extra_cost: payload 2505.00, evidence 2500.00` |
| `Verify` compares nothing (`if false && !pd.Equal`) | "a payload one paisa off verified" |
| raw-text digit check off | "a literal that equals a payload value: ACCEPTED"; the model-fallback test sees the rendered wall catch `59,384` instead |
| rendered check off | "rendered check accepted `…59,385.00…`", "`Shop 22 Traders`", "`over by 6 %`" |
| suppression ignored by `SelectDigest` | "suppressed type backdated_edits still in the digest" |
| nightly idempotency check off | `duplicate key … "insight_run_one_per_day"` |
| "not useful" counted as false | "not-useful verdicts lowered p_real to 0.4667" |
| routeAction `/feedback` arm renamed | owner 403 + `route … is NOT mapped by routeAction` |
| `DROP TRIGGER voucher_head_insight_event` (live DB, then recreated) | "events for one posted voucher = 0, want 1" |
| frontend: navMachine's `insights` Enter arm disabled | the Tab + Enter drill test fails |

## 5. Residuals, stated

1. **Staleness is not wrongness.** An insight's evidence describes the books when it was found; a later
   voucher moves a balance and `V` / `GET …/evidence` answers `reproduces: false` with the diff. The
   nightly run retires a real-time insight whose evidence no longer holds and every nightly insight not
   re-detected.
2. **Cash evidence and currency-split refs.** The report nets a ref per currency, the evidence per ref;
   the due date could then differ. Such a night shows up as `evidence_refused`, never as a wrong figure.
3. **GST liability is books-basis.** Voucher-level ITC eligibility only (the ledger-level default and
   Rule 36(4)'s 2B cap are not applied); RCM excluded; monthly filers only (QRMP owed).
4. **Number words** in languages other than English / Hindi / Devanagari pass the raw check; "do" (Hindi
   two) is not listed because it is an English verb. The model is never told a value, so such a word would
   be invented, not copied.
5. **Priors are judgement**, not measurement; the 0.70 floor is only as good as the feedback that moves
   them. No false-positive rate has been measured on real books (§6 item 3).
6. `insight_nightly_due_companies` returns EVERY company without a run today; on a large tenant base the
   night is serial within one poller (per-company transactions, 100 per tick).

## 6. ⛔ OWED — every W4 deliverable this lane did not build

1. **Model-backed insights**: predicted-late invoices (LightGBM + SHAP), expected pay date (survival),
   BG/NBD reorder / P(alive), ALS + FP-growth cross-sell, festival uplift (StatsForecast / Chronos-2 with
   holiday covariates), 13-week cash p10/p90 (Monte Carlo over AR/AP timing). `insights.Owed` lists them
   with reasons and the list reaches the screen. No statsmodels forecast was added to insightd.
2. **Early-payment discount** (no bill carries discount terms) and **vendor bank-detail change** (no
   ledger ALTER path exists and a party has no payee account; design: compare a supplier's matched
   bank-line counterparty/VPA to its history, after W5's matcher uses rail_reference/counterparty).
3. **The plan's measurement gates**: P(late) AUC backtest, forecast ensemble vs seasonal-naive on rolling
   origin, the CA panel's 200-insight pairwise rating vs a rules-only baseline, and a measured
   false-positive rate per type before digest eligibility (today: the prior + feedback floor).
4. **WhatsApp delivery** of the digest — W6's. The digest lives in the inbox.
5. **Agent insight tools** — W2-breadth's: register `get_insights`/`get_digest` tools, give the reads a
   provenance class in `internal/agent/readsource.go` (the digest is model text → `stored_text`), and
   decide whether the governance classification in `actions/risk.go` relaxes for reads.
6. **Per-recipient digests**: the ranker supports a recipient (affinity per `given_by`), but the poller
   composes only the company digest (`recipient = ''`); a per-user morning is not scheduled.
7. **Bandit over templates/timing** (Recovering Difference Softmax), quiet hours, per-user daily caps.
8. **UI**: a credit-limit field on the ledger form; a suppression list with per-row lift (L lifts the
   latest); per-entity suppression from the screen (the route supports `entity_key`); the digest's
   per-item drill (the inbox list is the drill today).
9. **HSN directory** reference data (W5 owes it); the HSN detector uses the company's own item masters
   and says so.
10. **Duplicate bill without a supplier number** (same party/date/amount) and fuzzy matching (Splink).
11. **Supplier lead time** for reorder-now (W3 owes PO → GRN linkage); the 14-day horizon is a constant.
12. **The fix for `migrations.Apply` taking no lock** (HANDOFF-AGENTIC-WAVE-1 §4.1) is still owed; this
    lane ran on a pre-migrated `tally_w2_w4`.

## 7. Gate figures at the end of the lane

All on `tally_w2_w4` (pre-migrated, 80 migration files, max `0103_ledger_credit_limit`), all four DSN envs
pointed at it.

| Gate | Command | Result |
|---|---|---|
| build + vet | `cd backend && go build ./... && go vet ./...` | clean |
| offline build | `GOFLAGS=-mod=readonly GOPROXY=off go build ./...` | clean |
| no new deps | `git diff 4e7cfff -- backend/go.mod backend/go.sum frontend/package.json frontend/pnpm-lock.yaml insightd/requirements.txt \| wc -l` | **0** |
| full backend suite | `go test ./... -count=1 -p 4 -timeout 60m` | **exit 0, 49 packages ok, 7 with no test files** (was 47 at the wave-1 merge: + `insights`, `insights/narrate`) |
| Tier 0 | `make tier0` | ok; `replayed 15 interactions across 5 cassettes in 3 directories` |
| frontend types | `cd frontend && pnpm exec tsc --noEmit` | exit 0 |
| frontend tests | `pnpm exec vitest run` | **Test Files 127 passed (127), Tests 2027 passed (2027)** (was 126 / 2019) |
| corpora | `python3 corpora/validate.py` | OK, counts unchanged (`KNOWN_SCREENS` gained `insights`) |
| insightd | — | not touched; not run |

Two first-run failures of the full suite were real and are fixed in `2abc98f`: the stored-date cutoff
census required the two id-naming write routes (feedback, unsuppress) to be accounted for (added, with
the reason they move nothing in the books), and the restore round-trip found `insight_voucher_event`
doubled — the restore's voucher_head inserts re-enqueue through the trigger — so the queue is now in
`backup.notRestored` with that reason.
