/**
 * §7.3 B — THE SEVENTH DOOR ON THE DRAFT ACCEPT, DRIVEN IN A REAL BROWSER.
 *
 * The claim -> post -> confirm reorder closes the race it was built for. The
 * CONFIRM did not get the same care: `recordAccepted` was fire-and-forget with a
 * `console.warn`, so a confirm that died after the voucher landed left insightd's
 * draft `ready` — LAYER 4, the only layer not confined to one browser profile,
 * with no record of an acceptance that had already moved money — while the
 * operator was told "Saved". The claim's TTL then became the duplicate's timer.
 *
 * ⛔ THE FAILURE IS INJECTED AT THE NETWORK LAYER, not by a stub, because that is
 * exactly how it was reported: `page.route(...).abort()` on the confirm POST kills
 * the request the way a dropped connection does, AFTER the voucher POST has
 * already succeeded. A stubbed port would prove the stub.
 *
 * ⛔ ARM 5 IS THE ONE THAT MATTERS AND IT WAS ADDED SECOND. Arms 1-4 all run in the
 * browser that POSTED, so every one of them would stay green against a build whose
 * server knew nothing at all: the memo, the durable memo and the queue are in this
 * profile. §7.3 B was never about that browser. Arm 5 opens a SECOND browser
 * context — no localStorage — against a NUMBERLESS bill (so the day book, the only
 * client layer that is not per-profile, has nothing to match on) with the claim
 * lease waited out, and asserts on the WIRE that cored answered the voucher POST
 * with 409 `draft_already_accepted`. That refusal cannot be manufactured by any
 * client layer, which is what makes the arm decisive.
 *
 * ⛔ RUN IT AGAINST BOTH VERSIONS. Against `git show HEAD:` of `draftAccept.ts` the
 * queue arms must FAIL — that is the defect reproduced — and the draft must stay
 * `ready` for ever no matter how many times the inbox is reopened. Against a cored
 * WITHOUT migration 0077 and its `draft_id` wire field, arm 5's POST is a 201 and
 * the tenant ends up with TWO purchases from one supplier bill.
 *
 * ⚠️ SYMBOL-CHECK THE cored BINARY BEFORE BELIEVING ARM 5. A stale `go run` process
 * is indistinguishable from a fix that does not work:
 *   strings "$(ps -o command= -p "$(lsof -nP -iTCP:8087 -sTCP:LISTEN -t)" | awk '{print $1}')" \
 *     | grep -c draft_already_accepted     # 0 means you are driving yesterday's server
 *
 * ⚠️ ARM 5 WAITS OUT insightd's 120-SECOND CLAIM LEASE, so the whole probe takes a
 * few minutes. That wait is load-bearing: without it the second browser is refused
 * by the RESERVATION, which is a different door, and the arm would be green for the
 * wrong reason.
 *
 * ⚠️ IT POSTS TWO REAL PURCHASES INTO THE DEMO TENANT, in the shape
 * drive-l21-d85.mjs established: every ledger id is read out of this tenant's own
 * chart so NO master is created, and the bill is dated on or before today.
 * ⛔ Re-run `pnpm verify:demo` after this probe.
 *
 * RUN:  CO="$(cat frontend/.demo-company-id)" node frontend/scripts/drive-7-3-b-confirm.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://localhost:8087";
const CO = process.env.CO;
if (!CO) throw new Error('CO is required — use "$(cat frontend/.demo-company-id)"');

/** The localStorage key the pending-confirm queue owns. Asserted, not assumed. */
const QUEUE_KEY = "tally.vouchers.pendingDraftConfirms.v1";

