package masters

// THE CHALLAN WRITE PATH — recording what tax was actually DEPOSITED.
//
// Migrations 0069 (tds_challan + tds_challan_allocation) and 0070 shipped, and
// internal/reporting/tds_return_source.go reads them, but NOTHING WROTE THEM:
// every test seeded a deposit by raw SQL. So on a real deployment T6 could
// deduct tax, store the deduction and show it in a register — and could never
// record the deposit, which means Form140StatementFor refused every quarter with
// ErrTaxNotDeposited and no operator had any way to fix it. This file is that
// gap. It is the write side of 0069; reporting is the read side.
//
// A challan is a bank deposit of tax ALREADY DEDUCTED. Recording one is what
// makes a return possible at all: Deductee Detail field 5 of the prescribed file
// is a CHALLAN REFERENCE (migration 0069's header, lines 14-17), so no deductee
// row can exist outside a challan.
//
// ===========================================================================
// ⛔ THE CREATE IS ATOMIC, AND THERE IS NOW AN ALLOCATE-LATER. READ WHY.
// ===========================================================================
// RecordTDSChallan writes the challan AND the allocations it was keyed with in
// ONE RunInTx. AllocateToRecordedChallan attaches FURTHER allocations to a
// challan that is already recorded. There is still no edit route and no delete
// route (see below).
//
// This file used to argue that allocate-later must not exist at all, on the
// ground that "splitting the two into separate requests would make a
// half-recorded deposit reachable through the front door". ⛔ THE HALF-RECORDED
// DEPOSIT WAS ALREADY REACHABLE — this file never required a challan's
// allocations to COVER its tax, only that a challan with tax names at least one
// deduction. So a ₹27,000 deposit that pays two ₹13,500 deductions could be
// keyed naming only the first, and the second ₹13,500 could then NEVER be
// attached to it: reporting.Form140StatementFor refuses that deduction with
// ErrTaxNotDeposited, and re-depositing is not an option because the money has
// already been paid. The parity audit DROVE exactly this and recorded it as
// register row D1: seven in-browser doors and six HTTP methods were tried
// against challan 05130 and its unallocated balance stayed at exactly
// 12000.0000. The refusal to build the route did not prevent the half-recorded
// state; it made the half-recorded state PERMANENT.
//
// So the shape of the atomicity argument is kept and its conclusion is not: an
// allocate-later request is itself ONE RunInTx that verifies every allocation
// against the live deduction (or collection) and against the deposit it is being
// attached to, and it is refused whole. What it may never do is leave a challan
// claiming to have paid more tax than it deposited — see
// overAllocationRefusal, which BOTH write paths call.
//
// STILL NOT BUILT, and stated plainly rather than discovered later: there is no
// route that MOVES an allocation from one challan to another, corrects a CIN or
// an amount, or deletes anything. A challan keyed wrong must still be corrected
// in the database. The right shape for that remains a reviewable correction
// restating the whole challan and its allocations together, not four small
// mutating routes bolted on.
//
// ===========================================================================
// ⚠️ WHERE THIS FILE CONTRADICTS MIGRATION 0069'S OWN HEADER — READ THIS
// ===========================================================================
// 0069 lines 203-210 say, of the one-challan-per-deduction constraint: "A part
// payment is modelled as an allocation smaller than the deduction, which the
// return reports through the 'has the tax been deposited' flag."
//
// THAT SENTENCE IS WRONG, and this file refuses what it describes.
//
// internal/reporting's challanRefusalFor (the function that raises
// ErrAllocationBrokenByAlter, and which the register's status panel calls too)
// tests
//
//	if !r.allocated.Equal(r.tax) { ... ErrAllocationBrokenByAlter ... }
//
// — INEQUALITY, in either direction. So a deliberately partial allocation is
// INDISTINGUISHABLE at return time from an allocation broken by a voucher ALTER,
// and the whole quarter then refuses to file with a message about the wrong
// cause: "the voucher has been altered since the tax was paid", about a voucher
// nobody touched. The operator is sent to look for a change that never happened.
//
// Storing the allocated amount IS the alter-detection mechanism (0069 lines
// 220-226, and 0045's precedent: store what you matched), so equality is the
// thing that makes it work. Given the choice between the two, refusing a partial
// allocation AT ENTRY — where the operator is holding the challan and can fix
// it — beats refusing the entire quarter at FILING time for a misleading reason.
//
// So: allocated_amount_rupees MUST EQUAL the deduction's tds_amount_rupees, and
// a mismatch is ErrTDSAllocationMismatch naming both numbers and the voucher.
// This needs a human eye: either 0069's header is corrected, or partial payment
// is modelled properly (which would need a distinguishable third state that
// reporting can tell apart from an alter — a schema change, not a Go change).
//
// ===========================================================================
// ⛔ A DEPOSIT MAY NOT PAY FOR MORE TAX THAN IT CONTAINS (register row D2)
// ===========================================================================
// Nothing compared the sum of a challan's allocations against its own tax box,
// and the parity audit drove the consequence: a deposit of ₹1 was accepted as
// having "paid" ₹27,000.0000 of tax across two deductees. Both deductions were
// then marked "already deposited" — so the operator could never allocate the
// real deposit to them — unallocated_tax_rupees went to -26999.0000, and the
// screen captioned that negative as the ordinary paise remainder, because a
// small negative IS the ordinary case here (see UnallocatedTaxRupees).
//
// THE TOLERANCE IS DERIVED, NOT CHOSEN. The challan's five amount boxes are
// WHOLE RUPEES (the format forbids paise in them) while
// tds_deduction.tds_amount_rupees is a 4-dp figure — ₹33,333 at 10% is
// ₹3,333.30 — so the whole-rupee deposit made against ONE deduction can be at
// most a rupee away from it, and the sum of N allocations can therefore exceed a
// legitimate deposit by STRICTLY LESS THAN ₹N. That bound is the refusal:
// allocated - tax >= N rupees is impossible from rounding and is refused;
// anything under it is the ordinary case and is reported, not refused.
//
// ⚠️ IT IS NOT THE EQUALITY THE HANDLER'S HEADER FORBIDS. Requiring the sum to
// EQUAL the tax would refuse legitimate rounded deposits and would also refuse
// the deliberately-partial state that allocate-later exists to finish; UNDER-
// allocation stays legal and is exactly what UnallocatedTaxRupees reports.
// Only the over-claim is refused, because an over-claim is a statement to the
// department that money reached the Government when it did not.
//
// ===========================================================================
// TCS: A COLLECTION IS DEPOSITED THE SAME WAY, AND MAY NOT SHARE A CHALLAN
// ===========================================================================
// tcs_collection (migration 0067) is the collection-side mirror of 0050's
// tds_deduction and carries the SAME natural key — (voucher_id, section,
// leg_seq). Until register row D4 it was not part of this mechanism at all, so a
// collector could record what it had collected and never record that it had
// deposited it: a live 2000.0000 collection under 206C(1)-SCRAP appeared in no
// allocation picker and could be attached to no challan.
//
// An allocation therefore names a KIND. There is no `kind` column in migration
// 0069 and none is needed: the two section vocabularies are disjoint by CHECK
// ('194…' against '206C(1)-SCRAP' / '206C(1F)-MV'), so the natural key still
// identifies exactly one row across the two tables, and the kind is recovered on
// read by which table matched.
//
// ⛔ BUT ONE CHALLAN MAY NOT CARRY BOTH KINDS, and that is not tidiness.
// reporting/tds_return_source.go reaches a challan THROUGH tds_deduction and
// files the challan's WHOLE tax_rupees as the Form 140 challan's
// TaxDeductedRupees (groupByChallan, `TaxDeductedRupees: r.chTax`). A challan
// that had also paid TCS would therefore over-state the deduction return's
// challan total by the collection half — discovered by the FVU at the portal
// against the .csi file, which is the most expensive place to find out. The two
// returns are separate documents; so are their deposits.
//
// ===========================================================================
// WHY THIS VALIDATES IN GO WHEN THE TABLE ALREADY HAS SEVEN CHECKS
// ===========================================================================
// Same reason as deductor_status.go: the CHECK is the authority and it stays.
// Without the Go checks below, an operator who types a six-digit BSR code is
// told "new row for relation tds_challan violates check constraint
// tds_challan_bsr_shape", which tells a shopkeeper nothing about the deposit
// slip in their hand. Every validation here names the constraint it shadows.
//
// ===========================================================================
// ⛔ LEADING ZEROS ARE SIGNIFICANT IN BOTH IDENTIFIERS
// ===========================================================================
// BSR code 0004329 and challan serial 05122 are the values in the census
// fixture (internal/platform/testdb/testdb_test.go) because that is what these
// identifiers look like. Both columns are TEXT and both stay TEXT all the way
// through this file and the JSON above it: one strconv.Atoi anywhere on this
// path turns 0004329 into 4329 — a different branch of a different bank — and
// the FVU discovers it against the .csi file at the portal, not here.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// ErrInvalidTDSChallan is returned for a challan this layer refuses to store on
// its own facts — a malformed CIN, paise in an amount, a missing minor head. A
// sentinel so the HTTP layer answers 400 rather than 500.
var ErrInvalidTDSChallan = errors.New("masters: invalid TDS challan")

// ErrDuplicateTDSChallanCIN is the same CIN recorded twice.
//
// A REFUSAL, NOT AN UPSERT, and that is the one place this file deliberately
// does NOT copy tcs_declaration.go next door. A declaration re-stated is a
// correction of one document; a CIN re-stated is THE SAME REAL PAYMENT ENTERED
// TWICE. The return's batch total is the sum of the challan deposits (migration
// 0069 lines 43-50), so a duplicate over-claims a deposit that never happened —
// and the FVU fails it against the .csi file at the portal, not here, which is
// the most expensive place to find out. An upsert would additionally destroy the
// first challan's allocations by silently re-pointing them.
var ErrDuplicateTDSChallanCIN = errors.New("masters: this challan (CIN) is already recorded")

