/**
 * THE PARITY-AUDIT BROWSER HARNESS — sign in, install a passkey, drive the app.
 *
 * ⛔ THIS FILE IS COMMITTED RATHER THAN DESCRIBED, for the reason
 * `e2e-journal.mjs` is: a recipe in a handoff gets re-derived every session and
 * drifts; a file gets imported. It exists because the 2026-08-07 audit pass
 * rebuilt this from scratch and lost an hour to two traps that are now baked in
 * below (`altMac` and the company assertion).
 *
 * USE IT:
 *
 *   import { openApp, rec, summary } from "./audit-harness.mjs";
 *   const app = await openApp({ company: process.env.CO });
 *   const { page, body, press, toGateway, altMac, net } = app;
 *   await toGateway();
 *   await altMac("g");                       // NOT press("Alt+g") — see below
 *   rec("Go To opened", /Go To/.test(await body()));
 *   await app.browser.close();
 *   process.exit(summary() > 0 ? 1 : 0);
 *
 * RUN IT (⛔ the absolute path matters — a bare `cat .demo-company-id` breaks
 * the moment your shell's cwd is not `frontend/`, and an EMPTY company id makes
 * the app bootstrap a fresh empty "My Company" whose every screen looks clean):
 *
 *   cd /path/to/tally-updated
 *   CO="$(cat frontend/.demo-company-id)" node frontend/scripts/<your-probe>.mjs
 *
 * PREREQS: cored :8087, insightd :8088, statutoryd :8091, vite :5173.
 * It starts none of them.
 */
import { createRequire } from "node:module";

// Resolved against frontend/ so a probe can live anywhere on disk.
const require = createRequire(new URL("./", import.meta.url));
const { chromium } = require("playwright");

export const URL_BASE = process.env.UI_BASE_URL ?? "http://localhost:5173/signin";
export const SUBJECT = process.env.E2E_SUBJECT ?? "browser-dev-owner";
export const PASSWORD = process.env.DEMO_PASSWORD ?? "tally-demo-2026";

/** The company the demo seed writes. Asserted on sign-in — see openApp. */
export const EXPECTED_COMPANY = process.env.EXPECTED_COMPANY ?? "Demo Traders";

export const results = [];

/** Record one probe result. `ok` false is a finding, not a crash. */
export const rec = (name, ok, detail = "") => {
  results.push({ name, ok });
  console.log(`${ok ? "✓" : "✗"} ${name}${detail ? "  — " + detail : ""}`);
};

/**
 * macOS composes Option+<letter> into a CHARACTER, so a real Mac never sends
 * `key: "l"` for Option+L — it sends `"¬"`. Playwright's `keyboard.press("Alt+l")`
 * synthesises `key: "l"`, i.e. an event no operator on this platform can
 * produce.
 *
 * ⛔⛔ THAT IS NOT A DETAIL. On 2026-08-07 every Alt+letter binding in the
 * product was found DEAD on macOS — Go To, Create Master, Export, Delete,
 * e-Way Bill, e-Invoice, Edit Log — and every browser test this repo had ever
 * run passed anyway, because they all pressed keys a Mac cannot send. The
 * resolver is fixed (`normalize.ts` prefers `ev.code` when Alt is held), but
 * the DRIVING METHOD is what hid it, so this table exists to stop the method
 * regressing.
 *
 * The composed characters for every letter, so a probe can assert the character
 * a Mac would really deliver rather than the one Playwright would rather send.
 */
export const MAC_OPTION_CHAR = {
  a: "å", b: "∫", c: "ç", d: "∂", e: "´", f: "ƒ", g: "©", h: "˙", i: "ˆ",
  j: "∆", k: "˚", l: "¬", m: "µ", n: "˜", o: "ø", p: "π", q: "œ", r: "®",
  s: "ß", t: "†", u: "¨", v: "√", w: "∑", x: "≈", y: "¥", z: "Ω",
};

/**
 * Open the app, sign in, and install a virtual WebAuthn authenticator so the
 * step-up ceremony on the first write completes by itself.
 *
 * ⚠️ THE AUTHENTICATOR IS INSTALLED BEFORE THE FIRST NAVIGATION, deliberately:
 * `stepUp()` ENROLS this device when it holds none of the listed credentials,
 * and a defect class this repo keeps finding (LEAD 1) only reproduces on the
 * FIRST write of a session — the one that carries the ceremony. Installing it
 * late, or reusing a warm session, hides exactly that.
 *
 * Chrome allows only ONE `internal` authenticator per environment; use
 * `transport: "usb"` if you ever need a second.
 */
