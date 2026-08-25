/**
 * §3 — A TALLY EXPORT'S GOODS, IMPORTED AND THEN LOOKED AT IN A BROWSER.
 *
 * ⛔ WHAT THIS ADDS TO THE GO TESTS. internal/migration checks every position
 * against an independent oracle and internal/server checks the same thing over
 * HTTP; both are right and neither can tell you that a migrated trader can SEE
 * their stock. The Stock Summary renders an item tree with per-(godown, batch)
 * positions, and until this lane no imported company had any — every fixture
 * that screen was ever tested against was keyed by hand.
 *
 * So: import a real-shaped export (UTF-16LE, masters, batch allocations) over
 * HTTP, then open the report in Chromium and read the warehouse off the page.
 *
 * ⛔ THE ORACLE IS QUANTITIES AND THEY ARE HAND-DERIVED. TURMERIC opens at
 * 200 KG untracked (16,000 -> a rate of 80.00 derived from OPENINGVALUE, because
 * the file states no rate), 100 KG arrives split 60/40 into two batches of COLD
 * STORE at 90.00, and 50 KG is sold. Three positions: 150 @ 80 = 12,000,
 * 60 @ 90 = 5,400, 40 @ 90 = 3,600. Closing stock 21,000.
 *
 * A build that dropped the batch allocations would show ONE position of the
 * right quantity; a build that dropped the stock lines entirely would show the
 * opening and nothing else, with the trial balance perfect in both cases.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY OF ITS OWN, NOT ON Demo Traders.
 *
 * RUN:  node frontend/scripts/drive-tally-import.mjs
 *       (needs cored :8087 and vite :5173. It creates its own company.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Import Drive ${stamp}`;

/** The export. Identical in shape to the one internal/server posts. */
const DOC = `<ENVELOPE><BODY><IMPORTDATA>
 <REQUESTDESC><REPORTNAME>All Masters</REPORTNAME><STATICVARIABLES>
  <SVCURRENTCOMPANY>Browser Trading Co</SVCURRENTCOMPANY></STATICVARIABLES></REQUESTDESC>
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
    <DATE>20260810</DATE><GUID>drive-v-0001</GUID>
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
    <DATE>20260812</DATE><GUID>drive-v-0002</GUID>
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
 </REQUESTDATA></IMPORTDATA></BODY></ENVELOPE>`;

/** Encode as Gateway of Tally does: UTF-16LE with a BOM. */
const utf16 = (s) => {
  const out = Buffer.alloc(2 + s.length * 2);
  out[0] = 0xff;
  out[1] = 0xfe;
  for (let i = 0; i < s.length; i++) out.writeUInt16LE(s.charCodeAt(i), 2 + i * 2);
  return out;
};

const j = async (method, path, { token, body } = {}) => {
  const r = await fetch(`${CORED}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const text = await r.text();
  let parsed;
  try {
    parsed = text ? JSON.parse(text) : {};
  } catch {
    parsed = { raw: text };
  }
  return { status: r.status, body: parsed };
};

const TOK = (
  await j("POST", "/auth/token", {
    body: { subject: "browser-dev-owner", amr: ["webauthn"] },
  })
).body.token;
const created = await j("POST", "/v1/companies", {
  token: TOK,
  body: { name: COMPANY_NAME, fy_start_date: "2026-04-01", state_code: "29" },
});
const CO = created.body.company_id ?? created.body.id;
if (!CO) throw new Error(`drive-tally-import: no company: ${JSON.stringify(created.body)}`);
console.log(`company ${COMPANY_NAME} = ${CO}`);

// ---- THE IMPORT, over HTTP, in the encoding a real export uses.
const imp = await fetch(`${CORED}/v1/companies/${CO}/import/tally-xml`, {
  method: "POST",
  headers: { "Content-Type": "application/xml", Authorization: `Bearer ${TOK}` },
  body: utf16(DOC),
});
const report = await imp.json();
rec("POST /import/tally-xml accepted a UTF-16LE export", imp.status === 200, `status ${imp.status}`);
rec("it posted both vouchers", report.vouchers_posted === 2, String(report.vouchers_posted));
rec(
  "and the report SAYS how many stock movements it wrote — the only figure that can",
  report.stock_lines === 3,
  `stock_lines=${String(report.stock_lines)} opening_positions=${String(report.opening_positions)}`,
);
rec(
  "the inventory masters it created are named",
  (report.stock_items_ensured ?? []).includes("TURMERIC") &&
    (report.godowns_ensured ?? []).includes("COLD STORE") &&
    (report.batches_ensured ?? []).includes("TURMERIC/T-1"),
  JSON.stringify([report.units_ensured, report.godowns_ensured, report.batches_ensured]),
);

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, openFromGateway } = app;

try {
  // F2 sets the report period; the Stock Summary reads its upper bound.
  await openFromGateway("Stock Summary", 2000);
  await page.waitForTimeout(700);

  const text = await body();
  rec(
    "the imported ITEM is on the screen with its unit",
    /TURMERIC/.test(text) && /KG/.test(text),
  );
  // ⛔ THE POSITIONS. A build that dropped BATCHALLOCATIONS.LIST would render ONE
  // line of the right total quantity; the location and the lot would simply not
  // exist, and nothing on the page would look wrong.
  rec(
    "and its two BATCHES in the named GODOWN, which live in BATCHALLOCATIONS.LIST",
    /COLD STORE/.test(text) && /T-1/.test(text) && /T-2/.test(text),
  );
  const row = async (godown) => {
    const cells = page.getByTestId(`stock-row:TURMERIC:${godown}`).first();
    return (await cells.count()) > 0 ? (await cells.innerText()).replace(/\s+/g, " ") : "";
  };
  const untracked = await row("-");
  const cold = await row("COLD STORE");
  rec(
    "the untracked opening position reads 150.0000 (200 opened, 50 sold)",
    /150\.0000/.test(untracked),
    untracked.slice(0, 120),
  );
  rec(
    "a COLD STORE position reads 60.0000 or 40.0000, not the line's own 100",
    /60\.0000|40\.0000/.test(cold) && !/100\.0000/.test(cold),
    cold.slice(0, 120),
  );
  const total = await page.getByTestId("stock-total-value").first().innerText();
  rec(
    "closing stock on screen is 21,000.00 — the opening rate was DERIVED from OPENINGVALUE",
    total.trim() === "21,000.00",
    total.trim(),
  );

  // The Balance Sheet's Closing Stock comes off the same derivation, so the two
  // statements must agree — and a migrated company's sheet is the first thing an
  // accountant looks at.
  const bs = await j("GET", `/v1/companies/${CO}/balance-sheet?as_on=2026-08-31`, { token: TOK });
  rec(
    "the Balance Sheet carries the same closing stock and balances",
    JSON.stringify(bs.body).includes("21000.0000") && bs.body.balanced === true,
    `balanced=${String(bs.body.balanced)}`,
  );
} finally {
  console.log("\n--- console errors ---");
  for (const e of app.consoleErrors.slice(0, 8)) console.log("  " + e);
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);
