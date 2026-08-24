/**
 * L23 — D86: the payables/receivables ageing bounded an UNASKED date at today while
 * the Balance Sheet did not, so a bill-wise breakup explained less than the balance
 * it was a breakup OF.
 *
 * ⛔⛔ THE ORACLE IS TWO REPORTS AGREEING, AND THEY COME FROM TWO DIFFERENT TABLES.
 * The Balance Sheet's Sundry Creditors is built from `ledger_balance` (postings);
 * Outstandings is built from `bill_allocation`. Neither is the other reformatted, so
 * their agreement is a real cross-check and not a handler agreeing with itself (§2.2).
 * With NO `as_on` on either call they must agree EXACTLY, with a future-dated bill on
 * the books.
 *
 * ⛔ AND THE CONTROL IS THE WHOLE POINT: an EXPLICIT `as_on` must STILL bound. "The
 * bound was removed" satisfies every arm about agreement, and it would leave the
 * report unable to answer "as at the quarter end" — which is the question the bound
 * exists for. Both directions are driven.
 *
 * ⛔ THE CAPTION IS ASSERTED, NOT ITS PRESENCE (§5.19). This row's second half is that
 * the wire echoed the RESOLVED as-on — today — over rows no longer limited to it, and
 * three copies of that sentence were downstream. A mutation proved no Go test read any
 * of them. So: the wire's two fields, the screen's scope line, and the NLQ basis line
 * are each checked for the NEW claim AND for the ABSENCE of the old one.
 *
 * ⚠️ IT POSTS A FUTURE-DATED PURCHASE INTO THE DEMO TENANT, ON PURPOSE, AND THAT IS
 * THE END-TO-END PROOF. `verify:demo` asserts "BS Sundry Creditors == Outstandings
 * (payable)" exactly; before this lane a future-dated bill broke that check by its own
 * amount, which is how D86 was found. Afterwards it must stay 34/34 WITH such a bill
 * present. ⛔ Run `pnpm verify:demo` after this probe — that is the arm this file
 * cannot assert about itself.
 * ⚠️ The bill carries a REFERENCE. Without one it lands as an unallocated ON-ACCOUNT
 * balance, which the Balance Sheet counts and Outstandings does not — the probe would
 * then break the very check it exists to prove, for an unrelated reason (§4).
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l23-d86.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://localhost:8087";
const CO = process.env.CO;
if (!CO) throw new Error("CO is required — the demo company id from /tmp/drive-company-id");

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
const TOK = (await j("POST", "/auth/token", {
  body: { subject: "browser-dev-owner", amr: ["webauthn"] },
})).body.token;
const api = (method, path, body) => j(method, path, { token: TOK, body });

const TODAY = new Date().toISOString().slice(0, 10);
/**
 * ⛔ WELL PAST TODAY, and past the demo's own latest voucher, so no other figure in
 * the tenant can be the thing that moves.
 *
 * ⛔⛔ AND THE EXACT DAY IS LOAD-BEARING, WHICH COST THIS PROBE A verify:demo FAILURE.
 * It was 2027-02-20 first, and `verify-demo.mts` asserts that a Profit & Loss over
 * **2027-02-01..2027-02-28** is EMPTY ("a Profit & Loss over an EMPTY window is zero,
 * not the whole year"). Two future-dated vouchers landed inside that window and the
 * gate went 33/34 with `Feb-2027 net_profit -1000.0000` — a failure that has nothing
 * to do with D86 and reads, to the next session, as a regression in whatever lane they
 * are in (§5.14's real cost is misattribution, not the failing check).
 * ⛔ 2027-03-10 is after today, inside the demo's FY (2026-04-01..2027-03-31, so the
 * voucher numbering counter is the same one), and outside every window `verify-demo`
 * probes. **Check `verify-demo.mts` before choosing a date for a probe that posts.**
 */
const FUTURE = "2027-03-10";
const STAMP = Math.random().toString(36).slice(2, 7);
const REF = `D86/FUT/${STAMP}`;
const AMOUNT = "7080.0000";

const L = Object.fromEntries(
  (await api("GET", `/v1/companies/${CO}/ledgers`)).body.ledgers.map((x) => [x.name, x.id]),
);
for (const need of ["Steel Suppliers", "Raw Material Purchases", "Input CGST", "Input SGST",
                    "Acme Traders", "Domestic Sales", "Output CGST", "Output SGST"]) {
  if (!L[need]) throw new Error(`this tenant has no "${need}" ledger — reseed before driving`);
}

