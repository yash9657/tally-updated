#!/usr/bin/env node
/**
 * drive-t8-unadjusted-forex.mjs — T8 STEP 7 AGAINST THE REAL SYSTEM.
 *
 * ⛔⛔ THE ORACLE IS THE WIRE AND `psql`. NEVER THE SCREEN. Every amount and BOTH
 * rates are compared AS TEXT, because §6.3's whole failure is a rendering choice:
 * `83.6512345678` and `83.65` are two different strings here and two plausible
 * numbers anywhere else.
 *
 * ⛔ PROBE 1 IS THE ROUTE REGISTRATION, and it is first because nothing else can
 * catch it. A new {companyID} route with no `routeAction` arm compiles, passes every
 * Go test — those call the SERVICE, not the route — and is 403 FOR THE OWNER, because
 * an unmapped route default-denies. ⚠️ AND `no such route` IS A DIFFERENT FAILURE: a
 * 404 means the route is not on the RUNNING BINARY, nine times out of ten a stale
 * cored. Read the message before debugging the code.
 *
 * ⭐⭐ WHAT ONLY THIS DRIVE CAN PROVE, BEYOND THE FIGURES:
 *   1. MIGRATION 0090's `display` ARM GOES RED-TO-GREEN. A report NAME arrives
 *      PRE-GRANTED to every seeded Data Entry level (0011's coarse
 *      `allow display report` matches any `report:NAME` — defect D95), so the refusal
 *      is the thing that had to be written and this is where it is measured.
 *   2. ⭐ AND THE `print` ARM IS TESTABLE HERE FOR THE FIRST TIME IN THIS FAMILY.
 *      Every earlier report with an ACL counterweight answers 501 on the print path,
 *      so its Owner control arm could only ever assert 501. THIS REPORT ACTUALLY
 *      PRINTS: Owner 200, clerk 403, on a route that really renders.
 *      ⚠️ The `print` refusal is still PRE-EXISTING (0038 seeds no coarse print
 *      grant), so it must not be reported as a second red-to-green win.
 *   3. THE COMPOSITION INVARIANT WITH STEP 5, ON REAL BOOKS: the report's predicted
 *      difference is POSTED through the settlement engine's verify path, which
 *      refuses any figure it did not derive itself. The engine is the assertion.
 *
 * Usage: node frontend/scripts/drive-t8-unadjusted-forex.mjs
 *        (needs cored on :8087 — verified FRESH, see HANDOFF §2.1 — and psql on :5433)
 */

import { execFileSync } from "node:child_process";
import crypto from "node:crypto";

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
const mint = async (subject) =>
  (await j("POST", "/auth/token", { body: { subject, amr: ["webauthn"] } })).body
    .token;

