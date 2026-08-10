package ledger

// cost_allocation.go — WHICH DEPARTMENT BORE THIS COST, as an input to the
// voucher path.
//
// A posting says "Salaries & Wages, Dr 1,00,000". It has never been able to say
// which department, branch or project bore it, and that is the number a
// manager's budget, a segment disclosure and every "why is Delhi losing money"
// conversation are computed from. Migration 0040 adds it. This file is the seam
// between a request and BOTH renderings of it: CanonicalPayload.CostAllocations
// (hashed, signed, part of the voucher's audit identity) and
// posting_cost_allocation (queryable, indexed, what the reports read).
//
// It is modelled on inward_doc.go, which is the worked example for "a value that
// rides in the hashed payload AND in columns, from one source in one
// transaction". The reasoning there applies verbatim: the two renderings are
// built from ONE in-memory slice inside ONE transaction by ledger.post(), so no
// code path exists that can write one without the other, and the signature can
// never disagree with the rows.
//
// ===========================================================================
// ⛔⛔ A CATEGORY IS AN AXIS. A CENTRE IS A POSITION ON THAT AXIS.
// ===========================================================================
// This is the one thing every naive implementation of this feature gets wrong,
// so it is stated here as well as in migration 0040's header and on
// CanonicalCostAllocation.
//
// Multiple categories exist in order to allocate the SAME money IN PARALLEL. One
// Rs 1,00,000 salary posting is allocated in full across Departments (Sales
// 60,000 / Admin 40,000) AND AGAIN IN FULL across Branches (Mumbai 70,000 /
// Delhi 30,000). Both allocations are complete, both are correct, and they
// describe the same money from two directions.
//
// Therefore validateCostAllocations groups by (posting, CATEGORY) and checks
// each group INDEPENDENTLY against the posting amount. A posting-wide total
// would come to 2,00,000 against a 1,00,000 posting and the validator would
// refuse perfectly correct data — the "helpful" check that makes the feature
// unusable the first time a business adds a second axis. Getting this wrong in
// the OTHER direction (checking nothing) leaves 60,000 of Department allocation
// standing against 1,00,000 of cost with 40,000 silently unattributed. Both
// directions are the whole risk of this file.
//
// ===========================================================================
// WHY THIS VALIDATION EXISTS AT ALL, GIVEN THE DATABASE ALREADY REFUSES IT
// ===========================================================================
// cost_allocation_sums_to_posting is a CONSTRAINT TRIGGER, DEFERRABLE INITIALLY
// DEFERRED, and it is the AUTHORITY. It has to be deferred: a split into three
// centres is three INSERTs and the first two are legitimately short of the
// total, so a per-statement check would make the feature unusable. Deferred
// means it fires at COMMIT — after the version row is written and the postings
// inserted — so what an operator gets back is a rolled-back transaction carrying
// a plpgsql exception.
//
// ⚠️ AND NOT, AS THIS COMMENT ONCE CLAIMED, A GAP IN THE VOUCHER SERIES. There is
// no sequence anywhere in this schema (grep the migrations for `nextval`: no
// hits). voucher_number_counter is an ordinary table row advanced by an
// `INSERT ... ON CONFLICT DO UPDATE` on the SAME transaction (numbering.go:126),
// so a rollback restores it and the next voucher takes the number this one gave
// back. The claim was written because a gap is what a Postgres SEQUENCE would
// leave, and it survived unchallenged because it sounds right — exactly the prose
// rot HANDOFF §7.1 records, where four claims in one feature outlived their truth.
//
// The real reason to check early is smaller and still sufficient: a constraint
// name is not a sentence. So these checks run BEFORE the write, for the same
// reason InwardDocument.Validate does — the DB decides, this layer explains. See
// where they are called from in ledger.post().

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// CostAllocationInput is ONE share of ONE posting borne by ONE cost centre on
// ONE axis.
//
// The four fields are exactly the four the hashed payload carries, in the same
// order, because CanonicalCostAllocation is built from this struct verbatim and
// nothing may be present in one and absent from the other.
type CostAllocationInput struct {
	// PostingSeq is the 0-BASED INDEX into this request's Postings slice — a
	// SEQUENCE, not a posting id, and that asymmetry with the
	// posting_cost_allocation table (which keys on posting_id) is deliberate.
	//
	// The payload cannot name a posting id: `posting` rows are re-created
	// wholesale by every ALTER, so an id in the signed bytes would be a value
	// that CHANGES on correction and version 2 would disagree with version 1
	// about which row an unchanged allocation belonged to. The table cannot key
	// on a sequence: its trigger compares against posting.amount and `posting`
	// has no ordinal column to join a sequence to. Both keys are derived from
	// THIS ONE slice in ONE transaction by post(), which inserts postings in
	// payload order and therefore holds both. Neither is ever reconstructed from
	// the other.
	//
	// It is NOT a ledger id, for a reason worth stating: one voucher may legally
	// post twice to the same ledger, and then a ledger key is ambiguous.
	PostingSeq int
	// CostCategoryID is the AXIS. NOT OPTIONAL and never uuid.Nil — see the file
	// header. An allocation without its axis would be summed together with every
	// other allocation of the same posting, and two categories legitimately
	// allocating the same money in parallel would add up to twice the cost.
	CostCategoryID uuid.UUID
	// CostCentreID is the position on that axis that bore the cost. It is
	// constrained by a COMPOSITE foreign key to actually sit on
	// CostCategoryID's axis (posting_cost_allocation_centre_matches_category_fk),
	// which is what stops "Mumbai the Branch" being claimed as a Department.
	CostCentreID uuid.UUID
	// Amount is a MAGNITUDE, never a sign: the posting's own dr_cr carries the
	// direction, exactly as stock_movement.qty leaves the sign to `direction`.
	// decimal.Decimal, never float64 — the per-category check below is EXACT
	// equality to the paisa, and a float would make "60,000 + 40,000 = 1,00,000"
	// a coin flip.
	Amount decimal.Decimal
}

