/**
 * L28 — ROWS 20 / 23 / 26 OF FeaturesScreen, RE-DRIVEN AGAINST T9 AND T10.
 *
 * ⛔ WHY THESE THREE ROWS, AND WHY NOW
 * ===========================================================================
 * All three were last driven BEFORE the two waves that changed the ground under
 * them, and each one's standing evidence is now either stale on its face or was
 * never a committed script at all:
 *
 *   ROW 20  "Stock items and units of measure (masters)" — evidence is
 *           drive-l13-masters.mjs ("a Kg item is created and reads back as Kg").
 *           Since then migration 0079 added TWO fields to the unit form
 *           (base_unit_id, conversion_factor — a both-or-neither CHECK) and ONE
 *           to the item form (stock_group_id). drive-stock-groups.mjs only ever
 *           creates a COMPOUND unit and a GROUPED item, so the SIMPLE path —
 *           the one an operator takes forty times a day — has not been driven
 *           since the fields landed.
 *
 *   ROW 23  "Stock Summary (closing quantity AND value)" — evidence is
 *           drive-l10-stock.mjs, which asserts `headers.length === 11`. The
 *           screen has THIRTEEN headers today (T9 added "Basis", T10 added
 *           "Closing Qty (base unit)"), so that assertion is stale on its face.
 *           And the row's own note still says value is derived "through a
 *           weighted-average valuer", which since 0078 is per-item and may be
 *           FIFO or LIFO.
 *
 *   ROW 26  "Inventory vouchers (Delivery Note, Receipt Note, Stock Journal,
 *           Physical Stock)" — there is NO committed drive script for Alt+F7 or
 *           Alt+F10 anywhere in frontend/scripts. Its only evidence is the
 *           hand-driven 2026-08-07 table rows that found LEAD 2 and LEAD 3.
 *           Every one of those observations predates inventory/lots.go entirely.
 *
 * ⛔ WHAT THIS DRIVE REACHES THAT drive-valuation.mjs AND drive-stock-groups.mjs
 * CANNOT: both of those drive only their OWN row, and both post every voucher
 * over HTTP as an ordinary purchase. Neither has ever keyed an INVENTORY
 * VOUCHER, so the intersection — a Delivery Note against a FIFO lot queue, a
 * Stock Journal between two godowns of a FIFO item, a Physical Stock count on a
 * COMPOUND-unit item — is untested ground in both directions.
 *
 * ⛔ THE ORACLES ARE HAND-DERIVED FROM 0078's OWN WORKED EXAMPLE, NEVER READ
 * BACK FROM THE THING UNDER TEST. Three items, IDENTICAL movements (buy 10 @
 * 100, buy 10 @ 120, issue 5), three different bases:
 *
 *     Avg Widget  (WEIGHTED_AVG)  outward 550.00   closing 1,650.00
 *     Fifo Widget (FIFO)          outward 500.00   closing 1,700.00
 *     Lifo Widget (LIFO)          outward 600.00   closing 1,600.00
 *
 * ⚠️⚠️ AND NOTE WHAT THE COMPANY TOTAL CANNOT TELL YOU: 1,650 + 1,700 + 1,600 =
 * 4,950 = 3 x 1,650. The mixed-basis total is EXACTLY the all-average total, to
 * the paisa. A build that stored all three methods and valued every item at
 * weighted average would print a Balance Sheet that foots, a Stock Summary whose
 * footer is right, and three item lines that are each wrong. That collision is
 * deliberate and it is the point: on a mixed company the per-ITEM lines are the
 * only oracle, and every assertion in §4 below is written per item.
 *
 * ⛔ EVERY ASSERTION IS PHRASED SO THE CORRECT BEHAVIOUR IS THE PASS. In
 * particular §6's Stock Journal arm passes when the transfer CONSERVES VALUE; if
 * it goes red, the finding is that it does not, and the weighted-average arm
 * beside it is the control that makes the failure attributable to the costing
 * method rather than to transfers as such.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY AND REAPS IT. L27 leaked a tenant twice for
 * want of exactly this, and drive-stock-groups.mjs (whose setup idiom this
 * copies) does not reap its own. The teardown is the catalog-derived one from
 * drive-u1-voided-vouchers.mjs, in a try/finally, and it prints clean/LEAKED.
 *
 * RUN:  node frontend/scripts/drive-l28-stock-restale.mjs
 *       (needs cored :8087, postgres :5433 and vite :5173 — the port matters,
 *        WebAuthn origin matching is exact. It creates its own company.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Stale Stock Rows Drive ${stamp}`;
const DE_SUBJECT = `l28-dataentry-${stamp}`;
const DE_PASSWORD = "tally-l28-2026";

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

const sql = async (query) => {
  const { execFile } = await import("node:child_process");
  return new Promise((res, rej) => {
    execFile(
      "psql",
      ["-h", "127.0.0.1", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", query],
      { env: { ...process.env, PGPASSWORD: "postgres" } },
      (err, stdout) => (err ? rej(err) : res(stdout.trim())),
    );
  });
};
const q = (s) => `'${String(s).replace(/'/g, "''")}'`;

// ===========================================================================
// SETUP. The tenant, the four ledgers, the SECOND GODOWN and the Data Entry
// member, over HTTP as the same subject the browser signs in as.
//
// ⚠️ THE PURCHASES AND THE SALE ARE POSTED OVER HTTP ON PURPOSE — they are not
// what these three rows are about, and keying six item invoices through the grid
// would make a valuation failure indistinguishable from a voucher-entry
// failure. THE MASTERS AND ALL FOUR INVENTORY VOUCHERS ARE KEYED IN THE BROWSER,
// because they ARE the feature under test.
//
// ⛔ THE SECOND GODOWN IS MINTED OVER HTTP AND NOT KEYED, for one reason only:
// there is no "Create Godown" entry in MASTER_GOTO_ENTRIES (navMachine.ts lists
// five master kinds and a godown is not one of them), so no keyboard route to
// one exists. That is itself worth knowing and is reported, not asserted here.
// ===========================================================================
const TOK = (
  await j("POST", "/auth/token", {
    body: { subject: "browser-dev-owner", amr: ["webauthn"] },
  })
).body.token;
const created = await j("POST", "/v1/companies", {
  token: TOK,
  body: { name: COMPANY_NAME, fy_start_date: "2026-04-01", state_code: "29" },
});
if (created.status !== 201 && created.status !== 200) {
  throw new Error(
    `drive-l28: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
console.log(`company ${COMPANY_NAME} = ${CO}`);

const base = `/v1/companies/${CO}`;
const ledger = async (name, group) =>
  (
    await j("POST", `${base}/ledgers`, {
      token: TOK,
      body: { name, group_name: group, created_by: "drive" },
    })
  ).body.ledger_id;
const SUPPLIER = await ledger("Drive Supplier", "Sundry Creditors");
const PURCHASES = await ledger("Drive Purchases", "Purchase Accounts");
const CUSTOMER = await ledger("Drive Customer", "Sundry Debtors");
const SALES = await ledger("Drive Sales", "Sales Accounts");
const GODOWNS = (await j("GET", `${base}/godowns`, { token: TOK })).body.godowns;
const MAIN = GODOWNS.find((g) => g.is_system);
const SECOND_NAME = "Second Store";
const secondCreated = await j("POST", `${base}/godowns`, {
  token: TOK,
  body: { name: SECOND_NAME, created_by: "drive" },
});
if (secondCreated.status !== 201) {
  throw new Error(`drive-l28: could not create the second godown: ${secondCreated.status}`);
}

// ---- The Data Entry member for §5. A FIRST password only exists on the dev
// token path (auth_login.go discloses this); an invite is not a membership until
// GET /v1/me converts it (that is D25), so the claim is made here — §5 is about
// what the level may READ, not about how it joins.
{
  const t = (await j("POST", "/auth/token", { body: { subject: DE_SUBJECT, amr: ["webauthn"] } }))
    .body.token;
  const set = await j("POST", "/auth/password", {
    token: t,
    body: { current_password: "", new_password: DE_PASSWORD },
  });
  if (set.status !== 204) {
    throw new Error(`drive-l28: could not set ${DE_SUBJECT}'s first password: ${set.status}`);
  }
  const inv = await j("POST", `${base}/members/invites`, {
    token: TOK,
    body: {
      subject: DE_SUBJECT,
      email: `${DE_SUBJECT}@example.test`,
      security_level_name: "Data Entry",
    },
  });
  if (inv.status !== 201 && inv.status !== 200) {
    throw new Error(`drive-l28: could not invite ${DE_SUBJECT}: ${inv.status}`);
  }
  const me = await j("GET", "/v1/me", {
    token: (await j("POST", "/auth/token", { body: { subject: DE_SUBJECT, amr: ["webauthn"] } }))
      .body.token,
  });
  if (me.status !== 200) throw new Error(`drive-l28: ${DE_SUBJECT} could not claim its invite`);
}

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, altMac, toGateway, openFromGateway, net } = app;

const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const text = async (t) =>
  (await seen(t)) ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim() : "";
const type = async (t, v) => {
  const el = testid(t).first();
  await el.click();
  await el.fill(v);
};
const choose = async (t, v) => {
  await testid(t).first().selectOption(v);
  await page.waitForTimeout(150);
};

/** Alt+G, type the label, Enter — the documented Go To route. */
const goTo = async (label, wait = 1400) => {
  await toGateway();
  await altMac("g");
  await page.keyboard.type(label);
  await page.waitForTimeout(400);
  await press("Enter", wait);
};

