// Package server wires the cored HTTP surface: a chi router exposing the
// Phase 1 capabilities of the modular monolith — company and master creation,
// posting each of the six voucher classes, fetching each financial report,
// generating (and reconciling) GSTR-1, and triggering a Tally-XML migration
// import (which doubles as the Tally-XML ingress). It is deliberately thin:
// every request is decoded, scoped to a company via the db GUC guard, and
// delegated to the owning module. The modules hold the logic.
//
// The server is the composition root of the monolith (it is the only package
// permitted by archtest to import every sibling module). It opens ONE
// application-DSN pool (the least-privilege tally_app role) and shares it across
// all module services, so every write and read flows through the same
// tenant-scoped RunInTx GUC guard and the default-deny RLS policies.
package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/compliance/itc"
	"github.com/khata/cored/internal/identity"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/migration"
	"github.com/khata/cored/internal/nlq"
	"github.com/khata/cored/internal/notify"
	"github.com/khata/cored/internal/payroll/payrolldb"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/query"
	"github.com/khata/cored/internal/reporting"
	"github.com/khata/cored/internal/sync"
	"github.com/khata/cored/internal/webauthn"
)

// Request-body size caps guard against unbounded uploads. The Tally-XML import
// endpoint accepts a larger body than the JSON endpoints.
const (
	maxJSONBody = 1 << 20  // 1 MiB
	maxXMLBody  = 32 << 20 // 32 MiB
)

// Server holds the module services and the chi router. All services share one
// tenant-scoped pool.
type Server struct {
	pool     *db.Pool
	identity *identity.Service
	masters  *masters.Service
	ledger   *ledger.Service
	report   *reporting.Service
	gstr     *gstr.Service
	importer *migration.Importer
	sync     *sync.Service
	query    *query.Service
	router   chi.Router

	// itc is input-tax-credit reconciliation: the GSTR-2B/2A inward statements
	// ingested into gst_inward_statement + gst_inward_document (migration 0032),
	// matched against purchase vouchers to decide what credit is claimable.
	itc *itc.Service
	// payroll is the employee master, the pay run and the register/payslip read
	// models over the tables migration 0034 created. It posts salary vouchers
	// through the SAME ledger service as everything else — never around it.
	payroll *payrolldb.Service

	// Auth (Task D, WS1). verifier verifies inbound JWTs; signer mints dev
	// tokens for POST /auth/token; authStore + the PDP enforce membership and
	// Disallow/Allow on every {companyID} request.
	verifier  authn.TokenVerifier
	signer    *authn.Signer
	authStore authStore
	authzSeed authSeeder
	// authAdmin is the WRITE side of the same catalog: the SaaS admin surface
	// (members, invites, custom security levels) behind /members and
	// /security-levels. See handler_admin.go.
	authAdmin adminStore

	// WebAuthn step-up + account lockout (Task D, WS1 AUTH FOLLOW-UP). These are
	// built UNCONDITIONALLY (they need only pool.Raw(), no signer). webauthn drives
	// the register/assert ceremonies; webauthnStore is the credential store the
	// assert/begin route lists allow_credentials from; webauthnRPID is echoed to
	// the browser. challenges stashes ceremony challenges between begin and finish
	// (Postgres-backed since WS6 — see auth_challenge_store.go — so the ceremony
	// works behind more than one replica). lockout enforces login lockout. The
	// /auth/login + assert/finish token minting still gate on signer != nil.
	webauthn      *webauthn.Service
	webauthnStore webauthn.CredentialStore
	webauthnRPID  string
	challenges    ChallengeStore
	lockout       *authn.Locker

	// credentials is the first-party PASSWORD store (WS-A1, migration 0021)
	// behind POST /auth/login and POST /auth/password. It is a separate interface
	// from authStore because the two live on opposite sides of the authentication
	// boundary: authStore is what the middleware consults about an ALREADY
	// authenticated subject, while credentials is what decides whether a caller is
	// that subject at all. Same concrete *authz.Store underneath.
	credentials credentialStore

	// notifier delivers the transactional outbox (item C). An invite ENQUEUES
	// its notification in the same transaction that creates it; this drains the
	// queue afterwards. Exposed so tests and an operator can drain on demand
	// rather than waiting on a ticker.
	notifier *notify.Dispatcher
}

// authSeeder seeds the Owner security level + membership for a freshly created
// company. Backed by the 0011 seed_company_auth_defaults() SQL helper.
type authSeeder interface {
	SeedCompanyAuthDefaults(ctx context.Context, companyID, userID uuid.UUID) error
}

// credentialStore is the subset of authz.Store the password routes need.
// Declared as an interface here for the same reason authStore is: the handlers
// stay testable without a database, and the composition root stays the only
// place that names the concrete type.
type credentialStore interface {
	// SetPassword upserts the given user's OWN password verifier (self-only RLS).
	SetPassword(ctx context.Context, userID uuid.UUID, c authn.Credential) error
	// CredentialBySubject is the PRE-AUTH read: subject -> (user id, verifier,
	// active, found). found=false covers both "no such user" and "no password
	// set"; the distinction never reaches the wire.
	CredentialBySubject(ctx context.Context, subject string) (userID uuid.UUID, c authn.Credential, active bool, found bool, err error)
	// ProfileByUserID reads the caller's own directory row for the login
	// response's `user` block.
	ProfileByUserID(ctx context.Context, userID uuid.UUID) (subject, displayName, email string, err error)
}

// New constructs a Server backed by the application-DSN connection pool and
// wires every Phase 1 module service over it.
func New(ctx context.Context, appDSN string) (*Server, error) {
	pool, err := db.New(ctx, appDSN)
	if err != nil {
		return nil, err
	}
	m := masters.NewService(pool)
	l := ledger.NewService(pool)

	verifier, signer, err := buildAuth()
	if err != nil {
		pool.Close()
		return nil, err
	}
	authzStore := authz.NewStore(pool)

	// WebAuthn + lockout (WS1). Built unconditionally — they need only the raw
	// pool. The challenge store is Postgres-backed (WS6, migration 0012), so the
	// begin->finish handoff survives a replica switch. RPID/Origin default to the
	// dev frontend; override via env for other deployments.
	rpID := envOr("AUTH_WEBAUTHN_RPID", "localhost")
	origin := envOr("AUTH_WEBAUTHN_ORIGIN", "http://localhost:5173")
	waStore := webauthn.NewPGStore(pool.Raw())
	waService := webauthn.NewService(webauthn.Config{RPID: rpID, Origin: origin}, waStore)
	locker := authn.NewLocker(authn.DefaultLockoutPolicy(), authn.NewPGLockoutStore(pool.Raw()), time.Now)

	s := &Server{
		pool:     pool,
		identity: identity.NewService(pool),
		masters:  m,
		ledger:   l,
		report:   reporting.NewService(pool),
		gstr:     gstr.NewService(pool),
		importer: migration.NewImporter(m, l),
		sync:     sync.NewService(l, pool),
		// Conversational query (Phase 3 + wave-2 WS1). buildTranslator selects the
		// real-Claude adapter when ANTHROPIC_API_KEY is present, else the
		// deterministic, shipped-by-default FakeTranslator. Either way the LLM emits
		// ONLY the typed DSL; execution runs through the same read-only,
		// company-GUC-scoped pool as every report, so the safety model is identical.
		query: query.NewService(pool, buildTranslator()),
		// ITC reconciliation (0032) and payroll (0034). Both take the same
		// tenant-scoped pool as gstr/query, so their transactions and RLS
		// semantics are identical; payroll additionally takes the ONE ledger
		// service l, because a pay run posts real double-entry vouchers.
		itc:       itc.NewService(pool),
		payroll:   payrolldb.NewService(pool, l),
		verifier:  verifier,
		signer:    signer,
		authStore: authzStore,
		authzSeed: authzStore,
		authAdmin: authzStore,
		// Password login (WS-A1). Same concrete store; a distinct interface
		// because it operates on the pre-auth side of the boundary.
		credentials: authzStore,

		webauthn:      waService,
		webauthnStore: waStore,
		webauthnRPID:  rpID,
		challenges:    newPGChallengeStore(pool.Raw()),
		lockout:       locker,

		// Invite delivery (item C). The default Sender LOGS: real SMTP is
		// configuration, not code, and a logger is the honest statement that this
		// deployment does not send mail. Nothing here claims a message arrived.
		notifier: notify.NewDispatcher(pool, notify.LogSender{}),
	}
	s.routes()
	return s, nil
}

// DispatchNotifications drains the notification outbox once and reports how many
// messages were sent.
//
// Exported because a test that had to wait on a background ticker would be slow
// and flaky, and because an operator wants a way to flush the queue on demand
// rather than wondering whether the loop is alive.
func (s *Server) DispatchNotifications(ctx context.Context) (int, error) {
	return s.notifier.DispatchOnce(ctx)
}

// WithNotificationSender returns a dispatcher over the SAME outbox but with a
// different transport. It exists so a test can prove what happens when delivery
// fails — retry, then dead-letter — without waiting on a real transport to
// misbehave, and without a production code path that can be pointed at a
// pretend sender.
func (s *Server) WithNotificationSender(sender notify.Sender) *notify.Dispatcher {
	return notify.NewDispatcher(s.pool, sender)
}

// RunNotifications delivers the outbox on a ticker until ctx is cancelled.
// Started by `cored serve`; never by tests, which drain deterministically.
func (s *Server) RunNotifications(ctx context.Context, every time.Duration) {
	s.notifier.Run(ctx, every)
}

// envOr returns the value of the named env var, or def when it is unset/empty.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// buildTranslator selects the NL->DSL translator (wave-2 WS1). With
// ANTHROPIC_API_KEY set it returns the real-Claude adapter (nlq.ClaudeTranslator);
// otherwise the deterministic FakeTranslator that ships by default. This mirrors
// the insightd adapter pattern: the network translator is built ONLY when a key
// is present, and it is the sole query-path code that touches the network.
//
//   - QUERY_TRANSLATOR_MODEL    overrides the model (default claude-haiku-4-5)
//   - QUERY_TRANSLATOR_BASE_URL overrides the API host (proxy/gateway, and the
//     test-injection seam — server_test.go points it at a dead loopback port to
//     prove the 503 path without a network)
//
// Whatever Query the translator returns is re-validated by the query compiler and
// executed read-only, so the safety model does not depend on which translator ran.
func buildTranslator() query.Translator {
	key := os.Getenv("ANTHROPIC_API_KEY")
	if key == "" {
		return query.FakeTranslator{Now: time.Now}
	}
	return &nlq.ClaudeTranslator{
		APIKey:  key,
		Model:   envOr("QUERY_TRANSLATOR_MODEL", nlq.DefaultModel),
		BaseURL: envOr("QUERY_TRANSLATOR_BASE_URL", nlq.DefaultBaseURL),
	}
}