// Canonical returns the hashed view of one allocation.
//
// Every field becomes a STRING, including the sequence number, for the reason
// CanonicalStockLine records: VerifyChain re-derives the hashed bytes by
// decoding the stored JSONB back into the struct and re-marshalling it, and that
// re-derivation is exact only while no value has a second legal spelling. The
// amount is StringFixed(4) so its SCALE survives — 1180 and 1180.0000 are the
// same number and different bytes, and a jsonb column does not preserve the text
// it was given.
func (a CostAllocationInput) Canonical() CanonicalCostAllocation {
	return CanonicalCostAllocation{
		PostingSeq:     strconv.Itoa(a.PostingSeq),
		CostCategoryID: a.CostCategoryID.String(),
		CostCentreID:   a.CostCentreID.String(),
		Amount:         a.Amount.StringFixed(4),
	}
}

// canonicalCostAllocations builds the hashed slice, or NIL.
//
// ⛔ THE NIL RETURN IS LOAD-BEARING, NOT TIDINESS. `cost_allocations,omitempty`
// omits a nil slice and an empty one alike, but only because encoding/json
// treats len == 0 as empty — a caller that unconditionally built
// make([]CanonicalCostAllocation, 0) would be relying on that, and a single
// stray `[]` in the payload changes the hash of every voucher it touches while
// looking completely correct at the call site. Every voucher posted before this
// session carries no allocations and must marshal to BYTE-IDENTICAL JSON to what
// it always did, or VerifyChain retroactively reports every book in the field as
// tampered with — which is indistinguishable from an attack.
// TestAVoucherWithNeitherAllocationsNorFulfilmentsHashesExactlyAsBefore is the
// test that can see it.
func canonicalCostAllocations(as []CostAllocationInput) []CanonicalCostAllocation {
	if len(as) == 0 {
		return nil
	}
	out := make([]CanonicalCostAllocation, 0, len(as))
	for _, a := range as {
		out = append(out, a.Canonical())
	}
	return out
}

// costAllocationAxisKey identifies ONE allocation SET: one posting, one axis.
// The per-category invariant is checked once per key.
type costAllocationAxisKey struct {
	seq      int
	category uuid.UUID
}

