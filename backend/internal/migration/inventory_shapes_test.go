package migration_test

// inventory_shapes_test.go — THE GOODS, AND AN ORACLE THAT SHARES NO CODE WITH
// THE IMPORTER.
//
// ⛔⛔ WHY THIS FILE COULD NOT BE A TRIAL-BALANCE TEST
// ===========================================================================
// Every assertion in the sibling TallyPrime corpus test is an ACCOUNTING one,
// and the importer passed all of them for months while reading every stock line
// off the wire and throwing it away. That is not a gap in those tests, it is a
// property of the statement: the rupee value of a trade is in the ledger
// entries, so the books balance whether the goods arrived or not. A migrated
// trader got a Balance Sheet carrying closing stock and a Stock Summary with
// nothing in it.
//
// ⛔ SO THE ORACLE HERE IS QUANTITIES, AND IT IS RE-DERIVED FROM THE XML BY A
// SECOND PARSER (`oracleEnvelope` below) that shares no type, no accessor and no
// helper with internal/integration. It walks the same bytes with its own structs
// and its own rules and produces the closing quantity of every (item, godown,
// batch) position. If the importer and this disagree, one of them is wrong and
// the test says which figure it read.
//
// ⚠️ NEVER COMPARE THE IMPORTER TO ITSELF. Asserting that the Stock Summary
// matches integration.TallyVoucher.InventoryLines() would pass on a build where
// both are wrong the same way, which is exactly the class of defect the first
// real file produced three of.

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/khata/cored/internal/ledger"
	"github.com/khata/cored/internal/masters"
	"github.com/khata/cored/internal/migration"
	"github.com/khata/cored/internal/reporting"
)

// ---------------------------------------------------------------------------
// THE INDEPENDENT ORACLE
// ---------------------------------------------------------------------------

type oracleEnvelope struct {
	Messages []struct {
		Unit *struct {
			Name string `xml:"NAME,attr"`
		} `xml:"UNIT"`
		Item *struct {
			Name      string `xml:"NAME,attr"`
			Opening   string `xml:"OPENINGBALANCE"`
			OpenValue string `xml:"OPENINGVALUE"`
			Batches   []struct {
				Godown    string `xml:"GODOWNNAME"`
				Batch     string `xml:"BATCHNAME"`
				Opening   string `xml:"OPENINGBALANCE"`
				OpenValue string `xml:"OPENINGVALUE"`
			} `xml:"BATCHALLOCATIONS.LIST"`
		} `xml:"STOCKITEM"`
		Voucher *struct {
			Type  string `xml:"VCHTYPE,attr"`
			Lines []struct {
				Item    string `xml:"STOCKITEMNAME"`
				Qty     string `xml:"ACTUALQTY"`
				Godown  string `xml:"GODOWNNAME"`
				Batches []struct {
					Godown string `xml:"GODOWNNAME"`
					Batch  string `xml:"BATCHNAME"`
					Qty    string `xml:"ACTUALQTY"`
				} `xml:"BATCHALLOCATIONS.LIST"`
			} `xml:"ALLINVENTORYENTRIES.LIST"`
		} `xml:"VOUCHER"`
	} `xml:"BODY>IMPORTDATA>REQUESTDATA>TALLYMESSAGE"`
}

// oraclePosition is a position key in the oracle's own terms.
type oraclePosition struct{ item, godown, batch string }

// oracleQuantity parses "120.00 BAG" / "-5.00 BAG" / "" WITHOUT calling
// integration.SplitQtyUnit — a second implementation on purpose. It keeps every
// character up to the first space and refuses anything else, which is stricter
// than the importer and therefore cannot agree with it by accident.
func oracleQuantity(t *testing.T, raw string) decimal.Decimal {
	t.Helper()
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return decimal.Zero
	}
	number := raw
	if i := strings.IndexByte(raw, ' '); i >= 0 {
		number = raw[:i]
	}
	d, err := decimal.NewFromString(strings.ReplaceAll(number, ",", ""))
	if err != nil {
		t.Fatalf("oracle: %q is not a quantity: %v", raw, err)
	}
	return d
}

