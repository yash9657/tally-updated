package insights

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/compliance/itc"
	"github.com/khata/cored/internal/statutory/rules"
)

// realtime.go: the detectors that run when a voucher commits.
//
// ⛔ THEY RUN AFTER THE COMMIT, NEVER INSIDE IT. The trigger in 0102 writes an
// insight_voucher_event row in the voucher's own transaction; ProcessEvents
// drains the queue in a transaction of its own. So a detector that errors, is
// slow, or finds a bug in its own SQL can delay an insight but can never
// refuse, slow or roll back a voucher — an insight engine that could block a
// posting would be an insight engine that stops the shop.
//
// Each detector reads the CURRENT version of the voucher. An event for a
// version that has since been superseded is skipped: the later version has its
// own event, and judging a version nobody can see any more would raise an
// insight about a voucher that no longer says that.

// currentJoin is the "current, not deleted" join every query here uses.
const currentJoin = `
	JOIN voucher_head vh
	  ON vh.company_id = vv.company_id
	 AND vh.voucher_id = vv.voucher_id
	 AND vh.current_version_id = vv.id
	 AND vh.is_deleted = false`

// RealtimeExpiry is how long a real-time insight stays open without being
// re-detected or dismissed; the nightly revalidation withdraws it sooner if its
// evidence stops holding.
const RealtimeExpiry = 30 * 24 * time.Hour

type voucherFacts struct {
	id, versionID     uuid.UUID
	class, voucherNo  string
	date              time.Time
	party             uuid.UUID
	partyName         string
	partyGSTIN        string
	pos               string
	tax               decimal.Decimal
	supplierInvoiceNo string
}

func loadVoucher(ctx context.Context, tx pgx.Tx, voucherID, versionID uuid.UUID) (voucherFacts, bool, error) {
	var (
		v      voucherFacts
		party  *uuid.UUID
		date   *time.Time
		pname  *string
		pgstin *string
	)
	err := tx.QueryRow(ctx, `
		SELECT vv.voucher_id, vv.id, COALESCE(vv.payload->>'class', ''), COALESCE(vv.voucher_no, ''),
		       vv.voucher_date, vv.party_ledger_id, l.name, l.gstin, COALESCE(vv.place_of_supply, ''),
		       COALESCE(vv.cgst, 0) + COALESCE(vv.sgst, 0) + COALESCE(vv.igst, 0),
		       COALESCE(vv.supplier_invoice_no, '')
		  FROM voucher_version vv`+currentJoin+`
		  LEFT JOIN ledger l ON l.id = vv.party_ledger_id
		 WHERE vv.voucher_id = $1 AND vv.id = $2`, voucherID, versionID).Scan(
		&v.id, &v.versionID, &v.class, &v.voucherNo, &date, &party, &pname, &pgstin, &v.pos, &v.tax, &v.supplierInvoiceNo)
	if errors.Is(err, pgx.ErrNoRows) {
		return voucherFacts{}, false, nil // superseded or deleted since the event
	}
	if err != nil {
		return voucherFacts{}, false, fmt.Errorf("insights: load voucher: %w", err)
	}
	if date != nil {
		v.date = *date
	}
	if party != nil {
		v.party = *party
	}
	if pname != nil {
		v.partyName = *pname
	}
	if pgstin != nil {
		v.partyGSTIN = *pgstin
	}
	return v, true, nil
}

// detectRealtime runs every real-time detector over one committed voucher.
func detectRealtime(ctx context.Context, tx pgx.Tx, v voucherFacts, now time.Time) ([]Candidate, error) {
	var out []Candidate
	for _, det := range []func(context.Context, pgx.Tx, voucherFacts) ([]Candidate, error){
		detectCreditLimit, detectDuplicateBill, detectPlaceOfSupply, detectHSNRate, detectEWayBill,
	} {
		cs, err := det(ctx, tx, v)
		if err != nil {
			return nil, err
		}
		out = append(out, cs...)
	}
	exp := now.Add(RealtimeExpiry)
	trig := v.id
	for i := range out {
		out[i].AsOf = BusinessDate(now)
		out[i].TriggerVoucherID = &trig
		out[i].ExpiresAt = &exp
	}
	return out, nil
}

// --- credit limit ------------------------------------------------------------

