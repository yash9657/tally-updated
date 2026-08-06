package rules

// ewaybill_test.go — T5's rules, its payload, and above all ITS HONESTY.
//
// ⛔⛔ THE MOST IMPORTANT TESTS IN THIS FILE ARE NOT ABOUT ARITHMETIC. They are
// TestNothingClaimsToBeARealEWayBill and TestEveryStatutoryFigureCarriesItsCaveat,
// because the thing that would do real damage here is not a wrong validity date —
// it is a document that a driver at a checkpost believes is a government e-way
// bill. PLAN-TALLY-PARITY.md §2.3 forbids claiming a validation that was not
// performed, and §7.6 lists NIC among the unreachable planes.
//
// Those two tests are written as NEGATIVE assertions, which HANDOFF §7.2 warns is
// the shape most likely to be inert ("a test that CANNOT FAIL is worse than no
// test"). So each one is mutation-proved in the session that wrote it, and each
// asserts a POSITIVE fact alongside the negative one — that the disclosure is
// present and says a specific thing — so it cannot pass by finding nothing at all.

import (
	"strings"
	"testing"
	"time"
)

// invoiceOver is a consignment worth more than the threshold, intra-State,
// 120 km — the ordinary case a distributor ships every day.
func invoiceOver() ConsignmentSnapshot {
	return ConsignmentSnapshot{
		Invoice: VoucherSnapshot{
			VoucherNo:       "INV-1042",
			VoucherDate:     "2026-07-15",
			SellerGstin:     "29AACCD1234K1Z8",
			SellerLegalName: "Demo Traders Private Limited",
			SellerStateCode: "29",
			BuyerGstin:      "29AACCB5678L1Z3",
			BuyerLegalName:  "Acme Retail LLP",
			BuyerStateCode:  "29",
			Lines: []SnapshotLine{{
				Desc: "Steel Table", HSN: "94036000", Qty: "10", Unit: "NOS",
				UnitPrice: "6000", Taxable: "60000", GstRatePct: "18",
				CGST: "5400", SGST: "5400", IGST: "0", LineTotal: "70800",
			}},
			Taxable: "60000", CGST: "5400", SGST: "5400", IGST: "0",
			InvoiceTotal: "70800",
		},
		FromPincode: "560001", ToPincode: "560095",
		FromStateCode: "29", ToStateCode: "29",
		TransporterID: "29AACCT9999K1Z5", TransporterName: "Speedy Logistics",
		VehicleNumber: "KA01AB1234", Mode: ModeRoad,
		DistanceKm:  120,
		GeneratedAt: time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC),
	}
}

// ---------------------------------------------------------------------------
// 1. HONESTY — the two that matter most
// ---------------------------------------------------------------------------

