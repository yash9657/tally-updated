package profile

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
	"github.com/khata/cored/internal/reporting"
)

// jobs.go runs the deterministic feature jobs.
//
// ⛔ ONE TRANSACTION, ONE LOCK, ALL JOBS. Refresh reads every report and every
// raw row through a runner that REUSES its own transaction (txRunner below),
// under a per-company advisory lock. Two reasons, both measured elsewhere in
// this repo rather than assumed:
//
//   - d120's race: two concurrent pollers executed a step twice while the
//     store recorded it once. Two concurrent refreshes here would each
//     supersede the other's rows and one set would be lost, or — with the
//     one-current unique index — one would fail half-way. The lock
//     (pg_advisory_xact_lock on the company) serialises them in the database.
//   - A fact set read across several transactions can describe no single
//     moment of the books. Inside one READ COMMITTED transaction a voucher
//     posted mid-refresh can still be seen by a later report and not an
//     earlier one — that residual is stated in d122 §6, and the nightly
//     recompute is what bounds it; the reconciliation gate is asserted on a
//     quiescent book.

// Runner is the database capability Refresh needs (*db.Pool satisfies it).
type Runner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// txRunner hands reporting.Service the transaction Refresh already holds, so
// every report is read in the same transaction as the facts are written.
type txRunner struct{ tx pgx.Tx }

func (r txRunner) RunInTx(ctx context.Context, fn db.TxFunc) error { return fn(ctx, r.tx) }

// Options parameterise a refresh.
type Options struct {
	// AsOf is the business date the facts describe (YYYY-MM-DD). Required:
	// a job never defaults to "today", because the server's today is UTC and
	// the books' today is IST, and a fact dated by the wrong one is off by a
	// day for five and a half hours of every twenty-four (query/whitelist.go
	// records the same trap for outstandings).
	AsOf string
	// WindowDays is the evidence window ending at AsOf. Default 365.
	WindowDays int
}

// Result summarises what a refresh wrote.
type Result struct {
	AsOf           string
	WindowFrom     string
	Facts          int
	Edges          int
	ByJob          map[string]int
	ProfileVersion int
	ProfileTokens  int
}

// ErrNoAsOf is returned when Options.AsOf is empty or malformed.
var ErrNoAsOf = errors.New("profile: an as_of date (YYYY-MM-DD) is required")

