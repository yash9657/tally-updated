#!/usr/bin/env node
/**
 * browse-t8-foreign-entry.mjs — T8 STEP 8 IN A REAL BROWSER.
 *
 * ⛔⛔ THIS IS THE ONLY PROBE THAT CAN PROVE STEP 8 AT ALL, AND THE REASON IS WHAT
 * STEP 8 IS. Steps 6 and 7 shipped READS: a wire drive proves cored emits the
 * figures and a browser probe proves they reach the page. Step 8 ships a FORM — so
 * the question is the opposite one, and no wire drive can ask it: **does what an
 * operator types on the screen become what the database stores?**
 *
 * `vitest` proves the grid renders three boxes and that clearing the currency clears
 * all three. It cannot prove that the accept path SENDS them: the request builder
 * lives in a 4,300-line screen behind a WebAuthn step-up, and a form that displays a
 * currency and posts a rupee voucher is green in every unit test ever written for it.
 *
 * ⭐⭐ AND IT PROVES DEFECT NUMBER NINE IS CLOSED END TO END. §6.1 of the handoff
 * tells step 8 to ask *"what does an alter through this form omit?"* — and the
 * answer, before step 8, was the whole currency triple: `DoubleEntryLine` did not
 * carry it, so opening a foreign Journal, fixing the narration and saving wrote a
 * RUPEE voucher over it, with nothing refusing it. Probe 5 below is that round trip,
 * with `psql` as the oracle on both sides of it.
 *
 * ⛔ THE ORACLE IS `psql`, NEVER THE SCREEN. The screen is what is under test.
 *
 * ⛔ IT DIRTIES A SCRATCH BOOK, NEVER THE DEMO ONE (§2.5):
 *
 *   node frontend/scripts/browse-t8-foreign-entry.mjs \
 *        "$(cat frontend/.t8-step6-scratch-company-id)"
 *
 * ⚠️ AND IT NEEDS A **FRESH** vite (§2.2). A stale one whose log shows
 * "Could not Fast Refresh" renders the PREVIOUS build of the grid — which for a
 * probe about a brand-new field means it reports step 8 as unshipped.
 */

import { execFileSync } from "node:child_process";

import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.argv[2];
if (!CO) {
  console.error(
    'usage: node browse-t8-foreign-entry.mjs "$(cat frontend/.t8-step6-scratch-company-id)"\n' +
      "⛔ an EMPTY ?company= makes the app bootstrap a fresh empty tenant and every " +
      "screen on it looks clean.",
  );
  process.exit(2);
}

