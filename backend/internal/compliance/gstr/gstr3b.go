// gstr3b.go generates the GSTR-3B monthly summary return from the same posted,
// immutable voucher log GSTR-1 reads. It is a pure read path: it re-aggregates
// the denormalised GST columns the ledger stamps on Sales AND Purchase
// voucher_versions (`gstActive := in.GST != nil && (in.Class == Sales ||
// in.Class == Purchase)` — ledger.go:583; the columns are assigned at
// ledger.go:820-827 and INSERTed at ledger.go:830-840), so the outward half
// comes from `vt.parent_class = 'SALES'` rows and the ITC half from
// `vt.parent_class = 'PURCHASE'` rows.
//
// Generate3B deliberately issues its OWN SQL rather than delegating to
// Service.Generate: the "3.1(a) == GSTR-1 tax totals" consistency gate is only
// an independent oracle if the two paths are independently computed. Do not
// "simplify" this by re-summing a GSTR1 value.
//
// Tables produced (RESEARCH.md sec 19.1d enumerates the real 3B tables):
//
//   - 3.1(a) osup_det   — outward taxable supplies, from SALES rows.
//   - 3.1(b) osup_zero  — zero-rated (export/SEZ): ALWAYS ZERO in v1; the books
//     carry no export/SEZ marker to classify on.
//   - 3.1(c) osup_nil_exmp — nil-rated/exempt: ALWAYS ZERO in v1 (same reason).
//   - 3.1(d) isup_rev   — inward supplies liable to reverse charge, from
//     PURCHASE rows with is_reverse_charge = true.
//   - 3.1(e) osup_nongst — non-GST outward: ALWAYS ZERO in v1 (no marker).
//   - 3.2 inter_sup     — inter-state supplies to UNREGISTERED recipients, by
//     place of supply. GSTN validation requires 3.2 <= 3.1(a); Generate3B
//     enforces that structurally (see Validate).
//   - 4 itc_elg         — ITC, re-summed from the PURCHASE-class GST columns and
//     split into "ISRC" (reverse charge) and "OTH" (all other ITC).
//
// # DISCLOSED v1 LIMITATIONS (do not remove without replacing the behaviour)
//
//  1. ITC IS REPORTED AS ALL-ELIGIBLE. No eligibility test, no rule 42/43
//     apportionment, no reversals, no ineligible-ITC (section 17(5)) block, no
//     GSTR-2A/2B matching. itc_net == itc_avl total, always. A filer MUST NOT
//     treat this as a filing-ready ITC figure.
//  2. FIELD NAMES ARE NOT VERIFIED AGAINST THE GSTN 3B OFFLINE-UTILITY SCHEMA.
//     No copy of that schema exists in this repo (internal/statutory carries
//     only the NIC e-INVOICE schema; RESEARCH.md sec 19.1d documents the TABLES,
//     not the JSON keys). Known likely divergences: `fp` vs the utility's
//     `ret_period`, and `inter_sup` being an ARRAY here versus an OBJECT
//     (`{unreg_details:[], comp_details:[], uin_details:[]}`) in the utility.
//     This output is a books-derived summary; PORTAL-UPLOAD COMPATIBILITY IS
//     NOT CLAIMED.
//  3. CESS IS NOT MODELLED anywhere in the books, so no `csamt` field is
//     emitted.
//  4. VOUCHERS POSTED WITHOUT GST PARAMS have taxable_value NULL and are
//     EXCLUDED (the `taxable_value IS NOT NULL` filter, inherited from GSTR-1).
//     A company that posts non-GST sales will therefore see 3.1(a) below its
//     P&L turnover.
//  5. RCM OUTWARD SUPPLIES: compliance.TaxDerive still returns non-zero
//     CGST/SGST/IGST under ReverseCharge (tax.go:109-160; only InvoiceTotal is
//     reduced), and those amounts are posted to the output-tax ledgers and
//     included by GSTR1.TaxTotals(). 3.1(a) therefore includes supplier-side RCM
//     tax, which is arguably wrong under GST law (the recipient discharges it).
//     It is included ON PURPOSE so 3.1(a) stays equal to the GSTR-1 rollup and
//     to the posted output tax; fixing it requires changing the outward side of
//     GSTR-1 too.
//  6. INTER-STATE IS DERIVED AS `igst > 0`, exactly as GSTR-1 does
//     (invoice.intra, gstr1.go:296-298), NOT from a supplier state code. A
//     0%-rated INTER-state supply has igst = 0 and is therefore classified
//     intra and omitted from 3.2. This is pre-existing GSTR-1 behaviour, not
//     introduced here; sharing it is what keeps 3.2 consistent with the B2CL
//     classification.
package gstr

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/platform/db"
)

