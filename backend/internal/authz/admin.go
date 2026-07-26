package authz

// admin.go is the WRITE side of the auth catalog: the store behind the SaaS
// admin API (members, invites and custom security levels). store.go stays the
// read path the middleware takes on every request; this file is only reached by
// the PDP-gated /members and /security-levels routes.
//
// GUC DISCIPLINE (the thing to get right):
//
//   - Everything company-scoped runs in RunInTx, which binds
//     app.current_company_id. After migration 0020 that GUC is what makes a
//     company's memberships, invites and co-member app_user rows visible AT ALL
//     — and it is set only after the middleware has verified the caller is a
//     member of that company.
//   - The invite-CLAIM path (PendingInvitesForSubject / AcceptInvite) is the one
//     place a user acts before any membership exists, so it runs under the
//     subject GUC, and AcceptInvite additionally binds the user GUC (the
//     membership INSERT names the accepting user) and the company GUC of the
//     invite it just read.
//   - Every guard that protects an invariant (last Owner, level in use, unique
//     name) is evaluated INSIDE the same transaction as the write it guards, so
//     two concurrent admins cannot both pass the check.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/khata/cored/internal/platform/db"
)

// Admin-path sentinel errors. The HTTP layer maps each to a status code.
var (
	// ErrConflict: the write would duplicate something that must be unique (a
	// level name, a membership).
	ErrConflict = errors.New("authz: conflict")
	// ErrLastOwner: the write would leave the company with no Owner-level member.
	ErrLastOwner = errors.New("authz: company must retain at least one owner")
	// ErrLevelInUse: the level still has members or pending invites.
	ErrLevelInUse = errors.New("authz: security level is in use")
	// ErrOwnerLevelImmutable: the built-in Owner level cannot be renamed,
	// re-scoped or deleted (its back-dated cutoff CAN be set).
	ErrOwnerLevelImmutable = errors.New("authz: the Owner level is immutable except for its back-dated cutoff")
)

// Member is one row of a company's member list.
type Member struct {
	UserID      uuid.UUID
	Subject     string
	Email       string
	DisplayName string
	LevelID     uuid.UUID
	LevelName   string
	IsOwner     bool
	CreatedAt   time.Time
}

// Invite is a pending (or historic) grant addressed to a JWT subject.
type Invite struct {
	ID          uuid.UUID
	CompanyID   uuid.UUID
	Subject     string
	Email       string
	DisplayName string
	LevelID     uuid.UUID
	LevelName   string
	Status      string
	CreatedAt   time.Time
	AcceptedAt  *time.Time
}

// RuleRow is an acl_rule with its id (Rule alone is what the PDP evaluates).
type RuleRow struct {
	ID       uuid.UUID
	Effect   Effect
	Action   Action
	Resource Resource
}

// LevelDetail is a security level plus everything the admin UI shows about it.
type LevelDetail struct {
	Level
	MemberCount int
	Rules       []RuleRow
}

// LevelSpec is a new custom security level. is_owner is deliberately absent: the
// Owner level is created by seed_company_auth_defaults and can never be minted
// through the API (a second is_owner level would be a silent full-access grant).
type LevelSpec struct {
	Name             string
	DefaultAccess    string
	RequiresWebAuthn bool
	BackdatedCutoff  *time.Time
	Rules            []Rule
}

// LevelPatch is a partial update. Nil fields are left untouched; Rules non-nil
// REPLACES the level's whole rule set (an empty non-nil slice clears it).
type LevelPatch struct {
	Name             *string
	DefaultAccess    *string
	RequiresWebAuthn *bool
	BackdatedCutoff  *time.Time
	ClearCutoff      bool
	Rules            *[]Rule
}

// Membership is one of a user's company memberships, resolved for /v1/me.
type Membership struct {
	CompanyID   uuid.UUID
	CompanyName string
	LevelID     uuid.UUID
	LevelName   string
	IsOwner     bool
}

// isUniqueViolation reports whether err is a Postgres unique-constraint error.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// ---------------------------------------------------------------------------
// Members
// ---------------------------------------------------------------------------

