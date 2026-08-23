/**
 * L15c — D92: a REFUSED Ctrl+A on a master-create frame threw away the operator's work.
 *
 * ⛔ THE DEFECT, MEASURED IN A BROWSER BEFORE ANY CODE MOVED. Name a ledger,
 * leave the group blank, press Ctrl+A: the form is gone, the name with it, and
 * the reason is shown on the Gateway — a screen the operator can no longer act
 * on. `navMachine`'s `accept` reducer pops a master-create frame
 * UNCONDITIONALLY, because for its whole life the only way to reach that reducer
 * was a successful create, and the shell could not tell a refusal from a success
 * (both returned a plain string).
 *
 * ⚠️ IT WAS SURVIVABLE ONLY BECAUSE THE REFUSALS WERE RARE — a missing group, a
 * breakup that does not add up. **D87 makes one of them the common case**: every
 * party keyed without a state code. So this is fixed FIRST and on its own,
 * because D87 lands on top of it and would otherwise be a net harm.
 *
 * ⛔ THE CONTROL IS THE ARM THAT MATTERS. Suppressing the machine on a refusal
 * is only correct if a SUCCESS still navigates — Alt+C's entire purpose is
 * create-and-return. An arm that only proved "the frame stays" would pass just
 * as well against a build that had broken the accept path outright.
 *
 * ⛔⛔ IT REAPS THE PARTY IT CREATES, AND THAT IS NOT TIDINESS — IT IS D91.
 * This probe must actually CREATE a ledger (the success control is the arm that
 * matters), and it must do it in the DEMO tenant, because the shell will not
 * open on a company `openApp` cannot assert. A party left behind has no
 * `state_code`, no GSTIN and no bill-by-bill, so it fails three of
 * `verify:demo`'s party checks BY CONSTRUCTION — which is the exact defect D91
 * was registered and closed for, one lane earlier. The first run of this file
 * did leave one, and `verify:demo` went 34/34 -> 31/34.
 *
 * cored has no `DELETE /ledgers`, so the reap is raw SQL. Safe here and nowhere
 * near general: this ledger is seconds old and has never been posted to, so
 * nothing references it. ⛔ Do NOT copy this into a probe whose master HAS
 * postings — deleting one of those silently rewrites the books.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15c-d92.mjs
 */
import { execFileSync } from "node:child_process";

import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO;
if (!CO) throw new Error("drive-l15c-d92: CO is required (the seeded company id)");

/** The party this probe creates. Named so the reap below cannot match anything else. */
const PARTY = "D92 Probe Party";