func TestNothingClaimsToBeARealEWayBill(t *testing.T) {
	c := invoiceOver()
	a := Assess(c)
	res := FakeEWayBillProvider{}.GenerateEWayBill(BuildEWayBill(c), a)

	// THE POSITIVE HALF, so this test cannot pass by finding nothing: the
	// disclosure must EXIST and must lead with what the document is not.
	if len(res.Disclosures) == 0 {
		t.Fatal("the result carries NO disclosures at all")
	}
	// Matched case-INSENSITIVELY on the substance rather than the exact casing: the
	// disclosure legitimately shouts ("is NOT an EBN"), and a test that pinned the
	// capitalisation would fail on an improvement to the wording while saying nothing
	// about whether the meaning survived.
	first := strings.ToLower(res.Disclosures[0])
	for _, want := range []string{"not a government-issued", "no submission was made", "not an ebn"} {
		if !strings.Contains(first, want) {
			t.Errorf("the FIRST disclosure does not contain %q — it must lead with what this is not, "+
				"because a reader stops reading: %s", want, res.Disclosures[0])
		}
	}

	// THE NEGATIVE HALF. The identifier must not be mistakable for an EBN, which is
	// twelve digits.
	if res.LocalReference == "" {
		t.Fatal("the result carries no reference at all")
	}
	if !strings.HasPrefix(res.LocalReference, "LOCAL-") {
		t.Errorf("the reference %q is not prefixed LOCAL- : on a printed sheet it would be "+
			"indistinguishable from a government identifier", res.LocalReference)
	}
	digits := 0
	for _, r := range res.LocalReference {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if onlyDigits(res.LocalReference) && digits == 12 {
		t.Errorf("the reference %q is a bare 12-digit number, which is exactly the shape of a real EBN",
			res.LocalReference)
	}
	// And the provider must say FAKE out loud, because the whole disclosure rests on
	// the name being carried through rather than assumed.
	if !strings.Contains(res.Provider, "fake") {
		t.Errorf("the provider name %q does not say it is a stand-in", res.Provider)
	}
}

func TestEveryStatutoryFigureCarriesItsCaveat(t *testing.T) {
	figs := Rule138Figures()
	// THE POSITIVE HALF: there ARE figures. Without this the loop below passes
	// vacuously on an empty slice — the §7.2 failure mode exactly.
	if len(figs) < 4 {
		t.Fatalf("Rule138Figures returned %d figures, want at least 4 (threshold, Part B distance, "+
			"and the two validity rates)", len(figs))
	}
	for _, f := range figs {
		if f.Basis.Grade != GradeSecondary {
			t.Errorf("%s is graded %q. ⛔ NOTHING about Rule 138 is verified in this repository — "+
				"CBIC failed TLS verification on 2026-08-05 — so a PRIMARY grade here is a false claim "+
				"about a statutory source. If the published text has genuinely been placed in "+
				"docs/statutory/, this test is what should be updated, deliberately, in that commit",
				f.Name, f.Basis.Grade)
		}
		if f.Basis.Citation == "" {
			t.Errorf("%s carries no citation", f.Name)
		}
		if f.Basis.Caveat == "" {
			t.Errorf("%s carries no caveat", f.Name)
		}
		if len(f.Basis.Sources) == 0 {
			t.Errorf("%s names no sources actually consulted", f.Name)
		}
		// The unreachable primary source must still be named: the next person to try
		// needs the URL, and its presence beside a SECONDARY grade is what makes the
		// gap legible rather than implied.
		var namesCBIC bool
		for _, s := range f.Basis.Sources {
			if strings.Contains(s, "cbic.gov.in") {
				namesCBIC = true
			}
		}
		if !namesCBIC {
			t.Errorf("%s does not name the CBIC primary source it was unable to reach", f.Name)
		}
	}

	// The disclosure block must restate the caveat AND must not be the only place a
	// number lives — it names each figure rather than hard-coding it.
	block := strings.Join(Rule138Disclosures(), "\n")
	if !strings.Contains(block, "SECONDARY") {
		t.Error("the disclosure block does not say the figures are secondary material")
	}
	if !strings.Contains(block, "docs/statutory/") {
		t.Error("the disclosure block does not say what would make the figures CITED — the human-only " +
			"step is the actionable part")
	}
}

func onlyDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(s) > 0
}

// ---------------------------------------------------------------------------
// 2. Assess — the three questions, each with its permissive wrong answer
// ---------------------------------------------------------------------------

