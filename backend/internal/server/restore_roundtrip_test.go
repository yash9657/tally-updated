package server_test

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/backup"
	"github.com/khata/cored/internal/platform/tenant"
	"github.com/khata/cored/internal/platform/testdb"
	"github.com/khata/cored/internal/server"
)

// ---------------------------------------------------------------------------
// T14 slice 2 — RESTORE. The oracle is a ROUND TRIP THAT DROPS THE ORIGINAL.
// ---------------------------------------------------------------------------
//
// ⛔ A RESTORE TESTED AGAINST A COMPANY THAT STILL EXISTS HAS PROVED NOTHING
// ABOUT RESTORE. The company is backed up, DROPPED with the teardown harness,
// and restored — and then three separate oracles have to agree:
//
//	1. the CENSUS: every table's row count AND every row's contents, enumerated
//	   from the live schema, never from a list;
//	2. the HASH CHAIN: VerifyChain over the restored vouchers, which is the whole
//	   point of preserving primary keys and the thing no row count can see;
//	3. three REPORTS, byte for byte. Rows can all be present and a report still
//	   differ if a current_version_id head pointer was restored wrongly.
//
// ⚠️ AND A BALANCED TRIAL BALANCE PROVES NOTHING ON ITS OWN. The statements
// balance for any self-consistent SUBSET of the books, so a restore that dropped
// a whole table still foots. That is why the census comparison is row-CONTENTS
// and not row-counts, and why it is oracle 1 rather than the only one.

// censusRows dumps, for every table the census covers, the exact jsonb of every
// row belonging to this company, sorted. It is the strongest comparison
// available and it is derived from the live schema at call time.
func censusRows(t *testing.T, companyID string) map[string][]string {
	t.Helper()
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer conn.Close(ctx)

	plan, err := tenant.Census(ctx, conn)
	if err != nil {
		t.Fatalf("census: %v", err)
	}
	id := uuid.MustParse(companyID)

	out := map[string][]string{}
	read := func(table, sql string, arg any) {
		rows, err := conn.Query(ctx, sql, arg)
		if err != nil {
			t.Fatalf("dump %s: %v", table, err)
		}
		defer rows.Close()
		vals := []string{}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				t.Fatalf("scan %s: %v", table, err)
			}
			vals = append(vals, raw)
		}
		if err := rows.Err(); err != nil {
			t.Fatalf("dump %s: %v", table, err)
		}
		sort.Strings(vals)
		out[table] = vals
	}
	for _, sc := range plan.Order {
		q := `SELECT to_jsonb(x)::text FROM ` + sc.Table + ` x WHERE x.company_id = $1`
		if sc.IsText {
			read(sc.Table, q, id.String())
		} else {
			read(sc.Table, q, id)
		}
	}
	for _, sp := range plan.Special {
		read(sp.Table,
			`SELECT to_jsonb(x)::text FROM `+sp.Table+` x WHERE `+tenant.RenderPredicate(sp.Predicate, "$1"),
			id.String())
	}
	return out
}

// reportSnapshot captures the three statements as raw bytes.
func reportSnapshot(t *testing.T, srv *server.Server, base string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, name := range []string{"trial-balance", "balance-sheet", "stock-summary"} {
		req := httptest.NewRequest(http.MethodGet, base+"/"+name, strings.NewReader(""))
		req.Header.Set("Authorization", "Bearer "+ownerToken(t, srv))
		rec := httptest.NewRecorder()
		srv.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200 (%s)", name, rec.Code, rec.Body.String())
		}
		out[name] = rec.Body.String()
	}
	return out
}

// chainSnapshot captures the hash-chain verification of every voucher.
func chainSnapshot(t *testing.T, srv *server.Server, base string, voucherIDs []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, id := range voucherIDs {
		rec, body := do(t, srv, http.MethodGet, base+"/vouchers/"+id+"/history", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET history %s = %d, want 200 (%s)", id, rec.Code, rec.Body.String())
		}
		enc, err := json.Marshal(body["verification"])
		if err != nil {
			t.Fatalf("marshal verification: %v", err)
		}
		if !strings.Contains(string(enc), `"ok":true`) {
			t.Fatalf("voucher %s chain is not intact BEFORE any restore: %s", id, enc)
		}
		out[id] = string(enc)
	}
	return out
}

