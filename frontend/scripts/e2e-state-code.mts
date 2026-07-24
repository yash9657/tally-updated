/**
 * LIVE e2e harness for the company HOME-STATE contract (WS8a).
 *
 * Not a vitest unit test — runs via tsx against a RUNNING `cored`:
 *
 *   CORED_BASE_URL=http://localhost:8087 pnpm exec tsx scripts/e2e-state-code.mts
 *
 * It drives the EXACT production REST client (no mock) and proves, over real
 * HTTP against real Postgres, that `company.state_code` is the single source of
 * truth for the supplier (home) state in the GST split:
 *
 *   1. CREATE a company with state_code "27" (Maharashtra).
 *   2. READ it back with GET /v1/companies/{id} — state_code round-trips.
 *      (This step needs the route + its routeAction mapping to be wired; before
 *      the wiring stage it 403s, which is the intended fail-closed signal.)
 *   3. POST an intra-27 sale that OMITS supplier_state_code entirely, so the
 *      server's company.state_code default is the only thing that can classify
 *      it, and assert the SERVER-returned split is CGST+SGST (900/900), not
 *      IGST. The oracle is VoucherResponse.cgst/sgst/igst — the server's own
 *      derivation — never the client's deriveGstSplit helper (that would be
 *      circular: the client helper is a mirror of the same rules).
 *   4. CONTROL: the same sale from a state-29 company to place-of-supply 27 must
 *      come back IGST 1800 / CGST 0 / SGST 0. Without this, step 3 would pass
 *      even if the server ignored state entirely and always derived intra.
 *
 * Exit 0 "e2e-state-code OK" on success; on the first mismatch print
 * expected-vs-actual and exit 1. It does NOT start cored.
 */

import { createRestClient } from "../src/api";
import type { CoredClient } from "../src/api";
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
  console.log(`  ok: ${message}`);
}

/** Create the masters a GST sale needs and return their ids. */
async function seedMasters(
  client: CoredClient,
  companyID: string,
  partyStateCode: string,
) {
  const debtor = await client.createLedger(companyID, {
    name: "Acme Corp",
    group_name: "Sundry Debtors",
    state_code: partyStateCode,
  });
  const sales = await client.createLedger(companyID, {
    name: "Domestic Sales",
    group_name: "Sales Accounts",
  });
  const cgst = await client.createLedger(companyID, {
    name: "Output CGST",
    group_name: "Duties & Taxes",
  });
  const sgst = await client.createLedger(companyID, {
    name: "Output SGST",
    group_name: "Duties & Taxes",
  });
  const igst = await client.createLedger(companyID, {
    name: "Output IGST",
    group_name: "Duties & Taxes",
  });
  return {
    debtorID: debtor.ledger_id,
    salesID: sales.ledger_id,
    cgstID: cgst.ledger_id,
    sgstID: sgst.ledger_id,
    igstID: igst.ledger_id,
  };
}

