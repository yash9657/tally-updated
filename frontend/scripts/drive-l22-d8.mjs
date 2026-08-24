/**
 * L22 — D8: closing stock was replayed in LEXICAL voucher_no order, so an issue
 * keyed after the receipt that funds it was costed at the PRE-receipt average.
 *
 * ⛔⛔ THIS ROW MOVES THE BALANCE SHEET, so the drive reads the figure a person reads
 * — in a browser — and FOOTS it against a handler that is not the one that produced
 * it. `/stock-summary` and `/balance-sheet` are two handlers over one replay; the
 * Balance Sheet's Closing Stock node must equal the Stock Summary's total, and the
 * browser's rendering of both must equal the wire. Reading the stock summary against
 * itself would prove only that it agrees with itself, and this is precisely a defect
 * where it could agree and be wrong (§2.2, §5.19).
 *
 * ⛔ IT OPENS WITH A PRECONDITION ARM BECAUSE THE ORACLE IS OTHERWISE VACUOUS (§5.5).
 * The defect needs two differently-numbered vouchers touching ONE position on ONE
 * date, whose NUMBER order is not their ENTRY order. Nothing in this seed had such a
 * pair until D8's lane added one, so on a stale seed every figure below is simply the
 * only figure there is, and the whole probe passes while proving nothing. The arm
 * reads the pair off the wire and asserts the two orders DISAGREE.
 *
 * ⚠️ AND THE FIGURES ARE PINNED, NOT DERIVED FROM THE SCREEN. They were predicted by
 * replaying the seed's own movements independently in Python before the fix was built
 * — that replay reproduced the PRE-fix numbers exactly, which is what licensed it —
 * and then observed on the wire against a binary verified fresh by symbol:
 *   MS Angle 50x50   closing 8500.8152 -> 8419.9091   outward 2359.1848 -> 2440.0909
 *   Closing Stock   75175.8152 -> 75094.9091          closing QTY unchanged 205.0000
 * ⛔ The quantity is the control: D8 moves VALUE and no goods. A probe that checked
 * only that the numbers changed could not tell this from a units bug.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l22-d8.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://localhost:8087";
const CO = process.env.CO;
if (!CO) throw new Error("CO is required — the demo company id from /tmp/drive-company-id");

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
const TOK = (await j("POST", "/auth/token", {
  body: { subject: "browser-dev-owner", amr: ["webauthn"] },
})).body.token;
const api = (method, path, body) => j(method, path, { token: TOK, body });

const ITEM = "MS Angle 50x50";
const PAIR_DATE = "2026-07-15";

// The figures ENTRY order gives. Pinned; see the header for where they come from.
const WANT = {
  closingQty: "205.0000",
  closingValue: "8419.9091",
  closingRate: "41.0727",
  outwardQty: "75.0000",
  outwardValue: "2440.0909",
  totalValue: "75094.9091",
};
// What LEXICAL voucher_no order gave — asserted ABSENT, so a revert is loud.
const DEFECT = { closingValue: "8500.8152", outwardValue: "2359.1848", totalValue: "75175.8152" };

/**
 * ⛔ THE SCREEN DOES NOT RENDER THE WIRE'S STRING, AND THE FIRST RUN OF THIS PROBE
 * FAILED ON EXACTLY THAT. The routes answer fixed-4 (`8419.9091`); the screen renders
 * Indian-grouped, 2 dp, TRUNCATED (`8,419.90` — not `8,419.91`). Both the wanted and
 * the defect figure were absent from the page text and all three browser arms went
 * red while every wire arm was green: a probe comparing a projection the DOM does not
 * carry (§5.7). These are MEASURED off the rendered page, not derived by re-formatting
 * here — a second copy of the formatter would drift and, worse, would agree with
 * itself.
 */
const SHOWN = {
  closingValue: "8,419.90",
  outwardValue: "2,440.09",
  closingRate: "41.07",
  totalValue: "75,094.90",
};
/**
 * ⚠️ BOTH PLAUSIBLE RENDERINGS OF THE DEFECT ARE ASSERTED ABSENT. The formatter
 * truncates today, so a reverted `8500.8152` would read `8,500.81` — but a probe that
 * pinned only that would go quiet if the rounding mode ever changed, and the defect
 * would come back invisibly. Name both.
 */
const DEFECT_SHOWN = {
  closingValue: ["8,500.81", "8,500.82"],
  outwardValue: ["2,359.18", "2,359.19"],
  totalValue: ["75,175.81", "75,175.82"],
};

