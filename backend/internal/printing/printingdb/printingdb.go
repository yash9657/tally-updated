// Package printingdb is the DB-FACING HALF of internal/printing: the store that
// reads and writes what a company chose to CHANGE about its printed documents.
//
// WHY THE OVERRIDE STORE IS A SEPARATE PACKAGE FROM THE RENDERER
// -------------------------------------------------------------
// internal/printing is pure — a document value in, deterministic bytes out — and
// that purity is what lets a golden test assert on the bytes instead of on a
// screenshot. A package that could open a transaction could also read a rate, a
// balance or a template mid-render, and the same invoice would then print
// differently depending on when it was printed. So the I/O lives here and the
// renderer stays a leaf: this package imports internal/printing (for the
// TemplateOverrides value it returns), and internal/printing imports nothing.
//
// WHAT AN ABSENT ROW MEANS, AND WHY THAT IS THE WHOLE DESIGN
// ---------------------------------------------------------
// The built-in default template lives in CODE. Migration 0037 stores only
// OVERRIDES, so LoadOverrides returns the ZERO printing.TemplateOverrides and NO
// ERROR when a company has no row for a kind. That is not leniency, it is the
// guarantee that a company created five minutes ago can print a correct Rule 46
// tax invoice with no seeding step to fail and no per-company backfill to run.
// A store that returned "not found" here would push that decision onto every
// caller, and the first caller to treat it as an error would be a company that
// cannot print at all.
//
// TENANCY: THE GUC IS SET ON EVERY TRANSACTION THIS PACKAGE OPENS
// --------------------------------------------------------------
// print_template has ENABLE + FORCE row-level security and a policy with both
// USING and WITH CHECK pinned to app_current_company_id() (migration 0037). That
// resolver returns NULL when the GUC is unset, so an unscoped transaction reads
// NOTHING and writes NOTHING — default deny, not default leak. Every statement
// this package issues therefore runs inside inTenantTx, which sets
// app.current_company_id transaction-locally as its FIRST statement.
//
// The constructor takes a *pgxpool.Pool rather than the *platform/db.Pool that
// most stores take, because that is the seam the print HTTP surface is built
// against. The consequence is that the eight-line GUC preamble in inTenantTx is
// a DELIBERATE DUPLICATE of db.Pool.RunInTx's, and the two must not drift: the
// SQL is copied verbatim (`SELECT set_config('app.current_company_id', $1,
// true)`), and TestATransactionWithNoTenantGUCReadsNoTemplateAtAll is what
// proves the GUC — not the WHERE clause — is what makes a template visible.
package printingdb

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoCompany is returned when a caller asks for template overrides without a
// company. It is returned BEFORE any database round-trip, exactly like
// db.ErrNoCompany: a forgotten tenant scope must fail loudly rather than run a
// query whose RLS predicate happens to deny it, because "denied by RLS" and
// "this company genuinely has no override" are indistinguishable in the result
// and only one of them is a bug.
var ErrNoCompany = errors.New("printingdb: no company (tenant scope required)")

// Store reads and writes print_template.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds the store over an application-role pool.
//
// The pool MUST authenticate as the least-privilege application role
// (tally_app, NOBYPASSRLS). Handed a superuser pool, every query here would
// bypass row-level security and the tenant predicate would stop being enforced —
// which is why the print HTTP surface passes the same pool the rest of the
// server uses and never an admin one.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// txFunc is a unit of work inside a tenant-scoped transaction.
type txFunc func(ctx context.Context, tx pgx.Tx) error

// inTenantTx runs fn inside one transaction bound to companyID.
//
// readOnly maps to Postgres `BEGIN ... READ ONLY`, under which any INSERT,
// UPDATE, DELETE or DDL errors regardless of role grants. It is pure
// defence-in-depth on the read path: printing is NOT a write (authz.Print's
// IsWrite() is false), so a print request that somehow reached a mutating
// statement is a bug that should fail rather than mutate.
//
// set_config(..., true) is transaction-local, so the GUC cannot leak to the next
// user of a pooled connection, and it is a SELECT, so it is permitted inside a
// read-only transaction.
func (s *Store) inTenantTx(ctx context.Context, companyID uuid.UUID, readOnly bool, fn txFunc) error {
	if companyID == uuid.Nil {
		return ErrNoCompany
	}

	opts := pgx.TxOptions{}
	if readOnly {
		opts.AccessMode = pgx.ReadOnly
	}
	tx, err := s.pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("printingdb: begin tx: %w", err)
	}
	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.current_company_id', $1, true)", companyID.String()); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("printingdb: set company guc: %w", err)
	}
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("printingdb: commit: %w", err)
	}
	return nil
}
