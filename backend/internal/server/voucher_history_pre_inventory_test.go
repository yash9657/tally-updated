package server_test

// voucher_history_pre_inventory_test.go — THE ONE-WAY DOOR, checked over HTTP,
// on a database that may have been wiped five minutes ago.
//
// WHAT THIS REPLACES AND WHY. NEXT-SESSION.md §3.2 check 4 used to read:
// "GET /vouchers/{id}/history on any pre-0025 voucher must return
// verification.ok = true". That check depended on the development database
// still holding the 283,066 voucher_version rows written BEFORE migration 0025
// and before CanonicalPayload grew its Inventory field. Those rows cannot be
// regenerated — a row produced by today's code is by definition not evidence
// about yesterday's — so the moment the database is reset the check becomes
// unrunnable, and a check that cannot run is a check that silently stops
// checking.
//
// This test manufactures a genuinely pre-inventory row on ANY database,
// including an empty one, by inserting the PINNED payload bytes and the PINNED
// hash captured at commit c3aca5d, before CanonicalPayload.Inventory existed.
// The endpoint then has to recompute that hash from those bytes and agree.
//
// DO NOT REGENERATE THE CONSTANTS BELOW FROM THE CURRENT CODE. Their entire
// value is that they were produced by code that no longer exists: a vector
// derived from the implementation under test cannot detect that the
// implementation changed. If CanonicalPayload's JSON encoding ever shifts — a
// reordered field, a renamed tag, a new non-omitempty member — every hash in
// every customer's database stops verifying, and the symptom is not a compile
// error, it is VerifyChain reporting the entire book as tampered with, which is
// indistinguishable from an actual attack. This test and its sibling wedge
// internal/ledger.TestExistingVoucherHashesAreUnchanged are what turn that into
// a red test instead of a support call. The sibling pins the pure function; this
// one pins the whole HTTP path — storage, JSONB round-trip, re-canonicalisation
// and the endpoint's report — which is the part a customer actually sees.
//
// WHY A RAW ADMIN INSERT IS SAFE HERE. voucher_version's immutability trigger is
// BEFORE UPDATE OR DELETE only (tgtype=27), so an INSERT needs no trigger
// suppression at all, and voucher_type_id / party_ledger_id are nullable, so no
// foreign key has to be satisfied. The row is written into a throwaway company
// created over HTTP, which newCompany has already registered for teardown.

import (
	"context"
	"encoding/hex"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// The pre-Inventory canonical bytes and chain hash, copied VERBATIM from
// backend/internal/ledger/stock_hash_test.go. They are duplicated rather than
// imported because they are `package ledger` unexported consts and this file is
// `package server_test`; the duplication is deliberate and the two copies must
// stay byte-identical. `preInventoryVoucherID` is the voucher_id INSIDE the
// payload — the stored column must carry the SAME value, because the endpoint
// looks the row up by column and then hashes the payload, and a mismatch would
// make the test pass while proving nothing about the row it fetched.
const (
	preInventoryJSON = `{"voucher_id":"018f0000-0000-7000-8000-000000000001","voucher_type_id":"018f0000-0000-7000-8000-000000000002","version_no":1,"op":"CREATE","class":"Sales","voucher_no":"INV-1","voucher_date":"2025-05-10","narration":"Sale to Acme","party":"Acme Corp","postings":[{"ledger_id":"018f0000-0000-7000-8000-000000000003","dr_cr":"D","amount":"1180.0000"},{"ledger_id":"018f0000-0000-7000-8000-000000000004","dr_cr":"C","amount":"1180.0000"}]}`
	preInventoryHash = "ec486146e659e1988b182b327f4b93cf96b0356fcf2716198ef409a46b7b73be"

	preInventoryVoucherID = "018f0000-0000-7000-8000-000000000001"
)

// seedPreInventoryVoucher writes the pinned pre-0025 version row (and its head)
// into companyID on an ADMIN connection and returns the version's id.
//
// It writes prev_hash = NULL and version_no = 1: this is the FIRST link of a
// chain, which is the only shape the pinned hash is valid for — ChainHash mixes
// prev_hash in, so the same payload at version 2 would hash to something else.
func seedPreInventoryVoucher(t *testing.T, conn *pgx.Conn, companyID string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	hash, err := hex.DecodeString(preInventoryHash)
	if err != nil {
		t.Fatalf("decode pinned hash: %v", err)
	}
	if len(hash) != 32 {
		t.Fatalf("pinned hash decoded to %d bytes, want 32 (BLAKE3-256)", len(hash))
	}

	versionID, err := uuid.NewV7()
	if err != nil {
		t.Fatalf("new version id: %v", err)
	}

	if _, err := conn.Exec(ctx,
		`INSERT INTO voucher_version
		   (id, company_id, voucher_id, version_no, op, node_id,
		    prev_hash, hash, payload, created_by,
		    voucher_type_id, party_ledger_id)
		 VALUES ($1, $2, $3, 1, 'CREATE'::voucher_op, 'pre-0025',
		         NULL, $4, $5::jsonb, 'pre-0025',
		         NULL, NULL)`,
		versionID, companyID, preInventoryVoucherID, hash, preInventoryJSON); err != nil {
		t.Fatalf("insert pinned pre-inventory version: %v", err)
	}

	if _, err := conn.Exec(ctx,
		`INSERT INTO voucher_head
		   (company_id, voucher_id, current_version_id, version_no, op, is_deleted, voucher_date)
		 VALUES ($1, $2, $3, 1, 'CREATE'::voucher_op, false, '2025-05-10')`,
		companyID, preInventoryVoucherID, versionID); err != nil {
		t.Fatalf("insert voucher head for the pinned version: %v", err)
	}
	return versionID
}

// TestPreInventoryVoucherStillVerifiesItsChain is the replacement for
// NEXT-SESSION.md §3.2 check 4, and unlike the check it replaces it runs on an
// empty database.
//
// A voucher hashed before CanonicalPayload.Inventory existed must still verify
// through the live endpoint. If it does not, the inventory work broke every
// historical record in every deployed database, and the visible symptom would be
// the audit log declaring the whole book tampered with.
func TestPreInventoryVoucherStillVerifiesItsChain(t *testing.T) {
	srv := newServer(t)
	companyID, base := newCompany(t, srv) // teardown registered inside newCompany
	conn := superuser(t)

	seedPreInventoryVoucher(t, conn, companyID)

	rec, body := do(t, srv, http.MethodGet, base+"/vouchers/"+preInventoryVoucherID+"/history", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET history of the pinned pre-inventory voucher = %d, want 200 (%s)",
			rec.Code, rec.Body.String())
	}

	v, _ := body["verification"].(map[string]any)
	if v == nil {
		t.Fatalf("history response carries no verification block (%s)", rec.Body.String())
	}
	if v["ok"] != true {
		t.Fatalf("A VOUCHER HASHED BEFORE THE INVENTORY FIELD EXISTED NO LONGER VERIFIES.\n"+
			"verification = %v\n"+
			"This means CanonicalPayload's JSON encoding has changed, and every hash written "+
			"before that change — 283,066 rows in this project's own dev database alone — now "+
			"recomputes to a different value. Every historical voucher in every deployed "+
			"database will be reported as TAMPERED WITH. See the sibling wedge "+
			"internal/ledger.TestExistingVoucherHashesAreUnchanged, which pins the pure "+
			"function; this test pins the same property through storage and HTTP.", v)
	}
	if got := v["versions_checked"]; got != float64(1) {
		t.Errorf("versions_checked = %v, want 1", got)
	}

	// The endpoint must be showing the PINNED row, not some other voucher that
	// happens to verify. Without this, a query that silently matched nothing
	// would still report ok=true on an empty chain if VerifyChain ever changed.
	versions := list(t, body, "versions")
	if len(versions) != 1 {
		t.Fatalf("history has %d versions, want exactly 1 (the pinned pre-0025 row)", len(versions))
	}
	if got := versions[0]["hash"]; got != preInventoryHash {
		t.Errorf("version 1 hash = %v, want the pinned %s", got, preInventoryHash)
	}
	if got := versions[0]["narration"]; got != "Sale to Acme" {
		t.Errorf("version 1 narration = %v, want %q — the row read back is not the pinned payload",
			got, "Sale to Acme")
	}
	if got := versions[0]["prev_hash"]; got != "" {
		t.Errorf("version 1 prev_hash = %v, want empty (it begins the chain, which is the only "+
			"shape the pinned hash is valid for)", got)
	}
}

