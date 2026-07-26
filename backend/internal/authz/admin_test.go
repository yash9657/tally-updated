package authz_test

// admin_test.go — STORE-level tests for the admin catalog, covering the
// properties the HTTP tests structurally cannot reach:
//
//   - claiming an invite is guarded at the moment of the WRITE, not only by the
//     list that offered it (a revoke that lands between list and accept must
//     still win);
//   - an invite belongs to its addressee: knowing an invite id must not let a
//     different subject claim it;
//   - the new company_invite table is tenant-isolated by RLS, like every other
//     table this system holds.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/platform/db"
)

// dataEntryLevel resolves the Data Entry level seeded with every company.
func dataEntryLevel(t *testing.T, store *authz.Store, companyID uuid.UUID) uuid.UUID {
	t.Helper()
	lv, err := store.LevelByName(context.Background(), companyID, "Data Entry")
	if err != nil {
		t.Fatalf("resolve Data Entry level: %v", err)
	}
	return lv.ID
}

// TestAcceptInviteIsExactlyOnceAndStatusGuarded proves the accept path re-checks
// the invite's STATUS inside the same transaction that writes the membership.
// The HTTP flow filters pending invites before accepting, so this guard only
// ever fires on a race — which is exactly why it needs a direct test.
func TestAcceptInviteIsExactlyOnceAndStatusGuarded(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	companyID, _, _ := seedCompanyWithOwner(t, store, pool, "inv-owner-"+uuid.NewString())
	deLevel := dataEntryLevel(t, store, companyID)

	// (a) A REVOKED invite cannot be accepted, even with its id in hand.
	revokedSubject := "revoked-" + uuid.NewString()[:8]
	revokedID, err := store.CreateInvite(ctx, companyID,
		authz.Invite{Subject: revokedSubject, LevelID: deLevel}, uuid.Nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if err := store.RevokeInvite(ctx, companyID, revokedID); err != nil {
		t.Fatalf("revoke invite: %v", err)
	}
	revokedUser, err := store.CreateUserOnFirstLogin(ctx, revokedSubject, "", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.AcceptInvite(ctx, revokedID, companyID, revokedUser, revokedSubject); !errors.Is(err, authz.ErrNotFound) {
		t.Fatalf("accepting a REVOKED invite returned %v, want ErrNotFound — a revoke that "+
			"lands after the invite was listed must still deny the claim", err)
	}
	if _, found, err := store.Membership(ctx, revokedUser, companyID); err != nil || found {
		t.Fatalf("SECURITY: a revoked invite created a membership (found=%v err=%v)", found, err)
	}

	// (b) A pending invite is accepted exactly once.
	subject := "claimant-" + uuid.NewString()[:8]
	inviteID, err := store.CreateInvite(ctx, companyID,
		authz.Invite{Subject: subject, LevelID: deLevel}, uuid.Nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}
	userID, err := store.CreateUserOnFirstLogin(ctx, subject, "", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.AcceptInvite(ctx, inviteID, companyID, userID, subject); err != nil {
		t.Fatalf("accept invite: %v", err)
	}
	lvl, found, err := store.Membership(ctx, userID, companyID)
	if err != nil || !found || lvl != deLevel {
		t.Fatalf("membership after accept: level=%v found=%v err=%v, want the Data Entry level", lvl, found, err)
	}
	if err := store.AcceptInvite(ctx, inviteID, companyID, userID, subject); !errors.Is(err, authz.ErrNotFound) {
		t.Fatalf("second accept returned %v, want ErrNotFound (accept must be exactly-once)", err)
	}
}

// TestAcceptInviteRejectsTheWrongSubject proves an invite is bound to its
// ADDRESSEE. An attacker holding a valid token and a leaked invite id must not
// be able to claim someone else's grant.
func TestAcceptInviteRejectsTheWrongSubject(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	companyID, _, _ := seedCompanyWithOwner(t, store, pool, "bind-owner-"+uuid.NewString())
	deLevel := dataEntryLevel(t, store, companyID)

	invitee := "invitee-" + uuid.NewString()[:8]
	inviteID, err := store.CreateInvite(ctx, companyID,
		authz.Invite{Subject: invitee, LevelID: deLevel}, uuid.Nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	attacker := "attacker-" + uuid.NewString()[:8]
	attackerID, err := store.CreateUserOnFirstLogin(ctx, attacker, "", "")
	if err != nil {
		t.Fatalf("create attacker: %v", err)
	}
	if err := store.AcceptInvite(ctx, inviteID, companyID, attackerID, attacker); !errors.Is(err, authz.ErrNotFound) {
		t.Fatalf("SECURITY: a different subject claimed the invite (err=%v, want ErrNotFound)", err)
	}
	if _, found, err := store.Membership(ctx, attackerID, companyID); err != nil || found {
		t.Fatalf("SECURITY: the attacker holds a membership (found=%v err=%v)", found, err)
	}
	// The rightful addressee still can — the invite was not consumed.
	inviteeID, err := store.CreateUserOnFirstLogin(ctx, invitee, "", "")
	if err != nil {
		t.Fatalf("create invitee: %v", err)
	}
	if err := store.AcceptInvite(ctx, inviteID, companyID, inviteeID, invitee); err != nil {
		t.Fatalf("the addressee could not claim their own invite: %v", err)
	}
}

// TestDeleteLevelSurvivesHistoricInvites pins the audit/lifecycle interaction
// that a live run found the hard way: an ACCEPTED invite still references the
// level it granted, so a naive FK made "delete this custom level" fail with a
// constraint violation forever after the first person used it. The invite now
// carries the level NAME and the FK is ON DELETE SET NULL, so history survives a
// deletion and the deletion survives history. A PENDING invite still blocks it.
func TestDeleteLevelSurvivesHistoricInvites(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	companyID, _, _ := seedCompanyWithOwner(t, store, pool, "hist-owner-"+uuid.NewString())

	levelID, err := store.CreateLevel(ctx, companyID, authz.LevelSpec{
		Name: "Temp Auditor", DefaultAccess: "none",
		Rules: []authz.Rule{{Effect: authz.Allow, Action: authz.Display, Resource: "report"}},
	})
	if err != nil {
		t.Fatalf("create level: %v", err)
	}

	subject := "hist-" + uuid.NewString()[:8]
	inviteID, err := store.CreateInvite(ctx, companyID, authz.Invite{Subject: subject, LevelID: levelID}, uuid.Nil)
	if err != nil {
		t.Fatalf("create invite: %v", err)
	}

	// A PENDING invite blocks deletion (nobody should lose a grant in flight).
	if err := store.DeleteLevel(ctx, companyID, levelID); !errors.Is(err, authz.ErrLevelInUse) {
		t.Fatalf("delete with a pending invite returned %v, want ErrLevelInUse", err)
	}

	userID, err := store.CreateUserOnFirstLogin(ctx, subject, "", "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := store.AcceptInvite(ctx, inviteID, companyID, userID, subject); err != nil {
		t.Fatalf("accept: %v", err)
	}
	// A MEMBER blocks it too...
	if err := store.DeleteLevel(ctx, companyID, levelID); !errors.Is(err, authz.ErrLevelInUse) {
		t.Fatalf("delete with a member returned %v, want ErrLevelInUse", err)
	}
	if err := store.RemoveMembership(ctx, companyID, userID); err != nil {
		t.Fatalf("remove membership: %v", err)
	}
	// ...but the ACCEPTED invite left behind must not block it forever.
	if err := store.DeleteLevel(ctx, companyID, levelID); err != nil {
		t.Fatalf("delete an unused level with a historic invite: %v", err)
	}

	// The audit row survives, still naming the level that was granted.
	all, err := store.ListInvites(ctx, companyID, false)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	var found bool
	for _, iv := range all {
		if iv.ID == inviteID {
			found = true
			if iv.Status != "accepted" || iv.LevelName != "Temp Auditor" {
				t.Fatalf("historic invite = %+v, want status accepted at 'Temp Auditor'", iv)
			}
		}
	}
	if !found {
		t.Fatalf("deleting the level destroyed the invite audit trail: %+v", all)
	}
}

// TestInviteRLSIsTenantScoped is the isolation proof for the new table: an
// invite written in company A is invisible under company B's GUC, and visible to
// its addressee by SUBJECT even with no company in scope (the one read that
// clause exists for).
func TestInviteRLSIsTenantScoped(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	companyA, _, _ := seedCompanyWithOwner(t, store, pool, "rlsA-"+uuid.NewString())
	companyB, _, _ := seedCompanyWithOwner(t, store, pool, "rlsB-"+uuid.NewString())

	subject := "cross-" + uuid.NewString()[:8]
	if _, err := store.CreateInvite(ctx, companyA,
		authz.Invite{Subject: subject, LevelID: dataEntryLevel(t, store, companyA)}, uuid.Nil); err != nil {
		t.Fatalf("create invite in A: %v", err)
	}

	inA, err := store.ListInvites(ctx, companyA, true)
	if err != nil || len(inA) != 1 {
		t.Fatalf("company A sees %d invites (err=%v), want 1", len(inA), err)
	}
	inB, err := store.ListInvites(ctx, companyB, true)
	if err != nil {
		t.Fatalf("list invites in B: %v", err)
	}
	if len(inB) != 0 {
		t.Fatalf("SECURITY: company B sees %d of company A's invites: %+v", len(inB), inB)
	}

	// The addressee sees it by subject, with NO company in scope.
	mine, err := store.PendingInvitesForSubject(ctx, subject)
	if err != nil || len(mine) != 1 || mine[0].CompanyID != companyA {
		t.Fatalf("addressee's pending invites = %+v (err=%v), want exactly the one in %s", mine, err, companyA)
	}
	// ...and a DIFFERENT subject sees none of it.
	others, err := store.PendingInvitesForSubject(ctx, "someone-else-"+uuid.NewString()[:8])
	if err != nil {
		t.Fatalf("pending invites for another subject: %v", err)
	}
	if len(others) != 0 {
		t.Fatalf("SECURITY: an unrelated subject sees %d invites: %+v", len(others), others)
	}
}

// TestMembershipRLSStillDeniesTheWrongCompany guards migration 0020's widening
// of company_membership: the company GUC now grants READ access to that
// company's memberships, and this asserts the widening did not become a hole —
// company B's GUC still sees none of company A's rows, and the WRITE side
// refuses a membership row for a company other than the one in scope.
func TestMembershipRLSStillDeniesTheWrongCompany(t *testing.T) {
	store, pool := newStore(t)
	ctx := context.Background()
	companyA, userA, levelA := seedCompanyWithOwner(t, store, pool, "wideA-"+uuid.NewString())
	companyB, _, _ := seedCompanyWithOwner(t, store, pool, "wideB-"+uuid.NewString())

	if members, err := store.ListMembers(ctx, companyA); err != nil || len(members) != 1 {
		t.Fatalf("company A member list = %+v (err=%v), want exactly its owner", members, err)
	}
	membersB, err := store.ListMembers(ctx, companyB)
	if err != nil {
		t.Fatalf("list members B: %v", err)
	}
	for _, m := range membersB {
		if m.UserID == userA {
			t.Fatalf("SECURITY: company B's member list contains company A's owner: %+v", m)
		}
	}

	// WRITE side: an INSERT naming company A while company B is in scope is
	// refused by RLS (WITH CHECK is company-GUC-only after 0020).
	bctx := db.WithCompany(ctx, companyB)
	err = pool.RunInTx(bctx, func(ctx context.Context, tx pgx.Tx) error {
		_, execErr := tx.Exec(ctx,
			`INSERT INTO company_membership (user_id, company_id, security_level_id)
			 VALUES ($1, $2, $3)`, userA, companyA, levelA)
		return execErr
	})
	if err == nil {
		t.Fatal("SECURITY: wrote a company-A membership while scoped to company B")
	}
}
