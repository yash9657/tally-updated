/**
 * L1r — BILL-WISE. D10 (two NEW refs on one invoice collapsed into one bill),
 * D11 (ADVANCE / ON_ACCOUNT stored as ordinary named bills), D32 (a Credit Note
 * could never print because the Agst Ref picker offered nothing).
 */
import { openApp, rec, summary } from "./audit-harness.mjs";
const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, toGateway } = app;

const TOKEN = await (async () => {
  const r = await fetch("http://localhost:8087/auth/token", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject: "browser-dev-owner", amr: ["webauthn"] }),
  });
  return (await r.json()).token;
})();
const get = async (path) => {
  const r = await fetch(`http://localhost:8087/v1/companies/${process.env.CO}${path}`, {
    headers: { Authorization: `Bearer ${TOKEN}` },
  });
  return { status: r.status, body: await r.json().catch(() => ({})) };
};
const pickLedger = async (locator, name) => {
  await locator.click(); await page.keyboard.type(name); await page.waitForTimeout(500);
  const top = await page.locator('[role="option"]').first().innerText().catch(() => "");
  await press("Enter", 400);
  return top.trim();
};

// ============ D10 / D11 — key a SALES invoice with TWO new bill references
await toGateway();
await press("F8", 1500);
rec("F8 opens a Sales voucher", /Sales/.test(await body()));
// \u26a0 THE ACCOUNTING-MODE SALES FORM HAS NO PARTICULARS LEDGER (the base leg
// is implicit in the orientation) and its bill picker is `billrefs-*`, not
// `bill:0-*`. The first run of this file waited 30s for `single-ledger:0`.
const party = page.getByLabel("Party A/c Name");
await party.click(); await page.keyboard.type("Acme Traders"); await page.waitForTimeout(600);
await press("Enter", 500);
const set = async (id, v) => { const e = page.getByTestId(id); if (await e.count()) { await e.fill(v); await page.waitForTimeout(150); return true; } return false; };
await set("gst-taxable", "10000");
await set("gst-rate", "18");
await set("gst-hsn", "7326");
await page.waitForTimeout(700);

// TWO NEW references, deliberately UNNAMED, on ONE invoice — D10's shape.
rec("the bill-reference picker is on the form", (await page.getByTestId("billrefs-add").count()) === 1);
await page.getByTestId("billrefs-type:0").selectOption("NEW").catch(() => {});
await set("billrefs-amount:0", "5000");
await page.getByTestId("billrefs-add").click();
await page.waitForTimeout(500);
await page.getByTestId("billrefs-type:1").selectOption("NEW").catch(() => {});
await set("billrefs-amount:1", "6800");
await page.waitForTimeout(400);
const refsText = await body();
rec("two NEW references are keyed on one invoice", /New Ref/i.test(refsText) && (await page.getByTestId("billrefs-amount:1").count()) === 1);

const before = app.net.length;
await press("Control+a", 6000);
const wire = app.net.slice(before);
const posted = wire.some((w) => /POST .*\/vouchers\/sales 201/.test(w));
const screenText = await body();
rec(
  "the invoice with two NEW refs either SAVES or is refused legibly",
  posted || /Error|refus/i.test(screenText),
  posted ? "saved" : (screenText.match(/(Error|refus)[^\n]{0,170}/) ?? [""])[0],
);

if (posted) {
  // \u26d4 D10's ORACLE IS THE BOOKS, NOT THE 201. Two references must be TWO
  // bills with DISTINCT names: post() used to stamp the allocated voucher number
  // onto EVERY unnamed reference with no uniqueness check, and Outstandings
  // groups by (ledger_id, ref_name) — so two references became one row.
  //
  // \u26a0 THE REPORT IS AT `/outstandings` (PLURAL). `/outstanding` answers 403
  // "unmapped route: access denied", which reads exactly like a permission
  // problem — D40's shape, and the third route this session where it cost time.
  const out = await get("/outstandings?as_on=2027-03-31");
  rec("the Outstandings report answers", out.status === 200, `HTTP ${out.status}`);
  const asText = JSON.stringify(out.body);
  rec(
    "D10: \u26d4 BOTH references appear in Outstandings as separate bills",
    /5000\.0000/.test(asText) && /6800\.0000/.test(asText),
    `5000 present: ${/5000\.0000/.test(asText)} · 6800 present: ${/6800\.0000/.test(asText)}`,
  );
  const names = [...asText.matchAll(/"(Sales\/[^"]+)"/g)].map((m) => m[1]);
  const uniq = new Set(names);
  rec(
    "D10: and the two bill NAMES are distinct (they used to collapse)",
    names.length >= 2 && uniq.size === names.length,
    [...uniq].slice(0, 6).join(" | ") || "(no bill names in the report)",
  );
}

