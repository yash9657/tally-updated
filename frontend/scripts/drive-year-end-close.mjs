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
 *       (needs cored :8087 AND vite :5173 — §8 drives the screen in a browser.)
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

// ===========================================================================
// 8. THE SCREEN, IN A REAL BROWSER, AGAINST THIS SAME SERVER
// ===========================================================================
// ⛔ EVERYTHING ABOVE IS THE WIRE, AND THE WIRE IS NOT THE PRODUCT. The screen
// tests drive DataScreen against the MOCK; §1-§7 drive cored with fetch. Only
// this section proves the two meet — which is the gap D112's own drive existed
// to close for payroll, and the gap that let eight payroll routes ship with no
// screen at all.
console.log("== 8. THE SCREEN (Alt+Y > Data), in a browser ==");
const { openApp, rec, summary } = await import("./audit-harness.mjs");
const app = await openApp({ company: CO, expectCompany: null });
const { page, altMac } = app;
const testid = (t) => page.getByTestId(t);
const textOf = async (t) => ((await testid(t).count()) ? (await testid(t).first().innerText()) : "");

await altMac("y", 1200);
rec("Alt+Y opens the Data screen", (await testid("data-screen").count()) > 0);

const stateBefore = await textOf("data-closure-state");
rec("the screen states the CURRENT position, not a blank",
    /open|closed/i.test(stateBefore), stateBefore.slice(0, 120));

await testid("data-close-through").fill("2027-03-31");
await testid("data-close-button").click();
await page.waitForTimeout(1500);
const stateAfter = await textOf("data-closure-state");
rec("closing from the screen locks the year on the SERVER",
    stateAfter.includes("2027-03-31") && /closed/i.test(stateAfter), stateAfter.slice(0, 160));

// ⛔ THE ORACLE IS THE WIRE, NEVER THE SCREEN. D107: a screen that renders
// "closed" off its own keypress looks identical to one that closed nothing.
const wire = await call("GET", `${base}/books-closure`);
rec("…and the SERVER agrees the books are closed through 2027-03-31",
    wire.j?.closed === true && wire.j?.closed_through === "2027-03-31", wire.text.slice(0, 140));

// The refusal, in the server's words, on the screen.
await testid("data-close-through").fill("2026-03-31");
await testid("data-close-button").click();
await page.waitForTimeout(1200);
const err = await textOf("data-error");
rec("closing BACKWARD is refused on screen, in the server's words",
    /already closed/i.test(err), err.slice(0, 160));
rec("…and the lock did NOT move",
    (await textOf("data-closure-state")).includes("2027-03-31"));

await testid("data-reopen-button").click();
await page.waitForTimeout(1500);
const reopenedText = await textOf("data-closure-state");
const wire2 = await call("GET", `${base}/books-closure`);
rec("re-opening from the screen lifts the lock on the SERVER",
    /open|not been closed/i.test(reopenedText) && wire2.j?.closed === false,
    `${reopenedText.slice(0, 80)} | wire ${wire2.text.slice(0, 60)}`);

// ⛔ THE EXPECTED REFUSALS ARE ENUMERATED, NOT FILTERED OUT — "ignore the
// failures" is how a probe stops measuring anything. This drive PROVOKES
// exactly two non-2xx responses and both are designed behaviour:
//
//   403  the WebAuthn step-up on the session's first write (the ceremony runs
//        and the write is retried; every drive in this repo meets it)
//   409  the backward-close we asked for on purpose, four probes above
//
// Anything else — and any real JavaScript error — must still fail this.
const expectedRefusal = /status of (403|409)/;
const unexpected = app.consoleErrors.filter((e) => !expectedRefusal.test(e));
rec("no javascript errors beyond the two refusals this drive provokes on purpose",
    unexpected.length === 0,
    unexpected.length === 0
      ? `${app.consoleErrors.length} expected refusal log(s), 0 unexplained`
      : unexpected.slice(0, 2).join(" | "));
await app.browser.close();

const browserFails = summary();
console.log(`\n${pass}/${pass + fail} wire checks passed`);
process.exit(fail || browserFails ? 1 : 0);
