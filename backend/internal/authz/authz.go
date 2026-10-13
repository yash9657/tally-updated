// Package authz is the server-side Policy Decision Point (PDP). It replicates
// Tally's Owner / Data-Entry / custom security levels with Disallow/Allow rules,
// evaluated in Go (OPA/Rego is a documented future swap — Evaluate is the
// equivalent, hermetically testable today).
//
// The PDP is enforced on EVERY request after authn + membership: given the
// caller's security Level and its Rules for the {companyID}, plus the requested
// (Action, Resource), Evaluate returns an allow/deny Decision. Precedence is
// pinned and tested non-vacuously (see authz_test.go):
//
//  1. service principal (handled by the middleware via Claims.IsService) -> full
//     allow scoped to the targeted company (back-dated cutoff + webauthn do NOT
//     apply to svc principals). EvaluateService encodes this.
//  2. level.IsOwner -> allow, BUT still apply the back-dated cutoff for writes.
//  3. else most-specific rule match wins; DISALLOW beats ALLOW at equal
//     specificity; exact resource+action beats a '*' wildcard. A matching
//     disallow denies; else a matching allow allows; else default_access:
//     full -> allow, none -> deny.
//  4. back-dated cutoff: Action in {Create,Alter,Delete} and cutoff != nil and
//     VoucherDate < cutoff -> deny (403).
//  5. webauthn: level.RequiresWebAuthn and Action is a write and !HasWebAuthn ->
//     deny with the STEPUP_REQUIRED contract (403; the middleware emits the
//     distinguishable step-up body, reserving 401 for missing/invalid tokens).
//
// This file (and store.go) are owned by WS1.
package authz

import (
	"time"

	"github.com/google/uuid"
)

// Action is a Tally operation verb. The canonical wire/DB tokens are lowercase.
type Action string

const (
	Create  Action = "create"
	Alter   Action = "alter"
	Delete  Action = "delete"
	Display Action = "display"
	Print   Action = "print"
)

// THERE IS DELIBERATELY NO `Export` ACTION. It existed here and in KnownActions
// while no code path consulted it; the T15 Alt+E export made that dangerous
// rather than merely dead, because an owner could then be SHOWN a
// `disallow export report:X` rule as valid and watch the export happen anyway.
// The full reasoning — including why the client-side export is governed by
// `display` and what would have to be true to bring the token back — is on
// KnownActions in vocabulary.go. Migration 0011's column comment still lists
// `export` among the tokens; migrations are immutable history and that one
// describes what the vocabulary was when it was written.

// Resource is the object class an Action targets: a voucher class, or one of the
// coarse resources master/report/import/sync, or the wildcard "*".
type Resource string

// Wildcard is the rule token that matches any action or resource.
const Wildcard = "*"

// Effect is a rule's allow/disallow polarity.
type Effect string

const (
	Allow    Effect = "allow"
	Disallow Effect = "disallow"
)

// IsWrite reports whether an action mutates the ledger (subject to the back-dated
// cutoff and the WebAuthn step-up requirement).
func (a Action) IsWrite() bool {
	switch a {
	case Create, Alter, Delete:
		return true
	default:
		return false
	}
}

