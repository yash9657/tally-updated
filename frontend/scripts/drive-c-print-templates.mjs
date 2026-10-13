/**
 * TRACK C — PER-COMPANY PRINT TEMPLATES, driven in a real browser.
 *
 * ⛔ WHAT THIS PROVES THAT THE VITEST SUITE CANNOT. Every frontend test runs
 * against the in-memory mock, which has no renderer: it stores an override and
 * reports it back, and that is exactly the read-back oracle HANDOFF rule 4
 * forbids relying on — it cannot see a printer that ignores the override, which
 * is the only failure a customer would notice. Here the override is typed INTO
 * THE SCREEN and then the SERVER'S OWN PDF is fetched and read.
 *
 * THE ORACLES, each chosen so a wrong answer FAILS rather than looks plausible:
 *
 *   1. THE GATEWAY DOOR LANDS ON THE SCREEN. The label is not a report name, and
 *      `canonicalReportName` falls back to the raw label — so a missing navMachine
 *      match pushes a `report` frame reading "not available in this build" over a
 *      screen that works. That is indistinguishable, from the operator's chair,
 *      from the feature not existing.
 *
 *   2. ⛔⛔ THE PRINTED PDF CHANGES. The distinctive probe words are absent from
 *      the built-in document and present after one Save. This is the assertion the
 *      whole track turns on: the write existed and was TESTED for months while
 *      nothing could reach it, so "the row was stored" proves nothing anybody
 *      cares about.
 *
 *   3. THE DATABASE HOLDS WHAT THE SCREEN SAID IT SAVED. A screen rendering a
 *      response the server never acted on passes every on-page assertion — the
 *      lesson T32's drive paid for.
 *
 *   4. ⛔ A REPORT DOES NOT OFFER THE FOUR FIELDS IT CANNOT PRINT, and the screen
 *      NAMES them. An absent control is indistinguishable from one that failed to
 *      render, so the sentence is asserted too.
 *
 *   5. ⛔⛔ AFTER CLEARING, THE PDF IS BYTE-IDENTICAL TO THE ONE BEFORE ANY
 *      OVERRIDE. Not "the probe words are gone" — byte-identical. That is the
 *      strongest available statement that the company is genuinely back on the
 *      built-in layout, and it is what makes this a two-way door rather than a
 *      trap. A `DELETE` that blanked the row instead of removing it would pass a
 *      "the words are gone" check and fail this one.
 *
 * ⛔ IT RUNS ON A THROWAWAY COMPANY OF ITS OWN, NOT ON Demo Traders, AND TEARS IT
 * DOWN. Residue drops `verify:demo` below 34/34.
 *
 * RUN:  node frontend/scripts/drive-c-print-templates.mjs
 *       (needs cored :8087 and vite :5173 — the port matters, WebAuthn origin
 *        matching is exact. It creates its own company.)
 */
import { createHash } from "node:crypto";
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://127.0.0.1:8087";
const stamp = Math.random().toString(36).slice(2, 7);
const COMPANY_NAME = `Print Template Drive ${stamp}`;

/**
 * ⛔ THE PROBE WORDS ARE SINGLE TOKENS WITH NO SPACES, AND THAT IS NOT COSMETIC.
 * The renderer WRAPS a long declaration into several show-text operands, so a
 * whole sentence does not appear contiguously in the PDF byte stream and a search
 * for one would fail against a document that is perfectly correct — a refusal for
 * the wrong reason (rule 5). A single unbroken token survives wrapping.
 */
const PROBE_TITLE = "ZZDRIVERECEIPTTITLE";
const PROBE_DECL_WORD = "ZZDRIVEDECLARATION";
const PROBE_DECL = `Subject to ${PROBE_DECL_WORD} at our bankers only.`;
const PROBE_LETTERHEAD_WORD = "ZZDRIVELETTERHEAD";

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

