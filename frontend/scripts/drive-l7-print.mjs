/**
 * L7 — PRINTING. Eleven rows, and the oracle is A PDF THAT NAMES ITSELF:
 * %PDF … %%EOF, uncompressed streams, and the document's OWN number and period
 * read back out of the bytes. A 200 with a content-type proves nothing about
 * what is on the paper.
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const app = await openApp({ company: process.env.CO, headless: true });
const { page, body, press, altMac, toGateway } = app;

/** Every print response, with its bytes and its Content-Disposition. */
/**
 * \u26d4 TWO THINGS THE FIRST RUN OF THIS FILE GOT WRONG, BOTH FATAL TO THE ORACLE.
 *
 *  1. IT RECORDED `new URL(r.url()).pathname` AND THREW THE QUERY STRING AWAY —
 *     and the query string IS D34. The whole defect was that the print target
 *     carried no from/to/as_on. A probe that drops it can only ever pass.
 *  2. `response.body()` COMES BACK EMPTY FOR THE PDF STREAMS (0 bytes on a 200
 *     that really is 4KB of paper; the 501's 225-byte JSON body reads fine). So
 *     the bytes are fetched AGAIN, server-side, from the EXACT url the browser
 *     asked for — which is the stronger oracle anyway: the browser proves what
 *     the client SENT, and the refetch proves what the paper SAYS.
 */
const pdfs = [];
page.on("response", (r) => {
  if (!/\/print\//.test(r.url())) return;
  const u = new URL(r.url());
  pdfs.push({
    url: u.pathname + u.search,          // \u26d4 SEARCH INCLUDED. See above.
    path: u.pathname,
    query: u.search,
    status: r.status(),
    type: r.headers()["content-type"] ?? "",
    disposition: r.headers()["content-disposition"] ?? "",
    text: "",
    bytes: 0,
  });
});

/** Re-fetch one print URL with a minted token and read the real bytes. */
const TOKEN = await (async () => {
  const r = await fetch("http://localhost:8087/auth/token", {
    method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject: process.env.E2E_SUBJECT ?? "browser-dev-owner", amr: ["webauthn"] }),
  });
  return (await r.json()).token;
})();
const hydrate = async (rec_) => {
  if (!rec_ || rec_.status !== 200) return rec_;
  const r = await fetch(`http://localhost:8087${rec_.url}`, { headers: { Authorization: `Bearer ${TOKEN}` } });
  const buf = Buffer.from(await r.arrayBuffer());
  rec_.text = buf.toString("latin1");
  rec_.bytes = buf.length;
  rec_.disposition = r.headers.get("content-disposition") ?? rec_.disposition;
  return rec_;
};
const lastPdf = async () => await hydrate(pdfs[pdfs.length - 1] ?? null);
/** The visible text of an uncompressed PDF: everything inside ( ) before Tj/TJ. */
const pdfText = (p) =>
  (p?.text.match(/\((?:\\.|[^\\)])*\)/g) ?? []).map((s) => s.slice(1, -1).replace(/\\([()\\])/g, "$1")).join(" ");

const goTo = async (name, wait = 1800) => {
  await toGateway(); await altMac("g"); await page.waitForTimeout(300);
  await page.keyboard.type(name); await page.waitForTimeout(400); await press("Enter", wait);
};
const status = () => page.getByTestId("status-line").innerText().catch(() => "");

// ======================================================== D34 — Ctrl+P and F2
await goTo("Trial Balance");
await press("F2", 700);
await page.getByTestId("period-from").fill("2026-04-01");
await page.getByTestId("period-to").fill("2026-06-30");
await press("Enter", 1800);
const screenText = await body();
rec(
  "Trial Balance is showing an F2-narrowed period",
  /2026-04-01/.test(screenText) || /2026-06-30/.test(screenText),
  (screenText.match(/.{0,60}(2026-0[46]|period).{0,60}/i) ?? ["<no period stated on screen>"])[0].replace(/\n/g, " | "),
)