// ErrNoSuchDeduction is an allocation naming a (voucher, section, leg_seq)
// triple no deduction has.
//
// ⛔ NOTHING IN THE DATABASE STOPS THIS. Migration 0069 deliberately carries NO
// foreign key to tds_deduction — internal/ledger/tds.go DELETEs and re-derives
// every tds_deduction row with fresh ids on each voucher ALTER, so a cascade
// would destroy the record of a real payment and a restrict would make the
// voucher unalterable once its tax was deposited (0069 lines 178-198). The price
// of that correct decision is that the natural key is UNENFORCED, so this layer
// verifies it inside the same transaction. An unmatched triple is a named
// refusal, never a stored orphan: an orphan allocation is a deposit recorded
// against nothing, which inflates the register and reconciles to no deductee.
var ErrNoSuchDeduction = errors.New(
	"masters: no deduction matches the voucher, section and leg this deposit was allocated to")

// ErrTDSAllocationMismatch is an allocated amount that is not the deduction's
// tax. See the file header for why this is a refusal and where it contradicts
// migration 0069's own header.
var ErrTDSAllocationMismatch = errors.New(
	"masters: the amount allocated is not the tax that was deducted")

// ErrDeductionAlreadyPaid is a deduction that another challan has already paid.
//
// Shadows tds_challan_allocation_one_per_deduction. The constraint is the
// authority; this exists so the operator is told WHICH challan already paid it
// rather than reading a constraint name — and because the answer ("you have
// already deposited this") is usually that they are about to double-pay.
var ErrDeductionAlreadyPaid = errors.New(
	"masters: that deduction is already paid by another challan")

// ErrChallanOverAllocated is a challan whose allocations claim MORE tax than the
// deposit slip says was paid. See the file header for why the tolerance is one
// rupee per allocation and why this is not an equality.
//
// A SEPARATE SENTINEL from ErrTDSAllocationMismatch, which is about ONE
// allocation disagreeing with ONE deduction. This one is about the challan as a
// whole and every individual allocation on it can be correct while it fires —
// collapsing the two would send the operator to check a line that is right.
var ErrChallanOverAllocated = errors.New(
	"masters: this challan's allocations claim more tax than it deposited")

// ErrMixedChallanKinds is one challan asked to pay both a deduction and a
// collection. The file header says why the return format makes that unfilable.
var ErrMixedChallanKinds = errors.New(
	"masters: one challan cannot pay both TDS deductions and TCS collections")

// AllocationKind says which side of the statute an allocation pays.
//
// It is NOT a column in migration 0069 and must not become one without a
// migration: it is recovered on read from which of the two tables carries the
// natural key, and the two section vocabularies are disjoint by CHECK.
type AllocationKind string

const (
	// AllocationKindTDS is a deduction (migration 0050's tds_deduction). It is
	// the ZERO VALUE's meaning — see normaliseAllocationKind for why defaulting
	// is safe here and is not safe for leg_seq next door.
	AllocationKindTDS AllocationKind = "TDS"
	// AllocationKindTCS is a collection (migration 0067's tcs_collection).
	AllocationKindTCS AllocationKind = "TCS"
)

// normaliseAllocationKind trims and upper-cases, and treats the EMPTY string as
// TDS.
//
// ⚠️ AN OMITTED FIELD IS DEFAULTED HERE, WHICH tdsChallanAllocationReq.LegSeq
// DELIBERATELY REFUSES TO DO. The difference is whether the wrong default is
// DETECTED: an omitted leg_seq silently means leg 0, which is a real leg, and
// nothing downstream catches it. An omitted kind means TDS, and if the operator
// meant a collection the lookup in tds_deduction finds NOTHING and the request
// is refused by name with ErrNoSuchDeduction — the sections cannot collide,
// because 0050's and 0067's CHECKs share no value. A default that cannot pass
// silently is a default; a default that can is a guess.
func normaliseAllocationKind(k AllocationKind) (AllocationKind, bool) {
	switch AllocationKind(strings.ToUpper(strings.TrimSpace(string(k)))) {
	case "", AllocationKindTDS:
		return AllocationKindTDS, true
	case AllocationKindTCS:
		return AllocationKindTCS, true
	}
	return "", false
}

// minorHeadDescriptions is migration 0069's tds_challan_minor_head_known CHECK,
// with the description each code carries in that migration's own comment (lines
// 102-106). Nothing is invented here and nothing is defaulted: see
// validateTDSChallan for why 200 is not a fallback.
//
// Ordered, and the order is the CHECK's order. A map would render the picker
// differently on every request.
var minorHeadDescriptions = []MinorHead{
	{Code: "200", Description: "tax the deductor pays over of its own motion"},
	{Code: "400", Description: "a demand raised on assessment"},
	{Code: "100", Description: "advance tax"},
}

// MinorHead is one Annexure 7 minor head, served to the client.
type MinorHead struct {
	// Code is what is stored: '200', '400' or '100'.
	Code string
	// Description is what the code MEANS, and it is served because the operator
	// has to choose one and there is no default. A picker of three bare numbers
	// invites whichever is first.
	Description string
}

// TDSChallanMinorHeads returns the three minor heads a challan may carry.
//
// SERVED, never hard-coded in the client, for the reason
// handler_deductor_status.go gives for the entity categories: a list copied into
// TypeScript is a copy that can drift from the CHECK, and a client that offers a
// fourth value would fail on save. Returns a copy so a caller cannot mutate the
// vocabulary.
func TDSChallanMinorHeads() []MinorHead {
	out := make([]MinorHead, len(minorHeadDescriptions))
	copy(out, minorHeadDescriptions)
	return out
}

// isKnownMinorHead reports whether code is one of the three.
func isKnownMinorHead(code string) bool {
	for _, m := range minorHeadDescriptions {
		if m.Code == code {
			return true
		}
	}
	return false
}

