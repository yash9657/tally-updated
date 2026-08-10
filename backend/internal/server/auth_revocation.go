package server

// auth_revocation.go — CREDENTIAL REVOCATION ON THE AUTHENTICATED PATH (D56).
//
// ==========================================================================
// THE DEFECT
// ==========================================================================
// Locking an account did not revoke the sessions it already held. `IsLocked`
// was consulted on exactly ONE code path — POST /auth/login (auth_login.go step
// (b)) — so locking somebody stopped them signing in AGAIN and did precisely
// nothing about the bearer already in their hand. Driven: a subject was locked,
// and its existing token then POSTed a voucher and got 201, and went on working
// for the remainder of the 3600-second `loginTokenTTLSeconds`.
//
// That is not a missing check on one route. It is a missing NOTION: the product
// had no concept of a credential whose sessions are revoked, only of a login
// that is refused.
//
// ==========================================================================
// THE PROPERTY, AND WHY IT IS ASSERTED HERE AND NOWHERE ELSE
// ==========================================================================
//
//	A LOCKED SUBJECT CAN PERFORM NO AUTHENTICATED REQUEST — read, write,
//	step-up, password change or company creation — AND THE SESSIONS A LOCK
//	KILLS STAY DEAD AFTER THE LOCK EXPIRES.
//
// Every authenticated route in this product passes through exactly one funnel:
// Server.authenticate, called by authnOnlyMiddleware (POST /v1/companies,
// GET /v1/me, POST /auth/password, all four /auth/webauthn/* routes) and by
// companyAuthMiddleware (the whole /v1/companies/{companyID} subtree). Putting
// the check THERE makes the property structural: a route mounted tomorrow
// inherits it without anybody remembering, which a list of per-handler checks
// could never promise. Rounds 1-4 of the back-dated cutoff are the standing
// lesson — every one of them fixed the door it was shown and the next re-drive
// found another.
//
// ==========================================================================
// THE DOORS, INCLUDING THE ONES THAT WERE ALREADY SAFE
// ==========================================================================
//
//	POST /auth/login                 ALREADY SAFE. Step (b) reads IsLocked
//	                                 before the password is touched. Unchanged.
//	an existing bearer, WRITE        CLOSED HERE. This was the driven defect.
//	an existing bearer, READ         CLOSED HERE. A locked account must not go
//	                                 on reading the books either; a report is an
//	                                 export of exactly the data the lock exists
//	                                 to protect.
//	POST /auth/webauthn/assert/*     CLOSED HERE, and it is the one that would
//	                                 have re-opened the whole defect: the assert
//	                                 ceremony MINTS A FRESH TOKEN (with `amr`,
//	                                 i.e. one that may WRITE). It runs under
//	                                 authnOnlyMiddleware, so the OLD token has
//	                                 to survive this check before any new one is
//	                                 signed — a revoked session cannot trade
//	                                 itself up for a stepped-up one.
//	POST /auth/webauthn/register/*   CLOSED HERE, same seam. A locked subject
//	                                 must not be able to enrol a NEW
//	                                 authenticator, or a compromised session
//	                                 would leave a permanent way back in.
//	POST /auth/password              CLOSED HERE, same seam. Note this is
//	                                 STRICTER than the route's own doc, which
//	                                 says being locked out of /auth/login should
//	                                 not stop an already-authenticated user
//	                                 changing their password: that argument was
//	                                 written when a lock could not revoke
//	                                 anything, and it amounts to letting a
//	                                 stolen session set a new password and keep
//	                                 the account. The way back in is the D55
//	                                 administrative unlock, not the session the
//	                                 lock was aimed at.
//	a REFRESH token                  NO SUCH THING EXISTS. Grepped: the only
//	                                 token minters in the tree are handleLogin,
//	                                 the WebAuthn assert, and the dev-gated
//	                                 POST /auth/token. There is no refresh
//	                                 route, no refresh claim and no rotation, so
//	                                 there is no fourth door to close.
//	POST /auth/token (dev)           DELIBERATELY NOT CLOSED, AND DISCLOSED. It
//	                                 is unauthenticated and mints a validly
//	                                 signed token for ANY subject, so it never
//	                                 passes through here at all. The
//	                                 currently-locked clause below still refuses
//	                                 the token it mints for as long as the lock
//	                                 lasts; after the lock expires such a token
//	                                 works, because /auth/token is a full
//	                                 impersonation oracle by construction and
//	                                 revocation cannot be stronger than the
//	                                 minter (auth_login.go's header disclosure,
//	                                 limit 2). It is gated on
//	                                 AUTH_DEV_TOKEN_ENABLED and must never be on
//	                                 outside a developer's own machine.
//	service principals (`svc:`)      NOT SUBJECT TO THIS, and correctly so: a
//	                                 service token has no app_user row, has
//	                                 never had a password and cannot be locked.
//	                                 Skipped before the query so the side plane
//	                                 pays nothing for a lock it cannot have.

import (
	"context"

	"github.com/khata/cored/internal/authn"
)

// sessionRevoked reports whether this VERIFIED token must be refused because
// the credential behind it has been revoked (the subject is locked now, or the
// token predates the revocation epoch a lock set).
//
// ⛔ THE ERROR IS NOT A `false`. A store that cannot answer "is this credential
// revoked?" must not be read as "it is not" — that is the fail-open this
// codebase has been burned by repeatedly (see the cutoff's beforeEveryCutoff
// sentinel). The caller turns an error into a refusal.
func (s *Server) sessionRevoked(ctx context.Context, claims *authn.Claims) (bool, error) {
	if claims == nil {
		// Structurally impossible (authenticate returns ok=false first), and
		// "impossible" is the case that must not fail open.
		return true, nil
	}
	// A service principal has no app_user row and no password, so there is
	// nothing that could ever have locked it. Skipping the query here is not a
	// hole, it is the absence of a lock to check — and it keeps statutoryd's
	// freeze hook and the sync/import seams off this read entirely.
	if claims.IsService() {
		return false, nil
	}
	// A Server assembled without a lockout store (unit fixtures, and any
	// deployment with no pool) cannot lock ANY account: SetLock has nowhere to
	// write, IsLocked has nothing to read, and POST /auth/login's own check is
	// equally inert. Revocation cannot be stronger than the lock it revokes
	// for, so with no lock possible there is nothing to revoke, and this is a
	// statement about that configuration rather than a fail-open in this one.
	if s.lockout == nil {
		return false, nil
	}
	return s.lockout.SessionRevoked(ctx, claims.Subject, claims.IssuedAt)
}
