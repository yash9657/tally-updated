package backup

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/tenant"
)

// ---------------------------------------------------------------------------
// RESTORE
// ---------------------------------------------------------------------------
//
// # A RESTORE CREATES A NEW COMPANY. IT NEVER REPLACES ONE.
//
// That is not a simplification, it is what the schema permits. There is no
// DELETE /companies route anywhere, and every *_no_mutate trigger is
// BEFORE UPDATE OR DELETE and RAISEs for EVERY role — which is why the test
// harness needs a superuser and session_replication_role = replica merely to
// drop a test company. "Restore over the existing company" would therefore mean
// putting a tenant-wipe into the product, and the immutability guarantee the
// whole audit trail rests on would be one function call from being bypassed in
// production. So restore refuses when the company already exists, and says why.
//
// # PRIMARY KEYS ARE PRESERVED, AND THAT IS THE WHOLE POINT
//
// ledger.CanonicalPayload hashes voucher_id, voucher_type_id and every
// ledger_id, stock_item_id, godown_id and batch_id — but NOT company_id. So a
// restore that preserves keys preserves the audit chain, and VerifyChain on the
// restored books attests to the ORIGINAL chain. A restore that re-minted ids
// would look perfect and either report tampering or, far worse, pass against a
// chain it had just re-derived itself — which attests to nothing.
//
// MEASURED: only 6 of 68 primary keys include company_id (voucher, ledger,
// posting, stock_item and voucher_version are all PK(id) alone), so there is no
// cheaper variant that re-mints only the company id and still avoids collisions.
// Preserving keys forces the refusal above, and that is the trade taken.
//
// # ONE TRANSACTION
//
// Six of the ten INSERT-firing triggers are DEFERRABLE INITIALLY DEFERRED
// (posting_balanced, stock_movement_not_negative and four more), so the books
// are checked once, at COMMIT, over the whole restored company rather than
// per-batch. A restore that committed in chunks could leave a company that
// exists, is a member's, opens on screen and is missing its second half — with
// every deferred check having passed on each chunk. A half-restored book is
// worse than a failed restore, and one transaction is the only thing that makes
// it impossible.
//
// # ONE STATEMENT PER TABLE, AND THAT IS A CORRECTNESS MECHANISM
//
// MEASURED: this schema has 83 foreign keys and NOT ONE of them is DEFERRABLE,
// so `SET CONSTRAINTS ALL DEFERRED` would do nothing. Five tables reference
// THEMSELVES — account_group, cost_centre, stock_group, unit and
// gst_inward_statement — and the archive's row order within a table is the
// jsonb sort order, which says nothing about hierarchy. Row-at-a-time inserts
// would fail whenever a child sorted before its parent. Postgres checks a
// non-deferrable FK in an AFTER ROW trigger fired at END OF STATEMENT, so
// inserting a whole table in ONE statement satisfies a self-reference whatever
// the row order — verified directly against this database before it was relied
// on. Across tables, the census order (parents first) does the same job.

// Seeder binds the restoring user to the restored company's Owner level, INSIDE
// the restore transaction.
//
// It is a seam rather than a direct call because the seeding lives in
// internal/authz and this package may not import it: an archive is rows in and
// rows out, and a package that could reach the authorization store is a package
// that could grant access from a file. The server supplies the closure.
//
// ⛔ IT MUST RUN IN THE RESTORE'S OWN TRANSACTION. Seeding afterwards, in a
// second transaction, means a failure there leaves a fully restored company that
// NOBODY IS A MEMBER OF — visible in the database, unreachable through the
// product, and undeletable because there is no delete path.
type Seeder func(ctx context.Context, tx pgx.Tx, companyID, userID uuid.UUID) error

