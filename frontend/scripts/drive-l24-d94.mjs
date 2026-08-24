/**
 * L24 — D94: the payables ageing had NO DOOR, and no ACL rule could name it.
 *
 * ⛔⛔ THE SECOND ARM IS THE POINT OF THE ROW AND THE FIRST ARM IS NOT. A keyboard path
 * to the payables book proves a door exists; a `?kind=` toggle on the existing screen
 * would have proved that too, in an afternoon, and left the real defect untouched. The
 * arm that only a SEPARATE REPORT NAME can pass is: a level carrying
 * `disallow display report:bills-payable` is REFUSED the payables ageing **while still
 * being ALLOWED the receivables one**. That rule was unwritable before this row,
 * because `routeAction` maps the flat literal path to the PDP resource and a query
 * parameter never reaches the decision.
 *
 * ⛔ AND THE CONTROLS MATTER MORE THAN THE REFUSALS HERE (§5.23). A 403 can be produced
 * by a dozen things — an unclaimed invite, a wrong token, a level that reads nothing at
 * all. So the same token is proved to be a MEMBER, to read the Trial Balance, and to
 * read the RECEIVABLES ageing, before its payables refusal is worth anything.
 *
 * ⚠️ IT MINTS ITS OWN TENANT AND REAPS IT (D91/L17 pattern), for the usual reason and
 * one more: it writes an ACL rule on a security level, which is a mutation of a
 * tenant's security posture that must never be left on shared books.
 *
 * ⚠️ NOTE WHAT IS **NOT** SEEDED. This row ships NO counterweight migration: a seeded
 * Data Entry level can still read the payables book by default. The rule is WRITABLE,
 * not written — so this probe WRITES it, which is exactly how a tenant that wants the
 * split would.
 *
 * ✅ **AND ITS RESIDUAL ARM HAS SINCE INVERTED — D97 IS CLOSED (L26).** When this
 * drive was written, the denied clerk was 200 on `?kind=PAYABLE` and the arm below
 * said so. The bill-wise picker was the only thing that needed that read, and it now
 * has a door of its own at `/vouchers/{class}/open-bills/{ledgerID}` authorised as
 * `display <class>`, so the parameter was retired: it answers a 308 to
 * `/bills-payable`, where the PDP refuses the clerk. The arm now asserts the 403 and
 * is the same experiment either way — which is why it was left in place and inverted
 * rather than deleted.
 *
 * RUN:  node frontend/scripts/drive-l24-d94.mjs        (takes NO CO — it makes one)
 */
import { execFileSync } from "node:child_process";

import { openApp, rec, summary } from "./audit-harness.mjs";

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
const OWNER_SUB = `d94-owner-${stamp}`;
const OWNER_PW = `d94-owner-pw-2026`;
const CLERK = `d94-clerk-${stamp}`;
const CLERK_PW = `d94-clerk-pw-2026`;
const COMPANY_NAME = `D94 Probe Co ${stamp}`;

const OWNER = await mint(OWNER_SUB);
{
  const pw = await j("POST", "/auth/password", {
    token: OWNER, body: { current_password: "", new_password: OWNER_PW },
  });
  if (pw.status !== 204) throw new Error(`set owner password: ${pw.status} ${pw.text}`);
  const r = await j("POST", "/v1/companies", {
    token: OWNER,
    body: { id: CO, name: COMPANY_NAME, fy_start_date: "2026-04-01", base_currency: "INR", state_code: "29" },
  });
  if (r.status !== 201) throw new Error(`create company: ${r.status} ${r.text}`);
}
const base = `/v1/companies/${CO}`;
console.log(`\n── tenant ${CO}  ·  owner ${OWNER_SUB}  ·  clerk ${CLERK} ──`);

