/** L8 — ORDERS + BOM. D6, D7, D44, D68, D69, D70, D71. */
import { openApp, rec, summary } from "./audit-harness.mjs";
const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, altMac, toGateway } = app;
const status = () => page.getByTestId("status-line").innerText().catch(() => "");

const TOKEN = await (async () => {
  const r = await fetch("http://localhost:8087/auth/token", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject: "browser-dev-owner", amr: ["webauthn"] }),
  });
  return (await r.json()).token;
})();
// \u26a0 THE ORDER BOOKS ARE MOUNTED AT THE COMPANY ROOT, not under /reports —
// `/reports/sales-order-book` answers 403 ("unmapped route: access denied"),
// which is D40's shape and reads exactly like a permission problem.
const api = async (path) => {
  const r = await fetch(`http://localhost:8087/v1/companies/${process.env.CO}${path}`, {
    headers: { Authorization: `Bearer ${TOKEN}` },
  });
  return { status: r.status, body: await r.json().catch(() => ({})) };
};

// ============================= D6 — the party a SALES order may be committed to
// \u26a0 TWO DIFFERENT SCREENS WITH SIMILAR NAMES. "Sales Orders" is the
// REGISTER (a Gateway entry, where an order is created); "Sales Order Book" is
// what is still outstanding as at a date (a REPORT_NAME, reached by Alt+G). The
// first run asked the Gateway for "Sales Order Register" — which is neither.
await app.openFromGateway("Sales Orders", 2200);
rec("the Sales Orders REGISTER opens from the Gateway", /Order/i.test(await body()), (await body()).slice(0, 60).replace(/\n/g, " | "));

// \u26a0 THE PARTY PICKER ONLY EXISTS AFTER Alt+C. The register itself is a list;
// "New Order" is what asks who the commitment is to. The first run inspected the
// list screen, found no combobox, and reported "the picker offered nothing".
await altMac("c");
await page.waitForTimeout(1200);
rec("Alt+C opens a New Order form", /New Order|Customer|Party/i.test(await body()), (await body()).slice(0, 70).replace(/\n/g, " | "));

// \u26a0 THE CUSTOMER FIELD IS A NATIVE <select>, NOT A COMBOBOX. The first run
// looked for input[role="combobox"] and reported "the picker offered nothing".
const customer = page.getByLabel("Customer", { exact: true });
const offered = (await customer.locator("option").allInnerTexts()).map((t) => t.trim()).filter((t) => t && !/choose/i.test(t));
// \u26d4 D6's ORACLE: a SALES order is a commitment BY A CUSTOMER, so the list
// must offer debtors — never a bank, a tax head or a capital account. Committing
// a sales order to "HDFC Bank" used to be accepted by the client, by Validate
// AND by the database.
const nonParty = offered.filter((o) => /HDFC|ICICI|Bank|Cash|Capital|CGST|SGST|IGST|Purchases|Domestic Sales|Rent|Electricity/i.test(o));
rec(
  "D6: \u26d4 the Customer list offers NO bank, tax, capital or nominal ledger",
  offered.length > 0 && nonParty.length === 0,
  offered.length === 0 ? "the list offered nothing at all" : `offered: ${offered.join(", ")}${nonParty.length ? ` | \u26d4 NON-PARTY: ${nonParty.join(", ")}` : ""}`,
);
rec(
  "D6: and the screen SAYS why the list is restricted",
  /under Sundry Debtors/i.test(await body()),
  (((await body()).match(/Customer[^|\n]{0,40}/) ?? [""])[0]).trim(),
);

