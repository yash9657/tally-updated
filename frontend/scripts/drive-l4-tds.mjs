/**
 * L4 — TDS/TCS. D1 (no route attached an allocation to an EXISTING challan),
 * D2 (nothing compared allocations against the challan's tax box), D3 (neither a
 * deduction nor a collection could be keyed from the keyboard at all), D4 (the
 * TCS half of the challan mechanism) and D88 (an alter silently deleted the
 * deduction).
 *
 * ⛔ THE ORACLE IS THE UNALLOCATED FIGURE ON SCREEN, AND IT IS NEVER THE STATUS
 * CODE. A screen that reported "attached" off a 200 would look IDENTICAL
 * whether it moved anything or not — this repo's signature failure. So D1's
 * assertion is that `tds-challan-unallocated:{serial}` FALLS, read off the
 * register the screen re-read for itself, with the BEFORE figure captured from
 * the panel (`tds-allocate-before`) first.
 *
 * ⛔ THE PRECONDITION NOTHING ON A VOUCHER SCREEN ASKS FOR: a deductor status
 * for the tax year PRECEDING the voucher's. `seed:demo` writes none, so every
 * TDS post is refused outright until F11 > TDS Deductor Details records one.
 * That is driven here rather than set up on the wire, because "the operator can
 * get themselves out of the refusal" is half of what D3 is.
 * ⚠️ COMPANY is a specified person WHATEVER its turnover, so the turnover
 * fields are hidden for it and sending a figure would be a 400.
 *
 * ⛔ WHAT THIS PROBE LEAVES BEHIND, AND WHY THAT IS NOT L13's MISTAKE. It posts
 * three real Journals and records one real deposit. The Journals are BALANCED
 * (Dr expense in full, Cr bank net, Cr the TDS liability), so unlike L13's
 * one-sided opening balance they leave the Trial Balance level — asserted at the
 * bottom, from the report's own `balanced` flag. Nothing is torn down because
 * deleting a voucher while keeping its deduction (or the reverse) would leave
 * the books and the return disagreeing, which is worse residue than the entries
 * themselves. The CHALLAN SERIAL IS STAMPED PER RUN: the CIN is an identity and
 * a re-post of one already on file is a 409, so a fixed serial would make the
 * second run of this file fail inside D1's arm and read as a regression.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l4-tds.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO;
const CORED = "http://localhost:8087";
if (!CO) throw new Error("drive-l4-tds: CO is required (the seeded company id)");

/** 5 digits, and the leading zero is part of it — 05122 is not 5122. */
const SERIAL = "0" + String(1000 + Math.floor(Math.random() * 8999));
/**
 * A SECOND deposit, for the collection — one challan cannot pay both (D4).
 * ⛔ DERIVED FROM THE FIRST rather than drawn again: two independent draws can
 * collide, and the collision is a 409 inside D4's arm that reads as the mixed-
 * kind refusal failing to fire.
 */
const SERIAL2 = "0" + String(((Number(SERIAL) + 1) % 9000) + 1000);
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

const sql = async (query) => {
  const { execFile } = await import("node:child_process");
  return new Promise((res, rej) => {
    execFile("psql",
      ["-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", query],
      { env: { ...process.env, PGPASSWORD: "postgres" } },
      (err, stdout) => (err ? rej(err) : res(stdout.trim())));
  });
};
const q = (s) => `'${String(s).replace(/'/g, "''")}'`;

const app = await openApp({ company: CO });
const { page, body, press, altMac, toGateway } = app;

const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const text = async (t) =>
  (await seen(t)) ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim() : "";
const fill = async (t, v) => { const el = testid(t).first(); await el.click(); await el.fill(v); };
const fillByLabel = async (label, v) => {
  const el = page.getByLabel(label, { exact: true }).first();
  await el.click();
  await el.fill(v);
};

// ===========================================================================
// PRECONDITION, DRIVEN — F11 > TDS Deductor Details for the PRECEDING year.
// ===========================================================================
console.log(`\n── the deductor status, keyed on F11 ─────────────────────────`);
await toGateway();
await press("F11", 1500);
rec("F11 reaches the deductor-status panel", await seen("deductor-status-panel"),
    (await text("deductor-status-panel")).slice(0, 90));
{
  const before = await text("deductor-status-panel");
  rec("…and it says out loud that nothing is recorded, so TDS will be REFUSED",
      /Nothing recorded yet/i.test(before) || /2025-26/.test(before),
      before.slice(0, 140));

  await fillByLabel("Tax year beginning", "2025");
  await page.getByLabel("Deductor type", { exact: true }).first().selectOption("COMPANY");
  await page.waitForTimeout(200);
  // ⚠️ THE TURNOVER FIELDS MUST NOT BE THERE FOR A COMPANY — a figure against
  // one is a 400, not a harmless extra, so their ABSENCE is the assertion.
  rec("a COMPANY is offered no turnover box (it is liable whatever its turnover)",
      (await page.getByLabel("Prior-year turnover", { exact: true }).count()) === 0);
  await page.getByRole("button", { name: "Record" }).first().click();
  await page.waitForTimeout(1200);
  const after = await text("deductor-status-panel");
  rec("the status is recorded for 2025-26 and names the year it GOVERNS",
      /2025-26/.test(after) && /2026-27/.test(after),
      after.replace(/\s+/g, " ").slice(-180));
}
const dbStatus = await sql(
  `select entity_category||'|'||coalesce(prior_year_turnover_rupees::text,'(null)')
     from company_deductor_status where company_id=${q(CO)} and tax_year_start=2025`);