// oracleClosingQuantities re-derives the closing quantity of every position in
// the file: openings plus movements, with a Purchase adding and a Sale
// subtracting, and a NEGATIVE quantity meaning the opposite of its voucher.
//
// It is deliberately signed arithmetic over one map — the closing QUANTITY of a
// position is order-independent (a plain sum), which is exactly why quantity can
// be checked this cheaply while VALUE cannot.
func oracleClosingQuantities(t *testing.T, path string) map[oraclePosition]decimal.Decimal {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("oracle: read %s: %v", path, err)
	}
	// The file is UTF-16LE with a BOM, because that is what TallyPrime writes.
	// ⛔ DECODED HERE BY HAND rather than through the importer's charset reader:
	// door 7 of the back-dated cutoff survived three test suites because every
	// test body was UTF-8, so an oracle that could only read UTF-8 would be
	// unable to check the one encoding that matters.
	var env oracleEnvelope
	dec := xml.NewDecoder(strings.NewReader(utf16LEToString(t, raw)))
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("oracle: parse %s: %v", path, err)
	}

	out := map[oraclePosition]decimal.Decimal{}
	add := func(p oraclePosition, q decimal.Decimal) {
		out[p] = out[p].Add(q)
	}

	items := 0
	for _, m := range env.Messages {
		if m.Item != nil {
			items++
			if len(m.Item.Batches) == 0 {
				add(oraclePosition{item: m.Item.Name}, oracleQuantity(t, m.Item.Opening))
				continue
			}
			for _, b := range m.Item.Batches {
				add(oraclePosition{m.Item.Name, b.Godown, b.Batch}, oracleQuantity(t, b.Opening))
			}
			continue
		}
		if m.Voucher == nil {
			continue
		}
		var sign decimal.Decimal
		switch strings.ToLower(strings.TrimSpace(m.Voucher.Type)) {
		case "purchase":
			sign = decimal.NewFromInt(1)
		case "sales":
			sign = decimal.NewFromInt(-1)
		default:
			// Any other class moves no goods in this build; the importer warns
			// and imports none, so the oracle must expect none.
			continue
		}
		for _, line := range m.Voucher.Lines {
			if len(line.Batches) == 0 {
				add(oraclePosition{line.Item, line.Godown, ""},
					sign.Mul(oracleQuantity(t, line.Qty)))
				continue
			}
			for _, b := range line.Batches {
				godown := b.Godown
				if godown == "" {
					godown = line.Godown
				}
				add(oraclePosition{line.Item, godown, b.Batch},
					sign.Mul(oracleQuantity(t, b.Qty)))
			}
		}
	}
	if items == 0 {
		t.Fatal("oracle: the fixture declares no STOCKITEM master — this test would prove nothing")
	}
	return out
}

// utf16LEToString decodes UTF-16LE (with or without a BOM) to a Go string.
func utf16LEToString(t *testing.T, b []byte) string {
	t.Helper()
	if len(b) >= 2 && b[0] == 0xff && b[1] == 0xfe {
		b = b[2:]
	}
	if len(b)%2 != 0 {
		t.Fatalf("oracle: %d bytes is not whole UTF-16", len(b))
	}
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, uint16(b[i])|uint16(b[i+1])<<8)
	}
	return string(utf16.Decode(u))
}

// ---------------------------------------------------------------------------
// THE TEST
// ---------------------------------------------------------------------------

