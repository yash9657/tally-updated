package itc

// corpus_test.go runs this package against the golden corpora in
// corpora/gstr2b and corpora/ims.
//
// ---------------------------------------------------------------------------
// WHY THE CORPUS IS NOT A TAUTOLOGY
// ---------------------------------------------------------------------------
// A fixture whose `expected` block was produced BY the code it tests proves
// only that the code is deterministic. These fixtures are the CONTRACT BETWEEN
// TWO INDEPENDENT IMPLEMENTATIONS of one written specification:
//
//   - this file runs the GO implementation against them;
//   - corpora/validate.py RE-DERIVES the classification from each fixture's
//     INPUTS with a separate, stdlib-only PYTHON implementation written from
//     the spec — not transcribed from the Go — and fails on any difference.
//
// Neither side may quietly "fix" a disagreement by editing the fixture, because
// the fixture is what the other side is checked against.
//
// THE POISONED-FIXTURE TRIPWIRE. Each family carries a `_poison/`
// subdirectory whose cases are valid in shape and DELIBERATELY WRONG in their
// `expected` block. This file walks it explicitly and FAILS IF A POISONED CASE
// PASSES. That is a test OF THE ORACLE, not of the data: if anyone ever
// "simplifies" a validator into restating the fixture back at itself, every
// poison case starts passing and CI says so in as many words.
//
// The leading underscore keeps `_poison` out of the ordinary glob on both
// sides, so it never inflates a family count.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// MIN_COUNTS floors, mirrored from corpora/validate.py. They exist so that
// DELETING a fixture fails CI rather than quietly shrinking the gate — the
// failure mode where a case that started failing is removed instead of fixed.
const (
	minGSTR2BCases = 10
	minIMSCases    = 8
)

// corporaDir locates <repo>/corpora/<name> by walking up from the package
// directory, exactly as compliance/tds_test.go's corporaSubdir does.
func corporaDir(t *testing.T, name string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 8; i++ {
		candidate := filepath.Join(dir, "corpora", name)
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		dir = filepath.Dir(dir)
	}
	t.Fatalf("could not locate corpora/%s above %q", name, dir)
	return ""
}

// caseFiles lists the *.json cases directly in dir, in name order. It never
// descends, so `_poison/` is not picked up here.
func caseFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		out = append(out, filepath.Join(dir, e.Name()))
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// corpora/gstr2b
// ---------------------------------------------------------------------------

// money is a decimal STRING everywhere in the corpora. json.Number would also
// work; a bare JSON number would not, and the Python side's money() helper
// rejects one outright.
type corpusMoney string

func (m corpusMoney) dec(t *testing.T, field string) decimal.Decimal {
	t.Helper()
	if m == "" {
		return decimal.Zero
	}
	v, err := decimal.NewFromString(string(m))
	if err != nil {
		t.Fatalf("%s: %q is not a decimal", field, string(m))
	}
	return v
}

type corpusTax struct {
	Camt corpusMoney `json:"camt"`
	Samt corpusMoney `json:"samt"`
	Iamt corpusMoney `json:"iamt"`
}

func (c corpusTax) tax(t *testing.T, field string) Tax {
	t.Helper()
	return Tax{
		CGST: c.Camt.dec(t, field+".camt"),
		SGST: c.Samt.dec(t, field+".samt"),
		IGST: c.Iamt.dec(t, field+".iamt"),
	}
}

type corpusBooksRow struct {
	VoucherID           string      `json:"voucherId"`
	VoucherNo           string      `json:"voucherNo"`
	VoucherDate         string      `json:"voucherDate"`
	PartyGSTIN          string      `json:"partyGstin"`
	SupplierInvoiceNo   string      `json:"supplierInvoiceNo"`
	SupplierInvoiceDate string      `json:"supplierInvoiceDate"`
	DocType             string      `json:"docType"`
	ReverseCharge       bool        `json:"reverseCharge"`
	ITCEligibility      string      `json:"itcEligibility"`
	BlockedClause       string      `json:"itcBlockedClause"`
	NoGSTParams         bool        `json:"noGstParams"`
	TaxableValue        corpusMoney `json:"taxableValue"`
	CGST                corpusMoney `json:"cgst"`
	SGST                corpusMoney `json:"sgst"`
	IGST                corpusMoney `json:"igst"`
}

type corpusStmtRow struct {
	DocID                string      `json:"docId"`
	Section              string      `json:"section"`
	Rejected             bool        `json:"rejected"`
	SupplierGSTIN        string      `json:"supplierGstin"`
	SupplierTradeName    string      `json:"supplierTradeName"`
	SupplierReturnPeriod string      `json:"supplierReturnPeriod"`
	SupplierFiledDate    string      `json:"supplierFiledDate"`
	DocNo                string      `json:"docNo"`
	DocDate              string      `json:"docDate"`
	DocType              string      `json:"docType"`
	DocValue             corpusMoney `json:"docValue"`
	POS                  string      `json:"pos"`
	ReverseCharge        bool        `json:"reverseCharge"`
	ITCAvailable         bool        `json:"itcAvailable"`
	ITCUnavailableReason string      `json:"itcUnavailableReason"`
	TaxableValue         corpusMoney `json:"taxableValue"`
	CGST                 corpusMoney `json:"cgst"`
	SGST                 corpusMoney `json:"sgst"`
	IGST                 corpusMoney `json:"igst"`
	OrigDocNo            string      `json:"origDocNo"`
	OrigDocDate          string      `json:"origDocDate"`
	IMSStatus            string      `json:"imsStatusInFile"`
	// IMSActions is the append-only action log for THIS document, nested where
	// the fixture keeps it: alongside the document it is about.
	IMSActions []struct {
		Action  string `json:"action"`
		ActedAt string `json:"actedAt"`
	} `json:"imsActions"`
}

