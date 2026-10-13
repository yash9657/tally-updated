/**
 * B2 — ALT+M E-MAILS A DOCUMENT, driven in a real browser.
 *
 * ✅ THE DECISION: **this product does not send mail.** Alt+M saves the server's
 * PDF and hands the operator's OWN mail client a draft. `internal/notify` is
 * untouched.
 *
 * ⛔ WHAT THIS PROVES THAT THE VITEST SUITE CANNOT:
 *
 *   1. ⚠️ **THE COMPOSED KEY ACTUALLY FIRES.** macOS composes Option+M into "µ",
 *      and every Alt binding in this product was once found dead on a Mac while
 *      every browser test passed — because Playwright's `press("Alt+m")`
 *      synthesises a `key` a Mac never produces. This sends the composed
 *      character with `code: "KeyM"`, which is what the hardware sends.
 *
 *   2. **A REAL FILE LANDS ON DISK**, through Chromium's own download machinery,
 *      and it is a real PDF (`%PDF-` at the front, `%%EOF` at the back). jsdom
 *      has no Blob URLs at all: `emailDocument.test.tsx` stubs
 *      `createObjectURL`, so what it asserts is that the right bytes reached the
 *      right sink.
 *
 *   3. ⛔⛔ **THE `mailto:` URL CHROMIUM WAS ASKED TO OPEN.** Setting
 *      `location.href` to a `mailto:` fires a real `page.on("request")` in
 *      Chromium — measured — so the exact encoded URL is observable here, on the
 *      real thing, rather than through a stub. The page does NOT navigate away,
 *      which is the property that made `location.href` the right sink over
 *      `window.open` (a pop-up blocker eats the latter after an await).
 *
 *   4. **BOTH EFFECTS REALLY HAPPEN**, against a real browser — and the refusal
 *      path really produces neither.
 *
 * ⚠️ WHAT THIS PROBE DELIBERATELY DOES **NOT** ASSERT IS THE ORDER. Chromium
 * emits the `mailto:` request synchronously from the renderer while a `download`
 * event must cross to the browser process, so the observed event order is
 * DELIVERY LATENCY rather than call order — the first version of this probe
 * asserted on it and failed against provably correct code. The strict ordering
 * oracle is `emailDocument.test.tsx`, where both sinks push to one synchronous
 * array and an executed mutation that drafts first turns it red.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY OF ITS OWN, NOT ON Demo Traders, AND TEARS IT
 * DOWN. Residue drops `verify:demo` below 34/34.
 *
 * RUN:  node frontend/scripts/drive-b2-email-document.mjs
 *       (needs cored :8087 and vite :5173 — the port matters, WebAuthn origin
 *        matching is exact. It creates its own company.)
 */
import { readFileSync } from "node:fs";
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Email Drive ${stamp}`;

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

// ---- The tenant and one posted sale, over HTTP. The voucher is not what this
// probe is about; keying it through the grid would make an e-mail failure
// indistinguishable from a voucher-entry failure.
const TOK = (
  await j("POST", "/auth/token", {
    body: { subject: "browser-dev-owner", amr: ["webauthn"] },
  })
).body.token;

const created = await j("POST", "/v1/companies", {
  token: TOK,
  body: {
    name: COMPANY_NAME,
    fy_start_date: "2026-04-01",
    state_code: "29",
    gstin: "29AABCU9603R1ZM",
    address: "14 Residency Road\nShanthala Nagar",
    city: "Bengaluru",
    pincode: "560025",
  },
});
if (created.status !== 201 && created.status !== 200) {
  throw new Error(
    `drive-b2: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
console.log(`company ${COMPANY_NAME} = ${CO}`);

const base = `/v1/companies/${CO}`;
const ledger = async (name, group) =>
  (
    await j("POST", `${base}/ledgers`, {
      token: TOK,
      body: { name, group_name: group, created_by: "drive-b2" },
    })
  ).body.ledger_id;
