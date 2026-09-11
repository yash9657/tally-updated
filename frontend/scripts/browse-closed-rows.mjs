#!/usr/bin/env node
/**
 * browse-closed-rows.mjs — D109, D110, D114 and D118 IN A REAL BROWSER, plus
 * the wire controls D117's fix must not have broken.
 *
 * ⚠️ THE NAME IS DELIBERATELY NOT A COUNT. It was `browse-five-rows-closed.mjs`
 * for exactly one lane, and D118 made it six — which is D110's own defect
 * (a name that was true when it was written) pointed at a file name.
 *
 * ⛔⛔ WHY A BROWSER PROBE AND NOT ONLY vitest. All three of these rows are
 * defects OF THE SURFACE, and two of them are invisible to a unit test by
 * construction:
 *
 *   D109 is about the BYTES ALT+E WRITES. vitest asserts the sheet builder; only
 *        a browser proves the key writes a file at all, over the SERVER'S OWN
 *        figures rather than a fixture — and this probe sums that file.
 *   D110 is a sentence on a screen. A screen nobody opens says nothing.
 *   D114 is a KEY, and `SCREEN_ACTIONS` in AppShell.tsx is a registration point
 *        that ships a key completely dead with every unit test still green,
 *        because unit tests call the dispatcher directly and bypass the map.
 *        That is exactly how Alt+O shipped.
 *
 * ⛔ IT RUNS AGAINST THE DEMO BOOK AND WRITES NOTHING TO IT. Every action here is
 * a read: opening the import panel imports nothing, opening the match panel
 * matches nothing (it PROPOSES — see D110's own fix), and Alt+E writes to the
 * download directory. §2.5's "dirty a scratch book" rule is about probes that
 * WRITE; this one must use the demo book, because D109's whole assertion is that
 * a file of REAL seeded figures foots.
 *
 * ⚠️ D111 IS NOT HERE, and that is stated rather than hidden. Its drive is
 * `backend/internal/bankstatement/name_integration_test.go`, which imports a
 * nameless statement against real Postgres on a tenant it mints and reaps, with
 * `psql` as the oracle. Driving it here would mean importing a statement into
 * the DEMO book — and `verify:demo` asserts that book has 0 imported lines.
 *
 * RUN:
 *   cd /path/to/tally-updated
 *   CO="$(cat frontend/.demo-company-id)" node frontend/scripts/browse-closed-rows.mjs
 *
 * PREREQS: cored :8087, statutoryd :8091, vite :5173 (a FRESH one — §2.2).
 */

import { readFileSync } from "node:fs";

import { openApp, rec, summary } from "./audit-harness.mjs";

const CO = process.env.CO ?? process.argv[2];
if (!CO) {
  console.error(
    'usage: CO="$(cat frontend/.demo-company-id)" node frontend/scripts/browse-closed-rows.mjs\n' +
      "⛔ an EMPTY company makes the app bootstrap a fresh empty tenant, and every " +
      "screen on it looks clean — including an Outstandings with nothing to sum.",
  );
  process.exit(2);
}

const CORED = "http://localhost:8087";
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

// ---------------------------------------------------------------------------
// Exact money. Every sum below is in integer paise; a probe about a money column
// that adds with `+` is not a probe about money.
// ---------------------------------------------------------------------------
const units = (s) => {
  const m = /^(-?)(\d+)\.(\d{4})$/.exec(String(s).trim());
  if (!m) throw new Error(`not a fixed-4 money string: ${JSON.stringify(s)}`);
  return BigInt(`${m[1]}${m[2]}${m[3]}`);
};
const sum = (xs) => xs.reduce((a, v) => a + units(v), 0n);

