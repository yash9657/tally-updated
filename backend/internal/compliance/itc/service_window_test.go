package itc_test

// service_window_test.go is the gate on Reconcile's ONE-PERIOD CONTRACT.
//
// THE BUG IT EXISTS FOR: Reconcile derived the tax period from `from` alone
// (`period := PeriodOf(fromT)`) but loaded the BOOKS for the whole [from, to]
// range. Handed a quarter, it loaded April's GSTR-2B and April-to-June's
// purchases, so every May and June purchase had nothing to match against, was
// classified MISSING_IN_2B and was REVERSED IN FULL in table 4(B)(2) — two
// thirds of a quarter's input tax credit destroyed by a report that returned no
// error at all.
//
// These tests need NO DATABASE. The guard is required to run before any loader
// touches the DB, so a runner that never executes its unit of work is enough to
// tell "the window was refused" from "the window was accepted"; and that is
// exactly the property under test. windowRunner asserts the second half of it:
// a REFUSED window must not have opened a transaction.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/compliance/itc"
	"github.com/khata/cored/internal/platform/db"
)

// windowRunner is a TxRunner that records whether a unit of work was ever
// started and deliberately does NOT run it. Every loader inside Reconcile is
// therefore a no-op, which leaves an empty report — enough to prove the window
// passed the guard, without a company, a migration or a teardown.
type windowRunner struct{ opened int }

func (r *windowRunner) RunInTx(ctx context.Context, fn db.TxFunc) error {
	r.opened++
	return nil
}

// windowCtx is a context carrying a company id but no database. Reconcile's
// company check is the only thing that reads it.
func windowCtx() context.Context {
	return db.WithCompany(context.Background(), uuid.MustParse("11111111-1111-1111-1111-111111111111"))
}

func windowOpts() itc.Options {
	return itc.Options{AsOn: time.Date(2025, 8, 1, 10, 0, 0, 0, itc.IST)}
}

// TestReconcileRefusesMultiMonthWindowInsteadOfReversingRealCredit is THE
// regression test for the blocker. A quarterly window must be refused outright.
//
// If it is not refused, Reconcile answers with a report in which every purchase
// made in months 2..N of the quarter is MISSING_IN_2B and is reversed in full
// in 4(B)(2) — real, filed, matched credit destroyed because the wrong month's
// statement was the only one loaded.
func TestReconcileRefusesMultiMonthWindowInsteadOfReversingRealCredit(t *testing.T) {
	cases := []struct {
		name       string
		from, to   string
		periods    int
		mustName   []string // periods whose credit would be wrongly reversed
		neverNamed string   // the one period that WOULD have been loaded
	}{
		{
			name: "a quarter reverses two of its three months",
			from: "2025-04-01", to: "2025-06-30",
			periods:    3,
			mustName:   []string{"052025", "062025"},
			neverNamed: "042025",
		},
		{
			name: "a financial year reverses eleven of its twelve months",
			from: "2025-04-01", to: "2026-03-31",
			periods:  12,
			mustName: []string{"052025", "032026"},
		},
		{
			name: "two months is already one month too many",
			from: "2026-01-01", to: "2026-02-28",
			periods:  2,
			mustName: []string{"022026"},
		},
		{
			name: "one day over the month end still spans two periods",
			from: "2025-04-01", to: "2025-05-01",
			periods:  2,
			mustName: []string{"052025"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &windowRunner{}
			svc := itc.NewService(runner)

			rep, err := svc.Reconcile(windowCtx(), tc.from, tc.to, windowOpts())
			if err == nil {
				t.Fatalf("WRONG CREDIT REVERSED: Reconcile accepted the %d-tax-period window "+
					"%s..%s and returned a report for period %q. Only that one period's "+
					"GSTR-2B is ever loaded, so every purchase in the other %d month(s) is "+
					"classified MISSING_IN_2B and its input tax is reversed IN FULL in table "+
					"4(B)(2) — the company loses credit it lawfully holds and its supplier "+
					"did file. Reconcile must refuse a window that is not exactly one tax "+
					"period",
					tc.periods, tc.from, tc.to, rep.Period, tc.periods-1)
			}
			if !errors.Is(err, itc.ErrWindowNotOnePeriod) {
				t.Fatalf("the %d-tax-period window %s..%s was rejected, but not with "+
					"ErrWindowNotOnePeriod, so no handler can tell this apart from a "+
					"database failure and retry logic will keep asking for a window that "+
					"can only ever destroy credit; got: %v",
					tc.periods, tc.from, tc.to, err)
			}
			if runner.opened != 0 {
				t.Fatalf("the refused window %s..%s still opened %d transaction(s): the guard "+
					"must run before any books or statement is loaded",
					tc.from, tc.to, runner.opened)
			}
			msg := err.Error()
			for _, p := range tc.mustName {
				if !strings.Contains(msg, p) {
					t.Fatalf("the refusal for %s..%s does not name tax period %s, whose credit "+
						"would have been wrongly reversed; an operator reading this error "+
						"cannot tell which months went missing. Got: %s", tc.from, tc.to, p, msg)
				}
			}
			if tc.neverNamed != "" && !strings.Contains(msg, tc.neverNamed) {
				t.Fatalf("the refusal for %s..%s does not name %s, the only period whose "+
					"GSTR-2B would have been loaded. Got: %s", tc.from, tc.to, tc.neverNamed, msg)
			}
		})
	}
}

