package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/authz"
	// integration is imported for the ROUND-3 GUID peek, not for the import
	// itself: peekTallyXMLSupersededGUIDs must decide which <VOUCHER> supersedes
	// an existing one with the SAME parse and the SAME GUIDValue() the importer
	// uses, or the PDP and the writer could disagree about which voucher is about
	// to be altered or tombstoned. Same reason server.go imports it for the DATE
	// peek.
	"github.com/khata/cored/internal/integration"
	"github.com/khata/cored/internal/platform/db"
	// sync is imported for its Op CONSTANTS only, so the op filter in
	// peekSyncSupersededVoucherIDs cannot drift from the switch in
	// sync.Service.applyOne that routes those ops to AlterVoucher/DeleteVoucher.
	"github.com/khata/cored/internal/sync"
)

// auth_middleware.go is the server-side authn + PDP enforcement seam (Task D,
// WS1). It closes the unauthenticated seam: a verified JWT establishes the
// caller, membership in {companyID} is the tenant gate, and the per-(action,
// resource) PDP decides each request. Only after a request is authorized does
// the company GUC get injected into the context — the URL path param alone no
// longer drives db.WithCompany.

// authStore is the subset of authz.Store the middleware needs. Declared as an
// interface so tests can drive the middleware without a DB if needed.
type authStore interface {
	LookupUserBySubject(ctx context.Context, subject string) (userID uuid.UUID, active bool, found bool, err error)
	CreateUserOnFirstLogin(ctx context.Context, subject, email, displayName string) (uuid.UUID, error)
	Membership(ctx context.Context, userID, companyID uuid.UUID) (securityLevelID uuid.UUID, found bool, err error)
	Level(ctx context.Context, companyID, levelID uuid.UUID) (authz.Level, error)
	Rules(ctx context.Context, companyID, levelID uuid.UUID) ([]authz.Rule, error)
}

// bearerToken extracts the raw JWT from an Authorization: Bearer <jwt> header.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) <= len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(h[len(prefix):]), true
}

// stepUpRequired writes the distinguishable WebAuthn step-up response: a 403
// with code STEPUP_REQUIRED. 401 is RESERVED for missing/invalid/expired tokens,
// so a client (and the e2e scripts) can tell "no token" apart from "token valid
// but needs WebAuthn step-up".
func stepUpRequired(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{
		"error": authz.StepUpReason,
		"code":  "STEPUP_REQUIRED",
	})
}

// authenticate verifies the Bearer token and returns its claims, or writes a 401
// and returns ok=false. It is the shared front half of both the authn-only path
// (POST /v1/companies) and the full PDP path ({companyID} subtree).
func (s *Server) authenticate(w http.ResponseWriter, r *http.Request) (*authn.Claims, bool) {
	raw, present := bearerToken(r)
	if !present {
		writeError(w, http.StatusUnauthorized, "missing bearer token")
		return nil, false
	}
	claims, err := s.verifier.Verify(r.Context(), raw)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid token")
		return nil, false
	}
	// A VALID SIGNATURE IS NOT A LIVE SESSION (D56). Locking an account used to
	// revoke nothing it already held — the bearer in an attacker's hand kept
	// working for the full 3600-second TTL and posted writes that returned 201,
	// because `locked_until` was read on POST /auth/login and nowhere else.
	// EVERY authenticated route in the product funnels through this function,
	// which is why the check lives here and not in the handlers: see
	// auth_revocation.go for the property, the door-by-door enumeration
	// (including the safe ones) and the two clauses.
	//
	// 401, not 403: this is "your session is over", the same class as an expired
	// token, and it is what makes the browser's bearerFetch call onUnauthorized()
	// and return the operator to the sign-in screen instead of leaving the shell
	// rendering empty screens against a dead session.
	revoked, rerr := s.sessionRevoked(r.Context(), claims)
	if rerr != nil {
		// Could not find out. A revocation check that fails must refuse, never
		// admit — the whole file above it is one long argument for that.
		writeError(w, http.StatusUnauthorized, "session state unavailable")
		return nil, false
	}
	if revoked {
		writeError(w, http.StatusUnauthorized, "session revoked: this account was locked")
		return nil, false
	}
	return claims, true
}