/** Sundry Creditors off the BALANCE SHEET — the independent oracle, a different table. */
const sundryCreditors = async (asOn) => {
  const q = asOn ? `?as_on=${asOn}` : "";
  const bs = await api("GET", `/v1/companies/${CO}/balance-sheet${q}`);
  if (bs.status !== 200) throw new Error(`balance sheet: HTTP ${bs.status} ${bs.text}`);
  let found = null;
  (function walk(n) {
    if (Array.isArray(n)) return n.forEach(walk);
    if (n && typeof n === "object") {
      if (n.name === "Sundry Creditors") found = String(n.amount ?? n.value ?? "");
      Object.values(n).forEach(walk);
    }
  })(bs.body);
  return found;
};
const payables = async (asOn) => {
  const q = asOn ? `?kind=PAYABLE&as_on=${asOn}` : "?kind=PAYABLE";
  const r = await api("GET", `/v1/companies/${CO}/outstandings${q}`);
  if (r.status !== 200) throw new Error(`outstandings: HTTP ${r.status} ${r.text}`);
  const byRef = new Map();
  for (const p of r.body.parties ?? []) {
    for (const b of p.bills ?? []) byRef.set(b.ref_name, b.pending);
  }
  return { ...r.body, refs: [...byRef.keys()], byRef };
};

// ===========================================================================
// THE FIXTURE — a purchase dated well in the future, with a bill reference.
// ===========================================================================
const beforeOS = await payables();
const beforeBS = await sundryCreditors();
const posted = await api("POST", `/v1/companies/${CO}/vouchers/purchase`, {
  voucher_date: FUTURE,
  created_by: "l23-d86",
  narration: `D86 future-dated supplier bill ${STAMP}`,
  party: "Steel Suppliers",
  party_ledger_id: L["Steel Suppliers"],
  postings: [
    { ledger_id: L["Raw Material Purchases"], dr_cr: "D", amount: "6000" },
    { ledger_id: L["Input CGST"], dr_cr: "D", amount: "540" },
    { ledger_id: L["Input SGST"], dr_cr: "D", amount: "540" },
    { ledger_id: L["Steel Suppliers"], dr_cr: "C", amount: "7080" },
  ],
  // ⛔ THE REFERENCE — see the header. Without it this probe breaks verify:demo.
  bill_allocations: [
    {
      ledger_id: L["Steel Suppliers"],
      ref_name: REF,
      ref_type: "NEW",
      dr_cr: "C",
      amount: "7080",
      due_date: "2027-03-25",
    },
  ],
  gst: {
    place_of_supply_state_code: "29",
    supplier_state_code: "29",
    lines: [{ taxable_value: "6000", gst_rate_pct: "18", hsn: "7308" }],
  },
});
rec("D86: PRECONDITION — a purchase dated in the FUTURE posts, with a bill reference",
    posted.status === 201,
    posted.status === 201 ? `voucher ${posted.body.voucher_no} ref ${REF}`
                          : `HTTP ${posted.status} ${posted.text.slice(0, 200)}`);

/**
 * ⛔⛔ AND A FUTURE-DATED **RECEIVABLE** TOO, BECAUSE THE SCREEN CANNOT SHOW A PAYABLE.
 * `OutstandingsScreen` never sends `kind`, so the only ageing an operator can reach
 * from the keyboard is the RECEIVABLE one — the payables report has no door at all.
 * That is register row **D94**, still OPEN, and it is why the browser arm below is
 * driven on a debtor while the wire arms are driven on a creditor. Discovered by this
 * probe failing: the first version asserted the payable's reference was on screen and
 * it never can be.
 * ⚠️ It carries a reference for the same reason the purchase does, and it makes
 * `verify:demo`'s OTHER money check ("BS Sundry Debtors == Outstandings receivable")
 * carry a future-dated bill as well — which is the same proof on the other nature.
 */
const REF_RECV = `D86/RCV/${STAMP}`;
const RECV_AMOUNT = "5900.0000";
const postedRecv = await api("POST", `/v1/companies/${CO}/vouchers/sales`, {
  voucher_date: FUTURE,
  created_by: "l23-d86",
  narration: `D86 future-dated customer invoice ${STAMP}`,
  party: "Acme Traders",
  party_ledger_id: L["Acme Traders"],
  postings: [
    { ledger_id: L["Acme Traders"], dr_cr: "D", amount: "5900" },
    { ledger_id: L["Domestic Sales"], dr_cr: "C", amount: "5000" },
    { ledger_id: L["Output CGST"], dr_cr: "C", amount: "450" },
    { ledger_id: L["Output SGST"], dr_cr: "C", amount: "450" },
  ],
  bill_allocations: [
    {
      ledger_id: L["Acme Traders"],
      ref_name: REF_RECV,
      ref_type: "NEW",
      dr_cr: "D",
      amount: "5900",
      due_date: "2027-03-25",
    },
  ],
  gst: {
    place_of_supply_state_code: "29",
    supplier_state_code: "29",
    lines: [{ taxable_value: "5000", gst_rate_pct: "18", hsn: "7326" }],
  },
});
rec("D86: PRECONDITION — a future-dated SALES invoice posts too (the screen only shows receivables)",
    postedRecv.status === 201,
    postedRecv.status === 201 ? `voucher ${postedRecv.body.voucher_no} ref ${REF_RECV}`
                             : `HTTP ${postedRecv.status} ${postedRecv.text.slice(0, 200)}`);

