// Package gstr generates India GST return outputs from the posted, immutable
// voucher log. Phase 1 implements GSTR-1 (the outward-supplies return) from
// Sales vouchers for a tax period, shaped to the GSTN offline-tool JSON schema
// (RESEARCH sec 7 & 19/20).
//
// The generator is a pure read path: it reads the denormalised GST columns the
// ledger stamps on every Sales voucher_version (taxable_value, cgst, sgst,
// igst, place_of_supply, is_reverse_charge) plus the counter-party ledger's GST
// master fields (gstin, state_code, registration_type), classifies each invoice
// into a GSTR-1 section, and rolls the sections up into the GSTN-shaped
// structure. It never writes, and it goes through the tenant-scoped GUC guard
// (db.RunInTx) like every other read in the system, so RLS confines it to the
// caller's company.
//
// Classification (RESEARCH sec 7.3, the Nature-of-Transaction rules):
//
//   - B2B (tables 4A/4B/4C): the recipient is GST-registered — a REGULAR /
//     COMPOSITION / SEZ ledger carrying a GSTIN. Reported invoice-wise, keyed by
//     the counter-party GSTIN.
//   - B2CL (table 5A, "B2C Large"): unregistered recipient, inter-state, invoice
//     value above the B2CL threshold (TallyPrime 5.0 lowered it to Rs 1,00,000).
//     Reported invoice-wise, keyed by place of supply.
//   - B2CS (table 7, "B2C Small"): every other unregistered supply (intra-state,
//     or inter-state at or below the threshold). Reported as a rate-wise summary
//     per (place-of-supply, rate, intra/inter) bucket — not invoice-wise.
//   - HSN summary (table 12): a rate-wise rollup of taxable value and tax. Because
//     Phase 1 persists tax at the voucher aggregate level (no per-line HSN/rate is
//     stored on the immutable payload or any table), the summary is keyed by the
//     effective per-voucher rate derived from the persisted aggregate, with the
//     HSN code left blank unless supplied. This mirrors TallyPrime, which as of
//     early 2026 shipped GSTR-1 JSON excluding rate-wise HSN (handled separately
//     via the offline tool) — see RESEARCH sec 19.1c.
//
// Reconciliation: Reconcile sums the GST output-tax POSTINGS (the credit
// amounts on the CGST/SGST/IGST output ledgers) for the same period and asserts
// they equal the GSTR-1 section tax totals to the paisa. Because an ALTER
// appends a new version whose postings accumulate on top of the prior version
// (Phase 0 ledger_balance semantics), both the GSTR-1 rollup and the posting sum
// aggregate over EVERY version in the period, so the two tie out exactly.
package gstr

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// TxRunner is the minimal database capability the generator needs: run a unit of
// work inside a tenant-scoped transaction. *db.Pool satisfies it.
type TxRunner interface {
	RunInTx(ctx context.Context, fn db.TxFunc) error
}

// b2clThreshold is the B2C Large invoice-value threshold. TallyPrime 5.0
// (Sep 2024) lowered it from Rs 2,50,000 to Rs 1,00,000 (RESEARCH sec 7.0/7.1).
var b2clThreshold = decimal.NewFromInt(100000)

// Service generates GST returns over the tenant-scoped transaction runner.
type Service struct {
	runner TxRunner
}

// NewService constructs a GSTR generation Service.
func NewService(runner TxRunner) *Service {
	return &Service{runner: runner}
}

// Options configures GSTR-1 generation.
type Options struct {
	// GSTIN is the supplier's (filing company's) GSTIN, emitted as the top-level
	// "gstin" field. Optional; empty is allowed for a draft/preview.
	GSTIN string
	// CGSTLedgerID, SGSTLedgerID, IGSTLedgerID identify the output-tax ledgers.
	// They drive the posting-side reconciliation in Reconcile and may be left
	// nil if reconciliation is not required.
	CGSTLedgerID uuid.UUID
	SGSTLedgerID uuid.UUID
	IGSTLedgerID uuid.UUID
}

