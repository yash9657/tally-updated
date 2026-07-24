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
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrUntranslatable is the typed error the translator returns when it cannot map
// a natural-language question to a Query. The endpoint maps it to a 422 with a
// helpful message and the list of example questions (ExampleQuestions). It is
// distinct from a compile error (an invalid DSL) so the handler can word each.
var ErrUntranslatable = errors.New("query: could not understand the question")

// ExampleQuestions is the canonical set of questions the FakeTranslator
// understands. The 422 response echoes these so a user learns what to ask. The
// real-Claude adapter (documented below) is not limited to this list, but the
// deterministic fake — the tested path — is.
var ExampleQuestions = []string{
	"trial balance",
	"balances by group",
	"closing balances",
	"sales this month",
	"sales this quarter",
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

	// Sales for a relative period -> vouchers filtered to the Sales type.
	case contains(norm, "sales"):
		dr := f.relativeRange(norm)
		return Query{
			Entity:     "vouchers",
			Metrics:    []Metric{{Func: "sum", Column: "taxable_value", As: "total_sales"}},
			Dimensions: []string{"month"},
			Filters:    []Filter{{Column: "voucher_type_name", Op: "eq", Values: []string{"Sales"}}},
			DateRange:  dr,
			OrderBy:    []OrderTerm{{Column: "month", Desc: false}},
			Limit:      100,
		}, nil

	// Purchases.
	case contains(norm, "purchase"):
		dr := f.relativeRange(norm)
		return Query{
			Entity:     "vouchers",
			Metrics:    []Metric{{Func: "sum", Column: "taxable_value", As: "total_purchases"}},
			Dimensions: []string{"month"},
			Filters:    []Filter{{Column: "voucher_type_name", Op: "eq", Values: []string{"Purchase"}}},
			DateRange:  dr,
			OrderBy:    []OrderTerm{{Column: "month", Desc: false}},
			Limit:      100,
		}, nil

	// Receivables / top debtors -> outstandings, ASSET orientation.
	case contains(norm, "debtor") || contains(norm, "receivable"):
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

// relativeRange derives a DateRange on voucher_date from the injected clock for
// "this month / quarter / year" phrasing. "this year" is the FINANCIAL year
// (FYStartMonth). With no recognised period word it returns nil (no date filter
// — all matching vouchers). The returned bounds are inclusive ISO dates.
func (f FakeTranslator) relativeRange(norm string) *DateRange {
	now := f.now()
	switch {
	case contains(norm, "this month") || contains(norm, "month"):
		from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 1, -1)
		return &DateRange{Column: "voucher_date", From: iso(from), To: iso(to)}
	case contains(norm, "this quarter") || contains(norm, "quarter"):
		from, to := quarterRange(now)
		return &DateRange{Column: "voucher_date", From: iso(from), To: iso(to)}
	case contains(norm, "this year") || contains(norm, "year"):
		from, to := fyRange(now, f.fyStartMonth())
		return &DateRange{Column: "voucher_date", From: iso(from), To: iso(to)}
	}
	return nil
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
