/**
 * L21 — D85: the persisted redaction tag was a CONSTANT, so a supplier's SECOND
 * bill on one date was refused as a duplicate of the first.
 *
 * ⛔⛔ THE TWO HALVES ARE BOTH REQUIRED, AND THE SECOND IS THE ONE THAT MAKES THIS
 * A PROOF. Half one: two DIFFERENT bare-PAN invoice numbers from ONE supplier on ONE
 * date both post. Half two: the SAME number is still refused. Without half two the
 * "fix" could simply be "the duplicate guard was deleted", which is the money defect
 * the five-layer guard exists to stop — and this probe would have cheered.
 *
 * ⛔ BARE PANs, NOT `INV/<PAN>/77`. `scrub()` replaces only the MATCHED SPAN and keeps
 * the surrounding text, so `INV/ABCDE1234F/77` and `INV/ZZZZZ9999Z/88` never collided
 * in the first place. Total collapse happens only when the whole invoice number IS the
 * entity — which is exactly what the L11 fixture carries. A probe built on the wrapped
 * form passes VACUOUSLY, before and after the fix.
 *
 * ⛔ AND THE CONTROL BILL DIFFERS IN AMOUNT ON PURPOSE. The five layers are ordered,
 * and layers 1 and 3 key on the draft's own CONTENT: an identical re-upload is refused
 * by the session memo before the DAY BOOK is ever asked, and the day book is the layer
 * D85 is about. Same party, same date, same (redacted) number, DIFFERENT amount is the
 * operator's re-keyed-from-a-fresh-scan case, and it is the only shape that reaches
 * layer 2. A probe that re-uploads a byte-identical bill measures layer 1 and reports
 * on layer 2.
 *
 * ⛔ FOOTED AGAINST A DIFFERENT HANDLER (§2.2). The day book and
 * `/outstandings?kind=PAYABLE` are two handlers; both must show the two bills as two.
 * The day book alone would only prove the day book agrees with itself.
 *
 * ⚠️ IT POSTS INTO THE DEMO TENANT, deliberately and in the shape drive-l11-ai.mjs
 * already established: it creates NO master (every ledger id in the fixture is read
 * out of this tenant's own chart), it dates its bills ON OR BEFORE TODAY so
 * Outstandings' today-bound does not diverge from the Balance Sheet (that divergence
 * is D86, not this probe's to manufacture), and the accepts go through F9 so
 * `defaultDraftBillRefs` gives each bill its own reference — an ON-ACCOUNT balance is
 * what took `verify:demo` from 34/34 to 33/34 with a MONEY failure once before.
 * ⛔ Re-run `pnpm verify:demo` after this probe. It must still be 34/34.
 *
 * RUN:  CO="$(cat /tmp/drive-company-id)" node frontend/scripts/drive-l21-d85.mjs
 */
import { openApp, rec, summary } from "./audit-harness.mjs";

const CORED = "http://localhost:8087";
const CO = process.env.CO;
if (!CO) throw new Error("CO is required — the demo company id from /tmp/drive-company-id");