/** Type into an as-you-type SelectionList and pick the exact match. */
const pickFromList = async (name, value) => {
  const combo = page.getByRole("combobox", { name }).first();
  await combo.click();
  await combo.fill(value);
  await page.waitForTimeout(300);
  await page.getByText(value, { exact: true }).first().click();
  await page.waitForTimeout(150);
};

/** The status line is the SHELL's, so it survives the create screen unmounting. */
const status = async () => text("status-line");

const openStockSummary = async () => {
  await openFromGateway("Stock Summary", 2500);
  await page.waitForTimeout(800);
};

/**
 * Every cell of one ITEM TOTAL row of the Stock Summary, in header order.
 * 0 Item · 1 Unit · 2 Basis · 3 Opening Qty · 4 Opening Value · 5 Inward Qty ·
 * 6 Inward Value · 7 Outward Qty · 8 Outward Value (at cost) · 9 Closing Qty ·
 * 10 Closing Qty (base unit) · 11 Rate · 12 Closing Value.
 */
const itemCells = async (name) =>
  (await page.locator(`[data-testid="stock-item:${name}"] td`).allInnerTexts()).map((c) =>
    c.replace(/\s+/g, " ").trim(),
  );

/** The same, for one (item, godown) POSITION row — where the Rate really lives. */
const positionCells = async (item, godown) =>
  (
    await page.locator(`[data-testid="stock-row:${item}:${godown}"] td`).allInnerTexts()
  ).map((c) => c.replace(/\s+/g, " ").trim());

let driveError = null;
const stockVoucherIDs = [];

