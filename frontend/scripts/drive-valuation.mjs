/**
 * T9 — FIFO / LIFO COSTING, driven from the keyboard in a real browser.
 *
 * ⛔ WHAT THIS PROVES, AND WHY IT NEEDED A BROWSER
 * ===========================================================================
 * The valuers, the CHECK constraint, the corpus fixtures and the Python oracle
 * can all be green while NO OPERATOR HAS FIFO — that is exactly what happened to
 * payroll one wave ago (eight mounted routes, a complete engine, and two missing
 * writers, so nobody could be paid). So this probe never calls a service and
 * never posts a voucher body of its own for the parts a user does: it keys the
 * item master's Costing method field, it presses Alt+V on the Stock Summary, and
 * it reads the figures off the screen.
 *
 * ⛔ THE ORACLE IS THAT THE THREE METHODS DISAGREE, and the numbers are
 * hand-derived, not read back from the thing under test. Buy 10 @ 100, buy
 * 10 @ 120, issue 5:
 *
 *     Avg. Cost   consumed 550.0000   closing 1,650.00
 *     FIFO        consumed 500.0000   closing 1,700.00
 *     LIFO        consumed 600.0000   closing 1,600.00
 *
 * A screen that STORED the method and valued everything at average would show
 * 1,650.00 under a FIFO heading — plausible, balanced, and wrong. Every
 * assertion below is chosen so that answer fails.
 *
 * ⛔ AND THE BALANCE SHEET IS CHECKED TOO. Closing stock feeds it, so a costing
 * method that reaches the Stock Summary and not the sheet would leave the two
 * statements stating different closing stock with nothing out of balance to say
 * which is right.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY OF ITS OWN, NOT ON Demo Traders — posting
 * purchases and sales moves a trial balance, and residue in the demo tenant
 * reads as broken books to the next lane and drops `verify:demo` below 34/34.
 *
 * RUN:  node frontend/scripts/drive-valuation.mjs
 *       (needs cored :8087 and vite :5173. It creates its own company.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Valuation Drive ${stamp}`;

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

// ---- The tenant, plus the ledgers the two vouchers need. Created over the API
// as the SAME subject the browser signs in as, so the browser is an Owner of it.
//
// ⚠️ THE LEDGERS AND THE VOUCHERS ARE SET UP OVER HTTP ON PURPOSE. They are not
// what this probe is about, and keying two item invoices through the grid would
// make a costing-method failure indistinguishable from a voucher-entry failure.
// THE ITEM MASTER AND THE COSTING METHOD ARE KEYED IN THE BROWSER, because they
// are the feature.
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
    `drive-valuation: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
console.log(`company ${COMPANY_NAME} = ${CO}`);

const base = `/v1/companies/${CO}`;
const ledger = async (name, group) =>
  (await j("POST", `${base}/ledgers`, {
    token: TOK,
    body: { name, group_name: group, created_by: "drive" },
  })).body.ledger_id;
const SUPPLIER = await ledger("Drive Supplier", "Sundry Creditors");
const PURCHASES = await ledger("Drive Purchases", "Purchase Accounts");
const CUSTOMER = await ledger("Drive Customer", "Sundry Debtors");
const SALES = await ledger("Drive Sales", "Sales Accounts");
const GODOWN = (await j("GET", `${base}/godowns`, { token: TOK })).body.godowns.find(
  (g) => g.is_system,
).id;

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

/** The Stock Summary, as at the end of the drive period, read from the screen. */
const openStockSummary = async () => {
  await openFromGateway("Stock Summary", 2000);
  await page.waitForTimeout(600);
};

