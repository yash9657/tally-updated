/**
 * T8 STEP 4 — FOREIGN ENTRY ON A VOUCHER, driven end to end against a live cored.
 *
 * ⛔⛔ THIS PROBE MAKES ITS OWN TENANT AND REAPS IT IN A `finally` (the D91
 * pattern). It does NOT touch the demo tenant: it posts foreign vouchers, and
 * `verify:demo` asserts a book in which every posting is base.
 *
 * ===========================================================================
 * ⛔ THE ORACLE, AND WHY IT IS `psql` AND THE WIRE AND NEVER A SCREEN
 * ===========================================================================
 * Base, foreign and rate are THREE STORED VALUES and TWO OF THEM CAN BE WRONG
 * WHILE THE VOUCHER BALANCES PERFECTLY. A probe that asserted "the voucher
 * posted" would be green against a write path that discarded every foreign
 * figure it was handed — which is exactly what the write path did until this
 * commit. So every assertion below names the SPECIFIC value it pins, and reads
 * it out of the column it is supposed to be in.
 *
 * ===========================================================================
 * ⛔⛔ THE THREE FIGURES ARE MUTUALLY UNMISTAKABLE, AND THE TWO VOUCHERS ARE AT
 * DIFFERENT RATES. Both of those are the design memo §6.2's lesson applied:
 * "a probe that posts one foreign invoice and settles it at the same rate will
 * be green and will prove NOTHING, because the gain is zero and zero is what a
 * broken computation also returns." Round figures (1000/1000/1.0) would let a
 * rate written into the foreign column pass; ONE rate would let a hard-coded
 * constant pass. So: base 83,651.23 / foreign 1,000.00 / rate 83.6512345678 on
 * the first voucher, and a wholly different rate on the second.
 *
 * ⚠️ NO SCREEN IS DRIVEN, AND THAT IS NOT A GAP IN THE PROBE. There is no
 * foreign-voucher ENTRY UI in this step: the design memo's build order (§5)
 * exposes the operator LAST, after settlement, Outstandings and the unrealised
 * report exist — "a toggle that flips a flag with nothing behind it is worse
 * than no toggle at all". What ships here is the data path, and the wire is
 * where a data path is true or false.
 *
 * RUN:  node frontend/scripts/drive-t8-forex-entry.mjs      (takes NO CO — it makes one)
 */
import { execFileSync } from "node:child_process";

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
const OWNER_SUB = `t8-owner-${stamp}`;
const base = `/v1/companies/${CO}`;

// The specimen figures. NOTE base !== foreign * rate exactly — 1000 x
// 83.6512345678 is 83,651.2345678, which is not representable in fixed-4 money.
// That is the POINT of the memo's Q2: base is STORED and authoritative, never
// re-derived. A write path that recomputed it would post 83,651.2346 and the
// deferred double-entry trigger would refuse the voucher outright.
const BASE_1 = "83651.23";
const FOREIGN_1 = "1000.00";
const RATE_1 = "83.6512345678";
// A DIFFERENT rate on the second voucher, so a constant cannot pass.
const BASE_2 = "45074.07";
const FOREIGN_2 = "600.00";
const RATE_2 = "75.1234567890";