type corpusEffect struct {
	AvlRow string    `json:"avlRow"`
	Avl    corpusTax `json:"avl"`
	RevRul corpusTax `json:"revRul"`
	RevOth corpusTax `json:"revOth"`
	Inelg  corpusTax `json:"inelg"`
}

type corpusLine struct {
	Status        string   `json:"status"`
	Fields        []string `json:"fields"`
	NotComparable string   `json:"notComparable"`
	Books         *string  `json:"books"`
	Statement     *string  `json:"statement"`
	// Candidates are the fixture's short labels for the rows a NEAR_MATCH or
	// AMBIGUOUS line points at. Only the COUNT is compared: a candidate is
	// advisory, it moves no figure, and pinning its exact rendering would make
	// the corpus brittle about something that cannot be wrong in a way that
	// costs money.
	Candidates []string `json:"candidates"`
	Delta      struct {
		Txval corpusMoney `json:"txval"`
		Camt  corpusMoney `json:"camt"`
		Samt  corpusMoney `json:"samt"`
		Iamt  corpusMoney `json:"iamt"`
	} `json:"delta"`
	Effect     corpusEffect `json:"effect"`
	S164Expiry *string      `json:"s164Expiry"`
}

type corpusFinding struct {
	Code      string `json:"code"`
	Ref       string `json:"ref"`
	DocNo     string `json:"docNo"`
	StrictKey string `json:"strictKey"`
}

type gstr2bCase struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Input       struct {
		Period    string           `json:"period"`
		AsOn      string           `json:"asOn"`
		ClaimDate string           `json:"claimDate"`
		AnnualRet string           `json:"annualReturnFiledOn"`
		Books     []corpusBooksRow `json:"books"`
		Statement struct {
			Kind         string          `json:"kind"`
			ReturnPeriod string          `json:"returnPeriod"`
			GenDate      string          `json:"genDate"`
			FiledAt      string          `json:"filedAt"`
			Documents    []corpusStmtRow `json:"documents"`
			Actions      []struct {
				DocID   string `json:"docId"`
				Action  string `json:"action"`
				ActedAt string `json:"actedAt"`
			} `json:"actions"`
		} `json:"statement"`
	} `json:"input"`
	Expected struct {
		RejectedStatementRows []string        `json:"rejectedStatementRows"`
		Findings              []corpusFinding `json:"findings"`
		Lines                 []corpusLine    `json:"lines"`
		Totals                struct {
			ItcAvlIsrc     corpusTax `json:"itcAvlIsrc"`
			ItcAvlOth      corpusTax `json:"itcAvlOth"`
			ItcRevRul      corpusTax `json:"itcRevRul"`
			ItcRevOth      corpusTax `json:"itcRevOth"`
			ItcInelgOth    corpusTax `json:"itcInelgOth"`
			ItcNet         corpusTax `json:"itcNet"`
			PostedInputTax corpusTax `json:"postedInputTax"`
		} `json:"totals"`
	} `json:"expected"`
}

// corpusID turns a fixture's short label ("v1", "s1") into a stable UUID, so
// the fixture stays readable while the engine keeps its real key type.
func corpusID(label string) uuid.UUID {
	b := make([]byte, 16)
	copy(b, label)
	id, _ := uuid.FromBytes(b)
	return id
}

func corpusDay(t *testing.T, s, field string) time.Time {
	t.Helper()
	if s == "" {
		return time.Time{}
	}
	v, err := time.ParseInLocation("2006-01-02", s, IST)
	if err != nil {
		t.Fatalf("%s: %q is not an ISO date", field, s)
	}
	return v
}

func corpusInstant(t *testing.T, s, field string) time.Time {
	t.Helper()
	if s == "" {
		return time.Time{}
	}
	v, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t.Fatalf("%s: %q is not an RFC3339 instant", field, s)
	}
	return v
}

