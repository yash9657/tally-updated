/**
 * L15 — D5 + D43: `company.allow_negative_stock` had two enforcers and no writer.
 *
 * ⛔ THE WIRE ARM IS THE ORACLE AND THE BROWSER ARM IS THE DOOR. Both are here
 * because the defect had two ends. D5 is a missing WRITE SIDE (no route, no
 * setter, no control); D43 is the same absence seen by the caller (the create
 * accepted the key, answered 201, and dropped it). A browser can prove the
 * control now exists and reaches the row. Only the wire can prove the thing the
 * setting is FOR — that the refusal actually lifts and comes back.
 *
 * ⛔⛔ NOTHING HERE ASSERTS ON A STATUS CODE OR A RESPONSE BODY AS THE PROOF OF
 * A SETTING. That is the exact shape of D43: POST /v1/companies answered 201 to
 * `allow_negative_stock: true` for the whole life of the route while the column
 * stayed false, so any probe reading the response would have gone green against
 * the defect. Every setting assertion below is `SELECT allow_negative_stock
 * FROM company` over a direct psql connection — out of band, and not a surface
 * cored or the browser owns.
 *
 * ⚠️ AND THE ACCEPT/REFUSE PAIR IS NOT A STATUS CHECK EITHER. A 201 on the
 * accept arm is only meaningful because the SAME BODY was refused moments
 * earlier and is refused again moments later, on the same company, with only
 * the setting moving between. A single accept proves nothing; the pair does.
 *
 * ⛔ IT RUNS ON ITS OWN FRESH TENANT, NOT THE DEMO BOOKS. The accept arm
 * deliberately drives a stock position below zero, and leaving that in the demo
 * company would silently re-price its Stock Summary and Balance Sheet for every
 * later probe. `CO` is still read and used for the BROWSER arm, because that
 * arm changes a setting and changes it straight back.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l15-d5.mjs
 */
import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";

import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://localhost:8087";
const CO = process.env.CO;
const STAMP = randomUUID().slice(0, 8);

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

// ⚠️ MINT ONCE, FOR `browser-dev-owner`. auth.mts's authHeaders() mints for a
// fresh RANDOM subject, which is right for a script that creates its own tenant
// and wrong for one that also drives an existing one — and this file does both.
const TOK = (await j("POST", "/auth/token", {
  body: { subject: "browser-dev-owner", amr: ["webauthn"] },
})).body.token;
if (!TOK) throw new Error("drive-l15-d5: could not mint a token (is cored up with AUTH_DEV_TOKEN_ENABLED=true?)");
const api = (method, path, body) => j(method, path, { token: TOK, body });

/** Read one value as `postgres` — the out-of-band oracle. */
const psql = (sql) =>
  execFileSync("psql", [
    "-h", "localhost", "-p", "5433", "-U", "postgres", "-d", "tally", "-tAc", sql,
  ], { env: { ...process.env, PGPASSWORD: "postgres" }, encoding: "utf8" }).trim();

/**
 * ⛔ THE ONLY PERMITTED READ OF THE SETTING.
 *
 * It throws on anything that is not exactly "t" or "f" rather than coercing.
 * A missing row returns "" and would coerce to false — which reads as "the
 * setting is off" and is really "there is no company", and that difference is
 * the whole of the arm below it.
 */
const flagInDB = (companyID) => {
  const raw = psql(`SELECT allow_negative_stock FROM company WHERE id = '${companyID}'`);
  if (raw !== "t" && raw !== "f") {
    throw new Error(
      `flagInDB(${companyID}): psql answered ${JSON.stringify(raw)}, which is neither ` +
        `"t" nor "f". Refusing to coerce — an absent row must not read as "off".`,
    );
  }
  return raw === "t";
};

// ===========================================================================
// ARM A — THE WIRE, ON A FRESH TENANT
// ===========================================================================

// --- D43: the key the create accepts must reach the column. ---------------
const madeWith = async (label, body) => {
  const id = randomUUID();
  const res = await api("POST", "/v1/companies", {
    id,
    name: `D5 ${label} ${STAMP}`,
    fy_start_date: "2025-04-01",
    state_code: "29",
    ...body,
  });
  if (res.status !== 201) throw new Error(`create company (${label}) = ${res.status} ${res.text}`);
  return id;
};