// ===========================================================================
// ⛔⛔ NOTHING BELOW THIS LINE MAY PUT A UUID OR A `posting_seq` IN A SENTENCE
// AN OPERATOR READS. TestNoRefusalPrintsAUUIDOrAPostingOrdinal enforces it.
// ===========================================================================
// These messages are the 422 body. They are the only thing the person who keyed
// the voucher ever sees about this feature, and they used to read:
//
//	cost allocation on posting 1 totals 60000.0000 on cost category
//	018f0000-0000-7000-8000-0000000000d1 but the posting is 100000.0000
//
// Two separate defects in one sentence, and the comment above it claimed it
// "names the axis":
//
//  1. THE UUID. The operator picked "Department" from a list. They have never
//     seen that id, cannot look it up from any screen, and the picker offers no
//     way to search by one. It names the axis to a database, not to a person.
//  2. `posting 1`. That is `posting_seq`, THE 0-BASED ORDINAL THIS PRODUCT
//     PROMISES NEVER TO DISPLAY — CostAllocationPicker.tsx states the rule in
//     its own header, and it is 0-based over `[primary, ...particulars]`, so
//     "posting 1" is the row the operator sees FIRST. The one number guaranteed
//     to send them to the wrong line is the number the refusal quoted.
//
// A line is therefore named the way the screen names it: by its SIDE and its
// AMOUNT, both of which are on the form in front of them. An axis cannot be
// named at all from here — this package holds ids and the names live in
// cost_category, which validateCostAllocations has no transaction to read (it
// runs BEFORE the transaction opens, deliberately, so a refusal costs nothing).
// So the axis is identified by the thing the operator TYPED: its shares. See
// describeAxisShares. Resolving the real name needs a lookup passed in from
// ledger.post(); that edit is reported, not made, because this lane does not own
// ledger.go — and the sentence below is complete and actionable without it.

// ⚠️ `sideWord` IS NOT DECLARED HERE. ledger.go already has one, for the party
// orientation refusal, and a second copy would be a second spelling of "Dr" that
// could drift from the one the rest of the package's messages use.

// describePosting names one accounting line by what the operator can SEE of it:
// its side and its amount.
//
// ⚠️ IT DISAMBIGUATES WHEN IT HAS TO. A voucher may legally post twice to the
// same side for the same amount (two 5,000 Dr expense lines), and then side +
// amount alone points at both. In that case — and ONLY in that case — the
// description adds a 1-BASED count among the identical-looking lines, which is
// something a human can actually do with their eyes. That is not the forbidden
// ordinal: it counts what is on the screen, not the position in a wire array.
func describePosting(postings []PostingInput, seq int) string {
	if seq < 0 || seq >= len(postings) {
		return "a line this voucher does not post"
	}
	p := postings[seq]
	base := fmt.Sprintf("the %s %s line", sideWord(p.DrCr), p.Amount.StringFixed(4))
	nth, total := 0, 0
	for i, q := range postings {
		if q.DrCr != p.DrCr || !q.Amount.Equal(p.Amount) {
			continue
		}
		total++
		if i == seq {
			nth = total
		}
	}
	if total <= 1 {
		return base
	}
	return fmt.Sprintf("%s (the %s of %d lines that read the same)",
		base, ordinal(nth), total)
}

// ordinal renders a small counting number as words a person reads off a form.
func ordinal(n int) string {
	switch n {
	case 1:
		return "first"
	case 2:
		return "second"
	case 3:
		return "third"
	}
	return fmt.Sprintf("%dth", n)
}

// describeAxisShares lists, in the order they were keyed, the shares belonging to
// ONE (posting, axis) group.
//
// ⛔ THIS IS HOW THE REFUSAL SAYS **WHICH** AXIS WITHOUT A NAME OR AN ID, and it
// is the part of the message with real work to do: a line analysed on Departments
// AND Branches in parallel produces two groups, only one of which is wrong, and
// telling the operator to fix "the cost category" would put them at even odds of
// correcting the split that was right. The shares are numbers they typed
// themselves, on rows they are looking at, so "60,000.0000 + 25,000.0000"
// identifies the Department axis to a person better than its primary key does.
func describeAxisShares(as []CostAllocationInput, key costAllocationAxisKey) string {
	parts := make([]string, 0, 4)
	for _, a := range as {
		if a.PostingSeq == key.seq && a.CostCategoryID == key.category {
			parts = append(parts, a.Amount.StringFixed(4))
		}
	}
	if len(parts) == 0 {
		return "no shares"
	}
	return strings.Join(parts, " + ")
}

