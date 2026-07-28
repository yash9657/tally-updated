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
 *
 * ===========================================================================
 * SIGN IN FIRST (the login gate)
 * ===========================================================================
 * The app is now behind a real password login: in live mode nothing below the
 * gate renders until somebody signs in. Every Playwright run gets a FRESH
 * browser context, so sessionStorage is always empty and the login screen
 * appears deterministically — there is no "already signed in" branch to handle.
 *
 * The identity must already have a password. `pnpm seed:demo` establishes it
 * (POST /auth/password), so the running order is seed -> e2e. Override with
 * E2E_SUBJECT / DEMO_PASSWORD.
 *
 * ===========================================================================
 * THEN STEP UP — WITH A REAL, VIRTUAL AUTHENTICATOR
 * ===========================================================================
 * A token minted by POST /auth/login carries NO `amr` claim, and every
 * company's Owner level is seeded requires_webauthn = true (this file
 * bootstraps its own fresh "My Company", whose creator is the Owner). So the
 * FIRST write of the session is answered
 *   403 POST /v1/companies/{id}/ledgers :: {"code":"STEPUP_REQUIRED", ...}
 * — the F8 screen creates its party/tax ledgers on demand, so the voucher POST
 * is not even reached.
 *
 * The app now handles that itself: App's transport runs the WebAuthn ceremony
 * (auth/webauthn.ts), swaps in the returned amr token and replays the request.
 * For that to work headlessly this file installs a CDP VIRTUAL AUTHENTICATOR on
 * the page's context BEFORE the first navigation, with presence simulated and
 * the user pre-verified, so navigator.credentials.create/get resolve without a
 * human. That makes the ceremony REAL end to end — real challenge, real COSE
 * key, real ES256 signature verified by cored's own verifier — and it is
 * asserted from OBSERVED NETWORK below ("assert/finish 200"), not inferred from
 * a screen eventually working.
 *
 * (The previous E2E_SIMULATE_STEPUP=1 crutch, which swapped in a dev-minted amr
 * token, is GONE. It only ever existed to prove the amr gap was the sole
 * breakage; the ceremony is now performed for real.)
 */
import { chromium } from "playwright";

const URL = process.env.UI_BASE_URL ?? "http://localhost:5173/";
/** The identity the browser signs in as. Its password comes from `seed:demo`. */
const SUBJECT = process.env.E2E_SUBJECT ?? "browser-dev-owner";
const PASSWORD = process.env.DEMO_PASSWORD ?? "tally-demo-2026";
const results = [];
const rec = (name, ok, detail = "") => {
  results.push({ name, ok });
  console.log(`${ok ? "✓" : "✗"} ${name}${detail ? "  — " + detail : ""}`);
};

const browser = await chromium.launch();
const context = await browser.newContext({ acceptDownloads: true });
const page = await context.newPage();

// ---------------------------------------------------------------------------
// VIRTUAL AUTHENTICATOR. Installed on the SAME context/page every flow uses and
// BEFORE the first navigation, so the very first write's step-up ceremony finds
// an authenticator. ctap2 + internal transport + resident keys is what Chromium
// treats as a platform passkey; automaticPresenceSimulation + isUserVerified
// stand in for the human touching the sensor.
// ---------------------------------------------------------------------------
const cdp = await context.newCDPSession(page);
await cdp.send("WebAuthn.enable");
await cdp.send("WebAuthn.addVirtualAuthenticator", {
  options: {
    protocol: "ctap2",
    transport: "internal",
    hasResidentKey: true,
    hasUserVerification: true,
    isUserVerified: true,
    automaticPresenceSimulation: true,
  },
});

