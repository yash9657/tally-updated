package rules

// ewaybill.go — THE e-WAY BILL (T5), beside einvoice.go and reusing its shape.
//
// An e-way bill is legally required to move goods over the consignment threshold
// (rule138.go). It is the same shape as the IRN workflow this package already
// implements: build a payload from a voucher snapshot, validate it against a
// recorded schema, call an external API through a pluggable provider, persist what
// comes back. PLAN-TALLY-PARITY.md §T5 is explicit that this must REUSE that
// pattern rather than invent a second one, so the types below mirror
// EInvoicePayload's conventions exactly — fixed-2 money strings, a recorded
// schema subset, a validator that returns a list of problems rather than the first.
//
// ===========================================================================
// ⛔⛔ NOTHING HERE IS A GOVERNMENT-ISSUED e-WAY BILL, AND THE ARTEFACT SAYS SO
// ===========================================================================
// The NIC e-Way Bill API is unreachable from this environment. So what this
// package produces is:
//
//	the PAYLOAD that would be submitted, validated against a recorded schema; and
//	a LOCAL, DETERMINISTIC reference — never an EBN.
//
// An EBN (e-Way Bill Number) is issued by NIC. Anything we generate that looked
// like one would tell a driver at a checkpost that the consignment is covered when
// it is not, which is a worse outcome than printing nothing — the same argument
// invQRNotPrintedNotice records in internal/printing/invoice.go for the rule 48(4)
// QR code, and the reason that notice is conditioned on the artefact being the KIND
// of thing the gap applies to rather than on a global constant.
//
// ⚠️ NOTE THE ONE PLACE THIS IS *BETTER* THAN THE IRN CASE, because it changes
// what is honest to claim. A rule 48(4) QR is signed with the Income Tax
// Department's private key, so T18 is permanently unshippable from anywhere without
// that key. An EBN is just an identifier NIC allocates: the payload here is the
// real payload, and the day credentials exist (a GSTIN plus sandbox access — §8
// item 4) a live provider drops in behind ProviderAdapter and this becomes a real
// e-way bill with no change to the payload builder. That is why the work is worth
// doing now and why the fake is a stand-in rather than a pretence.
//
// ===========================================================================
// WHY THE PACKAGE IMPORTS NOTHING
// ===========================================================================
// internal/archtest pins internal/statutory/rules to the EMPTY allowed-import
// set, exactly as it pins its parent: it may import no sibling at all. So the
// voucher facts arrive as a snapshot the caller assembles — the same contract
// BuildEInvoice takes — and nothing here reads a database, a ledger or a report.
//
// ===========================================================================
// ⛔⛔ WHY THIS IS A SEPARATE PACKAGE FROM internal/statutory — READ BEFORE
// MOVING ANYTHING BACK UP
// ===========================================================================
// These files used to live in internal/statutory alongside the IRN e-invoice
// workflow. They were split DOWN into this leaf so that internal/server (cored)
// may import them, because T5 needs a browser to be able to raise an e-way bill
// and cored is the only plane a browser can safely reach.
//
// The obvious objection is that internal/statutory is a separate deployable ON
// PURPOSE and this looks like eroding that. It is not, and the distinction is
// the whole reason the line falls here:
//
//	statutoryd exists because the IRN workflow wraps an EXACTLY-ONCE external
//	IRP call in a durable, resumable, memoized workflow (runner.go, StepStore,
//	PGStore, the cored freeze hook). A crash between "generate" and "freeze"
//	would mint a SECOND IRN against a real government API. That is a genuine
//	reason for a separate plane with its own durable state.
//
//	⛔ NONE OF THAT IS TRUE OF THE e-WAY BILL CORE. Assess, BuildEWayBill,
//	ValidateEWayBill, EWayBillDisclosures and even
//	FakeEWayBillProvider.GenerateEWayBill take no context.Context, return no
//	error, and touch no clock, no socket and no store. They are PURE FUNCTIONS
//	over a snapshot the caller assembles.
//
// So routing them through statutoryd would have put four pure function calls
// behind a second process, a service token and an HTTP hop — and, far worse,
// OUTSIDE cored's PDP, RLS, step-up and company scoping, none of which
// statutoryd has. That is inheriting the e-invoice architecture's costs without
// its reason. The split does not weaken the boundary; it names which part of
// internal/statutory was ever plane-specific.
//
// ⚠️ WHAT THIS DOES *NOT* DO, because the handoff claimed otherwise and the
// claim is worth correcting where someone will read it: it does NOT by itself
// unblock the e-invoice/IRN screen. The IRN genuinely needs the durable
// workflow, so a browser triggering one still needs cored to PROXY to
// statutoryd (or the browser to reach statutoryd directly, with all the authz
// consequences above). What that screen inherits from here is the shared
// snapshot vocabulary in a package cored can name — not a finished path.

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// TransportMode is how the goods move. NIC codes these 1-4; the names are carried
// here and mapped at the edge, because a payload of bare integers is unreadable in
// a log and a mis-mapped 3 is indistinguishable from a mis-mapped 4.
type TransportMode string