// GSTR1 is the generated return for one tax period, shaped to the GSTN
// offline-tool JSON (gstin, fp, b2b[], b2cl[], b2cs[], hsn{}). It marshals to
// JSON with the GSTN field names via the struct tags below.
type GSTR1 struct {
	// GSTIN of the supplier (filer).
	GSTIN string `json:"gstin,omitempty"`
	// FP is the filing period in "MMYYYY" form, e.g. "052025" for May 2025.
	FP string `json:"fp"`
	// B2B is the registered-recipient section (tables 4A/4B/4C), grouped by the
	// counter-party GSTIN (the "ctin").
	B2B []B2BParty `json:"b2b,omitempty"`
	// B2CL is the B2C Large section (table 5A), grouped by place of supply.
	B2CL []B2CLPlace `json:"b2cl,omitempty"`
	// B2CS is the B2C Small rate-wise summary (table 7).
	B2CS []B2CSItem `json:"b2cs,omitempty"`
	// CDNR is the credit/debit-note section for REGISTERED counter-parties
	// (table 9B), grouped by the counter-party GSTIN. A sales return raised on a
	// registered customer belongs here, NOT in b2b — and it belongs here as its
	// own dated document rather than as an alteration of the invoice it adjusts.
	CDNR []CDNRParty `json:"cdnr,omitempty"`
	// HSN is the HSN/rate summary (table 12).
	HSN HSNSummary `json:"hsn"`
	// Disclosures names anything about this return that would make a filer's
	// belief about it WRONG. It is not commentary: a return is an assertion to a
	// tax authority, and the one failure mode this package must never have is
	// handing back something that looks complete and is not.
	//
	// ⛔ THE FIRST ENTRY IS THE BLANK-HSN CASE, MEASURED IN A BROWSER 2026-08-07.
	// Table 12 is mandatory, HsnSc may legitimately be blank in this struct when
	// a supply could not be attributed to a code, and the two facts together mean
	// a perfectly well-formed JSON return that the GSTN portal REJECTS. It went
	// unnoticed because the item path takes HSN off the stock master and the demo
	// seed supplies it explicitly — so every gate was green while an
	// accounting-mode voucher keyed by hand produced `"hsn_sc": ""` for 50,000 of
	// taxable value.
	Disclosures []string `json:"disclosures,omitempty"`
}

// CDNRParty is one registered counter-party's credit/debit notes (GSTN "cdnr").
type CDNRParty struct {
	// CTIN is the counter-party GSTIN.
	CTIN string `json:"ctin"`
	// Nt are the notes issued to this counter-party.
	Nt []CDNRNote `json:"nt"`
}

// CDNRNote is a single credit or debit note (GSTN "nt" entry).
type CDNRNote struct {
	// Ntty is the note type: "C" credit, "D" debit.
	Ntty string `json:"ntty"`
	// Nt_num / Nt_dt are the note's own number and date — NOT the invoice's.
	NtNum string `json:"nt_num"`
	NtDt  string `json:"nt_dt"`
	// Val is the note's total value (taxable + tax).
	Val decimal.Decimal `json:"val"`
	// Pos is the place of supply.
	Pos string `json:"pos"`
	// Rchrg is the reverse-charge flag ("Y"/"N").
	Rchrg string `json:"rchrg"`
	// Itms are the rate-wise line details.
	Itms []ITM `json:"itms"`
}

// B2BParty is one registered counter-party's invoices (a GSTN "b2b" entry).
type B2BParty struct {
	// CTIN is the counter-party GSTIN.
	CTIN string `json:"ctin"`
	// Inv are the invoices issued to this counter-party.
	Inv []B2BInvoice `json:"inv"`
}

// B2BInvoice is a single registered-recipient invoice (GSTN "inv" entry).
type B2BInvoice struct {
	// Inum is the invoice number.
	Inum string `json:"inum"`
	// Idt is the invoice date as "DD-MM-YYYY".
	Idt string `json:"idt"`
	// Val is the total invoice value (taxable + tax), 2 dp.
	Val decimal.Decimal `json:"val"`
	// Pos is the two-digit place-of-supply state code.
	Pos string `json:"pos"`
	// Rchrg is "Y"/"N" reverse-charge flag.
	Rchrg string `json:"rchrg"`
	// Itms are the rate-wise line items of this invoice.
	Itms []ITM `json:"itms"`
}

// B2CLPlace groups B2C Large invoices by place of supply (GSTN "b2cl" entry).
type B2CLPlace struct {
	// Pos is the two-digit place-of-supply state code.
	Pos string `json:"pos"`
	// Inv are the large B2C invoices to this place of supply.
	Inv []B2CLInvoice `json:"inv"`
}

// B2CLInvoice is a single B2C Large invoice (GSTN b2cl "inv" entry).
type B2CLInvoice struct {
	Inum string          `json:"inum"`
	Idt  string          `json:"idt"`
	Val  decimal.Decimal `json:"val"`
	Itms []ITM           `json:"itms"`
}

// ITM is a rate-wise line of an invoice (GSTN "itms" entry). Num is the rate
// sequence index (1-based); Itm_det carries the amounts.
type ITM struct {
	Num    int        `json:"num"`
	ItmDet ItemDetail `json:"itm_det"`
}

// ItemDetail is the per-rate detail block (GSTN "itm_det").
type ItemDetail struct {
	// Rt is the combined GST rate percent, e.g. 18.
	Rt decimal.Decimal `json:"rt"`
	// Txval is the taxable value at this rate.
	Txval decimal.Decimal `json:"txval"`
	// Iamt/Camt/Samt are the IGST/CGST/SGST amounts.
	Iamt decimal.Decimal `json:"iamt"`
	Camt decimal.Decimal `json:"camt"`
	Samt decimal.Decimal `json:"samt"`
}