// buildAuth selects the authentication mode FAIL-CLOSED. A stock binary with no
// auth env configured REFUSES to start rather than booting fully open (the dev
// /auth/token issuer mints validly-signed tokens for any subject, so it must
// NEVER be the silent default).
//
//   - AUTH_JWKS_URL set: production. An external IdP (Keycloak) verifies tokens
//     via authn.JWKSVerifier; there is NO in-process signer, so /auth/token and
//     /auth/login are not mounted and WebAuthn assert/finish 501s (the IdP does
//     step-up). AUTH_ISSUER is REQUIRED alongside it — an unpinned issuer would
//     accept tokens from any realm/client sharing the IdP's signing key.
//     STARTUP BEHAVIOUR CHANGE (WS6): the JWKS is fetched ONCE here, with a 5s
//     timeout, and a failure is a HARD BOOT FAILURE. An operator whose IdP is
//     briefly unreachable now gets a refusal to start instead of a process that
//     boots and 401s every request; that is the deliberate fail-closed choice
//     (a degraded start is indistinguishable from a working one until traffic
//     arrives). Steady state stays lazy: JWKSVerifier re-fetches on TTL expiry
//     or an unknown kid, and serves a stale cache through a brief IdP outage.
//   - AUTH_DEV_TOKEN_ENABLED=true (or CORED_ENV=dev): dev/test. The in-process
//     deterministic issuer signs tokens and /auth/token is mounted.
//   - neither: refuse to start. No fail-open default.
//
// Returned signer is non-nil ONLY in dev mode; routes() mounts /auth/token iff
// the signer is present, and handleDevToken 404s if it is somehow reached.
func buildAuth() (authn.TokenVerifier, *authn.Signer, error) {
	if jwksURL := os.Getenv("AUTH_JWKS_URL"); jwksURL != "" {
		issuer := os.Getenv("AUTH_ISSUER")
		if issuer == "" {
			return nil, nil, errors.New("server: AUTH_JWKS_URL is set but AUTH_ISSUER is empty; without a pinned issuer the JWKS verifier would accept tokens from any realm sharing the signing key. Refusing to start fail-open")
		}
		verifier := authn.NewJWKSVerifier(jwksURL, issuer)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := verifier.Refresh(ctx); err != nil {
			return nil, nil, fmt.Errorf("server: AUTH_JWKS_URL %q unreachable or served an invalid JWKS: %w — refusing to start fail-open", jwksURL, err)
		}
		return verifier, nil, nil
	}
	if os.Getenv("AUTH_DEV_TOKEN_ENABLED") == "true" || os.Getenv("CORED_ENV") == "dev" {
		signer, verifier, err := devIssuer()
		if err != nil {
			return nil, nil, err
		}
		return verifier, signer, nil
	}
	return nil, nil, errors.New("server: no authentication configured — set AUTH_DEV_TOKEN_ENABLED=true for dev/test (mounts the in-process /auth/token issuer) or AUTH_JWKS_URL for production. Refusing to start fail-open")
}

// devIssuer builds the dev signing/verifying pair. The Ed25519 private key comes
// from AUTH_DEV_SIGNING_KEY (base64 RawURL of a 64-byte ed25519 private key) when
// set, otherwise a fresh key is generated and its public half logged. The
// matching StaticKeyVerifier (issuer "cored-dev", kid "cored-dev-ed25519")
// verifies tokens this signer mints.
//
// Production swap (SHIPPED as of WS6): set AUTH_JWKS_URL + AUTH_ISSUER and
// buildAuth uses authn.JWKSVerifier instead of this pair — it fetches the IdP's
// JWKS (ES256/EdDSA only, symmetric keys never admitted) over the network, so
// this package DOES make outbound requests in production mode. This dev issuer
// path itself remains network-free.
func devIssuer() (*authn.Signer, *authn.StaticKeyVerifier, error) {
	const kid = "cored-dev-ed25519"
	const issuer = "cored-dev"

	var priv ed25519.PrivateKey
	if enc := os.Getenv("AUTH_DEV_SIGNING_KEY"); enc != "" {
		raw, err := base64.RawURLEncoding.DecodeString(enc)
		if err != nil || len(raw) != ed25519.PrivateKeySize {
			return nil, nil, errors.New("server: AUTH_DEV_SIGNING_KEY must be base64url of a 64-byte ed25519 private key")
		}
		priv = ed25519.PrivateKey(raw)
	} else {
		_, generated, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return nil, nil, err
		}
		priv = generated
		log.Printf("auth: no AUTH_DEV_SIGNING_KEY set; generated an ephemeral dev key (kid=%s)", kid)
	}
	signer := authn.NewSigner(priv, kid, issuer)
	verifier := authn.NewStaticKeyVerifier(kid, signer.Public(), issuer)
	return signer, verifier, nil
}

// Close releases resources.
func (s *Server) Close() { s.pool.Close() }

// Router exposes the configured chi router so tests can drive the handlers via
// httptest without binding a socket.
func (s *Server) Router() http.Handler { return s.router }

// ListenAndServe starts the HTTP server on addr.
func (s *Server) ListenAndServe(addr string) error {
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           s.router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return httpSrv.ListenAndServe()
}

