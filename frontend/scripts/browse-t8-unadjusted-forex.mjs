#!/usr/bin/env node
/**
 * browse-t8-unadjusted-forex.mjs — T8 STEP 7 IN A REAL BROWSER.
 *
 * ⛔⛔ THE DEFECT THIS PROBE EXISTS FOR IS D22, AND IT IS NOT ABOUT RENDERING.
 * "Cash Flow" and "Anomalies" were served by cored for a WHOLE WAVE with no door —
 * no Gateway entry, no Go To entry and no screen — while F11 advertised them as `on`.
 * A `ScreenKind` registration is not an exhaustive Record the way `REPORT_SLUGS` and
 * `REPORT_PERIOD_SHAPE` are, so `tsc` enforces NONE of it: a name in REPORT_NAMES
 * with no entry in `defaultScreens.tsx` renders "not available in this build" over a
 * report that is built, served and ACL-gated. Only a browser can tell.
 *
 * ⭐ AND IT ASSERTS THE TWO THINGS ONLY A BROWSER COMPOSES:
 *   1. THAT Alt+G REACHES IT BY NAME — the door, not the component.
 *   2. THAT THE RATES ARRIVE FROM CORED AND SURVIVE TO THE PAGE AT TEN DECIMALS.
 *      vitest proves the screen renders what a faithful double hands it; the drive
 *      proves cored emits ten decimals. Neither proves the two meet, and a field the
 *      server sends at ten decimals and the screen prints through `formatMoney` is
 *      green on both sides and wrong on the page.
 *
 * ⛔ IT DIRTIES A SCRATCH BOOK, NEVER THE DEMO ONE (§2.5):
 *
 *   node frontend/scripts/browse-t8-unadjusted-forex.mjs \
 *        "$(cat frontend/.t8-step6-scratch-company-id)"
 *
 * ⚠️ AND IT NEEDS A **FRESH** vite (§2.2). A stale one whose log shows
 * "Could not Fast Refresh" renders the previous build of the screen.
 */

import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.argv[2];
if (!CO) {
  console.error(
    'usage: node browse-t8-unadjusted-forex.mjs "$(cat frontend/.t8-step6-scratch-company-id)"\n' +
      "⛔ an EMPTY ?company= makes the app bootstrap a fresh empty tenant and every " +
      "screen on it looks clean.",
  );
  process.exit(2);
}

