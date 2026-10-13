package reporting

// unadjusted_forex.go — T8 STEP 7: WHAT AN OPEN FOREIGN BILL IS WORTH TODAY, AS
// AGAINST WHAT THE BOOKS CARRY IT AT. A REPORT, AND IT POSTS NOTHING.
//
// Step 5 realises the difference when a foreign bill is SETTLED. Between the invoice
// and the receipt the rate moves and the books do not: a $1,000 export invoice raised
// at 83.65 and still open at 88.45 is ₹4,800 the business has gained, and until this
// file existed no report in the product would say so.
//
// =============================================================================
// ⭐⭐ DECISION 1 — REPORT ONLY. Q4, AND IT IS THE REPO OWNER'S OWN ANSWER.
// =============================================================================
// docs/design/t8-multi-currency-model.md Q4: "report only" in v1. The revaluation
// VOUCHER is deferred and costs no design debt — §2.2 of the memo already proves it
// could only ever BE a voucher, because postings are immutable, and v1's schema is
// the schema it would need.
//
// ⛔ SO NOTHING HERE WRITES, AND THE PRODUCT MUST SAY SO ON THE SCREEN AND IN F11 —
// in those words. A revaluation figure that looks like a posted balance is the
// FeaturesScreen failure mode with a number attached.
//
// =============================================================================
// ⭐⭐ DECISION 2 — THE ARITHMETIC IS `realisedFor`'s, WITH THE FINAL-SLICE BRANCH
//    TAKEN. THE TWO REPORTS CANNOT BE ALLOWED TO DISAGREE.
// =============================================================================
//
//	unrealised = round(foreign_pending × rate_as_on, 4) − book_value_carried
//
// Settle the whole remaining balance today at today's rate and step 5's engine
// derives EXACTLY this figure: its final slice clears the remaining book value, so
// `base_cleared` is `book_value_carried` and `foreign_settled` is `foreign_pending`.
// ⛔ IF THESE TWO EVER DISAGREE, ONE OF THE TWO REPORTS IS WRONG AND NOTHING ELSE IN
// THE PRODUCT WOULD SAY WHICH — `TestTodaysUnrealisedIsWhatASettlementTodayWouldRealise`
// posts the report's own prediction through the engine's verify path, so the engine
// is the assertion.
//
// ⚠️ AND IT DIFFERENCES THE VALUES, NOT THE RATES, for Decision 2 of
// forex_settlement.go: a bill's stored base is authoritative and is deliberately NOT
// `foreign × rate`. `foreign × (rate_as_on − bill_rate)` is short by the bill's own
// rounding residue and would put a figure on this report that a settlement could
// never realise.
//
// ⛔ ONE ROUNDING, ON THE PRODUCT, AT MONEY SCALE. Not on the rate — §6.3 of the
// handoff: a rate is NUMERIC(19,10) and money is (19,4), and quantising the rate
// first turned 1,600.0046 into 1,599.9700 in step 5's own mutation proof: plausible,
// in the right column, on the right side, and wrong for ever.
//
// =============================================================================
// ⭐⭐ DECISION 3 — A MISSING RATE IS **NOT** A RATE OF ZERO, AND THIS IS THE ONE
//    THING THIS REPORT CANNOT SHIP WITHOUT.
// =============================================================================
// A currency with no rate on or before the report date cannot be priced. A report
// that read the decimal and ignored the "found" flag would revalue a $1,000
// receivable carried at 83,651.23 at a rate of ZERO — a LOSS of the entire book
// value: large, plausible, correctly signed, in the right column, and produced by a
// rate nobody entered. `masters.RateAsOn` returns `(zero, false, nil)` for exactly
// this case and the `false` is the whole answer.
//
// ⛔ THE ROW IS STILL LISTED. A bill whose rate is unknown is still an EXPOSURE, and
// dropping it is how an operator reads a short report as a small exposure. It is
// listed with `HasRateAsOn` false, a zero difference, EXCLUDED from the total, and
// counted in `UnratedCount` so the screen can say the total is short.
//
// =============================================================================
// ⭐⭐ DECISION 4 — ONE REPORT COVERING BOTH SIDES, AND D94's ARGUMENT IS ANSWERED
//    RATHER THAN IGNORED.
// =============================================================================
// Outstandings and Bills Payable are TWO report names because the PDP decides on the
// report NAME and a query parameter is invisible to it — served as one report with
// `?kind=`, *"this level may see what customers owe us but not what we owe
// suppliers"* was a rule nobody could write down.
//
// ⭐ THAT ARGUMENT DOES NOT REACH HERE, FOR TWO REASONS:
//
//  1. THE SUBJECT IS THE COMPANY'S **NET** EXPOSURE TO A CURRENCY, which is not
//     separable by side. A $1,000 receivable and a $1,000 payable at one rate are an
//     exposure of NOTHING, and two reports would show two numbers neither of which
//     is the company's position. Splitting it would not restrict information, it
//     would destroy the only figure the report exists to produce.
//  2. THERE IS NO OPERATIONAL JOB THAT NEEDS ONE SIDE. Bills Payable ships with no
//     ACL counterweight precisely because a clerk keying a Payment NEEDS the payable
//     ageing — the bill-wise picker asks for it, and a clerk refused it settles ON
//     ACCOUNT instead. Nothing keys anything off this report; it posts nothing.
//
// ⛔ SO IT IS DENIED TO THE SEEDED DATA ENTRY LEVEL OUTRIGHT (migration 0090,
// `disallow display` AND `disallow print`), and a tenant that grants it grants both
// sides — which is stated on the screen rather than left to be discovered.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// moneyScale is the scale every amount in this product is carried at. ⚠️ A RATE IS
// NOT MONEY and is never rounded to this — see Decision 2.
const forexMoneyScale = 4