const OWNER = await mint(OWNER_SUB);
{
  const r = await j("POST", "/v1/companies", {
    token: OWNER,
    body: {
      id: CO,
      name: `T8 Forex Co ${stamp}`,
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
  const creditor = await mkLedger("Acme Inc (USA)", "Sundry Creditors");
  const purchase = await mkLedger("Imported Purchases", "Purchase Accounts");

  // =========================================================================
  // 1. THE CURRENCY MASTER (step 3's work, used here as a precondition)
  // =========================================================================
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
    ok(r.status === 201, "T8/4: a USD currency master is created", `status ${r.status} ${r.text}`);
    return r.body.currency_id;
  })();

  // =========================================================================
  // 2. THE WRITE PATH — all three values reach BOTH tables
  // =========================================================================
  const foreignBody = (no, date, baseAmt, foreignAmt, rate) => ({
    voucher_no: no,
    voucher_date: date,
    party_ledger_id: creditor,
    party: "Acme Inc (USA)",
    created_by: OWNER_SUB,
    postings: [
      { ledger_id: purchase, dr_cr: "D", amount: baseAmt },
      {
        ledger_id: creditor,
        dr_cr: "C",
        amount: baseAmt,
        currency_id: usd,
        foreign_amount: foreignAmt,
        rate,
      },
    ],
    bill_allocations: [
      {
        ledger_id: creditor,
        ref_name: no,
        ref_type: "NEW",
        dr_cr: "C",
        amount: baseAmt,
        due_date: "2025-07-10",
        currency_id: usd,
        foreign_amount: foreignAmt,
        rate,
      },
    ],
  });

  const post = async (body) => {
    const r = await j("POST", `${base}/vouchers/purchase`, { token: OWNER, body });
    if (r.status !== 201)
      throw new Error(`post ${body.voucher_no}: ${r.status} ${r.text}`);
    return r.body.voucher_id;
  };

  const v1 = await post(foreignBody("IMP-1", "2025-06-10", BASE_1, FOREIGN_1, RATE_1));
  const v2 = await post(foreignBody("IMP-2", "2025-06-20", BASE_2, FOREIGN_2, RATE_2));

  // The oracle: the columns themselves, as TEXT so the SCALE is visible. A rate
  // quantised to 83.6512 and a rate stored whole are two different strings here
  // and the same float everywhere else.
  const tripleOf = (table, voucherID, ledgerID) =>
    psql(`
      SELECT COALESCE(t.currency_id::text,'NULL') || '|' ||
             COALESCE(t.foreign_amount::text,'NULL') || '|' ||
             COALESCE(t.rate::text,'NULL') || '|' || t.amount::text
        FROM ${table} t
        JOIN voucher_head vh ON vh.current_version_id = t.voucher_version_id
       WHERE vh.voucher_id = '${voucherID}' AND t.ledger_id = '${ledgerID}'`);

  const assertTriple = (label, got, wantForeign, wantRate, wantBase) => {
    const [cur, foreign, rate, amount] = got.split("|");
    ok(cur === usd, `${label}: THE CURRENCY is the USD master`, `got ${cur}, want ${usd}`);
    ok(
      foreign === wantForeign,
      `${label}: THE FOREIGN AMOUNT is ${wantForeign}`,
      `got ${foreign}`,
    );
    ok(
      rate === wantRate,
      `${label}: THE RATE is ${wantRate} — all ten decimals`,
      `got ${rate} (a rate treated as fixed-4 money becomes ${wantRate.slice(0, wantRate.indexOf(".") + 5)})`,
    );
    ok(
      amount === wantBase,
      `${label}: THE BASE AMOUNT is ${wantBase}, unmoved`,
      `got ${amount}`,
    );
  };

  console.log("\n── the three values reach both tables ──────────");
  assertTriple("posting (IMP-1)", tripleOf("posting", v1, creditor), "1000.0000", "83.6512345678", "83651.2300");
  assertTriple("bill_allocation (IMP-1)", tripleOf("bill_allocation", v1, creditor), "1000.0000", "83.6512345678", "83651.2300");

  // ⛔ THE SECOND VOUCHER AT A DIFFERENT RATE. Without it, a write path that
  // stored a constant — or the first rate it ever saw — passes everything above.
  assertTriple("posting (IMP-2, a DIFFERENT rate)", tripleOf("posting", v2, creditor), "600.0000", "75.1234567890", "45074.0700");

  // ⛔ THE MIXED-VOUCHER HALF. The expense leg of the SAME voucher must stay
  // BASE. A write path that hung the currency off the VOUCHER instead of the
  // POSTING passes every assertion above and fails this one — and would retag
  // every rupee expense line on every foreign invoice in the book.
  {
    const got = tripleOf("posting", v1, purchase);
    const [cur, foreign, rate] = got.split("|");
    ok(
      cur === "NULL" && foreign === "NULL" && rate === "NULL",
      "T8/4: ⛔ the COUNTERPART leg of the same voucher is still BASE (three NULLs)",
      `got ${got} — the currency is per-POSTING, not per-voucher`,
    );
  }

  // =========================================================================
  // 3. THE WIRE READ-BACK — what a form is given to send back
  // =========================================================================
  console.log("\n── the GET hands a form all three, at full scale ──");
  const view = await (async () => {
    const r = await j("GET", `${base}/vouchers/${v1}`, { token: OWNER });
    if (r.status !== 200) throw new Error(`get: ${r.status} ${r.text}`);
    return r.body;
  })();
  {
    const leg = (view.postings ?? []).find((p) => p.currency_id);
    ok(!!leg, "T8/4: the GET body carries a posting with a currency_id at all");
    ok(leg?.currency_id === usd, "T8/4: the GET's THE CURRENCY is the USD master", `got ${leg?.currency_id}`);
    ok(leg?.foreign_amount === "1000.0000", "T8/4: the GET's THE FOREIGN AMOUNT is 1000.0000", `got ${leg?.foreign_amount}`);
    ok(
      leg?.rate === "83.6512345678",
      "T8/4: the GET's THE RATE is 83.6512345678 — a fixed-4 body would say 83.6512",
      `got ${leg?.rate}`,
    );
    const bill = (view.bill_allocations ?? []).find((b) => b.currency_id);
    ok(bill?.rate === "83.6512345678", "T8/4: the GET's BILL rate is 83.6512345678", `got ${bill?.rate}`);
    // ⚠️ ABSENT, NOT EMPTY, ON A BASE ROW. `"currency_id": ""` is a two-of-three
    // triple, which the server refuses — so a client echoing this body verbatim
    // would 422 on every rupee voucher in the book.
    const baseLeg = (view.postings ?? []).find((p) => p.ledger_id === purchase);
    ok(
      baseLeg && !("currency_id" in baseLeg) && !("rate" in baseLeg),
      "T8/4: ⛔ the base leg carries NO forex keys at all (absent, never empty-stringed)",
      `got ${JSON.stringify(baseLeg)}`,
    );
  }

  // =========================================================================
  // 4. THE ALTER ROUND TRIP — the defect this repo has now shipped six times
  // =========================================================================
  console.log("\n── an alter rebuilt from the GET preserves them ──");
  {
    const echo = {
      voucher_no: view.voucher_no,
      voucher_date: view.voucher_date,
      party_ledger_id: view.party_ledger_id,
      party: view.party,
      narration: "corrected the narration and nothing else",
      created_by: OWNER_SUB,
      postings: view.postings,
      bill_allocations: view.bill_allocations,
    };
    const r = await j("PUT", `${base}/vouchers/purchase/${v1}`, { token: OWNER, body: echo });
    ok(r.status === 200, "T8/4: an alter echoing the GET is accepted", `status ${r.status} ${r.text}`);
    assertTriple(
      "posting AFTER an alter rebuilt from the GET",
      tripleOf("posting", v1, creditor),
      "1000.0000",
      "83.6512345678",
      "83651.2300",
    );
    assertTriple(
      "bill_allocation AFTER an alter rebuilt from the GET",
      tripleOf("bill_allocation", v1, creditor),
      "1000.0000",
      "83.6512345678",
      "83651.2300",
    );
  }

  // ⛔ THE INVERSE CONTROL. Without it, "it survived" is equally consistent with
  // the values being immune to an alter, in which case everything above proves
  // nothing while reading as a guard.
  {
    const strip = (rows) =>
      rows.map((r) => {
        const c = { ...r };
        delete c.currency_id;
        delete c.currency;
        delete c.foreign_amount;
        delete c.rate;
        return c;
      });
    const r = await j("PUT", `${base}/vouchers/purchase/${v2}`, {
      token: OWNER,
      body: {
        voucher_no: "IMP-2",
        voucher_date: "2025-06-20",
        party_ledger_id: creditor,
        party: "Acme Inc (USA)",
        created_by: OWNER_SUB,
        postings: strip(foreignBody("IMP-2", "2025-06-20", BASE_2, FOREIGN_2, RATE_2).postings),
        bill_allocations: strip(
          foreignBody("IMP-2", "2025-06-20", BASE_2, FOREIGN_2, RATE_2).bill_allocations,
        ),
      },
    });
    ok(
      r.status === 200,
      "T8/4: ⛔ CONTROL — an alter that OMITS the three is accepted (a legal request)",
      `status ${r.status} ${r.text}`,
    );
    const got = tripleOf("posting", v2, creditor);
    ok(
      got.startsWith("NULL|NULL|NULL"),
      "T8/4: ⛔ CONTROL — and it DOES destroy them, so the survival above is the projection working",
      `got ${got}`,
    );
  }

  // =========================================================================
  // 5. THE BASE CONTROL — an ordinary rupee voucher is untouched by all of this
  // =========================================================================
  console.log("\n── the control: a rupee voucher is unchanged ────");
  {
    const vb = await post({
      voucher_no: "DOM-1",
      voucher_date: "2025-06-11",
      party_ledger_id: creditor,
      party: "Acme Inc (USA)",
      created_by: OWNER_SUB,
      postings: [
        { ledger_id: purchase, dr_cr: "D", amount: "5000.00" },
        { ledger_id: creditor, dr_cr: "C", amount: "5000.00" },
      ],
      bill_allocations: [
        {
          ledger_id: creditor,
          ref_name: "DOM-1",
          ref_type: "NEW",
          dr_cr: "C",
          amount: "5000.00",
        },
      ],
    });
    for (const [table, lid, label] of [
      ["posting", creditor, "posting (party leg)"],
      ["posting", purchase, "posting (expense leg)"],
      ["bill_allocation", creditor, "bill_allocation"],
    ]) {
      const got = tripleOf(table, vb, lid);
      ok(
        got.split("|").slice(0, 3).every((x) => x === "NULL"),
        `T8/4: CONTROL — a rupee voucher's ${label} has all three NULL`,
        `got ${got}`,
      );
    }
  }

  // =========================================================================
  // 6. THE REFUSALS — in words, naming the missing value
  // =========================================================================
  console.log("\n── two-of-three is refused, and says what is missing ──");
  for (const [label, mutate, wants] of [
    ["no rate", (p) => delete p.rate, "rate"],
    ["no foreign amount", (p) => delete p.foreign_amount, "foreign amount"],
    ["no currency", (p) => delete p.currency_id, "currency"],
  ]) {
    const body = foreignBody(`BAD-${label}`, "2025-06-12", BASE_1, FOREIGN_1, RATE_1);
    delete body.bill_allocations;
    mutate(body.postings[1]);
    const r = await j("POST", `${base}/vouchers/purchase`, { token: OWNER, body });
    ok(r.status === 422, `T8/4: a posting with ${label} is refused (422)`, `status ${r.status} ${r.text}`);
    const msg = (r.body.error ?? r.text ?? "").toLowerCase();
    ok(
      msg.includes(wants),
      `T8/4: and the refusal NAMES the missing value ("${wants}")`,
      `message: ${r.text}`,
    );
    ok(
      !msg.includes("check constraint"),
      "T8/4: and it is a sentence, not a raw constraint name",
      `message: ${r.text}`,
    );
  }

  // =========================================================================
  // 7. THE FREE ORACLE — Q2 says T8 must not move the Balance Sheet
  // =========================================================================
  console.log("\n── the base column is what the reports read ────");
  {
    // The creditor's balance must be the sum of the BASE figures, to the paisa.
    // If any layer had let a foreign amount into `posting.amount`, this is where
    // it would surface — and it is the same column `verify:demo` watches.
    // IMP-1 83,651.23 + IMP-2 45,074.07 + DOM-1 5,000.00 = 1,33,725.30 credit.
    const bal = psql(
      `SELECT (credit - debit)::text FROM ledger_balance
        WHERE company_id = '${CO}' AND ledger_id = '${creditor}'`,
    );
    ok(
      bal === "133725.3000",
      "T8/4: ⛔ ledger_balance sums the BASE column and nothing else (133725.3000)",
      `got ${bal} — a foreign amount that leaked into posting.amount lands here`,
    );
  }

  // =========================================================================
  // 8. THE HASH CHAIN — what the edit log SHOWS must be what it SIGNS
  // =========================================================================
  console.log("\n── the signature covers the rate ───────────────");
  {
    const r = await j("GET", `${base}/vouchers/${v1}/history`, { token: OWNER });
    ok(r.status === 200, "T8/4: the edit log is readable", `status ${r.status}`);
    ok(
      r.body.verification?.ok === true,
      "T8/4: ⛔ the chain VERIFIES over a foreign voucher (the jsonb re-derivation is exact)",
      `reason: ${r.body.verification?.reason}`,
    );
    const signed = psql(
      `SELECT vv.payload::text FROM voucher_head vh
         JOIN voucher_version vv ON vv.id = vh.current_version_id
        WHERE vh.voucher_id = '${v1}'`,
    );
    ok(
      signed.includes("83.6512345678"),
      "T8/4: ⛔ THE RATE IS INSIDE THE SIGNATURE at full scale",
      "a rate outside the hash could be restated in the database and VerifyChain would still report the book intact",
    );
    ok(
      signed.includes("1000.0000") && signed.includes(usd),
      "T8/4: and so are the foreign amount and the currency",
    );
  }

  // ⛔ AND A BASE VOUCHER'S SIGNATURE IS BYTE-IDENTICAL TO WHAT IT ALWAYS WAS.
  // Three omitempty keys is the whole reason every hash written before this
  // commit is still valid; a payload that emitted `"rate": ""` on a rupee
  // voucher would retroactively report every book in the field as tampered
  // with, which is indistinguishable from an actual attack.
  {
    const domPayload = psql(
      `SELECT vv.payload::text FROM voucher_head vh
         JOIN voucher_version vv ON vv.id = vh.current_version_id
        WHERE vh.company_id = '${CO}' AND vh.voucher_id IN (
          SELECT voucher_id FROM voucher_head WHERE company_id = '${CO}')
          AND vv.voucher_no = 'DOM-1'`,
    );
    ok(
      !domPayload.includes("currency_id") &&
        !domPayload.includes('"rate"') &&
        !domPayload.includes("foreign_amount"),
      "T8/4: ⛔ a RUPEE voucher's signed payload names none of the three keys",
      `payload: ${domPayload}`,
    );
  }
} finally {
  // =========================================================================
  // ⛔ REAP, IN A `finally`. §5.14 — a probe that creates must clean up and must
  // ASSERT it worked. NOT an HTTP DELETE: this tenant HAS BOOKS, and
  // posting_immutable RAISES on DELETE, so the rows can only be removed with the
  // triggers disarmed. internal/platform/testdb does exactly this
  // (SET LOCAL session_replication_role = replica) for the same reason, and only
  // a superuser may set it — which is why this connects as postgres.
  //
  // ⚠️ SET LOCAL, so it lasts only to COMMIT. A session-wide SET would leave the
  // shared dev database with immutability disarmed for whatever ran next on that
  // connection — a far worse outcome than a leaked tenant.
  //
  // ⛔ AND `currency_rate` BEFORE `currency`, AND BOTH BEFORE `company`. They are
  // T8's own tables and no earlier probe's reap lists them; a copy of an older
  // reap would fail on currency_company_scope with the tenant half-removed.
  // =========================================================================
  const q = (s) => `'${String(s).replace(/'/g, "''")}'`;
  psql(
    `begin;
     set local session_replication_role = replica;
     delete from bill_allocation      where company_id = ${q(CO)};
     delete from posting              where company_id = ${q(CO)};
     -- ⛔ HEAD BEFORE VERSION. voucher_head.current_version_id is NOT NULL and
     -- points at voucher_version, so nulling it first fails the not-null
     -- constraint (session_replication_role disarms TRIGGERS, not NOT NULL).
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
  ok(left === "0", "T8/4: ⛔ the probe reaped its own tenant, and asserted it worked",
     `companies matching ${CO.slice(0, 8)}…: ${left}`);
}

console.log(`\nT8 step 4 — foreign entry: ${pass}/${pass + fail} passed`);
if (fail > 0) process.exit(1);