// ---------------------------------------------------------------------------
// BOOKS: one customer bill and one supplier bill, so BOTH ageings have a row and
// "the payables list is empty" cannot be mistaken for "the payables list is refused".
// ---------------------------------------------------------------------------
const L = {};
const mkLedger = async (name, group) => {
  const r = await j("POST", `${base}/ledgers`, {
    token: OWNER, body: { name, group_name: group, state_code: "29" },
  });
  if (r.status !== 201) throw new Error(`create ledger ${name}: ${r.status} ${r.text}`);
  return r.body.ledger_id ?? r.body.id;
};
L.customer = await mkLedger("D94 Customer", "Sundry Debtors");
L.supplier = await mkLedger("D94 Supplier", "Sundry Creditors");
L.sales = await mkLedger("D94 Sales", "Sales Accounts");
L.purchases = await mkLedger("D94 Purchases", "Purchase Accounts");

const post = async (cls, body) => {
  const r = await j("POST", `${base}/vouchers/${cls}`, { token: OWNER, body });
  if (r.status !== 201) throw new Error(`post ${cls}: ${r.status} ${r.text}`);
  return r.body;
};
await post("sales", {
  voucher_date: "2026-06-10", created_by: OWNER_SUB, party: "D94 Customer",
  party_ledger_id: L.customer,
  postings: [
    { ledger_id: L.customer, dr_cr: "D", amount: "12000" },
    { ledger_id: L.sales, dr_cr: "C", amount: "12000" },
  ],
  bill_allocations: [{ ledger_id: L.customer, ref_name: `D94/RCV/${stamp}`, ref_type: "NEW", dr_cr: "D", amount: "12000", due_date: "2026-07-10" }],
});
await post("purchase", {
  voucher_date: "2026-06-12", created_by: OWNER_SUB, party: "D94 Supplier",
  party_ledger_id: L.supplier,
  postings: [
    { ledger_id: L.purchases, dr_cr: "D", amount: "9000" },
    { ledger_id: L.supplier, dr_cr: "C", amount: "9000" },
  ],
  bill_allocations: [{ ledger_id: L.supplier, ref_name: `D94/PAY/${stamp}`, ref_type: "NEW", dr_cr: "C", amount: "9000", due_date: "2026-07-12" }],
});

// ---------------------------------------------------------------------------
// THE WIRE — the new door serves the payables book, and it is the SAME report.
// ---------------------------------------------------------------------------
const payableViaKind = await j("GET", `${base}/outstandings?kind=PAYABLE`, { token: OWNER });
const payableViaName = await j("GET", `${base}/bills-payable`, { token: OWNER });
rec("D94: ⛔ the new FLAT route serves the payables ageing",
    payableViaName.status === 200 && payableViaName.body.kind === "PAYABLE",
    `GET /bills-payable = ${payableViaName.status} kind=${payableViaName.body.kind} total=${payableViaName.body.total}`);
rec("D94: ⛔ …and it is the SAME report, party for party — one writer, two doors",
    payableViaKind.status === 200 &&
      JSON.stringify(payableViaKind.body.parties) === JSON.stringify(payableViaName.body.parties) &&
      payableViaKind.body.total === payableViaName.body.total,
    `?kind=PAYABLE total=${payableViaKind.body.total} · /bills-payable total=${payableViaName.body.total}`);
// ⛔ THE ROUTE **IS** THE KIND. A ?kind=RECEIVABLE on it must not hand back the
// receivables list from under the payables rule.
const tricked = await j("GET", `${base}/bills-payable?kind=RECEIVABLE`, { token: OWNER });
rec("D94: ⛔ a ?kind=RECEIVABLE on the payables route is IGNORED, not honoured",
    tricked.status === 200 && tricked.body.kind === "PAYABLE",
    `GET /bills-payable?kind=RECEIVABLE -> kind=${tricked.body.kind} (honouring it would leak the receivables list under the payables rule)`);
rec("D94: ⛔ the printed form answers too, from the same layout",
    (await j("GET", `${base}/print/reports/bills-payable`, { token: OWNER })).status === 200,
    "GET /print/reports/bills-payable");

