/**
 * L14 — THE CHEAP HONEST ROWS. D82 (the HSN refusal on the ALTER path),
 * D77 and D80. One probe, one arm per row, grown as the lane is built.
 *
 * ⛔ WHY THESE ARE ONE LANE. Each is small, each has a clean oracle, and none
 * of them moves a reported number — so they can share a tenant and a run, which
 * the rows that DO move a number (D8, D86, D85) deliberately cannot.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l14-cheap.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO;
const CORED = "http://localhost:8087";
if (!CO) throw new Error("drive-l14-cheap: CO is required (the seeded company id)");

const STAMP = Math.random().toString(36).slice(2, 7);

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
  return { status: r.status, body: parsed };
};
const TOK = (await j("POST", "/auth/token", { body: { subject: "browser-dev-owner", amr: ["webauthn"] } })).body.token;
const api = (method, path, body) => j(method, path, { token: TOK, body });

const app = await openApp({ company: CO });
const { page, body, press, altMac, toGateway } = app;
const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const text = async (t) =>
  (await seen(t)) ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim() : "";
/** ⚠️ TWO role="status" spans live on the voucher screen. Read them both. */
const statusText = async () =>
  (await page.locator('[role="status"]').allInnerTexts()).join(" | ").replace(/\s+/g, " ");

/**
 * Drill a voucher open from the Day Book, the way an operator reaches one.
 *
 * ⛔ MATCH ON THE ROW'S OWN FIELDS, NOT ON THE NARRATION. The Day Book's
 * particulars cell shows the PARTY when the voucher has one and the narration
 * only otherwise — so a sales invoice is listed as "Acme Traders" and a probe
 * grepping for its narration finds nothing (gotcha #8, and it cost this file a
 * run). The date + number + party triple is what is actually on the row.
 *
 * ⛔ AND THE HEADING IS WHAT SAYS WHICH MODE THE FRAME IS IN. `VoucherScreen`
 * renders `{title} Voucher{isAlter ? " (Alteration)" : ""}`, so the suffix is
 * the ONLY thing on screen that distinguishes an alteration from a create —
 * the form carries the loaded voucher's values either way. Returning the
 * heading (not a bare boolean) is what lets the D89 arms below assert on it.
 *
 * ⚠️ THE WAITS ARE DELIBERATELY LONG. At 700/400/2200 this drill intermittently
 * found ZERO grid rows on a loaded machine (load average 80+ from other repos),
 * which reads as "the voucher is not on the books" rather than as "the Day Book
 * had not painted yet". 1200/800/4000 held across every run.
 */
const heading = async () =>
  (await page.locator("h1").first().innerText().catch(() => "")).trim();

const drill = async (match) => {
  await toGateway();
  await altMac("g", 1200);
  await page.keyboard.type("Day Book");
  await page.waitForTimeout(800);
  await press("Enter", 4000);
  const rows = page.locator('[role="grid"] [role="row"]');
  const n = await rows.count();
  if (n === 0) return { opened: false, heading: "", why: "the Day Book painted NO rows" };
  for (let i = 0; i < n; i++) {
    const txt = (await rows.nth(i).innerText()).replace(/\n/g, " | ");
    if (!txt.includes(match)) continue;
    await rows.nth(i).click();
    await page.waitForTimeout(400);
    await press("Enter", 4000);
    return { opened: true, heading: await heading(), why: txt };
  }
  return { opened: false, heading: "", why: `${n} rows, none matching` };
};

/** The Day Book over one date, on the wire — the count/op oracle for D89. */
const dayBook = async (date) =>
  (await api("GET", `/v1/companies/${CO}/day-book?from=${date}&to=${date}`)).body
    .entries ?? [];

// ===========================================================================
// D82 — THE HSN REFUSAL ON THE ALTER PATH. **WORKING AS INTENDED** (the user's
// decision, 2026-08-21) — so what is driven is that it REACHES the operator and
// READS as a reason, and that its remedy is on the same screen.
//
// ⛔ THE LEGACY INVOICE IS SEEDED OVER THE WIRE, because the form REFUSES to
// create one (that is D60). An invoice with no HSN arrives by import, or from a
// build predating the refusal — which is exactly the population the row is about.
// ===========================================================================
console.log(`\n── D82: the HSN refusal an operator actually meets ───────────`);
const legacy = await (async () => {
  const l = (await api("GET", `/v1/companies/${CO}/ledgers`)).body.ledgers;
  const L = Object.fromEntries(l.map((x) => [x.name, x.id]));
  const r = await api("POST", `/v1/companies/${CO}/vouchers/sales`, {
    voucher_date: "2026-08-19",
    created_by: "l14",
    narration: `L14 legacy invoice no HSN ${STAMP}`,
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
      lines: [{ taxable_value: "10000", gst_rate_pct: "18" }],
      cgst_ledger_id: L["Output CGST"],
      sgst_ledger_id: L["Output SGST"],
      igst_ledger_id: L["Output IGST"],
    },
  });
  return r;
})();
rec("a legacy sales invoice with NO HSN is on the books (seeded over the wire)",
    legacy.status === 201, `${legacy.status} ${JSON.stringify(legacy.body).slice(0, 120)}`);