// Refresh recomputes every job for the company in ctx and writes the facts,
// the edges and a new tenant_profile version.
func Refresh(ctx context.Context, runner Runner, companyID uuid.UUID, opt Options) (Result, error) {
	asOf, err := time.Parse(isoDate, opt.AsOf)
	if err != nil {
		return Result{}, ErrNoAsOf
	}
	days := opt.WindowDays
	if days <= 0 {
		days = 365
	}
	windowFrom := asOf.AddDate(0, 0, -(days - 1))
	res := Result{AsOf: opt.AsOf, WindowFrom: windowFrom.Format(isoDate), ByJob: map[string]int{}}

	err = runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('profile.refresh:' || $1::text))`, companyID); err != nil {
			return fmt.Errorf("profile: lock: %w", err)
		}
		j := &jobRun{
			ctx: ctx, tx: tx, companyID: companyID, asOf: asOf, from: windowFrom, days: days,
			rep: reporting.NewService(txRunner{tx}),
		}
		var facts []Fact
		var edges []Edge
		for _, step := range []func() ([]Fact, []Edge, error){
			j.customers, j.suppliers, j.items, j.streams, j.tenant,
		} {
			fs, es, err := step()
			if err != nil {
				return err
			}
			facts = append(facts, fs...)
			edges = append(edges, es...)
		}
		jobs := []string{JobCustomer, JobSupplier, JobItem, JobStream, JobTenant}
		if err := writeFacts(ctx, tx, companyID, asOf, windowFrom, jobs, facts); err != nil {
			return err
		}
		if err := writeEdges(ctx, tx, companyID, asOf, windowFrom, JobSupplier, edges); err != nil {
			return err
		}
		for _, fc := range facts {
			d, _ := Def(fc.Key)
			res.ByJob[d.Job]++
		}
		res.Facts, res.Edges = len(facts), len(edges)
		v, tokens, err := writeTenantProfile(ctx, tx, companyID, asOf, facts)
		if err != nil {
			return err
		}
		res.ProfileVersion, res.ProfileTokens = v, tokens
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

type jobRun struct {
	ctx       context.Context
	tx        pgx.Tx
	companyID uuid.UUID
	asOf      time.Time
	from      time.Time
	days      int
	rep       *reporting.Service
}

func (j *jobRun) asOfS() string { return j.asOf.Format(isoDate) }
func (j *jobRun) fromS() string { return j.from.Format(isoDate) }

type party struct {
	id   uuid.UUID
	name string
}

// partiesUnder lists ledgers in a group SUBTREE (a party filed under a
// sub-group of Sundry Debtors is still a debtor — the plRootGroupExpr lesson).
func (j *jobRun) partiesUnder(root string) ([]party, error) {
	rows, err := j.tx.Query(j.ctx, `
		SELECT l.id, l.name FROM ledger l JOIN account_group g ON g.id = l.group_id
		 WHERE EXISTS (SELECT 1 FROM account_group r WHERE r.name = $1 AND g.path <@ r.path)
		 ORDER BY l.name, l.id`, root)
	if err != nil {
		return nil, fmt.Errorf("profile: parties under %s: %w", root, err)
	}
	defer rows.Close()
	var out []party
	for rows.Next() {
		var p party
		if err := rows.Scan(&p.id, &p.name); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// currentVoucherJoin is the current-version / not-deleted guard every raw
// read here carries — daybook.go's, verbatim.
const currentVoucherJoin = `
	JOIN voucher_head vh
	  ON vh.company_id = vv.company_id
	 AND vh.voucher_id = vv.voucher_id
	 AND vh.current_version_id = vv.id`

// partyLegTotals sums, per party, the posting legs on the party's own ledger
// of vouchers of one payload class inside the window, on one side.
func (j *jobRun) partyLegTotals(class, side string, ids []uuid.UUID) (map[uuid.UUID]decimal.Decimal, error) {
	rows, err := j.tx.Query(j.ctx, `
		SELECT p.ledger_id, COALESCE(SUM(p.amount), 0)
		  FROM posting p
		  JOIN voucher_version vv ON vv.id = p.voucher_version_id`+currentVoucherJoin+`
		 WHERE vh.is_deleted = false
		   AND COALESCE(vv.payload->>'class', '') = $1
		   AND p.dr_cr::text = $2
		   AND p.ledger_id = ANY($3)
		   AND vv.voucher_date >= $4::date AND vv.voucher_date <= $5::date
		 GROUP BY p.ledger_id`, class, side, ids, j.from, j.asOf)
	if err != nil {
		return nil, fmt.Errorf("profile: party totals: %w", err)
	}
	defer rows.Close()
	out := map[uuid.UUID]decimal.Decimal{}
	for rows.Next() {
		var id uuid.UUID
		var d decimal.Decimal
		if err := rows.Scan(&id, &d); err != nil {
			return nil, err
		}
		out[id] = d
	}
	return out, rows.Err()
}

// bills loads bill-wise references for the given ledgers: bills RAISED in the
// window on `raiseSide`, with every AGAINST settlement up to as-of on the
// other side. Opening bills (ledger_opening_bill) have no raise date and are
// deliberately out of the lag statistics — a lag measured from the books'
// first day would be the age of the books, not the customer's habit.
func (j *jobRun) bills(ids []uuid.UUID, raiseSide string) (map[uuid.UUID][]bill, error) {
	rows, err := j.tx.Query(j.ctx, `
		SELECT b.ledger_id, b.ref_name, b.ref_type::text, b.dr_cr::text, b.amount,
		       vv.voucher_date, vv.voucher_id
		  FROM bill_allocation b
		  JOIN voucher_version vv ON vv.id = b.voucher_version_id`+currentVoucherJoin+`
		 WHERE vh.is_deleted = false
		   AND b.ledger_id = ANY($1)
		   AND vv.voucher_date <= $2::date
		   AND b.ref_type::text IN ('NEW', 'AGAINST')
		 ORDER BY b.ledger_id, b.ref_name, vv.voucher_date, vv.voucher_id`, ids, j.asOf)
	if err != nil {
		return nil, fmt.Errorf("profile: bills: %w", err)
	}
	defer rows.Close()
	type key struct {
		ledger uuid.UUID
		ref    string
	}
	raised := map[key]*bill{}
	var order []key
	type pend struct {
		k key
		s settlement
	}
	var settles []pend
	for rows.Next() {
		var (
			ledgerID  uuid.UUID
			ref, rt   string
			side      string
			amt       decimal.Decimal
			on        time.Time
			voucherID uuid.UUID
		)
		if err := rows.Scan(&ledgerID, &ref, &rt, &side, &amt, &on, &voucherID); err != nil {
			return nil, err
		}
		k := key{ledgerID, ref}
		switch {
		case rt == "NEW" && side == raiseSide:
			if on.Before(j.from) {
				continue
			}
			if b, ok := raised[k]; ok {
				// Two NEW refs of one name (a data-entry reuse): the bill is the
				// sum, raised on the earlier date.
				b.amount = b.amount.Add(amt)
				continue
			}
			raised[k] = &bill{raisedOn: on, amount: amt}
			order = append(order, k)
		case rt == "AGAINST" && side != raiseSide:
			settles = append(settles, pend{k, settlement{on: on, amount: amt, voucherID: voucherID.String()}})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, p := range settles {
		if b, ok := raised[p.k]; ok {
			b.settlements = append(b.settlements, p.s)
		}
	}
	out := map[uuid.UUID][]bill{}
	for _, k := range order {
		out[k.ledger] = append(out[k.ledger], *raised[k])
	}
	return out, nil
}

// lagFacts turns a party's bills into the p50/p90/mean/count facts, keeping
// as evidence the settling vouchers of the SLOWEST bills (the ones that moved
// the p90).
func lagFacts(p party, bs []bill, prefix string, withCount bool) []Fact {
	type lag struct {
		days int
		vid  string
	}
	var lags []lag
	for _, b := range bs {
		if d, vid, ok := settledLag(b); ok {
			lags = append(lags, lag{d, vid})
		}
	}
	if len(lags) == 0 {
		return nil
	}
	sort.SliceStable(lags, func(i, k int) bool {
		if lags[i].days != lags[k].days {
			return lags[i].days > lags[k].days
		}
		return lags[i].vid < lags[k].vid
	})
	var ev []uuid.UUID
	seen := map[string]bool{}
	for _, l := range lags {
		if len(ev) == maxEvidence {
			break
		}
		if !seen[l.vid] {
			seen[l.vid] = true
			ev = append(ev, uuid.MustParse(l.vid))
		}
	}
	ds := make([]int, len(lags))
	for i, l := range lags {
		ds[i] = l.days
	}
	sort.Ints(ds)
	mk := func(key string, v decimal.Decimal) Fact {
		return Fact{Kind: KindParty, EntityKey: p.id.String(), EntityID: p.id, EntityName: p.name, Key: key,
			Num: num(v), EvidenceKind: "voucher", Evidence: ev}
	}
	out := []Fact{
		mk(prefix+"_lag_p50_days", decimal.NewFromInt(int64(percentile(ds, 50)))),
		mk(prefix+"_lag_p90_days", decimal.NewFromInt(int64(percentile(ds, 90)))),
	}
	if withCount {
		out = append(out, mk(prefix+"_lag_mean_days", meanDays(ds)),
			mk("bills_settled_count", decimal.NewFromInt(int64(len(ds)))))
	}
	return out
}

// outstandingByParty is reporting.Outstandings as at as-of, keyed by ledger.
func (j *jobRun) outstandingByParty(kind reporting.OutstandingKind) (map[uuid.UUID]decimal.Decimal, decimal.Decimal, error) {
	o, err := j.rep.Outstandings(j.ctx, j.companyID, kind, j.asOfS(), nil)
	if err != nil {
		return nil, decimal.Zero, fmt.Errorf("profile: outstandings (%s): %w", kind, err)
	}
	m := map[uuid.UUID]decimal.Decimal{}
	for _, p := range o.Parties {
		m[p.LedgerID] = m[p.LedgerID].Add(p.Pending)
	}
	return m, o.Total, nil
}

func (j *jobRun) customers() ([]Fact, []Edge, error) {
	ps, err := j.partiesUnder("Sundry Debtors")
	if err != nil || len(ps) == 0 {
		return nil, nil, err
	}
	ids := make([]uuid.UUID, len(ps))
	for i, p := range ps {
		ids[i] = p.id
	}
	pending, _, err := j.outstandingByParty(reporting.Receivables)
	if err != nil {
		return nil, nil, err
	}
	sales, err := j.partyLegTotals("Sales", "D", ids)
	if err != nil {
		return nil, nil, err
	}
	bills, err := j.bills(ids, "D")
	if err != nil {
		return nil, nil, err
	}
	var out []Fact
	for _, p := range ps {
		recv, hasRecv := pending[p.id]
		sold, hasSales := sales[p.id]
		if !hasRecv && !hasSales && len(bills[p.id]) == 0 {
			continue // a debtor with no activity and nothing owed says nothing
		}
		base := Fact{Kind: KindParty, EntityKey: p.id.String(), EntityID: p.id, EntityName: p.name}
		f := base
		f.Key, f.Num = "receivable_outstanding", num(recv)
		out = append(out, f)
		f = base
		f.Key, f.Num = "credit_sales_window", num(sold)
		out = append(out, f)
		// DSO = receivable / credit sales x window days. Omitted with no
		// sales: "infinite days" is not a number, and 0 would be a lie.
		if sold.IsPositive() {
			f = base
			f.Key = "dso_days"
			f.Num = num(recv.Mul(decimal.NewFromInt(int64(j.days))).DivRound(sold, 1))
			out = append(out, f)
		}
		out = append(out, lagFacts(p, bills[p.id], "receipt", true)...)
	}
	return out, nil, nil
}

func (j *jobRun) suppliers() ([]Fact, []Edge, error) {
	ps, err := j.partiesUnder("Sundry Creditors")
	if err != nil || len(ps) == 0 {
		return nil, nil, err
	}
	ids := make([]uuid.UUID, len(ps))
	for i, p := range ps {
		ids[i] = p.id
	}
	pending, _, err := j.outstandingByParty(reporting.Payables)
	if err != nil {
		return nil, nil, err
	}
	bought, err := j.partyLegTotals("Purchase", "C", ids)
	if err != nil {
		return nil, nil, err
	}
	bills, err := j.bills(ids, "C")
	if err != nil {
		return nil, nil, err
	}
	drift, edges, err := j.priceDrift(ids)
	if err != nil {
		return nil, nil, err
	}
	lead, err := j.leadTimes(ids)
	if err != nil {
		return nil, nil, err
	}
	var out []Fact
	for _, p := range ps {
		owe, hasOwe := pending[p.id]
		b, hasBuy := bought[p.id]
		if !hasOwe && !hasBuy && len(bills[p.id]) == 0 && len(drift[p.id]) == 0 && len(lead[p.id]) == 0 {
			continue
		}
		out = append(out, leadTimeFacts(p, lead[p.id])...)
		base := Fact{Kind: KindParty, EntityKey: p.id.String(), EntityID: p.id, EntityName: p.name}
		f := base
		f.Key, f.Num = "payable_outstanding", num(owe)
		out = append(out, f)
		f = base
		f.Key, f.Num = "purchases_window", num(b)
		out = append(out, f)
		out = append(out, lagFacts(p, bills[p.id], "payment", false)...)
		for _, d := range drift[p.id] {
			d.EntityName = p.name
			out = append(out, d)
		}
	}
	return out, edges, nil
}

// leadLine is one purchase order line a supplier COMPLETED in the window.
type leadLine struct {
	days int
	vid  uuid.UUID // the voucher whose receipt completed the line
}

// leadTimes is W3's owed SUPPLIER LEAD TIME (d122 §6 item 6), now that order
// lines link to the receipts that fulfil them (migration 0042's
// order_fulfilment): per supplier, the days from a PURCHASE order line's
// order date to the receipt that COMPLETED it.
//
// ⛔ COMPLETION, NOT FIRST RECEIPT — the same rule the receipt lag uses ("the
// lag ends at the settlement that FULLY settles a bill"). A supplier who
// ships 10 of 100 on day 2 and the other 90 on day 60 has a 60-day lead time
// for that line; measuring the first drop would make a trickle-shipper look
// like the fastest supplier on the books. A line never completed has no lead
// time yet and is left out (it is the order book's business, not this
// statistic's).
//
// ⛔ THE FULFILMENT SUM JOINS voucher_head.current_version_id AND DROPS
// DELETED VOUCHERS, as reporting/orderbook.go's does and for its reason:
// fulfilments are versioned with the voucher, and an altered receipt left in
// the table under its superseded version would complete a line twice as
// early as it was.
//
// A statistic no report prints, so it is derived here (D122-6 allows exactly
// that); the window bounds the COMPLETING receipt's date, and receipts after
// as-of are invisible, so a refresh "as of July" cannot see an August
// delivery.
func (j *jobRun) leadTimes(ids []uuid.UUID) (map[uuid.UUID][]leadLine, error) {
	rows, err := j.tx.Query(j.ctx, `
		SELECT oh.party_ledger_id, ol.id, oh.order_date, ol.qty, vv.voucher_date, vv.voucher_id, f.qty
		  FROM order_line ol
		  JOIN order_head oh ON oh.company_id = ol.company_id AND oh.id = ol.order_id
		  JOIN order_fulfilment f ON f.company_id = ol.company_id AND f.order_line_id = ol.id
		  JOIN voucher_version vv ON vv.id = f.voucher_version_id`+currentVoucherJoin+`
		 WHERE vh.is_deleted = false
		   AND oh.direction = 'PURCHASE'
		   AND oh.party_ledger_id = ANY($1)
		   AND vv.voucher_date <= $2::date
		 ORDER BY ol.id, vv.voucher_date, vv.voucher_id`, ids, j.asOfS())
	if err != nil {
		return nil, fmt.Errorf("profile: lead times: %w", err)
	}
	defer rows.Close()
	type acc struct {
		party     uuid.UUID
		orderDate time.Time
		qty, got  decimal.Decimal
		done      bool
	}
	lines := map[uuid.UUID]*acc{}
	out := map[uuid.UUID][]leadLine{}
	for rows.Next() {
		var party, line, vid uuid.UUID
		var orderDate, vdate time.Time
		var qty, fq decimal.Decimal
		if err := rows.Scan(&party, &line, &orderDate, &qty, &vdate, &vid, &fq); err != nil {
			return nil, err
		}
		a := lines[line]
		if a == nil {
			a = &acc{party: party, orderDate: orderDate, qty: qty}
			lines[line] = a
		}
		if a.done {
			continue
		}
		a.got = a.got.Add(fq)
		if a.got.GreaterThanOrEqual(a.qty) {
			a.done = true
			if !vdate.Before(j.from) {
				out[party] = append(out[party], leadLine{days: int(vdate.Sub(a.orderDate).Hours() / 24), vid: vid})
			}
		}
	}
	return out, rows.Err()
}

// leadTimeFacts turns completed lines into p50/p90 and a count, keeping the
// SLOWEST completions as evidence (they are what moved the p90).
func leadTimeFacts(p party, ls []leadLine) []Fact {
	if len(ls) == 0 {
		return nil
	}
	slow := append([]leadLine(nil), ls...)
	sort.SliceStable(slow, func(i, k int) bool {
		if slow[i].days != slow[k].days {
			return slow[i].days > slow[k].days
		}
		return slow[i].vid.String() < slow[k].vid.String()
	})
	var ev []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, l := range slow {
		if len(ev) == maxEvidence {
			break
		}
		if !seen[l.vid] {
			seen[l.vid] = true
			ev = append(ev, l.vid)
		}
	}
	ds := make([]int, len(ls))
	for i, l := range ls {
		ds[i] = l.days
	}
	sort.Ints(ds)
	mk := func(key string, v decimal.Decimal) Fact {
		return Fact{Kind: KindParty, EntityKey: p.id.String(), EntityID: p.id, EntityName: p.name, Key: key,
			Num: num(v), EvidenceKind: "voucher", Evidence: ev}
	}
	return []Fact{
		mk("lead_time_p50_days", decimal.NewFromInt(int64(percentile(ds, 50)))),
		mk("lead_time_p90_days", decimal.NewFromInt(int64(percentile(ds, 90)))),
		mk("lead_time_lines_count", decimal.NewFromInt(int64(len(ds)))),
	}
}

// priceDrift reads every inward line of Purchase-class vouchers in the window
// with the supplier as party, per (supplier, item): first rate, latest rate,
// the % change, and the amount as entered. Also emits supplies_item edges.
func (j *jobRun) priceDrift(ids []uuid.UUID) (map[uuid.UUID][]Fact, []Edge, error) {
	rows, err := j.tx.Query(j.ctx, `
		SELECT vv.party_ledger_id, sm.stock_item_id, si.name, sm.rate, sm.qty, sm.amount,
		       vv.voucher_date, vv.voucher_id
		  FROM stock_movement sm
		  JOIN voucher_version vv ON vv.id = sm.voucher_version_id`+currentVoucherJoin+`
		  JOIN stock_item si ON si.id = sm.stock_item_id
		 WHERE vh.is_deleted = false
		   AND sm.direction = 'IN'
		   AND COALESCE(vv.payload->>'class', '') = 'Purchase'
		   AND vv.party_ledger_id = ANY($1)
		   AND vv.voucher_date >= $2::date AND vv.voucher_date <= $3::date
		 ORDER BY vv.party_ledger_id, sm.stock_item_id, vv.voucher_date, vv.created_at, sm.line_no`, ids, j.from, j.asOf)
	if err != nil {
		return nil, nil, fmt.Errorf("profile: price drift: %w", err)
	}
	defer rows.Close()
	type acc struct {
		supplier, item uuid.UUID
		itemName       string
		first, last    decimal.Decimal
		amount, qty    decimal.Decimal
		ev             []uuid.UUID
	}
	var accs []*acc
	idx := map[[2]uuid.UUID]*acc{}
	for rows.Next() {
		var (
			sup, item      uuid.UUID
			name           string
			rate, qty, amt decimal.Decimal
			on             time.Time
			vid            uuid.UUID
		)
		if err := rows.Scan(&sup, &item, &name, &rate, &qty, &amt, &on, &vid); err != nil {
			return nil, nil, err
		}
		k := [2]uuid.UUID{sup, item}
		a, ok := idx[k]
		if !ok {
			a = &acc{supplier: sup, item: item, itemName: name, first: rate}
			idx[k] = a
			accs = append(accs, a)
		}
		a.last = rate
		a.amount = a.amount.Add(amt)
		a.qty = a.qty.Add(qty)
		if len(a.ev) < maxEvidence && (len(a.ev) == 0 || a.ev[len(a.ev)-1] != vid) {
			a.ev = append(a.ev, vid)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	out := map[uuid.UUID][]Fact{}
	var edges []Edge
	for _, a := range accs {
		base := Fact{Kind: KindParty, EntityKey: a.supplier.String(), EntityID: a.supplier,
			QualifierKey: a.item.String(), QualifierName: a.itemName, EvidenceKind: "voucher", Evidence: a.ev}
		mk := func(key string, v decimal.Decimal) Fact { f := base; f.Key, f.Num = key, num(v); return f }
		fs := []Fact{
			mk("item_purchase_amount", a.amount),
			mk("item_first_rate", a.first),
			mk("item_last_rate", a.last),
		}
		if a.first.IsPositive() {
			fs = append(fs, mk("item_price_drift_pct",
				a.last.Sub(a.first).Mul(decimal.NewFromInt(100)).DivRound(a.first, 2)))
		}
		out[a.supplier] = append(out[a.supplier], fs...)
		edges = append(edges, Edge{SrcKind: KindParty, SrcKey: a.supplier.String(), DstKind: KindItem,
			DstKey: a.item.String(), DstName: a.itemName, EdgeKind: "supplies_item", Weight: a.qty, Evidence: a.ev})
	}
	// Edge source names are filled from the party list by the caller's facts;
	// read them here so an edge is readable on its own.
	names, err := j.ledgerNames(ids)
	if err != nil {
		return nil, nil, err
	}
	for i := range edges {
		edges[i].SrcName = names[uuid.MustParse(edges[i].SrcKey)]
	}
	return out, edges, nil
}

func (j *jobRun) ledgerNames(ids []uuid.UUID) (map[uuid.UUID]string, error) {
	rows, err := j.tx.Query(j.ctx, `SELECT id, name FROM ledger WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]string{}
	for rows.Next() {
		var id uuid.UUID
		var n string
		if err := rows.Scan(&id, &n); err != nil {
			return nil, err
		}
		out[id] = n
	}
	return out, rows.Err()
}