// validateCostAllocations refuses a split the database would refuse anyway, but
// with a sentence naming the consequence in rupees instead of a constraint name
// after a rollback.
//
// `postings` is the FINAL posting list of the version being written — for an
// item invoice that is the SERVER-GENERATED list, not whatever the client sent,
// which is why post() calls this again inside the transaction after the
// generator has run.
//
// A posting with NO allocations is LEGAL and stays legal. Allocation is optional
// per ledger; most postings in most books will never be allocated, and the
// deferred trigger says nothing about a posting with no allocation rows for
// exactly that reason. So this function checks only the SETS THAT EXIST: it never
// requires an allocation, only that one which exists is complete on its own axis.
func validateCostAllocations(postings []PostingInput, as []CostAllocationInput) error {
	if len(as) == 0 {
		return nil
	}

	// Pass 1 — each allocation on its own: does it point at a real line of THIS
	// request, does it name both an axis and a position, and is it a positive
	// magnitude.
	//
	// This runs first because an out-of-range sequence makes every later check
	// meaningless (there is no posting amount to total against), and because a
	// client bug that offsets the index by one is the single most likely defect
	// on this path. It must NOT reach Postgres, where it would arrive as a
	// foreign-key violation naming posting_cost_allocation_posting_fk — an error
	// about a table the operator has never heard of, for a mistake that is
	// entirely about the form they just filled in.
	for _, a := range as {
		if a.PostingSeq < 0 || a.PostingSeq >= len(postings) {
			// ⚠️ THE ONE PLACE A COUNT SURVIVES, and it is deliberately framed as a
			// fact about the REQUEST rather than about the form. The index is out of
			// range, so there is no line to describe by side and amount, and the only
			// useful thing left to say is how many lines the voucher actually has —
			// which is a defect in the SENDING PROGRAM (the index is computed from a
			// row's position; no human types one), so the sentence says that too
			// rather than sending an operator to hunt for "line 3".
			if len(postings) == 0 {
				return fmt.Errorf(
					"ledger: a cost allocation was sent for a voucher that posts no accounting lines at all, so there is no cost to attribute to a department")
			}
			return fmt.Errorf(
				"ledger: a cost allocation was sent against an accounting line this voucher does not have — it posts %d line(s). The line is identified by its position in the request, which is computed by the sending program and shown on no screen, so this is a defect in that program rather than something that was keyed",
				len(postings))
		}
		if a.CostCategoryID == uuid.Nil {
			return fmt.Errorf(
				"ledger: a cost allocation on %s names no cost category; without the axis it belongs to it would be added to every other allocation of the same line, and two axes that each allocate the full amount would come to twice the cost",
				describePosting(postings, a.PostingSeq))
		}
		if a.CostCentreID == uuid.Nil {
			return fmt.Errorf(
				"ledger: a cost allocation of %s on %s names a cost category but no cost centre, so nothing is recorded about which department bore the cost",
				a.Amount.StringFixed(4), describePosting(postings, a.PostingSeq))
		}
		if !a.Amount.IsPositive() {
			return fmt.Errorf(
				"ledger: the cost allocation of %s on %s must be more than zero — an allocation is a MAGNITUDE and the line's own Dr/Cr carries the direction, so a zero or negative share either records that a department bore nothing or reverses the cost it did bear",
				a.Amount.StringFixed(4), describePosting(postings, a.PostingSeq))
		}
	}

	// Pass 2 — the same centre twice on the same posting and axis.
	//
	// posting_cost_allocation_one_per_centre refuses it, but the reason it is
	// refused is arithmetic rather than bookkeeping neatness: the same share
	// keyed twice can only make a set total correctly by making the FIRST entry
	// wrong, so the operator would "fix" a sum error by corrupting a figure that
	// was right.
	seen := make(map[costAllocationAxisKey]map[uuid.UUID]bool, len(as))
	for _, a := range as {
		key := costAllocationAxisKey{seq: a.PostingSeq, category: a.CostCategoryID}
		centres := seen[key]
		if centres == nil {
			centres = make(map[uuid.UUID]bool, 2)
			seen[key] = centres
		}
		if centres[a.CostCentreID] {
			// The centre is not named because it cannot be from here (see the header
			// above the describe* helpers) — but the SHARES on that axis are, and the
			// duplicate is one of them, so the operator can see which two rows of the
			// picker point at the same centre.
			return fmt.Errorf(
				"ledger: one cost centre is allocated TWICE on %s within a single cost category (that axis carries %s); the same share keyed twice can only make the axis total correctly by making the other entry wrong",
				describePosting(postings, a.PostingSeq),
				describeAxisShares(as, key))
		}
		centres[a.CostCentreID] = true
	}

	// ⛔⛔ PASS 3 — THE PER-CATEGORY SUM. This is the invariant the whole file
	// exists for, and it is checked PER (POSTING, CATEGORY) and never per
	// posting. Read the file header before changing the grouping: a posting-wide
	// total refuses correct parallel allocation, and no total at all lets cost go
	// silently unattributed.
	//
	// EXACT decimal equality, to the paisa, with no tolerance and no rounding
	// window — matching the trigger. A tolerance is how a real misallocation
	// hides for a year: every voucher a few paise short, and a Cost Centre
	// Summary that disagrees with the Profit & Loss by an amount that grows with
	// volume and matches nothing.
	totals := make(map[costAllocationAxisKey]decimal.Decimal, len(as))
	// order preserves first-appearance order so the message names the axis the
	// operator would look at first, and so the same bad request always produces
	// the same sentence (map iteration order would make the error text flap
	// between runs on a request with two bad axes).
	order := make([]costAllocationAxisKey, 0, len(as))
	for _, a := range as {
		key := costAllocationAxisKey{seq: a.PostingSeq, category: a.CostCategoryID}
		if _, ok := totals[key]; !ok {
			order = append(order, key)
			totals[key] = decimal.Zero
		}
		totals[key] = totals[key].Add(a.Amount)
	}
	for _, key := range order {
		allocated := totals[key]
		want := postings[key.seq].Amount
		if allocated.Equal(want) {
			continue
		}
		// ⛔ THE MESSAGE THE OPERATOR ACTS ON. Their actual question is "WHICH axis
		// is short, and by how much" — and this sentence used to answer the first
		// half with a UUID and locate it with a `posting_seq`, i.e. with the two
		// values in this whole feature that no screen shows. It now names the line
		// the way the form does (side + amount) and identifies the axis by the
		// shares that were keyed on it, so a line analysed on Departments AND
		// Branches in parallel says which of the two to open. See the block above
		// costAllocationAxisKey.
		//
		// Signed: a NEGATIVE shortfall is an OVER-allocation, and "-15000 is
		// unattributed" would be nonsense, so the two cases get different sentences.
		short := want.Sub(allocated)
		if short.IsNegative() {
			return fmt.Errorf(
				"ledger: one cost category on %s totals %s (%s) but the line is only %s — %s more has been attributed to departments than was actually spent, so the Cost Centre Summary would overstate cost against the Profit & Loss by that amount. Any OTHER cost category on that line is complete and must not be changed",
				describePosting(postings, key.seq), allocated.StringFixed(4),
				describeAxisShares(as, key), want.StringFixed(4),
				short.Neg().StringFixed(4))
		}
		return fmt.Errorf(
			"ledger: one cost category on %s totals %s (%s) but the line is %s — %s is unattributed, so the Cost Centre Summary would disagree with the Profit & Loss by that amount. Any OTHER cost category on that line is complete and must not be changed",
			describePosting(postings, key.seq), allocated.StringFixed(4),
			describeAxisShares(as, key), want.StringFixed(4),
			short.StringFixed(4))
	}
	return nil
}