// runGSTR2BCase evaluates one fixture and returns the first disagreement, or
// "" when the engine and the fixture agree completely.
//
// It returns a STRING rather than calling t.Fatalf so the SAME function can be
// used for the poison sweep, where a disagreement is the required outcome.
func runGSTR2BCase(t *testing.T, c gstr2bCase) string {
	t.Helper()

	// ---- inputs ----
	books := make([]BooksRow, 0, len(c.Input.Books))
	for _, b := range c.Input.Books {
		row := BooksRow{
			VoucherID:       corpusID(b.VoucherID),
			VoucherNo:       b.VoucherNo,
			VoucherDate:     corpusDay(t, b.VoucherDate, b.VoucherID+".voucherDate"),
			ParentClass:     "PURCHASE",
			DocType:         DocType(b.DocType),
			PartyGSTIN:      NormalizeGSTIN(b.PartyGSTIN),
			SupplierDocNo:   b.SupplierInvoiceNo,
			SupplierDocDate: corpusDay(t, b.SupplierInvoiceDate, b.VoucherID+".supplierInvoiceDate"),
			Taxable:         b.TaxableValue.dec(t, b.VoucherID+".taxableValue"),
			Tax: Tax{
				CGST: b.CGST.dec(t, b.VoucherID+".cgst"),
				SGST: b.SGST.dec(t, b.VoucherID+".sgst"),
				IGST: b.IGST.dec(t, b.VoucherID+".igst"),
			},
			ReverseCharge:  b.ReverseCharge,
			ITCEligibility: b.ITCEligibility,
			BlockedClause:  b.BlockedClause,
			HasGSTParams:   !b.NoGSTParams,
		}
		if row.DocType == "" {
			row.DocType = DocInvoice
		}
		// A supplier note is recorded with NEGATIVE money, exactly as
		// store.go's sign convention leaves it. The fixture states magnitudes.
		if row.DocType == DocCreditNote && row.Taxable.IsPositive() {
			row.Taxable = row.Taxable.Neg()
			row.Tax = row.Tax.Neg()
		}
		books = append(books, row)
	}

	stmt := make([]StatementRow, 0, len(c.Input.Statement.Documents))
	for _, s := range c.Input.Statement.Documents {
		stmt = append(stmt, StatementRow{
			ID:                   corpusID(s.DocID),
			Section:              s.Section,
			Rejected:             s.Rejected,
			SupplierGSTIN:        NormalizeGSTIN(s.SupplierGSTIN),
			SupplierTradeName:    s.SupplierTradeName,
			SupplierReturnPeriod: s.SupplierReturnPeriod,
			SupplierFiledDate:    corpusDay(t, s.SupplierFiledDate, s.DocID+".supplierFiledDate"),
			DocNo:                s.DocNo,
			DocDate:              corpusDay(t, s.DocDate, s.DocID+".docDate"),
			DocType:              DocType(s.DocType),
			DocValue:             s.DocValue.dec(t, s.DocID+".docValue"),
			POS:                  s.POS,
			ReverseCharge:        s.ReverseCharge,
			ITCAvailable:         s.ITCAvailable,
			ITCUnavailableReason: s.ITCUnavailableReason,
			Taxable:              s.TaxableValue.dec(t, s.DocID+".taxableValue"),
			Tax: Tax{
				CGST: s.CGST.dec(t, s.DocID+".cgst"),
				SGST: s.SGST.dec(t, s.DocID+".sgst"),
				IGST: s.IGST.dec(t, s.DocID+".igst"),
			},
			OrigDocNo:       s.OrigDocNo,
			OrigDocDate:     corpusDay(t, s.OrigDocDate, s.DocID+".origDocDate"),
			IMSStatusInFile: s.IMSStatus,
		})
	}

	period, err := ParsePeriod(c.Input.Period)
	if err != nil {
		t.Fatalf("%s: period: %v", c.ID, err)
	}
	var genDate *time.Time
	if g := corpusDay(t, c.Input.Statement.GenDate, "genDate"); !g.IsZero() {
		genDate = &g
	}
	var freeze *time.Time
	if f := corpusInstant(t, c.Input.Statement.FiledAt, "filedAt"); !f.IsZero() {
		freeze = &f
	}
	var annual *time.Time
	if a := corpusDay(t, c.Input.AnnualRet, "annualReturnFiledOn"); !a.IsZero() {
		annual = &a
	}

	// TWO INSTANTS, and the fixture vocabulary already distinguished them:
	// `asOn` is when the report is generated (the IMS projection), `claimDate`
	// is the day the credit is claimed (s.16(4)). They are carried onto the two
	// separate Policy fields. Where a fixture states only one, the other is
	// derived: an unset claimDate falls to Policy.ClaimInstant (the period's own
	// 3B due date), and an unset asOn borrows the claim day, since a fixture
	// that states only claimDate is not testing the IMS clock.
	asOn := corpusInstant(t, c.Input.AsOn, "asOn")
	claimOn := corpusDay(t, c.Input.ClaimDate, "claimDate")
	if asOn.IsZero() {
		asOn = claimOn
	}

	actions := map[uuid.UUID][]Action{}
	for _, a := range c.Input.Statement.Actions {
		id := corpusID(a.DocID)
		actions[id] = append(actions[id], Action{
			Kind:    a.Action,
			ActedAt: corpusInstant(t, a.ActedAt, "action.actedAt"),
		})
	}
	for _, sd := range c.Input.Statement.Documents {
		id := corpusID(sd.DocID)
		for _, a := range sd.IMSActions {
			actions[id] = append(actions[id], Action{
				Kind:    a.Action,
				ActedAt: corpusInstant(t, a.ActedAt, "imsActions.actedAt"),
			})
		}
	}

	policy := Policy{
		AsOn:                asOn,
		ClaimOn:             claimOn,
		Period:              period,
		Resolution:          ResolutionInstant(period, genDate),
		Freeze:              freeze,
		Actions:             actions,
		AnnualReturnFiledOn: annual,
	}

	got := Match(books, stmt, policy)

	// ---- rejected rows ----
	gotRejected := []string{}
	for _, r := range RejectedRows(stmt) {
		gotRejected = append(gotRejected, labelOf(c, r.ID))
	}
	if d := diffStrings("rejectedStatementRows", c.Expected.RejectedStatementRows, gotRejected); d != "" {
		return d
	}

	// ---- lines ----
	if len(got) != len(c.Expected.Lines) {
		return fmt.Sprintf("line count: got %d, want %d\ngot: %s", len(got), len(c.Expected.Lines), dump(got))
	}
	// Lines are matched by the ROWS THEY ARE ABOUT — (books ref, statement ref) —
	// not by position. The corpus is a contract between a Go and a Python
	// implementation of one spec, and the spec does not fix an emit order; two
	// correct implementations may legitimately list a books-side and a
	// statement-side line the other way round. Determinism of the Go order is
	// pinned separately, by TestMatch_IsDeterministic, where it belongs.
	gotByRef := map[string]Line{}
	for _, g := range got {
		gotByRef[refKey(booksLabelOrEmpty(c, g), stmtLabelOrEmpty(c, g))] = g
	}
	for i, want := range c.Expected.Lines {
		wantKey := refKey(derefOr(want.Books), derefOr(want.Statement))
		g, ok := gotByRef[wantKey]
		if !ok {
			return fmt.Sprintf("no line for books=%s statement=%s (expected line %d, %s)\ngot: %s",
				derefOr(want.Books), derefOr(want.Statement), i, want.Status, dump(got))
		}
		if string(g.Status) != want.Status {
			return fmt.Sprintf("line %d (%s): status %s, want %s\n%s",
				i, describe(g), g.Status, want.Status, dump(got))
		}
		if string(g.NotComparable) != want.NotComparable {
			return fmt.Sprintf("line %d: notComparable %q, want %q", i, g.NotComparable, want.NotComparable)
		}
		gotFields := []string{}
		for _, f := range g.Fields {
			gotFields = append(gotFields, string(f))
		}
		if d := diffStrings(fmt.Sprintf("line %d fields", i), want.Fields, gotFields); d != "" {
			return d
		}
		if d := diffRef(fmt.Sprintf("line %d books", i), want.Books, booksLabel(c, g)); d != "" {
			return d
		}
		if d := diffRef(fmt.Sprintf("line %d statement", i), want.Statement, stmtLabel(c, g)); d != "" {
			return d
		}
		if len(g.Candidates) != len(want.Candidates) {
			return fmt.Sprintf("line %d: %d candidates, want %d", i, len(g.Candidates), len(want.Candidates))
		}
		if d := diffTax(fmt.Sprintf("line %d 4(A)", i), want.Effect.Avl.tax(t, "avl"), g.Effect.Avail); d != "" {
			return d
		}
		if d := diffTax(fmt.Sprintf("line %d 4(B)(1)", i), want.Effect.RevRul.tax(t, "revRul"), g.Effect.RevRule); d != "" {
			return d
		}
		if d := diffTax(fmt.Sprintf("line %d 4(B)(2)", i), want.Effect.RevOth.tax(t, "revOth"), g.Effect.RevOther); d != "" {
			return d
		}
		if d := diffTax(fmt.Sprintf("line %d 4(D)(2)", i), want.Effect.Inelg.tax(t, "inelg"), g.Effect.Inelig); d != "" {
			return d
		}
		gotRow := "OTH"
		if g.Effect.RCM {
			gotRow = "ISRC"
		}
		if want.Effect.AvlRow != "" && want.Effect.AvlRow != gotRow {
			return fmt.Sprintf("line %d: 4(A) row %s, want %s", i, gotRow, want.Effect.AvlRow)
		}
		wantDelta := Tax{
			CGST: want.Delta.Camt.dec(t, "delta.camt"),
			SGST: want.Delta.Samt.dec(t, "delta.samt"),
			IGST: want.Delta.Iamt.dec(t, "delta.iamt"),
		}
		if d := diffTax(fmt.Sprintf("line %d delta", i), wantDelta, g.Delta.Tax); d != "" {
			return d
		}
		if !want.Delta.Txval.dec(t, "delta.txval").Equal(g.Delta.Taxable) {
			return fmt.Sprintf("line %d: delta taxable %s, want %s", i, g.Delta.Taxable, want.Delta.Txval)
		}
		// s.16(4): the expiry is not on the Line — it is a pure function of the
		// document date — so it is re-derived here from the same input the
		// engine used. That keeps the fixture's expiry an independent check on
		// the clock rather than a restatement of it.
		if want.S164Expiry != nil {
			docDate := time.Time{}
			switch {
			case g.Books != nil && !g.Books.SupplierDocDate.IsZero():
				docDate = g.Books.SupplierDocDate
			case g.Statement != nil:
				docDate = g.Statement.DocDate
			}
			if !docDate.IsZero() {
				gotExpiry := Sec16_4Expiry(docDate, annual).Format("2006-01-02")
				if gotExpiry != *want.S164Expiry {
					return fmt.Sprintf("line %d: s.16(4) expiry %s, want %s",
						i, gotExpiry, *want.S164Expiry)
				}
			}
		}
	}

	// ---- findings ----
	gotFindings := []string{}
	for _, l := range got {
		for _, f := range l.Findings {
			ref := ""
			switch f.Side {
			case SideBooks:
				ref = booksLabelOrEmpty(c, l)
			case SideStatement:
				ref = stmtLabelOrEmpty(c, l)
			}
			gotFindings = append(gotFindings, string(f.Code)+"@"+ref)
		}
	}
	wantFindings := []string{}
	for _, f := range c.Expected.Findings {
		wantFindings = append(wantFindings, f.Code+"@"+f.Ref)
	}
	if d := diffStrings("findings", wantFindings, gotFindings); d != "" {
		return d
	}

	// ---- totals ----
	tot := Totalise(got, policy)
	for _, cmp := range []struct {
		name string
		want Tax
		got  Tax
	}{
		{"itcAvlIsrc", c.Expected.Totals.ItcAvlIsrc.tax(t, "itcAvlIsrc"), tot.AvailISRC},
		{"itcAvlOth", c.Expected.Totals.ItcAvlOth.tax(t, "itcAvlOth"), tot.AvailOTH},
		{"itcRevRul", c.Expected.Totals.ItcRevRul.tax(t, "itcRevRul"), tot.RevRule},
		{"itcRevOth", c.Expected.Totals.ItcRevOth.tax(t, "itcRevOth"), tot.RevOther},
		{"itcInelgOth", c.Expected.Totals.ItcInelgOth.tax(t, "itcInelgOth"), tot.Inelig},
		{"itcNet", c.Expected.Totals.ItcNet.tax(t, "itcNet"), tot.Net()},
		{"postedInputTax", c.Expected.Totals.PostedInputTax.tax(t, "postedInputTax"), tot.PostedTotal()},
	} {
		if d := diffTax(cmp.name, cmp.want, cmp.got); d != "" {
			return d
		}
	}

	// (I1) restated as an assertion on the fixture itself: 4(A) + 4(D) must
	// equal the posted input tax. It is checked here as well as in the totals
	// comparison because a fixture whose `postedInputTax` was itself wrong would
	// otherwise agree with a wrong engine.
	postedFromBooks := ZeroTax()
	for _, b := range books {
		postedFromBooks = postedFromBooks.Add(b.Tax)
	}
	if d := diffTax("(I1) 4(A)+4(D) vs the posted input tax on the fixture's own books rows",
		postedFromBooks, tot.PostedTotal()); d != "" {
		return d
	}
	return ""
}