const legacyRow = `2026-08-19 | ${legacy.body.voucher_no} | Acme Traders`;
const before89 = await dayBook("2026-08-19");
const opened = await drill(legacyRow);
// ⛔ ASSERT THE MODE ON THE HEADING, NOT ON `gst-panel`. A create frame has a
// gst-panel too, so the old assertion passed on either — and its evidence
// string was sliced to 80 chars, which cut " (Alteration)" off the end and
// made a REAL mode defect (D89, below) read as a probe bug for half an hour.
rec("it opens for ALTERATION from the Day Book — and the HEADING says so",
    opened.opened && /\(Alteration\)$/.test(opened.heading) && (await seen("gst-panel")),
    `${legacyRow} -> h1="${opened.heading}" (${opened.why.slice(0, 60)})`);
{
  // ⛔ THE REMEDY IS ON SCREEN BEFORE THE REFUSAL IS. A refusal whose fix is not
  // reachable from the keyboard would make this a real defect rather than a
  // decision — that is the shape the ITEM path has, and it is DISCLOSED there
  // instead of refused.
  rec("D82: the HSN field is on the screen, empty and keyable",
      (await seen("gst-hsn")) &&
        (await testid("gst-hsn").first().inputValue()) === "",
      `"${await testid("gst-hsn").first().inputValue().catch(() => "<absent>")}"`);

  const before = app.net.length;
  await press("Control+a", 4000);
  const wire = app.net.slice(before);
  const said = await statusText();
  rec("D82: ⛔ the refusal REACHES the operator on the alter",
      /HSN \/ SAC is required/.test(said), said.slice(0, 170));
  rec("D82: …and it says WHY — Rule 46(g), unprintable, and what to key",
      /Rule 46\(g\)/.test(said) && /cannot be printed/.test(said) &&
        /Key the code for what was supplied/.test(said),
      said.slice(0, 260));
  rec("D82: ⛔ CONTROL — and NOTHING was sent",
      !wire.some((w) => /vouchers\/sales/.test(w)), wire.join(" · ") || "(no non-GET traffic)");

  // =========================================================================
  // ⛔⛔ D89 — AND THE FRAME MUST STILL BE THE ONE IT WAS ALTERING.
  //
  // This is the arm the lane was missing, and it is where the defect lived.
  // `navMachine`'s `accept` reducer is PURE: it fires on the keystroke and can
  // never learn that the write was refused — least of all here, where
  // `gstRefusal` refused CLIENT-SIDE and not one byte left the browser. It
  // replaced the alter frame with a fresh CREATE frame anyway, dropping the
  // loaded voucher_id that routes the PUT, while the form kept the loaded
  // voucher's 10,000 on screen. The heading is the ONLY thing that changed, by
  // thirteen characters, at the bottom of a refusal the operator is reading.
  // =========================================================================
  rec("D89: ⛔ after a REFUSED accept the frame is still the ALTERATION",
      /\(Alteration\)$/.test(await heading()), `h1="${await heading()}"`);
}
{
  // ⛔ AND THE REMEDY WORKS. Without this arm "refuses everything" would pass as
  // "refuses the right thing".
  const hsn = testid("gst-hsn").first();
  await hsn.click();
  await hsn.fill("7326");
  await page.waitForTimeout(300);
  const before = app.net.length;
  // ⚠️ 9s, NOT 4s. This is the session's FIRST write, so it carries the WebAuthn
  // step-up ceremony: the POST/PUT comes back 403, the client enrols and
  // asserts, and only then does the real call go out. At 4000 the probe cut the
  // ceremony off mid-assert and reported the LAST thing it saw (the 403) as the
  // outcome. Six round trips do not fit in four seconds on a loaded machine.
  await press("Control+a", 9000);
  const wire = app.net.slice(before);
  rec("D82: ⛔ CONTROL — keying the code lets the SAME alter through",
      wire.some((w) => /PUT .*\/vouchers\/sales\/[0-9a-f-]{36} 200/.test(w)),
      wire.join(" · "));
  // ⛔ D89's OWN WIRE ARM, stated as the NEGATIVE, because the defect's whole
  // signature is a POST that succeeded: 201, "Saved", and a twin on the books.
  rec("D89: ⛔ and it did NOT post a twin — no CREATE went out",
      !wire.some((w) => /POST .*\/vouchers\/sales 20[01]/.test(w)),
      wire.filter((w) => /vouchers\/sales/.test(w)).join(" · ") || "(no sales writes)");

  const got = await api("GET", `/v1/companies/${CO}/vouchers/${legacy.body.voucher_id}`);
  rec("D82: ⛔ THE BOOKS — the code is stored, and the RATE survived the round trip",
      got.body.gst_lines?.[0]?.hsn === "7326" &&
        got.body.gst_lines?.[0]?.gst_rate_pct === "18.0000",
      JSON.stringify(got.body.gst_lines?.[0]));

  // ⛔⛔ D89's LEDGER ORACLE — A ROW NOT APPEARING, read back through the Day
  // Book, which is a surface neither the form nor the reducer owns. The count
  // over the date must be UNCHANGED, and the voucher must carry an ALTER op
  // rather than a second CREATE. `op` is the part a count alone would miss: a
  // twin plus a deleted original would also leave the count flat.
  const after89 = await dayBook("2026-08-19");
  rec("D89: ⛔ THE BOOKS — the Day Book has the SAME number of entries on the date",
      after89.length === before89.length,
      `${before89.length} entries before, ${after89.length} after`);
  const mine = after89.filter((e) => (e.narration ?? "").includes(STAMP));
  rec("D89: ⛔ THE BOOKS — this invoice reads as ALTERED, not created twice",
      mine.length === 1 && mine[0]?.op === "ALTER",
      `${mine.length} entry/entries for stamp ${STAMP}: ${mine.map((e) => e.op).join(",") || "(none)"}`);
}

console.log(`\n(stamp ${STAMP})`);
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
