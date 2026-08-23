/**
 * L16 position 1 — D22 + D52: the forecast plane had no door, and F11 said it did.
 *
 * ⛔⛔ THE ORACLE IS A KEYBOARD WALK, NOT AN HTTP 200 AND NOT A MOUNTED COMPONENT.
 * cored has served `GET /cash-flow` and `GET /anomalies` since wave 2 — routed,
 * ACL-mapped, in the PDP vocabulary — so every "is it built?" question that reads
 * the server answers YES and has answered yes for a whole wave. What was missing
 * was the half an operator touches. So every arm below starts from the Gateway and
 * uses nothing but keys.
 *
 * ⛔ AND THE LOAD-BEARING ARM IS THE FOOTING, NOT THE HEADING. A probe that
 * asserts `h1 === "Cash Flow"` passes on a screen that renders a title over
 * nothing; a probe that compares the screen against `curl /cash-flow` is the
 * screen re-reading its own source. So the three `assumed_settlement_date` bill
 * counts READ OFF THE PAGE are footed against the open-bill count from the
 * OUTSTANDINGS report — a different route, a different handler, a different
 * report. If the projection silently dropped or double-counted a bill, that is
 * the arm that sees it and no other arm here can.
 *
 * ⚠️ EVERY DOM READ THROWS ON AN ABSENT TESTID rather than comparing an absent
 * value. Three probes in L15 passed vacuously by reading a projection that did
 * not exist (`p.ledger_name` for `p.ledger`, `stock_item_name` for `item`,
 * `innerText` for an input's value) — an absent projection has a falsy value that
 * compares cleanly against anything.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l16-d22.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO;
const CORED = "http://localhost:8087";
if (!CO) throw new Error("drive-l16-d22: CO is required (the seeded company id)");

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

const app = await openApp({ company: CO, headless: true });
const { page, body, press, altMac, toGateway } = app;

const heading = async () =>
  (await page.locator("h1").first().innerText().catch(() => "")).trim();
const seen = async (t) => (await page.getByTestId(t).count()) > 0;
/** ⛔ THROWS when the testid is absent. An absent projection must not compare. */
const text = async (t) => {
  if (!(await seen(t))) {
    throw new Error(
      `drive-l16-d22: data-testid="${t}" is NOT on the page. This probe refuses to ` +
        `compare an absent projection — that is how three L15 arms passed vacuously.`,
    );
  }
  return (await page.getByTestId(t).first().innerText()).replace(/\s+/g, " ").trim();
};
/** Walk to a report by NAME through Alt+G, with nothing but keys. */
const gotoReport = async (typed) => {
  await toGateway();
  await altMac("g", 1200);
  await page.keyboard.type(typed);
  await page.waitForTimeout(800);
  await press("Enter", 4000);
};

// ===========================================================================
// THE WIRE, read ONCE, as the thing the screen is checked AGAINST — never as
// the thing that proves the screen.
// ===========================================================================
const wire = (await api("GET", `/v1/companies/${CO}/cash-flow`)).body;
const wireAnom = (await api("GET", `/v1/companies/${CO}/anomalies`)).body;
const recv = (await api("GET", `/v1/companies/${CO}/outstandings?kind=RECEIVABLE`)).body;
const pay = (await api("GET", `/v1/companies/${CO}/outstandings?kind=PAYABLE`)).body;

// ⛔ THE INDEPENDENT COUNT. Bills the OUTSTANDINGS report says are open, both
// sides. The projection's job is to give each of them a date, so the count must
// survive the trip exactly — no bill invented, none lost.
const openBills =
  recv.parties.reduce((n, p) => n + (p.bills ?? []).length, 0) +
  pay.parties.reduce((n, p) => n + (p.bills ?? []).length, 0);

console.log(`\n── the wire, for reference ───────────────────────────────────`);
console.log(`  cash-flow: as_on=${wire.as_on} horizon=${wire.horizon_days} buckets=${wire.buckets?.length}`);
console.log(`  basis bills: due=${wire.basis?.assumed_settlement_date?.from_due_date?.bills}` +
            ` party=${wire.basis?.assumed_settlement_date?.from_party_credit_days?.bills}` +
            ` default=${wire.basis?.assumed_settlement_date?.from_default_credit_days?.bills}`);
