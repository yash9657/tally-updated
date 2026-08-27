/**
 * B1 — ALT+E WRITES THE SERVER'S PDF, driven in a real browser.
 *
 * ⛔ WHAT THIS PROVES THAT THE VITEST SUITE CANNOT. jsdom has no Blob URLs, no
 * navigation and no PDF anything: `exportPdf.test.tsx` stubs `createObjectURL`
 * and the anchor's own `click`, so what it asserts is that the right bytes were
 * handed to the right sink. This probe reads the FILE CHROMIUM ACTUALLY SAVED and
 * checks it is a real document — `%PDF-` at the front, `%%EOF` at the back, and
 * the company's own name inside it.
 *
 * THE ORACLES, each chosen so a wrong answer FAILS rather than looks plausible:
 *
 *   1. THE F12 ROW EXISTS ON A REPORT THAT PRINTS AND IS ABSENT ON ONE THAT DOES
 *      NOT — and where it is absent the panel NAMES the report and says 501,
 *      because an absent control is indistinguishable from one that failed to
 *      render.
 *
 *   2. ALT+E SAVES A FILE, and the file is a PDF rather than a CSV with a .pdf
 *      name. A build that fell through to the sheet writer would still download
 *      something, still show a success line, and fail here on the first four
 *      bytes.
 *
 *   3. ⛔⛔ THE STATUS LINE NAMES THE Ctrl+F FILTER THAT WAS NOT APPLIED. This is
 *      the assertion the whole slice turns on. Alt+E's other two formats write
 *      what is DISPLAYED; the PDF is the server's WHOLE statement. They are two
 *      different documents under one key, and an operator who filters to one
 *      party and exports must not send a customer the whole ledger believing it
 *      was filtered.
 *
 *   4. AND THE FILTER REALLY IS ACTIVE ON SCREEN when that line is asserted — a
 *      warning about a filter nobody applied is a vacuous probe.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY OF ITS OWN, NOT ON Demo Traders, AND TEARS IT
 * DOWN.
 *
 * RUN:  node frontend/scripts/drive-b1-pdf-export.mjs
 *       (needs cored :8087 and vite :5173 — the port matters, WebAuthn origin
 *        matching is exact. It creates its own company.)
 */
import { openApp, rec, summary } from "./audit-harness.mjs";
import { readFileSync } from "node:fs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `PDF Export Drive ${stamp}`;

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