/** Fetch a printed document as BYTES, so the oracle is the paper and not a JSON echo. */
const pdf = async (path, token) => {
  const r = await fetch(`${CORED}${path}`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  const buf = Buffer.from(await r.arrayBuffer());
  return {
    status: r.status,
    bytes: buf,
    // latin1 so byte offsets survive; the renderer writes literal strings into
    // the content stream, which is how drive-b1 finds the company name in a PDF.
    text: buf.toString("latin1"),
    sha: createHash("sha256").update(buf).digest("hex"),
  };
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

// ---- The tenant and one posted RECEIPT, over HTTP. The voucher is not what this
// probe is about; keying it through the grid would make a template failure
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
    `drive-c: could not create a throwaway company: ${created.status} ${JSON.stringify(created.body)}`,
  );
}
const CO = created.body.company_id ?? created.body.id;
console.log(`company ${COMPANY_NAME} = ${CO}`);

const base = `/v1/companies/${CO}`;
const ledger = async (name, group, extra = {}) =>
  (
    await j("POST", `${base}/ledgers`, {
      token: TOK,
      body: { name, group_name: group, created_by: "drive-c", ...extra },
    })
  ).body.ledger_id;

const DEBTOR = await ledger("Acme Trading", "Sundry Debtors", {
  gstin: "29AAACA1111A1Z5",
  state_code: "29",
  registration_type: "REGULAR",
});
const CASH = await ledger("Drive Cash", "Cash-in-Hand");

const receipt = await j("POST", `${base}/vouchers/receipt`, {
  token: TOK,
  body: {
    voucher_no: "RCPT/DRIVE/1",
    voucher_date: "2026-04-12",
    party: "Acme Trading",
    party_ledger_id: DEBTOR,
    narration: "Against invoice",
    created_by: "drive-c",
    postings: [
      { ledger_id: CASH, dr_cr: "D", amount: "2500.0000" },
      { ledger_id: DEBTOR, dr_cr: "C", amount: "2500.0000" },
    ],
  },
});
if (receipt.status !== 201) {
  throw new Error(
    `drive-c: seed receipt failed: ${receipt.status} ${JSON.stringify(receipt.body)}`,
  );
}
const RECEIPT_ID = receipt.body.voucher_id;
const DOC_PATH = `${base}/print/vouchers/receipt/${RECEIPT_ID}`;

const app = await openApp({ company: CO, expectCompany: COMPANY_NAME });
const { page, body, press, toGateway, openFromGateway } = app;

/**
 * EVERY FAILED RESPONSE, WITH ITS URL.
 *
 * ⚠️ THE HARNESS'S consoleErrors CARRIES ONLY THE MESSAGE ("Failed to load
 * resource: … 500"), which cannot say WHOSE request failed — and the first
 * version of this probe asserted `consoleErrors.length === 0` and failed on
 * requests that have nothing to do with this screen. Asserting on an
 * unattributable error is a probe that reports a defect it cannot locate; worse,
 * one that would keep failing forever and get switched off.
 */
const failures = [];
page.on("response", (r) => {
  if (r.status() >= 400) failures.push(`${r.status()} ${r.request().method()} ${r.url()}`);
});

const testid = (t) => page.getByTestId(t);
const seen = async (t) => (await testid(t).count()) > 0;
const text = async (t) =>
  (await seen(t))
    ? (await testid(t).first().innerText()).replace(/\s+/g, " ").trim()
    : "";

/**
 * Open the screen from the GATEWAY menu.
 *
 * ⚠️ VIA THE HARNESS'S openFromGateway, NOT BY CLICKING THE LABEL. The first
 * version of this probe clicked the text and pressed Enter, and the screen never
 * opened — clicking leaves the SelectionList's selection on its default, so Enter
 * activates whatever was already highlighted. T32's drive carries the same
 * warning, and the harness helper drives the Gateway's own search box, which is
 * how a keyboard operator actually gets there.
 *
 * ⚠️ AND NOT the `Alt+G` form either. That picker is hard-wired to ["Vouchers",
 * ...REPORT_NAMES] and "Print Templates" is a ScreenKind, not a report name — the
 * B1 drive used the wrong one of these two helpers and failed three probes.
 */
