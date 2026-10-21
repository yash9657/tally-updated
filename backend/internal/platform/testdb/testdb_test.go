package testdb

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/platform/tenant"
)

// requireDB skips when the admin database is unreachable or CORED_SKIP_DB is
// set. It SKIPS rather than fails so a laptop with no Postgres can still run
// `go build ./... && go vet ./...` and the pure packages' tests — but note that
// backend/scripts/test_residue.sh deliberately refuses to accept a run in which
// these skipped, because "0 leaked companies" is trivially true of a suite that
// never connected.
func requireDB(t *testing.T) {
	t.Helper()
	if err := Available(context.Background()); err != nil {
		t.Skipf("testdb: admin database unavailable (%v)", err)
	}
}

// newCompanyID mints a time-ordered id, matching how the rest of the backend
// creates companies.
func newCompanyID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return id
}

// ---------------------------------------------------------------------------
// 1. Classification — the anti-rot test LIVES IN internal/platform/tenant
// ---------------------------------------------------------------------------
//
// TestEveryTableIsClassifiedForTeardown used to be here. It moved with the
// registries it makes complete, to internal/platform/tenant/census_test.go
// (TestEveryTableIsClassified) — a completeness test that lives apart from the
// thing it makes complete is one refactor away from being vacuous.
//
// It is still the OTHER HALF of this file's guarantee and the Drop comment still
// names it: the residue check below is generated from the same plan the deletes
// came from, so it structurally cannot see a table that is missing from the plan
// entirely. The classification test is what sees that one.

// ---------------------------------------------------------------------------
// The fixture: one row in EVERY table the plan touches
// ---------------------------------------------------------------------------

type fixtureRow struct {
	Table string
	SQL   string
	Args  []any
}

// optionalFixtureTables are fixture tables a migration creates only when the
// cluster supports them, each with the reason.
var optionalFixtureTables = map[string]string{
	"emb_vector": "0098 creates it only where pgvector is installed (emb_enable_vectors)",
}