func (s *Server) routes() {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", s.handleHealthz)

	// Dev auth: mint an Owner/user token. OUTSIDE the authed subtree — this is
	// how a dev/test caller obtains a Bearer token. Mounted ONLY when the
	// in-process dev signer is present (dev mode); in production the route does
	// not exist (404) and tokens come from the external IdP. This gating is the
	// fix for the unauthenticated token-minting oracle: the endpoint mints a
	// validly-signed token for any subject, so it must never serve in prod.
	if s.signer != nil {
		r.Post("/auth/token", s.handleDevToken)
	}

	// WebAuthn step-up ceremony (WS1 follow-up) + the first-party password login
	// (WS-A1). Mounted on this router before it is assigned to s.router. The
	// ceremony routes and POST /auth/password are authn-only; POST /auth/login is
	// pre-auth and gates on the dev signer, mirroring /auth/token.
	s.router = r
	s.authWebAuthnRoutes()
	s.authLoginRoutes()

	// Company lifecycle (identity). authn-ONLY: verify JWT + resolve user
	// (create-on-first-login); skip membership/PDP (the company does not exist
	// yet). handleCreateCompany seeds the Owner level + membership afterward.
	r.With(s.authnOnlyMiddleware).Post("/v1/companies", s.handleCreateCompany)

	// Session bootstrap + invite claim. authn-ONLY for the same reason company
	// creation is: an INVITED user is not yet a member of anything, so the
	// {companyID} subtree would 403 them before they could ever claim the invite
	// that would make them a member. It is also the company list a client needs
	// to offer company switching (F3).
	r.With(s.authnOnlyMiddleware).Get("/v1/me", s.handleMe)

	r.Route("/v1/companies/{companyID}", func(r chi.Router) {
		// Every route in this subtree is authn'd + PDP-enforced.
		r.Use(s.companyAuthMiddleware)

		// Company master read-back (WS8a). The frontend hydrates
		// CompanyInfo.homeStateCode from state_code so the in-browser GST split
		// agrees with the server's derivation for non-Karnataka companies.
		// Handler lives in handler_company_get.go. chi resolves both
		// /v1/companies/{id} and /v1/companies/{id}/ to this route.
		r.Get("/", s.handleGetCompany)

		// Masters.
		r.Post("/groups", s.handleCreateGroup)
		r.Post("/ledgers", s.handleCreateLedger)
		// Read the chart of accounts back. Until this existed a client could
		// CREATE ledgers but never enumerate them, so it could not resolve the
		// id of a ledger it did not create itself — including the two ledgers
		// every company is seeded with (Cash, Profit & Loss A/c).
		r.Get("/ledgers", s.handleListLedgers)
		// Set a ledger's OPENING BALANCE + bill-wise opening breakup. Narrow by
		// design (not a general master editor): it is the only way to give the
		// ledgers seeded with every company — Cash, Profit & Loss A/c — an
		// opening balance, since those are created before any client can speak.
		r.Put("/ledgers/{ledgerID}/opening", s.handleSetLedgerOpening)
		r.Post("/voucher-types", s.handleCreateVoucherType)
		r.Post("/units", s.handleCreateUnit)
		r.Post("/stock-items", s.handleCreateStockItem)

		// Inventory masters (wave-1 INV-F). Godowns (stock locations) and
		// batches/lots are masters like ledgers are: routeAction maps every
		// path in isMasterPath to (Create, "master") on POST and
		// (Display, "master") on GET, so ONE ACL rule governs stock masters and
		// accounting masters alike.
		//
		// The READS are what make the F9 item grid usable at all. Until they
		// existed a client could create a godown and receive its id, but could
		// never enumerate the 'Main Location' every company is seeded with —
		// the same hole that made the demo seed mint a duplicate cash ledger
		// (see handler_ledgers_list.go). GET /stock-items and GET /units close
		// the matching hole for the two masters that already had a POST.
		//
		// No {id} sub-route is registered under either collection, so there is
		// no chi literal-vs-parameter collision to order around.
		r.Get("/godowns", s.handleListGodowns)
		r.Post("/godowns", s.handleCreateGodown)
		r.Get("/batches", s.handleListStockBatches)
		r.Post("/batches", s.handleCreateStockBatch)
		r.Get("/stock-items", s.handleListStockItems)
		r.Get("/units", s.handleListUnits)

		// Vouchers: one endpoint per accounting class (F4-F9). The body is the
		// same generic voucher shape; the path fixes the class.
		r.Post("/vouchers/sales", s.postVoucher(ledger.Sales))
		r.Post("/vouchers/purchase", s.postVoucher(ledger.Purchase))
		r.Post("/vouchers/receipt", s.postVoucher(ledger.Receipt))
		r.Post("/vouchers/payment", s.postVoucher(ledger.Payment))
		r.Post("/vouchers/contra", s.postVoucher(ledger.Contra))
		r.Post("/vouchers/journal", s.postVoucher(ledger.Journal))
		// Credit Note (sales return) / Debit Note (purchase adjustment). The
		// CORRECT way to reverse part of an invoice: a separately-dated voucher
		// with its own number, rather than an alteration that rewrites history.
		r.Post("/vouchers/credit-note", s.postVoucher(ledger.CreditNote))
		r.Post("/vouchers/debit-note", s.postVoucher(ledger.DebitNote))

		// The NON-ACCOUNTING stock vouchers (INV-K): goods move, nothing is
		// posted. A warehouse operator lives in these screens — stock leaves
		// hours or weeks before the billing department raises the invoice, and
		// a system that can only record a movement by also recording revenue
		// forces them to invent an invoice or key nothing.
		//
		// They reuse postVoucher/alterVoucher unchanged. That is the whole
		// point of the class being a compile-time constant: ledger.post()
		// branches on postsDoubleEntry(class), so the HTTP layer has no idea
		// these four are different and cannot get the distinction wrong.
		//
		// Physical Stock takes `physical_count` rather than `inventory` (see
		// voucherReq.PhysicalCount); the other three take the ordinary
		// `inventory` grid. Both arrive on the SAME body, so the sync/commit
		// path carries them for free.
		//
		// Migration 0031 seeds the ACL rules in the same change that mounts
		// these routes. Without it the seeded 'Data Entry' level (default_access
		// none) would 403 on every one of them and the feature would ship
		// reachable only by an Owner.
		r.Post("/vouchers/delivery-note", s.postVoucher(ledger.DeliveryNote))
		r.Post("/vouchers/receipt-note", s.postVoucher(ledger.ReceiptNote))
		r.Post("/vouchers/stock-journal", s.postVoucher(ledger.StockJournal))
		r.Post("/vouchers/physical-stock", s.postVoucher(ledger.PhysicalStock))

		// Alter: PUT a full restatement of an existing voucher. One Put per literal
		// type segment keeps the class compile-time fixed, exactly like the Posts.
		// {voucherID} carries the existing logical voucher id (never reallocated).
		r.Put("/vouchers/sales/{voucherID}", s.alterVoucher(ledger.Sales))
		r.Put("/vouchers/purchase/{voucherID}", s.alterVoucher(ledger.Purchase))
		r.Put("/vouchers/receipt/{voucherID}", s.alterVoucher(ledger.Receipt))
		r.Put("/vouchers/payment/{voucherID}", s.alterVoucher(ledger.Payment))
		r.Put("/vouchers/contra/{voucherID}", s.alterVoucher(ledger.Contra))
		r.Put("/vouchers/journal/{voucherID}", s.alterVoucher(ledger.Journal))
		r.Put("/vouchers/credit-note/{voucherID}", s.alterVoucher(ledger.CreditNote))
		r.Put("/vouchers/debit-note/{voucherID}", s.alterVoucher(ledger.DebitNote))

		// Alter the stock vouchers. A Physical Stock ALTER restates by resending
		// `physical_count` — never the movements GET projects — because the
		// delta is recomputed against a book that EXCLUDES this voucher's
		// superseded version. Resending the movement would compound the
		// correction instead of restating the count. Same rule as an item
		// invoice, which resends `inventory` rather than the postings it
		// generated.
		r.Put("/vouchers/delivery-note/{voucherID}", s.alterVoucher(ledger.DeliveryNote))
		r.Put("/vouchers/receipt-note/{voucherID}", s.alterVoucher(ledger.ReceiptNote))
		r.Put("/vouchers/stock-journal/{voucherID}", s.alterVoucher(ledger.StockJournal))
		r.Put("/vouchers/physical-stock/{voucherID}", s.alterVoucher(ledger.PhysicalStock))

		// Fetch the CURRENT (head) version of one logical voucher. A param route
		// beside the literal POST/PUT /vouchers/sales etc.; the method differs
		// (GET vs POST/PUT) so chi resolves it without collision, exactly as the
		// DELETE /vouchers/{voucherID} below already coexists with the literals.
		r.Get("/vouchers/{voucherID}", s.handleGetVoucher)

		// The EDIT LOG of one logical voucher: every hash-chained version plus a
		// verification of the chain. Reads THROUGH a tombstone, unlike the head
		// fetch above — a deletion is the most interesting entry an audit log
		// can carry. See handler_voucher_history.go.
		r.Get("/vouchers/{voucherID}/history", s.handleVoucherHistory)

		// Delete: append a tombstone. Class-agnostic — a delete needs only the id.
		r.Delete("/vouchers/{voucherID}", s.handleDeleteVoucher)

		// Freeze-on-external-reference: pinning an IRN / filed GSTR-1 to a
		// voucher freezes its number and hard-blocks later alter/delete. The
		// POST is the shared seam statutoryd calls at IRN time; the GET lists
		// open reconciliation entries (refused alter/delete attempts).
		r.Post("/vouchers/{voucherID}/external-reference", s.handleRegisterExternalReference)
		r.Get("/reconciliation-queue", s.handleReconciliationQueue)

		// Backward-compatible alias for the original F8 Sales endpoint.
		r.Post("/sales", s.postVoucher(ledger.Sales))

		// Reports.
		r.Get("/trial-balance", s.handleTrialBalance)
		r.Get("/balance-sheet", s.handleBalanceSheet)
		r.Get("/profit-and-loss", s.handleProfitAndLoss)
		r.Get("/day-book", s.handleDayBook)
		r.Get("/outstandings", s.handleOutstandings)
		// Forecast/anomaly reports (wave-2 WS5a).
		r.Get("/cash-flow", s.handleCashFlow)
		r.Get("/anomalies", s.handleAnomalies)
		// The Stock Summary (wave-1 INV-F): quantity from the movement ledger,
		// VALUE from a replay through the pure valuer. It is a report and
		// nothing it prints is stored, which is why it sits here beside the
		// financial statements rather than beside the masters above.
		r.Get("/stock-summary", s.handleStockSummary)
		// The rest of the inventory reporting surface (handler_stock_reports.go).
		// Every one of them is a literal segment whose name IS its report name in
		// authz.KnownReportNames — TestReportVocabularyMatchesRouter walks the
		// vocabulary the other way and would fail on any report reached by a path
		// that does not spell its own name. That is also why the item drill takes
		// stock_item_id as a query param rather than sitting under
		// /stock-items/{itemID}: as a literal sibling of the /stock-items
		// collection it would additionally need registering before any {param}
		// sibling to avoid a chi collision, and it would still break the closure.
		//
		// Three of them are POSITIONS or PERIODS of the same replay the Stock
		// Summary runs; none of what they print is stored. All four carry VALUE
		// (cost), which is why migration 0028 disallows all four for Data Entry.
		r.Get("/stock-movement", s.handleStockMovement)
		r.Get("/godown-summary", s.handleGodownSummary)
		r.Get("/batch-summary", s.handleBatchSummary)
		r.Get("/stock-item-vouchers", s.handleStockItemVouchers)

		// GST.
		r.Get("/gstr1", s.handleGSTR1)
		r.Get("/gstr3b", s.handleGSTR3B)

		// INPUT TAX CREDIT: the books-vs-GSTR-2B reconciliation, the IMS
		// worklist, and the three writes that feed them (handler_itc.go).
		//
		// THE TWO REPORT SEGMENTS ARE FLAT LITERALS SPELLING THEIR OWN REPORT
		// NAMES, and they have to be: migration 0033 already seeds
		// `disallow display report:itc-reconciliation` and `report:ims-inbox`
		// for the seeded Data Entry level, and TestReportVocabularyMatchesRouter
		// requires GET /v1/companies/{id}/<name> to resolve for every name in
		// authz.KnownReportNames. A nested or renamed path would leave those two
		// seeded rows matching nothing — an administrator's `disallow` that
		// silently never fires, which is the one failure direction the closed
		// vocabulary exists to prevent. An older handoff naming
		// /gstr2b-reconciliation and /ims-worklist is wrong for that reason.
		//
		// The three POSTs are NOT reports and each carries its own resource
		// token in routeAction (inward-statement / ims-action / gst-filing).
		// They need no seeded rule: Data Entry's default_access is 'none', so a
		// token no rule names is already denied — see migration 0033's header,
		// which says so explicitly and deliberately seeds only the two reports.
		r.Get("/itc-reconciliation", s.handleITCReconciliation)
		r.Get("/ims-inbox", s.handleIMSInbox)
		r.Post("/gstr2b-import", s.handleGSTR2BImport)
		r.Post("/ims-actions", s.handleIMSAction)
		r.Post("/gst-filings", s.handleGSTFiling)

		// PAYROLL (handler_payroll.go, migrations 0034-0036).
		//
		// The employee master gets its OWN resource token rather than joining
		// isMasterPath: Data Entry holds `allow display master` so the party
		// picker works, and an employee row carries PAN, UAN, ESIC IP number and
		// bank IFSC. See authz.KnownResources' "employee" entry.
		//
		// The pay run rides /vouchers/payroll like any other class, so the PDP
		// mapping, the back-dated cutoff and the WebAuthn step-up reach it
		// through the same code that reaches an invoice. It is a literal sibling
		// of the other /vouchers/{class} POSTs and of GET /vouchers/{voucherID};
		// chi's trie sorts static nodes ahead of param nodes at match time, and
		// the methods differ besides, so there is no collision to order around.
		//
		// The five reports are flat literals equal to their report names, for
		// the same reason the two ITC reports are — migration 0035's header
		// writes them as ".../reports/payroll-register", and that nesting is
		// illustrative and wrong. 0035 seeds all five `disallow display` rows
		// plus the six for the two bare tokens, one wave BEFORE these routes, so
		// the day the surface appears the operator is refused rather than
		// admitted by 0011's coarse `allow display report`.
		r.Post("/employees", s.handleCreateEmployee)
		r.Get("/employees", s.handleListEmployees)
		r.Post("/vouchers/payroll", s.handlePayrollRun)
		r.Get("/payroll-register", s.handlePayrollRegister)
		r.Get("/payroll-payslip", s.handlePayrollPayslip)
		r.Get("/payroll-form-138", s.handlePayrollForm138)
		r.Get("/payroll-pf-ecr", s.handlePayrollPFECR)
		r.Get("/payroll-esi-return", s.handlePayrollESIReturn)

		// Price lists (INV-L, migration 0030): handler_price_list.go.
		//
		// /price-lists/quote is a LITERAL sibling of the {priceListID} param
		// route, and it is registered first so a reader meets the specific case
		// before the general one. BE PRECISE ABOUT WHAT THAT BUYS: it is
		// documentation, NOT the defence. chi's trie sorts static nodes ahead of
		// param nodes at MATCH time, so the two resolve correctly in either
		// order — this was verified by swapping these two lines and watching
		// TestPriceListRoutesAreOwnTokenAndDataEntryMayReadNotDefine stay green.
		// (/security-levels/vocabulary carries a similar note; there the
		// collision could not arise at all, because {levelID} has no GET.)
		//
		// What genuinely protects the quote route is that {priceListID} is
		// parsed as a UUID: a request for a list literally named "quote" is a
		// 400, not a silent mis-route. The real hazard to watch for is the
		// opposite one — adding a THIRD literal under /price-lists whose name a
		// caller might also use as an id.
		//
		// The PDP does not care either way: isPriceListPath matches the SHAPE,
		// so both paths emit (Display, "price-list") and neither can slip
		// through routeAction's default-deny.
		r.Get("/price-lists/quote", s.handlePriceQuote)
		r.Get("/price-lists/{priceListID}", s.handleGetPriceList)
		r.Post("/price-lists", s.handleCreatePriceList)

		// Migration import (Tally-XML ingress).
		r.Post("/import/tally-xml", s.handleImportTallyXML)

		// Offline cloud-sequencer commit (Task C): a desktop node POSTs its
		// outbox of voucher intents; the sequencer assigns final numbers,
		// resolves conflicts, and hard-blocks superseding frozen vouchers.
		r.Post("/sync/commit", s.handleSyncCommit)

		// SaaS admin surface (handler_admin.go): members, invites and custom
		// security levels. Every one maps to the "security" resource in
		// routeAction, so an Owner administers users while a Data Entry level
		// (default_access none, no `security` rule) is refused by the PDP — and
		// the writes inherit the Owner level's WebAuthn step-up.
		r.Get("/members", s.handleListMembers)
		r.Post("/members/invites", s.handleInviteMember)
		r.Delete("/members/invites/{inviteID}", s.handleRevokeInvite)
		r.Put("/members/{userID}", s.handleUpdateMember)
		r.Delete("/members/{userID}", s.handleRemoveMember)
		r.Get("/security-levels", s.handleListSecurityLevels)
		// The CLOSED rule vocabulary, so a browser rule editor offers exactly the
		// tokens the server will accept instead of a TypeScript copy that drifts.
		// A literal segment, so it cannot collide with {levelID} (which has no GET).
		r.Get("/security-levels/vocabulary", s.handleSecurityVocabulary)
		r.Post("/security-levels", s.handleCreateSecurityLevel)
		r.Put("/security-levels/{levelID}", s.handleUpdateSecurityLevel)
		r.Delete("/security-levels/{levelID}", s.handleDeleteSecurityLevel)

		// Conversational query (Phase 3 flagship): ask the books a question in
		// English (or POST a typed DSL) and get a typed, read-only answer. The
		// handler runs under companyAuthMiddleware (PDP maps POST /query ->
		// Display/query) and executes via the read-only, RLS-scoped pool.
		r.Post("/query", s.handleQuery)
	})

	s.router = r
}

// --------------------------------------------------------------------------
// Health
// --------------------------------------------------------------------------

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.pool.Ping(r.Context()); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "db_unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --------------------------------------------------------------------------
// Dev auth: POST /auth/token
// --------------------------------------------------------------------------

// devTokenReq is the dev /auth/token body. It mints a token for an arbitrary
// subject (the deterministic fake issuer; real Keycloak is the documented swap).
// amr may carry "webauthn" to simulate a stepped-up token for the assert
// ceremony. ttl_seconds defaults to 1 hour.
type devTokenReq struct {
	Subject    string   `json:"subject"`
	Email      string   `json:"email"`
	AMR        []string `json:"amr"`
	TTLSeconds int64    `json:"ttl_seconds"`
}