// TestTamperedPreInventoryVoucherFailsVerification is the negative half, and it
// is not optional.
//
// Without it, TestPreInventoryVoucherStillVerifiesItsChain cannot distinguish
// "the chain was verified" from "the endpoint reports ok=true for anything it
// can read" — an implementation that never checked would pass the positive test
// perfectly. So this seeds the same pinned row, flips ONE byte of the stored
// payload, and requires the endpoint to notice and to blame version 1.
//
// It runs in its own throwaway company so it cannot disturb the positive case,
// and it repairs nothing: the company is dropped at the end of the test.
func TestTamperedPreInventoryVoucherFailsVerification(t *testing.T) {
	srv := newServer(t)
	companyID, base := newCompany(t, srv)
	conn := superuser(t)

	seedPreInventoryVoucher(t, conn, companyID)

	// Sanity: intact first, so a failure below is attributable to the tamper and
	// not to a seed that was never valid.
	rec, body := do(t, srv, http.MethodGet, base+"/vouchers/"+preInventoryVoucherID+"/history", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET history = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if v, _ := body["verification"].(map[string]any); v["ok"] != true {
		t.Fatalf("the seeded pinned row does not verify before any tampering: %v", v)
	}

	// ONE character of the narration — the smallest edit that changes the
	// canonical bytes without changing their shape. "Sale to Acme" -> "Sale to
	// Acmf". Nothing about the row's structure, length or type changes; only the
	// content the hash covers.
	mutateAppendOnlyRows(t, conn, 1,
		`UPDATE voucher_version
		    SET payload = jsonb_set(payload, '{narration}', '"Sale to Acmf"')
		  WHERE company_id = $1 AND voucher_id = $2 AND version_no = 1`,
		companyID, preInventoryVoucherID)

	rec, body = do(t, srv, http.MethodGet, base+"/vouchers/"+preInventoryVoucherID+"/history", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET history after tamper = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	v, _ := body["verification"].(map[string]any)
	if v["ok"] != false {
		t.Fatalf("A ONE-CHARACTER EDIT TO A STORED PAYLOAD VERIFIED AS INTACT: %v\n"+
			"The endpoint is not actually recomputing the chain, so its ok=true on an "+
			"untouched voucher means nothing.", v)
	}
	if got := v["broken_at_version"]; got != float64(1) {
		t.Errorf("broken_at_version = %v, want 1", got)
	}
	t.Logf("one-character tamper detected: %v", v["reason"])
}