const coTrue = await madeWith("true", { allow_negative_stock: true });
rec(
  "D43: create with allow_negative_stock:true reaches the COLUMN",
  flagInDB(coTrue) === true,
  `company ${coTrue} -> ${flagInDB(coTrue)} (before the fix this was 201 + f)`,
);

const coFalse = await madeWith("false", { allow_negative_stock: false });
rec(
  "D43: an EXPLICIT false reaches the column too (not 'always write true')",
  flagInDB(coFalse) === false,
  `company ${coFalse} -> ${flagInDB(coFalse)}`,
);

const co = await madeWith("omitted", {});
rec(
  "D43: an OMITTED key takes the RESTRICTIVE default",
  flagInDB(co) === false,
  `company ${co} -> ${flagInDB(co)} — an escape hatch nobody asked for must be shut`,
);

// --- The fixture: one item, in the seeded location, with NO stock. ---------
const base = `/v1/companies/${co}`;
const unit = (await api("POST", `${base}/units`, { name: `NOS${STAMP}`, decimal_places: 0 })).body;
const item = (await api("POST", `${base}/stock-items`, {
  name: `Widget ${STAMP}`,
  unit_name: `NOS${STAMP}`,
  gst_rate_pct: "18.00",
})).body;
const itemID = item.stock_item_id ?? item.id;
const godowns = (await api("GET", `${base}/godowns`)).body;
const godownID = (godowns.godowns ?? godowns)[0]?.id ?? "";
const cash = (await api("POST", `${base}/ledgers`, { name: `Cash ${STAMP}`, group_name: "Cash-in-Hand" })).body;
const sales = (await api("POST", `${base}/ledgers`, { name: `Sales ${STAMP}`, group_name: "Sales Accounts" })).body;

rec(
  "fixture: an item with NO opening and NO receipts exists",
  Boolean(itemID) && Boolean(godownID),
  `item=${itemID} godown=${godownID}`,
);

/**
 * The identical outward voucher, three times. ⛔ IDENTICAL IS LOAD-BEARING —
 * the only thing that may differ between the refusal and the accept is the
 * setting, or the pair proves nothing about the setting.
 */
const issueOne = (n) => api("POST", `${base}/vouchers/sales`, {
  voucher_date: "2025-06-01",
  voucher_no: `NEG-${STAMP}-${n}`,
  party: "Walk-in",
  narration: `D5 negative-stock probe ${n}`,
  // ⚠️ "D"/"C", NOT "DR"/"CR". The REST CLIENT normalises those (rest.ts's
  // wireDrCr); this probe speaks to cored directly and gets no such favour. The
  // first run of this file sent "DR" and every voucher arm came back
  // `422 invalid dr_cr "DR"` — which is NOT the negative-stock refusal, and the
  // arms failed rather than passing, because each one matches on the refusal's
  // SENTENCE and not on the status code.
  postings: [
    { ledger_id: cash.ledger_id, dr_cr: "D", amount: "1000.0000" },
    { ledger_id: sales.ledger_id, dr_cr: "C", amount: "1000.0000" },
  ],
  inventory: [
    { stock_item_id: itemID, godown_id: godownID, direction: "OUT", qty: "5.0000", rate: "200.0000", amount: "1000.0000" },
  ],
});

// --- 1. SETTING OFF -> REFUSED. -------------------------------------------
const off1 = await issueOne(1);
rec(
  "D5 (control): with the setting OFF the issue is REFUSED",
  off1.status >= 400 && /negative stock/i.test(off1.text),
  `${off1.status} — ${off1.text.slice(0, 160).replace(/\n/g, " ")}`,
);

// --- 2. FLIP IT THROUGH THE PRODUCT, and read the ROW. --------------------
const put = await api("PUT", base, { allow_negative_stock: true });
rec(
  "D5: PUT /v1/companies/{id} answers 200 with the STORED value",
  put.status === 200 && put.body.allow_negative_stock === true,
  `${put.status} ${JSON.stringify(put.body)}`,
);
rec(
  "⛔ D5 ORACLE: the COLUMN moved — read out of Postgres, not off the response",
  flagInDB(co) === true,
  `company ${co} -> ${flagInDB(co)}`,
);

