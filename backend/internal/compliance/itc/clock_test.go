package itc

import (
	"testing"
	"time"
)

// mustTime parses an RFC3339 instant, failing the test if it is malformed. Every
// instant in this file is written with an EXPLICIT OFFSET so the test states the
// wall-clock reading AND the zone, and cannot silently start meaning something
// else if the process's TZ changes.
func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return v
}

// TestDeemedAcceptBoundary is the gate on the single most consequential instant
// in this package: the moment an un-actioned supplier document stops being
// "nobody has looked at this" and becomes claimed credit.
//
// It asserts the instant BEFORE and the instant AFTER, because a boundary test
// that only checks one side passes against a boundary that is off by a day.
func TestDeemedAcceptBoundary(t *testing.T) {
	cases := []struct {
		name   string
		period Period
		cutoff string
		before string
		after  string
	}{
		{
			name:   "jan_2026",
			period: Period{2026, time.January},
			cutoff: "2026-02-14T00:00:00+05:30",
			before: "2026-02-13T23:59:59.999999999+05:30",
			after:  "2026-02-14T00:00:00.000000000+05:30",
		},
		{
			// FEBRUARY IS THE STANDING TRAP IN DATE CODE. The 14th exists in
			// every month, so no clamping is needed here — this case is what
			// proves the implementation used calendar-month addition and not
			// "add 31 days".
			name:   "feb_2026_short_month",
			period: Period{2026, time.February},
			cutoff: "2026-03-14T00:00:00+05:30",
			before: "2026-03-13T23:59:59.999999999+05:30",
			after:  "2026-03-14T00:00:00.000000000+05:30",
		},
		{
			// YEAR ROLLOVER. Month 12 + 1 must be month 1 of the NEXT year, not
			// month 13 of this one.
			name:   "dec_2025_year_rollover",
			period: Period{2025, time.December},
			cutoff: "2026-01-14T00:00:00+05:30",
			before: "2026-01-13T23:59:59.999999999+05:30",
			after:  "2026-01-14T00:00:00.000000000+05:30",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DeemedAcceptCutoff(c.period)
			want := mustTime(t, c.cutoff)
			if !got.Equal(want) {
				t.Fatalf("cutoff for %s: got %s, want %s — a record with no IMS "+
					"action would deem-accept into the wrong tax period",
					c.period, got.Format(time.RFC3339Nano), want.Format(time.RFC3339Nano))
			}

			resolution := ResolutionInstant(c.period, nil)
			if s := EffectiveAction(nil, resolution, nil, mustTime(t, c.before)); s != NoAction {
				t.Fatalf("at %s (one nanosecond BEFORE the cutoff) state = %s, want %s: "+
					"a filer who acts on the 13th would find the record already "+
					"deem-accepted into period %s", c.before, s, NoAction, c.period)
			}
			if s := EffectiveAction(nil, resolution, nil, mustTime(t, c.after)); s != DeemedAccepted {
				t.Fatalf("at %s (the cutoff instant) state = %s, want %s: "+
					"credit for period %s would be withheld from a return that "+
					"is entitled to it", c.after, s, DeemedAccepted, c.period)
			}
		})
	}

	// -----------------------------------------------------------------
	// THE SUBTEST THAT BITES time.Local AND time.UTC.
	//
	// The very same two instants, written in UTC. 2026-02-14T00:00+05:30 IS
	// 2026-02-13T18:30Z — the same moment on the same timeline. Any
	// implementation that evaluates the boundary in UTC or in the server's local
	// zone answers these two DIFFERENTLY from the IST spellings above, because
	// it has moved the boundary by five and a half hours.
	// -----------------------------------------------------------------
	t.Run("utc_server_sees_the_same_instant", func(t *testing.T) {
		p := Period{2026, time.January}
		resolution := ResolutionInstant(p, nil)

		beforeUTC := mustTime(t, "2026-02-13T18:29:59.999999999Z")
		afterUTC := mustTime(t, "2026-02-13T18:30:00Z")

		// Sanity: these really are the same instants as the IST spellings.
		if !beforeUTC.Equal(mustTime(t, "2026-02-13T23:59:59.999999999+05:30")) {
			t.Fatalf("test is wrong: the UTC and IST spellings are not the same instant")
		}
		if !afterUTC.Equal(mustTime(t, "2026-02-14T00:00:00+05:30")) {
			t.Fatalf("test is wrong: the UTC and IST spellings are not the same instant")
		}

		if s := EffectiveAction(nil, resolution, nil, beforeUTC); s != NoAction {
			t.Fatalf("asOn %s (== 2026-02-13T23:59:59.999999999 IST) state = %s, want %s: "+
				"the deemed-acceptance boundary is NOT being evaluated in IST, so a "+
				"UTC-configured server deem-accepts records into tax period %s that "+
				"the portal has not yet resolved",
				beforeUTC.Format(time.RFC3339Nano), s, NoAction, p)
		}
		if s := EffectiveAction(nil, resolution, nil, afterUTC); s != DeemedAccepted {
			t.Fatalf("asOn %s (== 2026-02-14T00:00:00 IST) state = %s, want %s: "+
				"the deemed-acceptance boundary is NOT being evaluated in IST, so "+
				"credit for tax period %s is withheld for a further five and a half hours",
				afterUTC.Format(time.RFC3339Nano), s, DeemedAccepted, p)
		}

		// And the cutoff itself must BE that instant, stated as an absolute
		// moment rather than as a wall-clock reading. This is the assertion that
		// fails first when IST is replaced by time.UTC.
		if got := DeemedAcceptCutoff(p); !got.Equal(afterUTC) {
			t.Fatalf("DeemedAcceptCutoff(%s) = %s, want the instant %s: the zone the "+
				"boundary is computed in is wrong, and every record with no IMS action "+
				"resolves into the wrong tax period",
				p, got.Format(time.RFC3339Nano), afterUTC.Format(time.RFC3339Nano))
		}
	})

	// -----------------------------------------------------------------
	// THE SUBTEST THAT BITES A HARD-CODED 14.
	//
	// GSTR-2B for December 2024 was generated on 16 January 2025, because the
	// GSTR-1 due date for that period had been extended to the 13th. The 14th is
	// a default, not a statute, and the ingested statement's own gendt wins.
	// -----------------------------------------------------------------
	t.Run("generation_slipped", func(t *testing.T) {
		p := Period{2024, time.December}
		gen := mustTime(t, "2025-01-16T00:00:00+05:30")
		resolution := ResolutionInstant(p, &gen)

		if !resolution.Equal(gen) {
			t.Fatalf("ResolutionInstant with a statement gendt of 16-Jan-2025 returned %s, "+
				"want %s: the computed 14th is overriding the statement's own generation "+
				"date, so records deem-accept two days before the portal generated the 2B",
				resolution.Format(time.RFC3339Nano), gen.Format(time.RFC3339Nano))
		}

		early := mustTime(t, "2025-01-14T12:00:00+05:30")
		if s := EffectiveAction(nil, resolution, nil, early); s != NoAction {
			t.Fatalf("asOn %s state = %s, want %s: the 14th is hard-coded, so a Dec-2024 "+
				"record deem-accepts two days before GSTN generated the statement",
				early.Format(time.RFC3339Nano), s, NoAction)
		}
		onGen := mustTime(t, "2025-01-16T00:00:00+05:30")
		if s := EffectiveAction(nil, resolution, nil, onGen); s != DeemedAccepted {
			t.Fatalf("asOn %s state = %s, want %s", onGen.Format(time.RFC3339Nano), s, DeemedAccepted)
		}
	})
}

