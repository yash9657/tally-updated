/**
 * §7.3 C AND D — THE LAST TWO DOORS OF THE "WORK LANDS IN THE WRONG COMPANY"
 * CLASS, DRIVEN IN A REAL BROWSER.
 *
 * Both live in `src/app/CompanyContext.tsx` and both were left open when the
 * round-4 lane that owned them was killed by a session limit.
 *
 *   C — a `?company=` that fails the client-side uuid regex was SILENTLY
 *       DISCARDED and the localStorage company opened instead: real, populated
 *       books, nothing on screen wrong.
 *   D — a well-formed but NONEXISTENT `?company=` uuid WEDGED PERMANENTLY: cored
 *       answers 403 for a company that does not exist (membership is checked
 *       before any handler can 404), so `isDeadCompanyId`'s 404 arm is dead code
 *       and every reload asks the same dead question.
 *
 * ⛔ RUN IT AGAINST BOTH VERSIONS OF THE FILE. Against `git show HEAD:` the C
 * probes must FAIL — that is the defect reproduced in a browser, and a probe
 * that cannot fail proves nothing:
 *
 *   git show HEAD:frontend/src/app/CompanyContext.tsx > /tmp/head.tsx
 *   cp /tmp/head.tsx frontend/src/app/CompanyContext.tsx   # vite HMR picks it up
 *   CO="$(cat frontend/.demo-company-id)" node frontend/scripts/drive-7-3-cd-company.mjs
 *
 * PREREQS: cored :8087 and vite (VITE_LIVE=1). Set UI_BASE_URL if vite did not
 * get :5173.
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO;
if (!CO) throw new Error('set CO="$(cat frontend/.demo-company-id)"');
const BASE = process.env.UI_BASE_URL ?? "http://localhost:5173/";

/** A uuid that is well formed and names no company anywhere. §7.3 D. */
const DEAD_UUID = "deadbeef-0000-4000-8000-000000000000";
/** Not uuid-shaped: a half-substituted template, which is how C is really hit. */
const JUNK = "demo-co";

const app = await openApp({ company: CO });
const { page, body } = app;

const stored = () =>
  page.evaluate(() => globalThis.localStorage.getItem("tally.companyId"));
const notice = () =>
  page
    .locator('[data-testid="company-refused-notice"]')
    .innerText()
    .catch(() => "");
/**
 * Click the notice's exit. Returns false instead of throwing when the control is
 * absent, so a run against the PRE-FIX file reports every finding rather than
 * dying on the first missing selector — the whole point of driving both.
 */
const clickExit = async () => {
  const btn = page.locator('[data-testid="company-refused-forget"]');
  if ((await btn.count()) === 0) return false;
  await btn.click();
  await page.waitForTimeout(3000);
  return true;
};
const openTo = async (search) => {
  await page.goto(`${BASE}${search}`, { waitUntil: "networkidle" });
  await page.waitForTimeout(2500);
};

try {
  // The sign-in already opened the demo company, so this is the exact state an
  // operator is in: a GOOD id in localStorage, and a bad one in the address bar.
  rec("setup: the demo company is stored", (await stored()) === CO, await stored());

  // ══ §7.3 C ═══════════════════════════════════════════════════════════════
  await openTo(`?company=${JUNK}`);
  const cText = await body();
  const cNotice = await notice();
  rec(
    "C: the stored company is NOT opened behind a malformed ?company=",
    !cText.includes("Demo Traders"),
    cText.includes("Demo Traders")
      ? "THE DEFECT: Demo Traders opened anyway"
      : "no books opened",
  );
  rec("C: a notice appears", cNotice.length > 0);
  rec("C: it names what was ignored", cNotice.includes(JUNK));
  rec(
    "C: it says the stored company was not substituted",
    /not been quietly switched/i.test(cNotice),
  );
  rec("C: the stored id is left alone", (await stored()) === CO, await stored());

  // The exit: dropping the parameter is a deliberate act, so opening the stored
  // company AFTERWARDS is a choice rather than a substitution.
  const cExit = await clickExit();
  rec(
    "C: the notice's action then opens the stored company",
    cExit &&
    (await body()).includes("Demo Traders"),
  );
  rec(
    "C: and ?company= is gone from the address bar, with no reload",
    !(await page.evaluate(() => globalThis.location.search)).includes("company"),
    await page.evaluate(() => globalThis.location.search),
  );

  // ══ §7.3 D ═══════════════════════════════════════════════════════════════
  await openTo(`?company=${DEAD_UUID}`);
  const dNotice = await notice();
  rec("D: a nonexistent uuid is refused, not opened", dNotice.length > 0);
  rec("D: the refusal names the id", dNotice.includes(DEAD_UUID));
  rec(
    "D: it does NOT assert the company exists — a 403 cannot tell",
    /no such company exists/i.test(dNotice),
  );
  rec(
    "D: it says a reload cannot change the answer",
    /reload will NOT change this answer/i.test(dNotice),
  );
  rec(
    "D: nothing was opened and nothing minted",
    !(await body()).includes("Demo Traders"),
  );

  // The wedge, reproduced: the same URL again gets the same refusal for ever.
  await openTo(`?company=${DEAD_UUID}`);
  rec(
    "D: a reload really does repeat the refusal (the 404 valve never fires)",
    (await notice()).includes(DEAD_UUID),
  );

  // …and the exit ends it, in the same tab.
  const dExit = await clickExit();
  rec(
    "D: the action ENDS the wedge without a reload",
    dExit && (await body()).includes("Demo Traders"),
    (await body()).slice(0, 80).replace(/\s+/g, " "),
  );

  // ══ THE CONTROL. A good ?company= must still work exactly as before. ══════
  await openTo(`?company=${CO}`);
  rec(
    "CONTROL: a good ?company= still opens those books",
    (await body()).includes("Demo Traders") && (await notice()) === "",
  );
} finally {
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);
