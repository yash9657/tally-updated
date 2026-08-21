/**
 * L5 — NL QUERY. D20 (the NL answer did not match the Outstandings report),
 * D21 ("last month/quarter/year" silently answered for the CURRENT period),
 * D50 (a cross-tenant question was not refused, and the refusal that existed
 * was worded as "could not understand the question"), D51 ("total sales"
 * summed taxable_value only — no GST, no credit notes).
 *
 * ⛔ THE ORACLE IS THE REPORT, NEVER THE NL ANSWER'S OWN PLAUSIBILITY. Every
 * money assertion here re-fetches the report the answer CLAIMS to agree with —
 * Outstandings for D20, the Profit & Loss for D21/D51 — and compares to the
 * paisa. An NL surface that is internally consistent and disagrees with the
 * books is exactly the defect; a probe that only checked the NL answer looked
 * green throughout the period all four rows were open.
 *
 * ⛔ NOTHING HERE IS HARD-CODED FROM THE SEED (gotcha #1). Company name, ledger
 * names and every figure are resolved at run time. The DATE WINDOWS are the one
 * exception, and deliberately so: the probe computes the expected quarter/month/
 * FY itself from `new Date()` — that is the INDEPENDENT half of D21. It asserts
 * the screen DISCLOSES that window, and then fetches the P&L FOR THE DISCLOSED
 * WINDOW. A translator that answered the wrong period fails on the disclosure;
 * a screen that disclosed a window it did not use fails on the figure.
 *
 * ⛔ THIS PROBE NEVER WRITES TO THE BOOKS. The query core is read-only, so the
 * lane needs no mutation of the tenant and cannot leave `verify:demo` red
 * (gotcha #4). The ONE write it makes is a second, EMPTY company used to name a
 * real other tenant by name — it is created, never posted to, and never becomes
 * the signed-in scope.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l5-nlq.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO;
const CORED = "http://localhost:8087";
if (!CO) throw new Error("drive-l5-nlq: CO is required (the seeded company id)");

// ---------------------------------------------------------------------------
// Wire helpers — cored's own surface, no client, no mock.
// ---------------------------------------------------------------------------
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
  try {
    parsed = text ? JSON.parse(text) : {};
  } catch {
    parsed = { raw: text };
  }
  return { status: r.status, body: parsed };
};

const devToken = async (subject) =>
  (await j("POST", "/auth/token", { body: { subject, amr: ["webauthn"] } })).body.token;

const TOK = await devToken("browser-dev-owner");
const api = (method, path, body) => j(method, path, { token: TOK, body });
const ask = (nl) => api("POST", `/v1/companies/${CO}/query`, { nl });

// ---------------------------------------------------------------------------
// Money as INTEGER PAISA-of-four-decimals. Never parseFloat a rupee: the server
// speaks StringFixed(4) end to end and a float comparison would pass on a
// rounding that a ledger would not.
// ---------------------------------------------------------------------------
const fixed4 = (s) => {
  const t = String(s ?? "0").trim();
  const neg = t.startsWith("-");
  const [i, f = ""] = t.replace(/^-/, "").split(".");
  return (neg ? -1n : 1n) * BigInt(i || "0") * 10000n + (neg ? -1n : 1n) * BigInt((f + "0000").slice(0, 4));
};
const money = (n) => {
  const neg = n < 0n;
  const a = neg ? -n : n;
  return `${neg ? "-" : ""}${a / 10000n}.${String(a % 10000n).padStart(4, "0")}`;
};

// ---------------------------------------------------------------------------
// THE INDEPENDENT CALENDAR. Computed here, not read from the product — this is
// what makes the D21 assertions an oracle rather than an echo.
// ---------------------------------------------------------------------------
const iso = (d) => d.toISOString().slice(0, 10);
const utc = (y, m, day) => new Date(Date.UTC(y, m, day));
const TODAY = new Date();
const Y = TODAY.getUTCFullYear();
const M = TODAY.getUTCMonth(); // 0-11
const FY_START_MONTH = 3; // April, the Indian default the server applies

const monthWindow = (back) => {
  const from = utc(Y, M - back, 1);
  return [iso(from), iso(utc(from.getUTCFullYear(), from.getUTCMonth() + 1, 0))];
};
const quarterWindow = (back) => {
  const q = Math.floor(M / 3);
  const from = utc(Y, q * 3 - 3 * back, 1);
  return [iso(from), iso(utc(from.getUTCFullYear(), from.getUTCMonth() + 3, 0))];
};
const fyWindow = (back) => {
  const y = (M < FY_START_MONTH ? Y - 1 : Y) - back;
  return [iso(utc(y, FY_START_MONTH, 1)), iso(utc(y + 1, FY_START_MONTH, 0))];
};

const WIN = {
  "this month": monthWindow(0),
  "last month": monthWindow(1),
  "this quarter": quarterWindow(0),
  "last quarter": quarterWindow(1),
  "this year": fyWindow(0),
  "last year": fyWindow(1),
};

// ---------------------------------------------------------------------------
// THE REPORT ORACLES.
// ---------------------------------------------------------------------------
/** The P&L root-group figure ("Sales Accounts" / "Purchase Accounts") for a window. */
const plGroup = async (group, [from, to]) => {
  const r = await api("GET", `/v1/companies/${CO}/profit-and-loss?from=${from}&to=${to}`);
  if (r.status !== 200) throw new Error(`profit-and-loss ${from}..${to} -> ${r.status}`);
  for (const section of ["income", "expenses"]) {
    for (const n of r.body[section]?.nodes ?? []) {
      if (n.name === group) return fixed4(n.amount);
    }
  }
  return 0n; // the group had no movement in the window
};