// authnOnlyMiddleware guards POST /v1/companies: verify the JWT, set the subject
// in ctx, and resolve (create-on-first-login) the app_user. It SKIPS membership,
// the company GUC, and the PDP — the company does not exist yet, so there is no
// membership or security_level to read. handleCreateCompany keeps its own
// self-scoping and seeds the Owner level + membership afterward.
func (s *Server) authnOnlyMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := s.authenticate(w, r)
		if !ok {
			return
		}
		// Service principals cannot bootstrap companies.
		if claims.IsService() {
			writeError(w, http.StatusForbidden, "service principals cannot create companies")
			return
		}
		userID, err := s.authStore.CreateUserOnFirstLogin(r.Context(), claims.Subject, claims.Email, "")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "resolve user")
			return
		}
		ctx := db.WithSubject(r.Context(), claims.Subject)
		ctx = db.WithUser(ctx, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// companyAuthMiddleware guards the /v1/companies/{companyID} subtree. The pinned
// flow:
//
//  1. verify JWT (401 on missing/invalid) -> claims.
//  2. service principal? -> routeAction() must MAP the route and must not be a
//     Print; then full access scoped to {companyID}: set company GUC and call
//     next (cutoff + webauthn do not apply to svc).
//  3. set subject GUC -> LookupUserBySubject -> userID (401 if not found/inactive).
//  4. set user GUC -> Membership(user, pathCompanyID) -> securityLevelID (403).
//  5. set company GUC (db.WithCompany authorizedID) -> Level + Rules -> Evaluate.
//  6. deny -> 403 (or the distinguishable STEPUP_REQUIRED 403); allow -> inject
//     subject/user/company into ctx and call next.
func (s *Server) companyAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claims, ok := s.authenticate(w, r)
		if !ok {
			return
		}

		companyID, err := uuid.Parse(chi.URLParam(r, "companyID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid company id")
			return
		}

		// (2) SERVICE PRINCIPAL: synthetic full access scoped to the targeted
		// company. No membership/level read, no cutoff, no webauthn.
		//
		// WHY THE FAST PATH EXISTS. A "svc:" token is not a person and has no
		// app_user row, no membership and no security level, so steps (3)-(5)
		// cannot run for it at all: LookupUserBySubject would find nothing and it
		// would be refused. Task D created it deliberately for two callers —
		// statutoryd's freeze hook (GET /vouchers/{id} then POST
		// /vouchers/{id}/external-reference, pinning an IRN on a voucher in a
		// company it is not a member of) and the offline/integrator seams (POST
		// /sync/commit, POST /import/tally-xml). handler_admin.go's adminCtx keeps
		// the admin surface out of its reach separately, so a compromised side
		// plane cannot grant itself Owner.
		//
		// AND WHY IT IS NO LONGER UNCONDITIONAL. It used to return here BEFORE
		// routeAction ran, which made it the one caller in the product for whom
		// two invariants of this subtree did not hold:
		//
		//	PRINT. Track T1 mounted GET /print/vouchers/{class}/{id} and GET
		//	/print/reports/{name} inside {companyID}. Printing is a separate
		//	authority from Display precisely because a PDF leaves the building —
		//	that is the whole reason authz.Print exists and the reason migration
		//	0038 has to grant it explicitly. A service token was handed the
		//	payroll register and any named employee's payslip as a finished PDF,
		//	in any company whose UUID it could name, and there was NO rule an
		//	owner could write to stop it because the PDP never ran. Refused here,
		//	for every service principal, unconditionally: nothing in the tree
		//	prints with a svc: token, and no integrator needs paper.
		//
		//	DEFAULT-DENY. routeAction default-denies any {companyID} sub-path it
		//	does not map, which is what makes "a route mounted without a mapping
		//	is refused, not served unauthorized" a property of this subtree.
		//	Skipping routeAction exempted service principals from it, so every
		//	route added after this one was born reachable by them. Now an unmapped
		//	sub-path is 403 for a svc: token exactly as it is for a person.
		//
		// THE BOUNDARY AND ITS LIMIT. Everything routeAction DOES map that is not
		// a Print still gets Task D's synthetic full access — Create, Alter,
		// Delete and Display are unchanged, so the freeze hook, sync commit and
		// Tally-XML import are untouched, as is verify-tenancy.mts's contract that
		// a svc principal reads company A and its trial balance with no
		// membership. Giving service principals a REAL ACL identity (a level,
		// rules, a cutoff, so an owner could write `disallow display
		// report:payroll-register` against one) is a Task-D-sized change and is
		// deliberately not attempted here; this closes the document surface and
		// restores default-deny, nothing more. See auth_print_service_test.go.
		//
		// routeAction does not touch the request body, so calling it here cannot
		// disturb the payload the sync/import handlers read (only
		// voucherDateFromRequest peeks a body, and that stays on the person path).
		if claims.IsService() {
			act, _, _, mapped := routeAction(r)
			if !mapped {
				s.denyUnmappedRoute(w, r)
				return
			}
			if act == authz.Print {
				writeError(w, http.StatusForbidden, "service principals cannot print documents")
				return
			}
			ctx := db.WithSubject(r.Context(), claims.Subject)
			ctx = db.WithCompany(ctx, companyID)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// (3) Resolve the user by subject.
		userID, active, found, err := s.authStore.LookupUserBySubject(r.Context(), claims.Subject)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "resolve user")
			return
		}
		// A VALID token whose subject has no provisioned user (or a disabled one)
		// is authenticated but unauthorized for this company. 401 is reserved for
		// missing/invalid/expired tokens (see authenticate + stepUpRequired); an
		// authorization failure here is 403, uniform with the non-member case so
		// it does not leak whether the subject is a known user.
		if !found {
			writeError(w, http.StatusForbidden, "not authorized for this company")
			return
		}
		if !active {
			writeError(w, http.StatusForbidden, "account is inactive")
			return
		}

		// (4) Membership in the targeted company is the tenant gate.
		levelID, member, err := s.authStore.Membership(r.Context(), userID, companyID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "membership")
			return
		}
		if !member {
			writeError(w, http.StatusForbidden, "not a member of this company")
			return
		}

		// Map the matched route to (action, resource). An unmapped route under
		// {companyID} default-DENIES (never allow).
		act, res, resName, mapped := routeAction(r)
		if !mapped {
			s.denyUnmappedRoute(w, r)
			return
		}

		// (5) Load the level + rules under the company GUC and evaluate.
		level, err := s.authStore.Level(r.Context(), companyID, levelID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "load level")
			return
		}
		rules, err := s.authStore.Rules(r.Context(), companyID, levelID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "load rules")
			return
		}

		// THE DATE ALREADY IN THE BOOKS (round 3). A write that names an EXISTING
		// voucher is judged on BOTH ends of the move — see
		// storedVoucherDateForRequest for the two doors this closes and why
		// neither of the two earlier rounds could reach them.
		//
		// ⛔ THE READ IS GATED ON `level.BackdatedCutoff != nil`, WHICH IS THE
		// WHOLE ANSWER TO "A DB HIT ON THE HOT PATH". A store read the PDP does
		// not need is a store read that must not happen: at a company that has
		// locked no period the cutoff cannot deny anything, so the stored date
		// changes no decision and no query is issued. Every existing tenant that
		// has never set a cutoff pays exactly nothing, and the ones that have pay
		// ONE indexed lookup on (company_id, voucher_id) — and only on the four
		// routes that name an already-stored voucher. Never on an ordinary create,
		// never on a read, never on a master write: for those,
		// existingVouchersNamedByRequest returns no names and no transaction is
		// opened at all. `level` is already in hand here (it was loaded two
		// statements ago for the rule evaluation), so the gate itself is free.
		var storedDate *time.Time
		if level.BackdatedCutoff != nil {
			storedDate = s.storedCutoffDateForRequest(r, companyID, act)
		}

		req := authz.Request{
			UserID:            userID,
			CompanyID:         companyID,
			Action:            act,
			Resource:          res,
			ResourceName:      resName,
			VoucherDate:       voucherDateFromRequest(r, act),
			StoredVoucherDate: storedDate,
			DateSubject:       cutoffDateSubject(r),
			HasWebAuthn:       claims.HasWebAuthn(),
		}
		decision := authz.Evaluate(level, rules, req)
		if !decision.Allow {
			if decision.Reason == authz.StepUpReason {
				stepUpRequired(w)
				return
			}
			writeError(w, http.StatusForbidden, decision.Reason)
			return
		}

		// (6) Authorized: inject the authz-verified identity + company into ctx.
		ctx := db.WithSubject(r.Context(), claims.Subject)
		ctx = db.WithUser(ctx, userID)
		ctx = db.WithCompany(ctx, companyID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// denyUnmappedRoute answers a request routeAction could not map to an
// (action, resource) pair — D40.
//
// ⛔ IT USED TO BE A FLAT 403 "unmapped route: access denied", AND THAT MADE
// "THIS ROUTE WAS NEVER BUILT" INDISTINGUISHABLE FROM "YOUR LEVEL IS NOT
// PERMITTED". The cost was not theoretical: `/reports/sales-order-book`,
// `/reports/outstanding`, `/outstanding` and `/reports/day-book` are all
// misspellings of real routes, every one of them answered 403, and every one was
// diagnosed as a permission defect before somebody re-read the router. It also
// destroyed the recon's literal proof that PATCH on a challan returns 405.
//
// ⛔⛔ THE SECURITY PROPERTY IS PRESERVED EXACTLY, AND THIS IS THE WHOLE DESIGN.
// The question "does the PDP know this route?" and the question "does the router
// serve this path?" have different answers, and only ONE of them may disclose
// anything:
//
//	router serves it, PDP cannot map it -> 403, UNCHANGED. This is a PDP GAP —
//	    a route was mounted and nobody gave it an (action, resource). It MUST
//	    fail closed, because the alternative is serving a route no rule governs.
//	router serves the path, not this method -> 405 + Allow. The path's existence
//	    is already disclosed by the methods that do work.
//	router serves nothing here -> 404. Nothing to protect: there is no resource.
//
// TestEveryCompanyRouteIsMappedAndInVocabulary walks the router and fails on any
// mounted company route the PDP cannot map, so in a healthy tree the 403 arm is
// UNREACHABLE and this change cannot widen anything. That test is the reason the
// arm can be kept as a pure fail-closed backstop rather than being reasoned away.
//
// ⚠️ AND IT DISCLOSES NOTHING A CALLER DID NOT ALREADY HAVE. Both call sites are
// past authentication, and the person path is past the membership gate as well —
// a non-member is still refused with "not a member of this company" and can
// enumerate nothing. What is disclosed to an authenticated member of the company
// is the shape of the static API surface, which is identical for every tenant and
// carries no tenant data.
func (s *Server) denyUnmappedRoute(w http.ResponseWriter, r *http.Request) {
	served, canAsk := s.routeMethods(r)
	switch {
	case !canAsk:
		// The router could not be consulted. Fail CLOSED — an inability to tell
		// the cases apart is exactly the situation the old behaviour was for.
		writeError(w, http.StatusForbidden, "unmapped route: access denied")
	case len(served) == 0:
		writeError(w, http.StatusNotFound, "no such route")
	case !slices.Contains(served, r.Method):
		// RFC 9110 requires Allow on a 405, and it is also the disclosure D40
		// asked for: the recon can now prove which methods a route really has.
		w.Header().Set("Allow", strings.Join(served, ", "))
		writeError(w, http.StatusMethodNotAllowed, "method not allowed on this route")
	default:
		// The router WOULD serve this exact request and the PDP has no mapping
		// for it: a PDP gap, not a missing route. Fail closed.
		writeError(w, http.StatusForbidden, "unmapped route: access denied")
	}
}

// probeMethods is the method set routeMethods asks the router about. CONNECT and
// TRACE are omitted deliberately: nothing in this product mounts either, so
// probing them could only ever pad an Allow header with methods no route has.
var probeMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

// routeMethods asks the ROUTER — the same tree that will or will not serve this
// request — which methods it actually mounts at this request's path. `canAsk` is
// false when the router is not available, and the caller then fails closed.
//
// ⛔ THE ROUTER IS THE ORACLE, NOT A SECOND TABLE. A hand-maintained list of
// "routes that exist" would drift from the router the first time somebody mounted
// something, and the drift would show up as this function lying about a real
// route. chi.Mux.Match walks the same radix tree ServeHTTP walks; it runs no
// handler and no middleware, and it is given a FRESH route context so it cannot
// disturb the one this request is being routed with (chi mutates the context it
// is handed — see the note on Mux.Match).
//
// The FULL original path is used, not routeSuffix: chi does not rewrite
// r.URL.Path (it tracks progress in the route context), so this is the same
// string the top-level router matched, asked of the top-level router.
func (s *Server) routeMethods(r *http.Request) (served []string, canAsk bool) {
	if s.router == nil {
		return nil, false
	}
	for _, m := range probeMethods {
		if s.router.Match(chi.NewRouteContext(), m, r.URL.Path) {
			served = append(served, m)
		}
	}
	return served, true
}

// voucherDateFromRequest peeks a write body's voucher date out so the PDP can
// apply the back-dated cutoff, WITHOUT consuming the body the handler reads.
// It snapshots and restores r.Body. Returns nil when the date is absent or
// unparseable (the cutoff then does not apply).
//
// IT IS ROUTE-AWARE FOR ONE ROUTE, AND THAT IS NOT A SPECIAL CASE FOR ITS OWN
// SAKE. POST /vouchers/payroll is the only voucher write that does NOT carry
// `voucher_date` on the wire: a pay run is identified by `wage_month` and the
// voucher is dated the last day of it. Reading only `voucher_date` therefore
// left the pay run with NO date in the PDP request, and cutoffDeny skips
// entirely when the date is nil — so the cutoff was inert on the most sensitive
// voucher in the product. See peekPayrollVoucherDate for why the answer is to
// derive the date from wage_month rather than to add a second field carrying it.
//
// The route is matched on the SUFFIX, exactly as routeAction matches it, so the
// two cannot disagree about which request is a pay run.
func voucherDateFromRequest(r *http.Request, act authz.Action) *time.Time {
	if !act.IsWrite() {
		return nil
	}
	// WHICH REQUEST IS A PAY RUN MUST BE DECIDED THE SAME WAY routeAction
	// DECIDES IT. routeAction reaches the pay-run handler via splitPath, which
	// trims surrounding slashes; an exact `routeSuffix(r) == "/vouchers/payroll"`
	// comparison does not. Today the two agree on every path chi will actually
	// route. But the moment anything normalises paths — chi's StrictSlash, a
	// path-cleaning middleware, a reverse proxy collapsing "//" to "/" — a
	// variant path would still reach handlePayrollRun while THIS test missed it,
	// and the back-dated cutoff would silently switch off for exactly the
	// voucher it was just fixed to cover. One notion of route identity.
	payRunSegs := splitPath(routeSuffix(r))
	isPayRun := len(payRunSegs) == 2 && payRunSegs[0] == "vouchers" && payRunSegs[1] == "payroll"
	// Cap and snapshot the body, then restore it so the handler re-reads it.
	body := drainBody(r)
	if body == nil {
		if isPayRun {
			// No body at all on a pay run: the wage month cannot be determined,
			// so refuse rather than skip the cutoff. See peekPayrollVoucherDate.
			return beforeEveryCutoff()
		}
		return nil
	}
	if isPayRun {
		return peekPayrollVoucherDate(body)
	}
	// A TDS CHALLAN DATES ITSELF WITH `deposit_date`, SO IT WAS THE SECOND ROUTE
	// THE CUTOFF WAS INERT ON — and it shipped with three comments asserting the
	// opposite.
	//
	// The claim written alongside the route was "recording a deposit dated inside
	// a closed period IS a back-dated act", which is true, and the control was
	// simply absent: the body carries no `voucher_date`, peekVoucherDate returns
	// nil, and cutoffDeny skips entirely when the date is nil. A comment asserting
	// a control that never runs is worse than a missing control, because nobody
	// looks again.
	//
	// So it is handled here, the same way and for the same reason as the pay run:
	// one notion of route identity, matched on the segments routeAction itself
	// matches, so the two cannot disagree about which request is a challan.
	//
	// ⚠️ THE DATE THAT MATTERS IS THE DEPOSIT DATE, not the day it is keyed. An
	// owner who has locked a period has locked what may be ASSERTED about it, and
	// a deposit is an assertion that money reached the Government on that date.
	// Recording a genuinely old deposit against a closed period needs the cutoff
	// moved — deliberately, by whoever owns the lock — which is exactly the
	// conversation the cutoff exists to force.

	challanSegs := splitPath(routeSuffix(r))
	if len(challanSegs) == 1 && challanSegs[0] == "tds-challans" {
		return peekDepositDate(body)
	}
	// THE BANK DATE IS THE THIRD ROUTE OF THIS SHAPE, and it shipped with
	// routeAction's comment asserting the cutoff already reached it. See
	// peekBankDate for why it is not a copy of the challan's peek (the store
	// accepts spellings Go's layout rejects, so an unreadable one fails CLOSED)
	// and bankReconLeg for why the route is matched the way routeAction matches it.
	//
	// ⚠️ ONLY THE PUT CARRIES A DATE. The DELETE un-reconciles and has no body at
	// all; its whole act is on the STORED bank date, which is judged as the source
	// end by storedBankDateForRequest — the same both-ends rule round 3 established
	// for the voucher ALTER and DELETE.
	if _, ok := bankReconLeg(r); ok && r.Method == http.MethodPut {
		return peekBankDate(body)
	}
	d := peekVoucherDate(body)
	if d == nil {
		return nil
	}
	return d
}

// ===========================================================================
// ROUND 3 — THE DATE THAT IS ALREADY IN THE DATABASE
// ===========================================================================
// Everything above judges the date THE REQUEST CARRIES. Round 1 made an
// unreadable one fail closed; round 2 taught the peek to find one nested at any
// depth and one inside a Tally envelope. After both shipped, a browser re-drive
// walked out of a locked period without sending a back-dated date at all —
// verbatim, on the wire, against a company whose Owner level had
// BackdatedCutoff = 2026-07-01 and a journal already posted at 2026-05-01:
//
//	ALTER   PUT /v1/companies/<C>/vouchers/journal/<id>  voucher_date=2026-08-20
//	        -> 200 {"version_no":2,...}
//	DELETE  DELETE /v1/companies/<C>/vouchers/<id>
//	        -> 200 {"op":"DELETE","version_no":3,...}
//	CONTROL POST /v1/companies/<C>/vouchers/journal      voucher_date=2026-05-01
//	        -> 403  (the cutoff was live throughout)
//
// NEITHER DOOR IS BODY-SHAPED, WHICH IS WHY TWO ROUNDS OF PEEK-HARDENING COULD
// NOT SEE THEM. A DELETE has no body: drainBody returns nil, the peek returns
// nil, and cutoffDeny's `req.VoucherDate == nil` arm returns early. An ALTER has
// a body, but it only ever states where the voucher is GOING — so naming next
// month lifts a May voucher clean out of a filed quarter. An operator who cannot
// POST into a filed period could still empty it one voucher at a time.
//
// The date these need is the one the voucher ALREADY carries, and the only place
// that lives is the database. Hence a store read, gated as tightly as it can be.
//
// THE ENUMERATION IS THE POINT OF THIS ROUND. Rounds 1 and 2 each fixed the
// route they were shown and each was followed by another door. So this walks the
// class instead: every write that NAMES an already-stored voucher. There are
// four, and two of them are not the two that were driven —
//
//	PUT    /vouchers/{class}/{voucherID}     the ALTER that was driven
//	DELETE /vouchers/{voucherID}             the DELETE that was driven
//	POST   /sync/commit  op=ALTER | DELETE   the SAME two acts, by voucher id in
//	                                         the body (sync.Service.applyOne
//	                                         calls ledger.AlterVoucher /
//	                                         DeleteVoucher for those two ops)
//	POST   /import/tally-xml  ACTION=Alter|Delete
//	                                         the SAME two acts, by Tally GUID
//	                                         (migration.Importer resolves the
//	                                         GUID with VoucherIDBySourceGUID and
//	                                         calls AlterVoucher / DeleteVoucher)
//
// Every other write in the {companyID} subtree either creates something new or
// touches a document that is not a dated ledger entry; the verdict for each one,
// including the safe ones, is recorded in auth_stored_date_cutoff_test.go's
// route ledger so the next reader does not have to re-derive it.

// namedVouchers is the set of ALREADY-STORED vouchers one request restates or
// removes. The two name kinds are kept apart because their MISSING cases are
// opposites, and conflating them would either open a door or break an import:
//
//	ids   name a voucher DIRECTLY (a path segment, or client_voucher_id on a
//	      sync ALTER/DELETE intent). The named voucher MUST EXIST: the handler
//	      would otherwise 404, and a name we cannot resolve is a period we cannot
//	      rule out, so it fails CLOSED.
//	guids name a voucher through Tally's source_guid. An UNKNOWN guid is
//	      ordinary and must NOT refuse: migration.Importer treats ACTION=Delete
//	      on an unknown guid as a no-op ("skipped", nothing written) and
//	      ACTION=Alter on an unknown guid as a CREATE — which is dated by the
//	      envelope's own <DATE> and already judged by peekTallyXMLVoucherDate.
//	      Failing closed here would refuse every first-time import of an
//	      Alter-flavoured export at any company with a cutoff.
type namedVouchers struct {
	ids   []uuid.UUID
	guids []string
}

func (n namedVouchers) empty() bool { return len(n.ids) == 0 && len(n.guids) == 0 }

// deduped removes repeated names, and it is LOAD-BEARING rather than tidiness.
// storedVoucherDateForRequest refuses when the number of rows it resolved is not
// the number of ids it asked about — that count check IS the fail-closed door for
// a voucher that does not exist. A /sync/commit outbox legitimately names the
// SAME voucher twice (sync.Node emits create, then alter, then delete against one
// target, which is what its own node_test.go asserts), so without this a perfectly
// ordinary offline batch would resolve one row for two ids and be refused as
// unreadable at every company that has locked a period.
func (n namedVouchers) deduped() namedVouchers {
	out := namedVouchers{}
	seenID := make(map[uuid.UUID]struct{}, len(n.ids))
	for _, id := range n.ids {
		if _, dup := seenID[id]; dup {
			continue
		}
		seenID[id] = struct{}{}
		out.ids = append(out.ids, id)
	}
	seenGUID := make(map[string]struct{}, len(n.guids))
	for _, g := range n.guids {
		if _, dup := seenGUID[g]; dup {
			continue
		}
		seenGUID[g] = struct{}{}
		out.guids = append(out.guids, g)
	}
	return out
}

// storedVoucherDateForRequest returns the date the books ALREADY hold for the
// voucher(s) this write names, for authz.Request.StoredVoucherDate. The three
// answers are the three the peeks above use, which is deliberate — one
// convention for "readable / no date here / present but unreadable" across the
// whole cutoff seam:
//
//	nil                 this request names no already-stored voucher (every
//	                    create in the product), so there is no source end to
//	                    judge and cutoffDeny's stored arm skips.
//	a date              the EARLIEST date among the vouchers named. Earliest for
//	                    walkVoucherDates' reason: a batch is only as permitted as
//	                    its most-refused member, or one allowed intent carries a
//	                    back-dated one in with it.
//	beforeEveryCutoff() a voucher was named and its period could NOT be
//	                    established — it is missing, tombstoned, or the store
//	                    errored. Fail CLOSED, the pay run's convention: "I could
//	                    not find out which period this voucher is in" must never
//	                    be spelled the same way as "it is not in a locked one",
//	                    because the second is exactly what let the DELETE through.
//
// ⚠️ IT IS ONLY EVER CALLED WHEN A CUTOFF IS SET (see companyAuthMiddleware), so
// the fail-closed answers cost nothing at a company that has locked no period.
func (s *Server) storedVoucherDateForRequest(r *http.Request, companyID uuid.UUID, act authz.Action) *time.Time {
	named := existingVouchersNamedByRequest(r, act).deduped()
	if named.empty() {
		return nil
	}
	if s.pool == nil {
		// A Server assembled without a pool cannot answer the question, and the
		// answer it cannot give is the one that would allow the write.
		return beforeEveryCutoff()
	}

	// db.WithCompany is what binds RLS for the read. Membership in {companyID}
	// has ALREADY been confirmed by this point (step 4 of the pinned flow), so
	// scoping a read to it grants no reach the caller does not have — and the
	// read is a RunInReadOnlyTx, so a mistake here cannot become a write on the
	// authorization path.
	ctx := db.WithCompany(r.Context(), companyID)
	var (
		earliest *time.Time
		bad      bool
	)
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if len(named.ids) > 0 {
			found, d, unreadable, err := storedDatesByID(ctx, tx, companyID, named.ids)
			if err != nil {
				return err
			}
			// EVERY directly-named voucher must have resolved. A name that
			// resolved to nothing is a period we cannot rule out.
			if found != len(named.ids) {
				bad = true
			}
			bad = bad || unreadable
			earliest = earlierOf(earliest, d)
		}
		if len(named.guids) > 0 {
			// No count check: an unknown guid is a no-op or a create, per
			// namedVouchers' doc.
			_, d, unreadable, err := storedDatesByGUID(ctx, tx, companyID, named.guids)
			if err != nil {
				return err
			}
			bad = bad || unreadable
			earliest = earlierOf(earliest, d)
		}
		return nil
	})
	if err != nil || bad {
		return beforeEveryCutoff()
	}
	return earliest
}

// storedCutoffDateForRequest is the ONE entry point the middleware uses for "the
// date this write would move or remove". It dispatches on the route, because two
// different documents are datable here and they live in different tables:
//
//	the four voucher routes   the voucher's own date   (voucher_version)
//	the two bank-recon routes the leg's bank date      (bank_reconciliation)
//
// ⛔ THE BANK-RECON ROUTES DELIBERATELY DO NOT JUDGE THE VOUCHER'S OWN DATE, AND
// THAT IS A DOOR LEFT OPEN ON PURPOSE. Clearing a cheque written in April
// against a statement that arrives in August is the ordinary work of a
// reconciliation, and migration 0045's trigger already requires
// bank_date >= voucher_date, so nearly every leg an operator ticks names a
// voucher OLDER than the tick. Judging the voucher's date here would refuse the
// feature at every company that has ever locked a period, while protecting
// nothing: invariant I-2 is that reconciling moves no ledger figure, so no filed
// return changes. What the cutoff owns here is the BANK DATE — the assertion the
// operator is making about when money moved.
//
// ⛔ THE THIRD DATABLE DOCUMENT IS THE CHALLAN, and it arrived with the
// allocate-later route (register row D1). POST .../tds-challans/{id}/allocations
// carries NO date on the wire — the deposit is already recorded and an
// allocation adopts its date — so every body peek in this file is blind to it,
// and the create route's `deposit_date` arm cannot see it either. That is
// precisely round 3's shape: the date that decides the act is the one the books
// already hold. Attaching a deduction to a deposit dated inside a locked period
// changes what that quarter's filed return says the deposit paid for, which is
// what the lock exists to freeze.
func (s *Server) storedCutoffDateForRequest(r *http.Request, companyID uuid.UUID, act authz.Action) *time.Time {
	if !act.IsWrite() {
		return nil
	}
	if leg, ok := bankReconLeg(r); ok {
		return s.storedBankDateForLeg(r, companyID, leg)
	}
	if challanID, ok := challanAllocationTarget(r); ok {
		return s.storedChallanDepositDate(r, companyID, challanID)
	}
	return s.storedVoucherDateForRequest(r, companyID, act)
}