// Request is one access-control question the PDP answers.
type Request struct {
	UserID       uuid.UUID
	CompanyID    uuid.UUID
	Action       Action
	Resource     Resource
	ResourceName string     // e.g. a report name, when Resource == "report"
	VoucherDate  *time.Time // for the back-dated cutoff on writes; nil if unknown
	// StoredVoucherDate is the date the document this write NAMES already
	// carries IN THE DATABASE — the SOURCE of a move, where VoucherDate is its
	// DESTINATION. nil means the request names no already-stored dated document
	// (a create), or the cutoff is not set and no read was made.
	//
	// ===========================================================================
	// WHY A SECOND DATE FIELD EXISTS — ROUND 3, DRIVEN ON THE WIRE
	// ===========================================================================
	// Two earlier rounds hardened the cutoff against a date the PDP could not
	// READ (a loose spelling) and against a date it could not FIND (nested one
	// level down in a /sync/commit payload, or in a Tally XML <DATE> element).
	// Both rounds judged exactly ONE date: the one the REQUEST carried. A browser
	// re-drive then walked straight through the lock without sending a back-dated
	// date at all:
	//
	//	ALTER  PUT /v1/companies/<C>/vouchers/journal/<id>  voucher_date=2026-08-20
	//	       -> 200 {"version_no":2}     (the voucher was dated 2026-05-01, i.e.
	//	                                   INSIDE the period the cutoff locks)
	//	DELETE DELETE /v1/companies/<C>/vouchers/<id>
	//	       -> 200 {"op":"DELETE","version_no":3}   (no body, so NO date at all)
	//	CONTROL POST /v1/companies/<C>/vouchers/journal    voucher_date=2026-05-01
	//	       -> 403                     (the cutoff was live the whole time)
	//
	// Neither door is body-shaped, which is why neither earlier round could reach
	// it. The date that decides them is the one ALREADY IN THE BOOKS: a DELETE has
	// no body to peek, and an ALTER's body only ever states where the voucher is
	// GOING. An operator who could not post into a filed quarter could still
	// empty it one voucher at a time, or lift each voucher out of it by restating
	// it with next month's date. A cutoff you can delete around is not a cutoff.
	//
	// ===========================================================================
	// AND WHY IT IS A SECOND FIELD RATHER THAN FOLDED INTO VoucherDate
	// ===========================================================================
	// Arithmetically the middleware COULD hand over min(stored, requested) in the
	// single field and this control would be exactly as tight: everything the
	// cutoff asks is `date >= cutoff`, and min(a,b) >= cutoff iff both are. It is
	// two fields because of what the operator is TOLD. With one field, an ALTER
	// that moves a May voucher to 20 August is refused with "voucher date is
	// before the back-dated cutoff of 2026-07-01" — a sentence about a date the
	// operator did not send, which sends them hunting for a typo in a field that
	// is correct. That is precisely the lie the IsZero branch of cutoffDeny was
	// written to avoid, and the same argument applies here. Two fields let the
	// refusal name WHICH end of the move is inside the lock.
	StoredVoucherDate *time.Time
	// DateSubject names, in the operator's words, WHICH date the two fields above
	// carry. Empty means the voucher's own date — every write in the product
	// except the bank-reconciliation routes, whose date is a `bank_date` ticked
	// off a statement.
	//
	// It exists so a refusal cannot name a field the operator's screen does not
	// have. It changes NO wording when it is empty: the four cutoff sentences are
	// read verbatim by authz/cutoff_reason_test.go, auth_backdated_spelling_test.go
	// and auth_payroll_cutoff_test.go, and by the client, which surfaces them
	// unaltered.
	DateSubject string
	HasWebAuthn bool // from the token AMR
}

// Decision is the PDP verdict.
type Decision struct {
	Allow      bool
	Reason     string
	HTTPStatus int // 403 on an ordinary deny; 0 when Allow
}

// StepUpRequired is the canonical reason string for a WebAuthn step-up denial.
// The middleware keys its distinguishable 403 STEPUP_REQUIRED response off it.
const StepUpReason = "webauthn step-up required"

// Level mirrors a security_level row.
type Level struct {
	ID               uuid.UUID
	Name             string
	IsOwner          bool
	DefaultAccess    string // "full" or "none"
	RequiresWebAuthn bool
	BackdatedCutoff  *time.Time // nil if no cutoff

	// BooksClosedThrough is the COMPANY's year-end lock: the inclusive last
	// date of a period that has been closed. nil means no year is closed.
	//
	// ⛔ IT IS A COMPANY FACT, NOT A LEVEL ONE, and it is carried on Level for
	// one reason: a Level is already scoped to exactly one company and is loaded
	// by one query, so Store.Level reads it in the same round trip. It must
	// NEVER be exposed by the admin API as a level attribute — an administrator
	// editing a role must not be able to re-open a filed year, which is half
	// the reason it is not stored as N per-level cutoffs. See migration 0089.
	//
	// ⚠️ "THROUGH" IS INCLUSIVE AND A CUTOFF IS EXCLUSIVE. They are one day
	// apart and that day is the whole meaning of a year-end, which is why they
	// are different fields with different names rather than one reused. The
	// conversion happens in exactly one place: effectiveCutoff.
	BooksClosedThrough *time.Time
}