console.log(`  outstandings open bills: receivable+payable = ${openBills}`);
console.log(`  anomalies: ${wireAnom.findings?.length} finding(s)`);

// ===========================================================================
// D22a — THE CASH-FLOW DOOR
// ===========================================================================
console.log(`\n── D22a: Alt+G > "cash" reaches the projection ───────────────`);
{
  await gotoReport("Cash Flow");
  const h = await heading();
  const seenBody = (await body()).replace(/\s+/g, " ");

  rec("D22: ⛔ Alt+G → \"Cash Flow\" → Enter lands on the CASH FLOW report",
      h === "Cash Flow", `h1="${h}"`);
  // ⛔ THE ARM THAT WOULD HAVE CAUGHT A NAME WITHOUT A SCREEN. A report name
  // registered with no component does not fail loudly — it falls through to
  // UnwiredReportScreen, which renders a polite sentence under the right title.
  rec("D22: ⛔ and NOT on \"is not available in this build\" (the unwired fallback)",
      !/is not available in this build/.test(seenBody),
      (seenBody.match(/.{0,50}not available in this build.{0,20}/) ?? ["(absent — good)"])[0]);
  rec("D22: the bucket table is rendered, not just a heading",
      await seen("cash-flow-buckets"), `buckets testid present=${await seen("cash-flow-buckets")}`);
}

// ===========================================================================
// D23's CLIENT HALF — THE DISCLOSURE IS ON THE PAGE
// ===========================================================================
console.log(`\n── D23 client half: the projection DISCLOSES itself ──────────`);
{
  const basis = await text("cash-flow-basis");

  rec("D23: ⛔ the word ESTIMATE is on the page in its own right",
      /THIS IS AN ESTIMATE/.test(basis),
      basis.slice(0, 80));

  // ⛔ VERBATIM AGAINST THE WIRE, not a regex for "projection". A screen that
  // paraphrased the method would drift from the engine silently.
  const method = await text("cash-flow-method");
  rec("D23: ⛔ the server's METHOD sentence is rendered VERBATIM",
      method.includes(wire.method),
      `screen="${method}"\n         wire  ="${wire.method}"`);

  const projected = await text("cash-flow-projected");
  rec("D23: what_is_projected is rendered verbatim (the DATE, not the amount)",
      projected.includes(wire.basis.what_is_projected.slice(0, 60)),
      projected.slice(0, 100));

  // ⛔ THE THREE SOURCES, SEPARATELY. Reading each testid individually is the
  // point: a screen that totalled them into one "assumed" figure would still
  // show a plausible page, and the 30-day-convention line is the one an
  // operator most needs to see.
  const a = wire.basis.assumed_settlement_date;
  for (const [testid, key, label] of [
    ["cash-flow-basis-due-bills", "from_due_date", "the bill's own due date (OBSERVED)"],
    ["cash-flow-basis-party-bills", "from_party_credit_days", "the party's credit period (assumed)"],
    ["cash-flow-basis-default-bills", "from_default_credit_days", "this build's convention (assumed)"],
  ]) {
    const onScreen = await text(testid);
    rec(`D23: basis line — ${label} — shows the wire's bill count`,
        Number(onScreen) === a[key].bills,
        `screen=${onScreen} wire=${a[key].bills}`);
  }

  // ⛔ AND THE CONVENTION NAMES ITS DAY COUNT. "assumed" alone does not tell an
  // operator the assumption is a constant in the software.
  const defaultLine = await text("cash-flow-basis-default");
  rec("D23: ⛔ the convention line NAMES its day count, not just \"assumed\"",
      defaultLine.includes(String(a.default_credit_days)),
      `"${defaultLine.slice(0, 110)}"`);

  // not_modelled, every line, verbatim.
  const notModelled = await text("cash-flow-not-modelled");
  const missing = wire.basis.not_modelled.filter((n) => !notModelled.includes(n.slice(0, 40)));
  rec("D23: ⛔ all four not_modelled lines are on the page VERBATIM",
      missing.length === 0,
      missing.length === 0 ? `${wire.basis.not_modelled.length}/4 present` : `MISSING: ${missing.join(" // ")}`);

  // The overdue fold, with its money — the count inside `basis`, not the bool.
  const overdue = await text("cash-flow-overdue");
  const of = wire.basis.overdue_folded_into_first_bucket;
  rec("D23: the overdue fold is stated WITH its bill count",
      wire.overdue_folded_into_first_bucket
        ? overdue.includes(String(of.bills))
        : /Nothing is overdue/.test(overdue),
      `"${overdue.slice(0, 120)}"`);

  // beyond_horizon is an OBJECT, and is rendered as a row with no dates.
  rec("D23: beyond_horizon renders as a dateless tail row, not an invented bucket",
      await seen("cash-flow-beyond"),
      await text("cash-flow-beyond"));
}

