# D119 — the agent plane's foundations: five decisions W0 took, and the one line of research it overruled

**Written:** 2026-09-08, **after the code landed and every figure in it was measured.** Five build
lanes wrote `internal/trajectory`, `internal/llm`, `internal/actions`, the frontend UI-command
manifest and the `internal/nlq` re-point; this document is the integrator's record of the decisions
those lanes were given, what upheld them in the tree, and the one place where the answer is
**green on weaker evidence than the deliverable asked for, and says so in its own provenance** (§7).

⚠️ **§7 AND THE COUNTS IN §4 WERE REWRITTEN AFTER THE ADVERSARIAL REVIEW PASS, AND THE FIRST DRAFT OF
BOTH WAS WRONG IN THE SAME DIRECTION.** As first written, §7 recorded `./internal/actions` as red for
want of a UI join key, and §4 reported "95 cutoff-judged" as a measurement. The review then closed the
gate on a client-call census (§7) and deleted the cutoff column as counterfactual (§4). Both figures
are restated here rather than patched quietly, because a design record whose numbers no longer match
`registry_gen.go` is the artefact this repo trusts least.

⭐ Every claim below carries a `file:line`. Where a figure is a count, it is the number a command
printed, not a hand-derivation — the two disagreed twice and both times the command won (§6).

---

## 1. ⛔ THE DEPENDENCY RULE HELD, AND `RESEARCH-AGENTIC.md:217` DID NOT

`RESEARCH-AGENTIC.md:217` says, in terms: *"Go emits `gen_ai.*` spans via the plain OTel Go SDK to
Langfuse's OTLP/HTTP endpoint (no gRPC)"*. **That sentence was not followed.** It loses to
`IMPLEMENTATION_PLAN.md:25` §1.1, which is binding and unambiguous: *"**Go (`backend/`):** NO new
module dependencies."*

This is not a tie broken by seniority. It is resolvable without losing anything the SDK was wanted
for, and the research says so itself two documents down:

- `docs/research/agentic/r8-eval-guardrails-ops.md:19` — Langfuse accepts **"OTLP/HTTP (JSON + protobuf) at
  `/api/public/otel`"**. The JSON encoding is a first-class OTLP transport, not a fallback.
- `docs/research/agentic/r8-eval-guardrails-ops.md:32` — *"For Go, the realistic path is the plain OTel Go SDK
  emitting `gen_ai.*` attributes yourself (**a ~200-line helper**)"*. The helper was always going to
  be ours; the SDK was buying batching, an ID generator and a sampler.

So `backend/internal/trajectory/otlp.go` is a complete OTLP/HTTP protobuf-JSON exporter in stdlib:
**700 lines, 487 of them code**, holding the batcher (one goroutine, 512 spans or 5s), the
bounded queue whose `default:` arm makes `Record` never block, head-based sampling on the trace id,
and the three encoding rules that are easy to get silently wrong — 64-bit ints as decimal **strings**,
ids as lowercase hex, and `AnyValue` as a true one-of with **pointer** fields so that
`output_tokens = 0` survives instead of being eaten by `omitempty`.

**What the module gained: nothing.** `backend/go.mod:5-12` still lists the same six direct
requirements it listed before W0 — chi/v5, google/uuid, pgx/v5, shopspring/decimal, zeebo/blake3,
golang.org/x/text — and `git diff -- backend/go.mod backend/go.sum` is empty. The OTel Go SDK would
have pulled `go.opentelemetry.io/otel`, `.../sdk`, `.../exporters/otlp/otlptrace/otlptracehttp`,
`google.golang.org/protobuf` and `google.golang.org/genproto` into a 40-line `go.sum` — five module
trees and their trivy surface, to replace 487 lines this repo can read.

⚠️ **The cost is stated rather than hidden.** No W3C trace-context propagation, no OTel
`Baggage`, no auto-instrumentation of `net/http`, and a sampler that reads the **first** 4 bytes of
the trace id where the OTel spec reads the last 7. crypto/rand ids are uniform so the distribution
is identical, and nothing outside this process consumes the decision — but if a future Collector
needs consistent-probability propagation, that is the one line to change, and it is commented in
place.

---

## 2. THE PACKAGE LAYOUT, AND WHY `llm` DOES NOT IMPORT `trajectory`