/** Parse a CSV the way an operator's spreadsheet does, honouring `="…"`. */
function parseCsv(text) {
  const rows = [];
  let row = [];
  let cell = "";
  let quoted = false;
  for (let i = 0; i < text.length; i++) {
    const c = text[i];
    if (quoted) {
      if (c === '"' && text[i + 1] === '"') {
        cell += '"';
        i++;
      } else if (c === '"') quoted = false;
      else cell += c;
      continue;
    }
    if (c === '"') quoted = true;
    else if (c === ",") {
      row.push(cell);
      cell = "";
    } else if (c === "\n") {
      row.push(cell);
      rows.push(row);
      row = [];
      cell = "";
    } else if (c !== "\r") cell += c;
  }
  if (cell !== "" || row.length) {
    row.push(cell);
    rows.push(row);
  }
  // The export text-forces every exact cell as ="…" so a spreadsheet cannot
  // coerce it. Undo that here, which is what Excel does when it evaluates it.
  return rows.map((r) => r.map((v) => {
    const m = /^="(.*)"$/s.exec(v);
    return m ? m[1].replace(/""/g, '"') : v;
  }));
}

const col = (grid, header) => {
  const i = grid[0].indexOf(header);
  if (i < 0) {
    throw new Error(
      `no ${JSON.stringify(header)} column in the file; headers are ${JSON.stringify(grid[0])}`,
    );
  }
  return grid.slice(1).map((r) => r[i] ?? "");
};
const atLevel = (grid, money, level) => {
  const levels = col(grid, "Level");
  return col(grid, money).filter((_, i) => levels[i] === level);
};
/** The money on rows whose Parent cell is `parent` (D118). */
const under = (grid, money, parent) => {
  const parents = col(grid, "Parent");
  return col(grid, money).filter((_, i) => parents[i] === parent);
};
/** The money on the ONE row with this label. */
const amountOf = (grid, label, money) => {
  const labels = col(grid, grid[0][0]);
  const i = labels.indexOf(label);
  if (i < 0) throw new Error(`no row labelled ${JSON.stringify(label)}`);
  return col(grid, money)[i];
};

// ===========================================================================
const app = await openApp({ company: CO, headless: true });
const { page, body, press, altMac, toGateway } = app;
const flat = async () => (await body()).replace(/\s+/g, " ");