const (
	ModeRoad TransportMode = "ROAD"
	ModeRail TransportMode = "RAIL"
	ModeAir  TransportMode = "AIR"
	ModeShip TransportMode = "SHIP"
)

// nicTransportCode maps a mode to the code NIC expects. Road is 1, Rail 2, Air 3,
// Ship 4.
//
// ⚠️ SECONDARY, like everything else about this API in this build: the mapping is
// from commentary, not from a schema document in this repository. It is recorded
// as a map rather than inline arithmetic so a correction is one line.
var nicTransportCode = map[TransportMode]string{
	ModeRoad: "1", ModeRail: "2", ModeAir: "3", ModeShip: "4",
}

// Valid reports whether m is one of the four modes.
func (m TransportMode) Valid() bool { _, ok := nicTransportCode[m]; return ok }

// ConsignmentSnapshot is the input to BuildEWayBill: what the caller knows about
// the movement, on top of the invoice facts already in VoucherSnapshot.
//
// It is a SEPARATE struct from VoucherSnapshot rather than fields added to it,
// because a movement is a different act from an invoice. Most invoices never move
// goods over the threshold and would carry four empty fields forever; and an
// e-way bill can be raised for a delivery challan that is not an invoice at all.
type ConsignmentSnapshot struct {
	// Invoice is the document the movement is against.
	Invoice VoucherSnapshot

	// FromPincode and ToPincode are where the goods start and end. NIC keys the
	// route on these, not on the party addresses, because a consignment routinely
	// ships from a warehouse that is neither party's registered address.
	FromPincode string
	ToPincode   string
	// FromStateCode and ToStateCode decide whether the movement is intra-State,
	// which is what the Part B relaxation turns on.
	FromStateCode string
	ToStateCode   string

	// TransporterID is the transporter's GSTIN or their 15-character enrolment id.
	TransporterID   string
	TransporterName string

	// VehicleNumber is PART B. Empty is legal below the distance threshold for an
	// intra-State movement — see Assess, which decides rather than assuming.
	VehicleNumber string
	Mode          TransportMode

	// DistanceKm is the approximate road distance, which drives the validity
	// period. ⚠️ ZERO MEANS "NOT STATED" and is NOT the same as a zero-distance
	// movement: Assess refuses to compute a validity from an unstated distance
	// rather than granting the one-day minimum by accident.
	DistanceKm int

	// OverDimensionalCargo selects the 20 km/day validity rate instead of 200.
	// ⛔ IT MUST BE ASSERTED BY THE OPERATOR AND IS NEVER INFERRED. Nothing in a
	// voucher says whether a consignment is over-dimensional, and guessing it wrong
	// in the permissive direction (assuming ordinary cargo) would grant TEN TIMES
	// the validity the rule allows — an expired e-way bill that the system says is
	// live is a detained vehicle.
	OverDimensionalCargo bool

	// GeneratedAt is when the bill is raised. Supplied rather than read from the
	// clock so the payload and its validity are reproducible, which is what makes
	// the golden test possible at all. The IRN workflow takes the same approach.
	GeneratedAt time.Time
}