// Options3B configures GSTR-3B generation. It embeds the GSTR-1 Options (GSTIN
// plus the three OUTPUT-tax ledger ids used for the outward posting
// reconciliation) and adds the three INPUT-tax ledger ids needed for the ITC
// side, which have no analogue on Options.
type Options3B struct {
	Options
	// InputCGSTLedgerID, InputSGSTLedgerID, InputIGSTLedgerID identify the
	// INPUT-tax (ITC) ledgers. They drive the table-4 posting reconciliation in
	// Reconcile3B and may be left nil when reconciliation is not required.
	InputCGSTLedgerID uuid.UUID
	InputSGSTLedgerID uuid.UUID
	InputIGSTLedgerID uuid.UUID
}

// GSTR3B is the generated 3B summary for one tax period.
//
// The JSON field names below follow the plan's pinned names and GSTN naming
// conventions but are NOT verified against the GSTN 3B offline-utility schema —
// see limitation (2) in the file doc comment. Treat this as a books-derived
// summary, not a validated upload artifact. ITC is reported ALL-ELIGIBLE
// (limitation (1)).
type GSTR3B struct {
	// GSTIN of the supplier (filer).
	GSTIN string `json:"gstin,omitempty"`
	// FP is the filing period in "MMYYYY" form, e.g. "052025" for May 2025.
	FP string `json:"fp"`
	// T31 is table 3.1 (outward supplies + inward RCM), rows a..e.
	T31 Table31 `json:"sup_details"`
	// T32 is table 3.2: inter-state supplies to unregistered persons by place of
	// supply. v1 emits the unregistered bucket only (no composition/UIN split).
	T32 []Table32Row `json:"inter_sup"`
	// T4 is table 4: eligible ITC (all-eligible in v1).
	T4 Table4ITC `json:"itc_elg"`
}

// Table31 is GSTR-3B table 3.1, rows (a)..(e).
type Table31 struct {
	// OsupDet is 3.1(a): outward taxable supplies other than zero-rated, nil-rated
	// and exempted.
	OsupDet Table31Row `json:"osup_det"`
	// OsupZero is 3.1(b): outward taxable supplies (zero-rated). Always zero in v1.
	OsupZero Table31Row `json:"osup_zero"`
	// OsupNilExmp is 3.1(c): other outward supplies (nil-rated, exempted). Always
	// zero in v1.
	OsupNilExmp Table31Row `json:"osup_nil_exmp"`
	// IsupRev is 3.1(d): inward supplies liable to reverse charge.
	IsupRev Table31Row `json:"isup_rev"`
	// OsupNongst is 3.1(e): non-GST outward supplies. Always zero in v1.
	OsupNongst Table31Row `json:"osup_nongst"`
}

// Table31Row is one 3.1 row: taxable value plus the three tax heads.
type Table31Row struct {
	Txval decimal.Decimal `json:"txval"`
	Iamt  decimal.Decimal `json:"iamt"`
	Camt  decimal.Decimal `json:"camt"`
	Samt  decimal.Decimal `json:"samt"`
}

// Tax returns the row's three tax heads as a TaxTotals.
func (r Table31Row) Tax() TaxTotals {
	return TaxTotals{CGST: r.Camt, SGST: r.Samt, IGST: r.Iamt}
}

// zeroRow is an all-zero 3.1 row (decimal.Decimal's zero value is 0, but being
// explicit keeps the JSON stable and the arithmetic total-order safe).
func zeroRow() Table31Row {
	return Table31Row{Txval: decimal.Zero, Iamt: decimal.Zero, Camt: decimal.Zero, Samt: decimal.Zero}
}