// UnadjustedForexBill is one open FOREIGN reference's unrealised position: the five
// figures Q4 names, plus the two flags that keep them honest.
type UnadjustedForexBill struct {
	LedgerID   uuid.UUID
	LedgerName string
	GroupName  string
	RefName    string
	// RefType is 'NEW' or 'ADVANCE' — see the ADVANCE decision on UnadjustedForex.
	// It is emitted rather than inferred because the two are revalued the same way
	// and mean different things to a reader.
	RefType string

	CurrencyCode string
	// ForeignPending is what is STILL OPEN in the foreign currency, signed to the
	// party's natural side exactly as Bill.ForeignPending is. Negative on an advance
	// held, which is why the sign is carried rather than an absolute value.
	ForeignPending decimal.Decimal
	// BillRate is the rate the reference was RAISED at — a fact about the document,
	// read off the opener (the `NEW` allocation or the ledger's opening breakup).
	//
	// ⚠️ IT IS INFORMATIONAL AND THE DIFFERENCE DOES NOT USE IT. The difference is
	// measured against BookValue, which is what the books actually carry; a rate is
	// carried here so a reader can see WHY the difference is what it is.
	BillRate decimal.Decimal
	// RateAsOn is the latest rate on or before the report date. ⛔ READ HasRateAsOn,
	// NEVER THIS: zero is what "no rate was ever entered" looks like as a decimal,
	// and a zero rate revalues the whole exposure to nothing.
	RateAsOn    decimal.Decimal
	HasRateAsOn bool
	// BookValue is the base figure the books carry for the part still open — the
	// same figure Outstandings puts in Bill.Pending, and the one that ties to Sundry
	// Debtors/Creditors on the Balance Sheet.
	BookValue decimal.Decimal
	// Difference is the unrealised gain (positive) or loss (negative), IN THE BOOKS'
	// OWN CURRENCY, and ZERO when HasRateAsOn is false.
	Difference decimal.Decimal
}