try {
  // =========================================================================
  // §1 — ROW 20. THE SIMPLE PATH STILL WORKS AFTER T10 PUT THREE NEW FIELDS ON
  //      THE TWO MASTER FORMS.
  //
  // ⛔ THIS IS THE ARM NO EXISTING DRIVE COVERS. drive-stock-groups.mjs creates
  // a COMPOUND unit and a GROUPED item and nothing else; drive-l13-masters.mjs
  // predates all three fields. `unit_compound_both_or_neither` (0079) is a CHECK,
  // so a form that defaulted ONE half of the conversion would not fail quietly —
  // it would hand the operator a SQLSTATE on the one master they key most often.
  // =========================================================================
  await goTo("Create Unit");
  rec("ROW 20 · Alt+G > Create Unit opens the unit master", await seen("master-create:unit"));
  await type("master-name", "KGX");
  // ⛔ NOTHING ELSE IS TOUCHED. Base unit and Conversion factor are left exactly
  // as the form offers them, because that is what "create a simple unit" means.
  await press("Control+a", 1800);
  rec(
    "ROW 20 · a SIMPLE unit is created with the two T10 fields untouched",
    /created/i.test(await status()) && !/Nothing was created/i.test(await status()),
    (await status()).slice(0, 140),
  );
  // ⛔ POSTGRES IS THE ORACLE, and it is the both-or-neither CHECK that makes it
  // load-bearing: a form that sent a factor of 1 with no base, or a base with no
  // factor, is refused by the DATABASE, so "created" on the status line is not
  // enough — the shape of the row is the claim.
  const simpleUnit = await sql(
    `SELECT COALESCE(base_unit_id::text, 'NULL') || '/' || COALESCE(conversion_factor::text, 'NULL')
       FROM unit WHERE company_id = ${q(CO)} AND symbol = ${q("KGX")}`,
  );
  rec(
    "ROW 20 · unit.base_unit_id AND conversion_factor are BOTH NULL in POSTGRES (0079's both-or-neither CHECK)",
    simpleUnit === "NULL/NULL",
    simpleUnit || "(no row)",
  );

  // ---- and the item that uses it, with NO group and NO costing method keyed.
  await goTo("Create Stock Item");
  rec(
    "ROW 20 · Alt+G > Create Stock Item opens the item master",
    await seen("master-create:stock-item"),
  );
  await type("master-name", "Simple Kg");
  await pickFromList("Units", "KGX");
  await press("Control+a", 1800);
  const simpleItem = await sql(
    `SELECT COALESCE(u.symbol,'(none)') || '/' || COALESCE(si.stock_group_id::text,'NULL') || '/' || si.valuation_method
       FROM stock_item si LEFT JOIN unit u ON u.id = si.unit_id
      WHERE si.company_id = ${q(CO)} AND si.name = ${q("Simple Kg")}`,
  );
  // ⛔ THE UNIT READ-BACK IS L13's OWN ORACLE and it is kept verbatim: the defect
  // it was written for was an item silently filed as "Nos" whatever was keyed.
  rec(
    'ROW 20 · the item reads back in the unit that was KEYED, ungrouped, at the server default — never a silent "Nos"',
    simpleItem === "KGX/NULL/WEIGHTED_AVG",
    simpleItem || "(no row)",
  );

  // ---- ⭐ THE CONTROL THAT PROVES THE ORACLE CAN FAIL. A probe that read the
  // unit off the wrong row, or off a cached master list, passes the assertion
  // above vacuously. Two items in two units, read back distinct, is what makes
  // that read-back mean something.
  await goTo("Create Stock Item");
  await type("master-name", "Simple Nos");
  await pickFromList("Units", "Nos");
  await press("Control+a", 1800);
  const twoUnits = await sql(
    `SELECT string_agg(si.name || '=' || COALESCE(u.symbol,'(none)'), ',' ORDER BY si.name)
       FROM stock_item si LEFT JOIN unit u ON u.id = si.unit_id
      WHERE si.company_id = ${q(CO)} AND si.name LIKE 'Simple %'`,
  );
  rec(
    "ROW 20 · CONTROL: two items keyed in two different units read back DIFFERENT symbols",
    twoUnits === "Simple Kg=KGX,Simple Nos=Nos",
    twoUnits,
  );

  // =========================================================================
  // §2 — THE GROUND ROW 23 NEEDS: a two-level group tree, a compound unit, and
  //      three items on three different bases. All keyed, none posted.
  // =========================================================================
  await goTo("Create Stock Group");
  await type("master-name", "Provisions");
  await press("Control+a", 1800);
  await goTo("Create Stock Group");
  await type("master-name", "Grains");
  await pickFromList("Under (parent stock group)", "Provisions");
  await press("Control+a", 1800);
  const tree = await sql(
    `SELECT COALESCE(p.name,'(top)') FROM stock_group g LEFT JOIN stock_group p ON p.id = g.parent_id
      WHERE g.company_id = ${q(CO)} AND g.name = ${q("Grains")}`,
  );
  rec("ROW 20 · a CHILD stock group is filed under the parent that was keyed", tree === "Provisions", tree);

  await goTo("Create Unit");
  await type("master-name", "BOXX");
  await type("master-unit-decimals", "0");
  await page.waitForTimeout(400);
  await choose("master-unit-base", "Nos");
  await type("master-unit-conversion", "12");
  await press("Control+a", 1800);
  const compound = await sql(
    `SELECT COALESCE(b.symbol,'(none)') || '/' || COALESCE(u.conversion_factor::text,'NULL')
       FROM unit u LEFT JOIN unit b ON b.id = u.base_unit_id
      WHERE u.company_id = ${q(CO)} AND u.symbol = ${q("BOXX")}`,
  );
  rec(
    "ROW 20 · a COMPOUND unit stores both halves: 1 BOXX = 12 Nos",
    /^Nos\/12(\.0+)?$/.test(compound),
    compound || "(no row)",
  );

  /** Key one stock item through the form, with its group and its costing method. */
  const createItem = async (name, unit, group, method) => {
    await goTo("Create Stock Item");
    if (!(await seen("master-create:stock-item"))) {
      throw new Error(`Alt+G > Create Stock Item did not open (${name})`);
    }
    await type("master-name", name);
    await pickFromList("Units", unit);
    await page.waitForTimeout(400);
    if (group) await choose("master-item-stock-group", group);
    if (method) await choose("master-item-valuation-method", method);
    await press("Control+a", 1800);
  };
  await createItem("Avg Widget", "Nos", "", "");
  await createItem("Fifo Widget", "Nos", "Grains", "FIFO");
  await createItem("Lifo Widget", "BOXX", "Provisions", "LIFO");
  const filed = await sql(
    `SELECT string_agg(si.name || '=' || COALESCE(g.name,'') || ':' || si.valuation_method, ',' ORDER BY si.name)
       FROM stock_item si LEFT JOIN stock_group g ON g.id = si.stock_group_id
      WHERE si.company_id = ${q(CO)} AND si.name LIKE '% Widget'`,
  );
  rec(
    "ROW 20 · all three items filed as keyed — group AND costing method, including the UNGROUPED one",
    filed === "Avg Widget=:WEIGHTED_AVG,Fifo Widget=Grains:FIFO,Lifo Widget=Provisions:LIFO",
    filed,
  );

  // =========================================================================
  // §3 — THE MOVEMENTS, over HTTP. IDENTICAL on all three items: buy 10 @ 100,
  //      buy 10 @ 120, issue 5 at a SALE price of 200 — a price far from every
  //      cost, so a valuer consuming at the sale rate is caught by all three.
  // =========================================================================
  const itemID = async (name) =>
    sql(`SELECT id::text FROM stock_item WHERE company_id = ${q(CO)} AND name = ${q(name)}`);
  const IDS = {
    "Avg Widget": await itemID("Avg Widget"),
    "Fifo Widget": await itemID("Fifo Widget"),
    "Lifo Widget": await itemID("Lifo Widget"),
  };
  let seq = 0;
  for (const name of Object.keys(IDS)) {
    for (const [date, qty, rate, amount] of [
      ["2026-04-01", "10", "100", "1000"],
      ["2026-04-05", "10", "120", "1200"],
    ]) {
      seq += 1;
      const r = await j("POST", `${base}/vouchers/purchase`, {
        token: TOK,
        body: {
          voucher_no: `PUR-${String(seq)}`,
          voucher_date: date,
          created_by: "drive",
          postings: [
            { ledger_id: PURCHASES, dr_cr: "D", amount },
            { ledger_id: SUPPLIER, dr_cr: "C", amount },
          ],
          inventory: [
            {
              stock_item_id: IDS[name],
              godown_id: MAIN.id,
              batch_id: "",
              direction: "IN",
              qty,
              rate,
              amount,
            },
          ],
        },
      });
      if (r.status !== 201) throw new Error(`PUR-${String(seq)}: ${r.status} ${JSON.stringify(r.body)}`);
    }
    const s = await j("POST", `${base}/vouchers/sales`, {
      token: TOK,
      body: {
        voucher_no: `INV-${name.slice(0, 4)}`,
        voucher_date: "2026-04-10",
        created_by: "drive",
        postings: [
          { ledger_id: CUSTOMER, dr_cr: "D", amount: "1000" },
          { ledger_id: SALES, dr_cr: "C", amount: "1000" },
        ],
        inventory: [
          {
            stock_item_id: IDS[name],
            godown_id: MAIN.id,
            batch_id: "",
            direction: "OUT",
            qty: "5",
            rate: "200",
            amount: "1000",
          },
        ],
      },
    });
    if (s.status !== 201) throw new Error(`INV ${name}: ${s.status} ${JSON.stringify(s.body)}`);
  }

  // =========================================================================
  // §4 — ROW 23. THE MIXED-BASIS REPORT, WHICH NOTHING HAS EVER DRIVEN.
  //      drive-valuation.mjs checks a SINGLE-item FIFO company; a mixed company
  //      is where a per-item valuer selection can go wrong and still foot.
  // =========================================================================
  await openStockSummary();
  rec("ROW 23 · the Stock Summary opens", await seen("stock-summary-table"));

  // ---- THE COLUMN SET. drive-l10-stock.mjs asserts eleven; there are thirteen.
  const headers = await page
    .locator('[data-testid="stock-summary-table"] thead th')
    .allInnerTexts();
  rec(
    "ROW 23 · the report carries the two columns T9 and T10 added — Basis and Closing Qty (base unit)",
    headers.includes("Basis") && headers.includes("Closing Qty (base unit)"),
    `${String(headers.length)} headers: ${headers.join(" / ")}`,
  );
  // ⛔ StockSummaryScreen argues at length that BOTH footers (this table's
  // colSpan and toCsv's slice) put the company total in the FINAL cell, so a
  // column appended after Closing Value files closing stock under another
  // heading. That invariant is the assertion, not the count.
  rec(
    "ROW 23 · `Closing Value` is still the LAST column — both footers put the company total in the final cell",
    headers[headers.length - 1] === "Closing Value",
    headers[headers.length - 1],
  );
  rec(
    "ROW 23 · drive-l10-stock.mjs's standing `headers.length === 11` is STALE — the screen has 13",
    headers.length === 13,
    `measured ${String(headers.length)}`,
  );
  rec(
    "ROW 23 · the screen advertises Alt+V itself (the bar and the hint strip are per screen KIND)",
    (await text("stock-summary-basis-hint")).includes("Alt+V"),
    (await text("stock-summary-basis-hint")).slice(0, 90),
  );

  // ---- ⛔⛔ THE PER-ITEM ORACLE. See the header: the company TOTAL cannot
  // discriminate here (1650+1700+1600 = 3x1650), so the item lines must.
  const avg = await itemCells("Avg Widget");
  const fifo = await itemCells("Fifo Widget");
  const lifo = await itemCells("Lifo Widget");
  rec(
    "ROW 23 · WEIGHTED_AVG item: outward 550.00 at cost, closing 1,650.00",
    avg[8] === "550.00" && avg[12] === "1,650.00" && avg[2] === "WEIGHTED_AVG",
    `basis=${avg[2]} outward=${avg[8]} closing=${avg[12]}`,
  );
  rec(
    "ROW 23 · FIFO item: outward 500.00 (the OLDEST layer), closing 1,700.00 — average would say 1,650.00",
    fifo[8] === "500.00" && fifo[12] === "1,700.00" && fifo[2] === "FIFO",
    `basis=${fifo[2]} outward=${fifo[8]} closing=${fifo[12]}`,
  );
  rec(
    "ROW 23 · LIFO item: outward 600.00 (the NEWEST layer), closing 1,600.00 — a third distinct answer",
    lifo[8] === "600.00" && lifo[12] === "1,600.00" && lifo[2] === "LIFO",
    `basis=${lifo[2]} outward=${lifo[8]} closing=${lifo[12]}`,
  );
  // ⭐ THE CONTROL ON THE ORACLE. Three identical movement histories that agree
  // are a build that stored the method and ignored it — the failure mode 0078's
  // leaf default exists to prevent, and the one that looks perfectly plausible.
  rec(
    "ROW 23 · CONTROL: the three bases DISAGREE on identical movements (agreement would mean the method is stored and unused)",
    new Set([avg[12], fifo[12], lifo[12]]).size === 3,
    `${avg[12]} / ${fifo[12]} / ${lifo[12]}`,
  );
  rec(
    "ROW 23 · a COMPOUND-unit item is restated in its base unit beside the keyed figure: 15 BOXX = 180.0000 Nos",
    lifo[9] === "15.0000" && lifo[10] === "180.0000 Nos",
    `closing qty=${lifo[9]} base=${lifo[10]}`,
  );
  rec(
    "ROW 23 · a SIMPLE-unit item prints an em dash there — a zero would be a claim about the goods",
    avg[10] === "—",
    avg[10],
  );

  // ---- The ungrouped bucket is a STATED state, not a hidden one (row 20's half
  // of 0079's claim: an item created with no group stays reachable and reported).
  rec("ROW 23 · the group subtotal table is present", await seen("stock-group-subtotals"));
  const groupLine = async (name) => {
    const row = testid(`stock-group:${name}`).first();
    if ((await row.count()) === 0) return "(absent)";
    return (await row.innerText()).replace(/\s+/g, " ").trim();
  };
  rec(
    "ROW 20/23 · the PARENT prints its whole BRANCH: 3,300.00 = Lifo 1,600 direct + Grains 1,700 beneath",
    /\b3,300\.00$/.test(await groupLine("Provisions")),
    await groupLine("Provisions"),
  );
  rec(
    "ROW 20/23 · the child branch is 1,700.00 — the FIFO item, at its OWN basis inside a group total",
    /\b1,700\.00$/.test(await groupLine("Grains")),
    await groupLine("Grains"),
  );
  rec(
    "ROW 20/23 · the UNGROUPED item is named and printed, never dropped — 1,650.00 under (ungrouped)",
    /\(ungrouped\)/.test(await groupLine("")) && /\b1,650\.00$/.test(await groupLine("")),
    await groupLine(""),
  );

  const total0 = await text("stock-total-value");
  rec("ROW 23 · the company's closing stock is 4,950.00", total0 === "4,950.00", total0);
  // ⚠️ AND THE TOTAL IS RECORDED AS NON-DISCRIMINATING, out loud, so nobody
  // mistakes it for the oracle: 3 x 1,650 is the same number.
  rec(
    "ROW 23 · ⚠️ recorded: the mixed total 4,950.00 is EXACTLY the all-average total, so only the item lines can catch a wrong valuer",
    (1650 + 1700 + 1600) === 3 * 1650,
    "1,650 + 1,700 + 1,600 = 4,950 = 3 x 1,650",
  );
  const bs0 = await j("GET", `${base}/balance-sheet?as_on=2026-04-30`, { token: TOK });
  rec(
    "ROW 23 · the Balance Sheet's Closing Stock is the SAME replay: 4950.0000, and it balances",
    JSON.stringify(bs0.body).includes("4950.0000") && bs0.body.balanced === true,
    `balanced=${String(bs0.body.balanced)}`,
  );

  // =========================================================================
  // §5 — ROW 23's ACL CLAUSE, RE-TESTED AGAINST A WRITE SURFACE THAT DID NOT
  //      EXIST WHEN THE ROW WAS LAST DRIVEN.
  //
  // ⛔ THE SECOND HALF IS THE NEW ONE. T9 mounted PUT /stock-items/{id}/
  // valuation-method and auth_middleware maps it to (alter, "master"). An
  // operator who may NOT READ the closing valuation but MAY RESTATE it — moving
  // every reported period's closing stock — is exactly the hole this row's
  // clause exists to deny, and no probe has ever asked.
  // =========================================================================
  {
    const deTok = (
      await j("POST", "/auth/token", { body: { subject: DE_SUBJECT, amr: ["webauthn"] } })
    ).body.token;
    const restate = await j("PUT", `${base}/stock-items/${IDS["Fifo Widget"]}/valuation-method`, {
      token: deTok,
      body: { valuation_method: "WEIGHTED_AVG", created_by: DE_SUBJECT },
    });
    rec(
      "ROW 23 · ⛔ Data Entry is REFUSED the restatement route (a level denied the report must not re-value it either)",
      restate.status === 403,
      `PUT valuation-method = ${String(restate.status)} ${JSON.stringify(restate.body).slice(0, 140)}`,
    );
    const stillFIFO = await sql(
      `SELECT valuation_method FROM stock_item WHERE id = ${q(IDS["Fifo Widget"])}`,
    );
    rec(
      "ROW 23 · ...and the column did not move (a 403 that wrote anyway is the worst of both)",
      stillFIFO === "FIFO",
      stillFIFO,
    );

    const de = await openApp({
      company: CO,
      expectCompany: COMPANY_NAME,
      subject: DE_SUBJECT,
      password: DE_PASSWORD,
    });
    try {
      await de.openFromGateway("Stock Summary", 2500);
      await de.page.waitForTimeout(800);
      const denied = (await de.page.getByTestId("stock-summary-denied").count()) > 0;
      const tableShown = (await de.page.getByTestId("stock-summary-table").count()) > 0;
      rec(
        "ROW 23 · ⛔ the Stock Summary is DENIED to Data Entry by default, and says so instead of rendering a table",
        denied && !tableShown,
        `denied=${String(denied)} table=${String(tableShown)}`,
      );
      const advice = (await de.page.getByTestId("stock-summary-denied-advice").count()) > 0
        ? (await de.page.getByTestId("stock-summary-denied-advice").first().innerText())
            .replace(/\s+/g, " ")
            .trim()
        : "";
      rec(
        "ROW 23 · ...and the refusal names the remedy, so an operator asks their Owner instead of filing a bug",
        /Owner|Users & Roles|allow/i.test(advice),
        advice.slice(0, 150),
      );
      // ⭐ THE CONTROL. A screen that refused EVERYTHING would pass the two
      // assertions above without the rule existing at all.
      await de.openFromGateway("Day Book", 2500);
      await de.page.waitForTimeout(600);
      const dayBook = await de.body();
      rec(
        "ROW 23 · CONTROL: the SAME Data Entry subject CAN open the Day Book, so the denial above is a RULE and not a broken session",
        /Day Book/.test(dayBook) && !/Stock Summary/.test(dayBook),
        dayBook.replace(/\s+/g, " ").slice(0, 110),
      );
    } finally {
      await de.browser.close();
    }
  }

  // =========================================================================
  // §6 — ROW 26. THE FOUR INVENTORY VOUCHER CLASSES, KEYED FROM THE KEYBOARD.
  //
  // ⛔ Alt+F7/F8/F9/F10 ARE FUNCTION KEYS, so the altMac composition trap does
  // not apply (macOS does not compose Option+F7 into a character). They are
  // still driven with down("Alt") + press("F7") rather than press("Alt+F7"),
  // and the screen is ASSERTED OPEN, because the whole point of the altMac table
  // is that a key a Mac cannot send passes silently.
  // =========================================================================
  const tbBefore = JSON.stringify(
    (await j("GET", `${base}/trial-balance?as_on=2026-04-30`, { token: TOK })).body,
  );
  const postingsBefore = await sql(
    `SELECT count(*) FROM posting WHERE company_id = ${q(CO)}`,
  );

  /** Open one stock voucher screen by its function key and assert it is there. */
  const openStockVoucher = async (fkey, title) => {
    await toGateway();
    await page.keyboard.down("Alt");
    await page.keyboard.press(fkey);
    await page.keyboard.up("Alt");
    await page.waitForTimeout(1500);
    const open = await seen("stock-voucher-screen");
    rec(
      `ROW 26 · Alt+${fkey} opens the ${title} screen`,
      open && (await body()).includes(title),
      open ? (await body()).split("\n")[0] : "(no stock-voucher-screen)",
    );
    return open;
  };

  /** Key one line of a stock voucher grid and Ctrl+A. Returns the status line. */
  const keyStockLine = async ({ date, item, godown, toGodown, qty, rate, party }) => {
    await type("stock-date", date);
    if (party) await type("stock-party", party);
    await testid("stock-item:0").first().selectOption({ label: item });
    await page.waitForTimeout(250);
    await testid("stock-godown:0").first().selectOption({ label: godown });
    if (toGodown) await testid("stock-to-godown:0").first().selectOption({ label: toGodown });
    await type("stock-qty:0", qty);
    if (rate !== undefined) await type("stock-rate:0", rate);
    await page.waitForTimeout(200);
    await press("Control+a", 2500);
    return await text("stock-status");
  };

  // ---- PROBE G. DOES A DELIVERY NOTE'S MOVEMENT REACH THE LOT QUEUE AT ALL?
  //
  // ⛔ THE RATE IS KEYED AS 200 — THE SALE PRICE — ON PURPOSE. An outward's own
  // rate is IGNORED by every valuer (inventory.Movement.Rate), so the cost of
  // this shipment must be the OLDEST LAYER, 5 @ 100 = 500.0000. A valuer that
  // honoured the keyed rate prints 1,000 here; one that fell back to weighted
  // average prints 550. Both are plausible and both are wrong.
  if (await openStockVoucher("F8", "Delivery Note")) {
    rec(
      "ROW 26 · the Delivery Note states on its face that it posts NO accounting entry",
      (await text("stock-no-postings")).includes("No accounting entry"),
      await text("stock-no-postings"),
    );
    // ⛔ FIRST, THE REFUSAL — met by accident on the first run of this file and
    // kept deliberately. A Delivery Note is the CHALLAN THE GOODS TRAVEL ON, so
    // a note with no consignee is a document that cannot be delivered. It is
    // refused at the keyboard, in the screen's own vocabulary, before the wire.
    const refused = await keyStockLine({
      date: "2026-04-20",
      item: "Fifo Widget",
      godown: MAIN.name,
      qty: "5",
      rate: "200",
    });
    rec(
      "ROW 26 · a Delivery Note with NO CONSIGNEE is refused in words — the note is the paper the goods travel on",
      /consignee|Party A\/c Name/i.test(refused),
      refused.slice(0, 180),
    );
    const st = await keyStockLine({
      date: "2026-04-20",
      item: "Fifo Widget",
      godown: MAIN.name,
      qty: "5",
      rate: "200",
      party: "Drive Customer",
    });
    rec("ROW 26 · ...and it saves once the consignee is keyed", st === "Saved", st.slice(0, 160));
  }

  await openStockSummary();
  const fifoAfterDN = await itemCells("Fifo Widget");
  rec(
    "ROW 26 · ⛔ the Delivery Note was costed off the FIFO LOT QUEUE: outward at cost 1,000.00 (500 sale + 500 oldest layer)",
    fifoAfterDN[8] === "1,000.00",
    `outward at cost = ${fifoAfterDN[8]} — 1,500.00 would mean the keyed rate of 200 was honoured; ` +
      `1,100.00 would mean a weighted-average fallback under a FIFO heading`,
  );
  rec(
    "ROW 26 · ...and the position it leaves is the remaining layer: 10.0000 at 1,200.00",
    fifoAfterDN[9] === "10.0000" && fifoAfterDN[12] === "1,200.00",
    `closing qty=${fifoAfterDN[9]} value=${fifoAfterDN[12]}`,
  );

  // ---- PROBE G2. A RECEIPT NOTE INWARD MOVES THE POSITION BY EXACTLY qty*rate.
  if (await openStockVoucher("F9", "Receipt Note")) {
    const st = await keyStockLine({
      date: "2026-04-21",
      item: "Fifo Widget",
      godown: MAIN.name,
      qty: "5",
      rate: "130",
    });
    rec("ROW 26 · the Receipt Note saves", st === "Saved", st.slice(0, 160));
  }
  await openStockSummary();
  const fifoAfterRN = await itemCells("Fifo Widget");
  rec(
    "ROW 26 · a Receipt Note adds a NEW layer at the keyed rate: closing 15.0000 at 1,850.00 (1,200 + 5 x 130)",
    fifoAfterRN[9] === "15.0000" && fifoAfterRN[12] === "1,850.00",
    `closing qty=${fifoAfterRN[9]} value=${fifoAfterRN[12]}`,
  );
  rec(
    "ROW 26 · ...and the INWARD VALUE column carries it: 2,850.00 = 1,000 + 1,200 + 650",
    fifoAfterRN[6] === "2,850.00",
    fifoAfterRN[6],
  );

  // =========================================================================
  // ⛔⛔ PROBE H — THE HEADLINE. DOES A STOCK JOURNAL BETWEEN OUR OWN SHELVES
  // CONSERVE VALUE?
  //
  // This is LEAD 2's family. The 2026-08-07 fix forced a NON-ZERO rate on the
  // inward leg, and under weighted average that was enough: the rate the report
  // SHOWS an operator (the position's Rate column) IS the consumption rate, so
  // keying it conserves exactly. Under FIFO the OUT leg consumes the OLDEST
  // layer and the IN leg carries the goods at whatever single rate was keyed —
  // and there is in general NO rate an operator can key that conserves value.
  //
  // ⭐ THE WEIGHTED-AVERAGE ARM IS RUN FIRST AND IS THE CONTROL, so a failure on
  // the FIFO arm is attributable to the COSTING METHOD and not to transfers as
  // such. Both key the rate the SCREEN OFFERS — the Rate cell of the position
  // row, which is the only figure in the product an operator has to reach for.
  //
  // ⚠️ CONSERVATION IS THE PASS.
  // =========================================================================
  const avgMain = await positionCells("Avg Widget", MAIN.name);
  const avgRate = avgMain[11];
  rec(
    "ROW 26 · CONTROL SETUP: the WEIGHTED_AVG position offers a Rate of 110.00, which is also its consumption rate",
    avgRate === "110.00",
    avgRate,
  );
  const totalBeforeTransfers = await text("stock-total-value");
  rec(
    "ROW 26 · the company's closing stock before any transfer is 5,100.00 (1,650 + 1,850 + 1,600)",
    totalBeforeTransfers === "5,100.00",
    totalBeforeTransfers,
  );

  if (await openStockVoucher("F7", "Stock Journal")) {
    rec(
      "ROW 26 · the Stock Journal grid has a FROM and a TO godown — one keyed row, a PAIR of movements",
      (await seen("stock-godown:0")) && (await seen("stock-to-godown:0")),
    );
    const st = await keyStockLine({
      date: "2026-04-22",
      item: "Avg Widget",
      godown: MAIN.name,
      toGodown: SECOND_NAME,
      qty: "10",
      rate: avgRate,
    });
    rec("ROW 26 · the weighted-average transfer saves", st === "Saved", st.slice(0, 160));
  }
  await openStockSummary();
  const avgAfterTransfer = await itemCells("Avg Widget");
  rec(
    "ROW 26 · ⭐ CONTROL: a WEIGHTED_AVG transfer of 10 at the rate the screen offers CONSERVES value — still 1,650.00",
    avgAfterTransfer[12] === "1,650.00",
    `${avgAfterTransfer[12]} (was 1,650.00)`,
  );
  const totalAfterAvg = await text("stock-total-value");
  rec(
    "ROW 26 · ...and the company total is unmoved at 5,100.00",
    totalAfterAvg === "5,100.00",
    totalAfterAvg,
  );

  const fifoMain = await positionCells("Fifo Widget", MAIN.name);
  const fifoRate = fifoMain[11];
  rec(
    "ROW 26 · SETUP: the FIFO position offers a Rate of 123.33 — its BLENDED value/qty, which is NOT its consumption rate (the oldest layer is 120)",
    fifoRate === "123.33",
    `${fifoRate} — 1,850.00 / 15.0000, while the layer FIFO will consume is 10 @ 120.00`,
  );
  if (await openStockVoucher("F7", "Stock Journal")) {
    const st = await keyStockLine({
      date: "2026-04-23",
      item: "Fifo Widget",
      godown: MAIN.name,
      toGodown: SECOND_NAME,
      qty: "10",
      rate: fifoRate,
    });
    rec("ROW 26 · the FIFO transfer saves", st === "Saved", st.slice(0, 160));
  }
  await openStockSummary();
  const fifoAfterTransfer = await itemCells("Fifo Widget");
  const money = (t) => Number(t.replace(/,/g, ""));
  const fifoDelta = (money(fifoAfterTransfer[12]) - money(fifoAfterRN[12])).toFixed(2);
  rec(
    "ROW 26 · ⛔⛔ a FIFO transfer between OUR OWN godowns CONSERVES value — a document that moved nothing in or out must change no figure",
    fifoAfterTransfer[12] === fifoAfterRN[12],
    `${fifoAfterRN[12]} -> ${fifoAfterTransfer[12]}, a delta of ${fifoDelta}. The OUT leg consumes the ` +
      `OLDEST layer (10 @ 120.00 = 1,200.00) while the IN leg carries the goods at ${fifoRate} — the ` +
      `position's BLENDED value/qty, which is the only rate the product shows anywhere. A conserving ` +
      `rate does exist in principle (the FIFO consumption cost divided by the quantity, 120.00 here), ` +
      `but nothing in the product derives it or offers it, so the operator cannot key it. Quantity is ` +
      `unchanged, so no quantity check can see this, and closing stock feeds the BALANCE SHEET.`,
  );

  const totalAfterFifo = await text("stock-total-value");
  rec(
    "ROW 26 · ...and the company's closing stock is unmoved by it",
    totalAfterFifo === totalAfterAvg,
    `${totalAfterAvg} -> ${totalAfterFifo}, a delta of ` +
      `${(money(totalAfterFifo) - money(totalAfterAvg)).toFixed(2)} — while the weighted-average ` +
      `transfer immediately before it, keyed the same way, moved nothing at all`,
  );

  // ---- ⭐ THE CONTROL THAT LOCATES THE DEFECT PRECISELY. Main now holds ONE
  // layer (5 @ 130), so the blended rate the screen offers IS the consumption
  // rate — and the identical keystrokes conserve exactly. The fault is therefore
  // not "transfers", and not "FIFO": it is that the ONLY rate the product offers
  // an operator is the position's blended value/qty, which stops being the
  // consumption rate the moment a lot queue holds more than one layer.
  const fifoMainAfter = await positionCells("Fifo Widget", MAIN.name);
  rec(
    "ROW 26 · CONTROL SETUP: the FIFO position now holds ONE layer, 5.0000 at a Rate of 130.00",
    fifoMainAfter[9] === "5.0000" && fifoMainAfter[11] === "130.00",
    `qty=${fifoMainAfter[9]} rate=${fifoMainAfter[11]}`,
  );
  if (await openStockVoucher("F7", "Stock Journal")) {
    const st = await keyStockLine({
      date: "2026-04-23",
      item: "Fifo Widget",
      godown: MAIN.name,
      toGodown: SECOND_NAME,
      qty: "5",
      rate: fifoMainAfter[11],
    });
    rec("ROW 26 · the single-layer FIFO transfer saves", st === "Saved", st.slice(0, 160));
  }
  await openStockSummary();
  const fifoAfterSingleLayer = await itemCells("Fifo Widget");
  rec(
    "ROW 26 · ⭐ CONTROL: the SAME keystrokes on a SINGLE-LAYER FIFO position conserve value exactly",
    fifoAfterSingleLayer[12] === fifoAfterTransfer[12],
    `${fifoAfterTransfer[12]} -> ${fifoAfterSingleLayer[12]}. The offered rate and the consumption ` +
      `rate coincide only while the queue holds one layer, which is why the arm above moves and this one does not.`,
  );
  const bs1 = await j("GET", `${base}/balance-sheet?as_on=2026-04-30`, { token: TOK });
  const bsFlat = JSON.stringify(bs1.body);
  // ⛔ READ FRESH, not reused from before the control transfer: the point of this
  // arm is that the SHEET carries whatever the report carries, including the
  // 33.30 the transfer above created — a wrong figure that two statements agree
  // on is the one nothing in the product can catch.
  const totalNow = await text("stock-total-value");
  const screenTotal = totalNow.replace(/,/g, "");
  rec(
    "ROW 23/26 · the Balance Sheet's Closing Stock agrees with the Stock Summary TO THE BYTE, wrong figure included",
    bsFlat.includes(`${screenTotal}00`) && bs1.body.balanced === true,
    `screen ${totalNow} · sheet carries ${screenTotal}00 = ${String(bsFlat.includes(`${screenTotal}00`))} · balanced=${String(bs1.body.balanced)}`,
  );

  // =========================================================================
  // PROBE I — Alt+F10 PHYSICAL STOCK, ON A COMPOUND-UNIT ITEM.
  //
  // LEAD 3's shape, now with a factor of twelve available to get wrong: the
  // count is keyed in the ITEM'S OWN unit (15 BOXX), never in the base unit,
  // and a count equal to the book must derive a delta of ZERO and write nothing.
  // =========================================================================
  if (await openStockVoucher("F10", "Physical Stock")) {
    await testid("stock-item:0").first().selectOption({ label: "Lifo Widget" });
    await page.waitForTimeout(400);
    const gridRow = (await testid("stock-grid").first().innerText()).replace(/\s+/g, " ");
    rec(
      "ROW 26 · the count grid names the item's OWN unit (BOXX) beside it — the operator counts boxes, not the 180 pieces inside them",
      /BOXX/.test(gridRow),
      gridRow.slice(0, 180),
    );
    rec(
      "ROW 26 · the screen says the SERVER derives the correction, so the count can never be a delta against a stale read",
      /SERVER derives the correction/i.test(await text("stock-total")),
      (await text("stock-total")).slice(0, 150),
    );
    const st = await keyStockLine({
      date: "2026-04-24",
      item: "Lifo Widget",
      godown: MAIN.name,
      qty: "15",
      rate: "100",
    });
    rec("ROW 26 · a count that AGREES with the books saves", st === "Saved", st.slice(0, 160));
  }
  const zeroDelta = await sql(
    // ⛔ CURRENT VERSIONS ONLY, and the class comes from voucher_type.parent_class
    // (there is no voucher_class column — the class lives on the TYPE, and a
    // superseded version still has its movement rows).
    `SELECT count(*) FROM stock_movement sm
       JOIN voucher_head vh ON vh.current_version_id = sm.voucher_version_id
       JOIN voucher_version vv ON vv.id = vh.current_version_id
       JOIN voucher_type vt ON vt.id = vv.voucher_type_id
      WHERE vh.company_id = ${q(CO)} AND vh.is_deleted = false
        AND vt.parent_class = 'PHYSICAL_STOCK'`,
  );
  rec(
    "ROW 26 · ⛔ a count that AGREES with the books writes NO movement at all — a correction is not a thing that happened",
    zeroDelta === "0",
    `${zeroDelta} movement row(s) under Physical Stock`,
  );
  await openStockSummary();
  const lifoAfterCount = await itemCells("Lifo Widget");
  rec(
    "ROW 26 · ...and the LIFO item is untouched at 15.0000 / 1,600.00, still restated as 180.0000 Nos",
    lifoAfterCount[9] === "15.0000" &&
      lifoAfterCount[12] === "1,600.00" &&
      lifoAfterCount[10] === "180.0000 Nos",
    `qty=${lifoAfterCount[9]} value=${lifoAfterCount[12]} base=${lifoAfterCount[10]}`,
  );

  // ---- A SURPLUS WITH THE RATE COLUMN BLANK MUST BE REFUSED IN WORDS.
  let surplusRefusal = "";
  if (await openStockVoucher("F10", "Physical Stock")) {
    surplusRefusal = await keyStockLine({
      date: "2026-04-24",
      item: "Lifo Widget",
      godown: MAIN.name,
      qty: "17",
      rate: "",
    });
    rec(
      "ROW 26 · ⛔ a count ABOVE the books with the Rate column blank is REFUSED in words, not brought in at zero cost",
      /rate/i.test(surplusRefusal) && /above the books|surplus|zero cost/i.test(surplusRefusal),
      surplusRefusal.slice(0, 220),
    );
  }
  const afterSurplus = await sql(
    // ⛔ CURRENT VERSIONS ONLY, and the class comes from voucher_type.parent_class
    // (there is no voucher_class column — the class lives on the TYPE, and a
    // superseded version still has its movement rows).
    `SELECT count(*) FROM stock_movement sm
       JOIN voucher_head vh ON vh.current_version_id = sm.voucher_version_id
       JOIN voucher_version vv ON vv.id = vh.current_version_id
       JOIN voucher_type vt ON vt.id = vv.voucher_type_id
      WHERE vh.company_id = ${q(CO)} AND vh.is_deleted = false
        AND vt.parent_class = 'PHYSICAL_STOCK'`,
  );
  rec(
    "ROW 26 · ...and the refused surplus wrote nothing",
    afterSurplus === "0",
    `${afterSurplus} movement row(s)`,
  );
  // ⚠️ RECORDED AS COPY, NOT ARITHMETIC. The refusal is correct on all three
  // methods; it simply names the wrong one on a LIFO item.
  rec(
    '⚠️ COPY: the surplus refusal names "the item\'s weighted-average value" on a LIFO item — the rule is right, the reason is not',
    !/weighted-average/i.test(surplusRefusal),
    surplusRefusal.slice(0, 220),
  );

  // =========================================================================
  // PROBE J — THE CLASS CLAIMS THAT MUST STILL HOLD AFTER T9 AND T10.
  //
  // §6 of the audit already warns that a row covering a CLASS is not proved by
  // one member. These are the arms that keep that true.
  // =========================================================================
  const classes = await sql(
    `SELECT string_agg(DISTINCT vt.parent_class, ',' ORDER BY vt.parent_class)
       FROM voucher_head vh
       JOIN voucher_version vv ON vv.id = vh.current_version_id
       JOIN voucher_type vt ON vt.id = vv.voucher_type_id
      WHERE vh.company_id = ${q(CO)} AND vh.is_deleted = false
        AND vt.parent_class IN ('DELIVERY_NOTE','RECEIPT_NOTE','STOCK_JOURNAL','PHYSICAL_STOCK')`,
  );
  rec(
    "ROW 26 · all FOUR classes were keyed and committed from the keyboard",
    classes === "DELIVERY_NOTE,PHYSICAL_STOCK,RECEIPT_NOTE,STOCK_JOURNAL",
    classes || "(none)",
  );
  const stockPostings = await sql(
    `SELECT count(*) FROM posting p
       JOIN voucher_version vv ON vv.id = p.voucher_version_id
       JOIN voucher_type vt ON vt.id = vv.voucher_type_id
      WHERE p.company_id = ${q(CO)}
        AND vt.parent_class IN ('DELIVERY_NOTE','RECEIPT_NOTE','STOCK_JOURNAL','PHYSICAL_STOCK')`,
  );
  rec(
    "ROW 26 · ⛔ NONE of the four wrote a single accounting posting — goods move and the ledger does not",
    stockPostings === "0",
    `${stockPostings} posting row(s)`,
  );
  const postingsAfter = await sql(`SELECT count(*) FROM posting WHERE company_id = ${q(CO)}`);
  rec(
    "ROW 26 · CONTROL: the company's posting count is UNCHANGED across the whole inventory-voucher sequence",
    postingsAfter === postingsBefore,
    `${postingsBefore} -> ${postingsAfter}`,
  );
  const tbAfter = JSON.stringify(
    (await j("GET", `${base}/trial-balance?as_on=2026-04-30`, { token: TOK })).body,
  );
  rec(
    "ROW 26 · ...and the Trial Balance is byte-for-byte what it was before the four vouchers",
    tbAfter === tbBefore,
    tbAfter === tbBefore ? "identical" : "MOVED",
  );
  const pair = await sql(
    `SELECT string_agg(sm.direction::text, '+' ORDER BY sm.direction::text)
       FROM stock_movement sm
      WHERE sm.voucher_version_id = (
        SELECT vh.current_version_id FROM voucher_head vh
          JOIN voucher_version vv ON vv.id = vh.current_version_id
          JOIN voucher_type vt ON vt.id = vv.voucher_type_id
         WHERE vh.company_id = ${q(CO)} AND vh.is_deleted = false
           AND vt.parent_class = 'STOCK_JOURNAL'
         ORDER BY vv.created_at DESC LIMIT 1)`,
  );
  rec(
    "ROW 26 · one keyed transfer row is stored as a PAIR of movements, so a transfer cannot become a write-off",
    pair === "IN+OUT",
    pair || "(none)",
  );
  const dirs = await sql(
    `SELECT string_agg(DISTINCT vt.parent_class || '=' || sm.direction::text, ',' ORDER BY vt.parent_class || '=' || sm.direction::text)
       FROM stock_movement sm
       JOIN voucher_head vh ON vh.current_version_id = sm.voucher_version_id
       JOIN voucher_version vv ON vv.id = vh.current_version_id
       JOIN voucher_type vt ON vt.id = vv.voucher_type_id
      WHERE vh.company_id = ${q(CO)} AND vh.is_deleted = false
        AND vt.parent_class IN ('DELIVERY_NOTE','RECEIPT_NOTE')`,
  );
  rec(
    "ROW 26 · direction is still FIXED BY THE DOCUMENT: a Delivery Note only ships OUT, a Receipt Note only brings IN",
    dirs === "DELIVERY_NOTE=OUT,RECEIPT_NOTE=IN",
    dirs || "(none)",
  );
} catch (e) {
  driveError = e;
  rec("the drive ran to the end without throwing", false, String(e).slice(0, 500));
} finally {
  console.log("\n--- non-GET browser requests (status last) ---");
  for (const r of net) console.log("  " + r);
  console.log("\n--- console errors ---");
  for (const e of app.consoleErrors.slice(0, 8)) console.log("  " + e);
  await app.browser.close();
  // ⛔ THE TENANT IS REAPED. L27 leaked two for want of exactly this, and the
  // catalog-derived DELETE is copied verbatim from drive-u1-voided-vouchers.mjs
  // so a table added later does not silently start leaking.
  try {
    await sql(`
DO $$
DECLARE t text;
BEGIN
  SET LOCAL session_replication_role = replica;
  EXECUTE format('DELETE FROM statutory_step WHERE workflow_id LIKE %L', 'irn:' || ${q(CO)} || ':%');
  FOR t IN
    SELECT c.table_name FROM information_schema.columns c
      JOIN information_schema.tables tb USING (table_schema, table_name)
     WHERE c.table_schema='public' AND c.column_name='company_id'
       AND tb.table_type='BASE TABLE' AND c.table_name <> 'company'
  LOOP
    EXECUTE format('DELETE FROM %I WHERE company_id::text = %L', t, ${q(CO)});
  END LOOP;
  DELETE FROM company WHERE id = ${q(CO)};
  DELETE FROM app_user WHERE subject = ${q(DE_SUBJECT)};
END $$;`);
    const left = await sql(`SELECT count(*) FROM company WHERE id = ${q(CO)}`);
    console.log(`\ntorn down ${COMPANY_NAME}: ${left === "0" ? "clean" : `LEAKED (${left})`}`);
  } catch (e) {
    console.log(`\n⚠️ teardown FAILED for ${CO}: ${e}`);
  }
  if (driveError) console.log("\n--- the throw ---\n" + String(driveError).slice(0, 1500));
}
process.exit(summary() > 0 ? 1 : 0);
