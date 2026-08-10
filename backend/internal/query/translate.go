// Package query is the SAFE, read-only conversational query core (Phase 3
// flagship). The pipeline is: natural language -> a CLOSED typed DSL (validated
// against a per-entity whitelist) -> a deterministic compiler that emits a
// FIXED, schema-driven FROM/JOIN skeleton with ONLY whitelisted identifiers and
// EVERY user value as a $N parameter -> execution in a READ-ONLY,
// company-GUC-scoped transaction (RLS auto-scopes the tenant; the read-only tx
// is defense-in-depth against any write). The LLM produces ONLY the DSL, never
// SQL; the DSL->SQL compiler is deterministic and hermetically testable.
//
// This file holds the Translator (NL -> Query), the FakeTranslator
// (deterministic, shipped-by-default), and the query.Service the server's
// POST /query handler drives. The typed DSL + compiler + executor live beside it
// (dsl.go, whitelist.go, compile.go, execute.go); this file consumes those
// symbols (Query, Compile, Execute, Result, ColumnMeta, ReadOnlyRunner) by their
// pinned signatures and can only ever emit whitelisted identifiers, because
// every Query it produces is re-validated by Compile.
package query

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrUntranslatable is the typed error the translator returns when it cannot map
// a natural-language question to a Query. The endpoint maps it to a 422 with a
// helpful message and the list of example questions (ExampleQuestions). It is
// distinct from a compile error (an invalid DSL) so the handler can word each.
var ErrUntranslatable = errors.New("query: could not understand the question")

// ErrCrossTenantScope is returned when the question tries to SELECT A SCOPE —
// another company, every company, all tenants, or a bare UUID identifying one.
//
// ⛔ THIS IS A DISCLOSURE FIX, NOT A LEAK FIX, AND THE DIFFERENCE MATTERS.
// The compiled SQL carries no company predicate at all: tenant scoping rests
// entirely on FORCE ROW LEVEL SECURITY plus the company GUC set by
// RunInReadOnlyTx, and query_security_test.go's TestSecurity_CrossTenantIsolation
// proves a forged widening returns zero foreign rows. Nothing here weakens or
// replaces that, and nothing here may ever be relied on for isolation.
//
// What was broken is what the OPERATOR was told. A question naming another
// company returned 200 with the CALLER'S OWN table and no statement anywhere
// that the named company had been ignored — an answer that looks like an answer
// to the question that was asked, and is not. Refusing is the only honest
// response the translator can give, because it cannot answer for a scope it
// cannot reach.
//
// It WRAPS ErrUntranslatable deliberately: every existing caller tests
// errors.Is(err, ErrUntranslatable) (the server maps that to a 422 with the
// example questions), so this refusal reaches the operator through the path that
// already exists rather than needing a new arm in a handler this package does not
// own. QueryScreen states the scope of every answer unconditionally, which is the
// half of the disclosure that cannot drift out of step with this list.
var ErrCrossTenantScope = fmt.Errorf(
	"query: a question cannot select another company's books; every answer is scoped to the signed-in company: %w",
	ErrUntranslatable)

// ExampleQuestions is the canonical set of questions the FakeTranslator
// understands. The 422 response echoes these so a user learns what to ask. The
// real-Claude adapter (documented below) is not limited to this list, but the
// deterministic fake — the tested path — is.
var ExampleQuestions = []string{
	"trial balance",
	"balances by group",
	"closing balances",
	"sales this month",
	"sales last month",
	"sales this quarter",
	"sales last quarter",
	"sales this year",
	"total purchases",
	"top debtors",
	"payables",
}

// Translator turns a natural-language question into a typed Query. The
// production implementation is an Anthropic Claude adapter
// (client.messages.parse / tool-use against the Query JSON schema, lazy, behind
// AUTH/ANTHROPIC keys); this package NEVER calls the network. The tested,
// shipped-by-default implementation is FakeTranslator — a deterministic,
// rule/keyword-based mapper. companyID is passed so an adapter can resolve the
// company financial-year start (and other per-company context); the fake uses
// it only to scope nothing (RLS does the scoping at execute time).
type Translator interface {
	ToQuery(ctx context.Context, companyID uuid.UUID, nl string) (Query, error)
}