// Rule mirrors an acl_rule row.
type Rule struct {
	Effect   Effect
	Action   Action   // may be the wildcard "*"
	Resource Resource // may be the wildcard "*" (and "report" can carry a :NAME via ResourceName matching)
}

// allow / deny constructors.
func allow(reason string) Decision { return Decision{Allow: true, Reason: reason} }
func deny(reason string) Decision  { return Decision{Allow: false, Reason: reason, HTTPStatus: 403} }

// stepUp is the distinguishable WebAuthn step-up denial (still 403; the
// middleware adds the STEPUP_REQUIRED code/body so a client can tell it apart
// from a missing-token 401).
func stepUp() Decision {
	return Decision{Allow: false, Reason: StepUpReason, HTTPStatus: 403}
}

// EvaluateService is the synthetic full-access decision for a service principal
// (subject prefix "svc:"). It is scoped to the targeted company by the caller
// (the middleware sets the company GUC). The back-dated cutoff and WebAuthn
// step-up do NOT apply to service principals.
func EvaluateService() Decision { return allow("service principal: full access") }

// Evaluate applies the pinned precedence to a (level, rules, request) triple.
func Evaluate(level Level, rules []Rule, req Request) Decision {
	// (2) Owner: allow, but still enforce the back-dated cutoff on writes.
	if level.IsOwner {
		if d, blocked := cutoffDeny(level, req); blocked {
			return d
		}
		if d, blocked := webauthnDeny(level, req); blocked {
			return d
		}
		return allow("owner: full access")
	}

	// (3) Most-specific rule match. Specificity: exact action+resource (2) >
	// one wildcard (1) > both wildcard (0). DISALLOW beats ALLOW at equal
	// specificity, so a tie resolves to deny.
	bestSpec := -1
	var bestEffect Effect
	matched := false
	for _, r := range rules {
		spec, ok := ruleSpecificity(r, req)
		if !ok {
			continue
		}
		if spec > bestSpec {
			bestSpec = spec
			bestEffect = r.Effect
			matched = true
		} else if spec == bestSpec && r.Effect == Disallow {
			// disallow beats allow at equal specificity
			bestEffect = Disallow
		}
	}

	if matched {
		if bestEffect == Disallow {
			return deny("disallowed by rule")
		}
		// allow: still subject to cutoff + webauthn on writes.
		if d, blocked := cutoffDeny(level, req); blocked {
			return d
		}
		if d, blocked := webauthnDeny(level, req); blocked {
			return d
		}
		return allow("allowed by rule")
	}

	// (3 cont.) No rule matched -> fall back to default_access.
	if level.DefaultAccess == "full" {
		if d, blocked := cutoffDeny(level, req); blocked {
			return d
		}
		if d, blocked := webauthnDeny(level, req); blocked {
			return d
		}
		return allow("default access: full")
	}
	return deny("default access: none")
}

// ruleSpecificity returns how specifically a rule matches the request and
// whether it matches at all. A non-matching rule returns ok=false.
//
// resource matching supports a "report:NAME" form: a rule resource of
// "report:trial-balance" matches a request with Resource=="report" and
// ResourceName=="trial-balance" exactly (and counts as a more specific match
// than a bare "report").
func ruleSpecificity(r Rule, req Request) (int, bool) {
	actExact, actMatch := matchAction(r.Action, req.Action)
	if !actMatch {
		return 0, false
	}
	resSpec, resMatch := matchResource(r.Resource, req)
	if !resMatch {
		return 0, false
	}
	spec := 0
	if actExact {
		spec++
	}
	spec += resSpec // 0 wildcard, 1 exact resource, 2 exact resource:name
	return spec, true
}