await press("Control+p", 4000);
let p = await lastPdf();
rec("D34: Ctrl+P on a report asks the server for a PDF", p !== null && p.status === 200 && /pdf/.test(p.type), p ? `${p.url} -> ${p.status} ${p.type} ${p.bytes}B` : "NO print request was made");
if (p) {
  rec("D34: it is a whole PDF (%PDF … %%EOF)", p.text.startsWith("%PDF") && p.text.trimEnd().endsWith("%%EOF"));
  const t = pdfText(p);
  rec(
    "D34: ⛔ the PAPER states the F2 period, not 'all dates in the books'",
    // \u26a0 THE PAPER SPELLS A DATE `30-Jun-2026`, NOT `2026-06-30`. The first
    // run searched for the ISO form, found nothing, and called a working fix
    // broken. Print what you SAW, always.
    /30-Jun-2026/.test(t) && !/all dates in the books/i.test(t),
    (t.match(/As (at|on) [^)]{0,24}|all dates in the books/) ?? ["<no period on the paper>"])[0],
  );
  rec(
    "D34: \u26d4 and the CLIENT sent the dates — the query string is the defect",
    // \u26a0 A TRIAL BALANCE IS A POSITION and carries `as_on` ALONE, correctly.
    // Demanding from/to here was the probe misreading the report class; the
    // period half of D34 is driven on the Day Book below, where it applies.
    /as_on=2026-06-30/.test(p.query),
    `query="${p.query || "(EMPTY — this is D34 itself)"}"`,
  );
}

// ================================================= D75 — the download filename
await goTo("Day Book");
// \u26d4 THE PERIOD HALF OF D34: a Day Book is a RANGE, so this is the report
// whose paper and whose query must carry BOTH bounds.
await press("F2", 700);
await page.getByTestId("period-from").fill("2026-04-01");
await page.getByTestId("period-to").fill("2026-06-30");
await press("Enter", 1800);
await press("Control+p", 4000);
p = await lastPdf();
rec("the Day Book prints", p !== null && p.status === 200, p ? `${p.status} ${p.bytes}B` : "no request");
if (p) {
  rec(
    "D75: the filename carries no raw UUID",
    !/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/.test(p.disposition),
    p.disposition || "(no Content-Disposition)",
  );
  // ================================================================== D61
  rec(
    "D34: \u26d4 a PERIOD report's query carries BOTH bounds",
    /from=2026-04-01/.test(p.query) && /to=2026-06-30/.test(p.query),
    `query="${p.query || "(EMPTY — this is D34 itself)"}"`,
  );
  const t = pdfText(p);
  rec(
    "D34: \u26d4 and the Day Book's PAPER states that period",
    /01-Apr-2026/.test(t) && /30-Jun-2026/.test(t) && !/all dates in the books/i.test(t),
    (t.match(/(For|From)[^)]{0,44}|all dates in the books/) ?? ["<no period on the paper>"])[0],
  );
  // \u26d4 THE PROBE WAS PINNING THE DEFECT. It searched for "voucher
  // version(s)" — D61's own FALSE wording, which the fix removed, because this
  // report prints one line per VOUCHER at its current version and not one per
  // version. Searching for the broken text can only pass while the bug lives.
  const footer = (t.match(/voucher\\?\(s\\?\) in[\s\S]{0,420}/) ?? [""])[0];
  rec("D61: the Day Book footer is on the paper at all", footer !== "", footer.slice(0, 90).replace(/\s+/g, " ") || "<no footer>");
  rec(
    "D61: the count is of VOUCHERS, not voucher versions",
    footer !== "" && !/voucher\\?\(s\\?\) version/i.test(t) && !/voucher version/i.test(t),
    (t.match(/\d+ voucher\\?\(s\\?\)[^)]{0,20}/) ?? [""])[0],
  );
  rec(
    "D61: \u26d4 and the sentence RUNS TO ITS END rather than being clipped",
    /count the same money twice/.test(t),
    footer.slice(-120).replace(/\s+/g, " ") || "<nothing to judge>",
  );
}

