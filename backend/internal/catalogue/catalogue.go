// Package catalogue is the SEMANTIC CATALOGUE (PLAN-AGENTIC W3): every report
// in the authz vocabulary, and every entity in the query DSL, described in the
// words a business owner uses — English, Hindi-in-Latin-script and the mix of
// the two that most of them actually type — so a model (or the deterministic
// selector below) can route "Kaveri ka kitna baki hai?" to the outstandings
// report and an outstandings DSL instead of guessing.
//
// ⛔ WHAT THIS PACKAGE IS NOT: AN AUTHORITY. Nothing here grants, widens or
// describes a permission. The ACL key of a report is authz's, the route is the
// generated actions registry's, and the DSL is re-validated by query.Compile on
// every use — an example DSL that stopped compiling fails the completeness test
// rather than reaching a prompt. The catalogue is a HINT layer, exactly as
// query.Catalog() is for the translator (query/catalog.go's SECURITY NOTE): a
// hallucinated route or column simply 404s or 422s downstream.
//
// ⛔ WHY IT IS KEYED BY THE VOCABULARY AND NOT BY A LIST OF ITS OWN. A
// catalogue that enumerated "the reports" by hand would be one more list to
// drift, and the first report added after it was written would be invisible to
// the agent — present in the product, absent from every answer, with nothing
// failing. completeness_test.go walks authz.KnownReportNames and fails on a
// report with no entry (and on an entry for a report that does not exist), so
// adding a report to the vocabulary without describing it here is a red build.
//
// Why it lives outside internal/query: the catalogue needs authz (the report
// names) and actions (the display route), and internal/query's allowed set is
// {platform/db} — the SQL compiler must not grow a dependency on the auth
// stack or anything that could one day reach the network (archtest's
// TestQueryPackageIsNetworkFree does not follow transitive imports; the
// allowedDeps line is the invariant).
package catalogue

import (
	"github.com/khata/cored/internal/actions"
	"github.com/khata/cored/internal/authz"
	"github.com/khata/cored/internal/query"
)

// Entry is one report's agent metadata.
type Entry struct {
	// Report is the authz report NAME (the `report:NAME` token).
	Report string `json:"report"`
	Title  string `json:"title"`
	// Description says what the report answers AND what it does not — the
	// second half is what stops a model answering a stock-value question from
	// a movement register.
	Description string `json:"description"`
	// Synonyms are English phrasings, incl. Tally's own menu words.
	Synonyms []string `json:"synonyms"`
	// Hinglish are Hindi / Hinglish phrasings in LATIN script — the way the
	// question arrives on a phone keyboard. Devanagari is owed (the W1a
	// Hinglish embedding eval decides whether the selector needs it or the
	// embedding model covers transliteration).
	Hinglish []string `json:"hinglish"`
	// Questions are typical questions this report answers. At least one is
	// Hinglish. They double as the selector's self-retrieval gate.
	Questions []string `json:"questions"`
	// DSL are example queries that answer (a slice of) the report from the
	// typed DSL. Every one must compile. Empty when the DSL cannot express the
	// report, and then NoDSLReason must say why.
	DSL []query.Query `json:"dsl,omitempty"`
	// DSLFidelity is "exact" when an example is pinned equal to the report by
	// a parity test, "partial" when it answers a named slice of it (stated in
	// the description), "" when there is no DSL.
	DSLFidelity string `json:"dsl_fidelity,omitempty"`
	// ParityTest names the test function that pins an "exact" example equal
	// to the report. The completeness test finds it in the source tree, so an
	// "exact" claim with no test behind it — or one whose test was renamed
	// away — is a red build rather than a label a model trusts.
	ParityTest string `json:"parity_test,omitempty"`
	NoDSLReason string `json:"no_dsl_reason,omitempty"`
	// Sensitive marks reports carrying personal identifiers (PAN, UAN, salary)
	// that migration 0035 disallows by default. The agent may still read them
	// under a principal the tenant granted them to; a narrative must not quote
	// them to a channel the tenant did not (W6's concern, flagged here).
	Sensitive bool `json:"sensitive,omitempty"`
}

// DisplayActions returns the generated ActionSpecs that DISPLAY this report —
// the route the agent actually calls. Resolved from the registry rather than
// stored, so the catalogue cannot carry a stale route.
func (e Entry) DisplayActions() []actions.ActionSpec {
	return actions.ByACLKey(actions.RenderACLKey(authz.Display, "report", e.Report))
}

// EntityEntry describes one query-DSL entity for the selector's second hop
// (question -> reports -> ENTITIES -> DSL).
type EntityEntry struct {
	Entity      query.Entity `json:"entity"`
	Description string       `json:"description"`
	Synonyms    []string     `json:"synonyms"`
	Hinglish    []string     `json:"hinglish"`
}

// Reports returns every entry, in authz.KnownReportNames order.
func Reports() []Entry {
	out := make([]Entry, 0, len(authz.KnownReportNames))
	for _, n := range authz.KnownReportNames {
		if e, ok := byReport[n]; ok {
			out = append(out, e)
		}
	}
	return out
}

// Lookup returns the entry for a report name.
func Lookup(report string) (Entry, bool) {
	e, ok := byReport[report]
	return e, ok
}

// Entities returns the entity entries, in query.Catalog() order.
func Entities() []EntityEntry {
	var out []EntityEntry
	for _, d := range query.Catalog() {
		if e, ok := byEntity[query.Entity(d.Entity)]; ok {
			out = append(out, e)
		}
	}
	return out
}

var byReport = func() map[string]Entry {
	m := make(map[string]Entry, len(reportEntries))
	for _, e := range reportEntries {
		m[e.Report] = e
	}
	return m
}()

var byEntity = func() map[query.Entity]EntityEntry {
	m := make(map[query.Entity]EntityEntry, len(entityEntries))
	for _, e := range entityEntries {
		m[e.Entity] = e
	}
	return m
}()