// handleDevToken mints a signed JWT for the supplied subject using the dev
// signer. It is the seam tests + e2e scripts use to obtain an Owner token. A
// service token is requested by passing subject "svc:<name>".
func (s *Server) handleDevToken(w http.ResponseWriter, r *http.Request) {
	// Defense in depth: this handler must never run without the dev signer.
	// routes() only mounts it in dev mode, but guard here too.
	if s.signer == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	var req devTokenReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Subject == "" {
		writeError(w, http.StatusBadRequest, "subject is required")
		return
	}
	ttl := req.TTLSeconds
	if ttl <= 0 {
		ttl = 3600
	}
	now := time.Now()
	tok, err := s.signer.Sign(authn.Claims{
		Subject:   req.Subject,
		Email:     req.Email,
		AMR:       req.AMR,
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(time.Duration(ttl) * time.Second).Unix(),
		ID:        uuid.NewString(),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "sign token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      tok,
		"token_type": "Bearer",
		"expires_in": ttl,
	})
}

// --------------------------------------------------------------------------
// Company
// --------------------------------------------------------------------------

type companyReq struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	FYStartDate  string `json:"fy_start_date"`
	BaseCurrency string `json:"base_currency"`
	StateCode    string `json:"state_code"`
	GSTIN        string `json:"gstin"`

	// Mailing block + country (migration 0015). All optional; omitted fields
	// take the column/service defaults.
	MailingName string `json:"mailing_name"`
	Address     string `json:"address"`
	City        string `json:"city"`
	Pincode     string `json:"pincode"`
	Country     string `json:"country"`

	// BooksBeginningFrom defaults to FYStartDate when omitted. Supply it only
	// for a mid-year migration; it may not precede fy_start_date (DB CHECK).
	BooksBeginningFrom string `json:"books_beginning_from"`

	// Base-currency presentation. DecimalPlaces is a pointer so an explicit 0
	// (whole-rupee books) is distinguishable from "not supplied" (=> 2).
	CurrencySymbol     string `json:"currency_symbol"`
	CurrencyFormalName string `json:"currency_formal_name"`
	DecimalPlaces      *int   `json:"decimal_places"`
}

func (s *Server) handleCreateCompany(w http.ResponseWriter, r *http.Request) {
	var req companyReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id := uuid.New()
	if req.ID != "" {
		parsed, err := uuid.Parse(req.ID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid company id")
			return
		}
		id = parsed
	}
	// CreateCompany requires the context to be scoped to the same company id
	// (the insert is itself subject to RLS).
	ctx := db.WithCompany(r.Context(), id)
	created, err := s.identity.CreateCompany(ctx, identity.Company{
		ID:           id,
		Name:         req.Name,
		FYStartDate:  req.FYStartDate,
		BaseCurrency: req.BaseCurrency,
		StateCode:    req.StateCode,
		GSTIN:        req.GSTIN,

		MailingName: req.MailingName,
		Address:     req.Address,
		City:        req.City,
		Pincode:     req.Pincode,
		Country:     req.Country,

		BooksBeginningFrom: req.BooksBeginningFrom,

		CurrencySymbol:     req.CurrencySymbol,
		CurrencyFormalName: req.CurrencyFormalName,
		DecimalPlaces:      req.DecimalPlaces,
	})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	// Seed the Owner security level + make the creating user an Owner member, so
	// subsequent {companyID} requests authorize. The user was resolved by the
	// authn-only middleware (create-on-first-login) and is in the context.
	if userID, ok := db.UserFromContext(r.Context()); ok {
		if err := s.authzSeed.SeedCompanyAuthDefaults(r.Context(), created, userID); err != nil {
			writeError(w, http.StatusInternalServerError, "seed company auth: "+err.Error())
			return
		}
	}

	writeJSON(w, http.StatusCreated, map[string]string{"company_id": created.String()})
}

// --------------------------------------------------------------------------
// Masters
// --------------------------------------------------------------------------

type groupReq struct {
	Name               string `json:"name"`
	ParentName         string `json:"parent_name"`
	Nature             string `json:"nature"`
	IsPL               bool   `json:"is_pl"`
	BehavesAsSubledger bool   `json:"behaves_as_subledger"`
	CreatedBy          string `json:"created_by"`
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req groupReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.masters.CreateGroup(ctx, masters.Group{
		Name:               req.Name,
		ParentName:         req.ParentName,
		Nature:             masters.Nature(req.Nature),
		IsPL:               req.IsPL,
		BehavesAsSubledger: req.BehavesAsSubledger,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"group_id": id.String()})
}

type ledgerReq struct {
	Name               string `json:"name"`
	GroupName          string `json:"group_name"`
	OpeningBalance     string `json:"opening_balance"`
	OpeningDC          string `json:"opening_dc"`
	MaintainBillByBill bool   `json:"maintain_bill_by_bill"`
	GSTIN              string `json:"gstin"`
	StateCode          string `json:"state_code"`
	RegistrationType   string `json:"registration_type"`
	PAN                string `json:"pan"`
	CreatedBy          string `json:"created_by"`
	// OpeningBills is the bill-wise breakup of OpeningBalance. masters.CreateLedger
	// requires it to SUM to the opening balance when present.
	OpeningBills []openingBillReq `json:"opening_bills"`
}

// openingBillReq is one bill of a ledger's opening bill-wise breakup.
type openingBillReq struct {
	RefName string `json:"ref_name"`
	RefType string `json:"ref_type"`
	DrCr    string `json:"dr_cr"`
	Amount  string `json:"amount"`
	DueDate string `json:"due_date"`
}

func (s *Server) handleCreateLedger(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req ledgerReq
	if !decodeJSON(w, r, &req) {
		return
	}
	opening, err := optionalDecimal(req.OpeningBalance)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid opening_balance")
		return
	}
	openingBills := make([]masters.OpeningBill, 0, len(req.OpeningBills))
	for _, b := range req.OpeningBills {
		amt, derr := optionalDecimal(b.Amount)
		if derr != nil {
			writeError(w, http.StatusBadRequest, "invalid opening bill amount for "+b.RefName)
			return
		}
		openingBills = append(openingBills, masters.OpeningBill{
			RefName: b.RefName, RefType: b.RefType, DrCr: b.DrCr,
			Amount: amt, DueDate: b.DueDate,
		})
	}
	id, err := s.masters.CreateLedger(ctx, masters.Ledger{
		Name:               req.Name,
		GroupName:          req.GroupName,
		OpeningBalance:     opening,
		OpeningDC:          req.OpeningDC,
		MaintainBillByBill: req.MaintainBillByBill,
		GSTIN:              req.GSTIN,
		StateCode:          req.StateCode,
		RegistrationType:   masters.GSTRegistrationType(req.RegistrationType),
		PAN:                req.PAN,
		OpeningBills:       openingBills,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"ledger_id": id.String()})
}

type voucherTypeReq struct {
	Name            string `json:"name"`
	ParentClass     string `json:"parent_class"`
	NumberingMethod string `json:"numbering_method"`
	AffectsStock    bool   `json:"affects_stock"`
	IsAccounting    bool   `json:"is_accounting"`
	CreatedBy       string `json:"created_by"`
}

func (s *Server) handleCreateVoucherType(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req voucherTypeReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.masters.CreateVoucherType(ctx, masters.VoucherType{
		Name:            req.Name,
		ParentClass:     req.ParentClass,
		NumberingMethod: req.NumberingMethod,
		AffectsStock:    req.AffectsStock,
		IsAccounting:    req.IsAccounting,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"voucher_type_id": id.String()})
}

type unitReq struct {
	Symbol        string `json:"symbol"`
	FormalName    string `json:"formal_name"`
	DecimalPlaces int    `json:"decimal_places"`
	CreatedBy     string `json:"created_by"`
}

func (s *Server) handleCreateUnit(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req unitReq
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.masters.CreateUnit(ctx, masters.Unit{
		Symbol:        req.Symbol,
		FormalName:    req.FormalName,
		DecimalPlaces: req.DecimalPlaces,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"unit_id": id.String()})
}

type stockItemReq struct {
	Name        string `json:"name"`
	UnitSymbol  string `json:"unit_symbol"`
	HSN         string `json:"hsn"`
	GSTRatePct  string `json:"gst_rate_pct"`
	OpeningQty  string `json:"opening_qty"`
	OpeningRate string `json:"opening_rate"`
	CreatedBy   string `json:"created_by"`
}

func (s *Server) handleCreateStockItem(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req stockItemReq
	if !decodeJSON(w, r, &req) {
		return
	}
	rate, err := optionalDecimal(req.GSTRatePct)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid gst_rate_pct")
		return
	}
	qty, err := optionalDecimal(req.OpeningQty)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid opening_qty")
		return
	}
	openRate, err := optionalDecimal(req.OpeningRate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid opening_rate")
		return
	}
	id, err := s.masters.CreateStockItem(ctx, masters.StockItem{
		Name:        req.Name,
		UnitSymbol:  req.UnitSymbol,
		HSN:         req.HSN,
		GSTRatePct:  rate,
		OpeningQty:  qty,
		OpeningRate: openRate,
	}, req.CreatedBy)
	if err != nil {
		writeMastersError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"stock_item_id": id.String()})
}

// --------------------------------------------------------------------------
// Vouchers
// --------------------------------------------------------------------------

type postingReq struct {
	LedgerID string `json:"ledger_id"`
	DrCr     string `json:"dr_cr"`
	Amount   string `json:"amount"`
}

type billAllocationReq struct {
	LedgerID string `json:"ledger_id"`
	RefName  string `json:"ref_name"`
	RefType  string `json:"ref_type"`
	DrCr     string `json:"dr_cr"`
	Amount   string `json:"amount"`
	DueDate  string `json:"due_date"`
}

type gstLineReq struct {
	TaxableValue string `json:"taxable_value"`
	GSTRatePct   string `json:"gst_rate_pct"`
	HSN          string `json:"hsn"`
}

type gstReq struct {
	RulesetVersion         string       `json:"ruleset_version"`
	SupplierStateCode      string       `json:"supplier_state_code"`
	PlaceOfSupplyStateCode string       `json:"place_of_supply_state_code"`
	ReverseCharge          bool         `json:"reverse_charge"`
	Exempt                 bool         `json:"exempt"`
	Lines                  []gstLineReq `json:"lines"`
	CGSTLedgerID           string       `json:"cgst_ledger_id"`
	SGSTLedgerID           string       `json:"sgst_ledger_id"`
	IGSTLedgerID           string       `json:"igst_ledger_id"`
}

// inventoryLineReq is ONE line of the F8/F9 item grid on the wire. It mirrors
// ledger.InventoryLine field-for-field, and every numeric field is a STRING for
// the same reason every money field on this surface is: a JSON number is a
// float64, and a float quantity makes a closing stock balance depend on the
// order the lines were entered. Quantities are money here.
//
// godown_id and batch_id are OPTIONAL: "" means "not tracked on this line" and
// becomes uuid.Nil, which the post path writes as SQL NULL. A company that never
// enables multi-location or batches is not forced to invent master rows.
//
// amount is taken AS ENTERED and is never recomputed from qty * rate — Tally
// lets a user override a line total, and silently "correcting" it would make the
// printed invoice disagree with the stored one.
type inventoryLineReq struct {
	StockItemID string `json:"stock_item_id"`
	GodownID    string `json:"godown_id"`
	BatchID     string `json:"batch_id"`
	// Direction is "IN" or "OUT"; qty is ALWAYS positive and the direction
	// carries the sign. Validation of the token itself belongs to the ledger
	// (validateInventory), so an unknown direction is a 422 with a domain
	// message rather than a 400 from a second, drifting copy of the rule here.
	Direction string `json:"direction"`
	Qty       string `json:"qty"`
	// Rate is a *string so ABSENT is distinguishable from "0".
	//
	// An omitted rate means "price this for me from the effective price list";
	// a rate of "0" is a STATED fact — a free sample — and must never be
	// silently repriced. A plain string cannot tell the two apart, because
	// encoding/json leaves a missing field as "", which is also what a client
	// sends when it means nothing in particular. Without this pointer no HTTP
	// caller could ever produce an unpriced line and the price list, which the
	// ledger already consults inside post()'s transaction, would be reachable
	// only from Go.
	Rate        *string `json:"rate"`
	DiscountPct string  `json:"discount_pct"`
	Amount      string  `json:"amount"`
}