// RestoreResult is what the caller reports to the operator. It is deliberately
// explicit about what did NOT happen.
type RestoreResult struct {
	CompanyID   uuid.UUID `json:"company_id"`
	CompanyName string    `json:"company_name"`
	// Rows is per-table, so an operator can see the shape of what landed.
	Rows map[string]int64 `json:"rows"`
	// RowCount is the total actually inserted (skipped tables excluded).
	RowCount int64 `json:"row_count"`
	// SkippedTables and SkippedRows are what the restore deliberately refused to
	// recreate, reported rather than silently dropped.
	SkippedTables []string `json:"skipped_tables"`
	SkippedRows   int64    `json:"skipped_rows"`
	// MembershipsNotRestored names the people who could reach the SOURCE company
	// and who have NOT been granted access here. Somebody has to re-grant them
	// deliberately; this is the list they work from.
	MembershipsNotRestored []string `json:"memberships_not_restored"`
	// SourceSchemaVersion is the archive's schema version, reported so an
	// operator restoring an older archive knows it.
	SourceSchemaVersion string `json:"source_schema_version"`
	// SchemaVersion is this database's.
	SchemaVersion string `json:"schema_version"`
}

// ErrCompanyExists is returned when the archive's company is already here.
var ErrCompanyExists = errors.New("backup: that company already exists in this database")

// Restore reads a gzipped JSON Lines archive and creates the company it
// describes, in one transaction, preserving every primary key.
func (s *Service) Restore(ctx context.Context, r io.Reader, restoringUser uuid.UUID, seed Seeder) (*RestoreResult, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("backup: this file is not a gzipped archive: %w", err)
	}
	defer gz.Close()

	sc := bufio.NewScanner(gz)
	// Archive lines are whole rows. A jsonb payload can be large (a voucher
	// version carries its entire canonical payload), so the scanner is given room
	// rather than failing with "token too long" on a legitimate archive.
	sc.Buffer(make([]byte, 0, 1<<20), maxArchiveLine)

	hdr, err := readHeader(sc)
	if err != nil {
		return nil, err
	}

	var result *RestoreResult
	err = s.pool.RunInTx(db.WithCompany(ctx, hdr.SourceCompanyID), func(ctx context.Context, tx pgx.Tx) error {
		result, err = s.restoreInTx(ctx, tx, sc, hdr, restoringUser, seed)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// maxArchiveLine bounds a single archive line. One line is one database row, and
// the largest row shape in the schema is a voucher_version carrying its whole
// canonical payload as jsonb.
const maxArchiveLine = 64 << 20

// readHeader consumes the first record and validates it before a transaction is
// even opened — there is no reason to take a connection for a file that is not
// an archive.
func readHeader(sc *bufio.Scanner) (Header, error) {
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return Header{}, fmt.Errorf("backup: read archive: %w", err)
		}
		return Header{}, errors.New("backup: the archive is empty")
	}
	var env envelope
	if err := json.Unmarshal(sc.Bytes(), &env); err != nil {
		return Header{}, fmt.Errorf("backup: the first line of the archive is not a JSON record: %w", err)
	}
	if env.Kind != KindHeader {
		return Header{}, fmt.Errorf("backup: the archive does not begin with a header (first record is %q)", env.Kind)
	}
	var hdr Header
	if err := json.Unmarshal(sc.Bytes(), &hdr); err != nil {
		return Header{}, fmt.Errorf("backup: decode archive header: %w", err)
	}
	if hdr.Format != FormatName {
		return Header{}, fmt.Errorf("backup: this is not a %s archive (it says %q)", FormatName, hdr.Format)
	}
	if hdr.Version != FormatVersion {
		return Header{}, fmt.Errorf(
			"backup: archive format version %d, this build reads version %d", hdr.Version, FormatVersion)
	}
	if hdr.SourceCompanyID == uuid.Nil {
		return Header{}, errors.New("backup: the archive header names no company")
	}
	return hdr, nil
}

