/**
 * LIVE e2e harness for Task A — the OCR killer-feature loop (insightd draft ->
 * frontend F9 prefill -> Ctrl+A -> cored -> trial balance).
 *
 * Not a vitest unit test — runs via tsx against a RUNNING `cored` (:8087) AND a
 * RUNNING `insightd` (:8088):
 *
 *   CORED_BASE_URL=http://localhost:8087 INSIGHTD_BASE_URL=http://localhost:8088 \
 *     pnpm tsx scripts/e2e-ocr-prefill.mts
 *
 * It drives the EXACT production clients (createRestClient + createInsightdClient)
 * and the EXACT draft-accept path the F9 screen uses (acceptPurchaseDraft), proving
 * the whole loop over HTTP:
 *
 *   1. A company + its real ledgers are created in cored.
 *   2. A corpus invoice fixture is remapped so insightd's tenant config carries the
 *      REAL cored ledger uuids, then POSTed to insightd, which returns a `ready`
 *      PurchaseVoucherDraft whose body.party_ledger_id IS the real cored uuid.
 *   3. HITL invariant: the trial balance is all-zero BEFORE accept (insightd never
 *      wrote to the ledger — there is no insightd write path).
 *   4. getDraft round-trips the draft through the new insightd client.
 *   5. acceptPurchaseDraft posts the body verbatim via the EXISTING purchase API;
 *      cored re-derives GST (CGST 90 + SGST 90 on taxable 1000, intra @18) and the
 *      trial balance now reflects the purchase, balanced to the rupee.
 *
 * Exit 0 "e2e-ocr-prefill OK" on success; on the first mismatch print
 * expected-vs-actual and exit 1. The integrator runs the live pass; this file only
 * has to be runnable + correct (it does NOT start the servers).
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { createRestClient, createInsightdClient, acceptPurchaseDraft } from "../src/api";

const coredBase =
  process.env.CORED_BASE_URL ?? process.env.VITE_CORED_BASE_URL ?? "http://localhost:8087";
const insightdBase =
  process.env.INSIGHTD_BASE_URL ?? process.env.VITE_INSIGHTD_BASE_URL ?? "http://localhost:8088";

const here = dirname(fileURLToPath(import.meta.url));
const FIXTURE = resolve(
  here,
  "../../insightd/insightd/benchmark/corpus/intra-state-18-1line-clean.json",
);

function fail(message: string, expected: unknown, actual: unknown): never {
  console.error(`\nASSERTION FAILED: ${message}`);
  console.error(`  expected: ${JSON.stringify(expected)}`);
  console.error(`  actual:   ${JSON.stringify(actual)}`);
  process.exit(1);
}
function assertEqual(message: string, actual: unknown, expected: unknown): void {
  if (actual !== expected) fail(message, expected, actual);
}
function assertTrue(message: string, cond: boolean): void {
  if (!cond) fail(message, true, false);
}

async function main(): Promise<void> {
  console.log(`e2e-ocr-prefill: cored=${coredBase} insightd=${insightdBase}`);
  const client = createRestClient({ baseUrl: coredBase });
  const insightd = createInsightdClient({ baseUrl: insightdBase });

  // 1. Fresh tenant + its real ledgers (intra-state supplier in state 27).
  const cid = crypto.randomUUID();
  await client.createCompany({
    id: cid,
    name: "E2E OCR Co",
    fy_start_date: "2026-04-01",
    base_currency: "INR",
  });
  const led = async (name: string, group: string, extra: Record<string, string> = {}) =>
    (await client.createLedger(cid, { name, group_name: group, ...extra })).ledger_id;
  const party = await led("Acme Components Pvt Ltd", "Sundry Creditors", { state_code: "27" });
  const purchase = await led("Purchase - Raw Material", "Purchase Accounts");
  const cgst = await led("Input CGST", "Duties & Taxes");
  const sgst = await led("Input SGST", "Duties & Taxes");
  const igst = await led("Input IGST", "Duties & Taxes");

  // 2. Remap the corpus fixture's synthetic ids -> real cored uuids so the draft
  //    body references real ledgers (mirrors a real deployment's tenant config).
  const remap: Record<string, string> = {
    "CO-DEMO": cid,
    "L-PARTY-1": party,
    "L-PUR-RM": purchase,
    "L-PUR-CONS": purchase,
    "L-PUR-SVC": purchase,
    "L-CGST": cgst,
    "L-SGST": sgst,
    "L-IGST": igst,
  };
  let txt = readFileSync(FIXTURE, "utf8");
  for (const [k, v] of Object.entries(remap)) txt = txt.split(k).join(v);
  const fixture = JSON.parse(txt) as Record<string, unknown>;
  const tenant = (fixture.input as Record<string, unknown>).tenant as Record<string, unknown>;

  // 3. insightd returns a draft; insightd NEVER writes to cored.
  const draft = await insightd.createDraft(cid, { source: "upload", tenant, fixture });
  assertEqual("draft status is ready", draft.status, "ready");
  assertEqual("draft.body.party_ledger_id is the REAL cored ledger id", draft.body.party_ledger_id, party);
  assertTrue("derived_gst preview present", draft.derived_gst != null);
  assertEqual("derived CGST preview = 90.0000", draft.derived_gst?.cgst, "90.0000");
  assertTrue("redaction proves PII scrubbed (>0)", draft.redaction.total > 0);

  // getDraft round-trips through the new insightd client.
  const fetched = await insightd.getDraft(cid, draft.draft_id);
  assertEqual("getDraft returns the same draft_id", fetched.draft_id, draft.draft_id);

  // 4. HITL: every ledger balance is ZERO before accept (insightd wrote nothing).
  const tb0 = await client.getTrialBalance(cid);
  const nonZero0 = tb0.rows.filter(
    (r) => Number.parseFloat(r.debit) !== 0 || Number.parseFloat(r.credit) !== 0,
  );
  assertEqual("HITL: no nonzero balances before accept", nonZero0.length, 0);

  // 5. Ctrl+A — the EXISTING purchase API submits the body; cored re-derives GST.
  const res = await acceptPurchaseDraft(client, cid, draft.body);
  assertTrue("accepted voucher_no non-empty", !!res.voucher_no);
  assertEqual("server-derived CGST = 90.0000", res.cgst, "90.0000");
  assertEqual("server-derived SGST = 90.0000", res.sgst, "90.0000");
  assertEqual("server-derived IGST = 0.0000", res.igst, "0.0000");

  // 6. Trial balance now reflects the purchase, balanced to the rupee.
  const tb = await client.getTrialBalance(cid);
  assertTrue("trial balance is balanced", tb.balanced);
  const bal = (ledger: string) => tb.rows.find((r) => r.ledger === ledger);
  assertEqual("Purchase debited 1000", bal("Purchase - Raw Material")?.debit, "1000.0000");
  assertEqual("Input CGST debited 90", bal("Input CGST")?.debit, "90.0000");
  assertEqual("Input SGST debited 90", bal("Input SGST")?.debit, "90.0000");
  assertEqual("Party credited 1180", bal("Acme Components Pvt Ltd")?.credit, "1180.0000");

  console.log("e2e-ocr-prefill OK");
  process.exit(0);
}

main().catch((err) => {
  console.error("\ne2e-ocr-prefill ERROR (transport / unexpected):");
  console.error(err);
  process.exit(1);
});
