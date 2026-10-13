package backup

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/platform/tenant"
)

// Service reads and writes company archives.
type Service struct {
	pool *db.Pool
	// now is injectable so a test can assert a deterministic header without
	// reaching for a clock.
	now func() time.Time
}

// New builds the service.
func New(pool *db.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// Write streams a gzipped JSON Lines archive of companyID to w.
//
// IT RUNS IN ONE READ-ONLY TRANSACTION, which is doing two jobs. A single
// snapshot means the archive is CONSISTENT: without it, a voucher posted while
// table 40 of 62 was being read would appear in the header table and not in its
// postings, and the archive would restore into a book that does not balance.
// Read-only is defense in depth — an archive is the most sensitive read in the
// product and it has no business being able to write.
//
// RLS does the tenant scoping, as it does for every other read: RunInReadOnlyTx
// sets app.current_company_id transaction-locally as its first statement, so the
// per-table WHERE company_id = $1 below is belt AND braces rather than the only
// thing standing between two tenants.
func (s *Service) Write(ctx context.Context, w io.Writer, companyID uuid.UUID) (err error) {
	if companyID == uuid.Nil {
		return fmt.Errorf("backup: refusing to archive the nil company")
	}
	gz := gzip.NewWriter(w)
	// The gzip writer's Close is what flushes the final block. A deferred Close
	// whose error is dropped would produce a TRUNCATED archive reported as a
	// success, which is precisely the failure the trailer exists to catch on the
	// reading side — so it is caught on the writing side too.
	defer func() {
		cerr := gz.Close()
		if err == nil && cerr != nil {
			err = fmt.Errorf("backup: flush archive: %w", cerr)
		}
	}()
	enc := json.NewEncoder(gz)

	return s.pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		plan, err := tenant.Census(ctx, tx)
		if err != nil {
			return err
		}

		hdr, err := s.header(ctx, tx, plan, companyID)
		if err != nil {
			return err
		}
		if err := enc.Encode(hdr); err != nil {
			return fmt.Errorf("backup: write header: %w", err)
		}

		counts := map[string]int64{}
		var total int64
		for _, table := range insertOrder(plan) {
			n, err := s.writeTable(ctx, tx, enc, plan, table, companyID)
			if err != nil {
				return err
			}
			counts[table] = n
			total += n
		}

		if err := enc.Encode(Trailer{Kind: KindTrailer, Counts: counts, Rows: total}); err != nil {
			return fmt.Errorf("backup: write trailer: %w", err)
		}
		return nil
	})
}

// insertOrder returns the census tables in the order a RESTORE needs them:
// parents before children, the company row first. plan.Order is DELETE order
// (children first, tenant root last), so this is simply its reverse; the special
// tables participate in no foreign key at all and go last.
//
// ⛔ IT IS DERIVED, NOT WRITTEN DOWN. The one thing this package must never
// acquire is a list of tables — see the tenant package comment.
func insertOrder(plan *tenant.Plan) []string {
	out := make([]string, 0, len(plan.Order)+len(plan.Special))
	for i := len(plan.Order) - 1; i >= 0; i-- {
		out = append(out, plan.Order[i].Table)
	}
	for _, s := range plan.Special {
		out = append(out, s.Table)
	}
	return out
}

// header assembles the archive header, including the memberships it is
// deliberately NOT carrying, by name.
func (s *Service) header(ctx context.Context, tx pgx.Tx, plan *tenant.Plan, companyID uuid.UUID) (Header, error) {
	var name string
	if err := tx.QueryRow(ctx, `SELECT name FROM company WHERE id = $1`, companyID).Scan(&name); err != nil {
		if err == pgx.ErrNoRows {
			return Header{}, fmt.Errorf("backup: company %s does not exist", companyID)
		}
		return Header{}, fmt.Errorf("backup: read company %s: %w", companyID, err)
	}

	var schemaVersion string
	if err := tx.QueryRow(ctx,
		`SELECT coalesce(max(version)::text, '') FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		return Header{}, fmt.Errorf("backup: read schema version: %w", err)
	}

	members, err := memberNames(ctx, tx, companyID)
	if err != nil {
		return Header{}, err
	}

	return Header{
		Kind:            KindHeader,
		Format:          FormatName,
		Version:         FormatVersion,
		CreatedAt:       s.now().UTC(),
		SourceCompanyID: companyID,
		SourceName:      name,
		SchemaVersion:   schemaVersion,
		Tables:          insertOrder(plan),
		Contents: Contents{
			InsightdDrafts:  true,
			StatutorySteps:  true,
			NotRestored:     NotRestoredTables(),
			MembershipNames: members,
		},
	}, nil
}

// CompanyName reads the company's display name, for the download filename. It is
// a separate, tiny read rather than a field pulled out of Write because the
// filename has to be in the response HEADERS, and headers must be sent before
// the first byte of the archive body.
func (s *Service) CompanyName(ctx context.Context, companyID uuid.UUID) (string, error) {
	var name string
	err := s.pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT name FROM company WHERE id = $1`, companyID).Scan(&name); err != nil {
			if err == pgx.ErrNoRows {
				return fmt.Errorf("backup: company %s does not exist", companyID)
			}
			return fmt.Errorf("backup: read company %s: %w", companyID, err)
		}
		return nil
	})
	return name, err
}

