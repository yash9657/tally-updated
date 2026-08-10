package ledger

// cost_allocation_test.go — THE NO-DATABASE HALF: the per-category invariant,
// and the payload assembly.
//
// In the INTERNAL test package (`ledger`, not `ledger_test`) because
// validateCostAllocations and canonicalCostAllocations are unexported and are
// the two things worth pinning. Driving this through the exported API would need
// Postgres, and then the parallel-allocation ACCEPT case — the case the whole
// design turns on — would be a database test nobody runs in a hurry.
//
// ⛔⛔ THE ONE CLAIM THIS FILE EXISTS TO MAKE. The sum-to-posting invariant is
// PER (POSTING, CATEGORY), never per posting. Two axes each allocate the SAME
// money IN FULL, so a posting-wide total comes to twice the cost and would
// REFUSE CORRECT DATA. TestParallelAllocationAcrossTwoAxesIsAccepted is the
// control for that direction and TestAShortAxisIsRefused is the control for the
// other; either one alone would pass under a wrong implementation.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Fixed ids so failure messages are stable and greppable. The two CENTRES on the
// Branch axis are deliberately named after cities and the two on the Department
// axis after functions, because the bug this file guards against is exactly the
// one where the two sets get added together.
var (
	catDepartment = uuid.MustParse("018f0000-0000-7000-8000-0000000000d1")
	catBranch     = uuid.MustParse("018f0000-0000-7000-8000-0000000000d2")
	ccSales       = uuid.MustParse("018f0000-0000-7000-8000-0000000000c1")
	ccAdmin       = uuid.MustParse("018f0000-0000-7000-8000-0000000000c2")
	ccMumbai      = uuid.MustParse("018f0000-0000-7000-8000-0000000000c3")
	ccDelhi       = uuid.MustParse("018f0000-0000-7000-8000-0000000000c4")
	ledSalaries   = uuid.MustParse("018f0000-0000-7000-8000-0000000000a1")
	ledBank       = uuid.MustParse("018f0000-0000-7000-8000-0000000000a2")
)

