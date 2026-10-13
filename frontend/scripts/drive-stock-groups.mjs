/**
 * T10 — STOCK GROUPS AND COMPOUND UNITS, driven from the keyboard in a real
 * browser.
 *
 * ⛔ WHAT THIS PROVES, AND WHY IT NEEDED A BROWSER
 * ===========================================================================
 * Migration 0079, `masters.CreateStockGroup`, `rollupStockGroups` and the
 * restatement in `stock_summary.go` can all be green while NO OPERATOR HAS
 * EITHER FEATURE — which is precisely what happened to order processing (whole
 * backend, no screen) and to payroll (complete engine, two missing writers). So
 * this probe never calls a service: it presses Alt+G four times, keys the two
 * groups, the compound unit and four stock items THROUGH THE FORMS, and then
 * reads the subtotals and the restated quantity OFF THE PAGE.
 *
 * ⛔ THE TWO ORACLES, both hand-derived and both chosen so that the wrong answer
 * fails an assertion rather than looking plausible:
 *
 *   1. A GROUP SUBTOTAL IS THE WHOLE BRANCH.
 *
 *        Provisions                       2,450.00   <- 250 direct + the branch
 *          Provisions > Grains            2,200.00   <- Rice 1,000 + Carton 1,200
 *        (ungrouped)                         20.00
 *        --------------------------------------------
 *        the company's closing stock      2,470.00   <- NOT 4,670.00
 *
 *      A rollup that counted only DIRECT items prints Provisions at 250.00 —
 *      a parent SMALLER than its own child, with nothing on the page to explain
 *      the gap. And the three lines deliberately do not sum to the total: an
 *      item counts in every ancestor, so the table carries no total and says why.
 *
 *   2. A COMPOUND QUANTITY IS STORED IN THE ITEM'S OWN UNIT. Four boxes of
 *      twelve close at 4.0000 with 48.0000 Nos restated BESIDE them. A build
 *      that multiplied on the way in reads 48.0000 in the Qty column, which
 *      disagrees with the purchase invoice about what was bought.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY OF ITS OWN, NOT ON Demo Traders — posting
 * purchases moves a trial balance, and residue in the demo tenant reads as
 * broken books to the next lane and drops `verify:demo` below 34/34.
 *
 * ⛔ SYMBOL-CHECK THE RUNNING `cored` BEFORE BELIEVING A GREEN RUN. `make
 * run-cored` is `go run`, so a stale process is indistinguishable from a fix
 * that does not work:
 *
 *   strings "$(ps -o command= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)" \
 *     | awk '{print $1}')" | grep -c stock-groups
 *
 * RUN:  node frontend/scripts/drive-stock-groups.mjs
 *       (needs cored :8087 and vite :5173 — the port matters, WebAuthn origin
 *        matching is exact. It creates its own company.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Stock Group Drive ${stamp}`;

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

// ---- The tenant and the two ledgers the purchases need, over HTTP as the SAME
// subject the browser signs in as.
//
// ⚠️ THE VOUCHERS ARE POSTED OVER HTTP ON PURPOSE. They are not what this probe
// is about, and keying four item invoices through the grid would make a group
// rollup failure indistinguishable from a voucher-entry failure. THE GROUPS, THE
// UNIT AND THE ITEMS ARE KEYED IN THE BROWSER, because they are the feature.
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
    `drive-stock-groups: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
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
const GODOWN = (
  await j("GET", `${base}/godowns`, { token: TOK })
).body.godowns.find((g) => g.is_system).id;

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, altMac, toGateway, openFromGateway, net } = app;

const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const text = async (t) =>
  (await seen(t))
    ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim()
    : "";
const type = async (t, v) => {
  const el = testid(t).first();
  await el.click();
  await el.fill(v);
};
const choose = async (t, v) => {
  await testid(t).first().selectOption(v);
  await page.waitForTimeout(150);
};
const options = async (t) => testid(t).first().locator("option").allInnerTexts();

/** Alt+G, type the label, Enter — the documented Go To route. */
const goTo = async (label, wait = 1200) => {
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

/** Key one stock group through the form. `parent` empty = top of the tree. */
const createGroup = async (name, parent) => {
  await goTo("Create Stock Group");
  if (!(await seen("master-create:stock-group"))) {
    throw new Error(`Alt+G > Create Stock Group did not open (${name})`);
  }
  await type("master-name", name);
  if (parent) await pickFromList("Under (parent stock group)", parent);
  await press("Control+a", 1500);
};

/** Key one stock item through the form. */
const createItem = async (name, unit, group) => {
  await goTo("Create Stock Item");
  if (!(await seen("master-create:stock-item"))) {
    throw new Error(`Alt+G > Create Stock Item did not open (${name})`);
  }
  await type("master-name", name);
  await pickFromList("Units", unit);
  if (group) {
    // The picker is SERVED (GET /stock-groups) and arrives a tick after the
    // form, so wait for the option rather than selecting into an empty list.
    await page.waitForTimeout(400);
    await choose("master-item-stock-group", group);
  }
  await press("Control+a", 1500);
};

try {
  // =========================================================================
  // 1. THE HIERARCHY, KEYED. Alt+G > Create Stock Group, twice.
  // =========================================================================
  await goTo("Create Stock Group");
  rec(
    "Alt+G > Create Stock Group opens the stock group master",
    await seen("master-create:stock-group"),
  );
  // ⛔ THE PARENT PICKER IS SERVED, AND ON A FRESH COMPANY IT OFFERS NOTHING.
  // There are no built-in stock groups: every option is one the operator made,
  // so a hardcoded list (the way the LEDGER form seeds Tally's account groups)
  // would offer names the server answers 404 for AFTER the master is keyed.
  await pickFromListSafe();
  const offeredBefore = await page.getByRole("option").count();
  rec(
    "the parent picker offers NOTHING on a company with no groups (there are no built-ins)",
    offeredBefore === 0,
    `${String(offeredBefore)} option(s) offered`,
  );
  await type("master-name", "Provisions");
  await press("Control+a", 1500);
  rec(
    "a top-level group is created and the status line says where it went",
    /top of the item tree/i.test(await status()),
    (await status()).slice(0, 120),
  );

  await createGroup("Grains", "Provisions");
  rec(
    "a CHILD group is created under the parent that was just keyed",
    /under Provisions/i.test(await status()),
    (await status()).slice(0, 120),
  );
  // ⛔ POSTGRES IS THE ORACLE FOR THE SHAPE. A 201 with the parent discarded is
  // indistinguishable from a working write on the status line (register row
  // D43), and a child that lost its parent subtotals into the wrong branch.
  const parentOf = await sql(
    `SELECT COALESCE(p.name, '(none)')
       FROM stock_group g LEFT JOIN stock_group p ON p.id = g.parent_id
      WHERE g.company_id = ${q(CO)} AND g.name = ${q("Grains")}`,
  );
  rec("stock_group.parent_id in POSTGRES points at Provisions", parentOf === "Provisions", parentOf);
  const pathShape = await sql(
    `SELECT (SELECT path::text FROM stock_group WHERE company_id = ${q(CO)} AND name = ${q("Grains")})
            LIKE (SELECT path::text FROM stock_group WHERE company_id = ${q(CO)} AND name = ${q("Provisions")}) || '.%'`,
  );
  rec(
    "the LTREE path of the child extends the parent's, which is what makes a branch rollup one predicate",
    pathShape === "t",
    pathShape,
  );

  // =========================================================================
  // 2. THE COMPOUND UNIT, KEYED. 1 BOX = 12 Nos.
  // =========================================================================
  await goTo("Create Unit");
  rec("Alt+G > Create Unit opens the unit master", await seen("master-create:unit"));
  await type("master-name", "BOX");
  await type("master-unit-decimals", "0");
  await page.waitForTimeout(400);
  const baseOptions = await options("master-unit-base");
  rec(
    "the base-unit picker offers the company's OWN units (a base must exist before something is measured in it)",
    baseOptions.some((o) => /^Nos$/.test(o.trim())),
    baseOptions.join(" | "),
  );
  await choose("master-unit-base", "Nos");
  await type("master-unit-conversion", "12");
  rec(
    "choosing a base states the CONSEQUENCE before the save: quantities stay in the outer unit",
    /restates the closing quantity/i.test(await text("master-unit-compound-note")),
    (await text("master-unit-compound-note")).slice(0, 110),
  );
  await press("Control+a", 1500);
  rec(
    "the status line states the conversion that was stored",
    /1 BOX = 12\.0000 Nos/.test(await status()),
    (await status()).slice(0, 120),
  );
  const storedUnit = await sql(
    `SELECT COALESCE(b.symbol, '(none)') || '/' || COALESCE(u.conversion_factor::text, '(null)')
       FROM unit u LEFT JOIN unit b ON b.id = u.base_unit_id
      WHERE u.company_id = ${q(CO)} AND u.symbol = ${q("BOX")}`,
  );
  rec(
    "unit.base_unit_id + conversion_factor in POSTGRES say 1 BOX = 12 Nos",
    /^Nos\/12(\.0+)?$/.test(storedUnit),
    storedUnit,
  );

  // =========================================================================
  // 3. FOUR ITEMS: two in the CHILD group (one of them in BOX), one DIRECTLY in
  //    the parent, one in no group at all.
  // =========================================================================
  await createItem("Rice", "Nos", "Grains");
  await createItem("Carton", "BOX", "Grains");
  await createItem("Sugar", "Nos", "Provisions");
  await createItem("Loose", "Nos", "");
  const filed = await sql(
    `SELECT string_agg(si.name || '=' || COALESCE(g.name, ''), ',' ORDER BY si.name)
       FROM stock_item si LEFT JOIN stock_group g ON g.id = si.stock_group_id
      WHERE si.company_id = ${q(CO)}`,
  );
  rec(
    "every item was FILED as keyed — including the ungrouped one, which is a real state",
    filed === "Carton=Grains,Loose=,Rice=Grains,Sugar=Provisions",
    filed,
  );

  // =========================================================================
  // 4. THE MOVEMENTS, over HTTP. Rice 1,000 · Carton 1,200 · Sugar 250 · Loose 20.
  // =========================================================================
  const itemID = async (name) =>
    sql(`SELECT id::text FROM stock_item WHERE company_id = ${q(CO)} AND name = ${q(name)}`);
  let no = 0;
  for (const [name, qty, rate, amount] of [
    ["Rice", "10", "100", "1000"],
    ["Carton", "4", "300", "1200"],
    ["Sugar", "5", "50", "250"],
    ["Loose", "2", "10", "20"],
  ]) {
    no += 1;
    const r = await j("POST", `${base}/vouchers/purchase`, {
      token: TOK,
      body: {
        voucher_no: `PUR-${String(no)}`,
        voucher_date: "2026-04-01",
        created_by: "drive",
        postings: [
          { ledger_id: PURCHASES, dr_cr: "D", amount },
          { ledger_id: SUPPLIER, dr_cr: "C", amount },
        ],
        inventory: [
          {
            stock_item_id: await itemID(name),
            godown_id: GODOWN,
            batch_id: "",
            direction: "IN",
            qty,
            rate,
            amount,
          },
        ],
      },
    });
    if (r.status !== 201) throw new Error(`PUR-${String(no)}: ${r.status} ${JSON.stringify(r.body)}`);
  }

  // =========================================================================
  // 5. THE STOCK SUMMARY: THE BRANCH SUBTOTAL AND THE RESTATED QUANTITY, READ
  //    OFF THE PAGE.
  // =========================================================================
  await openFromGateway("Stock Summary", 2500);
  await page.waitForTimeout(700);
  rec("the Stock Summary opens", await seen("stock-summary-table"));
  rec("it carries the stock group subtotal table", await seen("stock-group-subtotals"));

  const groupLine = async (name) => {
    const row = testid(`stock-group:${name}`).first();
    if ((await row.count()) === 0) return "(absent)";
    return (await row.innerText()).replace(/\s+/g, " ").trim();
  };
  const provisions = await groupLine("Provisions");
  const grains = await groupLine("Grains");
  const ungrouped = await groupLine("");
  // ⛔ THE ORACLE. Provisions holds Sugar (250) DIRECTLY and Grains (2,200)
  // BENEATH it, so the branch is 2,450.00. A direct-only rollup prints 250.00
  // here — a parent smaller than its own child.
  rec(
    "the PARENT prints its whole BRANCH: 2,450.00 (direct-only would say 250.00)",
    /\b2,450\.00$/.test(provisions),
    provisions,
  );
  rec("the child prints 2,200.00 — Rice 1,000 plus Carton 1,200", /\b2,200\.00$/.test(grains), grains);
  rec("the ungrouped bucket is named and printed, never dropped", /\(ungrouped\)/.test(ungrouped) && /\b20\.00$/.test(ungrouped), ungrouped);
  rec(
    "the parent's ITEM COUNT is the branch too (3), or the two columns of one line disagree",
    / 3 /.test(provisions.replace(/\s+/g, " ")),
    provisions,
  );

  const total = await text("stock-total-value");
  rec(
    "the company's closing stock is 2,470.00 — NOT the 4,670.00 the three lines add up to",
    total === "2,470.00",
    total,
  );
  const groupTable = await text("stock-group-subtotals");
  rec(
    "the table says out loud that its rows must not be added, where a reader would reach for a calculator",
    /No total/i.test(groupTable) &&
      /double-count/i.test(groupTable) &&
      /Stock-in-Hand/.test(groupTable),
    groupTable.slice(-170),
  );

  // ---- The restatement.
  const cartonBase = await text("stock-base-qty:Carton");
  rec(
    "the compound item is restated in its BASE unit: 48.0000 Nos",
    cartonBase === "48.0000 Nos",
    cartonBase,
  );
  const cartonRow = (await testid("stock-item:Carton").first().innerText())
    .replace(/\s+/g, " ")
    .trim();
  // ⛔ AND THE STORED FIGURE IS UNTOUCHED. 48.0000 in the Qty column is a Stock
  // Summary that disagrees with the purchase invoice about what was bought.
  rec(
    "the QUANTITY on the item line is still 4.0000 — the keyed figure, in the item's own unit",
    / 4\.0000 /.test(` ${cartonRow} `),
    cartonRow,
  );
  const riceBase = await text("stock-base-qty:Rice");
  rec(
    "a SIMPLE unit prints an em dash, not 0.0000 — a zero would be a claim about the goods",
    riceBase === "—",
    riceBase,
  );

  // =========================================================================
  // 6. THE TWO REFUSALS AN OPERATOR CAN ACTUALLY REACH.
  // =========================================================================
  // (a) Half a conversion — refused BEFORE the request, naming the field.
  await goTo("Create Unit");
  await type("master-name", "CRATE");
  await page.waitForTimeout(400);
  await choose("master-unit-base", "Nos");
  await press("Control+a", 1500);
  rec(
    "a base with NO factor is refused, and the message asks the question the operator must answer",
    /1 CRATE = \? Nos/.test(await status()) && /Nothing was created/i.test(await status()),
    (await status()).slice(0, 140),
  );
  const crate = await sql(
    `SELECT count(*) FROM unit WHERE company_id = ${q(CO)} AND symbol = ${q("CRATE")}`,
  );
  rec("...and nothing was written", crate === "0", crate);

  // (b) A CHAIN — a base that is itself compound. The FORM cannot know this
  // (it would need the base unit's own base), so the refusal comes from the
  // server and must REACH the operator rather than dying in a console.
  await goTo("Create Unit");
  await type("master-name", "PALLET");
  await page.waitForTimeout(400);
  await choose("master-unit-base", "BOX");
  await type("master-unit-conversion", "20");
  await press("Control+a", 2000);
  rec(
    "a PALLET of 20 BOX of 12 Nos is REFUSED as a chain, in words, on the status line",
    /ONE level/i.test(await status()),
    (await status()).slice(0, 160),
  );
  const pallet = await sql(
    `SELECT count(*) FROM unit WHERE company_id = ${q(CO)} AND symbol = ${q("PALLET")}`,
  );
  rec("...and nothing was written for it either", pallet === "0", pallet);
} finally {
  // ⛔ EVERY non-GET THE BROWSER SENT IS PRINTED. A silent 403 on a screen that
  // rendered fine is how a refusal gets mistaken for a feature.
  console.log("\n--- non-GET browser requests (status last) ---");
  for (const r of net) console.log("  " + r);
  console.log("\n--- console errors ---");
  for (const e of app.consoleErrors.slice(0, 8)) console.log("  " + e);
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);

/**
 * Open the parent SelectionList without choosing anything, so its option count
 * can be READ. Clicking the combobox is what renders the list at all — a count
 * taken before the click is zero for the wrong reason.
 */
async function pickFromListSafe() {
  const combo = page
    .getByRole("combobox", { name: "Under (parent stock group)" })
    .first();
  await combo.click();
  await page.waitForTimeout(400);
}
