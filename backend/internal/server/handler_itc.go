package server

// handler_itc.go holds the HTTP surface over compliance/itc: the books-vs-2B
// reconciliation report, the IMS worklist, and the three writes that feed them
// (statement ingest, IMS action, return filing).
//
// ---------------------------------------------------------------------------
// WIRING (the two-touch rule, IMPLEMENTATION_PLAN.md sec 1.5)
// ---------------------------------------------------------------------------
// NOTHING HERE IS REGISTERED IN THIS FILE, deliberately, exactly as
// handler_gstr3b.go and internal/ledger/payroll_class.go sec 4 describe. A route
// only exists once the serial wiring stage adds BOTH
//
//	(a) the r.Get/r.Post in server.go routes(), and
//	(b) the matching case in routeAction() in auth_middleware.go
//	    (plus, for the two REPORTS, the name in authz.KnownReportNames),
//
// because routeAction default-DENIES an unmapped {companyID} sub-path: adding
// (a) alone makes even an Owner get a 403. Until then these methods are
// unreachable, which is the fail-closed direction.
//
// THE TWO REPORT PATH SEGMENTS ARE FIXED BY MIGRATION 0033, WHICH IS ALREADY
// APPLIED. It names the reports `report:itc-reconciliation` and
// `report:ims-inbox`, and TestReportVocabularyMatchesRouter requires
// GET /v1/companies/{companyID}/<name> to resolve for every name in
// authz.KnownReportNames. So the segments are, exactly:
//
//	GET  /itc-reconciliation   -> handleITCReconciliation   (report name: itc-reconciliation)
//	GET  /ims-inbox            -> handleIMSInbox            (report name: ims-inbox)
//	POST /gstr2b-import        -> handleGSTR2BImport        (not a report)
//	POST /ims-actions          -> handleIMSAction           (not a report)
//	POST /gst-filings          -> handleGSTFiling           (not a report)
//
// An older handoff document says /gstr2b-reconciliation and /ims-worklist.
// Those are WRONG: they would produce ACL rules that can never match, which is
// the silent-wrong-answer failure 0033 exists to prevent.
//
// ---------------------------------------------------------------------------
// THE CLOCK LIVES HERE AND NOWHERE ELSE
// ---------------------------------------------------------------------------
// compliance/itc NEVER calls time.Now(): every boundary in it (deemed
// acceptance, the IMS freeze, the s.16(4) time bar) decides which TAX PERIOD a
// credit lands in, and a package that reads the wall clock cannot be tested at
// those boundaries. It returns itc.ErrNoAsOn rather than defaulting.
//
// So THIS FILE is the only place that reads the clock for ITC: `as_on` defaults
// to time.Now() at the handler boundary, in resolveAsOn, and is threaded to the
// engine both as itc.Options.AsOn and — for the gstr.ITCSource path, whose
// Decisions(ctx, from, to) signature has no room for a clock — on the context
// via itc.WithAsOn. A MALFORMED `as_on` IS A 400, never a silent fallback to
// now: "the timestamp you sent was unparseable so I used a different one" is
// how a report for the wrong instant looks successful.

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance/itc"
	"github.com/khata/cored/internal/platform/db"
)

// maxStatementBody caps an uploaded GSTR-2A/2B file. It is deliberately larger
// than maxJSONBody (1 MiB, sized for hand-written API bodies): a real monthly 2B
// for a busy taxpayer is a multi-megabyte JSON document, and truncating one
// would present as "the parser skipped some sections" — a partial statement,
// which under model (b) reverses the ITC of every document that fell off the
// end. Refusing the upload outright is the survivable failure.
const maxStatementBody = 32 << 20 // 32 MiB

// ---------------------------------------------------------------------------
// GET /v1/companies/{companyID}/itc-reconciliation
// ---------------------------------------------------------------------------