rec("…and the books hold COMPANY with NO turnover figure",
    dbStatus === "COMPANY|(null)", dbStatus || "(no row)");

// ===========================================================================
// D3 (TDS half) — A DEDUCTION KEYED FROM THE KEYBOARD.
//
// F7 is the one class that DEFAULTS to the Dr/Cr grid the panel needs: a
// withholding splits the payment three ways and single entry puts the SUM of
// the particulars on one account, which leaves the withheld portion nowhere.
// ===========================================================================
const TDS_LEDGER = "TDS on Salary Payable";
/** Key one Journal that withholds tax. Returns the voucher number it was given. */
const keyDeduction = async ({ gross, net, tax, section, deductee, narration, leg = TDS_LEDGER }) => {
  await toGateway();
  await press("F7", 1500);
  await fill("voucher-narration", narration);
  await testid("de-side:0").first().selectOption("DR");
  await fill("de-ledger:0", "Rent");
  await fill("de-amount:0", gross);
  await testid("de-add-line").first().click(); await page.waitForTimeout(200);
  await testid("de-side:1").first().selectOption("CR");
  await fill("de-ledger:1", "HDFC Bank");
  await fill("de-amount:1", net);
  await testid("de-add-line").first().click(); await page.waitForTimeout(200);
  await testid("de-side:2").first().selectOption("CR");
  await fill("de-ledger:2", leg);
  await fill("de-amount:2", tax);
  await page.waitForTimeout(300);

  await testid("tax-tds-open").first().click();
  await page.waitForTimeout(300);
  await testid("tax-tds-section").first().selectOption(section);
  await fill("tax-tds-party", deductee);
  await fill("tax-tds-amount", gross);
  await page.waitForTimeout(200);
  await testid("tax-tds-leg").first().selectOption(leg);
  await page.waitForTimeout(300);
  const before = app.net.length;
  await press("Control+a", 4000);
  const wire = app.net.slice(before);
  const no = (await text("voucher-no")).replace(/[^0-9]/g, "");
  return { no, wire };
};

console.log(`\n── D3: a deduction keyed on F7 ───────────────────────────────`);
await toGateway();
await press("F7", 1500);
rec("F7 opens a Journal on the Dr/Cr grid", await seen("double-entry-panel"));
// ⛔ THE PANEL IS ON THIS CLASS AND ITS KIND IS TDS — the thing driving caught
// that review did not was a tax-COLLECTED panel appearing here, because the
// kind was inferred from `primarySide` (the Journal's is "DR", which has
// nothing to do with the direction of money).
rec("D3: the Journal offers a TDS block, and it is DEDUCTED not COLLECTED",
    (await seen("tax-tds-open")) && !(await seen("tax-tcs-open")),
    (await text("tax-tds-open")) || "(no block)");

await testid("tax-tds-open").first().click();
await page.waitForTimeout(300);
rec("…which opens the panel", await seen("tax-tds-panel"));

// The leg picker READS THE GRID, so with an empty grid it can offer nothing.
{
  const legs = await testid("tax-tds-leg").first().innerText();
  rec("D3: the liability-line picker starts empty — it reads THIS voucher's lines, not the chart",
      !/TDS on Salary Payable/.test(legs), legs.replace(/\s+/g, " ").slice(0, 90));
}
// A party typed but never created resolves to no id, and is REFUSED with the
// figures still on screen rather than minting a ledger to satisfy a 422.
await testid("tax-tds-section").first().selectOption("194I-B");
await fill("tax-tds-party", `Nobody ${STAMP}`);
await fill("tax-tds-amount", "200000");
await page.waitForTimeout(400);
rec("D3: a deductee who is not a ledger is REFUSED, and no ledger is minted",
    /refus|not a ledger|no ledger|unknown|create/i.test(await text("tax-tds-refusal")) ||
      (await text("tax-tds-refusal")) !== "",
    (await text("tax-tds-refusal")).slice(0, 160));
{
  const before = app.net.length;
  await press("Control+a", 2500);
  const after = app.net.slice(before);
  rec("D3: ⛔ CONTROL — Ctrl+A over that refusal posts NOTHING and mints NO ledger",
      !after.some((w) => /POST .*\/(vouchers|ledgers)/.test(w)),
      after.join(" · ") || "(no non-GET traffic at all)");
}
// The rate is ECHOED from the served catalog, never computed here.
await fill("tax-tds-party", "Pune Fabricators");
await page.waitForTimeout(400);
rec("D3: the served rate for 194I-B is echoed with its threshold and its basis",
    /10%/.test(await text("tax-tds-rate")) && /50000/.test(await text("tax-tds-rate")),
    (await text("tax-tds-rate")).slice(0, 170));