// EWayBillPayload is the subset of the NIC e-way bill schema this slice emits.
// Money is fixed-2 STRINGS, matching EInvoicePayload and NIC.
type EWayBillPayload struct {
	Version string `json:"version"`

	// SupplyType is "O" (outward) or "I" (inward); SubSupplyType 1 is Supply.
	SupplyType    string `json:"supplyType"`
	SubSupplyType string `json:"subSupplyType"`
	DocType       string `json:"docType"`
	DocNo         string `json:"docNo"`
	DocDate       string `json:"docDate"`

	FromGstin     string `json:"fromGstin"`
	FromTrdName   string `json:"fromTrdName"`
	FromStateCode string `json:"fromStateCode"`
	FromPincode   string `json:"fromPincode"`

	ToGstin     string `json:"toGstin"`
	ToTrdName   string `json:"toTrdName"`
	ToStateCode string `json:"toStateCode"`
	ToPincode   string `json:"toPincode"`

	ItemList []EWayBillItem `json:"itemList"`

	TotalValue  string `json:"totalValue"`
	CgstValue   string `json:"cgstValue"`
	SgstValue   string `json:"sgstValue"`
	IgstValue   string `json:"igstValue"`
	TotInvValue string `json:"totInvValue"`

	TransporterID   string `json:"transporterId"`
	TransporterName string `json:"transporterName"`
	TransDistance   string `json:"transDistance"`
	TransMode       string `json:"transMode"`
	VehicleNo       string `json:"vehicleNo"`
}

// EWayBillItem is one line of the consignment.
type EWayBillItem struct {
	ProductName   string `json:"productName"`
	HSNCode       string `json:"hsnCode"`
	Quantity      string `json:"quantity"`
	QtyUnit       string `json:"qtyUnit"`
	TaxableAmount string `json:"taxableAmount"`
	CgstRate      string `json:"cgstRate"`
	SgstRate      string `json:"sgstRate"`
	IgstRate      string `json:"igstRate"`
}

// Assessment is what the rules say about ONE movement: whether a bill is needed
// at all, whether Part B is required, and how long it would be valid.
//
// ⛔ IT IS RETURNED AS A VALUE RATHER THAN APPLIED SILENTLY, because every field
// is a claim under the caveat rule138.go records. A function that just returned a
// validity date would be asserting a statutory conclusion; this returns the
// conclusion WITH its reasoning, and the reasoning reaches the artefact.
type Assessment struct {
	// Required is whether the consignment value reaches the threshold.
	Required bool
	// PartBRequired is whether the vehicle number must be present.
	PartBRequired bool
	// ValidUntil is the last day the bill would be valid, or the zero time when it
	// cannot be computed (an unstated distance).
	ValidUntil time.Time
	// ValidityDays is the number of days granted, or 0 when uncomputable.
	ValidityDays int
	// Basis is the reasoning, in words, for each conclusion above.
	Basis []string
	// Blockers are the things that make this consignment un-submittable as
	// supplied. A NON-EMPTY Blockers with Required = true is the interesting case:
	// the movement needs a bill and cannot have one yet.
	Blockers []string
}