// handleITCReconciliation serves the books-vs-2B reconciliation for one tax
// period.
//
// Query params:
//
//	from, to    REQUIRED, YYYY-MM-DD. They must span EXACTLY ONE calendar month
//	            (see the ErrWindowNotOnePeriod handling below).
//	statement_id OPTIONAL. Absent means "the newest non-superseded statement for
//	            the period", which is what the engine loads. Supplied, it is
//	            CHECKED against the statement the engine actually used and a
//	            mismatch is a 400 — see the note on statementIDMismatch.
//	kind        OPTIONAL, GSTR2B (default) or GSTR2A.
//	as_on       OPTIONAL, RFC3339. Defaults to now AT THIS BOUNDARY.
func (s *Server) handleITCReconciliation(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	from, to := q.Get("from"), q.Get("to")
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query params are required (YYYY-MM-DD), "+
			"and must span exactly one tax period (calendar month)")
		return
	}
	asOn, err := resolveAsOn(q.Get("as_on"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	wantStatement, err := optionalUUID(q.Get("statement_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid statement_id (want a UUID)")
		return
	}
	kind := strings.ToUpper(strings.TrimSpace(q.Get("kind")))
	if kind != "" && kind != itc.KindGSTR2A && kind != itc.KindGSTR2B {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"invalid kind %q: want %s or %s", kind, itc.KindGSTR2B, itc.KindGSTR2A))
		return
	}

	rep, err := s.itc.Reconcile(itc.WithAsOn(ctx, asOn), from, to, itc.Options{Kind: kind, AsOn: asOn})
	if err != nil {
		writeITCError(w, from, to, err)
		return
	}
	if wantStatement != uuid.Nil && wantStatement != rep.StatementID {
		writeError(w, http.StatusBadRequest, statementIDMismatch(wantStatement, rep))
		return
	}
	writeJSON(w, http.StatusOK, itcReportJSON(rep, asOn))
}

// statementIDMismatch explains a statement_id that is not the one the engine
// reconciled against.
//
// WHY THIS IS A REFUSAL AND NOT A SILENT SUBSTITUTION. itc.Options carries no
// statement id: Reconcile always loads the NEWEST NON-SUPERSEDED statement for
// the period, which is the only correct default (a superseded 2B is a draft the
// portal has already replaced). A handler that accepted a statement_id and then
// quietly answered for a different statement would produce a report the caller
// would reasonably file, attributed to a document it was not derived from —
// exactly the "cannot name which 2B produced it" failure itc.Report.StatementID
// exists to prevent. Reported as a handoff: honouring an explicit statement_id
// needs a StatementID field on itc.Options, which is not this file's to add.
func statementIDMismatch(want uuid.UUID, rep itc.Report) string {
	if rep.StatementID == uuid.Nil {
		return fmt.Sprintf("statement_id %s was requested but NO inward statement has been "+
			"ingested for tax period %s; ingest one first (POST /gstr2b-import)", want, rep.Period)
	}
	return fmt.Sprintf("statement_id %s was requested but the newest non-superseded statement "+
		"for tax period %s is %s; a reconciliation is always against the live statement, and "+
		"answering for a superseded one would attribute the return to a document it was not "+
		"derived from. Re-request without statement_id, or with %s",
		want, rep.Period, rep.StatementID, rep.StatementID)
}

// ---------------------------------------------------------------------------
// GET /v1/companies/{companyID}/ims-inbox
// ---------------------------------------------------------------------------