func money(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// salaryPostings is the worked example from migration 0040's header and from the
// domain contract: Rs 1,00,000 of salary, Dr Salaries & Wages, Cr Bank.
func salaryPostings() []PostingInput {
	return []PostingInput{
		{LedgerID: ledSalaries, DrCr: Debit, Amount: money("100000.0000")},
		{LedgerID: ledBank, DrCr: Credit, Amount: money("100000.0000")},
	}
}

// departmentSplit is Sales 60,000 / Admin 40,000 on posting 0 — one COMPLETE
// allocation on the Department axis.
func departmentSplit() []CostAllocationInput {
	return []CostAllocationInput{
		{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("60000.0000")},
		{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccAdmin, Amount: money("40000.0000")},
	}
}

// branchSplit is Mumbai 70,000 / Delhi 30,000 on the SAME posting 0 — a second
// COMPLETE allocation, of the SAME money, on a DIFFERENT axis.
func branchSplit() []CostAllocationInput {
	return []CostAllocationInput{
		{PostingSeq: 0, CostCategoryID: catBranch, CostCentreID: ccMumbai, Amount: money("70000.0000")},
		{PostingSeq: 0, CostCategoryID: catBranch, CostCentreID: ccDelhi, Amount: money("30000.0000")},
	}
}

// TestParallelAllocationAcrossTwoAxesIsAccepted is the ACCEPT control, and it is
// the more important half of the pair.
//
// A refusal here is the defect the entire feature is judged on: it means the
// validator summed 60,000 + 40,000 + 70,000 + 30,000 = 2,00,000 against a
// 1,00,000 posting and concluded the operator had double-allocated. They had
// not. They had done the thing multiple cost categories EXIST for. Under that
// implementation a business can enter exactly one axis, which is the same as not
// shipping the feature — and it fails for the businesses that use it properly,
// silently, months in.
func TestParallelAllocationAcrossTwoAxesIsAccepted(t *testing.T) {
	both := append(departmentSplit(), branchSplit()...)

	if err := validateCostAllocations(salaryPostings(), both); err != nil {
		t.Fatalf("a Rs 1,00,000 salary posting allocated IN FULL across Departments (Sales 60,000 / Admin 40,000) AND IN FULL across Branches (Mumbai 70,000 / Delhi 30,000) was REFUSED: %v\n\nBoth allocations are complete and both are correct — they describe the same money from two directions, which is the entire reason cost CATEGORIES exist. This error means the check is summing across axes (2,00,000 against a 1,00,000 posting) instead of grouping by (posting, category). See the header of cost_allocation.go and of migration 0040.", err)
	}

	// And the same two sets in INTERLEAVED order, because a grouping implemented
	// as "watch for the category to change" would pass the test above and fail
	// this one.
	interleaved := []CostAllocationInput{
		departmentSplit()[0], branchSplit()[0], departmentSplit()[1], branchSplit()[1],
	}
	if err := validateCostAllocations(salaryPostings(), interleaved); err != nil {
		t.Fatalf("the same two complete axes, interleaved rather than grouped, were refused: %v. The grouping must be by KEY, not by watching for the category to change between adjacent entries.", err)
	}
}

// TestAShortAxisIsRefused is the REFUSE control for the same invariant, and it
// is the leg that catches "check nothing at all".
//
// Note what it does NOT do: it does not merely assert that some error came back.
// A validator that summed across axes would ALSO refuse this input — for
// completely the wrong reason — so the assertion checks that the message names
// the SHORT AXIS and the UNATTRIBUTED amount. A refusal that fires for the wrong
// reason is worse than no test.
func TestAShortAxisIsRefused(t *testing.T) {
	// Departments: Sales 60,000 / Admin 25,000. 15,000 of a real 1,00,000 cost is
	// attributed to nobody. Branches are complete, so a per-posting check would
	// have nothing to say and only the per-category grouping can see it.
	short := []CostAllocationInput{
		{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("60000.0000")},
		{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccAdmin, Amount: money("25000.0000")},
	}
	short = append(short, branchSplit()...)

	err := validateCostAllocations(salaryPostings(), short)
	if err == nil {
		t.Fatal("Departments allocated only 85,000 of a 1,00,000 salary posting and the split was ACCEPTED. 15,000 of real cost is now attributed to no department: the Cost Centre Summary will disagree with the Profit & Loss by 15,000, and nothing in either report says which axis is short.")
	}
	msg := err.Error()
	// ⛔ THIS ASSERTION USED TO DEMAND `catDepartment.String()` — THE RAW UUID — AND
	// THAT WAS D66 WRITTEN DOWN AS AN EXPECTATION. The operator picked "Department"
	// from a list; they have never seen that id and no screen in this product will
	// show it to them, so a test insisting the refusal quote one was pinning the
	// defect in place. Identify the axis the way a person can: by the shares THEY
	// KEYED, which is what describeAxisShares puts in the message.
	for _, want := range []string{
		"85000.0000",  // what the Department axis totals
		"100000.0000", // the line it must total to
		"15000.0000",  // the unattributed remainder
		"60000.0000",  // ...and the two shares that say WHICH axis,
		"25000.0000",  //    because a person recognises what they typed
		"Dr",          // the line, named the way the voucher form names it
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the refusal must tell the operator which axis is short and by how much in rupees; %q is missing from: %s", want, msg)
		}
	}
	// The BRANCH axis is complete and correct, so nothing of it may appear: neither
	// its id (which is no longer printed at all) nor — the assertion with real teeth
	// — its SHARES, which are what an operator would recognise and "go and fix".
	for _, mustNot := range []string{catBranch.String(), "70000.0000", "30000.0000"} {
		if strings.Contains(msg, mustNot) {
			t.Fatalf("the refusal pointed at the BRANCH axis (%q), which is complete and correct. The operator would go and 'fix' a split that was right. Message: %s", mustNot, msg)
		}
	}
	// The BRANCH axis on its own must still be accepted, which is what proves the
	// refusal above was about Departments being short and not about the request
	// having two axes in it at all.
	if err := validateCostAllocations(salaryPostings(), branchSplit()); err != nil {
		t.Fatalf("the Branch axis alone was refused (%v), so the assertion above proves nothing about WHICH axis was short", err)
	}
}