func matchAction(ruleAct, reqAct Action) (exact bool, match bool) {
	if string(ruleAct) == Wildcard {
		return false, true
	}
	if ruleAct == reqAct {
		return true, true
	}
	return false, false
}

// matchResource returns a specificity weight (0 wildcard, 1 exact resource, 2
// exact resource:name) and whether it matches.
func matchResource(ruleRes Resource, req Request) (int, bool) {
	rs := string(ruleRes)
	if rs == Wildcard {
		return 0, true
	}
	// "report:NAME" form: match only when the request names that report exactly.
	if i := indexColon(rs); i >= 0 {
		base := rs[:i]
		name := rs[i+1:]
		if Resource(base) == req.Resource && name == req.ResourceName {
			return 2, true
		}
		return 0, false
	}
	if Resource(rs) == req.Resource {
		return 1, true
	}
	return 0, false
}

func indexColon(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}

// cutoffDeny applies the back-dated cutoff: a write whose voucher date is BEFORE
// the level's cutoff is denied. Returns (decision, true) when blocked.
//
// ⚠️ `req.VoucherDate == nil` MEANS "THE CUTOFF DOES NOT APPLY", AND THAT IS A
// FAIL-OPEN DOOR THE CALLER OWNS. The PDP cannot tell a write that legitimately
// has no date (a master create, an upload) from one whose date it simply failed
// to read, so the caller must not hand nil for the second case. The convention
// the callers use — see server.beforeEveryCutoff, and peekPayrollVoucherDate for
// why — is to pass the ZERO TIME when a date was REQUIRED and could not be
// determined: it is before every cutoff an administrator can set, so it lands on
// the deny below rather than skipping the control.
//
// THE REFUSAL NAMES THE CUTOFF DATE. Decision.Reason is written to the client
// verbatim (the middleware's writeError puts it in `{"error": ...}`), and a
// refusal that says only "before the back-dated cutoff" tells an operator that
// some period is locked without telling them WHERE the boundary is — so the only
// way to find out is to keep guessing dates. The date is rendered exactly as
// GET /security-levels renders it (`.UTC().Format("2006-01-02")`), so the
// refusal and the level's own JSON cannot show two different boundaries. It
// leaks nothing: it is this caller's own company's cutoff, and the caller can
// already read it on the Members screen.
//
// ⚠️ IT JUDGES BOTH DATES ON THE REQUEST, AND BOTH MUST CLEAR THE CUTOFF.
// A write that names an existing voucher is a MOVE, and it has two ends:
//
//	req.StoredVoucherDate  where the voucher is NOW   (the SOURCE)
//	req.VoucherDate        where the request puts it  (the DESTINATION)
//
// Refusing on the destination alone lets a voucher be lifted OUT of a locked
// period by restating it with a post-cutoff date. Refusing on the source alone
// lets one be pushed INTO the lock. A DELETE has only a source, and it is the
// whole act. So both ends are judged, and the ORDER is deliberate: the stored
// date is judged FIRST, because when a voucher already sits inside the lock the
// destination the operator typed is usually perfectly legal and a refusal that
// talked about it would be describing the wrong end of the problem.
// effectiveCutoff resolves the two independent controls into the ONE date a
// request is judged against, and reports which of them set it.
//
// ⭐ THE LATER DATE ALWAYS WINS, AND THAT IS THE WHOLE COMPOSITION RULE.
// Neither control may weaken the other: a permissive role cutoff must not open
// a closed year, and a company that has closed nothing must not widen what a
// restricted role may back-date. Taking the later of the two is exactly that
// property, in one line.
//
// ⚠️ THE +1 DAY IS THE INCLUSIVE/EXCLUSIVE CONVERSION AND IT LIVES ONLY HERE.
// books_closed_through is the last CLOSED day; a cutoff is the earliest
// WRITEABLE day. Closing FY 2026-27 stores 2027-03-31 and must refuse writes ON
// 2027-03-31 while permitting 2027-04-01.
func effectiveCutoff(level Level) (cut *time.Time, viaYearEnd bool) {
	cut = level.BackdatedCutoff
	if level.BooksClosedThrough != nil {
		day := level.BooksClosedThrough.AddDate(0, 0, 1)
		if cut == nil || day.After(*cut) {
			return &day, true
		}
	}
	return cut, false
}

