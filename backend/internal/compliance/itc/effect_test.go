package itc

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/khata/cored/internal/compliance/gstr"
)

// assertEffect compares one line's effect against the four expected buckets,
// stated as CGST figures (the fixtures are intra-state 9+9, so SGST always
// mirrors CGST and IGST is always zero).
func assertEffect(t *testing.T, l Line, avail, revRule, revOther, inelig string) {
	t.Helper()
	check := func(name string, got Tax, want string) {
		t.Helper()
		if !got.CGST.Equal(d(want)) {
			t.Fatalf("%s CGST = %s, want %s (line %s %s)", name, got.CGST, want, l.Status, describe(l))
		}
	}
	check("4(A)", l.Effect.Avail, avail)
	check("4(B)(1)", l.Effect.RevRule, revRule)
	check("4(B)(2)", l.Effect.RevOther, revOther)
	check("4(D)(2)", l.Effect.Inelig, inelig)
}

// ---------------------------------------------------------------------------
// THE DISTINCTION THIS PACKAGE EXISTS TO PROTECT
// ---------------------------------------------------------------------------

// TestNotComparableIsNotMissingIn2B is the most important test in this file.
//
// Every purchase posted before migration 0032 carries no supplier document
// number. There is no fact in those books about which supplier document the
// credit was claimed against. Classifying them MISSING_IN_2B would:
//
//   - tell the filer their supplier did not file, which is a LIE about a third
//     party; and
//   - under model (b) REVERSE the credit in 4(B)(2) — credit that is, as far as
//     anybody knows, perfectly good.
//
// NOT_COMPARABLE says the true thing ("this software cannot compare this
// voucher") and produces ZERO reversal.
func TestNotComparableIsNotMissingIn2B(t *testing.T) {
	p := testPolicy(t)

	legacy := booksInvoice(t, "INV-LEGACY", "2026-01-05", "10000.0000")
	legacy.SupplierDocNo = "" // posted before 0032 — nothing was recorded
	legacy.SupplierDocDate = time.Time{}

	lines := Match([]BooksRow{legacy}, nil, p)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %s", len(lines), dump(lines))
	}
	l := lines[0]

	if l.Status == MissingIn2B {
		t.Fatalf("a purchase with NO supplier document number was classified %s. "+
			"That statement is false — it asserts the supplier did not file, when in "+
			"truth this software never recorded which document was purchased against — "+
			"and under model (b) it REVERSES %s of good credit per head in 4(B)(2)",
			MissingIn2B, l.Books.Tax.CGST)
	}
	if l.Status != NotComparable {
		t.Fatalf("status %s, want %s: %s", l.Status, NotComparable, dump(lines))
	}
	if l.NotComparable != NoSupplierDocNo {
		t.Fatalf("reason %s, want %s", l.NotComparable, NoSupplierDocNo)
	}

	// FULL availment, ZERO reversal. The credit stands; the gap is reported.
	assertEffect(t, l, "900", "0", "0", "0")

	// And identity (I1) still holds for this line: 4(A) + 4(D) == what was posted.
	tot := Totalise(lines, p)
	if !tot.PostedTotal().CGST.Equal(d("900")) {
		t.Fatalf("(I1) broken: 4(A)+4(D) CGST = %s, posted = 900", tot.PostedTotal().CGST)
	}

	// The other NOT_COMPARABLE reasons, each with the same zero-reversal
	// treatment and a DIFFERENT reported reason, because each has a different
	// remedy.
	for _, c := range []struct {
		name   string
		mutate func(*BooksRow)
		want   NotComparableReason
	}{
		{"no_party_gstin", func(b *BooksRow) { b.PartyGSTIN = "" }, NoPartyGSTIN},
		{"no_gst_params", func(b *BooksRow) { b.HasGSTParams = false }, NoGSTParams},
		{"isd", func(b *BooksRow) { b.ITCEligibility = EligibilityISD }, ImportOrISD},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := booksInvoice(t, "INV-X", "2026-01-05", "10000.0000")
			c.mutate(&b)
			ls := Match([]BooksRow{b}, nil, p)
			if ls[0].Status != NotComparable || ls[0].NotComparable != c.want {
				t.Fatalf("got %s/%s, want %s/%s", ls[0].Status, ls[0].NotComparable, NotComparable, c.want)
			}
			if !ls[0].Effect.RevOther.IsZero() || !ls[0].Effect.RevRule.IsZero() {
				t.Fatalf("a NOT_COMPARABLE line reversed credit: %v / %v",
					ls[0].Effect.RevRule, ls[0].Effect.RevOther)
			}
		})
	}
}

// TestRCMIsNeverReversedForBeingUnmatched pins the reverse-charge carve-out.
//
// IMS excludes RCM supplies (FAQ 8/18) and RCM credit is SELF-ASSESSED, not
// supplier-filed, so an RCM purchase can NEVER appear in a 2B match. The naive
// rule "unmatched means reverse" therefore reverses ONE HUNDRED PERCENT of RCM
// ITC — for every taxpayer, every period, silently.
func TestRCMIsNeverReversedForBeingUnmatched(t *testing.T) {
	p := testPolicy(t)

	rcm := booksInvoice(t, "RCM-1", "2026-01-08", "4000.0000")
	rcm.ReverseCharge = true

	// No statement at all: the worst case for the naive rule.
	lines := Match([]BooksRow{rcm}, nil, p)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %s", len(lines), dump(lines))
	}
	l := lines[0]

	if l.Status != NotComparable || l.NotComparable != ReverseChargeReason {
		t.Fatalf("an RCM purchase classified %s/%s, want %s/%s — it is about to be "+
			"matched against a statement it can never appear in",
			l.Status, l.NotComparable, NotComparable, ReverseChargeReason)
	}
	if !l.Effect.RevOther.IsZero() {
		t.Fatalf("RCM input tax of %s per head was REVERSED in 4(B)(2) for being "+
			"unmatched. RCM credit is self-assessed and never appears in a 2B, so "+
			"this reverses 100%% of every taxpayer's RCM ITC",
			l.Effect.RevOther.CGST)
	}
	// It stays in 4(A), routed to the ISRC row, exactly as gstr3b.go already
	// puts it there.
	assertEffect(t, l, "360", "0", "0", "0")
	if !l.Effect.RCM {
		t.Fatalf("the RCM line was not routed to the ISRC row of 4(A)")
	}

	tot := Totalise(lines, p)
	if !tot.AvailISRC.CGST.Equal(d("360")) || !tot.AvailOTH.CGST.IsZero() {
		t.Fatalf("4(A) split: ISRC=%s OTH=%s, want 360 / 0", tot.AvailISRC.CGST, tot.AvailOTH.CGST)
	}

	// And even WITH an IMS rejection recorded against a statement row, an RCM
	// row is untouchable: IMSApplies refuses it.
	if IMSApplies(SectionB2B, true) {
		t.Fatalf("IMSApplies allowed an action on a reverse-charge row — GSTN IMS FAQ " +
			"8/18 say RCM invoices are not part of IMS")
	}
}

// TestRejectedDocumentsNeverEnterITC pins the docRejdata rule.
//
// Rows GSTN reports under docRejdata are VIEW-ONLY (IMS FAQ 24). They are
// records some supplier filed against this GSTIN that the system rejected.
// Summing them into ITC is a filing-grade overclaim.
func TestRejectedDocumentsNeverEnterITC(t *testing.T) {
	p := testPolicy(t)

	good := stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")
	bad := stmtInvoice(t, "2", "INV-REJ", "2026-01-06", "50000.0000")
	bad.Rejected = true

	books := []BooksRow{
		booksInvoice(t, "INV-1", "2026-01-05", "10000.0000"),
		// A books row that WOULD match the rejected statement row if the
		// rejected row were in the pool. It must not pair with it.
		booksInvoice(t, "INV-REJ", "2026-01-06", "50000.0000"),
	}

	lines := Match(books, []StatementRow{good, bad}, p)

	// A docRejdata row produces NO LINE AT ALL: a line is a reconciliation
	// verdict, and there is no verdict to reach about a record the system
	// refused. Above all it must never PAIR with a books row, because pairing
	// confirms credit against a document GSTN rejected.
	for _, l := range lines {
		if l.Statement != nil && l.Statement.Rejected {
			t.Fatalf("a docRejdata row produced a reconciliation line (%s, books=%v). It "+
				"is VIEW-ONLY (GSTN IMS FAQ 24)", l.Status, l.Books != nil)
		}
	}
	// It is REPORTED, not discarded — the operator needs to know a supplier
	// filed something against their GSTIN.
	rejected := RejectedRows([]StatementRow{good, bad})
	if len(rejected) != 1 || rejected[0].DocNo != "INV-REJ" {
		t.Fatalf("RejectedRows returned %d rows; the operator cannot see what was "+
			"reported against them", len(rejected))
	}

	// The books row it would have matched is treated as UNMATCHED — full
	// availment, full reversal — which is the honest answer: the supplier's
	// filing was rejected, so there is no supported credit.
	unsupported := findBooksLine(t, lines, "PU-INV-REJ")
	if unsupported.Status != MissingIn2B {
		t.Fatalf("the books row facing a rejected statement row is %s, want %s: %s",
			unsupported.Status, MissingIn2B, dump(lines))
	}
	assertEffect(t, unsupported, "4500", "0", "4500", "0")

	// TOTALS: nothing from the rejected row reaches any bucket. 4(A) is the two
	// books rows (900 + 4500) and nothing else.
	tot := Totalise(lines, p)
	if !tot.AvailOTH.CGST.Equal(d("5400")) {
		t.Fatalf("4(A) OTH CGST = %s, want 5400 (900 + 4500) — a rejected record has "+
			"leaked into ITC", tot.AvailOTH.CGST)
	}
	if !tot.Inelig.IsZero() {
		t.Fatalf("4(D) is %v, want zero", tot.Inelig)
	}
}