// FakeTranslator is the deterministic translator. It is hermetic: "this
// month/quarter/year" date ranges derive from Now (an injected clock) and the
// company financial-year start FYStartMonth — NEVER from a hidden time.Now()
// inside ToQuery — so translate_test.go is reproducible. "this year" means the
// Indian financial year (FY starting FYStartMonth, default April=4), which is
// the correct default for Indian accounting.
//
// A real-Claude swap (production) keeps the same Translator interface:
//
//	type ClaudeTranslator struct { client *anthropic.Client; schema ... }
//	func (c *ClaudeTranslator) ToQuery(ctx, companyID, nl) (Query, error) {
//	    // client.Messages.New(... tool: emit_query(Query JSON schema) ...)
//	    // parse the single tool_use block into a Query; on no match -> ErrUntranslatable.
//	}
//
// It is lazy (built only when the key is present) and the network call lives
// entirely there; the compiler/executor downstream are identical, so the safety
// model (whitelist + $N params + read-only tx + RLS) is unchanged regardless of
// which translator produced the Query.
type FakeTranslator struct {
	// Now is the injected clock. Tests set it to a fixed instant; production wires
	// time.Now. It MUST be non-nil for time-relative questions; a nil Now falls
	// back to time.Now() but that path is never exercised by the tests.
	Now func() time.Time
	// FYStartMonth is the company financial-year start month (1-12). Zero means
	// the Indian default of April (4).
	FYStartMonth int
}

// now returns the injected clock instant, or time.Now() when Now is nil (the
// production default-wiring convenience; tests always inject a fixed clock).
func (f FakeTranslator) now() time.Time {
	if f.Now != nil {
		return f.Now()
	}
	return time.Now()
}

func (f FakeTranslator) fyStartMonth() time.Month {
	if f.FYStartMonth >= 1 && f.FYStartMonth <= 12 {
		return time.Month(f.FYStartMonth)
	}
	return time.April
}