// ListMembers returns every member of companyID with their security level. The
// app_user join is what migration 0020's widened USING clause makes possible;
// before it, an Owner listing their own company saw only themselves.
func (s *Store) ListMembers(ctx context.Context, companyID uuid.UUID) ([]Member, error) {
	ctx = db.WithCompany(ctx, companyID)
	out := []Member{}
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT m.user_id, u.subject, u.email, u.display_name,
			        m.security_level_id, sl.name, sl.is_owner, m.created_at
			   FROM company_membership m
			   JOIN app_user u       ON u.id = m.user_id
			   JOIN security_level sl ON sl.id = m.security_level_id
			  WHERE m.company_id = $1
			  ORDER BY sl.is_owner DESC, u.subject`, companyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var m Member
			if err := rows.Scan(&m.UserID, &m.Subject, &m.Email, &m.DisplayName,
				&m.LevelID, &m.LevelName, &m.IsOwner, &m.CreatedAt); err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("authz: list members: %w", err)
	}
	return out, nil
}

// MemberBySubject resolves a member of companyID by their JWT subject. Only
// members are resolvable: app_user's RLS shows the caller their own row plus
// co-members of the company in scope, so this can NEVER be used to probe the
// global user directory for a subject that is not already a member here.
func (s *Store) MemberBySubject(ctx context.Context, companyID uuid.UUID, subject string) (Member, bool, error) {
	members, err := s.ListMembers(ctx, companyID)
	if err != nil {
		return Member{}, false, err
	}
	for _, m := range members {
		if m.Subject == subject {
			return m, true, nil
		}
	}
	return Member{}, false, nil
}

// ownerCountTx counts Owner-level members of companyID inside an open tx.
func ownerCountTx(ctx context.Context, tx pgx.Tx, companyID uuid.UUID) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		`SELECT count(*) FROM company_membership m
		   JOIN security_level sl ON sl.id = m.security_level_id
		  WHERE m.company_id = $1 AND sl.is_owner`, companyID).Scan(&n)
	return n, err
}

// SetMembershipLevel moves an existing member to a different security level.
// Guarded, in-transaction, against demoting the LAST Owner — otherwise a company
// can be left with nobody able to administer it (and, because the admin routes
// are themselves PDP-gated, permanently so).
func (s *Store) SetMembershipLevel(ctx context.Context, companyID, userID, levelID uuid.UUID) error {
	ctx = db.WithCompany(ctx, companyID)
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// The target level must exist in THIS company (RLS already scopes the
		// read, so a level id from another tenant simply is not found).
		var newIsOwner bool
		if err := tx.QueryRow(ctx,
			`SELECT is_owner FROM security_level WHERE id = $1 AND company_id = $2`,
			levelID, companyID).Scan(&newIsOwner); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("security level %s: %w", levelID, ErrNotFound)
			}
			return err
		}

		var curIsOwner bool
		if err := tx.QueryRow(ctx,
			`SELECT sl.is_owner
			   FROM company_membership m
			   JOIN security_level sl ON sl.id = m.security_level_id
			  WHERE m.user_id = $1 AND m.company_id = $2
			  FOR UPDATE OF m`, userID, companyID).Scan(&curIsOwner); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("membership for user %s: %w", userID, ErrNotFound)
			}
			return err
		}

		if curIsOwner && !newIsOwner {
			n, err := ownerCountTx(ctx, tx, companyID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastOwner
			}
		}

		_, err := tx.Exec(ctx,
			`UPDATE company_membership SET security_level_id = $1
			  WHERE user_id = $2 AND company_id = $3`, levelID, userID, companyID)
		return err
	})
	if err != nil {
		return wrapAdmin("set membership level", err)
	}
	return nil
}

// RemoveMembership revokes a user's access to a company. Same last-Owner guard
// as SetMembershipLevel.
func (s *Store) RemoveMembership(ctx context.Context, companyID, userID uuid.UUID) error {
	ctx = db.WithCompany(ctx, companyID)
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var isOwner bool
		if err := tx.QueryRow(ctx,
			`SELECT sl.is_owner
			   FROM company_membership m
			   JOIN security_level sl ON sl.id = m.security_level_id
			  WHERE m.user_id = $1 AND m.company_id = $2
			  FOR UPDATE OF m`, userID, companyID).Scan(&isOwner); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("membership for user %s: %w", userID, ErrNotFound)
			}
			return err
		}
		if isOwner {
			n, err := ownerCountTx(ctx, tx, companyID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastOwner
			}
		}
		_, err := tx.Exec(ctx,
			`DELETE FROM company_membership WHERE user_id = $1 AND company_id = $2`,
			userID, companyID)
		return err
	})
	if err != nil {
		return wrapAdmin("remove membership", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Invites
// ---------------------------------------------------------------------------

// CreateInvite records a pending grant for a JWT subject. It returns the invite
// id. Re-inviting the same subject while an invite is still pending UPDATES that
// invite (so an administrator can correct the level) rather than creating a
// second one. The caller must have already established that the subject is not
// yet a member (MemberBySubject) — that check is an HTTP-level 409, since
// changing an existing member's level is PUT /members/{userID}.
func (s *Store) CreateInvite(ctx context.Context, companyID uuid.UUID, in Invite, invitedBy uuid.UUID) (uuid.UUID, error) {
	ctx = db.WithCompany(ctx, companyID)
	id := uuid.New()
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Resolve the level's NAME as well as its existence: the invite carries
		// the name so the audit trail survives the level being deleted later.
		var levelName string
		if err := tx.QueryRow(ctx,
			`SELECT name FROM security_level WHERE id = $1 AND company_id = $2`,
			in.LevelID, companyID).Scan(&levelName); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("security level %s: %w", in.LevelID, ErrNotFound)
			}
			return err
		}
		return tx.QueryRow(ctx,
			`INSERT INTO company_invite
			     (id, company_id, subject, email, display_name,
			      security_level_id, security_level_name, invited_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			 ON CONFLICT (company_id, subject) WHERE status = 'pending'
			 DO UPDATE SET security_level_id   = EXCLUDED.security_level_id,
			               security_level_name = EXCLUDED.security_level_name,
			               email               = EXCLUDED.email,
			               display_name        = EXCLUDED.display_name
			 RETURNING id`,
			id, companyID, in.Subject, in.Email, in.DisplayName,
			in.LevelID, levelName, invitedBy).Scan(&id)
	})
	if err != nil {
		return uuid.Nil, wrapAdmin("create invite", err)
	}
	return id, nil
}

// ListInvites returns a company's invites, pending-only when onlyPending.
func (s *Store) ListInvites(ctx context.Context, companyID uuid.UUID, onlyPending bool) ([]Invite, error) {
	ctx = db.WithCompany(ctx, companyID)
	out := []Invite{}
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT id, company_id, subject, email, display_name,
			        security_level_id, security_level_name, status, created_at, accepted_at
			   FROM company_invite
			  WHERE company_id = $1 AND ($2 = false OR status = 'pending')
			  ORDER BY created_at DESC`, companyID, onlyPending)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var iv Invite
			var levelID *uuid.UUID // NULL once a historic invite's level is deleted
			if err := rows.Scan(&iv.ID, &iv.CompanyID, &iv.Subject, &iv.Email, &iv.DisplayName,
				&levelID, &iv.LevelName, &iv.Status, &iv.CreatedAt, &iv.AcceptedAt); err != nil {
				return err
			}
			if levelID != nil {
				iv.LevelID = *levelID
			}
			out = append(out, iv)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("authz: list invites: %w", err)
	}
	return out, nil
}

