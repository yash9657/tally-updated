package server

// handler_import_inventory_test.go — A TALLY EXPORT'S GOODS, ARRIVING THROUGH
// THE FRONT DOOR (feature §3).
//
// ⛔⛔ WHY THIS EXISTS BESIDE THE IMPORTER'S OWN TEST. internal/migration already
// imports a corpus fixture and checks every position against an independent
// oracle. It calls the Go service. An operator posts a file to a URL — and this
// lane's own predecessor is the reason that distinction is not pedantry: the
// payroll module was complete, tested and mounted, and could not pay anybody,
// because the two writers a pay run needs had no route and every test reached
// past the seam that was missing.
//
// So this test performs THE WHOLE TASK over HTTP: POST the export, then read the
// warehouse back out of GET /stock-summary. Nothing here touches
// migration.Importer, masters.Service or reporting.Service directly.
//
// ⛔ AND THE ORACLE IS QUANTITIES, because a trial balance cannot see this
// defect: the rupee value of a trade is in the ledger entries, so an import that
// reads every stock line and throws it away produces books that BALANCE and a
// Stock Summary with nothing in it. That is exactly what this route did until
// the importer was taught the goods, and it is why the response now carries
// stock_lines.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// inventoryImportDoc is a Tally export carrying MASTERS AND GOODS: a unit, a
// location, an item with an opening position, and one purchase that receives
// into two batches.
//
// ⚠️ THE OPENING IS WHAT MAKES THE SALE POSSIBLE. Without a STOCKITEM master
// there is no opening stock, and the first sale of goods bought before the export
// window is refused — see the migration package's
// TestAnExportWithNoOpeningStockIsRefusedWithBothWaysForward. A file that carries
// its masters is the coherent case and the one an operator should be given.
const inventoryImportDoc = `<ENVELOPE><BODY><IMPORTDATA>
 <REQUESTDESC><REPORTNAME>All Masters</REPORTNAME><STATICVARIABLES>
  <SVCURRENTCOMPANY>Wire Trading Co</SVCURRENTCOMPANY></STATICVARIABLES></REQUESTDESC>
 <REQUESTDATA>
  <TALLYMESSAGE><UNIT NAME="KG"><SYMBOL>KG</SYMBOL><ISSIMPLEUNIT>Yes</ISSIMPLEUNIT></UNIT></TALLYMESSAGE>
  <TALLYMESSAGE><GODOWN NAME="COLD STORE"><PARENT/></GODOWN></TALLYMESSAGE>
  <TALLYMESSAGE>
   <STOCKITEM NAME="TURMERIC">
    <BASEUNITS>KG</BASEUNITS>
    <OPENINGBALANCE>200.00 KG</OPENINGBALANCE>
    <OPENINGVALUE>16000.00</OPENINGVALUE>
   </STOCKITEM>
  </TALLYMESSAGE>
  <TALLYMESSAGE><LEDGER NAME="WIRE CUSTOMER"><PARENT>Sundry Debtors</PARENT></LEDGER></TALLYMESSAGE>
  <TALLYMESSAGE><LEDGER NAME="WIRE SALES"><PARENT>Sales Accounts</PARENT></LEDGER></TALLYMESSAGE>
  <TALLYMESSAGE><LEDGER NAME="WIRE SUPPLIER"><PARENT>Sundry Creditors</PARENT></LEDGER></TALLYMESSAGE>
  <TALLYMESSAGE><LEDGER NAME="WIRE PURCHASES"><PARENT>Purchase Accounts</PARENT></LEDGER></TALLYMESSAGE>
  <TALLYMESSAGE>
   <VOUCHER VCHTYPE="Purchase" ACTION="Create">
    <DATE>20260810</DATE><GUID>wire-v-0001</GUID>
    <VOUCHERTYPENAME>Purchase</VOUCHERTYPENAME>
    <PARTYLEDGERNAME>WIRE SUPPLIER</PARTYLEDGERNAME>
    <VOUCHERNUMBER>WP-1</VOUCHERNUMBER>
    <ALLINVENTORYENTRIES.LIST>
     <STOCKITEMNAME>TURMERIC</STOCKITEMNAME>
     <RATE>90.00/KG</RATE><AMOUNT>-9000.00</AMOUNT>
     <ACTUALQTY>100.00 KG</ACTUALQTY><BILLEDQTY>100.00 KG</BILLEDQTY>
     <BATCHALLOCATIONS.LIST>
      <GODOWNNAME>COLD STORE</GODOWNNAME><BATCHNAME>T-1</BATCHNAME>
      <ACTUALQTY>60.00 KG</ACTUALQTY><AMOUNT>-5400.00</AMOUNT>
     </BATCHALLOCATIONS.LIST>
     <BATCHALLOCATIONS.LIST>
      <GODOWNNAME>COLD STORE</GODOWNNAME><BATCHNAME>T-2</BATCHNAME>
      <ACTUALQTY>40.00 KG</ACTUALQTY><AMOUNT>-3600.00</AMOUNT>
     </BATCHALLOCATIONS.LIST>
    </ALLINVENTORYENTRIES.LIST>
    <LEDGERENTRIES.LIST>
     <LEDGERNAME>WIRE PURCHASES</LEDGERNAME><ISDEEMEDPOSITIVE>Yes</ISDEEMEDPOSITIVE>
     <AMOUNT>-9000.00</AMOUNT>
    </LEDGERENTRIES.LIST>
    <LEDGERENTRIES.LIST>
     <LEDGERNAME>WIRE SUPPLIER</LEDGERNAME><ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>
     <ISPARTYLEDGER>Yes</ISPARTYLEDGER><AMOUNT>9000.00</AMOUNT>
    </LEDGERENTRIES.LIST>
   </VOUCHER>
  </TALLYMESSAGE>
  <TALLYMESSAGE>
   <VOUCHER VCHTYPE="Sales" ACTION="Create">
    <DATE>20260812</DATE><GUID>wire-v-0002</GUID>
    <VOUCHERTYPENAME>Sales</VOUCHERTYPENAME>
    <PARTYLEDGERNAME>WIRE CUSTOMER</PARTYLEDGERNAME>
    <VOUCHERNUMBER>WS-1</VOUCHERNUMBER>
    <ALLINVENTORYENTRIES.LIST>
     <STOCKITEMNAME>TURMERIC</STOCKITEMNAME>
     <RATE>150.00/KG</RATE><AMOUNT>7500.00</AMOUNT>
     <ACTUALQTY>50.00 KG</ACTUALQTY><BILLEDQTY>50.00 KG</BILLEDQTY>
     <ACCOUNTINGALLOCATIONS.LIST>
      <LEDGERNAME>WIRE SALES</LEDGERNAME><ISDEEMEDPOSITIVE>No</ISDEEMEDPOSITIVE>
      <AMOUNT>7500.00</AMOUNT>
     </ACCOUNTINGALLOCATIONS.LIST>
    </ALLINVENTORYENTRIES.LIST>
    <LEDGERENTRIES.LIST>
     <LEDGERNAME>WIRE CUSTOMER</LEDGERNAME><ISDEEMEDPOSITIVE>Yes</ISDEEMEDPOSITIVE>
     <ISPARTYLEDGER>Yes</ISPARTYLEDGER><AMOUNT>-7500.00</AMOUNT>
    </LEDGERENTRIES.LIST>
   </VOUCHER>
  </TALLYMESSAGE>
 </REQUESTDATA></IMPORTDATA></BODY></ENVELOPE>`