// ToQuery maps a handful of CANONICAL questions to a Query. Matching is on the
// normalised (lower-cased, trimmed) text; an unrecognised question returns
// ErrUntranslatable. Every produced Query references ONLY whitelisted
// entities/columns — it is itself re-validated by Compile, so a translator bug
// cannot widen the safety boundary.
func (f FakeTranslator) ToQuery(_ context.Context, _ uuid.UUID, nl string) (Query, error) {
	norm := strings.ToLower(strings.TrimSpace(nl))

	// ⛔ SCOPE FIRST, BEFORE ANY BUSINESS CASE. "closing balances for all
	// companies" contains "closing" and would otherwise be answered — for one
	// company — under a question that asked for every one of them. See
	// ErrCrossTenantScope for why this refuses rather than answers.
	if namesAnotherScope(norm) {
		return Query{}, ErrCrossTenantScope
	}

	switch {
	case norm == "":
		return Query{}, ErrUntranslatable

	// Closing balances -> the OPENING-FOLDED ledger position (closing_net),
	// which reconciles with the Trial Balance report per ledger. Matched BEFORE
	// the trial-balance case so a phrasing like "trial balance closing" resolves
	// to closing_net rather than the movement-only debit/credit columns.
	case contains(norm, "closing"):
		return Query{
			Entity:     "ledger_balances",
			Metrics:    []Metric{{Func: "sum", Column: "closing_net", As: "closing"}},
			Dimensions: []string{"ledger_name"},
			OrderBy:    []OrderTerm{{Column: "ledger_name", Desc: false}},
			Limit:      100,
		}, nil

	// Trial balance / balances grouped by account group.
	case contains(norm, "trial balance") || contains(norm, "balances by group") || norm == "balances":
		return Query{
			Entity:     "ledger_balances",
			Metrics:    []Metric{{Func: "sum", Column: "debit", As: "debit"}, {Func: "sum", Column: "credit", As: "credit"}},
			Dimensions: []string{"group_name"},
			OrderBy:    []OrderTerm{{Column: "group_name", Desc: false}},
			Limit:      100,
		}, nil

	// ------------------------------------------------------------------
	// Sales / purchases: THE P&L's OWN FIGURE, not the invoice register's.
	// ------------------------------------------------------------------
	//
	// ⛔ THIS USED TO BE sum(vv.taxable_value) OVER voucher_type_name='Sales',
	// AND THAT FIGURE MATCHED NO REPORT IN THE PRODUCT. Driven: 1,56,500.00
	// answered against a Sales-ledger position of 1,44,500.00 and an invoice
	// total of 1,83,080.00. It was wrong in BOTH directions at once —
	//
	//   * it EXCLUDED the GST on every invoice (so it was not what the customer
	//     was billed), and
	//   * it EXCLUDED credit notes entirely (a sales return is a voucher of a
	//     DIFFERENT type, so a 12,000.00 return never reduced it),
	//
	// and it depended on vv.taxable_value being stamped at all, which only
	// happens when GST derivation ran. On a non-GST book every one of those
	// columns is NULL and the old query answered 0.0000 for a trading company.
	//
	// The replacement is defined the way reporting defines it, not merely so it
	// agrees today: sum the POSTED MOVEMENT on the ledgers that roll up into the
	// P&L's "Sales Accounts" root group, signed to each ledger's natural side.
	// That is character-for-character the rule in reporting.ledgerBalance.signed()
	// plus the P&L's root-group rollup, so the answer equals the P&L's Sales line
	// BY CONSTRUCTION — with or without GST, netting credit notes automatically
	// (a credit note DEBITS Sales), and excluding GST automatically (output tax
	// posts to Duties & Taxes, a different group).
	//
	// The screen states this basis above every answer; see QueryScreen's
	// measureBasis. A figure this size may never be shown without saying what it
	// is — that was half of the driven defect.
	case contains(norm, "sales"):
		dr, err := f.relativeRange(norm)
		if err != nil {
			return Query{}, err
		}
		return Query{
			Entity:     "postings",
			Metrics:    []Metric{{Func: "sum", Column: "natural_net", As: "net_sales"}},
			Dimensions: []string{"month"},
			Filters:    []Filter{{Column: "pl_group", Op: "eq", Values: []string{"Sales Accounts"}}},
			DateRange:  dr,
			OrderBy:    []OrderTerm{{Column: "month", Desc: false}},
			Limit:      100,
		}, nil

	// Purchases — the exact mirror, and the same defect: the old form excluded
	// GST and excluded DEBIT notes (a purchase return).
	case contains(norm, "purchase"):
		dr, err := f.relativeRange(norm)
		if err != nil {
			return Query{}, err
		}
		return Query{
			Entity:     "postings",
			Metrics:    []Metric{{Func: "sum", Column: "natural_net", As: "net_purchases"}},
			Dimensions: []string{"month"},
			Filters:    []Filter{{Column: "pl_group", Op: "eq", Values: []string{"Purchase Accounts"}}},
			DateRange:  dr,
			OrderBy:    []OrderTerm{{Column: "month", Desc: false}},
			Limit:      100,
		}, nil

	// Receivables / top debtors -> outstandings, ASSET orientation.
	case contains(norm, "debtor") || contains(norm, "receivable"):
		if err := refuseIfPeriodScoped(f, norm); err != nil {
			return Query{}, err
		}
		return Query{
			Entity:     "outstandings",
			Metrics:    []Metric{{Func: "sum", Column: "pending", As: "pending"}},
			Dimensions: []string{"ledger_name"},
			Filters:    []Filter{{Column: "nature", Op: "eq", Values: []string{"ASSET"}}},
			OrderBy:    []OrderTerm{{Column: "pending", Desc: true}},
			Limit:      100,
		}, nil

	// Payables / creditors -> outstandings, LIABILITY orientation.
	case contains(norm, "payable") || contains(norm, "creditor"):
		if err := refuseIfPeriodScoped(f, norm); err != nil {
			return Query{}, err
		}
		return Query{
			Entity:     "outstandings",
			Metrics:    []Metric{{Func: "sum", Column: "pending", As: "pending"}},
			Dimensions: []string{"ledger_name"},
			Filters:    []Filter{{Column: "nature", Op: "eq", Values: []string{"LIABILITY"}}},
			OrderBy:    []OrderTerm{{Column: "pending", Desc: true}},
			Limit:      100,
		}, nil
	}
	return Query{}, ErrUntranslatable
}