const serverErrors = [];
/** Every ceremony response the BROWSER actually made, by route and status. */
const ceremony = { registerFinish: [], assertBegin: [], assertFinish: [] };
page.on("pageerror", (e) => serverErrors.push("pageerror: " + e.message));
page.on("response", (r) => {
  const u = r.url();
  if (r.status() >= 500 && (u.includes("/v1/") || u.includes("/companies")))
    serverErrors.push(`${r.status()} ${r.request().method()} ${u.replace(URL.replace(/\/$/, ""), "")}`);
  if (u.includes("/auth/webauthn/assert/begin")) ceremony.assertBegin.push(r.status());
  if (u.includes("/auth/webauthn/register/finish")) ceremony.registerFinish.push(r.status());
  if (u.includes("/auth/webauthn/assert/finish")) ceremony.assertFinish.push(r.status());
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

// ---------------------------------------------------------------------------
// FLOW 0 — SIGN IN. Everything else in this file depends on it, so it runs
// first and inline (not in a try/catch helper): if the gate is broken there is
// nothing to smoke-test.
//
// Keyboard only, per HANDOFF §6. The login screen autofocuses the user-name
// field, Tab reaches the password field and Enter submits — so `page.keyboard`
// alone can drive it, and a regression in any of those three affordances fails
// here instead of silently being papered over by locator.fill().
// ---------------------------------------------------------------------------
const onLoginScreen = async () => (await page.getByTestId("login-screen").count()) > 0;
const focusedField = () =>
  page.evaluate(() => document.activeElement?.getAttribute("name") ?? "(none)");
const loginError = async () => {
  const el = page.getByTestId("login-error");
  if (!(await el.count())) return null;
  return { kind: await el.getAttribute("data-failure"), text: await el.innerText() };
};
/** Wait until the login screen is gone (a successful sign-in swaps in the shell). */
const awaitShell = async () => {
  for (let i = 0; i < 40; i++) {
    if (!(await onLoginScreen())) return true;
    await page.waitForTimeout(250);
  }
  return false;
};

await page.goto(URL, { waitUntil: "networkidle" });
await page.waitForTimeout(800);

try {
  const focused = await focusedField();
  const text = await body();
  rec(
    "login: the gate renders FIRST — no shell, no company, focus in the user name",
    (await onLoginScreen()) &&
      !(await atGateway()) &&
      !text.includes("My Company") &&
      focused === "subject",
    `autofocus=${focused}`,
  );
} catch (e) { rec("login: the gate renders FIRST", false, e.message); }

try {
  // A WRONG password must be REJECTED, VISIBLY, and must not open the shell.
  await page.keyboard.type(SUBJECT);
  await page.keyboard.press("Tab");
  await page.waitForTimeout(100);
  await page.keyboard.type("definitely-not-the-password");
  await page.keyboard.press("Enter");
  await page.waitForTimeout(1200);
  const err = await loginError();
  const text = await body();
  rec(
    "login: a WRONG password shows a visible error and the Gateway does NOT render",
    !!err &&
      err.kind === "invalid" &&
      (await onLoginScreen()) &&
      !(await atGateway()) &&
      !text.includes("My Company"),
    err ? `data-failure=${err.kind} "${err.text}"` : "(no error rendered)",
  );
} catch (e) { rec("login: a WRONG password is rejected", false, e.message); }

try {
  // Retry the way a real operator does: the password field still holds focus
  // after the failed submit, so backspace it clear and type the right one. No
  // reload, no locator.fill — that would test a path no user takes.
  const focused = await focusedField();
  for (let i = 0; i < 48; i++) await page.keyboard.press("Backspace");
  await page.keyboard.type(PASSWORD);
  await page.keyboard.press("Enter");
  const entered = await awaitShell();
  await page.waitForTimeout(1500);
  const text = await body();
  rec(
    "login: the CORRECT password enters the shell, and the chrome says who I am",
    entered && !(await onLoginScreen()) && text.includes(SUBJECT) && text.includes("Sign Out"),
    `retry focus=${focused}, signed in as ${SUBJECT}`,
  );
} catch (e) { rec("login: the CORRECT password enters the shell", false, e.message); }

rec("bootstrap: live company created (StrictMode-safe)", (await body()).includes("My Company"));

/**
 * Post a GST voucher the way a real operator does.
 *
 * The party field is an as-you-type selection list over the company's REAL
 * chart of accounts (it used to be a hardcoded sample list). This file
 * bootstraps a FRESH company, which by definition has no customers yet — so the
 * flow is: type the name, then pick it. On the first voucher that resolves to
 * the "Create …" row and the party is created on accept; on later ones it
 * resolves to the account that now exists. Both paths land on the same click,
 * which is exactly the point: the operator does not have to know which case
 * they are in.
 */
async function postGst(fkey, party, taxable, rate) {
  await toGateway();
  await press(fkey, 500);
  // Type into the party combobox. `fill` is right here and does NOT breach the
  // file's rule: that rule forbids locator.press for KEY EVENTS the app must
  // handle globally (which is how two shortcut bugs hid), not typing data into
  // a text field — the taxable/rate fields below are filled the same way. The
  // combobox is not autofocused on entry, because the field cursor starts on
  // the voucher date.
  await page.getByRole("combobox").fill(party);
  await page.waitForTimeout(250);
  await page
    .getByRole("option")
    .filter({ hasText: party })
    .first()
    .click();
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
// The writes above could ONLY have succeeded through a real WebAuthn step-up:
// the login token has no `amr` and the Owner level requires one. Assert that
// from OBSERVED NETWORK rather than from "F8 eventually worked" — if a future
// change ever made writes pass some other way (a faked amr, a relaxed default),
// F8 would stay green and this flow would go red, which is the point.
// ---------------------------------------------------------------------------
try {
  const ok = ceremony.assertFinish.filter((s) => s === 200).length;
  rec(
    "WebAuthn step-up really ran in the browser (assert/finish 200)",
    ok > 0 && ceremony.assertBegin.length > 0,
    `assert/begin ${ceremony.assertBegin.join(",") || "-"} · register/finish ` +
      `${ceremony.registerFinish.join(",") || "-"} · assert/finish ` +
      `${ceremony.assertFinish.join(",") || "-"}`,
  );
} catch (e) { rec("WebAuthn step-up really ran in the browser", false, e.message); }

try {
  // …and it ran ONCE, not once per write: the stepped-up token is kept for the
  // rest of the session. Two vouchers were posted above; a per-write ceremony
  // would show several assert/finish calls (and, against cored's single-use
  // challenge store, would be racy).
  rec(
    "the stepped-up token is REUSED (one ceremony, not one per write)",
    ceremony.assertFinish.filter((s) => s === 200).length === 1,
    `${ceremony.assertFinish.length} assert/finish call(s)`,
  );
} catch (e) { rec("the stepped-up token is REUSED", false, e.message); }

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
  //
  // THIS PROBE HAS MOVED TWICE, and each move is the product getting built:
  // Alt+F6 (Credit Note) -> Alt+F7 (Stock Journal) -> Ctrl+F8 (Sales Order).
  // Alt+F7 opens a real Stock Journal screen as of the inventory work, so it
  // stopped being an example of an unbuilt key and this flow correctly failed.
  // Sales Order is deliberately NOT built: an order is a COMMITMENT, not a
  // movement, and one that moved stock would let a quotation deplete the
  // warehouse — so it needs a committed-quantity concept the movement ledger
  // does not have. When orders ARE built, move this probe again rather than
  // deleting it; the invariant it guards (a bound key never silently does
  // nothing) outlives any particular example.
  await toGateway();
  await page.keyboard.press("Control+F8"); // Sales Order
  await page.waitForTimeout(400);
  const msg = await status();
  rec("A key for an unbuilt feature explains itself", /Sales Order/.test(msg) && /not built/i.test(msg), msg);
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
  // UPLOAD A BILL — the OCR loop's front door, in a REAL browser with a REAL
  // File. The unit test has to fill in jsdom's missing File.arrayBuffer(); this
  // exercises the browser's own, which is the API the component actually uses.
  // The fake recogniser runs inside the request, so the draft comes back
  // terminal and the poll loop exits on its first read — the same code path a
  // 45-second real-OCR read takes, just faster.
  await navMenu("Bill Inbox");
  await page.getByTestId("inbox-upload").setInputFiles({
    name: "supplier-bill.pdf",
    mimeType: "application/pdf",
    buffer: Buffer.from("%PDF-1.4 a supplier bill"),
  });
  await page.waitForTimeout(2500);
  const notice = await page.getByTestId("inbox-notice").innerText().catch(() => "");
  const err = await page.getByTestId("inbox-error").innerText().catch(() => "");
  await toGateway();
  rec(
    "Bill Inbox accepts a real FILE UPLOAD and reports the outcome",
    /supplier-bill\.pdf/.test(notice) && err === "",
    err ? `error: ${err}` : notice || "(no notice)",
  );
} catch (e) { rec("Bill Inbox file upload", false, e.message); }

try {
  await navMenu("Bill Inbox");
  const headings = await page.getByRole("heading").allInnerTexts();
  rec("Bill Inbox opens", headings.some((h) => /Bill Inbox|Inbox/i.test(h)) || !(await atGateway()));
} catch (e) { rec("Bill Inbox opens", false, e.message); }

// ---------------------------------------------------------------------------
// SINGLE-ENTRY MODE (F5 Payment) and Ctrl+H Change Mode.
//
// Tally defaults Payment / Receipt / Contra to single entry — one account on one
// side, many on the other — and Ctrl+H switches. `voucherConfig` had declared
// `entry: "single"` since Phase 1 with nothing reading it, and Ctrl+H opened a
// picker whose options did nothing at all.
//
// What is asserted is the POSTED result, not the layout: a single-entry payment
// must produce a BALANCED voucher, which the Trial Balance is the independent
// witness for.
// ---------------------------------------------------------------------------
try {
  await toGateway();
  await press("F5", 600);
  const hasPanel = (await page.getByTestId("single-entry-panel").count()) > 0;

  await page.getByRole("combobox", { name: "Account" }).fill("HDFC Bank");
  await page.waitForTimeout(250);
  await page.getByRole("option").filter({ hasText: "HDFC Bank" }).first().click();
  await page.getByTestId("single-ledger:0").fill("Office Rent");
  await page.getByTestId("single-amount:0").fill("2500");
  // The single side's amount is DERIVED — shown before saving, never typed.
  const totalShown = await page.getByTestId("single-total").innerText();
  await press("Control+a", 600);
  for (let i = 0; i < 20; i++) {
    if (await page.getByTestId("voucher-no").count()) break;
    await page.waitForTimeout(250);
  }
  const no = (await page.getByTestId("voucher-no").count())
    ? await page.getByTestId("voucher-no").innerText()
    : "";
  await toGateway();
  rec(
    "F5 opens in SINGLE ENTRY and posts a balanced voucher from one account + particulars",
    hasPanel && /No:/.test(no) && /2500\.0000/.test(totalShown),
    `${hasPanel ? "single-entry panel" : "NO PANEL"} | ${totalShown} | ${no}`,
  );
} catch (e) { rec("F5 single-entry posts", false, e.message); }

try {
  // Ctrl+H offers THIS class's modes and applies the choice in one keystroke.
  await toGateway();
  await press("F5", 600);
  await press("Control+h", 500);
  const overlay = await body();
  const offersSingle = /Single Entry/.test(overlay) && /Double Entry/.test(overlay);
  const offersInvoice = /Item Invoice/.test(overlay);
  await page.getByRole("option").filter({ hasText: "Double Entry" }).first().click();
  await page.waitForTimeout(400);
  const switched = (await page.getByTestId("single-entry-panel").count()) === 0;
  await toGateway();
  rec(
    "Ctrl+H offers this class's modes and switching to Double Entry takes effect",
    offersSingle && !offersInvoice && switched,
    `single/double offered=${offersSingle} invoice-modes-absent=${!offersInvoice} switched=${switched}`,
  );
} catch (e) { rec("Ctrl+H change mode", false, e.message); }

// ---------------------------------------------------------------------------
// THE EDIT LOG (Alt+L on a drilled voucher). The MCA audit trail made visible:
// the hash-chained voucher_version log has existed since migration 0002 and
// nothing could read it. This flow drills a real voucher, opens its log, and
// asserts BOTH halves of the claim — the versions are there, AND the server
// verified the chain. A viewer that listed versions without the verification
// would be a claim rather than evidence, so the banner is what is asserted.
// ---------------------------------------------------------------------------
try {
  await toGateway();
  await navMenu("Day Book");
  await press("ArrowDown", 250);
  await press("Enter", 900); // drill into the voucher (alteration mode)
  const onVoucher = /Voucher \(Alteration\)/.test(await heading());
  await press("Alt+L", 1200);
  const h = await heading();
  const text = await body();
  // The chain must be VERIFIED — not merely present. "Not verified" is what a
  // client that never got a verification shows, and it must not pass here.
  const verified = /Chain verified/.test(text);
  const hasVersion = /v1 · Created/.test(text);
  await press("Escape", 400);
  const backOnVoucher = /Voucher \(Alteration\)/.test(await heading());
  await toGateway();
  rec(
    "Alt+L opens the Edit Log, and the server VERIFIES the hash chain",
    onVoucher && /Edit Log/i.test(h) && verified && hasVersion && backOnVoucher,
    verified
      ? `${h} — chain verified, v1 present, Esc returns to the voucher`
      : `${h} — verification banner said: ${(text.match(/(Chain verified|TAMPERED[^\n]*|Not verified)/) ?? ["(none)"])[0]}`,
  );
} catch (e) { rec("Alt+L opens the Edit Log", false, e.message); }

// ---------------------------------------------------------------------------
// The SaaS surface in the browser: F3 company switching and Users & Roles.
// F3 was bound to "not built" until the admin API made a second membership
// reachable, so these two flows are the browser-visible half of that work.
// ---------------------------------------------------------------------------
try {
  // WAIT FOR THE LIST, DO NOT SLEEP FOR IT. This flow used a flat 900ms and went
  // flaky once the shared dev account had accumulated 120 companies (one per
  // seed:demo run): the picker renders every company the user may open, so the
  // fixed budget that was ample at 3 rows is not at 120, and the failure looked
  // like a regression in F3 rather than what it was. The same applies to the Esc
  // return — waiting on the condition is both faster in the common case and
  // correct in the slow one.
  await toGateway();
  await page.keyboard.press("F3");
  const listed = await page
    .getByText(/List of Companies/i)
    .first()
    .waitFor({ state: "visible", timeout: 15000 })
    .then(() => true, () => false);
  const h = await heading();
  // Esc must leave the picker WITHOUT switching (the session stays put).
  await page.keyboard.press("Escape");
  let back = false;
  for (let i = 0; i < 30 && !back; i++) {
    back = await atGateway();
    if (!back) await page.waitForTimeout(200);
  }
  rec(
    "F3 opens the company picker (companies this user may open)",
    /Select Company/i.test(h) && listed && back,
    `${h} | list rendered=${listed} | Esc returned to gateway=${back}`,
  );
} catch (e) { rec("F3 opens the company picker", false, e.message); }

try {
  await navMenu("Users & Roles");
  const h = await heading();
  const text = await body();
  // The member listed is the subject that SIGNED IN (it created this company on
  // bootstrap, so it is its Owner). Asserting the live subject rather than a
  // hardcoded one is what makes this a per-user check now that the browser has
  // a real login.
  rec(
    "Users & Roles lists members, invites and security levels",
    /Users & Roles/i.test(h) &&
      /Members/.test(text) &&
      /Pending invites/.test(text) &&
      /Security levels/.test(text) &&
      text.includes(SUBJECT),
    h,
  );
} catch (e) { rec("Users & Roles opens", false, e.message); }

// ---------------------------------------------------------------------------
// The admin WRITES, in the browser. Until now the screen could only INVITE;
// changing a level, revoking access, revoking an invite and creating/deleting a
// custom security level were API-only. Each of these is a WRITE, so each one
// also runs the real WebAuthn step-up ceremony through the virtual
// authenticator installed above.
//
// NOTE this company (bootstrapped by this file) has exactly ONE member — the
// signed-in Owner — so it is the LAST-OWNER case by construction. That is
// asserted here rather than worked around; a successful level CHANGE needs a
// second member and is gated live by `pnpm verify:tenancy` §8b instead, with
// the PDP as its oracle.
// ---------------------------------------------------------------------------
try {
  await navMenu("Users & Roles");
  await page.getByTestId("members-table").waitFor({ timeout: 5000 });
  // The sole Owner: the screen must SAY they cannot be demoted or removed,
  // rather than offering a button the server would answer 409.
  const note = await page
    .getByTestId(`member-lastowner:${SUBJECT}`)
    .innerText()
    .catch(() => "");
  const selectDisabled = await page
    .getByTestId(`member-level:${SUBJECT}`)
    .isDisabled()
    .catch(() => false);
  const noRevoke = (await page.getByTestId(`member-remove:${SUBJECT}`).count()) === 0;
  rec(
    "the LAST Owner cannot be demoted or removed, and the screen says why",
    /last Owner/i.test(note) && selectDisabled && noRevoke,
    note || "(no note rendered)",
  );
} catch (e) { rec("the LAST Owner is protected in the browser", false, e.message); }

const LEVEL = `E2E Auditor ${process.pid}`;
try {
  // CREATE a custom security level, composing a rule from the dropdowns — which
  // are built from the SERVER's closed vocabulary (GET .../security-levels/
  // vocabulary), so a rule the PDP could never match is unrepresentable here.
  await page.getByTestId("level-new").click();
  await page.getByTestId("level-editor").waitFor({ timeout: 5000 });
  await page.getByTestId("level-name").fill(LEVEL);
  // A real vocabulary token; picking it PROVES the options came from the server.
  await page.getByTestId("rule-resource:0").selectOption("report:trial-balance");
  await page.getByTestId("level-save").click();

  await page.waitForTimeout(1500);
  const listed = await page.getByTestId("levels-list").innerText();
  rec(
    "create a custom security level from the SERVER's rule vocabulary",
    listed.includes(LEVEL) && listed.includes("report:trial-balance"),
    listed.includes(LEVEL) ? "listed with its rule" : listed.slice(0, 160),
  );
} catch (e) { rec("create a custom security level", false, e.message); }

try {
  // INVITE at that level, then REVOKE the invite. The two are one flow: an
  // invite is not a membership, so the screen lists it separately — and a level
  // with a pending invite against it cannot be deleted (409), which is exactly
  // why the revoke has to happen before the delete below.
  const invitee = `e2e-invitee-${process.pid}`;
  await page.getByTestId("invite-subject").fill(invitee);
  await page.getByTestId("invite-level").selectOption(LEVEL);
  await page.getByTestId("invite-submit").click();
  await page.waitForTimeout(1200);
  const pending = await page.getByTestId("invites-list").innerText().catch(() => "");

  // Revoke it — INLINE two-step, never window.confirm (a native modal would
  // block this very CDP channel).
  await page.getByTestId(`invite-revoke:${invitee}`).click();
  await page.getByTestId(`invite-revoke-confirm:${invitee}`).click();
  await page.waitForTimeout(1200);
  const gone = (await page.getByTestId(`invite-revoke:${invitee}`).count()) === 0;
  const notice = await page.getByTestId("members-notice").innerText().catch(() => "");
  rec(
    "invite someone at that level, then REVOKE the pending invite",
    pending.includes(invitee) && gone && /never be claimed/i.test(notice),
    gone ? notice.slice(0, 90) : "invite still listed after revoke",
  );
} catch (e) { rec("invite then revoke a pending invite", false, e.message); }

try {
  // DELETE the now-unused level (same inline two-step). Its pending invite was
  // revoked above, so the in-use 409 guard does not fire.
  await page.getByTestId(`level-delete:${LEVEL}`).click();
  await page.getByTestId(`level-delete-confirm:${LEVEL}`).click();
  await page.waitForTimeout(1200);
  const listed = await page.getByTestId("levels-list").innerText();
  rec(
    "delete the now-unused custom security level",
    !listed.includes(LEVEL) && listed.includes("Owner"),
    listed.includes(LEVEL) ? "still listed" : "gone; the seeded levels remain",
  );
} catch (e) { rec("delete a custom security level", false, e.message); }

try {
  // The built-in Owner level offers NO delete, and its editor locks every field
  // but the back-dated cutoff — the browser half of the server's 422.
  const noDelete = (await page.getByTestId("level-delete:Owner").count()) === 0;
  await page.getByTestId("level-edit:Owner").click();
  await page.getByTestId("level-owner-locked").waitFor({ timeout: 5000 });
  const nameLocked = await page.getByTestId("level-name").isDisabled();
  const cutoffEditable = !(await page.getByTestId("level-cutoff").isDisabled());
  await page.getByTestId("level-cancel").click();
  rec(
    "the built-in Owner level is immutable except its back-dated cutoff",
    noDelete && nameLocked && cutoffEditable,
    `noDelete=${noDelete} nameLocked=${nameLocked} cutoffEditable=${cutoffEditable}`,
  );
} catch (e) { rec("the Owner level is immutable in the browser", false, e.message); }

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