// bsrCodePattern and challanNoPattern mirror migration 0069's
// tds_challan_bsr_shape and tds_challan_no_shape CHECKs EXACTLY, including the
// anchors. A Go check looser than the constraint would produce the raw
// constraint violation this file exists to prevent.
var (
	bsrCodePattern   = regexp.MustCompile(`^[0-9]{7}$`)
	challanNoPattern = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// TDSChallanAllocationInput is one deduction this deposit paid.
type TDSChallanAllocationInput struct {
	// VoucherID, Section and LegSeq are tds_deduction's NATURAL key (migration
	// 0050's tds_deduction_one_per_leg: UNIQUE (company_id, voucher_id, section,
	// leg_seq)) — never its surrogate id, which ledger/tds.go regenerates on
	// every ALTER.
	VoucherID uuid.UUID
	Section   string
	LegSeq    int
	// Kind says whether the triple names a DEDUCTION (0050) or a COLLECTION
	// (0067). Empty means TDS — see normaliseAllocationKind for why that default
	// cannot pass silently.
	Kind AllocationKind
	// AmountRupees is what the operator says was deposited against that
	// deduction. It is NOT derived from tds_deduction on purpose: deriving it
	// would make the stored figure a cache of the deduction and destroy the
	// alter-detection mechanism 0069 built it for, and it would silently accept
	// a challan whose real total the operator has mis-added. It must EQUAL the
	// deduction's tax — see the file header.
	AmountRupees decimal.Decimal
}

// TDSChallanInput is one deposit and everything it paid for. Written atomically.
type TDSChallanInput struct {
	// ---- The CIN: what identifies this payment to the department. ----------
	// BSRCode is seven digits as TEXT (or, under ByBookEntry, the seven-digit
	// Form 24G receipt number — the file format overloads one field for both).
	// LEADING ZEROS ARE SIGNIFICANT: see the file header.
	BSRCode string
	// DepositDate is yyyy-mm-dd, the date tax was PAID to the Government. Not
	// the deduction date and not the voucher date.
	DepositDate string
	// ChallanNo is 1 to 5 digits as TEXT. Leading zeros significant.
	ChallanNo string
	// ByBookEntry selects the format's mode of payment: book adjustment for a
	// transfer voucher, bank challan otherwise.
	ByBookEntry bool

	// ---- The deposit, split the way the challan splits it. -----------------
	// Four SEPARATE FACTS, not a total with a breakdown: only TaxRupees
	// discharges the deduction liability (migration 0069 lines 90-96). All four
	// are non-negative WHOLE RUPEES, and their sum must exceed zero.
	TaxRupees      decimal.Decimal
	InterestRupees decimal.Decimal
	FeeRupees      decimal.Decimal
	PenaltyRupees  decimal.Decimal

	// MinorHead is '200', '400' or '100'. MANDATORY, and there is NO DEFAULT
	// anywhere on this path.
	MinorHead string
	// TaxYearStart is the tax year the DEPOSIT BELONGS TO (2026 = 2026-27).
	// STORED, never computed from DepositDate: a challan paid on 7 April can be
	// for March of the previous year and the deposit date alone cannot tell you
	// which (migration 0069 lines 109-113).
	TaxYearStart int

	// Allocations are the deductions this challan paid. Required when TaxRupees
	// is positive and forbidden when it is zero — see validateTDSChallan.
	Allocations []TDSChallanAllocationInput
}

// TDSChallanAllocationRecord is one allocation read back, with the LIVE
// deduction beside it so the register can show a broken allocation.
type TDSChallanAllocationRecord struct {
	VoucherID uuid.UUID
	// VoucherNo is the current version's number, which is what an operator
	// recognises. Empty if the voucher cannot be resolved.
	VoucherNo string
	Section   string
	LegSeq    int
	// Kind is TDS or TCS, recovered from WHICH table carries this natural key.
	// It is TDS for an allocation whose deduction has gone (DeductionOnRecord
	// false), because every allocation recorded before migration 0067 joined the
	// mechanism is a deduction and an orphan cannot say otherwise.
	Kind AllocationKind
	// DeducteeName is who was paid — or, for a TCS allocation, who was collected
	// from. Empty when DeductionOnRecord is false.
	DeducteeName string
	// AllocatedRupees is what was recorded as deposited against this deduction.
	AllocatedRupees decimal.Decimal
	// DeductionOnRecord is false when no tds_deduction row carries this triple
	// any more — an ALTER removed the leg entirely. The allocation SURVIVES
	// that (there is no FK, by design) and must be visible, not silently
	// dropped.
	DeductionOnRecord bool
	// DeductionTaxRupees is the tax the deduction shows NOW. Meaningful only
	// when DeductionOnRecord.
	DeductionTaxRupees decimal.Decimal
	// BrokenByAlter is the 0045 mechanism surfaced for a screen: the deduction
	// has gone, or it no longer shows the amount that was deposited against it.
	//
	// ⚠️ ADVISORY. reporting.Form140StatementFor applies the SAME test and
	// REFUSES the quarter (ErrAllocationBrokenByAlter). This flag exists so the
	// register shows it before filing day; the refusal at filing time remains
	// the authority. Two copies of one test, and that is a real drift risk —
	// noted deliberately rather than hidden.
	BrokenByAlter bool
}

// TDSChallanRecord is one challan read back, with what it paid for.
type TDSChallanRecord struct {
	ID uuid.UUID
	// The CIN parts.
	BSRCode     string
	DepositDate string
	ChallanNo   string
	ByBookEntry bool
	// The four amounts, as stored.
	TaxRupees      decimal.Decimal
	InterestRupees decimal.Decimal
	FeeRupees      decimal.Decimal
	PenaltyRupees  decimal.Decimal
	// TotalDepositedRupees is the sum of the four. DERIVED, never stored: a
	// stored total is a column that disagrees with its parts after the first
	// correction.
	TotalDepositedRupees decimal.Decimal
	MinorHead            string
	// MinorHeadDescription is the served description of MinorHead, so a list
	// row can read "400 — a demand raised on assessment" without the client
	// holding a fourth copy of the vocabulary.
	MinorHeadDescription string
	TaxYearStart         int

	// AllocationCount and AllocatedTotalRupees answer "what did this deposit
	// actually pay for", which is the register's whole question.
	AllocationCount      int
	AllocatedTotalRupees decimal.Decimal
	// UnallocatedTaxRupees is TaxRupees - AllocatedTotalRupees.
	//
	// ⚠️ IT CAN BE NEGATIVE, AND THAT IS NOT A BUG. The challan amounts are
	// WHOLE RUPEES (the format forbids paise in all five challan fields) while
	// tds_deduction.tds_amount_rupees is NUMERIC(19,4) rounded to 4 dp by
	// compliance.TDSDerive — ₹33,333 at 10% is ₹3,333.30. A deposit of ₹3,333
	// against it leaves +0.30 here and a deposit of ₹3,334 leaves -0.70. So this
	// layer imposes NO relation between the sum of the allocations and the
	// challan's tax; it reports the difference and lets a human read it. See the
	// notes in the handoff: whether the format requires those to agree is
	// UNVERIFIED here, and guessing an equality would make legitimate rounded
	// deposits unrecordable.
	UnallocatedTaxRupees decimal.Decimal

	Allocations []TDSChallanAllocationRecord

	RecordedBy string
	RecordedAt string
}

// isWholeRupees mirrors migration 0069's tds_challan_amounts_whole_rupees CHECK,
// which is `amount = trunc(amount)` — the same test, not an approximation of it.
func isWholeRupees(d decimal.Decimal) bool { return d.Equal(d.Truncate(0)) }

// allocationTriple is the natural key, used to spot a request that names the
// same deduction twice.
type allocationTriple struct {
	voucherID uuid.UUID
	section   string
	legSeq    int
}

// validateTDSChallan produces the sentence an operator can act on. It is PURE —
// no database, no clock — which is what makes it testable without Postgres.
//
// It returns the challan and allocations NORMALISED (trimmed), because a BSR
// code with a trailing space is the same deposit slip and a stored one would
// make the CIN uniqueness check miss its own duplicate.
func validateTDSChallan(in TDSChallanInput) (TDSChallanInput, error) {
	out := in
	out.BSRCode = strings.TrimSpace(in.BSRCode)
	out.ChallanNo = strings.TrimSpace(in.ChallanNo)
	out.MinorHead = strings.TrimSpace(in.MinorHead)
	out.DepositDate = strings.TrimSpace(in.DepositDate)

	// tds_challan_bsr_shape. The sentence has to say TEXT and it has to say
	// leading zeros, because the operator's instinct on seeing 0004329 rejected
	// is to drop the zeros.
	if !bsrCodePattern.MatchString(out.BSRCode) {
		// ⚠️ IT REPORTS WHICH RULE FAILED, NOT len(). This used to print the
		// LENGTH — "the BSR code is EXACTLY SEVEN DIGITS and \"ABCDEFG\" is 7" —
		// a sentence asserting the value satisfies the rule it is being refused
		// for, leaving the operator nothing to change.
		why := fmt.Sprintf("it is %d characters long", len(out.BSRCode))
		if len(out.BSRCode) == 7 {
			why = "it is seven characters but not all of them are digits"
		}
		return out, fmt.Errorf("%w: the BSR code is EXACTLY SEVEN DIGITS and %q fails that — %s. "+
			"It is the code of the bank branch that received the deposit (or, for a book-entry "+
			"deposit, the seven-digit Form 24G receipt number). LEADING ZEROS ARE PART OF IT: "+
			"0004329 is a valid code and 4329 is a different branch",
			ErrInvalidTDSChallan, out.BSRCode, why)
	}
	// tds_challan_no_shape. The format sizes this field at 5.
	if !challanNoPattern.MatchString(out.ChallanNo) {
		return out, fmt.Errorf("%w: the challan serial number is ONE TO FIVE DIGITS and %q is not — "+
			"it is the serial the bank printed on the counterfoil. LEADING ZEROS ARE PART OF IT: "+
			"05122 is not 5122",
			ErrInvalidTDSChallan, out.ChallanNo)
	}
	if _, err := time.Parse("2006-01-02", out.DepositDate); err != nil {
		return out, fmt.Errorf("%w: the deposit date %q is not yyyy-mm-dd. It is the date the tax "+
			"was PAID TO THE GOVERNMENT — not the date of the voucher and not the date of the "+
			"deduction", ErrInvalidTDSChallan, out.DepositDate)
	}

	// tds_challan_minor_head_known — and the reason there is no default.
	if out.MinorHead == "" {
		return out, fmt.Errorf("%w: the minor head is required and there is NO DEFAULT. 200 is tax "+
			"paid over of the deductor's own motion, 400 is a demand raised on assessment, 100 is "+
			"advance tax. Defaulting to 200 would assert on this company's behalf that the deposit "+
			"was voluntary rather than demanded, which is a statement about its assessment history "+
			"that this software has no business making — migration 0069 forbids it explicitly",
			ErrInvalidTDSChallan)
	}
	if !isKnownMinorHead(out.MinorHead) {
		return out, fmt.Errorf("%w: %q is not an Annexure 7 minor head; it is one of 200 (paid of "+
			"the deductor's own motion), 400 (a demand raised on assessment) or 100 (advance tax)",
			ErrInvalidTDSChallan, out.MinorHead)
	}
	// ⚠️ WHAT A SUCCESSFUL SAVE OF MINOR HEAD 100 DOES NOT PROMISE, stated here
	// beside the check that lets it through rather than discovered at filing time.
	//
	// form140's challanRules attach TWO EXTRA RULES to minor head 100 that this
	// layer cannot fully enforce:
	//
	//	1. the deposit date must fall INSIDE the quarter being filed, and
	//	2. it must not be later than the payment date of any deductee mapped to it.
	//
	// RULE 1 IS NOT CHECKABLE HERE, and that is a property of the schema rather
	// than an omission: a challan stores a TAX YEAR, not a quarter (0069, and see
	// the tax-year check below for why the year is stored and never derived), so
	// at write time the quarter this deposit will be filed in is not yet decided.
	//
	// So an advance-tax challan dated outside the quarter saves cleanly, reads
	// back cleanly, and is refused MONTHS LATER by form140 with "challan N carries
	// minor head 100, so its date must fall within Q1". The register's return-file
	// panel is where that surfaces (reporting.ReturnFileStatusFor), which is the
	// same shape of late refusal this file's header argues against for partial
	// allocations — recorded here as a known gap rather than left to be found.
	//
	// Rule 2 IS partly checkable, because allocateToDeduction already reads the
	// deduction row and therefore its payment date. It is deliberately NOT checked
	// in this slice: closing half a rule invites the reader to believe the whole
	// one is closed, which is the defect the back-dated-cutoff prose just made.

	// tds_challan_tax_year_sane — and the note that it is not derived.
	if out.TaxYearStart < 2025 || out.TaxYearStart > 2200 {
		return out, fmt.Errorf("%w: tax year %d is outside 2025–2200. This is the year the DEPOSIT "+
			"BELONGS TO, which is not always the year the deposit date falls in — a challan paid on "+
			"7 April 2026 is normally for March of 2025-26, so it is recorded and never computed "+
			"from the date", ErrInvalidTDSChallan, out.TaxYearStart)
	}

	// tds_challan_amounts_non_negative and tds_challan_amounts_whole_rupees.
	// Four separate names in the sentence, because "an amount is invalid" sends
	// the operator to check all four.
	amounts := []struct {
		name string
		d    decimal.Decimal
	}{
		{"tax", out.TaxRupees},
		{"interest", out.InterestRupees},
		{"fee", out.FeeRupees},
		{"penalty", out.PenaltyRupees},
	}
	for _, a := range amounts {
		if a.d.IsNegative() {
			return out, fmt.Errorf("%w: the %s on the challan is %s. These are MAGNITUDES — a "+
				"deposit is never negative, and a refund is not a challan",
				ErrInvalidTDSChallan, a.name, a.d.String())
		}
		if !isWholeRupees(a.d) {
			return out, fmt.Errorf("%w: the %s on the challan is %s, and all five challan amount "+
				"fields are WHOLE RUPEES: the prescribed format says \"no fractional portion is "+
				"allowed in this field (value should be integer), i.e. value '1000.50' will not be "+
				"allowed, whereas value '1000.00' will be considered to be valid value\". The "+
				"column is NUMERIC(19,4) so the TYPE accepts paise; the rule is on the VALUE",
				ErrInvalidTDSChallan, a.name, a.d.String())
		}
	}
	// tds_challan_something_was_deposited. A NIL challan is not stored here at
	// all: it is a RETURN ARTEFACT, derivable in full from the zero-tax
	// deductions, dated by the format to the last day of the quarter, and
	// corresponding to no event that ever happened.
	// internal/compliance/form140 synthesises it at emission time
	// (reporting/tds_return_source.go's groupByChallan).
	total := out.TaxRupees.Add(out.InterestRupees).Add(out.FeeRupees).Add(out.PenaltyRupees)
	if !total.IsPositive() {
		return out, fmt.Errorf("%w: nothing was deposited on this challan — tax, interest, fee and "+
			"penalty are all zero. A challan on which nothing was paid is a NIL challan, and a nil "+
			"challan is not a deposit: it is how the return carries deductee rows that have no tax, "+
			"it is derived from those deductions, and the emitter synthesises it. Storing one would "+
			"put a fiction in the same table as facts and the first report to SUM this table would "+
			"count it", ErrInvalidTDSChallan)
	}

	// ---- The allocations ---------------------------------------------------
	// A challan that deposited TAX must name at least one deduction AT ENTRY.
	//
	// ⚠️ THIS IS NOW A POLICY AND NO LONGER A NECESSITY, and the distinction is
	// written down because the sentence that used to justify it has stopped being
	// true. Before AllocateToRecordedChallan existed (register row D1) the create
	// was the ONLY writer and the CIN cannot be entered twice, so a challan stored
	// naming nothing was permanently unusable and this refusal was the only thing
	// between an operator and that trap. The allocate-later route now attaches to a
	// challan carrying any number of allocations, zero included, so the trap is
	// gone — and a refusal whose stated reason has evaporated is a refusal the next
	// reader deletes.
	//
	// IT IS KEPT FOR A DIFFERENT AND SMALLER REASON: the operator is holding the
	// deposit slip and the deductions it paid at THIS moment. Deductee Detail field
	// 5 of the return is a challan reference, so until something names it the
	// deposit discharges no liability and every deduction it really paid still
	// reports as never deposited (reporting's ErrTaxNotDeposited). One tick now
	// against a whole quarter refused on filing day.
	if out.TaxRupees.IsPositive() && len(out.Allocations) == 0 {
		return out, fmt.Errorf("%w: this challan deposited %s of TAX and names no deduction. "+
			"Deductee Detail field 5 of the return is a challan reference, so a deduction with no "+
			"allocation cannot be filed at all, and a deposit that names none discharges nothing — "+
			"every deduction it really paid would still report as never deposited. Name the "+
			"deductions in front of you now; the allocate-later route attaches the rest afterwards",
			ErrInvalidTDSChallan, out.TaxRupees.String())
	}
	// The converse. An interest-only or fee-only challan is legitimate — the
	// s.396 interest on a late deposit is paid on its own challan and is NOT tax
	// (migration 0069 lines 90-96) — but it discharges no deduction, so it can
	// allocate to none.
	if !out.TaxRupees.IsPositive() && len(out.Allocations) > 0 {
		return out, fmt.Errorf("%w: this challan deposited NO TAX (only interest, fee or penalty) "+
			"but allocates to %d deduction(s). Only the tax component discharges a deduction "+
			"liability, so there is nothing here to allocate; record the interest challan on its "+
			"own", ErrInvalidTDSChallan, len(out.Allocations))
	}

	if err := validateAllocationInputs(out.Allocations); err != nil {
		return out, err
	}
	// THE CAP (register row D2). Every allocation on a create is in this slice,
	// so the whole sum is knowable HERE, without a database — which is what makes
	// it testable on a machine where Postgres is down, the same argument the pure
	// half of this file's test suite is written on. The allocate-later path
	// cannot compute it purely (its sum includes rows already stored) and calls
	// the same function from inside its transaction.
	allocated := decimal.Zero
	for _, a := range out.Allocations {
		allocated = allocated.Add(a.AmountRupees)
	}
	if err := overAllocationRefusal(out.TaxRupees, allocated, len(out.Allocations)); err != nil {
		return out, err
	}
	return out, nil
}

// validateAllocationInputs is the per-allocation shape check, NORMALISING each
// entry in place (section trimmed, kind defaulted and upper-cased).
//
// ⛔ IT IS ONE FUNCTION BECAUSE THERE ARE NOW TWO WRITE PATHS. Register row D1
// added AllocateToRecordedChallan beside RecordTDSChallan, and a second copy of
// these rules would be a copy that drifts — with the drifted one winning for
// whichever caller reaches it first, which is the shape §7.2 of the parity audit
// records six times over for the back-dated cutoff's peeks.
func validateAllocationInputs(allocs []TDSChallanAllocationInput) error {
	seen := map[allocationTriple]bool{}
	var firstKind AllocationKind
	for i := range allocs {
		a := &allocs[i]
		a.Section = strings.TrimSpace(a.Section)
		kind, known := normaliseAllocationKind(a.Kind)
		if !known {
			return fmt.Errorf("%w: allocation for voucher %s names kind %q; an allocation pays "+
				"either a TDS deduction or a TCS collection, and nothing else",
				ErrInvalidTDSChallan, a.VoucherID, string(a.Kind))
		}
		a.Kind = kind
		if a.VoucherID == uuid.Nil {
			return fmt.Errorf("%w: an allocation must name the voucher whose deduction was "+
				"paid", ErrInvalidTDSChallan)
		}
		if a.Section == "" {
			return fmt.Errorf("%w: allocation for voucher %s names no section. The deduction "+
				"key is (voucher, section, leg) — one voucher can carry deductions under more than "+
				"one section", ErrInvalidTDSChallan, a.VoucherID)
		}
		// tds_challan_allocation_leg_seq_non_negative.
		if a.LegSeq < 0 {
			return fmt.Errorf("%w: allocation for voucher %s under %s has leg %d; the leg "+
				"ordinal is 0-based and never negative", ErrInvalidTDSChallan,
				a.VoucherID, a.Section, a.LegSeq)
		}
		// tds_challan_allocation_amount_non_negative. The equality test against
		// the live deduction happens in the transaction, where the deduction can
		// be read; this one is here so the pure layer catches the obvious case.
		if a.AmountRupees.IsNegative() {
			return fmt.Errorf("%w: allocation for voucher %s under %s is %s. An allocation is "+
				"a magnitude", ErrInvalidTDSChallan, a.VoucherID, a.Section, a.AmountRupees.String())
		}
		// ONE CHALLAN, ONE KIND — the file header says why a mixed challan
		// over-states the Form 140 batch total. This is the WITHIN-REQUEST half;
		// the against-what-is-already-stored half is in the transaction.
		if i == 0 {
			firstKind = a.Kind
		} else if a.Kind != firstKind {
			return fmt.Errorf("%w: this challan is asked to pay a %s and a %s. The deduction "+
				"return and the collection return are separate documents filed on separate "+
				"challans, and reporting files a challan's WHOLE tax as the deduction return's "+
				"challan total — so a mixed deposit over-states it by the collection half",
				ErrMixedChallanKinds, firstKind, a.Kind)
		}
		t := allocationTriple{voucherID: a.VoucherID, section: a.Section, legSeq: a.LegSeq}
		if seen[t] {
			return fmt.Errorf("%w: voucher %s under %s leg %d appears twice on this challan. "+
				"One challan pays a deduction ONCE — Deductee Detail field 5 is a single challan "+
				"reference, so a deduction split across two lines is not expressible in the return",
				ErrInvalidTDSChallan, a.VoucherID, a.Section, a.LegSeq)
		}
		seen[t] = true
	}
	return nil
}

// overAllocationRefusal is the ONE test for "does this deposit claim to have
// paid for more tax than it contains" (register row D2), and BOTH write paths
// call it: RecordTDSChallan purely, over the allocations it was keyed with, and
// AllocateToRecordedChallan inside its transaction, over the challan's whole
// stored total.
//
// THE TOLERANCE IS ONE RUPEE PER ALLOCATION AND IT IS DERIVED, NOT CHOSEN. The
// challan's tax box is WHOLE RUPEES (the format forbids paise in it) while a
// deduction is a 4-dp figure, so the whole-rupee deposit made against ONE
// deduction can be at most a rupee away from that deduction's tax; N allocations
// can therefore legitimately exceed the deposit by strictly less than ₹N. At or
// above that, no amount of rounding explains the difference.
//
// ⚠️ UNDER-ALLOCATION IS NOT REFUSED, and must not be. It is the ordinary state
// of a deposit whose deductions have not all been attached yet — the state
// AllocateToRecordedChallan exists to finish — and it is REPORTED as
// UnallocatedTaxRupees rather than refused.
func overAllocationRefusal(tax, allocated decimal.Decimal, count int) error {
	if count == 0 {
		return nil
	}
	tolerance := decimal.NewFromInt(int64(count))
	excess := allocated.Sub(tax)
	if excess.LessThan(tolerance) {
		return nil
	}
	return fmt.Errorf("%w: %s of tax was deposited on this challan and %s is allocated across %d "+
		"deduction(s) — %s more than was paid. The five challan boxes are WHOLE RUPEES and a "+
		"deduction is a 4-dp figure, so the allocations may exceed the deposit by rounding, but by "+
		"strictly less than ₹1 per allocation (here, ₹%d). A larger difference means the deposit "+
		"box or an allocation is wrong: recording it would tell the department that money reached "+
		"the Government when it did not, and would mark every deduction on it as already deposited "+
		"— so the real deposit could never be allocated to them afterwards",
		ErrChallanOverAllocated, tax.String(), allocated.String(), count, excess.String(), count)
}

// RecordTDSChallan records one deposit and the deductions it paid, atomically.
//
// ONE TRANSACTION for the challan and every allocation. Not a convenience: an
// allocation is verified against tds_deduction inside this transaction (there is
// no foreign key to verify it — see ErrNoSuchDeduction), and a check in one
// transaction with the insert in another is a check that a concurrent voucher
// ALTER can invalidate between the two.
//
// It is a CREATE and nothing else. Re-allocation, correction and deletion are
// NOT built in this slice; the file header says what that costs and why.
func (s *Service) RecordTDSChallan(ctx context.Context, in TDSChallanInput, recordedBy string) (uuid.UUID, error) {
	clean, err := validateTDSChallan(in)
	if err != nil {
		return uuid.Nil, err
	}
	// TRIMMED, as SetTDSProfile does. tds_challan.recorded_by has no non-blank
	// CHECK, so " " was accepted and stored as the audit identity of the one field
	// that says who asserted to the department that money reached the Government.
	recordedBy = strings.TrimSpace(recordedBy)
	if recordedBy == "" {
		return uuid.Nil, fmt.Errorf("%w: recorded_by is required", ErrInvalidTDSChallan)
	}
	// The company comes from the CONTEXT, never from a URL path: db.RunInTx sets
	// app.current_company_id from it as the transaction's first statement, and
	// the RLS policy on both tables (ENABLE + FORCE, USING and WITH CHECK) is
	// what confines the write. A company id reconstructed here from anything
	// else would be a second, unchecked, source of tenancy.
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return uuid.Nil, db.ErrNoCompany
	}
	// tds_challan.id and tds_challan_allocation.id have NO DEFAULT in migration
	// 0069, so they are generated here.
	challanID := uuid.New()

	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// 1. THE CIN. Read the existing challan first so the refusal can NAME
		//    it; the unique constraint is still the authority and is caught
		//    below for the concurrent case.
		//
		//    No WHERE company_id: RLS supplies the tenancy from the GUC, and a
		//    hand-written clause here would be a second copy of it that can
		//    disagree.
		var (
			existingTax decimal.Decimal
			existingBy  string
			existingAt  string
		)
		err := tx.QueryRow(ctx,
			`SELECT tax_rupees, recorded_by, to_char(recorded_at, 'YYYY-MM-DD')
			   FROM tds_challan
			  WHERE bsr_code = $1 AND deposit_date = $2 AND challan_no = $3`,
			clean.BSRCode, clean.DepositDate, clean.ChallanNo).
			Scan(&existingTax, &existingBy, &existingAt)
		switch {
		case err == nil:
			return fmt.Errorf("%w: challan %s of %s at BSR branch %s is already recorded, with %s "+
				"of tax on it, entered by %s on %s. The CIN (branch, date, serial) is how the "+
				"department identifies a payment, so this is the same deposit entered twice — which "+
				"would over-claim it in the return's batch total and be failed by the FVU against "+
				"the .csi file at the portal. If the first entry is wrong it must be corrected, not "+
				"duplicated",
				ErrDuplicateTDSChallanCIN, clean.ChallanNo, clean.DepositDate, clean.BSRCode,
				existingTax.String(), existingBy, existingAt)
		case errors.Is(err, pgx.ErrNoRows):
			// The normal path.
		default:
			return fmt.Errorf("masters: check challan CIN: %w", err)
		}

		// 2. THE CHALLAN.
		if _, err := tx.Exec(ctx,
			`INSERT INTO tds_challan
			   (id, company_id, bsr_code, deposit_date, challan_no, by_book_entry,
			    tax_rupees, interest_rupees, fee_rupees, penalty_rupees,
			    minor_head, tax_year_start, recorded_by)
			 VALUES ($1, $2, $3, $4, $5, $6,
			         $7, $8, $9, $10, $11, $12, $13)`,
			challanID, companyID, clean.BSRCode, clean.DepositDate, clean.ChallanNo,
			clean.ByBookEntry, clean.TaxRupees, clean.InterestRupees, clean.FeeRupees,
			clean.PenaltyRupees, clean.MinorHead, clean.TaxYearStart, recordedBy); err != nil {
			if isUniqueViolationOn(err, "tds_challan_cin_unique") {
				// The concurrent duplicate. The read above lost the race; the
				// constraint caught it, which is exactly the division of labour
				// the Go check is written for.
				return fmt.Errorf("%w: challan %s of %s at BSR branch %s was recorded by another "+
					"session while this one was being keyed",
					ErrDuplicateTDSChallanCIN, clean.ChallanNo, clean.DepositDate, clean.BSRCode)
			}
			return fmt.Errorf("masters: insert challan: %w", err)
		}

		// 3. THE ALLOCATIONS, each verified against the LIVE deduction.
		for _, a := range clean.Allocations {
			if err := allocateToDeduction(ctx, tx, allocationTarget{
				challanID:   challanID,
				companyID:   companyID,
				challanYear: clean.TaxYearStart,
				recordedBy:  recordedBy,
			}, a); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return uuid.Nil, err
	}
	return challanID, nil
}

