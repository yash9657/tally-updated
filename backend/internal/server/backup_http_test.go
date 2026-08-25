package server_test

import (
	"bufio"
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
	"github.com/khata/cored/internal/server"
)

// ---------------------------------------------------------------------------
// T14 slice 1 — BACKUP
// ---------------------------------------------------------------------------
//
// ⛔ THE ORACLE IS THE CENSUS, NOT A LIST OF TABLES. Every assertion below is
// derived at test time from tenant.Census against the live schema. A test that
// named the tables it expected would be the same defect as a product that named
// them: it would go stale on the day a migration lands, silently, and the
// omission would be discovered by somebody restoring a company that was missing
// a plane of its books.

// archive is a parsed backup, kept as raw JSON per row for the same reason the
// product keeps it raw: decoding a row into `any` routes every numeric through
// float64 and silently rounds an amount.
type archive struct {
	Header  backup.Header
	Rows    map[string][]json.RawMessage
	Trailer backup.Trailer
	Order   []string // tables in the order rows appeared
}

// readArchive gunzips and parses a JSON Lines archive, insisting on the header
// first and the trailer last.
func readArchive(t *testing.T, raw []byte) archive {
	t.Helper()
	gz, err := gzip.NewReader(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("archive is not gzip: %v", err)
	}
	defer gz.Close()

	a := archive{Rows: map[string][]json.RawMessage{}}
	var seenHeader, seenTrailer bool
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 0, 1<<20), 64<<20)
	seenTable := map[string]bool{}
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var env struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(line, &env); err != nil {
			t.Fatalf("archive line is not JSON: %v (%.120s)", err, line)
		}
		switch env.Kind {
		case backup.KindHeader:
			if seenHeader {
				t.Fatal("archive has two headers")
			}
			if len(a.Rows) > 0 {
				t.Fatal("archive header is not the first record")
			}
			if err := json.Unmarshal(line, &a.Header); err != nil {
				t.Fatalf("decode header: %v", err)
			}
			seenHeader = true
		case backup.KindRow:
			if !seenHeader {
				t.Fatal("a row record appeared before the header")
			}
			if seenTrailer {
				t.Fatal("a row record appeared after the trailer")
			}
			var row backup.Row
			if err := json.Unmarshal(line, &row); err != nil {
				t.Fatalf("decode row: %v", err)
			}
			if !seenTable[row.Table] {
				seenTable[row.Table] = true
				a.Order = append(a.Order, row.Table)
			}
			a.Rows[row.Table] = append(a.Rows[row.Table], row.Data)
		case backup.KindTrailer:
			if err := json.Unmarshal(line, &a.Trailer); err != nil {
				t.Fatalf("decode trailer: %v", err)
			}
			seenTrailer = true
		default:
			t.Fatalf("unknown archive record kind %q", env.Kind)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read archive: %v", err)
	}
	if !seenHeader {
		t.Fatal("archive has no header")
	}
	if !seenTrailer {
		t.Fatal("archive has no trailer — a truncated download would be indistinguishable from a complete one")
	}
	return a
}

// fetchBackup drives GET /v1/companies/{id}/backup over the real router.
func fetchBackup(t *testing.T, srv *server.Server, companyID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v1/companies/"+companyID+"/backup", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+ownerToken(t, srv))
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec
}