export async function openApp({
  company,
  headless = true,
  expectCompany = EXPECTED_COMPANY,
  /**
   * \u26d4 OPT IN TO SIGNING IN WITH **NO** `?company=`, AND ONLY FOR D25.
   *
   * The refusal below exists because an empty `?company=` makes the app
   * bootstrap a fresh empty tenant called "My Company", and every screen on it
   * looks clean — two audit rows were wrongly logged as passing that way.
   *
   * But D25 CANNOT be driven any other way. Its fix (App.useSessionBootstrap)
   * adopts the invited company only when the operator has expressed no
   * preference at all, i.e. when nothing is persisted AND nothing is in the URL
   * — so a probe that passes `?company=` disables the very code it is testing
   * and would report the defect as fixed no matter what.
   *
   * The trap it re-opens is closed by `expectCompany`, which is REQUIRED here:
   * landing in a minted "My Company" is exactly what the assertion catches, so
   * this option makes the tenant check load-bearing instead of optional.
   */
  allowNoCompany = false,
  /**
   * \u26d4 WHO TO SIGN IN AS. Defaults to the demo owner, which is what every
   * probe before L9 wanted — but L9 MUST NOT use it: lockout is keyed on
   * SUBJECT, so one wrong password for `browser-dev-owner` locks the account
   * every other probe here signs in as. Without this option a probe that means
   * to drive a throwaway user silently drives the owner instead, which (a) makes
   * its assertions vacuous, because the owner is already a member of everything,
   * and (b) aims the owner's own lockout budget at whatever the probe types.
   * Both happened on the first L9 run.
   */
  subject = SUBJECT,
  password = PASSWORD,
} = {}) {
  if (allowNoCompany && !expectCompany) {
    throw new Error(
      "openApp: allowNoCompany REQUIRES expectCompany \u2014 without it, landing in a " +
        'freshly minted empty "My Company" is indistinguishable from landing in the ' +
        "company the invite was for, which is the whole assertion.",
    );
  }
  const browser = await chromium.launch({ headless });
  const context = await browser.newContext();
  const page = await context.newPage();

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

  /** Every non-GET the browser made, as "METHOD /path STATUS". The wire oracle. */
  const net = [];
  page.on("response", (r) => {
    const m = r.request().method();
    if (m !== "GET") {
      net.push(`${m} ${new global.URL(r.url()).pathname} ${r.status()}`);
    }
  });
  const consoleErrors = [];
  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(m.text());
  });

  if (!company && !allowNoCompany) {
    throw new Error(
      "openApp: no company id. An EMPTY ?company= makes the app BOOTSTRAP a fresh " +
        'empty tenant called "My Company", and every screen on it looks clean — ' +
        "two audit rows were wrongly logged as passing that way on 2026-08-07. " +
        'Pass CO="$(cat frontend/.demo-company-id)" with an ABSOLUTE path.',
    );
  }

  await page.goto(company ? `${URL_BASE}?company=${company}` : URL_BASE, {
    waitUntil: "networkidle",
  });
  await page.waitForTimeout(800);
  await page.keyboard.type(subject);
  await page.keyboard.press("Tab");
  await page.waitForTimeout(100);
  await page.keyboard.type(password);
  await page.keyboard.press("Enter");

  let signedIn = false;
  for (let i = 0; i < 40; i++) {
    if ((await page.getByTestId("login-screen").count()) === 0) {
      signedIn = true;
      break;
    }
    await page.waitForTimeout(250);
  }
  if (!signedIn) {
    const shownErr = await page
      .locator('[data-testid="login-error"]')
      .innerText()
      .catch(() => "");
    await browser.close();
    throw new Error(
      `openApp: could not sign in as "${subject}"` +
        (shownErr ? ` — the screen says: ${shownErr.replace(/\n/g, " ")}` : ""),
    );
  }
  await page.waitForTimeout(600);

  const body = () => page.locator("body").innerText();

  // ⛔ ASSERT THE TENANT. See the empty-company trap above: clean results on an
  // empty company are indistinguishable from clean results on a seeded one.
  //
  // ⚠️ IT POLLS, AND IT DID NOT USED TO. The chrome renders "Opening your
  // companies…" until the company list resolves, and that read is O(tenants) —
  // so on a database with several seeded drive tenants the fixed 600ms wait
  // above expires first and the assertion fires against a LOADING SCREEN. It
  // then reports "signed in to the WRONG COMPANY", which sends the next session
  // looking for a tenancy bug that is not there (2026-08-21, and it cost a
  // diagnosis TWICE). A fixed sleep in front of an assertion is a flake; the
  // failure message below is unchanged, so a REAL wrong tenant still reads the
  // same, and it now QUOTES the chrome so the two are distinguishable.
  //
  // ⚠ 40s, AND THE NUMBER IS MEASURED RATHER THAN GUESSED. This dev database
  // has accumulated 112 companies across every drive ever run here (`seed:demo`
  // mints a NEW one each time and nothing reaps them), and `GET /v1/me` on that
  // subject measures 0.5-3.5s wall — variable, because it returns the whole
  // membership list. Until those tenants are reaped, the browser bootstrap is
  // occasionally slower than any sane fixed wait.
  //
  // ⚠ AND "No Company" IS A LOADING STATE, NOT A RESOLVED ONE. AppShell renders
  // `company?.name ?? "No Company"` and paints the Gateway underneath it while
  // CompanyContext is still resolving — so a probe reading the chrome early sees
  // a fully-drawn app naming no tenant. Worth knowing it means "not yet".
  if (expectCompany) {
    let shown = "";
    for (let i = 0; i < 160; i++) {
      shown = await body();
      if (shown.includes(expectCompany)) break;
      await page.waitForTimeout(250);
    }
    if (!shown.includes(expectCompany)) {
      await browser.close();
      throw new Error(
        `openApp: signed in to the WRONG COMPANY — expected the chrome to name ` +
          `"${expectCompany}". A probe run against the wrong tenant proves nothing. ` +
          `Set EXPECTED_COMPANY if the demo has been renamed. The chrome says: ` +
          `${shown.replace(/\s+/g, " ").slice(0, 120)}`,
      );
    }
  }

  const press = async (combo, wait = 350) => {
    await page.keyboard.press(combo);
    await page.waitForTimeout(wait);
  };

  const atGateway = async () => (await body()).includes("MASTERS / TRANSACTIONS");

  const toGateway = async () => {
    for (let i = 0; i < 8; i++) {
      if (await atGateway()) return true;
      await press("Escape", 250);
    }
    return await atGateway();
  };

  /**
   * Press Alt+<letter> the way macOS really sends it: with the physical `code`,
   * which is what the fixed resolver reads. Use this for EVERY Alt binding.
   */
  const altMac = async (letter, wait = 500) => {
    await page.keyboard.down("Alt");
    await page.keyboard.press(`Key${letter.toUpperCase()}`);
    await page.keyboard.up("Alt");
    await page.waitForTimeout(wait);
    return MAC_OPTION_CHAR[letter.toLowerCase()];
  };

  /**
   * Open a screen that lives on the GATEWAY MENU and NOT in Go To.
   *
   * ⛔ `Alt+G` IS NOT THE ONLY DOOR. The Go To picker is hard-wired to
   * `["Vouchers", ...REPORT_NAMES]`, so Cost Centres, both cost summaries, Bank
   * Reconciliation, TDS Challans, the two order registers and the BOM screen are
   * absent from it and present on the Gateway. Two audit rows named the wrong
   * key and would have been logged as unreachable.
   */
  const openFromGateway = async (label, wait = 1500) => {
    await toGateway();
    const search = page.getByRole("combobox").first();
    await search.click();
    await page.waitForTimeout(150);
    await search.fill("");
    await page.keyboard.type(label);
    await page.waitForTimeout(400);
    await press("Enter", wait);
  };

  return {
    browser, context, page, cdp, net, consoleErrors,
    body, press, atGateway, toGateway, altMac, openFromGateway,
  };
}

/** Print the tally and return the failure count (use it as the exit code). */
export function summary() {
  const bad = results.filter((r) => !r.ok);
  console.log(`\n${results.length - bad.length}/${results.length} probes ok`);
  for (const b of bad) console.log(`   ✗ ${b.name}`);
  return bad.length;
}