// writeCostAllocations inserts the posting_cost_allocation rows for one voucher
// version.
//
// postingIDs is indexed BY PostingSeq — it is the ids post() minted while
// inserting the postings of this version, captured in payload order in the same
// loop. That is the whole mapping between the payload's sequence key and the
// table's posting_id key, and it is held in memory rather than rediscovered: a
// re-query would have to ORDER BY something, `posting` has no ordinal column,
// and a voucher may post twice to the same ledger for the same amount, so no
// ORDER BY exists that recovers the input order. Guessing it wrong attributes
// one department's cost to another with nothing to detect.
//
// ⛔ An ALTER inserts NEW posting rows (posting is HARD IMMUTABLE — a trigger
// raises on UPDATE and DELETE), so the ids here are always the ids of the
// version being written and never the superseded one's. Attaching a correction's
// allocations to the old postings would leave the corrected voucher reporting
// the OLD split forever, which is the failure mode that made this a function
// taking the ids rather than a query.
func writeCostAllocations(ctx context.Context, tx pgx.Tx, companyID uuid.UUID,
	postingIDs []uuid.UUID, as []CostAllocationInput) error {
	for _, a := range as {
		if a.PostingSeq < 0 || a.PostingSeq >= len(postingIDs) {
			// Unreachable given validateCostAllocations ran, and stated as an
			// error rather than a panic because the alternative to a clear
			// message here is a nil-uuid FK violation two frames away.
			return fmt.Errorf(
				"ledger: cost allocation names posting %d but only %d postings were written for this version",
				a.PostingSeq, len(postingIDs))
		}
		allocID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("ledger: new cost allocation id: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO posting_cost_allocation
			   (id, company_id, posting_id, cost_category_id, cost_centre_id, amount)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			allocID, companyID, postingIDs[a.PostingSeq],
			a.CostCategoryID, a.CostCentreID, a.Amount,
		); err != nil {
			return fmt.Errorf("ledger: insert posting_cost_allocation: %w", err)
		}
	}
	return nil
}
