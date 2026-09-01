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
	"encoding/xml"
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

	"github.com/jackc/pgx/v5"
	"github.com/khata/cored/internal/authn"
	"github.com/khata/cored/internal/authz"

	"github.com/khata/cored/internal/backup"
	"github.com/khata/cored/internal/banking"
	"github.com/khata/cored/internal/bankstatement"
	"github.com/khata/cored/internal/compliance"
	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/compliance/itc"
	"github.com/khata/cored/internal/einvoice"
	"github.com/khata/cored/internal/ewaybill"
	"github.com/khata/cored/internal/identity"
	// integration is imported for the Tally-XML PEEK, not for the import itself:
	// peekTallyXMLVoucherDate must parse the envelope and convert a Tally date with
	// the SAME two calls migration's importer uses, or the PDP and the writer could
	// disagree about which date a <VOUCHER> asked for. See peekTallyXMLVoucherDate.
	"github.com/khata/cored/internal/integration"
	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/migration"
	"github.com/khata/cored/internal/nlq"
	"github.com/khata/cored/internal/notify"
	"github.com/khata/cored/internal/orders"
	"github.com/khata/cored/internal/payroll/payrolldb"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/printing/printingdb"
	"github.com/khata/cored/internal/query"
	"github.com/khata/cored/internal/reporting"
	"github.com/khata/cored/internal/sync"
	"github.com/khata/cored/internal/webauthn"
)

// Request-body size caps guard against unbounded uploads. The Tally-XML import
// endpoint accepts a larger body than the JSON endpoints.
//
// ⛔ THE DRAFT UPLOAD IS THE SECOND ROUTE THAT CANNOT LIVE WITH maxJSONBody, AND
// IT BECAME ONE THE DAY insightd MOVED BEHIND THIS PROXY (register row D49).
// `POST .../drafts` carries a base64 SCAN — the Bill Inbox has a file input and
// does `btoa(binary)` — and base64 inflates by 4/3, so a perfectly ordinary
// ~800 KB photo of a supplier bill exceeds 1 MiB on the wire. While the browser
// posted straight to insightd there was no cap in the way at all; forwarding
// through cored puts drainBody's cap in front of it, and drainBody REPLACES
// r.Body with what it read, so the truncation would be silent — insightd would
// answer 422 on a body the operator's own file produced. See bodyCapFor.
const (
	maxJSONBody = 1 << 20 // 1 MiB
	// ⛔ 32 MiB WAS NOT ENOUGH FOR ONE REAL COMPANY. A TallyPrime "All Masters"
	// export of a mid-sized trader — 3,706 ledgers, nine years of counter-parties
	// — is 84 MB, because Tally writes UTF-16 (two bytes per ASCII character) and
	// stamps ~200 mostly-empty elements on every master. The old cap refused it at
	// the door with a 413, so the commercial migration path could not be walked at
	// all with the very first genuine file it met.
	//
	// ⚠️ THE COST OF THIS NUMBER IS MEMORY — BUT NOT THE MEMORY AN EARLIER VERSION
	// OF THIS COMMENT NAMED, AND THE DIFFERENCE CHANGES WHAT THE FIX IS.
	//
	// It said "the importer builds a parse tree several times the size of the text"
	// and "a body at this cap can cost well over a gigabyte of heap", and concluded
	// the answer was a streaming decode. MEASURED 2026-08-25, on a document shaped
	// like a real export (~200 mostly-empty unmapped elements per master, which is
	// what TallyPrime writes):
	//
	//	document                       202.5 MiB (UTF-16LE)
	//	live parse tree                 25.5 MiB   <- 0.1x the document, not 3x
	//	total allocated, full parse   1352   MiB   <- the "gigabyte", and it is CHURN
	//	total allocated, streaming    1346   MiB   <- streaming does not avoid it
	//
	// encoding/xml discards every element the structs do not model, so the tree is a
	// FRACTION of the text; the gigabyte is tokenising churn, which a streaming walk
	// incurs identically. So the three real costs, largest first, are: tokenising
	// this body THREE TIMES per request (drainBody buffers it, then each of the two
	// XML peeks parses it, then the handler parses it again); the body buffer itself;
	// and only then the tree. Streaming removes the smallest one and would make us
	// tokenise MORE often, because the importer needs three passes over the messages.
	//
	// ⛔ THE CHANGE THAT PAYS IS PARSE ONCE — parse from r.Body in the middleware
	// after the membership check, memoise the envelope on the request context, and
	// have both peeks and the handler read that one envelope. It cuts the churn ~3x,
	// removes the body buffer (NewTallyDecodeReader already streams), and makes it
	// structurally impossible for the peeks and the handler to disagree about the
	// file — which is what every one of §7.2's seven doors has been. See HANDOFF.md
	// §1.11 item B. This cap stays at what a real export needs.
	maxXMLBody = 128 << 20 // 128 MiB
	// Sized for a phone photo of a bill (the OCR spike's ~12 MP case) once
	// base64 has grown it by a third, with headroom — and deliberately well
	// under the XML import's allowance, because this body is buffered in memory
	// on the authorization path before the PDP has agreed to anything.
	maxDraftUploadBody = 16 << 20 // 16 MiB
)