// refuseIfPeriodScoped rejects an outstandings question that names a period.
//
// ⛔ WHY A REFUSAL AND NOT A SHRUG. The outstandings entity is an AS-AT position
// (see whitelist.go's skeleton: the reference universe is bounded by today, and
// the opening bill-wise breakup is folded in only when today is on or after the
// books' beginning). It has no notion of "during last quarter". Before this, a
// period word on a receivables question was simply DROPPED and the caller was
// shown today's position under their own question about March — the identical
// failure to the one D21 records for sales, on a surface where nobody thought to
// look. Refusing is the only answer that is not a wrong number.
func refuseIfPeriodScoped(f FakeTranslator, norm string) error {
	dr, err := f.relativeRange(norm)
	if err != nil {
		return err
	}
	if dr != nil {
		return ErrUntranslatable
	}
	return nil
}

// --------------------------------------------------------------------------
// The period parser (D21). READ THIS BEFORE CHANGING relativeRange.
// --------------------------------------------------------------------------
//
// ⛔ WHAT THIS REPLACES, AND WHY A `switch contains(...)` CANNOT BE MADE SAFE.
// The previous form was three ordered substring tests:
//
//	case contains(norm,"this month")   || contains(norm,"month"):   ...
//	case contains(norm,"this quarter") || contains(norm,"quarter"): ...
//	case contains(norm,"this year")    || contains(norm,"year"):    ...
//
// and it silently answered for the WRONG WINDOW in three separate ways at once:
//
//  1. THERE WAS NO PREVIOUS-PERIOD BRANCH AT ALL. "sales last month" contains
//     the substring "month", so it fell into arm 1 and returned the 1st..last of
//     TODAY's month. Driven in a browser: "sales last quarter" answered "No
//     rows." for a quarter that in fact held 1,56,500.00, and nothing on screen
//     said which window had been used — the DSL that would have shown it sits
//     behind a collapsed-by-default panel.
//
//  2. ARM ORDER BEAT MEANING. Any question containing "month" ANYWHERE won,
//     even when the period the user named was a year: "sales this year by month"
//     asked for the FY grouped by month and was answered for the current month.
//
//  3. SUBSTRINGS ARE NOT WORDS. "quarter" is a substring of "headquarters", so
//     "sales at headquarters" quietly acquired a quarter window.
//
// The replacement matches WHOLE WORDS on a tokenised question, and binds the
// window to the period word that an explicit qualifier (this/current vs
// last/previous/prior/preceding) actually governs. Three rules carry the safety:
//
//   - A BARE period word with no qualifier yields NO window ("sales by month",
//     "monthly sales" = every date, grouped by month). Narrowing an unqualified
//     question to the current period is the same silent-window defect wearing a
//     different hat.
//   - A COUNT between the qualifier and the unit ("last 6 months", "past 3
//     years") is REFUSED, not rounded down to one. Answering one month for six
//     is D21 exactly.
//   - TWO DIFFERENT QUALIFIED WINDOWS ("last month and this year") are REFUSED,
//     because there is no defensible way to pick one.
//
// Refusals surface as ErrUntranslatable -> 422 with the example list, which is a
// visible non-answer. A wrong window is an invisible wrong answer. Prefer the
// refusal every time.