// Table32Row is one place-of-supply row of table 3.2 (inter-state supplies to
// unregistered persons). Only IGST can arise on an inter-state supply, so no
// CGST/SGST fields are emitted.
type Table32Row struct {
	Pos   string          `json:"pos"`
	Txval decimal.Decimal `json:"txval"`
	Iamt  decimal.Decimal `json:"iamt"`
}

// Table4ITC is GSTR-3B table 4 (eligible ITC). v1 reports every input-tax rupee
// found on PURCHASE-class vouchers as available and reverses nothing, so
// ItcNet always equals the sum of ItcAvl. See limitation (1).
type Table4ITC struct {
	// ItcAvl is 4(A): ITC available. v1 emits at most two rows: "ISRC" (inward
	// supplies liable to reverse charge) and "OTH" (all other ITC).
	ItcAvl []Table4Row `json:"itc_avl"`
	// ItcNet is 4(C): net ITC available. v1: == sum(ItcAvl), no reversals.
	ItcNet Table4Row `json:"itc_net"`
}

// Table4Row is one ITC row. Ty is the GSTN row code ("ISRC", "OTH", "NET").
type Table4Row struct {
	Ty   string          `json:"ty"`
	Iamt decimal.Decimal `json:"iamt"`
	Camt decimal.Decimal `json:"camt"`
	Samt decimal.Decimal `json:"samt"`
}

// Tax returns the row's three tax heads as a TaxTotals.
func (r Table4Row) Tax() TaxTotals {
	return TaxTotals{CGST: r.Camt, SGST: r.Samt, IGST: r.Iamt}
}

// OutwardTax returns the 3.1(a) tax heads. It is the value the GSTR-1
// consistency gate compares against GSTR1.TaxTotals().
func (g GSTR3B) OutwardTax() TaxTotals { return g.T31.OsupDet.Tax() }

// ITCTax returns the net ITC (table 4C) tax heads.
func (g GSTR3B) ITCTax() TaxTotals { return g.T4.ItcNet.Tax() }

// ErrInvalid3B reports a generated 3B that violates a GSTN validation rule.
var ErrInvalid3B = fmt.Errorf("gstr: generated GSTR-3B violates a validation rule")

// Validate enforces the GSTN cross-table rule that table 3.2 (inter-state
// supplies to unregistered persons) can never exceed table 3.1(a), on both the
// taxable value and the IGST. Generate3B calls it, so an internally
// inconsistent 3B is an error rather than a plausible-looking wrong return.
func (g GSTR3B) Validate() error {
	txval := decimal.Zero
	iamt := decimal.Zero
	for _, r := range g.T32 {
		txval = txval.Add(r.Txval)
		iamt = iamt.Add(r.Iamt)
	}
	if txval.GreaterThan(g.T31.OsupDet.Txval) {
		return fmt.Errorf("%w: table 3.2 taxable %s exceeds 3.1(a) taxable %s",
			ErrInvalid3B, txval.StringFixed(4), g.T31.OsupDet.Txval.StringFixed(4))
	}
	if iamt.GreaterThan(g.T31.OsupDet.Iamt) {
		return fmt.Errorf("%w: table 3.2 IGST %s exceeds 3.1(a) IGST %s",
			ErrInvalid3B, iamt.StringFixed(4), g.T31.OsupDet.Iamt.StringFixed(4))
	}
	return nil
}

// row3b is one resolved voucher_version row feeding the 3B aggregation.
type row3b struct {
	class      string // 'SALES' or 'PURCHASE'
	pos        string
	reverse    bool
	taxable    decimal.Decimal
	cgst       decimal.Decimal
	sgst       decimal.Decimal
	igst       decimal.Decimal
	registered bool
}

// interState reports whether the row is an inter-state supply, derived the same
// way GSTR-1 derives it (invoice.intra, gstr1.go:296-298): IGST charged.
func (r row3b) interState() bool { return r.igst.GreaterThan(decimal.Zero) }