// Assess applies rule138.go's figures to one consignment.
//
// ⛔ IT DECIDES; IT DOES NOT ASSUME. Each of the three questions has a permissive
// answer that would be silently wrong, and each is refused explicitly:
//
//	an unstated DISTANCE does not become "1 day" — it becomes a BLOCKER, because a
//	  bill whose validity the system guessed is a bill that expires without warning;
//	an unstated VEHICLE on a long or inter-State movement does not become "Part A
//	  only" — Part B is required and its absence is a blocker;
//	ORDINARY cargo is not the default when nothing was said, because assuming it
//	  grants ten times the validity of the over-dimensional rate. The flag has to
//	  be asserted, so its zero value (false = ordinary) is stated in the basis
//	  rather than left implicit.
func Assess(c ConsignmentSnapshot) Assessment {
	var a Assessment

	// --- 1. IS A BILL REQUIRED AT ALL?
	total := decimal.Zero
	if c.Invoice.InvoiceTotal != "" {
		if d, err := decimal.NewFromString(c.Invoice.InvoiceTotal); err == nil {
			total = d
		}
	}
	threshold := decimal.NewFromInt(ConsignmentThresholdRupees)
	// AT OR ABOVE, not strictly above: the commentaries phrase the rule as
	// "exceeding Rs 50,000", and this build takes the CONSERVATIVE reading
	// (>=) deliberately. Getting it wrong in the permissive direction means a
	// consignment that needed a bill did not get one, which is a detained vehicle
	// and a penalty; getting it wrong the other way means an unnecessary bill,
	// which costs nothing but a minute. The asymmetry decides the tie, and it is
	// recorded here because the reading is a judgement and not a citation.
	a.Required = total.GreaterThanOrEqual(threshold)
	if a.Required {
		a.Basis = append(a.Basis, fmt.Sprintf(
			"Consignment value %s is at or above the Rs %d threshold, so an e-way bill is required. "+
				"(The threshold is applied as \"at or above\" rather than \"exceeding\": the sources phrase "+
				"it as exceeding, and this build takes the conservative reading because a missing bill is a "+
				"detained vehicle while an unnecessary one costs a minute.)",
			total.StringFixed(2), ConsignmentThresholdRupees))
	} else {
		a.Basis = append(a.Basis, fmt.Sprintf(
			"Consignment value %s is below the Rs %d threshold, so no e-way bill is required on value "+
				"alone. ⚠️ This build does NOT apply any State-specific relaxation or the exempt-goods "+
				"carve-out in the rule's own explanation of \"consignment value\" — check both before "+
				"relying on this.",
			total.StringFixed(2), ConsignmentThresholdRupees))
	}

	// --- 2. IS PART B REQUIRED?
	intraState := c.FromStateCode != "" && c.FromStateCode == c.ToStateCode
	shortHop := c.DistanceKm > 0 && c.DistanceKm <= PartBExemptDistanceKm
	a.PartBRequired = !(intraState && shortHop)
	switch {
	case !a.PartBRequired:
		a.Basis = append(a.Basis, fmt.Sprintf(
			"Part B (the vehicle number) is not required: the movement is within State %s and the stated "+
				"distance of %d km is within the %d km relaxation.",
			c.FromStateCode, c.DistanceKm, PartBExemptDistanceKm))
	case intraState && c.DistanceKm == 0:
		// ⛔ An unstated distance cannot buy the relaxation. Saying "within the
		// State, so Part B is optional" without a distance would exempt a
		// 900 km intra-State haul.
		a.Basis = append(a.Basis, fmt.Sprintf(
			"Part B is required: the movement is within State %s, but no distance was stated, so the "+
				"%d km relaxation cannot be applied — it is a relaxation for SHORT hops and an unstated "+
				"distance is not a short one.",
			c.FromStateCode, PartBExemptDistanceKm))
	case !intraState:
		a.Basis = append(a.Basis,
			"Part B is required: the movement crosses a State boundary, and the distance relaxation "+
				"applies only within a State.")
	default:
		a.Basis = append(a.Basis, fmt.Sprintf(
			"Part B is required: the stated distance of %d km exceeds the %d km relaxation.",
			c.DistanceKm, PartBExemptDistanceKm))
	}
	if a.PartBRequired && strings.TrimSpace(c.VehicleNumber) == "" {
		a.Blockers = append(a.Blockers,
			"Part B is required for this movement and no vehicle number was supplied. The goods may not "+
				"move on this bill until one is added.")
	}

	// --- 3. HOW LONG IS IT VALID?
	perDay := ValidityKmPerDayNormal
	cargo := "ordinary cargo"
	if c.OverDimensionalCargo {
		perDay, cargo = ValidityKmPerDayODC, "OVER-DIMENSIONAL cargo"
	}
	if c.DistanceKm <= 0 {
		a.Blockers = append(a.Blockers,
			"No distance was stated, so the validity period cannot be computed. It is NOT assumed to be "+
				"one day: a bill whose expiry the system guessed is a bill that expires without warning, "+
				"and an expired e-way bill on a live consignment is a detained vehicle.")
		a.Basis = append(a.Basis,
			"Validity: NOT COMPUTED — no distance stated.")
		return a
	}
	// One day per perDay km, ROUNDED UP, with a floor of one day: 250 km of
	// ordinary cargo is two days, not 1.25. Ceil and not round-half, because
	// rounding 250/200 down to one day would expire the bill mid-journey.
	a.ValidityDays = int(math.Ceil(float64(c.DistanceKm) / float64(perDay)))
	if a.ValidityDays < 1 {
		a.ValidityDays = 1
	}
	a.ValidUntil = c.GeneratedAt.AddDate(0, 0, a.ValidityDays)
	a.Basis = append(a.Basis, fmt.Sprintf(
		"Validity %d day(s), to %s: %d km at one day per %d km for %s, rounded UP (a part-day is a "+
			"whole day, because rounding down would expire the bill mid-journey). ⚠️ The cargo class is "+
			"ASSERTED BY THE OPERATOR and is never inferred — nothing in a voucher says whether a "+
			"consignment is over-dimensional, and assuming ordinary cargo would grant ten times the "+
			"validity the rule allows.",
		a.ValidityDays, a.ValidUntil.Format("2006-01-02"), c.DistanceKm, perDay, cargo))
	return a
}

