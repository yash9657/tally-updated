/**
 * YEAR-END CLOSURE — the lock, its two back doors, and its controls.
 *
 * ⛔ WHY THIS EXISTS AND WHAT IT ALREADY CAUGHT
 * ===========================================================================
 * The company-wide year-end lock (migration 0089) reuses the PDP's hardened
 * back-dated-cutoff path, and authz.Evaluate was correct from the first commit:
 * asked about a write into a closed year, it denied it. The hermetic tests all
 * passed.
 *
 * THIS DRIVE FOUND A DELETE WALKING STRAIGHT OUT OF A CLOSED YEAR WITH A 200.
 * The middleware reads the date a voucher ALREADY carries only when a lock
 * could act on it — an optimisation gated on `level.BackdatedCutoff != nil`,
 * written when that was the only lock there was. A year-end lock is the second
 * kind, and at a company using it every level's own cutoff is still nil, so the
 * read was skipped, the PDP was asked about a nil date, and cutoffDeny returned
 * early. A DELETE carries no body, so nothing else in the request could catch
 * it.
 *
 * ⭐ THE LESSON: A NEW SOURCE OF TRUTH INHERITS THE ENFORCEMENT IT REUSES BUT
 * NOT THE GATES IN FRONT OF IT. A unit test that builds the PDP request by hand
 * cannot notice a caller that never builds it.
 *
 * ⛔ IT RUNS ON A SCRATCH COMPANY YOU PASS IN, AND IT WRITES. A pay run's worth
 * of journals land in the books it is given, and it closes and re-opens that
 * company's year. NEVER point it at Demo Traders — `verify:demo` reads that
 * book and a moved Trial Balance reads as a broken Balance Sheet to the next
 * lane.
 *
 * RUN:  (cd frontend && pnpm seed:demo -- --name "Year End Scratch")
 *       node frontend/scripts/drive-year-end-close.mjs "$(cat frontend/.year-end-scratch-company-id)"
 *       (needs cored :8087 only — there is no browser half here.)
 */
const B = "http://localhost:8087";
const CO = process.argv[2];
const base = `/v1/companies/${CO}`;
const tok = (await (await fetch(B + "/auth/token", { method: "POST",
  headers: { "content-type": "application/json" },
  body: JSON.stringify({ subject: "browser-dev-owner", amr: ["webauthn"] }) })).json()).token;
const call = async (m, p, body) => {
  const r = await fetch(B + p, { method: m,
    headers: { Authorization: `Bearer ${tok}`, "content-type": "application/json" },
    body: body ? JSON.stringify(body) : undefined });
  const t = await r.text(); let j = null; try { j = JSON.parse(t); } catch {}
  const out = { status: r.status, text: t, j };
  globalThis.__last = out;
  return out;
};
let pass = 0, fail = 0;
let last = null;
const ok = (c, m, d = "") => { c ? (pass++, console.log("  PASS", m, d && `— ${d}`))
                                 : (fail++, console.log("  FAIL", m, `— ${d}`,
                                     globalThis.__last ? `[${globalThis.__last.status} ${globalThis.__last.text.slice(0,180)}]` : "")); };

// A journal we can post repeatedly. Two legs, balanced.
const led = await call("GET", `${base}/ledgers`);
const rows = Array.isArray(led.j) ? led.j : (led.j?.ledgers ?? []);
const cash = rows.find(l => l.name === "Cash");
const sales = rows.find(l => l.name === "Rent");
if (!cash || !sales) { console.error("ledger lookup failed", rows.map(l=>l.name)); process.exit(2); }
const jrn = (date) => ({ voucher_date: date, narration: "year-end drive",
  postings: [{ ledger_id: cash.id, dr_cr: "D", amount: "100.0000" },
          { ledger_id: sales.id, dr_cr: "C", amount: "100.0000" }] });

// ⛔ RESET TO A KNOWN STATE. A drive that assumes the books are open passes
// once and then refuses itself on the second run — the state it sets is the
// state it depends on.
await call("DELETE", `${base}/books-closure`);