// TestReconcileAcceptsEveryWholeCalendarMonth is the OVER-TIGHTNESS gate.
//
// The obvious way to get the guard wrong is an off-by-one on the last day of
// the month — hard-coding 30, or treating February as 28 in a leap year. A
// guard that refuses a legitimate whole month is not a safe failure: it makes
// the company unable to produce that month's GSTR-3B at all, so the return is
// filed late or filed by hand.
//
// Every month length India's calendar produces is covered: 31, 30, 28 and — the
// one that bites — 29 in February 2024 and February 2028.
func TestReconcileAcceptsEveryWholeCalendarMonth(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		days     int
		want     string
	}{
		{"31-day January", "2026-01-01", "2026-01-31", 31, "012026"},
		{"30-day April", "2025-04-01", "2025-04-30", 30, "042025"},
		{"28-day February in a common year", "2025-02-01", "2025-02-28", 28, "022025"},
		{"29-day February in a leap year", "2024-02-01", "2024-02-29", 29, "022024"},
		{"29-day February in the next leap year", "2028-02-01", "2028-02-29", 29, "022028"},
		{"28-day February in 2100, a century non-leap year", "2100-02-01", "2100-02-28", 28, "022100"},
		{"31-day December, the year boundary", "2025-12-01", "2025-12-31", 31, "122025"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &windowRunner{}
			svc := itc.NewService(runner)

			rep, err := svc.Reconcile(windowCtx(), tc.from, tc.to, windowOpts())
			if err != nil {
				t.Fatalf("CANNOT FILE %s: Reconcile refused the whole %d-day month %s..%s, "+
					"which is exactly one tax period. The company cannot produce that "+
					"month's GSTR-3B table 4 at all and must file it by hand or file it "+
					"late. Error was: %v", tc.want, tc.days, tc.from, tc.to, err)
			}
			if rep.Period != tc.want {
				t.Fatalf("Reconcile accepted %s..%s but resolved it to tax period %q instead "+
					"of %q: the credit would be reported in the wrong month's return",
					tc.from, tc.to, rep.Period, tc.want)
			}
			if runner.opened != 1 {
				t.Fatalf("Reconcile accepted %s..%s but opened %d transactions (want 1): it "+
					"did not go on to load the books and the statement",
					tc.from, tc.to, runner.opened)
			}
		})
	}
}

// TestReconcileRefusesPartialMonthWindows covers the other half of the
// contract. A window inside a single month passes PeriodOf(from) ==
// PeriodOf(to), but the books side is still narrower than the statement side:
// the purchases outside the window vanish from 4(A) while the whole month's 2B
// is loaded, so their suppliers' documents surface as excess credit the
// operator is invited to chase — and the return under-reports eligible ITC.
func TestReconcileRefusesPartialMonthWindows(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
	}{
		{"starts on the 2nd", "2025-04-02", "2025-04-30"},
		{"stops on the 29th of a 30-day month", "2025-04-01", "2025-04-29"},
		{"stops on the 30th of a 31-day month", "2026-01-01", "2026-01-30"},
		{"stops on the 28th of a leap February", "2024-02-01", "2024-02-28"},
		{"a single mid-month day", "2025-04-15", "2025-04-15"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := &windowRunner{}
			svc := itc.NewService(runner)

			rep, err := svc.Reconcile(windowCtx(), tc.from, tc.to, windowOpts())
			if err == nil {
				t.Fatalf("UNDER-REPORTED CREDIT: Reconcile accepted the partial window %s..%s "+
					"and returned a report for period %q. The whole period's GSTR-2B is "+
					"loaded but only part of the period's purchases are, so the input tax on "+
					"every purchase outside the window is missing from 4(A) and its supplier's "+
					"document is reported as credit available in the portal but not in the "+
					"books. Reconcile must refuse a window that is not the whole month",
					tc.from, tc.to, rep.Period)
			}
			if !errors.Is(err, itc.ErrWindowNotOnePeriod) {
				t.Fatalf("the partial window %s..%s was rejected, but not with "+
					"ErrWindowNotOnePeriod, so a handler cannot tell it apart from a "+
					"database failure; got: %v", tc.from, tc.to, err)
			}
			if runner.opened != 0 {
				t.Fatalf("the refused partial window %s..%s still opened %d transaction(s)",
					tc.from, tc.to, runner.opened)
			}
		})
	}
}

// TestDecisionsInheritsTheOnePeriodContract pins the adapter gstr calls. gstr's
// ITCSource interface is Decisions(ctx, from, to) and it forwards the window
// straight to Reconcile, so a quarterly GSTR-3B request must fail loudly at
// this boundary rather than come back with eleven twelfths of the year's credit
// reversed in 4(B)(2).
func TestDecisionsInheritsTheOnePeriodContract(t *testing.T) {
	runner := &windowRunner{}
	svc := itc.NewService(runner)
	ctx := itc.WithAsOn(windowCtx(), time.Date(2025, 8, 1, 10, 0, 0, 0, itc.IST))

	decs, err := svc.Decisions(ctx, "2025-04-01", "2025-06-30")
	if err == nil {
		t.Fatalf("WRONG CREDIT REVERSED VIA GSTR-3B: Decisions accepted the quarterly window "+
			"2025-04-01..2025-06-30 and returned %d voucher decisions. Only 042025's GSTR-2B "+
			"is loaded, so May's and June's purchases come back reversed in full in 4(B)(2)",
			len(decs))
	}
	if !errors.Is(err, itc.ErrWindowNotOnePeriod) {
		t.Fatalf("Decisions rejected the quarterly window but not with ErrWindowNotOnePeriod, "+
			"so the GSTR-3B handler cannot report it as a bad request; got: %v", err)
	}
}