// TestAnOverAllocatedAxisIsRefusedWithItsOwnSentence covers the other side of
// exact equality. "-15000 is unattributed" would be nonsense, so the two
// directions get different sentences.
func TestAnOverAllocatedAxisIsRefusedWithItsOwnSentence(t *testing.T) {
	over := []CostAllocationInput{
		{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("60000.0000")},
		{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccAdmin, Amount: money("55000.0000")},
	}
	err := validateCostAllocations(salaryPostings(), over)
	if err == nil {
		t.Fatal("1,15,000 was allocated against a 1,00,000 posting and accepted; every department's cost would be overstated and the Cost Centre Summary would exceed the Profit & Loss")
	}
	msg := err.Error()
	if strings.Contains(msg, "-15000") || strings.Contains(msg, "unattributed") {
		t.Fatalf("an OVER-allocation must not be described as unattributed cost or as a negative shortfall; the operator has allocated more than was spent. Message: %s", msg)
	}
	if !strings.Contains(msg, "15000.0000") {
		t.Fatalf("the refusal must quote the 15,000 excess in rupees: %s", msg)
	}
}

// TestExactEqualityHasNoTolerance pins the paisa. A tolerance is how a real
// misallocation hides for a year — every voucher a few paise short, and a
// summary that disagrees with the P&L by an amount that grows with volume and
// matches nothing.
func TestExactEqualityHasNoTolerance(t *testing.T) {
	onePaisaShort := []CostAllocationInput{
		{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("59999.9900")},
		{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccAdmin, Amount: money("40000.0000")},
	}
	if err := validateCostAllocations(salaryPostings(), onePaisaShort); err == nil {
		t.Fatal("a split one paisa short of the posting was accepted. With a tolerance every voucher can be slightly short and the Cost Centre Summary drifts from the Profit & Loss by an amount that grows with volume and reconciles to nothing.")
	}
	// A tenth of a paisa — the fourth decimal place is where the scale actually
	// ends, so this is the smallest representable discrepancy.
	tenthOfAPaisaShort := []CostAllocationInput{
		{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("59999.9999")},
		{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccAdmin, Amount: money("40000.0000")},
	}
	if err := validateCostAllocations(salaryPostings(), tenthOfAPaisaShort); err == nil {
		t.Fatal("a split 0.0001 short of the posting was accepted; the invariant is exact to the fourth decimal place, matching cost_allocation_sums_to_posting")
	}
}

