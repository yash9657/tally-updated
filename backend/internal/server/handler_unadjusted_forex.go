package server

// handler_unadjusted_forex.go — T8 STEP 7's DOOR.
//
// ⛔ THE REPORT POSTS NOTHING, AND THIS FILE IS WHERE THAT STOPS BEING A CLAIM AND
// BECOMES A SHAPE: there is one route, it is a GET, and there is no writer anywhere
// in the package for `unadjusted-forex`. Q4 of docs/design/t8-multi-currency-model.md
// defers the revaluation VOUCHER, and §2.2 of the memo already proves it could only
// ever BE a voucher — so deferring it costs no design debt and building a POST here
// would be inventing the thing that was deferred.

import (
	"net/http"

	"github.com/khata/cored/internal/reporting"
)

// handleUnadjustedForex serves the unrealised gain/loss report.
//
// ⚠️ `as_on` IS READ OFF THE QUERY STRING AND NOT OUT OF A REQUEST STRUCT, which is
// how every as-at report in this package reads it (`/outstandings`, `/stock-summary`,
// `/cash-flow`). ⭐ THAT ALSO SETTLES THE REGISTRATION QUESTION THE HANDOFF RAISED:
// `TestEveryDateShapedWireFieldIsAccountedForByTheCutoff` parses this package's
// sources for STRUCT FIELDS carrying a date-shaped json tag — `as_on` would match its
// matcher (`HasSuffix(k, "_on")`) if it were a field — so a request struct here would
// need a classification and a query parameter does not. Measured: no as-at report in
// this package has an entry today, for the same reason.
//
// ⛔ AND THE BACK-DATED CUTOFF DOES NOT APPLY EITHER WAY. `cutoffDeny` gates on
// `Action.IsWrite()`, and this is a Display. A GET report cannot back-date anything;
// the cutoff exists to stop a WRITE landing in a closed period.
func (s *Server) handleUnadjustedForex(w http.ResponseWriter, r *http.Request) {
	ctx, companyID, ok := s.scopeCtxID(w, r)
	if !ok {
		return
	}
	asOn := r.URL.Query().Get("as_on")
	out, err := s.report.UnadjustedForex(ctx, companyID, asOn)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, unadjustedForexJSON(out))
}

// unadjustedForexJSON is the ONE writing of this report's wire shape, shared by the
// JSON route and by nothing else today — but named and separated because the printed
// form reads the same `reporting.UnadjustedForex` and D20's defect is two surfaces
// over one fact drifting apart.
func unadjustedForexJSON(out reporting.UnadjustedForex) map[string]any {
	bills := make([]map[string]any, 0, len(out.Bills))
	for _, b := range out.Bills {
		bills = append(bills, map[string]any{
			"ledger_id":     b.LedgerID.String(),
			"ledger":        b.LedgerName,
			"group":         b.GroupName,
			"ref_name":      b.RefName,
			"ref_type":      b.RefType,
			"currency_code": b.CurrencyCode,
			// Money, at four decimals, like every other amount on this wire.
			"foreign_pending": b.ForeignPending.StringFixed(4),
			"book_value":      b.BookValue.StringFixed(4),
			"difference":      b.Difference.StringFixed(4),
			// ⛔⛔ THE TWO RATES ARE RENDERED AT **TEN** DECIMALS AND NOT AT FOUR, AND
			// THIS IS THE ONE PLACE IN STEP 7 WHERE A WRONG CHOICE IS INVISIBLE (§6.3
			// of the handoff). A rate is NUMERIC(19,10); money is (19,4).
			// `StringFixed(4)` on a rate READS as consistency with every other number
			// on this page and silently changes it — 0.0033333333 becomes 0.0033,
			// thousands of rupees on a fifty-lakh invoice — and the truncated value
			// round-trips through its own type perfectly, so nothing fails.
			//
			// ⭐ STEP 5 MUTATION-PROVED THE COST: quantising the rates before the
			// multiplication turned 1,600.0046 into 1,599.9700. This report RENDERS
			// rates rather than only computing with them, which is exactly where "make
			// the columns line up" wins arguments.
			//
			// ⚠️ `bill_rate` IS ZERO WHEN THE OPENER CARRIED NONE, which cannot happen
			// through any write path today (0080's all-or-none CHECK), and is rendered
			// rather than omitted so the client never has to distinguish absent from
			// zero on a field it does not compute with.
			"bill_rate": b.BillRate.StringFixed(10),
			// ⛔ `has_rate_as_on` IS THE FIELD TO READ, NEVER `rate_as_on`. Zero is
			// what "no rate was ever entered" looks like as a decimal, and a zero rate
			// revalues the whole exposure to nothing — a loss of the entire book
			// value, correctly signed, in the right column, from a rate nobody keyed.
			"rate_as_on":     b.RateAsOn.StringFixed(10),
			"has_rate_as_on": b.HasRateAsOn,
		})
	}
	return map[string]any{
		// ⛔⛔ `as_on` IS EMPTY WHEN THE CALLER NAMED NO DATE (D86), and `priced_as_on`
		// is ALWAYS populated — the same pair `/outstandings` emits and for the same
		// reason, except that here the second half is sharper: an unbounded report
		// still PRICES every row against some day, and a screen that could not say
		// which day would be showing a revaluation with no reference.
		"as_on":        boundedAsOn(out.Bounded, out.AsOn),
		"priced_as_on": out.AsOn,
		"bills":        bills,
		// ⭐ THIS TOTAL IS MEANINGFUL, unlike Outstandings' foreign column: a
		// DIFFERENCE is in the books' own currency whatever currency produced it, so a
		// book with dollar and euro bills has one honest total — and receivable and
		// payable rows OFFSET rather than accumulate.
		"total_difference": out.TotalDifference.StringFixed(4),
		// ⛔ THE DISCLOSURE THAT THE TOTAL IS SHORT. Rows whose currency has no rate on
		// or before the date are LISTED (an exposure with an unknown rate is still an
		// exposure) and not PRICED, so the total omits them and this says how many.
		// The same argument `on_account_total` exists for on the ageing report.
		"unrated_count": out.UnratedCount,
		// ⛔ SAID ON THE WIRE AND NOT ONLY ON THE SCREEN. Q4 requires the product to
		// state that this report posts nothing, in those words, and a client that had
		// to hard-code that sentence could drift from the server that knows it.
		"posts_nothing": true,
	}
}

// boundedAsOn is D86's convention, factored so the two reports that follow it cannot
// spell it differently: the resolved date when the caller NAMED one, and the empty
// string — which is an ANSWER and not a missing field — when they did not.
func boundedAsOn(bounded bool, asOn string) string {
	if bounded {
		return asOn
	}
	return ""
}
