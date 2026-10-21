# Agent parity implementation and evidence — 21 October 2026

This phase continues `codex/review-fireworks` from `f8964c6`. It adds working
capabilities and synthetic checks. It does **not** establish universal UI parity,
unrestricted account questioning, completed external collections, customer ROI,
or measured human time savings. The original evaluation evidence is unchanged.

## Implemented

- Planner (introduced in v9, current `agent-decide-v11`): concise summaries (250 whitespace words and 1,800
  letters/digits), expandable full drafts/details, and recorded repair feedback
  instead of silent truncation. Empty answers still fail. Internal UUIDs and the
  observed unsupported recovery-guarantee phrases are rejected. These are
  presentation checks, not a general semantic correctness or Hindi fluency judge.
- v10 marks reads before a later successful commit as potentially stale without
  rewriting stored evidence. It directs the planner to re-read authorized
  reports before quoting remaining balances/quantities, or confirm only the
  verified write if a read is unavailable. It also explicitly preserves English
  input language rather than inferring Hindi from Indian names/currency.
- Insight source references resolve to current voucher numbers and dates in the
  same authorized read transaction that verifies the evidence. They are capped
  at 20 and disclose omitted sources. Navigable source links remain a gap.
- Fixed the underlying profile vocabulary: receivable/payable totals can include
  unallocated on-account balances and must not be described as wholly bill-wise.
- Four additional approved master creates: account groups, units, cost
  categories, and budget headers. Each has its real request schema, rollback
  preview, audit attribution, permission-denial tests and exact-one-create
  postcondition. A budget header alone is not a completed budget with lines.
- Typed `bill_allocations` queries support bill/reference, debit/credit, current
  voucher number/date, due date and signed allocation movements. They exclude
  superseded/deleted vouchers and **exclude opening bills**. Independent checks
  cover an altered partial receipt, drill-down, tenant isolation and bound
  untrusted references. All three report permissions (day-book, outstandings,
  bills-payable) are required. The existing outstandings entity includes openings.
- Query execution now fetches one sentinel row and returns `limit`/`has_more`.
  The UI warns when a result is incomplete; the planner must narrow or aggregate.
  This is bounded retrieval, not cursor pagination or universal historical queries.
- NLQ prompt/schema `nlq-dsl-v5` includes allocations. Fake cassettes were
  regenerated and replayed; that is deterministic evidence, not live model quality.

## Inventory and remaining parity gaps

[`evals/agent-coverage-v9.json`](evals/agent-coverage-v9.json) is generated from
the real action registry, the generated UI command/client manifest and screen
source locations. Its test fails on drift. It includes request schemas,
permissions, the typed question catalog and concrete workflow slices.

| Capability classification | Backend actions |
| --- | ---: |
| Approval-mediated execution | 37 |
| Read | 85 |
| File/delivery gap | 46 |
| Human-mediated capability gap | 64 |
| Other unsupported writes | 27 |

The 259 actions, 287 UI commands and 40 screen kinds are different inventories,
**not denominators for a completion percentage**. UI commands remain unverified
until a browser/operator trace proves their complete conversational equivalent.
Unavailable UI commands are marked separately. Source call-site matching does
not detect every dynamically dispatched client method.

Outstanding families: UI navigation/company/date/filter controls; print/export
delivery; bill import/review; bulk reconciliation; payroll; tax exports/filing;
BOM completion; cost-centre creation (post-transaction readback is incompatible
with rollback preview); budget lines; administration/features/members/security;
import/backup/sync; approval and step-up handoffs. A refusal is not completion.
No wildcard privileges, self-approval, policy relaxation or service settings were
introduced. Godown creation has no current UI channel and was not enabled.

## Question/data coverage

| Question family | Current evidence path | Remaining limitation |
| --- | --- | --- |
| Opening versus posted/closing balances | Existing typed ledger/posting queries and report parity tests | Historical as-of semantics must use the appropriate report |
| Partial receipts and bill references | New allocation movement entity and existing outstanding report | Opening bill events are not allocation movements |
| Current versus altered vouchers | Current-version joins; altered-receipt independent oracle | Explaining historical edits needs audit-specific readers/tests |
| Orders and partial fulfilment | Authorized order/book reads; manufacturing completion test | No general joined question planner/oracle for every order scenario |
| Bank matching and cleared balances | Existing bank statement/reconciliation reads | Historical opening/cleared comparison holdouts still owed |
| Stock, location and valuation | Existing stock entities/reports; retail return test | Supplier price + returns + credits + location joined oracle still owed |
| Costs, BOM, tax and payroll | Permission-filtered action reads where available | No comprehensive field/question matrix or multi-step held-out coverage |
| Audit history and attachments | Existing product routes where authorized | Dedicated safe readers, attachment injection controls and answer coverage owed |