// ===========================================================================
// ⛔⛔ THE FOOTING — THE ONE ARM THAT IS NOT THE SCREEN READING ITSELF
// ===========================================================================
console.log(`\n── THE FOOTING: the screen's basis vs the OUTSTANDINGS report ─`);
{
  const onScreen =
    Number(await text("cash-flow-basis-due-bills")) +
    Number(await text("cash-flow-basis-party-bills")) +
    Number(await text("cash-flow-basis-default-bills"));

  // ⛔ THE PROJECTION'S JOB IS TO DATE EVERY OPEN BILL AND INVENT NONE. The
  // amounts are Outstandings' own pending balances; only the DATE is projected.
  // So the bill count must survive the trip exactly. This compares a figure read
  // off the CASH FLOW SCREEN against a figure from a DIFFERENT REPORT'S route —
  // the only arm here that could catch a projection that lost or duplicated a
  // bill, because every other arm's two sides come from the same handler.
  rec("D22/D23: ⛔⛔ the screen's three basis counts FOOT to the open-bill count " +
      "from the Outstandings report (receivable + payable)",
      onScreen === openBills,
      `screen basis total=${onScreen}  ·  outstandings open bills=${openBills}` +
        `  (recv=${recv.parties.reduce((n, p) => n + (p.bills ?? []).length, 0)}` +
        ` pay=${pay.parties.reduce((n, p) => n + (p.bills ?? []).length, 0)})`);

  // A second, weaker cross-check with a DIFFERENT failure mode: every bill
  // landed somewhere in the buckets or the tail, so the money must foot too.
  const sum = (xs) => xs.reduce((t, x) => t + Math.round(Number(x) * 10000), 0);
  const bucketIn = sum(wire.buckets.map((b) => b.inflow)) + Math.round(Number(wire.beyond_horizon.inflow) * 10000);
  const basisIn = sum([
    wire.basis.assumed_settlement_date.from_due_date.inflow,
    wire.basis.assumed_settlement_date.from_party_credit_days.inflow,
    wire.basis.assumed_settlement_date.from_default_credit_days.inflow,
  ]);
  rec("D22/D23: every projected INFLOW rupee is in a bucket or the tail",
      bucketIn === basisIn,
      `buckets+tail=${(bucketIn / 10000).toFixed(4)}  basis=${(basisIn / 10000).toFixed(4)}`);
}

// ===========================================================================
// F2 — the date the projection runs FROM, and the heading follows the RESPONSE
// ===========================================================================
console.log(`\n── F2: the projection date, and whose date the heading shows ──`);
{
  const before = await text("cash-flow-buckets");
  await press("F2", 1500);
  const onF2 = (await body()).replace(/\s+/g, " ");
  // ⛔ THE PERIOD PANEL MUST NOT SAY THE PERIOD IS IGNORED. That defect shipped
  // TWICE (the TDS register and the Stock Summary) on screens whose own headings
  // asked for a period, and it is why AS_AT_ONLY_REPORTS exists.
  rec("D22: F2 does NOT tell the operator this screen has no date dimension",
      !/no date dimension in this build/.test(onF2),
      (onF2.match(/.{0,60}no date dimension.{0,20}/) ?? ["(absent — good)"])[0]);
  rec("D22: …and F2 says only the TO date applies (a projection runs forward)",
      /only .{0,20}To.{0,20} applies|as at|position/i.test(onF2),
      (onF2.match(/.{0,110}(only|as at|position).{0,40}/i) ?? ["(no period wording found)"])[0]);
  await press("Escape", 1200);
  rec("D22: Esc leaves the projection intact behind the panel",
      (await text("cash-flow-buckets")) === before, "bucket table unchanged");
}