// challanAllocationTarget recognises the allocate-later route and parses the
// challan it names:
//
//	POST .../tds-challans/{challanID}/allocations
//
// ⛔ THE SEGMENTS ARE SPLIT THE WAY routeAction SPLITS THEM, for the reason
// voucherDateFromRequest states at length for the pay run: ONE notion of route
// identity, so the PDP's date logic and the PDP's action mapping cannot disagree
// about which request this is. routeAction maps this path by the `/tds-challans`
// PREFIX; this narrows that to the one shape the router mounts.
//
// ⚠️ AN UNPARSEABLE {challanID} RETURNS ok=false, AND THAT IS NOT A FAIL-OPEN:
// parseChallanID parses the same segment with the same call and answers 400
// before masters is reached, so nothing can be allocated through a malformed
// name. It is bankReconLeg's call, made the same way.
func challanAllocationTarget(r *http.Request) (uuid.UUID, bool) {
	if r.Method != http.MethodPost {
		return uuid.Nil, false
	}
	segs := splitPath(routeSuffix(r))
	if len(segs) != 3 || segs[0] != "tds-challans" || segs[2] != "allocations" {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(segs[1])
	if err != nil {
		return uuid.Nil, false
	}
	return id, true
}

// storedChallanDepositDate returns the deposit date the books ALREADY hold for
// one challan, so the cutoff judges the only end this act has.
//
//	a date              the deposit date on record.
//	beforeEveryCutoff() the challan could not be read — it does not exist, it
//	                    belongs to another tenant (RLS makes those the same
//	                    answer), or the store errored. FAIL CLOSED.
//
// ⛔ THERE IS NO nil ARM, AND THAT IS THE DIFFERENCE FROM storedBankDateForLeg.
// An unreconciled leg is the ORDINARY case there, so a missing row must not
// refuse. Here the named challan must exist — the handler would otherwise 404 —
// so a name that resolves to nothing is a period we cannot rule out, which is
// storedVoucherDateForRequest's rule for a directly-named document and round 1's
// whole lesson: "I could not find out which period this is in" must never be
// spelled the same way as "it is not in a locked one". The cost of the strict
// answer is a 403 instead of a 404, and only at companies that have locked a
// period.
func (s *Server) storedChallanDepositDate(r *http.Request, companyID, challanID uuid.UUID) *time.Time {
	if s.pool == nil {
		return beforeEveryCutoff()
	}
	// db.WithCompany binds RLS; membership in {companyID} is already confirmed by
	// this point, and the read is a RunInReadOnlyTx so a mistake here cannot
	// become a write on the authorization path. Both are storedBankDateForLeg's
	// arguments, unchanged.
	ctx := db.WithCompany(r.Context(), companyID)
	var spelled *string
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, storedChallanDepositDateSQL, companyID, challanID).Scan(&spelled)
	})
	if err != nil || spelled == nil {
		return beforeEveryCutoff()
	}
	d, perr := time.Parse("2006-01-02", *spelled)
	if perr != nil {
		// to_char produced something this layout cannot read, from a NOT NULL DATE
		// column. It should be impossible, and "impossible" is the case that must
		// not fail open.
		return beforeEveryCutoff()
	}
	return &d
}

// storedChallanDepositDateSQL reads one challan's deposit date. company_id is
// stated as well as bound by RLS, exactly as storedBankDateSQL states it.
const storedChallanDepositDateSQL = `SELECT to_char(deposit_date, 'YYYY-MM-DD')
	   FROM tds_challan
	  WHERE company_id = $1 AND id = $2`

// storedBankDateForLeg returns the bank date the books ALREADY hold for one leg,
// so the cutoff judges the SOURCE end of a re-tick or an un-tick. The three
// answers are the three the whole cutoff seam uses:
//
//	nil                 this leg is not reconciled yet, so there is no stored
//	                    assertion to move or withdraw. ⚠️ UNLIKE A NAMED VOUCHER,
//	                    THIS ABSENCE IS ORDINARY AND MUST NOT REFUSE: a first tick
//	                    is the common case, and handleClearBankDate is deliberately
//	                    idempotent, so un-ticking an un-ticked leg is a 200 that
//	                    withdraws nothing.
//	a date              the bank date currently on record.
//	beforeEveryCutoff() the store could not be asked. Fail CLOSED.
//
// ⛔ WHY THE SOURCE END IS JUDGED AT ALL, when the destination check already
// stops a date being asserted into a locked period: the PUT is an UPSERT
// (banking.SetBankDate's ON CONFLICT ... DO UPDATE), so without this a leg
// cleared inside a filed quarter could be re-stamped with a post-cutoff date and
// walk out of the lock, and the DELETE could remove it outright. That is exactly
// the asymmetry round 3 found on the voucher ALTER and DELETE, one table across.
func (s *Server) storedBankDateForLeg(r *http.Request, companyID uuid.UUID, leg bankReconLegRef) *time.Time {
	if s.pool == nil {
		return beforeEveryCutoff()
	}
	// db.WithCompany binds RLS; membership in {companyID} is already confirmed by
	// this point, and the read is a RunInReadOnlyTx so a mistake here cannot become
	// a write on the authorization path. Both are storedVoucherDateForRequest's
	// arguments, unchanged.
	ctx := db.WithCompany(r.Context(), companyID)
	var (
		found   bool
		spelled *string
	)
	err := s.pool.RunInReadOnlyTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		row := tx.QueryRow(ctx, storedBankDateSQL,
			companyID, leg.voucherID, leg.ledgerID, leg.legNo)
		switch err := row.Scan(&spelled); {
		case errors.Is(err, pgx.ErrNoRows):
			return nil // not reconciled yet: see the doc above.
		case err != nil:
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return beforeEveryCutoff()
	}
	if !found || spelled == nil {
		return nil
	}
	d, perr := time.Parse("2006-01-02", *spelled)
	if perr != nil {
		// to_char produced something this layout cannot read, from a NOT NULL DATE
		// column. It should be impossible, and "impossible" is the case that must
		// not fail open — round 1's whole lesson.
		return beforeEveryCutoff()
	}
	return &d
}

// storedBankDateSQL reads the bank date one leg currently carries. The key is
// bank_reconciliation's own uniqueness constraint, so at most one row can match.
const storedBankDateSQL = `SELECT to_char(bank_date, 'YYYY-MM-DD')
	   FROM bank_reconciliation
	  WHERE company_id = $1 AND voucher_id = $2 AND ledger_id = $3 AND leg_no = $4`

// storedVoucherDateSQL reads the CURRENT (head) version's date for a set of
// logical vouchers. It mirrors ledger.GetVoucher's head join EXACTLY —
// voucher_head.current_version_id, `is_deleted = false` — so the PDP and the
// writer cannot disagree about WHICH version's date is the voucher's date.
//
// ⛔ IT IS NOT ledger.Service.GetVoucher, AND THAT IS THE ONE PERFORMANCE
// DECISION IN THIS FILE. GetVoucher additionally reads postings, bill
// allocations, inventory lines and cost allocations — four more queries and a
// full voucher graph — to answer a question about ONE date. This is a single
// indexed lookup on (company_id, voucher_id) plus one join on a primary key.
//
// `is_deleted = false` matters in the fail-closed direction: a voucher that has
// already been tombstoned resolves to NO row, so a second DELETE of it is
// refused under a cutoff rather than waved through on a nil date. Deleting a
// tombstone writes nothing anyway (ledger.DeleteVoucher 404s), so the cost is a
// 403 instead of a 404 at companies that have locked a period.
const storedVoucherDateSQL = `SELECT vh.voucher_id, to_char(vv.voucher_date, 'YYYY-MM-DD')
	   FROM voucher_head vh
	   JOIN voucher_version vv ON vv.id = vh.current_version_id
	  WHERE vh.company_id = $1 AND vh.is_deleted = false AND vh.voucher_id = ANY($2::uuid[])`

// storedVoucherDateByGUIDSQL is the same read keyed by Tally's source_guid — the
// only handle POST /import/tally-xml has on the voucher it is about to alter or
// tombstone (see migration.Importer, which resolves it with
// ledger.VoucherIDBySourceGUID).
const storedVoucherDateByGUIDSQL = `SELECT vh.voucher_id, to_char(vv.voucher_date, 'YYYY-MM-DD')
	   FROM voucher_head vh
	   JOIN voucher_version vv ON vv.id = vh.current_version_id
	  WHERE vh.company_id = $1 AND vh.is_deleted = false AND vh.source_guid = ANY($2::text[])`

func storedDatesByID(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, ids []uuid.UUID) (
	found int, earliest *time.Time, unreadable bool, err error) {
	rows, qerr := tx.Query(ctx, storedVoucherDateSQL, companyID, ids)
	if qerr != nil {
		return 0, nil, false, qerr
	}
	defer rows.Close()
	return scanStoredDates(rows)
}

func storedDatesByGUID(ctx context.Context, tx pgx.Tx, companyID uuid.UUID, guids []string) (
	found int, earliest *time.Time, unreadable bool, err error) {
	rows, qerr := tx.Query(ctx, storedVoucherDateByGUIDSQL, companyID, guids)
	if qerr != nil {
		return 0, nil, false, qerr
	}
	defer rows.Close()
	return scanStoredDates(rows)
}

// scanStoredDates reduces the rows to (how many vouchers resolved, the earliest
// date among them, whether any date was present-but-unreadable).
//
// ⚠️ A NULL voucher_date IS NOT UNREADABLE AND IS NOT EARLY. ledger.Post stores
// an absent date as SQL NULL (nullIfEmpty), so an undated voucher is dated into
// NO period at all and altering or deleting one takes nothing out of the locked
// one. It still COUNTS as resolved — the row exists — which is what keeps the
// count check above from refusing a legitimate write against an undated voucher.
// Treating NULL as the zero-time sentinel instead would refuse every such write
// at any company with a cutoff, which is round 1's "an absent date is somebody
// else's 400" applied to the stored end.
func scanStoredDates(rows pgx.Rows) (found int, earliest *time.Time, unreadable bool, err error) {
	seen := make(map[uuid.UUID]struct{})
	for rows.Next() {
		var (
			id      uuid.UUID
			spelled *string
		)
		if serr := rows.Scan(&id, &spelled); serr != nil {
			return 0, nil, false, serr
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		found++
		if spelled == nil {
			continue // SQL NULL: dated into no period. See the doc above.
		}
		d, perr := time.Parse("2006-01-02", *spelled)
		if perr != nil {
			// to_char produced something this layout cannot read. It should be
			// impossible from a DATE column, and "impossible" is precisely the
			// case that must not fail open — round 1's whole lesson.
			unreadable = true
			continue
		}
		earliest = earlierOf(earliest, &d)
	}
	if rerr := rows.Err(); rerr != nil {
		return 0, nil, false, rerr
	}
	return found, earliest, unreadable, nil
}

// existingVouchersNamedByRequest lists the already-stored vouchers a write
// RESTATES OR REMOVES. It is the enumeration this round exists for: four route
// families, matched on the SAME path segments routeAction matches (splitPath of
// routeSuffix), so the two cannot disagree about which request is which — the
// rule voucherDateFromRequest states at length for the pay run, applied again.
//
// ⚠️ AN UNPARSEABLE ID NAMES NOTHING, AND THAT IS NOT A FAIL-OPEN. It is
// peekDepositDate's call, not the pay run's: the handler parses the SAME uuid
// (uuid.Parse in alterVoucher / handleDeleteVoucher / handleSyncCommit) and
// answers 400 before any row is touched, so no voucher can leave the locked
// period through a malformed name. A 400 that says "invalid voucher id" is the
// more useful answer to that request than a 403 about a period lock.
func existingVouchersNamedByRequest(r *http.Request, act authz.Action) namedVouchers {
	// Only writes can move or remove anything; a Display or a Print names a
	// voucher constantly and must never pay for a read here.
	if !act.IsWrite() {
		return namedVouchers{}
	}
	segs := splitPath(routeSuffix(r))
	switch {
	// PUT /vouchers/{class}/{voucherID} — the ALTER that was driven. The class
	// is validated by isVoucherClass, exactly as routeAction's own PUT arm
	// validates it, so a path routeAction default-denies is not one this
	// function tries to read a date for.
	case r.Method == http.MethodPut && len(segs) == 3 && segs[0] == "vouchers" && isVoucherClass(segs[1]):
		return namedVouchers{ids: parseVoucherID(segs[2])}

	// DELETE /vouchers/{voucherID} — the DELETE that was driven. THE ONLY DATE
	// IN THIS REQUEST IS THE STORED ONE: there is no body at all.
	case r.Method == http.MethodDelete && len(segs) == 2 && segs[0] == "vouchers":
		return namedVouchers{ids: parseVoucherID(segs[1])}

	// POST /sync/commit — the same two acts, arriving from an offline node.
	// Round 2 closed this route for CREATE intents (their voucher_date is nested
	// in intents[].payload, which walkVoucherDates now reaches). An ALTER or
	// DELETE intent is the round-3 shape: sync.Service.applyOne switches on
	// Intent.Op and hands OpAlter to ledger.AlterVoucher and OpDelete to
	// ledger.DeleteVoucher, against the intent's client_voucher_id.
	case r.Method == http.MethodPost && len(segs) == 2 && segs[0] == "sync" && segs[1] == "commit":
		return namedVouchers{ids: peekSyncSupersededVoucherIDs(drainBody(r))}

	// POST /import/tally-xml — the same two acts again, and the one route that
	// names its target by Tally GUID rather than by uuid.
	case r.Method == http.MethodPost && len(segs) == 2 && segs[0] == "import" && segs[1] == "tally-xml":
		return namedVouchers{guids: peekTallyXMLSupersededGUIDs(drainBody(r))}
	}
	return namedVouchers{}
}

// cutoffDateSubject names the date THIS request asserts, so a refusal can be
// about the field the operator actually typed. Empty means the voucher's own
// date, which is every other write in the product and which
// authz.requestedCutoffDeny renders with its original wording.
//
// It exists because the bank-reconciliation routes are the first place where the
// cutoff judges something an operator would not call a voucher date: telling
// somebody ticking a cheque off a statement that "voucher date is before the
// back-dated cutoff" names a field their screen does not have, and sends them to
// look at the voucher instead of at the date they typed. The pay run and the
// challan are deliberately NOT given a subject here — `wage_month` IS the
// voucher's date (payrolldb.LastDayOf of it) and `deposit_date` dates the
// document the request creates, so "voucher date" is not wrong for either.
func cutoffDateSubject(r *http.Request) string {
	if _, ok := bankReconLeg(r); ok {
		return "bank date"
	}
	// The allocate-later route is the second place, and the more extreme one: the
	// operator typed NO date at all. They ticked deductions against a deposit that
	// is already on file, so "this voucher is dated …" names neither a field nor a
	// document they touched. What refuses them is the DEPOSIT's own date, and
	// saying so is the difference between a refusal they can act on (move the
	// cutoff, or allocate against a later deposit) and one they can only appeal.
	if _, ok := challanAllocationTarget(r); ok {
		return "deposit date"
	}
	return ""
}

// bankReconLegRef names one reconciliation row: the leg of one voucher against
// one bank ledger. It is the primary key bank_reconciliation is unique on
// (company_id, voucher_id, ledger_id, leg_no), minus the company.
type bankReconLegRef struct {
	ledgerID  uuid.UUID
	voucherID uuid.UUID
	legNo     int
}

// bankReconLeg recognises the two bank-reconciliation WRITE routes and parses the
// leg they name:
//
//	PUT    .../bank-reconciliation/{ledgerID}/{voucherID}/{legNo}   assert a bank date
//	DELETE .../bank-reconciliation/{ledgerID}/{voucherID}/{legNo}   withdraw one
//
// ⛔ THE PREFIX IS THE ONE routeAction MATCHES, for the reason
// voucherDateFromRequest states at length for the pay run: one notion of route
// identity, so the PDP's date logic and the PDP's action mapping cannot disagree
// about which request this is. routeAction maps any method on this prefix and
// leaves PUT/DELETE as Alter; this narrows that to the shape the router actually
// mounts, because a leg is what the date question is about.
//
// ⚠️ AN UNPARSEABLE PARAMETER RETURNS ok=false, AND THAT IS NOT A FAIL-OPEN. It
// is parseVoucherID's call: bankReconPathParams parses the SAME three segments
// with the same calls and answers 400 before banking.SetBankDate touches a row,
// so no reconciliation can land in a locked period through a malformed name.
func bankReconLeg(r *http.Request) (bankReconLegRef, bool) {
	switch r.Method {
	case http.MethodPut, http.MethodDelete:
	default:
		return bankReconLegRef{}, false
	}
	segs := splitPath(routeSuffix(r))
	if len(segs) != 4 || segs[0] != "bank-reconciliation" {
		return bankReconLegRef{}, false
	}
	ledgerID, lerr := uuid.Parse(segs[1])
	voucherID, verr := uuid.Parse(segs[2])
	legNo, nerr := strconv.Atoi(segs[3])
	if lerr != nil || verr != nil || nerr != nil || legNo < 0 {
		return bankReconLegRef{}, false
	}
	return bankReconLegRef{ledgerID: ledgerID, voucherID: voucherID, legNo: legNo}, true
}

// parseVoucherID turns one path segment into the (0 or 1)-element id list.
func parseVoucherID(seg string) []uuid.UUID {
	id, err := uuid.Parse(seg)
	if err != nil {
		return nil // names nothing; the handler's own 400. See the doc above.
	}
	return []uuid.UUID{id}
}

// peekSyncSupersededVoucherIDs lists the client_voucher_ids of the intents in a
// /sync/commit body that SUPERSEDE an existing voucher.
//
// ⛔ THE OP FILTER IS EXACT, AND MUST STAY THE SAME COMPARISON sync MAKES.
// handleSyncCommit converts the wire string straight to sync.Op with no
// normalisation, and sync.Service.applyOne switches on it: anything that is not
// exactly "CREATE", "ALTER" or "DELETE" falls to `unknown op %q` and the whole
// commit errors with 422 having written nothing. So matching the same two
// literals here cannot leave a spelling that reaches AlterVoucher while this
// function looks away — the "one notion of route identity" rule, applied to an op
// instead of a path.
//
// ⛔ A CREATE INTENT IS DELIBERATELY EXCLUDED. Its client_voucher_id is a NEW id
// the node minted offline (PostVoucherWithID takes a caller-supplied id), so it
// does not exist yet — and namedVouchers.ids fails closed on an id that does not
// resolve. Including creates would refuse every offline commit at any company
// with a cutoff.
func peekSyncSupersededVoucherIDs(body []byte) []uuid.UUID {
	if body == nil {
		return nil
	}
	var probe struct {
		Intents []struct {
			Op              string `json:"op"`
			ClientVoucherID string `json:"client_voucher_id"`
		} `json:"intents"`
	}
	// ⛔⛔ DECODED THE WAY handleSyncCommit DECODES, AND THE DIFFERENCE WAS A DRIVEN
	// BYPASS OF THE BACK-DATED CUTOFF.
	//
	// This used `json.Unmarshal`, under the comment "not a decodable sync body:
	// handleSyncCommit's decodeJSON answers 400 and nothing is written, so there is
	// no voucher named here to protect." ⚠️ THAT PREMISE IS FALSE. `decodeJSON` uses
	// `json.NewDecoder().Decode()`, which takes the FIRST JSON value and IGNORES
	// whatever follows; `json.Unmarshal` REJECTS trailing bytes. So one appended byte
	// made this peek see "not JSON", return NO ids, and `named.empty()` short-circuit
	// the STORED-date arm — while the handler decoded the body perfectly and applied
	// the ALTER. Driven through the real router:
	//
	//	CONTROL /sync/commit op=ALTER                -> 403, voucher still 2026-05-02 v1
	//	ATTACK  /sync/commit op=ALTER + trailing " x" -> 200, voucher now 2026-08-20 v2
	//
	// This is the SIXTH door found in this one control, and every one of the six was
	// a place where an authorization peek re-implemented a decoding rule that lives in
	// `encoding/json`. ⛔ THE RULE: a peek that decides a write's fate must decode the
	// body the way the handler decodes it, or the two disagree and the disagreement is
	// the hole. See peekVoucherDate and peekDepositDate, moved for the same reason.
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&probe); err != nil {
		// Genuinely not JSON at all. The handler's decodeJSON cannot read it either,
		// so it answers 400 and nothing is written — no voucher is named here.
		return nil
	}
	var ids []uuid.UUID
	for _, in := range probe.Intents {
		if in.Op != string(sync.OpAlter) && in.Op != string(sync.OpDelete) {
			continue
		}
		ids = append(ids, parseVoucherID(in.ClientVoucherID)...)
	}
	return ids
}