// handleIMSInbox serves the IMS worklist for one tax period: every statement
// document IMS actually covers, its effective state as at `as_on`, and — from
// the SAME pure gate the write path uses (itc.ValidateAction) — which actions
// would be accepted on it right now.
//
// Query params: return_period (REQUIRED, MMYYYY), as_on (OPTIONAL, RFC3339).
//
// The action list is computed rather than assumed for one reason: after the
// GSTR-3B for the period is filed the action set is FROZEN (GSTN IMS FAQ 12), and
// an inbox that still offered ACCEPT/REJECT would invite an operator to record
// something the portal will not honour.
func (s *Server) handleIMSInbox(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	rawPeriod := strings.TrimSpace(q.Get("return_period"))
	if rawPeriod == "" {
		writeError(w, http.StatusBadRequest, "return_period query param is required (MMYYYY, e.g. 102025)")
		return
	}
	period, err := itc.ParsePeriod(rawPeriod)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid return_period (want MMYYYY, e.g. 102025): "+err.Error())
		return
	}
	asOn, err := resolveAsOn(q.Get("as_on"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Calendar-month arithmetic, never day arithmetic: the last day of the
	// period is the day before the first of the next one, which is right for 28,
	// 29, 30 and 31 day months alike. This is the same derivation
	// itc.checkOnePeriod applies, so the window it builds always satisfies the
	// one-period contract.
	from := period.Start().Format("2006-01-02")
	to := period.AddMonths(1).Start().AddDate(0, 0, -1).Format("2006-01-02")

	rep, err := s.itc.Reconcile(itc.WithAsOn(ctx, asOn), from, to, itc.Options{AsOn: asOn})
	if err != nil {
		writeITCError(w, from, to, err)
		return
	}
	writeJSON(w, http.StatusOK, imsInboxJSON(rep, period, asOn))
}

// ---------------------------------------------------------------------------
// POST /v1/companies/{companyID}/gstr2b-import
// ---------------------------------------------------------------------------

// handleGSTR2BImport ingests one GSTR-2A/2B statement file.
//
// THE BODY IS THE FILE, VERBATIM. The sha256 that makes a re-upload idempotent
// (and a different file a new, SUPERSEDING statement) is taken over exactly
// these bytes, so the file must not be re-encoded on the way in — a JSON
// envelope that re-marshalled it would change the digest of a byte-identical
// re-upload and produce a second statement for the same period.
//
// Query params:
//
//	kind         REQUIRED, GSTR2B or GSTR2A. STATED BY THE CALLER, never guessed:
//	             the two are structurally identical in this rendering and
//	             guessing wrong puts a 2A's documents under a 2B's
//	             deemed-acceptance clock.
//	ingested_by  the audit actor, used only when the request carries no
//	             authenticated subject (see requireActor).
func (s *Server) handleGSTR2BImport(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	kind := strings.ToUpper(strings.TrimSpace(q.Get("kind")))
	if kind != itc.KindGSTR2A && kind != itc.KindGSTR2B {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"kind query param is required and must be %s or %s (it is stated, never guessed: "+
				"the two renderings are structurally identical and a 2A filed as a 2B would be put "+
				"under a deemed-acceptance clock that does not apply to it)",
			itc.KindGSTR2B, itc.KindGSTR2A))
		return
	}
	actor, ok := requireActor(w, r, "ingested_by")
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxStatementBody)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf(
			"could not read the statement body (max %d bytes): %v", maxStatementBody, err))
		return
	}
	if len(raw) == 0 {
		writeError(w, http.StatusBadRequest, "empty request body: POST the statement file itself as the body")
		return
	}

	// The ingest instant is read HERE, at the boundary, for the same reason
	// as_on is: nothing in compliance/itc calls time.Now().
	res, err := s.itc.Ingest(ctx, itc.IngestInput{
		Kind:       kind,
		Raw:        raw,
		IngestedAt: time.Now(),
		IngestedBy: actor,
	})
	if err != nil {
		writeITCError(w, "", "", err)
		return
	}

	body := map[string]any{
		"statement_id":  res.StatementID.String(),
		"return_period": res.ReturnPeriod,
		"kind":          kind,
		// inserted=false is the idempotent re-upload of a byte-identical file. It
		// is a 200, not a 201 and not an error: an operator who clicks twice must
		// not end up with two statements.
		"inserted":   res.Inserted,
		"superseded": res.Superseded,
		"documents":  res.Documents,
		"rejected":   res.Rejected,
		"skipped":    append([]string{}, res.Skipped...),
	}
	if len(res.Skipped) > 0 {
		// Any skip at all raises `statement-partially-parsed`, which raises
		// `not-filing-ready` on every return derived from this statement. Said
		// here too so an importer that never reads a return still sees it.
		body["disclosures"] = []string{"statement-partially-parsed", "not-filing-ready"}
	}
	status := http.StatusOK
	if res.Inserted {
		status = http.StatusCreated
	}
	writeJSON(w, status, body)
}

// ---------------------------------------------------------------------------
// POST /v1/companies/{companyID}/ims-actions
// ---------------------------------------------------------------------------