Three new packages, three entries in `backend/internal/archtest/boundaries_test.go`:

| package | allowed imports | line |
|---|---|---|
| `internal/trajectory` | **{}** — the empty map | `boundaries_test.go:267` |
| `internal/llm` | `internal/platform/db`, and nothing else | `boundaries_test.go:289` |
| `internal/actions` | `internal/authz`, and nothing else | `boundaries_test.go:313` |

**`internal/llm` does not import `internal/trajectory`, and that asymmetry is the decision.**
`backend/internal/llm/trace.go:71-73` declares the one-method interface the package consumes:

```go
type Recorder interface {
	StartLLMCall(ctx context.Context, s SpanStart) (context.Context, LLMSpan)
}
```

`internal/trajectory` satisfies it structurally, and `backend/internal/server/server.go` — the
composition root, the only package archtest lets import both — writes the adapter
(`trajectoryRecorder`, ~40 lines including its reasons). Accept interfaces, return structs.

The payoff is not tidiness. It is that **a test of either package needs no fake imported across a
boundary**: `internal/llm`'s suite asserts on tool calls without a telemetry package in scope, and
`internal/trajectory`'s asserts on span encoding without a provider. And it is that a deployment with
tracing off links no exporter at all.

⛔ **The empty map on `internal/trajectory` is load-bearing in both directions.** Outward: `llm`,
`actions`, the agent loop and eventually any handler will import it, so any sibling edge it gained
would be transitively linked into all of them — and the first tempting edge is `internal/platform/db`,
*"so a span can look up the company name"*, which puts a database round-trip on the telemetry path,
where a slow pool becomes a refused voucher write. Inward: it may be imported by anyone, which is
only safe **because** it can reach nothing.

⛔ **`allowedDeps["internal/query"]` was NOT touched** and stays `{"internal/platform/db": true}`
(`boundaries_test.go:226`). `internal/nlq` gained `internal/llm` (`boundaries_test.go:244`) and that
is the only place the edge may appear. The reason is stated at the entry: `TestQueryPackageIsNetworkFree`
(`boundaries_test.go:956-988`) scans only `internal/query`'s **own** files for `net`/`os/exec` and
does **not** follow transitive imports, so adding `internal/llm` to query's set would hand the SQL
compiler a socket and leave that test green while it happened. **The line in `allowedDeps` is the
real proof of the invariant; the test is a second opinion.**

⚠️ **A new package needs TWO edits.** Its own entry passes when the package is tested alone; the
missing line in `internal/server`'s set fails only in the full suite — `boundaries_test.go:670-673`
says so in terms, *"which is how it gets missed"*. All three are in server's set at
`boundaries_test.go:679-702`.

---

## 3. WHY `internal/actions` IMPORTS `internal/authz` — FOR THE TYPES

`ActionSpec` (`backend/internal/actions/spec.go`) spells its verb as `authz.Action` and its noun as
`authz.Resource`, not as strings. With the typed field a spec **cannot** name an action outside the
closed vocabulary, so *"the ACL key equals the arm the router took"* holds **by construction** rather
than by discipline.

The alternative — plain strings, keeping `actions` a dependency-free leaf — reintroduces exactly the
drift that `backend/internal/authz/vocabulary.go:24-30` is a monument to: the `export` token sat in
`KnownActions` while nothing consulted it, and the day T15 shipped Alt+E *"a dormant token became a
control that LIES"*. A registry of 230 ACL keys typed as strings is 230 more opportunities for that.

The edge is safe because it is one-way and shallow: `authz`'s own set is
`{platform/db, authn, notify}` (`boundaries_test.go:42`), and `actions` reads no database, evaluates
nothing, and **must never import `internal/server`** — `routeAction` is unexported, the router is the
oracle, and every edge points inward at the composition root.

---

## 4. THE GENERATOR IS A ROUTER WALK THAT WRITES A CHECKED-IN GOLDEN

`TestActionRegistryGolden` (`backend/internal/server/action_registry_golden_test.go`) builds the
**real** router with `srv := &Server{}; srv.routes()` — no database, the same move
`route_vocabulary_test.go:81-82` has made since it was written — walks it with `chi.Walk`, synthesises
a concrete request per route, and calls the **real** `routeAction`
(`backend/internal/server/auth_middleware.go:1254`). `UPDATE_GOLDENS=1` rewrites
`internal/actions/registry_gen.go` and `frontend/src/api/goldens/action-registry.json`; otherwise it
byte-compares, which is the shape `internal/printing/template_surface_golden_test.go:233-254` already
uses and the shape of the five goldens in `frontend/src/api/goldens/`.

