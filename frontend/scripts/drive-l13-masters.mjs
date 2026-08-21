/**
 * L13 — MASTERS. D36 (no keyboard path to an opening balance, an opening Dr/Cr,
 * a bill-wise opening breakup, or maintain_bill_by_bill on ANY ledger), D47 (a
 * refused master creation is reported into a screen that has already been
 * unmounted), and ROW 20's masters gap (no keystroke reaches masterKind
 * "stock-item" or "unit", so a Kg item is impossible).
 *
 * ⛔ THE ORACLE IS THE BOOKS, THROUGH A DIFFERENT SUBSYSTEM. A master form that
 * reports success from a status line looks identical whether it persisted the
 * four D36 fields or discarded them — which is exactly the D5/D43 shape
 * (`allow_negative_stock` is accepted at 201 and thrown away). So every field
 * keyed here is read back from a surface the form does not own: the ledger row
 * and its opening bills straight out of Postgres, and the OUTSTANDINGS REPORT,
 * which is the product's own reader of `ledger_opening_bill`.
 *
 * ⛔ EVERY NAME IS UNIQUE PER RUN. Masters cannot be deleted from the keyboard,
 * so a fixed name makes the SECOND run of this probe fail on a duplicate — and
 * it would fail inside D36's arm, reading as the fix having regressed. The
 * duplicate is used ON PURPOSE exactly once, in D47's arm.
 *
 * ⛔⛔ IT WRITES REAL MASTERS AND IT TEARS THEM DOWN AGAIN — gotcha #4, learned
 * here the hard way. A 5000.0000 opening Dr keyed on ONE ledger is a one-sided
 * opening: the Trial Balance then nets to 15000.0000 short after three runs and
 * `verify:demo` drops to 29/34. That is not a product defect (Tally itself shows
 * a "difference in opening balances" until every opening is keyed) — it is a
 * probe leaving residue, and the residue reads as a broken Balance Sheet to the
 * next lane. Masters cannot be deleted from the keyboard, so the teardown at the
 * bottom removes exactly the rows this file created, by NAME, AFTER every
 * assertion has read them back, and then re-asserts the books are level again.
 *
 * The stock item deliberately carries NO opening quantity, for the same reason:
 * a valued opening stock moves the Balance Sheet on its own.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l13-masters.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO;
const CORED = "http://localhost:8087";
if (!CO) throw new Error("drive-l13-masters: CO is required (the seeded company id)");

const stamp = Math.random().toString(36).slice(2, 7);
const LEDGER = `L13 Opening Debtor ${stamp}`;
const UNIT = `Qtl${stamp}`;
const ITEM = `L13 Steel Coil ${stamp}`;
const REF_A = `L13/OB/${stamp}/1`;
const REF_B = `L13/OB/${stamp}/2`;

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
const type = async (t, v) => {
  const el = testid(t).first();
  await el.click();
  await el.fill(v);
};
/** The shell's status line — it lives ABOVE the screen stack and survives a pop. */
const statusLine = () => text("status-line");

/** Alt+G > <label> — the Go To door row 20 says did not reach masters at all. */
const goTo = async (label) => {
  await toGateway();
  await altMac("g");
  await page.keyboard.type(label);
  await page.waitForTimeout(300);
  await press("Enter", 900);
};

// ===========================================================================
// ROW 20 — THE TWO MASTER KINDS NO KEYSTROKE USED TO REACH.
// ===========================================================================
console.log(`\n── ROW 20: masterKind "unit" and "stock-item" from the keyboard ──`);

await goTo("Create Unit");
rec("Alt+G > Create Unit reaches masterKind \"unit\"", await seen("master-create:unit"),
    (await body()).split("\n")[0]);
{
  await type("master-name", UNIT);
  await press("Tab", 250); // no second field on a unit: the cursor lands on details
  rec("the unit's DETAILS cursor lands on a real control (formal name)",
      await page.evaluate(() =>
        document.activeElement?.getAttribute("data-testid") === "master-unit-formal-name"),
      await page.evaluate(() => document.activeElement?.getAttribute("data-testid") ?? "(none)"));
  await type("master-unit-formal-name", `Quintal ${stamp}`);
  await press("Control+a", 1200);
  rec(`the unit "${UNIT}" is created, and the status line says so`,
      /Created unit/.test(await statusLine()), await statusLine());
}
rec("…and it is really in the books",
    (await sql(`select count(*) from unit where company_id=${q(CO)} and symbol=${q(UNIT)}`)) === "1");