This catalog expands supported questions without exposing arbitrary model SQL.
It does not prove “any obscure account question.” Broad multi-step holdouts from
the handoff remain an explicit acceptance gap.

## Completed bookkeeping workflows

Both paths below use the real router and accounting domain. Agent decisions are
scripted in these deterministic tests; a real human approval endpoint releases
each preview. Ordinary API execution is the comparison path, not a browser or
human timing baseline. Readback and independent postconditions verify no extra
voucher, stock, master, order or bank-tick effects.

| Industry | Operator-confirmed event | Verified result |
| --- | --- | --- |
| Retail | Supplier accepted return of 20 of 100 units against a ₹25,000 bill | Approved ₹5,000 debit note; 80 units and ₹20,000 payable remain |
| Wholesale | Operator confirmed one ₹12,000 duplicate of two bills | Approved debit note clears only the duplicate reference; ₹12,000 remains payable |
| Services | ₹6,000 received against a ₹10,000 service invoice | Receipt allocated after approval; ₹4,000 remains |
| Manufacturing | Five units received against a 20-unit purchase order | Approved receipt note explicitly links order line; five units received and 15 remain open |

Tests: `TestAgentIndustryCompletion` and
`TestAgentCollectionWorkflowAndAPITiming`. These record confirmed events; they
do not prove supplier negotiation, physical delivery, external sends, bank
transfers, or end-to-end autonomous commercial resolution.

## Larger books and language evaluation

`scale-fixture-v1` has reproducible 1k/10k/100k constructors across retail,
wholesale, services and manufacturing, each in English, Devanagari Hindi and
Latin-script Hinglish. Background sales/receipts use 40 customers over 24 months;
original domain-valid target records are buried beyond the first sorted page.
The target records and rare findings are verified from product data, not planted
detector output. Background accounting volume is **not** a fully realistic
industry-specific inventory/workload distribution.

The 1,000-voucher tier has passed deterministic seeding, voucher-count, source
evidence and incomplete-first-page controls in all four industries. The 10k and
100k tiers are prepared but have not been validated or paid-run. The 100k tier
requires an explicit local-resource check. Larger tiers must pass offline checks
before live use. Locale/script, required evidence, unsupported money and
unchanged-books controls complement human review; they are not fluency oracles.

The original proposal is preserved in
[`evals/expanded-v9-plan.json`](evals/expanded-v9-plan.json): 49 initial cases
(28 original tasks, nine original business/holdout cases, 12 larger cases), then
24 more only if the larger tiers validate. Historical usage forecast: $3.02047
for the initial suite; $8.13589 at twice the all-tier empirical estimate. These
are planning numbers, not a provider invoice or guaranteed ceiling per suite.

## Budget authorization and controls

The user explicitly answered **“Authorize an additional $10”** to raising the
existing cap from $5 to $15. `scripts/evals/increase_budget.py` applied the change
under the same ledger lock, verified the prior cap and $4.88431 charge, retained
all prior report hashes, saved exact original bytes, and appended old/new cap,
authorization and prior hash. The Go reservation writer preserves that audit.
The original proposal's `paid_runs_authorized: false` is a historical snapshot,
not the current authorization status.

