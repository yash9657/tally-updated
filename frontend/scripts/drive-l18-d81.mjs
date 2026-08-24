/**
 * L18 — D81: a voucher the SERVER refuses leaves ledger masters behind, and until
 * this lane the operator was never told which.
 *
 * ⛔⛔ THE ORACLE IS NOT "NO MASTER EXISTS". It cannot be: a posting needs real
 * ledger uuids, so the masters must be created before the POST that may be refused,
 * and cored has NO DELETE /ledgers to compensate with. The master gate (D39/D41)
 * closed every refusal the CLIENT can make; its own header says this window stays
 * open and calls it NOT CLOSED. So the oracle is: THE ORPHAN IS NAMED, and the
 * advice given about it is TRUE.
 *
 * ⛔ THE MINTED SET COMES FROM THE WIRE, NOT FROM THE SCREEN. `GET /ledgers` before
 * and after, diffed BY ID by this script, is what the browser's sentence is checked
 * against. Both sides from one handler would prove only that the handler agrees with
 * itself, and this defect is precisely one where it could agree and lie (§2.2, §5.19).
 *
 * ⛔ AND THE ADVICE IS EXERCISED, NOT JUST READ. The sentence promises "retrying this
 * accept REUSES them rather than creating more". The control arm clears the cutoff,
 * presses Ctrl+A again, and asserts the retry posted while creating ZERO further
 * ledgers. A disclosure can be fully present and fully wrong (§5.19); this is the arm
 * that would catch it.
 *
 * ⛔⛔ THIS PROBE MAKES ITS OWN TENANT AND REAPS IT (the D91 pattern), for two
 * reasons beyond the usual one: it MINTS A LEDGER, and there is NO ROUTE THAT DELETES
 * A LEDGER — that is the defect. A master minted in the demo tenant could not be
 * reaped through the product at all, so the probe brings its own books and drops them
 * whole. It also sets a BACK-DATED CUTOFF on the Owner level, which is a mutation of
 * the tenant's security posture that must not be left on shared books.
 *
 * RUN:  node frontend/scripts/drive-l18-d81.mjs        (takes NO CO — it makes one)
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
  try { parsed = text ? JSON.parse(text) : {}; } catch { parsed = { raw: text }; }
  return { status: r.status, body: parsed, text };
};
const mint = async (subject) =>
  (await j("POST", "/auth/token", { body: { subject, amr: ["webauthn"] } })).body.token;

const psql = (sql) =>
  execFileSync("psql", [
    "-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql,
  ], { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" }).trim();

const stamp = Math.random().toString(36).slice(2, 8);
const CO = crypto.randomUUID();
const OWNER_SUB = `d81-owner-${stamp}`;
const OWNER_PW = "d81-owner-pw-2026";
const COMPANY_NAME = `D81 Probe Co ${stamp}`;
/** The account this accept must MINT. Stamped so a re-run cannot resolve an old one. */
const NEW_EXPENSE = `D81 Orphan Expense ${stamp}`;

const OWNER = await mint(OWNER_SUB);
{
  const pw = await j("POST", "/auth/password", {
    token: OWNER, body: { current_password: "", new_password: OWNER_PW },
  });
  if (pw.status !== 204) throw new Error(`set owner password: ${pw.status} ${pw.text}`);
  const r = await j("POST", "/v1/companies", {
    token: OWNER,
    body: {
      id: CO, name: COMPANY_NAME, fy_start_date: "2026-04-01",
      base_currency: "INR", state_code: "29",
    },
  });
  if (r.status !== 201) throw new Error(`create company: ${r.status} ${r.text}`);
}
const base = `/v1/companies/${CO}`;
console.log(`\n── tenant ${CO}  ·  owner ${OWNER_SUB} ─────────────`);

const chart = async () => {
  const r = await j("GET", `${base}/ledgers`, { token: OWNER });
  if (r.status !== 200) throw new Error(`list ledgers: ${r.status} ${r.text}`);
  return new Map((r.body.ledgers ?? []).map((l) => [l.id, l.name]));
};

/**
 * ⛔ THE SERVER-ONLY REFUSAL. A back-dated cutoff in the FUTURE refuses every write
 * dated today, and the Owner level is the one level whose cutoff may be set
 * (ErrOwnerLevelImmutable allows exactly this). It is judged on `VoucherDate`, so a
 * POST /ledgers — which carries no date — is NOT refused: the masters are created
 * and the voucher behind them is not. That asymmetry IS the defect.
 */
const ownerLevelID = await (async () => {
  const r = await j("GET", `${base}/security-levels`, { token: OWNER });
  const owner = (r.body.security_levels ?? []).find((l) => l.is_owner);
  if (!owner) throw new Error(`no owner level: ${r.status} ${r.text}`);
  return owner.id;
})();
const setCutoff = async (value) => {
  const r = await j("PUT", `${base}/security-levels/${ownerLevelID}`, {
    token: OWNER, body: { backdated_cutoff: value },
  });
  if (r.status !== 200 && r.status !== 204) {
    throw new Error(`set cutoff ${value}: ${r.status} ${r.text}`);
  }
};
await setCutoff("2027-01-01");

