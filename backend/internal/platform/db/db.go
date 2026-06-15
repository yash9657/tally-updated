// Package db provides the tenant-safe PostgreSQL access layer for cored.
//
// The single most important invariant in the whole system is tenant isolation.
// Every query against a tenant table MUST run with the app.current_company_id
// GUC set to the caller's company, so that the default-deny row-level-security
// policies scope the result to that tenant. This package makes that invariant
// hard to violate:
//
//   - The application connects as the unprivileged tally_app role (NOBYPASSRLS).
//   - A company id is carried in context.Context via WithCompany.
//   - The query API (Tx / RunInTx) REFUSES to touch the database when no company
//     is present in context, returning ErrNoCompany before any SQL is issued.
//   - Each transaction issues `SET LOCAL app.current_company_id = $1` as its very
//     first statement, so the GUC is scoped to that transaction and reset on
//     commit/rollback automatically.
package db

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNoCompany is returned by the query API when no company id is present in the
// context. It is returned BEFORE any database round-trip, so a forgotten company
// scope can never leak data.
var ErrNoCompany = errors.New("db: no company in context (tenant scope required)")

// companyKey is the unexported context key under which the company id is stored.
type companyKey struct{}

// userKey and subjectKey carry the authenticated caller's identity, set by the
// auth middleware (WS1, Task D). They drive the user/subject GUCs the auth
// catalog RLS policies (0011) resolve against.
type userKey struct{}
type subjectKey struct{}

// WithCompany returns a copy of ctx carrying the given company id. All
// subsequent tenant-scoped queries derived from this context will be scoped to
// that company.
func WithCompany(ctx context.Context, companyID uuid.UUID) context.Context {
	return context.WithValue(ctx, companyKey{}, companyID)
}

// CompanyFromContext extracts the company id previously stored by WithCompany.
// The boolean is false if no company is present.
func CompanyFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(companyKey{}).(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

// WithUser returns a copy of ctx carrying the authenticated user id. Auth
// catalog reads/writes scoped to the user (company_membership, webauthn_credential,
// app_user-by-id) run with the user GUC set from this value.
func WithUser(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, userKey{}, userID)
}

// UserFromContext extracts the user id previously stored by WithUser.
func UserFromContext(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(userKey{}).(uuid.UUID)
	if !ok || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

// WithSubject returns a copy of ctx carrying the authenticated JWT subject.
// The create-on-first-login and app_user-by-subject reads run with the subject
// GUC set from this value.
func WithSubject(ctx context.Context, subject string) context.Context {
	return context.WithValue(ctx, subjectKey{}, subject)
}

// SubjectFromContext extracts the subject previously stored by WithSubject.
func SubjectFromContext(ctx context.Context) (string, bool) {
	s, ok := ctx.Value(subjectKey{}).(string)
	if !ok || s == "" {
		return "", false
	}
	return s, true
}

// Pool is a thin, tenant-aware wrapper around a pgx connection pool. It must be
// constructed with New (which connects as the application role) and closed with
// Close.
type Pool struct {
	pool *pgxpool.Pool
}

// New opens a connection pool against dsn. The dsn should authenticate as the
// least-privilege application role (tally_app), never an admin/superuser.
func New(ctx context.Context, dsn string) (*Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: parse dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: connect: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: ping: %w", err)
	}
	return &Pool{pool: pool}, nil
}

// Close releases all pooled connections.
func (p *Pool) Close() {
	if p.pool != nil {
		p.pool.Close()
	}
}

// Ping verifies connectivity.
func (p *Pool) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// Raw exposes the underlying pool for the migration runner and other
// admin-path callers that intentionally operate outside tenant scope. Business
// code MUST NOT use this; use RunInTx instead.
func (p *Pool) Raw() *pgxpool.Pool { return p.pool }

// TxFunc is a unit of work executed inside a tenant-scoped transaction.
type TxFunc func(ctx context.Context, tx pgx.Tx) error

// RunInTx runs fn inside a single transaction that is bound to the company id
// found in ctx. It returns ErrNoCompany (without contacting the database) if no
// company is in scope. The GUC app.current_company_id is set with SET LOCAL as
// the first statement, so RLS is enforced for every statement fn issues. The
// transaction is committed if fn returns nil, otherwise rolled back.
func (p *Pool) RunInTx(ctx context.Context, fn TxFunc) error {
	companyID, ok := CompanyFromContext(ctx)
	if !ok {
		return ErrNoCompany
	}

	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin tx: %w", err)
	}
	// Bind the tenant GUC for the lifetime of this transaction. SET LOCAL is
	// transaction-scoped, so it cannot leak to another pooled session.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_company_id', $1, true)", companyID.String()); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("db: set company guc: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

// RunInReadOnlyTx runs fn inside a READ-ONLY, company-scoped transaction. It is
// byte-for-byte RunInTx except that the transaction is begun with
// pgx.TxOptions{AccessMode: pgx.ReadOnly}, which maps to Postgres
// `BEGIN ... READ ONLY`: any INSERT/UPDATE/DELETE/DDL issued inside it errors
// with "cannot execute ... in a read-only transaction", independent of role
// grants. This is the execution path for the conversational query core (the
// NL->DSL->compiler pipeline) and is pure defense-in-depth: a compiler bug that
// somehow emitted a write would be rejected by Postgres before it could mutate
// anything.
//
// The ErrNoCompany short-circuit is preserved (returned BEFORE any database
// round-trip), and the company GUC is set with set_config(...,true) as the FIRST
// statement so RLS still scopes the tenant. set_config(...,true) is a SELECT (a
// read), so it runs fine inside a read-only transaction. The read-only flag
// alone does NOT scope the tenant — RLS needs the company GUC; both guarantees
// are present here.
func (p *Pool) RunInReadOnlyTx(ctx context.Context, fn TxFunc) error {
	companyID, ok := CompanyFromContext(ctx)
	if !ok {
		return ErrNoCompany
	}

	tx, err := p.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("db: begin read-only tx: %w", err)
	}
	// Bind the tenant GUC for the lifetime of this transaction. set_config with
	// is_local=true is a SELECT, so it is permitted in a read-only transaction.
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_company_id', $1, true)", companyID.String()); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("db: set company guc: %w", err)
	}

	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit read-only tx: %w", err)
	}
	return nil
}

// RunInAuthTx runs fn inside a transaction whose subject and/or user GUCs are
// bound from ctx (WithSubject / WithUser), WITHOUT the ErrNoCompany guard. It is
// the helper the auth catalog reads use: looking a user up by their JWT subject
// (or by id), and reading their company memberships, BEFORE any company is in
// scope. The GUCs are set transaction-local via set_config(...,true), exactly
// like RunInTx binds the company GUC, so the 0011 RLS resolvers
// (app_current_subject / app_current_user_id) see them.
//
// Unlike RunInTx, an absent subject/user does NOT short-circuit: the tx simply
// runs with those GUCs unset, which the 0011 policies treat as default-deny (the
// auth catalog returns no rows). It NEVER sets the company GUC; company-scoped
// catalog reads (security_level, acl_rule) use RunInTx after membership is
// confirmed.
func (p *Pool) RunInAuthTx(ctx context.Context, fn TxFunc) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin auth tx: %w", err)
	}
	if subject, ok := SubjectFromContext(ctx); ok {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_subject', $1, true)", subject); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("db: set subject guc: %w", err)
		}
	}
	if userID, ok := UserFromContext(ctx); ok {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", userID.String()); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("db: set user guc: %w", err)
		}
	}
	if err := fn(ctx, tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit auth tx: %w", err)
	}
	return nil
}