// RevokeInvite marks a pending invite revoked. Returns ErrNotFound if no PENDING
// invite with that id exists in the company (revoking twice is not an error the
// caller can distinguish from a bad id, which is why it is reported honestly
// rather than treated as idempotent success).
func (s *Store) RevokeInvite(ctx context.Context, companyID, inviteID uuid.UUID) error {
	ctx = db.WithCompany(ctx, companyID)
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		tag, err := tx.Exec(ctx,
			`UPDATE company_invite SET status = 'revoked'
			  WHERE id = $1 AND company_id = $2 AND status = 'pending'`, inviteID, companyID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("pending invite %s: %w", inviteID, ErrNotFound)
		}
		return nil
	})
	if err != nil {
		return wrapAdmin("revoke invite", err)
	}
	return nil
}

// PendingInvitesForSubject reads every pending invite addressed to subject,
// across all companies. It runs under the SUBJECT GUC with no company in scope —
// the one read migration 0020's `subject = app_current_subject()` clause exists
// for, since an invitee is by definition not yet a member anywhere.
func (s *Store) PendingInvitesForSubject(ctx context.Context, subject string) ([]Invite, error) {
	ctx = db.WithSubject(ctx, subject)
	out := []Invite{}
	err := s.pool.RunInAuthTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// NOTE: no join to security_level — that table is company-GUC scoped and
		// no company is in scope here, which is exactly why the invite carries a
		// copy of the level name. `security_level_id IS NOT NULL` skips an invite
		// whose level was deleted out from under it (the FK is ON DELETE SET
		// NULL so history survives), so a claim can never land on a dead level.
		rows, err := tx.Query(ctx,
			`SELECT id, company_id, subject, email, display_name,
			        security_level_id, security_level_name, status, created_at
			   FROM company_invite
			  WHERE subject = $1 AND status = 'pending' AND security_level_id IS NOT NULL
			  ORDER BY created_at`, subject)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var iv Invite
			if err := rows.Scan(&iv.ID, &iv.CompanyID, &iv.Subject, &iv.Email,
				&iv.DisplayName, &iv.LevelID, &iv.LevelName, &iv.Status, &iv.CreatedAt); err != nil {
				return err
			}
			out = append(out, iv)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("authz: pending invites: %w", err)
	}
	return out, nil
}