**Two alternatives were rejected, and both for the same reason.**

1. **A `go:generate` AST parser over `routeAction`.** There is no `go:generate` anywhere in this repo,
   and such a parser would have to re-implement seven path predicates — `isMasterPath`, `isDraftPath`,
   `isVoucherClass`, `isPriceListPath`, `isMembersPath`, `isEmployeeChildPath`, `reportName` — i.e. a
   **second implementation of route identity**. `auth_middleware.go:408` forbids that in terms:
   *"⛔ THE ROUTER IS THE ORACLE, NOT A SECOND TABLE."*
2. **Refactoring `routeAction` into a data table.** It is a control that has been driven and broken
   before (`auth_middleware.go:1135` is one such account, on a body decoded differently from the
   handler and a driven bypass of the back-dated cutoff). Rewriting it to make a generator easier
   would risk the PDP to buy a registry.

**Measured, on the tree as it stands: 230 specs** (create 55, alter 34, delete 6, display 89, print
46), 95 writes, 135 reads, **168 distinct ACL keys**, and **3** naming a date subject. The W0 gate
asked for ">= 60".

⚠️ **THE "95 CUTOFF-JUDGED" FIGURE THIS LINE USED TO CARRY WAS DELETED WITH THE COLUMN IT COUNTED,
AND THE REASON IS THE POINT OF THE WHOLE ARTEFACT.** `ActionSpec.CutoffJudged` was documented as a
measured property and was `IsWrite` spelled twice: the generator sent a probe body carrying every date
spelling to every route, and `voucherDateFromRequest` (`auth_middleware.go:447-518`) falls through to
a generic `peekVoucherDate(body)` walk for any write that is not a pay run, a TDS challan or a
bank-reconciliation PUT — so every write route necessarily answered `true`, on 230 of 230 rows. It was
worse than redundant, it was **counterfactual**: `DELETE /members/{userID}` and
`DELETE /books-closure` send no body at all, so the cutoff never runs on them, and the column claimed
the back-dated cutoff governs deleting a company member. W2 builds an autonomy table on these fields.
What survives is `DateSubject`, because `cutoffDateSubject` (`auth_middleware.go:1038-1052`) reads only
`bankReconLeg` and `challanAllocationTarget`, both method-and-path only — so it answers the same with
an **empty** body, which is a question that has an answer. 3 of 230 name one (bank date 2, deposit
date 1). `ActionSpec.Description` went the same way and for the same reason, renamed to `Label`: every
row read `"<ACLKey> (<METHOD> <route>)"`, and no embedding built from
`"delete security (DELETE .../members/{userID})"` and `"delete security (DELETE
.../members/invites/{inviteID})"` can separate removing a member from revoking an invite — which is
the one discrimination a tool-search field exists to provide.

---

## 5. MIGRATION 0091, AND WHY THERE IS NO ACL MIGRATION

`backend/migrations/0091_llm_token_budget.sql` creates `llm_token_budget`: one row per
(company, calendar month, feature), RLS `ENABLE`+`FORCE` with `USING` and `WITH CHECK` on
`app_current_company_id()`, a `(company_id, id)` company-scope UNIQUE, and nine named CHECKs. Token
counts are `BIGINT`.

**There is deliberately no money column.** A rupee figure needs a price that depends on provider,
model id, cache state and date, and one computed from a stale price list reads as authoritative
forever. (It would also be a semgrep failure waiting to happen: `money-must-not-be-float` is an ERROR
on `(amount|debit|credit|taxable|cgst|sgst|igst|total)\s+float(32|64)` anywhere under `backend/**`, so
a field named `total_cost float64` fails the build.)