// imsActionRequest is the body of POST /ims-actions.
//
// THERE IS NO acted_at FIELD, ON PURPOSE. The action instant is read from the
// clock at this boundary. It decides whether the action beats the 2B resolution
// instant and whether it precedes the filing freeze, i.e. whether it counts at
// all; a caller-supplied instant would let a filer back-date an action past a
// return that has already been filed and make the append-only log disagree with
// the portal.
type imsActionRequest struct {
	DocumentID  string `json:"document_id"`
	Action      string `json:"action"`
	Reason      string `json:"reason"`
	PendingCase string `json:"pending_case"`
	ActedBy     string `json:"acted_by"`
}

// handleIMSAction appends one IMS action to the append-only log.
func (s *Server) handleIMSAction(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req imsActionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	docID, err := uuid.Parse(strings.TrimSpace(req.DocumentID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "document_id is required and must be a UUID")
		return
	}
	action := strings.ToUpper(strings.TrimSpace(req.Action))
	switch action {
	case itc.ActionAccept, itc.ActionReject, itc.ActionPending, itc.ActionReset:
	default:
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"invalid action %q: want one of %s, %s, %s, %s",
			req.Action, itc.ActionAccept, itc.ActionReject, itc.ActionPending, itc.ActionReset))
		return
	}
	pendingCase := itc.PendingCase(strings.ToUpper(strings.TrimSpace(req.PendingCase)))
	if pendingCase == "" {
		pendingCase = itc.PendingOrdinary
	}
	if !knownPendingCase(pendingCase) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"invalid pending_case %q: want one of %s", req.PendingCase,
			strings.Join(pendingCaseNames(), ", ")))
		return
	}
	actor, ok := requireActorValue(w, req.ActedBy, r, "acted_by")
	if !ok {
		return
	}

	if err := s.itc.RecordAction(ctx, itc.ActionInput{
		DocumentID:  docID,
		Kind:        action,
		Reason:      strings.TrimSpace(req.Reason),
		PendingCase: pendingCase,
		ActedAt:     time.Now(),
		ActedBy:     actor,
	}); err != nil {
		writeITCError(w, "", "", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"document_id":  docID.String(),
		"action":       action,
		"pending_case": string(pendingCase),
	})
}

// ---------------------------------------------------------------------------
// POST /v1/companies/{companyID}/gst-filings
// ---------------------------------------------------------------------------

// gstFilingRequest is the body of POST /gst-filings.
//
// filed_at IS accepted here, unlike acted_at on an IMS action, and the asymmetry
// is deliberate. The filing instant is a fact about something that happened ON
// THE PORTAL, possibly before this software was told about it; refusing to record
// it would force every back-entered filing to carry the wrong freeze. The IMS
// action instant, by contrast, is a fact about something that happened HERE.
type gstFilingRequest struct {
	Form         string `json:"form"`
	ReturnPeriod string `json:"return_period"`
	FiledAt      string `json:"filed_at"`
	FiledBy      string `json:"filed_by"`
}

// handleGSTFiling records that a return was filed, which STAMPS THE IMS FREEZE
// for the period: after it, no IMS action counts (GSTN IMS FAQ 12).
func (s *Server) handleGSTFiling(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	var req gstFilingRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	form := strings.ToUpper(strings.TrimSpace(req.Form))
	if form == "" {
		form = itc.FormGSTR3B
	}
	if form != itc.FormGSTR3B {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"invalid form %q: only %s is modelled", req.Form, itc.FormGSTR3B))
		return
	}
	period := strings.TrimSpace(req.ReturnPeriod)
	if _, err := itc.ParsePeriod(period); err != nil {
		writeError(w, http.StatusBadRequest,
			"return_period is required in MMYYYY form (e.g. 102025): "+err.Error())
		return
	}
	filedAt := time.Now()
	if raw := strings.TrimSpace(req.FiledAt); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, fmt.Sprintf(
				"invalid filed_at %q: want RFC3339 (e.g. 2025-11-20T10:30:00+05:30). It is not "+
					"defaulted to now, because the filing instant is the IMS freeze and a wrong "+
					"one silently changes which actions counted", raw))
			return
		}
		filedAt = t
	}
	actor, ok := requireActorValue(w, req.FiledBy, r, "filed_by")
	if !ok {
		return
	}

	if err := s.itc.RecordFiling(ctx, form, period, filedAt, actor); err != nil {
		writeITCError(w, "", "", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"form":          form,
		"return_period": period,
		"filed_at":      filedAt.Format(time.RFC3339),
	})
}

