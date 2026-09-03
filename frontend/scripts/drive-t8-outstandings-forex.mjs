#!/usr/bin/env node
/**
 * drive-t8-outstandings-forex.mjs — T8 STEP 6 AGAINST THE REAL SYSTEM.
 *
 * ⛔⛔ THE ORACLE IS THE WIRE AND `psql`. NEVER THE SCREEN. The screen is checked by
 * vitest against a faithful double; what cannot be checked there is whether cored's
 * SQL — four query sites over two tables, plus a residue computed as a difference of
 * sums — produces these figures on a real Postgres. Every amount is compared AS TEXT,
 * so the SCALE is visible: 800.0000 and 800.0 are two different strings here.
 *
 * ⭐ A SEPARATE DRIVE FROM drive-t8-forex-settlement.mjs, for that file's own stated
 * reason: it is the step-5 baseline at 35/35 and the handoff's §1 quotes it. Folding
 * step 6 in would make a regression in either half read as a change in the other.
 *
 * ⛔ WHAT THIS DRIVE EXISTS TO CATCH THAT NO GO TEST CAN:
 *   1. The wire SHAPE — the Go service can return `CurrencyCode` and the handler can
 *      drop it. Two of the three fields were added to three separate emitters
 *      (`/outstandings`, the open-bills picker, the printed copy) and nothing
 *      type-checks that a map literal is complete.
 *   2. The OPENING-BILL WRITE PATH over HTTP. 0080 gave `ledger_opening_bill` three
 *      currency columns and no route could set them; the Go test proves the masters
 *      service now can, and only this proves `POST /ledgers` carries them.
 *   3. A BOUNDED read (§6.2 — "an aggregate query is not a sample of the queries a
 *      user makes"): the as-on date below SPLITS the two settlements, so a report
 *      asked over the whole book would hide the case that breaks it.
 *
 * ⚠️ TWO DIFFERENT RATES THROUGHOUT (§6.2). A hard-coded rate passes with one.
 *
 * Usage: node frontend/scripts/drive-t8-outstandings-forex.mjs
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
    [
      "-h",
      "localhost",
      "-p",
      "5433",
      "-U",
      "postgres",
      "-d",
      "tally",
      "-tAc",
      sql,
    ],
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
const OWNER_SUB = `t8os-owner-${stamp}`;
const base = `/v1/companies/${CO}`;

// ---------------------------------------------------------------------------
// THE FIGURES — step 5's, so the two lanes cannot disagree about arithmetic.
// ---------------------------------------------------------------------------
// BILL 1 — $1,000 at 83.6512345678, carried at 83,651.23 (base !== foreign × rate,
// deliberately: Q2 makes the stored base authoritative). $200 settled at
// 85.2512345678: cleared 16,730.2460 pro-rata, realised 320.0009.
const B1 = "83651.23";
const F1 = "1000.00";
const R1 = "83.6512345678";
const RS1 = "85.2512345678";
const CLEARED_1 = "16730.2460";
const REALISED_1 = "320.0009";
const PEND_1_BASE = "66920.9840"; // 83,651.23 − 16,730.2460
const PEND_1_FGN = "800.0000"; // $1,000 − $200

// BILL 2 — $600 at a WHOLLY DIFFERENT rate. $200 settled at a third rate:
// cleared 15,024.69 pro-rata, realised 800.0014.
const B2 = "45074.07";
const F2 = "600.00";
const R2 = "75.1234567890";
const RS2 = "79.1234567890";
const CLEARED_2 = "15024.69";
const REALISED_2 = "800.0014";
const PEND_2_BASE = "30049.3800";
const PEND_2_FGN = "400.0000";

// The two settlements are on DIFFERENT DAYS so an as-on read can sit between them.
const SETTLE_1_DATE = "2025-06-20";
const SETTLE_2_DATE = "2025-07-20";
const SPLIT_DATE = "2025-06-30"; // after settlement 1, before settlement 2

const OWNER = await mint(OWNER_SUB);
{
  const r = await j("POST", "/v1/companies", {
    token: OWNER,
    body: {
      id: CO,
      name: `T8 Ageing Co ${stamp}`,
      fy_start_date: "2025-04-01",
      books_beginning_from: "2025-04-01",
      base_currency: "INR",
      state_code: "29",
    },
  });
  if (r.status !== 201) throw new Error(`create company: ${r.status} ${r.text}`);
}

try {
  console.log(`\n── tenant ${CO} ─────────────────────────────────`);

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
  const inrDebtor = await mkLedger("Bharat Traders", "Sundry Debtors");
  const creditor = await mkLedger("Nippon Kikai KK", "Sundry Creditors");
  const walkin = await mkLedger("Walk-In Exports", "Sundry Debtors");
  const sales = await mkLedger("Export Sales", "Sales Accounts");
  const purchases = await mkLedger("Import Purchases", "Purchase Accounts");
  const bank = await mkLedger("EEFC Bank Account", "Bank Accounts");
  const forexPL = await mkLedger("Forex Gain/Loss", "Indirect Expenses");

  const usd = await (async () => {
    const r = await j("POST", `${base}/currencies`, {
      token: OWNER,
      body: {
        code: "USD",
        symbol: "$",
        formal_name: "US Dollars",
        created_by: OWNER_SUB,
      },
    });
    if (r.status !== 201) throw new Error(`currency: ${r.status} ${r.text}`);
    return r.body.currency_id;
  })();

  {
    const r = await j("PUT", `${base}/forex-settings`, {
      token: OWNER,
      body: { gain_loss_ledger_id: forexPL },
    });
    if (r.status !== 204)
      throw new Error(`forex-settings: ${r.status} ${r.text}`);
  }

  // =========================================================================
  // 1. THE BILLS AND THEIR SETTLEMENTS
  // =========================================================================
  const saleBody = (no, party, baseAmt, foreignAmt, rate, date, due) => {
    const alloc = {
      ledger_id: party,
      ref_name: no,
      ref_type: "NEW",
      dr_cr: "D",
      amount: baseAmt,
      due_date: due,
    };
    const leg = { ledger_id: party, dr_cr: "D", amount: baseAmt };
    if (foreignAmt !== null) {
      Object.assign(alloc, {
        currency_id: usd,
        foreign_amount: foreignAmt,
        rate,
      });
      Object.assign(leg, { currency_id: usd, foreign_amount: foreignAmt, rate });
    }
    return {
      voucher_no: no,
      voucher_date: date,
      party_ledger_id: party,
      party: "drive",
      created_by: OWNER_SUB,
      postings: [{ ledger_id: sales, dr_cr: "C", amount: baseAmt }, leg],
      bill_allocations: [alloc],
    };
  };
  const post = async (cls, body) => {
    const r = await j("POST", `${base}/vouchers/${cls}`, {
      token: OWNER,
      body,
    });
    if (r.status !== 201)
      throw new Error(`${cls} ${body.voucher_no}: ${r.status} ${r.text}`);
    return r.body.voucher_id;
  };

  await post(
    "sales",
    saleBody("EXP-1", debtor, B1, F1, R1, "2025-06-10", "2025-07-10"),
  );
  await post(
    "sales",
    saleBody("EXP-2", debtor, B2, F2, R2, "2025-06-12", "2025-07-12"),
  );
  // ⭐ THE BASE CONTROL, on its own party: a rupee bill must age, net and report
  // exactly as it does today. Every bill in the field is one of these.
  await post(
    "sales",
    saleBody("INR-1", inrDebtor, "50000.00", null, null, "2025-06-15", "2025-07-15"),
  );

  const receiptBody = (no, party, ref, foreign, rate, cleared, realised, date) => {
    const partyLeg = { ledger_id: party, dr_cr: "C", amount: cleared };
    const alloc = {
      ledger_id: party,
      ref_name: ref,
      ref_type: "AGAINST",
      dr_cr: "C",
      amount: cleared,
    };
    if (foreign !== null) {
      Object.assign(partyLeg, {
        currency_id: usd,
        foreign_amount: foreign,
        rate,
      });
      Object.assign(alloc, { currency_id: usd, foreign_amount: foreign, rate });
    }
    const postings = [
      {
        ledger_id: bank,
        dr_cr: "D",
        amount: (Number(cleared) + Number(realised)).toFixed(4),
      },
      partyLeg,
    ];
    if (Number(realised) !== 0)
      postings.push({
        ledger_id: forexPL,
        dr_cr: "C",
        amount: Number(realised).toFixed(4),
      });
    return {
      voucher_no: no,
      voucher_date: date,
      party_ledger_id: party,
      party: "drive",
      created_by: OWNER_SUB,
      postings,
      bill_allocations: [alloc],
    };
  };

  await post(
    "receipt",
    receiptBody(
      "RCT-1", debtor, "EXP-1", "200.00", RS1, CLEARED_1, REALISED_1,
      SETTLE_1_DATE,
    ),
  );
  await post(
    "receipt",
    receiptBody(
      "RCT-2", debtor, "EXP-2", "200.00", RS2, CLEARED_2, REALISED_2,
      SETTLE_2_DATE,
    ),
  );
  await post(
    "receipt",
    receiptBody("INR-RCT", inrDebtor, "INR-1", null, null, "20000.00", "0",
      SETTLE_1_DATE),
  );

  // ---------------------------------------------------------------------------
  const outstandings = async (asOn) => {
    const q = asOn ? `?as_on=${asOn}` : "";
    const r = await j("GET", `${base}/outstandings?kind=RECEIVABLE${q ? "&" + q.slice(1) : ""}`, {
      token: OWNER,
    });
    if (r.status !== 200)
      throw new Error(`outstandings: ${r.status} ${r.text}`);
    return r.body;
  };
  const partyOf = (rep, name) =>
    (rep.parties ?? []).find((p) => p.ledger === name);
  const rowsOf = (rep, name, ref) =>
    (partyOf(rep, name)?.bills ?? []).filter((b) => b.ref_name === ref);
  const shape = (rows) =>
    rows
      .map(
        (b) =>
          `${b.ref_name}|${b.currency_code || "(base)"}|${b.pending}|${b.foreign_pending}${b.currency_conflict ? "|CONFLICT" : ""}`,
      )
      .join("  ");

  // =========================================================================
  // 2. ⭐⭐ THE CORE — TWO FIELDS ON THE WIRE, AT TWO DIFFERENT RATES
  // =========================================================================
  {
    const rep = await outstandings(null);
    for (const [ref, wantBase, wantFgn] of [
      ["EXP-1", PEND_1_BASE, PEND_1_FGN],
      ["EXP-2", PEND_2_BASE, PEND_2_FGN],
    ]) {
      const rows = rowsOf(rep, "Globex LLC (USA)", ref);
      ok(
        rows.length === 1,
        `T8/6: ${ref} is ONE row (one currency, correctly settled)`,
        `got ${rows.length}: ${shape(rows)}`,
      );
      const b = rows[0] ?? {};
      ok(
        b.currency_code === "USD",
        `T8/6: ⭐ ${ref} names its currency on the WIRE — "USD"`,
        `got ${JSON.stringify(b.currency_code)} — the service can return it and the ` +
          `handler's map literal can drop it, and nothing type-checks that`,
      );
      ok(
        b.foreign_pending === wantFgn,
        `T8/6: ⭐ ${ref} foreign pending is ${wantFgn} — the figure the exposure is measured in`,
        `got ${b.foreign_pending}`,
      );
      ok(
        b.pending === wantBase,
        `T8/6: ⛔ ${ref} BASE pending is still ${wantBase} — it ties to Sundry Debtors`,
        `got ${b.pending} — a foreign column that moved the base figure is a ` +
          `regression on the Balance Sheet`,
      );
      ok(
        b.currency_conflict === false,
        `T8/6: ${ref} is NOT flagged — one currency in, the same currency out`,
        `a disclosure that fires on the normal case stops being read`,
      );
    }

    // ⭐ THE BASE CONTROL, ASSERTED AS HARD AS THE SUBJECT.
    const ctl = rowsOf(rep, "Bharat Traders", "INR-1")[0] ?? {};
    ok(
      ctl.currency_code === "",
      'T8/6: ⭐ CONTROL — the rupee bill reports NO currency ("")',
      `got ${JSON.stringify(ctl.currency_code)} — every bill in the field is one of ` +
        `these, and a code here means they all just acquired a currency`,
    );
    ok(
      ctl.foreign_pending === "0.0000",
      "T8/6: ⭐ CONTROL — and a zero foreign figure",
      `got ${ctl.foreign_pending}`,
    );
    ok(
      ctl.pending === "30000.0000",
      "T8/6: ⭐ CONTROL — the rupee bill's pending is unchanged at 30000.0000",
      `got ${ctl.pending}`,
    );

    // ⚠️ THE GRAND TOTAL IS BASE-ONLY (Q5). 66,920.9840 + 30,049.38 + 30,000 —
    // NOT plus 800 and 400, which would be a figure in no currency that still foots.
    const wantTotal = (
      Number(PEND_1_BASE) +
      Number(PEND_2_BASE) +
      30000
    ).toFixed(4);
    ok(
      rep.total === wantTotal,
      `T8/6: ⚠️ the grand total is BASE-ONLY — ${wantTotal}`,
      `got ${rep.total}`,
    );
    const slabSum = (rep.slab_totals ?? [])
      .reduce((a, s) => a + Number(s.amount), 0)
      .toFixed(4);
    ok(
      (Number(slabSum) + Number(rep.on_account_total)).toFixed(4) === rep.total,
      "T8/6: sum(slab_totals) + on_account_total === total — the report's own reconciliation still holds",
      `slabs ${slabSum} + on-account ${rep.on_account_total} vs total ${rep.total}`,
    );
  }

  // =========================================================================
  // 3. ⛔ §6.2 — A DATE THAT SPLITS THE SETTLEMENTS
  // =========================================================================
  // An unbounded read, or one over the whole year, hides the case that breaks it:
  // at SPLIT_DATE the first settlement has happened and the second has NOT, so
  // EXP-2 must be back to its FULL $600 while EXP-1 shows $800.
  {
    const rep = await outstandings(SPLIT_DATE);
    const b1 = rowsOf(rep, "Globex LLC (USA)", "EXP-1")[0] ?? {};
    const b2 = rowsOf(rep, "Globex LLC (USA)", "EXP-2")[0] ?? {};
    ok(
      b1.foreign_pending === PEND_1_FGN,
      `T8/6: ⛔ as at ${SPLIT_DATE}, EXP-1 is ${PEND_1_FGN} — its settlement IS in scope`,
      `got ${b1.foreign_pending}`,
    );
    ok(
      b2.foreign_pending === F2 + "00",
      `T8/6: ⛔ as at ${SPLIT_DATE}, EXP-2 is back to the FULL ${F2}00 — its settlement is NOT`,
      `got ${b2.foreign_pending} — the foreign column must honour the same inclusion ` +
        `bound as the base column, or the two halves of one row describe two different days`,
    );
    ok(
      b2.pending === B2 + "00",
      `T8/6: and its base figure moves with it — ${B2}00`,
      `got ${b2.pending}`,
    );
    ok(
      rep.as_on === SPLIT_DATE,
      "T8/6: and the report echoes the bound it was given (D86)",
      `got ${JSON.stringify(rep.as_on)}`,
    );
  }

  // =========================================================================
  // 4. ⛔⛔ §0.2 — THE FOREIGN OPENING BILL, AND ITS WRITE PATH OVER HTTP
  // =========================================================================
  const migrated = await mkLedger("Migrated Exporter", "Sundry Debtors", {
    opening_balance: B2,
    opening_dc: "D",
    maintain_bill_by_bill: true,
    opening_bills: [
      {
        ref_name: "OPEN-FX",
        dr_cr: "D",
        amount: B2,
        due_date: "2025-05-01",
        currency_id: usd,
        foreign_amount: F2,
        rate: R2,
      },
    ],
  });
  {
    // ⛔ THE psql ORACLE FIRST: the columns 0080 added and no route could set.
    const row = psql(
      `SELECT COALESCE(currency_id::text,'NULL')||'|'||COALESCE(foreign_amount::text,'NULL')
              ||'|'||COALESCE(rate::text,'NULL')
         FROM ledger_opening_bill
        WHERE company_id = '${CO}' AND ledger_id = '${migrated}'`,
    );
    ok(
      row === `${usd}|${F2}00|${R2}`,
      "T8/6: ⛔ POST /ledgers WRITES the opening bill's currency, foreign amount and rate",
      `psql says ${row}, want ${usd}|${F2}00|${R2} — before step 6 these three columns ` +
        `existed with no write path at all, so a migrated foreign debtor could not be ` +
        `keyed as foreign, reported as foreign, or settled at all`,
    );
    ok(
      row.endsWith(`|${R2}`),
      "T8/6: ⚠️ and the RATE survives at TEN decimals — it is not money (§6.3)",
      `psql says ${row} — StringFixed(4) on 75.1234567890 gives 75.1235, which ` +
        `round-trips through its own type perfectly and is wrong for ever`,
    );
  }
  {
    const rep = await outstandings(null);
    const rows = rowsOf(rep, "Migrated Exporter", "OPEN-FX");
    ok(
      rows.length === 1 &&
        rows[0].currency_code === "USD" &&
        rows[0].foreign_pending === F2 + "00",
      `T8/6: ⛔⛔ the foreign OPENING bill is reported FOREIGN — USD ${F2}00`,
      `got ${shape(rows)} — Outstandings nets a UNION of bill_allocation AND ` +
        `ledger_opening_bill, and reading the currency off the first alone turns ` +
        `every migrated foreign debtor into a rupee debtor`,
    );
    ok(
      rows[0]?.pending === B2 + "00",
      `T8/6: and its base figure is ${B2}00`,
      `got ${rows[0]?.pending}`,
    );
  }
  {
    // ⭐ AND THE TWO TABLES MEET IN ONE GROUP: the opening bill and the receipt
    // that settles it must net, in BOTH columns, or the UNION is two reports.
    await post(
      "receipt",
      receiptBody(
        "RCT-OPEN", migrated, "OPEN-FX", "200.00", RS2, CLEARED_2, REALISED_2,
        SETTLE_1_DATE,
      ),
    );
    const rep = await outstandings(null);
    const rows = rowsOf(rep, "Migrated Exporter", "OPEN-FX");
    ok(
      rows.length === 1 &&
        rows[0].pending === PEND_2_BASE &&
        rows[0].foreign_pending === PEND_2_FGN,
      `T8/6: ⛔ the settled opening bill nets to ONE row, ${PEND_2_BASE} / USD ${PEND_2_FGN}`,
      `got ${shape(rows)} — two rows here means the opening row and the AGAINST row ` +
        `landed in different netting groups, and the bill reads as both outstanding ` +
        `in full AND over-settled`,
    );
    ok(
      psql(
        `SELECT p.amount::text FROM posting p
           JOIN voucher_version vv ON vv.id = p.voucher_version_id
           JOIN voucher_head vh ON vh.current_version_id = vv.id
          WHERE p.company_id = '${CO}' AND p.ledger_id = '${forexPL}'
            AND vv.voucher_no = 'RCT-OPEN'`,
      ) === REALISED_2,
      `T8/6: ⭐ and step 5's engine realised ${REALISED_2} against the OPENING bill's rate`,
      `a foreign opening bill that reads as base is not just unreportable, it is ` +
        `UNSETTLEABLE — the engine refuses "carried in the books' own currency"`,
    );
  }

  // =========================================================================
  // 5. ⛔⛔ Q5's DISCLOSED CONFLICT — A RUPEE RECEIPT AGAINST A DOLLAR BILL
  // =========================================================================
  // Step 5 deliberately does NOT refuse this (L36 item 2: catching it would put the
  // bill lookup on every base settlement in the product), on the stated grounds that
  // step 6's report discloses it instead. This is that disclosure.
  {
    await post(
      "sales",
      saleBody("EXP-3", debtor, B1, F1, R1, "2025-06-14", "2025-07-14"),
    );
    await post(
      "receipt",
      receiptBody("RCT-BASE", debtor, "EXP-3", null, null, B1, "0", SETTLE_1_DATE),
    );
    const rep = await outstandings(null);
    const rows = rowsOf(rep, "Globex LLC (USA)", "EXP-3");
    ok(
      rows.length === 2,
      "T8/6: ⛔⛔ EXP-3 is TWO rows — one per currency it is open under",
      `got ${rows.length}: ${shape(rows)} — with the currency out of the netting key ` +
        `this is ONE row with an EMPTY currency code, so nothing on the page says ` +
        `which currency the money is in or that a rupee receipt cleared a dollar bill`,
    );
    const fx = rows.find((b) => b.currency_code === "USD") ?? {};
    const bs = rows.find((b) => b.currency_code === "") ?? {};
    ok(
      fx.foreign_pending === F1 + "00",
      `T8/6: the USD row still shows ${F1}00 uncollected — the dollar debt was never settled`,
      `got ${fx.foreign_pending}`,
    );
    ok(
      (Number(fx.pending) + Number(bs.pending)).toFixed(4) === "0.0000",
      "T8/6: and the two base figures sum to ZERO — the rupee receivable WAS cleared",
      `got ${fx.pending} + ${bs.pending}`,
    );
    ok(
      rows.every((b) => b.currency_conflict === true),
      "T8/6: ⛔ BOTH rows carry currency_conflict — a data error, not a sum",
      `got ${shape(rows)} — two unmarked rows under one bill number read as a ` +
        `customer who numbered two invoices alike, which is harmless`,
    );
    ok(
      rowsOf(rep, "Globex LLC (USA)", "EXP-1").every(
        (b) => b.currency_conflict === false,
      ),
      "T8/6: ⭐ INVERSE CONTROL — EXP-1 on the SAME party is still unflagged",
      "a conflict computed per party rather than per reference would flag everything",
    );
  }

  // =========================================================================
  // 6. ⛔ ON ACCOUNT — BASE-ONLY, AND STILL EXACTLY ONE ROW PER PARTY
  // =========================================================================
  {
    // (a) an EXPLICIT foreign ON_ACCOUNT allocation — source 1, carrying USD.
    await post("receipt", {
      voucher_no: "OA-USD",
      voucher_date: SETTLE_1_DATE,
      party_ledger_id: walkin,
      party: "drive",
      created_by: OWNER_SUB,
      postings: [
        { ledger_id: bank, dr_cr: "D", amount: "8365.12" },
        {
          ledger_id: walkin,
          dr_cr: "C",
          amount: "8365.12",
          currency_id: usd,
          foreign_amount: "100.00",
          rate: R1,
        },
      ],
      bill_allocations: [
        {
          ledger_id: walkin,
          ref_name: "On Account",
          ref_type: "ON_ACCOUNT",
          dr_cr: "C",
          amount: "8365.12",
          currency_id: usd,
          foreign_amount: "100.00",
          rate: R1,
        },
      ],
    });
    // (b) an UNREFERENCED rupee receipt — source 3, the DERIVED residue.
    await post("receipt", {
      voucher_no: "OA-INR",
      voucher_date: SETTLE_1_DATE,
      party_ledger_id: walkin,
      party: "drive",
      created_by: OWNER_SUB,
      postings: [
        { ledger_id: bank, dr_cr: "D", amount: "5000.00" },
        { ledger_id: walkin, dr_cr: "C", amount: "5000.00" },
      ],
    });

    const rep = await outstandings(null);
    const oa = rowsOf(rep, "Walk-In Exports", "On Account");
    ok(
      oa.length === 1,
      "T8/6: ⛔⛔ the party carries exactly ONE On Account row",
      `got ${oa.length}: ${shape(oa)} — the two sources of On Account are keyed on ` +
        `different tables, so putting the currency in this bucket's key gives a ` +
        `party two rows that EACH LOOK RIGHT and that still foot to the same total`,
    );
    ok(
      (oa[0]?.currency_code ?? "x") === "" &&
        oa[0]?.foreign_pending === "0.0000",
      "T8/6: ⛔ and it is BASE-ONLY — a difference of sums over many rates has no single currency",
      `got ${shape(oa)}`,
    );
    ok(
      oa[0]?.pending === "-13365.1200",
      "T8/6: ⭐ THE FREE ORACLE — the one row is the party's WHOLE balance, -13365.1200",
      `got ${oa[0]?.pending} — a bucket reporting only one of the two receipts would ` +
        `still be a plausible figure`,
    );
    ok(
      oa[0]?.currency_conflict === false,
      "T8/6: and it is never in conflict with itself",
      `got ${shape(oa)}`,
    );
    const bal = psql(
      `SELECT (COALESCE(SUM(CASE WHEN dr_cr='D' THEN amount ELSE -amount END),0))::numeric(19,4)::text
         FROM posting p JOIN voucher_head vh ON vh.current_version_id = p.voucher_version_id
        WHERE p.company_id = '${CO}' AND p.ledger_id = '${walkin}' AND NOT vh.is_deleted`,
    );
    ok(
      bal === oa[0]?.pending,
      "T8/6: ⭐ AND psql AGREES WITH THE REPORT — the residue IS the ledger balance",
      `psql ${bal} vs report ${oa[0]?.pending}`,
    );
  }

  // =========================================================================
  // 7. ⚠️ THE PAYABLE SIDE — the netting sign is spelled TWICE in that query
  // =========================================================================
  {
    await post("purchase", {
      voucher_no: "IMP-1",
      voucher_date: "2025-06-11",
      party_ledger_id: creditor,
      party: "drive",
      created_by: OWNER_SUB,
      postings: [
        { ledger_id: purchases, dr_cr: "D", amount: B1 },
        {
          ledger_id: creditor,
          dr_cr: "C",
          amount: B1,
          currency_id: usd,
          foreign_amount: F1,
          rate: R1,
        },
      ],
      bill_allocations: [
        {
          ledger_id: creditor,
          ref_name: "IMP-1",
          ref_type: "NEW",
          dr_cr: "C",
          amount: B1,
          due_date: "2025-07-11",
          currency_id: usd,
          foreign_amount: F1,
          rate: R1,
        },
      ],
    });
    const r = await j("GET", `${base}/bills-payable`, { token: OWNER });
    ok(r.status === 200, "T8/6: GET /bills-payable is 200", `${r.status} ${r.text}`);
    const rows = rowsOf(r.body, "Nippon Kikai KK", "IMP-1");
    ok(
      rows.length === 1 &&
        rows[0].currency_code === "USD" &&
        rows[0].foreign_pending === F1 + "00",
      `T8/6: ⚠️ the foreign PAYABLE reports USD ${F1}00, POSITIVE on the creditor side`,
      `got ${shape(rows)} — a foreign column added to the receivable branch and ` +
        `forgotten on the payable one is a report that works for exporters and is ` +
        `blank for importers`,
    );
    ok(
      rows[0]?.pending === B1 + "00",
      `T8/6: and its base figure is ${B1}00`,
      `got ${rows[0]?.pending}`,
    );
  }

  // =========================================================================
  // 8. ⭐⭐ WHERE STEPS 6 AND 8 MEET — THE `AGAINST` PICKER'S OWN READ
  // =========================================================================
  // Step 5 chose a VERIFY design: the CLIENT computes the realised figure and the
  // server refuses any disagreement. A settlement form therefore has to know what
  // the bill is carried at in BOTH currencies, and this is the read that feeds it.
  // ⛔ IT IS A DIFFERENT HANDLER FROM /outstandings, with its own map literal.
  {
    const r = await j(
      "GET",
      `${base}/vouchers/receipt/open-bills/${debtor}?kind=RECEIVABLE`,
      { token: OWNER },
    );
    ok(r.status === 200, "T8/6: the open-bills picker read is 200", `${r.status} ${r.text}`);
    const b = (r.body.bills ?? []).find((x) => x.ref_name === "EXP-1") ?? {};
    ok(
      b.currency_code === "USD" && b.foreign_pending === PEND_1_FGN,
      `T8/6: ⭐⭐ the picker read carries the currency and USD ${PEND_1_FGN}`,
      `got ${JSON.stringify(b)} — without it a settlement form can offer only the ` +
        `rupee figure, and the operator keys an allocation the engine then refuses ` +
        `while holding no number they could have got right`,
    );
    ok(
      "currency_conflict" in b,
      "T8/6: and it forwards currency_conflict rather than filtering it",
      `got ${JSON.stringify(b)} — a reference in conflict is exactly the one a ` +
        `picker must not silently offer as a single settleable bill`,
    );
  }

  // =========================================================================
  // 9. ⛔ THE PRINTED COPY — D20's DEFECT IS TWO SURFACES OVER ONE FACT
  // =========================================================================
  {
    const r = await fetch(`${CORED}${base}/print/reports/outstandings`, {
      headers: { Authorization: `Bearer ${OWNER}` },
    });
    const body = await r.text();
    ok(r.status === 200, "T8/6: the printed ageing renders", `${r.status}`);
    ok(
      body.includes("Foreign"),
      "T8/6: ⛔ the printed ageing has a Foreign column",
      "a statement posted to a customer, of a dollar debt, with the currency dropped",
    );
    // ⛔ THE CODE BESIDE ITS FORMATTED AMOUNT, NOT A BARE `includes("USD")` — WHICH
    // WAS MEASURED AS A FALSE GREEN AND IS THE REASON THIS COMMENT EXISTS. This
    // drive's opening bill was originally named `OPEN-USD`, so the Bill Ref cell
    // carried the substring "USD" and the probe passed against a cored built with
    // the currency taken out of the netting key — the sheet named no currency at
    // all and the assertion did not notice. The ref is now `OPEN-FX` AND the probe
    // asserts a pair only the Foreign column can produce.
    ok(
      /USD\s+[\d,]+\.\d\d/.test(body),
      "T8/6: and a foreign bill's currency reaches the paper BESIDE ITS AMOUNT",
      `the sheet carries no "USD <amount>" pair — matched: ` +
        JSON.stringify(body.match(/USD[^<\n]{0,20}/g) ?? []),
    );
    ok(
      body.includes("two currencies"),
      "T8/6: ⛔ and the conflict is marked ON THE ROW — paper has no tooltip",
      "two rows under one bill number with nothing between them read as two bills",
    );
  }
} finally {
  // =========================================================================
  // ⛔ REAP, IN A `finally`, AND IT ASSERTS THE REAP WORKED. Copied from
  // drive-t8-forex-settlement.mjs — the one reap that lists `company_forex_settings`
  // as well as `currency_rate` and `currency` — PLUS `ledger_opening_bill`, which
  // step 6 is the first drive to write. Omit that one and the `ledger` delete fails
  // on ledger_opening_bill_ledger_id_fkey with the tenant half-removed.
  //
  // ⚠️ NOT an HTTP DELETE: this tenant HAS BOOKS and posting_immutable RAISES on
  // DELETE, so the rows can only go with the triggers disarmed. SET LOCAL, so it
  // lasts only to COMMIT.
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
    "T8/6: ⛔ the probe reaped its own tenant, and asserted it worked",
    `companies matching ${CO.slice(0, 8)}…: ${left}`,
  );
}

console.log(
  `\nT8 step 6 — Outstandings shows a foreign bill as foreign: ${pass}/${pass + fail} passed`,
);
if (fail > 0) process.exit(1);