// physicalCountLineReq is ONE counted position on a Physical Stock voucher:
// "there are Qty of this item, in this godown, from this batch, on the shelf".
//
// IT HAS NO `direction` AND THAT IS THE POINT. A count carries no sign — the
// sign of the correction is DERIVED inside the post transaction by differencing
// the count against the book position. Offering a direction here would let a
// client state a delta it had computed itself, against a book it had read
// earlier, and a stock-take is exactly the moment two readers race.
//
// Qty may legitimately be "0": "the shelf is empty" is a count, and it is the
// one an auditor most wants recorded. Rate is required (non-zero) only when the
// count EXCEEDS the books — a surplus brought in at zero cost writes the item's
// weighted average down, so the ledger refuses it with a domain message rather
// than silently devaluing the warehouse.
type physicalCountLineReq struct {
	StockItemID string `json:"stock_item_id"`
	GodownID    string `json:"godown_id"`
	BatchID     string `json:"batch_id"`
	Qty         string `json:"qty"`
	Rate        string `json:"rate"`
}

type voucherReq struct {
	VoucherTypeID string `json:"voucher_type_id"`
	PartyLedgerID string `json:"party_ledger_id"`
	// ItemLedgerID is the revenue/expense ledger an ITEM INVOICE books the goods
	// to: the Sales Accounts ledger on F8, the Purchase Accounts ledger on F9.
	// It sits BESIDE party_ledger_id rather than inside the gst block because it
	// is an accounting fact, not a tax one: a stock-only document (a Delivery
	// Note) has no item ledger and a service invoice has no goods to book.
	//
	// LOAD-BEARING FOR THE WHOLE ITEM-INVOICE FEATURE: ledger.validateItemInvoice
	// refuses an item invoice whose ItemLedgerID is uuid.Nil, so until this field
	// existed on the wire EVERY item invoice the F8/F9 grid could send came back
	// 422 "an item invoice needs an item ledger" — the generator was reachable
	// only from Go tests. Empty is legal and means "not an item invoice"; the
	// ledger decides which shape a body is (isItemInvoice), never this struct.
	ItemLedgerID    string              `json:"item_ledger_id"`
	VoucherNo       string              `json:"voucher_no"`
	VoucherDate     string              `json:"voucher_date"`
	Narration       string              `json:"narration"`
	Party           string              `json:"party"`
	CreatedBy       string              `json:"created_by"`
	Postings        []postingReq        `json:"postings"`
	BillAllocations []billAllocationReq `json:"bill_allocations"`
	// Inventory is the item grid. Declared HERE — after bill_allocations and
	// before gst — to mirror ledger.VoucherInput's field order, so the wire
	// shape and the domain shape read the same way top to bottom.
	//
	// It is carried on THIS type, not on a parallel struct, which is what makes
	// POST /sync/commit carry stock for free: syncIntentReq.Payload IS a
	// voucherReq and buildVoucherInput is the single translator. A second
	// inventory type on the sync wire would compile, decode and then silently
	// drop every stock line an offline node ever recorded.
	Inventory []inventoryLineReq `json:"inventory"`
	// PhysicalCount is the Physical Stock voucher's body: the COUNTED
	// positions, not movements. It sits beside Inventory rather than replacing
	// it because the two are mutually exclusive per class and the LEDGER owns
	// that rule (validateStockVoucher refuses `inventory` on a Physical Stock
	// and refuses `physical_count` on anything else). Deciding it here as well
	// would be a second copy of the rule, and the drifted copy always wins for
	// whichever caller reaches it first.
	//
	// Carried on THIS type, like Inventory, so POST /sync/commit ships a
	// stock-take from an offline node for free: syncIntentReq.Payload IS a
	// voucherReq and buildVoucherInput is the single translator.
	PhysicalCount []physicalCountLineReq `json:"physical_count"`
	GST           *gstReq                `json:"gst"`
	TDS           *tdsReq                `json:"tds"`
}

// postVoucher returns a handler bound to a fixed accounting class. The same
// generic body posts any of the six classes; the path determines the class.
func (s *Server) postVoucher(class ledger.Class) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		var req voucherReq
		if !decodeJSON(w, r, &req) {
			return
		}
		in, ok := buildVoucherInput(w, class, req)
		if !ok {
			return
		}
		res, err := s.ledger.PostVoucher(ctx, in)
		if err != nil {
			if errors.Is(err, db.ErrNoCompany) {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, voucherResultJSON(res))
	}
}

// alterVoucher returns a handler bound to a fixed accounting class that appends
// an ALTER version to an existing logical voucher. It mirrors postVoucher: the
// class is fixed by the path, the {voucherID} segment carries the existing
// logical voucher id, and the body is a FULL restatement (all postings resent).
// The supplied voucher_no/voucher_date are stamped verbatim, so a client that
// resends the loaded number/date preserves them (Tally never auto-renumbers on
// alter).
func (s *Server) alterVoucher(class ledger.Class) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := s.scopeCtx(w, r)
		if !ok {
			return
		}
		voucherID, err := uuid.Parse(chi.URLParam(r, "voucherID"))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid voucher id")
			return
		}
		var req voucherReq
		if !decodeJSON(w, r, &req) {
			return
		}
		in, ok := buildVoucherInput(w, class, req)
		if !ok {
			return
		}
		res, err := s.ledger.AlterVoucher(ctx, voucherID, in)
		if err != nil {
			writeVoucherEditError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, voucherResultJSON(res))
	}
}

// handleDeleteVoucher appends a DELETE tombstone to an existing logical voucher.
// A delete needs only the logical id (the class is already recorded on the prior
// versions), so the route is class-agnostic. The optional ?created_by= query
// param stamps the deleting user onto the tombstone, consistent with the
// Tally-XML import endpoint. The tombstone reverses the voucher out of every
// balance and report while its versions remain in the audit log.
func (s *Server) handleDeleteVoucher(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	voucherID, err := uuid.Parse(chi.URLParam(r, "voucherID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid voucher id")
		return
	}
	createdBy := r.URL.Query().Get("created_by")
	res, err := s.ledger.DeleteVoucher(ctx, voucherID, createdBy)
	if err != nil {
		writeVoucherEditError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"voucher_id":         res.VoucherID.String(),
		"voucher_version_id": res.VoucherVersionID.String(),
		"version_no":         res.VersionNo,
		"voucher_no":         res.VoucherNo,
		"op":                 string(ledger.OpDelete),
	})
}

// externalRefReq is the body of the freeze-hook POST: the external reference to
// pin to a voucher. created_by is optional (stamps the registering actor).
type externalRefReq struct {
	Kind      string `json:"kind"`
	RefValue  string `json:"ref_value"`
	CreatedBy string `json:"created_by"`
}

// handleRegisterExternalReference pins a voucher's display number to an external
// reference (IRN / filed GSTR-1), freezing it against later alter/delete. It is
// the shared seam statutoryd calls at IRN time. Idempotent on
// (voucher,kind,ref_value): a re-POST returns the SAME pinned number. An unknown
// or deleted voucher is a 404.
func (s *Server) handleRegisterExternalReference(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	voucherID, err := uuid.Parse(chi.URLParam(r, "voucherID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid voucher id")
		return
	}
	var req externalRefReq
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Kind != "IRN" && req.Kind != "FILED_GSTR1" {
		writeError(w, http.StatusBadRequest, "kind must be IRN or FILED_GSTR1")
		return
	}
	if req.RefValue == "" {
		writeError(w, http.StatusBadRequest, "ref_value is required")
		return
	}
	res, err := s.ledger.RegisterExternalReference(ctx, voucherID, req.Kind, req.RefValue, req.CreatedBy)
	if err != nil {
		switch {
		case errors.Is(err, ledger.ErrVoucherNotFound):
			writeError(w, http.StatusNotFound, "voucher not found")
		case errors.Is(err, db.ErrNoCompany):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"voucher_id":        res.VoucherID.String(),
		"kind":              res.Kind,
		"ref_value":         res.Ref,
		"pinned_voucher_no": res.PinnedVoucherNo,
	})
}