// censusCounts asks the LIVE SCHEMA how many rows this company has in every
// table the census covers, INCLUDING the zeros.
//
// It runs plan.VerifySQL — the very query the teardown residue check uses — so
// the backup is measured against exactly the enumeration teardown is measured
// against. Nothing here is written down: a table added by tomorrow's migration
// appears in this map with no edit to this file.
func censusCounts(t *testing.T, companyID string) map[string]int64 {
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
	id, err := uuid.Parse(companyID)
	if err != nil {
		t.Fatalf("parse company id: %v", err)
	}
	rows, err := conn.Query(ctx, plan.VerifySQL, id, id.String())
	if err != nil {
		t.Fatalf("census counts: %v", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var table string
		var n int64
		if err := rows.Scan(&table, &n); err != nil {
			t.Fatalf("scan census count: %v", err)
		}
		out[table] = n
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("census counts: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("census returned no tables — the comparison would be vacuous")
	}
	return out
}

// seedBackupCompany creates a company and posts enough through the REAL HTTP
// surface to populate a broad set of tables: masters, a GST sale with inventory,
// a purchase, and a payment that allocates against a bill.
func seedBackupCompany(t *testing.T, srv *server.Server) (string, string) {
	t.Helper()
	companyID, base := newCompany(t, srv)

	debtor := createLedger(t, srv, base, "Backup Debtor", "Sundry Debtors")
	sales := createLedger(t, srv, base, "Backup Sales", "Sales Accounts")
	creditor := createLedger(t, srv, base, "Backup Creditor", "Sundry Creditors")
	purchases := createLedger(t, srv, base, "Backup Purchases", "Purchase Accounts")
	cash := createLedger(t, srv, base, "Backup Cash", "Cash-in-Hand")

	post := func(class string, body map[string]any) map[string]any {
		t.Helper()
		rec, out := do(t, srv, http.MethodPost, base+"/vouchers/"+class, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("post %s status = %d, want 201 (body %s)", class, rec.Code, rec.Body.String())
		}
		return out
	}

	post("sales", map[string]any{
		"voucher_no": "BK-S-1", "voucher_date": "2025-05-10",
		"narration": "backup sale", "party": "Backup Debtor", "created_by": "backup-test",
		"postings": []map[string]any{
			{"ledger_id": debtor, "dr_cr": "D", "amount": "1180.0000"},
			{"ledger_id": sales, "dr_cr": "C", "amount": "1180.0000"},
		},
	})
	post("purchase", map[string]any{
		"voucher_no": "BK-P-1", "voucher_date": "2025-05-11",
		"narration": "backup purchase", "party": "Backup Creditor", "created_by": "backup-test",
		"postings": []map[string]any{
			{"ledger_id": purchases, "dr_cr": "D", "amount": "500.0000"},
			{"ledger_id": creditor, "dr_cr": "C", "amount": "500.0000"},
		},
	})
	post("payment", map[string]any{
		"voucher_no": "BK-PAY-1", "voucher_date": "2025-05-12",
		"narration": "backup payment", "created_by": "backup-test",
		"postings": []map[string]any{
			{"ledger_id": creditor, "dr_cr": "D", "amount": "500.0000"},
			{"ledger_id": cash, "dr_cr": "C", "amount": "500.0000"},
		},
	})
	return companyID, base
}

// TestBackupCoversEveryTableTheCensusReports is slice 1's oracle.
//
// It asserts a PROPERTY — "the archive contains, for every table the census
// covers, exactly the rows that table holds for this company" — over an
// enumeration derived from the live schema at test time. The zeros are asserted
// too, which is what distinguishes "this company genuinely has no price lists"
// from "the price lists did not arrive".
//
// ⛔ MUTATION: drop one table from the backup's insertOrder loop and this test
// names that exact table with the count it did not carry.
func TestBackupCoversEveryTableTheCensusReports(t *testing.T) {
	srv := newServer(t)
	companyID, _ := seedBackupCompany(t, srv)

	rec := fetchBackup(t, srv, companyID)
	if rec.Code != http.StatusOK {
		t.Fatalf("backup status = %d, want 200 (body %.200s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q, want application/gzip", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, ".tallybak.jsonl.gz") {
		t.Errorf("Content-Disposition = %q, want an attachment named *.tallybak.jsonl.gz", cd)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store — an archive is the most sensitive read in the product", cc)
	}

	a := readArchive(t, rec.Body.Bytes())
	want := censusCounts(t, companyID)

	var missing, wrong []string
	var nonEmpty int
	for table, n := range want {
		got := int64(len(a.Rows[table]))
		if n > 0 {
			nonEmpty++
		}
		switch {
		case got == n:
		case got == 0 && n > 0:
			missing = append(missing, fmt.Sprintf("%s (census says %d rows, the archive has NONE)", table, n))
		default:
			wrong = append(wrong, fmt.Sprintf("%s (census %d, archive %d)", table, n, got))
		}
	}
	sort.Strings(missing)
	sort.Strings(wrong)
	if len(missing) > 0 {
		t.Errorf("the archive OMITS tables the census reports rows in — a restore from it would produce a "+
			"company missing a plane of its books:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(wrong) > 0 {
		t.Errorf("the archive disagrees with the census on row counts:\n  %s", strings.Join(wrong, "\n  "))
	}
	// A table in the archive that the census does not cover would mean the two
	// enumerations had drifted apart, which is the whole thing slice 0 exists to
	// make impossible.
	for table := range a.Rows {
		if _, ok := want[table]; !ok {
			t.Errorf("the archive carries table %q, which the census does not cover", table)
		}
	}
	if nonEmpty < 5 {
		t.Fatalf("only %d tables had rows; the comparison is too thin to prove anything", nonEmpty)
	}
	t.Logf("archive matched the census across %d tables (%d non-empty)", len(want), nonEmpty)
}

// TestBackupTrailerIsTheOracleForAShortFile pins the trailer's contract. A
// truncated download is still valid gzip up to the cut and still parses as JSON
// Lines, so without the trailer a restore of a short file would succeed and
// produce a company missing however much did not arrive.
func TestBackupTrailerIsTheOracleForAShortFile(t *testing.T) {
	srv := newServer(t)
	companyID, _ := seedBackupCompany(t, srv)

	a := readArchive(t, fetchBackup(t, srv, companyID).Body.Bytes())
	want := censusCounts(t, companyID)

	if len(a.Trailer.Counts) != len(want) {
		t.Errorf("trailer counts %d tables, census covers %d — the trailer must name every table, "+
			"including the empty ones", len(a.Trailer.Counts), len(want))
	}
	var total int64
	for table, n := range a.Trailer.Counts {
		if got := int64(len(a.Rows[table])); got != n {
			t.Errorf("trailer claims %d rows for %s but the archive carries %d", n, table, got)
		}
		if n != want[table] {
			t.Errorf("trailer claims %d rows for %s but the census says %d", n, table, want[table])
		}
		total += n
	}
	if a.Trailer.Rows != total {
		t.Errorf("trailer row_count = %d, want %d", a.Trailer.Rows, total)
	}
	if total == 0 {
		t.Fatal("the company had no rows at all; the trailer assertion would be vacuous")
	}
}

// TestBackupHeaderStatesWhatItCarries pins the decisions that are recorded IN
// THE FILE rather than only in the code that wrote it — D3, D4 and D5.
func TestBackupHeaderStatesWhatItCarries(t *testing.T) {
	srv := newServer(t)
	companyID, _ := seedBackupCompany(t, srv)

	a := readArchive(t, fetchBackup(t, srv, companyID).Body.Bytes())
	h := a.Header

	if h.Format != backup.FormatName || h.Version != backup.FormatVersion {
		t.Errorf("header format = %q/%d, want %q/%d", h.Format, h.Version, backup.FormatName, backup.FormatVersion)
	}
	if h.SourceCompanyID.String() != companyID {
		t.Errorf("header source_company_id = %s, want %s", h.SourceCompanyID, companyID)
	}
	if h.SchemaVersion == "" {
		t.Error("header carries no schema_version; a restore could not tell an archive from the future " +
			"from one it can read")
	}
	if !h.Contents.InsightdDrafts || !h.Contents.StatutorySteps {
		t.Errorf("header must state that drafts and statutory steps are carried, got %+v", h.Contents)
	}
	if len(h.Contents.NotRestored) == 0 {
		t.Error("header must name the tables a restore will not recreate")
	}
	for _, table := range h.Contents.NotRestored {
		if !backup.IsNotRestored(table) {
			t.Errorf("header names %q as not-restored but the registry disagrees", table)
		}
	}
	// The memberships are IN the file (the census is exact) AND named in the
	// header so a restore can report what it did not re-grant.
	if len(h.Contents.MembershipNames) == 0 {
		t.Error("header lists no memberships, but the creating Owner is one")
	}
	if len(a.Rows["company_membership"]) == 0 {
		t.Error("the archive must still CARRY the membership rows — dropping them at backup time would " +
			"make the file an incomplete record of the company; the refusal belongs at restore")
	}
	// The company row must be first, because a restore inserts in this order and
	// every RLS WITH CHECK on the new tenant depends on it existing.
	if len(a.Order) == 0 || a.Order[0] != "company" {
		t.Errorf("first table in the archive = %v, want company", a.Order)
	}
}

// TestBackupIsRefusedForANonMember is the ACL half. `backup` is its own resource
// token precisely so that a level which never mentions it is DENIED — and the
// thing this asserts is that a signed-in user who is not a member of the company
// cannot download its books.
func TestBackupIsRefusedForANonMember(t *testing.T) {
	srv := newServer(t)
	companyID, _ := seedBackupCompany(t, srv)

	stranger := mintToken(t, srv, "stranger-"+uuid.NewString(), "webauthn")
	req := httptest.NewRequest(http.MethodGet, "/v1/companies/"+companyID+"/backup", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+stranger)
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Fatalf("a non-member downloaded the whole company: status %d", rec.Code)
	}
	if rec.Code != http.StatusForbidden && rec.Code != http.StatusUnauthorized {
		t.Errorf("non-member backup status = %d, want 401/403", rec.Code)
	}
}

// TestEveryNotRestoredTableIsInTheCensus keeps the restore-side registry from
// naming a table that no longer exists. A stale entry would silently widen what
// the round-trip oracle forgives.
func TestEveryNotRestoredTableIsInTheCensus(t *testing.T) {
	if testing.Short() {
		t.Skip("needs the database")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer conn.Close(ctx)
	plan, err := tenant.Census(ctx, conn)
	if err != nil {
		t.Fatalf("census: %v", err)
	}
	covered := map[string]bool{}
	for _, table := range plan.Tables() {
		covered[table] = true
	}
	for _, table := range backup.NotRestoredTables() {
		if !covered[table] {
			t.Errorf("backup's notRestored registry names %q, which the census does not cover — "+
				"remove the stale entry before it silently widens what a restore skips", table)
		}
	}
}