func TestAssessDecidesRatherThanAssuming(t *testing.T) {
	t.Run("an unstated distance BLOCKS rather than granting one day", func(t *testing.T) {
		// ⛔ THE PERMISSIVE WRONG ANSWER. A validity the system guessed is a bill that
		// expires without warning, and an expired e-way bill on a live consignment is
		// a detained vehicle.
		c := invoiceOver()
		c.DistanceKm = 0
		a := Assess(c)
		if a.ValidityDays != 0 || !a.ValidUntil.IsZero() {
			t.Errorf("an unstated distance produced a validity of %d day(s) to %v, want none computed",
				a.ValidityDays, a.ValidUntil)
		}
		if len(a.Blockers) == 0 {
			t.Fatal("an unstated distance produced NO blocker")
		}
		if !strings.Contains(strings.Join(a.Blockers, " "), "validity period cannot be computed") {
			t.Errorf("the blocker does not say the validity is uncomputable: %v", a.Blockers)
		}
		// THE CONTROL: the same consignment WITH a distance computes a validity, so
		// the assertion above is about the distance and not about the fixture.
		c.DistanceKm = 120
		if got := Assess(c); got.ValidityDays != 1 {
			t.Errorf("CONTROL: 120 km should compute a validity, got %d day(s)", got.ValidityDays)
		}
	})

	t.Run("validity rounds UP, because a part day is a whole day", func(t *testing.T) {
		cases := []struct{ km, want int }{
			{1, 1},   // the floor
			{200, 1}, // exactly one day's worth
			{201, 2}, // ⛔ rounding DOWN here expires the bill mid-journey
			{250, 2}, // the case a "round to nearest" implementation gets wrong
			{400, 2},
			{401, 3},
		}
		for _, tc := range cases {
			c := invoiceOver()
			c.DistanceKm = tc.km
			if got := Assess(c).ValidityDays; got != tc.want {
				t.Errorf("%d km ordinary cargo = %d day(s), want %d. Rounding to NEAREST rather than UP "+
					"gives 1 for 250 km and expires the bill mid-journey", tc.km, got, tc.want)
			}
		}
	})

	t.Run("over-dimensional cargo gets the 20 km rate, and is never inferred", func(t *testing.T) {
		c := invoiceOver()
		c.DistanceKm = 200
		ordinary := Assess(c)
		c.OverDimensionalCargo = true
		odc := Assess(c)
		if ordinary.ValidityDays != 1 {
			t.Fatalf("CONTROL: 200 km ordinary = %d day(s), want 1", ordinary.ValidityDays)
		}
		if odc.ValidityDays != 10 {
			t.Errorf("200 km over-dimensional = %d day(s), want 10 (one per 20 km). Assuming ordinary "+
				"cargo would grant TEN TIMES the validity the rule allows", odc.ValidityDays)
		}
		// The basis must SAY which class was applied, because the flag is asserted by
		// an operator and an artefact that did not name it could not be audited.
		if !strings.Contains(strings.Join(odc.Basis, " "), "OVER-DIMENSIONAL") {
			t.Error("the basis does not name the cargo class it applied")
		}
	})

	t.Run("Part B is required unless the movement is intra-State AND short", func(t *testing.T) {
		base := invoiceOver()

		// Intra-State, 40 km: relaxed.
		short := base
		short.DistanceKm = 40
		if a := Assess(short); a.PartBRequired {
			t.Error("intra-State at 40 km should not require Part B")
		}

		// Intra-State, 120 km: required.
		if a := Assess(base); !a.PartBRequired {
			t.Error("intra-State at 120 km should require Part B (over the 50 km relaxation)")
		}

		// INTER-State, 40 km: required. ⛔ The relaxation is within a State only, and
		// applying it across a boundary is the permissive wrong answer.
		cross := base
		cross.DistanceKm = 40
		cross.ToStateCode = "27"
		if a := Assess(cross); !a.PartBRequired {
			t.Error("a 40 km INTER-State movement should still require Part B — the relaxation applies " +
				"only within a State")
		}

		// Intra-State, distance UNSTATED: required. Saying "within the State, so
		// optional" without a distance would exempt a 900 km haul.
		unstated := base
		unstated.DistanceKm = 0
		a := Assess(unstated)
		if !a.PartBRequired {
			t.Error("an intra-State movement with NO stated distance should require Part B — the " +
				"relaxation is for SHORT hops and an unstated distance is not a short one")
		}

		// And a required Part B with no vehicle number is a BLOCKER, not a silent
		// Part-A-only bill.
		noVehicle := base
		noVehicle.VehicleNumber = ""
		if bl := Assess(noVehicle).Blockers; len(bl) == 0 {
			t.Error("Part B required with no vehicle number produced no blocker")
		}
	})

	t.Run("the threshold is applied at-or-above, and the reading is stated", func(t *testing.T) {
		c := invoiceOver()
		c.Invoice.InvoiceTotal = "50000"
		a := Assess(c)
		if !a.Required {
			t.Error("a consignment of exactly Rs 50,000 should require a bill under the conservative " +
				"at-or-above reading this build takes")
		}
		if !strings.Contains(strings.Join(a.Basis, " "), "at or above") {
			t.Error("the basis does not state which reading of the threshold was applied — it is a " +
				"judgement rather than a citation, so it has to be said out loud")
		}

		c.Invoice.InvoiceTotal = "49999.99"
		below := Assess(c)
		if below.Required {
			t.Error("a consignment below the threshold should not require a bill on value alone")
		}
		// ⛔ AND THE BELOW-THRESHOLD ANSWER MUST DISCLOSE WHAT IT DID NOT CHECK. "No
		// bill required" is the answer most likely to be relied on and least likely
		// to be questioned.
		if !strings.Contains(strings.Join(below.Basis, " "), "State-specific relaxation") {
			t.Error("a below-threshold assessment does not disclose that no State-specific relaxation " +
				"or exempt-goods carve-out was applied")
		}
	})
}