// periodUnit is the calendar/financial window a question named.
type periodUnit string

const (
	unitNone    periodUnit = ""
	unitMonth   periodUnit = "month"
	unitQuarter periodUnit = "quarter"
	unitYear    periodUnit = "year"
)

// periodWords maps a WHOLE WORD to its unit. Plurals and the -ly adverbs are
// listed explicitly rather than matched by prefix, so "headquarters" (which
// CONTAINS "quarter") and "yearn" cannot bind a window. "fy"/"annum" are the
// Indian-accounting spellings of the financial year.
var periodWords = map[string]periodUnit{
	"month": unitMonth, "months": unitMonth, "monthly": unitMonth,
	"quarter": unitQuarter, "quarters": unitQuarter, "quarterly": unitQuarter,
	"year": unitYear, "years": unitYear, "yearly": unitYear,
	"fy": unitYear, "annum": unitYear, "annually": unitYear,
}

// currentQualifiers and previousQualifiers are the CLOSED sets of words that
// select which instance of the unit is meant. A unit with neither is UNQUALIFIED
// and yields no window (see the header).
//
// "past" is deliberately absent from both: "the past year" idiomatically means
// the trailing twelve months, not the previous financial year, and guessing
// between them is how this defect was born. It falls through to "no window",
// which is disclosed on screen as "all dates".
var currentQualifiers = map[string]bool{
	"this": true, "current": true, "present": true, "ongoing": true,
}
var previousQualifiers = map[string]bool{
	"last": true, "previous": true, "prior": true, "preceding": true, "prev": true,
}

// periodFillers may sit BETWEEN a qualifier and its unit without breaking the
// binding: "last financial year", "the previous fiscal quarter".
var periodFillers = map[string]bool{
	"the": true, "financial": true, "fiscal": true, "calendar": true,
	"full": true, "whole": true, "complete": true, "entire": true,
}

// maxQualifierGap is how far past a qualifier the parser will look for its unit.
// Three filler words is generous for real phrasing ("the last full financial
// year") and short enough that a qualifier cannot reach across a clause and
// capture an unrelated period word.
const maxQualifierGap = 4

// relativeRange derives an inclusive DateRange on voucher_date from the injected
// clock for a qualified period phrase. "year" is the FINANCIAL year
// (FYStartMonth); "quarter" is the CALENDAR quarter, matching the vouchers/
// postings `quarter` dimension in whitelist.go.
//
// It returns (nil, nil) when the question named no period — the caller then
// emits a Query with NO date filter, i.e. every date, which QueryScreen
// discloses as "all dates". It returns an error only for a phrase it can see is
// a period request it cannot honour exactly (see the header).
func (f FakeTranslator) relativeRange(norm string) (*DateRange, error) {
	unit, previous, err := parsePeriod(norm)
	if err != nil {
		return nil, err
	}
	if unit == unitNone {
		return nil, nil
	}
	now := f.now()
	var from, to time.Time
	switch unit {
	case unitMonth:
		// Shift from the month START, never from `now`: AddDate(0,-1,0) on the
		// 31st of March normalises "31 February" forward into March and would
		// silently return a window in the wrong month.
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		if previous {
			from = from.AddDate(0, -1, 0)
		}
		to = from.AddDate(0, 1, -1)
	case unitQuarter:
		from, to = quarterRange(now)
		if previous {
			from = from.AddDate(0, -3, 0)
			to = from.AddDate(0, 3, -1)
		}
	case unitYear:
		from, to = fyRange(now, f.fyStartMonth())
		if previous {
			from = from.AddDate(-1, 0, 0)
			to = from.AddDate(1, 0, -1)
		}
	}
	return &DateRange{Column: "voucher_date", From: iso(from), To: iso(to)}, nil
}

