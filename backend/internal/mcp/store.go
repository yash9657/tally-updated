package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/policy"
)

// store.go: migration 0105's two tables, always under the company scope (FORCE
// RLS), so a grant or session of another company is not "refused" — it is
// simply not there.

// Grant is an agent_mcp_grant row.
type Grant struct {
	ID             uuid.UUID  `json:"id"`
	CompanyID      uuid.UUID  `json:"-"`
	UserID         uuid.UUID  `json:"user_id"`
	ClientName     string     `json:"client_name"`
	ACLKeys        []string   `json:"acl_keys"`
	MaxAmountPaise *int64     `json:"max_amount_paise,omitempty"`
	StepUp         bool       `json:"step_up"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	RevokedAt      *time.Time `json:"revoked_at,omitempty"`
	RevokedBy      *uuid.UUID `json:"revoked_by,omitempty"`
}

// AgentID is the agent identity a grant's runs carry.
func (g Grant) AgentID() string { return "mcp:" + g.ID.String() }

// Principal is the policy principal the grant's runs act as.
func (g Grant) Principal() policy.Principal {
	return policy.Principal{AgentID: g.AgentID(), OnBehalfOf: g.UserID, CompanyID: g.CompanyID,
		Delegation: policy.Delegation{ACLKeys: g.ACLKeys, MaxAmountPaise: g.MaxAmountPaise, ExpiresAt: g.ExpiresAt, StepUp: g.StepUp}}
}

// Usable reports why a grant may not be used now, or "" if it may.
func (g Grant) Usable(now time.Time) string {
	switch {
	case g.RevokedAt != nil:
		return "this MCP grant has been revoked"
	case !now.Before(g.ExpiresAt):
		return "this MCP grant has expired"
	}
	return ""
}

// Errors the HTTP layer maps.
var (
	ErrNotFound = errors.New("mcp: not found")
	ErrInvalid  = errors.New("mcp: invalid request")
)

// MaxGrantLifetime bounds a standing grant. ⚠️ A CHOICE, NOT A DERIVATION: a
// grant is a standing delegation to software on somebody's laptop, and "for
// ever" is not a thing anyone should have to remember to revoke.
const MaxGrantLifetime = 90 * 24 * time.Hour

// NewGrant is what a human asks for.
type NewGrant struct {
	CompanyID      uuid.UUID
	UserID         uuid.UUID
	ClientName     string
	ACLKeys        []string
	MaxAmountPaise *int64
	ExpiresAt      time.Time
	StepUp         bool
}

// ValidateKeys is the delegation check at the door: every key must be one some
// action evaluates to, and at least one of its actions must be something an
// agent may ever do — so "delete voucher" is refused when the grant is made,
// not discovered at commit time. It mirrors agent.Start's own check, which
// runs again on every session's run.
func ValidateKeys(keys []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, k := range keys {
		k = strings.TrimSpace(k)
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		specs := actions.ByACLKey(k)
		if len(specs) == 0 {
			return nil, fmt.Errorf("%w: %q is not an ACL key any action evaluates to", ErrInvalid, k)
		}
		ok := false
		for _, s := range specs {
			if policy.Structural(s) == nil {
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("%w: %s cannot be delegated to an agent: %v", ErrInvalid, k, policy.Structural(specs[0]))
		}
		out = append(out, k)
	}
	return out, nil
}

func (s *Server) createGrant(ctx context.Context, in NewGrant) (Grant, error) {
	now := s.now()
	name := strings.TrimSpace(in.ClientName)
	switch {
	case name == "" || len(name) > 100:
		return Grant{}, fmt.Errorf("%w: client_name must be 1 to 100 characters", ErrInvalid)
	case !now.Before(in.ExpiresAt):
		return Grant{}, fmt.Errorf("%w: expires_at must be in the future", ErrInvalid)
	case in.ExpiresAt.Sub(now) > MaxGrantLifetime:
		return Grant{}, fmt.Errorf("%w: a grant may last at most %d days", ErrInvalid, int(MaxGrantLifetime/(24*time.Hour)))
	case in.MaxAmountPaise != nil && *in.MaxAmountPaise <= 0:
		return Grant{}, fmt.Errorf("%w: max_amount_paise must be positive", ErrInvalid)
	case len(in.ACLKeys) > 64:
		return Grant{}, fmt.Errorf("%w: at most 64 delegated keys", ErrInvalid)
	}
	keys, err := ValidateKeys(in.ACLKeys)
	if err != nil {
		return Grant{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Grant{}, err
	}
	g := Grant{ID: id, CompanyID: in.CompanyID, UserID: in.UserID, ClientName: name, ACLKeys: keys,
		MaxAmountPaise: in.MaxAmountPaise, StepUp: in.StepUp, ExpiresAt: in.ExpiresAt.UTC(), CreatedAt: now}
	err = s.Pool.RunInTx(db.WithCompany(ctx, in.CompanyID), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO agent_mcp_grant (id, company_id, user_id, client_name, acl_keys, max_amount_paise, step_up, expires_at, created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			g.ID, g.CompanyID, g.UserID, g.ClientName, g.ACLKeys, g.MaxAmountPaise, g.StepUp, g.ExpiresAt, g.CreatedAt)
		return err
	})
	if err != nil {
		return Grant{}, fmt.Errorf("mcp: create grant: %w", err)
	}
	return g, nil
}

const grantColumns = `id, user_id, client_name, acl_keys, max_amount_paise, step_up, expires_at, created_at, revoked_at, revoked_by`

func scanGrant(row pgx.Row, companyID uuid.UUID) (Grant, error) {
	var g Grant
	err := row.Scan(&g.ID, &g.UserID, &g.ClientName, &g.ACLKeys, &g.MaxAmountPaise, &g.StepUp, &g.ExpiresAt,
		&g.CreatedAt, &g.RevokedAt, &g.RevokedBy)
	g.CompanyID = companyID
	if g.ACLKeys == nil {
		g.ACLKeys = []string{}
	}
	return g, err
}

func loadGrant(ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID) (Grant, error) {
	g, err := scanGrant(tx.QueryRow(ctx, `SELECT `+grantColumns+` FROM agent_mcp_grant WHERE id = $1`, id), companyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Grant{}, ErrNotFound
	}
	return g, err
}

func (s *Server) getGrant(ctx context.Context, companyID, id uuid.UUID) (Grant, error) {
	var g Grant
	err := s.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		var err error
		g, err = loadGrant(ctx, tx, companyID, id)
		return err
	})
	return g, err
}

func (s *Server) listGrants(ctx context.Context, companyID uuid.UUID) ([]Grant, error) {
	out := []Grant{}
	err := s.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT `+grantColumns+` FROM agent_mcp_grant ORDER BY created_at DESC, id DESC LIMIT 200`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			g, err := scanGrant(rows, companyID)
			if err != nil {
				return err
			}
			out = append(out, g)
		}
		return rows.Err()
	})
	return out, err
}

