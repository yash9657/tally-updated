/**
 * L11 — AI-NATIVE. D16 (PII written into the books), D18 (a draft from the
 * screen carried no input-tax legs and was unbalanced by exactly the tax),
 * D19 (an INTRA-state bill derived as IGST because the screen supplied no
 * tenant ledgers), D23 (the cash-flow response disclosed nothing about being a
 * projection).
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
const cored = async (path) => {
  const r = await fetch(`http://localhost:8087/v1/companies/${process.env.CO}${path}`, {
    headers: { Authorization: `Bearer ${TOKEN}` } });
  return { status: r.status, body: await r.json().catch(() => ({})) };
};

// ⛔ THE FIXTURE CARRIES PII IN THE TWO PLACES THAT BECOME THE VOUCHER: a PAN as
// the invoice NUMBER (which lands in voucher_version.voucher_no) and an Aadhaar
// in a line description. D16 was that the panel promised redaction while these
// two values went through verbatim.
const PAN = "ABCDE1234F";
const AADHAAR = "4321 8765 2109";

/**
 * \u26d4 A FRESH INVOICE **DATE** PER RUN \u2014 not a fresh PAN. The books carry
 * a duplicate-supplier-bill refusal keyed on (supplier, voucher_no, date), and it
 * is CORRECT; a second run on the same tenant answers "This supplier bill is
 * already on the books" and D18 reads as BROKEN. Varying the PAN does **not**
 * help: redaction rewrites every PAN to the SAME literal token, so the stored
 * voucher_no is `[PAN REDACTED]` whatever the input was \u2014 the date is the only
 * part of that key this probe can move. Kept inside August 2026 so the day-book
 * window below still contains it.
 */
const INVOICE_DATE = await (async () => {
  const taken = new Set(
    ((await cored("/day-book?from=2026-08-01&to=2026-08-31")).body.entries ?? [])
      .filter((e) => e.party === "Steel Suppliers" && e.class === "Purchase")
      .map((e) => e.date),
  );
  // \u26d4 AND NOT AFTER TODAY. A probe must not leave the tenant failing the
  // repo's own gate: `verify:demo` asserts BS Sundry Creditors == Outstandings
  // (payable) EXACTLY, and Outstandings with no `as_on` bounds at today while the
  // Balance Sheet does not \u2014 so one future-dated bill breaks that check by
  // exactly its own amount. That divergence is a real defect (registered as D86);
  // this clamp is so THIS probe stops manufacturing it.
  const today = new Date().toISOString().slice(0, 10);
  for (let d = 1; d <= 28; d++) {
    const date = `2026-08-${String(d).padStart(2, "0")}`;
    if (date > today) break;
    if (!taken.has(date)) return date;
  }
  throw new Error(
    "no August date on or before today is free of a Steel Suppliers purchase \u2014 reseed",
  );
})();

/**
 * ⛔ THE FIXTURE SHAPE IS `{classify, input:{pages, ocr, inferred}}` — the fake
 * adapters read those blocks deterministically. A loose bag of invoice fields
 * (which the first run of this file sent) produces "(unknown supplier)" with no
 * inference at all, and every assertion below then measures a DIFFERENT draft:
 * the seeded Pune Fabricators one, whose IGST 3240.0000 is CORRECT (27 vs 29)
 * and which made D19 look live.
 *
 * ⛔⛔ AND THE FIXTURE DELIBERATELY CARRIES **NO `tenant` BLOCK**. That is the
 * whole of D18/D19: the SCREEN must supply the tenant context from these books
 * (`loadTenantContext`). It used to send `{company_id}` alone, so no ledger was
 * offered, the supplier could never be matched, `supplier_state` stayed None,
 * and tax.py only concludes intra-state when BOTH states are known — every bill
 * came out IGST, with no input-tax legs, unbalanced by exactly the tax.
 *
 * ⛔ AND IT IS INTRA-STATE ON PURPOSE: Steel Suppliers is in state 29, the demo
 * company's OWN state, so a correct derivation is CGST+SGST. The seeded draft
 * cannot test this — its supplier is in 27.
 */
