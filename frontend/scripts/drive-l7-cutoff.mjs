/**
 * D29 + D33 — the last two L7 rows.
 *
 * D29: on the four stock-only screens EVERY 403 was rendered as an ACL refusal
 * ("ask an Owner to allow 'create delivery-note' on your level"), so an operator
 * refused by the BACK-DATED CUTOFF was sent to fix a permission they already
 * hold, and was never told a period was closed or when it closed.
 *
 * D33: re-opening the Day Book after any drill froze the drill target at a stale
 * id, so Enter opened a DIFFERENT voucher than the highlighted row.
 *
 * PRECONDITION: the Owner level carries backdated_cutoff = 2026-07-01.
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, altMac, toGateway } = app;
const status = () => page.getByTestId("status-line").innerText().catch(() => "");

// ============================================================ D29
await toGateway();
await page.keyboard.down("Alt"); await page.keyboard.press("F8"); await page.keyboard.up("Alt");
await page.waitForTimeout(1400);
rec("Alt+F8 opens a Delivery Note", /Delivery Note/i.test(await body()));

await page.getByTestId("stock-date").fill("2026-05-15");     // ⛔ BEFORE the cutoff
await page.waitForTimeout(200);
await page.getByTestId("stock-party").fill("Acme Traders");
await page.waitForTimeout(200);
const item = page.getByTestId("stock-item:0");
await item.click(); await page.keyboard.type("MS Angle"); await page.waitForTimeout(500);
await press("Enter", 400);
await page.getByTestId("stock-qty:0").fill("1");
await page.waitForTimeout(200);
const rateBox = page.getByTestId("stock-rate:0");
if (await rateBox.count()) { await rateBox.fill("25"); await page.waitForTimeout(200); }

const before = app.net.length;
await press("Control+a", 5000);
const wire = app.net.slice(before);
const screenText = await body();
const refusal =
  (await page.getByTestId("stock-masters-error").innerText().catch(() => "")) ||
  (screenText.match(/(Error|Refused|refused)[^\n]{0,400}/) ?? [""])[0] ||
  (await status());

rec(
  "the back-dated write IS refused",
  wire.some((w) => /40[13]/.test(w)) || /refus|cutoff|closed|before/i.test(refusal),
  `wire: ${wire.join(" · ") || "(none)"}`,
);
rec(
  "D29: ⛔ the refusal is NOT dressed as a security-level problem",
  !/ask an Owner to allow|security level/i.test(refusal),
  refusal.replace(/\n/g, " ").slice(0, 220) || "<nothing on screen>",
);
rec(
  "D29: ⛔ and it names the CUTOFF — the operator learns the period is closed",
  /back-dated|cutoff|closed period|2026-07-01/i.test(refusal),
  refusal.replace(/\n/g, " ").slice(0, 220) || "<nothing on screen>",
);
rec(
  "D29: RULE 4 — the refusal wrote no voucher",
  !wire.some((w) => /POST .*\/vouchers\/.* 201/.test(w)),
  wire.filter((w) => /vouchers/.test(w)).join(" · ") || "(no voucher writes)",
);

// ============================================================ D33
await toGateway();
await altMac("g"); await page.waitForTimeout(300);
await page.keyboard.type("Day Book"); await page.waitForTimeout(400); await press("Enter", 2000);
const rows = page.locator('[role="grid"] [role="row"]');
const first = (await rows.nth(0).innerText()).replace(/\n/g, " | ");
await rows.nth(0).click(); await page.waitForTimeout(250);
await press("Enter", 2500);
const firstHeading = (await page.locator("h1").first().innerText().catch(() => "")) + " " + (await body()).slice(0, 120);
rec("D33: drilling the first row opens a voucher", /Voucher/i.test(firstHeading), first.slice(0, 60));

// Back out, re-open the Day Book, and drill a DIFFERENT row.
await toGateway();
await altMac("g"); await page.waitForTimeout(300);
await page.keyboard.type("Day Book"); await page.waitForTimeout(400); await press("Enter", 2000);
const target = 3;
const targetText = (await rows.nth(target).innerText()).replace(/\n/g, " | ");
await rows.nth(target).click(); await page.waitForTimeout(250);
await press("Enter", 2500);
const opened = await body();
// The row names a party or a narration; the opened voucher must show the SAME one.
const label = (targetText.split("|")[2] ?? "").trim().slice(0, 18);
rec(
  "D33: ⛔ after a previous drill, Enter opens the HIGHLIGHTED row — not a stale target",
  label !== "" && opened.includes(label),
  `row 3 said "${label}"; the opened voucher ${opened.includes(label) ? "names it" : "does NOT name it"}`,
);

console.log("\nNON-GET WIRE:", app.net.join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