// ---------------------------------------------------------------------------
// Shared boundary helpers
// ---------------------------------------------------------------------------

// resolveAsOn parses the `as_on` query parameter, DEFAULTING TO NOW.
//
// This is the one and only place the ITC surface reads the wall clock. A
// malformed value is an ERROR rather than a fallback to now: a report generated
// for a different instant than the caller asked for is indistinguishable from a
// correct one at a glance, and `as_on` decides the IMS projection — which
// documents have deem-accepted, and therefore how much credit is in 4(A).
func resolveAsOn(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now(), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid as_on %q: want RFC3339 (e.g. 2025-11-15T00:00:00+05:30). "+
			"It is NOT defaulted to now when unparseable: as_on decides which IMS records have "+
			"deem-accepted, so answering for a different instant would silently change table 4(A)", raw)
	}
	return t, nil
}

// requireActor resolves the audit actor for a write, preferring the
// AUTHENTICATED SUBJECT over anything the request said about itself. The
// fallback query param exists only for the pre-auth call paths the rest of this
// codebase still uses (the `created_by` convention on masters and vouchers); it
// can never override a real subject.
func requireActor(w http.ResponseWriter, r *http.Request, param string) (string, bool) {
	return requireActorValue(w, r.URL.Query().Get(param), r, param)
}

// requireActorValue is requireActor over an already-extracted value (a JSON body
// field rather than a query param).
func requireActorValue(w http.ResponseWriter, fallback string, r *http.Request, name string) (string, bool) {
	if subject, ok := db.SubjectFromContext(r.Context()); ok && strings.TrimSpace(subject) != "" {
		return strings.TrimSpace(subject), true
	}
	if v := strings.TrimSpace(fallback); v != "" {
		return v, true
	}
	// Empty is refused rather than stored. The column is the only record of WHO
	// ingested a statement or actioned a document, and a blank one turns an audit
	// trail into a list of events with no author.
	writeError(w, http.StatusBadRequest, name+" is required when the request carries no "+
		"authenticated subject: it is the only record of who performed this action")
	return "", false
}

// knownPendingCase gates the pending_case vocabulary at the boundary. It is
// CLOSED: an unrecognised case would be coerced to ORDINARY by the service, and
// ORDINARY is the one case on which PENDING is always allowed — so a typo would
// turn a refusal into an acceptance.
func knownPendingCase(c itc.PendingCase) bool {
	for _, k := range allPendingCases {
		if c == k {
			return true
		}
	}
	return false
}

var allPendingCases = []itc.PendingCase{
	itc.PendingOrdinary,
	itc.PendingOriginalCreditNote,
	itc.PendingUpwardAmendmentOfCN,
	itc.PendingDownwardAmendmentOfRejectedCN,
	itc.PendingDownwardAmendmentOfAcceptedDoc,
}

func pendingCaseNames() []string {
	out := make([]string, 0, len(allPendingCases))
	for _, c := range allPendingCases {
		out = append(out, string(c))
	}
	return out
}

// writeITCError maps a compliance/itc error onto a status.
//
// ErrWindowNotOnePeriod IS A 400 AND MUST STAY ONE. Reconcile REFUSES a window
// that is not exactly one calendar month because only ONE period's GSTR-2B is
// ever loaded: on a wider window every purchase outside the statement's own
// month has nothing to match against, is classified MISSING_IN_2B and is
// REVERSED IN FULL in table 4(B)(2) — a quarterly call would reverse two thirds
// of the company's input tax credit while looking entirely successful. The
// refusal is the DESIGNED CONTRACT, so this handler neither returns a 500 (which
// reads as "the server is broken, retry") nor papers over it by iterating the
// months itself. The caller asks once per period.
func writeITCError(w http.ResponseWriter, from, to string, err error) {
	switch {
	case errors.Is(err, itc.ErrWindowNotOnePeriod):
		writeError(w, http.StatusBadRequest, err.Error()+onePeriodHint(from, to))
	case errors.Is(err, itc.ErrNoAsOn):
		// Unreachable from this file — every path threads WithAsOn — but mapped
		// so that a future caller that forgets gets a 400 naming the fix rather
		// than a 500.
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, db.ErrNoCompany):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, itc.ErrReturnFiled):
		// The period is FROZEN. 409, not 422: nothing about the request is
		// malformed, the world moved on.
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, itc.ErrUnknownAction):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, itc.ErrIMSNotApplicable), errors.Is(err, itc.ErrPendingNotAllowed),
		errors.Is(err, itc.ErrNoPeriod):
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	default:
		writeError(w, http.StatusUnprocessableEntity, err.Error())
	}
}