// B2CSItem is one rate-wise B2C Small bucket (GSTN "b2cs" entry).
type B2CSItem struct {
	// Sply_ty is "INTRA" or "INTER".
	SplyTy string `json:"sply_ty"`
	// Pos is the two-digit place-of-supply state code.
	Pos string `json:"pos"`
	// Typ is the supply type marker; GSTN uses "OE" (other than e-commerce).
	Typ string `json:"typ"`
	// Rt is the combined GST rate percent.
	Rt decimal.Decimal `json:"rt"`
	// Txval is the taxable value in this bucket.
	Txval decimal.Decimal `json:"txval"`
	// Iamt/Camt/Samt are the IGST/CGST/SGST amounts in this bucket.
	Iamt decimal.Decimal `json:"iamt"`
	Camt decimal.Decimal `json:"camt"`
	Samt decimal.Decimal `json:"samt"`
}

// HSNSummary is the GSTR-1 table-12 HSN/rate summary (GSTN "hsn").
type HSNSummary struct {
	Data []HSNItem `json:"data"`
}

// HSNItem is one HSN/rate row of the table-12 summary (GSTN hsn "data" entry).
type HSNItem struct {
	// Num is the 1-based row index.
	Num int `json:"num"`
	// HsnSc is the HSN/SAC code; blank when not attributed (see package doc).
	HsnSc string `json:"hsn_sc"`
	// Rt is the combined GST rate percent for the row.
	Rt decimal.Decimal `json:"rt"`
	// Txval is the taxable value rolled up at this HSN/rate.
	Txval decimal.Decimal `json:"txval"`
	// Iamt/Camt/Samt are the rolled-up tax amounts.
	Iamt decimal.Decimal `json:"iamt"`
	Camt decimal.Decimal `json:"camt"`
	Samt decimal.Decimal `json:"samt"`
	// TotVal is the total value (taxable + tax) of the row.
	TotVal decimal.Decimal `json:"tot_val"`
}

// TaxTotals is the summed CGST/SGST/IGST across a set of rows.
//
// ⚠️ THE JSON TAGS WERE ADDED BY T12 AND THEY CHANGED NOTHING ON THE WIRE. This
// type had never been marshalled: Reconciliation and Reconciliation3B are
// internal, and both /gstr1 and /gstr3b send a `reconciliation_ok` BOOLEAN rather
// than the totals themselves. GSTR-9 is the first response to carry a TaxTotals,
// and it takes the same `camt`/`samt`/`iamt` spellings Table31Row and Table4Row
// already use — so one document does not have two conventions for the same three
// numbers. ⛔ Do not remove them: `{"CGST": ...}` beside `{"camt": ...}` in one
// payload is how a frontend reads the wrong key and renders a confident zero.
type TaxTotals struct {
	CGST decimal.Decimal `json:"camt"`
	SGST decimal.Decimal `json:"samt"`
	IGST decimal.Decimal `json:"iamt"`
}

// Total returns the sum of the three heads.
func (t TaxTotals) Total() decimal.Decimal {
	return t.CGST.Add(t.SGST).Add(t.IGST)
}

// Equal reports whether two TaxTotals are equal head-for-head.
func (t TaxTotals) Equal(o TaxTotals) bool {
	return t.CGST.Equal(o.CGST) && t.SGST.Equal(o.SGST) && t.IGST.Equal(o.IGST)
}

// TaxTotals sums the CGST/SGST/IGST recorded across all sections of the return.
// It is the GSTR-1 side of the reconciliation: it must equal the output-tax
// posting sum for the period.
func (g GSTR1) TaxTotals() TaxTotals {
	var t TaxTotals
	t.CGST = decimal.Zero
	t.SGST = decimal.Zero
	t.IGST = decimal.Zero
	for _, p := range g.B2B {
		for _, inv := range p.Inv {
			for _, it := range inv.Itms {
				t.CGST = t.CGST.Add(it.ItmDet.Camt)
				t.SGST = t.SGST.Add(it.ItmDet.Samt)
				t.IGST = t.IGST.Add(it.ItmDet.Iamt)
			}
		}
	}
	for _, p := range g.B2CL {
		for _, inv := range p.Inv {
			for _, it := range inv.Itms {
				t.CGST = t.CGST.Add(it.ItmDet.Camt)
				t.SGST = t.SGST.Add(it.ItmDet.Samt)
				t.IGST = t.IGST.Add(it.ItmDet.Iamt)
			}
		}
	}
	for _, b := range g.B2CS {
		t.CGST = t.CGST.Add(b.Camt)
		t.SGST = t.SGST.Add(b.Samt)
		t.IGST = t.IGST.Add(b.Iamt)
	}
	// CREDIT NOTES REDUCE liability; DEBIT NOTES increase it. Adding a credit
	// note like an invoice would report MORE outward tax after a sales return
	// than before it — and the books would disagree, because the note DEBITS the
	// output-tax ledgers.
	for _, p := range g.CDNR {
		for _, n := range p.Nt {
			sign := decimal.NewFromInt(-1)
			if n.Ntty == "D" {
				sign = decimal.NewFromInt(1)
			}
			for _, it := range n.Itms {
				t.CGST = t.CGST.Add(it.ItmDet.Camt.Mul(sign))
				t.SGST = t.SGST.Add(it.ItmDet.Samt.Mul(sign))
				t.IGST = t.IGST.Add(it.ItmDet.Iamt.Mul(sign))
			}
		}
	}
	return t
}

