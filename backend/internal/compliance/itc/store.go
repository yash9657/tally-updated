package itc

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"
)

// store.go is one of only two files in this package that touches a database
// (the other is service.go). The calculation core — normalize, clock, match,
// ims, effect — imports nothing from platform/db and calls no clock, exactly as
// compliance/tax.go, tds.go and tcs.go do.

// booksQuery reads every purchase-side voucher in the period, in the CURRENT
// version, with the supplier's document identity and the ITC classification.
//
// ---------------------------------------------------------------------------
// THE RESTATEMENT GUARD IS BOTH HALVES OR IT IS NOTHING
// ---------------------------------------------------------------------------
// The join to voucher_head carries TWO conditions and each is load-bearing:
//
//	AND vh.current_version_id = vv.id   -- without it an ALTERed voucher is
//	                                      counted ONCE PER VERSION. Every
//	                                      version carries the denormalised GST
//	                                      columns, so a purchase altered from
//	                                      2000 to 5000 contributes BOTH, and the
//	                                      filer claims credit twice on one
//	                                      invoice.
//	WHERE vh.is_deleted = false         -- without it a DELETEd voucher
//	                                      RESURRECTS. A tombstone version still
//	                                      has a row; only the head knows the
//	                                      voucher is gone.
//
// TestITCDecisionsAreVersionAware is the gate on both. It is the same guard
// gstr1.go:322-338 and gstr3b.go carry, and it is written out here rather than
// shared because the two paths must stay independently computed (see gstr3b.go's
// header).
//
// ---------------------------------------------------------------------------
// THE SIGN CONVENTION IS REUSED VERBATIM FROM gstr3b.go
// ---------------------------------------------------------------------------
// A CREDIT note reduces the side it faces. A DEBIT note raised on a SUPPLIER
// (party group nature LIABILITY — a purchase return) also reduces input credit.
// Everything else is +1. Restating the rule differently here would let the
// reconciliation and the return disagree about the same voucher, which is
// exactly the class of bug neither one could detect.
//
// The RAW vt.parent_class is carried too, BEFORE any note fold, so the caller
// can print which voucher class the operator actually used. The DOCUMENT TYPE
// used for matching is derived from the SIGN, not from the class — see
// DocType.Sense() for why.
//
// ---------------------------------------------------------------------------
// taxable_value IS NULL IS **NOT** FILTERED OUT
// ---------------------------------------------------------------------------
// gstr1.go and gstr3b.go both drop those rows. This query keeps them, with
// HasGSTParams = false, because a purchase posted without GST params is
// precisely the thing that must be REPORTED as NOT_COMPARABLE rather than
// silently omitted. Omitting it makes a data gap invisible; reporting it makes
// it a task.
const booksQuery = `
	SELECT vv.voucher_id,
	       COALESCE(vv.voucher_no, ''),
	       vv.voucher_date,
	       vt.parent_class,
	       s.sgn,
	       COALESCE(vv.supplier_invoice_no, ''),
	       vv.supplier_invoice_date,
	       COALESCE(NULLIF(vv.itc_eligibility, ''), l.itc_eligibility, 'ELIGIBLE'),
	       COALESCE(NULLIF(vv.itc_blocked_clause, ''), l.blocked_clause, ''),
	       vv.is_reverse_charge,
	       vv.taxable_value IS NOT NULL,
	       COALESCE(vv.taxable_value, 0) * s.sgn,
	       COALESCE(vv.cgst, 0) * s.sgn,
	       COALESCE(vv.sgst, 0) * s.sgn,
	       COALESCE(vv.igst, 0) * s.sgn,
	       COALESCE(l.gstin, ''),
	       COALESCE(ba.ref_name, '')
	  FROM voucher_version vv
	  JOIN voucher_head    vh
	    ON vh.company_id = vv.company_id
	   AND vh.voucher_id = vv.voucher_id
	   AND vh.current_version_id = vv.id
	  JOIN voucher_type    vt ON vt.id = vv.voucher_type_id
	  LEFT JOIN ledger        l  ON l.id = vv.party_ledger_id
	  LEFT JOIN account_group g  ON g.id = l.group_id
	  CROSS JOIN LATERAL (
	    SELECT CASE
	             WHEN vt.parent_class = 'CREDIT_NOTE' THEN -1
	             WHEN vt.parent_class = 'DEBIT_NOTE'
	                  AND g.nature = 'LIABILITY' THEN -1
	             ELSE 1
	           END
	  ) AS s(sgn)
	  -- The note-to-original pointer, from the EXISTING, already-populated
	  -- bill-wise mechanism (ref_type='AGAINST' under bill_allocation_ref). It
	  -- is INFORMATIONAL: it names the original in the report and changes no
	  -- arithmetic. A second pairing mechanism is not invented.
	  LEFT JOIN LATERAL (
	    SELECT b.ref_name
	      FROM bill_allocation b
	     WHERE b.company_id = vv.company_id
	       AND b.voucher_version_id = vv.id
	       AND b.ref_type = 'AGAINST'
	     ORDER BY b.ref_name
	     LIMIT 1
	  ) AS ba ON true
	 WHERE vh.is_deleted = false
	   AND vv.voucher_date BETWEEN $1 AND $2
	   AND (
	         vt.parent_class = 'PURCHASE'
	      OR (vt.parent_class IN ('CREDIT_NOTE', 'DEBIT_NOTE') AND g.nature = 'LIABILITY')
	   )
	 ORDER BY vv.voucher_date, vv.voucher_no, vv.voucher_id`

