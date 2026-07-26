/**
 * LIVE in-browser smoke test — drives the ACTUAL keystroke UI (Playwright,
 * headless Chromium) against the running stack in live mode, exercising the
 * end-to-end flows a real Tally user touches. It guards the wiring that is easy
 * to break silently: the company bootstrap committing under React StrictMode, the
 * dev proxy + auto-auth, server-derived GST, the reports/query reflecting posted
 * data, the Bill Inbox, and — the part that hid a real bug — that EVERY key and
 * every button on the bar works for a user who never clicks anything.
 *
 * TESTING RULE (learned the hard way, twice): send keys with `page.keyboard`,
 * NEVER with `locator.press()` on the app element. `locator.press()` focuses the
 * element first, which manufactures the very condition the app must not depend
 * on. Both bugs this file now guards — the combobox swallowing Enter, and every
 * shortcut dying after a screen change left focus on <body> — passed a suite
 * that pressed keys on the app element.
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
const page = await (await browser.newContext({ acceptDownloads: true })).newPage();
const serverErrors = [];
page.on("pageerror", (e) => serverErrors.push("pageerror: " + e.message));
page.on("response", (r) => {
  const u = r.url();
  if (r.status() >= 500 && (u.includes("/v1/") || u.includes("/companies")))
    serverErrors.push(`${r.status()} ${r.request().method()} ${u.replace(URL.replace(/\/$/, ""), "")}`);
});

const body = () => page.locator("body").innerText();
const status = () => page.getByTestId("status-line").innerText();
const heading = () => page.locator("h1").first().innerText();
const atGateway = async () => (await body()).includes("MASTERS / TRANSACTIONS");
/** Press a key WITHOUT focusing anything (see the testing rule above). */
const press = async (combo, wait = 300) => {
  await page.keyboard.press(combo);
  await page.waitForTimeout(wait);
};
const toGateway = async () => {
  for (let i = 0; i < 6; i++) {
    if (await atGateway()) return;
    await press("Escape", 250);
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
  await press("Enter", 800);
};

await page.goto(URL, { waitUntil: "networkidle" });
await page.waitForTimeout(1300);
rec("bootstrap: live company created (StrictMode-safe)", (await body()).includes("My Company"));

async function postGst(fkey, party, taxable, rate) {
  await toGateway();
  await press(fkey, 500);
  await page.getByRole("option", { name: party, exact: true }).click();
  await page.waitForTimeout(200);
  await page.getByTestId("gst-taxable").fill(taxable);
  await page.getByTestId("gst-rate").fill(rate);
  if (!(await page.getByTestId("gst-pos").inputValue())) await page.getByTestId("gst-pos").fill("29");
  await press("Control+a", 300);
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

// ---------------------------------------------------------------------------
// THE regression this file exists for: after drilling into a screen, focus is
// no longer on any control the app rendered. Every global key must still work.
// ---------------------------------------------------------------------------
try {
  await navMenu("Day Book");
  const opened = (await heading()) === "Day Book";
  const focusIsIdle = await page.evaluate(
    () => document.activeElement === document.body ||
      document.activeElement?.getAttribute("role") === "application" ||
      document.activeElement?.getAttribute("role") === "grid",
  );
  await press("Escape", 500);
  rec(
    "Esc works after drilling in (focus left the last control)",
    opened && focusIsIdle && (await atGateway()),
    `opened=${opened} idleFocus=${focusIsIdle}`,
  );
} catch (e) { rec("Esc works after drilling in", false, e.message); }

try {
  await navMenu("Day Book");
  await press("F9", 600); // a voucher F-key from inside a report
  const wentToVoucher = (await heading()).includes("Purchase Voucher");
  await press("Escape", 400);
  rec("F-keys work from inside a report (no focus needed)", wentToVoucher);
} catch (e) { rec("F-keys work from inside a report", false, e.message); }

// ---------------------------------------------------------------------------
// Reports: the whole button grammar (F2/F5/F12/Alt+F1/Ctrl+H/Ctrl+F/Alt+E).
// ---------------------------------------------------------------------------
try { await navMenu("Balance Sheet");
  const liab = await page.getByTestId("bs-liabilities-total").innerText().catch(() => "");
  const asset = await page.getByTestId("bs-assets-total").innerText().catch(() => "");
  rec("Balance Sheet opens with real totals", parseFloat(liab) > 0 || parseFloat(asset) > 0, `liab ${liab} / assets ${asset}`);
} catch (e) { rec("Balance Sheet opens with real totals", false, e.message); }

try { await navMenu("Profit & Loss A/c");
  const inc = await page.getByTestId("pl-income-total").innerText().catch(() => "");
  rec("P&L opens with income", parseFloat(inc) > 0, `income ${inc}`);
} catch (e) { rec("P&L opens with income", false, e.message); }

// Trial Balance via Tally's second-level report menu (Display More Reports).
try {
  await navMenu("Display More Reports");
  const inMenu = (await heading()) === "Display More Reports";
  await page.keyboard.type("Trial Balance");
  await page.waitForTimeout(150);
  await press("Enter", 900);
  rec("Display More Reports > Trial Balance opens", inMenu && (await heading()) === "Trial Balance");
} catch (e) { rec("Display More Reports > Trial Balance opens", false, e.message); }

try {
  // F5 flips the Trial Balance between ledger-wise and group-wise for real:
  // the first column header changes and the rows re-aggregate.
  const before = await page.getByRole("columnheader").first().innerText();
  await press("F5", 500);
  const after = await page.getByRole("columnheader").first().innerText();
  await press("F5", 400);
  rec("F5 toggles ledger-wise / group-wise", before === "Ledger" && after === "Group", `${before} -> ${after}`);
} catch (e) { rec("F5 toggles ledger-wise / group-wise", false, e.message); }

try {
  // F12 Configure is an OVERLAY: Esc must close the overlay and keep the report.
  await press("F12", 500);
  const overlayOpen = await page.getByRole("dialog", { name: /Configure/ }).count();
  await page.getByTestId("cfg-hide-zero").click();
  await page.waitForTimeout(300);
  const bannerShown = (await page.getByTestId("report-view-banner").count()) > 0;
  await press("Escape", 400);
  const stillOnReport = (await heading()) === "Trial Balance";
  rec("F12 Configure applies and Esc closes only the overlay", overlayOpen > 0 && bannerShown && stillOnReport);
} catch (e) { rec("F12 Configure applies and Esc closes only the overlay", false, e.message); }

try {
  // Ctrl+F filter really filters, and the banner states it.
  const rowsBefore = await page.locator("[role=row][data-rowkey]").count();
  await press("Control+f", 400);
  await page.getByTestId("filter-text").fill("zzz-no-such-ledger");
  await page.waitForTimeout(400);
  const rowsAfter = await page.locator("[role=row][data-rowkey]").count();
  await page.getByTestId("filter-text").fill("");
  await page.waitForTimeout(300);
  await press("Escape", 300);
  rec("Ctrl+F filters the rows", rowsBefore > 0 && rowsAfter < rowsBefore, `${rowsBefore} -> ${rowsAfter}`);
} catch (e) { rec("Ctrl+F filters the rows", false, e.message); }

try {
  // Alt+E writes a real CSV download (not a toast that lies).
  const [download] = await Promise.all([
    page.waitForEvent("download", { timeout: 5000 }),
    page.keyboard.press("Alt+e"),
  ]);
  rec("Alt+E exports the screen to CSV", /\.csv$/.test(download.suggestedFilename()), download.suggestedFilename());
} catch (e) { rec("Alt+E exports the screen to CSV", false, e.message); }

try {
  await press("F2", 500);
  const periodOpen = await page.getByRole("dialog", { name: "Period" }).count();
  await press("Escape", 300);
  rec("F2 opens the report period picker", periodOpen > 0);
} catch (e) { rec("F2 opens the report period picker", false, e.message); }

// ---------------------------------------------------------------------------
// The universal drill: statement -> group -> ledger -> voucher, and Esc back up.
// ---------------------------------------------------------------------------
try {
  await navMenu("Balance Sheet");
  await press("ArrowDown", 250);
  const trail = [];
  for (let i = 0; i < 4; i++) {
    await press("Enter", 900);
    trail.push(await heading());
    if (/Voucher/.test(await heading())) break;
  }
  const reachedVoucher = /Voucher \(Alteration\)/.test(trail[trail.length - 1] ?? "");
  // …and the drilled voucher is LOADED into the form. The StrictMode double
  // effect used to leave this EMPTY (number "auto", taxable 0), which would have
  // let Ctrl+A alter a real voucher into blank lines — so this assertion reads
  // the alteration form's own number field, not the post-save banner.
  await page.waitForTimeout(700);
  const loadedNo = await page.getByTestId("voucher-no-field").innerText().catch(() => "");
  const taxable = await page.getByTestId("gst-taxable").inputValue().catch(() => "");
  const hydrated = /\d/.test(loadedNo) && !/auto/.test(loadedNo) && parseFloat(taxable) > 0;
  let back = 0;
  for (let i = 0; i < 6 && !(await atGateway()); i++) { await press("Escape", 400); back++; }
  rec(
    "Drill statement > group > ledger > voucher, Esc walks back up",
    reachedVoucher && hydrated && (await atGateway()),
    `${trail.join(" > ")} | ${loadedNo || "(no number!)"} taxable ${taxable || "(empty!)"} | ${back} Escs back`,
  );
} catch (e) { rec("Drill statement > group > ledger > voucher", false, e.message); }

// ---------------------------------------------------------------------------
// F1 Help, F11 Features, Ctrl+N calculator, and the honesty of unbuilt keys.
// ---------------------------------------------------------------------------
try {
  await toGateway();
  await press("F1", 600);
  const help = (await heading()).includes("Help");
  const listsKeys = (await body()).includes("Ctrl+A");
  await press("Escape", 400);
  rec("F1 Help lists the live keymap", help && listsKeys && (await atGateway()));
} catch (e) { rec("F1 Help lists the live keymap", false, e.message); }

try {
  await press("F11", 600);
  const feats = (await heading()).includes("Company Features");
  const honest = (await body()).includes("Not built");
  await press("Escape", 400);
  rec("F11 Company Features states what is and is not built", feats && honest);
} catch (e) { rec("F11 Company Features", false, e.message); }

try {
  await press("Control+n", 500);
  await page.getByTestId("calc-input").fill("118000/1.18");
  await page.waitForTimeout(300);
  const result = await page.getByTestId("calc-result").innerText();
  await press("Escape", 300);
  rec("Ctrl+N calculator computes", result.startsWith("100000."), result);
} catch (e) { rec("Ctrl+N calculator computes", false, e.message); }

try {
  // A key whose FEATURE is not built must say so — never do nothing.
  // Alt+F7 (Stock Journal) stands in for the old Alt+F6 probe: credit notes are
  // built now, inventory vouchers are not.
  await toGateway();
  await page.keyboard.press("Alt+F7"); // Stock Journal
  await page.waitForTimeout(400);
  const msg = await status();
  rec("A key for an unbuilt feature explains itself", /Stock Journal/.test(msg) && /not built/i.test(msg), msg);
} catch (e) { rec("A key for an unbuilt feature explains itself", false, e.message); }

try {
  // Alt+F6 now opens a REAL Credit Note screen — the correct way to reverse
  // part of an invoice. If this regresses to an "unavailable" message, the F11
  // Features page and the keymap contradict each other.
  await toGateway();
  await page.keyboard.press("Alt+F6");
  await page.waitForTimeout(500);
  const h = await heading();
  await press("Escape", 300);
  rec("Alt+F6 opens the Credit Note voucher", /Credit Note/.test(h), h);
} catch (e) { rec("Alt+F6 opens the Credit Note voucher", false, e.message); }

try {
  // Mouse parity: clicking a button-bar item does exactly what its key does.
  await toGateway();
  await page.getByRole("button", { name: "F11 Features" }).click();
  await page.waitForTimeout(600);
  const viaClick = (await heading()).includes("Company Features");
  await press("Escape", 400);
  rec("Clicking a button-bar item fires the same action as its key", viaClick);
} catch (e) { rec("Clicking a button-bar item fires its key", false, e.message); }

// ---------------------------------------------------------------------------
// The AI-native screens.
// ---------------------------------------------------------------------------
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

// ---------------------------------------------------------------------------
// The SaaS surface in the browser: F3 company switching and Users & Roles.
// F3 was bound to "not built" until the admin API made a second membership
// reachable, so these two flows are the browser-visible half of that work.
// ---------------------------------------------------------------------------
try {
  await toGateway();
  await press("F3", 900);
  const h = await heading();
  const listed = (await body()).includes("List of Companies");
  // Esc must leave the picker WITHOUT switching (the session stays put).
  await press("Escape", 400);
  rec(
    "F3 opens the company picker (companies this user may open)",
    /Select Company/i.test(h) && listed && (await atGateway()),
    h,
  );
} catch (e) { rec("F3 opens the company picker", false, e.message); }

try {
  await navMenu("Users & Roles");
  const h = await heading();
  const text = await body();
  rec(
    "Users & Roles lists members, invites and security levels",
    /Users & Roles/i.test(h) &&
      /Members/.test(text) &&
      /Pending invites/.test(text) &&
      /Security levels/.test(text) &&
      /browser-dev-owner/.test(text),
    h,
  );
} catch (e) { rec("Users & Roles opens", false, e.message); }

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