// lockPhrase names WHICH control is refusing.
//
// ⛔ IT IS NOT COSMETIC. The two refusals demand completely different actions
// from the operator: a role limit is something an administrator can widen, and
// a closed year is a statutory fact nobody should widen casually. A refusal
// that blurs them sends people to ask for permissions they should not be given.
// D93 and D112 are the same rule applied elsewhere.
//
// ⚠️ THE PER-LEVEL WORDING IS BYTE-FOR-BYTE UNCHANGED. Three existing suites
// read those sentences (auth_backdated_spelling_test.go,
// auth_payroll_cutoff_test.go, authz/cutoff_reason_test.go); only the year-end
// arm is new, and its shape is chosen to read correctly in all three of the
// sentence positions below.
func lockPhrase(level Level, cut *time.Time, viaYearEnd bool) string {
	if viaYearEnd {
		return "the year-end lock (books closed through " +
			level.BooksClosedThrough.UTC().Format("2006-01-02") + ")"
	}
	return "the back-dated cutoff of " + cut.UTC().Format("2006-01-02")
}

// HasCutoff reports whether ANY period lock is in force — the level's own
// back-dated cutoff, or the company's year-end lock, or both.
//
// ⛔⛔ IT EXISTS FOR ONE CALLER AND THAT CALLER IS A SECURITY CONTROL. The
// middleware reads the date a voucher ALREADY carries only when a lock could
// act on it, because a store read the PDP does not need must not happen on the
// hot path. That gate was written when `level.BackdatedCutoff != nil` was the
// only way a period could be locked, and a company-wide year-end is the second
// way — a company that HAS locked a period while every level's own cutoff is
// still nil.
//
// Gating on the field instead of on this method skips the stored-date read at
// exactly those companies, and a DELETE (which carries no body and therefore no
// date at all) then walks straight out of a closed year with a 200. That was
// measured on the wire, not imagined; see
// TestYearEndLockRefusesADeleteOutOfAClosedYear.
//
// ⚠️ ANY FUTURE THIRD SOURCE OF A LOCK MUST BE ADDED HERE, not at the call
// site. That is the whole reason this is a method and not an expression.
func (l Level) HasCutoff() bool {
	cut, _ := effectiveCutoff(l)
	return cut != nil
}

func cutoffDeny(level Level, req Request) (Decision, bool) {
	cut, viaYearEnd := effectiveCutoff(level)
	if cut == nil || !req.Action.IsWrite() {
		return Decision{}, false
	}
	phrase := lockPhrase(level, cut, viaYearEnd)
	if d, blocked := storedCutoffDeny(req, cut, phrase); blocked {
		return d, true
	}
	return requestedCutoffDeny(req, cut, phrase)
}