// loadBooks reads the purchase side for [from, to].
func loadBooks(ctx context.Context, tx pgx.Tx, from, to string) ([]BooksRow, error) {
	q, err := tx.Query(ctx, booksQuery, from, to)
	if err != nil {
		return nil, fmt.Errorf("itc: query books rows: %w", err)
	}
	defer q.Close()

	var out []BooksRow
	for q.Next() {
		var (
			r          BooksRow
			sgn        int
			docDate    *time.Time
			vDate      time.Time
			taxable    decimal.Decimal
			cg, sg, ig decimal.Decimal
		)
		if err := q.Scan(
			&r.VoucherID, &r.VoucherNo, &vDate, &r.ParentClass, &sgn,
			&r.SupplierDocNo, &docDate,
			&r.ITCEligibility, &r.BlockedClause,
			&r.ReverseCharge, &r.HasGSTParams,
			&taxable, &cg, &sg, &ig,
			&r.PartyGSTIN, &r.BillRef,
		); err != nil {
			return nil, fmt.Errorf("itc: scan books row: %w", err)
		}
		r.VoucherDate = vDate.In(IST)
		if docDate != nil {
			r.SupplierDocDate = docDate.In(IST)
		}
		r.Taxable = taxable
		r.Tax = Tax{CGST: cg, SGST: sg, IGST: ig}
		r.PartyGSTIN = NormalizeGSTIN(r.PartyGSTIN)
		// THE DOCUMENT TYPE COMES FROM THE SIGN, not from the voucher class. Our
		// books record OUR document; the sign is the only thing that says which
		// direction the supplier's document moved credit in. See DocType.Sense().
		if sgn < 0 {
			r.DocType = DocCreditNote
		} else {
			r.DocType = DocInvoice
		}
		out = append(out, r)
	}
	return out, q.Err()
}

// loadLatestStatement returns the id and generation date of the newest
// non-superseded statement of the given kind for the period, or ok=false.
//
// SUPERSESSION, NOT OVERWRITE. gst_inward_statement is APPEND-ONLY: a second,
// DIFFERENT file for the same period is a NEW row whose predecessor's
// superseded_by is set. GSTR-2B is recomputable without limit before filing
// (GSTN IMS FAQ 27), so several are normal — and a return that cannot name WHICH
// 2B it was derived from is not explainable to anybody.
func loadLatestStatement(ctx context.Context, tx pgx.Tx, kind, period string) (id uuid.UUID, genDate *time.Time, ok bool, err error) {
	var g *time.Time
	row := tx.QueryRow(ctx, `
		SELECT id, gen_date
		  FROM gst_inward_statement
		 WHERE kind = $1
		   AND return_period = $2
		   AND superseded_by IS NULL
		 ORDER BY ingested_at DESC, id DESC
		 LIMIT 1`, kind, period)
	if err := row.Scan(&id, &g); err != nil {
		if err == pgx.ErrNoRows {
			return uuid.Nil, nil, false, nil
		}
		return uuid.Nil, nil, false, fmt.Errorf("itc: query latest statement: %w", err)
	}
	if g != nil {
		t := g.In(IST)
		g = &t
	}
	return id, g, true, nil
}

