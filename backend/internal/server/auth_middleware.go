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
//  2. service principal? -> full access scoped to {companyID}; set company GUC
//     and call next (cutoff + webauthn do not apply to svc).
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

		// (2) Service principal: synthetic full access scoped to the targeted
		// company. No membership/level read, no cutoff, no webauthn.
		if claims.IsService() {
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
//	POST  .../vouchers/{voucherID}/external-reference        -> svc (handled before this)
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
	// Logically an Alter on the voucher. Service principals (statutoryd) bypass
	// the PDP entirely (handled in the middleware before routeAction); an Owner
	// also passes this as Alter/voucher.
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

	// Reports (and the recon queue / gstr1, modelled as reports).
	case method == http.MethodGet && rest == "/reconciliation-queue":
		return authz.Display, "report", "reconciliation-queue", true
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
