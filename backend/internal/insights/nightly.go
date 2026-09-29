package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/forecast"
	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/profile"
	"github.com/khata/cored/internal/reporting"
)

// nightly.go: the nightly run — W3's profile.Refresh, then every nightly
// detector, then revalidation of what is still open — in ONE transaction per
// (company, business day), ending with the insight_run row that makes it
// idempotent.
//
// ⛔ ONE TRANSACTION, FOR THE SAME REASON profile.Refresh HAS ONE (D122-9).
// The detectors read facts the refresh has just written (uncommitted, visible
// only in this transaction), reports, and the forecast; if any of them read a
// different moment of the books than the others, an insight could combine a
// receivable from before a receipt with a lag from after it. So every reader
// here — the refresh, reporting, forecast — is handed a runner that REUSES
// this transaction (txRunner).

var istZone = time.FixedZone("IST", 5*3600+30*60)

// BusinessDate is the IST calendar date of an instant, at UTC midnight. The
// books' "today" is IST; the server's is UTC, and a nightly run dated by the
// wrong one is a day off for five and a half hours of every day (the trap
// profile.Options.AsOf records).
func BusinessDate(t time.Time) time.Time { return dateOnly(t.In(istZone)) }

// Window constants of the nightly detectors. Declared, not tuned.
const (
	WindowDays       = 365 // profile.Refresh's default evidence window
	ReorderHorizon   = 14  // days of cover "reorder now" wants (no lead time is recorded; W3 owes supplier lead time)
	DeadStockDays    = 90  // days without an outward movement
	BackdateDays     = 30  // an entry dated more than this before it was made
	PriceCreepPct    = 5   // supplier rate drift that counts as creep
	CashHorizonWeeks = 13
)

type txRunner struct{ tx pgx.Tx }

func (r txRunner) RunInTx(ctx context.Context, fn db.TxFunc) error         { return fn(ctx, r.tx) }
func (r txRunner) RunInReadOnlyTx(ctx context.Context, fn db.TxFunc) error { return fn(ctx, r.tx) }

// TypeStats counts one type's outcomes in a run.
type TypeStats struct {
	Inserted   int `json:"inserted"`
	Unchanged  int `json:"unchanged"`
	Superseded int `json:"superseded"`
	Refused    int `json:"refused"`
}

// Stats is what a nightly run did. It is stored as insight_run.stats.
type Stats struct {
	AsOf            string               `json:"as_of"`
	AlreadyRan      bool                 `json:"already_ran,omitempty"`
	ProfileFacts    int                  `json:"profile_facts"`
	ByType          map[string]TypeStats `json:"by_type"`
	EvidenceRefused []string             `json:"evidence_refused,omitempty"`
	Expired         int                  `json:"expired"`
	Revalidated     int                  `json:"revalidated"`
}

// ErrAlreadyRan is not returned; AlreadyRan in Stats says so. Declared for
// callers that want to branch on it by value.
var errStop = errors.New("stop")

// RunNightly runs the night for the company in ctx at business date asOf.
func RunNightly(ctx context.Context, r Runner, companyID uuid.UUID, asOf time.Time) (Stats, error) {
	asOf = dateOnly(asOf)
	st := Stats{AsOf: asOf.Format(isoDate), ByType: map[string]TypeStats{}}
	err := r.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('insights.nightly:' || $1::text))`, companyID); err != nil {
			return err
		}
		var done bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM insight_run WHERE run_date = $1 AND kind = 'nightly')`,
			asOf).Scan(&done); err != nil {
			return err
		}
		if done {
			st.AlreadyRan = true
			return errStop
		}
		res, err := profile.Refresh(ctx, txRunner{tx}, companyID, profile.Options{AsOf: asOf.Format(isoDate), WindowDays: WindowDays})
		switch {
		case errors.Is(err, profile.ErrAsOfBehind):
			// The facts already describe a LATER day (a clock moved back, or an
			// operator ran a future as-of). W3 refuses backfill rather than fake
			// it, so the fact-backed detectors find nothing tonight and say so.
			st.ProfileFacts = -1
		case err != nil:
			return fmt.Errorf("insights: profile refresh: %w", err)
		default:
			st.ProfileFacts = res.Facts
		}

		n := &night{ctx: ctx, tx: tx, companyID: companyID, asOf: asOf,
			from: asOf.AddDate(0, 0, -(WindowDays - 1))}
		cands, err := n.detectAll()
		if err != nil {
			return err
		}
		seen := map[uuid.UUID]bool{}
		for _, c := range cands {
			id, outcome, err := store(ctx, tx, companyID, c)
			if err != nil && outcome != Refused {
				return err
			}
			ts := st.ByType[c.Type]
			switch outcome {
			case Inserted:
				ts.Inserted++
			case Unchanged:
				ts.Unchanged++
			case Superseded:
				ts.Superseded++
			case Refused:
				ts.Refused++
				// The mismatch is the useful half: which figure, stored vs
				// re-derived. It goes into insight_run.stats, where an operator
				// (and the ADR's residual list) can read it.
				st.EvidenceRefused = append(st.EvidenceRefused, err.Error())
			}
			st.ByType[c.Type] = ts
			if id != uuid.Nil {
				seen[id] = true
			}
		}
		n.factsBehind = st.ProfileFacts == -1
		exp, reval, err := n.retire(seen)
		if err != nil {
			return err
		}
		st.Expired, st.Revalidated = exp, reval
		stats, _ := json.Marshal(st)
		_, err = tx.Exec(ctx, `INSERT INTO insight_run (id, company_id, run_date, kind, stats)
		                       VALUES ($1, $2, $3, 'nightly', $4::jsonb)`,
			uuid.Must(uuid.NewV7()), companyID, asOf, string(stats))
		return err
	})
	if errors.Is(err, errStop) {
		err = nil
	}
	return st, err
}

type night struct {
	ctx       context.Context
	tx        pgx.Tx
	companyID uuid.UUID
	asOf      time.Time
	from      time.Time
	// factsBehind: profile.Refresh refused tonight's as-of (ErrAsOfBehind), so
	// the fact-backed detectors ran on nothing and their silence proves no
	// insight cleared.
	factsBehind bool
}

// factBacked are the nightly types whose detector reads entity_facts at the
// run's as-of — the ones that find nothing when the facts are behind.
var factBacked = map[string]bool{
	TypeCustomerSlower: true, TypeSupplierPriceCreep: true, TypeReorderNow: true, TypeDeadStock: true,
}

func (n *night) asOfS() string { return n.asOf.Format(isoDate) }
func (n *night) fromS() string { return n.from.Format(isoDate) }

func (n *night) detectAll() ([]Candidate, error) {
	var out []Candidate
	for _, det := range []func() ([]Candidate, error){
		n.customerSlower, n.supplierNotIn2B, n.priceCreep, n.reorderDue, n.reorderNow, n.deadStock,
		n.cashShortfall, n.gstLiability, n.tdsNotDeposited, n.backdated,
	} {
		cs, err := det()
		if err != nil {
			return nil, err
		}
		for i := range cs {
			cs[i].AsOf = n.asOf
		}
		out = append(out, cs...)
	}
	return out, nil
}