// seedRoundTripCompany builds a company with masters, inventory, GST and three
// vouchers — enough that the three reports are all non-trivial.
func seedRoundTripCompany(t *testing.T, srv *server.Server) (companyID, base string, voucherIDs []string) {
	t.Helper()
	f := newStockFixture(t, srv)
	base = f.base
	companyID = strings.TrimPrefix(base, "/v1/companies/")

	post := func(class string, body map[string]any) string {
		t.Helper()
		rec, out := do(t, srv, http.MethodPost, base+"/vouchers/"+class, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("post %s = %d, want 201 (%s)", class, rec.Code, rec.Body.String())
		}
		id, _ := out["voucher_id"].(string)
		if id == "" {
			t.Fatalf("post %s returned no voucher_id (%s)", class, rec.Body.String())
		}
		return id
	}

	voucherIDs = append(voucherIDs, post("purchase", map[string]any{
		"voucher_no": "RT-P-1", "voucher_date": "2025-04-02", "created_by": "roundtrip",
		"postings": []map[string]any{
			{"ledger_id": f.purchaseID, "dr_cr": "D", "amount": "1000"},
			{"ledger_id": f.creditorID, "dr_cr": "C", "amount": "1000"},
		},
		"inventory": []map[string]any{
			{"stock_item_id": f.itemID, "godown_id": f.godownID,
				"direction": "IN", "qty": "10", "rate": "100", "amount": "1000"},
		},
	}))
	voucherIDs = append(voucherIDs, post("sales", map[string]any{
		"voucher_no": "RT-S-1", "voucher_date": "2025-04-05", "created_by": "roundtrip",
		"party": "Stock Customer",
		"postings": []map[string]any{
			{"ledger_id": f.debtorID, "dr_cr": "D", "amount": "600"},
			{"ledger_id": f.salesID, "dr_cr": "C", "amount": "600"},
		},
		"inventory": []map[string]any{
			{"stock_item_id": f.itemID, "godown_id": f.godownID,
				"direction": "OUT", "qty": "4", "rate": "150", "amount": "600"},
		},
	}))
	// A second version of the same voucher, so the chain has more than one link
	// and current_version_id actually points somewhere non-obvious.
	altered := voucherIDs[len(voucherIDs)-1]
	rec, _ := do(t, srv, http.MethodPut, base+"/vouchers/sales/"+altered, map[string]any{
		"voucher_no": "RT-S-1", "voucher_date": "2025-04-05", "created_by": "roundtrip",
		"party": "Stock Customer", "narration": "restated",
		"postings": []map[string]any{
			{"ledger_id": f.debtorID, "dr_cr": "D", "amount": "750"},
			{"ledger_id": f.salesID, "dr_cr": "C", "amount": "750"},
		},
		"inventory": []map[string]any{
			{"stock_item_id": f.itemID, "godown_id": f.godownID,
				"direction": "OUT", "qty": "5", "rate": "150", "amount": "750"},
		},
	})
	if rec.Code != http.StatusOK && rec.Code != http.StatusCreated {
		t.Fatalf("alter sales = %d (%s)", rec.Code, rec.Body.String())
	}
	return companyID, base, voucherIDs
}

// postRestore uploads an archive to POST /v1/companies/restore.
func postRestore(t *testing.T, srv *server.Server, archive []byte) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/companies/restore", bytes.NewReader(archive))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t, srv))
	req.Header.Set("Content-Type", "application/gzip")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	var decoded map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &decoded)
	}
	return rec, decoded
}

