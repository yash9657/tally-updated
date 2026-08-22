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
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15c-d92.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const app = await openApp({ company: process.env.CO, headless: true });
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
  await page.getByText("Sundry Debtors").click();
  await page.waitForTimeout(300);
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
}

summary();