const psql = (sql) =>
  execFileSync("psql", [
    "-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql,
  ], { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" }).trim();

const app = await openApp({ company: CO, headless: true });
const { page, press, altMac, toGateway } = app;

const openCreateLedger = async () => {
  await toGateway();
  await altMac("g");
  await page.waitForTimeout(300);
  await page.keyboard.type("Create Ledger");
  await page.waitForTimeout(400);
  await press("Enter", 1500);
};

/**
 * ⚠️ READ THE INPUT'S VALUE, NOT THE PAGE TEXT. `body.innerText` does NOT
 * contain input values, so the first version of this probe reported the keyed
 * name as lost even against the fixed build, while vitest (which reads `.value`)
 * said it survived. It throws rather than defaulting: an absent field must not
 * read as an empty one.
 */
const valueOf = async (testId) => {
  const el = page.getByTestId(testId);
  if ((await el.count()) === 0) throw new Error(`${testId} is not on the page`);
  return el.inputValue();
};
const onCreateForm = async () =>
  (await page.getByTestId("master-opening-balance").count()) === 1;

try {
  // ---- ARM 1: the no-group refusal. --------------------------------------
  await openCreateLedger();
  rec("the create-ledger frame opens from Alt+G", await onCreateForm());

  await page.keyboard.type("D92 Probe Party");
  await page.waitForTimeout(250);
  await press("Control+a", 1400);

  rec(
    "⛔ D92 ORACLE: after a refusal the FORM IS STILL UP",
    await onCreateForm(),
    (await onCreateForm())
      ? "master-opening-balance still present"
      : `popped to: ${(await page.locator("body").innerText()).slice(0, 120).replace(/\n/g, " | ")}`,
  );
  const kept = await valueOf("master-name");
  rec(
    "⛔ D92 ORACLE: and EVERYTHING KEYED SURVIVED",
    kept === "D92 Probe Party",
    `master-name = ${JSON.stringify(kept)} — the frame coming back EMPTY would be worth nothing`,
  );
  const status = await page
    .getByTestId("status-line")
    .innerText()
    .catch(() => "<no status line>");
  rec(
    "the reason reached the operator, on a screen they can still act on",
    /filed under a group/i.test(status),
    status.replace(/\n/g, " ").slice(0, 140),
  );

  // ---- ARM 2: the operator's NEXT keystroke (§4.4). ------------------------
  // ⛔ THE REFUSAL IS NOT THE END OF THE STORY; THE REMEDY IS. The remedy this
  // refusal invites is "choose a group and press Ctrl+A again", and it must
  // work from the frame the operator is still sitting on — with the name they
  // never had to retype.
  // ⚠️ getByRole("option"), NOT getByText. D87 put a party block on this form
  // whose own copy contains the words "Sundry Debtors" ("a state code is
  // REQUIRED under Sundry Debtors and Sundry Creditors"), so a text match now
  // resolves to two elements and Playwright's strict mode throws. The picker
  // entry is the OPTION; the sentence is prose.
  await page.getByRole("option", { name: "Sundry Debtors" }).click();
  await page.waitForTimeout(300);
  // ⚠️ AND THE PARTY NEEDS A STATE CODE NOW (D87). Without it this arm meets
  // D87's refusal instead of succeeding, and the CONTROL — "a success still
  // creates and pops" — would fail for a reason that has nothing to do with
  // D92. Supplying it keeps the control measuring what it was written to
  // measure.
  await page.getByTestId("master-state-code").selectOption("29");
  await page.waitForTimeout(150);
  await press("Control+a", 2500);
  rec(
    "§4.4: the remedy works from the SAME frame, without retyping",
    !(await onCreateForm()),
    (await onCreateForm())
      ? "the form is STILL up after a valid accept — the suppression is too broad"
      : "accepted and popped, as create-and-return must",
  );

  // ---- ARM 3: THE CONTROL. A success must still navigate. -----------------
  // Proven by arm 2 above; asserted separately against the BOOKS so it cannot
  // pass on the frame having popped for some other reason.
  const bodyText = await page.locator("body").innerText();
  rec(
    "⛔ CONTROL: the accept really created the ledger (not just closed the form)",
    /Created ledger/i.test(bodyText) && bodyText.includes("D92 Probe Party"),
    bodyText.split("\n").find((l) => /Created ledger/i.test(l)) ?? "<no creation message>",
  );
} finally {
  await app.browser.close();

  // ⛔ THE REAP, AND IT IS ASSERTED RATHER THAN ASSUMED. A cleanup that
  // silently failed would leave the same landmine while the run looked clean —
  // which is how D91 accumulated across three sessions in the first place.
  const posted = psql(
    `SELECT count(*) FROM posting p JOIN ledger l ON l.id = p.ledger_id
      WHERE l.company_id = '${CO}' AND l.name = '${PARTY}'`,
  );
  if (posted !== "0") {
    throw new Error(
      `refusing to reap "${PARTY}": it has ${posted} posting(s). Deleting a ` +
        `master that has been posted to rewrites the books.`,
    );
  }
  psql(`DELETE FROM ledger WHERE company_id = '${CO}' AND name = '${PARTY}'`);
  const left = psql(
    `SELECT count(*) FROM ledger WHERE company_id = '${CO}' AND name = '${PARTY}'`,
  );
  rec(
    "cleanup (D91): the probe's own party is gone from the demo tenant",
    left === "0",
    `${left} left — a party with no state_code fails verify:demo's checks by construction`,
  );
}

summary();