/**
 * \u26d4\u26d4 RESOLVE THE LEDGER IDS FROM **THIS** TENANT. They were hardcoded
 * here, which pinned the probe to the one tenant it was authored against: after a
 * reseed those UUIDs belong to a DIFFERENT company, insightd can match no
 * supplier, `supplier_state` stays None, and tax.py falls back to IGST with no
 * input-tax legs \u2014 i.e. the probe reproduced D18 AND D19 *itself* and blamed
 * the product. Read them, and refuse to run if they are missing.
 */
const ledgerIndex = await (async () => {
  const r = await cored("/ledgers");
  if (r.status !== 200) throw new Error(`cannot read this tenant's ledgers: HTTP ${r.status}`);
  return new Map((r.body.ledgers ?? []).map((l) => [l.name, l]));
})();
const needLedger = (name, wantState) => {
  const l = ledgerIndex.get(name);
  if (!l) throw new Error(`this tenant has no "${name}" ledger \u2014 reseed before driving`);
  if (wantState !== undefined && l.state_code !== wantState) {
    throw new Error(`"${name}" is in state ${l.state_code || "<unset>"}, not ${wantState} \u2014 the intra-state case below would not be intra-state`);
  }
  return l.id;
};
// \u26d4 IT IS INTRA-STATE ON PURPOSE and the assertion below depends on it, so
// the state is CHECKED here rather than trusted from a comment.
const STEEL = needLedger("Steel Suppliers", "29");     // the demo company's OWN state
const PURCHASES = needLedger("Raw Material Purchases");
/**
 * \u26d4 insightd's own draft list, used to identify **MY** draft by id. The
 * inbox row label is `party \u00b7 total \u00b7 status \u00b7 pct` and the screen
 * appends `\u00b7 #id` ONLY when two labels collide, so `hasText: "Steel
 * Suppliers"` + `.first()` is ambiguous the moment a previous run left one
 * behind \u2014 it silently measured a STALE draft and reported D18/D19 broken.
 * The render order is the API order (BillInboxScreen does not sort), so the
 * index of the new id in this list is the index of its row.
 */
const listDrafts = async () => {
  // ⛔ THROUGH CORED, WITH THE BEARER (register row D49, 2026-08-24). This used to
  // be a bare `fetch("http://localhost:8088/companies/…/drafts")` with no
  // credential at all, which worked because insightd authenticated nobody. It now
  // answers 401 to anything that did not come through cored's proxy, and the
  // symptom of leaving this line alone would be `cannot list insightd drafts:
  // HTTP 401` — i.e. this probe reading as broken for a reason that has nothing
  // to do with D16/D18/D19.
  const r = await fetch(
    `http://localhost:8087/v1/companies/${process.env.CO}/drafts`,
    { headers: { Authorization: `Bearer ${TOKEN}` } },
  );
  if (!r.ok) throw new Error(`cannot list drafts through cored: HTTP ${r.status}`);
  return await r.json();
};
const draftsBefore = new Set((await listDrafts()).map((d) => d.draft_id));