// TestValidateCostAllocations is the table for everything else. Each REFUSE case
// carries the message fragment that proves it was refused for the reason under
// test rather than by tripping an earlier check.
func TestValidateCostAllocations(t *testing.T) {
	for _, tc := range []struct {
		name     string
		postings []PostingInput
		allocs   []CostAllocationInput
		wantErr  string // "" means accept
	}{
		{
			name:     "no allocations at all is legal and must stay legal",
			postings: salaryPostings(),
			allocs:   nil,
		},
		{
			name:     "an empty non-nil slice is the same as none",
			postings: salaryPostings(),
			allocs:   []CostAllocationInput{},
		},
		{
			// Allocation is OPTIONAL PER LEDGER. Most postings in most books are
			// never allocated, and the deferred trigger fires on the ALLOCATION
			// and therefore says nothing about a posting with no rows. A validator
			// that demanded every posting be split would make the feature
			// mandatory the day it shipped.
			name:     "one posting split and the other left alone is legal",
			postings: salaryPostings(),
			allocs:   departmentSplit(),
		},
		{
			name:     "a complete split on the SECOND posting",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 1, CostCategoryID: catBranch, CostCentreID: ccMumbai, Amount: money("100000.0000")},
			},
		},
		{
			name:     "the whole posting to one centre",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("100000.0000")},
			},
		},
		{
			// The most likely client bug on this path: an off-by-one index. It
			// must not reach Postgres, where it arrives as a foreign-key violation
			// naming posting_cost_allocation_posting_fk — a table the operator has
			// never heard of, for a mistake entirely about the form they filled in.
			name:     "posting_seq past the end",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 2, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("100000.0000")},
			},
			// ⛔ THE FRAGMENT USED TO BE "numbered 0 to 1" — a refusal that handed the
			// operator the 0-based range of `posting_seq`, the one number
			// CostAllocationPicker.tsx promises no screen will ever display. An
			// out-of-range index is a defect in the SENDING PROGRAM, so the message
			// now says that instead of inviting a human to count array slots.
			wantErr: "an accounting line this voucher does not have",
		},
		{
			name:     "a negative posting_seq",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: -1, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("100000.0000")},
			},
			wantErr: "an accounting line this voucher does not have",
		},
		{
			// A stock-only voucher (Delivery Note, Stock Journal) posts nothing at
			// all, so there is no cost to attribute. The message has to say that
			// rather than talk about a range of zero lines.
			name:     "allocations on a voucher with no postings",
			postings: nil,
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("100.0000")},
			},
			wantErr: "posts no accounting lines at all",
		},
		{
			name:     "a centre with no axis",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCentreID: ccSales, Amount: money("100000.0000")},
			},
			wantErr: "names no cost category",
		},
		{
			name:     "an axis with no centre",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, Amount: money("100000.0000")},
			},
			wantErr: "no cost centre",
		},
		{
			name:     "a zero share",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("100000.0000")},
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccAdmin, Amount: decimal.Zero},
			},
			wantErr: "must be more than zero",
		},
		{
			// A negative share would let the axis total correctly while telling
			// the summary a department gave money BACK. The magnitude/sign split
			// (posting.dr_cr carries the direction) is the whole reason
			// posting_cost_allocation_amount_positive exists.
			name:     "a negative share that still totals correctly",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("110000.0000")},
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccAdmin, Amount: money("-10000.0000")},
			},
			wantErr: "must be more than zero",
		},
		{
			name:     "the same centre twice on one axis",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("60000.0000")},
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("40000.0000")},
			},
			wantErr: "allocated TWICE",
		},
		{
			// The SAME centre id on two DIFFERENT axes is NOT a duplicate. A
			// centre name is unique within its axis, not within the company
			// (cost_centre_name_unique), so "Mumbai" is legitimately both a Branch
			// and a Project — and even the same ID appearing under two categories
			// is only refused by the composite FK, not by a duplicate check.
			name:     "the same centre id under two different axes is not a duplicate here",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccMumbai, Amount: money("100000.0000")},
				{PostingSeq: 0, CostCategoryID: catBranch, CostCentreID: ccMumbai, Amount: money("100000.0000")},
			},
		},
		{
			// The same centre on the same axis but a DIFFERENT posting is two
			// separate allocations of two separate costs.
			name:     "the same centre on two different postings",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("100000.0000")},
				{PostingSeq: 1, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("100000.0000")},
			},
		},
		{
			// Posting 0's Department axis is complete; posting 1's is short. The
			// grouping key is (posting, category), so a check keyed on category
			// ALONE would total 1,00,000 + 60,000 against... nothing coherent, and
			// a check keyed on posting alone would miss it too.
			name:     "one posting complete and another short on the same axis",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("100000.0000")},
				{PostingSeq: 1, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("60000.0000")},
			},
			// ⛔ WAS "posting 1 totals 60000.0000". `posting 1` is the 0-based ordinal,
			// and in single entry it is the operator's FIRST particular — so the old
			// message aimed them at the wrong row by construction. The line is now
			// named as the form names it: salaryPostings()[1] is the Cr Bank leg.
			wantErr: "the Cr 100000.0000 line totals 60000.0000",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCostAllocations(tc.postings, tc.allocs)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected this split to be accepted, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected a refusal containing %q, got none — the database's deferred trigger would refuse it at COMMIT instead, after the voucher number had been allocated and rolled back", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("refused for the WRONG REASON. A refusal that fires for the wrong reason is worse than no test.\n  want fragment: %q\n  got:           %v", tc.wantErr, err)
			}
		})
	}
}