// The balance is re-derived from POSTINGS here; the detector reads it from
// ledger_balance (the incrementally maintained total). Two roads to one figure.
//
// ⛔ voucher_ids IS EVERY VOUCHER THAT MOVED THE BALANCE, up to MaxVoucherIDs
// (5000, 0104; the wave-2 review): the balance is the opening balance plus
// the party's legs on exactly these vouchers. A LIMIT 200 on both sides kept
// the evidence gate green while an auditor summing the list could not reach
// the figure; the 5000 cut, on both sides, is the stated residual.
const evidenceCreditLimit = `
	WITH l AS (
	  SELECT credit_limit,
	         CASE WHEN opening_dc = 'D' THEN opening_balance ELSE -opening_balance END AS op
	    FROM ledger WHERE id = $1::uuid),
	mv AS (
	  SELECT COALESCE(SUM(CASE WHEN p.dr_cr = 'D' THEN p.amount ELSE -p.amount END), 0) AS net
	    FROM posting p
	    JOIN voucher_head vh ON vh.company_id = p.company_id
	     AND vh.current_version_id = p.voucher_version_id AND vh.is_deleted = false
	   WHERE p.ledger_id = $1::uuid),
	vs AS (
	  SELECT vh.voucher_id FROM voucher_head vh
	   WHERE vh.is_deleted = false
	     AND EXISTS (SELECT 1 FROM posting p WHERE p.voucher_version_id = vh.current_version_id
	                                           AND p.ledger_id = $1::uuid)
	   ORDER BY vh.voucher_date DESC NULLS LAST, vh.voucher_id DESC
	   LIMIT 5000)
	SELECT l.credit_limit, l.op + mv.net AS balance, l.op + mv.net - l.credit_limit AS excess,
	       ARRAY(SELECT voucher_id FROM vs) AS voucher_ids
	  FROM l, mv
	 WHERE l.credit_limit IS NOT NULL`