// Generate3B produces the GSTR-3B summary for the [from, to] inclusive date
// range (ISO "YYYY-MM-DD"). The period FP is taken from the `from` month.
//
// The guard order mirrors Generate verbatim: company scope, then date parsing,
// then range ordering — all before any DB round-trip.
func (s *Service) Generate3B(ctx context.Context, from, to string, opts Options3B) (GSTR3B, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return GSTR3B{}, db.ErrNoCompany
	}
	fromT, err := time.Parse("2006-01-02", from)
	if err != nil {
		return GSTR3B{}, fmt.Errorf("gstr: invalid from date %q: %w", from, err)
	}
	toT, err := time.Parse("2006-01-02", to)
	if err != nil {
		return GSTR3B{}, fmt.Errorf("gstr: invalid to date %q: %w", to, err)
	}
	if toT.Before(fromT) {
		return GSTR3B{}, fmt.Errorf("gstr: period end %s precedes start %s", to, from)
	}

	var rows []row3b
	err = s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		// Same head-version + is_deleted guard as the GSTR-1 query
		// (gstr1.go:322-338), applied to BOTH classes. The guard is load-bearing:
		// without the voucher_head join an ALTERed voucher would be counted twice
		// (every version carries the GST columns) and a DELETEd one would survive.
		q, err := tx.Query(ctx,
			`SELECT vt.parent_class, vv.place_of_supply, vv.is_reverse_charge,
			        vv.taxable_value, vv.cgst, vv.sgst, vv.igst,
			        pl.gstin, pl.registration_type::text
			   FROM voucher_version vv
			   JOIN voucher_head    vh
			     ON vh.company_id = vv.company_id
			    AND vh.voucher_id = vv.voucher_id
			    AND vh.current_version_id = vv.id
			   JOIN voucher_type    vt ON vt.id = vv.voucher_type_id
			   LEFT JOIN ledger     pl ON pl.id = vv.party_ledger_id
			  WHERE vh.is_deleted = false
			    AND vt.parent_class IN ('SALES', 'PURCHASE')
			    AND vv.voucher_date BETWEEN $1 AND $2
			    AND vv.taxable_value IS NOT NULL
			  ORDER BY vt.parent_class, vv.voucher_date, vv.voucher_no, vv.version_no`,
			from, to)
		if err != nil {
			return fmt.Errorf("gstr: query 3b versions: %w", err)
		}
		defer q.Close()

		for q.Next() {
			var (
				class   string
				pos     *string
				reverse bool
				taxable decimal.Decimal
				cgst    *decimal.Decimal
				sgst    *decimal.Decimal
				igst    *decimal.Decimal
				gstin   *string
				regType *string
			)
			if err := q.Scan(&class, &pos, &reverse, &taxable,
				&cgst, &sgst, &igst, &gstin, &regType); err != nil {
				return fmt.Errorf("gstr: scan 3b version: %w", err)
			}
			rows = append(rows, row3b{
				class:      class,
				pos:        derefStr(pos),
				reverse:    reverse,
				taxable:    taxable,
				cgst:       deref(cgst),
				sgst:       deref(sgst),
				igst:       deref(igst),
				registered: isRegistered(derefStr(regType), derefStr(gstin)),
			})
		}
		return q.Err()
	})
	if err != nil {
		return GSTR3B{}, err
	}

	out := build3B(fromT, rows, opts)
	if err := out.Validate(); err != nil {
		return GSTR3B{}, err
	}
	return out, nil
}