// invoice is the internal, fully-resolved view of one posted Sales voucher
// version used during classification.
type invoice struct {
	voucherNo  string
	date       time.Time
	pos        string
	reverse    bool
	taxable    decimal.Decimal
	cgst       decimal.Decimal
	sgst       decimal.Decimal
	igst       decimal.Decimal
	ctin       string // counter-party GSTIN, empty if unregistered
	registered bool
	// class is the voucher's parent_class: 'SALES' for an invoice, 'CREDIT_NOTE'
	// / 'DEBIT_NOTE' for an adjustment note. Notes are reported in cdnr (table
	// 9B) as their own dated documents, never folded into b2b.
	class string
}

// isNote reports whether this row is a credit/debit note rather than an invoice.
func (in invoice) isNote() bool {
	return in.class == "CREDIT_NOTE" || in.class == "DEBIT_NOTE"
}

// noteType is the GSTN "ntty" flag: C credit, D debit.
func (in invoice) noteType() string {
	if in.class == "DEBIT_NOTE" {
		return "D"
	}
	return "C"
}

// rate returns the combined effective GST rate percent derived from the
// persisted aggregate tax over the taxable value, rounded to the nearest whole
// percent (GST slabs are integral: 0/5/12/18/28). A zero-taxable invoice yields
// a zero rate.
func (in invoice) rate() decimal.Decimal {
	if in.taxable.IsZero() {
		return decimal.Zero
	}
	tax := in.cgst.Add(in.sgst).Add(in.igst)
	return tax.Mul(decimal.NewFromInt(100)).Div(in.taxable).Round(0)
}

// total returns the invoice total (taxable + tax). Under reverse charge the
// supplier does not collect tax, so the invoice value is the taxable value.
func (in invoice) total() decimal.Decimal {
	if in.reverse {
		return in.taxable
	}
	return in.taxable.Add(in.cgst).Add(in.sgst).Add(in.igst)
}

// intra reports whether the supply is intra-state (CGST+SGST charged).
func (in invoice) intra() bool {
	return !in.igst.GreaterThan(decimal.Zero)
}

// Generate produces the GSTR-1 return for the [from, to] inclusive date range
// (ISO "YYYY-MM-DD"). It reads every Sales voucher_version whose voucher_date
// falls in the range, classifies each into B2B / B2CL / B2CS, and builds the
// HSN summary. The period FP is taken from the `from` month.
func (s *Service) Generate(ctx context.Context, from, to string, opts Options) (GSTR1, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return GSTR1{}, db.ErrNoCompany
	}
	fromT, err := time.Parse("2006-01-02", from)
	if err != nil {
		return GSTR1{}, fmt.Errorf("gstr: invalid from date %q: %w", from, err)
	}
	toT, err := time.Parse("2006-01-02", to)
	if err != nil {
		return GSTR1{}, fmt.Errorf("gstr: invalid to date %q: %w", to, err)
	}
	if toT.Before(fromT) {
		return GSTR1{}, fmt.Errorf("gstr: period end %s precedes start %s", to, from)
	}

	var invoices []invoice
	var hsnRows []hsnRow
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if hsnRows, err = loadHSNRows(ctx, tx, from, to); err != nil {
			return err
		}
		rows, err := tx.Query(ctx,
			`SELECT vv.voucher_no, vv.voucher_date, vv.place_of_supply,
			        vv.is_reverse_charge, vv.taxable_value, vv.cgst, vv.sgst, vv.igst,
			        pl.gstin, pl.registration_type::text, vt.parent_class
			   FROM voucher_version vv
			   JOIN voucher_head    vh
			     ON vh.company_id = vv.company_id
			    AND vh.voucher_id = vv.voucher_id
			    AND vh.current_version_id = vv.id
			   JOIN voucher_type    vt ON vt.id = vv.voucher_type_id
			   LEFT JOIN ledger        pl ON pl.id = vv.party_ledger_id
			   LEFT JOIN account_group pg ON pg.id = pl.group_id
			  WHERE vh.is_deleted = false
			    -- GSTR-1 is the OUTWARD return. Invoices always belong; a
			    -- credit/debit note belongs ONLY when it is raised on a CUSTOMER.
			    --
			    -- The discriminator is the party's group NATURE, not the voucher
			    -- class: both note classes can face either way. A debit note
			    -- raised on a SUPPLIER is a purchase return — from our side it
			    -- reverses input credit and belongs in GSTR-3B, not in our
			    -- outward return. Filtering on class alone reported supplier-side
			    -- notes as outward supply, inflating the return.
			    AND (
			         vt.parent_class = 'SALES'
			      OR (vt.parent_class IN ('CREDIT_NOTE', 'DEBIT_NOTE')
			          AND pg.nature = 'ASSET')
			    )
			    AND vv.voucher_date BETWEEN $1 AND $2
			    AND vv.taxable_value IS NOT NULL
			  ORDER BY vv.voucher_date, vv.voucher_no, vv.version_no`,
			from, to)
		if err != nil {
			return fmt.Errorf("gstr: query sales versions: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var (
				vno     *string
				vdate   time.Time
				pos     *string
				reverse bool
				taxable decimal.Decimal
				cgst    *decimal.Decimal
				sgst    *decimal.Decimal
				igst    *decimal.Decimal
				gstin   *string
				regType *string
				class   string
			)
			if err := rows.Scan(&vno, &vdate, &pos, &reverse, &taxable,
				&cgst, &sgst, &igst, &gstin, &regType, &class); err != nil {
				return fmt.Errorf("gstr: scan sales version: %w", err)
			}
			in := invoice{
				date:    vdate,
				reverse: reverse,
				taxable: taxable,
				cgst:    deref(cgst),
				sgst:    deref(sgst),
				igst:    deref(igst),
			}
			in.voucherNo = derefStr(vno)
			in.pos = derefStr(pos)
			in.ctin = derefStr(gstin)
			in.registered = isRegistered(derefStr(regType), in.ctin)
			in.class = class
			invoices = append(invoices, in)
		}
		return rows.Err()
	})
	if err != nil {
		return GSTR1{}, err
	}

	return build(fromT, invoices, hsnRows, opts), nil
}