// TestCanonicalCostAllocationAssembly pins the wire form: fixed-4 amounts, the
// sequence as a decimal string, ids as canonical uuid text, and the ORDER of the
// request preserved.
func TestCanonicalCostAllocationAssembly(t *testing.T) {
	got := canonicalCostAllocations(append(departmentSplit(), branchSplit()...))
	if len(got) != 4 {
		t.Fatalf("expected 4 canonical allocations, got %d", len(got))
	}
	want := []CanonicalCostAllocation{
		{PostingSeq: "0", CostCategoryID: catDepartment.String(), CostCentreID: ccSales.String(), Amount: "60000.0000"},
		{PostingSeq: "0", CostCategoryID: catDepartment.String(), CostCentreID: ccAdmin.String(), Amount: "40000.0000"},
		{PostingSeq: "0", CostCategoryID: catBranch.String(), CostCentreID: ccMumbai.String(), Amount: "70000.0000"},
		{PostingSeq: "0", CostCategoryID: catBranch.String(), CostCentreID: ccDelhi.String(), Amount: "30000.0000"},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("canonical allocation %d is %+v, want %+v", i, got[i], want[i])
		}
	}

	// The SCALE, specifically. A whole-rupee amount must still hash as fixed-4:
	// 60000 and 60000.0000 are the same number and different bytes, and a jsonb
	// column does not preserve the text it was given, so VerifyChain re-derives
	// the bytes from the decoded struct. An amount that could be spelled two ways
	// makes a correctly stored voucher read as tampered with.
	whole := canonicalCostAllocations([]CostAllocationInput{
		{PostingSeq: 3, CostCategoryID: catBranch, CostCentreID: ccDelhi, Amount: money("1")},
	})
	if whole[0].Amount != "1.0000" {
		t.Fatalf("a whole-rupee allocation serialised as %q, not %q — the scale must survive the JSONB round trip", whole[0].Amount, "1.0000")
	}
	if whole[0].PostingSeq != "3" {
		t.Fatalf("posting_seq serialised as %q, not %q", whole[0].PostingSeq, "3")
	}
	// A share with real paise, not truncated and not rounded.
	paise := canonicalCostAllocations([]CostAllocationInput{
		{PostingSeq: 0, CostCategoryID: catBranch, CostCentreID: ccDelhi, Amount: money("33333.3333")},
	})
	if paise[0].Amount != "33333.3333" {
		t.Fatalf("a share of 33333.3333 serialised as %q", paise[0].Amount)
	}
}

// TestNoAllocationsProducesANilSliceAndNotAnEmptyOne is the hash-identity half,
// and it protects data already in the field.
//
// `cost_allocations,omitempty` omits nil and empty alike, so this is not about
// the JSON of a payload built here — it is about the DISCIPLINE that keeps it
// that way. A helper returning make([]CanonicalCostAllocation, 0) would look
// correct at every call site and would emit `"cost_allocations":[]` the moment
// anybody dropped the omitempty tag or switched encoders, changing the hash of
// every voucher that carries no allocations — which is every voucher posted
// before migration 0040.
func TestNoAllocationsProducesANilSliceAndNotAnEmptyOne(t *testing.T) {
	for _, in := range [][]CostAllocationInput{nil, {}} {
		got := canonicalCostAllocations(in)
		if got != nil {
			t.Fatalf("canonicalCostAllocations(%#v) returned a non-nil %#v. An empty non-nil slice is one omitempty tag away from emitting \"cost_allocations\":[] and re-hashing every voucher already in every customer's database, which VerifyChain then reports as tampering.", in, got)
		}
	}

	// And the payload really is byte-identical to the pre-0040 vector when the
	// slice comes from this helper. vectorPayload/preInventoryJSON are hard-coded
	// in stock_hash_test.go from a commit that predates all of this, which is
	// what makes them evidence rather than a restatement of current behaviour.
	p := vectorPayload()
	p.CostAllocations = canonicalCostAllocations(nil)
	j, err := p.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(j) != preInventoryJSON {
		t.Fatalf("a voucher with no cost allocations no longer marshals to the bytes it was signed with.\n  now:    %s\n  before: %s", j, preInventoryJSON)
	}
}