// buildFixture returns one INSERT per table in the delete plan.
//
// The set of tables it covers is asserted to EQUAL the plan's table set before
// it is used (see seedCompany). That equality is the point: if a migration adds
// a tenant table, the plan grows, the fixture does not, and the test fails
// telling the author to add a row — instead of quietly proving the teardown
// works on 31 of 32 tables.
func buildFixture(cid uuid.UUID) []fixtureRow {
	id := func() uuid.UUID { return uuid.New() }
	var (
		groupID = id()
		levelID = id()
		userID  = id()
		unitID  = id()
		itemID  = id()
		// T10 — the stock group the item below belongs to (migration 0079).
		stockGroupID = id()
		godownID     = id()
		batchID      = id()
		ledgerID     = id()
		vtypeID      = id()
		voucherID    = id()
		versionID    = id()
		plistID      = id()

		// Captured (rather than inlined as id()) because
		// posting_cost_allocation below has to hang off this exact posting.
		postingID = id()

		// T2 — the two cost axes and a centre on one of them (migration 0040).
		costCategoryID = id()
		costCentreID   = id()

		// T11 — a budget and one line on it (migration 0085). Captured because the
		// line has to hang off this exact budget.
		budgetID = id()

		// W3 wave 2 — the vector row hangs off this exact document.
		embDocID = id()

		// T3 — a purchase order, its line, and the link from the stock_movement
		// below (migrations 0042 and 0043).
		orderID     = id()
		orderLineID = id()
		bomID       = id()
		// The BOM's FINISHED GOOD, which cannot be itemID: migration 0043's
		// bom_component_not_self refuses an item as its own component, and this
		// fixture may hold only ONE entry per table (seedCompany fatals on a
		// second) so a plain second `stock_item` row is not available. It is
		// created inside the bom_head statement instead. See the row for why that
		// is preferable to seeding an illegal recipe.
		finishedItemID = id()

		// T6 — the challan the allocation below hangs off (migration 0069).
		challanID = id()

		// T8 — the currency the rate below is quoted for (migration 0080).
		currencyID = id()

		// W2 — the agent run and approval the agent_* children hang off.
		agentRunID      = id()
		agentApprovalID = id()
		// W6 — the MCP grant the fixture session hangs off.
		mcpGrantID = id()

		// W4 — the insight the feedback row hangs off.
		insightID = id()

		// Track 2 — GSTR-2A/2B inward statement and the IMS worklist.
		statementID = id()
		inwardDocID = id()

		// T4 slice 2a — the imported bank statement (migration 0062).
		bankImportID = id()
		// Slice 2b (migration 0088) links a statement LINE to the clearance it
		// produced, so both ends need ids the match row can name.
		bankReconID = id()
		bankLineID  = id()

		// Track 3 — payroll.
		employeeID = id()
		payRunID   = id()
		payslipID  = id()
	)
	cidText := cid.String()

	return []fixtureRow{
		// The tenant root. books_beginning_from is supplied explicitly because the
		// BEFORE INSERT trigger that would default it does not fire under
		// session_replication_role = replica.
		{"company", `INSERT INTO company (id, company_id, name, fy_start_date, books_beginning_from)
			VALUES ($1, $1, 'testdb fixture ' || $2, DATE '2024-04-01', DATE '2024-04-01')`,
			[]any{cid, cidText[:8]}},

		// NAMED 'Bank Accounts', not 'Fixture Group', and that is load-bearing
		// rather than cosmetic: this fixture seeds exactly ONE row per table, so
		// the single account_group has to double as the bank group that
		// bank_reconciliation below requires. Migration 0045's trigger refuses a
		// reconciliation whose ledger is not under a group named 'Bank Accounts'
		// or 'Bank OD A/c' (matched by ltree ancestry), so a group called
		// anything else would make that row unseedable and the teardown of
		// bank_reconciliation would never be exercised.
		{"account_group", `INSERT INTO account_group (id, company_id, name, path, nature, is_pl)
			VALUES ($1, $2, 'Bank Accounts', 'fixture'::ltree, 'ASSET', false)`,
			[]any{groupID, cid}},
		{"security_level", `INSERT INTO security_level (id, company_id, name) VALUES ($1, $2, 'Fixture Level')`,
			[]any{levelID, cid}},
		{"acl_rule", `INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
			VALUES ($1, $2, $3, 'allow', 'display', 'report:Fixture')`,
			[]any{id(), cid, levelID}},
		{"company_invite", `INSERT INTO company_invite (id, company_id, subject, security_level_id)
			VALUES ($1, $2, 'fixture-subject-' || $3, $4)`,
			[]any{id(), cid, cidText, levelID}},
		{"company_membership", `INSERT INTO company_membership (user_id, company_id, security_level_id)
			VALUES ($1, $2, $3)`,
			[]any{userID, cid, levelID}},
		{"unit", `INSERT INTO unit (id, company_id, symbol) VALUES ($1, $2, 'Nos')`,
			[]any{unitID, cid}},
		// T10 — the item hierarchy (migration 0079). Seeded ABOVE stock_item
		// because the item below hangs off it, and the plan's order is the order
		// these run in.
		{"stock_group", `INSERT INTO stock_group (id, company_id, name, path)
			VALUES ($1, $2, 'Fixture Stock Group', 'fixturestockgroup'::ltree)`,
			[]any{stockGroupID, cid}},
		// ⚠️ THE ITEM CARRIES THE GROUP, and it does so on purpose: an item with a
		// NULL stock_group_id would seed a row in both tables while leaving the FK
		// between them unexercised, so a teardown that deleted the groups BEFORE
		// the items would still pass. It is the reference that has to be present
		// for the delete ORDER to be tested at all.
		{"stock_item", `INSERT INTO stock_item (id, company_id, name, unit_id, stock_group_id)
			VALUES ($1, $2, 'Fixture Item', $3, $4)`,
			[]any{itemID, cid, unitID, stockGroupID}},
		{"godown", `INSERT INTO godown (id, company_id, name) VALUES ($1, $2, 'Fixture Godown')`,
			[]any{godownID, cid}},
		{"stock_batch", `INSERT INTO stock_batch (id, company_id, stock_item_id, name)
			VALUES ($1, $2, $3, 'Fixture Batch')`,
			[]any{batchID, cid, itemID}},
		{"stock_opening", `INSERT INTO stock_opening (id, company_id, stock_item_id, godown_id, batch_id, qty, rate)
			VALUES ($1, $2, $3, $4, $5, 5, 10)`,
			[]any{id(), cid, itemID, godownID, batchID}},
		{"ledger", `INSERT INTO ledger (id, company_id, name, group_id) VALUES ($1, $2, 'Fixture Ledger', $3)`,
			[]any{ledgerID, cid, groupID}},
		{"ledger_balance", `INSERT INTO ledger_balance (company_id, ledger_id, debit, credit) VALUES ($1, $2, 100, 0)`,
			[]any{cid, ledgerID}},
		{"ledger_opening_bill", `INSERT INTO ledger_opening_bill (id, company_id, ledger_id, ref_name, dr_cr, amount)
			VALUES ($1, $2, $3, 'OB-1', 'D', 100)`,
			[]any{id(), cid, ledgerID}},
		{"voucher_type", `INSERT INTO voucher_type (id, company_id, name, parent_class)
			VALUES ($1, $2, 'Fixture Sales', 'Sales')`,
			[]any{vtypeID, cid}},
		{"voucher_number_counter", `INSERT INTO voucher_number_counter (company_id, voucher_type_id, fin_year, next_no)
			VALUES ($1, $2, 2024, 2)`,
			[]any{cid, vtypeID}},
		{"voucher_version", `INSERT INTO voucher_version
			(id, company_id, voucher_id, version_no, op, hash, payload, created_by, voucher_type_id, party_ledger_id, voucher_date)
			VALUES ($1, $2, $3, 1, 'CREATE', '\x00'::bytea, '{}'::jsonb, 'testdb', $4, $5, DATE '2024-05-01')`,
			[]any{versionID, cid, voucherID, vtypeID, ledgerID}},
		{"voucher_head", `INSERT INTO voucher_head (company_id, voucher_id, current_version_id, version_no, op, voucher_date)
			VALUES ($1, $2, $3, 1, 'CREATE', DATE '2024-05-01')`,
			[]any{cid, voucherID, versionID}},
		// W5 — migration 0100. Hangs off THIS voucher (its company-scoped FK), and
		// the teardown must get past the table's no-mutate trigger to delete it.
		{"voucher_idempotency", `INSERT INTO voucher_idempotency
			(company_id, idempotency_key, request_sha256, voucher_id, result)
			VALUES ($1, 'fixture-key-0001', repeat('a', 64), $2, '{}'::jsonb)`,
			[]any{cid, voucherID}},
		{"posting", `INSERT INTO posting (id, company_id, voucher_version_id, ledger_id, dr_cr, amount)
			VALUES ($1, $2, $3, $4, 'D', 100)`,
			[]any{postingID, cid, versionID, ledgerID}},
		{"bill_allocation", `INSERT INTO bill_allocation
			(id, company_id, voucher_version_id, ledger_id, ref_name, ref_type, dr_cr, amount)
			VALUES ($1, $2, $3, $4, 'BILL-1', 'NEW', 'D', 100)`,
			[]any{id(), cid, versionID, ledgerID}},

		// T2 — cost centres (migration 0040). The axis, a position on it, and the
		// split of the posting above.
		{"cost_category", `INSERT INTO cost_category (id, company_id, name, created_by)
			VALUES ($1, $2, 'Department', 'testdb')`,
			[]any{costCategoryID, cid}},
		{"cost_centre", `INSERT INTO cost_centre
			(id, company_id, cost_category_id, name, created_by)
			VALUES ($1, $2, $3, 'Sales', 'testdb')`,
			[]any{costCentreID, cid, costCategoryID}},
		// The amount is 100, matching the posting EXACTLY, because
		// cost_allocation_sums_to_posting refuses anything else -- to the paisa,
		// with no tolerance. It happens not to fire here (the fixture runs under
		// session_replication_role = replica, which disables triggers, the same
		// reason company's books_beginning_from is supplied explicitly above), but
		// a fixture row that would be ILLEGAL in production is a teardown test
		// standing on data the write path could never create.
		{"posting_cost_allocation", `INSERT INTO posting_cost_allocation
			(id, company_id, posting_id, cost_category_id, cost_centre_id, amount)
			VALUES ($1, $2, $3, $4, $5, 100)`,
			[]any{id(), cid, postingID, costCategoryID, costCentreID}},
		// T11 — budgets (migration 0085). The head and one line, which is all the
		// teardown needs to exercise: budget_line cascades from budget, so both
		// tables must hold a row or the plan's entry for one of them is never
		// touched.
		//
		// ⚠️ THE LINE TARGETS A LEDGER AND IS ON NETT_TRANSACTIONS, deliberately.
		// A COST_CENTRE line would be legal too, but 0085's
		// budget_line_cost_centre_is_transactional pins the basis for those, and a
		// fixture that quietly relied on that constraint being satisfied would be
		// one more thing to get wrong here. The amount is positive, as
		// budget_line_amount_positive requires -- a fixture row that would be
		// ILLEGAL in production is a teardown test standing on data the write path
		// could never create, the argument posting_cost_allocation records above.
		{"budget", `INSERT INTO budget (id, company_id, name, from_date, to_date, created_by)
			VALUES ($1, $2, 'Fixture Plan', DATE '2024-04-01', DATE '2025-03-31', 'testdb')`,
			[]any{budgetID, cid}},
		{"budget_line", `INSERT INTO budget_line
			(id, company_id, budget_id, dimension, ledger_id, basis, dr_cr, amount)
			VALUES ($1, $2, $3, 'LEDGER', $4, 'NETT_TRANSACTIONS', 'D', 500)`,
			[]any{id(), cid, budgetID, ledgerID}},
		{"voucher_gst_line", `INSERT INTO voucher_gst_line
			(id, company_id, voucher_version_id, line_no, hsn, taxable_value, gst_rate_pct, cgst, sgst, igst)
			VALUES ($1, $2, $3, 1, '1234', 100, 18, 9, 9, 0)`,
			[]any{id(), cid, versionID}},
		{"stock_movement", `INSERT INTO stock_movement
			(id, company_id, voucher_version_id, line_no, stock_item_id, godown_id, batch_id, direction, qty, rate, amount)
			VALUES ($1, $2, $3, 1, $4, $5, $6, 'IN', 3, 10, 30)`,
			[]any{id(), cid, versionID, itemID, godownID, batchID}},
		// T3 — orders (migration 0042). A PURCHASE order, deliberately: the
		// stock_movement above moves goods 'IN', and
		// order_fulfilment_within_commitment refuses a sales order fulfilled by an
		// inward movement. The line's item is itemID and its qty (5) covers the
		// movement's 3, so every one of 0042's invariants holds -- same item, right
		// direction, within commitment, and within what the line moved. The
		// triggers do not fire here (the fixture runs under
		// session_replication_role = replica, the same reason company's
		// books_beginning_from is supplied explicitly above), but a fixture row that
		// would be ILLEGAL in production is a teardown test standing on data the
		// write path could never create -- the argument posting_cost_allocation
		// records above.
		{"order_head", `INSERT INTO order_head
			(id, company_id, direction, order_no, order_date, party_ledger_id, created_by)
			VALUES ($1, $2, 'PURCHASE', 'FIX-PO-1', DATE '2024-05-01', $3, 'testdb')`,
			[]any{orderID, cid, ledgerID}},
		{"order_line", `INSERT INTO order_line
			(id, company_id, order_id, line_no, stock_item_id, qty, rate)
			VALUES ($1, $2, $3, 1, $4, 5, 10)`,
			[]any{orderLineID, cid, orderID, itemID}},
		// qty 3, matching the stock_movement above EXACTLY: a link may be less than
		// what moved (the over-delivery case) but never more.
		{"order_fulfilment", `INSERT INTO order_fulfilment
			(id, company_id, voucher_version_id, line_no, order_line_id, qty)
			VALUES ($1, $2, $3, 1, $4, 3)`,
			[]any{id(), cid, versionID, orderLineID}},

		// T3 — the bill of materials (migration 0043). The finished good is created
		// HERE, in this statement, rather than as a second `stock_item` fixture row:
		// seedCompany fatals on a table seeded twice, and a recipe whose component
		// is its own output is refused in production by bom_component_not_self. Of
		// the two ways to satisfy both, an extra row created inline is strictly
		// better than an illegal recipe -- the teardown assertions require each
		// table to go from >0 to 0 and never pin the count at one, and the extra row
		// is removed by the plan's existing stock_item step, which is the very thing
		// this test proves.
		{"bom_head", `WITH fg AS (
				INSERT INTO stock_item (id, company_id, name, unit_id)
				VALUES ($3, $2, 'Fixture Finished Good', $4) RETURNING id
			)
			INSERT INTO bom_head (id, company_id, stock_item_id, name, output_qty, created_by)
			SELECT $1, $2, fg.id, 'Standard', 1, 'testdb' FROM fg`,
			[]any{bomID, cid, finishedItemID, unitID}},
		{"bom_component", `INSERT INTO bom_component
			(id, company_id, bom_id, line_no, stock_item_id, qty)
			VALUES ($1, $2, $3, 1, $4, 4)`,
			[]any{id(), cid, bomID, itemID}},

		{"voucher_external_reference", `INSERT INTO voucher_external_reference
			(id, company_id, voucher_id, kind, ref_value, pinned_voucher_no)
			VALUES ($1, $2, $3, 'IRN', 'fixture-irn', 'F/1')`,
			[]any{id(), cid, voucherID}},
		{"reconciliation_queue", `INSERT INTO reconciliation_queue
			(id, company_id, voucher_id, attempted_op, kind, ref_value, frozen_voucher_no)
			VALUES ($1, $2, $3, 'ALTER', 'IRN', 'fixture-irn', 'F/1')`,
			[]any{id(), cid, voucherID}},
		{"sync_applied", `INSERT INTO sync_applied (id, company_id, idempotency_key, client_voucher_id, outcome)
			VALUES ($1, $2, 'fixture-key', $3, 'APPLIED')`,
			[]any{id(), cid, voucherID}},
		{"master_audit", `INSERT INTO master_audit (id, company_id, entity_type, entity_id, op, after_doc, created_by)
			VALUES ($1, $2, 'ledger', $3, 'CREATE', '{}'::jsonb, 'testdb')`,
			[]any{id(), cid, ledgerID}},
		{"notification_outbox", `INSERT INTO notification_outbox
			(id, company_id, kind, ref_id, recipient, subject_line, body)
			VALUES ($1, $2, 'invite', $3, 'a@example.com', 'subject', 'body')`,
			[]any{id(), cid, userID}},
		{"price_list", `INSERT INTO price_list (id, company_id, name, party_group_id, effective)
			VALUES ($1, $2, 'Fixture List', $3, daterange(DATE '2024-04-01', DATE '2025-04-01', '[)'))`,
			[]any{plistID, cid, groupID}},
		{"price_list_entry", `INSERT INTO price_list_entry
			(id, company_id, price_list_id, stock_item_id, qty_range, rate, discount_pct)
			VALUES ($1, $2, $3, $4, numrange(0, NULL, '[)'), 10, 0)`,
			[]any{id(), cid, plistID, itemID}},

		// One company's print-template OVERRIDES for one document kind
		// (migration 0037). footer_lines is left NULL and header_lines is given
		// an explicit empty array so the fixture exercises both of the states
		// that column distinguishes: NULL = "not overridden", '{}' = "overridden
		// with nothing".
		// T4 — one cleared bank leg. It must come AFTER voucher_head and the bank
		// ledger: 0045's composite FKs point at both, and its trigger reads the
		// voucher's date to refuse a bank date that precedes it (the fixture
		// voucher is dated 2024-05-01, so 2024-05-03 is legal).
		{"bank_reconciliation", `INSERT INTO bank_reconciliation
			(id, company_id, voucher_id, ledger_id, leg_no, bank_date, cleared_amount, cleared_dr_cr, reconciled_by)
			VALUES ($1, $2, $3, $4, 0, DATE '2024-05-03', 100, 'D', 'testdb')`,
			[]any{bankReconID, cid, voucherID, ledgerID}},

		// ------------------------------------------------------------------
		// Migration 0062 — T4 slice 2a, the imported bank statement.
		//
		// These reuse the SAME ledgerID as bank_reconciliation above, and they
		// have to: 0062's trigger refuses an import whose ledger is not under a
		// bank group, exactly as 0045's refuses a reconciliation. The fixture's
		// account_group is named 'Bank Accounts' for that reason (see its
		// comment), and both tables now depend on it.
		//
		// The line's ledger_id is not free either — bank_statement_line_import_fk
		// is a THREE-column FK, so a line whose ledger disagrees with its
		// import's is refused by the database rather than by discipline.
		// ------------------------------------------------------------------
		{"bank_statement_import", `INSERT INTO bank_statement_import
			(id, company_id, ledger_id, file_name, file_sha256, format, column_mapping,
			 period_from, period_to, line_count, imported_by)
			VALUES ($1, $2, $3, 'fixture.csv', 'fixture-sha-' || $4, 'csv', '{}'::jsonb,
			        DATE '2024-05-01', DATE '2024-05-31', 1, 'testdb')`,
			[]any{bankImportID, cid, ledgerID, cidText}},
		{"bank_statement_line", `INSERT INTO bank_statement_line
			(id, company_id, import_id, ledger_id, line_no, value_date, description,
			 amount, dr_cr, raw, line_hash, occurrence)
			VALUES ($1, $2, $3, $4, 2, DATE '2024-05-03', 'FIXTURE CREDIT',
			        100, 'D', '{}'::jsonb, 'fixture-hash-' || $5, 0)`,
			[]any{bankLineID, cid, bankImportID, ledgerID, cidText}},

		// ------------------------------------------------------------------
		// Migration 0088 — T4 slice 2b, one RESOLVED match.
		//
		// It must come after BOTH bank_statement_line and bank_reconciliation:
		// its two three-column FKs point at each, and they carry the LEDGER as
		// well as the id, so a match whose line and clearance belong to
		// different bank accounts is refused by the database rather than by the
		// service remembering to look.
		//
		// 'matched' with a reconciliation_id is the arm that exercises 0088's
		// clearance_matches_status CHECK in the direction a real row takes; the
		// set-aside arm (status 'set-aside', reconciliation_id NULL) is covered
		// by the service tests rather than here, because one fixture row per
		// table is what this census needs.
		// ------------------------------------------------------------------
		{"bank_statement_match", `INSERT INTO bank_statement_match
			(id, company_id, line_id, ledger_id, status, reconciliation_id, tier, matched_by)
			VALUES ($1, $2, $3, $4, 'matched', $5, 'exact', 'testdb')`,
			[]any{id(), cid, bankLineID, ledgerID, bankReconID}},

		// ------------------------------------------------------------------
		// Migration 0047 — T5, one raised e-way bill.
		//
		// It must come AFTER voucher_head (e_way_bill_voucher_fk is a composite
		// FK to (company_id, voucher_id)).
		//
		// generated_from_version_id is deliberately the SAME versionID the
		// fixture's voucher_head points at, i.e. this fixture row is NOT stale.
		// It is not a foreign key — the column records what the payload was
		// built from and must survive that version being superseded — so the
		// fixture is free to make it anything, and matching the current version
		// is the honest default: a fixture that shipped a stale bill would make
		// every reader of this file wonder whether staleness was the point.
		//
		// distance_km is > 0 and valid_until/validity_days are populated because
		// the schema refuses anything else: Assess never computes a validity
		// from an unstated distance, so a row that exists is a row whose expiry
		// was actually computed. See 0047's header.
		{"e_way_bill", `INSERT INTO e_way_bill
			(id, company_id, voucher_id, generated_from_version_id, local_reference,
			 provider, payload, disclosures, assessed_required, part_b_required,
			 valid_until, validity_days, from_pincode, to_pincode,
			 from_state_code, to_state_code, vehicle_number, transport_mode,
			 distance_km, over_dimensional_cargo, generated_at, created_by)
			VALUES ($1, $2, $3, $4, 'LOCAL-EWB-FIXTURE',
			        'fake-nic-ewaybill', '{}'::jsonb,
			        '["⛔ THIS IS NOT A GOVERNMENT-ISSUED e-WAY BILL."]'::jsonb,
			        true, true,
			        DATE '2024-05-04', 1, '560001', '560100',
			        '29', '29', 'KA01AB1234', 'ROAD',
			        120, false, TIMESTAMPTZ '2024-05-03 10:00:00+00', 'testdb')`,
			[]any{id(), cid, voucherID, versionID}},

		{"print_template", `INSERT INTO print_template
			(id, company_id, doc_kind, title, header_lines, show_bank_details)
			VALUES ($1, $2, 'tax-invoice', 'TAX INVOICE', '{}'::text[], false)`,
			[]any{id(), cid}},

		// ------------------------------------------------------------------
		// Migration 0049 — whether this company must deduct at all, and the
		// s.394(2) declarations that exempt a buyer from collection.
		//
		// The status row is an INDIVIDUAL on purpose: that is the category
		// subject to the turnover test, so it is the one whose CHECK constraints
		// (turnover_pair and turnover_required_for_natural_persons) actually
		// have to be satisfied. A COMPANY row would seed clean while leaving
		// both constraints unexercised.
		// ------------------------------------------------------------------
		{"company_deductor_status", `INSERT INTO company_deductor_status
			(id, company_id, tax_year_start, entity_category,
			 prior_year_turnover_rupees, prior_year_activity, recorded_by)
			VALUES ($1, $2, 2026, 'INDIVIDUAL', 20000000, 'BUSINESS', 'testdb')`,
			[]any{id(), cid}},

		// The section MUST be 206C(1)-SCRAP: s.394(2) reaches Table Sl. Nos. 1-5
		// only, and the migration's section_eligible CHECK refuses anything else.
		// A motor-vehicle row here would be unseedable and this table's teardown
		// would never be exercised.
		{"tcs_collection_declaration", `INSERT INTO tcs_collection_declaration
			(id, company_id, buyer_ledger_id, section, tax_year_start,
			 declaration_date, received_date, for_manufacturing, not_for_trading,
			 buyer_is_resident, buyer_pan, recorded_by)
			VALUES ($1, $2, $3, '206C(1)-SCRAP', 2026,
			        DATE '2026-05-01', DATE '2026-05-03', true, true, true,
			        'AAAPZ1234C', 'testdb')`,
			[]any{id(), cid, ledgerID}},

		// Migration 0050 — one deduction fact. tds_amount_rupees is deliberately
		// NON-zero here even though a zero row is legitimate: the
		// amount_within_base CHECK and the four-digit return_code shape are only
		// exercised by a row that actually carries tax.
		{"tds_deduction", `INSERT INTO tds_deduction
			(id, company_id, voucher_id, leg_seq, deductee_ledger_id, section,
			 deductee_type, tax_year_start, deduction_date, taxable_base_rupees,
			 rate_pct, tds_amount_rupees, return_code, pan_available,
			 ruleset_version, recorded_by)
			VALUES ($1, $2, $3, 0, $4, '194C', 'OTHER', 2026,
			        DATE '2026-07-10', 50000, 2, 1000, '1024', true,
			        'tds-2026-04', 'testdb')`,
			[]any{id(), cid, voucherID, ledgerID}},

		// Migration 0067 — one collection fact, the mirror of the row above.
		//
		// declaration_outcome is 'EXEMPT' and the amount is therefore ZERO, which
		// is the combination that exercises the two CHECKs unique to this table:
		// only_eligible_sections_exempt (an exemption is legal ONLY against
		// scrap, because s.394(2) reaches Table Sl. Nos. 1-5 and motor vehicles
		// are Sl. No. 6) and exempt_collects_nothing. A plain NONE row with tax
		// on it would satisfy neither.
		{"tcs_collection", `INSERT INTO tcs_collection
			(id, company_id, voucher_id, leg_seq, buyer_ledger_id, section,
			 tax_year_start, collection_date, taxable_base_rupees, rate_pct,
			 tcs_amount_rupees, pan_available, declaration_outcome,
			 ruleset_version, recorded_by)
			VALUES ($1, $2, $3, 0, $4, '206C(1)-SCRAP', 2026,
			        DATE '2026-07-10', 50000, 2, 0, true, 'EXEMPT',
			        'tcs-2026-04', 'testdb')`,
			[]any{id(), cid, voucherID, ledgerID}},

		// Migration 0069 — one deposit, and the deduction it paid.
		//
		// The amounts are WHOLE RUPEES on purpose: tds_challan forbids paise in
		// all five amount fields ("value 1000.50 will not be allowed"), so a
		// fractional fixture would fail the CHECK rather than exercise it. The
		// allocation matches the tds_deduction row above exactly — voucher,
		// section, leg_seq and amount — because a mismatch is what the reporting
		// layer reads as BROKEN BY ALTER.
		{"tds_challan", `INSERT INTO tds_challan
			(id, company_id, bsr_code, deposit_date, challan_no, tax_rupees,
			 minor_head, tax_year_start, recorded_by)
			VALUES ($1, $2, '0004329', DATE '2026-08-07', '05122', 1000,
			        '200', 2026, 'testdb')`,
			[]any{challanID, cid}},

		// The link table. It carries NO foreign key to tds_deduction (whose rows
		// are deleted and re-derived on every voucher alter), so the teardown
		// has to reach it on its own rather than by cascade — which is exactly
		// what this fixture row proves.
		{"tds_challan_allocation", `INSERT INTO tds_challan_allocation
			(id, company_id, challan_id, voucher_id, section, leg_seq,
			 allocated_amount_rupees, recorded_by)
			VALUES ($1, $2, $3, $4, '194C', 0, 1000, 'testdb')`,
			[]any{id(), cid, challanID, voucherID}},

		// Migration 0070 — the deductor return profile. Category 'K' (Company)
		// is chosen because it is the branch that forbids BOTH the Annexure 5
		// state name and the Annexure 3 ministry, so the conditional CHECKs are
		// satisfied by their empty defaults rather than needing extra columns.
		{"company_tds_profile", `INSERT INTO company_tds_profile (
			company_id, tan, pan, deductor_name, deductor_category,
			address1, state_code, pincode, email, contact_number,
			responsible_name, responsible_designation, responsible_pan,
			responsible_address1, responsible_state_code, responsible_pincode,
			responsible_contact_number, recorded_by)
			VALUES ($1, 'MUMK12345E', 'AAACK1234M', 'Testdb Traders', 'K',
			        '14 Marine Lines', '19', '400020',
			        'accounts@testdb.example', '2226301234',
			        'Radhika Venkatesan', 'Finance Director', 'AKQPV7821L',
			        '14 Marine Lines', '19', '400020', '2226301299', 'testdb')`,
			[]any{cid}},

		// ------------------------------------------------------------------
		// Track 2 — the inward-supply side: a GSTR-2B statement, one document
		// in it, an IMS action against that document, and the GSTR-3B filing
		// record whose filed_at freezes the period.
		// ------------------------------------------------------------------
		{"gst_inward_statement", `INSERT INTO gst_inward_statement
			(id, company_id, kind, return_period, filer_gstin, source_sha256, raw, ingested_by)
			VALUES ($1, $2, 'GSTR2B', '052024', '29ABCDE1234F1Z5', repeat('a', 64), '{}'::jsonb, 'testdb')`,
			[]any{statementID, cid}},
		{"gst_inward_document", `INSERT INTO gst_inward_document
			(id, company_id, statement_id, section, supplier_gstin, doc_no, doc_date, doc_type,
			 itc_available, taxable_value, cgst, sgst, igst)
			VALUES ($1, $2, $3, 'B2B', '29ABCDE1234F1Z5', 'FIX/1', DATE '2024-05-01', 'I',
			        true, 100, 9, 9, 0)`,
			[]any{inwardDocID, cid, statementID}},
		{"gst_ims_action", `INSERT INTO gst_ims_action
			(id, company_id, document_id, action, acted_at, acted_by)
			VALUES ($1, $2, $3, 'ACCEPT', TIMESTAMPTZ '2024-06-10T00:00:00Z', 'testdb')`,
			[]any{id(), cid, inwardDocID}},
		{"gst_return_filing", `INSERT INTO gst_return_filing
			(id, company_id, form, return_period, filed_at, filed_by)
			VALUES ($1, $2, 'GSTR3B', '052024', TIMESTAMPTZ '2024-06-20T00:00:00Z', 'testdb')`,
			[]any{id(), cid}},

		// ------------------------------------------------------------------
		// Track 3 — payroll: an employee, their salary structure and statutory
		// elections, and one posted pay run with a payslip and a component.
		// ------------------------------------------------------------------
		{"employee", `INSERT INTO employee (id, company_id, code, name, doj, payable_ledger_id)
			VALUES ($1, $2, 'FIX-EMP-1', 'Fixture Employee', DATE '2024-04-01', $3)`,
			[]any{employeeID, cid, ledgerID}},
		{"employee_salary_structure", `INSERT INTO employee_salary_structure
			(id, company_id, employee_id, effective_from, component_code, component_type, amount)
			VALUES ($1, $2, $3, DATE '2024-04-01', 'BASIC', 'BASIC', 20000)`,
			[]any{id(), cid, employeeID}},
		{"employee_statutory_election", `INSERT INTO employee_statutory_election
			(id, company_id, employee_id, effective_from, pf_base, tax_regime)
			VALUES ($1, $2, $3, DATE '2024-04-01', 'CEILING', 'NEW')`,
			[]any{id(), cid, employeeID}},
		{"pay_run", `INSERT INTO pay_run
			(id, company_id, wage_month, status, voucher_id, ss_ruleset_version, it_ruleset_version,
			 created_by, posted_at)
			VALUES ($1, $2, DATE '2024-05-01', 'POSTED', $3, 'fixture-ss', 'fixture-it', 'testdb',
			        TIMESTAMPTZ '2024-06-01T00:00:00Z')`,
			[]any{payRunID, cid, voucherID}},
		{"payslip", `INSERT INTO payslip
			(id, company_id, pay_run_id, employee_id, days_paid, gross_earnings, total_deductions, net_pay)
			VALUES ($1, $2, $3, $4, 31, 20000, 2400, 17600)`,
			[]any{payslipID, cid, payRunID, employeeID}},
		{"payslip_component", `INSERT INTO payslip_component
			(id, company_id, payslip_id, line_no, component_code, component_type, tag, direction, amount)
			VALUES ($1, $2, $3, 1, 'BASIC', 'BASIC', 'NA', 'EARNING', 20000)`,
			[]any{id(), cid, payslipID}},

		// T8 — multi-currency (migration 0080).
		//
		// ⚠️ 'USD' RATHER THAN THE COMPANY'S OWN BASE CURRENCY, and that is not
		// arbitrary: currency_not_base refuses a master for the base currency,
		// because a base-currency amount is encoded by currency_id IS NULL and a
		// row here would be a second encoding of it. The trigger does not fire
		// under session_replication_role = replica (see the company row above),
		// so this fixture would not be stopped by it — which is exactly why the
		// row is written the way real code must write it rather than the way the
		// fixture could get away with.
		{"currency", `INSERT INTO currency (id, company_id, code, symbol, formal_name, decimal_places)
			VALUES ($1, $2, 'USD', '$', 'US Dollars', 2)`,
			[]any{currencyID, cid}},
		{"currency_rate", `INSERT INTO currency_rate
			(id, company_id, currency_id, effective_date, rate)
			VALUES ($1, $2, $3, DATE '2024-04-01', 83.6500000000)`,
			[]any{id(), cid, currencyID}},
		// The key IS the company (mirrors company_tds_profile). The gain/loss
		// ledger is left NULL: "not chosen" is a real state the settlement path
		// must refuse on, so the fixture seeds the state that actually ships.
		{"company_forex_settings", `INSERT INTO company_forex_settings (company_id)
			VALUES ($1)`,
			[]any{cid}},

		// W0 — the per-tenant LLM token budget (migration 0091).
		//
		// ⚠️ POSITION: LAST AMONG THE UUID-company_id ROWS, AND IT MAY GO
		// ANYWHERE. This table has no foreign key at all — not even to
		// `company` — so unlike stock_group-before-stock_item above, nothing
		// about the delete ORDER is exercised by where it sits. It is placed at
		// the end of the ordinary rows so the two SPECIAL tenancy rows below
		// (TEXT company_id, and no company_id at all) stay adjacent to each
		// other and to the comments that explain them.
		//
		// The values are the shape internal/llm's PGBudget actually writes:
		// period_start is the FIRST of a month (llm_token_budget_period_is_month_start
		// refuses anything else), the limit is positive, and
		// cached_prompt_tokens <= prompt_tokens.
		{"llm_token_budget", `INSERT INTO llm_token_budget
			(id, company_id, period_start, feature, limit_tokens, reserved_tokens,
			 prompt_tokens, cached_prompt_tokens, completion_tokens, calls, released)
			VALUES ($1, $2, DATE '2026-09-01', 'fixture.feature', 1000000, 0, 1200, 800, 300, 4, 1)`,
			[]any{id(), cid}},

		// W3 — the entity facts, edges and tenant card (migration 0096). No
		// foreign keys, so, like llm_token_budget, their position exercises
		// nothing about delete ORDER. The values satisfy every named CHECK
		// (exactly one of value_num/value_text, a known unit/source/kind, the
		// tenant row with no entity_id, a token estimate under the 1500 cap).
		// ⚠️ The teardown can delete them only because replica mode disarms
		// w3_only_supersede — tally_app has no DELETE grant on any of the
		// three, by design.
		{"entity_facts", `INSERT INTO entity_facts
			(id, company_id, entity_kind, entity_key, entity_name, fact_key, value_num, unit,
			 source, job, job_version, valid_from)
			VALUES ($1, $2, 'tenant', 'tenant', 'company', 'receivables_total', 100, 'inr',
			        'computed', 'tenant.runrate', 'fixture', DATE '2026-03-31')`,
			[]any{id(), cid}},
		{"entity_edges", `INSERT INTO entity_edges
			(id, company_id, src_kind, src_key, src_name, dst_kind, dst_key, dst_name, edge_kind,
			 weight, source, job, job_version, valid_from)
			VALUES ($1, $2, 'party', 's', 'Supplier', 'item', 'i', 'Item', 'supplies_item',
			        10, 'computed', 'party.supplier', 'fixture', DATE '2026-03-31')`,
			[]any{id(), cid}},
		{"tenant_profile", `INSERT INTO tenant_profile
			(id, company_id, version, as_of, body, body_json, token_estimate, job_version)
			VALUES ($1, $2, 1, DATE '2026-03-31', 'fixture card', '{}'::jsonb, 4, 'fixture')`,
			[]any{id(), cid}},
		// W2 — the agent plane (migrations 0092–0094). Parents before
		// children in the order the FKs point: agent_run, then its steps,
		// approvals, decisions and commits. ⛔ FIVE OF THESE EIGHT TABLES ARE
		// APPEND-ONLY BY TRIGGER (and by grant), so the teardown can only
		// remove them under session_replication_role = replica — which is the
		// point of seeding them: a plan that tried a plain DELETE would fail
		// here, loudly, rather than leak an agent's intent log per test run.
		{"agent_run", `INSERT INTO agent_run
			(id, company_id, created_by, created_by_subject, agent_id, channel, instruction, delegation, status, next_poll_at)
			VALUES ($1, $2, $3, 'fixture-subject', 'khata-agent', 'panel', 'fixture', '{}'::jsonb, 'parked', now())`,
			[]any{agentRunID, cid, id()}},
		{"agent_run_step", `INSERT INTO agent_run_step (company_id, run_id, step_name, output)
			VALUES ($1, $2, 'decide:0', '{}'::jsonb)`,
			[]any{cid, agentRunID}},
		{"agent_approval", `INSERT INTO agent_approval
			(id, company_id, run_id, step_key, action_id, acl_key, risk, args, preview, reasons,
			 provenance, on_behalf_of, requested_at, deadline_at)
			VALUES ($1, $2, $3, 'gate:1', 'post.vouchers.payment', 'create payment', 'money',
			        '{}'::jsonb, '{}'::jsonb, '[]'::jsonb, '{}'::jsonb, $4, now(), now() + interval '72 hours')`,
			[]any{agentApprovalID, cid, agentRunID, id()}},
		{"agent_decision", `INSERT INTO agent_decision (id, company_id, approval_id, decision, decided_at)
			VALUES ($1, $2, $3, 'expired', now())`,
			[]any{id(), cid, agentApprovalID}},
		{"agent_commit", `INSERT INTO agent_commit (company_id, idempotency_key, run_id, action_id, voucher_ids)
			VALUES ($1, 'fixture:commit', $2, 'post.vouchers.payment', ARRAY[]::uuid[])`,
			[]any{cid, agentRunID}},
		{"agent_intent_log", `INSERT INTO agent_intent_log
			(id, company_id, run_id, seq, kind, actor_kind, actor_id, payload, hash, created_at)
			VALUES ($1, $2, $3, 1, 'run_started', 'system', 'fixture', '{}'::jsonb,
			        decode(repeat('00', 32), 'hex'), now())`,
			[]any{id(), cid, agentRunID}},
		{"agent_intent_anchor", `INSERT INTO agent_intent_anchor
			(id, company_id, anchored_at, run_count, event_count, root)
			VALUES ($1, $2, now(), 1, 1, decode(repeat('00', 32), 'hex'))`,
			[]any{id(), cid}},
		{"agent_autonomy", `INSERT INTO agent_autonomy (id, company_id, acl_key, level, set_by)
			VALUES ($1, $2, 'create payment', 1, $3)`,
			[]any{id(), cid, id()}},
		// W6 — the MCP server (migration 0105). The grant, then a session on
		// it tied to the agent_run above. ⛔ BOTH REFUSE DELETE BY TRIGGER
		// (only revocation / closing may change them), so like the agent
		// tables they are only removable under the teardown's replica role.
		{"agent_mcp_grant", `INSERT INTO agent_mcp_grant
			(id, company_id, user_id, client_name, acl_keys, expires_at, created_at)
			VALUES ($1, $2, $3, 'fixture client', ARRAY['create payment'], now() + interval '1 day', now())`,
			[]any{mcpGrantID, cid, id()}},
		{"agent_mcp_session", `INSERT INTO agent_mcp_session
			(id, company_id, grant_id, run_id, protocol_version, manifest_hash, created_at, last_seen_at)
			VALUES ($1, $2, $3, $4, '2025-06-18', repeat('0', 64), now(), now())`,
			[]any{id(), cid, mcpGrantID, agentRunID}},

		// W3 wave 2 — the retrieval store (migration 0098). emb_vector's
		// composite FK points at emb_document, so the plan must delete the
		// vector first; emb_vector exists only on a pgvector cluster, which
		// is what optionalFixtureTables below is for.
		{"emb_document", `INSERT INTO emb_document (id, company_id, kind, source_ref, content, content_hash, untrusted)
			VALUES ($1, $2, 'narration', 'fixture-line', 'NEFT FIXTURE', repeat('a', 64), true)`,
			[]any{embDocID, cid}},
		{"emb_vector", `INSERT INTO emb_vector (id, company_id, document_id, version, model_id, embedding)
			VALUES ($1, $2, $3, 'fixture@1024/v1', 'fixture', array_fill(0.5, ARRAY[1024])::real[]::halfvec)`,
			[]any{id(), cid, embDocID}},
		{"emb_golden_query", `INSERT INTO emb_golden_query (id, company_id, question, dsl, prompt_version, proposed_by)
			VALUES ($1, $2, 'fixture question', '{}'::jsonb, 'nlq-dsl-v4', 'fixture')`,
			[]any{id(), cid}},
		// W4 — the insight engine (migration 0102). ⛔ FOUR OF THE SIX ARE
		// APPEND-ONLY BY TRIGGER (feedback, run, digest) or refuse DELETE
		// (insight itself), so, like the agent tables above, only a
		// replica-mode teardown can remove them — and insight_feedback's
		// composite FK to insight exercises the delete ORDER.
		{"insight", `INSERT INTO insight
			(id, company_id, insight_type, cadence, entity_kind, entity_key, entity_name, fingerprint,
			 as_of, impact_inr, actionability, payload_json, evidence_sql, detector_version)
			VALUES ($1, $2, 'dead_stock', 'nightly', 'item', 'x', 'Fixture', 'dead_stock:x',
			        DATE '2024-05-01', 1, 1, '{}'::jsonb, 'SELECT 1', 'fixture')`,
			[]any{insightID, cid}},
		{"insight_feedback", `INSERT INTO insight_feedback (id, company_id, insight_id, insight_type, verdict, given_by)
			VALUES ($1, $2, $3, 'dead_stock', 'useful', 'fixture')`,
			[]any{id(), cid, insightID}},
		{"insight_suppression", `INSERT INTO insight_suppression (id, company_id, insight_type, reason, created_by)
			VALUES ($1, $2, 'dead_stock', 'fixture', 'fixture')`,
			[]any{id(), cid}},
		{"insight_run", `INSERT INTO insight_run (id, company_id, run_date, kind, stats)
			VALUES ($1, $2, DATE '2024-05-01', 'nightly', '{}'::jsonb)`,
			[]any{id(), cid}},
		{"insight_job", `INSERT INTO insight_job (company_id, kind, work_date)
			VALUES ($1, 'nightly', DATE '2024-05-01')`, []any{cid}},
		{"insight_digest", `INSERT INTO insight_digest (id, company_id, digest_date, items, narrative, narrator)
			VALUES ($1, $2, DATE '2024-05-01', '[]'::jsonb, '', 'template')`,
			[]any{id(), cid}},
		{"insight_voucher_event", `INSERT INTO insight_voucher_event (id, company_id, voucher_id, version_id, op)
			VALUES ($1, $2, $3, $4, 'CREATE')`,
			[]any{id(), cid, voucherID, id()}},

		// TEXT company_id. This is the row that the "AND data_type='uuid'"
		// mutation orphans, and the reason the enumeration query must not filter
		// on type.
		{"insightd_draft", `INSERT INTO insightd_draft (company_id, draft_id, body)
			VALUES ($1, 'fixture-draft', '{}'::jsonb)`,
			[]any{cidText}},

		// No company_id at all: tenancy lives in the composite TEXT primary key.
		{"statutory_step", `INSERT INTO statutory_step (workflow_id, step_name, output)
			VALUES ('irn:' || $1 || ':' || $2, 'build', '{}'::jsonb)`,
			[]any{cidText, voucherID.String()}},
	}
}