const DEBTOR = await ledger("Acme Trading", "Sundry Debtors");
const SALES = await ledger("Email Drive Sales", "Sales Accounts");
const posted = await j("POST", `${base}/vouchers/sales`, {
  token: TOK,
  body: {
    voucher_no: "EMAIL-1",
    voucher_date: "2026-04-10",
    party: "Acme Trading",
    created_by: "drive-b2",
    postings: [
      { ledger_id: DEBTOR, dr_cr: "D", amount: "2500.0000" },
      { ledger_id: SALES, dr_cr: "C", amount: "2500.0000" },
    ],
  },
});
if (posted.status !== 201) {
  throw new Error(`drive-b2: seed voucher failed: ${posted.status} ${JSON.stringify(posted.body)}`);
}

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, altMac, toGateway } = app;

/**
 * ONE LOG, BOTH EFFECTS — so a probe can say that each really happened, and that
 * the refusal path produces NEITHER.
 *
 * ⚠️ THE ORDER IN THIS ARRAY IS NOT THE CALL ORDER. See the file header: the
 * `mailto:` request is emitted synchronously from the renderer while a `download`
 * has to cross to the browser process, so ["draft", "file:…"] is what a CORRECT
 * build produces here. Do not re-add an ordering assertion on it.
 */
const events = [];
let mailtoURL = null;
page.on("download", (d) => events.push(`file:${d.suggestedFilename()}`));
page.on("request", (r) => {
  if (r.url().startsWith("mailto:")) {
    events.push("draft");
    mailtoURL = r.url();
  }
});

const testid = (t) => page.getByTestId(t);
const text = async (t) =>
  (await testid(t).count()) > 0
    ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim()
    : "";
const status = () => text("status-line");