// AllocateToRecordedChallan attaches further allocations to a challan that is
// ALREADY RECORDED, and returns the tax year it belongs to so the caller can
// read the register back without being told the year twice.
//
// ⛔ WHY THIS EXISTS AT ALL — register row D1, and it was DRIVEN, not imagined.
// A challan may legitimately be recorded naming fewer deductions than it paid
// for: this file has never required the allocations to cover the tax, only that
// a challan carrying tax names at least one deduction. Before this route the
// remainder could then never be attached to it, because the create is the only
// writer and the CIN cannot be entered twice. The audit tried seven in-browser
// doors and six HTTP methods against challan 05130 and its unallocated balance
// stayed at exactly 12000.0000, while reporting.Form140StatementFor refused the
// unattached deduction with ErrTaxNotDeposited — a deduction whose tax HAD been
// paid, permanently unfilable.
//
// THE SAME TRANSACTION RULE AS THE CREATE, and for the same reason: an
// allocation is verified against a live tds_deduction / tcs_collection row that
// no foreign key protects (migration 0069's header says why there is none), so a
// check in one transaction with the insert in another is a check a concurrent
// voucher ALTER can invalidate between the two. It is refused whole or applied
// whole.
//
// IT IS AN ADD AND NOTHING ELSE. It cannot move an allocation off another
// challan (that is ErrDeductionAlreadyPaid, naming the challan that has it),
// cannot change an amount, and cannot remove anything.
func (s *Service) AllocateToRecordedChallan(ctx context.Context, challanID uuid.UUID,
	allocs []TDSChallanAllocationInput, recordedBy string) (int, error) {
	if challanID == uuid.Nil {
		return 0, fmt.Errorf("%w: this allocation names no challan", ErrInvalidTDSChallan)
	}
	if len(allocs) == 0 {
		// A REFUSAL, NOT A NO-OP 200. An empty allocate-later request is an
		// operator who ticked nothing and pressed accept; answering "done" would
		// tell them the deposit now covers deductions it does not, and the
		// register would not have changed to contradict it.
		return 0, fmt.Errorf("%w: this request names no deduction to allocate. A deposit already "+
			"recorded is only changed by saying WHAT it paid for", ErrInvalidTDSChallan)
	}
	if err := validateAllocationInputs(allocs); err != nil {
		return 0, err
	}
	recordedBy = strings.TrimSpace(recordedBy)
	if recordedBy == "" {
		return 0, fmt.Errorf("%w: recorded_by is required", ErrInvalidTDSChallan)
	}
	companyID, ok := db.CompanyFromContext(ctx)
	if !ok {
		return 0, db.ErrNoCompany
	}

	var taxYear int
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// 1. THE CHALLAN, LOCKED.
		//
		// ⛔ `FOR UPDATE` IS LOAD-BEARING AND NOT A HABIT. The over-allocation cap
		// below is read-then-write on a SUM, so two concurrent requests attaching
		// different deductions to the SAME challan would each see the other's rows
		// as absent under READ COMMITTED and both pass a cap that their sum
		// breaks. The row lock serialises them per challan and costs nothing
		// anywhere else — the create path needs none of this, because the challan
		// it sums is one it created in the same transaction and nobody else can
		// see it yet.
		var (
			tax         decimal.Decimal
			bsr         string
			no          string
			depositDate string
		)
		err := tx.QueryRow(ctx,
			`SELECT tax_rupees, tax_year_start, bsr_code, challan_no,
			        to_char(deposit_date, 'YYYY-MM-DD')
			   FROM tds_challan
			  WHERE id = $1
			  FOR UPDATE`, challanID).
			Scan(&tax, &taxYear, &bsr, &no, &depositDate)
		if errors.Is(err, pgx.ErrNoRows) {
			// ErrNotFound, so the HTTP layer answers 404 rather than 400: the
			// request is well formed and names a challan this company does not
			// have. ⚠️ RLS makes another tenant's challan look exactly like a
			// missing one, which is the intended answer — the alternative leaks
			// whether a CIN exists elsewhere.
			return fmt.Errorf("%w: no challan %s is recorded for this company, so there is no "+
				"deposit to allocate against", ErrNotFound, challanID)
		}
		if err != nil {
			return fmt.Errorf("masters: read challan for allocation: %w", err)
		}
		// The converse rule the create path applies, applied here too: only the
		// TAX component discharges a liability, so an interest-only or fee-only
		// challan can allocate to nothing at all.
		if !tax.IsPositive() {
			return fmt.Errorf("%w: challan %s of %s at BSR branch %s deposited NO TAX (only "+
				"interest, fee or penalty), so there is nothing on it to allocate. Only the tax "+
				"component discharges a deduction liability",
				ErrInvalidTDSChallan, no, depositDate, bsr)
		}

		// 2. WHAT IS ALREADY ON IT — the total, and of WHICH KIND.
		//
		// The kind counts come from which table carries each stored allocation's
		// natural key, because migration 0069 has no kind column and must not grow
		// one without a migration. An allocation that matches NEITHER is an orphan
		// (its voucher leg was removed by an ALTER); it is counted as neither and
		// blocks a kind change, which is the fail-closed direction — see the
		// refusal below.
		var (
			storedCount int
			storedSum   decimal.Decimal
			tdsCount    int
			tcsCount    int
		)
		if err := tx.QueryRow(ctx,
			`SELECT count(*), COALESCE(sum(al.allocated_amount_rupees), 0),
			        count(*) FILTER (WHERE d.id IS NOT NULL),
			        count(*) FILTER (WHERE c.id IS NOT NULL)
			   FROM tds_challan_allocation al
			   LEFT JOIN tds_deduction d ON d.company_id = al.company_id
			                            AND d.voucher_id = al.voucher_id
			                            AND d.section    = al.section
			                            AND d.leg_seq    = al.leg_seq
			   LEFT JOIN tcs_collection c ON c.company_id = al.company_id
			                             AND c.voucher_id = al.voucher_id
			                             AND c.section    = al.section
			                             AND c.leg_seq    = al.leg_seq
			  WHERE al.challan_id = $1`, challanID).
			Scan(&storedCount, &storedSum, &tdsCount, &tcsCount); err != nil {
			return fmt.Errorf("masters: read challan allocations: %w", err)
		}

		// 3. ONE CHALLAN, ONE KIND — the against-what-is-stored half. The
		//    within-request half is in validateAllocationInputs, so a request that
		//    is internally mixed never reaches here.
		want := allocs[0].Kind
		mixed := false
		switch want {
		case AllocationKindTCS:
			// Fail CLOSED on an orphan: an allocation that resolves to neither
			// table is far more likely to be a deduction broken by an ALTER (every
			// allocation recorded before migration 0067 joined this mechanism is
			// one) than a collection, and guessing wrong here puts a collection on
			// a challan the deduction return will file in full.
			mixed = storedCount != tcsCount
		default:
			mixed = tcsCount > 0
		}
		if mixed {
			return fmt.Errorf("%w: challan %s of %s at BSR branch %s already carries %d allocation(s) "+
				"of the other kind (%d deduction(s), %d collection(s) and %d that no longer resolve), "+
				"and this request would add a %s. The deduction return and the collection return are "+
				"separate documents filed on separate challans, and reporting files a challan's WHOLE "+
				"tax as the deduction return's challan total — so a mixed deposit over-states it. "+
				"Record the collection's deposit on its own challan",
				ErrMixedChallanKinds, no, depositDate, bsr, storedCount, tdsCount, tcsCount,
				storedCount-tdsCount-tcsCount, want)
		}

		// 4. THE ALLOCATIONS, each verified against the LIVE deduction or
		//    collection — the SAME function the create path calls.
		//
		// ⛔ IT IS allocateToDeduction AND NOT A SECOND COPY OF ITS RULES, and that
		// is the one decision in this step. Six refusals live in that function —
		// the triple must match a live row, a zero-tax leg has nothing to deposit,
		// the amount must EQUAL the tax (0069's alter-detection mechanism), the
		// deduction's tax year must be the challan's, no other challan may already
		// have paid it, and the unique constraint catches the concurrent case. A
		// second copy here would be a copy that DRIFTS, with the drifted one
		// winning for whichever caller reaches it first — the exact shape §7.2 of
		// the parity audit records six times over for the back-dated cutoff's
		// peeks. Every one of those rules exists because breaking it puts a
		// statement in front of the department that is not true, and the operator
		// must not be able to choose which write path enforces them by choosing
		// which route to use.
		//
		// THE YEAR COMES FROM THE CHALLAN THAT IS ON RECORD (`taxYear`, scanned
		// under the row lock in step 1) and never from the request: this route is
		// told nothing about the year, and the caller is HANDED it back precisely
		// so it can read the register without guessing.
		//
		// ⚠️ AND THE RUNNING TOTALS ARE CARRIED FORWARD INTO THE CAP BELOW. Each
		// call either inserts exactly one allocation row or returns an error that
		// rolls the whole transaction back, so `storedSum`/`storedCount` — read in
		// step 2 as what the challan already carried — become what it carries
		// AFTER this request. Leaving them at their pre-insert values would leave
		// step 5 measuring the deposit against a total that predates the rows it
		// is supposed to be capping, which is the D2 hole reopened on the second
		// write path: a fully-allocated ₹1,000 challan would quietly accept
		// another ₹1,000.
		for _, a := range allocs {
			if err := allocateToDeduction(ctx, tx, allocationTarget{
				challanID:   challanID,
				companyID:   companyID,
				challanYear: taxYear,
				recordedBy:  recordedBy,
			}, a); err != nil {
				return err
			}
			storedCount++
			storedSum = storedSum.Add(a.AmountRupees)
		}

		// 5. THE CAP (register row D2), over the challan's WHOLE total and not
		//    just this request's. Attaching ₹12,000 to a challan that already
		//    claims its whole ₹12,000 deposit is exactly the shape this route
		//    would otherwise make reachable, and the count that sets the rounding
		//    tolerance is the count of allocations the challan then carries.
		//
		// ⛔ AFTER THE INSERTS, NOT BEFORE, AND IT IS NOT A LOOSE END. The refusal
		// is returned from inside RunInTx, so the rows written above are ROLLED
		// BACK with it — refused whole, exactly as the create path is. Checking
		// before would mean re-implementing "what would the total become", i.e. the
		// same arithmetic in a second place; checking after asks the transaction
		// what it now holds. The tests assert both halves: the sentinel, and that a
		// refused request left the allocation count and the unallocated balance
		// exactly where they were.
		return overAllocationRefusal(tax, storedSum, storedCount)
	})
	if err != nil {
		return 0, err
	}
	return taxYear, nil
}