// handleReconciliationQueue lists the OPEN reconciliation entries — refused
// alter/delete attempts against frozen vouchers — newest first.
func (s *Server) handleReconciliationQueue(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	rows, err := s.ledger.ListReconciliationQueue(ctx)
	if err != nil {
		if errors.Is(err, db.ErrNoCompany) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		entries = append(entries, map[string]any{
			"id":                e.ID.String(),
			"voucher_id":        e.VoucherID.String(),
			"attempted_op":      e.AttemptedOp,
			"kind":              e.Kind,
			"ref_value":         e.Ref,
			"frozen_voucher_no": e.FrozenVoucherNo,
			"detail":            e.Detail,
			"created_at":        e.CreatedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}

// handleGetVoucher returns the CURRENT (head) version of one logical voucher.
// Its postings carry the REAL ledger ids (not names), so a frontend alter path
// can re-seed a form from the fetched voucher without smuggling a name into a
// uuid field. An unknown id OR a tombstoned (is_deleted) head both surface as a
// 404 — the ledger excludes deleted heads from the head join.
func (s *Server) handleGetVoucher(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	voucherID, err := uuid.Parse(chi.URLParam(r, "voucherID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid voucher id")
		return
	}
	v, err := s.ledger.GetVoucher(ctx, voucherID)
	if err != nil {
		// A read endpoint: an unknown/deleted voucher is 404 and a missing
		// company scope is 400, but an unexpected error is a 500 (not the 422
		// the write path uses for validation failures).
		switch {
		case errors.Is(err, ledger.ErrVoucherNotFound):
			writeError(w, http.StatusNotFound, "voucher not found")
		case errors.Is(err, db.ErrNoCompany):
			writeError(w, http.StatusBadRequest, err.Error())
		default:
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, voucherFullJSON(v))
}

// voucherFullJSON serialises a fetched voucher to the documented GET shape:
// money as fixed-4 strings, dr_cr canonical "D"/"C", optional uuids rendered as
// "" when nil, and postings/bill_allocations as nested arrays.
func voucherFullJSON(v ledger.VoucherView) map[string]any {
	postings := make([]map[string]any, 0, len(v.Postings))
	for _, p := range v.Postings {
		postings = append(postings, map[string]any{
			"ledger_id": p.LedgerID.String(),
			"ledger":    p.LedgerName,
			"dr_cr":     p.DrCr,
			"amount":    p.Amount.StringFixed(4),
		})
	}
	bills := make([]map[string]any, 0, len(v.BillAllocations))
	for _, b := range v.BillAllocations {
		bills = append(bills, map[string]any{
			"ledger_id": b.LedgerID.String(),
			"ref_name":  b.RefName,
			"ref_type":  b.RefType,
			"dr_cr":     b.DrCr,
			"amount":    b.Amount.StringFixed(4),
			"due_date":  b.DueDate,
		})
	}
	// The item grid. Built by inventoryLinesJSON (handler_voucher_inventory.go),
	// which is where the reasoning about the round trip lives.
	inventory := inventoryLinesJSON(v.InventoryLines)
	return map[string]any{
		"voucher_id":       v.VoucherID.String(),
		"voucher_no":       v.VoucherNo,
		"voucher_date":     v.VoucherDate,
		"class":            v.Class,
		"voucher_type_id":  uuidOrEmpty(v.VoucherTypeID),
		"party":            v.Party,
		"party_ledger_id":  uuidOrEmpty(v.PartyLedgerID),
		"narration":        v.Narration,
		"version_no":       v.VersionNo,
		"op":               v.Op,
		"place_of_supply":  v.PlaceOfSupply,
		"reverse_charge":   v.ReverseCharge,
		"taxable_value":    v.Taxable.StringFixed(4),
		"cgst":             v.CGST.StringFixed(4),
		"sgst":             v.SGST.StringFixed(4),
		"igst":             v.IGST.StringFixed(4),
		"postings":         postings,
		"bill_allocations": bills,
		"inventory":        inventory,
	}
}

// uuidOrEmpty renders a uuid as its canonical string, or "" when nil, so an
// absent optional id serialises as an empty string rather than the all-zero uuid.
func uuidOrEmpty(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// voucherResultJSON builds the response body shared by the POST and PUT voucher
// handlers so the shape never drifts between create and alter.
func voucherResultJSON(res ledger.PostResult) map[string]any {
	out := map[string]any{
		"voucher_id":         res.VoucherID.String(),
		"voucher_version_id": res.VoucherVersionID.String(),
		"version_no":         res.VersionNo,
		"voucher_no":         res.VoucherNo,
		"cgst":               res.Tax.CGST.StringFixed(4),
		"sgst":               res.Tax.SGST.StringFixed(4),
		"igst":               res.Tax.IGST.StringFixed(4),
	}
	// TDS rides back the same way GST does, but only when derivation actually
	// ran: Basis is stamped from the matched section rule, so an empty Basis
	// means the post carried no `tds` block and the key stays absent (existing
	// clients see a byte-identical body).
	if res.TDS.Basis != "" {
		out["tds"] = map[string]any{
			"tds":       res.TDS.TDS.StringFixed(4),
			"rate_pct":  res.TDS.RatePct.StringFixed(4),
			"threshold": res.TDS.Threshold.StringFixed(4),
			"basis":     string(res.TDS.Basis),
			"applied":   res.TDS.Applied,
		}
	}
	return out
}

// writeVoucherEditError maps an alter/delete error to an HTTP status: an unknown
// voucher is a 404, a missing company scope is a 400, and anything else
// (unbalanced postings, GST mismatch, ledger validation) is a 422 — the same
// mapping the POST path uses.
func writeVoucherEditError(w http.ResponseWriter, err error) {
	// A frozen voucher (pinned by an IRN / filed GSTR-1) is a 409 Conflict
	// carrying the blocking reference, so a client can show what pinned it.
	var fe *ledger.FrozenError
	if errors.As(err, &fe) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": fe.Error(),
			"frozen": map[string]any{
				"kind":              fe.Kind,
				"ref_value":         fe.Ref,
				"pinned_voucher_no": fe.VoucherNo,
			},
		})
		return
	}
	switch {
	case errors.Is(err, ledger.ErrVoucherNotFound):
		writeError(w, http.StatusNotFound, "voucher not found")
	case errors.Is(err, db.ErrNoCompany):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}

// buildVoucherInput converts a decoded voucherReq into a ledger.VoucherInput,
// writing a 400 and returning ok=false on any malformed field.
func buildVoucherInput(w http.ResponseWriter, class ledger.Class, req voucherReq) (ledger.VoucherInput, bool) {
	in := ledger.VoucherInput{
		Class:       class,
		VoucherNo:   req.VoucherNo,
		VoucherDate: req.VoucherDate,
		Narration:   req.Narration,
		Party:       req.Party,
		CreatedBy:   req.CreatedBy,
	}
	var err error
	if in.VoucherTypeID, err = optionalUUID(req.VoucherTypeID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid voucher_type_id")
		return in, false
	}
	if in.PartyLedgerID, err = optionalUUID(req.PartyLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid party_ledger_id")
		return in, false
	}
	// The item ledger of an ITEM INVOICE (see voucherReq.ItemLedgerID). Parsed
	// with optionalUUID like every other id on this body: "" is uuid.Nil, which
	// is what every non-item voucher sends. Whether a Nil here is an ERROR is
	// the ledger's decision, not this layer's — validateItemInvoice refuses it
	// only for a body that is actually an item invoice (inventory + gst on a
	// GST-bearing class), and duplicating that condition here would be a second
	// copy of the rule that decides what an item invoice IS.
	if in.ItemLedgerID, err = optionalUUID(req.ItemLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid item_ledger_id")
		return in, false
	}
	for _, p := range req.Postings {
		lid, err := uuid.Parse(p.LedgerID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid posting ledger_id")
			return in, false
		}
		amt, err := decimal.NewFromString(p.Amount)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid posting amount")
			return in, false
		}
		in.Postings = append(in.Postings, ledger.PostingInput{
			LedgerID: lid,
			DrCr:     ledger.DrCr(p.DrCr),
			Amount:   amt,
		})
	}
	for _, b := range req.BillAllocations {
		lid, err := uuid.Parse(b.LedgerID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid bill allocation ledger_id")
			return in, false
		}
		amt, err := decimal.NewFromString(b.Amount)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid bill allocation amount")
			return in, false
		}
		in.BillAllocations = append(in.BillAllocations, ledger.BillAllocationInput{
			LedgerID: lid,
			RefName:  b.RefName,
			RefType:  ledger.RefType(b.RefType),
			DrCr:     ledger.DrCr(b.DrCr),
			Amount:   amt,
			DueDate:  b.DueDate,
		})
	}
	for i, l := range req.Inventory {
		line, ok := buildInventoryLine(w, i+1, l)
		if !ok {
			return in, false
		}
		in.Inventory = append(in.Inventory, line)
	}
	for i, c := range req.PhysicalCount {
		line, ok := buildPhysicalCountLine(w, i+1, c)
		if !ok {
			return in, false
		}
		in.PhysicalCount = append(in.PhysicalCount, line)
	}
	if req.GST != nil {
		gst, ok := buildGST(w, *req.GST)
		if !ok {
			return in, false
		}
		in.GST = gst
	}
	if req.TDS != nil {
		tds, ok := buildTDS(w, *req.TDS)
		if !ok {
			return in, false
		}
		in.TDS = tds
	}
	return in, true
}

// buildInventoryLine converts one wire inventory line into a
// ledger.InventoryLine, writing a 400 naming the 1-based line and the field and
// returning ok=false on any malformed value.
//
// EVERY NUMERIC FIELD GOES THROUGH decimal.NewFromString (or optionalDecimal,
// which is the same parse with "" -> zero). Never strconv.ParseFloat: float
// parsing ACCEPTS "NaN" and "Inf", which then blow up inside the decimal
// package as a panic the Recoverer turns into an opaque 500 — a malformed
// quantity typed by a data-entry operator has to come back as a 400 that names
// the line, not as a server error. It would also silently round a
// 19-digit quantity, so the stock movement stored would not be the one keyed.
//
// The line's SEMANTICS (direction token, positive quantity, discount in 0..100)
// are deliberately NOT re-checked here: ledger.validateInventory owns them and
// returns a domain message. A second copy of those rules in the HTTP layer is a
// copy that drifts, and the drifted one always wins for whichever caller reaches
// it first.
func buildInventoryLine(w http.ResponseWriter, lineNo int, l inventoryLineReq) (ledger.InventoryLine, bool) {
	var out ledger.InventoryLine
	where := fmt.Sprintf(" on inventory line %d", lineNo)

	itemID, err := uuid.Parse(l.StockItemID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid stock_item_id"+where)
		return out, false
	}
	godownID, err := optionalUUID(l.GodownID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid godown_id"+where)
		return out, false
	}
	batchID, err := optionalUUID(l.BatchID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid batch_id"+where)
		return out, false
	}
	// qty is REQUIRED (there is no such thing as a stock line for no quantity),
	// so it is parsed strictly: "" is a 400 rather than a silent zero that
	// ledger.validateInventory would then reject with a less specific message.
	qty, err := decimal.NewFromString(l.Qty)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid qty"+where)
		return out, false
	}
	// A MISSING rate asks the ledger to price the line; a PRESENT one is taken
	// as stated, including "0".
	var rate decimal.Decimal
	rateUnstated := l.Rate == nil
	if !rateUnstated {
		rate, err = optionalDecimal(*l.Rate)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid rate"+where)
			return out, false
		}
	}
	discount, err := optionalDecimal(l.DiscountPct)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid discount_pct"+where)
		return out, false
	}
	amount, err := optionalDecimal(l.Amount)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid amount"+where)
		return out, false
	}

	return ledger.InventoryLine{
		StockItemID:  itemID,
		GodownID:     godownID,
		BatchID:      batchID,
		Direction:    ledger.Direction(l.Direction),
		Qty:          qty,
		Rate:         rate,
		RateUnstated: rateUnstated,
		DiscountPct:  discount,
		Amount:       amount,
	}, true
}

// buildPhysicalCountLine converts one wire counted position into a
// ledger.PhysicalCountLine, writing a 400 naming the 1-based line and the field
// and returning ok=false on any malformed value.
//
// It mirrors buildInventoryLine deliberately, including the decimal parsing: a
// counted quantity typed by a warehouse hand goes through decimal.NewFromString
// and never strconv.ParseFloat, because float parsing ACCEPTS "NaN" and "Inf"
// and the decimal package then PANICS on them — a stock-take fat-finger has to
// come back as a 400 naming the line, not as an opaque 500 from a recovered
// panic.
//
// qty is parsed STRICTLY ("" is a 400): a Physical Stock line with no quantity
// is not "count zero", it is a line whose count was never entered, and silently
// reading it as zero would write the entire position off the books.
//
// rate is parsed LENIENTLY ("" is zero) because it is required only for a
// SURPLUS, and only the ledger — which alone knows the book position inside the
// post transaction — can tell a surplus from a shortage. Demanding it here
// would refuse every legitimate shortage line.
func buildPhysicalCountLine(w http.ResponseWriter, lineNo int, c physicalCountLineReq) (ledger.PhysicalCountLine, bool) {
	var out ledger.PhysicalCountLine
	where := fmt.Sprintf(" on physical count line %d", lineNo)

	itemID, err := uuid.Parse(c.StockItemID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid stock_item_id"+where)
		return out, false
	}
	godownID, err := optionalUUID(c.GodownID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid godown_id"+where)
		return out, false
	}
	batchID, err := optionalUUID(c.BatchID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid batch_id"+where)
		return out, false
	}
	qty, err := decimal.NewFromString(c.Qty)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid qty"+where)
		return out, false
	}
	rate, err := optionalDecimal(c.Rate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid rate"+where)
		return out, false
	}

	return ledger.PhysicalCountLine{
		StockItemID: itemID,
		GodownID:    godownID,
		BatchID:     batchID,
		Qty:         qty,
		Rate:        rate,
	}, true
}

// buildGST converts the GST request block into ledger.GSTParams, writing a 400
// and returning ok=false on any malformed field.
func buildGST(w http.ResponseWriter, g gstReq) (*ledger.GSTParams, bool) {
	out := &ledger.GSTParams{
		RulesetVersion:         g.RulesetVersion,
		SupplierStateCode:      g.SupplierStateCode,
		PlaceOfSupplyStateCode: g.PlaceOfSupplyStateCode,
		ReverseCharge:          g.ReverseCharge,
		Exempt:                 g.Exempt,
	}
	for _, ln := range g.Lines {
		tv, err := decimal.NewFromString(ln.TaxableValue)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid gst line taxable_value")
			return nil, false
		}
		rate, err := decimal.NewFromString(ln.GSTRatePct)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid gst line gst_rate_pct")
			return nil, false
		}
		out.Lines = append(out.Lines, compliance.Line{
			TaxableValue: tv,
			GSTRatePct:   rate,
			HSN:          ln.HSN,
		})
	}
	var err error
	if out.CGSTLedgerID, err = optionalUUID(g.CGSTLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid cgst_ledger_id")
		return nil, false
	}
	if out.SGSTLedgerID, err = optionalUUID(g.SGSTLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid sgst_ledger_id")
		return nil, false
	}
	if out.IGSTLedgerID, err = optionalUUID(g.IGSTLedgerID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid igst_ledger_id")
		return nil, false
	}
	return out, true
}