// build3B assembles the GSTR3B structure from the resolved rows. It is pure and
// deterministic (stable ordering), so it is straightforward to test.
func build3B(period time.Time, rows []row3b, opts Options3B) GSTR3B {
	out := GSTR3B{
		GSTIN: opts.GSTIN,
		FP:    fmt.Sprintf("%02d%04d", int(period.Month()), period.Year()),
		T31: Table31{
			OsupDet:     zeroRow(),
			OsupZero:    zeroRow(),
			OsupNilExmp: zeroRow(),
			IsupRev:     zeroRow(),
			OsupNongst:  zeroRow(),
		},
	}

	// 3.2 buckets keyed by place of supply.
	interUnreg := map[string]*Table32Row{}
	// Table 4 accumulators.
	itcRCM := TaxTotals{CGST: decimal.Zero, SGST: decimal.Zero, IGST: decimal.Zero}
	itcOther := TaxTotals{CGST: decimal.Zero, SGST: decimal.Zero, IGST: decimal.Zero}
	haveRCM, haveOther := false, false

	addRow := func(dst *Table31Row, r row3b) {
		dst.Txval = dst.Txval.Add(r.taxable)
		dst.Camt = dst.Camt.Add(r.cgst)
		dst.Samt = dst.Samt.Add(r.sgst)
		dst.Iamt = dst.Iamt.Add(r.igst)
	}

	for _, r := range rows {
		switch r.class {
		case "SALES":
			// 3.1(a). See limitation (5): RCM outward tax is included here on
			// purpose so 3.1(a) ties to GSTR-1 and to the posted output tax.
			addRow(&out.T31.OsupDet, r)

			// 3.2: inter-state supplies to UNREGISTERED recipients, by POS.
			if r.interState() && !r.registered {
				b := interUnreg[r.pos]
				if b == nil {
					b = &Table32Row{Pos: r.pos, Txval: decimal.Zero, Iamt: decimal.Zero}
					interUnreg[r.pos] = b
				}
				b.Txval = b.Txval.Add(r.taxable)
				b.Iamt = b.Iamt.Add(r.igst)
			}

		case "PURCHASE":
			// 3.1(d) inward supplies liable to reverse charge. Under RCM the
			// ledger stamps taxable_value = InvoiceTotal (ledger.go:1004-1009)
			// AND non-zero tax heads, so both txval and tax are reportable.
			if r.reverse {
				addRow(&out.T31.IsupRev, r)
				itcRCM.CGST = itcRCM.CGST.Add(r.cgst)
				itcRCM.SGST = itcRCM.SGST.Add(r.sgst)
				itcRCM.IGST = itcRCM.IGST.Add(r.igst)
				haveRCM = true
				continue
			}
			itcOther.CGST = itcOther.CGST.Add(r.cgst)
			itcOther.SGST = itcOther.SGST.Add(r.sgst)
			itcOther.IGST = itcOther.IGST.Add(r.igst)
			haveOther = true
		}
	}

	// 3.2 rows, stable by POS.
	if len(interUnreg) > 0 {
		poss := make([]string, 0, len(interUnreg))
		for p := range interUnreg {
			poss = append(poss, p)
		}
		sort.Strings(poss)
		out.T32 = make([]Table32Row, 0, len(poss))
		for _, p := range poss {
			out.T32 = append(out.T32, *interUnreg[p])
		}
	}

	// Table 4. Rows are emitted only when the class contributed anything, so a
	// books-with-no-purchases period yields an empty itc_avl rather than noise.
	net := TaxTotals{CGST: decimal.Zero, SGST: decimal.Zero, IGST: decimal.Zero}
	if haveRCM {
		out.T4.ItcAvl = append(out.T4.ItcAvl, Table4Row{
			Ty: "ISRC", Camt: itcRCM.CGST, Samt: itcRCM.SGST, Iamt: itcRCM.IGST})
	}
	if haveOther {
		out.T4.ItcAvl = append(out.T4.ItcAvl, Table4Row{
			Ty: "OTH", Camt: itcOther.CGST, Samt: itcOther.SGST, Iamt: itcOther.IGST})
	}
	for _, r := range out.T4.ItcAvl {
		net.CGST = net.CGST.Add(r.Camt)
		net.SGST = net.SGST.Add(r.Samt)
		net.IGST = net.IGST.Add(r.Iamt)
	}
	// v1: net == available (no reversals modelled). See limitation (1).
	out.T4.ItcNet = Table4Row{Ty: "NET", Camt: net.CGST, Samt: net.SGST, Iamt: net.IGST}
	return out
}