// parsePeriod tokenises the question and returns the single QUALIFIED window it
// names, if any. See relativeRange's header for the three safety rules; this is
// where they are enforced.
func parsePeriod(norm string) (periodUnit, bool, error) {
	toks := words(norm)

	unit := unitNone
	previous := false
	found := false

	for i, tok := range toks {
		isCurrent := currentQualifiers[tok]
		isPrevious := previousQualifiers[tok]
		if !isCurrent && !isPrevious {
			continue
		}
		// Walk forward for the unit this qualifier governs, stepping over
		// fillers. A NUMBER on the way ("last 6 months") means a multi-period
		// window this translator cannot express — refuse rather than answer for
		// one period, which is the whole point of D21.
		for j := i + 1; j < len(toks) && j <= i+maxQualifierGap; j++ {
			t := toks[j]
			if isCount(t) {
				return unitNone, false, ErrUntranslatable
			}
			if u, ok := periodWords[t]; ok {
				if found && (u != unit || isPrevious != previous) {
					// Two different windows named in one question.
					return unitNone, false, ErrUntranslatable
				}
				unit, previous, found = u, isPrevious, true
				break
			}
			if !periodFillers[t] {
				break // an unrelated word: this qualifier governs no period
			}
		}
	}
	return unit, previous, nil
}

// countWords are the spelled-out small numbers a question may put between a
// qualifier and a unit. Digits are caught structurally by isCount.
var countWords = map[string]bool{
	"two": true, "three": true, "four": true, "five": true, "six": true,
	"seven": true, "eight": true, "nine": true, "ten": true, "twelve": true,
	"couple": true, "few": true, "several": true,
}

