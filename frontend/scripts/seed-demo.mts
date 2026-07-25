/**
 * SEED A DEMO COMPANY — one company whose books exercise every feature the UI
 * can show, so the product can be walked end to end without hunting for data.
 *
 *   cd frontend && pnpm seed:demo            # against a running cored (:8087)
 *   cd frontend && pnpm seed:demo --insightd # also queue a Bill Inbox draft
 *
 * It prints a URL that opens the UI ON THIS COMPANY (`?company=<id>`, which the
 * shell persists), plus a summary of what was posted and which keystroke shows it.
 *
 * WHAT IT BUILDS (and why each row is there):
 *   - Masters: two customers (one Karnataka, one Maharashtra) and two suppliers
 *     (ditto), sales/purchase/expense ledgers, a bank, and the six GST tax
 *     ledgers — so BOTH the intra-state (CGST+SGST) and inter-state (IGST)
 *     derivations appear in the same books.
 *   - Opening capital, so the Balance Sheet has an equity side.
 *   - Sales + purchases across April–July, so the F2 period picker, the Day Book
 *     range and the cash-flow buckets all have something to move.
 *   - BILL-WISE references on every party voucher and a PART payment against
 *     one, so Outstandings shows both a fully open bill and a partly-settled one
 *     (which is what makes the ageing slabs non-trivial).
 *   - One ALTERED voucher (version 2) and one DELETED voucher (a tombstone), so
 *     the append-only edit trail is visible in the data even though this build
 *     has no Edit Log viewer yet.
 *   - Cash/bank contra + an expense journal, so all six voucher types (F4–F9)
 *     have real entries behind them.
 *
 * Money is fixed-4 decimal STRINGS end to end. Every voucher is posted through
 * the same REST client the UI uses — nothing is written to the database directly,
 * so the seed inherits every server-side invariant (balanced postings, the GST
 * re-derivation, server numbering).
 */

import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { createRestClient, createInsightdClient } from "../src/api";
import type { CoredClient, VoucherRequest } from "../src/api";

const here = dirname(fileURLToPath(import.meta.url));
/** The same deterministic bill fixture the OCR e2e uses (a clean intra-state 18%). */
const OCR_FIXTURE = resolve(
  here,
  "../../insightd/insightd/benchmark/corpus/intra-state-18-1line-clean.json",
);

const baseUrl =
  process.env.CORED_BASE_URL ??
  process.env.VITE_CORED_BASE_URL ??
  "http://localhost:8087";
const insightdUrl = process.env.INSIGHTD_BASE_URL ?? "http://localhost:8088";
const uiUrl = process.env.UI_BASE_URL ?? "http://localhost:5173";
const withInsightd = process.argv.includes("--insightd");

/** Karnataka = the company's own state; Maharashtra = the inter-state case. */
const HOME_STATE = "29";
const OTHER_STATE = "27";

/**
 * The seed MUST run as the same subject the dev browser authenticates as
 * (`src/App.tsx` devAuthedFetch), because company membership is per-subject:
 * seeded under any other subject, the browser's user is not a member, cored
 * answers 403, and the shell falls back to minting its own empty company — so
 * the demo data would appear to "not work". Override with SEED_SUBJECT when
 * pointing the UI at a different identity.
 */
const SEED_SUBJECT = process.env.SEED_SUBJECT ?? "browser-dev-owner";

const money = (n: number): string => n.toFixed(4);