try {
  // =========================================================================
  // 1. THE COSTING METHOD IS ON THE MASTER FORM, AND IT IS SERVED.
  // =========================================================================
  await toGateway();
  await altMac("g");
  await page.keyboard.type("Create Stock Item");
  await page.waitForTimeout(400);
  await press("Enter", 1200);
  rec("Alt+G > Create Stock Item opens the item master", await seen("master-create:stock-item"));

  const methodField = testid("master-item-valuation-method").first();
  rec("the master form has a Costing method field at all", (await methodField.count()) > 0);
  const options = await methodField.locator("option").allInnerTexts();
  // ⛔ THE OPTIONS ARE THE SERVER'S. A hardcoded client list can offer a method
  // cored refuses, and the operator meets that refusal after keying the master.
  rec(
    "it offers the three methods this build can VALUE, with Tally's own names",
    options.some((o) => /^FIFO — FIFO/.test(o)) &&
      options.some((o) => /^LIFO — LIFO \(Perpetual\)/.test(o)) &&
      options.some((o) => /^WEIGHTED_AVG — Avg\. Cost/.test(o)),
    options.join(" | "),
  );
  rec(
    "and it offers NOTHING this build cannot value (no Standard Cost, no At Zero Cost)",
    !options.some((o) => /STANDARD_COST|AT_ZERO_COST|LAST_PURCHASE_COST|MONTHLY_AVG/.test(o)),
    options.join(" | "),
  );

  await type("master-name", "Drive Widget");
  const units = page.getByRole("combobox", { name: "Units" }).first();
  await units.click();
  await units.fill("Nos");
  await page.waitForTimeout(250);
  await page.getByText("Nos", { exact: true }).first().click();
  await choose("master-item-valuation-method", "FIFO");
  rec(
    "choosing FIFO says what FIFO MEANS before the master is saved",
    /MOST RECENT purchases cost/i.test(await text("master-item-valuation-basis")),
    (await text("master-item-valuation-basis")).slice(0, 90),
  );
  await press("Control+a", 1500);

  // ⛔ THE ORACLE IS THE COLUMN, NOT THE STATUS LINE. A body key accepted and
  // discarded answers 201 and changes nothing (register row D43).
  const storedMethod = await sql(
    `SELECT valuation_method FROM stock_item
      WHERE company_id = ${q(CO)} AND name = ${q("Drive Widget")}`,
  );
  rec(
    "stock_item.valuation_method in POSTGRES says FIFO",
    storedMethod === "FIFO",
    storedMethod || "(no row)",
  );
  const ITEM = await sql(
    `SELECT id::text FROM stock_item WHERE company_id = ${q(CO)} AND name = ${q("Drive Widget")}`,
  );

  // =========================================================================
  // 2. THE MOVEMENTS. Buy 10 @ 100, buy 10 @ 120, issue 5 at a SALE price of
  //    200 — a price far from every cost, so a valuer that consumed at the sale
  //    rate would be caught by any of the three methods.
  // =========================================================================
  const inv = (dir, qty, rate, amount) => [
    {
      stock_item_id: ITEM,
      godown_id: GODOWN,
      batch_id: "",
      direction: dir,
      qty,
      rate,
      amount,
    },
  ];
  for (const [no, date, qty, rate, amount] of [
    ["PUR-1", "2026-04-01", "10", "100", "1000"],
    ["PUR-2", "2026-04-05", "10", "120", "1200"],
  ]) {
    const r = await j("POST", `${base}/vouchers/purchase`, {
      token: TOK,
      body: {
        voucher_no: no,
        voucher_date: date,
        created_by: "drive",
        postings: [
          { ledger_id: PURCHASES, dr_cr: "D", amount },
          { ledger_id: SUPPLIER, dr_cr: "C", amount },
        ],
        inventory: inv("IN", qty, rate, amount),
      },
    });
    if (r.status !== 201) throw new Error(`${no}: ${r.status} ${JSON.stringify(r.body)}`);
  }
  const sale = await j("POST", `${base}/vouchers/sales`, {
    token: TOK,
    body: {
      voucher_no: "INV-1",
      voucher_date: "2026-04-10",
      created_by: "drive",
      postings: [
        { ledger_id: CUSTOMER, dr_cr: "D", amount: "1000" },
        { ledger_id: SALES, dr_cr: "C", amount: "1000" },
      ],
      inventory: inv("OUT", "5", "200", "1000"),
    },
  });
  if (sale.status !== 201) throw new Error(`INV-1: ${sale.status} ${JSON.stringify(sale.body)}`);

  // =========================================================================
  // 3. THE STOCK SUMMARY VALUES IT AS FIFO, AND SAYS SO.
  // =========================================================================
  await openStockSummary();
  const screenBody = await body();
  rec(
    "the Stock Summary opens and has a BASIS column",
    /Basis/.test(screenBody) && (await seen("stock-summary-table")),
  );
  rec(
    "the item line names its basis as FIFO",
    (await text("stock-basis:Drive Widget")) === "FIFO",
    await text("stock-basis:Drive Widget"),
  );
  const totalFIFO = await text("stock-total-value");
  // 15 units: PUR-1's remaining 5 at 100 (500) + all of PUR-2 (1200) = 1,700.
  // ⛔ 1,650.00 HERE IS THE WHOLE DEFECT THIS PROBE EXISTS FOR: it is the
  // weighted-average answer, and it is what a build that stored the method
  // without honouring it would print under the word FIFO.
  rec(
    "closing stock is the FIFO figure 1,700.00 (average would say 1,650.00)",
    totalFIFO === "1,700.00",
    totalFIFO,
  );
  rec(
    "the screen ADVERTISES Alt+V, which is the only surface that can (the bar and the hint strip are per screen KIND)",
    /Alt\+V/.test(screenBody),
  );

  // =========================================================================
  // 4. Alt+V SWITCHES IT, AND THE FIGURE ON SCREEN MOVES.
  // =========================================================================
  await altMac("v", 800);
  rec("Alt+V opens the costing-method panel on the report", await seen("stock-valuation-panel"));
  rec(
    "the panel states the CONSEQUENCE before the choice, not after it",
    /WHOLE history|every period/i.test(await text("stock-valuation-panel")),
  );
  const combo = page.getByRole("combobox", { name: /New method/i }).first();
  await combo.click();
  await combo.fill("LIFO");
  await page.waitForTimeout(250);
  await press("Enter", 2000);

  const totalLIFO = await text("stock-total-value");
  rec(
    "the report REDREW on the LIFO basis: 1,600.00, a third distinct answer",
    totalLIFO === "1,600.00",
    totalLIFO,
  );
  rec(
    "the item line now names LIFO",
    (await text("stock-basis:Drive Widget")) === "LIFO",
    await text("stock-basis:Drive Widget"),
  );
  rec(
    "the operator is told the item's whole history was re-valued",
    /WHOLE history/i.test(await text("stock-valuation-status")),
    (await text("stock-valuation-status")).slice(0, 120),
  );
  const afterSwitch = await sql(
    `SELECT valuation_method FROM stock_item WHERE id = ${q(ITEM)}`,
  );
  rec("and POSTGRES agrees the column moved", afterSwitch === "LIFO", afterSwitch);
  // The restatement left an audit trail naming BOTH methods — the only record
  // anywhere of why a period's closing stock changed.
  const audit = await sql(
    `SELECT (before_doc->>'valuation_method') || '->' || (after_doc->>'valuation_method')
       FROM master_audit
      WHERE company_id = ${q(CO)} AND entity_id = ${q(ITEM)} AND op = 'ALTER'`,
  );
  rec("a master_audit row records the restatement, naming both methods", audit === "FIFO->LIFO", audit);

  // =========================================================================
  // 5. THE BALANCE SHEET MOVED WITH IT. Closing stock feeds the sheet, so a
  //    method that reached the report and not the statement would leave the two
  //    disagreeing with nothing out of balance to reveal which is right.
  // =========================================================================
  const bs = await j("GET", `${base}/balance-sheet?as_on=2026-04-30`, { token: TOK });
  const flat = JSON.stringify(bs.body);
  rec(
    "the Balance Sheet carries the LIFO closing stock (1600.0000) and balances",
    flat.includes("1600.0000") && bs.body.balanced === true,
    `balanced=${String(bs.body.balanced)}`,
  );
  rec(
    "and it does NOT still carry the FIFO or the average figure",
    !flat.includes("1700.0000") && !flat.includes("1650.0000"),
  );

  // =========================================================================
  // 6. THE REFUSALS AN OPERATOR CAN REACH.
  // =========================================================================
  await altMac("v", 800);
  const combo2 = page.getByRole("combobox", { name: /New method/i }).first();
  await combo2.click();
  await combo2.fill("LIFO");
  await page.waitForTimeout(250);
  await press("Enter", 1500);
  rec(
    "re-asserting the SAME method says so and changes nothing (409, not a silent success)",
    /already valued at LIFO/i.test(await text("stock-valuation-status")),
    (await text("stock-valuation-status")).slice(0, 120),
  );
  rec(
    "the figure did not move on a refused switch",
    (await text("stock-total-value")) === "1,600.00",
    await text("stock-total-value"),
  );
} finally {
  // ⛔ EVERY 4xx THE BROWSER SAW IS PRINTED. A console error is not a finding by
  // itself, but a silent 403 on a screen that rendered fine is how a refusal
  // gets mistaken for a feature — the Stock Summary is DENIED to Data Entry by
  // rule, so a 403 here would mean this probe was reading a screen it should
  // not have been able to open.
  console.log("\n--- non-GET browser requests (status last) ---");
  for (const r of net) console.log("  " + r);
  console.log("\n--- console errors ---");
  for (const e of app.consoleErrors.slice(0, 8)) console.log("  " + e);
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);