// Server holds the module services and the chi router. All services share one
// tenant-scoped pool.
type Server struct {
	pool     *db.Pool
	identity *identity.Service
	masters  *masters.Service
	ledger   *ledger.Service
	report   *reporting.Service
	// banking is the WRITE side of bank reconciliation (T4). It is a SEPARATE
	// service from `report` on purpose: internal/reporting is read-only by its
	// package contract, and reconciling is a write. See internal/banking's doc.
	banking *banking.Service
	// bankstmt is the WRITE side of bank-statement IMPORT (T4 slice 2a), a
	// third service for the same reason `banking` is a second one: importing is
	// a write, and internal/reporting is read-only by its package contract. The
	// READ of what was imported lives in `report`, beside the BRS.
	bankstmt *bankstatement.Service
	// orders is the WRITE side of sales/purchase orders and the bill of materials
	// (T3). A FOURTH write service, for the same reason `banking` and `bankstmt`
	// are the second and third: internal/reporting is read-only by its package
	// contract and creating an order is a write. The READ of what is outstanding
	// lives in `report` as the two order books.
	//
	// ⛔ internal/orders may import NOTHING but the db layer — archtest pins it —
	// because an order is a committed quantity and not a movement. That is why it
	// is a sibling of the ledger here rather than a caller of it.
	orders *orders.Service
	// ewaybill (T5) assembles a consignment from the books, runs the pure Rule 138
	// core over it and stores the result. ⛔ It is a sibling of the ledger and not
	// a caller of it — archtest pins internal/ewaybill to {platform/db,
	// statutory/rules}, so it can read a voucher but can never post, alter or
	// freeze one. An e-way bill is raised AGAINST a voucher; it is not a voucher.
	ewaybill *ewaybill.Service
	// einvoice is cored's half of the IRN path. It is a PROXY as much as a
	// service: it builds the NIC snapshot (identity AND item list) from the
	// books, then hands it to statutoryd over HTTP — because the IRN workflow
	// wraps an exactly-once external IRP call in durable state and must stay in
	// its own deployable, while only cored holds the inventory rows, the stock
	// masters, the HSN and the rate the payload needs. HANDOFF §1.4 B.2.
	//
	// ⛔ ITS READ PHASE COMMITS BEFORE IT CALLS OUT. The call graph is
	// cored -> statutoryd -> cored (GetVoucher, then the freeze hook), so a
	// transaction held across the call is a connection the callback then cannot
	// get, and the failure mode is a hung request rather than an error.
	einvoice *einvoice.Service
	gstr     *gstr.Service
	importer *migration.Importer
	sync     *sync.Service
	query    *query.Service
	router   chi.Router

	// itc is input-tax-credit reconciliation: the GSTR-2B/2A inward statements
	// ingested into gst_inward_statement + gst_inward_document (migration 0032),
	// matched against purchase vouchers to decide what credit is claimable.
	// backup is T14: whole-company archive out, whole-company restore in.
	//
	// ⛔ IT IS NOT A REPORT AND MUST NEVER BE MODELLED AS ONE. Migration 0011
	// grants the seeded Data Entry level a coarse `allow display report`, so a
	// `report:NAME` token is GRANTED to every operator in every tenant the moment
	// it exists. A backup is the most sensitive read in the product — every
	// ledger, every party, every salary, in one file — so it carries its own
	// `backup` resource token, which a level that never mentions it is DENIED.
	backup *backup.Service

	itc *itc.Service
	// payroll is the employee master, the pay run and the register/payslip read
	// models over the tables migration 0034 created. It posts salary vouchers
	// through the SAME ledger service as everything else — never around it.
	payroll *payrolldb.Service

	// printStore is the DB-facing half of printing (track T1): it assembles a
	// voucher's document from the books and reads a company's template overrides.
	// The RENDERER (internal/printing) is a pure leaf that this server hands a
	// document value to — see handler_print.go.
	//
	// It takes pool.Raw() rather than the *db.Pool every other service takes,
	// because that is the seam printingdb was built against. The consequence is
	// that printingdb sets `app.current_company_id` transaction-locally itself, in
	// its own inTenantTx, and print_template / voucher_head are FORCE RLS with the
	// GUC in the USING clause — so a print request with no tenant scope reads
	// NOTHING rather than everything. Never hand this an admin pool: it would
	// bypass RLS and the tenant predicate would stop being enforced.
	printStore *printingdb.Store

	// drafts is cored's PROXY into insightd — the AI bill inbox (register row
	// D49). It holds NO database handle and no service of its own: it forwards an
	// already-authorized request and passes insightd's answer back byte for byte.
	// insightd is a SEPARATE deployable that is routinely not running, so every
	// route through this answers 503 rather than failing the request in a way that
	// looks like a books problem. See insightd_proxy.go.
	drafts *draftProxy

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
	// SeedCompanyAuthDefaultsInTx is the same seeding inside a transaction the
	// CALLER owns, which is what RESTORE (T14) needs: the membership that makes a
	// restored company reachable has to commit with the rows, or a failure here
	// would leave a fully restored company nobody is a member of — and there is
	// no delete path with which to clean that up.
	SeedCompanyAuthDefaultsInTx(ctx context.Context, tx pgx.Tx, companyID, userID uuid.UUID) error
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
		banking:  banking.NewService(pool),
		bankstmt: bankstatement.NewService(pool),
		orders:   orders.NewService(pool),
		ewaybill: ewaybill.NewService(pool),
		// The IRN proxy. ⛔ statutoryd's location IS CONFIGURATION and must be,
		// because it is a second process that is routinely not running: the
		// default is the dev port from the Makefile, and STATUTORYD_BASE_URL
		// points cored at a real deployment. The client carries a deadline for
		// the same reason — statutoryd's transient path answers 503 and is
		// resumable by design, so a proxy with no timeout converts a slow IRP
		// into a hung browser tab in exchange for nothing.
		einvoice: einvoice.NewService(pool,
			einvoice.NewHTTPStatutorydClient(
				envOr("STATUTORYD_BASE_URL", einvoice.DefaultStatutorydBaseURL),
				statutorydTimeout())),
		// The AI bill inbox proxy (D49). Same shape as the IRN proxy above:
		// insightd's location is configuration, the client carries a deadline, and
		// the SERVICE CREDENTIAL has no default — there is no safe compiled-in
		// value for a credential. See insightd_proxy.go.
		drafts:   newDraftProxy(),
		gstr:     gstr.NewService(pool),
		importer: migration.NewImporter(m, l, pool),
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
		itc:     itc.NewService(pool),
		payroll: payrolldb.NewService(pool, l),
		// Printing (T1). The SAME application-role pool everything else uses, so
		// the print path is under the same NOBYPASSRLS role and the same
		// tenant-scoped transactions as every report.
		printStore: printingdb.NewStore(pool.Raw()),
		// backup (T14) serialises one company to a portable archive and restores
		// one into a company that does not yet exist. It reaches ONLY platform/db
		// and platform/tenant: an archive is rows in and rows out, and a package
		// that could call ledger.Post would be a second, unaudited voucher path.
		backup:    backup.New(pool),
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

// statutorydTimeout reads STATUTORYD_TIMEOUT (a Go duration, e.g. "45s") and
// falls back to einvoice.DefaultTimeout.
//
// ⛔ AN UNPARSEABLE VALUE FALLS BACK RATHER THAN FAILING BOOT, and it says so in
// the log. The alternative — refusing to start — takes the WHOLE server down
// because one optional knob for one optional side plane was mistyped, which is
// a much worse outcome than an e-invoice attempt that gives up after the default
// deadline. ⛔ But it must not fall back SILENTLY: a deployment that set 5m for
// a slow IRP and typoed it would otherwise time out at 30s forever with nothing
// anywhere saying why.
func statutorydTimeout() time.Duration {
	raw := os.Getenv("STATUTORYD_TIMEOUT")
	if raw == "" {
		return einvoice.DefaultTimeout
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		log.Printf("cored: STATUTORYD_TIMEOUT=%q is not a positive Go duration (e.g. \"45s\"); using %s",
			raw, einvoice.DefaultTimeout)
		return einvoice.DefaultTimeout
	}
	return d
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
	// Now is wired for the SAME reason FakeTranslator gets one: the prompt's
	// resolved period windows are built from it, and a translator with no clock
	// guesses what "last quarter" means (D21).
	return &nlq.ClaudeTranslator{
		APIKey:  key,
		Model:   envOr("QUERY_TRANSLATOR_MODEL", nlq.DefaultModel),
		BaseURL: envOr("QUERY_TRANSLATOR_BASE_URL", nlq.DefaultBaseURL),
		Now:     time.Now,
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
//     AUTH_AUDIENCE is REQUIRED alongside it too, and it is NOT redundant with
//     the issuer: `iss` identifies the REALM, and every client in a realm mints
//     tokens carrying the same `iss`, so the issuer pin cannot distinguish a
//     token minted FOR cored from one a user obtained for any other application
//     in that realm. Set it to the OIDC client/audience cored's tokens are
//     minted for; the token's `aud` must contain it. (D102.)
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
		// ⚠️ ORDER MATTERS: the AUTH_ISSUER guard above stays FIRST. An operator
		// who has set neither should be told about the issuer first (it is the
		// coarser pin), and the boot-matrix test that asserts the AUTH_ISSUER
		// message would otherwise start matching this branch instead.
		audience := os.Getenv("AUTH_AUDIENCE")
		if audience == "" {
			return nil, nil, errors.New("server: AUTH_JWKS_URL is set but AUTH_AUDIENCE is empty; the issuer pin scopes acceptance to one REALM, and every client in a realm mints tokens carrying the same iss, so without a pinned audience a token a user obtained for ANY OTHER application in that realm authenticates here as that subject. Set AUTH_AUDIENCE to the OIDC client/audience cored's tokens are minted for. Refusing to start fail-open")
		}
		verifier := authn.NewJWKSVerifier(jwksURL, issuer, audience)
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
// Production swap (SHIPPED as of WS6): set AUTH_JWKS_URL + AUTH_ISSUER +
// AUTH_AUDIENCE (all three REQUIRED — buildAuth refuses to boot without any of
// them; the audience pin is D102) and
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

	// RESTORE A COMPANY FROM AN ARCHIVE (T14). authn-ONLY, mounted BESIDE company
	// creation and OUTSIDE the {companyID} subtree, because that is what it is:
	// the company does not exist yet, so the subtree's membership check would 403
	// every caller before the company it names could ever be created.
	//
	// ⛔ routeAction() THEREFORE NEVER SEES THIS ROUTE, and the only thing gating
	// it is "you are a signed-in user" — exactly as for POST /v1/companies. That
	// is a deliberate, stated hole and not an oversight: a restore CREATES a
	// company from a file the caller already holds, it cannot read or overwrite
	// any company that is already here (it refuses when the id exists, and there
	// is no delete path in the product at all), and the restoring user becomes
	// the Owner of what they created. The archive is the authority; whoever holds
	// it already holds the books.
	r.With(s.authnOnlyMiddleware).Post("/v1/companies/restore", s.handleRestoreCompany)

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

		// Company FEATURE settings (register row D5). Narrow by design: the
		// only thing this writes is company.allow_negative_stock, which both
		// stock enforcers have read since migration 0025 and nothing could
		// write. It is deliberately NOT a general company editor -- the name,
		// the financial year, the state code and the currency block are
		// identity that posted books depend on.
		//
		// TWO-TOUCH RULE: this line is inert without the matching
		// `PUT ""`/`PUT "/"` -> (authz.Alter, "company") arm in routeAction().
		// TestEveryCompanyRouteIsMappedAndInVocabulary walks THIS router and
		// turns the suite red if the arm is missing, so the gap cannot ship.
		//
		// An ALTER, not a Create: it corrects the one company row, it never
		// adds a second -- the same reading that made PUT .../tds-profile an
		// Alter. Being a write it inherits the Owner level's WebAuthn step-up
		// (Action.IsWrite()); it does NOT inherit the back-dated cutoff,
		// because a settings change carries no date and
		// voucherDateFromRequest returns nil for a route with none, which is
		// correct: a policy switch is not a dated document.
		r.Put("/", s.handleUpdateCompany)

		// WHOLE-COMPANY BACKUP (T14). One gzipped JSON Lines archive of every
		// table the tenant census says holds this company's data.
		//
		// TWO-TOUCH RULE: inert without the matching `GET "/backup"` arm in
		// routeAction(). TestEveryCompanyRouteIsMappedAndInVocabulary walks THIS
		// router and turns the suite red if the arm is missing, so the gap cannot
		// ship as a 403 for the Owner.
		//
		// ⛔ ITS RESOURCE TOKEN IS `backup` AND NOT `report:NAME`. Migration 0011
		// seeds Data Entry a coarse `allow display report`, so a report token is
		// granted to every operator in every tenant the day it exists — and this
		// route emits every ledger, every party and every salary as one file. See
		// handler_backup.go.
		r.Get("/backup", s.handleBackupCompany)

		// ===================================================================
		// PER-COMPANY PRINT TEMPLATES (track C). handler_print_templates.go.
		// ===================================================================
		// ⛔ WHAT WAS MISSING WAS ONLY THIS. printingdb.SaveOverrides has been
		// implemented and tested since migration 0037, and the READ side has been
		// wired the whole time — printingdb/document.go honours overrides on both
		// document paths and handler_print_reports.go honours them on the report
		// letterhead. Nothing could WRITE one: no route reached SaveOverrides and
		// print_template held zero rows in the entire database. These three lines
		// are the fix, and the printed bytes change the moment one is used.
		//
		// TWO-TOUCH RULE: inert without the matching
		// `/print-templates/` arms in routeAction() — an unmapped {companyID}
		// sub-path default-DENIES, and
		// TestEveryCompanyRouteIsMappedAndInVocabulary walks THIS router and turns
		// the suite red if a mapping is missing, so the gap cannot ship as a 403
		// for the Owner.
		//
		// The token is `company`, the same as the F11 features write above: a
		// print template is company configuration, and modelling it as report:NAME
		// would hand it to every Data Entry operator by inheritance from migration
		// 0011. The full argument is on the routeAction arm.
		//
		// DELETE is what makes the setting two-way: it removes the override row so
		// the company returns to the built-in layout. Without it, a company that
		// sets a declaration once could never go back.
		r.Get("/print-templates/{docKind}", s.handleGetPrintTemplate)
		r.Put("/print-templates/{docKind}", s.handleSavePrintTemplate)
		r.Delete("/print-templates/{docKind}", s.handleClearPrintTemplate)

		// ===================================================================
		// THE AI BILL INBOX — cored's PROXY into insightd (register row D49).
		// ===================================================================
		// Six routes, enumerated here because the handler derives its upstream
		// path from routeSuffix and therefore cannot forward anything chi did not
		// route. They are inside THIS subtree, which is the entire point: insightd
		// authenticated nobody and scoped drafts by the company_id in the path, so
		// any caller who could reach the process could hydrate any tenant's
		// supplier bills. Now the same middleware that guards the day book guards
		// the drafts, and insightd refuses anything that did not come through here.
		//
		// TWO-TOUCH RULE: these six lines are inert without the matching
		// `/drafts` arms in routeAction() — an unmapped {companyID} sub-path
		// default-DENIES, and TestEveryCompanyRouteIsMappedAndInVocabulary walks
		// this router and turns the suite red if a mapping is missing.
		//
		// ⚠️ NOT AT /companies/*. The dev proxy used to route EVERY same-origin
		// /companies/* to insightd (a namespace grab: cored's own router is
		// /v1/companies/{companyID}), so a proxy route added there would have been
		// sent to insightd instead of cored — a fix that appears to do nothing in
		// the browser while every unit test passes. These are under /v1 like
		// everything else, and vite's /companies rule is deleted.
		r.Post("/drafts", s.handleDraftProxy)
		r.Get("/drafts", s.handleDraftProxy)
		r.Get("/drafts/{draftID}", s.handleDraftProxy)
		r.Post("/drafts/{draftID}/claim", s.handleDraftProxy)
		r.Post("/drafts/{draftID}/claim/release", s.handleDraftProxy)
		r.Post("/drafts/{draftID}/accepted", s.handleDraftProxy)

		// Masters.
		// ⛔ THE READ, ADDED FOR T11. `/groups` was already in isMasterPath — which
		// maps a GET to (Display, "master"), the token /ledgers and /units read under
		// — so this route arrives GOVERNED by a rule that already exists and grants
		// nothing new: a group name is on every ledger row today. It exists because a
		// budget line on a GROUP stores an account_group_id, and no master form in
		// this product may ask a human for a uuid. See handler_groups_list.go.
		r.Get("/groups", s.handleListGroups)
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

		// T9 — THE COSTING METHOD. Switching an item from weighted average to
		// FIFO or LIFO restates every report that has ever priced it (no data
		// moves; value is derived), so it is its own narrow route with its own
		// audit row, mapped to (Alter, "master") so it inherits the back-dated
		// cutoff and the step-up. The vocabulary is SERVED because a picker
		// built from a client-side list can offer a method the reports cannot
		// value; see handler_inventory_masters.go.
		//
		// ⚠️ ORDER MATTERS AND chi's ROUTER DOES NOT REORDER FOR YOU: this is
		// the FIRST {id} sub-route under /stock-items, and the comment above
		// which said there were none is now wrong wherever it still stands.
		// A literal segment ("/stock-items") and a parameterised one
		// ("/stock-items/{stockItemID}/...") do not collide, but a second
		// literal added under the collection later would.
		r.Put("/stock-items/{stockItemID}/valuation-method", s.handleSetStockItemValuationMethod)
		r.Get("/inventory-vocabulary", s.handleInventoryVocabulary)

		// T10 — STOCK GROUPS (migration 0079). The item hierarchy, which is what
		// makes a Stock Summary readable on a company with four hundred items.
		// Masters like every other inventory master: isMasterPath maps the POST
		// to (Create, "master") and the GET to (Display, "master"), so the level
		// that may pick a godown may pick a group and the level that may not mint
		// one cannot mint a group either.
		r.Get("/stock-groups", s.handleListStockGroups)
		r.Post("/stock-groups", s.handleCreateStockGroup)

		// T8 — FOREIGN CURRENCIES AND THEIR RATES (migration 0080).
		// isMasterPath maps both to the `master` token, so the level that may
		// read a godown may read a currency, and the level that may not mint one
		// cannot decide what a dollar is worth either. See auth_middleware.go's
		// note for why a bare `currency` token was rejected.
		r.Get("/currencies", s.handleListCurrencies)
		r.Post("/currencies", s.handleCreateCurrency)
		r.Post("/currencies/rates", s.handleSetCurrencyRate)

		// THE COST MASTERS (T2, migration 0040) — the AXES of cost analysis and the
		// POSITIONS on them. A cost CATEGORY is an axis (Department, Branch, Project);
		// a cost CENTRE is a position on that axis. Several axes exist in order to
		// allocate the SAME money in parallel — one salary posting split across
		// Departments AND again, in full, across Branches — so nothing on these routes
		// ever totals across them. masters/cost_centre.go has the model.
		//
		// WITHOUT THESE ROUTES COST ALLOCATION IS UNUSABLE. cost_centre.cost_category_id
		// is NOT NULL and nothing else writes either table, so until now an axis could
		// only be created by raw SQL and the allocation picker was permanently empty —
		// the same hole /deductor-status below exists to close for TDS.
		//
		// ALL SIX CARRY THE BARE RESOURCE TOKEN `cost-centre` — read and write, axes and
		// centres alike — because they are authorities over ONE small object: the shape
		// of this company's cost analysis. They are not separable in practice either, a
		// centre must name an axis, so whoever may create one must be able to read them.
		// ⛔ NOT a report name: `report:cost-centre` is reached by migration 0011's coarse
		// `allow display report` at specificity 1 and would be granted to every seeded
		// Data Entry level. handler_cost_centre.go records the argument in full; it is the
		// same one tds-challan and tds-profile record.
		//
		// The two collections are DISTINCT LITERALS (neither is a prefix of the other),
		// and each {param} child is registered after its own collection, so there is no
		// chi literal-vs-parameter collision to order around.
		//
		// THE PUTs ARE WHOLE-ROW RESTATEMENTS AND NOT PATCHES, the same call
		// PUT .../tds-profile makes: a centre's name, parent and axis are validated
		// AGAINST EACH OTHER (the within-axis uniqueness scope, the same-axis parent rule,
		// the parent cycle), so a partial body is not checkable. ⚠️ A PUT that omits
		// parent_id therefore moves the centre to the TOP of its axis; the response echoes
		// the stored row so that is visible at once rather than on a report a month later.
		r.Get("/cost-categories", s.handleListCostCategories)
		r.Post("/cost-categories", s.handleCreateCostCategory)
		r.Put("/cost-categories/{categoryID}", s.handleRenameCostCategory)
		// T3 — ORDERS AND THE BILL OF MATERIALS (migrations 0042, 0043).
		//
		// ⛔ TWO ROUTE FAMILIES OVER ONE order_head TABLE, and the split is in the
		// PATH because routeAction sees the path and not the body: `sales-order` and
		// `purchase-order` are separate authorities (a salesperson keys what
		// customers want; a purchase clerk commits the company to spending), and a
		// `direction` body field would let a caller holding one POST the other.
		//
		// None of these is a report — an order is a MASTER-like object, and the two
		// outstanding-position REPORTS are the order books mounted further down. See
		// handler_orders.go's header.
		r.Post("/sales-orders", s.postOrder(orders.Sales))
		r.Get("/sales-orders", s.getOrders(orders.Sales))
		r.Get("/sales-orders/{orderID}", s.getOrder(orders.Sales))
		r.Put("/sales-orders/{orderID}/close", s.putOrderClose(orders.Sales))
		r.Post("/purchase-orders", s.postOrder(orders.Purchase))
		r.Get("/purchase-orders", s.getOrders(orders.Purchase))
		r.Get("/purchase-orders/{orderID}", s.getOrder(orders.Purchase))
		r.Put("/purchase-orders/{orderID}/close", s.putOrderClose(orders.Purchase))
		// The recipe. `/explode` is a GET because it WRITES NOTHING: it returns what
		// a Stock Journal's lines would be, and the operator posts that journal
		// themselves — which is what keeps a manufacture an auditable voucher rather
		// than a side effect of opening a recipe.
		r.Post("/boms", s.postBom)
		r.Get("/boms", s.getBoms)
		r.Get("/boms/{bomID}", s.getBom)
		r.Get("/boms/{bomID}/explode", s.getBomExplode)

		// T5 — the e-way bill. ⛔ FLAT, NOT /vouchers/{id}/e-way-bill: routeAction
		// reads everything after "/vouchers/" as the voucher CLASS, so the nested
		// path would map to a class nothing knows and default-deny the Owner. The
		// voucher is named in the body on POST and in the path on GET.
		// See handler_ewaybill.go's header.
		r.Post("/e-way-bills", s.postEWayBill())
		r.Get("/e-way-bills/{voucherID}", s.getEWayBill())

		// The e-invoice / IRN, and cored's PROXY into statutoryd. ⛔ FLAT for
		// exactly the reason the e-way bill is: /vouchers/{id}/e-invoice would be
		// read by routeAction as a voucher CLASS and default-deny the Owner.
		// See handler_einvoice.go's header.
		r.Post("/e-invoices", s.postEInvoice())
		r.Get("/e-invoices/{voucherID}", s.getEInvoice())

		r.Get("/cost-centres", s.handleListCostCentres)
		r.Post("/cost-centres", s.handleCreateCostCentre)
		r.Put("/cost-centres/{centreID}", s.handleAlterCostCentre)

		// THE BUDGET MASTER (T11, migration 0085). All eight routes map to the BARE
		// `budget` resource, so the seeded Data Entry level is refused every one of
		// them with no ACL migration at all — see handler_budget.go's header.
		//
		// ⚠️ `/budgets` IS PLURAL AND THE REPORT IS `/budget-variance`, one character
		// from a collision. routeAction's master arm is HasPrefix(rest, "/budgets"),
		// which "/budget-variance" does not satisfy — so the report falls through to
		// the reports arm and is governed by `report:budget-variance`. A master route
		// at "/budget" would have swallowed it and made migration 0086 dead text.
		//
		// ⛔ DELETE IS MOUNTED, unlike every other master above. Migration 0085
		// grants UPDATE and DELETE because A BUDGET IS A PLAN AND PLANS ARE REVISED,
		// which is the opposite of a voucher. It maps to authz.Delete and not to
		// Alter so "may revise a figure but may not drop a line" stays writable.
		r.Get("/budgets", s.handleListBudgets)
		r.Post("/budgets", s.handleCreateBudget)
		r.Put("/budgets/{budgetID}", s.handleAlterBudget)
		r.Delete("/budgets/{budgetID}", s.handleDeleteBudget)
		r.Get("/budgets/{budgetID}/lines", s.handleListBudgetLines)
		r.Post("/budgets/{budgetID}/lines", s.handleAddBudgetLine)
		r.Put("/budgets/{budgetID}/lines/{lineID}", s.handleAlterBudgetLine)
		r.Delete("/budgets/{budgetID}/lines/{lineID}", s.handleDeleteBudgetLine)

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
		// The conflict compare screen's two missing halves (track D). The payload
		// GET is the operator's own keyed intent, preserved when the sequencer
		// refused it; the resolve POST is the only writer of resolved_at that has
		// ever existed, without which a queue row is immortal.
		r.Get("/reconciliation-queue/{reconID}/payload", s.handleReconciliationPayload)
		r.Post("/reconciliation-queue/{reconID}/resolve", s.handleResolveReconciliation)

		// Backward-compatible alias for the original F8 Sales endpoint.
		r.Post("/sales", s.postVoucher(ledger.Sales))

		// Reports.
		r.Get("/trial-balance", s.handleTrialBalance)
		r.Get("/balance-sheet", s.handleBalanceSheet)
		r.Get("/profit-and-loss", s.handleProfitAndLoss)
		r.Get("/day-book", s.handleDayBook)
		r.Get("/outstandings", s.handleOutstandings)
		// ⛔⛔ A SECOND FLAT LITERAL ROUTE, AND THE FLATNESS IS THE WHOLE POINT (D94).
		// `routeAction` maps the literal path to the PDP resource, so a report NAME is
		// what a rule can name and a QUERY PARAMETER is invisible to it. Serving the
		// payables ageing as `?kind=PAYABLE` on the route above — which is all the
		// product could do until now — makes *"the sales clerk may see what customers
		// owe us but not what we owe suppliers"* an UNWRITABLE rule. Two names, never
		// one report with a direction toggle: the same reasoning migration 0073 and
		// `REPORT_NAMES` already carry for the two ORDER BOOKS.
		//
		// ⚠️ IT WAS ADDITIVE WHEN IT LANDED, AND D97 IS WHY IT COULD NOT STAY THAT
		// WAY. `/outstandings?kind=PAYABLE` kept answering the payables book from
		// under the RECEIVABLES rule, because it had to: the bill-wise picker was the
		// only caller that needed it, and refusing it there pushes a clerk's payment
		// ON ACCOUNT. The picker now has a door of its own (`/vouchers/{class}/
		// open-bills/{ledgerID}` below), so nothing needs the parameter any more and
		// `handleOutstandings` redirects it to the route above — see that handler.
		r.Get("/bills-payable", s.handleBillsPayable)

		// ⛔⛔ THE BILL-WISE PICKER'S OWN DOOR (D97). ONE handler, EIGHT literal
		// routes, and the repetition is load-bearing twice over.
		//
		// The CLASS is a literal segment because `routeAction` is the PDP's only view
		// of the request and it reads the path: this read is authorised as
		// `display <class>` — the authority the clerk keying that voucher already
		// holds — so the class has to be IN the path, exactly as it is for the POSTs
		// and PUTs above. A `{class}` pattern would also break
		// TestEveryCompanyRouteIsMappedAndInVocabulary, whose synthesiser hands every
		// path parameter a UUID; `isVoucherClass("<uuid>")` is false, and the route
		// would walk as unmapped.
		//
		// The LEDGER is a path segment so there is NO unscoped form: this route can
		// never answer with the whole book, whatever a caller omits.
		//
		// ⚠️ ONLY THE EIGHT ACCOUNTING CLASSES. The four non-accounting stock vouchers
		// post no ledger lines at all, so they open and settle nothing — a route for
		// them would be an authority over an empty question.
		for _, cls := range []string{
			"sales", "purchase", "receipt", "payment", "contra", "journal",
			"credit-note", "debit-note",
		} {
			r.Get("/vouchers/"+cls+"/open-bills/{ledgerID}", s.handleOpenBills)
		}

		// BANK RECONCILIATION (T4). The GET is a report; the PUT/DELETE are an
		// Alter of "bank-reconciliation" — two DIFFERENT resources, because
		// ticking one cheque off is clerical while reading the statement is the
		// company's whole bank position. routeAction maps all three and
		// migration 0046 writes the rules (allow the write to Data Entry,
		// explicitly disallow the report at specificity 2 so 0011's coarse
		// `allow display report` cannot reach it).
		//
		// The write is PUT rather than POST because it is idempotent on
		// (voucher, ledger, leg): re-stating a bank date corrects it. legNo is
		// in the path rather than the body so the route is a stable identity for
		// one line of the statement.
		r.Get("/bank-reconciliation/{ledgerID}", s.handleBankReconciliation)
		r.Put("/bank-reconciliation/{ledgerID}/{voucherID}/{legNo}", s.handleSetBankDate)
		r.Delete("/bank-reconciliation/{ledgerID}/{voucherID}/{legNo}", s.handleClearBankDate)

		// BANK STATEMENT IMPORT (T4 slice 2a). Three routes, and the middle one
		// is the design: the operator CONFIRMS how their file is to be read
		// before a row of it is stored.
		//
		//   POST .../preview  parses nothing into the database and answers with
		//                     the file's own header, a PROPOSED mapping, and
		//                     every field the proposal would not commit to.
		//   POST .../import   takes the confirmed mapping and persists.
		//   GET  .../         reads back what was imported.
		//
		// PREVIEW IS A POST THAT WRITES NOTHING, which is unusual here and is
		// deliberate: it carries a whole uploaded file, which has no business
		// in a query string or a server log. It is mapped to the same `create
		// bank-statement` action as the import — a level that may not import a
		// statement has no business rehearsing one, and the preview echoes the
		// file's contents back.
		//
		// There are NO built-in per-bank layouts behind any of this; see
		// docs/design/t4-slice2-model.md §8.2 for why a profile recalled from
		// memory is the same class of mistake as an invented tax rate.
		r.Post("/bank-statement/{ledgerID}/preview", s.handlePreviewBankStatement)
		r.Post("/bank-statement/{ledgerID}/import", s.handleImportBankStatement)
		r.Get("/bank-statement/{ledgerID}", s.handleImportedStatement)

		// T4 SLICE 2b — MATCHING. Two of its three routes are here; the third
		// and most important one is not, because ACCEPTING a match is the
		// EXISTING bank-reconciliation PUT above with a statement_line_id in the
		// body. handler_bank_match.go's header has the four properties that
		// follow from sharing that route rather than minting a new one, and
		// migration 0088's footer records why slice 2b writes no ACL migration.
		//
		// All three are mapped by routeAction's existing `/bank-statement/`
		// prefix arm: the GET as Display/report:bank-statement (which 0063
		// already disallows to Data Entry) and both POSTs as
		// Create/bank-statement (which 0063 grants Data Entry nothing of).
		r.Get("/bank-statement/{ledgerID}/matches", s.handleBankStatementMatches)
		r.Post("/bank-statement/{ledgerID}/lines/{lineID}/set-aside", s.handleSetAsideStatementLine)
		r.Post("/bank-statement/{ledgerID}/lines/{lineID}/restore", s.handleRestoreStatementLine)

		// TDS DEDUCTOR STATUS (Item Zero step 2) — Tally's F11 > Enable TDS >
		// "TDS Deductor Details" (RESEARCH.md:692). Whether this company is a
		// s.402(11) designated person / s.402(37) specified person, per tax year.
		//
		// WITHOUT THIS ROUTE THE TDS POST PATH IS UNUSABLE: it refuses every
		// voucher for a company whose status is unrecorded, and nothing else
		// writes the row. That is why this is step 2 and not a later polish.
		//
		// PUT, keyed on the tax year in the path, because there is exactly one
		// answer per year and re-stating it CORRECTS it. Read and write share the
		// `deductor-status` resource token — see handler_deductor_status.go for
		// why that is the opposite call from bank reconciliation above.
		r.Get("/deductor-status", s.handleListDeductorStatus)
		r.Put("/deductor-status/{taxYear}", s.handleSetDeductorStatus)

		// THE TDS DEDUCTOR RETURN PROFILE (T6, migration 0070) — the TAN, the
		// deductor block and the person answerable for the deduction.
		//
		// reporting's return sourcing refuses a return with ErrNoDeductorProfile
		// BEFORE it looks at a challan or a deduction, so this is the other half of
		// "a return can actually be built", and it was seeded by raw SQL until now.
		//
		// PUT with no key in the path: company_tds_profile.company_id is the whole
		// primary key, so the company already in the path prefix IS the key. A
		// WHOLE ROW rather than a PATCH because migration 0070 carries cross-field
		// CHECKs (the Annexure 5 state code and the Annexure 3 ministry are each
		// mandatory for some deductor categories and FORBIDDEN for the rest), so a
		// partial body is not checkable and could leave a stale conditional value
		// behind.
		r.Get("/tds-profile", s.handleGetTDSProfile)
		r.Put("/tds-profile", s.handleSetTDSProfile)

		// s.394(2) MANUFACTURING DECLARATIONS (Item Zero step 3). A resident
		// buyer's written statement that goods are for manufacturing and NOT for
		// trading, which exempts collection on Table Sl. Nos. 1-5.
		//
		// The post path READS this table (ledger/tcs.go) rather than taking an
		// exemption flag off the voucher body, so these two routes are the only
		// way a sale becomes exempt — which is the point: the exemption is a
		// signed document the collector holds, not an entry-time opinion.
		//
		// POST rather than PUT because the identity is a TRIPLE (buyer, section,
		// tax year) and carrying it in the body reads better than three path
		// segments. Still idempotent on that triple.
		r.Get("/tcs-declarations", s.handleListTCSDeclarations)
		r.Post("/tcs-declarations", s.handleCreateTCSDeclaration)

		// THE TDS CHALLAN (T6, migration 0069) — the deposit slip, and which
		// deductions it paid. Deductee Detail field 5 of the prescribed return is
		// a challan reference, so WITHOUT THIS ROUTE NO RETURN CAN BE FILED for
		// any deduction at all: before it the only way to record a deposit was raw
		// SQL, which is how internal/reporting's tests still seed one.
		//
		// POST, and no PUT/PATCH/DELETE: a challan is an EVENT at a bank counter,
		// not a row an operator maintains. Re-posting one CIN is a 409 and NEVER an
		// upsert — the second post is either a double entry or a second real
		// deposit whose serial was mistyped, and overwriting either loses money.
		//
		// The GET takes ?tax_year_start= and REFUSES to default it: a deposit made
		// on 7 April normally belongs to the tax year that ended a week earlier,
		// which is why migration 0069 STORES the year on the challan instead of
		// deriving it from the date. Read and write share the `tds-challan`
		// resource token, which is a BARE token and not a report name — see
		// handler_tds_challan.go for why that is a safety decision about migration
		// 0011 rather than a matter of style.
		//
		// ⛔ AND THE SECOND POST IS THE ALLOCATE-LATER (register row D1). It was
		// missing, and its absence was not a gap in a form: a deposit may
		// legitimately be recorded naming fewer deductions than it paid for, and
		// nothing could then attach the rest — the audit drove seven in-browser
		// doors and six HTTP methods at challan 05130 and its unallocated balance
		// stayed at exactly 12000.0000, while the return refused the unattached
		// deduction as undeposited. It is an ADD (POST) and not an edit: it creates
		// links that were not there, and cannot move, change or remove one.
		//
		// It needs NO routeAction arm of its own — that case matches the
		// `/tds-challans` PREFIX, so read and write of every path under it share
		// the one `tds-challan` token, which is the arrangement that keeps one ACL
		// rule governing the whole surface.
		//
		// ⚠️ IT DOES NEED THE CUTOFF, and it gets it from the STORED deposit date
		// of the challan named in the path (storedChallanDepositDate). The body
		// carries no date at all — see tdsChallanAllocateReq — so the round-1/2
		// body peeks cannot see this act, and the round-3 rule applies instead:
		// judge the date the books already hold. Attaching a deduction to a deposit
		// dated inside a locked period changes what that quarter's return says it
		// paid for, which is exactly what the lock exists to freeze.
		r.Get("/tds-challans", s.handleListTDSChallans)
		r.Post("/tds-challans", s.handleCreateTDSChallan)
		r.Post("/tds-challans/{challanID}/allocations", s.handleAllocateToTDSChallan)

		// THE STATUTORY SECTION VOCABULARY (register row D3). What a voucher's
		// `tds` / `tcs` block may name, with the rate and threshold attached.
		//
		// ⛔ IT IS MAPPED AS A MASTER (isMasterPath), NOT AS A REPORT, and that
		// is the whole access decision: the DATA ENTRY level keys the vouchers
		// that carry a deduction, and it is DENIED report:tds-register by
		// migration 0068 for a good reason (that register is every supplier paid,
		// with amounts). Mapping this list as a report would deny the picker to
		// exactly the operator who needs it and leave D3 open under a different
		// error. A statutory rate table is a master list — it names no party, no
		// amount and nothing about this company — so it reads on the same terms
		// as /ledgers and /units, which the same operator already reads to key
		// the voucher this list belongs to.
		//
		// GET ONLY. There is no POST: the table is the Finance Act's, not the
		// tenant's, and an unmounted POST answers 404 "no such route" (D40).
		r.Get("/tds-sections", s.handleTDSSections)

		// THE TDS/TCS REGISTER (Item Zero step 4) — everything deducted and
		// collected in a period, per deductee and per buyer.
		//
		// A FLAT LITERAL path segment equal to the report name, per
		// PLAN-TALLY-PARITY.md §2.4: TestReportVocabularyMatchesRouter walks
		// authz.KnownReportNames and asserts this route resolves for every one.
		// Migration 0068 disallows it for Data Entry at specificity 2 — it lists
		// every supplier paid and customer collected from, with amounts.
		r.Get("/tds-register", s.handleTDSRegister)

		// THE TWO COST REPORTS (T2, migration 0040) — "which department bore this
		// cost", and "what am I missing".
		//
		// FLAT LITERAL segments equal to their report names, per
		// PLAN-TALLY-PARITY.md §2.4: TestReportVocabularyMatchesRouter walks
		// authz.KnownReportNames and asserts GET /v1/companies/{id}/<name>
		// resolves for every one, so a tidier /cost-categories/{id}/summary would
		// leave a vocabulary entry no route serves — a `disallow` an administrator
		// can write that can never match.
		//
		// ⛔ THE AXIS IS A QUERY PARAMETER (?cost_category_id=), the way
		// stock-item-vouchers takes stock_item_id, and it is REQUIRED. A cost
		// category is an AXIS and the same money is allocated IN FULL on every
		// axis, so defaulting it would render a real, correctly-footed report for
		// an axis nobody chose, and summing the axes would report a multiple of
		// the cost actually borne. The refusal names the axes this company has —
		// see handler_cost_summary.go.
		//
		// Both are a Display, so neither inherits the back-dated write cutoff or
		// the WebAuthn step-up (both gate on Action.IsWrite()). Both carry the
		// company's internal cost structure, which is why the ACL counterweight
		// migration disallows them for the seeded Data Entry level at
		// specificity 2.
		r.Get("/cost-centre-summary", s.handleCostCentreSummary)
		r.Get("/cost-category-summary", s.handleCostCategorySummary)

		// BUDGET VS ACTUAL (T11, migrations 0085/0086). A FLAT LITERAL PATH SPELLING
		// THE REPORT NAME, because TestReportVocabularyMatchesRouter asks routeAction
		// for GET /v1/companies/{id}/<name> for every entry in
		// authz.KnownReportNames — a tidier /budgets/{id}/variance would be a
		// vocabulary entry no route serves, i.e. a `disallow` an administrator can
		// write that can never match. The plan, the grain and the axis all ride in
		// the query string for that reason.
		//
		// A Display, so it inherits neither the back-dated write cutoff nor the
		// WebAuthn step-up (both gate on Action.IsWrite()). It carries what the owner
		// PLANNED — the one figure in this product no voucher contains — which is why
		// migration 0086 disallows it for the seeded Data Entry level in both the
		// display and the print form.
		r.Get("/budget-variance", s.handleBudgetVariance)

		// THE TWO ORDER BOOKS (T3, migration 0042). What is still outstanding, per
		// direction, as at a date.
		//
		// TWO FLAT LITERAL ROUTES AND NOT ONE WITH ?direction=, because the PDP
		// decides on the report NAME and never sees the query string: one shared
		// name would make "the salesperson may see our sales pipeline but not the
		// purchase commitments" unwritable. See handler_orderbook.go's header.
		//
		// Both are a Display, so neither inherits the back-dated write cutoff or
		// the WebAuthn step-up (both gate on Action.IsWrite()). Both are governed
		// at specificity 2 by migration 0073, which disallows display AND print for
		// the seeded Data Entry level: an order book is every commitment made and
		// received, by counterparty, with quantities and agreed rates.
		r.Get("/sales-order-book", s.handleOrderBook(reporting.SalesOrders))
		r.Get("/purchase-order-book", s.handleOrderBook(reporting.PurchaseOrders))

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
		// THE ANNUAL RECONCILIATION (T12). Both touches of the two-touch rule:
		// this mount, and `case "/gstr9"` in reportName() in auth_middleware.go.
		// With only this one, even an Owner gets a 403.
		r.Get("/gstr9", s.handleGSTR9)

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
		// ⛔ THE TWO EFFECTIVE-DATED CHILDREN, WITHOUT WHICH THE EIGHT ROUTES
		// ABOVE AND BELOW COULD NOT PAY ANYBODY. `payrolldb.SetSalaryComponent`
		// and `SetStatutoryElection` shipped with Track 3 and had no route at
		// all, so an employee created through POST /employees hit
		// "no salary structure is in force" / "no statutory election is in
		// force" on every pay run — a 422 arriving on the RUN, long after the
		// master was created, with nothing anywhere saying it was incomplete.
		// Every payroll test in this package set both up by calling the SERVICE,
		// which is exactly why a green suite never noticed. See
		// handler_payroll_structure.go's header.
		//
		// They resolve to the SAME `employee` authority as the master itself
		// (Create for the writes, Display for the reads), so migration 0035's
		// seeded `disallow create|alter|display employee` covers them from the
		// moment they exist and no new token — granted by nothing, denied by
		// nothing — guards a salary.
		r.Post("/employees/{employeeID}/salary-components", s.handleSetSalaryComponent)
		r.Get("/employees/{employeeID}/salary-components", s.handleListSalaryComponents)
		r.Post("/employees/{employeeID}/statutory-elections", s.handleSetStatutoryElection)
		r.Get("/employees/{employeeID}/statutory-elections", s.handleListStatutoryElections)
		// The closed sets a payroll form chooses from — served, not hardcoded in
		// the client, for the reason /security-levels/vocabulary is: `is_wages`
		// is a LEGAL classification and a second copy of it in another language
		// is the drift that under-contributes PF with no error anywhere.
		r.Get("/payroll-vocabulary", s.handlePayrollVocabulary)
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

		// PRINT (track T1: handler_print.go, handler_print_reports.go).
		//
		//	GET /print/vouchers/{class}/{voucherID} -> (print, <class>)
		//	GET /print/reports/{reportName}         -> (print, report:<name>)
		//
		// EVERY CLASS AND EVERY REPORT NAME IS REGISTERED AS A LITERAL SEGMENT,
		// drawn from authz.KnownVoucherClasses and authz.KnownReportNames. That is
		// not verbosity, it is what keeps the closure gates honest:
		//
		//   * TestEveryCompanyRouteIsMappedAndInVocabulary walks THIS router and
		//     substitutes a UUID for every {param}. A route registered as
		//     /print/vouchers/{class}/{voucherID} would be walked as
		//     /print/vouchers/<uuid>/<uuid>, routeAction would (correctly) refuse
		//     to map an unknown class, and the gate would fail on a route that is
		//     perfectly fine in production — or, worse, would be "fixed" by making
		//     routeAction map an arbitrary segment, which is the default-deny
		//     property gone.
		//   * With literals, the ROUTER ITSELF is the closed vocabulary and the
		//     walk exercises the REAL (print, sales) ... (print, report:day-book)
		//     pairs. It is the same thing /vouchers/sales/{voucherID} does.
		//
		// EVERY class is registered, including the seven that print no document at
		// all (Purchase, Contra, Journal, Payroll, Receipt Note, Stock Journal,
		// Physical Stock). They are mounted so the handler can answer with
		// printingdb's ErrNoDocumentForClass, which NAMES the class and says why —
		// "a Purchase is the SUPPLIER's invoice to issue, not ours". An unmounted
		// path would instead produce a bare 403 from routeAction's default-deny,
		// which reads as an authorization bug and sends somebody hunting for a
		// missing ACL rule.
		//
		// Print is NOT a write (authz.Print.IsWrite() is false), so neither route
		// inherits the back-dated write cutoff or the Owner level's WebAuthn
		// step-up. See routeAction's print arm for why that is deliberate.
		for _, class := range authz.KnownVoucherClasses {
			r.Get("/print/vouchers/"+string(class)+"/{voucherID}", s.handlePrintVoucher)
		}
		for _, name := range authz.KnownReportNames {
			r.Get("/print/reports/"+name, s.handlePrintReport)
		}

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

	// AllowNegativeStock is the inventory escape hatch. A POINTER for the same
	// reason DecimalPlaces is one -- false is a meaningful setting, not an
	// absence -- and omitted means "take the column default", which is false.
	//
	// ⛔ THIS KEY WAS ACCEPTED AND DISCARDED FOR THE WHOLE LIFE OF THE ROUTE
	// (register row D43). decodeJSON does not set DisallowUnknownFields (it is
	// shared by ~40 handlers, so turning it on here is not a local change), so
	// an unknown key is silently dropped and the caller still gets a 201. That
	// made the company create answer "applied" to a setting it had thrown away,
	// measured on the wire 2026-08-22: POST with allow_negative_stock:true ->
	// 201, and `SELECT allow_negative_stock FROM company` -> f.
	AllowNegativeStock *bool `json:"allow_negative_stock"`
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

		AllowNegativeStock: req.AllowNegativeStock,
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

	// CreditDays is the party's agreed payment term, and it is what makes an
	// Outstandings ageing bucket mean anything for a bill with no explicit due
	// date. A POINTER because the column is NULLABLE and NULL is a real,
	// distinct state — "no term agreed" is not "due immediately", and a plain
	// int would make every party that never stated one look like a cash party.
	//
	// ⛔ THE MASTERS LAYER HAS CARRIED THIS SINCE IT WAS WRITTEN AND THE WIRE
	// DROPPED IT — measured 2026-08-22, not inferred: POST a ledger with
	// `"credit_days": 45`, get 201, and `SELECT credit_days` reads NULL.
	// masters.Ledger.CreditDays existed and the INSERT bound it; there was
	// simply no field here to fill it from, so it was nil on every create. That
	// is D43's shape on a second route, and it is why D87's form could not have
	// carried the field honestly without this line.
	CreditDays *int `json:"credit_days"`

	CreatedBy string `json:"created_by"`

	// Address / City / Pincode are the party's POSTAL BLOCK, named to match the
	// company-profile request body (mailing_name / address / city / pincode) so
	// one client-side form shape serves both. `address` is a NEWLINE-SEPARATED
	// BLOCK, not a single line.
	//
	// These are MASTER FIELDS ON AN EXISTING RESOURCE, deliberately: no new
	// route, no new ACL action, no renamed field. A party's address is not a
	// separate thing you can be separately authorised for — whoever may create
	// the party may state where it is.
	//
	// ALL THREE ARE OPTIONAL, and omitting them is not an error here. Rule 46(d)
	// makes the recipient's address mandatory ON A TAX INVOICE, and that is
	// where it is enforced (printing.ValidateRule46); a party master with no
	// address is legal, because a walk-in cash customer below the Rule 46(e)
	// fifty-thousand threshold has none. Refusing the create instead would make
	// this endpoint enforce a threshold it cannot see.
	Address string `json:"address"`
	City    string `json:"city"`
	Pincode string `json:"pincode"`

	// BankAccountNumber and BankIFSC are the company's OWN details for a BANK
	// ledger (migration 0064). Master fields on an existing resource, for the
	// same reason the address is: whoever may create the bank ledger may state
	// which account it is.
	//
	// THEY SHIP WITH THIS ROUTE RATHER THAN AFTER IT, and that is invariant
	// I-11 rather than convenience — columns and data together. Adding the
	// columns with no way to fill them would put two permanently blank fields
	// on the reconciliation screen, and a blank labelled field reads as "this
	// bank has no IFSC" rather than "nobody has typed it yet".
	//
	// Both optional: a non-bank ledger has neither, and a bank ledger whose
	// operator has not typed them yet is a legal state the screen reports as
	// NOT RECORDED.
	BankAccountNumber string `json:"bank_account_number"`
	BankIFSC          string `json:"bank_ifsc"`

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
		CreditDays:         req.CreditDays,
		Address:            req.Address,
		City:               req.City,
		PIN:                req.Pincode,
		BankAccountNumber:  req.BankAccountNumber,
		BankIFSC:           req.BankIFSC,
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
	// A COMPOUND unit (T10): 1 <symbol> = <conversion_factor> <base_unit_symbol>,
	// e.g. 1 BOX = 12 NOS. Both or neither — a base with no factor cannot be
	// converted and a factor with no base converts to nothing, and either half
	// alone is a unit that looks compound to a reader and cannot be restated.
	//
	// ⚠️ conversion_factor IS A DECIMAL STRING, like every other number on this
	// wire. A JSON number would be a float, and a float conversion factor is how
	// 1 BOX quietly becomes 11.999999 NOS.
	BaseUnitSymbol   string `json:"base_unit_symbol"`
	ConversionFactor string `json:"conversion_factor"`
	CreatedBy        string `json:"created_by"`
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
	factor, err := optionalDecimal(req.ConversionFactor)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid conversion_factor (send a decimal STRING)")
		return
	}
	id, err := s.masters.CreateUnit(ctx, masters.Unit{
		Symbol:           req.Symbol,
		FormalName:       req.FormalName,
		DecimalPlaces:    req.DecimalPlaces,
		BaseUnitSymbol:   strings.TrimSpace(req.BaseUnitSymbol),
		ConversionFactor: factor,
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
	// ValuationMethod is the item's COSTING METHOD (T9). Absent means the
	// default, WEIGHTED_AVG, which is what every item created before T9 carries
	// and what Tally defaults to.
	//
	// ⛔ IT IS ACCEPTED HERE RATHER THAN ONLY ON THE ALTER ROUTE ON PURPOSE. An
	// item's method decided at creation values it from its first purchase; a
	// method that could only be set afterwards would mean every item's first
	// figures were on a basis nobody chose, and correcting it later would be a
	// RESTATEMENT of reports that had already been read. Register row D43 is the
	// nearby precedent for the other half of this: a body key that is accepted
	// and discarded is worse than one that is refused.
	ValuationMethod string `json:"valuation_method"`
	// StockGroupName is the item's STOCK GROUP (T10). Absent or empty leaves the
	// item UNGROUPED, which is a real state (Tally's "under Primary") and what
	// every item created before T10 is.
	//
	// ⚠️ THE GROUP MUST ALREADY EXIST. Creating one implicitly from a misspelling
	// here is how a company ends up with "Raw Materials" and "Raw materials"
	// subtotalling half its stock each.
	StockGroupName string `json:"stock_group_name"`
	CreatedBy      string `json:"created_by"`
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
		Name:            req.Name,
		UnitSymbol:      req.UnitSymbol,
		HSN:             req.HSN,
		GSTRatePct:      rate,
		OpeningQty:      qty,
		OpeningRate:     openRate,
		ValuationMethod: strings.TrimSpace(req.ValuationMethod),
		StockGroupName:  strings.TrimSpace(req.StockGroupName),
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

// costAllocationReq is ONE cost-centre allocation of ONE posting, on the wire.
//
// ⛔⛔ WITHOUT THIS TYPE THE WHOLE COST-CENTRE WRITE PATH IS UNREACHABLE, and that
// is exactly the defect ItemLedgerID records two fields below: the validation, the
// hashed payload and the posting_cost_allocation insert all existed, were tested,
// and could be reached ONLY from a Go test. Every client — the UI, the API, an
// offline node — could create axes and centres and read both reports, and had no
// way to allocate anything, so both reports could only ever render empty.
//
// ⚠️ posting_seq IS A 0-BASED ORDINAL AND NO SCREEN DISPLAYS IT. It indexes THIS
// request's own `postings` array, so a client fills it in from the row the operator
// is already looking at; a human must never type or count it. An ITEM INVOICE
// refuses allocations outright (its postings are generated inside the transaction
// and index 0 is the PARTY leg), so this field is only meaningful on a class that
// states its own postings.
//
// Carried on voucherReq, like Inventory and PhysicalCount, so POST /sync/commit
// ships an allocated voucher from an offline node for free: syncIntentReq.Payload
// IS a voucherReq and buildVoucherInput is the single translator. A second
// allocation type on the sync wire would compile, decode, and silently drop every
// split an offline node ever recorded.
type costAllocationReq struct {
	PostingSeq     int    `json:"posting_seq"`
	CostCategoryID string `json:"cost_category_id"`
	CostCentreID   string `json:"cost_centre_id"`
	Amount         string `json:"amount"`
}

// fulfilmentReq is ONE inventory line of this voucher consuming ONE order line —
// "line 1 of this delivery note ships 5 of the 20 sales order SO-14 committed"
// (T3, migration 0042).
//
// ⚠️ line_no IS 1-BASED, WHERE costAllocationReq.posting_seq DIRECTLY ABOVE IS
// 0-BASED, and the two sit in the same request body. The asymmetry is inherited
// rather than chosen: stock_movement.line_no has been a 1-based column since
// migration 0025 and the hashed CanonicalStockLine has carried it as such ever
// since, while a posting sequence indexes a JSON array that has never been a
// column. Spelling the same voucher line two ways inside one payload would be
// worse. ledger.validateFulfilments says so IN THE ERROR MESSAGE, because a
// client that sends an index here will be exactly one out and the resulting
// foreign-key violation would name a constraint rather than the mistake.
//
// ⚠️ AND UNLIKE A COST ALLOCATION, THIS IS HONOURED ON AN ITEM INVOICE. An
// allocation is refused there because an item invoice's postings are GENERATED
// inside the transaction, so a client-supplied posting index names the wrong leg.
// Its inventory is the operator's own grid on every class, so a line number here
// is a fact the client stated. That matters: an invoice fulfilling an order
// directly, with no separate delivery note, is one of the two ordinary ways an
// order is closed.
//
// Carried on voucherReq, like Inventory, PhysicalCount and CostAllocations, so
// POST /sync/commit ships an order-linked voucher from an offline node for free:
// syncIntentReq.Payload IS a voucherReq and buildVoucherInput is the single
// translator. A second fulfilment type on the sync wire would compile, decode,
// and silently drop every order link an offline node ever recorded.
type fulfilmentReq struct {
	LineNo      int    `json:"line_no"`
	OrderLineID string `json:"order_line_id"`
	Qty         string `json:"qty"`
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
	// CostAllocations is how this voucher's postings are split across cost centres
	// (T2, migration 0040). See costAllocationReq above for why it must exist at all
	// and why posting_seq is never a human's to supply.
	//
	// The SEMANTICS are deliberately not re-checked here — ledger.validateCostAllocations
	// owns the per-(posting, CATEGORY) sum, the sequence bounds and the duplicate
	// check, and returns a domain message. A second copy of those rules in the HTTP
	// layer is a copy that drifts, and the drifted one always wins for whichever
	// caller reaches it first.
	CostAllocations []costAllocationReq `json:"cost_allocations"`
	// Fulfilments is which order lines this voucher's inventory consumed (T3,
	// migration 0042). See fulfilmentReq above for why line_no is 1-based while
	// the cost allocation beside it is 0-based, and why an item invoice honours
	// this one.
	//
	// ⛔ THIS FIELD IS THE WHOLE FEATURE'S REACHABILITY. Until `cost_allocations`
	// was added directly above, T2's validation, hashed-payload entry and row
	// inserts were reachable ONLY from Go: no HTTP body could carry an allocation,
	// both report routes answered 200, and they were CORRECT — accurately
	// reporting that nothing had been allocated, because nothing could be. Nothing
	// failed and no test noticed, because every layer under test was fine.
	// `item_ledger_id` on this same struct carries a comment describing the
	// identical defect from an earlier feature. It has now happened twice, one
	// feature apart, and it is why this field was written in the same sitting as
	// ledger.FulfilmentInput rather than left to whoever built the screens.
	//
	// The SEMANTICS are deliberately not re-checked here — ledger.validateFulfilments
	// owns the line bounds, the duplicate-pair check and the per-line "cannot
	// discharge more than it moved" sum, and returns a domain message; migration
	// 0042's deferred trigger owns the three invariants that need a database row
	// (same item, right direction, cumulative commitment). A second copy of any of
	// those rules in the HTTP layer is a copy that drifts, and the drifted one
	// always wins for whichever caller reaches it first.
	Fulfilments []fulfilmentReq `json:"fulfilments"`
	// DraftID is the insightd PurchaseVoucherDraft this POST is ACCEPTING
	// (migration 0077, register row §7.3 B). Empty on every hand-keyed voucher,
	// which is nearly all of them.
	//
	// ⛔⛔ THIS FIELD IS THE SERVER HALF OF "ACCEPT AN AI BILL ONCE", AND WITHOUT
	// IT THE GUARANTEE IS A CLIENT-SIDE CLAIM. Accepting a draft is two writes to
	// two services: this POST, and then the browser telling insightd the draft is
	// `accepted`. When the second is lost — tab closed, network dropped, laptop
	// asleep — insightd keeps offering the bill as `ready`. The browser's own
	// retry queue is localStorage, so ONLY the machine that posted can drive it;
	// a second machine used to be protected by a day-book lookup alone, which
	// cannot see a bill whose supplier invoice number the OCR never read. Carrying
	// the draft id here puts the decision under a unique index inside the SAME
	// transaction as the voucher, so the loser writes nothing at all and gets a
	// 409 naming the voucher the draft did produce.
	//
	// Carried on voucherReq rather than on a purchase-only body for the same
	// reason Inventory and CostAllocations are: syncIntentReq.Payload IS a
	// voucherReq and buildVoucherInput is the single translator, so an accept
	// replayed from an offline node keeps its once-only key for free.
	DraftID string  `json:"draft_id"`
	GST     *gstReq `json:"gst"`
	TDS     *tdsReq `json:"tds"`
	// TCS is the collection-side mirror, honoured on Sales only. It carries no
	// exemption flag: a s.394(2) declaration is read from the declarations table
	// inside the post transaction, never asserted here.
	TCS *tcsReq `json:"tcs"`
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
			// THE DUPLICATED AI BILL — 409, not 422, and in insightd's OWN refusal
			// SHAPE. See writeDraftAlreadyAccepted.
			var dup *ledger.DraftAlreadyAcceptedError
			if errors.As(err, &dup) {
				writeDraftAlreadyAccepted(w, dup)
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

// handleReconciliationPayload returns the intent PARKED on one open
// reconciliation entry — the voucher body an offline operator keyed, preserved
// verbatim when the sequencer refused it. It is the right-hand pane of the
// conflict compare screen; the left-hand pane is GET /vouchers/{voucherID},
// which already exists and already returns real ledger ids.
//
// ⛔ WHY IT EXISTS AT ALL. sync.Service.ConflictPayload has read this column
// since migration 0010 and had NO caller outside two Go test files, so a client
// could learn THAT its work was refused and never WHAT it had typed. Without
// this route the conflict screen can only ever show one side of the comparison
// it exists to make.
//
// ⚠️ has_payload=false IS A REAL ANSWER, NOT AN ERROR, AND THE ASYMMETRY IS
// DELIBERATE. Only sync.parkConflict writes the payload column; a FROZEN row is
// raised by the ledger's freeze rule (kind IRN / FILED_GSTR1) and carries NO
// payload. So the honest answer for a freeze row is "this entry exists and never
// held one", which a 404 would spell the same way as "no such entry". The client
// must source the operator's keyed version from its own outbox in that case, and
// say so when the local copy is gone.
//
// The entry metadata is returned alongside so the compare screen needs ONE call:
// which op was attempted, what pinned it, and the sequencer's own explanation.
func (s *Server) handleReconciliationPayload(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	reconID, err := uuid.Parse(chi.URLParam(r, "reconID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid reconciliation id")
		return
	}

	// Locate the entry among the OPEN rows first. This reuses the tested lister
	// rather than adding a second query, and it pins the contract to open rows:
	// ConflictPayload alone cannot tell an unknown id from a null payload (both
	// answer (zero, false, nil)), and conflating those is exactly the confusion
	// the has_payload flag exists to remove.
	rows, err := s.ledger.ListReconciliationQueue(ctx)
	if err != nil {
		if errors.Is(err, db.ErrNoCompany) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var entry *ledger.ReconEntry
	for i := range rows {
		if rows[i].ID == reconID {
			entry = &rows[i]
			break
		}
	}
	if entry == nil {
		writeError(w, http.StatusNotFound, "no open reconciliation entry with that id")
		return
	}

	body := map[string]any{
		"reconciliation_id": entry.ID.String(),
		"voucher_id":        entry.VoucherID.String(),
		"attempted_op":      entry.AttemptedOp,
		"kind":              entry.Kind,
		"ref_value":         entry.Ref,
		"frozen_voucher_no": entry.FrozenVoucherNo,
		"detail":            entry.Detail,
		"created_at":        entry.CreatedAt,
	}

	in, hasPayload, err := s.sync.ConflictPayload(ctx, reconID)
	if err != nil {
		if errors.Is(err, db.ErrNoCompany) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	body["has_payload"] = hasPayload
	if !hasPayload {
		// Name the reason rather than leaving a bare false: a freeze row never had
		// a payload, and a client that cannot tell that from a fault would retry.
		body["payload_absent_because"] = "this entry was raised by the freeze rule, " +
			"which parks no keyed payload; the operator's own version exists only on the node that keyed it"
		writeJSON(w, http.StatusOK, body)
		return
	}
	body["intent"] = syncIntentJSON(in)
	writeJSON(w, http.StatusOK, body)
}

// syncIntentJSON serialises a parked sync.Intent for the compare screen.
//
// ⛔⛔ IT DOES NOT RETURN THE STORED BYTES, AND THAT IS THE WHOLE POINT. The
// column holds json.Marshal(sync.Intent) verbatim, which is the tempting thing
// to hand back — nothing omitted, nothing reformatted. MEASURED, that shape
// renders a posting of 1180 as "Amount":"1180", because ledger.VoucherInput
// carries no json tags and decimal.Decimal marshals bare. The LIVE side of the
// comparison, voucherFullJSON, renders the same money as "1180.0000" via
// StringFixed(4).
//
// So a compare screen fed the raw bytes would show "1180" beside "1180.0000" and
// invite an operator to discard their work over a difference that does not
// exist. Money is formatted HERE the way the live pane formats it, and the two
// panes are only comparable because of that.
func syncIntentJSON(in sync.Intent) map[string]any {
	return map[string]any{
		"idempotency_key":   in.IdempotencyKey,
		"client_voucher_id": in.ClientVoucherID.String(),
		"op":                string(in.Op),
		"class":             string(in.Class),
		"provisional_no":    in.ProvisionalNo,
		"node_id":           in.NodeID,
		"base_version":      in.BaseVersion,
		"hlc": map[string]any{
			"wall_millis": in.HLC.WallMillis,
			"counter":     in.HLC.Counter,
			"node_id":     in.HLC.NodeID,
		},
		"payload": parkedVoucherJSON(in.Payload),
	}
}

// parkedVoucherJSON renders the parked ledger.VoucherInput with the SAME
// conventions voucherFullJSON uses for a stored voucher: money as fixed-4
// strings, canonical "D"/"C", zero uuids as "". See syncIntentJSON for why
// re-formatting rather than echoing the stored bytes is the correct choice.
//
// ⚠️ THE FIELDS IT DOES NOT RENDER ARE NAMED, NOT DROPPED. A VoucherInput
// carries six optional blocks (inward document, cost allocations, order
// fulfilments, GST/TDS/TCS parameters) that a two-pane diff has no room for.
// Omitting them silently is the failure this product keeps paying for: the
// operator compares two panes that look identical and differ. So each one that
// is PRESENT is named in `also_carries`, and the screen can say so.
//
// TestParkedVoucherAccountsForEveryVoucherInputField reflects over the struct
// and fails if a field is added that is neither rendered here nor named there —
// so a new field cannot go silently missing from the comparison.
func parkedVoucherJSON(v ledger.VoucherInput) map[string]any {
	postings := make([]map[string]any, 0, len(v.Postings))
	for _, p := range v.Postings {
		postings = append(postings, map[string]any{
			"ledger_id": p.LedgerID.String(),
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
			"amount":    b.Amount.StringFixed(4),
		})
	}
	inventory := make([]map[string]any, 0, len(v.Inventory))
	for _, l := range v.Inventory {
		inventory = append(inventory, map[string]any{
			"stock_item_id": l.StockItemID.String(),
			"godown_id":     uuidOrEmpty(l.GodownID),
			"batch_id":      uuidOrEmpty(l.BatchID),
			"direction":     string(l.Direction),
			"qty":           l.Qty.StringFixed(4),
			"rate":          l.Rate.StringFixed(4),
			"discount_pct":  l.DiscountPct.StringFixed(4),
			"amount":        l.Amount.StringFixed(4),
			// A line that said NOTHING about price is not a line priced at zero,
			// and the two must not read the same in a comparison.
			"rate_unstated": l.RateUnstated,
		})
	}
	counts := make([]map[string]any, 0, len(v.PhysicalCount))
	for _, c := range v.PhysicalCount {
		counts = append(counts, map[string]any{
			"stock_item_id": c.StockItemID.String(),
			"godown_id":     uuidOrEmpty(c.GodownID),
			"batch_id":      uuidOrEmpty(c.BatchID),
			"qty":           c.Qty.StringFixed(4),
			"rate":          c.Rate.StringFixed(4),
		})
	}

	// Presence of the blocks a diff cannot show. Named, never dropped.
	var also []string
	if v.Inward != nil {
		also = append(also, "inward document")
	}
	if len(v.CostAllocations) > 0 {
		also = append(also, "cost allocations")
	}
	if len(v.Fulfilments) > 0 {
		also = append(also, "order fulfilments")
	}
	if v.GST != nil {
		also = append(also, "GST parameters")
	}
	if v.TDS != nil {
		also = append(also, "TDS parameters")
	}
	if v.TCS != nil {
		also = append(also, "TCS parameters")
	}
	if also == nil {
		also = []string{}
	}

	return map[string]any{
		"class":            string(v.Class),
		"voucher_type_id":  uuidOrEmpty(v.VoucherTypeID),
		"party_ledger_id":  uuidOrEmpty(v.PartyLedgerID),
		"item_ledger_id":   uuidOrEmpty(v.ItemLedgerID),
		"voucher_no":       v.VoucherNo,
		"voucher_date":     v.VoucherDate,
		"narration":        v.Narration,
		"party":            v.Party,
		"created_by":       v.CreatedBy,
		"source_guid":      v.SourceGUID,
		"source_draft_id":  v.SourceDraftID,
		"postings":         postings,
		"bill_allocations": bills,
		"inventory":        inventory,
		"physical_count":   counts,
		"also_carries":     also,
	}
}

// handleResolveReconciliation marks one open reconciliation entry handled.
//
// ⛔ WHY IT EXISTS. resolved_at has been in the schema since migration 0009 and
// every read filters on it, but NOTHING in this codebase ever wrote it — so a
// reconciliation row was immortal and the queue could only grow. See
// ledger.ResolveReconciliation.
//
// It is an Alter in the PDP (resource "reconciliation"), so it is a WRITE and
// inherits whatever step-up the level requires. It carries no body and asserts
// no date, so the back-dated cutoff correctly does not apply: clearing a
// workflow item moves nothing in or out of a locked period.
//
// A second resolve of the same id answers 404, not 200 — the row is no longer
// open. That makes the route idempotent in EFFECT (the timestamp never moves
// forward) while still telling a client that its view is stale.
func (s *Server) handleResolveReconciliation(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	reconID, err := uuid.Parse(chi.URLParam(r, "reconID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid reconciliation id")
		return
	}
	stamped, err := s.ledger.ResolveReconciliation(ctx, reconID)
	if err != nil {
		if errors.Is(err, db.ErrNoCompany) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !stamped {
		writeError(w, http.StatusNotFound, "no open reconciliation entry with that id")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"reconciliation_id": reconID.String(),
		"resolved":          true,
	})
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
	// ---- THE TWO ROUND-TRIP FIELDS THAT WERE MISSING FROM THIS BODY.
	//
	// ⛔⛔ ledger.VoucherView HAS CARRIED CostAllocations SINCE T2 AND THIS FUNCTION
	// DROPPED THEM. The service layer projected them, its own doc comment explains at
	// length why omitting them from an alter DESTROYS a departmental split — and then
	// the HTTP body left them out, so every client rebuilding a voucher from this GET
	// silently cleared the split on an unrelated edit. The Go struct was right and the
	// wire was not, which is the same shape as T2's other defect one layer up
	// (`VoucherRequest` in the TS types lacked `cost_allocations` while the Go struct
	// had it) — and it is why the rule is EVERY wire between the operator and the
	// table, not just the nearest one.
	//
	// `fulfilments` is the same field one further along, and it was absent from the
	// service layer too. Both are emitted here now, and both ALWAYS (never omitted
	// when empty): a client that rebuilds an alter body needs to distinguish "this
	// voucher has no links" from "this endpoint does not tell me about links", and an
	// absent key cannot say which. An empty array is the honest answer, and it is the
	// one an alter can safely restate.
	allocations := make([]map[string]any, 0, len(v.CostAllocations))
	for _, a := range v.CostAllocations {
		allocations = append(allocations, map[string]any{
			// 0-BASED, and an index into THIS body's `postings` array — see
			// ledger.CostAllocationView, which re-derives it against the same slice
			// this read returns. ⚠️ It is 0-based where `fulfilments[].line_no`
			// below is 1-based. Both are inherited and neither is negotiable.
			"posting_seq":        a.PostingSeq,
			"cost_category_id":   a.CostCategoryID.String(),
			"cost_category_name": a.CostCategoryName,
			"cost_centre_id":     a.CostCentreID.String(),
			"cost_centre_name":   a.CostCentreName,
			"amount":             a.Amount.StringFixed(4),
		})
	}
	fulfilments := make([]map[string]any, 0, len(v.Fulfilments))
	for _, f := range v.Fulfilments {
		fulfilments = append(fulfilments, map[string]any{
			// 1-BASED, indexing `inventory` at line_no-1. STORED, not re-derived:
			// order_fulfilment.line_no is a real column, unlike a posting's ordinal.
			"line_no":       f.LineNo,
			"order_id":      uuidOrEmpty(f.OrderID),
			"order_line_id": f.OrderLineID.String(),
			"order_no":      f.OrderNo,
			"stock_item":    f.StockItem,
			// How much of the ORDER this line consumed. NOT how much stock moved —
			// that is the `qty` on the matching `inventory` entry, and the two are
			// usually equal and are different facts.
			"qty": f.Qty.StringFixed(4),
		})
	}
	// ---- THE THIRD ROUND-TRIP FIELD, AND THE ONE §4.3's AUDIT NEVER LISTED.
	//
	// ⛔⛔ `voucher_gst_line` (migration 0016) IS KEYED ON voucher_version_id AND
	// WAS ABSENT FROM EVERY READ SHAPE — ledger.VoucherView did not carry it and
	// this body did not emit it. It is the same class as the five the audit did
	// list, and it was worse than any of them, because the missing field is an
	// INPUT and not just a record: the rate a voucher's tax was derived at is
	// stored ONLY here. `taxable_value`/`cgst`/`sgst`/`igst` above are what the
	// derivation produced, and you cannot recover a rate from them (an exempt line
	// and a nil-value line both produce zero).
	//
	// So a form rebuilt from this response had no rate to put back in its `gst`
	// block, and the ordinary round trip on an ACCOUNTING-mode GST invoice — open
	// it, fix the narration, save — re-derived the tax at 0%: the party leg fell
	// to the net value, the version's aggregate GST columns went to zero, the
	// invoice left the GSTR-1 b2b section and the HSN summary, and nothing refused
	// it because a zero-rated supply is a legal state.
	//
	// ALWAYS EMITTED, never omitted when empty, for the reason `cost_allocations`
	// and `fulfilments` are: an absent key cannot tell a client "this voucher has
	// no GST" apart from "this endpoint does not tell me about GST", and only one
	// of those is safe to restate as nothing.
	gstLines := make([]map[string]any, 0, len(v.GSTLines))
	for _, g := range v.GSTLines {
		gstLines = append(gstLines, map[string]any{
			// 1-BASED and STORED, like `fulfilments[].line_no` and unlike
			// `cost_allocations[].posting_seq`. It indexes nothing else on this
			// body — an accounting-mode GST voucher has lines and no inventory.
			"line_no": g.LineNo,
			"hsn":     g.HSN,
			// ⛔ MIRRORS gstLineReq FIELD FOR FIELD (taxable_value / gst_rate_pct /
			// hsn) so a client can lift these straight back into the `gst.lines` it
			// sends. The three tax amounts are extra and are ignored by the decoder
			// on the way in — the server re-derives them, and a client that tried to
			// state them would be stating a second opinion about the tax.
			"taxable_value": g.TaxableValue.StringFixed(4),
			"gst_rate_pct":  g.GSTRatePct.StringFixed(4),
			"cgst":          g.CGST.StringFixed(4),
			"sgst":          g.SGST.StringFixed(4),
			"igst":          g.IGST.StringFixed(4),
		})
	}
	// ---- THE STATUTORY DEDUCTION / COLLECTION (register row D88).
	//
	// ⛔ SHAPED LIKE THE REQUEST BLOCK IT WILL BE SENT BACK AS, field for field,
	// because that is the whole job: an alter RE-STATES, and the deduction is
	// deleted and re-derived from the body on every one. A body that omits it
	// removes the deduction while leaving the withheld credit posted — the books
	// then say the full sum was paid and the return says a fifth was withheld.
	//
	// ⚠️ THE DERIVED FIGURES RIDE ALONG AND ARE NOT PART OF THE REQUEST. `rate_pct`
	// and the amount are here so a screen can SHOW what was derived without
	// re-deriving it; the decoder ignores them on the way back in, exactly as it
	// ignores the GST lines' cgst/sgst/igst. A client that resent them would be
	// stating a second opinion about a rate table the server owns.
	//
	// ⚠️ AND NEITHER CARRIES A LIABILITY LEG, because neither row stores one —
	// `tds_ledger_id` is an INPUT to the server's cross-check. A client re-stating
	// a deduction finds the leg among the postings in this same body, and sending
	// none is legal and disclosed. See ledger.VoucherView.TDS.
	var tdsJSON, tcsJSON any
	if d := v.TDS; d != nil {
		tdsJSON = map[string]any{
			"leg_seq":            d.LegSeq,
			"section":            d.Section,
			"deductee_ledger_id": uuidOrEmpty(d.DeducteeLedgerID),
			"deductee_type":      d.DeducteeType,
			"pan_available":      d.PANAvailable,
			"taxable_amount":     d.TaxableBase.StringFixed(4),
			"rate_pct":           d.RatePct.StringFixed(4),
			"tds_amount":         d.TDSAmount.StringFixed(4),
			"deduction_date":     d.DeductionDate,
			"tax_year_start":     d.TaxYearStart,
			"return_code":        d.ReturnCode,
		}
	}
	if c := v.TCS; c != nil {
		tcsJSON = map[string]any{
			"leg_seq":             c.LegSeq,
			"section":             c.Section,
			"buyer_ledger_id":     uuidOrEmpty(c.BuyerLedgerID),
			"pan_available":       c.PANAvailable,
			"taxable_amount":      c.TaxableBase.StringFixed(4),
			"rate_pct":            c.RatePct.StringFixed(4),
			"tcs_amount":          c.TCSAmount.StringFixed(4),
			"collection_date":     c.CollectionDate,
			"tax_year_start":      c.TaxYearStart,
			"declaration_outcome": c.DeclarationOutcome,
		}
	}
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
		"cost_allocations": allocations,
		"fulfilments":      fulfilments,
		"gst_lines":        gstLines,
		// ⛔ NULL, NOT AN EMPTY OBJECT. "This voucher recorded no deduction" and
		// "a deduction of nothing under no section" are different statements, and
		// a client echoing the second back would post a malformed block.
		"tds": tdsJSON,
		"tcs": tcsJSON,
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
	// The collection-side mirror. Gated on Basis for the same reason: a voucher
	// that carried no `tcs` block gets no `tcs` key, so existing clients see a
	// byte-identical body.
	//
	// `declaration` is the field that is NOT a mirror, and it is why a bare
	// amount is not enough: a zero with "NONE" is a sale below Sl. No. 6's
	// limit, a zero with "EXEMPT" rests on a document the department can demand,
	// and "INVALID_NO_PAN" means the buyer was charged MORE than if they had
	// furnished nothing. Three different facts a client must be able to tell
	// apart.
	if res.TCS.Basis != "" {
		out["tcs"] = map[string]any{
			"tcs":         res.TCS.TCS.StringFixed(4),
			"rate_pct":    res.TCS.RatePct.StringFixed(4),
			"threshold":   res.TCS.Threshold.StringFixed(4),
			"basis":       string(res.TCS.Basis),
			"applied":     res.TCS.Applied,
			"declaration": string(res.TCS.Declaration),
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
		// The insightd draft being accepted. Passed through UNVALIDATED and
		// unparsed: it is an opaque insightd identifier, not a uuid (its benchmark
		// corpora use ids like "CO-DEMO/bill-3"), and the only thing cored needs of
		// it is that two accepts of the same draft spell it the same way. Migration
		// 0077's CHECK refuses the empty string; nullIfEmpty in the ledger maps ""
		// to NULL before it can get there.
		SourceDraftID: req.DraftID,
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
	for i, a := range req.CostAllocations {
		// The 1-based position in the request is what a message names, because the
		// operator's mental model is "the third split I keyed" and posting_seq is an
		// index into a different array.
		where := fmt.Sprintf(" on cost allocation %d", i+1)
		catID, err := uuid.Parse(a.CostCategoryID)
		if err != nil || catID == uuid.Nil {
			// ⛔ THE ZERO UUID PARSES. Refused explicitly here as well as in the
			// masters handlers: an allocation naming the all-zeros axis would reach
			// the per-category sum as a REAL grouping key, so a split across two
			// axes where one id failed to serialise would be summed together and
			// refused for an arithmetic reason that names neither cause.
			writeError(w, http.StatusBadRequest,
				"cost_category_id must be the uuid of a cost category (the AXIS the split is drawn on)"+where)
			return in, false
		}
		ctrID, err := uuid.Parse(a.CostCentreID)
		if err != nil || ctrID == uuid.Nil {
			writeError(w, http.StatusBadRequest,
				"cost_centre_id must be the uuid of a cost centre on that axis"+where)
			return in, false
		}
		// decimal.NewFromString, never strconv.ParseFloat: float parsing ACCEPTS
		// "NaN" and "Inf", which blow up inside the decimal package as a panic the
		// Recoverer turns into an opaque 500 — and it would silently round a
		// 19-digit amount, so the split stored would not be the one keyed.
		amount, err := decimal.NewFromString(a.Amount)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"amount must be a decimal number"+where)
			return in, false
		}
		in.CostAllocations = append(in.CostAllocations, ledger.CostAllocationInput{
			PostingSeq:     a.PostingSeq,
			CostCategoryID: catID,
			CostCentreID:   ctrID,
			Amount:         amount,
		})
	}
	for i, f := range req.Fulfilments {
		// The 1-based position in the request is what a message names, because the
		// operator's mental model is "the second order I keyed against" and line_no
		// points into a different array.
		where := fmt.Sprintf(" on fulfilment %d", i+1)
		olID, err := uuid.Parse(f.OrderLineID)
		if err != nil || olID == uuid.Nil {
			// ⛔ THE ZERO UUID PARSES (HANDOFF §7.3). Refused explicitly here as
			// well as in the ledger: as a real key it would reach the per-line sum
			// and the foreign key alike, so two links whose ids failed to serialise
			// would be treated as two claims against one non-existent order line
			// and refused for a reason naming neither cause.
			writeError(w, http.StatusBadRequest,
				"order_line_id must be the uuid of an order LINE (not the order) that this item line fulfils"+where)
			return in, false
		}
		// decimal.NewFromString, never strconv.ParseFloat: float parsing ACCEPTS
		// "NaN" and "Inf", which blow up inside the decimal package as a panic the
		// Recoverer turns into an opaque 500 — and it would silently round a
		// 19-digit quantity, so the link stored would not be the one keyed.
		qty, err := decimal.NewFromString(f.Qty)
		if err != nil {
			writeError(w, http.StatusBadRequest,
				"qty must be a decimal number — the quantity of the ORDER consumed by this line, which is not necessarily the quantity of stock that moved"+where)
			return in, false
		}
		in.Fulfilments = append(in.Fulfilments, ledger.FulfilmentInput{
			LineNo:      f.LineNo,
			OrderLineID: olID,
			Qty:         qty,
		})
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
	if req.TCS != nil {
		tcs, ok := buildTCS(w, *req.TCS)
		if !ok {
			return in, false
		}
		in.TCS = tcs
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

// handleBillsPayable serves the PAYABLES ageing under its own report name.
//
// ⛔ IT SHARES ITS BODY WITH `handleOutstandings` AND FORCES THE KIND. Two handlers
// that formatted the same report differently is exactly the divergence D20 is about,
// and `outstandingKindParam` is already shared between the JSON and printed forms for
// the same reason. What differs here is ONE thing and it is not a format: the PDP sees
// `report:bills-payable` instead of `report:outstandings`, because the path is
// different. That is the entire row.
//
// ⚠️ `?kind=` IS IGNORED HERE, DELIBERATELY, AND NOT REJECTED. This route IS the
// payables book; honouring a `?kind=RECEIVABLE` on it would hand a caller the
// receivables list from under the payables rule — the exact leak the split exists to
// prevent — and refusing the parameter with a 400 would make an operator's stale
// bookmark an error instead of an answer.
func (s *Server) handleBillsPayable(w http.ResponseWriter, r *http.Request) {
	s.writeOutstandings(w, r, reporting.Payables)
}

// handleOpenBills lists ONE party's open bills, for the bill-wise picker (D97).
//
// ⛔⛔ IT EXISTS TO SPLIT AN AUTHORITY, NOT TO ADD A REPORT. "Read the payables
// book" and "list THIS party's open bills so I can settle one" are two different
// jobs, and until this handler they went through one door: the picker called
// `/outstandings?kind=PAYABLE`, so a level refused `report:bills-payable` was
// refused the ageing and could still read every figure in it. The refusal governed
// the DOOR and not the bytes. `routeAction` sees the path and never the query, so
// the split had to become a route.
//
// ⛔ WHAT IT DELIBERATELY DOES NOT EMIT. No slab totals, no grand total, no other
// party. The picker had been receiving the whole book — every party's bills and the
// ageing summary — to render one supplier's list, and that over-disclosure was half
// of what made the leak worth anything. A caller here learns what they must name a
// ledger to ask about, and nothing else.
//
// ⚠️ THE RESIDUAL, STATED RATHER THAN HIDDEN. A level holding `display payment` can
// call this once per supplier ledger it can already see in the chart and rebuild a
// list of open bills by hand. That is inherent to ANY party-scoped door and it is
// the accepted cost of the decision: the authority to pick a bill is not separable
// from the authority to key the voucher that settles it. What it can no longer do is
// read the BOOK — no ageing, no totals, no parties it did not name.
//
// ⚠️ NO `as_on`, AND THAT IS DELIBERATE (D86, and the picker's own comment). The
// question is "which open bill is this payment for", and a bill dated ahead is an
// ordinary answer to it. Bounding at today would make an allocable bill unallocable
// and push the money ON ACCOUNT.
func (s *Server) handleOpenBills(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	// The SAME parser both ageing doors use. A kind spelled one way here and
	// another there would offer an operator the wrong party's list — and an empty
	// picker reads as "this supplier has no open bills", which is a settlement
	// keyed ON ACCOUNT for a bill that was sitting right there.
	kind, ok2 := outstandingKindParam(w, r)
	if !ok2 {
		return
	}
	ledgerID, err := uuid.Parse(chi.URLParam(r, "ledgerID"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid ledger id")
		return
	}
	out, err := s.report.Outstandings(ctx, companyID, kind, "", nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	// ⛔ MATCHED ON THE LEDGER ID, never the name — the same rule the client used
	// to apply and for the same reason: a rename would otherwise show a party with
	// several open bills as having none, silently.
	bills := make([]map[string]any, 0)
	found := false
	var ledgerName, groupName, pending string
	for _, party := range out.Parties {
		if party.LedgerID != ledgerID {
			continue
		}
		found = true
		ledgerName, groupName, pending = party.LedgerName, party.GroupName, party.Pending.StringFixed(4)
		for _, b := range party.Bills {
			// ⛔⛔ THE DERIVED ON-ACCOUNT ROW IS NOT A BILL AND MUST NOT BE OFFERED HERE.
			// This picker's rows become AGAINST allocations, and an AGAINST names the
			// reference it settles. `On Account` is not a stored reference — since D106
			// it is the COMPUTED remainder of a party's balance — so settling "against"
			// it would write an allocation naming a bill that does not exist, and the
			// arithmetic would still foot.
			//
			// ⭐ APPLYING ON-ACCOUNT MONEY TO A BILL IS A REAL OPERATION AND IT HAS A
			// DIFFERENT DOOR: alter the receipt that brought the money in and name the
			// bill on it. That is the only path that can work here, because allocations
			// live inside the voucher hash and a new version is how they change.
			if b.OnAccount {
				continue
			}
			bills = append(bills, map[string]any{
				"ref_name":     b.RefName,
				"pending":      b.Pending.StringFixed(4),
				"due_date":     b.DueDate,
				"overdue_days": b.OverdueDays,
				"slab":         b.SlabLabel,
			})
		}
		break
	}
	// ⚠️ A PARTY WITH NOTHING OPEN IS A 200 WITH AN EMPTY LIST, NOT A 404. "This
	// supplier owes nothing" is an ANSWER to the picker's question, and a 404 would
	// make the form render an error over an ordinary state — which trains an
	// operator to key past it.
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":      string(kind),
		"ledger_id": ledgerID.String(),
		"ledger":    ledgerName,
		"group":     groupName,
		"pending":   pending,
		"bills":     bills,
		"found":     found,
	})
}

func (s *Server) handleOutstandings(w http.ResponseWriter, r *http.Request) {
	// The `kind` parser lives in handler_print_reports.go and is SHARED with the
	// printed ageing. If the two parsed it differently — one defaulting to
	// payables, one accepting a spelling the other refused — an operator would
	// chase a customer for what is actually a supplier balance.
	//
	// ⚠️ THE PARAMETER IS PARSED BEFORE THE SCOPE CHECK ONLY ON THIS ROUTE, because
	// `/bills-payable` has no parameter to parse — see handleBillsPayable.
	if _, _, ok := s.scopeCtxID(w, r); !ok {
		return
	}
	kind, ok2 := outstandingKindParam(w, r)
	if !ok2 {
		return
	}
	// ⛔⛔ THIS IS THE RETIREMENT OF `?kind=PAYABLE` (D97), AND IT IS A REDIRECT
	// RATHER THAN A REFUSAL OR A 403.
	//
	// This route is decided as `report:outstandings`. Answering the PAYABLES book
	// from it handed a level that had been refused `report:bills-payable` the same
	// figures under the other name — the whole row. It could only ever be served
	// here because the bill-wise picker had no other way to ask; now it has one
	// (handleOpenBills above), and nothing needs the parameter.
	//
	// ⚠️ WHY 308 AND NOT 400. `/bills-payable`'s own comment records the call: an
	// operator's stale bookmark should be an ANSWER, not an error. A redirect keeps
	// that promise AND fixes the authority, because the follow-up request is a new
	// request over a different path — so `routeAction` decides it as
	// `report:bills-payable`, and a level refused that name gets a 403 from the
	// middleware, which is where every other refusal in this system comes from. A
	// 400 would break the bookmark; a 403 written here would be the first
	// authorization decision inside a handler in this codebase, and the PDP living
	// in exactly one place is what makes it auditable.
	//
	// ⚠️ THE QUERY IS CARRIED OVER MINUS THE KIND. `as_on` is the caller's question
	// and dropping it would answer a different one; `kind` is deleted because the
	// target route IS the kind and ignores it anyway (see handleBillsPayable).
	if kind == reporting.Payables {
		q := r.URL.Query()
		q.Del("kind")
		target := strings.TrimSuffix(r.URL.EscapedPath(), "/outstandings") + "/bills-payable"
		if enc := q.Encode(); enc != "" {
			target += "?" + enc
		}
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
		return
	}
	s.writeOutstandings(w, r, kind)
}

// writeOutstandings is the ONE body behind both ageing routes.
//
// ⛔ ONE WRITER, TWO DOORS. `/outstandings` parses `?kind=` and passes it; the flat
// `/bills-payable` route passes `Payables` and ignores the parameter. Everything the
// operator reads — the party rows, the bill arrays, the slab totals, the honest
// `as_on`/`aged_as_on` pair from D86 — is emitted here exactly once, so the two doors
// cannot drift into two different reports the way D20's two surfaces did.
func (s *Server) writeOutstandings(w http.ResponseWriter, r *http.Request, kind reporting.OutstandingKind) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
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
				// ⛔ EMITTED RATHER THAN LEFT TO THE CLIENT TO INFER FROM THE NAME. A
				// client testing `ref_name === "On Account"` would re-derive a server
				// fact from a display string, and a customer is perfectly entitled to
				// name a real bill "On Account".
				"on_account": b.OnAccount,
			})
		}
		parties = append(parties, map[string]any{
			// ⛔ THE ID IS EMITTED BECAUSE A SETTLEMENT IS KEYED AGAINST IT. The
			// bill-wise picker on a Receipt has to list THIS party's open bills, and
			// matching the party by NAME would work only by accident of
			// ledger_company_id_name_key being unique — it would break the moment a
			// ledger is renamed mid-period, and it would break silently, by showing
			// an operator no open bills for a customer who has several.
			"ledger_id": p.LedgerID.String(),
			"ledger":    p.LedgerName,
			"group":     p.GroupName,
			"pending":   p.Pending.StringFixed(4),
			"bills":     bills,
		})
	}
	slabs := make([]map[string]string, 0, len(out.SlabTotals))
	for _, st := range out.SlabTotals {
		slabs = append(slabs, map[string]string{
			"label":  st.Label,
			"amount": st.Amount.StringFixed(4),
		})
	}
	// ⛔⛔ `as_on` IS EMPTY WHEN THE CALLER NAMED NO DATE, AND THAT IS THE WHOLE
	// SECOND HALF OF D86. It used to echo the RESOLVED date — today — on a report
	// whose rows were no longer limited to today, so every caption downstream read
	// "as at <today>" over a table that included next month's invoices. A caption
	// that is present, well-formed and false is worse than none.
	//
	// ✅ THE CONVENTION IS ALREADY IN THIS CODEBASE AND IS COPIED RATHER THAN
	// INVENTED: `/stock-summary` echoes `as_on: ""` for an unbounded read, and
	// `stockSummaryScopeLine` in the client turns that empty string into "NO DATE
	// LIMIT — …". ⚠️ EMPTY IS AN ANSWER, not a missing field, and the client's own
	// comment explains why `??` and not `||` is the right operator on it.
	//
	// ⚠️ `aged_as_on` IS ALWAYS POPULATED, because the ageing reference is a real
	// and different fact: an unbounded report still buckets every bill by days
	// overdue against SOME day, and dropping that would trade a false caption for a
	// missing one.
	asOnEcho := ""
	if out.Bounded {
		asOnEcho = out.AsOn
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"kind":        string(out.Kind),
		"as_on":       asOnEcho,
		"aged_as_on":  out.AsOn,
		"parties":     parties,
		"slab_totals": slabs,
		"total":       out.Total.StringFixed(4),
		// ⛔ THE SLABS DO NOT SUM TO THE TOTAL, AND THIS FIELD IS THE DIFFERENCE.
		// An on-account amount has no due date, so it sits in no ageing bucket (D106;
		// Tally is the same — "On Account amounts are not reflected in ... Age
		// Analysis Statement"). Emitting the remainder lets the client state
		// `sum(slab_totals) + on_account_total == total` instead of leaving a reader
		// to discover the gap by adding the column up.
		"on_account_total": out.OnAccountTotal.StringFixed(4),
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
	// ⛔ WARNINGS ARE PART OF THE RESULT, NOT A LOG LINE. The importer carries on
	// past what it cannot represent exactly — an opening bill-wise breakup that
	// does not add up, a ledger whose Tally group is not in the file — and an
	// operator who is told only "121 vouchers posted" will believe the migration
	// was lossless when parts of it were not.
	warnings := res.Warnings
	if warnings == nil {
		warnings = []string{}
	}
	groups := res.GroupsEnsured
	if groups == nil {
		groups = []string{}
	}
	// D101: money the SOURCE books exclude. Never nil on the wire — a client that
	// has to distinguish "no voided vouchers" from "this server does not report
	// them" will get it wrong, and the wrong answer is the reassuring one.
	voided := res.VoidedVouchers
	if voided == nil {
		voided = []string{}
	}
	tombstoned := res.LedgersTombstoned
	if tombstoned == nil {
		tombstoned = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source_company":   res.SourceCompany,
		"vouchers_posted":  res.VouchersPosted,
		"vouchers_altered": res.VouchersAltered,
		"vouchers_deleted": res.VouchersDeleted,
		"vouchers_skipped": res.Skipped,
		// ---- MONEY THE SOURCE BOOKS EXCLUDE (D101) -------------------------
		//
		// ⛔ DELIBERATELY NOT FOLDED INTO vouchers_skipped. That figure means "a
		// voucher with this GUID was already imported"; these were never imported
		// and never will be, because Tally keeps a CANCELLED or OPTIONAL voucher
		// out of the books. An operator told their voided sales were "skipped"
		// would read it as a duplicate-detection message and move on.
		//
		// ⚠️ "3 posted" against a file of 5 is the same class of silence the
		// stock counts below exist to prevent, which is why this is on the wire
		// rather than in a log line.
		"vouchers_voided":    res.VouchersVoided,
		"voided_vouchers":    voided,
		"ledgers_tombstoned": tombstoned,
		"groups_ensured":     groups,
		"ledgers_ensured":    res.LedgersEnsured,
		"warnings":           warnings,
		"posted":             posted,
		// ---- THE GOODS. ⛔ THESE SIX ARE THE ONLY THING ON THE WIRE THAT CAN
		// TELL AN OPERATOR WHETHER THEIR STOCK ARRIVED. Quantity is invisible in
		// a trial balance, so an import that read every stock line and threw it
		// away answered 200 with a report that looked complete — which is exactly
		// what this route did until the importer was taught quantities. A caller
		// that sees `stock_lines: 0` on a file full of invoices knows something
		// is wrong; one that sees `vouchers_posted: 121` does not.
		//
		// ⚠️ `opening_positions: 0` ON A FILE WITH STOCK LINES IS A WARNING, not a
		// clean result: it means the export carried no opening stock, so every
		// position starts at nil and the first issue of older goods will either
		// be refused or consume nothing at all.
		"stock_lines":         res.StockLines,
		"opening_positions":   res.OpeningPositions,
		"stock_items_ensured": res.StockItemsEnsured,
		"units_ensured":       res.UnitsEnsured,
		"godowns_ensured":     res.GodownsEnsured,
		"batches_ensured":     res.BatchesEnsured,
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
			// ⛔ THE SCOPE REFUSAL MUST BE WORDED, NOT SWALLOWED (D50). A question
			// naming another company is refused by the translator with a typed
			// error carrying the REASON; routing it through the generic arm below
			// told the operator "could not understand the question", which is the
			// same non-statement D50 records — the caller still never learns that
			// the company they named was not read. It WRAPS ErrUntranslatable, so
			// this branch must precede the fallthrough or it is dead code.
			if errors.Is(err, query.ErrCrossTenantScope) {
				writeQueryCrossTenant(w)
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

// writeQueryCrossTenant writes the 422 for a question that tried to select a
// scope other than the signed-in company (another company by name or UUID,
// "every company", "all tenants").
//
// It is a 422 and not a 403: nothing was denied and nothing leaked — the
// compiled SQL carries no company predicate at all and tenant isolation rests
// on FORCE RLS (query_security_test.go's TestSecurity_CrossTenantIsolation
// proves a forged widening returns zero foreign rows). What the caller needs is
// the REASON, which is the half D50 records as missing: before this the same
// question got "could not understand the question", so an operator who asked
// for every company's sales and was shown one company's sales had nothing to
// tell the two apart.
//
// The examples are echoed exactly as the generic arm echoes them, so the screen
// renders one shape of 422 and not two.
func writeQueryCrossTenant(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error": "this question asks for another company's books, which this screen cannot read; " +
			"every answer is scoped to the signed-in company, so nothing was answered rather than " +
			"answering it for this company",
		"examples": query.ExampleQuestions,
	})
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
//
// ⛔⛔ THE CAP IS PER ROUTE, AND A SINGLE CAP HERE SILENTLY TRUNCATED THE ONE
// ROUTE THAT IS ALLOWED TO BE BIG.
//
// This read is not a read-only peek: it REPLACES r.Body with exactly the bytes it
// managed to read. So whatever it declines to read, the HANDLER never sees either.
// It capped everything at maxJSONBody (1 MiB) while handleImportTallyXML sets its
// own maxXMLBody (32 MiB), which meant a Tally XML import over 1 MiB reached the
// importer TRUNCATED — a parse failure (422) with a cutoff unset, or a fail-closed
// 403 with one set. Either way a real migration import, which is exactly the case
// the 32 MiB allowance exists for, could not succeed.
//
// ⚠️ Found while auditing the back-dated cutoff on 2026-08-07 and initially
// misattributed to that work. It is OLDER than the cutoff fixes: `drainBody(r)`
// has always run for every write, so the truncation predates the peek learning to
// read XML at all — the cutoff work only made a truncated body ALSO fail closed,
// which is what surfaced it.
//
// ⛔ AND THE CAP IS NOT SIMPLY RAISED TO 32 MiB FOR EVERYTHING. This buffers the
// whole body in memory on the AUTHORIZATION path, before any handler has agreed to
// process it, so a blanket 32 MiB would hand every unauthenticated-but-routed write
// a 32 MiB memory sink. Each route gets the cap its own handler already declares —
// one cap per route, named in one place.
func drainBody(r *http.Request) []byte {
	return drainBodyCapped(r, bodyCapFor(r))
}

// bodyCapFor returns the byte cap drainBody must use for r, which MUST match the
// cap the route's own handler declares — if this is smaller, the handler is fed a
// truncated body; if it is larger, the peek buffers more than the handler would
// ever accept.
//
// ⚠️ Keyed on the same path segments routeAction uses (see routeSuffix/splitPath),
// because "which route is this" must have ONE definition in this file — the pay-run
// peek carries the same warning and for the same reason.
// ⚠️ MATCHED ON THE TRAILING SEGMENTS, not on an exact two-segment suffix.
// routeSuffix strips `/v1/companies/{companyID}` by asking chi for the URL param,
// so OUTSIDE a mounted chi route (a bare httptest request, a future middleware
// ordering change, anything that has not populated the route context yet) the
// company id is still present in the path and an exact `len(segs) == 2` match
// silently misses — which would restore the truncation this function exists to fix,
// and restore it in the direction that FAILS QUIETLY. Anchoring at the tail matches
// in both cases, and no other route in the product ends in `import/tally-xml`.
func bodyCapFor(r *http.Request) int64 {
	segs := splitPath(routeSuffix(r))
	if n := len(segs); n >= 2 && segs[n-2] == "import" && segs[n-1] == "tally-xml" {
		return maxXMLBody
	}
	// THE DRAFT UPLOAD (D49). Matched on the trailing segment for the same reason
	// the XML import is, and narrowed to the POST: it is the CREATE that carries a
	// base64 scan. The other five draft routes carry a claim token or a voucher
	// id and nothing else, and `GET .../drafts` never reaches here at all
	// (drainBody runs only for writes). ⛔ 16 MiB here MUST equal the cap
	// handleDraftProxy declares — if this is smaller the proxy forwards a
	// truncated body, which is the exact failure this function was written to fix,
	// in the direction that fails quietly.
	if n := len(segs); n >= 1 && segs[n-1] == "drafts" && r.Method == http.MethodPost {
		return maxDraftUploadBody
	}
	return maxJSONBody
}

func drainBodyCapped(r *http.Request, max int64) []byte {
	if r.Body == nil {
		return nil
	}
	limited := io.LimitReader(r.Body, max)
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
// the back-dated cutoff check. Returns nil when the field is ABSENT (the cutoff
// then does not apply) and beforeEveryCutoff() when it is PRESENT AND UNREADABLE
// (the cutoff then refuses). It tolerates non-voucher bodies (returns nil).
//
// ===========================================================================
// THE CUTOFF WAS BYPASSABLE BY LOOSENING THE SPELLING OF THE DATE
// ===========================================================================
// This returned nil for BOTH absent and unparseable, and nil means "the cutoff
// does not apply" — authz.cutoffDeny opens with `req.VoucherDate == nil ->
// not blocked`. So the one control an owner has over a closed and already-filed
// period could be switched off from the wire, by spelling the date differently.
//
// IT IS NOT A THEORETICAL PARSE MISMATCH; THE TWO LAYERS GENUINELY DISAGREE.
// time.Parse here demands exactly "2006-01-02". The same string then travels to
// Postgres UNPARSED — ledger.Post writes `vDate any = nullIfEmpty(in.VoucherDate)`
// straight into a DATE column — and Postgres cheerfully accepts "2026-5-31",
// "2026-05-31T00:00:00Z" and "20260531". A date the PDP could not read was
// therefore a date the LEDGER could read, and the voucher landed inside the lock,
// correctly dated, with no trace that a control had been skipped.
//
// ===========================================================================
// SO AN UNREADABLE DATE FAILS CLOSED, LIKE THE PAY RUN'S
// ===========================================================================
// The pattern already exists in this file, and peekDepositDate's comment states
// the reason twenty lines below: "A pay run with no readable wage month returns
// beforeEveryCutoff() so the cutoff cannot be bypassed by omitting a field." A
// field the PDP cannot READ is the same hazard as a field it cannot FIND, so it
// gets the same answer. beforeEveryCutoff() is earlier than any cutoff an
// administrator can set, so cutoffDeny denies; where no cutoff is set it changes
// nothing and the request reaches the handler for its ordinary error.
//
// ===========================================================================
// BUT AN ABSENT DATE STILL RETURNS nil, AND THAT IS NOT THE SAME HOLE
// ===========================================================================
// Two reasons, and the first is the load-bearing one:
//
//  1. THIS PEEK RUNS ON EVERY WRITE IN THE PRODUCT, not only voucher writes.
//     voucherDateFromRequest calls it for every write that is not a pay run and
//     not a TDS challan — creating a ledger, a godown, a unit, a cost centre,
//     uploading a bill — which is why its doc has always said it "tolerates
//     non-voucher bodies". Failing closed on a body that simply has no
//     voucher_date would refuse EVERY master create at any company that has set
//     a cutoff. That is not a tighter control, it is an outage.
//  2. An absent date cannot BE back-dated: it asserts nothing about the closed
//     period the cutoff protects. ledger.Post stores it as
//     nullIfEmpty(in.VoucherDate) — SQL NULL, dated into no period at all — and
//     on the auto-numbering path it is defaulted to today server-side ("Default
//     an empty date to today server-side so a number can still be allocated").
//     Neither outcome puts anything into the locked period.
//
// The rule, then: a voucher_date we cannot READ refuses; a voucher_date that is
// not THERE is somebody else's 400.
//
// ===========================================================================
// ROUND 2: AND THEN A BROWSER RE-DRIVE PUT A VOUCHER INTO THE LOCKED PERIOD
// ANYWAY, THROUGH A DIFFERENT DOOR — A NESTED BODY
// ===========================================================================
// Everything above HOLDS: five loose spellings 403 with nothing written and the
// refusal names the cutoff. It was not enough, because the peek looked for ONE
// key at ONE depth. Same company, same level, same cutoff, same strictly-spelled
// date inside the locked period, on the wire, after the fix above shipped:
//
//	SYNC     POST /sync/commit  intents[0].payload.voucher_date=2026-05-01 -> 200
//	         {"outcomes":[{"detail":"created","final_voucher_no":"6","status":"COMMITTED"}]}
//	CONTROL  POST /vouchers/journal        voucher_date=2026-05-01         -> 403
//	         {"error":"voucher date is before the back-dated cutoff of 2026-07-01"}
//
// /sync/commit IS routed as a write, so every OTHER control applied to it. But
// its body is `{"intents":[{"payload":{...a full voucher request...}}]}` —
// syncIntentReq.Payload literally IS a voucherReq — so the top-level probe found
// no voucher_date, returned nil, and nil means the cutoff does not apply. An
// offline node could post into a filed quarter at will, and the committed
// voucher is in the books with matched Dr/Cr and an allocated final number.
//
// SO THE PEEK NO LONGER KNOWS WHICH ROUTES NEST THEIR PAYLOADS. It walks the
// decoded body and judges EVERY `voucher_date` it carries at ANY depth
// (walkVoucherDates). That closes /sync/commit and every future forwarder of a
// nested voucher body — a bulk endpoint, a second sync op, a queue drain —
// without a route list here that has to be kept in step with the router. Two
// route-shaped peeks remain (the pay run's wage_month, the challan's
// deposit_date) because those bodies spell the date with a DIFFERENT KEY; this
// one is about the same key at another depth.
//
// A BATCH IS ONLY AS PERMITTED AS ITS MOST-REFUSED MEMBER. The walk keeps the
// EARLIEST date it finds and refuses outright if ANY of them is unreadable, so
// one allowed intent cannot carry a back-dated one in with it. sync.Commit
// commits its intents together and answers 200 with a per-intent outcome list;
// there is no partial authorization to express, and "the allowed ones went in"
// would be indistinguishable to the operator from "all of them did".
//
// AND THE OTHER NON-TOP-LEVEL DATE IS NOT EVEN JSON: POST /import/tally-xml
// posts every VOUCHER in a Tally envelope through ledger.Post, dated by its
// <DATE> element. The round-1 comment above said a body that is not JSON has
// "no voucher_date here to loosen, and a voucher write cannot hide in such a
// body either" — which was true of a multipart upload and FALSE of this route,
// the one endpoint on the API that ingests thousands of dated vouchers at once.
// See peekTallyXMLVoucherDate.
//
// ===========================================================================
// ROUND 4: AND THEN A BROWSER RE-DRIVE CHANGED THE CASE OF THE JSON KEY
// ===========================================================================
// Everything above HOLDS — and it was STILL not enough, because rounds 1 and 2
// left this peek as a SECOND, HAND-WRITTEN IMPLEMENTATION OF THE DECODER. It
// walked the decoded body as a map[string]any and compared `k == "voucher_date"`
// EXACTLY. Go's encoding/json does not: it binds an object key to a struct field
// CASE-INSENSITIVELY. So the handler bound the date and dated the voucher while
// the peek saw no date at all — and a nil date means the cutoff does not apply.
// Verbatim, on the wire, against a company whose Owner level had
// BackdatedCutoff = 2026-07-01 and after both fixes above had shipped:
//
//	POST /vouchers/journal  {"VOUCHER_DATE":"2026-05-01",...} -> 201 no=6
//	POST /vouchers/journal  {"Voucher_Date":"2026-05-01",...} -> 201 no=7
//	POST /vouchers/journal  {"voucher_DATE":"2026-05-01",...} -> 201 no=8
//	POST /sync/commit  intents[0].payload.Voucher_Date=2026-05-04 -> 200 COMMITTED
//	CONTROL POST /vouchers/journal {"voucher_date":"2026-05-01"} -> 403
//
// Five back-dated vouchers landed inside the locked period and are in the
// operator's own May 2026 Day Book. One was driven from a real browser session
// with a real passkey step-up, so it is not a curl-only artefact.
//
// ⛔ THE LESSON IS NOT "CASE". ANY DIVERGENCE BETWEEN THE PEEK'S NOTION OF WHAT
// THE BODY SAYS AND THE HANDLER'S IS A BYPASS. Case was merely the cheapest one
// to type. The enumeration below is the point of this round, because the three
// previous rounds each patched the input they were shown:
//
//	(1) CASE                `{"VOUCHER_DATE":...}` — encoding/json binds folded.
//	(2) DUPLICATE KEYS      `{"voucher_date":"2026-09-01","VOUCHER_DATE":
//	                        "2026-05-01"}` — encoding/json assigns each matching
//	                        key in turn, so the LAST one wins and the handler
//	                        stores May. A map keyed on the exact string keeps BOTH
//	                        and a fold-compare would have picked whichever the
//	                        random map order handed it first.
//	(3) ESCAPED KEY FORMS   `{"\u0056OUCHER_DATE":...}` — the key is UNQUOTED
//	                        before it is matched, so it binds as VOUCHER_DATE and
//	                        then folds onto the field. An exact byte compare here
//	                        never sees the key the handler saw.
//	(4) ESCAPED VALUE FORMS `"2026\u002d05\u002d01"` — the value is unquoted
//	                        before it is parsed, so this IS 2026-05-01 to both layers.
//	(5) TRAILING BYTES      `{"voucher_date":"2026-05-01"} <junk>` — ⛔ THE DOOR
//	                        NOBODY DROVE YET. decodeJSON uses json.NewDecoder().
//	                        Decode(), which reads the FIRST JSON value and ignores
//	                        whatever follows; json.Unmarshal REJECTS trailing
//	                        bytes. So a single space and a brace after the object
//	                        made the old peek answer "not JSON at all" -> nil ->
//	                        cutoff does not apply, while the handler decoded and
//	                        posted the object perfectly. The same divergence made
//	                        an oversized body a bypass: drainBody TRUNCATES at
//	                        maxJSONBody with io.LimitReader, so a body padded past
//	                        1 MiB after a complete first object arrived here as
//	                        invalid JSON and left with the cutoff switched off.
//	(6) WRONG-TYPED VALUES  a number/object/array where the string goes: kept as
//	                        round 1 left it (present and unreadable -> refuse),
//	                        because authorization runs BEFORE the handler's 400.
//
// SO THE PEEK NOW BINDS THE KEY THE WAY THE HANDLER BINDS IT — WITH encoding/json
// ITSELF. Every object in the body is decoded through voucherDateProbe, whose tag
// is voucherReq.VoucherDate's tag, by the same Decode call decodeJSON makes. Case,
// duplicate keys and both escape forms are then not handled HERE at all: they are
// handled by the one library both layers share, which is why they cannot drift
// apart again. voucherDateBindingCannotDriftTest pins the tag equality.
//
// ⛔ WHY NOT ONE TYPED DECODE OF THE WHOLE BODY (the obvious version of the same
// idea): because this peek runs BEFORE THE ROUTE IS KNOWN. voucherDateFromRequest
// calls it for every write that is not a pay run and not a challan — ledgers,
// godowns, units, cost centres, price lists, bill uploads, /sync/commit, a future
// bulk endpoint — so there is no single request type to decode into, and round 2
// deliberately refused to keep a route list here in step with the router. Judging
// EVERY object with the probe is the shape that needs no route list AND still
// borrows the handler's binding rule.
//
// ⛔ WHY NOT strings.EqualFold ON THE KEY (the cheap patch): it closes case and
// NOTHING ELSE on the list above — it still keeps both duplicate keys, still picks
// between them by random map order, and it is still a second implementation of a
// rule that lives in encoding/json. The next re-drive finds items 2, 3 and 5.
func peekVoucherDate(body []byte) *time.Time {
	// ⚠️ DECODED THE WAY decodeJSON DECODES: json.NewDecoder().Decode() takes the
	// FIRST JSON value and ignores any trailing bytes. json.Unmarshal — what this
	// used until round 4 — rejects them, and that difference alone was a bypass
	// (item 5 above). One notion of "what this body is", shared with the handler.
	//
	// Decoded into json.RawMessage, not into `any` and not into a probe struct:
	// the date can be at ANY depth (round 2) so a typed shape cannot reach it,
	// while `any` — round 2's choice — resolves keys into a map and thereby throws
	// away the two things binding needs, the DOCUMENT ORDER of duplicate keys and
	// the ability to hand encoding/json the object bytes again.
	var root json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&root); err != nil {
		// Not JSON at all: a multipart preamble, a CSV, junk — or the ONE non-JSON
		// write body in the product, the Tally XML import, which dates every
		// voucher it carries and posts them through the ledger.
		return peekTallyXMLVoucherDate(body)
	}
	earliest, unreadable := walkVoucherDates(root, 0)
	if unreadable {
		// Any unreadable date anywhere in the body refuses the WHOLE body, even if
		// some other intent in it carried a perfectly good post-cutoff date.
		return beforeEveryCutoff()
	}
	return earliest
}

// voucherDatePeekMaxDepth bounds the walk below. Nothing this API accepts nests
// anywhere near this deep (a sync intent's voucher date sits at depth 3), so the
// bound only ever fires on a body no handler could decode either.
const voucherDatePeekMaxDepth = 64

// walkVoucherDates judges every `voucher_date` in a decoded write body, at any
// depth, and reduces them to the pair cutoffDeny needs: the EARLIEST date found
// (nil when the body carries none) and whether ANY of them was present and
// unreadable.
//
// ⚠️ EARLIEST, NOT FIRST, AND NOT LAST. The earliest date in a batch is the one
// the cutoff must judge: it is the most-refused member, and taking any other
// would let a batch smuggle a back-dated intent in behind an allowed one. Map
// iteration order in Go is random, so "first" would not even be stable.
//
// ⚠️ THE UNREADABLE FLAG IS SEPARATE FROM THE DATE ON PURPOSE. An unreadable
// date is not "very early" — it is "we do not know", and cutoffDeny says so in
// its own sentence (the zero-time sentinel branch). Folding it into the date
// would work today only because the sentinel happens to be earlier than
// everything, and would silently stop working for a level whose cutoff IS the
// zero date — the case cutoffDeny's IsZero branch exists to survive.
//
// ⚠️ ROUND 4: IT TAKES json.RawMessage, NOT `any`, AND THAT IS THE WHOLE FIX.
// A body decoded into `any` has already had its keys resolved into a map, and a
// map has thrown away everything the BINDING depends on: the document order of
// two keys that fold onto the same field, and the object's own bytes. Keeping the
// raw bytes lets each object be handed back to encoding/json — the same library,
// with the same tag, doing the same job it does for voucherReq in the handler —
// so "which key is the voucher date" is answered in ONE place for both layers.
// The door this closes was driven in a browser after THREE earlier fixes to this
// same control, by typing the key as VOUCHER_DATE; the enumeration in the header
// above (case, duplicate keys, escaped keys, escaped values, trailing bytes) is
// the point, and not pedantry.
func walkVoucherDates(node json.RawMessage, depth int) (earliest *time.Time, unreadable bool) {
	if depth > voucherDatePeekMaxDepth {
		// Refuse rather than stop looking. "I gave up reading this body" must never
		// be spelled the same way as "this body carries no date" — that conflation
		// IS the defect this whole function exists to un-make.
		return nil, true
	}

	// WHICH KIND OF NODE THIS IS, DECIDED BY encoding/json AND NOT BY LOOKING AT
	// THE FIRST BYTE. Peeking for '{' or '[' would be one more hand-written scrap
	// of decoder, which is exactly the thing that has now failed four times.
	// A JSON `null` unmarshals into a nil map with NO error, so it lands in the
	// object arm and iterates nothing — which is the right answer for it.
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(node, &obj); err == nil {
		if obj == nil {
			return nil, false // JSON null: a node, but not an object and not a date.
		}
		// THE BINDING. voucherDateProbe carries voucherReq.VoucherDate's tag, so
		// this Unmarshal answers "did the handler see a voucher_date in THIS object,
		// and which value did it bind" — case-folded, last-duplicate-wins, key and
		// value escapes already unquoted, all by encoding/json rather than by
		// anything written here.
		var probe voucherDateProbe
		if err := json.Unmarshal(node, &probe); err != nil {
			// Unreachable for a node that already parsed as an object (the sole field
			// is a json.RawMessage, which accepts any valid JSON value). Fail CLOSED
			// anyway: an object whose date we did not manage to look at is an object
			// whose period we cannot rule out.
			return nil, true
		}
		if probe.VoucherDate != nil {
			// A pointer field, so ABSENT and JSON-null both arrive as nil — the same
			// answer the handler reaches for them (the field stays "", ledger.Post
			// writes nullIfEmpty, and the row is dated into no period at all).
			d, bad := readVoucherDateValue(*probe.VoucherDate)
			earliest = earlierOf(earliest, d)
			unreadable = unreadable || bad
		}
		// EVERY value is then descended into, INCLUDING the one the probe just
		// bound. Round 2 skipped it, reasoning that an object under a voucher_date
		// key is a wrong-typed date rather than a container of dates. That is still
		// true and the skip is no longer worth the second key rule it would cost:
		// a wrong-typed date has ALREADY set `unreadable`, and unreadable refuses
		// the whole body in peekVoucherDate regardless of what any further date
		// says, so descending can only ever add refusal and never remove it. A
		// scalar under that key yields nothing to find.
		for _, v := range obj {
			d, bad := walkVoucherDates(v, depth+1)
			earliest = earlierOf(earliest, d)
			unreadable = unreadable || bad
		}
		return earliest, unreadable
	}

	var arr []json.RawMessage
	if err := json.Unmarshal(node, &arr); err == nil {
		// The `intents` array of a sync commit, and any bulk shape that follows it.
		for _, v := range arr {
			d, bad := walkVoucherDates(v, depth+1)
			earliest = earlierOf(earliest, d)
			unreadable = unreadable || bad
		}
		return earliest, unreadable
	}

	// Any scalar (a string, a number, a bool) that is not bound to a voucher_date
	// field carries no voucher date: no date, not unreadable.
	return nil, false
}

// voucherDateProbe is how the peek asks encoding/json the ONE question the
// authorization decision turns on: "what did the handler bind as this object's
// voucher_date?"
//
// ⛔ ITS TAG IS voucherReq.VoucherDate'S TAG AND MUST STAY THAT WAY. voucherReq is
// the single wire shape that carries a date into ledger.Post over HTTP — the F-key
// POST, the ALTER PUT, and /sync/commit all decode it (syncIntentReq.Payload IS a
// voucherReq) — so binding through the same tag with the same library is what makes
// the PDP and the writer unable to disagree about what the body SAYS. The two are
// pinned to each other by reflection in TestVoucherDateBindingCannotDriftFromTheHandler.
//
// ⚠️ *json.RawMessage, and both halves matter:
//
//	the POINTER   distinguishes ABSENT (and JSON null, which the handler also
//	              treats as no date) from PRESENT — round 1's rule that an absent
//	              date must return nil, or every master create at a company with a
//	              cutoff is refused, which is an outage and not a control.
//	the RAW BYTES keep the wire's own type, so PRESENT-BUT-NOT-A-STRING stays
//	              distinguishable from PRESENT-AND-READABLE. A `string` field here
//	              would collapse `{"voucher_date":20260501}` into "" — "no date" —
//	              and hand the cutoff its nil.
type voucherDateProbe struct {
	VoucherDate *json.RawMessage `json:"voucher_date"`
}

// readVoucherDateValue judges ONE `voucher_date` value as the handler bound it.
// The three answers are round 1's three answers, unchanged:
//
//	(&d,  false) a readable date
//	(nil, false) a value that asserts NO date: explicit null, or an empty string
//	(nil, true)  present and unreadable: a loose spelling, or not a string at all
//
// ⚠️ ROUND 4: THE VALUE IS UNQUOTED BY encoding/json, NOT BY A TYPE ASSERTION.
// json.Unmarshal into a string applies the same unescaping the handler's decode
// of voucherReq applies, so "2026-05-01" is 2026-05-01 to BOTH layers.
// A hand-rolled `raw[0] == '"'` plus a slice of the middle would read that as
// junk, refuse a perfectly ordinary date, and be a second decoder again.
func readVoucherDateValue(raw json.RawMessage) (*time.Time, bool) {
	var spelled string
	if err := json.Unmarshal(raw, &spelled); err != nil {
		// A number, an object, an array, a bool. The handler will reject it too —
		// but authorization runs BEFORE the handler, so the cutoff must not be the
		// thing that waves it through on the way there. (Nothing here ever reads the
		// value; it only decides that this is not a date.)
		return nil, true
	}
	if spelled == "" {
		// An empty string is how a client sends "no date", and a JSON null decodes
		// into a string as a no-op leaving "" — the handler's own answer for both.
		return nil, false
	}
	d, err := time.Parse("2006-01-02", spelled)
	if err != nil {
		// The round-1 hole: strict here, loose in Postgres. See the header above.
		return nil, true
	}
	return &d, false
}

// earlierOf returns the earlier of two peeked dates, treating nil as "no date
// seen here". It is the reduction that makes a batch only as permitted as its
// most-refused member.
func earlierOf(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case b.Before(*a):
		return b
	default:
		return a
	}
}

// peekTallyXMLVoucherDate derives the back-dated cutoff's date for the ONE write
// on this API whose body is not JSON: POST /import/tally-xml.
//
// ===========================================================================
// WHY THIS EXISTS — THE SECOND DOOR A BROWSER RE-DRIVE FOUND
// ===========================================================================
// The Tally import is not a metadata upload that some later step turns into
// vouchers. handleImportTallyXML hands the body straight to migration.Importer,
// which posts EVERY <VOUCHER> in the envelope through ledger.Post, dated by its
// own <DATE> element — the comment on the route says so: "posts each voucher
// through the foundation ledger path (the SOLE writer)". peekVoucherDate saw a
// body that was not JSON, returned nil, and nil means the cutoff does not apply.
// So the endpoint that writes the MOST vouchers per request was the one the
// period lock could not see at all: an operator who cannot post one back-dated
// journal could import ten thousand of them.
//
// ===========================================================================
// IT IS THE SAME LOOSE-SPELLING HAZARD, TOO
// ===========================================================================
// integration.TallyDateToISO returns an already-hyphenated value UNCHANGED, so
// <DATE>2026-5-1</DATE> travels through the importer to ledger.Post and into a
// Postgres DATE column, which reads it as 1 May 2026. The PDP must therefore
// hold the date to the SAME strict layout it holds a JSON voucher_date to, and
// refuse what it cannot read.
//
// ===========================================================================
// THE CONVERSION IS THE IMPORTER'S OWN, AND THE ROOT-ELEMENT CHECK IS WHY A
// LEDGER-ONLY IMPORT STILL WORKS
// ===========================================================================
// integration.TallyDateToISO and integration.ParseTallyEnvelope are the same two
// calls migration.buildVoucherInput makes, per the pay-run precedent: the PDP and
// the writer must not be able to disagree about WHICH date was asked for, only
// about whether it is allowed.
//
// A body whose root element is not <ENVELOPE> returns nil — it is a multipart
// preamble, a CSV, junk, and no voucher can hide in it. An envelope carrying only
// <LEDGER> masters also returns nil: masters are not dated, so importing a chart
// of accounts into a company with a cutoff must keep working (this is round 1's
// "an absent date on a non-voucher body must stay nil", in XML).
//
// A body that IS an envelope and does not parse REFUSES, because the importer
// would have dated something out of it and we cannot see what.
func peekTallyXMLVoucherDate(body []byte) *time.Time {
	if !isTallyEnvelope(body) {
		return nil
	}
	env, err := integration.ParseTallyEnvelope(bytes.NewReader(body))
	if err != nil {
		return beforeEveryCutoff()
	}
	var earliest *time.Time
	for _, m := range env.Messages() {
		if m.Voucher == nil {
			continue // a LEDGER master message: nothing dated, nothing to back-date.
		}
		iso, err := integration.TallyDateToISO(m.Voucher.Date)
		if err != nil {
			return beforeEveryCutoff()
		}
		d, err := time.Parse("2006-01-02", iso)
		if err != nil {
			return beforeEveryCutoff()
		}
		// Earliest voucher in the envelope, for the batch reason in walkVoucherDates:
		// one post-cutoff voucher must not carry a back-dated one in with it.
		earliest = earlierOf(earliest, &d)
	}
	return earliest
}

// isTallyEnvelope reports whether the body's ROOT ELEMENT is a Tally <ENVELOPE>.
//
// It tokenises rather than searching the bytes for "<ENVELOPE" on purpose. A
// substring search over a prefix would be bypassable by padding the front of the
// document with comments or whitespace, and a search over the whole body would
// match the word inside a CSV cell. The tokeniser skips the XML declaration,
// comments and whitespace and hands back the first real element — which is the
// same thing encoding/xml will hand the importer.
func isTallyEnvelope(body []byte) bool {
	// ⛔⛔ THROUGH NewTallyDecodeReader, AND THAT WORD IS THE WHOLE FIX (2026-08-25).
	//
	// This decoded the RAW BYTES. Every real TallyPrime export is UTF-16LE — that
	// is defect #1 of the seven T32 found — so `xml.Decoder` hit "invalid UTF-8"
	// on the first byte pair, this returned FALSE, and false means "not an
	// envelope, no voucher can hide in it". Measured on the corpus fixture that
	// carries the real shapes, on IDENTICAL BYTES:
	//
	//	integration.ParseTallyEnvelope  ->  12 messages, 1 voucher, DATED
	//	peekTallyXMLVoucherDate         ->  nil   (the cutoff does not apply)
	//	peekTallyXMLSupersededGUIDs     ->  []    (the step-up names nothing)
	//	CONTROL, the same document in UTF-8  ->  2025-04-10, seen correctly
	//
	// So the back-dated cutoff and the superseded-voucher step-up were BOTH inert
	// on the only encoding a real Tally file actually uses, while the importer
	// read that same file perfectly and posted every voucher in it. An operator
	// who cannot key one back-dated journal could import ten thousand of them.
	//
	// ⚠️ T32 BELIEVED IT HAD FIXED THIS AND HAD NOT. It fixed the ENVELOPE SHAPE
	// (BODY > IMPORTDATA > REQUESTDATA) in both peeks, so they now walk a real
	// export's messages — but they never get there, because this gate upstream of
	// them still could not read the bytes. A fix to the second stage of a pipeline
	// is worth nothing while the first stage returns false.
	//
	// It is the SAME rule this file states six times over for JSON, in XML: A PEEK
	// THAT DECIDES A WRITE'S FATE MUST DECODE THE BODY THE WAY THE HANDLER DECODES
	// IT. The handler decodes it with NewTallyDecodeReader. So does this now.
	dec := xml.NewDecoder(integration.NewTallyDecodeReader(bytes.NewReader(body)))
	// Lenient for the same reason ParseTallyEnvelope is: real Tally exports
	// declare namespace prefixes (xmlns:UDF) that the strict decoder rejects.
	dec.Strict = false
	// Anything reaching the decoder is already UTF-8, so a declared encoding is
	// passed straight through rather than refused as unsupported — ParseTallyEnvelope
	// does exactly this, and a peek that refused what the parser accepts would be
	// the same disagreement in the other direction.
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	for {
		tok, err := dec.Token()
		if err != nil {
			return false // no element at all: not XML.
		}
		if se, ok := tok.(xml.StartElement); ok {
			return strings.EqualFold(se.Name.Local, "ENVELOPE")
		}
	}
}

// peekDepositDate extracts `deposit_date` from a TDS challan write body.
//
// UNLIKE THE PAY RUN, AN ABSENT DATE IS NOT FORCED TO REFUSE. A pay run with no
// readable wage month returns beforeEveryCutoff() so the cutoff cannot be
// bypassed by omitting a field. Here the store refuses a challan with no
// deposit_date outright (it is NOT NULL in migration 0069, and
// masters.validateTDSChallan parses it with exactly "2006-01-02"), so a body
// without a readable one never becomes a stored row — the request dies at 400
// with a sentence about the missing date rather than at 403 with one about a
// period lock, which is the more useful answer for the same input.
//
// ===========================================================================
// ROUND 5, HERE TOO: THE TRAILING-BYTE DOOR WAS OPEN IN THIS PEEK AS WELL
// ===========================================================================
// peekVoucherDate's round-4/5 header enumerates the ways a peek can disagree
// with the handler about what a body SAYS, and item 5 is the decode call itself:
// decodeJSON uses json.NewDecoder().Decode(), which reads the FIRST JSON value
// and IGNORES whatever follows, while json.Unmarshal REJECTS trailing bytes.
// This function used json.Unmarshal. So one junk byte appended to an otherwise
// perfect challan body made the peek answer "not JSON" -> nil -> THE CUTOFF DOES
// NOT APPLY, while handleTDSChallan decoded the same body perfectly and recorded
// the deposit. Measured before the fix:
//
//	peekDepositDate(`{"deposit_date":"2026-05-01"}`)          -> 2026-05-01
//	peekDepositDate(`{"deposit_date":"2026-05-01"} trailing`) -> nil (cutoff off)
//
// Unlike the loose-spelling case above, the date here is PERFECTLY READABLE and
// validateTDSChallan accepts it, so the row lands inside the locked period —
// which is the whole hazard and not a 400 in disguise. Fixing peekVoucherDate's
// copy of this divergence and leaving the sibling's is how this control has
// already been re-opened four times: the door moves one route sideways.
func peekDepositDate(body []byte) *time.Time {
	var probe struct {
		DepositDate string `json:"deposit_date"`
	}
	// ⚠️ DECODED THE WAY decodeJSON DECODES, for the reason above. Everything else
	// this call rejects — a truncated object, a CSV, a multipart preamble — still
	// errors and still returns nil, because the handler's own decode fails on those
	// too and answers 400 with nothing written.
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&probe); err != nil || probe.DepositDate == "" {
		return nil
	}
	d, err := time.Parse("2006-01-02", probe.DepositDate)
	if err != nil {
		return nil
	}
	return &d
}

// peekBankDate extracts `bank_date` from a bank-reconciliation write body — the
// THIRD route whose date is not spelled `voucher_date`, after the pay run's
// `wage_month` and the challan's `deposit_date`.
//
// ⛔ IT SHIPPED WITH routeAction's COMMENT ASSERTING THE CONTROL WAS IN PLACE:
// "the write is an Alter and therefore IS a write, so it correctly inherits the
// back-dated cutoff — a bank date asserted into a closed period is exactly the
// kind of back-dating that cutoff exists for". The claim was right and the
// control was absent: setBankDateReq carries `bank_date`, the peek looked for
// `voucher_date`, so the PDP had no date, and cutoffDeny skips entirely when the
// date is nil. Byte for byte the challan's defect, found the same way — by
// asking whether the claim was true rather than whether it was plausible.
//
// ===========================================================================
// ⛔ AND IT IS *NOT* A COPY OF peekDepositDate, BECAUSE THE STORE IS DIFFERENT
// ===========================================================================
// peekDepositDate returns nil for a date it cannot parse, and the reason is
// stated there: masters.validateTDSChallan parses `deposit_date` with EXACTLY
// "2006-01-02", so a loosely-spelled one never becomes a stored row — it dies at
// 400 naming the field, which is the more useful answer than a 403 about a lock.
//
// THAT ARGUMENT DOES NOT HOLD HERE. banking.SetBankDate hands the string
// STRAIGHT to Postgres as `$6::date`, and Postgres accepts spellings Go's
// "2006-01-02" layout rejects — `2026-5-20` among them. So a loose spelling
// REACHES THE TABLE, dated inside the locked period, exactly as round 1's
// loosely-spelled `voucher_date` did. A present-but-unparseable bank date
// therefore fails CLOSED (the pay run's convention) rather than returning nil:
// "I cannot tell which period this asserts about" must never be spelled the same
// way as "it asserts about no period".
//
// An ABSENT or empty bank_date still returns nil, and that one IS peekDepositDate's
// call: handleSetBankDate refuses it with "bank_date is required" before
// banking.SetBankDate is reached, so no row can land in the locked period through
// an omitted field, and a 400 naming the field beats a 403 about a period lock.
func peekBankDate(body []byte) *time.Time {
	var probe struct {
		BankDate string `json:"bank_date"`
	}
	// ⚠️ DECODED THE WAY decodeJSON DECODES — `Decoder.Decode`, not
	// `json.Unmarshal` — which is round 5's trailing-byte door, and binding through
	// encoding/json is round 4's key-case door. Both are structural here rather
	// than remembered: the probe is a struct with the handler's own tag, so the two
	// bind the same key by the same rules.
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&probe); err != nil || probe.BankDate == "" {
		return nil
	}
	d, err := time.Parse("2006-01-02", probe.BankDate)
	if err != nil {
		return beforeEveryCutoff() // see the header: Postgres would accept it.
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
	// ⚠️ DECODED THE WAY handlePayrollRun DECODES — `Decoder.Decode`, not
	// `json.Unmarshal`. This one was NOT a bypass, because its error branch fails
	// CLOSED, but it diverged in the other direction: `json.Unmarshal` rejects
	// trailing bytes that `decodeJSON` accepts, so a pay run the handler would have
	// run happily was REFUSED as if it were back-dated, naming a cutoff the operator
	// had not crossed. Aligned with its four siblings after the same divergence was
	// found to be an actual hole in three of them.
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&probe); err != nil {
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

// writeCodedError is writeError plus a MACHINE-READABLE code, for the refusals a
// client has to tell apart from every other failure of the same status.
//
// ⛔ THE TWO KEYS ARE FOR TWO DIFFERENT READERS and both are load-bearing — the
// same split writeDraftAlreadyAccepted documents at length. `error` is the
// sentence a human reads and the only key http.ts lifts into ApiError.message;
// `code` is the stable token a screen switches on. A screen that has to
// recognise a refusal by matching its PROSE is a screen that breaks the next
// time somebody improves the wording — and it is why D112 shipped: the payroll
// register had nothing but the sentence, so an ordinary unrun month and a real
// server failure were literally indistinguishable to it.
func writeCodedError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// writeDraftAlreadyAccepted refuses a second accept of one insightd draft.
//
// ⛔⛔ THE BODY SHAPE IS insightd'S, NOT CORED'S, AND THAT IS THE WHOLE POINT OF
// WRITING IT BY HAND HERE. The browser's duplicate-bill guard reads
// `err.body.detail.code` on a 409 and translates exactly three insightd codes
// into sentences an operator can act on (draftAccept.ts's conflictCode /
// conflictField). `draft_already_accepted` is one of them, and it already names
// the winning voucher in `accepted_voucher_no` / `accepted_voucher_id`. Emitting
// cored's usual {"error": ...} instead would leave the client with an unrecognised
// 409 — still a refusal, but a generic one that could not tell the operator WHICH
// voucher their bill is already on, which is the single most useful fact here.
//
// `error` is carried AS WELL so ApiError.message is a sentence rather than
// "request failed: 409" — http.ts reads that key and nothing else for the message.
// Two keys, one refusal, and both readers get what they parse.
func writeDraftAlreadyAccepted(w http.ResponseWriter, e *ledger.DraftAlreadyAcceptedError) {
	detail := map[string]any{
		"code":     "draft_already_accepted",
		"draft_id": e.DraftID,
	}
	// Only ever the winner's REAL identity. An empty voucher_no is left out rather
	// than sent as "", because the client renders `accepted_voucher_no ||
	// accepted_voucher_id` and an empty string that is present reads to a human as
	// a voucher with no number.
	if e.VoucherNo != "" {
		detail["accepted_voucher_no"] = e.VoucherNo
	}
	if e.VoucherID != uuid.Nil {
		detail["accepted_voucher_id"] = e.VoucherID.String()
	}
	writeJSON(w, http.StatusConflict, map[string]any{
		"error":  e.Error(),
		"detail": detail,
	})
}