// onePeriodHint names the tax period the requested window resolves to and the
// exact from/to that would have been accepted, so the 400 is actionable rather
// than merely correct.
func onePeriodHint(from, to string) string {
	if from == "" {
		return ""
	}
	t, err := time.ParseInLocation("2006-01-02", from, itc.IST)
	if err != nil {
		return ""
	}
	p := itc.PeriodOf(t)
	first := p.Start()
	last := p.AddMonths(1).Start().AddDate(0, 0, -1)
	return fmt.Sprintf(". The window %s..%s resolves to tax period %s; request ONE MONTH AT A TIME, "+
		"e.g. from=%s&to=%s, and iterate the months for a longer span",
		from, to, p, first.Format("2006-01-02"), last.Format("2006-01-02"))
}

// ---------------------------------------------------------------------------
// Wire projections
// ---------------------------------------------------------------------------
//
// The itc types are projected EXPLICITLY rather than marshalled directly. Two
// reasons, and neither is style: (1) the field names on the wire are snake_case
// like every other report this server serves, and pinning them here means a
// rename inside compliance/itc is a compile-time decision rather than a silent
// breaking change to a frontend; (2) every money figure is emitted as a STRING
// at four decimal places, so no consumer can turn a rupee figure into a float.

func taxJSON(t itc.Tax) map[string]string {
	return map[string]string{
		"cgst":  t.CGST.StringFixed(4),
		"sgst":  t.SGST.StringFixed(4),
		"igst":  t.IGST.StringFixed(4),
		"total": t.Total().StringFixed(4),
	}
}

func moneyJSON(d decimal.Decimal) string { return d.StringFixed(4) }

func dayJSON(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

func instantJSON(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339)
}

func itcReportJSON(rep itc.Report, asOn time.Time) map[string]any {
	lines := make([]map[string]any, 0, len(rep.Lines))
	for _, l := range rep.Lines {
		lines = append(lines, itcLineJSON(l))
	}
	rejected := make([]map[string]any, 0, len(rep.RejectedStatementRows))
	for _, row := range rep.RejectedStatementRows {
		rejected = append(rejected, statementRowJSON(row))
	}
	out := map[string]any{
		"period":     rep.Period,
		"as_on":      asOn.Format(time.RFC3339),
		"resolution": rep.Resolution.Format(time.RFC3339),
		"freeze":     instantJSON(rep.Freeze),
		"lines":      lines,
		// docRejdata rows are VIEW-ONLY (GSTN IMS FAQ 24): they produce no
		// verdict and contribute to no table, but the operator must see them —
		// a supplier has reported something against this GSTIN and it was
		// refused.
		"rejected_statement_rows": rejected,
		"totals":                  totalsJSON(rep.Totals),
		"disclosures":             append([]string{}, rep.Disclosures...),
	}
	if rep.StatementID != uuid.Nil {
		out["statement_id"] = rep.StatementID.String()
	} else {
		// Explicitly null, never omitted: "no statement was ingested" is a fact
		// the caller must be able to read, and an absent key reads as a bug.
		out["statement_id"] = nil
	}
	out["statement_gen_date"] = instantJSON(rep.StatementGenDate)
	return out
}

func totalsJSON(t itc.Totals) map[string]any {
	return map[string]any{
		"avail_isrc": taxJSON(t.AvailISRC),
		"avail_oth":  taxJSON(t.AvailOTH),
		"rev_rule":   taxJSON(t.RevRule),
		"rev_other":  taxJSON(t.RevOther),
		"inelig":     taxJSON(t.Inelig),
		// net is 4(C) and MAY BE NEGATIVE — reversals can exceed availment in a
		// period. It is emitted as computed, never clamped.
		"net": taxJSON(t.Net()),
		// posted_total is sum(4A)+sum(4D), the left-hand side of identity (I1):
		// it must equal the period's input-tax POSTINGS head by head.
		"posted_total": taxJSON(t.PostedTotal()),
	}
}

