#!/usr/bin/env node
/**
 * drive-t8-forex-settlement.mjs — T8 STEP 5 AGAINST THE REAL SYSTEM.
 *
 * ⛔⛔ THE ORACLE IS `psql` AND THE WIRE. NEVER THE SCREEN. Step 5 ships no UI (that
 * is step 8), and every figure here is read back out of `posting`, `bill_allocation`
 * and `ledger_balance` as TEXT so the SCALE is visible: 1600.0046 and 1599.97 are two
 * different strings here and two plausible numbers anywhere else.
 *
 * ⭐ A SEPARATE DRIVE FROM drive-t8-forex-entry.mjs, DELIBERATELY. That one is the
 * step-4 baseline at 50/50 and the handoff's §1 quotes it; folding step 5 into it
 * would make a regression in either half read as a change in the other.
 *
 * ⛔ PROBE 1 IS THE `routeAction` ARM, and it is first because nothing else can catch
 * it. A new {companyID} route with no arm compiles, passes every Go test — those call
 * the SERVICE, not the route — and is 403 FOR THE OWNER, because an unmapped route
 * default-denies. Only an HTTP call through the real middleware sees it.
 *
 * ⚠️ AND EVERY SETTLEMENT BELOW SETTLES AT A RATE DIFFERENT FROM THE BILL'S, at TWO
 * different rates across the file. §6.2: a settlement at the bill's own rate realises
 * (almost) nothing, and nothing is what a derivation that never ran also returns.
 *
 * Usage: node frontend/scripts/drive-t8-forex-settlement.mjs
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
const OWNER_SUB = `t8s-owner-${stamp}`;
const base = `/v1/companies/${CO}`;

// ---------------------------------------------------------------------------
// THE FIGURES. Mutually unmistakable, and the arithmetic is written out so a
// reader can check it without running anything.
// ---------------------------------------------------------------------------
// BILL 1 — a $1,000 export invoice. base !== foreign * rate, deliberately (Q2).
const B1 = "83651.23";
const F1 = "1000.00";
const R1 = "83.6512345678";
// Settled IN FULL at a higher rate, so a RECEIVABLE gains.
//   received = 1000 x 85.2512345678 = 85,251.2345678 -> 85,251.2346
//   cleared  = the whole book value                     83,651.23
//   realised GAIN                                        1,600.0046
const RS1 = "85.2512345678";
const RECEIVED_1 = "85251.2346";
const REALISED_1 = "1600.0046";
// ⚠️ THE PLAUSIBLE WRONG ANSWER, and the one this drive sends to prove it is
// refused: the memo's own `foreign x (r_settle - r_bill)` = 1000 x 1.6 = 1,600.0000.
const MEMO_FIGURE_1 = "1600.0000";

// BILL 2 — a $600 export invoice at a WHOLLY DIFFERENT rate, settled PARTIALLY.
const B2 = "45074.07";
const F2 = "600.00";
const R2 = "75.1234567890";
// $200 of $600, at a third rate.
//   received = 200 x 79.1234567890 = 15,824.6913578 -> 15,824.6914
//   cleared  = 45,074.07 x 200/600                     15,024.69
//   realised GAIN                                         800.0014
const RS2 = "79.1234567890";
const RECEIVED_2 = "15824.6914";
const CLEARED_2 = "15024.69";
const REALISED_2 = "800.0014";
// What is left open on bill 2 afterwards.
const REMAIN_2_BASE = "30049.3800";
const REMAIN_2_FOREIGN = "400.0000";

const OWNER = await mint(OWNER_SUB);
{
  const r = await j("POST", "/v1/companies", {
    token: OWNER,
    body: {
      id: CO,
      name: `T8 Settle Co ${stamp}`,
      fy_start_date: "2025-04-01",
      base_currency: "INR",
      state_code: "29",
    },
  });
  if (r.status !== 201) throw new Error(`create company: ${r.status} ${r.text}`);
}

try {
  console.log(`\n── tenant ${CO} ─────────────────────────────────`);

  const mkLedger = async (name, group) => {
    const r = await j("POST", `${base}/ledgers`, {
      token: OWNER,
      body: { name, group_name: group, created_by: OWNER_SUB },
    });
    if (r.status !== 201 && r.status !== 200)
      throw new Error(`ledger ${name}: ${r.status} ${r.text}`);
    return r.body.ledger_id;
  };
  const debtor = await mkLedger("Globex LLC (USA)", "Sundry Debtors");
  const sales = await mkLedger("Export Sales", "Sales Accounts");
  const bank = await mkLedger("EEFC Bank Account", "Bank Accounts");
  const forexPL = await mkLedger("Forex Gain/Loss", "Indirect Expenses");

  const usd = await (async () => {
    const r = await j("POST", `${base}/currencies`, {
      token: OWNER,
      body: { code: "USD", symbol: "$", formal_name: "US Dollars", created_by: OWNER_SUB },
    });
    if (r.status !== 201) throw new Error(`currency: ${r.status} ${r.text}`);
    return r.body.currency_id;
  })();

  // =========================================================================
  // 1. ⛔ THE SETTER, AND PROBE 1 IS THE ROUTE REGISTRATION ITSELF
  // =========================================================================
  {
    const r = await j("GET", `${base}/forex-settings`, { token: OWNER });
    ok(
      r.status === 200,
      "T8/5: ⛔ GET /forex-settings is 200 FOR THE OWNER — the routeAction arm exists",
      `status ${r.status} ${r.text} — a 403 here is the MISSING routeAction arm, not an ACL rule: ` +
        `an unmapped {companyID} route default-denies and nothing in Go would tell you`,
    );
    ok(
      (r.body.gain_loss_ledger_id ?? "") === "",
      "T8/5: nothing is chosen yet, and that is a 200 with an empty field",
      `body ${r.text} — a 404 would report the ordinary state of every company that ` +
        `never trades abroad the way a mistyped URL is reported`,
    );
  }
  {
    // ⛔ THE SUITABILITY REFUSAL. 0080's composite FK already refuses ANOTHER
    // company's ledger — that is tenancy. What it cannot refuse is a debtor, and a
    // realised gain credited there lands on a CUSTOMER'S OWN balance.
    const r = await j("PUT", `${base}/forex-settings`, {
      token: OWNER,
      body: { gain_loss_ledger_id: debtor },
    });
    ok(
      r.status === 422 && r.text.includes("revenue or expense"),
      "T8/5: ⛔ a SUNDRY DEBTORS ledger is refused, and the refusal says what to choose",
      `status ${r.status} ${r.text}`,
    );
  }
  {
    // ⭐ THE INVERSE CONTROL for the refusal above: without it, "the debtor was
    // refused" is equally consistent with EVERY ledger being refused — which is
    // exactly what spelling account_nature "Expenses" instead of 'EXPENSE' does,
    // silently, with no build or test failure anywhere.
    const r = await j("PUT", `${base}/forex-settings`, {
      token: OWNER,
      body: { gain_loss_ledger_id: forexPL },
    });
    ok(
      r.status === 204,
      "T8/5: ⭐ CONTROL — an Indirect Expenses ledger IS accepted (204)",
      `status ${r.status} ${r.text} — if every ledger is refused, the nature comparison matches nothing`,
    );
    const g = await j("GET", `${base}/forex-settings`, { token: OWNER });
    ok(
      g.body.gain_loss_ledger_id === forexPL,
      "T8/5: the choice reads back by id",
      `got ${g.body.gain_loss_ledger_id}, want ${forexPL}`,
    );
    ok(
      g.body.gain_loss_ledger_name === "Forex Gain/Loss",
      "T8/5: and by the operator's own spelling of it",
      `got ${g.body.gain_loss_ledger_name}`,
    );
    ok(
      psql(`SELECT COALESCE(forex_gain_loss_ledger_id::text,'NULL')
              FROM company_forex_settings WHERE company_id = '${CO}'`) === forexPL,
      "T8/5: ⭐ AND THE ROW IS REALLY THERE — psql, not the read-back",
      "a setter that returned 204 and wrote nothing passes every wire assertion above",
    );
  }

  // =========================================================================
  // 2. THE BILLS
  // =========================================================================
  const saleBody = (no, baseAmt, foreignAmt, rate) => ({
    voucher_no: no,
    voucher_date: "2025-06-10",
    party_ledger_id: debtor,
    party: "Globex LLC (USA)",
    created_by: OWNER_SUB,
    postings: [
      { ledger_id: sales, dr_cr: "C", amount: baseAmt },
      {
        ledger_id: debtor, dr_cr: "D", amount: baseAmt,
        currency_id: usd, foreign_amount: foreignAmt, rate,
      },
    ],
    bill_allocations: [
      {
        ledger_id: debtor, ref_name: no, ref_type: "NEW", dr_cr: "D",
        amount: baseAmt, due_date: "2025-07-10",
        currency_id: usd, foreign_amount: foreignAmt, rate,
      },
    ],
  });
  const postSale = async (body) => {
    const r = await j("POST", `${base}/vouchers/sales`, { token: OWNER, body });
    if (r.status !== 201) throw new Error(`sale ${body.voucher_no}: ${r.status} ${r.text}`);
    return r.body.voucher_id;
  };
  await postSale(saleBody("EXP-1", B1, F1, R1));
  await postSale(saleBody("EXP-2", B2, F2, R2));

  // A receipt settling `ref`. Every figure is a parameter, so this helper cannot
  // agree with the engine by construction.
  const receiptBody = (no, ref, foreign, rate, cleared, realised, withLeg = true) => {
    const postings = [
      { ledger_id: bank, dr_cr: "D", amount: withLeg
          ? (Number(cleared) + Number(realised)).toFixed(4) : cleared },
      {
        ledger_id: debtor, dr_cr: "C", amount: cleared,
        currency_id: usd, foreign_amount: foreign, rate,
      },
    ];
    if (withLeg && Number(realised) !== 0) {
      postings.push({ ledger_id: forexPL, dr_cr: "C", amount: realised });
    }
    return {
      voucher_no: no,
      voucher_date: "2025-09-15",
      party_ledger_id: debtor,
      party: "Globex LLC (USA)",
      created_by: OWNER_SUB,
      postings,
      bill_allocations: [
        {
          ledger_id: debtor, ref_name: ref, ref_type: "AGAINST", dr_cr: "C",
          amount: cleared,
          currency_id: usd, foreign_amount: foreign, rate,
        },
      ],
    };
  };
  const postReceipt = (body) =>
    j("POST", `${base}/vouchers/receipt`, { token: OWNER, body });

  // =========================================================================
  // 3. ⛔ THE REFUSAL WHEN NO LEDGER IS CHOSEN
  // =========================================================================
  {
    // Clear the choice first — which is itself the assertion that clearing works,
    // and that it puts the company back into the refusing state rather than
    // leaving a stale choice behind.
    const c = await j("PUT", `${base}/forex-settings`, {
      token: OWNER, body: { gain_loss_ledger_id: "" },
    });
    ok(c.status === 204, "T8/5: the choice can be CLEARED (204)", `status ${c.status}`);

    const r = await postReceipt(
      receiptBody("RCT-NOLEDGER", "EXP-1", F1, RS1, B1, REALISED_1));
    ok(
      r.status === 422,
      "T8/5: ⛔⛔ a settlement realising 1600.0046 is REFUSED with no ledger chosen",
      `status ${r.status} ${r.text} — accepted, the gain went to a ledger nobody chose ` +
        `or was absorbed, and the operator was told nothing`,
    );
    ok(
      r.text.includes("forex_gain_loss_ledger_id"),
      "T8/5: ⛔ and the refusal NAMES THE SETTING the operator must choose",
      `body ${r.text} — a refusal an operator cannot act on is an outage`,
    );
    ok(
      r.text.includes(REALISED_1),
      `T8/5: ⛔ and it names the AMOUNT (${REALISED_1}) that could not be posted`,
      `body ${r.text}`,
    );
    ok(
      psql(`SELECT count(*) FROM voucher_head vh JOIN voucher_version vv
              ON vv.id = vh.current_version_id
             WHERE vh.company_id = '${CO}' AND vv.voucher_no = 'RCT-NOLEDGER'`) === "0",
      "T8/5: ⭐ AND NOTHING WAS WRITTEN — the refusal rolled the whole post back",
      "a 422 with a voucher in the table is worse than an acceptance",
    );
    // Restore the choice for everything below.
    await j("PUT", `${base}/forex-settings`, {
      token: OWNER, body: { gain_loss_ledger_id: forexPL },
    });
  }

  // =========================================================================
  // 4. ⛔ THE VERIFY DESIGN — a figure the engine did not derive is refused
  // =========================================================================
  {
    const r = await postReceipt(
      receiptBody("RCT-MEMO", "EXP-1", F1, RS1, B1, MEMO_FIGURE_1));
    ok(
      r.status === 422,
      "T8/5: ⛔ the memo's own rate-difference figure (1600.0000) is REFUSED",
      `status ${r.status} ${r.text}`,
    );
    ok(
      r.text.includes(MEMO_FIGURE_1) && r.text.includes(REALISED_1),
      "T8/5: ⛔ and the refusal names BOTH figures, as deriveAndVerifyGST's does",
      `body ${r.text} — an operator cannot correct a number the message does not state`,
    );
  }
  {
    // Right magnitude, wrong SIDE, and the voucher still balances because the bank
    // leg was moved to suit. Only the derivation can catch this.
    const b = receiptBody("RCT-SIDE", "EXP-1", F1, RS1, B1, REALISED_1);
    b.postings[2].dr_cr = "D";
    b.postings[0].amount = (Number(B1) - Number(REALISED_1)).toFixed(4);
    const r = await postReceipt(b);
    ok(
      r.status === 422,
      "T8/5: ⛔ a GAIN posted as a LOSS is refused — wrong by twice the figure, and it balances",
      `status ${r.status} ${r.text}`,
    );
  }
  {
    // ⛔ THE SHAPE A CLIENT THAT KNOWS NOTHING ABOUT STEP 5 ACTUALLY SENDS: two
    // legs, no forex leg, and a bank amount that balances them. The double-entry
    // guard has nothing to say; only the derivation refuses it.
    const r = await postReceipt(
      receiptBody("RCT-NOLEG", "EXP-1", F1, RS1, B1, "0", false));
    ok(
      r.status === 422 && r.text.includes(REALISED_1),
      "T8/5: ⛔⛔ a BALANCED settlement with NO realised leg is refused, naming 1600.0046",
      `status ${r.status} ${r.text} — accepted, the bank shows 83,651.23 where ` +
        `85,251.2346 arrived and the gain is in no ledger anywhere`,
    );
  }

  // =========================================================================
  // 5. ⭐ THE SETTLEMENT THAT WORKS, AND EVERY FIGURE READ BACK FROM psql
  // =========================================================================
  const amountOf = (voucherNo, ledgerID) =>
    psql(`SELECT p.amount::text || '|' || p.dr_cr::text
            FROM posting p
            JOIN voucher_version vv ON vv.id = p.voucher_version_id
            JOIN voucher_head vh ON vh.current_version_id = vv.id
           WHERE vh.company_id = '${CO}' AND vv.voucher_no = '${voucherNo}'
             AND p.ledger_id = '${ledgerID}'`);
  const pendingOf = (ref) =>
    psql(`SELECT COALESCE(SUM(CASE WHEN b.dr_cr='D' THEN b.amount ELSE -b.amount END),0)::numeric(19,4)::text
            || '|' ||
            COALESCE(SUM(CASE WHEN b.dr_cr='D' THEN b.foreign_amount ELSE -b.foreign_amount END),0)::numeric(19,4)::text
            FROM bill_allocation b
            JOIN voucher_head vh ON vh.current_version_id = b.voucher_version_id
           WHERE b.company_id = '${CO}' AND b.ledger_id = '${debtor}'
             AND b.ref_name = '${ref}' AND NOT vh.is_deleted`);

  let rct1;
  {
    const r = await postReceipt(receiptBody("RCT-1", "EXP-1", F1, RS1, B1, REALISED_1));
    ok(r.status === 201, "T8/5: the full settlement of EXP-1 posts", `status ${r.status} ${r.text}`);
    rct1 = r.body.voucher_id;
    ok(
      amountOf("RCT-1", forexPL) === `${REALISED_1}|C`,
      `T8/5: ⭐ THE REALISED GAIN is ${REALISED_1} CREDIT, on the chosen ledger`,
      `got ${amountOf("RCT-1", forexPL)} — this is the figure the whole step exists to produce`,
    );
    ok(
      amountOf("RCT-1", bank) === `${RECEIVED_1}|D`,
      `T8/5: THE BANK LEG is ${RECEIVED_1} DEBIT — foreign x the settlement rate`,
      `got ${amountOf("RCT-1", bank)} — a bank figure that is not this ties to no statement`,
    );
    ok(
      pendingOf("EXP-1") === "0.0000|0.0000",
      "T8/5: ⭐ EXP-1 IS CLOSED IN BOTH CURRENCIES — base 0.0000 and foreign 0.0000",
      `got ${pendingOf("EXP-1")} — a residue here is what a rate-difference formula ` +
        `strands, and it ages for ever in Outstandings`,
    );
  }

  // =========================================================================
  // 6. §6.2 — A PARTIAL SETTLEMENT, AT A THIRD RATE
  // =========================================================================
  {
    const r = await postReceipt(
      receiptBody("RCT-2", "EXP-2", "200.00", RS2, CLEARED_2, REALISED_2));
    ok(r.status === 201, "T8/5: a PARTIAL settlement of EXP-2 posts", `status ${r.status} ${r.text}`);
    ok(
      amountOf("RCT-2", forexPL) === `${REALISED_2}|C`,
      `T8/5: ⛔ THE PARTIAL REALISES ${REALISED_2}, not the whole bill's difference`,
      `got ${amountOf("RCT-2", forexPL)} — an engine using foreign_BILLED instead of ` +
        `foreign_SETTLED derives 3x this and still balances`,
    );
    ok(
      pendingOf("EXP-2") === `${REMAIN_2_BASE}|${REMAIN_2_FOREIGN}`,
      `T8/5: ⭐ AND THE REMAINDER STAYS OPEN at ${REMAIN_2_BASE} / $${REMAIN_2_FOREIGN}`,
      `got ${pendingOf("EXP-2")} — this pins the PRO-RATA of the book value`,
    );
  }

  // =========================================================================
  // 7. ⛔ §6.1's EIGHTH CANDIDATE — AN ALTER RE-STATES
  // =========================================================================
  {
    const g = await j("GET", `${base}/vouchers/${rct1}`, { token: OWNER });
    ok(g.status === 200, "T8/5: the settlement reads back for an alter", `status ${g.status}`);

    // The ordinary round trip, with a client that rebuilt the body WITHOUT the
    // forex leg and balanced the bank leg to suit — precisely what postingsFromView
    // did for the triple before step 4 fixed it.
    const bad = receiptBody("RCT-1", "EXP-1", F1, RS1, B1, "0", false);
    bad.narration = "corrected narration";
    const r = await j("PUT", `${base}/vouchers/receipt/${rct1}`, { token: OWNER, body: bad });
    ok(
      r.status === 422,
      "T8/5: ⛔⛔ AN ALTER THAT OMITS THE REALISED LEG IS REFUSED",
      `status ${r.status} ${r.text} — accepted, the 1600.0046 in the P&L is gone, the ` +
        `bill still closes, the voucher still balances, and it is the EIGHTH ` +
        `occurrence of "an alter re-states"`,
    );
    ok(
      amountOf("RCT-1", forexPL) === `${REALISED_1}|C`,
      "T8/5: and the gain is STILL on the ledger after the refused alter",
      `got ${amountOf("RCT-1", forexPL)}`,
    );

    // ⭐ THE INVERSE CONTROL. Without it, "the alter was refused" is equally
    // consistent with altering a settlement being broken outright.
    const good = receiptBody("RCT-1", "EXP-1", F1, RS1, B1, REALISED_1);
    good.narration = "corrected narration";
    const r2 = await j("PUT", `${base}/vouchers/receipt/${rct1}`, { token: OWNER, body: good });
    ok(
      r2.status === 200,
      "T8/5: ⭐ CONTROL — an alter that DOES restate the leg succeeds",
      `status ${r2.status} ${r2.text} — the refusal above proves nothing if no alter can succeed`,
    );
    ok(
      amountOf("RCT-1", forexPL) === `${REALISED_1}|C`,
      "T8/5: and the gain survived the faithful alter",
      `got ${amountOf("RCT-1", forexPL)}`,
    );
    ok(
      pendingOf("EXP-1") === "0.0000|0.0000",
      "T8/5: ⛔ AND EXP-1 IS STILL CLOSED — the alter's AGAINST row did not double-count",
      `got ${pendingOf("EXP-1")} — the prior version's rows are live in the table when ` +
        `the derivation runs, so the lookup must exclude this voucher by voucher_id`,
    );
  }

  // =========================================================================
  // 8. ⭐ THE FREE ORACLE — ledger_balance, which is what verify:demo reads
  // =========================================================================
  {
    const want = (Number(REALISED_1) + Number(REALISED_2)).toFixed(4);
    const got = psql(`SELECT (credit - debit)::numeric(19,4)::text FROM ledger_balance
                       WHERE company_id = '${CO}' AND ledger_id = '${forexPL}'`);
    ok(
      got === want,
      `T8/5: ⭐ the Forex Gain/Loss ledger's NET CREDIT is ${want}`,
      `got ${got} — the sum of the two realised gains, and the figure a Profit & Loss reads`,
    );
  }

  // =========================================================================
  // 9. ⛔ THE CONTROL THE HOUSE STANDARD REQUIRES — a rupee bill is untouched
  // =========================================================================
  {
    // Every bill in every book that exists today is base currency. If step 5
    // moved any of them it would be a defect, not a re-pin.
    const inrDebtor = await mkLedger("Bharat Traders", "Sundry Debtors");
    const r1 = await j("POST", `${base}/vouchers/sales`, {
      token: OWNER,
      body: {
        voucher_no: "INR-1", voucher_date: "2025-06-10",
        party_ledger_id: inrDebtor, party: "Bharat Traders", created_by: OWNER_SUB,
        postings: [
          { ledger_id: sales, dr_cr: "C", amount: "50000.00" },
          { ledger_id: inrDebtor, dr_cr: "D", amount: "50000.00" },
        ],
        bill_allocations: [{
          ledger_id: inrDebtor, ref_name: "INR-1", ref_type: "NEW", dr_cr: "D",
          amount: "50000.00", due_date: "2025-07-10",
        }],
      },
    });
    ok(r1.status === 201, "T8/5: CONTROL — an ordinary rupee invoice still posts", `status ${r1.status} ${r1.text}`);

    const r2 = await j("POST", `${base}/vouchers/receipt`, {
      token: OWNER,
      body: {
        voucher_no: "INR-RCT-1", voucher_date: "2025-09-15",
        party_ledger_id: inrDebtor, party: "Bharat Traders", created_by: OWNER_SUB,
        postings: [
          { ledger_id: bank, dr_cr: "D", amount: "20000.00" },
          { ledger_id: inrDebtor, dr_cr: "C", amount: "20000.00" },
        ],
        bill_allocations: [{
          ledger_id: inrDebtor, ref_name: "INR-1", ref_type: "AGAINST", dr_cr: "C",
          amount: "20000.00",
        }],
      },
    });
    ok(
      r2.status === 201,
      "T8/5: ⭐⭐ CONTROL — a PARTIAL RUPEE SETTLEMENT still posts, untouched by all of this",
      `status ${r2.status} ${r2.text} — a derivation that ran unconditionally, or read ` +
        `the setting before deciding it needed it, takes bill-wise settlement down ` +
        `for every existing user`,
    );
    ok(
      amountOf("INR-RCT-1", forexPL) === "",
      "T8/5: CONTROL — and NO posting reached the forex ledger",
      `got ${amountOf("INR-RCT-1", forexPL)} — nothing about a rupee receipt involves a rate`,
    );
    const inrPending = psql(
      `SELECT COALESCE(SUM(CASE WHEN b.dr_cr='D' THEN b.amount ELSE -b.amount END),0)::numeric(19,4)::text
         FROM bill_allocation b
         JOIN voucher_head vh ON vh.current_version_id = b.voucher_version_id
        WHERE b.company_id = '${CO}' AND b.ledger_id = '${inrDebtor}'
          AND b.ref_name = 'INR-1' AND NOT vh.is_deleted`);
    ok(
      inrPending === "30000.0000",
      "T8/5: CONTROL — the rupee bill's pending is 30000.0000",
      `got ${inrPending}`,
    );
  }
} finally {
  // =========================================================================
  // ⛔ REAP, IN A `finally`, AND IT ASSERTS THE REAP WORKED. Copied from
  // drive-t8-forex-entry.mjs — the only reap in the repo that lists `currency_rate`
  // and `currency` — PLUS `company_forex_settings`, which is step 5's own table and
  // which no earlier reap lists. Leave it out and the delete fails on
  // company_forex_settings_ledger_fk with the tenant half-removed.
  //
  // ⚠️ NOT an HTTP DELETE: this tenant HAS BOOKS and posting_immutable RAISES on
  // DELETE, so the rows can only go with the triggers disarmed. SET LOCAL, so it
  // lasts only to COMMIT — a session-wide SET would leave the shared dev database
  // with immutability disarmed for whatever ran next on that connection.
  // =========================================================================
  const q = (s) => `'${String(s).replace(/'/g, "''")}'`;
  psql(
    `begin;
     set local session_replication_role = replica;
     delete from company_forex_settings where company_id = ${q(CO)};
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
  ok(left === "0", "T8/5: ⛔ the probe reaped its own tenant, and asserted it worked",
     `companies matching ${CO.slice(0, 8)}…: ${left}`);
}

console.log(`\nT8 step 5 — realised gain/loss on settlement: ${pass}/${pass + fail} passed`);
if (fail > 0) process.exit(1);