// AcceptInvite materialises a pending invite into a membership, in ONE
// transaction that binds all three GUCs the writes need:
//
//	company — company_invite's WITH CHECK and company_membership's WITH CHECK
//	user    — so the membership row names the accepting user
//	subject — so the invite row stays visible while it is being claimed
//
// The invite is re-read FOR UPDATE with `subject = $subject AND status =
// 'pending'`, so a revoked or already-claimed invite loses the race and returns
// ErrNotFound: acceptance is exactly-once even under concurrent calls.
func (s *Store) AcceptInvite(ctx context.Context, inviteID, companyID, userID uuid.UUID, subject string) error {
	ctx = db.WithCompany(ctx, companyID)
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_user_id', $1, true)", userID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_subject', $1, true)", subject); err != nil {
			return err
		}
		var levelID uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT security_level_id FROM company_invite
			  WHERE id = $1 AND company_id = $2 AND subject = $3 AND status = 'pending'
			    AND security_level_id IS NOT NULL
			  FOR UPDATE`, inviteID, companyID, subject).Scan(&levelID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("pending invite %s: %w", inviteID, ErrNotFound)
			}
			return err
		}
		// An existing membership WINS over the invite: accepting must never
		// silently downgrade (or upgrade) access an administrator set directly.
		if _, err := tx.Exec(ctx,
			`INSERT INTO company_membership (user_id, company_id, security_level_id)
			 VALUES ($1, $2, $3)
			 ON CONFLICT (user_id, company_id) DO NOTHING`,
			userID, companyID, levelID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`UPDATE company_invite SET status = 'accepted', accepted_at = now()
			  WHERE id = $1`, inviteID)
		return err
	})
	if err != nil {
		return wrapAdmin("accept invite", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Security levels
// ---------------------------------------------------------------------------

// ListLevels returns every security level of a company with its rules and its
// member count (the count is what makes "can I delete this level?" answerable in
// the UI without a second round trip).
func (s *Store) ListLevels(ctx context.Context, companyID uuid.UUID) ([]LevelDetail, error) {
	ctx = db.WithCompany(ctx, companyID)
	out := []LevelDetail{}
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT sl.id, sl.name, sl.is_owner, sl.default_access,
			        sl.requires_webauthn, sl.backdated_cutoff,
			        (SELECT count(*) FROM company_membership m
			          WHERE m.security_level_id = sl.id AND m.company_id = sl.company_id)
			   FROM security_level sl
			  WHERE sl.company_id = $1
			  ORDER BY sl.is_owner DESC, sl.name`, companyID)
		if err != nil {
			return err
		}
		defer rows.Close()
		byID := map[uuid.UUID]int{}
		for rows.Next() {
			var d LevelDetail
			var cutoff *time.Time
			if err := rows.Scan(&d.ID, &d.Name, &d.IsOwner, &d.DefaultAccess,
				&d.RequiresWebAuthn, &cutoff, &d.MemberCount); err != nil {
				return err
			}
			d.BackdatedCutoff = cutoff
			d.Rules = []RuleRow{}
			byID[d.ID] = len(out)
			out = append(out, d)
		}
		if err := rows.Err(); err != nil {
			return err
		}

		rrows, err := tx.Query(ctx,
			`SELECT id, security_level_id, effect, action, resource
			   FROM acl_rule WHERE company_id = $1
			  ORDER BY resource, action`, companyID)
		if err != nil {
			return err
		}
		defer rrows.Close()
		for rrows.Next() {
			var levelID uuid.UUID
			var rr RuleRow
			if err := rrows.Scan(&rr.ID, &levelID, &rr.Effect, &rr.Action, &rr.Resource); err != nil {
				return err
			}
			if idx, ok := byID[levelID]; ok {
				out[idx].Rules = append(out[idx].Rules, rr)
			}
		}
		return rrows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("authz: list levels: %w", err)
	}
	return out, nil
}