// ===========================================================================
// ⛔ PRECONDITION — the fixture the whole oracle depends on is actually there.
// ===========================================================================
const day = await api("GET", `/v1/companies/${CO}/day-book?from=${PAIR_DATE}&to=${PAIR_DATE}`);
const onDate = (day.body.entries ?? []).filter(
  (e) => (e.op ?? "").toUpperCase() !== "DELETE" &&
         (e.postings ?? []).length >= 0 &&
         /Receipt Note|Delivery Note/i.test(e.voucher_type ?? e.type ?? e.class ?? ""),
);
// The day book does not carry inventory lines, so identify the pair by TYPE + date
// and read the ordering keys straight out of the two vouchers it names.
const pair = await Promise.all(
  onDate.map(async (e) => {
    const v = await api("GET", `/v1/companies/${CO}/vouchers/${e.voucher_id}`);
    return { no: v.body.voucher_no ?? "", id: e.voucher_id, type: v.body.voucher_type ?? v.body.class ?? "" };
  }),
);
{
  const byNo = [...pair].sort((a, b) => (a.no < b.no ? -1 : a.no > b.no ? 1 : 0)).map((p) => p.no);
  const byID = [...pair].sort((a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0)).map((p) => p.no);
  rec("D8: ⛔ PRECONDITION — the seed carries a same-date pair whose NUMBER order is NOT its ENTRY order",
      pair.length === 2 && byNo.join(",") !== byID.join(","),
      pair.length === 2
        ? `on ${PAIR_DATE}: by voucher_no [${byNo}] · by voucher_id (entry) [${byID}] — ` +
          (byNo.join(",") === byID.join(",")
            ? "THEY AGREE, so every figure below is vacuous — reseed"
            : "they disagree, so the ordering key is load-bearing here")
        : `found ${pair.length} stock voucher(s) on ${PAIR_DATE}, need 2 — reseed`);
}

const app = await openApp({ company: CO, headless: true });
const { page, body, press, altMac, toGateway } = app;

const heading = async () =>
  (await page.locator("h1").first().innerText().catch(() => "")).trim();
/** Walk to a report by NAME through Alt+G, with nothing but keys. */
const gotoReport = async (typed) => {
  await toGateway();
  await altMac("g", 1200);
  await page.keyboard.type(typed);
  await page.waitForTimeout(800);
  await press("Enter", 5000);
};