// peekTallyXMLSupersededGUIDs lists the source GUIDs of the <VOUCHER> messages in
// a Tally envelope that carry ACTION=Alter or ACTION=Delete — the ones
// migration.Importer resolves with ledger.VoucherIDBySourceGUID and hands to
// AlterVoucher / DeleteVoucher.
//
// ⛔ IT NORMALISES THE ACTION EXACTLY AS THE IMPORTER DOES
// (strings.ToUpper(strings.TrimSpace(...)), empty meaning CREATE) and reads the
// GUID through the importer's own TallyVoucher.GUIDValue, which prefers <GUID>
// and falls back to <REMOTEID>. Two spellings of "which voucher does this
// message supersede" is how the PDP and the writer come to disagree.
//
// A GUID-LESS Alter or Delete NAMES NOTHING, and neither case is a hole:
// ACTION=Delete without a GUID is a hard error inside the importer ("requires a
// GUID") that writes nothing, and ACTION=Alter without one is treated as a
// CREATE, dated by the envelope's own <DATE> and already judged by
// peekTallyXMLVoucherDate.
//
// A body that is not an envelope, or an envelope that will not parse, returns no
// names — NOT a refusal — because peekTallyXMLVoucherDate already answers
// beforeEveryCutoff() for an unparseable envelope on the very same body. One
// refusal for one cause; a second one here would only make the reason wrong.
func peekTallyXMLSupersededGUIDs(body []byte) []string {
	if body == nil || !isTallyEnvelope(body) {
		return nil
	}
	env, err := integration.ParseTallyEnvelope(bytes.NewReader(body))
	if err != nil {
		return nil
	}
	var guids []string
	for _, m := range env.Messages() {
		if m.Voucher == nil {
			continue // a LEDGER master message supersedes no voucher.
		}
		switch strings.ToUpper(strings.TrimSpace(m.Voucher.Action)) {
		case "ALTER", "DELETE":
			if g := m.Voucher.GUIDValue(); g != "" {
				guids = append(guids, g)
			}
		}
	}
	return guids
}