// loadStatementDocuments reads every document of one statement, including the
// docRejdata rows (flagged rejected=true) — they are stored so the operator can
// SEE them, and flagged so nothing can SUM them.
func loadStatementDocuments(ctx context.Context, tx pgx.Tx, statementID uuid.UUID) ([]StatementRow, error) {
	q, err := tx.Query(ctx, `
		SELECT id, statement_id, section, rejected,
		       COALESCE(supplier_gstin, ''), COALESCE(supplier_trade_name, ''),
		       COALESCE(supplier_return_period, ''), supplier_filed_date,
		       doc_no, doc_date, COALESCE(doc_type, 'I'), COALESCE(doc_value, 0),
		       COALESCE(pos, ''), reverse_charge, itc_available,
		       COALESCE(itc_unavailable_reason, ''),
		       taxable_value, cgst, sgst, igst, cess,
		       COALESCE(orig_doc_no, ''), orig_doc_date,
		       COALESCE(ims_status_in_file, ''), COALESCE(irn, '')
		  FROM gst_inward_document
		 WHERE statement_id = $1
		 ORDER BY doc_date, doc_no, id`, statementID)
	if err != nil {
		return nil, fmt.Errorf("itc: query statement documents: %w", err)
	}
	defer q.Close()

	var out []StatementRow
	for q.Next() {
		var (
			r          StatementRow
			filed      *time.Time
			docDate    time.Time
			origDate   *time.Time
			docType    string
			cg, sg, ig decimal.Decimal
		)
		if err := q.Scan(
			&r.ID, &r.StatementID, &r.Section, &r.Rejected,
			&r.SupplierGSTIN, &r.SupplierTradeName,
			&r.SupplierReturnPeriod, &filed,
			&r.DocNo, &docDate, &docType, &r.DocValue,
			&r.POS, &r.ReverseCharge, &r.ITCAvailable,
			&r.ITCUnavailableReason,
			&r.Taxable, &cg, &sg, &ig, &r.Cess,
			&r.OrigDocNo, &origDate,
			&r.IMSStatusInFile, &r.IRN,
		); err != nil {
			return nil, fmt.Errorf("itc: scan statement document: %w", err)
		}
		r.DocDate = docDate.In(IST)
		if filed != nil {
			r.SupplierFiledDate = filed.In(IST)
		}
		if origDate != nil {
			r.OrigDocDate = origDate.In(IST)
		}
		r.DocType = DocType(docType)
		r.Tax = Tax{CGST: cg, SGST: sg, IGST: ig}
		r.SupplierGSTIN = NormalizeGSTIN(r.SupplierGSTIN)
		out = append(out, r)
	}
	return out, q.Err()
}

// loadActions reads the append-only IMS action log for every document of a
// statement, keyed by document id and ORDERED BY (acted_at, id).
//
// The order is part of the contract: EffectiveAction breaks ties on acted_at by
// slice order, and two actions sharing a timestamp is a real possibility on a
// bulk accept. "Whichever the planner returned first" is not an answer to which
// action the filer took.
func loadActions(ctx context.Context, tx pgx.Tx, statementID uuid.UUID) (map[uuid.UUID][]Action, error) {
	q, err := tx.Query(ctx, `
		SELECT a.document_id, a.action, a.acted_at, COALESCE(a.reason, ''), a.acted_by
		  FROM gst_ims_action a
		  JOIN gst_inward_document d
		    ON d.company_id = a.company_id AND d.id = a.document_id
		 WHERE d.statement_id = $1
		 ORDER BY a.document_id, a.acted_at, a.id`, statementID)
	if err != nil {
		return nil, fmt.Errorf("itc: query ims actions: %w", err)
	}
	defer q.Close()

	out := map[uuid.UUID][]Action{}
	for q.Next() {
		var (
			docID uuid.UUID
			a     Action
		)
		if err := q.Scan(&docID, &a.Kind, &a.ActedAt, &a.Reason, &a.ActedBy); err != nil {
			return nil, fmt.Errorf("itc: scan ims action: %w", err)
		}
		out[docID] = append(out[docID], a)
	}
	return out, q.Err()
}

