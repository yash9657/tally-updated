/**
 * L14b — D77: the posting ORDER BY is not a total order, at three sites.
 *
 * ⛔ THIS IS A WIRE PROBE, NOT A BROWSER ONE. The defect is three SQL strings in
 * cored; the surfaces it reaches are the voucher GET, the day book and the
 * printed document, and all three are readable on the wire. A browser adds
 * nothing an operator could see that these three reads do not.
 *
 * ⛔⛔ THE FIXTURE IS NOT SYNTHETIC — IT IS EVERY INTRA-STATE GST INVOICE.
 * `ORDER BY p.dr_cr DESC, p.amount DESC` ties whenever two legs share a side and
 * an amount, and the CGST and SGST legs of an intra-state supply ALWAYS do: 18%
 * on 10,000 is 900 credit and 900 credit. So the exposure is not an exotic
 * hand-built voucher, it is the ordinary shape of the most common document in
 * the product. (`printing`'s copy adds the ledger NAME as a third key, which
 * separates CGST from SGST — that is why the print arm behaves differently from
 * the other two, and the register says so.)
 *
 * ⛔⛔ THE MECHANISM THAT MAKES A NON-TOTAL ORDER ACTUALLY MOVE: A HEAP REWRITE.
 * Postgres returns tied rows in whatever order the scan produced. On this schema
 * the read is an index scan over `posting_version`, so tied rows come back in
 * PHYSICAL heap order — stable across plans, across `VACUUM ANALYZE`, and across
 * `work_mem` settings (all four were tried and none moved it). What DOES move it
 * is rewriting the heap, which is exactly what `CLUSTER` and `pg_repack` do, and
 * `pg_repack` is routine production maintenance rather than an exotic event.
 *
 * So this probe rewrites the heap BETWEEN the two reads, in a way that is
 * guaranteed to change the physical order of tied rows:
 *
 *     CLUSTER posting USING posting_pkey     -> heap in id order
 *     ... read every surface ...
 *     CLUSTER posting USING posting_ledger   -> heap in ledger_id order
 *     ... read every surface again ...
 *
 * ⚠️ THE ORDER OF THE TWO CLUSTERS MATTERS AND IS DELIBERATE. Clustering by
 * `posting_pkey` LAST would leave the heap in `p.id` order, which is the very
 * order the fix's tie-break imposes — so the untied query would come out
 * accidentally correct and the probe would report green against a broken build.
 * `posting_ledger` is second precisely because ledger_id order is unrelated to
 * id order.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l14b-d77.mjs
 */