// detectCreditLimit judges every party with a credit limit the voucher
// POSTS TO, on either side.
//
// ⛔ EITHER SIDE, NOT ONLY THE DEBIT (the wave-2 review). A breach is raised by
// a sale, but a part-payment or a credit note changes the balance too — and
// with it the figures of the open insight, which then no longer reproduce, so
// the night withdrew a breach that was still true (Ganga, Rs 59k over a Rs 10k
// limit, paid Rs 1,000 on account and dropped out of the owner's inbox). A
// credit leg now re-judges the party: still over, the insight is superseded
// with the new balance; back under, nothing is raised and the night's
// revalidation withdraws the old one.
func detectCreditLimit(ctx context.Context, tx pgx.Tx, v voucherFacts) ([]Candidate, error) {
	ids, err := uuidList(ctx, tx, `
		SELECT DISTINCT l.id
		  FROM posting p
		  JOIN ledger l ON l.id = p.ledger_id
		 WHERE p.voucher_version_id = $1 AND l.credit_limit IS NOT NULL
		 ORDER BY l.id`, v.versionID)
	if err != nil {
		return nil, fmt.Errorf("insights: credit limit: %w", err)
	}
	var out []Candidate
	for _, id := range ids {
		c, ok, err := creditLimitCandidate(ctx, tx, id, v.voucherNo)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// creditLimitCandidate is the breach for one party as the books stand, or
// ok=false when the party is within its limit (or has none). The balance is
// read from ledger_balance; evidenceCreditLimit re-derives it from postings.
// voucherNo is the voucher the insight names as the latest move.
func creditLimitCandidate(ctx context.Context, tx pgx.Tx, ledgerID uuid.UUID, voucherNo string) (Candidate, bool, error) {
	var (
		name       string
		limit, bal decimal.Decimal
	)
	err := tx.QueryRow(ctx, `
		SELECT l.name, l.credit_limit,
		       CASE WHEN l.opening_dc = 'D' THEN l.opening_balance ELSE -l.opening_balance END
		         + COALESCE(lb.debit, 0) - COALESCE(lb.credit, 0)
		  FROM ledger l
		  LEFT JOIN ledger_balance lb ON lb.ledger_id = l.id
		 WHERE l.id = $1 AND l.credit_limit IS NOT NULL`, ledgerID).Scan(&name, &limit, &bal)
	if errors.Is(err, pgx.ErrNoRows) {
		return Candidate{}, false, nil
	}
	if err != nil {
		return Candidate{}, false, fmt.Errorf("insights: credit limit: %w", err)
	}
	if !bal.GreaterThan(limit) {
		return Candidate{}, false, nil
	}
	ids, err := uuidList(ctx, tx, `
		SELECT vh.voucher_id FROM voucher_head vh
		 WHERE vh.is_deleted = false
		   AND EXISTS (SELECT 1 FROM posting p WHERE p.voucher_version_id = vh.current_version_id AND p.ledger_id = $1)
		 ORDER BY vh.voucher_date DESC NULLS LAST, vh.voucher_id DESC LIMIT 5000`, ledgerID)
	if err != nil {
		return Candidate{}, false, err
	}
	excess := bal.Sub(limit)
	return Candidate{
		Type: TypeCreditLimitBreach, EntityKind: "party", EntityKey: ledgerID.String(), EntityName: name,
		Fingerprint: TypeCreditLimitBreach + ":" + ledgerID.String(),
		Impact:      excess,
		Payload: map[string]string{"party": name, "voucher_no": voucherNo,
			"credit_limit": Canonical(UnitINR, limit), "balance": Canonical(UnitINR, bal),
			"excess": Canonical(UnitINR, excess)},
		EvidenceSQL: evidenceCreditLimit, EvidenceArgs: []string{ledgerID.String()}, VoucherIDs: ids,
	}, true, nil
}

// --- duplicate purchase bill -------------------------------------------------

// ⛔ THE NORMALISATION IS itc.NormalizeDocNo AND IS NOT RESTATED IN SQL.
// Migration 0032 says so in terms ("A normalisation stated twice is a
// normalisation that drifts"). So the DETECTOR groups by the strict key in Go,
// and the evidence re-derives, from the books, that the named vouchers are
// live purchases of this party carrying those supplier numbers AS KEYED, and
// what the bill amounts to — the equivalence of the spellings is Go's claim,
// the rest is the books'.
const evidenceDuplicateBill = `
	WITH d AS (
	  SELECT vv.voucher_id FROM voucher_version vv` + currentJoin + `
	   WHERE COALESCE(vv.payload->>'class', '') = 'Purchase'
	     AND vv.party_ledger_id = $1::uuid
	     AND vv.supplier_invoice_no IN (SELECT jsonb_array_elements_text($2::jsonb)))
	SELECT (SELECT COALESCE(SUM(p.amount), 0) FROM posting p
	          JOIN voucher_head vh ON vh.company_id = p.company_id AND vh.current_version_id = p.voucher_version_id
	         WHERE vh.voucher_id = $3::uuid AND p.ledger_id = $1::uuid AND p.dr_cr = 'C') AS amount,
	       (SELECT count(*) FROM d) AS bills_with_same_number,
	       ARRAY(SELECT voucher_id FROM d) AS voucher_ids`

func detectDuplicateBill(ctx context.Context, tx pgx.Tx, v voucherFacts) ([]Candidate, error) {
	if v.class != "Purchase" || v.party == uuid.Nil || v.supplierInvoiceNo == "" {
		return nil, nil
	}
	key, _ := itc.NormalizeDocNo(v.supplierInvoiceNo)
	if key == "" {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `
		SELECT vv.voucher_id, vv.supplier_invoice_no FROM voucher_version vv`+currentJoin+`
		 WHERE COALESCE(vv.payload->>'class', '') = 'Purchase' AND vv.party_ledger_id = $1
		   AND COALESCE(vv.supplier_invoice_no, '') <> ''`, v.party)
	if err != nil {
		return nil, fmt.Errorf("insights: duplicate bill: %w", err)
	}
	var ids []uuid.UUID
	raw := map[string]bool{}
	for rows.Next() {
		var id uuid.UUID
		var no string
		if err := rows.Scan(&id, &no); err != nil {
			rows.Close()
			return nil, err
		}
		if k, _ := itc.NormalizeDocNo(no); k == key {
			ids = append(ids, id)
			raw[no] = true
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) < 2 {
		return nil, nil
	}
	var amount decimal.Decimal
	if err := tx.QueryRow(ctx, `SELECT COALESCE(SUM(amount), 0) FROM posting
	                             WHERE voucher_version_id = $1 AND ledger_id = $2 AND dr_cr = 'C'`,
		v.versionID, v.party).Scan(&amount); err != nil {
		return nil, err
	}
	var spellings []string
	for s := range raw {
		spellings = append(spellings, s)
	}
	sort.Strings(spellings)
	sj, _ := json.Marshal(spellings)
	return []Candidate{{
		Type: TypeDuplicateBill, EntityKind: "voucher", EntityKey: v.id.String(), EntityName: v.partyName,
		Fingerprint: TypeDuplicateBill + ":" + v.party.String() + ":" + key,
		Impact:      amount,
		Payload: map[string]string{"party": v.partyName, "supplier_invoice_no": v.supplierInvoiceNo,
			"voucher_no": v.voucherNo, "amount": Canonical(UnitINR, amount),
			"bills_with_same_number": fmt.Sprint(len(ids))},
		EvidenceSQL: evidenceDuplicateBill, EvidenceArgs: []string{v.party.String(), string(sj), v.id.String()},
		VoucherIDs: ids,
	}}, nil
}

// --- place of supply vs the recipient's GSTIN --------------------------------

const evidencePlaceOfSupply = `
	SELECT COALESCE(vv.cgst, 0) + COALESCE(vv.sgst, 0) + COALESCE(vv.igst, 0) AS tax,
	       ARRAY[vv.voucher_id] AS voucher_ids
	  FROM voucher_version vv` + currentJoin + `
	  JOIN ledger l ON l.id = vv.party_ledger_id
	 WHERE vv.voucher_id = $1::uuid
	   AND COALESCE(vv.payload->>'class', '') = 'Sales'
	   AND length(COALESCE(l.gstin, '')) >= 2
	   AND COALESCE(vv.place_of_supply, '') <> ''
	   AND vv.place_of_supply <> left(l.gstin, 2)`

func detectPlaceOfSupply(_ context.Context, _ pgx.Tx, v voucherFacts) ([]Candidate, error) {
	if v.class != "Sales" || len(v.partyGSTIN) < 2 || v.pos == "" || !v.tax.IsPositive() {
		return nil, nil
	}
	gstinState := v.partyGSTIN[:2]
	if gstinState == v.pos {
		return nil, nil
	}
	return []Candidate{{
		Type: TypePlaceOfSupply, EntityKind: "voucher", EntityKey: v.id.String(), EntityName: v.partyName,
		Fingerprint: TypePlaceOfSupply + ":" + v.id.String(),
		Impact:      v.tax,
		Payload: map[string]string{"party": v.partyName, "voucher_no": v.voucherNo,
			"place_of_supply": v.pos, "gstin_state": gstinState, "tax": Canonical(UnitINR, v.tax)},
		EvidenceSQL: evidencePlaceOfSupply, EvidenceArgs: []string{v.id.String()},
		VoucherIDs: []uuid.UUID{v.id},
	}}, nil
}

// --- HSN rate vs the item masters --------------------------------------------

// "The master's rate" is only claimed when EVERY stock item carrying the HSN
// agrees on one rate: two masters at 12% and 18% for one HSN is a question
// for the owner, not a rate this detector may assert (HSN → rate is not a
// function: the same heading carries different rates by description and by
// value slab). No HSN directory exists yet (W5 owes it), so the masters are
// the only reference this build has.
const evidenceHSNRate = `
	WITH g AS (
	  SELECT gl.taxable_value FROM voucher_gst_line gl
	    JOIN voucher_head vh ON vh.company_id = gl.company_id
	     AND vh.current_version_id = gl.voucher_version_id AND vh.is_deleted = false
	   WHERE vh.voucher_id = $1::uuid AND gl.hsn = $2 AND gl.gst_rate_pct = $3::numeric),
	m AS (SELECT min(gst_rate_pct) AS r, count(DISTINCT gst_rate_pct) AS n FROM stock_item WHERE hsn = $2)
	SELECT $3::numeric AS line_rate, m.r AS master_rate,
	       (SELECT SUM(taxable_value) FROM g) AS taxable,
	       ROUND((SELECT SUM(taxable_value) FROM g) * abs(m.r - $3::numeric) / 100, 2) AS tax_difference,
	       ARRAY[$1::uuid] AS voucher_ids
	  FROM m
	 WHERE m.n = 1 AND m.r <> $3::numeric AND EXISTS (SELECT 1 FROM g)`

func detectHSNRate(ctx context.Context, tx pgx.Tx, v voucherFacts) ([]Candidate, error) {
	rows, err := tx.Query(ctx, `
		SELECT gl.hsn, gl.gst_rate_pct, gl.taxable_value FROM voucher_gst_line gl
		 WHERE gl.voucher_version_id = $1 AND btrim(gl.hsn) <> ''
		 ORDER BY gl.line_no`, v.versionID)
	if err != nil {
		return nil, fmt.Errorf("insights: hsn lines: %w", err)
	}
	type key struct{ hsn, rate string }
	taxable := map[key]decimal.Decimal{}
	rates := map[key]decimal.Decimal{}
	var order []key
	for rows.Next() {
		var hsn string
		var rate, tv decimal.Decimal
		if err := rows.Scan(&hsn, &rate, &tv); err != nil {
			rows.Close()
			return nil, err
		}
		k := key{hsn, rate.StringFixed(2)}
		if _, ok := taxable[k]; !ok {
			order = append(order, k)
		}
		taxable[k] = taxable[k].Add(tv)
		rates[k] = rate
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Candidate
	for _, k := range order {
		var masters []decimal.Decimal
		mrows, err := tx.Query(ctx, `SELECT DISTINCT gst_rate_pct FROM stock_item WHERE hsn = $1`, k.hsn)
		if err != nil {
			return nil, err
		}
		for mrows.Next() {
			var r decimal.Decimal
			if err := mrows.Scan(&r); err != nil {
				mrows.Close()
				return nil, err
			}
			masters = append(masters, r)
		}
		mrows.Close()
		if len(masters) != 1 || masters[0].Equal(rates[k]) {
			continue
		}
		diff := taxable[k].Mul(masters[0].Sub(rates[k]).Abs()).Div(decimal.NewFromInt(100)).Round(2)
		out = append(out, Candidate{
			Type: TypeHSNRate, EntityKind: "voucher", EntityKey: v.id.String(), EntityName: v.voucherNo,
			Fingerprint: TypeHSNRate + ":" + v.id.String() + ":" + k.hsn + ":" + k.rate,
			Impact:      diff,
			Payload: map[string]string{"voucher_no": v.voucherNo, "hsn": k.hsn,
				"line_rate": Canonical(UnitPct, rates[k]), "master_rate": Canonical(UnitPct, masters[0]),
				"taxable": Canonical(UnitINR, taxable[k]), "tax_difference": Canonical(UnitINR, diff)},
			EvidenceSQL: evidenceHSNRate, EvidenceArgs: []string{v.id.String(), k.hsn, rates[k].String()},
			VoucherIDs: []uuid.UUID{v.id},
		})
	}
	return out, nil
}

// --- e-way bill needed ---------------------------------------------------------

// The threshold is rules.ConsignmentThresholdRupees, applied "at or above" as
// Assess applies it (rule138.go records why), and only to a SALE THAT MOVES
// GOODS (it has stock lines): a services invoice has no consignment. The
// penalty floor is s.122(1)(xiv)'s "ten thousand rupees or the tax sought to be
// evaded, whichever is higher" — the tax on the invoice standing in for the
// tax evaded, which is the reading an officer would start from.
const evidenceEWayBill = `
	SELECT (SELECT COALESCE(SUM(p.amount), 0) FROM posting p
	          WHERE p.voucher_version_id = vv.id AND p.ledger_id = vv.party_ledger_id AND p.dr_cr = 'D') AS invoice_value,
	       $2::numeric AS threshold,
	       GREATEST(10000::numeric, COALESCE(vv.cgst, 0) + COALESCE(vv.sgst, 0) + COALESCE(vv.igst, 0)) AS penalty_floor,
	       ARRAY[vv.voucher_id] AS voucher_ids
	  FROM voucher_version vv` + currentJoin + `
	 WHERE vv.voucher_id = $1::uuid
	   AND COALESCE(vv.payload->>'class', '') = 'Sales'
	   AND EXISTS (SELECT 1 FROM stock_movement sm WHERE sm.voucher_version_id = vv.id)
	   AND NOT EXISTS (SELECT 1 FROM e_way_bill e WHERE e.voucher_id = vv.voucher_id)`

func detectEWayBill(ctx context.Context, tx pgx.Tx, v voucherFacts) ([]Candidate, error) {
	if v.class != "Sales" || v.party == uuid.Nil {
		return nil, nil
	}
	var (
		value    decimal.Decimal
		hasGoods bool
		hasBill  bool
	)
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE((SELECT SUM(amount) FROM posting WHERE voucher_version_id = $1 AND ledger_id = $2 AND dr_cr = 'D'), 0),
		       EXISTS (SELECT 1 FROM stock_movement WHERE voucher_version_id = $1),
		       EXISTS (SELECT 1 FROM e_way_bill WHERE voucher_id = $3)`,
		v.versionID, v.party, v.id).Scan(&value, &hasGoods, &hasBill); err != nil {
		return nil, fmt.Errorf("insights: e-way bill: %w", err)
	}
	threshold := decimal.NewFromInt(rules.ConsignmentThresholdRupees)
	if !hasGoods || hasBill || value.LessThan(threshold) {
		return nil, nil
	}
	penalty := decimal.Max(decimal.NewFromInt(10000), v.tax)
	return []Candidate{{
		Type: TypeEWayBillNeeded, EntityKind: "voucher", EntityKey: v.id.String(), EntityName: v.partyName,
		Fingerprint: TypeEWayBillNeeded + ":" + v.id.String(),
		Impact:      penalty,
		Payload: map[string]string{"party": v.partyName, "voucher_no": v.voucherNo,
			"invoice_value": Canonical(UnitINR, value), "threshold": Canonical(UnitINR, threshold),
			"penalty_floor": Canonical(UnitINR, penalty)},
		EvidenceSQL: evidenceEWayBill, EvidenceArgs: []string{v.id.String(), threshold.String()},
		VoucherIDs: []uuid.UUID{v.id},
	}}, nil
}

// --- the queue -------------------------------------------------------------------

// EventStats summarises one ProcessEvents pass.
type EventStats struct {
	Events  int            `json:"events"`
	Skipped int            `json:"skipped"` // superseded/deleted since the event
	Raised  map[string]int `json:"raised"`  // by outcome
	Refused []string       `json:"refused,omitempty"`
}

// ProcessEvents drains up to limit pending voucher events for the company in
// ctx, in ONE transaction. Events are claimed FOR UPDATE SKIP LOCKED, so two
// pollers on two replicas split the queue rather than both judging a voucher;
// each event is marked processed in the same transaction as the insights it
// raised, so a crash re-processes the event rather than losing it.
func ProcessEvents(ctx context.Context, r Runner, companyID uuid.UUID, now time.Time, limit int) (EventStats, error) {
	if limit <= 0 {
		limit = 100
	}
	st := EventStats{Raised: map[string]int{}}
	err := r.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, voucher_id, version_id FROM insight_voucher_event
			 WHERE processed_at IS NULL
			 ORDER BY enqueued_at, id
			 LIMIT $1
			 FOR UPDATE SKIP LOCKED`, limit)
		if err != nil {
			return fmt.Errorf("insights: claim events: %w", err)
		}
		type ev struct{ id, voucher, version uuid.UUID }
		var evs []ev
		for rows.Next() {
			var e ev
			if err := rows.Scan(&e.id, &e.voucher, &e.version); err != nil {
				rows.Close()
				return err
			}
			evs = append(evs, e)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, e := range evs {
			st.Events++
			raised := 0
			v, ok, err := loadVoucher(ctx, tx, e.voucher, e.version)
			if err != nil {
				return err
			}
			if !ok {
				st.Skipped++
			} else {
				cs, err := detectRealtime(ctx, tx, v, now)
				if err != nil {
					return err
				}
				for _, c := range cs {
					_, outcome, err := store(ctx, tx, companyID, c)
					if err != nil && outcome != Refused {
						return err
					}
					st.Raised[string(outcome)]++
					if outcome == Refused {
						st.Refused = append(st.Refused, err.Error())
						log.Printf("insights: refused %s for %s: %v", c.Type, c.EntityName, err)
						continue
					}
					if outcome == Inserted || outcome == Superseded {
						raised++
					}
				}
			}
			if _, err := tx.Exec(ctx, `UPDATE insight_voucher_event SET processed_at = clock_timestamp(), raised = $2 WHERE id = $1`,
				e.id, raised); err != nil {
				return err
			}
		}
		return nil
	})
	return st, err
}

func uuidList(ctx context.Context, tx pgx.Tx, sql string, args ...any) ([]uuid.UUID, error) {
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func dateOnly(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}
