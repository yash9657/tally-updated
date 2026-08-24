/**
 * L25 — D83: two CONCURRENT posts of one hand-typed bill number merged into ONE bill.
 *
 * ⛔⛔ THE ASSERTION IS NOT ON THE STATUS CODES, AND A PROBE THAT CHECKED THEM WOULD BE
 * GREEN ON THE BROKEN CODE. Both posts return 201 before the fix AND after it. What
 * changes is the BOOKS: before, `GET /outstandings` showed ONE bill of the SUMMED amount
 * aged at the EARLIER due date, with the second instalment's amount and its own due date
 * simply gone from the ageing report and nothing anywhere to say so.
 *
 * ⛔ AND IT MUST BE GENUINELY CONCURRENT. The SEQUENTIAL case has always been closed —
 * `resolveGeneratedBillRefNames` asks "has any other voucher ever called a bill this?"
 * and renames itself — so a probe that posts one after the other passes in both worlds.
 * Both requests are put in flight with `Promise.all` before either can resolve.
 *
 * ⛔ THREE THINGS MUST BE TRUE OF THE FIXTURE OR IT MEASURES NOTHING:
 *   1. the SAME hand-typed `voucher_no` on both — numbering.go honours a client-supplied
 *      number verbatim and does NOT bump the counter, which is the door;
 *   2. NEITHER names its reference (`ref_name: ""`, `ref_type: "NEW"`) — only names the
 *      SERVER invents are resolved, because a name a caller supplied is left alone on
 *      purpose (that is how Tally adds to an existing bill);
 *   3. DIFFERENT amounts and DIFFERENT due dates, so the merge is visible as two lost
 *      facts rather than as a number that happens to look plausible.
 *
 * ⚠️ IT MINTS ITS OWN TENANT AND REAPS IT (D91/L17 pattern). It posts real vouchers, and
 * a probe that raced two of them into a shared tenant would leave a bill nobody can
 * explain.
 *
 * RUN:  node frontend/scripts/drive-l25-d83.mjs        (takes NO CO — it makes one)
 */
import { execFileSync } from "node:child_process";

import { rec, summary } from "./audit-harness.mjs";

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
  try { parsed = text ? JSON.parse(text) : {}; } catch { parsed = { raw: text }; }
  return { status: r.status, body: parsed, text };
};
const mint = async (subject) =>
  (await j("POST", "/auth/token", { body: { subject, amr: ["webauthn"] } })).body.token;
