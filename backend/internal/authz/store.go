package authz

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/platform/db"
)

// Store reads the auth catalog (0011) under the correct GUCs. The three reads
// happen across three GUC scopes, matching the pinned middleware ordering:
//
//   - LookupUserBySubject: under the SUBJECT GUC (RunInAuthTx), before any user
//     id is known.
//   - Membership: under the USER GUC (RunInAuthTx), before any company is in
//     scope.
//   - Level / Rules: under the COMPANY GUC (RunInTx), after membership confirms
//     the caller belongs to {companyID}.
type Store struct {
	pool *db.Pool
}

// NewStore builds a Store over the shared application pool.
func NewStore(pool *db.Pool) *Store { return &Store{pool: pool} }

// ErrNotFound is returned (wrapped) when a catalog row does not exist / is not
// visible under the current GUC scope.
var ErrNotFound = errors.New("authz: not found")

// LookupUserBySubject resolves an app_user row by its JWT subject, returning the
// user id and whether the account is active. found=false when no such user is
// visible (RLS scopes the read to subject = app_current_subject()). The caller
// must have set the subject in ctx via db.WithSubject.
func (s *Store) LookupUserBySubject(ctx context.Context, subject string) (userID uuid.UUID, active bool, found bool, err error) {
	ctx = db.WithSubject(ctx, subject)
	err = s.pool.RunInAuthTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT id, is_active FROM app_user WHERE subject = $1`, subject)
		scanErr := row.Scan(&userID, &active)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			found = false
			return nil
		}
		if scanErr != nil {
			return scanErr
		}
		found = true
		return nil
	})
	if err != nil {
		return uuid.Nil, false, false, fmt.Errorf("authz: lookup user by subject: %w", err)
	}
	return userID, active, found, nil
}

// CreateUserOnFirstLogin inserts an app_user for the verified subject if none
// exists, returning the (existing or new) user id. The INSERT runs under the
// subject GUC, so the RLS WITH CHECK (subject = app_current_subject()) passes.
// Used only on the POST /v1/companies bootstrap path (authn-only).
func (s *Store) CreateUserOnFirstLogin(ctx context.Context, subject, email, displayName string) (uuid.UUID, error) {
	ctx = db.WithSubject(ctx, subject)
	var userID uuid.UUID
	err := s.pool.RunInAuthTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Try to read first (visible under subject GUC).
		err := tx.QueryRow(ctx, `SELECT id FROM app_user WHERE subject = $1`, subject).Scan(&userID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		userID = uuid.New()
		_, err = tx.Exec(ctx,
			`INSERT INTO app_user (id, subject, email, display_name)
			 VALUES ($1, $2, $3, $4)`,
			userID, subject, email, displayName)
		return err
	})
	if err != nil {
		return uuid.Nil, fmt.Errorf("authz: create user on first login: %w", err)
	}
	return userID, nil
}

// Membership returns the caller's security level id for companyID, and whether a
// membership exists. The read runs under the USER GUC, so a user sees only their
// own memberships (the membership oracle). The caller must have set the user in
// ctx via db.WithUser.
func (s *Store) Membership(ctx context.Context, userID, companyID uuid.UUID) (securityLevelID uuid.UUID, found bool, err error) {
	ctx = db.WithUser(ctx, userID)
	err = s.pool.RunInAuthTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx,
			`SELECT security_level_id FROM company_membership
			  WHERE user_id = $1 AND company_id = $2`, userID, companyID)
		scanErr := row.Scan(&securityLevelID)
		if errors.Is(scanErr, pgx.ErrNoRows) {
			found = false
			return nil
		}
		if scanErr != nil {
			return scanErr
		}
		found = true
		return nil
	})
	if err != nil {
		return uuid.Nil, false, fmt.Errorf("authz: membership: %w", err)
	}
	return securityLevelID, found, nil
}

// Level reads a security_level by id under the COMPANY GUC. The caller must have
// set the company in ctx via db.WithCompany (the middleware does this only AFTER
// Membership confirms the caller belongs to companyID).
func (s *Store) Level(ctx context.Context, companyID, levelID uuid.UUID) (Level, error) {
	ctx = db.WithCompany(ctx, companyID)
	var lv Level
	var cutoff *time.Time
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, name, is_owner, default_access, requires_webauthn, backdated_cutoff
			   FROM security_level WHERE id = $1`, levelID).
			Scan(&lv.ID, &lv.Name, &lv.IsOwner, &lv.DefaultAccess, &lv.RequiresWebAuthn, &cutoff)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Level{}, fmt.Errorf("authz: level %s: %w", levelID, ErrNotFound)
		}
		return Level{}, fmt.Errorf("authz: level: %w", err)
	}
	lv.BackdatedCutoff = cutoff
	return lv, nil
}

// SeedCompanyAuthDefaults invokes the 0011 seed helper to create the Owner +
// Data Entry security levels for companyID and make userID an Owner member. The
// security_level/acl_rule inserts need the COMPANY GUC and the company_membership
// insert needs the USER GUC, so this tx sets BOTH (company via RunInTx, user via
// the additional set_config inside the tx). Idempotent (the SQL function uses ON
// CONFLICT DO NOTHING).
func (s *Store) SeedCompanyAuthDefaults(ctx context.Context, companyID, userID uuid.UUID) error {
	ctx = db.WithCompany(ctx, companyID)
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// company GUC is set by RunInTx; also set the user GUC so the
		// company_membership WITH CHECK (user_id = app_current_user_id()) passes.
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", userID.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `SELECT seed_company_auth_defaults($1, $2)`, companyID, userID)
		return err
	})
	if err != nil {
		return fmt.Errorf("authz: seed company auth defaults: %w", err)
	}
	return nil
}

// Rules reads all acl_rule rows for a security level under the COMPANY GUC.
func (s *Store) Rules(ctx context.Context, companyID, levelID uuid.UUID) ([]Rule, error) {
	ctx = db.WithCompany(ctx, companyID)
	var rules []Rule
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, qerr := tx.Query(ctx,
			`SELECT effect, action, resource FROM acl_rule WHERE security_level_id = $1`, levelID)
		if qerr != nil {
			return qerr
		}
		defer rows.Close()
		for rows.Next() {
			var r Rule
			if scanErr := rows.Scan(&r.Effect, &r.Action, &r.Resource); scanErr != nil {
				return scanErr
			}
			rules = append(rules, r)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("authz: rules: %w", err)
	}
	return rules, nil
}