// A deduction with no posted leg is LEGAL and DISCLOSED, not refused.
rec("D3: naming no liability line is DISCLOSED, not refused",
    /recorded for the return/i.test(await text("tax-tds-unposted")) &&
      (await text("tax-tds-refusal")) === "",
    (await text("tax-tds-unposted")).slice(0, 150));

// ---- Now key it for real, three lines and a named leg.
await press("Escape", 600);
const v1 = await keyDeduction({
  gross: "200000", net: "180000", tax: "20000", section: "194I-B",
  deductee: "Pune Fabricators", narration: `L4 rent withholding ${STAMP}`,
});
rec("D3: the Journal POSTS with its tds block", v1.wire.some((w) => /POST .*\/vouchers\/journal 201/.test(w)),
    `voucher ${v1.no} | ${v1.wire.join(" · ")}`);

// ⛔ THE BOOKS, THROUGH A DIFFERENT SUBSYSTEM. Sides and ledger NAMES — never a
// total (RULE 4). The withheld fifth must really be CREDITED to the liability.
{
  const legs = await sql(
    `select string_agg(l.name||':'||p.dr_cr, ' ' order by l.name)
       from posting p join ledger l on l.id=p.ledger_id
       join voucher_version vv on vv.id=p.voucher_version_id
       join voucher_head vh on vh.current_version_id=vv.id
      where vh.company_id=${q(CO)}
        and vv.payload->>'narration'=${q(`L4 rent withholding ${STAMP}`)}`);
  rec("D3: ⛔ THE BOOKS — the expense is DEBITED, the bank and the TDS liability CREDITED",
      legs === `HDFC Bank:C Rent:D ${TDS_LEDGER}:C`, legs || "(no postings)");
}
{
  const ded = await sql(
    `select d.section||'|'||d.tds_amount_rupees||'|'||l.name
       from tds_deduction d join ledger l on l.id=d.deductee_ledger_id
       join voucher_head vh on vh.voucher_id=d.voucher_id
       join voucher_version vv on vv.id=vh.current_version_id
      where vh.company_id=${q(CO)}
        and vv.payload->>'narration'=${q(`L4 rent withholding ${STAMP}`)}`);
  rec("D3: ⛔ THE RETURN — one deduction row, 194I-B, 20000 against Pune Fabricators",
      ded === "194I-B|20000.0000|Pune Fabricators", ded || "(no deduction row)");
}

// A second and a third deduction, so a deposit can be keyed SHORT and then
// completed — which is the whole of D1 — and then over-claimed, which is D2.
const v2 = await keyDeduction({
  gross: "50000", net: "45000", tax: "5000", section: "194J-D",
  deductee: "Delhi Parts", narration: `L4 fees withholding ${STAMP}`,
});
rec("a second deduction posts (194J-D, 5000)", v2.wire.some((w) => /vouchers\/journal 201/.test(w)), `voucher ${v2.no}`);
const v3 = await keyDeduction({
  gross: "10000", net: "9000", tax: "1000", section: "194J-D",
  deductee: "Acme Traders", narration: `L4 third withholding ${STAMP}`,
});
rec("a third deduction posts (194J-D, 1000)", v3.wire.some((w) => /vouchers\/journal 201/.test(w)), `voucher ${v3.no}`);

// ===========================================================================
// D3's LAST MILE — the deduction REACHES the challan picker.
// ===========================================================================
console.log(`\n── the challan register ──────────────────────────────────────`);
await toGateway();
await app.openFromGateway("TDS Challans", 1800);
rec("Gateway > TDS Challans opens the register (it is NOT in Alt+G)",
    await seen("tds-challans-year"), (await body()).replace(/\s+/g, " ").slice(0, 90));
rec("…and it asks for the tax year rather than guessing it from today",
    await seen("tds-challans-year-prompt"));