// refKey identifies a line by the two rows it is about.
func refKey(books, stmt string) string { return books + "|" + stmt }

func derefOr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func labelOf(c gstr2bCase, id uuid.UUID) string {
	for _, s := range c.Input.Statement.Documents {
		if corpusID(s.DocID) == id {
			return s.DocID
		}
	}
	for _, b := range c.Input.Books {
		if corpusID(b.VoucherID) == id {
			return b.VoucherID
		}
	}
	return id.String()
}

func booksLabel(c gstr2bCase, l Line) *string {
	if l.Books == nil {
		return nil
	}
	s := labelOf(c, l.Books.VoucherID)
	return &s
}

func stmtLabel(c gstr2bCase, l Line) *string {
	if l.Statement == nil {
		return nil
	}
	s := labelOf(c, l.Statement.ID)
	return &s
}

func booksLabelOrEmpty(c gstr2bCase, l Line) string {
	if p := booksLabel(c, l); p != nil {
		return *p
	}
	return ""
}

func stmtLabelOrEmpty(c gstr2bCase, l Line) string {
	if p := stmtLabel(c, l); p != nil {
		return *p
	}
	return ""
}

func diffRef(name string, want, got *string) string {
	w, g := "<nil>", "<nil>"
	if want != nil {
		w = *want
	}
	if got != nil {
		g = *got
	}
	if w != g {
		return fmt.Sprintf("%s: got %s, want %s", name, g, w)
	}
	return ""
}