// LevelByName resolves a level by its (company-unique) name. It is what lets an
// admin client say "Data Entry" instead of carrying uuids around.
func (s *Store) LevelByName(ctx context.Context, companyID uuid.UUID, name string) (Level, error) {
	ctx = db.WithCompany(ctx, companyID)
	var lv Level
	var cutoff *time.Time
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT id, name, is_owner, default_access, requires_webauthn, backdated_cutoff
			   FROM security_level WHERE company_id = $1 AND name = $2`, companyID, name).
			Scan(&lv.ID, &lv.Name, &lv.IsOwner, &lv.DefaultAccess, &lv.RequiresWebAuthn, &cutoff)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Level{}, fmt.Errorf("authz: security level %q: %w", name, ErrNotFound)
		}
		return Level{}, fmt.Errorf("authz: level by name: %w", err)
	}
	lv.BackdatedCutoff = cutoff
	return lv, nil
}

// CreateLevel creates a custom security level and its rules. The rules must
// already have been validated against the closed vocabulary (ValidateRule) —
// the store enforces the DB constraints, the caller enforces the vocabulary.
func (s *Store) CreateLevel(ctx context.Context, companyID uuid.UUID, spec LevelSpec) (uuid.UUID, error) {
	ctx = db.WithCompany(ctx, companyID)
	id := uuid.New()
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx,
			`INSERT INTO security_level
			     (id, company_id, name, is_owner, default_access, requires_webauthn, backdated_cutoff)
			 VALUES ($1, $2, $3, false, $4, $5, $6)`,
			id, companyID, spec.Name, spec.DefaultAccess, spec.RequiresWebAuthn, spec.BackdatedCutoff); err != nil {
			if isUniqueViolation(err) {
				return fmt.Errorf("security level %q already exists: %w", spec.Name, ErrConflict)
			}
			return err
		}
		return insertRulesTx(ctx, tx, companyID, id, spec.Rules)
	})
	if err != nil {
		return uuid.Nil, wrapAdmin("create level", err)
	}
	return id, nil
}

func insertRulesTx(ctx context.Context, tx pgx.Tx, companyID, levelID uuid.UUID, rules []Rule) error {
	for _, r := range rules {
		if _, err := tx.Exec(ctx,
			`INSERT INTO acl_rule (id, company_id, security_level_id, effect, action, resource)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			uuid.New(), companyID, levelID, string(r.Effect), string(r.Action), string(r.Resource)); err != nil {
			return err
		}
	}
	return nil
}

// UpdateLevel applies a partial update. The built-in Owner level accepts ONLY a
// back-dated-cutoff change (ErrOwnerLevelImmutable otherwise): renaming it,
// flipping its default_access, or turning off its WebAuthn step-up would each
// silently rewrite the security posture every other check assumes. Passing
// Rules replaces the level's entire rule set inside the same transaction, so a
// concurrent request never observes a half-applied policy.
func (s *Store) UpdateLevel(ctx context.Context, companyID, levelID uuid.UUID, patch LevelPatch) error {
	ctx = db.WithCompany(ctx, companyID)
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var isOwner bool
		if err := tx.QueryRow(ctx,
			`SELECT is_owner FROM security_level WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			levelID, companyID).Scan(&isOwner); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("security level %s: %w", levelID, ErrNotFound)
			}
			return err
		}
		if isOwner && (patch.Name != nil || patch.DefaultAccess != nil ||
			patch.RequiresWebAuthn != nil || patch.Rules != nil) {
			return ErrOwnerLevelImmutable
		}

		if patch.Name != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE security_level SET name = $1 WHERE id = $2`, *patch.Name, levelID); err != nil {
				if isUniqueViolation(err) {
					return fmt.Errorf("security level %q already exists: %w", *patch.Name, ErrConflict)
				}
				return err
			}
		}
		if patch.DefaultAccess != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE security_level SET default_access = $1 WHERE id = $2`,
				*patch.DefaultAccess, levelID); err != nil {
				return err
			}
		}
		if patch.RequiresWebAuthn != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE security_level SET requires_webauthn = $1 WHERE id = $2`,
				*patch.RequiresWebAuthn, levelID); err != nil {
				return err
			}
		}
		if patch.ClearCutoff {
			if _, err := tx.Exec(ctx,
				`UPDATE security_level SET backdated_cutoff = NULL WHERE id = $1`, levelID); err != nil {
				return err
			}
		} else if patch.BackdatedCutoff != nil {
			if _, err := tx.Exec(ctx,
				`UPDATE security_level SET backdated_cutoff = $1 WHERE id = $2`,
				*patch.BackdatedCutoff, levelID); err != nil {
				return err
			}
		}
		if patch.Rules != nil {
			if _, err := tx.Exec(ctx,
				`DELETE FROM acl_rule WHERE security_level_id = $1 AND company_id = $2`,
				levelID, companyID); err != nil {
				return err
			}
			if err := insertRulesTx(ctx, tx, companyID, levelID, *patch.Rules); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return wrapAdmin("update level", err)
	}
	return nil
}