// BuildEWayBill converts a ConsignmentSnapshot into an NIC e-way bill payload.
//
// A PURE FUNCTION, exactly like BuildEInvoice: no clock, no network, no database.
// That is what makes the golden test a byte comparison rather than a fixture
// dance.
//
// ⛔ IT DOES NOT REFUSE AN INCOMPLETE CONSIGNMENT. Building the payload and
// VALIDATING it are separate acts, as they are for the e-invoice: a caller that
// wants to show an operator everything wrong with a movement needs the payload
// built so ValidateEWayBill can list every problem, rather than being stopped at
// the first. Assess reports the statutory blockers; ValidateEWayBill reports the
// schema ones.
func BuildEWayBill(c ConsignmentSnapshot) EWayBillPayload {
	inv := c.Invoice
	items := make([]EWayBillItem, 0, len(inv.Lines))
	for _, ln := range inv.Lines {
		items = append(items, EWayBillItem{
			ProductName:   ln.Desc,
			HSNCode:       ln.HSN,
			Quantity:      ln.Qty,
			QtyUnit:       ln.Unit,
			TaxableAmount: Fixed2(ln.Taxable),
			// The RATE, not the amount, which is what the e-way bill schema takes
			// where the e-invoice takes both. Split evenly across CGST/SGST for an
			// intra-State movement and carried whole on IGST otherwise — decided by
			// which tax the invoice actually bears rather than by re-deriving the
			// place of supply here, because that derivation already happened when the
			// invoice was posted and a second copy of it would drift.
			CgstRate: halfRateIfLocal(ln.GstRatePct, ln.CGST),
			SgstRate: halfRateIfLocal(ln.GstRatePct, ln.SGST),
			IgstRate: fullRateIfIGST(ln.GstRatePct, ln.IGST),
		})
	}
	return EWayBillPayload{
		Version: ewayBillSchemaVersion,
		// Outward supply of type Supply. This slice emits the ordinary case and
		// nothing else; a job-work or export movement is a different subSupplyType
		// and is not built rather than guessed at.
		SupplyType:    "O",
		SubSupplyType: "1",
		DocType:       "INV",
		DocNo:         inv.VoucherNo,
		DocDate:       nicDate(inv.VoucherDate),

		FromGstin:     inv.SellerGstin,
		FromTrdName:   inv.SellerLegalName,
		FromStateCode: c.FromStateCode,
		FromPincode:   c.FromPincode,

		ToGstin:     inv.BuyerGstin,
		ToTrdName:   inv.BuyerLegalName,
		ToStateCode: c.ToStateCode,
		ToPincode:   c.ToPincode,

		ItemList: items,

		TotalValue:  Fixed2(inv.Taxable),
		CgstValue:   Fixed2(inv.CGST),
		SgstValue:   Fixed2(inv.SGST),
		IgstValue:   Fixed2(inv.IGST),
		TotInvValue: Fixed2(inv.InvoiceTotal),

		TransporterID:   c.TransporterID,
		TransporterName: c.TransporterName,
		TransDistance:   fmt.Sprintf("%d", c.DistanceKm),
		TransMode:       nicTransportCode[c.Mode],
		VehicleNo:       strings.ToUpper(strings.ReplaceAll(c.VehicleNumber, " ", "")),
	}
}