// seedCompany inserts the whole fixture for cid in ONE admin transaction with
// session_replication_role = replica.
//
// Replica mode here does two jobs: it suppresses foreign-key enforcement (so the
// rows can be inserted in any order, and rows may point at ids that are not
// themselves fixtures) and it suppresses the four DEFERRED INITIALLY DEFERRED
// AFTER INSERT triggers — posting_balanced, stock_movement_not_negative,
// stock_movement_ties_to_gst, voucher_gst_line_ties_to_stock. Those encode real
// accounting invariants that a deliberately minimal one-row-per-table fixture
// cannot satisfy; the fixture is not trying to be a valid set of books, it is
// trying to put exactly one row in every table so the teardown has something to
// miss.
func seedCompany(t *testing.T, cid uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	pl, err := loadPlan(ctx)
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}
	rows := buildFixture(cid)

	// A table that exists only on some clusters is seeded only where the plan
	// has it — and only a table NAMED here may be skipped, with its reason,
	// so this cannot quietly become "seed whatever the plan happens to have".
	inPlan := map[string]bool{}
	for _, tbl := range pl.Tables() {
		inPlan[tbl] = true
	}
	kept := rows[:0]
	for _, r := range rows {
		if _, optional := optionalFixtureTables[r.Table]; optional && !inPlan[r.Table] {
			t.Logf("fixture: %s not on this cluster (%s)", r.Table, optionalFixtureTables[r.Table])
			continue
		}
		kept = append(kept, r)
	}
	rows = kept

	// The fixture must cover EXACTLY the plan. Anything else and the test proves
	// less than it claims.
	planned := map[string]bool{}
	for _, s := range pl.Order {
		planned[s.Table] = true
	}
	for _, s := range pl.Special {
		planned[s.Table] = true
	}
	seeded := map[string]bool{}
	for _, r := range rows {
		if seeded[r.Table] {
			t.Fatalf("fixture seeds %q twice", r.Table)
		}
		seeded[r.Table] = true
	}
	var missing, extra []string
	for tbl := range planned {
		if !seeded[tbl] {
			missing = append(missing, tbl)
		}
	}
	for tbl := range seeded {
		if !planned[tbl] {
			extra = append(extra, tbl)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Fatalf("the delete plan covers %v but the test fixture seeds no row there, so the teardown of "+
			"those tables would never be exercised; add a row to buildFixture", missing)
	}
	if len(extra) > 0 {
		t.Fatalf("the test fixture seeds %v, which the delete plan does NOT cover — those rows would leak; "+
			"classify the tables in plan.go", extra)
	}

	pool, err := adminPool(ctx)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin fixture tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET LOCAL session_replication_role = replica`); err != nil {
		t.Fatalf("set replica role: %v", err)
	}
	for _, r := range rows {
		if _, err := tx.Exec(ctx, r.SQL, r.Args...); err != nil {
			t.Fatalf("seed %s: %v", r.Table, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit fixture: %v", err)
	}
}

// countsFor returns the per-table row counts for cid using the SAME residue
// query the teardown verifies with.
func countsFor(t *testing.T, cid uuid.UUID) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	pool, err := adminPool(ctx)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	pl, err := loadPlan(ctx)
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}
	counts, err := residue(ctx, pool, pl, cid)
	if err != nil {
		t.Fatalf("residue: %v", err)
	}
	out := make(map[string]int64, len(counts))
	for _, c := range counts {
		out[c.Table] = c.N
	}
	return out
}

// ---------------------------------------------------------------------------
// 2. The teardown actually removes everything
// ---------------------------------------------------------------------------

// TestDropCompanyRemovesEveryScopedRow is the load-bearing test of this package:
// it seeds one row in every table the plan covers, proves each row is really
// there, tears the company down, and proves every table is back to zero.
//
// Both halves matter. Without the "is really there" half the test would pass on
// an empty database and prove nothing — that vacuity is precisely how the old
// per-package cleanups stayed green while orphaning 603 MB of account_group.
func TestDropCompanyRemovesEveryScopedRow(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	cid := newCompanyID(t)
	seedCompany(t, cid)
	// Belt and braces: if an assertion below fails the company must still go.
	defer func() {
		if err := Drop(ctx, cid); err != nil {
			t.Logf("post-test cleanup of %s: %v", cid, err)
		}
	}()

	before := countsFor(t, cid)
	var empty []string
	for tbl, n := range before {
		if n == 0 {
			empty = append(empty, tbl)
		}
	}
	sort.Strings(empty)
	if len(empty) > 0 {
		t.Fatalf("fixture left %v empty; the teardown of those tables would be untested", empty)
	}
	t.Logf("seeded %d tables for company %s", len(before), cid)

	if err := Drop(ctx, cid); err != nil {
		t.Fatalf("Drop: %v", err)
	}

	after := countsFor(t, cid)
	var survivors []string
	for tbl, n := range after {
		if n != 0 {
			survivors = append(survivors, fmt.Sprintf("%s=%d", tbl, n))
		}
	}
	sort.Strings(survivors)
	if len(survivors) > 0 {
		t.Fatalf("after Drop(%s), test data SURVIVED and is leaking into the shared dev database: %s",
			cid, strings.Join(survivors, " "))
	}
}

// TestDropCompanyLeavesOtherCompaniesIntact is the missing-WHERE detector. A
// teardown that deleted `FROM posting` with no predicate would satisfy the test
// above perfectly while destroying every other tenant's books, including,
// on a shared dev database, another agent's in-flight test run.
func TestDropCompanyLeavesOtherCompaniesIntact(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	victim := newCompanyID(t)
	bystander := newCompanyID(t)
	seedCompany(t, victim)
	seedCompany(t, bystander)
	defer func() {
		if err := Drop(ctx, bystander); err != nil {
			t.Errorf("cleanup bystander %s: %v", bystander, err)
		}
	}()
	defer func() {
		if err := Drop(ctx, victim); err != nil {
			t.Logf("post-test cleanup of victim %s: %v", victim, err)
		}
	}()

	expected := countsFor(t, bystander)
	if len(expected) == 0 {
		t.Fatal("bystander has no counts — the plan is empty")
	}

	if err := Drop(ctx, victim); err != nil {
		t.Fatalf("Drop victim: %v", err)
	}

	got := countsFor(t, bystander)
	var damaged []string
	for tbl, want := range expected {
		if got[tbl] != want {
			damaged = append(damaged, fmt.Sprintf("%s: %d -> %d", tbl, want, got[tbl]))
		}
	}
	sort.Strings(damaged)
	if len(damaged) > 0 {
		t.Fatalf("dropping company %s damaged UNRELATED company %s — the teardown is not tenant-scoped: %s",
			victim, bystander, strings.Join(damaged, "; "))
	}
}

// TestResidueCheckCatchesADeleteThatMissedItsRows proves the pre-COMMIT residue
// check is load-bearing rather than decorative.
//
// It exists because of a real gap found while mutation-testing this package. The
// obvious mutation — filtering the enumeration query on data_type='uuid', which
// drops insightd_draft from the plan — is caught by
// TestEveryTableIsClassifiedForTeardown and by seedCompany's fixture/plan
// equality assertion, but NOT by the residue check: the residue query is
// generated from the same plan, so a table absent from the plan is absent from
// the count as well. The residue check's actual job is the other failure mode: a
// table that IS in the plan whose DELETE removes the wrong rows, or none.
//
// So that mode is provoked directly here, by handing dropWithPlan a plan whose
// statutory_step predicate has been broken (uppercased prefix) while its residue
// query still counts correctly. Drop must refuse to commit and must name the
// table and its surviving row count.
//
// Under replica mode nothing about this is an error to Postgres: the DELETE
// reports success having removed zero rows. Without the residue check the
// teardown would return nil and the row would leak in silence — which is exactly
// how this database reached 2 GB.
func TestResidueCheckCatchesADeleteThatMissedItsRows(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	cid := newCompanyID(t)
	seedCompany(t, cid)
	defer func() {
		if err := Drop(ctx, cid); err != nil {
			t.Errorf("cleanup %s: %v", cid, err)
		}
	}()

	good, err := loadPlan(ctx)
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}
	// A copy of the real plan with ONE broken predicate. verifySQL is untouched,
	// so the check still knows to look at statutory_step.
	broken := &tenant.Plan{VerifySQL: good.VerifySQL, Order: good.Order}
	var patched bool
	for _, s := range good.Special {
		if s.Table == "statutory_step" {
			broken.Special = append(broken.Special, tenant.SpecialStmt{
				Table: s.Table,
				// 'IRN:' never matches the lower-case prefix the workflow writes.
				SQL: `DELETE FROM statutory_step WHERE workflow_id LIKE 'IRN:' || $1 || ':%'`,
			})
			patched = true
			continue
		}
		broken.Special = append(broken.Special, s)
	}
	if !patched {
		t.Fatal("statutory_step is no longer in tenant.SpecialTenancy; this test needs a new subject")
	}

	err = dropWithPlan(ctx, broken, cid)
	if err == nil {
		t.Fatal("a teardown that left rows behind returned nil: the residue check is not running, " +
			"and every leak it was meant to catch would now be silent")
	}
	msg := err.Error()
	if !strings.Contains(msg, "statutory_step=1") {
		t.Errorf("the error must name the table AND its surviving row count so the leak is actionable; got: %v", err)
	}
	if !strings.Contains(msg, cid.String()) {
		t.Errorf("the error must name the company id; got: %v", err)
	}
	if !strings.Contains(msg, "NOT fully torn down") {
		t.Errorf("the error must state the CONSEQUENCE, not just an inequality; got: %v", err)
	}

	// And the failure must be atomic: because the residue check runs before
	// COMMIT, the whole teardown rolls back rather than leaving a half-deleted
	// company that a later run would report differently.
	counts := countsFor(t, cid)
	if counts["company"] != 1 {
		t.Errorf("a failed teardown must roll back entirely; company row count is %d, want 1", counts["company"])
	}
	if counts["ledger"] != 1 {
		t.Errorf("a failed teardown must roll back entirely; ledger row count is %d, want 1", counts["ledger"])
	}
}

// ---------------------------------------------------------------------------
// 3. The registration API
// ---------------------------------------------------------------------------

// TestCleanupRunsAtTestEnd pins the contract every adopting package relies on:
// registering Cleanup inside a test means the company is gone once that test
// returns, without the test body doing anything else.
func TestCleanupRunsAtTestEnd(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	cid := newCompanyID(t)
	seedCompany(t, cid)
	defer func() {
		if err := Drop(ctx, cid); err != nil {
			t.Logf("belt-and-braces cleanup of %s: %v", cid, err)
		}
	}()

	if n := companyRowCount(t, cid); n != 1 {
		t.Fatalf("fixture: company row count = %d, want 1", n)
	}

	t.Run("inner", func(t *testing.T) {
		Cleanup(t, cid)
		if n := companyRowCount(t, cid); n != 1 {
			t.Fatalf("inside the subtest the company must still exist; got count %d. "+
				"Cleanup must defer to the END of the test, or every harness that registers it "+
				"immediately after creating a company would delete its own fixture.", n)
		}
	})

	if n := companyRowCount(t, cid); n != 0 {
		t.Fatalf("after the subtest returned the company row count is %d, want 0: "+
			"testdb.Cleanup did not run at test end, so every adopting package would leak", n)
	}
}

// companyRowCount reads the company row directly, independently of the plan's
// residue query, so this test does not depend on the same code path it checks.
func companyRowCount(t *testing.T, cid uuid.UUID) int64 {
	t.Helper()
	ctx := context.Background()
	pool, err := adminPool(ctx)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	var n int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM company WHERE id = $1`, cid).Scan(&n); err != nil {
		t.Fatalf("count company: %v", err)
	}
	return n
}