// TestEffectiveAction_LatestWinsAndFreezeStops covers the projection rules that
// are not about the cutoff: last-write-wins, RESET, and the filing freeze.
func TestEffectiveAction_LatestWinsAndFreezeStops(t *testing.T) {
	p := Period{2026, time.January}
	resolution := ResolutionInstant(p, nil)

	accept1 := Action{Kind: ActionAccept, ActedAt: mustTime(t, "2026-02-02T10:00:00+05:30")}
	reject := Action{Kind: ActionReject, ActedAt: mustTime(t, "2026-02-05T10:00:00+05:30")}
	accept2 := Action{Kind: ActionAccept, ActedAt: mustTime(t, "2026-02-09T10:00:00+05:30")}
	log := []Action{accept1, reject, accept2}

	asOn := mustTime(t, "2026-02-20T00:00:00+05:30")
	if s := EffectiveAction(log, resolution, nil, asOn); s != Accepted {
		t.Fatalf("latest-action-wins: got %s, want %s (GSTN IMS FAQ 12: the latest action overwrites the previous)", s, Accepted)
	}

	// As at a point BETWEEN the reject and the second accept, the reject stands.
	mid := mustTime(t, "2026-02-06T00:00:00+05:30")
	if s := EffectiveAction(log, resolution, nil, mid); s != Rejected {
		t.Fatalf("as at %s: got %s, want %s — an action taken AFTER asOn is being counted, "+
			"so recomputing an already-filed period would give a different answer than it did",
			mid.Format(time.RFC3339), s, Rejected)
	}

	// THE FREEZE. GSTR-3B filed on the 7th: the second accept, on the 9th, does
	// not count, and the reject stands forever.
	freeze := mustTime(t, "2026-02-07T00:00:00+05:30")
	if s := EffectiveAction(log, resolution, &freeze, asOn); s != Rejected {
		t.Fatalf("with the return filed at %s: got %s, want %s — an IMS action taken "+
			"AFTER the GSTR-3B was filed is being counted, so this software would "+
			"report a different table 4 than the return that was actually filed",
			freeze.Format(time.RFC3339), s, Rejected)
	}

	// RESET un-does; it does not accept. Before the cutoff a reset record is
	// genuinely un-actioned.
	resetLog := []Action{
		{Kind: ActionReject, ActedAt: mustTime(t, "2026-02-02T10:00:00+05:30")},
		{Kind: ActionReset, ActedAt: mustTime(t, "2026-02-03T10:00:00+05:30")},
	}
	early := mustTime(t, "2026-02-04T00:00:00+05:30")
	if s := EffectiveAction(resetLog, resolution, nil, early); s != NoAction {
		t.Fatalf("after a RESET before the cutoff: got %s, want %s", s, NoAction)
	}
	if s := EffectiveAction(resetLog, resolution, nil, asOn); s != DeemedAccepted {
		t.Fatalf("after a RESET, past the cutoff: got %s, want %s", s, DeemedAccepted)
	}

	// An unknown action kind must fail SAFE (un-actioned), never OPEN (accepted).
	unknown := []Action{{Kind: "PARK", ActedAt: mustTime(t, "2026-02-02T10:00:00+05:30")}}
	if s := EffectiveAction(unknown, resolution, nil, early); s != NoAction {
		t.Fatalf("unknown action kind: got %s, want %s — an action kind nobody has "+
			"implemented must not confer credit", s, NoAction)
	}
}

