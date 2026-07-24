/**
 * LIVE e2e harness for the Phase-1 edit-loop (create -> post -> alter -> delete).
 *
 * This is NOT a vitest unit test. It runs via tsx against a RUNNING `cored`:
 *
 *   CORED_BASE_URL=http://localhost:8087 pnpm e2e:edit
 *
 * It exercises the EXACT production client code path — it constructs the real
 * REST client from ../src/api over node's global fetch (no mock, no
 * reimplementation) and asserts the over-HTTP fidelity invariants:
 *
 *   - 400 -> 650 -> 650: ALTER restates (does not accumulate to 1050).
 *   - identity preserved on ALTER: response.voucher_id === original id.
 *   - numbering/date preserved: the alter resends INV-1 / 2025-04-10 and the
 *     Day Book still shows exactly ONE entry with that number/date.
 *   - DELETE drops the voucher: trial balance total_debit -> 0, Day Book empty.
 *   - DELETE is IDEMPOTENT: a retried DELETE returns the SAME tombstone
 *     (voucher_version_id / version_no / voucher_no unchanged), not a new one.
 *   - ALTER of a DELETED voucher is a 404 — never a silent resurrection.
 *
 * Exit 0 with "e2e-edit OK" on success; exit 1 with "expected vs actual" on the
 * first failed assertion. The integrator runs the live pass; this file only has
 * to be runnable + correct (we do NOT start cored here).
 */

import { ApiError, createRestClient } from "../src/api";
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

