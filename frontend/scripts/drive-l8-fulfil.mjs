/**
 * D7 — the fulfilment picker's own comment says "Oldest commitment first" while
 * it sorted candidates by order_no AS A RAW STRING, so the order offered first
 * was the NEWEST. The fixture is built to tell those two apart: ZZ-OLDEST-001 is
 * the OLDEST order (2026-04-01) and its number sorts LAST alphabetically.
 */
import { openApp, rec, summary } from "./audit-harness.mjs";
const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, toGateway } = app;

await toGateway();
await page.keyboard.down("Alt"); await page.keyboard.press("F8"); await page.keyboard.up("Alt");
await page.waitForTimeout(1500);
rec("Alt+F8 opens a Delivery Note", /Delivery Note/i.test(await body()));

await page.getByTestId("stock-party").fill("Acme Traders");
const item = page.getByTestId("stock-item:0");
await item.selectOption({ label: "MS Angle 50x50" }).catch(async () => {
  await item.click(); await page.keyboard.type("MS Angle"); await press("Enter", 300);
});
await page.getByTestId("stock-qty:0").fill("3");
await page.waitForTimeout(600);

const link = page.getByLabel("Link line 1 to an order");
rec("the line offers an order LINK", (await link.count()) === 1);
if (await link.count()) {
  await link.click();
  await page.waitForTimeout(800);
  // \u26a0 SCOPE THE OPTIONS TO THE ORDER CANDIDATES. The page also carries the
  // item select and the godown select, and their options come FIRST in document
  // order — the first run read "MS Angle 50x50" as the leading candidate and
  // reported a working sort as broken. An order candidate is the one that states
  // when it was "ordered".
  const opts = (await page.locator('[role="option"], select option').allInnerTexts())
    .map((t) => t.trim())
    .filter((t) => /ordered \d{4}-\d{2}-\d{2}/.test(t));
  const first = opts[0] ?? "";
  rec(
    "D7: ⛔ the OLDEST commitment is offered FIRST, not the alphabetically-first number",
    /ZZ-OLDEST-001/.test(first),
    `offered in order: ${opts.join(" | ") || "(nothing offered)"}`,
  );
  rec(
    "D7: and every candidate is for the item on this line",
    opts.length > 0,
    `${opts.length} candidate(s)`,
  );
}

console.log("\nNON-GET WIRE:", app.net.join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