// ---------------------------------------------------------------------------
// 3. The payload and the recorded schema
// ---------------------------------------------------------------------------

func TestBuildEWayBillConformsToTheRecordedSchema(t *testing.T) {
	p := BuildEWayBill(invoiceOver())
	if v := ValidateEWayBill(p); len(v) != 0 {
		t.Fatalf("a well-formed consignment produced %d schema violation(s): %v", len(v), v)
	}

	// The version is read FROM the embedded schema, so payload and golden cannot
	// disagree about which version was targeted.
	if p.Version != EWayBillSchemaSubset().Version {
		t.Errorf("payload version %q != schema version %q", p.Version, EWayBillSchemaSubset().Version)
	}
	// NIC's date format, not cored's ISO.
	if p.DocDate != "15/07/2026" {
		t.Errorf("docDate = %q, want 15/07/2026 (NIC is DD/MM/YYYY, cored is ISO)", p.DocDate)
	}
	// An intra-State 18% line splits 9 + 9 and carries no IGST.
	it := p.ItemList[0]
	if it.CgstRate != "9" || it.SgstRate != "9" || it.IgstRate != "0" {
		t.Errorf("intra-State 18%% line rates = cgst %q sgst %q igst %q, want 9/9/0",
			it.CgstRate, it.SgstRate, it.IgstRate)
	}
	// Money at exactly two places.
	if p.TotInvValue != "70800.00" {
		t.Errorf("totInvValue = %q, want 70800.00", p.TotInvValue)
	}
	// The vehicle number is normalised: NIC takes it without spaces, upper case.
	if p.VehicleNo != "KA01AB1234" {
		t.Errorf("vehicleNo = %q, want KA01AB1234", p.VehicleNo)
	}
}

func TestAnInterStateLineCarriesIGSTAndNoCGST(t *testing.T) {
	c := invoiceOver()
	c.ToStateCode = "27"
	c.Invoice.BuyerStateCode = "27"
	c.Invoice.Lines[0].CGST, c.Invoice.Lines[0].SGST, c.Invoice.Lines[0].IGST = "0", "0", "10800"
	c.Invoice.CGST, c.Invoice.SGST, c.Invoice.IGST = "0", "0", "10800"
	it := BuildEWayBill(c).ItemList[0]
	if it.IgstRate != "18" {
		t.Errorf("inter-State line igstRate = %q, want the WHOLE 18", it.IgstRate)
	}
	if it.CgstRate != "0" || it.SgstRate != "0" {
		t.Errorf("inter-State line carries cgst %q sgst %q, want 0/0", it.CgstRate, it.SgstRate)
	}
}