await fill("tds-challans-year", "2026");
await page.keyboard.press("Enter");
await page.waitForTimeout(1500);

// ---- Alt+C, the deposit: 25000 of tax paying ONLY the 20000 deduction.
await altMac("c", 900);
rec("Alt+C opens the deposit form", await seen("tds-challan-form"));
rec(`D3: the deduction keyed on F7 is in the picker (voucher ${v1.no})`,
    await seen(`tds-allocatable:${v1.no}:194I-B:0`),
    (await text("tds-allocatable-empty")) || `${v1.no}/${v2.no}/${v3.no} offered`);

await fillByLabel("Deposit date", "2026-08-21");
await fillByLabel("BSR code", "0004329");
await fillByLabel("Challan serial number", SERIAL);
await fillByLabel("Tax", "25000");
await fillByLabel("Tax year this deposit belongs to", "2026");
await fillByLabel("Recorded by", "l4-drive");
await page.getByLabel("Minor head 200", { exact: true }).first().check();
await page.waitForTimeout(200);
// Tick ONLY the first. The deposit is deliberately keyed SHORT — that trap
// state is what D1 exists to get an operator out of.
await testid(`tds-allocatable:${v1.no}:194I-B:0`).first().locator('input[type=checkbox]').check();
await page.waitForTimeout(300);
{
  const before = app.net.length;
  await press("Control+a", 3500);
  const wire = app.net.slice(before);
  rec("the deposit is recorded", wire.some((w) => /POST .*\/tds-challans 201/.test(w)), wire.join(" · "));
}
const unallocatedNow = async () => (await text(`tds-challan-unallocated:${SERIAL}`));
rec(`the register shows challan ${SERIAL} with 5000.0000 of its tax UNALLOCATED`,
    /5000\.0000/.test(await unallocatedNow()), await unallocatedNow());

// ===========================================================================
// D1 — ATTACH TO A DEPOSIT ALREADY RECORDED. THE ORACLE IS THE FALLING FIGURE.
// ===========================================================================
console.log(`\n── D1: the allocate-later route, from the keyboard ───────────`);
await altMac("a", 900);
rec("D1: Alt+A opens the allocate panel", await seen("tds-allocate-form"));
// It must open on OUR challan; with residue from an earlier run it may not, so
// choose it by name rather than trusting the default.
await testid("tds-allocate-challan").first().selectOption({ label: new RegExp(`^${SERIAL} `) }).catch(async () => {
  const opts = await testid("tds-allocate-challan").first().locator("option").allInnerTexts();
  const want = opts.find((o) => o.startsWith(SERIAL + " "));
  if (want) await testid("tds-allocate-challan").first().selectOption({ label: want });
});
await page.waitForTimeout(400);
const beforeFigure = await text("tds-allocate-before");
rec("D1: the panel prints the BEFORE figure, so the fall is visible to an operator",
    /5000\.0000/.test(beforeFigure) && /1 deduction/.test(beforeFigure), beforeFigure.slice(0, 170));

// ⛔ THE ALREADY-PAID ROW CARRIES NO CHECKBOX, so the 409 is unreachable from
// the keyboard — it is shown, and it says WHICH reason applies.
rec("D1: the deduction this challan already paid is shown as paid and cannot be ticked",
    (await seen(`tds-allocatable-paid:${v1.no}:194I-B:0`)) &&
      (await testid(`tds-allocatable:${v1.no}:194I-B:0`).first().locator('input[type=checkbox]').count()) === 0,
    await text(`tds-allocatable-paid:${v1.no}:194I-B:0`));

// The empty-tick refusal comes from the ROUTE, not from a second copy of the
// rule on this screen.
{
  const before = app.net.length;
  await press("Control+a", 2500);
  const wire = app.net.slice(before);
  rec("D1: Ctrl+A with nothing ticked is REFUSED, and the refusal names the return's reason",
      /names no deduction to allocate/i.test(await text("tds-allocate-refusal")) &&
        /only changed by saying WHAT it paid for/i.test(await text("tds-allocate-refusal")),
      `${(await text("tds-allocate-refusal")).slice(0, 150)} | ${wire.join(" · ")}`);
  rec("D1: ⛔ CONTROL — and the figure on the register did NOT move",
      /5000\.0000/.test(await unallocatedNow()), await unallocatedNow());
}