// TestPostAssemblesTheSameSliceItValidates guards the seam between the two
// renderings in the only way a no-DB test can: by proving the canonical form is
// derived from the SAME CostAllocationInput values the validator judged, field
// for field. The DB-backed test asserts the ROWS agree with the payload; this
// asserts nothing is transformed on the way.
func TestPostAssemblesTheSameSliceItValidates(t *testing.T) {
	in := append(departmentSplit(), branchSplit()...)
	if err := validateCostAllocations(salaryPostings(), in); err != nil {
		t.Fatalf("fixture must be valid: %v", err)
	}
	canon := canonicalCostAllocations(in)
	total := decimal.Zero
	for i, a := range in {
		if canon[i].CostCentreID != a.CostCentreID.String() {
			t.Fatalf("allocation %d was assembled against centre %s but validated against %s: the rows and the signature would describe different departments", i, canon[i].CostCentreID, a.CostCentreID)
		}
		if canon[i].CostCategoryID != a.CostCategoryID.String() {
			t.Fatalf("allocation %d changed axis between validation and assembly (%s vs %s)", i, canon[i].CostCategoryID, a.CostCategoryID)
		}
		if canon[i].Amount != a.Amount.StringFixed(4) {
			t.Fatalf("allocation %d's amount changed between validation and assembly (%s vs %s)", i, canon[i].Amount, a.Amount.StringFixed(4))
		}
		total = total.Add(a.Amount)
	}
	// Stated explicitly so the next reader cannot mistake it for a bug: the
	// across-axis total IS twice the posting, and that is CORRECT. There is no
	// legitimate "total allocated" figure spanning axes, which is why no report
	// may sum across them.
	if !total.Equal(money("200000.0000")) {
		t.Fatalf("the two parallel axes should total 2,00,000 across a 1,00,000 posting (each axis complete in itself); got %s. If this changed, the fixture no longer exercises parallel allocation.", total.StringFixed(4))
	}
}