func itcLineJSON(l itc.Line) map[string]any {
	fields := make([]string, 0, len(l.Fields))
	for _, f := range l.Fields {
		fields = append(fields, string(f))
	}
	findings := make([]map[string]string, 0)
	for _, f := range append(append([]itc.Finding{}, l.Findings...), l.EffectFindings()...) {
		findings = append(findings, map[string]string{
			"code":       string(f.Code),
			"side":       f.Side,
			"doc_no":     f.DocNo,
			"strict_key": f.StrictKey,
		})
	}
	candidates := make([]map[string]any, 0, len(l.Candidates))
	for _, c := range l.Candidates {
		m := map[string]any{
			"doc_no":   c.DocNo,
			"doc_date": dayJSON(c.DocDate),
			"taxable":  moneyJSON(c.Taxable),
			"why":      c.Why,
		}
		if c.StatementRowID != uuid.Nil {
			m["statement_row_id"] = c.StatementRowID.String()
		}
		if c.VoucherID != uuid.Nil {
			m["voucher_id"] = c.VoucherID.String()
		}
		candidates = append(candidates, m)
	}
	out := map[string]any{
		"status":         string(l.Status),
		"fields":         fields,
		"not_comparable": string(l.NotComparable),
		"ims_state":      string(l.IMSState),
		"delta": map[string]any{
			"taxable": moneyJSON(l.Delta.Taxable),
			"tax":     taxJSON(l.Delta.Tax),
		},
		"effect":     effectJSON(l.Effect),
		"findings":   findings,
		"candidates": candidates,
	}
	if l.Books != nil {
		out["books"] = booksRowJSON(*l.Books)
	} else {
		out["books"] = nil
	}
	if l.Statement != nil {
		out["statement"] = statementRowJSON(*l.Statement)
	} else {
		out["statement"] = nil
	}
	return out
}

func effectJSON(e itc.ITCEffect) map[string]any {
	return map[string]any{
		"rcm":       e.RCM,
		"avail":     taxJSON(e.Avail),
		"rev_rule":  taxJSON(e.RevRule),
		"rev_other": taxJSON(e.RevOther),
		"inelig":    taxJSON(e.Inelig),
	}
}

func booksRowJSON(b itc.BooksRow) map[string]any {
	return map[string]any{
		"voucher_id":        b.VoucherID.String(),
		"voucher_no":        b.VoucherNo,
		"voucher_date":      dayJSON(b.VoucherDate),
		"parent_class":      b.ParentClass,
		"doc_type":          string(b.DocType),
		"party_gstin":       b.PartyGSTIN,
		"supplier_doc_no":   b.SupplierDocNo,
		"supplier_doc_date": dayJSON(b.SupplierDocDate),
		"taxable":           moneyJSON(b.Taxable),
		"tax":               taxJSON(b.Tax),
		"reverse_charge":    b.ReverseCharge,
		"itc_eligibility":   b.ITCEligibility,
		"blocked_clause":    b.BlockedClause,
		"has_gst_params":    b.HasGSTParams,
		"bill_ref":          b.BillRef,
	}
}

func statementRowJSON(s itc.StatementRow) map[string]any {
	return map[string]any{
		"document_id":            s.ID.String(),
		"statement_id":           s.StatementID.String(),
		"section":                s.Section,
		"rejected":               s.Rejected,
		"supplier_gstin":         s.SupplierGSTIN,
		"supplier_trade_name":    s.SupplierTradeName,
		"supplier_return_period": s.SupplierReturnPeriod,
		"supplier_filed_date":    dayJSON(s.SupplierFiledDate),
		"doc_no":                 s.DocNo,
		"doc_date":               dayJSON(s.DocDate),
		"doc_type":               string(s.DocType),
		"doc_value":              moneyJSON(s.DocValue),
		"pos":                    s.POS,
		"reverse_charge":         s.ReverseCharge,
		// itc_available is GSTN's OWN verdict (itcavl). Where it disagrees with
		// ours the DISAGREEMENT is a finding on the line; GSTN's verdict wins,
		// because it is the one the department will use.
		"itc_available":          s.ITCAvailable,
		"itc_unavailable_reason": s.ITCUnavailableReason,
		"taxable":                moneyJSON(s.Taxable),
		"tax":                    taxJSON(s.Tax),
		"cess":                   moneyJSON(s.Cess),
		"orig_doc_no":            s.OrigDocNo,
		"orig_doc_date":          dayJSON(s.OrigDocDate),
		"ims_status_in_file":     s.IMSStatusInFile,
		"irn":                    s.IRN,
	}
}