// TestSec16_4Expiry pins the financial-year boundary and the inclusive last day.
// Getting either wrong destroys or fabricates a whole year of credit, silently.
func TestSec16_4Expiry(t *testing.T) {
	day := func(s string) time.Time { return mustTime(t, s+"T00:00:00+05:30") }

	t.Run("fy_boundary", func(t *testing.T) {
		// 31 March 2026 is the LAST day of FY 2025-26 -> 30 November 2026.
		if got, want := Sec16_4Expiry(day("2026-03-31"), nil), day("2026-11-30"); !got.Equal(want) {
			t.Fatalf("31-Mar-2026 expiry: got %s, want %s — a March invoice would be "+
				"treated as belonging to the following financial year and stay claimable "+
				"a year too long", got.Format("2006-01-02"), want.Format("2006-01-02"))
		}
		// 1 April 2026 is the FIRST day of FY 2026-27 -> 30 November 2027.
		if got, want := Sec16_4Expiry(day("2026-04-01"), nil), day("2027-11-30"); !got.Equal(want) {
			t.Fatalf("01-Apr-2026 expiry: got %s, want %s — an April invoice would be "+
				"time-barred a full year early and its credit reversed",
				got.Format("2006-01-02"), want.Format("2006-01-02"))
		}
	})

	t.Run("last_day_is_inclusive", func(t *testing.T) {
		doc := day("2026-03-31")
		if Sec16_4Barred(doc, day("2026-11-30"), nil) {
			t.Fatalf("a claim ON 30-Nov-2026 was reported time-barred: the expiry day is " +
				"INCLUSIVE, and treating it as exclusive destroys a day's worth of " +
				"legitimate credit for every filer who claims on the last day")
		}
		if !Sec16_4Barred(doc, day("2026-12-01"), nil) {
			t.Fatalf("a claim on 01-Dec-2026 was NOT reported time-barred: credit that " +
				"has expired under s.16(4) would be claimed, which is exactly the " +
				"overclaim a department notice follows")
		}
	})

	t.Run("debit_note_is_delinked", func(t *testing.T) {
		// A debit note dated 10 April 2026 raised against a January 2026 invoice.
		// ITS OWN DATE fixes the financial year (delinked w.e.f. 01-01-2021), so
		// it expires on 30 November 2027 — not 30 November 2026, which is what
		// the underlying invoice's date would give.
		dn := day("2026-04-10")
		if got, want := Sec16_4Expiry(dn, nil), day("2027-11-30"); !got.Equal(want) {
			t.Fatalf("debit note dated 10-Apr-2026 against a Jan-2026 invoice: expiry "+
				"got %s, want %s — the note is being dated by its underlying invoice, "+
				"which is the pre-2021 rule and destroys a year of credit",
				got.Format("2006-01-02"), want.Format("2006-01-02"))
		}
		underlying := day("2026-01-15")
		if got := Sec16_4Expiry(underlying, nil); got.Equal(day("2027-11-30")) {
			t.Fatalf("the underlying Jan-2026 invoice must expire 30-Nov-2026, not %s",
				got.Format("2006-01-02"))
		}
	})

	t.Run("annual_return_brings_it_forward", func(t *testing.T) {
		doc := day("2026-03-31")
		filed := day("2026-09-15")
		if got, want := Sec16_4Expiry(doc, &filed), filed; !got.Equal(want) {
			t.Fatalf("with the annual return filed on 15-Sep-2026: got %s, want %s — "+
				"s.16(4) is the EARLIER of 30 November and the annual-return date",
				got.Format("2006-01-02"), want.Format("2006-01-02"))
		}
		late := day("2027-01-01")
		if got, want := Sec16_4Expiry(doc, &late), day("2026-11-30"); !got.Equal(want) {
			t.Fatalf("an annual return filed AFTER 30-Nov must not extend the expiry: got %s, want %s",
				got.Format("2006-01-02"), want.Format("2006-01-02"))
		}
	})
}