const before = await chart();
console.log(`   chart before: ${before.size} ledgers`);

const app = await openApp({
  company: CO,
  headless: true,
  expectCompany: COMPANY_NAME,
  subject: OWNER_SUB,
  password: OWNER_PW,
});
const { page, body, press } = app;
const status = async () =>
  (await page.locator('[role="status"]').first().innerText().catch(() => "")).replace(/\s+/g, " ").trim();

try {
  // ---- F5 Payment, single entry: one bank account, one NEW particular.
  await press("F5", 1200);
  await page.waitForTimeout(600);
  const accountBox = page.locator('#selection-list li').first();
  const accountName = (await accountBox.innerText().catch(() => "")).trim();
  await accountBox.click();
  await page.waitForTimeout(300);
  await page.getByTestId("single-ledger:0").fill(NEW_EXPENSE);
  await page.getByTestId("single-amount:0").fill("2500");
  await page.waitForTimeout(300);

  await page.keyboard.down("Control");
  await page.keyboard.press("KeyA");
  await page.keyboard.up("Control");
  await page.waitForTimeout(2500);

  const refusedText = await status();
  const after = await chart();
  const minted = [...after].filter(([id]) => !before.has(id)).map(([, name]) => name);

  // =========================================================================
  // ⛔ THE ORPHAN IS REAL, AND THE WIRE SAYS SO — NOT THE SCREEN.
  // =========================================================================
  rec("D81: ⛔ the refused accept DID leave a master on the chart (wire diff)",
      minted.length === 1 && minted[0] === NEW_EXPENSE,
      `GET /ledgers gained [${minted.join(", ")}] (${before.size} -> ${after.size})`);

  rec("D81: ⛔ …and NO voucher was written",
      (await j("GET", `${base}/day-book?from=2026-04-01&to=2027-03-31`, { token: OWNER }))
        .body?.entries?.length === 0 ||
      (await j("GET", `${base}/day-book?from=2026-04-01&to=2027-03-31`, { token: OWNER }))
        .body?.entries === undefined,
      "day book carries no entries for this tenant");

  // =========================================================================
  // ⛔⛔ THE ROW ITSELF: the status line NAMES the master it minted.
  // =========================================================================
  rec("D81: ⛔⛔ the status line NAMES the orphaned master",
      refusedText.includes(NEW_EXPENSE) && /had already created/.test(refusedText),
      refusedText.slice(0, 240) || "(empty status line)");

  rec("D81: ⛔ …and the SERVER's own refusal is still FIRST and unaltered",
      /back-dated cutoff/.test(refusedText) &&
        refusedText.indexOf("back-dated cutoff") < refusedText.indexOf("had already created"),
      (refusedText.match(/.{0,80}back-dated cutoff.{0,40}/) ?? ["(the cutoff sentence is absent)"])[0]);

  rec("D81: …and it says a retry REUSES the master, and that nothing can undo it",
      /Retrying this accept REUSES/.test(refusedText) &&
        /no route that deletes a ledger/.test(refusedText),
      refusedText.slice(-160));

  // =========================================================================
  // ⚠️ THE KEYING. Measured at HEAD BEFORE this lane: it ALREADY survived — the
  // accept reducer replaces the frame, but the form is component state and
  // nothing resets it. Pinned as a REGRESSION guard and labelled honestly: this
  // arm was green before the change and proves nothing about it (§5.23).
  // =========================================================================
  const keptLedger = await page.getByTestId("single-ledger:0").inputValue();
  const keptAmount = await page.getByTestId("single-amount:0").inputValue();
  rec("D81: ⚠️ the keyed values survive the refusal (TRUE BEFORE THIS LANE TOO — a pin, not a fix)",
      keptLedger === NEW_EXPENSE && keptAmount === "2500",
      `ledger="${keptLedger}" amount="${keptAmount}" account="${accountName}"`);

  // =========================================================================
  // ⛔⛔ THE CONTROL THAT MAKES THE ADVICE LOAD-BEARING. The sentence promises a
  // retry REUSES the master. Clear the cutoff, press Ctrl+A again: it must post,
  // and it must create ZERO further ledgers. A sentence that is present and
  // false is worse than none (§5.19), and only this arm can tell them apart.
  // =========================================================================
  await setCutoff("");
  await page.waitForTimeout(400);
  await page.keyboard.down("Control");
  await page.keyboard.press("KeyA");
  await page.keyboard.up("Control");
  await page.waitForTimeout(2500);

  const savedText = await status();
  const afterRetry = await chart();
  const mintedTwice = [...afterRetry].filter(([id]) => !after.has(id)).map(([, n]) => n);
  const dayBook = await j("GET", `${base}/day-book?from=2026-04-01&to=2027-03-31`, { token: OWNER });
  const entries = dayBook.body?.entries ?? [];

  rec("D81: ⛔ CONTROL — the retry POSTS (the disclosure delayed nothing)",
      entries.length === 1,
      `day book entries = ${entries.length}`);
  rec("D81: ⛔ CONTROL — and it created ZERO further ledgers, exactly as the sentence promised",
      mintedTwice.length === 0 && afterRetry.size === after.size,
      `chart ${after.size} -> ${afterRetry.size}, new: [${mintedTwice.join(", ")}]`);
  rec("D81: CONTROL — a SUCCESSFUL accept says nothing about masters",
      /Saved/.test(savedText) && !/had already created/.test(savedText),
      savedText.slice(0, 160));

  // ⛔ THE BOOKS, READ BACK: SIDES AND LEDGER NAMES, never totals.
  const vid = entries[0]?.voucher_id;
  if (vid) {
    const v = await j("GET", `${base}/vouchers/${vid}`, { token: OWNER });
    // ⚠️ THE FIELD NAMES ARE `ledger` AND A ONE-LETTER `dr_cr`, NOT `ledger_name`
    // AND NOT "DR"/"CR". The first run of this probe read both wrongly and printed
    // "C undefined | D undefined" — which is §5.7's failure exactly, and it only
    // showed because the arm asserted on the CONTENT rather than on a count.
    const legs = (v.body?.postings ?? [])
      .map((p) => `${p.dr_cr} ${p.ledger}`)
      .sort();
    rec("D81: ⛔ the posted voucher debits the ONCE-ORPHANED account and credits the bank",
        legs.length === 2 &&
          legs.some((l) => l === `D ${NEW_EXPENSE}`) &&
          legs.some((l) => l.startsWith("C ")),
        legs.join(" | ") || "(no postings)");
  } else {
    rec("D81: ⛔ the posted voucher debits the ONCE-ORPHANED account and credits the bank",
        false, "no voucher id in the day book");
  }

  const seen = (await body()).replace(/\s+/g, " ");
  rec("D81: …and the screen is not a crash or a stuck spinner",
      !/\bNaN\b/.test(seen) && !/Loading…$/.test(seen),
      "no NaN / spinner-stuck state");
} finally {
  await app.browser?.close?.();
}