// ITCPostingTotals sums the INPUT-tax POSTINGS for the period: the amounts
// posted to the input-tax ledgers named in opts on PURCHASE voucher versions
// whose voucher_date falls in [from, to]. It is the books-side ITC total that
// Reconcile3B ties table 4 against. A nil ledger id contributes zero.
//
// It is the PURCHASE-class twin of PostingTaxTotals (gstr1.go:389-436) and
// carries the identical head-version + is_deleted guard.
func (s *Service) ITCPostingTotals(ctx context.Context, from, to string, opts Options3B) (TaxTotals, error) {
	if _, ok := db.CompanyFromContext(ctx); !ok {
		return TaxTotals{}, db.ErrNoCompany
	}
	sum := func(ctx context.Context, tx pgx.Tx, ledgerID uuid.UUID) (decimal.Decimal, error) {
		if ledgerID == uuid.Nil {
			return decimal.Zero, nil
		}
		var total decimal.Decimal
		err := tx.QueryRow(ctx,
			`SELECT COALESCE(SUM(p.amount), 0)
			   FROM posting p
			   JOIN voucher_version vv ON vv.id = p.voucher_version_id
			   JOIN voucher_head    vh
			     ON vh.company_id = vv.company_id
			    AND vh.voucher_id = vv.voucher_id
			    AND vh.current_version_id = vv.id
			   JOIN voucher_type    vt ON vt.id = vv.voucher_type_id
			  WHERE p.ledger_id = $1
			    AND vh.is_deleted = false
			    AND vt.parent_class = 'PURCHASE'
			    AND vv.voucher_date BETWEEN $2 AND $3`,
			ledgerID, from, to).Scan(&total)
		if err != nil {
			return decimal.Zero, fmt.Errorf("gstr: sum input-tax postings: %w", err)
		}
		return total, nil
	}

	out := TaxTotals{CGST: decimal.Zero, SGST: decimal.Zero, IGST: decimal.Zero}
	err := s.runner.RunInTx(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var err error
		if out.CGST, err = sum(ctx, tx, opts.InputCGSTLedgerID); err != nil {
			return err
		}
		if out.SGST, err = sum(ctx, tx, opts.InputSGSTLedgerID); err != nil {
			return err
		}
		if out.IGST, err = sum(ctx, tx, opts.InputIGSTLedgerID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return TaxTotals{}, err
	}
	return out, nil
}

// Reconciliation3B is the result of tying a generated GSTR-3B against the
// postings for the same period: 3.1(a) against the output-tax postings on Sales
// vouchers, and table 4C against the input-tax postings on Purchase vouchers.
type Reconciliation3B struct {
	// Outward is the 3.1(a) tax total from the return.
	Outward TaxTotals
	// OutwardPostings is the books side: output-tax ledger postings on SALES.
	OutwardPostings TaxTotals
	// ITC is the table-4C net ITC from the return.
	ITC TaxTotals
	// ITCPostings is the books side: input-tax ledger postings on PURCHASE.
	ITCPostings TaxTotals
}

// Balanced reports whether both halves tie out to the paisa, head-for-head.
func (r Reconciliation3B) Balanced() bool {
	return r.Outward.Equal(r.OutwardPostings) && r.ITC.Equal(r.ITCPostings)
}

// Reconcile3B generates the GSTR-3B for the period, sums the output-tax and
// input-tax postings, and returns both sides so the caller can assert they tie
// out. It is the integrity check that the return reflects the books.
func (s *Service) Reconcile3B(ctx context.Context, from, to string, opts Options3B) (GSTR3B, Reconciliation3B, error) {
	g, err := s.Generate3B(ctx, from, to, opts)
	if err != nil {
		return GSTR3B{}, Reconciliation3B{}, err
	}
	outPosted, err := s.PostingTaxTotals(ctx, from, to, opts.Options)
	if err != nil {
		return GSTR3B{}, Reconciliation3B{}, err
	}
	itcPosted, err := s.ITCPostingTotals(ctx, from, to, opts)
	if err != nil {
		return GSTR3B{}, Reconciliation3B{}, err
	}
	return g, Reconciliation3B{
		Outward:         g.OutwardTax(),
		OutwardPostings: outPosted,
		ITC:             g.ITCTax(),
		ITCPostings:     itcPosted,
	}, nil
}