// isCount reports whether a token is a quantity ("6", "twelve", "few").
func isCount(tok string) bool {
	if countWords[tok] {
		return true
	}
	if tok == "" {
		return false
	}
	for _, ch := range tok {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// words splits a normalised question into lower-case alphanumeric tokens. Every
// other byte is a separator, so punctuation and hyphens do not fuse words —
// "sales, last-quarter?" tokenises to [sales last quarter].
func words(norm string) []string {
	return strings.FieldsFunc(norm, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
}

// --------------------------------------------------------------------------
// Scope recognition (D50)
// --------------------------------------------------------------------------

// scopeNouns are the words that name a SET OF BOOKS rather than anything inside
// one. They are matched as PREFIXES of a whole token so "company"/"companies"
// and "organisation"/"organisations"/"organization" all land, without a prefix
// test ever reaching inside an unrelated word (tokens are compared whole).
var scopeNouns = []string{
	"compan", "tenant", "organisation", "organization", "org", "orgs",
	"firm", "firms", "subsidiar", "business", "businesses", "book", "books",
}

// scopeQuantifiers are the words that turn a scope noun into a request for a
// scope OTHER than the current one. "my"/"our"/"this"/"the" are deliberately
// absent: "my company's total sales" is a perfectly good question about the
// caller's own books and must keep working.
var scopeQuantifiers = map[string]bool{
	"all": true, "every": true, "each": true, "both": true, "any": true,
	"other": true, "another": true, "across": true, "different": true,
	"others": true, "everyone": true, "everybody": true, "someone": true,
	"somebody": true, "anyone": true, "else": true, "second": true,
}

// namesAnotherScope reports whether a question tries to select a scope this
// translator cannot reach: another company, every company, or a company named by
// its UUID.
//
// ⚠️ THIS IS A LIST, AND §7.2 IS RIGHT THAT LISTS GET BYPASSED. It is not the
// guard — it is the part that turns the most explicit attempts into an honest
// refusal instead of a mis-attributed answer. The guard that does not drift is
// on the screen: QueryScreen states the scope of EVERY answer and every refusal
// unconditionally, so a question naming a company by a NAME nobody enumerated is
// still answered under a visible "this is <Company> only" caption rather than
// silently. Do not delete that caption believing this function covers it.
func namesAnotherScope(norm string) bool {
	if containsUUID(norm) {
		return true
	}
	toks := words(norm)
	for i, tok := range toks {
		if !scopeQuantifiers[tok] {
			continue
		}
		// The noun may be one or two words along ("all of the companies",
		// "every other tenant").
		for j := i + 1; j < len(toks) && j <= i+3; j++ {
			if isScopeNoun(toks[j]) {
				return true
			}
		}
	}
	return false
}

// isScopeNoun matches a whole token against the scope-noun prefixes.
func isScopeNoun(tok string) bool {
	for _, n := range scopeNouns {
		if strings.HasPrefix(tok, n) {
			return true
		}
	}
	return false
}

// containsUUID reports whether the question carries a canonical 8-4-4-4-12 hex
// UUID. A raw company id in an English question is never a question about the
// books — it is an attempt to name a tenant — and it is the exact probe the
// audit drove. It is scanned by hand rather than by regexp so this package keeps
// its zero-dependency, network-free leaf shape.
func containsUUID(s string) bool {
	groups := []int{8, 4, 4, 4, 12} // the canonical 8-4-4-4-12 hex shape
	for start := 0; start+36 <= len(s); start++ {
		pos := start
		ok := true
		for gi, n := range groups {
			if gi > 0 {
				if s[pos] != '-' {
					ok = false
					break
				}
				pos++
			}
			for k := 0; k < n; k++ {
				if !isHex(s[pos+k]) {
					ok = false
					break
				}
			}
			if !ok {
				break
			}
			pos += n
		}
		if ok {
			return true
		}
	}
	return false
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// quarterRange returns the calendar quarter containing t (inclusive bounds).
func quarterRange(t time.Time) (time.Time, time.Time) {
	q := (int(t.Month()) - 1) / 3 // 0..3
	startMonth := time.Month(q*3 + 1)
	from := time.Date(t.Year(), startMonth, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 3, -1)
	return from, to
}

// fyRange returns the Indian financial year (starting fyStart month) containing
// t, as inclusive bounds. For an April start: 2025-04-01 .. 2026-03-31.
func fyRange(t time.Time, fyStart time.Month) (time.Time, time.Time) {
	year := t.Year()
	if t.Month() < fyStart {
		year-- // before the FY start month -> the FY began the previous calendar year
	}
	from := time.Date(year, fyStart, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(1, 0, -1)
	return from, to
}

func iso(t time.Time) string { return t.Format("2006-01-02") }

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// --------------------------------------------------------------------------
// Service: the seam the server's POST /query handler drives.
// --------------------------------------------------------------------------

// Service couples a Translator with WS1's Compile+Execute core. The server
// holds one Service; the handler calls Translate for an NL question, then Run /
// Preview to compile+execute or render the SQL preview. The runner is WS1's
// ReadOnlyRunner (satisfied by *db.Pool via RunInReadOnlyTx).
type Service struct {
	runner     ReadOnlyRunner
	translator Translator
}

// NewService wires a query Service over the read-only runner and a translator.
func NewService(runner ReadOnlyRunner, t Translator) *Service {
	return &Service{runner: runner, translator: t}
}

// Translate exposes the underlying translator (NL -> Query). It returns
// ErrUntranslatable for an unrecognised question.
func (s *Service) Translate(ctx context.Context, companyID uuid.UUID, nl string) (Query, error) {
	return s.translator.ToQuery(ctx, companyID, nl)
}

// Run compiles and executes a Query against the read-only, company-scoped
// runner (RLS scopes the tenant). It is the single execution path for both
// NL-derived and DSL-supplied queries. A compile error surfaces as a
// *CompileError (IsCompileError); the handler turns any failure into a 422 with
// a SAFE message — never the raw SQL/internal text.
func (s *Service) Run(ctx context.Context, q Query) (Result, error) {
	return Execute(ctx, s.runner, q)
}

// Preview returns the compiled SQL (placeholders only, never interpolated
// values) for the response's sql_preview field, or a compile error. It wraps
// WS1's SQLPreview (which returns "" on a compile error) by re-running Compile
// to surface that error to the handler.
func (s *Service) Preview(q Query) (string, error) {
	sql, _, err := Compile(q)
	if err != nil {
		return "", err
	}
	return sql, nil
}