// ---- THE ORACLE.
await testid(`tds-allocatable:${v2.no}:194J-D:0`).first().locator('input[type=checkbox]').check();
await fillByLabel("Attached by", "l4-drive");
await page.waitForTimeout(200);
{
  const before = app.net.length;
  await press("Control+a", 4000);
  const wire = app.net.slice(before);
  rec("D1: the attach reaches the allocate-later route",
      wire.some((w) => /POST .*\/tds-challans\/[0-9a-f-]{36}\/allocations 200/.test(w)), wire.join(" · "));
}
rec("D1: ⛔⛔ THE ORACLE — the unallocated figure on the register FELL 5000.0000 -> 0.0000",
    /0\.0000/.test(await unallocatedNow()) && !/5000\.0000/.test(await unallocatedNow()),
    `before "${beforeFigure.slice(0, 60)}…" after "${await unallocatedNow()}"`);
rec("D1: …and the screen says what the deposit now covers, off the SERVER's re-read",
    /2 deduction/.test(await text("tds-challans-notice")), (await text("tds-challans-notice")).slice(0, 170));
{
  const rows = await sql(
    `select count(*)||'|'||sum(a.allocated_amount_rupees)::text from tds_challan_allocation a
       join tds_challan c on c.id=a.challan_id
      where c.company_id=${q(CO)} and c.challan_no=${q(SERIAL)}`);
  rec("D1: ⛔ THE BOOKS — two allocations are stored against this challan",
      rows === "2|25000.0000", rows || "(none)");
}

// ===========================================================================
// D2 — NOTHING COMPARED ALLOCATIONS AGAINST THE CHALLAN'S TAX BOX.
// ===========================================================================
console.log(`\n── D2: the cap the create applies in one shot ────────────────`);
await altMac("a", 900);
await testid("tds-allocate-challan").first().selectOption({ label: new RegExp(`^${SERIAL} `) }).catch(async () => {
  const opts = await testid("tds-allocate-challan").first().locator("option").allInnerTexts();
  const want = opts.find((o) => o.startsWith(SERIAL + " "));
  if (want) await testid("tds-allocate-challan").first().selectOption({ label: want });
});
await page.waitForTimeout(400);
await testid(`tds-allocatable:${v3.no}:194J-D:0`).first().locator('input[type=checkbox]').check();
await fillByLabel("Attached by", "l4-drive");
{
  const before = app.net.length;
  await press("Control+a", 3500);
  const wire = app.net.slice(before);
  const refusal = await text("tds-allocate-refusal");
  rec("D2: ⛔⛔ attaching ONE AT A TIME cannot pass a cap the create applies in one shot",
      /claim more tax than it deposited/i.test(refusal), refusal.slice(0, 210));
  rec("D2: …and the refusal names BOTH figures and the overshoot",
      /25000/.test(refusal) && /26000/.test(refusal) && /1000 more/.test(refusal), refusal.slice(0, 240));
  rec("D2: the route refused it (400), it was not accepted and quietly dropped",
      wire.some((w) => /POST .*\/allocations 400/.test(w)), wire.join(" · "));
}
{
  const rows = await sql(
    `select count(*)||'|'||sum(a.allocated_amount_rupees)::text from tds_challan_allocation a
       join tds_challan c on c.id=a.challan_id
      where c.company_id=${q(CO)} and c.challan_no=${q(SERIAL)}`);
  rec("D2: ⛔ CONTROL — the books still hold exactly the two allocations that fit",
      rows === "2|25000.0000", rows || "(none)");
}


// ===========================================================================
// D3 (COLLECTION HALF) — A TCS COLLECTION KEYED ON F8.
//
// ⛔ THE BLOCK ALONE WOULD NOT HAVE BEEN A FIX. A `tcs` block records the
// collection for the return and POSTS NOTHING, so the invoice would charge the
// goods and the GST while the return says a further 2% was collected from the
// buyer — money the company must deposit and never billed. The ADDITIONAL
// CHARGE LINE is what makes it real, and the block NAMES that line so the
// server can verify the posted credit against its own derivation.
// ===========================================================================
console.log(`\n── D3: a collection keyed on F8 ──────────────────────────────`);
await toGateway();
await press("F8", 1800);
rec("F8 opens the ACCOUNTING-mode Sales invoice", await seen("gst-panel"));
// ⛔ IT IS A COLLECTION AND NOT A DEDUCTION. A seller collects from its buyer; it
// does not withhold from itself. `primarySide` reads "CR" on Sales AND on the
// credit note, which is exactly why the kind is stated per class.
rec("D3: the sale offers a TCS block, and it is COLLECTED not DEDUCTED",
    (await seen("tax-tcs-open")) && !(await seen("tax-tds-open")),
    (await text("tax-tcs-open")) || "(no block)");
rec("D3: and it offers an ADDITIONAL CHARGE line — the thing the block can name",
    await seen("gst-charges"), (await text("gst-charges")).slice(0, 120));