await goTo("Create Stock Item");
rec("Alt+G > Create Stock Item reaches masterKind \"stock-item\"", await seen("master-create:stock-item"));
{
  await type("master-name", ITEM);
  // ⛔ THE UNIT IS CHOSEN, NOT DEFAULTED. Row 20's headline is "a Kg item is
  // impossible", so this arm asks for Kg by name rather than taking whatever
  // the picker offers first.
  const picker = page.getByRole("combobox").first();
  await picker.click();
  await picker.fill("Kg");
  await page.waitForTimeout(250);
  await press("Enter", 300);
  rec("the units picker accepts Kg", (await text("master-parent-echo")).includes("Kg"),
      await text("master-parent-echo"));
  await press("Tab", 250);
  await type("master-item-hsn", "7208");
  await type("master-item-gst-rate", "18");
  await press("Control+a", 1200);
  rec(`a Kg stock item is created ("${ITEM}")`, /Created stock item/.test(await statusLine()),
      await statusLine());
}
{
  const row = await sql(
    `select si.name||'|'||u.symbol||'|'||coalesce(si.hsn,'') from stock_item si
       join unit u on u.id = si.unit_id
      where si.company_id=${q(CO)} and si.name=${q(ITEM)}`);
  rec("…and the books hold it in Kg with its HSN — not the silent \"Nos\"",
      row === `${ITEM}|Kg|7208`, row || "(no row)");
}

// ===========================================================================
// D36 — AN OPENING BALANCE, A SIDE, BILL-BY-BILL AND A BREAKUP, ALL KEYED.
// ===========================================================================
console.log(`\n── D36: the four fields that had no keyboard path ────────────`);

await goTo("Create Ledger");
rec("Alt+G > Create Ledger opens the ledger form", await seen("master-create:ledger"));
await type("master-name", LEDGER);
{
  const picker = page.getByRole("combobox").first();
  await picker.click();
  await picker.fill("Sundry Debtors");
  await page.waitForTimeout(250);
  await press("Enter", 300);
  rec("the ledger is filed under Sundry Debtors",
      (await text("master-parent-echo")).includes("Sundry Debtors"), await text("master-parent-echo"));
}
// ⛔ THE CURSOR. D36's traced cause was that MASTER_FIELDS' "details" position
// had NO CONTROL BEHIND IT — the form rendered two inputs and Enter advanced a
// cursor into nothing. Assert where the focus actually goes, not that a field
// exists.
await press("Enter", 350);
rec("the DETAILS cursor lands on the opening-balance field (it used to land on nothing)",
    await page.evaluate(() =>
      document.activeElement?.getAttribute("data-testid") === "master-opening-balance"),
    await page.evaluate(() => document.activeElement?.getAttribute("data-testid") ?? "(none)"));

await type("master-opening-balance", "5000");
await testid("master-opening-dc-D").first().check();
await testid("master-billwise").first().check();
await page.waitForTimeout(200);
rec("ticking bill-by-bill reveals the opening breakup", await seen("master-opening-bills"));

// A BREAKUP THAT DOES NOT ADD UP IS CAUGHT WHILE THE FIGURES ARE STILL ON SCREEN.
await type("master-bill-0-ref", REF_A);
await type("master-bill-0-amount", "3000");
await page.waitForTimeout(200);
rec("the running difference names the shortfall BEFORE the frame pops",
    /2000\.0000 unaccounted/.test(await text("master-bill-difference")),
    await text("master-bill-difference"));

await testid("master-bill-add").first().click();
await page.waitForTimeout(200);
await type("master-bill-1-ref", REF_B);
await type("master-bill-1-amount", "2000");
await testid("master-bill-0-due").first().fill("2026-09-30");
await testid("master-bill-1-due").first().fill("2026-10-31");
await page.waitForTimeout(250);
rec("…and says so when it adds up", /it adds up/.test(await text("master-bill-difference")),
    await text("master-bill-difference"));

await press("Control+a", 1400);
rec("Ctrl+A creates the ledger and the status line states the opening",
    /Created ledger/.test(await statusLine()) && /5000\.0000 D/.test(await statusLine()),
    await statusLine());