// hsnRow is one (hsn, rate) bucket of the table-12 summary, rolled up in SQL
// from voucher_gst_line.
type hsnRow struct {
	hsn              string
	rate             decimal.Decimal
	taxable          decimal.Decimal
	cgst, sgst, igst decimal.Decimal
}

// loadHSNRows builds the GSTR-1 table-12 HSN summary from PER-LINE GST detail
// (migration 0016). It deliberately does NOT reuse the invoice aggregate: the
// summary keys on (hsn, rate), and a single invoice may carry several HSN codes
// at several rates, which a voucher-level aggregate cannot express.
//
// The WHERE clause mirrors the invoice query exactly -- current version, not
// deleted, SALES, in period -- so the two sections describe the same set of
// vouchers. gl.hsn = ” is included rather than skipped: a line with no code
// still carries taxable value the summary must report.
func loadHSNRows(ctx context.Context, tx pgx.Tx, from, to string) ([]hsnRow, error) {
	// SIGNED by document type: a credit note REDUCES the reported outward value
	// at its (hsn, rate), a debit note increases it. Table 12 must describe net
	// outward supply, or a month with a return would overstate every HSN bucket.
	rows, err := tx.Query(ctx,
		`SELECT gl.hsn, gl.gst_rate_pct,
		        SUM(gl.taxable_value * sgn), SUM(gl.cgst * sgn),
		        SUM(gl.sgst * sgn),          SUM(gl.igst * sgn)
		   FROM voucher_gst_line gl
		   JOIN voucher_version vv ON vv.id = gl.voucher_version_id
		   JOIN voucher_head    vh
		     ON vh.company_id = vv.company_id
		    AND vh.voucher_id = vv.voucher_id
		    AND vh.current_version_id = vv.id
		   JOIN voucher_type    vt ON vt.id = vv.voucher_type_id
		   LEFT JOIN ledger        pl ON pl.id = vv.party_ledger_id
		   LEFT JOIN account_group pg ON pg.id = pl.group_id
		   CROSS JOIN LATERAL (
		     SELECT CASE WHEN vt.parent_class = 'CREDIT_NOTE' THEN -1 ELSE 1 END
		   ) AS s(sgn)
		  WHERE vh.is_deleted = false
		    -- Same customer-side restriction as the invoice query above, so
		    -- table 12 describes exactly the documents the return reports.
		    AND (
		         vt.parent_class = 'SALES'
		      OR (vt.parent_class IN ('CREDIT_NOTE', 'DEBIT_NOTE')
		          AND pg.nature = 'ASSET')
		    )
		    AND vv.voucher_date BETWEEN $1 AND $2
		  GROUP BY gl.hsn, gl.gst_rate_pct
		  ORDER BY gl.gst_rate_pct, gl.hsn`,
		from, to)
	if err != nil {
		return nil, fmt.Errorf("gstr: query gst lines: %w", err)
	}
	defer rows.Close()

	var out []hsnRow
	for rows.Next() {
		var h hsnRow
		if err := rows.Scan(&h.hsn, &h.rate, &h.taxable, &h.cgst, &h.sgst, &h.igst); err != nil {
			return nil, fmt.Errorf("gstr: scan gst line: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// PostingTaxTotals sums the output-tax POSTINGS for the period: the amounts
// posted to the CGST/SGST/IGST output ledgers (identified in opts) on Sales
// voucher versions whose voucher_date falls in [from, to]. This is the
// books-side total that Reconcile ties the GSTR-1 rollup against. A nil ledger
// id contributes zero for that head.
func (s *Service) PostingTaxTotals(ctx context.Context, from, to string, opts Options) (TaxTotals, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return TaxTotals{}, db.ErrNoCompany
	}
	sum := func(ctx context.Context, tx pgx.Tx, ledgerID uuid.UUID) (decimal.Decimal, error) {
		if ledgerID == uuid.Nil {
			return decimal.Zero, nil
		}
		var total decimal.Decimal
		err := tx.QueryRow(ctx,
			// NET by dr_cr, not a raw SUM. An invoice CREDITS the output-tax
			// ledger; a credit note DEBITS it to reverse the liability. Summing
			// absolute amounts would make a sales return INCREASE the posted tax
			// total, so the return and the books would drift apart by twice the
			// note. Invoices are credit-only, so netting is identical to the old
			// SUM for books with no notes.
			`SELECT COALESCE(SUM(CASE WHEN p.dr_cr = 'C' THEN p.amount
			                          ELSE -p.amount END), 0)
			   FROM posting p
			   JOIN voucher_version vv ON vv.id = p.voucher_version_id
			   JOIN voucher_head    vh
			     ON vh.company_id = vv.company_id
			    AND vh.voucher_id = vv.voucher_id
			    AND vh.current_version_id = vv.id
			   JOIN voucher_type    vt ON vt.id = vv.voucher_type_id
			  WHERE p.ledger_id = $1
			    AND vh.is_deleted = false
			    AND (
			         vt.parent_class = 'SALES'
			      OR (vt.parent_class IN ('CREDIT_NOTE', 'DEBIT_NOTE')
			          AND EXISTS (SELECT 1 FROM ledger pl
			                        JOIN account_group pg ON pg.id = pl.group_id
			                       WHERE pl.id = vv.party_ledger_id
			                         AND pg.nature = 'ASSET'))
			    )
			    AND vv.voucher_date BETWEEN $2 AND $3`,
			ledgerID, from, to).Scan(&total)
		if err != nil {
			return decimal.Zero, fmt.Errorf("gstr: sum output-tax postings: %w", err)
		}
		return total, nil
	}

	out := TaxTotals{CGST: decimal.Zero, SGST: decimal.Zero, IGST: decimal.Zero}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if out.CGST, err = sum(ctx, tx, opts.CGSTLedgerID); err != nil {
			return err
		}
		if out.SGST, err = sum(ctx, tx, opts.SGSTLedgerID); err != nil {
			return err
		}
		if out.IGST, err = sum(ctx, tx, opts.IGSTLedgerID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return TaxTotals{}, err
	}
	return out, nil
}

// Reconciliation is the result of tying a generated GSTR-1 against the
// output-tax postings for the same period.
type Reconciliation struct {
	// Return is the GSTR-1 side tax total (summed across sections).
	Return TaxTotals
	// Postings is the books side tax total (output-tax ledger postings).
	Postings TaxTotals
}

// Balanced reports whether the GSTR-1 tax equals the posted output tax to the
// paisa, head-for-head.
func (r Reconciliation) Balanced() bool {
	return r.Return.Equal(r.Postings)
}

// Reconcile generates the GSTR-1 for the period, sums the output-tax postings,
// and returns both totals so the caller can assert they tie out. It is the
// integrity check that the return faithfully reflects the books.
func (s *Service) Reconcile(ctx context.Context, from, to string, opts Options) (GSTR1, Reconciliation, error) {
	g, err := s.Generate(ctx, from, to, opts)
	if err != nil {
		return GSTR1{}, Reconciliation{}, err
	}
	posted, err := s.PostingTaxTotals(ctx, from, to, opts)
	if err != nil {
		return GSTR1{}, Reconciliation{}, err
	}
	return g, Reconciliation{Return: g.TaxTotals(), Postings: posted}, nil
}

// build assembles the GSTR1 structure from the resolved invoices. It is pure
// and deterministic (stable ordering), so it is straightforward to test.
func build(period time.Time, invoices []invoice, hsnRows []hsnRow, opts Options) GSTR1 {
	out := GSTR1{
		GSTIN: opts.GSTIN,
		FP:    fmt.Sprintf("%02d%04d", int(period.Month()), period.Year()),
	}

	// B2B grouped by counter-party GSTIN.
	b2bByCTIN := map[string][]B2BInvoice{}
	// B2CL grouped by place of supply.
	b2clByPOS := map[string][]B2CLInvoice{}
	// B2CS rate-wise buckets keyed by (sply_ty, pos, rate).
	b2csBuckets := map[b2csKey]*B2CSItem{}
	// Credit/debit notes to registered parties, grouped by counter-party GSTIN.
	cdnrByCTIN := map[string][]CDNRNote{}
	// HSN rate-wise rollup keyed by (hsn, rate).
	hsnBuckets := map[hsnKey]*HSNItem{}

	for _, in := range invoices {
		rate := in.rate()
		itm := ITM{Num: 1, ItmDet: ItemDetail{
			Rt:    rate,
			Txval: in.taxable,
			Iamt:  in.igst,
			Camt:  in.cgst,
			Samt:  in.sgst,
		}}

		switch {
		// CREDIT/DEBIT NOTES first: a note to a registered party belongs in cdnr
		// (table 9B), never in b2b. Checked BEFORE the registered case, which
		// would otherwise swallow it and report a return as a fresh sale.
		case in.isNote() && in.registered:
			cdnrByCTIN[in.ctin] = append(cdnrByCTIN[in.ctin], CDNRNote{
				Ntty:  in.noteType(),
				NtNum: in.voucherNo,
				NtDt:  in.date.Format("02-01-2006"),
				Val:   in.total(),
				Pos:   in.pos,
				Rchrg: ynFlag(in.reverse),
				Itms:  []ITM{itm},
			})
		case in.registered:
			b2bByCTIN[in.ctin] = append(b2bByCTIN[in.ctin], B2BInvoice{
				Inum:  in.voucherNo,
				Idt:   in.date.Format("02-01-2006"),
				Val:   in.total(),
				Pos:   in.pos,
				Rchrg: ynFlag(in.reverse),
				Itms:  []ITM{itm},
			})
		case !in.intra() && in.total().GreaterThan(b2clThreshold):
			b2clByPOS[in.pos] = append(b2clByPOS[in.pos], B2CLInvoice{
				Inum: in.voucherNo,
				Idt:  in.date.Format("02-01-2006"),
				Val:  in.total(),
				Itms: []ITM{itm},
			})
		default:
			sply := "INTER"
			if in.intra() {
				sply = "INTRA"
			}
			k := b2csKey{sply: sply, pos: in.pos, rate: rate.String()}
			b := b2csBuckets[k]
			if b == nil {
				b = &B2CSItem{
					SplyTy: sply, Pos: in.pos, Typ: "OE", Rt: rate,
					Txval: decimal.Zero, Iamt: decimal.Zero,
					Camt: decimal.Zero, Samt: decimal.Zero,
				}
				b2csBuckets[k] = b
			}
			b.Txval = b.Txval.Add(in.taxable)
			b.Iamt = b.Iamt.Add(in.igst)
			b.Camt = b.Camt.Add(in.cgst)
			b.Samt = b.Samt.Add(in.sgst)
		}

	}

	// HSN/rate rollup (table 12), sourced from PER-LINE detail rather than from
	// the invoice aggregate above, because (hsn, rate) is a line-grain key.
	for _, hr := range hsnRows {
		hk := hsnKey{hsn: hr.hsn, rate: hr.rate.String()}
		h := hsnBuckets[hk]
		if h == nil {
			h = &HSNItem{
				HsnSc: hr.hsn, Rt: hr.rate,
				Txval: decimal.Zero, Iamt: decimal.Zero,
				Camt: decimal.Zero, Samt: decimal.Zero, TotVal: decimal.Zero,
			}
			hsnBuckets[hk] = h
		}
		h.Txval = h.Txval.Add(hr.taxable)
		h.Iamt = h.Iamt.Add(hr.igst)
		h.Camt = h.Camt.Add(hr.cgst)
		h.Samt = h.Samt.Add(hr.sgst)
		// tot_val is taxable + tax FOR THIS BUCKET. Summing the whole invoice
		// total here (as the aggregate version did) would over-count a
		// mixed-rate invoice once per bucket it appears in.
		h.TotVal = h.TotVal.
			Add(hr.taxable).Add(hr.cgst).Add(hr.sgst).Add(hr.igst)
	}

	out.B2B = sortedB2B(b2bByCTIN)
	out.B2CL = sortedB2CL(b2clByPOS)
	out.B2CS = sortedB2CS(b2csBuckets)
	out.CDNR = sortedCDNR(cdnrByCTIN)
	out.HSN = sortedHSN(hsnBuckets)
	out.Disclosures = discloseBlankHSN(out.HSN)
	return out
}

// discloseBlankHSN reports supplies that reached the mandatory table-12 summary
// with NO HSN/SAC code.
//
// ⛔ WHY THIS IS A DISCLOSURE AND NOT A REFUSAL. The books are not wrong — the
// tax, the sides and the ledgers are all correct, and the supply genuinely
// happened. What is wrong is the RETURN: table 12 is mandatory and the portal
// rejects a blank code. Refusing to build the return would leave the filer with
// nothing at all and no way to see what to fix; refusing the VOUCHER at post
// time would brick the OCR-draft path for a bill whose HSN did not survive the
// scan. So the return is produced, and it SAYS what is wrong with it, naming the
// taxable value at stake so the filer can find the vouchers.
//
// The alternative that was rejected outright: dropping the blank row. That would
// make the summary's total disagree with the invoices for no visible reason,
// which is the same defect with the evidence removed.
func discloseBlankHSN(h HSNSummary) []string {
	blank := decimal.Zero
	rows := 0
	for _, it := range h.Data {
		if strings.TrimSpace(it.HsnSc) == "" {
			blank = blank.Add(it.Txval)
			rows++
		}
	}
	if rows == 0 {
		return nil
	}
	return []string{fmt.Sprintf(
		"table 12 (HSN summary) has %d row(s) with NO HSN/SAC code, covering %s of taxable value. "+
			"HSN is mandatory and the portal will reject this return: set the HSN on the supplies "+
			"concerned (an accounting-mode GST voucher takes it from the HSN/SAC field; an item "+
			"invoice takes it from the stock master) and re-read the return.",
		rows, blank.StringFixed(4))}
}

// sortedCDNR renders the cdnr section deterministically: parties by GSTIN, notes
// by number within a party. Mirrors sortedB2B exactly.
func sortedCDNR(m map[string][]CDNRNote) []CDNRParty {
	if len(m) == 0 {
		return nil
	}
	ctins := make([]string, 0, len(m))
	for c := range m {
		ctins = append(ctins, c)
	}
	sort.Strings(ctins)
	out := make([]CDNRParty, 0, len(ctins))
	for _, c := range ctins {
		notes := m[c]
		sort.Slice(notes, func(i, j int) bool { return notes[i].NtNum < notes[j].NtNum })
		out = append(out, CDNRParty{CTIN: c, Nt: notes})
	}
	return out
}

func sortedB2B(m map[string][]B2BInvoice) []B2BParty {
	if len(m) == 0 {
		return nil
	}
	ctins := make([]string, 0, len(m))
	for c := range m {
		ctins = append(ctins, c)
	}
	sort.Strings(ctins)
	out := make([]B2BParty, 0, len(ctins))
	for _, c := range ctins {
		invs := m[c]
		sort.Slice(invs, func(i, j int) bool { return invs[i].Inum < invs[j].Inum })
		out = append(out, B2BParty{CTIN: c, Inv: invs})
	}
	return out
}

func sortedB2CL(m map[string][]B2CLInvoice) []B2CLPlace {
	if len(m) == 0 {
		return nil
	}
	poss := make([]string, 0, len(m))
	for p := range m {
		poss = append(poss, p)
	}
	sort.Strings(poss)
	out := make([]B2CLPlace, 0, len(poss))
	for _, p := range poss {
		invs := m[p]
		sort.Slice(invs, func(i, j int) bool { return invs[i].Inum < invs[j].Inum })
		out = append(out, B2CLPlace{Pos: p, Inv: invs})
	}
	return out
}

// b2csKey identifies a B2C Small rate-wise bucket.
type b2csKey struct {
	sply string
	pos  string
	rate string
}

// hsnKey identifies an HSN/rate rollup bucket.
type hsnKey struct {
	hsn  string
	rate string
}

func sortedB2CS(m map[b2csKey]*B2CSItem) []B2CSItem {
	if len(m) == 0 {
		return nil
	}
	keys := make([]b2csKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].sply != keys[j].sply {
			return keys[i].sply < keys[j].sply
		}
		if keys[i].pos != keys[j].pos {
			return keys[i].pos < keys[j].pos
		}
		return keys[i].rate < keys[j].rate
	})
	out := make([]B2CSItem, 0, len(keys))
	for _, k := range keys {
		out = append(out, *m[k])
	}
	return out
}

func sortedHSN(m map[hsnKey]*HSNItem) HSNSummary {
	if len(m) == 0 {
		return HSNSummary{Data: nil}
	}
	keys := make([]hsnKey, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].hsn != keys[j].hsn {
			return keys[i].hsn < keys[j].hsn
		}
		return keys[i].rate < keys[j].rate
	})
	data := make([]HSNItem, 0, len(keys))
	for i, k := range keys {
		it := *m[k]
		it.Num = i + 1
		data = append(data, it)
	}
	return HSNSummary{Data: data}
}

// deref returns the value of a *decimal.Decimal, or zero if nil.
func deref(d *decimal.Decimal) decimal.Decimal {
	if d == nil {
		return decimal.Zero
	}
	return *d
}

// derefStr returns the value of a *string, or "" if nil.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ynFlag maps a bool to the GSTN "Y"/"N" flag.
func ynFlag(b bool) string {
	if b {
		return "Y"
	}
	return "N"
}

// ErrNoData is returned by callers that choose to treat an empty period as an
// error rather than an empty return.
var ErrNoData = errors.New("gstr: no posted sales in period")

// isRegistered reports whether a counter-party is a registered (B2B) recipient.
// A recipient is registered when it carries a GSTIN and its registration type is
// one that files returns (REGULAR / COMPOSITION / SEZ). CONSUMER / UNREGISTERED /
// UNKNOWN are B2C.
func isRegistered(regType, gstin string) bool {
	if gstin == "" {
		return false
	}
	switch regType {
	case "REGULAR", "COMPOSITION", "SEZ":
		return true
	default:
		return false
	}
}
