package server

// handler_gstr3b.go holds the GET /v1/companies/{companyID}/gstr3b handler.
//
// WIRING (the two-touch rule, IMPLEMENTATION_PLAN.md sec 1.5): the route IS
// MOUNTED, and it took BOTH touches —
//
//	(a) r.Get("/gstr3b", s.handleGSTR3B)  in server.go routes(), next to the
//	    existing r.Get("/gstr1", s.handleGSTR1), and
//	(b) case "/gstr3b": return "gstr3b"   in reportName() in auth_middleware.go.
//
// Keep both if this handler is ever renamed or moved: with only (a), even an
// Owner gets a 403, because routeAction default-denies unmapped {companyID}
// sub-paths.
//
// Response shape mirrors handleGSTR1: the bare return when no tax-ledger ids are
// supplied, or {"gstr3b": ..., "reconciliation_ok": bool} when any are, so the
// caller can see whether the return ties to the posted tax to the paisa.
//
// ---------------------------------------------------------------------------
// WHAT TABLE 4 NOW IS, AND WHAT IT STILL IS NOT
// ---------------------------------------------------------------------------
// ITC IS NO LONGER REPORTED ALL-ELIGIBLE. That statement stood here while table
// 4 had no reconciliation behind it; it is now false, and leaving it would be
// worse than the original gap — a comment claiming the software knows LESS than
// it does invites a reader to add a second, conflicting eligibility model.
//
// There are two modes and the query decides which:
//
//   - `statement_id` ABSENT (the default). The RETURN ITSELF is unchanged: no
//     ITCSource is passed, every posted input rupee is reported available in
//     4(A) WITH NO ELIGIBILITY CHECK OF ANY KIND, and nothing is reversed. The
//     `disclosures` field then carries SEVEN tokens, not two — three are
//     unconditional and four follow from having no statement:
//
//     4d1-reclaim-not-modelled, cess-not-modelled,
//     itc-eligibility-not-classified, no-inward-statement, not-filing-ready,
//     portal-schema-unverified, rule-42-43-not-computed
//
//     A frontend must switch on ALL SEVEN. Recognising only some of them and
//     ignoring the rest is the closed-vocabulary failure the field exists to
//     prevent — see the comment above the const block in
//     compliance/gstr/gstr3b.go. `itc-eligibility-not-classified` is the one
//     that must never be dropped: it is the return saying every input rupee in
//     4(A) went in unchecked.
//
//     TWO TESTS PIN THIS PATH, AND NEITHER IS CALLED
//     TestGSTR3BWithoutStatementIDIsUnchanged (a name this comment used to give,
//     which never existed):
//     - TestGSTR3BWithoutStatementIDIsTheBareReturn, handler_itc_test.go, is the
//     HANDLER-level pin: the response body must equal Generate3B with a zero
//     Options3B, byte for byte.
//     - TestGSTR3BWithoutStatementIDCarriesTheWholeDisclosureSet,
//     handler_gstr3b_test.go, pins the seven tokens above as a SET, so adding
//     or losing one has to be a deliberate edit here.
//     - TestBuild3B_NilITCSourceIsByteIdentical, in package gstr
//     (gstr3b_table4_test.go), pins the same property one layer down at
//     build3B and does not exercise this handler at all.
//
//     ONE THING ON THIS PATH IS NOT "UNCHANGED": `as_on` is parsed
//     UNCONDITIONALLY (see resolveAsOn below), so an unparseable `as_on` is a
//     400 even with no statement_id. That is deliberate — silently ignoring a
//     malformed instant answers a question nobody asked — and
//     TestGSTR3BRefusesAMalformedAsOnEvenWithoutAStatement pins it.
//
//   - `statement_id` SUPPLIED. compliance/itc resolves the per-voucher ITC
//     decisions for the period from the ingested GSTR-2A/2B: the books-vs-2B
//     match, the IMS state as at `as_on`, the s.16(4) time bar and the s.17(5)
//     blocked-credit classification. 4(A) then carries only what survives, and
//     4(B)(2) carries the real reversals. The reconciliation's OWN disclosures
//     are forwarded VERBATIM into the response — including
//     `sec-16-6-revocation-not-modelled` and, when they apply,
//     `statement-partially-parsed`, `statement-credit-note-not-in-books` and
//     `reversals-not-posted-to-books`.
//
// IT IS NOT FILING-READY IN EITHER MODE, and every response says so — the
// `disclosures` field always contains `not-filing-ready`, because
// `portal-schema-unverified` is unconditional. The open gaps, in the words of
// the tokens that name them:
//
//	itc-eligibility-not-classified      At least one purchase carried NO ITC
//	  decision and was reported fully available in 4(A) by default. With no
//	  statement_id that is EVERY purchase: nothing has been checked against
//	  s.16(4), s.17(5) or what the supplier actually filed.
//	rule-42-43-not-computed             The rule 42/43 apportionment is computed
//	  ONLY from CALLER-SUPPLIED inputs (exempt_turnover, total_turnover and the
//	  head-wise common credit). Nothing here derives them: the books carry no
//	  export/SEZ marker, so an engine that substituted its own E would return
//	  D1 = 0 for every taxpayer and look like it worked.
//	statement-credit-note-not-in-books  The PERIOD-LEVEL credit-note adjustment
//	  is DISCLOSED, NOT CORRECTED: a supplier credit note in the 2B that the
//	  books never recorded leaves 4(C) higher than the department's own
//	  auto-populated figure by the whole of that note.
//	sec-16-6-revocation-not-modelled    s.16(6) credit for a period during which
//	  the registration stood cancelled and was later restored is NOT computed —
//	  no table here holds the cancellation or revocation dates. The direction is
//	  an UNDER-claim, so the filer pays tax they do not owe unless they check
//	  those periods by hand. Reaches this response only on the statement-backed
//	  path, because it comes from the reconciliation.
//	4d1-reclaim-not-modelled            There is no earlier-period reversal
//	  store, so 4(D)(1) is never emitted.
//	cess-not-modelled                   Compensation cess is nowhere in the books.
//	portal-schema-unverified            The JSON key names are NOT verified
//	  against the GSTN 3B offline-utility schema; portal upload is not claimed.
//	reversals-not-posted-to-books       A 4(B) reversal is return-side only; the
//	  filer must still pass Dr Expense / Cr Input tax themselves.
//	statement-partially-parsed          The 2A/2B parser recognised a section it
//	  could not decode. Under model (b) an undecoded document is indistinguishable
//	  from a supplier who never filed, so it REVERSES credit.
//
// s.17(5) IS honoured, with a stated boundary: it is honoured where the OPERATOR
// has classified the supplier ledger or the voucher as BLOCKED_17_5. Nothing
// infers the clause from the goods or services themselves, and the clause letter
// is free text rather than a validated vocabulary. See the doc comment at the
// top of internal/compliance/gstr/gstr3b.go and internal/compliance/itc/doc.go
// for the full lists.

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance/gstr"
	"github.com/khata/cored/internal/compliance/itc"
)