// UnadjustedForex is the unrealised gain/loss report.
//
// ⛔⛔ THE ADVANCE DECISION, AND IT IS TAKEN HERE RATHER THAN IN THE MEMO. The memo
// notes that "ADVANCE and ON_ACCOUNT have no bill rate to difference against …
// they realise nothing until they are allocated". ⭐ THAT NOTE IS ABOUT Q3 —
// REALISED — and it says so in its own words. An UNREALISED difference is measured
// against the BOOK VALUE CARRIED and not against a bill's historical rate, so:
//
//   - A foreign ADVANCE **IS** revalued. It is a named reference with one rate on one
//     document and a real book value; $10,000 received at 83 and still held at 88 is
//     a genuine ₹50,000 exposure and omitting it understates the position.
//   - `On Account` is NOT on this report. Step 6 made that bucket base-only and
//     currency-collapsed deliberately: it is `the whole balance − every named
//     reference`, a difference of sums over many vouchers at many rates, and it
//     carries no currency at all. A row here would be a revaluation of a figure with
//     no rate — which is Decision 3's failure with the flag bypassed.
//
// ⚠️ THE FIRST OF THOSE IS A DESIGN DECISION AND NOT PARITY. Flagged rather than
// folded in.
type UnadjustedForex struct {
	// AsOn is the date the revaluation RATE is taken at, and it is always populated —
	// every figure on this report is priced against some day.
	AsOn string
	// Bounded is whether the CALLER named a date, and therefore whether the rows are
	// limited to vouchers dated on or before AsOn.
	//
	// ⛔ D86, AND ON THIS REPORT THE DATE REALLY DOES TWO JOBS. It bounds which
	// vouchers are IN and it chooses which rate PRICES them, and a report that
	// honoured it for one and not the other is D86's exact shape: a caption that is
	// present, well-formed and false. Outstandings needed `Bounded` because `AsOn`
	// could not say both things; this report needs it for the same reason and the
	// second job makes it sharper, not softer.
	Bounded bool
	Bills   []UnadjustedForexBill
	// TotalDifference is the sum of the differences of the rows that could actually
	// be PRICED.
	//
	// ⭐ UNLIKE OUTSTANDINGS' FOREIGN COLUMN, THIS TOTAL IS MEANINGFUL, and the
	// contrast is worth carrying: Q5 makes that report's total base-only because
	// summing dollars into rupees is nonsense. A DIFFERENCE is already in the books'
	// own currency whatever currency produced it, so a book with dollar and euro
	// bills has one honest total here — which is also why receivable and payable
	// rows OFFSET rather than accumulate (Decision 4).
	TotalDifference decimal.Decimal
	// UnratedCount is how many listed rows could NOT be priced, because their
	// currency has no rate on or before AsOn.
	//
	// ⛔ IT IS THE DISCLOSURE THAT THE TOTAL IS SHORT. A report whose total silently
	// omits a row is worse than one that says what it omitted — the same argument
	// `OnAccountTotal` exists for on Outstandings.
	UnratedCount int
}