// ===========================================================================
// Ctrl+P — MAPPED, so the operator reads the SERVER's 501 and not a local guess
// ===========================================================================
console.log(`\n── Ctrl+P: the mapped 501, not a locally-invented refusal ─────`);
{
  await press("Control+p", 3000);
  const afterPrint = (await body()).replace(/\s+/g, " ");
  const status = (await page.locator('[role="status"]').allInnerTexts()).join(" | ").replace(/\s+/g, " ");
  const both = `${afterPrint} ${status}`;
  // The server's own sentence names the ACL rule it honoured and says the layout
  // is unspecified. A locally-invented refusal cannot say that.
  rec("D22: Ctrl+P surfaces the SERVER's 501 for cash-flow, not a blank page",
      /501|not implemented|no printed layout|does not print/i.test(both),
      (both.match(/.{0,140}(501|not implemented|no printed layout|does not print).{0,60}/i) ?? ["(nothing about printing on screen)"])[0]);
}

// ===========================================================================
// D22b — THE ANOMALY DOOR. The second half of the row, and the reason this
// lane did not stop at cash flow.
// ===========================================================================
console.log(`\n── D22b: Alt+G > "anom" reaches the anomaly list ─────────────`);
{
  await gotoReport("Anomalies");
  const h = await heading();
  const seenBody = (await body()).replace(/\s+/g, " ");

  rec("D22: ⛔ Alt+G → \"Anomalies\" → Enter lands on the ANOMALY LIST",
      h === "Anomalies", `h1="${h}"`);
  rec("D22: …and NOT on the unwired fallback",
      !/is not available in this build/.test(seenBody),
      (seenBody.match(/.{0,50}not available in this build.{0,20}/) ?? ["(absent — good)"])[0]);

  // ⛔ THE AXIS IS ON THE PAGE. An operator who read this as a window over
  // voucher DATES would call the list broken the first time a backdated voucher
  // failed to appear under its own period.
  rec("D22: ⛔ the page says the window is on when a voucher was ENTERED",
      /ENTERED/.test(seenBody) && /backdated/i.test(seenBody),
      (seenBody.match(/.{0,150}ENTERED.{0,80}/) ?? ["(the axis is NOT stated)"])[0]);
  rec("D22: …and says a finding is not an error and not an accusation",
      /not an error and not an accusation/.test(seenBody),
      (seenBody.match(/.{0,90}not an error.{0,40}/) ?? ["(absent)"])[0]);

  // The list agrees with the wire — including the empty case, which is a real
  // answer and gets its own sentence rather than a blank panel.
  const n = wireAnom.findings.length;
  if (n === 0) {
    rec("D22: an empty list SAYS it is the normal state of clean books",
        /No findings in this window/.test(seenBody),
        (seenBody.match(/.{0,120}No findings.{0,60}/) ?? ["(no empty-state sentence)"])[0]);
  } else {
    const rows = await page.getByTestId("anomalies-findings").locator("tbody tr").count();
    rec(`D22: all ${n} findings from the wire are rendered as rows`,
        rows === n, `rows=${rows} wire=${n}`);
  }
}