// ---- The ITEM invoice carries none, and SAYS SO. A silent absence would read
// as "this build has no TCS"; it has one, one Ctrl+H away.
await press("Control+h", 700);
await page.getByText("Item Invoice", { exact: true }).first().click().catch(() => {});
await page.waitForTimeout(900);
if (await seen("item-invoice-panel")) {
  rec("D3: the ITEM invoice offers no TCS block…",
      !(await seen("tax-tcs-open")) && !(await seen("gst-charges")));
  rec("…and says WHY, naming the door that does carry one",
      /generated by the server/i.test(await text("item-statutory-unavailable")) &&
        /Accounting Invoice/.test(await text("item-statutory-unavailable")),
      (await text("item-statutory-unavailable")).slice(0, 170));
  await press("Control+h", 700);
  await page.getByText("Accounting Invoice", { exact: true }).first().click().catch(() => {});
  await page.waitForTimeout(900);
} else {
  rec("D3: the ITEM invoice offers no TCS block…", false, "Ctrl+H did not reach the item grid");
  rec("…and says WHY, naming the door that does carry one", false, "(not reached)");
}
rec("Ctrl+H returns to the accounting invoice", await seen("gst-panel"));

// ---- Key the sale: 100000 of scrap at 18% to a buyer in the home state.
{
  const party = page.getByLabel("Party A/c Name");
  await party.click();
  await page.keyboard.type("Acme Traders");
  await page.waitForTimeout(600);
  await press("Enter", 500);
}
await fill("gst-taxable", "100000");
await fill("gst-rate", "18");
await fill("gst-hsn", "7204");
await page.waitForTimeout(500);

// ⛔ THE LEG PICKER READS THE CHARGE LINES, not the chart. Before a charge is
// keyed it can offer nothing — a leg that is not posted is a verification that
// never runs.
await testid("tax-tcs-open").first().click();
await page.waitForTimeout(400);
{
  const legs = await testid("tax-tcs-leg").first().innerText();
  rec("D3: the collection's leg picker starts empty — it reads the CHARGES, not the chart",
      !/TCS Payable/.test(legs) && !/Acme/.test(legs), legs.replace(/\s+/g, " ").slice(0, 90));
}
await fill("charge-ledger:0", "TCS Payable");
await fill("charge-amount:0", "2000");
await page.waitForTimeout(500);
{
  const legs = await testid("tax-tcs-leg").first().innerText();
  rec("D3: …and the keyed charge line appears in it",
      /TCS Payable/.test(legs), legs.replace(/\s+/g, " ").slice(0, 90));
}
await testid("tax-tcs-section").first().selectOption("206C(1)-SCRAP");
await fill("tax-tcs-party", "Acme Traders");
await fill("tax-tcs-amount", "100000");
await page.waitForTimeout(200);
await testid("tax-tcs-leg").first().selectOption("TCS Payable");
await page.waitForTimeout(300);
rec("D3: the served rate for 206C(1)-SCRAP is echoed, and it is 2%",
    /2%/.test(await text("tax-tcs-rate")), (await text("tax-tcs-rate")).slice(0, 150));
// The bill this invoice opens must total to the PARTY leg, charge included —
// the server refuses any other total, to the paisa.
if (await seen("billrefs-amount:0")) await fill("billrefs-amount:0", "120000");
await page.waitForTimeout(300);

const saleWire = (() => { const n = app.net.length; return () => app.net.slice(n); })();
await press("Control+a", 4500);
rec("D3: the sale POSTS with its tcs block",
    saleWire().some((w) => /POST .*\/vouchers\/sales 201/.test(w)), saleWire().join(" · "));
const saleNo = (await text("voucher-no")).replace(/[^0-9]/g, "");

// ⛔ THE BOOKS, BY SIDE AND BY LEDGER NAME. The buyer is DEBITED 120000 — the
// goods, the GST and the collection — and the collection is its own CREDIT.
{
  const legs = await sql(
    `select string_agg(l.name||':'||p.dr_cr||':'||p.amount::text, ' ' order by l.name)
       from posting p join ledger l on l.id=p.ledger_id
       join voucher_version vv on vv.id=p.voucher_version_id
       join voucher_head vh on vh.current_version_id=vv.id
      where vh.company_id=${q(CO)} and vv.voucher_no=${q(saleNo)}
        and vv.payload->>'class'='Sales'`);
  rec("D3: ⛔ THE BOOKS — the buyer is DEBITED 120000 and TCS Payable CREDITED 2000",
      /Acme Traders:D:120000/.test(legs) && /TCS Payable:C:2000/.test(legs) &&
        /Domestic Sales:C:100000/.test(legs),
      legs || "(no postings)");
}
{
  const col = await sql(
    `select c.section||'|'||c.tcs_amount_rupees||'|'||c.rate_pct||'|'||l.name
       from tcs_collection c join ledger l on l.id=c.buyer_ledger_id
       join voucher_head vh on vh.voucher_id=c.voucher_id
       join voucher_version vv on vv.id=vh.current_version_id
      where vh.company_id=${q(CO)} and vv.voucher_no=${q(saleNo)} and vv.payload->>'class'='Sales'`);
  rec("D3: ⛔ THE RETURN — one collection row, 206C(1)-SCRAP at 2%, 2000 from Acme Traders",
      col === "206C(1)-SCRAP|2000.0000|2.0000|Acme Traders", col || "(no collection row)");
}

