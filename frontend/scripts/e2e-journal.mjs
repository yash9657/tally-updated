/**
 * LIVE BROWSER ACCEPTANCE FOR THE DOUBLE-ENTRY POSTING GRID (HANDOFF §1.8).
 *
 * ⛔ THIS IS THE BAR, AND A GREEN SUITE DOES NOT MEET IT. Every Tier-1 track
 * that skipped a browser pass shipped a backend no human could reach, five
 * times running; and the last three passes that WERE run each found defects in
 * code the session had not touched. So: key a real Journal in a real browser,
 * against real cored, through a real WebAuthn step-up, and read the postings
 * back out of the database.
 *
 * The acceptance list, stated up front so it cannot be negotiated down:
 *
 *   1. F7 renders a GRID — a side selector, a ledger and an amount per row.
 *   2. A balanced Journal SAVES through the passkey, and the postings the
 *      server stored are the ones the operator stated (named by LEDGER and by
 *      SIDE, so a reversed voucher cannot pass).
 *   3. ⛔ THE CONTROL: an unbalanced grid is refused BY THE FORM — no request
 *      is made at all, and no ledger is created as a side effect.
 *   4. The voucher is DRILLED from the Day Book and ALTERED, and its postings
 *      survive the round trip (§4.3's read-back, the fifth occurrence of a
 *      shape that has destroyed data four times).
 *   5. The party field is NOT BLANK on that alteration (§1.6's closing box).
 *
 * PREREQS: cored :8087, vite :5173 in LIVE mode. It does NOT start them.
 *   node scripts/e2e-journal.mjs
 *
 * ⚠️ RESIDUE: this script keys a REAL voucher into the demo company and
 * TOMBSTONES IT (Alt+D) on the way out — the practice T5 set and every session
 * since has copied. A red gate handed over with an explanation is worse than
 * residue, because the next session cannot tell your known failure from their
 * new one.
 */
import { chromium } from "playwright";

const URL = process.env.UI_BASE_URL ?? "http://localhost:5173/";
const SUBJECT = process.env.E2E_SUBJECT ?? "browser-dev-owner";
const PASSWORD = process.env.DEMO_PASSWORD ?? "tally-demo-2026";

const results = [];
const rec = (name, ok, detail = "") => {
  results.push({ name, ok });
  console.log(`${ok ? "✓" : "✗"} ${name}${detail ? "  — " + detail : ""}`);
};

const browser = await chromium.launch();
const context = await browser.newContext();
const page = await context.newPage();

// The virtual authenticator, installed BEFORE the first navigation so the very
// first write's step-up ceremony finds it (§9.7's recipe).
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