func diffTax(name string, want, got Tax) string {
	if want.Equal(got) {
		return ""
	}
	return fmt.Sprintf("%s: got c=%s s=%s i=%s, want c=%s s=%s i=%s",
		name, got.CGST, got.SGST, got.IGST, want.CGST, want.SGST, want.IGST)
}

func diffStrings(name string, want, got []string) string {
	w := append([]string(nil), want...)
	g := append([]string(nil), got...)
	sort.Strings(w)
	sort.Strings(g)
	if strings.Join(w, ",") == strings.Join(g, ",") {
		return ""
	}
	return fmt.Sprintf("%s: got [%s], want [%s]", name, strings.Join(g, ","), strings.Join(w, ","))
}

func loadGSTR2BCase(t *testing.T, path string) gstr2bCase {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var c gstr2bCase
	// DisallowUnknownFields is deliberately NOT used: the Python validator and
	// this loader may legitimately consume different subsets of a fixture, and a
	// field only one side reads must not fail the other.
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if c.ID == "" || c.Description == "" {
		t.Fatalf("%s: every fixture must carry an id and a description saying what "+
			"specific way of being wrong it exists to catch", path)
	}
	return c
}

// TestGSTR2BCorpus runs every corpora/gstr2b case.
func TestGSTR2BCorpus(t *testing.T) {
	dir := corporaDir(t, "gstr2b")
	files := caseFiles(t, dir)
	if len(files) < minGSTR2BCases {
		t.Fatalf("corpora/gstr2b holds %d cases, minimum %d. Deleting a fixture must "+
			"fail CI rather than quietly shrink the gate", len(files), minGSTR2BCases)
	}
	for _, f := range files {
		c := loadGSTR2BCase(t, f)
		t.Run(c.ID, func(t *testing.T) {
			if d := runGSTR2BCase(t, c); d != "" {
				t.Fatalf("%s\n%s\n%s", filepath.Base(f), c.Description, d)
			}
		})
	}
}