// ---------------------------------------------------------------------------
// ⛔⛔ ARM (b) — THE RULE THAT WAS UNWRITABLE. A level denied the payables book
// while still allowed the receivables one.
// ---------------------------------------------------------------------------
const levelID = await (async () => {
  const r = await j("POST", `${base}/security-levels`, {
    token: OWNER,
    body: {
      name: `Sales Clerk ${stamp}`,
      default_access: "none",
      rules: [
        // Everything this level needs to open the product and read the receivables book…
        { effect: "allow", action: "display", resource: "report" },
        // …and the ONE refusal this row exists to make writable. At specificity 2 it
        // beats the coarse allow above; before D94 there was no name to put here.
        { effect: "disallow", action: "display", resource: "report:bills-payable" },
        // ⛔⛔ AND THE PAYMENT CLASS, WHICH IS WHAT MAKES THIS LEVEL THE ONE D97 IS
        // ABOUT (added L26). The tenant that wants this split is not banning a
        // clerk from paying suppliers — it is saying "pay them, but you may not
        // read the whole payables book". Without these three the level cannot key
        // a Payment at all, and the picker arm below would assert that a clerk who
        // cannot pay anybody should still be handed a supplier's open bills.
        //
        // ⚠️ `sales` IS DELIBERATELY LEFT OFF, and that absence is the negative
        // control the picker arms use: same token, two classes, one held and one
        // not. It is what distinguishes "the class is the authority" from "the new
        // route is a free pass".
        { effect: "allow", action: "create", resource: "payment" },
        { effect: "allow", action: "alter", resource: "payment" },
        { effect: "allow", action: "display", resource: "payment" },
      ],
    },
  });
  if (r.status !== 201) throw new Error(`create level: ${r.status} ${r.text}`);
  return r.body.security_level_id ?? r.body.id;
})();

const CLERK_TOK = await (async () => {
  const tok = await mint(CLERK);
  const pw = await j("POST", "/auth/password", { token: tok, body: { current_password: "", new_password: CLERK_PW } });
  if (pw.status !== 204) throw new Error(`set clerk password: ${pw.status} ${pw.text}`);
  const inv = await j("POST", `${base}/members/invites`, {
    token: OWNER, body: { subject: CLERK, security_level_id: levelID },
  });
  if (inv.status !== 201) throw new Error(`invite: ${inv.status} ${inv.text}`);
  const me = await j("GET", "/v1/me", { token: tok });
  if (me.status !== 200) throw new Error(`claim via /v1/me: ${me.status} ${me.text}`);
  if ((me.body.claimed_invites ?? []).length !== 1) {
    throw new Error(`claimed_invites = ${(me.body.claimed_invites ?? []).length}, want 1`);
  }
  return tok;
})();

// ⛔ THE CONTROLS COME FIRST. Without them a 403 proves nothing at all.
const clerkRecv = await j("GET", `${base}/outstandings`, { token: CLERK_TOK });
rec("D94: ⛔ CONTROL — the clerk IS a member and reads the RECEIVABLES ageing",
    clerkRecv.status === 200,
    `GET /outstandings = ${clerkRecv.status} (a 403 here means the invite was never ` +
    `claimed, and every refusal below would be vacuous)`);
rec("D94: ⛔ CONTROL — …and the receivables book actually has a row in it",
    (clerkRecv.body.parties ?? []).length === 1,
    `${(clerkRecv.body.parties ?? []).length} party row(s) — an empty report cannot ` +
    `distinguish "allowed" from "allowed and blank"`);

const clerkPay = await j("GET", `${base}/bills-payable`, { token: CLERK_TOK });
rec("D94: ⛔⛔ THE ROW — the clerk is REFUSED the payables ageing, by NAME",
    clerkPay.status === 403,
    `GET /bills-payable = ${clerkPay.status} ${clerkPay.text.slice(0, 120)}`);
const clerkPayPrint = await j("GET", `${base}/print/reports/bills-payable`, { token: CLERK_TOK });
rec("D94: ⛔ …and the PRINTED form is refused too, or the refusal has a paper trail",
    clerkPayPrint.status === 403,
    `GET /print/reports/bills-payable = ${clerkPayPrint.status}`);

