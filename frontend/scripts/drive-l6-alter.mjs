/**
 * D41 — altering an existing Purchase from the Day Book used to create a
 * "Purchases" ledger over the wire and THEN fail the voucher write with
 * "invalid gst line gst_rate_pct". A refused alter left a master behind.
 */
import { openApp, rec, summary } from "./audit-harness.mjs";
const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, altMac, toGateway } = app;

const ledgersBefore = await (async () => {
  const r = await fetch(`http://localhost:8087/auth/token`, {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject: "browser-dev-owner", amr: ["webauthn"] }),
  });
  const { token } = await r.json();
  const res = await fetch(`http://localhost:8087/v1/companies/${process.env.CO}/ledgers`, {
    headers: { Authorization: `Bearer ${token}` },
  });
  return (await res.json()).ledgers.length;
})();

await toGateway();
await altMac("g"); await page.waitForTimeout(300);
await page.keyboard.type("Day Book"); await page.waitForTimeout(400); await press("Enter", 2000);

// Find a PURCHASE row: it credits a supplier. The Day Book shows the party, so
// the supplier names are the handle — Steel Suppliers is the seeded creditor.
const rows = page.locator('[role="grid"] [role="row"]');
const n = await rows.count();
let opened = false;
for (let i = 0; i < n; i++) {
  const txt = (await rows.nth(i).innerText()).replace(/\n/g, " | ");
  if (!/Steel Suppliers|Pune Fabricators/i.test(txt)) continue;
  await rows.nth(i).click(); await page.waitForTimeout(250);
  await press("Enter", 2500);
  // \u26d4 CHECK THE HEADING, NOT THE BODY. Every voucher screen's button bar
  // carries "F9 Purchase", so /Purchase/ against the body matches on EVERY
  // class — the first run of this probe altered a Receipt Note believing it was
  // a Purchase, and only the heading assertion caught it.
  const h = await page.locator("h1").first().innerText().catch(() => "");
  if (/^Purchase/i.test(h.trim())) { opened = true; break; }
  await toGateway();
  await altMac("g"); await page.waitForTimeout(300);
  await page.keyboard.type("Day Book"); await page.waitForTimeout(400); await press("Enter", 2000);
}
rec("a seeded Purchase opens for ALTERATION from the Day Book", opened);

if (opened) {
  const heading = await page.locator("h1").first().innerText().catch(() => "");
  rec("D41: it opens as an ALTERATION of its own class", /Purchase/i.test(heading), heading);

  const before = app.net.length;
  await press("Control+a", 5000);
  const wire = app.net.slice(before);
  const screenText = await body();
  const saved = wire.some((w) => /PUT .*\/vouchers\/purchase\/.* 200/.test(w));
  const refused = /Error|refus/i.test(screenText);
  rec(
    "D41: ⛔ re-accepting an unchanged alter does NOT mint a ledger",
    !wire.some((w) => /POST .*\/ledgers/.test(w)),
    wire.filter((w) => /ledgers/.test(w)).join(" · ") || "(no ledger writes)",
  );
  rec(
    "D41: and it either saves cleanly or refuses WITHOUT the gst_rate_pct error",
    saved || (refused && !/gst_rate_pct/i.test(screenText)),
    saved ? `saved: ${wire.filter((w) => /purchase/.test(w)).join(" · ")}` : (screenText.match(/(Error|refus)[^\n]{0,180}/) ?? [""])[0],
  );

  const after = await (async () => {
    const r = await fetch(`http://localhost:8087/auth/token`, {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ subject: "browser-dev-owner", amr: ["webauthn"] }),
    });
    const { token } = await r.json();
    const res = await fetch(`http://localhost:8087/v1/companies/${process.env.CO}/ledgers`, {
      headers: { Authorization: `Bearer ${token}` },
    });
    return (await res.json()).ledgers.length;
  })();
  rec(
    "D41: ⛔ the chart of accounts is the SAME SIZE after the alter",
    after === ledgersBefore,
    `${ledgersBefore} ledgers before, ${after} after`,
  );
}

console.log("\nNON-GET WIRE:", app.net.join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
