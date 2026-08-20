/**
 * L10's DRIVE — the half that was never paid on D9, D45, D46, D53, D64.
 * Rows 29, 34, 43 of the parity checklist, driven against real cored.
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, altMac, toGateway } = app;

const goTo = async (name, wait = 1800) => {
  await toGateway();
  await altMac("g");
  await page.waitForTimeout(300);
  await page.keyboard.type(name);
  await page.waitForTimeout(400);
  await press("Enter", wait);
};

// ---------------------------------------------------------------- STOCK SUMMARY
await goTo("Stock Summary");
let txt = await body();
rec("Stock Summary opens from Alt+G", /Stock Summary/.test(txt), txt.slice(0, 60).replace(/\n/g, " | "));

// D45 — the scope caption over an UNBOUNDED report.
const scope = await page.getByTestId("stock-summary-scope").innerText().catch(() => "<no scope element>");
rec(
  "D45: unbounded report is NOT captioned 'as at today'",
  /NO DATE LIMIT/.test(scope) && !/As at today/.test(scope),
  scope.replace(/\n/g, " "),
);

// D64 — one column list, every quantity with its value beside it.
const headers = await page.locator('[data-testid="stock-summary-table"] thead th').allInnerTexts().catch(() => []);
rec("D64: 11 columns, Inward Value present", headers.length === 11 && headers.includes("Inward Value"), headers.join(" / "));
rec(
  "D64: Inward Qty and Inward Value are ADJACENT and named",
  headers.indexOf("Inward Value") === headers.indexOf("Inward Qty") + 1,
  `qty@${headers.indexOf("Inward Qty")} value@${headers.indexOf("Inward Value")}`,
);

// The inward money figure that used to be written down nowhere.
const firstRow = await page.locator('[data-testid="stock-summary-table"] tbody tr').first().allInnerTexts().catch(() => []);
rec("D64: a row renders an inward VALUE cell", (firstRow[0] ?? "").split("\t").length >= 10, (firstRow[0] ?? "").replace(/\n/g, " | ").slice(0, 160));

// D45 second half — F2 a To date, and the caption must follow the SERVER's echo.
await press("F2", 700);
// \u26d4 F2 FOCUSES `period-from`, AND ONLY `To` APPLIES TO A POSITION REPORT —
// the dialog says so on its face. Typing the date into the focused field leaves
// `To` empty, the report stays UNBOUNDED, and the caption correctly keeps saying
// so. That is the screen being right and the probe being wrong; the first run of
// this file logged it as a defect.
const toBox = page.getByTestId("period-to");
rec("D45: the F2 dialog offers a To bound", (await toBox.count()) === 1);
await toBox.fill("2026-06-30");
await press("Enter", 2000);
const scope2 = await page.getByTestId("stock-summary-scope").innerText().catch(() => "");
rec("D45: an F2 To date is stated as the boundary", /As at 2026-06-30/.test(scope2), scope2.replace(/\n/g, " ").slice(0, 90));

// \u26d4 AND THE FIGURES MUST MOVE WITH THE CAPTION. A caption that changes over
// an unchanged table is the same lie in a new place.
const bounded = await page.locator('[data-testid="stock-summary-table"] tbody tr').first().innerText();
rec("D45: the figures under the caption moved with it", !bounded.includes("6,162.15"), bounded.replace(/\n|\t/g, " | ").slice(0, 120));

// ---------------------------------------------------------------- BALANCE SHEET
await goTo("Balance Sheet");
txt = await body();
rec("Balance Sheet opens", /Balance Sheet/.test(txt));

// D9 — Closing Stock must be on the DEFAULT (condensed) view, with its ancestors.
rec("D9: Closing Stock is visible in the CONDENSED view", /Closing Stock/.test(txt), /Stock-in-Hand/.test(txt) ? "Stock-in-Hand present too" : "NO Stock-in-Hand group");
rec("D9: its enclosing groups came with it", /Stock-in-Hand/.test(txt) && /Current Assets/.test(txt));

// D46 — the P&L line names the period it actually covers.
const pl = (txt.match(/Profit & Loss A\/c \([^)]*\)/) ?? ["<no P&L line>"])[0];
rec("D46: the P&L line does NOT claim '(Current Period)'", !/\(Current Period\)/.test(pl), pl);
rec("D46: it names the cumulative boundary instead", /cumulative to/.test(pl), pl);

console.log("\nNON-GET WIRE:", app.net.join(" · ") || "(none)");
if (app.consoleErrors.length) console.log("CONSOLE ERRORS:", app.consoleErrors.slice(0, 5));
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
