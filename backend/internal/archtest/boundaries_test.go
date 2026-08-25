// Package archtest enforces the modular-monolith import boundaries in CI. The
// big-ball-of-mud risk (PLAN.md A.6) is structural and un-retrofittable, so we
// assert the allowed cross-internal import graph mechanically. A new forbidden
// edge fails the build.
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const modulePath = "github.com/khata/cored"

// allowedDeps maps each business module to the set of sibling internal packages
// it MAY import. The platform/db package is universally importable (it is the
// shared infrastructure seam). compliance is a pure leaf and imports no
// siblings. ledger/reporting/identity may use platform/db only. server is the
// composition root and may import any module. Anything not listed is forbidden.
var allowedDeps = map[string]map[string]bool{
	"internal/compliance": {},
	// authn (Task D) verifies/mints JWTs with the Go stdlib ONLY — no sibling
	// imports (it is a pure crypto/identity leaf). The lockout half (WS2) lives in
	// the same package and may import the db layer for its pg store.
	"internal/authn": {"internal/platform/db": true},
	// authz (Task D) is the PDP + catalog store; it reads the auth catalog over
	// the db layer. Since WS-A1 (first-party password login) it also imports authn
	// for the authn.Credential value type its user_credential store persists.
	//
	// The edge is ONE-WAY BY CONSTRUCTION and this map is what keeps it that way:
	// authn's own entry above allows ONLY internal/platform/db, so authn can never
	// import authz back. That matters more than usual here — a cycle would make the
	// whole auth stack uncompilable, and the tempting mistake (reusing
	// authz.ErrNotFound inside authn) is exactly what this line forbids.
	// authz gained internal/notify with item C: an invite ENQUEUES its
	// notification inside the same transaction that creates it, so the two
	// cannot exist without each other. The edge is one-way — notify knows
	// nothing about invites.
	"internal/authz": {"internal/platform/db": true, "internal/authn": true, "internal/notify": true},
	// webauthn (Task D WS2) is the WebAuthn ceremony + credential store over the
	// db layer; stdlib crypto only, no other siblings.
	"internal/webauthn": {"internal/platform/db": true},
	// gstr is a read-only GST return generator (GSTR-1) over the tenant-scoped
	// db layer. It does not import the parent compliance package.
	"internal/compliance/gstr": {"internal/platform/db": true},
	// itc (Track 2) is the GSTR-2B / IMS reconciliation core: a pure matching and
	// effect engine plus a thin read/write service over the tenant-scoped db
	// layer, exactly the shape gstr has. One sibling edge, nothing else.
	//
	// NOTE FOR THE INTEGRATOR: this entry was added by the Track 3 slice that
	// owns this file, because internal/compliance/itc appeared mid-run and the
	// walker fails the build for any package with no declared policy. Track 2
	// owns the package; if it also wrote this line, take Track 2's wording.
	"internal/compliance/itc": {"internal/platform/db": true, "internal/compliance/gstr": true},
	// form140 (T6) emits the prescribed Form 140 TDS return file. It is the
	// STRICTEST policy in this map — {} — and that is the design, not an
	// oversight: it has no database edge, unlike gstr and itc, because it takes
	// a fully-specified Statement and returns bytes.
	//
	// The temptation will be to add internal/platform/db so it can read
	// tds_deduction itself. Resist it. A return file that queries is a return
	// file whose output depends on when it ran, and its golden test would need
	// a database to assert a byte sequence transcribed from a spec. Sourcing
	// the Statement belongs to whatever owns the challan register; emitting it
	// belongs here.
	"internal/compliance/form140": {},
	"internal/inventory":          {},
	"internal/integration":        {},
	"internal/identity":           {"internal/platform/db": true},
	// ledger derives GST inline during a Sales/Purchase post, so it may import
	// the pure compliance leaf (no I/O, safe inside the post transaction).
	"internal/ledger": {"internal/platform/db": true, "internal/compliance": true},
	// masters holds the TDS DEDUCTOR STATUS (migration 0049), which is a
	// STATUTORY master: its vocabulary is the s.402(11)(a)–(l) entity categories
	// and the business/profession distinction, both of which internal/compliance
	// already defines and reasons about.
	//
	// The edge is to the PURE COMPLIANCE LEAF only — the same one the ledger has
	// above, and for the same reason: no I/O, no siblings of its own, safe to
	// call from inside a transaction. What it buys is that the category list has
	// exactly TWO copies (this one and migration 0049's CHECK, pinned against
	// each other by TestKnownEntityCategoriesMatchTheMigration) instead of three.
	// A masters-local copy of the fifteen categories would be a third, and a
	// closed vocabulary in three places drifts.
	//
	// IT IS AN EDGE TO COMPLIANCE, NOT THE REVERSE, and that direction is the
	// constraint worth keeping: compliance must stay a leaf that knows nothing
	// about storage. If you ever find yourself wanting compliance -> masters,
	// that is the signal the logic belongs on this side of the line instead.
	//
	// THE SECOND EDGE, TO internal/inventory, IS THE SAME ARGUMENT ONE FEATURE
	// ALONG (T9). stock_item.valuation_method names the VALUER a report replays
	// the item's movements through, and inventory is where the valuers live. So
	// the vocabulary of costing methods has exactly TWO copies — the valuers
	// themselves and migration 0078's CHECK, pinned to each other by
	// TestValuationMethodsMatchTheMigration — instead of three.
	//
	// ⛔ THE ALTERNATIVE IS NOT "no edge", IT IS A THIRD COPY, and that copy is
	// the one that goes stale ahead of the others: a masters-local list of
	// methods would let a master be created naming a method no valuer implements,
	// and the report that then failed would fail for the WHOLE COMPANY, on the
	// Stock Summary and the Balance Sheet, long after the master was written.
	// masters therefore validates by asking inventory.ValuerFor whether a valuer
	// EXISTS, which is the only question worth asking.
	//
	// internal/inventory's own allowed set is EMPTY ({} above): it imports
	// nothing in this repo, has no I/O and no clock, so this edge cannot drag a
	// dependency in behind it. And the direction is the one that matters, exactly
	// as it is for compliance: inventory must stay a leaf that knows nothing
	// about storage. inventory -> masters would be the signal that the logic
	// belongs on this side of the line.
	"internal/masters": {
		"internal/platform/db": true,
		"internal/compliance":  true,
		"internal/inventory":   true,
	},
	// banking (T4) is the WRITE side of bank reconciliation. It touches the db
	// layer and NOTHING else — deliberately, and the empty-but-for-db set is the
	// enforcement of the feature's central rule: reconciling records a fact about
	// the BANK, and must never reach the ledger, a voucher or a balance. An edge
	// to internal/ledger here would be the beginning of "while we are clearing
	// this, also adjust the books", which is exactly what a reconciliation must
	// not do. The read side lives in internal/reporting, which stays read-only.
	"internal/banking": {"internal/platform/db": true},
	// bankstatement (T4 slice 2a) is the WRITE side of bank-statement IMPORT, and
	// its allowed set is the same one-edge set as banking above, for the same
	// reason stated one line further: importing records what the BANK said, and
	// must never reach the ledger, a voucher or a balance.
	//
	// THE CONSTRAINT IS SHARPER HERE THAN IT LOOKS. The tempting edge is
	// bankstatement -> banking, so that a matched row could write its own
	// bank_reconciliation row. That is slice 2b's work, and when it comes it
	// belongs on THIS side of the line: memo §2.1 requires a resolved match to
	// land in 0045's table precisely so there is ONE source of "cleared". Whoever
	// adds that edge should add it deliberately and write down why, rather than
	// discovering it compiles.
	//
	// Note also what is NOT here: no edge to internal/masters, though the import
	// reads ledger.bank_account_number. It reads it in SQL, inside its own
	// transaction, because that column is a fact it compares against a file — not
	// a master it maintains.
	"internal/bankstatement": {"internal/platform/db": true},
	// orders (T3) is the WRITE side of sales/purchase orders and the bill of
	// materials. It touches the db layer and NOTHING else, and the
	// empty-but-for-db set is the ARCHITECTURAL ENFORCEMENT OF THE FEATURE'S
	// CENTRAL RULE:
	//
	//	⛔ AN ORDER IS A COMMITTED QUANTITY, NOT A MOVEMENT.
	//
	// The stock ledger has exactly one primitive — a positive quantity plus a
	// direction — and an order is neither. Nothing has moved when a customer
	// orders 20 units: the goods are still on our shelf, still ours, and still in
	// closing stock at full value. An edge to internal/ledger here would be the
	// beginning of "while we are recording this order, also move the stock", which
	// would corrupt closing stock and therefore the BALANCE SHEET, compounding
	// with every open order and reconciling against nothing. The empty map makes
	// that mechanically impossible rather than merely discouraged — the same
	// argument internal/banking's entry above makes about never touching a
	// balance while reconciling.
	//
	// THE LINK IN THE OTHER DIRECTION ALREADY EXISTS AND IS NOT THIS EDGE.
	// internal/ledger owns order_fulfilment (fulfilment.go) because a fulfilment
	// is written INSIDE the voucher's transaction, from the same in-memory slice
	// as the hashed payload. So the ledger reaches the ORDER TABLES, and this
	// package must not reach the ledger; the dependency runs one way and the
	// order table is the shared vocabulary rather than a call.
	//
	// The read side is the order book in internal/reporting, which stays
	// read-only. Note what is NOT here: no edge to internal/inventory, though an
	// order line names a stock_item_id. It validates that in SQL, inside its own
	// transaction, because an item is a master it references and not a valuation
	// it performs.
	"internal/orders": {"internal/platform/db": true},
	// reporting gained internal/inventory with the Stock Summary. Stock VALUE is
	// never stored: the report derives it by replaying the movement ledger through
	// the pure valuer, so reporting MUST be able to call the leaf. The edge is
	// one-way by construction — inventory's own entry above is the empty map, so
	// the valuer can never reach back into a report, a database or a tenant
	// context, which is the whole reason a valuation is reproducible.
	// reporting gained internal/compliance/form140 with T6's return-file status.
	// The edge exists for ONE reason and it is worth stating: the TDS register
	// tells an operator whether a return can be filed, and the format version it
	// quotes must be the one the emitter actually implements. Restating "1.0"
	// here would be a second copy of a citation, and the two would part company
	// the first time the department issues version 1.1.
	//
	// It is one-way and must stay so: form140's own policy is {} — it is a pure
	// leaf that knows nothing about storage, and reporting is the only sibling
	// that may reach it.
	"internal/reporting": {"internal/platform/db": true, "internal/inventory": true,
		"internal/compliance/form140": true},
	// forecast (wave 2 WS5a) is a read-only projection/anomaly reporter. It reuses
	// reporting's Outstandings netting (never re-implementing it) and issues its own
	// supplementary read-only queries over the db layer. Two siblings, nothing else.
	"internal/forecast": {"internal/platform/db": true, "internal/reporting": true},
	// query (Phase 3) is the NL->typed-DSL->safe-read-only-SQL engine: it compiles
	// a closed DSL to parameterized SQL and executes it via the db layer's
	// read-only, RLS-scoped tx. It imports the db layer and nothing else (the
	// translator is a pure leaf; the real-Claude adapter is the documented swap).
	"internal/query": {"internal/platform/db": true},
	// nlq (wave 2 WS1) is the real-Claude NL->DSL adapter — the ONLY query-path
	// package that touches the network. It depends on query (the Query it emits +
	// the Catalog it advertises) and NOTHING else (not even platform/db). The edge
	// is ONE-WAY by design: query must stay a network-free pure leaf, so query
	// never imports nlq (TestQueryPackageIsNetworkFree pins that).
	"internal/nlq": {"internal/query": true},
	// notify (item C) is the transactional OUTBOX for outbound notifications.
	// It touches the db layer and nothing else — deliberately: a notifier that
	// could reach into the ledger or the authz store would be a way for a
	// delivery concern to acquire domain authority. Callers hand it a rendered
	// Message; it never looks anything up for itself.
	"internal/notify": {"internal/platform/db": true},
	// sync is the OFFLINE cloud-sequencer (Task C WS1): it commits offline node
	// outbox intents through the ledger, assigning final numbers + resolving
	// conflicts + reusing the freeze rule. It may import the ledger (the post/
	// alter/delete/freeze path) and the db layer (its own sync_applied + recon
	// reads), and NOTHING else — never server or statutory.
	"internal/sync": {"internal/ledger": true, "internal/platform/db": true},
	// statutoryd is a separate deployable plane: it generates IRN e-invoices and
	// talks to cored ONLY over HTTP (the freeze-hook contract). It must NOT import
	// any sibling internal package — the one entry below is its OWN pure leaf and
	// nothing else.
	//
	// The single allowed edge is to statutory/rules, which holds the voucher-fact
	// vocabulary (VoucherSnapshot/SnapshotLine/Fixed2) that BOTH statutory
	// documents are assembled from, plus the pure Rule 138 e-way bill core. That
	// edge cannot become a route into cored: rules' own allowed set is the EMPTY
	// map immediately below, so it can reach nothing this package could not
	// already reach — which is nothing.
	"internal/statutory": {"internal/statutory/rules": true},
	// statutory/rules is the PURE statutory leaf, and its empty allowed set is
	// load-bearing in BOTH directions.
	//
	// Outward: it may import no sibling, so a "rule" can never quietly acquire a
	// database, a ledger or a report — every figure it applies is a function of
	// its arguments and nothing else. That is what makes rule138.go's provenance
	// block a complete account of where the numbers come from.
	//
	// Inward: internal/server MAY import it (see server's set below), which is the
	// T5 decision and the reason this package exists at all. cored must be able to
	// raise an e-way bill for a browser, because cored is the only plane that has
	// the PDP, RLS, step-up and company scoping a write path needs. That import is
	// safe precisely BECAUSE of the empty map here: server depends on a leaf that
	// depends on nothing, so no cycle and no smuggled authority is possible.
	//
	// ⛔ DO NOT GRANT THIS PACKAGE AN EDGE "FOR CONVENIENCE". The moment it can
	// read a voucher for itself, Assess stops being a pure function of a stated
	// consignment and starts being a claim about the database — and a statutory
	// conclusion whose inputs are invisible is exactly what ewaybill.go's
	// Assessment type was designed to prevent.
	"internal/statutory/rules": {},
	// ewaybill (T5) is cored's orchestration half: it assembles a consignment
	// from the books, runs the pure rules core over it, and stores the result
	// (migration 0047). Exactly two edges, and the pairing is the point —
	// db to read the voucher and write the bill, rules to decide what the bill
	// says.
	//
	// ⛔ NOT internal/ledger, AND THAT IS DELIBERATE RATHER THAN UNTIDY. An
	// e-way bill is raised AGAINST a voucher and never writes one: it must not
	// be able to post, alter, freeze or delete. It reads the voucher tables
	// directly for the same reason internal/orders reaches only platform/db —
	// a package that can call post() is a package that could acquire a second
	// voucher path, and this one has no business having one.
	//
	// ⛔ AND NOT internal/statutory. Only the pure rules leaf. Granting the
	// parent would put the IRN workflow's external-API machinery inside a cored
	// request; see the T5 decision at the top of rules/ewaybill.go.
	"internal/ewaybill": {
		"internal/platform/db":     true,
		"internal/statutory/rules": true,
	},
	// einvoice is cored's half of the IRN path: it builds the e-invoice snapshot
	// (identity AND item list) from the books and PROXIES to statutoryd over
	// HTTP. Exactly the same two edges as ewaybill above, and the fact that it
	// needs no third one is the architecture holding:
	//
	// ⛔ NOT internal/statutory, EVEN THOUGH THIS IS THE IRN PATH. The workflow
	// wraps an exactly-once external IRP call in durable, resumable state and
	// stays in its own deployable; cored reaches it over a SOCKET, never by
	// import. The one thing the two planes must agree on — the request/response
	// wire shape — lives in the pure rules leaf below, so both name the SAME
	// struct without either importing the other. See rules/einvoice_wire.go.
	//
	// ⛔ AND NOT internal/ledger. An e-invoice is raised AGAINST a voucher and
	// never writes one. The IRN is pinned by statutoryd calling cored's freeze
	// hook back over HTTP, which is the ONE seam authorised to freeze a voucher —
	// giving this package the ledger would create a second, unaudited one.
	"internal/einvoice": {
		"internal/platform/db":     true,
		"internal/statutory/rules": true,
	},
	// migration is the Tally-XML importer + reconciliation harness: it parses
	// via integration, ensures masters, posts through ledger, and reconciles via
	// reporting. It is an orchestration leaf — nothing imports it except server.
	"internal/migration": {
		"internal/integration": true,
		// gststate is the statutory State-code table, a pure reference leaf with
		// no I/O. The importer needs it because a Tally export records a party's
		// State by NAME and never as a code, while cored stores the two-digit GST
		// code — so somewhere a name has to become a code, and the alternative to
		// this edge is a second copy of the table inside the importer.
		"internal/gststate":        true,
		"internal/masters":         true,
		"internal/ledger":          true,
		"internal/reporting":       true,
		"internal/compliance":      true,
		"internal/compliance/gstr": true,
		"internal/platform/db":     true,
	},
	// payroll (Track 3) is the PURE payroll calculation core, and its allowed set
	// is the EMPTY MAP for a reason that is worth spelling out, because the
	// tempting edge is to internal/compliance:
	//
	// compliance is the other pure tax leaf and it already has a ThresholdBasis,
	// a PANAvailable flag and a rounding helper. Reusing them would be inheriting
	// a DIFFERENT COMPUTATION, not sharing a definition. compliance's
	// sectionRule.PerTxnThreshold carries a disclosed approximation IN ITS NAME:
	// v1's s.194x engine tests every threshold PER TRANSACTION even where the
	// statute makes it annual. For a vendor payment that under-deducts and is a
	// recoverable exposure. For SALARY the statute itself defines the base as
	// ANNUAL (s.392, ex-192), so a per-payslip threshold test is not an
	// approximation of the rule — it is a different rule, and it deducts ZERO for
	// an employee whose monthly pay is under the exemption but whose annual pay is
	// far over it. That is a short salary in March and a Form 138 that TRACES
	// cannot reconcile. The empty map makes that inheritance mechanically
	// impossible; do not grant the edge "for reuse".
	"internal/payroll": {},
	// payrolldb is the DB-facing half: it owns all defaulting, gathers the
	// year-to-date state the pure engine takes as input, and posts the pay run.
	// It takes the NARROWEST capability rather than a pool (the seam
	// internal/compliance/gstr established) and reaches exactly four siblings.
	//
	// The edge to payrollfile is the one the comment below ASKS FOR: the emitters
	// may not import the engine, so somebody has to map persisted rows onto each
	// emitter's file-local row struct, and payrolldb is nominated for it by name.
	// The direction is what matters — payrolldb -> payrollfile, never the reverse
	// — because it keeps the file formats downstream of the books. A Q4 format
	// change rewrites a writer and a mapper; it cannot reach the engine.
	"internal/payroll/payrolldb": {
		"internal/platform/db":         true,
		"internal/ledger":              true,
		"internal/payroll":             true,
		"internal/payroll/payrollfile": true,
	},
	// payrollfile holds the statutory file emitters (PF ECR, Form 138, the ESI
	// return) and its allowed set is ALSO the empty map — including no edge to
	// internal/payroll, which is the load-bearing part. If an emitter could import
	// the engine, a file format's field list would start dictating engine
	// semantics: the ECR's "EPF-EPS Diff" column would become a Result field and
	// the Form 138 record layout would leak into the income-tax result. Each
	// emitter takes its own file-local row struct and payrolldb does the mapping,
	// which is what keeps the writers replaceable when the Q4 format is published.
	// printing (track T1) is a PURE RENDERING LEAF: a document value in,
	// deterministic bytes out. The empty map is the strongest statement available
	// here and it is the right one — doc.go, pdf.go, font.go, page.go, pdftext.go,
	// format.go, words.go, invoice.go, voucherdoc.go, report.go and rule46.go
	// import stdlib plus github.com/shopspring/decimal and NOT ONE sibling.
	//
	// THAT EMPTINESS IS WHAT MAKES A GOLDEN TEST ON THE BYTES MEAN ANYTHING. A
	// renderer that could import internal/reporting or internal/payroll could read
	// a rate, a balance or a template mid-render, and the same invoice would then
	// print differently depending on WHEN it was printed — at which point asserting
	// on the generated bytes stops being possible and the only remaining check is
	// somebody looking at a screenshot. It also forbids the tempting shortcut of
	// letting the renderer compute a total "for convenience": every amount must
	// arrive as a decimal.Decimal already agreed with the ledger, because a printed
	// total that differs from the posting by a paisa is an invoice a customer
	// disputes. The DB-facing half is internal/printing/printingdb, below.
	"internal/printing": {},
	// gststate is STATUTORY REFERENCE DATA as a pure leaf: it maps a two-digit GST
	// State code to the NAME of the State or Union territory it denotes, and its
	// allowed set is the EMPTY MAP.
	//
	// THE EMPTINESS IS THE POINT, and the tempting edge is internal/platform/db. The
	// name of a State is not a per-company fact and it is not data anybody keys — it
	// is a property of the code, fixed by the Act that named the State. A table that
	// could read the database would answer differently per tenant, per migration and
	// per seed, and "Place of Supply: Karnataka (29)" on a tax invoice would become
	// something a company could get wrong by editing a row. It also forbids the
	// reverse temptation — importing internal/compliance, which decides intra- versus
	// inter-State from the same codes — because that would put a tax determination
	// behind a printing lookup.
	//
	// Everything upstream stays upstream: the two consumers are
	// internal/printing/printingdb (below) and, in a later slice, whatever surfaces
	// the code list in a UI. gststate itself can reach nothing.
	"internal/gststate": {},
	// printingdb is that DB-facing half: it reads a voucher and assembles the
	// document value, and it reads/writes print_template (migration 0037).
	//
	// It imports EXACTLY TWO siblings — internal/printing for the value types it
	// returns, and internal/gststate for the NAME of a State (Rule 46(n) requires
	// the place of supply along with the name of the State, Rule 46(e) an
	// unregistered recipient's State by name, and this schema stores only the
	// two-digit code, so without that edge no inter-State tax invoice and no
	// high-value B2C invoice can be printed at all) — and the set must not be
	// widened in either direction:
	//
	//   * NOT internal/platform/db. It takes a *pgxpool.Pool and sets
	//     app.current_company_id transaction-locally in its own inTenantTx, which
	//     is a deliberate duplicate of db.Pool.RunInTx's preamble (printingdb's
	//     package comment says so and names the test that pins it).
	//   * NOT internal/ledger, ever. printing must stay DOWNSTREAM of the books:
	//     the moment the ledger could be told that a document was printed, this
	//     edge becomes a cycle and "printed" starts to be a fact the posting path
	//     has to know about.
	//
	// Test files are skipped by the walker, so printingdb's document_test.go
	// reaching internal/ledger, internal/masters, internal/identity and
	// internal/platform/testdb needs no entry.
	"internal/printing/printingdb": {
		"internal/printing": true,
		"internal/gststate": true,
	},
	"internal/payroll/payrollfile": {},
	"internal/platform/db":         {},
	// testdb is the shared TEST-DATA TEARDOWN harness (internal/platform/testdb).
	// Its allowed set is the EMPTY SET, and that is the strongest statement in
	// this map: it imports no sibling at all — not platform/db, not identity — it
	// talks to Postgres directly over pgx with the admin DSN, and it does not even
	// import "testing" (it takes a minimal TB interface instead).
	//
	// That emptiness is what makes it universally adoptable. Every DB-touching
	// package's tests import it, and the walker above SKIPS _test.go files, so no
	// other entry in this map needs to change to let them. Had testdb been allowed
	// even one sibling edge, that edge would have had to be added to the allowed
	// set of every adopting package forever — a test harness would end up
	// dictating the production import graph.
	//
	// It is a NON-test package, so it could in principle be linked into cored.
	// TestNoProductionCodeImportsTestdb below is what forbids that.
	"internal/platform/testdb": {"internal/platform/tenant": true},
	// tenant is the SCHEMA-DERIVED CENSUS of which tables hold one company's data
	// (internal/platform/tenant). Its allowed set is the EMPTY SET for the same
	// reason testdb's nearly is: it issues catalog queries over a caller-supplied
	// pgx ConnLike and imports no sibling at all — not platform/db, which would be
	// the tempting edge and is the wrong one. Census has to run on whatever
	// connection the caller already holds (a pooled admin conn in testdb, the
	// request's pgx.Tx in the backup service), and taking a *db.Pool would both
	// break testdb's emptiness and stop the census running inside the restore's
	// single transaction.
	//
	// It is the ONE enumeration: teardown and backup both derive from it, because
	// the day they derive from two lists is the day a new migration's table is
	// cleaned up but not backed up, and the omission surfaces as a restored company
	// missing a plane of its books.
	"internal/platform/tenant": {},
	"internal/server": {
		"internal/platform/db": true,
		"internal/authn":       true,
		"internal/authz":       true,
		"internal/webauthn":    true,
		"internal/ledger":      true,
		"internal/masters":     true,
		"internal/reporting":   true,
		// Bank reconciliation (T4) is TWO edges because it is two halves: the
		// read model lives in reporting (which stays read-only by its package
		// contract) and the WRITE lives in banking. server is the composition
		// root that owns both handlers, so it needs both. banking's own allowed
		// set is {platform/db}, so the writer can never reach the ledger.
		"internal/banking": true,
		// Statement import (T4 slice 2a) splits the same way, for the same reason:
		// the read model is in reporting and the WRITE is in bankstatement. Its
		// own allowed set is {platform/db}, so the importer can never reach the
		// ledger either.
		"internal/bankstatement": true,
		// Orders and the bill of materials (T3) split the SAME way, for the same
		// reason, and the pattern is now three-for-three: the read model is the two
		// order books in reporting (read-only by its package contract) and the WRITE
		// is in orders. server is the composition root that owns both sets of
		// handlers, so it needs both edges.
		//
		// ⛔ WHAT THIS EDGE DOES NOT LICENSE: orders' own allowed set is
		// {platform/db}, so the order writer can never reach the ledger, a voucher or
		// a balance — which is the architectural statement of "an order is a
		// committed quantity, not a movement". The fulfilment LINK goes the other way
		// (internal/ledger owns order_fulfilment, written inside the voucher's
		// transaction), so the dependency runs one way and the order table is the
		// shared vocabulary rather than a call.
		"internal/orders":          true,
		"internal/forecast":        true,
		"internal/query":           true,
		"internal/nlq":             true,
		"internal/notify":          true,
		"internal/identity":        true,
		"internal/compliance":      true,
		"internal/compliance/gstr": true,
		// The GSTR-2A/2B + IMS surface (Track 2). server holds the handlers for
		// /itc-reconciliation, /ims-inbox and the three ITC writes, so it needs
		// the service. Note what this edge does NOT license: itc's own allowed
		// set (line ~57) is {platform/db, compliance/gstr}, so the reconciliation
		// engine still cannot reach a handler, a report or the ledger.
		"internal/compliance/itc": true,
		// Payroll (Track 3). server needs payrolldb for the handlers, and the
		// pure internal/payroll for the value types those handlers name in their
		// signatures — payroll.WageMonth above all, which is how a wage month
		// crosses the HTTP boundary without the handler re-deriving it.
		//
		// Both edges point INWARD only. internal/payroll's allowed set is the
		// empty map and payrolldb's reaches four siblings, none of them server,
		// so the engine and the store can never call back out to a request.
		"internal/payroll":           true,
		"internal/payroll/payrolldb": true,
		// Printing (track T1). server is the composition root: it holds the two
		// print routes, so it needs printingdb to assemble a document and printing
		// to render one and to FORMAT every money cell at the seam (Row.Cells are
		// strings precisely so that this package, which already holds the decimal,
		// owns the formatting decision).
		//
		// Both edges point INWARD only, and the two entries above are what keeps it
		// that way: internal/printing's allowed set is the empty map and
		// printingdb's is {internal/printing}, so neither can call back out to a
		// handler, a report or the ledger.
		"internal/printing":            true,
		"internal/printing/printingdb": true,
		"internal/inventory":           true,
		"internal/integration":         true,
		"internal/migration":           true,
		"internal/sync":                true,
		// T5, the e-way bill. server holds POST/GET
		// /companies/{id}/vouchers/{vid}/e-way-bill, so it assembles a
		// rules.ConsignmentSnapshot from the ledger and calls the pure core.
		//
		// ⛔ NOTE WHICH PACKAGE IS *NOT* HERE: internal/statutory itself. server
		// may reach the pure rules leaf and NOT the IRN workflow plane, and that
		// asymmetry is the T5 architecture decision in one line. The e-way bill
		// core is pure, so cored may hold it; the IRN workflow wraps an
		// exactly-once external call in durable state, so it stays in statutoryd
		// and a browser reaching it will need cored to proxy. Granting
		// "internal/statutory" here to save that proxy would put an external
		// government API call inside a request handler — do not.
		"internal/statutory/rules": true,
		// ...and the orchestration half that uses it. server holds the two
		// routes; internal/ewaybill holds the assembly, the refusals and the
		// store. Its own allowed set is {platform/db, statutory/rules}, so it
		// can never call back out to a handler or into the ledger.
		"internal/ewaybill": true,
		// The e-invoice / IRN path's cored half. server holds POST /e-invoices
		// and GET /e-invoices/{voucherID}; internal/einvoice holds the snapshot
		// assembly, the refusals and the statutoryd client.
		//
		// ⛔ THIS ENTRY IS THE SECOND OF TWO (HANDOFF §7.1). A new internal
		// package needs its OWN allowed-import set AND a line here; only the
		// first passes when the package is tested alone, and the second fails
		// only in the full suite — which is how it gets missed.
		//
		// ⛔ NOTE, AGAIN, WHICH PACKAGE IS STILL NOT IN server's SET:
		// internal/statutory. The proxy did not buy cored an import of the IRN
		// workflow plane; it bought it an HTTP client.
		"internal/einvoice": true,
	},
	"internal/archtest": {},
}