// memberNames lists who could reach the source company, so the restore can
// report what it did not recreate. It reads NAMES ONLY — never a credential,
// never a security level id — because this list is documentation for a human,
// not an instruction the restore acts on.
func memberNames(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) ([]string, error) {
	rows, err := tx.Query(ctx, `
SELECT coalesce(nullif(u.display_name, ''), u.subject)
  FROM company_membership m
  JOIN app_user u ON u.id = m.user_id
 WHERE m.company_id = $1
 ORDER BY 1`, companyID)
	if err != nil {
		return nil, fmt.Errorf("backup: list memberships: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, fmt.Errorf("backup: scan membership: %w", err)
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// writeTable streams one table's rows and returns how many it wrote.
//
// The SELECT is built from the census: a scoped table is filtered on its
// company_id column with the id bound as UUID or TEXT according to the type the
// census RECORDED (insightd_draft's is TEXT), and a special table is filtered by
// its REGISTERED predicate, rendered — never re-spelled — by tenant.
func (s *Service) writeTable(
	ctx context.Context, tx pgx.Tx, enc *json.Encoder,
	plan *tenant.Plan, table string, companyID uuid.UUID,
) (int64, error) {
	sql, arg, err := selectSQL(plan, table, companyID)
	if err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, sql, arg)
	if err != nil {
		return 0, fmt.Errorf("backup: read %s for company %s: %w", table, companyID, err)
	}
	defer rows.Close()

	var n int64
	for rows.Next() {
		// json.RawMessage, NEVER a decoded value: see the package comment.
		var raw json.RawMessage
		if err := rows.Scan(&raw); err != nil {
			return 0, fmt.Errorf("backup: scan %s row: %w", table, err)
		}
		if err := enc.Encode(Row{Kind: KindRow, Table: table, Data: raw}); err != nil {
			return 0, fmt.Errorf("backup: write %s row: %w", table, err)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("backup: read %s for company %s: %w", table, companyID, err)
	}
	return n, nil
}

// selectSQL renders one table's read. `ORDER BY 1` sorts on the jsonb value
// itself, which is a total order in Postgres: it costs a sort of a few hundred
// rows and buys a BYTE-STABLE archive, so two backups of an unchanged company
// are diffable and a round-trip test can compare archives rather than only row
// counts.
func selectSQL(plan *tenant.Plan, table string, companyID uuid.UUID) (string, any, error) {
	for _, sc := range plan.Order {
		if sc.Table != table {
			continue
		}
		q := `SELECT to_jsonb(x) FROM ` + table + ` x WHERE x.company_id = $1 ORDER BY 1`
		if sc.IsText {
			return q, companyID.String(), nil
		}
		return q, companyID, nil
	}
	for _, sp := range plan.Special {
		if sp.Table != table {
			continue
		}
		return `SELECT to_jsonb(x) FROM ` + table + ` x WHERE ` +
			tenant.RenderPredicate(sp.Predicate, "$1") + ` ORDER BY 1`, companyID.String(), nil
	}
	// Unreachable via insertOrder, which is derived from the same plan. It is an
	// error rather than a skip because a table that fell out of the plan between
	// enumeration and reading must not become a silently empty section.
	return "", nil, fmt.Errorf("backup: table %q is not in the census plan", table)
}