// TestTallyPrimeInventoryShapesCorpus imports a TallyPrime export that carries
// GOODS and checks what arrived in the warehouse.
//
// The fixture is in the form Tally writes (UTF-16LE, BOM, CRLF) and carries the
// shapes a real export has and a hand-written one never does:
//
//   - UNIT masters, one of them COMPOUND ("1 BOX = 12 BAG");
//   - GODOWN masters, one of them a CHILD of the other (Tally's locations are a
//     tree; cored's are flat);
//   - a STOCKITEM with a FLAT opening (an untracked position) and another whose
//     opening is stated per (godown, batch);
//   - a stock line whose location lives in BATCHALLOCATIONS.LIST and not on the
//     line — which is where a real export puts it;
//   - one line SPLIT across two warehouses, so the line's own ACTUALQTY must be
//     ignored in favour of the allocations (adding both would double the goods);
//   - a NEGATIVE quantity inside a Purchase (five bags rejected back), which has
//     to become an OUTWARD movement because qty is CHECK (qty > 0);
//   - and the accounting at the TOP LEVEL on one voucher and NESTED in the stock
//     line on the other.
func TestTallyPrimeInventoryShapesCorpus(t *testing.T) {
	pool, ctx, companyID := setup(t)
	path := filepath.Join(repoRoot(t), "corpora", "migration", "07-tallyprime-inventory-shapes.xml")

	ms := masters.NewService(pool)
	im := migration.NewImporter(ms, ledger.NewService(pool))

	res, err := im.ImportFile(ctx, path, "tester")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.SourceCompany != "Goods Trading Co" {
		t.Errorf("source company %q", res.SourceCompany)
	}
	if res.VouchersPosted != 2 {
		t.Fatalf("vouchers posted = %d, want 2", res.VouchersPosted)
	}

	// ---- THE MASTERS THE FILE DECLARED, and the count that says the goods were
	// read at all. StockLines is the ONLY figure in an import report that can
	// distinguish "imported the accounting" from "imported the trade".
	if res.StockLines != 4 {
		t.Errorf("stock movements written = %d, want 4 (RICE out 100; RICE in 20 + 10 across two "+
			"warehouses; WHEAT 5 rejected back) — a stock line that is read and dropped leaves "+
			"the trial balance perfect and the warehouse empty", res.StockLines)
	}
	if res.OpeningPositions != 2 {
		t.Errorf("opening positions written = %d, want 2 (RICE untracked, WHEAT in WAREHOUSE A/W-1) "+
			"— without them the first issue of older goods has nothing to consume and the cost of "+
			"goods sold is understated by the whole opening value", res.OpeningPositions)
	}
	for _, want := range []struct {
		list    []string
		label   string
		members []string
	}{
		{res.StockItemsEnsured, "stock items", []string{"RICE", "WHEAT"}},
		{res.UnitsEnsured, "units", []string{"BAG", "BOX"}},
		{res.GodownsEnsured, "godowns", []string{"WAREHOUSE A", "WAREHOUSE B"}},
		{res.BatchesEnsured, "batches", []string{"RICE/R-1", "RICE/R-2", "WHEAT/W-1"}},
	} {
		if strings.Join(want.list, ",") != strings.Join(want.members, ",") {
			t.Errorf("%s created = %v, want %v", want.label, want.list, want.members)
		}
	}

	// ---- WHAT COULD NOT BE REPRESENTED WAS REPORTED, not swallowed.
	joined := strings.Join(res.Warnings, " | ")
	for _, want := range []struct{ needle, why string }{
		{"COMPOUND unit", "a 'BOX of 12 BAG' unit is imported as the simple symbol BOX; nothing " +
			"in this build can restate a quantity in BAG, and a reader who assumes it can is out " +
			"by the conversion factor"},
		{"locations are FLAT", "WAREHOUSE B was a child of WAREHOUSE A in Tally; the stock is in " +
			"the right place and the hierarchy is gone"},
	} {
		if !strings.Contains(joined, want.needle) {
			t.Errorf("no warning mentioning %q — %s. Warnings were: %s", want.needle, want.why, joined)
		}
	}

	// ---- THE ACCOUNTING STILL BALANCES. This is the control, not the oracle: it
	// passed while the goods were being thrown away, which is the whole reason
	// the rest of this test exists.
	rep := reporting.NewService(pool)
	tb, err := rep.TrialBalance(ctx, companyID, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	if !tb.IsBalanced() {
		t.Fatalf("trial balance not balanced: dr=%s cr=%s", tb.TotalDebit, tb.TotalCredit)
	}

	// ---- ⛔ THE ORACLE. Closing quantity per position, re-derived from the XML
	// by a parser that shares nothing with the importer.
	want := oracleClosingQuantities(t, path)
	sum, err := rep.StockSummary(ctx, companyID, "2026-08-31")
	if err != nil {
		t.Fatalf("stock summary: %v", err)
	}
	got := map[oraclePosition]decimal.Decimal{}
	for _, row := range sum.Rows {
		// ⛔ NO NORMALISATION OF 'Main Location' TO "". The temptation is real —
		// masters.CreateStockItem puts a keyboard-created item's opening in the
		// system location, so an importer that copied it would produce rows there
		// — and folding the two together here would HIDE the exact defect this
		// assertion exists to catch: an opening at Main Location and every issue
		// UNTRACKED are two positions, and mapping both to "" would collapse
		// them into one map entry, the second silently overwriting the first.
		// A position is (item, godown, batch) or it is nothing.
		got[oraclePosition{row.ItemName, row.GodownName, row.BatchName}] = row.ClosingQty
	}

	if len(got) != len(want) {
		t.Errorf("the books hold %d positions and the file describes %d:\n  books: %v\n  file:  %v",
			len(got), len(want), fmtPositions(got), fmtPositions(want))
	}
	for pos, q := range want {
		have, ok := got[pos]
		if !ok {
			t.Errorf("position %v is in the FILE (closing %s) and NOT in the books — that stock "+
				"exists in the trader's warehouse and would be invisible in every report",
				pos, q.StringFixed(4))
			continue
		}
		if !have.Equal(q) {
			t.Errorf("position %v closes at %s in the books and %s in the file — the warehouse is "+
				"out by %s and nothing in the trial balance disagrees",
				pos, have.StringFixed(4), q.StringFixed(4), have.Sub(q).StringFixed(4))
		}
	}
	for pos, q := range got {
		if _, ok := want[pos]; !ok {
			t.Errorf("position %v (closing %s) is in the books and NOT in the file — the import "+
				"INVENTED stock", pos, q.StringFixed(4))
		}
	}

	// ---- AND THE VALUE, hand-derived, because a quantity oracle cannot see a
	// cost. RICE opened 120 @ 90 = 10800 untracked; SI-1 issued 100 at that
	// average cost (9000), leaving 20 @ 90 = 1800. PU-1 received 20 @ 100 into
	// WAREHOUSE A/R-1 (2000) and 10 @ 100 into WAREHOUSE B/R-2 (1000). WHEAT
	// opened 40 for 3600 (a rate of 90 DERIVED from OPENINGVALUE, because the
	// file states no OPENINGRATE) and gave 5 back at 90 (450), leaving 35 @ 90 =
	// 3150. Closing stock 1800 + 2000 + 1000 + 3150 = 7950.
	if wantTotal := decimal.RequireFromString("7950.0000"); !sum.TotalValue.Equal(wantTotal) {
		t.Errorf("closing stock VALUE = %s, want %s. Two ways this goes wrong and both balance: an "+
			"opening whose rate was not derived from OPENINGVALUE carries the stock at nil (that "+
			"alone is 3600 of this figure), and a rejected-goods line posted as an INWARD adds 450 "+
			"instead of removing it", sum.TotalValue.StringFixed(4), wantTotal.StringFixed(4))
	}

	// ---- RE-IMPORTING THE SAME FILE MUST NOT DOUBLE THE WAREHOUSE. The vouchers
	// are deduped on GUID and the openings are not additive — an opening that
	// were re-applied would double the trader's opening stock silently, on every
	// report, for ever.
	second, err := im.ImportFile(ctx, path, "tester")
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if second.VouchersPosted != 0 || second.Skipped != 2 {
		t.Errorf("re-import posted %d and skipped %d, want 0 and 2", second.VouchersPosted, second.Skipped)
	}
	if second.StockLines != 0 || second.OpeningPositions != 0 {
		t.Errorf("re-import wrote %d stock lines and %d openings, want none of either",
			second.StockLines, second.OpeningPositions)
	}
	again, err := rep.StockSummary(ctx, companyID, "2026-08-31")
	if err != nil {
		t.Fatalf("stock summary after re-import: %v", err)
	}
	if !again.TotalValue.Equal(sum.TotalValue) {
		t.Errorf("closing stock moved from %s to %s on a RE-IMPORT of the same file",
			sum.TotalValue.StringFixed(4), again.TotalValue.StringFixed(4))
	}
}

// fmtPositions renders a position map for a failure message, sorted so two runs
// print the same thing.
func fmtPositions(m map[oraclePosition]decimal.Decimal) string {
	out := make([]string, 0, len(m))
	for p, q := range m {
		out = append(out, fmt.Sprintf("%s/%s/%s=%s", p.item, p.godown, p.batch, q.StringFixed(4)))
	}
	sortStrings(out)
	return strings.Join(out, " ")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// noOpeningStockXML is a Day Book export in miniature: one sale of goods the
// file never says were bought, which is what EVERY export without STOCKITEM
// masters looks like. It is UTF-8 on purpose — the encoding is covered by the
// corpus fixture above, and this test is about a refusal.
const noOpeningStockXML = `<ENVELOPE><BODY><IMPORTDATA>
 <REQUESTDESC><REPORTNAME>Day Book</REPORTNAME><STATICVARIABLES>
  <SVCURRENTCOMPANY>No Openings Co</SVCURRENTCOMPANY></STATICVARIABLES></REQUESTDESC>
 <REQUESTDATA>
  <TALLYMESSAGE><LEDGER NAME="CUSTOMER X"><PARENT>Sundry Debtors</PARENT></LEDGER></TALLYMESSAGE>
  <TALLYMESSAGE><LEDGER NAME="SALES X"><PARENT>Sales Accounts</PARENT></LEDGER></TALLYMESSAGE>
  <TALLYMESSAGE>
   <VOUCHER VCHTYPE="Sales" ACTION="Create">
    <DATE>20260801</DATE><GUID>no-open-0001</GUID>
    <VOUCHERTYPENAME>Sales</VOUCHERTYPENAME>
    <PARTYLEDGERNAME>CUSTOMER X</PARTYLEDGERNAME>
    <VOUCHERNUMBER>SX-1</VOUCHERNUMBER>
    <ALLINVENTORYENTRIES.LIST>
     <STOCKITEMNAME>PULSES</STOCKITEMNAME>
     <RATE>50.00/KG</RATE><AMOUNT>2500.00</AMOUNT>
     <ACTUALQTY>50.00 KG</ACTUALQTY><BILLEDQTY>50.00 KG</BILLEDQTY>
     <ACCOUNTINGALLOCATIONS.LIST>
      <LEDGERNAME>SALES X</LEDGERNAME><ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>
      <AMOUNT>2500.00</AMOUNT>
     </ACCOUNTINGALLOCATIONS.LIST>
    </ALLINVENTORYENTRIES.LIST>
    <LEDGERENTRIES.LIST>
     <LEDGERNAME>CUSTOMER X</LEDGERNAME><ISDEEMEDPOSITIVE>Yes</ISDEEMEDPOSITIVE>
     <ISPARTYLEDGER>Yes</ISPARTYLEDGER><AMOUNT>-2500.00</AMOUNT>
    </LEDGERENTRIES.LIST>
   </VOUCHER>
  </TALLYMESSAGE>
 </REQUESTDATA></IMPORTDATA></BODY></ENVELOPE>`

// TestAnExportWithNoOpeningStockIsRefusedWithBothWaysForward is the failure a
// real migration meets FIRST, and the one an importer must not paper over.
//
// ⛔ THE THREE THINGS THIS PINS ARE ALL REFUSALS TO GUESS:
//
//  1. the import ABORTS rather than importing the accounting and silently
//     dropping the goods — which is what it did before this lane, and what makes
//     a Stock Summary empty under a Balance Sheet that carries closing stock;
//  2. it does NOT invent an opening position. "Whatever quantity makes the
//     ledger never go negative" is derivable and is a number nobody stated; it
//     would put stock in a trader's warehouse on this program's authority;
//  3. it does NOT switch the company's allow_negative_stock setting. That is a
//     decision about how the books are kept, and an importer that flips it has
//     changed a rule the operator chose in order to make its own job succeed.
//
// And the message names BOTH ways forward, because the operator cannot be
// expected to know that a Masters.xml carries opening stock and a Day Book does
// not.
func TestAnExportWithNoOpeningStockIsRefusedWithBothWaysForward(t *testing.T) {
	pool, ctx, companyID := setup(t)
	ms := masters.NewService(pool)
	im := migration.NewImporter(ms, ledger.NewService(pool))

	_, err := im.ImportReader(ctx, strings.NewReader(noOpeningStockXML), "tester")
	if err == nil {
		t.Fatal("a sale of 50 KG of stock the file never bought was IMPORTED. Either an opening " +
			"position was invented or the goods were dropped; both leave a trader's warehouse " +
			"describing something that never happened, and neither shows up in a trial balance")
	}
	msg := err.Error()
	for _, want := range []struct{ needle, why string }{
		{"NEGATIVE", "the refusal must name what actually happened"},
		{"PULSES", "and the ITEM, or the operator has 227 stock lines to search"},
		{"SX-1", "and the voucher"},
		{"Masters.xml", "way forward 1: the export that carries opening stock"},
		{"allow negative stock", "way forward 2: the setting, with its cost stated"},
		{"understates the cost of goods sold", "the cost of choosing way 2, stated before it is chosen"},
	} {
		if !strings.Contains(msg, want.needle) {
			t.Errorf("the refusal does not mention %q — %s.\nIt said: %s", want.needle, want.why, msg)
		}
	}

	// ---- AND NOTHING WAS LEFT BEHIND THAT MISLEADS. The voucher is not posted,
	// so the accounting did not land either: each voucher is its own transaction
	// and this one rolled back. ⚠️ The MASTERS it created before the refusal DO
	// remain (they are separate transactions), which is correct — they are facts
	// about the file, not about the trade.
	rep := reporting.NewService(pool)
	sum, err := rep.StockSummary(ctx, companyID, "2026-08-31")
	if err != nil {
		t.Fatalf("stock summary: %v", err)
	}
	if len(sum.Rows) != 0 {
		t.Errorf("the refused import left %d stock position(s) behind: %v", len(sum.Rows), sum.Rows)
	}
	tb, err := rep.TrialBalance(ctx, companyID, "")
	if err != nil {
		t.Fatalf("trial balance: %v", err)
	}
	for _, r := range tb.Rows {
		if strings.EqualFold(r.LedgerName, "SALES X") && !r.Credit.IsZero() {
			t.Errorf("the refused voucher's ACCOUNTING landed anyway (SALES X credit %s) — a "+
				"half-imported voucher is worse than a refused one", r.Credit.StringFixed(4))
		}
	}

	// ---- WAY FORWARD 2, DRIVEN: with negative stock allowed, the same file
	// imports and the shortfall is VISIBLE as a negative position rather than
	// hidden. This is the control that proves the refusal above is the setting
	// working and not the importer being broken.
	adminConn, err := pgx.Connect(ctx, adminDSN)
	if err != nil {
		t.Skipf("admin postgres unavailable: %v", err)
	}
	defer func() { _ = adminConn.Close(ctx) }()
	if _, err := adminConn.Exec(ctx,
		`UPDATE company SET allow_negative_stock = true WHERE id = $1`, companyID); err != nil {
		t.Fatalf("allow negative stock: %v", err)
	}

	res, err := im.ImportReader(ctx, strings.NewReader(noOpeningStockXML), "tester")
	if err != nil {
		t.Fatalf("with negative stock allowed the same file must import: %v", err)
	}
	if res.StockLines != 1 {
		t.Errorf("stock lines = %d, want 1", res.StockLines)
	}
	sum, err = rep.StockSummary(ctx, companyID, "2026-08-31")
	if err != nil {
		t.Fatalf("stock summary: %v", err)
	}
	if len(sum.Rows) != 1 {
		t.Fatalf("want exactly one position, got %d", len(sum.Rows))
	}
	row := sum.Rows[0]
	if want := decimal.RequireFromString("-50.0000"); !row.ClosingQty.Equal(want) {
		t.Errorf("closing quantity %s, want %s — the shortfall is the POINT: a negative position "+
			"is how an operator finds the purchase the export did not carry",
			row.ClosingQty.StringFixed(4), want.StringFixed(4))
	}
	if !row.ClosingValue.IsZero() {
		t.Errorf("the negative position is carried at %s; stock that was never received has no "+
			"cost, and inventing one from the SALE rate is the classic inventory bug",
			row.ClosingValue.StringFixed(4))
	}
}