// TestATallyImportOverHTTPFillsTheWarehouse is §3 through the public surface.
//
//	POST /import/tally-xml  (UTF-16LE, as Gateway of Tally writes it)
//	GET  /stock-summary     — and the goods are there, per position
func TestATallyImportOverHTTPFillsTheWarehouse(t *testing.T) {
	s := payrollServer(t)
	f := newSpellingFixture(t, s)

	// ⛔ UTF-16LE, because that is what a real export is (defect #1 of the seven
	// T32 found) and because door 7 of the back-dated cutoff survived three
	// thorough suites for the single reason that every test body in all three was
	// UTF-8. An import test that posts UTF-8 is not testing the import.
	rec, body := importXMLBytesJSON(t, s, f.base, f.ownerTok, tallyUTF16(inventoryImportDoc))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /import/tally-xml = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if n := jsonNumber(body["vouchers_posted"]); n != 2 {
		t.Fatalf("vouchers_posted = %v, want 2 (%s)", body["vouchers_posted"], rec.Body.String())
	}

	// ---- THE COUNT THAT MAKES THE DEFECT VISIBLE ON THE WIRE. Before this lane
	// the response could not say whether any goods arrived, and the answer was
	// none.
	if n := jsonNumber(body["stock_lines"]); n != 3 {
		t.Errorf("stock_lines = %v, want 3 (60 KG + 40 KG in, 50 KG out). This is the ONLY figure "+
			"in this response that distinguishes 'imported the accounting' from 'imported the "+
			"trade' — a trial balance cannot tell them apart", body["stock_lines"])
	}
	if n := jsonNumber(body["opening_positions"]); n != 1 {
		t.Errorf("opening_positions = %v, want 1. Zero here on a file with stock lines means the "+
			"export carried no opening stock, and the operator has to be able to see that",
			body["opening_positions"])
	}
	for _, tc := range []struct{ key, want string }{
		{"stock_items_ensured", "TURMERIC"},
		{"units_ensured", "KG"},
		{"godowns_ensured", "COLD STORE"},
		{"batches_ensured", "TURMERIC/T-1"},
	} {
		list, _ := body[tc.key].([]any)
		var found bool
		for _, v := range list {
			if str, _ := v.(string); str == tc.want {
				found = true
			}
		}
		if !found {
			t.Errorf("%s = %v, want it to contain %q — the masters were INFERRED from the "+
				"vouchers and the file, and a caller that cannot see what was created cannot "+
				"review the migration", tc.key, list, tc.want)
		}
	}

	// ---- THE WAREHOUSE, READ BACK THROUGH THE PRODUCT'S OWN REPORT.
	//
	// TURMERIC: opening 200 KG untracked at 80.00 (a rate DERIVED from
	// OPENINGVALUE 16000, because the file states no OPENINGRATE), less the 50 KG
	// sale = 150 KG at 80 = 12,000. Received 60 KG into COLD STORE/T-1 at 90 =
	// 5,400 and 40 KG into COLD STORE/T-2 at 90 = 3,600. Three positions,
	// 250 KG, 21,000.
	rec, sum := aclRequest(t, s, http.MethodGet, f.base+"/stock-summary?as_on=2026-08-31", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /stock-summary = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	rows, _ := sum["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("the Stock Summary holds %d positions, want 3 (untracked opening, COLD STORE/T-1, "+
			"COLD STORE/T-2). An import that dropped its batch allocations would show ONE, and its "+
			"quantity would still be right (%s)", len(rows), rec.Body.String())
	}
	got := map[string]string{}
	for _, r := range rows {
		row, _ := r.(map[string]any)
		item, _ := row["item"].(string)
		godown, _ := row["godown"].(string)
		batch, _ := row["batch"].(string)
		qty, _ := row["closing_qty"].(string)
		got[item+"|"+godown+"|"+batch] = qty
	}
	for _, tc := range []struct{ key, qty, why string }{
		{"TURMERIC||", "150.0000",
			"the opening of 200 less the 50 sold — an UNTRACKED position, because the file's own " +
				"sale names no location and an opening put at 'Main Location' would be a DIFFERENT " +
				"position that the sale could never consume"},
		{"TURMERIC|COLD STORE|T-1", "60.0000",
			"the first batch allocation of the purchase; the location is in BATCHALLOCATIONS.LIST " +
				"and not on the line, which is where a real export puts it"},
		{"TURMERIC|COLD STORE|T-2", "40.0000",
			"the second — and the line's own ACTUALQTY of 100 must NOT also be posted, or the " +
				"warehouse holds 100 KG that never arrived"},
	} {
		if got[tc.key] != tc.qty {
			t.Errorf("position %s closes at %q, want %s — %s", tc.key, got[tc.key], tc.qty, tc.why)
		}
	}
	if v, _ := sum["total_value"].(string); v != "21000.0000" {
		t.Errorf("closing stock value = %q, want 21000.0000. An opening whose rate was not derived "+
			"from OPENINGVALUE would carry 200 KG at NIL and this figure would read 9000.0000 — "+
			"with every quantity above still correct", v)
	}

	// ---- AND THE BOOKS STILL BALANCE. The control, not the oracle: it passed
	// while the goods were being dropped.
	rec, tb := aclRequest(t, s, http.MethodGet, f.base+"/trial-balance", f.ownerTok, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /trial-balance = %d (%s)", rec.Code, rec.Body.String())
	}
	if tb["balanced"] != true {
		t.Errorf("the trial balance does not balance after the import: %s", rec.Body.String())
	}
}

// jsonNumber reads a JSON number out of a decoded body. encoding/json gives
// float64; a helper keeps the assertions above about the figure rather than
// about the decoding.
func jsonNumber(v any) int {
	f, ok := v.(float64)
	if !ok {
		return -1
	}
	return int(f)
}

// importXMLBytesJSON posts a raw export body and decodes the JSON report.
// importXMLBytes (the stored-date suite's) returns only the recorder, because
// every test there asserts a STATUS; this lane asserts what the report SAYS.
func importXMLBytesJSON(t *testing.T, s *Server, base, tok string, body []byte) (
	*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	rec := importXMLBytes(t, s, base, tok, body)
	out := map[string]any{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("import response is not JSON: %v (%s)", err, rec.Body.String())
		}
	}
	return rec, out
}
