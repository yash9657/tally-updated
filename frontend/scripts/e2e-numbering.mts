/**
 * LIVE e2e harness for Phase-1 voucher NUMBERING (WORKSTREAM B).
 *
 * Not a vitest unit test — runs via tsx against a RUNNING `cored`:
 *
 *   CORED_BASE_URL=http://localhost:8087 pnpm tsx scripts/e2e-numbering.mts
 *
 * It drives the EXACT production REST client (no mock) and asserts the
 * over-HTTP numbering invariants:
 *
 *   - AUTOMATIC allocation: an EMPTY voucher_no on a create yields a non-empty
 *     server number; two creates in the same FY are consecutive (tail n, n+1).
 *   - FORMAT faithfulness: the prefix/suffix are identical across the series.
 *   - NO-REALLOC-ON-ALTER: a PUT with empty voucher_no carries the number
 *     forward unchanged (version_no -> 2), and the Day Book still shows it once.
 *   - GAP-FREE without deletes: the next create continues the sequence.
 *   - FY RESTART: a create in a new financial year restarts the tail at 1.
 *
 * Exit 0 "e2e-numbering OK" on success; on the first mismatch print
 * expected-vs-actual and exit 1. The integrator runs the live pass; this file
 * only has to be runnable + correct (it does NOT start cored).
 */

import { createRestClient } from "../src/api";
import { authHeaders } from "./auth.mjs";

const baseUrl =
  process.env.CORED_BASE_URL ??
  process.env.VITE_CORED_BASE_URL ??
  "http://localhost:8087";

function fail(message: string, expected: unknown, actual: unknown): never {
  console.error(`\nASSERTION FAILED: ${message}`);
  console.error(`  expected: ${JSON.stringify(expected)}`);
  console.error(`  actual:   ${JSON.stringify(actual)}`);
  process.exit(1);
}

function assertEqual(message: string, actual: unknown, expected: unknown): void {
  if (actual !== expected) fail(message, expected, actual);
}

/** Split a display number into its leading prefix, numeric tail, and suffix. */
function splitNumber(no: string): { prefix: string; tail: number; suffix: string } {
  // Numeric tail = the LAST run of digits in the string (the counter); the
  // characters before it are the prefix, after it the suffix.
  const m = no.match(/^(.*?)(\d+)(\D*)$/);
  if (!m) fail("voucher_no contains a numeric counter", "<prefix><digits>", no);
  return { prefix: m![1]!, tail: parseInt(m![2]!, 10), suffix: m![3]! };
}

async function main(): Promise<void> {
  console.log(`e2e-numbering against ${baseUrl}`);
  const client = createRestClient({
    baseUrl,
    headers: await authHeaders(baseUrl),
  });

  // Fresh tenant (FY 2026-27) — seeds the Sales voucher type as AUTOMATIC.
  const companyUUID = crypto.randomUUID();
  await client.createCompany({
    id: companyUUID,
    name: "E2E Numbering Co",
    fy_start_date: "2026-04-01",
    base_currency: "INR",
  });

  const debtor = await client.createLedger(companyUUID, {
    name: "Acme Corp",
    group_name: "Sundry Debtors",
  });
  const sales = await client.createLedger(companyUUID, {
    name: "Domestic Sales",
    group_name: "Sales Accounts",
  });

  function lines(amount: string) {
    return [
      { ledger_id: debtor.ledger_id, dr_cr: "DR" as const, amount },
      { ledger_id: sales.ledger_id, dr_cr: "CR" as const, amount },
    ];
  }

  // #1: EMPTY voucher_no, date in FY 2026-27 -> first allocated number.
  const v1 = await client.postSalesVoucher(companyUUID, {
    party_ledger_id: debtor.ledger_id,
    voucher_date: "2026-04-10",
    party: "Acme Corp",
    postings: lines("400.0000"),
  });
  if (!v1.voucher_no) fail("#1 voucher_no is non-empty", "non-empty", v1.voucher_no);
  const NO1 = v1.voucher_no;
  const p1 = splitNumber(NO1);

  // #2: same type, EMPTY voucher_no, SAME FY -> consecutive tail, same format.
  const v2 = await client.postSalesVoucher(companyUUID, {
    party_ledger_id: debtor.ledger_id,
    voucher_date: "2026-05-01",
    party: "Acme Corp",
    postings: lines("500.0000"),
  });
  const p2 = splitNumber(v2.voucher_no);
  assertEqual("#2 tail === n1+1 (consecutive)", p2.tail, p1.tail + 1);
  assertEqual("#2 same prefix as #1", p2.prefix, p1.prefix);
  assertEqual("#2 same suffix as #1", p2.suffix, p1.suffix);

  // ALTER #1 with EMPTY voucher_no -> number carried forward, version_no === 2.
  const altered = await client.alterSalesVoucher(companyUUID, v1.voucher_id, {
    party_ledger_id: debtor.ledger_id,
    voucher_date: "2026-04-10",
    party: "Acme Corp",
    postings: lines("650.0000"),
  });
  assertEqual("alter preserves voucher_no", altered.voucher_no, NO1);
  assertEqual("alter version_no === 2", altered.version_no, 2);

  // Day Book shows #1 exactly once with NO1 (no realloc, no dupe).
  const dayBook = await client.getDayBook(companyUUID);
  const e1 = dayBook.entries.filter((e) => e.voucher_id === v1.voucher_id);
  assertEqual("day book has one entry for #1", e1.length, 1);
  assertEqual("day book #1 voucher_no === NO1", e1[0]!.voucher_no, NO1);

  // #3: EMPTY voucher_no, same FY -> tail === n1+2 (sequence gap-free; the alter
  // did NOT consume a number).
  const v3 = await client.postSalesVoucher(companyUUID, {
    party_ledger_id: debtor.ledger_id,
    voucher_date: "2026-06-01",
    party: "Acme Corp",
    postings: lines("100.0000"),
  });
  const p3 = splitNumber(v3.voucher_no);
  assertEqual("#3 tail === n1+2 (gap-free continuation)", p3.tail, p1.tail + 2);

  // #4: still FY 2026-27 (date 2027-02-01) -> tail === n1+3.
  const v4 = await client.postSalesVoucher(companyUUID, {
    party_ledger_id: debtor.ledger_id,
    voucher_date: "2027-02-01",
    party: "Acme Corp",
    postings: lines("100.0000"),
  });
  const p4 = splitNumber(v4.voucher_no);
  assertEqual("#4 tail === n1+3 (same FY)", p4.tail, p1.tail + 3);

  // #5: NEW FY 2027-28 (date 2027-05-01) -> counter restarts at 1.
  const v5 = await client.postSalesVoucher(companyUUID, {
    party_ledger_id: debtor.ledger_id,
    voucher_date: "2027-05-01",
    party: "Acme Corp",
    postings: lines("100.0000"),
  });
  const p5 = splitNumber(v5.voucher_no);
  assertEqual("#5 tail === 1 (new FY restarts)", p5.tail, 1);
  assertEqual("#5 same prefix family as #1", p5.prefix, p1.prefix);

  console.log("e2e-numbering OK");
  process.exit(0);
}

main().catch((err) => {
  console.error("\ne2e-numbering ERROR (transport / unexpected):");
  console.error(err);
  process.exit(1);
});