// ---------------------------------------------------------------------------
// 4. Properties of the plan itself — these need no database round trip per case
// ---------------------------------------------------------------------------

// TestDeleteOrderIsReverseTopological checks the derived order against the FK
// edges: for every edge child -> parent inside the plan, the child must be
// deleted first. Replica mode makes this non-load-bearing TODAY; it is asserted
// so that it stays true if replica mode is ever narrowed, and so a partial
// failure always stops at a comprehensible place.
func TestDeleteOrderIsReverseTopological(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	pl, err := loadPlan(ctx)
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}
	pool, err := adminPool(ctx)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer conn.Release()
	edges, err := tenant.FKEdges(ctx, conn)
	if err != nil {
		t.Fatalf("fk edges: %v", err)
	}

	pos := map[string]int{}
	for i, s := range pl.Order {
		pos[s.Table] = i
	}
	for _, e := range edges {
		ci, cok := pos[e.Child]
		pi, pok := pos[e.Parent]
		if !cok || !pok {
			continue
		}
		if ci > pi {
			t.Errorf("delete order puts parent %q (position %d) before child %q (position %d); "+
				"with foreign keys enforced that delete would fail", e.Parent, pi, e.Child, ci)
		}
	}
	if len(pl.Order) == 0 {
		t.Fatal("plan is empty")
	}
	if last := pl.Order[len(pl.Order)-1].Table; last != tenant.TenantRoot {
		t.Errorf("the tenant root %q must be deleted last, got %q", tenant.TenantRoot, last)
	}
}