// --- 3. THE SAME VOUCHER IS NOW ACCEPTED. ---------------------------------
const on = await issueOne(2);
rec(
  "⛔⛔ D5 ORACLE: the IDENTICAL issue is now ACCEPTED",
  on.status === 201,
  `${on.status} ${on.text.slice(0, 120).replace(/\n/g, " ")} — same body as arm 1, only the setting moved`,
);

// The books, read back through a surface neither the setting nor the post owns.
//
// ⚠️ MATCHED BY `stock_item_id`, AND IT THROWS RATHER THAN COMPARING A MISSING
// FIELD. The first run of this arm matched on `r.stock_item_name ?? r.name` —
// the wire field is `item` — so every row projected `undefined`, `.includes()`
// ran on "", nothing matched, and the arm reported "<row not found>". It failed
// LOUDLY only because the fallback was a string; had it been a number the arm
// would have compared 0 < 0 and read as a clean, meaningless false. That is
// §4.6 exactly, and the fix is to make an absent projection an EXCEPTION.
const summaryRes = await api("GET", `${base}/stock-summary`);
const sumRows = summaryRes.body.rows ?? [];
const mine = sumRows.find((r) => r.stock_item_id === itemID);
if (!mine) {
  throw new Error(
    `stock-summary has no row for stock_item_id ${itemID}. Rows carry ` +
      `${JSON.stringify(Object.keys(sumRows[0] ?? {}))} — do not "fix" this by ` +
      `loosening the match; find the field that identifies the item.`,
  );
}
if (typeof mine.closing_qty !== "string") {
  throw new Error(`stock-summary row has no closing_qty: ${JSON.stringify(mine)}`);
}
rec(
  "⛔ D5 ORACLE: the Stock Summary shows a NEGATIVE closing quantity",
  Number(mine.closing_qty) < 0,
  `closing_qty = ${mine.closing_qty} in ${mine.godown} for "${mine.item}"`,
);
// ⛔ AND THE VALUE IS ZERO AT A NEGATIVE QUANTITY, WHICH IS THE POINT OF THE
// WARNING ON THE F11 PANEL. An issue with no stock to consume has no COST to
// consume either, and the valuer refuses to invent one from the sale rate
// (internal/inventory/valuation_test.go pins that reasoning). So turning the
// setting on does not make the position harmless — it makes closing valuation
// wrong until a receipt arrives to price it, and the panel says so. This arm is
// what stops that sentence from being decorative.
rec(
  "D5: a negative position carries ZERO value — the panel's warning is true",
  mine.closing_value === "0.0000" && Number(mine.closing_qty) < 0,
  `closing_qty ${mine.closing_qty} at closing_value ${mine.closing_value}`,
);

// --- 4. FLIP IT BACK, and the refusal RETURNS. ----------------------------
const back = await api("PUT", base, { allow_negative_stock: false });
rec(
  "D5: the switch goes BOTH ways (a one-way switch is not a switch)",
  back.status === 200 && flagInDB(co) === false,
  `${back.status} ${JSON.stringify(back.body)} · column -> ${flagInDB(co)}`,
);
const off2 = await issueOne(3);
rec(
  "⛔⛔ D5 ORACLE: with the setting OFF again the SAME issue is REFUSED again",
  off2.status >= 400 && /negative stock/i.test(off2.text),
  `${off2.status} — ${off2.text.slice(0, 160).replace(/\n/g, " ")}`,
);

// --- 5. THE SECOND KEYSTROKE (§4.4). --------------------------------------
// ⛔ A REFUSAL IS NOT THE END OF THE STORY; THE REMEDY IS. D89 lived exactly
// here — the operator's next action after a refusal. The remedy this refusal
// invites is "turn the setting on and try again", so the probe does that, and
// then makes sure turning it off again does not leave the accept behind as a
// silent success.
const remedy = await api("PUT", base, { allow_negative_stock: true });
const retry = await issueOne(4);
await api("PUT", base, { allow_negative_stock: false });
rec(
  "§4.4: the remedy the refusal invites actually works, and once",
  remedy.status === 200 && retry.status === 201,
  `PUT ${remedy.status} then re-issue ${retry.status}`,
);
const posted = psql(
  `SELECT count(*) FROM voucher_head WHERE company_id = '${co}' AND is_deleted = false`,
);
rec(
  "§4.4: exactly TWO vouchers exist — the two accepts, and no ghost from the two refusals",
  posted === "2",
  `voucher_head count = ${posted}. Four issues were attempted: arms 1 and 3 were ` +
    `REFUSED and must have written nothing, arms 2 and 4 were accepted. ` +
    `(The first run of this file asserted 3 and was simply wrong about its own ` +
    `arithmetic — the probe was corrected, not the code.)`,
);