// TestRestoreRoundTripAfterDroppingTheOriginal is T14's definition of done.
func TestRestoreRoundTripAfterDroppingTheOriginal(t *testing.T) {
	srv := newServer(t)
	companyID, base, voucherIDs := seedRoundTripCompany(t, srv)

	// ---- BEFORE: three independent oracles ----------------------------------
	rowsBefore := censusRows(t, companyID)
	reportsBefore := reportSnapshot(t, srv, base)
	chainBefore := chainSnapshot(t, srv, base, voucherIDs)

	rec := fetchBackup(t, srv, companyID)
	if rec.Code != http.StatusOK {
		t.Fatalf("backup = %d, want 200 (%.200s)", rec.Code, rec.Body.String())
	}
	archive := append([]byte(nil), rec.Body.Bytes()...)

	// ---- DROP THE ORIGINAL --------------------------------------------------
	if err := testdb.Drop(context.Background(), uuid.MustParse(companyID)); err != nil {
		t.Fatalf("drop the original company: %v", err)
	}
	if got := censusRows(t, companyID); len(got["company"]) != 0 {
		t.Fatalf("the company survived the drop; the round trip would prove nothing")
	}

	// ---- RESTORE ------------------------------------------------------------
	rrec, rbody := postRestore(t, srv, archive)
	if rrec.Code != http.StatusCreated {
		t.Fatalf("restore = %d, want 201 (%s)", rrec.Code, rrec.Body.String())
	}
	if rbody["company_id"] != companyID {
		t.Fatalf("restore created company %v, want %s — preserving the id is what preserves the chain",
			rbody["company_id"], companyID)
	}

	// ---- ORACLE 1: the census, row CONTENTS and not only counts --------------
	rowsAfter := censusRows(t, companyID)
	skipped := map[string]bool{}
	for _, table := range backup.NotRestoredTables() {
		skipped[table] = true
	}
	var diffs []string
	for table, before := range rowsBefore {
		after := rowsAfter[table]
		if skipped[table] {
			continue
		}
		if len(before) != len(after) {
			diffs = append(diffs, fmt.Sprintf("%s: %d rows before, %d after", table, len(before), len(after)))
			continue
		}
		for i := range before {
			if before[i] != after[i] {
				diffs = append(diffs, fmt.Sprintf("%s row %d differs:\n    before %s\n    after  %s",
					table, i, before[i], after[i]))
				break
			}
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 0 {
		t.Errorf("the restored company does not match the archived one:\n  %s", strings.Join(diffs, "\n  "))
	}
	var restored int
	for table, rows := range rowsAfter {
		if !skipped[table] {
			restored += len(rows)
		}
	}
	if restored < 20 {
		t.Fatalf("only %d rows were restored; the comparison is too thin to prove anything", restored)
	}

	// The deliberately-skipped table: NOT restored from the archive, and holding
	// exactly the one membership the restore granted the restoring user.
	if n := len(rowsAfter["company_membership"]); n != 1 {
		t.Errorf("company_membership has %d rows after restore, want exactly 1 (the restoring user "+
			"seeded as Owner); memberships must never be recreated from a file", n)
	}
	if names, ok := rbody["memberships_not_restored"].([]any); !ok || len(names) == 0 {
		t.Errorf("the restore did not REPORT the memberships it declined to recreate: %v",
			rbody["memberships_not_restored"])
	}

	// ---- ORACLE 2: the hash chain -------------------------------------------
	chainAfter := chainSnapshot(t, srv, base, voucherIDs)
	for id, before := range chainBefore {
		if chainAfter[id] != before {
			t.Errorf("voucher %s: the hash-chain verification changed across the round trip.\n"+
				"  before %s\n  after  %s\n"+
				"Preserving primary keys is what makes the restored chain verify against the ORIGINAL "+
				"one; a chain that only verifies against itself attests to nothing.",
				id, before, chainAfter[id])
		}
	}

	// ---- ORACLE 3: three reports, byte for byte -----------------------------
	reportsAfter := reportSnapshot(t, srv, base)
	for name, before := range reportsBefore {
		if reportsAfter[name] != before {
			t.Errorf("%s differs across the round trip — rows can all be present and a report still "+
				"differ if a current_version_id head pointer was restored wrongly.\n  before %s\n  after  %s",
				name, before, reportsAfter[name])
		}
	}
	t.Logf("round trip clean: %d rows across %d tables, %d chains, 3 reports byte-identical",
		restored, len(rowsAfter), len(chainBefore))
}

// TestRestoreRefusesWhenTheCompanyAlreadyExists is D2's stated consequence. The
// archive preserves every primary key — which is what keeps the audit chain
// verifiable — so it cannot be restored beside itself, and the refusal has to
// say why rather than failing on some downstream unique violation.
func TestRestoreRefusesWhenTheCompanyAlreadyExists(t *testing.T) {
	srv := newServer(t)
	companyID, _ := seedBackupCompany(t, srv)
	archive := fetchBackup(t, srv, companyID).Body.Bytes()

	rec, _ := postRestore(t, srv, archive)
	if rec.Code != http.StatusConflict {
		t.Fatalf("restore over an existing company = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{"already", "primary key"} {
		if !strings.Contains(strings.ToLower(body), want) {
			t.Errorf("the 409 does not explain itself (missing %q): %s", want, body)
		}
	}
}

// TestRestoreRefusesATruncatedArchive is the trailer earning its place, in the
// THREE shapes a short file actually arrives in.
//
// A cut-off download is still valid gzip up to the cut and still parses as JSON
// Lines, so without these a restore would succeed and produce a company missing
// however much did not arrive.
//
// ⛔ THE FIRST VERSION OF THIS TEST ASSERTED ONLY THE STATUS CODE, and the
// browser drive showed why that was not enough: a byte-cut archive is refused by
// the JSON decoder long before the trailer check, so the test was green while the
// operator got "archive line is not a JSON record: unexpected end of JSON input"
// — a true sentence they can do nothing with. Each case below now pins the
// SENTENCE as well as the refusal, and cases (b) and (c) are constructed so that
// the TRAILER is the only thing that can catch them.
func TestRestoreRefusesATruncatedArchive(t *testing.T) {
	srv := newServer(t)
	companyID, _ := seedBackupCompany(t, srv)
	archive := append([]byte(nil), fetchBackup(t, srv, companyID).Body.Bytes()...)
	if err := testdb.Drop(context.Background(), uuid.MustParse(companyID)); err != nil {
		t.Fatalf("drop: %v", err)
	}

	for _, tc := range []struct {
		name  string
		build func(t *testing.T) []byte
		why   string
	}{
		{
			// (a) The literal cut: a download that stopped. It usually lands
			// mid-record, so the decoder is what notices.
			name:  "cut mid-stream",
			build: func(*testing.T) []byte { return archive[: len(archive)*3/4 : len(archive)*3/4] },
			why:   "a download that stopped part-way",
		},
		{
			// (b) A file that is PERFECTLY WELL FORMED and simply stops early:
			// whole lines, cleanly re-gzipped, no trailer. Nothing but the trailer
			// check can tell this from a complete archive.
			name:  "whole lines, trailer missing",
			build: func(t *testing.T) []byte { return rebuildArchive(t, archive, func(n int) int { return n * 3 / 4 }) },
			why:   "an archive that ends cleanly but early",
		},
		{
			// (c) The nastiest: the trailer IS there and DISAGREES. Every line
			// parses, the file ends properly, and a restore that trusted the rows
			// it was given would create a company missing whatever was cut.
			name:  "trailer present but rows missing",
			build: func(t *testing.T) []byte { return dropRowsKeepingTrailer(t, archive, 5) },
			why:   "rows lost while the trailer still claims them",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec, _ := postRestore(t, srv, tc.build(t))
			if rec.Code == http.StatusCreated {
				t.Fatalf("%s restored successfully — most of a company is worse than a failed restore", tc.why)
			}
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s = %d, want 422 (%s)", tc.name, rec.Code, rec.Body.String())
			}
			// ⛔ THE SENTENCE, not only the status. An operator who is told the
			// archive is INCOMPLETE downloads it again; one who is told about
			// "unexpected end of JSON input" files a bug against the wrong thing.
			body := strings.ToUpper(rec.Body.String())
			if !strings.Contains(body, "INCOMPLETE") {
				t.Errorf("the refusal for %s does not tell the operator the archive is incomplete: %s",
					tc.name, rec.Body.String())
			}
			if n := len(censusRows(t, companyID)["company"]); n != 0 {
				t.Errorf("a refused restore left %d company rows behind; it must be one transaction", n)
			}
		})
	}
}

// TestRestoreRefusesAnArchiveFromANewerSchema pins the direction that fails
// SILENTLY. jsonb_populate_record ignores keys it has no column for, so an
// archive from a newer database would restore "successfully" having dropped
// every column added since.
func TestRestoreRefusesAnArchiveFromANewerSchema(t *testing.T) {
	srv := newServer(t)
	companyID, _ := seedBackupCompany(t, srv)
	archive := append([]byte(nil), fetchBackup(t, srv, companyID).Body.Bytes()...)
	if err := testdb.Drop(context.Background(), uuid.MustParse(companyID)); err != nil {
		t.Fatalf("drop: %v", err)
	}

	forged := rewriteHeader(t, archive, func(h *backup.Header) {
		h.SchemaVersion = "9999_from_the_future"
	})
	rec, _ := postRestore(t, srv, forged)
	if rec.Code == http.StatusCreated {
		t.Fatal("an archive from a NEWER schema restored; every column added since would have been " +
			"silently dropped by jsonb_populate_record")
	}
	if !strings.Contains(rec.Body.String(), "NEWER") {
		t.Errorf("the refusal does not name the reason: %s", rec.Body.String())
	}
}

// TestRestoreRefusesAFileThatIsNotAnArchive covers the ordinary operator error
// of uploading the wrong file.
func TestRestoreRefusesAFileThatIsNotAnArchive(t *testing.T) {
	srv := newServer(t)
	rec, _ := postRestore(t, srv, []byte("this is not a gzip file"))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("restore of a non-archive = %d, want 422 (%s)", rec.Code, rec.Body.String())
	}
}