const CORED = "http://localhost:8087";
const SUB = "browser-dev-owner";
const base = `/v1/companies/${CO}`;

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
  return { status: r.status, body: parsed, text };
};
const psql = (sql) =>
  execFileSync(
    "psql",
    ["-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql],
    { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" },
  ).trim();

// ---------------------------------------------------------------------------
// THE FIGURES. ⭐ THE RATE HAS TEN SIGNIFICANT DECIMALS ON PURPOSE — it is the one
// value on this form that a wrong helper silently changes, and the whole probe turns
// on whether it survives from the keyboard to the table.
// ---------------------------------------------------------------------------
const stamp = Math.random().toString(36).slice(2, 6);
const REF = `JFX-${stamp}`;
const B = "83651.23";
const F = "1000.00";
const R = "83.6512345678";
const PARTY = `Globex Journal ${stamp}`;

const token = (
  await j("POST", "/auth/token", { body: { subject: SUB, amr: ["webauthn"] } })
).body.token;

// ⛔ IDEMPOTENT, for `browse-t8-outstandings-forex.mjs`'s measured reason: a probe
// meant to be re-run against the same scratch book must not die on
// `already has a ledger named …` while the thing it measures goes unmeasured.
const mk = async (name, group) => {
  const r = await j("POST", `${base}/ledgers`, {
    token,
    body: { name, group_name: group, created_by: SUB },
  });
  if (r.status === 201 || r.status === 200) return r.body.ledger_id;
  const list = await j("GET", `${base}/ledgers`, { token });
  const found = (list.body.ledgers ?? []).find((l) => l.name === name);
  if (!found) throw new Error(`ledger ${name}: ${r.status} ${r.text}`);
  return found.id;
};
const debtor = await mk(PARTY, "Sundry Debtors");
await mk("Export Sales", "Sales Accounts");
const forexPL = await mk("Forex Gain/Loss", "Indirect Expenses");
const usd = await (async () => {
  const r = await j("POST", `${base}/currencies`, {
    token,
    body: {
      code: "USD",
      symbol: "$",
      formal_name: "US Dollars",
      created_by: SUB,
    },
  });
  if (r.status === 201) return r.body.currency_id;
  const list = await j("GET", `${base}/currencies`, { token });
  const found = (list.body.currencies ?? []).find((c) => c.code === "USD");
  if (!found) throw new Error(`currency: ${r.status} ${r.text}`);
  return found.id;
})();

// ⛔ THE SETTING IS CLEARED FIRST, so probe 2 measures the PANEL choosing it rather
// than a choice left behind by an earlier run. A probe that passes because of its
// own previous run is the shape §2.5 warns about.
await j("PUT", `${base}/forex-settings`, {
  token,
  body: { gain_loss_ledger_id: "" },
});

const app = await openApp({ company: CO, expectCompany: "T8 Step6 Scratch" });
const { page, press, toGateway, body, altMac } = app;

// Every voucher write the BROWSER actually made — the wire oracle beside psql.
const writes = [];
// ⚠️ AND EVERY NON-2xx WITH ITS URL. `consoleErrors` carries only the MESSAGE
// ("Failed to load resource: … 403"), which cannot say WHOSE request failed — the
// note at `drive-c-print-templates.mjs:188` is the measured version of this. This
// probe WRITES, so it necessarily provokes the Owner level's step-up 403; the
// read-only T8 probes never do, which is why THEY can assert consoleErrors === 0.
const failures = [];
const responses = [];
page.on("response", (r) => {
  const m = r.request().method();
  const u = r.url();
  responses.push({ method: m, path: new URL(u).pathname, ok: r.status() < 400 });
  // ⚠️ THE ORIGIN IS vite's, NOT `CORED`'s — the app reaches cored through the
  // dev proxy, so these URLs are http://localhost:5173/v1/…. Stripping `CORED`
  // stripped nothing and the first version of the assertion below rejected its
  // own expected 403 for having the "wrong" host.
  if (r.status() >= 400) failures.push(`${r.status()} ${m} ${new URL(u).pathname}`);
  if (m === "POST" && /\/vouchers\/journal$/.test(u))
    writes.push(`POST journal ${r.status()}`);
  if (m === "PUT" && /\/vouchers\/journal\//.test(u))
    writes.push(`PUT journal ${r.status()}`);
  if (m === "PUT" && /\/forex-settings$/.test(u))
    writes.push(`PUT forex-settings ${r.status()}`);
});

const postingRow = (voucherNo) =>
  psql(
    `SELECT COALESCE(p.currency_id::text,'NULL')||'|'||
            COALESCE(p.foreign_amount::text,'NULL')||'|'||
            COALESCE(p.rate::text,'NULL')||'|'||p.amount::text
       FROM posting p
       JOIN voucher_version vv ON vv.id = p.voucher_version_id
       JOIN voucher_head vh ON vh.current_version_id = vv.id
      WHERE p.company_id = '${CO}' AND p.ledger_id = '${debtor}'
        AND vv.voucher_no = '${voucherNo}'`,
  );

try {
  // =========================================================================
  // 1. ⛔ THE ROSTER ROW, FIRST — "a feature nobody can find is not built"
  // =========================================================================
  await toGateway();
  await press("F11", 1500);
  const f11 = await body();
  rec(
    "T8/8 browser: ⛔ F11's Multi-currency row reads 'Part built', not 'On' and not 'Not built'",
    /Part built/.test(f11),
    `⚠️ AND NOT "On": an operator who reads On believes the UNREALISED difference ` +
      `is posted, and it is not. Q4 asks for this state by name. Screen says: ` +
      JSON.stringify(f11.replace(/\s+/g, " ").slice(0, 120)),
  );
  rec(
    "T8/8 browser: …and the legend explains the new state",
    /PART BUILT \(usable from the keyboard/.test(f11),
    "a fifth badge nobody defined is worse than four",
  );

  // =========================================================================
  // 2. ⭐⭐ THE SETTING STEP 5's REFUSAL NAMES — F11 > Multi-currency
  // =========================================================================
  // `errForexLedgerNotChosen` says "(Gateway > F11 > Multi-currency)". Until step 8
  // that path led to a row of prose, and a refusal an operator cannot act on is an
  // outage.
  rec(
    "T8/8 browser: ⛔ the panel says NONE CHOSEN and what that costs",
    /NONE CHOSEN — a realised difference will be refused/.test(f11),
    "a blank field would leave an operator hunting for the reason their " +
      "settlement was refused",
  );
  {
    const pick = page.getByTestId("forex-ledger-pick");
    await pick.selectOption(forexPL);
    await page.getByTestId("forex-ledger-save").click();
    await page.waitForTimeout(2500);
    const after = await body();
    rec(
      "T8/8 browser: ⭐ choosing a ledger on the SCREEN reports back the SERVER's row",
      /Realised exchange gains and losses will post to Forex Gain\/Loss/.test(after),
      `screen says ${JSON.stringify(after.replace(/\s+/g, " ").match(/Current setting:[^.]{0,80}/)?.[0] ?? "")}`,
    );
    rec(
      "T8/8 browser: ⛔⛔ AND psql CONFIRMS IT — the row is really there",
      psql(
        `SELECT COALESCE(forex_gain_loss_ledger_id::text,'NULL')
           FROM company_forex_settings WHERE company_id = '${CO}'`,
      ) === forexPL,
      "a panel that reported success from its own request could not detect a " +
        "setter that answered 204 and wrote nothing — a resolved promise looks " +
        "identical either way (register row D43)",
    );
  }

  // =========================================================================
  // 3. ⛔ THE GRID GAINS A CURRENCY BOX — F7, which is ALWAYS double entry
  // =========================================================================
  await toGateway();
  await press("F7", 1200);
  rec(
    "T8/8 browser: F7 renders the Dr/Cr grid",
    (await page.getByTestId("double-entry-panel").count()) > 0,
    "the grid itself must be there before its new fields can be",
  );
  rec(
    "T8/8 browser: ⭐⭐ each line now has a CURRENCY box, because this company has one",
    (await page.getByTestId("de-currency:0").count()) > 0,
    "⛔ absent here means the currency list never reached the panel — the fields " +
      "are hidden when a company has NO currency, so a failing fetch and a " +
      "base-only company look identical on the page",
  );
  rec(
    "T8/8 browser: ⛔ and the foreign amount and rate are HIDDEN until one is picked",
    (await page.getByTestId("de-foreign:0").count()) === 0 &&
      (await page.getByTestId("de-rate:0").count()) === 0,
    "a foreign-amount box with no currency chosen invites exactly the " +
      "two-of-three row migration 0080's CHECK refuses",
  );

  // =========================================================================
  // 4. ⭐⭐ KEY A FOREIGN JOURNAL FROM THE KEYBOARD AND READ IT OUT OF `posting`
  // =========================================================================
  await page.getByTestId("voucher-date").fill("2026-06-10");
  await page.getByTestId("de-ledger:0").fill(PARTY);
  await page.getByTestId("de-side:0").selectOption("DR");
  await page.getByTestId("de-amount:0").fill(B);
  await page.getByTestId("de-currency:0").selectOption(usd);
  await page.waitForTimeout(300);
  rec(
    "T8/8 browser: picking a currency reveals the foreign amount and the rate",
    (await page.getByTestId("de-foreign:0").count()) > 0 &&
      (await page.getByTestId("de-rate:0").count()) > 0,
    "the two fields appear together, because neither is meaningful alone",
  );
  await page.getByTestId("de-foreign:0").fill(F);
  await page.getByTestId("de-rate:0").fill(R);

  // The credit side, in the company's own currency — ⭐ WHICH IS THE POINT OF Q2:
  // the voucher balances in BASE and need not balance in any foreign currency.
  //
  // ⛔⛔ AND THE GRID OFFERS **ONE** ROW: line 1 does not exist until `de-add-line`
  // is clicked. `e2e-journal.mjs:129` is the worked example. Filling `de-ledger:1`
  // without it does not fail loudly — it hangs for the full 30s locator timeout.
  await page.getByTestId("de-add-line").click();
  await page.waitForTimeout(200);
  await page.getByTestId("de-ledger:1").fill("Export Sales");
  await page.getByTestId("de-side:1").selectOption("CR");
  await page.getByTestId("de-amount:1").fill(B);
  await page.waitForTimeout(300);

  // ⛔ THE HALF-KEYED REFUSAL, MEASURED BEFORE THE COMPLETE ENTRY IS ACCEPTED. The
  // form must say which value is missing BEFORE any request is made — the server
  // and a DB CHECK both refuse it, but by then `ensureLedger` has created every
  // ledger the operator named (defect D39's shape) and they have lost the form.
  //
  // ⛔⛔ IT IS ASSERTED ONLY NOW, WITH **BOTH** LINES KEYED, AND THAT ORDER IS THE
  // PANEL'S DESIGN RATHER THAN THIS PROBE'S CONVENIENCE:
  //     const refusal = doubleEntryRefusal(rows) || doubleEntryForexRefusal(rows);
  // — "an unbalanced grid is the error an operator is most likely to be in the
  // middle of, and a half-keyed currency triple on line 4 is not worth burying it
  // under". Asserted before line 1 was keyed, the grid is 83,651.23 out and the
  // BALANCE refusal correctly wins, so the forex one is never reached. ⛔ THE
  // PRECEDENCE IS NOT THE BUG — reaching it from an unbalanced grid is.
  //
  // ⭐ AND IT READS `de-refusal`, NOT THE WHOLE BODY. Regexing `body()` is why this
  // probe's first failure detail came back as `""` instead of naming what it saw.
  const deRefusal = async () =>
    (await page.getByTestId("de-refusal").count()) === 0
      ? ""
      : await page.getByTestId("de-refusal").innerText();
  rec(
    "T8/8 browser: ⭐ CONTROL — with both lines keyed the grid is balanced and SILENT",
    (await deRefusal()) === "",
    `de-refusal says ${JSON.stringify(await deRefusal())} — ⛔ anything here and ` +
      `the assertion below cannot tell the forex refusal from the balance one, ` +
      `which is exactly how this probe failed the first time`,
  );
  await page.getByTestId("de-rate:0").fill("");
  await page.waitForTimeout(300);
  {
    const said = await deRefusal();
    rec(
      "T8/8 browser: ⛔ a half-keyed triple is refused BY THE FORM, naming the missing value",
      /no EXCHANGE RATE/.test(said) && /Line 1/.test(said),
      `de-refusal says ${JSON.stringify(said)} — it must name the LINE and the ` +
        `missing value, because "invalid entry" on a 12-line grid is not actionable`,
    );
  }
  await page.getByTestId("de-rate:0").fill(R);
  await page.waitForTimeout(300);
  rec(
    "T8/8 browser: …and restoring the rate CLEARS it",
    (await deRefusal()) === "",
    `de-refusal says ${JSON.stringify(await deRefusal())} — a refusal that does ` +
      `not clear would block the accept below, and every psql assertion after it ` +
      `would then be measuring a voucher that was never written`,
  );

  await page.getByTestId("voucher-narration").fill(REF);
  await page.waitForTimeout(400);

  rec(
    "T8/8 browser: ⭐ a grid with ONE foreign line and one rupee line shows NO difference",
    !/difference/i.test(await body()),
    "Q2: the BASE amount is what the double-entry check sums, so a " +
      "multi-currency journal balances in the company's own currency and need " +
      "not balance in any foreign one",
  );

  // ⚠️ Ctrl+A twice: the first is the Owner level's WebAuthn step-up, which the
  // virtual authenticator answers, and the second is the retried write. §2.6.
  await press("Control+a", 3500);
  await press("Control+a", 3000);
  // ⛔ NARRATION IS NOT A COLUMN. `voucher_version` stores the voucher's own body
  // in `payload` jsonb — `payload->>'narration'` is the house idiom (every other
  // drive in this directory reads it that way) and `vv.narration` is an ERROR, not
  // an empty result: psql exits 1 and `execFileSync` throws, which killed this
  // probe here on its second run with 13 assertions already green.
  const vno = psql(
    `SELECT vv.voucher_no FROM voucher_version vv
       JOIN voucher_head vh ON vh.current_version_id = vv.id
      WHERE vv.company_id = '${CO}' AND vv.payload->>'narration' = '${REF}'`,
  );
  rec(
    "T8/8 browser: the journal POSTED from the screen",
    vno !== "" && writes.some((w) => w.startsWith("POST journal 201")),
    `psql found voucher_no ${JSON.stringify(vno)}; browser writes ${JSON.stringify(writes)}`,
  );
  rec(
    "T8/8 browser: ⛔⛔ AND `posting` CARRIES ALL THREE VALUES THE OPERATOR TYPED",
    postingRow(vno) === `${usd}|${F}00|${R}|${B}00`,
    `psql says ${postingRow(vno)}, want ${usd}|${F}00|${R}|${B}00 — ⛔ THIS IS THE ` +
      `WHOLE OF STEP 8. A form that displays a currency and posts a rupee voucher ` +
      `is green in every unit test ever written for it`,
  );
  rec(
    "T8/8 browser: ⛔ and the RATE survived at TEN decimals from the keyboard to the table",
    postingRow(vno).split("|")[2] === R,
    `psql says ${JSON.stringify(postingRow(vno).split("|")[2])} — the fixed-4 money ` +
      `helper this form uses for every other number would have stored 83.6512, ` +
      `which round-trips through its own type perfectly and prices the bill wrong ` +
      `for ever (§6.3)`,
  );

  // =========================================================================
  // 5. ⛔⛔ DEFECT NUMBER NINE, END TO END — AN ALTER MUST NOT DESTROY IT
  // =========================================================================
  // §6.1: "what does an alter through this form omit?" Before step 8 the grid
  // rebuilt every line WITHOUT its currency, so this exact sequence — open the
  // voucher, change the narration, save — wrote a rupee voucher over the dollar
  // one. ⚠️ AND NOTHING REFUSED IT: the entry still balanced in base and the
  // Balance Sheet did not move by a paisa.
  {
    const before = postingRow(vno);
    await toGateway();
    // Reach the voucher through the Day Book, which is the door an operator uses.
    //
    // ⛔ CLICK THE **ROW**, NOT THE TEXT. `getByText(REF).click()` lands on the
    // narration CELL, which does not move the grid's highlight — so the Enter that
    // follows drills whatever row was highlighted already, or nothing. The house
    // idiom is to iterate `[role="grid"] [role="row"]` and click the row whose own
    // innerText carries the marker (`drive-l6-alter.mjs:28`, `drive-l4-tds.mjs:558`),
    // and REF is the narration, which the Day Book prints.
    await altMac("g", 700);
    await page.keyboard.type("Day Book");
    await page.waitForTimeout(400);
    await press("Enter", 2500);
    let found = false;
    {
      const rows = page.locator('[role="grid"] [role="row"]');
      const n = await rows.count();
      for (let i = 0; i < n; i++) {
        const txt = (await rows.nth(i).innerText()).replace(/\n/g, " | ");
        if (!txt.includes(REF)) continue;
        await rows.nth(i).click();
        await page.waitForTimeout(250);
        await press("Enter", 2500);
        found = true;
        break;
      }
    }
    // ⛔ THE HEADING, NOT THE BODY (`drive-l6-alter.mjs:36`): every voucher screen's
    // button bar carries "F7 Journal", so /Journal/ against the body matches on
    // every class. This must be the ALTERATION of a Journal or probe 5 is measuring
    // a fresh blank form and the "preserved" triple below would be a fresh one.
    const heading = await page
      .locator("h1")
      .first()
      .innerText()
      .catch(() => "");
    const onForm =
      (await page.getByTestId("de-ledger:0").count()) > 0 &&
      /\(Alteration\)/.test(heading);
    rec(
      "T8/8 browser: the posted foreign journal re-opens on the grid",
      onForm,
      `⚠️ IF THIS FAILS THE PROBE BELOW IS VACUOUS — a save that never happened ` +
        `cannot destroy anything. Day Book row clicked: ${String(found)}; ` +
        `heading ${JSON.stringify(heading)}`,
    );
    if (onForm) {
      // ⛔⛔ THE FOREIGN LINE IS NOT ROW 0 ON THE WAY BACK. It was keyed as row 0,
      // but the read re-orders the postings — this voucher comes back CREDIT
      // first, so `de-currency:0` is Export Sales and is CORRECTLY empty. Asserting
      // on index 0 reported "the hydration dropped the triple" against a wire that
      // was measured carrying it, which is a probe that invents a defect. The row
      // is found BY ITS LEDGER, which is the only handle that survives re-ordering.
      const foreignRow = await (async () => {
        for (let i = 0; i < 8; i++) {
          const box = page.getByTestId(`de-ledger:${i}`);
          if ((await box.count()) === 0) break;
          if ((await box.inputValue()) === PARTY) return i;
        }
        return -1;
      })();
      rec(
        "T8/8 browser: ⛔ the foreign line is findable on the re-opened grid",
        foreignRow >= 0,
        `foreignRow=${foreignRow} for ledger ${JSON.stringify(PARTY)} (-1 means ` +
          `no row hydrated with it) — every assertion below reads this row by ` +
          `index, so they are vacuous without it`,
      );
      const at = async (t) =>
        (await page.getByTestId(`${t}:${foreignRow}`).count()) === 0
          ? ""
          : await page.getByTestId(`${t}:${foreignRow}`).inputValue();
      rec(
        "T8/8 browser: ⭐ and it re-opens WITH its currency still on the line",
        (await at("de-currency")) === usd,
        `row ${foreignRow}'s picker shows ${JSON.stringify(await at("de-currency"))} ` +
          `— an empty box here means the hydration dropped the triple, and the ` +
          `save below would then write it away`,
      );
      rec(
        "T8/8 browser: …and its RATE, at ten decimals",
        (await at("de-rate")) === R,
        `row ${foreignRow}'s box shows ${JSON.stringify(await at("de-rate"))}`,
      );
      rec(
        "T8/8 browser: …and its FOREIGN AMOUNT",
        (await at("de-foreign")) === "1000.0000",
        `row ${foreignRow}'s box shows ${JSON.stringify(await at("de-foreign"))} ` +
          `— the third of the triple, and the one an operator reads to know the ` +
          `bill is still a $1,000 bill`,
      );
      rec(
        "T8/8 browser: ⭐ CONTROL — the RUPEE line came back with NO currency",
        (await page.getByTestId(`de-currency:${foreignRow === 0 ? 1 : 0}`).inputValue()) ===
          "",
        `a hydration that put a currency on EVERY row would satisfy every ` +
          `assertion above and be just as wrong`,
      );
      await page.getByTestId("voucher-narration").fill(`${REF} edited`);
      await press("Control+a", 3500);
      await press("Control+a", 3000);
      const after = postingRow(vno);
      rec(
        "T8/8 browser: ⛔⛔ AN ALTER THROUGH THE GRID PRESERVES THE TRIPLE (defect nine)",
        after === before && after.startsWith(usd),
        `psql before ${before}, after ${after} — ⛔ before step 8 the "after" was ` +
          `NULL|NULL|NULL|83651.2300: a $1,000 bill silently became a ₹83,651.23 ` +
          `bill on a NARRATION EDIT, with nothing refusing it`,
      );
      rec(
        "T8/8 browser: ⭐ CONTROL — and the alter really happened",
        writes.some((w) => w.startsWith("PUT journal 200")),
        `browser writes ${JSON.stringify(writes)} — without a PUT the assertion ` +
          `above is "nothing changed because nothing was saved"`,
      );
    }
  }

  // ⛔ EVERY NON-2xx IS AN **EXPECTED** STEP-UP 403, AND "EXPECTED" IS PROVED, NOT
  // ASSUMED: a 403 counts only if the SAME method and path also came back 2xx —
  // which is exactly what the WebAuthn step-up looks like (refuse, do the
  // ceremony, retry). A PUT that 403'd and never succeeded is a denied write, and
  // it fails here rather than hiding inside an unattributable "Failed to load
  // resource" message the harness cannot attribute to any request at all.
  const succeeded = new Set(
    responses.filter((r) => r.ok).map((r) => `${r.method} ${r.path}`),
  );
  const unexpected = failures.filter((f) => {
    const [status, method, path] = f.split(" ");
    return status !== "403" || !succeeded.has(`${method} ${path}`);
  });
  rec(
    "T8/8 browser: ⭐ every non-2xx the browser saw is a step-up 403 THAT THEN SUCCEEDED",
    unexpected.length === 0,
    `all failures ${JSON.stringify(failures)}; unexpected ${JSON.stringify(unexpected)}`,
  );
  // JS exceptions only. The browser logs "Failed to load resource" for the 403s
  // above — answers the UI handled and the assertion above already accounted for.
  // An exception thrown by the app is a different thing entirely (drive-payroll).
  const errs = app.consoleErrors.filter(
    (e) => !/favicon|Download the React|Failed to load resource/i.test(e),
  );
  rec(
    "T8/8 browser: no unexplained javascript errors",
    errs.length === 0,
    errs.slice(0, 3).join(" | ") || "none",
  );
} finally {
  await app.browser.close();
}

process.exit(summary());