// backendRoot walks up from the test's working directory to the module root
// (the directory containing go.mod).
func backendRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate go.mod (backend root)")
	return ""
}

func TestInternalImportBoundaries(t *testing.T) {
	root := backendRoot(t)
	internalRoot := filepath.Join(root, "internal")

	fset := token.NewFileSet()
	violations := 0

	err := filepath.Walk(internalRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		// Skip test files: tests legitimately import siblings to exercise them.
		if strings.HasSuffix(path, "_test.go") {
			return nil
		}

		// Determine which module this file belongs to (its package dir,
		// relative to backend root, using forward slashes).
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		pkg := filepath.ToSlash(rel)

		allowed, known := allowedDeps[pkg]
		if !known {
			t.Errorf("package %q is not declared in allowedDeps; add its boundary policy", pkg)
			return nil
		}

		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range f.Imports {
			ip := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(ip, modulePath+"/internal/") {
				continue // external or non-internal import: not our concern
			}
			dep := strings.TrimPrefix(ip, modulePath+"/")
			if dep == pkg {
				continue
			}
			if !allowed[dep] {
				t.Errorf("forbidden import: %q -> %q (not in allowed set)", pkg, dep)
				violations++
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal: %v", err)
	}
	if violations == 0 {
		t.Log("import boundaries clean")
	}
}

// testdbPkg is the import path of the test-data teardown harness.
const testdbPkg = modulePath + "/internal/platform/testdb"

// TestNoProductionCodeImportsTestdb keeps the teardown harness out of every
// shipped binary.
//
// internal/platform/testdb is a NON-test package (it has to be, so that _test.go
// files in every other package can import it), which means nothing about the Go
// build system stops `cored` from linking it. What it contains is a hardcoded
// SUPERUSER DSN and a routine that, in one transaction, disarms every
// immutability trigger and mass-deletes an entire tenant. That is a perfectly
// good test harness and an unacceptable thing to ship: a single accidental
// import — an autocompleted `testdb.Drop` in a handler, a "cleanup" endpoint
// written in a hurry — would put an unauthenticated tenant-wipe into the server
// binary, and the immutability guarantee that the whole audit trail rests on
// would be one function call from being bypassed in production.
//
// Code review is not a reliable defence against a single import line, so this is
// mechanical. The walk covers every non-test .go file in the module, not just
// internal/, because cmd/ is where a binary would actually pick it up.
//
// Mutation check: add `_ "github.com/khata/cored/internal/platform/testdb"` to
// any non-test file (e.g. backend/cmd/cored/main.go) and this test must go red.
func TestNoProductionCodeImportsTestdb(t *testing.T) {
	root := backendRoot(t)
	fset := token.NewFileSet()

	var scanned, violations int
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// Nothing to scan inside vendored or hidden trees.
			base := filepath.Base(path)
			if base == "vendor" || (strings.HasPrefix(base, ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		scanned++
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			rel = path
		}
		for _, imp := range f.Imports {
			if strings.Trim(imp.Path.Value, `"`) == testdbPkg {
				violations++
				t.Errorf("%s is production code and imports the test-data teardown harness %q; "+
					"testdb holds a superuser DSN and a whole-tenant delete and must never be linked "+
					"into a shipped binary — import it from _test.go files only",
					filepath.ToSlash(rel), testdbPkg)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk backend root: %v", err)
	}
	if scanned == 0 {
		t.Fatal("scanned no non-test source files — the walk is vacuous")
	}
	if violations == 0 {
		t.Logf("no production import of testdb across %d source files", scanned)
	}
}

// dbPkgs is the denylist of import paths that would let code open a database
// connection or run a query.
var dbPkgs = map[string]bool{
	"database/sql":                      true,
	"github.com/jackc/pgx/v5":           true,
	"github.com/jackc/pgx/v5/pgxpool":   true,
	"github.com/jackc/pgx/v5/pgconn":    true,
	"github.com/jackc/pgx/v5/stdlib":    true,
	"github.com/jackc/pgx/v5/pgtype":    true,
	"github.com/khata/cored/migrations": true,
}

// TestThePrintingRendererCannotReachADatabase pins the invariant the whole
// printing track was written against, which allowedDeps alone does NOT cover.
//
// The empty allowedDeps entry for internal/printing forbids every SIBLING import,
// so the renderer cannot reach internal/reporting, internal/payroll or
// internal/platform/db. It says nothing about pgx, which is an EXTERNAL module
// and is already in go.mod for everybody else — so `import "github.com/jackc/pgx/v5"`
// inside internal/printing would sail past the boundary walker.
//
// WHY THAT MATTERS ENOUGH TO BE ITS OWN TEST. Every golden test in this track
// asserts on the GENERATED BYTES of a document. That is only a meaningful oracle
// while rendering is a pure function of the document value it was handed. A
// renderer that could open a transaction could read a rate, a balance or a
// template halfway through laying out page two, and the same invoice would print
// differently depending on when it was printed — at which point the golden bytes
// start failing for reasons that have nothing to do with the renderer, somebody
// regenerates them, and the gate is gone. The DB-facing half is
// internal/printing/printingdb, which is where pgx belongs.
//
// Mutation check: add `_ "github.com/jackc/pgx/v5"` to any non-test file under
// internal/printing and this test must go red.
func TestThePrintingRendererCannotReachADatabase(t *testing.T) {
	root := backendRoot(t)
	printingRoot := filepath.Join(root, "internal", "printing")
	fset := token.NewFileSet()

	var scanned int
	err := filepath.Walk(printingRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			// printingdb is the DB half and is SUPPOSED to import pgx.
			if info.Name() == "printingdb" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		scanned++
		for _, imp := range f.Imports {
			ip := strings.Trim(imp.Path.Value, `"`)
			if dbPkgs[ip] {
				t.Errorf("internal/printing must be a pure rendering leaf, but %s imports %q; "+
					"a renderer that can open a transaction can read a balance mid-layout, and then "+
					"the same document no longer produces the same bytes — which is the only oracle "+
					"the golden tests in this track have. Put the read in internal/printing/printingdb",
					filepath.Base(path), ip)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/printing: %v", err)
	}
	if scanned == 0 {
		t.Fatal("scanned no internal/printing source files — the walk is vacuous")
	}
	t.Logf("internal/printing is database-free across %d source files", scanned)
}

// networkPkgs is the denylist of standard-library packages that would let code
// open a socket or spawn a process. internal/query is the SAFE read-only query
// core and must reach the network through NOTHING but the (separate) internal/nlq
// adapter — so none of its non-test source may import any of these.
var networkPkgs = map[string]bool{
	"net":      true,
	"net/http": true,
	"net/url":  true,
	"net/rpc":  true,
	"os/exec":  true,
}

// TestQueryPackageIsNetworkFree pins the security-critical invariant that the
// query compiler/executor is a network-free pure leaf. Until wave 2 this was only
// a prose comment; now it is enforced. Mutation check: temporarily add
// `_ "net/http"` to any non-test file under internal/query and this test must go
// red.
func TestQueryPackageIsNetworkFree(t *testing.T) {
	root := backendRoot(t)
	queryRoot := filepath.Join(root, "internal", "query")
	fset := token.NewFileSet()

	var scanned int
	err := filepath.Walk(queryRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return perr
		}
		scanned++
		for _, imp := range f.Imports {
			ip := strings.Trim(imp.Path.Value, `"`)
			if networkPkgs[ip] || strings.HasPrefix(ip, "net/") {
				t.Errorf("internal/query must be network-free, but %s imports %q", filepath.Base(path), ip)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk internal/query: %v", err)
	}
	if scanned == 0 {
		t.Fatal("scanned no internal/query source files — the walk is vacuous")
	}
	t.Logf("internal/query network-free across %d source files", scanned)
}