// DeleteLevel removes a custom level and its rules. Refused for the Owner level,
// and refused while any member or pending invite still references it — a deleted
// level would otherwise leave a membership pointing at nothing (the FK would
// reject it anyway; this turns a 500 into an explanatory 409).
func (s *Store) DeleteLevel(ctx context.Context, companyID, levelID uuid.UUID) error {
	ctx = db.WithCompany(ctx, companyID)
	err := s.pool.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var isOwner bool
		if err := tx.QueryRow(ctx,
			`SELECT is_owner FROM security_level WHERE id = $1 AND company_id = $2 FOR UPDATE`,
			levelID, companyID).Scan(&isOwner); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("security level %s: %w", levelID, ErrNotFound)
			}
			return err
		}
		if isOwner {
			return ErrOwnerLevelImmutable
		}
		var inUse int
		if err := tx.QueryRow(ctx,
			`SELECT (SELECT count(*) FROM company_membership
			          WHERE security_level_id = $1 AND company_id = $2)
			      + (SELECT count(*) FROM company_invite
			          WHERE security_level_id = $1 AND company_id = $2 AND status = 'pending')`,
			levelID, companyID).Scan(&inUse); err != nil {
			return err
		}
		if inUse > 0 {
			return fmt.Errorf("%d member(s)/invite(s) still use it: %w", inUse, ErrLevelInUse)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM acl_rule WHERE security_level_id = $1 AND company_id = $2`,
			levelID, companyID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`DELETE FROM security_level WHERE id = $1 AND company_id = $2`, levelID, companyID)
		return err
	})
	if err != nil {
		return wrapAdmin("delete level", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// /v1/me — the identity + company list a client needs to switch companies
// ---------------------------------------------------------------------------

// UserCompanies returns every company the user is a member of, with the level
// they hold there. The membership ids come from ONE user-GUC read; the company
// and level NAMES are then resolved per company under that company's GUC,
// because `company` and `security_level` are company-scoped by RLS and no single
// transaction may hold two company GUCs. The loop is bounded by the user's own
// membership count.
func (s *Store) UserCompanies(ctx context.Context, userID uuid.UUID) ([]Membership, error) {
	type pair struct {
		companyID uuid.UUID
		levelID   uuid.UUID
	}
	var pairs []pair
	authCtx := db.WithUser(ctx, userID)
	if err := s.pool.RunInAuthTx(authCtx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx,
			`SELECT company_id, security_level_id FROM company_membership
			  WHERE user_id = $1 ORDER BY created_at`, userID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var p pair
			if err := rows.Scan(&p.companyID, &p.levelID); err != nil {
				return err
			}
			pairs = append(pairs, p)
		}
		return rows.Err()
	}); err != nil {
		return nil, fmt.Errorf("authz: user companies: %w", err)
	}

	out := []Membership{}
	for _, p := range pairs {
		m := Membership{CompanyID: p.companyID, LevelID: p.levelID}
		cctx := db.WithCompany(ctx, p.companyID)
		err := s.pool.RunInTx(cctx, func(ctx context.Context, tx pgx.Tx) error {
			if err := tx.QueryRow(ctx,
				`SELECT name FROM company WHERE id = $1`, p.companyID).Scan(&m.CompanyName); err != nil {
				return err
			}
			return tx.QueryRow(ctx,
				`SELECT name, is_owner FROM security_level WHERE id = $1`, p.levelID).
				Scan(&m.LevelName, &m.IsOwner)
		})
		if err != nil {
			return nil, fmt.Errorf("authz: user companies (resolve %s): %w", p.companyID, err)
		}
		out = append(out, m)
	}
	return out, nil
}

// wrapAdmin annotates an admin-path error with its operation while keeping the
// sentinel errors (ErrNotFound, ErrConflict, ErrLastOwner, ErrLevelInUse,
// ErrOwnerLevelImmutable) matchable with errors.Is by the HTTP layer.
func wrapAdmin(op string, err error) error {
	return fmt.Errorf("authz: %s: %w", op, err)
}