// routeAction maps the matched chi route to a (action, resource) pair plus an
// optional resource name (for report:NAME). The pinned table:
//
//	POST  .../groups|ledgers|voucher-types|units|stock-items -> Create/master
//	POST  .../godowns|batches                                -> Create/master
//	GET   .../ledgers|units|stock-items|godowns|batches      -> Display/master
//	GET   .../drafts, .../drafts/{id}                        -> Display/purchase
//	POST  .../drafts, .../drafts/{id}/{claim,claim/release,accepted}
//	                                                         -> Create/purchase
//	POST  .../vouchers/{class}                               -> Create/{class}
//	POST  .../sales (alias)                                  -> Create/sales
//	PUT   .../vouchers/{class}/{voucherID}                   -> Alter/{class}
//	GET   .../vouchers/{voucherID}                           -> Display/voucher
//	DELETE .../vouchers/{voucherID}                          -> Delete/voucher
//	POST  .../vouchers/{voucherID}/external-reference        -> Alter/voucher
//	GET   .../reconciliation-queue                           -> Display/report:reconciliation-queue
//	GET   .../trial-balance|balance-sheet|profit-and-loss|day-book|outstandings|gstr1
//	GET   .../stock-summary|stock-movement|godown-summary
//	GET   .../batch-summary|stock-item-vouchers               -> Display/report:NAME
//	GET   .../price-lists/quote|price-lists/{id}             -> Display/price-list
//	POST  .../price-lists                                    -> Create/price-list
//	POST  .../import/tally-xml                               -> Create/import
//	POST  .../sync/commit                                    -> Create/sync
//	GET   .../itc-reconciliation|ims-inbox                   -> Display/report:NAME
//	POST  .../gstr2b-import                                  -> Create/inward-statement
//	POST  .../ims-actions                                    -> Create/ims-action
//	POST  .../gst-filings                                    -> Create/gst-filing
//	GET   .../employees                                      -> Display/employee
//	POST  .../employees                                      -> Create/employee
//	POST  .../vouchers/payroll                               -> Create/payroll
//	GET   .../payroll-register|payroll-payslip
//	GET   .../payroll-form-138|payroll-pf-ecr
//	GET   .../payroll-esi-return                             -> Display/report:NAME
//	GET   .../print/vouchers/{class}/{voucherID}             -> Print/{class}
//	GET   .../print/reports/{reportName}                     -> Print/report:NAME
//	GET   .../print-templates/{docKind}                      -> Display/company
//	PUT   .../print-templates/{docKind}                      -> Alter/company
//	DELETE .../print-templates/{docKind}                     -> Alter/company
//
// An unmatched route returns mapped=false (default-deny).
func routeAction(r *http.Request) (act authz.Action, res authz.Resource, resName string, mapped bool) {
	// The path AFTER /v1/companies/{companyID}.
	rest := routeSuffix(r)
	method := r.Method

	switch {
	// GET the company master itself: routeSuffix() is "" for
	// /v1/companies/{id} and "/" for the trailing-slash form. This is a Display
	// (never a write), so it must NOT inherit the back-dated write cutoff or the
	// WebAuthn step-up (both gate on Action.IsWrite()). LOAD-BEARING:
	// routeAction default-denies unmapped {companyID} routes, so without this an
	// Owner gets 403 "unmapped route: access denied".
	case method == http.MethodGet && (rest == "" || rest == "/"):
		return authz.Display, "company", "", true

	// PUT the company master: the FEATURE settings write (register row D5).
	// Alter over the SAME `company` token the GET above uses, for the reason
	// spelled out on the master read/write pair below -- one resource, one
	// token, so an ACL rule cannot govern the read and miss the write.
	//
	// THE TOKEN CHOICE IS WHAT KEEPS THIS SAFE WITH NO MIGRATION.
	// `company` is already in authz's vocabulary, migration 0022 seeds Data
	// Entry `allow display company` and nothing wider, and that level's
	// default_access is 'none' -- so `alter company` is default-DENIED for
	// every seeded non-Owner level the day this route appears. Modelling it as
	// anything reached by 0011's coarse `allow display report` would have
	// handed the inventory escape hatch to every Data Entry operator.
	//
	// Being a write it DOES take the Owner level's WebAuthn step-up, and does
	// NOT take the back-dated cutoff (voucherDateFromRequest returns nil: the
	// body carries no date, and a policy switch is not a dated document).
	case method == http.MethodPut && (rest == "" || rest == "/"):
		return authz.Alter, "company", "", true

	// WHOLE-COMPANY BACKUP (T14): a Display over its OWN `backup` token.
	//
	// ⛔ NOT report:NAME, AND THIS IS THE WHOLE SECURITY ARGUMENT FOR THE ROUTE.
	// Migration 0011 seeds the Data Entry level a coarse `allow display report`,
	// so any resource spelled report:<something> is GRANTED to every operator in
	// every tenant the moment it appears. This route answers with every ledger,
	// every party's PAN and address and every payslip in one file. `backup` is a
	// token no seeded level mentions and Data Entry's default_access is 'none',
	// so it is default-DENIED for every seeded non-Owner level — which is why
	// this line needs NO ACL migration, exactly as tds-challan, cost-centre,
	// sales-order, purchase-order and bom shipped with zero acl_rule rows.
	//
	// ⛔ AND NOT `company` EITHER, even though the token exists and is convenient.
	// Migration 0022 seeds Data Entry `allow display company` so the client can
	// read the home state code; folding the backup into that token would hand
	// every operator the entire book by inheritance, without anybody deciding to.
	//
	// A Display, so it does NOT inherit the WebAuthn step-up or the back-dated
	// write cutoff — both gate on Action.IsWrite(), and a read carries no date.
	case method == http.MethodGet && rest == "/backup":
		return authz.Display, "backup", "", true

	// PER-COMPANY PRINT TEMPLATES (track C): what this company's own documents
	// SAY — the title, the letterhead lines, the declaration, the terms, the
	// signature label and the bank block, per document kind.
	//
	// TWO-TOUCH RULE: these arms are what make the three routes in server.go
	// anything other than a 403 for an Owner.
	// TestEveryCompanyRouteIsMappedAndInVocabulary walks the router and turns the
	// suite red if one is missing.
	//
	// ⛔ THE TOKEN IS `company`, AND NOT report:NAME. A print template is company
	// CONFIGURATION — the same authority as the F11 feature settings, which the
	// `PUT ""` arm above already maps to (Alter, "company"). Migration 0011 seeds
	// the Data Entry level a coarse `allow display report`, so anything spelled
	// report:<something> is GRANTED to every operator in every tenant the day it
	// appears — and this write changes what the company's INVOICES say to its
	// customers. Migration 0022 seeds Data Entry `allow display company`, which is
	// a Display, so `alter company` stays default-DENIED for every seeded
	// non-Owner level and this needs NO ACL MIGRATION.
	//
	// ⚠️ AND NOT A NEW TOKEN EITHER. A separate `print-template` token would make
	// "may this level change our invoice layout?" a question no existing rule
	// answers, which sounds tidier until an administrator who wrote `disallow
	// alter company` to lock down company configuration discovers it did not
	// cover the letterhead.
	//
	// THE READ IS A Display AND THE WRITES ARE Alter — including DELETE, which
	// clears an override and returns the company to the built-in layout. That is
	// the same split /bank-reconciliation/ uses for the same reason: the DELETE
	// here UNDOES A CUSTOMISATION, it does not destroy a record, and mapping it to
	// authz.Delete would let a level permitted to SET a declaration be unable to
	// remove it — a one-way door — while making it share authority with `delete
	// company`, which means something far more dangerous.
	//
	// Being writes, PUT and DELETE inherit the Owner level's WebAuthn step-up
	// (Action.IsWrite()). They correctly do NOT inherit the back-dated cutoff: a
	// layout carries no date, and voucherDateFromRequest returns nil for a route
	// with none.
	case strings.HasPrefix(rest, "/print-templates/"):
		switch method {
		case http.MethodGet:
			return authz.Display, "company", "", true
		case http.MethodPut, http.MethodDelete:
			return authz.Alter, "company", "", true
		}
		// Any other method on this prefix falls through to default-deny rather
		// than being silently treated as one of the above.
		return "", "", "", false

	// Masters.
	case method == http.MethodPost && isMasterPath(rest):
		return authz.Create, "master", "", true

	// GET a master list: the chart of accounts, and the inventory masters
	// (godowns, batches, stock items, units). A Display over masters, NOT a
	// report: these are master lists, and modelling them as report:NAME would
	// let a level that disallows one report accidentally hide the chart of
	// accounts or empty the item picker. Read-only, so they must not inherit the
	// back-dated cutoff or the step-up (both gate on Action.IsWrite()).
	// LOAD-BEARING: unmapped {companyID} routes default-deny.
	//
	// It shares isMasterPath with the POST arm ON PURPOSE. The read and the
	// write of the same master must emit the SAME resource token, because that
	// is the only thing that keeps `allow display master` (migration 0022's
	// grant to Data Entry) a READ: the moment a read and a write disagree about
	// which resource they are, one ACL rule stops governing both and a
	// one-token slip hands an operator a power a supervisor never granted.
	// TestDataEntryReadsTheCompanyAndMastersAndNothingWider pins that width.
	case method == http.MethodGet && isMasterPath(rest):
		return authz.Display, "master", "", true

	// Set a ledger's opening balance: an ALTER of a master. It IS a write, so it
	// correctly inherits the back-dated cutoff and step-up rules.
	case method == http.MethodPut && strings.HasPrefix(rest, "/ledgers/"):
		segs := splitPath(rest)
		if len(segs) == 3 && segs[0] == "ledgers" && segs[2] == "opening" {
			return authz.Alter, "master", "", true
		}
		return "", "", "", false

	// Switch a stock item's COSTING METHOD (T9): an ALTER of a master, and the
	// most consequential one in the product. It writes no figure and restates
	// every report that has ever priced the item, so it MUST inherit both write
	// rules: the back-dated cutoff (this is how a closed period's closing stock
	// would otherwise be moved through the front door) and the step-up.
	//
	// ⛔ THE SHAPE IS EXACT AND THE FALL-THROUGH IS THE POINT. Anything else
	// under PUT /stock-items/... returns false and therefore DEFAULT-DENIES,
	// rather than inheriting this rule by prefix — a later sub-route must be
	// mapped deliberately or it does not exist.
	case method == http.MethodPut && strings.HasPrefix(rest, "/stock-items/"):
		segs := splitPath(rest)
		if len(segs) == 3 && segs[0] == "stock-items" && segs[2] == "valuation-method" {
			return authz.Alter, "master", "", true
		}
		return "", "", "", false

	// ===================================================================
	// THE AI BILL INBOX — the six routes cored proxies into insightd (D49).
	// ===================================================================
	// ⛔⛔ THE TOKEN IS `purchase`, THE VOUCHER CLASS, AND THAT CHOICE IS THE
	// LOAD-BEARING PART OF THIS ROW. A draft IS a proposed purchase voucher: the
	// accept posts one through POST /vouchers/purchase moments later, from the same
	// screen, in the same operator's session. So:
	//
	//	GET  /drafts, /drafts/{id}            -> (Display, "purchase")
	//	POST /drafts                          -> (Create,  "purchase")
	//	POST /drafts/{id}/claim               -> (Create,  "purchase")
	//	POST /drafts/{id}/claim/release       -> (Create,  "purchase")
	//	POST /drafts/{id}/accepted            -> (Create,  "purchase")
	//
	// WHY NOT A NEW `draft` TOKEN, which is the obvious answer and the wrong one.
	// Migration 0011 seeds the Data Entry level `allow create|alter|display` on the
	// six voucher classes and `default_access = 'none'`, so a brand-new token would
	// arrive DENIED for the one level that actually keys supplier bills — and the
	// lane would then have to write a migration granting it, i.e. ask a tenant to
	// re-decide a power they already gave that level. Reusing the class token means
	// the F9 Bill Inbox works for Data Entry on the day it lands, with NO migration
	// and with NO new resource arriving pre-granted (contrast D95 and D94, which
	// exist precisely because a new report NAME does arrive granted through 0011's
	// coarse `allow display report`).
	//
	// AND WHAT IT BUYS AN ADMINISTRATOR, which is the test of any token choice:
	// `disallow display purchase` hides the inbox, and `disallow create purchase`
	// refuses the CLAIM — which is the first write of the whole flow and runs
	// before any master or voucher exists, so a level that may not book purchases
	// is stopped at the cheapest possible moment instead of after minting ledgers.
	// Both are rules a supervisor can already write today.
	//
	// ⚠️ THE RELEASE IS A `Create` TOO, AND NOT A Delete. It hands back the lease
	// the claim took; whoever may claim must be able to release, or a failed post
	// strands the bill in a state the operator can neither post nor clear. Giving
	// it its own weaker action would be a rule that lets somebody strand other
	// people's bills.
	//
	// ⚠️ THE BACK-DATED CUTOFF REACHES `POST /drafts` through
	// voucherDateFromRequest's generic peek (it is a write), and it is INERT today
	// because no draft-create body carries a voucher_date — measured: zero
	// occurrences across the whole OCR corpus, and CreateDraftRequest has no such
	// field. If one ever does, refusing is the correct direction: the accept it is
	// preparing would be refused a moment later anyway.
	//
	// ⚠️ AND THE OWNER'S WEBAUTHN STEP-UP now fires at the CLAIM rather than at the
	// on-demand POST /ledgers, because the claim is now the first write of an F9
	// accept. bearerFetch runs the ceremony and replays the request, which is the
	// same path every other first write of a session takes. `GET /drafts` is a
	// Display and must never demand a passkey — an inbox that asks for a
	// fingerprint to LIST is an inbox nobody opens.
	case (method == http.MethodGet || method == http.MethodPost) && isDraftPath(rest):
		if method == http.MethodGet {
			return authz.Display, "purchase", "", true
		}
		return authz.Create, "purchase", "", true

	// Voucher class POSTs (and the /sales alias).
	case method == http.MethodPost && rest == "/sales":
		return authz.Create, "sales", "", true

	// External-reference POST (freeze hook): /vouchers/{voucherID}/external-reference.
	// Logically an Alter on the voucher. Service principals (statutoryd) skip the
	// PDP itself, but they are now routed through THIS function first so that
	// Print is refused and an unmapped sub-path still default-denies for them —
	// so this arm has to map, or the freeze hook the fast path exists for would be
	// refused as unmapped. An Owner passes it as Alter/voucher.
	case method == http.MethodPost && strings.HasPrefix(rest, "/vouchers/"):
		segs := splitPath(rest)
		if len(segs) == 3 && segs[0] == "vouchers" && segs[2] == "external-reference" {
			return authz.Alter, "voucher", "", true
		}
		// fall through to voucher class POST handling below.
		class := strings.TrimPrefix(rest, "/vouchers/")
		if isVoucherClass(class) {
			return authz.Create, authz.Resource(class), "", true
		}
		return "", "", "", false

	// Voucher class PUT (alter): /vouchers/{class}/{voucherID}.
	case method == http.MethodPut && strings.HasPrefix(rest, "/vouchers/"):
		// derive the class from the path: /vouchers/{class}/{voucherID}.
		segs := splitPath(rest)
		if len(segs) == 3 && segs[0] == "vouchers" && isVoucherClass(segs[1]) {
			return authz.Alter, authz.Resource(segs[1]), "", true
		}
		return "", "", "", false

	// GET single voucher, and its EDIT LOG.
	//
	// The history is the SAME (Display, voucher) pair as the voucher itself:
	// whoever may see a voucher may see how it came to look that way, and a
	// level that cannot see it cannot see its history either. Modelling the log
	// as its own resource would let the two drift apart, which for an audit
	// trail is the wrong direction.
	case method == http.MethodGet && strings.HasPrefix(rest, "/vouchers/"):
		segs := splitPath(rest)
		if len(segs) == 2 && segs[0] == "vouchers" {
			return authz.Display, "voucher", "", true
		}
		if len(segs) == 3 && segs[0] == "vouchers" && segs[2] == "history" {
			return authz.Display, "voucher", "", true
		}
		// ⛔⛔ THE BILL-WISE PICKER'S OWN DOOR (D97), AND THE CLASS IN THE PATH IS
		// THE WHOLE POINT OF THE SHAPE.
		//
		// Until this route existed, listing a party's open bills to settle one meant
		// calling `GET /outstandings?kind=PAYABLE` — the RECEIVABLES report's door —
		// so a level carrying `disallow display report:bills-payable` was refused the
		// payables ageing and could still read every figure in it. THE RULE GOVERNED
		// THE DOOR AND NOT THE BYTES. It could not be fixed where it was: this
		// function is the PDP's only view of the request, it sees the PATH, and a
		// query parameter never reaches it.
		//
		// ⛔ THE AUTHORITY IS THE VOUCHER CLASS BEING KEYED, not a report name and not
		// a token of its own. "May list THIS party's open bills so I can settle one"
		// is not the same authority as "may read the payables book", and it is not
		// separable from "may key a Payment" either: a clerk who may post the payment
		// but may not pick the bill can only post it ON ACCOUNT — the balance nobody
		// can chase, which is the failure this whole row exists to avoid. So the class
		// segment carries the decision, every seeded level that can key the voucher
		// already holds `allow display <class>` (migration 0011), and NO migration is
		// needed: a default-deny level cannot silently lose its picker on upgrade.
		//
		// ⛔ THE LEDGER IS IN THE PATH SO THERE IS NO UNSCOPED FORM TO SERVE. Had it
		// ridden a `?ledger_id=`, omitting it would have to be validated into a 400 —
		// and a route that CAN answer with the whole book, guarded only by a check
		// inside the handler, is the same defect as before wearing a third name.
		// Structure, not validation.
		//
		// ⚠️ `?kind=` STILL RIDES IN THE QUERY, AND THAT IS CORRECT HERE. The kind
		// follows the PARTY's group (a Payment can refund a debtor — see kindForGroup
		// in VoucherScreen.tsx), not the class, and the authority granted does not
		// depend on it: it is the same job either way.
		if len(segs) == 4 && segs[0] == "vouchers" && isVoucherClass(segs[1]) &&
			segs[2] == "open-bills" {
			return authz.Display, authz.Resource(segs[1]), "", true
		}
		return "", "", "", false

	// DELETE voucher.
	case method == http.MethodDelete && strings.HasPrefix(rest, "/vouchers/"):
		segs := splitPath(rest)
		if len(segs) == 2 && segs[0] == "vouchers" {
			return authz.Delete, "voucher", "", true
		}
		return "", "", "", false

	// PRINT (track T1). Two routes, both READS:
	//
	//	GET /print/vouchers/{class}/{voucherID} -> (Print, <class>)
	//	GET /print/reports/{reportName}         -> (Print, report:<name>)
	//
	// THE ACTION IS Print AND NOT Display, which is the whole reason this arm
	// exists rather than folding print into the existing report/voucher arms.
	// Handing somebody the Trial Balance on a screen and handing them a PDF of it
	// to take out of the building are different authorities, and Tally has always
	// modelled them as different actions. Because they are different actions,
	// SILENCE DENIES PRINT SEPARATELY: migration 0011's coarse `allow display
	// report` is action=display and does not reach action=print at all, so print
	// of every report is default-denied for the seeded Data Entry level until an
	// administrator (or migration 0038) says otherwise.
	//
	// THE CLASS COMES FROM THE PATH, exactly as POST /vouchers/{class} derives its
	// token, and it is validated by the SAME isVoucherClass the POST arm uses. The
	// report name is validated by the SAME reportName() the JSON report arm uses.
	// One notion of "which segment is legal" per thing: a class a path can carry
	// and a class the PDP can name must not be able to drift apart, or a
	// `disallow print sales` stops matching the route it was written for.
	//
	// AN UNKNOWN CLASS OR REPORT NAME RETURNS mapped=false, so the default-DENY
	// applies. That is the fail-closed direction — a typo'd segment must never
	// resolve to some other resource token that an administrator's rule happens to
	// allow.
	//
	// PRINT IS NOT A WRITE, so neither route inherits the back-dated write cutoff
	// (voucherDateFromRequest returns nil when !act.IsWrite()) or the Owner level's
	// WebAuthn step-up (authz gates the step-up on Action.IsWrite() too, which is
	// false for Print exactly as it is for Display). This is the same line the
	// price-list quote and the Stock Summary sit on, and for the same reason: an
	// operator asked for a second factor to reprint the invoice they keyed five
	// minutes ago prints it from a spreadsheet instead, and then the paper and the
	// books disagree. TestAnOwnerWhoHasNotSteppedUpCanStillPrint pins it, with a
	// control proving the step-up is genuinely armed for that same token on a write.
	//
	// AND THE HOLE THIS ARM OPENS, WHICH THE HANDLER CLOSES. The decision is made
	// from the class in the PATH, before any row is read, so the voucher id is
	// unchecked here. A level allowed `print sales` and refused `print payroll`
	// could otherwise ask for /print/vouchers/sales/<a payroll voucher's id> and be
	// handed the wage bill. handlePrintVoucher therefore reads the STORED class
	// (printingdb.Store.VoucherClass) and refuses a mismatch with a 404 that is
	// byte-identical to the one a non-existent id gets — see handler_print.go.
	case method == http.MethodGet && strings.HasPrefix(rest, "/print/"):
		return printRouteAction(rest)

	// Bank reconciliation (T4). ONE prefix, TWO authorities, and they are
	// deliberately different resources rather than one:
	//
	//   GET    .../bank-reconciliation/{ledgerID}          -> Display/report:bank-reconciliation
	//   PUT    .../bank-reconciliation/{ledgerID}/{v}/{n}  -> Alter/bank-reconciliation
	//   DELETE .../bank-reconciliation/{ledgerID}/{v}/{n}  -> Alter/bank-reconciliation
	//
	// WHY THE READ AND THE WRITE DO NOT SHARE A RESOURCE TOKEN, when the masters
	// arms above go to some trouble to make sure a read and a write DO:
	//
	// For a master, read and write are two authorities over the SAME OBJECT, so
	// one token keeps one ACL rule governing both. Here they are authorities over
	// DIFFERENT objects. Ticking one cheque off against a statement is clerical
	// and moves nothing (invariant I-2). Reading the STATEMENT is the company's
	// entire bank position — every un-cleared cheque and counterparty, the
	// document a leaver walks out with. A single token would force those two to
	// be granted together, and the only safe way to resolve that is to refuse the
	// clerk the ability to reconcile at all, which deletes the feature.
	//
	// So: the write is Alter/bank-reconciliation (migration 0046 grants it to
	// Data Entry) and the read is a REPORT, which 0046 explicitly DISALLOWS at
	// specificity 2 so 0011's coarse `allow display report` cannot reach it.
	//
	// ⛔ THE CUTOFF, AND THE FOUR LINES THAT USED TO STAND HERE. This comment
	// said the write "correctly inherits the back-dated cutoff — a bank date
	// asserted into a closed period is exactly the kind of back-dating that
	// cutoff exists for". The second half was true and the first half was NOT:
	// being an Alter is what makes a request a write, and a write is judged on
	// the date it CARRIES — which the peek looked for under `voucher_date`, while
	// setBankDateReq spells it `bank_date`. So the PDP had no date, cutoffDeny
	// skipped, and a bank date could be asserted into a filed period. Byte for
	// byte the TDS challan's defect, which also shipped with comments asserting
	// the opposite; found by a browser-driven audit, not hypothetically.
	//
	// It is now genuinely judged, on BOTH ends and by the routes' own field:
	// peekBankDate reads the `bank_date` being asserted (the destination) and
	// storedBankDateForLeg reads the one already on the leg (the source, since
	// the PUT is an upsert and the DELETE withdraws). ⚠️ The voucher's OWN date
	// is deliberately not judged here — see storedCutoffDateForRequest for why
	// judging it would refuse ordinary reconciliation while protecting nothing.
	//
	// It does not change a balance, so no separate step-up decision is implied
	// beyond whatever the level already requires of an Alter.
	case strings.HasPrefix(rest, "/bank-reconciliation/"):
		switch method {
		case http.MethodGet:
			return authz.Display, "report", "bank-reconciliation", true
		case http.MethodPut, http.MethodDelete:
			return authz.Alter, "bank-reconciliation", "", true
		}
		// Any other method on this prefix falls through to default-deny rather
		// than being silently treated as one of the above.
		return "", "", "", false

	// BANK STATEMENT IMPORT (T4 slice 2a). ONE prefix, TWO authorities, split
	// the same way bank-reconciliation splits — but the ANSWER is different on
	// the write half, and that difference is the decision:
	//
	//   POST .../bank-statement/{ledgerID}/preview -> Create / bank-statement
	//   POST .../bank-statement/{ledgerID}/import  -> Create / bank-statement
	//   GET  .../bank-statement/{ledgerID}         -> Display/ report:bank-statement
	//
	// 0046 GRANTED Data Entry the reconciliation write, because ticking one
	// cheque off is clerical. Migration 0063 grants Data Entry NOTHING here, and
	// the header of that file has the reasoning: an imported statement is the
	// bank's own file of every counterparty, including the rows that correspond
	// to no voucher the clerk has ever seen. The refusal is the recoverable
	// direction — an owner who disagrees writes one allow rule; a file that has
	// been read cannot be un-read.
	//
	// PREVIEW IS A Create AND NOT A Display, though it writes nothing. It is the
	// first half of an import and it echoes the uploaded file back, so a level
	// that may not import has no business rehearsing one. Calling it a Display
	// would put it under 0011's coarse `allow display report` — which is exactly
	// how a route acquires an authority nobody chose for it.
	//
	// Create rather than Alter because an import ADDS a statement; it never
	// restates one. Re-importing the same file is a no-op, not an update.
	case strings.HasPrefix(rest, "/bank-statement/"):
		switch method {
		case http.MethodGet:
			return authz.Display, "report", "bank-statement", true
		case http.MethodPost:
			return authz.Create, "bank-statement", "", true
		}
		return "", "", "", false

	// THE COST MASTERS (T2, migration 0040) — the axes of cost analysis and the
	// positions on them.
	//
	//   GET  .../cost-categories                  -> Display / cost-centre
	//   POST .../cost-categories                  -> Create  / cost-centre
	//   PUT  .../cost-categories/{categoryID}     -> Alter   / cost-centre
	//   GET  .../cost-centres[?cost_category_id=] -> Display / cost-centre
	//   POST .../cost-centres                     -> Create  / cost-centre
	//   PUT  .../cost-centres/{centreID}          -> Alter   / cost-centre
	//
	// ONE TOKEN FOR ALL SIX — both masters, both halves of each — as for
	// /deductor-status below and for the same reason: they are authorities over the
	// SAME small object (the shape of this company's cost analysis), so one token keeps
	// one rule governing all of it. They are not separable in any case: a centre's
	// cost_category_id is NOT NULL, so a level that may create a centre must be able
	// to read the axes, and an axis with no centres allocates nothing.
	//
	// ⛔ A BARE RESOURCE TOKEN, DELIBERATELY NOT `report:cost-centre`. internal/authz
	// matches a bare `report` rule against ANY `report:NAME` at specificity 1, so a
	// report name would be GRANTED to every seeded Data Entry level by migration 0011's
	// `allow display report` — a rule written years ago for another purpose — and taking
	// it back would need a counterweight migration, which is exactly why 0068 exists
	// for report:tds-register. As a bare token, Data Entry (default_access 'none', every
	// rule it carries naming a specific token) is refused all six routes with NO ACL
	// MIGRATION AT ALL. State the consequence plainly: DATA ENTRY CAN NEITHER SEE NOR
	// CREATE COST MASTERS until an administrator writes `allow create cost-centre`
	// through the admin API. Creating an axis decides how every future allocation is
	// grouped and a wrong axis is invisible on every voucher that used it, so the
	// refusal is the recoverable direction and the grant is a tenant's decision.
	//
	// ⛔ `cost-centre` IS THEREFORE NOT IN authz.KnownReportNames AND MUST NOT BE ADDED.
	// server.go loops over that list and auto-mounts GET /print/reports/<name>; a name
	// with no reportName() case is a route routeAction cannot map, i.e. default-DENY for
	// every level including the Owner. That bug already shipped once for
	// reconciliation-queue — see the reports arm below. The two COST REPORTS
	// (report:cost-centre-summary, report:cost-category-summary) ARE report names and
	// are reached by that arm, not by this one.
	//
	// The POSTs and PUTs are writes and inherit whatever step-up the level requires of
	// a Create or an Alter. ⚠️ THE BACK-DATED CUTOFF IS INERT ON ALL SIX, AND THAT IS
	// STATED RATHER THAN LEFT IMPLIED: none of these bodies carries a date,
	// voucherDateFromRequest finds none, and cutoffDeny skips entirely on a nil date.
	// Renaming an axis is not a back-dated act and there is nothing here for the cutoff
	// to judge. A comment asserting a control that never runs is worse than a missing
	// control, which is the lesson the /tds-challans arm below records at length.
	// ===========================================================================
	// T3 — SALES / PURCHASE ORDERS AND THE BILL OF MATERIALS (migrations 0042, 0043)
	// ===========================================================================
	// THREE BARE TOKENS: `sales-order`, `purchase-order`, `bom`. No ACL migration
	// accompanies any of them, and that is the point rather than an omission — Data
	// Entry's default_access is 'none' and every rule it carries names a specific
	// token, so a token never mentioned is denied. State the consequence plainly:
	// DATA ENTRY CAN NEITHER SEE NOR CREATE ORDERS OR RECIPES until an administrator
	// writes `allow create sales-order` through the admin API.
	//
	// ⛔ TWO ORDER TOKENS AND ONE BOM TOKEN, and the asymmetry is deliberate. Sales
	// and purchase orders are worked by DIFFERENT PEOPLE — a salesperson keys what
	// customers want, a purchase clerk commits the company to spending — so "may key
	// sales orders, may not commit us to paying suppliers" is a rule a business
	// actually writes. A recipe and its components are ONE object (bom_id is NOT
	// NULL), so two tokens there would always have to be granted together, which is
	// a rule an administrator can only get wrong. The test is not "are these things
	// similar" but "would an administrator ever grant one without the other".
	//
	// ⛔ THE DIRECTION IS IN THE PATH, NOT IN THE BODY, PRECISELY SO THIS FUNCTION
	// CAN SEE IT. routeAction reads the path and nothing else; a `direction` field on
	// the request body would let a caller holding only `sales-order` POST a purchase
	// commitment to the sales route, and the PDP would have said yes to the wrong
	// object. The handlers additionally 404 an id whose direction does not match its
	// route, because both families read one order_head table.
	//
	// ⛔ NONE OF THESE THREE IS IN authz.KnownReportNames AND NONE MAY BE ADDED.
	// server.go loops over that list and auto-mounts GET /print/reports/<name>; a
	// name with no reportName() case is a route routeAction cannot map, i.e.
	// default-DENY for every level including the Owner. That bug already shipped once
	// for reconciliation-queue. The two order BOOKS
	// (report:sales-order-book, report:purchase-order-book) ARE report names and are
	// reached by the reports arm below, not by this one. Sitting in a rule list,
	// `sales-order` and `report:sales-order-book` are one word apart, which is how an
	// administrator grants the wrong one.
	//
	// ⚠️ THE BACK-DATED CUTOFF IS INERT ON ALL OF THEM, stated rather than left
	// implied: an order body carries an `order_date`, but voucherDateFromRequest
	// looks for a `voucher_date` and finds none, so cutoffDeny skips entirely. An
	// order is not a posting and back-dating one moves no balance — but a comment
	// asserting a control that never runs is worse than a missing control, so this
	// says which it is. If orders ever need a back-dated cutoff of their own, it is
	// new work and not a rename.
	case strings.HasPrefix(rest, "/sales-orders"):
		switch method {
		case http.MethodGet:
			return authz.Display, "sales-order", "", true
		case http.MethodPost:
			return authz.Create, "sales-order", "", true
		case http.MethodPut:
			// Closing an order: an Alter, not a Delete. The order stays on the books
			// with its stated reason, because one that vanished would take the
			// question "what happened to SO-14" with it.
			return authz.Alter, "sales-order", "", true
		}
		// Any other method default-denies rather than being silently treated as one
		// of the three above.
		return "", "", "", false

	case strings.HasPrefix(rest, "/purchase-orders"):
		switch method {
		case http.MethodGet:
			return authz.Display, "purchase-order", "", true
		case http.MethodPost:
			return authz.Create, "purchase-order", "", true
		case http.MethodPut:
			return authz.Alter, "purchase-order", "", true
		}
		return "", "", "", false

	// The recipe, and its explosion. ⚠️ THE EXPLOSION IS A Display AND NOT A Create,
	// which is worth stating because it returns what a Stock Journal's lines WOULD
	// be: it writes nothing, posts nothing, and could not post anything (archtest
	// forbids internal/orders -> internal/ledger). The operator submits the
	// manufacture as an ordinary Stock Journal, which is governed by the
	// `stock journal` voucher class as it always was.
	case strings.HasPrefix(rest, "/boms"):
		switch method {
		case http.MethodGet:
			return authz.Display, "bom", "", true
		case http.MethodPost:
			return authz.Create, "bom", "", true
		case http.MethodPut:
			return authz.Alter, "bom", "", true
		}
		return "", "", "", false

	// ===========================================================================
	// T5 — THE e-WAY BILL (migration 0047)
	// ===========================================================================
	// ONE BARE TOKEN, `e-way-bill`, with no ACL migration — §6.1's rule, and the
	// consequence stated plainly as the T3 arm above states its own: DATA ENTRY
	// CAN NEITHER RAISE NOR SEE AN e-WAY BILL until an administrator writes
	// `allow create e-way-bill`.
	//
	// ⛔ ONE TOKEN AND NOT TWO (a separate read token), applying the T3 test —
	// "would an administrator ever grant one without the other". They would not:
	// the operator who raises a consignment's bill is the one who reads it back to
	// hand the driver a sheet, and a clerk who could raise a bill but not read it
	// could not check what they had raised.
	//
	// ⛔ Create AND NOT Alter ON THE POST, even though a second POST for the same
	// voucher UPDATEs the row (0047's e_way_bill_one_per_voucher). The act is
	// "raise an e-way bill for this consignment" both times; the upsert is a
	// storage decision, not an authority one. Making the regeneration an Alter
	// would mean an administrator granting `create e-way-bill` had granted the
	// ability to raise a bill exactly once per voucher and no more, which is not a
	// rule anybody would write on purpose — and the failure would land on the
	// operator correcting a typo'd distance.
	//
	// ⚠️ THE BACK-DATED CUTOFF IS INERT HERE, stated rather than left implied: the
	// body carries no `voucher_date`, voucherDateFromRequest finds none, and
	// cutoffDeny skips entirely. Raising an e-way bill posts nothing and moves no
	// balance. If that ever changes it is new work, not a rename.
	case strings.HasPrefix(rest, "/e-way-bills"):
		switch method {
		case http.MethodGet:
			return authz.Display, "e-way-bill", "", true
		case http.MethodPost:
			return authz.Create, "e-way-bill", "", true
		}
		// Any other method default-denies rather than being silently treated as one
		// of the two above.
		return "", "", "", false

	// ===========================================================================
	// THE e-INVOICE / IRN — cored's PROXY into statutoryd
	// ===========================================================================
	// ONE BARE TOKEN, `e-invoice`, with no ACL migration — §6.1's rule again, and
	// the consequence stated plainly: DATA ENTRY CAN NEITHER RAISE NOR SEE AN
	// e-INVOICE until an administrator writes `allow create e-invoice`.
	//
	// ⛔ ONE TOKEN AND NOT TWO, on the same test the e-way bill arm applies:
	// would an administrator ever grant one without the other? They would not.
	// The operator who raises the IRN is the one who reads it back to put it on
	// the invoice, and a clerk who could mint an IRN but not read it could not
	// tell whether the attempt had succeeded — against an endpoint whose whole
	// hazard is minting a SECOND one.
	//
	// ⛔ Create AND NOT Alter ON THE POST, even though raising an e-invoice
	// FREEZES the voucher (statutoryd calls the freeze hook, which pins the
	// display number and hard-blocks later alter/delete). The act being
	// authorised is "raise the e-invoice for this invoice"; the freeze is that
	// act's statutory consequence, not a second one. ⚠️ AND THE FREEZE ITSELF IS
	// SEPARATELY GOVERNED: statutoryd performs it by POSTing
	// /vouchers/{id}/external-reference back into cored, which maps to
	// Alter/voucher in its own arm above. So the pin passes through the PDP on
	// its own terms rather than riding in on this token.
	//
	// ⚠️ THE BACK-DATED CUTOFF IS INERT HERE, stated rather than left implied:
	// the body carries only `voucher_id`, voucherDateFromRequest finds no
	// voucher_date, and cutoffDeny skips. Raising an e-invoice posts nothing and
	// moves no balance.
	case strings.HasPrefix(rest, "/e-invoices"):
		switch method {
		case http.MethodGet:
			return authz.Display, "e-invoice", "", true
		case http.MethodPost:
			return authz.Create, "e-invoice", "", true
		}
		return "", "", "", false

	case strings.HasPrefix(rest, "/cost-categories"):
		switch method {
		case http.MethodGet:
			return authz.Display, "cost-centre", "", true
		case http.MethodPost:
			return authz.Create, "cost-centre", "", true
		case http.MethodPut:
			return authz.Alter, "cost-centre", "", true
		}
		// Any other method default-denies rather than being silently treated as one
		// of the three above.
		return "", "", "", false

	case strings.HasPrefix(rest, "/cost-centres"):
		switch method {
		case http.MethodGet:
			return authz.Display, "cost-centre", "", true
		case http.MethodPost:
			return authz.Create, "cost-centre", "", true
		case http.MethodPut:
			return authz.Alter, "cost-centre", "", true
		}
		return "", "", "", false

		// TDS DEDUCTOR STATUS (Item Zero step 2): whether this company must deduct
	// at all, per tax year. Tally's F11 > Enable TDS > TDS Deductor Details.
	//
	//   GET .../deductor-status            -> Display / deductor-status
	//   PUT .../deductor-status/{taxYear}  -> Alter   / deductor-status
	//
	// ONE TOKEN FOR BOTH, unlike bank-reconciliation above. There, read and write
	// are authorities over different objects. Here they are two authorities over
	// the same small object, so one token keeps one rule governing both — the
	// masters arms' reasoning, not the statement's.
	//
	// IT IS NOT `company`, AND THAT IS THE DECISION IN THIS ARM. Folding it in
	// would make "an accountant may correct our address but must not change our
	// deductor status" unwritable — and those are not comparable acts: flipping
	// the category from INDIVIDUAL to COMPANY starts withholding tax out of every
	// supplier payment for the year. It is not `master` either, because migration
	// 0022 grants Data Entry `allow display master` for the F9 party picker, and
	// there is no `master:NAME` form to narrow it back afterwards.
	//
	// The PUT is an Alter and therefore a write, so it correctly inherits the
	// back-dated cutoff and whatever step-up the level requires of an Alter —
	// restating a prior year's status IS a back-dated act, and it changes what
	// every payment in the following year should have deducted.
	case strings.HasPrefix(rest, "/deductor-status"):
		switch method {
		case http.MethodGet:
			return authz.Display, "deductor-status", "", true
		case http.MethodPut:
			return authz.Alter, "deductor-status", "", true
		}
		// Any other method falls through to default-deny rather than being
		// silently treated as one of the above.
		return "", "", "", false

	// THE TDS DEDUCTOR RETURN PROFILE (T6, migration 0070): the TAN, the deductor
	// block the return is filed under, and the individual personally answerable
	// for the deduction.
	//
	//   GET .../tds-profile -> Display / tds-profile
	//   PUT .../tds-profile -> Alter   / tds-profile
	//
	// ONE TOKEN FOR BOTH, and here that is the SAFE call rather than merely the
	// tidy one. The row carries a NAMED INDIVIDUAL'S PAN, so the read is personal
	// data — and the shape a reviewer reaches for (a sensitive read modelled as
	// `report:NAME`, the write on its own token, as bank-statement does) is the
	// WRONG one here: `report:tds-profile` is reached by migration 0011's coarse
	// `allow display report` at specificity 1 and would hand that PAN to every
	// seeded Data Entry operator, needing a 0068-style counterweight migration to
	// take back. As a bare token both halves are default-DENIED for Data Entry
	// with no migration at all.
	//
	// PUT WITH NO KEY IN THE PATH because the company IS the key
	// (company_tds_profile.company_id is its whole primary key), and it is an
	// Alter rather than a Create: restating the block CORRECTS the one row, it
	// never adds a second.
	case strings.HasPrefix(rest, "/tds-profile"):
		switch method {
		case http.MethodGet:
			return authz.Display, "tds-profile", "", true
		case http.MethodPut:
			return authz.Alter, "tds-profile", "", true
		}
		return "", "", "", false

	// s.394(2) MANUFACTURING DECLARATIONS (Item Zero step 3).
	//
	//   GET  .../tcs-declarations -> Display / tcs-declaration
	//   POST .../tcs-declarations -> Create  / tcs-declaration
	//
	// ITS OWN TOKEN, AND SEPARATE FROM deductor-status, though the two sit side
	// by side on the same screen. They are opposite authorities over opposite
	// sides of the same statute: the deductor status says what WE must withhold,
	// and a declaration says what we must NOT collect from a named customer.
	// Filing one is closer to clerical master-data upkeep — the document arrives
	// from the buyer and is transcribed — while the deductor status is a
	// self-assessment that decides a year of withholding. One token would force
	// an owner to grant both together.
	//
	// Both halves are STILL default-denied for the seeded Data Entry level (its
	// default_access is 'none' and no rule names this token), because a
	// declaration ZEROES tax on every future sale to that buyer and its absence
	// is not detectable from the invoice. That is a decision, not an accident,
	// and TestTCSDeclarationAuthMatrix pins it — an administrator who wants to
	// delegate transcription can now write the rule, which is the point of the
	// separate token.
	case strings.HasPrefix(rest, "/tcs-declarations"):
		switch method {
		case http.MethodGet:
			return authz.Display, "tcs-declaration", "", true
		case http.MethodPost:
			return authz.Create, "tcs-declaration", "", true
		}
		return "", "", "", false

	// THE TDS CHALLAN (T6, migration 0069): the deposit slip — what was paid to
	// the Government, and which deductions it paid for.
	//
	//   GET  .../tds-challans?tax_year_start= -> Display / tds-challan
	//   POST .../tds-challans                 -> Create  / tds-challan
	//
	// ONE TOKEN FOR BOTH HALVES, as for /deductor-status above and for the same
	// reason: they are two authorities over the same small object (this deposit
	// and what it discharged), so one token keeps one rule governing both. The
	// read is not separable in any case — the register's whole content is the
	// allocations the write creates.
	//
	// ⛔ A BARE RESOURCE TOKEN, AND DELIBERATELY NOT `report:tds-challan`. A report
	// name is reached by migration 0011's coarse `allow display report` at
	// specificity 1 and would therefore be GRANTED to every seeded Data Entry
	// level — which is exactly why migration 0068 has to exist for
	// report:tds-register. As a bare token the seeded Data Entry level
	// (default_access 'none', every rule it carries naming a specific token) is
	// refused both halves with NO ACL MIGRATION AT ALL; that was measured on a
	// freshly-migrated database. State the consequence plainly: Data Entry is
	// refused both routes with no rule written, and an administrator who wants a
	// clerk recording challans writes an explicit `allow create tds-challan`
	// through the admin API — a tenant's decision, not a default.
	//
	// ⛔ `tds-challan` IS THEREFORE NOT IN authz.KnownReportNames AND MUST NOT BE
	// ADDED. server.go loops over that list and auto-mounts
	// GET /print/reports/<name>; a name with no matching reportName() case is a
	// route routeAction cannot map, i.e. default-DENY for every level including
	// the Owner. That bug already shipped once for reconciliation-queue — see the
	// reports arm below.
	//
	// The POST is a Create and therefore a write, so it inherits whatever step-up
	// the level requires of a Create.
	//
	// ⛔ AND IT INHERITS THE BACK-DATED CUTOFF ONLY BECAUSE voucherDateFromRequest
	// WAS TAUGHT TO READ `deposit_date`. This comment originally asserted the
	// cutoff applied and it did NOT: the cutoff reads `voucher_date`, the challan
	// body carries `deposit_date`, and cutoffDeny skips entirely on a nil date. So
	// a deposit could be dated into a period an owner had locked, with the prose
	// here telling the next reader the control was in place. See peekDepositDate,
	// and TestAChallanDepositedIntoAClosedPeriodIsRefused, which fails if that
	// peek is removed.
	case strings.HasPrefix(rest, "/tds-challans"):
		switch method {
		case http.MethodGet:
			return authz.Display, "tds-challan", "", true
		case http.MethodPost:
			return authz.Create, "tds-challan", "", true
		}
		// Any other method default-denies rather than being silently treated as
		// one of the two above.
		return "", "", "", false

	// The reconciliation queue's two SUB-ROUTES (track D). The bare
	// GET /reconciliation-queue is NOT matched here — the prefix carries a
	// trailing slash on purpose — so it keeps falling through to the reports arm
	// below and its shipped resource (report:reconciliation-queue) is unchanged.
	//
	// ⛔ THEY NEEDED A CASE OF THEIR OWN BECAUSE reportName() MATCHES THE BARE
	// PATH EXACTLY. Without this, GET .../reconciliation-queue/{id}/payload maps
	// to nothing, i.e. default-DENY for every level including the Owner — the
	// same defect this file already records shipping once for
	// GET /print/reports/reconciliation-queue.
	//
	// WHY "reconciliation" AND NOT report:reconciliation-queue: migration 0011
	// grants Data Entry a coarse `allow display report`, so reusing the report
	// token would hand every operator in every tenant the voucher body a
	// colleague keyed on another machine, by inheritance. See
	// authz.KnownResources' "reconciliation" entry for the full argument.
	//
	// The POST is an Alter and therefore a write, inheriting the level's step-up.
	// It names no voucher and carries no date, so existingVouchersNamedByRequest
	// returns nothing for it and the back-dated cutoff correctly does not apply —
	// clearing a workflow item moves nothing into or out of a locked period.
	case strings.HasPrefix(rest, "/reconciliation-queue/"):
		switch method {
		case http.MethodGet:
			return authz.Display, "reconciliation", "", true
		case http.MethodPost:
			return authz.Alter, "reconciliation", "", true
		}
		// Any other method default-denies rather than being folded into one above.
		return "", "", "", false

	// Reports (and the recon queue / gstr1, modelled as reports).
	//
	// reconciliation-queue used to be handled by a case of its own HERE, with
	// reportName() not knowing about it, and the two places were not equivalent:
	// this arm only ever ran for GET "/reconciliation-queue", while everything
	// that asks reportName() which segments are legal report names — the print
	// route's printRouteAction, above — refused it. The consequence was that
	// GET /print/reports/reconciliation-queue was UNMAPPED, i.e. default-denied
	// for every level including an Owner, and `disallow print
	// report:reconciliation-queue` was a rule an administrator could write that
	// could never match. It is now a report name in reportName() like every other
	// one, which is the only arrangement under which the two arms cannot disagree.
	case method == http.MethodGet && reportName(rest) != "":
		return authz.Display, "report", reportName(rest), true

	// SaaS admin surface: members, invites, custom security levels. All map to
	// the single resource "security" (Tally's "Security Control"), so one ACL
	// rule governs the whole surface and the seeded Data Entry level — which has
	// no `security` rule and default_access none — is refused on every one of
	// them. The WRITES are Create/Alter/Delete, so they inherit the Owner level's
	// WebAuthn step-up: granting someone access demands a second factor exactly
	// like posting a voucher does.
	case isMembersPath(rest) || isSecurityLevelsPath(rest):
		switch method {
		case http.MethodGet:
			return authz.Display, "security", "", true
		case http.MethodPost:
			return authz.Create, "security", "", true
		case http.MethodPut:
			return authz.Alter, "security", "", true
		case http.MethodDelete:
			return authz.Delete, "security", "", true
		}
		return "", "", "", false

	// Price lists (INV-L, migration 0030). One resource token for the whole
	// surface, split by METHOD into Display and Create, for the same reason the
	// master collections are: the read and the write of a thing must emit the
	// same resource or one ACL rule stops governing both, and the moment they
	// disagree a `display` grant can start carrying a `create`.
	//
	// GET /price-lists/quote is the rate RESOLVER and it is a Display, not a
	// Create: it runs masters.ResolveRate, which is read-only and writes
	// nothing, so it must not inherit the back-dated write cutoff or the
	// WebAuthn step-up (both gate on Action.IsWrite()). An operator keying an
	// invoice at 9am cannot be asked for a second factor to find out what we
	// charge for a widget.
	case isPriceListPath(rest):
		switch method {
		case http.MethodGet:
			return authz.Display, "price-list", "", true
		case http.MethodPost:
			return authz.Create, "price-list", "", true
		}
		return "", "", "", false

	// Import + sync.
	case method == http.MethodPost && rest == "/import/tally-xml":
		return authz.Create, "import", "", true
	case method == http.MethodPost && rest == "/sync/commit":
		return authz.Create, "sync", "", true

	// Conversational query (Phase 3). A read-only NL/DSL query is a DISPLAY,
	// not a Create: it never writes, so it must NOT inherit the back-dated write
	// cutoff or the WebAuthn step-up (both gate on Action.IsWrite(), which is
	// false for Display). An Owner (default_access full) is allowed; a custom
	// level can disallow resource "query". This mapping is LOAD-BEARING:
	// routeAction default-denies any unmapped {companyID} route, so without it
	// EVERY POST /query would 403 even for an Owner.
	case method == http.MethodPost && rest == "/query":
		return authz.Display, "query", "", true

	// The EMPLOYEE MASTER (Track 3). It is deliberately NOT in isMasterPath: the
	// seeded Data Entry level holds `allow display master` (migration 0022) so
	// the F9 party picker works, and folding employees into that token would
	// hand every operator the PAN, UAN, ESIC IP number and bank IFSC of every
	// person in the company by inheritance. See authz.KnownResources' "employee"
	// entry and migration 0035, which disallows create/alter/display on it.
	//
	// Read and write emit the SAME resource and differ only by ACTION, for the
	// reason the master collections do: the moment a read and a write disagree
	// about which resource they are, one ACL rule stops governing both and a
	// `display` grant can start carrying a `create`. The POST is a write, so it
	// correctly inherits the back-dated cutoff and the WebAuthn step-up; the GET
	// is a Display and inherits neither.
	case rest == "/employees":
		switch method {
		case http.MethodGet:
			return authz.Display, "employee", "", true
		case http.MethodPost:
			return authz.Create, "employee", "", true
		}
		return "", "", "", false

	// THE EMPLOYEE'S TWO EFFECTIVE-DATED CHILDREN — the salary structure and the
	// statutory election — plus the closed vocabulary a form keys them from.
	//
	// ⛔ THE SAME RESOURCE AS THE MASTER, ON PURPOSE. A new token here would be
	// granted by nothing and denied by nothing — migration 0035 has never heard
	// of it — so it would guard a SALARY with a rule no administrator has
	// written. Mapped to `employee`, 0035's seeded
	// `disallow create|alter|display employee` covers these routes from the
	// moment they exist, in every tenant, without a migration.
	//
	// AND THE WRITES ARE Creates, so they inherit the back-dated cutoff and the
	// WebAuthn step-up exactly as POST /employees does. That is not incidental: a
	// salary component is effective-DATED, and back-dating one into a closed
	// period silently re-prices every pay run that period contains.
	case isEmployeeChildPath(rest):
		switch method {
		case http.MethodGet:
			return authz.Display, "employee", "", true
		case http.MethodPost:
			return authz.Create, "employee", "", true
		}
		return "", "", "", false

	// The payroll VOCABULARY: the component types with their s.2(88)
	// classification, the PF bases, the regimes and the age bands. It names no
	// party, no amount and nothing about this company — it is the statute's own
	// list — but it is mapped to `employee` rather than to `master` (which every
	// seeded Data Entry level holds) because the only screens that read it are
	// the employee screens, and a level refused the master should meet ONE
	// consistent refusal rather than a form it can fill in and never submit.
	// GET only; there is no POST route, so a POST answers 404 rather than being
	// authorised into nothing.
	case method == http.MethodGet && rest == "/payroll-vocabulary":
		return authz.Display, "employee", "", true

	// The three ITC/IMS WRITES (Track 2). Each gets its OWN resource token —
	// see authz.KnownResources for why three and not one. All three are Creates
	// (each appends a row: a statement, an IMS action, a filing record), so all
	// three inherit the back-dated cutoff and the Owner level's WebAuthn
	// step-up, which is the right side of that line for writes that decide how
	// much input tax credit is claimed.
	//
	// Migration 0033 seeds NO rule for any of them and does not need to: the
	// seeded Data Entry level's default_access is 'none', so a resource no rule
	// names is already denied. The coarse `allow display report` leak that forced
	// 0033 to write two rows applies ONLY to report:NAME, and none of these three
	// is a report.
	case method == http.MethodPost && rest == "/gstr2b-import":
		return authz.Create, "inward-statement", "", true
	case method == http.MethodPost && rest == "/ims-actions":
		return authz.Create, "ims-action", "", true
	case method == http.MethodPost && rest == "/gst-filings":
		return authz.Create, "gst-filing", "", true
	}
	return "", "", "", false
}