const TOKEN = await (async () => {
  const r = await fetch(`${CORED}/auth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ subject: "browser-dev-owner", amr: ["webauthn"] }),
  });
  return (await r.json()).token;
})();

const cored = async (path, init = {}) => {
  const r = await fetch(`${CORED}/v1/companies/${CO}${path}`, {
    ...init,
    headers: {
      ...(init.body ? { "Content-Type": "application/json" } : {}),
      Authorization: `Bearer ${TOKEN}`,
      ...(init.headers ?? {}),
    },
  });
  const text = await r.text();
  let body;
  try { body = text ? JSON.parse(text) : {}; } catch { body = { raw: text }; }
  return { status: r.status, body, text };
};

const ledgerIndex = await (async () => {
  const r = await cored("/ledgers");
  if (r.status !== 200) throw new Error(`cannot read this tenant's ledgers: HTTP ${r.status}`);
  return new Map((r.body.ledgers ?? []).map((l) => [l.name, l]));
})();
const needLedger = (name) => {
  const l = ledgerIndex.get(name);
  if (!l) throw new Error(`this tenant has no "${name}" ledger — reseed before driving`);
  return l.id;
};
const STEEL = needLedger("Steel Suppliers");
const PURCHASES = needLedger("Raw Material Purchases");

/**
 * TWO free August dates on or before today, so neither bill collides with an
 * existing one — and, just as importantly, so the two bills this probe posts
 * cannot be confused for each other by a date-scoped day-book read.
 */
const [INVOICE_DATE, NUMBERLESS_DATE] = await (async () => {
  const day = await cored("/day-book?from=2026-08-01&to=2026-08-31");
  if (day.status !== 200) throw new Error(`day book: HTTP ${day.status} ${day.text}`);
  const taken = new Set(
    (day.body.entries ?? [])
      .filter((e) => e.party === "Steel Suppliers" && e.class === "Purchase")
      .map((e) => e.date),
  );
  const today = new Date().toISOString().slice(0, 10);
  const free = [];
  for (let d = 28; d >= 1; d--) {
    const date = `2026-08-${String(d).padStart(2, "0")}`;
    if (date > today) continue;
    if (!taken.has(date)) free.push(date);
    if (free.length === 2) return free;
  }
  throw new Error("need TWO free August dates on or before today for Steel Suppliers — reseed");
})();

/** A unique invoice number, so layers 1-3 cannot answer for layer 4. */
const INVOICE_NO = `B73/${Date.now().toString().slice(-8)}`;
console.log(
  `\n── tenant ${CO} · bill ${INVOICE_NO} dated ${INVOICE_DATE}` +
  ` · numberless bill dated ${NUMBERLESS_DATE} ──`,
);

const ocrLine = (text, y, conf) => ({ text, bbox: [40, y, 1200, y + 22], confidence: conf });
/**
 * ⛔ `no: ""` IS NOT A DEGENERATE CASE, IT IS THE CASE ARM 5 EXISTS FOR. The
 * day-book layer returns early for a draft with no supplier invoice number
 * (draftAccept.ts's assertNotAlreadyOnTheBooks: `if (no === "" || date === "")
 * return`), so a numberless bill is the only shape in which the fresh browser
 * reaches the server-side door instead of being stopped by a layer that lives in
 * neither browser. A numbered bill would make that arm pass without ever
 * exercising it — the same false green the pinned unit test was written around.
 */
const fixture = ({ no, date, taxable }) => ({
  classify: { doc_type: "purchase_invoice", is_purchase: true, supplier_ledger_id: STEEL },
  input: {
    pages: [{ png_b64: "", width: 1240, height: 1754, dpi: 200 }],
    ocr: {
      lines: [
        ocrLine("TAX INVOICE", 10, 0.99),
        ...(no ? [ocrLine(`Invoice No: ${no}`, 40, 0.98)] : []),
        ocrLine(`Date: ${date}`, 70, 0.98),
        ocrLine("Supplier: Steel Suppliers", 100, 0.97),
        ocrLine("Place of Supply: 29", 130, 0.97),
        ocrLine(`MS Angle HSN 7308 qty 10.0000 rate ${taxable} ${taxable} @ 18.0000%`, 170, 0.96),
      ],
      mean_confidence: 0.968,
    },
    inferred: {
      invoice_no: no,
      invoice_date: date,
      place_of_supply_state_code: "29",
      reverse_charge: false,
      exempt: false,
      line_items: [{
        description: "MS Angle for site",
        hsn: "7308",
        qty: "10.0000",
        rate: (Number(taxable) / 10).toFixed(4),
        taxable_value: Number(taxable).toFixed(4),
        gst_rate_pct: "18.0000",
        ledger_id: PURCHASES,
        ledger_name: "Raw Material Purchases",
        ledger_confidence: 0.95,
      }],
      totals: { taxable: Number(taxable).toFixed(4), invoice_total: (Number(taxable) * 1.18).toFixed(4) },
    },
  },
});

const listDrafts = async () => {
  const r = await cored("/drafts");
  if (r.status !== 200) throw new Error(`cannot list drafts through cored: HTTP ${r.status}`);
  return r.body;
};
const draftRow = async (id) => (await listDrafts()).find((d) => d.draft_id === id);

const app = await openApp({ company: CO, headless: true });
const { page, body, press, toGateway, openFromGateway } = app;

const status = async () =>
  (await page.locator('[role="status"]').first().innerText().catch(() => ""))
    .replace(/\s+/g, " ")
    .trim();
const queue = async () => {
  const raw = await page.evaluate((k) => globalThis.localStorage.getItem(k), QUEUE_KEY);
  if (!raw) return [];
  try { return Object.values(JSON.parse(raw)); } catch { return []; }
};
/** Any refused draft POST, with its body — an unexplained refusal must not be noise. */
const refused = [];
page.on("response", async (r) => {
  if (r.request().method() !== "POST") return;
  if (!/\/drafts$/.test(new URL(r.url()).pathname)) return;
  refused.push({ status: r.status(), body: (await r.text().catch(() => "")).slice(0, 400) });
});
const openInbox = async () => {
  await toGateway();
  await openFromGateway("Bill Inbox", 3000);
};

try {
  // ── 1. UPLOAD ONE BILL through the inbox, exactly as an operator would. ────
  const before = new Set((await listDrafts()).map((d) => d.draft_id));
  await openInbox();
  await page.getByTestId("inbox-fixture").fill(
    JSON.stringify(fixture({ no: INVOICE_NO, date: INVOICE_DATE, taxable: "4000.0000" })),
  );
  await page.waitForTimeout(300);
  await page.getByTestId("inbox-create").click();
  await page.waitForTimeout(5000);
  const after = await listDrafts();
  const fresh = after.filter((d) => !before.has(d.draft_id));
  if (fresh.length !== 1) {
    throw new Error(
      `expected exactly ONE new draft, got ${fresh.length}. ` +
      `POST /drafts responses: ${JSON.stringify(refused)} · ` +
      `inbox notice: ${(await status()) || "(none)"} · ` +
      `screen: ${(await body()).replace(/\s+/g, " ").slice(0, 300)}`,
    );
  }
  const DRAFT = fresh[0].draft_id;
  const rowIndex = after.findIndex((d) => d.draft_id === DRAFT);
  // ⚠️ NOT `=== "ready"`. This fixture lands as `low_confidence_manual` (the
  // classifier is not confident about a synthetic bill), and that is a PERFECTLY
  // ACCEPTABLE starting state — the inbox offers it and Ctrl+A posts it. What the
  // probe needs is that it is NOT ALREADY TERMINAL, because every arm below is
  // about a draft moving to `accepted`; asserting a specific non-terminal name
  // would fail on a fixture detail and say nothing about §7.3 B.
  rec("setup: one fresh draft is offered and NOT already accepted",
      fresh[0].status !== "accepted", `${DRAFT} status=${fresh[0].status}`);

  // ── 2. KILL THE CONFIRM AT THE NETWORK LAYER, then accept. ────────────────
  // ⛔ The voucher POST is untouched: only `POST .../drafts/{id}/accepted` dies,
  // which is precisely "the money moved and the confirm did not".
  let confirmsAborted = 0;
  await page.route("**/drafts/*/accepted", (route) => {
    confirmsAborted += 1;
    return route.abort("failed");
  });

  await openInbox();
  await page.locator('[role="option"]').nth(rowIndex).click();
  await page.waitForTimeout(3500);
  await press("Control+a", 8000);
  const savedText = await status();

  const dayBook = await cored(`/day-book?from=${INVOICE_DATE}&to=${INVOICE_DATE}`);
  const posted = (dayBook.body.entries ?? []).filter(
    (e) => e.class === "Purchase" && (e.voucher_no || "") === INVOICE_NO &&
           (e.op || "").toUpperCase() !== "DELETE",
  );
  rec("the VOUCHER landed — the money moved", posted.length === 1,
      `${posted.length} purchase(s) numbered ${INVOICE_NO}`);
  rec("the operator was told it saved", /saved/i.test(savedText), savedText.slice(0, 90));
  rec("the confirm really was killed on the wire", confirmsAborted >= 1,
      `${confirmsAborted} aborted`);

  // The defect state, on the server: layer 4 has no record of the acceptance.
  const stillOffered = await draftRow(DRAFT);
  rec("insightd's draft is NOT accepted — layer 4 lost the acceptance",
      stillOffered?.status !== "accepted", `status=${stillOffered?.status}`);
  rec("…and it names no voucher, so the inbox still invites a re-post",
      !stillOffered?.accepted_voucher_no,
      `accepted_voucher_no=${stillOffered?.accepted_voucher_no}`);

  // ⛔ THE FIX: the confirm is written down as OWED, so something can go back for
  // it. At HEAD this array is EMPTY and the acceptance is durably recorded nowhere
  // outside this browser's own memo.
  const owed = await queue();
  rec("⛔ the confirm is OWED in durable storage, naming the draft",
      owed.length === 1 && owed[0].draftId === DRAFT,
      JSON.stringify(owed).slice(0, 160));
  rec("…and it names the voucher that already exists",
      owed[0]?.voucherNo === INVOICE_NO || (owed[0]?.voucherID ?? "") !== "",
      `voucherNo=${owed[0]?.voucherNo} voucherID=${owed[0]?.voucherID}`);

  // ── 3. THE NETWORK COMES BACK, AND THE INBOX SETTLES THE DEBT. ────────────
  await page.unroute("**/drafts/*/accepted");
  await openInbox();
  await page.waitForTimeout(2500);

  const settled = await draftRow(DRAFT);
  rec("⛔ reopening the Bill Inbox lands the owed confirm — layer 4 is RESTORED",
      settled?.status === "accepted", `status=${settled?.status}`);
  rec("…and the draft now names the voucher it became",
      (settled?.accepted_voucher_no || "") === INVOICE_NO,
      `accepted_voucher_no=${settled?.accepted_voucher_no}`);
  // ⚠️ GATED ON THE DEBT HAVING EXISTED. "the queue is empty" passes VACUOUSLY
  // against a build that never queues anything — measured: at HEAD this arm was
  // green while the three arms around it were red.
  rec("nothing is owed any more (and something WAS owed)",
      owed.length === 1 && (await queue()).length === 0,
      `owed before=${owed.length} after=${(await queue()).length}`);

  // ── 4. AND THE DOOR IS SHUT: the bill cannot be accepted a second time. ────
  const listNow = await listDrafts();
  const idxNow = listNow.findIndex((d) => d.draft_id === DRAFT);
  await openInbox();
  await page.locator('[role="option"]').nth(idxNow).click();
  await page.waitForTimeout(3500);
  await press("Control+a", 8000);
  const second = await status();
  const dayBook2 = await cored(`/day-book?from=${INVOICE_DATE}&to=${INVOICE_DATE}`);
  const postedNow = (dayBook2.body.entries ?? []).filter(
    (e) => e.class === "Purchase" && (e.voucher_no || "") === INVOICE_NO &&
           (e.op || "").toUpperCase() !== "DELETE",
  );
  rec("⛔⛔ ONE supplier bill, ONE voucher — the re-accept is refused",
      postedNow.length === 1, `${postedNow.length} purchase(s) numbered ${INVOICE_NO}`);
  rec("…and the refusal SAYS the bill is already posted",
      /already|accepted|posted/i.test(second), second.slice(0, 110));

  // ── 5. ⛔⛔ A FRESH BROWSER — THE ARM THE OTHER FOUR CANNOT REACH. ─────────
  //
  // Everything above runs in the browser that POSTED. Its session memo, its
  // durable memo and its pending-confirm queue are all in this profile, so all
  // four arms above would be green even if the server knew nothing at all. The
  // case §7.3 B was actually about is the OTHER machine: a colleague's laptop,
  // inside the window where the confirm is owed and nobody here can drive it.
  //
  // Everything that could stop it is deliberately removed:
  //   · a SECOND browser context — no localStorage, so no memo and no queue;
  //   · a NUMBERLESS bill — so the day book (the one client layer that is not
  //     per-profile) has nothing to match on, exactly as when the OCR fails to
  //     read a supplier's invoice number;
  //   · the claim LEASE IS WAITED OUT — otherwise the refusal would come from
  //     layer 5 and this arm would prove the reservation, not the door;
  //   · and insightd is still `ready`, because this confirm is killed too.
  //
  // ⛔ AND THE ASSERTION IS ON THE WIRE, NOT ON THE COUNT ALONE. "one voucher"
  // can be true because something refused EARLY; this arm asserts that the POST
  // was actually made and that CORED answered 409 `draft_already_accepted` —
  // which no client layer can produce. Against `git show HEAD:` that POST is a
  // 201 and the tenant ends up with TWO purchases from one bill.
  await page.route("**/drafts/*/accepted", (route) => route.abort("failed"));
  const before2 = new Set((await listDrafts()).map((d) => d.draft_id));
  await openInbox();
  await page.getByTestId("inbox-fixture").fill(
    JSON.stringify(fixture({ no: "", date: NUMBERLESS_DATE, taxable: "4321.0000" })),
  );
  await page.waitForTimeout(300);
  await page.getByTestId("inbox-create").click();
  await page.waitForTimeout(5000);
  const after2 = await listDrafts();
  const fresh2 = after2.filter((d) => !before2.has(d.draft_id));
  if (fresh2.length !== 1) {
    throw new Error(
      `arm 5 setup: expected exactly ONE new numberless draft, got ${fresh2.length}. ` +
      `POST /drafts responses: ${JSON.stringify(refused.slice(-2))}`,
    );
  }
  const DRAFT2 = fresh2[0].draft_id;
  const purchasesOn = async (date) => {
    const d = await cored(`/day-book?from=${date}&to=${date}`);
    return (d.body.entries ?? []).filter(
      (e) => e.class === "Purchase" && e.party === "Steel Suppliers" &&
             (e.op || "").toUpperCase() !== "DELETE",
    );
  };

  await openInbox();
  await page.locator('[role="option"]').nth(
    after2.findIndex((d) => d.draft_id === DRAFT2),
  ).click();
  await page.waitForTimeout(3500);
  await press("Control+a", 8000);
  rec("arm 5 setup: the numberless bill posted from browser 1",
      (await purchasesOn(NUMBERLESS_DATE)).length === 1,
      `${(await purchasesOn(NUMBERLESS_DATE)).length} purchase(s) on ${NUMBERLESS_DATE}`);
  const lost = await cored(`/drafts/${encodeURIComponent(DRAFT2)}`);
  rec("arm 5 setup: its confirm was lost too — insightd still offers it",
      lost.body?.status !== "accepted", `status=${lost.body?.status}`);

  // WAIT THE LEASE OUT. Browser 1 claimed the draft and the confirm that would
  // have cleared the claim never landed, so the reservation stands for its full
  // TTL (insightd's store.CLAIM_TTL_SECONDS = 120). Without this wait the fresh
  // browser is refused by the CLAIM, which is a different door.
  const leaseGone = await (async () => {
    for (let i = 0; i < 60; i++) {
      const r = await cored(`/drafts/${encodeURIComponent(DRAFT2)}`);
      const exp = r.body?.claim_expires_at;
      if (!exp || Date.parse(exp) <= Date.now()) return true;
      await new Promise((res) => setTimeout(res, 5000));
    }
    return false;
  })();
  rec("arm 5 setup: the posting claim has lapsed, so layer 5 cannot answer",
      leaseGone, `claim_expires_at=${(await cored(`/drafts/${encodeURIComponent(DRAFT2)}`)).body?.claim_expires_at}`);

  // THE SECOND MACHINE.
  const other = await openApp({ company: CO, headless: true });
  try {
    const otherPage = other.page;
    const posts = [];
    otherPage.on("response", async (r) => {
      if (r.request().method() !== "POST") return;
      if (!/\/vouchers\/purchase$/.test(new URL(r.url()).pathname)) return;
      posts.push({ status: r.status(), body: (await r.text().catch(() => "")).slice(0, 500) });
    });

    const otherQueue = await otherPage.evaluate(
      (k) => globalThis.localStorage.getItem(k), QUEUE_KEY,
    );
    rec("⛔ the second machine holds NO pending-confirm queue at all",
        otherQueue === null || Object.keys(JSON.parse(otherQueue)).length === 0,
        `queue=${String(otherQueue).slice(0, 80)}`);

    const listB = await listDrafts();
    await other.toGateway();
    await other.openFromGateway("Bill Inbox", 3000);
    await otherPage.locator('[role="option"]').nth(
      listB.findIndex((d) => d.draft_id === DRAFT2),
    ).click();
    await otherPage.waitForTimeout(3500);
    await otherPage.keyboard.press("Control+a");
    await otherPage.waitForTimeout(8000);

    const otherStatus = (await otherPage.locator('[role="status"]').first()
      .innerText().catch(() => "")).replace(/\s+/g, " ").trim();

    // ⛔⛔ THE WHOLE POINT: the POST HAPPENED and CORED refused it.
    rec("⛔⛔ the second machine DID attempt the voucher POST",
        posts.length === 1, `${posts.length} POST(s) to /vouchers/purchase`);
    rec("⛔⛔ …and cored refused it 409 draft_already_accepted — IN the voucher's transaction",
        posts[0]?.status === 409 &&
          /"code"\s*:\s*"draft_already_accepted"/.test(posts[0]?.body ?? ""),
        `${posts[0]?.status} ${(posts[0]?.body ?? "").slice(0, 160)}`);
    rec("⛔⛔ ONE supplier bill, ONE voucher — from a browser that knew nothing",
        (await purchasesOn(NUMBERLESS_DATE)).length === 1,
        `${(await purchasesOn(NUMBERLESS_DATE)).length} purchase(s) on ${NUMBERLESS_DATE}`);
    rec("…and the operator on that machine is TOLD, not left guessing",
        /already|accepted|posted/i.test(otherStatus), otherStatus.slice(0, 130));

    // ⛔ AND THE BILL IS NOT WEDGED. The refusal teaches insightd the fact the
    // first browser never delivered, so the inbox stops offering a bill that can
    // no longer be posted — otherwise this fix would just move the defect.
    //
    // ⚠️ GATED ON THE REFUSAL HAVING HAPPENED, AND THE GATE WAS ADDED BECAUSE THIS
    // ARM WAS MEASURED GREEN FOR THE WRONG REASON. On the HEAD-equivalent run (the
    // client not sending `draft_id`) the second browser SUCCEEDS, and its own
    // ordinary post-accept confirm marks the draft `accepted` — so "the inbox stops
    // offering it" was true while the three arms above it were red and the tenant
    // held TWO vouchers for one bill. Reconciliation only means anything here if it
    // followed a REFUSAL. Same lesson as "nothing is owed any more" one arm-set up.
    await otherPage.waitForTimeout(2000);
    const healed = await cored(`/drafts/${encodeURIComponent(DRAFT2)}`);
    rec("⛔ the refusal RECONCILED insightd — the inbox stops offering it",
        posts[0]?.status === 409 && healed.body?.status === "accepted",
        `post=${posts[0]?.status} status=${healed.body?.status}`);
  } finally {
    await other.browser.close();
  }
} finally {
  await app.browser.close();
}
process.exit(summary() > 0 ? 1 : 0);