// ✅⛔ THE RESIDUAL, NOW CLOSED — THIS ARM IS INVERTED FROM WHAT IT ASSERTED WHEN IT
// REGISTERED D97, AND IT IS THE SAME EXPERIMENT. It used to demand a 200 here and get
// one: the payables book served from under `report:outstandings`, to a clerk who had
// just been refused `report:bills-payable` at its own name. `fetch` follows the 308,
// so what this measures is the END of that journey — the PDP's verdict on
// `/bills-payable`, which is a 403. The rule now governs the BYTES and not just the
// door.
const bypass = await j("GET", `${base}/outstandings?kind=PAYABLE`, { token: CLERK_TOK });
rec("D97: ⛔⛔ THE ROW — ?kind=PAYABLE no longer answers under the OTHER name",
    bypass.status === 403,
    `GET /outstandings?kind=PAYABLE as the DENIED clerk = ${bypass.status}` +
    (bypass.status === 200
      ? ` total=${bypass.body.total} — THE LEAK IS BACK: the rule governs the door, not the data`
      : " (308 -> /bills-payable -> refused by the PDP, where every refusal belongs)"));
// ⛔ AND THE PAPER DOOR WITH IT. Closing the JSON one and leaving this open is a
// refusal fully present and fully wrong: the same rows, on a filed sheet.
const bypassPrint = await j("GET", `${base}/print/reports/outstandings?kind=PAYABLE`, { token: CLERK_TOK });
rec("D97: ⛔ …and neither does the PRINTED form",
    bypassPrint.status === 403,
    `GET /print/reports/outstandings?kind=PAYABLE as the DENIED clerk = ${bypassPrint.status}`);
// ⛔⛔ THE CONTROL THAT MAKES BOTH REFUSALS MEAN SOMETHING: the clerk KEEPS THE
// PICKER. If this were 403 too, the row would be "closed" by taking away the read a
// Payment needs — and every settlement that clerk keys would land ON ACCOUNT, the
// balance nobody can chase. That is the failure the whole design decision avoids.
//
// ⛔ THIS CLERK HOLDS `display payment` AND IS DENIED `report:bills-payable`. That
// pair is the entire point: the two authorities are now separable, and the one that
// governs settling a bill is the one that governs keying the voucher.
const picker = await j("GET",
    `${base}/vouchers/payment/open-bills/${L.supplier}?kind=PAYABLE`, { token: CLERK_TOK });
rec("D97: ⛔⛔ CONTROL — the SAME clerk still lists that supplier's open bills to settle one",
    picker.status === 200 && Array.isArray(picker.body.bills),
    `GET /vouchers/payment/open-bills/{supplier}?kind=PAYABLE = ${picker.status}` +
    (picker.status === 200 ? ` bills=${picker.body.bills.length} party=${JSON.stringify(picker.body.ledger)}` : ""));
// ⛔⛔ AND THE NEGATIVE CONTROL, ON THE SAME TOKEN AND THE SAME LEDGER: a class this
// level does NOT hold. Without it the arm above would read equally well as "the new
// route lets anybody in", which is a worse leak than the one being closed. The ONLY
// difference between these two requests is the class segment — which is the claim.
const pickerDenied = await j("GET",
    `${base}/vouchers/sales/open-bills/${L.supplier}?kind=PAYABLE`, { token: CLERK_TOK });
rec("D97: ⛔⛔ …but NOT under a class it does not hold — the class IS the authority",
    pickerDenied.status === 403,
    `same token, same ledger, class=sales (not granted) = ${pickerDenied.status} ` +
    `vs class=payment (granted) = ${picker.status}`);
// ⛔ AND IT IS THE PAYABLES DATA — the very rows the clerk was just refused as a
// REPORT. An arm that only proved "200" would pass on an empty body.
rec("D97: ⛔ …and the bill it lists IS the supplier bill the REPORT refused it",
    picker.status === 200 && picker.body.bills.some((b) => b.ref_name === `D94/PAY/${stamp}`),
    `bills = ${picker.status === 200 ? JSON.stringify(picker.body.bills.map((b) => b.ref_name)) : "n/a"} ` +
    `(the same D94/PAY/${stamp} that /bills-payable answered 403 for)`);
