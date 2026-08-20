/** D53 — a refusal may not name a level the operator does not hold. */
import { openApp, rec, summary } from "./audit-harness.mjs";
const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, altMac, toGateway } = app;

await toGateway(); await altMac("g"); await page.waitForTimeout(300);
await page.keyboard.type("Stock Summary"); await page.waitForTimeout(400); await press("Enter", 2200);

const txt = await body();
rec("the report is REFUSED to this operator", /refus|denied|not permitted|permission/i.test(txt));
rec(
  "D53: the refusal does NOT name 'Data Entry' as the denying level",
  !/Data Entry/.test(txt),
  (txt.match(/.{0,70}Data Entry.{0,70}/) ?? ["(absent — good)"])[0].replace(/\n/g, " "),
);
const advice = await page.getByTestId("stock-summary-denied-advice").innerText().catch(() => "<no advice element>");
rec("D53: the advice is derived from the server's answer", /rule on your own security level/i.test(advice), advice.replace(/\n/g, " "));
rec("D53: the server's own words are quoted", /The server’s own words|The server's own words/.test(txt), (txt.match(/server.{0,10}own words.{0,90}/) ?? [""])[0].replace(/\n/g, " "));
rec("D53: it still says WHY the report is restricted at all", /cost price/i.test(txt));

// ⛔ RULE 4 — a refusal must not have WRITTEN. And it must not leak the figures.
rec("the refused screen shows no stock figures", !/72,837\.15|6,162\.15|66,675\.00/.test(txt), "no cost values on the page");

console.log("\nNON-GET WIRE:", app.net.join(" · ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