// halfRateIfLocal returns the per-head GST rate when the line actually bears that
// head, and "0" otherwise.
//
// The e-way bill schema takes RATES where the e-invoice takes amounts, and an
// 18% intra-State line is 9 + 9. Keyed on whether the AMOUNT is non-zero rather
// than on a re-derived place of supply, because that derivation happened when the
// invoice was posted and a second copy would drift from it.
func halfRateIfLocal(ratePct, headAmount string) string {
	if isZeroAmount(headAmount) {
		return "0"
	}
	r, err := decimal.NewFromString(ratePct)
	if err != nil {
		return ratePct
	}
	return r.Div(decimal.NewFromInt(2)).String()
}

// fullRateIfIGST returns the whole rate on an inter-State line, "0" otherwise.
func fullRateIfIGST(ratePct, igstAmount string) string {
	if isZeroAmount(igstAmount) {
		return "0"
	}
	return ratePct
}

func isZeroAmount(s string) bool {
	if strings.TrimSpace(s) == "" {
		return true
	}
	d, err := decimal.NewFromString(s)
	if err != nil {
		return true
	}
	return d.IsZero()
}

// nicDate converts an ISO date (YYYY-MM-DD) to NIC's DD/MM/YYYY.
//
// ⚠️ AN UNPARSEABLE DATE IS RETURNED UNCHANGED rather than zeroed, so
// ValidateEWayBill can report it as malformed. Substituting a placeholder here
// would put a plausible-looking wrong date on a statutory document, and the
// validator would have nothing to complain about.
func nicDate(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return t.Format("02/01/2006")
}

// EWayBillResult is what a provider returns, or what the fake stands in for.
type EWayBillResult struct {
	// LocalReference is a DETERMINISTIC local identifier. ⛔ IT IS NOT AN EBN and
	// must never be labelled as one: an EBN is issued by NIC, and a document
	// carrying a fabricated one tells a driver at a checkpost that the consignment
	// is covered when it is not.
	LocalReference string
	ValidUntil     time.Time
	// Provider is which adapter answered, so an artefact can say whether it was
	// the real NIC or the stand-in. The whole disclosure rests on this being
	// carried rather than assumed.
	Provider string
	// Disclosures is what must reach the artefact's face.
	Disclosures []string
}

// EWayBillDisclosures is the full block for one assessed consignment: the
// no-submission statement, the rule provenance, and any statutory blockers.
//
// ⛔ THE ORDER IS DELIBERATE. The no-submission statement comes FIRST, because it
// is the one a reader must not miss, and a reader stops reading. The IRN case made
// the same call: invQRNotPrintedNotice leads with what the document is NOT.
func EWayBillDisclosures(a Assessment, providerName string) []string {
	out := []string{
		"⛔ THIS IS NOT A GOVERNMENT-ISSUED e-WAY BILL. No submission was made to the NIC e-Way Bill " +
			"portal, which is unreachable from this deployment. The identifier on this document is a " +
			"LOCAL REFERENCE generated by \"" + providerName + "\" and is NOT an EBN. Presenting it as one " +
			"would tell a checkpost that this consignment is covered when it is not.",
	}
	out = append(out, Rule138Disclosures()...)
	for _, b := range a.Blockers {
		out = append(out, "BLOCKER: "+b)
	}
	out = append(out, a.Basis...)
	return out
}