// TestNoRefusalPrintsAUUIDOrAPostingOrdinal is the CLASS test for D66, and it is
// deliberately a property over EVERY refusal this file can produce rather than a
// check on the one message the audit happened to read.
//
// ⛔⛔ THE TWO VALUES THIS FEATURE PROMISES NEVER TO SHOW A HUMAN.
//
//  1. A COST CATEGORY / COST CENTRE **UUID**. The operator picked "Department"
//     from a list. They have never seen its primary key, no screen renders one,
//     and the picker cannot be searched by one — so a refusal quoting it names
//     the axis to a database and to nobody else.
//  2. **`posting_seq`.** CostAllocationPicker.tsx's header states the rule for
//     the whole product: it is a 0-based ordinal no screen displays. Worse than
//     useless — in single entry the postings array is `[primary, ...particulars]`,
//     so "posting 1" is the row the operator sees FIRST. A refusal quoting it
//     sends them to the wrong line by construction.
//
// Fixing only the sum message would have left four other sentences on this path
// still printing both (the out-of-range check, the missing-axis check, the
// missing-centre check, the duplicate check). Every refusal is walked here so a
// message added later cannot quietly reintroduce either.
func TestNoRefusalPrintsAUUIDOrAPostingOrdinal(t *testing.T) {
	// A v4/v7 UUID in any message, and any phrasing that hands over an ordinal.
	uuidRe := regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	ordinalRe := regexp.MustCompile(`(?i)posting[_ ]?(seq|\d|number|index)|line \d+ of|numbered \d`)

	for _, tc := range []struct {
		name     string
		postings []PostingInput
		allocs   []CostAllocationInput
	}{
		{
			name:     "sequence past the end",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 7, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("100000.0000")},
			},
		},
		{
			name:     "no postings at all",
			postings: nil,
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("100.0000")},
			},
		},
		{
			name:     "no axis",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCentreID: ccSales, Amount: money("100000.0000")},
			},
		},
		{
			name:     "no centre",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, Amount: money("100000.0000")},
			},
		},
		{
			name:     "a zero share",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: decimal.Zero},
			},
		},
		{
			name:     "the same centre twice",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("60000.0000")},
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("40000.0000")},
			},
		},
		{
			name:     "a short axis",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("60000.0000")},
			},
		},
		{
			name:     "an over-allocated axis",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 0, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("160000.0000")},
			},
		},
		{
			// The SECOND posting, so a message that leaked the ordinal would print a
			// non-zero one — the case a fixture pinned to posting 0 cannot see, since
			// "0" hides inside amounts like "100000.0000".
			name:     "a short axis on the second line",
			postings: salaryPostings(),
			allocs: []CostAllocationInput{
				{PostingSeq: 1, CostCategoryID: catBranch, CostCentreID: ccMumbai, Amount: money("60000.0000")},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateCostAllocations(tc.postings, tc.allocs)
			if err == nil {
				t.Fatal("this input must be refused; the case proves nothing about the wording of a refusal that never happens")
			}
			msg := err.Error()
			if m := uuidRe.FindString(msg); m != "" {
				t.Fatalf("the refusal an operator reads quotes a raw UUID (%s). They picked a name from a list and no screen in this product shows an id, so this sentence names the axis to a database and to nobody else.\n  %s", m, msg)
			}
			if m := ordinalRe.FindString(msg); m != "" {
				t.Fatalf("the refusal an operator reads quotes a posting ordinal (%q). posting_seq is 0-based over [primary, ...particulars], so it points at the row BEFORE the one meant — CostAllocationPicker.tsx states that no screen may ever display it.\n  %s", m, msg)
			}
		})
	}
}

// TestARefusalNamesTheLineTheWayTheFormDoes is the positive half: having taken
// the ordinal away, the message must still say WHICH line — otherwise D66 has
// been traded for a refusal nobody can act on at all, which is the failure mode
// RULE 3 is about.
func TestARefusalNamesTheLineTheWayTheFormDoes(t *testing.T) {
	// The Cr leg, so "Dr"/"Cr" actually discriminates.
	err := validateCostAllocations(salaryPostings(), []CostAllocationInput{
		{PostingSeq: 1, CostCategoryID: catBranch, CostCentreID: ccMumbai, Amount: money("60000.0000")},
	})
	if err == nil {
		t.Fatal("a 60,000 allocation against a 1,00,000 line was accepted")
	}
	if !strings.Contains(err.Error(), "the Cr 100000.0000 line") {
		t.Fatalf("the refusal must name the line by its SIDE and AMOUNT — both of which are on the form in front of the operator: %s", err.Error())
	}

	// ⚠️ AND WHEN SIDE + AMOUNT IS AMBIGUOUS IT SAYS SO. Two identical Dr legs are
	// legal (two 5,000 expense lines), and "the Dr 5000.0000 line" would then point
	// at both. The disambiguator counts LINES ON THE SCREEN, 1-based, which is
	// something a person can do with their eyes — it is not the wire ordinal.
	twins := []PostingInput{
		{LedgerID: ledSalaries, DrCr: Debit, Amount: money("5000.0000")},
		{LedgerID: ledSalaries, DrCr: Debit, Amount: money("5000.0000")},
		{LedgerID: ledBank, DrCr: Credit, Amount: money("10000.0000")},
	}
	err = validateCostAllocations(twins, []CostAllocationInput{
		{PostingSeq: 1, CostCategoryID: catDepartment, CostCentreID: ccSales, Amount: money("2000.0000")},
	})
	if err == nil {
		t.Fatal("a 2,000 allocation against a 5,000 line was accepted")
	}
	if !strings.Contains(err.Error(), "the second of 2 lines that read the same") {
		t.Fatalf("with two identical Dr legs the refusal must say WHICH of them, by counting what is on screen: %s", err.Error())
	}
}
