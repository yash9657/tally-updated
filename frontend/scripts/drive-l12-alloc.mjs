/** L12 — D67 (a centre-less split reported as complete) and the write control. */
import { openApp, rec, summary } from "./audit-harness.mjs";
const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, toGateway } = app;

const focused = () => page.evaluate(() => document.activeElement?.getAttribute("data-testid") ?? "<none>");
const tabTo = async (id) => {
  for (let i = 0; i < 45; i++) { if (String(await focused()) === id) return true; await press("Tab", 80); }
  return false;
};

await toGateway();
await press("F5", 1200);                                  // Payment
rec("F5 opens a Payment", /Payment/.test(await body()));

// Key the accounting side: an account, a party and an amount, so the particulars
// line has a posting for an axis to be measured against.
/**
 * \u26d4 A LEDGER FIELD IS A COMBOBOX AND `.fill()` DOES NOT CHOOSE FROM IT.
 * Filling sets the visible text and binds NO ledger id, so Ctrl+A answers
 * "Account is required" over a box that plainly reads "HDFC Bank" — the first
 * run of this probe spent a diagnosis on that.
 *
 * \u26a0 AND DO NOT PRESS ArrowDown "to be safe": the list puts existing matches
 * FIRST and `Create "<what you typed>"` LAST, so one ArrowDown past a single
 * match lands on CREATE. That is how the first run minted a junk ledger called
 * "HDFC" beside the real "HDFC Bank" and posted a live payment to it. Type
 * enough to be unambiguous, then Enter.
 */
const pickLedger = async (locator, name) => {
  await locator.click();
  await page.keyboard.type(name);
  await page.waitForTimeout(500);
  const first = await page.locator('[role="option"]').first().innerText().catch(() => "");
  await press("Enter", 400);
  return first.trim();
};
const acctPick = await pickLedger(page.getByLabel("Account").first(), "HDFC Bank");
rec("the Account field binds an EXISTING ledger", acctPick === "HDFC Bank", `top option was "${acctPick}"`);
// \u26a0 THE TESTIDS, READ OFF THE LIVE DOM — the single-entry grid is
// `single-ledger:0` / `single-amount:0`, not the aria labels the first run
// guessed. With a 0.0000 posting the axis is over-allocated by construction and
// the LAST assertion below fails for a reason that has nothing to do with D67.
const legPick = await pickLedger(page.getByTestId("single-ledger:0"), "Office Rent");
await page.getByTestId("single-amount:0").fill("1000");
await page.waitForTimeout(250);
rec(
  "the particulars line carries a ledger and 1000.0000",
  (await page.getByTestId("single-amount:0").inputValue()) === "1000",
  `ledger top option "${legPick}"`,
);
await page.waitForTimeout(300);

// ---- D67: a split with an AMOUNT and NO CENTRE.
rec("the cost-split add button is there", await tabTo("alloc:0-add"));
await press("Enter", 500);
await tabTo("alloc:0-axis:0");
await page.keyboard.type("Department"); await page.waitForTimeout(400);
await page.getByTestId("alloc:0-amount:0").fill("1000");   // full amount, NO centre
await page.waitForTimeout(500);

const picker = await page.getByTestId("alloc:0").innerText().catch(() => "");
rec(
  "D67: the axis is NOT reported as allocated in full",
  !/allocated in full|complete/i.test(picker),
  picker.replace(/\n/g, " | ").slice(0, 150),
);
const refusal = await page.getByTestId("alloc:0-refusal").innerText().catch(() => "<no refusal>");
rec(
  "D67: the refusal says the split would be DROPPED, with both figures",
  /no cost centre/i.test(refusal) && /1000\.0000/.test(refusal) && /NOT SENT|dropped/i.test(refusal),
  refusal.replace(/\n/g, " ").slice(0, 220),
);

// ---- THE WRITE CONTROL: Ctrl+A must send NOTHING, and mint NO master.
const before = app.net.length;
await press("Control+a", 2500);
await press("Control+a", 1500);
const after = app.net.slice(before);
rec(
  "D67: ⛔ CONTROL — Ctrl+A posts NO voucher with a short axis",
  !after.some((w) => /POST .*\/vouchers\//.test(w)),
  after.join(" · ") || "(no non-GET traffic at all)",
);
rec(
  "D39/D67: ⛔ CONTROL — and mints NO ledger on the way to being refused",
  !after.some((w) => /POST .*\/ledgers/.test(w)),
  after.filter((w) => /ledger/.test(w)).join(" · ") || "(no ledger writes)",
);

// ---- And the fix must not have broken the HAPPY path: choose a centre.
await page.getByTestId("alloc:0-centre:0").selectOption({ index: 1 });
await page.waitForTimeout(500);
const picker2 = await page.getByTestId("alloc:0").innerText().catch(() => "");
rec(
  "D67: choosing a centre CLEARS the refusal",
  (await page.getByTestId("alloc:0-refusal").count()) === 0,
  picker2.replace(/\n/g, " | ").slice(0, 140),
);

// \u26d4 AND THE COMPLETED AXIS NOW SAVES — a control on the control, so
// "refuses everything" cannot pass as "refuses the right thing".
const beforeOk = app.net.length;
await press("Control+a", 4000);
const okWire = app.net.slice(beforeOk);
const no = await page.getByTestId("voucher-no").innerText().catch(() => "");
rec(
  "D67: ⛔ CONTROL — a COMPLETE split saves through the passkey",
  okWire.some((w) => /POST .*\/vouchers\/payment 201/.test(w)),
  `${no.replace(/\n/g, " ")} | ${okWire.join(" · ")}`,
);

console.log("\nNON-GET WIRE:", app.net.join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