// --------------------------------------------------------------------------
// Reports
// --------------------------------------------------------------------------

// reportDate validates an optional YYYY-MM-DD query param. An unparseable date
// is a 400, NOT a silent fallback to "no bound": a report that quietly ignored
// the period a user asked for would answer a different question than the one on
// screen, and still look right.
func reportDate(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	v := strings.TrimSpace(r.URL.Query().Get(param))
	if v == "" {
		return "", true
	}
	if _, err := time.Parse("2006-01-02", v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid "+param+" (want YYYY-MM-DD)")
		return "", false
	}
	return v, true
}

func (s *Server) handleTrialBalance(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	// AS AT a date, not a from/to range: a Trial Balance is a POSITION. It
	// takes `as_on` only, and the response echoes it so a client cannot render
	// a period it did not get.
	asOn, ok2 := reportDate(w, r, "as_on")
	if !ok2 {
		return
	}
	tb, err := s.report.TrialBalance(ctx, companyID, asOn)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	rows := make([]map[string]string, 0, len(tb.Rows))
	for _, row := range tb.Rows {
		rows = append(rows, map[string]string{
			"ledger": row.LedgerName,
			"group":  row.GroupName,
			"debit":  row.Debit.StringFixed(4),
			"credit": row.Credit.StringFixed(4),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"rows":         rows,
		"total_debit":  tb.TotalDebit.StringFixed(4),
		"total_credit": tb.TotalCredit.StringFixed(4),
		"balanced":     tb.IsBalanced(),
		"as_on":        asOn,
	})
}

func (s *Server) handleBalanceSheet(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	asOn, ok2 := reportDate(w, r, "as_on")
	if !ok2 {
		return
	}
	bs, err := s.report.BalanceSheet(ctx, companyID, asOn)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"assets":      sectionJSON(bs.Assets),
		"liabilities": sectionJSON(bs.Liabilities),
		"net_profit":  bs.NetProfit.StringFixed(4),
		"balanced":    bs.IsBalanced(),
		"as_on":       asOn,
	})
}

func (s *Server) handleProfitAndLoss(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	// FOR A PERIOD, so this one takes both bounds — a P&L is the only statement
	// of the three for which a lower bound is meaningful.
	from, okFrom := reportDate(w, r, "from")
	if !okFrom {
		return
	}
	to, okTo := reportDate(w, r, "to")
	if !okTo {
		return
	}
	pl, err := s.report.ProfitAndLoss(ctx, companyID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"income":     sectionJSON(pl.Income),
		"expenses":   sectionJSON(pl.Expenses),
		"net_profit": pl.NetProfit.StringFixed(4),
		"from":       from,
		"to":         to,
	})
}

func (s *Server) handleDayBook(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	dbk, err := s.report.DayBook(ctx, companyID, from, to)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	entries := make([]map[string]any, 0, len(dbk.Entries))
	for _, e := range dbk.Entries {
		entries = append(entries, voucherEntryJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"from":    dbk.From,
		"to":      dbk.To,
		"entries": entries,
	})
}

func (s *Server) handleOutstandings(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	var kind reporting.OutstandingKind
	switch r.URL.Query().Get("kind") {
	case "", "RECEIVABLE", "receivable":
		kind = reporting.Receivables
	case "PAYABLE", "payable":
		kind = reporting.Payables
	default:
		writeError(w, http.StatusBadRequest, "invalid kind (RECEIVABLE|PAYABLE)")
		return
	}
	asOn := r.URL.Query().Get("as_on")
	out, err := s.report.Outstandings(ctx, companyID, kind, asOn, nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	parties := make([]map[string]any, 0, len(out.Parties))
	for _, p := range out.Parties {
		bills := make([]map[string]any, 0, len(p.Bills))
		for _, b := range p.Bills {
			bills = append(bills, map[string]any{
				"ref_name":     b.RefName,
				"pending":      b.Pending.StringFixed(4),
				"due_date":     b.DueDate,
				"overdue_days": b.OverdueDays,
				"slab":         b.SlabLabel,
			})
		}
		parties = append(parties, map[string]any{
			"ledger":  p.LedgerName,
			"group":   p.GroupName,
			"pending": p.Pending.StringFixed(4),
			"bills":   bills,
		})
	}
	slabs := make([]map[string]string, 0, len(out.SlabTotals))
	for _, st := range out.SlabTotals {
		slabs = append(slabs, map[string]string{
			"label":  st.Label,
			"amount": st.Amount.StringFixed(4),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":        string(out.Kind),
		"as_on":       out.AsOn,
		"parties":     parties,
		"slab_totals": slabs,
		"total":       out.Total.StringFixed(4),
	})
}

// --------------------------------------------------------------------------
// GST
// --------------------------------------------------------------------------

func (s *Server) handleGSTR1(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query params are required (YYYY-MM-DD)")
		return
	}
	// The FILER's GSTIN is a property of the company, not of the request. It was
	// read only from ?gstin=, so every caller that did not happen to pass it —
	// including the UI — produced a return with NO gstin field at all, which
	// cannot be filed. Fall back to company.gstin; an explicit query param still
	// wins (useful for previewing a return under a different registration).
	gstin := r.URL.Query().Get("gstin")
	if gstin == "" {
		var cerr error
		if gstin, cerr = s.companyGSTIN(ctx); cerr != nil {
			writeError(w, http.StatusInternalServerError, "read company gstin: "+cerr.Error())
			return
		}
	}
	opts := gstr.Options{GSTIN: gstin}
	var err error
	if opts.CGSTLedgerID, err = optionalUUID(r.URL.Query().Get("cgst_ledger_id")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid cgst_ledger_id")
		return
	}
	if opts.SGSTLedgerID, err = optionalUUID(r.URL.Query().Get("sgst_ledger_id")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid sgst_ledger_id")
		return
	}
	if opts.IGSTLedgerID, err = optionalUUID(r.URL.Query().Get("igst_ledger_id")); err != nil {
		writeError(w, http.StatusBadRequest, "invalid igst_ledger_id")
		return
	}

	// When the caller supplied any output-tax ledger, reconcile the return
	// against the books and report whether it ties out to the paisa.
	if opts.CGSTLedgerID != uuid.Nil || opts.SGSTLedgerID != uuid.Nil || opts.IGSTLedgerID != uuid.Nil {
		g, rec, err := s.gstr.Reconcile(ctx, from, to, opts)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"gstr1":             g,
			"reconciliation_ok": rec.Balanced(),
		})
		return
	}

	g, err := s.gstr.Generate(ctx, from, to, opts)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// --------------------------------------------------------------------------
// Migration import (Tally-XML ingress)
// --------------------------------------------------------------------------

func (s *Server) handleImportTallyXML(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	createdBy := r.URL.Query().Get("created_by")
	r.Body = http.MaxBytesReader(w, r.Body, maxXMLBody)
	// The importer parses the Tally ENVELOPE and posts each voucher through the
	// foundation ledger path (the SOLE writer), inheriting every invariant.
	res, err := s.importer.ImportReader(ctx, r.Body, createdBy)
	if err != nil {
		if errors.Is(err, db.ErrNoCompany) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	posted := make([]map[string]any, 0, len(res.Posted))
	for _, p := range res.Posted {
		posted = append(posted, map[string]any{
			"voucher_id":         p.VoucherID.String(),
			"voucher_version_id": p.VoucherVersionID.String(),
			"version_no":         p.VersionNo,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source_company":  res.SourceCompany,
		"vouchers_posted": res.VouchersPosted,
		"ledgers_ensured": res.LedgersEnsured,
		"posted":          posted,
	})
}

// --------------------------------------------------------------------------
// Offline cloud-sequencer commit (Task C)
// --------------------------------------------------------------------------

// hlcReq mirrors sync.HLCTimestamp on the wire.
type hlcReq struct {
	WallMillis int64  `json:"wall_millis"`
	Counter    int64  `json:"counter"`
	NodeID     string `json:"node_id"`
}

// syncIntentReq mirrors sync.Intent on the wire. The voucher body reuses the
// generic voucherReq decoded by buildVoucherInput (its VoucherNo is ignored —
// the server numbers a committed CREATE — but it is carried so postings/date/
// party/gst map through unchanged). class is required so the post path can
// resolve the default voucher type.
type syncIntentReq struct {
	IdempotencyKey  string     `json:"idempotency_key"`
	ClientVoucherID string     `json:"client_voucher_id"`
	Op              string     `json:"op"`
	Class           string     `json:"class"`
	ProvisionalNo   string     `json:"provisional_no"`
	HLC             hlcReq     `json:"hlc"`
	NodeID          string     `json:"node_id"`
	BaseVersion     int        `json:"base_version"`
	Payload         voucherReq `json:"payload"`
}

type syncCommitReq struct {
	Intents []syncIntentReq `json:"intents"`
}

// handleSyncCommit accepts an offline node's outbox of intents, commits them
// through the cloud sequencer, and returns one outcome per intent (final number,
// status, reconciliation id). It is the HTTP face of sync.Service.Commit.
func (s *Server) handleSyncCommit(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req syncCommitReq
	if !decodeJSON(w, r, &req) {
		return
	}

	intents := make([]sync.Intent, 0, len(req.Intents))
	for _, ir := range req.Intents {
		cvid, err := uuid.Parse(ir.ClientVoucherID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid client_voucher_id")
			return
		}
		class := ledger.Class(ir.Class)
		payload, ok := buildVoucherInput(w, class, ir.Payload)
		if !ok {
			return
		}
		intents = append(intents, sync.Intent{
			IdempotencyKey:  ir.IdempotencyKey,
			ClientVoucherID: cvid,
			Op:              sync.Op(ir.Op),
			Class:           class,
			ProvisionalNo:   ir.ProvisionalNo,
			HLC: sync.HLCTimestamp{
				WallMillis: ir.HLC.WallMillis,
				Counter:    ir.HLC.Counter,
				NodeID:     ir.HLC.NodeID,
			},
			NodeID:      ir.NodeID,
			BaseVersion: ir.BaseVersion,
			Payload:     payload,
		})
	}

	result, err := s.sync.Commit(ctx, intents)
	if err != nil {
		if errors.Is(err, db.ErrNoCompany) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}

	outcomes := make([]map[string]any, 0, len(result.Outcomes))
	for _, o := range result.Outcomes {
		outcomes = append(outcomes, map[string]any{
			"idempotency_key":   o.IdempotencyKey,
			"client_voucher_id": o.ClientVoucherID.String(),
			"status":            string(o.Status),
			"final_voucher_no":  o.FinalVoucherNo,
			"version_no":        o.VersionNo,
			"reconciliation_id": o.ReconciliationID,
			"detail":            o.Detail,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"outcomes": outcomes})
}

// --------------------------------------------------------------------------
// Conversational query (Phase 3 flagship)
// --------------------------------------------------------------------------

// queryReq is the POST /query body. Exactly one of nl / dsl is used: dsl WINS
// when present (the power-user / contract path), otherwise nl is translated.
type queryReq struct {
	NL  string       `json:"nl"`
	DSL *query.Query `json:"dsl"`
}

// handleQuery answers a natural-language (or typed-DSL) question against the
// books, read-only and tenant-scoped. The flow:
//
//  1. resolve the authz-VERIFIED company from ctx (scopeCtxID), exactly like
//     every report handler — never the raw path param.
//  2. if a DSL is supplied, use it directly; else translate nl -> Query
//     (ErrUntranslatable -> 422 with examples).
//  3. Compile (SQLPreview) + Execute via the read-only, RLS-scoped pool.
//  4. 200 { dsl, sql_preview, columns, rows }.
//
// 422 is returned for an untranslatable question OR an invalid/uncompilable DSL,
// with a SAFE message — the raw SQL/internal error text is NEVER leaked (a
// compiler error could otherwise echo schema/identifier internals).
func (s *Server) handleQuery(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	var req queryReq
	if !decodeJSON(w, r, &req) {
		return
	}

	// (2) Resolve the typed Query: a supplied DSL wins; otherwise translate the
	// NL. An empty body (neither nl nor dsl) is an untranslatable question.
	var q query.Query
	if req.DSL != nil {
		q = *req.DSL
	} else {
		translated, err := s.query.Translate(ctx, companyID, req.NL)
		if err != nil {
			// Operator fault first (wave-2 WS1): the real-Claude translator upstream
			// is unreachable / misconfigured (dead key, bad model id, 429, 5xx). That
			// is a 503 "retry or POST a typed dsl" — NOT a user's bad question. This
			// branch MUST precede the 422 fallthrough so the existing NL tests, which
			// exercise the FakeTranslator (never returning this error), stay identical.
			if errors.Is(err, nlq.ErrTranslatorUnavailable) {
				writeError(w, http.StatusServiceUnavailable, "translator unavailable; retry or POST a typed dsl")
				return
			}
			// Untranslatable (or any other translator error): 422 with examples. Do
			// not leak internal error text — the message is fixed and helpful.
			writeQueryUntranslatable(w)
			return
		}
		q = translated
	}

	// (3) Compile for the preview, then execute. A compile error (unknown
	// entity/column/op, empty IN, bad order-by, etc.) is a 422 with a SAFE
	// message — never the raw compiler/SQL text.
	preview, err := s.query.Preview(q)
	if err != nil {
		writeQueryInvalid(w)
		return
	}
	res, err := s.query.Run(ctx, q)
	if err != nil {
		if errors.Is(err, db.ErrNoCompany) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Compile failures surface here too (Execute compiles internally). Treat
		// any execution/compile failure as an invalid query (422), never a 500
		// that could leak SQL or internal details.
		writeQueryInvalid(w)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"dsl":         q,
		"sql_preview": preview,
		"columns":     queryColumnsJSON(res.Columns),
		"rows":        res.Rows,
	})
}

// queryColumnsJSON serialises the column metadata to the pinned
// [{name,type},...] shape.
func queryColumnsJSON(cols []query.ColumnMeta) []map[string]string {
	out := make([]map[string]string, 0, len(cols))
	for _, c := range cols {
		out = append(out, map[string]string{"name": c.Name, "type": c.Type})
	}
	return out
}

// writeQueryUntranslatable writes the 422 for a question the translator could
// not map, echoing the example questions so the user learns what to ask. It
// never includes internal error text.
func writeQueryUntranslatable(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":    "could not understand the question; try one of the examples",
		"examples": query.ExampleQuestions,
	})
}