console.log("== 1. BEFORE closing: both years accept a posting ==");
const preOld = await call("POST", `${base}/vouchers/journal`, jrn("2026-06-15"));
const preNew = await call("POST", `${base}/vouchers/journal`, jrn("2027-06-15"));
ok(preOld.status === 201, "a 2026-06-15 journal posts", preOld.text.slice(0,200));
ok(preNew.status === 201, "a 2027-06-15 journal posts", `status ${preNew.status}`);

console.log("== 2. CLOSE FY 2026-27 ==");
const closed = await call("POST", `${base}/books-closure`, { closed_through: "2027-03-31" });
ok(closed.status === 200 && closed.j?.closed === true && closed.j?.closed_through === "2027-03-31",
   "the books close through 2027-03-31", closed.text.slice(0, 120));
ok(typeof closed.j?.closed_by === "string" && closed.j.closed_by.length > 0,
   "the closure records WHO closed it", `by ${closed.j?.closed_by}`);

console.log("== 3. THE LOCK BITES, and says which lock it is ==");
const inClosed = await call("POST", `${base}/vouchers/journal`, jrn("2026-06-15"));
ok(inClosed.status === 403, "a journal INSIDE the closed year is refused", `status ${inClosed.status}`);
ok(/closed through 2027-03-31/.test(inClosed.text),
   "…and the refusal names the year-end lock and its date", inClosed.text.slice(0, 200));
const lastDay = await call("POST", `${base}/vouchers/journal`, jrn("2027-03-31"));
ok(lastDay.status === 403, "the LAST day of the closed year is refused (through is inclusive)",
   `status ${lastDay.status}`);
const firstOpen = await call("POST", `${base}/vouchers/journal`, jrn("2027-04-01"));
ok(firstOpen.status === 201, "the FIRST day of the open year still posts", `status ${firstOpen.status}`);

console.log("== 4. THE DOORS ROUND THE BACK (the round-3 hardening still holds) ==");
const victim = preOld.j?.voucher_id ?? preOld.j?.id;
const del = await call("DELETE", `${base}/vouchers/${victim}`);
ok(del.status === 403, "a DELETE of a voucher inside the closed year is refused",
   `status ${del.status} ${del.text.slice(0, 120)}`);
const lift = await call("PUT", `${base}/vouchers/journal/${victim}`, jrn("2027-06-15"));
ok(lift.status === 403, "an ALTER lifting it OUT of the closed year is refused",
   `status ${lift.status} ${lift.text.slice(0, 120)}`);

console.log("== 5. READS ARE UNAFFECTED — the point of closing ==");
const tb = await call("GET", `${base}/trial-balance`);
ok(tb.status === 200, "the Trial Balance still reads over the closed year", `status ${tb.status}`);

console.log("== 6. CLOSING ONLY MOVES FORWARD ==");
const back = await call("POST", `${base}/books-closure`, { closed_through: "2026-03-31" });
ok(back.status === 409 && back.j?.code === "BOOKS_ALREADY_CLOSED_LATER",
   "closing BACKWARD is refused 409 with a code", `${back.status} ${back.j?.code}`);
ok(/re-open/i.test(back.text), "…and it says what to do instead", back.text.slice(0, 160));

console.log("== 7. RE-OPEN, and the lock lifts ==");
const reopened = await call("DELETE", `${base}/books-closure`);
ok(reopened.status === 200 && reopened.j?.closed === false, "the books re-open", reopened.text.slice(0, 120));
const after = await call("POST", `${base}/vouchers/journal`, jrn("2026-06-15"));
ok(after.status === 201, "a journal in the formerly-closed year posts again", `status ${after.status}`);
const again = await call("DELETE", `${base}/books-closure`);
ok(again.status === 409 && again.j?.code === "NOTHING_CLOSED",
   "re-opening when nothing is closed is refused, not silently OK", `${again.status} ${again.j?.code}`);

console.log(`\n${pass}/${pass + fail} checks passed`);
process.exit(fail ? 1 : 0);