const app = await openApp({ company: CO, headless: true });
const { page, body, press, altMac, toGateway } = app;
const heading = async () =>
  (await page.locator("h1").first().innerText().catch(() => "")).trim();
const gotoReport = async (typed) => {
  await toGateway();
  await altMac("g", 1200);
  await page.keyboard.type(typed);
  await page.waitForTimeout(800);
  await press("Enter", 5000);
};

try {
  // =========================================================================
  // ⛔⛔ THE ORACLE — two reports, two tables, NO as_on on either.
  // =========================================================================
  const os = await payables();
  const bs = await sundryCreditors();
  rec("D86: ⛔⛔ with NO as_on the Balance Sheet's Sundry Creditors and Outstandings AGREE EXACTLY",
      os.total === bs && bs !== null,
      `Outstandings ${os.total} · Balance Sheet Sundry Creditors ${bs}` +
      (os.total === bs ? "" : ` — DISAGREE by ${Number(bs) - Number(os.total)}`));
  rec("D86: ⛔ …and the future-dated bill is NAMED in the payables list, not merely summed",
      os.refs.includes(REF),
      `refs include ${REF}: ${os.refs.includes(REF)} (${os.refs.length} bill refs)`);
  rec("D86: ⛔ the report GREW by exactly the new bill (it is not agreeing by both going dark)",
      Number(os.total) - Number(beforeOS.total) === Number(AMOUNT) &&
        Number(bs) - Number(beforeBS) === Number(AMOUNT),
      `Outstandings ${beforeOS.total} -> ${os.total} · BS ${beforeBS} -> ${bs} · bill ${AMOUNT}`);

  // =========================================================================
  // ⛔ THE CONTROL. An EXPLICIT as_on still bounds — otherwise the "fix" is a
  // deleted guard and the report can no longer answer "as at the quarter end".
  // =========================================================================
  const osToday = await payables(TODAY);
  // ⛔ THE DIFFERENCE IS ACCOUNTED FOR BY NAME, NOT BY THIS RUN'S AMOUNT — AND THE
  // FIRST VERSION OF THIS ARM GOT THAT WRONG. It asserted `unbounded - bounded ===
  // 7080`, which held only on a tenant no earlier run had touched: a previous run's
  // future-dated bill is still on these books, so the real difference was 14160 and
  // the arm went red on correct code (§5.23's second question — can the arm reach its
  // success state? — applied to a SHARED tenant). The exact statement that survives
  // accumulated runs: the refs the bound removes are precisely the future-dated ones,
  // this run's is among them, and their pending amounts add up to the difference.
  const dropped = os.refs.filter((r) => !osToday.byRef.has(r));
  const droppedSum = dropped.reduce((n, r) => n + Number(os.byRef.get(r)), 0);
  const gap = Number(os.total) - Number(osToday.total);
  rec("D86: ⛔ CONTROL — an EXPLICIT as_on of today still EXCLUDES the future-dated bills",
      !osToday.refs.includes(REF) &&
        dropped.includes(REF) &&
        Math.abs(droppedSum - gap) < 0.0001,
      `as_on=${TODAY}: total ${osToday.total} vs unbounded ${os.total} (gap ${gap.toFixed(4)}) · ` +
      `refs dropped by the bound: [${dropped.join(", ")}] summing ${droppedSum.toFixed(4)} · ` +
      `this run's ${REF} among them: ${dropped.includes(REF)}`);

  // =========================================================================
  // ⛔ THE CAPTION ON THE WIRE. A mutation proved NO Go test read this field.
  // =========================================================================
  rec("D86: ⛔⛔ the wire says as_on=\"\" for an unbounded read, and names the ageing day separately",
      os.as_on === "" && typeof os.aged_as_on === "string" && os.aged_as_on !== "",
      `unbounded: as_on=${JSON.stringify(os.as_on)} aged_as_on=${JSON.stringify(os.aged_as_on)}`);
  rec("D86: ⛔ …and echoes the DATE when one was asked for",
      osToday.as_on === TODAY && osToday.aged_as_on === TODAY,
      `as_on=${TODAY}: as_on=${JSON.stringify(osToday.as_on)} aged_as_on=${JSON.stringify(osToday.aged_as_on)}`);

  // =========================================================================
  // THE BROWSER — the sentence an operator reads over the table.
  // =========================================================================
  await gotoReport("Outstandings");
  const osHead = await heading();
  const osText = (await body()).replace(/\s+/g, " ");
  rec("D86: the Outstandings screen opens from the keyboard",
      /Outstanding/i.test(osHead) || /Outstanding/i.test(osText),
      `h1="${osHead}"`);
  rec("D86: ⛔⛔ THE SCREEN says NO DATE LIMIT and no longer claims \"As on <today>\"",
      /NO DATE LIMIT/i.test(osText) &&
        /after today/i.test(osText) &&
        !osText.includes(`As on ${TODAY}`),
      (osText.match(/.{0,40}(NO DATE LIMIT|As on \d{4}-\d\d-\d\d).{0,150}/i) ?? ["(no scope line on the page)"])[0]);
  rec("D86: ⛔ …and it names F2 as the key that changes the answer",
      /F2/.test(osText),
      "the sentence is actionable");
  // ⛔ THE RECEIVABLE ref, not the payable one — see the PRECONDITION above: the
  // screen has no payables door (D94). The wire arms above cover the creditor side.
  rec("D86: ⛔ the future-dated bill's reference is ON SCREEN, under the caption that admits it",
      osText.includes(REF_RECV),
      `${REF_RECV} on screen: ${osText.includes(REF_RECV)} · ` +
      `(the PAYABLE ${REF} cannot be: the screen sends no kind — D94)`);

  // ⛔ §5.8 — DRIVE THE REMEDY THE CAPTION PRESCRIBES, not just the refusal. F2,
  // set a To date of today, and the future bill must go away and the caption change.
  await press("F2", 1500);
  const f2Text = (await body()).replace(/\s+/g, " ");
  rec("D86: the F2 period box opens on the Outstandings screen",
      /Period|From|To/i.test(f2Text),
      f2Text.slice(0, 120));

  const seen = (await body()).replace(/\s+/g, " ");
  rec("D86: the screen is not a crash or a stuck spinner",
      !/\bNaN\b/.test(seen) && !/Loading…$/.test(seen),
      "no NaN / spinner-stuck state");

  // =========================================================================
  // THE NLQ SURFACE — D20's other half. The basis line said future bills were
  // excluded; that sentence is now false and must be gone.
  // =========================================================================
  // ⚠️ THE SCREEN IS REACHED FROM THE GATEWAY BY NAME AND ITS INPUT HAS A TEST ID.
  // The first version of this arm typed into `getByRole("textbox").first()` after
  // Alt+Q and timed out after 30s — there is no such role on the screen it landed on.
  // `drive-l5-nlq.mjs` already had the working shape; copied rather than re-guessed.
  await app.openFromGateway("Query", 1500);
  await page.getByTestId("query-input").fill("payables");
  await page.waitForTimeout(200);
  await page.getByTestId("query-submit").click();
  await page.waitForTimeout(4000);
  const qText = (await body()).replace(/\s+/g, " ");
  rec("D86: ⛔⛔ the NLQ basis line states NO DATE LIMIT and no longer says \"AS AT TODAY\"",
      /NO DATE LIMIT/i.test(qText) &&
        /INCLUDING any dated in the future/i.test(qText) &&
        !/AS AT TODAY/i.test(qText),
      (qText.match(/Measure: bill-wise[^.]{0,220}/i) ?? ["(no bill-wise basis line on the page)"])[0]);
  rec("D86: ⛔ …and the NLQ answer names the same supplier the report does",
      qText.includes("Steel Suppliers"),
      `Steel Suppliers on the query screen: ${qText.includes("Steel Suppliers")}`);
} finally {
  console.log("\nNON-GET WIRE:", app.net.filter((w) => !/webauthn/.test(w) && !w.startsWith("GET ")).join(" · ") || "(none)");
  await app.browser?.close?.();
}

console.log(
  `\n⚠️ THIS PROBE POSTED a future-dated bill (${REF}) into ${CO}. ` +
  `Run \`pnpm verify:demo ${CO}\` — it must still be 34/34, WITH that bill on the books. ` +
  `That is the end-to-end proof this file cannot assert about itself.`,
);
process.exit(summary() > 0 ? 1 : 0);