// --- 6. THE ACL READING THAT LET THIS SHIP WITH NO MIGRATION. -------------
const anon = await j("PUT", base, { body: { allow_negative_stock: true } });
rec(
  "D5: the new route is not open — an unauthenticated PUT is refused",
  anon.status === 401 || anon.status === 403,
  `${anon.status} ${anon.text.slice(0, 90)}`,
);
rec(
  "D5: ...and it did not move the column on its way to being refused",
  flagInDB(co) === false,
  `company ${co} -> ${flagInDB(co)}`,
);

// ===========================================================================
// ARM B — THE BROWSER: the CONTROL that did not exist
// ===========================================================================
// ⛔ ON THE DEMO TENANT, AND IT PUTS THE SETTING BACK. This arm changes a
// setting and reverts it in the same run; it never posts a voucher, so it
// cannot leave a negative position behind the way arm A deliberately does.
if (!CO) {
  rec("browser arm SKIPPED: CO is not set", false, "export CO=$(cat /tmp/drive-company-id)");
} else {
  const before = flagInDB(CO);
  const app = await openApp({ company: CO, headless: true });
  const { page, press, altMac, toGateway } = app;
  try {
    await toGateway();
    await press("F11", 1500);

    const panel = page.getByTestId("negative-stock-panel");
    rec("D5: the F11 screen has a control at all", (await panel.count()) === 1);

    const stateText = async () =>
      (await page.getByTestId("negative-stock-state").innerText().catch(() => "<absent>")).replace(/\n/g, " ");

    // ⛔ IT MUST SHOW THE ROW'S STATE, NOT A DEFAULT. A control that renders
    // "REFUSED" without reading is right by accident on a fresh tenant and
    // wrong on every tenant that ever turned it on.
    const shown = await stateText();
    rec(
      "D5: the control READS its own state back from the company",
      before ? /ALLOWED/.test(shown) : /REFUSED/.test(shown),
      `column=${before} · screen says "${shown}"`,
    );

    await page.getByTestId("negative-stock-allow").click();
    await page.waitForTimeout(1200);
    rec(
      "⛔ D5 ORACLE (browser): clicking Allow moved the COLUMN",
      flagInDB(CO) === true,
      `company ${CO} -> ${flagInDB(CO)} · screen says "${await stateText()}"`,
    );

    await page.getByTestId("negative-stock-refuse").click();
    await page.waitForTimeout(1200);
    rec(
      "⛔ D5 ORACLE (browser): clicking Refuse moved it back",
      flagInDB(CO) === false,
      `company ${CO} -> ${flagInDB(CO)} · screen says "${await stateText()}"`,
    );

    // The note that USED to describe the defect must now describe the control.
    const page_text = await page.locator("body").innerText();
    rec(
      "D22-shaped: the Negative stock row no longer claims the opt-in is unbuilt",
      /Negative stock/.test(page_text) && !/THE PER-COMPANY OPT-IN IS NOT BUILT/.test(page_text),
      /THE PER-COMPANY OPT-IN IS NOT BUILT/.test(page_text)
        ? "the screen still says the opt-in is not built"
        : "the row names the control",
    );
  } finally {
    // ⛔ RESTORE, whatever happened above. A probe that leaves the demo tenant
    // permissive would make every later inventory probe measure the wrong
    // engine — and would look like a passing run while doing it.
    if (flagInDB(CO) !== before) {
      await api("PUT", `/v1/companies/${CO}`, { allow_negative_stock: before });
    }
    rec(
      "cleanup: the demo tenant's setting is back where it started",
      flagInDB(CO) === before,
      `company ${CO} -> ${flagInDB(CO)}, started ${before}`,
    );
    await app.browser.close();
  }
}

summary();