// restoreInTx is the body, inside the one transaction.
func (s *Service) restoreInTx(
	ctx context.Context, tx pgx.Tx, sc *bufio.Scanner,
	hdr Header, restoringUser uuid.UUID, seed Seeder,
) (*RestoreResult, error) {
	schemaVersion, err := checkSchema(ctx, tx, hdr)
	if err != nil {
		return nil, err
	}
	if err := refuseIfCompanyExists(ctx, tx, hdr.SourceCompanyID); err != nil {
		return nil, err
	}

	plan, err := tenant.Census(ctx, tx)
	if err != nil {
		return nil, err
	}
	covered := map[string]bool{}
	for _, table := range plan.Tables() {
		covered[table] = true
	}

	res := &RestoreResult{
		CompanyID:              hdr.SourceCompanyID,
		CompanyName:            hdr.SourceName,
		Rows:                   map[string]int64{},
		SkippedTables:          NotRestoredTables(),
		MembershipsNotRestored: hdr.Contents.MembershipNames,
		SourceSchemaVersion:    hdr.SchemaVersion,
		SchemaVersion:          schemaVersion,
	}

	// seen counts EVERY row the archive carried, including the skipped ones, so
	// it can be compared with the trailer. The trailer describes the FILE; the
	// result describes what was written, and conflating the two would let a
	// truncated archive pass by hiding behind a deliberate skip.
	seen := map[string]int64{}

	var (
		batchTable string
		batch      []json.RawMessage
		trailer    *Trailer
	)
	flush := func() error {
		if batchTable == "" || len(batch) == 0 {
			batchTable, batch = "", nil
			return nil
		}
		n, err := insertTable(ctx, tx, batchTable, batch)
		if err != nil {
			return err
		}
		res.Rows[batchTable] += n
		res.RowCount += n
		batchTable, batch = "", nil
		return nil
	}

	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var env envelope
		if err := json.Unmarshal(line, &env); err != nil {
			// ⛔ THE OVERWHELMINGLY LIKELY CAUSE IS A CUT-SHORT DOWNLOAD, and the
			// message has to say so. A truncated archive usually ends mid-line, so
			// the FIRST thing that fails is this decode rather than the trailer
			// check below — and "archive line is not a JSON record: unexpected end
			// of JSON input" is a true sentence that tells an operator nothing they
			// can act on. Found by the browser drive, which cut an archive at 70%
			// and got exactly that.
			return nil, fmt.Errorf(
				"backup: the archive is INCOMPLETE and was not restored — it ends mid-record, which "+
					"means the download was CUT SHORT. Download it again. (%w)", err)
		}
		switch env.Kind {
		case KindHeader:
			return nil, errors.New("backup: the archive has a second header record")
		case KindTrailer:
			if err := flush(); err != nil {
				return nil, err
			}
			var t Trailer
			if err := json.Unmarshal(line, &t); err != nil {
				return nil, fmt.Errorf("backup: decode archive trailer: %w", err)
			}
			trailer = &t
		case KindRow:
			if trailer != nil {
				return nil, errors.New("backup: the archive has rows after its trailer")
			}
			var row Row
			if err := json.Unmarshal(line, &row); err != nil {
				return nil, fmt.Errorf("backup: decode archive row: %w", err)
			}
			if !covered[row.Table] {
				// A table this database's census does not cover. Refusing is the
				// only safe answer: skipping would silently drop a plane of the
				// company, and inserting blind would write outside the tenant model.
				return nil, fmt.Errorf(
					"backup: the archive carries table %q, which this database's census does not cover — "+
						"it was taken from a different schema (archive %s, this database %s)",
					row.Table, hdr.SchemaVersion, schemaVersion)
			}
			seen[row.Table]++
			if IsNotRestored(row.Table) {
				res.SkippedRows++
				continue
			}
			data, err := s.prepareRow(plan, row, hdr.SourceCompanyID)
			if err != nil {
				return nil, err
			}
			if row.Table != batchTable {
				if err := flush(); err != nil {
					return nil, err
				}
				batchTable = row.Table
			}
			batch = append(batch, data)
		default:
			return nil, fmt.Errorf("backup: unknown archive record kind %q", env.Kind)
		}
	}
	if err := sc.Err(); err != nil {
		// A gzip stream cut mid-block surfaces here as an unexpected EOF. Same
		// cause, same sentence: the operator needs to know to download it again.
		return nil, fmt.Errorf(
			"backup: the archive is INCOMPLETE and was not restored — the compressed stream ends "+
				"before it should, which means the download was CUT SHORT. Download it again. (%w)", err)
	}
	if err := flush(); err != nil {
		return nil, err
	}

	// ⛔ THE TRAILER CHECK IS THE WHOLE DEFENCE AGAINST A SHORT FILE, and it runs
	// BEFORE the commit so a truncated archive rolls back rather than producing
	// most of a company.
	if err := checkTrailer(trailer, seen); err != nil {
		return nil, err
	}

	// Bind the restoring user to the restored company's OWN Owner level, in this
	// same transaction. seed_company_auth_defaults is idempotent — every level
	// insert is ON CONFLICT (company_id, name) DO NOTHING and the rule seeding is
	// guarded on the level having no rules — so against a company whose
	// security_level and acl_rule rows have just been restored it adds exactly
	// one thing: the membership.
	if seed != nil && restoringUser != uuid.Nil {
		if err := seed(ctx, tx, hdr.SourceCompanyID, restoringUser); err != nil {
			return nil, fmt.Errorf("backup: grant the restoring user access to the restored company: %w", err)
		}
	}
	return res, nil
}