// printRouteAction maps the two print paths onto (Print, resource).
//
// It is split out of routeAction's switch because it is the one arm with TWO
// shapes under one prefix, and because the unknown-segment answer must be the
// same for both: mapped=false, i.e. default-deny. A single `default: return
// mapped=false` at the bottom is what guarantees that — an arm per shape, each
// with its own fallthrough, is how one of them would eventually come to return
// mapped=true for a segment nobody validated.
func printRouteAction(rest string) (act authz.Action, res authz.Resource, resName string, mapped bool) {
	segs := splitPath(rest)
	switch {
	// /print/vouchers/{class}/{voucherID}
	case len(segs) == 4 && segs[1] == "vouchers" && isVoucherClass(segs[2]):
		return authz.Print, authz.Resource(segs[2]), "", true
	// /print/reports/{reportName}. reportName() takes the leading-slash form
	// because that is how it is called for the JSON report routes; one function,
	// one list of legal report segments.
	case len(segs) == 3 && segs[1] == "reports" && reportName("/"+segs[2]) != "":
		return authz.Print, "report", segs[2], true
	}
	return "", "", "", false
}

// routeSuffix returns the request path after the /v1/companies/{companyID}
// prefix (always beginning with '/'). It uses the resolved companyID URL param
// so it is robust to id length.
func routeSuffix(r *http.Request) string {
	companyID := chi.URLParam(r, "companyID")
	prefix := "/v1/companies/" + companyID
	p := r.URL.Path
	if strings.HasPrefix(p, prefix) {
		return p[len(prefix):]
	}
	return p
}

