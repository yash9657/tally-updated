/**
 * T4 SLICE 2b — BANK STATEMENT MATCHING, driven end to end against a live cored.
 *
 * ⛔⛔ THIS PROBE MAKES ITS OWN TENANT AND REAPS IT IN A `finally` (the D91
 * pattern). It does NOT touch the demo tenant: it needs a Data Entry member and
 * the demo tenant has none.
 *
 * ===========================================================================
 * ⭐ THE ONE ARM THAT MATTERS: A PROPOSAL IS NOT A RECONCILIATION
 * ===========================================================================
 * The whole value of a bank reconciliation is that a HUMAN asserted "this line
 * is that voucher". A matcher that ticks its own guesses produces a
 * reconciliation that attests to nothing, and it fails in the direction that
 * FOOTS PERFECTLY: every figure is a real figure off a real statement, the
 * difference is zero, and the one wrong link is invisible until somebody chases
 * a supplier for a payment the books say cleared.
 *
 * So this probe builds the ordinary ambiguous case — two suppliers paid ₹25,000
 * on the same day, one bank row — and asserts the system REFUSES to choose:
 * `ambiguous` true, BOTH candidates returned, `acceptable_in_bulk` false, and
 * ⭐ ZERO bank_reconciliation rows written by the read itself, counted in psql
 * rather than inferred from the response.
 *
 * ===========================================================================
 * ⛔ THE CONTROL PROBES, AND WHAT EACH ONE DISPROVES
 * ===========================================================================
 *  1. THE CLERK IS REALLY A MEMBER. An invite never CLAIMED via GET /v1/me
 *     yields a token that is 403 on EVERY company route — satisfying every
 *     refusal below while proving nothing.
 *  2. ⭐ THE CLERK CAN STILL TICK A BANK DATE BY HAND. This is the control that
 *     makes the probe about the STATEMENT rather than about the bank screen
 *     wholesale: migration 0046 grants Data Entry `alter bank-reconciliation`
 *     deliberately, because ticking one cheque off is clerical. Without this arm
 *     the 403s on matching would be equally consistent with the clerk being
 *     locked out of banking altogether.
 *  3. A PAISA OUT MATCHES NOTHING. A detector that fires on everything is as
 *     useless as one that never fires, and without this arm the "zero tolerance"
 *     claim could pass on a matcher that matched on date alone.
 *  4. AN ALREADY-CLEARED LEG IS NOT OFFERED. Without it, "no candidate" could
 *     mean the matcher was simply not looking.
 *
 * ⚠️ THE OWNER'S 200 ON /matches IS THE ALLOW ARM. A refusal test whose allow
 * arm is untested cannot tell "correctly refused" from "broken for everyone".
 *
 * RUN:  node frontend/scripts/drive-t4-slice2b-matching.mjs   (takes NO CO)
 */
import { execFileSync } from "node:child_process";

import { openApp, rec, summary } from "./audit-harness.mjs";

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
const mint = async (subject) =>
  (await j("POST", "/auth/token", { body: { subject, amr: ["webauthn"] } })).body
    .token;