import { execFileSync } from "node:child_process";
import { rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO;
const CORED = "http://localhost:8087";
if (!CO) throw new Error("drive-l14b-d77: CO is required (the seeded company id)");

const j = async (method, path, { token, body, raw } = {}) => {
  const r = await fetch(`${CORED}${path}`, {
    method,
    headers: {
      "Content-Type": "application/json",
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const text = await r.text();
  if (raw) return { status: r.status, text };
  let parsed;
  try { parsed = text ? JSON.parse(text) : {}; } catch { parsed = { raw: text }; }
  return { status: r.status, body: parsed };
};
const TOK = (await j("POST", "/auth/token", {
  body: { subject: "browser-dev-owner", amr: ["webauthn"] },
})).body.token;
const api = (method, path, body) => j(method, path, { token: TOK, body });
const apiRaw = (method, path) => j(method, path, { token: TOK, raw: true });

/** Run one statement as `postgres`. ⛔ CLUSTER needs ownership; the app role is not the owner. */
const psql = (sql) =>
  execFileSync("psql", [
    "-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql,
  ], { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" }).trim();

const cluster = (index) => psql(`CLUSTER posting USING ${index};`);

/**
 * ⛔⛔ THE PRECONDITION THAT STOPS A GREEN RUN FROM BEING LUCK.
 *
 * The product arms below compare one read against another across a heap
 * rewrite. If the rewrite happened not to reorder THIS fixture's tied rows,
 * every arm would pass on a broken build and the probe would certify the
 * defect. Whether it reorders them is a coin toss per tied pair: `posting_pkey`
 * puts the heap in posting-id order, `posting_ledger` in ledger-id order, and
 * for any one pair those agree about half the time.
 *
 * So the probe runs the OLD, UNTIED query itself — as raw SQL, independent of
 * cored — in both heap states, and REQUIRES the two to differ. That is the
 * mechanism being live for this fixture, asserted rather than assumed.
 */
const untiedSQL = (versionID) =>
  psql(
    `SELECT string_agg(x.id::text, ',') FROM (
       SELECT p.id FROM posting p LEFT JOIN ledger l ON l.id = p.ledger_id
        WHERE p.voucher_version_id = '${versionID}'
        ORDER BY p.dr_cr DESC, p.amount DESC) x`,
  );

const STAMP = Math.random().toString(36).slice(2, 7);
const DATE = "2026-08-18";

// ===========================================================================
// FIXTURES, over the wire.
// ===========================================================================
const L = Object.fromEntries(
  (await api("GET", `/v1/companies/${CO}/ledgers`)).body.ledgers.map((x) => [x.name, x.id]),
);

// F1 — an ORDINARY intra-state sales invoice. Its CGST and SGST legs are both
// 900 CREDIT: the tie is not contrived, it is what 18% on 10,000 produces.
const inv = await api("POST", `/v1/companies/${CO}/vouchers/sales`, {
  voucher_date: DATE,
  created_by: "l14b",
  narration: `D77 intra-state invoice ${STAMP}`,
  party: "Acme Traders",
  party_ledger_id: L["Acme Traders"],
  postings: [
    { ledger_id: L["Acme Traders"], dr_cr: "D", amount: "11800" },
    { ledger_id: L["Domestic Sales"], dr_cr: "C", amount: "10000" },
    { ledger_id: L["Output CGST"], dr_cr: "C", amount: "900" },
    { ledger_id: L["Output SGST"], dr_cr: "C", amount: "900" },
  ],
  gst: {
    place_of_supply_state_code: "29",
    supplier_state_code: "29",
    lines: [{ taxable_value: "10000", gst_rate_pct: "18", hsn: "7326" }],
    cgst_ledger_id: L["Output CGST"],
    sgst_ledger_id: L["Output SGST"],
    igst_ledger_id: L["Output IGST"],
  },
});
rec("fixture: an ordinary intra-state sales invoice, CGST 900 = SGST 900 on the same side",
    inv.status === 201, `${inv.status} cgst=${inv.body.cgst} sgst=${inv.body.sgst}`);

// F2 — two equal debits to two DIFFERENT ledgers, with a cost split attached to
// one of them by `posting_seq`.
//
// ⛔⛔ THIS WAS BUILT AS THE "MONEY" ARM ON A HYPOTHESIS THE MUTATION PROOF
// DISPROVED, AND IT IS KEPT AS A CONTROL BECAUSE OF THAT. The hypothesis:
// `posting_seq` indexes the postings array, so a tied swap should make the split
// name a different ledger — invisibly, since both legs are equal and every
// per-category total would still foot. It does NOT happen. `GetVoucher` builds
// `postingSeqByID[pid]` while it walks the same ordered read and joins the
// allocations by POSTING_ID, so the seq is re-derived to point at the SAME
// posting however the array is ordered. Proved: with the fix reverted and the
// array demonstrably reordered (the arm below went red), this arm stayed GREEN.
//
// So the cost attribution is safe, and D77 is a PRESENTATION-order defect. That
// is the difference between what the register may claim and what the code does,
// and it is why this arm stays: a future reader who re-derives the hypothesis
// finds it already tested.
const cats = (await api("GET", `/v1/companies/${CO}/cost-centres`)).body;
const dept = cats.cost_categories.find((c) => c.name === "Department");
const centres = (cats.cost_centres ?? []).filter((c) => c.cost_category_id === dept?.id);
const split = await api("POST", `/v1/companies/${CO}/vouchers/journal`, {
  voucher_date: DATE,
  created_by: "l14b",
  narration: `D77 equal debits with a cost split ${STAMP}`,
  postings: [
    // ⛔ TWO DIFFERENT LEDGERS, ONE AMOUNT. The tie is on (side, amount); the
    // ledgers differ, so a swap is VISIBLE in the array and changes which head
    // the split below names. Two legs to the SAME ledger would tie too, and
    // would be invisible — which is the case the print arm is about.
    { ledger_id: L["Salaries & Wages"], dr_cr: "D", amount: "5000" },
    { ledger_id: L["Rent"], dr_cr: "D", amount: "5000" },
    { ledger_id: L["HDFC Bank"], dr_cr: "C", amount: "10000" },
  ],
  cost_allocations: centres.length >= 2
    ? [
        { posting_seq: 0, cost_category_id: dept.id, cost_centre_id: centres[0].id, amount: "3000" },
        { posting_seq: 0, cost_category_id: dept.id, cost_centre_id: centres[1].id, amount: "2000" },
      ]
    : undefined,
});
rec("fixture: a journal with two EQUAL debits to different ledgers, one carrying a cost split",
    split.status === 201,
    `${split.status} ${centres.length} Department centres ${JSON.stringify(split.body).slice(0, 90)}`);

// ===========================================================================
// THE READS. One function per SITE, so each is proved separately.
// ===========================================================================
/** ledger.go — GetVoucher's postings projection, as the array an operator's screen is built from. */
const readVoucher = async (id) => {
  const v = (await api("GET", `/v1/companies/${CO}/vouchers/${id}`)).body;
  // ⚠️ THE FIELD IS `ledger`, NOT `ledger_name`. The first version of this probe
  // read `p.ledger_name`, got `undefined` for every leg, and its "the array is
  // element-identical" assertion then compared SIDE AND AMOUNT ONLY — i.e. it
  // compared the two things that are equal BY CONSTRUCTION in a tie and dropped
  // the one thing a swap changes. It passed 7/7 while asserting nothing.
  const legs = (v.postings ?? []).map((p) => `${p.dr_cr} ${p.ledger} ${p.amount}`);
  if (legs.some((l) => l.includes("undefined"))) {
    throw new Error(`drive-l14b-d77: a posting projected \`undefined\` — the field names moved: ${legs.join(" | ")}`);
  }
  return {
    postings: legs,
    // ⛔ RESOLVED THROUGH THE ARRAY, which is the whole point: `posting_seq` is
    // meaningless except as an index into the postings THIS read returned, and
    // the server RE-DERIVES it per read (CostAllocationView says so at the
    // field). Observed on the fixture: the payload sent seq 0 for Salaries &
    // Wages and the read returned seq 1, because the read's own ordering puts
    // it second. Tie the array and the split follows the tie.
    splits: (v.cost_allocations ?? []).map((a) => {
      const at = (v.postings ?? [])[a.posting_seq];
      if (!at) {
        throw new Error(`drive-l14b-d77: posting_seq ${a.posting_seq} is out of range for ${legs.length} legs — the split arm would assert nothing`);
      }
      return `${a.cost_centre_name}@${at.ledger}=${a.amount}`;
    }),
  };
};
/** daybook.go — loadPostings, which GetVoucher's comment promises to match. */
const readDayBook = async (narrationMatch) => {
  const entries = (await api("GET", `/v1/companies/${CO}/day-book?from=${DATE}&to=${DATE}`)).body.entries ?? [];
  const mine = entries.filter((e) => (e.narration ?? "").includes(narrationMatch));
  return mine.map((e) => (e.postings ?? []).map((p) => `${p.dr_cr} ${p.ledger} ${p.amount}`).join(" | "));
};
/** document.go — loadLegs, read as the bytes that reach the paper. */
const readPrint = async (id) => {
  const r = await apiRaw("GET", `/v1/companies/${CO}/print/vouchers/sales/${id}`);
  return `${r.status}:${r.text}`;
};

const snapshot = async () => ({
  voucher: await readVoucher(inv.body.voucher_id),
  split: await readVoucher(split.body.voucher_id),
  dayBook: await readDayBook(STAMP),
  print: await readPrint(inv.body.voucher_id),
});

console.log(`\n── D77: reading every surface across a HEAP REWRITE ──────────`);
const invVersion = psql(
  `SELECT id FROM voucher_version WHERE voucher_id = '${inv.body.voucher_id}' ORDER BY version_no DESC LIMIT 1`);
const splitVersion = psql(
  `SELECT id FROM voucher_version WHERE voucher_id = '${split.body.voucher_id}' ORDER BY version_no DESC LIMIT 1`);

cluster("posting_pkey");
const untiedInvBefore = untiedSQL(invVersion);
const untiedSplitBefore = untiedSQL(splitVersion);
const before = await snapshot();
console.log(`  before: postings ${JSON.stringify(before.voucher.postings)}`);
console.log(`  before: splits   ${JSON.stringify(before.split.splits)}`);
cluster("posting_ledger");
const untiedInvAfter = untiedSQL(invVersion);
const untiedSplitAfter = untiedSQL(splitVersion);
const after = await snapshot();
console.log(`  after:  postings ${JSON.stringify(after.voucher.postings)}`);
console.log(`  after:  splits   ${JSON.stringify(after.split.splits)}`);

const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

// ---- PRECONDITION. ⛔ If this fails, every arm below is vacuous — so it is an
// ARM, not a silent assumption, and it fails LOUDLY rather than reporting green.
rec("D77 · ⛔ PRECONDITION — the heap rewrite really does reorder this fixture's TIED rows",
    untiedInvBefore !== untiedInvAfter || untiedSplitBefore !== untiedSplitAfter,
    `invoice untied: ${untiedInvBefore === untiedInvAfter ? "UNCHANGED" : "CHANGED"} · ` +
    `journal untied: ${untiedSplitBefore === untiedSplitAfter ? "UNCHANGED" : "CHANGED"}\n` +
    `         (if BOTH are UNCHANGED the fixture is not exposed and nothing below means anything)`);

// ---- SITE 1: ledger.go
rec("D77 · ledger.go: ⛔ the voucher's postings array is ELEMENT-IDENTICAL across a heap rewrite",
    same(before.voucher.postings, after.voucher.postings),
    `before ${JSON.stringify(before.voucher.postings)}\n         after  ${JSON.stringify(after.voucher.postings)}`);
rec("D77 · ledger.go: ⛔ CONTROL — the cost split names the same ledger EVEN ON A BROKEN BUILD (keyed by posting_id, not position)",
    same(before.split.splits, after.split.splits) && before.split.splits.length > 0,
    `before ${JSON.stringify(before.split.splits)}\n         after  ${JSON.stringify(after.split.splits)}`);
rec("D77 · ledger.go: and the equal-debit journal's own array is element-identical",
    same(before.split.postings, after.split.postings),
    `before ${JSON.stringify(before.split.postings)}\n         after  ${JSON.stringify(after.split.postings)}`);

// ---- SITE 2: daybook.go
rec("D77 · daybook.go: ⛔ the day book's postings are ELEMENT-IDENTICAL across a heap rewrite",
    same(before.dayBook, after.dayBook) && before.dayBook.length > 0,
    `before ${JSON.stringify(before.dayBook)}\n         after  ${JSON.stringify(after.dayBook)}`);

// ---- SITE 3: document.go
// ⚠️ PREDICTED TO HOLD EVEN ON A BROKEN BUILD, and that is recorded rather than
// hidden. `loadLegs` already tie-broke on the ledger NAME, and a leg that ties
// on side+amount+name also ties on ledger_id and group (ledger names are unique
// per company), so the two rows are identical in EVERY projected column and
// swapping them cannot change a byte. Appending p.id makes the comment's
// "chosen to be total" claim TRUE; it does not change an output. This arm is
// here as the CONTROL that says so out loud.
rec("D77 · document.go: the printed document is BYTE-IDENTICAL across a heap rewrite",
    before.print === after.print,
    `${before.print.length} vs ${after.print.length} bytes, equal=${before.print === after.print}`);

console.log(`\n(stamp ${STAMP})`);
process.exit(summary() > 0 ? 1 : 0);