try {
  // =========================================================================
  // D110 — the Bank Reconciliation screen must not deny a feature that is ON it.
  // =========================================================================
  await toGateway();
  await page.keyboard.type("Bank Reconciliation");
  await page.waitForTimeout(400);
  await press("Enter", 4000);

  const h1 = async () =>
    (await page.locator("h1").first().innerText().catch(() => "")).trim();
  rec(
    "D110: Bank Reconciliation opens from the Gateway",
    /Bank Reconciliation/.test(await h1()),
    `h1="${await h1()}"`,
  );

  const note = await page
    .getByTestId("brs-import-note")
    .innerText()
    .catch(() => "");
  const noteFlat = note.replace(/\s+/g, " ");
  rec(
    "D110: ⛔ the closing paragraph NAMES Alt+S",
    /Alt\+S/.test(noteFlat),
    noteFlat.slice(0, 140),
  );
  rec(
    "D110: ⛔ …and no longer says matching is not built",
    !/not built/i.test(noteFlat),
    /not built/i.test(noteFlat) ? "STILL DENIES IT" : "the denial is gone",
  );
  rec(
    "D110: ⭐ CONTROL — it still says nothing is ticked for the operator",
    /PROPOSES/.test(noteFlat) && /Nothing on this screen is ticked for you/.test(noteFlat),
    "the half the old sentence got right survived",
  );

  // =========================================================================
  // D114 — Esc in a panel closes the PANEL, not the screen.
  // =========================================================================
  await altMac("o", 2500);
  rec(
    "D114: Alt+O opens the statement-import panel",
    (await page.getByTestId("stmt-import-panel").count()) > 0,
    `h1="${await h1()}"`,
  );
  await press("Escape", 1500);
  rec(
    "D114: ⛔⛔ ONE Esc returns to the reconciliation statement — NOT the Gateway",
    (await page.getByTestId("stmt-import-panel").count()) === 0 &&
      (await page.getByTestId("brs-as-on").count()) > 0 &&
      !(await app.atGateway()),
    `h1="${await h1()}" (before the fix this said "Gateway of Tally")`,
  );

  await altMac("s", 2500);
  rec(
    "D114: Alt+S opens the matching panel",
    (await page.getByTestId("bank-match-panel").count()) > 0,
    `h1="${await h1()}"`,
  );
  await press("Escape", 1500);
  rec(
    "D114: ⛔ …and ONE Esc closes that panel too, onto the statement",
    (await page.getByTestId("bank-match-panel").count()) === 0 &&
      (await page.getByTestId("brs-as-on").count()) > 0 &&
      !(await app.atGateway()),
    `h1="${await h1()}"`,
  );

  await press("Escape", 1500);
  rec(
    "D114: ⭐⭐ CONTROL — with no panel open, Esc still LEAVES the screen",
    await app.atGateway(),
    "a key that swallowed Esc unconditionally would strand the operator",
  );

  // =========================================================================
  // D109 — the exported file must be summable. The oracle is the FILE.
  // =========================================================================
  const exportOf = async (reportName, { detailed = false } = {}) => {
    await toGateway();
    await app.altMac("g", 900);
    await page.keyboard.type(reportName);
    await page.waitForTimeout(400);
    await press("Enter", 3000);
    // Alt+F1 — the DETAILED view. D118's export names the view in the file, so
    // the probe has to be able to ask for both.
    if (detailed) {
      await page.keyboard.down("Alt");
      await page.keyboard.press("F1");
      await page.keyboard.up("Alt");
      await page.waitForTimeout(1200);
    }
    const [download] = await Promise.all([
      page.waitForEvent("download", { timeout: 15000 }),
      app.altMac("e", 1500),
    ]);
    const path = await download.path();
    return {
      filename: download.suggestedFilename(),
      grid: parseCsv(readFileSync(path, "utf8")),
    };
  };

  /**
   * Run a block of D109 assertions, recording a MISSING COLUMN as a finding
   * rather than crashing the probe.
   *
   * ⛔ IT EXISTS BECAUSE THE MUTATION PROOF NEEDED IT. Reverting the Level
   * column and re-running made `col()` throw, the probe died mid-run, and the
   * summary never printed — so the run that is supposed to DEMONSTRATE the
   * detection looked like a broken probe instead of a caught defect.
   */
  const guard = async (name, fn) => {
    try {
      await fn();
    } catch (err) {
      rec(name, false, String(err.message ?? err).slice(0, 200));
    }
  };

  await guard("D109: the Outstandings file is readable and declares its levels", async () => {
    const { filename, grid } = await exportOf("Outstandings");
    rec(
      "D109: Alt+E on Outstandings writes a file",
      grid.length > 2,
      `${filename}, ${grid.length - 1} rows`,
    );

    const levels = col(grid, "Level");
    rec(
      "D109: ⛔ every row in the file declares WHICH LEVEL it is",
      levels.length > 0 && levels.every((l) => l !== ""),
      `levels: ${[...new Set(levels)].join(", ")}`,
    );

    const stated = atLevel(grid, "Pending", "Total");
    const parties = sum(atLevel(grid, "Pending", "Party"));
    const bills = sum(atLevel(grid, "Pending", "Bill"));
    const everything = sum(
      col(grid, "Pending").filter((_, i) => levels[i] !== "Total"),
    );
    rec(
      "D109: ⛔⛔ summing ONE level equals the file's own Total, to the paisa",
      stated.length === 1 &&
        parties === units(stated[0]) &&
        bills === units(stated[0]),
      `Total=${stated[0]} · Σ Party=${parties} · Σ Bill=${bills}`,
    );
    rec(
      "D109: ⚠️ CONTROL — the whole money column is still exactly 2x, as it should be",
      stated.length === 1 && everything === units(stated[0]) * 2n,
      `Σ everything=${everything} = 2 × ${units(stated[0])} — the duplication IS ` +
        `the report; what was missing was the declaration`,
    );
  });

  await guard("D109: the Stock Summary file is readable and declares its levels", async () => {
    const { filename, grid } = await exportOf("Stock Summary");
    const levels = col(grid, "Level");
    rec(
      "D109: Alt+E on Stock Summary writes a file that declares its levels too",
      grid.length > 2 && levels.every((l) => l !== ""),
      `${filename} · levels: ${[...new Set(levels)].join(", ")}`,
    );
    const stated = atLevel(grid, "Closing Value", "Total");
    const items = sum(atLevel(grid, "Closing Value", "Item"));
    const locations = sum(atLevel(grid, "Closing Value", "Location"));
    rec(
      "D109: ⛔⛔ Stock-in-Hand foots against the ITEM level and the LOCATION level",
      stated.length === 1 &&
        items === units(stated[0]) &&
        locations === units(stated[0]),
      `Stock-in-Hand=${stated[0]} · Σ Item=${items} · Σ Location=${locations}`,
    );
  });
  // =========================================================================
  // D118 â the STATEMENT TREES. Same question as D109, asked of the three
  // reports D109 did not name.
  // =========================================================================
  await guard("D118: the Balance Sheet exports its levels and its parents", async () => {
    const { filename, grid } = await exportOf("Balance Sheet");
    rec(
      "D118: ⛔ the CONDENSED file says so in its own name",
      filename === "Balance-Sheet-condensed.csv",
      filename,
    );
    const levels = col(grid, "Level");
    rec(
      "D118: ⛔ every row of the statement says WHAT it is",
      levels.length > 0 && levels.every((l) => l !== ""),
      `levels: ${[...new Set(levels)].join(", ")}`,
    );

    const liabilities = amountOf(grid, "Liabilities", "Amount");
    const assets = amountOf(grid, "Assets", "Amount");
    rec(
      "D118: ⛔⛔ SUMIF(Parent, <section>) equals that section's own row, to the paisa",
      sum(under(grid, "Amount", "Liabilities")) === units(liabilities) &&
        sum(under(grid, "Amount", "Assets")) === units(assets),
      `Liabilities=${liabilities} Σ=${sum(under(grid, "Amount", "Liabilities"))} · ` +
        `Assets=${assets} Σ=${sum(under(grid, "Amount", "Assets"))}`,
    );
    rec(
      "D118: ⚠️ CONTROL — the whole Amount column is still ~4.2x, as it should be",
      sum(col(grid, "Amount")) > units(liabilities) * 4n &&
        sum(col(grid, "Amount")) < units(liabilities) * 5n,
      `Σ everything=${sum(col(grid, "Amount"))} vs a section total of ${liabilities} — ` +
        `the nesting IS the statement; what was missing was the declaration`,
    );
    // ⭐ THE MEASUREMENT THE FILENAME EXISTS FOR: in the condensed file a deep
    // group's children are only PARTLY present, because `condense` keeps a
    // non-drillable row at any depth plus its ancestors and drops its siblings.
    const ca = amountOf(grid, "Current Assets", "Amount");
    rec(
      "D118: ⚠️ …and a DEEP group in the condensed file is partial — which the name warns about",
      sum(under(grid, "Amount", "Current Assets")) !== units(ca),
      `Current Assets=${ca} but the rows under it sum to ` +
        `${sum(under(grid, "Amount", "Current Assets"))} — press Alt+F1 for a file where it foots`,
    );
  });

  await guard("D118: the DETAILED Balance Sheet is a different file that foots all the way down", async () => {
    const { filename, grid } = await exportOf("Balance Sheet", { detailed: true });
    rec(
      "D118: ⛔ Alt+F1 writes a file that names ITSELF detailed",
      filename === "Balance-Sheet-detailed.csv",
      filename,
    );
    const ca = amountOf(grid, "Current Assets", "Amount");
    rec(
      "D118: ⛔⛔ …and in it a DEEP group foots against its own row",
      sum(under(grid, "Amount", "Current Assets")) === units(ca),
      `Current Assets=${ca} · Σ under it=${sum(under(grid, "Amount", "Current Assets"))}`,
    );
    rec(
      "D118: …and the LEDGER rows the condensed view drops are present",
      col(grid, "Level").includes("Ledger"),
      `levels: ${[...new Set(col(grid, "Level"))].join(", ")}`,
    );
  });

  await guard("D118: the Trial Balance marks its Total row", async () => {
    const { grid } = await exportOf("Trial Balance");
    const levels = col(grid, "Level");
    const stated = amountOf(grid, "Total", "Debit");
    const ledgers = col(grid, "Debit").filter((_, i) => levels[i] !== "Total");
    rec(
      "D118: ⛔ the Total row is no longer indistinguishable from a ledger named Total",
      levels.every((l) => l !== "") && levels.includes("Total"),
      `levels: ${[...new Set(levels)].join(", ")}`,
    );
    rec(
      "D118: ⛔⛔ summing every row that is not the Total equals the Total, to the paisa",
      sum(ledgers) === units(stated),
      `Total=${stated} · Σ ledgers=${sum(ledgers)}`,
    );
    rec(
      "D118: ⚠️ CONTROL — the whole Debit column is still exactly 2x",
      sum(col(grid, "Debit")) === units(stated) * 2n,
      `Σ everything=${sum(col(grid, "Debit"))} = 2 × ${units(stated)}`,
    );
    rec(
      "D118: ⭐ CONTROL — a FLAT report's Parent column is empty, not invented",
      col(grid, "Parent").every((p) => p === ""),
      "nothing on a Trial Balance is anybody's child",
    );
  });

  await guard("D118: the Profit & Loss names Net Profit as computed, not as a section", async () => {
    const { grid } = await exportOf("Profit & Loss A/c");
    const labels = col(grid, grid[0][0]);
    const levels = col(grid, "Level");
    rec(
      "D118: ⛔ Net Profit is `Computed` — it is the sum of nothing in this file",
      levels[labels.indexOf("Net Profit")] === "Computed",
      `Net Profit level = ${levels[labels.indexOf("Net Profit")]}`,
    );
    const income = amountOf(grid, "Income", "Amount");
    rec(
      "D118: ⛔⛔ each section foots against its own row",
      sum(under(grid, "Amount", "Income")) === units(income),
      `Income=${income} · Σ under it=${sum(under(grid, "Amount", "Income"))}`,
    );
  });
} finally {
  await app.browser?.close?.();
}