const ocrLine = (text, y, conf) => ({ text, bbox: [40, y, 1200, y + 22], confidence: conf });
const fixture = {
  classify: { doc_type: "purchase_invoice", is_purchase: true, supplier_ledger_id: STEEL },
  input: {
    pages: [{ png_b64: "", width: 1240, height: 1754, dpi: 200 }],
    ocr: {
      lines: [
        ocrLine("TAX INVOICE", 10, 0.99),
        ocrLine(`Invoice No: ${PAN}`, 40, 0.98),
        ocrLine(`Date: ${INVOICE_DATE}`, 70, 0.98),
        ocrLine("Supplier: Steel Suppliers", 100, 0.97),
        ocrLine(`Aadhaar ${AADHAAR}`, 130, 0.96),
        ocrLine("Place of Supply: 29", 160, 0.97),
        ocrLine("MS Angle HSN 7308 qty 10.0000 rate 100.0000 1000.0000 @ 18.0000%", 200, 0.96),
      ],
      mean_confidence: 0.968,
    },
    inferred: {
      // ⛔ THE PAN IS THE INVOICE NUMBER AND THE AADHAAR IS IN A LINE
      // DESCRIPTION — the two values that BECOME the voucher (voucher_no and the
      // narration). D16 was that the panel promised redaction while these went
      // through verbatim.
      invoice_no: PAN,
      invoice_date: INVOICE_DATE,
      place_of_supply_state_code: "29",
      reverse_charge: false,
      exempt: false,
      line_items: [
        {
          description: `MS Angle for site, ref Aadhaar ${AADHAAR}`,
          hsn: "7308",
          qty: "10.0000",
          rate: "100.0000",
          taxable_value: "1000.0000",
          gst_rate_pct: "18.0000",
          ledger_id: PURCHASES,
          ledger_name: "Raw Material Purchases",
          ledger_confidence: 0.95,
        },
      ],
      totals: { taxable: "1000.0000", invoice_total: "1180.0000" },
    },
  },
};

await toGateway();
await app.openFromGateway("Bill Inbox", 2200);
rec("the Bill Inbox opens", /Bill Inbox/i.test(await body()));
await page.getByTestId("inbox-fixture").fill(JSON.stringify(fixture));
await page.waitForTimeout(300);
await page.getByTestId("inbox-create").click();
await page.waitForTimeout(3500);
const afterCreate = await body();
rec("a draft is created from the fixture", !/Invalid fixture|error/i.test(afterCreate) && !/No pending drafts/.test(afterCreate), afterCreate.slice(0, 90).replace(/\n/g, " | "));

// Open the draft — the panel is what promises redaction and balance.
// \u26a0 OPEN **MY** DRAFT BY NAME. The inbox also holds the two seeded drafts,
// and a blind Enter opened the Pune Fabricators one — whose IGST is correct,
// which is how the first run reported D19 as live.
const listAfter = await listDrafts();
const mineIdx = listAfter.findIndex((d) => !draftsBefore.has(d.draft_id));
const mine = mineIdx >= 0 ? listAfter[mineIdx] : null;
const rows = await page.locator('[role="option"]').allInnerTexts();
rec(
  "the inbox lists the new draft",
  Boolean(mine) && rows.length === listAfter.length && /Steel Suppliers/.test(rows[mineIdx] ?? ""),
  mine
    ? `mine = row ${mineIdx} of ${rows.length}: ${(rows[mineIdx] ?? "<no such row>").replace(/\n/g, " ")}`
    : `no NEW draft appeared; ${listAfter.length} draft(s) all pre-existed`,
);
if (mine) {
  await page.locator('[role="option"]').nth(mineIdx).click();
  await page.waitForTimeout(3000);
}
const panel = await body();

// ---- D16: neither the PAN nor the Aadhaar may reach the F9 prefill.
rec(
  "D16: ⛔ the PAN is NOT carried into the voucher number the panel prefills",
  !panel.includes(PAN),
  panel.includes(PAN) ? `the panel shows ${PAN} verbatim` : "PAN absent from the panel",
);
rec(
  "D16: ⛔ nor is the Aadhaar carried into a narration/description",
  !panel.includes(AADHAAR) && !panel.includes(AADHAAR.replace(/ /g, "")),
  panel.includes(AADHAAR) ? "the panel shows the Aadhaar verbatim" : "Aadhaar absent from the panel",
);
rec(
  "D16: and the panel STATES that redaction happened",
  /redact/i.test(panel),
  (panel.match(/.{0,60}redact.{0,80}/i) ?? ["<no redaction statement>"])[0].replace(/\n/g, " "),
);

// ---- D18/D19: the draft must be BALANCED and derived INTRA-state.
rec(
  "D19: ⛔ an INTRA-state bill derives CGST+SGST, not IGST",
  /CGST/i.test(panel) && /SGST/i.test(panel) && !/IGST\s*[1-9]/i.test(panel),
  (panel.match(/(CGST|SGST|IGST)[^\n]{0,40}/g) ?? ["<no tax lines on the panel>"]).slice(0, 3).join(" | "),
);
rec(
  "D18: the panel does not report an unbalanced draft",
  !/unbalanc|does not balance/i.test(panel),
  (panel.match(/.{0,60}(unbalanc|balance).{0,60}/i) ?? ["<no balance statement>"])[0].replace(/\n/g, " "),
);