// imsInboxJSON projects the reconciliation onto the IMS worklist: only the
// documents IMS actually covers.
//
// The exclusions are not a filter for brevity, they are the law. Imports (IMPG,
// IMPGSEZ), ISD credit (ISD, ISDA) and EVERY reverse-charge row bypass IMS
// entirely (GSTN IMS FAQ 8 and 18: "RCM invoices are not part of IMS but will
// continue to be part of GSTR-2B"). Showing an RCM row in an inbox with a REJECT
// button is the worst failure available here: a filer rejects it believing the
// credit goes away, when in law they owe the tax and hold the credit regardless.
func imsInboxJSON(rep itc.Report, period itc.Period, asOn time.Time) map[string]any {
	frozen := rep.Freeze != nil
	items := make([]map[string]any, 0, len(rep.Lines))
	for _, l := range rep.Lines {
		if l.Statement == nil {
			continue
		}
		st := *l.Statement
		if st.Rejected || !itc.IMSApplies(st.Section, st.ReverseCharge) {
			continue
		}
		item := statementRowJSON(st)
		item["ims_state"] = string(l.IMSState)
		item["match_status"] = string(l.Status)
		if l.Books != nil {
			item["books_voucher_id"] = l.Books.VoucherID.String()
			item["books_voucher_no"] = l.Books.VoucherNo
		} else {
			item["books_voucher_id"] = nil
		}
		// The allowed actions come from the SAME pure gate the write path runs
		// (itc.ValidateAction), so the inbox can never offer an action the POST
		// would then refuse.
		allowed := make([]string, 0, 4)
		for _, k := range []string{itc.ActionAccept, itc.ActionReject, itc.ActionPending, itc.ActionReset} {
			if itc.ValidateAction(k, st.Section, st.ReverseCharge, period, itc.PendingOrdinary, frozen) == nil {
				allowed = append(allowed, k)
			}
		}
		item["actions_allowed"] = allowed
		// PENDING is EFFECTIVE-DATED and case-dependent: before the October-2025
		// tax period four categories cannot be kept pending at all, and from it
		// they may be deferred for exactly one period. Reporting one boolean
		// would be wrong for four of the five cases, so all five are reported.
		byCase := map[string]any{}
		for _, c := range allPendingCases {
			ok, expires := itc.PendingAllowed(period, c)
			entry := map[string]any{"allowed": ok}
			if !expires.IsZero() {
				entry["expires_at"] = expires.Format(time.RFC3339)
			}
			byCase[string(c)] = entry
		}
		item["pending_allowed_by_case"] = byCase
		items = append(items, item)
	}

	viewOnly := make([]map[string]any, 0, len(rep.RejectedStatementRows))
	for _, row := range rep.RejectedStatementRows {
		viewOnly = append(viewOnly, statementRowJSON(row))
	}

	out := map[string]any{
		"return_period": period.String(),
		"as_on":         asOn.Format(time.RFC3339),
		"resolution":    rep.Resolution.Format(time.RFC3339),
		"freeze":        instantJSON(rep.Freeze),
		// frozen is the single flag a UI needs to disable every action control:
		// after the GSTR-3B for the period is filed, no action counts.
		"frozen":      frozen,
		"items":       items,
		"view_only":   viewOnly,
		"disclosures": append([]string{}, rep.Disclosures...),
	}
	if rep.StatementID != uuid.Nil {
		out["statement_id"] = rep.StatementID.String()
	} else {
		out["statement_id"] = nil
	}
	return out
}
