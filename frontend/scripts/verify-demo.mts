/**
 * VERIFY THE DEMO COMPANY — every Step-6 invariant, checked at once.
 *
 *   cd frontend && pnpm verify:demo <companyID>
 *
 * This is the gate for the remediation pass: it re-derives each claim from the
 * LIVE stack (cored + insightd + Postgres) rather than trusting the seed's own
 * console output, and exits non-zero on the first broken invariant. Several of
 * these checks are cross-report: two independent code paths over the same books
 * that must agree to the paisa. Those are the ones that caught real bugs.
 */

import { createRestClient, createInsightdClient } from "../src/api";
import type { CoredClient } from "../src/api";

const baseUrl = process.env.CORED_BASE_URL ?? "http://localhost:8087";
const insightdUrl = process.env.INSIGHTD_BASE_URL ?? "http://localhost:8088";
const SEED_SUBJECT = process.env.SEED_SUBJECT ?? "browser-dev-owner";
const companyID: string = (() => {
  const id = process.argv[2];
  if (!id) {
    console.error("usage: pnpm verify:demo <companyID>");
    process.exit(2);
  }
  return id;
})();

const FY = { from: "2026-04-01", to: "2027-03-31" };

let failures = 0;
let checks = 0;

function check(name: string, ok: boolean, detail: string): void {
  checks += 1;
  if (ok) {
    console.log(`  PASS  ${name}\n          ${detail}`);
  } else {
    failures += 1;
    console.log(`  FAIL  ${name}\n          ${detail}`);
  }
}

