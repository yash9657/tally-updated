package masters

// stock_group.go — THE ITEM HIERARCHY (feature T10, migration 0079).
//
// A stock group is what makes a Stock Summary readable on a company with four
// hundred items: "Raw Materials 4,50,000 / Finished Goods 2,10,000" instead of
// four hundred lines. It is modelled on account_group down to the LTREE path,
// because it is the same problem — a group's total is its own items plus every
// descendant's — and reusing the shape means the reporting side already knows
// how to roll it up.
//
// ⛔ WHAT A STOCK GROUP IS NOT: an accounting classification. Every item in every
// group lands in the same Stock-in-Hand asset, so there is no `nature` here and
// no group anywhere decides which statement a figure appears on. A group decides
// how the Stock Summary is SUBTOTALLED and nothing else. (account_group needs a
// nature because a primary group's nature is what puts a ledger on the Balance
// Sheet rather than the P&L.)

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// StockGroup is one node of the item hierarchy. ParentName is empty for a
// top-level group.
type StockGroup struct {
	ID         uuid.UUID
	Name       string
	ParentName string
}

// StockGroupRecord is a group as stored, for a list or a picker. Path is the
// ltree ancestry — the reports use it to roll a branch up in one predicate, and
// a client can use it to render the tree in order without a second query.
type StockGroupRecord struct {
	ID         uuid.UUID
	Name       string
	ParentID   uuid.UUID
	ParentName string
	Path       string
	// ItemCount is how many stock items sit DIRECTLY in this group (not in its
	// descendants). It is here because the one question a picker has to answer
	// before offering a delete is "is this group empty".
	ItemCount int
}

// CreateStockGroup creates a stock group and appends a master_audit row.
//
// A group whose parent is named must have that parent already; the parent is
// resolved case-insensitively, because a Tally export spells its own group names
// inconsistently across four years of use and an exact match would create a
// second "Raw Materials" beside the first.
func (s *Service) CreateStockGroup(ctx context.Context, g StockGroup, createdBy string) (uuid.UUID, error) {
	if g.Name == "" {
		return uuid.Nil, errors.New("masters: stock group name is required")
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	id, err := uuid.NewV7()
	if err != nil {
		return uuid.Nil, fmt.Errorf("masters: new stock group id: %w", err)
	}
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var (
			parentID   *uuid.UUID
			parentPath string
		)
		if g.ParentName != "" {
			var (
				pid   uuid.UUID
				ppath string
			)
			err := tx.QueryRow(ctx,
				`SELECT id, path::text FROM stock_group
				  WHERE lower(btrim(name)) = lower(btrim($1))`, g.ParentName).Scan(&pid, &ppath)
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("masters: %w: parent stock group %q", ErrNotFound, g.ParentName)
			}
			if err != nil {
				return fmt.Errorf("masters: resolve parent stock group: %w", err)
			}
			parentID, parentPath = &pid, ppath
		}
		// The ltree path, exactly as CreateGroup builds one: a top-level group is
		// a single "g<id>" label and a child appends "s<id>" to its parent's
		// path. It is passed as text and cast in SQL, so it is fully bound.
		path := "g" + dashless(id)
		if parentID != nil {
			path = parentPath + ".s" + dashless(id)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO stock_group (id, company_id, name, parent_id, path)
			 VALUES ($1, $2, $3, $4, $5::ltree)`,
			id, companyID, g.Name, parentID, path,
		); err != nil {
			return nameTakenNormalised(err,
				"stock_group_company_name_normalised", "stock group", g.Name, "insert stock group")
		}
		after := map[string]any{"name": g.Name, "parent": g.ParentName}
		return appendAudit(ctx, tx, companyID, "stock_group", id, OpCreate, nil, after, createdBy)
	})
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// StockGroupIDByName resolves a stock group case-insensitively, returning the id
// and the name as stored.
func (s *Service) StockGroupIDByName(ctx context.Context, name string) (uuid.UUID, string, error) {
	return s.resolveNormalised(ctx, "stock_group", "name", name)
}

// EnsureStockGroup resolves a stock group by name and creates it if absent. An
// already-present group is returned UNCHANGED — its parent is never restated, so
// re-importing a hierarchy cannot reshape one.
func (s *Service) EnsureStockGroup(ctx context.Context, g StockGroup, createdBy string) (
	id uuid.UUID, stored string, created bool, err error) {

	id, stored, err = s.StockGroupIDByName(ctx, g.Name)
	if err == nil {
		return id, stored, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return uuid.Nil, "", false, err
	}
	id, err = s.CreateStockGroup(ctx, g, createdBy)
	if err != nil {
		return uuid.Nil, "", false, err
	}
	return id, g.Name, true, nil
}

// ListStockGroups enumerates the hierarchy in PATH order, which is tree order:
// a parent always precedes its children, so a client can render the tree by
// indenting on the number of path labels without sorting anything itself.
func (s *Service) ListStockGroups(ctx context.Context) ([]StockGroupRecord, error) {
	out := []StockGroupRecord{}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT g.id, g.name, g.parent_id, COALESCE(p.name, ''), g.path::text,
			        (SELECT count(*) FROM stock_item si WHERE si.stock_group_id = g.id)
			   FROM stock_group g
			   LEFT JOIN stock_group p ON p.id = g.parent_id
			  ORDER BY g.path`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				r        StockGroupRecord
				parentID *uuid.UUID
			)
			if err := rows.Scan(&r.ID, &r.Name, &parentID, &r.ParentName, &r.Path, &r.ItemCount); err != nil {
				return err
			}
			if parentID != nil {
				r.ParentID = *parentID
			}
			out = append(out, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