/** The Outstandings report, as {total, byLedger} for one orientation. */
const outstandings = async (kind) => {
  const r = await api("GET", `/v1/companies/${CO}/outstandings?kind=${kind}`);
  if (r.status !== 200) throw new Error(`outstandings ${kind} -> ${r.status}`);
  const byLedger = new Map();
  for (const p of r.body.parties ?? []) byLedger.set(p.ledger, fixed4(p.pending));
  return { total: fixed4(r.body.total), byLedger, asOn: r.body.as_on };
};

const companyName = (await api("GET", `/v1/companies/${CO}`)).body.name;

// ===========================================================================
// PART 1 — THE WIRE. D50's scope clause.
// ===========================================================================
console.log(`\n── WIRE: D50, scope selection ────────────────────────────────`);

for (const nl of [
  "total sales for every company",
  "receivables across all tenants",
  "closing balances for all companies",
  `closing balances for company ${CO}`,
]) {
  const r = await ask(nl);
  const msg = r.body.error ?? "";
  rec(
    `refused, and IN WORDS: ${JSON.stringify(nl)}`,
    r.status === 422 && /another company/i.test(msg),
    `${r.status} — ${msg.slice(0, 90)}`,
  );
}

// The CONTRAST. A refusal message every 422 carries says nothing; this proves
// the scope wording is discriminating and not blanket.
{
  const r = await ask("what is the meaning of life");
  rec(
    "an ordinary unrecognised question keeps the generic wording",
    r.status === 422 && !/another company/i.test(r.body.error ?? ""),
    `${r.status} — ${(r.body.error ?? "").slice(0, 70)}`,
  );
}

// A question about the caller's OWN books that merely contains "company" must
// still be answered — the refusal list must not swallow the legitimate case.
{
  const r = await ask("my company's total sales");
  rec("a question about MY OWN company is still answered", r.status === 200, `${r.status}`);
}

// ---------------------------------------------------------------------------
// ⛔ THE BY-NAME CASE. A REAL second tenant, named by its REAL name.
// This is the arm the handoff expected to be refused and it is NOT: the
// translator carries no tenant data by design (no company names are ever put
// in the prompt), so it cannot tell "Zenith Exports" the tenant from "Zenith
// Exports" the customer, and refusing every proper noun would break
// "sales to <customer>". The answer is therefore given UNDER THE SCOPE CAPTION,
// asserted in the browser below. Recorded here so the ledger states which arm
// refuses and which discloses, rather than implying all four refuse.
// ---------------------------------------------------------------------------
const OTHER_NAME = `Zenith Exports ${Math.random().toString(36).slice(2, 7)} Pvt Ltd`;
{
  const created = await api("POST", "/v1/companies", {
    name: OTHER_NAME,
    fy_start_date: "2026-04-01",
    base_currency: "INR",
    state_code: "27",
  });
  rec("a SECOND real tenant exists to name", created.status === 201, `${created.status}`);

  const r = await ask(`total sales for ${OTHER_NAME}`);
  const ownFY = await plGroup("Sales Accounts", WIN["this year"]);
  const answered = (r.body.rows ?? []).reduce((a, row) => a + fixed4(row[1]), 0n);
  rec(
    "naming another tenant by name is ANSWERED (200) with THIS company's figure — by design, see the scope caption",
    r.status === 200 && answered === ownFY,
    `${r.status}, answered ${money(answered)} = own P&L sales ${money(ownFY)}`,
  );
}

