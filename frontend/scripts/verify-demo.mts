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

/**
 * MONEY IS A SCALED INTEGER HERE, NEVER A FLOAT.
 *
 * The project rule is `decimal.Decimal` on the server and scaled integers on
 * the client, and this gate used to be the one place that broke it: the P&L
 * additivity check ran `parseFloat` through `Math.round(x * 10000)` and then
 * compared with a 0.00005 tolerance. That is wrong for two reasons and only one
 * of them is about drift.
 *
 *   1. A tolerance turns an EXACT algebraic identity into an approximate one.
 *      Additivity is not "these two numbers are close"; the halves sum to the
 *      whole EXACTLY, because both sides are sums of the same decimal postings.
 *      A tolerance is headroom for a bug to hide in.
 *   2. A double carries 53 bits of mantissa, so `x * 10000` is exact only while
 *      the scaled value stays under 2^53 — about 900 billion rupees. The demo's
 *      figures are five and six digits, so the float arithmetic HAPPENS to be
 *      exact today. The headroom shrinks with the size of the books, and a gate
 *      that is correct only for small companies is not a gate. We remove the
 *      float because of the RULE, not because we watched it drift.
 *
 * Everything cored emits for money is `decimal.StringFixed(4)` — a plain,
 * optionally-signed decimal with exactly four fraction digits. Ten-thousandths
 * of a rupee is therefore the natural integer unit, and BigInt has no upper
 * bound to run out of.
 */
function money4(s: string): bigint {
  const m = /^([+-]?)(\d+)(?:\.(\d*))?$/.exec(s.trim());
  if (m === null) throw new Error(`money4: unparseable amount ${JSON.stringify(s)}`);
  const frac = m[3] ?? "";
  // Pad to exactly four digits. Truncating a NON-ZERO fifth digit would make
  // this gate silently disagree with the books, so refuse instead: a verifier
  // that cannot represent a figure must not pretend it checked it.
  if (frac.length > 4 && /[1-9]/.test(frac.slice(4))) {
    throw new Error(
      `money4: ${JSON.stringify(s)} carries more than 4 decimal places; ` +
        `cored emits StringFixed(4), so this is a contract change, not a rounding question`,
    );
  }
  const scaled = BigInt(m[2]!) * 10000n + BigInt(frac.padEnd(4, "0").slice(0, 4));
  return m[1] === "-" ? -scaled : scaled;
}

