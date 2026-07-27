package masters

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// --------------------------------------------------------------------------
// Godown (stock location)
// --------------------------------------------------------------------------

// MainLocationName is the name of the system godown every company is seeded
// with (migration 0025's seed_inventory_defaults, which seed_company_defaults
// PERFORMs for every new company and which was backfilled over every existing
// one).
//
// It exists so stock always has a named place to sit even for a tenant that
// never enables multi-location. The alternative — leaving godown_id NULL for
// single-location companies — means every stock report has to special-case a
// NULL dimension, and the day the tenant DOES enable a second location their
// entire history sits in a location that has no name to show in a column.
const MainLocationName = "Main Location"

// Godown is a stock location — Tally calls these Godowns (or Locations, in the
// UK/Intl. product). It is a pure dimension: it carries no balance, because
// stock VALUE is never stored and closing QUANTITY is derived by replaying
// stock_movement.
//
// There is deliberately no IsSystem field here. is_system marks the seeded
// 'Main Location' and is the flag a future delete path will refuse to act on;
// letting a caller assert it would let a user mint an undeletable location.
// Only seed_inventory_defaults creates system rows.
type Godown struct {
	ID   uuid.UUID
	Name string
}

// CreateGodown creates a stock location and appends a master_audit CREATE row.
//
// The audit append is INSIDE the transaction on purpose (as everywhere else in
// this package): an audit row for a location that does not exist is worse than
// no audit row at all — it is a false statement in the Rule 11(g) edit log,
// and the log is only worth having if it can be trusted to describe rows that
// actually landed.
func (s *Service) CreateGodown(ctx context.Context, g Godown, createdBy string) (uuid.UUID, error) {
	if g.Name == "" {
		return uuid.Nil, errors.New("masters: godown name is required")
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new godown id: %w", err)
	}
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// is_system is left at its DEFAULT false: a user-created location is
		// never a system location. See the type comment.
		if _, err := tx.Exec(ctx,
			`INSERT INTO godown (id, company_id, name)
			 VALUES ($1, $2, $3)`,
			id, companyID, g.Name,
		); err != nil {
			return fmt.Errorf("masters: insert godown: %w", err)
		}
		after := map[string]any{"name": g.Name}
		return appendAudit(ctx, tx, companyID, "godown", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// GodownID resolves a stock location's id by name within the company in
// context. Another tenant's location of the same name is not merely filtered
// out here — it is invisible to the query, because RLS confines the statement
// to the company GUC RunInTx binds.
func (s *Service) GodownID(ctx context.Context, name string) (uuid.UUID, error) {
	return s.resolveID(ctx, "godown", name)
}

// MainLocationID returns the company's system 'Main Location', creating it if
// it is somehow absent. It is the default position for stock that a caller has
// not explicitly located — notably the opening position CreateStockItem
// writes.
func (s *Service) MainLocationID(ctx context.Context) (uuid.UUID, error) {
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	var id uuid.UUID
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		id, err = mainLocationID(ctx, tx, companyID)
		return err
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// mainLocationID resolves the system 'Main Location' inside the caller's
// transaction, self-healing via seed_inventory_defaults if the row is missing.
//
// The self-heal is not defensive clutter: seed_inventory_defaults is idempotent
// and GRANTed to tally_app precisely so a caller can guarantee the location
// exists. Without it, a company created by any path that does not run
// seed_company_defaults (a restore, a fixture, a future import) could accept a
// stock item with an opening quantity and then fail on the FK — reporting a
// foreign-key violation to a user whose only sin was typing an opening
// balance.
func mainLocationID(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := tx.QueryRow(ctx,
		`SELECT id FROM godown WHERE name = $1`, MainLocationName).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("masters: resolve %q: %w", MainLocationName, err)
	}
	if _, err := tx.Exec(ctx, `SELECT seed_inventory_defaults($1)`, companyID); err != nil {
		return uuid.Nil, fmt.Errorf("masters: seed inventory defaults: %w", err)
	}
	if err := tx.QueryRow(ctx,
		`SELECT id FROM godown WHERE name = $1`, MainLocationName).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("masters: resolve %q after seed: %w", MainLocationName, err)
	}
	return id, nil
}