// ============================== D35 — the TDS Register's advertised Ctrl+P
await goTo("TDS / TCS Register");
rec("TDS / TCS Register opens", /TDS/.test(await body()));
const beforeTds = pdfs.length;
await press("Control+p", 3000);
const tdsStatus = await status();
rec(
  "D35: Ctrl+P is NOT answered with the shell's generic 'does not apply'",
  !/does not apply/i.test(tdsStatus),
  tdsStatus.replace(/\n/g, " ").slice(0, 140),
);
rec(
  "D35: it reaches the server (or states the server's own refusal)",
  pdfs.length > beforeTds || /501|not implemented|cannot be printed/i.test(tdsStatus),
  `print requests=${pdfs.length - beforeTds} status="${tdsStatus.replace(/\n/g, " ").slice(0, 90)}"`,
);

// ============================== D76 — the Cost Centre Summary's print refusal
await app.openFromGateway("Cost Centre Summary");
rec("Cost Centre Summary opens", /Cost Centre/i.test(await body()));
await press("Control+p", 2500);
const ccStatus = await status();
rec(
  "D76: the refusal does NOT claim Alt+E is unbuilt here",
  !/Alt\+E is not built/i.test(ccStatus),
  ccStatus.replace(/\n/g, " ").slice(0, 160),
);

// ============================ D30 / D31 / D75 — the VOUCHER classes that print
//
// \u26d4 D30: the Delivery Note is one of the six classes printTarget declares
// printable and had NO Ctrl+P at all — the key fell through to the shell while
// the voucher bar on that very screen advertised it. D31: a Debit Note could
// NEVER be printed by this product, because printingdb read its against-number
// only from a column no HTTP request can set.
// \u26d4 FINDING A DELIVERY NOTE IN THE DAY BOOK IS NOT OBVIOUS, AND TWO RUNS OF
// THIS FILE GOT IT WRONG.
//
// The Day Book has NO class column — date, number, particulars, debit, credit —
// and the particulars cell shows the PARTY when the voucher has one and the
// narration when it does not. So the moment the seed gave its delivery notes
// their consignees (which this session did, to close D30's own 422) the rows
// stopped reading "Delivery note" and started reading "Acme Traders". Matching
// on a class name found nothing, twice.
//
// A stock-only voucher posts NOTHING to the ledger, so it is a row with 0.00 on
// both sides; the receipt notes among those still carry narrations that name
// themselves. Then the DRILL CONFIRMS the class by its heading, because a guess
// that lands on the wrong voucher must fail loudly rather than print something
// else and pass.
await toGateway();
await goTo("Day Book");
{
  const rows = page.locator('[role="grid"] [role="row"]');
  const n = await rows.count();
  const candidates = [];
  for (let i = 0; i < n; i++) {
    const txt = (await rows.nth(i).innerText()).replace(/\n/g, " | ");
    const zeros = (txt.match(/\b0\.00\b/g) ?? []).length;
    if (zeros < 2) continue;                                          // has a ledger side
    if (/receipt note|stock journal|physical/i.test(txt)) continue;    // names itself
    candidates.push([i, txt]);
  }
  let opened = null;
  for (const [i, txt] of candidates) {
    await rows.nth(i).click();
    await page.waitForTimeout(250);
    await press("Enter", 2500);
    if (/Delivery Note/i.test(await body())) { opened = txt; break; }
    await toGateway();
    await goTo("Day Book");
  }
  rec(
    "a seeded Delivery Note can be drilled from the Day Book",
    opened !== null,
    opened ? opened.slice(0, 74) : `${n} rows, ${candidates.length} zero-amount candidates, none was a Delivery Note`,
  );

  if (opened !== null) {
    const before = pdfs.length;
    await press("Control+p", 5000);
    const q = pdfs.length > before ? await hydrate(pdfs[pdfs.length - 1]) : null;
    rec(
      "D30: \u26d4 Ctrl+P on a Delivery Note REACHES the server — the key was DEAD here",
      q !== null,
      q ? `${q.path} -> ${q.status}` : "NO print request was made at all",
    );
    if (q) {
      rec(
        "D30: ...and a Delivery Note PDF comes back that names itself",
        q.status === 200 && q.text.startsWith("%PDF") && /%%EOF/.test(q.text),
        `${q.status} ${q.bytes}B`,
      );
      const t = pdfText(q);
      rec(
        "D30: the paper names its CONSIGNEE and has a signature line",
        /Acme Traders|Mumbai Metals/.test(t) && /Received by/.test(t),
        (t.match(/Acme Traders|Mumbai Metals/) ?? ["<no consignee on the paper>"])[0],
      );
      rec(
        "D75: the Delivery Note filename carries no raw UUID",
        !/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}/.test(q.disposition),
        q.disposition || "(none)",
      );
    }
  }
}

