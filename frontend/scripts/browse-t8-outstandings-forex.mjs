#!/usr/bin/env node
/**
 * browse-t8-outstandings-forex.mjs — T8 STEP 6 IN A REAL BROWSER.
 *
 * ⛔ WHY THIS EXISTS BESIDE THE WIRE DRIVE AND THE VITEST SUITE, AND WHAT ONLY IT
 * CAN CATCH. `drive-t8-outstandings-forex.mjs` proves cored EMITS the three fields;
 * `ReportScreens.test.tsx` proves the screen RENDERS them from a faithful double.
 * Neither proves the two halves meet: the mock and the rest client share a
 * TypeScript type, so `tsc` guarantees the SHAPE agrees and guarantees nothing about
 * whether cored's actual JSON populates it. A field emitted as `""` by the server and
 * rendered from a mock that always fills it is green on both sides and blank on the
 * page — and "a feature nobody can find is a feature that is not built".
 *
 * ⛔ IT DIRTIES A SCRATCH BOOK, NEVER THE DEMO ONE (§2.5). Pass the id:
 *
 *   node frontend/scripts/browse-t8-outstandings-forex.mjs \
 *        "$(cat frontend/.t8-step6-scratch-company-id)"
 *
 * ⚠️ AND IT NEEDS A **FRESH** vite (§2.2). A stale one whose log shows
 * "Could not Fast Refresh" renders the previous build of the screen, and this probe
 * would then report step 6 as unshipped — or, worse, as shipped when it is not.
 */

import { openApp, rec, summary, EXPECTED_COMPANY } from "./audit-harness.mjs";

const CO = process.argv[2];
if (!CO) {
  console.error(
    'usage: node browse-t8-outstandings-forex.mjs "$(cat frontend/.t8-step6-scratch-company-id)"\n' +
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
// SEED, OVER THE WIRE. ⭐ The bill is keyed by HTTP rather than through the screen
// because there IS no entry screen for a foreign voucher yet — that is step 8. The
// point of this probe is the READ side.
// ---------------------------------------------------------------------------
const stamp = Math.random().toString(36).slice(2, 6);
const SUB = "browser-dev-owner";
const base = `/v1/companies/${CO}`;
const REF = `FXB-${stamp}`;
const B = "83651.23";
const F = "1000.00";
const R = "83.6512345678";

const token = (
  await j("POST", "/auth/token", { body: { subject: SUB, amr: ["webauthn"] } })
).body.token;

// ⛔ IDEMPOTENT, AND THAT IS NOT TIDINESS. §2.5: existing scratch books already
// carry drive data, and this probe is meant to be re-run — against the same book,
// after every edit to the screen, and against a cored deliberately mutated to prove
// the probe is not vacuous. The first re-run died on
// `already has a ledger named "Export Sales"` while the MUTATION it was measuring
// went unmeasured, which is the worst way for a probe to fail: it looks like the
// harness broke rather than like the assertion never ran.
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
  // A second run against the same scratch book finds USD already there.
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

const app = await openApp({ company: CO, expectCompany: "T8 Step6 Scratch" });
try {
  // Outstandings IS in REPORT_NAMES, so Alt+G reaches it.
  await app.press("Escape", 300);
  await app.toGateway();
  await app.page.keyboard.down("Alt");
  await app.page.keyboard.press("KeyG");
  await app.page.keyboard.up("Alt");
  await app.page.waitForTimeout(400);
  await app.page.keyboard.type("Outstandings");
  await app.page.waitForTimeout(400);
  await app.press("Enter", 2500);

  const text = await app.body();

  rec(
    "T8/6 browser: the Outstandings screen opened",
    text.includes("Outstandings"),
    text.replace(/\s+/g, " ").slice(0, 140),
  );
  // ⛔ THE COLUMN HEADER. A screen that reads the field and has no column for it
  // renders the value into whichever cell happens to be next.
  rec(
    "T8/6 browser: ⛔ the report has a FOREIGN column header",
    text.includes("Foreign"),
    `the header row is ${JSON.stringify(
      text.split("\n").find((l) => l.includes("Pending")) ?? "",
    )}`,
  );
  rec(
    "T8/6 browser: ⭐ the foreign bill is on the page by its reference",
    text.includes(REF),
    `looked for ${REF}`,
  );
  // ⭐⭐ THE ONE ASSERTION THAT NEEDS A BROWSER: the CODE and the AMOUNT, together,
  // rendered from cored's own JSON. `formatMoney` renders 1000.0000 as "1,000.00".
  rec(
    "T8/6 browser: ⭐⭐ the currency and the foreign amount RENDER — \"USD 1,000.00\"",
    text.includes("USD 1,000.00"),
    `no "USD 1,000.00" on the page. Present USD strings: ` +
      JSON.stringify(text.match(/USD[^\n]{0,16}/g) ?? []) +
      " — a field cored emits as \"\" and a mock always fills is green on both " +
      "sides and blank here",
  );
  // ⛔ AND THE BASE FIGURE IS STILL BESIDE IT, NOT REPLACED BY IT.
  rec(
    "T8/6 browser: ⛔ and the rupee figure is still there — 83,651.23",
    text.includes("83,651.23"),
    "the base figure is what ties to Sundry Debtors; the foreign column is beside " +
      "it, never instead of it",
  );
  // ⭐ THE CONTROL: the demo book's own rupee bills render NO currency. This scratch
  // book is a seeded demo, so it carries base bills of its own.
  // ⭐ THE CONTROL: the currency reaches the FOREIGN rows and no others. This is a
  // seeded demo book, so it carries dozens of rupee bills of its own.
  //
  // ⚠️ IT COUNTS AGAINST THE ROWS, NOT AGAINST 1. A re-run against the same scratch
  // book leaves the previous run's foreign bill in place, so an assertion of
  // "exactly one USD" is green on the first run and red for ever afterwards —
  // which reads as a regression in the product rather than in the probe.
  const lines = text.split("\n").filter((l) => l.trim() !== "");
  const usdLines = lines.filter((l) => l.includes("USD")).length;
  const fxbLines = lines.filter((l) => l.includes("FXB-")).length;
  rec(
    "T8/6 browser: ⭐ CONTROL — the currency reaches the FOREIGN rows and no others",
    usdLines > 0 && usdLines === fxbLines,
    `${usdLines} row(s) name USD and ${fxbLines} row(s) are foreign bills, out of ` +
      `${lines.length} rows — a column stamped on every row would show far more, ` +
      `and zero USD rows would mean it reaches none`,
  );
  rec(
    "T8/6 browser: no unexplained javascript errors",
    app.consoleErrors.length === 0,
    app.consoleErrors.slice(0, 3).join(" | "),
  );
} finally {
  await app.browser.close();
}

console.log(
  `\n(scratch book ${CO}, chrome expected to name "T8 Step6 Scratch"; ` +
    `EXPECTED_COMPANY default is "${EXPECTED_COMPANY}")`,
);
process.exit(summary());