// handleGSTR3B generates the GSTR-3B summary for [from, to].
//
// Query params:
//
//	from, to        REQUIRED, YYYY-MM-DD.
//	gstin           filer GSTIN.
//	cgst_ledger_id / sgst_ledger_id / igst_ledger_id
//	                OUTPUT-tax ledgers, for the outward reconciliation (same
//	                names as /gstr1).
//	input_cgst_ledger_id / input_sgst_ledger_id / input_igst_ledger_id
//	                INPUT-tax ledgers, for the table-4 ITC reconciliation.
//	statement_id    OPTIONAL. Supplying it turns ON the 2A/2B-backed table 4 and
//	                is CHECKED against the statement the engine used.
//	as_on           OPTIONAL, RFC3339, default now. Only meaningful with
//	                statement_id; it decides the IMS projection.
//	exempt_turnover / total_turnover
//	                rule 42/43 E and F.
//	common_credit_cgst / _sgst / _igst
//	                rule 42 C2, HEAD BY HEAD.
//	capital_common_credit_cgst / _sgst / _igst
//	                rule 43 Tr, HEAD BY HEAD.
func (s *Server) handleGSTR3B(w http.ResponseWriter, r *http.Request) {
	ctx, ok := s.scopeCtx(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	from := q.Get("from")
	to := q.Get("to")
	if from == "" || to == "" {
		writeError(w, http.StatusBadRequest, "from and to query params are required (YYYY-MM-DD)")
		return
	}

	opts := gstr.Options3B{Options: gstr.Options{GSTIN: q.Get("gstin")}}
	var err error
	for _, p := range []struct {
		param string
		dst   *uuid.UUID
	}{
		{"cgst_ledger_id", &opts.CGSTLedgerID},
		{"sgst_ledger_id", &opts.SGSTLedgerID},
		{"igst_ledger_id", &opts.IGSTLedgerID},
		{"input_cgst_ledger_id", &opts.InputCGSTLedgerID},
		{"input_sgst_ledger_id", &opts.InputSGSTLedgerID},
		{"input_igst_ledger_id", &opts.InputIGSTLedgerID},
	} {
		if *p.dst, err = optionalUUID(q.Get(p.param)); err != nil {
			writeError(w, http.StatusBadRequest, "invalid "+p.param)
			return
		}
	}

	// Rule 42/43 first: whether they were supplied is an INPUT to the
	// reconciliation (it decides itc's own rule-42-43 disclosure), so it has to
	// be resolved before the ITC source is built.
	policy, err := rule4243Policy(q)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	opts.ITCPolicy = policy

	statementID, err := optionalUUID(q.Get("statement_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid statement_id (want a UUID)")
		return
	}
	asOn, err := resolveAsOn(q.Get("as_on"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if statementID != uuid.Nil {
		// THE ASSIGNMENT IS INSIDE THIS BRANCH ON PURPOSE. opts.ITC is an
		// INTERFACE: assigning s.itc unconditionally would store a non-nil
		// interface holding a possibly-nil pointer, gstr would take the
		// reconciled path for every caller, and the no-statement output would
		// stop being byte-identical to what it is today.
		ictx := itc.WithAsOn(ctx, asOn)
		rep, err := s.itc.Reconcile(ictx, from, to, itc.Options{
			AsOn:             asOn,
			Rule4243Supplied: policy != nil && (policy.Rule42 != nil || policy.Rule43 != nil),
		})
		if err != nil {
			writeITCError(w, from, to, err)
			return
		}
		if rep.StatementID != statementID {
			writeError(w, http.StatusBadRequest, statementIDMismatch(statementID, rep))
			return
		}
		opts.ITC = s.itc
		if policy == nil {
			policy = &gstr.ITCPolicy{}
			opts.ITCPolicy = policy
		}
		// FORWARDED VERBATIM. Not intersected with gstr.KnownDisclosures: an
		// intersection drops a token gstr has not been taught yet, and a dropped
		// disclosure is invisible to the filer on the one document they sign.
		// gstr validates the list itself and refuses an unknown token with
		// ErrInvalid3B naming it, so drift here is a loud 422 with the fix in the
		// message rather than a return that quietly under-discloses. See
		// DiscSec16_6NotModelled in compliance/itc/effect.go for the token this
		// used to lose.
		policy.SourceDisclosures = rep.Disclosures
		// From here on the context MUST carry the as-on instant: gstr calls
		// itc.Decisions(ctx, from, to), whose signature has no room for a clock,
		// and compliance/itc returns ErrNoAsOn rather than reading one itself.
		ctx = ictx
	}

	// When the caller supplied ANY tax ledger, reconcile the return against the
	// books and report whether both halves tie out to the paisa.
	anyLedger := opts.CGSTLedgerID != uuid.Nil || opts.SGSTLedgerID != uuid.Nil || opts.IGSTLedgerID != uuid.Nil ||
		opts.InputCGSTLedgerID != uuid.Nil || opts.InputSGSTLedgerID != uuid.Nil || opts.InputIGSTLedgerID != uuid.Nil
	if anyLedger {
		g, rec, err := s.gstr.Reconcile3B(ctx, from, to, opts)
		if err != nil {
			writeGSTR3BError(w, from, to, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"gstr3b":            g,
			"reconciliation_ok": rec.Balanced(),
		})
		return
	}

	g, err := s.gstr.Generate3B(ctx, from, to, opts)
	if err != nil {
		writeGSTR3BError(w, from, to, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// writeGSTR3BError keeps the historical 422 for a generation failure but lets
// the ITC sentinels through with their own statuses, so a one-period-window
// refusal reaching this path is still the 400 its contract says it is.
func writeGSTR3BError(w http.ResponseWriter, from, to string, err error) {
	if errors.Is(err, itc.ErrWindowNotOnePeriod) || errors.Is(err, itc.ErrNoAsOn) {
		writeITCError(w, from, to, err)
		return
	}
	writeError(w, http.StatusUnprocessableEntity, err.Error())
}

// THERE IS DELIBERATELY NO forwardableDisclosures FILTER HERE ANY MORE.
//
// One used to sit at this spot and intersect the reconciliation's disclosures
// with gstr.KnownDisclosures before forwarding them, on the reasoning that gstr
// refuses an unknown token and that the dropped token "is still on the
// reconciliation report". It dropped exactly one token in practice —
// `sec-16-6-revocation-not-modelled` — and the reconciliation report is not the
// document the filer signs. The GSTR-3B is. A filer whose registration was
// cancelled and later restored got a return that could not tell them s.16(6)
// credit had not been computed, and under-claimed credit they could not see is
// tax they do not owe.
//
// A filter that turns an unrecognised disclosure into silence is the same
// failure the closed vocabulary exists to prevent, moved one layer out. The list
// now goes across verbatim; gstr is the single validator, and an unknown token
// is ErrInvalid3B -> 422 naming the token, which is loud and fixable.
//
// rule4243Policy builds the caller-supplied rule 42/43 inputs, or nil when none
// were supplied.
//
// ---------------------------------------------------------------------------
// THE COMMON CREDIT IS TAKEN HEAD BY HEAD AND THERE IS NO SCALAR SHORTHAND
// ---------------------------------------------------------------------------
// C2 (rule 42) and Tr (rule 43) are TAX amounts, and GSTR-3B table 4 is
// head-wise: the apportionment scales each of CGST, SGST and IGST by E/F
// independently and the reversal lands in that head's column. A single
// `capital_common_credit=1000` cannot be split without inventing an
// apportionment, and putting the whole of it in one head reverses credit the
// filer holds in a head they do not — a wrong number in real cash. So the bare
// spellings are REFUSED WITH THE THREE NAMES THAT WOULD WORK, rather than
// guessed at. This is the same stance apportionment() itself takes on a missing
// F: refuse, because a zero reversal is indistinguishable from a correct one.
//
// ALL MONEY IS PARSED WITH decimal.NewFromString. There is no ParseFloat here
// and there must never be: a turnover figure through a float64 changes the
// reversal in the fourth decimal place, every time.
func rule4243Policy(q url.Values) (*gstr.ITCPolicy, error) {
	get := func(name string) string { return strings.TrimSpace(q.Get(name)) }
	for _, bare := range []struct{ name, headwise string }{
		{"common_credit", "common_credit_cgst, common_credit_sgst and common_credit_igst"},
		{"capital_common_credit", "capital_common_credit_cgst, capital_common_credit_sgst and capital_common_credit_igst"},
	} {
		if get(bare.name) != "" {
			return nil, fmt.Errorf("%s is a TAX amount and GSTR-3B table 4 is head-wise: supply %s "+
				"instead. A single figure cannot be apportioned across CGST/SGST/IGST without "+
				"inventing a split, and reversing credit in the wrong head is a wrong number in cash",
				bare.name, bare.headwise)
		}
	}

	money := func(name string) (decimal.Decimal, bool, error) {
		raw := get(name)
		if raw == "" {
			return decimal.Zero, false, nil
		}
		d, err := decimal.NewFromString(raw)
		if err != nil {
			return decimal.Zero, false, fmt.Errorf("invalid %s %q: want a decimal amount", name, raw)
		}
		return d, true, nil
	}
	heads := func(prefix string) (gstr.TaxTotals, bool, error) {
		t := gstr.TaxTotals{CGST: decimal.Zero, SGST: decimal.Zero, IGST: decimal.Zero}
		any := false
		for _, h := range []struct {
			suffix string
			dst    *decimal.Decimal
		}{
			{"_cgst", &t.CGST}, {"_sgst", &t.SGST}, {"_igst", &t.IGST},
		} {
			d, ok, err := money(prefix + h.suffix)
			if err != nil {
				return t, false, err
			}
			if ok {
				*h.dst = d
				any = true
			}
		}
		return t, any, nil
	}

	e, haveE, err := money("exempt_turnover")
	if err != nil {
		return nil, err
	}
	f, haveF, err := money("total_turnover")
	if err != nil {
		return nil, err
	}
	c2, haveC2, err := heads("common_credit")
	if err != nil {
		return nil, err
	}
	tr, haveTr, err := heads("capital_common_credit")
	if err != nil {
		return nil, err
	}

	if !haveE && !haveF && !haveC2 && !haveTr {
		return nil, nil
	}
	if !haveE || !haveF {
		return nil, fmt.Errorf("rule 42/43 needs BOTH exempt_turnover (E) and total_turnover in the " +
			"State (F): they are never derived from this return, because the books carry no " +
			"export/SEZ marker and an engine that substituted its own E would report a zero " +
			"reversal for every taxpayer")
	}
	if !haveC2 && !haveTr {
		return nil, fmt.Errorf("exempt_turnover and total_turnover were supplied but no common credit " +
			"was: supply common_credit_cgst/_sgst/_igst (rule 42 C2) and/or " +
			"capital_common_credit_cgst/_sgst/_igst (rule 43 Tr). Computing the apportionment over a " +
			"zero credit would clear the rule-42-43-not-computed disclosure while reversing nothing")
	}

	policy := &gstr.ITCPolicy{}
	if haveC2 {
		policy.Rule42 = &gstr.Rule42Inputs{C2: c2, E: e, F: f}
	}
	if haveTr {
		policy.Rule43 = &gstr.Rule43Inputs{Tr: tr, E: e, F: f}
	}
	return policy, nil
}