// TestGSTR2BPoisonFixturesFail is the tripwire on the oracle.
//
// Every case in corpora/gstr2b/_poison is valid in shape and DELIBERATELY WRONG
// in its `expected` block. If one of them PASSES, the checker above has stopped
// checking — most likely because somebody made it restate the fixture back at
// itself, which is exactly how a golden corpus quietly becomes decoration.
func TestGSTR2BPoisonFixturesFail(t *testing.T) {
	dir := filepath.Join(corporaDir(t, "gstr2b"), "_poison")
	files := caseFiles(t, dir)
	if len(files) == 0 {
		t.Fatalf("corpora/gstr2b/_poison is empty: the tripwire on the oracle is gone, " +
			"and a checker that had stopped checking would look exactly like a passing suite")
	}
	for _, f := range files {
		c := loadGSTR2BCase(t, f)
		t.Run(c.ID, func(t *testing.T) {
			if d := runGSTR2BCase(t, c); d == "" {
				t.Fatalf("POISONED FIXTURE VALIDATED — the oracle has become tautological.\n"+
					"%s carries a deliberately WRONG expected block and the checker "+
					"agreed with it.\n%s", filepath.Base(f), c.Description)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// corpora/ims
// ---------------------------------------------------------------------------

type imsCase struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Input       struct {
		Period           string  `json:"period"`
		StatementGenDate *string `json:"statementGenDate"`
		FiledAt          *string `json:"filedAt"`
		TaxPeriod        string  `json:"taxPeriod"`
		Actions          []struct {
			Action  string `json:"action"`
			ActedAt string `json:"actedAt"`
		} `json:"actions"`
		Probes []struct {
			AsOn                string `json:"asOn"`
			DocCategory         string `json:"docCategory"`
			Section             string `json:"section"`
			ReverseCharge       bool   `json:"reverseCharge"`
			Action              string `json:"action"`
			DocKind             string `json:"docKind"`
			DocDate             string `json:"docDate"`
			OriginalInvoiceDate string `json:"originalInvoiceDate"`
			ClaimDate           string `json:"claimDate"`
			AnnualReturnFiledOn string `json:"annualReturnFiledOn"`
		} `json:"probes"`
		Rows []struct {
			DocCategory string `json:"docCategory"`
			Action      string `json:"action"`
		} `json:"rows"`
	} `json:"input"`
	Expected struct {
		Cutoff     string   `json:"cutoff"`
		Resolution string   `json:"resolution"`
		States     []string `json:"states"`
		Rule       string   `json:"rule"`
		Probes     []struct {
			PendingAllowed *bool   `json:"pendingAllowed"`
			ExpiresAt      *string `json:"expiresAt"`
			Error          *string `json:"error"`
			FYEnd          string  `json:"fyEnd"`
			Expiry         string  `json:"expiry"`
			Barred         *bool   `json:"barred"`
		} `json:"probes"`
		Rows []struct {
			In2B string `json:"in2B"`
			ITC  string `json:"itc"`
		} `json:"rows"`
	} `json:"expected"`
}

// pendingCaseFor maps the corpus's document-category vocabulary onto this
// package's PendingCase. An UNKNOWN category is a hard failure, not a default:
// silently treating an unrecognised restricted category as "ordinary" would
// permit a PENDING the portal refuses.
func pendingCaseFor(t *testing.T, cat string) PendingCase {
	t.Helper()
	switch cat {
	case "CREDIT_NOTE_ORIGINAL":
		return PendingOriginalCreditNote
	case "CN_AMENDMENT_UPWARD":
		return PendingUpwardAmendmentOfCN
	case "CN_AMENDMENT_DOWNWARD_ORIGINAL_REJECTED":
		return PendingDownwardAmendmentOfRejectedCN
	case "INV_DN_AMENDMENT_DOWNWARD_ORIGINAL_ACCEPTED_3B_FILED":
		return PendingDownwardAmendmentOfAcceptedDoc
	case "B2B_INVOICE", "DEBIT_NOTE", "ORDINARY":
		return PendingOrdinary
	default:
		t.Fatalf("unknown document category %q in the pending corpus", cat)
		return PendingOrdinary
	}
}

// runIMSCase evaluates one ims fixture, returning the first disagreement or "".
func runIMSCase(t *testing.T, c imsCase) string {
	t.Helper()
	switch c.Kind {

	case "clock":
		period, err := ParsePeriod(c.Input.Period)
		if err != nil {
			t.Fatalf("%s: period: %v", c.ID, err)
		}
		var genDate *time.Time
		if c.Input.StatementGenDate != nil {
			g := corpusDay(t, *c.Input.StatementGenDate, "statementGenDate")
			genDate = &g
		}
		var freeze *time.Time
		if c.Input.FiledAt != nil {
			f := corpusInstant(t, *c.Input.FiledAt, "filedAt")
			freeze = &f
		}
		if got := DeemedAcceptCutoff(period).Format(time.RFC3339); got != c.Expected.Cutoff {
			// Compare as INSTANTS, not strings, so "+05:30" and "Z" spellings of
			// the same moment agree.
			if !DeemedAcceptCutoff(period).Equal(corpusInstant(t, c.Expected.Cutoff, "cutoff")) {
				return fmt.Sprintf("cutoff: got %s, want %s", got, c.Expected.Cutoff)
			}
		}
		res := ResolutionInstant(period, genDate)
		if !res.Equal(corpusInstant(t, c.Expected.Resolution, "resolution")) {
			return fmt.Sprintf("resolution: got %s, want %s",
				res.Format(time.RFC3339), c.Expected.Resolution)
		}
		var actions []Action
		for _, a := range c.Input.Actions {
			actions = append(actions, Action{
				Kind: a.Action, ActedAt: corpusInstant(t, a.ActedAt, "actedAt"),
			})
		}
		if len(c.Input.Probes) != len(c.Expected.States) {
			t.Fatalf("%s: %d probes but %d expected states", c.ID, len(c.Input.Probes), len(c.Expected.States))
		}
		for i, pr := range c.Input.Probes {
			asOn := corpusInstant(t, pr.AsOn, "probe.asOn")
			got := EffectiveAction(actions, res, freeze, asOn)
			if string(got) != c.Expected.States[i] {
				return fmt.Sprintf("probe %d (asOn %s): state %s, want %s",
					i, pr.AsOn, got, c.Expected.States[i])
			}
		}
		return ""

	case "pending":
		period, err := ParsePeriod(c.Input.TaxPeriod)
		if err != nil {
			t.Fatalf("%s: taxPeriod: %v", c.ID, err)
		}
		for i, pr := range c.Input.Probes {
			allowed, expires := PendingAllowed(period, pendingCaseFor(t, pr.DocCategory))
			w := c.Expected.Probes[i]
			if w.PendingAllowed == nil {
				t.Fatalf("%s probe %d: expected.pendingAllowed is missing", c.ID, i)
			}
			if allowed != *w.PendingAllowed {
				return fmt.Sprintf("probe %d (%s): pendingAllowed %v, want %v",
					i, pr.DocCategory, allowed, *w.PendingAllowed)
			}
			if w.ExpiresAt == nil {
				if !expires.IsZero() {
					return fmt.Sprintf("probe %d (%s): expiresAt %s, want none",
						i, pr.DocCategory, expires.Format(time.RFC3339))
				}
			} else {
				// The fixture states the expiry as an IST DAY (the GSTR-3B due
				// date), which is what it is: a due date, not an instant.
				if !expires.Equal(corpusDay(t, *w.ExpiresAt, "expiresAt")) {
					return fmt.Sprintf("probe %d (%s): expiresAt %s, want %s",
						i, pr.DocCategory, expires.Format(time.RFC3339), *w.ExpiresAt)
				}
			}
		}
		return ""

	case "applicability":
		for i, pr := range c.Input.Probes {
			// The tax period is irrelevant to applicability; a period well past
			// the relaxation keeps the PENDING rule out of the way so this case
			// tests only the section carve-out.
			err := ValidateAction(pr.Action, pr.Section, pr.ReverseCharge,
				Period{2026, time.January}, PendingOrdinary, false)
			w := c.Expected.Probes[i]
			switch {
			case w.Error == nil && err != nil:
				return fmt.Sprintf("probe %d (%s rcm=%v %s): got error %v, want none",
					i, pr.Section, pr.ReverseCharge, pr.Action, err)
			case w.Error != nil && err == nil:
				return fmt.Sprintf("probe %d (%s rcm=%v %s): got no error, want %s",
					i, pr.Section, pr.ReverseCharge, pr.Action, *w.Error)
			case w.Error != nil && *w.Error == "ErrIMSNotApplicable" && !errorIs(err, ErrIMSNotApplicable):
				return fmt.Sprintf("probe %d (%s rcm=%v %s): got %v, want ErrIMSNotApplicable",
					i, pr.Section, pr.ReverseCharge, pr.Action, err)
			}
		}
		return ""

	case "s164":
		for i, pr := range c.Input.Probes {
			var annual *time.Time
			if pr.AnnualReturnFiledOn != "" {
				a := corpusDay(t, pr.AnnualReturnFiledOn, "annualReturnFiledOn")
				annual = &a
			}
			// A DEBIT NOTE'S OWN DATE fixes the financial year — delinked from
			// the underlying invoice w.e.f. 01-01-2021. originalInvoiceDate is
			// carried by the fixture precisely so that an implementation which
			// used it would be caught here.
			docDate := corpusDay(t, pr.DocDate, "docDate")
			claim := corpusDay(t, pr.ClaimDate, "claimDate")
			w := c.Expected.Probes[i]
			if got := FYEnd(docDate).Format("2006-01-02"); got != w.FYEnd {
				return fmt.Sprintf("probe %d (%s %s): fyEnd %s, want %s",
					i, pr.DocKind, pr.DocDate, got, w.FYEnd)
			}
			if got := Sec16_4Expiry(docDate, annual).Format("2006-01-02"); got != w.Expiry {
				return fmt.Sprintf("probe %d (%s %s): expiry %s, want %s",
					i, pr.DocKind, pr.DocDate, got, w.Expiry)
			}
			if w.Barred != nil {
				if got := Sec16_4Barred(docDate, claim, annual); got != *w.Barred {
					return fmt.Sprintf("probe %d (%s %s claimed %s): barred %v, want %v",
						i, pr.DocKind, pr.DocDate, pr.ClaimDate, got, *w.Barred)
				}
			}
		}
		return ""

	case "annexure":
		// GSTN's Annexure-A, verbatim. It carries GSTN's own disclaimer that it
		// is explanatory and "is not a statement of law", which is why it is
		// graded CITED-SECONDARY in the fixture and is checked as a DECISION
		// TABLE rather than being treated as authority.
		for i, r := range c.Input.Rows {
			w := c.Expected.Rows[i]
			applicable := r.Action != "NOT_APPLICABLE"
			if !applicable {
				// Imports, RCM and ISD are outside IMS entirely.
				section := map[string]string{
					"IMPG": SectionIMPG, "ISD": SectionISD, "RCM": SectionB2B,
				}[r.DocCategory]
				rcm := r.DocCategory == "RCM"
				if IMSApplies(section, rcm) {
					return fmt.Sprintf("row %d (%s): IMS was reported as applicable",
						i, r.DocCategory)
				}
				continue
			}
			state := State(r.Action)
			// AVAILABLE vs REDUCTION is the DOCUMENT's direction, not the
			// state's: an accepted invoice adds credit, an accepted CREDIT NOTE
			// takes it away. Both are "the document counts"; conflating them is
			// how a credit note ends up increasing ITC.
			gotITC := "NONE"
			if state.AvailableForITC() {
				gotITC = "AVAILABLE"
				if r.DocCategory == "CREDIT_NOTE" {
					gotITC = "REDUCTION"
				}
			}
			if w.ITC == "DEFERRED" {
				// PENDING is DEFERRED, not DENIED. The distinction is the whole
				// point of the fourth state: an implementation with only
				// accept/reject reports a pending document identically to a
				// rejected one, and the credit is lost rather than carried.
				if state != Pending {
					return fmt.Sprintf("row %d: only PENDING may be DEFERRED, got %s", i, state)
				}
				if state.AvailableForITC() {
					return fmt.Sprintf("row %d: a PENDING document was reported as "+
						"available for ITC in this period", i)
				}
				continue
			}
			if gotITC != w.ITC {
				return fmt.Sprintf("row %d (%s %s): itc %s, want %s",
					i, r.DocCategory, r.Action, gotITC, w.ITC)
			}
		}
		return ""

	default:
		t.Fatalf("%s: unknown ims fixture kind %q", c.ID, c.Kind)
		return ""
	}
}

// errorIs is errors.Is, kept local so the corpus loader's imports stay minimal.
func errorIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func loadIMSCase(t *testing.T, path string) imsCase {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var c imsCase
	if err := json.Unmarshal(raw, &c); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if c.ID == "" || c.Kind == "" || c.Description == "" {
		t.Fatalf("%s: every fixture must carry an id, a kind and a description", path)
	}
	return c
}

// TestIMSCorpus runs every corpora/ims case.
func TestIMSCorpus(t *testing.T) {
	dir := corporaDir(t, "ims")
	files := caseFiles(t, dir)
	if len(files) < minIMSCases {
		t.Fatalf("corpora/ims holds %d cases, minimum %d", len(files), minIMSCases)
	}
	for _, f := range files {
		c := loadIMSCase(t, f)
		t.Run(c.ID, func(t *testing.T) {
			if d := runIMSCase(t, c); d != "" {
				t.Fatalf("%s\n%s\n%s", filepath.Base(f), c.Description, d)
			}
		})
	}
}

// TestIMSPoisonFixturesFail is the ims family's tripwire on the oracle. See
// TestGSTR2BPoisonFixturesFail.
func TestIMSPoisonFixturesFail(t *testing.T) {
	dir := filepath.Join(corporaDir(t, "ims"), "_poison")
	files := caseFiles(t, dir)
	if len(files) == 0 {
		t.Fatalf("corpora/ims/_poison is empty: the tripwire on the oracle is gone")
	}
	for _, f := range files {
		c := loadIMSCase(t, f)
		t.Run(c.ID, func(t *testing.T) {
			if d := runIMSCase(t, c); d == "" {
				t.Fatalf("POISONED FIXTURE VALIDATED — the oracle has become tautological.\n"+
					"%s carries a deliberately WRONG expected block and the checker "+
					"agreed with it.\n%s", filepath.Base(f), c.Description)
			}
		})
	}
}