// \u26d4 THE ESCAPE HATCH IS THE INTERESTING HALF. The client offers "Offer every
// ledger" for a customer kept in a SUB-GROUP that this list cannot see — and the
// screen's own text promises "the server still refuses anything outside Sundry
// Debtors and says so". That promise is worth driving: a widened client picker
// with no server refusal behind it is D6 again with a checkbox in front of it.
const widen = page.getByLabel("Offer every ledger", { exact: false });
rec("D6: the widening escape hatch exists", (await widen.count()) === 1);
if (await widen.count()) {
  await widen.check();
  await page.waitForTimeout(600);
  const wide = (await customer.locator("option").allInnerTexts()).map((t) => t.trim()).filter((t) => t && !/choose/i.test(t));
  rec(
    "D6: ticking it widens the list (so the hatch is real)",
    wide.length > offered.length,
    `${offered.length} debtors -> ${wide.length} ledgers`,
  );
  const bank = wide.find((o) => /HDFC|Bank/i.test(o));
  if (bank) {
    await customer.selectOption({ label: bank });
    await page.getByLabel("Quantity on line 1").fill("5");
    await page.getByLabel("Rate on line 1").fill("100");
    const itemSel = page.getByLabel("Item on line 1");
    const opts = await itemSel.locator("option").allInnerTexts();
    const realItem = opts.map((t) => t.trim()).find((t) => t && !/choose/i.test(t));
    if (realItem) await itemSel.selectOption({ label: realItem });
    await page.waitForTimeout(300);
    const before = app.net.length;
    await press("Control+a", 4000);
    const wire = app.net.slice(before);
    const refusedOnWire = wire.some((w) => /POST .*orders.* 4\d\d/.test(w));
    const created = wire.some((w) => /POST .*orders.* 201/.test(w));
    rec(
      "D6: \u26d4 committing a sales order to a BANK is REFUSED — the promise holds",
      refusedOnWire && !created,
      `wire: ${wire.filter((w) => /order/i.test(w)).join(" · ") || "(no order write at all)"} | screen: ${(((await body()).match(/(Error|refus)[^\n]{0,140}/) ?? [""])[0])}`,
    );
  }
}

// ============================= D44 — overdue in the DEFAULT (no as-at) state
const ob = await api("/sales-order-book");
rec("the Sales Order Book answers with no as_on", ob.status === 200, `HTTP ${ob.status}`);
if (ob.status === 200) {
  const rows = ob.body.rows ?? ob.body.orders ?? [];
  const flagged = rows.filter((r) => r.overdue === true || r.is_overdue === true);
  const hasDue = rows.filter((r) => (r.due_date ?? "") !== "");
  rec(
    "D44: ⛔ the DEFAULT view can flag an overdue commitment",
    hasDue.length === 0 || flagged.length > 0 || rows.every((r) => (r.balance_qty ?? "0") === "0.0000"),
    `${rows.length} row(s), ${hasDue.length} with a due date, ${flagged.length} flagged overdue`,
  );
  const asOn = await api("/sales-order-book?as_on=2027-03-31");
  const flagged2 = (asOn.body.rows ?? []).filter((r) => r.overdue === true || r.is_overdue === true);
  rec(
    "D44: and an explicit as_on agrees with the default rather than contradicting it",
    ob.status === 200 && asOn.status === 200 && flagged.length <= flagged2.length,
    `default ${flagged.length} overdue vs as_on=2027-03-31 ${flagged2.length}`,
  );
}

// ============================= D71 — the purchase column's own word
await toGateway();
await altMac("g"); await page.waitForTimeout(300);
await page.keyboard.type("Purchase Order Book"); await page.waitForTimeout(400);
await press("Enter", 2200);
const pob = await body();
rec("the Purchase Order Book opens", /Purchase Order|Order Book/i.test(pob), pob.slice(0, 60).replace(/\n/g, " | "));
rec(
  "D71: ⛔ a PURCHASE order book says 'Received', not 'Delivered'",
  /Received/i.test(pob) && !/Delivered/i.test(pob),
  (pob.match(/Received|Delivered/g) ?? ["<neither word on screen>"]).join(", "),
);

console.log("\nNON-GET WIRE:", app.net.join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