**And no ACL migration**, which is the part worth writing down because "no ACL migration" is exactly
the kind of omission that is usually a defect. The header argues it (`0091_llm_token_budget.sql:27-43`):
no route reaches the table in W0, and an unmapped `{companyID}` sub-path **default-denies** at
`auth_middleware.go:2372`. The condition on the future is stated rather than left implicit: when a
route does arrive it must carry a **bare** token like `llm-budget`, never a `report:NAME` — migration
0011 seeds Data Entry a coarse `allow display report`, so a report token is granted to every operator
in every tenant the day it exists. A `report:` token would make an ACL migration mandatory.

⚠️ `0091` restates no seeder (`grep -c "CREATE OR REPLACE FUNCTION seed_company_auth_defaults"` = 0),
which is what keeps it clear of the 0071 defect: `migrations.go:71` applies in `sort.Strings` order
and `CREATE OR REPLACE` is last-writer-wins, so a later file that restates the seeder silently
un-does every rule a lower-numbered file added.

---

## 6. TWO FIGURES IN THE BRIEF THAT THE CODE CORRECTED

Recorded because both were stated as measurements and neither was.

- **"roughly 169 distinct ACL keys"** — the generator prints **168**. The brief said the number was a
  hand-derivation and that the generator must produce the exact one; it did.
- **A voucher's resource token is `sales`, not `voucher:sales`.** The first draft of
  `TestTrajectoryScopeMiddlewarePopulatesTheScope` asserted the latter and was wrong; the registry
  golden settled it. That is the same class of mistake as deriving an ACL key from a screen name, and
  it is the reason §7's join must not be papered over.

---

## 7. ⛔ THE COVERAGE JOIN IS GREEN ON THE WEAKER OF ITS TWO HALVES, AND SAYS SO IN ITS OWN PROVENANCE

**Read `Source` and `Channel` before reading a passing `./internal/actions` as more than it is.**

The join was specified as: each UI command names the HTTP `(verb, path)` it calls; the generator
resolves that pair through the **router** to a spec id; every action then either has a human path or an
entry in `actionsWithNoHumanPathYet` **with a written reason**. The generator implements all of it,
including `${x}`/`{x}`/`:x` placeholder forms and query strings, and `TestUICommandJoinResolvesRoutes`
proves it end to end against a fixture manifest.

**The keystroke-level half of that key does not exist in the artefact, and the registry records the
absence rather than smoothing it.** `frontend/src/api/goldens/ui-commands.json` ships 281 commands
shaped `{id, surface, scope, combo, action, label, screen_action, dispatch}`. Not one carries an `acl`
claim, so `Source.UICommandsJoined == 0`.

What makes the gate green instead is a **second, weaker half: a client-call census.** The frontend
records which HTTP calls the shipped clients actually make, driven against a recording transport, and
**162** of them resolve to a route (10 more are methods nothing in the app references, and are counted
separately as `ClientCallsUnused`). So `Channel` reads **`client`** on 203 of 230 rows and
**never `keystroke`**, and the evidence behind a passing test is *"a method on one of the app's HTTP
clients requests this route, and the app's own source references that method"* — a person driving the
product reaches it — **not** *"this key presses it"*.

⛔ **THAT DISTINCTION IS WHY `ChannelClient` IS A SEPARATE VALUE RATHER THAN FOLDED INTO
`ChannelKeystroke`.** The two answer different questions, and only the second one is the question W0
D3 was commissioned to answer. A single `reachable` boolean would have made this section unwritable.

**27 rows carry a written opt-out**, and `maxActionsWithNoHumanPathYet` is pinned at 27 so the
twenty-eighth is an edit to a test with a reason in the commit. That number is inside the W0 brief's
own bound — *"if the map grows past a couple of dozen entries, STOP: that means the join key is
wrong"* — and the map earned its keep immediately: four of its entries are **write routes into the
books with no operator in front of them**. `client.alterBudget`, `alterBudgetLine`, `deleteBudget` and
`deleteBudgetLine` all exist in `rest.ts` and `BudgetsScreen.tsx` calls none of them, so the screen
creates and lists budgets and nothing in the product alters or deletes one. The agent layer will read
those four rows and walk through them happily. That is the census doing the job it was built for, on
its first run.

**What closes the keystroke half** is one field per UI command, emitted by
`frontend/src/actions/uiCommandManifest.ts`:

```json
"acl": [{"verb": "POST", "path": "/v1/companies/${companyId}/vouchers/sales"}]
```