// ===========================================================================
// D117 — THE WIRE CONTROLS. The fix is a STATUS DECISION, and the way to get it
// wrong in the other direction is to answer 500 for everything: a filer would
// then be told to report a fault when the message names the parameter to change.
// (The 500 itself is driven in backend/internal/server/handler_gstr_datasource_test.go,
// where the database can be made to fail on demand.)
// ===========================================================================
{
  const token = (
    await j("POST", "/auth/token", {
      body: { subject: "browser-dev-owner", amr: ["webauthn"] },
    })
  ).body.token;
  const base = `/v1/companies/${CO}`;

  const ok = await j("GET", `${base}/gstr3b?from=2026-04-01&to=2026-04-30`, { token });
  rec(
    "D117: ⭐ CONTROL — a GSTR-3B the books CAN answer is still a 200",
    ok.status === 200,
    `status ${ok.status}`,
  );

  const badYear = await j("GET", `${base}/gstr9?from=2025-01-01&to=2025-12-31`, { token });
  rec(
    "D117: ⭐ CONTROL — a window that is not a financial year is still a 400 with its own sentence",
    badYear.status === 400 && /financial year/i.test(badYear.body.error ?? ""),
    `status ${badYear.status} — ${(badYear.body.error ?? "").slice(0, 90)}`,
  );

  // ⛔ A NON-NIL statement_id, DELIBERATELY. `uuid.Nil` reads as "not supplied"
  // and takes the ordinary path — which this probe asserted for one run and
  // reported a 200 as a refusal. The id below is a real UUID that names no
  // statement, so the handler takes the ITC branch and answers from it.
  const itcPath = await j(
    "GET",
    `${base}/gstr3b?from=2026-04-01&to=2026-04-30&statement_id=6f1c0b9e-6c2a-4f4a-9d31-6d1a2f0c4e77`,
    { token },
  );
  rec(
    "D117: ⭐ CONTROL — the ITC branch keeps ITS OWN status, ahead of the data-source check",
    itcPath.status === 400 &&
      !/BOOKS_UNREADABLE/.test(itcPath.text) &&
      (itcPath.body.error ?? "") !== "",
    `status ${itcPath.status} — ${(itcPath.body.error ?? "").slice(0, 110)}`,
  );
}

process.exit(summary() > 0 ? 1 : 0);