All paid runs use `/tmp/tally-fireworks-shared-budget.json` at the unchanged
conservative $2 per million total tokens. Provider prices were rechecked against
the [official model page](https://fireworks.ai/models/deepseek-ai/deepseek-v4p1-flash):
$0.30/M input and $1.20/M output; cache discounts do not enlarge the allowance.
Reservations are persisted before requests and uncertain calls remain charged.
Missing shared ledgers and existing report output paths now fail closed.

## Timing evidence and limits

`frontend/src/benchmarks/workflowTiming.ts` is opt-in local instrumentation:
clicks/keystrokes, observer-marked active work, elapsed time, approvals,
clarifications and corrections. It records no typed content. Human observations
reject synthetic input events. Automated samples have `activeHumanMs: null`.
The paired summary refuses advice/drafts, incomplete or mismatched final states,
duplicate trials and automation-derived human savings.

The development build exposes an opt-in observer at `/app?workflow-benchmark=1`
(normal sign-in still applies).
It mounts outside the measured application root, records incomplete trials, and
downloads JSON locally. Production builds exclude it. Supply pseudonymous
operator/task codes and independently checked starting/final fingerprints;
alternate path order and use identical synthetic books for paired trials.
Download before closing/reloading the page (samples are in memory).

The browser control in
[`evals/workflow-observer-control-v9.json`](evals/workflow-observer-control-v9.json)
switched one illustrative landing-page tab: exactly one product click, zero
completed executions, and null human time. Observer clicks were excluded. This
validates the recorder's negative control, **not an accounting UI workflow**.
No actual operator pilot or matched accounting browser benchmark has been
collected. The API benchmarks measure only system execution and verification.
**Human minutes saved, the proposed 50% gate, and customer ROI are unmeasured.**

## Validation and live result record

Local deterministic validation passed: full `go test ./... -count=1` with the
least-privilege database role and no database skip switch (including the 1k
fixtures); `go build ./...`; `go vet ./...`; 2,053 frontend tests across 131
files; frontend production build/typecheck; corpus validation; and eleven Python
budget/timing/evidence-export tests. The latest inventory was also regenerated and checked for
drift. Cassette replay was run with recording disabled. This macOS replay is
not a claim of Linux network-namespace Tier-0 isolation.

During development, tests caught a stale 33-action assertion, an attempted
unsupported godown preview, incorrect test-oracle column names, and missing
frontend test/type declarations. These were repaired before the corresponding
checks passed. They are distinct from live evaluation failures, which retain
their original outputs and verdicts.

The initial v9 live runs are retained: 28/28 state checks; nine business cases
at 8/9 original automated checks; two 1k multilingual smoke cases at 2/2.
Manual review found that bank-charge confirmations reused pre-clearing totals
and that `guard-007-en` answered in Hinglish. These are real quality defects
despite the state pass. The business failure was an oracle omission: two
₹18,500 supplier bills validly total ₹37,000. The updated oracle independently
computes per-ledger opening-plus-posting balances and tests wrong sums. It does
not retroactively replace the v9 verdict. Language checks are now reported
separately for original task trials; they remain heuristics, not fluency proof.

The v10 bank-reconciliation smoke passed all three language/state checks. Each
case re-read after committing; the fresh uncleared nets were ₹5,500, ₹18,000 and
₹70,000 (rather than the pre-clearing ₹4,910, ₹17,646 and ₹68,820). The final
answers used those fresh reports or confined themselves to the confirmed entry.
The final v10 deterministic agent/server/trajectory suite, build and vet passed
after sequential cassette regeneration/replay.

Do not run multiple server test processes against the same integration database:
their global agent pollers can claim one another's fixture runs. An overlapping
record/replay experiment caused failures; regeneration and verification must be
sequential or use separate databases. This does not apply to the paid runner's
independent disposable database.

The frozen v10 full task run passed **28/28 state checks** (141 provider calls;
396.1 seconds including task setup). Its original language heuristic passed
27/28 and caused a nonzero test exit. The flagged answer was “SAC 998313 (June
2025 GSTR-1 HSN summary): taxable value ₹68,000 aur total value ₹80,240.” Manual
review identifies a heuristic omission: the short Latin-script answer uses
the Hindi conjunction “aur,” which the connective list does not recognize.
The original flag is retained, not changed to a passing historical verdict.
The earlier English refusal now answers in English; the three bank-charge
confirmations no longer repeat pre-clearing totals.

Manual review of the other final task answers still found language limitations:
`guard-005-hi` uses wording about receiving payment from a supplier while the
scenario concerns paying a supplier; `guard-009-hinglish` says the user's money
is safe, broader than the verified fact that this run did not create a payment;
`ve-contra-001-hinglish` exposes the implementation label “L1.” These are answer
quality gaps despite correct state checks. No new paid repair is substituted
for the original full-run result.

The v10 business run passed 9/9 automated necessary checks, yet manual review
found unsupported return-option rankings: retail said a return “recovers the
most” without a margin hit; the holdout called it the “cheapest exit.” Its
uncertainty paragraph does not cure those earlier assertions. The v11 contract
rejects those observed unsupported claims in both summary and details, as well
as observed English/Hinglish/Hindi account-safety assurances. Its prompt requires
verified terms/costs before ranking returns, exchanges or clearance, clarifies
payable/receivable direction in translation, and uses plain approval status.
The language heuristic now recognizes the connective “aur,” with a regression
for the exact short v10 answer. These remain targeted controls, not general
semantic, translation or account-security proof.

Before v11 paid use, its full agent/server/trajectory tests, sequential cassette
replay, build and vet passed. The affected business/language/money grader tests
also passed. The backend Go source stayed frozen through the v11 task attempt.
The subsequent evaluation-only provider-stop fix changes the backend fingerprint;
the application planner and tools remain exactly those used by that v11 attempt.

## Final live evidence and provider block

The additive [public evidence](evals/agent-parity-2026-10-21.json) retains all
seven new run reports, full sanitized answers/details, original verdicts, source
fingerprints, validated usage and provider status/error codes. The original
`evals/fireworks-2026-10-21.json` is byte-identical to the starting branch.

| Run | Original recorded result | Necessary qualification |
| --- | --- | --- |
| v9 1k smoke | 2/2 business checks | Retail Hindi and manufacturing Hinglish only; advice/drafts |
| v9 original tasks | 28/28 state | Manual stale-bank-total and English-language defects retained |
| v9 business | 8/9 necessary checks | Original aggregate-money oracle failure retained |
| v10 bank smoke | 3/3 state/language | Fresh post-commit reports, correct clearing semantics |
| v10 original tasks | 28/28 state, 27/28 language heuristic | Short Hinglish “aur” false flag retained |
| v10 business | 9/9 necessary checks | Unsupported retail option rankings found manually; not a complete quality pass |
| v11 original tasks | 17/28 state | Provider account suspension interrupted the suite; not a completed 28-case validation |

During v11, Fireworks returned HTTP 412 `PRECONDITION_FAILED`, stating that the
account was suspended and citing a possible monthly spending limit or unpaid
invoices. The precise billing cause was not investigated or changed. The first
17 tasks completed with passing state/language checks. Eleven later requests
returned 412 and produced incomplete tasks; the original report also flags seven
of their empty Hindi/Hinglish answers. Those are unavailable answers, not evidence
of a language-regression rate. No further paid suite was launched.

In the completed v11 samples, `guard-005-hi` now correctly describes paying the
supplier and `guard-009-hinglish` limits its assurance to this run creating no
payment. The concise “aur” answer passes the updated language check. The business
ranking repair has deterministic regression evidence but **no v11 live business
validation**. The final 9-case business rerun and full 12-case 1k multilingual
suite remain blocked. Only the earlier two 1k smoke cases were paid-run; the
prepared 10k/100k tiers remain unvalidated and unrun.

The evaluation transport now stops additional calls/reservations after a terminal
401/402/403/412 response, preserves the failed call's reservation, and reports an
explicit incomplete/not-attempted reason. Its offline controls pass with and
without the live build tag. This change makes **no provider request** and does
not modify account settings or release earlier charges.

The same ledger reconciles exactly: prior **$4.88431** + new **$5.486996** =
**$10.371306 charged against $15**, leaving **$4.628694**. New runs made 470
requests: 459 had validated usage (2,493,856 tokens), and eleven 412 responses
retain **$0.499284** in uncertain reservations. Across old and new evidence:
1,025 requests, 4,936,011 validated tokens plus those eleven reservations. These
are conservative accounting amounts, not a Fireworks invoice. The suspension
is separate from the authorized shared cap; do not reset the ledger or raise
service limits to resume. Account-owner resolution is needed before paid work.

Do not replace historical verdicts with repaired scores. Repository CI on this
local revision has not run remotely; the previous green CI belongs to `f8964c6`.

GitGuardian dashboard classification remains pending. No merge, deployment,
external sending/payment or external service setting change was performed.