// ===========================================================================
// D88 — ALTERING A VOUCHER USED TO SILENTLY DELETE ITS DEDUCTION.
//
// ⛔ THE LOSS IS INVISIBLE ON THE BOOKS. The withheld credit is a posting and
// the postings ARE re-stated, so the entry is identical afterwards. Only the
// RETURN changed — and the challan that paid the deduction keeps an allocation
// whose natural key now matches no row. So this arm reads the deduction table
// and the challan register, never the voucher.
// ===========================================================================
console.log(`\n── D88: the alter round trip ─────────────────────────────────`);
const deductionRow = async () => sql(
  `select d.section||'|'||d.tds_amount_rupees
     from tds_deduction d
     join voucher_head vh on vh.voucher_id=d.voucher_id
     join voucher_version vv on vv.id=vh.current_version_id
    where vh.company_id=${q(CO)} and vv.voucher_no=${q(v1.no)}
      and vv.payload->>'class'='Journal'`);
rec("D88: the deduction is on record before the alter",
    (await deductionRow()) === "194I-B|20000.0000", await deductionRow());

await toGateway();
await altMac("g", 700);
await page.keyboard.type("Day Book");
await page.waitForTimeout(400);
await press("Enter", 2200);
let altered = false;
{
  const rows = page.locator('[role="grid"] [role="row"]');
  const n = await rows.count();
  for (let i = 0; i < n; i++) {
    const txt = (await rows.nth(i).innerText()).replace(/\n/g, " | ");
    if (!txt.includes(`L4 rent withholding ${STAMP}`)) continue;
    await rows.nth(i).click();
    await page.waitForTimeout(250);
    await press("Enter", 2500);
    altered = await seen("double-entry-panel");
    break;
  }
}
rec("D88: the withholding Journal opens for ALTERATION from the Day Book", altered,
    (await body()).replace(/\s+/g, " ").slice(0, 110));

if (altered) {
  // ⛔ VISIBLE ON THE FORM FIRST. A voucher that will re-post a deduction while
  // nothing on screen mentions one is the undisclosed-figure defect; the
  // collapsed block is the surface that stops it.
  rec("D88: the form SAYS the voucher carries a deduction, before anything is sent",
      /194I-B/.test(await text("tax-tds-collapsed")),
      (await text("tax-tds-collapsed")).slice(0, 150));
  // The ordinary round trip: change the narration, nothing else.
  await fill("voucher-narration", `L4 rent withholding ${STAMP} (corrected)`);
  await page.waitForTimeout(300);
  const before = app.net.length;
  await press("Control+a", 4500);
  const wire = app.net.slice(before);
  rec("D88: the alter is accepted",
      wire.some((w) => /PUT .*\/vouchers\/journal\/[0-9a-f-]{36} 200/.test(w)), wire.join(" · "));
  rec("D88: ⛔⛔ THE ORACLE — the deduction SURVIVED an alter that only touched the narration",
      (await deductionRow()) === "194I-B|20000.0000",
      (await deductionRow()) || "(the deduction was DELETED)");
  const narration = await sql(
    `select vv.payload->>'narration' from voucher_version vv
       join voucher_head vh on vh.current_version_id=vv.id
      where vh.company_id=${q(CO)} and vv.voucher_no=${q(v1.no)} and vv.payload->>'class'='Journal'`);
  rec("D88: ⛔ CONTROL — and the alter really took, so the survival is not a no-op",
      narration === `L4 rent withholding ${STAMP} (corrected)`, narration);
}

// ---- AND THE CHALLAN THAT PAID IT IS STILL WHOLE. This is the consequence the
// row is actually about: an orphaned allocation is what the operator would see.
await toGateway();
await app.openFromGateway("TDS Challans", 1800);
await fill("tds-challans-year", "2026");
await page.keyboard.press("Enter");
await page.waitForTimeout(1600);
rec("D88: the challan's allocation is NOT flagged as orphaned by the alter",
    !(await seen(`tds-allocation-flag:${v1.no}:194I-B:0`)),
    (await text(`tds-allocation-flag:${v1.no}:194I-B:0`)) || "(no flag — the deduction is on record)");