// storedCutoffDeny judges the SOURCE end of the move: the date the named
// document already carries in the books. nil means the request names no stored
// dated document (an ordinary create), so there is nothing here to judge and the
// destination check below is the whole control — that is the pre-round-3
// behaviour, unchanged, for every create in the product.
//
// ⚠️ THE ZERO-TIME SENTINEL MEANS SOMETHING SHARPER HERE THAN IT DOES FOR THE
// DESTINATION. The middleware hands it over when a voucher this write NAMES
// could not be read at all — it is missing, tombstoned, or the store errored.
// That is the fail-closed direction and it is not optional: "I could not find
// out which period this voucher is in" must never be spelled the same way as
// "this voucher is not in a locked period", because the second is what lets the
// delete through. It costs a 403 (rather than the handler's 404) for an operator
// who names a non-existent voucher at a company that HAS locked a period, which
// is a small price and leaks nothing they could not already see.
func storedCutoffDeny(req Request, cut *time.Time, phrase string) (Decision, bool) {
	if req.StoredVoucherDate == nil {
		return Decision{}, false
	}
	if req.StoredVoucherDate.IsZero() {
		// Matched by IDENTITY, for the reason the destination branch below
		// records: parseCutoff puts no lower bound on a cutoff, so a level whose
		// cutoff IS 0001-01-01 would make `zero.Before(cutoff)` false and the
		// fail-closed sentinel would fail OPEN at exactly that one setting.
		if req.DateSubject != "" {
			return deny("the stored " + req.DateSubject + " could not be read, so " +
				phrase + " cannot be cleared"), true
		}
		return deny("the stored date of the voucher being changed could not be read, so " +
			phrase + " cannot be cleared"), true
	}
	if req.StoredVoucherDate.Before(*cut) {
		// Name the date the voucher is ON. The operator's own field is fine; what
		// they need to be told is that the voucher they picked lives inside a
		// filed period, and where that period starts.
		stored := req.StoredVoucherDate.UTC().Format("2006-01-02")
		if req.DateSubject != "" {
			return deny("this " + req.DateSubject + " of " + stored +
				" is inside the period closed by " + phrase), true
		}
		return deny("this voucher is dated " + stored +
			", inside the period closed by " + phrase), true
	}
	return Decision{}, false
}

// requestedCutoffDeny judges the DESTINATION end: the date the request itself
// carries. This is rounds 1 and 2's control, and its two sentences are
// UNCHANGED — auth_backdated_spelling_test.go, auth_payroll_cutoff_test.go and
// authz/cutoff_reason_test.go all read them.
func requestedCutoffDeny(req Request, cut *time.Time, phrase string) (Decision, bool) {
	if req.VoucherDate == nil {
		return Decision{}, false
	}
	if req.VoucherDate.IsZero() {
		// The zero-time sentinel: a date was required and could not be read (a
		// loosely-spelled voucher_date, an unparseable wage month). Saying "your
		// voucher date is before the cutoff" here would be a LIE — the date the
		// caller sent might well be after it — and it would send them hunting for
		// a period lock instead of fixing the spelling. Refusing is still the only
		// safe answer, so the refusal says which of the two things happened.
		//
		// It is matched by IDENTITY (IsZero) and not left to the Before() below on
		// purpose. parseCutoff puts no lower bound on a cutoff, so a level whose
		// cutoff IS 0001-01-01 would make `zero.Before(cutoff)` false and the
		// fail-closed sentinel would fail OPEN at exactly that one setting.
		if req.DateSubject != "" {
			return deny("the " + req.DateSubject + " could not be read, so " +
				phrase + " cannot be cleared"), true
		}
		return deny("the voucher date could not be read, so " +
			phrase + " cannot be cleared"), true
	}
	if req.VoucherDate.Before(*cut) {
		if req.DateSubject != "" {
			return deny(req.DateSubject + " is before " + phrase), true
		}
		return deny("voucher date is before " + phrase), true
	}
	return Decision{}, false
}

// webauthnDeny applies the WebAuthn step-up requirement: a write under a level
// that requires WebAuthn, by a caller without a WebAuthn AMR, is denied with the
// STEPUP_REQUIRED contract. Returns (decision, true) when blocked.
func webauthnDeny(level Level, req Request) (Decision, bool) {
	if level.RequiresWebAuthn && req.Action.IsWrite() && !req.HasWebAuthn {
		return stepUp(), true
	}
	return Decision{}, false
}