const CORED = "http://localhost:8087";
const j = async (method, path, { token, body } = {}) => {
  const r = await fetch(`${CORED}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const text = await r.text();
  let parsed;
  try {
    parsed = text ? JSON.parse(text) : {};
  } catch {
    parsed = { raw: text };
  }
  return { status: r.status, body: parsed, text };
};

// ---------------------------------------------------------------------------
// SEED, OVER THE WIRE. ⭐ There is no entry screen for a foreign voucher yet (that
// is step 8), so the bill is keyed by HTTP. The point of this probe is the READ.
// ---------------------------------------------------------------------------
const stamp = Math.random().toString(36).slice(2, 6);
const SUB = "browser-dev-owner";
const base = `/v1/companies/${CO}`;
const REF = `UFX-${stamp}`;
const B = "83651.23";
const F = "1000.00";
const R = "83.6512345678";
// ⭐ THE RATE IS DATED IN THE **PAST**, AND THE PROBE READS THE DEFAULT UNBOUNDED
// VIEW RATHER THAN DRIVING F2. Two reasons, the second measured:
//
//  1. The unbounded read is what an operator sees on opening the screen, so it is
//     the view worth proving.
//  2. ⛔ TYPING A DATE INTO THE F2 "To" FIELD PRODUCED `61231-02-02` — the field is
//     not a plain text input, so `fill("") + type()` concatenated into nonsense and
//     the screen showed `Unadjusted Forex Gain/Loss unavailable (422). reporting:
//     invalid as-on date "61231-02-02"`. ⭐ THE SERVER'S REFUSAL WAS CORRECT AND
//     NAMED THE BAD DATE, which is the only reason that was diagnosable in one run —
//     but a probe whose FIXTURE fails reads as a product defect, so the interaction
//     is avoided rather than worked around. Driving F2 properly is its own probe.
const RATE_ON = "2026-09-01";
const RATE_AS_ON = "88.4512345678";

const token = (
  await j("POST", "/auth/token", { body: { subject: SUB, amr: ["webauthn"] } })
).body.token;

// ⛔ IDEMPOTENT, for the reason `browse-t8-outstandings-forex.mjs` records at length:
// this probe is meant to be re-run against the same scratch book, and a harness that
// dies on `already has a ledger named …` looks like a broken harness rather than
// like an assertion that never ran.
const mk = async (name, group) => {
  const r = await j("POST", `${base}/ledgers`, {
    token,
    body: { name, group_name: group, created_by: SUB },
  });
  if (r.status === 201 || r.status === 200) return r.body.ledger_id;
  const list = await j("GET", `${base}/ledgers`, { token });
  const found = (list.body.ledgers ?? []).find((l) => l.name === name);
  if (!found) throw new Error(`ledger ${name}: ${r.status} ${r.text}`);
  return found.id;
};
const party = await mk(`Globex LLC ${stamp}`, "Sundry Debtors");
const sales = await mk("Export Sales", "Sales Accounts");
const usd = await (async () => {
  const r = await j("POST", `${base}/currencies`, {
    token,
    body: {
      code: "USD",
      symbol: "$",
      formal_name: "US Dollars",
      created_by: SUB,
    },
  });
  if (r.status === 201) return r.body.currency_id;
  const list = await j("GET", `${base}/currencies`, { token });
  const found = (list.body.currencies ?? []).find((c) => c.code === "USD");
  if (!found) throw new Error(`currency: ${r.status} ${r.text}`);
  return found.id;
})();

{
  const r = await j("POST", `${base}/vouchers/sales`, {
    token,
    body: {
      voucher_no: REF,
      voucher_date: "2026-06-10",
      party_ledger_id: party,
      party: "browser probe",
      created_by: SUB,
      postings: [
        { ledger_id: sales, dr_cr: "C", amount: B },
        {
          ledger_id: party,
          dr_cr: "D",
          amount: B,
          currency_id: usd,
          foreign_amount: F,
          rate: R,
        },
      ],
      bill_allocations: [
        {
          ledger_id: party,
          ref_name: REF,
          ref_type: "NEW",
          dr_cr: "D",
          amount: B,
          due_date: "2026-07-10",
          currency_id: usd,
          foreign_amount: F,
          rate: R,
        },
      ],
    },
  });
  if (r.status !== 201) throw new Error(`seed the foreign bill: ${r.status} ${r.text}`);
}
{
  const r = await j("POST", `${base}/currencies/rates`, {
    token,
    body: {
      code: "USD",
      effective_date: RATE_ON,
      rate: RATE_AS_ON,
      created_by: SUB,
    },
  });
  if (r.status !== 201 && r.status !== 204)
    throw new Error(`rate: ${r.status} ${r.text}`);
}

const app = await openApp({ company: CO, expectCompany: "T8 Step6 Scratch" });
try {
  await app.press("Escape", 300);
  await app.toGateway();
  // ⛔ Alt+G BY NAME. This is the door, and D22 is what happens without it.
  await app.page.keyboard.down("Alt");
  await app.page.keyboard.press("KeyG");
  await app.page.keyboard.up("Alt");
  await app.page.waitForTimeout(400);
  await app.page.keyboard.type("Unadjusted Forex");
  await app.page.waitForTimeout(500);
  await app.press("Enter", 2500);

  const text0 = await app.body();
  rec(
    "T8/7 browser: ⛔ Alt+G reaches the report BY NAME (D22 — the door, not the component)",
    text0.includes("Unadjusted Forex"),
    `the frame reads ${JSON.stringify(text0.replace(/\s+/g, " ").slice(0, 160))} — ` +
      `"not available in this build" here means the ScreenKind registration is ` +
      `missing, and nothing type-checks it`,
  );
  rec(
    "T8/7 browser: ⛔ …and it is NOT the not-built placeholder",
    !/not available in this build|not built/i.test(text0),
    text0.replace(/\s+/g, " ").slice(0, 200),
  );

  const text = text0;

  rec(
    "T8/7 browser: ⭐ the seeded foreign bill is on the page",
    text.includes(REF),
    `looked for ${REF} in ${JSON.stringify(text.replace(/\s+/g, " ").slice(0, 220))}`,
  );
  // ⭐⭐ THE ASSERTION THAT NEEDS A BROWSER: cored's TEN decimals, on the page.
  rec(
    "T8/7 browser: ⛔⛔ the BILL RATE renders at TEN decimals — 83.6512345678",
    text.includes(R),
    `no "${R}" on the page. Rate-shaped strings present: ` +
      JSON.stringify(text.match(/\d+\.\d{4,}/g) ?? []) +
      " — a rate the server sends at ten decimals and the screen prints through " +
      "formatMoney is green on both sides and wrong here",
  );
  rec(
    "T8/7 browser: ⛔⛔ …and so does the RATE AS ON — 88.4512345678",
    text.includes(RATE_AS_ON),
    `no "${RATE_AS_ON}" on the page`,
  );
  rec(
    "T8/7 browser: ⭐ and the money columns are still MONEY (83,651.23)",
    text.includes("83,651.23"),
    "the two conventions must be visibly different on one page, or a reader " +
      "cannot tell them apart",
  );
  // ⛔ Q4's SENTENCE, ON THE SCREEN, IN THOSE WORDS.
  rec(
    "T8/7 browser: ⛔ the screen says the difference is REPORTED AND NOT POSTED",
    /REPORTED AND NOT POSTED/.test(text),
    "a table of gains and losses reads as posted unless it says otherwise — this " +
      "is the FeaturesScreen failure mode with a number attached",
  );
  rec(
    "T8/7 browser: …and that it covers receivable AND payable",
    /receivable AND payable/.test(text),
    "a reader who assumed receivables only would read a NETTED total as a " +
      "receivable figure, and netting is the point",
  );
  rec(
    "T8/7 browser: no unexplained javascript errors",
    app.consoleErrors.length === 0,
    app.consoleErrors.slice(0, 3).join(" | "),
  );
} finally {
  await app.browser.close();
}

process.exit(summary());