// ===========================================================================
// D52 — F11 NAMES THE DOORS, AND THE DOORS OPEN
// ===========================================================================
console.log(`\n── D52: F11 names a door, and the door actually opens ────────`);
{
  await toGateway();
  await press("F11", 2500);
  const f11 = (await body()).replace(/\s+/g, " ");

  rec("D52: F11 lists \"Cash-flow projection\" as its own row",
      /Cash-flow projection/.test(f11),
      (f11.match(/.{0,20}Cash-flow projection.{0,60}/) ?? ["(absent)"])[0]);
  rec("D52: F11 lists \"Anomaly detection\" as its own row",
      /Anomaly detection/.test(f11),
      (f11.match(/.{0,20}Anomaly detection.{0,60}/) ?? ["(absent)"])[0]);
  // ⛔ THE DEFECT WAS A NOTE THAT NAMED NO DOOR while both neighbours named
  // theirs. Both notes must now name one.
  rec("D52: ⛔ the cash-flow note NAMES its door (Alt+G > Cash Flow)",
      /Alt\+G > Cash Flow/.test(f11),
      (f11.match(/.{0,10}Alt\+G > Cash Flow.{0,50}/) ?? ["(names no door)"])[0]);
  rec("D52: ⛔ the anomaly note NAMES its door (Alt+G > Anomalies)",
      /Alt\+G > Anomalies/.test(f11),
      (f11.match(/.{0,10}Alt\+G > Anomalies.{0,50}/) ?? ["(names no door)"])[0]);
  // The old note is gone — it advertised "an optional statistical forecast",
  // which is D72's insightd forecast and has no renderer at all.
  rec("D52: the old note's unrenderable \"optional statistical forecast\" claim is GONE",
      !/optional statistical forecast/.test(f11),
      (f11.match(/.{0,40}optional statistical forecast.{0,20}/) ?? ["(absent — good)"])[0]);
  // The moved count.
  rec("D52: the Alt+E row's pinned report count moved from NINE to ELEVEN",
      /ELEVEN reports in the Alt\+G list/.test(f11) && !/NINE reports in the Alt\+G list/.test(f11),
      (f11.match(/.{0,10}(NINE|ELEVEN) reports in the Alt\+G list.{0,40}/) ?? ["(count sentence absent)"])[0]);

  // ⛔⛔ AND NOW DO WHAT THE SENTENCE SAYS, WITH NOTHING BUT THE KEYBOARD. This
  // is the arm the recon asked for and it is the whole point of D52: a note that
  // names a door is only true if the door opens. F11 -> Esc -> the exact walk
  // the note prescribes.
  await press("Escape", 1500);
  await gotoReport("Cash Flow");
  rec("D52: ⛔⛔ following F11's own instruction from the keyboard REACHES the projection",
      (await heading()) === "Cash Flow", `h1="${await heading()}"`);
  await gotoReport("Anomalies");
  rec("D52: ⛔⛔ …and F11's anomaly instruction reaches the anomaly list",
      (await heading()) === "Anomalies", `h1="${await heading()}"`);
}

// ===========================================================================
// The alias that is deliberately ABSENT — a wrong answer beats no answer only
// if you never have to explain it to an auditor.
// ===========================================================================
console.log(`\n── the alias that must NOT exist ─────────────────────────────`);
{
  await gotoReport("Cash Flow Statement");
  const h = await heading();
  const seenBody = (await body()).replace(/\s+/g, " ");
  // In Tally "Cash Flow Statement" is a HISTORIC statement of cash that actually
  // moved. Landing that phrase on a forward projection of invented settlement
  // dates would have an operator read projected figures as recorded ones.
  rec("D22: ⛔ \"Cash Flow Statement\" does NOT silently land on the projection",
      h !== "Cash Flow",
      `h1="${h}" — a different question deserves no alias`);
  // ⛔⛔ AND THE ARM ABOVE IS NOT ENOUGH ON ITS OWN, WHICH IS WHY THIS ONE EXISTS.
  // On the first run it passed with `h1=""` — it passed because NOTHING was on
  // the page, not because the right thing was. That is the §5.7 shape exactly: an
  // absent projection compares cleanly against anything. So assert POSITIVELY
  // where the operator ended up: the picker is still open with the unmatched text
  // still in it, waiting, which is the honest outcome for a phrase this product
  // does not answer to.
  rec("D22: ⛔ …and the operator is left IN THE PICKER with no match, not nowhere",
      /Go To/i.test(seenBody) && !/Cash Flow$/.test(h),
      `h1="${h}" · picker still on screen=${/Go To/i.test(seenBody)}` +
        ` · body="${seenBody.slice(0, 90)}"`);
}

summary();