/**
 * Alt+G Go To, type a report name, Enter.
 *
 * ⚠️ NOT `openFromGateway`. That helper drives the GATEWAY menu, which carries
 * SCREENS; "Trial Balance" is a REPORT NAME and lives behind Alt+G. The B1 drive
 * used the wrong one of these two and failed three probes for it.
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
  // 1. THE KEY FIRES AT ALL — the composed Option+M.
  // =========================================================================
  await goTo("Trial Balance");
  rec(
    "control: the Trial Balance is up before anything is e-mailed",
    (await body()).includes("Trial Balance"),
    (await body()).slice(0, 90),
  );
  rec(
    "control: nothing has been saved or drafted yet",
    events.length === 0,
    JSON.stringify(events),
  );

  const download = page.waitForEvent("download", { timeout: 30_000 });
  // ⚠️ THE COMPOSED CHARACTER, NOT press("Alt+m"). macOS turns Option+M into "µ",
  // and combos resolve on `code` precisely so that cannot kill a binding — but a
  // probe that sends the UNcomposed key tests a keystroke no Mac produces.
  await altMac("m");
  const dl = await download;
  await page.waitForTimeout(1200);

  // =========================================================================
  // 2. A REAL PDF LANDED ON DISK.
  // =========================================================================
  const path = await dl.path();
  const bytes = readFileSync(path);
  const head = bytes.subarray(0, 5).toString("latin1");
  const tail = bytes.subarray(-1024).toString("latin1");
  rec(
    "Alt+M saves a file, named for the report",
    /trial-balance/i.test(dl.suggestedFilename()),
    `${dl.suggestedFilename()} (${bytes.length} bytes)`,
  );
  rec(
    "⛔ ...and it is a real PDF, not a CSV with a .pdf name",
    head === "%PDF-" && tail.includes("%%EOF"),
    `head=${JSON.stringify(head)} eof=${tail.includes("%%EOF")}`,
  );
  rec(
    "...whose contents are the SERVER'S statement, carrying the company name",
    bytes.toString("latin1").includes(COMPANY_NAME),
    `${COMPANY_NAME} ${bytes.toString("latin1").includes(COMPANY_NAME) ? "present" : "MISSING"}`,
  );

  // =========================================================================
  // 3. ⛔⛔ THE DRAFT — AND THE ORDER.
  // =========================================================================
  rec(
    "⛔⛔ Chromium was asked to open a mailto: draft",
    mailtoURL !== null,
    mailtoURL ? `${mailtoURL.slice(0, 110)}…` : "no mailto: request was ever made",
  );
  // ⚠️⚠️ THE ORDER IS **NOT** ASSERTED FROM THIS EVENT LOG, AND THE FIRST VERSION
  // OF THIS PROBE DID — WRONGLY. It observed ["draft", "file:…"] and failed on a
  // build whose call order is provably correct. Chromium emits the `mailto:`
  // request synchronously from the renderer's navigation attempt, while a
  // `download` event has to cross to the browser process and start writing a
  // file — so what the log records is EVENT-DELIVERY LATENCY, not the order the
  // code called its two sinks in. Asserting on it was a refusal for the wrong
  // reason (rule 5), and "fixing" the product to satisfy it would have been
  // reordering correct code to please a broken oracle.
  //
  // ⛔ THE STRICT ORDERING ORACLE IS `emailDocument.test.tsx`, where both sinks
  // are injected and push to ONE synchronous array — deterministic, and red under
  // an executed mutation that opens the draft first. What THIS probe adds is that
  // both effects really happen in a real browser and that the bytes reach a real
  // file, which jsdom cannot show.
  rec(
    "⛔ both effects happened: a real file was saved AND a draft was requested",
    events.some((e) => e.startsWith("file:")) && events.includes("draft"),
    JSON.stringify(events),
  );

  const url = mailtoURL ?? "";
  const decoded = decodeURIComponent(url);
  rec(
    "the draft has an ENCODED subject and body",
    /[?&]subject=[^&]+/.test(url) && /[?&]body=[^&]+/.test(url),
    url.slice(0, 140),
  );
  rec(
    "⛔ ...and NO recipient, because no party e-mail is stored anywhere",
    url.startsWith("mailto:?") && !/[?&]to=/.test(url),
    url.slice(0, 60),
  );
  rec(
    "the subject names this company and this document",
    decoded.includes(COMPANY_NAME) && /Trial Balance/i.test(decoded),
    decoded.split("&body=")[0]?.slice(0, 120) ?? "",
  );
  rec(
    "⛔ the BODY never claims the document is attached — it is not, yet",
    !/attached|enclosed|please find|herewith/i.test(decoded),
    (decoded.split("&body=")[1] ?? "").slice(0, 160),
  );
  rec(
    "⚠️ ...and a line break travelled as %0D%0A, which is what a mail client reads",
    /%0D%0A/i.test(url),
    /%0D%0A/i.test(url) ? "present" : "the body has no encoded line break",
  );
  rec(
    "control: the page did NOT navigate away to the mailto:",
    page.url().includes("5173"),
    page.url(),
  );

  // =========================================================================
  // 4. THE OPERATOR IS TOLD BOTH LIMITS, ON THE PAGE.
  // =========================================================================
  const line = await status();
  rec(
    "the status line NAMES the saved file",
    line.includes(dl.suggestedFilename()),
    line.slice(0, 160),
  );
  rec(
    "⛔ ...and says the operator must ATTACH it before sending",
    /ATTACH/.test(line),
    line.slice(0, 200),
  );
  rec(
    "⛔ ...and says the draft has no recipient, and why",
    /no recipient/i.test(line) && /does not store a party/i.test(line),
    line.slice(-160),
  );

  // =========================================================================
  // 5. A REFUSAL SAVES NOTHING AND DRAFTS NOTHING.
  // =========================================================================
  events.length = 0;
  mailtoURL = null;
  await toGateway();
  await press("F8", 1200); // a Sales voucher, unsaved
  rec(
    "control: an unsaved Sales voucher is on screen",
    (await body()).toLowerCase().includes("sales"),
    (await body()).slice(0, 80),
  );
  await altMac("m");
  await page.waitForTimeout(1500);
  rec(
    "⛔ Alt+M on an UNSAVED voucher saves nothing and opens no draft",
    events.length === 0 && mailtoURL === null,
    JSON.stringify(events),
  );
  rec(
    "...and says why, in the words Ctrl+P uses",
    /not been saved/i.test(await status()),
    (await status()).slice(0, 160),
  );

  rec(
    "no console errors while driving the key",
    app.consoleErrors.length === 0,
    app.consoleErrors.slice(0, 3).join(" | ") || "none",
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