// writeQueryInvalid writes the 422 for a DSL that fails to compile/execute. The
// message is fixed and SAFE — it never leaks the compiled SQL or the internal
// compiler error (which could reveal schema/identifier internals).
func writeQueryInvalid(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error":    "invalid query",
		"examples": query.ExampleQuestions,
	})
}

// --------------------------------------------------------------------------
// Shared helpers
// --------------------------------------------------------------------------

// scopeCtxID parses the {companyID} path param, binds it into the request
// context for the GUC guard, and returns both. On a bad id it writes a 400 and
// returns ok=false.
func (s *Server) scopeCtxID(w http.ResponseWriter, r *http.Request) (context.Context, uuid.UUID, bool) {
	// Prefer the authz-VERIFIED company injected by companyAuthMiddleware. After
	// Task D, the path param alone no longer drives db.WithCompany; the middleware
	// validated membership and injected the authorized company into the context.
	if authorized, ok := db.CompanyFromContext(r.Context()); ok {
		// Cross-check the path param when present: a mismatch is a 400 (the route
		// should never resolve to a different company than the one authorized).
		if pathID, err := uuid.Parse(chi.URLParam(r, "companyID")); err == nil && pathID != authorized {
			writeError(w, http.StatusBadRequest, "company id mismatch")
			return nil, uuid.Nil, false
		}
		return r.Context(), authorized, true
	}
	// Fallback (no middleware / no injected company): parse the path param.
	companyID, err := uuid.Parse(chi.URLParam(r, "companyID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid company id")
		return nil, uuid.Nil, false
	}
	return db.WithCompany(r.Context(), companyID), companyID, true
}

// scopeCtx is scopeCtxID without surfacing the id (for handlers that only need
// the scoped context).
func (s *Server) scopeCtx(w http.ResponseWriter, r *http.Request) (context.Context, bool) {
	ctx, _, ok := s.scopeCtxID(w, r)
	return ctx, ok
}

// sectionJSON serialises a report Section (and its node tree) to a plain map.
func sectionJSON(sec reporting.Section) map[string]any {
	return map[string]any{
		"title": sec.Title,
		"nodes": nodesJSON(sec.Nodes),
		"total": sec.Total.StringFixed(4),
	}
}

func nodesJSON(nodes []reporting.StatementNode) []map[string]any {
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		m := map[string]any{
			"kind":   string(n.Kind),
			"name":   n.Name,
			"amount": n.Amount.StringFixed(4),
		}
		if n.ID != uuid.Nil {
			m["id"] = n.ID.String()
		}
		if len(n.Children) > 0 {
			m["children"] = nodesJSON(n.Children)
		}
		out = append(out, m)
	}
	return out
}

func voucherEntryJSON(e reporting.VoucherEntry) map[string]any {
	postings := make([]map[string]string, 0, len(e.Postings))
	for _, p := range e.Postings {
		postings = append(postings, map[string]string{
			"ledger": p.LedgerName,
			"dr_cr":  p.DrCr,
			"amount": p.Amount.StringFixed(4),
		})
	}
	return map[string]any{
		"voucher_id":   e.VoucherID.String(),
		"version_no":   e.VersionNo,
		"op":           e.Op,
		"class":        e.Class,
		"voucher_type": e.VoucherTypeName,
		"voucher_no":   e.VoucherNo,
		"date":         e.Date,
		"narration":    e.Narration,
		"party":        e.Party,
		"debit":        e.Debit.StringFixed(4),
		"credit":       e.Credit.StringFixed(4),
		"postings":     postings,
	}
}

// decodeJSON decodes a JSON request body into v, capping the body size and
// writing a 400 on failure. It returns false when the caller should stop.
func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "empty request body")
		} else {
			writeError(w, http.StatusBadRequest, "invalid json body")
		}
		return false
	}
	return true
}

// drainBody reads (and caps) the request body, then RESTORES r.Body so the
// downstream handler can re-read it. Returns the raw bytes, or nil on read
// failure / empty body. Used by the PDP to peek a write's voucher_date without
// consuming the body.
func drainBody(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	limited := io.LimitReader(r.Body, maxJSONBody)
	b, err := io.ReadAll(limited)
	_ = r.Body.Close()
	if err != nil {
		// Restore an empty body so the handler errors cleanly rather than panicking.
		r.Body = io.NopCloser(bytes.NewReader(nil))
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	if len(b) == 0 {
		return nil
	}
	return b
}

// peekVoucherDate extracts the voucher_date field from a voucher write body for
// the back-dated cutoff check. Returns nil when absent/unparseable (the cutoff
// then does not apply). It tolerates non-voucher bodies (returns nil).
func peekVoucherDate(body []byte) *time.Time {
	var probe struct {
		VoucherDate string `json:"voucher_date"`
	}
	if err := json.Unmarshal(body, &probe); err != nil || probe.VoucherDate == "" {
		return nil
	}
	d, err := time.Parse("2006-01-02", probe.VoucherDate)
	if err != nil {
		return nil
	}
	return &d
}

// peekPayrollVoucherDate derives the PAY RUN's voucher date for the back-dated
// cutoff check, from the only field that carries it: wage_month.
//
// ===========================================================================
// WHY THE PAY RUN NEEDS ITS OWN PEEK
// ===========================================================================
// Every other voucher class puts its date on the wire as `voucher_date`, so
// peekVoucherDate finds it and the cutoff applies. POST /vouchers/payroll does
// not: payRunReq carries `wage_month` and the voucher is dated the LAST DAY of
// that month, derived inside payrolldb. With only peekVoucherDate in the path
// the PDP saw no date at all, cutoffDeny returned early, and the cutoff — the
// one control an owner has over a closed period — was INERT on the single most
// sensitive voucher in the product. A level created to run payroll could post
// salaries into a wage month whose PF ECR, ESIC return and 24Q are already
// filed, and the resulting ledger entries would appear in no filed return.
//
// ===========================================================================
// WHY NOT JUST ADD voucher_date TO payRunReq
// ===========================================================================
// Because a client could then set it INDEPENDENTLY of wage_month. The posting
// would still use wage_month while the cutoff judged voucher_date, the two
// would drift, and the drift would be invisible — nothing compares them. One
// field on the wire, one rule for the date: payrolldb.LastDayOf, called here
// and by the posting path, is that one rule.
//
// ===========================================================================
// A MALFORMED OR ABSENT wage_month REFUSES
// ===========================================================================
// It returns the ZERO time, which is before every cutoff a company could set,
// so cutoffDeny denies. Refusing is the safe direction and the alternative is
// not merely untidy: returning nil ("the cutoff does not apply") would make
// `{"wage_month": "notamonth"}` a way to walk straight through a closed period.
// The handler's own 400 for that month is no protection, because authorization
// runs BEFORE the handler. Where no cutoff is set the zero time changes
// nothing, and the request reaches the handler and gets its ordinary 400.
func peekPayrollVoucherDate(body []byte) *time.Time {
	var probe struct {
		WageMonth string `json:"wage_month"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return beforeEveryCutoff()
	}
	// parseWageMonth is the SAME parse handlePayrollRun applies to this field,
	// so the PDP and the handler cannot disagree about which month was asked
	// for — only about whether it is allowed.
	m, err := parseWageMonth(probe.WageMonth)
	if err != nil {
		return beforeEveryCutoff()
	}
	d := payrolldb.LastDayOf(m.First())
	return &d
}

// beforeEveryCutoff returns a date earlier than any cutoff an administrator
// could set, so a write whose date could not be determined is REFUSED by
// cutoffDeny rather than waved through. A fresh value each call: the caller
// receives a pointer and must not be able to mutate a shared one.
func beforeEveryCutoff() *time.Time {
	var zero time.Time
	return &zero
}

// optionalDecimal parses a possibly-empty decimal string, returning zero for an
// empty string.
func optionalDecimal(s string) (decimal.Decimal, error) {
	if s == "" {
		return decimal.Zero, nil
	}
	return decimal.NewFromString(s)
}

// optionalUUID parses a possibly-empty UUID string, returning uuid.Nil for an
// empty string.
func optionalUUID(s string) (uuid.UUID, error) {
	if s == "" {
		return uuid.Nil, nil
	}
	return uuid.Parse(s)
}

// writeMastersError maps a masters service error to an HTTP status: not-found
// and no-company are client errors, everything else is unprocessable.
func writeMastersError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, db.ErrNoCompany):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, masters.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