// ⛔ AND WHAT IT NO LONGER CARRIES. The picker used to receive the WHOLE book —
// every party's bills and the ageing totals — to render one supplier's list. A door
// that still handed those over would be the same disclosure wearing a new path.
rec("D97: ⛔ …and that read carries NO ageing totals and NO other party",
    picker.status === 200 && picker.body.slab_totals === undefined &&
      picker.body.total === undefined && picker.body.parties === undefined,
    `keys = ${picker.status === 200 ? Object.keys(picker.body).sort().join(",") : "n/a"}`);

// ---------------------------------------------------------------------------
// ARM (a) — A KEYBOARD PATH, IN A REAL BROWSER, as the OWNER.
// ---------------------------------------------------------------------------
const app = await openApp({
  company: CO, headless: true, expectCompany: COMPANY_NAME,
  subject: OWNER_SUB, password: OWNER_PW,
});
const { page, body, press, altMac, toGateway } = app;
try {
  const heading = async () =>
    (await page.locator("h1").first().innerText().catch(() => "")).trim();
  await toGateway();
  await altMac("g", 1200);
  // ⛔ TYPED AS AN OPERATOR WOULD — the words for what the company owes. This phrase
  // opened the RECEIVABLE report until this row: `REPORT_ALIASES` mapped it there.
  await page.keyboard.type("bills payable");
  await page.waitForTimeout(800);
  await press("Enter", 5000);

  const head = await heading();
  const text = (await body()).replace(/\s+/g, " ");
  rec("D94: ⛔⛔ typing \"bills payable\" reaches the PAYABLES book, not the receivables one",
      /Bills Payable/i.test(head),
      `h1="${head}" (before this row this phrase opened "Outstandings" — the RECEIVABLE report)`);
  rec("D94: ⛔ the screen shows the SUPPLIER and its bill, and NOT the customer",
      text.includes("D94 Supplier") && text.includes(`D94/PAY/${stamp}`) &&
        !text.includes("D94 Customer"),
      `supplier=${text.includes("D94 Supplier")} bill=${text.includes(`D94/PAY/${stamp}`)} ` +
      `customer-absent=${!text.includes("D94 Customer")}`);
  rec("D94: ⛔ …and the figure on screen matches ?kind=PAYABLE on the wire",
      text.includes("9,000.00"),
      `wire total ${payableViaName.body.total} · "9,000.00" on screen: ${text.includes("9,000.00")}`);
  rec("D94: it carries D86's scope caption too — one sentence, not a second copy",
      /NO DATE LIMIT/i.test(text),
      (text.match(/.{0,30}NO DATE LIMIT.{0,80}/i) ?? ["(no scope line)"])[0]);
  rec("D94: the screen is not a crash or a stuck spinner",
      !/\bNaN\b/.test(text) && !/Loading…$/.test(text),
      "no NaN / spinner-stuck state");
} finally {
  await app.browser?.close?.();
}

// ---------------------------------------------------------------------------
// ⛔ REAP (§5.14). This tenant has BOOKS and a hand-written ACL rule.
// ---------------------------------------------------------------------------
{
  const q = (s) => `'${String(s).replace(/'/g, "''")}'`;
  // ⛔ `posting` and `voucher_version` carry *_no_mutate triggers that RAISE on DELETE.
  // `SET LOCAL session_replication_role = replica` is TRANSACTION-LOCAL and reverts at
  // COMMIT; it is permissible ONLY because this tenant was minted by this probe minutes
  // ago and its books are the four vouchers it wrote (§5.26). NEVER against keyed books.
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
  rec("D94: ⛔ the probe reaped its own tenant, including the ACL rule it wrote",
      left === "0", `companies matching ${CO.slice(0, 8)}…: ${left}`);
}

process.exit(summary() > 0 ? 1 : 0);