// UnadjustedForex builds the unrealised gain/loss report as of asOn (ISO date; empty
// means today for the RATE and unbounded for the ROWS — see Bounded).
//
// ⛔⛔ IT IS A NEW SPELLING OF THE TWO-TABLE UNION AND THAT IS DELIBERATE, NOT LAZY.
// §0.2 of the memo: the open-bill universe is `bill_allocation` UNION
// `ledger_opening_bill`, and handling only the first makes every migrated foreign
// debtor's exposure invisible — which for a customer acquired before this build IS
// their whole history. `outstanding.go` cannot be called for it: `internal/archtest`
// allows `reporting` no import of `internal/ledger`, and Outstandings' own query is
// shaped around ageing slabs and an On Account residue this report has no use for.
// This is the sixth spelling in the package and the comment above each says why.
func (s *Service) UnadjustedForex(ctx context.Context, companyID uuid.UUID, asOn string) (UnadjustedForex, error) {
	if err := scope(ctx, companyID); err != nil {
		return UnadjustedForex{}, err
	}
	asOnTime, err := resolveAsOn(asOn)
	if err != nil {
		return UnadjustedForex{}, err
	}
	// Spelled the way Outstandings spells it, so "the caller named nothing" and "the
	// caller named today" stay two different questions on both reports (D86).
	bounded := strings.TrimSpace(asOn) != ""

	out := UnadjustedForex{
		AsOn:            asOnTime.Format("2006-01-02"),
		Bounded:         bounded,
		TotalDifference: decimal.Zero,
	}
	asOnISO := asOnTime.Format("2006-01-02")

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// ⛔ THE INCLUSION BOUND AND ITS ARGUMENT LIST MOVE TOGETHER, for the reason
		// `outstanding.go` states in full: pgx binds exactly what the statement
		// references, so leaving `$1` in the args while dropping it from the text
		// answers "bind message supplies 1 parameters, but prepared statement requires
		// 0" — a 500 on the report rather than a wrong number.
		//
		// ⚠️ `$1` IS THE RATE DATE AND IS ALWAYS BOUND, because the rate lookup always
		// needs a day even when the rows are unbounded. Only the voucher predicate is
		// conditional, and it binds `$1` too rather than a second parameter — one date
		// answers both questions and passing it twice invites them to drift.
		inclusionBound := ""
		if bounded {
			inclusionBound = "\n\t\t\t             AND vv.voucher_date <= $1::date"
		}
		openingBound := ""
		if bounded && !s.openingIsInScope(ctx, tx, asOnTime) {
			// ⛔ THE OPENING BREAKUP IS ALL-OR-NOTHING, exactly as it is on
			// Outstandings and for the same stated reason: an opening carries no date
			// of its own, it is stated as at books_beginning_from, and asked for a date
			// BEFORE that the position did not exist.
			openingBound = " AND false"
		}

		// The netting key is `(ledger, ref_name, currency)` — Q5's key, and step 6's,
		// because two reports over one bill universe that grouped it differently would
		// disagree about what a bill IS.
		//
		// ⛔ `currency_id IS NOT NULL` ON THE **OPENER**, not on the row. A foreign
		// bill settled by a BASE allocation (the case step 5 deliberately does not
		// refuse) must keep its foreign group, and the base group must not appear here
		// at all: it has no currency and nothing to revalue. Filtering on the row would
		// drop nothing and filtering on the group's currency does exactly the right
		// thing, because the currency IS the group key.
		//
		// ⚠️ AND `On Account` IS EXCLUDED BY NAME, WHICH IS LOAD-BEARING FOR EXACTLY
		// ONE CASE — MEASURED. The DERIVED residue cannot reach this report at all: it
		// is computed from `posting`, and this query reads `bill_allocation` and
		// `ledger_opening_bill` and never `posting`. What the filter catches is an
		// **explicit** `ON_ACCOUNT` allocation, which arrives here with a currency and
		// a rate on it. Reverting this line puts `Walk-In Exports / On Account USD
		// diff=-307.0741` on the report.
		//
		// ⭐ AND REVALUING THAT WOULD BE WORSE THAN OMITTING IT. Outstandings shows the
		// whole On Account bucket as ONE base figure (step 6 collapses it, because it
		// is `the whole balance − every named reference` over many rates). Revaluing
		// only the fragment that happens to have been keyed with an allocation is a
		// partial revaluation of a figure no report presents in parts — a number that
		// ties to nothing on either page.
		rows, err := tx.Query(ctx, fmt.Sprintf(`
			SELECT x.ledger_id, x.ledger_name, x.group_name, x.ref_name, x.ref_type,
			       c.code, x.foreign_pending_dr, x.bill_rate, x.book_value_dr, x.nature,
			       r.rate
			  FROM (
			         SELECT ba.ledger_id, l.name AS ledger_name, g.name AS group_name,
			                ba.ref_name, ba.currency_id, g.nature,
			                MAX(ba.ref_type::text) FILTER (WHERE ba.is_opener) AS ref_type,
			                MAX(ba.rate)           FILTER (WHERE ba.is_opener) AS bill_rate,
			                SUM(CASE WHEN ba.dr_cr='D' THEN ba.amount ELSE -ba.amount END)
			                  AS book_value_dr,
			                SUM(CASE WHEN ba.dr_cr='D' THEN ba.foreign_amount
			                         ELSE -ba.foreign_amount END) AS foreign_pending_dr
			           FROM (
			                  SELECT b.ledger_id, b.ref_name, b.ref_type, b.dr_cr,
			                         b.amount, b.currency_id, b.foreign_amount, b.rate,
			                         (b.ref_type <> 'AGAINST') AS is_opener
			                    FROM bill_allocation b
			                    JOIN voucher_version vv ON vv.id = b.voucher_version_id
			                    JOIN voucher_head    vh
			                      ON vh.company_id = vv.company_id
			                     AND vh.voucher_id = vv.voucher_id
			                     AND vh.current_version_id = vv.id
			                   WHERE vh.is_deleted = false
			                     AND b.currency_id IS NOT NULL
			                     AND b.ref_name <> '%[2]s'%[1]s
			                  UNION ALL
			                  SELECT ob.ledger_id, ob.ref_name, ob.ref_type, ob.dr_cr,
			                         ob.amount, ob.currency_id, ob.foreign_amount, ob.rate,
			                         true AS is_opener
			                    FROM ledger_opening_bill ob
			                   WHERE ob.currency_id IS NOT NULL
			                     AND ob.ref_name <> '%[2]s'%[3]s
			                ) ba
			           JOIN ledger        l ON l.id = ba.ledger_id
			           JOIN account_group g ON g.id = l.group_id
			          WHERE EXISTS (SELECT 1 FROM account_group anc
			                         WHERE anc.company_id = g.company_id
			                           AND anc.behaves_as_subledger
			                           AND g.path <@ anc.path)
			          GROUP BY ba.ledger_id, l.name, g.name, ba.ref_name,
			                   ba.currency_id, g.nature
			       ) x
			  JOIN currency c ON c.id = x.currency_id
			  LEFT JOIN LATERAL (
			         SELECT cr.rate FROM currency_rate cr
			          WHERE cr.currency_id = x.currency_id
			            AND cr.effective_date <= $1::date
			          ORDER BY cr.effective_date DESC
			          LIMIT 1
			       ) r ON true
			 ORDER BY x.ledger_name, x.ref_name, c.code`,
			inclusionBound, OnAccountRefName, openingBound), asOnISO)
		if err != nil {
			return fmt.Errorf("reporting: query unadjusted forex: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var (
				b         UnadjustedForexBill
				nature    string
				refType   *string
				billRate  *decimal.Decimal
				rateAsOn  *decimal.Decimal
				bookDr    decimal.Decimal
				foreignDr decimal.Decimal
			)
			if err := rows.Scan(&b.LedgerID, &b.LedgerName, &b.GroupName, &b.RefName,
				&refType, &b.CurrencyCode, &foreignDr, &billRate, &bookDr, &nature,
				&rateAsOn); err != nil {
				return fmt.Errorf("reporting: scan unadjusted forex: %w", err)
			}

			// ⛔ THE SIGN IS ORIENTED TO THE PARTY'S NATURAL SIDE, exactly as
			// Outstandings orients Pending, so the two reports agree about what a
			// bill is worth before either of them revalues anything. A receivable
			// (debit-natured party) is positive on the debit side; a payable is
			// positive on the credit side.
			b.BookValue, b.ForeignPending = bookDr, foreignDr
			if nature == "LIABILITY" {
				b.BookValue, b.ForeignPending = bookDr.Neg(), foreignDr.Neg()
			}
			// ⛔ THE SKIP IS STEP 6's TWO-QUESTION SKIP. Both columns zero means the
			// reference is closed and its difference has been REALISED — reporting it
			// here would double-count it. Either column non-zero is still an exposure.
			if b.BookValue.IsZero() && b.ForeignPending.IsZero() {
				continue
			}
			if refType != nil {
				b.RefType = *refType
			}
			if billRate != nil {
				b.BillRate = *billRate
			}

			if rateAsOn != nil {
				b.HasRateAsOn = true
				b.RateAsOn = *rateAsOn
				// ⭐ round(foreign × rate, 4) − book value. ONE rounding, on the
				// PRODUCT.
				//
				// ⛔⛔ AND THE SIGN IS FLIPPED ON A PAYABLE, WHICH IS A SECOND BRANCH
				// AND CANNOT BE AVOIDED. An earlier draft of this comment claimed it
				// could — that orienting the figures to the party's natural side above
				// made the difference come out right on both sides "with no second
				// branch, which is the one that gets forgotten". ⚠️ IT WAS MEASURED
				// WRONG: `TestARisingRateLOSESOnAPayableAndGAINSOnAReceivable` reported
				// the payable's difference as **+4800.0046 instead of −4800.0046**, and
				// the total as 9600.0092 instead of zero. Orienting to the natural side
				// makes an open payable POSITIVE, so the expression then gains on both
				// sides — wrong by TWICE the figure, with every number on the page still
				// looking like money.
				//
				// ⭐ `realisedFor` HAS EXACTLY THIS BRANCH AND SAYS EXACTLY WHY: "the
				// rupees went OUT. Paying more than the books carry is a LOSS, so the
				// same rise in the rate that gains on a receivable loses here." The two
				// engines now agree in shape as well as in arithmetic.
				received := b.ForeignPending.Mul(b.RateAsOn).Round(forexMoneyScale)
				b.Difference = received.Sub(b.BookValue)
				if nature == "LIABILITY" {
					b.Difference = b.Difference.Neg()
				}
				out.TotalDifference = out.TotalDifference.Add(b.Difference)
			} else {
				// Decision 3. Listed, not priced, not totalled, counted.
				out.UnratedCount++
			}
			out.Bills = append(out.Bills, b)
		}
		return rows.Err()
	})
	if err != nil {
		return UnadjustedForex{}, err
	}

	// Stable order: the largest ABSOLUTE exposure first, because a loss of 50,000 is
	// as interesting as a gain of 50,000 and sorting signed would bury every loss at
	// the bottom. Unpriced rows sort last — they have no difference to rank by, and
	// putting them above priced rows would order the page by an absence.
	sort.SliceStable(out.Bills, func(i, j int) bool {
		a, b := out.Bills[i], out.Bills[j]
		if a.HasRateAsOn != b.HasRateAsOn {
			return a.HasRateAsOn
		}
		if !a.Difference.Abs().Equal(b.Difference.Abs()) {
			return a.Difference.Abs().GreaterThan(b.Difference.Abs())
		}
		if a.LedgerName != b.LedgerName {
			return a.LedgerName < b.LedgerName
		}
		return a.RefName < b.RefName
	})
	return out, nil
}