The Go generator **already reads and prefers `commands[].acl` over the client census**, so a real claim
sharpens those rows from `client` to `keystroke` with no Go change — only a regeneration.

⚠️ **THE SEAM THAT WAS SUPPOSED TO SUPPLY IT HAS BEEN DELETED, DELIBERATELY, AND THE REPLACEMENT IS
WRITTEN DOWN.** Lane E opened a `calls?: readonly (keyof CoredClient)[]` parameter on
`useRegisterScreenAction` and a `DECLARED_CLIENT_CALLS` map behind it. Nothing ever fed it (47 call
sites, none passing a third argument) and nothing read it — and it **could not have worked in that
shape**, by its own doc comment: it registered from inside a `useEffect`, so it held only what was
*mounted*, and `uiCommandManifest.ts` runs in a bare Node process where the map is empty by
construction. Populating call sites would have produced zero `acl` rows. It was removed rather than
left standing, because a typed, documented, exported API wired to nothing is how the next reader
concludes the gate is fed — `authz/vocabulary.go:24-57` on the deleted `export` token is the same
defect and the more expensive one. A tombstone comment stands where it was. The mechanism that would
actually discharge the half — a plain `const` exported from each screen module beside its handler and
imported the way `SCREEN_ACTIONS` and `GATEWAY_MENU` already are — is recorded in
`uiCommandManifest.ts`'s `ClientCall` doc for whoever takes it.

⚠️ **The key must come from the router, never from a screen name.** The manifest carries `scope` and
`screen_action`, and a join on those would produce ~200 rows plausibly right and a handful silently
wrong — `tds-challans` (the screen) maps to `tds-challan` (the resource), `print-templates` to
`company`, and §6's `vouchers/sales` to `sales`. A registry that is quietly wrong about six doors into
the books is worse than one that says it does not know.

---

## 8. THE WIRING, AND THE ONE PROPERTY IT MUST NOT LOSE

`backend/internal/server/server.go` gained `buildLLM()` (`LLM_PROVIDER` / `LLM_API_KEY` / `LLM_MODEL` /
`LLM_BASE_URL` / `LLM_TIMEOUT`), `buildBudget()`, the `trajectoryRecorder` adapter and
`trajectoryScopeMiddleware`; `backend/cmd/cored/main.go` constructs the OTLP recorder, installs it and
flushes on shutdown.

⛔ **THE SHIPPED DEFAULT IS STILL HERMETIC.** With no keys and no network, `buildTranslator` returns
`query.FakeTranslator` exactly as before W0, and `trajectory.Start` runs its zero-allocation nop path.
`TestTheShippedDefaultTranslatorIsHermetic` pins it, with the pre-W0 `ANTHROPIC_API_KEY` path as its
control — because "returns FakeTranslator" is otherwise satisfied by a function that **always** does,
which would silently disable the real translator for every deployment that has one.

Three refusals rather than degradations, each with a message that names the fix:

- an unknown `LLM_PROVIDER` **refuses to boot**, because the fallback that suggests itself —
  FakeTranslator — answers accounting questions by keyword match while the operator believes a model
  is running, and `internal/nlq/translate.go`'s comment on `llmClient` says nobody downstream could
  tell the two apart;
- a provider with no model or (except vLLM) no credential refuses, via a **dry run of `llm.New`**
  rather than a second copy of its rules;
- a malformed `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` refuses to serve, because a server that looks
  instrumented and exports nothing is discovered during the incident it was supposed to explain.

Two **logged degradations**, never silent: an unset trace endpoint (`trajectory/otlp.go:134` prints
it), and an unset `LLM_BUDGET_TOKENS_PER_MONTH` (no per-tenant cap — `internal/llm/trace.go:81-86`
makes saying so the composition root's job).

`trajectoryScopeMiddleware` mounts **inside** `r.Route("/v1/companies/{companyID}")`, immediately after
`companyAuthMiddleware`, and **adds no route of its own** — a route there without a `routeAction` arm
turns `TestEveryCompanyRouteIsMappedAndInVocabulary` red and, worse, is a door the PDP never
evaluated. It calls `routeAction` a **second** time, which is safe because that function reads the
method and the path only and never touches the body (`auth_middleware.go:212-214`), and which is
preferable to stashing the middleware's own copy: that would be a second source of truth for route
identity, which is what §4.1 forbids.