// items: closing position from the Stock Summary, window movement from the
// Stock Movement report (both TAKEN, not re-derived), then the statistics.
func (j *jobRun) items() ([]Fact, []Edge, error) {
	ss, err := j.rep.StockSummary(j.ctx, j.companyID, j.asOfS())
	if err != nil {
		return nil, nil, fmt.Errorf("profile: stock summary: %w", err)
	}
	mv, err := j.rep.StockMovementReport(j.ctx, j.companyID, j.fromS(), j.asOfS())
	if err != nil {
		return nil, nil, fmt.Errorf("profile: stock movement: %w", err)
	}
	type item struct {
		id                 uuid.UUID
		name               string
		closeQty, closeVal decimal.Decimal
		inQty, outQty      decimal.Decimal
	}
	byID := map[uuid.UUID]*item{}
	var order []uuid.UUID
	get := func(id uuid.UUID, name string) *item {
		if it, ok := byID[id]; ok {
			return it
		}
		it := &item{id: id, name: name}
		byID[id] = it
		order = append(order, id)
		return it
	}
	for _, r := range ss.Items {
		it := get(r.StockItemID, r.ItemName)
		it.closeQty, it.closeVal = r.ClosingQty, r.ClosingValue
	}
	for _, r := range mv.Items {
		it := get(r.StockItemID, r.ItemName)
		it.inQty, it.outQty = r.InwardQty, r.OutwardQty
	}
	if len(order) == 0 {
		return nil, nil, nil
	}

	// Raw: outward amount as entered, and monthly outward qty, per item.
	outAmt := map[uuid.UUID]decimal.Decimal{}
	monthly := map[uuid.UUID]map[string]decimal.Decimal{}
	rows, err := j.tx.Query(j.ctx, `
		SELECT sm.stock_item_id, to_char(vv.voucher_date, 'YYYY-MM'), SUM(sm.qty), SUM(sm.amount)
		  FROM stock_movement sm
		  JOIN voucher_version vv ON vv.id = sm.voucher_version_id`+currentVoucherJoin+`
		 WHERE vh.is_deleted = false AND sm.direction = 'OUT'
		   AND vv.voucher_date >= $1::date AND vv.voucher_date <= $2::date
		 GROUP BY 1, 2`, j.from, j.asOf)
	if err != nil {
		return nil, nil, fmt.Errorf("profile: item months: %w", err)
	}
	for rows.Next() {
		var id uuid.UUID
		var month string
		var q, a decimal.Decimal
		if err := rows.Scan(&id, &month, &q, &a); err != nil {
			rows.Close()
			return nil, nil, err
		}
		outAmt[id] = outAmt[id].Add(a)
		if monthly[id] == nil {
			monthly[id] = map[string]decimal.Decimal{}
		}
		monthly[id][month] = q
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	months := monthsBetween(j.from, j.asOf)

	var abcIn []abcItem
	for _, id := range order {
		abcIn = append(abcIn, abcItem{key: id.String(), value: outAmt[id]})
	}
	abc := abcClasses(abcIn)

	var out []Fact
	for _, id := range order {
		it := byID[id]
		base := Fact{Kind: KindItem, EntityKey: id.String(), EntityID: id, EntityName: it.name}
		mk := func(key string, v decimal.Decimal) Fact { f := base; f.Key, f.Num = key, num(v); return f }
		cls := func(key, v string) Fact { f := base; f.Key, f.Text = key, v; return f }
		out = append(out,
			mk("closing_qty", it.closeQty),
			mk("closing_value", it.closeVal),
			mk("in_qty_window", it.inQty),
			mk("out_qty_window", it.outQty),
			mk("out_amount_window", outAmt[id]),
			cls("abc_class", abc[id.String()]),
		)
		vel := it.outQty.DivRound(decimal.NewFromInt(int64(j.days)), 4)
		out = append(out, mk("velocity_qty_per_day", vel))
		if vel.IsPositive() {
			out = append(out, mk("days_of_cover", it.closeQty.DivRound(vel, 1)))
		}
		series := make([]decimal.Decimal, len(months))
		for i, mo := range months {
			series[i] = monthly[id][mo]
		}
		if cv, ok := coefficientOfVariation(series); ok {
			out = append(out, mk("monthly_out_cv", cv), cls("xyz_class", xyzClass(cv)))
		}
	}
	return out, nil, nil
}

// monthsBetween lists YYYY-MM from the month of a to the month of b.
func monthsBetween(a, b time.Time) []string {
	var out []string
	cur := time.Date(a.Year(), a.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(b.Year(), b.Month(), 1, 0, 0, 0, 0, time.UTC)
	for !cur.After(end) {
		out = append(out, cur.Format("2006-01"))
		cur = cur.AddDate(0, 1, 0)
	}
	return out
}

// maxStreamSeries caps a candidate stream at its latest 50 occurrences —
// the evidence cap — which also bounds detectStream's search.
const maxStreamSeries = maxEvidence

// streams: recurring PAYMENTS (the debit legs of Payment vouchers) and
// RECEIPTS (the credit legs of Receipt vouchers), per counter-ledger.
func (j *jobRun) streams() ([]Fact, []Edge, error) {
	var out []Fact
	for _, spec := range []struct{ class, side, prefix, label string }{
		{"Payment", "D", "payment", "paid"},
		{"Receipt", "C", "receipt", "received"},
	} {
		rows, err := j.tx.Query(j.ctx, `
			SELECT p.ledger_id, l.name, vv.voucher_date, p.amount, vv.voucher_id
			  FROM posting p
			  JOIN voucher_version vv ON vv.id = p.voucher_version_id`+currentVoucherJoin+`
			  JOIN ledger l ON l.id = p.ledger_id
			 WHERE vh.is_deleted = false
			   AND COALESCE(vv.payload->>'class', '') = $1
			   AND p.dr_cr::text = $2
			   AND vv.voucher_date >= $3::date AND vv.voucher_date <= $4::date
			 ORDER BY p.ledger_id, vv.voucher_date, vv.voucher_id`, spec.class, spec.side, j.from, j.asOf)
		if err != nil {
			return nil, nil, fmt.Errorf("profile: streams: %w", err)
		}
		series := map[uuid.UUID][]occurrence{}
		names := map[uuid.UUID]string{}
		var order []uuid.UUID
		for rows.Next() {
			var id, vid uuid.UUID
			var name string
			var on time.Time
			var amt decimal.Decimal
			if err := rows.Scan(&id, &name, &on, &amt, &vid); err != nil {
				rows.Close()
				return nil, nil, err
			}
			if _, ok := series[id]; !ok {
				order = append(order, id)
				names[id] = name
			}
			series[id] = append(series[id], occurrence{on: on, amount: amt, voucherID: vid.String()})
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
		for _, id := range order {
			s := series[id]
			if len(s) > maxStreamSeries {
				s = s[len(s)-maxStreamSeries:]
			}
			ds, ok := detectStream(s)
			if !ok {
				continue
			}
			key := spec.prefix + ":" + id.String()
			var ev []uuid.UUID
			for _, o := range ds.occurrences {
				ev = append(ev, uuid.MustParse(o.voucherID))
			}
			base := Fact{Kind: KindStream, EntityKey: key, EntityName: names[id] + " (" + spec.label + ")",
				QualifierKey: id.String(), QualifierName: names[id], EvidenceKind: "voucher", Evidence: ev}
			mk := func(k string, v decimal.Decimal) Fact { f := base; f.Key, f.Num = k, num(v); return f }
			last := ds.occurrences[len(ds.occurrences)-1].on
			next := last.AddDate(0, 0, ds.intervalDays)
			dueIn := int(next.Sub(j.asOf).Hours() / 24)
			cad := base
			cad.Key, cad.Text = "stream_cadence", ds.cadence
			out = append(out,
				mk("stream_total_window", ds.total),
				mk("stream_median_amount", ds.medianAmount),
				mk("stream_interval_days", decimal.NewFromInt(int64(ds.intervalDays))),
				mk("stream_occurrences", decimal.NewFromInt(int64(len(ds.occurrences)))),
				mk("stream_next_due_in_days", decimal.NewFromInt(int64(dueIn))),
				cad,
			)
		}
	}
	return out, nil, nil
}

// tenant: monthly sales/purchases FROM THE PROFIT & LOSS, one report per
// month, and the run-rates and seasonality derived from those facts.
func (j *jobRun) tenant() ([]Fact, []Edge, error) {
	base := Fact{Kind: KindTenant, EntityKey: "tenant", EntityName: "company"}
	mk := func(key, qk string, v decimal.Decimal) Fact {
		f := base
		f.Key, f.QualifierKey, f.QualifierName, f.Num = key, qk, qk, num(v)
		return f
	}
	var out []Fact
	var sales []decimal.Decimal
	months := monthsBetween(j.from, j.asOf)
	for _, mo := range months {
		start, _ := time.Parse("2006-01", mo)
		end := start.AddDate(0, 1, -1)
		if start.Before(j.from) {
			start = j.from
		}
		if end.After(j.asOf) {
			end = j.asOf
		}
		pl, err := j.rep.ProfitAndLoss(j.ctx, j.companyID, start.Format(isoDate), end.Format(isoDate))
		if err != nil {
			return nil, nil, fmt.Errorf("profile: P&L %s: %w", mo, err)
		}
		s := SectionNode(pl.Income, "Sales Accounts")
		p := SectionNode(pl.Expenses, "Purchase Accounts")
		sales = append(sales, s)
		out = append(out, mk("sales_month", mo, s), mk("purchases_month", mo, p))
	}
	// Run-rates over COMPLETE months only: the as-of month counts only when
	// as-of is its last day, or a mid-month run-rate reads a half month as a
	// collapse in sales.
	complete := sales
	if j.asOf.AddDate(0, 0, 1).Day() != 1 && len(complete) > 0 {
		complete = complete[:len(complete)-1]
	}
	// ...and the first month counts only when the window starts on its 1st.
	if j.from.Day() != 1 && len(complete) > 0 {
		complete = complete[1:]
	}
	mean := func(xs []decimal.Decimal) decimal.Decimal {
		t := decimal.Zero
		for _, x := range xs {
			t = t.Add(x)
		}
		return t.DivRound(decimal.NewFromInt(int64(len(xs))), 2)
	}
	if n := len(complete); n >= 3 {
		out = append(out, mk("sales_run_rate_3m", "", mean(complete[n-3:])))
	}
	if n := len(complete); n >= 12 {
		last12 := complete[n-12:]
		m12 := mean(last12)
		out = append(out, mk("sales_run_rate_12m", "", m12))
		if m12.IsPositive() {
			// Index per CALENDAR month from the last 12 complete months.
			fullMonths := months
			if j.asOf.AddDate(0, 0, 1).Day() != 1 {
				fullMonths = fullMonths[:len(fullMonths)-1]
			}
			if j.from.Day() != 1 {
				fullMonths = fullMonths[1:]
			}
			fullMonths = fullMonths[len(fullMonths)-12:]
			for i, mo := range fullMonths {
				out = append(out, mk("sales_seasonality_index", mo[5:], last12[i].DivRound(m12, 4)))
			}
		}
	}
	_, recv, err := j.outstandingByParty(reporting.Receivables)
	if err != nil {
		return nil, nil, err
	}
	_, pay, err := j.outstandingByParty(reporting.Payables)
	if err != nil {
		return nil, nil, err
	}
	var n int64
	if err := j.tx.QueryRow(j.ctx, `
		SELECT count(*) FROM voucher_version vv`+currentVoucherJoin+`
		 WHERE vh.is_deleted = false AND vv.voucher_date >= $1::date AND vv.voucher_date <= $2::date`,
		j.from, j.asOf).Scan(&n); err != nil {
		return nil, nil, fmt.Errorf("profile: voucher count: %w", err)
	}
	out = append(out, mk("receivables_total", "", recv), mk("payables_total", "", pay),
		mk("vouchers_window", "", decimal.NewFromInt(n)))
	return out, nil, nil
}

// SectionNode returns a P&L root node's amount by name, zero when the section
// has no such node (a month with no sales prints no Sales line).
func SectionNode(sec reporting.Section, name string) decimal.Decimal {
	for _, n := range sec.Nodes {
		if n.Name == name {
			return n.Amount
		}
	}
	return decimal.Zero
}