// TestSec16_5AmnestyRevivesTheFourFinancialYears is the gate on the credit
// Parliament put back.
//
// s.16(5) CGST Act, inserted by the Finance (No. 2) Act 2024 with retrospective
// effect from 01-07-2017, entitles a registered person to take ITC on an invoice
// or debit note pertaining to FY 2017-18, 2018-19, 2019-20 or 2020-21 in ANY
// return under s.39 filed up to 30 November 2021, "notwithstanding anything
// contained in sub-section (4)".
//
// Software that applies the bare s.16(4) rule to those years reports the credit
// as extinguished YEARS before the statute says it dies — 30-Nov-2018 for an
// FY 2017-18 invoice, where the answer is 30-Nov-2021. Every rupee of the
// difference is destroyed silently, on exactly the population that buys
// migration software: a filer bringing historical books onto a new system.
func TestSec16_5AmnestyRevivesTheFourFinancialYears(t *testing.T) {
	day := func(s string) time.Time { return mustTime(t, s+"T00:00:00+05:30") }
	amnesty := day("2021-11-30")

	// One document in each of the four covered financial years, with the expiry
	// the BARE s.16(4) rule would have produced, so the failure message can name
	// the years of credit at stake.
	for _, c := range []struct {
		fy      string
		docDate string
		bare    string
	}{
		{"2017-18", "2017-09-10", "2018-11-30"},
		{"2018-19", "2018-09-10", "2019-11-30"},
		{"2019-20", "2019-06-10", "2020-11-30"},
		{"2020-21", "2021-03-31", "2021-11-30"},
	} {
		t.Run("fy_"+c.fy, func(t *testing.T) {
			got := Sec16_4Expiry(day(c.docDate), nil)
			if !got.Equal(amnesty) {
				t.Fatalf("an invoice dated %s pertains to FY %s, which s.16(5) covers: its "+
					"expiry is %s, want 30-Nov-2021. This software is applying the bare "+
					"s.16(4) date of %s and extinguishing the credit %s early. Parliament "+
					"revived this credit RETROSPECTIVELY by the Finance (No. 2) Act 2024 "+
					"and this destroys it silently",
					c.docDate, c.fy, got.Format("2006-01-02"), c.bare,
					amnesty.Sub(day(c.bare)).String())
			}
			// And the bar itself must not fire anywhere inside the window.
			if Sec16_4Barred(day(c.docDate), amnesty, nil) {
				t.Fatalf("a FY %s invoice claimed ON 30-Nov-2021 — the last day s.16(5) "+
					"names — was reported time-barred. The amnesty day is INCLUSIVE, and "+
					"treating it as exclusive destroys the credit of every filer who "+
					"regularised on the final day", c.fy)
			}
			if !Sec16_4Barred(day(c.docDate), day("2021-12-01"), nil) {
				t.Fatalf("a FY %s invoice claimed on 01-Dec-2021 was NOT reported "+
					"time-barred: s.16(5) ends on 30 November 2021 and claiming after it "+
					"is a straight over-claim", c.fy)
			}
		})
	}

	// s.16(5) opens "Notwithstanding anything contained in sub-section (4)", so it
	// displaces the ANNUAL-RETURN limb as well as the 30-November one. Reading it
	// as merely another "whichever is earlier" test extinguishes the credit on the
	// GSTR-9 date, which for these years is long before 30-Nov-2021.
	t.Run("annual_return_does_not_cut_it_short", func(t *testing.T) {
		// The annual-return date must be BEFORE the ordinary 30-November, or the
		// limb never engages and this subtest asserts nothing. FY 2018-19's
		// ordinary date is 30-Nov-2019, so the GSTR-9 is dated 30-Jun-2019.
		doc := day("2018-09-10") // FY 2018-19
		filed := day("2019-06-30")
		if got, ordinary := Sec16_4Expiry(doc, &filed), day("2019-11-30"); !filed.Before(ordinary) {
			t.Fatalf("fixture is inert: the annual-return date %s is not before the ordinary "+
				"s.16(4) date %s, so the limb this subtest exists to override never fires",
				filed.Format("2006-01-02"), ordinary.Format("2006-01-02"))
		} else if !got.Equal(amnesty) {
			t.Fatalf("an FY 2018-19 invoice whose annual return was furnished on "+
				"30-Jun-2019 expires on %s, want 30-Nov-2021. s.16(5) applies "+
				"NOTWITHSTANDING the whole of s.16(4) — the annual-return cut-off "+
				"included — so treating it as one more 'whichever is earlier' limb "+
				"destroys nearly two and a half years of revived credit",
				got.Format("2006-01-02"))
		}
	})
}