// ==================================================== D62 — screen vs paper
await goTo("Stock Summary");
const screenHeaders = await page.locator('[data-testid="stock-summary-table"] thead th').allInnerTexts();
const beforeSS = pdfs.length;
await press("Control+p", 4000);
const ss = pdfs.length > beforeSS ? await hydrate(pdfs[pdfs.length - 1]) : null;
rec("D62: the Stock Summary prints", ss !== null && ss.status === 200, ss ? `${ss.status} ${ss.bytes}B` : "no request");
if (ss) {
  const t = pdfText(ss);
  const paperHas = (h) => new RegExp(h.replace(/[()]/g, "."), "i").test(t);
  const missing = ["Opening", "Inward", "Outward", "Closing"].filter((h) => !paperHas(h));
  rec("D62: the paper carries all four movement columns", missing.length === 0, `missing: ${missing.join(", ") || "none"}`);
  rec(
    "D62: \u26d4 and the SCREEN is no longer a narrower report than the paper",
    screenHeaders.includes("Inward Value") && screenHeaders.includes("Opening Value"),
    `screen: ${screenHeaders.join(" / ")}`,
  );
}

// ==================================== D63 — Bank Rec: Ctrl+P as the FIRST key
await app.openFromGateway("Bank Reconciliation");
rec("Bank Reconciliation opens", /Bank Reconciliation|reconcil/i.test(await body()));
const beforeBR = pdfs.length;
await press("Control+p", 4000);
const br = pdfs.length > beforeBR ? await hydrate(pdfs[pdfs.length - 1]) : null;
const brStatus = await status();
// \u26d4 WHAT THE FIX ACTUALLY DOES, AND THE PROBE HAD TO BE CORRECTED TO MATCH.
// D63 reported that the refusal ("Select a bank account first") is unreachable
// because the screen AUTO-SELECTS when the company has one bank account — so
// Ctrl+P prints a statement for an account nobody chose. L7 did NOT remove the
// auto-select (on a one-bank company it is the right behaviour); it made the
// status line NAME the account that went to paper, and split the refusal so a
// company with NO bank accounts is not told to "select one first". So the oracle
// is the NAME, not the refusal.
rec(
  "D63: Ctrl+P prints for a bank account and the wire says which",
  br !== null && /ledger_id=/.test(br.query),
  br ? `query "${br.query}"` : "no print request",
);
rec(
  "D63: \u26d4 and the status line NAMES the account that went to paper",
  /HDFC/i.test(brStatus),
  brStatus.replace(/\n/g, " ").slice(0, 150) || "<status line said nothing>",
);

console.log("\nPRINT REQUESTS:", pdfs.map((x) => `${x.url} ${x.status} ${x.bytes}B`).join("\n  ") || "(none)");
await app.browser.close();
process.exit(summary() > 0 ? 1 : 0);