// ---------------------------------------------------------------------------
// The rest of the effect table, one subtest per row
// ---------------------------------------------------------------------------

func TestEffectTable(t *testing.T) {
	p := testPolicy(t)

	t.Run("missing_in_2b_positive_tax_is_reversed_in_full", func(t *testing.T) {
		lines := Match([]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")}, nil, p)
		if lines[0].Status != MissingIn2B {
			t.Fatalf("status %s, want %s", lines[0].Status, MissingIn2B)
		}
		assertEffect(t, lines[0], "900", "0", "900", "0")

		// (I1) still exact, and (I2) nets to zero: unsupported credit is claimed
		// in 4(A) and taken straight back out in 4(B)(2).
		tot := Totalise(lines, p)
		if !tot.PostedTotal().CGST.Equal(d("900")) {
			t.Fatalf("(I1): 4(A)+4(D) = %s, posted = 900", tot.PostedTotal().CGST)
		}
		if !tot.Net().CGST.IsZero() {
			t.Fatalf("(I2): 4(C) = %s, want 0", tot.Net().CGST)
		}
	})

	// THE NEGATIVE-TAX RULE. A supplier credit note the supplier has not yet
	// filed must NOT be reversed: reversing a negative ADDS credit, which is
	// exactly backwards, and the conservative direction for a missing credit
	// note is to KEEP the reduction we have already made.
	t.Run("missing_in_2b_negative_tax_is_never_reversed", func(t *testing.T) {
		cn := booksCreditNote(t, "CN-1", "2026-01-20", "2000.0000")
		lines := Match([]BooksRow{cn}, nil, p)
		if lines[0].Status != MissingIn2B {
			t.Fatalf("status %s, want %s", lines[0].Status, MissingIn2B)
		}
		if !lines[0].Effect.Avail.CGST.Equal(d("-180")) {
			t.Fatalf("4(A) CGST = %s, want -180 (the note reduces availment)",
				lines[0].Effect.Avail.CGST)
		}
		if !lines[0].Effect.RevOther.IsZero() {
			t.Fatalf("a supplier credit note absent from the statement produced a "+
				"4(B)(2) reversal of %s. Reversing a NEGATIVE tax ADDS credit — this "+
				"hands the filer back %s per head they had correctly given up",
				lines[0].Effect.RevOther.CGST, lines[0].Effect.RevOther.CGST.Abs())
		}
	})

	t.Run("blocked_17_5_is_availed_then_reversed_in_4B1_only_once", func(t *testing.T) {
		b := booksInvoice(t, "INV-CAR", "2026-01-05", "10000.0000")
		b.ITCEligibility = EligibilityBlocked
		b.BlockedClause = "17(5)(a) motor vehicle"

		// UNMATCHED as well as blocked — both 4(B)(1) and 4(B)(2) would apply if
		// precedence were not enforced.
		lines := Match([]BooksRow{b}, nil, p)
		assertEffect(t, lines[0], "900", "900", "0", "0")

		tot := Totalise(lines, p)
		if !tot.Net().CGST.IsZero() {
			t.Fatalf("4(C) = %s, want 0 — a blocked credit that is ALSO unmatched was "+
				"reversed TWICE, which halves the filer's working capital on a line "+
				"nobody can trace", tot.Net().CGST)
		}
	})

	t.Run("statement_verdict_N_excludes_from_4A_and_reports_in_4D", func(t *testing.T) {
		s := stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")
		s.ITCAvailable = false
		s.ITCUnavailableReason = "POS and supplier state are the same but recipient state is different"

		lines := Match([]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")}, []StatementRow{s}, p)

		// EXCLUDED FROM 4(A) ENTIRELY — it never was available — and reported in
		// 4(D)(2). This is what keeps (I1) exact: every posted rupee lands in
		// exactly one of 4(A) or 4(D).
		assertEffect(t, lines[0], "0", "0", "0", "900")

		tot := Totalise(lines, p)
		if !tot.PostedTotal().CGST.Equal(d("900")) {
			t.Fatalf("(I1) broken: 4(A)+4(D) = %s, posted = 900", tot.PostedTotal().CGST)
		}
		// 4(D) is NOT subtracted from 4(C): in the post-Notification-14/2022
		// format 4(D) is "Other Details" and informational. Subtracting it as
		// well would double-count the haircut.
		if !tot.Net().CGST.IsZero() {
			t.Fatalf("4(C) = %s, want 0", tot.Net().CGST)
		}

		// The disagreement between GSTN's verdict and ours is REPORTED.
		found := false
		for _, f := range lines[0].Findings {
			if f.Code == FindingGSTNVerdictDisagrees {
				found = true
			}
		}
		if !found {
			t.Fatalf("GSTN said itcavl=N while our own s.16(4) computation found nothing "+
				"wrong, and the disagreement was not reported: findings %v", lines[0].Findings)
		}
	})

	t.Run("our_own_16_4_time_bar_excludes_from_4A", func(t *testing.T) {
		// A FY2024-25 invoice (expires 30-Nov-2025), claimed as at 01-Mar-2026.
		b := booksInvoice(t, "INV-OLD", "2024-06-10", "10000.0000")
		lines := Match([]BooksRow{b}, nil, p)

		assertEffect(t, lines[0], "0", "0", "0", "900")

		tot := Totalise(lines, p)
		if !tot.PostedTotal().CGST.Equal(d("900")) {
			t.Fatalf("(I1) broken on a time-barred document: 4(A)+4(D) = %s, posted = 900",
				tot.PostedTotal().CGST)
		}
	})

	t.Run("ims_rejected_or_pending_reverses_in_full", func(t *testing.T) {
		for _, kind := range []string{ActionReject, ActionPending} {
			s := stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")
			pol := testPolicy(t)
			pol.Actions = map[uuid.UUID][]Action{
				s.ID: {{Kind: kind, ActedAt: mustTime(t, "2026-02-10T10:00:00+05:30")}},
			}
			lines := Match([]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")},
				[]StatementRow{s}, pol)
			if lines[0].Status != Matched {
				t.Fatalf("%s: the line should still MATCH; IMS is about availability, not identity", kind)
			}
			assertEffect(t, lines[0], "900", "0", "900", "0")
		}
	})

	t.Run("ims_accepted_reverses_nothing", func(t *testing.T) {
		s := stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")
		pol := testPolicy(t)
		pol.Actions = map[uuid.UUID][]Action{
			s.ID: {{Kind: ActionAccept, ActedAt: mustTime(t, "2026-02-10T10:00:00+05:30")}},
		}
		lines := Match([]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")},
			[]StatementRow{s}, pol)
		assertEffect(t, lines[0], "900", "0", "0", "0")
		if lines[0].IMSState != Accepted {
			t.Fatalf("IMS state %s, want %s", lines[0].IMSState, Accepted)
		}
	})

	t.Run("ims_action_after_the_return_was_filed_is_ignored", func(t *testing.T) {
		s := stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")
		freeze := mustTime(t, "2026-02-18T09:00:00+05:30")
		pol := testPolicy(t)
		pol.Freeze = &freeze
		pol.Actions = map[uuid.UUID][]Action{
			// A rejection recorded AFTER the 3B was filed.
			s.ID: {{Kind: ActionReject, ActedAt: mustTime(t, "2026-02-20T10:00:00+05:30")}},
		}
		lines := Match([]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")},
			[]StatementRow{s}, pol)
		if lines[0].IMSState != DeemedAccepted {
			t.Fatalf("IMS state %s, want %s — an action taken after the GSTR-3B was "+
				"filed is being counted, so this software reports a table 4 that "+
				"differs from the return actually filed", lines[0].IMSState, DeemedAccepted)
		}
		assertEffect(t, lines[0], "900", "0", "0", "0")
	})

	t.Run("statement_only_row_contributes_nothing_to_4A", func(t *testing.T) {
		lines := Match(nil, []StatementRow{stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")}, p)
		if lines[0].Status != MissingInBooks {
			t.Fatalf("status %s, want %s", lines[0].Status, MissingInBooks)
		}
		assertEffect(t, lines[0], "0", "0", "0", "0")
		// Under model (b) 4(A) is what the BOOKS posted. A document we never
		// recorded posted nothing, so it moves no figure — it is reported so the
		// filer chases the missing purchase.
	})

	t.Run("amendment_supersedes_the_original", func(t *testing.T) {
		orig := stmtInvoice(t, "1", "INV-5", "2026-01-05", "10000.0000")
		amd := stmtInvoice(t, "2", "INV-5R", "2026-01-05", "12000.0000")
		amd.Section = SectionB2BA
		amd.OrigDocNo = "INV-5"
		amd.OrigDocDate = day(t, "2026-01-05")

		lines := Match(nil, []StatementRow{orig, amd}, p)
		sup := findStmtLine(t, lines, "INV-5", DocInvoice)
		if sup.Status != Superseded {
			t.Fatalf("the amended ORIGINAL is %s, want %s — counting both the original "+
				"and its amendment double-counts the supply (IMS advisory kp7 / FAQ 13)",
				sup.Status, Superseded)
		}
		if !sup.Effect.Avail.IsZero() {
			t.Fatalf("a superseded original contributed %v to 4(A)", sup.Effect.Avail)
		}
	})
}

// TestRule4243IsNotComputedHereAndSaysSo pins the boundary: this package does
// NOT implement the apportionment formula (compliance/gstr owns it — a formula
// stated twice is a formula that drifts), and when the caller has not supplied
// the inputs, the fact is DISCLOSED rather than silently treated as zero.
//
// An engine that quietly substituted E = 0 would return D1 = 0 for every
// taxpayer and look like it worked. That is the confidently-wrong-zero failure
// mode, and the disclosure is what makes it visible.
func TestRule4243IsNotComputedHereAndSaysSo(t *testing.T) {
	p := testPolicy(t)
	lines := Match([]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")},
		[]StatementRow{stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")}, p)

	tot := Totalise(lines, p)
	if !tot.RevRule.IsZero() {
		t.Fatalf("Totalise added a rule 42/43 component of %v. This package must not "+
			"compute the apportionment: compliance/gstr owns the formula, and two "+
			"implementations produce two reversal figures for one taxpayer with "+
			"nothing to say which is right", tot.RevRule)
	}

	disc := Disclosures(lines, true, p, nil)
	if !contains(disc, DiscRule4243NotComputed) || !contains(disc, DiscNotFilingReady) {
		t.Fatalf("disclosures %v must contain %q and %q", disc, DiscRule4243NotComputed, DiscNotFilingReady)
	}

	p.Rule4243Supplied = true
	disc = Disclosures(lines, true, p, nil)
	if contains(disc, DiscRule4243NotComputed) {
		t.Fatalf("with the inputs supplied, %q must not be disclosed: %v",
			DiscRule4243NotComputed, disc)
	}
}

// TestNegative4CIsLegal pins that reversals exceeding availment are NOT clamped.
// A negative 4(C) is a real, legal outcome in a period of heavy purchase
// returns, and clamping it to zero would silently inflate the claim.
func TestNegative4CIsLegal(t *testing.T) {
	p := testPolicy(t)
	// One small matched invoice and one large blocked credit.
	small := booksInvoice(t, "INV-1", "2026-01-05", "1000.0000")
	blocked := booksInvoice(t, "INV-2", "2026-01-06", "50000.0000")
	blocked.ITCEligibility = EligibilityBlocked

	lines := Match([]BooksRow{small, blocked},
		[]StatementRow{
			stmtInvoice(t, "1", "INV-1", "2026-01-05", "1000.0000"),
			stmtInvoice(t, "2", "INV-2", "2026-01-06", "50000.0000"),
		}, p)
	tot := Totalise(lines, p)
	// 4(A) = 90 + 4500 = 4590; 4(B)(1) = 4500; 4(C) = 90. Now add a rule-42
	// reversal that exceeds it.
	// 4(A) = 90 + 4500 = 4590; 4(B)(1) = 4500. Now make the small invoice
	// unmatched too, so 4(B)(2) takes its 90 as well and the reversals exceed
	// availment on the SGST head.
	lines2 := Match([]BooksRow{small, blocked}, nil, p)
	tot2 := Totalise(lines2, p)
	if !tot2.Net().CGST.IsZero() {
		t.Fatalf("4(C) = %s, want 0", tot2.Net().CGST)
	}

	// And a period that is nothing but a purchase RETURN nets negative in 4(A)
	// and therefore in 4(C) — legal, and returned as-is.
	cn := booksCreditNote(t, "CN-1", "2026-01-20", "10000.0000")
	lines3 := Match([]BooksRow{cn}, []StatementRow{stmtCreditNote(t, "9", "CN-1", "2026-01-20", "10000.0000")}, p)
	tot3 := Totalise(lines3, p)
	if !tot3.Net().CGST.Equal(d("-900")) {
		t.Fatalf("4(C) = %s, want -900 — a negative net is LEGAL (reversals and credit "+
			"notes may exceed availment in a period) and clamping it to zero silently "+
			"inflates the claim", tot3.Net().CGST)
	}
	_ = tot
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestDisclosuresAreClosedSortedAndAlwaysFlagNotFilingReady pins the closed
// vocabulary. This field is what lets a frontend REFUSE to present the return as
// final, so it must be machine-readable and it must never be empty in this wave.
func TestDisclosuresAreClosedSortedAndAlwaysFlagNotFilingReady(t *testing.T) {
	p := testPolicy(t)
	lines := Match([]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")}, nil, p)

	got := Disclosures(lines, false, p, []string{DiscStatementPartial, DiscStatementPartial, ""})

	known := map[string]bool{
		DiscNoInwardStatement: true, DiscRule4243NotComputed: true,
		DiscEligibilityNotSet: true, Disc4D1ReclaimNotModel: true,
		DiscReversalsNotPosted: true, DiscStatementPartial: true,
		DiscCessNotModelled: true, DiscPortalSchemaUnverif: true,
		DiscStmtCreditNoteNotInBooks: true,
		DiscNotFilingReady:           true,
		DiscSec16_6NotModelled:       true,
	}
	for _, g := range got {
		if !known[g] {
			t.Fatalf("disclosure %q is outside the closed vocabulary", g)
		}
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] >= got[i] {
			t.Fatalf("disclosures are not sorted+deduplicated: %v", got)
		}
	}
	for _, want := range []string{DiscNoInwardStatement, DiscStatementPartial, DiscNotFilingReady,
		DiscReversalsNotPosted, Disc4D1ReclaimNotModel, DiscSec16_6NotModelled} {
		if !contains(got, want) {
			t.Fatalf("disclosures %v must contain %q", got, want)
		}
	}
}

// TestDisclosureVocabularyMatchesGSTR is the DRIFT GUARD on the one thing this
// package duplicates.
//
// The disclosure tokens are declared in BOTH compliance/gstr (which owns the
// closed vocabulary and REJECTS an unknown token, because a token the frontend
// does not recognise is a disclosure that will not be shown) and here (because
// effect.go must stay a pure leaf and cannot import gstr). Two declarations of
// one vocabulary is exactly the drift this codebase warns about, so it is tied
// down mechanically rather than by memory: every token this package can emit
// must be in gstr.KnownDisclosures, and every token gstr knows must be
// emittable here.
//
// This test file may import gstr freely — archtest's boundary walker skips
// _test.go files — which is what makes the guard possible without giving the
// production package the edge.
//
// THE VOCABULARY IS IN TWO PARTS AND THE SPLIT IS ASSERTED, NOT ASSUMED.
// `mirrored` is declared on both sides and must match gstr EXACTLY, both ways.
// `itcLocalPending` is for a token declared HERE ONLY, when adding it to
// gstr.KnownDisclosures is an edit the change that introduced it does not own.
// Each pending token is asserted to be (a) actually emitted, so it is not a dead
// constant, and (b) still absent from gstr — so that the day gstr gains it, THIS
// TEST FAILS and forces it up into `mirrored` instead of leaving two half-truths
// in the tree.
//
// THE PENDING HALF IS EMPTY TODAY, AND THAT IS THE FIX, NOT AN OVERSIGHT.
// `sec-16-6-revocation-not-modelled` sat there while the GSTR-3B handler quietly
// FILTERED it out of the disclosures it forwarded, so the one document the filer
// signs was the one place the s.16(6) gap could not be seen. It is now in
// gstr.KnownDisclosures and therefore in `mirrored`, covered by the bidirectional
// guard below. The mechanism stays for the next token that arrives before its
// gstr edit does.
func TestDisclosureVocabularyMatchesGSTR(t *testing.T) {
	mirrored := []string{
		DiscNoInwardStatement, DiscRule4243NotComputed, DiscEligibilityNotSet,
		Disc4D1ReclaimNotModel, DiscReversalsNotPosted, DiscStatementPartial,
		DiscCessNotModelled, DiscPortalSchemaUnverif, DiscStmtCreditNoteNotInBooks,
		DiscSec16_6NotModelled, DiscNotFilingReady,
	}
	// Declared in this package only, awaiting its gstr edit. Empty today.
	itcLocalPending := []string{}

	// DECLARED IN gstr ONLY, AND STRUCTURALLY UNREACHABLE FROM HERE.
	//
	// Every token above is about the INWARD side — a statement, an eligibility
	// decision, a reversal — which is what this package reconciles, so the two
	// vocabularies were identical and the guard below could demand that. The
	// first OUTWARD-side token breaks that assumption rather than the gate:
	// `negative-values-carried-forward` is raised by gstr's clampNegatives from
	// tables 3.1 and 3.2, and this package never sees an outward supply. Adding
	// it to `mirrored` would mean declaring a constant here that nothing here
	// can ever emit — a dead constant, which is exactly what the non-vacuity
	// check below exists to prevent.
	//
	// ⛔ THE GATE IS NOT WEAKENED BY THIS LIST: a new gstr token still fails
	// this test until somebody classifies it. What it stops being is a claim
	// that the two packages disclose the same things.
	gstrOnly := map[string]bool{
		gstr.DiscNegativeValuesCarriedForward: true,
	}

	theirs := map[string]bool{}
	for _, s := range gstr.KnownDisclosures {
		theirs[s] = true
	}
	for _, o := range mirrored {
		if !theirs[o] {
			t.Fatalf("itc emits the disclosure %q, which is NOT in gstr.KnownDisclosures. "+
				"gstr REJECTS an unknown token, so this reconciliation would make every "+
				"3B generation fail — or, worse, the token would be dropped and the "+
				"frontend would present a return as final that is not", o)
		}
	}
	mine := map[string]bool{}
	for _, o := range mirrored {
		mine[o] = true
	}
	for _, k := range gstr.KnownDisclosures {
		if !mine[k] && !gstrOnly[k] {
			t.Fatalf("gstr knows the disclosure %q and this package cannot emit it. The "+
				"vocabularies have drifted; a reason the return is not filing-ready has "+
				"no way to reach the field that says so. If the token is OUTWARD-side and "+
				"this package structurally cannot raise it, put it in gstrOnly and say why", k)
		}
	}
	// gstrOnly must name tokens that REALLY EXIST over there. A stale or
	// misspelled entry would silently excuse a genuine drift.
	for k := range gstrOnly {
		if !theirs[k] {
			t.Fatalf("gstrOnly names %q, which is not in gstr.KnownDisclosures — the exemption "+
				"is stale and is now hiding whatever token replaced it", k)
		}
		if mine[k] {
			t.Fatalf("%q is in BOTH mirrored and gstrOnly; it cannot be unreachable here and "+
				"emitted here at the same time", k)
		}
	}

	// The pending half. Emitted here, not yet known there.
	emitted := map[string]bool{}
	for _, s := range Disclosures(nil, false, Policy{}, nil) {
		emitted[s] = true
	}
	// NON-VACUITY FOR THE TOKEN THAT JUST GRADUATED. `mirrored` proves only that
	// the two vocabularies agree on the spelling; it cannot tell a live token from
	// a dead constant. s.16(6) is disclosed UNCONDITIONALLY, so a reconciliation
	// over nothing at all must still emit it.
	if !emitted[DiscSec16_6NotModelled] {
		t.Fatalf("Disclosures() no longer emits %q. s.16(6) is still not computed — no table "+
			"in this schema holds the cancellation or revocation dates it turns on — so a "+
			"filer whose registration was cancelled and later restored under-claims credit "+
			"for those periods and pays tax they do not owe, with nothing on the return to "+
			"tell them to check by hand", DiscSec16_6NotModelled)
	}
	for _, o := range itcLocalPending {
		if !emitted[o] {
			t.Fatalf("%q is declared as an itc-local pending disclosure but Disclosures() "+
				"never emits it. A token nobody emits discloses nothing; the gap it names "+
				"is back to being silent", o)
		}
		if theirs[o] {
			t.Fatalf("gstr.KnownDisclosures now contains %q, so it is no longer "+
				"itc-local. MOVE IT from itcLocalPending into `mirrored` in this test — "+
				"leaving it here means the bidirectional drift guard above stops covering "+
				"it, and the two vocabularies can then diverge on it unnoticed", o)
		}
	}
}

// TestDecisionsFromLinesSatisfiesGSTRsStructuralRules is the gate on the ONE
// interface this package exposes to the return.
//
// gstr.checkITCDecision refuses a decision that breaks any of three identities,
// and each refusal is a whole 3B that will not generate. Asserting them here —
// against gstr's own types, over the full spread of verdicts — means the
// failure surfaces in this package's own suite rather than as an opaque
// errStaleITCDecision from a report handler.
func TestDecisionsFromLinesSatisfiesGSTRsStructuralRules(t *testing.T) {
	p := testPolicy(t)

	blocked := booksInvoice(t, "INV-BLK", "2026-01-06", "10000.0000")
	blocked.ITCEligibility = EligibilityBlocked
	rcm := booksInvoice(t, "RCM-1", "2026-01-08", "4000.0000")
	rcm.ReverseCharge = true
	legacy := booksInvoice(t, "INV-LEG", "2026-01-09", "3000.0000")
	legacy.SupplierDocNo = ""
	legacy.SupplierDocDate = time.Time{}
	cn := booksCreditNote(t, "CN-1", "2026-01-20", "2000.0000")
	barred := booksInvoice(t, "INV-OLD", "2024-06-10", "5000.0000")
	// The case that broke this seam: a supplier credit note we booked SMALLER
	// than the supplier declared. Its decision carries a POSITIVE 4(B)(2)
	// against a NEGATIVE 4(A), which is legal and which the restated rule (3)
	// below must therefore not refuse. See
	// TestCreditNoteDeclaredLargerAgreesWithGSTR.
	cnShort := booksCreditNote(t, "CN-2", "2026-01-21", "10000.0000")
	cnShort.Taxable = d("-3333.3333")
	cnShort.Tax = Tax{CGST: d("-300"), SGST: d("-300"), IGST: d("0")}
	cnShortStmt := stmtCreditNote(t, "4", "CN-2", "2026-01-21", "10000.0000")
	cnShortStmt.Taxable = d("4444.4444")
	cnShortStmt.Tax = Tax{CGST: d("400"), SGST: d("400"), IGST: d("0")}

	books := []BooksRow{
		booksInvoice(t, "INV-1", "2026-01-05", "10000.0000"), // matched
		blocked, rcm, legacy, cn, cnShort, barred,
		booksInvoice(t, "INV-GONE", "2026-01-11", "7000.0000"), // missing in 2B
	}
	stmt := []StatementRow{
		stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000"),
		stmtInvoice(t, "2", "INV-BLK", "2026-01-06", "10000.0000"),
		stmtInvoice(t, "3", "INV-ORPHAN", "2026-01-12", "1000.0000"), // missing in books
		cnShortStmt,
	}

	lines := Match(books, stmt, p)
	decs := decisionsFromLines(lines)

	if len(decs) != len(books) {
		t.Fatalf("got %d decisions for %d books rows — every voucher must carry a "+
			"decision, including the ones this software cannot compare, or gstr's "+
			"Available+Ineligible partition cannot hold", len(decs), len(books))
	}

	byID := map[uuid.UUID]BooksRow{}
	for _, b := range books {
		byID[b.VoucherID] = b
	}
	for id, dec := range decs {
		b, ok := byID[id]
		if !ok {
			t.Fatalf("a decision was emitted for an unknown voucher %s", id)
		}

		// (1) THE PARTITION: Available + Ineligible == the posted input tax.
		got := gstr.TaxTotals{
			CGST: dec.Available.CGST.Add(dec.Ineligible.CGST),
			SGST: dec.Available.SGST.Add(dec.Ineligible.SGST),
			IGST: dec.Available.IGST.Add(dec.Ineligible.IGST),
		}
		if !got.CGST.Equal(b.Tax.CGST) || !got.SGST.Equal(b.Tax.SGST) || !got.IGST.Equal(b.Tax.IGST) {
			t.Fatalf("voucher %s posted c=%s s=%s i=%s but the decision accounts for "+
				"c=%s s=%s i=%s — gstr.checkITCDecision refuses this and the whole 3B "+
				"fails to generate", b.VoucherNo,
				b.Tax.CGST, b.Tax.SGST, b.Tax.IGST, got.CGST, got.SGST, got.IGST)
		}

		// (2) AT MOST ONE 4(B) REVERSAL.
		rulesNonZero := !dec.ReversedRules.CGST.IsZero() || !dec.ReversedRules.SGST.IsZero() || !dec.ReversedRules.IGST.IsZero()
		otherNonZero := !dec.ReversedOther.CGST.IsZero() || !dec.ReversedOther.SGST.IsZero() || !dec.ReversedOther.IGST.IsZero()
		if rulesNonZero && otherNonZero {
			t.Fatalf("voucher %s is reversed under BOTH 4(B)(1) and 4(B)(2): the credit "+
				"is taken away twice", b.VoucherNo)
		}

		// (3) A REVERSAL IS NEVER NEGATIVE, AND — WHERE CREDIT WAS ACTUALLY
		//     AVAILED — NEVER EXCEEDS WHAT WAS AVAILED.
		//
		//     WHERE AVAILMENT IS NEGATIVE THERE IS NO UPPER BOUND, and that is
		//     not laxity: a supplier credit note the supplier declared LARGER
		//     than we booked carries a positive correction on a negative
		//     availment (books -300, statement -400, reversal 100, net -400),
		//     and its size comes from the statement. gstr's checkITCDecision
		//     rule (4) says exactly this; this restatement must not drift from
		//     it, or a decision this package considers correct is refused there
		//     and the whole 3B fails to generate.
		for _, h := range []struct {
			name       string
			rev, avail string
		}{
			{"CGST", dec.ReversedRules.CGST.Add(dec.ReversedOther.CGST).String(), dec.Available.CGST.String()},
			{"SGST", dec.ReversedRules.SGST.Add(dec.ReversedOther.SGST).String(), dec.Available.SGST.String()},
			{"IGST", dec.ReversedRules.IGST.Add(dec.ReversedOther.IGST).String(), dec.Available.IGST.String()},
		} {
			rev, avail := d(h.rev), d(h.avail)
			if rev.IsNegative() {
				t.Fatalf("voucher %s has a NEGATIVE %s reversal %s — reversing a negative "+
					"ADDS credit", b.VoucherNo, h.name, rev)
			}
			if avail.IsNegative() {
				continue
			}
			if rev.GreaterThan(avail) {
				t.Fatalf("voucher %s reverses %s of %s but only availed %s",
					b.VoucherNo, rev, h.name, avail)
			}
		}

		// (4) THE BUCKET IS ONE gstr RECOGNISES, and RCM goes to ISRC.
		switch dec.Bucket {
		case gstr.BucketISRC, gstr.BucketOTH:
		default:
			t.Fatalf("voucher %s carries bucket %q, which this software cannot justify "+
				"from the books", b.VoucherNo, dec.Bucket)
		}
		if b.ReverseCharge && dec.Bucket != gstr.BucketISRC {
			t.Fatalf("the RCM voucher %s went to bucket %q, want %q",
				b.VoucherNo, dec.Bucket, gstr.BucketISRC)
		}
		if dec.Reason == "" {
			t.Fatalf("voucher %s carries no reason; a reversal nobody can explain is a "+
				"reversal nobody can defend", b.VoucherNo)
		}
	}

	// The statement-only row produced NO decision — there is no voucher to
	// decide about.
	for _, l := range lines {
		if l.Books == nil && l.Statement != nil && l.Statement.DocNo == "INV-ORPHAN" {
			if l.Status != MissingInBooks {
				t.Fatalf("the orphan statement row is %s, want %s", l.Status, MissingInBooks)
			}
		}
	}
}

// TestCreditNoteDeclaredLargerAgreesWithGSTR is one half of a PAIR. Its twin is
// TestCreditNoteDeclaredLargerAgreesWithITC in internal/compliance/gstr, and the
// two assert the SAME rupee figures for the SAME case:
//
//	books      -300 per head (a supplier credit note we recorded)
//	statement  -400 per head (the note the supplier actually declared)
//	4(A)       -300   — the books figure, because 4(A) is model (b)
//	4(B)(2)    +100   — the correction down to the supplier's larger note
//	4(C)       -400   — which is the statement's figure
//
// WHY THE PAIR EXISTS. These two packages are one seam and they once disagreed
// about this exact case in silence: this package computed the +100 correction
// while gstr's checkITCDecision rule (4) capped every reversal at
// max(Available, 0) — zero on a credit note — and REFUSED the decision, so the
// whole GSTR-3B failed to generate. Neither suite went red, because neither
// suite had a mismatched credit note in it. One number, asserted on both sides,
// is the only thing that keeps the halves together.
//
// The two cases that must not be conflated are written out above itcEffect in
// effect.go: a note ABSENT from the statement is never reversed (case (a)), a
// note the supplier declared LARGER than we booked is corrected by the
// difference (case (b), this test).
func TestCreditNoteDeclaredLargerAgreesWithGSTR(t *testing.T) {
	p := testPolicy(t)

	// The books credit note: -300 per head, as store.go's sign convention
	// leaves it. Money is stated literally rather than derived from a rate, so
	// the pairing with the gstr test is legible at a glance.
	cn := booksCreditNote(t, "CN-B3", "2026-01-20", "10000.0000")
	cn.Taxable = d("-3333.3333")
	cn.Tax = Tax{CGST: d("-300"), SGST: d("-300"), IGST: d("0")}

	// The supplier's own note, declared LARGER: the statement reports
	// magnitudes, so 400 here is -400 in the books' direction.
	st := stmtCreditNote(t, "9", "CN-B3", "2026-01-20", "10000.0000")
	st.Taxable = d("4444.4444")
	st.Tax = Tax{CGST: d("400"), SGST: d("400"), IGST: d("0")}
	st.DocValue = d("5244.4444")

	lines := Match([]BooksRow{cn}, []StatementRow{st}, p)
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %s", len(lines), dump(lines))
	}
	l := lines[0]
	if l.Status != Mismatched {
		t.Fatalf("a books credit note of 300 against a supplier's 400 is %s, want %s — "+
			"the whole case turns on the two being PAIRED and the difference being "+
			"visible", l.Status, Mismatched)
	}

	if !l.Effect.Avail.CGST.Equal(d("-300")) {
		t.Fatalf("4(A) CGST = %s, want -300. 4(A) is the BOOKS figure under model (b); "+
			"putting the supplier's -400 here instead breaks the Available+Ineligible "+
			"partition gstr.checkITCDecision enforces against the posted input tax",
			l.Effect.Avail.CGST)
	}
	if !l.Effect.RevOther.CGST.Equal(d("100")) || !l.Effect.RevOther.SGST.Equal(d("100")) {
		t.Fatalf("4(B)(2) = c %s s %s, want 100 per head. The supplier declared a credit "+
			"note 100 per head LARGER than we booked, so 100 per head of credit has been "+
			"cancelled and must come out of the return; leaving it in over-claims 100 per "+
			"head against GSTR-2B. gstr and itc now disagree about a supplier credit note; "+
			"see TestCreditNoteDeclaredLargerAgreesWithITC in internal/compliance/gstr",
			l.Effect.RevOther.CGST, l.Effect.RevOther.SGST)
	}
	if !l.Effect.RevRule.IsZero() || !l.Effect.Inelig.IsZero() {
		t.Fatalf("the correction must ride in 4(B)(2) alone, got 4(B)(1) %s and 4(D)(2) %s",
			l.Effect.RevRule.CGST, l.Effect.Inelig.CGST)
	}

	tot := Totalise(lines, p)
	if !tot.Net().CGST.Equal(d("-400")) || !tot.Net().SGST.Equal(d("-400")) {
		t.Fatalf("4(C) = c %s s %s, want -400 per head — the net must equal the credit "+
			"note the supplier actually declared. -300 means the 100 correction never "+
			"reached the return and the filer over-claims 100 per head; -500 means it was "+
			"counted twice. gstr and itc now disagree about a supplier credit note; see "+
			"TestCreditNoteDeclaredLargerAgreesWithITC in internal/compliance/gstr",
			tot.Net().CGST, tot.Net().SGST)
	}
	// (I1) still exact: every posted rupee is in 4(A) or 4(D), and the
	// correction is NOT one of them.
	if !tot.PostedTotal().CGST.Equal(d("-300")) {
		t.Fatalf("(I1): 4(A)+4(D) = %s, posted = -300", tot.PostedTotal().CGST)
	}

	// AND THE DECISION THAT CROSSES THE SEAM CARRIES THOSE SAME FIGURES. This
	// is the value gstr's build3B receives, and it is what the twin test feeds
	// to build3B by hand.
	dec, ok := decisionsFromLines(lines)[cn.VoucherID]
	if !ok {
		t.Fatal("the credit note produced no decision at all — gstr's partition rule " +
			"cannot hold if a posted voucher is missing from the map")
	}
	if !dec.Available.CGST.Equal(d("-300")) || !dec.ReversedOther.CGST.Equal(d("100")) {
		t.Fatalf("the decision handed to gstr carries available %s / reversed %s, want "+
			"-300 / 100 — the two packages must be told the same numbers; see "+
			"TestCreditNoteDeclaredLargerAgreesWithITC in internal/compliance/gstr",
			dec.Available.CGST, dec.ReversedOther.CGST)
	}
}

// ---------------------------------------------------------------------------
// THE TWO CLOCKS: s.16(4) IS ABOUT WHEN THE CREDIT WAS CLAIMED, NOT WHEN THE
// REPORT WAS PRINTED
// ---------------------------------------------------------------------------

// TestFiledPeriodReportIsStableWhenRegeneratedYearsLater is the regression that
// pins Policy.ClaimOn apart from Policy.AsOn.
//
// s.16(4) bars a credit TAKEN after the 30 November following the document's
// financial year. Evaluated instead against the instant the REPORT IS
// GENERATED, every document in a past period becomes "time-barred" the moment
// that 30 November passes: 100% of the period's ITC leaves table 4(A) for
// 4(D)(2), 4(C) collapses to zero, and a paired row additionally raises
// FindingGSTNVerdictDisagrees against a GSTN verdict that has not changed. It
// happens to periods ALREADY FILED with the department — the same books, the
// same statement, a different and catastrophic answer, purely because time
// passed.
//
// Both policy shapes here are FILED periods — that is the whole scope of this
// test, and the two shapes are the two ways a filed period's claim instant is
// arrived at: stated Period (what the service will pass) and Period unset with
// a freeze (what the service passes today). An UNFILED period is deliberately
// NOT here: its answer is SUPPOSED to move once the bar passes, and that is
// pinned separately by TestUnfiledPeriodLosesCreditOnceTheSec16_4BarPasses.
func TestFiledPeriodReportIsStableWhenRegeneratedYearsLater(t *testing.T) {
	// May 2025. Its documents belong to FY 2025-26, so their s.16(4) expiry is
	// 30 November 2026 — long past by the time the report is regenerated.
	period := Period{Year: 2025, Month: time.May}
	filedAt := mustTime(t, "2025-06-20T14:30:00+05:30")
	// The day the return was filed, and three years and change later.
	dayOfFiling := mustTime(t, "2025-06-20T15:00:00+05:30")
	yearsLater := mustTime(t, "2028-09-01T10:00:00+05:30")

	// A matched pair (statement says itcavl = Y) and a books-only invoice.
	stmt := []StatementRow{stmtInvoice(t, "1", "INV-A", "2025-05-12", "10000.0000")}
	books := []BooksRow{
		booksInvoice(t, "INV-A", "2025-05-12", "10000.0000"),
		booksInvoice(t, "INV-B", "2025-05-20", "20000.0000"),
	}

	shapes := []struct {
		name   string
		policy func(asOn time.Time) Policy
	}{
		{"period_stated", func(asOn time.Time) Policy {
			return Policy{
				AsOn:       asOn,
				Period:     period,
				Resolution: ResolutionInstant(period, nil),
				Freeze:     &filedAt,
			}
		}},
		{"period_unset_freeze_present", func(asOn time.Time) Policy {
			// Exactly the literal service.go builds today: no Period field. The
			// freeze IS the claim instant, so the fix must hold without it.
			return Policy{
				AsOn:       asOn,
				Resolution: ResolutionInstant(period, nil),
				Freeze:     &filedAt,
			}
		}},
	}

	for _, sh := range shapes {
		t.Run(sh.name, func(t *testing.T) {
			atFiling := Totalise(Match(books, stmt, sh.policy(dayOfFiling)), sh.policy(dayOfFiling))
			laterLines := Match(books, stmt, sh.policy(yearsLater))
			later := Totalise(laterLines, sh.policy(yearsLater))

			// The figure the whole thing turns on: 4(A) "All other ITC".
			if !later.AvailOTH.CGST.Equal(atFiling.AvailOTH.CGST) ||
				!later.AvailOTH.SGST.Equal(atFiling.AvailOTH.SGST) {
				moved := atFiling.AvailOTH.CGST.Sub(later.AvailOTH.CGST).
					Add(atFiling.AvailOTH.SGST.Sub(later.AvailOTH.SGST))
				t.Fatalf("regenerating already-filed period %s on %s moved Rs %s of ITC "+
					"out of table 4(A) (was CGST %s + SGST %s, now CGST %s + SGST %s) — "+
					"an already-filed period's return must not change because time "+
					"passed; s.16(4) is measured from when the credit was CLAIMED, not "+
					"from when the report was generated",
					period, yearsLater.Format("2006-01-02"), moved,
					atFiling.AvailOTH.CGST, atFiling.AvailOTH.SGST,
					later.AvailOTH.CGST, later.AvailOTH.SGST)
			}

			// 4(D)(2): the bucket the credit wrongly lands in.
			if !later.Inelig.CGST.Equal(atFiling.Inelig.CGST) ||
				!later.Inelig.SGST.Equal(atFiling.Inelig.SGST) {
				t.Fatalf("regenerating already-filed period %s on %s moved Rs %s of ITC "+
					"into table 4(D)(2) 'ineligible under s.16(4)' that was not there "+
					"when the return was filed (4(D)(2) was CGST %s, is now CGST %s) — "+
					"the credit was claimed on %s, inside its s.16(4) window, and the "+
					"filed return says so",
					period, yearsLater.Format("2006-01-02"),
					later.Inelig.CGST.Sub(atFiling.Inelig.CGST).
						Add(later.Inelig.SGST.Sub(atFiling.Inelig.SGST)),
					atFiling.Inelig.CGST, later.Inelig.CGST, filedAt.Format("2006-01-02"))
			}

			// 4(C): net ITC available. This is the number that is actually
			// credited to the electronic credit ledger.
			if !later.Net().CGST.Equal(atFiling.Net().CGST) || !later.Net().SGST.Equal(atFiling.Net().SGST) {
				t.Fatalf("regenerating already-filed period %s on %s changed table 4(C) "+
					"net ITC from CGST %s / SGST %s to CGST %s / SGST %s — Rs %s of credit "+
					"the department has already received a return for vanished from an "+
					"already-filed period because the report was run later",
					period, yearsLater.Format("2006-01-02"),
					atFiling.Net().CGST, atFiling.Net().SGST, later.Net().CGST, later.Net().SGST,
					atFiling.Net().CGST.Sub(later.Net().CGST).
						Add(atFiling.Net().SGST.Sub(later.Net().SGST)))
			}

			// And the paired row must not start claiming GSTN is wrong.
			for _, l := range laterLines {
				if l.Statement == nil {
					continue
				}
				for _, f := range l.Findings {
					if f.Code == FindingGSTNVerdictDisagrees {
						t.Fatalf("regenerating already-filed period %s on %s raised %s on "+
							"document %s: this software now says the Rs %s of CGST on it is "+
							"time-barred while the 2B GSTN issued still says itcavl='Y'. "+
							"Nothing about the document changed — only the day the report "+
							"was run — and an already-filed period must not grow new "+
							"disagreements with the department over time",
							period, yearsLater.Format("2006-01-02"), FindingGSTNVerdictDisagrees,
							l.Statement.DocNo, l.Statement.Tax.CGST)
					}
				}
			}

			// The pinned figures, so the test cannot pass by both runs being
			// equally wrong: 4(A) is the books figure on both invoices
			// (900 + 1800 CGST), INV-B is unmatched and reversed in 4(B)(2)
			// (1800), and 4(C) is what is left (900).
			if !atFiling.AvailOTH.CGST.Equal(d("2700")) || !atFiling.Net().CGST.Equal(d("900")) ||
				!atFiling.Inelig.CGST.IsZero() {
				t.Fatalf("the day-of-filing baseline is not the expected return: 4(A) OTH "+
					"CGST %s (want 2700), 4(C) CGST %s (want 900), 4(D)(2) CGST %s (want 0)",
					atFiling.AvailOTH.CGST, atFiling.Net().CGST, atFiling.Inelig.CGST)
			}
		})
	}
}

// TestUnfiledPeriodLosesCreditOnceTheSec16_4BarPasses is the OTHER half of the
// two-clocks rule, and it is the half that guards against an OVER-claim.
//
// A period whose GSTR-3B has NEVER been furnished has not taken its ITC: under
// s.16(4) the credit is "taken" when the return is furnished, so the earliest
// instant this filer could now take it is today. If the claim instant were
// pinned to the period's own 3B due date — always about one month after the
// period, and therefore always inside the window — the time bar could NEVER
// fire for an unfiled return, and a filer catching up years late would be shown
// full credit on documents the statute has already extinguished.
//
// That population is exactly who s.16(4) exists to catch and exactly who buys
// catch-up software, and the consequence of telling them wrong is recovery of
// the credit, interest under s.50(3) and a s.73/74 notice.
//
// So: an unfiled period's answer MUST move as the bar passes. That is the
// statute operating, not the instability that
// TestFiledPeriodReportIsStableWhenRegeneratedYearsLater forbids — which is why
// only the FILED shapes live there.
func TestUnfiledPeriodLosesCreditOnceTheSec16_4BarPasses(t *testing.T) {
	// October 2024. Its documents belong to FY 2024-25, so their s.16(4) expiry
	// is 30 November 2025.
	period := Period{Year: 2024, Month: time.October}
	inWindow := mustTime(t, "2025-01-10T10:00:00+05:30")   // before the bar
	pastTheBar := mustTime(t, "2027-01-15T10:00:00+05:30") // 14 months after it

	stmt := []StatementRow{stmtInvoice(t, "1", "INV-A", "2024-10-15", "10000.0000")}
	books := []BooksRow{
		booksInvoice(t, "INV-A", "2024-10-15", "10000.0000"),
		booksInvoice(t, "INV-B", "2024-10-20", "20000.0000"),
	}
	// Never filed. Period is left unset on purpose so this also covers the
	// literal Policy service.go builds for an unfiled period, recovered from
	// Resolution.
	policy := func(asOn time.Time) Policy {
		return Policy{AsOn: asOn, Resolution: ResolutionInstant(period, nil)}
	}

	early := Totalise(Match(books, stmt, policy(inWindow)), policy(inWindow))
	if !early.AvailOTH.CGST.Equal(d("2700")) || !early.Net().CGST.Equal(d("900")) ||
		!early.Inelig.CGST.IsZero() {
		t.Fatalf("the inside-the-window baseline is not the expected return: 4(A) OTH "+
			"CGST %s (want 2700), 4(C) CGST %s (want 900), 4(D)(2) CGST %s (want 0) — "+
			"a credit claimed before 30 November 2025 is not time-barred and must be "+
			"claimable in full",
			early.AvailOTH.CGST, early.Net().CGST, early.Inelig.CGST)
	}

	lateLines := Match(books, stmt, policy(pastTheBar))
	late := Totalise(lateLines, policy(pastTheBar))

	// 4(A) must EMPTY. This is the assertion the old
	// `period_unset_not_yet_filed` subtest had backwards.
	if !late.AvailOTH.CGST.IsZero() || !late.AvailOTH.SGST.IsZero() {
		t.Fatalf("period %s has never been filed and is being reconciled on %s, "+
			"%d months after its s.16(4) bar of %s, yet table 4(A) 'All other ITC' "+
			"still carries CGST %s + SGST %s. This software is telling a filer who "+
			"is catching up late that Rs %s of credit the statute has EXTINGUISHED is "+
			"claimable. Claiming it means recovery of the credit, interest under "+
			"s.50(3), and a notice under s.73/74",
			period, pastTheBar.Format("2006-01-02"), 14,
			Sec16_4Expiry(mustTime(t, "2024-10-15T00:00:00+05:30"), nil).Format("2006-01-02"),
			late.AvailOTH.CGST, late.AvailOTH.SGST,
			late.AvailOTH.CGST.Add(late.AvailOTH.SGST))
	}

	// and the same rupees must APPEAR in 4(D)(2) "ineligible under s.16(4)".
	if !late.Inelig.CGST.Equal(d("2700")) || !late.Inelig.SGST.Equal(d("2700")) {
		t.Fatalf("period %s, unfiled and reconciled on %s past its s.16(4) bar, reports "+
			"table 4(D)(2) 'ITC ineligible under s.16(4)' as CGST %s / SGST %s, want "+
			"2700 / 2700. Time-barred credit must be DISCLOSED in 4(D)(2), not merely "+
			"dropped: the department reads 4(D)(2) to see the filer knew",
			period, pastTheBar.Format("2006-01-02"), late.Inelig.CGST, late.Inelig.SGST)
	}

	// 4(C) — what actually reaches the electronic credit ledger — must be nil.
	if !late.Net().CGST.IsZero() || !late.Net().SGST.IsZero() {
		t.Fatalf("period %s, unfiled and reconciled %s (past its s.16(4) bar), still "+
			"credits table 4(C) net ITC of CGST %s / SGST %s to the electronic credit "+
			"ledger. Rs %s of extinguished credit would be utilised against output tax",
			period, pastTheBar.Format("2006-01-02"), late.Net().CGST, late.Net().SGST,
			late.Net().CGST.Add(late.Net().SGST))
	}

	// And the paired row MUST now disagree with GSTN out loud: the 2B still says
	// itcavl='Y' (it was generated when the credit was live) while we say the bar
	// has since passed. The filer has to see that before signing.
	var sawDisagreement bool
	for _, l := range lateLines {
		if l.Statement == nil {
			continue
		}
		for _, f := range l.Findings {
			if f.Code == FindingGSTNVerdictDisagrees {
				sawDisagreement = true
			}
		}
	}
	if !sawDisagreement {
		t.Fatalf("the 2B row for INV-A says itcavl='Y' and this software says the credit "+
			"is time-barred on %s, and no %s finding was raised. A silent disagreement "+
			"with the department over Rs 900 of CGST is the one thing this reconciliation "+
			"must never do — whichever side is wrong, the filer needs to know before "+
			"they sign the return",
			pastTheBar.Format("2006-01-02"), FindingGSTNVerdictDisagrees)
	}
}

// TestSec16_5AmnestyKeepsHistoricalCreditOutOf4D2 is the END-TO-END gate on the
// revived credit, at the only place a filer can see it: GSTR-3B table 4.
//
// The population is migration. A filer bringing FY 2018-19 books onto this
// system, whose GSTR-3B for that period was furnished on 01-Aug-2021 — squarely
// inside the window s.16(5) opens — must keep every rupee in table 4(A). Under
// the bare s.16(4) rule the whole of it is swept into 4(D)(2) "ineligible", 4(C)
// collapses, and NOTHING in the report says a time bar was applied: no finding,
// no disclosure naming s.16(4) or s.16(5), nothing for a human to catch. That
// silence is the defect this test exists to keep closed.
func TestSec16_5AmnestyKeepsHistoricalCreditOutOf4D2(t *testing.T) {
	// September 2018 — FY 2018-19. Bare s.16(4) would expire this on 30-Nov-2019.
	period := Period{Year: 2018, Month: time.September}
	filed := mustTime(t, "2021-08-01T11:00:00+05:30")
	policy := Policy{
		// Reconciled TODAY, years later, which is what a migration does. AsOn must
		// not touch the bar — ClaimInstant is the filing instant.
		AsOn:       mustTime(t, "2026-03-01T10:00:00+05:30"),
		Period:     period,
		Resolution: ResolutionInstant(period, nil),
		Freeze:     &filed,
	}

	inv := booksInvoice(t, "INV-1819", "2018-09-10", "10000.0000")

	t.Run("matched_to_the_2b", func(t *testing.T) {
		lines := Match([]BooksRow{inv},
			[]StatementRow{stmtInvoice(t, "1", "INV-1819", "2018-09-10", "10000.0000")}, policy)
		tot := Totalise(lines, policy)

		if !tot.Inelig.CGST.IsZero() || !tot.Inelig.SGST.IsZero() {
			t.Fatalf("an FY 2018-19 invoice whose GSTR-3B was filed on 2021-08-01 — inside "+
				"the s.16(5) window — lost Rs %s CGST + Rs %s SGST to table 4(D)(2). "+
				"Parliament expressly revived this credit retrospectively (s.16(5) CGST "+
				"Act, Finance (No. 2) Act 2024, retrospective to 01-07-2017) and this "+
				"software is destroying it silently",
				tot.Inelig.CGST, tot.Inelig.SGST)
		}
		if !tot.AvailOTH.CGST.Equal(d("900")) || !tot.AvailOTH.SGST.Equal(d("900")) {
			t.Fatalf("table 4(A) 'All other ITC' is CGST %s / SGST %s, want 900 / 900 — the "+
				"whole of the revived credit belongs in 4(A) under model (b)",
				tot.AvailOTH.CGST, tot.AvailOTH.SGST)
		}
		if !tot.Net().CGST.Equal(d("900")) || !tot.Net().SGST.Equal(d("900")) {
			t.Fatalf("table 4(C) net ITC is CGST %s / SGST %s, want 900 / 900. Rs %s that "+
				"s.16(5) restored never reaches the electronic credit ledger, and the "+
				"filer pays it in cash",
				tot.Net().CGST, tot.Net().SGST,
				d("900").Add(d("900")).Sub(tot.Net().CGST).Sub(tot.Net().SGST))
		}
	})

	t.Run("no_statement_row_at_all", func(t *testing.T) {
		// The exact shape the finding was reproduced with. Being unmatched is a
		// 4(B)(2) reversal — recoverable the moment the supplier files — but it
		// must NOT be a 4(D)(2) extinction, which is not.
		lines := Match([]BooksRow{inv}, nil, policy)
		tot := Totalise(lines, policy)
		if !tot.Inelig.CGST.IsZero() {
			t.Fatalf("an FY 2018-19 invoice with no 2B row, 3B filed 2021-08-01, reports Rs "+
				"%s of CGST in table 4(D)(2) 'ineligible'. 4(D)(2) says the credit NEVER "+
				"WAS AVAILABLE, which s.16(5) says is false; the honest answer for an "+
				"unmatched document is a 4(B)(2) reversal that comes back when the "+
				"supplier files", tot.Inelig.CGST)
		}
		if !tot.AvailOTH.CGST.Equal(d("900")) {
			t.Fatalf("table 4(A) CGST %s, want 900", tot.AvailOTH.CGST)
		}
	})

	// The control: one financial year later, OUTSIDE the amnesty, the bar must
	// still empty 4(A) into 4(D)(2). Without this the fix could be "never bar
	// anything", which is the over-claim.
	t.Run("fy_2021_22_control_is_still_barred", func(t *testing.T) {
		ctlPeriod := Period{Year: 2021, Month: time.September}
		ctlFiled := mustTime(t, "2023-01-15T11:00:00+05:30") // past 30-Nov-2022
		ctl := Policy{
			AsOn:       mustTime(t, "2026-03-01T10:00:00+05:30"),
			Period:     ctlPeriod,
			Resolution: ResolutionInstant(ctlPeriod, nil),
			Freeze:     &ctlFiled,
		}
		lines := Match([]BooksRow{booksInvoice(t, "INV-2122", "2021-09-10", "10000.0000")},
			[]StatementRow{stmtInvoice(t, "2", "INV-2122", "2021-09-10", "10000.0000")}, ctl)
		tot := Totalise(lines, ctl)
		if !tot.Inelig.CGST.Equal(d("900")) || !tot.AvailOTH.CGST.IsZero() {
			t.Fatalf("an FY 2021-22 invoice whose 3B was filed on 15-Jan-2023 — after its "+
				"ordinary s.16(4) bar of 30-Nov-2022 and OUTSIDE the four years s.16(5) "+
				"names — reports 4(A) CGST %s / 4(D)(2) CGST %s, want 0 / 900. The "+
				"amnesty has been applied to a year Parliament did not revive, and Rs 900 "+
				"of extinguished credit would be utilised against output tax: recovery, "+
				"interest under s.50(3) and a notice under s.73/74",
				tot.AvailOTH.CGST, tot.Inelig.CGST)
		}
	})
}

// TestSec16_6IsDisclosedRatherThanSilentlyIgnored pins the OTHER half of the
// amnesty pair.
//
// s.16(6) revives credit for the period during which a registration stood
// cancelled, where the cancellation was later revoked. It is NOT implemented:
// the cancellation and revocation order dates it turns on are not in this
// schema, and guessing them would revive credit on registrations that were never
// cancelled. The rule this package holds itself to is that whatever is not
// implemented must produce a TOKEN, not silence — every other gap here has one,
// and an undisclosed gap is the inconsistency, not the gap.
func TestSec16_6IsDisclosedRatherThanSilentlyIgnored(t *testing.T) {
	p := testPolicy(t)
	// A completely ordinary, fully-matched, entirely un-remarkable reconciliation:
	// the token must be there even when nothing at all is wrong, because the gap
	// is unconditional.
	lines := Match([]BooksRow{booksInvoice(t, "INV-1", "2026-01-05", "10000.0000")},
		[]StatementRow{stmtInvoice(t, "1", "INV-1", "2026-01-05", "10000.0000")}, p)
	p.Rule4243Supplied = true

	disc := Disclosures(lines, true, p, nil)
	if !contains(disc, DiscSec16_6NotModelled) {
		t.Fatalf("the disclosure list %v does not contain %q. Section 16(6) restores ITC "+
			"for the period a cancelled registration was later restored for, and this "+
			"software does not model it at all — it holds no cancellation date, no "+
			"revocation order, nothing. A filer whose registration was cancelled and "+
			"revoked is silently under-claiming every rupee of that period's credit, "+
			"and nothing in the report tells them to check it by hand. Every other gap "+
			"in this package is disclosed; the inconsistency IS the defect",
			disc, DiscSec16_6NotModelled)
	}
	if !contains(disc, DiscNotFilingReady) {
		t.Fatalf("disclosures %v must carry %q whenever any other token is present",
			disc, DiscNotFilingReady)
	}
}

// TestClaimInstantNeverFallsBackToAsOn pins the derivation itself, in both
// directions:
//
//   - A FILED period's claim instant is NEVER the report instant. Otherwise
//     every credit in every filed period becomes time-barred the moment the
//     calendar passes its 30 November, and ITC the department has already
//     received a return for silently leaves table 4(A).
//   - An UNFILED period's claim instant is the LATER of its 3B due date and the
//     report instant, because nothing has been taken yet and the earliest it
//     could now be taken is today. Pinning it to the due date instead means the
//     bar can never fire and a late filer over-claims extinguished credit.
func TestClaimInstantNeverFallsBackToAsOn(t *testing.T) {
	period := Period{Year: 2025, Month: time.May}
	asOn := mustTime(t, "2028-09-01T10:00:00+05:30")
	dueDate := mustTime(t, "2025-06-20T00:00:00+05:30")
	filedAt := mustTime(t, "2025-06-20T14:30:00+05:30")
	early := mustTime(t, "2025-06-05T10:00:00+05:30") // report run BEFORE the due date

	cases := []struct {
		name string
		// filed says the claim instant must be immune to the report instant.
		filed  bool
		policy Policy
		want   time.Time
	}{
		{"explicit_claim_on_wins", true,
			Policy{AsOn: asOn, ClaimOn: filedAt, Period: period}, filedAt},
		{"freeze_is_the_claim_instant", true,
			Policy{AsOn: asOn, Period: period, Freeze: &filedAt}, filedAt},
		// Unfiled: the report instant is a FLOOR, so a late run moves the bar.
		{"unfiled_period_late_run_uses_the_report_instant", false,
			Policy{AsOn: asOn, Period: period}, asOn},
		{"unfiled_period_recovered_from_resolution", false,
			Policy{AsOn: asOn, Resolution: ResolutionInstant(period, nil)}, asOn},
		// ...but never EARLIER than the period's own 3B due date: a return run
		// on the 5th cannot have been furnished before it was due.
		{"unfiled_period_early_run_floors_at_the_3b_due_date", false,
			Policy{AsOn: early, Period: period}, dueDate},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.policy.ClaimInstant()
			if c.filed && got.Equal(asOn) {
				t.Fatalf("an already-FILED period's s.16(4) claim instant is the report "+
					"instant %s. Every credit in every filed period becomes time-barred as "+
					"soon as the calendar passes 30 November, and ITC on a return the "+
					"department already holds silently leaves table 4(A)",
					asOn.Format(time.RFC3339))
			}
			if !c.filed && got.Before(dueDate) {
				t.Fatalf("an UNFILED period's claim instant %s is before its own GSTR-3B due "+
					"date %s — the return cannot have been furnished before it was due, and "+
					"dating the claim early can only ever hide a s.16(4) bar",
					got.Format(time.RFC3339), dueDate.Format(time.RFC3339))
			}
			if !got.Equal(c.want) {
				t.Fatalf("claim instant %s, want %s: s.16(4) would be measured from the "+
					"wrong day and could bar (or fail to bar) a whole period's credit",
					got.Format(time.RFC3339), c.want.Format(time.RFC3339))
			}
		})
	}

	// With nothing to derive from, s.16(4) is NOT evaluated — it does not
	// silently become "now" and it does not silently become AsOn.
	if got := (Policy{AsOn: asOn}).ClaimInstant(); !got.IsZero() {
		t.Fatalf("a Policy carrying no period, no freeze and no claim instant produced "+
			"claim instant %s. s.16(4) must not be decided against a clock nobody "+
			"supplied; a whole period's credit turns on this day", got.Format(time.RFC3339))
	}
}

// ---------------------------------------------------------------------------
// THE BLOCKED PURCHASE THAT IS RETURNED
// ---------------------------------------------------------------------------

// TestBlockedPurchaseReturnedNetsToZero is the twin of
// TestBlockedPurchaseReturnedNetsToZeroAgreesWithITC in internal/compliance/gstr.
// Both assert the SAME rupee figures on the same fixture so the two halves of
// the 4(B)(1) seam cannot drift.
//
// A section 17(5) blocked purchase is availed in 4(A) and taken straight back
// out in 4(B)(1), netting ZERO — the taxpayer never gets the credit. When that
// purchase is RETURNED, the supplier's credit note posts NEGATIVE input tax, so
// 4(A) falls by the same amount. If 4(B)(1) does not fall with it, the return
// hands back a reversal that has nothing left to reverse and 4(C) goes NEGATIVE
// by the full tax on a car the taxpayer never claimed credit for.
func TestBlockedPurchaseReturnedNetsToZero(t *testing.T) {
	p := testPolicy(t)

	// Rs 10,00,000 motor vehicle at 18%: 90,000 CGST + 90,000 SGST.
	car := booksInvoice(t, "INV-CAR", "2026-01-05", "1000000.0000")
	car.ITCEligibility = EligibilityBlocked
	car.BlockedClause = "17(5)(a) motor vehicle"

	// Returned in full. store.go's sign convention leaves the note NEGATIVE.
	ret := booksCreditNote(t, "CN-CAR", "2026-01-25", "1000000.0000")
	ret.ITCEligibility = EligibilityBlocked
	ret.BlockedClause = "17(5)(a) motor vehicle"

	lines := Match(
		[]BooksRow{car, ret},
		[]StatementRow{
			stmtInvoice(t, "1", "INV-CAR", "2026-01-05", "1000000.0000"),
			stmtCreditNote(t, "2", "CN-CAR", "2026-01-25", "1000000.0000"),
		}, p)

	inv := findBooksLine(t, lines, "PU-INV-CAR")
	assertEffect(t, inv, "90000", "90000", "0", "0")

	cn := findBooksLine(t, lines, "DN-CN-CAR")
	if !cn.Effect.Avail.CGST.Equal(d("-90000")) {
		t.Fatalf("4(A) CGST on the credit note = %s, want -90000", cn.Effect.Avail.CGST)
	}
	if !cn.Effect.RevRule.CGST.Equal(d("-90000")) || !cn.Effect.RevRule.SGST.Equal(d("-90000")) {
		t.Fatalf("4(B)(1) on the RETURNED blocked purchase = c %s s %s, want -90000 per "+
			"head. The section 17(5) reversal must move WITH the credit it reverses: "+
			"clamping it at zero leaves a 90,000-per-head reversal standing against a "+
			"90,000-per-head reduction in availment, so the filer pays Rs 1,80,000 out "+
			"of the electronic cash ledger for a car they returned and never took "+
			"credit on. See TestBlockedPurchaseReturnedNetsToZeroAgreesWithITC in "+
			"internal/compliance/gstr",
			cn.Effect.RevRule.CGST, cn.Effect.RevRule.SGST)
	}

	tot := Totalise(lines, p)
	if !tot.Net().CGST.IsZero() || !tot.Net().SGST.IsZero() {
		t.Fatalf("4(C) = c %s s %s, want 0 per head. A blocked purchase bought and "+
			"returned in the same period has a NET ITC effect of exactly zero; %s per "+
			"head of real credit has been destroyed",
			tot.Net().CGST, tot.Net().SGST, tot.Net().CGST.Abs())
	}
	// (I1) is untouched: the two vouchers' posted input tax cancels, and every
	// posted rupee is still in exactly one of 4(A) or 4(D).
	if !tot.PostedTotal().CGST.IsZero() {
		t.Fatalf("(I1): 4(A)+4(D) = %s, posted = 0", tot.PostedTotal().CGST)
	}

	// And the decisions that cross the seam carry those same figures.
	decs := decisionsFromLines(lines)
	dinv, ok := decs[car.VoucherID]
	if !ok {
		t.Fatal("the blocked invoice produced no decision")
	}
	dcn, ok := decs[ret.VoucherID]
	if !ok {
		t.Fatal("the blocked credit note produced no decision")
	}
	if !dinv.ReversedRules.CGST.Equal(d("90000")) || !dcn.ReversedRules.CGST.Equal(d("-90000")) {
		t.Fatalf("the decisions handed to gstr carry 4(B)(1) invoice %s / note %s, want "+
			"90000 / -90000 — the two packages must be told the same numbers",
			dinv.ReversedRules.CGST, dcn.ReversedRules.CGST)
	}
}

// ---------------------------------------------------------------------------
// THE SUPPLIER CREDIT NOTE THAT IS NOT IN THE BOOKS
// ---------------------------------------------------------------------------

// TestStatementCreditNoteMissingFromBooksIsDisclosed pins the ASYMMETRY that
// otherwise sits silently in the return.
//
// A books invoice the supplier never filed is reversed in full in 4(B)(2). A
// STATEMENT credit note the books never recorded moves nothing at all — model
// (b) reports what the books posted, and nothing was posted. The two errors are
// not symmetric in consequence: the first REDUCES the claim, the second INFLATES
// it, because under IMS an un-actioned credit note deem-accepts and is already
// netted out of the recipient's own GSTR-2B. The department's auto-populated
// figure is therefore LOWER than this return's 4(C) by the whole of that note.
//
// Until the period-level adjustment is built (see the gap recorded in gstr3b.go),
// the minimum this software owes the filer is to SAY SO.
func TestStatementCreditNoteMissingFromBooksIsDisclosed(t *testing.T) {
	p := testPolicy(t)

	books := booksInvoice(t, "INV-1", "2026-01-05", "100000.0000")
	stmt := []StatementRow{
		stmtInvoice(t, "1", "INV-1", "2026-01-05", "100000.0000"),
		stmtCreditNote(t, "2", "CN-UNBOOKED", "2026-01-22", "20000.0000"),
	}

	lines := Match([]BooksRow{books}, stmt, p)

	var orphan *Line
	for i := range lines {
		if lines[i].Status == MissingInBooks {
			orphan = &lines[i]
		}
	}
	if orphan == nil {
		t.Fatalf("the unbooked supplier credit note did not surface as %s: %s",
			MissingInBooks, dump(lines))
	}

	found := false
	for _, f := range orphan.EffectFindings() {
		if f.Code == FindingStatementCreditNoteNotInBooks {
			found = true
		}
	}
	if !found {
		t.Fatalf("a supplier CREDIT NOTE of 1800 per head that is in GSTR-2B and not in "+
			"the books raised no %s finding. It contributes NOTHING to the return, so "+
			"4(C) here is 9000 per head where the department's own auto-populated "+
			"figure is 7200 — an over-claim of 1800 per head that nothing else in this "+
			"software reports", FindingStatementCreditNoteNotInBooks)
	}

	disc := Disclosures(lines, true, p, nil)
	if !contains(disc, DiscStmtCreditNoteNotInBooks) {
		t.Fatalf("disclosures %v do not carry %q. Without it a frontend can present "+
			"this return as filing-ready with a known 1800-per-head overstatement of "+
			"4(C) sitting in it", disc, DiscStmtCreditNoteNotInBooks)
	}

	// The plain invoice, by contrast, must NOT raise it — a finding raised on
	// every line is a finding nobody reads.
	inv := findBooksLine(t, lines, "PU-INV-1")
	for _, f := range inv.EffectFindings() {
		if f.Code == FindingStatementCreditNoteNotInBooks {
			t.Fatalf("the matched invoice raised %s", f.Code)
		}
	}

	// And a statement-only INVOICE must not raise it either: an unbooked
	// invoice understates the claim, which is the filer's own problem and not
	// an overstatement of the return.
	inv2 := Match(nil, []StatementRow{stmtInvoice(t, "3", "INV-NOBOOK", "2026-01-09", "5000.0000")}, p)
	for _, f := range inv2[0].EffectFindings() {
		if f.Code == FindingStatementCreditNoteNotInBooks {
			t.Fatalf("a statement-only INVOICE raised %s; only a credit note inflates "+
				"the claim by being absent from the books", f.Code)
		}
	}
	if contains(Disclosures(inv2, true, p, nil), DiscStmtCreditNoteNotInBooks) {
		t.Fatalf("a statement-only invoice must not raise %q", DiscStmtCreditNoteNotInBooks)
	}
}