// TestKeepRequiresAReason pins the opt-out contract: an opt-out without a stated
// reason is indistinguishable from a forgotten cleanup, which is the exact
// failure this package was written to end.
func TestKeepRequiresAReason(t *testing.T) {
	rec := &recordingTB{}
	Keep(rec, uuid.New(), "   ")
	if len(rec.errs) != 1 {
		t.Fatalf("Keep with a blank reason must fail the test; got %d errors", len(rec.errs))
	}
	if !strings.Contains(rec.errs[0], "reason is required") {
		t.Errorf("message should say a reason is required, got %q", rec.errs[0])
	}

	rec2 := &recordingTB{}
	id := uuid.New()
	Keep(rec2, id, "documented in internal/testhygiene declaredKeeps")
	if len(rec2.errs) != 0 {
		t.Fatalf("Keep with a reason must not fail: %v", rec2.errs)
	}
	var found bool
	for _, k := range Kept() {
		if k.ID == id {
			found = true
		}
	}
	if !found {
		t.Error("Keep must record the company id so the harness can report what it deliberately left behind")
	}
}

// TestDropRefusesTheNilCompany: a zero uuid almost always means an id was never
// assigned. Running the plan with it would be a no-op that reports success, so
// the caller would never learn their company id was empty.
func TestDropRefusesTheNilCompany(t *testing.T) {
	err := Drop(context.Background(), uuid.Nil)
	if err == nil {
		t.Fatal("Drop(uuid.Nil) must refuse")
	}
	if !strings.Contains(err.Error(), "nil company") {
		t.Errorf("message should name the problem, got %v", err)
	}
}