async function main(): Promise<void> {
  console.log(`e2e-state-code against ${baseUrl}`);
  const client = createRestClient({
    baseUrl,
    headers: await authHeaders(baseUrl),
  });

  // -----------------------------------------------------------------------
  // 1. Create a Maharashtra (27) company.
  // -----------------------------------------------------------------------
  const mhID = crypto.randomUUID();
  await client.createCompany({
    id: mhID,
    name: "E2E Maharashtra Traders",
    fy_start_date: "2026-04-01",
    base_currency: "INR",
    state_code: "27",
    gstin: "27AABCT1234M1Z5",
  });
  console.log("  created company (state_code 27)");

  // -----------------------------------------------------------------------
  // 2. Read the master back (GET /v1/companies/{id}).
  // -----------------------------------------------------------------------
  const view = await client.getCompany(mhID).catch((err: unknown) => {
    console.error(
      "\nGET /v1/companies/{id} failed. If this is a 403, the route or its " +
        "routeAction mapping is not wired yet (the two-touch rule).",
    );
    throw err;
  });
  assertEqual("getCompany id round-trips", view.id, mhID);
  assertEqual("getCompany state_code round-trips", view.state_code, "27");
  assertEqual("getCompany gstin round-trips", view.gstin, "27AABCT1234M1Z5");
  assertEqual("getCompany fy_start_date round-trips", view.fy_start_date, "2026-04-01");
  assertEqual("getCompany base_currency round-trips", view.base_currency, "INR");
  assertEqual("getCompany name round-trips", view.name, "E2E Maharashtra Traders");

  // -----------------------------------------------------------------------
  // 3. Intra-27 sale with supplier_state_code OMITTED -> server derives
  //    CGST+SGST from company.state_code.
  // -----------------------------------------------------------------------
  const mh = await seedMasters(client, mhID, "27");
  const intra = await client.postSalesVoucher(mhID, {
    party_ledger_id: mh.debtorID,
    voucher_date: "2026-05-10",
    party: "Acme Corp",
    postings: [
      { ledger_id: mh.debtorID, dr_cr: "DR", amount: "11800.0000" },
      { ledger_id: mh.salesID, dr_cr: "CR", amount: "10000.0000" },
      { ledger_id: mh.cgstID, dr_cr: "CR", amount: "900.0000" },
      { ledger_id: mh.sgstID, dr_cr: "CR", amount: "900.0000" },
    ],
    gst: {
      // supplier_state_code deliberately ABSENT: company.state_code must fill it.
      place_of_supply_state_code: "27",
      lines: [{ taxable_value: "10000.0000", gst_rate_pct: "18" }],
      cgst_ledger_id: mh.cgstID,
      sgst_ledger_id: mh.sgstID,
      igst_ledger_id: mh.igstID,
    },
  });
  assertEqual("intra-27 server-derived CGST", intra.cgst, "900.0000");
  assertEqual("intra-27 server-derived SGST", intra.sgst, "900.0000");
  assertEqual("intra-27 server-derived IGST", intra.igst, "0.0000");

  // The same sale WITH the supplier state sent explicitly must be identical —
  // the hydrated client path and the server-default path agree.
  const intraExplicit = await client.postSalesVoucher(mhID, {
    party_ledger_id: mh.debtorID,
    voucher_date: "2026-05-11",
    party: "Acme Corp",
    postings: [
      { ledger_id: mh.debtorID, dr_cr: "DR", amount: "11800.0000" },
      { ledger_id: mh.salesID, dr_cr: "CR", amount: "10000.0000" },
      { ledger_id: mh.cgstID, dr_cr: "CR", amount: "900.0000" },
      { ledger_id: mh.sgstID, dr_cr: "CR", amount: "900.0000" },
    ],
    gst: {
      supplier_state_code: view.state_code,
      place_of_supply_state_code: "27",
      lines: [{ taxable_value: "10000.0000", gst_rate_pct: "18" }],
      cgst_ledger_id: mh.cgstID,
      sgst_ledger_id: mh.sgstID,
      igst_ledger_id: mh.igstID,
    },
  });
  assertEqual("explicit-27 CGST matches the defaulted post", intraExplicit.cgst, intra.cgst);
  assertEqual("explicit-27 SGST matches the defaulted post", intraExplicit.sgst, intra.sgst);
  assertEqual("explicit-27 IGST matches the defaulted post", intraExplicit.igst, intra.igst);

  // -----------------------------------------------------------------------
  // 4. CONTROL: a Karnataka (29) company selling into 27 is INTER-state.
  // -----------------------------------------------------------------------
  const kaID = crypto.randomUUID();
  await client.createCompany({
    id: kaID,
    name: "E2E Karnataka Traders",
    fy_start_date: "2026-04-01",
    base_currency: "INR",
    state_code: "29",
  });
  const ka = await seedMasters(client, kaID, "27");
  const inter = await client.postSalesVoucher(kaID, {
    party_ledger_id: ka.debtorID,
    voucher_date: "2026-05-10",
    party: "Acme Corp",
    postings: [
      { ledger_id: ka.debtorID, dr_cr: "DR", amount: "11800.0000" },
      { ledger_id: ka.salesID, dr_cr: "CR", amount: "10000.0000" },
      { ledger_id: ka.igstID, dr_cr: "CR", amount: "1800.0000" },
    ],
    gst: {
      // Again omitted — the 29 company master must classify this as inter-state.
      place_of_supply_state_code: "27",
      lines: [{ taxable_value: "10000.0000", gst_rate_pct: "18" }],
      cgst_ledger_id: ka.cgstID,
      sgst_ledger_id: ka.sgstID,
      igst_ledger_id: ka.igstID,
    },
  });
  assertEqual("control inter-state IGST", inter.igst, "1800.0000");
  assertEqual("control inter-state CGST", inter.cgst, "0.0000");
  assertEqual("control inter-state SGST", inter.sgst, "0.0000");

  console.log("e2e-state-code OK");
  process.exit(0);
}

main().catch((err) => {
  console.error("\ne2e-state-code ERROR (transport / unexpected):");
  console.error(err);
  process.exit(1);
});