/** Mint an Owner token for {@link SEED_SUBJECT} (amr webauthn satisfies step-up). */
async function seedAuthHeaders(): Promise<Record<string, string>> {
  const res = await fetch(`${baseUrl.replace(/\/$/, "")}/auth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject: SEED_SUBJECT, amr: ["webauthn"] }),
  });
  if (!res.ok) {
    throw new Error(
      `mint token for "${SEED_SUBJECT}" failed: ${res.status} ${await res.text()}` +
        " (is cored running with AUTH_DEV_TOKEN_ENABLED=true?)",
    );
  }
  const body = (await res.json()) as { token?: string };
  if (!body.token) throw new Error("mint token: no token in response");
  return { Authorization: `Bearer ${body.token}` };
}

async function main(): Promise<void> {
  const headers = await seedAuthHeaders();
  const client: CoredClient = createRestClient({ baseUrl, headers });

  const stamp = new Date().toISOString().slice(0, 10);
  const company = await client.createCompany({
    name: "Demo Traders",
    fy_start_date: "2026-04-01",
    base_currency: "INR",
    state_code: HOME_STATE,
  });
  const co = company.company_id;
  console.log(`\nDemo Traders  (created ${stamp})\n  company id: ${co}`);

  // ---- Masters -------------------------------------------------------------
  const ledger = async (name: string, group: string, stateCode?: string) => {
    const res = await client.createLedger(co, {
      name,
      group_name: group,
      ...(stateCode ? { state_code: stateCode } : {}),
    });
    return res.ledger_id;
  };

  const acme = await ledger("Acme Traders", "Sundry Debtors", HOME_STATE);
  const mumbai = await ledger("Mumbai Metals", "Sundry Debtors", OTHER_STATE);
  const steel = await ledger("Steel Suppliers", "Sundry Creditors", HOME_STATE);
  const delhi = await ledger("Delhi Parts", "Sundry Creditors", OTHER_STATE);

  const sales = await ledger("Domestic Sales", "Sales Accounts");
  const purchases = await ledger("Raw Material Purchases", "Purchase Accounts");
  const rent = await ledger("Rent", "Indirect Expenses");
  const power = await ledger("Electricity", "Indirect Expenses");
  const capital = await ledger("Capital Account", "Capital Account");
  const bank = await ledger("HDFC Bank", "Bank Accounts");
  const cash = await ledger("Cash A/c", "Cash-in-Hand");

  const outCgst = await ledger("Output CGST", "Duties & Taxes");
  const outSgst = await ledger("Output SGST", "Duties & Taxes");
  const outIgst = await ledger("Output IGST", "Duties & Taxes");
  const inCgst = await ledger("Input CGST", "Duties & Taxes");
  const inSgst = await ledger("Input SGST", "Duties & Taxes");
  const inIgst = await ledger("Input IGST", "Duties & Taxes");

  console.log("  masters:    4 parties, 6 tax ledgers, sales/purchase/expense, bank + cash");

  // ---- Helpers -------------------------------------------------------------
  /** A GST sales invoice with a bill-wise NEW reference (so it ages). */
  const salesInvoice = async (opts: {
    date: string;
    party: string;
    partyId: string;
    partyState: string;
    taxable: number;
    ratePct: number;
    ref: string;
    dueDate: string;
  }) => {
    const inter = opts.partyState !== HOME_STATE;
    const tax = (opts.taxable * opts.ratePct) / 100;
    const total = opts.taxable + tax;
    const body: VoucherRequest = {
      voucher_date: opts.date,
      party: opts.party,
      party_ledger_id: opts.partyId,
      narration: `${opts.ref} — ${opts.party}`,
      postings: [
        { ledger_id: opts.partyId, dr_cr: "D", amount: money(total) },
        { ledger_id: sales, dr_cr: "C", amount: money(opts.taxable) },
        ...(inter
          ? [{ ledger_id: outIgst, dr_cr: "C" as const, amount: money(tax) }]
          : [
              { ledger_id: outCgst, dr_cr: "C" as const, amount: money(tax / 2) },
              { ledger_id: outSgst, dr_cr: "C" as const, amount: money(tax / 2) },
            ]),
      ],
      bill_allocations: [
        {
          ledger_id: opts.partyId,
          ref_name: opts.ref,
          ref_type: "NEW",
          dr_cr: "D",
          amount: money(total),
          due_date: opts.dueDate,
        },
      ],
      gst: {
        supplier_state_code: HOME_STATE,
        place_of_supply_state_code: opts.partyState,
        lines: [{ taxable_value: money(opts.taxable), gst_rate_pct: String(opts.ratePct) }],
        cgst_ledger_id: outCgst,
        sgst_ledger_id: outSgst,
        igst_ledger_id: outIgst,
      },
    };
    return client.postSalesVoucher(co, body);
  };

  /** A GST purchase bill with a bill-wise NEW reference on the supplier. */
  const purchaseBill = async (opts: {
    date: string;
    party: string;
    partyId: string;
    partyState: string;
    taxable: number;
    ratePct: number;
    ref: string;
    dueDate: string;
  }) => {
    const inter = opts.partyState !== HOME_STATE;
    const tax = (opts.taxable * opts.ratePct) / 100;
    const total = opts.taxable + tax;
    const body: VoucherRequest = {
      voucher_date: opts.date,
      party: opts.party,
      party_ledger_id: opts.partyId,
      narration: `${opts.ref} — ${opts.party}`,
      postings: [
        { ledger_id: purchases, dr_cr: "D", amount: money(opts.taxable) },
        ...(inter
          ? [{ ledger_id: inIgst, dr_cr: "D" as const, amount: money(tax) }]
          : [
              { ledger_id: inCgst, dr_cr: "D" as const, amount: money(tax / 2) },
              { ledger_id: inSgst, dr_cr: "D" as const, amount: money(tax / 2) },
            ]),
        { ledger_id: opts.partyId, dr_cr: "C", amount: money(total) },
      ],
      bill_allocations: [
        {
          ledger_id: opts.partyId,
          ref_name: opts.ref,
          ref_type: "NEW",
          dr_cr: "C",
          amount: money(total),
          due_date: opts.dueDate,
        },
      ],
    };
    return client.postPurchaseVoucher(co, body);
  };

  // ---- The books -----------------------------------------------------------
  // 1. Opening capital (F7 Journal) — gives the Balance Sheet an equity side.
  await client.postJournalVoucher(co, {
    voucher_date: "2026-04-01",
    narration: "Opening capital introduced",
    postings: [
      { ledger_id: cash, dr_cr: "D", amount: money(500000) },
      { ledger_id: capital, dr_cr: "C", amount: money(500000) },
    ],
  });

  // 2. F4 Contra — cash into the bank.
  await client.postContraVoucher(co, {
    voucher_date: "2026-04-02",
    narration: "Cash deposited to HDFC",
    postings: [
      { ledger_id: bank, dr_cr: "D", amount: money(300000) },
      { ledger_id: cash, dr_cr: "C", amount: money(300000) },
    ],
  });

  // 3. F8 Sales — intra-state (CGST+SGST) and inter-state (IGST), both aged.
  const inv1 = await salesInvoice({
    date: "2026-04-12", party: "Acme Traders", partyId: acme, partyState: HOME_STATE,
    taxable: 50000, ratePct: 18, ref: "INV-001", dueDate: "2026-05-12",
  });
  const inv2 = await salesInvoice({
    date: "2026-05-08", party: "Mumbai Metals", partyId: mumbai, partyState: OTHER_STATE,
    taxable: 80000, ratePct: 18, ref: "INV-002", dueDate: "2026-06-22",
  });
  const inv3 = await salesInvoice({
    date: "2026-06-19", party: "Acme Traders", partyId: acme, partyState: HOME_STATE,
    taxable: 24000, ratePct: 12, ref: "INV-003", dueDate: "2026-07-19",
  });

  // 4. F9 Purchases — intra and inter-state input tax.
  const bill1 = await purchaseBill({
    date: "2026-04-20", party: "Steel Suppliers", partyId: steel, partyState: HOME_STATE,
    taxable: 30000, ratePct: 18, ref: "SUP-101", dueDate: "2026-05-20",
  });
  await purchaseBill({
    date: "2026-05-15", party: "Delhi Parts", partyId: delhi, partyState: "07",
    taxable: 20000, ratePct: 18, ref: "SUP-102", dueDate: "2026-06-29",
  });

  // 5. F6 Receipt — a PART payment against INV-001, so one bill stays partly
  //    open (the interesting Outstandings case) while INV-002/3 are fully open.
  await client.postReceiptVoucher(co, {
    voucher_date: "2026-05-30",
    party: "Acme Traders",
    party_ledger_id: acme,
    narration: "Part payment against INV-001",
    postings: [
      { ledger_id: bank, dr_cr: "D", amount: money(35000) },
      { ledger_id: acme, dr_cr: "C", amount: money(35000) },
    ],
    bill_allocations: [
      { ledger_id: acme, ref_name: "INV-001", ref_type: "AGAINST", dr_cr: "C", amount: money(35000) },
    ],
  });

  // 6. F5 Payments — settle a supplier bill, and pay the rent.
  await client.postPaymentVoucher(co, {
    voucher_date: "2026-06-05",
    party: "Steel Suppliers",
    party_ledger_id: steel,
    narration: "Payment against SUP-101",
    postings: [
      { ledger_id: steel, dr_cr: "D", amount: money(20000) },
      { ledger_id: bank, dr_cr: "C", amount: money(20000) },
    ],
    bill_allocations: [
      { ledger_id: steel, ref_name: "SUP-101", ref_type: "AGAINST", dr_cr: "D", amount: money(20000) },
    ],
  });
  await client.postPaymentVoucher(co, {
    voucher_date: "2026-06-30",
    narration: "Office rent — Q1",
    postings: [
      { ledger_id: rent, dr_cr: "D", amount: money(45000) },
      { ledger_id: bank, dr_cr: "C", amount: money(45000) },
    ],
  });

  // 7. F7 Journal — an accrued expense (no cash movement).
  await client.postJournalVoucher(co, {
    voucher_date: "2026-07-01",
    narration: "Electricity accrued for June",
    postings: [
      { ledger_id: power, dr_cr: "D", amount: money(8600) },
      { ledger_id: cash, dr_cr: "C", amount: money(8600) },
    ],
  });

  // 8. An ALTERED voucher: INV-003 is corrected from 24,000 to 26,500 @12%.
  //    This appends VERSION 2 — the original is retained, never overwritten.
  const inv3Tax = (26500 * 12) / 100;
  await client.alterSalesVoucher(co, inv3.voucher_id, {
    voucher_date: "2026-06-19",
    party: "Acme Traders",
    party_ledger_id: acme,
    narration: "INV-003 — corrected quantity",
    postings: [
      { ledger_id: acme, dr_cr: "D", amount: money(26500 + inv3Tax) },
      { ledger_id: sales, dr_cr: "C", amount: money(26500) },
      { ledger_id: outCgst, dr_cr: "C", amount: money(inv3Tax / 2) },
      { ledger_id: outSgst, dr_cr: "C", amount: money(inv3Tax / 2) },
    ],
    gst: {
      supplier_state_code: HOME_STATE,
      place_of_supply_state_code: HOME_STATE,
      lines: [{ taxable_value: money(26500), gst_rate_pct: "12" }],
      cgst_ledger_id: outCgst,
      sgst_ledger_id: outSgst,
      igst_ledger_id: outIgst,
    },
  });

  // 9. A DELETED voucher: posted, then tombstoned. It vanishes from the reports
  //    but its versions remain in the ledger (the audit trail is append-only).
  const mistake = await client.postJournalVoucher(co, {
    voucher_date: "2026-07-03",
    narration: "Duplicate entry — to be deleted",
    postings: [
      { ledger_id: power, dr_cr: "D", amount: money(1200) },
      { ledger_id: cash, dr_cr: "C", amount: money(1200) },
    ],
  });
  await client.deleteVoucher(co, mistake.voucher_id);

  console.log("  vouchers:   11 live across Apr–Jul 2026 (all six types), 1 altered (v2), 1 deleted");

  // ---- Optional: a pending OCR draft for the Bill Inbox --------------------
  // Uses the SAME benchmark fixture the OCR e2e drives, with its placeholder
  // ledger ids rewritten to this company's real ones, so the draft that lands in
  // the inbox resolves against these books and can actually be accepted.
  if (withInsightd) {
    try {
      const insightd = createInsightdClient({ baseUrl: insightdUrl });
      const remap: Record<string, string> = {
        "CO-DEMO": co,
        "L-PARTY-1": steel,
        "L-PUR-RM": purchases,
        "L-PUR-CONS": purchases,
        "L-PUR-SVC": purchases,
        "L-CGST": inCgst,
        "L-SGST": inSgst,
        "L-IGST": inIgst,
      };
      let txt = readFileSync(OCR_FIXTURE, "utf8");
      for (const [k, v] of Object.entries(remap)) txt = txt.split(k).join(v);
      const fixture = JSON.parse(txt) as Record<string, unknown>;
      const tenant = (fixture.input as Record<string, unknown>).tenant;
      const draft = await insightd.createDraft(co, {
        source: "upload",
        tenant: tenant as never,
        fixture,
      });
      console.log(
        `  bill inbox: 1 pending OCR draft (${draft.status}, ${draft.redaction.total} PII fields redacted) — Gateway → Bill Inbox`,
      );
    } catch (err) {
      console.log(
        `  bill inbox: SKIPPED — ${err instanceof Error ? err.message : String(err)} (insightd at ${insightdUrl})`,
      );
    }
  }

  // ---- Report the state so the console output is itself the proof ----------
  const tb = await client.getTrialBalance(co);
  const bs = await client.getBalanceSheet(co);
  const os = await client.getOutstandings(co, { kind: "RECEIVABLE" });
  console.log(
    `\n  trial balance: Dr ${tb.total_debit} / Cr ${tb.total_credit}  balanced=${tb.balanced}` +
      `\n  balance sheet: assets ${bs.assets.total} / liabilities ${bs.liabilities.total}  balanced=${bs.balanced}` +
      `\n  receivables:   ${os.parties.length} parties, total ${os.total}`,
  );
  // Disclosed on purpose: the INV-003 alteration does not restate its bill-wise
  // reference, so Outstandings still ages the ORIGINAL 26,880 rather than the
  // corrected 29,680. That is the documented over-count on altered vouchers
  // (HANDOFF §6) shown honestly in the demo data rather than papered over.
  console.log(
    "  note:          INV-003 was altered (v2) without restating its bill ref —\n" +
      "                 Outstandings therefore ages the original amount (known gap).",
  );

  console.log(`\n  OPEN IT:  ${uiUrl}/?company=${co}\n`);
  console.log("  Then try:  F8/F9 post · Day Book → Enter to drill · Alt+G Trial Balance");
  console.log("             F5 group-wise · F12 configure · Ctrl+F filter · Alt+E export");
  console.log("             F1 help · F11 features · Ctrl+N calculator · Esc to go back\n");
}

main().catch((err: unknown) => {
  console.error("seed-demo FAILED:", err instanceof Error ? err.message : err);
  process.exit(1);
});