const TOKEN = await (async () => {
  const r = await fetch(`${CORED}/auth/token`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    // ⚠️ browser-dev-owner, NOT a random subject: this probe is handed an EXISTING
    // tenant, and a fresh random subject is a member of nothing (§5.12).
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

/**
 * ⛔ THE CLIENT'S COPY OF insightd's TAG SHAPE, asserted rather than assumed.
 * `draftAccept.ts`'s `REDACTION_TAG_RE` is a second copy of a shape that lives in
 * `insightd/insightd/redaction/persistence.py`, and its own comment promises this
 * probe checks the two still agree. Keep them byte-identical.
 */
const REDACTION_TAG_RE = /\[[A-Z][A-Z_]* REDACTED [0-9a-f]+\]/;

// Two structurally valid PANs. ⛔ BARE — the whole invoice number IS the entity.
const PAN_A = "ABCDE1234F";
const PAN_B = "ZZZZZ9999Z";

const ledgerIndex = await (async () => {
  const r = await cored("/ledgers");
  if (r.status !== 200) throw new Error(`cannot read this tenant's ledgers: HTTP ${r.status}`);
  return new Map((r.body.ledgers ?? []).map((l) => [l.name, l]));
})();
const needLedger = (name, wantState) => {
  const l = ledgerIndex.get(name);
  if (!l) throw new Error(`this tenant has no "${name}" ledger — reseed before driving`);
  if (wantState !== undefined && (l.state_code || "") !== wantState) {
    throw new Error(`"${name}" is in state ${l.state_code || "<unset>"}, not ${wantState}`);
  }
  return l.id;
};
// Intra-state on purpose (29 == the demo company's own state), so the derivation is
// CGST+SGST and the legs this probe asserts on by NAME are the input-tax ones.
const STEEL = needLedger("Steel Suppliers", "29");
const PURCHASES = needLedger("Raw Material Purchases");

/**
 * ⛔ ONE DATE, SHARED BY ALL THREE BILLS — that is the point of the row: the day-book
 * check keys on (class, date, voucher_no, party), so a probe that varies the date
 * cannot reach it at all. Pick an August date with no existing Steel Suppliers
 * purchase, ON OR BEFORE TODAY (see the header for why the future is off limits).
 */
const INVOICE_DATE = await (async () => {
  const day = await cored("/day-book?from=2026-08-01&to=2026-08-31");
  if (day.status !== 200) throw new Error(`day book: HTTP ${day.status} ${day.text}`);
  const taken = new Set(
    (day.body.entries ?? [])
      .filter((e) => e.party === "Steel Suppliers" && e.class === "Purchase")
      .map((e) => e.date),
  );
  const today = new Date().toISOString().slice(0, 10);
  for (let d = 1; d <= 28; d++) {
    const date = `2026-08-${String(d).padStart(2, "0")}`;
    if (date > today) break;
    if (!taken.has(date)) return date;
  }
  throw new Error("no free August date on or before today for Steel Suppliers — reseed");
})();
console.log(`\n── tenant ${CO}  ·  one date for all three bills: ${INVOICE_DATE} ──`);

const ocrLine = (text, y, conf) => ({ text, bbox: [40, y, 1200, y + 22], confidence: conf });

/** A complete intra-state @18% purchase fixture whose invoice number IS a bare PAN. */
const fixture = (pan, taxable) => {
  const total = (Number(taxable) * 1.18).toFixed(4);
  return {
    // ⛔ THE KEY IS `supplier_ledger_id`, FLAT. `FakeClassifier._match_supplier` reads
    // `spec["supplier_ledger_id"]`; a nested `{supplier: {ledger_id}}` is silently no
    // match, so `supplier_state` stays unknown, the mapper emits ZERO input-tax legs and
    // cored refuses the accept with "CGST posting 0.0000 != derived 90.0000". That reads
    // like D18/D19 having regressed and is entirely this fixture's fault.
    classify: { doc_type: "purchase_invoice", is_purchase: true, supplier_ledger_id: STEEL },
    input: {
      // ⛔ A LIST OF PAGE OBJECTS, NOT A COUNT. `_normalized_from_fixture` iterates
      // `input.pages`, so `pages: 1` raises "'int' object is not iterable" inside the
      // handler and cored answers 500 — a probe error that reads like a server defect.
      pages: [{ png_b64: "", width: 1240, height: 1754, dpi: 200 }],
      ocr: {
        lines: [
          ocrLine("TAX INVOICE", 10, 0.99),
          ocrLine(`Invoice No: ${pan}`, 40, 0.98),
          ocrLine(`Date: ${INVOICE_DATE}`, 70, 0.98),
          ocrLine("Supplier: Steel Suppliers", 100, 0.97),
          ocrLine("Place of Supply: 29", 130, 0.97),
          ocrLine(`MS Angle HSN 7308 qty 10.0000 rate ${taxable} ${taxable} @ 18.0000%`, 170, 0.96),
        ],
        mean_confidence: 0.968,
      },
      inferred: {
        // ⛔ THE INVOICE NUMBER IS THE WHOLE PAN. Anything wrapping it survives
        // `scrub()` and the two numbers would differ without the fix (see header).
        invoice_no: pan,
        invoice_date: INVOICE_DATE,
        place_of_supply_state_code: "29",
        reverse_charge: false,
        exempt: false,
        line_items: [
          {
            description: "MS Angle for site",
            hsn: "7308",
            qty: "10.0000",
            rate: (Number(taxable) / 10).toFixed(4),
            taxable_value: Number(taxable).toFixed(4),
            gst_rate_pct: "18.0000",
            ledger_id: PURCHASES,
            ledger_name: "Raw Material Purchases",
            ledger_confidence: 0.95,
          },
        ],
        totals: { taxable: Number(taxable).toFixed(4), invoice_total: total },
      },
    },
  };
};

const listDrafts = async () => {
  // ⛔ THROUGH CORED, WITH THE BEARER (D49). insightd 401s anything else.
  const r = await cored("/drafts");
  if (r.status !== 200) throw new Error(`cannot list drafts through cored: HTTP ${r.status}`);
  return r.body;
};

/**
 * ⛔⛔ THE LIST CARRIES NO `body` AT ALL — MEASURED, AFTER THIS PROBE READ IT ANYWAY.
 * `GET /drafts` returns SUMMARIES: `draft_id · party · grand_total · status · confidence
 * · accepted_voucher_no`. The first run of this probe read `draft.body.voucher_no` off a
 * summary, got `undefined`, defaulted it to `""` — and then the determinism arm compared
 * `"" === ""` and reported PASS. That is §5.7 exactly: a projection that does not exist,
 * COMPARED instead of THROWN on. So this THROWS, and only `GET /drafts/{id}` is trusted.
 */
const voucherNoOf = async (draftId) => {
  const r = await cored(`/drafts/${draftId}`);
  if (r.status !== 200) throw new Error(`GET draft ${draftId}: HTTP ${r.status} ${r.text}`);
  const no = r.body?.body?.voucher_no;
  if (typeof no !== "string" || no === "") {
    throw new Error(
      `draft ${draftId} has no voucher_no on its body (got ${JSON.stringify(no)}) — ` +
      "the fixture's invoice_no never reached the draft, so every arm below would be vacuous",
    );
  }
  return no;
};

const app = await openApp({ company: CO, headless: true });
const { page, body, press, toGateway, openFromGateway } = app;

/**
 * ⚠️ CAPTURE THE BODY OF ANY REFUSED DRAFT POST. The first run of this probe emitted
 * one unexplained `POST /drafts 403` per run alongside its three 201s, and an
 * unexplained refusal on the route a lane is driving is exactly the thing that must
 * not be waved through as noise. `net` records the STATUS only, so the body is read
 * here and asserted on below.
 */
const refusedPosts = [];
page.on("response", async (r) => {
  if (r.request().method() !== "POST") return;
  if (!/\/drafts$/.test(new URL(r.url()).pathname)) return;
  if (r.status() < 400) return;
  refusedPosts.push({ status: r.status(), body: (await r.text().catch(() => "")).slice(0, 300) });
});
const status = async () =>
  (await page.locator('[role="status"]').first().innerText().catch(() => ""))
    .replace(/\s+/g, " ")
    .trim();

/** Upload one fixture through the Bill Inbox and return its draft as insightd stored it. */
const upload = async (fx) => {
  const before = new Set((await listDrafts()).map((d) => d.draft_id));
  await toGateway();
  await openFromGateway("Bill Inbox", 2200);
  await page.getByTestId("inbox-fixture").fill(JSON.stringify(fx));
  await page.waitForTimeout(300);
  await page.getByTestId("inbox-create").click();
  await page.waitForTimeout(4000);
  const after = await listDrafts();
  const idx = after.findIndex((d) => !before.has(d.draft_id));
  if (idx < 0) throw new Error("no NEW draft appeared after the fixture upload");
  return { draft: after[idx], index: idx, count: after.length };
};

/** Open a draft row by its index in insightd's own list and press Ctrl+A. */
const acceptRow = async (index) => {
  await toGateway();
  await openFromGateway("Bill Inbox", 2200);
  // ⚠️ BY INDEX FROM THE API, NEVER BY hasText+first(). The inbox row label is
  // `party · total · status · pct` and three Steel Suppliers drafts share a party:
  // `.first()` would silently measure whichever one rendered first.
  const rows = page.locator('[role="option"]');
  await rows.nth(index).click();
  await page.waitForTimeout(3000);
  await press("Control+a", 7000);
  return { statusText: await status() };
};

const rowIndexOf = async (draftId) =>
  (await listDrafts()).findIndex((d) => d.draft_id === draftId);

const dayBook = async () => {
  const r = await cored(`/day-book?from=${INVOICE_DATE}&to=${INVOICE_DATE}`);
  if (r.status !== 200) throw new Error(`day book: HTTP ${r.status} ${r.text}`);
  return (r.body.entries ?? []).filter(
    (e) => e.party === "Steel Suppliers" && e.class === "Purchase" &&
           (e.op || "").toUpperCase() !== "DELETE",
  );
};

try {
  // =========================================================================
  // WIRE — what insightd actually stores in the value that PERSISTS.
  // =========================================================================
  const A = await upload(fixture(PAN_A, "1000"));
  const noA = await voucherNoOf(A.draft.draft_id);

  rec("D85: PRECONDITION — the running insightd redacts a bare-PAN invoice number at all",
      REDACTION_TAG_RE.test(noA) && !noA.includes(PAN_A),
      `draft A voucher_no = ${JSON.stringify(noA)}`);

  // ⛔ THE DRIFT CHECK the client's own comment promises. A regex that stops matching
  // the real tag costs the operator the accurate sentence and nothing else — but it
  // costs it SILENTLY, which is why it is asserted here against a real draft.
  rec("D85: the client's REDACTION_TAG_RE still matches the tag insightd writes",
      REDACTION_TAG_RE.test(noA) && /^\[PAN REDACTED [0-9a-f]{4}\]$/.test(noA),
      `${noA} vs ${REDACTION_TAG_RE}`);

  const B = await upload(fixture(PAN_B, "2000"));
  const noB = await voucherNoOf(B.draft.draft_id);

  // =========================================================================
  // ⛔⛔ THE ORACLE, HALF ONE. Red before this lane: both were "[PAN REDACTED]".
  // =========================================================================
  rec("D85: ⛔⛔ two DIFFERENT bare-PAN invoice numbers do NOT collapse to one voucher number",
      noA !== noB && REDACTION_TAG_RE.test(noB) && !noB.includes(PAN_B),
      `A=${noA}  B=${noB}`);

  // The re-keyed bill: SAME number as A, different amount so it reaches layer 2.
  const Aprime = await upload(fixture(PAN_A, "1500"));
  const noAprime = await voucherNoOf(Aprime.draft.draft_id);

  // =========================================================================
  // ⛔ CROSS-REQUEST DETERMINISM. This is the arm no in-process test can give, and
  // a mutation proof showed the pytest version of it passing through a per-process
  // salt. Three separate insightd requests; A and A' carried the same number.
  // =========================================================================
  rec("D85: ⛔ the SAME number redacts to the SAME tag on a LATER request (unsalted)",
      noAprime === noA,
      `A=${noA} (request 1)  A'=${noAprime} (request 3)`);

  // =========================================================================
  // BROWSER — accept both bills. THE MONEY ARM.
  // =========================================================================
  const acceptA = await acceptRow(await rowIndexOf(A.draft.draft_id));
  rec("D85: bill A accepts",
      /Saved/i.test(acceptA.statusText),
      acceptA.statusText.slice(0, 200) || "(empty status line)");

  const afterA = await dayBook();
  const acceptB = await acceptRow(await rowIndexOf(B.draft.draft_id));
  rec("D85: ⛔⛔ bill B ALSO accepts — the second bill from one supplier on one date POSTS",
      /Saved/i.test(acceptB.statusText) && !/already on the books/i.test(acceptB.statusText),
      acceptB.statusText.slice(0, 240) || "(empty status line)");

  const afterB = await dayBook();
  rec("D85: ⛔ the books hold TWO purchases for that supplier on that date, not one",
      afterA.length === 1 && afterB.length === 2,
      `after A: ${afterA.length} · after B: ${afterB.length}`);

  // ⛔ SIDES AND LEDGER NAMES, NEVER TOTALS.
  const legsOf = (e) => (e.postings ?? []).map((p) => `${p.dr_cr}:${p.ledger}`).sort();
  const numbers = afterB.map((e) => e.voucher_no).sort();
  rec("D85: ⛔ two DISTINCT voucher numbers on the books, each a tag with its own fingerprint",
      new Set(numbers).size === 2 && numbers.every((n) => REDACTION_TAG_RE.test(n)),
      numbers.join(" | "));
  rec("D85: ⛔ and each bill left its OWN party CREDIT leg and its own input-tax DEBITs",
      afterB.length === 2 &&
        afterB.every((e) => {
          const legs = legsOf(e);
          return legs.includes("C:Steel Suppliers") &&
                 legs.includes("D:Raw Material Purchases") &&
                 legs.includes("D:Input CGST") &&
                 legs.includes("D:Input SGST") &&
                 !legs.some((l) => /IGST/.test(l));
        }),
      afterB.map((e) => `${e.voucher_no} -> ${legsOf(e).join(",")}`).join("  ·  "));

  // =========================================================================
  // ⛔ FOOTED AGAINST A HANDLER THAT IS NOT THE DAY BOOK (§2.2). Outstandings reads
  // `bill_allocation`, not the day book's postings, and it names each bill by the
  // REF NAME cored generated from the voucher number — `Purchase/2026-27/<number>`.
  // ⚠️ MEASURED, NOT GUESSED: the rows are `parties[].ledger` + `parties[].bills[]`,
  // and a bill carries `ref_name`/`due_date`/`pending`/`slab` and NO invoice date. A
  // first version of this arm filtered on `b.date` and reported "none of 6" — an
  // absent projection again (§5.7).
  //
  // ⛔⛔ AND THIS IS WHERE THE COLLAPSE WAS DOING A SECOND KIND OF DAMAGE. The
  // generated bill-ref name embeds the redacted number, so before this lane BOTH
  // bills wanted the ref name `Purchase/2026-27/[PAN REDACTED]` — one namespace, two
  // bills, which is the shape D83 is about. This tenant still carries one such row
  // from an earlier probe run, and the two new ones are distinct.
  // =========================================================================
  const out = await cored("/outstandings?kind=PAYABLE");
  const steel = (out.body?.parties ?? []).find((p) => p.ledger === "Steel Suppliers");
  const refNames = (steel?.bills ?? []).map((b) => b.ref_name ?? "");
  // ⚠️ COUNTS, NOT AN EXACT NUMBER: this probe posts into the SHARED demo tenant, so a
  // previous run's two bills are still here and an `=== 2` would be stable only by
  // accident. The property that matters is that every generated ref name is DISTINCT
  // and that both of this run's tags are named — a collapsed tag would have put two
  // bills in one ref-name namespace, which is precisely what
  // `resolveGeneratedBillRefNames` then papers over by appending `(xxxxxxxx)`.
  rec("D85: ⛔ Outstandings (a DIFFERENT handler, a different table) names the two bills separately",
      out.status === 200 &&
        new Set(refNames).size === refNames.length &&
        refNames.some((n) => n.includes(noA)) &&
        refNames.some((n) => n.includes(noB)) &&
        noA !== noB,
      out.status === 200
        ? `Steel Suppliers bill refs: ${refNames.join(" | ") || "(none)"}`
        : `HTTP ${out.status} ${out.text.slice(0, 140)}`);

  // ⛔⛔ A SECOND DEFECT'S WORTH OF EVIDENCE, RECORDED WHERE IT WAS FOUND. cored mints a
  // generated bill reference as `Purchase/<fy>/<voucher_no>`, so while the tag was a
  // CONSTANT every bill whose invoice number was PII-shaped competed for ONE ref name
  // per party. `resolveGeneratedBillRefNames` renames the loser to `… (xxxxxxxx)`, which
  // is why the collapse never showed up as an error here — it showed up as a supplier's
  // bills wearing suffixes nobody typed. This is D83's namespace, and the fingerprint
  // removes the collision at the source for this class of number.
  const suffixed = refNames.filter((n) => /\(\w{8}\)$/.test(n));
  console.log(
    `   ⚠️ generated bill refs carrying a discriminating suffix: ${suffixed.length}` +
    (suffixed.length ? ` — ${suffixed.join(" | ")}` : ""),
  );

  // ⛔ D16 STILL HOLDS. The fingerprint is not a leak of the value.
  const asText = JSON.stringify(await dayBook());
  rec("D85: ⛔ neither PAN reached the books — the fingerprint is not the value",
      !asText.includes(PAN_A) && !asText.includes(PAN_B),
      `PAN_A in books: ${asText.includes(PAN_A)} · PAN_B in books: ${asText.includes(PAN_B)}`);

  // =========================================================================
  // ⛔⛔ THE ORACLE, HALF TWO — THE CONTROL. Without this the "fix" could be
  // "the duplicate guard was deleted" and every arm above would still be green.
  // =========================================================================
  const acceptAprime = await acceptRow(await rowIndexOf(Aprime.draft.draft_id));
  const refusal = acceptAprime.statusText;
  const afterAprime = await dayBook();

  rec("D85: ⛔⛔ CONTROL — the SAME supplier number is STILL REFUSED, and posts nothing",
      /already on the books/i.test(refusal) && afterAprime.length === 2,
      `${afterAprime.length} purchases on ${INVOICE_DATE} · ${refusal.slice(0, 200)}`);

  // ⛔ §5.19 — ASSERT ON THE ADVICE, NOT ON THE PRESENCE OF A REFUSAL. The old
  // sentence told the operator to "key its own supplier invoice number", which for a
  // redacted number was a dead end: every number they keyed landed on the same token.
  rec("D85: ⛔⛔ …and its ADVICE names the fingerprint instead of the dead end it used to give",
      /fingerprint of the removed number/i.test(refusal) &&
        /SAME supplier number/.test(refusal) &&
        !/key its own supplier invoice number/.test(refusal),
      refusal.slice(-260) || "(empty status line)");

  // ⛔ §5.8 — ASK WHAT A SECOND KEYPRESS DOES. The refusal is not the end of the
  // story; the remedy is. A second Ctrl+A must refuse again and post nothing.
  await press("Control+a", 6000);
  const second = await status();
  const afterSecond = await dayBook();
  rec("D85: ⛔ a SECOND Ctrl+A on the refused frame refuses again and still posts nothing",
      /already on the books/i.test(second) && afterSecond.length === 2,
      `${afterSecond.length} purchases · ${second.slice(0, 160)}`);

  // ⚠️ THE ONE REFUSED DRAFT POST PER RUN IS EXPLAINED, AND IT IS THE PRODUCT WORKING.
  // The first run of this probe logged an unexplained `POST /drafts 403` beside its
  // three 201s. Read the body and it is `{"code":"STEPUP_REQUIRED"}`: this session
  // signed in with a PASSWORD, and the first mutating write demands a WebAuthn
  // step-up, which the client performs and then retries. ⛔ Asserted rather than
  // filtered out, because "one 403 on the route this lane drives" must never be
  // waved through as noise — if a SECOND kind of refusal ever appears here, or the
  // step-up stops being satisfied, this arm is what says so.
  rec("D85: ⚠️ the only refused draft POST is the WebAuthn STEP-UP, which the client then satisfies",
      refusedPosts.length === 1 &&
        refusedPosts[0].status === 403 &&
        /STEPUP_REQUIRED/.test(refusedPosts[0].body),
      refusedPosts.map((r) => `HTTP ${r.status}: ${r.body}`).join("  ·  ") || "no refused POST /drafts");

  const seen = (await body()).replace(/\s+/g, " ");
  rec("D85: the screen is not a crash or a stuck spinner",
      !/\bNaN\b/.test(seen) && !/Loading…$/.test(seen),
      "no NaN / spinner-stuck state");
} finally {
  console.log("\nNON-GET WIRE:", app.net.filter((w) => !/webauthn/.test(w) && !w.startsWith("GET ")).join(" · ") || "(none)");
  await app.browser?.close?.();
}

process.exit(summary() > 0 ? 1 : 0);