func splitPath(rest string) []string {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return nil
	}
	return strings.Split(rest, "/")
}

// isMasterPath matches the master COLLECTION paths — the ones a POST creates
// into and a GET enumerates. `godown` and `stock_batch` (migration 0025) are
// masters like any other: a batch is a MASTER row, never a free-text column on
// a movement, so creating one is (Create, "master") and listing them is
// (Display, "master"). Modelling them as anything else would put stock masters
// outside the one rule a supervisor writes to govern master creation.
func isMasterPath(rest string) bool {
	switch rest {
	case "/groups", "/ledgers", "/voucher-types", "/units", "/stock-items",
		"/godowns", "/batches",
		// T10 — the STOCK GROUP hierarchy (migration 0079). A master on exactly
		// the terms /godowns is: it names no amount, it is what an item form's
		// group picker is built from, and the READ and the WRITE emit the SAME
		// token so one ACL rule governs both (the reason isMasterPath is shared
		// between the two arms above).
		"/stock-groups",
		// THE STATUTORY TDS/TCS SECTION VOCABULARY (D3). A master list in the
		// only sense that matters to the PDP: it names no party, no amount and
		// nothing about this company — it is the Finance Act's rate table — so it
		// reads on the same terms as /ledgers and /units, which the operator
		// keying the deduction already reads. ⛔ NOT a report: report:tds-register
		// is denied to Data Entry by migration 0068 because it lists every
		// supplier paid WITH AMOUNTS, and mapping this list the same way would
		// deny the picker to the one level that keys the vouchers, leaving D3
		// open behind a different error. GET only; there is no POST route, so a
		// POST answers 404 rather than being authorised into nothing.
		"/tds-sections",
		// THE COSTING-METHOD VOCABULARY (T9). A master list on exactly the terms
		// /tds-sections is: it names no party, no amount and nothing about this
		// company — it is the list of methods THIS BUILD can value stock with —
		// and it is what an item form's picker is built from, so the level that
		// keys stock items must be able to read it. GET only; there is no POST
		// route, so a POST answers 404 rather than being authorised into nothing.
		"/inventory-vocabulary":
		return true
	}
	return false
}

// isEmployeeChildPath matches the employee master's two effective-dated
// children:
//
//	/employees/{employeeID}/salary-components
//	/employees/{employeeID}/statutory-elections
//
// It matches the SHAPE and not the id, for the reason isMembersPath does: a
// malformed employee id still resolves to the `employee` resource and is
// rejected by the handler with a 400, rather than falling through to
// routeAction's default-deny and reporting an authorization failure for what is
// actually a bad request.
func isEmployeeChildPath(rest string) bool {
	segs := splitPath(rest)
	if len(segs) != 3 || segs[0] != "employees" {
		return false
	}
	return segs[2] == "salary-components" || segs[2] == "statutory-elections"
}