// ============================ D32 — a Credit Note's Agst Ref picker
await toGateway();
await page.keyboard.down("Alt"); await page.keyboard.press("F6"); await page.keyboard.up("Alt");
await page.waitForTimeout(1600);
rec("Alt+F6 opens a Credit Note", /Credit Note/i.test(await body()));
{
  const cnParty = page.getByLabel("Party A/c Name");
  await cnParty.click(); await page.keyboard.type("Acme Traders"); await page.waitForTimeout(700);
  await press("Enter", 900);
  await page.getByTestId("gst-taxable").fill("1000").catch(() => {});
  await page.getByTestId("gst-rate").fill("18").catch(() => {});
  await page.waitForTimeout(900);

  const type0 = page.getByTestId("billrefs-type:0");
  const kinds = (await type0.locator("option").allInnerTexts()).map((t) => t.trim());
  rec(
    "D11: the four reference KINDS are each their own choice",
    kinds.length === 4 && /new/i.test(kinds[0]) && kinds.some((k) => /advance/i.test(k)) && kinds.some((k) => /on account/i.test(k)),
    kinds.join(" | "),
  );
  await type0.selectOption("AGAINST");
  await page.waitForTimeout(1000);

  // \u26a0 THE AGAINST ROW IS AN <input list="billrefs-openbills"> BACKED BY A
  // DATALIST — not a <select>, and not a role="option" listbox. Two runs of this
  // probe read an empty option list and reported D32 as still live.
  const bill = page.getByTestId("billrefs-bill:0");
  rec("D32: the AGAINST row renders a bill field", (await bill.count()) === 1, `list=${await bill.getAttribute("list")}`);
  const offered = await page.locator("datalist#billrefs-openbills option").evaluateAll((els) =>
    els.map((e) => ({ value: e.getAttribute("value") ?? "", label: e.textContent ?? "" })),
  );
  // \u26d4 D32: this list took its contents from useOpenBills(..., mode === "settle")
  // — false on a credit note — so it was EMPTY, a Credit Note could never name the
  // invoice it adjusts, and printing then refused the class outright ("a note
  // nobody can match to an invoice cannot be applied to any invoice").
  rec(
    "D32: \u26d4 the picker OFFERS this party's open bills",
    offered.length > 0,
    offered.length ? offered.slice(0, 4).map((o) => `${o.value} (${o.label.trim()})`).join(" | ") : "(the picker offered NOTHING — D32 as reported)",
  );
  const mine = offered.find((o) => /Sales\/2026-27\//.test(o.value));
  rec("D32: and the invoices this session raised are among them", Boolean(mine), mine ? mine.value : offered.slice(0, 3).map((o) => o.value).join(" | "));

  if (mine) {
    await bill.fill(mine.value);
    await page.waitForTimeout(500);
    await page.getByTestId("billrefs-amount:0").fill("1180").catch(() => {});
    await page.waitForTimeout(600);
    const refusal = await body();
    rec(
      "D32: naming the bill CLEARS the 'does not say which bill it settles' refusal",
      !/does not say which bill it settles/.test(refusal),
      (refusal.match(/does not say which bill[^|\n]{0,60}/) ?? ["(refusal gone)"])[0],
    );
    const before3 = app.net.length;
    await press("Control+a", 6000);
    const w3 = app.net.slice(before3);
    const cnPosted = w3.find((x) => /POST .*\/vouchers\/credit-note 201/.test(x));
    rec("D32: the Credit Note SAVES against that bill", Boolean(cnPosted), w3.filter((x) => /credit-note/.test(x)).join(" · ") || (await body()).match(/(Error|refus)[^\n]{0,140}/)?.[0] || "");
  }
}

// ============================ D11 — ADVANCE / ON_ACCOUNT are not ordinary bills
await toGateway();
await press("F6", 1600);
rec("F6 opens a Receipt", /Receipt/i.test(await body()));
{
  const acct = page.getByLabel("Account").first();
  if (await acct.count()) {
    await acct.click(); await page.keyboard.type("HDFC Bank"); await page.waitForTimeout(600); await press("Enter", 400);
  }
  const leg = page.getByTestId("single-ledger:0");
  if (await leg.count()) {
    await leg.click(); await page.keyboard.type("Acme Traders"); await page.waitForTimeout(600); await press("Enter", 400);
    await page.getByTestId("single-amount:0").fill("2500");
    await page.waitForTimeout(800);
  }
  const t0 = page.getByTestId("billrefs-type:0");
  if (await t0.count()) {
    const kinds = (await t0.locator("option").allInnerTexts()).map((t) => t.trim());
    rec(
      "D11: the reference types include ADVANCE and ON ACCOUNT as their own kinds",
      kinds.some((k) => /advance/i.test(k)) && kinds.some((k) => /on account/i.test(k)),
      kinds.join(" | "),
    );
    await t0.selectOption({ label: kinds.find((k) => /advance/i.test(k)) ?? "" }).catch(() => {});
    await page.getByTestId("billrefs-amount:0").fill("2500").catch(() => {});
    await page.waitForTimeout(400);
    const before2 = app.net.length;
    await press("Control+a", 6000);
    const w2 = app.net.slice(before2);
    const ok = w2.some((x) => /POST .*\/vouchers\/receipt 201/.test(x));
    rec("D11: an ADVANCE receipt saves", ok, w2.filter((x) => /receipt/.test(x)).join(" · ") || (await body()).match(/(Error|refus)[^\n]{0,140}/)?.[0] || "");
  }
}

console.log("\nNON-GET WIRE:", app.net.join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
