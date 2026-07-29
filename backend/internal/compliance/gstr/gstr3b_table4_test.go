package gstr

// gstr3b_table4_test.go is the PURE half of the GSTR-3B table-4 coverage: no
// database, no fixtures, no service. Everything here calls build3B or Validate
// directly, which is only possible from inside the package — and that is the
// point. The ITC seam's hardest properties (the exact bytes of an untouched
// return, the (I2) identity, the negative-reversal rule, the staleness guard)
// are all properties of the pure core, and a test that has to stand up a
// company to reach them is a test that runs a hundred times less often.
//
// The DB-backed half lives in gstr3b_integration_test.go (package gstr_test).

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func d4(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func tt(c, s, i string) TaxTotals {
	return TaxTotals{CGST: d4(c), SGST: d4(s), IGST: d4(i)}
}

func may2025() time.Time {
	t, err := time.Parse("2006-01-02", "2025-05-01")
	if err != nil {
		panic(err)
	}
	return t
}

// goldenRows is a fixed, representative set: an intra-state B2B sale, an
// inter-state sale to an unregistered party (which populates 3.2), one plain
// ITC purchase and one reverse-charge purchase (which populates 3.1(d) and
// ISRC). Every table of the return is non-trivial, so the pinned bytes below
// cover all of them.
func goldenRows() []row3b {
	return []row3b{
		{voucherID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), voucherNo: "S-1",
			class: "SALES", rawClass: "SALES", pos: "29",
			taxable: d4("10000"), cgst: d4("900"), sgst: d4("900"), igst: d4("0"), registered: true},
		{voucherID: uuid.MustParse("22222222-2222-2222-2222-222222222222"), voucherNo: "S-2",
			class: "SALES", rawClass: "SALES", pos: "27",
			taxable: d4("200000"), cgst: d4("0"), sgst: d4("0"), igst: d4("36000"), registered: false},
		{voucherID: uuid.MustParse("33333333-3333-3333-3333-333333333333"), voucherNo: "PU-1",
			class: "PURCHASE", rawClass: "PURCHASE", pos: "29",
			taxable: d4("2000"), cgst: d4("180"), sgst: d4("180"), igst: d4("0")},
		{voucherID: uuid.MustParse("44444444-4444-4444-4444-444444444444"), voucherNo: "PU-2",
			class: "PURCHASE", rawClass: "PURCHASE", pos: "29", reverse: true,
			taxable: d4("4000"), cgst: d4("360"), sgst: d4("360"), igst: d4("0")},
	}
}

// preITCJSON is the EXACT output of build3B for goldenRows() as the code stood
// BEFORE the ITC seam was opened — captured from the pre-change binary, not
// re-derived from the post-change one, which is the only way a golden of this
// kind is worth anything.
//
// WHY IT IS PINNED. Opening the ITC seam touched the SQL projection, the row
// struct, every table-4 accumulator and the shape of Table4ITC. A company that
// has ingested no GSTR-2B must see NONE of that: not a re-ordered key, not a
// row that used to be omitted, not a "0" that became "0.0000". This string is
// what makes "nothing changed" a checkable claim rather than a hope.
const preITCJSON = `{"gstin":"29ZZZZZ0000Z1Z5","fp":"052025",` +
	`"sup_details":{"osup_det":{"txval":"210000","iamt":"36000","camt":"900","samt":"900"},` +
	`"osup_zero":{"txval":"0","iamt":"0","camt":"0","samt":"0"},` +
	`"osup_nil_exmp":{"txval":"0","iamt":"0","camt":"0","samt":"0"},` +
	`"isup_rev":{"txval":"4000","iamt":"0","camt":"360","samt":"360"},` +
	`"osup_nongst":{"txval":"0","iamt":"0","camt":"0","samt":"0"}},` +
	`"inter_sup":[{"pos":"27","txval":"200000","iamt":"36000"}],` +
	`"itc_elg":{"itc_avl":[{"ty":"ISRC","iamt":"0","camt":"360","samt":"360"},` +
	`{"ty":"OTH","iamt":"0","camt":"180","samt":"180"}],` +
	`"itc_net":{"ty":"NET","iamt":"0","camt":"540","samt":"540"}}}`