// ===========================================================================
// PART 2 — THE BROWSER.
// ===========================================================================
const app = await openApp({ company: CO });
const { page, body } = app;

/** Ask a question on the screen and read everything it states back. */
const askOnScreen = async (nl) => {
  await app.toGateway();
  await app.openFromGateway("Query", 1200);
  const input = page.getByTestId("query-input");
  await input.click();
  await input.fill(nl);
  await page.getByTestId("query-submit").click();
  for (let i = 0; i < 24; i++) {
    if (
      (await page.getByTestId("query-basis").count()) ||
      (await page.getByTestId("query-error").count())
    ) {
      break;
    }
    await page.waitForTimeout(250);
  }
  await page.waitForTimeout(200);
  const txt = async (tid) =>
    (await page.getByTestId(tid).count())
      ? (await page.getByTestId(tid).first().innerText()).replace(/\s+/g, " ").trim()
      : "";
  const rows = await page.getByTestId("query-row").evaluateAll((trs) =>
    trs.map((tr) => Array.from(tr.querySelectorAll("td")).map((td) => td.innerText.trim())),
  );
  return {
    error: await txt("query-error"),
    scope: await txt("query-scope"),
    period: await txt("query-basis-period"),
    measure: (await page.getByTestId("query-basis-measure").allInnerTexts()).join(" ").replace(/\s+/g, " "),
    empty: await txt("query-empty"),
    rows,
    total: rows.reduce((a, r) => a + fixed4(r[r.length - 1]), 0n),
  };
};

console.log(`\n── BROWSER: D51, what "total sales" measures ─────────────────`);

// The DEFECTIVE figure, computed on the wire through the typed-DSL door: the
// taxable value of Sales-typed vouchers. It must NOT be what the screen answers.
// Without this the D51 assertion could pass on a book where the two coincide.
const defectiveSales = await (async () => {
  const r = await api("POST", `/v1/companies/${CO}/query`, {
    dsl: {
      entity: "vouchers",
      metrics: [{ func: "sum", column: "taxable_value", as: "taxable" }],
      dimensions: [],
      filters: [{ column: "voucher_type_name", op: "eq", values: ["Sales"] }],
      date_range: null,
      order_by: [],
      limit: 100,
    },
  });
  return (r.body.rows ?? []).reduce((a, row) => a + fixed4(row[0]), 0n);
})();

{
  const a = await askOnScreen("total sales");
  const pl = await plGroup("Sales Accounts", WIN["this year"]);
  rec(
    'the "total sales" answer equals the Profit & Loss Sales line',
    a.total === pl,
    `screen ${money(a.total)} vs P&L ${money(pl)}`,
  );
  rec(
    "and it is NOT the old taxable-value figure (so the agreement is not a coincidence)",
    defectiveSales !== 0n && a.total !== defectiveSales,
    `net sales ${money(a.total)} vs sum(taxable_value) over Sales vouchers ${money(defectiveSales)}`,
  );
  rec(
    "the screen states WHAT was measured, above the table",
    /Profit & Loss/i.test(a.measure) && /credit\/debit notes|credit notes/i.test(a.measure),
    a.measure.slice(0, 120),
  );
  rec("the scope caption names the signed-in company", a.scope.includes(companyName), a.scope.slice(0, 80));
}

{
  const a = await askOnScreen("total purchases");
  const pl = await plGroup("Purchase Accounts", WIN["this year"]);
  rec(
    'the "total purchases" answer equals the Profit & Loss Purchases line',
    a.total === pl,
    `screen ${money(a.total)} vs P&L ${money(pl)}`,
  );
}

console.log(`\n── BROWSER: D21, which period was answered ───────────────────`);

for (const phrase of ["this month", "last month", "this quarter", "last quarter", "this year", "last year"]) {
  const [from, to] = WIN[phrase];
  const a = await askOnScreen(`sales ${phrase}`);
  const disclosed = a.period.includes(from) && a.period.includes(to);
  rec(
    `"sales ${phrase}" DISCLOSES the window ${from}..${to}`,
    disclosed,
    a.period.slice(0, 110),
  );
  // Only meaningful once the window is right; fetch the report for the window
  // the SCREEN named, so a screen lying about its own DSL still fails.
  const pl = await plGroup("Sales Accounts", [from, to]);
  rec(
    `"sales ${phrase}" equals the P&L for that same window`,
    a.total === pl,
    `screen ${money(a.total)} vs P&L ${money(pl)}${a.empty ? ` (screen: ${a.empty})` : ""}`,
  );
}