try {
  // =========================================================================
  // THE WIRE, read once, as the thing the SCREEN is checked against.
  // =========================================================================
  const ss = await api("GET", `/v1/companies/${CO}/stock-summary`);
  const row = (ss.body.rows ?? []).find((r) => r.item === ITEM);
  rec("D8: the stock-summary route answers and carries the item under test",
      ss.status === 200 && row !== undefined,
      ss.status === 200 ? `rows=${(ss.body.rows ?? []).length}, ${ITEM} ${row ? "present" : "ABSENT"}`
                        : `HTTP ${ss.status}`);

  if (row) {
    // ⛔ THE CONTROL FIRST: no goods moved differently. If the quantity moved, this
    // is not D8 and every value assertion below is measuring something else.
    rec("D8: ⛔ CONTROL — the closing QUANTITY is untouched (D8 moves value, not units)",
        row.closing_qty === WANT.closingQty && row.outward_qty === WANT.outwardQty,
        `closing_qty=${row.closing_qty} (want ${WANT.closingQty}) · outward_qty=${row.outward_qty}`);

    rec("D8: ⛔⛔ the issue is costed at the POST-receipt average, on the wire",
        row.outward_value === WANT.outwardValue,
        `outward_value=${row.outward_value} · want ${WANT.outwardValue} · the defect gave ${DEFECT.outwardValue}`);
    rec("D8: ⛔⛔ …so the item's closing VALUE is the entry-order figure",
        row.closing_value === WANT.closingValue && row.closing_rate === WANT.closingRate,
        `closing_value=${row.closing_value} (want ${WANT.closingValue}, defect ${DEFECT.closingValue}) · ` +
        `closing_rate=${row.closing_rate} (want ${WANT.closingRate})`);
    // ⛔ AND THE DEFECT'S OWN NUMBER IS ASSERTED ABSENT, so a revert cannot read as
    // "some other figure" — it reads as the defect, by name.
    rec("D8: ⛔ the pre-fix figure is GONE from the wire, by value",
        row.closing_value !== DEFECT.closingValue && row.outward_value !== DEFECT.outwardValue,
        `closing_value=${row.closing_value} vs the defect's ${DEFECT.closingValue}`);
  }

  // =========================================================================
  // ⛔ FOOTED AGAINST A DIFFERENT HANDLER: the BALANCE SHEET. Closing stock reaches
  // the accounts through exactly one term, and this row moves it.
  // =========================================================================
  const bs = await api("GET", `/v1/companies/${CO}/balance-sheet`);
  const closingStockNodes = [];
  (function walk(n) {
    if (Array.isArray(n)) return n.forEach(walk);
    if (n && typeof n === "object") {
      if (n.name === "Closing Stock") closingStockNodes.push(String(n.amount ?? n.value ?? ""));
      Object.values(n).forEach(walk);
    }
  })(bs.body);
  rec("D8: ⛔ the BALANCE SHEET's Closing Stock equals the stock summary's total — two handlers, one replay",
      bs.status === 200 && closingStockNodes.length === 1 &&
        closingStockNodes[0] === WANT.totalValue &&
        String(ss.body.total_value) === WANT.totalValue,
      `BS Closing Stock=[${closingStockNodes}] · stock-summary total_value=${ss.body.total_value} · ` +
      `want ${WANT.totalValue} · the defect gave ${DEFECT.totalValue}`);
  rec("D8: ⛔ and the sheet still BALANCES after the re-pricing",
      bs.body.balanced === true,
      `balanced=${bs.body.balanced}`);

  // =========================================================================
  // THE BROWSER — the figure a person actually reads.
  // =========================================================================
  await gotoReport("Stock Summary");
  const ssHead = await heading();
  const ssText = (await body()).replace(/\s+/g, " ");
  rec("D8: the Stock Summary opens from the keyboard",
      /Stock Summary/i.test(ssHead) || /Stock Summary/i.test(ssText),
      `h1="${ssHead}"`);
  const absent = (text, list) => list.every((v) => !text.includes(v));
  rec("D8: ⛔⛔ THE BROWSER shows the entry-order closing value and cost of issue, not the defect's",
      ssText.includes(SHOWN.closingValue) && ssText.includes(SHOWN.outwardValue) &&
        ssText.includes(SHOWN.closingRate) &&
        absent(ssText, DEFECT_SHOWN.closingValue) && absent(ssText, DEFECT_SHOWN.outwardValue),
      `on screen: closing ${SHOWN.closingValue}=${ssText.includes(SHOWN.closingValue)} · ` +
      `outward ${SHOWN.outwardValue}=${ssText.includes(SHOWN.outwardValue)} · ` +
      `rate ${SHOWN.closingRate}=${ssText.includes(SHOWN.closingRate)} · ` +
      `defect [${DEFECT_SHOWN.closingValue}] absent=${absent(ssText, DEFECT_SHOWN.closingValue)}`);
  rec("D8: ⛔ …and the browser's Stock-in-Hand total agrees with both routes",
      ssText.includes(SHOWN.totalValue) && absent(ssText, DEFECT_SHOWN.totalValue),
      `on screen: total ${SHOWN.totalValue}=${ssText.includes(SHOWN.totalValue)} · ` +
      `defect [${DEFECT_SHOWN.totalValue}] absent=${absent(ssText, DEFECT_SHOWN.totalValue)}`);

  await gotoReport("Balance Sheet");
  const bsHead = await heading();
  const bsText = (await body()).replace(/\s+/g, " ");
  rec("D8: the Balance Sheet opens from the keyboard",
      /Balance Sheet/i.test(bsHead) || /Balance Sheet/i.test(bsText),
      `h1="${bsHead}"`);
  rec("D8: ⛔⛔ THE BROWSER's Balance Sheet carries the re-priced closing stock",
      bsText.includes(SHOWN.totalValue) && absent(bsText, DEFECT_SHOWN.totalValue),
      `on screen: ${SHOWN.totalValue}=${bsText.includes(SHOWN.totalValue)} · ` +
      `defect [${DEFECT_SHOWN.totalValue}] absent=${absent(bsText, DEFECT_SHOWN.totalValue)}`);

  rec("D8: the screens are not a crash or a stuck spinner",
      !/\bNaN\b/.test(ssText + bsText) && !/Loading…$/.test(bsText),
      "no NaN / spinner-stuck state");
} finally {
  console.log("\nNON-GET WIRE:", app.net.filter((w) => !/webauthn/.test(w) && !w.startsWith("GET ")).join(" · ") || "(none — this probe READS ONLY)");
  await app.browser?.close?.();
}

process.exit(summary() > 0 ? 1 : 0);
