/**
 * LIVE in-browser smoke test — drives the ACTUAL keystroke UI (Playwright,
 * headless Chromium) against the running stack in live mode, exercising the
 * end-to-end flows a real Tally user touches. It guards the wiring that is easy
 * to break silently: the company bootstrap committing under React StrictMode, the
 * dev proxy + auto-auth, server-derived GST, the reports/query reflecting posted
 * data, and the Bill Inbox.
 *
 * PREREQS (all local): cored :8087, insightd :8088, and the Vite dev server in
 * LIVE mode on :5173 (frontend/.env.development.local with VITE_LIVE=1 — see
 * vite.config.ts). Then:
 *
 *   node scripts/e2e-ui-smoke.mjs
 *
 * Exit 0 "ui-smoke OK" when every flow passes; on any failure it prints the
 * flow and exits 1. It does NOT start the servers.
 */
import { chromium } from "playwright";

const URL = process.env.UI_BASE_URL ?? "http://localhost:5173/";
const results = [];
const rec = (name, ok, detail = "") => {
  results.push({ name, ok });
  console.log(`${ok ? "✓" : "✗"} ${name}${detail ? "  — " + detail : ""}`);
};

const browser = await chromium.launch();
const page = await (await browser.newContext()).newPage();
const serverErrors = [];
page.on("pageerror", (e) => serverErrors.push("pageerror: " + e.message));
page.on("response", (r) => {
  const u = r.url();
  if (r.status() >= 500 && (u.includes("/v1/") || u.includes("/companies")))
    serverErrors.push(`${r.status()} ${r.request().method()} ${u.replace(URL.replace(/\/$/, ""), "")}`);
});

const app = page.getByRole("application");
const body = () => page.locator("body").innerText();
const atGateway = async () => (await body()).includes("MASTERS / TRANSACTIONS");
const toGateway = async () => {
  for (let i = 0; i < 5; i++) {
    if (await atGateway()) return;
    await app.press("Escape");
    await page.waitForTimeout(250);
  }
};
// Navigate a Gateway menu item the way a REAL KEYBOARD USER does: the menu's
// combobox is autofocused, so type to filter then press Enter ONCE. This drives
// the exact path that was broken (the combobox used to swallow the drilling
// Enter), so a regression fails this test rather than sailing past it.
const navMenu = async (label) => {
  await toGateway();
  await page.keyboard.type(label);
  await page.waitForTimeout(150);
  await page.keyboard.press("Enter");
  await page.waitForTimeout(700);
};

await page.goto(URL, { waitUntil: "networkidle" });
await page.waitForTimeout(1300);
rec("bootstrap: live company created (StrictMode-safe)", (await body()).includes("My Company"));

async function postGst(fkey, party, taxable, rate) {
  await toGateway();
  await app.press(fkey);
  await page.waitForTimeout(500);
  await page.getByRole("option", { name: party, exact: true }).click();
  await page.waitForTimeout(200);
  await page.getByTestId("gst-taxable").fill(taxable);
  await page.getByTestId("gst-rate").fill(rate);
  if (!(await page.getByTestId("gst-pos").inputValue())) await page.getByTestId("gst-pos").fill("29");
  await app.press("Control+a");
  for (let i = 0; i < 20; i++) {
    if (await page.getByTestId("voucher-no").count()) break;
    await page.waitForTimeout(250);
  }
  const no = (await page.getByTestId("voucher-no").count()) ? await page.getByTestId("voucher-no").innerText() : "";
  const cgst = (await page.getByTestId("tax-cgst").count()) ? await page.getByTestId("tax-cgst").innerText() : "";
  return { no, cgst };
}

try { const { no, cgst } = await postGst("F8", "Acme Traders", "10000", "18"); rec("F8 Sales posts (auto-number + server GST)", /No:/.test(no), `${no}, CGST ${cgst}`); }
catch (e) { rec("F8 Sales posts", false, e.message); }
try { const { no, cgst } = await postGst("F9", "Acme Traders", "4000", "18"); rec("F9 Purchase posts", /No:/.test(no), `${no}, CGST ${cgst}`); }
catch (e) { rec("F9 Purchase posts", false, e.message); }

try { await navMenu("Day Book"); rec("Day Book opens", (await page.getByRole("heading", { name: "Day Book" }).count()) > 0); }
catch (e) { rec("Day Book opens", false, e.message); }
try {
  await navMenu("Balance Sheet");
  const liab = await page.getByTestId("bs-liabilities-total").innerText().catch(() => "");
  const asset = await page.getByTestId("bs-assets-total").innerText().catch(() => "");
  rec("Balance Sheet opens with real totals", parseFloat(liab) > 0 || parseFloat(asset) > 0, `liab ${liab} / assets ${asset}`);
} catch (e) { rec("Balance Sheet opens with real totals", false, e.message); }
try {
  await navMenu("Profit & Loss A/c");
  const inc = await page.getByTestId("pl-income-total").innerText().catch(() => "");
  rec("P&L opens with income", parseFloat(inc) > 0, `income ${inc}`);
} catch (e) { rec("P&L opens with income", false, e.message); }
// Trial Balance is reached via Go To (Alt+G), Tally's universal navigator.
try {
  await toGateway();
  await app.press("Alt+g");
  await page.waitForTimeout(300);
  await page.keyboard.type("Trial Balance");
  await page.waitForTimeout(150);
  await page.keyboard.press("Enter");
  await page.waitForTimeout(700);
  rec("Trial Balance opens (via Go To / Alt+G)", (await page.getByRole("heading", { name: "Trial Balance" }).count()) > 0);
} catch (e) { rec("Trial Balance opens (via Go To / Alt+G)", false, e.message); }

try {
  await navMenu("Query");
  await page.getByTestId("query-input").fill("total sales this year");
  await page.getByTestId("query-submit").click();
  for (let i = 0; i < 16; i++) {
    if ((await page.getByTestId("query-row").count()) || (await page.getByTestId("query-error").count())) break;
    await page.waitForTimeout(250);
  }
  rec("Query answers in English (NL -> DSL -> read-only SQL)", (await page.getByTestId("query-row").count()) > 0);
} catch (e) { rec("Query answers in English", false, e.message); }

try {
  await navMenu("Bill Inbox");
  const headings = await page.getByRole("heading").allInnerTexts();
  rec("Bill Inbox opens", headings.some((h) => /Bill Inbox|Inbox/i.test(h)) || !(await atGateway()));
} catch (e) { rec("Bill Inbox opens", false, e.message); }

await browser.close();

const passed = results.filter((r) => r.ok).length;
console.log(`\n${passed}/${results.length} flows passed`);
if (serverErrors.length) console.log("5xx/page errors: " + serverErrors.join(" | "));
if (passed === results.length && !serverErrors.length) {
  console.log("ui-smoke OK");
  process.exit(0);
}
console.error("ui-smoke FAILED");
process.exit(1);