// TestSec16_5DoesNotReachBeyondTheFourFinancialYears is the other half: an
// amnesty applied too widely is an OVER-claim, and an over-claim is recovery of
// the credit, s.50(3) interest and a s.73/74 notice.
//
// The statute names four years and only four. FY 2016-17 is one year BEFORE the
// window and FY 2021-22 is one year after it.
func TestSec16_5DoesNotReachBeyondTheFourFinancialYears(t *testing.T) {
	day := func(s string) time.Time { return mustTime(t, s+"T00:00:00+05:30") }

	t.Run("fy_2016_17_is_outside_the_window", func(t *testing.T) {
		doc := day("2017-03-31") // the LAST day of FY 2016-17
		if got, want := Sec16_4Expiry(doc, nil), day("2017-11-30"); !got.Equal(want) {
			t.Fatalf("an invoice dated 31-Mar-2017 pertains to FY 2016-17, which s.16(5) "+
				"does NOT name: expiry %s, want 2017-11-30. The amnesty window has been "+
				"opened a year too wide and this credit would be claimed four years after "+
				"it died", got.Format("2006-01-02"))
		}
		if !Sec16_4Barred(doc, day("2021-08-01"), nil) {
			t.Fatalf("an FY 2016-17 invoice claimed on 01-Aug-2021 was reported as still " +
				"claimable. s.16(5) begins at FY 2017-18; extending it back one year lets " +
				"a filer take credit the statute extinguished on 30-Nov-2017 — recovery, " +
				"interest under s.50(3) and a notice under s.73/74")
		}
	})

	t.Run("fy_2021_22_is_barred_normally", func(t *testing.T) {
		doc := day("2021-09-10") // FY 2021-22
		if got, want := Sec16_4Expiry(doc, nil), day("2022-11-30"); !got.Equal(want) {
			t.Fatalf("an invoice dated 10-Sep-2021 pertains to FY 2021-22, the first year "+
				"OUTSIDE the s.16(5) window: expiry %s, want 2022-11-30. The ordinary "+
				"s.16(4) rule must still be the whole answer here",
				got.Format("2006-01-02"))
		}
		if Sec16_4Barred(doc, day("2022-06-01"), nil) {
			t.Fatalf("an FY 2021-22 invoice claimed on 01-Jun-2022 was reported time-barred. " +
				"The amnesty date has been applied as a CEILING to a year it does not " +
				"cover, and a full year of perfectly good credit is being thrown away")
		}
		if !Sec16_4Barred(doc, day("2022-12-01"), nil) {
			t.Fatalf("an FY 2021-22 invoice claimed on 01-Dec-2022 was NOT reported " +
				"time-barred: the ordinary s.16(4) bar of 30-Nov-2022 must still fire, and " +
				"the amnesty must not be over-broad enough to keep it alive")
		}
	})

	// Sec16_5Applies is the predicate the expiry rests on; pin its two boundaries
	// directly so a future caller cannot get them from anywhere else.
	t.Run("predicate_boundaries", func(t *testing.T) {
		for _, c := range []struct {
			date string
			want bool
		}{
			{"2017-03-31", false}, // last day of FY 2016-17
			{"2017-04-01", true},  // first day of FY 2017-18
			{"2021-03-31", true},  // last day of FY 2020-21
			{"2021-04-01", false}, // first day of FY 2021-22
		} {
			if got := Sec16_5Applies(day(c.date)); got != c.want {
				t.Fatalf("Sec16_5Applies(%s) = %v, want %v — the amnesty window is off by "+
					"a financial year at this boundary, which either destroys a year of "+
					"revived credit or claims a year the statute never gave back",
					c.date, got, c.want)
			}
		}
	})
}