// checkSchema compares the archive's schema version with this database's.
//
// An archive from the FUTURE is refused, and the reason is subtle enough to be
// worth stating: jsonb_populate_record silently IGNORES keys with no matching
// column, so restoring a newer archive into an older schema would drop whole
// columns without erroring — a company that restored "successfully" and lost a
// field. An OLDER archive is allowed through, because the opposite direction
// fails loudly: a column added since is simply absent from the JSON and lands as
// NULL, and any NOT NULL column added since makes the INSERT raise.
func checkSchema(ctx context.Context, tx pgx.Tx, hdr Header) (string, error) {
	var here string
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(version), '') FROM schema_migrations`).Scan(&here); err != nil {
		return "", fmt.Errorf("backup: read schema version: %w", err)
	}
	if hdr.SchemaVersion > here {
		return here, fmt.Errorf(
			"backup: this archive was taken from a NEWER database (archive %s, this database %s). "+
				"Restoring it here would silently discard every column added since %s, because "+
				"jsonb_populate_record ignores keys it has no column for. Upgrade this database first",
			hdr.SchemaVersion, here, here)
	}
	return here, nil
}

// refuseIfCompanyExists is the consequence of preserving primary keys, and it is
// checked inside the transaction so it cannot race a concurrent restore.
func refuseIfCompanyExists(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) error {
	var name string
	err := tx.QueryRow(ctx, `SELECT name FROM company WHERE id = $1`, companyID).Scan(&name)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("backup: check whether company %s already exists: %w", companyID, err)
	}
	return fmt.Errorf(
		"%w: %q (%s) is already here. This archive preserves every primary key, which is what keeps the "+
			"audit hash chain verifiable, so it cannot be restored beside itself — restore it into a "+
			"different database. There is deliberately no way to overwrite a company: every immutability "+
			"trigger refuses DELETE for every role",
		ErrCompanyExists, name, companyID)
}

// prepareRow applies the ONE value transformation a restore performs: the
// tenancy encoded INSIDE a special table's primary key.
//
// Because primary keys are preserved, the company id does not change and the
// rewrite is an IDENTITY — but it still VALIDATES, and that is why it runs. A
// workflow_id that names a different company means the archive mixes tenants,
// and it is refused rather than restored. The rewrite path itself is exercised
// by tenant's own tests and is what a future restore-under-a-new-id would use.
func (s *Service) prepareRow(plan *tenant.Plan, row Row, companyID uuid.UUID) (json.RawMessage, error) {
	col := tenant.SpecialKeyColumn(row.Table)
	if col == "" {
		return row.Data, nil
	}
	// Decode ONLY the one string column. The rest of the row is left as raw
	// bytes: see the package comment on why a row payload is never decoded.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(row.Data, &fields); err != nil {
		return nil, fmt.Errorf("backup: decode %s row: %w", row.Table, err)
	}
	raw, ok := fields[col]
	if !ok {
		return nil, fmt.Errorf(
			"backup: %s row carries no %s column, but that is where its tenancy is encoded", row.Table, col)
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, fmt.Errorf("backup: %s.%s is not a string: %w", row.Table, col, err)
	}
	rewritten, handled, err := tenant.RewriteSpecialKey(row.Table, value, companyID.String(), companyID.String())
	if err != nil {
		return nil, err
	}
	if !handled || rewritten == value {
		return row.Data, nil
	}
	encoded, err := json.Marshal(rewritten)
	if err != nil {
		return nil, err
	}
	fields[col] = encoded
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// insertTable inserts one table's whole batch in ONE statement. See the
// self-referencing-FK note at the top of this file for why that is a
// correctness mechanism and not only a speed one.
//
// The table name is interpolated because it CANNOT be a parameter in SQL — and
// it is safe because it came from the live catalog through tenant.Census and was
// checked against that census before reaching here, never from the archive
// alone. quoteIdent is applied anyway, for the reason tenant.QuoteLiteral gives:
// a helper that is safe only because of its callers stops being safe the first
// time somebody adds a caller.
func insertTable(ctx context.Context, tx pgx.Tx, table string, rows []json.RawMessage) (int64, error) {
	payload, err := json.Marshal(rows)
	if err != nil {
		return 0, fmt.Errorf("backup: encode %s batch: %w", table, err)
	}
	ident := quoteIdent(table)
	sql := `INSERT INTO ` + ident + ` SELECT * FROM jsonb_populate_recordset(NULL::` + ident + `, $1::jsonb)`
	tag, err := tx.Exec(ctx, sql, string(payload))
	if err != nil {
		return 0, fmt.Errorf("backup: restore %d rows into %s: %w", len(rows), table, err)
	}
	if n := tag.RowsAffected(); n != int64(len(rows)) {
		return 0, fmt.Errorf(
			"backup: restore into %s inserted %d rows from %d archived — the archive and the table disagree",
			table, n, len(rows))
	}
	return tag.RowsAffected(), nil
}

// quoteIdent renders a SQL identifier.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// checkTrailer is the oracle for a truncated download.
//
// A cut-off archive is still valid gzip up to the cut and still parses as JSON
// Lines, so without this a restore of a short file would succeed and produce a
// company missing however much did not arrive. It compares EVERY table the
// trailer names, including the zeros, and it runs before COMMIT.
func checkTrailer(trailer *Trailer, seen map[string]int64) error {
	if trailer == nil {
		return errors.New(
			"backup: the archive is INCOMPLETE and was not restored — it has no trailer, which means the " +
				"download was CUT SHORT. A truncated archive is still valid gzip and still parses, so the " +
				"trailer is the only thing that can tell a complete file from most of one. Download it again")
	}
	var bad []string
	for table, want := range trailer.Counts {
		if got := seen[table]; got != want {
			bad = append(bad, fmt.Sprintf("%s (trailer says %d, the file carries %d)", table, want, got))
		}
	}
	for table, got := range seen {
		if _, ok := trailer.Counts[table]; !ok {
			bad = append(bad, fmt.Sprintf("%s (%d rows the trailer does not account for)", table, got))
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("backup: the archive is INCOMPLETE and was not restored:\n  %s", strings.Join(bad, "\n  "))
	}
	var total int64
	for _, n := range seen {
		total += n
	}
	if trailer.Rows != total {
		return fmt.Errorf(
			"backup: the archive is INCOMPLETE and was not restored: trailer says %d rows in total, "+
				"the file carries %d", trailer.Rows, total)
	}
	return nil
}