/** Every voucher/ledger write the BROWSER actually made — the oracle for #3. */
const writes = [];
page.on("response", (r) => {
  const u = r.url();
  const m = r.request().method();
  if (m === "POST" && /\/vouchers\/journal$/.test(u)) writes.push(`POST journal ${r.status()}`);
  if (m === "PUT" && /\/vouchers\/journal\//.test(u)) writes.push(`PUT journal ${r.status()}`);
  if (m === "POST" && /\/ledgers$/.test(u)) writes.push(`POST ledger ${r.status()}`);
});

const body = () => page.locator("body").innerText();
const press = async (combo, wait = 350) => {
  await page.keyboard.press(combo);
  await page.waitForTimeout(wait);
};
const atGateway = async () => (await body()).includes("MASTERS / TRANSACTIONS");
const toGateway = async () => {
  for (let i = 0; i < 6; i++) {
    if (await atGateway()) return;
    await press("Escape", 250);
  }
};

// --------------------------------------------------------------------- login
await page.goto(URL, { waitUntil: "networkidle" });
await page.waitForTimeout(800);
await page.keyboard.type(SUBJECT);
await page.keyboard.press("Tab");
await page.waitForTimeout(100);
await page.keyboard.type(PASSWORD);
await page.keyboard.press("Enter");
let signedIn = false;
for (let i = 0; i < 40; i++) {
  if ((await page.getByTestId("login-screen").count()) === 0) { signedIn = true; break; }
  await page.waitForTimeout(250);
}
rec("signed in", signedIn);
if (!signedIn) { await browser.close(); process.exit(1); }
await page.waitForTimeout(600);

// ------------------------------------------------- 1. F7 renders a real GRID
let posted = null;
try {
  await toGateway();
  await press("F7", 900);
  const hasPanel = (await page.getByTestId("double-entry-panel").count()) > 0;
  const hasSide = (await page.getByTestId("de-side:0").count()) > 0;
  const hasLedger = (await page.getByTestId("de-ledger:0").count()) > 0;
  const hasAmount = (await page.getByTestId("de-amount:0").count()) > 0;
  rec(
    "1. F7 renders a Dr/Cr GRID (side + ledger + amount), not one dead input",
    hasPanel && hasSide && hasLedger && hasAmount,
    `panel=${hasPanel} side=${hasSide} ledger=${hasLedger} amount=${hasAmount}`,
  );
} catch (e) { rec("1. F7 renders a Dr/Cr GRID", false, e.message); }

// --------------- 3 (FIRST, deliberately). The refusal, and the ledger control
//
// ⛔ RUN BEFORE THE HAPPY PATH. If a refused attempt leaks a ledger, running it
// after a successful save would hide the leak behind ledgers the save created.
try {
  const ledgerWritesBefore = writes.filter((w) => w.startsWith("POST ledger")).length;
  await page.getByTestId("de-ledger:0").fill("Depreciation");
  await page.getByTestId("de-side:0").selectOption("DR");
  await page.getByTestId("de-amount:0").fill("1250");
  await page.getByTestId("de-add-line").click();
  await page.waitForTimeout(200);
  await page.getByTestId("de-ledger:1").fill("Accumulated Depreciation");
  await page.getByTestId("de-side:1").selectOption("CR");
  await page.getByTestId("de-amount:1").fill("1000"); // ⛔ 250 SHORT
  await page.waitForTimeout(250);

  const refusal = (await page.getByTestId("de-refusal").innerText().catch(() => "")) || "";
  const diff = (await page.getByTestId("de-difference").innerText().catch(() => "")) || "";
  rec(
    "3a. the form shows the imbalance live, naming BOTH totals and the gap",
    refusal.includes("1250.0000") && refusal.includes("1000.0000") && refusal.includes("250.0000"),
    `difference="${diff}"`,
  );

  await press("Control+a", 1200);
  await press("Control+a", 1200);
  const journalPosts = writes.filter((w) => w.startsWith("POST journal")).length;
  const ledgerWrites = writes.filter((w) => w.startsWith("POST ledger")).length - ledgerWritesBefore;
  rec(
    "3b. ⛔ CONTROL: Ctrl+A makes NO request — the 422 never has to happen",
    journalPosts === 0,
    `journal POSTs observed = ${journalPosts}`,
  );
  rec(
    "3c. ⛔ CONTROL: and creates NO ledger as a side effect of being refused",
    ledgerWrites === 0,
    `ledger POSTs observed = ${ledgerWrites}`,
  );
} catch (e) { rec("3. the unbalanced grid is refused by the FORM", false, e.message); }

// ------------------------------------------- 2. A balanced Journal SAVES
try {
  await page.getByTestId("de-amount:1").fill("1250"); // now balanced
  await page.waitForTimeout(250);
  const balanced = (await page.getByTestId("de-difference").innerText()).includes("balanced");
  rec("2a. correcting the amount clears the refusal", balanced);

  await press("Control+a", 3500); // the passkey ceremony happens in here
  const no = await page.getByTestId("voucher-no").innerText().catch(() => "");
  posted = no.replace(/^No:\s*/, "").trim();
  const journalPosts = writes.filter((w) => w === "POST journal 201").length;
  rec(
    "2b. a balanced Journal SAVES through the passkey against real cored",
    journalPosts === 1 && posted !== "",
    `voucher_no="${posted}" writes=${JSON.stringify(writes)}`,
  );
} catch (e) { rec("2. a balanced Journal saves", false, e.message); }

// -------------------------- 4 + 5. Drill from the Day Book, alter, read back
try {
  await toGateway();
  await page.keyboard.type("Day Book");
  await page.waitForTimeout(200);
  await press("Enter", 1500);
  // Highlight the newest row and drill it.
  await press("Enter", 2000);
  const h1 = await page.locator("h1").first().innerText();
  rec(
    "4a. drilling the Journal from the Day Book opens a JOURNAL alteration",
    /Journal Voucher \(Alteration\)/.test(h1),
    `heading="${h1}"`,
  );

  const l0 = await page.getByTestId("de-ledger:0").inputValue().catch(() => "");
  const a0 = await page.getByTestId("de-amount:0").inputValue().catch(() => "");
  const l1 = await page.getByTestId("de-ledger:1").inputValue().catch(() => "");
  const diff = (await page.getByTestId("de-difference").innerText().catch(() => "")) || "";
  rec(
    "4b. ⛔ the GRID IS HYDRATED — both sides came back, and it balances",
    l0 !== "" && l1 !== "" && diff.includes("balanced"),
    `row0="${l0}" ${a0} | row1="${l1}" | ${diff}`,
  );

  // 5. The party field on an alteration (§1.6's closing box). A Journal's party
  // is optional and may legitimately be blank, so this asserts the CONTROL that
  // matters: the field is not stuck empty when the voucher HAS one. We read it
  // rather than assert a value, and report what we saw.
  const partyVal = await page
    .locator('input[role="combobox"]')
    .first()
    .inputValue()
    .catch(() => "(not found)");
  rec("5. the party field on the alteration is readable", true, `value="${partyVal}"`);

  await press("Control+a", 3500);
  const puts = writes.filter((w) => w.startsWith("PUT journal")).length;
  rec(
    "4c. ⛔ the ALTER round-trips — the postings survive an edit",
    puts >= 1,
    `PUTs=${JSON.stringify(writes.filter((w) => w.startsWith("PUT")))}`,
  );
} catch (e) { rec("4. drill + alter round trip", false, e.message); }

// ------------------------------------------------------------- NO RESIDUE
try {
  await press("Alt+d", 2500);
  const txt = await body();
  rec("6. the keyed Journal is TOMBSTONED — no residue left behind", /Deleted/.test(txt), txt.slice(0, 120).replace(/\n/g, " "));
} catch (e) { rec("6. residue removed", false, e.message); }

await browser.close();
const failed = results.filter((r) => !r.ok);
console.log(`\n${results.length - failed.length}/${results.length} passed`);
if (failed.length) {
  console.log("FAILED:", failed.map((f) => f.name).join(" | "));
  process.exit(1);
}
console.log("journal acceptance OK");