/** Render a scaled integer back as the 4-dp string the server would have sent. */
function money4str(n: bigint): string {
  const neg = n < 0n;
  const a = neg ? -n : n;
  return `${neg ? "-" : ""}${a / 10000n}.${(a % 10000n).toString().padStart(4, "0")}`;
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

  // --- 8. GSTR-3B table 4 ties to the input-tax ledgers -------------------
  //
  // THIS CHECK USED TO BE `itc_net == the Input-tax ledger balances`, AND THAT
  // COMPARISON IS NOW WRONG — not stale, WRONG, in the specific sense that it
  // goes red on CORRECT behaviour. Once a supplier's failure to file can
  // produce a table-4(B) reversal, the NET ITC (4C) is deliberately LESS than
  // what the books posted, and the difference is the right answer. A gate that
  // reddens on the right answer is worse than no gate: the cheapest way to make
  // it green again is to stop reversing.
  //
  // What replaces it is the pair of identities the return is actually built on,
  // and between them they are STRICTLY STRONGER than the old single comparison
  // (which they reduce to exactly when nothing is reversed and nothing is
  // ineligible):
  //
  //   (I1) sum(4A) + sum(4D) == the Input CGST/SGST/IGST ledger balances.
  //        Every rupee of posted input tax lands in exactly one of "available"
  //        and "never was available". This is the tie to the BOOKS and it is
  //        the reason 4(A) reports what we posted rather than what the supplier
  //        filed — reversals move credit out of 4(C), never out of 4(A).
  //   (I2) 4C == sum(4A) - sum(4B). 4(D) is NOT subtracted: post-Notification
  //        14/2022 it is informational, and taking it out twice would
  //        understate claimable credit.
  //
  // Both are exact, head by head, in SCALED INTEGERS — see money4. The old code
  // in this block parsed the four figures as doubles and compared them at
  // toFixed(4), against the project's own rule; (I2) is a SUBTRACTION, which is
  // exactly where that stops being a style point and starts being drift.
  const g3b = await getJSON<{
    itc_elg: {
      itc_avl?: { ty: string; camt: string; samt: string; iamt: string }[];
      itc_rev?: { ty: string; camt: string; samt: string; iamt: string }[];
      itc_net: { camt: string; samt: string; iamt: string };
      itc_inelg?: { ty: string; camt: string; samt: string; iamt: string }[];
    };
    disclosures?: string[];
  }>(`/v1/companies/${co}/gstr3b?from=${FY.from}&to=${FY.to}`, headers);

  type Head = "camt" | "samt" | "iamt";
  const HEADS: { head: Head; ledger: string }[] = [
    { head: "camt", ledger: "Input CGST" },
    { head: "samt", ledger: "Input SGST" },
    { head: "iamt", ledger: "Input IGST" },
  ];
  /** Net Dr-Cr balance of a Trial Balance row, as a scaled integer. */
  const ledgerNet = (name: string): bigint => {
    const row = tb.rows.find((r) => r.ledger === name);
    if (!row) return 0n;
    return money4(row.debit) - money4(row.credit);
  };
  /** Sum one tax head across a table-4 row set, as a scaled integer. */
  const sumHead = (
    rows: { camt: string; samt: string; iamt: string }[] | undefined,
    head: Head,
  ): bigint => (rows ?? []).reduce((a, r) => a + money4(r[head]), 0n);

  const t4 = g3b.itc_elg;
  const booksBreaks: string[] = [];
  const internalBreaks: string[] = [];
  let availTotal = 0n;
  for (const { head, ledger } of HEADS) {
    const avl = sumHead(t4.itc_avl, head);
    const rev = sumHead(t4.itc_rev, head);
    const inelg = sumHead(t4.itc_inelg, head);
    availTotal += avl;
    const posted = ledgerNet(ledger);
    if (avl + inelg !== posted) {
      booksBreaks.push(
        `${ledger}: 4A ${money4str(avl)} + 4D ${money4str(inelg)} = ${money4str(avl + inelg)} vs posted ${money4str(posted)}`,
      );
    }
    if (avl - rev !== money4(t4.itc_net[head])) {
      internalBreaks.push(
        `${head}: 4A ${money4str(avl)} - 4B ${money4str(rev)} = ${money4str(avl - rev)} vs 4C ${t4.itc_net[head]}`,
      );
    }
  }
  check(
    "GSTR-3B (I1): sum(4A) + sum(4D) == the Input CGST/SGST/IGST ledger balances",
    booksBreaks.length === 0 && availTotal > 0n,
    booksBreaks.length > 0
      ? booksBreaks.join("; ")
      : availTotal > 0n
        ? HEADS.map(
            ({ head, ledger }) =>
              `${ledger} ${money4str(ledgerNet(ledger))} = 4A ${money4str(sumHead(t4.itc_avl, head))} + 4D ${money4str(sumHead(t4.itc_inelg, head))}`,
          ).join(" | ")
        : "VACUOUS: table 4(A) is entirely zero, so this identity proves nothing",
  );
  check(
    "GSTR-3B (I2): 4C == sum(4A) - sum(4B), and 4(D) is NOT subtracted",
    internalBreaks.length === 0,
    internalBreaks.length === 0
      ? `4C c=${t4.itc_net.camt} s=${t4.itc_net.samt} i=${t4.itc_net.iamt} from ` +
        `4A c=${money4str(sumHead(t4.itc_avl, "camt"))} less 4B c=${money4str(sumHead(t4.itc_rev, "camt"))}` +
        ` (4D c=${money4str(sumHead(t4.itc_inelg, "camt"))} deliberately excluded)`
      : internalBreaks.join("; "),
  );
  // The return must be able to SAY it is not filing-ready. A frontend that
  // cannot read that flag will present an unreconciled 3B as final.
  check(
    "GSTR-3B declares its own limitations in `disclosures`",
    (g3b.disclosures ?? []).includes("not-filing-ready"),
    `disclosures: ${(g3b.disclosures ?? []).join(", ") || "(none)"}`,
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
  const stockMismatch: string[] = [];
  const stockSeen: string[] = [];
  let closingStockAtYearEnd: string | null = null;
  for (const d of asAt) {
    const b = await client.getBalanceSheet(co, d);
    if (!b.balanced) unbalanced.push(`${d} (A ${b.assets.total} / L ${b.liabilities.total})`);
    const t = await client.getTrialBalance(co, d);
    if (!t.balanced) unbalanced.push(`TB ${d} (Dr ${t.total_debit} / Cr ${t.total_credit})`);

    // TWO INDEPENDENT PATHS over the stock plane — see the check below for why
    // the BALANCE check above cannot stand in for this one.
    const ss = await client.getStockSummary(co, d);
    const node = findNode(b.assets.nodes as unknown as TreeNode[], "Closing Stock");
    if (d === FY.to) closingStockAtYearEnd = node;
    stockSeen.push(`${d} BS=${node ?? "ABSENT"} SS=${ss.total_value}`);
    if (node === null) {
      // ABSENT is a distinct outcome from "0.0000" and it is checked on its own:
      // the demo seed puts stock on hand at every one of these dates, so an
      // absent node means the fold-in did not happen at all — which the string
      // comparison below could never report, because there is no string.
      stockMismatch.push(`${d}: Closing Stock ABSENT from the Balance Sheet while the Stock Summary values the position at ${ss.total_value}`);
    } else if (node !== ss.total_value) {
      stockMismatch.push(`${d}: BS ${node} vs Stock Summary ${ss.total_value}`);
    }
  }
  check(
    "Balance Sheet and Trial Balance BALANCE at every month-end of the year",
    unbalanced.length === 0,
    unbalanced.length === 0 ? `${asAt.length} dates checked, all balanced` : unbalanced.join("; "),
  );

  // WHY THE CHECK ABOVE IS NOT ENOUGH, AND WHY THIS ONE EXISTS.
  //
  // Closing stock folds into the Balance Sheet by adding the SAME amount S to
  // two places: to Assets, and to the net-profit accumulator that lands in
  // Liabilities. Write out the difference the `balanced` flag tests:
  //
  //     (Assets + S) - (Liabilities + NetProfit + S)  =  Assets - Liabilities - NetProfit
  //
  // S cancels. The sheet therefore FOOTS FOR ANY VALUE OF S — the right one, one
  // off by a factor of ten, one computed at the wrong date, or zero. `balanced`
  // can catch exactly one bug class, an ASYMMETRIC fold-in (the asset without
  // the P&L term, or the two terms taken at different dates), and nothing more.
  // It is structurally incapable of catching a wrong NUMBER. Keep it — it is a
  // real check of a real property — but never treat it as cover for this one.
  //
  // So the number is pinned the way this file pins Debtors and Creditors: by a
  // SECOND INDEPENDENT PATH. The Balance Sheet's Closing Stock node and the
  // Stock Summary's total_value both replay the movement ledger, but they are
  // reached through different report code and different handlers, and both are
  // rendered with decimal StringFixed(4) — so they must agree to the BYTE. No
  // tolerance, no parseFloat, no "close enough".
  check(
    "Balance Sheet Closing Stock == the Stock Summary at every month-end (TWO INDEPENDENT PATHS)",
    stockMismatch.length === 0,
    stockMismatch.length === 0
      ? `${asAt.length} dates agree exactly: ${stockSeen.join(" | ")}`
      : stockMismatch.join("; "),
  );

  // A position BEFORE the books begin is empty, and an empty position balances.
  const preBooks = await client.getTrialBalance(co, "2020-01-01");
  check(
    "a Trial Balance dated before the books begin is empty, not an error",
    preBooks.balanced && parseFloat(preBooks.total_debit) === 0,
    `total_debit ${preBooks.total_debit} balanced=${preBooks.balanced}`,
  );

  // ...and the same must be true of the BALANCE SHEET, which until now this gate
  // never fetched at a pre-books date at all — only the Trial Balance above.
  //
  // THIS IS A NAMED REGRESSION CLASS, NOT A HYPOTHETICAL. A live verifier once
  // caught a Trial Balance dated 2020 reporting 520,000 of opening capital on
  // books that begin in 2026: the opening balances were being folded in
  // regardless of the as-on date. `queryBalances` suppresses the opening when
  // `as_on < books_beginning_from` because of that bug. The inventory plane has
  // its own opening — `stock_opening`, which carries NO date of its own because
  // it is stated AS AT the books' beginning — and `loadStockOpenings` carries
  // the matching cutoff. This check is what stops the fold-in reintroducing the
  // bug on the stock side.
  //
  // ABSENT vs 0: `findNode` returns null for a node that is not in the tree and
  // a string for one that is, so `=== null` distinguishes the two. Comparing
  // against "0.0000" would pass just as happily on a zero-valued row that had
  // changed the shape of the statement, which is a different (and also wrong)
  // outcome. The seed's `stock_opening` row is what makes this non-vacuous: it
  // is real stock that WOULD leak here if the cutoff were missing.
  const preBooksBS = await client.getBalanceSheet(co, "2020-01-01");
  const preBooksStock = findNode(
    preBooksBS.assets.nodes as unknown as TreeNode[],
    "Closing Stock",
  );
  check(
    "a Balance Sheet dated before the books begin is empty — no opening stock leaks",
    preBooksBS.balanced &&
      preBooksStock === null &&
      money4(preBooksBS.assets.total) === 0n,
    `2020-01-01: assets.total ${preBooksBS.assets.total}, balanced=${preBooksBS.balanced}, ` +
      `Closing Stock ${preBooksStock === null ? "ABSENT" : `PRESENT ${preBooksStock}`} ` +
      `— while the SAME report at ${FY.to} carries Closing Stock ` +
      `${closingStockAtYearEnd ?? "ABSENT"} on assets ${bs.assets.total}`,
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
  //
  // WHAT ADDITIVITY MEANS ONCE STOCK IS IN THE STATEMENT. The stock term of a
  // P&L over [d0,d1] is S(d0,d1) = ClosingStock(d1) - ClosingStock(d0 MINUS ONE).
  // For two windows that MEET — [d0,d1] and [d1+1,d2] — the shared boundary
  // value appears once positively and once negatively:
  //
  //     S(d0,d1) + S(d1+1,d2) = (CS(d1) - CS(d0-1)) + (CS(d2) - CS(d1))
  //                           = CS(d2) - CS(d0-1) = S(d0,d2)
  //
  // and it cancels. That telescoping is the WHOLE reason the opening term is
  // taken at d0 MINUS ONE rather than at d0: take it at d0 and the two halves no
  // longer share a value, and they differ by whatever moved on d1+1 — which on
  // these books is a real receipt note, so the identity below has something to
  // detect. The other way to break it is `from == ""`, which means UNBOUNDED and
  // must map the opening term to ZERO rather than to CS(""); CS("") is the whole
  // warehouse, and subtracting it would understate gross profit by every rupee
  // of inventory while the statement still footed.
  //
  // NOTE the identity holds for CONTIGUOUS windows only, which is why both of
  // these checks split a single span at a seam rather than adding up months
  // picked out of the year.
  const plYear = await client.getProfitLoss(co, FY.from, FY.to);
  const plH1 = await client.getProfitLoss(co, FY.from, "2026-06-30");
  const plH2 = await client.getProfitLoss(co, "2026-07-01", FY.to);
  const halvesSum = money4(plH1.net_profit) + money4(plH2.net_profit);
  check(
    "Profit & Loss is ADDITIVE: Apr–Jun + Jul–Mar equals the whole year",
    // EXACT, with no tolerance: see money4's header. Both sides are sums of the
    // same decimal postings, so "close enough" is only ever room for a bug.
    halvesSum === money4(plYear.net_profit),
    `${plH1.net_profit} + ${plH2.net_profit} = ${money4str(halvesSum)} vs year ${plYear.net_profit}`,
  );

  // The same identity again, TIGHTENED ONTO THE SEAM. The split above is a real
  // check but it is a blunt one: each half spans months of unrelated movement,
  // so a wrong opening term is one small figure inside two large ones. Here both
  // windows are a single month and they meet on 2026-07-01, the date the seed
  // brings goods in — so the boundary position CS(2026-06-30) is non-zero and
  // the two halves only agree with the whole if the opening stock of July is
  // taken at 2026-06-30 and not at 2026-07-01. An off-by-one on that one
  // subtraction moves this sum by exactly the value of the 1 July inward.
  const plJun = await client.getProfitLoss(co, "2026-06-01", "2026-06-30");
  const plJul = await client.getProfitLoss(co, "2026-07-01", "2026-07-31");
  const plJunJul = await client.getProfitLoss(co, "2026-06-01", "2026-07-31");
  const seamSum = money4(plJun.net_profit) + money4(plJul.net_profit);
  check(
    "P&L halves that meet on 2026-07-01 still sum — the opening stock is taken at d0 MINUS ONE",
    seamSum === money4(plJunJul.net_profit),
    `Jun ${plJun.net_profit} + Jul ${plJul.net_profit} = ${money4str(seamSum)} ` +
      `vs Jun–Jul ${plJunJul.net_profit} ` +
      `(difference ${money4str(seamSum - money4(plJunJul.net_profit))})`,
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

  // -------------------------------------------------------------------------
  // THE DEMO COMPANY CAN PRINT ITS OWN FIRST INVOICE.
  //
  // WHY THE BYTES AND NOT THE STATUS CODE. Every other way of checking a print
  // route passes on a document that is WRONG: a 200 passes on an empty page, a
  // content-type passes on somebody else's invoice, and a length passes on a
  // PDF containing the word "undefined" four times. The one check that cannot be
  // satisfied by the wrong document is to look for the document's own
  // identifying strings INSIDE the file — and that is possible here only because
  // the PDF writer leaves its content streams UNCOMPRESSED on purpose. Deflate
  // them and this check silently degrades to "some bytes came back", so if a
  // future change adds compression it must replace these two greps with text
  // extraction, not delete them.
  //
  // AND THAT DEGRADATION IS ITSELF ASSERTED, because a check that can quietly
  // stop checking is worse than one that was never written: the day somebody
  // switches on FlateDecode, both greps below would still PASS on a file whose
  // text they can no longer see, and this gate would go on reporting that the
  // right document came back. So the invoice half also asserts the PRECONDITION
  // its greps rest on — a plain `/Length N >>` stream dictionary, and no stream
  // filter anywhere in the file. That assertion fails LOUDLY on the compression
  // commit, naming what to do about it, instead of being discovered months later
  // by a cross-tenant leak this gate was supposed to catch.
  //
  // A REFUSAL PRINTS ITS BODY, NOT JUST ITS STATUS. This check now depends on a
  // chain: the ledger master must hold the party's address, POST /ledgers must
  // carry it through, the demo seed must set one, and printingdb must put it in
  // the recipient block — because Rule 46(d) requires the recipient's name AND
  // address, and the renderer REFUSES an invoice missing a mandatory particular
  // rather than printing a blank. The refusal names every missing field
  // (`missing_fields: ["recipient.address1", ...]`), so printing the body turns
  // "false" into "the demo data has no address" or "the pipeline dropped it" at
  // a glance. A gate that says only `status 400` costs an hour of bisecting.
  //
  // The two strings chosen are the two that identify WHICH document this is and
  // WHOSE books it came from: the voucher number, and the filer's GSTIN. A
  // cross-tenant bug in the print handler — the print route reading a voucher
  // without re-scoping to the company in the path — would come back 200 with a
  // valid PDF and the WRONG GSTIN, and nothing else in this file would notice.
  //
  // A 501 IS NOT TOLERATED HERE. The print track deliberately returns 501 for
  // the report names it has not implemented, and this check names two it HAS
  // (a sales voucher and the Trial Balance), so a 501 on either is a regression
  // and not a disclosure.
  const pdfChecks = async (): Promise<void> => {
    const company = await client.getCompany(co);
    const day = await client.getDayBook(co, { from: FY.from, to: FY.to });
    // THE CLASS LABEL IS CAPITALISED AND THIS LINE SAID "sales" FOR ONE WAVE.
    // ledger.Class is `Sales` (internal/ledger/ledger.go:65) and the Day Book
    // hands back the payload's label verbatim (reporting.DayBook's Class is
    // "accounting class label from the payload"), so `e.class === "sales"`
    // matched NOTHING: this gate reported "there is nothing to print" against a
    // day book holding a Sales voucher, and — because the guard below returns —
    // it also skipped the Trial-Balance PDF check underneath it. Two assertions
    // written to close a blocker were dead code, and the gate was red for a
    // reason that had nothing to do with printing.
    //
    // Compared case-INSENSITIVELY on purpose, so this cannot break again if the
    // label's casing is ever normalised at either end. It is matched against the
    // Go constant, not against a lowercase guess.
    const sales = day.entries.find((e) => e.class.toLowerCase() === "sales");
    if (!sales) {
      check(
        "the demo company can PRINT its first sales invoice",
        false,
        `no sales voucher in the day book for ${FY.from}..${FY.to}, so there is nothing to print — ` +
          `${day.entries.length} entries of classes [${[...new Set(day.entries.map((e) => e.class))].join(", ")}]. ` +
          `If that list DOES contain a sale, this selector is broken again, not the seed`,
      );
      return;
    }

    /**
     * GET a print route and describe the bytes. Returns the raw body as a
     * LATIN-1 string: one JavaScript char per byte, so `%PDF-`, `%%EOF` and an
     * ASCII invoice number can be searched for without a text decoder deciding
     * that a byte over 0x7f was a broken UTF-8 sequence and replacing it.
     */
    const getPDF = async (
      path: string,
    ): Promise<{ status: number; ctype: string; bytes: number; text: string }> => {
      const res = await fetch(`${baseUrl}${path}`, { headers });
      const buf = Buffer.from(await res.arrayBuffer());
      return {
        status: res.status,
        ctype: (res.headers.get("content-type") ?? "").split(";")[0]!.trim(),
        bytes: buf.length,
        text: buf.toString("latin1"),
      };
    };

    /**
     * What went wrong, in the words the server used. A print refusal is JSON
     * naming every mandatory particular that is absent, so it is the single
     * most useful thing this gate can put on the operator's screen; a PDF body
     * is binary and is described rather than dumped. Whitespace is collapsed so
     * a multi-line error stays on one gate line.
     */
    const bodyDiag = (r: { ctype: string; bytes: number; text: string }): string =>
      r.ctype === "application/pdf"
        ? `${r.bytes} bytes of PDF`
        : `body [${r.ctype || "no content-type"}, ${r.bytes} bytes]: ` +
          (r.text.slice(0, 800).replace(/\s+/g, " ").trim() || "(empty)");

    const invPath = `/v1/companies/${co}/print/vouchers/sales/${sales.voucher_id}`;
    const inv = await getPDF(invPath);
    // %%EOF is the last LINE of a PDF, so a trailing newline is legal and
    // expected; trimming it is not a weakening of the check.
    const invTail = inv.text.trimEnd();
    const wantNo = sales.voucher_no;
    const wantGSTIN = company.gstin;
    const invBreaks: string[] = [];
    if (inv.status !== 200) {
      invBreaks.push(
        `status ${inv.status}` +
          (inv.status === 400 || inv.status === 422
            ? " — the document was REFUSED as incomplete, so read missing_fields below:" +
              " a Rule 46 particular the demo data does not carry (recipient.address1 means" +
              " the party master has no address, or POST /ledgers dropped it, or the seed set none)"
            : "") +
          ` — ${bodyDiag(inv)}`,
      );
    }
    if (inv.ctype !== "application/pdf") invBreaks.push(`content-type ${inv.ctype || "(absent)"}`);
    if (!inv.text.startsWith("%PDF-")) invBreaks.push(`does not start with %PDF- (starts ${JSON.stringify(inv.text.slice(0, 8))})`);
    if (!invTail.endsWith("%%EOF")) invBreaks.push(`does not end with %%EOF (ends ${JSON.stringify(invTail.slice(-16))})`);
    if (inv.bytes <= 1000) invBreaks.push(`only ${inv.bytes} bytes — an invoice that short is a blank page, not a document`);
    // THE PRECONDITION THE TWO GREPS BELOW REST ON, asserted before them so a
    // compressed file is reported as "this gate can no longer see the text"
    // rather than as "the invoice number is missing". printing/pdf.go writes
    // every content stream as `<< /Length N >>\nstream\n` with no filter, on
    // purpose (see its header). Either half of this going wrong makes the greps
    // vacuous: no plain /Length dict means the file is not shaped the way we
    // grep it, and any stream filter means the bytes we are grepping are
    // compressed and the greps prove only that some bytes came back.
    //
    // Only asked of a body that IS a PDF: on a refusal the status line above
    // already carries the whole explanation, and two more sentences about stream
    // dictionaries would bury it.
    if (inv.status === 200 && inv.ctype === "application/pdf") {
      if (!/\/Length \d+ >>\s*stream/.test(inv.text)) {
        invBreaks.push(
          "no plain `/Length N >> stream` dictionary in the file, so the two greps below cannot be trusted:" +
            " they search the RAW bytes and only work while printing/pdf.go writes uncompressed content streams." +
            " If the PDF writer changed, replace the greps with real text extraction — do not delete them",
        );
      }
      const filterAt = inv.text.search(/\/Filter|FlateDecode/);
      if (filterAt >= 0) {
        invBreaks.push(
          `the PDF now carries a stream filter (${JSON.stringify(inv.text.slice(filterAt, filterAt + 40))} at byte ${filterAt}),` +
            " so grepping the raw bytes for the invoice number and the GSTIN proves NOTHING — a compressed stream" +
            " contains neither string even when the document is perfect, and both greps would pass on the wrong" +
            " document. This gate is telling you it stopped checking: extract the text (or print an uncompressed" +
            " copy for the gate) before you take the two checks below as evidence again",
        );
      }
    }
    if (!inv.text.includes(wantNo)) invBreaks.push(`the voucher number ${JSON.stringify(wantNo)} does not appear in the file, so this is not that invoice (or the streams are now compressed, which this check cannot see through)`);
    if (wantGSTIN === "") {
      invBreaks.push("the demo company has no GSTIN recorded, so the who-printed-it half of this check is VACUOUS");
    } else if (!inv.text.includes(wantGSTIN)) {
      invBreaks.push(`the filer's GSTIN ${wantGSTIN} does not appear in the file — a tax invoice without the supplier's GSTIN is defective under Rule 46(a), and a PDF carrying a DIFFERENT company's GSTIN is a cross-tenant leak`);
    }
    check(
      `the demo company PRINTS its own sales invoice ${wantNo} as a PDF that names itself`,
      invBreaks.length === 0,
      invBreaks.length === 0
        ? `${invPath} -> 200 application/pdf, ${inv.bytes} bytes, %PDF-..%%EOF, uncompressed streams, contains ${wantNo} and ${wantGSTIN}`
        : invBreaks.join("; "),
    );

    const tbPath = `/v1/companies/${co}/print/reports/trial-balance`;
    const tbPDF = await getPDF(tbPath);
    const tbTail = tbPDF.text.trimEnd();
    const tbBreaks: string[] = [];
    if (tbPDF.status !== 200) tbBreaks.push(`status ${tbPDF.status}${tbPDF.status === 501 ? " — the Trial Balance is one of the statements this track DOES print, so 501 is a regression" : ""}`);
    if (tbPDF.ctype !== "application/pdf") tbBreaks.push(`content-type ${tbPDF.ctype || "(absent)"}`);
    if (!tbPDF.text.startsWith("%PDF-")) tbBreaks.push(`does not start with %PDF- (starts ${JSON.stringify(tbPDF.text.slice(0, 8))})`);
    if (!tbTail.endsWith("%%EOF")) tbBreaks.push(`does not end with %%EOF (ends ${JSON.stringify(tbTail.slice(-16))})`);
    if (tbPDF.bytes <= 1000) tbBreaks.push(`only ${tbPDF.bytes} bytes`);
    check(
      "a printed Trial Balance is a PDF",
      tbBreaks.length === 0,
      tbBreaks.length === 0
        ? `${tbPath} -> 200 application/pdf, ${tbPDF.bytes} bytes, %PDF-..%%EOF`
        : tbBreaks.join("; "),
    );
  };
  await pdfChecks();

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