async function authHeaders(): Promise<Record<string, string>> {
  const res = await fetch(`${baseUrl}/auth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject: SEED_SUBJECT, amr: ["webauthn"] }),
  });
  const body = (await res.json()) as { token: string };
  return { Authorization: `Bearer ${body.token}` };
}

/** A statement-tree node, structurally typed so both sides of the tree fit. */
interface TreeNode {
  name: string;
  amount: string;
  children?: TreeNode[];
}

/** Depth-first search for a named node in a statement tree. */
function findNode(nodes: readonly TreeNode[], name: string): string | null {
  for (const n of nodes) {
    if (n.name === name) return n.amount;
    const v = findNode(n.children ?? [], name);
    if (v !== null) return v;
  }
  return null;
}

/** GET a JSON endpoint on cored that the typed client does not expose. */
async function getJSON<T>(path: string, headers: Record<string, string>): Promise<T> {
  const res = await fetch(`${baseUrl}${path}`, { headers });
  if (!res.ok) throw new Error(`${path}: ${res.status} ${await res.text()}`);
  return (await res.json()) as T;
}

async function main(): Promise<void> {
  const headers = await authHeaders();
  const client: CoredClient = createRestClient({ baseUrl, headers });
  const insightd = createInsightdClient({ baseUrl: insightdUrl });
  const co = companyID;

  console.log(`\nVERIFYING ${co}\n`);

  // --- 1. Trial Balance nets to zero -------------------------------------
  const tb = await client.getTrialBalance(co);
  check(
    "Trial Balance nets to zero",
    tb.balanced && tb.total_debit === tb.total_credit,
    `Dr ${tb.total_debit} / Cr ${tb.total_credit}`,
  );

  // --- 2. Balance Sheet balances -----------------------------------------
  const bs = await client.getBalanceSheet(co);
  check(
    "Balance Sheet balances (Assets = Liabilities + Capital)",
    bs.balanced && bs.assets.total === bs.liabilities.total,
    `assets ${bs.assets.total} / liabilities ${bs.liabilities.total}`,
  );

  // --- 3. Debtors and Creditors tie to Outstandings ----------------------
  // TWO INDEPENDENT PATHS: the Balance Sheet totals come from ledger_balance
  // (+ opening balances); Outstandings comes from bill_allocation UNION the
  // opening bill-wise breakup. They must agree exactly.
  const rec = await client.getOutstandings(co, { kind: "RECEIVABLE" });
  const debtors = findNode(bs.assets.nodes as unknown as TreeNode[], "Sundry Debtors");
  check(
    "BS Sundry Debtors == Outstandings (receivable)",
    debtors === rec.total,
    `BS ${debtors} vs Outstandings ${rec.total}`,
  );

  const pay = await client.getOutstandings(co, { kind: "PAYABLE" });
  const creditors = findNode(bs.liabilities.nodes as unknown as TreeNode[], "Sundry Creditors");
  check(
    "BS Sundry Creditors == Outstandings (payable)",
    creditors === pay.total,
    `BS ${creditors} vs Outstandings ${pay.total}`,
  );

  // --- 4-7. GSTR-1 shape --------------------------------------------------
  const g1 = (await client.generateGSTR1(co, FY)) as unknown as {
    gstin?: string;
    b2b?: { ctin: string; inv: unknown[] }[];
    b2cs?: unknown[];
    cdnr?: { ctin: string; nt: { ntty: string }[] }[];
    // `data` is null on a company with no per-line GST detail at all (every
    // pre-remediation company), so it is read defensively — this check must
    // REPORT that, not crash before the remaining checks run.
    hsn?: { data?: { hsn_sc: string; txval: string }[] | null } | null;
  };

  check(
    "GSTR-1 carries the filer's gstin",
    !!g1.gstin && g1.gstin.length === 15,
    `gstin=${g1.gstin ?? "(absent)"}`,
  );

  const b2bInvoices = (g1.b2b ?? []).reduce((n, p) => n + p.inv.length, 0);
  check(
    "GSTR-1 b2b is populated (not everything falling into b2cs)",
    (g1.b2b ?? []).length > 0 && b2bInvoices > 0,
    `${(g1.b2b ?? []).length} registered parties, ${b2bInvoices} invoices; b2cs entries=${(g1.b2cs ?? []).length}`,
  );

  const notes = (g1.cdnr ?? []).reduce((n, p) => n + p.nt.length, 0);
  check(
    "GSTR-1 cdnr is populated (credit/debit notes)",
    notes > 0,
    `${(g1.cdnr ?? []).length} parties, ${notes} notes: ` +
      (g1.cdnr ?? [])
        .map((p) => `${p.ctin}[${p.nt.map((n) => n.ntty).join(",")}]`)
        .join(" "),
  );

  const hsnRows = g1.hsn?.data ?? [];
  const blankHsn = hsnRows.filter((h) => !h.hsn_sc);
  check(
    "GSTR-1 hsn_sc fields are non-empty",
    hsnRows.length > 0 && blankHsn.length === 0,
    hsnRows.length === 0
      ? "no HSN rows at all (no per-line GST detail persisted)"
      : `${hsnRows.length} HSN rows: ` +
        hsnRows.map((h) => `${h.hsn_sc}(${h.txval})`).join(" "),
  );

  // A supplier-side note must NOT appear in the OUTWARD return.
  const supplierCtins = new Set<string>(
    (await client.listLedgers(co)).ledgers
      .filter((l) => l.group_name === "Sundry Creditors" && !!l.gstin)
      .map((l) => l.gstin),
  );
  const leaked = (g1.cdnr ?? []).filter((p) => supplierCtins.has(p.ctin));
  check(
    "GSTR-1 cdnr excludes SUPPLIER-side notes (outward return only)",
    leaked.length === 0,
    leaked.length === 0
      ? "no supplier GSTINs in cdnr"
      : `leaked: ${leaked.map((p) => p.ctin).join(", ")}`,
  );

  // --- 8. GSTR-3B ITC ties to the input-tax ledgers ----------------------
  // INDEPENDENT ORACLE: the return's ITC vs the actual balances sitting in the
  // Input CGST/SGST/IGST ledgers, read from the Trial Balance.
  // GSTR-3B has no typed client method; fetch it directly.
  const g3b = await getJSON<{
    itc_elg: { itc_net: { camt: string; samt: string; iamt: string } };
  }>(`/v1/companies/${co}/gstr3b?from=${FY.from}&to=${FY.to}`, headers);
  const ledgerNet = (name: string): string => {
    const row = tb.rows.find((r) => r.ledger === name);
    if (!row) return "0.0000";
    return (parseFloat(row.debit) - parseFloat(row.credit)).toFixed(4);
  };
  const itc = g3b.itc_elg.itc_net;
  const itcMatches =
    parseFloat(itc.camt).toFixed(4) === ledgerNet("Input CGST") &&
    parseFloat(itc.samt).toFixed(4) === ledgerNet("Input SGST") &&
    parseFloat(itc.iamt).toFixed(4) === ledgerNet("Input IGST");
  const itcTotal =
    parseFloat(itc.camt) + parseFloat(itc.samt) + parseFloat(itc.iamt);
  check(
    "GSTR-3B ITC == the balance in the Input CGST/SGST/IGST ledgers",
    itcMatches && itcTotal > 0,
    `return c=${itc.camt} s=${itc.samt} i=${itc.iamt} (total ${itcTotal.toFixed(2)}) | ` +
      `ledgers c=${ledgerNet("Input CGST")} s=${ledgerNet("Input SGST")} i=${ledgerNet("Input IGST")}`,
  );

  // --- 9. An ALTERED sales voucher stays in the GST returns ---------------
  // The Step-0 regression, re-proved against the LIVE demo data: INV-003 was
  // altered 24,000 -> 26,500, so the return's taxable total must include the
  // RESTATED figure. A drop here means voucher_type_id was lost on the alter.
  const hsnTotal = hsnRows.reduce((a, h) => a + parseFloat(h.txval), 0);
  const salesLedger = tb.rows.find((r) => r.ledger === "Domestic Sales");
  // Net sales from the books = credits - debits (a credit note debits sales).
  const booksSales =
    parseFloat(salesLedger?.credit ?? "0") - parseFloat(salesLedger?.debit ?? "0");
  check(
    "Altered sales voucher still reported (GSTR-1 taxable == net sales in the books)",
    Math.abs(hsnTotal - booksSales) < 0.005,
    `GSTR-1 taxable ${hsnTotal.toFixed(4)} vs Domestic Sales ${booksSales.toFixed(4)}`,
  );

  // --- 10. Party state / GSTIN / place-of-supply all agree ----------------
  const ledgers = (await client.listLedgers(co)).ledgers;
  const parties = ledgers.filter(
    (l) => l.group_name === "Sundry Debtors" || l.group_name === "Sundry Creditors",
  );
  const mismatched = parties.filter(
    (l) => !l.gstin || !l.state_code || !l.gstin.startsWith(l.state_code),
  );
  check(
    "Every party's GSTIN prefix == its state_code",
    mismatched.length === 0 && parties.length > 0,
    mismatched.length === 0
      ? parties.map((l) => `${l.name}:${l.state_code}`).join(" ")
      : `mismatched: ${mismatched.map((l) => `${l.name} gstin=${l.gstin} state=${l.state_code}`).join("; ")}`,
  );

  const unregistered = parties.filter((l) => l.registration_type !== "REGULAR");
  check(
    "Every party is registration_type REGULAR",
    unregistered.length === 0,
    unregistered.length === 0
      ? `${parties.length} parties`
      : `not REGULAR: ${unregistered.map((l) => l.name).join(", ")}`,
  );

  // --- 11. maintain_bill_by_bill on every party --------------------------
  const notBillwise = parties.filter((l) => !l.maintain_bill_by_bill);
  check(
    "maintain_bill_by_bill = true on every party",
    notBillwise.length === 0,
    notBillwise.length === 0
      ? `${parties.length} parties`
      : `off for: ${notBillwise.map((l) => l.name).join(", ")}`,
  );

  // --- 12. No duplicate cash ledger --------------------------------------
  const cashLedgers = ledgers.filter((l) => l.group_name === "Cash-in-Hand");
  check(
    "Exactly one Cash-in-Hand ledger (no duplicate dead row)",
    cashLedgers.length === 1,
    cashLedgers.map((l) => `${l.name}(system=${l.is_system})`).join(" "),
  );

  // --- 13. Bill Inbox draft belongs to these books -----------------------
  const drafts = await insightd.listDrafts(co);
  if (drafts.length === 0) {
    check("Bill Inbox draft present (survives an insightd restart)", false,
      "no drafts — is insightd running with INSIGHTD_DATABASE_URL set?");
  } else {
    const d = drafts[0]!;
    const full = (await (
      await fetch(`${insightdUrl}/companies/${co}/drafts/${d.draft_id}`)
    ).json()) as {
      body: {
        voucher_date: string;
        party: string;
        postings: { ledger_id: string; ledger_name: string }[];
        gst: { supplier_state_code: string; place_of_supply_state_code: string };
      };
    };
    const b = full.body;
    check(
      "Bill Inbox draft is dated inside the financial year",
      b.voucher_date >= FY.from && b.voucher_date <= FY.to,
      `voucher_date ${b.voucher_date} (FY ${FY.from}..${FY.to})`,
    );

    const known = new Set(ledgers.map((l) => l.id));
    const unknownPostings = b.postings.filter((p) => !known.has(p.ledger_id));
    check(
      "Bill Inbox draft resolves to ledgers that exist in these books",
      unknownPostings.length === 0,
      unknownPostings.length === 0
        ? b.postings.map((p) => p.ledger_name).join(" | ")
        : `unknown: ${unknownPostings.map((p) => p.ledger_name).join(", ")}`,
    );

    const home = parties.find((l) => l.name === b.party)?.state_code;
    const interState =
      b.gst.supplier_state_code !== b.gst.place_of_supply_state_code;
    const igstLeg = b.postings.find((p) => p.ledger_name === "Input IGST");
    check(
      "Bill Inbox draft's GST split matches the supplier's actual state",
      home === b.gst.supplier_state_code && interState === !!igstLeg,
      `party ${b.party} state=${home}; draft ${b.gst.supplier_state_code}->${b.gst.place_of_supply_state_code}; ` +
        `${igstLeg ? "IGST leg present" : "no IGST leg"}`,
    );
  }

  // -------------------------------------------------------------------------
  // PERIOD SEMANTICS on the real books. The Trial Balance and Balance Sheet are
  // POSITIONS AS AT a date; the Profit & Loss is a statement FOR A PERIOD. Two
  // properties are checked because either alone can pass while the numbers are
  // wrong: a POSITION must still BALANCE at every date (a filter that dropped
  // one leg of a voucher would not), and a PERIOD statement must be ADDITIVE
  // (halves summing to the whole is what makes it a period rather than a
  // position).
  // -------------------------------------------------------------------------
  const asAt = ["2026-04-30", "2026-05-31", "2026-06-30", "2026-07-31", FY.to];
  const unbalanced: string[] = [];
  for (const d of asAt) {
    const b = await client.getBalanceSheet(co, d);
    if (!b.balanced) unbalanced.push(`${d} (A ${b.assets.total} / L ${b.liabilities.total})`);
    const t = await client.getTrialBalance(co, d);
    if (!t.balanced) unbalanced.push(`TB ${d} (Dr ${t.total_debit} / Cr ${t.total_credit})`);
  }
  check(
    "Balance Sheet and Trial Balance BALANCE at every month-end of the year",
    unbalanced.length === 0,
    unbalanced.length === 0 ? `${asAt.length} dates checked, all balanced` : unbalanced.join("; "),
  );

  // A position BEFORE the books begin is empty, and an empty position balances.
  const preBooks = await client.getTrialBalance(co, "2020-01-01");
  check(
    "a Trial Balance dated before the books begin is empty, not an error",
    preBooks.balanced && parseFloat(preBooks.total_debit) === 0,
    `total_debit ${preBooks.total_debit} balanced=${preBooks.balanced}`,
  );

  // ...and a position AS AT the end of the year equals the unbounded one, since
  // nothing is posted beyond it. If these differed, the date bound would be
  // dropping real movement.
  const tbYearEnd = await client.getTrialBalance(co, FY.to);
  const tbUnbounded = await client.getTrialBalance(co);
  check(
    "a Trial Balance as at the year end equals the unbounded one (nothing is posted after it)",
    tbYearEnd.total_debit === tbUnbounded.total_debit,
    `as-at ${tbYearEnd.total_debit} vs unbounded ${tbUnbounded.total_debit}`,
  );

  // ADDITIVITY: the P&L for two disjoint, exhaustive halves must sum to the year.
  const plYear = await client.getProfitLoss(co, FY.from, FY.to);
  const plH1 = await client.getProfitLoss(co, FY.from, "2026-06-30");
  const plH2 = await client.getProfitLoss(co, "2026-07-01", FY.to);
  const sum = (
    Math.round(parseFloat(plH1.net_profit) * 10000) +
    Math.round(parseFloat(plH2.net_profit) * 10000)
  ) / 10000;
  check(
    "Profit & Loss is ADDITIVE: Apr–Jun + Jul–Mar equals the whole year",
    Math.abs(sum - parseFloat(plYear.net_profit)) < 0.00005,
    `${plH1.net_profit} + ${plH2.net_profit} = ${sum.toFixed(4)} vs year ${plYear.net_profit}`,
  );

  // A window with nothing in it is zero — not the whole year, which is what a
  // silently-ignored period would return.
  const plEmpty = await client.getProfitLoss(co, "2027-02-01", "2027-02-28");
  check(
    "a Profit & Loss over an EMPTY window is zero, not the whole year",
    parseFloat(plEmpty.net_profit) === 0,
    `Feb-2027 net_profit ${plEmpty.net_profit} (year is ${plYear.net_profit})`,
  );

  // An unparseable date is a 400, never a silent fallback to "no bound": a
  // report that quietly ignored the period would answer a different question
  // than the one on screen and still look right.
  const badDate = await fetch(
    `${baseUrl}/v1/companies/${co}/trial-balance?as_on=last-tuesday`,
    { headers },
  );
  check(
    "an unparseable as_on is a 400, not a silently-ignored filter",
    badDate.status === 400,
    `status ${badDate.status}`,
  );

  console.log(
    `\n${failures === 0 ? "ALL CHECKS PASSED" : `${failures} CHECK(S) FAILED`}` +
      ` (${checks - failures}/${checks})\n`,
  );
  process.exit(failures === 0 ? 0 : 1);
}

main().catch((err: unknown) => {
  console.error("verify FAILED:", err instanceof Error ? err.stack : err);
  process.exit(2);
});