const psql = (sql) =>
  execFileSync(
    "psql",
    ["-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql],
    { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" },
  ).trim();

const stamp = Math.random().toString(36).slice(2, 8);
const CO = crypto.randomUUID();
const OWNER_SUB = `t4b-owner-${stamp}`;
const OWNER_PW = "t4b-owner-pw-2026";
const CLERK = `t4b-clerk-${stamp}`;
const COMPANY_NAME = `T4b Matching Co ${stamp}`;
const base = `/v1/companies/${CO}`;
const q = (s) => `'${String(s).replace(/'/g, "''")}'`;

/** bank_reconciliation rows for THIS tenant — the ONE table that records clearance. */
const clearances = () =>
  Number(psql(`select count(*) from bank_reconciliation where company_id = ${q(CO)}`));
const matchRows = () =>
  Number(psql(`select count(*) from bank_statement_match where company_id = ${q(CO)}`));

// ---------------------------------------------------------------------------
// THE STATEMENT. Five rows, each one chosen to exercise a different outcome —
// and the running balance walks, so the import's B-4 check actually runs.
// ---------------------------------------------------------------------------
const CSV = [
  "Date,Narration,Withdrawal,Deposit,Balance",
  // (a) matches a leg that has ALREADY been cleared by hand -> no candidate.
  "2026-05-05,STANDING ORDER,500.00,,99500.00",
  // (b) one voucher, same day, same amount -> EXACT and unambiguous.
  "2026-05-14,RENT MAY,1000.00,,98500.00",
  // (c) a paisa out from anything in the books -> no candidate at any tier.
  "2026-05-14,ODD AMOUNT,1000.01,,97499.99",
  // (d) ⭐ TWO vouchers fit it exactly -> AMBIGUOUS, and neither is chosen.
  "2026-05-20,BULK TRANSFER,25000.00,,72499.99",
  // (e) nothing in the books explains it -> the set-aside case.
  "2026-05-22,ACCOUNT MAINTENANCE FEE,177.00,,72322.99",
  "",
].join("\n");

const MAPPING = {
  encoding: "two-columns",
  date_layout: "yyyy-mm-dd",
  value_date: "Date",
  description: "Narration",
  withdrawal: "Withdrawal",
  deposit: "Deposit",
  running_balance: "Balance",
};

const OWNER = await mint(OWNER_SUB);

{
  const r = await j("POST", "/v1/companies", {
    token: OWNER,
    body: {
      id: CO,
      name: COMPANY_NAME,
      fy_start_date: "2026-04-01",
      base_currency: "INR",
      state_code: "29",
    },
  });
  if (r.status !== 201) throw new Error(`create company: ${r.status} ${r.text}`);
}

try {
  // The owner needs a password for the BROWSER arm below.
  {
    const pw = await j("POST", "/auth/password", {
      token: OWNER,
      body: { current_password: "", new_password: OWNER_PW },
    });
    if (pw.status !== 204)
      throw new Error(`set owner password: ${pw.status} ${pw.text}`);
  }

  // -------------------------------------------------------------------------
  // A REAL Data Entry member: mint -> invite -> CLAIM.
  // -------------------------------------------------------------------------
  const CLERK_TOK = await (async () => {
    const tok = await mint(CLERK);
    const inv = await j("POST", `${base}/members/invites`, {
      token: OWNER,
      body: { subject: CLERK, security_level_name: "Data Entry" },
    });
    if (inv.status !== 201) throw new Error(`invite: ${inv.status} ${inv.text}`);
    // ⛔ THE CLAIM. Without this the token is 403 on everything and every
    // refusal below would pass while proving nothing.
    const me = await j("GET", "/v1/me", { token: tok });
    if (me.status !== 200) throw new Error(`claim: ${me.status} ${me.text}`);
    if ((me.body.claimed_invites ?? []).length !== 1)
      throw new Error("the invite was not claimed");
    return tok;
  })();

  console.log(`\n── tenant ${CO}  ·  clerk ${CLERK} ─────────────`);

  // =========================================================================
  // CONTROL 1 — the clerk IS a member.
  // =========================================================================
  {
    const r = await j("GET", `${base}/trial-balance?as_on=2026-07-31`, {
      token: CLERK_TOK,
    });
    rec(
      "T4b: ⛔ CONTROL — the clerk IS a member and reads the Trial Balance",
      r.status === 200,
      `trial-balance = ${r.status} (403 here would mean the invite was never ` +
        `claimed and every refusal below is vacuous)`,
    );
  }

  // =========================================================================
  // THE MASTERS AND THE BOOKS.
  // =========================================================================
  const mkLedger = async (name, group, extra = {}) => {
    const r = await j("POST", `${base}/ledgers`, {
      token: OWNER,
      body: { name, group_name: group, state_code: "29", ...extra },
    });
    if (r.status !== 201)
      throw new Error(`create ledger ${name}: ${r.status} ${r.text}`);
    return r.body.ledger_id ?? r.body.id;
  };
  const BANK = await mkLedger("HDFC Current", "Bank Accounts", {
    bank_account_number: "50200012345678",
    bank_ifsc: "HDFC0001234",
  });
  const RENT = await mkLedger("Rent Paid", "Indirect Expenses");

  const pay = async (no, date, amount, narration) => {
    const r = await j("POST", `${base}/vouchers/payment`, {
      token: OWNER,
      body: {
        voucher_no: no,
        voucher_date: date,
        narration,
        created_by: "probe",
        postings: [
          { ledger_id: RENT, dr_cr: "D", amount },
          { ledger_id: BANK, dr_cr: "C", amount },
        ],
      },
    });
    if (r.status !== 201)
      throw new Error(`post ${no}: ${r.status} ${r.text}`);
    return r.body.voucher_id;
  };

  const V_STANDING = await pay("PAY-1", "2026-05-05", "500.0000", "standing order");
  const V_RENT = await pay("PAY-2", "2026-05-14", "1000.0000", "rent for May");
  // ⭐ THE AMBIGUITY. Two suppliers, same amount, same day — ordinary, not exotic.
  const V_ACME = await pay("PAY-3", "2026-05-20", "25000.0000", "to Acme");
  const V_GLOBEX = await pay("PAY-4", "2026-05-20", "25000.0000", "to Globex");

  // =========================================================================
  // CONTROL 2 — ⛔ THE ONE THAT MAKES THIS PROBE ABOUT THE STATEMENT.
  //
  // Migration 0046 GRANTS Data Entry `alter bank-reconciliation`, deliberately:
  // ticking one cheque off a statement is clerical. If the clerk could not do
  // this, every matching refusal below would be equally consistent with the
  // clerk being locked out of banking altogether.
  // =========================================================================
  {
    const r = await j(
      "PUT",
      `${base}/bank-reconciliation/${BANK}/${V_STANDING}/0`,
      { token: CLERK_TOK, body: { bank_date: "2026-05-05", created_by: "clerk" } },
    );
    rec(
      "T4b: ⛔ CONTROL — the clerk may still TICK A BANK DATE by hand (0046)",
      r.status === 200,
      `PUT bank-reconciliation = ${r.status} (a 403 here would make every ` +
        `matching refusal below attributable to banking in general)`,
    );
  }

  // The import itself — the owner's, since 0063 refuses Data Entry the statement.
  {
    const r = await j("POST", `${base}/bank-statement/${BANK}/import`, {
      token: OWNER,
      body: {
        file_name: `hdfc-may-${stamp}.csv`,
        content: CSV,
        mapping: MAPPING,
        created_by: "probe",
        statement_account_number: "50200012345678",
      },
    });
    rec(
      "T4b: the statement imports, and its balance walk RAN",
      r.status === 200 && r.body.new_lines === 5 && r.body.balance_walked === true,
      `import = ${r.status} new_lines=${r.body.new_lines} ` +
        `balance_walked=${r.body.balance_walked}`,
    );
  }

  // =========================================================================
  // THE ACL — matching borrows authorities that already exist.
  // =========================================================================
  {
    const r = await j("GET", `${base}/bank-statement/${BANK}/matches`, {
      token: CLERK_TOK,
    });
    rec(
      "T4b: the clerk is REFUSED the match proposals (0063's disallow reaches them)",
      r.status === 403,
      `GET matches = ${r.status} — the proposals carry the bank's own rows, ` +
        `including the ones no voucher exists for`,
    );
  }
  {
    const r = await j(
      "POST",
      `${base}/bank-statement/${BANK}/lines/${crypto.randomUUID()}/set-aside`,
      { token: CLERK_TOK, body: { created_by: "clerk" } },
    );
    rec(
      "T4b: the clerk is REFUSED the set-aside write",
      r.status === 403,
      `POST set-aside = ${r.status}`,
    );
  }

  // =========================================================================
  // ⭐ THE PROPOSALS. Read as the OWNER — the allow arm.
  // =========================================================================
  const before = clearances();
  const view = await j("GET", `${base}/bank-statement/${BANK}/matches`, {
    token: OWNER,
  });
  rec(
    "T4b: the OWNER reads the proposals (so the clerk's 403 is the ACL, not a broken route)",
    view.status === 200,
    `GET matches = ${view.status}`,
  );

  // ⛔ ROWS ARE FOUND BY THEIR NARRATION, NOT BY line_no. `line_no` is the
  // position in the FILE and counts the header, so the first data row is 2 —
  // and indexing by the data-row ordinal silently returned `undefined` for
  // every row, which made three assertions pass VACUOUSLY against `?? []`.
  // Matching on the narration is both correct and self-documenting.
  const findRow = (needle) => {
    const hit = (view.body.proposals ?? []).find((p) =>
      p.description.includes(needle),
    );
    if (!hit) throw new Error(`no proposal row matching ${needle}`);
    return hit;
  };
  const rowStanding = findRow("STANDING ORDER");
  const rowRent = findRow("RENT MAY");
  const rowOdd = findRow("ODD AMOUNT");
  const rowAmbiguous = findRow("BULK TRANSFER");
  const rowFee = findRow("ACCOUNT MAINTENANCE FEE");

  rec(
    "T4b: ⛔ NOTHING WAS RECONCILED BY READING — the clearance count did not move",
    clearances() === before,
    `bank_reconciliation rows before=${before} after=${clearances()} ` +
      `(counted in psql, not inferred from the response)`,
  );
  rec(
    "T4b: …and the response SAYS so, unconditionally, in its first disclosure",
    /NOTHING ON THIS SCREEN HAS BEEN RECONCILED/.test(
      (view.body.disclosures ?? []).join(" "),
    ),
    (view.body.disclosures ?? [])[0]?.slice(0, 80) ?? "(no disclosures)",
  );

  // ---- ⭐ INVARIANT B-1 ----------------------------------------------------
  rec(
    "T4b: ⭐ B-1 — two vouchers of the same amount on the same day are AMBIGUOUS",
    rowAmbiguous?.ambiguous === true,
    `BULK TRANSFER ambiguous=${rowAmbiguous?.ambiguous} best_tier=${rowAmbiguous?.best_tier}`,
  );
  rec(
    "T4b: ⭐ B-1 — and BOTH candidates are surfaced, so the operator can choose",
    (rowAmbiguous?.candidates ?? []).length === 2,
    `candidates=${(rowAmbiguous?.candidates ?? []).length} ` +
      `(${(rowAmbiguous?.candidates ?? []).map((c) => c.voucher_no).join(", ")})`,
  );
  rec(
    "T4b: ⭐ B-1 — and it is NOT offered for bulk acceptance",
    rowAmbiguous?.acceptable_in_bulk === false,
    `acceptable_in_bulk=${rowAmbiguous?.acceptable_in_bulk} — picking one is ` +
      `right half the time, and the half it gets wrong is invisible`,
  );

  // ---- the unambiguous exact match ----------------------------------------
  rec(
    "T4b: the rent row is EXACT and unambiguous",
    rowRent?.best_tier === "exact" && rowRent?.acceptable_in_bulk === true,
    `RENT MAY best_tier=${rowRent?.best_tier} bulk=${rowRent?.acceptable_in_bulk}`,
  );
  rec(
    "T4b: …and its candidate carries the REASON, not just a verdict",
    (rowRent?.candidates ?? [])[0]?.why?.length > 10,
    (rowRent?.candidates ?? [])[0]?.why ?? "(no reason given)",
  );
  rec(
    "T4b: …and EVEN AN EXACT MATCH is still only a proposal — nothing is cleared",
    clearances() === before,
    `clearances still ${clearances()}`,
  );

  // ---- CONTROL 3 — zero tolerance -----------------------------------------
  rec(
    "T4b: ⛔ CONTROL — a row one PAISA out matches nothing at any tier",
    (rowOdd?.candidates ?? []).length === 0 && rowOdd?.best_tier === "",
    `ODD AMOUNT (1000.01 against a 1000.00 voucher on the same day): ` +
      `candidates=${(rowOdd?.candidates ?? []).length} — a tolerance here is ` +
      `how a real difference hides for a year`,
  );

  // ---- CONTROL 4 — an already-cleared leg is not a candidate ---------------
  rec(
    "T4b: ⛔ CONTROL — the leg the clerk already ticked is NOT offered again",
    (rowStanding?.candidates ?? []).length === 0,
    `STANDING ORDER candidates=${(rowStanding?.candidates ?? []).length} — offering it ` +
      `would let a second clearance be asserted over the first`,
  );

  rec(
    "T4b: the bank charge matches nothing, and is reported as a row rather than dropped",
    rowFee !== undefined && (rowFee?.candidates ?? []).length === 0,
    `ACCOUNT MAINTENANCE FEE present=${rowFee !== undefined} candidates=${(rowFee?.candidates ?? []).length}`,
  );

  // =========================================================================
  // THE BROWSER ARM — reachable from the keyboard, and B-1 RENDERS.
  // =========================================================================
  {
    const app = await openApp({
      company: CO,
      headless: true,
      expectCompany: COMPANY_NAME,
      subject: OWNER_SUB,
      password: OWNER_PW,
    });
    const { page, body, press, altMac, toGateway } = app;
    try {
      // ⛔ FROM THE GATEWAY, NOT FROM Alt+G. navMachine's BANK_RECON_LABEL doc
      // says it in capitals: "Alt+G Go To IS NOT A THIRD DOOR AND CANNOT BE MADE
      // ONE HERE." Bank Reconciliation is a SCREEN reached from the Gateway
      // menu, not a report name in the Go To picker — typing it there leaves the
      // overlay open and Enter answers "does not apply on this screen", which is
      // exactly what this probe saw before the recipe was corrected.
      await toGateway();
      await page.keyboard.type("Bank Reconciliation");
      await page.waitForTimeout(400);
      await press("Enter", 4000);

      const h = async () =>
        (await page.locator("h1").first().innerText().catch(() => "")).trim();
      rec(
        "T4b: the owner reaches Bank Reconciliation from the keyboard (Gateway)",
        /Bank Reconciliation/.test(await h()),
        `h1="${await h()}"`,
      );

      // ⭐ Alt+S — the key that answered "not built" for two rounds.
      await altMac("s", 2500);
      const seen = (await body()).replace(/\s+/g, " ");
      rec(
        "T4b: ⭐ Alt+S OPENS THE MATCHING PANEL — the key that used to refuse",
        /Match Bank Statement/.test(seen),
        (seen.match(/.{0,20}Match Bank Statement.{0,40}/) ?? [
          "(the panel did not open)",
        ])[0],
      );
      rec(
        "T4b: …and the FIRST thing it says is that nothing has been reconciled",
        (await page.getByTestId("match-disclosures").count()) > 0 &&
          /NOTHING ON THIS SCREEN HAS BEEN RECONCILED/.test(seen),
        "the disclosure block is present and unconditional",
      );
      rec(
        "T4b: ⭐ …and the ambiguous row is drawn as AMBIGUOUS, naming both vouchers",
        /AMBIGUOUS/.test(seen) && /PAY-3/.test(seen) && /PAY-4/.test(seen),
        (seen.match(/.{0,10}AMBIGUOUS.{0,70}/) ?? ["(not drawn)"])[0],
      );
      rec(
        "T4b: ⭐ …and the bulk key offers ONE row, not the ambiguous one",
        /Accept 1 unambiguous exact match/.test(seen),
        (seen.match(/Accept \d+ unambiguous[^·]{0,30}/) ?? ["(button absent)"])[0],
      );
      // Ctrl+J — the exception report, a key that was inert before slice 2a and
      // a refusal after it.
      await press("Control+j", 1200);
      const withExceptions = (await body()).replace(/\s+/g, " ");
      rec(
        "T4b: Ctrl+J draws the EXCEPTION REPORT rather than answering 'not built'",
        (await page.getByTestId("match-exceptions").count()) > 0 &&
          /books-only unreconciled/.test(withExceptions),
        (withExceptions.match(/.{0,30}books-only unreconciled.{0,40}/) ?? [
          "(no exception report)",
        ])[0],
      );
      // Ctrl+B — basis of values.
      await press("Control+b", 1200);
      const withBasis = (await body()).replace(/\s+/g, " ");
      rec(
        "T4b: Ctrl+B states the tolerance and calls the window a SEARCH WIDTH",
        (await page.getByTestId("match-basis").count()) > 0 &&
          /zero tolerance/.test(withBasis) &&
          /search width/.test(withBasis),
        (withBasis.match(/.{0,20}search width.{0,60}/) ?? ["(no basis panel)"])[0],
      );
    } finally {
      await app.browser?.close?.();
    }
  }

  // =========================================================================
  // THE WRITES — every one of them a human asking by name.
  // =========================================================================

  // ⛔ A date that is not the bank row's own is REFUSED, naming BOTH.
  {
    const c = rowRent.candidates[0];
    const r = await j(
      "PUT",
      `${base}/bank-reconciliation/${BANK}/${c.voucher_id}/${c.leg_no}`,
      {
        token: OWNER,
        body: {
          bank_date: "2026-05-19",
          created_by: "probe",
          statement_line_id: rowRent.line_id,
        },
      },
    );
    rec(
      "T4b: ⛔ a resolve whose date is NOT the bank row's own is refused, naming both",
      r.status === 400 && /2026-05-14/.test(r.text) && /2026-05-19/.test(r.text),
      `= ${r.status}: ${r.text.slice(0, 140)}`,
    );
    rec(
      "T4b: ⛔ …and it wrote NOTHING — the refusal rolls the tick back with it",
      clearances() === before,
      `clearances still ${clearances()} (a failed match must not leave a tick ` +
        `nothing points at)`,
    );
  }

  // Accept the unambiguous exact match.
  {
    const c = rowRent.candidates[0];
    const r = await j(
      "PUT",
      `${base}/bank-reconciliation/${BANK}/${c.voucher_id}/${c.leg_no}`,
      {
        token: OWNER,
        body: {
          bank_date: rowRent.value_date,
          created_by: "probe",
          statement_line_id: rowRent.line_id,
        },
      },
    );
    rec(
      "T4b: accepting a proposal writes ONE clearance, through the tick's own route",
      r.status === 200 && clearances() === before + 1 && matchRows() === 1,
      `PUT = ${r.status}; clearances ${before} -> ${clearances()}; ` +
        `match rows ${matchRows()}`,
    );
  }

  // The tier is the SERVER's verdict.
  {
    const tier = psql(
      `select tier from bank_statement_match where company_id = ${q(CO)}`,
    );
    rec(
      "T4b: the stored TIER is the server's own verdict, not a caller's claim",
      tier === "exact",
      `tier=${tier} (there is no request field by which a client could describe ` +
        `its own evidence)`,
    );
  }

  // ⭐ The ambiguous row is STILL waiting.
  {
    const again = await j("GET", `${base}/bank-statement/${BANK}/matches`, {
      token: OWNER,
    });
    const still = (again.body.proposals ?? []).find((p) =>
      p.description.includes("BULK TRANSFER"),
    );
    rec(
      "T4b: ⭐ accepting one row did NOT resolve the ambiguous one — it still waits",
      still?.ambiguous === true && (still?.candidates ?? []).length === 2,
      `BULK TRANSFER ambiguous=${still?.ambiguous} candidates=${(still?.candidates ?? []).length}`,
    );
    const resolvedRent = (again.body.resolved ?? []).find(
      (r) => r.line_id === rowRent.line_id,
    );
    rec(
      "T4b: …and the accepted row has moved to RESOLVED, naming the voucher",
      resolvedRent?.status === "matched" && resolvedRent?.voucher_no === "PAY-2",
      `resolved: status=${resolvedRent?.status} voucher=${resolvedRent?.voucher_no}`,
    );
  }

  // SET ASIDE — a real category, and it writes no clearance.
  {
    const at = clearances();
    const r = await j(
      "POST",
      `${base}/bank-statement/${BANK}/lines/${rowFee.line_id}/set-aside`,
      {
        token: OWNER,
        body: { note: "bank charge, no voucher raised", created_by: "probe" },
      },
    );
    rec(
      "T4b: ⛔ a SET-ASIDE writes no clearance and moves no balance",
      r.status === 200 && clearances() === at,
      `POST = ${r.status}; clearances ${at} -> ${clearances()} — it asserts that ` +
        `this bank row matches NOTHING in the books, which is not a weak match`,
    );
  }

  // Restoring a MATCHED row is refused, and says which key undoes a match.
  {
    const r = await j(
      "POST",
      `${base}/bank-statement/${BANK}/lines/${rowRent.line_id}/restore`,
      { token: OWNER, body: {} },
    );
    rec(
      "T4b: ⛔ 'restore' REFUSES a matched row rather than orphaning its clearance",
      r.status === 400 && /MATCHED/.test(r.text),
      `= ${r.status}: ${r.text.slice(0, 120)}`,
    );
  }

  // ⭐ B-8 — un-ticking withdraws the clearance AND the match, in one operation.
  {
    const c = rowRent.candidates[0];
    const r = await j(
      "DELETE",
      `${base}/bank-reconciliation/${BANK}/${c.voucher_id}/${c.leg_no}`,
      { token: OWNER },
    );
    rec(
      "T4b: ⭐ B-8 — un-ticking the bank date takes the MATCH with it (0088's cascade)",
      r.status === 200 && matchRows() === 1,
      `DELETE = ${r.status}; match rows now ${matchRows()} (the set-aside, which ` +
        `is not a clearance and must survive) — a match outliving its clearance ` +
        `would leave the leg un-ticked while the bank row still read as matched`,
    );
    const again = await j("GET", `${base}/bank-statement/${BANK}/matches`, {
      token: OWNER,
    });
    const back = (again.body.proposals ?? []).find(
      (p) => p.line_id === rowRent.line_id,
    );
    rec(
      "T4b: …and the bank row is BACK among the proposals, unmatched",
      back !== undefined && back?.best_tier === "exact",
      `line returned=${back !== undefined} best_tier=${back?.best_tier}`,
    );
  }
} finally {
  // =========================================================================
  // ⛔ REAP, IN A `finally`. §5.14 — a probe that creates must clean up, and
  // must ASSERT it worked. This tenant HAS books AND an imported statement.
  // =========================================================================
  psql(
    `begin;
     -- THE TRIGGERS MUST BE DISARMED: posting_immutable RAISES on DELETE.
     set local session_replication_role = replica;
     delete from bank_statement_match where company_id = ${q(CO)};
     delete from bank_statement_line  where company_id = ${q(CO)};
     delete from bank_statement_import where company_id = ${q(CO)};
     delete from bank_reconciliation  where company_id = ${q(CO)};
     delete from posting_cost_allocation where company_id = ${q(CO)};
     delete from voucher_gst_line     where company_id = ${q(CO)};
     delete from bill_allocation      where company_id = ${q(CO)};
     delete from posting              where company_id = ${q(CO)};
     -- ⛔ HEAD BEFORE VERSION: voucher_head.current_version_id is NOT NULL, and
     -- session_replication_role disarms TRIGGERS, not NOT NULL.
     delete from voucher_head         where company_id = ${q(CO)};
     delete from voucher_version      where company_id = ${q(CO)};
     delete from ledger_balance       where company_id = ${q(CO)};
     delete from acl_rule             where company_id = ${q(CO)};
     delete from company_membership   where company_id = ${q(CO)};
     delete from company_invite       where company_id = ${q(CO)};
     delete from notification_outbox  where company_id = ${q(CO)};
     delete from master_audit         where company_id = ${q(CO)};
     delete from ledger               where company_id = ${q(CO)};
     delete from godown               where company_id = ${q(CO)};
     delete from unit                 where company_id = ${q(CO)};
     delete from voucher_type         where company_id = ${q(CO)};
     delete from account_group        where company_id = ${q(CO)};
     delete from security_level       where company_id = ${q(CO)};
     delete from company              where id = ${q(CO)};
     commit;`,
  );
  const left = psql(`select count(*) from company where id = ${q(CO)}`);
  rec(
    "T4b: ⛔ the probe reaped its own tenant, and asserted it worked",
    left === "0",
    `companies matching ${CO.slice(0, 8)}…: ${left}`,
  );
}

process.exit(summary());