// allocationTarget is the challan an allocation is being attached to — the parts
// of it the verification and the refusals need. A struct rather than six
// positional parameters because two of them are UUIDs and two are strings, and a
// transposed pair of those is the kind of mistake a compiler cannot see.
type allocationTarget struct {
	challanID uuid.UUID
	companyID uuid.UUID
	// challanYear is the challan's STORED tax_year_start.
	challanYear int
	recordedBy  string
}

// deductionSQL and collectionSQL read the ONE tax event an allocation names.
//
// THEY ARE THE SAME SHAPE ON PURPOSE — same natural key, same four columns, so
// the caller's refusals do not have to know which side of the statute it is on.
// The joins are for the SENTENCE: a refusal that says "voucher 4f9c…" instead of
// "voucher P/1042 paying Chitra Consulting" is a refusal an operator cannot act
// on.
//
// ⛔ NEITHER CARRIES `WHERE company_id`. RLS supplies the tenancy from the GUC,
// exactly as every other statement in this file; a hand-written clause here
// would be a second copy of it that can disagree.
const (
	// The natural key of migration 0050 (tds_deduction_one_per_leg).
	deductionSQL = `SELECT d.tds_amount_rupees, d.tax_year_start,
	        COALESCE(vv.voucher_no, ''), l.name
	   FROM tds_deduction d
	   JOIN voucher_head vh    ON vh.company_id = d.company_id AND vh.voucher_id = d.voucher_id
	   JOIN voucher_version vv ON vv.id = vh.current_version_id
	   JOIN ledger l           ON l.company_id  = d.company_id AND l.id = d.deductee_ledger_id
	  WHERE d.voucher_id = $1 AND d.section = $2 AND d.leg_seq = $3`
	// The natural key of migration 0067 (tcs_collection_one_per_leg). The party
	// is the BUYER — who the tax was collected FROM.
	collectionSQL = `SELECT c.tcs_amount_rupees, c.tax_year_start,
	        COALESCE(vv.voucher_no, ''), l.name
	   FROM tcs_collection c
	   JOIN voucher_head vh    ON vh.company_id = c.company_id AND vh.voucher_id = c.voucher_id
	   JOIN voucher_version vv ON vv.id = vh.current_version_id
	   JOIN ledger l           ON l.company_id  = c.company_id AND l.id = c.buyer_ledger_id
	  WHERE c.voucher_id = $1 AND c.section = $2 AND c.leg_seq = $3`
)