rec("D88: …and the register still names the deductee rather than 'no longer on record'",
    !/no longer on record/.test(await text(`tds-allocation:${v1.no}:194I-B:0`)),
    (await text(`tds-allocation:${v1.no}:194I-B:0`)).slice(0, 130));

// ===========================================================================
// D4 — THE TCS HALF OF THE CHALLAN MECHANISM.
// ===========================================================================
console.log(`\n── D4: a collection reaches the deposit, and one challan cannot pay both ──`);
rec("D4: the collection keyed on F8 is in the allocation picker",
    await seen(`tds-allocatable:${saleNo}:206C(1)-SCRAP:0`),
    (await text("tds-allocatable-empty")) || `sale ${saleNo} offered`);

await altMac("c", 900);
rec("D4: Alt+C opens a second deposit form", await seen("tds-challan-form"));
await fillByLabel("Deposit date", "2026-08-21");
await fillByLabel("BSR code", "0004329");
await fillByLabel("Challan serial number", SERIAL2);
await fillByLabel("Tax", "2000");
await fillByLabel("Tax year this deposit belongs to", "2026");
await fillByLabel("Recorded by", "l4-drive");
await page.getByLabel("Minor head 200", { exact: true }).first().check();
await testid(`tds-allocatable:${saleNo}:206C(1)-SCRAP:0`).first().locator('input[type=checkbox]').check();
await page.waitForTimeout(300);
{
  const before = app.net.length;
  await press("Control+a", 3500);
  const wire = app.net.slice(before);
  rec("D4: a deposit against a COLLECTION is recorded",
      wire.some((w) => /POST .*\/tds-challans 201/.test(w)), wire.join(" · "));
}
rec(`D4: challan ${SERIAL2} has 0.0000 of its tax unallocated — the collection is paid`,
    /0\.0000/.test(await text(`tds-challan-unallocated:${SERIAL2}`)),
    await text(`tds-challan-unallocated:${SERIAL2}`));
{
  const stored = await sql(
    `select a.section from tds_challan_allocation a join tds_challan c on c.id=a.challan_id
      where c.company_id=${q(CO)} and c.challan_no=${q(SERIAL2)}`);
  rec("D4: ⛔ THE BOOKS — the allocation on it is the COLLECTION's section",
      stored === "206C(1)-SCRAP", stored || "(none)");
}

// ---- ONE CHALLAN CANNOT PAY BOTH. The return files deductions and collections
// as different statements, so a challan naming both is unfilable.
await altMac("a", 900);
await testid("tds-allocate-challan").first().selectOption({ label: new RegExp(`^${SERIAL2} `) }).catch(async () => {
  const opts = await testid("tds-allocate-challan").first().locator("option").allInnerTexts();
  const want = opts.find((o) => o.startsWith(SERIAL2 + " "));
  if (want) await testid("tds-allocate-challan").first().selectOption({ label: want });
});
await page.waitForTimeout(400);
await testid(`tds-allocatable:${v3.no}:194J-D:0`).first().locator('input[type=checkbox]').check();
await fillByLabel("Attached by", "l4-drive");
{
  const before = app.net.length;
  await press("Control+a", 3500);
  const wire = app.net.slice(before);
  const refusal = await text("tds-allocate-refusal");
  rec("D4: ⛔⛔ one challan is REFUSED when asked to pay a deduction AND a collection",
      /cannot pay both/i.test(refusal), refusal.slice(0, 200));
  rec("D4: the route refused it — it was not accepted and quietly dropped",
      wire.some((w) => /POST .*\/allocations 400/.test(w)), wire.join(" · "));
}
{
  const stored = await sql(
    `select count(*)::text from tds_challan_allocation a join tds_challan c on c.id=a.challan_id
      where c.company_id=${q(CO)} and c.challan_no=${q(SERIAL2)}`);
  rec("D4: ⛔ CONTROL — the deposit still pays exactly the one collection",
      stored === "1", stored || "(none)");
}

// ===========================================================================
// THE TENANT IS HANDED ON LEVEL. Three balanced Journals leave it that way —
// gotcha #4 says to prove that rather than assume it.
// ===========================================================================
console.log(`\n── the tenant, handed on ─────────────────────────────────────`);
{
  const tb = await api("GET", `/v1/companies/${CO}/trial-balance`);
  rec("the Trial Balance says it is BALANCED — the three Journals left no residue",
      tb.body.balanced === true,
      `balanced=${tb.body.balanced} Dr ${tb.body.total_debit} / Cr ${tb.body.total_credit}`);
}

console.log(`\n(challan ${SERIAL}; vouchers ${v1.no}/${v2.no}/${v3.no}; stamp ${STAMP})`);
console.log("NON-GET WIRE:", app.net.join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
