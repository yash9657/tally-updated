import { openApp } from "./audit-harness.mjs";
const app = await openApp({ company: process.env.CO, headless: true });
const { page, press, altMac, toGateway } = app;
await toGateway(); await altMac("g"); await page.waitForTimeout(300);
await page.keyboard.type("Stock Summary"); await page.waitForTimeout(400); await press("Enter", 1800);
await press("F2", 700);
console.log("=== BODY AFTER F2 ===");
console.log((await page.locator("body").innerText()).split("\n").slice(0, 30).join("\n"));
console.log("=== INPUTS ===");
for (const h of await page.locator("input").all()) {
  console.log(JSON.stringify({
    testid: await h.getAttribute("data-testid"), name: await h.getAttribute("name"),
    aria: await h.getAttribute("aria-label"), ph: await h.getAttribute("placeholder"),
    value: await h.inputValue().catch(() => null), focused: await h.evaluate((e) => e === document.activeElement),
  }));
}
await app.browser.close();