// ---- D18: ACCEPT the draft and read the BOOKS. A panel that does not mention
// imbalance is not evidence of balance — the draft used to be unbalanced by
// exactly the tax, and cored's deferred debit=credit trigger would have caught
// it, so the defect showed up as a REFUSED accept rather than a bad voucher.
const beforeAccept = app.net.length;
await press("Control+a", 7000);
const acceptWire = app.net.slice(beforeAccept);
const posted = acceptWire.find((w) => /POST .*\/vouchers\/purchase 201/.test(w));
rec(
  "D18: \u26d4 the draft ACCEPTS — the body it sends balances",
  Boolean(posted),
  acceptWire.filter((w) => /vouchers|drafts/.test(w)).join(" · ") ||
    (((await body()).match(/(Error|refus|balance)[^\n]{0,180}/) ?? [""])[0]),
);

if (posted) {
  // \u26d4 RULE 4 — READ THE BOOKS BACK, by SIDE and by LEDGER NAME.
  // \u26d4 THE ROUTE IS `/day-book`, **NOT** `/reports/day-book`. The wrong guess
  // answers `{"error":"unmapped route: access denied"}` with HTTP 403, and the
  // substring assertion below then reads "no Input CGST in the books" \u2014 a
  // PERMISSION error masquerading as a missing tax leg. Assert the status first.
  const day = await cored("/day-book?from=2026-08-01&to=2026-08-31");
  rec(
    "the day book answers (a 403 here would read as 'no tax legs')",
    day.status === 200,
    `HTTP ${day.status}${day.status === 200 ? "" : ` ${JSON.stringify(day.body)}`}`,
  );
  // Find MY voucher by its party + date, then assert the LEGS by side and name.
  const mineVoucher = (day.body.entries ?? []).find(
    (e) => e.party === "Steel Suppliers" && e.date === INVOICE_DATE && e.class === "Purchase",
  );
  const legs = (mineVoucher?.postings ?? []).map((p) => `${p.dr_cr}:${p.ledger}:${p.amount}`);
  // The whole day book as text, for the PII sweep below \u2014 D16 must hold across
  // EVERY field of every voucher in the window, not just my voucher's legs.
  const asText = JSON.stringify(day.body);
  const has = (leg) => legs.includes(leg);
  rec(
    "D18: the posted purchase carries the INPUT tax legs (not just the base)",
    has("D:Input CGST:90.0000") && has("D:Input SGST:90.0000") &&
      has("D:Raw Material Purchases:1000.0000") && has("C:Steel Suppliers:1180.0000") &&
      !legs.some((l) => /IGST/.test(l)),
    legs.join(" | ") || "<my purchase is not in the day book at all>",
  );
  rec(
    "D16: \u26d4 and NEITHER the PAN NOR the Aadhaar reached the BOOKS",
    !asText.includes(PAN) && !asText.includes(AADHAAR) && !asText.includes(AADHAAR.replace(/ /g, "")),
    `PAN in books: ${asText.includes(PAN)} · Aadhaar in books: ${asText.includes(AADHAAR)}`,
  );
}

// ---- D23: the cash-flow response's own disclosure.
const cf = await cored("/cash-flow");
rec("the cash-flow route answers", cf.status === 200, `HTTP ${cf.status}`);
if (cf.status === 200) {
  const keys = Object.keys(cf.body);
  const text = JSON.stringify(cf.body);
  rec(
    "D23: ⛔ the response DISCLOSES that it is a projection and states its basis",
    /projection|forecast|basis|assum/i.test(text),
    `keys: ${keys.join(", ")}`,
  );
}

console.log("\nNON-GET WIRE:", app.net.filter((w) => !/webauthn/.test(w)).join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
