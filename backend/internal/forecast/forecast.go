// Package forecast produces two DETERMINISTIC, read-only reports over the
// immutable ledger: (a) a cash-flow projection in Tally's Inflow/Outflow/Nett
// layout, bucketed from open bills; and (b) a rule-based anomaly report over the
// voucher-version chain. There is NO statistics or ML here (that is the deferred
// wave-3 insightd module) — every number is a pen-and-paper-derivable function of
// the inputs, so the corpus fixtures are an INDEPENDENT oracle.
//
// DESIGN: pure core + thin loader. The projection (Project) and the anomaly
// detector (DetectAnomalies) are PURE functions over explicit inputs — no DB, no
// clock. Service.CashFlow / Service.Anomalies just load rows and call them. This
// is the compliance/tax pattern (pure TaxDerive + golden corpus), and it is the
// only way the anomaly rules are testable: created_at is DB-generated (always
// now()), so AFTER_HOURS positives are unseedable and its near-misses are
// wall-clock flaky through the DB — but trivially deterministic as pure inputs.
package forecast

import (
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// --------------------------------------------------------------------------
// Cash-flow projection (pure)
// --------------------------------------------------------------------------

// FlowKind marks a bill as a receivable (cash IN) or a payable (cash OUT).
type FlowKind string

const (
	Inflow  FlowKind = "inflow"  // a receivable — money we expect to collect
	Outflow FlowKind = "outflow" // a payable — money we expect to pay
)

// MaxHorizonDays caps the projection window so the "infinite horizon" invariance
// test cannot allocate a runaway number of buckets.
const MaxHorizonDays = 3650

// defaultCreditDays is the fallback settlement lag when a bill has neither a due
// date nor the party's credit_days (documented default; Tally uses a similar
// convention).
const defaultCreditDays = 30

// ProjBill is one open bill fed to the projector. Pending is signed to the
// party's natural side (normally positive; a NEGATIVE pending — an advance or
// over-allocation — is KEPT and signed, because reporting.Outstandings' Total
// includes it, and dropping it would break the invariance gate). DueDate is the
// bill's due date if known; else the projector uses VoucherDate + CreditDays, and
// failing that VoucherDate + defaultCreditDays.
type ProjBill struct {
	Kind        FlowKind
	Pending     decimal.Decimal
	DueDate     *time.Time
	VoucherDate time.Time
	CreditDays  *int
}

// DateSource says WHERE a bill's projected settlement date came from, so the
// response can DISCLOSE how much of the projection is observed and how much is
// assumed.
//
// ⛔⛔ THIS TYPE EXISTS BECAUSE THE PROJECTION INVENTS A SETTLEMENT DATE FOR
// EVERY UNPAID BILL AND USED TO SAY SO NOWHERE. The wire carried seven keys —
// as_on, horizon_days, bucket_days, opening_cash, buckets, beyond_horizon,
// overdue_folded_into_first_bucket — and not one of them contained the words
// projection, estimate, basis, assumed or method. An operator reading
// "closing_cash 4,12,300.0000" in a bucket dated three months out has no way to
// tell that number apart from a ledger balance, and a large part of it can rest
// on nothing better than "we assume suppliers are paid 30 days after the invoice
// date" — a constant compiled into this file.
//
// Nobody is guessing a date on a bill that HAS a due date; the disclosure is
// what separates the two, per bill and in money.
type DateSource string

const (
	// The bill carried an explicit due date. Observed, not assumed.
	DateFromDueDate DateSource = "due_date"
	// No due date; the party's own credit_days was applied to the voucher date.
	// A tenant-configured assumption.
	DateFromPartyCreditDays DateSource = "party_credit_days"
	// No due date and no credit_days on the party: defaultCreditDays. ⛔ This is
	// the arm that is a CONVENTION of this build and nothing else.
	DateFromDefault DateSource = "default_credit_days"
)

// expectedDate is the date the bill is projected to settle: due_date, else
// voucher_date + credit_days, else voucher_date + 30.
func (b ProjBill) expectedDate() time.Time {
	d, _ := b.expectedDateWithSource()
	return d
}

// expectedDateWithSource is expectedDate plus WHICH of the three rules produced
// it. One function, so the disclosure can never drift from the arithmetic: a
// fourth rule added here reports itself or it does not compile.
func (b ProjBill) expectedDateWithSource() (time.Time, DateSource) {
	if b.DueDate != nil {
		return dateOnly(*b.DueDate), DateFromDueDate
	}
	days := defaultCreditDays
	src := DateFromDefault
	if b.CreditDays != nil {
		days = *b.CreditDays
		src = DateFromPartyCreditDays
	}
	return dateOnly(b.VoucherDate).AddDate(0, 0, days), src
}

// FlowTotals is an inflow/outflow/nett triple (used for the beyond-horizon
// remainder).
type FlowTotals struct {
	Inflow  decimal.Decimal
	Outflow decimal.Decimal
	Nett    decimal.Decimal
}

// Bucket is one projection period [From, To) with its flows and the running cash
// position (OpeningCash carried from the prior bucket's ClosingCash).
type Bucket struct {
	From        time.Time
	To          time.Time
	Inflow      decimal.Decimal
	Outflow     decimal.Decimal
	Nett        decimal.Decimal
	OpeningCash decimal.Decimal
	ClosingCash decimal.Decimal
}

// BasisCount is how many bills — and how much money — one settlement-date rule
// accounts for. Money is kept as decimal here and rendered fixed-4 on the wire;
// a COUNT is an int and is not money.
type BasisCount struct {
	Bills   int
	Inflow  decimal.Decimal
	Outflow decimal.Decimal
}

func (c *BasisCount) add(b ProjBill) {
	c.Bills++
	if b.Kind == Outflow {
		c.Outflow = c.Outflow.Add(b.Pending)
		return
	}
	c.Inflow = c.Inflow.Add(b.Pending)
}

// Basis is the projection's DISCLOSURE: what it did, and how much of its answer
// is observed versus assumed. See DateSource.
//
// ⛔ RULE 4 — this reports SIDES (inflow/outflow) per source and not one total.
// "80% of the projection rests on an assumed date" is a different statement from
// "80% of the money we expect to RECEIVE rests on one", and only the second tells
// an operator whether to trust the closing-cash line.
type Basis struct {
	// The three settlement-date rules, disjoint and exhaustive over the bills
	// that were projected (zero-pending bills are skipped and counted nowhere,
	// exactly as reporting.Outstandings skips them).
	FromDueDate           BasisCount
	FromPartyCreditDays   BasisCount
	FromDefaultCreditDays BasisCount
	// The constant the third arm applied, published rather than left implicit.
	DefaultCreditDays int
	// Bills whose expected date fell before AsOn and were therefore treated as
	// immediately collectible in the FIRST bucket. Already flagged as a bool by
	// OverdueFolded; this says how much.
	Overdue BasisCount
}

// Projection is the full cash-flow result: rolling buckets from AsOn plus the
// beyond-horizon remainder. OverdueFolded discloses whether any past-due bill was
// folded into the first bucket (immediately collectible). Basis discloses what the
// projection ASSUMED — see Basis and DateSource; a projected number that does not
// say it is projected is indistinguishable from a ledger balance on screen.
type Projection struct {
	AsOn          time.Time
	HorizonDays   int
	BucketDays    int
	OpeningCash   decimal.Decimal
	Buckets       []Bucket
	Beyond        FlowTotals
	OverdueFolded bool
	Basis         Basis
}

// Project buckets the open bills into rolling windows of bucketDays starting at
// asOn (NOT calendar weeks/months — no partial first bucket, so a fixture is
// hand-derivable). A bill whose expected date is:
//   - before asOn (overdue)      -> the FIRST bucket (immediately collectible)
//   - within [asOn, asOn+H)      -> its bucket floor((day-offset)/bucketDays)
//   - on/after asOn+H (H rounded up to a whole bucket) -> the Beyond remainder
//
// The running cash position threads OpeningCash/ClosingCash bucket to bucket.
// Zero-pending bills are skipped (as reporting.Outstandings does); negative
// pendings are kept and signed. The invariant sum(bucket flows)+Beyond == sum of
// input pendings by kind holds exactly, so the caller's invariance gate against
// reporting.Outstandings is a conservation check.
func Project(bills []ProjBill, opening decimal.Decimal, asOn time.Time, horizonDays, bucketDays int) (Projection, error) {
	if bucketDays <= 0 {
		return Projection{}, errors.New("forecast: bucketDays must be > 0")
	}
	if horizonDays <= 0 {
		return Projection{}, errors.New("forecast: horizonDays must be > 0")
	}
	if horizonDays > MaxHorizonDays {
		return Projection{}, errors.New("forecast: horizonDays exceeds the cap")
	}
	asOn = dateOnly(asOn)

	nBuckets := (horizonDays + bucketDays - 1) / bucketDays // ceil
	p := Projection{
		AsOn:        asOn,
		HorizonDays: horizonDays,
		BucketDays:  bucketDays,
		OpeningCash: opening,
		Buckets:     make([]Bucket, nBuckets),
	}
	for k := range p.Buckets {
		p.Buckets[k].From = asOn.AddDate(0, 0, k*bucketDays)
		p.Buckets[k].To = asOn.AddDate(0, 0, (k+1)*bucketDays)
		p.Buckets[k].Inflow = decimal.Zero
		p.Buckets[k].Outflow = decimal.Zero
	}
	p.Beyond = FlowTotals{Inflow: decimal.Zero, Outflow: decimal.Zero, Nett: decimal.Zero}
	p.Basis = Basis{
		FromDueDate:           BasisCount{Inflow: decimal.Zero, Outflow: decimal.Zero},
		FromPartyCreditDays:   BasisCount{Inflow: decimal.Zero, Outflow: decimal.Zero},
		FromDefaultCreditDays: BasisCount{Inflow: decimal.Zero, Outflow: decimal.Zero},
		Overdue:               BasisCount{Inflow: decimal.Zero, Outflow: decimal.Zero},
		DefaultCreditDays:     defaultCreditDays,
	}

	for _, b := range bills {
		if b.Pending.IsZero() {
			continue
		}
		expected, src := b.expectedDateWithSource()
		// Accumulate the disclosure from the SAME call that decides the bucket, so
		// the two cannot disagree. (A separate pass over `bills` would be a second
		// copy of the rule, and this repo has been burned by a second copy before.)
		switch src {
		case DateFromDueDate:
			p.Basis.FromDueDate.add(b)
		case DateFromPartyCreditDays:
			p.Basis.FromPartyCreditDays.add(b)
		default:
			p.Basis.FromDefaultCreditDays.add(b)
		}
		dayOff := daysBetween(asOn, expected)
		idx := 0
		switch {
		case dayOff < 0:
			idx = 0 // overdue -> first bucket
			p.OverdueFolded = true
			p.Basis.Overdue.add(b)
		default:
			idx = dayOff / bucketDays
		}
		if idx >= nBuckets {
			addFlow(&p.Beyond.Inflow, &p.Beyond.Outflow, b)
			continue
		}
		addFlow(&p.Buckets[idx].Inflow, &p.Buckets[idx].Outflow, b)
	}

	prevClose := opening
	for k := range p.Buckets {
		p.Buckets[k].OpeningCash = prevClose
		p.Buckets[k].Nett = p.Buckets[k].Inflow.Sub(p.Buckets[k].Outflow)
		p.Buckets[k].ClosingCash = p.Buckets[k].OpeningCash.Add(p.Buckets[k].Nett)
		prevClose = p.Buckets[k].ClosingCash
	}
	p.Beyond.Nett = p.Beyond.Inflow.Sub(p.Beyond.Outflow)
	return p, nil
}

// addFlow adds a bill's pending to the correct inflow/outflow accumulator.
func addFlow(inflow, outflow *decimal.Decimal, b ProjBill) {
	if b.Kind == Outflow {
		*outflow = outflow.Add(b.Pending)
		return
	}
	*inflow = inflow.Add(b.Pending)
}

// --------------------------------------------------------------------------
// Anomaly detection (pure)
// --------------------------------------------------------------------------

// Anomaly kinds.
const (
	KindBackdated   = "BACKDATED"
	KindAfterHours  = "AFTER_HOURS"
	KindEditBurst   = "EDIT_BURST"
	KindRoundAmount = "ROUND_AMOUNT"
)

// VersionRecord is one voucher_version row (ALL versions, not just current — the
// EDIT_BURST rule needs the supersessions). TaxableValue is meaningful only when
// HasTaxable is true (vv.taxable_value is NULL for non-GST vouchers).
type VersionRecord struct {
	VoucherID    uuid.UUID
	VoucherNo    string
	VoucherDate  time.Time
	CreatedAt    time.Time
	VersionNo    int
	TaxableValue decimal.Decimal
	HasTaxable   bool
}

// Thresholds are the (constant, non-config-v1) rule parameters. TZ is the company
// timezone for AFTER_HOURS (IST, fixed v1).
type Thresholds struct {
	BackdateDays    int
	BusinessFromHr  int // inclusive lower bound of business hours
	BusinessToHr    int // exclusive upper bound
	EditBurstCount  int
	EditBurstWindow time.Duration
	RoundAmountMin  decimal.Decimal
	RoundAmountMod  decimal.Decimal
	TZ              *time.Location
}

// istZone is IST (UTC+5:30), fixed v1 (no per-company TZ yet).
var istZone = time.FixedZone("IST", 5*3600+30*60)

// DefaultThresholds pins the v1 rule parameters.
func DefaultThresholds() Thresholds {
	return Thresholds{
		BackdateDays:    7,
		BusinessFromHr:  7,
		BusinessToHr:    22,
		EditBurstCount:  3,
		EditBurstWindow: 24 * time.Hour,
		RoundAmountMin:  decimal.RequireFromString("10000"),
		RoundAmountMod:  decimal.RequireFromString("1000"),
		TZ:              istZone,
	}
}

// Severity levels per kind.
const (
	SevHigh   = "HIGH"
	SevMedium = "MEDIUM"
	SevLow    = "LOW"
)

// Finding is one flagged anomaly.
type Finding struct {
	Kind      string
	VoucherID uuid.UUID
	VoucherNo string
	Severity  string
	Detail    string
}

// DetectAnomalies applies the four deterministic rules to the records. BACKDATED,
// AFTER_HOURS and ROUND_AMOUNT are per-record; EDIT_BURST is per-voucher (a
// voucher with >= EditBurstCount versions whose created_at span fits within
// EditBurstWindow). The result is sorted deterministically (kind, voucher_no) so
// the corpus comparison is stable.
func DetectAnomalies(recs []VersionRecord, th Thresholds) []Finding {
	tz := th.TZ
	if tz == nil {
		tz = istZone
	}
	var out []Finding

	for _, r := range recs {
		// BACKDATED: voucher_date is more than BackdateDays before it was entered.
		if daysBetween(dateOnly(r.VoucherDate), dateOnly(r.CreatedAt)) > th.BackdateDays {
			out = append(out, Finding{
				Kind: KindBackdated, VoucherID: r.VoucherID, VoucherNo: r.VoucherNo,
				Severity: SevHigh,
				Detail:   "voucher dated " + isoDate(r.VoucherDate) + " but entered " + isoDate(r.CreatedAt),
			})
		}
		// AFTER_HOURS: entered outside [BusinessFromHr, BusinessToHr) local time.
		if hr := r.CreatedAt.In(tz).Hour(); hr < th.BusinessFromHr || hr >= th.BusinessToHr {
			out = append(out, Finding{
				Kind: KindAfterHours, VoucherID: r.VoucherID, VoucherNo: r.VoucherNo,
				Severity: SevMedium,
				Detail:   "entered at " + r.CreatedAt.In(tz).Format("15:04") + " IST",
			})
		}
		// ROUND_AMOUNT: a suspiciously round taxable value (>= min and a multiple
		// of mod). NULL taxable (non-GST voucher) never matches.
		if r.HasTaxable && !r.TaxableValue.IsZero() &&
			r.TaxableValue.GreaterThanOrEqual(th.RoundAmountMin) &&
			r.TaxableValue.Mod(th.RoundAmountMod).IsZero() {
			out = append(out, Finding{
				Kind: KindRoundAmount, VoucherID: r.VoucherID, VoucherNo: r.VoucherNo,
				Severity: SevLow,
				Detail:   "round taxable value " + r.TaxableValue.StringFixed(4),
			})
		}
	}

	// EDIT_BURST: group by voucher; sort each group by created_at; a burst exists
	// if any window of EditBurstCount consecutive versions spans <= EditBurstWindow.
	byVoucher := map[uuid.UUID][]VersionRecord{}
	for _, r := range recs {
		byVoucher[r.VoucherID] = append(byVoucher[r.VoucherID], r)
	}
	for _, group := range byVoucher {
		if len(group) < th.EditBurstCount {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].CreatedAt.Before(group[j].CreatedAt) })
		var burst bool
		for i := 0; i+th.EditBurstCount-1 < len(group); i++ {
			span := group[i+th.EditBurstCount-1].CreatedAt.Sub(group[i].CreatedAt)
			if span <= th.EditBurstWindow {
				burst = true
				break
			}
		}
		if burst {
			out = append(out, Finding{
				Kind: KindEditBurst, VoucherID: group[0].VoucherID, VoucherNo: group[0].VoucherNo,
				Severity: SevMedium,
				Detail:   "3+ edits within 24h",
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].VoucherNo < out[j].VoucherNo
	})
	return out
}

// --------------------------------------------------------------------------
// date helpers
// --------------------------------------------------------------------------

// dateOnly strips the time-of-day, normalising to UTC midnight so day arithmetic
// is exact.
func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// daysBetween returns whole days from -> to (both normalised to UTC dates).
func daysBetween(from, to time.Time) int {
	from = dateOnly(from)
	to = dateOnly(to)
	return int(to.Sub(from).Hours()/24 + 0.5)
}

func isoDate(t time.Time) string { return t.Format("2006-01-02") }