const psql = (sql) =>
  execFileSync("psql", [
    "-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql,
  ], { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" }).trim();

const stamp = Math.random().toString(36).slice(2, 8);
const CO = crypto.randomUUID();
const OWNER_SUB = `d83-owner-${stamp}`;
const OWNER = await mint(OWNER_SUB);
{
  const r = await j("POST", "/v1/companies", {
    token: OWNER,
    body: { id: CO, name: `D83 Probe Co ${stamp}`, fy_start_date: "2026-04-01", base_currency: "INR", state_code: "29" },
  });
  if (r.status !== 201) throw new Error(`create company: ${r.status} ${r.text}`);
}
const base = `/v1/companies/${CO}`;
console.log(`\n── tenant ${CO}  ·  owner ${OWNER_SUB} ─────────────`);

const mk = async (name, group, billwise) => {
  const r = await j("POST", `${base}/ledgers`, {
    token: OWNER,
    body: { name, group_name: group, state_code: "29", maintain_bill_by_bill: billwise },
  });
  if (r.status !== 201) throw new Error(`create ledger ${name}: ${r.status} ${r.text}`);
  return r.body.ledger_id ?? r.body.id;
};
const CUST = await mk("D83 Customer", "Sundry Debtors", true);
const SALES = await mk("D83 Sales", "Sales Accounts", false);

const NUMBER = "7";
const A = { amount: "1000", due: "2026-07-10" };
const B = { amount: "2000", due: "2026-08-10" };
const sale = ({ amount, due }) => ({
  voucher_date: "2026-06-10",
  created_by: OWNER_SUB,
  voucher_no: NUMBER,
  party: "D83 Customer",
  party_ledger_id: CUST,
  postings: [
    { ledger_id: CUST, dr_cr: "D", amount },
    { ledger_id: SALES, dr_cr: "C", amount },
  ],
  bill_allocations: [
    { ledger_id: CUST, ref_name: "", ref_type: "NEW", dr_cr: "D", amount, due_date: due },
  ],
});

try {
  // =========================================================================
  // ⛔ THE RACE — both in flight before either resolves.
  // =========================================================================
  const [ra, rb] = await Promise.all([
    j("POST", `${base}/vouchers/sales`, { token: OWNER, body: sale(A) }),
    j("POST", `${base}/vouchers/sales`, { token: OWNER, body: sale(B) }),
  ]);
  rec("D83: ⛔ CONTROL — BOTH concurrent posts still succeed",
      ra.status === 201 && rb.status === 201,
      `A=${ra.status} ${ra.body.voucher_no ?? ra.text.slice(0, 60)} · ` +
      `B=${rb.status} ${rb.body.voucher_no ?? rb.text.slice(0, 60)} — ` +
      `this row is about what LANDS IN THE BOOKS, not about refusing a write`);
  rec("D83: ⛔ CONTROL — …and both carry the SAME hand-typed number, which is the door",
      ra.body.voucher_no === NUMBER && rb.body.voucher_no === NUMBER,
      `A no=${ra.body.voucher_no} · B no=${rb.body.voucher_no} (numbering.go honours a ` +
      `client-supplied number verbatim and does not bump the counter)`);

  // =========================================================================
  // ⛔⛔ THE ROW — read the BOOKS back through the report that groups by name.
  // =========================================================================
  const os = await j("GET", `${base}/outstandings?kind=RECEIVABLE`, { token: OWNER });
  const bills = (os.body.parties ?? []).flatMap((p) => p.bills ?? []);
  const byAmount = new Map(bills.map((b) => [b.pending, b]));
  rec("D83: ⛔⛔ the ageing report shows TWO bills, not one of the summed amount",
      os.status === 200 && bills.length === 2,
      `${bills.length} bill(s): ` +
      bills.map((b) => `${JSON.stringify(b.ref_name)}=${b.pending} due ${b.due_date}`).join(" · ") +
      (bills.length === 1 ? "  ⛔ ONE BILL = the merge: the later instalment is GONE" : ""));

  rec("D83: ⛔ each bill keeps its OWN amount — the summed figure is absent",
      byAmount.has("1000.0000") && byAmount.has("2000.0000") && !byAmount.has("3000.0000"),
      `amounts on the report: ${bills.map((b) => b.pending).join(", ")}`);
  rec("D83: ⛔ …and its OWN due date — the merge aged BOTH at the earlier one",
      byAmount.get("1000.0000")?.due_date === A.due &&
        byAmount.get("2000.0000")?.due_date === B.due,
      `1000 due ${byAmount.get("1000.0000")?.due_date} (want ${A.due}) · ` +
      `2000 due ${byAmount.get("2000.0000")?.due_date} (want ${B.due})`);

  // ⛔ THE LOSER TOOK THE **RENAME** BRANCH, which is what says the fix worked rather
  // than the two of them differing by luck. Exactly one discriminated stem.
  const stems = bills.map((b) => b.ref_name).filter((n) => / \(\w{8}\)$/.test(n));
  rec("D83: ⛔ exactly ONE bill carries the discriminated `(xxxxxxxx)` stem",
      stems.length === 1 && bills.length === 2,
      `names: ${bills.map((b) => JSON.stringify(b.ref_name)).join(" · ")}`);
  rec("D83: ⛔ …and the other keeps the plain generated name, so the winner is unrenamed",
      bills.some((b) => b.ref_name === "Sales/2026-27/7"),
      `names: ${bills.map((b) => b.ref_name).join(" · ")}`);

  // ⛔ FOOTED AGAINST A DIFFERENT HANDLER: the total must be unchanged by the split.
  // The defect never lost MONEY — it lost a BILL — so a probe that only checked the
  // total would be green in both worlds. Asserted precisely because it does NOT move.
  const bs = await j("GET", `${base}/balance-sheet`, { token: OWNER });
  let debtors = null;
  (function walk(n) {
    if (Array.isArray(n)) return n.forEach(walk);
    if (n && typeof n === "object") {
      if (n.name === "Sundry Debtors") debtors = String(n.amount ?? n.value ?? "");
      Object.values(n).forEach(walk);
    }
  })(bs.body);
  rec("D83: ⚠️ the TOTAL is unchanged by the split — the defect lost a BILL, not money",
      os.body.total === "3000.0000" && debtors === "3000.0000",
      `Outstandings total ${os.body.total} · Balance Sheet Sundry Debtors ${debtors} — ` +
      `equal in BOTH worlds, which is why a total-only probe proves nothing here`);
} finally {
  // -------------------------------------------------------------------------
  // ⛔ REAP (§5.14). This tenant has BOOKS.
  // -------------------------------------------------------------------------
  const q = (s) => `'${String(s).replace(/'/g, "''")}'`;
  // ⛔ `posting`/`voucher_version` carry *_no_mutate triggers that RAISE on DELETE.
  // `SET LOCAL session_replication_role = replica` is TRANSACTION-LOCAL and reverts at
  // COMMIT; permissible ONLY because this tenant was minted by this probe moments ago
  // (§5.26). NEVER against books an operator keyed.
  psql(
    `begin;
     set local session_replication_role = replica;
     delete from posting_cost_allocation where company_id = ${q(CO)};
     delete from bill_allocation    where company_id = ${q(CO)};
     delete from voucher_gst_line   where voucher_version_id in (select id from voucher_version where company_id = ${q(CO)});
     delete from posting            where voucher_version_id in (select id from voucher_version where company_id = ${q(CO)});
     delete from voucher_head       where company_id = ${q(CO)};
     delete from voucher_version    where company_id = ${q(CO)};
     delete from voucher_number_counter where company_id = ${q(CO)};
     delete from acl_rule           where company_id = ${q(CO)};
     delete from company_membership where company_id = ${q(CO)};
     delete from company_invite     where company_id = ${q(CO)};
     delete from notification_outbox where company_id = ${q(CO)};
     delete from ledger             where company_id = ${q(CO)};
     delete from godown             where company_id = ${q(CO)};
     delete from unit               where company_id = ${q(CO)};
     delete from voucher_type       where company_id = ${q(CO)};
     delete from account_group      where company_id = ${q(CO)};
     delete from security_level     where company_id = ${q(CO)};
     delete from company            where id = ${q(CO)};
     commit;`,
  );
  const left = psql(`select count(*) from company where id = ${q(CO)}`);
  rec("D83: ⛔ the probe reaped its own tenant",
      left === "0", `companies matching ${CO.slice(0, 8)}…: ${left}`);
}

process.exit(summary() > 0 ? 1 : 0);