// retire withdraws what is no longer true. A NIGHTLY insight not re-detected
// tonight has cleared (the customer paid, the TDS was deposited); a REAL-TIME
// one is re-judged by its own evidence — if the SQL no longer reproduces its
// numbers (the e-way bill was raised, the duplicate deleted), it is withdrawn.
// Either way the row is 'expired', never deleted: the feedback given on it
// stays attached to what the owner was actually shown.
//
// ⛔ ABSENCE IS EVIDENCE ONLY WHEN THE DETECTOR COULD HAVE SEEN IT (the wave-2
// review). Three cases withdrew insights that were still true:
//
//   - a NIGHTLY insight detected at a LATER as-of than tonight's (an operator
//     backfilling a missed day with NightlyFor, a clock stepped back): tonight
//     judged an earlier book, so its not finding it says nothing. Kept.
//   - a fact-backed nightly insight on a night whose facts were behind
//     (ErrAsOfBehind): its detector ran on nothing. Kept.
//   - a REAL-TIME credit-limit breach whose evidence stopped reproducing
//     because the balance moved (a part-payment, a limit change) while the
//     party is still over: re-derived from the books and superseded with the
//     new figures, not expired. Only a party back within its limit clears.
func (n *night) retireKeeps(in Insight) bool {
	if in.AsOf > n.asOfS() {
		return true
	}
	return in.Cadence == string(Nightly) && n.factsBehind && factBacked[in.Type]
}
func (n *night) retire(seen map[uuid.UUID]bool) (expired, revalidated int, err error) {
	rows, err := n.tx.Query(n.ctx, selectInsight+` WHERE i.state = 'open'`)
	if err != nil {
		return 0, 0, err
	}
	var open []Insight
	for rows.Next() {
		in, err := scanInsight(rows)
		if err != nil {
			rows.Close()
			return 0, 0, err
		}
		open = append(open, in)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	for _, in := range open {
		if seen[in.ID] || n.retireKeeps(in) {
			continue
		}
		withdraw := false
		switch {
		case in.Cadence == string(Nightly):
			withdraw = true
		default:
			revalidated++
			sp, err := n.tx.Begin(n.ctx)
			if err != nil {
				return 0, 0, err
			}
			verr := Verify(n.ctx, sp, in.Candidate())
			_ = sp.Rollback(n.ctx)
			withdraw = verr != nil
			if withdraw && in.Type == TypeCreditLimitBreach {
				kept, err := n.rederiveCreditLimit(in)
				if err != nil {
					return 0, 0, err
				}
				withdraw = !kept
			}
		}
		if !withdraw {
			continue
		}
		if _, err := n.tx.Exec(n.ctx, `UPDATE insight SET state = 'expired', state_changed_at = clock_timestamp(),
		                                   state_changed_by = 'nightly' WHERE id = $1`, in.ID); err != nil {
			return 0, 0, err
		}
		expired++
	}
	if tag, err := n.tx.Exec(n.ctx, `UPDATE insight SET state = 'expired', state_changed_at = clock_timestamp(),
	                                     state_changed_by = 'expiry' WHERE state = 'open' AND expires_at < $1`,
		n.asOf.Add(24*time.Hour)); err != nil {
		return 0, 0, err
	} else {
		expired += int(tag.RowsAffected())
	}
	return expired, revalidated, nil
}

// rederiveCreditLimit re-judges a breach whose evidence no longer reproduces:
// if the party is still over its limit the breach is stored afresh (store
// supersedes the old row, re-running the evidence first); kept reports that.
func (n *night) rederiveCreditLimit(in Insight) (bool, error) {
	id, err := uuid.Parse(in.EntityKey)
	if err != nil {
		return false, nil
	}
	c, ok, err := creditLimitCandidate(n.ctx, n.tx, id, in.Payload["voucher_no"])
	if err != nil || !ok {
		return false, err
	}
	exp := n.asOf.Add(RealtimeExpiry)
	c.AsOf, c.ExpiresAt = n.asOf, &exp
	_, outcome, err := store(n.ctx, n.tx, n.companyID, c)
	if outcome == Refused {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// fact is one current entity_facts row at the run's as-of.
type fact struct {
	id                    uuid.UUID
	entityKey, entityName string
	entityID              *uuid.UUID
	qualifierKey          string
	qualifierName         string
	num                   decimal.Decimal
	text                  string
}

// facts reads the facts profile.Refresh wrote at this as-of, keyed by
// entity_key (+ "|" + qualifier_key when it has one).
func (n *night) facts(key string) (map[string]fact, error) {
	rows, err := n.tx.Query(n.ctx, `
		SELECT id, entity_key, entity_name, entity_id, qualifier_key, qualifier_name,
		       COALESCE(value_num, 0), COALESCE(value_text, '')
		  FROM entity_facts
		 WHERE fact_key = $1 AND superseded_at IS NULL AND valid_to IS NULL AND valid_from = $2::date`,
		key, n.asOfS())
	if err != nil {
		return nil, fmt.Errorf("insights: facts %s: %w", key, err)
	}
	defer rows.Close()
	out := map[string]fact{}
	for rows.Next() {
		var f fact
		if err := rows.Scan(&f.id, &f.entityKey, &f.entityName, &f.entityID, &f.qualifierKey, &f.qualifierName, &f.num, &f.text); err != nil {
			return nil, err
		}
		k := f.entityKey
		if f.qualifierKey != "" {
			k += "|" + f.qualifierKey
		}
		out[k] = f
	}
	return out, rows.Err()
}

func sortedKeys(m map[string]fact) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// ============================================================================
// customer slower than their own norm
// ============================================================================

// A bill is "late for this customer" when it is older than the customer's
// OWN 90th-percentile days to pay (W3's receipt_lag_p90_days, from at least
// three fully settled bills). Tenant-relative before absolute (D8's build rule
// 3): a 60-day bill is normal for Ganga and alarming for Kaveri.
//
// Pending comes from reporting.Outstandings (via the detector); the evidence
// re-nets bill_allocation itself. voucher_ids is every voucher with an
// allocation on an overdue bill — the invoice that raised it AND the receipts
// and notes that part-settled it — so the listed vouchers net to
// overdue_amount (the wave-2 review: the raising invoices alone overstated it
// by every part-payment). Opening bills are out of both: they have no
// raise date, and an age measured from the books' first day is not a habit.
const evidenceCustomerSlower = `
	WITH p90 AS (SELECT value_num AS d FROM entity_facts WHERE id = $3::uuid),
	a AS (
	  SELECT ba.ref_name,
	         SUM(CASE WHEN ba.dr_cr = 'D' THEN ba.amount ELSE -ba.amount END) AS pending,
	         MIN(vv.voucher_date) FILTER (WHERE ba.ref_type = 'NEW') AS raised,
	         (array_agg(vv.voucher_id ORDER BY vv.voucher_date, vv.voucher_id) FILTER (WHERE ba.ref_type = 'NEW'))[1] AS raised_by
	    FROM bill_allocation ba
	    JOIN voucher_version vv ON vv.id = ba.voucher_version_id` + currentJoin + `
	   WHERE ba.ledger_id = $1::uuid AND vv.voucher_date <= $2::date AND ba.ref_name <> 'On Account'
	   GROUP BY ba.ref_name),
	o AS (SELECT a.* FROM a, p90 WHERE a.pending > 0 AND a.raised IS NOT NULL AND ($2::date - a.raised) > p90.d)
	SELECT (SELECT d FROM p90) AS p90_days,
	       (SELECT count(*) FROM o) AS overdue_bills,
	       (SELECT max($2::date - raised) FROM o) AS oldest_days,
	       (SELECT COALESCE(sum(pending), 0) FROM o) AS overdue_amount,
	       ARRAY(SELECT DISTINCT vv.voucher_id
	               FROM bill_allocation ba
	               JOIN voucher_version vv ON vv.id = ba.voucher_version_id` + currentJoin + `
	              WHERE ba.ledger_id = $1::uuid AND vv.voucher_date <= $2::date
	                AND ba.ref_name IN (SELECT ref_name FROM o)
	              ORDER BY 1 LIMIT 5000) AS voucher_ids`

func (n *night) customerSlower() ([]Candidate, error) {
	p90s, err := n.facts("receipt_lag_p90_days")
	if err != nil {
		return nil, err
	}
	settled, err := n.facts("bills_settled_count")
	if err != nil {
		return nil, err
	}
	if len(p90s) == 0 {
		return nil, nil
	}
	rep := reporting.NewService(txRunner{n.tx})
	out, err := rep.Outstandings(n.ctx, n.companyID, reporting.Receivables, n.asOfS(), nil)
	if err != nil {
		return nil, fmt.Errorf("insights: outstandings: %w", err)
	}
	var cands []Candidate
	for _, party := range out.Parties {
		key := party.LedgerID.String()
		p90, ok := p90s[key]
		if !ok || settled[key].num.LessThan(decimal.NewFromInt(3)) {
			continue
		}
		raised, err := n.raiseDates(party.LedgerID)
		if err != nil {
			return nil, err
		}
		var (
			bills  int
			oldest int
			amount = decimal.Zero
			refs   []string
		)
		for _, b := range party.Bills {
			if b.OnAccount || !b.Pending.IsPositive() {
				continue
			}
			rd, ok := raised[b.RefName]
			if !ok {
				continue
			}
			age := int(n.asOf.Sub(rd.date).Hours() / 24)
			if decimal.NewFromInt(int64(age)).LessThanOrEqual(p90.num) {
				continue
			}
			bills++
			amount = amount.Add(b.Pending)
			if age > oldest {
				oldest = age
			}
			refs = append(refs, b.RefName)
		}
		if bills == 0 {
			continue
		}
		ids, err := uuidList(n.ctx, n.tx, `
			SELECT DISTINCT vv.voucher_id
			  FROM bill_allocation ba
			  JOIN voucher_version vv ON vv.id = ba.voucher_version_id`+currentJoin+`
			 WHERE ba.ledger_id = $1 AND vv.voucher_date <= $2::date AND ba.ref_name = ANY($3)
			 ORDER BY 1 LIMIT 5000`,
			party.LedgerID, n.asOfS(), refs)
		if err != nil {
			return nil, err
		}
		cands = append(cands, Candidate{
			Type: TypeCustomerSlower, EntityKind: "party", EntityKey: key, EntityName: party.LedgerName,
			Fingerprint: TypeCustomerSlower + ":" + key, Impact: amount,
			Payload: map[string]string{"party": party.LedgerName, "p90_days": Canonical(UnitDays, p90.num),
				"overdue_bills": fmt.Sprint(bills), "oldest_days": fmt.Sprint(oldest),
				"overdue_amount": Canonical(UnitINR, amount)},
			EvidenceSQL: evidenceCustomerSlower, EvidenceArgs: []string{key, n.asOfS(), p90.id.String()},
			VoucherIDs: ids,
		})
	}
	return cands, nil
}

type raise struct {
	date    time.Time
	voucher uuid.UUID
}

func (n *night) raiseDates(ledgerID uuid.UUID) (map[string]raise, error) {
	rows, err := n.tx.Query(n.ctx, `
		SELECT ba.ref_name, vv.voucher_date, vv.voucher_id
		  FROM bill_allocation ba
		  JOIN voucher_version vv ON vv.id = ba.voucher_version_id`+currentJoin+`
		 WHERE ba.ledger_id = $1 AND ba.ref_type = 'NEW' AND vv.voucher_date <= $2::date
		 ORDER BY vv.voucher_date, vv.voucher_id`, ledgerID, n.asOfS())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]raise{}
	for rows.Next() {
		var ref string
		var r raise
		if err := rows.Scan(&ref, &r.date, &r.voucher); err != nil {
			return nil, err
		}
		if _, ok := out[ref]; !ok {
			out[ref] = r
		}
	}
	return out, rows.Err()
}

// ============================================================================
// supplier missing from GSTR-2B
// ============================================================================

// A supplier who appears NOWHERE in an imported GSTR-2B for a period, while
// the books hold eligible-ITC purchases from them dated in it, has not filed
// (or filed under another GSTIN): the credit on those invoices is not in 2B
// and cannot be claimed (s.16(2)(aa)). Supplier-level, deliberately: the
// document-level match (amount, number, date drift) is itc's reconciliation
// report, which this does not restate. Only the latest statement per period.
const evidenceSupplierNotIn2B = `
	WITH s AS (SELECT id, to_date(return_period, 'MMYYYY') AS m0 FROM gst_inward_statement WHERE id = $2::uuid),
	pv AS (
	  SELECT vv.voucher_id, COALESCE(vv.cgst, 0) + COALESCE(vv.sgst, 0) + COALESCE(vv.igst, 0) AS itc
	    FROM voucher_version vv` + currentJoin + `
	    JOIN ledger l ON l.id = vv.party_ledger_id, s
	   WHERE l.id = $1::uuid
	     AND COALESCE(vv.payload->>'class', '') = 'Purchase'
	     AND NOT vv.is_reverse_charge
	     AND COALESCE(vv.itc_eligibility, 'ELIGIBLE') = 'ELIGIBLE'
	     AND COALESCE(vv.supplier_invoice_date, vv.voucher_date) >= s.m0
	     AND COALESCE(vv.supplier_invoice_date, vv.voucher_date) < (s.m0 + interval '1 month')::date
	     AND COALESCE(vv.cgst, 0) + COALESCE(vv.sgst, 0) + COALESCE(vv.igst, 0) > 0
	     AND NOT EXISTS (SELECT 1 FROM gst_inward_document gd
	                      WHERE gd.statement_id = s.id AND upper(gd.supplier_gstin) = upper(l.gstin)))
	SELECT (SELECT count(*) FROM pv) AS invoices,
	       (SELECT COALESCE(sum(itc), 0) FROM pv) AS itc_at_risk,
	       ARRAY(SELECT voucher_id FROM pv) AS voucher_ids`

func (n *night) supplierNotIn2B() ([]Candidate, error) {
	rows, err := n.tx.Query(n.ctx, `
		SELECT DISTINCT ON (return_period) id, return_period, to_date(return_period, 'MMYYYY')
		  FROM gst_inward_statement
		 WHERE kind = 'GSTR2B' AND superseded_by IS NULL
		   AND to_date(return_period, 'MMYYYY') >= $1::date - interval '6 months'
		   AND to_date(return_period, 'MMYYYY') <= $1::date
		 ORDER BY return_period, ingested_at DESC, id`, n.asOfS())
	if err != nil {
		return nil, fmt.Errorf("insights: 2b statements: %w", err)
	}
	type stmt struct {
		id     uuid.UUID
		period string
		m0     time.Time
	}
	var stmts []stmt
	for rows.Next() {
		var s stmt
		if err := rows.Scan(&s.id, &s.period, &s.m0); err != nil {
			rows.Close()
			return nil, err
		}
		stmts = append(stmts, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Candidate
	for _, s := range stmts {
		// Which GSTINs filed: read once, compared in Go.
		filed := map[string]bool{}
		gs, err := n.tx.Query(n.ctx, `SELECT DISTINCT upper(supplier_gstin) FROM gst_inward_document WHERE statement_id = $1`, s.id)
		if err != nil {
			return nil, err
		}
		for gs.Next() {
			var g string
			if err := gs.Scan(&g); err != nil {
				gs.Close()
				return nil, err
			}
			filed[g] = true
		}
		gs.Close()
		prs, err := n.tx.Query(n.ctx, `
			SELECT l.id, l.name, upper(l.gstin), vv.voucher_id,
			       COALESCE(vv.cgst, 0) + COALESCE(vv.sgst, 0) + COALESCE(vv.igst, 0)
			  FROM voucher_version vv`+currentJoin+`
			  JOIN ledger l ON l.id = vv.party_ledger_id
			 WHERE COALESCE(vv.payload->>'class', '') = 'Purchase' AND NOT vv.is_reverse_charge
			   AND COALESCE(vv.itc_eligibility, 'ELIGIBLE') = 'ELIGIBLE'
			   AND COALESCE(l.gstin, '') <> ''
			   AND COALESCE(vv.supplier_invoice_date, vv.voucher_date) >= $1::date
			   AND COALESCE(vv.supplier_invoice_date, vv.voucher_date) < ($1::date + interval '1 month')::date
			 ORDER BY l.name, vv.voucher_id`, s.m0.Format(isoDate))
		if err != nil {
			return nil, err
		}
		type acc struct {
			id          uuid.UUID
			name, gstin string
			itc         decimal.Decimal
			ids         []uuid.UUID
		}
		var order []*acc
		by := map[uuid.UUID]*acc{}
		for prs.Next() {
			var (
				id          uuid.UUID
				name, gstin string
				vid         uuid.UUID
				tax         decimal.Decimal
			)
			if err := prs.Scan(&id, &name, &gstin, &vid, &tax); err != nil {
				prs.Close()
				return nil, err
			}
			if filed[gstin] || !tax.IsPositive() {
				continue
			}
			a, ok := by[id]
			if !ok {
				a = &acc{id: id, name: name, gstin: gstin}
				by[id] = a
				order = append(order, a)
			}
			a.itc = a.itc.Add(tax)
			a.ids = append(a.ids, vid)
		}
		prs.Close()
		if err := prs.Err(); err != nil {
			return nil, err
		}
		for _, a := range order {
			out = append(out, Candidate{
				Type: TypeSupplierNotIn2B, EntityKind: "party", EntityKey: a.id.String(), EntityName: a.name,
				Fingerprint: TypeSupplierNotIn2B + ":" + a.id.String() + ":" + s.period, Impact: a.itc,
				Payload: map[string]string{"party": a.name, "gstin": a.gstin, "return_period": s.period,
					"invoices": fmt.Sprint(len(a.ids)), "itc_at_risk": Canonical(UnitINR, a.itc)},
				EvidenceSQL: evidenceSupplierNotIn2B, EvidenceArgs: []string{a.id.String(), s.id.String()},
				VoucherIDs: a.ids,
			})
		}
	}
	return out, nil
}

// ============================================================================
// supplier price creep
// ============================================================================

// From W3's per-(supplier, item) first/last rate and drift facts. The facts
// are immutable rows, so the evidence reads them BY ID — the rates it quotes
// are the ones W3 reconciled, not a third derivation of them — and re-derives
// only the quantity (the detector takes it from W3's supplies_item edge) and
// the extra cost.
const evidencePriceCreep = `
	WITH f AS (SELECT value_num AS v FROM entity_facts WHERE id = $1::uuid),
	     l AS (SELECT value_num AS v FROM entity_facts WHERE id = $2::uuid),
	     dr AS (SELECT value_num AS v FROM entity_facts WHERE id = $3::uuid),
	m AS (
	  SELECT sm.qty, vv.voucher_id FROM stock_movement sm
	    JOIN voucher_version vv ON vv.id = sm.voucher_version_id` + currentJoin + `
	   WHERE sm.direction = 'IN' AND COALESCE(vv.payload->>'class', '') = 'Purchase'
	     AND vv.party_ledger_id = $4::uuid AND sm.stock_item_id = $5::uuid
	     AND vv.voucher_date >= $6::date AND vv.voucher_date <= $7::date)
	SELECT f.v AS first_rate, l.v AS last_rate, dr.v AS drift_pct,
	       (SELECT COALESCE(sum(qty), 0) FROM m) AS qty_window,
	       ROUND((l.v - f.v) * (SELECT COALESCE(sum(qty), 0) FROM m), 2) AS extra_cost,
	       ARRAY(SELECT DISTINCT voucher_id FROM m) AS voucher_ids
	  FROM f, l, dr`

func (n *night) priceCreep() ([]Candidate, error) {
	drift, err := n.facts("item_price_drift_pct")
	if err != nil {
		return nil, err
	}
	first, err := n.facts("item_first_rate")
	if err != nil {
		return nil, err
	}
	last, err := n.facts("item_last_rate")
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, k := range sortedKeys(drift) {
		df := drift[k]
		if df.num.LessThan(decimal.NewFromInt(PriceCreepPct)) {
			continue
		}
		ff, ok1 := first[k]
		lf, ok2 := last[k]
		if !ok1 || !ok2 || df.entityID == nil {
			continue
		}
		var qty decimal.Decimal
		err := n.tx.QueryRow(n.ctx, `
			SELECT weight FROM entity_edges
			 WHERE edge_kind = 'supplies_item' AND src_key = $1 AND dst_key = $2
			   AND superseded_at IS NULL AND valid_to IS NULL AND valid_from = $3::date`,
			df.entityKey, df.qualifierKey, n.asOfS()).Scan(&qty)
		if err != nil {
			return nil, fmt.Errorf("insights: supplies_item edge: %w", err)
		}
		ids, err := uuidList(n.ctx, n.tx, `
			SELECT DISTINCT vv.voucher_id FROM stock_movement sm
			  JOIN voucher_version vv ON vv.id = sm.voucher_version_id`+currentJoin+`
			 WHERE sm.direction = 'IN' AND COALESCE(vv.payload->>'class', '') = 'Purchase'
			   AND vv.party_ledger_id = $1 AND sm.stock_item_id = $2::uuid
			   AND vv.voucher_date >= $3::date AND vv.voucher_date <= $4::date`,
			*df.entityID, df.qualifierKey, n.fromS(), n.asOfS())
		if err != nil {
			return nil, err
		}
		extra := lf.num.Sub(ff.num).Mul(qty).Round(2)
		out = append(out, Candidate{
			Type: TypeSupplierPriceCreep, EntityKind: "party", EntityKey: df.entityKey, EntityName: df.entityName,
			Fingerprint: TypeSupplierPriceCreep + ":" + df.entityKey + ":" + df.qualifierKey, Impact: extra,
			Payload: map[string]string{"party": df.entityName, "item": df.qualifierName,
				"first_rate": Canonical(UnitINR, ff.num), "last_rate": Canonical(UnitINR, lf.num),
				"drift_pct": Canonical(UnitPct, df.num), "qty_window": Canonical(UnitQty, qty),
				"extra_cost": Canonical(UnitINR, extra)},
			EvidenceSQL: evidencePriceCreep,
			EvidenceArgs: []string{ff.id.String(), lf.id.String(), df.id.String(), df.entityKey, df.qualifierKey,
				n.fromS(), n.asOfS()},
			VoucherIDs: ids,
		})
	}
	return out, nil
}

// ============================================================================
// customer overdue for their usual reorder (the SIMPLE INTERVAL RULE)
// ============================================================================

// ⛔ NOT BG/NBD. The plan names BG/NBD P(alive) for this insight; that model
// is owed (types.go Owed). This is the rule a shopkeeper would state: a
// customer with at least four order days in the year whose silence has run
// past their usual gap by more than max(7 days, half the gap). The usual gap
// is the LOWER median of the gaps between distinct order days (percentile_disc
// 0.5 in the evidence; index (n-1)/2 of the sorted gaps here), and the typical
// order the lower median of their invoice values.
const evidenceReorderDue = `
	WITH inv AS (
	  SELECT vv.voucher_id, vv.voucher_date AS d,
	         (SELECT COALESCE(SUM(p.amount), 0) FROM posting p
	           WHERE p.voucher_version_id = vv.id AND p.ledger_id = $1::uuid AND p.dr_cr = 'D') AS amt
	    FROM voucher_version vv` + currentJoin + `
	   WHERE COALESCE(vv.payload->>'class', '') = 'Sales' AND vv.party_ledger_id = $1::uuid
	     AND vv.voucher_date >= $2::date AND vv.voucher_date <= $3::date),
	days AS (SELECT DISTINCT d FROM inv),
	gaps AS (SELECT d - lag(d) OVER (ORDER BY d) AS g FROM days)
	SELECT (SELECT percentile_disc(0.5) WITHIN GROUP (ORDER BY g) FROM gaps WHERE g IS NOT NULL) AS median_gap_days,
	       $3::date - (SELECT max(d) FROM days) AS days_since_last,
	       (SELECT max(d) FROM days) AS last_invoice_date,
	       (SELECT count(*) FROM inv) AS invoices,
	       (SELECT percentile_disc(0.5) WITHIN GROUP (ORDER BY amt) FROM inv) AS typical_invoice,
	       ARRAY(SELECT voucher_id FROM inv) AS voucher_ids`

func (n *night) reorderDue() ([]Candidate, error) {
	rows, err := n.tx.Query(n.ctx, `
		SELECT vv.party_ledger_id, l.name, vv.voucher_id, vv.voucher_date,
		       (SELECT COALESCE(SUM(p.amount), 0) FROM posting p
		         WHERE p.voucher_version_id = vv.id AND p.ledger_id = vv.party_ledger_id AND p.dr_cr = 'D')
		  FROM voucher_version vv`+currentJoin+`
		  JOIN ledger l ON l.id = vv.party_ledger_id
		 WHERE COALESCE(vv.payload->>'class', '') = 'Sales'
		   AND vv.voucher_date >= $1::date AND vv.voucher_date <= $2::date
		 ORDER BY l.name, vv.party_ledger_id, vv.voucher_date`, n.fromS(), n.asOfS())
	if err != nil {
		return nil, fmt.Errorf("insights: reorder: %w", err)
	}
	type acc struct {
		id      uuid.UUID
		name    string
		days    []time.Time
		amounts []decimal.Decimal
		ids     []uuid.UUID
	}
	var order []*acc
	by := map[uuid.UUID]*acc{}
	for rows.Next() {
		var (
			pid  uuid.UUID
			name string
			vid  uuid.UUID
			on   time.Time
			amt  decimal.Decimal
		)
		if err := rows.Scan(&pid, &name, &vid, &on, &amt); err != nil {
			rows.Close()
			return nil, err
		}
		a, ok := by[pid]
		if !ok {
			a = &acc{id: pid, name: name}
			by[pid] = a
			order = append(order, a)
		}
		if len(a.days) == 0 || !a.days[len(a.days)-1].Equal(on) {
			a.days = append(a.days, on)
		}
		a.amounts = append(a.amounts, amt)
		a.ids = append(a.ids, vid)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Candidate
	for _, a := range order {
		if len(a.days) < 4 || len(a.ids) > 200 {
			continue
		}
		gaps := make([]int, 0, len(a.days)-1)
		for i := 1; i < len(a.days); i++ {
			gaps = append(gaps, int(a.days[i].Sub(a.days[i-1]).Hours()/24))
		}
		sort.Ints(gaps)
		g := gaps[(len(gaps)-1)/2]
		lastDay := a.days[len(a.days)-1]
		since := int(n.asOf.Sub(lastDay).Hours() / 24)
		slack := g / 2
		if slack < 7 {
			slack = 7
		}
		if since <= g+slack {
			continue
		}
		amts := append([]decimal.Decimal(nil), a.amounts...)
		sort.Slice(amts, func(i, j int) bool { return amts[i].LessThan(amts[j]) })
		typical := amts[(len(amts)-1)/2]
		out = append(out, Candidate{
			Type: TypeCustomerReorderDue, EntityKind: "party", EntityKey: a.id.String(), EntityName: a.name,
			Fingerprint: TypeCustomerReorderDue + ":" + a.id.String(), Impact: typical,
			Payload: map[string]string{"party": a.name, "median_gap_days": fmt.Sprint(g),
				"days_since_last": fmt.Sprint(since), "last_invoice_date": lastDay.Format(isoDate),
				"invoices": fmt.Sprint(len(a.ids)), "typical_invoice": Canonical(UnitINR, typical)},
			EvidenceSQL: evidenceReorderDue, EvidenceArgs: []string{a.id.String(), n.fromS(), n.asOfS()},
			VoucherIDs: a.ids,
		})
	}
	return out, nil
}

// ============================================================================
// reorder now / dead stock — from W3's item facts
// ============================================================================

const lastPurchaseSQL = `
	SELECT sm.rate, vv.voucher_id FROM stock_movement sm
	  JOIN voucher_version vv ON vv.id = sm.voucher_version_id` + currentJoin + `
	 WHERE sm.direction = 'IN' AND COALESCE(vv.payload->>'class', '') = 'Purchase'
	   AND sm.stock_item_id = $1::uuid AND vv.voucher_date <= $2::date
	 ORDER BY vv.voucher_date DESC, vv.created_at DESC, sm.line_no DESC
	 LIMIT 1`

const evidenceReorderNow = `
	WITH c AS (SELECT value_num AS v FROM entity_facts WHERE id = $1::uuid),
	     vel AS (SELECT value_num AS v FROM entity_facts WHERE id = $2::uuid),
	     cov AS (SELECT value_num AS v FROM entity_facts WHERE id = $3::uuid),
	     lp AS (` + lastPurchaseSQL4 + `)
	SELECT c.v AS closing_qty, vel.v AS velocity_qty_per_day, cov.v AS days_of_cover,
	       $6::int AS horizon_days,
	       ROUND(vel.v * $6::int - c.v, 4) AS shortfall_qty,
	       lp.rate AS last_purchase_rate,
	       ROUND(ROUND(vel.v * $6::int - c.v, 4) * lp.rate, 2) AS reorder_value,
	       ARRAY[lp.voucher_id] AS voucher_ids
	  FROM c, vel, cov, lp`

// lastPurchaseSQL4 is lastPurchaseSQL with the evidence's parameter numbers.
const lastPurchaseSQL4 = `
	SELECT sm.rate, vv.voucher_id FROM stock_movement sm
	  JOIN voucher_version vv ON vv.id = sm.voucher_version_id` + currentJoin + `
	 WHERE sm.direction = 'IN' AND COALESCE(vv.payload->>'class', '') = 'Purchase'
	   AND sm.stock_item_id = $4::uuid AND vv.voucher_date <= $5::date
	 ORDER BY vv.voucher_date DESC, vv.created_at DESC, sm.line_no DESC
	 LIMIT 1`

// lastPurchaseSQL23 is lastPurchaseSQL with the dead-stock evidence's numbers.
const lastPurchaseSQL23 = `
	SELECT sm.rate, vv.voucher_id FROM stock_movement sm
	  JOIN voucher_version vv ON vv.id = sm.voucher_version_id` + currentJoin + `
	 WHERE sm.direction = 'IN' AND COALESCE(vv.payload->>'class', '') = 'Purchase'
	   AND sm.stock_item_id = $2::uuid AND vv.voucher_date <= $3::date
	 ORDER BY vv.voucher_date DESC, vv.created_at DESC, sm.line_no DESC
	 LIMIT 1`

func (n *night) lastPurchase(item string) (decimal.Decimal, uuid.UUID, bool, error) {
	var rate decimal.Decimal
	var vid uuid.UUID
	err := n.tx.QueryRow(n.ctx, lastPurchaseSQL, item, n.asOfS()).Scan(&rate, &vid)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Zero, uuid.Nil, false, nil
	}
	return rate, vid, err == nil, err
}

func (n *night) reorderNow() ([]Candidate, error) {
	closing, err := n.facts("closing_qty")
	if err != nil {
		return nil, err
	}
	vel, err := n.facts("velocity_qty_per_day")
	if err != nil {
		return nil, err
	}
	cover, err := n.facts("days_of_cover")
	if err != nil {
		return nil, err
	}
	horizon := decimal.NewFromInt(ReorderHorizon)
	var out []Candidate
	for _, k := range sortedKeys(cover) {
		cv, c, v := cover[k], closing[k], vel[k]
		if !v.num.IsPositive() || !cv.num.LessThan(horizon) || c.id == uuid.Nil {
			continue
		}
		rate, vid, ok, err := n.lastPurchase(k)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue // nothing to value a purchase order at; not guessed
		}
		short := v.num.Mul(horizon).Sub(c.num).Round(4)
		value := short.Mul(rate).Round(2)
		out = append(out, Candidate{
			Type: TypeReorderNow, EntityKind: "item", EntityKey: k, EntityName: cv.entityName,
			Fingerprint: TypeReorderNow + ":" + k, Impact: value,
			Payload: map[string]string{"item": cv.entityName, "closing_qty": Canonical(UnitQty, c.num),
				"velocity_qty_per_day": Canonical(UnitQty, v.num), "days_of_cover": Canonical(UnitQty, cv.num),
				"horizon_days": fmt.Sprint(ReorderHorizon), "shortfall_qty": Canonical(UnitQty, short),
				"last_purchase_rate": Canonical(UnitINR, rate), "reorder_value": Canonical(UnitINR, value)},
			EvidenceSQL: evidenceReorderNow,
			EvidenceArgs: []string{c.id.String(), v.id.String(), cv.id.String(), k, n.asOfS(),
				fmt.Sprint(ReorderHorizon)},
			VoucherIDs: []uuid.UUID{vid},
		})
	}
	return out, nil
}

const evidenceDeadStock = `
	WITH c AS (SELECT value_num AS v FROM entity_facts WHERE id = $1::uuid),
	mv AS (
	  SELECT max(vv.voucher_date) FILTER (WHERE sm.direction = 'OUT') AS lo,
	         min(vv.voucher_date) FILTER (WHERE sm.direction = 'IN') AS fi
	    FROM stock_movement sm
	    JOIN voucher_version vv ON vv.id = sm.voucher_version_id` + currentJoin + `
	   WHERE sm.stock_item_id = $2::uuid AND vv.voucher_date <= $3::date),
	lp AS (` + lastPurchaseSQL23 + `)
	SELECT c.v AS closing_qty, $3::date - COALESCE(mv.lo, mv.fi) AS idle_days,
	       lp.rate AS last_purchase_rate, ROUND(c.v * lp.rate, 2) AS stock_value_at_last_rate,
	       ARRAY[lp.voucher_id] AS voucher_ids
	  FROM c, mv, lp`

func (n *night) deadStock() ([]Candidate, error) {
	closing, err := n.facts("closing_qty")
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, k := range sortedKeys(closing) {
		c := closing[k]
		if !c.num.IsPositive() {
			continue
		}
		var lo, fi *time.Time
		if err := n.tx.QueryRow(n.ctx, `
			SELECT max(vv.voucher_date) FILTER (WHERE sm.direction = 'OUT'),
			       min(vv.voucher_date) FILTER (WHERE sm.direction = 'IN')
			  FROM stock_movement sm
			  JOIN voucher_version vv ON vv.id = sm.voucher_version_id`+currentJoin+`
			 WHERE sm.stock_item_id = $1::uuid AND vv.voucher_date <= $2::date`, k, n.asOfS()).Scan(&lo, &fi); err != nil {
			return nil, err
		}
		since := lo
		if since == nil {
			since = fi
		}
		if since == nil {
			continue
		}
		idle := int(n.asOf.Sub(*since).Hours() / 24)
		if idle < DeadStockDays {
			continue
		}
		rate, vid, ok, err := n.lastPurchase(k)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		value := c.num.Mul(rate).Round(2)
		out = append(out, Candidate{
			Type: TypeDeadStock, EntityKind: "item", EntityKey: k, EntityName: c.entityName,
			Fingerprint: TypeDeadStock + ":" + k, Impact: value,
			Payload: map[string]string{"item": c.entityName, "closing_qty": Canonical(UnitQty, c.num),
				"idle_days": fmt.Sprint(idle), "last_purchase_rate": Canonical(UnitINR, rate),
				"stock_value_at_last_rate": Canonical(UnitINR, value)},
			EvidenceSQL:  evidenceDeadStock,
			EvidenceArgs: []string{c.id.String(), k, n.asOfS()},
			VoucherIDs:   []uuid.UUID{vid},
		})
	}
	return out, nil
}

// ============================================================================
// 13-week cash path
// ============================================================================

// The DETECTOR reads internal/forecast's projection (13 weekly buckets from
// as-of) and fires on the first week whose closing cash is negative. The
// EVIDENCE is a second, independent derivation from the base tables of the
// same three sums — opening cash, and the pending of every open bill whose
// expected settlement falls in or before that week — using the projection's
// stated date rule (due date, else voucher date + the party's credit days,
// else + 30; overdue and undatable folded into week one) and the
// Outstandings netting (bill-wise refs, opening bills, and the "On Account"
// residue of subledger parties). A disagreement between forecast and this SQL
// is REFUSED at write time and counted, never shown.
//
// ⚠️ Known limit: a ref split across two currencies is netted per currency by
// the report and per ref here; the due date could then differ. Such a night
// shows up as evidence_refused, not as a wrong number.
const evidenceCashShortfall = `
	WITH cash AS (
	  SELECT
	    (SELECT COALESCE(SUM(CASE WHEN l.opening_dc = 'D' THEN l.opening_balance ELSE -l.opening_balance END), 0)
	       FROM ledger l JOIN account_group g ON g.id = l.group_id
	      WHERE g.path <@ (SELECT path FROM account_group WHERE name = 'Cash-in-Hand')
	         OR g.path <@ (SELECT path FROM account_group WHERE name = 'Bank Accounts'))
	  + (SELECT COALESCE(SUM(CASE WHEN p.dr_cr = 'D' THEN p.amount ELSE -p.amount END), 0)
	       FROM posting p
	       JOIN ledger l ON l.id = p.ledger_id JOIN account_group g ON g.id = l.group_id
	       JOIN voucher_version vv ON vv.id = p.voucher_version_id` + currentJoin + `
	      WHERE (g.path <@ (SELECT path FROM account_group WHERE name = 'Cash-in-Hand')
	          OR g.path <@ (SELECT path FROM account_group WHERE name = 'Bank Accounts'))
	        AND vv.voucher_date <= $1::date) AS v),
	sub AS (
	  SELECT l.id FROM ledger l JOIN account_group g ON g.id = l.group_id
	   WHERE EXISTS (SELECT 1 FROM account_group anc WHERE anc.behaves_as_subledger AND g.path <@ anc.path)),
	rows_ AS (
	  SELECT b.ledger_id, b.ref_name, b.dr_cr, b.amount, b.due_date
	    FROM bill_allocation b JOIN voucher_version vv ON vv.id = b.voucher_version_id` + currentJoin + `
	   WHERE vv.voucher_date <= $1::date
	  UNION ALL
	  SELECT ob.ledger_id, ob.ref_name, ob.dr_cr, ob.amount, ob.due_date FROM ledger_opening_bill ob
	  UNION ALL
	  SELECT p.ledger_id, 'On Account', p.dr_cr, p.amount, NULL::date
	    FROM posting p JOIN voucher_version vv ON vv.id = p.voucher_version_id` + currentJoin + `
	   WHERE vv.voucher_date <= $1::date AND p.ledger_id IN (SELECT id FROM sub)
	  UNION ALL
	  SELECT b.ledger_id, 'On Account', CASE b.dr_cr WHEN 'D' THEN 'C'::dr_cr ELSE 'D'::dr_cr END, b.amount, NULL::date
	    FROM bill_allocation b JOIN voucher_version vv ON vv.id = b.voucher_version_id` + currentJoin + `
	   WHERE vv.voucher_date <= $1::date AND b.ledger_id IN (SELECT id FROM sub)
	  UNION ALL
	  SELECT lo.id, 'On Account', CASE lo.opening_dc WHEN 'D' THEN 'D'::dr_cr ELSE 'C'::dr_cr END, lo.opening_balance, NULL::date
	    FROM ledger lo WHERE lo.opening_balance <> 0 AND lo.id IN (SELECT id FROM sub)
	  UNION ALL
	  SELECT ob2.ledger_id, 'On Account', CASE ob2.dr_cr WHEN 'D' THEN 'C'::dr_cr ELSE 'D'::dr_cr END, ob2.amount, NULL::date
	    FROM ledger_opening_bill ob2 WHERE ob2.ledger_id IN (SELECT id FROM sub)),
	bills AS (
	  SELECT r.ledger_id, r.ref_name, g.nature,
	         SUM(CASE WHEN (g.nature = 'ASSET') = (r.dr_cr = 'D') THEN r.amount ELSE -r.amount END) AS pending,
	         MIN(r.due_date) AS due
	    FROM rows_ r JOIN ledger l ON l.id = r.ledger_id JOIN account_group g ON g.id = l.group_id
	   WHERE g.nature IN ('ASSET', 'LIABILITY')
	   GROUP BY r.ledger_id, r.ref_name, g.nature),
	dated AS (
	  SELECT b.*, COALESCE(b.due,
	           (SELECT MIN(vv.voucher_date) FROM bill_allocation ba JOIN voucher_version vv ON vv.id = ba.voucher_version_id
	             WHERE ba.ledger_id = b.ledger_id AND ba.ref_name = b.ref_name AND ba.ref_type = 'NEW')
	           + COALESCE((SELECT credit_days FROM ledger WHERE id = b.ledger_id), 30)) AS expected
	    FROM bills b WHERE b.pending <> 0),
	upto AS (SELECT * FROM dated WHERE expected IS NULL OR expected < $3::date)
	SELECT (SELECT v FROM cash) AS opening_cash,
	       $2::date AS week_from, $3::date AS week_to,
	       COALESCE((SELECT SUM(pending) FROM upto WHERE nature = 'ASSET'), 0) AS inflow_through,
	       COALESCE((SELECT SUM(pending) FROM upto WHERE nature = 'LIABILITY'), 0) AS outflow_through,
	       (SELECT v FROM cash) + COALESCE((SELECT SUM(pending) FROM upto WHERE nature = 'ASSET'), 0)
	         - COALESCE((SELECT SUM(pending) FROM upto WHERE nature = 'LIABILITY'), 0) AS closing_cash,
	       -((SELECT v FROM cash) + COALESCE((SELECT SUM(pending) FROM upto WHERE nature = 'ASSET'), 0)
	         - COALESCE((SELECT SUM(pending) FROM upto WHERE nature = 'LIABILITY'), 0)) AS shortfall,
	       ARRAY[]::uuid[] AS voucher_ids`

func (n *night) cashShortfall() ([]Candidate, error) {
	fc := forecast.NewServiceOver(txRunner{n.tx})
	p, err := fc.CashFlow(n.ctx, n.companyID, n.asOfS(), CashHorizonWeeks*7, "week")
	if err != nil {
		return nil, fmt.Errorf("insights: cash flow: %w", err)
	}
	in, out := decimal.Zero, decimal.Zero
	for _, b := range p.Buckets {
		in, out = in.Add(b.Inflow), out.Add(b.Outflow)
		if !b.ClosingCash.IsNegative() {
			continue
		}
		short := b.ClosingCash.Neg()
		return []Candidate{{
			Type: TypeCashShortfall, EntityKind: "tenant", EntityKey: "tenant", EntityName: "cash and bank",
			Fingerprint: TypeCashShortfall, Impact: short,
			Payload: map[string]string{"week_from": b.From.Format(isoDate), "week_to": b.To.Format(isoDate),
				"opening_cash": Canonical(UnitINR, p.OpeningCash), "inflow_through": Canonical(UnitINR, in),
				"outflow_through": Canonical(UnitINR, out), "closing_cash": Canonical(UnitINR, b.ClosingCash),
				"shortfall": Canonical(UnitINR, short)},
			EvidenceSQL:  evidenceCashShortfall,
			EvidenceArgs: []string{n.asOfS(), b.From.Format(isoDate), b.To.Format(isoDate)},
			// ⚠️ EMPTY BY DESIGN: the figure is every open bill of the company;
			// the drill-down is the Cash Flow Projection report at this as-of,
			// not a voucher list capped at 200 that could not reproduce it.
			VoucherIDs: []uuid.UUID{},
		}}, nil
	}
	return nil, nil
}

// ============================================================================
// GST liability for the month just closed
// ============================================================================

// From the 1st to the 20th of a month, the previous month's GSTR-3B is due
// (the 20th for a monthly filer — QRMP quarterly filers are OWED). Output tax
// is Sales less Credit Notes; ITC is Purchases less Debit Notes where the
// voucher's eligibility is ELIGIBLE (or inherited) and it is not reverse
// charge. ⚠️ Books basis: the ledger-level eligibility default and the 2B
// cap on ITC (Rule 36(4)) are not applied here — supplier_not_in_gstr2b is the
// 2B half. Silent once a GSTR-3B filing is recorded for the period.
const evidenceGSTLiability = `
	WITH v AS (
	  SELECT vv.voucher_id, COALESCE(vv.payload->>'class', '') AS cls,
	         COALESCE(vv.cgst, 0) + COALESCE(vv.sgst, 0) + COALESCE(vv.igst, 0) AS tax,
	         vv.is_reverse_charge AS rc, COALESCE(vv.itc_eligibility, 'ELIGIBLE') AS elig
	    FROM voucher_version vv` + currentJoin + `
	   WHERE vv.voucher_date >= $1::date AND vv.voucher_date < ($1::date + interval '1 month')::date),
	c AS (SELECT * FROM v WHERE tax <> 0 AND (cls IN ('Sales', 'Credit Note')
	        OR (cls IN ('Purchase', 'Debit Note') AND NOT rc AND elig = 'ELIGIBLE'))),
	t AS (SELECT COALESCE(SUM(tax) FILTER (WHERE cls = 'Sales'), 0) - COALESCE(SUM(tax) FILTER (WHERE cls = 'Credit Note'), 0) AS o,
	             COALESCE(SUM(tax) FILTER (WHERE cls = 'Purchase'), 0) - COALESCE(SUM(tax) FILTER (WHERE cls = 'Debit Note'), 0) AS i
	        FROM c)
	SELECT ($1::date + interval '1 month' + interval '19 days')::date AS due_date,
	       t.o AS output_tax, t.i AS itc_available,
	       GREATEST(t.o - t.i, 0) AS net_payable, GREATEST(t.i - t.o, 0) AS itc_carried_forward,
	       ARRAY(SELECT voucher_id FROM c ORDER BY tax DESC, voucher_id LIMIT 5000) AS voucher_ids
	  FROM t
	 WHERE NOT EXISTS (SELECT 1 FROM gst_return_filing f WHERE f.form = 'GSTR3B' AND f.return_period = $2)`

func (n *night) gstLiability() ([]Candidate, error) {
	if n.asOf.Day() > 20 {
		return nil, nil
	}
	m0 := time.Date(n.asOf.Year(), n.asOf.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, -1, 0)
	period := m0.Format("012006")
	var filed bool
	if err := n.tx.QueryRow(n.ctx, `SELECT EXISTS (SELECT 1 FROM gst_return_filing WHERE form = 'GSTR3B' AND return_period = $1)`,
		period).Scan(&filed); err != nil {
		return nil, err
	}
	if filed {
		return nil, nil
	}
	rows, err := n.tx.Query(n.ctx, `
		SELECT vv.voucher_id, COALESCE(vv.payload->>'class', ''),
		       COALESCE(vv.cgst, 0) + COALESCE(vv.sgst, 0) + COALESCE(vv.igst, 0),
		       vv.is_reverse_charge, COALESCE(vv.itc_eligibility, 'ELIGIBLE')
		  FROM voucher_version vv`+currentJoin+`
		 WHERE vv.voucher_date >= $1::date AND vv.voucher_date < ($1::date + interval '1 month')::date`, m0.Format(isoDate))
	if err != nil {
		return nil, fmt.Errorf("insights: gst month: %w", err)
	}
	type contrib struct {
		id  uuid.UUID
		tax decimal.Decimal
	}
	var (
		outTax, itcAvail = decimal.Zero, decimal.Zero
		cs               []contrib
	)
	for rows.Next() {
		var (
			id   uuid.UUID
			cls  string
			tax  decimal.Decimal
			rc   bool
			elig string
		)
		if err := rows.Scan(&id, &cls, &tax, &rc, &elig); err != nil {
			rows.Close()
			return nil, err
		}
		if tax.IsZero() {
			continue
		}
		switch {
		case cls == "Sales":
			outTax = outTax.Add(tax)
		case cls == "Credit Note":
			outTax = outTax.Sub(tax)
		case cls == "Purchase" && !rc && elig == "ELIGIBLE":
			itcAvail = itcAvail.Add(tax)
		case cls == "Debit Note" && !rc && elig == "ELIGIBLE":
			itcAvail = itcAvail.Sub(tax)
		default:
			continue
		}
		cs = append(cs, contrib{id, tax})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	net := decimal.Max(outTax.Sub(itcAvail), decimal.Zero)
	if !net.IsPositive() {
		return nil, nil
	}
	sort.Slice(cs, func(i, j int) bool {
		if !cs[i].tax.Equal(cs[j].tax) {
			return cs[i].tax.GreaterThan(cs[j].tax)
		}
		return cs[i].id.String() < cs[j].id.String()
	})
	// Every contributing voucher up to MaxVoucherIDs (was 200): the two
	// figures are sums over exactly this list (the wave-2 review).
	if len(cs) > MaxVoucherIDs {
		cs = cs[:MaxVoucherIDs]
	}
	ids := make([]uuid.UUID, len(cs))
	for i, c := range cs {
		ids[i] = c.id
	}
	due := m0.AddDate(0, 1, 19)
	return []Candidate{{
		Type: TypeGSTLiability, EntityKind: "period", EntityKey: m0.Format("2006-01"), EntityName: "GST " + m0.Format("2006-01"),
		Fingerprint: TypeGSTLiability + ":" + m0.Format("2006-01"), Impact: net,
		Payload: map[string]string{"period": m0.Format("2006-01"), "due_date": due.Format(isoDate),
			"output_tax": Canonical(UnitINR, outTax), "itc_available": Canonical(UnitINR, itcAvail),
			"net_payable":         Canonical(UnitINR, net),
			"itc_carried_forward": Canonical(UnitINR, decimal.Max(itcAvail.Sub(outTax), decimal.Zero))},
		EvidenceSQL: evidenceGSTLiability, EvidenceArgs: []string{m0.Format(isoDate), period},
		VoucherIDs: ids,
	}}, nil
}

// ============================================================================
// TDS deducted but not deposited
// ============================================================================

// Due date (Rule 30(2)): the 7th of the next month, except tax deducted in
// March, due 30 April. Unpaid = deducted − allocated to challans, per
// (voucher, section, leg). tds_deduction is re-derived on ALTER and removed
// on DELETE (ledger/tds.go), so it is already current-version only.
const evidenceTDSNotDeposited = `
	WITH d AS (
	  SELECT td.voucher_id,
	         td.tds_amount_rupees - COALESCE((SELECT SUM(a.allocated_amount_rupees) FROM tds_challan_allocation a
	                                           WHERE a.voucher_id = td.voucher_id AND a.section = td.section
	                                             AND a.leg_seq = td.leg_seq), 0) AS unpaid
	    FROM tds_deduction td
	   WHERE td.deduction_date >= $1::date AND td.deduction_date < ($1::date + interval '1 month')::date
	     AND td.tds_amount_rupees > 0),
	u AS (SELECT * FROM d WHERE unpaid > 0),
	due AS (SELECT CASE WHEN extract(month FROM $1::date) = 3
	                    THEN ($1::date + interval '1 month' + interval '29 days')::date
	                    ELSE ($1::date + interval '1 month' + interval '6 days')::date END AS dd)
	SELECT due.dd AS due_date, $2::date - due.dd AS days_late,
	       (SELECT count(*) FROM u) AS deductions,
	       (SELECT COALESCE(SUM(unpaid), 0) FROM u) AS unpaid_tds,
	       ARRAY(SELECT DISTINCT voucher_id FROM u) AS voucher_ids
	  FROM due`

func tdsDue(m0 time.Time) time.Time {
	if m0.Month() == time.March {
		return m0.AddDate(0, 1, 29)
	}
	return m0.AddDate(0, 1, 6)
}

func (n *night) tdsNotDeposited() ([]Candidate, error) {
	rows, err := n.tx.Query(n.ctx, `
		SELECT date_trunc('month', td.deduction_date)::date, td.voucher_id,
		       td.tds_amount_rupees, COALESCE((SELECT SUM(a.allocated_amount_rupees) FROM tds_challan_allocation a
		           WHERE a.voucher_id = td.voucher_id AND a.section = td.section AND a.leg_seq = td.leg_seq), 0)
		  FROM tds_deduction td
		 WHERE td.tds_amount_rupees > 0 AND td.deduction_date <= $1::date
		 ORDER BY 1, 2`, n.asOfS())
	if err != nil {
		return nil, fmt.Errorf("insights: tds: %w", err)
	}
	type acc struct {
		m0     time.Time
		unpaid decimal.Decimal
		n      int
		ids    []uuid.UUID
		seen   map[uuid.UUID]bool
	}
	var order []*acc
	by := map[time.Time]*acc{}
	for rows.Next() {
		var (
			m0        time.Time
			vid       uuid.UUID
			amt, paid decimal.Decimal
		)
		if err := rows.Scan(&m0, &vid, &amt, &paid); err != nil {
			rows.Close()
			return nil, err
		}
		un := amt.Sub(paid)
		if !un.IsPositive() {
			continue
		}
		a, ok := by[m0]
		if !ok {
			a = &acc{m0: m0, seen: map[uuid.UUID]bool{}}
			by[m0] = a
			order = append(order, a)
		}
		a.unpaid = a.unpaid.Add(un)
		a.n++
		if !a.seen[vid] {
			a.seen[vid] = true
			a.ids = append(a.ids, vid)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Candidate
	for _, a := range order {
		due := tdsDue(a.m0)
		if !n.asOf.After(due) || len(a.ids) > 200 {
			continue
		}
		out = append(out, Candidate{
			Type: TypeTDSNotDeposited, EntityKind: "period", EntityKey: a.m0.Format("2006-01"), EntityName: "TDS " + a.m0.Format("2006-01"),
			Fingerprint: TypeTDSNotDeposited + ":" + a.m0.Format("2006-01"), Impact: a.unpaid,
			Payload: map[string]string{"period": a.m0.Format("2006-01"), "due_date": due.Format(isoDate),
				"days_late": fmt.Sprint(int(n.asOf.Sub(due).Hours() / 24)), "deductions": fmt.Sprint(a.n),
				"unpaid_tds": Canonical(UnitINR, a.unpaid)},
			EvidenceSQL: evidenceTDSNotDeposited, EvidenceArgs: []string{a.m0.Format(isoDate), n.asOfS()},
			VoucherIDs: a.ids,
		})
	}
	return out, nil
}

// ============================================================================
// back-dated entries, by user
// ============================================================================

// Over the last 7 business days (IST, by when the version was WRITTEN), every
// version — create, alter or delete — whose voucher date is more than
// BackdateDays before the day it was written, grouped by who wrote it. The
// amount is the debit total of those versions (a delete tombstone carries
// none, and counts as an entry).
const evidenceBackdated = `
	WITH e AS (
	  SELECT vv.id, vv.voucher_id, vv.voucher_date FROM voucher_version vv
	   WHERE vv.created_by = $1
	     AND (vv.created_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $2::date AND $3::date
	     AND vv.voucher_date IS NOT NULL
	     AND ((vv.created_at AT TIME ZONE 'Asia/Kolkata')::date - vv.voucher_date) > $4::int)
	SELECT $2::date AS week_from, $3::date AS week_to,
	       (SELECT count(*) FROM e) AS entries,
	       (SELECT count(DISTINCT voucher_id) FROM e) AS vouchers,
	       (SELECT min(voucher_date) FROM e) AS oldest_voucher_date,
	       (SELECT COALESCE(SUM(p.amount), 0) FROM posting p
	         WHERE p.voucher_version_id IN (SELECT id FROM e) AND p.dr_cr = 'D') AS amount,
	       ARRAY(SELECT DISTINCT voucher_id FROM e) AS voucher_ids`

func (n *night) backdated() ([]Candidate, error) {
	from := n.asOf.AddDate(0, 0, -6)
	rows, err := n.tx.Query(n.ctx, `
		SELECT vv.created_by, vv.id, vv.voucher_id, vv.voucher_date,
		       (vv.created_at AT TIME ZONE 'Asia/Kolkata')::date,
		       COALESCE((SELECT SUM(p.amount) FROM posting p WHERE p.voucher_version_id = vv.id AND p.dr_cr = 'D'), 0)
		  FROM voucher_version vv
		 WHERE (vv.created_at AT TIME ZONE 'Asia/Kolkata')::date BETWEEN $1::date AND $2::date
		   AND vv.voucher_date IS NOT NULL
		 ORDER BY vv.created_by, vv.created_at, vv.id`, from.Format(isoDate), n.asOfS())
	if err != nil {
		return nil, fmt.Errorf("insights: backdated: %w", err)
	}
	type acc struct {
		user    string
		entries int
		ids     []uuid.UUID
		seen    map[uuid.UUID]bool
		oldest  time.Time
		amount  decimal.Decimal
	}
	var order []*acc
	by := map[string]*acc{}
	for rows.Next() {
		var (
			user         string
			ver, vid     uuid.UUID
			vdate, wrote time.Time
			amt          decimal.Decimal
		)
		if err := rows.Scan(&user, &ver, &vid, &vdate, &wrote, &amt); err != nil {
			rows.Close()
			return nil, err
		}
		if int(wrote.Sub(vdate).Hours()/24) <= BackdateDays {
			continue
		}
		a, ok := by[user]
		if !ok {
			a = &acc{user: user, seen: map[uuid.UUID]bool{}, oldest: vdate}
			by[user] = a
			order = append(order, a)
		}
		a.entries++
		a.amount = a.amount.Add(amt)
		if vdate.Before(a.oldest) {
			a.oldest = vdate
		}
		if !a.seen[vid] {
			a.seen[vid] = true
			a.ids = append(a.ids, vid)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Candidate
	for _, a := range order {
		if len(a.ids) > 200 {
			continue
		}
		out = append(out, Candidate{
			Type: TypeBackdatedEdits, EntityKind: "user", EntityKey: a.user, EntityName: a.user,
			Fingerprint: TypeBackdatedEdits + ":" + a.user, Impact: a.amount,
			Payload: map[string]string{"user": a.user, "week_from": from.Format(isoDate), "week_to": n.asOfS(),
				"entries": fmt.Sprint(a.entries), "vouchers": fmt.Sprint(len(a.ids)),
				"oldest_voucher_date": a.oldest.Format(isoDate), "amount": Canonical(UnitINR, a.amount)},
			EvidenceSQL:  evidenceBackdated,
			EvidenceArgs: []string{a.user, from.Format(isoDate), n.asOfS(), fmt.Sprint(BackdateDays)},
			VoucherIDs:   a.ids,
		})
	}
	return out, nil
}