const sql = async (query) => {
  const { execFile } = await import("node:child_process");
  return new Promise((res, rej) => {
    execFile(
      "psql",
      ["-h", "127.0.0.1", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", query],
      { env: { ...process.env, PGPASSWORD: "postgres" } },
      (err, stdout) => (err ? rej(err) : res(stdout.trim())),
    );
  });
};
const q = (s) => `'${String(s).replace(/'/g, "''")}'`;

// ---- The tenant and one posted sale, over HTTP. The vouchers are not what this
// probe is about; keying them through the grid would make an export failure
// indistinguishable from a voucher-entry failure.
const TOK = (
  await j("POST", "/auth/token", {
    body: { subject: "browser-dev-owner", amr: ["webauthn"] },
  })
).body.token;
const created = await j("POST", "/v1/companies", {
  token: TOK,
  body: { name: COMPANY_NAME, fy_start_date: "2026-04-01", state_code: "29" },
});
if (created.status !== 201 && created.status !== 200) {
  throw new Error(
    `drive-b1: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
console.log(`company ${COMPANY_NAME} = ${CO}`);

const base = `/v1/companies/${CO}`;
const ledger = async (name, group) =>
  (
    await j("POST", `${base}/ledgers`, {
      token: TOK,
      body: { name, group_name: group, created_by: "drive-b1" },
    })
  ).body.ledger_id;
const DEBTOR = await ledger("Acme Trading", "Sundry Debtors");
const SALES = await ledger("Export Drive Sales", "Sales Accounts");
const posted = await j("POST", `${base}/vouchers/sales`, {
  token: TOK,
  body: {
    voucher_no: "PDF-1",
    voucher_date: "2026-04-10",
    party: "Acme Trading",
    created_by: "drive-b1",
    postings: [
      { ledger_id: DEBTOR, dr_cr: "D", amount: "2500.0000" },
      { ledger_id: SALES, dr_cr: "C", amount: "2500.0000" },
    ],
  },
});
if (posted.status !== 201) {
  throw new Error(`drive-b1: seed voucher failed: ${posted.status} ${JSON.stringify(posted.body)}`);
}

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, altMac, toGateway } = app;

const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const text = async (t) =>
  (await seen(t))
    ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim()
    : "";
const status = () => text("status-line");

/**
 * Alt+G Go To, type a report name, Enter.
 *
 * ⚠️ NOT `openFromGateway`. That helper drives the GATEWAY menu, which carries
 * screens rather than report names — "Trial Balance" is not on it, and the first
 * version of this probe used it and never left the Gateway. Alt+G's picker is
 * hard-wired to ["Vouchers", ...REPORT_NAMES], which is exactly the set this
 * probe needs and exactly why the T32 screen could NOT use it.
 */
const goTo = async (name, wait = 1800) => {
  await toGateway();
  await altMac("g");
  await page.waitForTimeout(300);
  await page.keyboard.type(name);
  await page.waitForTimeout(400);
  await press("Enter", wait);
};

try {
  // =========================================================================
  // 1. THE F12 ROW EXISTS WHERE A LAYOUT DOES.
  // =========================================================================
  await goTo("Trial Balance");
  rec(
    "control: the Trial Balance is up before anything is exported",
    (await body()).includes("Trial Balance"),
    (await body()).slice(0, 90),
  );

  await press("F12", 700);
  const pdfRowThere = await seen("cfg-export-pdf");
  rec(
    "F12 offers 'Export as PDF' on a report the server has a layout for",
    pdfRowThere,
    pdfRowThere ? await text("cfg-export-pdf") : "the row is absent",
  );
  rec(
    "⛔ ...and the row's LABEL says the PDF is the whole statement, not the rows on screen",
    /whole statement/i.test(await text("cfg-export-pdf")) &&
      /not the rows on screen/i.test(await text("cfg-export-pdf")),
    await text("cfg-export-pdf"),
  );

  // =========================================================================
  // 2. ALT+E SAVES A REAL PDF.
  // =========================================================================
  await testid("cfg-export-pdf").first().click();
  await press("Escape", 500);

  const download = page.waitForEvent("download", { timeout: 30_000 });
  await page.keyboard.press("Alt+KeyE");
  // ⚠️ The harness's altMac is for Alt+<letter> bindings that a Mac COMPOSES.
  // Alt+E is a global keymap binding resolved on `code`, and Playwright's
  // "Alt+KeyE" sends exactly that. If this ever stops arriving, send the composed
  // "´" with code KeyE the way drive-t32 does for Option+O.
  const dl = await download;
  const path = await dl.path();
  const suggested = dl.suggestedFilename();
  const bytes = readFileSync(path);
  const head = bytes.subarray(0, 5).toString("latin1");
  const tail = bytes.subarray(-1024).toString("latin1");

  rec(
    "Alt+E downloads a file, named for the report",
    /\.pdf$/.test(suggested) && /trial-balance/i.test(suggested),
    suggested,
  );
  // ⛔ THE ASSERTION A FALL-THROUGH TO THE SHEET WRITER FAILS. A CSV named .pdf
  // downloads happily and reports success.
  rec(
    "⛔ and it is a REAL PDF — %PDF- at the front, %%EOF at the back",
    head === "%PDF-" && tail.includes("%%EOF"),
    `${bytes.length} bytes, head=${JSON.stringify(head)}, has EOF=${tail.includes("%%EOF")}`,
  );
  rec(
    "the document carries this company's own letterhead",
    bytes.toString("latin1").includes(COMPANY_NAME) ||
      bytes.toString("latin1").includes("PDF Export Drive"),
    `looked for ${COMPANY_NAME} in ${bytes.length} bytes`,
  );

  // =========================================================================
  // 3. THE STATUS LINE SAYS WHAT THE FILE CONTAINS.
  // =========================================================================
  const clean = await status();
  rec(
    "the status line says the PDF is the SERVER'S whole statement",
    /whole statement/i.test(clean) && /not the rows on screen/i.test(clean),
    clean.slice(0, 200),
  );
  rec(
    "...and with nothing narrowed it says so plainly rather than hedging",
    /nothing is narrowed on screen/i.test(clean),
    clean.slice(0, 200),
  );

  // =========================================================================
  // 4. ⛔⛔ AND WITH A FILTER ON, IT NAMES THE FILTER.
  // =========================================================================
  await page.keyboard.press("Control+KeyF");
  await page.waitForTimeout(400);
  const filterBox = testid("filter-text").first();
  rec(
    "control: the Ctrl+F filter panel opened",
    (await filterBox.count()) > 0,
    (await body()).slice(0, 90),
  );
  await filterBox.fill("Acme");
  await page.waitForTimeout(400);
  await press("Escape", 400);

  // ⛔ THE CONTROL THAT STOPS THIS BEING VACUOUS: the filter must really be
  // narrowing the screen. Warning about a filter nobody applied proves nothing.
  const filteredBody = await body();
  rec(
    "control: the filter is ACTIVE — the screen is narrowed to Acme",
    filteredBody.includes("Acme") && !filteredBody.includes("Export Drive Sales"),
    filteredBody.includes("Export Drive Sales")
      ? "the sales ledger is STILL on screen, so the filter did not narrow anything"
      : "narrowed to Acme",
  );

  const download2 = page.waitForEvent("download", { timeout: 30_000 });
  await page.keyboard.press("Alt+KeyE");
  await download2;
  const filtered = await status();
  rec(
    '⛔⛔ the status line NAMES the Ctrl+F filter that was NOT applied to the PDF',
    /Ctrl\+F filter "Acme"/.test(filtered) && /NOT applied/.test(filtered),
    filtered.slice(0, 240),
  );

  // =========================================================================
  // 5. AND A REPORT WITH NO LAYOUT SAYS SO INSTEAD OF OFFERING PDF.
  // =========================================================================
  await goTo("Sales Order Book");
  await press("F12", 700);
  const noteThere = await seen("cfg-export-pdf-note");
  rec(
    "control: the F12 panel is open on the Sales Order Book",
    noteThere,
    (await body()).slice(0, 90),
  );
  rec(
    "⛔ PDF is NOT offered on a report the server answers 501 for",
    !(await seen("cfg-export-pdf")),
    (await seen("cfg-export-pdf")) ? "the row is offered and it should not be" : "absent",
  );
  const note = await text("cfg-export-pdf-note");
  rec(
    "...and the panel NAMES the report and says 501, rather than silently lacking a row",
    /no printed layout/i.test(note) && /501/.test(note),
    note.slice(0, 200),
  );
  rec(
    "...while making clear the sheet formats still work",
    /CSV and Excel XML/i.test(note),
    note.slice(0, 200),
  );
} finally {
  try {
    await sql(`
DO $$
DECLARE t text;
BEGIN
  SET LOCAL session_replication_role = replica;
  EXECUTE format('DELETE FROM statutory_step WHERE workflow_id LIKE %L', 'irn:' || ${q(CO)} || ':%');
  FOR t IN
    SELECT c.table_name FROM information_schema.columns c
      JOIN information_schema.tables tb USING (table_schema, table_name)
     WHERE c.table_schema='public' AND c.column_name='company_id'
       AND tb.table_type='BASE TABLE' AND c.table_name <> 'company'
  LOOP
    EXECUTE format('DELETE FROM %I WHERE company_id::text = %L', t, ${q(CO)});
  END LOOP;
  DELETE FROM company WHERE id = ${q(CO)};
END $$;`);
    const left = await sql(`SELECT count(*) FROM company WHERE id = ${q(CO)}`);
    console.log(`\ntorn down ${COMPANY_NAME}: ${left === "0" ? "clean" : `LEAKED (${left} rows)`}`);
  } catch (e) {
    console.log(`\n⚠️ teardown FAILED for ${CO}: ${e}`);
  }

  console.log("\n--- non-GET browser requests (status last) ---");
  for (const r of app.net) console.log("  " + r);
  console.log("\n--- console errors ---");
  for (const e of app.consoleErrors.slice(0, 8)) console.log("  " + e);
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);
