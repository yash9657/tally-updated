/**
 * L12 — D38 IS GLOBAL. Tab was bound in the keymap, preventDefaulted by
 * useNavMachine, and then discarded on every screen that is not a voucher. So
 * this drives FIVE screens, not the cost picker it was reported on.
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, altMac, toGateway } = app;

/** What has focus, as something a human can read in a failure line. */
const focused = () =>
  page.evaluate(() => {
    const e = document.activeElement;
    if (!e || e === document.body) return "<body>";
    return `${e.tagName.toLowerCase()}${e.getAttribute("data-testid") ? `[${e.getAttribute("data-testid")}]` : ""}${
      e.getAttribute("aria-label") ? `{${e.getAttribute("aria-label")}}` : ""
    }`;
  });
const status = () => page.getByTestId("status-line").innerText().catch(() => "");

const tabMoves = async (where) => {
  const before = await focused();
  await press("Tab", 260);
  const after = await focused();
  rec(`D38: Tab MOVES focus on ${where}`, before !== after, `${before} -> ${after}`);
  const s = await status();
  rec(`D38: ...and the status line does not deny it on ${where}`, !/does not apply/i.test(s), s.replace(/\n/g, " ").slice(0, 90));
  const back = await focused();
  await page.keyboard.down("Shift"); await page.keyboard.press("Tab"); await page.keyboard.up("Shift");
  await page.waitForTimeout(260);
  rec(`D38: Shift+Tab MOVES focus back on ${where}`, (await focused()) !== back, `${back} -> ${await focused()}`);
};

const goTo = async (name, wait = 1800) => {
  await toGateway(); await altMac("g"); await page.waitForTimeout(300);
  await page.keyboard.type(name); await page.waitForTimeout(400); await press("Enter", wait);
};

// ---- 1. the Gateway itself
await toGateway();
await tabMoves("the Gateway");

// ---- 2. a report
await goTo("Trial Balance");
rec("Trial Balance opened", /Trial Balance/.test(await body()));
await tabMoves("a report (Trial Balance)");

// ---- 3. Users & Roles (Gateway-only, not in Go To)
await app.openFromGateway("Users & Roles");
rec("Users & Roles opened", /Users|Roles|Members/i.test(await body()));
await tabMoves("Users & Roles");

// ---- 4. the Bill Inbox
await app.openFromGateway("Bill Inbox");
rec("Bill Inbox opened", /Bill Inbox/i.test(await body()));
await tabMoves("the Bill Inbox");

// ---- 5. THE SCREEN IT WAS REPORTED ON: the particulars grid's cost picker.
await toGateway();
await press("F5", 1200); // Payment
rec("F5 opens a Payment", /Payment/.test(await body()));

// Walk the grid with Tab ONLY and see whether the cost-split controls are
// reachable at all. `alloc:0-add` is the button the report named as mouse-only.
let reached = null;
for (let i = 0; i < 40; i++) {
  const f = await focused();
  if (/alloc:0-add/.test(f)) { reached = i + 1; break; }
  await press("Tab", 90);
}
rec(
  "D38: the cost-split ADD button is reachable by Tab alone",
  reached !== null,
  reached ? `focused after ${reached} Tab presses` : `never focused in 40 presses (last: ${await focused()})`,
);

if (reached !== null) {
  await press("Enter", 500);
  const axis = await page.getByTestId("alloc:0-axis:0").count();
  rec("D38: Enter on it ADDS a split (the axis select appears)", axis === 1, `axis selects=${axis}`);
  // \u26d4 AN AXIS MUST BE CHOSEN FIRST, AND THAT IS THE SCREEN BEING RIGHT.
  // The centre select carries `disabled={a.costCategoryId === ""}` and the
  // "rest" button is not RENDERED until an axis is picked — so neither is
  // Tab-reachable before then, correctly. The first run of this probe walked
  // straight past both and logged a defect; the screen was right and the probe
  // was wrong. Choose the axis from the KEYBOARD, then walk.
  for (let i = 0; i < 25; i++) {
    if (/alloc:0-axis:0/.test(await focused())) break;
    await press("Tab", 90);
  }
  // \u26a0 TYPE THE OPTION'S NAME, DO NOT ARROW. A native <select> ignores
  // ArrowDown in HEADLESS Chromium (no popup exists to walk), so the first run
  // read value="" and logged three defects that were all this. Type-to-select
  // is also what an operator does. VERIFIED against the served list: the axes on
  // the demo tenant are "Branch" and "Department".
  await page.keyboard.type("Department");
  await page.waitForTimeout(400);
  const axisValue = await page.getByTestId("alloc:0-axis:0").inputValue();
  rec("D38: an axis can be CHOSEN from the keyboard", axisValue !== "", `value="${axisValue}"`);
  rec(
    "D38: choosing it ENABLES the centre and RENDERS the 'rest' button",
    (await page.getByTestId("alloc:0-centre:0").isDisabled()) === false &&
      (await page.getByTestId("alloc:0-rest:0").count()) === 1,
  );

  let hit = { centre: false, amount: false, rest: false };
  for (let i = 0; i < 25; i++) {
    const f = await focused();
    if (/alloc:0-centre:0/.test(f)) hit.centre = true;
    if (/alloc:0-amount:0/.test(f)) hit.amount = true;
    if (/alloc:0-rest:0/.test(f)) hit.rest = true;
    if (hit.centre && hit.amount && hit.rest) break;
    await press("Tab", 90);
  }
  rec("D38: centre, amount AND the 'rest' button are all Tab-reachable", hit.centre && hit.amount && hit.rest, JSON.stringify(hit));
}

console.log("\nNON-GET WIRE:", app.net.join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