// allocateToDeduction verifies one triple against tds_deduction (or, for a TCS
// allocation, tcs_collection) and records the allocation. Everything it does
// happens inside the caller's transaction — RecordTDSChallan's or
// AllocateToRecordedChallan's.
func allocateToDeduction(ctx context.Context, tx pgx.Tx, target allocationTarget,
	a TDSChallanAllocationInput) error {
	var (
		deductionTax decimal.Decimal
		taxYear      int
		voucherNo    string
		deducteeName string
	)
	// WHICH TABLE, and the vocabulary that makes the choice safe: the two section
	// CHECKs share no value, so a kind that is wrong cannot silently match the
	// other side's row — it finds nothing and is refused by name below.
	readSQL := deductionSQL
	if a.Kind == AllocationKindTCS {
		readSQL = collectionSQL
	}
	err := tx.QueryRow(ctx, readSQL, a.VoucherID, a.Section, a.LegSeq).
		Scan(&deductionTax, &taxYear, &voucherNo, &deducteeName)
	if errors.Is(err, pgx.ErrNoRows) {
		if a.Kind == AllocationKindTCS {
			return fmt.Errorf("%w: nothing was collected on voucher %s under %s at leg %d, so there "+
				"is nothing on it to deposit. A collection is recorded by the SALE that made it "+
				"(migration 0067), and only the sections the engine ships exist — check that this "+
				"deposit is a collection at all and not a deduction",
				ErrNoSuchDeduction, a.VoucherID, a.Section, a.LegSeq)
		}
		return fmt.Errorf("%w: nothing was deducted on voucher %s under %s at leg %d, so there is "+
			"nothing on it to deposit. Migration 0069 carries no foreign key to tds_deduction (its "+
			"rows are deleted and re-derived on every voucher ALTER), so nothing in the database "+
			"would have stopped this — it would have been stored as a deposit against no deduction, "+
			"visible in no register and reconciling to no deductee",
			ErrNoSuchDeduction, a.VoucherID, a.Section, a.LegSeq)
	}
	if err != nil {
		return fmt.Errorf("masters: read deduction for allocation: %w", err)
	}

	// A deduction that deducted NOTHING has nothing deposited against it. It is
	// still REPORTED — the s.393(1) aggregate is of amounts PAID and Annexure 6
	// flag Y reports a below-threshold payment — but it belongs to the
	// SYNTHESISED nil challan, which is why reporting's groupByChallan routes
	// every zero-tax row there regardless of any allocation. An allocation here
	// would be a payment of nothing on a real deposit slip.
	if deductionTax.IsZero() {
		if a.Kind == AllocationKindTCS {
			// The collection side's zero is a DIFFERENT fact and must not be
			// described as a threshold: migration 0067 records a zero row for a
			// sale exempted by a s.394(2) declaration as well as for one below Sl.
			// No. 6's ten-lakh limit, and an exempt sale is the one that most needs
			// its record. Neither collected anything, so neither can be deposited.
			return fmt.Errorf("%w: voucher %s sold to %s under %s and collected NO tax — the sale "+
				"is exempt under a s.394(2) declaration, or below the section's limit — so no "+
				"deposit can be allocated to it. The sale is still recorded and still reported",
				ErrTDSAllocationMismatch, voucherNo, deducteeName, a.Section)
		}
		return fmt.Errorf("%w: voucher %s paid %s under %s and deducted NO tax (the payment is "+
			"below the threshold), so no deposit can be allocated to it. It is still reported — "+
			"under the return's nil challan, which is derived and never stored",
			ErrTDSAllocationMismatch, voucherNo, deducteeName, a.Section)
	}

	// THE EQUALITY. See the file header: this is where 0069's own header is
	// wrong, and refusing here beats refusing the whole quarter at filing time
	// with ErrAllocationBrokenByAlter, which would blame a voucher alter that
	// never happened.
	if !a.AmountRupees.Equal(deductionTax) {
		return fmt.Errorf("%w: voucher %s deducted %s under %s from %s, but %s is being allocated "+
			"to it. The two must be EQUAL: the amount stored on the allocation is what makes a "+
			"later voucher ALTER detectable, and reporting treats ANY inequality as an allocation "+
			"broken by an alter — so a deliberately partial allocation would refuse the entire "+
			"quarter at filing time with a message about a change nobody made",
			ErrTDSAllocationMismatch, voucherNo, deductionTax.String(), a.Section, deducteeName,
			a.AmountRupees.String())
	}

	// ONE CHALLAN BELONGS TO ONE TAX YEAR, which is the whole reason
	// tds_challan.tax_year_start is STORED rather than derived from the deposit
	// date. A deduction of another year was not paid by this deposit, and
	// allocating it would file a deductee row under a challan the department has
	// recorded against a different year — a mismatch discovered at the portal.
	//
	// ⚠️ THIS IS THE ONE REFUSAL WITH NO CONSTRAINT BEHIND IT AT ALL, not even a
	// CHECK: 0069 cannot express it, because the two tax years sit on different
	// tables and the link table carries neither. It is therefore the refusal most
	// worth reading twice.
	if taxYear != target.challanYear {
		return fmt.Errorf("%w: the deduction on voucher %s under %s belongs to tax year %d, and "+
			"this challan is recorded against %d. A challan belongs to ONE tax year — that is why "+
			"the year is stored on it rather than computed from the deposit date (a challan paid on "+
			"7 April is normally for March of the year before). Record the deposit against the year "+
			"whose tax it paid",
			ErrTDSAllocationMismatch, voucherNo, a.Section, taxYear, target.challanYear)
	}

	// Already paid? The unique constraint would catch it; this names the challan
	// that paid it, because the operator's next question is "then what is this
	// second slip in my hand".
	var otherBSR, otherNo, otherDate string
	err = tx.QueryRow(ctx,
		`SELECT c.bsr_code, c.challan_no, to_char(c.deposit_date, 'YYYY-MM-DD')
		   FROM tds_challan_allocation al
		   JOIN tds_challan c ON c.company_id = al.company_id AND c.id = al.challan_id
		  WHERE al.voucher_id = $1 AND al.section = $2 AND al.leg_seq = $3
		    AND al.challan_id <> $4`,
		a.VoucherID, a.Section, a.LegSeq, target.challanID).Scan(&otherBSR, &otherNo, &otherDate)
	switch {
	case err == nil:
		return fmt.Errorf("%w: the %s deducted on voucher %s under %s from %s was already "+
			"deposited on challan %s of %s at BSR branch %s. Deductee Detail field 5 is a SINGLE "+
			"challan reference, so a deduction cannot be paid by two challans — if this deposit is "+
			"the right one, the earlier allocation is the thing to correct",
			ErrDeductionAlreadyPaid, deductionTax.String(), voucherNo, a.Section, deducteeName,
			otherNo, otherDate, otherBSR)
	case errors.Is(err, pgx.ErrNoRows):
		// The normal path.
	default:
		return fmt.Errorf("masters: check existing allocation: %w", err)
	}

	// tds_challan_allocation.id has NO DEFAULT either. Generated in Go.
	if _, err := tx.Exec(ctx,
		`INSERT INTO tds_challan_allocation
		   (id, company_id, challan_id, voucher_id, section, leg_seq,
		    allocated_amount_rupees, recorded_by)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		uuid.New(), target.companyID, target.challanID, a.VoucherID, a.Section, a.LegSeq,
		a.AmountRupees, target.recordedBy); err != nil {
		if isUniqueViolationOn(err, "tds_challan_allocation_one_per_deduction") {
			return fmt.Errorf("%w: voucher %s under %s leg %d was allocated to a challan by "+
				"another session while this one was being keyed",
				ErrDeductionAlreadyPaid, voucherNo, a.Section, a.LegSeq)
		}
		return fmt.Errorf("masters: insert allocation: %w", err)
	}
	return nil
}

// isUniqueViolationOn reports whether err is a Postgres unique violation raised
// by the NAMED constraint.
//
// The name matters. Two different unique constraints on this path mean two
// different sentences — one is "you have entered the same deposit twice" and the
// other is "this deduction is already paid" — and a bare 23505 test would
// collapse them into whichever check happened to be written first.
func isUniqueViolationOn(err error, constraint string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" &&
		pgErr.ConstraintName == constraint
}

// ListTDSChallans returns every challan recorded for one tax year, earliest
// deposit first, with what each one paid for.
//
// The tax year is the STORED tds_challan.tax_year_start and not a date range
// over deposit_date, for the reason 0069 stores it at all: a challan paid on
// 7 April belongs to the year that ended a week earlier, and a date range would
// file it in the wrong one.
//
// ONE TRANSACTION for both reads, so the challans and their allocations are seen
// at the same instant. Two separate reads could straddle a voucher ALTER and
// produce a list whose totals do not add up.
func (s *Service) ListTDSChallans(ctx context.Context, taxYearStart int) ([]TDSChallanRecord, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return nil, db.ErrNoCompany
	}
	var (
		out   []TDSChallanRecord
		byID  = map[uuid.UUID]int{}
		descs = map[string]string{}
	)
	for _, m := range minorHeadDescriptions {
		descs[m.Code] = m.Description
	}

	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		out = nil
		byID = map[uuid.UUID]int{}

		rows, err := tx.Query(ctx,
			`SELECT id, bsr_code, to_char(deposit_date, 'YYYY-MM-DD'), challan_no,
			        by_book_entry, tax_rupees, interest_rupees, fee_rupees,
			        penalty_rupees, minor_head, tax_year_start, recorded_by,
			        to_char(recorded_at, 'YYYY-MM-DD"T"HH24:MI:SSOF:00')
			   FROM tds_challan
			  WHERE tax_year_start = $1
			  ORDER BY deposit_date, challan_no`, taxYearStart)
		if err != nil {
			return fmt.Errorf("masters: list challans: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var r TDSChallanRecord
			if err := rows.Scan(&r.ID, &r.BSRCode, &r.DepositDate, &r.ChallanNo,
				&r.ByBookEntry, &r.TaxRupees, &r.InterestRupees, &r.FeeRupees,
				&r.PenaltyRupees, &r.MinorHead, &r.TaxYearStart, &r.RecordedBy,
				&r.RecordedAt); err != nil {
				return fmt.Errorf("masters: scan challan: %w", err)
			}
			r.TotalDepositedRupees = r.TaxRupees.Add(r.InterestRupees).
				Add(r.FeeRupees).Add(r.PenaltyRupees)
			r.MinorHeadDescription = descs[r.MinorHead]
			r.AllocatedTotalRupees = decimal.Zero
			r.UnallocatedTaxRupees = r.TaxRupees
			byID[r.ID] = len(out)
			out = append(out, r)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(out) == 0 {
			return nil
		}

		// The allocations, with the LIVE deduction beside each one.
		//
		// LEFT JOIN tds_deduction, and that is the whole point of the query: an
		// ALTER can remove a deduction leg entirely, and the allocation SURVIVES
		// it because 0069 has no foreign key. An inner join would make the
		// deposit that funded it vanish from this list — silently losing the
		// record of a payment really made to the Government, which is precisely
		// the failure 0069's key design exists to prevent.
		//
		// ⛔ AND IT LEFT JOINS tcs_collection TOO (register row D4). The link table
		// carries no kind column and needs none — the two section vocabularies are
		// disjoint by CHECK — but a reader that only knew about tds_deduction
		// reported every TCS allocation as DeductionOnRecord=false and therefore
		// BROKEN BY ALTER, i.e. it told the operator their collection deposit had
		// been invalidated by a voucher change that never happened.
		arows, err := tx.Query(ctx,
			`SELECT al.challan_id, al.voucher_id, COALESCE(vv.voucher_no, ''),
			        al.section, al.leg_seq, al.allocated_amount_rupees,
			        COALESCE(l.name, bl.name, ''),
			        COALESCE(d.tds_amount_rupees, tc.tcs_amount_rupees),
			        (tc.id IS NOT NULL)
			   FROM tds_challan_allocation al
			   JOIN tds_challan ch       ON ch.company_id = al.company_id AND ch.id = al.challan_id
			   LEFT JOIN voucher_head vh ON vh.company_id = al.company_id
			                            AND vh.voucher_id = al.voucher_id
			   LEFT JOIN voucher_version vv ON vv.id = vh.current_version_id
			   LEFT JOIN tds_deduction d ON d.company_id = al.company_id
			                            AND d.voucher_id = al.voucher_id
			                            AND d.section    = al.section
			                            AND d.leg_seq    = al.leg_seq
			   LEFT JOIN tcs_collection tc ON tc.company_id = al.company_id
			                             AND tc.voucher_id = al.voucher_id
			                             AND tc.section    = al.section
			                             AND tc.leg_seq    = al.leg_seq
			   LEFT JOIN ledger l        ON l.company_id  = d.company_id
			                            AND l.id         = d.deductee_ledger_id
			   LEFT JOIN ledger bl       ON bl.company_id = tc.company_id
			                            AND bl.id        = tc.buyer_ledger_id
			  WHERE ch.tax_year_start = $1
			  ORDER BY ch.deposit_date, ch.challan_no, vv.voucher_no, al.section, al.leg_seq`,
			taxYearStart)
		if err != nil {
			return fmt.Errorf("masters: list challan allocations: %w", err)
		}
		defer arows.Close()
		for arows.Next() {
			var (
				challanID uuid.UUID
				a         TDSChallanAllocationRecord
				live      *decimal.Decimal
				isTCS     bool
			)
			if err := arows.Scan(&challanID, &a.VoucherID, &a.VoucherNo, &a.Section,
				&a.LegSeq, &a.AllocatedRupees, &a.DeducteeName, &live, &isTCS); err != nil {
				return fmt.Errorf("masters: scan challan allocation: %w", err)
			}
			// The kind is recovered from WHICH table matched. An orphan matches
			// neither and reads as TDS — see the field's doc.
			a.Kind = AllocationKindTDS
			if isTCS {
				a.Kind = AllocationKindTCS
			}
			if live != nil {
				a.DeductionOnRecord = true
				a.DeductionTaxRupees = *live
			}
			// The 0045 mechanism, advisory here and authoritative in reporting:
			// the deduction has gone, or it no longer shows what was deposited
			// against it.
			a.BrokenByAlter = !a.DeductionOnRecord || !a.DeductionTaxRupees.Equal(a.AllocatedRupees)

			i, ok := byID[challanID]
			if !ok {
				// Cannot happen: the query is joined to the same tax year. If it
				// ever does, dropping the row silently would understate a
				// deposit, so it is an error rather than a skip.
				return fmt.Errorf("masters: allocation names challan %s, which is not in the "+
					"same tax year's challan list", challanID)
			}
			out[i].Allocations = append(out[i].Allocations, a)
			out[i].AllocationCount++
			out[i].AllocatedTotalRupees = out[i].AllocatedTotalRupees.Add(a.AllocatedRupees)
			out[i].UnallocatedTaxRupees = out[i].TaxRupees.Sub(out[i].AllocatedTotalRupees)
		}
		return arows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// WHAT CAN BE ALLOCATED — the read that makes the entry form usable at all
// ---------------------------------------------------------------------------

// AllocatableDeduction is one deduction a new challan could pay for.
//
// ⛔ THIS READ EXISTS BECAUSE THE FORM WAS UNUSABLE WITHOUT IT, and the way it
// was unusable is worth recording. The allocation is keyed on
// (voucher_id, section, leg_seq) — a UUID and a 0-BASED ORDINAL — because that is
// tds_deduction's natural key and the only key that survives an ALTER. But NO
// SCREEN IN THIS PRODUCT DISPLAYS EITHER OF THEM: the TDS/TCS Register renders
// voucher_no, and its payload carries no leg field at all. So the first version
// of the entry form asked the operator to type two identifiers they had no way to
// obtain, and typing the one thing they COULD see ("P1") earned a bare
// "allocation 1: invalid voucher_id".
//
// It passed every test, because the tests keyed mock fixture ids that the real
// server rejects as non-UUIDs. A challan cannot be filed without an allocation
// (Deductee Detail field 5 is a challan reference), so the allocation grid IS the
// feature, and on the real stack a human could not complete it.
//
// The fix is not a better error message: it is to stop asking. The screen offers
// what is allocatable and sends the identifiers it was given.
type AllocatableDeduction struct {
	// VoucherID and LegSeq are the identifiers a human must never type. They
	// travel to the client only so the client can send them back.
	VoucherID uuid.UUID
	LegSeq    int
	// Kind is TDS (a deduction) or TCS (a collection), and it travels back on the
	// allocation for the same reason the ids do: it is how the write path knows
	// which table to verify the triple against. ⛔ WITHOUT IT A COLLECTOR COULD
	// NOT RECORD A DEPOSIT AT ALL (register row D4) — the picker offered
	// deductions only, so a live 2000.0000 collection under 206C(1)-SCRAP
	// appeared nowhere and could be attached to no challan.
	Kind AllocationKind
	// VoucherNo and DeducteeName are what a human actually recognises. On a TCS
	// row DeducteeName is the BUYER — who the tax was collected from.
	VoucherNo    string
	DeducteeName string
	Section      string
	// DeductionDate is the deduction date, or — on a TCS row — the collection
	// date. One field, because it answers one question ("when did the liability
	// arise") and a second name for it would be a second column every reader has
	// to remember to check.
	DeductionDate string
	// TaxRupees is what was deducted, and therefore EXACTLY what may be
	// allocated: RecordTDSChallan refuses an amount that is not equal to it. So
	// the amount is not an input either — the screen sends this figure back.
	TaxRupees decimal.Decimal
	// AlreadyPaid means a challan already covers this leg. Such a deduction is
	// returned rather than filtered out, and flagged: an operator looking for a
	// deduction that is missing from the list needs to know the difference
	// between "not deducted in this year" and "already deposited".
	AlreadyPaid bool
}

// ListAllocatableDeductions returns every deduction of a tax year that carries
// tax, with whether a challan already pays it.
//
// ZERO-TAX ROWS ARE EXCLUDED, and that is not a convenience. A sub-threshold
// payment has no tax to deposit and cannot be allocated at all — the return
// reports it under the SYNTHESISED nil challan, which the emitter builds and
// nothing stores. Offering one in a picker would invite an operator to allocate
// against it and collect a refusal for a thing that is not an error.
func (s *Service) ListAllocatableDeductions(ctx context.Context, taxYearStart int) ([]AllocatableDeduction, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return nil, db.ErrNoCompany
	}
	var out []AllocatableDeduction
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		out = nil
		// voucher_no comes off the CURRENT version, the same way the register
		// reads it: an altered voucher must show the number it now carries.
		// The LEFT JOIN to the allocation is what answers AlreadyPaid; there is no
		// foreign key between the two tables by design (0069), so this join is on
		// the natural key exactly as the write path matches it.
		//
		// ⛔ THE UNION IS THE COLLECTION SIDE (register row D4). tcs_collection is
		// the same shape keyed the same way, and until it was read here a collector
		// could record what it had collected and never record that it had deposited
		// it: the picker offered deductions only, so the deposit had nothing to
		// name. Its zero rows are excluded by the SAME rule as the deduction side's
		// and for a stronger reason — a zero collection is an EXEMPT sale or one
		// below the section's limit, and nothing was collected to deposit.
		rows, err := tx.Query(ctx,
			`SELECT voucher_id, leg_seq, voucher_no, party_name, section,
			        liability_date, tax_rupees, already_paid, kind
			   FROM (
			     SELECT d.voucher_id AS voucher_id, d.leg_seq AS leg_seq,
			            COALESCE(vv.voucher_no, '') AS voucher_no, l.name AS party_name,
			            d.section AS section,
			            to_char(d.deduction_date, 'YYYY-MM-DD') AS liability_date,
			            d.tds_amount_rupees AS tax_rupees,
			            (a.id IS NOT NULL) AS already_paid, 'TDS' AS kind
			       FROM tds_deduction d
			       JOIN ledger l           ON l.company_id  = d.company_id AND l.id = d.deductee_ledger_id
			       JOIN voucher_head vh    ON vh.company_id = d.company_id AND vh.voucher_id = d.voucher_id
			       JOIN voucher_version vv ON vv.id = vh.current_version_id
			       LEFT JOIN tds_challan_allocation a
			              ON a.company_id = d.company_id
			             AND a.voucher_id = d.voucher_id
			             AND a.section    = d.section
			             AND a.leg_seq    = d.leg_seq
			      WHERE d.tax_year_start = $1
			        AND d.tds_amount_rupees > 0
			     UNION ALL
			     SELECT c.voucher_id, c.leg_seq,
			            COALESCE(vv.voucher_no, ''), l.name, c.section,
			            to_char(c.collection_date, 'YYYY-MM-DD'),
			            c.tcs_amount_rupees, (a.id IS NOT NULL), 'TCS'
			       FROM tcs_collection c
			       JOIN ledger l           ON l.company_id  = c.company_id AND l.id = c.buyer_ledger_id
			       JOIN voucher_head vh    ON vh.company_id = c.company_id AND vh.voucher_id = c.voucher_id
			       JOIN voucher_version vv ON vv.id = vh.current_version_id
			       LEFT JOIN tds_challan_allocation a
			              ON a.company_id = c.company_id
			             AND a.voucher_id = c.voucher_id
			             AND a.section    = c.section
			             AND a.leg_seq    = c.leg_seq
			      WHERE c.tax_year_start = $1
			        AND c.tcs_amount_rupees > 0
			   ) x
			  ORDER BY liability_date, voucher_no, leg_seq, kind`, taxYearStart)
		if err != nil {
			return fmt.Errorf("masters: read allocatable deductions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var a AllocatableDeduction
			if err := rows.Scan(&a.VoucherID, &a.LegSeq, &a.VoucherNo, &a.DeducteeName,
				&a.Section, &a.DeductionDate, &a.TaxRupees, &a.AlreadyPaid, &a.Kind); err != nil {
				return fmt.Errorf("masters: scan allocatable deduction: %w", err)
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
