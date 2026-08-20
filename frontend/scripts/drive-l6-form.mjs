/**
 * L6 — THE VOUCHER FORM. D59 (the printed money voucher named the BANK as the
 * other party), D60 (a Sales invoice saved without an HSN and then could not be
 * printed), D39/D41 (a refused Ctrl+A left masters behind).
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, toGateway } = app;
const status = () => page.getByTestId("status-line").innerText().catch(() => "");

const pdfs = [];
page.on("response", (r) => {
  if (!/\/print\//.test(r.url())) return;
  const u = new URL(r.url());
  pdfs.push({ path: u.pathname, query: u.search, status: r.status(), disposition: r.headers()["content-disposition"] ?? "" });
});
const TOKEN = await (async () => {
  const r = await fetch("http://localhost:8087/auth/token", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject: "browser-dev-owner", amr: ["webauthn"] }),
  });
  return (await r.json()).token;
})();
const paperOf = async (rec_) => {
  const r = await fetch(`http://localhost:8087${rec_.path}${rec_.query}`, { headers: { Authorization: `Bearer ${TOKEN}` } });
  const raw = Buffer.from(await r.arrayBuffer()).toString("latin1");
  return [...raw.matchAll(/\((?:\\.|[^\\)])*\)/g)].map((m) => m[0].slice(1, -1)).join(" ");
};
/** A ledger field is a COMBOBOX: type enough to be unambiguous, then Enter. Never ArrowDown. */
const pickLedger = async (locator, name) => {
  await locator.click();
  await page.keyboard.type(name);
  await page.waitForTimeout(500);
  const top = await page.locator('[role="option"]').first().innerText().catch(() => "");
  await press("Enter", 400);
  return top.trim();
};

// ===================================================== D59 — a KEYED payment
await toGateway();
await press("F5", 1200);
rec("F5 opens a Payment", /Payment/.test(await body()));
const acct = await pickLedger(page.getByLabel("Account").first(), "HDFC Bank");
rec("the Account is the BANK the money left", acct === "HDFC Bank", `top option "${acct}"`);
const leg = await pickLedger(page.getByTestId("single-ledger:0"), "Office Rent");
await page.getByTestId("single-amount:0").fill("1750");
await page.waitForTimeout(300);
await press("Control+a", 5000);
const savedNo = await page.getByTestId("voucher-no").innerText().catch(() => "");
rec("the Payment saves through the passkey", /\d/.test(savedNo), `${savedNo.replace(/\n/g, " ")} | leg top option "${leg}"`);

const beforeP = pdfs.length;
await press("Control+p", 5000);
const pv = pdfs.length > beforeP ? pdfs[pdfs.length - 1] : null;
rec("D59: Ctrl+P on the payment just keyed reaches the print route", pv !== null && pv.status === 200, pv ? `${pv.path} -> ${pv.status}` : "no print request");
if (pv && pv.status === 200) {
  const t = await paperOf(pv);
  const line = (t.match(/Paid To[^)]{0,60}/) ?? ["<no Paid To line>"])[0];
  rec(
    "D59: ⛔ the paper does NOT name the BANK as the other party",
    !/Paid To\s*:?\s*(HDFC|ICICI|Bank|Cash)/i.test(t),
    line,
  );
  rec(
    "D59: it names what the money was paid for/to",
    /Office Rent/i.test(t),
    (t.match(/.{0,30}Office Rent.{0,30}/) ?? ["<Office Rent absent from the paper>"])[0],
  );
}

// ===================================================== D60 — HSN before saving
await toGateway();
await press("F8", 1400);
rec("F8 opens a Sales voucher", /Sales/.test(await body()));
const party = await pickLedger(page.getByLabel("Party", { exact: false }).first(), "Acme Traders");
rec("the party is keyed", party !== "", `top option "${party}"`);
// Fill the GST panel WITHOUT an HSN.
const setIf = async (testid, value) => {
  const el = page.getByTestId(testid);
  if ((await el.count()) === 0) return false;
  await el.fill(value); await page.waitForTimeout(150); return true;
};
await setIf("gst-taxable", "10000");
await setIf("gst-rate", "18");
const leg2 = await pickLedger(page.getByTestId("single-ledger:0"), "Domestic Sales").catch(() => "");
await setIf("single-amount:0", "11800");
await page.waitForTimeout(300);

const beforeS = app.net.length;
await press("Control+a", 4000);
const wireS = app.net.slice(beforeS);
const screenS = await body();
const refusalS = (screenS.match(/(Error|refused)[^\n]{0,300}/) ?? [""])[0] || (await status());
rec(
  "D60: ⛔ a Sales invoice with NO HSN is refused BY THE FORM, before the wire",
  !wireS.some((w) => /POST .*\/vouchers\/sales/.test(w)),
  `wire: ${wireS.filter((w) => /vouchers|ledgers/.test(w)).join(" · ") || "(no voucher or ledger writes)"}`,
);
rec(
  "D60: and the refusal cites the rule and says printing would fail",
  /HSN/i.test(refusalS) && /46\(g\)|printed|print/i.test(refusalS),
  refusalS.replace(/\n/g, " ").slice(0, 200) || "<nothing on screen>",
);
rec(
  "D39/D41: ⛔ the refused Ctrl+A minted NO ledger",
  !wireS.some((w) => /POST .*\/ledgers/.test(w)),
  wireS.filter((w) => /ledgers/.test(w)).join(" · ") || "(no ledger writes)",
);

console.log("\nNON-GET WIRE:", app.net.join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
