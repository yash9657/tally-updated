/**
 * D70 — after Alt+W pre-closes an order, the register's line panel kept showing
 * that order's balance as "Outstanding" with no pre-closed marker, while the row
 * itself had vanished from the list above it.
 */
import { openApp, rec, summary } from "./audit-harness.mjs";
const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, altMac } = app;

await app.openFromGateway("Sales Orders", 2200);
rec("the Sales Orders register opens", /Sales Orders/i.test(await body()));

// Highlight the order we created for D7 — the one we can afford to pre-close.
const target = page.getByLabel(/Select order ZZ-OLDEST-001/i);
rec("the probe's own order is listed", (await target.count()) === 1);
if (await target.count()) {
  await target.click();
  await page.waitForTimeout(700);
  const before = await body();
  rec("its lines panel shows the outstanding balance", /Outstanding|outstanding/.test(before));

  await altMac("w");
  await page.waitForTimeout(900);
  const dialog = await body();
  // A pre-close asks for a REASON (an order closed with no reason is unauditable).
  const reason = page.getByLabel(/reason/i).first();
  if (await reason.count()) {
    await reason.fill("d70 probe: the customer cancelled");
    await page.waitForTimeout(200);
    await press("Enter", 2500);
  } else {
    await press("Enter", 2500);
  }

  const after = await body();
  rec(
    "D70: the order is gone from the OUTSTANDING list",
    !/Select order ZZ-OLDEST-001/i.test(await page.locator("body").innerHTML()) ||
      (await page.getByLabel(/Select order ZZ-OLDEST-001/i).count()) === 0,
    (after.match(/\d+ order\(s\)/) ?? [""])[0],
  );
  rec(
    "D70: ⛔ and the panel below does NOT still call its balance Outstanding with no marker",
    !/Outstanding/.test(after) || /pre-clos/i.test(after),
    (after.match(/.{0,60}(Outstanding|pre-clos).{0,60}/i) ?? ["<panel says neither>"])[0].replace(/\n/g, " | "),
  );
}

console.log("\nNON-GET WIRE:", app.net.join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