// TestBuild3B_NilITCSourceIsByteIdentical is the compatibility gate for the
// whole slice.
//
// It asserts something slightly stronger than "the numbers are the same": with
// no ITCSource the marshalled document is byte-for-byte what it was before,
// EXCEPT for one purely ADDITIVE trailing key, `disclosures`. That key is a
// deliberate, disclosed divergence from strict byte-identity and it is checked
// explicitly rather than tolerated: a return that is not filing-ready has to be
// able to SAY so, and the case with no inward statement at all is the one that
// most needs to.
func TestBuild3B_NilITCSourceIsByteIdentical(t *testing.T) {
	out, err := build3B(may2025(), goldenRows(), nil, Options3B{Options: Options{GSTIN: "29ZZZZZ0000Z1Z5"}})
	if err != nil {
		t.Fatalf("build3B with a nil decision map must not error: %v", err)
	}
	raw, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(raw)

	const tail = `,"disclosures":`
	i := strings.Index(got, tail)
	if i < 0 {
		t.Fatalf("expected a trailing disclosures key, got:\n%s", got)
	}
	stripped := got[:i] + "}"
	if stripped != preITCJSON {
		t.Fatalf("a nil ITCSource must reproduce the pre-seam bytes exactly.\n got: %s\nwant: %s", stripped, preITCJSON)
	}

	// itc_rev and itc_inelg must be ABSENT, not present-and-empty: an empty
	// array is a different document to a consumer that switches on presence.
	for _, k := range []string{"itc_rev", "itc_inelg"} {
		if strings.Contains(got, `"`+k+`"`) {
			t.Fatalf("key %q must be omitted when nothing was reversed; got:\n%s", k, got)
		}
	}

	// The disclosure set for "purchases exist, no statement ingested".
	want := []string{
		Disc4D1ReclaimNotModelled,
		DiscCessNotModelled,
		DiscITCEligibilityNotClassified,
		DiscNoInwardStatement,
		DiscNotFilingReady,
		DiscPortalSchemaUnverified,
		DiscRule4243NotComputed,
	}
	if strings.Join(out.Disclosures, ",") != strings.Join(want, ",") {
		t.Fatalf("disclosures: got %v want %v", out.Disclosures, want)
	}
	// Non-vacuity: the return must not be able to claim it is filing-ready.
	if !contains(out.Disclosures, DiscNotFilingReady) {
		t.Fatal("a return with no inward statement must carry not-filing-ready")
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// TestValidate3B_RejectsBrokenTable4Identity proves (I2) is a real gate in both
// directions: an inconsistent 4C is rejected, and a NEGATIVE 4C built
// consistently is ACCEPTED.
//
// The negative half matters as much as the rejection half. Reversals can exceed
// availment in a period — a supplier who never filed last quarter has this
// period's whole credit reversed while little new was bought — and a validator
// that "helpfully" rejected a negative 4C would force a wrong return.
func TestValidate3B_RejectsBrokenTable4Identity(t *testing.T) {
	base := GSTR3B{
		T4: Table4ITC{
			ItcAvl: []Table4Row{{Ty: "OTH", Camt: d4("900"), Samt: d4("900"), Iamt: d4("0")}},
			ItcRev: []Table4Row{{Ty: "OTH", Camt: d4("100"), Samt: d4("100"), Iamt: d4("0")}},
			ItcNet: Table4Row{Ty: "NET", Camt: d4("800"), Samt: d4("800"), Iamt: d4("0")},
		},
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("900 - 100 == 800 must validate, got %v", err)
	}

	// 4C left at sum(4A), i.e. the reversal never applied. This is EXACTLY the
	// shape the mutation in the proof produces.
	broken := base
	broken.T4.ItcNet = Table4Row{Ty: "NET", Camt: d4("900"), Samt: d4("900"), Iamt: d4("0")}
	err := broken.Validate()
	if !errors.Is(err, ErrInvalid3B) {
		t.Fatalf("an itc_net that ignores the reversal must be rejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "4C") {
		t.Fatalf("the error must name the table it is about, got %v", err)
	}

	// One head wrong is enough — the identity is per head, not on the total.
	oneHead := base
	oneHead.T4.ItcNet = Table4Row{Ty: "NET", Camt: d4("800"), Samt: d4("801"), Iamt: d4("0")}
	if err := oneHead.Validate(); !errors.Is(err, ErrInvalid3B) {
		t.Fatalf("a per-head break must be rejected, got %v", err)
	}

	// A NEGATIVE 4C, built consistently, is LEGAL.
	negative := GSTR3B{
		T4: Table4ITC{
			ItcAvl: []Table4Row{{Ty: "OTH", Camt: d4("100"), Samt: d4("100"), Iamt: d4("0")}},
			ItcRev: []Table4Row{{Ty: "OTH", Camt: d4("450"), Samt: d4("450"), Iamt: d4("0")}},
			ItcNet: Table4Row{Ty: "NET", Camt: d4("-350"), Samt: d4("-350"), Iamt: d4("0")},
		},
	}
	if err := negative.Validate(); err != nil {
		t.Fatalf("a negative 4C is legal when reversals exceed availment, got %v", err)
	}
	// ...and 4(D) is NOT part of the identity: adding an ineligible row must not
	// change whether the return validates.
	withInelg := negative
	withInelg.T4.ItcInelg = []Table4Row{{Ty: "OTH", Camt: d4("77"), Samt: d4("77"), Iamt: d4("0")}}
	if err := withInelg.Validate(); err != nil {
		t.Fatalf("4(D) is informational and must not enter the 4C identity, got %v", err)
	}

	// AND THE PRODUCER MUST SATISFY THE VALIDATOR. Everything above is
	// hand-built, so on its own it proves only that Validate is strict about a
	// shape — it cannot notice if build3B never emits that shape. This closes
	// the loop: a return actually BUILT with a reversal must pass the identity
	// its own validator enforces, and its 4C must carry the subtraction.
	//
	// This is the half that bites a build3B which leaves itc_net at sum(4A).
	id := uuid.MustParse("55555555-5555-5555-5555-555555555555")
	built, err := build3B(may2025(),
		[]row3b{{voucherID: id, voucherNo: "PU-9", class: "PURCHASE", rawClass: "PURCHASE",
			taxable: d4("10000"), cgst: d4("900"), sgst: d4("900"), igst: d4("0")}},
		map[uuid.UUID]ITCDecision{id: {
			Bucket:        BucketOTH,
			Available:     tt("900", "900", "0"),
			ReversedOther: tt("100", "100", "0"),
		}}, Options3B{})
	if err != nil {
		t.Fatalf("build3B: %v", err)
	}
	if err := built.Validate(); err != nil {
		t.Fatalf("a return BUILT with a reversal must satisfy (I2) — build3B and Validate "+
			"disagree about what 4C means: %v", err)
	}
	if !built.T4.ItcNet.Camt.Equal(d4("800")) {
		t.Fatalf("built 4C CGST: want 900 - 100 = 800, got %s (900 means the reversal never "+
			"reached itc_net)", built.T4.ItcNet.Camt)
	}
}

// TestValidate3B_RejectsNegativeReversal pins the negative-tax rule: a document
// with negative posted input tax (a supplier credit note) is never reversed for
// being unmatched, because reversing a negative ADDS credit.
//
// The row below is internally CONSISTENT — 100 - (-50) == 150 — so (I2) passes
// and only the sign check can catch it. Without that check this return would
// hand the filer 50 rupees of credit for a purchase return the supplier never
// reported, and every arithmetic gate in the file would stay green.
func TestValidate3B_RejectsNegativeReversal(t *testing.T) {
	g := GSTR3B{
		T4: Table4ITC{
			ItcAvl: []Table4Row{{Ty: "OTH", Camt: d4("100"), Samt: d4("100"), Iamt: d4("0")}},
			ItcRev: []Table4Row{{Ty: "OTH", Camt: d4("-50"), Samt: d4("0"), Iamt: d4("0")}},
			ItcNet: Table4Row{Ty: "NET", Camt: d4("150"), Samt: d4("100"), Iamt: d4("0")},
		},
	}
	err := g.Validate()
	if !errors.Is(err, ErrInvalid3B) {
		t.Fatalf("a negative 4B row must be rejected, got %v", err)
	}
	if !strings.Contains(err.Error(), "negative") {
		t.Fatalf("the error must say what is wrong, got %v", err)
	}

	// A negative 4(D), by contrast, is LEGAL and must pass: a supplier credit
	// note against a time-barred invoice genuinely reduces the ineligible
	// figure, and rejecting it would make a correct return unrepresentable.
	ok := GSTR3B{
		T4: Table4ITC{
			ItcInelg: []Table4Row{{Ty: "OTH", Camt: d4("-50"), Samt: d4("-50"), Iamt: d4("0")}},
			ItcNet:   Table4Row{Ty: "NET", Camt: d4("0"), Samt: d4("0"), Iamt: d4("0")},
		},
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a negative 4(D) row is legal (a credit note against time-barred credit), got %v", err)
	}
}

// stubDecisions is a fixed decision map for the pure tests.
func stubDecisions(m map[uuid.UUID]ITCDecision) map[uuid.UUID]ITCDecision { return m }

// TestBuild3B_DecisionMustPartitionPostedTax is the staleness / version guard,
// tested purely. A decision whose Available + Ineligible does not equal the
// voucher's posted input tax is REFUSED, not quietly ignored.
//
// The failure it exists for: the matcher resolves a decision against version 1
// of a purchase (taxable 2000, tax 180+180) and the voucher is then ALTERED to
// 5000 (450+450). Taxable value is the field the 2B match keys on, so the old
// decision is not merely stale, it is about a document that no longer exists.
// Applying it would either reverse 180 of a 450 credit or claim 450 against a
// match for 180 — and (I1) would silently stop holding.
func TestBuild3B_DecisionMustPartitionPostedTax(t *testing.T) {
	id := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	rows := []row3b{{voucherID: id, voucherNo: "PU-1", class: "PURCHASE", rawClass: "PURCHASE",
		taxable: d4("5000"), cgst: d4("450"), sgst: d4("450"), igst: d4("0")}}

	// A decision computed against the SUPERSEDED version (tax 180/180).
	stale := stubDecisions(map[uuid.UUID]ITCDecision{
		id: {Bucket: BucketOTH, Available: tt("180", "180", "0"), Ineligible: zeroTax()},
	})
	_, err := build3B(may2025(), rows, stale, Options3B{})
	if !errors.Is(err, errStaleITCDecision) {
		t.Fatalf("a decision that does not account for the CURRENT version's tax must be refused, got %v", err)
	}
	if !strings.Contains(err.Error(), "PU-1") {
		t.Fatalf("the error must name the voucher, got %v", err)
	}

	// The same decision, restated against the current version, is accepted.
	fresh := stubDecisions(map[uuid.UUID]ITCDecision{
		id: {Bucket: BucketOTH, Available: tt("450", "450", "0"), Ineligible: zeroTax()},
	})
	out, err := build3B(may2025(), rows, fresh, Options3B{})
	if err != nil {
		t.Fatalf("a fresh decision must be accepted: %v", err)
	}
	if !out.T4.ItcNet.Camt.Equal(d4("450")) {
		t.Fatalf("itc_net CGST: want 450, got %s", out.T4.ItcNet.Camt)
	}
}

// TestBuild3B_DecisionStructuralRules covers the three remaining per-voucher
// guards, each of which is an identity somebody could otherwise break by
// writing a plausible-looking matcher.
func TestBuild3B_DecisionStructuralRules(t *testing.T) {
	id := uuid.MustParse("33333333-3333-3333-3333-333333333333")
	rows := []row3b{{voucherID: id, voucherNo: "PU-1", class: "PURCHASE", rawClass: "PURCHASE",
		taxable: d4("2000"), cgst: d4("180"), sgst: d4("180"), igst: d4("0")}}
	// A credit-note row: NEGATIVE posted input tax.
	notes := []row3b{{voucherID: id, voucherNo: "CN-1", class: "PURCHASE", rawClass: "DEBIT_NOTE",
		taxable: d4("-2000"), cgst: d4("-180"), sgst: d4("-180"), igst: d4("0")}}

	cases := []struct {
		name string
		rows []row3b
		dec  ITCDecision
		want string
	}{
		{
			name: "both reversal kinds on one voucher",
			rows: rows,
			dec: ITCDecision{Bucket: BucketOTH, Available: tt("180", "180", "0"),
				ReversedRules: tt("180", "180", "0"), ReversedOther: tt("180", "180", "0")},
			want: "BOTH",
		},
		{
			name: "reversal exceeds what was availed",
			rows: rows,
			dec: ITCDecision{Bucket: BucketOTH, Available: tt("180", "180", "0"),
				ReversedOther: tt("200", "180", "0")},
			want: "only",
		},
		{
			// CASE (a) OF THE CREDIT-NOTE ANALYSIS in checkITCDecision rule (4):
			// a note the supplier never filed, "reversed" by the negative books
			// figure. That is the shape that ADDS credit, and it is refused for
			// its SIGN. (The positive correction of case (b) — the supplier
			// declared a LARGER note than we booked — is legal and is pinned by
			// TestCreditNoteDeclaredLargerAgreesWithITC below.)
			name: "a credit note reversed by a negative amount",
			rows: notes,
			dec: ITCDecision{Bucket: BucketOTH, Available: tt("-180", "-180", "0"),
				ReversedOther: tt("-180", "-180", "0")},
			want: "NEGATIVE",
		},
		{
			name: "unknown 4(A) row code",
			rows: rows,
			dec:  ITCDecision{Bucket: ITCBucket("NOPE"), Available: tt("180", "180", "0")},
			want: "unknown ITC bucket",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := build3B(may2025(), tc.rows, map[uuid.UUID]ITCDecision{id: tc.dec}, Options3B{})
			if !errors.Is(err, ErrInvalid3B) {
				t.Fatalf("want ErrInvalid3B, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error should mention %q, got %v", tc.want, err)
			}
		})
	}

	// A credit note with a ZERO reversal is fine, and its negative credit flows
	// straight through to 4(A) — that is the conservative direction.
	out, err := build3B(may2025(), notes, map[uuid.UUID]ITCDecision{
		id: {Bucket: BucketOTH, Available: tt("-180", "-180", "0")},
	}, Options3B{})
	if err != nil {
		t.Fatalf("an unreversed credit note must build: %v", err)
	}
	if !out.T4.ItcNet.Camt.Equal(d4("-180")) {
		t.Fatalf("credit note itc_net CGST: want -180, got %s", out.T4.ItcNet.Camt)
	}
}

// TestCreditNoteDeclaredLargerAgreesWithITC is one half of a PAIR. Its twin is
// TestCreditNoteDeclaredLargerAgreesWithGSTR in internal/compliance/itc, and the
// two assert the SAME rupee figures for the SAME case:
//
//	books      -300 per head (a supplier credit note we recorded)
//	statement  -400 per head (the note the supplier actually declared)
//	4(A)       -300   — the books figure, because 4(A) is model (b)
//	4(B)(2)    +100   — the correction down to the supplier's larger note
//	4(C)       -400   — which is the statement's figure
//
// WHY THE PAIR EXISTS. These two packages are one seam and they once disagreed
// about this exact case in silence: itc computed the +100 correction and gstr's
// rule (4) capped any reversal at max(Available, 0) — zero on a credit note —
// so the decision was REFUSED and the whole return failed to generate. Nothing
// in either suite went red, because neither suite had a mismatched credit note
// in it. One number, asserted on both sides, is the only thing that keeps the
// halves together.
func TestCreditNoteDeclaredLargerAgreesWithITC(t *testing.T) {
	id := uuid.MustParse("77777777-7777-7777-7777-777777777777")
	// A supplier credit note as store.go leaves it: NEGATIVE money.
	notes := []row3b{{voucherID: id, voucherNo: "DN-B3", class: "PURCHASE", rawClass: "DEBIT_NOTE",
		taxable: d4("-3333.3333"), cgst: d4("-300"), sgst: d4("-300"), igst: d4("0")}}
	dec := map[uuid.UUID]ITCDecision{id: {
		Bucket:        BucketOTH,
		Available:     tt("-300", "-300", "0"),
		ReversedOther: tt("100", "100", "0"),
		Reason:        "MISMATCHED: CGST,SGST,TAXABLE_VALUE",
	}}

	out, err := build3B(may2025(), notes, dec, Options3B{})
	if err != nil {
		t.Fatalf("a supplier credit note we booked at 300 against a statement of 400 was "+
			"REFUSED: %v.\nThe 100 of credit the supplier has cancelled cannot reach the "+
			"return at all — the whole GSTR-3B fails to generate for any company holding "+
			"one such note. itc emits this decision today; see "+
			"TestCreditNoteDeclaredLargerAgreesWithGSTR in internal/compliance/itc, which "+
			"asserts the same 100 and the same net -400. gstr and itc now disagree about a "+
			"supplier credit note", err)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("the built return does not satisfy its own (I2) identity: %v", err)
	}

	if len(out.T4.ItcAvl) != 1 || !out.T4.ItcAvl[0].Camt.Equal(d4("-300")) {
		t.Fatalf("4(A) CGST = %+v, want -300 — 4(A) is the BOOKS figure under model (b); "+
			"moving the supplier's -400 into 4(A) instead would break the "+
			"Available+Ineligible partition against the posted input tax", out.T4.ItcAvl)
	}
	if len(out.T4.ItcRev) != 1 || !out.T4.ItcRev[0].Camt.Equal(d4("100")) {
		t.Fatalf("4(B)(2) CGST = %+v, want 100 — the supplier declared a credit note 100 "+
			"larger than we booked, and that 100 of credit must come out of the return; "+
			"see TestCreditNoteDeclaredLargerAgreesWithGSTR in internal/compliance/itc",
			out.T4.ItcRev)
	}
	if !out.T4.ItcNet.Camt.Equal(d4("-400")) || !out.T4.ItcNet.Samt.Equal(d4("-400")) {
		t.Fatalf("4(C) CGST = %s SGST = %s, want -400 per head — the net must equal the "+
			"supplier's declared credit note. -300 means the 100 correction never reached "+
			"the return and the filer over-claims 100 per head; -500 means it was counted "+
			"twice. gstr and itc now disagree about a supplier credit note; see "+
			"TestCreditNoteDeclaredLargerAgreesWithGSTR in internal/compliance/itc",
			out.T4.ItcNet.Camt, out.T4.ItcNet.Samt)
	}
}

// TestBuild3B_Rule42And43 checks the apportionment arithmetic against figures
// derived by hand from the rules, and checks that unusable inputs are REFUSED
// rather than silently producing a zero reversal.
//
// Hand derivation, rule 42: C2 = 1000 per head, E = 200000, F = 1000000, so
// E/F = 0.2; D1 = 0.2 x 1000 = 200; D2 = 5% x 1000 = 50; D1 + D2 = 250.
// Rule 43: Tr = 400 per head, same ratio, Te = 0.2 x 400 = 80.
// 4(B)(1) = 250 + 80 = 330 per head on CGST and SGST, 0 on IGST.
func TestBuild3B_Rule42And43(t *testing.T) {
	pol := &ITCPolicy{
		Rule42: &Rule42Inputs{C2: tt("1000", "1000", "0"), E: d4("200000"), F: d4("1000000")},
		Rule43: &Rule43Inputs{Tr: tt("400", "400", "0"), E: d4("200000"), F: d4("1000000")},
	}
	out, err := build3B(may2025(), nil, nil, Options3B{ITCPolicy: pol})
	if err != nil {
		t.Fatalf("build3B: %v", err)
	}
	if len(out.T4.ItcRev) != 1 || out.T4.ItcRev[0].Ty != "RUL" {
		t.Fatalf("want one RUL row, got %+v", out.T4.ItcRev)
	}
	r := out.T4.ItcRev[0]
	if !r.Camt.Equal(d4("330")) || !r.Samt.Equal(d4("330")) || !r.Iamt.Equal(d4("0")) {
		t.Fatalf("4(B)(1): want c/s 330 i 0, got %+v", r)
	}
	// No purchases at all, so 4C is legally negative and Validate accepts it.
	if err := out.Validate(); err != nil {
		t.Fatalf("a rule-42/43-only return must validate: %v", err)
	}
	if !out.T4.ItcNet.Camt.Equal(d4("-330")) {
		t.Fatalf("itc_net CGST: want -330, got %s", out.T4.ItcNet.Camt)
	}
	// The disclosure must now be ABSENT — the apportionment WAS computed.
	if contains(out.Disclosures, DiscRule4243NotComputed) {
		t.Fatalf("rule-42-43-not-computed must not be emitted when both rules were supplied: %v", out.Disclosures)
	}

	// F == 0 is refused. A zero reversal computed from an unusable denominator
	// is indistinguishable from a correct one, which is the whole failure mode.
	bad := &ITCPolicy{Rule42: &Rule42Inputs{C2: tt("1000", "1000", "0"), E: d4("200000"), F: decimal.Zero}}
	if _, err := build3B(may2025(), nil, nil, Options3B{ITCPolicy: bad}); !errors.Is(err, ErrInvalid3B) {
		t.Fatalf("F = 0 must be refused, got %v", err)
	}
	worse := &ITCPolicy{Rule42: &Rule42Inputs{C2: tt("1000", "1000", "0"), E: d4("2000000"), F: d4("1000000")}}
	if _, err := build3B(may2025(), nil, nil, Options3B{ITCPolicy: worse}); !errors.Is(err, ErrInvalid3B) {
		t.Fatalf("E > F must be refused, got %v", err)
	}
}

// TestDisclosures_ClosedVocabulary proves the vocabulary is actually closed: a
// token the frontend cannot switch on is an error, not a message.
func TestDisclosures_ClosedVocabulary(t *testing.T) {
	ok := &ITCPolicy{SourceDisclosures: []string{DiscStatementPartiallyParsed}}
	out, err := build3B(may2025(), nil, nil, Options3B{ITCPolicy: ok})
	if err != nil {
		t.Fatalf("a known token must be accepted: %v", err)
	}
	if !contains(out.Disclosures, DiscStatementPartiallyParsed) {
		t.Fatalf("the source's token must survive into the return: %v", out.Disclosures)
	}
	bad := &ITCPolicy{SourceDisclosures: []string{"probably-fine"}}
	if _, err := build3B(may2025(), nil, nil, Options3B{ITCPolicy: bad}); !errors.Is(err, ErrInvalid3B) {
		t.Fatalf("an unknown token must be refused, got %v", err)
	}
	// Sorted and deduplicated, deterministically.
	dup := &ITCPolicy{SourceDisclosures: []string{DiscCessNotModelled, DiscCessNotModelled}}
	out, err = build3B(may2025(), nil, nil, Options3B{ITCPolicy: dup})
	if err != nil {
		t.Fatalf("build3B: %v", err)
	}
	n := 0
	for _, d := range out.Disclosures {
		if d == DiscCessNotModelled {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("disclosures must be deduplicated, got %v", out.Disclosures)
	}
	for i := 1; i < len(out.Disclosures); i++ {
		if out.Disclosures[i-1] >= out.Disclosures[i] {
			t.Fatalf("disclosures must be sorted and unique, got %v", out.Disclosures)
		}
	}
}

// TestBlockedPurchaseReturnedNetsToZeroAgreesWithITC is one half of a PAIR. Its
// twin is TestBlockedPurchaseReturnedNetsToZero in internal/compliance/itc, and
// the two assert the SAME rupee figures for the SAME case:
//
//	a section 17(5)(a) motor vehicle, Rs 10,00,000 at 18%, bought and RETURNED
//
//	4(A) OTH   0        — +90,000 on the invoice, -90,000 on the credit note
//	4(B)(1)    0        — +90,000 on the invoice, -90,000 on the credit note
//	4(C)       0        — the taxpayer never took the credit and never loses it
//
// WHY THE PAIR EXISTS. A blocked purchase is availed in 4(A) and taken straight
// back out in 4(B)(1); the two halves are the same figure and must carry the
// same SIGN. Clamping 4(B)(1) at zero on the credit note leaves the invoice's
// +90,000 reversal standing against the note's -90,000 availment: 4(C) goes to
// -90,000 per head and the filer pays Rs 1,80,000 out of the electronic cash
// ledger on a car they returned. NOTHING DOWNSTREAM COMPLAINS — the partition
// identity (I1) still holds exactly, because the reversal is not part of it.
//
// And the fix cannot be made on one side alone. itc emitting a signed 4(B)(1)
// against a gstr that bans negative reversals outright makes GSTR-3B generation
// FAIL for every company with a returned blocked purchase, which is the same
// silent seam break TestCreditNoteDeclaredLargerAgreesWithITC above records.
func TestBlockedPurchaseReturnedNetsToZeroAgreesWithITC(t *testing.T) {
	car := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	ret := uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002")

	rows := []row3b{
		{voucherID: car, voucherNo: "PU-INV-CAR", class: "PURCHASE", rawClass: "PURCHASE",
			taxable: d4("1000000"), cgst: d4("90000"), sgst: d4("90000"), igst: d4("0")},
		// The supplier's credit note, as store.go's sign convention leaves it.
		{voucherID: ret, voucherNo: "DN-CN-CAR", class: "PURCHASE", rawClass: "DEBIT_NOTE",
			taxable: d4("-1000000"), cgst: d4("-90000"), sgst: d4("-90000"), igst: d4("0")},
	}
	dec := map[uuid.UUID]ITCDecision{
		car: {Bucket: BucketOTH, Available: tt("90000", "90000", "0"),
			ReversedRules: tt("90000", "90000", "0"), Reason: "MATCHED"},
		ret: {Bucket: BucketOTH, Available: tt("-90000", "-90000", "0"),
			ReversedRules: tt("-90000", "-90000", "0"), Reason: "MATCHED"},
	}

	out, err := build3B(may2025(), rows, dec, Options3B{})
	if err != nil {
		t.Fatalf("a RETURNED section 17(5) blocked purchase was REFUSED: %v.\n"+
			"itc emits this decision today — a signed 4(B)(1) that mirrors the signed "+
			"4(A) — so the whole GSTR-3B fails to generate for any company that returned "+
			"a blocked purchase. See TestBlockedPurchaseReturnedNetsToZero in "+
			"internal/compliance/itc, which asserts the same -90000 and the same net 0. "+
			"gstr and itc now disagree about a returned blocked purchase", err)
	}
	if err := out.Validate(); err != nil {
		t.Fatalf("the built return does not satisfy its own identities: %v.\n"+
			"A negative 4(B)(1) RUL row is the CORRECT figure here; banning it at period "+
			"level makes a correct return unrepresentable", err)
	}

	if len(out.T4.ItcAvl) != 1 || !out.T4.ItcAvl[0].Camt.IsZero() {
		t.Fatalf("4(A) = %+v, want a single OTH row of 0 per head — the purchase and its "+
			"return cancel", out.T4.ItcAvl)
	}
	if len(out.T4.ItcRev) != 1 || out.T4.ItcRev[0].Ty != "RUL" || !out.T4.ItcRev[0].Camt.IsZero() {
		t.Fatalf("4(B) = %+v, want a single RUL row of 0 per head. 90000 means the "+
			"section 17(5) reversal on the RETURN was clamped at zero while its availment "+
			"stayed negative; see TestBlockedPurchaseReturnedNetsToZero in "+
			"internal/compliance/itc", out.T4.ItcRev)
	}
	if !out.T4.ItcNet.Camt.IsZero() || !out.T4.ItcNet.Samt.IsZero() {
		t.Fatalf("4(C) = c %s s %s, want 0 per head. A blocked purchase bought and "+
			"returned has a NET ITC effect of exactly zero; -90000 per head means "+
			"Rs 1,80,000 of real credit destroyed and paid in cash on a car the taxpayer "+
			"never took credit on", out.T4.ItcNet.Camt, out.T4.ItcNet.Samt)
	}

	// THE RETURN ALONE, in a later period with nothing to absorb it. This is
	// the shape a period-level ban on negative 4(B)(1) makes ungeneratable, and
	// it is the ordinary case: the purchase and the return rarely fall in the
	// same month.
	alone, err := build3B(may2025(), rows[1:], map[uuid.UUID]ITCDecision{ret: dec[ret]}, Options3B{})
	if err != nil {
		t.Fatalf("a blocked purchase returned in a LATER period was REFUSED: %v.\n"+
			"The invoice's period already netted to zero; this period must too, and that "+
			"needs a 4(B)(1) of -90000 per head standing alone", err)
	}
	if err := alone.Validate(); err != nil {
		t.Fatalf("the later-period return does not validate: %v", err)
	}
	if !alone.T4.ItcNet.Camt.IsZero() {
		t.Fatalf("4(C) for the return-only period = %s, want 0 — %s per head of credit "+
			"the taxpayer never took has been destroyed",
			alone.T4.ItcNet.Camt, alone.T4.ItcNet.Camt.Abs())
	}
	if len(alone.T4.ItcRev) != 1 || !alone.T4.ItcRev[0].Camt.Equal(d4("-90000")) {
		t.Fatalf("4(B)(1) for the return-only period = %+v, want -90000 per head",
			alone.T4.ItcRev)
	}
}

// TestValidate3B_NegativeRulRowIsLegalNegativeOthIsNot pins the ROW the
// negative-reversal ban applies to.
//
// 4(B)(2) "OTH" is a haircut with nothing in 4(A) to mirror, so a negative one
// manufactures credit and stays refused. 4(B)(1) "RUL" mirrors a 4(A) figure
// rupee for rupee — the section 17(5) reversal of a document that is itself in
// 4(A) — so when that document is a credit note the RUL row is negative and
// that is the CORRECT answer. A blanket ban here made a correct return
// unrepresentable; see TestBlockedPurchaseReturnedNetsToZeroAgreesWithITC.
func TestValidate3B_NegativeRulRowIsLegalNegativeOthIsNot(t *testing.T) {
	rul := GSTR3B{
		T4: Table4ITC{
			ItcAvl: []Table4Row{{Ty: "OTH", Camt: d4("-90000"), Samt: d4("-90000"), Iamt: d4("0")}},
			ItcRev: []Table4Row{{Ty: "RUL", Camt: d4("-90000"), Samt: d4("-90000"), Iamt: d4("0")}},
			ItcNet: Table4Row{Ty: "NET", Camt: d4("0"), Samt: d4("0"), Iamt: d4("0")},
		},
	}
	if err := rul.Validate(); err != nil {
		t.Fatalf("a negative 4(B)(1) RUL row is LEGAL — it is the section 17(5) reversal "+
			"of a RETURNED blocked purchase, and refusing it destroys Rs 1,80,000 of "+
			"credit the taxpayer never took. Got %v", err)
	}

	// The same shape on the OTH row is still refused.
	oth := GSTR3B{
		T4: Table4ITC{
			ItcAvl: []Table4Row{{Ty: "OTH", Camt: d4("-90000"), Samt: d4("-90000"), Iamt: d4("0")}},
			ItcRev: []Table4Row{{Ty: "OTH", Camt: d4("-90000"), Samt: d4("-90000"), Iamt: d4("0")}},
			ItcNet: Table4Row{Ty: "NET", Camt: d4("0"), Samt: d4("0"), Iamt: d4("0")},
		},
	}
	if err := oth.Validate(); !errors.Is(err, ErrInvalid3B) {
		t.Fatalf("a negative 4(B)(2) OTH row must still be rejected — it is a haircut "+
			"with no 4(A) figure to mirror, so a negative one hands the filer credit for "+
			"a purchase return the supplier never reported. Got %v", err)
	}
}

// TestBuild3B_NegativeReversalOtherIsStillRefused pins the per-voucher half of
// the same split: a NEGATIVE 4(B)(2) is refused even when the availment is
// negative, which is exactly the shape the old blanket rule was protecting and
// the shape case (c) must not have widened.
func TestBuild3B_NegativeReversalOtherIsStillRefused(t *testing.T) {
	id := uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000001")
	rows := []row3b{{voucherID: id, voucherNo: "DN-1", class: "PURCHASE", rawClass: "DEBIT_NOTE",
		taxable: d4("-2000"), cgst: d4("-180"), sgst: d4("-180"), igst: d4("0")}}
	_, err := build3B(may2025(), rows, map[uuid.UUID]ITCDecision{id: {
		Bucket: BucketOTH, Available: tt("-180", "-180", "0"),
		ReversedOther: tt("-180", "-180", "0"),
	}}, Options3B{})
	if !errors.Is(err, ErrInvalid3B) {
		t.Fatalf("a NEGATIVE 4(B)(2) on a credit note must be refused — it adds 180 per "+
			"head of credit for a purchase return the supplier never reported. Got %v", err)
	}
	if !strings.Contains(err.Error(), "4(B)(2)") {
		t.Fatalf("the error must name the head it is about, got %v", err)
	}

	// A negative reversal larger than the note itself is refused on 4(B)(1) too:
	// undoing a section 17(5) reversal can be at most the whole of the note.
	_, err = build3B(may2025(), rows, map[uuid.UUID]ITCDecision{id: {
		Bucket: BucketOTH, Available: tt("-180", "-180", "0"),
		ReversedRules: tt("-500", "-500", "0"),
	}}, Options3B{})
	if !errors.Is(err, ErrInvalid3B) {
		t.Fatalf("a 4(B)(1) of -500 against an availment of -180 must be refused — it "+
			"adds 320 per head of credit the document never carried. Got %v", err)
	}
}