// THE REGRESSION THAT DEFINED D21: last-anything must differ from this-anything
// whenever the books differ across the two windows. If a future change reverts
// relativeRange to the substring switch, "last quarter" silently becomes "this
// quarter" and only this assertion notices.
{
  const lastQ = await plGroup("Sales Accounts", WIN["last quarter"]);
  const thisQ = await plGroup("Sales Accounts", WIN["this quarter"]);
  rec(
    "the books make the last/this distinction OBSERVABLE (else the two above are vacuous)",
    lastQ !== thisQ,
    `last quarter ${money(lastQ)} vs this quarter ${money(thisQ)}`,
  );
}

// SUBSTRINGS ARE NOT WORDS, and an UNQUALIFIED period word names no window.
{
  const a = await askOnScreen("sales at headquarters");
  rec(
    '"headquarters" does not smuggle in a quarter window',
    /ALL DATES/i.test(a.period),
    a.period.slice(0, 90),
  );
}
{
  const a = await askOnScreen("monthly sales");
  const all = await ask("total sales");
  const allTotal = (all.body.rows ?? []).reduce((x, r) => x + fixed4(r[1]), 0n);
  rec(
    '"monthly sales" is every date grouped by month, not the current month',
    /ALL DATES/i.test(a.period) && a.total === allTotal,
    `${a.period.slice(0, 60)} | ${money(a.total)} vs all-dates ${money(allTotal)}`,
  );
}
// A MULTI-PERIOD WINDOW IS REFUSED, not rounded down to one.
for (const nl of ["sales for the last 6 months", "sales last month and this year"]) {
  const a = await askOnScreen(nl);
  rec(`refused rather than answered for one period: ${JSON.stringify(nl)}`, a.error !== "", a.error.slice(0, 90));
}

console.log(`\n── BROWSER: D20, the NL answer vs the Outstandings report ────`);

for (const [phrase, kind] of [
  ["top debtors", "RECEIVABLE"],
  ["payables", "PAYABLE"],
]) {
  const a = await askOnScreen(phrase);
  const o = await outstandings(kind);
  rec(
    `"${phrase}" total equals the Outstandings report (${kind})`,
    a.total === o.total,
    `screen ${money(a.total)} vs report ${money(o.total)} as on ${o.asOn}`,
  );
  // RULE 4: names and sides, not just the total. A total can agree while the
  // party breakdown does not.
  const screenByLedger = new Map(a.rows.map((r) => [r[0], fixed4(r[1])]));
  const sameNames =
    screenByLedger.size === o.byLedger.size &&
    [...o.byLedger].every(([name, amt]) => screenByLedger.get(name) === amt);
  rec(
    `"${phrase}" names the SAME parties, for the same amounts`,
    sameNames,
    `screen ${JSON.stringify([...screenByLedger].map(([n, v]) => `${n}=${money(v)}`))} vs report ${JSON.stringify([...o.byLedger].map(([n, v]) => `${n}=${money(v)}`))}`,
  );
  rec(
    `"${phrase}" states its as-at basis on screen`,
    /AS AT TODAY/i.test(a.measure),
    a.measure.slice(0, 100),
  );
}

// A PERIOD-SCOPED OUTSTANDINGS QUESTION IS REFUSED. This is the same silent-
// window defect on the surface nobody looked at: before the fix the period word
// was dropped and today's position was shown under a question about March.
{
  const a = await askOnScreen("receivables last quarter");
  rec("a period-scoped receivables question is refused, not answered for today", a.error !== "", a.error.slice(0, 90));
}

console.log(`\n── BROWSER: D50, the caption that does not drift ─────────────`);

{
  const a = await askOnScreen("total sales for every company");
  rec("a cross-tenant question is refused on screen", a.error !== "", a.error.slice(0, 100));
  rec(
    "and the refusal SAYS the other company's books were not read",
    /another company/i.test(a.error),
    a.error.slice(0, 120),
  );
  rec("the scope caption is present on the refusal too", a.scope.includes(companyName), a.scope.slice(0, 80));
}
{
  const a = await askOnScreen(`total sales for ${OTHER_NAME}`);
  rec(
    "an answer to a question naming another tenant BY NAME is captioned with the company it is really for",
    a.rows.length > 0 && a.scope.includes(companyName) && /only this company/i.test(a.scope),
    `${a.rows.length} row(s); scope: ${a.scope.slice(0, 100)}`,
  );
}

console.log(`\n(company: ${companyName}; other tenant: ${OTHER_NAME}; today: ${iso(TODAY)})`);
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