// rewriteHeader rebuilds an archive with a mutated header, so a test can forge
// the one field it wants to probe without hand-writing a whole archive.
func rewriteHeader(t *testing.T, archive []byte, mutate func(*backup.Header)) []byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	defer gz.Close()

	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	first := true
	for sc.Scan() {
		line := append([]byte(nil), sc.Bytes()...)
		if first {
			first = false
			var h backup.Header
			if err := json.Unmarshal(line, &h); err != nil {
				t.Fatalf("decode header: %v", err)
			}
			mutate(&h)
			if line, err = json.Marshal(h); err != nil {
				t.Fatalf("encode header: %v", err)
			}
		}
		if _, err := zw.Write(append(line, '\n')); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return out.Bytes()
}

// rebuildArchive gunzips an archive, keeps the first keep(n) of its n lines, and
// re-gzips them cleanly. The result is a VALID gzip file of VALID JSON Lines
// that simply stops early — which is the shape nothing but the trailer can
// detect.
func rebuildArchive(t *testing.T, archive []byte, keep func(n int) int) []byte {
	t.Helper()
	lines := archiveLines(t, archive)
	return gzipLines(t, lines[:keep(len(lines))])
}

// dropRowsKeepingTrailer removes n row records from the middle and puts the
// trailer back, so the file is well formed and the trailer over-claims.
func dropRowsKeepingTrailer(t *testing.T, archive []byte, n int) []byte {
	t.Helper()
	lines := archiveLines(t, archive)
	if len(lines) < n+3 {
		t.Fatalf("archive has only %d lines; cannot drop %d rows", len(lines), n)
	}
	kept := append([][]byte{}, lines[:len(lines)-1-n]...)
	kept = append(kept, lines[len(lines)-1]) // the trailer, unchanged
	return gzipLines(t, kept)
}

func archiveLines(t *testing.T, archive []byte) [][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	defer gz.Close()
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	var out [][]byte
	for sc.Scan() {
		out = append(out, append([]byte(nil), sc.Bytes()...))
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan: %v", err)
	}
	return out
}

func gzipLines(t *testing.T, lines [][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	for _, l := range lines {
		if _, err := zw.Write(append(l, '\n')); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}