// ===========================================================================
// ⛔ REAP. §5.14 — and here it is the ONLY way the minted master can go, since
// the absence of a DELETE /ledgers route is the defect itself.
// ===========================================================================
{
  const q = (s) => `'${String(s).replace(/'/g, "''")}'`;
  // ⛔ THE TABLE NAMES ARE MEASURED, NOT GUESSED, AND THE FIRST RUN GOT THEM WRONG.
  // There is no `cost_allocation` (it is `posting_cost_allocation`) and no `voucher`
  // (it is `voucher_head`). `bill_allocation` hangs off `voucher_version_id`, NOT
  // `posting_id`. And `voucher_head.current_version_id` points AT `voucher_version`,
  // so the head must go first or the FK blocks the delete.
  //
  // ⚠️ THIS TENANT HAS BOOKS, unlike L17's — the control arm posts a voucher on
  // purpose. So the voucher tables below are load-bearing here in a way they were
  // not there, and a reap copied from that probe fails on this one.
  // ⛔⛔ AND `posting` IS APPEND-ONLY BY TRIGGER — `posting_no_mutate` RAISES on any
  // DELETE, as does `voucher_version_no_mutate`. That is a correctness property of
  // the ledger and it is not being weakened: `SET LOCAL session_replication_role`
  // is TRANSACTION-LOCAL, so it reverts at COMMIT and touches nothing outside these
  // statements. It is spelled out rather than hidden because suppressing an
  // immutability trigger is exactly the sort of line that must never read as
  // routine. It is permissible HERE only because this tenant was minted by this
  // probe sixty seconds ago and its books are two postings the probe itself wrote.
  // ⛔ NEVER run this shape against books an operator keyed.
  psql(
    `begin;
     set local session_replication_role = replica;
     delete from posting_cost_allocation where company_id = ${q(CO)};
     delete from bill_allocation    where company_id = ${q(CO)};
     delete from voucher_gst_line   where voucher_version_id in (select id from voucher_version where company_id = ${q(CO)});
     delete from posting            where voucher_version_id in (select id from voucher_version where company_id = ${q(CO)});
     delete from voucher_head       where company_id = ${q(CO)};
     delete from voucher_version    where company_id = ${q(CO)};
     delete from voucher_number_counter where company_id = ${q(CO)};
     delete from acl_rule           where company_id = ${q(CO)};
     delete from company_membership where company_id = ${q(CO)};
     delete from company_invite     where company_id = ${q(CO)};
     delete from notification_outbox where company_id = ${q(CO)};
     delete from ledger             where company_id = ${q(CO)};
     delete from godown             where company_id = ${q(CO)};
     delete from unit               where company_id = ${q(CO)};
     delete from voucher_type       where company_id = ${q(CO)};
     delete from account_group      where company_id = ${q(CO)};
     delete from security_level     where company_id = ${q(CO)};
     delete from company            where id = ${q(CO)};
     commit;`,
  );
  const left = psql(`select count(*) from company where id = ${q(CO)}`);
  rec("D81: ⛔ the probe reaped its own tenant — including the master nothing else can delete",
      left === "0", `companies matching ${CO.slice(0, 8)}…: ${left}`);
}

summary();