// isMembersPath matches the member/invite admin routes:
//
//	/members  /members/invites  /members/invites/{inviteID}  /members/{userID}
//
// It matches the SHAPE, not the id, so a malformed id still resolves to the
// "security" resource and is rejected by the handler with a 400 — rather than
// falling through to routeAction's default-deny and reporting an authorization
// failure for what is actually a bad request.
func isMembersPath(rest string) bool {
	segs := splitPath(rest)
	if len(segs) == 0 || segs[0] != "members" {
		return false
	}
	switch len(segs) {
	case 1: // /members
		return true
	case 2: // /members/invites or /members/{userID}
		return true
	case 3: // /members/invites/{inviteID}
		return segs[1] == "invites"
	}
	return false
}

// isSecurityLevelsPath matches /security-levels and /security-levels/{levelID}.
func isSecurityLevelsPath(rest string) bool {
	segs := splitPath(rest)
	if len(segs) == 0 || segs[0] != "security-levels" {
		return false
	}
	return len(segs) == 1 || len(segs) == 2
}

// isDraftPath matches the SIX bill-inbox paths cored proxies into insightd, and
// ONLY those six (register row D49).
//
// ⛔ IT MATCHES SHAPES, NOT PREFIXES, ON PURPOSE. A `strings.HasPrefix(rest,
// "/drafts")` arm would map every future /drafts/... sub-path to (Create,
// "purchase") the moment somebody mounted one — which is the opposite of what
// routeAction's default-deny is for, and it is how a route gets born authorized
// by a rule nobody wrote for it. The six lines in routes() and the six shapes
// here are the same list, twice, deliberately.
//
// ⚠️ The METHODS are checked by the caller, so this deliberately has no opinion
// about them: `GET /drafts/{id}/claim` is not a mounted route, chi answers 405,
// and this function has no business duplicating that.
func isDraftPath(rest string) bool {
	segs := splitPath(rest)
	if len(segs) == 0 || segs[0] != "drafts" {
		return false
	}
	switch len(segs) {
	case 1: // /drafts            (the list, and the create)
		return true
	case 2: // /drafts/{draftID}  (hydrate one)
		return true
	case 3: // /drafts/{draftID}/claim | .../accepted
		return segs[2] == "claim" || segs[2] == "accepted"
	case 4: // /drafts/{draftID}/claim/release
		return segs[2] == "claim" && segs[3] == "release"
	}
	return false
}

// isVoucherClass matches the path segment that FIXES a voucher class, and its
// list must stay in step with authz.KnownVoucherClasses —
// TestEveryCompanyRouteIsMappedAndInVocabulary walks the router and fails on any
// class the vocabulary would refuse in a rule.
func isVoucherClass(c string) bool {
	switch c {
	case "sales", "purchase", "receipt", "payment", "contra", "journal",
		// Credit/Debit Notes are first-class voucher classes, not variants of
		// sales/purchase: they carry their own numbering series and their own
		// GSTR-1 section (cdnr).
		"credit-note", "debit-note",
		// The NON-ACCOUNTING stock vouchers (INV-K). They post no ledger lines
		// at all, so nothing about them is an accounting authority — but each
		// one MOVES GOODS, which is an authority of its own, and each therefore
		// gets its own token so a level can hold one without the others.
		//
		// They ride the ordinary /vouchers/{class} shape on purpose: the class
		// is fixed by the path exactly as it is for Sales, so the PDP mapping,
		// the back-dated cutoff and the WebAuthn step-up all apply to them
		// through the SAME code that applies them to an invoice. A parallel
		// /stock-vouchers/ subtree would have been a second seam to keep in
		// step, and the one that drifted would be the unguarded one.
		"delivery-note", "receipt-note", "stock-journal", "physical-stock",
		// The PAY RUN (Track 3). It rides /vouchers/payroll so the class is
		// fixed by the path and the PDP mapping and the WebAuthn step-up reach
		// it through the SAME code that reaches an invoice. A parallel
		// /payroll-runs subtree would be a second seam to keep in step, and the
		// one that drifted would be the unguarded one — on the most sensitive
		// voucher in the product.
		//
		// THE BACK-DATED CUTOFF IS THE EXCEPTION, AND THIS COMMENT USED TO CLAIM
		// OTHERWISE. Sharing the path is NOT enough to inherit it: the cutoff
		// needs the voucher's DATE, and every other class supplies that as
		// `voucher_date` on the wire while a pay run supplies `wage_month`. For
		// a while the PDP therefore saw no date on a pay run and cutoffDeny
		// skipped it entirely — an owner who closed a filed period still had
		// salaries posted into it. voucherDateFromRequest now derives the date
		// from wage_month with payrolldb.LastDayOf, the same rule the posting
		// uses. Anything else mounted here that dates itself from something
		// other than `voucher_date` needs the same treatment; the path alone
		// will not give it.
		//
		// There is no PUT /vouchers/payroll/{id} today, so `alter payroll` names
		// no route yet; migration 0035 disallows it anyway, which is the
		// fail-closed order.
		"payroll":
		return true
	}
	return false
}

// isPriceListPath matches the price-list master routes:
//
//	/price-lists  /price-lists/quote  /price-lists/{priceListID}
//
// Like isMembersPath it matches the SHAPE and not the id, so a malformed id
// still resolves to the "price-list" resource and is rejected by the handler
// with a 400 — rather than falling through to routeAction's default-deny and
// reporting an authorization failure for what is actually a bad request.
func isPriceListPath(rest string) bool {
	segs := splitPath(rest)
	if len(segs) == 0 || segs[0] != "price-lists" {
		return false
	}
	return len(segs) == 1 || len(segs) == 2
}

// reportName maps a report GET suffix to its pinned report name, or "" if it is
// not a report route.
func reportName(rest string) string {
	switch rest {
	// The GSTR-1 RECONCILIATION QUEUE. It is a report name in
	// authz.KnownReportNames, so it has to be one HERE too: reportName() is what
	// every arm of routeAction asks "is this segment a legal report name?", and a
	// name known to the vocabulary but not to this function is a name that some
	// arms serve and others default-deny. It cost exactly that once — see the
	// comment on the reports arm above.
	case "/reconciliation-queue":
		return "reconciliation-queue"
	// The BANK RECONCILIATION STATEMENT (T4).
	//
	// It is here for the reason the reconciliation-queue comment above records:
	// a name in authz.KnownReportNames that this function does not know is a
	// name some arms serve and others default-deny, and routes() mounts
	// /print/reports/<name> for EVERY known name — so omitting it would leave a
	// mounted route that is unreachable for every level including an Owner.
	//
	// NOTE THAT THE BARE "/bank-reconciliation" IS NOT A ROUTE. The statement is
	// per bank ledger, so the live route is /bank-reconciliation/{ledgerID} and
	// is handled by its own arm above (which matches the trailing slash and so
	// cannot collide with this exact-match case). This entry exists to make the
	// PRINT route mappable and to make `disallow print report:bank-reconciliation`
	// a rule that can match. Printing it still answers 501: the print route
	// carries no ledger id, so there is no way to say WHICH bank account is being
	// printed — a layout decision, recorded in docs/design/t4-model.md §1.3
	// rather than guessed at here.
	case "/bank-reconciliation":
		return "bank-reconciliation"
	// THE IMPORTED BANK STATEMENT (T4 slice 2a). Like the entry above, the bare
	// path is NOT a live route — the statement is per bank ledger, and
	// /bank-statement/{ledgerID} is handled by its own arm (which matches the
	// trailing slash and cannot collide with this exact match). This entry makes
	// the PRINT route mappable and makes `disallow print report:bank-statement`
	// — which migration 0063 writes — a rule that can match something. Printing
	// answers 501: there is no layout, and a blank page that looks like a bank
	// statement is worse than a refusal.
	case "/bank-statement":
		return "bank-statement"
	// THE TDS/TCS REGISTER (Item Zero step 4). Here for the reason the two
	// entries above record: a name in authz.KnownReportNames that this function
	// does not know is a name some arms serve and others default-deny, and
	// routes() mounts /print/reports/<name> for EVERY known name. Printing it
	// answers 501 — there is no print layout for it yet, and a blank page that
	// looks like a filed register is worse than a refusal.
	case "/tds-register":
		return "tds-register"
	// THE TWO COST REPORTS (T2, migration 0040): the Cost Centre Summary for ONE
	// axis, and the per-axis Cost Category Summary that shows what is NOT
	// allocated. Here for the reason the three entries above record: a name in
	// authz.KnownReportNames that this function does not know is a name some arms
	// serve and others default-deny, and routes() mounts /print/reports/<name> for
	// EVERY known name — so omitting either would leave a mounted route that is
	// unreachable for every level INCLUDING the Owner. That bug already shipped
	// once for reconciliation-queue.
	//
	// Both are Displays, so neither inherits the back-dated write cutoff or the
	// WebAuthn step-up (both gate on Action.IsWrite()).
	//
	// ⚠️ NEITHER NEEDS AN ARM OF ITS OWN IN routeAction, and adding one would be a
	// mistake. Both route paths are flat literals equal to these names, so the
	// `method == GET && reportName(rest) != ""` arm already maps them — which is
	// the only arrangement under which the two places cannot disagree. The axis of
	// the Cost Centre Summary rides in ?cost_category_id= rather than in the path,
	// for the flat-literal reason the /stock-item-vouchers comment below states at
	// length.
	//
	// Printing either answers 501: no printed layout has been specified for a cost
	// statement, and a blank page that looks like a filed one is worse than a
	// refusal — the same call bank-statement and tds-register record above.
	case "/cost-centre-summary":
		return "cost-centre-summary"
	case "/cost-category-summary":
		return "cost-category-summary"
	// THE TWO ORDER BOOKS (T3, migration 0042). TWO NAMES AND NOT ONE WITH A
	// ?direction=, because THIS FUNCTION IS THE PDP'S ONLY VIEW OF THE REQUEST and
	// it sees the path, not the query string. A single `report:order-book` would
	// make "the salesperson may see our sales pipeline but not what we have
	// committed to pay suppliers" an unwritable rule — an ordinary rule an ordinary
	// business wants, since the two lists belong to two different jobs. Migration
	// 0073 writes `disallow display` and `disallow print` for each.
	//
	// Printing either answers 501: no printed layout has been specified for an
	// order book, and a blank page that looks like a filed commitment schedule is
	// worse than a refusal — the same call the cost statements above record.
	case "/sales-order-book":
		return "sales-order-book"
	case "/purchase-order-book":
		return "purchase-order-book"
	case "/trial-balance":
		return "trial-balance"
	case "/balance-sheet":
		return "balance-sheet"
	case "/profit-and-loss":
		return "profit-and-loss"
	case "/day-book":
		return "day-book"
	case "/outstandings":
		return "outstandings"
	// ⛔⛔ THE PAYABLES AGEING'S OWN NAME (D94). This arm is the row: the PDP decides on
	// the resource this function returns, and it returns it from the FLAT LITERAL PATH.
	// Until this line existed, the payables book could only be asked for as
	// `/outstandings?kind=PAYABLE` — and a query parameter never reaches here, so
	// *"this level may see what customers owe us but not what we owe suppliers"* could
	// not be written down at all. Same shape as the two order books above.
	//
	// ⚠️ IT IS A SECOND NAME, NOT A REPLACEMENT. `outstandings` still maps as before, so
	// every `acl_rule` row naming it — and the bill-wise picker that a Data Entry clerk
	// needs to settle a supplier bill — is untouched.
	case "/bills-payable":
		return "bills-payable"
	case "/cash-flow":
		return "cash-flow"
	case "/anomalies":
		return "anomalies"
	case "/gstr1":
		return "gstr1"
	case "/gstr3b":
		return "gstr3b"
	// The Stock Summary (INV-F). A Display over report:stock-summary — read-only,
	// so it must NOT inherit the back-dated write cutoff or the WebAuthn step-up
	// (both gate on Action.IsWrite()). An Owner who has not stepped up can read
	// the stock position for the same reason they can read the Trial Balance.
	case "/stock-summary":
		return "stock-summary"

	// The rest of the inventory reporting surface (waves 1 and 2). All four are
	// Displays over report:NAME on the same terms as the Stock Summary above:
	// read-only, so no back-dated cutoff and no WebAuthn step-up.
	//
	// WHY EVERY ONE OF THEM IS A FLAT LITERAL SEGMENT, INCLUDING THE DRILL.
	// TestReportVocabularyMatchesRouter asserts the converse closure by asking
	// routeAction for GET /v1/companies/{id}/<name> for every name in
	// authz.KnownReportNames, so a report whose ROUTE is not exactly its NAME is
	// a vocabulary entry no route serves — a rule an administrator can write and
	// that can never match, which for a `disallow` is the silent-wrong-answer
	// bug internal/authz/vocabulary.go exists to prevent. That is why the item
	// drill takes its stock_item_id as a QUERY PARAM (like every date on this
	// seam) instead of the RESTful /stock-items/{id}/vouchers: the tidier path
	// would either break that closure or force a mapping for a path no route
	// serves.
	//
	// /stock-movement is a PERIOD (from+to); the two summaries are POSITIONS
	// (as_on); the drill is a period over one item.
	case "/stock-movement":
		return "stock-movement"
	case "/godown-summary":
		return "godown-summary"
	case "/batch-summary":
		return "batch-summary"
	case "/stock-item-vouchers":
		return "stock-item-vouchers"

	// ITC reconciliation and the IMS inbox (Track 2, handler_itc.go). Both are
	// Displays over report:NAME on the same terms as everything above: READ-ONLY,
	// so neither may inherit the back-dated write cutoff or the WebAuthn step-up
	// (both gate on Action.IsWrite(), which is false for Display). The person who
	// signs the return reads the reconciliation all day; asking them for a second
	// factor to look at it would be as wrong as asking for one to open the Trial
	// Balance. The three ITC WRITES are mapped separately in routeAction and DO
	// gate, which is where that distinction belongs.
	//
	// Migration 0033 seeds `disallow display report:itc-reconciliation` and
	// `disallow display report:ims-inbox` on the seeded Data Entry level, and
	// those rows are the reason these two lines do not silently hand every
	// operator in every tenant the supplier book: 0011's coarse
	// `allow display report` matches any report:NAME at specificity 1, and the
	// seeded disallow beats it at specificity 2.
	case "/itc-reconciliation":
		return "itc-reconciliation"
	case "/ims-inbox":
		return "ims-inbox"

	// The five PAYROLL reports (Track 3, handler_payroll.go). Read-only, exactly
	// as above: no back-dated cutoff and no step-up, because Action.IsWrite() is
	// false for Display. Note that three of the five stream a ZIP rather than
	// JSON — a statutory file plus its disclosure sidecar — and that changes
	// nothing here: producing a file is still a Display of what is already in the
	// books, and it writes nothing.
	//
	// EVERY SEGMENT IS A FLAT LITERAL EQUAL TO ITS REPORT NAME, and migration
	// 0035's header — which writes them as ".../reports/payroll-register" — is
	// wrong about that. TestReportVocabularyMatchesRouter asks routeAction for
	// GET /v1/companies/{id}/<name> for every name in authz.KnownReportNames, so
	// a nested route would leave five vocabulary entries no route serves: five
	// `disallow` rows an administrator can write that can never match, which for
	// payroll is the silent-wrong-answer bug at its most expensive.
	//
	// Migration 0035 seeds all five disallows for the seeded Data Entry level.
	// Without them the coarse `allow display report` from 0011 would hand every
	// operator the whole wage bill the moment these routes mounted.
	case "/payroll-register":
		return "payroll-register"
	case "/payroll-payslip":
		return "payroll-payslip"
	case "/payroll-form-138":
		return "payroll-form-138"
	case "/payroll-pf-ecr":
		return "payroll-pf-ecr"
	case "/payroll-esi-return":
		return "payroll-esi-return"
	}
	return ""
}