// ---- READ THE BOOKS BACK, through surfaces the form does not own.
{
  // ⚠️ `psql -tAc` renders a boolean as "true"/"false", NOT "t"/"f" (that is
  // the ALIGNED table format). The first run of this probe failed here on its
  // own expectation while the product was correct — gotcha #2, again.
  const row = await sql(
    `select opening_balance||'|'||opening_dc||'|'||maintain_bill_by_bill
       from ledger where company_id=${q(CO)} and name=${q(LEDGER)}`);
  rec("the ledger carries the opening balance, the SIDE, and maintain_bill_by_bill",
      row === "5000.0000|D|true", row || "(no ledger row)");
}
{
  const bills = await sql(
    `select string_agg(ob.ref_name||'/'||ob.dr_cr||'/'||ob.amount||'/'||ob.due_date, ' ' order by ob.ref_name)
       from ledger_opening_bill ob join ledger l on l.id = ob.ledger_id
      where l.company_id=${q(CO)} and l.name=${q(LEDGER)}`);
  rec("both opening bills are on the books, by NAME, on the DEBIT side, with their due dates",
      bills === `${REF_A}/D/3000.0000/2026-09-30 ${REF_B}/D/2000.0000/2026-10-31`,
      bills || "(no opening bills)");
}
// THE PRODUCT'S OWN READER of ledger_opening_bill. A master form that wrote
// rows nothing reports is not a keyboard path to an opening balance.
{
  const r = await api("GET", `/v1/companies/${CO}/outstandings?kind=RECEIVABLE`);
  const party = (r.body.parties ?? []).find((p) => p.ledger === LEDGER);
  const refs = (party?.bills ?? []).map((b) => `${b.ref_name}=${b.pending}`).sort();
  rec("the OUTSTANDINGS REPORT shows the new party's two opening references",
      party?.pending === "5000.0000" &&
        refs.join(",") === [`${REF_A}=3000.0000`, `${REF_B}=2000.0000`].sort().join(","),
      `${party?.pending ?? "(absent)"} — ${refs.join(", ")}`);
}
// AND ON SCREEN, in a report the operator can actually open.
{
  await toGateway();
  await altMac("g");
  await page.keyboard.type("Outstandings");
  await page.waitForTimeout(300);
  await press("Enter", 1500);
  const shown = await body();
  rec("…and the Outstandings SCREEN names the party it created",
      shown.includes(LEDGER), shown.includes(LEDGER) ? "present" : shown.slice(0, 140));
}

// ===========================================================================
// D47 — A REFUSAL MUST REACH THE OPERATOR AFTER THE FRAME HAS POPPED.
// ===========================================================================
console.log(`\n── D47: the refusal that used to reach nobody ────────────────`);

await goTo("Create Ledger");
await type("master-name", LEDGER); // the SAME name — the server refuses it
{
  const picker = page.getByRole("combobox").first();
  await picker.click();
  await picker.fill("Sundry Debtors");
  await page.waitForTimeout(250);
  await press("Enter", 300);
}
await press("Control+a", 1600);
{
  const msg = await statusLine();
  const backAtGateway = (await body()).includes("MASTERS / TRANSACTIONS");
  rec("the master-create frame really did pop (so a screen-local message would be lost)",
      backAtGateway, backAtGateway ? "at the Gateway" : (await body()).slice(0, 100));
  rec("the SERVER's refusal reaches the operator on the shell's status line",
      /Could not create ledger/.test(msg) && msg.includes(LEDGER), msg.slice(0, 170));
  rec("…and it says nothing was created", /nothing was created/i.test(msg), msg.slice(0, 170));
  // ⛔ FOUND BY THIS DRIVE. D47's fix delivered the refusal; what it delivered
  // was `duplicate key value violates unique constraint
  // "ledger_company_id_name_key" (SQLSTATE 23505)`. A refusal an accountant
  // cannot read is barely better than the silence D47 records, and it hands out
  // the schema's constraint names. Now mapped to a sentence (ErrMasterNameTaken).
  rec("…and it is a SENTENCE, not a Postgres error",
      /this company already has a ledger named/.test(msg) &&
        !/SQLSTATE|unique constraint|duplicate key/.test(msg),
      msg.slice(0, 170));
}
// ⛔ NOTHING WAS CREATED IS A CLAIM ABOUT THE BOOKS, SO CHECK THE BOOKS.
rec("and the books agree: still exactly ONE ledger of that name",
    (await sql(`select count(*) from ledger where company_id=${q(CO)} and name=${q(LEDGER)}`)) === "1");

// A CLIENT-SIDE refusal must reach the operator too, and it must NOT reach the
// server: this arm is refused by `build` before any request is made.
await goTo("Create Ledger");
await type("master-name", `L13 Unbalanced ${stamp}`);
{
  const picker = page.getByRole("combobox").first();
  await picker.click();
  await picker.fill("Sundry Debtors");
  await page.waitForTimeout(250);
  await press("Enter", 300);
}
await press("Enter", 300);
await type("master-opening-balance", "5000");
await testid("master-billwise").first().check();
await page.waitForTimeout(200);
await type("master-bill-0-ref", `${REF_A}/x`);
await type("master-bill-0-amount", "3000");
await press("Control+a", 1200);
{
  const msg = await statusLine();
  rec("a breakup that does not add up is refused, naming the difference",
      /sums to 3000\.0000 but the opening balance is 5000\.0000/.test(msg) &&
        /difference is 2000\.0000/.test(msg),
      msg.slice(0, 170));
  rec("…and nothing was created",
      (await sql(`select count(*) from ledger where company_id=${q(CO)} and name=${q(`L13 Unbalanced ${stamp}`)}`)) === "0");
}