// ⛔ THE VALIDATOR MUST ACTUALLY REFUSE THINGS. Each case has a CONTROL — the same
// payload with only that field corrected — because a validator that refused
// everything would pass a test that only checked for non-empty violations.
func TestValidateEWayBillCatchesEachMalformedField(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*EWayBillPayload)
		want   string
	}{
		{"a malformed GSTIN", func(p *EWayBillPayload) { p.FromGstin = "NOTAGSTIN" }, "fromGstin"},
		{"a missing GSTIN", func(p *EWayBillPayload) { p.ToGstin = "" }, "toGstin"},
		{"a five-digit pincode", func(p *EWayBillPayload) { p.ToPincode = "56009" }, "toPincode"},
		{"a pincode starting with zero", func(p *EWayBillPayload) { p.FromPincode = "060001" }, "fromPincode"},
		{"an ISO date where NIC wants DD/MM/YYYY", func(p *EWayBillPayload) { p.DocDate = "2026-07-15" }, "docDate"},
		{"an unknown doc type", func(p *EWayBillPayload) { p.DocType = "XYZ" }, "docType"},
		{"an unknown transport mode", func(p *EWayBillPayload) { p.TransMode = "9" }, "transMode"},
		{"a malformed vehicle number", func(p *EWayBillPayload) { p.VehicleNo = "1234" }, "vehicleNo"},
		{"money at three decimal places", func(p *EWayBillPayload) { p.TotInvValue = "70800.000" }, "totInvValue"},
		{"no items", func(p *EWayBillPayload) { p.ItemList = nil }, "itemList"},
		{
			// A ZERO DISTANCE IS OUT OF RANGE ON PURPOSE — the second line of defence
			// behind Assess, so a payload that reached NIC could not carry an unstated
			// distance silently.
			name: "a zero distance", mutate: func(p *EWayBillPayload) { p.TransDistance = "0" },
			want: "transDistance",
		},
		{"a non-numeric distance", func(p *EWayBillPayload) { p.TransDistance = "about 120" }, "transDistance"},
		{"a transporter id of the wrong length", func(p *EWayBillPayload) { p.TransporterID = "TOOSHORT" }, "transporterId"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// THE CONTROL, inside each subtest: the unmutated payload is clean, so the
			// violation below is attributable to the mutation and nothing else.
			base := BuildEWayBill(invoiceOver())
			if v := ValidateEWayBill(base); len(v) != 0 {
				t.Fatalf("CONTROL: the unmutated payload is already invalid: %v", v)
			}
			tc.mutate(&base)
			v := ValidateEWayBill(base)
			if len(v) == 0 {
				t.Fatalf("%s was ACCEPTED", tc.name)
			}
			if !strings.Contains(strings.Join(v, " "), tc.want) {
				t.Errorf("the violation for %s does not name %q: %v", tc.name, tc.want, v)
			}
		})
	}
}

// An EMPTY vehicle number is legal at the SCHEMA layer even though Assess may
// require it, and that separation is deliberate rather than an oversight: this
// validator checks SHAPE, Assess decides what the RULE requires. A schema validator
// that also enforced Part B would be a second copy of that logic.
func TestAnAbsentVehicleNumberIsASchemaNonIssueAndAStatutoryOne(t *testing.T) {
	c := invoiceOver()
	c.VehicleNumber = ""
	p := BuildEWayBill(c)
	if v := ValidateEWayBill(p); len(v) != 0 {
		t.Errorf("an absent vehicle number is a SCHEMA violation, but it should only be a statutory "+
			"one — the two layers must not hold two copies of the Part B rule: %v", v)
	}
	if bl := Assess(c).Blockers; len(bl) == 0 {
		t.Error("an absent vehicle number on a 120 km movement is not a schema problem but it IS a " +
			"statutory blocker, and Assess did not report it")
	}
}

// The whole point of a recorded schema is that it is READ rather than restated, so
// the embedded JSON must actually load and carry its provenance.
func TestTheRecordedSchemaLoadsAndAdmitsItIsSecondary(t *testing.T) {
	s := EWayBillSchemaSubset()
	if s.Schema != "nic-ewaybill-subset" {
		t.Errorf("schema name = %q", s.Schema)
	}
	if s.Version == "" {
		t.Error("the schema carries no version")
	}
	if !strings.Contains(s.Provenance, "SECONDARY") {
		t.Error("the recorded schema does not declare itself secondary material — it was derived from " +
			"documentation summaries, not from a NIC schema document or a live call, and a reader who " +
			"assumed otherwise would treat a clean validation as conformance")
	}
	if s.ItemList.Min < 1 {
		t.Error("the schema permits a consignment with no goods")
	}
}