const psql = (sql) =>
  execFileSync(
    "psql",
    ["-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql],
    { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" },
  ).trim();

let pass = 0;
let fail = 0;
const ok = (cond, what, detail = "") => {
  if (cond) {
    pass += 1;
    console.log(`  ✓ ${what}`);
  } else {
    fail += 1;
    console.log(`  ✗ ${what}${detail ? `  — ${detail}` : ""}`);
  }
};

const stamp = Math.random().toString(36).slice(2, 8);
const CO = crypto.randomUUID();
const OWNER_SUB = `t8uf-owner-${stamp}`;
const CLERK = `t8uf-clerk-${stamp}`;
const CLERK_PW = "clerk-pass-2026";
const base = `/v1/companies/${CO}`;

// ---------------------------------------------------------------------------
// THE FIGURES. Step 5's and step 6's, so the three lanes cannot disagree about
// arithmetic, plus a FOURTH rate for the revaluation itself.
// ---------------------------------------------------------------------------
const B1 = "83651.23";
const F1 = "1000.00";
const R1 = "83.6512345678";
// $200 settled at 85.2512345678, clearing 16,730.2460 pro-rata (realised 320.0009).
const RS1 = "85.2512345678";
const CLEARED_1 = "16730.2460";
const REALISED_1 = "320.0009";
const PEND_1_BASE = "66920.9840";
const PEND_1_FGN = "800.0000";

const B2 = "45074.07";
const F2 = "600.00";
const R2 = "75.1234567890";

// ⭐ THE REVALUATION RATE — a FOURTH distinct rate, so nothing here can pass by
// accident of two figures coinciding.
const AS_ON = "2025-08-31";
const RATE_AS_ON = "88.4512345678";
// EXP-1: round(800 × 88.4512345678, 4) − 66,920.9840
//        = 70,760.9877 − 66,920.9840 = 3,840.0037
const DIFF_1 = "3840.0037";
// EXP-2 (untouched, $600 at 75.1234567890 carried at 45,074.07):
//        round(600 × 88.4512345678, 4) − 45,074.07
//        = 53,070.7407 − 45,074.07 = 7,996.6707
const DIFF_2 = "7996.6707";
// A PAYABLE for the same $1,000 at the same bill rate LOSES the same amount a
// receivable gains: round(1000 × 88.4512345678, 4) − 83,651.23 = 4,800.0046.
const DIFF_PAY = "-4800.0046";

const OWNER = await mint(OWNER_SUB);
{
  const r = await j("POST", "/v1/companies", {
    token: OWNER,
    body: {
      id: CO,
      name: `T8 Forex Co ${stamp}`,
      fy_start_date: "2025-04-01",
      books_beginning_from: "2025-04-01",
      base_currency: "INR",
      state_code: "29",
    },
  });
  if (r.status !== 201) throw new Error(`create company: ${r.status} ${r.text}`);
}

try {
  console.log(`\n── tenant ${CO}  ·  clerk ${CLERK} ─────────────`);

  // -------------------------------------------------------------------------
  // A REAL Data Entry member: mint -> first password -> invite -> CLAIM.
  // ⛔ THE CLAIM IS NOT OPTIONAL. Without it the token is 403 on everything and
  // every refusal below passes while proving nothing.
  // -------------------------------------------------------------------------
  const CLERK_TOK = await (async () => {
    const tok = await mint(CLERK);
    const pw = await j("POST", "/auth/password", {
      token: tok,
      body: { current_password: "", new_password: CLERK_PW },
    });
    if (pw.status !== 204) throw new Error(`clerk password: ${pw.status} ${pw.text}`);
    const inv = await j("POST", `${base}/members/invites`, {
      token: OWNER,
      body: { subject: CLERK, security_level_name: "Data Entry" },
    });
    if (inv.status !== 201) throw new Error(`invite: ${inv.status} ${inv.text}`);
    const me = await j("GET", "/v1/me", { token: tok });
    if (me.status !== 200) throw new Error(`claim: ${me.status} ${me.text}`);
    if ((me.body.claimed_invites ?? []).length !== 1)
      throw new Error("the invite was not claimed");
    return tok;
  })();

  const mkLedger = async (name, group, extra = {}) => {
    const r = await j("POST", `${base}/ledgers`, {
      token: OWNER,
      body: { name, group_name: group, created_by: OWNER_SUB, ...extra },
    });
    if (r.status !== 201 && r.status !== 200)
      throw new Error(`ledger ${name}: ${r.status} ${r.text}`);
    return r.body.ledger_id;
  };
  const debtor = await mkLedger("Globex LLC (USA)", "Sundry Debtors");
  const creditor = await mkLedger("Nippon Kikai KK", "Sundry Creditors");
  const inrDebtor = await mkLedger("Bharat Traders", "Sundry Debtors");
  const sales = await mkLedger("Export Sales", "Sales Accounts");
  const purchases = await mkLedger("Import Purchases", "Purchase Accounts");
  const bank = await mkLedger("EEFC Bank Account", "Bank Accounts");
  const forexPL = await mkLedger("Forex Gain/Loss", "Indirect Expenses");

  const currency = async (code, name) => {
    const r = await j("POST", `${base}/currencies`, {
      token: OWNER,
      body: { code, symbol: code, formal_name: name, created_by: OWNER_SUB },
    });
    if (r.status !== 201) throw new Error(`currency ${code}: ${r.status} ${r.text}`);
    return r.body.currency_id;
  };
  const usd = await currency("USD", "US Dollars");
  // ⛔ EUR GETS NO RATE, EVER. It is Decision 3's fixture: a currency with no rate
  // on file is an exposure nobody can price, and the report must say so rather than
  // revalue it at zero.
  const eur = await currency("EUR", "Euro");

  {
    const r = await j("PUT", `${base}/forex-settings`, {
      token: OWNER,
      body: { gain_loss_ledger_id: forexPL },
    });
    if (r.status !== 204) throw new Error(`forex-settings: ${r.status} ${r.text}`);
  }

  // =========================================================================
  // 1. ⛔ PROBE 1 — THE ROUTE REGISTRATION ITSELF
  // =========================================================================
  {
    const r = await j("GET", `${base}/unadjusted-forex`, { token: OWNER });
    ok(
      r.status === 200,
      "T8/7: ⛔ GET /unadjusted-forex is 200 FOR THE OWNER — the routeAction arm exists",
      `status ${r.status} ${r.text} — a 403 here is a MISSING routeAction arm (an ` +
        `unmapped {companyID} route default-denies and nothing in Go would tell ` +
        `you); a 404 saying "no such route" is a STALE cored, which is a different ` +
        `problem entirely`,
    );
    ok(
      r.body.posts_nothing === true,
      "T8/7: ⛔ the response SAYS IT POSTS NOTHING (Q4), so no client hard-codes the sentence",
      `body ${r.text}`,
    );
    ok(
      Array.isArray(r.body.bills) && r.body.bills.length === 0,
      "T8/7: an empty book is an empty LIST, not a 404",
      `body ${r.text}`,
    );
  }

  // =========================================================================
  // 2. THE BOOKS
  // =========================================================================
  const saleBody = (no, party, baseAmt, cur, foreignAmt, rate, date) => {
    const alloc = {
      ledger_id: party, ref_name: no, ref_type: "NEW", dr_cr: "D",
      amount: baseAmt, due_date: "2025-09-30",
    };
    const leg = { ledger_id: party, dr_cr: "D", amount: baseAmt };
    if (cur) {
      Object.assign(alloc, { currency_id: cur, foreign_amount: foreignAmt, rate });
      Object.assign(leg, { currency_id: cur, foreign_amount: foreignAmt, rate });
    }
    return {
      voucher_no: no, voucher_date: date, party_ledger_id: party, party: "drive",
      created_by: OWNER_SUB,
      postings: [{ ledger_id: sales, dr_cr: "C", amount: baseAmt }, leg],
      bill_allocations: [alloc],
    };
  };
  const post = async (cls, body) => {
    const r = await j("POST", `${base}/vouchers/${cls}`, { token: OWNER, body });
    if (r.status !== 201)
      throw new Error(`${cls} ${body.voucher_no}: ${r.status} ${r.text}`);
    return r.body.voucher_id;
  };

  await post("sales", saleBody("EXP-1", debtor, B1, usd, F1, R1, "2025-06-10"));
  await post("sales", saleBody("EXP-2", debtor, B2, usd, F2, R2, "2025-06-12"));
  // ⛔ THE EUR BILL — an exposure the report cannot price.
  await post("sales", saleBody("EUR-1", debtor, "90000.00", eur, "1000.00", "90.0000000000", "2025-06-13"));
  // ⭐ THE BASE CONTROL — a rupee bill has no rate and nothing to revalue, and must
  // not be on this report at all.
  await post("sales", saleBody("INR-1", inrDebtor, "50000.00", null, null, null, "2025-06-15"));

  // A partial settlement of EXP-1, so its remaining position is not the bill.
  await post("receipt", {
    voucher_no: "RCT-1", voucher_date: "2025-06-20", party_ledger_id: debtor,
    party: "drive", created_by: OWNER_SUB,
    postings: [
      { ledger_id: bank, dr_cr: "D", amount: (Number(CLEARED_1) + Number(REALISED_1)).toFixed(4) },
      { ledger_id: debtor, dr_cr: "C", amount: CLEARED_1, currency_id: usd, foreign_amount: "200.00", rate: RS1 },
      { ledger_id: forexPL, dr_cr: "C", amount: REALISED_1 },
    ],
    bill_allocations: [{
      ledger_id: debtor, ref_name: "EXP-1", ref_type: "AGAINST", dr_cr: "C",
      amount: CLEARED_1, currency_id: usd, foreign_amount: "200.00", rate: RS1,
    }],
  });

  // A foreign PAYABLE, for the sign.
  await post("purchase", {
    voucher_no: "IMP-1", voucher_date: "2025-06-11", party_ledger_id: creditor,
    party: "drive", created_by: OWNER_SUB,
    postings: [
      { ledger_id: purchases, dr_cr: "D", amount: B1 },
      { ledger_id: creditor, dr_cr: "C", amount: B1, currency_id: usd, foreign_amount: F1, rate: R1 },
    ],
    bill_allocations: [{
      ledger_id: creditor, ref_name: "IMP-1", ref_type: "NEW", dr_cr: "C",
      amount: B1, due_date: "2025-09-30", currency_id: usd, foreign_amount: F1, rate: R1,
    }],
  });

  // A foreign OPENING bill (§0.2 again — this report is a NEW spelling of the UNION).
  const migrated = await mkLedger("Migrated Exporter", "Sundry Debtors", {
    opening_balance: B2, opening_dc: "D", maintain_bill_by_bill: true,
    opening_bills: [{
      ref_name: "OPEN-FX", dr_cr: "D", amount: B2, due_date: "2025-05-01",
      currency_id: usd, foreign_amount: F2, rate: R2,
    }],
  });

  // ⛔ THE RATES. An EARLIER one and the one as on the report date, so "the latest
  // rate on or before" is a real question rather than "the only rate there is".
  for (const [on, rate] of [["2025-07-15", "80.0000000000"], [AS_ON, RATE_AS_ON]]) {
    const r = await j("POST", `${base}/currencies/rates`, {
      token: OWNER,
      body: { code: "USD", effective_date: on, rate, created_by: OWNER_SUB },
    });
    if (r.status !== 201 && r.status !== 204)
      throw new Error(`rate ${on}: ${r.status} ${r.text}`);
  }
  // ⛔ A USD RATE **AFTER** THE REPORT DATE, which must NOT be used.
  {
    const r = await j("POST", `${base}/currencies/rates`, {
      token: OWNER,
      body: { code: "USD", effective_date: "2025-12-31", rate: "99.0000000000", created_by: OWNER_SUB },
    });
    if (r.status !== 201 && r.status !== 204)
      throw new Error(`future rate: ${r.status} ${r.text}`);
  }

  const report = async (asOn, token = OWNER) => {
    const q = asOn ? `?as_on=${asOn}` : "";
    const r = await j("GET", `${base}/unadjusted-forex${q}`, { token });
    if (r.status !== 200) throw new Error(`unadjusted-forex: ${r.status} ${r.text}`);
    return r.body;
  };
  const row = (rep, ledger, ref) =>
    (rep.bills ?? []).find((b) => b.ledger === ledger && b.ref_name === ref);
  const shape = (rep) =>
    (rep.bills ?? [])
      .map(
        (b) =>
          `${b.ledger}/${b.ref_name}|${b.currency_code}|fgn=${b.foreign_pending}` +
          `|book=${b.book_value}|billRate=${b.bill_rate}|asOn=${b.rate_as_on}` +
          `|diff=${b.difference}${b.has_rate_as_on ? "" : "|NO-RATE"}`,
      )
      .join("\n      ");

  // =========================================================================
  // 3. ⭐⭐ THE FIVE FIGURES, AND THE RATES AT **TEN** DECIMALS
  // =========================================================================
  {
    const rep = await report(AS_ON);
    for (const [ref, wantFgn, wantBook, wantBillRate, wantDiff] of [
      ["EXP-1", PEND_1_FGN, PEND_1_BASE, R1, DIFF_1],
      ["EXP-2", "600.0000", "45074.0700", R2, DIFF_2],
    ]) {
      const b = row(rep, "Globex LLC (USA)", ref) ?? {};
      ok(
        b.currency_code === "USD" && b.foreign_pending === wantFgn,
        `T8/7: ${ref} — USD ${wantFgn} still open`,
        `got ${JSON.stringify(b)}`,
      );
      ok(
        b.book_value === wantBook,
        `T8/7: ${ref} — the books carry ${wantBook}`,
        `got ${b.book_value}`,
      );
      // ⛔⛔ THE ONE THAT MATTERS. `StringFixed(4)` on a rate READS as consistency
      // with every other number on this page and silently changes it.
      ok(
        b.bill_rate === wantBillRate + "".padEnd(0) &&
          b.bill_rate.split(".")[1]?.length === 10,
        `T8/7: ⛔ ${ref}'s BILL RATE survives at TEN decimals — ${wantBillRate}`,
        `got ${JSON.stringify(b.bill_rate)} — a rate through a money formatter gives ` +
          `four decimals, round-trips through its own type perfectly, and is wrong ` +
          `for ever (§6.3; step 5 measured 1600.0046 becoming 1599.9700)`,
      );
      ok(
        b.rate_as_on === RATE_AS_ON && b.has_rate_as_on === true,
        `T8/7: ⛔ ${ref}'s RATE AS ON survives at TEN decimals — ${RATE_AS_ON}`,
        `got ${JSON.stringify(b.rate_as_on)} / has=${String(b.has_rate_as_on)}`,
      );
      ok(
        b.difference === wantDiff,
        `T8/7: ⭐ ${ref}'s unrealised difference is ${wantDiff}`,
        `got ${b.difference} — ONE rounding, on the PRODUCT: quantising the rate ` +
          `first gives a plausible figure in the right column that is wrong for ever`,
      );
    }

    // ⭐ THE BASE CONTROL, ASSERTED AS HARD AS THE SUBJECT.
    ok(
      row(rep, "Bharat Traders", "INR-1") === undefined,
      "T8/7: ⭐ CONTROL — the RUPEE bill is not on this report at all",
      `it has no currency and no rate, so a row for it would put every bill in the ` +
        `field on a page about foreign exposure. Rows:\n      ${shape(rep)}`,
    );

    // ⛔ THE FUTURE RATE WAS NOT USED.
    ok(
      row(rep, "Globex LLC (USA)", "EXP-1")?.rate_as_on !== "99.0000000000",
      "T8/7: ⛔ the rate dated AFTER the report date is NOT used",
      `a rate is "as on" a date, and reading a later one restates a position with ` +
        `information that did not exist`,
    );
  }

  // =========================================================================
  // 4. ⛔⛔ DECISION 3 — A MISSING RATE IS NOT A RATE OF ZERO
  // =========================================================================
  {
    const rep = await report(AS_ON);
    const b = row(rep, "Globex LLC (USA)", "EUR-1") ?? {};
    ok(
      b.currency_code === "EUR",
      "T8/7: ⛔ the EUR bill is LISTED — an exposure with an unknown rate is still an exposure",
      `dropping it is how a reader takes a short report for a small exposure. ` +
        `Rows:\n      ${shape(rep)}`,
    );
    ok(
      b.has_rate_as_on === false && b.difference === "0.0000",
      "T8/7: ⛔⛔ …and it is NOT PRICED — no difference, and the flag says why",
      `got has=${String(b.has_rate_as_on)} diff=${b.difference} — revaluing at a ` +
        `rate of ZERO turns a 90,000.00 receivable into a LOSS of that whole ` +
        `amount: large, plausible, correctly signed, in the right column, from a ` +
        `rate nobody entered`,
    );
    ok(
      rep.unrated_count === 1,
      "T8/7: ⛔ and the report DISCLOSES that its total is short by one row",
      `unrated_count = ${rep.unrated_count} — a report whose total silently omits a ` +
        `row is worse than one that says what it omitted`,
    );
    // The total is the PRICED rows only, summed.
    const priced = (rep.bills ?? [])
      .filter((x) => x.has_rate_as_on)
      .reduce((a, x) => a + Number(x.difference), 0)
      .toFixed(4);
    ok(
      rep.total_difference === priced,
      `T8/7: the total (${rep.total_difference}) is exactly the priced rows' sum (${priced})`,
      "a reader adds this column up, so the two must agree",
    );
  }

  // =========================================================================
  // 5. ⛔ THE SIGN — ONE REPORT, BOTH SIDES, AND THEY OFFSET
  // =========================================================================
  {
    const rep = await report(AS_ON);
    const pay = row(rep, "Nippon Kikai KK", "IMP-1") ?? {};
    ok(
      pay.foreign_pending === F1 + "00" && pay.book_value === B1 + "00",
      `T8/7: the PAYABLE is USD ${F1}00 carried at ${B1}00, POSITIVE on the creditor side`,
      `got ${JSON.stringify(pay)}`,
    );
    ok(
      pay.difference === DIFF_PAY,
      `T8/7: ⛔ …and its difference is ${DIFF_PAY} — a LOSS`,
      `got ${pay.difference} — the same rise in the rate that GAINS ${DIFF_PAY.slice(1)} ` +
        `on a receivable LOSES it here. An engine reading the rates alone is wrong ` +
        `by TWICE the figure while every number on the page still looks like money`,
    );
    // ⭐ AND THEY ARE ON ONE REPORT, which is what makes a NET figure possible.
    const recEXP1 = row(rep, "Globex LLC (USA)", "EXP-1");
    ok(
      recEXP1 !== undefined && pay.difference !== undefined,
      "T8/7: ⭐ ONE report carries the receivable AND the payable (migration 0090's decision)",
      `two reports would show two numbers neither of which is the company's position`,
    );
  }

  // =========================================================================
  // 6. ⛔⛔ §0.2 — THE FOREIGN OPENING BILL, IN A SIXTH SPELLING OF THE UNION
  // =========================================================================
  {
    const rep = await report(AS_ON);
    const b = row(rep, "Migrated Exporter", "OPEN-FX") ?? {};
    ok(
      b.currency_code === "USD" && b.foreign_pending === F2 + "00",
      `T8/7: ⛔⛔ the foreign OPENING bill is on the report — USD ${F2}00`,
      `got ${JSON.stringify(b)} — this report reads a UNION of bill_allocation AND ` +
        `ledger_opening_bill, and handling only the first makes every migrated ` +
        `foreign debtor's exposure invisible. Rows:\n      ${shape(rep)}`,
    );
    ok(
      b.bill_rate === R2,
      `T8/7: …and its rate is read off the OPENING BREAKUP (${R2}), not defaulted`,
      `got ${b.bill_rate}`,
    );
    ok(
      b.difference === DIFF_2,
      `T8/7: …and it revalues to ${DIFF_2}`,
      `got ${b.difference}`,
    );
    void migrated;
  }

  // =========================================================================
  // 7. ⭐⭐ THE COMPOSITION INVARIANT WITH STEP 5 — THE ENGINE IS THE ASSERTION
  // =========================================================================
  // Settle EXP-2 IN FULL at exactly the rate the report priced it at. Step 5's engine
  // derives the realised figure from its own arithmetic and REFUSES any figure it did
  // not derive (naming both), so posting the REPORT'S OWN PREDICTION is only possible
  // if the two agree to the paisa.
  {
    const predicted = row(await report(AS_ON), "Globex LLC (USA)", "EXP-2").difference;
    const r = await j("POST", `${base}/vouchers/receipt`, {
      token: OWNER,
      body: {
        voucher_no: "RCT-FULL", voucher_date: AS_ON, party_ledger_id: debtor,
        party: "drive", created_by: OWNER_SUB,
        postings: [
          { ledger_id: bank, dr_cr: "D", amount: (Number(B2) + Number(predicted)).toFixed(4) },
          { ledger_id: debtor, dr_cr: "C", amount: B2, currency_id: usd, foreign_amount: F2, rate: RATE_AS_ON },
          { ledger_id: forexPL, dr_cr: "C", amount: predicted },
        ],
        bill_allocations: [{
          ledger_id: debtor, ref_name: "EXP-2", ref_type: "AGAINST", dr_cr: "C",
          amount: B2, currency_id: usd, foreign_amount: F2, rate: RATE_AS_ON,
        }],
      },
    });
    ok(
      r.status === 201,
      `T8/7: ⭐⭐ STEP 5's ENGINE ACCEPTS THE REPORT'S OWN PREDICTION (${predicted})`,
      `status ${r.status} ${r.text} — a 422 here means the two halves of T8 ` +
        `disagree about the same arithmetic, and nothing else in the product would ` +
        `say which is wrong`,
    );
    ok(
      psql(`SELECT p.amount::text FROM posting p
              JOIN voucher_version vv ON vv.id = p.voucher_version_id
              JOIN voucher_head vh ON vh.current_version_id = vv.id
             WHERE p.company_id = '${CO}' AND p.ledger_id = '${forexPL}'
               AND vv.voucher_no = 'RCT-FULL'`) === predicted,
      "T8/7: ⭐ and psql confirms that figure REALLY reached the forex ledger",
      "the wire's 201 says the engine agreed; psql says the money moved",
    );
    // ⛔ AND THE BILL IS OFF THE REPORT. Its difference is REALISED now, and
    // reporting it here as well would double-count it.
    const after = await report(AS_ON);
    ok(
      row(after, "Globex LLC (USA)", "EXP-2") === undefined,
      "T8/7: ⛔ …and EXP-2 leaves the report the moment it is settled",
      `it is realised now, and reporting it again double-counts it. ` +
        `Rows:\n      ${shape(after)}`,
    );
  }

  // =========================================================================
  // 8. ⚠️ THE DATE DOES TWO JOBS (D86, with a rate in it)
  // =========================================================================
  {
    // As at 2025-07-31 the 2025-07-15 rate of 80 applies and the 2025-08-31 rate
    // does not — and EXP-1 is included either way, so the difference is the RATE.
    const rep = await report("2025-07-31");
    const b = row(rep, "Globex LLC (USA)", "EXP-1") ?? {};
    ok(
      b.rate_as_on === "80.0000000000",
      "T8/7: ⚠️ asked as at 2025-07-31 the report prices at the 2025-07-15 rate (80)",
      `got ${b.rate_as_on} — the latest rate ON OR BEFORE the date, never the ` +
        `latest rate on the books`,
    );
    // round(800 × 80, 4) − 66,920.9840 = 64,000 − 66,920.9840 = −2,920.9840
    ok(
      b.difference === "-2920.9840",
      "T8/7: …so the same position is a LOSS of 2920.9840 on that day",
      `got ${b.difference} — one book, two dates, two honest answers`,
    );
    ok(
      rep.as_on === "2025-07-31" && rep.priced_as_on === "2025-07-31",
      "T8/7: and the report echoes the bound it was given (D86)",
      `as_on=${JSON.stringify(rep.as_on)} priced_as_on=${JSON.stringify(rep.priced_as_on)}`,
    );
    const unbounded = await report(null);
    ok(
      unbounded.as_on === "" && unbounded.priced_as_on !== "",
      "T8/7: ⛔ an UNBOUNDED read echoes as_on \"\" and STILL names the day it priced at",
      `as_on=${JSON.stringify(unbounded.as_on)} priced_as_on=${JSON.stringify(unbounded.priced_as_on)} ` +
        `— empty is an ANSWER, and a revaluation with no rate date is a number with ` +
        `no reference`,
    );
  }

  // =========================================================================
  // 9. ⛔⛔ MIGRATION 0090 — THE ARM THAT GOES RED-TO-GREEN
  // =========================================================================
  {
    // CONTROL FIRST: the clerk IS a member and the coarse report grant is real. A
    // 403 here would make every refusal below vacuous.
    const ctl = await j("GET", `${base}/trial-balance?as_on=2025-08-31`, {
      token: CLERK_TOK,
    });
    ok(
      ctl.status === 200,
      "T8/7: ⛔ CONTROL — the clerk IS a member and still reads the Trial Balance",
      `trial-balance = ${ctl.status} — a 403 here means the invite was never ` +
        `claimed and the refusals below prove nothing`,
    );
    const ctl2 = await j("GET", `${base}/outstandings?kind=RECEIVABLE`, {
      token: CLERK_TOK,
    });
    ok(
      ctl2.status === 200,
      "T8/7: ⭐ CONTROL — and still reads the AGEING, which is the report next door",
      `outstandings = ${ctl2.status} — this is what makes the refusal below a ` +
        `decision about THIS report rather than about this clerk`,
    );

    const r = await j("GET", `${base}/unadjusted-forex?as_on=${AS_ON}`, {
      token: CLERK_TOK,
    });
    ok(
      r.status === 403,
      "T8/7: ⛔⛔ the seeded Data Entry clerk is REFUSED the exposure report (0090)",
      `unadjusted-forex = ${r.status} — a 200 here is D95 unopposed: a report NAME ` +
        `arrives PRE-GRANTED because 0011's coarse \`allow display report\` matches ` +
        `any report:NAME, so the REFUSAL is the thing that had to be written`,
    );
    // ⚠️ THE PRINT ARM IS **PRE-EXISTING** AND MUST NOT BE REPORTED AS A SECOND WIN.
    const p = await fetch(`${CORED}${base}/print/reports/unadjusted-forex`, {
      headers: { Authorization: `Bearer ${CLERK_TOK}` },
    });
    ok(
      p.status === 403,
      "T8/7: ⚠️ the clerk is refused the PRINTED form too — and this arm is PRE-EXISTING",
      `print = ${p.status}. 0038 seeds NO coarse print grant, so print was already ` +
        `default-denied; 0090's print row is DURABILITY. It matters more here than ` +
        `in any earlier file because this report actually renders`,
    );
    ok(
      psql(`SELECT count(*) FROM acl_rule ar
              JOIN security_level sl ON sl.id = ar.security_level_id
             WHERE sl.company_id = '${CO}' AND sl.name = 'Data Entry'
               AND ar.resource = 'report:unadjusted-forex'`) === "2",
      "T8/7: ⛔ and psql confirms 0090 seeded BOTH rows for this fresh tenant",
      "the seeder runs at company creation; a missing row here means the PERFORM " +
        "is not in the winning seeder body",
    );
  }

  // =========================================================================
  // 10. ⭐ THE PRINTED FORM — TESTABLE HERE FOR THE FIRST TIME IN THIS FAMILY
  // =========================================================================
  {
    const r = await fetch(`${CORED}${base}/print/reports/unadjusted-forex?as_on=${AS_ON}`, {
      headers: { Authorization: `Bearer ${OWNER}` },
    });
    const body = await r.text();
    ok(
      r.status === 200,
      "T8/7: ⭐ the Owner PRINTS it — 200, not the 501 every earlier ACL'd report gives",
      `status ${r.status}`,
    );
    ok(
      body.startsWith("%PDF"),
      "T8/7: …and it is a real PDF",
      `first bytes ${JSON.stringify(body.slice(0, 12))}`,
    );
    // ⛔ Q4's SENTENCE IS ON THE PAPER. A printout outlives the screen it came from,
    // and a table of gains and losses on letterhead reads as posted unless it says
    // otherwise. ⚠️ PDF text is compressed, so this is asserted through the SIZE and
    // through the JSON+layout tests rather than by grepping the stream — see the
    // golden and the Go test. What IS checkable here is that the sheet is not empty.
    ok(
      body.length > 3000,
      "T8/7: …with rows on it, not an empty sheet",
      `${body.length} bytes — an empty exposure statement and a rendered one are ` +
        `both 200, and only the size tells them apart from out here`,
    );
  }

  // =========================================================================
  // 11. ⛔ WHAT IS DELIBERATELY NOT ON THE REPORT
  // =========================================================================
  {
    // An explicit FOREIGN On Account allocation. Step 6 collapses that bucket to
    // base, and revaluing the fragment that happens to carry an allocation would be
    // a partial revaluation of a figure no report presents in parts.
    const walkin = await mkLedger("Walk-In Exports", "Sundry Debtors");
    await post("receipt", {
      voucher_no: "OA-USD", voucher_date: "2025-06-25", party_ledger_id: walkin,
      party: "drive", created_by: OWNER_SUB,
      postings: [
        { ledger_id: bank, dr_cr: "D", amount: "8365.12" },
        { ledger_id: walkin, dr_cr: "C", amount: "8365.12", currency_id: usd, foreign_amount: "100.00", rate: R1 },
      ],
      bill_allocations: [{
        ledger_id: walkin, ref_name: "On Account", ref_type: "ON_ACCOUNT", dr_cr: "C",
        amount: "8365.12", currency_id: usd, foreign_amount: "100.00", rate: R1,
      }],
    });
    const rep = await report(AS_ON);
    ok(
      row(rep, "Walk-In Exports", "On Account") === undefined,
      "T8/7: ⛔ `On Account` is NOT on this report, even carrying a currency",
      `step 6 collapsed that bucket to base because it is the whole balance less ` +
        `every named reference over many rates; revaluing the fragment that happens ` +
        `to have an allocation is a partial revaluation of a figure no report ` +
        `presents in parts. Rows:\n      ${shape(rep)}`,
    );
    ok(
      row(rep, "Globex LLC (USA)", "EXP-1") !== undefined,
      "T8/7: ⭐ CONTROL — and the ordinary bills are still there",
      `so "nothing is reported" is distinguishable from "On Account was dropped"`,
    );
  }
} finally {
  // =========================================================================
  // ⛔ REAP, IN A `finally`, AND IT ASSERTS THE REAP WORKED. Copied from
  // drive-t8-outstandings-forex.mjs — the one reap that lists BOTH
  // `company_forex_settings` and `ledger_opening_bill`. Step 7 adds
  // `currency_rate`, which was already there, and `webauthn_credential` +
  // `auth_user`-adjacent rows are NOT reaped here for the reason every other drive
  // gives: the clerk subject is stamped per run and its user row is harmless.
  // =========================================================================
  const q = (s) => `'${String(s).replace(/'/g, "''")}'`;
  psql(
    `begin;
     set local session_replication_role = replica;
     delete from company_forex_settings where company_id = ${q(CO)};
     delete from ledger_opening_bill   where company_id = ${q(CO)};
     delete from bill_allocation      where company_id = ${q(CO)};
     delete from posting              where company_id = ${q(CO)};
     delete from voucher_head         where company_id = ${q(CO)};
     delete from voucher_version      where company_id = ${q(CO)};
     delete from ledger_balance       where company_id = ${q(CO)};
     delete from currency_rate        where company_id = ${q(CO)};
     delete from currency             where company_id = ${q(CO)};
     delete from acl_rule             where company_id = ${q(CO)};
     delete from company_membership   where company_id = ${q(CO)};
     delete from company_invite       where company_id = ${q(CO)};
     delete from notification_outbox  where company_id = ${q(CO)};
     delete from master_audit         where company_id = ${q(CO)};
     delete from ledger               where company_id = ${q(CO)};
     delete from godown               where company_id = ${q(CO)};
     delete from unit                 where company_id = ${q(CO)};
     delete from voucher_type         where company_id = ${q(CO)};
     delete from account_group        where company_id = ${q(CO)};
     delete from security_level       where company_id = ${q(CO)};
     delete from company              where id = ${q(CO)};
     commit;`,
  );
  const left = psql(`select count(*) from company where id = ${q(CO)}`);
  ok(
    left === "0",
    "T8/7: ⛔ the probe reaped its own tenant, and asserted it worked",
    `companies matching ${CO.slice(0, 8)}…: ${left}`,
  );
}

console.log(
  `\nT8 step 7 — the unadjusted-forex report: ${pass}/${pass + fail} passed`,
);
if (fail > 0) process.exit(1);