// TestTeardownDoesNotLeaveTriggersDisarmed is the guard against the banned
// mechanism creeping back. `ALTER TABLE ... DISABLE TRIGGER` is database-wide and
// persistent; if the teardown ever used it, every later test in every package
// would run with immutability off and nobody would notice. SET LOCAL cannot do
// that, and this test proves the observable difference.
func TestTeardownDoesNotLeaveTriggersDisarmed(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	cid := newCompanyID(t)
	seedCompany(t, cid)
	if err := Drop(ctx, cid); err != nil {
		t.Fatalf("Drop: %v", err)
	}

	pool, err := adminPool(ctx)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	var role string
	if err := pool.QueryRow(ctx, `SHOW session_replication_role`).Scan(&role); err != nil {
		t.Fatalf("show session_replication_role: %v", err)
	}
	if role != "origin" {
		t.Errorf("session_replication_role is %q on a pooled connection after a teardown, want \"origin\"; "+
			"the SET must be LOCAL to the teardown transaction", role)
	}
	var disarmed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_trigger WHERE NOT tgisinternal AND tgenabled <> 'O'`).Scan(&disarmed); err != nil {
		t.Fatalf("count disarmed triggers: %v", err)
	}
	if disarmed != 0 {
		t.Errorf("%d user triggers are left disabled in the database after a teardown; "+
			"the immutability guarantee is off for every other connection", disarmed)
	}

	// And prove the guarantee is genuinely back on: an ordinary DELETE of an
	// immutable row must still be refused.
	other := newCompanyID(t)
	seedCompany(t, other)
	defer func() {
		if err := Drop(ctx, other); err != nil {
			t.Errorf("cleanup %s: %v", other, err)
		}
	}()
	_, err = pool.Exec(ctx, `DELETE FROM posting WHERE company_id = $1`, other)
	if err == nil {
		t.Fatal("posting is supposed to be immutable, but a plain DELETE succeeded — " +
			"a previous run left the immutability trigger disabled")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "immutable") {
		t.Errorf("expected an immutability error, got %v", err)
	}
}

// recordingTB is a TB that records instead of failing, so the failure paths of
// this package can themselves be tested.
type recordingTB struct {
	errs     []string
	cleanups []func()
}

func (r *recordingTB) Helper() {}
func (r *recordingTB) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}
func (r *recordingTB) Cleanup(f func()) { r.cleanups = append(r.cleanups, f) }

// TestDropNowReportsFailureAsATestError pins decision A4: a teardown that fails
// must FAIL THE TEST, not log. The recording TB lets us observe that without
// failing this test.
func TestDropNowReportsFailureAsATestError(t *testing.T) {
	rec := &recordingTB{}
	DropNow(rec, uuid.Nil) // guaranteed to fail, and requires no database
	if len(rec.errs) != 1 {
		t.Fatalf("DropNow must report a teardown failure through Errorf; got %d errors: %v", len(rec.errs), rec.errs)
	}
	if !strings.Contains(rec.errs[0], "testdb:") {
		t.Errorf("the reported error should be the teardown error, got %q", rec.errs[0])
	}
}

// TestCleanupDefersRatherThanDropping proves Cleanup registers work instead of
// doing it, using the recording TB (no database needed).
func TestCleanupDefersRatherThanDropping(t *testing.T) {
	rec := &recordingTB{}
	Cleanup(rec, uuid.New())
	if len(rec.cleanups) != 1 {
		t.Fatalf("Cleanup must register exactly one t.Cleanup; got %d", len(rec.cleanups))
	}
	if len(rec.errs) != 0 {
		t.Fatalf("Cleanup must not touch the database at registration time; got %v", rec.errs)
	}
}

// TestASecondSpecialTenancyEntryIsVerifiedByItsOwnPredicate pins the fix for a
// silent-leak bug that only a SECOND tenant.SpecialTenancy entry could ever expose.
//
// WHAT WENT WRONG. tenant.SpecialTenancy used to hold a full DELETE per table, while
// buildVerifySQL re-spelled the residue predicate itself — and it re-spelled it
// as statutory_step's, `workflow_id LIKE 'irn:' || $2 || ':%'`, for EVERY entry.
// With exactly one entry registered, the two happened to agree and nothing could
// notice. Register a second table whose tenancy is encoded any other way and the
// registry deletes by that table's predicate while the residue check counts by
// statutory_step's, which matches nothing in it — so a WRONG predicate deletes
// nothing, the check counts zero survivors, and Drop returns nil having orphaned
// every tenant row in the new table. A teardown reporting success while leaking
// is strictly worse than no teardown, because the bound then passes forever.
//
// The predicate is now registered ONCE and both statements are generated from
// it. This test proves that by registering a second entry with a DELIBERATELY
// DIFFERENT encoding (a plain equality on a differently-named column, so nothing
// about statutory_step's LIKE-prefix shape can rescue it) and then breaking its
// predicate: the residue check must name THAT table, not statutory_step.
func TestASecondSpecialTenancyEntryIsVerifiedByItsOwnPredicate(t *testing.T) {
	requireDB(t)
	ctx := context.Background()

	pool, err := adminPool(ctx)
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}

	// A throwaway table standing in for "some future table whose tenancy is not a
	// company_id column, encoded differently from statutory_step".
	const probe = "zz_second_special_probe"
	if _, err := pool.Exec(ctx,
		`CREATE TABLE IF NOT EXISTS `+probe+` (id uuid PRIMARY KEY, owner_key text NOT NULL)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS `+probe); err != nil {
			t.Errorf("drop probe table: %v", err)
		}
	})

	cid := newCompanyID(t)
	rowID := newCompanyID(t)
	if _, err := pool.Exec(ctx,
		`INSERT INTO `+probe+` (id, owner_key) VALUES ($1, $2)`, rowID, cid.String()); err != nil {
		t.Fatalf("seed probe row: %v", err)
	}

	// The real plan's `order` is carried through so the residue query still binds
	// $1 (the UUID form) somewhere — a plan with no scoped tables leaves $1
	// unreferenced and Postgres refuses the statement with "could not determine
	// data type of parameter $1". The company id below never existed, so those
	// scoped deletes touch nothing; only the special entry is under test.
	real, err := loadPlan(ctx)
	if err != nil {
		t.Fatalf("load plan: %v", err)
	}

	// A plan carrying ONLY the second entry, with a predicate that is CORRECT.
	// Equality on owner_key — nothing like statutory_step's prefix LIKE.
	good := &tenant.Plan{Order: real.Order}
	good.Special = append(good.Special, tenant.SpecialStmt{
		Table:     probe,
		SQL:       tenant.DeleteFor(probe, `owner_key = `+tenant.IDParam),
		Predicate: `owner_key = ` + tenant.IDParam,
	})
	good.VerifySQL = tenant.BuildVerifySQL(good)

	// The residue predicate must be THIS table's, not statutory_step's. If
	// buildVerifySQL ever re-spells it again, this assertion is what says so
	// before anything silently leaks.
	if strings.Contains(good.VerifySQL, "workflow_id") {
		t.Fatalf("the residue query verifies %s with statutory_step's predicate; "+
			"a second special entry would be deleted by its own predicate and verified by "+
			"someone else's, so a wrong predicate would leak silently. got: %s", probe, good.VerifySQL)
	}
	if !strings.Contains(good.VerifySQL, "owner_key") {
		t.Fatalf("the residue query does not mention %s's own column; got: %s", probe, good.VerifySQL)
	}

	// The correct predicate removes the row and the check agrees.
	if err := dropWithPlan(ctx, good, cid); err != nil {
		t.Fatalf("teardown with a correct second-entry predicate failed: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM `+probe+` WHERE owner_key = $1`, cid.String()).Scan(&n); err != nil {
		t.Fatalf("count after good teardown: %v", err)
	}
	if n != 0 {
		t.Fatalf("the correct predicate left %d rows in %s", n, probe)
	}

	// Now the mutation that used to pass silently: a BROKEN predicate on the
	// second entry. It must be caught, and the error must name THIS table.
	if _, err := pool.Exec(ctx,
		`INSERT INTO `+probe+` (id, owner_key) VALUES ($1, $2)`, newCompanyID(t), cid.String()); err != nil {
		t.Fatalf("re-seed probe row: %v", err)
	}
	broken := &tenant.Plan{Order: real.Order}
	broken.Special = append(broken.Special, tenant.SpecialStmt{
		Table: probe,
		// Still binds $1 (the batch always supplies it) but matches nothing: the
		// prefix makes every comparison fail. This is the realistic shape of the
		// bug — a predicate that is subtly wrong, not one that is malformed.
		SQL:       `DELETE FROM ` + probe + ` WHERE owner_key = 'nobody-' || $1`,
		Predicate: `owner_key = ` + tenant.IDParam,
	})
	broken.VerifySQL = tenant.BuildVerifySQL(broken)

	err = dropWithPlan(ctx, broken, cid)
	if err == nil {
		t.Fatal("a second-entry teardown that deleted NOTHING returned nil: the residue check is " +
			"verifying that table with the wrong predicate, so every row it fails to delete leaks silently")
	}
	if !strings.Contains(err.Error(), probe+"=1") {
		t.Errorf("the error must name the leaking table and its surviving row count so the leak is "+
			"actionable; got: %v", err)
	}

	// Leave nothing behind for the next test.
	if _, err := pool.Exec(ctx, `DELETE FROM `+probe+` WHERE owner_key = $1`, cid.String()); err != nil {
		t.Errorf("cleanup probe rows: %v", err)
	}
}