// loadFiling reads the GSTR-3B filing instant for a period, or nil.
//
// It exists because GSTN IMS FAQ 12 freezes the action set at filing. WITHOUT a
// stored instant the same period yields a different answer every time it is
// recomputed — a filed return that changes when you look at it again is not a
// filed return, it is a guess.
func loadFiling(ctx context.Context, tx pgx.Tx, form, period string) (*time.Time, error) {
	var filedAt time.Time
	err := tx.QueryRow(ctx, `
		SELECT filed_at FROM gst_return_filing
		 WHERE form = $1 AND return_period = $2`, form, period).Scan(&filedAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("itc: query return filing: %w", err)
	}
	t := filedAt.In(IST)
	return &t, nil
}

// insertStatement writes one ingested file. The UNIQUE (company_id, kind,
// return_period, source_sha256) makes re-uploading the IDENTICAL file
// idempotent; a DIFFERENT file for the same period is a new row that SUPERSEDES
// its predecessor.
func insertStatement(ctx context.Context, tx pgx.Tx, companyID, id uuid.UUID,
	kind, period, filerGSTIN string, genDate *time.Time, chksum, version, sha string,
	raw []byte, at time.Time, by string) (uuid.UUID, bool, error) {

	var existing uuid.UUID
	err := tx.QueryRow(ctx, `
		SELECT id FROM gst_inward_statement
		 WHERE kind = $1 AND return_period = $2 AND source_sha256 = $3`,
		kind, period, sha).Scan(&existing)
	if err == nil {
		return existing, false, nil // byte-identical re-upload: nothing to do
	}
	if err != pgx.ErrNoRows {
		return uuid.Nil, false, fmt.Errorf("itc: check existing statement: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO gst_inward_statement
		  (id, company_id, kind, return_period, filer_gstin, gen_date, chksum,
		   file_version, source_sha256, raw, ingested_at, ingested_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		id, companyID, kind, period, filerGSTIN, genDate, chksum, version, sha, raw, at, by,
	); err != nil {
		return uuid.Nil, false, fmt.Errorf("itc: insert statement: %w", err)
	}

	// Everything previously live for this (kind, period) is superseded by the
	// new row. Set-based, one statement, no per-row loop.
	if _, err := tx.Exec(ctx, `
		UPDATE gst_inward_statement
		   SET superseded_by = $1
		 WHERE kind = $2 AND return_period = $3
		   AND id <> $1 AND superseded_by IS NULL`,
		id, kind, period); err != nil {
		return uuid.Nil, false, fmt.Errorf("itc: supersede previous statements: %w", err)
	}
	return id, true, nil
}

// insertDocuments writes a statement's documents. It uses CopyFrom-shaped batch
// semantics via a single multi-row INSERT per chunk rather than a per-row
// round-trip.
func insertDocuments(ctx context.Context, tx pgx.Tx, companyID, statementID uuid.UUID, rows []StatementRow) error {
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	for _, r := range rows {
		id, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("itc: new document id: %w", err)
		}
		var filed, orig *time.Time
		if !r.SupplierFiledDate.IsZero() {
			t := r.SupplierFiledDate
			filed = &t
		}
		if !r.OrigDocDate.IsZero() {
			t := r.OrigDocDate
			orig = &t
		}
		batch.Queue(`
			INSERT INTO gst_inward_document
			  (id, company_id, statement_id, section, rejected,
			   supplier_gstin, supplier_trade_name, supplier_return_period, supplier_filed_date,
			   doc_no, doc_date, doc_type, doc_value, pos, reverse_charge,
			   itc_available, itc_unavailable_reason,
			   taxable_value, cgst, sgst, igst, cess,
			   orig_doc_no, orig_doc_date, ims_status_in_file, irn)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26)`,
			id, companyID, statementID, r.Section, r.Rejected,
			nullStr(r.SupplierGSTIN), nullStr(r.SupplierTradeName), nullStr(r.SupplierReturnPeriod), filed,
			r.DocNo, r.DocDate, string(r.DocType), r.DocValue, nullStr(r.POS), r.ReverseCharge,
			r.ITCAvailable, nullStr(r.ITCUnavailableReason),
			r.Taxable, r.Tax.CGST, r.Tax.SGST, r.Tax.IGST, r.Cess,
			nullStr(r.OrigDocNo), orig, nullStr(r.IMSStatusInFile), nullStr(r.IRN))
	}
	res := tx.SendBatch(ctx, batch)
	defer res.Close()
	for i := 0; i < len(rows); i++ {
		if _, err := res.Exec(); err != nil {
			return fmt.Errorf("itc: insert statement document %d: %w", i, err)
		}
	}
	return nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