const openTemplates = async () => {
  await openFromGateway("Print Templates", 1500);
  await testid("print-template-state").first().waitFor({ timeout: 10_000 });
};

/** Pick a document kind and wait for its form to land. */
const pickKind = async (kind) => {
  await testid("print-template-kind").first().selectOption(kind);
  await page.waitForTimeout(700);
};

const fill = async (field, value) => {
  await testid(`print-template-input-${field}`).first().fill(value);
};

const save = async () => {
  await testid("print-template-save").first().click();
  await testid("print-template-saved").first().waitFor({ timeout: 10_000 });
};

try {
  // =========================================================================
  // 1. THE DOOR LANDS ON THE SCREEN, NOT ON A REPORT FRAME.
  // =========================================================================
  await toGateway();
  rec(
    "control: the Gateway offers a 'Print Templates' door",
    (await body()).includes("Print Templates"),
    (await body()).slice(0, 120),
  );

  await openTemplates();
  rec(
    "⛔ the door lands on the print-templates SCREEN",
    await seen("print-template-screen"),
    (await body()).slice(0, 160),
  );
  rec(
    "...and NOT on a report frame saying the report is unavailable",
    !/not available in this build/i.test(await body()),
    (await body()).slice(0, 160),
  );
  rec(
    "the screen names the company whose documents are being edited",
    (await text("print-template-target")).includes(COMPANY_NAME),
    await text("print-template-target"),
  );
  rec(
    "⚠️ ...and discloses that a change also affects REPRINTS of old documents",
    /printed, not when it was entered/i.test(await text("print-template-reprint-note")),
    await text("print-template-reprint-note"),
  );

  // =========================================================================
  // 2. THE BUILT-IN DOCUMENT — the baseline every assertion below rests on.
  // =========================================================================
  const before = await pdf(DOC_PATH, TOK);
  rec(
    "control: the seeded receipt prints as a real PDF before any override",
    before.status === 200 && before.bytes.subarray(0, 5).toString("latin1") === "%PDF-",
    `status=${before.status} head=${before.bytes.subarray(0, 8).toString("latin1")} bytes=${before.bytes.length}`,
  );
  rec(
    "control: the built-in receipt carries the BUILT-IN title, so there is something to replace",
    before.text.includes("RECEIPT VOUCHER"),
    "RECEIPT VOUCHER present in the baseline PDF",
  );
  rec(
    "control: and the probe words are ABSENT from it — otherwise every check below is vacuous",
    !before.text.includes(PROBE_TITLE) && !before.text.includes(PROBE_DECL_WORD),
    `title probe ${before.text.includes(PROBE_TITLE)} decl probe ${before.text.includes(PROBE_DECL_WORD)}`,
  );

  rec(
    "the screen says this company has customised nothing",
    /has not customised/i.test(await text("print-template-state")),
    await text("print-template-state"),
  );
  rec(
    "⛔ ...so it offers NO clear button — one would report success for a state already held",
    !(await seen("print-template-clear")),
    (await seen("print-template-clear")) ? "offered and it should not be" : "absent",
  );

  // =========================================================================
  // 3. SET AN OVERRIDE THROUGH THE SCREEN, AND THE PAPER MUST CHANGE.
  // =========================================================================
  await pickKind("receipt");
  rec(
    "control: the receipt form shows the BUILT-IN declaration as its placeholder",
    /realisation of the instrument/i.test(
      (await testid("print-template-input-declaration").first().getAttribute("placeholder")) ?? "",
    ),
    (await testid("print-template-input-declaration").first().getAttribute("placeholder")) ?? "",
  );

  await fill("title", PROBE_TITLE);
  await fill("declaration", PROBE_DECL);
  await save();
  rec(
    "the screen reports the save and flips to 'customised'",
    /has customised/i.test(await text("print-template-state")),
    `${await text("print-template-saved")} | ${await text("print-template-state")}`,
  );

  // ⛔ THE DATABASE, not the screen's own word for it. A screen rendering a
  // response the server never acted on passes every on-page assertion.
  const row = await sql(
    `SELECT title || '|' || declaration FROM print_template
      WHERE company_id = ${q(CO)} AND doc_kind = 'receipt'`,
  );
  rec(
    "⛔ the DATABASE holds the row the screen said it saved",
    row.includes(PROBE_TITLE) && row.includes(PROBE_DECL_WORD),
    row.slice(0, 140),
  );

  const after = await pdf(DOC_PATH, TOK);
  rec(
    "⛔⛔ THE PRINTED RECEIPT NOW CARRIES THE OVERRIDDEN TITLE",
    after.text.includes(PROBE_TITLE),
    `present=${after.text.includes(PROBE_TITLE)} bytes=${after.bytes.length} (was ${before.bytes.length})`,
  );
  rec(
    "⛔⛔ ...AND THE OVERRIDDEN DECLARATION",
    after.text.includes(PROBE_DECL_WORD),
    `present=${after.text.includes(PROBE_DECL_WORD)}`,
  );
  rec(
    "⛔ ...and the BUILT-IN title is GONE — an override must replace, not print beside",
    !after.text.includes("RECEIPT VOUCHER"),
    after.text.includes("RECEIPT VOUCHER")
      ? "the built-in title is still on the page next to the override"
      : "replaced",
  );

  // =========================================================================
  // 4. A REPORT DOES NOT OFFER WHAT IT CANNOT PRINT — AND SAYS SO.
  // =========================================================================
  await pickKind("report");
  rec(
    "⛔ a REPORT does not offer a declaration control",
    !(await seen("print-template-field-declaration")),
    (await seen("print-template-field-declaration"))
      ? "offered; a stored declaration would never print on a statement"
      : "absent",
  );
  for (const f of ["terms", "signature_label", "bank_details"]) {
    rec(
      `⛔ ...nor a ${f} control`,
      !(await seen(`print-template-field-${f}`)),
      (await seen(`print-template-field-${f}`)) ? "offered" : "absent",
    );
  }
  rec(
    "control: it DOES still offer the letterhead, so the form is not simply empty",
    await seen("print-template-field-header_lines"),
    (await seen("print-template-field-header_lines")) ? "offered" : "absent",
  );
  rec(
    "⛔ ...and it NAMES what a report does not print, rather than the box being silently gone",
    /does not print/i.test(await text("print-template-ignored")) &&
      /declaration/i.test(await text("print-template-ignored")),
    (await text("print-template-ignored")).slice(0, 200),
  );
  rec(
    "...and says a report's default title is the report's OWN NAME, not a blank",
    /report's own name/i.test(
      (await testid("print-template-input-title").first().getAttribute("placeholder")) ?? "",
    ),
    (await testid("print-template-input-title").first().getAttribute("placeholder")) ?? "",
  );

  // The honoured half really works on a statement.
  await fill(
    "header_lines",
    `${PROBE_LETTERHEAD_WORD}\n14 Residency Road, Bengaluru 560025`,
  );
  await save();
  const tb = await pdf(`${base}/print/reports/trial-balance?as_on=2026-04-30`, TOK);
  rec(
    "⛔ the printed TRIAL BALANCE carries the overridden letterhead",
    tb.status === 200 && tb.text.includes(PROBE_LETTERHEAD_WORD),
    `status=${tb.status} present=${tb.text.includes(PROBE_LETTERHEAD_WORD)}`,
  );

  // =========================================================================
  // 5. CLEARING PUTS THE BUILT-IN LAYOUT BACK — BYTE FOR BYTE.
  // =========================================================================
  await pickKind("receipt");
  rec(
    "control: the clear button IS offered now that there is a customisation",
    await seen("print-template-clear"),
    (await seen("print-template-clear")) ? "offered" : "absent — nothing to press",
  );

  await testid("print-template-clear").first().click();
  await page.waitForTimeout(900);
  rec(
    "the screen flips back to 'has not customised'",
    /has not customised/i.test(await text("print-template-state")),
    `${await text("print-template-saved")} | ${await text("print-template-state")}`,
  );
  rec(
    "...and withdraws the clear button, which now has nothing to do",
    !(await seen("print-template-clear")),
    (await seen("print-template-clear")) ? "still offered" : "withdrawn",
  );

  const gone = await sql(
    `SELECT count(*) FROM print_template
      WHERE company_id = ${q(CO)} AND doc_kind = 'receipt'`,
  );
  rec(
    "⛔ the ROW IS REMOVED, not blanked — a blank row still counts as a customisation",
    gone === "0",
    `print_template rows for receipt = ${gone}`,
  );

  const restored = await pdf(DOC_PATH, TOK);
  rec(
    "the printed receipt carries the built-in title again",
    restored.text.includes("RECEIPT VOUCHER") && !restored.text.includes(PROBE_TITLE),
    `built-in=${restored.text.includes("RECEIPT VOUCHER")} probe=${restored.text.includes(PROBE_TITLE)}`,
  );
  rec(
    "⛔⛔ AND THE PDF IS BYTE-IDENTICAL TO THE ONE BEFORE ANY OVERRIDE EXISTED",
    restored.sha === before.sha,
    restored.sha === before.sha
      ? `sha ${before.sha.slice(0, 16)} — genuinely back on the built-in layout`
      : `sha ${before.sha.slice(0, 16)} -> ${restored.sha.slice(0, 16)} (${before.bytes.length} -> ${restored.bytes.length} bytes)`,
  );

  // The report override was NOT cleared, so it must survive — a DELETE that took
  // the whole company's templates with it would pass every check above.
  const reportRow = await sql(
    `SELECT count(*) FROM print_template
      WHERE company_id = ${q(CO)} AND doc_kind = 'report'`,
  );
  rec(
    "⛔ clearing the receipt did NOT clear the report's own template",
    reportRow === "1",
    `print_template rows for report = ${reportRow}`,
  );

  // =========================================================================
  // 6. NOTHING THREW ON THE WAY.
  // =========================================================================
  // ⛔ SCOPED TO THIS SCREEN'S OWN REQUESTS. Anything else failing is a fact about
  // the dev environment (other services, assets) and is PRINTED below rather than
  // asserted on — an assertion nobody can locate is an assertion that gets
  // switched off.
  const mine = failures.filter((f) => f.includes("/print-templates/"));
  /**
   * ⚠️ ONE 403 IS EXPECTED AND IS THE FEATURE WORKING. The write maps to
   * (alter, company) and the Owner level requires WebAuthn on writes, so the
   * FIRST PUT is refused with STEPUP_REQUIRED, the app runs the ceremony, and the
   * retry succeeds — visible in the non-GET log below as 403 → register/assert →
   * 200. Treating that as a fault would be asserting the second factor does not
   * happen.
   */
  const unexpected = mine.filter((f) => !f.startsWith("403 "));
  rec(
    "⛔ no print-template request failed, beyond the expected step-up 403",
    unexpected.length === 0,
    unexpected.join(" | ") || `none (${mine.length} step-up 403s, which is the second factor working)`,
  );
  rec(
    "control: the step-up 403 DID happen, so the write really is behind a second factor",
    mine.some((f) => f.startsWith("403 ")),
    mine.join(" | ") || "no 403 seen — the write was not step-up gated",
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
    const tmpl = await sql(
      `SELECT count(*) FROM print_template WHERE company_id = ${q(CO)}`,
    );
    console.log(
      `\ntorn down ${COMPANY_NAME}: ${left === "0" && tmpl === "0" ? "clean" : `LEAKED (company=${left} print_template=${tmpl})`}`,
    );
  } catch (e) {
    console.log(`\n⚠️ teardown FAILED for ${CO}: ${e}`);
  }

  console.log("\n--- non-GET browser requests (status last) ---");
  for (const r of app.net) console.log("  " + r);
  console.log("\n--- every failed response, with its URL (diagnostics) ---");
  for (const f of failures) console.log("  " + f);
  console.log("\n--- console errors ---");
  for (const e of app.consoleErrors.slice(0, 8)) console.log("  " + e);
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);