// Session is an agent_mcp_session row.
type Session struct {
	ID              uuid.UUID
	GrantID         uuid.UUID
	RunID           uuid.UUID
	ProtocolVersion string
	ManifestHash    string
	ClientName      string
	ClientVersion   string
	CreatedAt       time.Time
	InitializedAt   *time.Time
	LastSeenAt      time.Time
	ClosedAt        *time.Time
}

func insertSession(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, se Session) error {
	_, err := tx.Exec(ctx,
		`INSERT INTO agent_mcp_session (id, company_id, grant_id, run_id, protocol_version, manifest_hash,
		                                client_name, client_version, created_at, last_seen_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`,
		se.ID, companyID, se.GrantID, se.RunID, se.ProtocolVersion, se.ManifestHash, se.ClientName, se.ClientVersion, se.CreatedAt)
	return err
}

func (s *Server) getSession(ctx context.Context, companyID, id uuid.UUID) (Session, error) {
	var se Session
	err := s.Pool.RunInReadOnlyTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		err := tx.QueryRow(ctx,
			`SELECT id, grant_id, run_id, protocol_version, manifest_hash, client_name, client_version,
			        created_at, initialized_at, last_seen_at, closed_at
			   FROM agent_mcp_session WHERE id = $1`, id).Scan(
			&se.ID, &se.GrantID, &se.RunID, &se.ProtocolVersion, &se.ManifestHash, &se.ClientName, &se.ClientVersion,
			&se.CreatedAt, &se.InitializedAt, &se.LastSeenAt, &se.ClosedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	})
	return se, err
}

// touchSession records activity; initialized marks notifications/initialized;
// closed closes it. Each is one narrow UPDATE the 0105 trigger permits.
func (s *Server) touchSession(ctx context.Context, companyID, id uuid.UUID, initialized, closed bool) error {
	now := s.now()
	return s.Pool.RunInTx(db.WithCompany(ctx, companyID), func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`UPDATE agent_mcp_session
			    SET last_seen_at = $2,
			        initialized_at = CASE WHEN $3 THEN COALESCE(initialized_at, $2) ELSE initialized_at END,
			        closed_at = CASE WHEN $4 THEN $2 ELSE closed_at END
			  WHERE id = $1 AND closed_at IS NULL`, id, now, initialized, closed)
		return err
	})
}