async function main(): Promise<void> {
  console.log(`e2e-edit against ${baseUrl}`);
  // Real fetch, real client code path — exactly what the app ships.
  const client = createRestClient({
    baseUrl,
    headers: await authHeaders(baseUrl),
  });

  // Fresh tenant per run: isolated + repeatable.
  const companyUUID = crypto.randomUUID();

  // Step 1: create company (seeds 28 groups + Cash/P&L + Sales voucher type).
  const company = await client.createCompany({
    id: companyUUID,
    name: "E2E Edit-Loop Co",
    fy_start_date: "2025-04-01",
    base_currency: "INR",
  });
  assertEqual(
    "createCompany echoes the supplied id",
    company.company_id,
    companyUUID,
  );

  // Step 2: create the two ledgers and capture their ids.
  const debtor = await client.createLedger(companyUUID, {
    name: "Acme Corp",
    group_name: "Sundry Debtors",
  });
  const sales = await client.createLedger(companyUUID, {
    name: "Domestic Sales",
    group_name: "Sales Accounts",
  });
  if (!debtor.ledger_id) fail("debtor ledger_id is non-empty", "non-empty", debtor.ledger_id);
  if (!sales.ledger_id) fail("sales ledger_id is non-empty", "non-empty", sales.ledger_id);

  const VOUCHER_NO = "INV-1";
  const VOUCHER_DATE = "2025-04-10";

  // Step 3: post the sales voucher at 400.
  const posted = await client.postSalesVoucher(companyUUID, {
    party_ledger_id: debtor.ledger_id,
    voucher_no: VOUCHER_NO,
    voucher_date: VOUCHER_DATE,
    party: "Acme Corp",
    postings: [
      { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "400.0000" },
      { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "400.0000" },
    ],
  });
  assertEqual("post version_no === 1", posted.version_no, 1);
  const voucherID = posted.voucher_id;
  if (!voucherID) fail("posted voucher_id is non-empty", "non-empty", voucherID);

  // Step 4: baseline trial balance is 400 and balanced.
  const tbBefore = await client.getTrialBalance(companyUUID);
  assertEqual("baseline trial balance balanced", tbBefore.balanced, true);
  assertEqual("baseline total_debit === 400.0000", tbBefore.total_debit, "400.0000");

  // Step 5: ALTER to 650 as a FULL restatement, RESENDING the same number/date.
  const altered = await client.alterSalesVoucher(companyUUID, voucherID, {
    party_ledger_id: debtor.ledger_id,
    voucher_no: VOUCHER_NO, // unchanged — proves numbering preservation
    voucher_date: VOUCHER_DATE, // unchanged — proves date preservation
    party: "Acme Corp",
    postings: [
      { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "650.0000" },
      { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "650.0000" },
    ],
  });
  assertEqual(
    "alter preserves identity (same voucher_id)",
    altered.voucher_id,
    voucherID,
  );
  assertEqual("alter version_no === 2", altered.version_no, 2);

  // Step 6: trial balance reflects 650, NOT 1050 — the restate-not-accumulate proof.
  const tbAfter = await client.getTrialBalance(companyUUID);
  assertEqual("post-alter trial balance balanced", tbAfter.balanced, true);
  assertEqual(
    "post-alter total_debit === 650.0000 (not 1050.0000)",
    tbAfter.total_debit,
    "650.0000",
  );

  // Step 7: Day Book shows exactly ONE entry for the voucher (no CREATE+ALTER
  // dupes), with the preserved number/date and the new 650 debit.
  const dayBook = await client.getDayBook(companyUUID);
  const entries = dayBook.entries.filter((e) => e.voucher_id === voucherID);
  assertEqual("day book has exactly one entry for the voucher", entries.length, 1);
  const entry = entries[0]!;
  assertEqual("day book voucher_no preserved", entry.voucher_no, VOUCHER_NO);
  assertEqual("day book date preserved", entry.date, VOUCHER_DATE);
  assertEqual("day book debit === 650.0000", entry.debit, "650.0000");

  // Step 8: DELETE drops the voucher from reports + day book (audit log keeps it).
  const deleted = await client.deleteVoucher(companyUUID, voucherID);
  assertEqual("delete op === DELETE", deleted.op, "DELETE");
  assertEqual(
    "delete preserves identity (same voucher_id)",
    deleted.voucher_id,
    voucherID,
  );

  const tbDeleted = await client.getTrialBalance(companyUUID);
  assertEqual(
    "post-delete total_debit === 0.0000",
    tbDeleted.total_debit,
    "0.0000",
  );

  const dayBookDeleted = await client.getDayBook(companyUUID);
  const remaining = dayBookDeleted.entries.filter(
    (e) => e.voucher_id === voucherID,
  );
  assertEqual("day book has zero entries for the deleted voucher", remaining.length, 0);

  // Step 9: DELETE POLICY — re-deleting is an IDEMPOTENT no-op (retry safety for
  // flaky clients / offline replay). The server must echo the EXISTING tombstone
  // rather than stacking a second one, so every identity field is unchanged.
  const reDeleted = await client.deleteVoucher(companyUUID, voucherID);
  assertEqual("re-delete op === DELETE", reDeleted.op, "DELETE");
  assertEqual("re-delete voucher_id unchanged", reDeleted.voucher_id, deleted.voucher_id);
  assertEqual(
    "re-delete echoes the SAME tombstone version id (no new version)",
    reDeleted.voucher_version_id,
    deleted.voucher_version_id,
  );
  assertEqual(
    "re-delete does not bump version_no",
    reDeleted.version_no,
    deleted.version_no,
  );
  assertEqual("re-delete voucher_no unchanged", reDeleted.voucher_no, deleted.voucher_no);

  const tbReDeleted = await client.getTrialBalance(companyUUID);
  assertEqual(
    "post-re-delete total_debit still 0.0000",
    tbReDeleted.total_debit,
    "0.0000",
  );

  // Step 10: DELETE POLICY — ALTERing a deleted voucher is a 404, never a silent
  // resurrection. The read path (GET) already 404s a tombstoned voucher; the
  // write path must agree.
  let alterStatus: number | string = "no error thrown";
  try {
    await client.alterSalesVoucher(companyUUID, voucherID, {
      party_ledger_id: debtor.ledger_id,
      voucher_no: VOUCHER_NO,
      voucher_date: VOUCHER_DATE,
      party: "Acme Corp",
      postings: [
        { ledger_id: debtor.ledger_id, dr_cr: "DR", amount: "9999.0000" },
        { ledger_id: sales.ledger_id, dr_cr: "CR", amount: "9999.0000" },
      ],
    });
  } catch (err) {
    alterStatus = err instanceof ApiError ? err.status : `non-ApiError: ${String(err)}`;
  }
  assertEqual("alter of a DELETED voucher rejects with 404", alterStatus, 404);

  // ...and the refusal changed nothing: the books stay flat (a resurrection
  // would put 9999 back) and the day book stays empty for this voucher.
  const tbAfterRefusedAlter = await client.getTrialBalance(companyUUID);
  assertEqual(
    "refused alter must not resurrect the voucher (total_debit still 0.0000)",
    tbAfterRefusedAlter.total_debit,
    "0.0000",
  );
  const dayBookAfterRefusedAlter = await client.getDayBook(companyUUID);
  assertEqual(
    "day book still has zero entries after the refused alter",
    dayBookAfterRefusedAlter.entries.filter((e) => e.voucher_id === voucherID).length,
    0,
  );

  console.log("e2e-edit OK");
  process.exit(0);
}

main().catch((err) => {
  console.error("\ne2e-edit ERROR (transport / unexpected):");
  console.error(err);
  process.exit(1);
});
