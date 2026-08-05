package server

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/platform/db"
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
				writeError(w, http.StatusForbidden, "unmapped route: access denied")
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
			writeError(w, http.StatusForbidden, "unmapped route: access denied")
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

		req := authz.Request{
			UserID:       userID,
			CompanyID:    companyID,
			Action:       act,
			Resource:     res,
			ResourceName: resName,
			VoucherDate:  voucherDateFromRequest(r, act),
			HasWebAuthn:  claims.HasWebAuthn(),
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
	d := peekVoucherDate(body)
	if d == nil {
		return nil
	}
	return d
}

// routeAction maps the matched chi route to a (action, resource) pair plus an
// optional resource name (for report:NAME). The pinned table:
//
//	POST  .../groups|ledgers|voucher-types|units|stock-items -> Create/master
//	POST  .../godowns|batches                                -> Create/master
//	GET   .../ledgers|units|stock-items|godowns|batches      -> Display/master
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
	// The write is an Alter and therefore IS a write, so it correctly inherits
	// the back-dated cutoff — a bank date asserted into a closed period is
	// exactly the kind of back-dating that cutoff exists for. It does not change
	// a balance, so no separate step-up decision is implied beyond whatever the
	// level already requires of an Alter.
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
		"/godowns", "/batches":
		return true
	}
	return false
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