// TestPendingAllowedIsEffectiveDated pins the October-2025 change. A constant
// answer here is wrong on one side of that date or the other.
func TestPendingAllowedIsEffectiveDated(t *testing.T) {
	restricted := []PendingCase{
		PendingOriginalCreditNote,
		PendingUpwardAmendmentOfCN,
		PendingDownwardAmendmentOfRejectedCN,
		PendingDownwardAmendmentOfAcceptedDoc,
	}

	sep := Period{2025, time.September}
	for _, c := range restricted {
		if allowed, _ := PendingAllowed(sep, c); allowed {
			t.Fatalf("PENDING was permitted on %s in the Sep-2025 tax period: before "+
				"October 2025 the portal refuses it (IMS advisory Flow(3)/FAQ 11), so "+
				"this software would record an action the portal never accepted", c)
		}
	}

	oct := Period{2025, time.October}
	for _, c := range restricted {
		allowed, expires := PendingAllowed(oct, c)
		if !allowed {
			t.Fatalf("PENDING was refused on %s in the Oct-2025 tax period: from that "+
				"period it is permitted for one tax period (GSTN Advisory 631 of "+
				"08-10-2025, CITED-SECONDARY), and refusing it blocks a legitimate action", c)
		}
		// Expiry is the GSTR-3B due date of the NEXT tax period: Nov-2025's 3B is
		// due 20-Dec-2025.
		want := mustTime(t, "2025-12-20T00:00:00+05:30")
		if !expires.Equal(want) {
			t.Fatalf("PENDING expiry for %s in Oct-2025: got %s, want %s",
				c, expires.Format(time.RFC3339), want.Format(time.RFC3339))
		}
	}

	// An ordinary record is unrestricted in every period, with no expiry.
	if allowed, exp := PendingAllowed(sep, PendingOrdinary); !allowed || !exp.IsZero() {
		t.Fatalf("an ordinary record: allowed=%v expiry=%v, want allowed with no expiry", allowed, exp)
	}
}

// TestPeriodArithmeticIsCalendarBased pins the one operation this package uses
// to move between tax periods.
func TestPeriodArithmeticIsCalendarBased(t *testing.T) {
	cases := []struct {
		in   Period
		n    int
		want Period
	}{
		{Period{2025, time.December}, 1, Period{2026, time.January}},
		{Period{2026, time.January}, -1, Period{2025, time.December}},
		{Period{2026, time.January}, 12, Period{2027, time.January}},
		{Period{2026, time.March}, -3, Period{2025, time.December}},
	}
	for _, c := range cases {
		if got := c.in.AddMonths(c.n); got != c.want {
			t.Fatalf("%s.AddMonths(%d) = %s, want %s", c.in, c.n, got, c.want)
		}
	}

	p, err := ParsePeriod("012026")
	if err != nil || p != (Period{2026, time.January}) {
		t.Fatalf("ParsePeriod(012026) = %v, %v", p, err)
	}
	if p.String() != "012026" {
		t.Fatalf("Period.String() = %q, want %q", p.String(), "012026")
	}
	for _, bad := range []string{"", "12026", "0132026", "132026", "002026", "ab2026"} {
		if _, err := ParsePeriod(bad); err == nil {
			t.Fatalf("ParsePeriod(%q) accepted a malformed period", bad)
		}
	}
}