// THE INLINE Alt+C DOOR — the one the comment says it cannot fully undo, so the
// message has to SAY the field now names a master that does not exist.
console.log(`\n── D47: the inline Alt+C door from a voucher field ───────────`);
{
  await toGateway();
  await press("F8", 900); // Sales
  const onVoucher = (await body()).includes("Sales");
  rec("a voucher screen is open to create from", onVoucher, (await body()).split("\n")[0]);
  await altMac("c");
  await page.waitForTimeout(600);
  if (await seen("master-create:ledger")) {
    await type("master-name", LEDGER); // duplicate again -> the server refuses
    const picker = page.getByRole("combobox").first();
    await picker.click();
    await picker.fill("Sundry Debtors");
    await page.waitForTimeout(250);
    await press("Enter", 300);
    await press("Control+a", 1600);
    const msg = await statusLine();
    rec("an inline create refused by the server still reaches the status line",
        /Could not create ledger/.test(msg) && !/SQLSTATE/.test(msg), msg.slice(0, 150));
    rec("…and it warns that the voucher field now names a master that does not exist",
        /clear it before saving the voucher/.test(msg), msg.slice(0, 200));
  } else {
    rec("Alt+C from a voucher field opens the inline master-create frame", false,
        (await body()).slice(0, 140));
  }
}

// ===========================================================================
// ⛔ NEW, FOUND BY THIS DRIVE — registered as D87, deliberately NOT fixed here.
// The ledger form can key an opening balance now, but it still cannot key a
// PARTY'S GSTIN, STATE CODE or REGISTRATION TYPE, and `masters.CreateLedger`
// has accepted all three since it was written. So every Sundry Debtor/Creditor
// created from the keyboard has state_code NULL — which is exactly the input
// that made D19 derive an intra-state bill as IGST — and the repo's own
// `verify:demo` flags it ("Every party's GSTIN prefix == its state_code").
// ===========================================================================
console.log(`\n── D87 (new): a keyed party has no GSTIN and no state ────────`);
{
  await goTo("Create Ledger");
  const form = await body();
  rec("the ledger form offers no GSTIN / state-code / registration-type field",
      !/GSTIN/i.test(form) && !/State code/i.test(form) && !/Registration type/i.test(form),
      form.replace(/\s+/g, " ").slice(0, 130));
  await press("Escape", 400);
  const row = await sql(
    `select coalesce(gstin,'(null)')||'|'||coalesce(state_code,'(null)')||'|'||coalesce(registration_type::text,'(null)')
       from ledger where company_id=${q(CO)} and name=${q(LEDGER)}`);
  rec("…and the party it created really carries none of them",
      row === "(null)|(null)|UNREGISTERED" || row.startsWith("(null)|(null)|"),
      row || "(no row)");
}

// ===========================================================================
// TEARDOWN. Everything above has already been read back; remove the residue so
// the drive tenant is handed on level. Ordered by dependency; the superuser is
// used because RLS hides the rows from `tally_app`.
// ===========================================================================
console.log(`\n── TEARDOWN ─────────────────────────────────────────────────`);
await sql(
  `delete from ledger_opening_bill where ledger_id in
     (select id from ledger where company_id=${q(CO)} and name=${q(LEDGER)})`);
await sql(`delete from ledger_balance where ledger_id in
     (select id from ledger where company_id=${q(CO)} and name=${q(LEDGER)})`);
await sql(`delete from ledger where company_id=${q(CO)} and name=${q(LEDGER)}`);
await sql(`delete from stock_item where company_id=${q(CO)} and name=${q(ITEM)}`);
await sql(`delete from unit where company_id=${q(CO)} and symbol=${q(UNIT)}`);
rec("teardown removed every master this run created",
    (await sql(
      `select (select count(*) from ledger where company_id=${q(CO)} and name=${q(LEDGER)})
            + (select count(*) from stock_item where company_id=${q(CO)} and name=${q(ITEM)})
            + (select count(*) from unit where company_id=${q(CO)} and symbol=${q(UNIT)})`)) === "0");
// ⛔ THE ASSERTION GOTCHA #4 SHOULD HAVE BEEN ALL ALONG: the tenant is level.
{
  // The report answers `balanced` itself — its own arithmetic, not the probe's.
  const tb = await api("GET", `/v1/companies/${CO}/trial-balance`);
  rec("the Trial Balance says it is BALANCED again — the probe left no residue",
      tb.body.balanced === true,
      `balanced=${tb.body.balanced} Dr ${tb.body.total_debit} / Cr ${tb.body.total_credit}`);
}

console.log(`\n(ledger: ${LEDGER}; unit: ${UNIT}; item: ${ITEM} — all removed)`);
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
